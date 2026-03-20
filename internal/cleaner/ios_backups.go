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

// Scan identifies top-level iOS backup directories, calculating each one's total size.
// Each backup is represented as a single FileEntry with IsDir=true so that Clean
// can remove the entire directory at once instead of individual files.
func (c *IOSBackupsCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if c.homeDir == "" {
		return &ScanResult{Category: CategoryIOSBackups}, nil
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryIOSBackups}

	backupRoot := filepath.Join(c.homeDir, "Library", "Application Support", "MobileSync", "Backup")

	dirEntries, err := os.ReadDir(backupRoot)
	if err != nil {
		if os.IsNotExist(err) {
			result.Duration = time.Since(start)
			return result, nil
		}
		result.Errors = append(result.Errors, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	for _, dirEntry := range dirEntries {
		if !dirEntry.IsDir() {
			continue
		}
		if ctx.Err() != nil {
			break
		}

		info, err := dirEntry.Info()
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		backupDir := filepath.Join(backupRoot, dirEntry.Name())

		var dirSize int64
		_ = filepath.WalkDir(backupDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsPermission(err) {
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
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			dirSize += fi.Size()
			return nil
		})

		result.Entries = append(result.Entries, FileEntry{
			Path:     backupDir,
			Size:     dirSize,
			IsDir:    true,
			ModTime:  info.ModTime(),
			Category: CategoryIOSBackups,
		})
		result.TotalSize += dirSize
		result.TotalFiles++

		if progress != nil {
			progress(ScanProgress{
				Category:   CategoryIOSBackups,
				FilesFound: result.TotalFiles,
				BytesFound: result.TotalSize,
				CurrentDir: backupRoot,
			})
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// Clean removes each iOS backup directory entirely. Each entry produced by Scan has
// IsDir=true, so os.RemoveAll is used to delete the whole backup folder at once.
func (c *IOSBackupsCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{Category: CategoryIOSBackups, DryRun: dryRun}
	total := totalSize(entries)

	for _, entry := range entries {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if !dryRun {
			if err := os.RemoveAll(entry.Path); err != nil && !os.IsNotExist(err) {
				result.Errors = append(result.Errors, err)
				continue
			}
		}
		result.FilesDeleted++
		result.BytesFreed += entry.Size

		if progress != nil {
			progress(CleanProgress{
				Category:     CategoryIOSBackups,
				FilesDeleted: result.FilesDeleted,
				FilesTotal:   len(entries),
				BytesDeleted: result.BytesFreed,
				BytesTotal:   total,
				CurrentFile:  entry.Path,
			})
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}
