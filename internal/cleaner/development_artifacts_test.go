package cleaner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDevelopmentArtifactsCleanerMetadata(t *testing.T) {
	c := NewDevelopmentArtifactsCleaner()

	if c.Category() != CategoryDevelopmentArtifacts {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryDevelopmentArtifacts)
	}
	if c.Name() != "Development Artifacts" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Development Artifacts")
	}
	if c.Description() != "Go build cache and downloaded module cache" {
		t.Errorf("Description() = %q, want %q", c.Description(), "Go build cache and downloaded module cache")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
}

func TestDevelopmentArtifactsCleanerScanEmptyHomeDir(t *testing.T) {
	c := &DevelopmentArtifactsCleaner{
		homeDir: "",
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
	}

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

func TestDevelopmentArtifactsCleanerScanFindsGoTargets(t *testing.T) {
	dir := t.TempDir()
	goCacheDir := filepath.Join(dir, "custom-go-cache")
	goModDir := filepath.Join(dir, "custom-gopath", "pkg", "mod")

	if err := os.MkdirAll(goCacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(goModDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createTempFile(t, goCacheDir, "build-cache-a", 128)
	createTempFile(t, goCacheDir, "build-cache-b", 256)
	createTempFile(t, goModDir, "module-a.zip", 512)
	createTempFile(t, goModDir, "module-b.zip", 1024)

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv: func(_ context.Context, key string) (string, error) {
			switch key {
			case "GOCACHE":
				return goCacheDir, nil
			case "GOPATH":
				return filepath.Join(dir, "custom-gopath"), nil
			}
			return "", nil
		},
	}

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if result.TotalFiles != 4 {
		t.Fatalf("TotalFiles = %d, want 4", result.TotalFiles)
	}
	if result.TotalSize != int64(128+256+512+1024) {
		t.Errorf("TotalSize = %d, want %d", result.TotalSize, int64(128+256+512+1024))
	}
	if len(result.Entries) != 4 {
		t.Fatalf("len(Entries) = %d, want 4", len(result.Entries))
	}

	for _, entry := range result.Entries {
		if entry.IsDir {
			t.Errorf("entry %q IsDir = true, want false", entry.Path)
		}
		if entry.Category != CategoryDevelopmentArtifacts {
			t.Errorf("entry %q Category = %q, want %q", entry.Path, entry.Category, CategoryDevelopmentArtifacts)
		}
	}
}

func TestDevelopmentArtifactsCleanerScanFallsBackToDefaultGoPath(t *testing.T) {
	dir := t.TempDir()
	goBuildDir := filepath.Join(dir, "Library", "Caches", "go-build")
	defaultGoModDir := filepath.Join(dir, "go", "pkg", "mod")

	if err := os.MkdirAll(goBuildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(defaultGoModDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createTempFile(t, goBuildDir, "build-cache", 64)
	createTempFile(t, defaultGoModDir, "module.zip", 128)

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv: func(context.Context, string) (string, error) {
			return "", errors.New("go unavailable")
		},
	}

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result.TotalFiles != 2 {
		t.Fatalf("TotalFiles = %d, want 2", result.TotalFiles)
	}
}

func TestDevelopmentArtifactsCleanerScanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Library", "Caches", "go-build"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
	}

	_, err := c.Scan(ctx, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDevelopmentArtifactsCleanerCleanDryRun(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "go-build")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	createTempFile(t, target, "cache.bin", 256)

	c := NewDevelopmentArtifactsCleaner()
	entries := []FileEntry{
		{Path: target, Size: 256, IsDir: true, Category: CategoryDevelopmentArtifacts},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 256 {
		t.Errorf("BytesFreed = %d, want 256", result.BytesFreed)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("target directory should still exist after dry run: %v", err)
	}
}

func TestDevelopmentArtifactsCleanerCleanDeletesDirectories(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "pkg", "mod")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	createTempFile(t, target, "module.zip", 512)

	c := NewDevelopmentArtifactsCleaner()
	entries := []FileEntry{
		{Path: target, Size: 512, IsDir: true, Category: CategoryDevelopmentArtifacts},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 512 {
		t.Errorf("BytesFreed = %d, want 512", result.BytesFreed)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target directory should have been deleted, stat err = %v", err)
	}
}

func TestDevelopmentArtifactsCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewDevelopmentArtifactsCleaner()
	entries := []FileEntry{
		{Path: "/tmp/go-build", Size: 100, IsDir: true, Category: CategoryDevelopmentArtifacts},
	}

	_, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDevelopmentArtifactsCleanerCleanReportsProgress(t *testing.T) {
	dir := t.TempDir()
	t1 := filepath.Join(dir, "go-build")
	t2 := filepath.Join(dir, "pkg", "mod")

	if err := os.MkdirAll(t1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(t2, 0o755); err != nil {
		t.Fatal(err)
	}

	c := NewDevelopmentArtifactsCleaner()
	entries := []FileEntry{
		{Path: t1, Size: 256, IsDir: true, Category: CategoryDevelopmentArtifacts},
		{Path: t2, Size: 512, IsDir: true, Category: CategoryDevelopmentArtifacts},
	}

	var progressCalls int
	_, err := c.Clean(t.Context(), entries, true, func(p CleanProgress) {
		progressCalls++
		if p.Category != CategoryDevelopmentArtifacts {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryDevelopmentArtifacts)
		}
		if p.FilesTotal != 2 {
			t.Errorf("progress FilesTotal = %d, want 2", p.FilesTotal)
		}
	})
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if progressCalls != 2 {
		t.Errorf("progressCalls = %d, want 2 (one per entry)", progressCalls)
	}
}

func TestNormalizeGoEnvPath(t *testing.T) {
	value := "/tmp/custom-go" + string(os.PathListSeparator) + "/tmp/other"

	got := normalizeGoEnvPath(value)

	if got != filepath.Clean("/tmp/custom-go") {
		t.Errorf("normalizeGoEnvPath() = %q, want %q", got, filepath.Clean("/tmp/custom-go"))
	}
}

func TestNormalizeGoEnvPathEmpty(t *testing.T) {
	if got := normalizeGoEnvPath(""); got != "" {
		t.Errorf("normalizeGoEnvPath(\"\") = %q, want %q", got, "")
	}
}

func TestNormalizeGoEnvPathSingleEntry(t *testing.T) {
	want := filepath.Clean("/tmp/go")
	if got := normalizeGoEnvPath("/tmp/go"); got != want {
		t.Errorf("normalizeGoEnvPath() = %q, want %q", got, want)
	}
}
