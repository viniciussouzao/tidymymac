package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// scanCmd represents the scan command
var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scans the system for junk files and other unnecessary data without entering the TUI",
	Long: `Scans the system for junk files and other unnecessary data.
This command helps users identify files that can be safely removed to free up disk space.

Example usage:
# Scan the system for junk files
$ tidymymac scan

# Scan the system data and provide a summary of the findings
$ tidymymac scan system-data

# Scan multiple categories at once (e.g., caches and Docker-related files)
$ tidymymac scan docker caches
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		m := newScanModel(cmd.Context(), args)
		p := tea.NewProgram(m)

		final, err := p.Run()
		if err != nil {
			return err
		}

		finalModel, ok := final.(scanModel)
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
			return fmt.Errorf("scan completed with errors in: %s", strings.Join(failed, ", "))
		}

		return finalModel.err
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)

}

var (
	scanTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ffb56b")).
			MarginBottom(1)

	scanDoneStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981"))

	scanErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444"))

	scanDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#717171"))

	scanHelpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginTop(1)

	scanSizeSmall = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	scanSizeMid   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))
	scanSizeLarge = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true)
)

func styledSize(size int64, text string) string {
	const gb = 1 << 30
	const mb100 = 100 << 20
	switch {
	case size >= gb:
		return scanSizeLarge.Render(text)
	case size >= mb100:
		return scanSizeMid.Render(text)
	default:
		return scanSizeSmall.Render(text)
	}
}

type scanDoneMsg struct {
	result commands.ScanResult
	err    error
}

type scanEventMsg struct {
	event  commands.ScanEvent
	closed bool
}

type scanCategoryProgress struct {
	name string
	done bool
	err  bool
}

type scanModel struct {
	ctx        context.Context
	args       []string
	spinner    spinner.Model
	result     *commands.ScanResult
	err        error
	scanning   bool
	categories []scanCategoryProgress
	eventCh    chan commands.ScanEvent
}

func newScanModel(ctx context.Context, args []string) scanModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))

	return scanModel{
		ctx:      ctx,
		args:     args,
		spinner:  s,
		scanning: true,
		eventCh:  make(chan commands.ScanEvent, 50),
	}
}

func (m scanModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			result, err := commands.RunScan(m.ctx, cleaner.DefaultRegistry(), m.args, func(event commands.ScanEvent) {
				m.eventCh <- event
			})
			close(m.eventCh)
			return scanDoneMsg{result: result, err: err}
		},
		m.listenEvents(),
	)
}

func (m scanModel) listenEvents() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventCh
		if !ok {
			return scanEventMsg{closed: true}
		}
		return scanEventMsg{event: event}
	}
}

func (m scanModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case scanEventMsg:
		if msg.closed {
			return m, nil
		}
		switch msg.event.Type {
		case commands.ScanEventStarted:
			m.categories = append(m.categories, scanCategoryProgress{name: msg.event.Name})
		case commands.ScanEventDone:
			for i, cat := range m.categories {
				if cat.name == msg.event.Name {
					m.categories[i].done = true
					m.categories[i].err = msg.event.Err != nil
					break
				}
			}
		}
		return m, m.listenEvents()
	case scanDoneMsg:
		m.scanning = false
		m.err = msg.err
		if m.err == nil {
			m.result = &msg.result
		}
		return m, tea.Quit
	}

	return m, nil
}

const (
	colCategory = 24
	colFiles    = 8
	colSize     = 12
	tableWidth  = colCategory + colFiles + colSize + 6
)

func (m scanModel) View() string {
	var b strings.Builder

	b.WriteString(scanTitleStyle.Render("🔎 scanning your mac..."))
	b.WriteString("\n")

	if m.scanning {
		b.WriteString(fmt.Sprintf(" %s %s", m.spinner.View(), scanDimStyle.Render("looking for things that you may not need anymore...")))
		b.WriteString("\n\n")
		for _, cat := range m.categories {
			if cat.err {
				b.WriteString(fmt.Sprintf("  %s %s\n", scanErrorStyle.Render("✗"), scanDimStyle.Render(cat.name)))
			} else if cat.done {
				b.WriteString(fmt.Sprintf("  %s %s\n", scanDoneStyle.Render("✓"), scanDimStyle.Render(cat.name)))
			} else {
				b.WriteString(fmt.Sprintf("  %s %s\n", scanDimStyle.Render("·"), scanDimStyle.Render(cat.name)))
			}
		}
		b.WriteString("\n")
		b.WriteString(scanHelpStyle.Render(" q to quit"))
		return b.String()
	}

	if m.err != nil {
		b.WriteString(scanErrorStyle.Render(fmt.Sprintf("  ✗ error scanning: %v", m.err)))
		return b.String()
	}

	boldStyle := lipgloss.NewStyle().Bold(true)
	sep := scanDimStyle.Render("  " + strings.Repeat("─", tableWidth))

	// header — pad plain text first, then apply style
	b.WriteString(fmt.Sprintf("\n  %s  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("%-*s", colCategory, "Category")),
		boldStyle.Render(fmt.Sprintf("%*s", colFiles, "Files")),
		boldStyle.Render(fmt.Sprintf("%*s", colSize, "Freeable")),
	))
	b.WriteString(sep)
	b.WriteString("\n")

	// rows — pad plain text first, then apply style
	for _, cat := range m.result.Categories {
		var filesText, sizeText string

		if cat.Err != nil {
			filesText = scanErrorStyle.Render(fmt.Sprintf("%*s", colFiles, "─"))
			sizeText = scanErrorStyle.Render(fmt.Sprintf("%*s", colSize, "error"))
		} else {
			filesText = scanDimStyle.Render(fmt.Sprintf("%*d", colFiles, cat.TotalFiles))
			sizeText = styledSize(cat.TotalSize, fmt.Sprintf("%*s", colSize, utils.FormatBytes(cat.TotalSize)))
		}

		b.WriteString(fmt.Sprintf("  %-*s  %s  %s\n",
			colCategory, cat.Name,
			filesText,
			sizeText,
		))
	}

	// total
	b.WriteString(sep)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("%-*s", colCategory, "Total")),
		scanDimStyle.Render(fmt.Sprintf("%*d", colFiles, m.result.TotalFiles)),
		styledSize(m.result.TotalSize, fmt.Sprintf("%*s", colSize, utils.FormatBytes(m.result.TotalSize))),
	))

	b.WriteString(scanHelpStyle.Render("\n  Run 'tidymymac clean' to remove these files | Run 'tidymymac clean <category>' to remove specific categories"))

	return b.String()
}
