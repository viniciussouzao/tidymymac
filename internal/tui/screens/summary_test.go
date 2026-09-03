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

func TestSummaryShowErrorsExpandsPerItemDetail(t *testing.T) {
	summary := NewSummary([]*cleaner.CleanResult{
		{
			Category: cleaner.CategoryDocker,
			Errors: []error{
				errors.New("/private/tmp/locked: operation not permitted"),
				errors.New("/private/tmp/gone: no such file or directory"),
			},
		},
	}, false)

	collapsed := summary.View()
	if !strings.Contains(collapsed, "(2 errors)") {
		t.Errorf("collapsed view = %q, want the (N errors) summary", collapsed)
	}
	if strings.Contains(collapsed, "operation not permitted") {
		t.Errorf("collapsed view must not leak per-item detail:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "e to show error details") {
		t.Errorf("collapsed view is missing the toggle hint:\n%s", collapsed)
	}

	summary.ToggleShowErrors()
	if !summary.ShowErrors {
		t.Fatal("ToggleShowErrors did not set ShowErrors")
	}

	expanded := summary.View()
	if !strings.Contains(expanded, "operation not permitted") || !strings.Contains(expanded, "no such file or directory") {
		t.Errorf("expanded view is missing per-item detail:\n%s", expanded)
	}
	if !strings.Contains(expanded, "e to hide error details") {
		t.Errorf("expanded view is missing the toggle-off hint:\n%s", expanded)
	}

	summary.ToggleShowErrors()
	if summary.ShowErrors {
		t.Fatal("ToggleShowErrors did not clear ShowErrors on a second call")
	}
}

func TestSummaryNoErrorsOmitsToggleHint(t *testing.T) {
	summary := NewSummary([]*cleaner.CleanResult{
		{Category: cleaner.CategoryTemp, BytesFreed: 10, FilesDeleted: 1},
	}, false)

	view := summary.View()
	if strings.Contains(view, "show error details") || strings.Contains(view, "hide error details") {
		t.Errorf("view must not offer an error toggle when nothing failed:\n%s", view)
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
