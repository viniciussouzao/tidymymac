package cleaner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// projectArtifactsLargeFileThreshold is the default size above which a single
// regular file inside a scanned project root is reported. Deliberately much
// higher than downloadsLargeItemThreshold: a project directory legitimately
// holds large-ish assets, so only genuinely oversized files are worth
// surfacing.
const projectArtifactsLargeFileThreshold int64 = 500 * 1024 * 1024

// projectArtifactsJunkDirs are directory names that hold regenerable build
// output or dependency trees. Matched by base name anywhere under a scanned
// root, and recorded as a whole unit (the scan never descends into a match).
//
// "vendor" is deliberately absent: Go and PHP projects routinely commit it,
// so it is not regenerable-by-definition the way the rest of this list is.
// "dist", "build" and "out" carry the highest false-positive risk here --
// some projects do version real content under those names -- which is
// acceptable because nothing is deleted without a scan/review step first.
var projectArtifactsJunkDirs = map[string]struct{}{
	"node_modules":  {},
	"dist":          {},
	"build":         {},
	"out":           {},
	".next":         {},
	".nuxt":         {},
	".turbo":        {},
	".parcel-cache": {},
	"coverage":      {},
	"__pycache__":   {},
	".pytest_cache": {},
	".venv":         {},
	"venv":          {},
	"target":        {},
	".gradle":       {},
	".terraform":    {},
	".cache":        {},
}

// projectArtifactsSkipDirs are VCS internals: never matched as junk, never
// descended into. A junk-dir name occurring inside git's object store is
// repository data, not build output.
var projectArtifactsSkipDirs = map[string]struct{}{
	".git": {},
	".hg":  {},
	".svn": {},
}

// ProjectArtifactsCleaner scans user-configured project roots for regenerable
// junk directories and oversized files. Unlike every other cleaner it has no
// fixed location of its own: its roots come from a profile
// (config.ResolveProfile), so the instance registered in DefaultRegistry has
// none and scans nothing.
type ProjectArtifactsCleaner struct {
	paths            []string // absolute, validated roots to scan
	threshold        int64    // large-file threshold in bytes
	deleteLargeFiles bool     // whether Clean may delete file entries (large files are opt-in)
}

// NewProjectArtifactsCleaner creates a cleaner scanning the given roots.
// threshold <= 0 falls back to projectArtifactsLargeFileThreshold; taking it
// as a parameter keeps the door open for a per-profile setting later.
// deleteLargeFiles is false everywhere except an explicit
// "clean --include-large-files" -- see Clean.
func NewProjectArtifactsCleaner(paths []string, threshold int64, deleteLargeFiles bool) *ProjectArtifactsCleaner {
	if threshold <= 0 {
		threshold = projectArtifactsLargeFileThreshold
	}
	return &ProjectArtifactsCleaner{
		paths:            paths,
		threshold:        threshold,
		deleteLargeFiles: deleteLargeFiles,
	}
}

func (c *ProjectArtifactsCleaner) Category() Category { return CategoryProjectArtifacts }

func (c *ProjectArtifactsCleaner) Name() string { return "Project Artifacts" }

func (c *ProjectArtifactsCleaner) Description() string {
	return "Junk directories and oversized files in project paths configured via profiles"
}

func (c *ProjectArtifactsCleaner) RequiresSudo() bool { return false }

// DeletesWholeDomain is false: every entry (a junk directory or a large file)
// is independently removable, so protected_paths can strip individual entries
// without forcing the whole category to be skipped.
//
// Safe with respect to symlinks: filepath.WalkDir does not follow them (a
// symlink to a directory fails d.IsDir(), so it is never matched as a junk
// dir), and os.RemoveAll removes a symlink itself, never its target -- a junk
// dir full of symlinks pointing outside the root cannot cause deletion
// outside the root.
func (c *ProjectArtifactsCleaner) DeletesWholeDomain() bool { return false }

// Scan walks every configured root, recording two kinds of entry as whole
// units: directories whose base name is a known junk dir (never descending
// into a match), and regular files above the large-file threshold. The roots
// themselves are never recorded.
func (c *ProjectArtifactsCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryProjectArtifacts}

	report := func(currentDir string) {
		if progress == nil {
			return
		}
		progress(ScanProgress{
			Category:   CategoryProjectArtifacts,
			FilesFound: result.TotalFiles,
			BytesFound: result.TotalSize,
			CurrentDir: currentDir,
		})
	}

	if len(c.paths) == 0 {
		result.Duration = time.Since(start)
		report("")
		return result, nil
	}

	add := func(entry FileEntry) {
		result.Entries = append(result.Entries, entry)
		result.TotalSize += entry.Size
		result.TotalFiles++

		if result.TotalFiles%25 == 0 {
			report(filepath.Dir(entry.Path))
		}
	}

	for _, root := range c.paths {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		if _, err := os.Stat(root); err != nil {
			if !os.IsNotExist(err) {
				result.Errors = append(result.Errors, err)
			}
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
				// The configured root is a project, not junk -- even when its
				// own base name happens to match the junk list.
				if path == root {
					return nil
				}

				name := d.Name()
				if _, skip := projectArtifactsSkipDirs[name]; skip {
					return fs.SkipDir
				}
				if _, junk := projectArtifactsJunkDirs[name]; !junk {
					return nil
				}

				size, sizeErr := getPathSize(ctx, path)
				if sizeErr != nil {
					result.Errors = append(result.Errors, sizeErr)
					return fs.SkipDir
				}

				entry := FileEntry{
					Path:     path,
					Size:     size,
					IsDir:    true,
					Category: CategoryProjectArtifacts,
				}
				if info, infoErr := d.Info(); infoErr == nil {
					entry.ModTime = info.ModTime()
				}
				add(entry)

				// Recorded as a whole unit: never walk inside a match.
				return fs.SkipDir
			}

			// Symlinks, sockets and devices are not "oversized files"; only
			// regular files are ever reported.
			if !d.Type().IsRegular() {
				return nil
			}

			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			if info.Size() <= c.threshold {
				return nil
			}

			add(FileEntry{
				Path:     path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Category: CategoryProjectArtifacts,
			})
			return nil
		})
	}

	result.Duration = time.Since(start)
	report("")

	return result, nil
}

// Clean removes the given entries, with one asymmetry: directory entries are
// always deleted (a junk dir is regenerable by definition), but file entries
// -- the oversized files -- are skipped entirely unless the cleaner was
// constructed with deleteLargeFiles. A 500MB file may be a dataset, a VM
// image or an export, and unlike a build directory nothing regenerates it.
// Scan always reports both kinds; deleting the irreplaceable kind requires
// "clean --include-large-files".
func (c *ProjectArtifactsCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	result := &CleanResult{Category: CategoryProjectArtifacts, DryRun: dryRun}
	total := totalSize(entries)

	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		// Checked before the dryRun branch on purpose: a dry run must preview
		// exactly what an execute run would do, skipped files included.
		if !entry.IsDir && !c.deleteLargeFiles {
			continue
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
				Category:     CategoryProjectArtifacts,
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
