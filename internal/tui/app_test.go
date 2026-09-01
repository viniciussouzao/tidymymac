package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/elevate"
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
		currentScreen:     screenReview,
		executeMode:       true,
		registry:          registry,
		scanningScr:       scanning,
		reviewScr:         screens.NewReview(scanning.Results(), true, registry, false),
		reviewScanResults: scanning.Results(),
		isElevated:        false,
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

func TestUpdateReview_UpDownTogglesAuthenticateSudoDuringConfirmSudo(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	scanResult := &cleaner.ScanResult{
		Category:   cleaner.CategoryTemp,
		TotalFiles: 1,
		TotalSize:  1024,
		Entries:    []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}},
	}
	scanning := screens.NewScanning([]string{string(cleaner.CategoryTemp)}, registry)
	scanning.UpdateScanResult(cleaner.CategoryTemp, scanResult, nil)

	app := App{
		currentScreen:     screenReview,
		executeMode:       true,
		registry:          registry,
		scanningScr:       scanning,
		reviewScr:         screens.NewReview(scanning.Results(), true, registry, false),
		reviewScanResults: scanning.Results(),
	}

	model, _ := app.updateReview(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if app.reviewScr.ConfirmState != screens.ConfirmSudo {
		t.Fatalf("ConfirmState = %v, want ConfirmSudo", app.reviewScr.ConfirmState)
	}
	if app.reviewScr.AuthenticateSudo {
		t.Fatal("AuthenticateSudo default = true, want false (skip is the safe default)")
	}

	model, _ = app.updateReview(tea.KeyMsg{Type: tea.KeyDown})
	app = model.(App)
	if !app.reviewScr.AuthenticateSudo {
		t.Fatal("expected Down to toggle AuthenticateSudo to true while on the sudo dialog")
	}
	if app.reviewScr.ScrollPos != 0 {
		t.Fatal("Down during ConfirmSudo must toggle the choice, not scroll the underlying file list")
	}

	model, _ = app.updateReview(tea.KeyMsg{Type: tea.KeyUp})
	app = model.(App)
	if app.reviewScr.AuthenticateSudo {
		t.Fatal("expected Up to toggle AuthenticateSudo back to false")
	}
}

// TestUpdateReview_DefaultChoiceNeverTriggersElevation is the important case
// for a returning user: pressing enter through every confirmation without
// ever touching the sudo dialog's toggle -- the exact "mash enter" sequence
// that, before this dialog existed, always meant "skip sudo categories" --
// must keep meaning exactly that, never silently start authenticating as
// root.
func TestUpdateReview_DefaultChoiceNeverTriggersElevation(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	scanResult := &cleaner.ScanResult{
		Category:   cleaner.CategoryTemp,
		TotalFiles: 1,
		TotalSize:  1024,
		Entries:    []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}},
	}
	scanning := screens.NewScanning([]string{string(cleaner.CategoryTemp)}, registry)
	scanning.UpdateScanResult(cleaner.CategoryTemp, scanResult, nil)

	app := App{
		currentScreen:     screenReview,
		executeMode:       true,
		registry:          registry,
		scanningScr:       scanning,
		reviewScr:         screens.NewReview(scanning.Results(), true, registry, false),
		reviewScanResults: scanning.Results(),
		ctx:               context.Background(),
	}

	model, _ := app.updateReview(tea.KeyMsg{Type: tea.KeyEnter}) // ConfirmNone -> ConfirmSudo
	app = model.(App)
	if app.reviewScr.AuthenticateSudo {
		t.Fatal("AuthenticateSudo default = true, want false")
	}
	model, _ = app.updateReview(tea.KeyMsg{Type: tea.KeyEnter}) // ConfirmSudo -> ConfirmExecute
	app = model.(App)
	if app.reviewScr.ConfirmState != screens.ConfirmExecute {
		t.Fatalf("ConfirmState = %v, want ConfirmExecute", app.reviewScr.ConfirmState)
	}

	model, cmd := app.updateReview(tea.KeyMsg{Type: tea.KeyEnter}) // ConfirmExecute -> cleaning
	app = model.(App)
	if cmd != nil {
		t.Fatal("expected no elevation command to be dispatched without an explicit opt-in")
	}
	if app.cleaningScr.Categories[0].Status != "skipped" {
		t.Fatalf("Status = %q, want skipped", app.cleaningScr.Categories[0].Status)
	}
	if !strings.Contains(app.cleaningScr.Categories[0].SkipReason, "chose not to authenticate") {
		t.Fatalf("SkipReason = %q, want it to mention the user's choice", app.cleaningScr.Categories[0].SkipReason)
	}
}

// TestUpdateReview_ExplicitAuthenticateChoiceTriggersElevation is the
// opt-in counterpart: only after actively toggling to Authenticate does
// confirming dispatch the elevation command.
func TestUpdateReview_ExplicitAuthenticateChoiceTriggersElevation(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	scanResult := &cleaner.ScanResult{
		Category:   cleaner.CategoryTemp,
		TotalFiles: 1,
		TotalSize:  1024,
		Entries:    []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}},
	}
	scanning := screens.NewScanning([]string{string(cleaner.CategoryTemp)}, registry)
	scanning.UpdateScanResult(cleaner.CategoryTemp, scanResult, nil)

	app := App{
		currentScreen:     screenReview,
		executeMode:       true,
		registry:          registry,
		scanningScr:       scanning,
		reviewScr:         screens.NewReview(scanning.Results(), true, registry, false),
		reviewScanResults: scanning.Results(),
		ctx:               context.Background(),
	}

	model, _ := app.updateReview(tea.KeyMsg{Type: tea.KeyEnter}) // ConfirmNone -> ConfirmSudo
	app = model.(App)
	model, _ = app.updateReview(tea.KeyMsg{Type: tea.KeyDown}) // toggle to Authenticate
	app = model.(App)
	if !app.reviewScr.AuthenticateSudo {
		t.Fatal("expected Authenticate to be selected after toggling")
	}
	model, _ = app.updateReview(tea.KeyMsg{Type: tea.KeyEnter}) // ConfirmSudo -> ConfirmExecute
	app = model.(App)
	model, cmd := app.updateReview(tea.KeyMsg{Type: tea.KeyEnter}) // ConfirmExecute -> cleaning
	app = model.(App)

	if cmd == nil {
		t.Fatal("expected an elevation command to be dispatched after explicitly choosing to authenticate")
	}
	if app.cleaningScr.Categories[0].Status != "cleaning" {
		t.Fatalf("Status = %q, want cleaning (elevation in flight)", app.cleaningScr.Categories[0].Status)
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

func newSudoReviewApp(t *testing.T, entries []cleaner.FileEntry) App {
	t.Helper()

	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: len(entries),
			Entries:    entries,
		},
	}

	scanning := screens.NewScanning([]string{string(cleaner.CategoryTemp)}, registry)
	scanning.UpdateScanResult(cleaner.CategoryTemp, results[cleaner.CategoryTemp], nil)

	return App{
		currentScreen: screenCleaning,
		executeMode:   true,
		registry:      registry,
		scanningScr:   scanning,
		cleaningScr:   screens.NewCleaningModel(results, false),
		reviewScr:     screens.NewReview(results, true, registry, false),
		isElevated:    false,
		ctx:           context.Background(),
	}
}

func TestStartElevation_SkipsCategoryWhenEveryEntryIsProtected(t *testing.T) {
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/secret", Size: 10, Category: cleaner.CategoryTemp, Protected: true}}
	app := newSudoReviewApp(t, entries)

	model, cmd := app.startElevation()
	app = model.(App)

	if cmd != nil {
		t.Fatal("expected no command when every sudo entry is protected (nothing to authenticate for)")
	}
	if app.cleaningScr.Categories[0].Status != "skipped" {
		t.Fatalf("Status = %q, want skipped", app.cleaningScr.Categories[0].Status)
	}
	if app.cleaningScr.Categories[0].SkipReason == "" {
		t.Error("expected a non-empty skip reason")
	}
}

func TestStartElevation_BuildsPlanAndMarksCategoryCleaning(t *testing.T) {
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
	app := newSudoReviewApp(t, entries)

	model, cmd := app.startElevation()
	app = model.(App)

	if cmd == nil {
		t.Fatal("expected a command to run the elevated helper")
	}
	if app.cleaningScr.Categories[0].Status != "cleaning" {
		t.Fatalf("Status = %q, want cleaning", app.cleaningScr.Categories[0].Status)
	}
}

func TestHandleElevateComplete_SuccessAppliesPerCategoryOutcome(t *testing.T) {
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
	app := newSudoReviewApp(t, entries)
	app.cleaningScr.Categories[0].Status = "cleaning"

	plan := elevate.Plan{
		Version: elevate.PlanSchemaVersion,
		Categories: []elevate.PlanCategory{
			{Category: cleaner.CategoryTemp, Entries: entries},
		},
	}
	result := elevate.Result{
		Version: elevate.ResultSchemaVersion,
		Clean: commands.CleanResult{
			Categories: []commands.CleanCategoryResult{
				{Category: cleaner.CategoryTemp, DeletedFiles: 1, DeletedSize: 1024},
			},
		},
	}

	model, _ := app.handleElevateComplete(elevateCompleteMsg{plan: plan, result: result, err: nil})
	app = model.(App)

	cat := app.cleaningScr.Categories[0]
	if cat.Status != "done" {
		t.Fatalf("Status = %q, want done", cat.Status)
	}
	if cat.FilesDeleted != 1 || cat.BytesDeleted != 1024 {
		t.Fatalf("FilesDeleted/BytesDeleted = %d/%d, want 1/1024", cat.FilesDeleted, cat.BytesDeleted)
	}
}

func TestHandleElevateComplete_ElevationFailedNeverClaimsPartialProgress(t *testing.T) {
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
	app := newSudoReviewApp(t, entries)
	app.cleaningScr.Categories[0].Status = "cleaning"

	plan := elevate.Plan{
		Version:    elevate.PlanSchemaVersion,
		Categories: []elevate.PlanCategory{{Category: cleaner.CategoryTemp, Entries: entries}},
	}

	model, _ := app.handleElevateComplete(elevateCompleteMsg{
		plan: plan,
		err:  errors.Join(elevate.ErrElevationFailed, errors.New("sudo: 3 incorrect password attempts")),
	})
	app = model.(App)

	cat := app.cleaningScr.Categories[0]
	if cat.Status != "skipped" {
		t.Fatalf("Status = %q, want skipped -- ErrElevationFailed must never be reported as done or error", cat.Status)
	}
	if cat.BytesDeleted != 0 || cat.FilesDeleted != 0 {
		t.Fatalf("BytesDeleted/FilesDeleted = %d/%d, want 0/0 -- nothing was deleted", cat.BytesDeleted, cat.FilesDeleted)
	}
}

func TestHandleElevateComplete_OutcomeUnknownNeverClaimsNothingWasDeleted(t *testing.T) {
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
	app := newSudoReviewApp(t, entries)
	app.cleaningScr.Categories[0].Status = "cleaning"

	plan := elevate.Plan{
		Version:    elevate.PlanSchemaVersion,
		Categories: []elevate.PlanCategory{{Category: cleaner.CategoryTemp, Entries: entries}},
	}

	model, _ := app.handleElevateComplete(elevateCompleteMsg{
		plan: plan,
		err:  errors.Join(elevate.ErrElevationOutcomeUnknown, errors.New("signal: terminated")),
	})
	app = model.(App)

	cat := app.cleaningScr.Categories[0]
	if cat.Status != "error" {
		t.Fatalf("Status = %q, want error (uncertain outcome, not a plain skip)", cat.Status)
	}
	if cat.Error == nil {
		t.Fatal("expected a non-nil Error carrying the outcome-unknown message")
	}
}
