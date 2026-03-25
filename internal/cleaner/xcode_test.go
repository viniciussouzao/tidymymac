package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestXcodeCleanerMetadata(t *testing.T) {
	c := NewXcodeCleaner()

	if c.Category() != CategoryXcode {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryXcode)
	}
	if c.Name() != "Xcode" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Xcode")
	}
	if c.Description() != "DerivedData, archives, simulators" {
		t.Errorf("Description() = %q, want %q", c.Description(), "DerivedData, archives, simulators")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
}

func TestXcodeCleanerScanEmptyHomeDir(t *testing.T) {
	c := &XcodeCleaner{homeDir: ""}

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result == nil {
		t.Fatal("Scan() returned nil result")
	}
	if len(result.Entries) != 0 {
		t.Errorf("len(Entries) = %d, want 0", len(result.Entries))
	}
}

func TestXcodeCleanerScanContextCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := &XcodeCleaner{homeDir: t.TempDir()}

	_, err := c.Scan(ctx, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestXcodeCleanerScanFindsFiles(t *testing.T) {
	dir := t.TempDir()
	derivedDataDir := filepath.Join(dir, "Library", "Developer", "Xcode", "DerivedData")
	archivesDir := filepath.Join(dir, "Library", "Developer", "Xcode", "Archives")

	if err := os.MkdirAll(derivedDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archivesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createTempFile(t, derivedDataDir, "build-a", 1024)
	createTempFile(t, derivedDataDir, "build-b", 2048)
	createTempFile(t, archivesDir, "MyApp.xcarchive", 4096)

	c := &XcodeCleaner{homeDir: dir}

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if result.TotalFiles != 3 {
		t.Errorf("TotalFiles = %d, want 3", result.TotalFiles)
	}
	if result.TotalSize != int64(1024+2048+4096) {
		t.Errorf("TotalSize = %d, want %d", result.TotalSize, int64(1024+2048+4096))
	}
	if len(result.Entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3", len(result.Entries))
	}

	for _, entry := range result.Entries {
		if entry.IsDir {
			t.Errorf("entry %q IsDir = true, want false", entry.Path)
		}
		if entry.Category != CategoryXcode {
			t.Errorf("entry %q Category = %q, want %q", entry.Path, entry.Category, CategoryXcode)
		}
	}
}

func TestXcodeCleanerScanSkipsNonExistentPaths(t *testing.T) {
	c := &XcodeCleaner{homeDir: t.TempDir()}

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("len(Entries) = %d, want 0 (no Xcode dirs created)", len(result.Entries))
	}
}

func TestXcodeCleanerScanScansAllPaths(t *testing.T) {
	dir := t.TempDir()

	paths := []string{
		filepath.Join(dir, "Library", "Developer", "Xcode", "DerivedData"),
		filepath.Join(dir, "Library", "Developer", "Xcode", "Archives"),
		filepath.Join(dir, "Library", "Developer", "Xcode", "iOS DeviceSupport"),
		filepath.Join(dir, "Library", "Developer", "CoreSimulator", "Caches"),
	}

	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		createTempFile(t, p, "file", 100)
	}

	c := &XcodeCleaner{homeDir: dir}

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result.TotalFiles != 4 {
		t.Errorf("TotalFiles = %d, want 4 (one file per path)", result.TotalFiles)
	}
}

func TestXcodeCleanerCleanDryRun(t *testing.T) {
	dir := t.TempDir()
	file := createTempFile(t, dir, "DerivedData-cache", 512)

	c := NewXcodeCleaner()
	entries := []FileEntry{
		{Path: file, Size: 512, Category: CategoryXcode},
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
	if _, err := os.Stat(file); err != nil {
		t.Errorf("file should still exist after dry run: %v", err)
	}
}

func TestXcodeCleanerCleanDeletesFiles(t *testing.T) {
	dir := t.TempDir()
	file := createTempFile(t, dir, "archive.xcarchive", 2048)

	c := NewXcodeCleaner()
	entries := []FileEntry{
		{Path: file, Size: 2048, Category: CategoryXcode},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 2048 {
		t.Errorf("BytesFreed = %d, want 2048", result.BytesFreed)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestXcodeCleanerCleanSkipsDirEntries(t *testing.T) {
	dir := t.TempDir()

	c := NewXcodeCleaner()
	entries := []FileEntry{
		{Path: dir, Size: 1024, IsDir: true, Category: CategoryXcode},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0 (dir entries must be skipped)", result.FilesDeleted)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("directory should still exist: %v", err)
	}
}

func TestXcodeCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewXcodeCleaner()
	entries := []FileEntry{
		{Path: "/tmp/xcode-derived-data", Size: 100, Category: CategoryXcode},
	}

	_, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestXcodeCleanerCleanProgress(t *testing.T) {
	dir := t.TempDir()
	f1 := createTempFile(t, dir, "file-a", 128)
	f2 := createTempFile(t, dir, "file-b", 256)

	c := NewXcodeCleaner()
	entries := []FileEntry{
		{Path: f1, Size: 128, Category: CategoryXcode},
		{Path: f2, Size: 256, Category: CategoryXcode},
	}

	var progressCalls int
	result, err := c.Clean(t.Context(), entries, true, func(p CleanProgress) {
		progressCalls++
		if p.Category != CategoryXcode {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryXcode)
		}
		if p.FilesTotal != 2 {
			t.Errorf("progress FilesTotal = %d, want 2", p.FilesTotal)
		}
	})
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	// i=0: 0%100==0 → called; i=1: 1==len(entries)-1 → called
	if progressCalls != 2 {
		t.Errorf("progressCalls = %d, want 2", progressCalls)
	}
}
