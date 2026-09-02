package cleaner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
//
// What deliberately remains: a symlink whose target stays *inside* the same
// scan root is followed. That cannot cross the privilege boundary, because
// everything under a scan root is by definition what this cleaner was already
// authorized to delete; it can at worst redirect one in-domain deletion to
// another in-domain file. Closing even that would need a device/inode identity
// captured at scan time and carried to Clean, which buys no additional
// containment.
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

// Remove deletes the regular file at path, which must be strictly inside one
// of the remover's roots. A root itself is never removable.
func (r *rootedRemover) Remove(path string) error {
	abs := filepath.Clean(path)
	if !filepath.IsAbs(abs) {
		return fmt.Errorf("%w: %s", ErrOutsideApprovedRoots, path)
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
		return nil, err
	}

	// relDir is "." when the file sits directly in the scan root; os.Root
	// accepts that and hands back an independent Root the cache can own.
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

// absolutize rewrites the relative name in a *fs.PathError produced by an
// os.Root method back to the absolute path the caller asked about. Without it
// every reported failure -- including the per-item details the CLI and TUI now
// print -- would name a leaf like "a.log" with no indication of where it lived.
// The error is freshly constructed by the os.Root call above, so mutating it
// cannot be observed by anyone else.
func absolutize(err error, abs string) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		pathErr.Path = abs
	}
	return err
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
