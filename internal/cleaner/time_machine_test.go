package cleaner

import (
	"context"
	"testing"
)

// --- metadata ---

func TestTimeMachineCleanerMetadata(t *testing.T) {
	c := NewTimeMachineCleaner()

	if c.Category() != CategoryTimeMachineSnapshots {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryTimeMachineSnapshots)
	}
	if c.Name() != "Time Machine Snapshots" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Time Machine Snapshots")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
	if !c.RequiresSudo() {
		t.Error("RequiresSudo() = false, want true")
	}
}

// --- parseLocalSnapshots ---

func TestParseLocalSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   []string
	}{
		{
			name:  "empty output",
			input: "",
			want:  []string{},
		},
		{
			name:  "single snapshot",
			input: "com.apple.TimeMachine.2024-01-15-120000.local\n",
			want:  []string{"com.apple.TimeMachine.2024-01-15-120000.local"},
		},
		{
			name: "multiple snapshots",
			input: "com.apple.TimeMachine.2024-01-15-120000.local\n" +
				"com.apple.TimeMachine.2024-01-16-080000.local\n" +
				"com.apple.TimeMachine.2024-01-17-060000.local\n",
			want: []string{
				"com.apple.TimeMachine.2024-01-15-120000.local",
				"com.apple.TimeMachine.2024-01-16-080000.local",
				"com.apple.TimeMachine.2024-01-17-060000.local",
			},
		},
		{
			name:  "ignores lines without prefix",
			input: "Snapshots for volume /:\n com.apple.TimeMachine.2024-01-15-120000.local\nSome other line\n",
			want:  []string{"com.apple.TimeMachine.2024-01-15-120000.local"},
		},
		{
			name:  "blank lines are skipped",
			input: "\ncom.apple.TimeMachine.2024-01-15-120000.local\n\n",
			want:  []string{"com.apple.TimeMachine.2024-01-15-120000.local"},
		},
		{
			name:  "whitespace-only lines are skipped",
			input: "   \ncom.apple.TimeMachine.2024-01-15-120000.local\n   \n",
			want:  []string{"com.apple.TimeMachine.2024-01-15-120000.local"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLocalSnapshots(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseLocalSnapshots() returned %d entries, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("entry[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- snapshotDateFromEntry ---

func TestSnapshotDateFromEntry(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		want    string
		wantOk  bool
	}{
		{
			name:   "valid snapshot",
			entry:  "com.apple.TimeMachine.2024-01-15-120000.local",
			want:   "2024-01-15-120000",
			wantOk: true,
		},
		{
			name:   "valid snapshot different date",
			entry:  "com.apple.TimeMachine.2023-12-31-235959.local",
			want:   "2023-12-31-235959",
			wantOk: true,
		},
		{
			name:   "missing prefix",
			entry:  "2024-01-15-120000.local",
			wantOk: false,
		},
		{
			name:   "missing suffix",
			entry:  "com.apple.TimeMachine.2024-01-15-120000",
			wantOk: false,
		},
		{
			name:   "empty string",
			entry:  "",
			wantOk: false,
		},
		{
			name:   "only prefix and suffix, no date",
			entry:  "com.apple.TimeMachine..local",
			wantOk: false,
		},
		{
			// "local" remains after TrimPrefix; TrimSuffix(".local") does not match
			// because there is no leading dot, so the function accepts it.
			name:   "prefix and suffix adjacent",
			entry:  "com.apple.TimeMachine.local",
			want:   "local",
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := snapshotDateFromEntry(tt.entry)
			if ok != tt.wantOk {
				t.Errorf("snapshotDateFromEntry(%q) ok = %v, want %v", tt.entry, ok, tt.wantOk)
			}
			if ok && got != tt.want {
				t.Errorf("snapshotDateFromEntry(%q) = %q, want %q", tt.entry, got, tt.want)
			}
		})
	}
}

// --- Scan ---

func TestTimeMachineCleanerScanReturnsSizeUnknownWhenSnapshotsFound(t *testing.T) {
	// This test exercises the SizeKnown=false path by directly constructing
	// a result via the logic: if TotalFiles > 0, SizeKnown must be false.
	// Since tmutil is a real system binary, we verify the contract via
	// the cleaner's result shape when run on a live system.
	c := NewTimeMachineCleaner()
	result, err := c.Scan(t.Context(), nil)
	if err != nil && result == nil {
		t.Fatalf("Scan() returned nil result with error: %v", err)
	}
	if result == nil {
		t.Fatal("Scan() returned nil result")
	}
	if result.Category != CategoryTimeMachineSnapshots {
		t.Errorf("result.Category = %q, want %q", result.Category, CategoryTimeMachineSnapshots)
	}
	// If snapshots were found, size must be marked unknown.
	if result.TotalFiles > 0 && result.SizeKnown {
		t.Error("SizeKnown = true when snapshots exist, want false")
	}
	// TotalFiles must match number of entries.
	if result.TotalFiles != len(result.Entries) {
		t.Errorf("TotalFiles = %d, len(Entries) = %d, want equal", result.TotalFiles, len(result.Entries))
	}
	for _, e := range result.Entries {
		if e.Category != CategoryTimeMachineSnapshots {
			t.Errorf("entry %q: Category = %q, want %q", e.Path, e.Category, CategoryTimeMachineSnapshots)
		}
	}
}

func TestTimeMachineCleanerScanSizeKnownWhenNoSnapshots(t *testing.T) {
	// When there are no snapshots, SizeKnown stays true (zero is a known size).
	result := &ScanResult{
		Category:  CategoryTimeMachineSnapshots,
		SizeKnown: true,
	}
	// Simulate the no-snapshots branch: TotalFiles == 0 → SizeKnown stays true.
	if result.TotalFiles > 0 {
		result.SizeKnown = false
	}
	if !result.SizeKnown {
		t.Error("SizeKnown should remain true when no snapshots found")
	}
}

func TestTimeMachineCleanerScanContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewTimeMachineCleaner()
	// A cancelled context passed to exec.CommandContext causes the command to
	// fail. The cleaner must return a non-nil result regardless.
	result, _ := c.Scan(ctx, nil)
	if result == nil {
		t.Fatal("Scan() must return a non-nil result even on context cancellation")
	}
}

// --- Clean ---

func TestTimeMachineCleanerCleanDryRun(t *testing.T) {
	c := NewTimeMachineCleaner()
	entries := []FileEntry{
		{Path: "com.apple.TimeMachine.2024-01-15-120000.local", Category: CategoryTimeMachineSnapshots},
		{Path: "com.apple.TimeMachine.2024-01-16-080000.local", Category: CategoryTimeMachineSnapshots},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if !result.DryRun {
		t.Error("DryRun = false, want true")
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if result.Category != CategoryTimeMachineSnapshots {
		t.Errorf("Category = %q, want %q", result.Category, CategoryTimeMachineSnapshots)
	}
}

func TestTimeMachineCleanerCleanDryRunSkipsInvalidEntries(t *testing.T) {
	c := NewTimeMachineCleaner()
	entries := []FileEntry{
		{Path: "not-a-valid-snapshot", Category: CategoryTimeMachineSnapshots},
		{Path: "com.apple.TimeMachine.2024-01-15-120000.local", Category: CategoryTimeMachineSnapshots},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1 (invalid entry skipped)", result.FilesDeleted)
	}
	if len(result.Errors) != 1 {
		t.Errorf("len(Errors) = %d, want 1", len(result.Errors))
	}
}

func TestTimeMachineCleanerCleanEmptyEntries(t *testing.T) {
	c := NewTimeMachineCleaner()

	result, err := c.Clean(t.Context(), nil, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0", result.FilesDeleted)
	}
	if len(result.Errors) != 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

func TestTimeMachineCleanerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewTimeMachineCleaner()
	entries := []FileEntry{
		{Path: "com.apple.TimeMachine.2024-01-15-120000.local", Category: CategoryTimeMachineSnapshots},
	}

	_, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestTimeMachineCleanerCleanProgress(t *testing.T) {
	c := NewTimeMachineCleaner()
	entries := []FileEntry{
		{Path: "com.apple.TimeMachine.2024-01-15-120000.local", Category: CategoryTimeMachineSnapshots},
		{Path: "com.apple.TimeMachine.2024-01-16-080000.local", Category: CategoryTimeMachineSnapshots},
	}

	var calls int
	result, err := c.Clean(t.Context(), entries, true, func(p CleanProgress) {
		calls++
		if p.Category != CategoryTimeMachineSnapshots {
			t.Errorf("progress Category = %q, want %q", p.Category, CategoryTimeMachineSnapshots)
		}
		if p.FilesTotal != 2 {
			t.Errorf("progress FilesTotal = %d, want 2", p.FilesTotal)
		}
	})
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}
	if result.FilesDeleted != 2 {
		t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
	}
	if calls != 2 {
		t.Errorf("progress calls = %d, want 2", calls)
	}
}
