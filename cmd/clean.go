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

	"github.com/spf13/cobra"
	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "delete the junk files without opening the TUI",
	Long: `Cleans junk files without opening the TUI.
By default this command runs in dry-run mode and only simulates the cleanup.
Pass --execute to actually delete files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		detailed, _ := cmd.Flags().GetBool("detailed")
		fromFile, _ := cmd.Flags().GetString("from-file")
		forceStaleScan, _ := cmd.Flags().GetBool("force-stale-scan")
		output, _ := cmd.Flags().GetString("output")
		quiet, _ := cmd.Flags().GetBool("quiet")

		if output != "" && output != "json" {
			return fmt.Errorf("invalid --output value %q: must be json", output)
		}

		return runCleanNonInteractive(cmd.Context(), args, detailed, fromFile, forceStaleScan, output, quiet)
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

	if fromFile != "" {
		scanResult, loadErr := loadScanResultFile(fromFile)
		if loadErr != nil {
			return loadErr
		}

		age := time.Since(scanResult.ScannedAt)
		if !scanResult.ScannedAt.IsZero() && age > cleanScanWarnAge {
			stderr("warning: scan file is %s old; entries will be revalidated before cleaning\n", roundAge(age))
		}
		if !scanResult.ScannedAt.IsZero() && age > cleanScanMaxAge && !dryRun && !forceStaleScan {
			return fmt.Errorf("scan file is %s old; rerun the scan or use --force-stale-scan with --execute", roundAge(age))
		}

		prepared, prepErr := commands.PrepareScanResultForClean(registry, scanResult, args)
		if prepErr != nil {
			return prepErr
		}
		revalidation = &commands.RevalidationSummary{
			RevalidatedFiles: prepared.RevalidatedFiles,
			MissingFiles:     prepared.MissingFiles,
			TypeChangedFiles: prepared.TypeChangedFiles,
			EmptyCategories:  prepared.EmptyCategories,
		}
		printRevalidationSummary(stderr, revalidation)

		result, err = commands.RunCleanWithPreparedScanResult(
			ctx,
			registry,
			prepared,
			args,
			opts,
			cleanProgressPrinter(stderr),
		)
	} else {
		result, err = commands.RunClean(
			ctx,
			registry,
			args,
			opts,
			cleanProgressPrinter(stderr),
		)
	}
	if err != nil {
		return err
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

func printRevalidationSummary(stderr func(string, ...any), summary *commands.RevalidationSummary) {
	if summary == nil {
		return
	}
	stderr("  revalidated %d files", summary.RevalidatedFiles)
	if summary.MissingFiles > 0 || summary.TypeChangedFiles > 0 || summary.EmptyCategories > 0 {
		stderr(" (%d missing, %d type-changed, %d empty categories)", summary.MissingFiles, summary.TypeChangedFiles, summary.EmptyCategories)
	}
	stderr("\n")
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

func actionSize(size int64) string {
	return utils.FormatBytes(size)
}
