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
		},
		cleaner.CategoryApplicationCaches: {
			Category:   cleaner.CategoryApplicationCaches,
			TotalSize:  20,
			TotalFiles: 2,
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
