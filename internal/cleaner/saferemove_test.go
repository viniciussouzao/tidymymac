package cleaner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// mustWrite creates a file with the given contents, making its parent first.
func mustWrite(t *testing.T, path, contents string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should have been removed, Lstat err = %v", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s should still exist: %v", path, err)
	}
}

func TestRootedRemoverRemovesOrdinaryFiles(t *testing.T) {
	root := t.TempDir()
	direct := mustWrite(t, filepath.Join(root, "top.log"), "x")
	nested := mustWrite(t, filepath.Join(root, "a", "b", "deep.log"), "y")

	r := newRootedRemover(root)
	defer r.Close()

	for _, path := range []string{direct, nested} {
		if err := r.Remove(path); err != nil {
			t.Fatalf("Remove(%s) = %v, want nil", path, err)
		}
		mustNotExist(t, path)
	}
}

// TestRootedRemoverRefusesEscapeViaSwappedParent is the F1 regression: the
// parent directory that the scan validated is replaced by a symlink pointing
// out of the domain before the deletion runs. The path string is unchanged, so
// every string-based check still passes; only descriptor-relative resolution
// can catch it.
func TestRootedRemoverRefusesEscapeViaSwappedParent(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "domain")
	outside := filepath.Join(base, "outside")

	target := mustWrite(t, filepath.Join(outside, "hosts"), "critical")

	// What the scan saw: an ordinary file under an ordinary parent.
	candidate := filepath.Join(root, "sub", "hosts")
	mustWrite(t, candidate, "junk")

	tests := []struct {
		name string
		link string
	}{
		{"absolute symlink", outside},
		// Relative escapes are the interesting half: they never contain the
		// target's absolute path, so no amount of prefix checking finds them.
		{"relative symlink", filepath.Join("..", "outside")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The swap: same path string, different parent directory.
			if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
				t.Fatalf("rm sub: %v", err)
			}
			if err := os.Symlink(tt.link, filepath.Join(root, "sub")); err != nil {
				t.Fatalf("symlink: %v", err)
			}
			t.Cleanup(func() { _ = os.Remove(filepath.Join(root, "sub")) })

			r := newRootedRemover(root)
			defer r.Close()

			if err := r.Remove(candidate); err == nil {
				t.Fatal("Remove followed a swapped parent out of the root")
			}
			mustExist(t, target)
		})
	}
}

// TestRootedRemoverRefusesFinalComponentSymlink covers the leaf being swapped
// rather than a parent. unlink(2) would remove the link and not its target, so
// this is not an escape -- but Clean must not report a symlink removal as the
// deletion of the file the scan measured.
func TestRootedRemoverRefusesFinalComponentSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "domain")
	target := mustWrite(t, filepath.Join(base, "outside", "hosts"), "critical")

	link := filepath.Join(root, "hosts")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := newRootedRemover(root)
	defer r.Close()

	err := r.Remove(link)
	if !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("Remove(symlink) = %v, want ErrNotRegularFile", err)
	}
	mustExist(t, target)
	mustExist(t, link)
}

func TestRootedRemoverRefusesTypeTransitions(t *testing.T) {
	root := t.TempDir()

	// A path the scan recorded as a file that is a directory by deletion time.
	nowDir := filepath.Join(root, "swapped")
	if err := os.MkdirAll(nowDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	r := newRootedRemover(root)
	defer r.Close()

	if err := r.Remove(nowDir); !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("Remove(directory) = %v, want ErrNotRegularFile", err)
	}
	mustExist(t, nowDir)

	// The reverse -- a directory the walk descended into, now a file -- fails
	// at the openat for the parent chain instead.
	mustWrite(t, filepath.Join(root, "wasdir"), "now a file")
	if err := r.Remove(filepath.Join(root, "wasdir", "child.log")); err == nil {
		t.Fatal("Remove through a non-directory component succeeded")
	}
}

func TestRootedRemoverConfinesToApprovedRoots(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "domain")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stranger := mustWrite(t, filepath.Join(base, "elsewhere", "file.log"), "x")

	r := newRootedRemover(root)
	defer r.Close()

	cases := map[string]string{
		"sibling directory":  stranger,
		"parent traversal":   filepath.Join(root, "..", "elsewhere", "file.log"),
		"the root itself":    root,
		"relative path":      "relative/file.log",
		"unrelated absolute": "/etc/hosts",
	}

	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			if err := r.Remove(path); !errors.Is(err, ErrOutsideApprovedRoots) {
				t.Fatalf("Remove(%s) = %v, want ErrOutsideApprovedRoots", path, err)
			}
		})
	}
	mustExist(t, stranger)
	mustExist(t, root)
}

// TestRootedRemoverIgnoresUnusableRoots documents that a remover built with no
// usable root deletes nothing at all, rather than silently falling back to
// unconfined removal.
func TestRootedRemoverIgnoresUnusableRoots(t *testing.T) {
	file := mustWrite(t, filepath.Join(t.TempDir(), "a.log"), "x")

	r := newRootedRemover("", "relative/dir", "/")
	defer r.Close()

	if len(r.roots) != 0 {
		t.Fatalf("roots = %v, want none kept", r.roots)
	}
	if err := r.Remove(file); !errors.Is(err, ErrOutsideApprovedRoots) {
		t.Fatalf("Remove = %v, want ErrOutsideApprovedRoots", err)
	}
	mustExist(t, file)
}

// TestRootedRemoverReportsAbsolutePaths guards the per-item error details the
// CLI and TUI print: os.Root reports failures against the name relative to the
// root, which on its own would name a bare leaf.
func TestRootedRemoverReportsAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "nested", "gone.log")
	if err := os.MkdirAll(filepath.Dir(missing), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	r := newRootedRemover(root)
	defer r.Close()

	err := r.Remove(missing)
	if !os.IsNotExist(err) {
		t.Fatalf("Remove(missing) = %v, want a not-exist error", err)
	}

	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("error is %T, want *os.PathError", err)
	}
	if pathErr.Path != missing {
		t.Fatalf("PathError.Path = %q, want the absolute %q", pathErr.Path, missing)
	}
}

// TestRootedRemoverFollowsSymlinksInsideTheRoot pins the deliberate residual
// documented on rootedRemover: a redirect that stays inside the scan root is
// allowed, because everything under that root is already what the cleaner was
// authorized to delete.
func TestRootedRemoverFollowsSymlinksInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	real := mustWrite(t, filepath.Join(root, "real", "file.log"), "x")
	if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := newRootedRemover(root)
	defer r.Close()

	if err := r.Remove(filepath.Join(root, "alias", "file.log")); err != nil {
		t.Fatalf("Remove through an in-root symlink = %v, want nil", err)
	}
	mustNotExist(t, real)
}

func TestRootedRemoverPrefersTheTightestRoot(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	r := newRootedRemover(outer, inner)
	defer r.Close()

	root, rel, ok := r.locate(filepath.Join(inner, "a.log"))
	if !ok || root != inner || rel != "a.log" {
		t.Fatalf("locate = (%q, %q, %v), want (%q, %q, true)", root, rel, ok, inner, "a.log")
	}
}

// --- per-cleaner coverage --------------------------------------------------

// sudoCleanerCase describes one privileged cleaner in terms of the fake home
// its domain is derived from, so the swapped-parent attack can be replayed
// identically against each of them.
type sudoCleanerCase struct {
	name string
	// build returns the cleaner and the directory inside its domain where the
	// candidate file lives.
	build func(t *testing.T, home string) (Cleaner, string)
}

func sudoCleanerCases() []sudoCleanerCase {
	return []sudoCleanerCase{
		{
			name: "Temp",
			build: func(t *testing.T, home string) (Cleaner, string) {
				t.Helper()
				// The real /tmp and /var/tmp roots are irrelevant here and are
				// left out so the test never touches them.
				domain := filepath.Join(home, "Library", "Caches", "TemporaryItems")
				return &TempCleaner{homeDir: home, roots: []string{domain}}, domain
			},
		},
		{
			name: "Logs",
			build: func(t *testing.T, home string) (Cleaner, string) {
				t.Helper()
				return &LogsCleaner{homeDir: home, roots: logsScanRoots(home)},
					filepath.Join(home, "Library", "Logs")
			},
		},
		{
			name: "Updates",
			build: func(t *testing.T, home string) (Cleaner, string) {
				t.Helper()
				return &UpdatesCleaner{homeDir: home, roots: updatesScanRoots(home)},
					filepath.Join(home, "Library", "Updates")
			},
		},
	}
}

// TestSudoCleanersRefuseSwappedParent replays F1 through the actual Clean
// methods of every cleaner that RequiresSudo and deletes by path: the entry is
// approved and its parent is then swapped for a symlink out of the domain. The
// deletion must be refused, reported as a per-item error, and the external
// target must survive.
func TestSudoCleanersRefuseSwappedParent(t *testing.T) {
	for _, tc := range sudoCleanerCases() {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "home")
			target := mustWrite(t, filepath.Join(base, "outside", "hosts"), "critical")

			c, domain := tc.build(t, home)

			candidate := filepath.Join(domain, "sub", "hosts")
			mustWrite(t, candidate, "junk")

			// A sibling that is not tampered with, to prove the refusal is
			// specific rather than the cleaner giving up wholesale.
			innocent := mustWrite(t, filepath.Join(domain, "keep.log"), "z")

			if err := os.RemoveAll(filepath.Join(domain, "sub")); err != nil {
				t.Fatalf("rm sub: %v", err)
			}
			if err := os.Symlink(filepath.Join(base, "outside"), filepath.Join(domain, "sub")); err != nil {
				t.Fatalf("symlink: %v", err)
			}

			entries := []FileEntry{
				{Path: candidate, Size: 4, Category: c.Category()},
				{Path: innocent, Size: 1, Category: c.Category()},
			}

			result, err := c.Clean(t.Context(), entries, false, nil)
			if err != nil {
				t.Fatalf("Clean() error: %v", err)
			}

			mustExist(t, target)
			mustNotExist(t, innocent)

			if result.FilesDeleted != 1 {
				t.Errorf("FilesDeleted = %d, want 1 (only the untampered sibling)", result.FilesDeleted)
			}
			if len(result.Errors) != 1 {
				t.Fatalf("Errors = %v, want exactly one refusal", result.Errors)
			}
		})
	}
}

// TestSudoCleanersRefuseEntriesOutsideTheirDomain covers the other half of the
// confinement: an entry that never could have come from this cleaner's Scan --
// a stale --from-file scan, a hand-edited plan -- is not deleted just because
// it reached Clean.
func TestSudoCleanersRefuseEntriesOutsideTheirDomain(t *testing.T) {
	for _, tc := range sudoCleanerCases() {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "home")
			c, domain := tc.build(t, home)
			if err := os.MkdirAll(domain, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			stranger := mustWrite(t, filepath.Join(base, "elsewhere", "file"), "x")

			result, err := c.Clean(t.Context(), []FileEntry{
				{Path: stranger, Size: 1, Category: c.Category()},
			}, false, nil)
			if err != nil {
				t.Fatalf("Clean() error: %v", err)
			}

			mustExist(t, stranger)
			if result.FilesDeleted != 0 {
				t.Errorf("FilesDeleted = %d, want 0", result.FilesDeleted)
			}
			if len(result.Errors) != 1 || !errors.Is(result.Errors[0], ErrOutsideApprovedRoots) {
				t.Fatalf("Errors = %v, want one ErrOutsideApprovedRoots", result.Errors)
			}
		})
	}
}

// TestSudoCleanersDryRunTouchesNothing pins that the confinement did not turn
// dry runs into deletions or into refusals: a dry run still reports every
// entry as deletable without opening a single root.
func TestSudoCleanersDryRunTouchesNothing(t *testing.T) {
	for _, tc := range sudoCleanerCases() {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "home")
			c, domain := tc.build(t, home)

			file := mustWrite(t, filepath.Join(domain, "a.log"), "x")

			result, err := c.Clean(t.Context(), []FileEntry{
				{Path: file, Size: 1, Category: c.Category()},
			}, true, nil)
			if err != nil {
				t.Fatalf("Clean(dryRun) error: %v", err)
			}
			if result.FilesDeleted != 1 || len(result.Errors) != 0 {
				t.Fatalf("dry run = %d deleted, errors %v, want 1 and none", result.FilesDeleted, result.Errors)
			}
			mustExist(t, file)
		})
	}
}

func TestTempScanRootsRejectsHostileTMPDIR(t *testing.T) {
	const home = "/Users/someone"

	tests := []struct {
		name    string
		tmpDir  string
		euid    int
		wantHas string
		wantNot string
	}{
		{name: "legitimate TMPDIR is a root", tmpDir: "/var/folders/ab/cd/T", euid: 501, wantHas: "/var/folders/ab/cd/T"},
		{name: "home directory is not", tmpDir: "/Users/someone/Documents", euid: 501, wantNot: "/Users/someone/Documents"},
		{name: "dropped entirely when elevated", tmpDir: "/var/folders/ab/cd/T", euid: 0, wantNot: "/var/folders/ab/cd/T"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots := tempScanRoots(home, tt.tmpDir, tt.euid)

			has := func(want string) bool {
				for _, r := range roots {
					if r == want {
						return true
					}
				}
				return false
			}

			if tt.wantHas != "" && !has(tt.wantHas) {
				t.Fatalf("roots = %v, want it to contain %q", roots, tt.wantHas)
			}
			if tt.wantNot != "" && has(tt.wantNot) {
				t.Fatalf("roots = %v, want it NOT to contain %q", roots, tt.wantNot)
			}
			// The unconditional roots are always present.
			if !has("/tmp") || !has("/var/tmp") {
				t.Fatalf("roots = %v, want /tmp and /var/tmp", roots)
			}
		})
	}
}

func TestHomeRelativeCleanersHaveNoDomainWithoutHome(t *testing.T) {
	if roots := logsScanRoots(""); roots != nil {
		t.Errorf("logsScanRoots(\"\") = %v, want nil", roots)
	}
	if roots := updatesScanRoots(""); roots != nil {
		t.Errorf("updatesScanRoots(\"\") = %v, want nil", roots)
	}
	// Temp still has the system roots, but nothing home-derived.
	for _, r := range tempScanRoots("", "", 501) {
		if !filepath.IsAbs(r) {
			t.Errorf("tempScanRoots(\"\") produced the relative root %q", r)
		}
	}
}
