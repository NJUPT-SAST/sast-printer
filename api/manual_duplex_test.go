package api

import (
	"testing"
	"time"

	"goprint/config"
)

func TestClaimManualDuplexPendingPreventsConcurrentUse(t *testing.T) {
	prevCfg := getConfig()
	SetConfig(testManualDuplexConfig())
	defer SetConfig(prevCfg)

	token, _, err := saveManualDuplexPending("job-1", "printer-1", "/tmp/nonexistent-second-pass.pdf", 1, "", 1)
	if err != nil {
		t.Fatalf("saveManualDuplexPending: %v", err)
	}
	defer deleteManualDuplexPending(token)

	if _, ok := claimManualDuplexPending(token); !ok {
		t.Fatal("first claimManualDuplexPending failed")
	}
	if _, ok := claimManualDuplexPending(token); ok {
		t.Fatal("second claimManualDuplexPending succeeded while action was in progress")
	}

	releaseManualDuplexPending(token)
	if _, ok := claimManualDuplexPending(token); !ok {
		t.Fatal("claimManualDuplexPending failed after release")
	}
}

func TestManualDuplexTimeoutUsesPageCountAndMinimum(t *testing.T) {
	prevCfg := getConfig()
	SetConfig(testManualDuplexConfig())
	defer SetConfig(prevCfg)

	printer := config.PrinterConfig{ID: "printer-1", ManualDuplexPerPageTimeout: "45s"}
	if got, want := manualDuplexTimeout(printer, 3), 10*time.Minute; got != want {
		t.Fatalf("manualDuplexTimeout short job = %s, want %s", got, want)
	}
	if got, want := manualDuplexTimeout(printer, 20), 15*time.Minute; got != want {
		t.Fatalf("manualDuplexTimeout long job = %s, want %s", got, want)
	}
}

func TestExtendManualDuplexPendingRequiresExtendWindow(t *testing.T) {
	prevCfg := getConfig()
	SetConfig(testManualDuplexConfig())
	defer SetConfig(prevCfg)

	token, _, err := saveManualDuplexPending("job-1", "printer-1", "/tmp/nonexistent-second-pass.pdf", 1, "", 1)
	if err != nil {
		t.Fatalf("saveManualDuplexPending: %v", err)
	}
	defer deleteManualDuplexPending(token)

	pending, ok, err := extendManualDuplexPending(token)
	if !ok {
		t.Fatal("extendManualDuplexPending did not find hook")
	}
	if err == nil {
		t.Fatalf("extendManualDuplexPending succeeded too early; expires_at=%s", pending.ExpiresAt)
	}

	manualDuplexStore.Lock()
	item := manualDuplexStore.items[token]
	item.ExpiresAt = time.Now().Add(2 * time.Minute)
	manualDuplexStore.items[token] = item
	manualDuplexStore.Unlock()

	extended, ok, err := extendManualDuplexPending(token)
	if !ok || err != nil {
		t.Fatalf("extendManualDuplexPending near expiry ok=%t err=%v", ok, err)
	}

	remaining := time.Until(extended.ExpiresAt)
	if remaining < 9*time.Minute || remaining > 10*time.Minute {
		t.Fatalf("extended remaining = %s, want close to 10m", remaining)
	}
}

func testManualDuplexConfig() *config.Config {
	return &config.Config{
		Printing: config.PrintingConfig{
			ManualDuplexMinTimeout:   "10m",
			ManualDuplexExtendWindow: "3m",
		},
		Printers: []config.PrinterConfig{
			{
				ID:                         "printer-1",
				URI:                        "ipp://localhost:631/printers/printer-1",
				ManualDuplexPerPageTimeout: "30s",
			},
		},
	}
}
