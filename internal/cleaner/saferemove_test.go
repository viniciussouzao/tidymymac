package cleaner

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
		if err := r.Remove(FileEntry{Path: path}); err != nil {
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

			if err := r.Remove(FileEntry{Path: candidate}); err == nil {
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

	err := r.Remove(FileEntry{Path: link})
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

	if err := r.Remove(FileEntry{Path: nowDir}); !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("Remove(directory) = %v, want ErrNotRegularFile", err)
	}
	mustExist(t, nowDir)

	// The reverse -- a directory the walk descended into, now a file -- fails
	// at the openat for the parent chain instead.
	mustWrite(t, filepath.Join(root, "wasdir"), "now a file")
	if err := r.Remove(FileEntry{Path: filepath.Join(root, "wasdir", "child.log")}); err == nil {
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
			if err := r.Remove(FileEntry{Path: path}); !errors.Is(err, ErrOutsideApprovedRoots) {
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
	if err := r.Remove(FileEntry{Path: file}); !errors.Is(err, ErrOutsideApprovedRoots) {
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

	err := r.Remove(FileEntry{Path: missing})
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

// TestRootedRemoverRefusesInRootRedirect covers the half that confinement
// alone does not: a swapped parent whose symlink target stays *inside* the
// scan root. os.Root follows it, and "inside the cleaner's domain" is a
// strictly larger set than "approved by the user" -- config.StripProtected and
// the review screen filter the entry list, never the roots. So without an
// identity check this silently deletes a protected or deselected file and
// reports it as the approved one.
func TestRootedRemoverRefusesInRootRedirect(t *testing.T) {
	root := t.TempDir()

	approved := mustWrite(t, filepath.Join(root, "att", "junk.log"), "junk")
	// Never in the entry list: think a protected path, or one the user
	// unchecked in the review screen.
	unapproved := mustWrite(t, filepath.Join(root, "keep", "junk.log"), "keep me")

	entry := entryFor(t, approved)

	// The swap, entirely within the root.
	if err := os.RemoveAll(filepath.Join(root, "att")); err != nil {
		t.Fatalf("rm att: %v", err)
	}
	if err := os.Symlink("keep", filepath.Join(root, "att")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := newRootedRemover(root)
	defer r.Close()

	if err := r.Remove(entry); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("Remove after an in-root redirect = %v, want ErrIdentityChanged", err)
	}
	mustExist(t, unapproved)
}

// TestRootedRemoverWithoutIdentityIsStillConfined documents the fallback: an
// entry that never got an identity -- a --from-file scan file, where the
// value would be attacker-supplied and is therefore deliberately not carried
// -- keeps the containment guarantee but not the in-root one.
func TestRootedRemoverWithoutIdentityIsStillConfined(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "domain")
	outside := mustWrite(t, filepath.Join(base, "outside", "hosts"), "critical")

	candidate := mustWrite(t, filepath.Join(root, "sub", "hosts"), "junk")
	if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
		t.Fatalf("rm sub: %v", err)
	}
	if err := os.Symlink(filepath.Join(base, "outside"), filepath.Join(root, "sub")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := newRootedRemover(root)
	defer r.Close()

	// No Dev/Ino: the identity check is skipped, the confinement is not.
	if err := r.Remove(FileEntry{Path: candidate}); err == nil {
		t.Fatal("an identity-less entry escaped the root")
	}
	mustExist(t, outside)
}

// TestRootedRemoverAcceptsMatchingIdentity is the control: the identity check
// must not refuse the ordinary case it is wrapped around.
func TestRootedRemoverAcceptsMatchingIdentity(t *testing.T) {
	root := t.TempDir()
	file := mustWrite(t, filepath.Join(root, "nested", "a.log"), "x")

	r := newRootedRemover(root)
	defer r.Close()

	entry := entryFor(t, file)
	if entry.Ino == 0 {
		t.Fatal("entryFor produced no identity; the rest of the suite is vacuous")
	}
	if err := r.Remove(entry); err != nil {
		t.Fatalf("Remove(matching identity) = %v, want nil", err)
	}
	mustNotExist(t, file)
}

// entryFor builds the FileEntry a Scan would have produced for path,
// identity included.
func entryFor(t *testing.T, path string) FileEntry {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	dev, ino, ok := fileIdentity(info)
	if !ok {
		t.Fatalf("no filesystem identity available for %s", path)
	}
	return FileEntry{Path: path, Size: info.Size(), ModTime: info.ModTime(), Dev: dev, Ino: ino}
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
	// build returns the cleaner and its single scan root.
	//
	// Each cleaner is given exactly one root, inside a throwaway home, rather
	// than the set its constructor would produce: the real ones are /tmp,
	// /var/tmp, /Library/Logs and /var/log, and a test must neither walk nor
	// delete inside those. The real root resolution is covered separately by
	// TestTempScanRootsRejectsHostileTMPDIR and
	// TestHomeRelativeCleanersHaveNoDomainWithoutHome.
	build func(t *testing.T, home string) (Cleaner, string)
}

func sudoCleanerCases() []sudoCleanerCase {
	return []sudoCleanerCase{
		{
			name: "Temp",
			build: func(t *testing.T, home string) (Cleaner, string) {
				t.Helper()
				domain := filepath.Join(home, "Library", "Caches", "TemporaryItems")
				return &TempCleaner{homeDir: home, roots: []string{domain}}, domain
			},
		},
		{
			name: "Logs",
			build: func(t *testing.T, home string) (Cleaner, string) {
				t.Helper()
				domain := filepath.Join(home, "Library", "Logs")
				return &LogsCleaner{homeDir: home, roots: []string{domain}}, domain
			},
		},
		{
			name: "Updates",
			build: func(t *testing.T, home string) (Cleaner, string) {
				t.Helper()
				domain := filepath.Join(home, "Library", "Updates")
				return &UpdatesCleaner{homeDir: home, roots: []string{domain}}, domain
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

			// Roots come back resolved through symlinks, so expectations are
			// resolved the same way rather than spelled out twice.
			if tt.wantHas != "" && !has(resolveScanRoot(tt.wantHas)) {
				t.Fatalf("roots = %v, want it to contain %q", roots, tt.wantHas)
			}
			if tt.wantNot != "" && has(resolveScanRoot(tt.wantNot)) {
				t.Fatalf("roots = %v, want it NOT to contain %q", roots, tt.wantNot)
			}
			// The unconditional roots are always present.
			if !has(resolveScanRoot("/tmp")) || !has(resolveScanRoot("/var/tmp")) {
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

// TestAbsolutizeCopiesRatherThanMutating covers a bug where absolutize
// rewrote its argument in place. os.Root errors can be shared -- rootFor
// caches one per root and hands the same pointer to every entry beneath it --
// so mutating made all of them report whichever path failed last, in the very
// list a user reads to learn what was not deleted.
func TestAbsolutizeCopiesRatherThanMutating(t *testing.T) {
	shared := &fs.PathError{Op: "openat", Path: "rel", Err: fs.ErrNotExist}

	first := absolutize(shared, "/domain/a/rel")
	second := absolutize(shared, "/domain/b/rel")

	if shared.Path != "rel" {
		t.Errorf("absolutize mutated its input: Path = %q, want %q", shared.Path, "rel")
	}

	firstPath := first.(*fs.PathError).Path
	secondPath := second.(*fs.PathError).Path
	if firstPath != "/domain/a/rel" || secondPath != "/domain/b/rel" {
		t.Fatalf("paths = %q / %q, want the two distinct absolutes", firstPath, secondPath)
	}
	if !errors.Is(first, fs.ErrNotExist) {
		t.Error("the copy lost the underlying error")
	}
}

// TestRootedRemoverReportsEachEntrysOwnPath is the same guarantee end to end:
// two entries failing under one live root must each name themselves.
func TestRootedRemoverReportsEachEntrysOwnPath(t *testing.T) {
	root := t.TempDir()

	r := newRootedRemover(root)
	defer r.Close()

	first := filepath.Join(root, "dirA", "one.log")
	second := filepath.Join(root, "dirB", "two.log")

	errFirst := r.Remove(FileEntry{Path: first})
	errSecond := r.Remove(FileEntry{Path: second})

	if errFirst == nil || errSecond == nil {
		t.Fatalf("both removals should fail: %v / %v", errFirst, errSecond)
	}
	if !strings.Contains(errFirst.Error(), filepath.Join(root, "dirA")) {
		t.Errorf("first error = %q, want it to name dirA", errFirst)
	}
	if !strings.Contains(errSecond.Error(), filepath.Join(root, "dirB")) {
		t.Errorf("second error = %q, want it to name dirB", errSecond)
	}
}

// TestRootedRemoverReportsAVanishedRootAsAnError pins that a scan root which
// disappeared between scan and clean is never mistaken for "this one file was
// already gone". The cleaners treat a not-exist result as a successful
// deletion and add the entry's size to the reclaimed total, so a root-level
// ENOENT reaching that branch would report space that was never freed.
func TestRootedRemoverReportsAVanishedRootAsAnError(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "gone")

	r := newRootedRemover(missingRoot)
	defer r.Close()

	err := r.Remove(FileEntry{Path: filepath.Join(missingRoot, "a.log")})
	if !errors.Is(err, ErrRootUnavailable) {
		t.Fatalf("Remove under a missing root = %v, want ErrRootUnavailable", err)
	}
	if os.IsNotExist(err) {
		t.Fatal("a vanished root must not look like a vanished file: the cleaners would count it as deleted")
	}
}

// TestSudoCleanersDoNotClaimSpaceFromAVanishedRoot is the same guarantee seen
// through a real Clean, which is where the miscounting would actually happen.
func TestSudoCleanersDoNotClaimSpaceFromAVanishedRoot(t *testing.T) {
	for _, tc := range sudoCleanerCases() {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			c, domain := tc.build(t, filepath.Join(base, "home"))

			// The domain is never created: the scan root does not exist.
			result, err := c.Clean(t.Context(), []FileEntry{
				{Path: filepath.Join(domain, "a.log"), Size: 4096, Category: c.Category()},
			}, false, nil)
			if err != nil {
				t.Fatalf("Clean() error: %v", err)
			}

			if result.FilesDeleted != 0 || result.BytesFreed != 0 {
				t.Errorf("reported %d files / %d bytes reclaimed from a root that does not exist",
					result.FilesDeleted, result.BytesFreed)
			}
			if len(result.Errors) != 1 || !errors.Is(result.Errors[0], ErrRootUnavailable) {
				t.Fatalf("Errors = %v, want one ErrRootUnavailable", result.Errors)
			}
		})
	}
}

// TestSudoCleanersScanOnlyRegularFiles covers a regression the confinement
// work introduced: WalkDir does not follow symlinks, so it reports one as an
// ordinary non-directory entry, and Scan used to offer it as a deletion
// candidate. Clean now refuses non-regular leaves -- it cannot tell an
// enumerated symlink from one swapped in to redirect a deletion -- so those
// entries turned into a partial error on every single run, with a non-zero
// exit status, for a clean that did nothing wrong. /tmp, /var/log and
// ~/Library/Logs all hold symlinks and sockets in normal operation.
func TestSudoCleanersScanOnlyRegularFiles(t *testing.T) {
	for _, tc := range sudoCleanerCases() {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			c, domain := tc.build(t, filepath.Join(base, "home"))

			regular := mustWrite(t, filepath.Join(domain, "real.log"), "content")
			if err := os.Symlink(regular, filepath.Join(domain, "alias.log")); err != nil {
				t.Fatalf("symlink: %v", err)
			}
			if err := syscall.Mkfifo(filepath.Join(domain, "pipe"), 0o600); err != nil {
				t.Fatalf("mkfifo: %v", err)
			}

			result, err := c.Scan(t.Context(), nil)
			if err != nil {
				t.Fatalf("Scan() error: %v", err)
			}

			if len(result.Entries) != 1 || result.Entries[0].Path != regular {
				t.Fatalf("Scan returned %+v, want only %s", result.Entries, regular)
			}

			// And the entry it did return must clean without complaint.
			cleaned, err := c.Clean(t.Context(), result.Entries, false, nil)
			if err != nil {
				t.Fatalf("Clean() error: %v", err)
			}
			if cleaned.FilesDeleted != 1 || len(cleaned.Errors) != 0 {
				t.Fatalf("Clean = %d deleted, errors %v, want 1 and none", cleaned.FilesDeleted, cleaned.Errors)
			}
			mustNotExist(t, regular)
		})
	}
}

// TestSudoCleanersScanPopulatesIdentity pins that the identity check is not
// silently inert: a Scan that stopped filling Dev/Ino would skip it on every
// entry and nothing else in the suite would notice.
func TestSudoCleanersScanPopulatesIdentity(t *testing.T) {
	for _, tc := range sudoCleanerCases() {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			c, domain := tc.build(t, filepath.Join(base, "home"))
			path := mustWrite(t, filepath.Join(domain, "a.log"), "x")

			result, err := c.Scan(t.Context(), nil)
			if err != nil {
				t.Fatalf("Scan() error: %v", err)
			}
			if len(result.Entries) != 1 {
				t.Fatalf("got %d entries, want 1", len(result.Entries))
			}

			want := entryFor(t, path)
			got := result.Entries[0]
			if got.Ino == 0 {
				t.Fatal("Scan produced no inode; the identity check would be skipped for every entry")
			}
			if got.Dev != want.Dev || got.Ino != want.Ino {
				t.Fatalf("identity = (%d,%d), want (%d,%d)", got.Dev, got.Ino, want.Dev, want.Ino)
			}
		})
	}
}

// TestFileEntryIdentityNeverCrossesATrustBoundary pins the json:"-" tags. The
// identity is only meaningful because whichever process checks it also
// observed it; a value arriving over the elevation IPC or out of a
// --from-file scan file would be attacker-supplied, and honoring it would turn
// the check into a way to authorize a swap rather than detect one.
func TestFileEntryIdentityNeverCrossesATrustBoundary(t *testing.T) {
	encoded, err := json.Marshal(FileEntry{Path: "/tmp/a", Dev: 99, Ino: 12345})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{"Dev", "Ino", "12345", "99"} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("serialized FileEntry leaks the identity (%q): %s", field, encoded)
		}
	}

	var decoded FileEntry
	if err := json.Unmarshal([]byte(`{"Path":"/tmp/a","Dev":99,"Ino":12345}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Dev != 0 || decoded.Ino != 0 {
		t.Fatalf("decoded identity = (%d,%d), want it ignored", decoded.Dev, decoded.Ino)
	}
}

// TestScanRootResolutionDoesNotFollowArbitrarySymlinks is the regression for
// the scan-root confused deputy: a user-writable cleaner root must not be able
// to redefine its domain. If this link were followed, a Logs/Temp/Updates root
// could point at /etc and the privileged helper's two fences would both agree
// that /etc belonged to that cleaner.
func TestScanRootResolutionDoesNotFollowArbitrarySymlinks(t *testing.T) {
	tests := []struct {
		name      string
		rel       string
		buildRoot func(home string) []string
		cleaner   func(home, root string) Cleaner
	}{
		{
			name:      "Logs",
			rel:       filepath.Join("Library", "Logs"),
			buildRoot: logsScanRoots,
			cleaner: func(home, root string) Cleaner {
				return &LogsCleaner{homeDir: home, roots: []string{root}}
			},
		},
		{
			name: "Temp",
			rel:  filepath.Join("Library", "Caches", "TemporaryItems"),
			buildRoot: func(home string) []string {
				return tempScanRoots(home, "", 0)
			},
			cleaner: func(home, root string) Cleaner {
				return &TempCleaner{homeDir: home, roots: []string{root}}
			},
		},
		{
			name:      "Updates",
			rel:       filepath.Join("Library", "Updates"),
			buildRoot: updatesScanRoots,
			cleaner: func(home, root string) Cleaner {
				return &UpdatesCleaner{homeDir: home, roots: []string{root}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "home")
			realDir := filepath.Join(base, "outside")
			buried := mustWrite(t, filepath.Join(realDir, "nested", "a.log"), "content")
			link := filepath.Join(home, tt.rel)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatalf("mkdir link parent: %v", err)
			}
			if err := os.Symlink(realDir, link); err != nil {
				t.Fatalf("symlink: %v", err)
			}

			if got := resolveScanRoot(link); got != link {
				t.Fatalf("resolveScanRoot(%q) = %q, want the arbitrary symlink left untouched", link, got)
			}

			var foundLiteral, foundTarget bool
			for _, root := range tt.buildRoot(home) {
				foundLiteral = foundLiteral || root == link
				foundTarget = foundTarget || root == realDir
			}
			if !foundLiteral || foundTarget {
				t.Fatalf("roots must retain %q and never adopt target %q", link, realDir)
			}

			result, err := tt.cleaner(home, link).Scan(t.Context(), nil)
			if err != nil {
				t.Fatalf("Scan() error: %v", err)
			}
			if len(result.Entries) != 0 {
				t.Fatalf("Scan through a symlinked root = %+v, want no entries", result.Entries)
			}
			mustExist(t, buried)
		})
	}
}

// TestTrustedSystemScanRootsStillResolve pins the narrow exception. macOS
// ships /tmp as a symlink and WalkDir will not descend through a symlink used
// as its root, so this one system-owned alias must remain canonicalized.
func TestTrustedSystemScanRootsStillResolve(t *testing.T) {
	for _, root := range []string{"/tmp", "/var/tmp", "/var/log"} {
		want, err := filepath.EvalSymlinks(root)
		if err != nil {
			// The project is macOS-only, but keeping the helper harmless on a
			// host missing one of these roots makes the unit test portable.
			want = filepath.Clean(root)
		}
		if got := resolveScanRoot(root); got != want {
			t.Errorf("resolveScanRoot(%q) = %q, want trusted alias %q", root, got, want)
		}
	}
}

func TestResolveScanRootsDedupesCollapsedSpellings(t *testing.T) {
	// "/tmp" and a TMPDIR of "/private/tmp" collapse onto one path; walking
	// both would double-count every file under it.
	roots := resolveScanRoots([]string{"/tmp", "/private/tmp"})
	if len(roots) != 1 {
		t.Fatalf("roots = %v, want the two spellings deduped to one", roots)
	}

	// A root that cannot be resolved is kept as written rather than dropped.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if got := resolveScanRoots([]string{missing}); len(got) != 1 || got[0] != missing {
		t.Fatalf("resolveScanRoots(missing) = %v, want it kept unchanged", got)
	}
}
