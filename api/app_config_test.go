package api

import (
	"testing"

	"goprint/config"
)

func TestParsePrinterURIIPPSUsesTLS(t *testing.T) {
	host, port, printerName, useTLS, err := parsePrinterURI("ipps://cups.example.test:8631/printers/secure")
	if err != nil {
		t.Fatalf("parsePrinterURI: %v", err)
	}
	if host != "cups.example.test" || port != 8631 || printerName != "secure" || !useTLS {
		t.Fatalf("parsePrinterURI = host %q port %d printer %q tls %t", host, port, printerName, useTLS)
	}
}

func TestResolveVisiblePrinterRejectsHiddenPrinter(t *testing.T) {
	prevCfg := getConfig()
	SetConfig(&config.Config{
		Printers: []config.PrinterConfig{
			{ID: "hidden", URI: "ipp://localhost:631/printers/hidden", Visible: false},
			{ID: "visible", URI: "ipp://localhost:631/printers/visible", Visible: true},
		},
	})
	defer SetConfig(prevCfg)

	if _, err := resolveVisiblePrinter("visible"); err != nil {
		t.Fatalf("resolveVisiblePrinter visible: %v", err)
	}
	if _, err := resolveVisiblePrinter("hidden"); err == nil {
		t.Fatal("resolveVisiblePrinter hidden succeeded")
	}
}
