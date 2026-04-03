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
	if strings.TrimSpace(user.OpenID) == "" {
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

	cfg, cfgErr := requireConfig()
	if cfgErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": cfgErr.Error()})
		return
	}

	if !isSupportedUploadFile(cfg, file.Filename) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":      "unsupported file type, accepted formats are configured by office_conversion.accepted_formats plus pdf",
			"error_code": "unsupported_file_type",
		})
		return
	}

	if err := acquirePrintSubmitQueue(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":      "print queue is busy or request cancelled",
			"error_code": "print_queue_unavailable",
			"details":    err.Error(),
		})
		return
	}
	defer releasePrintSubmitQueue()

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

	printSourcePath := tempPath
	convertedPath := ""
	if isOfficeConvertible(cfg, file.Filename) {
		convertedPath, err = convertOfficeToPDF(c.Request.Context(), cfg, tempPath)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":      "failed to convert office file to pdf",
				"error_code": "office_conversion_failed",
				"details":    err.Error(),
			})
			return
		}
		defer os.Remove(convertedPath)
		printSourcePath = convertedPath
	}

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
	if pageCount, countErr := countPDFPages(printSourcePath); countErr == nil && pageCount == 1 {
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
		firstPassPath, secondPassPath, cleanup, err := prepareManualDuplexFiles(printSourcePath, printerCfg)
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

		hookURL := fmt.Sprintf("/manual-duplex-hooks/%s/continue", token)

		c.JSON(http.StatusCreated, gin.H{
			"job_id":          initialJobID,
			"printer":         printerID,
			"copies":          copies,
			"collate":         collate,
			"status":          "pending",
			"duplex":          true,
			"note":            printerCfg.Note,
			"message":         "First pass submitted. Use hook_url to print remaining pages.",
			"hook_url":        hookURL,
			"hook_expires_at": expiresAt.In(time.Local).Format("2006-01-02 15:04"),
		})

		if user, ok := currentAuthUser(c); ok {
			cfg, cfgErr := requireConfig()
			if cfgErr == nil {
				persistPrintJobToBitable(c, cfg, printJobRecord{
					JobID:      initialJobID,
					PrinterID:  printerID,
					FileName:   file.Filename,
					Status:     "pending_manual_continue",
					Copies:     copies,
					Duplex:     true,
					DuplexHook: hookURL,
					User:       user,
				})

				// 将任务注册到后台轮询器
				tracker := initJobStatusPoller(cfg)
				if tracker != nil {
					tracker.AddPendingJob(initialJobID, printerID)
				}
			}
		}
		return
	}

	printPath := printSourcePath
	if duplexMode == "off" && printerCfg.Reverse {
		reversedPath, reverseErr := prepareReversedPDF(printSourcePath)
		if reverseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "failed to reverse document for single-side printing",
				"details": reverseErr.Error(),
			})
			return
		}
		if reversedPath != printSourcePath {
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
		autoSides, sideErr := chooseAutoDuplexSides(finalPrintPath)
		if sideErr != nil {
			log.Printf("[print] failed to detect document orientation, fallback to long-edge: %v", sideErr)
			printOpts.Sides = "two-sided-long-edge"
		} else {
			printOpts.Sides = autoSides
		}
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

			// 将任务注册到后台轮询器
			tracker := initJobStatusPoller(cfg)
			if tracker != nil {
				tracker.AddPendingJob(jobID, printerID)
			}
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

	// 将第二遍任务也注册到后台轮询器
	if _, ok := currentAuthUser(c); ok {
		cfg, cfgErr := requireConfig()
		if cfgErr == nil {
			tracker := initJobStatusPoller(cfg)
			if tracker != nil {
				tracker.AddPendingJob(jobID, pending.PrinterID)
			}
		}
	}
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
	if strings.TrimSpace(jobIDStr) == "" {
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

	deleted, err := store.DeleteJobByUserAndJobID(context.Background(), user, jobIDStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "failed to delete print job from bitable",
			"job_id":  jobIDStr,
			"details": err.Error(),
		})
		return
	}

	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{
			"error":  "job not found",
			"job_id": jobIDStr,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id":  jobIDStr,
		"status":  "deleted",
		"message": "Task removed from bitable only. Please cancel the physical print job on printer panel if needed.",
	})
}
