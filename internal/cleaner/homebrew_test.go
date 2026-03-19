package cleaner

import (
	"os"
	"os/exec"
	"testing"
)

func TestHomebrewCleanerMetadata(t *testing.T) {
	c := NewHomebrewCleaner()
	if c.Category() != CategoryHomebrew {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryHomebrew)
	}
	if c.Name() != "Homebrew Cache" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Homebrew Cache")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestHomebrewCleanerScanReturnsResult(t *testing.T) {
	c := NewHomebrewCleaner()
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result == nil {
		t.Fatal("Scan() returned nil result")
	}
	if result.Category != CategoryHomebrew {
		t.Errorf("Category = %q, want %q", result.Category, CategoryHomebrew)
	}
}

func TestHomebrewCleanerScanWithoutBrew(t *testing.T) {
	if _, err := exec.LookPath("brew"); err == nil {
		t.Skip("brew is installed, cannot test missing-brew path")
	}

	c := NewHomebrewCleaner()
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries without brew, got %d", len(result.Entries))
	}
}

func TestHomebrewCleanerCleanDryRun(t *testing.T) {
	if _, err := exec.LookPath("brew"); err != nil {
		t.Skip("brew not installed")
	}

	c := NewHomebrewCleaner()
	entries := []FileEntry{
		{Path: "/tmp/fake-brew-cache/file.tar.gz", Size: 1024, Category: CategoryHomebrew},
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
}

func TestHomebrewCleanerCleanReportsDeletedFiles(t *testing.T) {
	if _, err := exec.LookPath("brew"); err != nil {
		t.Skip("brew not installed")
	}

	dir := t.TempDir()
	f := createTempFile(t, dir, "formula.tar.gz", 512)

	// Remove the file before Clean to simulate brew cleanup removing it
	os.Remove(f)

	c := NewHomebrewCleaner()
	entries := []FileEntry{
		{Path: f, Size: 512, Category: CategoryHomebrew},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	// File didn't exist, so brew cleanup should report it as deleted
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
}
