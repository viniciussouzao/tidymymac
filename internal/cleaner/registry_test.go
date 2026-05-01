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
		CategoryLogs,
		CategoryDocker,
		CategoryIOSBackups,
		CategoryUpdates,
		CategoryDownloads,
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
