package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// SplitEntriesByPrivilege partitions a category's approved entries into the
// subset that genuinely needs root to delete and the subset the invoking
// user can delete directly, using cleaner.PrivilegeSplitter when c
// implements it. A cleaner that does not implement the interface keeps every
// entry on the sudo side -- RequiresSudo()'s current all-or-nothing behavior
// is the conservative default when no finer-grained split is available.
func SplitEntriesByPrivilege(c cleaner.Cleaner, entries []cleaner.FileEntry) (sudoEntries, directEntries []cleaner.FileEntry) {
	splitter, ok := c.(cleaner.PrivilegeSplitter)
	if !ok {
		return entries, nil
	}
	// A whole-domain cleaner ignores the entries it is handed and clears
	// everything it owns, so it must never be called with a deliberately
	// partial list (see Cleaner.DeletesWholeDomain) -- and a direct leg is
	// exactly that. No such cleaner exists today
	// (TestNoCleanerIsBothSudoAndWholeDomain), but if one ever does, it stays
	// all-or-nothing rather than silently wiping the sudo half too.
	if c.DeletesWholeDomain() {
		return entries, nil
	}
	for _, e := range entries {
		if splitter.NeedsSudo(e) {
			sudoEntries = append(sudoEntries, e)
		} else {
			directEntries = append(directEntries, e)
		}
	}
	return sudoEntries, directEntries
}

// CleanDirectly runs Clean for entries a caller has determined (via
// SplitEntriesByPrivilege) do not need elevation, and converts the outcome
// into the same CleanCategoryResult shape runClean produces for an ordinary
// non-sudo category -- so a direct-clean leg for a privilege-split category
// looks exactly like it would have if the category had never required sudo
// at all, and can be merged with an elevated leg via MergeCategoryResults.
func CleanDirectly(ctx context.Context, c cleaner.Cleaner, entries []cleaner.FileEntry, dryRun bool) CleanCategoryResult {
	name := c.Category().DisplayName()
	item := CleanCategoryResult{Category: c.Category(), Name: name}
	if len(entries) == 0 {
		return item
	}

	result, err := c.Clean(ctx, entries, dryRun, nil)
	if result != nil {
		item.DeletedFiles = result.FilesDeleted
		item.DeletedSize = result.BytesFreed
		item.PartialErrors = len(result.Errors)
		item.PartialErrorDetails, item.PartialErrorsTruncated = itemErrors(result.Errors)
	}
	if err != nil {
		item.Err = err
		item.ErrMsg = err.Error()
	}
	return item
}

// directCleanedSuffix qualifies a merged error message when the direct leg
// reclaimed something the failing leg's own wording would otherwise deny.
const directCleanedSuffix = " (%d of this category's own files were still cleaned directly, without elevation)"

// MergeCategoryResults combines a direct-clean leg (see CleanDirectly) with
// an elevated leg for the SAME category into one row, so a privilege-split
// category never appears twice in a result set or a history record.
//
// Deleted counts are summed unconditionally: a confirmed direct deletion is
// real regardless of what happened to the elevated leg, mirroring the
// existing rule that a partial failure's reclaimed counts still count (see
// CleanCategoryResult.PartialErrors's doc comment). The elevated leg's
// Err/ErrMsg wins when set -- it is the leg whose outcome can be a failure
// or genuinely unknown (see elevate.ErrElevationOutcomeUnknown); the direct
// leg, by the time this is called, has already run to completion and its
// outcome is always fully known. PartialErrorDetails from both legs are
// concatenated, re-bounded to MaxPartialErrorDetails. A winning message is
// qualified with directCleanedSuffix when the direct leg did reclaim
// something, so the row never claims nothing was deleted while reporting a
// deletion.
func MergeCategoryResults(direct, elevated CleanCategoryResult) CleanCategoryResult {
	merged := CleanCategoryResult{
		Category:     elevated.Category,
		Name:         elevated.Name,
		DeletedFiles: direct.DeletedFiles + elevated.DeletedFiles,
		DeletedSize:  direct.DeletedSize + elevated.DeletedSize,
		// PartialErrors is the true total, not len(details): the details are
		// bounded for output size, the count is what HasErrors and the
		// summary line are derived from.
		PartialErrors: direct.PartialErrors + elevated.PartialErrors,
	}
	if merged.Category == "" {
		merged.Category = direct.Category
	}
	if merged.Name == "" {
		merged.Name = direct.Name
	}

	// Files is only populated on the --detailed CLI path and by the elevated
	// helper (with the entries it matched); concatenating keeps both legs'
	// view of what was in scope rather than silently dropping one.
	if len(direct.Files) > 0 || len(elevated.Files) > 0 {
		merged.Files = make([]cleaner.FileEntry, 0, len(direct.Files)+len(elevated.Files))
		merged.Files = append(merged.Files, direct.Files...)
		merged.Files = append(merged.Files, elevated.Files...)
	}

	details := make([]ItemError, 0, len(direct.PartialErrorDetails)+len(elevated.PartialErrorDetails))
	details = append(details, direct.PartialErrorDetails...)
	details = append(details, elevated.PartialErrorDetails...)
	truncated := direct.PartialErrorsTruncated || elevated.PartialErrorsTruncated
	if len(details) > MaxPartialErrorDetails {
		details = details[:MaxPartialErrorDetails]
		truncated = true
	}
	if len(details) > 0 {
		merged.PartialErrorDetails = details
	}
	merged.PartialErrorsTruncated = truncated

	if elevated.Err != nil || elevated.ErrMsg != "" {
		merged.Err, merged.ErrMsg = elevated.Err, elevated.ErrMsg
	} else {
		merged.Err, merged.ErrMsg = direct.Err, direct.ErrMsg
	}

	// The winning message describes one leg's outcome, and the elevated
	// leg's failures say so in absolute terms ("nothing was deleted", see
	// elevate.ErrElevationFailed) -- true of that leg, false of the merged
	// row once the direct leg reclaimed something. Qualify it rather than
	// let a completed deletion sit next to a claim that none happened. The
	// qualified error is a plain errors.New: it is a composed description of
	// two legs, not a sentinel any caller should match on.
	if merged.ErrMsg != "" && (direct.DeletedFiles > 0 || direct.DeletedSize > 0) {
		merged.ErrMsg += fmt.Sprintf(directCleanedSuffix, direct.DeletedFiles)
		merged.Err = errors.New(merged.ErrMsg)
	}

	return merged
}
