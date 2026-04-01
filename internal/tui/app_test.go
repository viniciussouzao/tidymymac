package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/screens"
)

func TestUpdateReviewRequiresSudoAndExecuteConfirmationsInSequence(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	scanResult := &cleaner.ScanResult{
		Category:   cleaner.CategoryTemp,
		TotalFiles: 1,
		TotalSize:  1024,
		Entries: []cleaner.FileEntry{
			{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp},
		},
	}

	scanning := screens.NewScanning([]string{string(cleaner.CategoryTemp)}, registry)
	scanning.UpdateScanResult(cleaner.CategoryTemp, scanResult, nil)

	app := App{
		currentScreen: screenReview,
		executeMode:   true,
		registry:      registry,
		scanningScr:   scanning,
		reviewScr:     screens.NewReview(scanning.Results(), true, registry, false),
		isElevated:    false,
	}

	model, _ := app.updateReview(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if app.reviewScr.ConfirmState != screens.ConfirmSudo {
		t.Fatalf("first enter ConfirmState = %v, want %v", app.reviewScr.ConfirmState, screens.ConfirmSudo)
	}
	if app.currentScreen != screenReview {
		t.Fatalf("first enter currentScreen = %v, want screenReview", app.currentScreen)
	}

	model, _ = app.updateReview(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if app.reviewScr.ConfirmState != screens.ConfirmExecute {
		t.Fatalf("second enter ConfirmState = %v, want %v", app.reviewScr.ConfirmState, screens.ConfirmExecute)
	}
	if app.currentScreen != screenReview {
		t.Fatalf("second enter currentScreen = %v, want screenReview", app.currentScreen)
	}

	model, _ = app.updateReview(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if app.reviewScr.ConfirmState != screens.ConfirmNone {
		t.Fatalf("third enter ConfirmState = %v, want %v", app.reviewScr.ConfirmState, screens.ConfirmNone)
	}
	if app.currentScreen != screenCleaning {
		t.Fatalf("third enter currentScreen = %v, want screenCleaning", app.currentScreen)
	}
}
