package cleaner

import (
	"context"
	"io/fs"
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
				_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
					if err != nil {
						// Skip subtrees on permission issues.
						if os.IsPermission(err) {
							return fs.SkipDir
						}
						return nil
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

	var err error
	if !dryRun {
		osascript := `tell application "Finder" to empty the trash`
		cmd := exec.Command("osascript", "-e", osascript)
		err = cmd.Run()
		if err != nil {
			result.Errors = append(result.Errors, err)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}
