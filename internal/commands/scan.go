package commands

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// ScanOptions defines options for the scanning process.
type ScanOptions struct {
	Detailed bool
}

// ScanCategoryResult represents the result of scanning a specific category, including metadata and any errors encountered.
type ScanCategoryResult struct {
	Category       cleaner.Category    `json:"category"`
	Name           string              `json:"name"`
	TotalFiles     int                 `json:"total_files"`
	TotalSize      int64               `json:"total_size_bytes"`
	TotalSizeHuman string              `json:"total_size_human"`
	Files          []cleaner.FileEntry `json:"files,omitempty"`
	Err            error               `json:"-"`
	ErrMsg         string              `json:"error,omitempty"`
}

// ScanResult represents the overall result of a scanning operation
type ScanResult struct {
	ScannedAt      time.Time            `json:"scanned_at"`
	TotalFiles     int                  `json:"total_files"`
	TotalSize      int64                `json:"total_size_bytes"`
	TotalSizeHuman string               `json:"total_size_human"`
	HasErrors      bool                 `json:"has_errors"`
	Categories     []ScanCategoryResult `json:"categories"`
}

// ScanEventType defines the type of events emitted during the scanning process.
type ScanEventType string

const (
	ScanEventStarted  ScanEventType = "started"
	ScanEventProgress ScanEventType = "progress"
	ScanEventDone     ScanEventType = "done"
)

// ScanEvent represents an event emitted during the scanning process, containing information about the category being scanned, progress updates, and any results or errors.
type ScanEvent struct {
	Type     ScanEventType
	Category cleaner.Category
	Name     string
	Progress cleaner.ScanProgress
	Result   *ScanCategoryResult
	Err      error
}

// RunScan executes the scanning process for the specified categories and returns a ScanResult summarizing the findings.
func RunScan(ctx context.Context, registry *cleaner.Registry, selected []string, opts ScanOptions, onEvent func(ScanEvent)) (ScanResult, error) {
	cleaners, err := resolveCleaners(registry, selected)
	if err != nil {
		return ScanResult{}, err
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		result = ScanResult{
			ScannedAt:  time.Now().UTC(),
			Categories: make([]ScanCategoryResult, 0, len(cleaners)),
		}
	)

	wg.Add(len(cleaners))

	for _, c := range cleaners {
		go func() {
			defer wg.Done()

			name := c.Category().DisplayName()

			if onEvent != nil {
				onEvent(ScanEvent{
					Type:     ScanEventStarted,
					Category: c.Category(),
					Name:     name,
				})
			}

			scanResult, scanErr := c.Scan(ctx, func(progress cleaner.ScanProgress) {
				if onEvent != nil {
					onEvent(ScanEvent{
						Type:     ScanEventProgress,
						Category: c.Category(),
						Name:     name,
						Progress: progress,
					})
				}
			})

			item := ScanCategoryResult{
				Category: c.Category(),
				Name:     name,
				Err:      scanErr,
			}
			if scanErr != nil {
				item.ErrMsg = scanErr.Error()
			}

			if scanResult != nil {
				item.TotalSize = scanResult.TotalSize
				item.TotalSizeHuman = utils.FormatBytes(scanResult.TotalSize)
				item.TotalFiles = scanResult.TotalFiles
				if opts.Detailed {
					item.Files = scanResult.Entries
				}
			}

			mu.Lock()
			defer mu.Unlock()

			result.Categories = append(result.Categories, item)
			if scanErr != nil {
				result.HasErrors = true
			} else {
				result.TotalSize += item.TotalSize
				result.TotalFiles += item.TotalFiles
			}

			if onEvent != nil {
				itemCopy := item
				onEvent(ScanEvent{
					Type:     ScanEventDone,
					Category: c.Category(),
					Name:     name,
					Result:   &itemCopy,
					Err:      scanErr,
				})
			}
		}()
	}

	wg.Wait()

	// Reorder to match the original registry order (goroutines may append in any order).
	resultMap := make(map[cleaner.Category]ScanCategoryResult, len(cleaners))
	for _, cat := range result.Categories {
		resultMap[cat.Category] = cat
	}
	ordered := make([]ScanCategoryResult, 0, len(cleaners))
	for _, c := range cleaners {
		if r, ok := resultMap[c.Category()]; ok {
			ordered = append(ordered, r)
		}
	}
	result.Categories = ordered
	result.TotalSizeHuman = utils.FormatBytes(result.TotalSize)

	return result, nil
}

// resolveCleaners is a helper function that takes a registry of cleaners and a list of selected category strings, and returns a slice of Cleaner instances corresponding to the selected categories.
func resolveCleaners(registry *cleaner.Registry, selected []string) ([]cleaner.Cleaner, error) {
	if len(selected) == 0 {
		return registry.All(), nil
	}

	cleaners := make([]cleaner.Cleaner, 0, len(selected))

	for _, raw := range selected {
		category := cleaner.Category(raw)

		c, ok := registry.Get(category)
		if !ok {
			return nil, fmt.Errorf("unknown category %q", raw)
		}

		cleaners = append(cleaners, c)
	}

	return cleaners, nil
}

// WriteOutput writes the scan result to w in the specified format.
// format must be "json" or "csv". detailed controls whether individual file
// entries are included (only applicable to json and csv formats).
func WriteOutput(w io.Writer, result ScanResult, format string, detailed bool) error {
	switch format {
	case "json":
		return writeJSON(w, result)
	case "csv":
		return writeCSV(w, result, detailed)
	default:
		return fmt.Errorf("unsupported format %q: must be json or csv", format)
	}
}

// writeJSON encodes the ScanResult as pretty-printed JSON and writes it to w.
func writeJSON(w io.Writer, result ScanResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// writeCSV writes the ScanResult to w in CSV format. If detailed is true, it includes individual file entries
func writeCSV(w io.Writer, result ScanResult, detailed bool) error {
	cw := csv.NewWriter(w)

	if detailed {
		if err := cw.Write([]string{"category", "path", "size_bytes", "size_human", "is_dir", "mod_time"}); err != nil {
			return err
		}
		for _, cat := range result.Categories {
			for _, f := range cat.Files {
				record := []string{
					cat.Name,
					f.Path,
					fmt.Sprintf("%d", f.Size),
					utils.FormatBytes(f.Size),
					fmt.Sprintf("%t", f.IsDir),
					f.ModTime.UTC().Format("2006-01-02T15:04:05Z"),
				}
				if err := cw.Write(record); err != nil {
					return err
				}
			}
		}
	} else {
		if err := cw.Write([]string{"category", "files", "size_bytes", "size_human", "error"}); err != nil {
			return err
		}
		for _, cat := range result.Categories {
			record := []string{
				cat.Name,
				fmt.Sprintf("%d", cat.TotalFiles),
				fmt.Sprintf("%d", cat.TotalSize),
				utils.FormatBytes(cat.TotalSize),
				cat.ErrMsg,
			}
			if err := cw.Write(record); err != nil {
				return err
			}
		}
		total := []string{
			"Total",
			fmt.Sprintf("%d", result.TotalFiles),
			fmt.Sprintf("%d", result.TotalSize),
			utils.FormatBytes(result.TotalSize),
			"",
		}
		if err := cw.Write(total); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
