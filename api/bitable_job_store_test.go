package api

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizedJobStatusAcceptsHyphenatedManualContinue(t *testing.T) {
	if got := normalizedJobStatus(" pending-manual-continue "); got != "pending_manual_continue" {
		t.Fatalf("normalizedJobStatus = %q, want pending_manual_continue", got)
	}
}

func TestFieldAsPersonNameSupportsFeishuShapes(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want string
	}{
		{
			name: "single map",
			in:   map[string]interface{}{"name": "Alice", "id": "ou_1"},
			want: "Alice",
		},
		{
			name: "single string map",
			in:   map[string]string{"en_name": "Bob", "id": "ou_2"},
			want: "Bob",
		},
		{
			name: "list fallback id",
			in:   []interface{}{map[string]interface{}{"id": "ou_3"}},
			want: "ou_3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fieldAsPersonName(tt.in); got != tt.want {
				t.Fatalf("fieldAsPersonName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatActiveJobWarningMessage(t *testing.T) {
	warning := &printerActiveJobWarning{
		Type:     "manual_duplex",
		UserName: "Alice",
	}
	got := formatActiveJobWarningMessage(warning)

	for _, want := range []string{
		"Alice正在进行手动双面打印翻面",
		"请去对应打印机出纸口观察是否有未打印完成的页面",
		"这也可能是误判",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning message %q does not contain %q", got, want)
		}
	}
}

func TestNewerActiveWarningUsesSubmittedTimeForPrinting(t *testing.T) {
	current := &printerActiveJobWarning{
		Type:          "printing",
		SubmittedTime: time.Date(2026, 6, 11, 9, 0, 0, 0, time.Local),
	}
	candidate := &printerActiveJobWarning{
		Type:          "printing",
		SubmittedTime: time.Date(2026, 6, 11, 10, 0, 0, 0, time.Local),
	}

	if !newerActiveWarning(candidate, current) {
		t.Fatal("newerActiveWarning did not prefer later submitted printing job")
	}
}

func TestNewerActiveWarningUsesExpiresAtForManualDuplex(t *testing.T) {
	current := &printerActiveJobWarning{
		Type:      "manual_duplex",
		ExpiresAt: time.Date(2026, 6, 11, 9, 0, 0, 0, time.Local),
	}
	candidate := &printerActiveJobWarning{
		Type:      "manual_duplex",
		ExpiresAt: time.Date(2026, 6, 11, 10, 0, 0, 0, time.Local),
	}

	if !newerActiveWarning(candidate, current) {
		t.Fatal("newerActiveWarning did not prefer later manual duplex expiry")
	}
}
