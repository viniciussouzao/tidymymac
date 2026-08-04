package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	return string(data)
}

func TestAddProtectedPathAt_NewFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "dir", "config.yaml")

	if err := addProtectedPathAt(p, "/Users/vini/Secrets"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected config file to be created: %v", err)
	}
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom() error: %v", err)
	}
	if !cfg.IsProtected("/Users/vini/Secrets/file.txt") {
		t.Error("expected the new entry to be protected after round-trip")
	}
}

func TestAddProtectedPathAt_AppendsToExistingList(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/a"]`)

	if err := addProtectedPathAt(p, "/b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom() error: %v", err)
	}
	if !cfg.IsProtected("/a") || !cfg.IsProtected("/b") {
		t.Errorf("expected both /a and /b protected, got %+v", cfg.ProtectedPaths)
	}
}

func TestAddProtectedPathAt_KeyAbsent(t *testing.T) {
	p := writeConfig(t, `disabled_categories: ["docker"]`)

	if err := addProtectedPathAt(p, "/Users/vini/Secrets"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom() error: %v", err)
	}
	if !cfg.IsProtected("/Users/vini/Secrets") {
		t.Error("expected the new protected_paths key to be created with the entry")
	}
	if !cfg.IsCategoryDisabled("docker") {
		t.Error("expected disabled_categories to survive unchanged")
	}
}

func TestAddProtectedPathAt_DedupesAlreadyProtected(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home dir: %v", err)
	}
	target := filepath.Join(home, "Secrets")
	p := writeConfig(t, `protected_paths: ["`+target+`"]`)
	before := readFile(t, p)

	if err := addProtectedPathAt(p, "~/Secrets"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := readFile(t, p)
	if before != after {
		t.Errorf("adding an already-protected (equivalent) path must not rewrite the file\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestAddProtectedPathAt_RejectsRelativePath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")

	if err := addProtectedPathAt(p, "Documents/Secrets"); err == nil {
		t.Fatal("expected an error for a relative path")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("expected no file to be created for a rejected entry")
	}
}

func TestAddProtectedPathAt_RejectsEmptyPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")

	if err := addProtectedPathAt(p, ""); err == nil {
		t.Fatal("expected an error for an empty path")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("expected no file to be created for a rejected entry")
	}
}

func TestAddProtectedPathAt_RefusesToEditAlreadyInvalidFile(t *testing.T) {
	p := writeConfig(t, `protected_path: ["/Users/vini/Secrets"]`) // typo'd key
	before := readFile(t, p)

	if err := addProtectedPathAt(p, "/Users/vini/Other"); err == nil {
		t.Fatal("expected an error when the existing file is already invalid")
	}

	after := readFile(t, p)
	if before != after {
		t.Error("refusing to edit an invalid file must not modify it")
	}
}

func TestRemoveProtectedPathAt_RemovesExactMatch(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/a", "/b"]`)

	removed, err := removeProtectedPathAt(p, "/a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom() error: %v", err)
	}
	if cfg.IsProtected("/a") {
		t.Error("expected /a to no longer be protected")
	}
	if !cfg.IsProtected("/b") {
		t.Error("expected /b to remain protected")
	}
}

func TestRemoveProtectedPathAt_RemovesByNormalizedEquivalence(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home dir: %v", err)
	}
	target := filepath.Join(home, "Secrets")
	p := writeConfig(t, `protected_paths: ["`+target+`"]`)

	removed, err := removeProtectedPathAt(p, "~/Secrets")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true for a normalized-equivalent path")
	}

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom() error: %v", err)
	}
	if cfg.IsProtected(target) {
		t.Error("expected the entry to be removed")
	}
}

func TestRemoveProtectedPathAt_NoMatchIsNoopNotError(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/a"]`)
	before := readFile(t, p)

	removed, err := removeProtectedPathAt(p, "/never/protected")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("expected removed = false for a path that was never protected")
	}

	after := readFile(t, p)
	if before != after {
		t.Error("a no-op remove must not modify the file")
	}
}

func TestRemoveProtectedPathAt_KeyAbsent(t *testing.T) {
	p := writeConfig(t, `disabled_categories: ["docker"]`)

	removed, err := removeProtectedPathAt(p, "/a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("expected removed = false when protected_paths doesn't exist")
	}
}

func TestRemoveProtectedPathAt_LastEntryEmptiesProtection(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/a"]`)

	removed, err := removeProtectedPathAt(p, "/a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom() error: %v", err)
	}
	if cfg.IsProtected("/a") {
		t.Error("expected no paths to be protected after removing the last entry")
	}
}

func TestAddProtectedPath_PreservesCommentsElsewhereInFile(t *testing.T) {
	p := writeConfig(t, "protected_paths:\n  - /a # keep this one\ndisabled_categories:\n  - docker\n")

	if err := addProtectedPathAt(p, "/b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := readFile(t, p)
	if !strings.Contains(after, "keep this one") {
		t.Errorf("expected the hand-written comment to survive the edit, got:\n%s", after)
	}
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom() error: %v", err)
	}
	if !cfg.IsProtected("/a") || !cfg.IsProtected("/b") {
		t.Errorf("expected both /a and /b protected, got %+v", cfg.ProtectedPaths)
	}
	if !cfg.IsCategoryDisabled("docker") {
		t.Error("expected disabled_categories to survive unchanged")
	}
}

func TestRemoveProtectedPath_PreservesCommentsElsewhereInFile(t *testing.T) {
	p := writeConfig(t, "protected_paths:\n  - /a\n  - /b # keep this one\n")

	removed, err := removeProtectedPathAt(p, "/a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}

	after := readFile(t, p)
	if !strings.Contains(after, "keep this one") {
		t.Errorf("expected the hand-written comment to survive the edit, got:\n%s", after)
	}
}
