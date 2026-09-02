package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/elevate"
	"github.com/viniciussouzao/tidymymac/internal/history"
	"github.com/viniciussouzao/tidymymac/internal/tui/screens"
)

// TestMain points HOME at a throwaway directory for the whole package. Several
// tests below drive execute-mode runs to completion, and finishCleaning /
// recordElevatedHistory append to ~/.tidymymac/history.json -- without this,
// they write into the developer's real history file.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tidymymac-tui-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// plainMockCleaner is an ordinary non-sudo cleaner that honors a filtered
// entry list, for runs that mix an elevated category with a normal one.
type plainMockCleaner struct{ wholeDomainMockCleaner }

func (m *plainMockCleaner) DeletesWholeDomain() bool { return false }

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

// newMixedReviewApp builds an execute-mode app whose cleaning screen holds a
// sudo category (Temp) followed by an ordinary non-sudo mock category, the
// shape in which the elevated part finishes first and the run then continues
// unprivileged.
func newMixedReviewApp(t *testing.T, tempEntries []cleaner.FileEntry) App {
	t.Helper()

	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())
	registry.Register(&plainMockCleaner{wholeDomainMockCleaner{category: "cat_plain"}})

	plainEntries := []cleaner.FileEntry{{Path: "/Users/vini/Library/Caches/x", Size: 7, Category: "cat_plain"}}
	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {Category: cleaner.CategoryTemp, TotalFiles: len(tempEntries), Entries: tempEntries},
		"cat_plain":          {Category: "cat_plain", TotalFiles: 1, Entries: plainEntries},
	}

	return App{
		currentScreen: screenCleaning,
		executeMode:   true,
		registry:      registry,
		cleaningScr:   screens.NewCleaningModel(results, false),
		reviewScr:     screens.NewReview(results, true, registry, false),
		ctx:           context.Background(),
	}
}

// markCleaning flips the given category to "cleaning" the way startElevation
// does. Categories come from a map, so their index is not stable.
func markCleaning(t *testing.T, app *App, cat cleaner.Category) {
	t.Helper()
	for i := range app.cleaningScr.Categories {
		if app.cleaningScr.Categories[i].Category == cat {
			app.cleaningScr.Categories[i].Status = "cleaning"
			return
		}
	}
	t.Fatalf("category %q not found in cleaning screen", cat)
}

func app0Time() time.Time { return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) }

func loadHistoryRuns(t *testing.T) []history.RunRecord {
	t.Helper()
	rec, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load() error: %v", err)
	}
	return rec.Runs
}

func resetHistory(t *testing.T) {
	t.Helper()
	home, _ := os.UserHomeDir()
	_ = os.RemoveAll(home + "/.tidymymac")
}

func elevatedTempSuccess(entries []cleaner.FileEntry) elevateCompleteMsg {
	return elevateCompleteMsg{
		plan: elevate.Plan{
			Version:    elevate.PlanSchemaVersion,
			Categories: []elevate.PlanCategory{{Category: cleaner.CategoryTemp, Entries: entries}},
		},
		result: elevate.Result{
			Version: elevate.ResultSchemaVersion,
			Clean: commands.CleanResult{
				Categories: []commands.CleanCategoryResult{
					{Category: cleaner.CategoryTemp, DeletedFiles: 1, DeletedSize: 1024},
				},
			},
		},
	}
}

// TestHandleElevateComplete_RecordsHistoryBeforeQuit is the review's F3
// scenario: the root helper has already deleted files, the run moves on to a
// non-sudo category, and the user presses q before it finishes. The quit key
// never reaches finishCleaning, so the elevated deletion must have been
// written to history the moment the helper returned.
func TestHandleElevateComplete_RecordsHistoryBeforeQuit(t *testing.T) {
	resetHistory(t)
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
	app := newMixedReviewApp(t, entries)
	markCleaning(t, &app, cleaner.CategoryTemp)
	ctx, cancel := context.WithCancel(context.Background())
	app.ctx, app.cancel = ctx, cancel

	model, cmd := app.handleElevateComplete(elevatedTempSuccess(entries))
	app = model.(App)
	if cmd == nil {
		t.Fatal("expected the non-sudo category to start cleaning after elevation")
	}
	if app.cleaningScr.Done {
		t.Fatal("run must still be in progress: the non-sudo category has not finished")
	}

	runs := loadHistoryRuns(t)
	if len(runs) != 1 || len(runs[0].Categories) != 1 || runs[0].Categories[0].Name != string(cleaner.CategoryTemp) || runs[0].TotalFiles != 1 {
		t.Fatalf("history after elevation = %+v, want exactly one run holding the Temp deletion", runs)
	}

	model, quit := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	app = model.(App)
	if quit == nil {
		t.Fatal("expected a quit command")
	}
	if ctx.Err() == nil {
		t.Fatal("quit must cancel the in-flight run")
	}
	if got := loadHistoryRuns(t); len(got) != 1 {
		t.Fatalf("history after quit has %d runs, want the elevated record and nothing else", len(got))
	}
}

// TestFinishCleaning_DoesNotDoubleRecordElevatedPart pins the other half of
// the contract: when the run does finish normally, the elevated category is
// not counted a second time, and the non-sudo part gets its own record.
func TestFinishCleaning_DoesNotDoubleRecordElevatedPart(t *testing.T) {
	resetHistory(t)
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
	app := newMixedReviewApp(t, entries)
	markCleaning(t, &app, cleaner.CategoryTemp)

	model, _ := app.handleElevateComplete(elevatedTempSuccess(entries))
	app = model.(App)

	model, _ = app.handleCleanComplete(cleanCompleteMsg{
		category: "cat_plain",
		result:   &cleaner.CleanResult{Category: "cat_plain", FilesDeleted: 1, BytesFreed: 7},
	})
	app = model.(App)
	if !app.cleaningScr.Done {
		t.Fatal("run should be complete")
	}

	runs := loadHistoryRuns(t)
	if len(runs) != 2 {
		t.Fatalf("history has %d runs, want 2 (elevated, then non-sudo)", len(runs))
	}
	if runs[0].Categories[0].Name != string(cleaner.CategoryTemp) || runs[0].TotalFiles != 1 {
		t.Fatalf("first run = %+v, want the Temp deletion only", runs[0])
	}
	if len(runs[1].Categories) != 1 || runs[1].Categories[0].Name != "cat_plain" || runs[1].TotalBytes != 7 {
		t.Fatalf("second run = %+v, want the non-sudo deletion only", runs[1])
	}
}

// TestFinishCleaning_SkipsSecondRecordWhenOnlyElevatedWorkRan: a run made of
// nothing but sudo categories has already been fully recorded by the time
// finishCleaning runs, so it must not append an empty second record.
func TestFinishCleaning_SkipsSecondRecordWhenOnlyElevatedWorkRan(t *testing.T) {
	resetHistory(t)
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
	app := newSudoReviewApp(t, entries)
	markCleaning(t, &app, cleaner.CategoryTemp)

	model, _ := app.handleElevateComplete(elevatedTempSuccess(entries))
	app = model.(App)
	if !app.cleaningScr.Done {
		t.Fatal("run should be complete")
	}
	if runs := loadHistoryRuns(t); len(runs) != 1 || runs[0].TotalFiles != 1 {
		t.Fatalf("history = %+v, want exactly one run", runs)
	}
}

// TestHandleElevateComplete_NoDeletionRecordOnFailureOrUnknown: neither an
// authentication failure nor an unknown outcome may write a history entry
// claiming files were deleted -- there is no observed deletion to record.
func TestHandleElevateComplete_NoDeletionRecordOnFailureOrUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"elevation failed", errors.Join(elevate.ErrElevationFailed, errors.New("sudo: 3 incorrect password attempts"))},
		{"outcome unknown", errors.Join(elevate.ErrElevationOutcomeUnknown, errors.New("signal: terminated"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetHistory(t)
			entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
			app := newMixedReviewApp(t, entries)
			markCleaning(t, &app, cleaner.CategoryTemp)

			msg := elevatedTempSuccess(entries)
			msg.result = elevate.Result{}
			msg.err = tc.err
			model, _ := app.handleElevateComplete(msg)
			app = model.(App)

			for _, run := range loadHistoryRuns(t) {
				if run.TotalFiles != 0 || run.TotalBytes != 0 || len(run.Categories) != 0 {
					t.Fatalf("history run %+v claims a deletion after %s", run, tc.name)
				}
			}
			if _, recorded := app.elevatedRecorded[cleaner.CategoryTemp]; recorded {
				t.Fatal("only a successful elevation is recorded early; failures stay with the final run record")
			}
		})
	}
}

// TestHandleElevateComplete_PartialErrorsSurfaceInResults: per-item failures
// collected on the root side arrive through the helper's JSON result and
// must reach the summary the same way a locally cleaned category's would --
// never rendering as an unqualified success.
func TestHandleElevateComplete_PartialErrorsSurfaceInResults(t *testing.T) {
	resetHistory(t)
	entries := []cleaner.FileEntry{{Path: "/private/var/tmp/foo", Size: 1024, Category: cleaner.CategoryTemp}}
	app := newSudoReviewApp(t, entries)
	markCleaning(t, &app, cleaner.CategoryTemp)

	msg := elevatedTempSuccess(entries)
	msg.result.Clean.Categories[0].PartialErrors = 3
	msg.result.Clean.Categories[0].PartialErrorDetails = []commands.ItemError{
		{Path: "/private/var/tmp/locked", Reason: "operation not permitted"},
	}
	msg.result.Clean.Categories[0].PartialErrorsTruncated = true

	model, _ := app.handleElevateComplete(msg)
	app = model.(App)

	results := app.cleaningScr.Results()
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0]
	if r.FilesDeleted != 1 || r.BytesFreed != 1024 {
		t.Fatalf("deleted counts = %d/%d, want 1/1024 preserved alongside the errors", r.FilesDeleted, r.BytesFreed)
	}
	if len(r.Errors) != 2 {
		t.Fatalf("Errors = %v, want the one detail plus a '2 more' summary", r.Errors)
	}
	if got := r.Errors[0].Error(); !strings.Contains(got, "/private/var/tmp/locked") || !strings.Contains(got, "operation not permitted") {
		t.Fatalf("Errors[0] = %q, want path and reason", got)
	}
	if got := r.Errors[1].Error(); !strings.Contains(got, "2 more") {
		t.Fatalf("Errors[1] = %q, want the truncated remainder", got)
	}

	// And the deletion that did happen is still in the audit trail.
	runs := loadHistoryRuns(t)
	if len(runs) != 1 || runs[0].TotalFiles != 1 {
		t.Fatalf("history = %+v, want the partial deletion recorded", runs)
	}
}

func TestBuildTUIRunRecord_KeepsDeletionsFromCategoriesWithErrors(t *testing.T) {
	record := buildTUIRunRecord([]*cleaner.CleanResult{
		{Category: cleaner.CategoryTemp, FilesDeleted: 5, BytesFreed: 50, Errors: []error{errors.New("one failed")}},
		{Category: "cat_skipped", Skipped: true},
		{Category: "cat_failed", Errors: []error{errors.New("nothing deleted")}},
	}, app0Time(), 0)

	if len(record.Categories) != 1 || record.Categories[0].Name != string(cleaner.CategoryTemp) || record.TotalFiles != 5 {
		t.Fatalf("record = %+v, want only the Temp deletion (with its 5 files) recorded", record)
	}
}
