package api

import "testing"

func TestBotSelectedDuplexModeHonorsSingleSidedSelection(t *testing.T) {
	if got := botSelectedDuplexMode("off", 8); got != "off" {
		t.Fatalf("duplex mode = %s, want off", got)
	}
}

func TestBotSelectedDuplexModeKeepsManualSelection(t *testing.T) {
	if got := botSelectedDuplexMode("manual", 8); got != "manual" {
		t.Fatalf("duplex mode = %s, want manual", got)
	}
}

func TestBotSelectedDuplexModeForcesSinglePageToSingleSided(t *testing.T) {
	if got := botSelectedDuplexMode("manual", 1); got != "off" {
		t.Fatalf("duplex mode = %s, want off", got)
	}
}
