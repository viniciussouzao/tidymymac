package tui

import (
	"context"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/tui/screens"
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

	registry    *cleaner.Registry
	scanResults map[cleaner.Category]*cleaner.ScanResult
	spinner     spinner.Model
	scanning    bool
	ctx         context.Context
	cancel      context.CancelFunc

	// to-do: i want to use this for the generate script only feature
	scriptMessage string
}

// NewApp initializes the TUI application with default values and a spinner
func NewApp(execute bool) App {
	s := spinner.New()
	s.Spinner = spinner.Dot

	ctx, cancel := context.WithCancel(context.Background())

	return App{
		currentScreen: screenDashboard,
		executeMode:   execute,
		dashboard:     screens.NewDashboard(),
		registry:      cleaner.DefaultRegistry(),
		scanResults:   make(map[cleaner.Category]*cleaner.ScanResult),
		spinner:       s,
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Init is the initial command that runs when the TUI starts. It can be used to kick off any setup tasks or initial scans.
func (a App) Init() tea.Cmd {
	cmds := []tea.Cmd{
		a.spinner.Tick,
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

func cleanCategoryCmd(ctx context.Context, c cleaner.Cleaner, entries []cleaner.FileEntry, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		result, err := c.Clean(ctx, entries, dryRun, nil)
		return cleanCompleteMsg{
			category: c.Category(),
			result:   result,
			err:      err,
		}
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
		return a, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		var scanCmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		a.scanningScr.Spinner, scanCmd = a.scanningScr.Spinner.Update(msg) // update scanning screen spinner as well
		return a, tea.Batch(cmd, scanCmd)

	case scanCompleteMsg:
		return a.handleScanComplete(msg)

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

		}
	}

	return a, nil
}

func (a App) handleScanComplete(msg scanCompleteMsg) (tea.Model, tea.Cmd) {
	if msg.result != nil {
		a.scanResults[msg.category] = msg.result
		a.dashboard.UpdateCategorySize(string(msg.category), msg.result.TotalSize)
	} else {
		a.dashboard.UpdateCategorySize(string(msg.category), 0)
	}

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

func (a App) updateDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// to-do: implement re-run scan for a category when it's selected and user presses "r"
	if key.Matches(msg, keys.ReRun) {

	}

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
			results := a.scanningScr.Results()
			a.reviewScr = screens.NewReview(results, a.executeMode)
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
		if a.reviewScr.TotalSize == 0 {
			return a, nil
		}
		// results := a.scanningScr.Results()
		// a.cleaningScr = screens.NewCleaning(results, !a.executeMode)
		// a.cleaningScr.SetSize(a.width, a.height)
		// a.currentScreen = screenCleaning
		// return a.startNextClean()

	case key.Matches(msg, keys.Back):
		a.currentScreen = screenScanning
		return a, nil

	case key.Matches(msg, keys.Up):
		a.reviewScr.ScrollUp()
	case key.Matches(msg, keys.Down):
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
	if key.Matches(msg, keys.Confirm) {
		// Reset and return to dashboard for re-run
		a.scanResults = make(map[cleaner.Category]*cleaner.ScanResult)
		a.dashboard = screens.NewDashboard()
		a.dashboard.SetSize(a.width, a.height)
		a.currentScreen = screenDashboard

		// Re-scan all categories
		cmds := []tea.Cmd{a.spinner.Tick}
		for _, c := range a.registry.All() {
			a.dashboard.SetCategoryScanning(string(c.Category()))
			cmds = append(cmds, scanCategoryCmd(a.ctx, c))
		}
		a.scanning = true
		return a, tea.Batch(cmds...)
	}
	return a, nil
}

func (a App) View() string {
	// Global header with ASCII logo and tagline
	header := Logo() + "\n" + TagLine() + "\n\n"

	var banner string
	if !a.executeMode {
		banner = dryRunBannerStyle.Render("DRY RUN MODE - No files will be deleted. Start the app with --execute to clean.") + "\n"
	}

	var content string
	switch a.currentScreen {
	case screenDashboard:
		content = a.dashboard.View()
		if a.scanning {
			content += "\n" + dimStyle.Render(" "+a.spinner.View()+" scanning filesystem...")
		}

	case screenScanning:
		content = a.scanningScr.View()

	case screenReview:
		content = a.reviewScr.View()
	}

	return header + banner + content
}

func (a App) Model() tea.Model {
	return a
}
