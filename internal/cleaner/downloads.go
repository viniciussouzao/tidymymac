package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const downloadsLargeItemThreshold int64 = 100 * 1024 * 1024

// DownloadsCleaner scans and cleans installers and large items in the Downloads folder.
type DownloadsCleaner struct {
	homeDir string
}

// NewDownloadsCleaner creates a new instance of DownloadsCleaner.
func NewDownloadsCleaner() *DownloadsCleaner {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	return &DownloadsCleaner{homeDir: home}
}

func (c *DownloadsCleaner) Category() Category { return CategoryDownloads }

func (c *DownloadsCleaner) Name() string { return "Downloads" }

func (c *DownloadsCleaner) Description() string {
	return "Installer files and large items in the Downloads folder"
}

func (c *DownloadsCleaner) RequiresSudo() bool { return false }

func (c *DownloadsCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if c.homeDir == "" {
		return &ScanResult{Category: CategoryDownloads}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryDownloads}
	downloadsDir := filepath.Join(c.homeDir, "Downloads")

	entries, err := os.ReadDir(downloadsDir)
	if err != nil {
		if os.IsNotExist(err) {
			result.Duration = time.Since(start)
			if progress != nil {
				progress(ScanProgress{Category: CategoryDownloads})
			}
			return result, nil
		}

		result.Errors = append(result.Errors, err)
		result.Duration = time.Since(start)
		if progress != nil {
			progress(ScanProgress{Category: CategoryDownloads})
		}
		return result, nil
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		path := filepath.Join(downloadsDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		isDir := info.IsDir()
		size := info.Size()
		if isDir {
			size, err = getPathSize(ctx, path)
			if err != nil {
				result.Errors = append(result.Errors, err)
				continue
			}
		}

		if !shouldIncludeDownloadEntry(path, size, isDir) {
			continue
		}

		result.Entries = append(result.Entries, FileEntry{
			Path:     path,
			Size:     size,
			IsDir:    isDir,
			ModTime:  info.ModTime(),
			Category: CategoryDownloads,
		})
		result.TotalSize += size
		result.TotalFiles++

		if progress != nil && result.TotalFiles%25 == 0 {
			progress(ScanProgress{
				Category:   CategoryDownloads,
				FilesFound: result.TotalFiles,
				BytesFound: result.TotalSize,
				CurrentDir: downloadsDir,
			})
		}
	}

	result.Duration = time.Since(start)
	if progress != nil {
		progress(ScanProgress{
			Category:   CategoryDownloads,
			FilesFound: result.TotalFiles,
			BytesFound: result.TotalSize,
			CurrentDir: downloadsDir,
		})
	}

	return result, nil
}

func (c *DownloadsCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	result := &CleanResult{Category: CategoryDownloads, DryRun: dryRun}
	total := totalSize(entries)

	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		if !dryRun {
			var err error
			if entry.IsDir {
				err = os.RemoveAll(entry.Path)
			} else {
				err = os.Remove(entry.Path)
			}
			if err != nil && !os.IsNotExist(err) {
				result.Errors = append(result.Errors, err)
				continue
			}
		}

		result.FilesDeleted++
		result.BytesFreed += entry.Size

		if progress != nil && (i%10 == 0 || i == len(entries)-1) {
			progress(CleanProgress{
				Category:     CategoryDownloads,
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

func shouldIncludeDownloadEntry(path string, size int64, isDir bool) bool {
	if !isDir && isInstallerPath(path) {
		return true
	}

	return size > downloadsLargeItemThreshold
}

func isInstallerPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".dmg" || ext == ".pkg"
}
