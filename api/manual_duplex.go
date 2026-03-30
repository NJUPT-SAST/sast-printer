package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"goprint/config"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

	if err := buildOrderedPDF(workingSource, firstPassPath, firstSelectors); err != nil {
		_ = os.Remove(secondPassPath)
		cleanup()
		return "", "", nil, fmt.Errorf("failed to build first pass pdf: %w", err)
	}

	if err := buildOrderedPDF(workingSource, secondPassPath, secondSelectors); err != nil {
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

func prepareReversedPDF(sourcePath string) (string, error) {
	totalPages, err := pdfapi.PageCountFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("failed to read pdf page count: %w", err)
	}
	if totalPages <= 1 {
		return sourcePath, nil
	}

	selectors := make([]string, 0, totalPages)
	for i := totalPages; i >= 1; i-- {
		selectors = append(selectors, strconv.Itoa(i))
	}

	tmpFile, err := os.CreateTemp("", "goprint-reverse-*.pdf")
	if err != nil {
		return "", err
	}
	outPath := tmpFile.Name()
	_ = tmpFile.Close()

	if err := buildOrderedPDF(sourcePath, outPath, selectors); err != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("failed to build reversed pdf: %w", err)
	}

	return outPath, nil
}

func ApplySingleSideReverse(sourcePath string) (string, error) {
	return prepareReversedPDF(sourcePath)
}

func buildOrderedPDF(sourcePath, outPath string, selectors []string) error {
	if len(selectors) == 0 {
		return fmt.Errorf("empty selectors")
	}

	workDir, err := os.MkdirTemp("", "goprint-order-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	parts := make([]string, 0, len(selectors))
	for i, selector := range selectors {
		partPath := filepath.Join(workDir, fmt.Sprintf("part-%04d.pdf", i))
		if err := pdfapi.TrimFile(sourcePath, partPath, []string{selector}, nil); err != nil {
			return err
		}
		parts = append(parts, partPath)
	}

	if err := pdfapi.MergeCreateFile(parts, outPath, false, nil); err != nil {
		return err
	}
	return nil
}

func ApplyCollateCopies(sourcePath string, copies int) (string, error) {
	if copies <= 1 {
		return sourcePath, nil
	}

	totalPages, err := pdfapi.PageCountFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("failed to read pdf page count: %w", err)
	}
	if totalPages <= 0 {
		return "", fmt.Errorf("invalid pdf page count: %d", totalPages)
	}

	selectors := make([]string, 0, totalPages*copies)
	for c := 0; c < copies; c++ {
		for p := 1; p <= totalPages; p++ {
			selectors = append(selectors, strconv.Itoa(p))
		}
	}

	tmpFile, err := os.CreateTemp("", "goprint-collate-*.pdf")
	if err != nil {
		return "", err
	}
	outPath := tmpFile.Name()
	_ = tmpFile.Close()

	if err := buildOrderedPDF(sourcePath, outPath, selectors); err != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("failed to build collated pdf: %w", err)
	}

	return outPath, nil
}

func applyCopiesForPDF(sourcePath string, copies int, collate bool) (string, error) {
	if copies <= 1 {
		return sourcePath, nil
	}

	if collate {
		return ApplyCollateCopies(sourcePath, copies)
	}

	return ApplyUncollatedCopies(sourcePath, copies)
}

func ApplyUncollatedCopies(sourcePath string, copies int) (string, error) {
	if copies <= 1 {
		return sourcePath, nil
	}

	totalPages, err := pdfapi.PageCountFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("failed to read pdf page count: %w", err)
	}
	if totalPages <= 0 {
		return "", fmt.Errorf("invalid pdf page count: %d", totalPages)
	}

	selectors := make([]string, 0, totalPages*copies)
	for p := 1; p <= totalPages; p++ {
		for c := 0; c < copies; c++ {
			selectors = append(selectors, strconv.Itoa(p))
		}
	}

	tmpFile, err := os.CreateTemp("", "goprint-uncollate-*.pdf")
	if err != nil {
		return "", err
	}
	outPath := tmpFile.Name()
	_ = tmpFile.Close()

	if err := buildOrderedPDF(sourcePath, outPath, selectors); err != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("failed to build uncollated pdf: %w", err)
	}

	return outPath, nil
}

func BuildManualDuplexPreview(sourcePath string, printerCfg config.PrinterConfig, copies int, collate bool) (string, string, func(), error) {
	firstPassPath, secondPassPath, baseCleanup, err := prepareManualDuplexFiles(sourcePath, printerCfg)
	if err != nil {
		return "", "", nil, err
	}

	firstPreviewPath, err := applyCopiesForPDF(firstPassPath, copies, collate)
	if err != nil {
		baseCleanup()
		_ = os.Remove(secondPassPath)
		return "", "", nil, err
	}

	secondPreviewPath, err := applyCopiesForPDF(secondPassPath, copies, collate)
	if err != nil {
		if firstPreviewPath != firstPassPath {
			_ = os.Remove(firstPreviewPath)
		}
		baseCleanup()
		_ = os.Remove(secondPassPath)
		return "", "", nil, err
	}

	cleanup := func() {
		if firstPreviewPath != firstPassPath {
			_ = os.Remove(firstPreviewPath)
		}
		if secondPreviewPath != secondPassPath {
			_ = os.Remove(secondPreviewPath)
		}
		_ = os.Remove(secondPassPath)
		baseCleanup()
	}

	return firstPreviewPath, secondPreviewPath, cleanup, nil
}

func SavePDFForTest(sourcePath, label string) (string, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return "", fmt.Errorf("source path is empty")
	}

	if err := os.MkdirAll("test", 0o755); err != nil {
		return "", err
	}

	safeLabel := sanitizeLabel(label)
	if safeLabel == "" {
		safeLabel = "pdf"
	}

	fileName := fmt.Sprintf("%s-%s.pdf", time.Now().Format("20060102-150405"), safeLabel)
	outPath := filepath.Join("test", fileName)

	src, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}

	return outPath, nil
}

func sanitizeLabel(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('_')
	}
	return strings.Trim(b.String(), "_")
}
