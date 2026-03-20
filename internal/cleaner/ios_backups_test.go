package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// makeBackupDir creates a fake iOS backup directory with the given files inside.
// Returns the path to the backup directory.
func makeBackupDir(t *testing.T, root, name string, files map[string]int) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("makeBackupDir MkdirAll: %v", err)
	}
	for fname, size := range files {
		createTempFile(t, dir, fname, size)
	}
	return dir
}

func TestIOSBackupsCleanerMetadata(t *testing.T) {
	c := NewIOSBackupsCleaner()
	if c.Category() != CategoryIOSBackups {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryIOSBackups)
	}
	if c.Name() != "iOS Backups" {
		t.Errorf("Name() = %q, want %q", c.Name(), "iOS Backups")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestIOSBackupsCleanerScanEmptyHomeDir(t *testing.T) {
	c := &IOSBackupsCleaner{homeDir: ""}
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

func TestIOSBackupsCleanerScanNoBackupDir(t *testing.T) {
	c := &IOSBackupsCleaner{homeDir: t.TempDir()}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries when backup root absent, got %d", len(result.Entries))
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %v", result.Errors)
	}
}

func TestIOSBackupsCleanerScanOneBackup(t *testing.T) {
	home := t.TempDir()
	backupRoot := filepath.Join(home, "Library", "Application Support", "MobileSync", "Backup")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	makeBackupDir(t, backupRoot, "00008101-AABBCCDDEEFF", map[string]int{
		"Manifest.db": 1024,
		"Info.plist":  512,
	})

	c := &IOSBackupsCleaner{homeDir: home}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry (one backup dir), got %d", len(result.Entries))
	}

	entry := result.Entries[0]
	if !entry.IsDir {
		t.Error("entry.IsDir = false, want true")
	}
	if entry.Size != 1536 {
		t.Errorf("entry.Size = %d, want 1536", entry.Size)
	}
	if entry.Category != CategoryIOSBackups {
		t.Errorf("entry.Category = %q, want %q", entry.Category, CategoryIOSBackups)
	}
	if result.TotalFiles != 1 {
		t.Errorf("TotalFiles = %d, want 1", result.TotalFiles)
	}
	if result.TotalSize != 1536 {
		t.Errorf("TotalSize = %d, want 1536", result.TotalSize)
	}
}

func TestIOSBackupsCleanerScanMultipleBackups(t *testing.T) {
	home := t.TempDir()
	backupRoot := filepath.Join(home, "Library", "Application Support", "MobileSync", "Backup")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	makeBackupDir(t, backupRoot, "backup-iphone", map[string]int{"a": 1000, "b": 2000})
	makeBackupDir(t, backupRoot, "backup-ipad", map[string]int{"c": 500})

	c := &IOSBackupsCleaner{homeDir: home}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}
	if result.TotalFiles != 2 {
		t.Errorf("TotalFiles = %d, want 2", result.TotalFiles)
	}
	if result.TotalSize != 3500 {
		t.Errorf("TotalSize = %d, want 3500", result.TotalSize)
	}
	for _, e := range result.Entries {
		if !e.IsDir {
			t.Errorf("entry %q: IsDir = false, want true", e.Path)
		}
	}
}

func TestIOSBackupsCleanerScanSkipsFilesAtRoot(t *testing.T) {
	home := t.TempDir()
	backupRoot := filepath.Join(home, "Library", "Application Support", "MobileSync", "Backup")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	// A stray file at the backup root should not become an entry.
	createTempFile(t, backupRoot, "stray.txt", 100)
	makeBackupDir(t, backupRoot, "00008101-AABBCCDDEEFF", map[string]int{"file": 200})

	c := &IOSBackupsCleaner{homeDir: home}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry (dir only), got %d", len(result.Entries))
	}
}

func TestIOSBackupsCleanerScanProgress(t *testing.T) {
	home := t.TempDir()
	backupRoot := filepath.Join(home, "Library", "Application Support", "MobileSync", "Backup")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	makeBackupDir(t, backupRoot, "backup1", map[string]int{"f": 100})
	makeBackupDir(t, backupRoot, "backup2", map[string]int{"f": 200})

	var calls int
	c := &IOSBackupsCleaner{homeDir: home}
	_, err := c.Scan(t.Context(), func(p ScanProgress) {
		calls++
		if p.Category != CategoryIOSBackups {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryIOSBackups)
		}
	})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if calls != 2 {
		t.Errorf("progress calls = %d, want 2 (one per backup)", calls)
	}
}

func TestIOSBackupsCleanerScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewIOSBackupsCleaner()
	result, err := c.Scan(ctx, nil)
	if err != nil {
		t.Fatalf("Scan() unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestIOSBackupsCleanerCleanDryRun(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backup1")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createTempFile(t, backupDir, "Manifest.db", 1024)

	c := NewIOSBackupsCleaner()
	entries := []FileEntry{
		{Path: backupDir, Size: 1024, IsDir: true, Category: CategoryIOSBackups},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 1024 {
		t.Errorf("BytesFreed = %d, want 1024", result.BytesFreed)
	}

	// Directory must still exist after dry run.
	if _, err := os.Stat(backupDir); err != nil {
		t.Error("backup dir should still exist after dry run")
	}
}

func TestIOSBackupsCleanerCleanActualDeletion(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "00008101-AABBCCDDEEFF")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createTempFile(t, backupDir, "Manifest.db", 2048)
	createTempFile(t, backupDir, "Info.plist", 512)

	c := NewIOSBackupsCleaner()
	entries := []FileEntry{
		{Path: backupDir, Size: 2560, IsDir: true, Category: CategoryIOSBackups},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 2560 {
		t.Errorf("BytesFreed = %d, want 2560", result.BytesFreed)
	}

	// Entire backup directory must be gone.
	if _, err := os.Stat(backupDir); !os.IsNotExist(err) {
		t.Error("backup dir should have been deleted entirely")
	}
}

func TestIOSBackupsCleanerCleanMultipleBackups(t *testing.T) {
	dir := t.TempDir()

	backup1 := filepath.Join(dir, "backup-iphone")
	backup2 := filepath.Join(dir, "backup-ipad")
	for _, d := range []string{backup1, backup2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		createTempFile(t, d, "data", 500)
	}

	c := NewIOSBackupsCleaner()
	entries := []FileEntry{
		{Path: backup1, Size: 500, IsDir: true, Category: CategoryIOSBackups},
		{Path: backup2, Size: 500, IsDir: true, Category: CategoryIOSBackups},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if result.BytesFreed != 1000 {
		t.Errorf("BytesFreed = %d, want 1000", result.BytesFreed)
	}
	for _, d := range []string{backup1, backup2} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("expected %q to be deleted", d)
		}
	}
}

func TestIOSBackupsCleanerCleanNonExistentPathIsIgnored(t *testing.T) {
	c := NewIOSBackupsCleaner()
	entries := []FileEntry{
		{Path: "/tmp/tidymymac-does-not-exist-backup", Size: 100, IsDir: true, Category: CategoryIOSBackups},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	// os.IsNotExist is ignored; the entry still counts as deleted.
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if len(result.Errors) != 0 {
		t.Errorf("expected no errors for not-exist path, got %v", result.Errors)
	}
}

func TestIOSBackupsCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewIOSBackupsCleaner()
	entries := []FileEntry{
		{Path: "/tmp/whatever", Size: 100, IsDir: true, Category: CategoryIOSBackups},
	}

	_, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestIOSBackupsCleanerCleanProgress(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backup1")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createTempFile(t, backupDir, "data", 100)

	var calls int
	c := NewIOSBackupsCleaner()
	entries := []FileEntry{
		{Path: backupDir, Size: 100, IsDir: true, Category: CategoryIOSBackups},
	}

	_, err := c.Clean(t.Context(), entries, false, func(p CleanProgress) {
		calls++
		if p.Category != CategoryIOSBackups {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryIOSBackups)
		}
		if p.FilesTotal != 1 {
			t.Errorf("progress FilesTotal = %d, want 1", p.FilesTotal)
		}
	})
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if calls != 1 {
		t.Errorf("progress calls = %d, want 1", calls)
	}
}
