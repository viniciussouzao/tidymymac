package explain

import (
	"context"
	"fmt"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

type contributorSpec struct {
	name        ContributorName
	description string
	sources     []string
	category    cleaner.Category
	context     ExplainContext
}

type scannerContributor struct {
	name        ContributorName
	description string
	sources     []string
	context     ExplainContext
	cleaner     cleaner.Cleaner
	runtimeErr  error
}

func (c scannerContributor) Name() ContributorName { return c.name }

func (c scannerContributor) Description() string { return c.description }

// Sources returns a copy of the sources slice to prevent external modification.
func (c scannerContributor) Sources() []string {
	if len(c.sources) == 0 {
		return nil
	}

	out := make([]string, len(c.sources))
	copy(out, c.sources)
	return out
}

func (c scannerContributor) Run(ctx context.Context) (ContributorResult, error) {
	result := ContributorResult{
		Name:    c.name,
		Context: c.context,
	}

	if c.runtimeErr != nil {
		result.HasError = true
		result.ErrorMessage = c.runtimeErr.Error()
		return result, nil
	}

	if c.cleaner == nil {
		result.HasError = true
		result.ErrorMessage = fmt.Sprintf("cleaner for contributor %q is not available", c.name)
		return result, nil
	}

	scanResult, err := c.cleaner.Scan(ctx, nil)
	if scanResult != nil {
		result.TotalSize = scanResult.TotalSize
		result.TotalSizeHuman = utils.FormatBytes(scanResult.TotalSize)
		result.TotalItems = scanResult.TotalFiles
		if len(scanResult.Entries) > 0 {
			result.Items = make([]DetailedItem, 0, len(scanResult.Entries))
			for _, entry := range scanResult.Entries {
				result.Items = append(result.Items, DetailedItem{
					Path: entry.Path,
					Size: entry.Size,
				})
			}
		}
	} else {
		result.TotalSizeHuman = utils.FormatBytes(0)
	}

	if err != nil {
		result.HasError = true
		result.ErrorMessage = err.Error()
		return result, nil
	}

	return result, nil
}

// RunProfile executes all contributors in the given profile definition and aggregates the results.
func RunProfile(ctx context.Context, def ProfileDefinition) (ProfileResult, error) {
	if len(def.Profile) == 0 {
		return ProfileResult{}, fmt.Errorf("profile definition has no profile names")
	}

	result := ProfileResult{
		Name:         def.Profile[0],
		Description:  def.Description,
		Summary:      def.Summary,
		CoverageNote: def.CoverageNote,
		ScannedAt:    time.Now().UTC(),
	}

	for _, contributor := range def.Contributors {
		contributorResult, err := contributor.Run(ctx)
		if err != nil {
			contributorResult = ContributorResult{
				Name:         contributor.Name(),
				HasError:     true,
				ErrorMessage: err.Error(),
			}
		}
		result.Contributors = append(result.Contributors, contributorResult)
		if contributorResult.HasError {
			result.HasErrors = true
			continue
		}
		result.TotalSize += contributorResult.TotalSize
	}

	result.TotalSizeHuman = utils.FormatBytes(result.TotalSize)
	return result, nil
}
