package explain

import (
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
)

// Registry

func TestNewRegistryIsEmpty(t *testing.T) {
	r := NewRegistry()
	if got := len(r.All()); got != 0 {
		t.Errorf("NewRegistry().All() has %d definitions, want 0", got)
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	def := ProfileDefinition{Profile: []Profile{ProfileSystemData}}
	r.Register(def)

	got, ok := r.Get(ProfileSystemData)
	if !ok {
		t.Fatal("Get(ProfileSystemData) returned false after Register")
	}
	if len(got.Profile) == 0 || got.Profile[0] != ProfileSystemData {
		t.Errorf("Get returned unexpected profile: %v", got.Profile)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get(ProfileSystemData)
	if ok {
		t.Error("Get(ProfileSystemData) returned true on empty registry")
	}
}

func TestRegistryAllPreservesOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(ProfileDefinition{Profile: []Profile{"first"}})
	r.Register(ProfileDefinition{Profile: []Profile{"second"}})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All() returned %d definitions, want 2", len(all))
	}
	if all[0].Profile[0] != "first" || all[1].Profile[0] != "second" {
		t.Error("All() did not preserve registration order")
	}
}

func TestRegistryRegisterMultipleAliases(t *testing.T) {
	r := NewRegistry()
	r.Register(ProfileDefinition{Profile: []Profile{"alias-one", "alias-two"}})

	for _, alias := range []Profile{"alias-one", "alias-two"} {
		if _, ok := r.Get(alias); !ok {
			t.Errorf("Get(%q) returned false, want true", alias)
		}
	}
}

func TestDefaultRegistryContainsSystemData(t *testing.T) {
	r := DefaultRegistry(cleaner.DefaultRegistry())
	if _, ok := r.Get(ProfileSystemData); !ok {
		t.Error("DefaultRegistry does not contain ProfileSystemData")
	}
}

// ResolveProfile

func TestResolveProfileKnown(t *testing.T) {
	def, err := ResolveProfile(ProfileSystemData, cleaner.DefaultRegistry())
	if err != nil {
		t.Fatalf("ResolveProfile() error: %v", err)
	}
	if len(def.Profile) == 0 || def.Profile[0] != ProfileSystemData {
		t.Errorf("unexpected profile: %v", def.Profile)
	}
}

func TestResolveProfileUnknown(t *testing.T) {
	_, err := ResolveProfile("nonexistent", cleaner.NewRegistry())
	if err == nil {
		t.Fatal("ResolveProfile() expected error for unknown profile, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %q does not mention the unknown profile name", err.Error())
	}
}

// newContributorDetails

func TestNewContributorDetailsNilRegistry(t *testing.T) {
	c := newContributorDetails(nil, contributorSpec{
		name:     ContributorCaches,
		category: cleaner.CategoryCaches,
	})

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !result.HasError {
		t.Error("HasError = false, want true for nil registry")
	}
}

func TestNewContributorDetailsMissingCategory(t *testing.T) {
	r := cleaner.NewRegistry()
	r.Register(stubCleaner{category: cleaner.CategoryLogs})

	c := newContributorDetails(r, contributorSpec{
		name:     ContributorCaches,
		category: cleaner.CategoryCaches,
	})

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !result.HasError {
		t.Error("HasError = false, want true when category is missing from registry")
	}
}

func TestNewContributorDetailsFound(t *testing.T) {
	r := cleaner.NewRegistry()
	r.Register(stubCleaner{
		category:   cleaner.CategoryCaches,
		scanResult: &cleaner.ScanResult{TotalSize: 2048, TotalFiles: 3},
	})

	c := newContributorDetails(r, contributorSpec{
		name:     ContributorCaches,
		category: cleaner.CategoryCaches,
	})

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.HasError {
		t.Errorf("HasError = true: %s", result.ErrorMessage)
	}
	if result.TotalSize != 2048 {
		t.Errorf("TotalSize = %d, want 2048", result.TotalSize)
	}
	if result.TotalItems != 3 {
		t.Errorf("TotalItems = %d, want 3", result.TotalItems)
	}
}
