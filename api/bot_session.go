package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

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
