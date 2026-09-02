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

// splitPrivilegeCleaner is a RequiresSudo cleaner that needs root only for
// entries whose path is prefixed "/sudo/", standing in for Temp's real /tmp
// vs $TMPDIR split (see internal/cleaner.PrivilegeSplitter). Mirrors
// cmd/clean_elevate_test.go's cleaner of the same name and shape -- test
// types aren't importable across packages.
type splitPrivilegeCleaner struct {
	category cleaner.Category

	cleanCalls  int
	cleanedWith []cleaner.FileEntry
}

func (c *splitPrivilegeCleaner) Category() cleaner.Category { return c.category }
func (c *splitPrivilegeCleaner) Name() string               { return string(c.category) }
func (c *splitPrivilegeCleaner) Description() string        { return "split privilege spy" }
func (c *splitPrivilegeCleaner) RequiresSudo() bool         { return true }
func (c *splitPrivilegeCleaner) DeletesWholeDomain() bool   { return false }

func (c *splitPrivilegeCleaner) NeedsSudo(entry cleaner.FileEntry) bool {
	return strings.HasPrefix(entry.Path, "/sudo/")
}

func (c *splitPrivilegeCleaner) Scan(context.Context, func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	return &cleaner.ScanResult{Category: c.category}, nil
}

func (c *splitPrivilegeCleaner) Clean(_ context.Context, entries []cleaner.FileEntry, _ bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	c.cleanCalls++
	c.cleanedWith = append(c.cleanedWith, entries...)
	var freed int64
	for _, e := range entries {
		freed += e.Size
	}
	return &cleaner.CleanResult{Category: c.category, FilesDeleted: len(entries), BytesFreed: freed}, nil
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

// newSplitReviewApp builds an execute-mode app around a single
// splitPrivilegeCleaner category, mirroring newSudoReviewApp's shape.
func newSplitReviewApp(t *testing.T, cat cleaner.Category, entries []cleaner.FileEntry) (App, *splitPrivilegeCleaner) {
	t.Helper()

	c := &splitPrivilegeCleaner{category: cat}
	registry := cleaner.NewRegistry()
	registry.Register(c)

	results := map[cleaner.Category]*cleaner.ScanResult{
		cat: {Category: cat, TotalFiles: len(entries), Entries: entries},
	}

	scanning := screens.NewScanning([]string{string(cat)}, registry)
	scanning.UpdateScanResult(cat, results[cat], nil)

	app := App{
		currentScreen: screenCleaning,
		executeMode:   true,
		registry:      registry,
		scanningScr:   scanning,
		cleaningScr:   screens.NewCleaningModel(results, false),
		reviewScr:     screens.NewReview(results, true, registry, false),
		isElevated:    false,
		ctx:           context.Background(),
	}
	return app, c
}

// runDirectCleanCmd invokes the single tea.Cmd startElevation dispatched for
// a category's direct leg and asserts it produced a directCleanCompleteMsg.
// tea.Batch collapses a single-command batch to that command directly (see
// bubbletea's compactCmds), so this works whether startElevation dispatched
// one direct leg alone or as part of a larger batch already reduced to one.
func runDirectCleanCmd(t *testing.T, cmd tea.Cmd) directCleanCompleteMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a non-nil command for the pending direct leg")
	}
	msg, ok := cmd().(directCleanCompleteMsg)
	if !ok {
		t.Fatalf("expected directCleanCompleteMsg, got %T", msg)
	}
	return msg
}

// runDirectCleanBatch unwraps the tea.Batch startElevation dispatches when
// more than one category has a direct leg, runs every command in it, and
// returns the directCleanCompleteMsg each produced, keyed by category (the
// order categories are batched in follows cleaningScr.Categories, which is
// built from a map, so callers must never rely on positional order). want is
// the number of direct legs expected in the batch.
func runDirectCleanBatch(t *testing.T, cmd tea.Cmd, want int) map[cleaner.Category]directCleanCompleteMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a non-nil batch command for the pending direct legs")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected a tea.BatchMsg covering every direct leg, got %T", cmd())
	}
	if len(batch) != want {
		t.Fatalf("batch has %d command(s), want %d (one per category with a direct leg)", len(batch), want)
	}
	msgs := make(map[cleaner.Category]directCleanCompleteMsg, len(batch))
	for i, sub := range batch {
		msg, ok := sub().(directCleanCompleteMsg)
		if !ok {
			t.Fatalf("batch command %d produced %T, want directCleanCompleteMsg", i, sub())
		}
		msgs[msg.category] = msg
	}
	if len(msgs) != want {
		t.Fatalf("batch produced %d distinct categories, want %d", len(msgs), want)
	}
	return msgs
}

// TestStartElevation_DirectOnlyCategoryDispatchesAsyncAndResolvesOnCompletion
// covers the case where a sudo-required category's approved entries need no
// elevation at all (e.g. Temp's own $TMPDIR entries). Issue B: the direct
// leg must be dispatched as an async tea.Cmd, never run inline inside
// startElevation itself -- so cmd must be non-nil and c.Clean must not have
// run yet by the time startElevation returns. Issue A: once that command's
// result is fed back through handleDirectCleanComplete, the deletion's
// history row must already exist even though this category never enters an
// elevate.Plan and finishCleaning has not run.
func TestStartElevation_DirectOnlyCategoryDispatchesAsyncAndResolvesOnCompletion(t *testing.T) {
	resetHistory(t)
	const cat cleaner.Category = "split_direct_only"
	entries := []cleaner.FileEntry{
		{Path: "/direct/a", Size: 5, Category: cat},
		{Path: "/direct/b", Size: 7, Category: cat},
	}
	app, c := newSplitReviewApp(t, cat, entries)

	model, cmd := app.startElevation()
	app = model.(App)

	if cmd == nil {
		t.Fatal("expected a direct-clean command to be dispatched (Issue B: must be async, not run inline)")
	}
	if c.cleanCalls != 0 {
		t.Fatal("the direct leg must not have run yet: startElevation only dispatches the command, it never calls Clean itself")
	}
	if app.cleaningScr.Categories[0].Status != "cleaning" {
		t.Fatalf("Status = %q, want cleaning (in flight, not yet resolved)", app.cleaningScr.Categories[0].Status)
	}
	if app.pendingElevation == nil {
		t.Fatal("expected pendingElevation to be set while the direct leg is in flight")
	}

	dmsg := runDirectCleanCmd(t, cmd)
	if c.cleanCalls != 1 || len(c.cleanedWith) != 2 {
		t.Fatalf("direct Clean called %d time(s) with %d entries, want 1 call with both entries", c.cleanCalls, len(c.cleanedWith))
	}

	model, cmd = app.handleDirectCleanComplete(dmsg)
	app = model.(App)

	if cmd != nil {
		t.Fatal("expected no further command: nothing needed elevation, so this should fall through to the ordinary clean loop and finish")
	}
	if app.pendingElevation != nil {
		t.Fatal("pendingElevation must be cleared once its only pending direct leg has resolved")
	}
	got := app.cleaningScr.Categories[0]
	if got.Status != "done" {
		t.Fatalf("Status = %q, want done", got.Status)
	}
	if got.FilesDeleted != 2 || got.BytesDeleted != 12 {
		t.Fatalf("FilesDeleted/BytesDeleted = %d/%d, want 2/12", got.FilesDeleted, got.BytesDeleted)
	}
	if !app.cleaningScr.Done {
		t.Fatal("the single category resolved directly, so the whole run should be done")
	}

	// Issue A: the history row must exist now, immediately -- not deferred
	// to finishCleaning, which a mid-run quit could skip entirely.
	runs := loadHistoryRuns(t)
	if len(runs) != 1 || len(runs[0].Categories) != 1 || runs[0].Categories[0].Name != string(cat) || runs[0].TotalFiles != 2 || runs[0].TotalBytes != 12 {
		t.Fatalf("history = %+v, want exactly one run with the direct-only deletion", runs)
	}
	if _, recorded := app.elevatedRecorded[cat]; !recorded {
		t.Fatal("a direct-only category's leg is still recordDirectHistory's responsibility -- it must be marked elevatedRecorded so finishCleaning never revisits it")
	}
}

// TestStartElevation_DirectOnlyCategoryHistorySurvivesQuitBeforeRunFinishes
// is Issue A's exact regression scenario: a direct-only category resolves
// while a second, unrelated category is still pending, so finishCleaning has
// definitely not run yet -- and the deletion must already be durable.
func TestStartElevation_DirectOnlyCategoryHistorySurvivesQuitBeforeRunFinishes(t *testing.T) {
	resetHistory(t)
	const cat cleaner.Category = "split_direct_only_mixed"
	directCleaner := &splitPrivilegeCleaner{category: cat}
	plain := &plainMockCleaner{wholeDomainMockCleaner{category: "cat_plain_2"}}
	registry := cleaner.NewRegistry()
	registry.Register(directCleaner)
	registry.Register(plain)

	directEntries := []cleaner.FileEntry{{Path: "/direct/a", Size: 5, Category: cat}}
	plainEntries := []cleaner.FileEntry{{Path: "/Users/vini/Library/Caches/y", Size: 3, Category: "cat_plain_2"}}
	results := map[cleaner.Category]*cleaner.ScanResult{
		cat:           {Category: cat, TotalFiles: 1, Entries: directEntries},
		"cat_plain_2": {Category: "cat_plain_2", TotalFiles: 1, Entries: plainEntries},
	}

	app := App{
		currentScreen: screenCleaning,
		executeMode:   true,
		registry:      registry,
		cleaningScr:   screens.NewCleaningModel(results, false),
		reviewScr:     screens.NewReview(results, true, registry, false),
		ctx:           context.Background(),
	}

	model, cmd := app.startElevation()
	app = model.(App)
	dmsg := runDirectCleanCmd(t, cmd)

	model, _ = app.handleDirectCleanComplete(dmsg)
	app = model.(App)

	if app.cleaningScr.Done {
		t.Fatal("the plain category has not run yet, so the whole run must not be done")
	}
	if runs := loadHistoryRuns(t); len(runs) != 1 || runs[0].TotalFiles != 1 || runs[0].TotalBytes != 5 {
		t.Fatalf("history after the direct leg alone = %+v, want the direct deletion already recorded", runs)
	}

	// Simulate the quit key: cancel and exit without ever reaching
	// finishCleaning for the still-pending plain category.
	ctx, cancel := context.WithCancel(context.Background())
	app.ctx, app.cancel = ctx, cancel
	model, quit := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	app = model.(App)
	if quit == nil {
		t.Fatal("expected a quit command")
	}
	if runs := loadHistoryRuns(t); len(runs) != 1 {
		t.Fatalf("history after quit has %d run(s), want exactly the one already-recorded direct deletion", len(runs))
	}
}

// TestStartElevation_TwoDirectLegsDispatchTogetherAndPlanWaitsForBoth covers
// the multi-category shape of F-F's async split: two sudo categories that
// each have BOTH a root-only entry and an entry the user owns. startElevation
// must dispatch both direct legs at once (a tea.Batch, not one inline and one
// deferred), and handleDirectCleanComplete must hold the elevate.Plan back
// until the LAST leg reports -- dispatching after the first would elevate with
// one category's direct result missing from the map handleElevateComplete
// later merges from.
func TestStartElevation_TwoDirectLegsDispatchTogetherAndPlanWaitsForBoth(t *testing.T) {
	resetHistory(t)
	const catA cleaner.Category = "split_two_a"
	const catB cleaner.Category = "split_two_b"

	cleanerA := &splitPrivilegeCleaner{category: catA}
	cleanerB := &splitPrivilegeCleaner{category: catB}
	registry := cleaner.NewRegistry()
	registry.Register(cleanerA)
	registry.Register(cleanerB)

	entriesA := []cleaner.FileEntry{
		{Path: "/sudo/a", Size: 10, Category: catA},
		{Path: "/direct/a", Size: 5, Category: catA},
	}
	entriesB := []cleaner.FileEntry{
		{Path: "/sudo/b", Size: 20, Category: catB},
		{Path: "/direct/b", Size: 7, Category: catB},
	}
	results := map[cleaner.Category]*cleaner.ScanResult{
		catA: {Category: catA, TotalFiles: len(entriesA), Entries: entriesA},
		catB: {Category: catB, TotalFiles: len(entriesB), Entries: entriesB},
	}

	app := App{
		currentScreen: screenCleaning,
		executeMode:   true,
		registry:      registry,
		cleaningScr:   screens.NewCleaningModel(results, false),
		reviewScr:     screens.NewReview(results, true, registry, false),
		ctx:           context.Background(),
	}

	model, cmd := app.startElevation()
	app = model.(App)

	if cleanerA.cleanCalls != 0 || cleanerB.cleanCalls != 0 {
		t.Fatal("neither direct leg may run inline: startElevation only dispatches commands")
	}
	pe := app.pendingElevation
	if pe == nil {
		t.Fatal("expected pendingElevation to be set while both direct legs are in flight")
	}
	if len(pe.pendingDirect) != 2 {
		t.Fatalf("pendingDirect has %d entry/entries, want 2 (one per category with a direct leg)", len(pe.pendingDirect))
	}

	msgs := runDirectCleanBatch(t, cmd, 2)
	if cleanerA.cleanCalls != 1 || len(cleanerA.cleanedWith) != 1 || cleanerA.cleanedWith[0].Path != "/direct/a" {
		t.Fatalf("cleanerA direct leg: %d call(s) with %+v, want one call with only /direct/a", cleanerA.cleanCalls, cleanerA.cleanedWith)
	}
	if cleanerB.cleanCalls != 1 || len(cleanerB.cleanedWith) != 1 || cleanerB.cleanedWith[0].Path != "/direct/b" {
		t.Fatalf("cleanerB direct leg: %d call(s) with %+v, want one call with only /direct/b", cleanerB.cleanCalls, cleanerB.cleanedWith)
	}

	// First leg back: the plan must NOT be dispatched yet.
	model, cmd = app.handleDirectCleanComplete(msgs[catA])
	app = model.(App)
	if cmd != nil {
		t.Fatal("the elevate plan must not be dispatched while the second category's direct leg is still in flight")
	}
	if app.pendingElevation == nil {
		t.Fatal("pendingElevation must stay set until every direct leg has reported")
	}
	if len(app.pendingElevation.pendingDirect) != 1 {
		t.Fatalf("pendingDirect has %d entry/entries after one leg, want 1", len(app.pendingElevation.pendingDirect))
	}
	if _, still := app.pendingElevation.pendingDirect[catB]; !still {
		t.Fatal("the still-unreported category must be the one left in pendingDirect")
	}

	// Last leg back: now the plan goes out, covering both categories.
	model, cmd = app.handleDirectCleanComplete(msgs[catB])
	app = model.(App)
	if cmd == nil {
		t.Fatal("expected the elevate command once the last pending direct leg resolves")
	}
	if app.pendingElevation != nil {
		t.Fatal("pendingElevation must be cleared once every direct leg has resolved")
	}

	// pe is the same state object handleDirectCleanComplete folded results
	// into (and handed to elevateCmd) before clearing the field, so it is
	// exactly what the elevated leg was dispatched with.
	if len(pe.pendingDirect) != 0 {
		t.Fatalf("pendingDirect = %+v, want empty once both legs reported", pe.pendingDirect)
	}
	planned := map[cleaner.Category][]string{}
	for _, pc := range pe.plan.Categories {
		for _, e := range pc.Entries {
			planned[pc.Category] = append(planned[pc.Category], e.Path)
		}
	}
	if len(planned) != 2 {
		t.Fatalf("plan covers %d categor(y/ies) (%+v), want both categories' sudo entries", len(planned), planned)
	}
	if got := planned[catA]; len(got) != 1 || got[0] != "/sudo/a" {
		t.Fatalf("plan entries for %s = %v, want only /sudo/a (the direct entry must not be elevated)", catA, got)
	}
	if got := planned[catB]; len(got) != 1 || got[0] != "/sudo/b" {
		t.Fatalf("plan entries for %s = %v, want only /sudo/b (the direct entry must not be elevated)", catB, got)
	}

	// Both direct results must survive: the second must not overwrite the first.
	if len(pe.direct) != 2 {
		t.Fatalf("direct results = %+v, want one per category", pe.direct)
	}
	if got := pe.direct[catA]; got.DeletedFiles != 1 || got.DeletedSize != 5 {
		t.Fatalf("direct result for %s = %d file(s)/%d byte(s), want 1/5", catA, got.DeletedFiles, got.DeletedSize)
	}
	if got := pe.direct[catB]; got.DeletedFiles != 1 || got.DeletedSize != 7 {
		t.Fatalf("direct result for %s = %d file(s)/%d byte(s), want 1/7", catB, got.DeletedFiles, got.DeletedSize)
	}

	// Both are split categories, so neither is resolved on screen yet -- the
	// elevated leg still has to report -- but both direct deletions are
	// already durable in history (Issue A), one row each.
	for _, c := range app.cleaningScr.Categories {
		if c.Status != "cleaning" {
			t.Fatalf("Status for %s = %q, want cleaning (elevated leg still outstanding)", c.Category, c.Status)
		}
	}
	runs := loadHistoryRuns(t)
	var totalFiles int
	var totalBytes int64
	for _, run := range runs {
		totalFiles += run.TotalFiles
		totalBytes += run.TotalBytes
	}
	if len(runs) != 2 || totalFiles != 2 || totalBytes != 12 {
		t.Fatalf("history = %+v, want one already-durable row per direct leg totalling 2 files / 12 bytes", runs)
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

// startSplitElevation drives a split category's real, async direct-clean leg
// through startElevation and handleDirectCleanComplete -- exactly what
// happens before any elevateCompleteMsg exists -- so tests exercising the
// elevated leg's branches start from the same state production code would:
// the direct leg's history row already written (Issue A), pendingElevation
// cleared, and an elevateCmd dispatched for the sudo entries. Returns the
// resulting app plus the sudo entries so callers can hand-build a matching
// elevateCompleteMsg for the elevated leg (there is no invokeElevated seam
// in this package to stub, matching every other handleElevateComplete test).
func startSplitElevation(t *testing.T, cat cleaner.Category) (App, []cleaner.FileEntry) {
	t.Helper()
	sudoEntries := []cleaner.FileEntry{{Path: "/sudo/a", Size: 10, Category: cat}}
	directEntries := []cleaner.FileEntry{{Path: "/direct/b", Size: 5, Category: cat}}
	app, _ := newSplitReviewApp(t, cat, append(append([]cleaner.FileEntry{}, sudoEntries...), directEntries...))

	model, cmd := app.startElevation()
	app = model.(App)
	dmsg := runDirectCleanCmd(t, cmd)

	model, cmd = app.handleDirectCleanComplete(dmsg)
	app = model.(App)
	if cmd == nil {
		t.Fatal("expected the elevate command once the only pending direct leg resolves")
	}
	if app.pendingElevation != nil {
		t.Fatal("pendingElevation should be cleared once every direct leg has resolved")
	}
	return app, sudoEntries
}

// TestHandleElevateComplete_SplitCategoryMergesDirectAndElevatedLegs is F-F's
// core case: a category whose approved entries split across both legs must
// show summed counts on the cleaning screen, appearing exactly once (never
// double-counted between the direct leg and the elevated leg). In history,
// per Issue A's fix, the two legs are recorded as two separate rows (the
// direct leg's the instant it finished, the elevated leg's here) rather than
// one row written once at the very end -- so together they must total the
// full, real amount without duplicating either leg.
func TestHandleElevateComplete_SplitCategoryMergesDirectAndElevatedLegs(t *testing.T) {
	resetHistory(t)
	const cat cleaner.Category = "split_merge"
	app, sudoEntries := startSplitElevation(t, cat)

	if runs := loadHistoryRuns(t); len(runs) != 1 || runs[0].TotalFiles != 1 || runs[0].TotalBytes != 5 {
		t.Fatalf("history after the direct leg alone = %+v, want a single 1-file/5-byte row", runs)
	}

	msg := elevateCompleteMsg{
		plan: elevate.Plan{
			Version:    elevate.PlanSchemaVersion,
			Categories: []elevate.PlanCategory{{Category: cat, Entries: sudoEntries}},
		},
		result: elevate.Result{
			Version: elevate.ResultSchemaVersion,
			Clean: commands.CleanResult{
				Categories: []commands.CleanCategoryResult{
					{Category: cat, DeletedFiles: 1, DeletedSize: 10},
				},
			},
		},
		direct: map[cleaner.Category]commands.CleanCategoryResult{
			cat: {Category: cat, DeletedFiles: 1, DeletedSize: 5},
		},
	}

	model, _ := app.handleElevateComplete(msg)
	app = model.(App)

	results := app.cleaningScr.Results()
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 (the category must never appear twice on screen)", len(results))
	}
	r := results[0]
	if r.FilesDeleted != 2 || r.BytesFreed != 15 {
		t.Fatalf("FilesDeleted/BytesFreed = %d/%d, want 2/15 (the cleaning screen shows the combined total)", r.FilesDeleted, r.BytesFreed)
	}

	runs := loadHistoryRuns(t)
	if len(runs) != 2 {
		t.Fatalf("history = %+v, want 2 rows: the direct leg's (already written) plus the elevated leg's own", runs)
	}
	var totalFiles int
	var totalBytes int64
	for _, run := range runs {
		totalFiles += run.TotalFiles
		totalBytes += run.TotalBytes
	}
	if totalFiles != 2 || totalBytes != 15 {
		t.Fatalf("history totals across both rows = %d files / %d bytes, want 2/15 (no double counting, nothing lost)", totalFiles, totalBytes)
	}
}

// TestHandleElevateComplete_ElevationFailedKeepsDirectLegCounts verifies the
// direct leg's real, already-completed deletion is never discarded, and
// never recorded a second time, just because the elevated leg for the same
// category failed outright.
func TestHandleElevateComplete_ElevationFailedKeepsDirectLegCounts(t *testing.T) {
	resetHistory(t)
	const cat cleaner.Category = "split_elevate_failed"
	app, sudoEntries := startSplitElevation(t, cat)

	msg := elevateCompleteMsg{
		plan: elevate.Plan{
			Version:    elevate.PlanSchemaVersion,
			Categories: []elevate.PlanCategory{{Category: cat, Entries: sudoEntries}},
		},
		err: errors.Join(elevate.ErrElevationFailed, errors.New("sudo: 3 incorrect password attempts")),
		direct: map[cleaner.Category]commands.CleanCategoryResult{
			cat: {Category: cat, DeletedFiles: 1, DeletedSize: 5},
		},
	}

	model, _ := app.handleElevateComplete(msg)
	app = model.(App)

	got := app.cleaningScr.Categories[0]
	if got.Status == "skipped" {
		t.Fatal("a category with a real direct-leg deletion must never be reported as a plain skip")
	}
	if got.FilesDeleted != 1 || got.BytesDeleted != 5 {
		t.Fatalf("FilesDeleted/BytesDeleted = %d/%d, want 1/5 from the direct leg", got.FilesDeleted, got.BytesDeleted)
	}
	if got.Error == nil {
		t.Fatal("expected the elevation failure to still surface as an error")
	}

	// The direct leg's row was already written by startSplitElevation; the
	// failed elevated leg must not add a second one (nothing was deleted by
	// it) nor drop the first.
	if runs := loadHistoryRuns(t); len(runs) != 1 || runs[0].TotalFiles != 1 || runs[0].TotalBytes != 5 {
		t.Fatalf("history = %+v, want exactly the direct leg's one record, not zero and not doubled", runs)
	}
}

// TestHandleElevateComplete_OutcomeUnknownKeepsDirectLegCounts is the same
// guarantee for the "default" (unknown outcome) branch.
func TestHandleElevateComplete_OutcomeUnknownKeepsDirectLegCounts(t *testing.T) {
	resetHistory(t)
	const cat cleaner.Category = "split_outcome_unknown"
	app, sudoEntries := startSplitElevation(t, cat)

	msg := elevateCompleteMsg{
		plan: elevate.Plan{
			Version:    elevate.PlanSchemaVersion,
			Categories: []elevate.PlanCategory{{Category: cat, Entries: sudoEntries}},
		},
		err: errors.Join(elevate.ErrElevationOutcomeUnknown, errors.New("signal: terminated")),
		direct: map[cleaner.Category]commands.CleanCategoryResult{
			cat: {Category: cat, DeletedFiles: 1, DeletedSize: 5},
		},
	}

	model, _ := app.handleElevateComplete(msg)
	app = model.(App)

	got := app.cleaningScr.Categories[0]
	if got.Status != "error" {
		t.Fatalf("Status = %q, want error (uncertain outcome, not a plain skip)", got.Status)
	}
	if got.FilesDeleted != 1 || got.BytesDeleted != 5 {
		t.Fatalf("FilesDeleted/BytesDeleted = %d/%d, want 1/5 from the direct leg", got.FilesDeleted, got.BytesDeleted)
	}
	if got.Error == nil {
		t.Fatal("expected a non-nil Error carrying the outcome-unknown message")
	}

	if runs := loadHistoryRuns(t); len(runs) != 1 || runs[0].TotalFiles != 1 || runs[0].TotalBytes != 5 {
		t.Fatalf("history = %+v, want exactly the direct leg's one record, not zero and not doubled", runs)
	}
}

// TestHandleElevateComplete_EmptyIntersectionWithDirectLegIsNotAPlainSkip
// covers the success path's "approved but nothing matched the fresh scan"
// branch: ordinarily a plain skip, but a direct leg's real deletion must
// still be reported rather than discarded as "nothing is known", and its
// history row (already written when the direct leg finished) must not be
// duplicated just because this branch has nothing of its own to add.
func TestHandleElevateComplete_EmptyIntersectionWithDirectLegIsNotAPlainSkip(t *testing.T) {
	resetHistory(t)
	const cat cleaner.Category = "split_empty_intersection"
	app, sudoEntries := startSplitElevation(t, cat)

	msg := elevateCompleteMsg{
		plan: elevate.Plan{
			Version:    elevate.PlanSchemaVersion,
			Categories: []elevate.PlanCategory{{Category: cat, Entries: sudoEntries}},
		},
		result: elevate.Result{
			Version: elevate.ResultSchemaVersion,
			Intersections: []elevate.CategoryIntersection{
				{Category: cat, Approved: 1, Matched: 0, Missing: 1},
			},
		},
		direct: map[cleaner.Category]commands.CleanCategoryResult{
			cat: {Category: cat, DeletedFiles: 1, DeletedSize: 5},
		},
	}

	model, _ := app.handleElevateComplete(msg)
	app = model.(App)

	got := app.cleaningScr.Categories[0]
	if got.Status == "skipped" {
		t.Fatal("a category with a real direct-leg deletion must never be reported as a plain skip")
	}
	if got.FilesDeleted != 1 || got.BytesDeleted != 5 {
		t.Fatalf("FilesDeleted/BytesDeleted = %d/%d, want 1/5 from the direct leg", got.FilesDeleted, got.BytesDeleted)
	}

	runs := loadHistoryRuns(t)
	if len(runs) != 1 || runs[0].TotalFiles != 1 || runs[0].TotalBytes != 5 {
		t.Fatalf("history = %+v, want exactly the direct leg's one record (written when it finished), not doubled", runs)
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
