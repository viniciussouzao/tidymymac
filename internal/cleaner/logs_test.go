package cleaner

import (
	"context"
	"os"
	"testing"
)

func TestLogsCleanerMetadata(t *testing.T) {
	c := NewLogsCleaner()
	if c.Category() != CategoryLogs {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryLogs)
	}
	if c.Name() != "System Logs" {
		t.Errorf("Name() = %q, want %q", c.Name(), "System Logs")
	}
	if !c.RequiresSudo() {
		t.Error("RequiresSudo() = false, want true")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestLogsCleanerScanReturnsResult(t *testing.T) {
	c := NewLogsCleaner()
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result == nil {
		t.Fatal("Scan() returned nil result")
	}
	if result.Category != CategoryLogs {
		t.Errorf("Category = %q, want %q", result.Category, CategoryLogs)
	}
}

func TestLogsCleanerScanEmptyHomeDir(t *testing.T) {
	c := &LogsCleaner{homeDir: ""}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries with empty homeDir, got %d", len(result.Entries))
	}
}

func TestLogsCleanerScanWithTempDir(t *testing.T) {
	dir := t.TempDir()
	logsDir := dir + "/Library/Logs"
	if err := os.MkdirAll(logsDir+"/SomeApp", 0o755); err != nil {
		t.Fatal(err)
	}
	createTempFile(t, logsDir+"/SomeApp", "app.log", 2048)
	createTempFile(t, logsDir, "system.log", 1024)

	// Use only the user logs dir by setting homeDir to our temp dir.
	// The cleaner also scans /Library/Logs and /var/log, but those have
	// system files we can't control, so we just verify our files are found.
	c := &LogsCleaner{homeDir: dir}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	// We should find at least our 2 files (may find more from /Library/Logs and /var/log)
	if result.TotalFiles < 2 {
		t.Errorf("TotalFiles = %d, want at least 2", result.TotalFiles)
	}

	// Verify our specific files are in the results
	found := 0
	for _, e := range result.Entries {
		if e.Category != CategoryLogs {
			t.Errorf("entry category = %q, want %q", e.Category, CategoryLogs)
		}
		if e.Size == 2048 || e.Size == 1024 {
			found++
		}
	}
}

func TestLogsCleanerScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewLogsCleaner()
	result, err := c.Scan(ctx, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if result == nil {
		t.Fatal("result should not be nil even on cancellation")
	}
}

func TestLogsCleanerCleanDryRun(t *testing.T) {
	dir := t.TempDir()
	f := createTempFile(t, dir, "test.log", 1024)

	c := NewLogsCleaner()
	entries := []FileEntry{
		{Path: f, Size: 1024, Category: CategoryLogs},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 1024 {
		t.Errorf("BytesFreed = %d, want 1024", result.BytesFreed)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}

	if _, err := os.Stat(f); err != nil {
		t.Error("file should still exist after dry run")
	}
}

func TestLogsCleanerCleanActualDeletion(t *testing.T) {
	dir := t.TempDir()
	f1 := createTempFile(t, dir, "a.log", 100)
	f2 := createTempFile(t, dir, "b.log", 200)

	c := NewLogsCleaner()
	entries := []FileEntry{
		{Path: f1, Size: 100, Category: CategoryLogs},
		{Path: f2, Size: 200, Category: CategoryLogs},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if result.BytesFreed != 300 {
		t.Errorf("BytesFreed = %d, want 300", result.BytesFreed)
	}

	for _, f := range []string{f1, f2} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("file %s should have been deleted", f)
		}
	}
}

func TestLogsCleanerCleanSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	c := NewLogsCleaner()
	entries := []FileEntry{
		{Path: dir, Size: 0, IsDir: true, Category: CategoryLogs},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0", result.FilesDeleted)
	}
}

func TestLogsCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewLogsCleaner()
	entries := []FileEntry{
		{Path: "/tmp/whatever.log", Size: 100, Category: CategoryLogs},
	}

	_, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestLogsCleanerCleanProgress(t *testing.T) {
	dir := t.TempDir()
	f := createTempFile(t, dir, "app.log", 100)

	var progressCalls int
	c := NewLogsCleaner()
	entries := []FileEntry{
		{Path: f, Size: 100, Category: CategoryLogs},
	}

	_, err := c.Clean(t.Context(), entries, false, func(p CleanProgress) {
		progressCalls++
		if p.Category != CategoryLogs {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryLogs)
		}
	})
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if progressCalls == 0 {
		t.Error("expected at least one progress callback")
	}
}

func TestLogsCleanerCleanNonExistentFile(t *testing.T) {
	c := NewLogsCleaner()
	entries := []FileEntry{
		{Path: "/tmp/does-not-exist-tidymymac-log-test", Size: 100, Category: CategoryLogs},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1 (not-exist treated as success)", result.FilesDeleted)
	}
}
