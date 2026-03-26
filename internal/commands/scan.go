package commands

import (
	"context"
	"fmt"

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
}

func RunScan(ctx context.Context, registry *cleaner.Registry, selected []string) (ScanResult, error) {
	cleaners, err := resolveCleaners(registry, selected)
	if err != nil {
		return ScanResult{}, err
	}

	result := ScanResult{}

	for _, c := range cleaners {
		scanResult, scanErr := c.Scan(ctx, nil)

		item := ScanCategoryResult{
			Category: c.Category(),
			Name:     c.Category().DisplayName(),
			Err:      scanErr,
		}

		if scanResult != nil {
			item.TotalSize = scanResult.TotalSize
			item.TotalFiles = scanResult.TotalFiles

			result.TotalSize += scanResult.TotalSize
			result.TotalFiles += scanResult.TotalFiles
		}

		result.Categories = append(result.Categories, item)
	}

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
