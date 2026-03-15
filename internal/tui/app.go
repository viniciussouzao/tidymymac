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

	dashboard screens.DashboardModel

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
		return a, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd

	case tea.KeyMsg:
		if key.Matches(msg, keys.Quit) {
			a.cancel() // cancel any ongoing scans or cleans
			return a, tea.Quit
		}

		switch a.currentScreen {
		case screenDashboard:
			return a.updateDashboard(msg)
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
			_ = dmsg
			// to-do: transition to review screen with selected categories
		}
	}

	return a, nil
}

func (a App) View() string {
	switch a.currentScreen {
	case screenDashboard:
		return a.dashboard.View()
	default:
		return "Not implemented yet"
	}
}

func (a App) Model() tea.Model {
	return a
}
