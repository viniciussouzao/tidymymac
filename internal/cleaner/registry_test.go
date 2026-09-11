package cleaner

import "testing"

func TestNewRegistryIsEmpty(t *testing.T) {
	r := NewRegistry()
	if got := len(r.All()); got != 0 {
		t.Errorf("NewRegistry().All() has %d cleaners, want 0", got)
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	c := NewTempCleaner()
	r.Register(c)

	got, ok := r.Get(CategoryTemp)
	if !ok {
		t.Fatal("Get(CategoryTemp) returned false after Register")
	}
	if got.Name() != c.Name() {
		t.Errorf("Get(CategoryTemp).Name() = %q, want %q", got.Name(), c.Name())
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get(CategoryTemp)
	if ok {
		t.Error("Get(CategoryTemp) returned true on empty registry")
	}
}

func TestRegistryAllPreservesOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(NewLogsCleaner())
	r.Register(NewTempCleaner())
	r.Register(NewCachesCleaner())

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d cleaners, want 3", len(all))
	}

	want := []Category{CategoryLogs, CategoryTemp, CategoryApplicationCaches}
	for i, c := range all {
		if c.Category() != want[i] {
			t.Errorf("All()[%d].Category() = %q, want %q", i, c.Category(), want[i])
		}
	}
}

func TestRegistryRegisterOverwritesByID(t *testing.T) {
	r := NewRegistry()
	c1 := NewTempCleaner()
	c2 := NewTempCleaner()
	r.Register(c1)
	r.Register(c2)

	// byID should point to the last registered
	got, ok := r.Get(CategoryTemp)
	if !ok {
		t.Fatal("Get(CategoryTemp) returned false")
	}
	if got != c2 {
		t.Error("Get(CategoryTemp) did not return the last registered cleaner")
	}

	// All() keeps both (append behavior)
	if len(r.All()) != 2 {
		t.Errorf("All() returned %d, want 2 (both registered)", len(r.All()))
	}
}

func TestDefaultRegistryHasAllCleaners(t *testing.T) {
	r := DefaultRegistry()

	expected := []Category{
		CategoryTemp,
		CategoryHomebrew,
		CategoryApplicationCaches,
		CategoryDevelopmentArtifacts,
		CategoryProjectArtifacts,
		CategoryLogs,
		CategoryDocker,
		CategoryIOSBackups,
		CategoryUpdates,
		CategoryDownloads,
		CategoryAppOrphans,
		CategoryTrashBin,
		CategoryXcode,
		CategoryTimeMachineSnapshots,
	}

	all := r.All()
	if len(all) != len(expected) {
		t.Fatalf("DefaultRegistry().All() has %d cleaners, want %d", len(all), len(expected))
	}

	for i, want := range expected {
		if all[i].Category() != want {
			t.Errorf("DefaultRegistry().All()[%d].Category() = %q, want %q", i, all[i].Category(), want)
		}
	}

	// Also verify Get works for each
	for _, cat := range expected {
		if _, ok := r.Get(cat); !ok {
			t.Errorf("DefaultRegistry().Get(%q) returned false", cat)
		}
	}
}

// TestNoCleanerIsBothSudoAndWholeDomain is a conformance test that internal/
// elevate depends on.
//
// The elevated helper deletes only the intersection of the approved plan and a
// fresh root scan, and it hands that intersection to Clean. A cleaner that
// DeletesWholeDomain ignores the entry list it is given and clears its entire
// domain -- which under elevation means clearing it AS ROOT, for a domain the
// intersection may have narrowed to a handful of entries, or to none.
// internal/elevate is designed on the assumption that this combination does not
// exist.
//
// If this test ever fails, do NOT relax it: revisit internal/elevate first and
// decide how a whole-domain cleaner may (or may not) be elevated at all.
func TestNoCleanerIsBothSudoAndWholeDomain(t *testing.T) {
	for _, c := range DefaultRegistry().All() {
		if c.RequiresSudo() && c.DeletesWholeDomain() {
			t.Errorf("cleaner %q is both RequiresSudo and DeletesWholeDomain; internal/elevate assumes no cleaner is, see its Elevation Model", c.Category())
		}
	}
}

// TestPrivilegeSplittersAreSudoAndNotWholeDomain is the generic half of the
// PrivilegeSplitter contract -- the part that can be checked without knowing
// any cleaner's internals.
//
// The subset invariant (a splitter's sudo roots must be a subset of its own
// scan roots) cannot be asserted generically: the interface deliberately
// exposes only a classification, not the roots behind it, so each
// implementation carries its own test (see
// TestTempCleanerSudoRootsAreSubsetOfRoots).
func TestPrivilegeSplittersAreSudoAndNotWholeDomain(t *testing.T) {
	for _, c := range DefaultRegistry().All() {
		splitter, ok := c.(PrivilegeSplitter)
		if !ok {
			continue
		}

		// A split only ever decides which entries reach the elevated helper.
		// On a cleaner that never elevates, NeedsSudo would be dead code that
		// looks like a live safety decision.
		if !c.RequiresSudo() {
			t.Errorf("cleaner %q implements PrivilegeSplitter but does not RequiresSudo", c.Category())
		}
		// Splitting produces a partial entry list, which a whole-domain
		// cleaner must never be handed; commands.SplitEntriesByPrivilege
		// refuses to split such a cleaner, so implementing both is at best
		// misleading. See TestNoCleanerIsBothSudoAndWholeDomain.
		if c.DeletesWholeDomain() {
			t.Errorf("cleaner %q implements PrivilegeSplitter and DeletesWholeDomain; a whole-domain cleaner cannot take a partial entry list", c.Category())
		}

		// Nothing outside a real domain is ever elevated: an empty path is
		// not a location, and "/" is never a scan root (newRootedRemover
		// drops it outright).
		for _, path := range []string{"", "/", "relative/path"} {
			if splitter.NeedsSudo(FileEntry{Path: path, Category: c.Category()}) {
				t.Errorf("cleaner %q: NeedsSudo(%q) = true, want false", c.Category(), path)
			}
		}
	}
}

// TestItemSelectableCleaners pins which cleaners opt into the review
// screen's per-item selection (see the "Melhorar tela de review" card) and
// guards the interface's own invariant: a whole-domain cleaner cannot honor
// a filtered entry list, so it must never also claim per-item selection.
func TestItemSelectableCleaners(t *testing.T) {
	want := map[Category]bool{
		CategoryDownloads:            true,
		CategoryDocker:               true,
		CategoryIOSBackups:           true,
		CategoryTimeMachineSnapshots: true,
	}

	for _, c := range DefaultRegistry().All() {
		selectable, ok := c.(ItemSelectable)
		gotSupports := ok && selectable.SupportsItemSelection()
		if gotSupports != want[c.Category()] {
			t.Errorf("cleaner %q: SupportsItemSelection() = %v, want %v", c.Category(), gotSupports, want[c.Category()])
		}
		if gotSupports && c.DeletesWholeDomain() {
			t.Errorf("cleaner %q supports item selection but also DeletesWholeDomain; it cannot honor a filtered entry list", c.Category())
		}
	}
}
