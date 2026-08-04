package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

func loadProfiles(t *testing.T, p string) map[string]Profile {
	t.Helper()
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom(%s): %v", p, err)
	}
	return cfg.Profiles
}

// --- create / delete ---------------------------------------------------

func TestCreateProfileAt_NewFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.yaml")

	if err := createProfileAt(p, "dev"); err != nil {
		t.Fatalf("createProfileAt: %v", err)
	}

	profiles := loadProfiles(t, p)
	if _, ok := profiles["dev"]; !ok {
		t.Fatalf("profile dev missing after create, got %+v", profiles)
	}
	if len(profiles["dev"].Categories) != 0 || len(profiles["dev"].Paths) != 0 {
		t.Errorf("a fresh profile must be empty, got %+v", profiles["dev"])
	}
}

func TestCreateProfileAt_DuplicateIsAnError(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker]\n")
	before := readFile(t, p)

	if err := createProfileAt(p, "dev"); err == nil {
		t.Fatal("expected an error creating an already-existing profile")
	}
	if readFile(t, p) != before {
		t.Error("a rejected create must not modify the file")
	}
}

func TestCreateProfileAt_RejectsEmptyName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")

	if err := createProfileAt(p, "  "); err == nil {
		t.Fatal("expected an error for a blank profile name")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("expected no file to be created for a rejected name")
	}
}

func TestCreateProfileAt_PreservesOtherSettings(t *testing.T) {
	p := writeConfig(t, "protected_paths:\n  - /a # keep this one\ndisabled_categories:\n  - docker\n")

	if err := createProfileAt(p, "dev"); err != nil {
		t.Fatalf("createProfileAt: %v", err)
	}

	after := readFile(t, p)
	if !strings.Contains(after, "keep this one") {
		t.Errorf("hand-written comments must survive the edit, got:\n%s", after)
	}
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if !cfg.IsProtected("/a") || !cfg.IsCategoryDisabled("docker") {
		t.Error("existing settings must survive profile creation")
	}
}

func TestDeleteProfileAt_RemovesProfile(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker]\n  ops:\n    categories: [logs]\n")

	removed, err := deleteProfileAt(p, "dev")
	if err != nil {
		t.Fatalf("deleteProfileAt: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}

	profiles := loadProfiles(t, p)
	if _, ok := profiles["dev"]; ok {
		t.Error("dev should be gone")
	}
	if _, ok := profiles["ops"]; !ok {
		t.Error("ops must survive")
	}
}

func TestDeleteProfileAt_LastProfileDropsTheKey(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker]\n")

	if _, err := deleteProfileAt(p, "dev"); err != nil {
		t.Fatalf("deleteProfileAt: %v", err)
	}

	if strings.Contains(readFile(t, p), "profiles") {
		t.Errorf("expected the empty profiles key to be dropped, got:\n%s", readFile(t, p))
	}
	if len(loadProfiles(t, p)) != 0 {
		t.Error("expected no profiles left")
	}
}

func TestDeleteProfileAt_MissingIsNoopNotError(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker]\n")
	before := readFile(t, p)

	removed, err := deleteProfileAt(p, "nope")
	if err != nil {
		t.Fatalf("deleteProfileAt: %v", err)
	}
	if removed {
		t.Error("expected removed = false for a profile that never existed")
	}
	if readFile(t, p) != before {
		t.Error("a no-op delete must not modify the file")
	}
}

// --- categories --------------------------------------------------------

func TestAddProfileCategoryAt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := createProfileAt(p, "dev"); err != nil {
		t.Fatalf("createProfileAt: %v", err)
	}

	if err := addProfileCategoryAt(p, "dev", "development-artifacts"); err != nil {
		t.Fatalf("addProfileCategoryAt: %v", err)
	}
	if err := addProfileCategoryAt(p, "dev", "docker"); err != nil {
		t.Fatalf("addProfileCategoryAt: %v", err)
	}

	got := loadProfiles(t, p)["dev"].Categories
	if len(got) != 2 || got[0] != "development-artifacts" || got[1] != "docker" {
		t.Errorf("categories = %v, want [development-artifacts docker] in insertion order", got)
	}
}

func TestAddProfileCategoryAt_DedupesWithoutRewriting(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker]\n")
	before := readFile(t, p)

	if err := addProfileCategoryAt(p, "dev", "docker"); err != nil {
		t.Fatalf("addProfileCategoryAt: %v", err)
	}
	if readFile(t, p) != before {
		t.Error("adding an already-listed category must not rewrite the file")
	}
}

func TestAddProfileCategoryAt_UnknownProfile(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker]\n")

	err := addProfileCategoryAt(p, "nope", "docker")
	if err == nil {
		t.Fatal("expected an error adding to a profile that doesn't exist")
	}
	if !strings.Contains(err.Error(), "profile create") {
		t.Errorf("error should point at 'profile create', got: %v", err)
	}
}

func TestRemoveProfileCategoryAt(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker, logs]\n")

	removed, err := removeProfileCategoryAt(p, "dev", "docker")
	if err != nil {
		t.Fatalf("removeProfileCategoryAt: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}

	got := loadProfiles(t, p)["dev"].Categories
	if len(got) != 1 || got[0] != "logs" {
		t.Errorf("categories = %v, want [logs]", got)
	}
}

func TestRemoveProfileCategoryAt_LastOneDropsTheKey(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker]\n    paths: [/Users/vini/proj]\n")

	if _, err := removeProfileCategoryAt(p, "dev", "docker"); err != nil {
		t.Fatalf("removeProfileCategoryAt: %v", err)
	}

	profile := loadProfiles(t, p)["dev"]
	if len(profile.Categories) != 0 {
		t.Errorf("categories = %v, want empty", profile.Categories)
	}
	if len(profile.Paths) != 1 {
		t.Errorf("paths = %v, want the untouched entry to survive", profile.Paths)
	}
}

func TestRemoveProfileCategoryAt_NotListedIsNoop(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    categories: [docker]\n")
	before := readFile(t, p)

	removed, err := removeProfileCategoryAt(p, "dev", "logs")
	if err != nil {
		t.Fatalf("removeProfileCategoryAt: %v", err)
	}
	if removed {
		t.Error("expected removed = false")
	}
	if readFile(t, p) != before {
		t.Error("a no-op remove must not modify the file")
	}
}

// --- paths -------------------------------------------------------------

func TestAddProfilePathAt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := createProfileAt(p, "dev"); err != nil {
		t.Fatalf("createProfileAt: %v", err)
	}

	if err := addProfilePathAt(p, "dev", "/Users/vini/proj"); err != nil {
		t.Fatalf("addProfilePathAt: %v", err)
	}

	got := loadProfiles(t, p)["dev"].Paths
	if len(got) != 1 || got[0] != "/Users/vini/proj" {
		t.Errorf("paths = %v, want [/Users/vini/proj] stored verbatim", got)
	}
}

func TestAddProfilePathAt_RejectsBroadRootBeforeWriting(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev: {}\n")
	before := readFile(t, p)

	for _, bad := range []string{"~", "/", "relative/path", ""} {
		if err := addProfilePathAt(p, "dev", bad); err == nil {
			t.Errorf("addProfilePathAt(%q) = nil error, want a rejection", bad)
		}
	}
	if readFile(t, p) != before {
		t.Error("a rejected path must not modify the file")
	}
}

func TestAddProfilePathAt_DedupesByNormalizedEquivalence(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home dir: %v", err)
	}
	target := filepath.Join(home, "proj")
	p := writeConfig(t, "profiles:\n  dev:\n    paths: [\""+target+"\"]\n")
	before := readFile(t, p)

	if err := addProfilePathAt(p, "dev", "~/proj"); err != nil {
		t.Fatalf("addProfilePathAt: %v", err)
	}
	if readFile(t, p) != before {
		t.Error("adding an equivalent path must not rewrite the file")
	}
}

func TestAddProfilePathAt_UnknownProfile(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev: {}\n")

	if err := addProfilePathAt(p, "nope", "/Users/vini/proj"); err == nil {
		t.Fatal("expected an error adding a path to a profile that doesn't exist")
	}
}

func TestRemoveProfilePathAt_ByNormalizedEquivalence(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home dir: %v", err)
	}
	target := filepath.Join(home, "proj")
	p := writeConfig(t, "profiles:\n  dev:\n    paths: [\""+target+"\", /Users/vini/other]\n")

	removed, err := removeProfilePathAt(p, "dev", "~/proj")
	if err != nil {
		t.Fatalf("removeProfilePathAt: %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true for a normalized-equivalent path")
	}

	got := loadProfiles(t, p)["dev"].Paths
	if len(got) != 1 || got[0] != "/Users/vini/other" {
		t.Errorf("paths = %v, want only the untouched entry", got)
	}
}

func TestRemoveProfilePathAt_NotListedIsNoop(t *testing.T) {
	p := writeConfig(t, "profiles:\n  dev:\n    paths: [/Users/vini/proj]\n")
	before := readFile(t, p)

	removed, err := removeProfilePathAt(p, "dev", "/Users/vini/other")
	if err != nil {
		t.Fatalf("removeProfilePathAt: %v", err)
	}
	if removed {
		t.Error("expected removed = false")
	}
	if readFile(t, p) != before {
		t.Error("a no-op remove must not modify the file")
	}
}

// --- round trip --------------------------------------------------------

func TestProfileWriteRoundTripThroughEveryCommand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	root := t.TempDir()

	if err := createProfileAt(p, "dev"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := addProfileCategoryAt(p, "dev", "docker"); err != nil {
		t.Fatalf("add-category: %v", err)
	}
	if err := addProfilePathAt(p, "dev", root); err != nil {
		t.Fatalf("add-path: %v", err)
	}

	profile := loadProfiles(t, p)["dev"]
	if len(profile.Categories) != 1 || len(profile.Paths) != 1 {
		t.Fatalf("profile = %+v, want one category and one path", profile)
	}

	// The written file must survive a resolve, not just a load.
	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if _, _, err := cfg.ResolveProfile(cleaner.DefaultRegistry(), "dev", false); err != nil {
		t.Fatalf("ResolveProfile on a freshly-written profile: %v", err)
	}

	if _, err := removeProfileCategoryAt(p, "dev", "docker"); err != nil {
		t.Fatalf("remove-category: %v", err)
	}
	if _, err := removeProfilePathAt(p, "dev", root); err != nil {
		t.Fatalf("remove-path: %v", err)
	}

	profile = loadProfiles(t, p)["dev"]
	if len(profile.Categories) != 0 || len(profile.Paths) != 0 {
		t.Errorf("profile = %+v, want empty after removing everything", profile)
	}

	removed, err := deleteProfileAt(p, "dev")
	if err != nil || !removed {
		t.Fatalf("delete: removed=%v err=%v", removed, err)
	}
	if len(loadProfiles(t, p)) != 0 {
		t.Error("expected no profiles left")
	}
}
