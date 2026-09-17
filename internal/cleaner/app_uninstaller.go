package cleaner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
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

// AppUninstaller removes an application -- its .app bundle and the leftovers it
// scattered across the user's Library. Unlike every other Cleaner, it is not
// part of DefaultRegistry(): it is built on demand for one specific target and
// registered into a throwaway registry by the caller.
//
// Known limitation: the bundle is removed with the current process's own
// privileges. A bundle in /Applications owned by root cannot be removed that
// way; the failure is reported as an error on that one entry and the leftovers
// are still cleaned. See describeRemovalError and RequiresSudo.
type AppUninstaller struct {
	target          AppTarget
	homeDir         string
	bundleIDReader  func(context.Context, string) (string, error)
	pathSizeFetcher func(context.Context, string) (int64, error)

	// confidenceMu guards confidenceIndex. Scan publishes a whole new map while
	// a review screen may be calling ExplainCandidate concurrently, so the field
	// needs real synchronisation and not just an atomic-looking assignment.
	confidenceMu sync.RWMutex
	// confidenceIndex holds the per-entry evidence scoring produced by the
	// last Scan, keyed by FileEntry.Path, and is what ExplainCandidate reads.
	// It is only ever replaced wholesale, never mutated in place after
	// publication.
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
	return fmt.Sprintf("Application bundle and leftover files belonging to %s", c.target.Name)
}

// RequiresSudo is false on purpose: this cleaner never elevates. Everything
// under ~/Library belongs to the user, and so does a bundle installed in
// ~/Applications. A bundle in /Applications owned by root is the one case that
// cannot be removed, and it is reported as a per-entry error rather than
// escalating privileges for the whole run.
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

// Scan collects the removal candidates for the target application: its .app
// bundle plus every leftover found under ~/Library. It has no side effects:
// nothing is deleted, moved or written.
func (c *AppUninstaller) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	start := time.Now()
	result := &ScanResult{Category: CategoryAppUninstall}
	// A fresh index per Scan: stale scores from a previous run must never
	// outlive the entries they described. The index is built in a local map and
	// published in a single assignment at the end, so a concurrent reader
	// (ExplainCandidate from a review screen while a re-scan runs) can only ever
	// observe the complete previous index or the complete new one, never a
	// half-filled one.
	localIndex := map[string]Confidence{}
	defer func() { c.setConfidenceIndex(localIndex) }()

	c.setDefaults()

	var candidates []rawCandidate

	// The .app bundle itself, so an uninstall really uninstalls. It goes
	// through the very same scoring pipeline as the leftovers -- no special
	// case downstream.
	if bundle, ok := appBundleCandidate(ctx, c.target, c.pathSizeFetcher); ok {
		candidates = append(candidates, bundle)
	}

	if c.homeDir != "" && (c.target.BundleID != "" || c.target.Name != "") {
		leftovers, err := c.findLeftoverCandidates(ctx)
		if err != nil {
			return result, err
		}
		candidates = append(candidates, leftovers...)
	}

	for _, candidate := range candidates {
		// Discovery keeps the single strongest reason per candidate, so the
		// scored evidence is that one reason.
		localIndex[candidate.entry.Path] = scoreCandidate(
			[]MatchReason{candidate.reason},
			confidenceFlags{Shared: candidate.shared, ContainerData: candidate.isContainerData},
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
		// "I cannot check" is not "it is safe". Without a bundle path the
		// running-process guard has nothing to match executables against and
		// would answer false for an app that is very much running, so refuse
		// the whole batch instead of deleting on the strength of a non-answer.
		// A target resolved only by bundle id (e.g. `uninstall com.acme.editor`
		// where the .app was never located) lands here.
		if strings.TrimSpace(c.target.BundlePath) == "" {
			result.Skipped = true
			result.SkipReason = fmt.Sprintf(
				"could not confirm whether %s is running: its application bundle path is unknown; nothing was removed",
				c.targetLabel(),
			)
			result.Duration = time.Since(start)
			return result, nil
		}

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

		// Defence in depth. Shared data (a Group Container used by more than one
		// app) always scores as Caution, but that verdict is only advice: the
		// CLI and the review screen are expected to filter it out long before
		// Clean runs. This refusal makes the rule binding for any caller that
		// forgets -- deleting a shared container takes data away from an
		// application the user never asked to touch.
		//
		// Deliberate scope: only entries this uninstaller's own Scan produced
		// are second-guessed. An entry with no confidence record is not from
		// this scan (a caller-supplied path, or a call made before Scan), and
		// this cleaner has no evidence to judge it with, so it is left to the
		// caller's own vetting rather than blocked on a guess.
		if confidence, known := c.lookupConfidence(entry.Path); known && confidence.Shared {
			result.Errors = append(result.Errors, fmt.Errorf(
				"refusing to remove %s: it is shared with other applications", entry.Path,
			))
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
				result.Errors = append(result.Errors, c.describeRemovalError(entry, err))
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

// describeRemovalError wraps a failed removal with context. The one case worth
// spelling out is a permission failure on the .app bundle itself: an
// application under /Applications frequently belongs to root, and this cleaner
// deliberately does not elevate (see RequiresSudo). The user gets a message
// they can act on instead of a bare EACCES, and -- because this is an error and
// not a Skipped result -- the leftovers in ~/Library are still removed. Skipped
// means "nothing was touched at all", which is reserved for the running-app and
// unverifiable-target refusals; conflating the two would hide the fact that the
// rest of the batch did go through.
func (c *AppUninstaller) describeRemovalError(entry FileEntry, err error) error {
	if c.target.BundlePath != "" && entry.Path == c.target.BundlePath && os.IsPermission(err) {
		return fmt.Errorf(
			"removing the app bundle requires elevated permissions, not supported yet -- "+
				"remove %s manually or drag it to Trash: %w", entry.Path, err,
		)
	}
	return err
}
