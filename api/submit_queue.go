package api

import (
	"context"
	"strings"
	"time"

	"goprint/api/submitqueue"
)

func printQueueWaitTimeout() time.Duration {
	cfg := getConfig()
	if cfg == nil {
		return submitqueue.DefaultWaitTimeout
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(cfg.Printing.QueueWaitTimeout))
	if err != nil || timeout <= 0 {
		return submitqueue.DefaultWaitTimeout
	}
	return timeout
}

func acquirePrintSubmitQueue(ctx context.Context) error {
	return submitqueue.Acquire(ctx, printQueueWaitTimeout())
}

func releasePrintSubmitQueue() {
	submitqueue.Release()
}
