package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/tui/screens"
)

// wholeDomainMockCleaner is a test double for a Cleaner that cannot honor a
// filtered entry list (e.g. it shells out to a command that clears its
// entire domain), used to verify startNextClean skips it when protected
// paths are present rather than invoking it with a partial list.
type wholeDomainMockCleaner struct {
	category    cleaner.Category
	cleanCalled bool
}

func (m *wholeDomainMockCleaner) Category() cleaner.Category { return m.category }
func (m *wholeDomainMockCleaner) Name() string               { return "Mock Whole Domain" }
func (m *wholeDomainMockCleaner) Description() string        { return "mock" }
func (m *wholeDomainMockCleaner) RequiresSudo() bool         { return false }
func (m *wholeDomainMockCleaner) DeletesWholeDomain() bool   { return true }

func (m *wholeDomainMockCleaner) Scan(_ context.Context, _ func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	return &cleaner.ScanResult{Category: m.category}, nil
}

func (m *wholeDomainMockCleaner) Clean(_ context.Context, entries []cleaner.FileEntry, dryRun bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	m.cleanCalled = true
	return &cleaner.CleanResult{Category: m.category, DryRun: dryRun, FilesDeleted: len(entries)}, nil
}

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

func TestStartNextClean_SkipsWholeDomainCleanerWhenAnyEntryProtected(t *testing.T) {
	mock := &wholeDomainMockCleaner{category: "cat_a"}
	registry := cleaner.NewRegistry()
	registry.Register(mock)

	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	entries := []cleaner.FileEntry{
		{Path: "/Users/vini/Secrets/file.txt", Size: 100, Category: "cat_a"},
		{Path: "/Users/vini/Downloads/file.txt", Size: 200, Category: "cat_a"},
	}
	entries = cfg.Tag(entries)

	results := map[cleaner.Category]*cleaner.ScanResult{
		"cat_a": {Category: "cat_a", TotalFiles: len(entries), TotalSize: 300, Entries: entries},
	}

	app := App{
		currentScreen: screenCleaning,
		executeMode:   true,
		registry:      registry,
		cleaningScr:   screens.NewCleaningModel(results, false),
		cfg:           cfg,
		ctx:           context.Background(),
	}

	model, _ := app.startNextClean()
	app = model.(App)

	if mock.cleanCalled {
		t.Fatal("a whole-domain cleaner must never be invoked by the TUI when any of its entries are protected")
	}
	if !app.cleaningScr.Done {
		t.Fatal("the single category should have been skipped, leaving nothing pending")
	}
	if app.cleaningScr.Categories[0].Status != "skipped" {
		t.Errorf("Status = %q, want skipped", app.cleaningScr.Categories[0].Status)
	}
}
