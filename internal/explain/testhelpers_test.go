package explain

import (
	"context"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

type stubCleaner struct {
	category   cleaner.Category
	scanResult *cleaner.ScanResult
	scanErr    error
}

func (s stubCleaner) Category() cleaner.Category    { return s.category }
func (s stubCleaner) Name() string                  { return "stub" }
func (s stubCleaner) Description() string           { return "" }
func (s stubCleaner) RequiresSudo() bool            { return false }

func (s stubCleaner) Scan(_ context.Context, _ func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	return s.scanResult, s.scanErr
}

func (s stubCleaner) Clean(_ context.Context, _ []cleaner.FileEntry, _ bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	return &cleaner.CleanResult{}, nil
}

type stubContributor struct {
	name   ContributorName
	result ContributorResult
	err    error
}

func (s stubContributor) Name() ContributorName                           { return s.name }
func (s stubContributor) Description() string                             { return "" }
func (s stubContributor) Sources() []string                               { return nil }
func (s stubContributor) Run(_ context.Context) (ContributorResult, error) {
	return s.result, s.err
}
