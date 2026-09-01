package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/homedir"
)

// UpdatesCleaner is a cleaner that targets old macOS update residues and installers.
type UpdatesCleaner struct {
	homeDir string
}

// NewUpdatesCleaner creates a new instance of UpdatesCleaner with the user's
// home directory. It resolves via homedir.Resolve rather than os.UserHomeDir
// because this cleaner requires sudo: when the process runs elevated,
// os.UserHomeDir would resolve to root's home (/var/root) and the cleaner
// would scan and clean the wrong home.
func NewUpdatesCleaner() *UpdatesCleaner {
	home, err := homedir.Resolve()
	if err != nil {
		home = ""
	}
	return &UpdatesCleaner{
		homeDir: home,
	}
}

func (c *UpdatesCleaner) Category() Category { return CategoryUpdates }

func (c *UpdatesCleaner) Name() string { return "macOS Updates" }

func (c *UpdatesCleaner) Description() string { return "Old macOS update residues and installers" }

func (c *UpdatesCleaner) RequiresSudo() bool { return true }

func (c *UpdatesCleaner) DeletesWholeDomain() bool { return false }

func (c *UpdatesCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if ctx.Err() != nil {
		return &ScanResult{Category: CategoryUpdates}, ctx.Err()
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryUpdates}

	paths := []string{
		filepath.Join(c.homeDir, "Library", "Updates"),
		filepath.Join(c.homeDir, "Library", "iTunes", "iPad Software Updates"),
		filepath.Join(c.homeDir, "Library", "iTunes", "iPhone Software Updates"),
	}

	for _, path := range paths {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		// Check if the path exists before trying to scan it to avoid unnecessary errors
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}

		_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				if os.IsPermission(err) {
					result.Errors = append(result.Errors, err)
					return fs.SkipDir
				}
				return nil
			}

			if ctx.Err() != nil {
				return fs.SkipAll
			}

			if d.IsDir() {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				result.Errors = append(result.Errors, err)
				return nil
			}

			// p, not path: path is the walk ROOT of the enclosing loop.
			// Recording the root here made every entry claim to be the
			// directory itself, which is both wrong reporting and, under
			// elevation, a path the fresh-scan intersection would happily
			// match.
			result.Entries = append(result.Entries, FileEntry{
				Path:     p,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				IsDir:    d.IsDir(),
				Category: CategoryUpdates,
			})

			result.TotalSize += info.Size()
			result.TotalFiles++
			return nil
		})
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (c *UpdatesCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	if ctx.Err() != nil {
		return &CleanResult{Category: CategoryUpdates, DryRun: dryRun}, ctx.Err()
	}

	start := time.Now()
	result := &CleanResult{Category: CategoryUpdates, DryRun: dryRun}

	for i, entry := range entries {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if entry.IsDir {
			continue
		}
		if !dryRun {
			if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
				result.Errors = append(result.Errors, err)
				continue
			}
		}
		result.FilesDeleted++
		result.BytesFreed += entry.Size

		if progress != nil && (i%10 == 0 || i == len(entries)-1) {
			progress(CleanProgress{
				Category:     CategoryUpdates,
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
