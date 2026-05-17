package api

import (
	"context"
	"net/http"
	"testing"

	"goprint/config"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
)

func TestBotDispatcherReturnsChallengeWithConfiguredVerificationToken(t *testing.T) {
	dispatcher := newBotEventDispatcher(&config.Config{
		Bot: config.BotConfig{
			Enabled:           true,
			VerificationToken: "verify-token",
		},
	})

	resp := dispatcher.Handle(context.Background(), &larkevent.EventReq{
		Header:     http.Header{},
		Body:       []byte(`{"type":"url_verification","token":"verify-token","challenge":"challenge-code"}`),
		RequestURI: "/api/bot/events",
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("challenge status = %d, want %d, body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}
	if got, want := string(resp.Body), `{"challenge":"challenge-code"}`; got != want {
		t.Fatalf("challenge body = %s, want %s", got, want)
	}
}
