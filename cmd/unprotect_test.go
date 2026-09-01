package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/config"
)

// runUnprotect executes the unprotect command against a sandboxed HOME (so it
// only ever touches a temp config file, never the developer's real one) and
// returns its stdout.
func runUnprotect(t *testing.T, path string) string {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	prev := loadedConfig
	loadedConfig = cfg
	t.Cleanup(func() { loadedConfig = prev })

	var out bytes.Buffer
	unprotectCmd.SetOut(&out)
	t.Cleanup(func() { unprotectCmd.SetOut(nil) })
	if err := unprotectCmd.Flags().Set("path", path); err != nil {
		t.Fatalf("setting --path: %v", err)
	}
	t.Cleanup(func() { _ = unprotectCmd.Flags().Set("path", "") })

	if err := unprotectCmd.RunE(unprotectCmd, nil); err != nil {
		t.Fatalf("unprotect RunE: %v", err)
	}
	return out.String()
}

func sandboxHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestUnprotect_BuiltinPathReportsBuiltinNotUnprotected(t *testing.T) {
	sandboxHome(t)

	out := runUnprotect(t, "~/.ollama/models")

	if !strings.Contains(out, "built-in") {
		t.Errorf("expected a built-in-specific message, got %q", out)
	}
	if strings.Contains(out, "not protected") {
		t.Errorf("a built-in path must never be reported as unprotected, got %q", out)
	}
}

func TestUnprotect_UnknownPathReportsNotProtected(t *testing.T) {
	sandboxHome(t)

	out := runUnprotect(t, "/Users/vini/Downloads")

	if !strings.Contains(out, "not protected") {
		t.Errorf("expected the not-protected message, got %q", out)
	}
	if strings.Contains(out, "built-in") {
		t.Errorf("an unprotected path must not be reported as built-in, got %q", out)
	}
}

func TestUnprotect_RemovesUserPath(t *testing.T) {
	home := sandboxHome(t)

	cfgPath := filepath.Join(home, ".tidymymac", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte("protected_paths:\n  - /Users/vini/Secrets\n"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	out := runUnprotect(t, "/Users/vini/Secrets")

	if !strings.Contains(out, "unprotected:") {
		t.Errorf("expected the removal message, got %q", out)
	}
	if strings.Contains(out, "built-in") {
		t.Errorf("a user path must not be reported as built-in, got %q", out)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.IsProtected("/Users/vini/Secrets") {
		t.Error("expected the user entry to be gone after unprotect")
	}
}
