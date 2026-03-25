package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// XcodeCleaner scans and cleans Xcode derived data, archives, and simulators.
type XcodeCleaner struct {
	homeDir string
}

// NewXcodeCleaner creates an XcodeCleaner instance.
func NewXcodeCleaner() *XcodeCleaner {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return &XcodeCleaner{homeDir: home}
}

func (c *XcodeCleaner) Category() Category { return CategoryXcode }

func (c *XcodeCleaner) Name() string { return "Xcode" }

func (c *XcodeCleaner) Description() string { return "DerivedData, archives, simulators" }

func (c *XcodeCleaner) RequiresSudo() bool { return false }

// Scan looks for Xcode-related files in common locations and calculates their total size.
func (c *XcodeCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if c.homeDir == "" {
		return &ScanResult{Category: CategoryXcode}, nil
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryXcode}

	paths := []string{
		filepath.Join(c.homeDir, "Library", "Developer", "Xcode", "DerivedData"),
		filepath.Join(c.homeDir, "Library", "Developer", "Xcode", "Archives"),
		filepath.Join(c.homeDir, "Library", "Developer", "Xcode", "iOS DeviceSupport"),
		filepath.Join(c.homeDir, "Library", "Developer", "CoreSimulator", "Caches"),
	}

	for _, root := range paths {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
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

			result.Entries = append(result.Entries, FileEntry{
				Path:     path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Category: CategoryXcode,
			})
			result.TotalSize += info.Size()
			result.TotalFiles++

			if progress != nil && result.TotalFiles%100 == 0 {
				progress(ScanProgress{
					Category:   CategoryXcode,
					FilesFound: result.TotalFiles,
					BytesFound: result.TotalSize,
					CurrentDir: filepath.Dir(path),
				})
			}

			return nil
		})
	}

	result.Duration = time.Since(start)
	return result, nil
}

// Clean deletes the specified Xcode-related files and updates the result with the total bytes freed.
func (c *XcodeCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	start := time.Now()
	result := &CleanResult{Category: CategoryXcode, DryRun: dryRun}

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
				Category:     CategoryXcode,
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
