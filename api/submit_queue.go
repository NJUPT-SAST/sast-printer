package api

import (
	"context"
	"fmt"
	"strings"
	"time"
)

var printSubmitQueue = make(chan struct{}, 1)

const defaultPrintQueueWaitTimeout = 60 * time.Second

func init() {
	printSubmitQueue <- struct{}{}
}

func printQueueWaitTimeout() time.Duration {
	cfg := getConfig()
	if cfg == nil {
		return defaultPrintQueueWaitTimeout
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(cfg.Printing.QueueWaitTimeout))
	if err != nil || timeout <= 0 {
		return defaultPrintQueueWaitTimeout
	}
	return timeout
}

func acquirePrintSubmitQueue(ctx context.Context) error {
	timeout := printQueueWaitTimeout()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-waitCtx.Done():
		if waitCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("print submit queue wait timeout after %s", timeout)
		}
		return waitCtx.Err()
	case <-printSubmitQueue:
		return nil
	}
}

func releasePrintSubmitQueue() {
	printSubmitQueue <- struct{}{}
}
