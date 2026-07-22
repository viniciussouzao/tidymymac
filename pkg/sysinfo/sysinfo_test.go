package sysinfo

import (
	"context"
	"testing"
	"time"
)

func TestParseChipBrand(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "valid", raw: "Apple M2 Pro", want: "Apple M2 Pro", ok: true},
		{name: "empty", raw: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseChipBrand(tt.raw)
			if ok != tt.ok || got != tt.want {
				t.Errorf("parseChipBrand(%q) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParseMemSize(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int64
		ok   bool
	}{
		{name: "valid", raw: "34359738368", want: 34359738368, ok: true},
		{name: "empty", raw: "", ok: false},
		{name: "whitespace only", raw: "   ", ok: false},
		{name: "non-numeric", raw: "not-a-number", ok: false},
		{name: "zero", raw: "0", ok: false},
		{name: "negative", raw: "-1", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseMemSize(tt.raw)
			if ok != tt.ok || got != tt.want {
				t.Errorf("parseMemSize(%q) = (%d, %v), want (%d, %v)", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestGatherPreExpiredContext confirms Gather returns promptly with
// everything unknown when the context is already canceled, and never
// panics regardless of which probes would otherwise succeed.
func TestGatherPreExpiredContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan Info, 1)
	go func() { done <- Gather(ctx) }()

	select {
	case info := <-done:
		if info.ChipKnown || info.MemoryKnown || info.BatteryKnown || info.CycleCountKnown || info.OSVersionKnown {
			t.Errorf("expected all-unknown Info on pre-expired context, got %+v", info)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Gather did not return promptly on a pre-expired context")
	}
}
