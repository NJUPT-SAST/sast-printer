package cups

// Printer 表示一台打印机
type Printer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"` // idle, processing, stopped
	Model       string `json:"model"`
	Location    string `json:"location"`
}

// PrintJob 表示一个打印任务
type PrintJob struct {
	ID        string `json:"id"`
	PrinterID string `json:"printer_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`    // pending, held, processing, stopped, completed, cancelled, aborted
	Reason    string `json:"reason"`    // job-state-reason
	RawState  int    `json:"raw_state"` // 原始IPP状态码
	Progress  int    `json:"progress"`  // 0-100
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// PrintOptions 打印选项
type PrintOptions struct {
	Copies      int    `json:"copies"`
	ColorModel  string `json:"color_model"` // Color, Gray
	Orientation string `json:"orientation"` // Portrait, Landscape
	MediaSize   string `json:"media_size"`  // A4, Letter, etc.
	Quality     string `json:"quality"`     // Draft, Normal, High
}
