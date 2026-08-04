package celebration

import (
	"fmt"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

func TestLargestSuccessfulResultUsesLargestAndInputOrderForTies(t *testing.T) {
	results := []Result{
		{Category: cleaner.CategoryDocker, BytesFreed: 8 * giB, Failed: true},
		{Category: cleaner.CategoryTemp, BytesFreed: 0},
		{Category: cleaner.CategoryLogs, BytesFreed: 2 * giB},
		{Category: cleaner.CategoryDownloads, BytesFreed: 2 * giB},
		{Category: cleaner.CategoryXcode, BytesFreed: giB},
	}

	winner, ok := largestSuccessfulResult(results)
	if !ok {
		t.Fatal("largestSuccessfulResult() found no winner")
	}
	if winner.Category != cleaner.CategoryLogs {
		t.Errorf("winner = %q, want first tied successful category %q", winner.Category, cleaner.CategoryLogs)
	}
}

func TestMessageReturnsEmptyWithoutSuccessfulFreedSpace(t *testing.T) {
	message := Message([]Result{
		{Category: cleaner.CategoryTemp, BytesFreed: 10 * miB, Failed: true},
		{Category: cleaner.CategoryLogs, BytesFreed: 0},
	})
	if message != "" {
		t.Errorf("Message() = %q, want empty", message)
	}
}

func TestComparisonForUsesHDTVEpisodesForOnePointOneGiB(t *testing.T) {
	comparison := comparisonFor(11 * giB / 10)
	if comparison.kind != comparisonCount {
		t.Fatalf("comparison kind = %v, want count", comparison.kind)
	}
	if comparison.reference.plural != "HD TV episodes" || comparison.count != 2 {
		t.Errorf("comparison = %+v, want about 2 HD TV episodes", comparison)
	}
	if got := comparison.suffix(); !strings.Contains(got, "about 2 HD TV episodes at ~500.0 MB each") {
		t.Errorf("suffix = %q, want an accurate HD TV episode comparison", got)
	}
}

func TestComparisonForSelectsPlausibleCountsAcrossReferenceTransitions(t *testing.T) {
	tests := []struct {
		name      string
		bytes     int64
		wantThing string
		wantCount int64
	}{
		{"one MiB", miB, "compressed images", 2},
		{"one point one GiB", 11 * giB / 10, "HD TV episodes", 2},
		{"ten GiB", 10 * giB, "HD movies", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comparison := comparisonFor(tt.bytes)
			if comparison.kind != comparisonCount {
				t.Fatalf("comparison kind = %v, want count", comparison.kind)
			}
			if comparison.reference.plural != tt.wantThing || comparison.count != tt.wantCount {
				t.Errorf("comparison = %+v, want %d %s", comparison, tt.wantCount, tt.wantThing)
			}
			if comparison.count < 2 || comparison.count > 20 {
				t.Errorf("count = %d, want the preferred range 2–20", comparison.count)
			}
		})
	}
}

func TestComparisonForUsesDirectMessageBelowOneMiB(t *testing.T) {
	comparison := comparisonFor(miB - 1)
	if comparison.kind != comparisonDirect {
		t.Errorf("comparison kind = %v, want direct", comparison.kind)
	}
	if got := comparison.suffix(); got != "" {
		t.Errorf("direct comparison suffix = %q, want empty", got)
	}
}

func TestMessageBelowOneMiBDoesNotIncludeAnAnalogy(t *testing.T) {
	message := Message([]Result{{Category: cleaner.CategoryTemp, BytesFreed: miB - 1}})
	if strings.Contains(message, " — ") || strings.Contains(message, "~") {
		t.Errorf("message = %q, want no analogy below one MiB", message)
	}
}

func TestComparisonNeverRoundsSubReferenceSpaceUpToOneItem(t *testing.T) {
	comparison := comparisonFor(11 * giB / 10)
	if comparison.kind == comparisonCount && comparison.count == 1 {
		t.Fatalf("comparison = %+v, must not describe sub-reference space as one item", comparison)
	}
}

func TestComparisonForUsesLargestReferenceForExceptionallyLargeCleanup(t *testing.T) {
	comparison := comparisonFor(6 * 1024 * giB)
	if comparison.kind != comparisonCount {
		t.Fatalf("comparison kind = %v, want count", comparison.kind)
	}
	if comparison.reference.plural != "media libraries" || comparison.count != 25 {
		t.Errorf("comparison = %+v, want 25 media libraries", comparison)
	}
}

func TestMessageNamesUnknownCategory(t *testing.T) {
	message := Message([]Result{{Category: "future-category", BytesFreed: giB}})
	if !strings.Contains(message, "future-category") {
		t.Errorf("message %q does not name the unknown category", message)
	}
	if !strings.Contains(message, "about") {
		t.Errorf("message %q does not make its comparison approximate", message)
	}
}

func TestMessageTemplatesAreWellFormed(t *testing.T) {
	const (
		category   = "Docker"
		size       = "2.0 GB"
		comparison = " — about 4 HD TV episodes at ~500.0 MB each"
	)

	for _, template := range messages {
		rendered := fmt.Sprintf(template, category, size, comparison)
		if strings.Contains(rendered, "%!") {
			t.Errorf("template %q renders verb errors: %q", template, rendered)
		}
		for _, arg := range []string{category, size, comparison} {
			if !strings.Contains(rendered, arg) {
				t.Errorf("template %q renders %q, which is missing %q", template, rendered, arg)
			}
		}
	}
}
