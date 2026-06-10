package api

import (
	"context"
	"encoding/json"
	"log"

	botcard "goprint/api/bot"
	"goprint/config"
)

type printerOption = botcard.PrinterOption
type printerSelectCardState = botcard.PrinterSelectCardState
type printConfigCardState = botcard.PrintConfigCardState
type duplexContinueCardState = botcard.DuplexContinueCardState

type printConflictCardState struct {
	Disabled       bool
	ContinueText   string
	CancelText     string
	StatusText     string
	Warning        *printerActiveJobWarning
	PrinterID      string
	Copies         int
	Pages          string
	Nup            int
	Scale          string
	Duplex         string
	OriginalAction string
	CancelStage    string
}

func botCardTimeoutMinutes() int {
	minutes := int(botCardTTL().Minutes())
	if minutes <= 0 {
		return 10
	}
	return minutes
}

func buildPrinterOptions(cfg *config.Config) []printerOption {
	return botcard.BuildPrinterOptions(cfg)
}

func buildPrinterSelectCardData(filename string, totalPages int, printers []printerOption, sessionID string, state printerSelectCardState) map[string]interface{} {
	state.TimeoutMinutes = botCardTimeoutMinutes()
	return botcard.BuildPrinterSelectCardData(filename, totalPages, printers, sessionID, state)
}

func buildPrinterSelectCard(filename string, totalPages int, printers []printerOption, sessionID string) (string, error) {
	card := buildPrinterSelectCardData(filename, totalPages, printers, sessionID, printerSelectCardState{})
	b, err := json.Marshal(card)
	return string(b), err
}

func buildPrintConfigCardData(filename string, totalPages int, printer config.PrinterConfig, defaults config.FileTypeDefault, sessionID string, state printConfigCardState) map[string]interface{} {
	state.TimeoutMinutes = botCardTimeoutMinutes()
	return botcard.BuildPrintConfigCardData(filename, totalPages, printer, defaults, sessionID, state)
}

func buildPrintConfigCard(filename string, totalPages int, printer config.PrinterConfig, defaults config.FileTypeDefault, sessionID string) (string, error) {
	card := buildPrintConfigCardData(filename, totalPages, printer, defaults, sessionID, printConfigCardState{})
	b, err := json.Marshal(card)
	return string(b), err
}

func resendPrintConfigCard(cfg *config.Config, session botCardSession, sessionID, chatID, idType string) {
	_ = idType
	pages := session.TotalPages
	if pages <= 0 {
		var err error
		pages, err = countPDFPages(session.SourcePath)
		if err != nil {
			log.Printf("[bot] recount pages for resend: %v", err)
			return
		}
	}

	printerID := session.PrinterID
	printer, err := resolveVisibleBotPrinter(cfg, printerID)
	if err != nil {
		visible := cfg.VisiblePrinters()
		if len(visible) == 0 {
			log.Printf("[bot] no visible printer for resend session=%s", sessionID)
			return
		}
		printer = visible[0]
	}

	defaults := botSessionDefaults(cfg, session)
	card, err := buildPrintConfigCard(session.Filename, pages, printer, defaults, sessionID)
	if err != nil {
		log.Printf("[bot] rebuild card for resend: %v", err)
		return
	}
	if _, err := sendBotCard(context.Background(), cfg, chatID, session.ChatType, session.RequesterOpenID, card, session.ReplyMessageID); err != nil {
		log.Printf("[bot] resend card failed: %v", err)
	}
}

func isPrinterDuplexOptionSupported(printer config.PrinterConfig, duplex string) bool {
	return botcard.IsPrinterDuplexOptionSupported(printer, duplex)
}

func isValidPages(s string) bool {
	return botcard.IsValidPages(s)
}

func buildJobSubmittedCard(jobID, printerID, filename string, copies int, duplex string) (string, error) {
	return botcard.BuildJobSubmittedCard(jobID, printerID, filename, copies, duplex)
}

func toBotPrintConflictState(state printConflictCardState) botcard.PrintConflictCardState {
	var warning *botcard.ActiveJobWarning
	if state.Warning != nil {
		warning = &botcard.ActiveJobWarning{
			Message:       state.Warning.Message,
			FileName:      state.Warning.FileName,
			HookExpiresAt: state.Warning.HookExpiresAt,
		}
	}
	return botcard.PrintConflictCardState{
		Disabled:       state.Disabled,
		ContinueText:   state.ContinueText,
		CancelText:     state.CancelText,
		StatusText:     state.StatusText,
		Warning:        warning,
		PrinterID:      state.PrinterID,
		Copies:         state.Copies,
		Pages:          state.Pages,
		Nup:            state.Nup,
		Scale:          state.Scale,
		Duplex:         state.Duplex,
		OriginalAction: state.OriginalAction,
		CancelStage:    state.CancelStage,
	}
}

func buildPrintConflictCardData(sessionID string, state printConflictCardState) map[string]interface{} {
	return botcard.BuildPrintConflictCardData(sessionID, toBotPrintConflictState(state))
}

func buildPrintConflictCard(sessionID string, state printConflictCardState) (string, error) {
	return botcard.BuildPrintConflictCard(sessionID, toBotPrintConflictState(state))
}

func buildDuplexContinueCardData(token string, state duplexContinueCardState) map[string]interface{} {
	return botcard.BuildDuplexContinueCardData(token, state)
}

func buildDuplexContinueCard(token string) (string, error) {
	return botcard.BuildDuplexContinueCard(token)
}

func disabledButtonPatch(content string) (string, error) {
	return botcard.DisabledButtonPatch(content)
}
