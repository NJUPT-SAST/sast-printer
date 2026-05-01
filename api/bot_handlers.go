package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var botDispatcher *dispatcher.EventDispatcher

func initBotDispatcher() {
	cfg := getConfig()
	if cfg == nil || !cfg.Bot.Enabled {
		return
	}
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

func buildPrintConfigCard(filename string, totalPages int, printers []printerOption, defaults config.FileTypeDefault, sessionID string) (string, error) {
	printerOpts := make([]map[string]interface{}, len(printers))
	for i, p := range printers {
		printerOpts[i] = map[string]interface{}{
			"text":  p.Name,
			"value": p.ID,
		}
	}

	nupOptions := []map[string]interface{}{
		{"text": "1-up (不缩印)", "value": "1"},
		{"text": "2-up", "value": "2"},
		{"text": "4-up", "value": "4"},
		{"text": "6-up", "value": "6"},
	}
	duplexOptions := []map[string]interface{}{
		{"text": "单面", "value": "off"},
		{"text": "双面（自动）", "value": "auto"},
		{"text": "双面（手动）", "value": "manual"},
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
		"config": map[string]interface{}{"wide_screen_mode": true},
		"header": map[string]interface{}{
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "🖨️ 打印配置",
			},
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag": "div",
				"text": map[string]interface{}{
					"tag":     "lark_md",
					"content": fmt.Sprintf("📄 **%s**　共 %d 页", filename, totalPages),
				},
			},
			map[string]interface{}{
				"tag":         "select_static",
				"placeholder": map[string]interface{}{"tag": "plain_text", "content": "选择打印机"},
				"options":     printerOpts,
				"value":       map[string]interface{}{"printer_id": printers[0].ID},
			},
			map[string]interface{}{
				"tag":   "input",
				"label": map[string]interface{}{"tag": "plain_text", "content": "份数"},
				"value": map[string]interface{}{"copies": fmt.Sprintf("%d", copies)},
			},
			map[string]interface{}{
				"tag":   "input",
				"label": map[string]interface{}{"tag": "plain_text", "content": "页码范围"},
				"value": map[string]interface{}{"pages": fmt.Sprintf("1-%d", totalPages)},
			},
			map[string]interface{}{
				"tag":         "select_static",
				"placeholder": map[string]interface{}{"tag": "plain_text", "content": "缩印"},
				"options":     nupOptions,
				"value":       map[string]interface{}{"nup": fmt.Sprintf("%d", nup)},
			},
			map[string]interface{}{
				"tag":         "select_static",
				"placeholder": map[string]interface{}{"tag": "plain_text", "content": "单双面"},
				"options":     duplexOptions,
				"value":       map[string]interface{}{"duplex": duplex},
			},
			map[string]interface{}{"tag": "hr"},
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":   "button",
						"text":  map[string]interface{}{"tag": "plain_text", "content": "取消"},
						"type":  "default",
						"value": map[string]interface{}{"action": "cancel"},
					},
					map[string]interface{}{
						"tag":   "button",
						"text":  map[string]interface{}{"tag": "plain_text", "content": "开始打印"},
						"type":  "primary",
						"value": map[string]interface{}{"action": "print", "session_id": sessionID},
						"confirm": map[string]interface{}{
							"title": map[string]interface{}{"tag": "plain_text", "content": "确认打印？"},
							"text":  map[string]interface{}{"tag": "plain_text", "content": "将按所选参数提交打印任务"},
						},
					},
				},
			},
		},
	}

	b, err := json.Marshal(card)
	return string(b), err
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

func sendCard(ctx context.Context, cfg *config.Config, chatID, receiveIDType, cardJSON string) error {
	client, err := newFeishuClient(cfg)
	if err != nil {
		return err
	}

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("interactive").
			Content(cardJSON).
			Build()).
		Build()

	resp, err := client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("send card: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("send card error: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func sendTextMsg(ctx context.Context, cfg *config.Config, chatID, receiveIDType, text string) error {
	escaped, _ := json.Marshal(text)
	card := fmt.Sprintf(`{"config":{"wide_screen_mode":true},"elements":[{"tag":"div","text":{"tag":"lark_md","content":%s}}]}`, escaped)
	return sendCard(ctx, cfg, chatID, receiveIDType, card)
}

// --- Card session storage ---

type botCardSession struct {
	SourcePath string
	Filename   string
	PrinterID  string
	ChatID     string
	ChatType   string
	CreatedAt  time.Time
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

func getBotSession(id string) (botCardSession, bool) {
	botSessionsMu.RLock()
	defer botSessionsMu.RUnlock()
	s, ok := botSessions[id]
	if !ok || time.Since(s.CreatedAt) > botCardTTL() {
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

	var content struct {
		FileKey  string `json:"file_key"`
		FileName string `json:"file_name"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal([]byte(contentJSON), &content); err != nil {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "无法解析消息")
		return
	}

	var sourcePath string
	var filename string
	var cleanup func()
	var isCloudDoc bool

	switch msgType {
	case "file":
		path, fn, cl, err := downloadBotFile(context.Background(), cfg, ptrStr(msg.MessageId), content.FileKey, content.FileName)
		if err != nil {
			log.Printf("[bot] download file failed: %v", err)
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("下载文件失败：%v", err))
			return
		}
		sourcePath, filename, cleanup = path, fn, cl

	case "text":
		raw := strings.TrimSpace(content.Text)
		if raw == "" {
			return
		}
		docType, token, urlErr := parseFeishuURL(raw)
		if urlErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "请发送文件（PDF/Office/图片）或飞书云文档链接")
			return
		}
		client, cliErr := newFeishuClient(cfg)
		if cliErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "内部错误")
			return
		}
		doc := &feishuDocInfo{Token: token, Type: docType}
		if docType == "wiki" {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, "Bot 暂不支持 Wiki 链接，请发送文件或使用网页端")
			return
		}
		ticket, tkErr := createExportTask(context.Background(), client, "", doc)
		if tkErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("导出失败：%v", tkErr))
			return
		}
		fileToken, pollErr := pollExportTask(context.Background(), client, "", ticket, doc.Token)
		if pollErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("导出超时：%v", pollErr))
			return
		}
		pdfPath, dlErr := downloadExportedFile(context.Background(), client, "", fileToken, doc.Filename)
		if dlErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("下载失败：%v", dlErr))
			return
		}
		sourcePath, filename = pdfPath, doc.Filename
		cleanup = func() { _ = os.Remove(pdfPath) }

	default:
		return
	}
	defer cleanup()

	pages, err := countPDFPages(sourcePath)
	if err != nil {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "无法读取文件页数")
		return
	}

	defaults := cfg.ResolveFileTypeDefault(filename)
	if isCloudDoc {
		defaults = cfg.CloudDocDefault()
	}
	printers := buildPrinterOptions(cfg)

	sessionID := fmt.Sprintf("%s-%d", chatID, time.Now().UnixNano())
	saveBotSession(sessionID, botCardSession{
		SourcePath: sourcePath,
		Filename:   filename,
		PrinterID:  printers[0].ID,
		ChatID:     chatID,
		ChatType:   chatType,
		CreatedAt:  time.Now(),
	})

	card, err := buildPrintConfigCard(filename, pages, printers, defaults, sessionID)
	if err != nil {
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "构建卡片失败")
		return
	}

	if err := sendCard(context.Background(), cfg, chatID, idType, card); err != nil {
		log.Printf("[bot] send card failed: %v", err)
	}
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
	values := event.Event.Action.Value
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
		log.Printf("[bot] card session expired or not found: %s", sessionID)
		return
	}

	chatID := session.ChatID
	idType := receiveIDType(session.ChatType)

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

	printerCfg, err := resolvePrinter(printerID)
	if err != nil {
		log.Printf("[bot] resolve printer %s: %v", printerID, err)
		return
	}

	printSourcePath := session.SourcePath

	// Page selection
	pageCleanup := func() {}
	if pagesStr != "" {
		selectedPath, cleanupPages, err := extractPDFPages(printSourcePath, pagesStr)
		if err != nil {
			log.Printf("[bot] extract pages: %v", err)
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
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, "该打印机不支持双面打印")
		return
	}

	finalPath, err := applyCopiesMode(printSourcePath, copies, true)
	if err != nil {
		log.Printf("[bot] copies: %v", err)
		return
	}
	if finalPath != printSourcePath {
		defer os.Remove(finalPath)
	}

	if duplexMode == "manual" {
		firstPassPath, secondPassPath, cleanupDup, err := prepareManualDuplexFiles(finalPath, printerCfg)
		if err != nil {
			log.Printf("[bot] manual duplex prepare: %v", err)
			return
		}
		defer cleanupDup()

		jobID, err := cupsClient.SubmitJob(printerName, firstPassPath, cups.PrintOptions{Copies: 1})
		if err != nil {
			log.Printf("[bot] submit first pass: %v", err)
			return
		}

		token, _, err := saveManualDuplexPending(jobID, printerID, secondPassPath, 1)
		if err != nil {
			log.Printf("[bot] save duplex pending: %v", err)
			return
		}

		duplexCard, _ := buildDuplexContinueCard(token)
		_ = sendCard(context.Background(), cfg, chatID, idType, duplexCard)

		persistBotJob(cfg, jobID, printerID, session.Filename, copies, true, openID)
		return
	}

	printPath := finalPath
	if duplexMode == "off" && printerCfg.Reverse {
		reversedPath, reverseErr := prepareReversedPDF(finalPath)
		if reverseErr != nil {
			log.Printf("[bot] reverse pdf: %v", reverseErr)
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
		_ = sendTextMsg(context.Background(), cfg, chatID, idType, fmt.Sprintf("打印提交失败：%v", err))
		return
	}

	persistBotJob(cfg, jobID, printerID, session.Filename, copies, duplexMode != "off", openID)
	log.Printf("[bot] print job submitted: job_id=%s printer=%s duplex=%s", jobID, printerID, duplexMode)

	duplexLabel := "单面"
	if duplexMode != "off" {
		duplexLabel = "双面（" + duplexMode + "）"
	}
	card, _ := buildJobSubmittedCard(jobID, printerID, session.Filename, copies, duplexLabel)
	_ = sendCard(context.Background(), cfg, chatID, idType, card)
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
		"config": map[string]interface{}{"wide_screen_mode": true},
		"header": map[string]interface{}{
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "打印任务已提交",
			},
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag": "div",
				"fields": []interface{}{
					map[string]interface{}{"is_short": true, "text": map[string]interface{}{"tag": "lark_md", "content": "**文件**\n" + filename}},
					map[string]interface{}{"is_short": true, "text": map[string]interface{}{"tag": "lark_md", "content": "**打印机**\n" + printerID}},
					map[string]interface{}{"is_short": true, "text": map[string]interface{}{"tag": "lark_md", "content": "**份数**\n" + strconv.Itoa(copies)}},
					map[string]interface{}{"is_short": true, "text": map[string]interface{}{"tag": "lark_md", "content": "**模式**\n" + duplex}},
				},
			},
			map[string]interface{}{"tag": "hr"},
			map[string]interface{}{
				"tag": "note",
				"elements": []interface{}{
					map[string]interface{}{"tag": "plain_text", "content": "任务 ID: " + jobID},
				},
			},
		},
	}
	b, _ := json.Marshal(card)
	return string(b), nil
}

func buildDuplexContinueCard(token string) (string, error) {
	card := map[string]interface{}{
		"header": map[string]interface{}{
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": "🔄 手动双面打印",
			},
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag": "div",
				"text": map[string]interface{}{
					"tag":     "lark_md",
					"content": "第一面已完成。请取出纸张**翻面**后放回纸盒，点击继续。",
				},
			},
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":   "button",
						"text":  map[string]interface{}{"tag": "plain_text", "content": "取消剩余"},
						"type":  "default",
						"value": map[string]interface{}{"action": "cancel_duplex", "token": token},
					},
					map[string]interface{}{
						"tag":   "button",
						"text":  map[string]interface{}{"tag": "plain_text", "content": "已翻面，继续打印"},
						"type":  "primary",
						"value": map[string]interface{}{"action": "continue_duplex", "token": token},
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

