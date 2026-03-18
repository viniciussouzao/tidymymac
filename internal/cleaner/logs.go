package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// LogsCleaner scans and cleans system and user log files.
type LogsCleaner struct {
	homeDir string
}

// NewLogsCleaner creates a LogsCleaner using the current user's home directory.
func NewLogsCleaner() *LogsCleaner {
	home, _ := os.UserHomeDir()
	return &LogsCleaner{homeDir: home}
}

func (c *LogsCleaner) Category() Category  { return CategoryLogs }
func (c *LogsCleaner) Name() string        { return "System Logs" }
func (c *LogsCleaner) Description() string { return "Application and system log files" }
func (c *LogsCleaner) RequiresSudo() bool  { return true }

func (c *LogsCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	start := time.Now()
	result := &ScanResult{Category: CategoryLogs}

	paths := []string{
		filepath.Join(c.homeDir, "Library", "Logs"),
		"/Library/Logs",
		"/var/log",
	}

	for _, root := range paths {
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

			entry := FileEntry{
				Path:     path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Category: CategoryLogs,
			}
			result.Entries = append(result.Entries, entry)
			result.TotalSize += info.Size()
			result.TotalFiles++

			if progress != nil && result.TotalFiles%100 == 0 {
				progress(ScanProgress{
					Category:   CategoryLogs,
					FilesFound: result.TotalFiles,
					BytesFound: result.TotalSize,
					CurrentDir: root,
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

func (c *LogsCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{
		Category: CategoryLogs,
		DryRun:   dryRun,
	}

	for i, entry := range entries {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		if entry.IsDir {
			continue
		}

		if !dryRun {
			if err := os.Remove(entry.Path); err != nil {
				if !os.IsNotExist(err) {
					result.Errors = append(result.Errors, err)
				}
				continue
			}
		}

		result.FilesDeleted++
		result.BytesFreed += entry.Size

		if progress != nil && (i%50 == 0 || i == len(entries)-1) {
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
