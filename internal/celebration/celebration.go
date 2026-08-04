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

type tier uint8

const (
	tierSmall     tier = iota // >0 through 100 MiB
	tierMedium                // >100 MiB through <1 GiB
	tierLarge                 // 1 GiB through <5 GiB
	tierVeryLarge             // 5 GiB through <10 GiB
	tierHuge                  // 10 GiB and above
)

type reference struct {
	plural   string
	singular string
	bytes    int64
}

var references = []reference{
	{plural: "compressed images", singular: "compressed image", bytes: 512 * kiB},
	{plural: "high-resolution photos", singular: "high-resolution photo", bytes: 4 * miB},
	{plural: "app downloads", singular: "app download", bytes: 25 * miB},
	{plural: "music albums", singular: "music album", bytes: 100 * miB},
	{plural: "HD TV episodes", singular: "HD TV episode", bytes: 500 * miB},
	{plural: "HD movies", singular: "HD movie", bytes: 2 * giB},
	{plural: "device backups", singular: "device backup", bytes: 10 * giB},
	{plural: "large device backups", singular: "large device backup", bytes: 50 * giB},
	{plural: "media libraries", singular: "media library", bytes: 250 * giB},
}

type comparisonKind uint8

const (
	comparisonDirect comparisonKind = iota
	comparisonCount
	comparisonProportion
)

type comparison struct {
	kind      comparisonKind
	count     int64
	reference reference
	ratio     float64
}

// catalog contains two messages for each supported category and size tier.
// The comparisons are deliberately phrased as estimates because file sizes vary.
var catalog = buildCatalog()

var supportedCategories = []cleaner.Category{
	cleaner.CategoryTemp,
	cleaner.CategoryHomebrew,
	cleaner.CategoryApplicationCaches,
	cleaner.CategoryLogs,
	cleaner.CategoryDocker,
	cleaner.CategoryIOSBackups,
	cleaner.CategoryUpdates,
	cleaner.CategoryTrashBin,
	cleaner.CategoryXcode,
	cleaner.CategoryDevelopmentArtifacts,
	cleaner.CategoryProjectArtifacts,
	cleaner.CategoryTimeMachineSnapshots,
	cleaner.CategoryDownloads,
	cleaner.CategoryAppOrphans,
}

func buildCatalog() map[cleaner.Category]map[tier][]string {
	catalog := make(map[cleaner.Category]map[tier][]string, len(supportedCategories))
	for _, category := range supportedCategories {
		byTier := make(map[tier][]string, tierHuge-tierSmall+1)
		for currentTier := tierSmall; currentTier <= tierHuge; currentTier++ {
			byTier[currentTier] = []string{
				"%s was the cleanup champion, reclaiming %s%s.",
				"%s took the biggest bite out of clutter: %s%s.",
			}
		}
		catalog[category] = byTier
	}
	return catalog
}

// Message returns a randomized celebration for the successful category that
// reclaimed the most space. It returns an empty string when there is nothing
// worth celebrating. Ties deliberately keep the input order.
func Message(results []Result) string {
	winner, ok := largestSuccessfulResult(results)
	if !ok {
		return ""
	}

	currentTier := tierFor(winner.BytesFreed)
	comparison := comparisonFor(winner.BytesFreed)

	messages := catalog[winner.Category][currentTier]
	if len(messages) == 0 {
		messages = fallbackMessages()
	}

	template := messages[rand.IntN(len(messages))]
	return fmt.Sprintf(
		template,
		winner.Category.DisplayName(),
		utils.FormatBytes(winner.BytesFreed),
		comparison.suffix(),
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

func tierFor(bytes int64) tier {
	switch {
	case bytes <= 100*miB:
		return tierSmall
	case bytes < giB:
		return tierMedium
	case bytes < 5*giB:
		return tierLarge
	case bytes < 10*giB:
		return tierVeryLarge
	default:
		return tierHuge
	}
}

func comparisonFor(bytes int64) comparison {
	return comparisonForReferences(bytes, references)
}

func comparisonForReferences(bytes int64, candidates []reference) comparison {
	if bytes < miB || len(candidates) == 0 {
		return comparison{kind: comparisonDirect}
	}

	if ref, count, ok := bestCountReference(bytes, candidates); ok {
		return comparison{kind: comparisonCount, count: count, reference: ref}
	}

	if ref, ok := smallestLargerReference(bytes, candidates); ok {
		return comparison{
			kind:      comparisonProportion,
			reference: ref,
			ratio:     float64(bytes) / float64(ref.bytes),
		}
	}

	// An exceptionally large cleanup can outgrow every candidate. Keeping the
	// largest reference still gives a useful approximate comparison, even when
	// the resulting count is above the preferred 2–20 range.
	ref := candidates[len(candidates)-1]
	return comparison{
		kind:      comparisonCount,
		count:     roundedCount(bytes, ref.bytes),
		reference: ref,
	}
}

func bestCountReference(bytes int64, candidates []reference) (reference, int64, bool) {
	const idealCount int64 = 5

	var best reference
	var bestCount, bestDistance int64
	found := false
	for _, candidate := range candidates {
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

func smallestLargerReference(bytes int64, candidates []reference) (reference, bool) {
	for _, candidate := range candidates {
		if candidate.bytes > bytes {
			return candidate, true
		}
	}
	return reference{}, false
}

func roundedCount(bytes, referenceBytes int64) int64 {
	if referenceBytes <= 0 {
		return 0
	}
	return int64(math.Round(float64(bytes) / float64(referenceBytes)))
}

func (c comparison) suffix() string {
	switch c.kind {
	case comparisonCount:
		return fmt.Sprintf(" — about %d %s at ~%s each", c.count, c.reference.plural, utils.FormatBytes(c.reference.bytes))
	case comparisonProportion:
		return fmt.Sprintf(" — %s the size of a %s (~%s)", proportionLabel(c.ratio), c.reference.singular, utils.FormatBytes(c.reference.bytes))
	default:
		return ""
	}
}

func proportionLabel(ratio float64) string {
	switch {
	case ratio < 0.375:
		return "roughly one quarter"
	case ratio < 0.625:
		return "about half"
	case ratio < 0.875:
		return "about three-quarters"
	default:
		return "almost"
	}
}

func fallbackMessages() []string {
	return []string{
		"%s led the cleanup with %s%s.",
		"The biggest cleanup win was %s at %s%s.",
	}
}
