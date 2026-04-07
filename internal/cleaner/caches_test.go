package cleaner

import (
	"context"
	"os"
	"testing"
)

func TestCachesCleanerMetadata(t *testing.T) {
	c := NewCachesCleaner()
	if c.Category() != CategoryApplicationCaches {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryApplicationCaches)
	}
	if c.Name() != "App Caches" {
		t.Errorf("Name() = %q, want %q", c.Name(), "App Caches")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestCachesCleanerScanReturnsResult(t *testing.T) {
	c := NewCachesCleaner()
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result == nil {
		t.Fatal("Scan() returned nil result")
	}
	if result.Category != CategoryApplicationCaches {
		t.Errorf("Category = %q, want %q", result.Category, CategoryApplicationCaches)
	}
}

func TestCachesCleanerScanEmptyHomeDir(t *testing.T) {
	c := &CachesCleaner{homeDir: ""}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result == nil {
		t.Fatal("Scan() returned nil result")
	}
	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries with empty homeDir, got %d", len(result.Entries))
	}
}

func TestCachesCleanerScanWithTempDir(t *testing.T) {
	dir := t.TempDir()
	// Simulate ~/Library/Caches structure
	cachesDir := dir + "/Library/Caches"
	if err := os.MkdirAll(cachesDir+"/SomeApp", 0o755); err != nil {
		t.Fatal(err)
	}
	createTempFile(t, cachesDir+"/SomeApp", "cache.db", 1024)
	createTempFile(t, cachesDir, "global.cache", 512)

	c := &CachesCleaner{homeDir: dir}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if result.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d, want 2", result.TotalFiles)
	}
	if result.TotalSize != 1536 {
		t.Errorf("TotalSize = %d, want 1536", result.TotalSize)
	}
	for _, e := range result.Entries {
		if e.Category != CategoryApplicationCaches {
			t.Errorf("entry category = %q, want %q", e.Category, CategoryApplicationCaches)
		}
	}
}

func TestCachesCleanerScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewCachesCleaner()
	result, err := c.Scan(ctx, nil)
	// CachesCleaner doesn't return ctx error from Scan, it just stops walking
	if err != nil {
		t.Fatalf("Scan() unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestCachesCleanerCleanDryRun(t *testing.T) {
	dir := t.TempDir()
	f := createTempFile(t, dir, "cache.bin", 256)

	c := NewCachesCleaner()
	entries := []FileEntry{
		{Path: f, Size: 256, Category: CategoryApplicationCaches},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}

	if _, err := os.Stat(f); err != nil {
		t.Error("file should still exist after dry run")
	}
}

func TestCachesCleanerCleanActualDeletion(t *testing.T) {
	dir := t.TempDir()
	f := createTempFile(t, dir, "cache.bin", 256)

	c := NewCachesCleaner()
	entries := []FileEntry{
		{Path: f, Size: 256, Category: CategoryApplicationCaches},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 256 {
		t.Errorf("BytesFreed = %d, want 256", result.BytesFreed)
	}

	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestCachesCleanerCleanSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	c := NewCachesCleaner()
	entries := []FileEntry{
		{Path: dir, Size: 0, IsDir: true, Category: CategoryApplicationCaches},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0", result.FilesDeleted)
	}
}

func TestCachesCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewCachesCleaner()
	entries := []FileEntry{
		{Path: "/tmp/whatever", Size: 100, Category: CategoryApplicationCaches},
	}

	_, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestCachesCleanerCleanProgress(t *testing.T) {
	dir := t.TempDir()
	f := createTempFile(t, dir, "cache.dat", 100)

	var progressCalls int
	c := NewCachesCleaner()
	entries := []FileEntry{
		{Path: f, Size: 100, Category: CategoryApplicationCaches},
	}

	_, err := c.Clean(t.Context(), entries, false, func(p CleanProgress) {
		progressCalls++
		if p.Category != CategoryApplicationCaches {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryApplicationCaches)
		}
	})
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if progressCalls == 0 {
		t.Error("expected at least one progress callback")
	}
}

func TestCachesCleanerScanProgress(t *testing.T) {
	dir := t.TempDir()
	cachesDir := dir + "/Library/Caches"
	if err := os.MkdirAll(cachesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create enough files to trigger progress (every 100 files)
	for i := range 101 {
		createTempFile(t, cachesDir, "f_"+itoa(i)+".tmp", 1)
	}

	var progressCalls int
	c := &CachesCleaner{homeDir: dir}
	_, err := c.Scan(t.Context(), func(p ScanProgress) {
		progressCalls++
	})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	// At least 1 progress call during scan + 1 final call
	if progressCalls < 1 {
		t.Errorf("expected progress calls, got %d", progressCalls)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
