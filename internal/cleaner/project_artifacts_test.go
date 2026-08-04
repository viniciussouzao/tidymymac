package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// writeFileOfSize creates a file of exactly size bytes at path, creating
// parent directories as needed.
func writeFileOfSize(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}

// scanPaths returns the entry paths found by scanning roots, sorted.
func scanPaths(t *testing.T, c *ProjectArtifactsCleaner) []string {
	t.Helper()
	result, err := c.Scan(context.Background(), nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	paths := make([]string, 0, len(result.Entries))
	for _, e := range result.Entries {
		paths = append(paths, e.Path)
	}
	sort.Strings(paths)
	return paths
}

func TestProjectArtifactsScanNoPathsIsInert(t *testing.T) {
	c := NewProjectArtifactsCleaner(nil, 0, false)

	result, err := c.Scan(context.Background(), nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Entries) != 0 || result.TotalFiles != 0 || result.TotalSize != 0 {
		t.Errorf("Scan with no paths returned %d entries (%d bytes), want empty", len(result.Entries), result.TotalSize)
	}
	if result.Category != CategoryProjectArtifacts {
		t.Errorf("Category = %q, want %q", result.Category, CategoryProjectArtifacts)
	}
}

func TestProjectArtifactsScanMissingRootIsNotAnError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	c := NewProjectArtifactsCleaner([]string{root}, 0, false)

	result, err := c.Scan(context.Background(), nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("Scan returned %d entries for a missing root, want 0", len(result.Entries))
	}
	if len(result.Errors) != 0 {
		t.Errorf("Scan recorded %d errors for a missing root, want 0", len(result.Errors))
	}
}

func TestProjectArtifactsScanDetectsEveryJunkDirName(t *testing.T) {
	for name := range projectArtifactsJunkDirs {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			junk := filepath.Join(root, "app", name)
			writeFileOfSize(t, filepath.Join(junk, "chunk.bin"), 128)

			got := scanPaths(t, NewProjectArtifactsCleaner([]string{root}, 0, false))
			if len(got) != 1 || got[0] != junk {
				t.Fatalf("Scan found %v, want exactly [%s]", got, junk)
			}
		})
	}
}

func TestProjectArtifactsScanRecordsJunkDirAsWholeUnit(t *testing.T) {
	root := t.TempDir()
	outer := filepath.Join(root, "node_modules")
	writeFileOfSize(t, filepath.Join(outer, "pkg", "index.js"), 64)
	// A junk dir nested inside a junk dir must NOT produce a second entry:
	// the outer one is already recorded as a whole unit.
	mkdirAll(t, filepath.Join(outer, "pkg", "dist"))

	got := scanPaths(t, NewProjectArtifactsCleaner([]string{root}, 0, false))
	if len(got) != 1 || got[0] != outer {
		t.Fatalf("Scan found %v, want exactly [%s]", got, outer)
	}
}

func TestProjectArtifactsScanSkipsVCSInternals(t *testing.T) {
	for _, vcs := range []string{".git", ".hg", ".svn"} {
		t.Run(vcs, func(t *testing.T) {
			root := t.TempDir()
			// A junk-dir name inside VCS internals is repository data.
			writeFileOfSize(t, filepath.Join(root, vcs, "objects", "build", "pack"), 128)
			// The VCS dir itself is never junk either, no matter its size.
			writeFileOfSize(t, filepath.Join(root, vcs, "big.pack"), 4096)

			got := scanPaths(t, NewProjectArtifactsCleaner([]string{root}, 1024, false))
			if len(got) != 0 {
				t.Fatalf("Scan found %v inside %s, want nothing", got, vcs)
			}
		})
	}
}

func TestProjectArtifactsScanNeverEmitsTheRootItself(t *testing.T) {
	base := t.TempDir()
	// Root whose own base name matches the junk list: it is the configured
	// project, not junk.
	root := filepath.Join(base, "node_modules")
	writeFileOfSize(t, filepath.Join(root, "src", "main.go"), 32)
	inner := filepath.Join(root, "src", "dist")
	mkdirAll(t, inner)

	got := scanPaths(t, NewProjectArtifactsCleaner([]string{root}, 0, false))
	if len(got) != 1 || got[0] != inner {
		t.Fatalf("Scan found %v, want exactly [%s]", got, inner)
	}
}

func TestProjectArtifactsScanLargeFileThresholdBoundary(t *testing.T) {
	root := t.TempDir()
	const threshold = 1024

	writeFileOfSize(t, filepath.Join(root, "at-threshold.bin"), threshold)
	writeFileOfSize(t, filepath.Join(root, "under.bin"), threshold-1)
	over := filepath.Join(root, "over.bin")
	writeFileOfSize(t, over, threshold+1)

	got := scanPaths(t, NewProjectArtifactsCleaner([]string{root}, threshold, false))
	if len(got) != 1 || got[0] != over {
		t.Fatalf("Scan found %v, want exactly [%s] (strictly-greater-than threshold)", got, over)
	}
}

func TestProjectArtifactsScanDefaultThresholdIgnoresSmallFiles(t *testing.T) {
	root := t.TempDir()
	writeFileOfSize(t, filepath.Join(root, "readme.md"), 4096)

	c := NewProjectArtifactsCleaner([]string{root}, 0, false)
	if c.threshold != projectArtifactsLargeFileThreshold {
		t.Fatalf("threshold = %d, want the %d default", c.threshold, projectArtifactsLargeFileThreshold)
	}
	if got := scanPaths(t, c); len(got) != 0 {
		t.Fatalf("Scan found %v, want nothing", got)
	}
}

func TestProjectArtifactsScanIgnoresSymlinkedJunkDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFileOfSize(t, filepath.Join(outside, "real", "payload.bin"), 128)

	link := filepath.Join(root, "node_modules")
	if err := os.Symlink(filepath.Join(outside, "real"), link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// WalkDir does not follow symlinks, so the link fails d.IsDir() and is
	// never matched as a junk dir -- deleting outside the root is impossible.
	if got := scanPaths(t, NewProjectArtifactsCleaner([]string{root}, 0, false)); len(got) != 0 {
		t.Fatalf("Scan found %v, want nothing (symlinks are not junk dirs)", got)
	}
}

func TestProjectArtifactsScanMultipleRoots(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	junkA := filepath.Join(rootA, "node_modules")
	junkB := filepath.Join(rootB, "target")
	writeFileOfSize(t, filepath.Join(junkA, "a.bin"), 64)
	writeFileOfSize(t, filepath.Join(junkB, "b.bin"), 64)

	got := scanPaths(t, NewProjectArtifactsCleaner([]string{rootA, rootB}, 0, false))
	want := []string{junkA, junkB}
	sort.Strings(want)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Scan found %v, want %v", got, want)
	}
}

func TestProjectArtifactsCleanDryRunDeletesNothing(t *testing.T) {
	root := t.TempDir()
	junk := filepath.Join(root, "node_modules")
	writeFileOfSize(t, filepath.Join(junk, "a.bin"), 64)

	c := NewProjectArtifactsCleaner([]string{root}, 0, false)
	scan, err := c.Scan(context.Background(), nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	result, err := c.Clean(context.Background(), scan.Entries, true, nil)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if !result.DryRun {
		t.Error("CleanResult.DryRun = false, want true")
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1 (dry run still previews)", result.FilesDeleted)
	}
	if _, statErr := os.Stat(junk); statErr != nil {
		t.Errorf("dry run removed %s: %v", junk, statErr)
	}
}

func TestProjectArtifactsCleanRemovesJunkDirs(t *testing.T) {
	root := t.TempDir()
	junk := filepath.Join(root, "node_modules")
	keep := filepath.Join(root, "src", "main.go")
	writeFileOfSize(t, filepath.Join(junk, "a.bin"), 64)
	writeFileOfSize(t, keep, 32)

	c := NewProjectArtifactsCleaner([]string{root}, 0, false)
	scan, err := c.Scan(context.Background(), nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	result, err := c.Clean(context.Background(), scan.Entries, false, nil)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if result.FilesDeleted != 1 {
		t.Errorf("FilesDeleted = %d, want 1", result.FilesDeleted)
	}
	if _, statErr := os.Stat(junk); !os.IsNotExist(statErr) {
		t.Errorf("%s still exists after Clean (err=%v)", junk, statErr)
	}
	if _, statErr := os.Stat(keep); statErr != nil {
		t.Errorf("Clean removed unrelated file %s: %v", keep, statErr)
	}
}

func TestProjectArtifactsCleanLargeFilesAreOptIn(t *testing.T) {
	const threshold = 1024

	setup := func(t *testing.T) (root, junk, large string) {
		t.Helper()
		root = t.TempDir()
		junk = filepath.Join(root, "dist")
		large = filepath.Join(root, "dataset.bin")
		writeFileOfSize(t, filepath.Join(junk, "bundle.js"), 64)
		writeFileOfSize(t, large, threshold+1)
		return root, junk, large
	}

	t.Run("without deleteLargeFiles", func(t *testing.T) {
		root, junk, large := setup(t)
		c := NewProjectArtifactsCleaner([]string{root}, threshold, false)

		scan, err := c.Scan(context.Background(), nil)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(scan.Entries) != 2 {
			t.Fatalf("Scan found %d entries, want 2 (both kinds are always reported)", len(scan.Entries))
		}

		result, err := c.Clean(context.Background(), scan.Entries, false, nil)
		if err != nil {
			t.Fatalf("Clean: %v", err)
		}
		if result.FilesDeleted != 1 {
			t.Errorf("FilesDeleted = %d, want 1 (the large file is not counted as deleted)", result.FilesDeleted)
		}
		if _, statErr := os.Stat(junk); !os.IsNotExist(statErr) {
			t.Errorf("junk dir %s survived Clean (err=%v)", junk, statErr)
		}
		if _, statErr := os.Stat(large); statErr != nil {
			t.Errorf("large file %s was deleted without opt-in: %v", large, statErr)
		}
	})

	t.Run("with deleteLargeFiles", func(t *testing.T) {
		root, junk, large := setup(t)
		c := NewProjectArtifactsCleaner([]string{root}, threshold, true)

		scan, err := c.Scan(context.Background(), nil)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}

		result, err := c.Clean(context.Background(), scan.Entries, false, nil)
		if err != nil {
			t.Fatalf("Clean: %v", err)
		}
		if result.FilesDeleted != 2 {
			t.Errorf("FilesDeleted = %d, want 2", result.FilesDeleted)
		}
		if _, statErr := os.Stat(junk); !os.IsNotExist(statErr) {
			t.Errorf("junk dir %s survived Clean (err=%v)", junk, statErr)
		}
		if _, statErr := os.Stat(large); !os.IsNotExist(statErr) {
			t.Errorf("large file %s survived Clean with opt-in (err=%v)", large, statErr)
		}
	})

	t.Run("dry run mirrors the opt-out", func(t *testing.T) {
		root, _, _ := setup(t)
		c := NewProjectArtifactsCleaner([]string{root}, threshold, false)

		scan, err := c.Scan(context.Background(), nil)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		result, err := c.Clean(context.Background(), scan.Entries, true, nil)
		if err != nil {
			t.Fatalf("Clean: %v", err)
		}
		if result.FilesDeleted != 1 {
			t.Errorf("dry-run FilesDeleted = %d, want 1 -- the preview must match what execute would do", result.FilesDeleted)
		}
	})
}

func TestProjectArtifactsCleanMissingEntryIsNotAnError(t *testing.T) {
	root := t.TempDir()
	c := NewProjectArtifactsCleaner([]string{root}, 0, false)
	result, err := c.Clean(context.Background(), []FileEntry{
		{Path: filepath.Join(root, "gone"), Size: 10, IsDir: true, Category: CategoryProjectArtifacts},
	}, false, nil)
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Clean recorded %d errors for an already-missing entry, want 0", len(result.Errors))
	}
}

func TestProjectArtifactsMetadata(t *testing.T) {
	c := NewProjectArtifactsCleaner(nil, 0, false)

	if c.Category() != CategoryProjectArtifacts {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryProjectArtifacts)
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
	if c.DeletesWholeDomain() {
		t.Error("DeletesWholeDomain() = true, want false -- entries must be individually strippable by protected_paths")
	}
	if c.Name() == "" || c.Description() == "" {
		t.Error("Name()/Description() must not be empty")
	}
}
