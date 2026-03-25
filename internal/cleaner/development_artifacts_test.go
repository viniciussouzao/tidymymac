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
	f1 := createTempFile(t, dir, "cache-a.bin", 128)
	f2 := createTempFile(t, dir, "cache-b.bin", 256)

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
		goClean: func(context.Context, ...string) error {
			t.Error("goClean must not be called in dry-run mode")
			return nil
		},
	}
	entries := []FileEntry{
		{Path: f1, Size: 128, Category: CategoryDevelopmentArtifacts},
		{Path: f2, Size: 256, Category: CategoryDevelopmentArtifacts},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if result.BytesFreed != 384 {
		t.Errorf("BytesFreed = %d, want 384", result.BytesFreed)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}
	// files must not be removed in dry-run
	for _, f := range []string{f1, f2} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("file %q should still exist after dry run: %v", f, err)
		}
	}
}

// TestDevelopmentArtifactsCleanerCleanGoCleanSuccess verifies that when "go clean"
// succeeds, all scanned entries are counted as freed without manual deletion.
func TestDevelopmentArtifactsCleanerCleanGoCleanSuccess(t *testing.T) {
	dir := t.TempDir()
	f1 := createTempFile(t, dir, "cache-a.bin", 512)
	f2 := createTempFile(t, dir, "cache-b.bin", 1024)

	var cleanCalls []string
	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
		goClean: func(_ context.Context, args ...string) error {
			cleanCalls = append(cleanCalls, args[0])
			return nil
		},
	}
	entries := []FileEntry{
		{Path: f1, Size: 512, Category: CategoryDevelopmentArtifacts},
		{Path: f2, Size: 1024, Category: CategoryDevelopmentArtifacts},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if result.BytesFreed != 1536 {
		t.Errorf("BytesFreed = %d, want 1536", result.BytesFreed)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want none", result.Errors)
	}
	if len(cleanCalls) != 2 || cleanCalls[0] != "-cache" || cleanCalls[1] != "-modcache" {
		t.Errorf("goClean calls = %v, want [-cache -modcache]", cleanCalls)
	}
}

// TestDevelopmentArtifactsCleanerCleanGoCleanSuccessReportsProgress verifies that
// a single progress callback is emitted after a successful "go clean".
func TestDevelopmentArtifactsCleanerCleanGoCleanSuccessReportsProgress(t *testing.T) {
	dir := t.TempDir()
	f1 := createTempFile(t, dir, "cache-a.bin", 256)
	f2 := createTempFile(t, dir, "cache-b.bin", 512)

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
		goClean: func(context.Context, ...string) error { return nil },
	}
	entries := []FileEntry{
		{Path: f1, Size: 256, Category: CategoryDevelopmentArtifacts},
		{Path: f2, Size: 512, Category: CategoryDevelopmentArtifacts},
	}

	var progressCalls int
	_, err := c.Clean(t.Context(), entries, false, func(p CleanProgress) {
		progressCalls++
		if p.FilesDeleted != 2 {
			t.Errorf("progress FilesDeleted = %d, want 2", p.FilesDeleted)
		}
		if p.BytesDeleted != 768 {
			t.Errorf("progress BytesDeleted = %d, want 768", p.BytesDeleted)
		}
		if p.FilesTotal != 2 {
			t.Errorf("progress FilesTotal = %d, want 2", p.FilesTotal)
		}
	})
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if progressCalls != 1 {
		t.Errorf("progressCalls = %d, want 1 (single call after go clean)", progressCalls)
	}
}

// TestDevelopmentArtifactsCleanerCleanGoCleanCacheFails verifies that when
// "go clean -cache" fails the cleaner falls back to manual deletion and
// records the error.
func TestDevelopmentArtifactsCleanerCleanGoCleanCacheFails(t *testing.T) {
	dir := t.TempDir()
	file := createTempFile(t, dir, "cache.bin", 256)

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
		goClean: func(_ context.Context, args ...string) error {
			if args[0] == "-cache" {
				return errors.New("go clean -cache: permission denied")
			}
			return nil
		},
	}
	entries := []FileEntry{
		{Path: file, Size: 256, Category: CategoryDevelopmentArtifacts},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	// fallback must have deleted the file
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if result.BytesFreed != 256 {
		t.Errorf("BytesFreed = %d, want 256", result.BytesFreed)
	}
	// error from go clean -cache must be recorded
	if len(result.Errors) != 1 {
		t.Errorf("len(Errors) = %d, want 1", len(result.Errors))
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file should have been deleted by fallback")
	}
}

// TestDevelopmentArtifactsCleanerCleanGoCleanModcacheFails verifies that when
// "go clean -modcache" fails the cleaner falls back to manual deletion and
// records the error.
func TestDevelopmentArtifactsCleanerCleanGoCleanModcacheFails(t *testing.T) {
	dir := t.TempDir()
	file := createTempFile(t, dir, "module.zip", 512)

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
		goClean: func(_ context.Context, args ...string) error {
			if args[0] == "-modcache" {
				return errors.New("go clean -modcache: permission denied")
			}
			return nil
		},
	}
	entries := []FileEntry{
		{Path: file, Size: 512, Category: CategoryDevelopmentArtifacts},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if len(result.Errors) != 1 {
		t.Errorf("len(Errors) = %d, want 1", len(result.Errors))
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file should have been deleted by fallback")
	}
}

// TestDevelopmentArtifactsCleanerCleanGoCleanBothFail verifies that when both
// "go clean" calls fail, both errors are recorded and fallback still runs.
func TestDevelopmentArtifactsCleanerCleanGoCleanBothFail(t *testing.T) {
	dir := t.TempDir()
	file := createTempFile(t, dir, "cache.bin", 128)

	goCleanErr := errors.New("go not found")
	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
		goClean: func(context.Context, ...string) error { return goCleanErr },
	}
	entries := []FileEntry{
		{Path: file, Size: 128, Category: CategoryDevelopmentArtifacts},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if len(result.Errors) != 2 {
		t.Errorf("len(Errors) = %d, want 2 (one per go clean call)", len(result.Errors))
	}
	// fallback must still have deleted the file
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file should have been deleted by fallback")
	}
}

func TestDevelopmentArtifactsCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dir := t.TempDir()
	file := createTempFile(t, dir, "cache.bin", 128)

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
		goClean: func(context.Context, ...string) error { return errors.New("context canceled") },
	}
	entries := []FileEntry{
		{Path: file, Size: 128, Category: CategoryDevelopmentArtifacts},
	}

	_, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDevelopmentArtifactsCleanerCleanReportsProgress(t *testing.T) {
	dir := t.TempDir()
	f1 := createTempFile(t, dir, "cache-a.bin", 256)
	f2 := createTempFile(t, dir, "cache-b.bin", 512)

	c := &DevelopmentArtifactsCleaner{
		homeDir: dir,
		goEnv:   func(context.Context, string) (string, error) { return "", nil },
		goClean: func(context.Context, ...string) error { return errors.New("go unavailable") },
	}
	entries := []FileEntry{
		{Path: f1, Size: 256, Category: CategoryDevelopmentArtifacts},
		{Path: f2, Size: 512, Category: CategoryDevelopmentArtifacts},
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
	// i=0: 0%50==0 → called; i=1: 1==len(entries)-1 → called
	if progressCalls != 2 {
		t.Errorf("progressCalls = %d, want 2", progressCalls)
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
