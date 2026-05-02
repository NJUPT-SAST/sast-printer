package api

import (
	"context"
	"net/http"

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
