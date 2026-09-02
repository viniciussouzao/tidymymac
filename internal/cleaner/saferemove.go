package cleaner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

var (
	// ErrOutsideApprovedRoots is returned when an entry handed to Clean does
	// not live under any of the roots that cleaner's own Scan walks. It is a
	// containment failure, not a filesystem failure: whatever produced the
	// entry (a stale --from-file scan, a hand-edited plan, a bug) pointed at
	// something this cleaner has no business deleting.
	ErrOutsideApprovedRoots = errors.New("path is outside the cleaner's approved roots")

	// ErrNotRegularFile is returned when the object actually present at an
	// entry's location is not the regular file the scan reported -- a
	// directory, a symlink, a device node. Under elevation this is the shape a
	// swap attempt leaves behind, so it is refused rather than removed.
	ErrNotRegularFile = errors.New("path is no longer a regular file")

	// ErrRootUnavailable is returned when one of the cleaner's scan roots
	// cannot be opened at all -- renamed, unmounted or removed between the
	// scan and the deletion.
	//
	// It deliberately does not wrap the underlying error. The cleaners treat a
	// not-exist result as "already gone, count it as deleted", which is right
	// for a single missing file and badly wrong for a missing root: every
	// entry beneath it would be reported as reclaimed space that was never
	// reclaimed.
	ErrRootUnavailable = errors.New("scan root is no longer available")

	// ErrIdentityChanged is returned when the path still resolves to a regular
	// file, but not the same one the scan measured.
	ErrIdentityChanged = errors.New("path no longer refers to the file that was scanned")
)

// rootedRemover deletes files without ever re-resolving an attacker-controlled
// path component by name after validation.
//
// The problem it exists for: a cleaner's Scan establishes that a path string
// belongs to its domain, and Clean then deletes that same string later, in a
// separate syscall. os.Remove re-walks every component from "/" at that later
// moment. If anything between the root and the leaf is swapped for a symlink
// in between -- trivially arrangeable when the parent is under a
// world-writable /tmp -- the kernel resolves the deletion somewhere else
// entirely while the path string, and therefore every check made against it,
// stays identical. As root, that turns an approved temp-file cleanup into
// unlink("/etc/hosts").
//
// The fix is to stop resolving by name. Each removal is performed relative to
// an os.Root anchored at one of the cleaner's own scan roots:
//
//   - os.Root resolves each component with openat and refuses any symlink that
//     would leave the root, and refuses absolute symlinks outright. A swapped
//     parent can therefore no longer redirect the unlink out of the domain the
//     user approved.
//   - The parent directory is then held open as a descriptor and the leaf is
//     both checked (Lstat) and unlinked (Remove) relative to that same
//     descriptor, so validation and removal share one already-resolved parent
//     instead of two independent path walks.
//   - The leaf must still be a regular file. A leaf swapped for a symlink
//     between the Lstat and the Remove is harmless on its own -- unlink(2)
//     removes the link, never its target -- but refusing the transition keeps
//     Clean honest about what it deleted.
//   - The leaf must still be the same object the scan measured, compared by
//     device and inode. Confinement alone leaves a real gap: a symlink whose
//     target stays *inside* the scan root is followed, and "inside the
//     cleaner's domain" is a strictly larger set than "approved by the user".
//     config.StripProtected and the review screen's deselection both filter
//     the entry *list*, not the roots, so an in-root redirect can land on a
//     protected path or on a file the user explicitly unchecked -- and /tmp
//     and /var/tmp are world-writable and shared, so the attacker need not
//     even be the victim. The identity check closes that, and closes the
//     window between the Lstat and the Remove along with it.
//
// The identity is only as good as its source. It is populated by the cleaner's
// own Scan and never crosses a trust boundary (see FileEntry.Dev/Ino), so an
// entry that arrives without one -- from a --from-file scan file, say -- is
// still confined to the roots but cannot be identity-checked.
//
// A rootedRemover is not safe for concurrent use; each Clean call builds its
// own.
type rootedRemover struct {
	// roots are cleaned, absolute, longest-first, so a nested root wins over
	// an ancestor: the tighter the root, the smaller the region within which a
	// symlink is allowed to redirect at all.
	roots []string

	openRoots map[string]*os.Root
	openErrs  map[string]error

	// Entries arrive grouped by directory (WalkDir emits them that way and the
	// elevated intersection preserves scan order), so caching a single open
	// parent turns one openat chain per file into one per directory without
	// risking descriptor exhaustion.
	parentDir  string
	parentRoot *os.Root
}

// resolveScanRoot returns the path a scan root should actually be addressed
// by. It follows symlinks only for the fixed, system-owned macOS roots below;
// an arbitrary root (in particular one under the user's home) is never allowed
// to redefine a cleaner's deletion domain by pointing somewhere else.
//
// This is not cosmetic. macOS ships /tmp as a symlink to private/tmp, and
// filepath.WalkDir only Lstats its root: handed "/tmp" it sees a symlink, not
// a directory, reports the symlink itself as the single entry, and never
// descends. The Temp cleaner therefore scanned nothing at all under the one
// world-writable directory it most needs to cover. ("/var/tmp" escaped this
// because only the *final* component is Lstat'd, and "tmp" there is a real
// directory.)
//
// Following every root was unsafe: a user-writable ~/Library/Logs symlink to
// /etc made both the ordinary and privileged scans call /etc the Logs domain;
// the plan/fresh-scan intersection and rootedRemover then correctly agreed on
// /etc/hosts and authorized its removal as root. The two fences can only be as
// safe as the domain definition they share.
//
// /tmp, /var/tmp and /var/log are different: their aliases are part of the
// macOS filesystem layout and their parent components are system-owned. They
// are the only roots for which following the platform alias is both necessary
// and trusted. Resolving them also keeps a root and the entries beneath it
// spelled the same way, which is what locate's prefix match depends on.
// Protected paths are unaffected: internal/config stores every protected
// entry under its literal spelling, its firmlink alias, and its EvalSymlinks
// resolution.
//
// A root that cannot be resolved -- most often because it does not exist on
// this machine -- is returned unchanged and simply fails later.
func resolveScanRoot(path string) string {
	cleaned := filepath.Clean(path)
	switch cleaned {
	case "/tmp", "/var/tmp", "/var/log":
	default:
		return cleaned
	}

	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return cleaned
	}
	return resolved
}

// resolveScanRoots canonicalizes trusted system aliases and drops duplicates,
// preserving order. Deduping matters because distinct spellings can collapse
// onto one another -- "/tmp" and a TMPDIR of "/private/tmp" being the case
// that actually occurs -- and walking the same tree twice would double-count
// every file in it. Arbitrary symlink roots are deliberately left untouched;
// filepath.WalkDir Lstats such a root and does not descend through it.
func resolveScanRoots(paths []string) []string {
	resolved := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		r := resolveScanRoot(p)
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		resolved = append(resolved, r)
	}
	return resolved
}

// newRootedRemover builds a remover confined to roots. Empty, relative and
// duplicate roots are dropped, as is "/" -- a cleaner whose domain is the
// whole filesystem would make the confinement meaningless.
func newRootedRemover(roots ...string) *rootedRemover {
	cleaned := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))

	for _, root := range roots {
		if root == "" {
			continue
		}
		c := filepath.Clean(root)
		if !filepath.IsAbs(c) || c == "/" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		cleaned = append(cleaned, c)
	}

	sort.Slice(cleaned, func(i, j int) bool { return len(cleaned[i]) > len(cleaned[j]) })

	return &rootedRemover{
		roots:     cleaned,
		openRoots: make(map[string]*os.Root),
		openErrs:  make(map[string]error),
	}
}

// Remove deletes the regular file the entry describes. Its path must be
// strictly inside one of the remover's roots, and -- when the entry carries an
// identity -- it must still be the object the scan measured. A root itself is
// never removable.
func (r *rootedRemover) Remove(entry FileEntry) error {
	abs := filepath.Clean(entry.Path)
	if !filepath.IsAbs(abs) {
		return fmt.Errorf("%w: %s", ErrOutsideApprovedRoots, entry.Path)
	}

	rootPath, rel, ok := r.locate(abs)
	if !ok {
		return fmt.Errorf("%w: %s", ErrOutsideApprovedRoots, abs)
	}

	parent, err := r.parentFor(rootPath, filepath.Dir(abs), filepath.Dir(rel))
	if err != nil {
		return absolutize(err, filepath.Dir(abs))
	}

	leaf := filepath.Base(rel)

	info, err := parent.Lstat(leaf)
	if err != nil {
		return absolutize(err, abs)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is %s", ErrNotRegularFile, abs, describeMode(info.Mode()))
	}
	if entry.Ino != 0 {
		dev, ino, ok := fileIdentity(info)
		if !ok || dev != entry.Dev || ino != entry.Ino {
			return fmt.Errorf("%w: %s", ErrIdentityChanged, abs)
		}
	}

	return absolutize(parent.Remove(leaf), abs)
}

// Close releases every descriptor the remover opened.
func (r *rootedRemover) Close() {
	r.closeParent()
	for _, root := range r.openRoots {
		_ = root.Close()
	}
	clear(r.openRoots)
}

// locate finds the approved root containing abs and the path of abs relative
// to it. A path equal to a root is reported as not located: scan roots are
// domain boundaries, never deletion candidates.
func (r *rootedRemover) locate(abs string) (root, rel string, ok bool) {
	for _, candidate := range r.roots {
		if prefix := candidate + "/"; strings.HasPrefix(abs, prefix) {
			return candidate, abs[len(prefix):], true
		}
	}
	return "", "", false
}

// parentFor returns an os.Root anchored at the entry's parent directory,
// reached only through openat calls constrained to rootPath. absDir is used
// solely as the cache key and for error messages.
func (r *rootedRemover) parentFor(rootPath, absDir, relDir string) (*os.Root, error) {
	if r.parentRoot != nil && r.parentDir == absDir {
		return r.parentRoot, nil
	}

	root, err := r.rootFor(rootPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrRootUnavailable, rootPath, err)
	}

	// relDir is "." when the file sits directly in the scan root; os.Root
	// accepts that and hands back an independent Root the cache can own.
	//
	// A failure here, unlike a failure to open the root, is left as the raw
	// *fs.PathError: a parent directory that vanished between scan and clean
	// is the ordinary "already gone" case, and an escape attempt is not a
	// not-exist error to begin with.
	parent, err := root.OpenRoot(relDir)
	if err != nil {
		return nil, err
	}

	r.closeParent()
	r.parentDir, r.parentRoot = absDir, parent

	return parent, nil
}

// rootFor opens (once) the os.Root for an approved scan root. A root that
// fails to open -- most often because it does not exist on this machine --
// fails the same way for every later entry beneath it, so the error is cached
// rather than retried per file.
//
// The cached error is shared by every caller, so nothing downstream may mutate
// it. That is why absolutize returns a copy.
func (r *rootedRemover) rootFor(rootPath string) (*os.Root, error) {
	if root, ok := r.openRoots[rootPath]; ok {
		return root, nil
	}
	if err, ok := r.openErrs[rootPath]; ok {
		return nil, err
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		r.openErrs[rootPath] = err
		return nil, err
	}

	r.openRoots[rootPath] = root

	return root, nil
}

func (r *rootedRemover) closeParent() {
	if r.parentRoot != nil {
		_ = r.parentRoot.Close()
		r.parentRoot = nil
		r.parentDir = ""
	}
}

// absolutize restates a *fs.PathError produced by an os.Root method in terms
// of the absolute path the caller asked about. Without it every reported
// failure -- including the per-item details the CLI and TUI print -- would
// name a leaf like "a.log" with no indication of where it lived.
//
// It returns a copy and never mutates in place. Errors here can be shared:
// rootFor caches one per root and hands the same pointer to every entry
// beneath it, so rewriting the original made all of those report whichever
// path happened to fail last.
func absolutize(err error, abs string) error {
	pathErr, ok := err.(*fs.PathError)
	if !ok {
		return err
	}
	return &fs.PathError{Op: pathErr.Op, Path: abs, Err: pathErr.Err}
}

// fileIdentity extracts the (device, inode) pair behind a FileInfo. ok is
// false on a platform or filesystem that does not expose one, in which case
// the caller must fall back to the confinement guarantees alone rather than
// silently treating "no identity" as "identity matched".
func fileIdentity(info fs.FileInfo) (dev, ino uint64, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, 0, false
	}
	// Dev is signed on darwin; Ino is already uint64 on every supported
	// platform.
	return uint64(stat.Dev), stat.Ino, true
}

func describeMode(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "a symlink"
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeDevice != 0:
		return "a device"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	default:
		return "not a regular file"
	}
}
