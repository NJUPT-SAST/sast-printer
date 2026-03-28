package cups

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	ipp "github.com/phin1x/go-ipp"
)

// CupsClient CUPS客户端
type CupsClient struct {
	Host string
	Port int
}

// NewCupsClient 创建新的CUPS客户端
func NewCupsClient(host string, port int) *CupsClient {
	return &CupsClient{
		Host: host,
		Port: port,
	}
}

func (c *CupsClient) newIPPClient() *ipp.CUPSClient {
	username := strings.TrimSpace(os.Getenv("USER"))
	if username == "" {
		username = "goprint"
	}

	return ipp.NewCUPSClient(c.Host, c.Port, username, "", false)
}

// Connect 连接到CUPS服务
func (c *CupsClient) Connect() error {
	log.Printf("Connecting to CUPS: %s:%d\n", c.Host, c.Port)
	client := c.newIPPClient()
	_, err := client.GetPrinters([]string{ipp.AttributePrinterName})
	if err != nil {
		return fmt.Errorf("Failed to connect to CUPS: %w", err)
	}

	return nil
}

// GetPrinters 获取所有打印机
func (c *CupsClient) GetPrinters() ([]Printer, error) {
	log.Println("Acquiring printer list...")
	client := c.newIPPClient()

	attributes := []string{
		ipp.AttributePrinterName,
		ipp.AttributePrinterInfo,
		ipp.AttributePrinterLocation,
		ipp.AttributePrinterMakeAndModel,
		ipp.AttributePrinterState,
	}

	printerMap, err := client.GetPrinters(attributes)
	if err != nil {
		return nil, fmt.Errorf("Failed to get CUPS printer list: %w", err)
	}

	printerNames := make([]string, 0, len(printerMap))
	for name := range printerMap {
		printerNames = append(printerNames, name)
	}
	sort.Strings(printerNames)

	printers := make([]Printer, 0, len(printerNames))
	for _, name := range printerNames {
		attrs := printerMap[name]

		printerName := firstStringAttr(attrs, ipp.AttributePrinterName)
		if printerName == "" {
			printerName = name
		}

		printers = append(printers, Printer{
			ID:          printerName,
			Name:        printerName,
			Description: firstStringAttr(attrs, ipp.AttributePrinterInfo),
			Status:      mapPrinterState(attrs[ipp.AttributePrinterState]),
			Model:       firstStringAttr(attrs, ipp.AttributePrinterMakeAndModel),
			Location:    firstStringAttr(attrs, ipp.AttributePrinterLocation),
		})
	}

	return printers, nil
}

// GetPrinterDetails 获取单个打印机的详细信息
func (c *CupsClient) GetPrinterDetails(printerID string) (*Printer, error) {
	log.Printf("Acquiring details for printer %s\n", printerID)
	client := c.newIPPClient()

	attributes := []string{
		ipp.AttributePrinterName,
		ipp.AttributePrinterInfo,
		ipp.AttributePrinterLocation,
		ipp.AttributePrinterMakeAndModel,
		ipp.AttributePrinterState,
		ipp.AttributePrinterIsAcceptingJobs,
		ipp.AttributePrinterStateReasons,
	}

	printerAttrs, err := client.GetPrinterAttributes(printerID, attributes)
	if err != nil {
		return nil, fmt.Errorf("Failed to get printer attributes: %w", err)
	}

	return &Printer{
		ID:          printerID,
		Name:        firstStringAttr(printerAttrs, ipp.AttributePrinterName),
		Description: firstStringAttr(printerAttrs, ipp.AttributePrinterInfo),
		Status:      mapPrinterState(printerAttrs[ipp.AttributePrinterState]),
		Model:       firstStringAttr(printerAttrs, ipp.AttributePrinterMakeAndModel),
		Location:    firstStringAttr(printerAttrs, ipp.AttributePrinterLocation),
	}, nil
}

// GetPrinterStatus 获取打印机状态
func (c *CupsClient) GetPrinterStatus(printerID string) (string, error) {
	log.Printf("Acquiring status for printer %s\n", printerID)
	printer, err := c.GetPrinterDetails(printerID)
	if err != nil {
		return "", err
	}
	return printer.Status, nil
}

// GetPrintJobs 获取打印机上的所有打印任务
func (c *CupsClient) GetPrintJobs(printerID string) ([]PrintJob, error) {
	log.Printf("Acquiring print jobs for printer %s\n", printerID)
	client := c.newIPPClient()

	attributes := []string{
		ipp.AttributeJobID,
		ipp.AttributeJobName,
		ipp.AttributeJobState,
		ipp.AttributeJobStateReason,
		ipp.AttributeJobPrinterURI,
	}

	jobsMap, err := client.GetJobs(printerID, "", ipp.JobStateFilterAll, false, 0, 0, attributes)
	if err != nil {
		return nil, fmt.Errorf("Failed to get print jobs: %w", err)
	}

	jobs := make([]PrintJob, 0, len(jobsMap))
	for _, attrs := range jobsMap {
		jobID := firstIntAttr(attrs, ipp.AttributeJobID)
		jobName := firstStringAttr(attrs, ipp.AttributeJobName)
		jobStateCode := firstIntAttr(attrs, ipp.AttributeJobState)
		jobReason := firstStringAttr(attrs, ipp.AttributeJobStateReason)
		jobStatus := mapJobStateWithReason(jobStateCode, jobReason)

		jobs = append(jobs, PrintJob{
			ID:        fmt.Sprintf("%d", jobID),
			PrinterID: printerID,
			Title:     jobName,
			Status:    jobStatus,
			Reason:    jobReason,
			RawState:  jobStateCode,
			Progress:  0, // IPP doesn't provide progress directly
		})
	}

	return jobs, nil
}

// GetPrintJobDetails 获取单个打印任务的详细信息
func (c *CupsClient) GetPrintJobDetails(jobID int) (*PrintJob, error) {
	log.Printf("Acquiring details for job %d\n", jobID)
	client := c.newIPPClient()

	attributes := []string{
		ipp.AttributeJobID,
		ipp.AttributeJobName,
		ipp.AttributeJobState,
		ipp.AttributeJobStateReason,
		ipp.AttributeJobPrinterURI,
	}

	attrs, err := client.GetJobAttributes(jobID, attributes)
	if err != nil {
		return nil, fmt.Errorf("Failed to get job attributes: %w", err)
	}

	jobName := firstStringAttr(attrs, ipp.AttributeJobName)
	printerURI := firstStringAttr(attrs, ipp.AttributeJobPrinterURI)
	jobStateCode := firstIntAttr(attrs, ipp.AttributeJobState)
	jobReason := firstStringAttr(attrs, ipp.AttributeJobStateReason)
	jobStatus := mapJobStateWithReason(jobStateCode, jobReason)

	return &PrintJob{
		ID:        fmt.Sprintf("%d", jobID),
		PrinterID: extractPrinterNameFromURI(printerURI),
		Title:     jobName,
		Status:    jobStatus,
		Reason:    jobReason,
		RawState:  jobStateCode,
		Progress:  0,
	}, nil
}

// SubmitJob 提交打印任务
func (c *CupsClient) SubmitJob(printerID string, filePath string, opts PrintOptions) (string, error) {
	log.Printf("Submitting print job to printer %s: %s\n", printerID, filePath)
	client := c.newIPPClient()

	fileStats, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("Failed to stat file: %w", err)
	}

	document, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("Failed to open file: %w", err)
	}
	defer document.Close()

	jobAttributes := map[string]any{}
	if opts.Copies > 0 {
		jobAttributes[ipp.AttributeCopies] = opts.Copies
	}

	jobID, err := client.PrintJob(ipp.Document{
		Document: document,
		Name:     filepath.Base(filePath),
		Size:     int(fileStats.Size()),
		MimeType: detectMimeType(filePath),
	}, printerID, jobAttributes)
	if err != nil {
		return "", fmt.Errorf("Failed to submit print job: %w", err)
	}

	return fmt.Sprintf("%d", jobID), nil
}

func detectMimeType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".ps":
		return ipp.MimeTypePostscript
	default:
		return ipp.MimeTypeOctetStream
	}
}

// CancelJob 取消打印任务
func (c *CupsClient) CancelJob(jobID string) error {
	log.Printf("Canceling print job %s\n", jobID)
	client := c.newIPPClient()

	id, err := strconv.Atoi(strings.TrimSpace(jobID))
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid job id: %s", jobID)
	}

	if err := client.CancelJob(id, false); err != nil {
		return fmt.Errorf("failed to cancel print job %d: %w", id, err)
	}

	return nil
}

func firstStringAttr(attrs ipp.Attributes, key string) string {
	values, ok := attrs[key]
	if !ok || len(values) == 0 {
		return ""
	}

	if value, ok := values[0].Value.(string); ok {
		return value
	}

	return fmt.Sprintf("%v", values[0].Value)
}

func mapPrinterState(values []ipp.Attribute) string {
	if len(values) == 0 {
		return "unknown"
	}

	var state int8

	switch v := values[0].Value.(type) {
	case int8:
		state = v
	case int16:
		state = int8(v)
	case int:
		state = int8(v)
	default:
		return fmt.Sprintf("%v", values[0].Value)
	}

	switch state {
	case ipp.PrinterStateIdle:
		return "idle"
	case ipp.PrinterStateProcessing:
		return "processing"
	case ipp.PrinterStateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// firstIntAttr 获取属性中的第一个整数值
func firstIntAttr(attrs ipp.Attributes, key string) int {
	values, ok := attrs[key]
	if !ok || len(values) == 0 {
		return 0
	}

	switch v := values[0].Value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}

// mapJobState 将IPP任务状态代码映射为字符串
func mapJobStateWithReason(state int, reason string) string {
	if strings.Contains(reason, "job-canceled") {
		return "cancelled"
	}
	if strings.Contains(reason, "job-completed-successfully") {
		return "completed"
	}

	switch state {
	case int(ipp.JobStatePending):
		return "pending"
	case int(ipp.JobStateHeld):
		return "held"
	case int(ipp.JobStateProcessing):
		return "processing"
	case int(ipp.JobStateStopped):
		return "stopped"
	case int(ipp.JobStateCanceled):
		return "cancelled"
	case int(ipp.JobStateAborted):
		return "aborted"
	case int(ipp.JobStateCompleted):
		return "completed"
	default:
		return "unknown"
	}
}

// extractPrinterNameFromURI 从打印机URI中提取打印机名称
func extractPrinterNameFromURI(uri string) string {
	// URI format: ipp://localhost/printers/printer-name
	if uri == "" {
		return ""
	}
	parts := strings.Split(uri, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return uri
}
