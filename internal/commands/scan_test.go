package commands

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
)

// mockCleaner is a test double implementing the cleaner.Cleaner interface.
type mockCleaner struct {
	category cleaner.Category
	name     string
	entries  []cleaner.FileEntry
	err      error

	// cleanedEntries records whatever was actually passed to Clean, so tests
	// can assert on what reached deletion (e.g. that protected entries never do).
	cleanedEntries []cleaner.FileEntry
}

func (m *mockCleaner) Category() cleaner.Category { return m.category }
func (m *mockCleaner) Name() string               { return m.name }
func (m *mockCleaner) Description() string        { return "mock cleaner" }
func (m *mockCleaner) RequiresSudo() bool         { return false }
func (m *mockCleaner) DeletesWholeDomain() bool   { return false }

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

func (m *mockCleaner) Clean(_ context.Context, entries []cleaner.FileEntry, dryRun bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	m.cleanedEntries = entries
	return &cleaner.CleanResult{Category: m.category, DryRun: dryRun, FilesDeleted: len(entries)}, nil
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
	got, err := resolveCleaners(r, nil, nil)
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
	got, err := resolveCleaners(r, []string{"cat_a", "cat_c"}, nil)
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
	_, err := resolveCleaners(r, []string{"nonexistent"}, nil)
	if err == nil {
		t.Fatal("expected error for unknown category, got nil")
	}
	if !containsString(err.Error(), "nonexistent") {
		t.Errorf("error %q should mention the unknown category", err.Error())
	}
}

func TestResolveCleaners_SkipsDisabledCategoriesWhenSelectedEmpty(t *testing.T) {
	r := newMockRegistry(
		&mockCleaner{category: "cat_a"},
		&mockCleaner{category: "cat_b"},
	)
	cfg, err := config.New(nil, []string{"cat_b"})
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	got, err := resolveCleaners(r, nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Category() != "cat_a" {
		t.Errorf("got %v, want only cat_a", got)
	}
}

func TestResolveCleaners_ExplicitSelectionIgnoresDisabled(t *testing.T) {
	r := newMockRegistry(
		&mockCleaner{category: "cat_a"},
		&mockCleaner{category: "cat_b"},
	)
	cfg, err := config.New(nil, []string{"cat_b"})
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	got, err := resolveCleaners(r, []string{"cat_b"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Category() != "cat_b" {
		t.Errorf("explicit selection must win over disabled_categories, got %v", got)
	}
}

func TestResolveCleaners_AllDisabledWithNoSelectionErrors(t *testing.T) {
	r := newMockRegistry(&mockCleaner{category: "cat_a"})
	cfg, err := config.New(nil, []string{"cat_a"})
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	if _, err := resolveCleaners(r, nil, cfg); err == nil {
		t.Fatal("expected an error when all categories are disabled with no explicit selection")
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

func TestRunScan_TagsProtectedEntriesWithoutRemovingThem(t *testing.T) {
	entries := []cleaner.FileEntry{
		{Path: "/Users/vini/Secrets/file.txt", Size: 100},
		{Path: "/Users/vini/Downloads/file.txt", Size: 200},
	}
	r := newMockRegistry(&mockCleaner{category: "cat_a", entries: entries})
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	result, err := RunScan(t.Context(), r, nil, ScanOptions{Detailed: true, Config: cfg}, nil)
	if err != nil {
		t.Fatalf("RunScan() error: %v", err)
	}
	files := result.Categories[0].Files
	if len(files) != 2 {
		t.Fatalf("Tag must not remove entries, got %d want 2", len(files))
	}
	if !files[0].Protected {
		t.Error("expected the secrets file to be tagged protected")
	}
	if files[1].Protected {
		t.Error("expected the downloads file to remain unprotected")
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

// --- writeTable ---

func makeFileEntries(n int, sizeBase int64) []cleaner.FileEntry {
	entries := make([]cleaner.FileEntry, n)
	for i := range n {
		entries[i] = cleaner.FileEntry{
			Path: fmt.Sprintf("/tmp/file-%02d", i),
			Size: sizeBase + int64(i),
		}
	}
	return entries
}

func TestWriteTable_SortsBySizeDescending(t *testing.T) {
	result := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Name:       "Caches",
				TotalFiles: 3,
				Files: []cleaner.FileEntry{
					{Path: "/tmp/small", Size: 100},
					{Path: "/tmp/big", Size: 900},
					{Path: "/tmp/mid", Size: 500},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := writeTable(&buf, result, false); err != nil {
		t.Fatalf("writeTable() error: %v", err)
	}

	out := buf.String()
	bigIdx := strings.Index(out, "/tmp/big")
	midIdx := strings.Index(out, "/tmp/mid")
	smallIdx := strings.Index(out, "/tmp/small")
	if bigIdx == -1 || midIdx == -1 || smallIdx == -1 {
		t.Fatalf("missing entries in output: %s", out)
	}
	if bigIdx >= midIdx || midIdx >= smallIdx {
		t.Errorf("entries not sorted by size descending: %s", out)
	}
}

func TestWriteTable_CapsAtTenEntriesWithoutPrintAll(t *testing.T) {
	result := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Name:       "Caches",
				TotalFiles: 15,
				Files:      makeFileEntries(15, 1000),
			},
		},
	}

	var buf bytes.Buffer
	if err := writeTable(&buf, result, false); err != nil {
		t.Fatalf("writeTable() error: %v", err)
	}

	out := buf.String()
	shown := strings.Count(out, "/tmp/file-")
	if shown != 10 {
		t.Errorf("shown entries = %d, want 10", shown)
	}
	if !strings.Contains(out, "5 more omitted, use --print-all to list all") {
		t.Errorf("missing omitted-count hint: %s", out)
	}
}

func TestWriteTable_PrintAllListsEverything(t *testing.T) {
	result := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Name:       "Caches",
				TotalFiles: 15,
				Files:      makeFileEntries(15, 1000),
			},
		},
	}

	var buf bytes.Buffer
	if err := writeTable(&buf, result, true); err != nil {
		t.Fatalf("writeTable() error: %v", err)
	}

	out := buf.String()
	shown := strings.Count(out, "/tmp/file-")
	if shown != 15 {
		t.Errorf("shown entries = %d, want 15", shown)
	}
	if strings.Contains(out, "more omitted") {
		t.Errorf("should not omit anything with printAll: %s", out)
	}
}

func TestWriteTable_EmptyCategoryReportsNothingFound(t *testing.T) {
	result := ScanResult{
		Categories: []ScanCategoryResult{
			{Name: "Caches", TotalFiles: 0},
		},
	}

	var buf bytes.Buffer
	if err := writeTable(&buf, result, false); err != nil {
		t.Fatalf("writeTable() error: %v", err)
	}

	if !strings.Contains(buf.String(), "nothing found") {
		t.Errorf("expected 'nothing found', got: %s", buf.String())
	}
}

func TestWriteTable_CategoryErrorSurfacesCauseAndNextStep(t *testing.T) {
	result := ScanResult{
		HasErrors: true,
		Categories: []ScanCategoryResult{
			{Category: cleaner.CategoryDocker, Name: "Docker", Err: errors.New("docker not running"), ErrMsg: "docker not running"},
		},
	}

	var buf bytes.Buffer
	if err := writeTable(&buf, result, false); err != nil {
		t.Fatalf("writeTable() error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Docker") || !strings.Contains(out, "docker not running") {
		t.Errorf("missing category/cause in error output: %s", out)
	}
	if !strings.Contains(out, "tidymymac scan docker") {
		t.Errorf("missing next-step hint in error output: %s", out)
	}
}

func TestWriteTable_GroupsDockerByResourceKind(t *testing.T) {
	result := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Category:   cleaner.CategoryDocker,
				Name:       "Docker",
				TotalFiles: 3,
				Files: []cleaner.FileEntry{
					{Path: "docker://image/abc", Size: 100, ResourceKind: cleaner.DockerResourceKindImageDangling},
					{Path: "docker://container/def", Size: 200, ResourceKind: cleaner.DockerResourceKindContainerStopped},
					{Path: "docker://volume/ghi", Size: 300, ResourceKind: cleaner.DockerResourceKindVolumeOrphaned},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := writeTable(&buf, result, false); err != nil {
		t.Fatalf("writeTable() error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"Unreferenced images", "Stopped containers", "Unused volumes"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing docker group %q in output: %s", want, out)
		}
	}
	if strings.Contains(out, "Images tied to stopped containers") {
		t.Errorf("group with no entries should not render: %s", out)
	}
}

func TestWriteTable_JSONAndCSVUnaffectedByTableAddition(t *testing.T) {
	result := ScanResult{
		TotalFiles: 1,
		TotalSize:  1024,
		Categories: []ScanCategoryResult{
			{Name: "Caches", TotalFiles: 1, TotalSize: 1024, Files: []cleaner.FileEntry{{Path: "/tmp/a", Size: 1024}}},
		},
	}

	var jsonBuf, csvBuf bytes.Buffer
	if err := WriteOutput(&jsonBuf, result, "json", true, false); err != nil {
		t.Fatalf("WriteOutput(json) error: %v", err)
	}
	if err := WriteOutput(&csvBuf, result, "csv", true, false); err != nil {
		t.Fatalf("WriteOutput(csv) error: %v", err)
	}

	var directJSON, directCSV bytes.Buffer
	if err := writeJSON(&directJSON, result); err != nil {
		t.Fatalf("writeJSON() error: %v", err)
	}
	if err := writeCSV(&directCSV, result, true); err != nil {
		t.Fatalf("writeCSV() error: %v", err)
	}

	if jsonBuf.String() != directJSON.String() {
		t.Errorf("WriteOutput(json) diverged from writeJSON()")
	}
	if csvBuf.String() != directCSV.String() {
		t.Errorf("WriteOutput(csv) diverged from writeCSV()")
	}
}

func TestWriteOutput_PrintAllOnlyAffectsTable(t *testing.T) {
	result := ScanResult{
		Categories: []ScanCategoryResult{
			{Name: "Caches", TotalFiles: 15, Files: makeFileEntries(15, 1000)},
		},
	}

	var buf bytes.Buffer
	if err := WriteOutput(&buf, result, "table", true, true); err != nil {
		t.Fatalf("WriteOutput() error: %v", err)
	}
	if strings.Count(buf.String(), "/tmp/file-") != 15 {
		t.Errorf("printAll should list all entries via WriteOutput")
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

// TestWriteTable_EscapesControlCharactersInPaths: the table exists for human
// review, so a file name must not be able to forge rows, hide text, or drive
// the terminal. Ordinary Unicode names stay readable.
func TestWriteTable_EscapesControlCharactersInPaths(t *testing.T) {
	result := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Category:   cleaner.CategoryTemp,
				Name:       "Temp Files",
				TotalFiles: 3,
				TotalSize:  300,
				Files: []cleaner.FileEntry{
					{Path: "/tmp/a\n   9.0 GB  /etc/forged-row", Size: 100},
					{Path: "/tmp/\x1b[2Jcleared", Size: 90},
					{Path: "/tmp/relatório – ção 📦", Size: 80},
				},
			},
			{
				Category:   cleaner.CategoryDocker,
				Name:       "Docker",
				TotalFiles: 1,
				TotalSize:  50,
				Files: []cleaner.FileEntry{
					{Path: "docker://image/evil\rtag", Size: 50, ResourceKind: cleaner.DockerResourceKindImageDangling},
				},
			},
			{
				Category: cleaner.CategoryLogs,
				Name:     "System Logs",
				Err:      errors.New("x"),
				ErrMsg:   "open /var/log/\x1b]0;pwned\x07: permission denied",
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteOutput(&buf, result, "table", true, false); err != nil {
		t.Fatalf("WriteOutput() error: %v", err)
	}
	out := buf.String()

	for _, raw := range []string{"\x1b", "\r", "\x07"} {
		if strings.Contains(out, raw) {
			t.Fatalf("table output contains raw control character %q:\n%s", raw, out)
		}
	}
	for _, want := range []string{
		`/tmp/a\n   9.0 GB  /etc/forged-row`,
		`/tmp/\e[2Jcleared`,
		"/tmp/relatório – ção 📦",
		`docker://image/evil\rtag`,
		`open /var/log/\e]0;pwned\x07: permission denied`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("table output missing %q:\n%s", want, out)
		}
	}
	// The forged row must stay inside the first entry's own line, right
	// after its real size, rather than becoming a line of its own.
	if !strings.Contains(out, `100 B  /tmp/a\n   9.0 GB  /etc/forged-row`) {
		t.Fatalf("forged row escaped onto its own line:\n%s", out)
	}
}

// TestResolveCleanersDedupesRepeatedCategories covers a pre-existing quirk:
// "tidymymac clean docker docker --execute" resolved to two Docker cleaners
// and ran the category twice, scanning and deleting the same domain in two
// passes. The sudo half of clean already deduped its own selection; this makes
// the ordinary path agree.
func TestResolveCleanersDedupesRepeatedCategories(t *testing.T) {
	registry := cleaner.NewRegistry()
	registry.Register(cleaner.NewCachesCleaner())
	registry.Register(cleaner.NewDownloadsCleaner())

	selected := []string{
		string(cleaner.CategoryApplicationCaches),
		string(cleaner.CategoryDownloads),
		string(cleaner.CategoryApplicationCaches),
	}

	cleaners, err := resolveCleaners(registry, selected, nil)
	if err != nil {
		t.Fatalf("resolveCleaners: %v", err)
	}

	if len(cleaners) != 2 {
		t.Fatalf("got %d cleaners, want 2", len(cleaners))
	}
	// First-seen order is preserved, so the output still reads the way the
	// user wrote the command.
	if cleaners[0].Category() != cleaner.CategoryApplicationCaches ||
		cleaners[1].Category() != cleaner.CategoryDownloads {
		t.Fatalf("order = %q, %q; want first-seen order preserved",
			cleaners[0].Category(), cleaners[1].Category())
	}
}
