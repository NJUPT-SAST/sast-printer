package submitqueue

import (
	"context"
	"fmt"
	"time"
)

const DefaultWaitTimeout = 60 * time.Second

var queue = make(chan struct{}, 1)

func init() {
	queue <- struct{}{}
}

func Acquire(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultWaitTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-waitCtx.Done():
		if waitCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("print submit queue wait timeout after %s", timeout)
		}
		return waitCtx.Err()
	case <-queue:
		return nil
	}
}

func Release() {
	queue <- struct{}{}
}
