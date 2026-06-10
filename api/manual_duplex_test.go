package api

import "testing"

func TestClaimManualDuplexPendingPreventsConcurrentUse(t *testing.T) {
	token, _, err := saveManualDuplexPending("job-1", "printer-1", "/tmp/nonexistent-second-pass.pdf", 1, "")
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
