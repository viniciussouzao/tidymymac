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
	def := TopicDefinition{Aliases: []Topic{TopicSystemData}}
	r.Register(def)

	got, ok := r.Get(TopicSystemData)
	if !ok {
		t.Fatal("Get(TopicSystemData) returned false after Register")
	}
	if len(got.Aliases) == 0 || got.Aliases[0] != TopicSystemData {
		t.Errorf("Get returned unexpected topic: %v", got.Aliases)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get(TopicSystemData)
	if ok {
		t.Error("Get(TopicSystemData) returned true on empty registry")
	}
}

func TestRegistryAllPreservesOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(TopicDefinition{Aliases: []Topic{"first"}})
	r.Register(TopicDefinition{Aliases: []Topic{"second"}})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All() returned %d definitions, want 2", len(all))
	}
	if all[0].Aliases[0] != "first" || all[1].Aliases[0] != "second" {
		t.Error("All() did not preserve registration order")
	}
}

func TestRegistryRegisterMultipleAliases(t *testing.T) {
	r := NewRegistry()
	r.Register(TopicDefinition{Aliases: []Topic{"alias-one", "alias-two"}})

	for _, alias := range []Topic{"alias-one", "alias-two"} {
		if _, ok := r.Get(alias); !ok {
			t.Errorf("Get(%q) returned false, want true", alias)
		}
	}
}

func TestDefaultRegistryContainsSystemData(t *testing.T) {
	r := DefaultRegistry(cleaner.DefaultRegistry())
	if _, ok := r.Get(TopicSystemData); !ok {
		t.Error("DefaultRegistry does not contain TopicSystemData")
	}
}

// ResolveTopic

func TestResolveTopicKnown(t *testing.T) {
	def, err := ResolveTopic(TopicSystemData, cleaner.DefaultRegistry())
	if err != nil {
		t.Fatalf("ResolveTopic() error: %v", err)
	}
	if len(def.Aliases) == 0 || def.Aliases[0] != TopicSystemData {
		t.Errorf("unexpected topic: %v", def.Aliases)
	}
}

func TestResolveTopicUnknown(t *testing.T) {
	_, err := ResolveTopic("nonexistent", cleaner.NewRegistry())
	if err == nil {
		t.Fatal("ResolveTopic() expected error for unknown topic, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %q does not mention the unknown topic name", err.Error())
	}
}

// newContributorDetails

func TestNewContributorDetailsNilRegistry(t *testing.T) {
	c := newContributorDetails(nil, contributorSpec{
		name:     ContributorCaches,
		category: cleaner.CategoryApplicationCaches,
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
		category: cleaner.CategoryApplicationCaches,
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
		category:   cleaner.CategoryApplicationCaches,
		scanResult: &cleaner.ScanResult{TotalSize: 2048, TotalFiles: 3},
	})

	c := newContributorDetails(r, contributorSpec{
		name:     ContributorCaches,
		category: cleaner.CategoryApplicationCaches,
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
