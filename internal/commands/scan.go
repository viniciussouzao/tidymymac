package commands

import (
	"context"
	"fmt"
	"sync"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

type ScanCategoryResult struct {
	Category   cleaner.Category
	Name       string
	TotalSize  int64
	TotalFiles int
	Err        error
}

type ScanResult struct {
	Categories []ScanCategoryResult
	TotalSize  int64
	TotalFiles int
	HasErrors  bool
}

type ScanEventType string

const (
	ScanEventStarted  ScanEventType = "started"
	ScanEventProgress ScanEventType = "progress"
	ScanEventDone     ScanEventType = "done"
)

type ScanEvent struct {
	Type     ScanEventType
	Category cleaner.Category
	Name     string
	Progress cleaner.ScanProgress
	Result   *ScanCategoryResult
	Err      error
}

func RunScan(ctx context.Context, registry *cleaner.Registry, selected []string, onEvent func(ScanEvent)) (ScanResult, error) {
	cleaners, err := resolveCleaners(registry, selected)
	if err != nil {
		return ScanResult{}, err
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		result = ScanResult{Categories: make([]ScanCategoryResult, 0, len(cleaners))}
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

			if scanResult != nil {
				item.TotalSize = scanResult.TotalSize
				item.TotalFiles = scanResult.TotalFiles
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

	return result, nil
}

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
