package cleaner

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/safety"
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

	// processChecker refuses deletion while the target app is running.
	// Defaults to the real checker; override with SetProcessChecker in tests.
	processChecker safety.ProcessChecker
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
		processChecker:  safety.NewProcessChecker(),
	}
}

// SetProcessChecker overrides the running-process guard. It exists so callers
// and tests outside this package can inject a fake; production code should rely
// on the default wired by NewAppUninstaller.
func (c *AppUninstaller) SetProcessChecker(checker safety.ProcessChecker) {
	c.processChecker = checker
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

// targetLabel is the human-readable name used in skip reasons and errors.
func (c *AppUninstaller) targetLabel() string {
	if c.target.Name == "" {
		return "the application"
	}
	return c.target.Name
}

func (c *AppUninstaller) DeletesWholeDomain() bool { return false }

func (c *AppUninstaller) setDefaults() {
	if c.bundleIDReader == nil {
		c.bundleIDReader = readAppBundleID
	}
	if c.pathSizeFetcher == nil {
		c.pathSizeFetcher = getPathSize
	}
	if c.processChecker == nil {
		c.processChecker = safety.NewProcessChecker()
	}
}

// TargetIsRunning reports whether the target application is running right now.
// It is a side channel outside the Cleaner interface: the CLI and the TUI call
// it before the confirmation screen so the user is warned early instead of
// discovering the skip only after confirming the cleanup.
func (c *AppUninstaller) TargetIsRunning(ctx context.Context) (bool, error) {
	c.setDefaults()
	return c.processChecker.IsRunning(ctx, safety.ProcessTarget{
		BundleID:   c.target.BundleID,
		BundlePath: c.target.BundlePath,
	})
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

	// Deleting the files of a running app corrupts its state, so the check
	// guards every removal below. A dry run touches nothing, so it does not pay
	// the cost of shelling out to ps; callers that want to warn the user before
	// confirming use TargetIsRunning instead.
	if !dryRun {
		running, err := c.TargetIsRunning(ctx)
		if err != nil {
			// Fail closed: a failed check is not the same as "not running".
			result.Errors = append(result.Errors, fmt.Errorf("could not verify whether %s is running: %w", c.targetLabel(), err))
			// Also surfaced as a skip so the "nothing was deleted" outcome stays
			// visible even to callers that only look at Skipped/SkipReason.
			result.Skipped = true
			result.SkipReason = fmt.Sprintf("could not verify whether %s is running; nothing was removed", c.targetLabel())
			result.Duration = time.Since(start)
			return result, nil
		}
		if running {
			result.Skipped = true
			result.SkipReason = fmt.Sprintf("%s is currently running; quit it before removing its files", c.targetLabel())
			result.Duration = time.Since(start)
			return result, nil
		}
	}

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
