package cleaner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppUninstallerImplementsCandidateExplainer(t *testing.T) {
	var _ CandidateExplainer = NewAppUninstaller(AppTarget{})
}

// TestEvidenceSourceBandMatrix pins the band of every evidence source in
// isolation, on non-shared data.
//
// Threshold choice documented here: bandForScore uses `score > 60` for Review,
// not `>= 60`. A lone name heuristic is worth exactly 60 and therefore lands in
// Caution -- a name match alone never implies "this belongs to the app", it
// only suggests it, so a human has to promote it.
func TestEvidenceSourceBandMatrix(t *testing.T) {
	tests := []struct {
		source    string
		wantScore int
		wantBand  ConfidenceBand
	}{
		{matchSourceExactBundleID, 100, ConfidenceSafe},
		{matchSourceKnownAppPath, 95, ConfidenceSafe},
		{matchSourceVendorIdentifier, 80, ConfidenceReview},
		{matchSourceNameHeuristic, 60, ConfidenceCaution},
		{"something_nobody_defined", 0, ConfidenceCaution},
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := scoreCandidate([]MatchReason{newMatchReason(tt.source)}, false)

			if got.Score != tt.wantScore {
				t.Errorf("Score = %d, want %d", got.Score, tt.wantScore)
			}
			if got.Band != tt.wantBand {
				t.Errorf("Band = %q, want %q", got.Band, tt.wantBand)
			}
			if got.Shared {
				t.Error("Shared = true, want false")
			}
			if len(got.Reasons) != 1 || got.Reasons[0].Source != tt.source {
				t.Errorf("Reasons = %v, want the single source %q", got.Reasons, tt.source)
			}
			if got.Reasons[0].Weight != tt.wantScore {
				t.Errorf("Reasons[0].Weight = %d, want %d (discovery must not leave it at zero)",
					got.Reasons[0].Weight, tt.wantScore)
			}
		})
	}
}

// TestNameHeuristicAloneNeverSafe is an invariant guard, not a value check: a
// leftover matched only by its name must never be pre-selected for deletion.
// It fails loudly if someone raises weightNameHeuristic to (or past)
// confidenceSafeThreshold, or lowers the threshold onto it.
func TestNameHeuristicAloneNeverSafe(t *testing.T) {
	if weightNameHeuristic >= confidenceSafeThreshold {
		t.Fatalf("weightNameHeuristic = %d must stay below confidenceSafeThreshold = %d: "+
			"a name match alone can never be Safe", weightNameHeuristic, confidenceSafeThreshold)
	}

	for _, shared := range []bool{false, true} {
		got := scoreCandidate([]MatchReason{newMatchReason(matchSourceNameHeuristic)}, shared)
		if got.Band == ConfidenceSafe {
			t.Errorf("name heuristic alone (shared=%v) produced %q, want anything but %q",
				shared, got.Band, ConfidenceSafe)
		}
	}

	// And for every score a lone name-heuristic reason could ever carry
	// without crossing the Safe threshold.
	for score := 0; score < confidenceSafeThreshold; score++ {
		reason := MatchReason{Source: matchSourceNameHeuristic, Weight: score}
		if got := scoreCandidate([]MatchReason{reason}, false); got.Band == ConfidenceSafe {
			t.Fatalf("score %d with a lone name heuristic produced %q", score, ConfidenceSafe)
		}
	}
}

// TestSharedAlwaysCaution pins the hard rule: shared data (a Group Container
// another app may still be using) is Caution regardless of how certain we are
// that it belongs to the target -- even a perfect exact bundle-id match.
func TestSharedAlwaysCaution(t *testing.T) {
	sources := []string{
		matchSourceExactBundleID,
		matchSourceKnownAppPath,
		matchSourceVendorIdentifier,
		matchSourceNameHeuristic,
	}

	for _, source := range sources {
		t.Run(source, func(t *testing.T) {
			unshared := scoreCandidate([]MatchReason{newMatchReason(source)}, false)
			shared := scoreCandidate([]MatchReason{newMatchReason(source)}, true)

			if shared.Band != ConfidenceCaution {
				t.Errorf("shared Band = %q, want %q", shared.Band, ConfidenceCaution)
			}
			if !shared.Shared {
				t.Error("Shared = false, want true")
			}
			// The score is unchanged: sharing downgrades the verdict, not the
			// strength of the evidence.
			if shared.Score != unshared.Score {
				t.Errorf("shared Score = %d, want %d", shared.Score, unshared.Score)
			}
		})
	}

	// Explicitly: the one case that would otherwise be Safe.
	perfect := scoreCandidate([]MatchReason{newMatchReason(matchSourceExactBundleID)}, true)
	if perfect.Score != 100 {
		t.Fatalf("exact bundle id Score = %d, want 100", perfect.Score)
	}
	if perfect.Band != ConfidenceCaution {
		t.Fatalf("exact bundle id on shared data = %q, want %q", perfect.Band, ConfidenceCaution)
	}
}

func TestBandForScore(t *testing.T) {
	tests := []struct {
		score  int
		shared bool
		want   ConfidenceBand
	}{
		{100, false, ConfidenceSafe},
		{90, false, ConfidenceSafe}, // inclusive lower bound for Safe
		{89, false, ConfidenceReview},
		{61, false, ConfidenceReview},
		{60, false, ConfidenceCaution}, // exclusive lower bound for Review
		{59, false, ConfidenceCaution},
		{0, false, ConfidenceCaution},
		{100, true, ConfidenceCaution},
		{0, true, ConfidenceCaution},
	}

	for _, tt := range tests {
		if got := bandForScore(tt.score, tt.shared); got != tt.want {
			t.Errorf("bandForScore(%d, %v) = %q, want %q", tt.score, tt.shared, got, tt.want)
		}
	}
}

func TestScoreCandidateTakesStrongestReason(t *testing.T) {
	reasons := []MatchReason{
		newMatchReason(matchSourceNameHeuristic),
		newMatchReason(matchSourceExactBundleID),
		newMatchReason(matchSourceVendorIdentifier),
	}

	got := scoreCandidate(reasons, false)
	if got.Score != weightExactBundleID {
		t.Errorf("Score = %d, want %d (max, not sum)", got.Score, weightExactBundleID)
	}
	if len(got.Reasons) != len(reasons) {
		t.Errorf("Reasons kept = %d, want all %d", len(got.Reasons), len(reasons))
	}
}

func TestScoreCandidateNoReasons(t *testing.T) {
	got := scoreCandidate(nil, false)
	if got.Score != 0 || got.Band != ConfidenceCaution {
		t.Errorf("scoreCandidate(nil, false) = %+v, want score 0 / %q", got, ConfidenceCaution)
	}
}

func TestExplainCandidateUnknownEntry(t *testing.T) {
	// Never scanned at all: the index is nil.
	fresh := NewAppUninstaller(AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})
	if got, ok := fresh.ExplainCandidate(FileEntry{Path: "/tmp/whatever"}); ok {
		t.Errorf("ExplainCandidate before Scan = (%+v, true), want (Confidence{}, false)", got)
	} else if got.Score != 0 || got.Band != "" || got.Shared || got.Reasons != nil {
		t.Errorf("ExplainCandidate returned %+v, want the zero Confidence", got)
	}

	// Scanned, but asked about a path that never came out of this Scan.
	home := t.TempDir()
	createDir(t, filepath.Join(home, "Library"), "Caches", "com.acme.editor")

	c := newTestUninstaller(home, AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})
	if _, err := c.Scan(t.Context(), nil); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if got, ok := c.ExplainCandidate(FileEntry{Path: "/some/other/cleaners/path"}); ok {
		t.Errorf("ExplainCandidate for a foreign path = (%+v, true), want ok=false", got)
	}
}

func TestScanPopulatesConfidenceIndex(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")

	exact := createDir(t, library, "Application Support", "com.acme.editor")
	knownPath := createDir(t, library, "Application Support", "Acme Editor")
	vendor := createDir(t, library, "Caches", "com.acme.launcher")
	nameOnly := createDir(t, library, "Logs", "acmeeditor")
	sharedGroup := createDir(t, library, "Group Containers", "group.com.acme.editor")

	c := newTestUninstaller(home, AppTarget{
		BundlePath: "/Applications/Acme Editor.app",
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	want := map[string]struct {
		band   ConfidenceBand
		source string
		shared bool
	}{
		exact:       {ConfidenceSafe, matchSourceExactBundleID, false},
		knownPath:   {ConfidenceSafe, matchSourceKnownAppPath, false},
		vendor:      {ConfidenceReview, matchSourceVendorIdentifier, false},
		nameOnly:    {ConfidenceCaution, matchSourceNameHeuristic, false},
		sharedGroup: {ConfidenceCaution, matchSourceExactBundleID, true},
	}

	if len(result.Entries) != len(want) {
		t.Fatalf("Scan produced %d entries, want %d", len(result.Entries), len(want))
	}

	for _, entry := range result.Entries {
		expected, known := want[entry.Path]
		if !known {
			t.Errorf("unexpected entry %q", entry.Path)
			continue
		}

		got, ok := c.ExplainCandidate(entry)
		if !ok {
			t.Errorf("ExplainCandidate(%q) ok = false, want every scanned entry explained", entry.Path)
			continue
		}
		if got.Band != expected.band {
			t.Errorf("entry %q Band = %q, want %q", entry.Path, got.Band, expected.band)
		}
		if got.Shared != expected.shared {
			t.Errorf("entry %q Shared = %v, want %v", entry.Path, got.Shared, expected.shared)
		}
		if len(got.Reasons) != 1 || got.Reasons[0].Source != expected.source {
			t.Errorf("entry %q Reasons = %v, want the single source %q", entry.Path, got.Reasons, expected.source)
		}
		if got.Score != weightForMatchSource(expected.source) {
			t.Errorf("entry %q Score = %d, want %d", entry.Path, got.Score, weightForMatchSource(expected.source))
		}
		if got.Reasons[0].Detail != entry.Path {
			t.Errorf("entry %q Reason detail = %q, want the path", entry.Path, got.Reasons[0].Detail)
		}
	}
}

func TestScanResetsConfidenceIndexBetweenRuns(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")
	stale := createDir(t, library, "Caches", "com.acme.editor")

	c := newTestUninstaller(home, AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})
	if _, err := c.Scan(t.Context(), nil); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if _, ok := c.ExplainCandidate(FileEntry{Path: stale}); !ok {
		t.Fatalf("expected %q to be explained after the first scan", stale)
	}

	// The leftover is gone by the time we scan again (deleted here to simulate
	// a completed cleanup, not by the read-only Scan itself).
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Scan(t.Context(), nil); err != nil {
		t.Fatalf("second Scan() error: %v", err)
	}
	if got, ok := c.ExplainCandidate(FileEntry{Path: stale}); ok {
		t.Errorf("stale confidence survived a rescan: (%+v, true)", got)
	}
}

// Regression for the unsafe zero value. Confidence{} -- what ExplainCandidate
// returns for an entry it has no evidence about -- has Band == "", which is
// none of the three band constants. A filter written as
// `switch band { case ConfidenceCaution: block; default: allow }` would
// therefore wave every unexplained item through. IsSafe/NeedsReview invert that
// default, and every caller deciding whether to delete must use them.
func TestConfidenceZeroValueIsNotSafe(t *testing.T) {
	var zero Confidence

	if zero.Band == ConfidenceSafe || zero.Band == ConfidenceReview || zero.Band == ConfidenceCaution {
		t.Fatalf("zero value Band = %q; this test exists because it is none of the constants", zero.Band)
	}
	if zero.IsSafe() {
		t.Error("Confidence{}.IsSafe() = true, want false: no evidence is not safety")
	}
	if !zero.NeedsReview() {
		t.Error("Confidence{}.NeedsReview() = false, want true")
	}
}

func TestConfidenceIsSafe(t *testing.T) {
	tests := []struct {
		name string
		band ConfidenceBand
		want bool
	}{
		{name: "safe", band: ConfidenceSafe, want: true},
		{name: "review", band: ConfidenceReview, want: false},
		{name: "caution", band: ConfidenceCaution, want: false},
		{name: "unset", band: "", want: false},
		{name: "garbage", band: ConfidenceBand("totally-fine-honest"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Confidence{Band: tt.band}
			if got := c.IsSafe(); got != tt.want {
				t.Errorf("IsSafe() = %v, want %v", got, tt.want)
			}
			if got := c.NeedsReview(); got == tt.want {
				t.Errorf("NeedsReview() = %v, want %v", got, !tt.want)
			}
		})
	}
}

// ExplainCandidate's "not explained" answer must itself be unsafe, so a caller
// that ignores the ok flag still fails closed.
func TestExplainCandidateUnknownEntryIsNotSafe(t *testing.T) {
	c := newTestUninstaller(t.TempDir(), AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})

	got, ok := c.ExplainCandidate(FileEntry{Path: "/nowhere/at/all"})
	if ok {
		t.Fatalf("ExplainCandidate() ok = true for an unknown path: %+v", got)
	}
	if got.IsSafe() {
		t.Error("the unexplained verdict reports IsSafe() = true; it must fail closed")
	}
}

// Regression for the unsynchronised confidence index: Scan used to fill the
// struct's map in place while ExplainCandidate read it, which the race detector
// flags the moment a review screen reads during a rescan. Scan now builds a
// local map and publishes it in one guarded swap.
func TestConfidenceIndexIsRaceFreeAcrossScanAndExplain(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")
	for _, name := range []string{"Caches", "Preferences", "Logs", "Containers"} {
		createDir(t, library, name, "com.acme.editor")
	}
	target := filepath.Join(library, "Caches", "com.acme.editor")

	c := newTestUninstaller(home, AppTarget{BundleID: "com.acme.editor", Name: "Acme Editor"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			if _, err := c.Scan(t.Context(), nil); err != nil {
				t.Errorf("Scan() error: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 200; i++ {
		// The result is intentionally unasserted: a concurrent reader may
		// legitimately observe either the pre- or the post-scan index. What is
		// under test is that it never observes a half-built one.
		_, _ = c.ExplainCandidate(FileEntry{Path: target})
	}
	<-done
}
