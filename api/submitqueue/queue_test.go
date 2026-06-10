package submitqueue

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAcquireTimesOut(t *testing.T) {
	select {
	case <-queue:
		defer Release()
	default:
		t.Fatal("queue was unexpectedly empty before test")
	}

	started := time.Now()
	err := Acquire(context.Background(), 5*time.Millisecond)
	if err == nil {
		t.Fatal("Acquire succeeded while queue was held")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("Acquire error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Acquire took %s, want quick timeout", elapsed)
	}
}
