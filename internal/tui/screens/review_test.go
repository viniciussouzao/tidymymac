package screens

import (
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

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
