package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// CleanerOptions defines options for the cleaning process.
type CleanerOptions struct {
	Detailed bool
	DryRun   bool
	Config   *config.Config
}

// CleanCategoryResult represents the result of cleaning a specific category.
type CleanCategoryResult struct {
	Category     cleaner.Category    `json:"category"`
	Name         string              `json:"name"`
	DeletedFiles int                 `json:"deleted_files"`
	DeletedSize  int64               `json:"deleted_size_bytes"`
	Files        []cleaner.FileEntry `json:"files,omitempty"`
	Err          error               `json:"-"`
	ErrMsg       string              `json:"error,omitempty"`
	// PartialErrors counts non-fatal per-file errors collected by the cleaner
	// while it still reclaimed some space. Kept out of the JSON output to
	// preserve the machine-readable schema.
	PartialErrors int `json:"-"`
}

// CleanResult represents the overall result of the cleaning process.
type CleanResult struct {
	CleanedAt      time.Time             `json:"cleaned_at"`
	TotalFiles     int                   `json:"total_files"`
	TotalSize      int64                 `json:"total_size_bytes"`
	TotalSizeHuman string                `json:"total_size_human"`
	HasErrors      bool                  `json:"has_errors"`
	Categories     []CleanCategoryResult `json:"categories"`
}

// RevalidationSummary provides a summary of the revalidation process during cleaning.
type RevalidationSummary struct {
	RevalidatedFiles int `json:"revalidated_files"`
	MissingFiles     int `json:"missing_files"`
	TypeChangedFiles int `json:"type_changed_files"`
	EmptyCategories  int `json:"empty_categories"`
}

// CleanOutput represents the output of the cleaning process, including results and optional revalidation summary.
type CleanOutput struct {
	Result       CleanResult          `json:"result"`
	Revalidation *RevalidationSummary `json:"revalidation,omitempty"`
}

// CleanEventType defines the type of events emitted during the cleaning process.
type CleanEventType string

const (
	CleanEventStarted  CleanEventType = "started"
	CleanEventProgress CleanEventType = "progress"
	CleanEventDone     CleanEventType = "done"
)

// CleanEvent represents an event emitted during the cleaning process, including its type, category, progress, and any associated errors.
type CleanEvent struct {
	Type     CleanEventType
	Category cleaner.Category
	Name     string
	Progress cleaner.CleanProgress
	Result   *CleanCategoryResult
	Err      error
}

// RunClean executes the cleaning process for the selected categories and returns the results.
func RunClean(ctx context.Context, registry *cleaner.Registry, selected []string, opts CleanerOptions, onEvent func(CleanEvent)) (CleanResult, error) {
	return runClean(ctx, registry, selected, opts, ScanResult{}, false, onEvent)
}

// runClean is the internal implementation of the cleaning process, allowing for optional use of a prepared scan result.
func runClean(ctx context.Context, registry *cleaner.Registry, selected []string, opts CleanerOptions, preparedScan ScanResult, usePreparedScan bool, onEvent func(CleanEvent)) (CleanResult, error) {
	cleaners, err := resolveCleaners(registry, selected, opts.Config)
	if err != nil {
		return CleanResult{}, err
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		result = CleanResult{
			CleanedAt:  time.Now().UTC(),
			Categories: make([]CleanCategoryResult, 0, len(cleaners)),
		}
	)

	wg.Add(len(cleaners))

	for _, c := range cleaners {
		c := c

		go func() {
			defer wg.Done()

			name := c.Category().DisplayName()

			if onEvent != nil {
				onEvent(CleanEvent{
					Type:     CleanEventStarted,
					Category: c.Category(),
					Name:     name,
				})
			}

			scanResult, scanErr := buildCleanScanResult(c, preparedScan, usePreparedScan)
			if !usePreparedScan {
				scanResult, scanErr = c.Scan(ctx, nil)
			}

			if scanErr == nil && scanResult != nil {
				scanResult.Entries = opts.Config.Tag(scanResult.Entries)
				if protected := config.CountProtected(scanResult.Entries); protected > 0 && c.DeletesWholeDomain() {
					// This cleaner cannot honor a filtered entry list (it
					// shells out to a command that clears its entire
					// domain), so there's no way to run it without also
					// deleting protected paths. Skip the category entirely
					// rather than silently ignoring the protection.
					scanErr = fmt.Errorf("skipped: %d protected path(s) found in this category, and %s cannot selectively clean around them", protected, name)
				} else {
					scanResult.Entries = config.StripProtected(scanResult.Entries)
				}
			}

			// A whole-domain cleaner shells out to a command that clears its
			// entire domain regardless of which entries are passed in -- so
			// zero reviewed entries must never be read as "run it anyway".
			// That matters most exactly when entries can under-report reality:
			// a stale --from-file scan, a category the file never mentioned
			// (buildCleanScanResult then hands back an empty result rather
			// than an error), or every entry having been revalidated away.
			// Skipping is indistinguishable on screen from "genuinely already
			// clean" (both report 0 files, 0 bytes, no error), which is the
			// same trade-off the elevated helper makes for an empty
			// plan/scan intersection.
			skipEmptyWholeDomain := !opts.DryRun && c.DeletesWholeDomain() && scanResult != nil && len(scanResult.Entries) == 0

			var cleanRunResult *cleaner.CleanResult
			var cleanErr error
			if scanErr == nil && !skipEmptyWholeDomain {
				cleanRunResult, cleanErr = c.Clean(ctx, scanResult.Entries, opts.DryRun, func(progress cleaner.CleanProgress) {
					if onEvent != nil {
						onEvent(CleanEvent{
							Type:     CleanEventProgress,
							Category: c.Category(),
							Name:     name,
							Progress: progress,
						})
					}
				})
			}

			item := CleanCategoryResult{
				Category: c.Category(),
				Name:     name,
			}

			if scanResult != nil && opts.Detailed {
				item.Files = scanResult.Entries
			}

			if cleanRunResult != nil {
				item.DeletedFiles = cleanRunResult.FilesDeleted
				item.DeletedSize = cleanRunResult.BytesFreed
				item.PartialErrors = len(cleanRunResult.Errors)
			}

			if scanErr != nil {
				item.Err = scanErr
				item.ErrMsg = scanErr.Error()
			} else if cleanErr != nil {
				item.Err = cleanErr
				item.ErrMsg = cleanErr.Error()
			}

			mu.Lock()
			defer mu.Unlock()

			result.Categories = append(result.Categories, item)
			if item.Err != nil {
				result.HasErrors = true
			} else {
				result.TotalSize += item.DeletedSize
				result.TotalFiles += item.DeletedFiles
			}

			if onEvent != nil {
				itemCopy := item
				onEvent(CleanEvent{
					Type:     CleanEventDone,
					Category: c.Category(),
					Name:     name,
					Result:   &itemCopy,
					Err:      item.Err,
				})
			}
		}()
	}

	wg.Wait()

	// Order categories according to the registry
	resultMap := make(map[cleaner.Category]CleanCategoryResult)
	for _, cat := range result.Categories {
		resultMap[cat.Category] = cat
	}

	ordered := make([]CleanCategoryResult, 0, len(cleaners))
	for _, c := range cleaners {
		if r, ok := resultMap[c.Category()]; ok {
			ordered = append(ordered, r)
		}
	}

	result.Categories = ordered
	result.TotalSizeHuman = utils.FormatBytes(result.TotalSize)

	return result, nil
}

// ApprovedCategory is one category's approved-for-deletion entries: freshly
// scanned, tagged, and stripped of protected paths, but not cleaned.
type ApprovedCategory struct {
	Category cleaner.Category
	Name     string
	Entries  []cleaner.FileEntry

	// Err is set when the scan itself failed, or when the category cannot
	// honor a filtered entry list (DeletesWholeDomain) and protected paths
	// were found in it -- the same "skip the whole category" rule runClean
	// applies per cleaner. Entries is empty whenever Err is set.
	Err error
}

// ResolveApprovedEntries scans, tags, and strips protected paths for exactly
// the given categories, without cleaning anything. It runs the identical
// per-category pipeline runClean's own loop applies right before calling
// Clean -- including honoring a prepared --from-file scan the same way --
// so a caller that needs a category's approved entries without cleaning it
// can't drift from what an ordinary clean run would have used.
//
// It exists for the elevated-helper path in cmd/clean.go: entries destined
// for elevate.Invoke must be gathered the same way as everything else clean
// deletes, not through a second, possibly-divergent implementation of scan
// + tag + strip.
func ResolveApprovedEntries(ctx context.Context, registry *cleaner.Registry, selected []string, cfg *config.Config, preparedScan ScanResult, usePreparedScan bool) ([]ApprovedCategory, error) {
	cleaners, err := resolveCleaners(registry, selected, cfg)
	if err != nil {
		return nil, err
	}

	results := make([]ApprovedCategory, len(cleaners))
	var wg sync.WaitGroup
	wg.Add(len(cleaners))

	for i, c := range cleaners {
		i, c := i, c

		go func() {
			defer wg.Done()

			name := c.Category().DisplayName()
			results[i] = ApprovedCategory{Category: c.Category(), Name: name}

			scanResult, scanErr := buildCleanScanResult(c, preparedScan, usePreparedScan)
			if !usePreparedScan {
				scanResult, scanErr = c.Scan(ctx, nil)
			}
			if scanErr != nil {
				results[i].Err = scanErr
				return
			}
			if scanResult == nil {
				return
			}

			scanResult.Entries = cfg.Tag(scanResult.Entries)
			if protected := config.CountProtected(scanResult.Entries); protected > 0 && c.DeletesWholeDomain() {
				results[i].Err = fmt.Errorf("skipped: %d protected path(s) found in this category, and %s cannot selectively clean around them", protected, name)
				return
			}
			results[i].Entries = config.StripProtected(scanResult.Entries)
		}()
	}

	wg.Wait()
	return results, nil
}

// buildCleanScanResult constructs a cleaner.ScanResult from a prepared ScanResult for a specific cleaner category.
// It helps avoid re-scanning if we already have the scan results available.
func buildCleanScanResult(c cleaner.Cleaner, preparedScan ScanResult, usePreparedScan bool) (*cleaner.ScanResult, error) {
	if !usePreparedScan {
		return nil, nil
	}

	for _, category := range preparedScan.Categories {
		if category.Category != c.Category() {
			continue
		}
		if category.ErrMsg != "" {
			return nil, fmt.Errorf("%s", category.ErrMsg)
		}
		return &cleaner.ScanResult{
			Category:   c.Category(),
			Entries:    category.Files,
			TotalFiles: category.TotalFiles,
			TotalSize:  category.TotalSize,
		}, nil
	}

	return &cleaner.ScanResult{Category: c.Category()}, nil
}

// WriteCleanOutput writes the CleanOutput to the provided writer in the specified format (e.g., JSON).
func WriteCleanOutput(w io.Writer, output CleanOutput, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	default:
		return fmt.Errorf("unsupported format %q: must be json", format)
	}
}
