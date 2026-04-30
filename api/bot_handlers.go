package api

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// --- Event data structures ---

type feishuEventWrapper struct {
	Schema string            `json:"schema"`
	Header feishuEventHeader `json:"header"`
	Event  json.RawMessage   `json:"event"`
}

type feishuEventHeader struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	Token     string `json:"token"`
	AppID     string `json:"app_id"`
}

type feishuMessageEvent struct {
	MessageID string              `json:"message_id"`
	ChatID    string              `json:"chat_id"`
	ChatType  string              `json:"chat_type"`
	Message   feishuMessageContent `json:"message"`
	Sender    struct {
		SenderID struct {
			OpenID string `json:"open_id"`
			UserID string `json:"user_id"`
		} `json:"sender_id"`
	} `json:"sender"`
}

type feishuMessageContent struct {
	MessageType string `json:"message_type"`
	Content     string `json:"content"` // JSON string
}

type feishuMsgContentParsed struct {
	FileKey  string `json:"file_key"`
	FileName string `json:"file_name"`
	Text     string `json:"text"`
}

type feishuCardEvent struct {
	Action struct {
		Value    map[string]string `json:"value"`
		ActionID string            `json:"action_id"`
	} `json:"action"`
	OpenID     string `json:"open_id"`
	ChatID     string `json:"chat_id"`
	OpenChatID string `json:"open_chat_id"`
}

type feishuEncryptedEvent struct {
	Encrypt string `json:"encrypt"`
}

type feishuURLChallenge struct {
	Challenge string `json:"challenge"`
	Type      string `json:"type"`
}

// HandleBotEvent handles POST /api/bot/events
func HandleBotEvent(c *gin.Context) {
	cfg := getConfig()
	if cfg == nil || !cfg.Bot.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bot not enabled"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	// Decrypt event if encryption is configured
	if cfg.Bot.EncryptKey != "" {
		decrypted, err := decryptEvent(body, cfg.Bot.EncryptKey)
		if err != nil {
			log.Printf("[bot] event decryption failed: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "decryption failed"})
			return
		}
		body = decrypted
	}

	// URL verification
	var ch feishuURLChallenge
	if json.Unmarshal(body, &ch) == nil && ch.Type == "url_verification" && ch.Challenge != "" {
		c.JSON(http.StatusOK, gin.H{"challenge": ch.Challenge})
		return
	}

	var wrapper feishuEventWrapper
	if err := json.Unmarshal(body, &wrapper); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event format"})
		return
	}

	switch wrapper.Header.EventType {
	case "im.message.receive_v1":
		c.JSON(http.StatusOK, gin.H{})
		go processMessageEvent(cfg, body)
	case "card.action.trigger":
		c.JSON(http.StatusOK, gin.H{})
		go processCardAction(cfg, body)
	default:
		c.JSON(http.StatusOK, gin.H{})
	}
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

func sendCard(ctx context.Context, cfg *config.Config, chatID, cardJSON string) error {
	client, err := newFeishuClient(cfg)
	if err != nil {
		return err
	}

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
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

func sendTextMsg(ctx context.Context, cfg *config.Config, chatID, text string) error {
	escaped, _ := json.Marshal(text)
	card := fmt.Sprintf(`{"config":{"wide_screen_mode":true},"elements":[{"tag":"markdown","content":%s}]}`, escaped)
	return sendCard(ctx, cfg, chatID, card)
}

// --- Card session storage ---

type botCardSession struct {
	SourcePath string
	Filename   string
	PrinterID  string
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

func processMessageEvent(cfg *config.Config, body []byte) {
	var wrapper feishuEventWrapper
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return
	}
	var msg feishuMessageEvent
	if err := json.Unmarshal(wrapper.Event, &msg); err != nil {
		return
	}

	chatID := msg.ChatID
	msgType := msg.Message.MessageType

	var content feishuMsgContentParsed
	if err := json.Unmarshal([]byte(msg.Message.Content), &content); err != nil {
		_ = sendTextMsg(context.Background(), cfg, chatID, "无法解析消息")
		return
	}

	var sourcePath string
	var filename string
	var cleanup func()
	var isCloudDoc bool

	switch msgType {
	case "file":
		path, fn, cl, err := downloadBotFile(context.Background(), cfg, msg.MessageID, content.FileKey, content.FileName)
		if err != nil {
			log.Printf("[bot] download file failed: %v", err)
			_ = sendTextMsg(context.Background(), cfg, chatID, fmt.Sprintf("下载文件失败：%v", err))
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
			_ = sendTextMsg(context.Background(), cfg, chatID, "请发送文件（PDF/Office/图片）或飞书云文档链接")
			return
		}
		client, cliErr := newFeishuClient(cfg)
		if cliErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, "内部错误")
			return
		}
		doc := &feishuDocInfo{Token: token, Type: docType}
		if docType == "wiki" {
			_ = sendTextMsg(context.Background(), cfg, chatID, "Bot 暂不支持 Wiki 链接，请发送文件或使用网页端")
			return
		}
		ticket, tkErr := createExportTask(context.Background(), client, "", doc)
		if tkErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, fmt.Sprintf("导出失败：%v", tkErr))
			return
		}
		fileToken, pollErr := pollExportTask(context.Background(), client, "", ticket, doc.Token)
		if pollErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, fmt.Sprintf("导出超时：%v", pollErr))
			return
		}
		pdfPath, dlErr := downloadExportedFile(context.Background(), client, "", fileToken, doc.Filename)
		if dlErr != nil {
			_ = sendTextMsg(context.Background(), cfg, chatID, fmt.Sprintf("下载失败：%v", dlErr))
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
		_ = sendTextMsg(context.Background(), cfg, chatID, "无法读取文件页数")
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
		CreatedAt:  time.Now(),
	})

	card, err := buildPrintConfigCard(filename, pages, printers, defaults, sessionID)
	if err != nil {
		_ = sendTextMsg(context.Background(), cfg, chatID, "构建卡片失败")
		return
	}

	if err := sendCard(context.Background(), cfg, chatID, card); err != nil {
		log.Printf("[bot] send card failed: %v", err)
	}
}

func downloadBotFile(ctx context.Context, cfg *config.Config, messageID, fileKey, fileName string) (string, string, func(), error) {
	client, err := newFeishuClient(cfg)
	if err != nil {
		return "", "", nil, err
	}

	tokenReq := larkcore.SelfBuiltTenantAccessTokenReq{
		AppID:     cfg.Auth.Feishu.AppID,
		AppSecret: cfg.Auth.Feishu.AppSecret,
	}
	tokenResp, err := client.GetTenantAccessTokenBySelfBuiltApp(ctx, &tokenReq)
	if err != nil {
		return "", "", nil, fmt.Errorf("get tenant token: %w", err)
	}
	if !tokenResp.Success() {
		return "", "", nil, fmt.Errorf("tenant token error: code=%d msg=%s", tokenResp.Code, tokenResp.Msg)
	}
	tenantToken := tokenResp.TenantAccessToken

	url := fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages/%s/resources/%s?type=file", messageID, fileKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tenantToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", nil, fmt.Errorf("download status %d", resp.StatusCode)
	}

	workDir := cfg.Bot.WorkDir
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", "", nil, err
	}

		safeName := sanitizeFilename(fileName)
		if !filepath.IsLocal(safeName) {
			return "", "", nil, fmt.Errorf("invalid filename: %s", fileName)
		}
		absWorkDir, err := filepath.Abs(filepath.Clean(workDir))
		if err != nil {
			return "", "", nil, err
		}
		outPath := filepath.Join(absWorkDir, safeName)

		f, err := os.Create(outPath)
		if err != nil {
			return "", "", nil, err
		}
		defer f.Close()

		if _, err := io.Copy(f, resp.Body); err != nil {
			_ = os.Remove(outPath)
			return "", "", nil, err
		}

		return outPath, fileName, func() { _ = os.Remove(outPath) }, nil
}

// --- Card action handling ---

func processCardAction(cfg *config.Config, body []byte) {
	var cardEvt feishuCardEvent
	if err := json.Unmarshal(body, &cardEvt); err != nil {
		return
	}

	action := cardEvt.Action.Value["action"]
	switch action {
	case "cancel":
		log.Printf("[bot] card action: cancel")
	case "print":
		handleBotPrint(cfg, cardEvt)
	case "continue_duplex":
		handleBotDuplexContinue(cfg, cardEvt)
	case "cancel_duplex":
		handleBotDuplexCancel(cfg, cardEvt)
	}
}

func handleBotPrint(cfg *config.Config, evt feishuCardEvent) {
	values := evt.Action.Value
	sessionID := values["session_id"]
	session, ok := getBotSession(sessionID)
	if !ok {
		log.Printf("[bot] card session expired or not found: %s", sessionID)
		return
	}

	printerID := values["printer_id"]
	copies, _ := strconv.Atoi(values["copies"])
	if copies <= 0 {
		copies = 1
	}
	pagesStr := strings.TrimSpace(values["pages"])
	nup, _ := strconv.Atoi(values["nup"])
	if nup <= 0 {
		nup = 1
	}
	duplex := strings.TrimSpace(values["duplex"])
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
		_ = sendTextMsg(context.Background(), cfg, evt.ChatID, "该打印机不支持双面打印")
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
		_ = sendCard(context.Background(), cfg, evt.ChatID, duplexCard)

		persistBotJob(cfg, jobID, printerID, session.Filename, copies, true, evt.OpenID)
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
		_ = sendTextMsg(context.Background(), cfg, evt.ChatID, fmt.Sprintf("打印提交失败：%v", err))
		return
	}

	persistBotJob(cfg, jobID, printerID, session.Filename, copies, duplexMode != "off", evt.OpenID)
	log.Printf("[bot] print job submitted: job_id=%s printer=%s duplex=%s", jobID, printerID, duplexMode)
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

func handleBotDuplexContinue(cfg *config.Config, evt feishuCardEvent) {
	token := evt.Action.Value["token"]
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

	persistBotJob(cfg, jobID, pending.PrinterID, "manual-duplex-continue", pending.Copies, true, evt.OpenID)
	log.Printf("[bot] manual duplex continue: job_id=%s", jobID)
}

func handleBotDuplexCancel(cfg *config.Config, evt feishuCardEvent) {
	token := evt.Action.Value["token"]
	pending, ok := getManualDuplexPending(token)
	if !ok {
		return
	}
	_ = os.Remove(pending.RemainingFilePath)
	deleteManualDuplexPending(token)
	log.Printf("[bot] manual duplex cancelled: token=%s", token)
}

// decryptEvent 解密飞书加密事件。
// encryptKey: 飞书开放平台事件订阅配置的 Encrypt Key。
// 飞书加密算法: AES-256-CBC, key=SHA256(encryptKey), IV=ciphertext前16字节, PKCS7填充。
func decryptEvent(body []byte, encryptKey string) ([]byte, error) {
	var encrypted feishuEncryptedEvent
	if err := json.Unmarshal(body, &encrypted); err != nil {
		return nil, fmt.Errorf("parse encrypted event: %w", err)
	}
	if encrypted.Encrypt == "" {
		return nil, fmt.Errorf("empty encrypt field")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encrypted.Encrypt)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	if len(ciphertext) < 16 {
		return nil, fmt.Errorf("ciphertext too short: %d bytes", len(ciphertext))
	}

	keyHash := sha256.Sum256([]byte(encryptKey))
	iv := ciphertext[:16]
	data := ciphertext[16:]

	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}

	if len(data)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("ciphertext not block-aligned")
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plain := make([]byte, len(data))
	mode.CryptBlocks(plain, data)

	// PKCS7 unpad
	plain, err = pkcs7Unpad(plain)
	if err != nil {
		return nil, fmt.Errorf("pkcs7 unpad: %w", err)
	}

	return plain, nil
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(data) {
		return nil, fmt.Errorf("invalid padding length: %d", padLen)
	}
	for i := len(data) - padLen; i < len(data); i++ {
		if data[i] != byte(padLen) {
			return nil, fmt.Errorf("invalid padding byte at position %d", i)
		}
	}
	return data[:len(data)-padLen], nil
}
