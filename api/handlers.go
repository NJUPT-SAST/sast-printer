package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goprint/config"
	"goprint/cups"

	"github.com/gin-gonic/gin"
)

func currentAuthUser(c *gin.Context) (feishuUserInfo, bool) {
	v, ok := c.Get("auth_user")
	if !ok {
		return feishuUserInfo{}, false
	}
	user, ok := v.(feishuUserInfo)
	if !ok {
		return feishuUserInfo{}, false
	}
	if user.UserID == "" && user.OpenID == "" && user.UnionID == "" {
		return feishuUserInfo{}, false
	}
	return user, true
}

func persistPrintJobToBitable(c *gin.Context, cfg *config.Config, record printJobRecord) {
	store, err := newBitableJobStore(cfg)
	if err != nil {
		log.Printf("[jobs] skip bitable persist: %v", err)
		return
	}

	if err := store.SaveJob(context.Background(), record); err != nil {
		log.Printf("[jobs] bitable persist failed job_id=%s printer=%s err=%v", record.JobID, record.PrinterID, err)
	}
}

// HealthCheck 健康检查接口
func HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"message": "GoPrint is running.",
	})
}

func applyCopiesMode(sourcePath string, copies int, collate bool) (string, error) {
	if copies <= 1 {
		return sourcePath, nil
	}

	if collate {
		return ApplyCollateCopies(sourcePath, copies)
	}

	return ApplyUncollatedCopies(sourcePath, copies)
}

// ListPrinters 列出所有可用打印机
func ListPrinters(c *gin.Context) {
	cfg, err := requireConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	configured := cfg.VisiblePrinters()
	printers := make([]gin.H, 0, len(configured))
	for _, printerCfg := range configured {
		cupsClient, printerName, err := newCupsClientForPrinter(printerCfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		printer, err := cupsClient.GetPrinterDetails(printerName)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":      "Cannot connect to configured printer",
				"printer_id": printerCfg.ID,
				"details":    err.Error(),
			})
			return
		}

		printer.ID = printerCfg.ID
		printers = append(printers, gin.H{
			"id":                  printer.ID,
			"name":                printer.Name,
			"description":         printer.Description,
			"status":              printer.Status,
			"model":               printer.Model,
			"location":            printer.Location,
			"duplex_mode":         printerCfg.NormalizedDuplexMode(),
			"reverse":             printerCfg.Reverse,
			"first_pass":          printerCfg.NormalizedFirstPass(),
			"pad_to_even":         printerCfg.PadToEvenEnabled(),
			"reverse_first_pass":  printerCfg.ReverseFirstPass,
			"reverse_second_pass": printerCfg.ReverseSecondPass,
			"rotate_second_pass":  printerCfg.RotateSecondPass,
			"note":                printerCfg.Note,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"printers": printers,
		"count":    len(printers),
	})
}

// GetPrinterInfo 获取指定打印机的详细信息
func GetPrinterInfo(c *gin.Context) {
	printerID := c.Param("id")
	printerCfg, err := resolvePrinter(printerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "printer_id": printerID})
		return
	}

	cupsClient, printerName, err := newCupsClientForPrinter(printerCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "printer_id": printerID})
		return
	}

	printer, err := cupsClient.GetPrinterDetails(printerName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":      "Printer not found or CUPS service unavailable",
			"printer_id": printerID,
			"details":    err.Error(),
		})
		return
	}
	printer.ID = printerCfg.ID

	c.JSON(http.StatusOK, gin.H{
		"id":                  printer.ID,
		"name":                printer.Name,
		"description":         printer.Description,
		"status":              printer.Status,
		"model":               printer.Model,
		"location":            printer.Location,
		"duplex_mode":         printerCfg.NormalizedDuplexMode(),
		"reverse":             printerCfg.Reverse,
		"first_pass":          printerCfg.NormalizedFirstPass(),
		"pad_to_even":         printerCfg.PadToEvenEnabled(),
		"reverse_first_pass":  printerCfg.ReverseFirstPass,
		"reverse_second_pass": printerCfg.ReverseSecondPass,
		"rotate_second_pass":  printerCfg.RotateSecondPass,
		"note":                printerCfg.Note,
	})
}

// SubmitPrintJob 提交打印任务
func SubmitPrintJob(c *gin.Context) {
	printerID := c.PostForm("printer_id")
	if printerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "printer_id is required",
		})
		return
	}

	copies := 1
	if rawCopies := c.Query("copies"); rawCopies != "" {
		parsedCopies, err := strconv.Atoi(rawCopies)
		if err != nil || parsedCopies <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "copies must be a positive integer",
			})
			return
		}
		copies = parsedCopies
	}

	duplexRequested := false
	if rawDuplex := strings.TrimSpace(strings.ToLower(c.Query("duplex"))); rawDuplex != "" {
		switch rawDuplex {
		case "1", "true", "yes", "on":
			duplexRequested = true
		case "0", "false", "no", "off":
			duplexRequested = false
		default:
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "duplex must be one of: true/false/1/0/yes/no/on/off",
			})
			return
		}
	}

	collate := true
	if rawCollate := strings.TrimSpace(strings.ToLower(c.Query("collate"))); rawCollate != "" {
		switch rawCollate {
		case "1", "true", "yes", "on":
			collate = true
		case "0", "false", "no", "off":
			collate = false
		default:
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "collate must be one of: true/false/1/0/yes/no/on/off",
			})
			return
		}
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "file is required in multipart form field 'file'",
		})
		return
	}

	tempDir := os.TempDir()
	tempPath := filepath.Join(tempDir, fmt.Sprintf("goprint-%d-%s", os.Getpid(), filepath.Base(file.Filename)))
	if err := c.SaveUploadedFile(file, tempPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to save uploaded file",
			"details": err.Error(),
		})
		return
	}
	defer os.Remove(tempPath)

	printerCfg, err := resolvePrinter(printerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "printer_id": printerID})
		return
	}

	cupsClient, printerName, err := newCupsClientForPrinter(printerCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "printer_id": printerID})
		return
	}

	duplexMode := printerCfg.NormalizedDuplexMode()
	if !duplexRequested {
		duplexMode = "off"
	}
	if pageCount, countErr := countPDFPages(tempPath); countErr == nil && pageCount == 1 {
		duplexMode = "off"
	}

	if duplexRequested && printerCfg.NormalizedDuplexMode() == "off" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "duplex requested but printer duplex_mode is off",
			"printer_id": printerID,
			"printer_config": gin.H{
				"duplex_mode":         printerCfg.NormalizedDuplexMode(),
				"reverse":             printerCfg.Reverse,
				"first_pass":          printerCfg.NormalizedFirstPass(),
				"pad_to_even":         printerCfg.PadToEvenEnabled(),
				"reverse_first_pass":  printerCfg.ReverseFirstPass,
				"reverse_second_pass": printerCfg.ReverseSecondPass,
				"rotate_second_pass":  printerCfg.RotateSecondPass,
				"note":                printerCfg.Note,
			},
			"hint": "set duplex_mode to auto or manual in config.yaml if this printer supports duplex",
		})
		return
	}

	if duplexMode == "manual" {
		firstPassPath, secondPassPath, cleanup, err := prepareManualDuplexFiles(tempPath, printerCfg)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "failed to prepare manual duplex files",
				"details": err.Error(),
			})
			return
		}
		defer cleanup()

		firstPassToSubmit, err := applyCopiesMode(firstPassPath, copies, collate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "failed to build first pass copies",
				"details": err.Error(),
			})
			return
		}
		if firstPassToSubmit != firstPassPath {
			defer os.Remove(firstPassToSubmit)
		}

		secondPassToStore, err := applyCopiesMode(secondPassPath, copies, collate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "failed to build second pass copies",
				"details": err.Error(),
			})
			return
		}

		initialJobID, err := cupsClient.SubmitJob(printerName, firstPassToSubmit, cups.PrintOptions{Copies: 1})
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":      "failed to submit first pass for manual duplex",
				"printer_id": printerID,
				"details":    err.Error(),
			})
			return
		}

		token, expiresAt, err := saveManualDuplexPending(printerID, secondPassToStore, 1)
		if err != nil {
			_ = os.Remove(secondPassToStore)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "failed to create manual duplex hook",
				"details": err.Error(),
			})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"job_id":          initialJobID,
			"printer":         printerID,
			"copies":          copies,
			"collate":         collate,
			"status":          "pending",
			"duplex":          true,
			"note":            printerCfg.Note,
			"message":         "First pass submitted. Use hook_url to print remaining pages.",
			"hook_url":        fmt.Sprintf("/api/manual-duplex-hooks/%s/continue", token),
			"hook_expires_at": expiresAt.UTC().Format(time.RFC3339),
		})

		if user, ok := currentAuthUser(c); ok {
			cfg, cfgErr := requireConfig()
			if cfgErr == nil {
				persistPrintJobToBitable(c, cfg, printJobRecord{
					JobID:     initialJobID,
					PrinterID: printerID,
					FileName:  file.Filename,
					Status:    "pending_manual_continue",
					Copies:    copies,
					Duplex:    true,
					User:      user,
				})
			}
		}
		return
	}

	printPath := tempPath
	if duplexMode == "off" && printerCfg.Reverse {
		reversedPath, reverseErr := prepareReversedPDF(tempPath)
		if reverseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "failed to reverse document for single-side printing",
				"details": reverseErr.Error(),
			})
			return
		}
		if reversedPath != tempPath {
			defer os.Remove(reversedPath)
			printPath = reversedPath
		}
	}

	finalPrintPath, err := applyCopiesMode(printPath, copies, collate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "failed to build copies",
			"details": err.Error(),
		})
		return
	}
	if finalPrintPath != printPath {
		defer os.Remove(finalPrintPath)
	}

	printOpts := cups.PrintOptions{Copies: 1, Collate: collate}
	if duplexMode == "auto" {
		printOpts.Sides = "two-sided-long-edge"
	}

	jobID, err := cupsClient.SubmitJob(printerName, finalPrintPath, printOpts)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":      "failed to submit print job",
			"printer_id": printerID,
			"details":    err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"job_id":  jobID,
		"printer": printerID,
		"copies":  copies,
		"collate": collate,
		"status":  "pending",
		"duplex":  duplexMode != "off",
		"note":    printerCfg.Note,
		"message": "Print job submitted successfully",
	})

	if user, ok := currentAuthUser(c); ok {
		cfg, cfgErr := requireConfig()
		if cfgErr == nil {
			persistPrintJobToBitable(c, cfg, printJobRecord{
				JobID:     jobID,
				PrinterID: printerID,
				FileName:  file.Filename,
				Status:    "pending",
				Copies:    copies,
				Duplex:    duplexMode != "off",
				User:      user,
			})
		}
	}
}

func ContinueManualDuplexPrint(c *gin.Context) {
	token := c.Param("token")
	pending, ok := getManualDuplexPending(token)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "manual duplex hook not found or already used",
		})
		return
	}

	printerCfg, err := resolvePrinter(pending.PrinterID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "printer_id": pending.PrinterID})
		return
	}

	cupsClient, printerName, err := newCupsClientForPrinter(printerCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "printer_id": pending.PrinterID})
		return
	}

	jobID, err := cupsClient.SubmitJob(printerName, pending.RemainingFilePath, cups.PrintOptions{Copies: pending.Copies})
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "failed to submit remaining pages",
			"details": err.Error(),
		})
		return
	}

	_ = os.Remove(pending.RemainingFilePath)
	deleteManualDuplexPending(token)

	c.JSON(http.StatusCreated, gin.H{
		"job_id":  jobID,
		"printer": pending.PrinterID,
		"status":  "pending",
		"duplex":  true,
		"message": "Remaining pages submitted successfully",
	})
}

func CancelManualDuplexPrint(c *gin.Context) {
	token := c.Param("token")
	pending, ok := getManualDuplexPending(token)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "manual duplex hook not found or already used",
		})
		return
	}

	_ = os.Remove(pending.RemainingFilePath)
	deleteManualDuplexPending(token)

	c.JSON(http.StatusOK, gin.H{
		"printer":   pending.PrinterID,
		"duplex":    false,
		"status":    "cancelled",
		"message":   "Manual duplex flow cancelled; remaining pages were not submitted",
		"cancelled": true,
	})
}

// ListPrintJobs 列出所有打印任务
func ListPrintJobs(c *gin.Context) {
	cfg, err := requireConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	user, ok := currentAuthUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authenticated user context"})
		return
	}

	store, err := newBitableJobStore(cfg)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "job store is not available",
			"details": err.Error(),
		})
		return
	}

	printerID := c.Query("printer_id")
	allJobs, err := store.ListJobsByUser(context.Background(), user, 500)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query jobs from bitable", "details": err.Error()})
		return
	}

	jobs := make([]map[string]interface{}, 0, len(allJobs))
	for _, job := range allJobs {
		if printerID != "" {
			if fmt.Sprint(job["printer"]) != printerID {
				continue
			}
		}
		jobs = append(jobs, job)
	}

	c.JSON(http.StatusOK, gin.H{
		"jobs":  jobs,
		"count": len(jobs),
	})
}

// GetJobStatus 获取打印任务状态
func GetJobStatus(c *gin.Context) {
	jobIDStr := c.Param("id")
	jobID, err := strconv.Atoi(jobIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "Invalid job ID format",
			"job_id": jobIDStr,
		})
		return
	}

	cfg, err := requireConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var job *cups.PrintJob
	for _, printerCfg := range cfg.Printers {
		cupsClient, _, clientErr := newCupsClientForPrinter(printerCfg)
		if clientErr != nil {
			continue
		}

		candidate, queryErr := cupsClient.GetPrintJobDetails(jobID)
		if queryErr == nil {
			candidate.PrinterID = printerCfg.ID
			job = candidate
			break
		}
	}

	if job == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Job not found or CUPS service unavailable",
			"job_id":  jobIDStr,
			"details": "unable to resolve job from configured printers",
		})
		return
	}

	c.JSON(http.StatusOK, job)
}

// CancelPrintJob 取消打印任务
func CancelPrintJob(c *gin.Context) {
	jobIDStr := c.Param("id")
	jobID, err := strconv.Atoi(jobIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "Invalid job ID format",
			"job_id": jobIDStr,
		})
		return
	}

	cfg, err := requireConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var lastErr error
	cancelled := false
	for _, printerCfg := range cfg.Printers {
		cupsClient, _, clientErr := newCupsClientForPrinter(printerCfg)
		if clientErr != nil {
			lastErr = clientErr
			continue
		}

		if cancelErr := cupsClient.CancelJob(jobIDStr); cancelErr == nil {
			cancelled = true
			break
		} else {
			lastErr = cancelErr
		}
	}

	if !cancelled {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Failed to cancel print job",
			"job_id":  jobIDStr,
			"details": fmt.Sprintf("%v", lastErr),
		})
		return
	}

	job, err := func() (*cups.PrintJob, error) {
		for _, printerCfg := range cfg.Printers {
			cupsClient, _, clientErr := newCupsClientForPrinter(printerCfg)
			if clientErr != nil {
				continue
			}

			candidate, queryErr := cupsClient.GetPrintJobDetails(jobID)
			if queryErr == nil {
				candidate.PrinterID = printerCfg.ID
				return candidate, nil
			}
		}
		return nil, fmt.Errorf("cancelled but unable to re-query job")
	}()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"job_id":  jobIDStr,
			"status":  "cancelled",
			"message": "Print job cancelled",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id":  jobIDStr,
		"status":  job.Status,
		"reason":  job.Reason,
		"message": "Print job cancellation requested",
	})
}
