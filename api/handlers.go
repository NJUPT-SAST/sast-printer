package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"goprint/cups"

	"github.com/gin-gonic/gin"
)

// HealthCheck 健康检查接口
func HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"message": "GoPrint is running.",
	})
}

// ListPrinters 列出所有可用打印机
func ListPrinters(c *gin.Context) {
	cupsClient := cups.NewCupsClient("localhost", 631)
	printers, err := cupsClient.GetPrinters()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "Cannot connect to CUPS service",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"printers": printers,
		"count":    len(printers),
	})
}

// GetPrinterInfo 获取指定打印机的详细信息
func GetPrinterInfo(c *gin.Context) {
	printerID := c.Param("id")
	cupsClient := cups.NewCupsClient("localhost", 631)
	printer, err := cupsClient.GetPrinterDetails(printerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":      "Printer not found or CUPS service unavailable",
			"printer_id": printerID,
			"details":    err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, printer)
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
	if rawCopies := c.PostForm("copies"); rawCopies != "" {
		parsedCopies, err := strconv.Atoi(rawCopies)
		if err != nil || parsedCopies <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "copies must be a positive integer",
			})
			return
		}
		copies = parsedCopies
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

	cupsClient := cups.NewCupsClient("localhost", 631)
	jobID, err := cupsClient.SubmitJob(printerID, tempPath, cups.PrintOptions{Copies: copies})
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
		"status":  "pending",
		"message": "Print job submitted successfully",
	})
}

// ListPrintJobs 列出所有打印任务
func ListPrintJobs(c *gin.Context) {
	cupsClient := cups.NewCupsClient("localhost", 631)
	printerID := c.Query("printer_id")

	if printerID != "" {
		// 获取特定打印机的任务
		jobs, err := cupsClient.GetPrintJobs(printerID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":      "Failed to get print jobs",
				"printer_id": printerID,
				"details":    err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"jobs":       jobs,
			"count":      len(jobs),
			"printer_id": printerID,
		})
		return
	}

	// 获取所有打印机的所有任务
	printers, err := cupsClient.GetPrinters()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "Cannot connect to CUPS service",
			"details": err.Error(),
		})
		return
	}

	allJobs := []cups.PrintJob{}
	for _, printer := range printers {
		jobs, err := cupsClient.GetPrintJobs(printer.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":      "Failed to get print jobs",
				"printer_id": printer.ID,
				"details":    err.Error(),
			})
			return
		}
		allJobs = append(allJobs, jobs...)
	}

	c.JSON(http.StatusOK, gin.H{
		"jobs":  allJobs,
		"count": len(allJobs),
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

	cupsClient := cups.NewCupsClient("localhost", 631)
	job, err := cupsClient.GetPrintJobDetails(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Job not found or CUPS service unavailable",
			"job_id":  jobIDStr,
			"details": err.Error(),
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

	cupsClient := cups.NewCupsClient("localhost", 631)
	if err := cupsClient.CancelJob(jobIDStr); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Failed to cancel print job",
			"job_id":  jobIDStr,
			"details": err.Error(),
		})
		return
	}

	job, err := cupsClient.GetPrintJobDetails(jobID)
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
