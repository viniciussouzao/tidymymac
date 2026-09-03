package config

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeHome points HOME at a temp dir so the tilde-based built-in protected
// paths resolve somewhere writable and predictable, instead of depending on
// whatever the developer running the tests happens to have in their real home.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func builtinTargets(home string) []string {
	return []string{
		filepath.Join(home, ".ollama", "models"),
		filepath.Join(home, ".cache", "huggingface"),
	}
}

func TestBuiltinProtected_AppliesWithNoConfigFile(t *testing.T) {
	home := fakeHome(t)

	// The regression this guards: loadFrom's missing-file early return used to
	// skip normalize entirely, so the built-ins never got installed for the
	// users most likely to need them (those who never wrote a config file).
	cfg, err := loadFrom(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}

	for _, target := range builtinTargets(home) {
		if !cfg.IsProtected(target) {
			t.Errorf("expected %q to be protected with no config file present", target)
		}
	}
}

func TestBuiltinProtected_AppliesWithEmptyConfigFile(t *testing.T) {
	home := fakeHome(t)

	for _, contents := range []string{"", "   \n\n"} {
		cfg, err := loadFrom(writeConfig(t, contents))
		if err != nil {
			t.Fatalf("loadFrom(%q): %v", contents, err)
		}
		for _, target := range builtinTargets(home) {
			if !cfg.IsProtected(target) {
				t.Errorf("config %q: expected %q to be protected", contents, target)
			}
		}
	}
}

func TestBuiltinProtected_NewIncludesBuiltins(t *testing.T) {
	home := fakeHome(t)

	cfg, err := New(nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, target := range builtinTargets(home) {
		if !cfg.IsProtected(target) {
			t.Errorf("expected New() config to protect %q", target)
		}
	}
}

func TestBuiltinProtected_NestedPaths(t *testing.T) {
	home := fakeHome(t)

	cfg, err := New(nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(home, ".ollama", "models"), true},
		{filepath.Join(home, ".ollama", "models", "blobs", "sha256-abc"), true},
		{filepath.Join(home, ".ollama", "MODELS", "manifests", "registry.ollama.ai"), true},
		{filepath.Join(home, ".cache", "huggingface", "hub", "models--meta-llama", "blob.bin"), true},
		{filepath.Join(home, ".cache", "huggingface", "xet", "chunk-cache", "x"), true},
		// Siblings sharing a string prefix must not match.
		{filepath.Join(home, ".ollama", "models-old", "blob"), false},
		{filepath.Join(home, ".cache", "huggingface-old"), false},
		// Neighbouring, genuinely cleanable directories stay cleanable.
		{filepath.Join(home, ".ollama", "history"), false},
		{filepath.Join(home, ".cache", "pip"), false},
	}

	for _, tc := range tests {
		if got := cfg.IsProtected(tc.path); got != tc.want {
			t.Errorf("IsProtected(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestBuiltinProtected_ResolvesThroughSymlink(t *testing.T) {
	home := fakeHome(t)

	// ~/.ollama/models is a symlink to a big external-ish store: the real
	// target must be protected too, exactly like a user-configured path.
	realStore := filepath.Join(t.TempDir(), "ollama-store")
	if err := os.MkdirAll(realStore, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ollama"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(realStore, filepath.Join(home, ".ollama", "models")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg, err := New(nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resolved, err := filepath.EvalSymlinks(realStore)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if !cfg.IsProtected(filepath.Join(resolved, "blobs", "sha256-abc")) {
		t.Errorf("expected the symlink target %q to be protected", resolved)
	}
	if !cfg.IsProtected(filepath.Join(home, ".ollama", "models", "blobs")) {
		t.Error("expected the symlink path itself to stay protected")
	}
}

func TestBuiltinProtected_ContainsProtected(t *testing.T) {
	home := fakeHome(t)

	cfg, err := New(nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(home, ".ollama"), true},
		{filepath.Join(home, ".cache"), true},
		{home, true},
		// The built-in root itself is not strictly inside itself.
		{filepath.Join(home, ".cache", "huggingface"), false},
		{filepath.Join(home, ".cache", "pip"), false},
	}

	for _, tc := range tests {
		if got := cfg.ContainsProtected(tc.path); got != tc.want {
			t.Errorf("ContainsProtected(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestBuiltinProtected_UnionWithUserConfig(t *testing.T) {
	home := fakeHome(t)

	cfg, err := New([]string{"/Users/vini/Secrets", "~/.ollama/models"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !cfg.IsProtected("/Users/vini/Secrets/file.txt") {
		t.Error("user-configured path must stay protected alongside the built-ins")
	}
	for _, target := range builtinTargets(home) {
		if !cfg.IsProtected(target) {
			t.Errorf("expected built-in %q to remain protected", target)
		}
	}

	// The redundant user entry duplicating a built-in must dedup away, and
	// keep its built-in origin (built-ins are processed first).
	seen := make(map[string]int, len(cfg.normalizedProtected))
	for _, root := range cfg.normalizedProtected {
		seen[root.key]++
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("normalized protected root %q appears %d times, want 1", key, n)
		}
	}
	if !cfg.IsBuiltinProtected(filepath.Join(home, ".ollama", "models", "blobs")) {
		t.Error("a built-in duplicated by the user config must keep its built-in origin")
	}
}

func TestIsBuiltinProtected(t *testing.T) {
	home := fakeHome(t)

	cfg, err := New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(home, ".ollama", "models"), true},
		{filepath.Join(home, ".ollama", "models", "blobs", "sha256-abc"), true},
		{filepath.Join(home, ".cache", "huggingface"), true},
		// Raw, user-typed CLI form: "tidymymac unprotect --path ~/..." must
		// still be recognized as a built-in.
		{"~/.ollama/models", true},
		{"~/.cache/huggingface/hub", true},
		{"~/Downloads", false},
		{"/Users/vini/Secrets", false},          // user-configured, not built-in
		{"/Users/vini/Secrets/file.txt", false}, // ditto, nested
		{"/Users/vini/Downloads", false},        // not protected at all
		{filepath.Join(home, ".ollama"), false}, // parent, not under the root
	}

	for _, tc := range tests {
		if got := cfg.IsBuiltinProtected(tc.path); got != tc.want {
			t.Errorf("IsBuiltinProtected(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestIsBuiltinProtected_NilSafe(t *testing.T) {
	var cfg *Config
	if cfg.IsBuiltinProtected("/anything") {
		t.Error("IsBuiltinProtected on a nil Config must be false")
	}
}

func TestProtectedPathEntries(t *testing.T) {
	cfg, err := New([]string{"~/Secrets"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	entries := cfg.ProtectedPathEntries()
	if len(entries) != len(builtinProtectedPaths)+1 {
		t.Fatalf("len(entries) = %d, want %d", len(entries), len(builtinProtectedPaths)+1)
	}

	for i, b := range builtinProtectedPaths {
		got := entries[i]
		if !got.Builtin {
			t.Errorf("entry %d (%s): Builtin = false, want true", i, got.Path)
		}
		if got.Path != b.path {
			t.Errorf("entry %d: Path = %q, want %q", i, got.Path, b.path)
		}
		if got.Reason != b.reason {
			t.Errorf("entry %d: Reason = %q, want %q", i, got.Reason, b.reason)
		}
	}

	last := entries[len(entries)-1]
	if last.Builtin || last.Path != "~/Secrets" || last.Reason != "" {
		t.Errorf("user entry = %+v, want {Path:~/Secrets Builtin:false Reason:}", last)
	}
}

func TestProtectedPathEntries_NilConfigStillListsBuiltins(t *testing.T) {
	var cfg *Config
	entries := cfg.ProtectedPathEntries()
	if len(entries) != len(builtinProtectedPaths) {
		t.Fatalf("len(entries) = %d, want %d", len(entries), len(builtinProtectedPaths))
	}
	for _, e := range entries {
		if !e.Builtin {
			t.Errorf("entry %q should be marked built-in", e.Path)
		}
	}
}

func TestRemoveProtectedPathAt_BuiltinIsNotInUserFile(t *testing.T) {
	fakeHome(t)

	// cmd/unprotect relies on this: a built-in is never present in the user's
	// file, so removal is a (false, nil) no-op and the command falls back to
	// the "built-in, cannot be removed" message.
	p := writeConfig(t, `protected_paths: ["/Users/vini/Secrets"]`)
	removed, err := removeProtectedPathAt(p, "~/.ollama/models")
	if err != nil {
		t.Fatalf("removeProtectedPathAt: %v", err)
	}
	if removed {
		t.Error("removing a built-in path must report removed=false")
	}

	cfg, err := loadFrom(p)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if !cfg.IsProtected(filepath.Join(os.Getenv("HOME"), ".ollama", "models")) {
		t.Error("the built-in must remain protected after an unprotect attempt")
	}
	if !cfg.IsProtected("/Users/vini/Secrets") {
		t.Error("the untouched user entry must remain protected")
	}
}
