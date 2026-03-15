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
