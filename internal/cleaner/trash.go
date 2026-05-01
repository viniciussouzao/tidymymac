package cleaner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// TrashCleaner scans and empties the user's Trash.
// It focuses on top-level trashed items (files and directories) to avoid double counting.
type TrashCleaner struct {
	homeDir string
}

// NewTrashCleaner creates a TrashCleaner.
func NewTrashCleaner() *TrashCleaner {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	return &TrashCleaner{homeDir: home}
}

func (c *TrashCleaner) Category() Category { return CategoryTrashBin }

func (c *TrashCleaner) Name() string { return "Trash" }

func (c *TrashCleaner) Description() string { return "Empty the Trash" }

func (c *TrashCleaner) RequiresSudo() bool { return false } // try to empty the user's Trash without sudo using osascript

func (c *TrashCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryTrashBin}

	// Candidate trash locations:
	// - ~/.Trash (boot volume)
	// - iCloud Drive Trash (if present): ~/Library/Mobile Documents/com~apple~CloudDocs/.Trash
	// - /Volumes/*/.Trashes/<uid> (external/removable volumes)
	roots := []string{filepath.Join(c.homeDir, ".Trash")}

	// iCloud Drive Trash
	roots = append(roots, filepath.Join(c.homeDir, "Library", "Mobile Documents", "com~apple~CloudDocs", ".Trash"))

	// For each trash root, list top-level entries and measure sizes.
	for _, root := range roots {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				result.Errors = append(result.Errors, err)
				continue
			}

			result.Errors = append(result.Errors, err)
			continue
		}

		for _, e := range entries {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}

			path := filepath.Join(root, e.Name())
			info, err := e.Info()
			if err != nil {
				if os.IsPermission(err) {
					result.Errors = append(result.Errors, err)
					continue
				}
				result.Errors = append(result.Errors, err)
				continue
			}

			isDir := info.IsDir()
			size := info.Size()
			if isDir {
				// Compute directory size to present accurate savings.
				var dirSize int64
				dirSize, err := getPathSize(ctx, path)
				if err != nil {
					result.Errors = append(result.Errors, err)
					continue
				}

				size = dirSize
			}

			result.Entries = append(result.Entries, FileEntry{
				Path:     path,
				Size:     size,
				IsDir:    isDir,
				ModTime:  info.ModTime(),
				Category: CategoryTrashBin,
			})
			result.TotalSize += size
			result.TotalFiles++

			if progress != nil && result.TotalFiles%100 == 0 {
				progress(ScanProgress{
					Category:   CategoryTrashBin,
					FilesFound: result.TotalFiles,
					BytesFound: result.TotalSize,
					CurrentDir: root,
				})
			}
		}
	}

	result.Duration = time.Since(start)
	if progress != nil {
		progress(ScanProgress{
			Category:   CategoryTrashBin,
			FilesFound: result.TotalFiles,
			BytesFound: result.TotalSize,
		})
	}
	return result, nil
}

func (c *TrashCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	start := time.Now()
	result := &CleanResult{Category: CategoryTrashBin, DryRun: dryRun}
	total := totalSize(entries)

	if dryRun {
		for i, entry := range entries {
			result.FilesDeleted++
			result.BytesFreed += entry.Size

			if progress != nil && (i%10 == 0 || i == len(entries)-1) {
				progress(CleanProgress{
					Category:     CategoryTrashBin,
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

	hasAccess := checkFullDiskAccess(c.homeDir)
	if !hasAccess.FullDiskAccess {
		// If we don't have Full Disk Access, we use osascript to empty the Trash as a fallback.
		// In tradeoff, we won't be able to report progress or handle individual entry errors, but at least we can attempt to free up space.
		cmd := exec.CommandContext(ctx, "osascript", "-e", `tell application "Finder" to empty trash with security`)
		err := cmd.Run()
		if err != nil {
			result.Errors = append(result.Errors, err)
		}

		result.Duration = time.Since(start)
		return result, nil
	}

	for i, entry := range entries {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		err := os.RemoveAll(entry.Path)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		result.FilesDeleted++
		result.BytesFreed += entry.Size

		if progress != nil && (i%10 == 0 || i == len(entries)-1) {
			progress(CleanProgress{
				Category:     CategoryTrashBin,
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
