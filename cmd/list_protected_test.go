package cmd

import (
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/config"
)

func TestReturnProtected_ShowsBuiltinsMarkedAndUserPathsPlain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	prev := loadedConfig
	loadedConfig = cfg
	t.Cleanup(func() { loadedConfig = prev })

	out := returnProtected()

	for _, want := range []string{
		"~/.ollama/models",
		"Ollama local models",
		"~/.cache/huggingface",
		"Hugging Face Hub cache",
		"/Users/vini/Secrets",
		"cannot be removed with tidymymac unprotect",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// The user's own entry must not be labeled built-in.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "/Users/vini/Secrets") && strings.Contains(line, "built-in") {
			t.Errorf("user-configured path wrongly marked built-in: %q", line)
		}
	}
}
