package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	larkcardkit "github.com/larksuite/oapi-sdk-go/v3/service/cardkit/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"goprint/config"
)

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

func cardStr(v map[string]interface{}, key string) string {
	if val, ok := v[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}
