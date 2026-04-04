package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/internal/scriptgen"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// scanCmd represents the scan command
var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scans the system for junk files and other unnecessary data without entering the TUI",
	Long: `Scans the system for junk files and other unnecessary data.
This command helps users identify files that can be safely removed to free up disk space.

Example usage:
# Scan the system for junk files (interactive table)
$ tidymymac scan

# Output results as JSON to stdout
$ tidymymac scan --output json

# Output as CSV and save to a file in the current directory
$ tidymymac scan --output csv --save

# Include individual file paths in the output
$ tidymymac scan --output json --detailed

# Scan specific categories
$ tidymymac scan docker caches
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")
		detailed, _ := cmd.Flags().GetBool("detailed")
		save, _ := cmd.Flags().GetBool("save")
		quiet, _ := cmd.Flags().GetBool("quiet")
		generateScript, _ := cmd.Flags().GetBool("generate-script")

		if output != "" && output != "json" && output != "csv" {
			return fmt.Errorf("invalid --output value %q: must be json or csv", output)
		}

		if output != "" {
			return runScanNonInteractive(cmd.Context(), args, output, detailed, save, quiet, generateScript)
		}

		return runScanInteractive(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)
	scanCmd.Flags().StringP("output", "o", "", "output format: json or csv (omit for interactive table)")
	scanCmd.Flags().Bool("detailed", false, "include individual file paths in json/csv output")
	scanCmd.Flags().Bool("save", false, "save output to a file in the current directory instead of stdout")
	scanCmd.Flags().Bool("quiet", false, "suppress progress output to stderr (only applies with --output)")
	scanCmd.Flags().Bool("generate-script", false, "generate a cleanup script based on the scan results")
}

// runScanNonInteractive runs the scan, using BubbleTea when --save is set (and
// --quiet is absent), otherwise printing progress to stderr.
func runScanNonInteractive(ctx context.Context, args []string, format string, detailed bool, save bool, quiet bool, generateScript bool) error {
	if save && !quiet {
		m := newScanModel(ctx, args, generateScript, true, format, detailed)
		p := tea.NewProgram(m)

		final, err := p.Run()
		if err != nil {
			return err
		}

		finalModel, ok := final.(scanModel)
		if !ok {
			return nil
		}

		if finalModel.err != nil {
			return finalModel.err
		}

		if generateScript && finalModel.result != nil {
			scriptInput := scanResultToCleanerResults(*finalModel.result)
			scriptPath, genErr := scriptgen.Generate(scriptInput, cleaner.DefaultRegistry())
			if genErr != nil {
				return fmt.Errorf("generating cleanup script: %w", genErr)
			}
			fmt.Println(scanHelpStyle.Render("  cleanup script generated: " + filepath.Base(scriptPath)))
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

		return nil
	}

	stderr := func(format string, a ...any) {
		if !quiet {
			fmt.Fprintf(os.Stderr, format, a...)
		}
	}

	stderr("🔎 scanning your mac...\n")

	result, err := commands.RunScan(
		ctx,
		cleaner.DefaultRegistry(),
		args,
		commands.ScanOptions{Detailed: detailed || generateScript},
		func(event commands.ScanEvent) {
			switch event.Type {
			case commands.ScanEventStarted:
				stderr("  · %s\n", event.Name)
			case commands.ScanEventDone:
				if event.Err != nil {
					stderr("  ✗ %s\n", event.Name)
				} else {
					stderr("  ✓ %s\n", event.Name)
				}
			}
		},
	)
	if err != nil {
		return err
	}

	out := os.Stdout
	if save {
		filename := fmt.Sprintf("tidymymac-scan-%s.%s", time.Now().Format("2006-01-02"), format)
		f, createErr := os.Create(filename)
		if createErr != nil {
			return fmt.Errorf("creating output file: %w", createErr)
		}
		defer f.Close()
		out = f
		stderr("\n  saved to ./%s\n", filename)
	}

	if generateScript {
		scriptInput := scanResultToCleanerResults(result)
		scriptPath, genErr := scriptgen.Generate(scriptInput, cleaner.DefaultRegistry())
		if genErr != nil {
			return fmt.Errorf("generating cleanup script: %w", genErr)
		}
		stderr("\n  cleanup script generated: %s\n", scriptPath)
	}

	if writeErr := commands.WriteOutput(out, result, format, detailed); writeErr != nil {
		return writeErr
	}

	if result.HasErrors {
		var failed []string
		for _, cat := range result.Categories {
			if cat.Err != nil {
				failed = append(failed, cat.Name)
			}
		}
		return fmt.Errorf("scan completed with errors in: %s", strings.Join(failed, ", "))
	}

	return nil
}

// scanResultToCleanerResults converts command-layer scan output into the format
// expected by the script generator.
func scanResultToCleanerResults(result commands.ScanResult) map[cleaner.Category]*cleaner.ScanResult {
	converted := make(map[cleaner.Category]*cleaner.ScanResult, len(result.Categories))
	for _, cat := range result.Categories {
		if cat.Err != nil || cat.TotalFiles == 0 {
			continue
		}
		converted[cat.Category] = &cleaner.ScanResult{
			Category:   cat.Category,
			Entries:    cat.Files,
			TotalSize:  cat.TotalSize,
			TotalFiles: cat.TotalFiles,
		}
	}
	return converted
}

// runScanInteractive runs the scan using the BubbleTea model (default mode).
func runScanInteractive(cmd *cobra.Command, args []string) error {
	generateScript, _ := cmd.Flags().GetBool("generate-script")

	m := newScanModel(cmd.Context(), args, generateScript, false, "", false)
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

	if finalModel.err == nil && finalModel.result != nil {
		if generateScript {
			scriptInput := scanResultToCleanerResults(*finalModel.result)
			scriptPath, genErr := scriptgen.Generate(scriptInput, cleaner.DefaultRegistry())
			if genErr != nil {
				return fmt.Errorf("generating cleanup script: %w", genErr)
			}
			fmt.Println(scanHelpStyle.Render(" Cleanup script generated: " + filepath.Base(scriptPath)))
		}
		fmt.Println(scanHelpStyle.Render("  Run 'tidymymac clean' to remove these files | Run 'tidymymac clean <category>' to remove specific categories"))
	}

	return finalModel.err
}

var (
	scanTitleStyle = lipgloss.NewStyle().
			Bold(false).
			Foreground(lipgloss.Color("#717171")).
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
	result  commands.ScanResult
	err     error
	savedTo string
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
	ctx            context.Context
	args           []string
	spinner        spinner.Model
	result         *commands.ScanResult
	err            error
	scanning       bool
	categories     []scanCategoryProgress
	eventCh        chan commands.ScanEvent
	generateScript bool
	save           bool
	format         string
	detailed       bool
	savedTo        string
}

func newScanModel(ctx context.Context, args []string, generateScript bool, save bool, format string, detailed bool) scanModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))

	return scanModel{
		ctx:            ctx,
		args:           args,
		spinner:        s,
		scanning:       true,
		eventCh:        make(chan commands.ScanEvent, 50),
		generateScript: generateScript,
		save:           save,
		format:         format,
		detailed:       detailed,
	}
}

func (m scanModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			result, err := commands.RunScan(m.ctx, cleaner.DefaultRegistry(), m.args, commands.ScanOptions{Detailed: m.detailed || m.generateScript}, func(event commands.ScanEvent) {
				m.eventCh <- event
			})
			close(m.eventCh)

			if err != nil {
				return scanDoneMsg{result: result, err: err}
			}

			if m.save {
				filename := fmt.Sprintf("tidymymac-scan-%s.%s", time.Now().Format("2006-01-02"), m.format)
				f, createErr := os.Create(filename)
				if createErr != nil {
					if errors.Is(createErr, os.ErrPermission) {
						return scanDoneMsg{result: result, err: fmt.Errorf("permission denied: ./%s", filename)}
					}
					return scanDoneMsg{result: result, err: createErr}
				}
				writeErr := commands.WriteOutput(f, result, m.format, m.detailed)
				_ = f.Close()
				if writeErr != nil {
					return scanDoneMsg{result: result, err: writeErr}
				}
				return scanDoneMsg{result: result, savedTo: filename}
			}

			return scanDoneMsg{result: result}
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
		m.savedTo = msg.savedTo
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
		b.WriteString(scanErrorStyle.Render(fmt.Sprintf("  ✗ %v", m.err)))
		return b.String()
	}

	if m.savedTo != "" {
		b.WriteString(scanDoneStyle.Render(fmt.Sprintf("  saved to ./%s", m.savedTo)))
		b.WriteString("\n")
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

	return b.String()
}
