package bot

import (
	"encoding/json"
	"fmt"
	"goprint/api/pdfutil"
	"goprint/config"
	"strconv"
	"strings"
)

type PrinterOption struct {
	ID    string `json:"id"`
	Name  string `json:"text"`
	Value string `json:"value"`
}

type PrinterSelectCardState struct {
	Disabled          bool
	NextButtonText    string
	CancelButtonText  string
	SelectedPrinterID string
	StatusText        string
	TimeoutMinutes    int
}

type PrintConfigCardState struct {
	Disabled         bool
	PrintButtonText  string
	CancelButtonText string
	PagesValue       string
	ScaleValue       string
	StatusText       string
	TimeoutMinutes   int
}

type DuplexContinueCardState struct {
	Disabled     bool
	ContinueText string
	CancelText   string
	StatusText   string
}

type ActiveJobWarning struct {
	Message       string
	FileName      string
	HookExpiresAt string
}

type PrintConflictCardState struct {
	Disabled       bool
	ContinueText   string
	CancelText     string
	StatusText     string
	Warning        *ActiveJobWarning
	PrinterID      string
	Copies         int
	Pages          string
	Nup            int
	Scale          string
	Duplex         string
	OriginalAction string
	CancelStage    string
}

func BuildPrinterOptions(cfg *config.Config) []PrinterOption {
	visible := cfg.VisiblePrinters()
	opts := make([]PrinterOption, len(visible))
	for i, p := range visible {
		opts[i] = PrinterOption{ID: p.ID, Name: p.ID, Value: p.ID}
	}
	return opts
}

func optionInitialValue(options []map[string]interface{}, selected string) string {
	for _, opt := range options {
		if value, ok := opt["value"].(string); ok && value == selected {
			return value
		}
	}
	if len(options) == 0 {
		return ""
	}
	if value, ok := options[0]["value"].(string); ok {
		return value
	}
	return ""
}

func botCardConfig() map[string]interface{} {
	return map[string]interface{}{
		"update_multi": true,
	}
}

func displayTimeoutMinutes(minutes int) int {
	if minutes <= 0 {
		return 10
	}
	return minutes
}

func BuildPrinterSelectCardData(filename string, totalPages int, printers []PrinterOption, sessionID string, state PrinterSelectCardState) map[string]interface{} {
	mkOptText := func(s string) map[string]interface{} {
		return map[string]interface{}{"tag": "plain_text", "content": s}
	}
	printerOpts := make([]map[string]interface{}, len(printers))
	for i, p := range printers {
		printerOpts[i] = map[string]interface{}{
			"text":  mkOptText(p.Name),
			"value": p.ID,
		}
	}

	selectedPrinterID := strings.TrimSpace(state.SelectedPrinterID)
	if selectedPrinterID == "" && len(printers) > 0 {
		selectedPrinterID = printers[0].ID
	}
	printerInitialOption := optionInitialValue(printerOpts, selectedPrinterID)

	cancelButtonText := state.CancelButtonText
	if cancelButtonText == "" {
		cancelButtonText = "取消"
	}
	nextButtonText := state.NextButtonText
	if nextButtonText == "" {
		nextButtonText = "下一步"
	}

	cancelButton := map[string]interface{}{
		"tag":        "button",
		"element_id": "cancel_btn",
		"text":       map[string]interface{}{"tag": "plain_text", "content": cancelButtonText},
		"type":       "default",
		"name":       "cancel_btn",
	}
	nextButton := map[string]interface{}{
		"tag":        "button",
		"element_id": "select_printer_btn",
		"text":       map[string]interface{}{"tag": "plain_text", "content": nextButtonText},
		"type":       "primary_filled",
		"name":       "select_printer_btn",
	}
	if state.Disabled {
		cancelButton["disabled"] = true
		cancelButton["form_action_type"] = "submit"
		nextButton["disabled"] = true
		nextButton["form_action_type"] = "submit"
	} else {
		cancelButton["form_action_type"] = "submit"
		cancelButton["behaviors"] = []interface{}{
			map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "cancel", "stage": "printer_select", "session_id": sessionID}},
		}
		nextButton["form_action_type"] = "submit"
		nextButton["behaviors"] = []interface{}{
			map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "select_printer", "session_id": sessionID, "printer_id": selectedPrinterID}},
		}
	}

	bodyElements := []interface{}{
		map[string]interface{}{
			"tag":        "markdown",
			"element_id": "file_info",
			"content":    fmt.Sprintf("📄 **%s**　共 %d 页", filename, totalPages),
		},
		map[string]interface{}{
			"tag":        "form",
			"element_id": "printer_form",
			"name":       "printer_form",
			"elements": []interface{}{
				map[string]interface{}{
					"tag":            "select_static",
					"element_id":     "printer_select",
					"name":           "printer_id",
					"placeholder":    map[string]interface{}{"tag": "plain_text", "content": "选择打印机"},
					"options":        printerOpts,
					"initial_option": printerInitialOption,
					"width":          "fill",
				},
				map[string]interface{}{
					"tag":                "column_set",
					"element_id":         "printer_btn_cols",
					"flex_mode":          "bisect",
					"horizontal_spacing": "8px",
					"horizontal_align":   "right",
					"columns": []interface{}{
						map[string]interface{}{
							"tag":      "column",
							"width":    "auto",
							"elements": []interface{}{cancelButton},
						},
						map[string]interface{}{
							"tag":      "column",
							"width":    "auto",
							"elements": []interface{}{nextButton},
						},
					},
				},
			},
		},
	}
	if strings.TrimSpace(state.StatusText) != "" {
		bodyElements = append(bodyElements, map[string]interface{}{
			"tag":        "markdown",
			"element_id": "status_hint",
			"content":    state.StatusText,
		})
	}
	bodyElements = append(bodyElements,
		map[string]interface{}{"tag": "hr", "element_id": "divider"},
		map[string]interface{}{
			"tag":        "markdown",
			"element_id": "timeout_hint",
			"content":    fmt.Sprintf("⏰ %d 分钟后自动取消任务", displayTimeoutMinutes(state.TimeoutMinutes)),
		},
	)

	return map[string]interface{}{
		"schema": "2.0",
		"config": botCardConfig(),
		"header": map[string]interface{}{
			"template": "blue",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "🖨️ 选择打印机",
			},
		},
		"body": map[string]interface{}{
			"elements": bodyElements,
		},
	}
}

func BuildPrinterSelectCard(filename string, totalPages int, printers []PrinterOption, sessionID string) (string, error) {
	b, err := json.Marshal(BuildPrinterSelectCardData(filename, totalPages, printers, sessionID, PrinterSelectCardState{}))
	return string(b), err
}

func buildNupOptions() []map[string]interface{} {
	mkOptText := func(s string) map[string]interface{} {
		return map[string]interface{}{"tag": "plain_text", "content": s}
	}
	return []map[string]interface{}{
		{"text": mkOptText("1-up (不缩印)"), "value": "1"},
		{"text": mkOptText("2-up"), "value": "2"},
		{"text": mkOptText("4-up"), "value": "4"},
		{"text": mkOptText("6-up"), "value": "6"},
	}
}

func buildDuplexOptions(printer config.PrinterConfig) []map[string]interface{} {
	mkOptText := func(s string) map[string]interface{} {
		return map[string]interface{}{"tag": "plain_text", "content": s}
	}
	options := []map[string]interface{}{
		{"text": mkOptText("单面"), "value": "off"},
	}
	switch printer.NormalizedDuplexMode() {
	case "auto":
		options = append(options, map[string]interface{}{"text": mkOptText("双面（自动）"), "value": "auto"})
	case "manual":
		options = append(options, map[string]interface{}{"text": mkOptText("双面（手动）"), "value": "manual"})
	}
	return options
}

func IsPrinterDuplexOptionSupported(printer config.PrinterConfig, duplex string) bool {
	switch strings.TrimSpace(strings.ToLower(duplex)) {
	case "", "off":
		return true
	case "auto":
		return printer.NormalizedDuplexMode() == "auto"
	case "manual":
		return printer.NormalizedDuplexMode() == "manual"
	default:
		return false
	}
}

func normalizeDefaultsForPrinter(defaults config.FileTypeDefault, printer config.PrinterConfig) config.FileTypeDefault {
	if defaults.Copies < 1 {
		defaults.Copies = 1
	}
	if !pdfutil.ValidNup(defaults.Nup) {
		defaults.Nup = 1
	}
	if defaults.Scale <= 0 {
		defaults.Scale = 100
	}
	if !IsPrinterDuplexOptionSupported(printer, defaults.Duplex) {
		defaults.Duplex = "off"
	}
	return defaults
}

func BuildPrintConfigCardData(filename string, totalPages int, printer config.PrinterConfig, defaults config.FileTypeDefault, sessionID string, state PrintConfigCardState) map[string]interface{} {
	nupOptions := buildNupOptions()
	duplexOptions := buildDuplexOptions(printer)

	defaults = normalizeDefaultsForPrinter(defaults, printer)
	copies := defaults.Copies
	nup := defaults.Nup
	scale := defaults.Scale
	duplex := defaults.Duplex
	if duplex == "" {
		duplex = "off"
	}
	pagesValue := fmt.Sprintf("1-%d", totalPages)
	if strings.TrimSpace(state.PagesValue) != "" {
		pagesValue = strings.TrimSpace(state.PagesValue)
	}
	scaleValue := fmt.Sprintf("%d", scale)
	if strings.TrimSpace(state.ScaleValue) != "" {
		scaleValue = strings.TrimSpace(state.ScaleValue)
	}

	cancelButtonText := state.CancelButtonText
	if cancelButtonText == "" {
		cancelButtonText = "取消"
	}
	printButtonText := state.PrintButtonText
	if printButtonText == "" {
		printButtonText = "开始打印"
	}

	cancelButton := map[string]interface{}{
		"tag":        "button",
		"element_id": "cancel_btn",
		"text":       map[string]interface{}{"tag": "plain_text", "content": cancelButtonText},
		"type":       "default",
		"name":       "cancel_btn",
	}
	printButton := map[string]interface{}{
		"tag":        "button",
		"element_id": "print_btn",
		"text":       map[string]interface{}{"tag": "plain_text", "content": printButtonText},
		"type":       "primary_filled",
		"name":       "print_btn",
	}
	if state.Disabled {
		cancelButton["disabled"] = true
		cancelButton["form_action_type"] = "submit"
		printButton["disabled"] = true
		printButton["form_action_type"] = "submit"
	} else {
		cancelButton["form_action_type"] = "submit"
		cancelButton["behaviors"] = []interface{}{
			map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "cancel", "stage": "print_details", "session_id": sessionID, "printer_id": printer.ID}},
		}
		cancelButton["confirm"] = map[string]interface{}{
			"title": map[string]interface{}{"tag": "plain_text", "content": "取消打印？"},
			"text":  map[string]interface{}{"tag": "plain_text", "content": "将取消本次打印配置"},
		}
		printButton["form_action_type"] = "submit"
		printButton["behaviors"] = []interface{}{
			map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "print", "session_id": sessionID, "printer_id": printer.ID}},
		}
		printButton["confirm"] = map[string]interface{}{
			"title": map[string]interface{}{"tag": "plain_text", "content": "确认打印？"},
			"text":  map[string]interface{}{"tag": "plain_text", "content": "将按所选参数提交打印任务"},
		}
	}

	bodyElements := []interface{}{
		map[string]interface{}{
			"tag":        "markdown",
			"element_id": "file_info",
			"content":    fmt.Sprintf("📄 **%s**　共 %d 页", filename, totalPages),
		},
		map[string]interface{}{
			"tag":        "markdown",
			"element_id": "selected_printer_info",
			"content":    fmt.Sprintf("🖨️ 打印机：**%s**", printer.ID),
		},
		map[string]interface{}{
			"tag":        "form",
			"element_id": "print_form",
			"name":       "print_form",
			"elements": []interface{}{
				map[string]interface{}{
					"tag":           "input",
					"element_id":    "copies_input",
					"name":          "copies",
					"label":         map[string]interface{}{"tag": "plain_text", "content": "份数"},
					"default_value": fmt.Sprintf("%d", copies),
					"width":         "fill",
				},
				map[string]interface{}{
					"tag":           "input",
					"element_id":    "pages_input",
					"name":          "pages",
					"label":         map[string]interface{}{"tag": "plain_text", "content": "页码范围"},
					"default_value": pagesValue,
					"width":         "fill",
				},
				map[string]interface{}{
					"tag":           "input",
					"element_id":    "scale_input",
					"name":          "scale",
					"label":         map[string]interface{}{"tag": "plain_text", "content": "缩放比例（%）"},
					"default_value": scaleValue,
					"width":         "fill",
				},
				map[string]interface{}{
					"tag":            "select_static",
					"element_id":     "nup_select",
					"name":           "nup",
					"placeholder":    map[string]interface{}{"tag": "plain_text", "content": "缩印"},
					"options":        nupOptions,
					"initial_option": optionInitialValue(nupOptions, strconv.Itoa(nup)),
					"width":          "fill",
				},
				map[string]interface{}{
					"tag":            "select_static",
					"element_id":     "duplex_select",
					"name":           "duplex",
					"placeholder":    map[string]interface{}{"tag": "plain_text", "content": "单双面"},
					"options":        duplexOptions,
					"initial_option": optionInitialValue(duplexOptions, duplex),
					"width":          "fill",
				},
				map[string]interface{}{
					"tag":                "column_set",
					"element_id":         "btn_cols",
					"flex_mode":          "bisect",
					"horizontal_spacing": "8px",
					"horizontal_align":   "right",
					"columns": []interface{}{
						map[string]interface{}{
							"tag":      "column",
							"width":    "auto",
							"elements": []interface{}{cancelButton},
						},
						map[string]interface{}{
							"tag":      "column",
							"width":    "auto",
							"elements": []interface{}{printButton},
						},
					},
				},
			},
		},
	}
	if strings.TrimSpace(state.StatusText) != "" {
		bodyElements = append(bodyElements, map[string]interface{}{
			"tag":        "markdown",
			"element_id": "status_hint",
			"content":    state.StatusText,
		})
	}
	bodyElements = append(bodyElements,
		map[string]interface{}{"tag": "hr", "element_id": "divider"},
		map[string]interface{}{
			"tag":        "markdown",
			"element_id": "timeout_hint",
			"content":    fmt.Sprintf("⏰ %d 分钟后自动取消任务", displayTimeoutMinutes(state.TimeoutMinutes)),
		},
	)

	return map[string]interface{}{
		"schema": "2.0",
		"config": botCardConfig(),
		"header": map[string]interface{}{
			"template": "blue",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "🖨️ 打印配置",
			},
		},
		"body": map[string]interface{}{
			"elements": bodyElements,
		},
	}
}

func BuildPrintConfigCard(filename string, totalPages int, printer config.PrinterConfig, defaults config.FileTypeDefault, sessionID string) (string, error) {
	b, err := json.Marshal(BuildPrintConfigCardData(filename, totalPages, printer, defaults, sessionID, PrintConfigCardState{}))
	return string(b), err
}

func IsValidPages(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}
		rng := strings.SplitN(part, "-", 2)
		if len(rng) == 2 {
			start, err1 := strconv.Atoi(strings.TrimSpace(rng[0]))
			end, err2 := strconv.Atoi(strings.TrimSpace(rng[1]))
			if err1 != nil || err2 != nil || start < 1 || end < start {
				return false
			}
		} else {
			if _, err := strconv.Atoi(part); err != nil {
				return false
			}
		}
	}
	return true
}

func BuildJobSubmittedCard(jobID, printerID, filename string, copies int, duplex string) (string, error) {
	card := map[string]interface{}{
		"schema": "2.0",
		"config": botCardConfig(),
		"header": map[string]interface{}{
			"template": "green",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "打印任务已提交",
			},
		},
		"body": map[string]interface{}{
			"elements": []interface{}{
				map[string]interface{}{
					"tag":        "markdown",
					"element_id": "job_info",
					"content": fmt.Sprintf(
						"**文件**：%s\n**打印机**：%s\n**份数**：%d\n**模式**：%s",
						filename,
						printerID,
						copies,
						duplex,
					),
				},
				map[string]interface{}{"tag": "hr", "element_id": "divider"},
				map[string]interface{}{
					"tag":        "markdown",
					"element_id": "job_id_note",
					"content":    "任务 ID: " + jobID,
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b), nil
}

func BuildPrintConflictCardData(sessionID string, state PrintConflictCardState) map[string]interface{} {
	continueText := state.ContinueText
	if continueText == "" {
		continueText = "仍然打印"
	}
	cancelText := state.CancelText
	if cancelText == "" {
		cancelText = "取消"
	}
	if state.Nup <= 0 {
		state.Nup = 1
	}
	if state.Copies <= 0 {
		state.Copies = 1
	}
	if strings.TrimSpace(state.Scale) == "" {
		state.Scale = "100"
	}
	if strings.TrimSpace(state.Duplex) == "" {
		state.Duplex = "off"
	}
	if strings.TrimSpace(state.OriginalAction) == "" {
		state.OriginalAction = "print"
	}
	if strings.TrimSpace(state.CancelStage) == "" {
		state.CancelStage = "print_details"
	}

	continueButton := map[string]interface{}{
		"tag":        "button",
		"element_id": "confirm_print_btn",
		"text":       map[string]interface{}{"tag": "plain_text", "content": continueText},
		"type":       "primary_filled",
		"name":       "confirm_print_btn",
	}
	cancelButton := map[string]interface{}{
		"tag":        "button",
		"element_id": "cancel_btn",
		"text":       map[string]interface{}{"tag": "plain_text", "content": cancelText},
		"type":       "default",
		"name":       "cancel_btn",
	}
	if state.Disabled {
		continueButton["disabled"] = true
		cancelButton["disabled"] = true
	} else {
		continueButton["behaviors"] = []interface{}{
			map[string]interface{}{"type": "callback", "value": map[string]interface{}{
				"action":          "confirm_print",
				"session_id":      sessionID,
				"printer_id":      state.PrinterID,
				"copies":          strconv.Itoa(state.Copies),
				"pages":           state.Pages,
				"nup":             strconv.Itoa(state.Nup),
				"scale":           state.Scale,
				"duplex":          state.Duplex,
				"original_action": state.OriginalAction,
				"confirmed":       "true",
			}},
		}
		cancelButton["behaviors"] = []interface{}{
			map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "cancel", "stage": state.CancelStage, "session_id": sessionID, "printer_id": state.PrinterID}},
		}
		cancelButton["confirm"] = map[string]interface{}{
			"title": map[string]interface{}{"tag": "plain_text", "content": "取消打印？"},
			"text":  map[string]interface{}{"tag": "plain_text", "content": "将取消本次打印配置"},
		}
	}

	message := "有人正在使用这台打印机。请去对应打印机出纸口观察是否有未打印完成的页面并提醒对方及时拿取文件。这也可能是误判。"
	if state.Warning != nil && strings.TrimSpace(state.Warning.Message) != "" {
		message = state.Warning.Message
	}
	if state.Warning != nil && state.Warning.FileName != "" {
		message += fmt.Sprintf("\n\n相关文件：%s", state.Warning.FileName)
	}
	if state.Warning != nil && state.Warning.HookExpiresAt != "" {
		message += fmt.Sprintf("\n等待截止：%s", state.Warning.HookExpiresAt)
	}

	bodyElements := []interface{}{
		map[string]interface{}{
			"tag":        "markdown",
			"element_id": "printer_conflict_warning",
			"content":    message,
		},
		map[string]interface{}{
			"tag":                "column_set",
			"element_id":         "confirm_print_cols",
			"flex_mode":          "bisect",
			"horizontal_spacing": "8px",
			"horizontal_align":   "right",
			"columns": []interface{}{
				map[string]interface{}{
					"tag":      "column",
					"width":    "auto",
					"elements": []interface{}{cancelButton},
				},
				map[string]interface{}{
					"tag":      "column",
					"width":    "auto",
					"elements": []interface{}{continueButton},
				},
			},
		},
	}
	if strings.TrimSpace(state.StatusText) != "" {
		bodyElements = append(bodyElements, map[string]interface{}{
			"tag":        "markdown",
			"element_id": "conflict_status",
			"content":    state.StatusText,
		})
	}

	return map[string]interface{}{
		"schema": "2.0",
		"config": botCardConfig(),
		"header": map[string]interface{}{
			"template": "orange",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "打印机可能正在使用",
			},
		},
		"body": map[string]interface{}{
			"elements": bodyElements,
		},
	}
}

func BuildPrintConflictCard(sessionID string, state PrintConflictCardState) (string, error) {
	card := BuildPrintConflictCardData(sessionID, state)
	b, _ := json.Marshal(card)
	return string(b), nil
}

func BuildDuplexContinueCardData(token string, state DuplexContinueCardState) map[string]interface{} {
	continueText := state.ContinueText
	if continueText == "" {
		continueText = "已翻面，继续打印"
	}
	cancelText := state.CancelText
	if cancelText == "" {
		cancelText = "取消剩余"
	}

	continueButton := map[string]interface{}{
		"tag":        "button",
		"element_id": "continue_duplex_btn",
		"text":       map[string]interface{}{"tag": "plain_text", "content": continueText},
		"type":       "primary",
	}
	cancelButton := map[string]interface{}{
		"tag":        "button",
		"element_id": "cancel_duplex_btn",
		"text":       map[string]interface{}{"tag": "plain_text", "content": cancelText},
		"type":       "default",
	}
	if state.Disabled {
		continueButton["disabled"] = true
		cancelButton["disabled"] = true
	} else {
		continueButton["behaviors"] = []interface{}{
			map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "continue_duplex", "token": token}},
		}
		cancelButton["behaviors"] = []interface{}{
			map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "cancel_duplex", "token": token}},
		}
	}

	bodyElements := []interface{}{
		map[string]interface{}{
			"tag":        "markdown",
			"element_id": "duplex_msg",
			"content":    "第一面已完成。请取出纸张**翻面**后放回纸盒，点击继续。",
		},
		map[string]interface{}{
			"tag":                "column_set",
			"element_id":         "duplex_btn_cols",
			"flex_mode":          "bisect",
			"horizontal_spacing": "8px",
			"horizontal_align":   "right",
			"columns": []interface{}{
				map[string]interface{}{
					"tag":      "column",
					"width":    "auto",
					"elements": []interface{}{continueButton},
				},
				map[string]interface{}{
					"tag":      "column",
					"width":    "auto",
					"elements": []interface{}{cancelButton},
				},
			},
		},
	}
	if strings.TrimSpace(state.StatusText) != "" {
		bodyElements = append(bodyElements, map[string]interface{}{
			"tag":        "markdown",
			"element_id": "duplex_status",
			"content":    state.StatusText,
		})
	}

	return map[string]interface{}{
		"schema": "2.0",
		"config": botCardConfig(),
		"header": map[string]interface{}{
			"template": "orange",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "🔄 手动双面打印",
			},
		},
		"body": map[string]interface{}{
			"elements": bodyElements,
		},
	}
}

func BuildDuplexContinueCard(token string) (string, error) {
	card := BuildDuplexContinueCardData(token, DuplexContinueCardState{})
	b, _ := json.Marshal(card)
	return string(b), nil
}

func DisabledButtonPatch(content string) (string, error) {
	partial := map[string]interface{}{
		"disabled": true,
		"text": map[string]interface{}{
			"tag":     "plain_text",
			"content": content,
		},
	}
	b, err := json.Marshal(partial)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
