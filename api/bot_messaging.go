package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"goprint/config"
)

type botCardDelivery struct {
	CardID             string
	EphemeralMessageID string
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

func sendEphemeralCard(ctx context.Context, cfg *config.Config, chatID, openID, cardJSON string) (string, error) {
	chatID = strings.TrimSpace(chatID)
	openID = strings.TrimSpace(openID)
	if chatID == "" {
		return "", fmt.Errorf("chat_id is required for ephemeral card")
	}
	if openID == "" {
		return "", fmt.Errorf("open_id is required for ephemeral card")
	}

	var card map[string]interface{}
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		return "", fmt.Errorf("parse card json: %w", err)
	}

	token, err := getFeishuTenantAccessToken(ctx, cfg)
	if err != nil {
		return "", err
	}

	body, _ := json.Marshal(map[string]interface{}{
		"chat_id":  chatID,
		"open_id":  openID,
		"msg_type": "interactive",
		"card":     card,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/ephemeral/v1/send", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build ephemeral card request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send ephemeral card: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", fmt.Errorf("parse ephemeral card response status=%d: %w", resp.StatusCode, err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("ephemeral card error: code=%d msg=%s", result.Code, result.Msg)
	}
	if result.Data.MessageID == "" {
		return "", fmt.Errorf("empty ephemeral message_id")
	}
	return result.Data.MessageID, nil
}

func deleteEphemeralCard(ctx context.Context, cfg *config.Config, messageID string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	token, err := getFeishuTenantAccessToken(ctx, cfg)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]string{"message_id": messageID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/ephemeral/v1/delete", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build delete ephemeral card request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete ephemeral card: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return fmt.Errorf("parse delete ephemeral card response status=%d: %w", resp.StatusCode, err)
	}
	if result.Code != 0 {
		return fmt.Errorf("delete ephemeral card error: code=%d msg=%s", result.Code, result.Msg)
	}
	return nil
}

func sendBotCard(ctx context.Context, cfg *config.Config, chatID, chatType, visibleOpenID, cardJSON, messageID string) (botCardDelivery, error) {
	if chatType != "p2p" {
		if strings.TrimSpace(visibleOpenID) == "" {
			return botCardDelivery{}, fmt.Errorf("open_id is required for private group bot card")
		}
		messageID, err := sendEphemeralCard(ctx, cfg, chatID, visibleOpenID, cardJSON)
		if err != nil {
			return botCardDelivery{}, err
		}
		return botCardDelivery{EphemeralMessageID: messageID}, nil
	}

	cardID, err := sendCard(ctx, cfg, chatID, receiveIDType(chatType), cardJSON, messageID)
	if err != nil {
		return botCardDelivery{}, err
	}
	return botCardDelivery{CardID: cardID}, nil
}

func notifyUserCard(ctx context.Context, cfg *config.Config, openID, cardJSON string) {
	cardID, err := sendCard(ctx, cfg, openID, "open_id", cardJSON, "")
	if err != nil {
		log.Printf("[bot] notify user card failed: %v", err)
		return
	}
	log.Printf("[bot] notified user %s card_id=%s", maskSensitive(openID), cardID)
}

func disableCardButtons(ctx context.Context, cfg *config.Config, cardID string) error {
	client, err := newFeishuClient(cfg)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	for _, el := range []string{"print_btn", "cancel_btn"} {
		var element string
		if el == "print_btn" {
			element = `{"tag":"button","element_id":"print_btn","text":{"tag":"plain_text","content":"处理中..."},"type":"primary_filled","disabled":true}`
		} else {
			element = `{"tag":"button","element_id":"cancel_btn","text":{"tag":"plain_text","content":"取消"},"type":"default","disabled":true}`
		}
		req := larkcardkit.NewUpdateCardElementReqBuilder().
			CardId(cardID).
			ElementId(el).
			Body(larkcardkit.NewUpdateCardElementReqBodyBuilder().
				Element(element).
				Build()).
			Build()
		resp, err := client.Cardkit.V1.CardElement.Update(ctx, req)
		if err != nil {
			return fmt.Errorf("update %s: %w", el, err)
		}
		if !resp.Success() {
			return fmt.Errorf("update %s: code=%d msg=%s", el, resp.Code, resp.Msg)
		}
	}
	return nil
}

func sendTextMsg(ctx context.Context, cfg *config.Config, chatID, receiveIDType, text, messageID string) error {
	escaped, _ := json.Marshal(text)
	card := fmt.Sprintf(`{"schema":"2.0","body":{"elements":[{"tag":"markdown","element_id":"msg","content":%s}]}}`, escaped)
	_, err := sendCard(ctx, cfg, chatID, receiveIDType, card, messageID)
	return err
}

func sendBotText(ctx context.Context, cfg *config.Config, chatID, chatType, visibleOpenID, text, messageID string) error {
	escaped, _ := json.Marshal(text)
	card := fmt.Sprintf(`{"schema":"2.0","body":{"elements":[{"tag":"markdown","element_id":"msg","content":%s}]}}`, escaped)
	_, err := sendBotCard(ctx, cfg, chatID, chatType, visibleOpenID, card, messageID)
	return err
}

func sendSessionText(ctx context.Context, cfg *config.Config, session botCardSession, text string) error {
	return sendBotText(ctx, cfg, session.ChatID, session.ChatType, session.RequesterOpenID, text, session.ReplyMessageID)
}

func cardStr(v map[string]interface{}, key string) string {
	if val, ok := v[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}
