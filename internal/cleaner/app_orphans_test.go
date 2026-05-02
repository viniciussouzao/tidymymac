package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAppOrphansCleanerMetadata(t *testing.T) {
	c := NewAppOrphansCleaner()
	if c.Category() != CategoryAppOrphans {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryAppOrphans)
	}
	if c.Name() != "App Orphans" {
		t.Errorf("Name() = %q, want %q", c.Name(), "App Orphans")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestAppOrphansCleanerScanFindsOnlyMissingBundleIDs(t *testing.T) {
	home := t.TempDir()
	appRoot := filepath.Join(home, "Applications")
	createTestApp(t, appRoot, "Live.app", "com.example.LiveApp")
	createTestApp(t, appRoot, "Helper.app", "com.example.LiveApp.Helper")

	library := filepath.Join(home, "Library")
	createDir(t, library, "Application Support", "com.example.OldApp")
	createDir(t, library, "Caches", "com.example.OldApp")
	createSparseFile(t, filepath.Join(library, "Preferences"), "com.example.OldApp.plist", 128)
	createSparseFile(t, filepath.Join(library, "Logs"), "com.example.OldApp", 256)
	createDir(t, library, "Saved Application State", "com.example.OldApp.savedState")
	createDir(t, library, "HTTPStorages", "com.example.OldApp")
	createDir(t, library, "WebKit", "com.example.OldApp")

	createDir(t, library, "Caches", "com.example.LiveApp")
	createDir(t, library, "Caches", "com.example.LiveApp.Helper")
	createDir(t, library, "Caches", "com.apple.TextEdit")
	createDir(t, library, "Caches", "Spotify")
	createSparseFile(t, filepath.Join(library, "Preferences"), "com.example.OldApp.lockfile", 64)

	c := &AppOrphansCleaner{
		homeDir:         home,
		appSearchRoots:  []string{appRoot},
		bundleIDReader:  readAppBundleID,
		pathSizeFetcher: func(context.Context, string) (int64, error) { return 4096, nil },
	}

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Scan() errors = %v, want none", result.Errors)
	}
	if result.TotalFiles != 7 {
		t.Fatalf("TotalFiles = %d, want 7", result.TotalFiles)
	}

	got := map[string]bool{}
	for _, entry := range result.Entries {
		got[entry.Path] = true
		if entry.Category != CategoryAppOrphans {
			t.Errorf("entry Category = %q, want %q", entry.Category, CategoryAppOrphans)
		}
	}

	want := []string{
		filepath.Join(library, "Application Support", "com.example.OldApp"),
		filepath.Join(library, "Caches", "com.example.OldApp"),
		filepath.Join(library, "Preferences", "com.example.OldApp.plist"),
		filepath.Join(library, "Logs", "com.example.OldApp"),
		filepath.Join(library, "Saved Application State", "com.example.OldApp.savedState"),
		filepath.Join(library, "HTTPStorages", "com.example.OldApp"),
		filepath.Join(library, "WebKit", "com.example.OldApp"),
	}
	for _, path := range want {
		if !got[path] {
			t.Errorf("expected orphan path %q", path)
		}
	}

	notWant := []string{
		filepath.Join(library, "Caches", "com.example.LiveApp"),
		filepath.Join(library, "Caches", "com.example.LiveApp.Helper"),
		filepath.Join(library, "Caches", "com.apple.TextEdit"),
		filepath.Join(library, "Caches", "Spotify"),
		filepath.Join(library, "Preferences", "com.example.OldApp.lockfile"),
	}
	for _, path := range notWant {
		if got[path] {
			t.Errorf("did not expect path %q", path)
		}
	}
}

func TestAppOrphansCleanerScanEmptyHomeDir(t *testing.T) {
	c := &AppOrphansCleaner{homeDir: ""}
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

func TestAppOrphansCleanerScanProgress(t *testing.T) {
	home := t.TempDir()
	createDir(t, filepath.Join(home, "Library"), "Caches", "com.example.OldApp")

	c := &AppOrphansCleaner{
		homeDir:         home,
		appSearchRoots:  []string{filepath.Join(home, "Applications")},
		bundleIDReader:  readAppBundleID,
		pathSizeFetcher: func(context.Context, string) (int64, error) { return 1, nil },
	}

	var calls int
	_, err := c.Scan(t.Context(), func(p ScanProgress) {
		calls++
		if p.Category != CategoryAppOrphans {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryAppOrphans)
		}
	})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if calls == 0 {
		t.Error("expected at least one progress callback")
	}
}

func TestAppOrphansCleanerScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewAppOrphansCleaner()
	_, err := c.Scan(ctx, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestAppOrphansCleanerCleanDryRun(t *testing.T) {
	dir := t.TempDir()
	filePath := createSparseFile(t, dir, "com.example.OldApp.plist", 1024)
	dirPath := filepath.Join(dir, "com.example.OldApp")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	createSparseFile(t, dirPath, "payload.bin", 2048)

	c := NewAppOrphansCleaner()
	entries := []FileEntry{
		{Path: filePath, Size: 1024, Category: CategoryAppOrphans},
		{Path: dirPath, Size: 2048, IsDir: true, Category: CategoryAppOrphans},
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
	if _, err := os.Stat(filePath); err != nil {
		t.Error("file should still exist after dry run")
	}
	if _, err := os.Stat(dirPath); err != nil {
		t.Error("directory should still exist after dry run")
	}
}

func TestAppOrphansCleanerCleanActualDeletion(t *testing.T) {
	dir := t.TempDir()
	filePath := createSparseFile(t, dir, "com.example.OldApp.plist", 1024)
	dirPath := filepath.Join(dir, "com.example.OldApp")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	createSparseFile(t, dirPath, "payload.bin", 2048)

	c := NewAppOrphansCleaner()
	entries := []FileEntry{
		{Path: filePath, Size: 1024, Category: CategoryAppOrphans},
		{Path: dirPath, Size: 2048, IsDir: true, Category: CategoryAppOrphans},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Error("directory should have been deleted")
	}
}

func TestAppOrphansCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewAppOrphansCleaner()
	_, err := c.Clean(ctx, []FileEntry{{Path: "/tmp/file", Size: 1, Category: CategoryAppOrphans}}, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestOrphanCandidateBundleID(t *testing.T) {
	tests := []struct {
		root string
		name string
		want string
		ok   bool
	}{
		{root: "/Users/me/Library/Caches", name: "com.example.App", want: "com.example.App", ok: true},
		{root: "/Users/me/Library/Preferences", name: "com.example.App.plist", want: "com.example.App", ok: true},
		{root: "/Users/me/Library/Saved Application State", name: "com.example.App.savedState", want: "com.example.App", ok: true},
		{root: "/Users/me/Library/Caches", name: "com.apple.TextEdit", ok: false},
		{root: "/Users/me/Library/Caches", name: "Spotify", ok: false},
		{root: "/Users/me/Library/Preferences", name: "com.example.App.lockfile", ok: false},
	}

	for _, tt := range tests {
		got, ok := orphanCandidateBundleID(tt.root, tt.name)
		if ok != tt.ok || got != tt.want {
			t.Errorf("orphanCandidateBundleID(%q, %q) = %q, %v; want %q, %v", tt.root, tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestReadXMLBundleID(t *testing.T) {
	got := readXMLBundleID([]byte(testInfoPlist("com.example.App")))
	if got != "com.example.App" {
		t.Errorf("readXMLBundleID() = %q, want %q", got, "com.example.App")
	}
}

func createTestApp(t *testing.T, root, name, bundleID string) string {
	t.Helper()

	appDir := filepath.Join(root, name)
	contentsDir := filepath.Join(appDir, "Contents")
	if err := os.MkdirAll(contentsDir, 0o755); err != nil {
		t.Fatalf("failed to create app contents: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentsDir, "Info.plist"), []byte(testInfoPlist(bundleID)), 0o644); err != nil {
		t.Fatalf("failed to write Info.plist: %v", err)
	}
	return appDir
}

func createDir(t *testing.T, base string, parts ...string) string {
	t.Helper()

	path := filepath.Join(append([]string{base}, parts...)...)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("failed to create dir %q: %v", path, err)
	}
	return path
}

func testInfoPlist(bundleID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>` + bundleID + `</string>
</dict>
</plist>
`
}
