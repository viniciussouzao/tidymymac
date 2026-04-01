package screens

import (
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

func TestCleaningModelOverallExecutionProgressCountsSkippedAndPartial(t *testing.T) {
	m := CleaningModel{
		Categories: []CleaningCategory{
			{Category: cleaner.CategoryCaches, Status: "done", FilesTotal: 10, FilesDeleted: 10},
			{Category: cleaner.CategoryLogs, Status: "skipped", FilesTotal: 5},
			{Category: cleaner.CategoryTemp, Status: "cleaning", FilesTotal: 4, FilesDeleted: 2},
			{Category: cleaner.CategoryDocker, Status: "pending", FilesTotal: 3},
		},
	}

	got := m.overallExecutionProgress()
	want := 0.625
	if got != want {
		t.Fatalf("overallExecutionProgress() = %v, want %v", got, want)
	}
}

func TestCleaningModelSkipCategoryMarksTerminalState(t *testing.T) {
	m := CleaningModel{
		Categories: []CleaningCategory{
			{Category: cleaner.CategoryLogs, Status: "cleaning"},
			{Category: cleaner.CategoryCaches, Status: "done"},
		},
	}

	m.SkipCategory(cleaner.CategoryLogs, "requires sudo")

	if m.Categories[0].Status != "skipped" {
		t.Fatalf("status = %q, want skipped", m.Categories[0].Status)
	}
	if m.Categories[0].SkipReason != "requires sudo" {
		t.Fatalf("SkipReason = %q, want requires sudo", m.Categories[0].SkipReason)
	}
	if !m.Done {
		t.Fatal("Done = false, want true after all categories reach terminal state")
	}
}
