package screens

import (
	"context"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// wholeDomainSelectableMockCleaner is a test double that (incorrectly, on
// purpose) claims both cleaner.ItemSelectable and DeletesWholeDomain --
// standing in for a future cleaner that might make that mistake, since none
// of the four real cleaners that implement ItemSelectable today do.
type wholeDomainSelectableMockCleaner struct{}

func (wholeDomainSelectableMockCleaner) Category() cleaner.Category { return "mock_whole_selectable" }
func (wholeDomainSelectableMockCleaner) Name() string                { return "mock" }
func (wholeDomainSelectableMockCleaner) Description() string         { return "mock" }
func (wholeDomainSelectableMockCleaner) RequiresSudo() bool          { return false }
func (wholeDomainSelectableMockCleaner) DeletesWholeDomain() bool    { return true }
func (wholeDomainSelectableMockCleaner) SupportsItemSelection() bool { return true }
func (wholeDomainSelectableMockCleaner) Scan(context.Context, func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	return nil, nil
}
func (wholeDomainSelectableMockCleaner) Clean(context.Context, []cleaner.FileEntry, bool, func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	return nil, nil
}

func TestReviewModelShouldWarnAboutSudo(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewLogsCleaner())
	registry.Register(cleaner.NewCachesCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryLogs: {
			Category:   cleaner.CategoryLogs,
			TotalSize:  10,
			TotalFiles: 1,
			Entries:    []cleaner.FileEntry{{Path: "/logs/a", Size: 10}},
		},
		cleaner.CategoryApplicationCaches: {
			Category:   cleaner.CategoryApplicationCaches,
			TotalSize:  20,
			TotalFiles: 2,
			Entries:    []cleaner.FileEntry{{Path: "/caches/a", Size: 12}, {Path: "/caches/b", Size: 8}},
		},
	}

	m := NewReview(results, true, registry, false)

	if !m.ShouldWarnAboutSudo() {
		t.Fatal("ShouldWarnAboutSudo() = false, want true")
	}

	// AuthenticateSudo defaults to false (skip), so the sudo category (Logs,
	// size 10) is excluded from the total by default -- only caches (20)
	// count until the user explicitly opts into authenticating.
	if m.AuthenticateSudo {
		t.Fatal("AuthenticateSudo default = true, want false")
	}
	size, files := m.actionableTotals()
	if size != 20 || files != 2 {
		t.Fatalf("actionableTotals() with AuthenticateSudo=false (default) = (%d, %d), want (20, 2)", size, files)
	}

	// Choosing to authenticate instead includes the sudo category too.
	m.AuthenticateSudo = true
	size, files = m.actionableTotals()
	if size != 30 || files != 3 {
		t.Fatalf("actionableTotals() with AuthenticateSudo=true = (%d, %d), want (30, 3)", size, files)
	}
}

func TestReviewModelDoesNotWarnWhenElevated(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalSize:  42,
			TotalFiles: 3,
			Entries: []cleaner.FileEntry{
				{Path: "/tmp/a", Size: 20},
				{Path: "/tmp/b", Size: 12},
				{Path: "/tmp/c", Size: 10},
			},
		},
	}

	m := NewReview(results, true, registry, true)

	if m.ShouldWarnAboutSudo() {
		t.Fatal("ShouldWarnAboutSudo() = true, want false for elevated execution")
	}

	size, files := m.actionableTotals()
	if size != 42 || files != 3 {
		t.Fatalf("actionableTotals() = (%d, %d), want (42, 3)", size, files)
	}
}

func TestReviewModel_MarksProtectedFilesWithoutRemovingFromDisplay(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalSize:  30,
			TotalFiles: 2,
			Entries: []cleaner.FileEntry{
				{Path: "/tmp/secret", Size: 10, Protected: true},
				{Path: "/tmp/other", Size: 20},
			},
		},
	}

	m := NewReview(results, false, registry, false)

	if len(m.Categories) != 1 || len(m.Categories[0].AllFiles) != 2 {
		t.Fatalf("protected files must remain in AllFiles, got %+v", m.Categories)
	}

	var sawProtected, sawUnprotected bool
	for _, f := range m.Categories[0].AllFiles {
		if f.Path == "/tmp/secret" {
			sawProtected = f.Protected
		}
		if f.Path == "/tmp/other" {
			sawUnprotected = !f.Protected
		}
	}
	if !sawProtected {
		t.Error("expected /tmp/secret to be marked Protected")
	}
	if !sawUnprotected {
		t.Error("expected /tmp/other to remain unprotected")
	}
}

func TestReviewModel_ActionableTotalsExcludeProtectedFiles(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalSize:  30,
			TotalFiles: 2,
			Entries: []cleaner.FileEntry{
				{Path: "/tmp/secret", Size: 10, Protected: true},
				{Path: "/tmp/other", Size: 20},
			},
		},
	}

	m := NewReview(results, false, registry, false)

	size, files := m.actionableTotals()
	if size != 20 || files != 1 {
		t.Fatalf("actionableTotals() = (%d, %d), want (20, 1) excluding the protected file", size, files)
	}
}

func TestRevalidationDelta_Material(t *testing.T) {
	cases := []struct {
		name  string
		delta RevalidationDelta
		want  bool
	}{
		{"nothing changed", RevalidationDelta{}, false},
		{"missing files", RevalidationDelta{MissingFiles: 1}, true},
		{"type changed", RevalidationDelta{TypeChangedFiles: 1}, true},
		{"newly protected", RevalidationDelta{NewlyProtected: 1}, true},
		{"identity changed", RevalidationDelta{IdentityChanged: 1}, true},
		{"size changed only", RevalidationDelta{SizeChanged: true}, false},
	}
	for _, tc := range cases {
		if got := tc.delta.Material(); got != tc.want {
			t.Errorf("%s: Material() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestReviewModel_ViewRendersRevalidationDelta(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalSize:  10,
			TotalFiles: 1,
			Entries:    []cleaner.FileEntry{{Path: "/tmp/a", Size: 10}},
		},
	}

	m := NewReview(results, true, registry, false)
	m.ConfirmState = ConfirmRevalidated
	m.RevalidationDelta = &RevalidationDelta{
		MissingFiles:     2,
		TypeChangedFiles: 1,
		IdentityChanged:  3,
		TotalSize:        5,
		TotalFiles:       1,
	}

	view := m.View()
	if !strings.Contains(view, "2 item(s) no longer exist") {
		t.Errorf("View() missing the missing-files line:\n%s", view)
	}
	if !strings.Contains(view, "1 item(s) changed type") {
		t.Errorf("View() missing the type-changed line:\n%s", view)
	}
	if !strings.Contains(view, "3 item(s) changed on disk") {
		t.Errorf("View() missing the identity-changed line:\n%s", view)
	}
	if !strings.Contains(view, "confirm updated plan") {
		t.Errorf("View() missing the execute-mode re-confirm hint:\n%s", view)
	}

	m.ExecuteMode = false
	view = m.View()
	if !strings.Contains(view, "nothing will be deleted") {
		t.Errorf("View() missing the dry-run wording:\n%s", view)
	}
}

// TestReviewModel_ViewRendersEmptiedPlanIdentityChanged pins NEW-3 from the
// BRANCH-REVIEW.md follow-up: TotalFiles == 0's own delta summary (shown
// when revalidation emptied the whole plan, as opposed to the
// ConfirmRevalidated summary above for a plan that merely shrank) must also
// render IdentityChanged -- it was the one delta field with no render
// coverage in this package at all.
func TestReviewModel_ViewRendersEmptiedPlanIdentityChanged(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewTempCleaner())

	m := NewReview(map[cleaner.Category]*cleaner.ScanResult{}, true, registry, false)
	m.RevalidationDelta = &RevalidationDelta{IdentityChanged: 1}

	view := m.View()
	if !strings.Contains(view, "now empty") {
		t.Errorf("View() missing the emptied-plan message:\n%s", view)
	}
	if !strings.Contains(view, "1 item(s) changed on disk") {
		t.Errorf("View() missing the identity-changed line:\n%s", view)
	}
}

// downloadsReview builds a ReviewModel with a Selectable Downloads category
// (three files) and a non-Selectable Temp category (one file), for the
// per-item selection and filter tests below.
func downloadsReview(t *testing.T) ReviewModel {
	t.Helper()
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewDownloadsCleaner())
	registry.Register(cleaner.NewTempCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryDownloads: {
			Category:   cleaner.CategoryDownloads,
			TotalSize:  30,
			TotalFiles: 3,
			Entries: []cleaner.FileEntry{
				{Path: "/Users/vini/Downloads/movie.mp4", Size: 20},
				{Path: "/Users/vini/Downloads/installer.dmg", Size: 8},
				{Path: "/Users/vini/Downloads/locked.zip", Size: 2, Protected: true},
			},
		},
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalSize:  5,
			TotalFiles: 1,
			Entries:    []cleaner.FileEntry{{Path: "/tmp/a", Size: 5}},
		},
	}

	return NewReview(results, false, registry, false)
}

func downloadsCategoryIndex(m ReviewModel) int {
	for i, c := range m.Categories {
		if c.Category == cleaner.CategoryDownloads {
			return i
		}
	}
	return -1
}

func TestNewReview_MarksSelectableCategories(t *testing.T) {
	m := downloadsReview(t)

	for _, c := range m.Categories {
		want := c.Category == cleaner.CategoryDownloads
		if c.Selectable != want {
			t.Errorf("category %q: Selectable = %v, want %v", c.Category, c.Selectable, want)
		}
	}
}

// TestNewReview_WholeDomainClearsItemSelectableFlag pins a BRANCH-REVIEW
// follow-up finding: SupportsItemSelection() alone used to be enough to
// mark a category Selectable, with only a test (registry_test.go's
// TestItemSelectableCleaners) -- not the code itself -- ensuring no real
// cleaner combines it with DeletesWholeDomain. A whole-domain Clean ignores
// whatever entry list it's given and clears its entire domain regardless,
// so per-item selection on one would be a lie: a deselected entry gets
// deleted anyway.
func TestNewReview_WholeDomainClearsItemSelectableFlag(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(wholeDomainSelectableMockCleaner{})

	results := map[cleaner.Category]*cleaner.ScanResult{
		"mock_whole_selectable": {
			Category:   "mock_whole_selectable",
			TotalSize:  10,
			TotalFiles: 1,
			Entries:    []cleaner.FileEntry{{Path: "/mock/a", Size: 10}},
		},
	}

	m := NewReview(results, false, registry, false)
	if len(m.Categories) != 1 {
		t.Fatalf("expected exactly 1 category, got %d", len(m.Categories))
	}
	if m.Categories[0].Selectable {
		t.Error("a DeletesWholeDomain cleaner must never be marked Selectable, even if it claims SupportsItemSelection")
	}
}

func TestToggleSelected_TogglesEligibleEntry(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	// Cursor starts at index 0 globally; Downloads sorts by size desc, so
	// the largest file (movie.mp4, 20) is first.
	m.Cursor = m.globalFileIndexFor(ci, 0)

	if !m.Categories[ci].AllFiles[0].Selected {
		t.Fatal("entry should start Selected")
	}
	m.ToggleSelected()
	if m.Categories[ci].AllFiles[0].Selected {
		t.Error("ToggleSelected() did not deselect the entry")
	}
	m.ToggleSelected()
	if !m.Categories[ci].AllFiles[0].Selected {
		t.Error("ToggleSelected() did not re-select the entry")
	}
}

func TestToggleSelected_NoopOnNonSelectableCategory(t *testing.T) {
	m := downloadsReview(t)
	var tempIdx int
	for i, c := range m.Categories {
		if c.Category == cleaner.CategoryTemp {
			tempIdx = i
		}
	}
	m.Cursor = m.globalFileIndexFor(tempIdx, 0)

	m.ToggleSelected()
	if !m.Categories[tempIdx].AllFiles[0].Selected {
		t.Error("ToggleSelected() must be a no-op on a non-Selectable category")
	}
}

func TestToggleSelected_NoopOnProtectedEntry(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	// locked.zip is the smallest file, so it sorts last.
	lastFi := len(m.Categories[ci].AllFiles) - 1
	m.Cursor = m.globalFileIndexFor(ci, lastFi)
	if !m.Categories[ci].AllFiles[lastFi].Protected {
		t.Fatal("test setup: expected the cursor on the protected entry")
	}

	m.ToggleSelected()
	if !m.Categories[ci].AllFiles[lastFi].Selected {
		t.Error("ToggleSelected() must be a no-op on a Protected entry")
	}
}

func TestToggleSelectAll_TogglesBetweenAllAndNoneSelected(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	// Every non-Protected entry starts Selected (movie.mp4, installer.dmg);
	// locked.zip is Protected and must never be touched by either call.
	m.ToggleSelectAll()
	for _, f := range m.Categories[ci].AllFiles {
		if f.Protected {
			continue
		}
		if f.Selected {
			t.Errorf("ToggleSelectAll() (1st call) left %q Selected, want all deselected", f.Path)
		}
	}

	m.ToggleSelectAll()
	for _, f := range m.Categories[ci].AllFiles {
		if f.Protected {
			continue
		}
		if !f.Selected {
			t.Errorf("ToggleSelectAll() (2nd call) left %q deselected, want all re-selected", f.Path)
		}
	}

	var lockedSelected bool
	for _, f := range m.Categories[ci].AllFiles {
		if f.Protected {
			lockedSelected = f.Selected
		}
	}
	if !lockedSelected {
		t.Error("ToggleSelectAll() must never change a Protected entry's Selected field")
	}
}

func TestToggleSelectAll_NoopOnNonSelectableCategory(t *testing.T) {
	m := downloadsReview(t)
	var tempIdx int
	for i, c := range m.Categories {
		if c.Category == cleaner.CategoryTemp {
			tempIdx = i
		}
	}
	m.Cursor = m.globalFileIndexFor(tempIdx, 0)

	m.ToggleSelectAll()
	if !m.Categories[tempIdx].AllFiles[0].Selected {
		t.Error("ToggleSelectAll() must be a no-op on a non-Selectable category")
	}
}

// TestToggleSelectAll_ScopedToVisibleFilterMatches mirrors ToggleSelected's
// own filter-scoping guard: "select/deselect all" must only act on what the
// active "/" filter currently shows, the same way a single space-toggle
// already does -- otherwise it would silently sweep up rows the user
// can't see.
func TestToggleSelectAll_ScopedToVisibleFilterMatches(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	m.OpenFilter()
	for _, r := range "installer" {
		m.AppendFilterRune(r)
	}
	if got := m.categoryMatchCount(ci); got != 1 {
		t.Fatalf("test setup: categoryMatchCount() = %d, want 1", got)
	}

	m.ToggleSelectAll()

	for _, f := range m.Categories[ci].AllFiles {
		switch f.Path {
		case "/Users/vini/Downloads/installer.dmg":
			if f.Selected {
				t.Error("the filtered-in entry should have been deselected")
			}
		case "/Users/vini/Downloads/movie.mp4":
			if !f.Selected {
				t.Error("a filtered-out entry must be untouched by ToggleSelectAll()")
			}
		}
	}
}

func TestActionableTotals_ExcludesDeselectedEntries(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0) // movie.mp4, size 20
	m.ToggleSelected()

	size, files := m.actionableTotals()
	// Total actionable (excluding the always-protected locked.zip) would be
	// 20 + 8 (Downloads) + 5 (Temp) = 33 across 3 files; deselecting
	// movie.mp4 removes 20 bytes / 1 file.
	if size != 13 || files != 2 {
		t.Fatalf("actionableTotals() after deselecting movie.mp4 = (%d, %d), want (13, 2)", size, files)
	}
}

func TestExcludedByUserCount(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)

	if got := m.excludedByUserCount(); got != 0 {
		t.Fatalf("excludedByUserCount() before any toggle = %d, want 0", got)
	}

	m.Cursor = m.globalFileIndexFor(ci, 0)
	m.ToggleSelected()

	if got := m.excludedByUserCount(); got != 1 {
		t.Fatalf("excludedByUserCount() after deselecting one entry = %d, want 1", got)
	}

	view := m.View()
	if !strings.Contains(view, "1 excluded from this run") {
		t.Errorf("View() title missing the excluded count:\n%s", view)
	}
}

func TestReviewModel_ViewRendersSelectionHeaderAndCheckboxes(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)
	m.ToggleSelected()

	view := m.View()
	// 3 files total; movie.mp4 was just deselected and locked.zip is
	// Protected (never counted as selected), leaving only installer.dmg.
	if !strings.Contains(view, "1/3 selected") {
		t.Errorf("View() missing the Downloads selection header:\n%s", view)
	}
	if !strings.Contains(view, "[ ] ") {
		t.Errorf("View() missing an unchecked checkbox:\n%s", view)
	}
	if !strings.Contains(view, "[x] ") {
		t.Errorf("View() missing a checked checkbox:\n%s", view)
	}
	if !strings.Contains(view, "Temp (") && strings.Contains(view, "1/1 selected") {
		t.Errorf("Temp (non-Selectable) must keep the plain header format:\n%s", view)
	}
}

// TestReviewModel_ViewRendersSelectionHintsOnlyOnSelectableCategory pins the
// help-line fix that came with ToggleSelectAll: the review screen used to
// never advertise "/" (or space/A) as a review-screen action anywhere in
// its own help text. The hint should appear while the cursor is on a
// Selectable category and disappear once it moves to an aggregate one.
func TestReviewModel_ViewRendersSelectionHintsOnlyOnSelectableCategory(t *testing.T) {
	m := downloadsReview(t)
	downloadsIdx := downloadsCategoryIndex(m)
	var tempIdx int
	for i, c := range m.Categories {
		if c.Category == cleaner.CategoryTemp {
			tempIdx = i
		}
	}

	m.Cursor = m.globalFileIndexFor(downloadsIdx, 0)
	view := m.View()
	if !strings.Contains(view, "space: toggle") || !strings.Contains(view, "A: select/deselect all") || !strings.Contains(view, "/: filter") {
		t.Errorf("View() missing the selection hints while on a Selectable category:\n%s", view)
	}

	m.Cursor = m.globalFileIndexFor(tempIdx, 0)
	view = m.View()
	if strings.Contains(view, "select/deselect all") {
		t.Errorf("View() must not advertise selection hints on a non-Selectable category:\n%s", view)
	}
}

func TestOpenFilter_NoopOnNonSelectableCategory(t *testing.T) {
	m := downloadsReview(t)
	var tempIdx int
	for i, c := range m.Categories {
		if c.Category == cleaner.CategoryTemp {
			tempIdx = i
		}
	}
	m.Cursor = m.globalFileIndexFor(tempIdx, 0)

	m.OpenFilter()
	if m.FilterActive {
		t.Error("OpenFilter() must be a no-op on a non-Selectable category")
	}
}

func TestFilter_NarrowsMatchingEntriesCaseInsensitive(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	m.OpenFilter()
	if !m.FilterActive {
		t.Fatal("OpenFilter() did not activate filtering")
	}
	for _, r := range "MOVIE" {
		m.AppendFilterRune(r)
	}

	if got := m.categoryMatchCount(ci); got != 1 {
		t.Fatalf("categoryMatchCount() = %d, want 1 (only movie.mp4 matches)", got)
	}
	if m.VisibleCount[ci] != 1 {
		t.Fatalf("VisibleCount[Downloads] = %d, want 1", m.VisibleCount[ci])
	}

	view := m.View()
	if !strings.Contains(view, "movie.mp4") {
		t.Errorf("View() should still show the matching entry:\n%s", view)
	}
	if strings.Contains(view, "installer.dmg") {
		t.Errorf("View() should hide the non-matching entry while filtered:\n%s", view)
	}

	// Selection and category membership must survive filtering unchanged.
	if !m.Categories[ci].AllFiles[0].Selected {
		t.Error("filtering must not mutate Selected")
	}
	if len(m.Categories[ci].AllFiles) != 3 {
		t.Errorf("filtering must not remove entries from AllFiles, got %d", len(m.Categories[ci].AllFiles))
	}
}

func TestFilter_NoMatchesRendersMessage(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	m.OpenFilter()
	for _, r := range "nonexistent-query" {
		m.AppendFilterRune(r)
	}

	if got := m.categoryMatchCount(ci); got != 0 {
		t.Fatalf("categoryMatchCount() = %d, want 0", got)
	}
	view := m.View()
	if !strings.Contains(view, "no items match") {
		t.Errorf("View() missing the no-match message:\n%s", view)
	}
}

// TestHeaderLineIndexForCategory_AccountsForNoMatchLine pins a BRANCH-REVIEW
// follow-up finding: the "(no items match ...)" line View() renders in
// place of a zero-match category's (empty) file list used to be invisible
// to headerLineIndexForCategory/fileLineIndex, which compute where each
// category's header actually lands among the rendered lines -- so every
// category after a zero-match one had its scroll position off by one.
func TestHeaderLineIndexForCategory_AccountsForNoMatchLine(t *testing.T) {
	m := twoSelectableCategoriesReview(t)
	downloadsIdx, dockerIdx := 0, 1
	if m.Categories[downloadsIdx].Category != cleaner.CategoryDownloads {
		downloadsIdx, dockerIdx = dockerIdx, downloadsIdx
	}

	m.Cursor = m.globalFileIndexFor(downloadsIdx, 0)
	m.OpenFilter()
	for _, r := range "no-such-item" {
		m.AppendFilterRune(r)
	}
	if m.categoryMatchCount(downloadsIdx) != 0 {
		t.Fatalf("test setup: expected the Downloads filter to match nothing")
	}

	// Downloads' rendered block with zero matches is exactly 3 lines: its
	// own header, the "(no items match ...)" line, and the spacer -- no
	// file rows, no "+N more" line (categoryMatchCount - shown = 0).
	if got := m.headerLineIndexForCategory(dockerIdx); got != 3 {
		t.Fatalf("headerLineIndexForCategory(docker) = %d, want 3", got)
	}
	if got := m.fileLineIndex(dockerIdx, 0); got != 4 {
		t.Fatalf("fileLineIndex(docker, 0) = %d, want 4 (header line + its own header row)", got)
	}
}

// TestFilter_QueryIsSanitizedForTerminal pins a BRANCH-REVIEW follow-up
// finding: every other user-influenced string in View() goes through
// utils.SanitizeForTerminal so a control character can't inject rows or
// escape sequences into the rendered screen (see displayPath) -- the "/"
// filter's own query, typed by the same user, must not be an exception.
func TestFilter_QueryIsSanitizedForTerminal(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	m.OpenFilter()
	for _, r := range "\x1b[31mmovie" {
		m.AppendFilterRune(r)
	}

	view := m.View()
	if strings.Contains(view, "\x1b[31m") {
		t.Errorf("View() must not echo a raw escape sequence from FilterQuery:\n%q", view)
	}
}

func TestFilter_EscClearsQueryAndRestoresFullList(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	m.OpenFilter()
	for _, r := range "movie" {
		m.AppendFilterRune(r)
	}
	m.CloseFilter(true) // esc

	if m.FilterActive {
		t.Error("CloseFilter(true) must exit filter mode")
	}
	if m.FilterQuery != "" {
		t.Errorf("CloseFilter(true) must clear the query, got %q", m.FilterQuery)
	}
	if got := m.categoryMatchCount(ci); got != 3 {
		t.Fatalf("categoryMatchCount() after clearing = %d, want 3 (full list restored)", got)
	}
	view := m.View()
	if !strings.Contains(view, "installer.dmg") {
		t.Errorf("View() should show every entry again after clearing the filter:\n%s", view)
	}
}

func TestFilter_EnterKeepsQueryAppliedAndClosesOverlay(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	m.OpenFilter()
	for _, r := range "movie" {
		m.AppendFilterRune(r)
	}
	m.CloseFilter(false) // enter

	if m.FilterActive {
		t.Error("CloseFilter(false) must close the overlay")
	}
	if m.FilterQuery != "movie" {
		t.Errorf("CloseFilter(false) must keep the query, got %q", m.FilterQuery)
	}
	if got := m.categoryMatchCount(ci); got != 1 {
		t.Fatalf("categoryMatchCount() after enter = %d, want 1 (still narrowed)", got)
	}
}

func TestFilter_BackspaceRemovesLastRune(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	m.OpenFilter()
	for _, r := range "movz" {
		m.AppendFilterRune(r)
	}
	if got := m.categoryMatchCount(ci); got != 0 {
		t.Fatalf("categoryMatchCount() for %q = %d, want 0", m.FilterQuery, got)
	}
	m.BackspaceFilter()
	if m.FilterQuery != "mov" {
		t.Fatalf("BackspaceFilter() query = %q, want %q", m.FilterQuery, "mov")
	}
	if got := m.categoryMatchCount(ci); got != 1 {
		t.Fatalf("categoryMatchCount() after backspace = %d, want 1", got)
	}
}

func TestFilter_MatchesFriendlyDisplayName(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewDockerCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryDocker: {
			Category:   cleaner.CategoryDocker,
			TotalSize:  10,
			TotalFiles: 1,
			Entries: []cleaner.FileEntry{
				{Path: "docker://image/qwen2.5-coder", Size: 10},
			},
		},
	}
	m := NewReview(results, false, registry, false)
	m.Cursor = 0

	m.OpenFilter()
	for _, r := range "qwen" {
		m.AppendFilterRune(r)
	}

	if got := m.categoryMatchCount(0); got != 1 {
		t.Fatalf("categoryMatchCount() matching the friendly docker name = %d, want 1", got)
	}
}

// twoSelectableCategoriesReview builds a ReviewModel with two Selectable
// categories (Downloads and Docker), for tests about switching the "/"
// filter from one category to another.
func twoSelectableCategoriesReview(t *testing.T) ReviewModel {
	t.Helper()
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewDownloadsCleaner())
	registry.Register(cleaner.NewDockerCleaner())

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryDownloads: {
			Category:   cleaner.CategoryDownloads,
			TotalSize:  30,
			TotalFiles: 3,
			Entries: []cleaner.FileEntry{
				{Path: "/Users/vini/Downloads/movie.mp4", Size: 20},
				{Path: "/Users/vini/Downloads/installer.dmg", Size: 8},
				{Path: "/Users/vini/Downloads/zip.zip", Size: 2},
			},
		},
		cleaner.CategoryDocker: {
			Category:   cleaner.CategoryDocker,
			TotalSize:  2,
			TotalFiles: 2,
			Entries: []cleaner.FileEntry{
				{Path: "docker://image/alpha", Size: 1},
				{Path: "docker://image/beta", Size: 1},
			},
		},
	}
	return NewReview(results, false, registry, false)
}

// TestOpenFilter_SwitchingCategoryUnnarrowsPrevious pins a BRANCH-REVIEW
// follow-up finding: OpenFilter used to reassign FilterCategory to the new
// category before un-narrowing the old one, so applyFilter's own "clear the
// previous category" step ran against the wrong (already-switched)
// category, permanently pinning the old one's VisibleCount/order to its
// last query -- with no overlay, "+N more" line, or way to scroll back to
// the rows it was still hiding, even though they were still part of the
// plan and would still be deleted.
func TestOpenFilter_SwitchingCategoryUnnarrowsPrevious(t *testing.T) {
	m := twoSelectableCategoriesReview(t)
	downloadsIdx, dockerIdx := 0, 1
	if m.Categories[downloadsIdx].Category != cleaner.CategoryDownloads {
		downloadsIdx, dockerIdx = dockerIdx, downloadsIdx
	}

	m.Cursor = m.globalFileIndexFor(downloadsIdx, 0)
	m.OpenFilter()
	for _, r := range "movie" {
		m.AppendFilterRune(r)
	}
	m.CloseFilter(false) // enter: keep the query applied, close the overlay
	if got := m.categoryMatchCount(downloadsIdx); got != 1 {
		t.Fatalf("categoryMatchCount(Downloads) after filtering = %d, want 1", got)
	}

	// Move the cursor into Docker and open a filter there instead.
	m.Cursor = m.globalFileIndexFor(dockerIdx, 0)
	m.OpenFilter()

	if got := m.categoryMatchCount(downloadsIdx); got != 3 {
		t.Fatalf("categoryMatchCount(Downloads) after switching the filter away = %d, want 3 (un-narrowed)", got)
	}
	if got := m.VisibleCount[downloadsIdx]; got != 3 {
		t.Fatalf("VisibleCount[Downloads] after switching the filter away = %d, want 3", got)
	}

	view := m.View()
	if !strings.Contains(view, "installer.dmg") || !strings.Contains(view, "zip.zip") {
		t.Errorf("View() must show every Downloads entry again once its filter is no longer active:\n%s", view)
	}
}

// TestToggleSelected_NoopWhenCursorBeyondVisibleRange pins a BRANCH-REVIEW
// follow-up finding: a zero-match "/" filter leaves VisibleCount at 0 (no
// row rendered, nothing highlighted), but the cursor -- which addresses
// AllFiles directly and knows nothing about VisibleCount -- still resolves
// to a real entry. Without this guard, ToggleSelected would silently flip
// that invisible entry's Selected: deselect a file, then filter to a typo
// with no matches and press space out of habit, and the file is silently
// re-selected with nothing on screen to suggest it happened.
func TestToggleSelected_NoopWhenCursorBeyondVisibleRange(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.Cursor = m.globalFileIndexFor(ci, 0)
	m.ToggleSelected() // deselect movie.mp4 (the largest, first-sorted file)
	if m.Categories[ci].AllFiles[0].Selected {
		t.Fatal("test setup: expected movie.mp4 to be deselected")
	}

	m.OpenFilter()
	for _, r := range "no-such-file" {
		m.AppendFilterRune(r)
	}
	if got := m.categoryMatchCount(ci); got != 0 {
		t.Fatalf("categoryMatchCount() = %d, want 0", got)
	}
	if m.VisibleCount[ci] != 0 {
		t.Fatalf("VisibleCount[Downloads] = %d, want 0 (nothing rendered)", m.VisibleCount[ci])
	}

	before := make([]bool, len(m.Categories[ci].AllFiles))
	for i, f := range m.Categories[ci].AllFiles {
		before[i] = f.Selected
	}

	m.ToggleSelected() // must be a no-op: nothing is currently visible

	for i, f := range m.Categories[ci].AllFiles {
		if f.Selected != before[i] {
			t.Errorf("ToggleSelected() with a zero-match filter changed entry %d (%q) Selected from %v to %v, want no-op",
				i, f.Path, before[i], f.Selected)
		}
	}
}

func TestScrollDown_SkipsFilteredOutEntries(t *testing.T) {
	m := downloadsReview(t)
	ci := downloadsCategoryIndex(m)
	m.SetSize(80, 40)
	m.Cursor = m.globalFileIndexFor(ci, 0)

	m.OpenFilter()
	// "installer.dmg" is the only match; movie.mp4 and locked.zip are
	// filtered out and must be unreachable via ScrollDown.
	for _, r := range "installer" {
		m.AppendFilterRune(r)
	}
	m.CloseFilter(false)

	startCi, startFi := m.cursorCatFile()
	if m.Categories[startCi].AllFiles[startFi].Path != "/Users/vini/Downloads/installer.dmg" {
		t.Fatalf("cursor should land on the sole match after filtering, got %q", m.Categories[startCi].AllFiles[startFi].Path)
	}

	before := m.Cursor
	m.ScrollDown()
	if m.Cursor != before {
		t.Error("ScrollDown() should not move past the only visible (matching) entry")
	}
}
