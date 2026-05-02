package api

import (
	"context"
	"encoding/json"
	"fmt"
	"goprint/config"
	"log"
	"strconv"
	"strings"
)

type printerOption struct {
	ID    string `json:"id"`
	Name  string `json:"text"`
	Value string `json:"value"`
}

func buildPrinterOptions(cfg *config.Config) []printerOption {
	visible := cfg.VisiblePrinters()
	opts := make([]printerOption, len(visible))
	for i, p := range visible {
		opts[i] = printerOption{ID: p.ID, Name: p.ID, Value: p.ID}
	}
	return opts
}

func nupIndex(nup int) int {
	switch nup {
	case 2:
		return 2
	case 4:
		return 3
	case 6:
		return 4
	default:
		return 1
	}
}

func duplexIndex(duplex string) int {
	switch duplex {
	case "auto":
		return 2
	case "manual":
		return 3
	default:
		return 1
	}
}

func buildPrintConfigCard(filename string, totalPages int, printers []printerOption, defaults config.FileTypeDefault, sessionID string) (string, error) {
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

	nupOptions := []map[string]interface{}{
		{"text": mkOptText("1-up (不缩印)"), "value": "1"},
		{"text": mkOptText("2-up"), "value": "2"},
		{"text": mkOptText("4-up"), "value": "4"},
		{"text": mkOptText("6-up"), "value": "6"},
	}
	duplexOptions := []map[string]interface{}{
		{"text": mkOptText("单面"), "value": "off"},
		{"text": mkOptText("双面（自动）"), "value": "auto"},
		{"text": mkOptText("双面（手动）"), "value": "manual"},
	}

	copies := defaults.Copies
	if copies < 1 {
		copies = 1
	}
	nup := defaults.Nup
	if nup < 1 {
		nup = 1
	}
	duplex := defaults.Duplex
	if duplex == "" {
		duplex = "off"
	}

	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": "blue",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "🖨️ 打印配置",
			},
		},
		"body": map[string]interface{}{
			"elements": []interface{}{
				map[string]interface{}{
					"tag":        "markdown",
					"element_id": "file_info",
					"content":    fmt.Sprintf("📄 **%s**　共 %d 页", filename, totalPages),
				},
				map[string]interface{}{
					"tag":        "form",
					"element_id": "print_form",
					"name":       "print_form",
					"elements": []interface{}{
						map[string]interface{}{
							"tag":           "select_static",
							"element_id":    "printer_select",
							"name":          "printer_id",
							"placeholder":   map[string]interface{}{"tag": "plain_text", "content": "选择打印机"},
							"options":       printerOpts,
							"initial_index": 1,
							"width":         "fill",
						},
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
							"default_value": fmt.Sprintf("1-%d", totalPages),
							"width":         "fill",
						},
						map[string]interface{}{
							"tag":           "select_static",
							"element_id":    "nup_select",
							"name":          "nup",
							"placeholder":   map[string]interface{}{"tag": "plain_text", "content": "缩印"},
							"options":       nupOptions,
							"initial_index": nupIndex(nup),
							"width":         "fill",
						},
						map[string]interface{}{
							"tag":           "select_static",
							"element_id":    "duplex_select",
							"name":          "duplex",
							"placeholder":   map[string]interface{}{"tag": "plain_text", "content": "单双面"},
							"options":       duplexOptions,
							"initial_index": duplexIndex(duplex),
							"width":         "fill",
						},
						map[string]interface{}{
							"tag":                "column_set",
							"element_id":         "btn_cols",
							"flex_mode":          "bisect",
							"horizontal_spacing": "8px",
							"horizontal_align":   "right",
							"columns": []interface{}{
								map[string]interface{}{
									"tag":   "column",
									"width": "auto",
									"elements": []interface{}{
										map[string]interface{}{
											"tag":              "button",
											"element_id":       "cancel_btn",
											"text":             map[string]interface{}{"tag": "plain_text", "content": "取消"},
											"type":             "default",
											"form_action_type": "reset",
											"name":             "cancel_btn",
										},
									},
								},
								map[string]interface{}{
									"tag":   "column",
									"width": "auto",
									"elements": []interface{}{
										map[string]interface{}{
											"tag":              "button",
											"element_id":       "print_btn",
											"text":             map[string]interface{}{"tag": "plain_text", "content": "开始打印"},
											"type":             "primary_filled",
											"form_action_type": "submit",
											"name":             "print_btn",
											"behaviors": []interface{}{
												map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "print", "session_id": sessionID}},
											},
											"confirm": map[string]interface{}{
												"title": map[string]interface{}{"tag": "plain_text", "content": "确认打印？"},
												"text":  map[string]interface{}{"tag": "plain_text", "content": "将按所选参数提交打印任务"},
											},
										},
									},
								},
							},
						},
					},
				},
				map[string]interface{}{"tag": "hr", "element_id": "divider"},
				map[string]interface{}{
					"tag":        "markdown",
					"element_id": "timeout_hint",
					"content":    fmt.Sprintf("⏰ %d 分钟后自动取消任务", int(botCardTTL().Minutes())),
				},
			},
		},
	}

	b, err := json.Marshal(card)
	return string(b), err
}

func resendPrintConfigCard(cfg *config.Config, session botCardSession, sessionID, chatID, idType string) {
	pages, err := countPDFPages(session.SourcePath)
	if err != nil {
		log.Printf("[bot] recount pages for resend: %v", err)
		return
	}
	defaults := cfg.ResolveFileTypeDefault(session.Filename)
	printers := buildPrinterOptions(cfg)
	card, err := buildPrintConfigCard(session.Filename, pages, printers, defaults, sessionID)
	if err != nil {
		log.Printf("[bot] rebuild card for resend: %v", err)
		return
	}
	if _, err := sendBotCard(context.Background(), cfg, chatID, session.ChatType, session.RequesterOpenID, card, session.ReplyMessageID); err != nil {
		log.Printf("[bot] resend card failed: %v", err)
	}
}

func isValidPages(s string) bool {
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

func buildJobSubmittedCard(jobID, printerID, filename string, copies int, duplex string) (string, error) {
	card := map[string]interface{}{
		"schema": "2.0",
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
					"tag":        "div",
					"element_id": "job_info",
					"fields": []interface{}{
						map[string]interface{}{"is_short": true, "text": map[string]interface{}{"tag": "lark_md", "content": "**文件**\n" + filename}},
						map[string]interface{}{"is_short": true, "text": map[string]interface{}{"tag": "lark_md", "content": "**打印机**\n" + printerID}},
						map[string]interface{}{"is_short": true, "text": map[string]interface{}{"tag": "lark_md", "content": "**份数**\n" + strconv.Itoa(copies)}},
						map[string]interface{}{"is_short": true, "text": map[string]interface{}{"tag": "lark_md", "content": "**模式**\n" + duplex}},
					},
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

func buildDuplexContinueCard(token string) (string, error) {
	card := map[string]interface{}{
		"schema": "2.0",
		"header": map[string]interface{}{
			"template": "orange",
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "🔄 手动双面打印",
			},
		},
		"body": map[string]interface{}{
			"elements": []interface{}{
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
							"tag":   "column",
							"width": "auto",
							"elements": []interface{}{
								map[string]interface{}{
									"tag":        "button",
									"element_id": "continue_duplex_btn",
									"text":       map[string]interface{}{"tag": "plain_text", "content": "已翻面，继续打印"},
									"type":       "primary",
									"behaviors": []interface{}{
										map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "continue_duplex", "token": token}},
									},
								},
							},
						},
						map[string]interface{}{
							"tag":   "column",
							"width": "auto",
							"elements": []interface{}{
								map[string]interface{}{
									"tag":        "button",
									"element_id": "cancel_duplex_btn",
									"text":       map[string]interface{}{"tag": "plain_text", "content": "取消剩余"},
									"type":       "default",
									"behaviors": []interface{}{
										map[string]interface{}{"type": "callback", "value": map[string]interface{}{"action": "cancel_duplex", "token": token}},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b), nil
}
