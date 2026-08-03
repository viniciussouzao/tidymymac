package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return p
}

func TestLoad_MissingFileReturnsZeroValueConfig(t *testing.T) {
	cfg, err := loadFrom(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil zero-value config")
	}
	if cfg.IsProtected("/anything") {
		t.Error("zero-value config should not protect anything")
	}
	if cfg.IsCategoryDisabled("anything") {
		t.Error("zero-value config should not disable anything")
	}
}

func TestLoad_EmptyFileReturnsZeroValueConfig(t *testing.T) {
	p := writeConfig(t, "")
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.IsProtected("/anything") {
		t.Error("empty config should not protect anything")
	}
}

func TestLoad_MalformedYAMLReturnsError(t *testing.T) {
	p := writeConfig(t, "protected_paths: [unterminated")
	if _, err := loadFrom(p); err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestLoad_UnknownKeyReturnsError(t *testing.T) {
	p := writeConfig(t, `protected_path: ["/Users/vini/Secrets"]`)
	_, err := loadFrom(p)
	if err == nil {
		t.Fatal("expected an error for unrecognized key")
	}
	if !strings.Contains(err.Error(), "unrecognized or malformed content") {
		t.Errorf("error %q does not mention unrecognized/malformed content", err)
	}
}

func TestLoad_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home dir: %v", err)
	}
	p := writeConfig(t, `protected_paths: ["~/Secrets"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(home, "Secrets")
	if !cfg.IsProtected(want) {
		t.Errorf("expected %q to be protected after tilde expansion", want)
	}
}

func TestLoad_RejectsEmptyProtectedPathEntry(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/Users/vini/Secrets", ""]`)
	if _, err := loadFrom(p); err == nil {
		t.Fatal("expected an error for an empty protected_paths entry")
	}
}

func TestLoad_RejectsWhitespaceOnlyProtectedPathEntry(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["   "]`)
	if _, err := loadFrom(p); err == nil {
		t.Fatal("expected an error for a whitespace-only protected_paths entry")
	}
}

func TestLoad_RejectsRelativeProtectedPathEntry(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["Documents/Secrets"]`)
	if _, err := loadFrom(p); err == nil {
		t.Fatal("expected an error for a relative protected_paths entry")
	}
}

func TestLoad_ValidConfigLoadsDisabledCategories(t *testing.T) {
	p := writeConfig(t, `disabled_categories: ["docker", "logs"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsCategoryDisabled("docker") || !cfg.IsCategoryDisabled("logs") {
		t.Error("expected docker and logs to be disabled")
	}
	if cfg.IsCategoryDisabled("caches") {
		t.Error("caches was not listed as disabled")
	}
}

func TestIsProtected_ExactMatch(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/Users/vini/Secrets/file.txt"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsProtected("/Users/vini/Secrets/file.txt") {
		t.Error("expected exact match to be protected")
	}
}

func TestIsProtected_DirectoryPrefixMatch(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/Users/vini/Projects"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsProtected("/Users/vini/Projects/secret/file.txt") {
		t.Error("expected nested file under protected dir to be protected")
	}
}

func TestIsProtected_DoesNotMatchSiblingWithCommonStringPrefix(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/Users/vini/Projects"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.IsProtected("/Users/vini/Projects-old/secret") {
		t.Error("sibling directory with a common string prefix must not match")
	}
}

func TestIsProtected_CaseInsensitive(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/Users/vini/Secrets"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsProtected("/Users/VINI/SECRETS/file.txt") {
		t.Error("expected case-insensitive match to be protected")
	}
}

func TestIsProtected_FirmlinkAliasBothDirections(t *testing.T) {
	// Config written in the /var/tmp shortcut form must also protect the
	// /private/var/tmp backing form that some tooling reports paths in.
	p := writeConfig(t, `protected_paths: ["/var/tmp/keepme"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsProtected("/var/tmp/keepme/file.txt") {
		t.Error("expected the configured shortcut form to protect itself")
	}
	if !cfg.IsProtected("/private/var/tmp/keepme/file.txt") {
		t.Error("expected the /private backing form to also be protected")
	}

	// And the reverse: config written in the /private form must also
	// protect the /var shortcut form that cleaners actually emit.
	p2 := writeConfig(t, `protected_paths: ["/private/var/tmp/keepme"]`)
	cfg2, err := loadFrom(p2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg2.IsProtected("/var/tmp/keepme/file.txt") {
		t.Error("expected the /var shortcut form (what cleaners emit) to be protected when config uses the /private form")
	}
}

func TestIsProtected_UnrelatedPathNotProtected(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/Users/vini/Secrets"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.IsProtected("/Users/vini/Downloads/file.txt") {
		t.Error("unrelated path should not be protected")
	}
}

func TestNilConfig_IsProtectedReturnsFalse(t *testing.T) {
	var cfg *Config
	if cfg.IsProtected("/anything") {
		t.Error("nil config should never protect anything")
	}
}

func TestNilConfig_IsCategoryDisabledReturnsFalse(t *testing.T) {
	var cfg *Config
	if cfg.IsCategoryDisabled("docker") {
		t.Error("nil config should never disable anything")
	}
}

func TestNilConfig_TagReturnsEntriesUnchanged(t *testing.T) {
	var cfg *Config
	entries := []cleaner.FileEntry{{Path: "/a"}, {Path: "/b"}}
	got := cfg.Tag(entries)
	if len(got) != 2 || got[0].Protected || got[1].Protected {
		t.Errorf("nil config should tag nothing as protected, got %+v", got)
	}
}

func TestTag_SetsProtectedWithoutRemovingEntries(t *testing.T) {
	p := writeConfig(t, `protected_paths: ["/Users/vini/Secrets"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := []cleaner.FileEntry{
		{Path: "/Users/vini/Secrets/file.txt"},
		{Path: "/Users/vini/Downloads/file.txt"},
	}
	tagged := cfg.Tag(entries)
	if len(tagged) != 2 {
		t.Fatalf("Tag must not remove entries, got %d want 2", len(tagged))
	}
	if !tagged[0].Protected {
		t.Error("expected first entry to be tagged protected")
	}
	if tagged[1].Protected {
		t.Error("expected second entry to remain unprotected")
	}
}

func TestStripProtected_RemovesOnlyTaggedEntries(t *testing.T) {
	entries := []cleaner.FileEntry{
		{Path: "/a", Protected: true},
		{Path: "/b", Protected: false},
		{Path: "/c", Protected: true},
	}
	kept := StripProtected(entries)
	if len(kept) != 1 || kept[0].Path != "/b" {
		t.Errorf("got %+v, want only /b", kept)
	}
}

func TestFilterRegistry_ExcludesDisabledCategories(t *testing.T) {
	r := cleaner.DefaultRegistry()
	p := writeConfig(t, `disabled_categories: ["docker"]`)
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	filtered := FilterRegistry(r, cfg)
	if _, ok := filtered.Get(cleaner.CategoryDocker); ok {
		t.Error("expected docker to be excluded from the filtered registry")
	}
	if len(filtered.All()) != len(r.All())-1 {
		t.Errorf("got %d cleaners, want %d", len(filtered.All()), len(r.All())-1)
	}
}

func TestFilterRegistry_NilConfigReturnsSameRegistry(t *testing.T) {
	r := cleaner.DefaultRegistry()
	if FilterRegistry(r, nil) != r {
		t.Error("expected nil config to return the same registry unchanged")
	}
}
