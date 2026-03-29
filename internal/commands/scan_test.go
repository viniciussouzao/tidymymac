package commands

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// mockCleaner is a test double implementing the cleaner.Cleaner interface.
type mockCleaner struct {
	category cleaner.Category
	name     string
	entries  []cleaner.FileEntry
	err      error
}

func (m *mockCleaner) Category() cleaner.Category { return m.category }
func (m *mockCleaner) Name() string               { return m.name }
func (m *mockCleaner) Description() string        { return "mock cleaner" }
func (m *mockCleaner) RequiresSudo() bool         { return false }

func (m *mockCleaner) Scan(_ context.Context, _ func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	if m.err != nil {
		return &cleaner.ScanResult{Category: m.category}, m.err
	}
	total := int64(0)
	for _, e := range m.entries {
		total += e.Size
	}
	return &cleaner.ScanResult{
		Category:   m.category,
		Entries:    m.entries,
		TotalSize:  total,
		TotalFiles: len(m.entries),
	}, nil
}

func (m *mockCleaner) Clean(_ context.Context, _ []cleaner.FileEntry, dryRun bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	return &cleaner.CleanResult{Category: m.category, DryRun: dryRun}, nil
}

func newMockRegistry(mocks ...*mockCleaner) *cleaner.Registry {
	r := cleaner.NewRegistry()
	for _, c := range mocks {
		r.Register(c)
	}
	return r
}

// --- resolveCleaners ---

func TestResolveCleaners_EmptySelectedReturnsAll(t *testing.T) {
	r := newMockRegistry(
		&mockCleaner{category: "cat_a"},
		&mockCleaner{category: "cat_b"},
	)
	got, err := resolveCleaners(r, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestResolveCleaners_SpecificCategories(t *testing.T) {
	r := newMockRegistry(
		&mockCleaner{category: "cat_a"},
		&mockCleaner{category: "cat_b"},
		&mockCleaner{category: "cat_c"},
	)
	got, err := resolveCleaners(r, []string{"cat_a", "cat_c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Category() != "cat_a" {
		t.Errorf("got[0] = %q, want cat_a", got[0].Category())
	}
	if got[1].Category() != "cat_c" {
		t.Errorf("got[1] = %q, want cat_c", got[1].Category())
	}
}

func TestResolveCleaners_InvalidCategoryReturnsError(t *testing.T) {
	r := newMockRegistry(&mockCleaner{category: "cat_a"})
	_, err := resolveCleaners(r, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown category, got nil")
	}
	if !containsString(err.Error(), "nonexistent") {
		t.Errorf("error %q should mention the unknown category", err.Error())
	}
}

// --- RunScan ---

func TestRunScan_PreservesRegistryOrder(t *testing.T) {
	// Goroutines may complete in any order; results must match registration order.
	r := newMockRegistry(
		&mockCleaner{category: "first", name: "First"},
		&mockCleaner{category: "second", name: "Second"},
		&mockCleaner{category: "third", name: "Third"},
	)

	result, err := RunScan(t.Context(), r, nil, ScanOptions{}, nil)
	if err != nil {
		t.Fatalf("RunScan() error: %v", err)
	}
	if len(result.Categories) != 3 {
		t.Fatalf("got %d categories, want 3", len(result.Categories))
	}

	want := []cleaner.Category{"first", "second", "third"}
	for i, w := range want {
		if result.Categories[i].Category != w {
			t.Errorf("Categories[%d] = %q, want %q", i, result.Categories[i].Category, w)
		}
	}
}

func TestRunScan_AggregatesTotal(t *testing.T) {
	r := newMockRegistry(
		&mockCleaner{
			category: "cat_a",
			entries:  []cleaner.FileEntry{{Size: 1024}, {Size: 512}},
		},
		&mockCleaner{
			category: "cat_b",
			entries:  []cleaner.FileEntry{{Size: 2048}},
		},
	)

	result, err := RunScan(t.Context(), r, nil, ScanOptions{}, nil)
	if err != nil {
		t.Fatalf("RunScan() error: %v", err)
	}
	if result.TotalFiles != 3 {
		t.Errorf("TotalFiles = %d, want 3", result.TotalFiles)
	}
	if result.TotalSize != 3584 {
		t.Errorf("TotalSize = %d, want 3584", result.TotalSize)
	}
}

func TestRunScan_HasErrorsWhenCleanerFails(t *testing.T) {
	r := newMockRegistry(
		&mockCleaner{category: "ok_cat", entries: []cleaner.FileEntry{{Size: 100}}},
		&mockCleaner{category: "fail_cat", err: errors.New("scan failed")},
	)

	result, err := RunScan(t.Context(), r, nil, ScanOptions{}, nil)
	if err != nil {
		t.Fatalf("RunScan() should not return a top-level error, got: %v", err)
	}
	if !result.HasErrors {
		t.Error("HasErrors = false, want true")
	}

	var failed *ScanCategoryResult
	for i := range result.Categories {
		if result.Categories[i].Category == "fail_cat" {
			failed = &result.Categories[i]
			break
		}
	}
	if failed == nil {
		t.Fatal("failed category not present in results")
	}
	if failed.ErrMsg == "" {
		t.Error("ErrMsg should be set for failed category")
	}
}

func TestRunScan_FailedCategoryExcludedFromTotal(t *testing.T) {
	r := newMockRegistry(
		&mockCleaner{category: "ok_cat", entries: []cleaner.FileEntry{{Size: 500}}},
		&mockCleaner{category: "fail_cat", err: errors.New("boom")},
	)

	result, _ := RunScan(t.Context(), r, nil, ScanOptions{}, nil)
	if result.TotalSize != 500 {
		t.Errorf("TotalSize = %d, want 500 (failed category must not contribute)", result.TotalSize)
	}
	if result.TotalFiles != 1 {
		t.Errorf("TotalFiles = %d, want 1", result.TotalFiles)
	}
}

func TestRunScan_ScannedAtIsSet(t *testing.T) {
	before := time.Now().UTC()
	r := newMockRegistry(&mockCleaner{category: "cat_a"})

	result, err := RunScan(t.Context(), r, nil, ScanOptions{}, nil)
	if err != nil {
		t.Fatalf("RunScan() error: %v", err)
	}
	after := time.Now().UTC()

	if result.ScannedAt.IsZero() {
		t.Fatal("ScannedAt should not be zero")
	}
	if result.ScannedAt.Before(before) || result.ScannedAt.After(after) {
		t.Errorf("ScannedAt = %v is outside expected range [%v, %v]", result.ScannedAt, before, after)
	}
}

func TestRunScan_TotalSizeHuman(t *testing.T) {
	r := newMockRegistry(&mockCleaner{
		category: "cat_a",
		entries:  []cleaner.FileEntry{{Size: 1024}},
	})

	result, err := RunScan(t.Context(), r, nil, ScanOptions{}, nil)
	if err != nil {
		t.Fatalf("RunScan() error: %v", err)
	}
	if result.TotalSizeHuman != "1.0 KB" {
		t.Errorf("TotalSizeHuman = %q, want %q", result.TotalSizeHuman, "1.0 KB")
	}
	if result.Categories[0].TotalSizeHuman != "1.0 KB" {
		t.Errorf("Categories[0].TotalSizeHuman = %q, want %q", result.Categories[0].TotalSizeHuman, "1.0 KB")
	}
}

func TestRunScan_DetailedPopulatesFiles(t *testing.T) {
	entries := []cleaner.FileEntry{
		{Path: "/tmp/a", Size: 100},
		{Path: "/tmp/b", Size: 200},
	}
	r := newMockRegistry(&mockCleaner{category: "cat_a", entries: entries})

	detailed, err := RunScan(t.Context(), r, nil, ScanOptions{Detailed: true}, nil)
	if err != nil {
		t.Fatalf("RunScan(detailed) error: %v", err)
	}
	if len(detailed.Categories[0].Files) != 2 {
		t.Errorf("Files count = %d, want 2", len(detailed.Categories[0].Files))
	}

	summary, err := RunScan(t.Context(), r, nil, ScanOptions{Detailed: false}, nil)
	if err != nil {
		t.Fatalf("RunScan(summary) error: %v", err)
	}
	if len(summary.Categories[0].Files) != 0 {
		t.Errorf("Files should be empty without Detailed, got %d entries", len(summary.Categories[0].Files))
	}
}

// --- writeCSV ---

func TestWriteCSV_SummaryMode(t *testing.T) {
	result := ScanResult{
		TotalFiles:     3,
		TotalSize:      3072,
		TotalSizeHuman: "3.0 KB",
		Categories: []ScanCategoryResult{
			{Name: "Temp Files", TotalFiles: 1, TotalSize: 1024, TotalSizeHuman: "1.0 KB"},
			{Name: "Docker Artifacts", TotalFiles: 2, TotalSize: 2048, TotalSizeHuman: "2.0 KB"},
		},
	}

	var buf bytes.Buffer
	if err := writeCSV(&buf, result, false); err != nil {
		t.Fatalf("writeCSV() error: %v", err)
	}

	records := mustParseCSV(t, &buf)
	// header + 2 category rows + total row
	if len(records) != 4 {
		t.Fatalf("got %d rows, want 4", len(records))
	}
	if records[0][0] != "category" {
		t.Errorf("header[0] = %q, want category", records[0][0])
	}
	if records[1][0] != "Temp Files" {
		t.Errorf("row 1 category = %q, want Temp Files", records[1][0])
	}
	if records[1][1] != "1" {
		t.Errorf("row 1 files = %q, want 1", records[1][1])
	}
	if records[1][3] != "1.0 KB" {
		t.Errorf("row 1 size_human = %q, want 1.0 KB", records[1][3])
	}
	last := records[len(records)-1]
	if last[0] != "Total" {
		t.Errorf("total row category = %q, want Total", last[0])
	}
	if last[1] != "3" {
		t.Errorf("total files = %q, want 3", last[1])
	}
}

func TestWriteCSV_DetailedMode(t *testing.T) {
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	result := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Name: "Temp Files",
				Files: []cleaner.FileEntry{
					{Path: "/tmp/a", Size: 512, IsDir: false, ModTime: now},
					{Path: "/tmp/b", Size: 1024, IsDir: true, ModTime: now},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := writeCSV(&buf, result, true); err != nil {
		t.Fatalf("writeCSV() error: %v", err)
	}

	records := mustParseCSV(t, &buf)
	// header + 2 file rows
	if len(records) != 3 {
		t.Fatalf("got %d rows, want 3", len(records))
	}
	if records[0][0] != "category" || records[0][1] != "path" {
		t.Errorf("unexpected detailed header: %v", records[0])
	}
	if records[1][0] != "Temp Files" {
		t.Errorf("file row category = %q, want Temp Files", records[1][0])
	}
	if records[1][1] != "/tmp/a" {
		t.Errorf("file row path = %q, want /tmp/a", records[1][1])
	}
	if records[1][3] != "512 B" {
		t.Errorf("file row size_human = %q, want 512 B", records[1][3])
	}
	if records[2][4] != "true" {
		t.Errorf("file row is_dir = %q, want true", records[2][4])
	}
}

func TestWriteCSV_CategoryWithError(t *testing.T) {
	result := ScanResult{
		HasErrors: true,
		Categories: []ScanCategoryResult{
			{Name: "Docker Artifacts", ErrMsg: "docker not running"},
		},
	}

	var buf bytes.Buffer
	if err := writeCSV(&buf, result, false); err != nil {
		t.Fatalf("writeCSV() error: %v", err)
	}

	records := mustParseCSV(t, &buf)
	// header + 1 category row + total row
	if len(records) != 3 {
		t.Fatalf("got %d rows, want 3", len(records))
	}
	if records[1][4] != "docker not running" {
		t.Errorf("error field = %q, want docker not running", records[1][4])
	}
}

// --- helpers ---

func mustParseCSV(t *testing.T, buf *bytes.Buffer) [][]string {
	t.Helper()
	records, err := csv.NewReader(buf).ReadAll()
	if err != nil {
		t.Fatalf("csv.ReadAll() error: %v", err)
	}
	return records
}

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
