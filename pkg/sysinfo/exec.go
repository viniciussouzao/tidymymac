package sysinfo

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// probeTimeout bounds how long any single subprocess call is allowed to run.
// These are purely informational probes, so they must never block the TUI.
const probeTimeout = 2 * time.Second

// maxOutputBytes caps how much output we parse from any probe, guarding
// against a pathological/huge response ballooning memory.
const maxOutputBytes = 64 * 1024

// runCommand executes an absolute-path binary with fixed arguments and
// returns its trimmed stdout. It never invokes a shell. Any failure (missing
// binary, non-zero exit, timeout, empty output) returns ok=false rather than
// an error, since callers only ever want to know "did we get output".
func runCommand(ctx context.Context, path string, args ...string) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, path, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}

	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes]
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", false
	}

	return trimmed, true
}
