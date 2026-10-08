package cleaner

// Match sources produced by the uninstall discovery heuristics, paired with
// the score each one is worth. The values are the user-facing evidence labels
// and are part of the JSON output contract, so they must stay stable.
const (
	// matchSourceAppBundleItself is the .app bundle of the resolved target
	// itself, not a leftover. There is no heuristic involved: the path *is*
	// the application the user asked to uninstall.
	matchSourceAppBundleItself  = "app_bundle_itself"
	matchSourceExactBundleID    = "exact_bundle_id"
	matchSourceKnownAppPath     = "known_app_path"
	matchSourceVendorIdentifier = "vendor_identifier"
	matchSourceNameHeuristic    = "name_heuristic"
)

// Evidence weights. Only the top two clear confidenceSafeThreshold, and both
// are identifier-based: the bundle itself ties with an exact bundle-id match
// at the top of the table, because both identify the target without ambiguity
// and nothing can be more certain than "this is the very bundle we resolved".
//
// Everything below them is circumstantial and deliberately lands in Review,
// never Safe:
//   - known_app_path is only "this directory's name equals the app's display
//     name" (see the discovery pass that emits matchSourceKnownAppPath). A
//     name collision with an unrelated, precious directory is entirely
//     plausible -- ~/Library/Application Support/Steam is the game library,
//     not a leftover -- so a name-of-directory match alone must never
//     authorise a permanent delete without the user seeing it. It still
//     outranks a vendor prefix: an exact path match is stronger evidence,
//     just not conclusive.
//   - vendor_identifier is a prefix shared with sibling apps
//     ("com.acme.editor" vs "com.acme.launcher").
//   - name_heuristic (a normalized name match) is intentionally the weakest
//     signal we have.
const (
	weightAppBundleItself  = 100
	weightExactBundleID    = 100
	weightKnownAppPath     = 85
	weightVendorIdentifier = 80
	weightNameHeuristic    = 60
)

// weightForMatchSource returns the score an evidence source is worth. An
// unknown source is worth nothing, so a typo degrades to Caution rather than
// silently granting confidence.
func weightForMatchSource(source string) int {
	switch source {
	case matchSourceAppBundleItself:
		return weightAppBundleItself
	case matchSourceExactBundleID:
		return weightExactBundleID
	case matchSourceKnownAppPath:
		return weightKnownAppPath
	case matchSourceVendorIdentifier:
		return weightVendorIdentifier
	case matchSourceNameHeuristic:
		return weightNameHeuristic
	default:
		return 0
	}
}

// newMatchReason builds a weighted piece of evidence for a match source.
func newMatchReason(source string) MatchReason {
	return MatchReason{Source: source, Weight: weightForMatchSource(source)}
}

// scoreCandidate turns the evidence collected for one leftover into a
// Confidence. The score is the strongest single piece of evidence, not a sum:
// two weak hints about the same path do not add up to a strong one.
func scoreCandidate(reasons []MatchReason, flags confidenceFlags) Confidence {
	score := 0
	for _, reason := range reasons {
		if reason.Weight > score {
			score = reason.Weight
		}
	}

	return Confidence{
		Score:         score,
		Band:          bandForScore(score, flags),
		Reasons:       reasons,
		Shared:        flags.Shared,
		ContainerData: flags.ContainerData,
	}
}

// ExplainCandidate reports the evidence Scan recorded for an entry. It answers
// only about entries produced by this uninstaller's own Scan; anything else
// (including a path from a different cleaner, or a call made before Scan) is
// reported as unexplained rather than as low confidence.
func (c *AppUninstaller) ExplainCandidate(entry FileEntry) (Confidence, bool) {
	return c.lookupConfidence(entry.Path)
}

// lookupConfidence reads the published confidence index under a read lock.
func (c *AppUninstaller) lookupConfidence(path string) (Confidence, bool) {
	c.confidenceMu.RLock()
	defer c.confidenceMu.RUnlock()

	if c.confidenceIndex == nil {
		return Confidence{}, false
	}
	confidence, ok := c.confidenceIndex[path]
	if !ok {
		return Confidence{}, false
	}
	return confidence, true
}

// setConfidenceIndex publishes a freshly built index in one swap. Scan builds
// its map locally and calls this exactly once, so readers never see a partially
// populated index.
func (c *AppUninstaller) setConfidenceIndex(index map[string]Confidence) {
	c.confidenceMu.Lock()
	defer c.confidenceMu.Unlock()
	c.confidenceIndex = index
}
