package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// IOSBackupsCleaner is a cleaner that identifies and removes old iOS device backups stored on the Mac.
type IOSBackupsCleaner struct {
	homeDir string
}

// NewIOSBackupsCleaner creates a new instance of IOSBackupsCleaner
func NewIOSBackupsCleaner() *IOSBackupsCleaner {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	return &IOSBackupsCleaner{
		homeDir: home,
	}
}

func (c *IOSBackupsCleaner) Category() Category { return CategoryIOSBackups }

func (c *IOSBackupsCleaner) Name() string { return "iOS Backups" }

func (c *IOSBackupsCleaner) Description() string { return "Old iPhone/iPad backups stored on your Mac" }

func (c *IOSBackupsCleaner) RequiresSudo() bool { return false }

// Scan identifies iOS backup files and directories, calculating their total size and count.
func (c *IOSBackupsCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if c.homeDir == "" {
		return &ScanResult{Category: CategoryIOSBackups}, nil
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryIOSBackups}

	roots := []string{
		filepath.Join(c.homeDir, "Library", "Application Support", "MobileSync", "Backup"),
	}

	for _, root := range roots {
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
				Category: CategoryIOSBackups,
			})

			result.TotalSize += info.Size()
			result.TotalFiles++

			if progress != nil && result.TotalFiles%100 == 0 {
				progress(ScanProgress{
					Category:   CategoryIOSBackups,
					FilesFound: result.TotalFiles,
					BytesFound: result.TotalSize,
					CurrentDir: path,
				})
			}
			return nil
		})
	}

	result.Duration = time.Since(start)
	return result, nil
}

// Clean removes the identified iOS backup files, tracking the number of files deleted and total bytes freed.
func (c *IOSBackupsCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{Category: CategoryIOSBackups, DryRun: dryRun}

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

		if progress != nil && (i%100 == 0 || i == len(entries)-1) {
			progress(CleanProgress{
				Category:     CategoryIOSBackups,
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
