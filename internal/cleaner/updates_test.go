package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestUpdatesCleanerScanRecordsEachFilesOwnPath locks in the fix for a bug
// where the WalkDir callback recorded the walk ROOT as every entry's Path: the
// callback's own parameter was shadowed by the enclosing loop's `path`. The
// consequence was not merely cosmetic -- Clean would have been handed the
// Updates directory itself, repeated once per file.
func TestUpdatesCleanerScanRecordsEachFilesOwnPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	updates := filepath.Join(home, "Library", "Updates")
	if err := os.MkdirAll(filepath.Join(updates, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	first := filepath.Join(updates, "installer.pkg")
	second := filepath.Join(updates, "nested", "residue.dmg")
	if err := os.WriteFile(first, make([]byte, 10), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(second, make([]byte, 25), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := NewUpdatesCleaner()
	if c.homeDir != home {
		t.Fatalf("homeDir = %q, want the fake home %q", c.homeDir, home)
	}

	result, err := c.Scan(context.Background(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if len(result.Entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(result.Entries), result.Entries)
	}

	bySize := map[string]int64{}
	paths := make([]string, 0, len(result.Entries))
	for _, e := range result.Entries {
		if e.Path == updates {
			t.Fatalf("entry Path is the walk root %q, want the file itself", e.Path)
		}
		if e.IsDir {
			t.Errorf("entry %q is marked IsDir, but only files are collected", e.Path)
		}
		if e.Category != CategoryUpdates {
			t.Errorf("entry %q category = %q, want %q", e.Path, e.Category, CategoryUpdates)
		}
		paths = append(paths, e.Path)
		bySize[e.Path] = e.Size
	}

	sort.Strings(paths)
	want := []string{first, second}
	sort.Strings(want)
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}

	if bySize[first] != 10 || bySize[second] != 25 {
		t.Fatalf("sizes = %v, want 10 and 25", bySize)
	}
	if result.TotalFiles != 2 || result.TotalSize != 35 {
		t.Fatalf("totals = %d files / %d bytes, want 2 / 35", result.TotalFiles, result.TotalSize)
	}
}
