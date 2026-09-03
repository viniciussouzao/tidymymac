package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/homedir"
)

// LogsCleaner scans and cleans system and user log files.
type LogsCleaner struct {
	homeDir string

	// roots is the cleaner's domain: the only directories Scan walks and the
	// only ones Clean will delete inside. Resolved once at construction so the
	// two can never disagree.
	roots []string
}

// NewLogsCleaner creates a LogsCleaner using the current user's home
// directory. It resolves via homedir.Resolve rather than os.UserHomeDir
// because this cleaner requires sudo: when the process runs elevated,
// os.UserHomeDir would resolve to root's home (/var/root) and the cleaner
// would scan and clean the wrong home.
func NewLogsCleaner() *LogsCleaner {
	home, err := homedir.Resolve()
	if err != nil {
		home = ""
	}
	return &LogsCleaner{homeDir: home, roots: logsScanRoots(home)}
}

// logsScanRoots resolves the Logs domain. With no home directory there is no
// domain at all: the system roots alone would let Clean delete under /var/log
// on the strength of a scan that never established a user context.
func logsScanRoots(homeDir string) []string {
	if homeDir == "" {
		return nil
	}
	return resolveScanRoots([]string{
		filepath.Join(homeDir, "Library", "Logs"),
		"/Library/Logs",
		"/var/log",
	})
}

func (c *LogsCleaner) Category() Category       { return CategoryLogs }
func (c *LogsCleaner) Name() string             { return "System Logs" }
func (c *LogsCleaner) Description() string      { return "Application and system log files" }
func (c *LogsCleaner) RequiresSudo() bool       { return true }
func (c *LogsCleaner) DeletesWholeDomain() bool { return false }

// Scan walks through common log directories and collects information about log files.
func (c *LogsCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if c.homeDir == "" {
		return &ScanResult{Category: CategoryLogs}, nil
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryLogs}

	for _, root := range c.roots {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
				return nil
			}

			// Only regular files -- see the equivalent note in temp.go.
			// /var/log and ~/Library/Logs routinely hold symlinks and sockets.
			if !info.Mode().IsRegular() {
				return nil
			}

			dev, ino, _ := fileIdentity(info)

			entry := FileEntry{
				Path:     path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Category: CategoryLogs,
				Dev:      dev,
				Ino:      ino,
			}
			result.Entries = append(result.Entries, entry)
			result.TotalSize += info.Size()
			result.TotalFiles++

			if progress != nil && result.TotalFiles%100 == 0 {
				progress(ScanProgress{
					Category:   CategoryLogs,
					FilesFound: result.TotalFiles,
					BytesFound: result.TotalSize,
					CurrentDir: filepath.Dir(path),
				})
			}

			return nil
		})
	}

	result.Duration = time.Since(start)

	if progress != nil {
		progress(ScanProgress{
			Category:   CategoryLogs,
			FilesFound: result.TotalFiles,
			BytesFound: result.TotalSize,
		})
	}

	return result, nil
}

// Clean deletes the specified log files and updates the result with the number of files deleted and bytes freed.
func (c *LogsCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{
		Category: CategoryLogs,
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
			continue
		}

		if !dryRun {
			if err := remover.Remove(entry); err != nil {
				if !os.IsNotExist(err) {
					result.Errors = append(result.Errors, err)
					continue
				}
			}
		}

		result.FilesDeleted++
		result.BytesFreed += entry.Size

		if progress != nil && (i%100 == 0 || i == len(entries)-1) {
			progress(CleanProgress{
				Category:     CategoryLogs,
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
