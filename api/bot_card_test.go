package api

import (
	"testing"

	"goprint/config"
)

func TestBuildPrintConfigCardDataDisablesActionButtons(t *testing.T) {
	card := buildPrintConfigCardData(
		"report.pdf",
		3,
		config.PrinterConfig{ID: "printer-a", Visible: true, DuplexMode: "manual"},
		config.FileTypeDefault{Copies: 1, Nup: 1, Duplex: "off"},
		"session-1",
		printConfigCardState{Disabled: true, PrintButtonText: "处理中..."},
	)

	printButton := findCardElement(t, card, "print_btn")
	if disabled, ok := printButton["disabled"].(bool); !ok || !disabled {
		t.Fatalf("print button disabled = %v, want true", printButton["disabled"])
	}
	if _, ok := printButton["behaviors"]; ok {
		t.Fatal("disabled print button should not keep callback behaviors")
	}

	cancelButton := findCardElement(t, card, "cancel_btn")
	if disabled, ok := cancelButton["disabled"].(bool); !ok || !disabled {
		t.Fatalf("cancel button disabled = %v, want true", cancelButton["disabled"])
	}
}

func TestBuildPrinterSelectCardDataOnlyShowsPrinterPicker(t *testing.T) {
	card := buildPrinterSelectCardData(
		"report.pdf",
		3,
		[]printerOption{{ID: "printer-a", Name: "Printer A", Value: "printer-a"}},
		"session-1",
		printerSelectCardState{},
	)

	_ = findCardElement(t, card, "printer_select")
	_ = findCardElement(t, card, "select_printer_btn")
	if element := findCardElementValue(card, "duplex_select"); element != nil {
		t.Fatal("printer selection card should not include duplex details")
	}
	if element := findCardElementValue(card, "nup_select"); element != nil {
		t.Fatal("printer selection card should not include n-up details")
	}
}

func TestBuildPrintConfigCardDataFiltersDuplexByPrinter(t *testing.T) {
	card := buildPrintConfigCardData(
		"report.pdf",
		3,
		config.PrinterConfig{ID: "sast-printer", Visible: true, DuplexMode: "manual"},
		config.FileTypeDefault{Copies: 1, Nup: 1, Duplex: "auto"},
		"session-1",
		printConfigCardState{},
	)

	duplexSelect := findCardElement(t, card, "duplex_select")
	options, ok := duplexSelect["options"].([]map[string]interface{})
	if !ok {
		t.Fatalf("duplex options type = %T, want []map[string]interface{}", duplexSelect["options"])
	}
	if hasOptionValue(options, "auto") {
		t.Fatal("manual-only printer should not show auto duplex option")
	}
	if !hasOptionValue(options, "manual") {
		t.Fatal("manual-only printer should show manual duplex option")
	}
}

func TestBuildDuplexContinueCardDataDisablesActionButtons(t *testing.T) {
	card := buildDuplexContinueCardData("token-1", duplexContinueCardState{Disabled: true})

	continueButton := findCardElement(t, card, "continue_duplex_btn")
	if disabled, ok := continueButton["disabled"].(bool); !ok || !disabled {
		t.Fatalf("continue button disabled = %v, want true", continueButton["disabled"])
	}
	if _, ok := continueButton["behaviors"]; ok {
		t.Fatal("disabled continue button should not keep callback behaviors")
	}

	cancelButton := findCardElement(t, card, "cancel_duplex_btn")
	if disabled, ok := cancelButton["disabled"].(bool); !ok || !disabled {
		t.Fatalf("cancel button disabled = %v, want true", cancelButton["disabled"])
	}
}

func hasOptionValue(options []map[string]interface{}, value string) bool {
	for _, option := range options {
		if option["value"] == value {
			return true
		}
	}
	return false
}

func findCardElement(t *testing.T, node interface{}, elementID string) map[string]interface{} {
	t.Helper()
	if element := findCardElementValue(node, elementID); element != nil {
		return element
	}
	t.Fatalf("element %q not found", elementID)
	return nil
}

func findCardElementValue(node interface{}, elementID string) map[string]interface{} {
	switch v := node.(type) {
	case map[string]interface{}:
		if v["element_id"] == elementID {
			return v
		}
		for _, child := range v {
			if found := findCardElementValue(child, elementID); found != nil {
				return found
			}
		}
	case []interface{}:
		for _, child := range v {
			if found := findCardElementValue(child, elementID); found != nil {
				return found
			}
		}
	}
	return nil
}
