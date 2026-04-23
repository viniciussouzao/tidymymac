package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadsCleanerMetadata(t *testing.T) {
	c := NewDownloadsCleaner()
	if c.Category() != CategoryDownloads {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryDownloads)
	}
	if c.Name() != "Downloads" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Downloads")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestDownloadsCleanerScanEmptyHomeDir(t *testing.T) {
	c := &DownloadsCleaner{homeDir: ""}
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

func TestDownloadsCleanerScanNoDownloadsDir(t *testing.T) {
	c := &DownloadsCleaner{homeDir: t.TempDir()}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries when Downloads is absent, got %d", len(result.Entries))
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %v", result.Errors)
	}
}

func TestDownloadsCleanerScanIncludesInstallersAndLargeTopLevelItems(t *testing.T) {
	home := t.TempDir()
	downloadsDir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dmgPath := createSparseFile(t, downloadsDir, "Installer.DMG", 1024)
	pkgPath := createSparseFile(t, downloadsDir, "Setup.pkg", 2048)
	largeFilePath := createSparseFile(t, downloadsDir, "movie.mov", downloadsLargeItemThreshold+1)
	createSparseFile(t, downloadsDir, "notes.txt", 4096)

	largeDirPath := filepath.Join(downloadsDir, "Archive")
	if err := os.MkdirAll(largeDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	createAllocatedFile(t, largeDirPath, "payload.bin", downloadsLargeItemThreshold+1)

	nestedDir := filepath.Join(downloadsDir, "Nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createSparseFile(t, filepath.Join(nestedDir, "subdir"), "ignored.pkg", 4096)

	c := &DownloadsCleaner{homeDir: home}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if result.TotalFiles != 4 {
		t.Fatalf("TotalFiles = %d, want 4", result.TotalFiles)
	}

	got := map[string]FileEntry{}
	for _, entry := range result.Entries {
		got[filepath.Base(entry.Path)] = entry
		if entry.Category != CategoryDownloads {
			t.Errorf("entry %q category = %q, want %q", entry.Path, entry.Category, CategoryDownloads)
		}
	}

	if _, ok := got[filepath.Base(dmgPath)]; !ok {
		t.Error("expected dmg installer to be included")
	}
	if _, ok := got[filepath.Base(pkgPath)]; !ok {
		t.Error("expected pkg installer to be included")
	}
	if _, ok := got[filepath.Base(largeFilePath)]; !ok {
		t.Error("expected large file to be included")
	}
	largeDirEntry, ok := got[filepath.Base(largeDirPath)]
	if !ok {
		t.Fatal("expected large directory to be included")
	}
	if !largeDirEntry.IsDir {
		t.Error("large directory entry IsDir = false, want true")
	}
	if _, ok := got["notes.txt"]; ok {
		t.Error("small non-installer file should not be included")
	}
	if _, ok := got["Nested"]; ok {
		t.Error("directory below threshold should not be included")
	}
}

func TestDownloadsCleanerScanProgress(t *testing.T) {
	home := t.TempDir()
	downloadsDir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createSparseFile(t, downloadsDir, "Installer.dmg", 1024)

	var calls int
	c := &DownloadsCleaner{homeDir: home}
	_, err := c.Scan(t.Context(), func(p ScanProgress) {
		calls++
		if p.Category != CategoryDownloads {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryDownloads)
		}
	})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if calls == 0 {
		t.Error("expected at least one progress callback")
	}
}

func TestDownloadsCleanerScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewDownloadsCleaner()
	_, err := c.Scan(ctx, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDownloadsCleanerCleanDryRun(t *testing.T) {
	dir := t.TempDir()
	filePath := createSparseFile(t, dir, "Installer.dmg", 1024)
	dirPath := filepath.Join(dir, "Archive")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	createSparseFile(t, dirPath, "payload.bin", 2048)

	c := NewDownloadsCleaner()
	entries := []FileEntry{
		{Path: filePath, Size: 1024, Category: CategoryDownloads},
		{Path: dirPath, Size: 2048, IsDir: true, Category: CategoryDownloads},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if result.BytesFreed != 3072 {
		t.Errorf("BytesFreed = %d, want 3072", result.BytesFreed)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Error("file should still exist after dry run")
	}
	if _, err := os.Stat(dirPath); err != nil {
		t.Error("directory should still exist after dry run")
	}
}

func TestDownloadsCleanerCleanActualDeletion(t *testing.T) {
	dir := t.TempDir()
	filePath := createSparseFile(t, dir, "Setup.pkg", 1024)
	dirPath := filepath.Join(dir, "Archive")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	createSparseFile(t, dirPath, "payload.bin", 2048)

	c := NewDownloadsCleaner()
	entries := []FileEntry{
		{Path: filePath, Size: 1024, Category: CategoryDownloads},
		{Path: dirPath, Size: 2048, IsDir: true, Category: CategoryDownloads},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if result.BytesFreed != 3072 {
		t.Errorf("BytesFreed = %d, want 3072", result.BytesFreed)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Error("directory should have been deleted")
	}
}

func TestDownloadsCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewDownloadsCleaner()
	_, err := c.Clean(ctx, []FileEntry{{Path: "/tmp/file", Size: 1, Category: CategoryDownloads}}, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestIsInstallerPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/tmp/Installer.dmg", want: true},
		{path: "/tmp/Setup.PKG", want: true},
		{path: "/tmp/archive.zip", want: false},
		{path: "/tmp/folder", want: false},
	}

	for _, tt := range tests {
		if got := isInstallerPath(tt.path); got != tt.want {
			t.Errorf("isInstallerPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

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
