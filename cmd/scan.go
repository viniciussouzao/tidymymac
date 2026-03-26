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

		if finalModel, ok := final.(scanModel); ok && finalModel.err != nil {
			return finalModel.err
		}
		return nil
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
			Foreground(lipgloss.Color("#555555"))

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

type scanModel struct {
	ctx      context.Context
	args     []string
	spinner  spinner.Model
	result   *commands.ScanResult
	err      error
	scanning bool
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
	}
}

func (m scanModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			result, err := commands.RunScan(m.ctx, cleaner.DefaultRegistry(), m.args)
			return scanDoneMsg{result: result, err: err}
		},
	)
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

func (m scanModel) View() string {
	var b strings.Builder

	b.WriteString(scanTitleStyle.Render("🔎 scanning your mac..."))
	b.WriteString("\n")

	if m.scanning {
		b.WriteString(fmt.Sprintf(" %s %s", m.spinner.View(), scanDimStyle.Render("looking for things that you may not need anymore...")))
		b.WriteString("\n\n")
		b.WriteString(scanHelpStyle.Render(" q to quit"))
		return b.String()
	}

	if m.err != nil {
		b.WriteString(scanErrorStyle.Render(fmt.Sprintf("❌ error scanning: %v", m.err)))
		return b.String()
	}

	for _, cat := range m.result.Categories {
		var icon, sizeText string

		if cat.Err != nil {
			icon = scanErrorStyle.Render("✗")
			sizeText = scanErrorStyle.Render("error: " + cat.Err.Error())
		} else {
			icon = scanDoneStyle.Render("✓")
			formatted := utils.FormatBytes(cat.TotalSize)
			sizeText = styledSize(cat.TotalSize, formatted)
			if cat.TotalFiles > 0 {
				sizeText += scanDimStyle.Render(fmt.Sprintf(" (%d files)", cat.TotalFiles))
			}
		}

		b.WriteString(fmt.Sprintf("  %s %-22s %s\n", icon, cat.Name, sizeText))
	}

	b.WriteString("\n")
	b.WriteString(scanDimStyle.Render("  " + strings.Repeat("─", 42)))
	b.WriteString("\n")

	totalFormatted := utils.FormatBytes(m.result.TotalSize)
	totalLine := fmt.Sprintf(
		"  %-24s %s",
		lipgloss.NewStyle().Bold(true).Render("Total"),
		styledSize(m.result.TotalSize, totalFormatted)+
			scanDimStyle.Render(fmt.Sprintf(" (%d files)", m.result.TotalFiles)),
	)
	b.WriteString(totalLine)
	b.WriteString("\n")

	b.WriteString(scanHelpStyle.Render("\n  Run 'tidymymac clean' to remove junk files."))

	return b.String()
}
