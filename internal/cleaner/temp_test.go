package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTempCleanerMetadata(t *testing.T) {
	c := NewTempCleaner()
	if c.Category() != CategoryTemp {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryTemp)
	}
	if c.Name() != "Temp Files" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Temp Files")
	}
	if !c.RequiresSudo() {
		t.Error("RequiresSudo() = false, want true")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestTempCleanerScanReturnsResult(t *testing.T) {
	c := NewTempCleaner()
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result == nil {
		t.Fatal("Scan() returned nil result")
	}
	if result.Category != CategoryTemp {
		t.Errorf("Category = %q, want %q", result.Category, CategoryTemp)
	}
	if result.Duration == 0 {
		t.Error("Duration should be > 0")
	}
}

func TestTempCleanerScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewTempCleaner()
	result, err := c.Scan(ctx, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if result == nil {
		t.Fatal("result should not be nil even on cancellation")
	}
}

func TestTempCleanerCleanDryRun(t *testing.T) {
	dir := t.TempDir()
	f := createTempFile(t, dir, "test.tmp", 512)

	c := NewTempCleaner()
	entries := []FileEntry{
		{Path: f, Size: 512, Category: CategoryTemp},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 512 {
		t.Errorf("BytesFreed = %d, want 512", result.BytesFreed)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}

	// File should still exist
	if _, err := os.Stat(f); err != nil {
		t.Error("file should still exist after dry run")
	}
}

func TestTempCleanerCleanActualDeletion(t *testing.T) {
	dir := t.TempDir()
	f1 := createTempFile(t, dir, "a.tmp", 100)
	f2 := createTempFile(t, dir, "b.tmp", 200)

	c := NewTempCleaner()
	entries := []FileEntry{
		{Path: f1, Size: 100, Category: CategoryTemp},
		{Path: f2, Size: 200, Category: CategoryTemp},
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

func TestTempCleanerCleanSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	c := NewTempCleaner()
	entries := []FileEntry{
		{Path: dir, Size: 0, IsDir: true, Category: CategoryTemp},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0 (dirs skipped)", result.FilesDeleted)
	}
}

func TestTempCleanerCleanNonExistentFile(t *testing.T) {
	c := NewTempCleaner()
	entries := []FileEntry{
		{Path: "/tmp/does-not-exist-tidymymac-test", Size: 100, Category: CategoryTemp},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	// os.Remove returns IsNotExist which is silently handled
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1 (not-exist treated as success)", result.FilesDeleted)
	}
}

func TestTempCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewTempCleaner()
	entries := []FileEntry{
		{Path: "/tmp/whatever", Size: 100, Category: CategoryTemp},
	}

	_, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestTempCleanerCleanProgress(t *testing.T) {
	dir := t.TempDir()
	var files []FileEntry
	for i := range 3 {
		f := createTempFile(t, dir, filepath.Base(filepath.Join(dir, string(rune('a'+i))+".tmp")), 100)
		files = append(files, FileEntry{Path: f, Size: 100, Category: CategoryTemp})
	}

	var progressCalls int
	c := NewTempCleaner()
	_, err := c.Clean(t.Context(), files, false, func(p CleanProgress) {
		progressCalls++
		if p.Category != CategoryTemp {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryTemp)
		}
		if p.FilesTotal != 3 {
			t.Errorf("progress FilesTotal = %d, want 3", p.FilesTotal)
		}
	})
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if progressCalls == 0 {
		t.Error("expected at least one progress callback")
	}
}

// createTempFile creates a file with the given size and returns its path.
func createTempFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data := make([]byte, size)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	return path
}

// createSparseFile creates a sparse file reporting the given logical size via stat without allocating disk blocks.
func createSparseFile(t *testing.T, dir, name string, size int64) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	defer f.Close()

	if err := f.Truncate(size); err != nil {
		t.Fatalf("failed to truncate file: %v", err)
	}

	return path
}

// createAllocatedFile creates a file with fully written content so du reports the actual size.
func createAllocatedFile(t *testing.T, dir, name string, size int64) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	defer f.Close()

	chunk := make([]byte, 1024*1024)
	remaining := size
	for remaining > 0 {
		writeSize := int64(len(chunk))
		if remaining < writeSize {
			writeSize = remaining
		}

		if _, err := f.Write(chunk[:writeSize]); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}
		remaining -= writeSize
	}

	return path
}
