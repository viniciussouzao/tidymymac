package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// TempCleaner scans and cleans temporary files
type TempCleaner struct {
	homeDir string
}

func NewTempCleaner() *TempCleaner {
	home, _ := os.UserHomeDir()
	return &TempCleaner{
		homeDir: home,
	}
}

func (c *TempCleaner) Category() Category { return CategoryTemp }

func (c *TempCleaner) Name() string { return "Temp Files" }

func (c *TempCleaner) Description() string { return "System and user temporary files" }

func (c *TempCleaner) RequiresSudo() bool { return true }

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

	userTmp := os.TempDir()
	if userTmp != "/tmp" {
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
