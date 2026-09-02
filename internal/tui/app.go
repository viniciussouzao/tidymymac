package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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

	// elevatedRecorded holds the categories whose deletion has already been
	// written to history by handleElevateComplete, so finishCleaning does
	// not record them a second time. See recordElevatedHistory for why the
	// elevated part is persisted early rather than with the rest of the run.
	elevatedRecorded map[cleaner.Category]struct{}

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

		for _, pc := range msg.plan.Categories {
			if ccr, ok := byCategory[pc.Category]; ok {
				cr := &cleaner.CleanResult{
					Category:     ccr.Category,
					FilesDeleted: ccr.DeletedFiles,
					BytesFreed:   ccr.DeletedSize,
					DryRun:       msg.plan.DryRun,
				}
				var cerr error
				if ccr.ErrMsg != "" {
					cerr = errors.New(ccr.ErrMsg)
				}
				a.cleaningScr.UpdateCleanResult(pc.Category, cr, cerr)
				continue
			}
			if in, ok := intersections[pc.Category]; ok {
				if in.ErrMsg != "" {
					a.cleaningScr.UpdateCleanResult(pc.Category, nil, errors.New(in.ErrMsg))
					continue
				}
				// Approved but nothing matched the helper's fresh root scan:
				// already gone, or never belonged to this category. Not an
				// error -- a skip.
				a.cleaningScr.SkipCategory(pc.Category, "none of the approved items were found by the elevated helper's fresh scan (already gone, or no longer in this category)")
				continue
			}
			// The helper reported success overall but said nothing at all
			// about this specific category -- unlike the two cases above,
			// there is no observation to report a confident skip from.
			a.cleaningScr.UpdateCleanResult(pc.Category, nil, errors.New("the elevated helper returned no result for this category; outcome unknown"))
		}

		a.recordElevatedHistory(msg.plan)

	case errors.Is(msg.err, elevate.ErrElevationFailed):
		// msg.err always carries elevate.Invoke's own explanation (auth
		// failure, a guard rejection, or a spawn failure) -- surface it
		// rather than guessing a single specific cause, since only the
		// "nothing was deleted" half of any hardcoded guess is guaranteed
		// true for every case ErrElevationFailed covers.
		reason := fmt.Sprintf("elevation did not run (%v); nothing was deleted", msg.err)
		for _, pc := range msg.plan.Categories {
			a.cleaningScr.SkipCategory(pc.Category, reason)
		}

	default:
		// Includes ErrElevationOutcomeUnknown and any other unexpected
		// failure: the helper may have been past its guards and mid-deletion,
		// so this must never read as "skipped" or "nothing happened".
		for _, pc := range msg.plan.Categories {
			a.cleaningScr.UpdateCleanResult(pc.Category, nil, fmt.Errorf("elevated helper outcome unknown (%v); re-scan to check what was deleted", msg.err))
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
// Only categories with a recorded deletion are written (the same rule
// buildTUIRunRecord applies); every plan category is marked regardless so
// finishCleaning does not revisit it.
func (a *App) recordElevatedHistory(plan elevate.Plan) {
	if !a.executeMode || plan.DryRun {
		return
	}
	if a.elevatedRecorded == nil {
		a.elevatedRecorded = make(map[cleaner.Category]struct{}, len(plan.Categories))
	}
	planned := make(map[cleaner.Category]struct{}, len(plan.Categories))
	for _, pc := range plan.Categories {
		planned[pc.Category] = struct{}{}
		a.elevatedRecorded[pc.Category] = struct{}{}
	}

	var results []*cleaner.CleanResult
	for _, r := range a.cleaningScr.Results() {
		if _, ok := planned[r.Category]; ok {
			results = append(results, r)
		}
	}
	record := buildTUIRunRecord(results, a.cleanStartTime, time.Since(a.cleanStartTime).Milliseconds())
	if len(record.Categories) == 0 {
		return
	}
	_ = history.Append(record)
}

// finishCleaning is the single terminal response for a completed cleaning
// run: it appends the run to history (execute mode only) and returns a nil
// command. Every place that can make cleaningScr.Done flip true -- including
// a run that finishes entirely through skips inside startNextClean's loop,
// with no cleanCompleteMsg or elevateCompleteMsg ever arriving -- must route
// through here, or that run goes unrecorded even though it may have deleted
// real files as root.
//
// Categories already persisted by recordElevatedHistory are left out so a
// mixed run is not double-counted; if nothing but those ran, there is no
// second record to write at all.
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
func (a App) startElevation() (tea.Model, tea.Cmd) {
	sudoCats := make(map[cleaner.Category]struct{}, len(a.reviewScr.SudoCategories))
	for _, cat := range a.reviewScr.SudoCategories {
		sudoCats[cat] = struct{}{}
	}

	var plan elevate.Plan
	plan.DryRun = !a.executeMode

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

		cat.Status = "cleaning"
		cat.StartedAt = time.Now()
		plan.Categories = append(plan.Categories, elevate.PlanCategory{
			Category: cat.Category,
			Entries:  entries,
		})
	}

	if len(plan.Categories) == 0 {
		return a.startNextClean()
	}

	return a, elevateCmd(a.ctx, plan)
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

func elevateCmd(ctx context.Context, plan elevate.Plan) tea.Cmd {
	e := &elevateRun{ctx: ctx, plan: plan}
	return tea.Exec(e, func(err error) tea.Msg {
		// Deliberately e.err, not the err bubbletea passes here: when Run
		// succeeds, bubbletea's Program.exec calls this callback with
		// RestoreTerminal's error instead, which is unrelated to whether
		// elevate.Invoke actually succeeded and would otherwise cause a
		// fully successful sudo clean to be misreported as outcome-unknown.
		return elevateCompleteMsg{plan: plan, result: e.result, err: e.err}
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
		case screens.ConfirmExecute:
			a.reviewScr.ConfirmState = screens.ConfirmNone
		}
		// Deliberately a.reviewScanResults, not a fresh a.scanningScr.Results()
		// call: what gets cleaned (and, for sudo categories, what gets sent
		// to the elevated helper) must be exactly what the review screen
		// showed, even if a background re-scan mutated scanningScr since.
		a.cleaningScr = screens.NewCleaningModel(a.reviewScanResults, !a.executeMode)
		a.cleaningScr.SetSize(a.width, a.height)
		a.currentScreen = screenCleaning
		a.cleanStartTime = time.Now()

		if a.reviewScr.ShouldWarnAboutSudo() && a.reviewScr.AuthenticateSudo {
			return a.startElevation()
		}
		return a.startNextClean()

	case key.Matches(msg, keys.Back):
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

func buildTUIRunRecord(results []*cleaner.CleanResult, ranAt time.Time, durationMs int64) history.RunRecord {
	var categories []history.CategoryRecord
	for _, r := range results {
		if r == nil || r.Skipped || len(r.Errors) > 0 || (r.FilesDeleted == 0 && r.BytesFreed == 0) {
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
