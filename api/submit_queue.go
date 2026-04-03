package api

import "context"

var printSubmitQueue = make(chan struct{}, 1)

func init() {
	printSubmitQueue <- struct{}{}
}

func acquirePrintSubmitQueue(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-printSubmitQueue:
		return nil
	}
}

func releasePrintSubmitQueue() {
	printSubmitQueue <- struct{}{}
}
