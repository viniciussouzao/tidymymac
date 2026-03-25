package cleaner

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type goEnvFunc func(ctx context.Context, key string) (string, error)

// DevelopmentArtifactsCleaner scans removable development caches and build artifacts.
type DevelopmentArtifactsCleaner struct {
	homeDir string
	goEnv   goEnvFunc
}

// NewDevelopmentArtifactsCleaner creates a cleaner for development caches and artifacts.
func NewDevelopmentArtifactsCleaner() *DevelopmentArtifactsCleaner {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	return &DevelopmentArtifactsCleaner{
		homeDir: home,
		goEnv:   lookupGoEnv,
	}
}

func (c *DevelopmentArtifactsCleaner) Category() Category { return CategoryDevelopmentArtifacts }

func (c *DevelopmentArtifactsCleaner) Name() string { return "Development Artifacts" }

func (c *DevelopmentArtifactsCleaner) Description() string {
	return "Go build cache and downloaded module cache"
}

func (c *DevelopmentArtifactsCleaner) RequiresSudo() bool { return false }

// Scan looks for Go build and module caches in known locations.
func (c *DevelopmentArtifactsCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if c.homeDir == "" {
		return &ScanResult{Category: CategoryDevelopmentArtifacts}, nil
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryDevelopmentArtifacts}

	goCachePath, _ := c.goEnv(ctx, "GOCACHE")
	goPath, _ := c.goEnv(ctx, "GOPATH")

	// If GOCACHE or GOPATH are not set, use defaults.
	if goCachePath == "" {
		goCachePath = filepath.Join(c.homeDir, "Library", "Caches", "go-build")
	} else {
		goCachePath = normalizeGoEnvPath(goCachePath)
	}

	if goPath == "" {
		goPath = filepath.Join(c.homeDir, "go")
	} else {
		goPath = normalizeGoEnvPath(goPath)
	}

	paths := []string{
		goCachePath,
		filepath.Join(goPath, "pkg", "mod"),
	}

	for _, root := range paths {
		if ctx.Err() != nil {
			return result, ctx.Err()
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

			entry := FileEntry{
				Path:     path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Category: CategoryDevelopmentArtifacts,
			}

			result.Entries = append(result.Entries, entry)
			result.TotalSize += info.Size()
			result.TotalFiles++

			if progress != nil && result.TotalFiles%100 == 0 {
				progress(ScanProgress{
					Category:   CategoryDevelopmentArtifacts,
					FilesFound: result.TotalFiles,
					BytesFound: result.TotalSize,
					CurrentDir: root,
				})
			}
			return nil

		})
	}

	result.Duration = time.Since(start)
	if progress != nil {
		progress(ScanProgress{
			Category:   CategoryDevelopmentArtifacts,
			FilesFound: result.TotalFiles,
			BytesFound: result.TotalSize,
		})
	}

	return result, nil
}

// Clean removes scanned files and updates the result with the total bytes freed
func (c *DevelopmentArtifactsCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{Category: CategoryDevelopmentArtifacts, DryRun: dryRun}

	// first try to clean Go cache using "go clean" command, which is more efficient and handles all edge cases
	var success bool
	var err error
	if !dryRun {
		if success, err = cleanupGoCacheUsingGoClean(ctx); !success {
			// if it fails, fall back to manual file deletion
			result.Errors = append(result.Errors, err)
		}
	}

	if !success {
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

			// Update progress every 50 files or on the last file
			if progress != nil && (i%50 == 0 || i == len(entries)-1) {
				progress(CleanProgress{
					Category:     CategoryDevelopmentArtifacts,
					FilesDeleted: result.FilesDeleted,
					FilesTotal:   len(entries),
					BytesDeleted: result.BytesFreed,
					BytesTotal:   totalSize(entries),
					CurrentFile:  entry.Path,
				})
			}
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

func cleanupGoCacheUsingGoClean(ctx context.Context) (success bool, err error) {
	_, err = exec.CommandContext(ctx, "go", "clean", "-cache").Output()
	if err != nil {
		return false, err
	}

	_, err = exec.CommandContext(ctx, "go", "clean", "-modcache").Output()
	if err != nil {
		return false, err
	}

	_, err = exec.CommandContext(ctx, "go", "clean", "-testcache").Output()
	if err != nil {
		return false, err
	}

	return true, nil
}

// func cleanupGoCacheManually(ctx context.Context, entries []FileEntry) error {
// 	for _, entry := range entries {
// 		if ctx.Err() != nil {
// 			return ctx.Err()
// 		}

// 		if entry.IsDir {
// 			continue
// 		}

// 		if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
// 			return err
// 		}
// 	}

// 	return nil
// }

// normalizeGoEnvPath cleans up the output from "go env" to get a usable path.
func normalizeGoEnvPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	parts := strings.Split(value, string(os.PathListSeparator))
	if len(parts) == 0 {
		return ""
	}

	return filepath.Clean(parts[0])
}

// lookupGoEnv runs "go env" to get the value of a Go environment variable, such as GOCACHE or GOPATH.
func lookupGoEnv(ctx context.Context, key string) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "env", key).Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}
