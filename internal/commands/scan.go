package commands

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// ScanOptions defines options for the scanning process.
type ScanOptions struct {
	Detailed bool
	Config   *config.Config
}

// ScanCategoryResult represents the result of scanning a specific category, including metadata and any errors encountered.
type ScanCategoryResult struct {
	Category       cleaner.Category    `json:"category"`
	Name           string              `json:"name"`
	RequireSudo    bool                `json:"requires_sudo"`
	TotalFiles     int                 `json:"total_files"`
	TotalSize      int64               `json:"total_size_bytes"`
	TotalSizeHuman string              `json:"total_size_human"`
	Files          []cleaner.FileEntry `json:"files,omitempty"`
	Err            error               `json:"-"`
	ErrMsg         string              `json:"error,omitempty"`
}

// ScanResult represents the overall result of a scanning operation.
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
	cleaners, err := resolveCleaners(registry, selected, opts.Config)
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
			requireSudo := c.RequiresSudo()

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

			if scanErr == nil && scanResult != nil {
				scanResult.Entries = opts.Config.Tag(scanResult.Entries)
			}

			item := ScanCategoryResult{
				Category:    c.Category(),
				Name:        name,
				Err:         scanErr,
				RequireSudo: requireSudo,
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
// When selected is empty, categories disabled via cfg.DisabledCategories are excluded from the default set;
// an explicit selection always wins over cfg, since it reflects the user's direct intent.
func resolveCleaners(registry *cleaner.Registry, selected []string, cfg *config.Config) ([]cleaner.Cleaner, error) {
	if len(selected) == 0 {
		all := registry.All()
		cleaners := config.FilterRegistry(registry, cfg).All()
		if len(cleaners) == 0 && len(all) > 0 {
			return nil, fmt.Errorf("all categories are disabled by config; pass explicit categories to override")
		}
		return cleaners, nil
	}

	cleaners := make([]cleaner.Cleaner, 0, len(selected))
	// Deduped by category: "clean docker docker" would otherwise run the
	// cleaner twice, scanning and deleting the same domain in two passes. The
	// sudo half of clean already deduped its own selection; this makes the
	// ordinary path agree, for scan and clean alike.
	seen := make(map[cleaner.Category]struct{}, len(selected))

	for _, raw := range selected {
		category := cleaner.Category(raw)

		c, ok := registry.Get(category)
		if !ok {
			return nil, fmt.Errorf("unknown category %q", raw)
		}

		if _, dup := seen[category]; dup {
			continue
		}
		seen[category] = struct{}{}

		cleaners = append(cleaners, c)
	}

	return cleaners, nil
}

// WriteOutput writes the scan result to w in the specified format.
// format must be "json", "csv" or "table". detailed controls whether
// individual file entries are included (only applicable to json, csv, and
// table formats). printAll only applies to table output: when false, each
// category/group is capped at maxTableEntries rows.
func WriteOutput(w io.Writer, result ScanResult, format string, detailed bool, printAll bool) error {
	switch format {
	case "json":
		return writeJSON(w, result)
	case "csv":
		return writeCSV(w, result, detailed)
	case "table":
		return writeTable(w, result, printAll)
	default:
		return fmt.Errorf("unsupported format %q: must be json, csv, or table", format)
	}
}

// maxTableEntries is the number of rows shown per category/group in table
// output when printAll is false.
const maxTableEntries = 10

// dockerResourceGroup pairs a Docker ResourceKind with its display label.
// Kept as an ordered slice (rather than a map) so a future kind (e.g. build
// cache) can be added without restructuring writeTable; groups with no
// matching entries simply don't render.
type dockerResourceGroup struct {
	kind  string
	label string
}

var dockerResourceGroups = []dockerResourceGroup{
	{cleaner.DockerResourceKindImageDangling, "Unreferenced images"},
	{cleaner.DockerResourceKindImageStoppedContainer, "Images tied to stopped containers"},
	{cleaner.DockerResourceKindContainerStopped, "Stopped containers"},
	{cleaner.DockerResourceKindVolumeOrphaned, "Unused volumes"},
}

// writeTable writes a concise, human-readable report of result to w. It
// requires detailed data (result.Categories[i].Files) to produce per-entry
// breakdowns; categories without Files are reported by their totals only.
func writeTable(w io.Writer, result ScanResult, printAll bool) error {
	if _, err := fmt.Fprintf(w, "Scan report - %s\n", result.ScannedAt.Local().Format("2006-01-02 15:04:05 MST")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Total: %d items, %s\n\n", result.TotalFiles, utils.FormatBytes(result.TotalSize)); err != nil {
		return err
	}

	for _, cat := range result.Categories {
		if _, err := fmt.Fprintf(w, "== %s ==\n", cat.Name); err != nil {
			return err
		}

		if cat.Err != nil {
			// Error text can embed a file name (fs.PathError does), so it
			// gets the same treatment as the paths below.
			if _, err := fmt.Fprintf(w, "  error: %s (category skipped; re-run 'tidymymac scan %s' to retry)\n\n", utils.SanitizeForTerminal(cat.ErrMsg), cat.Category); err != nil {
				return err
			}
			continue
		}

		if cat.TotalFiles == 0 {
			if _, err := fmt.Fprintln(w, "  nothing found"); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
			continue
		}

		if _, err := fmt.Fprintf(w, "  %d items, %s\n", cat.TotalFiles, utils.FormatBytes(cat.TotalSize)); err != nil {
			return err
		}

		var err error
		if cat.Category == cleaner.CategoryDocker {
			err = writeDockerGroups(w, cat.Files, printAll)
		} else {
			err = writeEntryTable(w, cat.Files, printAll, "  ")
		}
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	return nil
}

// writeDockerGroups renders each Docker resource group (see
// dockerResourceGroups) with its own count, size, and entry table.
func writeDockerGroups(w io.Writer, files []cleaner.FileEntry, printAll bool) error {
	for _, group := range dockerResourceGroups {
		var entries []cleaner.FileEntry
		for _, f := range files {
			if f.ResourceKind == group.kind {
				entries = append(entries, f)
			}
		}
		if len(entries) == 0 {
			continue
		}

		var size int64
		for _, e := range entries {
			size += e.Size
		}

		if _, err := fmt.Fprintf(w, "  -- %s (%d, %s) --\n", group.label, len(entries), utils.FormatBytes(size)); err != nil {
			return err
		}
		if err := writeEntryTable(w, entries, printAll, "    "); err != nil {
			return err
		}
	}
	return nil
}

// writeEntryTable prints entries sorted by size descending, one per line,
// prefixed with indent. When printAll is false, output is capped at
// maxTableEntries rows and the omitted count is reported.
func writeEntryTable(w io.Writer, entries []cleaner.FileEntry, printAll bool, indent string) error {
	sorted := make([]cleaner.FileEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Size > sorted[j].Size })

	shown := sorted
	omitted := 0
	if !printAll && len(sorted) > maxTableEntries {
		shown = sorted[:maxTableEntries]
		omitted = len(sorted) - maxTableEntries
	}

	for _, e := range shown {
		// Paths are untrusted: a file name (or a Docker image tag) can carry
		// a newline or an escape sequence that would inject a fake row or
		// rewrite what the terminal shows. Escape for display only -- the
		// entry itself is never modified.
		if _, err := fmt.Fprintf(w, "%s%10s  %s\n", indent, utils.FormatBytes(e.Size), utils.SanitizeForTerminal(e.Path)); err != nil {
			return err
		}
	}

	if omitted > 0 {
		if _, err := fmt.Fprintf(w, "%s... %d more omitted, use --print-all to list all\n", indent, omitted); err != nil {
			return err
		}
	}
	return nil
}

// writeJSON encodes the ScanResult as pretty-printed JSON and writes it to w.
func writeJSON(w io.Writer, result ScanResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// writeCSV writes the ScanResult to w in CSV format. If detailed is true, it includes individual file entries.
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
