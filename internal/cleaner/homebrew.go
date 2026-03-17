package cleaner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type HomebrewCleaner struct{}

// NewHomebrewCleaner creates a new instance of HomebrewCleaner.
func NewHomebrewCleaner() *HomebrewCleaner {
	return &HomebrewCleaner{}
}

func (c *HomebrewCleaner) Category() Category { return CategoryHomebrew }

func (c *HomebrewCleaner) Name() string { return "Homebrew Cache" }

func (c *HomebrewCleaner) Description() string { return "Old formula versions and cache" }

func (c *HomebrewCleaner) RequiresSudo() bool { return false }

func (c *HomebrewCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	start := time.Now()
	result := &ScanResult{
		Category: CategoryHomebrew,
	}

	if _, err := exec.LookPath("brew"); err != nil {
		return result, nil // Homebrew not installed, just return empty result
	}

	cachePath, err := exec.Command("brew", "--cache").Output()
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("brew --cache: %w", err))
		result.Duration = time.Since(start)
		return result, nil
	}

	cacheDir := strings.TrimSpace(string(cachePath))
	if cacheDir == "" {
		result.Duration = time.Since(start)
		return result, nil
	}

	// Walk the cache directory.
	_ = filepath.WalkDir(cacheDir, func(path string, d fs.DirEntry, err error) error {
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
			Category: CategoryHomebrew,
		})
		result.TotalSize += info.Size()
		result.TotalFiles++

		return nil
	})

	result.Duration = time.Since(start)
	return result, nil
}

// Clean runs "brew cleanup" to remove old cache files. It then checks which files were actually removed based on the entries provided.
func (c *HomebrewCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{Category: CategoryHomebrew, DryRun: dryRun}

	if dryRun {
		// Report what brew cleanup would do.
		out, err := exec.CommandContext(ctx, "brew", "cleanup", "-n").Output()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("brew cleanup -n: %w", err))
		}
		// Count lines as approximate files.
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, entry := range entries {
			result.FilesDeleted++
			result.BytesFreed += entry.Size
		}
		_ = lines
		result.Duration = time.Since(start)
		return result, nil
	}

	// Actual cleanup via brew.
	cmd := exec.CommandContext(ctx, "brew", "cleanup")
	if out, err := cmd.CombinedOutput(); err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("brew cleanup: %s: %w", string(out), err))
	}

	// Report freed based on entries (cache files that no longer exist).
	for _, entry := range entries {
		if _, err := os.Stat(entry.Path); os.IsNotExist(err) {
			result.FilesDeleted++
			result.BytesFreed += entry.Size
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}
