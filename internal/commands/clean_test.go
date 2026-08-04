package commands

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
)

type mockCleanRunner struct {
	category           cleaner.Category
	name               string
	entries            []cleaner.FileEntry
	scanErr            error
	cleanErr           error
	cleanResult        *cleaner.CleanResult
	deletesWholeDomain bool

	// cleanedEntries records whatever was actually passed to Clean, so tests
	// can assert protected entries never reach it.
	cleanedEntries []cleaner.FileEntry
	// cleanCalled records whether Clean was invoked at all, so tests can
	// assert a whole-domain cleaner was skipped entirely rather than called
	// with a partial entry list.
	cleanCalled bool
}

func (m *mockCleanRunner) Category() cleaner.Category { return m.category }
func (m *mockCleanRunner) Name() string               { return m.name }
func (m *mockCleanRunner) Description() string        { return "mock cleaner" }
func (m *mockCleanRunner) RequiresSudo() bool         { return false }
func (m *mockCleanRunner) DeletesWholeDomain() bool   { return m.deletesWholeDomain }

func (m *mockCleanRunner) Scan(_ context.Context, _ func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	if m.scanErr != nil {
		return &cleaner.ScanResult{Category: m.category}, m.scanErr
	}

	total := int64(0)
	for _, entry := range m.entries {
		total += entry.Size
	}

	return &cleaner.ScanResult{
		Category:   m.category,
		Entries:    m.entries,
		TotalSize:  total,
		TotalFiles: len(m.entries),
	}, nil
}

func (m *mockCleanRunner) Clean(_ context.Context, entries []cleaner.FileEntry, dryRun bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	m.cleanCalled = true
	m.cleanedEntries = entries
	if m.cleanErr != nil {
		return nil, m.cleanErr
	}

	if m.cleanResult != nil {
		return m.cleanResult, nil
	}

	var bytesFreed int64
	for _, entry := range entries {
		bytesFreed += entry.Size
	}

	return &cleaner.CleanResult{
		Category:     m.category,
		FilesDeleted: len(entries),
		BytesFreed:   bytesFreed,
		DryRun:       dryRun,
	}, nil
}

func newMockCleanRegistry(mocks ...*mockCleanRunner) *cleaner.Registry {
	r := cleaner.NewRegistry()
	for _, c := range mocks {
		r.Register(c)
	}
	return r
}

func TestRunClean_PreservesRegistryOrder(t *testing.T) {
	r := newMockCleanRegistry(
		&mockCleanRunner{category: "first"},
		&mockCleanRunner{category: "second"},
		&mockCleanRunner{category: "third"},
	)

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{}, nil)
	if err != nil {
		t.Fatalf("RunClean() error: %v", err)
	}

	want := []cleaner.Category{"first", "second", "third"}
	if len(result.Categories) != len(want) {
		t.Fatalf("got %d categories, want %d", len(result.Categories), len(want))
	}

	for i, category := range want {
		if result.Categories[i].Category != category {
			t.Errorf("Categories[%d] = %q, want %q", i, result.Categories[i].Category, category)
		}
	}
}

func TestRunClean_AggregatesDeletedTotals(t *testing.T) {
	r := newMockCleanRegistry(
		&mockCleanRunner{
			category: "cat_a",
			entries:  []cleaner.FileEntry{{Path: "/tmp/a", Size: 1024}, {Path: "/tmp/b", Size: 512}},
		},
		&mockCleanRunner{
			category: "cat_b",
			entries:  []cleaner.FileEntry{{Path: "/tmp/c", Size: 2048}},
		},
	)

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{}, nil)
	if err != nil {
		t.Fatalf("RunClean() error: %v", err)
	}

	if result.TotalFiles != 3 {
		t.Errorf("TotalFiles = %d, want 3", result.TotalFiles)
	}
	if result.TotalSize != 3584 {
		t.Errorf("TotalSize = %d, want 3584", result.TotalSize)
	}
	if result.TotalSizeHuman != "3.5 KB" {
		t.Errorf("TotalSizeHuman = %q, want %q", result.TotalSizeHuman, "3.5 KB")
	}
}

func TestRunClean_HasErrorsWhenScanFails(t *testing.T) {
	r := newMockCleanRegistry(
		&mockCleanRunner{category: "ok_cat", entries: []cleaner.FileEntry{{Path: "/tmp/a", Size: 100}}},
		&mockCleanRunner{category: "fail_cat", scanErr: errors.New("scan failed")},
	)

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{}, nil)
	if err != nil {
		t.Fatalf("RunClean() should not return a top-level error, got: %v", err)
	}
	if !result.HasErrors {
		t.Fatal("HasErrors = false, want true")
	}

	var failed *CleanCategoryResult
	for i := range result.Categories {
		if result.Categories[i].Category == "fail_cat" {
			failed = &result.Categories[i]
			break
		}
	}
	if failed == nil {
		t.Fatal("failed category not present in results")
	}
	if failed.ErrMsg != "scan failed" {
		t.Errorf("ErrMsg = %q, want %q", failed.ErrMsg, "scan failed")
	}
}

func TestRunClean_HasErrorsWhenCleanFails(t *testing.T) {
	r := newMockCleanRegistry(
		&mockCleanRunner{category: "cat_a", cleanErr: errors.New("clean failed")},
	)

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{}, nil)
	if err != nil {
		t.Fatalf("RunClean() should not return a top-level error, got: %v", err)
	}
	if !result.HasErrors {
		t.Fatal("HasErrors = false, want true")
	}
	if result.Categories[0].ErrMsg != "clean failed" {
		t.Errorf("ErrMsg = %q, want %q", result.Categories[0].ErrMsg, "clean failed")
	}
}

func TestRunClean_DetailedPopulatesFiles(t *testing.T) {
	entries := []cleaner.FileEntry{
		{Path: "/tmp/a", Size: 100, ModTime: time.Now()},
		{Path: "/tmp/b", Size: 200, ModTime: time.Now()},
	}
	r := newMockCleanRegistry(&mockCleanRunner{category: "cat_a", entries: entries})

	detailed, err := RunClean(t.Context(), r, nil, CleanerOptions{Detailed: true}, nil)
	if err != nil {
		t.Fatalf("RunClean(detailed) error: %v", err)
	}
	if len(detailed.Categories[0].Files) != 2 {
		t.Errorf("Files count = %d, want 2", len(detailed.Categories[0].Files))
	}

	summary, err := RunClean(t.Context(), r, nil, CleanerOptions{Detailed: false}, nil)
	if err != nil {
		t.Fatalf("RunClean(summary) error: %v", err)
	}
	if len(summary.Categories[0].Files) != 0 {
		t.Errorf("Files should be empty without Detailed, got %d entries", len(summary.Categories[0].Files))
	}
}

func TestRunClean_StripsProtectedEntriesBeforeClean(t *testing.T) {
	mock := &mockCleanRunner{
		category: "cat_a",
		entries: []cleaner.FileEntry{
			{Path: "/Users/vini/Secrets/file.txt", Size: 100},
			{Path: "/Users/vini/Downloads/file.txt", Size: 200},
		},
	}
	r := newMockCleanRegistry(mock)
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{Config: cfg}, nil)
	if err != nil {
		t.Fatalf("RunClean() error: %v", err)
	}

	if len(mock.cleanedEntries) != 1 || mock.cleanedEntries[0].Path != "/Users/vini/Downloads/file.txt" {
		t.Fatalf("protected entry reached Clean(): %+v", mock.cleanedEntries)
	}
	if result.TotalFiles != 1 || result.TotalSize != 200 {
		t.Errorf("TotalFiles/TotalSize = %d/%d, want 1/200", result.TotalFiles, result.TotalSize)
	}
}

func TestRunClean_DryRunAlsoStripsProtectedEntries(t *testing.T) {
	mock := &mockCleanRunner{
		category: "cat_a",
		entries: []cleaner.FileEntry{
			{Path: "/Users/vini/Secrets/file.txt", Size: 100},
		},
	}
	r := newMockCleanRegistry(mock)
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	_, err = RunClean(t.Context(), r, nil, CleanerOptions{Config: cfg, DryRun: true}, nil)
	if err != nil {
		t.Fatalf("RunClean() error: %v", err)
	}

	if len(mock.cleanedEntries) != 0 {
		t.Errorf("dry-run must also strip protected entries before Clean(), got %+v", mock.cleanedEntries)
	}
}

func TestRunClean_ReportsCorrectTotalsExcludingProtected(t *testing.T) {
	r := newMockCleanRegistry(&mockCleanRunner{
		category: "cat_a",
		entries: []cleaner.FileEntry{
			{Path: "/Users/vini/Secrets/a", Size: 100},
			{Path: "/Users/vini/Secrets/b", Size: 300},
			{Path: "/Users/vini/Downloads/c", Size: 50},
		},
	})
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{Detailed: true, Config: cfg}, nil)
	if err != nil {
		t.Fatalf("RunClean() error: %v", err)
	}

	if result.TotalFiles != 1 || result.TotalSize != 50 {
		t.Errorf("TotalFiles/TotalSize = %d/%d, want 1/50", result.TotalFiles, result.TotalSize)
	}
	if len(result.Categories[0].Files) != 1 {
		t.Errorf("detailed Files should also exclude protected entries, got %+v", result.Categories[0].Files)
	}
}

func TestRunClean_SkipsWholeDomainCleanerWhenAnyEntryProtected(t *testing.T) {
	mock := &mockCleanRunner{
		category:           "cat_a",
		deletesWholeDomain: true,
		entries: []cleaner.FileEntry{
			{Path: "/Users/vini/Secrets/file.txt", Size: 100},
			{Path: "/Users/vini/Downloads/file.txt", Size: 200},
		},
	}
	r := newMockCleanRegistry(mock)
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{Config: cfg}, nil)
	if err != nil {
		t.Fatalf("RunClean() error: %v", err)
	}

	if mock.cleanCalled {
		t.Fatal("a whole-domain cleaner must never be invoked when any of its entries are protected")
	}
	if !result.HasErrors {
		t.Fatal("HasErrors = false, want true (the skip must be visible to the caller)")
	}
	if result.Categories[0].ErrMsg == "" {
		t.Error("expected a message explaining why the category was skipped")
	}
}

func TestRunClean_WholeDomainCleanerRunsWhenNothingProtected(t *testing.T) {
	mock := &mockCleanRunner{
		category:           "cat_a",
		deletesWholeDomain: true,
		entries: []cleaner.FileEntry{
			{Path: "/Users/vini/Downloads/file.txt", Size: 200},
		},
	}
	r := newMockCleanRegistry(mock)
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{Config: cfg}, nil)
	if err != nil {
		t.Fatalf("RunClean() error: %v", err)
	}

	if !mock.cleanCalled {
		t.Fatal("a whole-domain cleaner with nothing protected in its category must still run")
	}
	if result.HasErrors {
		t.Errorf("HasErrors = true, want false: %+v", result.Categories[0])
	}
}

func TestRunClean_WholeDomainSkipAppliesInDryRunToo(t *testing.T) {
	mock := &mockCleanRunner{
		category:           "cat_a",
		deletesWholeDomain: true,
		entries: []cleaner.FileEntry{
			{Path: "/Users/vini/Secrets/file.txt", Size: 100},
		},
	}
	r := newMockCleanRegistry(mock)
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	_, err = RunClean(t.Context(), r, nil, CleanerOptions{Config: cfg, DryRun: true}, nil)
	if err != nil {
		t.Fatalf("RunClean() error: %v", err)
	}
	if mock.cleanCalled {
		t.Error("dry-run must also skip a whole-domain cleaner when something is protected, so the preview is honest")
	}
}

func TestRunClean_FailedCategoryExcludedFromTotals(t *testing.T) {
	r := newMockCleanRegistry(
		&mockCleanRunner{category: "ok_cat", entries: []cleaner.FileEntry{{Path: "/tmp/a", Size: 500}}},
		&mockCleanRunner{category: "fail_cat", cleanErr: errors.New("boom")},
	)

	result, err := RunClean(t.Context(), r, nil, CleanerOptions{}, nil)
	if err != nil {
		t.Fatalf("RunClean() should not return a top-level error, got: %v", err)
	}
	if result.TotalSize != 500 {
		t.Errorf("TotalSize = %d, want 500", result.TotalSize)
	}
	if result.TotalFiles != 1 {
		t.Errorf("TotalFiles = %d, want 1", result.TotalFiles)
	}
}
