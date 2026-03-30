package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// PreparedScanResult is a wrapper around ScanResult that includes additional metadata about the revalidation process.
type PreparedScanResult struct {
	Result           ScanResult
	RevalidatedFiles int
	MissingFiles     int
	TypeChangedFiles int
	EmptyCategories  int
}

// LoadScanResult reads a JSON-encoded ScanResult from the provided reader and decodes it into a ScanResult struct.
func LoadScanResult(r io.Reader) (ScanResult, error) {
	var result ScanResult
	if err := json.NewDecoder(r).Decode(&result); err != nil {
		return ScanResult{}, fmt.Errorf("decode scan result: %w", err)
	}
	return result, nil
}

// PrepareScanResultForClean takes a ScanResult and prepares it for the cleaning process by revalidating file entries and categorizing them according to the provided registry and selected categories.
// It returns a PreparedScanResult that includes the original ScanResult along with metadata about the revalidation process, such as the number of revalidated files, missing files, type-changed files, and empty categories.
// If any errors occur during preparation, they are returned as well.
func PrepareScanResultForClean(registry *cleaner.Registry, scan ScanResult, selected []string) (PreparedScanResult, error) {
	cleaners, err := resolveCleaners(registry, selected)
	if err != nil {
		return PreparedScanResult{}, err
	}

	categories := make(map[cleaner.Category]ScanCategoryResult, len(scan.Categories))
	for _, category := range scan.Categories {
		categories[category.Category] = category
	}

	prepared := PreparedScanResult{
		Result: ScanResult{
			ScannedAt:  scan.ScannedAt,
			Categories: make([]ScanCategoryResult, 0, len(cleaners)),
		},
	}

	for _, c := range cleaners {
		category := categories[c.Category()]
		item := ScanCategoryResult{
			Category: c.Category(),
			Name:     c.Category().DisplayName(),
			ErrMsg:   category.ErrMsg,
		}
		if category.Name != "" {
			item.Name = category.Name
		}
		if category.ErrMsg != "" {
			item.Err = fmt.Errorf("%s", category.ErrMsg)
			prepared.Result.HasErrors = true
			prepared.Result.Categories = append(prepared.Result.Categories, item)
			continue
		}
		if category.TotalFiles > 0 && len(category.Files) == 0 {
			item.Err = fmt.Errorf("scan file for %s does not include file entries; rerun with --output json --detailed", item.Name)
			item.ErrMsg = item.Err.Error()
			prepared.Result.HasErrors = true
			prepared.Result.Categories = append(prepared.Result.Categories, item)
			continue
		}

		revalidated, missing, typeChanged := revalidateEntries(category.Files)
		for i := range revalidated {
			revalidated[i].Category = item.Category
		}
		prepared.RevalidatedFiles += len(revalidated)
		prepared.MissingFiles += missing
		prepared.TypeChangedFiles += typeChanged

		item.Files = revalidated
		item.TotalFiles = len(revalidated)
		for _, file := range revalidated {
			item.TotalSize += file.Size
		}
		item.TotalSizeHuman = utils.FormatBytes(item.TotalSize)
		if len(revalidated) == 0 {
			prepared.EmptyCategories++
		}

		prepared.Result.TotalFiles += item.TotalFiles
		prepared.Result.TotalSize += item.TotalSize
		prepared.Result.Categories = append(prepared.Result.Categories, item)
	}

	prepared.Result.TotalSizeHuman = utils.FormatBytes(prepared.Result.TotalSize)
	return prepared, nil
}

func revalidateEntries(entries []cleaner.FileEntry) ([]cleaner.FileEntry, int, int) {
	revalidated := make([]cleaner.FileEntry, 0, len(entries))
	var missing int
	var typeChanged int

	for _, entry := range entries {
		info, err := os.Stat(entry.Path)
		if err != nil {
			if os.IsNotExist(err) {
				missing++
				continue
			}
			missing++
			continue
		}
		if info.IsDir() != entry.IsDir {
			typeChanged++
			continue
		}

		revalidated = append(revalidated, cleaner.FileEntry{
			Path:     entry.Path,
			Size:     info.Size(),
			IsDir:    info.IsDir(),
			ModTime:  info.ModTime().UTC(),
			Category: entry.Category,
		})
	}

	return revalidated, missing, typeChanged
}

// RunCleanWithScanResult takes a ScanResult, prepares it for cleaning, and then executes the cleaning process while emitting events through the provided onEvent callback.
// It returns a CleanResult summarizing the outcome of the cleaning operation or an error if any step fails.
func RunCleanWithScanResult(ctx context.Context, registry *cleaner.Registry, scan ScanResult, selected []string, opts CleanerOptions, onEvent func(CleanEvent)) (CleanResult, error) {
	prepared, err := PrepareScanResultForClean(registry, scan, selected)
	if err != nil {
		return CleanResult{}, err
	}

	return RunCleanWithPreparedScanResult(ctx, registry, prepared, selected, opts, onEvent)
}

func RunCleanWithPreparedScanResult(ctx context.Context, registry *cleaner.Registry, prepared PreparedScanResult, selected []string, opts CleanerOptions, onEvent func(CleanEvent)) (CleanResult, error) {
	return runClean(ctx, registry, selected, opts, prepared.Result, true, onEvent)
}
