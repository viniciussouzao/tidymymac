package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// splitCleaner is a RequiresSudo cleaner that needs root only for entries
// under /sudo, standing in for Temp's /tmp vs $TMPDIR split.
type splitCleaner struct {
	mockCleanRunner
}

func (s *splitCleaner) RequiresSudo() bool { return true }

func (s *splitCleaner) NeedsSudo(entry cleaner.FileEntry) bool {
	return strings.HasPrefix(entry.Path, "/sudo/")
}

func newSplitCleaner() *splitCleaner {
	return &splitCleaner{mockCleanRunner{category: cleaner.CategoryTemp, name: "Temp Files"}}
}

func paths(entries []cleaner.FileEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func TestSplitEntriesByPrivilege(t *testing.T) {
	entries := []cleaner.FileEntry{
		{Path: "/sudo/a", Size: 1},
		{Path: "/user/b", Size: 2},
		{Path: "/sudo/deep/c", Size: 4},
		{Path: "/user/d", Size: 8},
	}

	tests := []struct {
		name       string
		c          cleaner.Cleaner
		entries    []cleaner.FileEntry
		wantSudo   []string
		wantDirect []string
	}{
		{
			name:       "splitter partitions by NeedsSudo",
			c:          newSplitCleaner(),
			entries:    entries,
			wantSudo:   []string{"/sudo/a", "/sudo/deep/c"},
			wantDirect: []string{"/user/b", "/user/d"},
		},
		{
			name:       "everything needs sudo",
			c:          newSplitCleaner(),
			entries:    []cleaner.FileEntry{{Path: "/sudo/a"}},
			wantSudo:   []string{"/sudo/a"},
			wantDirect: nil,
		},
		{
			name:       "nothing needs sudo",
			c:          newSplitCleaner(),
			entries:    []cleaner.FileEntry{{Path: "/user/b"}},
			wantSudo:   nil,
			wantDirect: []string{"/user/b"},
		},
		{
			name: "a cleaner without the interface keeps every entry on the sudo side",
			// The conservative default: no finer-grained split available.
			c:          &mockCleanRunner{category: cleaner.CategoryTemp, name: "Temp Files"},
			entries:    entries,
			wantSudo:   []string{"/sudo/a", "/user/b", "/sudo/deep/c", "/user/d"},
			wantDirect: nil,
		},
		{
			name: "a whole-domain cleaner is never split",
			// Clean would ignore the partial list and wipe the sudo half too;
			// see Cleaner.DeletesWholeDomain.
			c: func() cleaner.Cleaner {
				c := newSplitCleaner()
				c.deletesWholeDomain = true
				return c
			}(),
			entries:    entries,
			wantSudo:   []string{"/sudo/a", "/user/b", "/sudo/deep/c", "/user/d"},
			wantDirect: nil,
		},
		{
			name:       "no entries at all",
			c:          newSplitCleaner(),
			entries:    nil,
			wantSudo:   nil,
			wantDirect: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sudo, direct := SplitEntriesByPrivilege(tt.c, tt.entries)

			if got := paths(sudo); !equalStrings(got, tt.wantSudo) {
				t.Errorf("sudo entries = %v, want %v", got, tt.wantSudo)
			}
			if got := paths(direct); !equalStrings(got, tt.wantDirect) {
				t.Errorf("direct entries = %v, want %v", got, tt.wantDirect)
			}
			// The split must never invent or lose an entry.
			if len(sudo)+len(direct) != len(tt.entries) {
				t.Errorf("split produced %d entries, want %d", len(sudo)+len(direct), len(tt.entries))
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestCleanDirectly(t *testing.T) {
	t.Run("deletes the entries it was given", func(t *testing.T) {
		c := newSplitCleaner()
		entries := []cleaner.FileEntry{{Path: "/user/b", Size: 2}, {Path: "/user/d", Size: 8}}

		item := CleanDirectly(t.Context(), c, entries, false)

		if item.Category != cleaner.CategoryTemp || item.Name != cleaner.CategoryTemp.DisplayName() {
			t.Fatalf("category/name = %q/%q", item.Category, item.Name)
		}
		if item.DeletedFiles != 2 || item.DeletedSize != 10 {
			t.Fatalf("deleted = %d files / %d bytes, want 2/10", item.DeletedFiles, item.DeletedSize)
		}
		if item.Err != nil || item.ErrMsg != "" {
			t.Fatalf("unexpected error: %v / %q", item.Err, item.ErrMsg)
		}
		if got := paths(c.cleanedEntries); !equalStrings(got, []string{"/user/b", "/user/d"}) {
			t.Fatalf("Clean got %v", got)
		}
	})

	t.Run("no entries means Clean is never called", func(t *testing.T) {
		c := newSplitCleaner()

		item := CleanDirectly(t.Context(), c, nil, false)

		if c.cleanCalled {
			t.Fatal("Clean was called with an empty entry list")
		}
		if item.Category != cleaner.CategoryTemp || item.DeletedFiles != 0 {
			t.Fatalf("item = %+v, want an empty result for the category", item)
		}
	})

	t.Run("dry run reports without deleting", func(t *testing.T) {
		c := newSplitCleaner()
		c.cleanResult = &cleaner.CleanResult{Category: cleaner.CategoryTemp, DryRun: true, FilesDeleted: 1, BytesFreed: 2}

		item := CleanDirectly(t.Context(), c, []cleaner.FileEntry{{Path: "/user/b", Size: 2}}, true)

		if item.DeletedFiles != 1 || item.DeletedSize != 2 {
			t.Fatalf("deleted = %d/%d, want 1/2", item.DeletedFiles, item.DeletedSize)
		}
	})

	t.Run("a fatal Clean error is carried", func(t *testing.T) {
		c := newSplitCleaner()
		c.cleanErr = errors.New("boom")

		item := CleanDirectly(t.Context(), c, []cleaner.FileEntry{{Path: "/user/b"}}, false)

		if item.Err == nil || item.ErrMsg != "boom" {
			t.Fatalf("Err/ErrMsg = %v/%q, want boom", item.Err, item.ErrMsg)
		}
	})

	t.Run("per-item errors are non-fatal and structured", func(t *testing.T) {
		c := newSplitCleaner()
		c.cleanResult = &cleaner.CleanResult{
			Category:     cleaner.CategoryTemp,
			FilesDeleted: 1,
			BytesFreed:   2,
			Errors:       []error{&fs.PathError{Op: "remove", Path: "/user/x", Err: errors.New("permission denied")}},
		}

		item := CleanDirectly(t.Context(), c, []cleaner.FileEntry{{Path: "/user/b", Size: 2}}, false)

		if item.Err != nil {
			t.Fatalf("per-item errors must not become a fatal error: %v", item.Err)
		}
		if item.PartialErrors != 1 || len(item.PartialErrorDetails) != 1 {
			t.Fatalf("partial errors = %d, details = %v", item.PartialErrors, item.PartialErrorDetails)
		}
		if item.PartialErrorDetails[0].Path != "/user/x" {
			t.Fatalf("detail = %+v, want the failing path", item.PartialErrorDetails[0])
		}
		// Whatever was reclaimed is real and stays counted.
		if item.DeletedFiles != 1 || item.DeletedSize != 2 {
			t.Fatalf("deleted = %d/%d, want 1/2", item.DeletedFiles, item.DeletedSize)
		}
	})
}

func TestMergeCategoryResults(t *testing.T) {
	details := func(n int, prefix string) []ItemError {
		out := make([]ItemError, 0, n)
		for i := range n {
			out = append(out, ItemError{Path: fmt.Sprintf("%s/%d", prefix, i), Reason: "denied"})
		}
		return out
	}

	t.Run("both legs succeeded", func(t *testing.T) {
		direct := CleanCategoryResult{Category: cleaner.CategoryTemp, Name: "Temp Files", DeletedFiles: 2, DeletedSize: 10}
		elevated := CleanCategoryResult{Category: cleaner.CategoryTemp, Name: "Temp Files", DeletedFiles: 3, DeletedSize: 30}

		merged := MergeCategoryResults(direct, elevated)

		if merged.DeletedFiles != 5 || merged.DeletedSize != 40 {
			t.Fatalf("deleted = %d/%d, want 5/40", merged.DeletedFiles, merged.DeletedSize)
		}
		if merged.Err != nil || merged.ErrMsg != "" || merged.PartialErrors != 0 {
			t.Fatalf("merged = %+v, want no errors", merged)
		}
	})

	t.Run("elevated leg failed but the direct leg's deletions still count", func(t *testing.T) {
		elevErr := errors.New("elevation outcome unknown")
		direct := CleanCategoryResult{Category: cleaner.CategoryTemp, Name: "Temp Files", DeletedFiles: 2, DeletedSize: 10}
		elevated := CleanCategoryResult{Category: cleaner.CategoryTemp, Name: "Temp Files", Err: elevErr, ErrMsg: elevErr.Error()}

		merged := MergeCategoryResults(direct, elevated)

		if merged.DeletedFiles != 2 || merged.DeletedSize != 10 {
			t.Fatalf("deleted = %d/%d, want 2/10", merged.DeletedFiles, merged.DeletedSize)
		}
		// The elevated leg's own wording is kept, but qualified: it says
		// "nothing was deleted", which is true of that leg and false of the
		// merged row now that the direct leg reclaimed something.
		if !strings.Contains(merged.ErrMsg, elevErr.Error()) {
			t.Fatalf("ErrMsg = %q, want it to keep the elevated leg's message", merged.ErrMsg)
		}
		if !strings.Contains(merged.ErrMsg, "cleaned directly") {
			t.Fatalf("ErrMsg = %q, want it to qualify that the direct leg still cleaned files", merged.ErrMsg)
		}
		if !strings.Contains(merged.ErrMsg, "2") {
			t.Fatalf("ErrMsg = %q, want it to name the 2 files cleaned directly", merged.ErrMsg)
		}
		if merged.Err == nil || merged.Err.Error() != merged.ErrMsg {
			t.Fatalf("Err = %v, want it to carry the same qualified message as ErrMsg", merged.Err)
		}
	})

	t.Run("a failing elevated leg is left unqualified when the direct leg deleted nothing", func(t *testing.T) {
		elevErr := errors.New("elevated helper did not run; nothing was deleted")
		merged := MergeCategoryResults(
			CleanCategoryResult{Category: cleaner.CategoryTemp, Name: "Temp Files"},
			CleanCategoryResult{Category: cleaner.CategoryTemp, Name: "Temp Files", Err: elevErr, ErrMsg: elevErr.Error()},
		)

		if merged.ErrMsg != elevErr.Error() {
			t.Fatalf("ErrMsg = %q, want the elevated leg's verbatim", merged.ErrMsg)
		}
		// Unqualified messages stay wrapped, so callers can still match the
		// elevate sentinels.
		if !errors.Is(merged.Err, elevErr) {
			t.Fatalf("Err = %v, want the elevated leg's error unchanged", merged.Err)
		}
	})

	t.Run("bytes-only direct progress still qualifies the message", func(t *testing.T) {
		elevErr := errors.New("nothing was deleted")
		merged := MergeCategoryResults(
			CleanCategoryResult{Category: cleaner.CategoryTemp, DeletedSize: 10},
			CleanCategoryResult{Category: cleaner.CategoryTemp, Err: elevErr, ErrMsg: elevErr.Error()},
		)

		if !strings.Contains(merged.ErrMsg, "cleaned directly") {
			t.Fatalf("ErrMsg = %q, want it qualified", merged.ErrMsg)
		}
	})

	t.Run("the elevated leg's error wins over the direct leg's", func(t *testing.T) {
		directErr := errors.New("direct failed")
		elevErr := errors.New("elevated failed")
		merged := MergeCategoryResults(
			CleanCategoryResult{Category: cleaner.CategoryTemp, Err: directErr, ErrMsg: directErr.Error()},
			CleanCategoryResult{Category: cleaner.CategoryTemp, Err: elevErr, ErrMsg: elevErr.Error()},
		)

		if !errors.Is(merged.Err, elevErr) {
			t.Fatalf("Err = %v, want %v", merged.Err, elevErr)
		}
		if strings.Contains(merged.ErrMsg, directErr.Error()) {
			t.Fatalf("ErrMsg = %q, want only the elevated leg's message", merged.ErrMsg)
		}
	})

	t.Run("direct leg empty: category had nothing outside the sudo roots", func(t *testing.T) {
		elevated := CleanCategoryResult{Category: cleaner.CategoryTemp, Name: "Temp Files", DeletedFiles: 3, DeletedSize: 30}

		merged := MergeCategoryResults(CleanCategoryResult{}, elevated)

		if merged.Category != cleaner.CategoryTemp || merged.Name != "Temp Files" {
			t.Fatalf("category/name = %q/%q", merged.Category, merged.Name)
		}
		if merged.DeletedFiles != 3 || merged.DeletedSize != 30 {
			t.Fatalf("deleted = %d/%d, want 3/30", merged.DeletedFiles, merged.DeletedSize)
		}
	})

	t.Run("elevated leg absent: nothing needed sudo this run", func(t *testing.T) {
		directErr := errors.New("direct failed")
		direct := CleanCategoryResult{
			Category: cleaner.CategoryTemp, Name: "Temp Files",
			DeletedFiles: 2, DeletedSize: 10,
			Err: directErr, ErrMsg: directErr.Error(),
			PartialErrors: 1, PartialErrorDetails: details(1, "/user"),
		}

		merged := MergeCategoryResults(direct, CleanCategoryResult{})

		if merged.Category != cleaner.CategoryTemp || merged.Name != "Temp Files" {
			t.Fatalf("category/name = %q/%q, want them taken from the direct leg", merged.Category, merged.Name)
		}
		if merged.DeletedFiles != 2 || merged.DeletedSize != 10 {
			t.Fatalf("deleted = %d/%d, want 2/10", merged.DeletedFiles, merged.DeletedSize)
		}
		if !strings.Contains(merged.ErrMsg, directErr.Error()) {
			t.Fatalf("ErrMsg = %q, want the direct leg's message", merged.ErrMsg)
		}
		// Qualified because the direct leg deleted before it failed -- the
		// rule is about what was reclaimed, not about which leg failed.
		if !strings.Contains(merged.ErrMsg, "cleaned directly") {
			t.Fatalf("ErrMsg = %q, want it qualified", merged.ErrMsg)
		}
		if merged.PartialErrors != 1 || len(merged.PartialErrorDetails) != 1 {
			t.Fatalf("partial = %d, details = %v", merged.PartialErrors, merged.PartialErrorDetails)
		}
	})

	t.Run("partial error details are concatenated, direct leg first", func(t *testing.T) {
		merged := MergeCategoryResults(
			CleanCategoryResult{PartialErrors: 2, PartialErrorDetails: details(2, "/user")},
			CleanCategoryResult{PartialErrors: 1, PartialErrorDetails: details(1, "/sudo")},
		)

		if merged.PartialErrors != 3 {
			t.Fatalf("PartialErrors = %d, want 3", merged.PartialErrors)
		}
		got := make([]string, 0, len(merged.PartialErrorDetails))
		for _, d := range merged.PartialErrorDetails {
			got = append(got, d.Path)
		}
		if !equalStrings(got, []string{"/user/0", "/user/1", "/sudo/0"}) {
			t.Fatalf("details = %v", got)
		}
		if merged.PartialErrorsTruncated {
			t.Fatal("PartialErrorsTruncated = true, want false")
		}
	})

	t.Run("combined details are re-bounded, count stays the true total", func(t *testing.T) {
		direct := CleanCategoryResult{PartialErrors: 40, PartialErrorDetails: details(40, "/user")}
		elevated := CleanCategoryResult{PartialErrors: 30, PartialErrorDetails: details(30, "/sudo")}

		merged := MergeCategoryResults(direct, elevated)

		if len(merged.PartialErrorDetails) != MaxPartialErrorDetails {
			t.Fatalf("details = %d, want %d", len(merged.PartialErrorDetails), MaxPartialErrorDetails)
		}
		if !merged.PartialErrorsTruncated {
			t.Fatal("PartialErrorsTruncated = false, want true")
		}
		if merged.PartialErrors != 70 {
			t.Fatalf("PartialErrors = %d, want the true total 70", merged.PartialErrors)
		}
	})

	t.Run("truncation on either leg is carried through", func(t *testing.T) {
		merged := MergeCategoryResults(
			CleanCategoryResult{PartialErrors: 100, PartialErrorDetails: details(1, "/user"), PartialErrorsTruncated: true},
			CleanCategoryResult{},
		)

		if !merged.PartialErrorsTruncated {
			t.Fatal("PartialErrorsTruncated = false, want true")
		}
		if merged.PartialErrors != 100 {
			t.Fatalf("PartialErrors = %d, want 100", merged.PartialErrors)
		}
	})
}
