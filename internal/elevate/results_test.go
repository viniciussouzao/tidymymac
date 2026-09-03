package elevate

import (
	"errors"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/commands"
)

func testPlan(categories ...cleaner.Category) Plan {
	plan := Plan{Version: PlanSchemaVersion}
	for _, c := range categories {
		plan.Categories = append(plan.Categories, PlanCategory{Category: c, Entries: []cleaner.FileEntry{{Path: "/x", Size: 1}}})
	}
	return plan
}

func TestCategoryResults_SuccessPassesThroughCleanCategoryResult(t *testing.T) {
	plan := testPlan(cleaner.CategoryTemp)
	result := Result{
		Version: ResultSchemaVersion,
		Clean: commands.CleanResult{
			Categories: []commands.CleanCategoryResult{
				{Category: cleaner.CategoryTemp, Name: "Temp", DeletedFiles: 3, DeletedSize: 100},
			},
		},
	}

	got := CategoryResults(plan, result, nil)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].DeletedFiles != 3 || got[0].DeletedSize != 100 {
		t.Fatalf("got[0] = %+v, want the helper's own counts", got[0])
	}
	if got[0].Err != nil {
		t.Errorf("Err = %v, want nil for a clean success", got[0].Err)
	}
}

func TestCategoryResults_SuccessReconstructsErrFromErrMsg(t *testing.T) {
	// Err carries json:"-" and never survives Invoke's JSON round trip from
	// the helper; CategoryResults must reconstruct it from ErrMsg so every
	// caller that checks Err (as the rest of this codebase does) sees it.
	plan := testPlan(cleaner.CategoryTemp)
	result := Result{
		Version: ResultSchemaVersion,
		Clean: commands.CleanResult{
			Categories: []commands.CleanCategoryResult{
				{Category: cleaner.CategoryTemp, Name: "Temp", ErrMsg: "boom"},
			},
		},
	}

	got := CategoryResults(plan, result, nil)
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("got = %+v, want Err reconstructed from ErrMsg", got)
	}
}

func TestCategoryResults_IntersectionErrorBecomesCategoryError(t *testing.T) {
	plan := testPlan(cleaner.CategoryTemp)
	result := Result{
		Version: ResultSchemaVersion,
		Intersections: []CategoryIntersection{
			{Category: cleaner.CategoryTemp, ErrMsg: "fresh scan failed"},
		},
	}

	got := CategoryResults(plan, result, nil)
	if len(got) != 1 || got[0].Err == nil || !strings.Contains(got[0].ErrMsg, "fresh scan failed") {
		t.Fatalf("got = %+v, want the intersection's scan error surfaced", got)
	}
}

func TestCategoryResults_NothingMatchedIsAZeroResultNotAnError(t *testing.T) {
	plan := testPlan(cleaner.CategoryTemp)
	result := Result{
		Version: ResultSchemaVersion,
		Intersections: []CategoryIntersection{
			{Category: cleaner.CategoryTemp, Approved: 1, Matched: 0, Missing: 1},
		},
	}

	got := CategoryResults(plan, result, nil)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Err != nil {
		t.Errorf("Err = %v, want nil -- nothing matching the fresh scan is a skip, not a failure", got[0].Err)
	}
	if got[0].DeletedFiles != 0 || got[0].DeletedSize != 0 {
		t.Errorf("got[0] = %+v, want zero counts", got[0])
	}
}

func TestCategoryResults_NoObservationAtAllIsOutcomeUnknown(t *testing.T) {
	// The helper reported overall success but said nothing about this
	// specific category at all -- unlike a reported "nothing matched", there
	// is no observation to report a confident skip from.
	plan := testPlan(cleaner.CategoryTemp)
	result := Result{Version: ResultSchemaVersion}

	got := CategoryResults(plan, result, nil)
	if len(got) != 1 || got[0].Err == nil || !strings.Contains(got[0].ErrMsg, "outcome unknown") {
		t.Fatalf("got = %+v, want an outcome-unknown error, never a silent skip", got)
	}
}

func TestCategoryResults_ElevationFailedNeverClaimsPartialProgress(t *testing.T) {
	plan := testPlan(cleaner.CategoryTemp, cleaner.CategoryLogs)

	got := CategoryResults(plan, Result{}, errors.Join(ErrElevationFailed, errors.New("sudo: 3 incorrect password attempts")))
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.Err == nil {
			t.Errorf("category %s: Err = nil, want a non-nil error for ErrElevationFailed", r.Category)
		}
		if r.DeletedFiles != 0 || r.DeletedSize != 0 {
			t.Errorf("category %s: got %+v, want zero counts -- nothing was deleted", r.Category, r)
		}
		if !strings.Contains(r.ErrMsg, "nothing was deleted") {
			t.Errorf("category %s: ErrMsg = %q, want it to say nothing was deleted", r.Category, r.ErrMsg)
		}
	}
}

func TestCategoryResults_OutcomeUnknownNeverClaimsNothingWasDeleted(t *testing.T) {
	plan := testPlan(cleaner.CategoryTemp)

	got := CategoryResults(plan, Result{}, errors.Join(ErrElevationOutcomeUnknown, errors.New("signal: terminated")))
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("got = %+v, want a non-nil error", got)
	}
	if strings.Contains(got[0].ErrMsg, "nothing was deleted") {
		t.Errorf("ErrMsg = %q, must never claim nothing was deleted when the outcome is unknown", got[0].ErrMsg)
	}
	if !strings.Contains(got[0].ErrMsg, "outcome unknown") {
		t.Errorf("ErrMsg = %q, want it to say the outcome is unknown", got[0].ErrMsg)
	}
}

func TestCategoryResults_UnexpectedErrorIsTreatedAsOutcomeUnknown(t *testing.T) {
	plan := testPlan(cleaner.CategoryTemp)

	got := CategoryResults(plan, Result{}, errors.New("some unclassified failure"))
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("got = %+v, want a non-nil error", got)
	}
	if strings.Contains(got[0].ErrMsg, "nothing was deleted") {
		t.Errorf("ErrMsg = %q, an unrecognized error must not be assumed safe either", got[0].ErrMsg)
	}
}
