package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"goprint/config"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/go-pdf/fpdf"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

type manualDuplexPending struct {
	PrinterID         string
	RemainingFilePath string
	Copies            int
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

const defaultManualDuplexHookTTL = 30 * time.Minute

var manualDuplexStore = struct {
	sync.RWMutex
	items map[string]manualDuplexPending
}{
	items: map[string]manualDuplexPending{},
}

func saveManualDuplexPending(printerID, remainingFilePath string, copies int) (string, error) {
	token, err := randomToken(16)
	if err != nil {
		return "", err
	}

	now := time.Now()
	ttl := getManualDuplexHookTTL()

	manualDuplexStore.Lock()
	defer manualDuplexStore.Unlock()
	manualDuplexStore.items[token] = manualDuplexPending{
		PrinterID:         printerID,
		RemainingFilePath: remainingFilePath,
		Copies:            copies,
		CreatedAt:         now,
		ExpiresAt:         now.Add(ttl),
	}

	return token, nil
}

func getManualDuplexPending(token string) (manualDuplexPending, bool) {
	manualDuplexStore.Lock()
	defer manualDuplexStore.Unlock()
	item, ok := manualDuplexStore.items[token]
	if !ok {
		return manualDuplexPending{}, false
	}

	if time.Now().After(item.ExpiresAt) {
		_ = os.Remove(item.RemainingFilePath)
		delete(manualDuplexStore.items, token)
		return manualDuplexPending{}, false
	}

	return item, ok
}

func deleteManualDuplexPending(token string) {
	manualDuplexStore.Lock()
	defer manualDuplexStore.Unlock()
	delete(manualDuplexStore.items, token)
}

func prepareManualDuplexFiles(sourcePath string, printerCfg config.PrinterConfig) (string, string, func(), error) {
	totalPages, err := pdfapi.PageCountFile(sourcePath)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to read pdf page count: %w", err)
	}
	if totalPages <= 0 {
		return "", "", nil, fmt.Errorf("invalid pdf page count: %d", totalPages)
	}

	workDir, err := os.MkdirTemp("", "goprint-manual-duplex-")
	if err != nil {
		return "", "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }

	firstPassPath := filepath.Join(workDir, "first-pass.pdf")
	secondPassFile, err := os.CreateTemp("", "goprint-manual-duplex-second-pass-*.pdf")
	if err != nil {
		cleanup()
		return "", "", nil, err
	}
	secondPassPath := secondPassFile.Name()
	_ = secondPassFile.Close()

	workingSource := sourcePath
	if printerCfg.PadToEvenEnabled() && totalPages%2 == 1 {
		blankTail := filepath.Join(workDir, "blank-tail.pdf")
		padded := filepath.Join(workDir, "padded-source.pdf")
		if err := createBlankPDF(blankTail, 1); err != nil {
			_ = os.Remove(secondPassPath)
			cleanup()
			return "", "", nil, err
		}
		if err := pdfapi.MergeCreateFile([]string{sourcePath, blankTail}, padded, false, nil); err != nil {
			_ = os.Remove(secondPassPath)
			cleanup()
			return "", "", nil, fmt.Errorf("failed to append blank tail page: %w", err)
		}
		workingSource = padded
		totalPages++
	}

	firstPassOdd := printerCfg.NormalizedFirstPass() == "odd"
	firstSelectors := pageSelectors(totalPages, !firstPassOdd)
	secondSelectors := pageSelectors(totalPages, firstPassOdd)

	if printerCfg.ReverseFirstPass {
		reverseStrings(firstSelectors)
	}
	if printerCfg.ReverseSecondPass {
		reverseStrings(secondSelectors)
	}

	if len(firstSelectors) == 0 || len(secondSelectors) == 0 {
		_ = os.Remove(secondPassPath)
		cleanup()
		return "", "", nil, fmt.Errorf("invalid page selectors generated for manual duplex")
	}

	if err := pdfapi.TrimFile(workingSource, firstPassPath, firstSelectors, nil); err != nil {
		_ = os.Remove(secondPassPath)
		cleanup()
		return "", "", nil, fmt.Errorf("failed to build first pass pdf: %w", err)
	}

	if err := pdfapi.TrimFile(workingSource, secondPassPath, secondSelectors, nil); err != nil {
		_ = os.Remove(secondPassPath)
		cleanup()
		return "", "", nil, fmt.Errorf("failed to build second pass pdf: %w", err)
	}

	if printerCfg.RotateSecondPass {
		rotated := filepath.Join(workDir, "second-pass-rotated.pdf")
		if err := pdfapi.RotateFile(secondPassPath, rotated, 180, nil, nil); err != nil {
			_ = os.Remove(secondPassPath)
			cleanup()
			return "", "", nil, fmt.Errorf("failed to rotate second pass pdf: %w", err)
		}
		if err := os.Rename(rotated, secondPassPath); err != nil {
			_ = os.Remove(secondPassPath)
			cleanup()
			return "", "", nil, err
		}
	}

	return firstPassPath, secondPassPath, cleanup, nil
}

func pageSelectors(totalPages int, even bool) []string {
	selectors := make([]string, 0, totalPages/2+1)
	for i := 1; i <= totalPages; i++ {
		if even && i%2 == 0 {
			selectors = append(selectors, strconv.Itoa(i))
		}
		if !even && i%2 == 1 {
			selectors = append(selectors, strconv.Itoa(i))
		}
	}
	return selectors
}

func reverseStrings(items []string) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func createBlankPDF(path string, pages int) error {
	if pages <= 0 {
		return fmt.Errorf("invalid blank page count: %d", pages)
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	for i := 0; i < pages; i++ {
		pdf.AddPage()
	}
	if err := pdf.OutputFileAndClose(path); err != nil {
		return fmt.Errorf("failed to create blank pdf: %w", err)
	}
	return nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func getManualDuplexHookTTL() time.Duration {
	cfg := getConfig()
	if cfg == nil {
		return defaultManualDuplexHookTTL
	}

	raw := cfg.Printing.ManualDuplexHookTTL
	if raw == "" {
		return defaultManualDuplexHookTTL
	}

	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return defaultManualDuplexHookTTL
	}

	return ttl
}

func countPDFPages(sourcePath string) (int, error) {
	return pdfapi.PageCountFile(sourcePath)
}
