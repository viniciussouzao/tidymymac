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
	}
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

	paths := []string{
		"/tmp",
		"/var/tmp",
		filepath.Join(c.homeDir, "Library", "Caches", "TemporaryItems"),
	}

	// os.TempDir() is just $TMPDIR: attacker-settable, and this cleaner
	// RequiresSudo, so an unvalidated value becomes a root-walked scan root and
	// therefore a root-deletable domain (the elevated helper's fence 2 is
	// exactly "whatever Scan returned"). Accept it only when it really is a
	// macOS temp location, and never at all when elevated.
	if userTmp, ok := userTempRoot(os.TempDir(), os.Geteuid()); ok {
		paths = append(paths, userTmp)
	}

	for _, root := range paths {
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

			result.Entries = append(result.Entries, FileEntry{
				Path:     path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Category: CategoryTemp,
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

	for i, entry := range entries {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		if entry.IsDir {
			continue // skip directories for now
		}

		if !dryRun {
			if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
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
