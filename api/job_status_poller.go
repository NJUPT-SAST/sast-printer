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
	JobID     string
	PrinterID string
	LastCheck time.Time
}

var (
	jobTracker *pendingJobTracker
	once       sync.Once
)

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
	if t == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.jobs[jobID] = &trackedJob{
		JobID:     jobID,
		PrinterID: printerID,
		LastCheck: time.Now(),
	}

	log.Printf("[job-poller] tracked pending job job_id=%s printer=%s", jobID, printerID)
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
			JobID:     j.JobID,
			PrinterID: j.PrinterID,
			LastCheck: time.Now(),
		}
	}
	log.Printf("[job-poller] restored pending jobs count=%d", len(jobs))
}

// pollLoop 后台轮询循环，定期检查任务状态
func (t *pendingJobTracker) pollLoop() {
	ticker := time.NewTicker(30 * time.Second) // 检查间隔
	defer ticker.Stop()

	for range ticker.C {
		t.checkPendingJobs()
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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

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
			log.Printf("[job-poller] job completed job_id=%s old_status=pending new_status=%s", jobID, job.Status)

			if err := t.store.UpdateJobStatus(ctx, jobID, job.Status); err != nil {
				log.Printf("[job-poller] failed to update job status job_id=%s err=%v", jobID, err)
			} else {
				log.Printf("[job-poller] updated job status in bitable job_id=%s status=%s", jobID, job.Status)
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

// GetPendingJobCount 获取正在跟踪的 pending 任务数
func (t *pendingJobTracker) GetPendingJobCount() int {
	if t == nil {
		return 0
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	return len(t.jobs)
}
