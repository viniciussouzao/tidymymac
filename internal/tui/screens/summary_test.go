package screens

import (
	"errors"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

func TestNewSummaryShowsOneCelebrationForSuccessfulCleanup(t *testing.T) {
	summary := NewSummary([]*cleaner.CleanResult{
		{Category: cleaner.CategoryTemp, BytesFreed: 50 << 20, FilesDeleted: 2},
		{Category: cleaner.CategoryDocker, BytesFreed: 2 << 30, FilesDeleted: 1},
	}, false)

	if summary.Celebration == "" {
		t.Fatal("Celebration is empty after reclaiming space")
	}
	if !strings.Contains(summary.Celebration, cleaner.CategoryDocker.DisplayName()) {
		t.Errorf("Celebration = %q, want it to use the largest successful category", summary.Celebration)
	}

	view := summary.View()
	if got := strings.Count(view, summary.Celebration); got != 1 {
		t.Errorf("summary renders celebration %d times, want 1\n%s", got, view)
	}
	if strings.Index(view, summary.Celebration) < strings.Index(view, "Total") {
		t.Errorf("celebration must appear below the total\n%s", view)
	}
}

func TestNewSummaryOmitsCelebrationForDryRunZeroSpaceAndFailures(t *testing.T) {
	tests := []struct {
		name    string
		results []*cleaner.CleanResult
		dryRun  bool
	}{
		{
			name:    "dry run",
			results: []*cleaner.CleanResult{{Category: cleaner.CategoryTemp, BytesFreed: 50 << 20}},
			dryRun:  true,
		},
		{
			name:    "zero bytes",
			results: []*cleaner.CleanResult{{Category: cleaner.CategoryTemp}},
		},
		{
			name:    "all failed",
			results: []*cleaner.CleanResult{{Category: cleaner.CategoryDocker, BytesFreed: 2 << 30, Errors: []error{errors.New("clean failed")}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := NewSummary(tt.results, tt.dryRun)
			if summary.Celebration != "" {
				t.Errorf("Celebration = %q, want empty", summary.Celebration)
			}
		})
	}
}

func TestNewSummaryCelebratesOnlySuccessfulCategoriesInPartialCleanup(t *testing.T) {
	summary := NewSummary([]*cleaner.CleanResult{
		{Category: cleaner.CategoryDocker, BytesFreed: 8 << 30, Errors: []error{errors.New("permission denied")}},
		{Category: cleaner.CategoryLogs, BytesFreed: 200 << 20},
	}, false)

	if !strings.Contains(summary.Celebration, cleaner.CategoryLogs.DisplayName()) {
		t.Errorf("Celebration = %q, want successful category %q", summary.Celebration, cleaner.CategoryLogs.DisplayName())
	}
	if strings.Contains(summary.Celebration, cleaner.CategoryDocker.DisplayName()) {
		t.Errorf("Celebration = %q, must not use failed category", summary.Celebration)
	}
}

func TestNewSummaryIgnoresNilResults(t *testing.T) {
	summary := NewSummary([]*cleaner.CleanResult{
		nil,
		{Category: cleaner.CategoryLogs, BytesFreed: 200 << 20, FilesDeleted: 3},
	}, false)

	if summary.TotalFreed != 200<<20 || summary.TotalFiles != 3 {
		t.Errorf("totals = %d bytes / %d files, want only the non-nil result", summary.TotalFreed, summary.TotalFiles)
	}
	if !strings.Contains(summary.Celebration, cleaner.CategoryLogs.DisplayName()) {
		t.Errorf("Celebration = %q, want it to use the non-nil category", summary.Celebration)
	}
}
