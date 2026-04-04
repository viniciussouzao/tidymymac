package cmd

import (
	"context"
	"encoding/json"
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
	"github.com/viniciussouzao/tidymymac/internal/history"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "delete the junk files without opening the TUI",
	Long: `Cleans junk files without opening the TUI.
By default this command runs in dry-run mode and only simulates the cleanup.
Pass --execute to actually delete files.

Example usage:
# Preview what would be cleaned across all categories
$ tidymymac clean

# Actually delete files
$ tidymymac clean --execute

# Clean only specific categories
$ tidymymac clean docker caches --execute

# Use a previous detailed JSON scan and revalidate entries before cleaning
$ tidymymac clean --from-file scan.json

# Output the cleanup result as JSON
$ tidymymac clean --output json

# Use a previous scan file and output the cleanup result as JSON
$ tidymymac clean --from-file scan.json --output json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		detailed, _ := cmd.Flags().GetBool("detailed")
		fromFile, _ := cmd.Flags().GetString("from-file")
		forceStaleScan, _ := cmd.Flags().GetBool("force-stale-scan")
		output, _ := cmd.Flags().GetString("output")
		quiet, _ := cmd.Flags().GetBool("quiet")

		if output != "" && output != "json" {
			return fmt.Errorf("invalid --output value %q: must be json", output)
		}

		if output != "" {
			return runCleanNonInteractive(cmd.Context(), args, detailed, fromFile, forceStaleScan, output, quiet)
		}

		return runCleanInteractive(cmd, args, detailed, fromFile, forceStaleScan)
	},
}

func init() {
	rootCmd.AddCommand(cleanCmd)
	cleanCmd.Flags().StringP("output", "o", "", "output format: json")
	cleanCmd.Flags().Bool("detailed", false, "include individual file paths in the cleanup result")
	cleanCmd.Flags().String("from-file", "", "load a previous JSON scan result and revalidate its file list before cleaning")
	cleanCmd.Flags().Bool("force-stale-scan", false, "allow --from-file scan results older than 24 hours when used with --execute")
	cleanCmd.Flags().Bool("quiet", false, "suppress progress output to stderr")
}

const (
	cleanScanWarnAge = time.Hour
	cleanScanMaxAge  = 24 * time.Hour
)

func runCleanNonInteractive(ctx context.Context, args []string, detailed bool, fromFile string, forceStaleScan bool, output string, quiet bool) error {
	start := time.Now()
	stderr := func(format string, a ...any) {
		if !quiet {
			fmt.Fprintf(os.Stderr, format, a...)
		}
	}

	dryRun := !executeFlag
	if dryRun {
		stderr("🧪 dry-run mode: no files will be deleted. Use --execute to actually clean.\n")
	} else {
		stderr("🧹 cleaning your mac...\n")
	}

	registry := cleaner.DefaultRegistry()
	opts := commands.CleanerOptions{
		Detailed: detailed,
		DryRun:   dryRun,
	}

	var (
		result       commands.CleanResult
		err          error
		revalidation *commands.RevalidationSummary
	)

	result, revalidation, err = executeClean(ctx, registry, args, fromFile, forceStaleScan, opts, cleanProgressPrinter(stderr), stderr)
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
			var failed []string
			for _, cat := range result.Categories {
				if cat.Err != nil {
					failed = append(failed, cat.Name)
				}
			}
			return fmt.Errorf("clean completed with errors in: %s", strings.Join(failed, ", "))
		}
		return nil
	}

	actionSummary := "Would reclaim"
	actionVerb := "would clean"
	if !dryRun {
		actionSummary = "Reclaimed"
		actionVerb = "cleaned"
	}

	fmt.Printf("%s %s across %d files.\n", actionSummary, result.TotalSizeHuman, result.TotalFiles)
	for _, category := range result.Categories {
		if category.Err != nil {
			fmt.Printf("- %s: error: %s\n", category.Name, category.ErrMsg)
			continue
		}

		fmt.Printf("- %s: %s, %d files %s\n", category.Name, actionSize(category.DeletedSize), category.DeletedFiles, actionVerb)
		if detailed {
			for _, file := range category.Files {
				fmt.Printf("  %s\n", file.Path)
			}
		}
	}

	if result.HasErrors {
		var failed []string
		for _, cat := range result.Categories {
			if cat.Err != nil {
				failed = append(failed, cat.Name)
			}
		}
		return fmt.Errorf("clean completed with errors in: %s", strings.Join(failed, ", "))
	}

	return nil
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

		prepared, prepErr := commands.PrepareScanResultForClean(registry, scanResult, args)
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
	defer f.Close()

	result, err := commands.LoadScanResult(f)
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

func runCleanInteractive(cmd *cobra.Command, args []string, detailed bool, fromFile string, forceStaleScan bool) error {
	m := newCleanModel(cmd.Context(), args, detailed, fromFile, forceStaleScan, !executeFlag)
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
		fmt.Println(scanHelpStyle.Render("  Run 'tidymymac clean --execute' to actually delete these files"))
	}

	return finalModel.err
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
	ctx            context.Context
	args           []string
	detailed       bool
	fromFile       string
	forceStaleScan bool
	dryRun         bool
	spinner        spinner.Model
	result         *commands.CleanResult
	revalidation   *commands.RevalidationSummary
	err            error
	cleaning       bool
	categories     []cleanCategoryProgress
	eventCh        chan commands.CleanEvent
}

func newCleanModel(ctx context.Context, args []string, detailed bool, fromFile string, forceStaleScan bool, dryRun bool) cleanModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))

	return cleanModel{
		ctx:            ctx,
		args:           args,
		detailed:       detailed,
		fromFile:       fromFile,
		forceStaleScan: forceStaleScan,
		dryRun:         dryRun,
		spinner:        s,
		cleaning:       true,
		eventCh:        make(chan commands.CleanEvent, 50),
	}
}

func (m cleanModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			start := time.Now()
			result, revalidation, err := executeClean(
				m.ctx,
				cleaner.DefaultRegistry(),
				m.args,
				m.fromFile,
				m.forceStaleScan,
				commands.CleanerOptions{
					Detailed: m.detailed,
					DryRun:   m.dryRun,
				},
				func(event commands.CleanEvent) {
					m.eventCh <- event
				},
				func(string, ...any) {},
			)

			close(m.eventCh)
			if err != nil {
				return cleanDoneMsg{
					result:       result,
					revalidation: revalidation,
					err:          err,
				}
			}

			if !m.dryRun {
				_ = history.Append(buildRunRecord(result, time.Since(start).Milliseconds()))
			}

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
		if m.err == nil {
			m.result = &msg.result
		}
		return m, tea.Quit
	}

	return m, nil
}

func (m cleanModel) View() string {
	var b strings.Builder

	title := "🧹 cleaning your mac..."
	statusText := "removing files you selected for cleanup..."
	modeBanner := scanDoneStyle.Render("  EXECUTE MODE - Files are being deleted.")
	helpText := " q to quit"
	if m.dryRun {
		title = "🧪 dry-run cleanup..."
		statusText = "simulating cleanup without deleting files..."
		modeBanner = scanHelpStyle.Render("  DRY RUN MODE - No files will be deleted. Run with --execute to actually clean.")
		helpText = " q to quit | rerun with --execute to actually delete files"
	}

	b.WriteString(scanTitleStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(modeBanner)
	b.WriteString("\n")

	if m.cleaning {
		b.WriteString(fmt.Sprintf(" %s %s", m.spinner.View(), scanDimStyle.Render(statusText)))
		b.WriteString("\n")
		if m.fromFile != "" {
			b.WriteString("\n")
			b.WriteString(scanDimStyle.Render("  using entries revalidated from the provided scan file"))
			b.WriteString("\n")
		}
		b.WriteString("\n")
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
		b.WriteString(scanHelpStyle.Render(helpText))
		return b.String()
	}

	if m.err != nil {
		b.WriteString(scanErrorStyle.Render(fmt.Sprintf("  ✗ error cleaning: %v", m.err)))
		return b.String()
	}

	if m.revalidation != nil {
		b.WriteString("\n")
		b.WriteString(scanDimStyle.Render(fmt.Sprintf(
			"  Revalidated %d files (%d missing, %d type-changed, %d empty categories)",
			m.revalidation.RevalidatedFiles,
			m.revalidation.MissingFiles,
			m.revalidation.TypeChangedFiles,
			m.revalidation.EmptyCategories,
		)))
		b.WriteString("\n")
	}

	boldStyle := lipgloss.NewStyle().Bold(true)
	sep := scanDimStyle.Render("  " + strings.Repeat("─", tableWidth))

	sizeLabel := "Reclaimed"
	if m.dryRun {
		sizeLabel = "Would Free"
	}

	b.WriteString(fmt.Sprintf("\n  %s  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("%-*s", colCategory, "Category")),
		boldStyle.Render(fmt.Sprintf("%*s", colFiles, "Files")),
		boldStyle.Render(fmt.Sprintf("%*s", colSize, sizeLabel)),
	))
	b.WriteString(sep)
	b.WriteString("\n")

	for _, cat := range m.result.Categories {
		var filesText, sizeText string

		if cat.Err != nil {
			filesText = scanErrorStyle.Render(fmt.Sprintf("%*s", colFiles, "─"))
			sizeText = scanErrorStyle.Render(fmt.Sprintf("%*s", colSize, "error"))
		} else {
			filesText = scanDimStyle.Render(fmt.Sprintf("%*d", colFiles, cat.DeletedFiles))
			sizeText = styledSize(cat.DeletedSize, fmt.Sprintf("%*s", colSize, utils.FormatBytes(cat.DeletedSize)))
		}

		b.WriteString(fmt.Sprintf("  %-*s  %s  %s\n",
			colCategory, cat.Name,
			filesText,
			sizeText,
		))
	}

	b.WriteString(sep)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s  %s  %s\n",
		boldStyle.Render(fmt.Sprintf("%-*s", colCategory, "Total")),
		scanDimStyle.Render(fmt.Sprintf("%*d", colFiles, m.result.TotalFiles)),
		styledSize(m.result.TotalSize, fmt.Sprintf("%*s", colSize, utils.FormatBytes(m.result.TotalSize))),
	))
	b.WriteString("\n")
	if m.dryRun {
		b.WriteString(scanHelpStyle.Render("  Preview only. Run 'tidymymac clean --execute' to actually delete these files."))
	} else {
		b.WriteString(scanHelpStyle.Render("  Cleanup finished. You can run 'tidymymac history' to inspect previous cleanup sessions."))
	}

	return b.String()
}

func actionSize(size int64) string {
	return utils.FormatBytes(size)
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
