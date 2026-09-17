package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// uninstallMinConfidenceLevels are the only values --min-confidence accepts,
// weakest last. The default ("safe") is the only band a Smart Uninstall run
// removes without a human looking first -- see cleaner.Confidence.IsSafe.
var uninstallMinConfidenceLevels = []string{"safe", "review", "caution"}

func validMinConfidence(value string) bool {
	for _, v := range uninstallMinConfidenceLevels {
		if value == v {
			return true
		}
	}
	return false
}

// passesMinConfidence decides whether one entry's scored Confidence clears the
// --min-confidence bar the user asked for.
//
// This is deliberately not a `switch c.Band { ...; default: allow }`: per
// docs/ARCHITECTURE.md's Confidence section, the zero value Confidence{} (an
// entry ExplainCandidate has no evidence for) has Band == "", which is none of
// the three ConfidenceBand constants, so a permissive default would wave every
// unexplained item straight through. Callers of this function must therefore
// only ever call it with a Confidence that came back with ok == true from
// ExplainCandidate -- see filterEntriesByConfidence, the only caller. Given
// that guarantee, Band is always one of the three real constants here (never
// the zero value), so the equality checks below are a closed, exhaustive
// whitelist rather than an open-ended default-allow.
func passesMinConfidence(conf cleaner.Confidence, minConfidence string) bool {
	switch minConfidence {
	case "review":
		return conf.IsSafe() || conf.Band == cleaner.ConfidenceReview
	case "caution":
		return conf.IsSafe() || conf.Band == cleaner.ConfidenceReview || conf.Band == cleaner.ConfidenceCaution
	default: // "safe", and any unvalidated value -- fail closed to the strictest tier
		return conf.IsSafe()
	}
}

// filterEntriesByConfidence keeps only the entries that both have a recorded
// Confidence (an entry with none is "unexplained", not "low confidence", and
// is dropped rather than guessed at -- see cleaner.Confidence's doc comment)
// and clear the requested --min-confidence bar.
//
// This must run on the freshly scanned entries *before* they are handed to
// commands.PrepareScanResultForClean / commands.RunCleanWithPreparedScanResult:
// those functions revalidate and tag but have no notion of confidence
// banding, so filtering after them would be too late to keep a Review/Caution
// leftover out of a "safe"-only run.
func filterEntriesByConfidence(explainer cleaner.CandidateExplainer, entries []cleaner.FileEntry, minConfidence string) []cleaner.FileEntry {
	filtered := make([]cleaner.FileEntry, 0, len(entries))
	for _, entry := range entries {
		conf, ok := explainer.ExplainCandidate(entry)
		if !ok {
			continue
		}
		if !passesMinConfidence(conf, minConfidence) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// uninstallRunningChecker is the subset of *cleaner.AppUninstaller that
// runUninstallNonInteractive (and uninstallScanAndFilter, which it calls)
// need: the confidence explainer used to filter the preview, the target's own
// identity (for the warning message below), and TargetIsRunning -- the
// running-process side channel documented on AppUninstaller as something the
// CLI is supposed to call up front, before the confirmation/--execute step,
// rather than leaving the user to discover a refusal only after asking for
// the real deletion. Defined as an interface here, rather than depending on
// *cleaner.AppUninstaller directly, so tests in this package can inject a
// fake without reaching into internal/cleaner.
type uninstallRunningChecker interface {
	cleaner.CandidateExplainer
	Target() cleaner.AppTarget
	TargetIsRunning(ctx context.Context) (bool, error)
}

// uninstallTargetLabel is the human-readable name used in the running-app
// warning below, mirroring AppUninstaller's own (unexported) targetLabel.
func uninstallTargetLabel(target cleaner.AppTarget) string {
	if target.Name == "" {
		return "the application"
	}
	return target.Name
}

// warnIfTargetRunning calls TargetIsRunning up front -- before the scan
// preview is even built -- and writes a warning to stderr if the target
// application is (or might be) running, so a dry-run reader is not surprised
// later that --execute silently refuses to remove anything (see
// CleanCategoryResult.Skipped and the exit-status check in
// runUninstallNonInteractive below).
//
// This is deliberately just an early, informational echo of the check
// AppUninstaller.Clean performs for real right before deleting: it does not
// replace that check, and does not gate whether this function runs the
// scan/clean pipeline at all. A failed check (err != nil, e.g. `ps` could not
// be consulted) is reported the same way, as a warning rather than a fatal
// error -- "I could not check" is not a reason to refuse a dry-run preview,
// and Clean will make its own (fail-closed) decision if --execute is used.
func warnIfTargetRunning(ctx context.Context, target uninstallRunningChecker) {
	label := uninstallTargetLabel(target.Target())
	running, err := target.TargetIsRunning(ctx)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "warning: could not determine whether %s is currently running: %v\n", label, err)
	case running:
		fmt.Fprintf(os.Stderr, "warning: %s is currently running; --execute will refuse to remove its files until it is quit\n", label)
	}
}

// uninstallScanAndFilter runs a fresh scan against the throwaway registry
// built for one AppTarget, filters its entries by --min-confidence, and
// returns the result reshaped as if it were a --from-file scan restricted to
// exactly the entries approved for this run. The reshaping is what lets the
// rest of the pipeline reuse commands.PrepareScanResultForClean /
// commands.RunCleanWithPreparedScanResult unchanged, and guarantees no second
// scan ever runs between this preview and the eventual deletion.
func uninstallScanAndFilter(ctx context.Context, registry *cleaner.Registry, explainer cleaner.CandidateExplainer, minConfidence string) (commands.ScanResult, error) {
	categories := []string{string(cleaner.CategoryAppUninstall)}

	scanResult, err := commands.RunScan(ctx, registry, categories, commands.ScanOptions{Detailed: true, Config: loadedConfig}, nil)
	if err != nil {
		return commands.ScanResult{}, err
	}

	filtered := commands.ScanResult{ScannedAt: scanResult.ScannedAt}
	for _, cat := range scanResult.Categories {
		item := cat
		if item.Err != nil {
			filtered.HasErrors = true
			filtered.Categories = append(filtered.Categories, item)
			continue
		}

		item.Files = filterEntriesByConfidence(explainer, cat.Files, minConfidence)
		item.TotalFiles = len(item.Files)
		item.TotalSize = 0
		for _, f := range item.Files {
			item.TotalSize += f.Size
		}
		item.TotalSizeHuman = utils.FormatBytes(item.TotalSize)

		filtered.TotalFiles += item.TotalFiles
		filtered.TotalSize += item.TotalSize
		filtered.Categories = append(filtered.Categories, item)
	}
	filtered.TotalSizeHuman = utils.FormatBytes(filtered.TotalSize)

	return filtered, nil
}

// runUninstallNonInteractive is the --output json entry point: scan, filter
// by confidence, prepare, and (when --execute is set) clean -- entirely
// non-interactive, mirroring runCleanNonInteractive's shape in cmd/clean.go.
func runUninstallNonInteractive(ctx context.Context, registry *cleaner.Registry, target uninstallRunningChecker, minConfidence string, detailed bool, output string) error {
	warnIfTargetRunning(ctx, target)

	filtered, err := uninstallScanAndFilter(ctx, registry, target, minConfidence)
	if err != nil {
		return err
	}

	categories := []string{string(cleaner.CategoryAppUninstall)}

	prepared, err := commands.PrepareScanResultForClean(ctx, registry, filtered, categories, loadedConfig)
	if err != nil {
		return err
	}

	dryRun := !executeFlag
	opts := commands.CleanerOptions{
		Detailed: detailed,
		DryRun:   dryRun,
		Config:   loadedConfig,
	}

	result, err := commands.RunCleanWithPreparedScanResult(ctx, registry, prepared, categories, opts, nil)

	revalidation := &commands.RevalidationSummary{
		RevalidatedFiles: prepared.RevalidatedFiles,
		MissingFiles:     prepared.MissingFiles,
		TypeChangedFiles: prepared.TypeChangedFiles,
		EmptyCategories:  prepared.EmptyCategories,
	}

	if writeErr := commands.WriteCleanOutput(os.Stdout, commands.CleanOutput{
		Result:       result,
		Revalidation: revalidation,
	}, output); writeErr != nil {
		return writeErr
	}

	if err != nil {
		return err
	}
	// A skip (the cleaner deliberately refusing the whole batch -- e.g. the
	// target app is running) is already visible in the JSON just written, via
	// CleanCategoryResult.Skipped/SkipReason. Without also failing here, an
	// `--execute` run that skipped everything would still exit 0 with
	// deleted_files: 0 and has_errors: false -- indistinguishable from a
	// genuine success. Only fail on it under --execute: a dry-run preview
	// never fails on its own (matching runCleanNonInteractive's convention),
	// and in practice a dry run never sets Skipped in the first place --
	// AppUninstaller.Clean's running-process check only runs when !dryRun.
	if !dryRun {
		if reason, skipped := uninstallFirstSkipReason(result); skipped {
			return fmt.Errorf("uninstall was skipped: %s", reason)
		}
	}
	if result.HasErrors {
		return fmt.Errorf("uninstall completed with errors")
	}
	return nil
}

// uninstallFirstSkipReason returns the SkipReason of the first category the
// cleaner reported as Skipped, if any.
func uninstallFirstSkipReason(result commands.CleanResult) (string, bool) {
	for _, cat := range result.Categories {
		if cat.Skipped {
			return cat.SkipReason, true
		}
	}
	return "", false
}

// uninstallAppListEntry is the --list --output json shape for one discovered
// application.
type uninstallAppListEntry struct {
	Name       string `json:"name"`
	BundleID   string `json:"bundle_id"`
	BundlePath string `json:"bundle_path"`
}

// writeUninstallAppListJSON writes every discovered app as a JSON array.
func writeUninstallAppListJSON(w io.Writer, apps []cleaner.AppTarget) error {
	entries := make([]uninstallAppListEntry, 0, len(apps))
	for _, app := range apps {
		entries = append(entries, uninstallAppListEntry{
			Name:       app.Name,
			BundleID:   app.BundleID,
			BundlePath: app.BundlePath,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

// writeUninstallAppListHuman writes every discovered app as a plain,
// human-readable list to w.
func writeUninstallAppListHuman(w io.Writer, apps []cleaner.AppTarget) error {
	if len(apps) == 0 {
		_, err := fmt.Fprintln(w, "no third-party applications found")
		return err
	}

	sorted := make([]cleaner.AppTarget, len(apps))
	copy(sorted, apps)
	sort.Slice(sorted, func(i, j int) bool { return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name) })

	for _, app := range sorted {
		if _, err := fmt.Fprintf(w, "%s (%s)\n  %s\n", app.Name, app.BundleID, app.BundlePath); err != nil {
			return err
		}
	}
	return nil
}

// resolveUninstallTarget matches the user's positional argument (an app name
// or a bundle identifier) against the applications DiscoverInstalledApps
// found, case-insensitively. Bundle ID is checked first because it is the
// unambiguous identifier; name is the convenience fallback and can, in
// principle, collide between two different vendors' apps -- an ambiguous
// match is reported rather than guessed at.
func resolveUninstallTarget(apps []cleaner.AppTarget, query string) (cleaner.AppTarget, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return cleaner.AppTarget{}, fmt.Errorf("no application specified")
	}

	for _, app := range apps {
		if strings.EqualFold(app.BundleID, query) {
			return app, nil
		}
	}

	var nameMatches []cleaner.AppTarget
	for _, app := range apps {
		if strings.EqualFold(app.Name, query) {
			nameMatches = append(nameMatches, app)
		}
	}

	switch len(nameMatches) {
	case 0:
		return cleaner.AppTarget{}, fmt.Errorf("no installed application matches %q; run 'tidymymac uninstall --list' to see discovered applications", query)
	case 1:
		return nameMatches[0], nil
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%q matches more than one installed application; specify the bundle id instead:\n", query)
		for _, app := range nameMatches {
			fmt.Fprintf(&b, "  %s (%s) at %s\n", app.Name, app.BundleID, app.BundlePath)
		}
		return cleaner.AppTarget{}, fmt.Errorf("%s", b.String())
	}
}
