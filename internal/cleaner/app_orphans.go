package cleaner

import (
	"bytes"
	"context"
	"encoding/xml"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

var appOrphanLibraryDirs = []string{
	"Application Support",
	"Caches",
	"Preferences",
	"Logs",
	"Saved Application State",
	"HTTPStorages",
	"WebKit",
}

// AppOrphansCleaner scans and cleans high-confidence leftovers from apps no longer installed.
type AppOrphansCleaner struct {
	homeDir         string
	appSearchRoots  []string
	bundleIDReader  func(context.Context, string) (string, error)
	pathSizeFetcher func(context.Context, string) (int64, error)
}

// NewAppOrphansCleaner creates a new instance of AppOrphansCleaner.
func NewAppOrphansCleaner() *AppOrphansCleaner {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	return &AppOrphansCleaner{
		homeDir:         home,
		appSearchRoots:  defaultAppSearchRoots(home),
		bundleIDReader:  readAppBundleID,
		pathSizeFetcher: getPathSize,
	}
}

func (c *AppOrphansCleaner) Category() Category { return CategoryAppOrphans }

func (c *AppOrphansCleaner) Name() string { return "App Orphans" }

func (c *AppOrphansCleaner) Description() string {
	return "Leftover files from apps no longer installed"
}

func (c *AppOrphansCleaner) RequiresSudo() bool { return false }

func (c *AppOrphansCleaner) setDefaults() {
	if len(c.appSearchRoots) == 0 {
		c.appSearchRoots = defaultAppSearchRoots(c.homeDir)
	}
	if c.bundleIDReader == nil {
		c.bundleIDReader = readAppBundleID
	}
	if c.pathSizeFetcher == nil {
		c.pathSizeFetcher = getPathSize
	}
}

func (c *AppOrphansCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if c.homeDir == "" {
		return &ScanResult{Category: CategoryAppOrphans}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.setDefaults()

	start := time.Now()
	result := &ScanResult{Category: CategoryAppOrphans}

	installed, err := c.installedBundleIDs(ctx)
	if err != nil {
		result.Errors = append(result.Errors, err)
	}

	libraryDir := filepath.Join(c.homeDir, "Library")
	for _, rel := range appOrphanLibraryDirs {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		root := filepath.Join(libraryDir, rel)
		c.scanLibraryRoot(ctx, root, installed, result)

		if progress != nil {
			progress(ScanProgress{
				Category:   CategoryAppOrphans,
				FilesFound: result.TotalFiles,
				BytesFound: result.TotalSize,
				CurrentDir: root,
			})
		}
	}

	result.Duration = time.Since(start)
	if progress != nil {
		progress(ScanProgress{
			Category:   CategoryAppOrphans,
			FilesFound: result.TotalFiles,
			BytesFound: result.TotalSize,
			CurrentDir: libraryDir,
		})
	}

	return result, nil
}

func (c *AppOrphansCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	result := &CleanResult{Category: CategoryAppOrphans, DryRun: dryRun}
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
				Category:     CategoryAppOrphans,
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

func (c *AppOrphansCleaner) installedBundleIDs(ctx context.Context) (map[string]struct{}, error) {
	installed := map[string]struct{}{}
	var firstErr error

	for _, root := range c.appSearchRoots {
		if err := ctx.Err(); err != nil {
			return installed, err
		}

		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsPermission(err) {
					if firstErr == nil {
						firstErr = err
					}
					return fs.SkipDir
				}
				return nil
			}
			if ctx.Err() != nil {
				return fs.SkipAll
			}
			if !d.IsDir() || filepath.Ext(path) != ".app" {
				return nil
			}

			bundleID, err := c.bundleIDReader(ctx, filepath.Join(path, "Contents", "Info.plist"))
			if err == nil && isValidThirdPartyBundleID(bundleID) {
				installed[bundleID] = struct{}{}
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}

	return installed, firstErr
}

func (c *AppOrphansCleaner) scanLibraryRoot(ctx context.Context, root string, installed map[string]struct{}, result *ScanResult) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			result.Errors = append(result.Errors, err)
		}
		return
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			result.Errors = append(result.Errors, err)
			return
		}

		bundleID, ok := orphanCandidateBundleID(root, entry.Name())
		if !ok {
			continue
		}
		if _, ok := installed[bundleID]; ok {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}

		path := filepath.Join(root, entry.Name())
		isDir := info.IsDir()
		size := info.Size()
		if isDir {
			size, err = c.pathSizeFetcher(ctx, path)
			if err != nil {
				result.Errors = append(result.Errors, err)
				continue
			}
		}

		result.Entries = append(result.Entries, FileEntry{
			Path:     path,
			Size:     size,
			IsDir:    isDir,
			ModTime:  info.ModTime(),
			Category: CategoryAppOrphans,
		})
		result.TotalSize += size
		result.TotalFiles++
	}
}

func defaultAppSearchRoots(homeDir string) []string {
	roots := []string{"/Applications"}
	if homeDir != "" {
		roots = append(roots, filepath.Join(homeDir, "Applications"))
	}
	roots = append(roots, "/System/Applications")
	return roots
}

func orphanCandidateBundleID(root, name string) (string, bool) {
	base := name
	switch filepath.Base(root) {
	case "Preferences":
		if !strings.HasSuffix(base, ".plist") {
			return "", false
		}
		base = strings.TrimSuffix(base, ".plist")
	case "Saved Application State":
		if !strings.HasSuffix(base, ".savedState") {
			return "", false
		}
		base = strings.TrimSuffix(base, ".savedState")
	}

	if !isValidThirdPartyBundleID(base) {
		return "", false
	}
	return base, true
}

func isValidThirdPartyBundleID(bundleID string) bool {
	if bundleID == "" || strings.HasPrefix(bundleID, "com.apple.") {
		return false
	}

	parts := strings.Split(bundleID, ".")
	if len(parts) < 3 {
		return false
	}

	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-') {
				return false
			}
		}
	}

	return true
}

func readAppBundleID(ctx context.Context, plistPath string) (string, error) {
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return "", err
	}

	if bundleID := readXMLBundleID(data); bundleID != "" {
		return bundleID, nil
	}

	cmd := exec.CommandContext(ctx, "plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", plistPath)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

func readXMLBundleID(data []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var previousKey string

	for {
		tok, err := decoder.Token()
		if err != nil {
			return ""
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "key":
			var key string
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return ""
			}
			previousKey = key
		case "string":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return ""
			}
			if previousKey == "CFBundleIdentifier" {
				return strings.TrimSpace(value)
			}
			previousKey = ""
		}
	}
}
