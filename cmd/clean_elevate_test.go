package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/elevate"
)

// spyCleaner records whether it was ever asked to scan or clean, so tests
// can assert that a category never registered on the command line stays
// completely untouched.
type spyCleaner struct {
	category cleaner.Category
	scanned  bool
	cleaned  bool
}

func (c *spyCleaner) Category() cleaner.Category { return c.category }
func (c *spyCleaner) Name() string               { return string(c.category) }
func (c *spyCleaner) Description() string        { return "spy" }
func (c *spyCleaner) RequiresSudo() bool         { return false }
func (c *spyCleaner) DeletesWholeDomain() bool   { return false }

func (c *spyCleaner) Scan(context.Context, func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	c.scanned = true
	return &cleaner.ScanResult{Category: c.category}, nil
}

func (c *spyCleaner) Clean(context.Context, []cleaner.FileEntry, bool, func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	c.cleaned = true
	return &cleaner.CleanResult{Category: c.category}, nil
}

// runCleanModelInit synchronously drives the tea.Cmd batch Init() returns
// and extracts the resulting cleanDoneMsg, without needing a real
// tea.Program -- Init's closures are plain functions with no dependency on
// bubbletea's runtime.
func runCleanModelInit(t *testing.T, m cleanModel) cleanDoneMsg {
	t.Helper()

	batchMsg, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("Init() did not return a tea.BatchMsg")
	}
	for _, cmd := range batchMsg {
		if cmd == nil {
			continue
		}
		if done, ok := cmd().(cleanDoneMsg); ok {
			return done
		}
	}
	t.Fatal("Init() batch never produced a cleanDoneMsg")
	return cleanDoneMsg{}
}

func TestCleanModel_SkipLiveRunNeverTouchesOtherCategories(t *testing.T) {
	spy := &spyCleaner{category: "other_cat"}
	registry := cleaner.NewRegistry()
	registry.Register(spy)

	// args is nil/empty here on purpose: this is exactly the shape
	// runCleanInteractive produces when every selected category needed
	// sudo. Without skipLiveRun, an empty args would be misread as "clean
	// every registered category" and spy would be scanned and cleaned.
	// dryRun: true so this test never touches the real on-disk history file.
	preResolved := []commands.CleanCategoryResult{
		{Category: cleaner.CategoryTemp, Name: cleaner.CategoryTemp.DisplayName(), DeletedFiles: 2, DeletedSize: 20},
	}
	m := newCleanModel(context.Background(), registry, nil, false, false, commands.PreparedScanResult{}, true, preResolved, true)

	done := runCleanModelInit(t, m)

	if spy.scanned || spy.cleaned {
		t.Fatal("skipLiveRun must never touch a category outside preResolved")
	}
	if done.err != nil {
		t.Fatalf("unexpected error: %v", done.err)
	}
	if len(done.result.Categories) != 1 || done.result.Categories[0].Category != cleaner.CategoryTemp {
		t.Fatalf("result.Categories = %+v, want only the preResolved entry", done.result.Categories)
	}
	if done.result.TotalFiles != 2 || done.result.TotalSize != 20 {
		t.Fatalf("totals = %d files, %d bytes, want 2 files, 20 bytes from preResolved alone", done.result.TotalFiles, done.result.TotalSize)
	}
}

func TestCleanModel_LiveRunErrorNeverHidesAnAlreadyCompletedElevatedDeletion(t *testing.T) {
	registry := cleaner.NewRegistry() // "other_cat" is deliberately not registered, so the live run fails structurally.

	preResolved := []commands.CleanCategoryResult{
		{Category: cleaner.CategoryTemp, Name: cleaner.CategoryTemp.DisplayName(), DeletedFiles: 4, DeletedSize: 4096},
	}
	// dryRun: true so this test never touches the real on-disk history file.
	m := newCleanModel(context.Background(), registry, []string{"other_cat"}, false, false, commands.PreparedScanResult{}, true, preResolved, false)

	done := runCleanModelInit(t, m)
	if done.err == nil {
		t.Fatal("expected the live run to fail structurally (unregistered category)")
	}

	updated, _ := m.Update(done)
	finished := updated.(cleanModel)

	if finished.result == nil {
		t.Fatal("result must still be set so the elevated deletion is not hidden behind the unrelated live-run error")
	}
	view := finished.View()
	if !strings.Contains(view, cleaner.CategoryTemp.DisplayName()) {
		t.Errorf("view does not mention the elevated category:\n%s", view)
	}
	if !strings.Contains(view, "4.0 KB") && !strings.Contains(view, "4 KB") {
		// FormatBytes' exact spacing isn't the point here; just confirm the
		// elevated deletion's size made it into the rendered table.
		t.Errorf("view does not show the elevated deletion's size:\n%s", view)
	}
	if !strings.Contains(view, done.err.Error()) {
		t.Errorf("view does not surface the live-run's own error text:\n%s", view)
	}
}

func TestCleanModel_PreResolvedMergesWithLiveRun(t *testing.T) {
	spy := &spyCleaner{category: "other_cat"}
	registry := cleaner.NewRegistry()
	registry.Register(spy)

	// dryRun: true so this test never touches the real on-disk history file.
	preResolved := []commands.CleanCategoryResult{
		{Category: cleaner.CategoryTemp, Name: cleaner.CategoryTemp.DisplayName(), DeletedFiles: 2, DeletedSize: 20},
	}
	m := newCleanModel(context.Background(), registry, []string{"other_cat"}, false, false, commands.PreparedScanResult{}, true, preResolved, false)

	done := runCleanModelInit(t, m)

	if !spy.scanned {
		t.Error("the explicitly selected non-sudo category must still run live")
	}
	if len(done.result.Categories) != 2 {
		t.Fatalf("result.Categories = %+v, want the live category plus the preResolved one", done.result.Categories)
	}
	if done.result.Categories[0].Category != cleaner.CategoryTemp {
		t.Errorf("Categories[0] = %s, want preResolved categories listed first", done.result.Categories[0].Category)
	}
}

func TestSplitSudoCategories_ExplicitSelection(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())   // RequiresSudo() == true
	registry.Register(cleaner.NewCachesCleaner()) // RequiresSudo() == false

	cfg, err := config.New(nil, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	sudo, rest, err := splitSudoCategories(registry, cfg, []string{string(cleaner.CategoryTemp), string(cleaner.CategoryApplicationCaches)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sudo) != 1 || sudo[0] != string(cleaner.CategoryTemp) {
		t.Errorf("sudo = %v, want [%s]", sudo, cleaner.CategoryTemp)
	}
	if len(rest) != 1 || rest[0] != string(cleaner.CategoryApplicationCaches) {
		t.Errorf("rest = %v, want [%s]", rest, cleaner.CategoryApplicationCaches)
	}
}

func TestSplitSudoCategories_EmptySelectionMeansEveryEnabledCategory(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())
	registry.Register(cleaner.NewCachesCleaner())

	cfg, err := config.New(nil, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	sudo, rest, err := splitSudoCategories(registry, cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sudo)+len(rest) != 2 {
		t.Fatalf("sudo+rest = %d entries, want 2 (every registered category)", len(sudo)+len(rest))
	}
}

func TestSplitSudoCategories_DedupesDuplicateSelection(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	cfg, err := config.New(nil, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	sudo, rest, err := splitSudoCategories(registry, cfg, []string{string(cleaner.CategoryTemp), string(cleaner.CategoryTemp)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sudo) != 1 {
		t.Fatalf("sudo = %v, want a single deduped entry -- the elevated helper rejects a plan listing the same category twice", sudo)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty", rest)
	}
}

func TestSplitSudoCategories_ExplicitSelectionOverridesDisabled(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())
	registry.Register(cleaner.NewCachesCleaner())

	cfg, err := config.New(nil, []string{string(cleaner.CategoryTemp)})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	// "An explicit selection wins over disabled_categories" is this package's
	// rule for every category (see resolveCleaners), and needing root is not a
	// reason to apply a second, contradictory policy. Naming a disabled sudo
	// category therefore elevates it, exactly as naming a disabled non-sudo
	// category runs it.
	sudo, rest, err := splitSudoCategories(registry, cfg, []string{string(cleaner.CategoryTemp), string(cleaner.CategoryApplicationCaches)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sudo) != 1 || sudo[0] != string(cleaner.CategoryTemp) {
		t.Errorf("sudo = %v, want [%s]: an explicit selection overrides disabled_categories", sudo, cleaner.CategoryTemp)
	}
	if len(rest) != 1 || rest[0] != string(cleaner.CategoryApplicationCaches) {
		t.Errorf("rest = %v, want [%s]", rest, cleaner.CategoryApplicationCaches)
	}
}

// TestSplitSudoCategories_DefaultSelectionStillHonorsDisabled pins the other
// half: with no categories named, disabled_categories still applies, because
// the expansion goes through config.FilterRegistry.
func TestSplitSudoCategories_DefaultSelectionStillHonorsDisabled(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())
	registry.Register(cleaner.NewCachesCleaner())

	cfg, err := config.New(nil, []string{string(cleaner.CategoryTemp)})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	sudo, rest, err := splitSudoCategories(registry, cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sudo) != 0 {
		t.Errorf("sudo = %v, want none: a disabled category must not be picked up by default", sudo)
	}
	if len(rest) != 1 || rest[0] != string(cleaner.CategoryApplicationCaches) {
		t.Errorf("rest = %v, want [%s]", rest, cleaner.CategoryApplicationCaches)
	}
}

func TestSplitSudoCategories_UnknownCategory(t *testing.T) {
	registry := cleaner.NewRegistry()
	cfg, err := config.New(nil, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	_, _, err = splitSudoCategories(registry, cfg, []string{"nope"})
	if err == nil {
		t.Fatal("expected an error for an unknown category")
	}
}

func TestSudoNeedMessage_Singular(t *testing.T) {
	msg := sudoNeedMessage([]elevate.PlanCategory{{Category: cleaner.CategoryLogs}})
	if !strings.Contains(msg, cleaner.CategoryLogs.DisplayName()) {
		t.Errorf("message %q does not mention the category", msg)
	}
	if strings.Contains(msg, "categories") {
		t.Errorf("message %q should use singular wording for one category", msg)
	}
}

func TestSudoNeedMessage_Plural(t *testing.T) {
	msg := sudoNeedMessage([]elevate.PlanCategory{{Category: cleaner.CategoryLogs}, {Category: cleaner.CategoryTemp}})
	if !strings.Contains(msg, cleaner.CategoryLogs.DisplayName()) || !strings.Contains(msg, cleaner.CategoryTemp.DisplayName()) {
		t.Errorf("message %q does not mention both categories", msg)
	}
}

func TestExpandCategoriesFromPreparedScan_ExplicitSelectionWins(t *testing.T) {
	prepared := commands.PreparedScanResult{
		Result: commands.ScanResult{Categories: []commands.ScanCategoryResult{{Category: cleaner.CategoryDocker}}},
	}
	got := expandCategoriesFromPreparedScan([]string{"other_cat"}, prepared)
	if len(got) != 1 || got[0] != "other_cat" {
		t.Fatalf("got = %v, want the explicit selection untouched", got)
	}
}

func TestExpandCategoriesFromPreparedScan_EmptySelectionExpandsToScanFileCategoriesOnly(t *testing.T) {
	// The bug this guards against: an empty selection with --from-file must
	// expand to what the scan file actually contains, never to "every
	// registered category" -- the latter would hand a whole-domain cleaner
	// (brew cleanup, go clean -cache -modcache, empty Trash) an empty entry
	// list for a category the file never scanned, which those cleaners read
	// as "clear everything" rather than "nothing to do".
	prepared := commands.PreparedScanResult{
		Result: commands.ScanResult{Categories: []commands.ScanCategoryResult{{Category: cleaner.CategoryDocker}}},
	}
	got := expandCategoriesFromPreparedScan(nil, prepared)
	if len(got) != 1 || got[0] != string(cleaner.CategoryDocker) {
		t.Fatalf("got = %v, want exactly [docker] (the scan file's own categories)", got)
	}
}

// wholeDomainSpyCleaner records the entries it was actually asked to clean,
// so a regression where it is invoked with a nil/empty list (which it would
// read as "clear my entire domain") is directly observable.
type wholeDomainSpyCleaner struct {
	category    cleaner.Category
	entries     []cleaner.FileEntry
	cleaned     bool
	cleanedWith []cleaner.FileEntry
}

func (c *wholeDomainSpyCleaner) Category() cleaner.Category { return c.category }
func (c *wholeDomainSpyCleaner) Name() string               { return string(c.category) }
func (c *wholeDomainSpyCleaner) Description() string        { return "whole domain spy" }
func (c *wholeDomainSpyCleaner) RequiresSudo() bool         { return false }
func (c *wholeDomainSpyCleaner) DeletesWholeDomain() bool   { return true }

func (c *wholeDomainSpyCleaner) Scan(context.Context, func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	return &cleaner.ScanResult{Category: c.category, Entries: c.entries, TotalFiles: len(c.entries)}, nil
}

func (c *wholeDomainSpyCleaner) Clean(_ context.Context, entries []cleaner.FileEntry, _ bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	c.cleaned = true
	c.cleanedWith = entries
	return &cleaner.CleanResult{Category: c.category}, nil
}

func TestFromFileEmptySelection_NeverInvokesWholeDomainCleanerOutsideTheScanFile(t *testing.T) {
	// End-to-end regression for the bug expandCategoriesFromPreparedScan
	// guards against: a --from-file scan naming only "docker" must never
	// cause a registered whole-domain cleaner ("brew", standing in for
	// homebrew/development-artifacts/trash) to run at all, let alone with an
	// empty entry list.
	brew := &wholeDomainSpyCleaner{category: "brew"}
	registry := cleaner.NewRegistry()
	registry.Register(brew)
	registry.Register(&spyCleaner{category: cleaner.CategoryDocker})

	cfg, err := config.New(nil, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	scanResult := commands.ScanResult{
		Categories: []commands.ScanCategoryResult{
			{Category: cleaner.CategoryDocker, TotalFiles: 1, Files: []cleaner.FileEntry{{Path: "docker://image/x", Size: 10, Category: cleaner.CategoryDocker}}},
		},
	}

	prepared, err := commands.PrepareScanResultForClean(context.Background(), registry, scanResult, nil, cfg)
	if err != nil {
		t.Fatalf("PrepareScanResultForClean: %v", err)
	}

	effective := expandCategoriesFromPreparedScan(nil, prepared)

	_, err = commands.RunCleanWithPreparedScanResult(context.Background(), registry, prepared, effective, commands.CleanerOptions{DryRun: false, Config: cfg}, nil)
	if err != nil {
		t.Fatalf("RunCleanWithPreparedScanResult: %v", err)
	}

	if brew.cleaned {
		t.Fatalf("brew.Clean was called with entries=%v; a category absent from the scan file must never be cleaned at all", brew.cleanedWith)
	}
}

func TestMergeCleanResults_CombinesCategoriesAndTotals(t *testing.T) {
	base := commands.CleanResult{
		TotalFiles: 2,
		TotalSize:  200,
		Categories: []commands.CleanCategoryResult{
			{Category: cleaner.CategoryApplicationCaches, DeletedFiles: 2, DeletedSize: 200},
		},
	}
	extra := []commands.CleanCategoryResult{
		{Category: cleaner.CategoryTemp, DeletedFiles: 3, DeletedSize: 300},
	}

	merged := mergeCleanResults(base, extra)

	if merged.TotalFiles != 5 || merged.TotalSize != 500 {
		t.Errorf("totals = %d files, %d bytes, want 5 files, 500 bytes", merged.TotalFiles, merged.TotalSize)
	}
	if len(merged.Categories) != 2 {
		t.Fatalf("len(Categories) = %d, want 2", len(merged.Categories))
	}
	if merged.Categories[0].Category != cleaner.CategoryTemp {
		t.Errorf("Categories[0] = %s, want elevate-derived categories listed first", merged.Categories[0].Category)
	}
}

func TestMergeCleanResults_ExtraErrorSetsHasErrors(t *testing.T) {
	base := commands.CleanResult{}
	extra := []commands.CleanCategoryResult{
		{Category: cleaner.CategoryTemp, Err: errors.New("boom")},
	}

	merged := mergeCleanResults(base, extra)
	if !merged.HasErrors {
		t.Error("HasErrors = false, want true when an elevate-derived category carries an error")
	}
}

func TestMergeCleanResults_NoExtraReturnsBaseUnchanged(t *testing.T) {
	base := commands.CleanResult{TotalFiles: 1, TotalSize: 10}
	merged := mergeCleanResults(base, nil)
	if merged.TotalFiles != 1 || merged.TotalSize != 10 {
		t.Errorf("merged = %+v, want base unchanged when extra is empty", merged)
	}
}

func TestMergeCleanResults_PartialErrorsSetHasErrorsButKeepTotals(t *testing.T) {
	base := commands.CleanResult{TotalFiles: 1, TotalSize: 5}
	extra := []commands.CleanCategoryResult{{
		Category:      cleaner.CategoryTemp,
		Name:          "Temp Files",
		DeletedFiles:  3,
		DeletedSize:   30,
		PartialErrors: 1,
		PartialErrorDetails: []commands.ItemError{
			{Path: "/private/tmp/locked", Reason: "operation not permitted"},
		},
	}}

	merged := mergeCleanResults(base, extra)
	if !merged.HasErrors {
		t.Fatal("HasErrors = false, want true for an elevated category with partial failures")
	}
	if merged.TotalFiles != 4 || merged.TotalSize != 35 {
		t.Fatalf("totals = %d/%d, want 4/35 (partial failures keep what was reclaimed)", merged.TotalFiles, merged.TotalSize)
	}
	if got := failedCategoryNames(merged); len(got) != 1 || got[0] != "Temp Files" {
		t.Fatalf("failedCategoryNames = %v, want [Temp Files]", got)
	}
}

// splitPrivilegeCleaner is a RequiresSudo cleaner that needs root only for
// entries under /sudo/, standing in for Temp's real /tmp vs $TMPDIR split, so
// elevateForClean's privilege-split behavior can be exercised without a real
// TempCleaner or real filesystem paths.
type splitPrivilegeCleaner struct {
	category cleaner.Category
	entries  []cleaner.FileEntry

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
	return &cleaner.ScanResult{Category: c.category, Entries: c.entries, TotalFiles: len(c.entries)}, nil
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

// withLoadedConfig sets the package-level loadedConfig elevateForClean reads,
// and restores whatever was there before -- mirroring the save/set/cleanup
// pattern used by list_protected_test.go and unprotect_test.go.
func withLoadedConfig(t *testing.T) {
	t.Helper()
	cfg, err := config.New(nil, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	prev := loadedConfig
	loadedConfig = cfg
	t.Cleanup(func() { loadedConfig = prev })
}

// stubInvokeElevated swaps invokeElevated for fn and restores it afterwards
// -- the same seam-substitution shape internal/elevate's own tests use for
// sudoCommand/sudoAuthCommand, but reachable from cmd since invokeElevated is
// declared in this package specifically so elevateForClean is testable
// without spawning a real sudo prompt.
func stubInvokeElevated(t *testing.T, fn func(ctx context.Context, plan elevate.Plan) (elevate.Result, error)) {
	t.Helper()
	prev := invokeElevated
	invokeElevated = fn
	t.Cleanup(func() { invokeElevated = prev })
}

func assertNoDuplicateCategories(t *testing.T, results []commands.CleanCategoryResult) {
	t.Helper()
	seen := make(map[cleaner.Category]struct{}, len(results))
	for _, r := range results {
		if _, ok := seen[r.Category]; ok {
			t.Fatalf("category %s appears more than once in results: %+v", r.Category, results)
		}
		seen[r.Category] = struct{}{}
	}
}

func TestElevateForClean_SplitCategoryMergesDirectAndSudoLegs(t *testing.T) {
	withLoadedConfig(t)

	const cat cleaner.Category = "split_cat"
	c := &splitPrivilegeCleaner{
		category: cat,
		entries: []cleaner.FileEntry{
			{Path: "/sudo/a", Size: 10, Category: cat},
			{Path: "/sudo/b", Size: 20, Category: cat},
			{Path: "/direct/c", Size: 5, Category: cat},
			{Path: "/direct/d", Size: 7, Category: cat},
		},
	}
	registry := cleaner.NewRegistry()
	registry.Register(c)

	var gotPlan elevate.Plan
	invokeCalled := false
	stubInvokeElevated(t, func(_ context.Context, plan elevate.Plan) (elevate.Result, error) {
		invokeCalled = true
		gotPlan = plan
		if c.cleanCalls != 0 {
			t.Errorf("direct leg ran before elevation completed: %d call(s)", c.cleanCalls)
		}
		return elevate.Result{
			Clean: commands.CleanResult{
				Categories: []commands.CleanCategoryResult{
					{Category: cat, Name: cat.DisplayName(), DeletedFiles: 2, DeletedSize: 30},
				},
			},
		}, nil
	})

	results, err := elevateForClean(context.Background(), registry, []string{string(cat)}, commands.ScanResult{}, false)
	if err != nil {
		t.Fatalf("elevateForClean: %v", err)
	}
	if !invokeCalled {
		t.Fatal("expected elevate.Invoke (stubbed) to be called: this category has sudo-required entries")
	}
	if len(gotPlan.Categories) != 1 || len(gotPlan.Categories[0].Entries) != 2 {
		t.Fatalf("plan sent to invokeElevated = %+v, want exactly the 2 /sudo/ entries", gotPlan.Categories)
	}
	for _, e := range gotPlan.Categories[0].Entries {
		if !strings.HasPrefix(e.Path, "/sudo/") {
			t.Errorf("plan entry %s should not have been sent for elevation", e.Path)
		}
	}

	assertNoDuplicateCategories(t, results)
	if len(results) != 1 {
		t.Fatalf("results = %+v, want a single merged row for %s", results, cat)
	}
	got := results[0]
	if got.DeletedFiles != 4 {
		t.Errorf("DeletedFiles = %d, want 4 (2 direct + 2 elevated)", got.DeletedFiles)
	}
	if got.DeletedSize != 42 {
		t.Errorf("DeletedSize = %d, want 42 (5+7 direct, 10+20 elevated)", got.DeletedSize)
	}
	if got.Err != nil {
		t.Errorf("Err = %v, want nil", got.Err)
	}
	if len(got.Files) != 0 {
		t.Errorf("Files = %+v, want empty: neither leg populated Files (not --detailed)", got.Files)
	}
	if c.cleanCalls != 1 || len(c.cleanedWith) != 2 {
		t.Fatalf("direct Clean called %d time(s) with %d entries, want 1 call with the 2 /direct/ entries", c.cleanCalls, len(c.cleanedWith))
	}
}

func TestElevateForClean_DirectOnlyCategoryNeverReachesThePlan(t *testing.T) {
	withLoadedConfig(t)

	const cat cleaner.Category = "direct_only_cat"
	c := &splitPrivilegeCleaner{
		category: cat,
		entries: []cleaner.FileEntry{
			{Path: "/direct/a", Size: 5, Category: cat},
			{Path: "/direct/b", Size: 7, Category: cat},
		},
	}
	registry := cleaner.NewRegistry()
	registry.Register(c)

	stubInvokeElevated(t, func(context.Context, elevate.Plan) (elevate.Result, error) {
		t.Fatal("invokeElevated must never be called: this category has no sudo-required entries")
		return elevate.Result{}, nil
	})

	results, err := elevateForClean(context.Background(), registry, []string{string(cat)}, commands.ScanResult{}, false)
	if err != nil {
		t.Fatalf("elevateForClean: %v", err)
	}

	assertNoDuplicateCategories(t, results)
	if len(results) != 1 {
		t.Fatalf("results = %+v, want a single direct-only row for %s", results, cat)
	}
	got := results[0]
	if got.DeletedFiles != 2 || got.DeletedSize != 12 {
		t.Errorf("got %d files / %d bytes, want 2 files / 12 bytes from the direct leg alone", got.DeletedFiles, got.DeletedSize)
	}
	if got.Err != nil {
		t.Errorf("Err = %v, want nil", got.Err)
	}

	// sudoNeedMessage is driven entirely by plan.Categories; a category that
	// never entered the plan must never be named as a reason for the sudo
	// prompt it never caused.
	msg := sudoNeedMessage(nil)
	if strings.Contains(msg, cat.DisplayName()) {
		t.Errorf("sudoNeedMessage mentions %s, which never needed sudo", cat)
	}
}

func TestElevateForClean_EveryCategoryDirectOnlySkipsInvokeEntirely(t *testing.T) {
	withLoadedConfig(t)

	const catA, catB cleaner.Category = "direct_a", "direct_b"
	a := &splitPrivilegeCleaner{category: catA, entries: []cleaner.FileEntry{{Path: "/direct/a", Size: 3, Category: catA}}}
	b := &splitPrivilegeCleaner{category: catB, entries: []cleaner.FileEntry{{Path: "/direct/b", Size: 4, Category: catB}}}
	registry := cleaner.NewRegistry()
	registry.Register(a)
	registry.Register(b)

	stubInvokeElevated(t, func(context.Context, elevate.Plan) (elevate.Result, error) {
		t.Fatal("invokeElevated must never be called when every sudoName resolves via the direct leg")
		return elevate.Result{}, nil
	})

	results, err := elevateForClean(context.Background(), registry, []string{string(catA), string(catB)}, commands.ScanResult{}, false)
	if err != nil {
		t.Fatalf("elevateForClean: %v", err)
	}

	assertNoDuplicateCategories(t, results)
	if len(results) != 2 {
		t.Fatalf("results = %+v, want one row per direct-only category", results)
	}
}

func TestElevateForClean_OnlySudoEntriesUnchanged(t *testing.T) {
	withLoadedConfig(t)

	const cat cleaner.Category = "sudo_only_cat"
	c := &splitPrivilegeCleaner{
		category: cat,
		entries: []cleaner.FileEntry{
			{Path: "/sudo/a", Size: 10, Category: cat},
			{Path: "/sudo/b", Size: 20, Category: cat},
		},
	}
	registry := cleaner.NewRegistry()
	registry.Register(c)

	invokeCalled := false
	stubInvokeElevated(t, func(_ context.Context, plan elevate.Plan) (elevate.Result, error) {
		invokeCalled = true
		if len(plan.Categories) != 1 || len(plan.Categories[0].Entries) != 2 {
			t.Fatalf("plan = %+v, want the single category with both entries", plan.Categories)
		}
		return elevate.Result{
			Clean: commands.CleanResult{
				Categories: []commands.CleanCategoryResult{
					{Category: cat, Name: cat.DisplayName(), DeletedFiles: 2, DeletedSize: 30},
				},
			},
		}, nil
	})

	results, err := elevateForClean(context.Background(), registry, []string{string(cat)}, commands.ScanResult{}, false)
	if err != nil {
		t.Fatalf("elevateForClean: %v", err)
	}
	if !invokeCalled {
		t.Fatal("expected invokeElevated to be called")
	}

	assertNoDuplicateCategories(t, results)
	if len(results) != 1 {
		t.Fatalf("results = %+v, want a single row for %s", results, cat)
	}
	if got := results[0]; got.DeletedFiles != 2 || got.DeletedSize != 30 {
		t.Errorf("got %d files / %d bytes, want 2 files / 30 bytes (the elevated leg, unchanged)", got.DeletedFiles, got.DeletedSize)
	}
	if c.cleanCalls != 0 {
		t.Errorf("direct Clean called %d time(s), want 0: every entry needed sudo", c.cleanCalls)
	}
}

func TestElevateForClean_ElevatedFailureDoesNotStartDirectLeg(t *testing.T) {
	withLoadedConfig(t)

	const cat cleaner.Category = "mixed_failure_cat"
	c := &splitPrivilegeCleaner{
		category: cat,
		entries: []cleaner.FileEntry{
			{Path: "/sudo/a", Size: 10, Category: cat},
			{Path: "/direct/b", Size: 5, Category: cat},
		},
	}
	registry := cleaner.NewRegistry()
	registry.Register(c)

	stubInvokeElevated(t, func(context.Context, elevate.Plan) (elevate.Result, error) {
		return elevate.Result{}, elevate.ErrElevationFailed
	})

	results, err := elevateForClean(context.Background(), registry, []string{string(cat)}, commands.ScanResult{}, false)
	if !errors.Is(err, elevate.ErrElevationFailed) {
		t.Fatalf("elevateForClean error = %v, want ErrElevationFailed", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want none from an aborted orchestration", results)
	}
	if c.cleanCalls != 0 || len(c.cleanedWith) != 0 {
		t.Fatalf("direct leg ran after elevation failure: calls=%d entries=%v", c.cleanCalls, c.cleanedWith)
	}
}

// TestElevatedFailureAfterRealDirectDeletion_TotalsAndHistoryStillReportIt is
// the exact scenario a security review flagged: a split category's direct
// leg deletes real files, and its elevated leg then fails (mistyped sudo
// password) or comes back with an unknown outcome. commands.MergeCategoryResults
// correctly sums the direct leg's counts into the merged row's
// DeletedFiles/DeletedSize alongside the elevated leg's Err -- but
// mergeCleanResults and buildRunRecord each used to have their own "Err !=
// nil means nothing happened" shortcut that discarded those counts anyway.
// A row with Err != nil and DeletedFiles > 0 must still show up in both the
// displayed totals and the history record.
func TestElevatedFailureAfterRealDirectDeletion_TotalsAndHistoryStillReportIt(t *testing.T) {
	merged := commands.MergeCategoryResults(
		commands.CleanCategoryResult{
			Category:     cleaner.CategoryTemp,
			Name:         cleaner.CategoryTemp.DisplayName(),
			DeletedFiles: 3,
			DeletedSize:  300,
		},
		commands.CleanCategoryResult{
			Category: cleaner.CategoryTemp,
			Name:     cleaner.CategoryTemp.DisplayName(),
			ErrMsg:   "elevation did not run (elevated helper did not run; nothing was deleted); nothing was deleted",
			Err:      errors.New("elevation did not run (elevated helper did not run; nothing was deleted); nothing was deleted"),
		},
	)
	if merged.Err == nil {
		t.Fatal("sanity check: MergeCategoryResults should still carry the elevated leg's error")
	}
	if merged.DeletedFiles != 3 || merged.DeletedSize != 300 {
		t.Fatalf("sanity check: merged row = %d files/%d bytes, want the direct leg's 3/300 to survive the merge", merged.DeletedFiles, merged.DeletedSize)
	}

	t.Run("mergeCleanResults totals include the errored row's real counts", func(t *testing.T) {
		result := mergeCleanResults(commands.CleanResult{}, []commands.CleanCategoryResult{merged})
		if !result.HasErrors {
			t.Error("HasErrors = false, want true: the elevated leg failed")
		}
		if result.TotalFiles != 3 || result.TotalSize != 300 {
			t.Errorf("totals = %d files / %d bytes, want 3 files / 300 bytes: a completed direct deletion must not be hidden behind the elevated leg's failure", result.TotalFiles, result.TotalSize)
		}
	})

	t.Run("buildRunRecord still records the errored row's real deletion", func(t *testing.T) {
		record := buildRunRecord(commands.CleanResult{
			CleanedAt:  time.Now().UTC(),
			Categories: []commands.CleanCategoryResult{merged},
		}, 0)
		if len(record.Categories) != 1 {
			t.Fatalf("record.Categories = %+v, want one entry for the category that actually deleted files", record.Categories)
		}
		got := record.Categories[0]
		if got.Files != 3 || got.Bytes != 300 {
			t.Errorf("recorded = %d files / %d bytes, want 3 files / 300 bytes", got.Files, got.Bytes)
		}
		if record.TotalFiles != 3 || record.TotalBytes != 300 {
			t.Errorf("record totals = %d files / %d bytes, want 3 files / 300 bytes", record.TotalFiles, record.TotalBytes)
		}
	})
}

func TestWritePartialErrors_RendersPathReasonAndTruncation(t *testing.T) {
	var b strings.Builder
	writePartialErrors(&b, commands.CleanCategoryResult{
		Name:          "Temp Files",
		PartialErrors: 3,
		PartialErrorDetails: []commands.ItemError{
			{Path: "/private/tmp/locked", Reason: "operation not permitted"},
			{Reason: "no path in this one"},
		},
		PartialErrorsTruncated: true,
	}, "  ")

	out := b.String()
	for _, want := range []string{
		"  3 item(s) could not be cleaned:",
		"    /private/tmp/locked: operation not permitted",
		"    no path in this one",
		"    ... 1 more not shown",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}

	b.Reset()
	writePartialErrors(&b, commands.CleanCategoryResult{Name: "Clean"}, "  ")
	if b.Len() != 0 {
		t.Fatalf("a category without partial errors must render nothing, got %q", b.String())
	}
}
