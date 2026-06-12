package api

import (
	"context"
	"fmt"
	"goprint/config"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pendingJobTracker 跟踪所有 pending 的任务
type pendingJobTracker struct {
	mu    sync.RWMutex
	jobs  map[string]*trackedJob
	store *bitableJobStore
	cfg   *config.Config
}

type trackedJob struct {
	JobID        string
	PrinterID    string
	RecordStatus string
	LastCheck    time.Time
}

var (
	jobTracker *pendingJobTracker
	once       sync.Once
)

const (
	staleBitableJobCleanupInterval = 47*time.Minute + 17*time.Second
	staleBitableJobMaxAge          = 12 * time.Hour
)

func InitJobStatusPoller(cfg *config.Config) *pendingJobTracker {
	return initJobStatusPoller(cfg)
}

// initJobStatusPoller 初始化任务状态轮询器
func initJobStatusPoller(cfg *config.Config) *pendingJobTracker {
	once.Do(func() {
		tracker := &pendingJobTracker{
			jobs: make(map[string]*trackedJob),
			cfg:  cfg,
		}

		store, err := newBitableJobStore(cfg)
		if err != nil {
			log.Printf("[job-poller] bitable store init failed: %v", err)
			return
		}
		tracker.store = store
		jobTracker = tracker
		tracker.restorePendingJobs()

		// 启动后台轮询 goroutine
		go tracker.pollLoop()
	})

	return jobTracker
}

// AddPendingJob 记录一个 pending 任务
func (t *pendingJobTracker) AddPendingJob(jobID, printerID string) {
	t.AddPendingJobWithStatus(jobID, printerID, "pending")
}

func (t *pendingJobTracker) AddPendingJobWithStatus(jobID, printerID, recordStatus string) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.jobs[jobID] = &trackedJob{
		JobID:        jobID,
		PrinterID:    printerID,
		RecordStatus: normalizedJobStatus(recordStatus),
		LastCheck:    time.Now(),
	}

	log.Printf("[job-poller] tracked pending job job_id=%s printer=%s record_status=%s", jobID, printerID, recordStatus)
}

func (t *pendingJobTracker) restorePendingJobs() {
	if t == nil || t.store == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jobs, err := t.store.ListTrackableJobs(ctx)
	if err != nil {
		log.Printf("[job-poller] failed to restore pending jobs: %v", err)
		return
	}

	if len(jobs) == 0 {
		log.Printf("[job-poller] no pending jobs to restore")
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for _, j := range jobs {
		t.jobs[j.JobID] = &trackedJob{
			JobID:        j.JobID,
			PrinterID:    j.PrinterID,
			RecordStatus: j.Status,
			LastCheck:    time.Now(),
		}
	}
	log.Printf("[job-poller] restored pending jobs count=%d", len(jobs))
}

// pollLoop 后台轮询循环，定期检查任务状态
func (t *pendingJobTracker) pollLoop() {
	statusTicker := time.NewTicker(30 * time.Second) // 检查间隔
	defer statusTicker.Stop()

	staleTicker := time.NewTicker(staleBitableJobCleanupInterval)
	defer staleTicker.Stop()

	for {
		select {
		case <-statusTicker.C:
			t.checkPendingJobs()
		case <-staleTicker.C:
			t.completeStaleBitableJobs()
		}
	}
}

// checkPendingJobs 检查所有 pending 任务的状态
func (t *pendingJobTracker) checkPendingJobs() {
	if t == nil || t.store == nil {
		return
	}

	t.mu.RLock()
	jobsCopy := make(map[string]*trackedJob)
	for k, v := range t.jobs {
		jobsCopy[k] = v
	}
	t.mu.RUnlock()

	for jobID, tracked := range jobsCopy {
		// 获取任务状态
		cupsJobID, err := strconv.Atoi(strings.TrimSpace(jobID))
		if err != nil {
			log.Printf("[job-poller] invalid job_id format job_id=%s err=%v", jobID, err)
			t.removePendingJob(jobID)
			continue
		}

		printerCfg, err := resolvePrinter(tracked.PrinterID)
		if err != nil {
			log.Printf("[job-poller] failed to resolve printer printer_id=%s err=%v", tracked.PrinterID, err)
			continue
		}

		cupsClient, _, err := newCupsClientForPrinter(printerCfg)
		if err != nil {
			log.Printf("[job-poller] failed to create cups client printer_id=%s err=%v", tracked.PrinterID, err)
			continue
		}

		job, err := cupsClient.GetPrintJobDetails(cupsJobID)
		if err != nil {
			log.Printf("[job-poller] failed to get job details job_id=%s err=%v", jobID, err)
			continue
		}

		oldStatus := "checking"
		if tracked != nil {
			oldStatus = fmt.Sprintf("job_id=%s printer=%s", tracked.JobID, tracked.PrinterID)
		}

		log.Printf("[job-poller] status check %s status=%s", oldStatus, job.Status)

		// 如果任务不再是 pending，更新表格并移除跟踪
		if job.Status != "pending" && job.Status != "held" && job.Status != "processing" {
			if tracked.RecordStatus == "pending_manual_continue" && job.Status == "completed" {
				log.Printf("[job-poller] manual duplex first pass completed job_id=%s, keep record waiting for continue", jobID)
				t.removePendingJob(jobID)
				continue
			}

			log.Printf("[job-poller] job completed job_id=%s old_status=pending new_status=%s", jobID, job.Status)

			// 重试更新状态
			updateSucceeded := false
			for attempt := 1; attempt <= 3; attempt++ {
				updateCtx, updateCancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := t.store.UpdateJobStatus(updateCtx, jobID, job.Status)
				updateCancel()

				if err != nil {
					log.Printf("[job-poller] update attempt %d/3 failed job_id=%s err=%v", attempt, jobID, err)
					if attempt < 3 {
						time.Sleep(time.Duration(attempt) * time.Second)
						continue
					}
				} else {
					if attempt > 1 {
						log.Printf("[job-poller] updated job status after retry job_id=%s status=%s attempts=%d", jobID, job.Status, attempt)
					} else {
						log.Printf("[job-poller] updated job status in bitable job_id=%s status=%s", jobID, job.Status)
					}
					updateSucceeded = true
					break
				}
			}

			if !updateSucceeded {
				log.Printf("[job-poller] failed to update job status after 3 attempts, keeping in tracker job_id=%s", jobID)
				// 不从跟踪列表移除，下次继续尝试
				continue
			}

			t.removePendingJob(jobID)
		}
	}
}

// removePendingJob 移除对 pending 任务的跟踪
func (t *pendingJobTracker) removePendingJob(jobID string) {
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.jobs[jobID]; exists {
		delete(t.jobs, jobID)
		log.Printf("[job-poller] stopped tracking job_id=%s", jobID)
	}
}

func (t *pendingJobTracker) RemovePendingJob(jobID string) {
	t.removePendingJob(jobID)
}

func (t *pendingJobTracker) completeStaleBitableJobs() {
	if t == nil || t.store == nil {
		return
	}

	cutoff := time.Now().Add(-staleBitableJobMaxAge)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jobs, err := t.store.ListStaleIncompleteJobs(ctx, cutoff)
	if err != nil {
		log.Printf("[job-poller] failed to list stale pending bitable jobs: %v", err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	for _, job := range jobs {
		updateCtx, updateCancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := t.store.UpdateJobStatus(updateCtx, job.JobID, "completed")
		updateCancel()
		if err != nil {
			log.Printf("[job-poller] failed to complete stale bitable job job_id=%s status=%s submitted_at=%s err=%v",
				job.JobID, job.Status, job.SubmittedAt.Format(time.RFC3339), err)
			continue
		}

		log.Printf("[job-poller] completed stale bitable job job_id=%s status=%s submitted_at=%s",
			job.JobID, job.Status, job.SubmittedAt.Format(time.RFC3339))
		t.removePendingJob(job.JobID)
	}
}

// GetPendingJobCount 获取正在跟踪的 pending 任务数
func (t *pendingJobTracker) GetPendingJobCount() int {
	if t == nil {
		return 0
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	return len(t.jobs)
}
