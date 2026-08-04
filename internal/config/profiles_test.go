package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// --- profile schema ----------------------------------------------------

func TestLoadProfilesRoundTrip(t *testing.T) {
	p := writeConfig(t, `
profiles:
  dev:
    categories: [development-artifacts, docker]
    paths:
      - ~/meu-projeto-js
  empty: {}
`)

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Fatalf("loaded %d profiles, want 2", len(cfg.Profiles))
	}

	dev := cfg.Profiles["dev"]
	if len(dev.Categories) != 2 || dev.Categories[0] != "development-artifacts" || dev.Categories[1] != "docker" {
		t.Errorf("dev.Categories = %v, want [development-artifacts docker]", dev.Categories)
	}
	if len(dev.Paths) != 1 || dev.Paths[0] != "~/meu-projeto-js" {
		t.Errorf("dev.Paths = %v, want [~/meu-projeto-js] (stored verbatim)", dev.Paths)
	}
}

func TestLoadRejectsUnknownProfileKey(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categorys: [docker]\n")

	if _, err := loadFrom(p); err == nil {
		t.Fatal("expected a typo'd key inside a profile to fail Load (KnownFields)")
	}
}

func TestLoadDoesNotValidateProfilePaths(t *testing.T) {
	// A deliberate decision: a broken profile blocks only itself, at use
	// time, never config loading -- otherwise every command would break.
	p := writeConfig(t, "profiles:\n  broken:\n    paths: [\"~\", \"relative/path\"]\n")

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom must not validate profile paths, got error: %v", err)
	}
	if len(cfg.Profiles["broken"].Paths) != 2 {
		t.Errorf("expected the broken paths to load verbatim, got %v", cfg.Profiles["broken"].Paths)
	}
}

func TestValidateProfilePathEntryRejectsBroadRoots(t *testing.T) {
	broad := []string{"/", "/Users", "/System", "/Library", "/Applications", "/private", "/Volumes", "~"}
	for _, raw := range broad {
		if _, err := validateProfilePathEntry(raw); err == nil {
			t.Errorf("validateProfilePathEntry(%q) = nil error, want a too-broad rejection", raw)
		}
	}

	if _, err := validateProfilePathEntry("relative/path"); err == nil {
		t.Error("expected a relative path to be rejected")
	}
	if _, err := validateProfilePathEntry(""); err == nil {
		t.Error("expected an empty path to be rejected")
	}
	if _, err := validateProfilePathEntry("/Users/vini/proj"); err != nil {
		t.Errorf("validateProfilePathEntry on a normal project path: %v", err)
	}
}

// --- resolution --------------------------------------------------------

func profileConfig(t *testing.T, profiles map[string]Profile, disabled []string) *Config {
	t.Helper()
	cfg, err := New(nil, disabled)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cfg.Profiles = profiles
	return cfg
}

func projectArtifactsCount(r *cleaner.Registry) int {
	n := 0
	for _, c := range r.All() {
		if c.Category() == cleaner.CategoryProjectArtifacts {
			n++
		}
	}
	return n
}

func TestResolveProfileUnknownName(t *testing.T) {
	cfg := profileConfig(t, map[string]Profile{"dev": {Categories: []string{"docker"}}}, nil)

	_, _, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "nope", false)
	if err == nil {
		t.Fatal("expected an error for an unknown profile")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the profile, got: %v", err)
	}
}

func TestResolveProfileEmptyProfileErrors(t *testing.T) {
	cfg := profileConfig(t, map[string]Profile{"dev": {}}, nil)

	// An empty category list means "everything" downstream -- the opposite
	// of what an empty profile should select.
	if _, _, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "dev", false); err == nil {
		t.Fatal("expected an error for an empty profile")
	}
}

func TestResolveProfileCategoriesOnlyKeepsBaseRegistry(t *testing.T) {
	cfg := profileConfig(t, map[string]Profile{
		"dev": {Categories: []string{"development-artifacts", "docker"}},
	}, nil)
	base := cleaner.DefaultRegistry()

	categories, registry, err := cfg.ResolveProfile(base, "dev", false)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(categories) != 2 || categories[0] != "development-artifacts" || categories[1] != "docker" {
		t.Errorf("categories = %v, want the profile's list in order", categories)
	}
	if registry != base {
		t.Error("with no paths, ResolveProfile should hand back the base registry unchanged")
	}
}

func TestResolveProfileBypassesDisabledCategories(t *testing.T) {
	cfg := profileConfig(t, map[string]Profile{
		"dev": {Categories: []string{"docker"}},
	}, []string{"docker"})

	categories, _, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "dev", false)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(categories) != 1 || categories[0] != "docker" {
		t.Errorf("categories = %v: a profile's categories must bypass disabled_categories, like explicit CLI args", categories)
	}
}

func TestResolveProfileSubstitutesProjectArtifactsCleaner(t *testing.T) {
	root := t.TempDir()
	junk := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(junk, "a.bin"), make([]byte, 64), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := profileConfig(t, map[string]Profile{
		"dev": {Categories: []string{"docker"}, Paths: []string{root}},
	}, nil)

	categories, registry, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "dev", false)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}

	wantCat := string(cleaner.CategoryProjectArtifacts)
	if len(categories) != 2 || categories[1] != wantCat {
		t.Fatalf("categories = %v, want docker followed by %s", categories, wantCat)
	}

	// Guards the Register pitfall: Register appends to the ordered slice
	// even when it replaces the byID entry.
	if got := projectArtifactsCount(registry); got != 1 {
		t.Fatalf("registry has %d project-artifacts cleaners, want exactly 1", got)
	}

	c, ok := registry.Get(cleaner.CategoryProjectArtifacts)
	if !ok {
		t.Fatal("resolved registry has no project-artifacts cleaner")
	}
	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Path != junk {
		t.Errorf("substituted cleaner scanned %+v, want just %s", result.Entries, junk)
	}

	// The base registry's own instance must stay inert.
	baseCleaner, _ := cleaner.DefaultRegistry().Get(cleaner.CategoryProjectArtifacts)
	baseResult, err := baseCleaner.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("base Scan: %v", err)
	}
	if len(baseResult.Entries) != 0 {
		t.Error("substitution must not mutate the default registry's cleaner")
	}
}

func TestResolveProfileDoesNotDuplicateProjectArtifactsCategory(t *testing.T) {
	cfg := profileConfig(t, map[string]Profile{
		"dev": {Categories: []string{"project-artifacts"}, Paths: []string{t.TempDir()}},
	}, nil)

	categories, _, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "dev", false)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if len(categories) != 1 {
		t.Errorf("categories = %v, want project-artifacts listed once", categories)
	}
}

func TestResolveProfileForwardsIncludeLargeFiles(t *testing.T) {
	root := t.TempDir()
	large := filepath.Join(root, "dataset.bin")
	if err := os.WriteFile(large, make([]byte, 64), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := profileConfig(t, map[string]Profile{"dev": {Paths: []string{root}}}, nil)

	for _, includeLargeFiles := range []bool{false, true} {
		_, registry, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "dev", includeLargeFiles)
		if err != nil {
			t.Fatalf("ResolveProfile: %v", err)
		}
		c, _ := registry.Get(cleaner.CategoryProjectArtifacts)
		pac, ok := c.(*cleaner.ProjectArtifactsCleaner)
		if !ok {
			t.Fatalf("resolved cleaner is %T, want *cleaner.ProjectArtifactsCleaner", c)
		}

		// Behavioral check: with the flag off, a file entry survives Clean.
		entries := []cleaner.FileEntry{{Path: large, Size: 64, Category: cleaner.CategoryProjectArtifacts}}
		result, err := pac.Clean(t.Context(), entries, true, nil)
		if err != nil {
			t.Fatalf("Clean: %v", err)
		}
		wantDeleted := 0
		if includeLargeFiles {
			wantDeleted = 1
		}
		if result.FilesDeleted != wantDeleted {
			t.Errorf("includeLargeFiles=%v: FilesDeleted = %d, want %d", includeLargeFiles, result.FilesDeleted, wantDeleted)
		}
	}
}

func TestResolveProfileRejectsBadPathAtUseTime(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home dir: %v", err)
	}

	for _, bad := range []string{home, "/", "relative/path"} {
		cfg := profileConfig(t, map[string]Profile{"dev": {Paths: []string{bad}}}, nil)
		if _, _, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "dev", false); err == nil {
			t.Errorf("ResolveProfile with paths=[%q] = nil error, want a rejection", bad)
		}
	}
}

func TestResolveProfileNilConfig(t *testing.T) {
	var cfg *Config
	if _, _, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "dev", false); err == nil {
		t.Fatal("expected an error resolving a profile with no config loaded")
	}
}
