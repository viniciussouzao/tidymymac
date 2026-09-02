package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/homedir"
)

// TempCleaner scans and cleans temporary files
type TempCleaner struct {
	homeDir string

	// roots is the cleaner's domain: the only directories Scan walks and,
	// just as importantly, the only ones Clean will delete inside. It is
	// resolved once at construction so the two can never disagree about what
	// the domain is -- a Clean allowed to delete under a root Scan never
	// visited would be a hole in the elevated helper's second fence.
	roots []string
}

// NewTempCleaner creates a TempCleaner. The home directory comes from
// homedir.Resolve rather than os.UserHomeDir because this cleaner requires
// sudo: when the process runs elevated, os.UserHomeDir would resolve to root's
// home (/var/root) and the cleaner would scan and clean the wrong home.
func NewTempCleaner() *TempCleaner {
	home, err := homedir.Resolve()
	if err != nil {
		home = ""
	}
	return &TempCleaner{
		homeDir: home,
		roots:   tempScanRoots(home, os.TempDir(), os.Geteuid()),
	}
}

// tempScanRoots resolves the Temp domain.
//
// os.TempDir() is just $TMPDIR: attacker-settable, and this cleaner
// RequiresSudo, so an unvalidated value becomes a root-walked scan root and
// therefore a root-deletable domain. Accept it only when it really is a macOS
// temp location, and never at all when elevated -- see userTempRoot.
func tempScanRoots(homeDir, tmpDir string, euid int) []string {
	roots := []string{
		"/tmp",
		"/var/tmp",
	}

	// An empty home would make this a relative path, which as a walk root
	// means "wherever the process happens to be running from".
	if homeDir != "" {
		roots = append(roots, filepath.Join(homeDir, "Library", "Caches", "TemporaryItems"))
	}

	if userTmp, ok := userTempRoot(tmpDir, euid); ok {
		roots = append(roots, userTmp)
	}

	return resolveScanRoots(roots)
}

func (c *TempCleaner) Category() Category { return CategoryTemp }

func (c *TempCleaner) Name() string { return "Temp Files" }

func (c *TempCleaner) Description() string { return "System and user temporary files" }

func (c *TempCleaner) RequiresSudo() bool { return true }

func (c *TempCleaner) DeletesWholeDomain() bool { return false }

// legitimateTempRoots are the only places macOS ever puts a per-user temp
// directory. /tmp and /private/tmp are already scanned unconditionally; they
// are listed so an explicit TMPDIR pointing at them is not treated as hostile.
var legitimateTempRoots = []string{
	"/var/folders",
	"/private/var/folders",
	"/tmp",
	"/private/tmp",
}

// userTempRoot validates $TMPDIR before it is allowed to become a scan root.
//
// Two independent rules, both about the same risk -- an environment variable
// deciding what a root process walks and offers up for deletion:
//
//  1. it must live under a real macOS temp root, so "TMPDIR=$HOME/Documents"
//     cannot turn a user's documents into temp-file candidates;
//  2. when euid is 0 it is dropped entirely. The elevated helper already covers
//     /tmp and /var/tmp explicitly, and an env-derived root has no business in
//     a scan whose results a root process is about to delete.
//
// It returns the cleaned path to use, or ok=false to skip it.
func userTempRoot(tmpDir string, euid int) (string, bool) {
	if euid == 0 {
		return "", false
	}
	if tmpDir == "" {
		return "", false
	}

	cleaned := filepath.Clean(tmpDir)
	if !filepath.IsAbs(cleaned) {
		return "", false
	}
	// Already walked unconditionally; adding it again would double-count.
	if cleaned == "/tmp" {
		return "", false
	}

	for _, root := range legitimateTempRoots {
		if cleaned == root || strings.HasPrefix(cleaned, root+"/") {
			return cleaned, true
		}
	}
	return "", false
}

func (c *TempCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	start := time.Now()
	result := &ScanResult{
		Category: CategoryTemp,
	}

	for _, root := range c.roots {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsPermission(err) {
					result.Errors = append(result.Errors, err)
					return fs.SkipDir // skip directories we can't access
				}
				return nil
			}

			// Check for cancellation
			if ctx.Err() != nil {
				return fs.SkipAll
			}

			// Only consider files, skip directories for now
			if d.IsDir() {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				result.Errors = append(result.Errors, err)
				return nil
			}

			// Only regular files. A symlink, socket or FIFO reports a size
			// that is not reclaimable space, and offering one as a deletion
			// candidate makes Clean refuse it on every run -- it cannot tell
			// an enumerated symlink from one swapped in to redirect a
			// deletion, and must assume the latter.
			if !info.Mode().IsRegular() {
				return nil
			}

			dev, ino, _ := fileIdentity(info)

			result.Entries = append(result.Entries, FileEntry{
				Path:     path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Category: CategoryTemp,
				Dev:      dev,
				Ino:      ino,
			})

			result.TotalSize += info.Size()
			result.TotalFiles++

			if progress != nil && result.TotalFiles%100 == 0 {
				progress(ScanProgress{
					Category:   CategoryTemp,
					FilesFound: result.TotalFiles,
					BytesFound: result.TotalSize,
					CurrentDir: root,
				})
			}

			return nil
		})
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (c *TempCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{
		Category: CategoryTemp,
		DryRun:   dryRun,
	}

	// Deletion never re-resolves entry.Path from "/": see rootedRemover for
	// why a root process must not, and what it is confined to instead.
	remover := newRootedRemover(c.roots...)
	defer remover.Close()

	for i, entry := range entries {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		if entry.IsDir {
			continue // skip directories for now
		}

		if !dryRun {
			if err := remover.Remove(entry); err != nil && !os.IsNotExist(err) {
				result.Errors = append(result.Errors, err)
				continue
			}
		}

		result.FilesDeleted++
		result.BytesFreed += entry.Size

		// Update progress every 50 files or on the last file
		if progress != nil && (i%50 == 0 || i == len(entries)-1) {
			progress(CleanProgress{
				Category:     CategoryTemp,
				FilesDeleted: result.FilesDeleted,
				FilesTotal:   len(entries),
				BytesDeleted: result.BytesFreed,
				BytesTotal:   totalSize(entries),
				CurrentFile:  entry.Path,
			})
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}
