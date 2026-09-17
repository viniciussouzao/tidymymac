package cleaner

import (
	"context"
	"fmt"
	"os"
	"time"
)

// AppTarget identifies a single installed application that the user asked to
// uninstall. It is resolved before the cleaner is built (see
// DiscoverInstalledApps) and is immutable for the lifetime of an AppUninstaller.
type AppTarget struct {
	BundlePath string // /Applications/Foo.app
	BundleID   string // com.vendor.foo
	Name       string // "Foo"
}

// AppUninstaller removes an application and the leftovers it scattered across
// the user's Library. Unlike every other Cleaner, it is not part of
// DefaultRegistry(): it is built on demand for one specific target and
// registered into a throwaway registry by the caller.
type AppUninstaller struct {
	target          AppTarget
	homeDir         string
	bundleIDReader  func(context.Context, string) (string, error)
	pathSizeFetcher func(context.Context, string) (int64, error)

	// confidenceIndex holds the per-entry evidence scoring produced by the
	// last Scan, keyed by FileEntry.Path, and is what ExplainCandidate reads.
	// It is only ever written by Scan.
	confidenceIndex map[string]Confidence

	// Reserved for a later phase of the Smart Uninstall feature:
	//   processChecker safety.ProcessChecker -- refuse to delete a running app.
	// Not implemented yet; discovery must not depend on it.
}

// NewAppUninstaller builds an uninstaller for a single resolved application.
func NewAppUninstaller(target AppTarget) *AppUninstaller {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	return &AppUninstaller{
		target:          target,
		homeDir:         home,
		bundleIDReader:  readAppBundleID,
		pathSizeFetcher: getPathSize,
	}
}

// Target returns the application this uninstaller was built for.
func (c *AppUninstaller) Target() AppTarget { return c.target }

func (c *AppUninstaller) Category() Category { return CategoryAppUninstall }

func (c *AppUninstaller) Name() string {
	if c.target.Name == "" {
		return "Uninstall App"
	}
	return fmt.Sprintf("Uninstall %s", c.target.Name)
}

func (c *AppUninstaller) Description() string {
	if c.target.Name == "" {
		return "Application bundle and its leftover support files"
	}
	return fmt.Sprintf("Leftover files belonging to %s", c.target.Name)
}

func (c *AppUninstaller) RequiresSudo() bool { return false }

func (c *AppUninstaller) DeletesWholeDomain() bool { return false }

func (c *AppUninstaller) setDefaults() {
	if c.bundleIDReader == nil {
		c.bundleIDReader = readAppBundleID
	}
	if c.pathSizeFetcher == nil {
		c.pathSizeFetcher = getPathSize
	}
}

// Scan collects the leftover candidates for the target application. It has no
// side effects: nothing is deleted, moved or written.
func (c *AppUninstaller) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryAppUninstall}
	// A fresh index per Scan: stale scores from a previous run must never
	// outlive the entries they described.
	c.confidenceIndex = map[string]Confidence{}

	if c.homeDir == "" || (c.target.BundleID == "" && c.target.Name == "") {
		result.Duration = time.Since(start)
		return result, nil
	}

	c.setDefaults()

	candidates, err := c.findLeftoverCandidates(ctx)
	if err != nil {
		return result, err
	}

	for _, candidate := range candidates {
		// Discovery keeps the single strongest reason per candidate, so the
		// scored evidence is that one reason.
		c.confidenceIndex[candidate.entry.Path] = scoreCandidate(
			[]MatchReason{candidate.reason}, candidate.shared,
		)

		result.Entries = append(result.Entries, candidate.entry)
		result.TotalSize += candidate.entry.Size
		result.TotalFiles++
	}

	result.Duration = time.Since(start)
	if progress != nil {
		progress(ScanProgress{
			Category:   CategoryAppUninstall,
			FilesFound: result.TotalFiles,
			BytesFound: result.TotalSize,
			CurrentDir: c.target.BundlePath,
		})
	}

	return result, nil
}

// Clean removes the selected entries. Errors on individual entries are
// accumulated and never abort the batch; a dry run touches nothing.
func (c *AppUninstaller) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	result := &CleanResult{Category: CategoryAppUninstall, DryRun: dryRun}
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
				Category:     CategoryAppUninstall,
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
