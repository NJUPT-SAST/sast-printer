package api

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"goprint/config"
	"goprint/cups"
)

var (
	appConfigMu sync.RWMutex
	appConfig   *config.Config
)

func SetConfig(cfg *config.Config) {
	appConfigMu.Lock()
	defer appConfigMu.Unlock()
	appConfig = cfg
}

func getConfig() *config.Config {
	appConfigMu.RLock()
	defer appConfigMu.RUnlock()
	return appConfig
}

func requireConfig() (*config.Config, error) {
	cfg := getConfig()
	if cfg == nil {
		return nil, fmt.Errorf("application config not initialized")
	}
	return cfg, nil
}

func resolvePrinter(printerID string) (config.PrinterConfig, error) {
	cfg, err := requireConfig()
	if err != nil {
		return config.PrinterConfig{}, err
	}

	printer, ok := cfg.GetPrinterByID(printerID)
	if !ok {
		return config.PrinterConfig{}, fmt.Errorf("printer not configured: %s", printerID)
	}

	return printer, nil
}

func parsePrinterURI(printerURI string) (host string, port int, printerName string, err error) {
	u, err := url.Parse(printerURI)
	if err != nil {
		return "", 0, "", fmt.Errorf("invalid printer uri: %w", err)
	}

	if !strings.EqualFold(u.Scheme, "ipp") && !strings.EqualFold(u.Scheme, "ipps") {
		return "", 0, "", fmt.Errorf("unsupported printer uri scheme: %s", u.Scheme)
	}

	host = u.Hostname()
	if host == "" {
		return "", 0, "", fmt.Errorf("printer uri missing host")
	}

	port = 631
	if rawPort := u.Port(); rawPort != "" {
		parsedPort, parseErr := strconv.Atoi(rawPort)
		if parseErr != nil {
			return "", 0, "", fmt.Errorf("invalid printer uri port: %s", rawPort)
		}
		port = parsedPort
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "printers" || parts[1] == "" {
		return "", 0, "", fmt.Errorf("printer uri path should be /printers/<name>")
	}

	printerName = parts[1]
	return host, port, printerName, nil
}

func newCupsClientForPrinter(printer config.PrinterConfig) (*cups.CupsClient, string, error) {
	cfg, err := requireConfig()
	if err != nil {
		return nil, "", err
	}

	host, port, printerName, err := parsePrinterURI(printer.URI)
	if err != nil {
		return nil, "", fmt.Errorf("invalid printer uri for %s: %w", printer.ID, err)
	}

	client := cups.NewCupsClient(host, port, cfg.Printing.IPPUsername)
	return client, printerName, nil
}

func tempDir() string {
	cfg := getConfig()
	if cfg != nil {
		dir := strings.TrimSpace(cfg.Printing.TempDir)
		if dir != "" {
			return dir
		}
	}
	return os.TempDir()
}

func InitTempDir() error {
	dir := tempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create temp dir %s: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read temp dir %s: %w", dir, err)
	}
	for _, entry := range entries {
		_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
	}
	return nil
}
