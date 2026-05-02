package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"goprint/config"
	"goprint/cups"

	"github.com/gin-gonic/gin"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/core/httpserverext"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var botDispatcher *dispatcher.EventDispatcher

func initBotDispatcher() {
	cfg := getConfig()
	if cfg == nil || !cfg.Bot.Enabled {
		return
	}
	startBotSessionCleaner()
	botDispatcher = dispatcher.NewEventDispatcher("", cfg.Bot.EncryptKey).
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			go processMessageEvent(getConfig(), event)
			return nil
		}).
		OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
			go processCardAction(getConfig(), event)
			return nil, nil
		})
}

// HandleBotEvent handles POST /api/bot/events
func HandleBotEvent(c *gin.Context) {
	cfg := getConfig()
	if cfg == nil || !cfg.Bot.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bot not enabled"})
		return
	}
	if botDispatcher == nil {
		initBotDispatcher()
	}
	handler := httpserverext.NewEventHandlerFunc(botDispatcher, larkevent.WithLogLevel(larkcore.LogLevelDebug))
	handler(c.Writer, c.Request)
}

// --- Printer options ---

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

// --- Card builder ---

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
	if _, err := sendCard(context.Background(), cfg, chatID, idType, card, session.ReplyMessageID); err != nil {
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

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func receiveIDType(chatType string) string {
	if chatType == "p2p" {
		return "open_id"
	}
	return "chat_id"
}

func sendCard(ctx context.Context, cfg *config.Config, chatID, receiveIDType, cardJSON, messageID string) (string, error) {
	client, err := newFeishuClient(cfg)
	if err != nil {
		return "", err
	}

	// Step 1: Create card entity via CardKit API
	cardReq := larkcardkit.NewCreateCardReqBuilder().
		Body(larkcardkit.NewCreateCardReqBodyBuilder().
			Type("card_json").
			Data(cardJSON).
			Build()).
		Build()

	cardResp, err := client.Cardkit.V1.Card.Create(ctx, cardReq)
	if err != nil {
		return "", fmt.Errorf("cardkit create: %w", err)
	}
	if !cardResp.Success() {
		return "", fmt.Errorf("cardkit create error: code=%d msg=%s", cardResp.Code, cardResp.Msg)
	}
	cardID := *cardResp.Data.CardId

	// Step 2: Send or reply with the CardKit entity
	content, _ := json.Marshal(map[string]interface{}{
		"type": "card",
		"data": map[string]string{"card_id": cardID},
	})
	contentStr := string(content)

	if messageID != "" {
		replyReq := larkim.NewReplyMessageReqBuilder().
			MessageId(messageID).
			Body(larkim.NewReplyMessageReqBodyBuilder().
				Content(contentStr).
				MsgType("interactive").
				Build()).
			Build()
		resp, replyErr := client.Im.V1.Message.Reply(ctx, replyReq)
		if replyErr != nil {
			return "", fmt.Errorf("reply card: %w", replyErr)
		}
		if !resp.Success() {
			return "", fmt.Errorf("reply card error: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return cardID, nil
	}

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("interactive").
			Content(contentStr).
			Build()).
		Build()

	resp, err := client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("send card: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("send card error: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return cardID, nil
}

func notifyUserCard(ctx context.Context, cfg *config.Config, openID, cardJSON string) {
	cardID, err := sendCard(ctx, cfg, openID, "open_id", cardJSON, "")
	if err != nil {
		log.Printf("[bot] notify user card failed: %v", err)
		return
	}
	log.Printf("[bot] notified user %s card_id=%s", maskSensitive(openID), cardID)
}

func disableCardButtons(ctx context.Context, cfg *config.Config, cardID string) {
	client, err := newFeishuClient(cfg)
	if err != nil {
		log.Printf("[bot] patch card: %v", err)
		return
	}
	for _, el := range []string{"print_btn", "cancel_btn"} {
		patch := `{"disabled":true}`
		if el == "print_btn" {
			patch = `{"disabled":true,"text":{"tag":"plain_text","content":"处理中..."}}`
		}
		req := larkcardkit.NewPatchCardElementReqBuilder().
			CardId(cardID).
			ElementId(el).
			Body(larkcardkit.NewPatchCardElementReqBodyBuilder().
				PartialElement(patch).
				Build()).
			Build()
		_, err := client.Cardkit.V1.CardElement.Patch(ctx, req)
		if err != nil {
			log.Printf("[bot] patch card element %s: %v", el, err)
		}
	}
}

func sendTextMsg(ctx context.Context, cfg *config.Config, chatID, receiveIDType, text, messageID string) error {
	escaped, _ := json.Marshal(text)
	card := fmt.Sprintf(`{"schema":"2.0","body":{"elements":[{"tag":"markdown","element_id":"msg","content":%s}]}}`, escaped)
	_, err := sendCard(ctx, cfg, chatID, receiveIDType, card, messageID)
	return err
}

// --- Card session storage ---

type botCardSession struct {
	SourcePath     string
	Filename       string
	PrinterID      string
	ChatID         string
	ChatType       string
	CardID         string
	ReplyMessageID string
	CreatedAt      time.Time
}

func persistSessionFile(sourcePath string) (string, error) {
	dir := filepath.Join(tempDir(), "bot-sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(sourcePath))
	if err := os.Rename(sourcePath, dst); err != nil {
		return "", fmt.Errorf("rename %s -> %s: %w", sourcePath, dst, err)
	}
	return dst, nil
}

func startBotSessionCleaner() {
	go func() {
		ttl := botCardTTL()
		if ttl <= 0 {
			ttl = 10 * time.Minute
		}
		ticker := time.NewTicker(ttl)
		defer ticker.Stop()
		for range ticker.C {
			var expiredCardIDs []string
			botSessionsMu.Lock()
			for id, s := range botSessions {
				if time.Since(s.CreatedAt) > ttl {
					_ = os.Remove(s.SourcePath)
					if s.CardID != "" {
						expiredCardIDs = append(expiredCardIDs, s.CardID)
					}
					delete(botSessions, id)
				}
			}
			botSessionsMu.Unlock()
			cfg := getConfig()
			for _, cardID := range expiredCardIDs {
				go disableCardButtons(context.Background(), cfg, cardID)
			}
		}
	}()
}

var (
	botSessions   = make(map[string]botCardSession)
	botSessionsMu sync.RWMutex
)

func saveBotSession(id string, s botCardSession) {
	botSessionsMu.Lock()
	defer botSessionsMu.Unlock()
	botSessions[id] = s
}

func deleteBotSession(id string) {
	botSessionsMu.Lock()
	defer botSessionsMu.Unlock()
	delete(botSessions, id)
}

func getBotSession(id string) (botCardSession, bool) {
	botSessionsMu.RLock()
	s, ok := botSessions[id]
	expired := ok && time.Since(s.CreatedAt) > botCardTTL()
	botSessionsMu.RUnlock()

	if expired {
		_ = os.Remove(s.SourcePath)
		deleteBotSession(id)
		return botCardSession{}, false
	}
	if !ok {
		return botCardSession{}, false
	}
	return s, true
}

func botCardTTL() time.Duration {
	cfg := getConfig()
	if cfg == nil || cfg.Bot.CardTimeout == "" {
		return 10 * time.Minute
	}
	d, err := time.ParseDuration(cfg.Bot.CardTimeout)
	if err != nil || d <= 0 {
		return 10 * time.Minute
	}
	return d
}

// --- Message event processing ---

func processMessageEvent(cfg *config.Config, event *larkim.P2MessageReceiveV1) {
	msg := event.Event.Message
	if msg == nil {
		return
	}
	chatID := ptrStr(msg.ChatId)
	chatType := ptrStr(msg.ChatType)
	msgType := ptrStr(msg.MessageType)
	contentJSON := ptrStr(msg.Content)

	// For p2p chats, reply via sender's open_id; for group chats, reply via chat_id.
	if chatType == "p2p" {
		if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
			if sid := ptrStr(event.Event.Sender.SenderId.OpenId); sid != "" {
				chatID = sid
			}
		}
	}
	idType := receiveIDType(chatType)
	messageID := ptrStr(msg.MessageId)

	var content struct {
		FileKey  string `json:"file_key"`
		FileName string `json:"file_name"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal([]byte(contentJSON), &content); err != nil {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "无法解析消息", messageID)
		return
	}

	var sourcePath string
	var filename string
	var cleanup func()
	var isCloudDoc bool

	switch msgType {
	case "file":
		if !isSupportedUploadFile(cfg, content.FileName) {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "不支持的文件类型，请发送 PDF、Office 文档（doc/docx/ppt/pptx）或图片（jpg/png）", messageID)
			return
		}
		path, fn, cl, err := downloadBotFile(context.Background(), cfg, ptrStr(msg.MessageId), content.FileKey, content.FileName)
		if err != nil {
			log.Printf("[bot] download file failed: %v", err)
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("下载文件失败：%v", err), messageID)
			return
		}
		sourcePath, filename, cleanup = path, fn, cl

		// Convert office docs and images to PDF (same pipeline as web flow)
		if isOfficeConvertible(cfg, filename) {
			pdfPath, convErr := convertOfficeToPDF(context.Background(), cfg, sourcePath)
			if convErr != nil {
				log.Printf("[bot] office convert failed: %v", convErr)
				_ = sendTextMsg(context.Background(), cfg, chatID, idType, "文档转换失败，请确保文件格式正确", messageID)
				cleanup()
				return
			}
			oldCleanup := cleanup
			sourcePath = pdfPath
			cleanup = func() { _ = os.Remove(pdfPath); oldCleanup() }
		} else if isImageConvertible(filename) {
			pdfPath, convErr := convertImageToPDF(cfg, sourcePath)
			if convErr != nil {
				log.Printf("[bot] image convert failed: %v", convErr)
				_ = sendTextMsg(context.Background(), cfg, chatID, idType, "图片转换失败", messageID)
				cleanup()
				return
			}
			oldCleanup := cleanup
			sourcePath = pdfPath
			cleanup = func() { _ = os.Remove(pdfPath); oldCleanup() }
		}

	case "text":
		raw := strings.TrimSpace(content.Text)
		if raw == "" {
			return
		}
		client, cliErr := newFeishuClient(cfg)
		if cliErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "内部错误", messageID)
			return
		}
		pdfPath, docFilename, exportErr := exportFeishuDocToPDF(context.Background(), client, "", raw)
		if exportErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("导出失败：%v", exportErr), messageID)
			return
		}
		sourcePath, filename = pdfPath, docFilename
		cleanup = func() { _ = os.Remove(pdfPath) }
		isCloudDoc = true

	default:
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "请发送 PDF/Office 文档/图片文件，或飞书云文档链接", messageID)
		return
	}
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	pages, err := countPDFPages(sourcePath)
	if err != nil {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "无法读取文件页数", messageID)
		return
	}

	defaults := cfg.ResolveFileTypeDefault(filename)
	if isCloudDoc {
		defaults = cfg.CloudDocDefault()
	}
	printers := buildPrinterOptions(cfg)
	if len(printers) == 0 {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "没有可用的打印机", messageID)
		return
	}

	sessionID := fmt.Sprintf("%s-%d", chatID, time.Now().UnixNano())

	card, err := buildPrintConfigCard(filename, pages, printers, defaults, sessionID)
	if err != nil {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "构建卡片失败", messageID)
		return
	}

	cardID, err := sendCard(context.Background(), cfg, chatID, idType, card, messageID)
	if err != nil {
		log.Printf("[bot] send card failed: %v", err)
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "发送卡片失败，请重试", messageID)
		return
	}

	persistedPath, persistErr := persistSessionFile(sourcePath)
	if persistErr != nil {
		log.Printf("[bot] persist session file: %v", persistErr)
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "保存文件失败，请重试", messageID)
		return
	}
	saveBotSession(sessionID, botCardSession{
			ReplyMessageID: messageID,
		SourcePath: persistedPath,
		Filename:   filename,
		PrinterID:  printers[0].ID,
		ChatID:     chatID,
		CardID:     cardID,
		ChatType:   chatType,
		CreatedAt:  time.Now(),
	})
}

func downloadBotFile(ctx context.Context, cfg *config.Config, messageID, fileKey, fileName string) (string, string, func(), error) {
	client, err := newFeishuClient(cfg)
	if err != nil {
		return "", "", nil, err
	}

	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(fileKey).
		Type("file").
		Build()

	resp, err := client.Im.V1.MessageResource.Get(ctx, req)
	if err != nil {
		return "", "", nil, fmt.Errorf("download: %w", err)
	}
	if !resp.Success() {
		return "", "", nil, fmt.Errorf("download error: code=%d msg=%s", resp.Code, resp.Msg)
	}

	f, err := os.CreateTemp(tempDir(), "bot-*")
	if err != nil {
		return "", "", nil, err
	}
	outPath := f.Name()
	f.Close()

	if err := resp.WriteFile(outPath); err != nil {
		_ = os.Remove(outPath)
		return "", "", nil, fmt.Errorf("write file: %w", err)
	}

	return outPath, fileName, func() { _ = os.Remove(outPath) }, nil
}

func cardStr(v map[string]interface{}, key string) string {
	if val, ok := v[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}

// --- Card action handling ---

func processCardAction(cfg *config.Config, event *callback.CardActionTriggerEvent) {
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return
	}
	// Merge button callback value with form component values (v2 form container)
	values := event.Event.Action.Value
	if values == nil {
		values = make(map[string]interface{})
	}
	for k, v := range event.Event.Action.FormValue {
		values[k] = v
	}
	openID := event.Event.Operator.OpenID

	switch cardStr(values, "action") {
	case "cancel":
		log.Printf("[bot] card action: cancel")
	case "print":
		handleBotPrint(cfg, values, openID)
	case "continue_duplex":
		handleBotDuplexContinue(cfg, values, openID)
	case "cancel_duplex":
		handleBotDuplexCancel(cfg, values)
	}
}

func handleBotPrint(cfg *config.Config, values map[string]interface{}, openID string) {
	sessionID := cardStr(values, "session_id")
	session, ok := getBotSession(sessionID)
	if !ok {
		// sessionID is "chatID-timestamp", extract chatID for error reply
		if idx := strings.LastIndex(sessionID, "-"); idx > 0 {
			_ = sendTextMsg(context.Background(), cfg, sessionID[:idx], "open_id", "会话已过期，请重新发送文件", "")
		}
		log.Printf("[bot] card session expired or not found: %s", sessionID)
		return
	}

	chatID := session.ChatID
	idType := receiveIDType(session.ChatType)
	replyMsgID := session.ReplyMessageID

	printerID := cardStr(values, "printer_id")
	copies, _ := strconv.Atoi(cardStr(values, "copies"))
	if copies <= 0 {
		copies = 1
	}
	pagesStr := strings.TrimSpace(cardStr(values, "pages"))
	nup, _ := strconv.Atoi(cardStr(values, "nup"))
	if nup <= 0 {
		nup = 1
	}
	duplex := strings.TrimSpace(cardStr(values, "duplex"))
	if duplex == "" {
		duplex = "off"
	}

	// Validate inputs; re-send config card with error hint on invalid input
	if printerID == "" {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "请选择打印机", replyMsgID)
		return
	}
	if copies < 1 || copies > 99 {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "份数必须为 1-99", replyMsgID)
		resendPrintConfigCard(cfg, session, sessionID, chatID, idType)
		return
	}
	if !isValidPages(pagesStr) {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "页码范围格式无效（如 1-5,7,9-12）", replyMsgID)
		resendPrintConfigCard(cfg, session, sessionID, chatID, idType)
		return
	}
	if nup != 1 && !validNup(nup) {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "无效的缩印选项（支持 1/2/4/6）", replyMsgID)
		resendPrintConfigCard(cfg, session, sessionID, chatID, idType)
		return
	}
	if duplex != "off" && duplex != "auto" && duplex != "manual" {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "无效的双面选项", replyMsgID)
		resendPrintConfigCard(cfg, session, sessionID, chatID, idType)
		return
	}

	printerCfg, err := resolvePrinter(printerID)
	if err != nil {
		log.Printf("[bot] resolve printer %s: %v", printerID, err)
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "打印机配置错误", replyMsgID)
		return
	}

	printSourcePath := session.SourcePath

	// Page selection
	pageCleanup := func() {}
	if pagesStr != "" {
		selectedPath, cleanupPages, err := extractPDFPages(printSourcePath, pagesStr)
		if err != nil {
			log.Printf("[bot] extract pages pagesStr=%q source=%s: %v", pagesStr, printSourcePath, err)
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("页码范围提取失败：%v", err), replyMsgID)
			return
		}
		printSourcePath = selectedPath
		pageCleanup = cleanupPages
	}
	defer pageCleanup()

	// N-up
	nupCleanup := func() {}
	if nup > 1 {
		nupPath, cleanupNup, err := applyNupLayout(printSourcePath, nup)
		if err != nil {
			log.Printf("[bot] nup: %v", err)
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "缩印排版失败", replyMsgID)
			return
		}
		if nupPath != printSourcePath {
			printSourcePath = nupPath
			nupCleanup = cleanupNup
		}
	}
	defer nupCleanup()

	cupsClient, printerName, err := newCupsClientForPrinter(printerCfg)
	if err != nil {
		log.Printf("[bot] cups client: %v", err)
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "打印机连接失败", replyMsgID)
		return
	}

	duplexMode := printerCfg.NormalizedDuplexMode()
	if duplex != "off" {
		duplexMode = duplex
	}
	if pageCount, countErr := countPDFPages(printSourcePath); countErr == nil && pageCount == 1 {
		duplexMode = "off"
	}

	if duplexMode != "off" && printerCfg.NormalizedDuplexMode() == "off" && duplex != "manual" {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "该打印机不支持双面打印", replyMsgID)
		return
	}

	finalPath, err := applyCopiesMode(printSourcePath, copies, true)
	if err != nil {
		log.Printf("[bot] copies: %v", err)
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "份数排版失败", replyMsgID)
		return
	}
	if finalPath != printSourcePath {
		defer os.Remove(finalPath)
	}

	if duplexMode == "manual" {
		firstPassPath, secondPassPath, cleanupDup, err := prepareManualDuplexFiles(finalPath, printerCfg)
		if err != nil {
			log.Printf("[bot] manual duplex prepare: %v", err)
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "手动双面准备失败", replyMsgID)
			return
		}
		defer cleanupDup()

		jobID, err := cupsClient.SubmitJob(printerName, firstPassPath, cups.PrintOptions{Copies: 1})
		if err != nil {
			log.Printf("[bot] submit first pass: %v", err)
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "手动双面提交失败", replyMsgID)
			return
		}

		token, _, err := saveManualDuplexPending(jobID, printerID, secondPassPath, 1, openID)
		if err != nil {
			log.Printf("[bot] save duplex pending: %v", err)
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "保存双面任务失败", replyMsgID)
			return
		}

		duplexCard, err := buildDuplexContinueCard(token)
		if err != nil {
			log.Printf("[bot] build duplex card: %v", err)
		} else {
			_, _ = sendCard(context.Background(), cfg, chatID, idType, duplexCard, replyMsgID)
		}

		persistBotJob(cfg, jobID, printerID, session.Filename, copies, true, openID)
		if session.CardID != "" {
			go disableCardButtons(context.Background(), cfg, session.CardID)
		}
		_ = os.Remove(session.SourcePath)
		deleteBotSession(sessionID)
		return
	}

	printPath := finalPath
	if duplexMode == "off" && printerCfg.Reverse {
		reversedPath, reverseErr := prepareReversedPDF(finalPath)
		if reverseErr != nil {
			log.Printf("[bot] reverse pdf: %v", reverseErr)
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "PDF逆序处理失败", replyMsgID)
			return
		}
		if reversedPath != finalPath {
			defer os.Remove(reversedPath)
			printPath = reversedPath
		}
	}

	printOpts := cups.PrintOptions{Copies: 1, Collate: true}
	if duplexMode == "auto" {
		sides, sideErr := chooseAutoDuplexSides(printPath)
		if sideErr != nil {
			printOpts.Sides = "two-sided-long-edge"
		} else {
			printOpts.Sides = sides
		}
	}

	jobID, err := cupsClient.SubmitJob(printerName, printPath, printOpts)
	if err != nil {
		log.Printf("[bot] submit job: %v", err)
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("打印提交失败：%v", err), replyMsgID)
		return
	}

	persistBotJob(cfg, jobID, printerID, session.Filename, copies, duplexMode != "off", openID)
	if session.CardID != "" {
		go disableCardButtons(context.Background(), cfg, session.CardID)
	}
	_ = os.Remove(session.SourcePath)
	deleteBotSession(sessionID)
	log.Printf("[bot] print job submitted: job_id=%s printer=%s duplex=%s", jobID, printerID, duplexMode)

	duplexLabel := "单面"
	if duplexMode != "off" {
		duplexLabel = "双面（" + duplexMode + "）"
	}
	card, err := buildJobSubmittedCard(jobID, printerID, session.Filename, copies, duplexLabel)
	if err != nil {
		log.Printf("[bot] build submitted card: %v", err)
	} else {
		_, _ = sendCard(context.Background(), cfg, chatID, idType, card, replyMsgID)
	}
}

func persistBotJob(cfg *config.Config, jobID, printerID, filename string, copies int, duplex bool, openID string) {
	store, err := newBitableJobStore(cfg)
	if err != nil {
		log.Printf("[bot] bitable store init failed: %v", err)
		return
	}

	record := printJobRecord{
		JobID:     jobID,
		PrinterID: printerID,
		FileName:  filename,
		Status:    "pending",
		Copies:    copies,
		Duplex:    duplex,
		User:      feishuUserInfo{OpenID: openID},
	}

	if err := store.SaveJob(context.Background(), record); err != nil {
		log.Printf("[bot] bitable persist failed: job_id=%s err=%v", jobID, err)
	}
}

// --- Duplex continuation card ---

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

func handleBotDuplexContinue(cfg *config.Config, values map[string]interface{}, openID string) {
	token := cardStr(values, "token")
	pending, ok := getManualDuplexPending(token)
	if !ok {
		log.Printf("[bot] duplex hook not found: %s", token)
		return
	}

	printerCfg, err := resolvePrinter(pending.PrinterID)
	if err != nil {
		return
	}

	cupsClient, printerName, err := newCupsClientForPrinter(printerCfg)
	if err != nil {
		return
	}

	jobID, err := cupsClient.SubmitJob(printerName, pending.RemainingFilePath, cups.PrintOptions{Copies: 1})
	if err != nil {
		log.Printf("[bot] duplex continue submit failed: %v", err)
		return
	}

	_ = os.Remove(pending.RemainingFilePath)
	deleteManualDuplexPending(token)

	persistBotJob(cfg, jobID, pending.PrinterID, "manual-duplex-continue", pending.Copies, true, openID)
	log.Printf("[bot] manual duplex continue: job_id=%s", jobID)
}

func handleBotDuplexCancel(cfg *config.Config, values map[string]interface{}) {
	token := cardStr(values, "token")
	pending, ok := getManualDuplexPending(token)
	if !ok {
		return
	}
	_ = os.Remove(pending.RemainingFilePath)
	deleteManualDuplexPending(token)
	log.Printf("[bot] manual duplex cancelled: token=%s", token)
}
