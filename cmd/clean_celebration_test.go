package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
)

func TestCleanModelStoresAndRendersOneCelebration(t *testing.T) {
	model := cleanModel{cleaning: true}
	updated, _ := model.Update(cleanDoneMsg{result: commands.CleanResult{
		TotalFiles: 3,
		TotalSize:  2 << 30,
		Categories: []commands.CleanCategoryResult{
			{Category: cleaner.CategoryTemp, Name: cleaner.CategoryTemp.DisplayName(), DeletedSize: 40 << 20},
			{Category: cleaner.CategoryDocker, Name: cleaner.CategoryDocker.DisplayName(), DeletedSize: 2 << 30},
		},
	}})
	finished := updated.(cleanModel)

	if finished.celebration == "" {
		t.Fatal("celebration was not stored when clean result arrived")
	}
	if !strings.Contains(finished.celebration, cleaner.CategoryDocker.DisplayName()) {
		t.Errorf("celebration = %q, want largest category", finished.celebration)
	}

	view := finished.View()
	if got := strings.Count(view, finished.celebration); got != 1 {
		t.Errorf("clean model renders celebration %d times, want 1\n%s", got, view)
	}
	if strings.Index(view, finished.celebration) < strings.Index(view, "Total") ||
		strings.Index(view, finished.celebration) > strings.Index(view, "Cleanup finished") {
		t.Errorf("celebration must be between total and history hint\n%s", view)
	}
}

func TestCleanModelOmitsCelebrationForDryRunZeroSpaceAndFailure(t *testing.T) {
	tests := []struct {
		name   string
		dryRun bool
		result commands.CleanResult
	}{
		{
			name:   "dry run",
			dryRun: true,
			result: commands.CleanResult{Categories: []commands.CleanCategoryResult{{Category: cleaner.CategoryTemp, DeletedSize: 50 << 20}}},
		},
		{
			name:   "zero space",
			result: commands.CleanResult{Categories: []commands.CleanCategoryResult{{Category: cleaner.CategoryTemp}}},
		},
		{
			name: "all failed",
			result: commands.CleanResult{Categories: []commands.CleanCategoryResult{{
				Category: cleaner.CategoryDocker, DeletedSize: 2 << 30, Err: errors.New("clean failed"),
			}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := cleanModel{cleaning: true, dryRun: tt.dryRun}
			updated, _ := model.Update(cleanDoneMsg{result: tt.result})
			if got := updated.(cleanModel).celebration; got != "" {
				t.Errorf("celebration = %q, want empty", got)
			}
		})
	}
}

func TestCleanCelebrationIgnoresFailedCategoriesInPartialCleanup(t *testing.T) {
	message := cleanCelebration(commands.CleanResult{Categories: []commands.CleanCategoryResult{
		{Category: cleaner.CategoryDocker, DeletedSize: 8 << 30, Err: errors.New("permission denied")},
		{Category: cleaner.CategoryLogs, DeletedSize: 200 << 20},
	}})

	if !strings.Contains(message, cleaner.CategoryLogs.DisplayName()) {
		t.Errorf("celebration = %q, want successful category", message)
	}
	if strings.Contains(message, cleaner.CategoryDocker.DisplayName()) {
		t.Errorf("celebration = %q, must not use failed category", message)
	}
}
