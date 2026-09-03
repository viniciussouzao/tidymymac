package elevate

import (
	"errors"
	"fmt"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
)

// CategoryResults translates the outcome of one Invoke call into per-category
// commands.CleanCategoryResult entries, one per plan.Categories entry in the
// same order, so a caller can merge them into an ordinary CleanResult's
// Categories slice without an extra lookup step.
//
// It exists so a caller working in terms of commands.CleanCategoryResult --
// currently cmd/clean.go's interactive `clean --execute` path -- can
// interpret Invoke's outcome without re-deriving the same three-way split
// (and risking a subtly different bug from the one Invoke's own
// honest-outcome contract was specifically designed to prevent). The TUI's
// own execute flow predates this function and still has its own equivalent
// mapping in internal/tui/app.go's handleElevateComplete, built around
// cleaner.CleanResult rather than commands.CleanCategoryResult; the two are
// intentionally kept to the same contract, verified by each package's own
// tests, rather than merged into one shared implementation.
//
// The mapping mirrors Invoke's documented contract exactly: only err ==
// ErrElevationFailed may ever be reported as "nothing was deleted" for a
// category. Every other outcome -- a successful call, ErrElevationOutcomeUnknown,
// or any other unexpected error -- must assume a partial clean is possible and
// say so, never silently read as a plain skip or a clean success.
func CategoryResults(plan Plan, result Result, err error) []commands.CleanCategoryResult {
	switch {
	case err == nil:
		return successCategoryResults(plan, result)

	case errors.Is(err, ErrElevationFailed):
		// err always carries Invoke's own explanation (auth failure, a guard
		// rejection, or a spawn failure) -- surface it rather than guessing a
		// single specific cause, since only the "nothing was deleted" half of
		// any hardcoded guess is guaranteed true for every case this covers.
		return uniformErrorResults(plan, fmt.Sprintf("elevation did not run (%v); nothing was deleted", err))

	default:
		// Includes ErrElevationOutcomeUnknown and any other unexpected
		// failure: the helper may have been past its guards and mid-deletion,
		// so this must never read as "nothing happened".
		return uniformErrorResults(plan, fmt.Sprintf("elevated helper outcome unknown (%v); re-scan to check what was deleted", err))
	}
}

func successCategoryResults(plan Plan, result Result) []commands.CleanCategoryResult {
	byCategory := make(map[cleaner.Category]commands.CleanCategoryResult, len(result.Clean.Categories))
	for _, ccr := range result.Clean.Categories {
		byCategory[ccr.Category] = ccr
	}
	intersections := make(map[cleaner.Category]CategoryIntersection, len(result.Intersections))
	for _, ci := range result.Intersections {
		intersections[ci.Category] = ci
	}

	out := make([]commands.CleanCategoryResult, len(plan.Categories))
	for i, pc := range plan.Categories {
		if ccr, ok := byCategory[pc.Category]; ok {
			// Err carries json:"-" and never survives the Result's trip over
			// the wire from the helper; reconstruct it from ErrMsg so callers
			// that check Err (as the rest of this codebase does) see it too.
			if ccr.ErrMsg != "" && ccr.Err == nil {
				ccr.Err = errors.New(ccr.ErrMsg)
			}
			out[i] = ccr
			continue
		}

		if in, ok := intersections[pc.Category]; ok {
			if in.ErrMsg != "" {
				out[i] = errCategoryResult(pc.Category, in.ErrMsg)
				continue
			}
			// Approved but nothing matched the helper's fresh root scan:
			// already gone, or never belonged to this category. Not an
			// error -- an ordinary zero-result category, same as one that
			// simply scanned to nothing.
			out[i] = commands.CleanCategoryResult{Category: pc.Category, Name: pc.Category.DisplayName()}
			continue
		}

		// The helper reported success overall but said nothing at all about
		// this specific category -- unlike the two cases above, there is no
		// observation to report a confident zero-result from.
		out[i] = errCategoryResult(pc.Category, "the elevated helper returned no result for this category; outcome unknown")
	}
	return out
}

func uniformErrorResults(plan Plan, reason string) []commands.CleanCategoryResult {
	out := make([]commands.CleanCategoryResult, len(plan.Categories))
	for i, pc := range plan.Categories {
		out[i] = errCategoryResult(pc.Category, reason)
	}
	return out
}

func errCategoryResult(category cleaner.Category, msg string) commands.CleanCategoryResult {
	return commands.CleanCategoryResult{
		Category: category,
		Name:     category.DisplayName(),
		ErrMsg:   msg,
		Err:      errors.New(msg),
	}
}
