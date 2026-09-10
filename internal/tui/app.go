package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/internal/elevate"
	"github.com/viniciussouzao/tidymymac/internal/history"
	"github.com/viniciussouzao/tidymymac/internal/tui/screens"
	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/sysinfo"
)

type screen int

const (
	screenDashboard screen = iota
	screenScanning
	screenReview
	screenCleaning
	screenSummary
)

type scanCompleteMsg struct {
	category cleaner.Category
	result   *cleaner.ScanResult
	err      error
}

type cleanCompleteMsg struct {
	category cleaner.Category
	result   *cleaner.CleanResult
	err      error
}

type cleanProgressMsg struct {
	progress cleaner.CleanProgress
}

type healthInfoMsg struct {
	info sysinfo.Info
}

// elevateCompleteMsg carries the outcome of a single elevate.Invoke call
// covering every sudo category the user chose to authenticate for. plan is
// carried alongside the result (rather than re-derived from app state) so
// the handler knows exactly which categories this particular elevation
// attempt was responsible for, even if app state has moved on by the time
// the message is processed.
type elevateCompleteMsg struct {
	plan   elevate.Plan
	result elevate.Result
	err    error

	// direct holds the outcome of any entries startElevation's direct-clean
	// legs cleaned unprivileged, before plan was dispatched -- keyed by
	// category so handleElevateComplete can fold each one into that
	// category's elevated outcome. See commands.PrivilegeSplitter and
	// pendingElevationState.
	direct map[cleaner.Category]commands.CleanCategoryResult
}

// directCleanCompleteMsg carries the outcome of one category's direct leg
// (the unprivileged subset of a sudo category's approved entries, split off
// by commands.SplitEntriesByPrivilege in startElevation and dispatched via
// directCleanCmd). See handleDirectCleanComplete.
type directCleanCompleteMsg struct {
	category cleaner.Category
	result   commands.CleanCategoryResult
}

// pendingElevationState accumulates an in-flight elevation attempt whose
// direct legs (see commands.SplitEntriesByPrivilege) are still cleaning
// asynchronously. startElevation populates it and returns immediately, one
// tea.Cmd per pending direct leg; handleDirectCleanComplete folds each
// result in as it arrives and, once pendingDirect is empty, dispatches
// whatever plan accumulated (or falls through to the ordinary clean loop),
// exactly as startElevation would have done synchronously before direct legs
// existed. nil on App whenever no elevation is in flight.
type pendingElevationState struct {
	plan          elevate.Plan
	direct        map[cleaner.Category]commands.CleanCategoryResult
	pendingDirect map[cleaner.Category]struct{}
}

// App is the root bubbletea model that manages screens transitions
type App struct {
	currentScreen screen
	executeMode   bool
	width         int
	height        int

	// Screens
	dashboard   screens.DashboardModel
	scanningScr screens.ScanningModel
	cleaningScr screens.CleaningModel
	summaryScr  screens.SummaryModel
	reviewScr   screens.ReviewModel
	reviewBuilt bool // true once review is built for the current scan; prevents state reset on esc+enter

	// reviewScanResults is the exact scan snapshot the review screen was
	// built from. Cleaning (and, through it, the elevate.Plan sent to root)
	// must be built from this same snapshot rather than a fresh call to
	// scanningScr.Results() -- a category re-scanned in the background while
	// the user sat on the review screen must never change what gets deleted
	// without the user reviewing it again.
	reviewScanResults map[cleaner.Category]*cleaner.ScanResult

	registry       *cleaner.Registry
	scanResults    map[cleaner.Category]*cleaner.ScanResult
	spinner        spinner.Model
	isElevated     bool
	scanning       bool
	ctx            context.Context
	cancel         context.CancelFunc
	cleanMsgCh     <-chan tea.Msg
	cleanStartTime time.Time
	cfg            *config.Config

	// elevatedRecorded holds every category whose deletion has already been
	// written to history outside finishCleaning's own end-of-run write, so
	// finishCleaning does not record it a second time. Two things populate
	// it: recordElevatedHistory, for a category whose elevated leg
	// (elevate.Invoke) completed, and recordDirectHistory, for a category
	// whose direct leg (commands.SplitEntriesByPrivilege) completed --
	// including a direct-only category that never enters an elevate.Plan at
	// all and so would otherwise never be marked by anything else. See
	// either function's own comment for why each write happens immediately
	// rather than waiting for the whole run to finish.
	elevatedRecorded map[cleaner.Category]struct{}

	// pendingElevation is non-nil while an elevation attempt's direct legs
	// are still cleaning asynchronously. See pendingElevationState.
	pendingElevation *pendingElevationState

	// revalidating is true while a revalidateCmd dispatched from updateReview
	// is in flight, so a second enter press can't dispatch another one
	// racing it. revalidateSeq is stamped into each dispatch's
	// revalidateCompleteMsg; handleRevalidateComplete discards any message
	// whose seq doesn't match the current one (superseded by a later
	// dispatch) or that arrives after currentScreen has left screenReview
	// (the user backed out with esc while it was still running). See
	// updateReview's Confirm and Back cases.
	revalidating  bool
	revalidateSeq int

	// scriptMessage will support the generate-script-only flow.
}

// NewApp initializes the TUI application with default values and a spinner.
// Categories disabled via cfg.DisabledCategories are excluded from the
// registry entirely -- the TUI has no equivalent of an explicit CLI category
// override, so disabled categories simply never appear.
//
// parent must be cmd.Context(), not context.Background(): it is the root
// command's signal-aware context (see cmd/root.go's Execute), and this app's
// own ctx is derived from it rather than rooted independently so that a
// process-level SIGINT/SIGTERM reaches every in-flight scan, clean, and
// elevate.Invoke call the same way a.cancel() from the in-TUI quit key does.
// This matters specifically once the terminal is briefly handed to sudo's
// native password prompt (see startElevation/elevateCmd): that is the one
// moment a real, kernel-delivered SIGINT can reach this process at all
// (bubbletea's raw mode otherwise turns Ctrl-C into an ordinary keypress),
// and without this wiring that signal would abort only the elevation and
// silently leave the rest of the run to continue deleting.
func NewApp(parent context.Context, execute bool, cfg *config.Config) App {
	s := spinner.New()
	s.Spinner = spinner.Dot

	ctx, cancel := context.WithCancel(parent)

	return App{
		currentScreen: screenDashboard,
		executeMode:   execute,
		dashboard:     screens.NewDashboard(),
		registry:      config.FilterRegistry(cleaner.DefaultRegistry(), cfg),
		scanResults:   make(map[cleaner.Category]*cleaner.ScanResult),
		spinner:       s,
		isElevated:    os.Geteuid() == 0,
		ctx:           ctx,
		cancel:        cancel,
		cfg:           cfg,
	}
}

// Init is the initial command that runs when the TUI starts. It can be used to kick off any setup tasks or initial scans.
func (a App) Init() tea.Cmd {
	cmds := []tea.Cmd{
		a.spinner.Tick,
		gatherHealthInfoCmd(a.ctx),
	}

	for _, c := range a.registry.All() {
		a.dashboard.SetCategoryScanning(string(c.Category()))
		cmds = append(cmds, scanCategoryCmd(a.ctx, c))
	}

	a.scanning = true

	return tea.Batch(cmds...)
}

func scanCategoryCmd(ctx context.Context, c cleaner.Cleaner) tea.Cmd {
	return func() tea.Msg {
		result, err := c.Scan(ctx, nil)
		return scanCompleteMsg{
			category: c.Category(),
			result:   result,
			err:      err,
		}
	}
}

func gatherHealthInfoCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		return healthInfoMsg{info: sysinfo.Gather(ctx)}
	}
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.dashboard.SetSize(msg.Width, msg.Height)
		a.scanningScr.SetSize(msg.Width, msg.Height)
		a.reviewScr.SetSize(msg.Width, msg.Height)
		a.cleaningScr.SetSize(msg.Width, msg.Height)
		a.summaryScr.SetSize(msg.Width, msg.Height)
		return a, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		var scanCmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		a.scanningScr.Spinner, scanCmd = a.scanningScr.Spinner.Update(msg) // update scanning screen spinner as well
		a.cleaningScr.SetActivityFrame(a.spinner.View())
		return a, tea.Batch(cmd, scanCmd)

	case scanCompleteMsg:
		return a.handleScanComplete(msg)

	case healthInfoMsg:
		a.dashboard.SetHealthInfo(msg.info)
		return a, nil

	case cleanCompleteMsg:
		return a.handleCleanComplete(msg)

	case elevateCompleteMsg:
		return a.handleElevateComplete(msg)

	case directCleanCompleteMsg:
		return a.handleDirectCleanComplete(msg)

	case revalidateCompleteMsg:
		return a.handleRevalidateComplete(msg)

	case cleanProgressMsg:
		a.cleaningScr.UpdateCleanProgress(msg.progress)
		if a.cleanMsgCh != nil {
			return a, waitForCleanMsgCmd(a.cleanMsgCh)
		}
		return a, nil

	case tea.KeyMsg:
		if key.Matches(msg, keys.Quit) {
			a.cancel() // cancel any ongoing scans or cleans
			return a, tea.Quit
		}

		switch a.currentScreen {
		case screenDashboard:
			return a.updateDashboard(msg)
		case screenScanning:
			return a.updateScanning(msg)
		case screenReview:
			return a.updateReview(msg)
		case screenCleaning:
			return a.updateCleaning(msg)
		case screenSummary:
			return a.updateSummary(msg)

		}
	}

	return a, nil
}

func (a App) handleScanComplete(msg scanCompleteMsg) (tea.Model, tea.Cmd) {
	if msg.result != nil {
		msg.result.Entries = a.cfg.Tag(msg.result.Entries)
		a.scanResults[msg.category] = msg.result
	}
	a.dashboard.UpdateCategoryResult(string(msg.category), msg.result)

	if a.currentScreen == screenScanning {
		a.scanningScr.UpdateScanResult(msg.category, msg.result, msg.err)
	}

	a.scanning = false
	for _, cat := range a.dashboard.Categories {
		if cat.Scanning {
			a.scanning = true
			break
		}
	}

	return a, nil
}

func (a App) handleCleanComplete(msg cleanCompleteMsg) (tea.Model, tea.Cmd) {
	a.cleanMsgCh = nil
	a.cleaningScr.UpdateCleanResult(msg.category, msg.result, msg.err)

	if a.cleaningScr.Done {
		return a.finishCleaning()
	}

	return a.startNextClean()
}

// handleElevateComplete applies the outcome of one elevate.Invoke call
// (covering every sudo category the user chose to authenticate for) to the
// cleaning screen, then resumes the normal per-category clean loop for
// whatever non-sudo categories are still pending.
//
// The three-way split below mirrors elevate.Invoke's documented error
// contract exactly: only ErrElevationFailed licenses "nothing was deleted"
// wording (rendered here as a skip); everything else -- a successful call,
// ErrElevationOutcomeUnknown, or any other unexpected error -- must assume a
// partial clean is possible and never claim otherwise.
func (a App) handleElevateComplete(msg elevateCompleteMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err == nil:
		byCategory := make(map[cleaner.Category]commands.CleanCategoryResult, len(msg.result.Clean.Categories))
		for _, ccr := range msg.result.Clean.Categories {
			byCategory[ccr.Category] = ccr
		}
		intersections := make(map[cleaner.Category]elevate.CategoryIntersection, len(msg.result.Intersections))
		for _, ci := range msg.result.Intersections {
			intersections[ci.Category] = ci
		}

		// elevatedOnly holds each category's OWN elevated-leg outcome, kept
		// separate from the (possibly direct-merged) counts written to
		// cleaningScr below. A direct leg already got its own history row
		// the moment it finished (see recordDirectHistory), so
		// recordElevatedHistory must record only what this elevate.Invoke
		// call itself contributed -- recording the merged UI-facing total
		// here too would double the direct portion into two rows instead of
		// complementing it with exactly one.
		var elevatedOnly []*cleaner.CleanResult

		for _, pc := range msg.plan.Categories {
			direct, hasDirect := msg.direct[pc.Category]

			if ccr, ok := byCategory[pc.Category]; ok {
				elevatedOnly = append(elevatedOnly, &cleaner.CleanResult{
					Category:     ccr.Category,
					FilesDeleted: ccr.DeletedFiles,
					BytesFreed:   ccr.DeletedSize,
					DryRun:       msg.plan.DryRun,
					Errors:       partialErrorsFromResult(ccr),
				})

				merged := ccr
				if hasDirect {
					merged = commands.MergeCategoryResults(direct, ccr)
				}
				cr := &cleaner.CleanResult{
					Category:     merged.Category,
					FilesDeleted: merged.DeletedFiles,
					BytesFreed:   merged.DeletedSize,
					DryRun:       msg.plan.DryRun,
					Errors:       partialErrorsFromResult(merged),
				}
				var cerr error
				if merged.ErrMsg != "" {
					cerr = errors.New(merged.ErrMsg)
				}
				a.cleaningScr.UpdateCleanResult(pc.Category, cr, cerr)
				continue
			}
			if in, ok := intersections[pc.Category]; ok {
				if in.ErrMsg != "" {
					// A direct leg's confirmed deletions are real regardless
					// of what the elevated leg reports -- never drop them
					// just because this category's elevated half errored.
					cr, cerr := directOnlyResult(direct, in.ErrMsg, msg.plan.DryRun)
					if hasDirect {
						a.cleaningScr.UpdateCleanResult(pc.Category, cr, cerr)
					} else {
						a.cleaningScr.UpdateCleanResult(pc.Category, nil, errors.New(in.ErrMsg))
					}
					continue
				}
				// Approved but nothing matched the helper's fresh root scan:
				// already gone, or never belonged to this category. Not an
				// error -- a skip. But a direct leg's real deletions must
				// still be reported, never discarded as "nothing is known".
				if hasDirect {
					cr, cerr := directOnlyResult(direct, "", msg.plan.DryRun)
					a.cleaningScr.UpdateCleanResult(pc.Category, cr, cerr)
				} else {
					a.cleaningScr.SkipCategory(pc.Category, "none of the approved items were found by the elevated helper's fresh scan (already gone, or no longer in this category)")
				}
				continue
			}
			// The helper reported success overall but said nothing at all
			// about this specific category -- unlike the two cases above,
			// there is no observation to report a confident skip from. Same
			// direct-leg carve-out as above.
			if hasDirect {
				cr, cerr := directOnlyResult(direct, "the elevated helper returned no result for this category; outcome unknown", msg.plan.DryRun)
				a.cleaningScr.UpdateCleanResult(pc.Category, cr, cerr)
			} else {
				a.cleaningScr.UpdateCleanResult(pc.Category, nil, errors.New("the elevated helper returned no result for this category; outcome unknown"))
			}
		}

		a.recordElevatedHistory(msg.plan, elevatedOnly)

	case errors.Is(msg.err, elevate.ErrElevationFailed):
		// msg.err always carries elevate.Invoke's own explanation (auth
		// failure, a guard rejection, or a spawn failure) -- surface it
		// rather than guessing a single specific cause, since only the
		// "nothing was deleted" half of any hardcoded guess is guaranteed
		// true for every case ErrElevationFailed covers.
		reason := fmt.Sprintf("elevation did not run (%v); nothing was deleted", msg.err)
		for _, pc := range msg.plan.Categories {
			// The direct leg (if any) ran in this process before elevation
			// was even attempted -- its deletions happened regardless of
			// whether the elevated half ran at all, so this category cannot
			// be reported as a plain skip ("nothing is known").
			if direct, ok := msg.direct[pc.Category]; ok {
				cr, cerr := directOnlyResult(direct, reason, msg.plan.DryRun)
				a.cleaningScr.UpdateCleanResult(pc.Category, cr, cerr)
				continue
			}
			a.cleaningScr.SkipCategory(pc.Category, reason)
		}

	default:
		// Includes ErrElevationOutcomeUnknown and any other unexpected
		// failure: the helper may have been past its guards and mid-deletion,
		// so this must never read as "skipped" or "nothing happened".
		errMsg := fmt.Sprintf("elevated helper outcome unknown (%v); re-scan to check what was deleted", msg.err)
		for _, pc := range msg.plan.Categories {
			if direct, ok := msg.direct[pc.Category]; ok {
				cr, cerr := directOnlyResult(direct, errMsg, msg.plan.DryRun)
				a.cleaningScr.UpdateCleanResult(pc.Category, cr, cerr)
				continue
			}
			a.cleaningScr.UpdateCleanResult(pc.Category, nil, errors.New(errMsg))
		}
	}

	if a.cleaningScr.Done {
		return a.finishCleaning()
	}
	return a.startNextClean()
}

// recordElevatedHistory persists the outcome of a completed elevate.Invoke
// immediately, before any later non-sudo category starts cleaning.
//
// The elevated deletion is real and final the moment the helper returns, but
// the run as a whole only reaches finishCleaning once every remaining
// category has resolved -- and the quit key (see Update) cancels and exits
// right away without going through finishCleaning. Without this, pressing q
// during the non-sudo phase would take the audit trail of an already-completed
// root deletion with it. Interactive `clean --execute` records its elevated
// part the same way, for the same reason; the cost is that a mixed run shows
// up as two history records.
//
// elevatedOnly must hold each plan category's OWN elevated-leg counts, never
// the merged total a split category may show on the cleaning screen: a
// direct leg already wrote its own history row the instant it finished (see
// recordDirectHistory), independent of whatever this elevate.Invoke call
// does, so recording the merged counts here as well would double the direct
// portion into two rows instead of complementing it with exactly one. Every
// plan category is marked in elevatedRecorded regardless of whether it made
// it into elevatedOnly, so finishCleaning never revisits it either way.
func (a *App) recordElevatedHistory(plan elevate.Plan, elevatedOnly []*cleaner.CleanResult) {
	if !a.executeMode || plan.DryRun {
		return
	}
	if a.elevatedRecorded == nil {
		a.elevatedRecorded = make(map[cleaner.Category]struct{}, len(plan.Categories))
	}
	for _, pc := range plan.Categories {
		a.elevatedRecorded[pc.Category] = struct{}{}
	}

	record := buildTUIRunRecord(elevatedOnly, a.cleanStartTime, time.Since(a.cleanStartTime).Milliseconds())
	if len(record.Categories) == 0 {
		return
	}
	_ = history.Append(record)
}

// recordDirectHistory persists a direct-clean leg's own, already-final
// outcome the moment handleDirectCleanComplete observes it -- before that
// category's elevated leg (if any) has even started, and long before
// finishCleaning would otherwise reach it. Mirrors recordElevatedHistory's
// reasoning exactly: a real deletion performed in this process is just as
// vulnerable to being lost to a mid-run quit as one the elevated helper
// performed, and handleDirectCleanComplete is the only place that knows the
// leg finished at all.
//
// category is marked in elevatedRecorded unconditionally, whether or not it
// also requires elevation: for a direct-only category (no sudo entries at
// all) this is the ONLY history write it will ever get, so finishCleaning
// must never touch it either. For a split category, the elevated leg gets
// its own separate history row later from recordElevatedHistory -- two rows
// for one category in the same run, the same accepted trade-off already
// documented above for mixed elevated/non-sudo runs.
func (a *App) recordDirectHistory(category cleaner.Category, result commands.CleanCategoryResult, dryRun bool) {
	if a.elevatedRecorded == nil {
		a.elevatedRecorded = make(map[cleaner.Category]struct{})
	}
	a.elevatedRecorded[category] = struct{}{}

	if !a.executeMode || dryRun {
		return
	}
	cr := &cleaner.CleanResult{
		Category:     result.Category,
		FilesDeleted: result.DeletedFiles,
		BytesFreed:   result.DeletedSize,
	}
	record := buildTUIRunRecord([]*cleaner.CleanResult{cr}, a.cleanStartTime, time.Since(a.cleanStartTime).Milliseconds())
	if len(record.Categories) == 0 {
		return
	}
	_ = history.Append(record)
}

// finishCleaning is the single terminal response for a completed cleaning
// run: it appends the run to history (execute mode only) and returns a nil
// command. Every place that can make cleaningScr.Done flip true -- including
// a run that finishes entirely through skips inside startNextClean's loop,
// with no cleanCompleteMsg, elevateCompleteMsg, nor directCleanCompleteMsg
// ever arriving -- must route through here, or that run goes unrecorded even
// though it may have deleted real files as root.
//
// Categories already persisted by recordElevatedHistory or
// recordDirectHistory (both mark a.elevatedRecorded, see its own comment)
// are left out so a mixed run is not double-counted; if nothing but those
// ran, there is no second record to write at all.
func (a App) finishCleaning() (tea.Model, tea.Cmd) {
	if !a.executeMode {
		return a, nil
	}
	var results []*cleaner.CleanResult
	for _, r := range a.cleaningScr.Results() {
		if _, done := a.elevatedRecorded[r.Category]; done {
			continue
		}
		results = append(results, r)
	}
	if len(results) == 0 && len(a.elevatedRecorded) > 0 {
		return a, nil
	}
	_ = history.Append(buildTUIRunRecord(results, a.cleanStartTime, time.Since(a.cleanStartTime).Milliseconds()))
	return a, nil
}

// startElevation builds a Plan covering every sudo-required category with
// approved (non-protected) entries and hands it to the elevated helper via a
// single sudo prompt -- one password for every sudo category at once, rather
// than one per category. Categories that end up with nothing to elevate for
// (every entry protected) are resolved immediately without a password
// prompt; if none of them have anything to do, the normal clean loop takes
// over unchanged.
//
// Splitting a category's entries (commands.SplitEntriesByPrivilege) can
// produce a direct-clean leg -- entries the invoking user already owns and
// that need no root at all. That leg is dispatched as a directCleanCmd,
// never run inline here: this function must return promptly so the event
// loop keeps handling key presses (including q) and ctx cancellation while
// the deletion runs, exactly as every other deletion in this file already
// does via cleanCategoryStreamCmd. If any direct legs are in flight, the
// elevate.Plan built below is stashed on a.pendingElevation rather than
// dispatched immediately; handleDirectCleanComplete dispatches it once every
// direct leg has reported back.
func (a App) startElevation() (tea.Model, tea.Cmd) {
	sudoCats := make(map[cleaner.Category]struct{}, len(a.reviewScr.SudoCategories))
	for _, cat := range a.reviewScr.SudoCategories {
		sudoCats[cat] = struct{}{}
	}

	var plan elevate.Plan
	plan.DryRun = !a.executeMode

	// direct/pendingDirect accumulate as directCleanCmd results arrive (see
	// pendingElevationState); directCmds is what actually gets dispatched
	// below if any category needed one.
	direct := map[cleaner.Category]commands.CleanCategoryResult{}
	pendingDirect := map[cleaner.Category]struct{}{}
	var directCmds []tea.Cmd

	for i := range a.cleaningScr.Categories {
		cat := &a.cleaningScr.Categories[i]
		if _, ok := sudoCats[cat.Category]; !ok {
			continue
		}
		c, ok := a.registry.Get(cat.Category)
		if !ok {
			continue
		}

		// Mirrors startNextClean's whole-domain guard. No registered cleaner
		// is currently both RequiresSudo() and DeletesWholeDomain() (see
		// TestNoCleanerIsBothSudoAndWholeDomain), so this branch should be
		// unreachable today -- kept so the two guard sets can't silently
		// diverge if that ever changes.
		if protected := config.CountProtected(cat.Entries); protected > 0 && c.DeletesWholeDomain() {
			a.cleaningScr.SkipCategory(cat.Category, fmt.Sprintf("%d protected path(s) found in this category, and %s cannot selectively clean around them.", protected, cat.Category.DisplayName()))
			continue
		}

		entries := config.StripProtected(cat.Entries)
		if len(entries) == 0 {
			a.cleaningScr.SkipCategory(cat.Category, "every entry in this category is a protected path")
			continue
		}

		// F-F: only the subset that genuinely needs root (e.g. Temp's shared
		// /tmp and /var/tmp) is elevated; entries the invoking user already
		// owns are cleaned directly -- asynchronously, see above.
		sudoEntries, directEntries := commands.SplitEntriesByPrivilege(c, entries)

		cat.Status = "cleaning"
		cat.StartedAt = time.Now()

		if len(directEntries) > 0 {
			pendingDirect[cat.Category] = struct{}{}
			directCmds = append(directCmds, directCleanCmd(a.ctx, c, directEntries, plan.DryRun))
		}

		if len(sudoEntries) == 0 {
			// Nothing in this category needs root. handleDirectCleanComplete
			// resolves it fully once the direct leg above reports back --
			// it never enters plan, and is never added to
			// a.elevatedRecorded until that happens (see
			// recordDirectHistory), exactly like an ordinary non-sudo
			// category would be if nothing here had gone through
			// elevation at all.
			continue
		}

		plan.Categories = append(plan.Categories, elevate.PlanCategory{
			Category: cat.Category,
			Entries:  sudoEntries,
		})
	}

	if len(pendingDirect) > 0 {
		a.pendingElevation = &pendingElevationState{
			plan:          plan,
			direct:        direct,
			pendingDirect: pendingDirect,
		}
		return a, tea.Batch(directCmds...)
	}

	if len(plan.Categories) == 0 {
		return a.startNextClean()
	}

	return a, elevateCmd(a.ctx, plan, direct)
}

// directCleanCmd runs a category's direct-clean leg (the unprivileged subset
// of a sudo category's approved entries, see commands.SplitEntriesByPrivilege)
// as an ordinary tea.Cmd, exactly like cleanCategoryStreamCmd does for a
// non-sudo category's Clean call. Running it synchronously inside Update, as
// startElevation once did, would block the whole event loop for the
// deletion's entire duration -- no key press (including q) could reach
// a.cancel(), and ctx cancellation, the mechanism that actually stops
// c.Clean mid-walk, would be unreachable until the call returned on its own.
// commands.CleanDirectly does not accept a progress callback, so this leg
// reports no incremental progress the way a normal category's clean does;
// the category's status is set to "cleaning" by startElevation before this
// dispatches, so the screen at least does not read as untouched while it
// runs.
func directCleanCmd(ctx context.Context, c cleaner.Cleaner, entries []cleaner.FileEntry, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		return directCleanCompleteMsg{
			category: c.Category(),
			result:   commands.CleanDirectly(ctx, c, entries, dryRun),
		}
	}
}

// handleDirectCleanComplete applies the outcome of one category's direct leg
// as it arrives (see startElevation/directCleanCmd). A direct leg's deletion
// is real and final the instant this fires, so its history row is written
// right here, immediately -- see recordDirectHistory -- rather than waiting
// for finishCleaning or for this category's elevated leg (if any) to also
// resolve.
//
// A category with no sudo entries at all is fully resolved here: no elevated
// leg is coming, so its CleanResult goes to the cleaning screen now. A split
// category (sudo entries still pending) only gets its history row written
// here; handleElevateComplete finishes wiring the cleaning screen up once
// the elevated leg reports back, merging this leg's counts in (see
// commands.MergeCategoryResults).
//
// Once every direct leg this elevation attempt was waiting on has reported
// back, whatever plan accumulated in startElevation is dispatched, exactly
// as it would have been synchronously before direct legs existed.
func (a App) handleDirectCleanComplete(msg directCleanCompleteMsg) (tea.Model, tea.Cmd) {
	pe := a.pendingElevation
	if pe == nil {
		// Defensive only: directCleanCompleteMsg is only ever produced by a
		// tea.Cmd startElevation dispatches in the same step it sets
		// a.pendingElevation, so this should be unreachable.
		return a, nil
	}

	pe.direct[msg.category] = msg.result
	delete(pe.pendingDirect, msg.category)

	a.recordDirectHistory(msg.category, msg.result, pe.plan.DryRun)

	needsElevation := false
	for _, pc := range pe.plan.Categories {
		if pc.Category == msg.category {
			needsElevation = true
			break
		}
	}
	if !needsElevation {
		cr, cerr := directOnlyResult(msg.result, "", pe.plan.DryRun)
		a.cleaningScr.UpdateCleanResult(msg.category, cr, cerr)
	}

	if len(pe.pendingDirect) > 0 {
		return a, nil
	}

	plan, direct := pe.plan, pe.direct
	a.pendingElevation = nil

	if len(plan.Categories) == 0 {
		return a.startNextClean()
	}
	return a, elevateCmd(a.ctx, plan, direct)
}

// elevateRun adapts elevate.Invoke to bubbletea's tea.Exec: tea.Exec releases
// the terminal for the duration of Run() so sudo's own password prompt can
// use it directly, exactly like handing the terminal to an external editor.
// Invoke already targets os.Stdin/os.Stderr itself once the terminal is
// released to it, so the Set* methods have nothing to do.
type elevateRun struct {
	ctx    context.Context
	plan   elevate.Plan
	result elevate.Result
	err    error
}

func (e *elevateRun) SetStdin(io.Reader)  {}
func (e *elevateRun) SetStdout(io.Writer) {}
func (e *elevateRun) SetStderr(io.Writer) {}

func (e *elevateRun) Run() error {
	e.result, e.err = elevate.Invoke(e.ctx, e.plan)
	return e.err
}

func elevateCmd(ctx context.Context, plan elevate.Plan, direct map[cleaner.Category]commands.CleanCategoryResult) tea.Cmd {
	e := &elevateRun{ctx: ctx, plan: plan}
	return tea.Exec(e, func(err error) tea.Msg {
		// Deliberately e.err, not the err bubbletea passes here: when Run
		// succeeds, bubbletea's Program.exec calls this callback with
		// RestoreTerminal's error instead, which is unrelated to whether
		// elevate.Invoke actually succeeded and would otherwise cause a
		// fully successful sudo clean to be misreported as outcome-unknown.
		return elevateCompleteMsg{plan: plan, result: e.result, err: e.err, direct: direct}
	})
}

func (a App) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// to-do: implement re-run scan for a category when it's selected and user presses "r"

	var keyType string
	switch {
	case key.Matches(msg, keys.Up):
		keyType = "up"
	case key.Matches(msg, keys.Down):
		keyType = "down"
	case key.Matches(msg, keys.Confirm):
		keyType = "enter"
	}

	dash, action := a.dashboard.HandleKey(msg.String(), keyType)
	a.dashboard = dash

	if action != nil {
		if dmsg, ok := action.(screens.DashboardMsg); ok {
			a.scanningScr = screens.NewScanning(dmsg.Selected, a.registry)
			a.scanningScr.SetSize(a.width, a.height)
			a.reviewBuilt = false // new scan invalidates the previous review state
			a.currentScreen = screenScanning

			cmds := []tea.Cmd{a.scanningScr.Spinner.Tick}
			for _, id := range dmsg.Selected {
				c, ok := a.registry.Get(cleaner.Category(id))
				if !ok {
					continue
				}

				if result, exists := a.scanResults[cleaner.Category(id)]; exists {
					a.scanningScr.UpdateScanResult(cleaner.Category(id), result, nil)
				} else {
					cmds = append(cmds, scanCategoryCmd(a.ctx, c))
				}
			}
			return a, tea.Batch(cmds...)
		}
	}

	return a, nil
}

func (a App) updateScanning(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.scanningScr.AllDone() {
		if key.Matches(msg, keys.Confirm) {
			if !a.reviewBuilt {
				results := a.scanningScr.Results()
				a.reviewScr = screens.NewReview(results, a.executeMode, a.registry, a.isElevated)
				a.reviewScanResults = results
				a.reviewBuilt = true
			}
			a.reviewScr.SetSize(a.width, a.height)
			a.currentScreen = screenReview
			return a, nil
		}
	}

	if key.Matches(msg, keys.Back) {
		a.currentScreen = screenDashboard
		return a, nil
	}

	return a, nil
}

func (a App) updateReview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.revalidating {
		// A revalidation is in flight (dispatched from the Confirm case
		// below). ReviewModel.View()'s Revalidating branch takes priority
		// over every other render while this is true, so the sudo dialog,
		// the file list, none of it is actually on screen -- any other key
		// here would mutate state (most importantly AuthenticateSudo via
		// up/down, see keys.Up/Down below) that the user cannot see the
		// effect of, and that mutated state would then feed whatever the
		// in-flight result eventually does. Every key except back is
		// ignored until it resolves.
		if key.Matches(msg, keys.Back) {
			// Actually invalidates the dispatch rather than merely hoping
			// currentScreen changes enough to reject it later:
			// revalidateSeq is bumped here so the in-flight
			// revalidateCompleteMsg's stamped seq can never match again,
			// even if the user later navigates back into screenReview and
			// currentScreen would otherwise read the same as it did at
			// dispatch time.
			a.revalidating = false
			a.reviewScr.Revalidating = false
			a.reviewScr.RevalidationErr = nil
			a.revalidateSeq++
			if a.reviewScr.ConfirmState != screens.ConfirmNone {
				a.reviewScr.ConfirmState = screens.ConfirmNone
				return a, nil
			}
			a.currentScreen = screenScanning
			return a, nil
		}
		return a, nil
	}

	switch {
	case key.Matches(msg, keys.Confirm):
		if a.reviewScr.TotalFiles == 0 {
			return a, nil
		}
		switch a.reviewScr.ConfirmState {
		case screens.ConfirmNone:
			if a.reviewScr.ShouldWarnAboutSudo() {
				a.reviewScr.ConfirmState = screens.ConfirmSudo
				return a, nil
			}
			if a.executeMode {
				a.reviewScr.ConfirmState = screens.ConfirmExecute
				return a, nil
			}
			// dry run: proceed immediately without an extra confirmation step
		case screens.ConfirmSudo:
			// ConfirmSudo is only ever entered via ShouldWarnAboutSudo(),
			// which requires ExecuteMode, so this is always the dry-run-free
			// path straight to the final delete confirmation.
			a.reviewScr.ConfirmState = screens.ConfirmExecute
			return a, nil
		case screens.ConfirmExecute, screens.ConfirmRevalidated:
			// Both land here the same way: ConfirmExecute's own "delete?"
			// question was already answered, and ConfirmRevalidated's second
			// enter is the user re-confirming the corrected plan revalidation
			// found below. Either way there is nothing left to ask before
			// revalidating (or re-revalidating) one more time.
			a.reviewScr.ConfirmState = screens.ConfirmNone
		}

		// Re-check the approved snapshot against disk and the current config
		// right before it reaches cleaning: a.reviewScanResults may be
		// however long the user sat on the review screen out of date, and
		// nothing before this point has ever re-verified it. Still
		// deliberately a.reviewScanResults, not a fresh a.scanningScr.Results()
		// call -- revalidation only ever narrows what was already approved,
		// it never lets a background re-scan introduce something new. See
		// revalidatePlan's own doc comment.
		//
		// Dispatched as a Cmd rather than called inline: it os.Stats every
		// approved entry and, for Docker/Time Machine, shells out to their
		// EntryRevalidator, so running it synchronously here would freeze
		// the event loop for however long that takes -- see revalidateCmd's
		// own doc comment, which is the exact reasoning startElevation
		// already documents for why directCleanCmd exists.
		//
		// revalidateSeq is stamped into the dispatched message and compared
		// back in handleRevalidateComplete; the a.revalidating guard at the
		// top of this function is what actually keeps a second enter from
		// reaching here and dispatching a second revalidateCmd, and is also
		// what routes esc, while this is in flight, to bump revalidateSeq
		// instead of falling through to the ordinary Back case below.
		a.revalidating = true
		a.reviewScr.Revalidating = true
		a.revalidateSeq++
		return a, revalidateCmd(a.ctx, a.revalidateSeq, a.registry, a.cfg, a.reviewScanResults)

	case key.Matches(msg, keys.Back):
		// a.revalidating is never true here: the top-of-function guard
		// above handles back on its own while one is in flight.
		a.reviewScr.RevalidationErr = nil
		if a.reviewScr.ConfirmState != screens.ConfirmNone {
			a.reviewScr.ConfirmState = screens.ConfirmNone
			return a, nil
		}
		a.currentScreen = screenScanning
		return a, nil

	case key.Matches(msg, keys.Up):
		if a.reviewScr.ConfirmState == screens.ConfirmSudo {
			a.reviewScr.AuthenticateSudo = !a.reviewScr.AuthenticateSudo
			return a, nil
		}
		a.reviewScr.ScrollUp()
	case key.Matches(msg, keys.Down):
		if a.reviewScr.ConfirmState == screens.ConfirmSudo {
			a.reviewScr.AuthenticateSudo = !a.reviewScr.AuthenticateSudo
			return a, nil
		}
		a.reviewScr.ScrollDown()
	case key.Matches(msg, keys.SelectAll):
		a.reviewScr.ToggleShowAll()
	case key.Matches(msg, keys.FullPath):
		a.reviewScr.ToggleFullPath()
	case key.Matches(msg, keys.NextList):
		a.reviewScr.NextCategory()
	}

	return a, nil
}

// handleRevalidateComplete applies the outcome of revalidateCmd, dispatched
// from updateReview's final confirm step. See revalidatePlan's own doc
// comment for what it checks; this is purely the TUI-state decision built on
// top of that result.
func (a App) handleRevalidateComplete(msg revalidateCompleteMsg) (tea.Model, tea.Cmd) {
	// A stale result. msg.seq != a.revalidateSeq is the real defense here:
	// updateReview's top-of-function a.revalidating guard bumps
	// revalidateSeq the instant the user presses back while this is in
	// flight, so a dispatch the user has abandoned can never match again --
	// not even if they later navigate back into screenReview and
	// currentScreen happens to read the same as it did at dispatch time.
	// currentScreen != screenReview is kept as a second, independent check
	// (e.g. against some future path that changes screens without going
	// through updateReview's Back handling). Acting on a stale result
	// regardless of where the app is now is exactly how an aborted confirm
	// could still start deleting, or how two overlapping enter presses
	// could each dispatch their own clean pipeline over the same plan.
	if msg.seq != a.revalidateSeq || a.currentScreen != screenReview {
		return a, nil
	}
	a.revalidating = false
	a.reviewScr.Revalidating = false

	err := msg.err
	if err == nil && len(msg.categoryErrs) > 0 {
		// A category's own revalidation failing (e.g. Docker unreachable at
		// confirm time) is data, not a structural error, but it must still
		// block rather than silently vanish: NewCleaningModel below skips
		// any category with 0 files with no trace of why. Surfacing it the
		// same way a structural error is surfaced -- stop here, let the user
		// retry with enter or back out with esc -- keeps every failure
		// visible without teaching NewCleaningModel a new shape of "empty
		// but not actually done" category. Deliberately NOT the same check
		// as "does this category carry any ScanResult.Errors": those may be
		// pre-existing, non-fatal scan-time issues (see revalidatePlan's doc
		// comment) that must never permanently block confirming.
		err = joinCategoryErrs(msg.categoryErrs)
	}
	if err != nil {
		a.reviewScr.RevalidationErr = err
		return a, nil
	}
	a.reviewScr.RevalidationErr = nil
	a.reviewScanResults = msg.results

	if msg.delta.Material() {
		// Rebuild reviewScr from the revalidated snapshot rather than only
		// swapping reviewScanResults: the review screen's own file list and
		// totals otherwise keep showing the pre-revalidation state (a file
		// already known gone, a total that no longer matches) underneath
		// the very screen telling the user something changed. AuthenticateSudo
		// is the one piece of user intent NewReview would otherwise reset to
		// its default (skip) -- explicitly carried over so re-confirming a
		// revalidated plan can never silently discard that choice.
		authenticateSudo := a.reviewScr.AuthenticateSudo
		delta := msg.delta
		a.reviewScr = screens.NewReview(a.reviewScanResults, a.executeMode, a.registry, a.isElevated)
		a.reviewScr.SetSize(a.width, a.height)
		a.reviewScr.AuthenticateSudo = authenticateSudo
		// Kept even when nothing survived (TotalFiles == 0 below), so
		// View()'s empty-plan message can say why the plan is empty instead
		// of reusing the same "already tidy" wording a scan that genuinely
		// found nothing would show.
		a.reviewScr.RevalidationDelta = &delta
		if a.reviewScr.TotalFiles > 0 {
			a.reviewScr.ConfirmState = screens.ConfirmRevalidated
		}
		// Else: nothing survived revalidation. ReviewModel.View()'s own
		// TotalFiles == 0 branch renders an empty-plan message instead --
		// entering ConfirmRevalidated here would show a "confirm updated
		// plan" prompt whose enter key is a silent no-op (updateReview's
		// own TotalFiles == 0 guard, at the very top of its Confirm case).
		return a, nil
	}
	a.reviewScr.RevalidationDelta = nil

	a.cleaningScr = screens.NewCleaningModel(a.reviewScanResults, !a.executeMode)
	a.cleaningScr.SetSize(a.width, a.height)
	a.currentScreen = screenCleaning
	a.cleanStartTime = time.Now()

	if a.reviewScr.ShouldWarnAboutSudo() && a.reviewScr.AuthenticateSudo {
		return a.startElevation()
	}
	return a.startNextClean()
}

// joinCategoryErrs turns a set of per-category revalidation failures into
// one deterministic error: map iteration order is not, so picking or
// ordering by it would make the reported message (and, in a test, which
// category "wins") vary run to run for the same input.
func joinCategoryErrs(categoryErrs map[cleaner.Category]error) error {
	names := make([]string, 0, len(categoryErrs))
	byName := make(map[string]cleaner.Category, len(categoryErrs))
	for cat := range categoryErrs {
		names = append(names, string(cat))
		byName[string(cat)] = cat
	}
	sort.Strings(names)

	errs := make([]error, 0, len(names))
	for _, name := range names {
		cat := byName[name]
		errs = append(errs, fmt.Errorf("%s: %w", cat.DisplayName(), categoryErrs[cat]))
	}
	return errors.Join(errs...)
}

func (a App) updateCleaning(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.cleaningScr.Done {
		if key.Matches(msg, keys.Confirm) {
			results := a.cleaningScr.Results()
			a.summaryScr = screens.NewSummary(results, !a.executeMode)
			a.summaryScr.SetSize(a.width, a.height)
			a.currentScreen = screenSummary
			return a, nil
		}
	}
	return a, nil
}

func (a App) updateSummary(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// if context is done, quit immediately on any key press
	if a.ctx.Err() != nil {
		return a, tea.Quit
	}

	if key.Matches(msg, keys.ShowErrors) {
		a.summaryScr.ToggleShowErrors()
		return a, nil
	}

	if key.Matches(msg, keys.Confirm) {
		// Reset and return to dashboard for re-run
		a.scanResults = make(map[cleaner.Category]*cleaner.ScanResult)
		a.dashboard = screens.NewDashboard()
		a.dashboard.SetSize(a.width, a.height)
		a.currentScreen = screenDashboard

		// Re-scan all categories
		cmds := []tea.Cmd{a.spinner.Tick, gatherHealthInfoCmd(a.ctx)}
		for _, c := range a.registry.All() {
			a.dashboard.SetCategoryScanning(string(c.Category()))
			cmds = append(cmds, scanCategoryCmd(a.ctx, c))
		}
		a.scanning = true
		return a, tea.Batch(cmds...)
	}
	return a, nil
}

func (a App) startNextClean() (tea.Model, tea.Cmd) {
	for {
		next := a.cleaningScr.NextCategory()
		if next == nil {
			// No pending category left. Normally this means every category
			// resolved inside this loop's own skip branches (each of which
			// already routes through finishCleaning when it flips Done) and
			// this is unreachable; the check is kept anyway as a safety net
			// against ending a fully-resolved run without recording it.
			if a.cleaningScr.Done {
				return a.finishCleaning()
			}
			return a, nil
		}

		c, ok := a.registry.Get(next.Category)
		if !ok {
			continue
		}

		// By the time this loop runs, startElevation has already resolved
		// every sudo category the user chose to authenticate for -- so this
		// branch only fires for a category the user explicitly chose to skip
		// on the sudo dialog (or the ConfirmSudo dialog never appeared, e.g.
		// this is a re-run after "back").
		if a.executeMode && !a.isElevated && c.RequiresSudo() {
			a.cleaningScr.SkipCategory(next.Category, fmt.Sprintf("%s requires sudo; skipped because you chose not to authenticate.", c.Category().DisplayName()))
			if a.cleaningScr.Done {
				return a.finishCleaning()
			}
			continue
		}

		if protected := config.CountProtected(next.Entries); protected > 0 && c.DeletesWholeDomain() {
			// This cleaner cannot honor a filtered entry list (it shells out
			// to a command that clears its entire domain), so there's no way
			// to run it without also deleting protected paths. Skip the
			// category entirely rather than silently ignoring the protection.
			a.cleaningScr.SkipCategory(next.Category, fmt.Sprintf("%d protected path(s) found in this category, and %s cannot selectively clean around them.", protected, c.Category().DisplayName()))
			if a.cleaningScr.Done {
				return a.finishCleaning()
			}
			continue
		}

		dryRun := !a.executeMode
		entries := config.StripProtected(next.Entries)
		cmd, msgCh := cleanCategoryStreamCmd(a.ctx, c, entries, dryRun)
		a.cleanMsgCh = msgCh
		return a, cmd
	}
}

func cleanCategoryStreamCmd(ctx context.Context, c cleaner.Cleaner, entries []cleaner.FileEntry, dryRun bool) (tea.Cmd, <-chan tea.Msg) {
	msgCh := make(chan tea.Msg)

	go func() {
		defer close(msgCh)

		progressFunc := func(p cleaner.CleanProgress) {
			select {
			case <-ctx.Done():
				return
			case msgCh <- cleanProgressMsg{progress: p}:
			}
		}

		result, err := c.Clean(ctx, entries, dryRun, progressFunc)

		select {
		case <-ctx.Done():
			return
		case msgCh <- cleanCompleteMsg{
			category: c.Category(),
			result:   result,
			err:      err,
		}:
		}
	}()

	return waitForCleanMsgCmd(msgCh), msgCh
}

func waitForCleanMsgCmd(msgCh <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-msgCh
		if !ok {
			return nil
		}
		return msg
	}
}

func (a App) View() string {
	// Global header with ASCII logo and tagline
	header := styles.RenderLogo() + "\n" + styles.RenderTagLine() + "\n\n"

	var banner string
	if !a.executeMode {
		banner = styles.DryRunBanner.Render("DRY RUN MODE - No files will be deleted. Start the app with --execute to clean.") + "\n"
	}

	var content string
	switch a.currentScreen {
	case screenDashboard:
		content = a.dashboard.View()
		if a.scanning {
			content += "\n" + styles.Dim.Render(" "+a.spinner.View()+" scanning filesystem...")
		}

	case screenScanning:
		content = a.scanningScr.View()

	case screenReview:
		content = a.reviewScr.View()

	case screenCleaning:
		content = a.cleaningScr.View()
	case screenSummary:
		content = a.summaryScr.View()
	}

	return header + banner + content
}

func (a App) Model() tea.Model {
	return a
}

// partialErrorsFromResult rebuilds the per-item errors a cleaner collected
// on the root side, as they arrive over the helper's JSON result, so the
// summary screen shows them exactly as it would for a category cleaned in
// this process. The helper bounds the detail list; the count is preserved
// through a trailing summary error when it was cut.
func partialErrorsFromResult(ccr commands.CleanCategoryResult) []error {
	if ccr.PartialErrors == 0 {
		return nil
	}
	errs := make([]error, 0, len(ccr.PartialErrorDetails)+1)
	for _, ie := range ccr.PartialErrorDetails {
		if ie.Path != "" {
			errs = append(errs, fmt.Errorf("%s: %s", ie.Path, ie.Reason))
		} else {
			errs = append(errs, errors.New(ie.Reason))
		}
	}
	if more := ccr.PartialErrors - len(ccr.PartialErrorDetails); more > 0 {
		errs = append(errs, fmt.Errorf("%d more item(s) could not be cleaned", more))
	}
	return errs
}

// directOnlyResult converts a direct-clean leg (see commands.CleanDirectly)
// into the cleaner.CleanResult shape UpdateCleanResult expects, optionally
// folding in an error/reason from the elevated leg for the same category
// (an empty elevatedErrMsg means the elevated leg has nothing to add -- the
// category simply never needed elevation at all, or the elevated leg found
// nothing to report). Reuses MergeCategoryResults rather than a parallel
// rule so a direct leg's confirmed counts are never lost, whatever the
// elevated leg's outcome was.
func directOnlyResult(direct commands.CleanCategoryResult, elevatedErrMsg string, dryRun bool) (*cleaner.CleanResult, error) {
	merged := direct
	if elevatedErrMsg != "" {
		merged = commands.MergeCategoryResults(direct, commands.CleanCategoryResult{
			Err:    errors.New(elevatedErrMsg),
			ErrMsg: elevatedErrMsg,
		})
	}
	cr := &cleaner.CleanResult{
		Category:     merged.Category,
		FilesDeleted: merged.DeletedFiles,
		BytesFreed:   merged.DeletedSize,
		DryRun:       dryRun,
		Errors:       partialErrorsFromResult(merged),
	}
	var cerr error
	if merged.ErrMsg != "" {
		cerr = errors.New(merged.ErrMsg)
	}
	return cr, cerr
}

// buildTUIRunRecord turns the cleaning screen's results into a history
// record. Skipped categories and those that deleted nothing are left out;
// a category that deleted some files and then hit errors IS recorded with
// what it deleted -- the audit trail must reflect what happened on disk,
// and the errors are the summary screen's job.
func buildTUIRunRecord(results []*cleaner.CleanResult, ranAt time.Time, durationMs int64) history.RunRecord {
	var categories []history.CategoryRecord
	for _, r := range results {
		if r == nil || r.Skipped || (r.FilesDeleted == 0 && r.BytesFreed == 0) {
			continue
		}
		categories = append(categories, history.CategoryRecord{
			Name:        string(r.Category),
			DisplayName: r.Category.DisplayName(),
			Files:       r.FilesDeleted,
			Bytes:       r.BytesFreed,
		})
	}
	return history.NewRunRecord(ranAt, durationMs, categories)
}
