package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/viniciussouzao/tidymymac/internal/celebration"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/elevate"
	"github.com/viniciussouzao/tidymymac/internal/history"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Delete junk files by category",
	Long: `Delete junk files across all categories or a specific subset.
By default this command runs in dry-run mode and only simulates the cleanup.
Pass --execute to actually delete files.

Example usage:
# Preview what would be cleaned across all categories
$ tidymymac clean

# Actually delete files
$ tidymymac clean --execute

# Clean only specific categories
$ tidymymac clean docker app-caches --execute

# Use a previous detailed JSON scan and revalidate entries before cleaning
$ tidymymac clean --from-file scan.json

# Output the cleanup result as JSON
$ tidymymac clean --output json

# Use a previous scan file and output the cleanup result as JSON
$ tidymymac clean --from-file scan.json --output json

# Clean everything a profile bundles (categories + project paths)
$ tidymymac clean --profile dev --execute

# Also delete the oversized files a profile's project paths turned up
$ tidymymac clean --profile dev --include-large-files --execute
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if executeFlag {
			if err := guardRootDeletion(); err != nil {
				return err
			}
		}

		detailed, _ := cmd.Flags().GetBool("detailed")
		fromFile, _ := cmd.Flags().GetString("from-file")
		forceStaleScan, _ := cmd.Flags().GetBool("force-stale-scan")
		output, _ := cmd.Flags().GetString("output")
		quiet, _ := cmd.Flags().GetBool("quiet")
		profileName, _ := cmd.Flags().GetString("profile")
		includeLargeFiles, _ := cmd.Flags().GetBool("include-large-files")

		if output != "" && output != "json" {
			return fmt.Errorf("invalid --output value %q: must be json", output)
		}

		// Resolved once here so every path below works in terms of a plain
		// (categories, registry) pair, profile or not.
		categories, registry, err := resolveSelection(args, profileName, includeLargeFiles)
		if err != nil {
			return err
		}

		if output != "" {
			return runCleanNonInteractive(cmd.Context(), registry, categories, detailed, fromFile, forceStaleScan, output, quiet)
		}

		return runCleanInteractive(cmd, registry, categories, detailed, fromFile, forceStaleScan)
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(cleanCmd)
	cleanCmd.Flags().StringP("output", "o", "", "output format for results: json (omit for interactive table)")
	cleanCmd.Flags().String("profile", "", "clean the categories and project paths bundled by a configured profile")
	cleanCmd.Flags().Bool("include-large-files", false, "also delete the oversized files found in a profile's project paths (they are reported but never deleted without this)")
	cleanCmd.Flags().Bool("detailed", false, "include individual file paths in the cleanup result (only applies with --output json)")
	cleanCmd.Flags().String("from-file", "", "load a JSON scan file (from 'scan --output json --detailed') and revalidate its entries before cleaning")
	cleanCmd.Flags().Bool("force-stale-scan", false, "allow --from-file scan results older than 24 hours when used with --execute")
	cleanCmd.Flags().Bool("quiet", false, "suppress progress output to stderr")
}

const (
	cleanScanWarnAge = time.Hour
	cleanScanMaxAge  = 24 * time.Hour
)

func runCleanNonInteractive(ctx context.Context, registry *cleaner.Registry, categories []string, detailed bool, fromFile string, forceStaleScan bool, output string, quiet bool) error {
	start := time.Now()

	stderr := func(format string, a ...any) {
		if quiet {
			return
		}
		fmt.Fprintf(os.Stderr, format, a...)
	}

	var b strings.Builder

	dryRun := !executeFlag
	if dryRun {
		b.WriteString("🧪 dry-run mode: no files will be deleted. Use --execute to actually clean.\n")
	} else {
		b.WriteString("🧹 cleaning your mac...\n")
	}

	opts := commands.CleanerOptions{
		Detailed: detailed,
		DryRun:   dryRun,
		Config:   loadedConfig,
	}

	var (
		result       commands.CleanResult
		err          error
		revalidation *commands.RevalidationSummary
	)

	result, revalidation, err = executeClean(ctx, registry, categories, fromFile, forceStaleScan, opts, cleanProgressPrinter(stderr), stderr)
	if err != nil {
		return err
	}

	if !dryRun {
		_ = history.Append(buildRunRecord(result, time.Since(start).Milliseconds()))
	}

	if output != "" {
		if writeErr := commands.WriteCleanOutput(os.Stdout, commands.CleanOutput{
			Result:       result,
			Revalidation: revalidation,
		}, output); writeErr != nil {
			return writeErr
		}
		if result.HasErrors {
			return fmt.Errorf("clean completed with errors in: %s", strings.Join(failedCategoryNames(result), ", "))
		}
		return nil
	}

	actionSummary := "Would reclaim"
	actionVerb := "would clean"
	if !dryRun {
		actionSummary = "Reclaimed"
		actionVerb = "cleaned"
	}

	fmt.Fprintf(&b, "%s %s across %d files.\n", actionSummary, result.TotalSizeHuman, result.TotalFiles)
	for _, category := range result.Categories {
		if category.Err != nil {
			fmt.Fprintf(&b, "- %s: error: %s\n", category.Name, utils.SanitizeForTerminal(category.ErrMsg))
			continue
		}

		fmt.Fprintf(&b, "- %s: %s, %d files %s\n", category.Name, actionSize(category.DeletedSize), category.DeletedFiles, actionVerb)
		writePartialErrors(&b, category, "  ")
		if detailed {
			for _, file := range category.Files {
				fmt.Fprintf(&b, "  %s\n", utils.SanitizeForTerminal(file.Path))
			}
		}
	}

	_, _ = fmt.Fprint(os.Stdout, b.String())

	if result.HasErrors {
		return fmt.Errorf("clean completed with errors in: %s", strings.Join(failedCategoryNames(result), ", "))
	}

	return nil
}

// failedCategoryNames lists every category that failed outright or only
// partially -- both set HasErrors, and the exit-status message must name
// whichever it was.
func failedCategoryNames(result commands.CleanResult) []string {
	var failed []string
	for _, cat := range result.Categories {
		if cat.Err != nil || cat.PartialErrors > 0 {
			failed = append(failed, cat.Name)
		}
	}
	return failed
}

// writePartialErrors renders a category's non-fatal per-item failures as
// "path: reason" lines under it, so a partial failure is never mistaken for
// a clean success and the user knows exactly which items to look at.
func writePartialErrors(b *strings.Builder, category commands.CleanCategoryResult, indent string) {
	if category.PartialErrors == 0 {
		return
	}
	fmt.Fprintf(b, "%s%d item(s) could not be cleaned:\n", indent, category.PartialErrors)
	for _, ie := range category.PartialErrorDetails {
		path, reason := utils.SanitizeForTerminal(ie.Path), utils.SanitizeForTerminal(ie.Reason)
		if path != "" {
			fmt.Fprintf(b, "%s  %s: %s\n", indent, path, reason)
		} else {
			fmt.Fprintf(b, "%s  %s\n", indent, reason)
		}
	}
	if category.PartialErrorsTruncated {
		fmt.Fprintf(b, "%s  ... %d more not shown\n", indent, category.PartialErrors-len(category.PartialErrorDetails))
	}
}

func executeClean(
	ctx context.Context,
	registry *cleaner.Registry,
	args []string,
	fromFile string,
	forceStaleScan bool,
	opts commands.CleanerOptions,
	onEvent func(commands.CleanEvent),
	stderr func(string, ...any),
) (commands.CleanResult, *commands.RevalidationSummary, error) {
	if fromFile != "" {
		scanResult, loadErr := loadScanResultFile(fromFile)
		if loadErr != nil {
			return commands.CleanResult{}, nil, loadErr
		}

		age := time.Since(scanResult.ScannedAt)
		if !scanResult.ScannedAt.IsZero() && age > cleanScanWarnAge {
			stderr("warning: scan file is %s old; entries will be revalidated before cleaning\n", roundAge(age))
		}
		if !scanResult.ScannedAt.IsZero() && age > cleanScanMaxAge && !opts.DryRun && !forceStaleScan {
			return commands.CleanResult{}, nil, fmt.Errorf("scan file is %s old; rerun the scan or use --force-stale-scan with --execute", roundAge(age))
		}

		prepared, prepErr := commands.PrepareScanResultForClean(ctx, registry, scanResult, args, opts.Config)
		if prepErr != nil {
			return commands.CleanResult{}, nil, prepErr
		}

		revalidation := &commands.RevalidationSummary{
			RevalidatedFiles: prepared.RevalidatedFiles,
			MissingFiles:     prepared.MissingFiles,
			TypeChangedFiles: prepared.TypeChangedFiles,
			EmptyCategories:  prepared.EmptyCategories,
		}

		result, err := commands.RunCleanWithPreparedScanResult(
			ctx,
			registry,
			prepared,
			args,
			opts,
			onEvent,
		)
		return result, revalidation, err
	}

	result, err := commands.RunClean(ctx, registry, args, opts, onEvent)
	return result, nil, err
}

// runLiveClean is executeClean's counterpart for the interactive model's own
// scan+clean, given a scan that was already loaded and prepared exactly
// once by runCleanInteractive. Unlike executeClean it never touches
// --from-file itself: reading it a second time here would either consume an
// already-exhausted "--from-file -" stdin pipe, or simply redo the same
// file parse and revalidation for no reason.
func runLiveClean(
	ctx context.Context,
	registry *cleaner.Registry,
	args []string,
	usePreparedScan bool,
	prepared commands.PreparedScanResult,
	opts commands.CleanerOptions,
	onEvent func(commands.CleanEvent),
) (commands.CleanResult, *commands.RevalidationSummary, error) {
	if usePreparedScan {
		revalidation := &commands.RevalidationSummary{
			RevalidatedFiles: prepared.RevalidatedFiles,
			MissingFiles:     prepared.MissingFiles,
			TypeChangedFiles: prepared.TypeChangedFiles,
			EmptyCategories:  prepared.EmptyCategories,
		}
		result, err := commands.RunCleanWithPreparedScanResult(ctx, registry, prepared, args, opts, onEvent)
		return result, revalidation, err
	}

	result, err := commands.RunClean(ctx, registry, args, opts, onEvent)
	return result, nil, err
}

func cleanProgressPrinter(stderr func(string, ...any)) func(commands.CleanEvent) {
	return func(event commands.CleanEvent) {
		switch event.Type {
		case commands.CleanEventStarted:
			stderr("  · %s\n", event.Name)
		case commands.CleanEventDone:
			if event.Err != nil {
				stderr("  ✗ %s\n", event.Name)
				return
			}

			stderr("  ✓ %s (%s, %d files)\n", event.Name, actionSize(event.Result.DeletedSize), event.Result.DeletedFiles)
		}
	}
}

func loadScanResultFile(path string) (commands.ScanResult, error) {
	if path != "-" && strings.EqualFold(filepath.Ext(path), ".csv") {
		return commands.ScanResult{}, fmt.Errorf("--from-file only accepts JSON scan files; CSV output cannot be used for cleaning")
	}

	if path == "-" {
		result, err := commands.LoadScanResult(os.Stdin)
		if err != nil {
			return commands.ScanResult{}, explainScanLoadError(err)
		}
		return result, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return commands.ScanResult{}, fmt.Errorf("open scan file: %w", err)
	}
	return loadScanResultReader(f)
}

func loadScanResultReader(r io.ReadCloser) (result commands.ScanResult, err error) {
	defer func() {
		if closeErr := r.Close(); closeErr != nil && err == nil {
			result = commands.ScanResult{}
			err = fmt.Errorf("close scan file: %w", closeErr)
		}
	}()

	result, err = commands.LoadScanResult(r)
	if err != nil {
		return commands.ScanResult{}, explainScanLoadError(err)
	}
	return result, nil
}

func explainScanLoadError(err error) error {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return fmt.Errorf("--from-file only accepts JSON scan files generated by 'tidymymac scan --output json --detailed': %w", err)
	}
	return fmt.Errorf("invalid scan file: --from-file only accepts JSON scan files generated by 'tidymymac scan --output json --detailed': %w", err)
}

func roundAge(age time.Duration) time.Duration {
	if age < time.Minute {
		return age.Round(time.Second)
	}
	if age < time.Hour {
		return age.Round(time.Minute)
	}
	return age.Round(time.Hour)
}

// expandCategoriesFromPreparedScan returns categories unchanged when it is
// non-empty (an explicit selection always wins), and otherwise expands it to
// every category the prepared scan actually contains -- mirroring
// PrepareScanResultForClean's own empty-selection rule.
//
// This must never fall back to "every registered category" instead: that
// would let a whole-domain cleaner (brew cleanup, go clean -cache -modcache,
// empty Trash) run against a category the scan file never mentioned, with an
// empty entry list that a DeletesWholeDomain cleaner reads as "clear
// everything" rather than "nothing to do".
func expandCategoriesFromPreparedScan(categories []string, prepared commands.PreparedScanResult) []string {
	if len(categories) > 0 {
		return categories
	}
	expanded := make([]string, 0, len(prepared.Result.Categories))
	for _, cat := range prepared.Result.Categories {
		expanded = append(expanded, string(cat.Category))
	}
	return expanded
}

func runCleanInteractive(cmd *cobra.Command, registry *cleaner.Registry, categories []string, detailed bool, fromFile string, forceStaleScan bool) error {
	ctx := cmd.Context()
	dryRun := !executeFlag

	// Loaded and prepared at most once, up front, and reused by both the
	// elevated-sudo path below and the live (non-sudo) run inside the
	// bubbletea model: reading fromFile a second time would consume stdin
	// ("--from-file -") against an already-exhausted pipe, and would let the
	// two runs work from two different reads of the same file.
	var prepared commands.PreparedScanResult
	usePreparedScan := false
	effectiveCategories := categories

	if fromFile != "" {
		scanResult, err := loadScanResultFile(fromFile)
		if err != nil {
			return err
		}
		age := time.Since(scanResult.ScannedAt)
		if !scanResult.ScannedAt.IsZero() && age > cleanScanWarnAge {
			fmt.Fprintf(os.Stderr, "warning: scan file is %s old; entries will be revalidated before cleaning\n", roundAge(age))
		}
		if !dryRun && !scanResult.ScannedAt.IsZero() && age > cleanScanMaxAge && !forceStaleScan {
			return fmt.Errorf("scan file is %s old; rerun the scan or use --force-stale-scan with --execute", roundAge(age))
		}

		p, err := commands.PrepareScanResultForClean(ctx, registry, scanResult, categories, loadedConfig)
		if err != nil {
			return err
		}
		prepared = p
		usePreparedScan = true
		effectiveCategories = expandCategoriesFromPreparedScan(effectiveCategories, prepared)
	}

	// Sudo categories are handled separately, before any bubbletea Program
	// exists: dry-run needs no elevation (nothing gets deleted), so this
	// only ever runs for --execute.
	nonSudoCategories := effectiveCategories
	var preResolved []commands.CleanCategoryResult
	skipLiveRun := false

	if !dryRun {
		sudoNames, restNames, err := splitSudoCategories(registry, loadedConfig, effectiveCategories)
		if err != nil {
			return err
		}
		if len(sudoNames) > 0 {
			elevateStart := time.Now()
			results, err := elevateForClean(ctx, registry, sudoNames, prepared.Result, usePreparedScan)
			if err != nil {
				return err
			}
			preResolved = results
			nonSudoCategories = restNames
			// Every selected category needed sudo: an empty selected list
			// means "every category" elsewhere in this function, so
			// nonSudoCategories being empty here must skip the live run
			// entirely rather than pass that empty slice through and
			// accidentally clean something the user never selected.
			skipLiveRun = len(restNames) == 0

			// Recorded immediately, before the live (non-sudo) run's own
			// bubbletea Program even starts: elevate.Invoke already ran to
			// completion by this point, so this deletion is real and final.
			// It must not depend on the separate live run reaching its own
			// history.Append later -- quitting (q/ctrl+c) mid-live-run
			// abandons that goroutine entirely, which would otherwise take
			// this already-completed elevated deletion's audit trail with it.
			_ = history.Append(buildRunRecord(
				commands.CleanResult{CleanedAt: time.Now().UTC(), Categories: preResolved},
				time.Since(elevateStart).Milliseconds(),
			))
		}
	}

	m := newCleanModel(ctx, registry, nonSudoCategories, detailed, usePreparedScan, prepared, dryRun, preResolved, skipLiveRun)
	p := tea.NewProgram(m)

	final, err := p.Run()
	if err != nil {
		return err
	}

	finalModel, ok := final.(cleanModel)
	if !ok {
		return nil
	}

	if finalModel.result != nil && finalModel.result.HasErrors {
		var failed []string
		for _, cat := range finalModel.result.Categories {
			if cat.Err != nil {
				failed = append(failed, cat.Name)
			}
		}
		return fmt.Errorf("clean completed with errors in: %s", strings.Join(failed, ", "))
	}

	if finalModel.err == nil && finalModel.result != nil && finalModel.dryRun {
		fmt.Println(styles.Help.Render("  Run 'tidymymac clean --execute' to actually delete these files"))
	}

	return finalModel.err
}

// splitSudoCategories resolves the categories clean would actually act on --
// mirroring resolveCleaners' "an empty selected list means every enabled
// category" rule, since selected may be empty here the same way it can reach
// commands.RunClean -- and partitions them into those requiring sudo and
// everything else. Duplicate names are collapsed: the elevated helper
// rejects a plan that lists the same category twice, and letting a
// duplicate through would only be discovered after the password prompt.
func splitSudoCategories(registry *cleaner.Registry, cfg *config.Config, selected []string) (sudoNames, restNames []string, err error) {
	names := dedupeStrings(selected)
	if len(names) == 0 {
		for _, c := range config.FilterRegistry(registry, cfg).All() {
			names = append(names, string(c.Category()))
		}
	}

	for _, name := range names {
		c, ok := registry.Get(cleaner.Category(name))
		if !ok {
			return nil, nil, fmt.Errorf("unknown category %q", name)
		}
		if !c.RequiresSudo() {
			restNames = append(restNames, name)
			continue
		}
		// The elevated helper rejects a plan containing any disabled
		// category outright (see validatePlan in internal/elevate/helper.go)
		// -- catch that here too, before any password prompt, rather than
		// asking for credentials for work that is guaranteed to be refused.
		// Categories reached via the empty-selection expansion above are
		// never disabled to begin with (FilterRegistry already excludes
		// them), so this only ever fires for an explicit selection.
		//
		// Dropped rather than failing the whole command: "an explicit
		// selection always wins over disabled_categories" is this package's
		// rule for every other category (see resolveCleaners), and sudo
		// elevation simply cannot honor that for this one category -- the
		// rest of an explicit selection should still run.
		if cfg.IsCategoryDisabled(name) {
			fmt.Fprintf(os.Stderr, "warning: %s is disabled via config; skipping it rather than requesting a sudo password the elevated helper would refuse anyway\n", name)
			continue
		}
		sudoNames = append(sudoNames, name)
	}
	return sudoNames, restNames, nil
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// sudoNeedMessage explains, before the password prompt appears, which
// selected categories are the reason for it -- an unexplained sudo prompt in
// the middle of an ordinary command is exactly the shape of a
// credential-phishing prompt.
func sudoNeedMessage(categories []elevate.PlanCategory) string {
	names := make([]string, len(categories))
	for i, pc := range categories {
		names[i] = pc.Category.DisplayName()
	}
	if len(names) == 1 {
		return fmt.Sprintf("%s requires sudo to clean.", names[0])
	}
	return fmt.Sprintf("The following selected categories require sudo to clean: %s.", strings.Join(names, ", "))
}

// elevateForClean scans, tags, and strips protected paths for sudoNames --
// the same pipeline runClean applies to every other category -- then hands
// whatever remains to a single elevate.Invoke call covering all of them, so
// one sudo password authenticates every sudo category in this run. It is
// only ever called for --execute (dry-run needs no elevation) with a
// non-empty sudoNames. preparedScan/usePreparedScan is whatever
// runCleanInteractive already loaded from --from-file (or the zero value
// when no --from-file was given) -- this function never reads fromFile
// itself, so it never re-reads a file (or re-consumes a "--from-file -"
// stdin pipe) the live, non-sudo run is also about to read.
//
// Called before any bubbletea Program exists, so elevate.Invoke's sudo
// prompt runs on a perfectly ordinary terminal: no tea.Exec "release the
// terminal" dance is needed here, unlike the full TUI's execute flow, which
// is already deep in an alt-screen session by the time it reaches this same
// decision.
func elevateForClean(ctx context.Context, registry *cleaner.Registry, sudoNames []string, preparedScan commands.ScanResult, usePreparedScan bool) ([]commands.CleanCategoryResult, error) {
	approved, err := commands.ResolveApprovedEntries(ctx, registry, sudoNames, loadedConfig, preparedScan, usePreparedScan)
	if err != nil {
		return nil, err
	}

	var results []commands.CleanCategoryResult
	// DryRun is left false explicitly, not merely by relying on the zero
	// value: this function is only ever reached for --execute (see the
	// !dryRun guard around its one call site), but a plan defaulting to
	// "delete for real" should never depend on that being remembered
	// correctly by a caller two frames away.
	plan := elevate.Plan{DryRun: false}
	for _, ac := range approved {
		switch {
		case ac.Err != nil:
			results = append(results, commands.CleanCategoryResult{Category: ac.Category, Name: ac.Name, ErrMsg: ac.Err.Error(), Err: ac.Err})
		case len(ac.Entries) == 0:
			// Nothing to delete: either the category is genuinely empty, or
			// every entry it found is a protected path. Either way there is
			// nothing to authenticate for, so no password prompt.
			results = append(results, commands.CleanCategoryResult{Category: ac.Category, Name: ac.Name})
		default:
			plan.Categories = append(plan.Categories, elevate.PlanCategory{Category: ac.Category, Entries: ac.Entries})
		}
	}

	if len(plan.Categories) == 0 {
		return results, nil
	}

	fmt.Fprintln(os.Stderr, sudoNeedMessage(plan.Categories))
	result, invokeErr := elevate.Invoke(ctx, plan)
	results = append(results, elevate.CategoryResults(plan, result, invokeErr)...)
	return results, nil
}

// mergeCleanResults folds elevate-derived category results into an ordinary
// CleanResult, recomputing totals so the merged result renders and records
// to history exactly as if a single clean run had produced it.
func mergeCleanResults(base commands.CleanResult, extra []commands.CleanCategoryResult) commands.CleanResult {
	if len(extra) == 0 {
		return base
	}

	merged := base
	merged.Categories = append(append([]commands.CleanCategoryResult{}, extra...), base.Categories...)
	for _, r := range extra {
		// Mirrors runClean's own totals computation (internal/commands/clean.go):
		// an errored category's counts, even if partially non-zero, are never
		// folded into the displayed total -- only HasErrors is set for it. A
		// partial failure also sets HasErrors, but what it reclaimed is real
		// and stays in the totals.
		if r.PartialErrors > 0 {
			merged.HasErrors = true
		}
		if r.Err != nil {
			merged.HasErrors = true
			continue
		}
		merged.TotalFiles += r.DeletedFiles
		merged.TotalSize += r.DeletedSize
	}
	merged.TotalSizeHuman = utils.FormatBytes(merged.TotalSize)
	return merged
}

type cleanDoneMsg struct {
	result       commands.CleanResult
	revalidation *commands.RevalidationSummary
	err          error
}

type cleanEventMsg struct {
	event  commands.CleanEvent
	closed bool
}

type cleanCategoryProgress struct {
	name string
	done bool
	err  bool
}

type cleanModel struct {
	ctx             context.Context
	registry        *cleaner.Registry
	args            []string
	detailed        bool
	usePreparedScan bool
	prepared        commands.PreparedScanResult
	dryRun          bool
	spinner         spinner.Model
	result          *commands.CleanResult
	revalidation    *commands.RevalidationSummary
	err             error
	cleaning        bool
	categories      []cleanCategoryProgress
	eventCh         chan commands.CleanEvent
	celebration     string

	// preResolved carries category results elevateForClean already produced
	// -- including their history record already written -- before this
	// model ever started. It is merged into the live run's result purely
	// for display; it must never be written to history again here.
	preResolved []commands.CleanCategoryResult
	// skipLiveRun is true when every selected category went to
	// elevateForClean, leaving nothing for this model's own scan+clean to
	// do. args is empty in that case, and args being empty ordinarily means
	// "every category" to the rest of this package -- this flag is what
	// keeps that empty slice from being misread as "unspecified" here.
	skipLiveRun bool
}

func newCleanModel(ctx context.Context, registry *cleaner.Registry, args []string, detailed bool, usePreparedScan bool, prepared commands.PreparedScanResult, dryRun bool, preResolved []commands.CleanCategoryResult, skipLiveRun bool) cleanModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styles.Cursor

	return cleanModel{
		ctx:             ctx,
		registry:        registry,
		args:            args,
		detailed:        detailed,
		usePreparedScan: usePreparedScan,
		prepared:        prepared,
		dryRun:          dryRun,
		spinner:         s,
		cleaning:        true,
		eventCh:         make(chan commands.CleanEvent, 50),
		preResolved:     preResolved,
		skipLiveRun:     skipLiveRun,
	}
}

func (m cleanModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			start := time.Now()

			var result commands.CleanResult
			var revalidation *commands.RevalidationSummary
			var err error

			if m.skipLiveRun {
				result = commands.CleanResult{CleanedAt: time.Now().UTC()}
				if m.usePreparedScan {
					revalidation = &commands.RevalidationSummary{
						RevalidatedFiles: m.prepared.RevalidatedFiles,
						MissingFiles:     m.prepared.MissingFiles,
						TypeChangedFiles: m.prepared.TypeChangedFiles,
						EmptyCategories:  m.prepared.EmptyCategories,
					}
				}
			} else {
				result, revalidation, err = runLiveClean(
					m.ctx,
					m.registry,
					m.args,
					m.usePreparedScan,
					m.prepared,
					commands.CleanerOptions{
						Detailed: m.detailed,
						DryRun:   m.dryRun,
						Config:   loadedConfig,
					},
					func(event commands.CleanEvent) {
						m.eventCh <- event
					},
				)
			}
			close(m.eventCh)

			// preResolved's own history record was already written
			// synchronously in runCleanInteractive, right after
			// elevate.Invoke returned -- recording it again here would
			// double it, and worse, quitting mid-live-run would mean the
			// live portion's record (below) never gets appended at all,
			// while preResolved's already happened regardless.
			if !m.dryRun && !m.skipLiveRun {
				_ = history.Append(buildRunRecord(result, time.Since(start).Milliseconds()))
			}

			result = mergeCleanResults(result, m.preResolved)

			return cleanDoneMsg{
				result:       result,
				revalidation: revalidation,
				err:          err,
			}
		},
		m.listenEvents(),
	)
}

func (m cleanModel) listenEvents() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventCh
		if !ok {
			return cleanEventMsg{closed: true}
		}
		return cleanEventMsg{event: event}
	}
}

func (m cleanModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case cleanEventMsg:
		if msg.closed {
			return m, nil
		}
		switch msg.event.Type {
		case commands.CleanEventStarted:
			m.categories = append(m.categories, cleanCategoryProgress{name: msg.event.Name})
		case commands.CleanEventDone:
			for i, cat := range m.categories {
				if cat.name == msg.event.Name {
					m.categories[i].done = true
					m.categories[i].err = msg.event.Err != nil
					break
				}
			}
		}
		return m, m.listenEvents()
	case cleanDoneMsg:
		m.cleaning = false
		m.err = msg.err
		m.revalidation = msg.revalidation
		// result is set even when err != nil: err only ever reflects a
		// structural failure of the separate, non-sudo live run (a bad
		// --from-file, an unresolvable category), and msg.result already
		// carries any elevate-derived categories merged in. Those deletions
		// already happened for real and must never be hidden behind an
		// unrelated live-run failure -- see View()'s error handling below.
		m.result = &msg.result
		if m.err == nil && !m.dryRun {
			m.celebration = cleanCelebration(msg.result)
		}
		return m, tea.Quit
	}

	return m, nil
}

func (m cleanModel) View() string {
	var b strings.Builder

	title := "🧹 cleaning your mac..."
	statusText := "removing files you selected for cleanup..."
	modeBanner := styles.Size.Render("  EXECUTE MODE - Files are being deleted.")
	helpText := " q to quit"
	if m.dryRun {
		title = "🧪 dry-run cleanup..."
		statusText = "simulating cleanup without deleting files..."
		modeBanner = styles.Help.Render("  DRY RUN MODE - No files will be deleted. Run with --execute to actually clean.")
		helpText = " q to quit | rerun with --execute to actually delete files"
	}

	b.WriteString(scanTitleStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(modeBanner)
	b.WriteString("\n")

	if m.cleaning {
		fmt.Fprintf(&b, " %s %s", m.spinner.View(), styles.Dim.Render(statusText))
		b.WriteString("\n")
		if m.usePreparedScan {
			b.WriteString("\n")
			b.WriteString(styles.Dim.Render("  using entries revalidated from the provided scan file"))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		for _, cat := range m.categories {
			switch {
			case cat.err:
				fmt.Fprintf(&b, "  %s %s\n", styles.Error.Render("✗"), styles.Dim.Render(cat.name))
			case cat.done:
				fmt.Fprintf(&b, "  %s %s\n", styles.Success.Render("✓"), styles.Dim.Render(cat.name))
			default:
				fmt.Fprintf(&b, "  %s %s\n", styles.Dim.Render("·"), styles.Dim.Render(cat.name))
			}
		}
		b.WriteString("\n")
		b.WriteString(styles.Help.Render(helpText))
		return b.String()
	}

	// m.err is a structural failure of the separate, non-sudo live run; it
	// never invalidates m.result, which may still carry real elevate-derived
	// deletions merged in (see the cleanDoneMsg handler above). Only bail
	// out to a plain error line when there is truly nothing to show.
	if m.err != nil && (m.result == nil || len(m.result.Categories) == 0) {
		return styles.Error.Render(fmt.Sprintf("  ✗ error cleaning: %v", m.err))
	}

	if m.revalidation != nil {
		b.WriteString("\n")
		b.WriteString(styles.Dim.Render(fmt.Sprintf(
			"  Revalidated %d files (%d missing, %d type-changed, %d empty categories)",
			m.revalidation.RevalidatedFiles,
			m.revalidation.MissingFiles,
			m.revalidation.TypeChangedFiles,
			m.revalidation.EmptyCategories,
		)))
		b.WriteString("\n")
	}

	// compute colCategory dynamically from the widest visible name
	colCategory := lipgloss.Width("Category")
	for _, cat := range m.result.Categories {
		if w := lipgloss.Width(cat.Name); w > colCategory {
			colCategory = w
		}
	}
	colCategory += 2
	tableWidth := colCategory + colFiles + colSize + 6

	boldStyle := lipgloss.NewStyle().Bold(true)
	sep := styles.Dim.Render("  " + strings.Repeat("─", tableWidth))

	sizeLabel := "Reclaimed"
	if m.dryRun {
		sizeLabel = "Would Free"
	}

	fmt.Fprintf(&b, "\n  %s  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("%-*s", colCategory, "Category")),
		boldStyle.Render(fmt.Sprintf("%*s", colFiles, "Files")),
		boldStyle.Render(fmt.Sprintf("%*s", colSize, sizeLabel)))
	b.WriteString(sep)
	b.WriteString("\n")

	for _, cat := range m.result.Categories {
		var filesText, sizeText string

		if cat.Err != nil {
			filesText = styles.Error.Render(fmt.Sprintf("%*s", colFiles, "─"))
			sizeText = styles.Error.Render(fmt.Sprintf("%*s", colSize, "error"))
		} else {
			filesText = styles.Dim.Render(fmt.Sprintf("%*d", colFiles, cat.DeletedFiles))
			sizeText = styles.SizeStyled(cat.DeletedSize, fmt.Sprintf("%*s", colSize, utils.FormatBytes(cat.DeletedSize)))
		}

		fmt.Fprintf(&b, "  %-*s  %s  %s\n",
			colCategory, cat.Name,
			filesText,
			sizeText)
	}

	b.WriteString(sep)
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %s  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("%-*s", colCategory, "Total")),
		styles.Dim.Render(fmt.Sprintf("%*d", colFiles, m.result.TotalFiles)),
		styles.SizeStyled(m.result.TotalSize, fmt.Sprintf("%*s", colSize, utils.FormatBytes(m.result.TotalSize))))
	b.WriteString("\n")
	if m.err != nil {
		b.WriteString(styles.Error.Render(fmt.Sprintf("  Note: the non-sudo portion of this run failed: %v", m.err)))
		b.WriteString("\n")
	}
	if m.celebration != "" {
		b.WriteString(styles.Success.Render("  " + m.celebration))
		b.WriteString("\n")
	}
	if m.dryRun {
		b.WriteString(styles.Help.Render("  Preview only. Run 'tidymymac clean --execute' to actually delete these files."))
	} else {
		b.WriteString(styles.Help.Render("  Cleanup finished. You can run 'tidymymac history' to inspect previous cleanup sessions."))
	}

	return b.String()
}

func actionSize(size int64) string {
	return utils.FormatBytes(size)
}

func cleanCelebration(result commands.CleanResult) string {
	converted := make([]celebration.Result, 0, len(result.Categories))
	for _, category := range result.Categories {
		converted = append(converted, celebration.Result{
			Category:   category.Category,
			BytesFreed: category.DeletedSize,
			// Mirror the TUI summary: a category with any error, fatal or
			// partial, is not celebrated even if it reclaimed some space.
			Failed: category.Err != nil || category.PartialErrors > 0,
		})
	}
	return celebration.Message(converted)
}

func buildRunRecord(result commands.CleanResult, durationMs int64) history.RunRecord {
	var categories []history.CategoryRecord
	for _, cat := range result.Categories {
		if cat.Err != nil || (cat.DeletedFiles == 0 && cat.DeletedSize == 0) {
			continue // skip categories that failed or cleaned nothing
		}
		categories = append(categories, history.CategoryRecord{
			Name:        cat.Name,
			DisplayName: cat.Category.DisplayName(),
			Files:       cat.DeletedFiles,
			Bytes:       cat.DeletedSize,
		})
	}
	return history.NewRunRecord(result.CleanedAt, durationMs, categories)
}
