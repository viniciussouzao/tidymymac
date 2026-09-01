package elevate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
)

// fakeCleaner is a Cleaner whose Scan returns a canned entry list and whose
// Clean really deletes what it is handed, so tests can assert on the
// filesystem rather than on a mock's bookkeeping.
type fakeCleaner struct {
	category     cleaner.Category
	requiresSudo bool
	entries      []cleaner.FileEntry
	scanErr      error

	cleanedPaths []string
	// cleanCalled records the CALL, not its arguments: the empty-intersection
	// landmine is that Clean is reached at all with an empty list, which
	// cleanedPaths alone cannot distinguish from never being called.
	cleanCalled bool
}

func (f *fakeCleaner) Category() cleaner.Category { return f.category }
func (f *fakeCleaner) Name() string               { return string(f.category) }
func (f *fakeCleaner) Description() string        { return "fake cleaner" }
func (f *fakeCleaner) RequiresSudo() bool         { return f.requiresSudo }
func (f *fakeCleaner) DeletesWholeDomain() bool   { return false }

func (f *fakeCleaner) Scan(_ context.Context, _ func(cleaner.ScanProgress)) (*cleaner.ScanResult, error) {
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	var total int64
	for _, e := range f.entries {
		total += e.Size
	}
	return &cleaner.ScanResult{
		Category:   f.category,
		Entries:    f.entries,
		TotalSize:  total,
		TotalFiles: len(f.entries),
	}, nil
}

func (f *fakeCleaner) Clean(_ context.Context, entries []cleaner.FileEntry, dryRun bool, _ func(cleaner.CleanProgress)) (*cleaner.CleanResult, error) {
	f.cleanCalled = true
	result := &cleaner.CleanResult{Category: f.category, DryRun: dryRun}
	for _, e := range entries {
		f.cleanedPaths = append(f.cleanedPaths, e.Path)
		if !dryRun {
			if err := os.RemoveAll(e.Path); err != nil {
				result.Errors = append(result.Errors, err)
				continue
			}
		}
		result.FilesDeleted++
		result.BytesFreed += e.Size
	}
	return result, nil
}

func testRegistry(cleaners ...cleaner.Cleaner) *cleaner.Registry {
	r := cleaner.NewRegistry()
	for _, c := range cleaners {
		r.Register(c)
	}
	return r
}

// testEnv is a helperEnv that passes every guard, so each test only has to
// override the one fact it is about.
func testEnv(registry *cleaner.Registry) helperEnv {
	return helperEnv{
		geteuid: func() int { return 0 },
		// The plan files these tests write are owned by whoever runs the test,
		// so SUDO_UID must claim that same user for the ownership check to be
		// exercised for real rather than trivially bypassed.
		getenv:      func(string) string { return strconv.Itoa(os.Getuid()) },
		loadConfig:  func() (*config.Config, error) { return config.New(nil, nil) },
		newRegistry: func() *cleaner.Registry { return registry },
		progress:    io.Discard,
	}
}

// writeTestPlan writes plan into a private directory and returns its path,
// using the production writer so the helper's own file checks apply.
func writeTestPlan(t *testing.T, plan Plan) string {
	t.Helper()
	dir, path, err := writePlanFile(plan)
	if err != nil {
		t.Fatalf("writePlanFile() error: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return path
}

func TestRunHelperGuards(t *testing.T) {
	validPlan := Plan{
		Version: PlanSchemaVersion,
		Categories: []PlanCategory{{
			Category: cleaner.CategoryTemp,
			Entries:  []cleaner.FileEntry{{Path: "/tmp/nope"}},
		}},
	}

	tests := []struct {
		name    string
		env     func(helperEnv) helperEnv
		plan    Plan
		wantErr string
	}{
		{
			name:    "rejects a non-root process",
			env:     func(e helperEnv) helperEnv { e.geteuid = func() int { return 1000 }; return e },
			plan:    validPlan,
			wantErr: "must be started by tidymymac itself via sudo",
		},
		{
			name:    "rejects a missing SUDO_UID",
			env:     func(e helperEnv) helperEnv { e.getenv = func(string) string { return "" }; return e },
			plan:    validPlan,
			wantErr: "SUDO_UID is not set",
		},
		{
			name:    "rejects an unparsable SUDO_UID",
			env:     func(e helperEnv) helperEnv { e.getenv = func(string) string { return "root" }; return e },
			plan:    validPlan,
			wantErr: "not a valid user id",
		},
		{
			name: "propagates a config load failure",
			env: func(e helperEnv) helperEnv {
				e.loadConfig = func() (*config.Config, error) { return nil, errors.New("boom") }
				return e
			},
			plan:    validPlan,
			wantErr: "loading config",
		},
		{
			name:    "rejects a plan with a mismatched schema version",
			plan:    Plan{Version: PlanSchemaVersion + 1, Categories: validPlan.Categories},
			wantErr: "schema version",
		},
		{
			name:    "rejects a plan with no categories",
			plan:    Plan{Version: PlanSchemaVersion},
			wantErr: "no categories",
		},
		{
			name: "rejects a plan whose categories have no entries",
			plan: Plan{
				Version:    PlanSchemaVersion,
				Categories: []PlanCategory{{Category: cleaner.CategoryTemp}},
			},
			wantErr: "no entries",
		},
		{
			name: "rejects the whole plan when a category is unknown",
			plan: Plan{
				Version: PlanSchemaVersion,
				Categories: []PlanCategory{
					{Category: cleaner.CategoryTemp, Entries: []cleaner.FileEntry{{Path: "/tmp/a"}}},
					{Category: cleaner.Category("not-a-category"), Entries: []cleaner.FileEntry{{Path: "/tmp/b"}}},
				},
			},
			wantErr: "unknown category",
		},
		{
			name: "rejects the whole plan when a category does not require elevation",
			plan: Plan{
				Version: PlanSchemaVersion,
				Categories: []PlanCategory{
					{Category: cleaner.CategoryDownloads, Entries: []cleaner.FileEntry{{Path: "/tmp/b"}}},
				},
			},
			wantErr: "does not require elevation",
		},
		{
			// The elevated path must be strictly narrower than the interactive
			// one: elevation cannot become the way around disabled_categories.
			name: "rejects the whole plan when a category is disabled in the config",
			env: func(e helperEnv) helperEnv {
				e.loadConfig = func() (*config.Config, error) {
					return config.New(nil, []string{string(cleaner.CategoryTemp)})
				}
				return e
			},
			plan:    validPlan,
			wantErr: "disabled in your config",
		},
		{
			name: "rejects a plan listing the same category twice",
			plan: Plan{
				Version: PlanSchemaVersion,
				Categories: []PlanCategory{
					{Category: cleaner.CategoryTemp, Entries: []cleaner.FileEntry{{Path: "/tmp/a"}}},
					{Category: cleaner.CategoryTemp, Entries: []cleaner.FileEntry{{Path: "/tmp/b"}}},
				},
			},
			wantErr: "more than once",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The allowlist tests need a registry that also knows a non-sudo
			// category, so the rejection is about RequiresSudo rather than
			// about the category simply being absent.
			reg := testRegistry(
				&fakeCleaner{category: cleaner.CategoryTemp, requiresSudo: true},
				&fakeCleaner{category: cleaner.CategoryDownloads, requiresSudo: false},
			)

			env := testEnv(reg)
			if tt.env != nil {
				env = tt.env(env)
			}

			_, err := runHelper(context.Background(), writeTestPlan(t, tt.plan), env)
			if err == nil {
				t.Fatalf("runHelper() expected error containing %q, got none", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("runHelper() error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestRunHelperDeletesOnlyTheIntersection is the end-to-end pipeline test,
// minus actually being root: real files on disk, a real fresh scan, the real
// clean pipeline.
func TestRunHelperDeletesOnlyTheIntersection(t *testing.T) {
	dir := t.TempDir()

	approvedAndPresent := filepath.Join(dir, "approved-present")
	approvedButGone := filepath.Join(dir, "approved-gone")
	freshOnly := filepath.Join(dir, "fresh-only")
	notInAnyScan := filepath.Join(dir, "precious")

	for _, p := range []string{approvedAndPresent, freshOnly, notInAnyScan} {
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	fake := &fakeCleaner{
		category:     cleaner.CategoryTemp,
		requiresSudo: true,
		entries: []cleaner.FileEntry{
			{Path: approvedAndPresent, Size: 4},
			{Path: freshOnly, Size: 4},
		},
	}

	plan := Plan{
		Version: PlanSchemaVersion,
		Categories: []PlanCategory{{
			Category: cleaner.CategoryTemp,
			Entries: []cleaner.FileEntry{
				// A stale size the fresh scan must override.
				{Path: approvedAndPresent, Size: 999999},
				{Path: approvedButGone, Size: 10},
				// A tampered entry pointing outside the cleaner's domain.
				{Path: notInAnyScan, Size: 10},
			},
		}},
	}

	result, err := runHelper(context.Background(), writeTestPlan(t, plan), testEnv(testRegistry(fake)))
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}

	if len(fake.cleanedPaths) != 1 || fake.cleanedPaths[0] != approvedAndPresent {
		t.Fatalf("Clean received %v, want only %v", fake.cleanedPaths, []string{approvedAndPresent})
	}
	if _, err := os.Stat(approvedAndPresent); !os.IsNotExist(err) {
		t.Fatalf("approved+present file should have been deleted, stat err = %v", err)
	}
	if _, err := os.Stat(freshOnly); err != nil {
		t.Fatalf("fresh-only file must never be deleted: %v", err)
	}
	if _, err := os.Stat(notInAnyScan); err != nil {
		t.Fatalf("path outside the cleaner's domain must never be deleted: %v", err)
	}

	if len(result.Intersections) != 1 {
		t.Fatalf("got %d intersections, want 1", len(result.Intersections))
	}
	got := result.Intersections[0]
	if got.Approved != 3 || got.Matched != 1 || got.Missing != 2 {
		t.Fatalf("intersection = %+v, want approved 3, matched 1, missing 2", got)
	}
	if result.Version != ResultSchemaVersion {
		t.Fatalf("result version = %d, want %d", result.Version, ResultSchemaVersion)
	}
	// The fresh entry's size must win over the plan's inflated claim.
	if result.Clean.TotalSize != 4 {
		t.Fatalf("reclaimed %d bytes, want 4 (the fresh scan's size, not the plan's)", result.Clean.TotalSize)
	}
	if result.HasErrors() {
		t.Fatalf("unexpected errors: %+v", result.Clean.Categories)
	}
}

func TestRunHelperDryRunDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fake := &fakeCleaner{
		category:     cleaner.CategoryTemp,
		requiresSudo: true,
		entries:      []cleaner.FileEntry{{Path: path, Size: 4}},
	}
	plan := Plan{
		Version: PlanSchemaVersion,
		DryRun:  true,
		Categories: []PlanCategory{{
			Category: cleaner.CategoryTemp,
			Entries:  []cleaner.FileEntry{{Path: path, Size: 4}},
		}},
	}

	if _, err := runHelper(context.Background(), writeTestPlan(t, plan), testEnv(testRegistry(fake))); err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dry run must not delete: %v", err)
	}
}

// TestRunHelperScanFailureIsScopedToItsCategory mirrors runClean's philosophy:
// one category failing must not cancel the others.
func TestRunHelperScanFailureIsScopedToItsCategory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	broken := &fakeCleaner{category: cleaner.CategoryLogs, requiresSudo: true, scanErr: errors.New("scan exploded")}
	working := &fakeCleaner{
		category:     cleaner.CategoryTemp,
		requiresSudo: true,
		entries:      []cleaner.FileEntry{{Path: path, Size: 4}},
	}

	plan := Plan{
		Version: PlanSchemaVersion,
		Categories: []PlanCategory{
			{Category: cleaner.CategoryLogs, Entries: []cleaner.FileEntry{{Path: "/var/log/x"}}},
			{Category: cleaner.CategoryTemp, Entries: []cleaner.FileEntry{{Path: path, Size: 4}}},
		},
	}

	result, err := runHelper(context.Background(), writeTestPlan(t, plan), testEnv(testRegistry(broken, working)))
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if !result.HasErrors() {
		t.Fatalf("expected the failed category to be reported")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the healthy category should still have been cleaned, stat err = %v", err)
	}

	var logs CategoryIntersection
	for _, i := range result.Intersections {
		if i.Category == cleaner.CategoryLogs {
			logs = i
		}
	}
	if !strings.Contains(logs.ErrMsg, "scan exploded") {
		t.Fatalf("logs intersection = %+v, want the scan error recorded", logs)
	}
	if logs.Matched != 0 || logs.Missing != 1 {
		t.Fatalf("a failed scan must match nothing, got %+v", logs)
	}
}

// TestRunHelperHonorsProtectedPaths asserts the protection gate still applies
// under elevation -- which is the whole reason the intersection is fed through
// commands.RunCleanWithPreparedScanResult instead of a local delete loop.
func TestRunHelperHonorsProtectedPaths(t *testing.T) {
	dir := t.TempDir()
	protected := filepath.Join(dir, "protected")
	ordinary := filepath.Join(dir, "ordinary")
	for _, p := range []string{protected, ordinary} {
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	fake := &fakeCleaner{
		category:     cleaner.CategoryTemp,
		requiresSudo: true,
		entries: []cleaner.FileEntry{
			{Path: protected, Size: 4},
			{Path: ordinary, Size: 4},
		},
	}

	env := testEnv(testRegistry(fake))
	env.loadConfig = func() (*config.Config, error) { return config.New([]string{protected}, nil) }

	plan := Plan{
		Version: PlanSchemaVersion,
		Categories: []PlanCategory{{
			Category: cleaner.CategoryTemp,
			Entries: []cleaner.FileEntry{
				// Protected is attacker-controllable input; claiming false here
				// must not disable tagging.
				{Path: protected, Size: 4, Protected: false},
				{Path: ordinary, Size: 4},
			},
		}},
	}

	if _, err := runHelper(context.Background(), writeTestPlan(t, plan), env); err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if _, err := os.Stat(protected); err != nil {
		t.Fatalf("protected path must survive an elevated clean: %v", err)
	}
	if _, err := os.Stat(ordinary); !os.IsNotExist(err) {
		t.Fatalf("unprotected path should have been deleted, stat err = %v", err)
	}
}

// TestRunHelperNeverCleansAnEmptyIntersection is a landmine test. runClean
// calls Clean(ctx, entries, ...) unconditionally for every selected category,
// so a category whose intersection came out empty must be dropped from the
// selection rather than handed an empty list -- a cleaner that both
// RequiresSudo and DeletesWholeDomain would read that as "clear everything",
// as root, for a category the user approved nothing surviving in.
func TestRunHelperNeverCleansAnEmptyIntersection(t *testing.T) {
	dir := t.TempDir()
	survivor := filepath.Join(dir, "survivor")
	if err := os.WriteFile(survivor, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Approved entries exist, but the fresh scan no longer returns any of them.
	empty := &fakeCleaner{category: cleaner.CategoryLogs, requiresSudo: true}
	working := &fakeCleaner{
		category:     cleaner.CategoryTemp,
		requiresSudo: true,
		entries:      []cleaner.FileEntry{{Path: survivor, Size: 4}},
	}

	plan := Plan{
		Version: PlanSchemaVersion,
		Categories: []PlanCategory{
			{Category: cleaner.CategoryLogs, Entries: []cleaner.FileEntry{{Path: "/var/log/gone"}}},
			{Category: cleaner.CategoryTemp, Entries: []cleaner.FileEntry{{Path: survivor, Size: 4}}},
		},
	}

	result, err := runHelper(context.Background(), writeTestPlan(t, plan), testEnv(testRegistry(empty, working)))
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}

	if empty.cleanCalled {
		t.Fatalf("Clean must not be reached for a category with an empty intersection")
	}
	if !working.cleanCalled {
		t.Fatalf("the category that did match must still be cleaned")
	}

	// Dropped from the clean, but never dropped from the report.
	var logs CategoryIntersection
	found := false
	for _, i := range result.Intersections {
		if i.Category == cleaner.CategoryLogs {
			logs, found = i, true
		}
	}
	if !found {
		t.Fatalf("the skipped category must still be reported: %+v", result.Intersections)
	}
	if logs.Approved != 1 || logs.Matched != 0 || logs.Missing != 1 || logs.ErrMsg != "" {
		t.Fatalf("logs intersection = %+v, want approved 1, matched 0, missing 1, no error", logs)
	}
	if result.HasErrors() {
		t.Fatalf("an empty intersection is a skip, not an error: %+v", result.Clean.Categories)
	}
}

// TestRunHelperCleansNothingWhenEveryIntersectionIsEmpty guards the corollary:
// commands.resolveCleaners reads an EMPTY selection as "every category", so
// dropping the last selected category must short-circuit rather than fall
// through into a full-registry clean running as root.
func TestRunHelperCleansNothingWhenEveryIntersectionIsEmpty(t *testing.T) {
	empty := &fakeCleaner{category: cleaner.CategoryTemp, requiresSudo: true}
	bystander := &fakeCleaner{
		category:     cleaner.CategoryLogs,
		requiresSudo: true,
		entries:      []cleaner.FileEntry{{Path: "/var/log/never-approved"}},
	}

	plan := Plan{
		Version: PlanSchemaVersion,
		Categories: []PlanCategory{{
			Category: cleaner.CategoryTemp,
			Entries:  []cleaner.FileEntry{{Path: "/tmp/gone"}},
		}},
	}

	result, err := runHelper(context.Background(), writeTestPlan(t, plan), testEnv(testRegistry(empty, bystander)))
	if err != nil {
		t.Fatalf("runHelper() error: %v", err)
	}
	if empty.cleanCalled || bystander.cleanCalled {
		t.Fatalf("no category may be cleaned when nothing survived the intersection")
	}
	if len(result.Intersections) != 1 || result.Intersections[0].Matched != 0 {
		t.Fatalf("intersections = %+v, want the single approved category reported with 0 matches", result.Intersections)
	}
	if result.Clean.TotalFiles != 0 || result.Clean.TotalSize != 0 || len(result.Clean.Categories) != 0 {
		t.Fatalf("clean result = %+v, want an empty clean", result.Clean)
	}
}

func TestIntersectEntries(t *testing.T) {
	tests := []struct {
		name         string
		approved     []cleaner.FileEntry
		fresh        []cleaner.FileEntry
		wantPaths    []string
		wantApproved int
		wantMissing  int
		wantSizes    map[string]int64
	}{
		{
			name:         "matches on exact path equality",
			approved:     []cleaner.FileEntry{{Path: "/a"}, {Path: "/b"}},
			fresh:        []cleaner.FileEntry{{Path: "/a", Size: 1}, {Path: "/b", Size: 2}},
			wantPaths:    []string{"/a", "/b"},
			wantApproved: 2,
		},
		{
			name:         "counts approved entries the fresh scan did not return",
			approved:     []cleaner.FileEntry{{Path: "/a"}, {Path: "/gone"}},
			fresh:        []cleaner.FileEntry{{Path: "/a"}},
			wantPaths:    []string{"/a"},
			wantApproved: 2,
			wantMissing:  1,
		},
		{
			name:         "never includes fresh-only entries",
			approved:     []cleaner.FileEntry{{Path: "/a"}},
			fresh:        []cleaner.FileEntry{{Path: "/a"}, {Path: "/new"}},
			wantPaths:    []string{"/a"},
			wantApproved: 1,
		},
		{
			name:         "the fresh entry's size wins over the plan's",
			approved:     []cleaner.FileEntry{{Path: "/a", Size: 999}},
			fresh:        []cleaner.FileEntry{{Path: "/a", Size: 7}},
			wantPaths:    []string{"/a"},
			wantApproved: 1,
			wantSizes:    map[string]int64{"/a": 7},
		},
		{
			name:         "a repeated approved path is only acted on once",
			approved:     []cleaner.FileEntry{{Path: "/a"}, {Path: "/a"}},
			fresh:        []cleaner.FileEntry{{Path: "/a"}},
			wantPaths:    []string{"/a"},
			wantApproved: 1,
		},
		{
			name:         "a trailing-slash variant is not a match",
			approved:     []cleaner.FileEntry{{Path: "/a/"}},
			fresh:        []cleaner.FileEntry{{Path: "/a"}},
			wantPaths:    nil,
			wantApproved: 1,
			wantMissing:  1,
		},
		{
			name:         "an empty fresh scan matches nothing",
			approved:     []cleaner.FileEntry{{Path: "/a"}},
			fresh:        nil,
			wantPaths:    nil,
			wantApproved: 1,
			wantMissing:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, approved, missing := intersectEntries(tt.approved, tt.fresh, cleaner.CategoryTemp)

			var gotPaths []string
			for _, e := range matched {
				gotPaths = append(gotPaths, e.Path)
				if e.Category != cleaner.CategoryTemp {
					t.Fatalf("entry %s has category %q, want it normalized to temp", e.Path, e.Category)
				}
				if want, ok := tt.wantSizes[e.Path]; ok && e.Size != want {
					t.Fatalf("entry %s size = %d, want %d", e.Path, e.Size, want)
				}
			}
			if strings.Join(gotPaths, ",") != strings.Join(tt.wantPaths, ",") {
				t.Fatalf("matched = %v, want %v", gotPaths, tt.wantPaths)
			}
			if approved != tt.wantApproved {
				t.Fatalf("approved = %d, want %d", approved, tt.wantApproved)
			}
			if missing != tt.wantMissing {
				t.Fatalf("missing = %d, want %d", missing, tt.wantMissing)
			}
		})
	}
}

// TestIntersectEntriesDropsPlanProtectedFlag guards the one field a plan must
// never be able to assert for itself.
func TestIntersectEntriesDropsPlanProtectedFlag(t *testing.T) {
	matched, _, _ := intersectEntries(
		[]cleaner.FileEntry{{Path: "/a", Protected: true}},
		[]cleaner.FileEntry{{Path: "/a"}},
		cleaner.CategoryTemp,
	)
	if len(matched) != 1 || matched[0].Protected {
		t.Fatalf("matched = %+v, want the plan's Protected flag discarded", matched)
	}
}
