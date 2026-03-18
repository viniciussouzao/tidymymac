package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type CachesCleaner struct {
	homeDir string
}

func NewCachesCleaner() *CachesCleaner {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // fallback to empty string if we can't get the home directory
	}
	return &CachesCleaner{homeDir: home}
}

func (c *CachesCleaner) Category() Category { return CategoryCaches }

func (c *CachesCleaner) Name() string { return "App Caches" }

func (c *CachesCleaner) Description() string { return "Browser and application cache files" }

func (c *CachesCleaner) RequiresSudo() bool { return false }

func (c *CachesCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	start := time.Now()
	result := &ScanResult{Category: CategoryCaches}

	cachesDir := filepath.Join(c.homeDir, "Library", "Caches")

	_ = filepath.WalkDir(cachesDir, func(path string, d fs.DirEntry, err error) error {
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
			Category: CategoryCaches,
		}
		result.Entries = append(result.Entries, entry)
		result.TotalSize += info.Size()
		result.TotalFiles++

		if progress != nil && result.TotalFiles%200 == 0 {
			progress(ScanProgress{
				Category:   CategoryCaches,
				FilesFound: result.TotalFiles,
				BytesFound: result.TotalSize,
				CurrentDir: filepath.Dir(path),
			})
		}

		return nil
	})

	result.Duration = time.Since(start)

	if progress != nil {
		progress(ScanProgress{
			Category:   CategoryCaches,
			FilesFound: result.TotalFiles,
			BytesFound: result.TotalSize,
		})
	}

	return result, nil
}

func (c *CachesCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{
		Category: CategoryCaches,
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

		if progress != nil && (i%100 == 0 || i == len(entries)-1) {
			progress(CleanProgress{
				Category:     CategoryCaches,
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
