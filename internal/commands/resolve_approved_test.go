package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
)

func TestResolveApprovedEntries_ScansTagsAndStripsProtected(t *testing.T) {
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	c := &mockCleanRunner{
		category: "cat_a",
		name:     "Cat A",
		entries: []cleaner.FileEntry{
			{Path: "/Users/vini/Secrets/file.txt", Size: 10, Category: "cat_a"},
			{Path: "/Users/vini/Downloads/file.txt", Size: 20, Category: "cat_a"},
		},
	}
	registry := newMockRegistry2(c)

	got, err := ResolveApprovedEntries(context.Background(), registry, []string{"cat_a"}, cfg, ScanResult{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Err != nil {
		t.Fatalf("unexpected category error: %v", got[0].Err)
	}
	if len(got[0].Entries) != 1 || got[0].Entries[0].Path != "/Users/vini/Downloads/file.txt" {
		t.Fatalf("Entries = %+v, want only the non-protected file", got[0].Entries)
	}
	if c.cleanCalled {
		t.Error("ResolveApprovedEntries must never call Clean")
	}
}

func TestResolveApprovedEntries_WholeDomainWithProtectedIsSkipped(t *testing.T) {
	cfg, err := config.New([]string{"/Users/vini/Secrets"}, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	c := &mockCleanRunner{
		category:           "cat_a",
		name:               "Cat A",
		deletesWholeDomain: true,
		entries: []cleaner.FileEntry{
			{Path: "/Users/vini/Secrets/file.txt", Size: 10, Category: "cat_a"},
		},
	}
	registry := newMockRegistry2(c)

	got, err := ResolveApprovedEntries(context.Background(), registry, []string{"cat_a"}, cfg, ScanResult{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Err == nil {
		t.Fatal("expected an error for a whole-domain category with protected paths")
	}
	if len(got[0].Entries) != 0 {
		t.Fatalf("Entries = %+v, want empty when the category is skipped", got[0].Entries)
	}
}

func TestResolveApprovedEntries_ScanError(t *testing.T) {
	c := &mockCleanRunner{category: "cat_a", name: "Cat A", scanErr: errors.New("boom")}
	registry := newMockRegistry2(c)

	got, err := ResolveApprovedEntries(context.Background(), registry, []string{"cat_a"}, newTestConfig(t), ScanResult{}, false)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if len(got) != 1 || got[0].Err == nil {
		t.Fatalf("got = %+v, want a single category carrying the scan error", got)
	}
}

func TestResolveApprovedEntries_UnknownCategory(t *testing.T) {
	registry := newMockRegistry2()

	_, err := ResolveApprovedEntries(context.Background(), registry, []string{"nope"}, newTestConfig(t), ScanResult{}, false)
	if err == nil {
		t.Fatal("expected an error for an unknown category")
	}
}

func TestResolveApprovedEntries_UsePreparedScan(t *testing.T) {
	c := &mockCleanRunner{category: "cat_a", name: "Cat A"}
	registry := newMockRegistry2(c)

	prepared := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Category:   "cat_a",
				TotalFiles: 1,
				Files:      []cleaner.FileEntry{{Path: "/from/prepared/scan", Size: 5, Category: "cat_a"}},
			},
		},
	}

	got, err := ResolveApprovedEntries(context.Background(), registry, []string{"cat_a"}, newTestConfig(t), prepared, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || len(got[0].Entries) != 1 || got[0].Entries[0].Path != "/from/prepared/scan" {
		t.Fatalf("got = %+v, want the prepared scan's entry, not a fresh Scan() call", got)
	}
}

func newMockRegistry2(mocks ...*mockCleanRunner) *cleaner.Registry {
	r := cleaner.NewRegistry()
	for _, c := range mocks {
		r.Register(c)
	}
	return r
}

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.New(nil, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}
