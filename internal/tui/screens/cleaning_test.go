package screens

import (
	"testing"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

func TestCleaningModelOverallExecutionProgressCountsSkippedAndPartial(t *testing.T) {
	m := CleaningModel{
		Categories: []CleaningCategory{
			{Category: cleaner.CategoryApplicationCaches, Status: "done", FilesTotal: 10, FilesDeleted: 10},
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
			{Category: cleaner.CategoryApplicationCaches, Status: "done"},
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

func TestCleaningModelElapsedFreezesWhenDone(t *testing.T) {
	startedAt := time.Now().Add(-2 * time.Minute)
	finishedAt := startedAt.Add(35 * time.Second)

	m := CleaningModel{
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Done:       true,
	}

	if got := m.elapsed(); got != 35*time.Second {
		t.Fatalf("elapsed() = %s, want %s", got, 35*time.Second)
	}
}
