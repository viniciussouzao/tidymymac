package cleaner

// ConfidenceBand is the coarse, user-facing verdict derived from a Confidence
// score. It exists so callers (CLI filters, TUI badges) never have to know the
// numeric thresholds.
type ConfidenceBand string

const (
	// ConfidenceSafe means the evidence identifies the item unambiguously; it
	// is the only band pre-selected for deletion without human review.
	ConfidenceSafe ConfidenceBand = "safe"
	// ConfidenceReview means the evidence is strong but circumstantial (same
	// vendor, sibling app); a human should confirm.
	ConfidenceReview ConfidenceBand = "review"
	// ConfidenceCaution means the evidence is weak (name heuristics only) or
	// the data is shared with other applications.
	ConfidenceCaution ConfidenceBand = "caution"
)

// MatchReason is a single piece of evidence tying an item to the thing being
// removed. Weight is the score that evidence source is worth (0-100); the
// per-source weight table lives with the cleaner that produces the reasons.
type MatchReason struct {
	Source string
	Weight int
	Detail string
}

// Confidence is the scored verdict for one entry: how sure we are that it
// belongs to the target, and why.
type Confidence struct {
	Score   int // 0-100
	Band    ConfidenceBand
	Reasons []MatchReason
	Shared  bool
}

// IsSafe reports whether this verdict clears the bar for deletion without
// human review.
//
// Always decide with this method, never with a `switch c.Band` whose default
// branch permits. The zero value Confidence{} -- what ExplainCandidate returns
// for an entry it has no evidence about -- has Band == "", which is none of the
// three constants, so a "block Caution, allow the rest" switch would wave
// unexplained items straight through. IsSafe inverts that default: anything
// that is not explicitly ConfidenceSafe, the zero value included, is not safe.
func (c Confidence) IsSafe() bool { return c.Band == ConfidenceSafe }

// NeedsReview reports whether a human must look at this verdict before it is
// acted on. It is the complement of IsSafe, spelled out so callers do not have
// to write the negation themselves and accidentally get the default wrong.
func (c Confidence) NeedsReview() bool { return !c.IsSafe() }

const (
	// confidenceSafeThreshold is the inclusive lower bound for Safe: a score
	// of exactly 90 is Safe.
	confidenceSafeThreshold = 90
	// confidenceReviewThreshold is the *exclusive* lower bound for Review: a
	// score must beat it, not just reach it. This is deliberate. The weakest
	// evidence source, a bare name heuristic, is worth exactly 60, and a name
	// match alone must never be enough to imply "we know this belongs to the
	// app" -- it lands in Caution, where a human has to promote it.
	confidenceReviewThreshold = 60
)

// bandForScore maps a score to a band. Hard rule, no exception: shared data
// (a Group Container used by more than one app) is always Caution, even with a
// perfect 100 from an exact bundle-id match -- deleting it would take data away
// from an application the user never asked to touch.
func bandForScore(score int, shared bool) ConfidenceBand {
	if shared {
		return ConfidenceCaution
	}
	switch {
	case score >= confidenceSafeThreshold:
		return ConfidenceSafe
	case score > confidenceReviewThreshold:
		return ConfidenceReview
	default:
		return ConfidenceCaution
	}
}
