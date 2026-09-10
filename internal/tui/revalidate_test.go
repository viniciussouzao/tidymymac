package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
)

// mockRevalCleaner is an EntryRevalidator-implementing cleaner, used to
// simulate a Docker/Time Machine-shaped cleaner whose entries are not
// literal filesystem paths (and, in TestRevalidatePlan_CategoryErrorSurfaces,
// to simulate its backing store being unreachable at revalidation time).
type mockRevalCleaner struct {
	wholeDomainMockCleaner
	revalFn func(ctx context.Context, entries []cleaner.FileEntry) ([]cleaner.FileEntry, int, int, error)
}

func (m *mockRevalCleaner) RevalidateEntries(ctx context.Context, entries []cleaner.FileEntry) ([]cleaner.FileEntry, int, int, error) {
	return m.revalFn(ctx, entries)
}

func writeFile(t *testing.T, dir, name string, size int64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("writing fixture file %s: %v", path, err)
	}
	return path
}

func TestRevalidatePlan_DropsMissingFile(t *testing.T) {
	dir := t.TempDir()
	kept := writeFile(t, dir, "keep.log", 100)
	missing := filepath.Join(dir, "gone.log")

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 2,
			TotalSize:  300,
			Entries: []cleaner.FileEntry{
				{Path: kept, Size: 100, Category: cleaner.CategoryTemp},
				{Path: missing, Size: 200, Category: cleaner.CategoryTemp},
			},
		},
	}

	revalidated, delta, categoryErrs, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if len(categoryErrs) != 0 {
		t.Fatalf("categoryErrs = %v, want none", categoryErrs)
	}
	if delta.MissingFiles != 1 {
		t.Errorf("MissingFiles = %d, want 1", delta.MissingFiles)
	}
	if !delta.Material() {
		t.Error("Material() = false, want true (a file vanished)")
	}
	entries := revalidated[cleaner.CategoryTemp].Entries
	if len(entries) != 1 || entries[0].Path != kept {
		t.Fatalf("revalidated entries = %+v, want only %q", entries, kept)
	}
}

func TestRevalidatePlan_DropsTypeChangedEntry(t *testing.T) {
	dir := t.TempDir()
	// Entry claims to be a file; on disk it is now a directory.
	path := filepath.Join(dir, "was-a-file")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  10,
			Entries: []cleaner.FileEntry{
				{Path: path, Size: 10, IsDir: false, Category: cleaner.CategoryTemp},
			},
		},
	}

	revalidated, delta, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if delta.TypeChangedFiles != 1 {
		t.Errorf("TypeChangedFiles = %d, want 1", delta.TypeChangedFiles)
	}
	if !delta.Material() {
		t.Error("Material() = false, want true (type changed)")
	}
	if len(revalidated[cleaner.CategoryTemp].Entries) != 0 {
		t.Fatalf("revalidated entries = %+v, want none", revalidated[cleaner.CategoryTemp].Entries)
	}
}

func TestRevalidatePlan_DetectsNewlyProtected(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "secret.log", 50)

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	// Protected == false at scan time: the review screen never flagged it,
	// exactly like a path added to protected_paths after the scan ran.
	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  50,
			Entries: []cleaner.FileEntry{
				{Path: path, Size: 50, Category: cleaner.CategoryTemp, Protected: false},
			},
		},
	}

	cfg, err := config.New([]string{dir}, nil)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	revalidated, delta, _, err := revalidatePlan(context.Background(), registry, cfg, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if delta.NewlyProtected != 1 {
		t.Errorf("NewlyProtected = %d, want 1", delta.NewlyProtected)
	}
	if !delta.Material() {
		t.Error("Material() = false, want true (newly protected)")
	}
	// The entry is not dropped by revalidation -- only the actual delete-time
	// gate (config.StripProtected, called later in startNextClean/
	// startElevation) removes it. It must still be present here, just tagged.
	entries := revalidated[cleaner.CategoryTemp].Entries
	if len(entries) != 1 || !entries[0].Protected {
		t.Fatalf("revalidated entries = %+v, want one Protected entry", entries)
	}
}

func TestRevalidatePlan_NoChangeIsNotMaterial(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "keep.log", 100)

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  100,
			Entries: []cleaner.FileEntry{
				{Path: path, Size: 100, Category: cleaner.CategoryTemp},
			},
		},
	}

	_, delta, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if delta.Material() {
		t.Errorf("Material() = true, want false: delta = %+v", delta)
	}
}

// TestRevalidatePlan_SizeDriftAloneIsNotMaterial pins the fix for
// confirmation fatigue: a file growing between scan and confirm (the common
// case for Logs/Caches/Temp on a live system) must not, on its own, force a
// re-confirmation -- only missing/type-changed/newly-protected do.
func TestRevalidatePlan_SizeDriftAloneIsNotMaterial(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "growing.log", 100)

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	// The scan recorded 50 bytes; the file has since grown to 100.
	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  50,
			Entries:    []cleaner.FileEntry{{Path: path, Size: 50, Category: cleaner.CategoryTemp}},
		},
	}

	_, delta, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if !delta.SizeChanged {
		t.Error("SizeChanged = false, want true (the size did change)")
	}
	if delta.Material() {
		t.Errorf("Material() = true, want false: a size-only drift must not force re-confirmation: delta = %+v", delta)
	}
}

// TestRevalidatePlan_NeverAddsEntries pins the acceptance criterion that
// revalidation only ever narrows the approved plan. For the default
// (non-EntryRevalidator) path this is structural: revalidateEntries can only
// os.Stat the exact paths it was given, so a category that starts with N
// entries can never end up with more than N, only fewer.
func TestRevalidatePlan_NeverAddsEntries(t *testing.T) {
	dir := t.TempDir()
	kept := writeFile(t, dir, "keep.log", 10)
	missing := filepath.Join(dir, "gone.log")

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 2,
			TotalSize:  30,
			Entries: []cleaner.FileEntry{
				{Path: kept, Size: 10, Category: cleaner.CategoryTemp},
				{Path: missing, Size: 20, Category: cleaner.CategoryTemp},
			},
		},
	}

	revalidated, _, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	entries := revalidated[cleaner.CategoryTemp].Entries
	if len(entries) > len(results[cleaner.CategoryTemp].Entries) {
		t.Fatalf("revalidated %d entries from %d approved -- revalidation must only ever narrow the plan", len(entries), len(results[cleaner.CategoryTemp].Entries))
	}
	for _, e := range entries {
		if e.Path != kept {
			t.Fatalf("revalidated entry %q was never in the approved set", e.Path)
		}
	}
}

// TestRevalidatePlan_DropsEntryNotInApprovedSet is the guard against a
// misbehaving EntryRevalidator: PrepareScanResultForClean trusts it to only
// narrow its input, but that is a contract, not a check. attachFreshIdentity
// is the independent second guard -- an entry with no matching {category,
// path} in the originally-approved set must never reach the plan, even if
// the revalidator itself returns it.
func TestRevalidatePlan_DropsEntryNotInApprovedSet(t *testing.T) {
	dir := t.TempDir()
	approved := writeFile(t, dir, "approved.log", 10)
	injected := writeFile(t, dir, "injected.log", 10)

	registry := cleaner.NewRegistry()
	registry.Register(&mockRevalCleaner{
		wholeDomainMockCleaner: wholeDomainMockCleaner{category: "mock_cat"},
		revalFn: func(_ context.Context, entries []cleaner.FileEntry) ([]cleaner.FileEntry, int, int, error) {
			// Simulates "current store contents" instead of "surviving
			// input entries" -- a never-reviewed path slipping in.
			extra := append([]cleaner.FileEntry{}, entries...)
			extra = append(extra, cleaner.FileEntry{Path: injected, Size: 10, Category: "mock_cat"})
			return extra, 0, 0, nil
		},
	})

	results := map[cleaner.Category]*cleaner.ScanResult{
		"mock_cat": {
			Category:   "mock_cat",
			TotalFiles: 1,
			TotalSize:  10,
			Entries:    []cleaner.FileEntry{{Path: approved, Size: 10, Category: "mock_cat"}},
		},
	}

	revalidated, _, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	entries := revalidated["mock_cat"].Entries
	if len(entries) != 1 || entries[0].Path != approved {
		t.Fatalf("entries = %+v, want only the originally approved %q", entries, approved)
	}
}

func TestRevalidatePlan_OnlyTouchesReviewedCategories(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "keep.log", 10)

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryDownloads})

	// Only Temp was ever scanned/reviewed; Downloads is registered but absent
	// from results entirely.
	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  10,
			Entries:    []cleaner.FileEntry{{Path: path, Size: 10, Category: cleaner.CategoryTemp}},
		},
	}

	revalidated, _, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if _, ok := revalidated[cleaner.CategoryDownloads]; ok {
		t.Fatal("revalidatePlan touched a category that was never in the reviewed snapshot")
	}
	if _, ok := revalidated[cleaner.CategoryTemp]; !ok {
		t.Fatal("revalidatePlan dropped the one reviewed category")
	}
}

// TestRevalidatePlan_NeverTouchesRegistryWhenNothingToRevalidate pins the
// fix for a related edge case: if every category in results has 0 files
// (e.g. after a prior revalidation pass already emptied everything), the
// `selected` list passed to PrepareScanResultForClean ends up empty too --
// which, unguarded, that function reads as "every category", walking the
// whole registry (and, for Docker/Time Machine, shelling out) for categories
// the user never scanned or reviewed at all.
func TestRevalidatePlan_NeverTouchesRegistryWhenNothingToRevalidate(t *testing.T) {
	called := false
	registry := cleaner.NewRegistry()
	registry.Register(&mockRevalCleaner{
		wholeDomainMockCleaner: wholeDomainMockCleaner{category: cleaner.CategoryDownloads},
		revalFn: func(context.Context, []cleaner.FileEntry) ([]cleaner.FileEntry, int, int, error) {
			called = true
			return nil, 0, 0, nil
		},
	})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {Category: cleaner.CategoryTemp, TotalFiles: 0},
	}

	revalidated, delta, categoryErrs, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if called {
		t.Fatal("revalidatePlan touched Downloads' EntryRevalidator even though it was never in the reviewed snapshot")
	}
	if len(revalidated) != 0 || delta.Material() || len(categoryErrs) != 0 {
		t.Fatalf("revalidated = %+v, delta = %+v, categoryErrs = %v, want all empty/non-material", revalidated, delta, categoryErrs)
	}
}

func TestRevalidatePlan_CapturesFreshIdentityForSurvivingEntries(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "keep.log", 10)

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat fixture: %v", err)
	}
	wantDev, wantIno, ok := fileIdentity(info)
	if !ok {
		t.Skip("platform does not expose Dev/Ino via syscall.Stat_t")
	}

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  10,
			// A stale/bogus identity from a hypothetical earlier scan --
			// must be overwritten with a freshly captured one, never trusted
			// as-is (see attachFreshIdentity's doc comment on why the OLD
			// identity would produce false swap-detected failures for a
			// file legitimately rewritten between scan and confirm).
			Entries: []cleaner.FileEntry{
				{Path: path, Size: 10, Category: cleaner.CategoryTemp, Dev: 999999, Ino: 999999},
			},
		},
	}

	revalidated, _, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	entries := revalidated[cleaner.CategoryTemp].Entries
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1", entries)
	}
	if entries[0].Dev != wantDev || entries[0].Ino != wantIno {
		t.Fatalf("Dev/Ino = %d/%d, want the freshly Lstat'd %d/%d (not the stale scan-time values)", entries[0].Dev, entries[0].Ino, wantDev, wantIno)
	}
}

// TestRevalidatePlan_ZeroesIdentityWhenFreshLstatFails pins a security
// review finding: revalidateEntries' keep-on-ambiguous-stat-error path (see
// internal/commands/scan_input.go) can carry a stale, scan-time Dev/Ino
// forward on the entry it keeps. attachFreshIdentity's own Lstat on that
// same path fails the same way -- it must zero Dev/Ino rather than leave
// that stale identity in place, matching cleaner.fileIdentity's own
// "ok=false means unknown" contract instead of silently keeping a value
// that was never actually confirmed at this revalidation pass.
func TestRevalidatePlan_ZeroesIdentityWhenFreshLstatFails(t *testing.T) {
	dir := t.TempDir()
	regularFile := writeFile(t, dir, "not-a-dir", 1)
	// regularFile is a file, so treating it as a directory component makes
	// every Lstat on this path fail with ENOTDIR -- both the one inside
	// revalidateEntries (which then keeps the entry as-is, stale identity
	// included) and attachFreshIdentity's own, later one.
	ambiguous := filepath.Join(regularFile, "child")

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  10,
			Entries: []cleaner.FileEntry{
				{Path: ambiguous, Size: 10, Category: cleaner.CategoryTemp, Dev: 999999, Ino: 999999},
			},
		},
	}

	revalidated, _, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	entries := revalidated[cleaner.CategoryTemp].Entries
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1 (an ambiguous stat error must keep the entry, not drop it)", entries)
	}
	if entries[0].Dev != 0 || entries[0].Ino != 0 {
		t.Fatalf("Dev/Ino = %d/%d, want 0/0 (unknown, not the stale scan-time values) since this pass could not confirm identity", entries[0].Dev, entries[0].Ino)
	}
}

func TestRevalidatePlan_PreservesSizeKnown(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "keep.log", 10)

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  10,
			SizeKnown:  false,
			Entries:    []cleaner.FileEntry{{Path: path, Size: 10, Category: cleaner.CategoryTemp}},
		},
	}

	revalidated, _, _, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if revalidated[cleaner.CategoryTemp].SizeKnown {
		t.Fatal("SizeKnown flipped to true across revalidation; commands.ScanCategoryResult has no such field to have set it")
	}
}

// TestRevalidatePlan_CategoryErrorSurfaces pins the fix for a security
// finding: a category's own revalidation failure must be reported through
// the dedicated categoryErrs return, never by mutating the ScanResult's own
// Errors field -- that field also carries pre-existing, non-fatal scan-time
// errors (see internal/cleaner/caches.go and friends), and conflating the
// two used to make a routine scan warning permanently block confirming.
func TestRevalidatePlan_CategoryErrorSurfaces(t *testing.T) {
	wantErr := errors.New("daemon unreachable")
	registry := cleaner.NewRegistry()
	registry.Register(&mockRevalCleaner{
		wholeDomainMockCleaner: wholeDomainMockCleaner{category: "mock_cat"},
		revalFn: func(context.Context, []cleaner.FileEntry) ([]cleaner.FileEntry, int, int, error) {
			return nil, 0, 0, wantErr
		},
	})

	results := map[cleaner.Category]*cleaner.ScanResult{
		"mock_cat": {
			Category:   "mock_cat",
			TotalFiles: 1,
			TotalSize:  1,
			// A pre-existing, non-fatal scan-time issue that must survive
			// untouched and must NOT be conflated with the fresh
			// revalidation failure below.
			Errors:  []error{errors.New("some directory was unreadable during scan")},
			Entries: []cleaner.FileEntry{{Path: "/mock/a", Size: 1, Category: "mock_cat"}},
		},
	}

	revalidated, _, categoryErrs, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() top-level error = %v, want nil (a per-category failure is data, not a structural error)", err)
	}
	got, ok := categoryErrs["mock_cat"]
	if !ok || !errors.Is(got, wantErr) {
		t.Fatalf("categoryErrs[mock_cat] = %v, want %v", got, wantErr)
	}
	if len(revalidated["mock_cat"].Errors) != 1 {
		t.Fatalf("revalidated Errors = %v, want the one pre-existing scan-time error, untouched", revalidated["mock_cat"].Errors)
	}
}

// TestRevalidatePlan_PreExistingScanErrorDoesNotBlock pins the actual
// release-blocker the security review found: a category whose ScanResult
// already carried a non-fatal scan-time error (permission denied on one
// subdirectory, Docker's `docker info` failing while `docker ps`/`images`
// still worked, etc.) must still revalidate normally -- categoryErrs must
// stay empty when nothing went wrong DURING revalidation itself.
func TestRevalidatePlan_PreExistingScanErrorDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "keep.log", 10)

	registry := cleaner.NewRegistry()
	registry.Register(&wholeDomainMockCleaner{category: cleaner.CategoryTemp})

	results := map[cleaner.Category]*cleaner.ScanResult{
		cleaner.CategoryTemp: {
			Category:   cleaner.CategoryTemp,
			TotalFiles: 1,
			TotalSize:  10,
			Errors:     []error{errors.New("permission denied: some/other/dir")},
			Entries:    []cleaner.FileEntry{{Path: path, Size: 10, Category: cleaner.CategoryTemp}},
		},
	}

	_, _, categoryErrs, err := revalidatePlan(context.Background(), registry, &config.Config{}, results)
	if err != nil {
		t.Fatalf("revalidatePlan() error = %v", err)
	}
	if len(categoryErrs) != 0 {
		t.Fatalf("categoryErrs = %v, want none -- a pre-existing scan-time error must not be treated as a revalidation failure", categoryErrs)
	}
}
