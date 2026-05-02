package api

import (
	"context"
	"encoding/json"
	"fmt"
	"goprint/config"
	"goprint/cups"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

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
		SourcePath:     persistedPath,
		Filename:       filename,
		PrinterID:      printers[0].ID,
		ChatID:         chatID,
		CardID:         cardID,
		ChatType:       chatType,
		CreatedAt:      time.Now(),
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

	if session.CardID != "" {
		if err := disableCardButtons(context.Background(), cfg, session.CardID); err != nil {
			log.Printf("[bot] disable card buttons card=%s: %v", session.CardID, err)
		}
	} else {
		log.Printf("[bot] cardID is empty, skip disableButtons session=%s", sessionID)
	}

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
