package screens

import (
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

	size, files := m.actionableTotals()
	if size != 20 || files != 2 {
		t.Fatalf("actionableTotals() = (%d, %d), want (20, 2)", size, files)
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
