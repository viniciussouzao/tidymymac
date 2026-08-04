// Package celebration selects a short, human-friendly cleanup message.
package celebration

import (
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

const (
	kiB int64 = 1 << 10
	miB int64 = 1 << 20
	giB int64 = 1 << 30
)

// Result is the part of a category cleanup result needed to select a message.
// Failed results must be marked so they cannot be mistaken for reclaimed space.
type Result struct {
	Category   cleaner.Category
	BytesFreed int64
	Failed     bool
}

type reference struct {
	plural string
	bytes  int64
}

// references must stay sorted ascending by bytes: the exceptionally-large
// fallback in comparisonFor relies on the last entry being the largest.
var references = []reference{
	{plural: "compressed images", bytes: 512 * kiB},
	{plural: "high-resolution photos", bytes: 4 * miB},
	{plural: "app downloads", bytes: 25 * miB},
	{plural: "music albums", bytes: 100 * miB},
	{plural: "HD TV episodes", bytes: 500 * miB},
	{plural: "HD movies", bytes: 2 * giB},
	{plural: "device backups", bytes: 10 * giB},
	{plural: "large device backups", bytes: 50 * giB},
	{plural: "media libraries", bytes: 250 * giB},
}

type comparisonKind uint8

const (
	comparisonDirect comparisonKind = iota
	comparisonCount
)

type comparison struct {
	kind      comparisonKind
	count     int64
	reference reference
}

// messages are the celebration templates. Each must take exactly three verbs,
// in this order: category display name, formatted freed size, comparison
// suffix. The comparisons are deliberately phrased as estimates because file
// sizes vary.
var messages = []string{
	"%s was the cleanup champion, reclaiming %s%s.",
	"%s took the biggest bite out of clutter: %s%s.",
}

// Message returns a randomized celebration for the successful category that
// reclaimed the most space. It returns an empty string when there is nothing
// worth celebrating. Ties deliberately keep the input order.
func Message(results []Result) string {
	winner, ok := largestSuccessfulResult(results)
	if !ok {
		return ""
	}

	template := messages[rand.IntN(len(messages))]
	return fmt.Sprintf(
		template,
		winner.Category.DisplayName(),
		utils.FormatBytes(winner.BytesFreed),
		comparisonFor(winner.BytesFreed).suffix(),
	)
}

func largestSuccessfulResult(results []Result) (Result, bool) {
	var winner Result
	found := false
	for _, result := range results {
		if result.Failed || result.BytesFreed <= 0 {
			continue
		}
		if !found || result.BytesFreed > winner.BytesFreed {
			winner = result
			found = true
		}
	}
	return winner, found
}

func comparisonFor(bytes int64) comparison {
	if bytes < miB {
		return comparison{kind: comparisonDirect}
	}

	if ref, count, ok := bestCountReference(bytes); ok {
		return comparison{kind: comparisonCount, count: count, reference: ref}
	}

	// An exceptionally large cleanup can outgrow every reference. Keeping the
	// largest one still gives a useful approximate comparison, even when the
	// resulting count is above the preferred 2–20 range.
	ref := references[len(references)-1]
	return comparison{
		kind:      comparisonCount,
		count:     roundedCount(bytes, ref.bytes),
		reference: ref,
	}
}

func bestCountReference(bytes int64) (reference, int64, bool) {
	const idealCount int64 = 5

	var best reference
	var bestCount, bestDistance int64
	found := false
	for _, candidate := range references {
		count := roundedCount(bytes, candidate.bytes)
		if count < 2 || count > 20 {
			continue
		}

		distance := int64(math.Abs(float64(count - idealCount)))
		if !found || distance < bestDistance {
			best, bestCount, bestDistance, found = candidate, count, distance, true
		}
	}
	return best, bestCount, found
}

func roundedCount(bytes, referenceBytes int64) int64 {
	if referenceBytes <= 0 {
		return 0
	}
	return int64(math.Round(float64(bytes) / float64(referenceBytes)))
}

func (c comparison) suffix() string {
	if c.kind == comparisonCount {
		return fmt.Sprintf(" — about %d %s at ~%s each", c.count, c.reference.plural, utils.FormatBytes(c.reference.bytes))
	}
	return ""
}
