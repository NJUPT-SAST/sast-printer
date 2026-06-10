package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"goprint/config"
)

func TestAcquirePrintSubmitQueueTimesOut(t *testing.T) {
	prevCfg := getConfig()
	SetConfig(&config.Config{
		Printing: config.PrintingConfig{QueueWaitTimeout: "5ms"},
	})
	defer SetConfig(prevCfg)

	select {
	case <-printSubmitQueue:
		defer releasePrintSubmitQueue()
	default:
		t.Fatal("printSubmitQueue was unexpectedly empty before test")
	}

	started := time.Now()
	err := acquirePrintSubmitQueue(context.Background())
	if err == nil {
		t.Fatal("acquirePrintSubmitQueue succeeded while queue was held")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("acquirePrintSubmitQueue error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("acquirePrintSubmitQueue took %s, want quick timeout", elapsed)
	}
}
