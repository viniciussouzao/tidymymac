package cleaner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Regression suite for the three acceptance scenarios of Smart Uninstall.
// Every case here drives the real pipeline end to end -- Scan() walks a fake
// $HOME built in t.TempDir(), and the verdicts are read back through the real
// ExplainCandidate() -- rather than calling scoreCandidate directly. The point
// is to pin the *observable* behaviour a caller (CLI filter, review screen)
// sees, so that loosening a weight, a threshold or a discovery heuristic breaks
// a test instead of silently pre-selecting a neighbour app's data for deletion.

// scanVerdicts runs the real Scan and returns, for every produced entry, the
// verdict the real ExplainCandidate reports for it. Any entry Scan produced but
// cannot explain is a bug in its own right and fails the test immediately.
func scanVerdicts(t *testing.T, c *AppUninstaller) map[string]Confidence {
	t.Helper()

	result, err := c.Scan(t.Context(), nil)
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if result == nil {
		t.Fatal("Scan() returned a nil result")
	}

	verdicts := make(map[string]Confidence, len(result.Entries))
	for _, entry := range result.Entries {
		confidence, ok := c.ExplainCandidate(entry)
		if !ok {
			t.Fatalf("ExplainCandidate(%q) reported no evidence for an entry Scan itself produced", entry.Path)
		}
		verdicts[entry.Path] = confidence
	}
	if len(verdicts) != len(result.Entries) {
		t.Fatalf("Scan() produced %d entries but only %d distinct paths", len(result.Entries), len(verdicts))
	}
	return verdicts
}

func sortedPaths(verdicts map[string]Confidence) []string {
	paths := make([]string, 0, len(verdicts))
	for path := range verdicts {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// assertVerdict pins the whole verdict for one candidate: which evidence
// source justified it, what that evidence is worth, the band it lands in, and
// -- because IsSafe is what callers actually branch on -- that IsSafe and
// NeedsReview agree with the band.
func assertVerdict(t *testing.T, verdicts map[string]Confidence, path, wantSource string, wantScore int, wantBand ConfidenceBand) {
	t.Helper()

	confidence, ok := verdicts[path]
	if !ok {
		t.Fatalf("no candidate for %q; Scan produced %v", path, sortedPaths(verdicts))
	}
	if len(confidence.Reasons) != 1 {
		t.Fatalf("candidate %q has %d reasons, want exactly 1 (discovery keeps the single strongest): %+v",
			path, len(confidence.Reasons), confidence.Reasons)
	}
	if got := confidence.Reasons[0].Source; got != wantSource {
		t.Errorf("candidate %q matched via %q, want %q", path, got, wantSource)
	}
	if confidence.Score != wantScore {
		t.Errorf("candidate %q score = %d, want %d", path, confidence.Score, wantScore)
	}
	if confidence.Band != wantBand {
		t.Errorf("candidate %q band = %q, want %q", path, confidence.Band, wantBand)
	}
	if confidence.IsSafe() != (wantBand == ConfidenceSafe) {
		t.Errorf("candidate %q IsSafe() = %v, but band is %q", path, confidence.IsSafe(), confidence.Band)
	}
	if confidence.NeedsReview() == confidence.IsSafe() {
		t.Errorf("candidate %q: NeedsReview() = %v and IsSafe() = %v must be opposites",
			path, confidence.NeedsReview(), confidence.IsSafe())
	}
}

// assertNotACandidate proves a path belonging to a neighbouring application was
// never picked up at all -- the strongest possible outcome, stricter than
// "picked up but not Safe".
func assertNotACandidate(t *testing.T, verdicts map[string]Confidence, path string) {
	t.Helper()

	if confidence, ok := verdicts[path]; ok {
		t.Errorf("%q belongs to another application but Scan claimed it as a candidate (%q, score %d)",
			path, confidence.Band, confidence.Score)
	}
}

// assertNeverSafe is the acceptance criterion itself, asserted independently of
// the exact band so it keeps meaning if the band ever legitimately moves
// between Caution and Review.
func assertNeverSafe(t *testing.T, verdicts map[string]Confidence, path string) {
	t.Helper()

	confidence, ok := verdicts[path]
	if !ok {
		t.Fatalf("no candidate for %q; Scan produced %v", path, sortedPaths(verdicts))
	}
	if confidence.IsSafe() {
		t.Errorf("%q was scored Safe (score %d, reasons %+v); a neighbouring app's data must never be auto-selected",
			path, confidence.Score, confidence.Reasons)
	}
}

// ---------------------------------------------------------------------------
// Scenario 1: apps with similar names.
//
// A bare name match is worth 60, and the Review threshold is *exclusive*
// (score > 60), so a name-only match lands in Caution and can never be
// pre-selected. This test pins both halves of that: a genuinely different app
// whose name merely resembles the target is not claimed at all, and a path that
// does match by normalized name only stops at Caution.
// ---------------------------------------------------------------------------

func TestRegression_SimilarAppNames_NeverSafe(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")
	apps := createDir(t, home, "Applications")

	bundle := createTestApp(t, apps, "Slack.app", "com.tinyspeck.slackmacgap")

	// Genuinely the target's own data: exact bundle id and exact app name.
	ownSupport := createDir(t, library, "Application Support", "com.tinyspeck.slackmacgap")
	ownNamed := createDir(t, library, "Application Support", "Slack")

	// A different application that merely looks alike. Nothing here is Slack's.
	neighbourSupport := createDir(t, library, "Application Support", "SlackBot")
	neighbourCache := createDir(t, library, "Caches", "Slack Bot")
	neighbourPrefs := createSparseFile(t, filepath.Join(library, "Preferences"), "com.slackbot.app.plist", 128)
	neighbourHelper := createDir(t, library, "Logs", "Slack Helper")

	// A path whose *normalized* name collides with the target ("slack!" ->
	// "slack") but which is not an exact name match: this is the weakest
	// evidence we have, and it must stop at Caution.
	nameOnly := createDir(t, library, "Caches", "slack!")

	c := newTestUninstaller(home, AppTarget{
		BundlePath: bundle,
		BundleID:   "com.tinyspeck.slackmacgap",
		Name:       "Slack",
	})

	verdicts := scanVerdicts(t, c)

	t.Run("target's own data still scores normally", func(t *testing.T) {
		assertVerdict(t, verdicts, bundle, matchSourceAppBundleItself, weightAppBundleItself, ConfidenceSafe)
		assertVerdict(t, verdicts, ownSupport, matchSourceExactBundleID, weightExactBundleID, ConfidenceSafe)
		// Only identifier-grade evidence reaches Safe. A directory merely
		// *named* after the app is Review: the same match would fire on
		// ~/Library/Application Support/Steam, so a human sees it first.
		assertVerdict(t, verdicts, ownNamed, matchSourceKnownAppPath, weightKnownAppPath, ConfidenceReview)
		assertNeverSafe(t, verdicts, ownNamed)
	})

	t.Run("similar-named neighbour is not claimed at all", func(t *testing.T) {
		for _, path := range []string{neighbourSupport, neighbourCache, neighbourPrefs, neighbourHelper} {
			assertNotACandidate(t, verdicts, path)
		}
	})

	t.Run("name-only match stops at Caution", func(t *testing.T) {
		assertVerdict(t, verdicts, nameOnly, matchSourceNameHeuristic, weightNameHeuristic, ConfidenceCaution)
		assertNeverSafe(t, verdicts, nameOnly)
		// Pins the exclusive Review threshold: 60 must not clear it.
		if weightNameHeuristic > confidenceReviewThreshold {
			t.Errorf("a bare name match (%d) now clears the Review threshold (%d); it must stay in Caution",
				weightNameHeuristic, confidenceReviewThreshold)
		}
	})

	t.Run("the reverse direction is symmetric", func(t *testing.T) {
		// Uninstalling "Slack Bot" must not claim plain "Slack"'s data either.
		botBundle := createTestApp(t, apps, "Slack Bot.app", "com.slackbot.app")
		bot := newTestUninstaller(home, AppTarget{
			BundlePath: botBundle,
			BundleID:   "com.slackbot.app",
			Name:       "Slack Bot",
		})

		botVerdicts := scanVerdicts(t, bot)
		assertVerdict(t, botVerdicts, neighbourPrefs, matchSourceExactBundleID, weightExactBundleID, ConfidenceSafe)
		for _, path := range []string{bundle, ownSupport, ownNamed, nameOnly} {
			assertNotACandidate(t, botVerdicts, path)
		}
	})
}

// ---------------------------------------------------------------------------
// Scenario 2: same vendor, different applications.
//
// "com.acme.editor" and "com.acme.editor.beta" share a vendor prefix and
// nothing else. The vendor heuristic is worth 80, which clears Review but not
// Safe -- a human must confirm before a sibling product's data is removed.
// ---------------------------------------------------------------------------

func TestRegression_SameVendorDifferentApp_ReviewNotSafe(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")
	apps := createDir(t, home, "Applications")

	bundle := createTestApp(t, apps, "Acme Editor.app", "com.acme.editor")

	own := createDir(t, library, "Application Support", "com.acme.editor")
	ownPrefs := createSparseFile(t, filepath.Join(library, "Preferences"), "com.acme.editor.plist", 64)

	// Same vendor, different product. Circumstantial evidence only.
	siblingSupport := createDir(t, library, "Application Support", "com.acme.editor.beta")
	siblingCache := createDir(t, library, "Caches", "com.acme.launcher")
	siblingPrefs := createSparseFile(t, filepath.Join(library, "Preferences"), "com.acme.editor.beta.plist", 64)
	siblingAgent := createSparseFile(t, filepath.Join(library, "LaunchAgents"), "com.acme.notes.plist", 64)

	// A different vendor with a confusingly similar product name: no match.
	otherVendor := createDir(t, library, "Application Support", "com.rival.editor")

	c := newTestUninstaller(home, AppTarget{
		BundlePath: bundle,
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})

	verdicts := scanVerdicts(t, c)

	t.Run("exact identifier is Safe", func(t *testing.T) {
		assertVerdict(t, verdicts, own, matchSourceExactBundleID, weightExactBundleID, ConfidenceSafe)
		assertVerdict(t, verdicts, ownPrefs, matchSourceExactBundleID, weightExactBundleID, ConfidenceSafe)
	})

	t.Run("vendor sibling is Review, never Safe", func(t *testing.T) {
		siblings := []struct {
			name string
			path string
		}{
			{"support dir of a sibling product", siblingSupport},
			{"cache dir of a sibling product", siblingCache},
			{"preferences plist of a sibling product", siblingPrefs},
			{"launch agent of a sibling product", siblingAgent},
		}
		for _, tc := range siblings {
			t.Run(tc.name, func(t *testing.T) {
				assertVerdict(t, verdicts, tc.path, matchSourceVendorIdentifier, weightVendorIdentifier, ConfidenceReview)
				assertNeverSafe(t, verdicts, tc.path)
			})
		}
		// Pins the gap between the vendor weight and the Safe bar. If these
		// ever meet, every sibling product becomes auto-deletable.
		if weightVendorIdentifier >= confidenceSafeThreshold {
			t.Errorf("vendor evidence (%d) now reaches the Safe threshold (%d); sibling apps would be auto-selected",
				weightVendorIdentifier, confidenceSafeThreshold)
		}
	})

	t.Run("a different vendor is not claimed at all", func(t *testing.T) {
		assertNotACandidate(t, verdicts, otherVendor)
	})

	t.Run("the target's own sandbox container is capped at Review", func(t *testing.T) {
		// Same perfect 100 as the exact-id match above, but container data can
		// hold user documents, so it must not be Safe.
		container := createDir(t, library, "Containers", "com.acme.editor")
		containerVerdicts := scanVerdicts(t, c)

		assertVerdict(t, containerVerdicts, container, matchSourceExactBundleID, weightExactBundleID, ConfidenceReview)
		if !containerVerdicts[container].ContainerData {
			t.Error("container candidate is not flagged as ContainerData")
		}
	})
}

// ---------------------------------------------------------------------------
// Scenario 3: a Group Container shared between two applications.
//
// Shared is a property of the *path* -- anything under
// ~/Library/Group Containers/ is shared by construction -- and not of whether
// the other app happens to still be installed. The fixture deliberately leaves
// only one of the two apps on disk to prove the verdict does not depend on
// that.
// ---------------------------------------------------------------------------

func TestRegression_SharedGroupContainer_AlwaysCaution(t *testing.T) {
	home := t.TempDir()
	library := filepath.Join(home, "Library")
	apps := createDir(t, home, "Applications")

	// Only Acme Editor is still installed. Acme Notes -- the other member of
	// the group -- was uninstalled earlier: no bundle for it exists anywhere.
	bundle := createTestApp(t, apps, "Acme Editor.app", "com.acme.editor")
	if _, err := os.Stat(filepath.Join(apps, "Acme Notes.app")); !os.IsNotExist(err) {
		t.Fatalf("fixture invalid: Acme Notes.app must not exist, stat err = %v", err)
	}

	groupVendor := createDir(t, library, "Group Containers", "group.com.acme.suite")
	groupExact := createDir(t, library, "Group Containers", "group.com.acme.editor")
	groupNamed := createDir(t, library, "Group Containers", "Acme Editor")
	unrelatedGroup := createDir(t, library, "Group Containers", "group.com.rival.suite")

	c := newTestUninstaller(home, AppTarget{
		BundlePath: bundle,
		BundleID:   "com.acme.editor",
		Name:       "Acme Editor",
	})

	verdicts := scanVerdicts(t, c)

	cases := []struct {
		name       string
		path       string
		wantSource string
		wantScore  int
	}{
		{
			name:       "vendor-level group container",
			path:       groupVendor,
			wantSource: matchSourceVendorIdentifier,
			wantScore:  weightVendorIdentifier,
		},
		{
			// Perfect evidence, and still Caution: Shared overrides the score.
			name:       "group container named after the target's own bundle id",
			path:       groupExact,
			wantSource: matchSourceExactBundleID,
			wantScore:  weightExactBundleID,
		},
		{
			name:       "group container named after the app",
			path:       groupNamed,
			wantSource: matchSourceKnownAppPath,
			wantScore:  weightKnownAppPath,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertVerdict(t, verdicts, tc.path, tc.wantSource, tc.wantScore, ConfidenceCaution)
			assertNeverSafe(t, verdicts, tc.path)
			if !verdicts[tc.path].Shared {
				t.Errorf("%q is under Group Containers but Shared = false", tc.path)
			}
		})
	}

	t.Run("another vendor's group container is not claimed", func(t *testing.T) {
		assertNotACandidate(t, verdicts, unrelatedGroup)
	})

	t.Run("Clean refuses shared entries even in a dry run", func(t *testing.T) {
		// Defence in depth: the Caution verdict is advice callers are expected
		// to honour, but Clean must refuse regardless. dryRun keeps the whole
		// call side-effect free; the assertions below prove nothing moved.
		entries := []FileEntry{
			{Path: groupVendor, IsDir: true, Size: 4096, Category: CategoryAppUninstall},
			{Path: groupExact, IsDir: true, Size: 4096, Category: CategoryAppUninstall},
		}

		result, err := c.Clean(t.Context(), entries, true, nil)
		if err != nil {
			t.Fatalf("Clean() error: %v", err)
		}
		if result.FilesDeleted != 0 {
			t.Errorf("Clean() reported %d files deleted, want 0 (all entries are shared)", result.FilesDeleted)
		}
		if len(result.Errors) != len(entries) {
			t.Errorf("Clean() returned %d errors, want one refusal per shared entry (%d): %v",
				len(result.Errors), len(entries), result.Errors)
		}
		for _, path := range []string{groupVendor, groupExact, groupNamed, unrelatedGroup, bundle} {
			if _, statErr := os.Stat(path); statErr != nil {
				t.Errorf("a dry run touched %q: %v", path, statErr)
			}
		}
	})
}
