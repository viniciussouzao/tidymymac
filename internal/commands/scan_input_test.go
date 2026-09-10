package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/internal/config"
)

func TestLoadScanResult_DecodesJSON(t *testing.T) {
	input := `{
	  "scanned_at": "2026-03-29T12:00:00Z",
	  "categories": [
	    {
	      "category": "temp_files",
	      "name": "Temp Files",
	      "total_files": 1,
	      "total_size_bytes": 123,
	      "files": [
	        {
	          "Path": "/tmp/a",
	          "Size": 123,
	          "IsDir": false,
	          "ModTime": "2026-03-29T12:00:00Z",
	          "Category": "temp_files"
	        }
	      ]
	    }
	  ]
	}`

	result, err := LoadScanResult(strings.NewReader(input))
	if err != nil {
		t.Fatalf("LoadScanResult() error: %v", err)
	}
	if len(result.Categories) != 1 {
		t.Fatalf("got %d categories, want 1", len(result.Categories))
	}
	if result.Categories[0].Category != cleaner.Category("temp_files") {
		t.Errorf("Category = %q, want %q", result.Categories[0].Category, cleaner.Category("temp_files"))
	}
	if len(result.Categories[0].Files) != 1 {
		t.Fatalf("got %d files, want 1", len(result.Categories[0].Files))
	}
	if result.Categories[0].Files[0].Path != "/tmp/a" {
		t.Errorf("Path = %q, want /tmp/a", result.Categories[0].Files[0].Path)
	}
}

func TestRevalidateEntries_PreservesResourceKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries := []cleaner.FileEntry{
		{Path: path, Size: 5, Category: cleaner.Category("temp_files"), ResourceKind: cleaner.DockerResourceKindImageDangling},
	}

	revalidated, missing, typeChanged := revalidateEntries(entries)
	if missing != 0 || typeChanged != 0 {
		t.Fatalf("missing = %d, typeChanged = %d, want 0/0", missing, typeChanged)
	}
	if len(revalidated) != 1 {
		t.Fatalf("len = %d, want 1", len(revalidated))
	}
	if revalidated[0].ResourceKind != cleaner.DockerResourceKindImageDangling {
		t.Errorf("ResourceKind = %q, want %q", revalidated[0].ResourceKind, cleaner.DockerResourceKindImageDangling)
	}
}

// TestRevalidateEntries_KeepsEntryOnAmbiguousStatError pins the fix for a
// security finding: an os.Stat error that is NOT "does not exist" (here,
// ENOTDIR from treating a regular file as a directory component -- the
// portable way to provoke a non-NotExist stat error without relying on
// permission bits) must not be treated as "missing". Doing so would silently
// drop the entry from the plan, and for one protected_paths already tagged,
// dropping it removes the only signal a DeletesWholeDomain cleaner's skip
// check depends on -- see config.CountProtected's callers in cmd/clean.go
// and internal/tui/app.go.
func TestRevalidateEntries_KeepsEntryOnAmbiguousStatError(t *testing.T) {
	dir := t.TempDir()
	regularFile := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(regularFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// regularFile is a file, so treating it as a directory component makes
	// every os.Stat on this path fail with ENOTDIR, not ENOENT.
	ambiguous := filepath.Join(regularFile, "child")

	original := cleaner.FileEntry{Path: ambiguous, Size: 999, Category: cleaner.Category("temp_files")}
	revalidated, missing, typeChanged := revalidateEntries([]cleaner.FileEntry{original})

	if missing != 0 || typeChanged != 0 {
		t.Fatalf("missing = %d, typeChanged = %d, want 0/0 (the entry must be kept, not counted as missing)", missing, typeChanged)
	}
	if len(revalidated) != 1 || revalidated[0] != original {
		t.Fatalf("revalidated = %+v, want the original entry kept unchanged", revalidated)
	}
}

// TestRevalidateEntries_PreservesDirectorySize pins the fix for a security
// finding: a directory entry's own Lstat size is the filesystem's tiny
// inode/metadata size, not the recursive size of what it contains (every
// cleaner that reports a directory entry records the recursive size it
// measured while walking it). Overwriting Size with info.Size() here would
// silently collapse the reported reclaimable size to near-zero -- on the
// TUI's review/dry-run screens, a lie about how much would be freed --
// while Clean still deletes the full tree.
func TestRevalidateEntries_PreservesDirectorySize(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "big-dir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "file.bin"), make([]byte, 5_000_000), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The scan-time recursive size, as every directory-reporting cleaner
	// would have recorded it -- deliberately much larger than whatever a
	// bare Lstat on the directory itself would report.
	const recursiveSize = 5_000_000
	entries := []cleaner.FileEntry{
		{Path: subdir, Size: recursiveSize, IsDir: true, Category: cleaner.Category("temp_files")},
	}

	revalidated, missing, typeChanged := revalidateEntries(entries)
	if missing != 0 || typeChanged != 0 {
		t.Fatalf("missing = %d, typeChanged = %d, want 0/0", missing, typeChanged)
	}
	if len(revalidated) != 1 {
		t.Fatalf("revalidated = %+v, want 1 entry", revalidated)
	}
	if revalidated[0].Size != recursiveSize {
		t.Fatalf("Size = %d, want %d (the scan-time recursive size, not the directory's own Lstat size)", revalidated[0].Size, recursiveSize)
	}
}

func TestPrepareScanResultForClean_RevalidatesAndSkipsMissing(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.log")
	if err := os.WriteFile(keep, []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	missing := filepath.Join(dir, "missing.log")

	r := cleaner.NewRegistry()
	r.Register(&mockCleaner{category: cleaner.Category("temp_files"), name: "Temp Files"})

	scan := ScanResult{
		ScannedAt: time.Now().UTC(),
		Categories: []ScanCategoryResult{
			{
				Category:   cleaner.Category("temp_files"),
				Name:       "Temp Files",
				TotalFiles: 2,
				Files: []cleaner.FileEntry{
					{Path: keep, Size: 1, IsDir: false},
					{Path: missing, Size: 1, IsDir: false},
				},
			},
		},
	}

	prepared, err := PrepareScanResultForClean(context.Background(), r, scan, nil, nil)
	if err != nil {
		t.Fatalf("PrepareScanResultForClean() error: %v", err)
	}

	if prepared.RevalidatedFiles != 1 {
		t.Errorf("RevalidatedFiles = %d, want 1", prepared.RevalidatedFiles)
	}
	if prepared.MissingFiles != 1 {
		t.Errorf("MissingFiles = %d, want 1", prepared.MissingFiles)
	}
	if prepared.TypeChangedFiles != 0 {
		t.Errorf("TypeChangedFiles = %d, want 0", prepared.TypeChangedFiles)
	}
	if prepared.EmptyCategories != 0 {
		t.Errorf("EmptyCategories = %d, want 0", prepared.EmptyCategories)
	}
	if prepared.Result.TotalFiles != 1 {
		t.Errorf("TotalFiles = %d, want 1", prepared.Result.TotalFiles)
	}
	if len(prepared.Result.Categories[0].Files) != 1 {
		t.Fatalf("got %d files, want 1", len(prepared.Result.Categories[0].Files))
	}
	if prepared.Result.Categories[0].Files[0].Size != 5 {
		t.Errorf("revalidated size = %d, want 5", prepared.Result.Categories[0].Files[0].Size)
	}
}

func TestPrepareScanResultForClean_ReappliesCurrentConfigNotSavedFileState(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.log")
	if err := os.WriteFile(secret, []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	r := cleaner.NewRegistry()
	r.Register(&mockCleaner{category: cleaner.Category("temp_files"), name: "Temp Files"})

	// The saved scan file predates protecting `dir`; nothing in it is tagged.
	scan := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Category:   cleaner.Category("temp_files"),
				Name:       "Temp Files",
				TotalFiles: 1,
				Files: []cleaner.FileEntry{
					{Path: secret, Size: 1, IsDir: false},
				},
			},
		},
	}

	cfg, err := config.New([]string{dir}, nil)
	if err != nil {
		t.Fatalf("config.New() error: %v", err)
	}

	prepared, err := PrepareScanResultForClean(context.Background(), r, scan, nil, cfg)
	if err != nil {
		t.Fatalf("PrepareScanResultForClean() error: %v", err)
	}

	// PrepareScanResultForClean tags against the current config but does not
	// strip -- the hard-block gate (including the DeletesWholeDomain skip
	// decision) lives solely in runClean, which needs to see the true
	// protected count. Here we just confirm the now-protected file is
	// correctly re-tagged rather than trusted from the stale saved file.
	files := prepared.Result.Categories[0].Files
	if len(files) != 1 || !files[0].Protected {
		t.Fatalf("expected the now-protected file to be tagged (not stripped) here, got %+v", files)
	}
}

func TestPrepareScanResultForClean_RejectsSummaryOnlyScan(t *testing.T) {
	r := cleaner.NewRegistry()
	r.Register(&mockCleaner{category: cleaner.Category("temp_files"), name: "Temp Files"})

	scan := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Category:   cleaner.Category("temp_files"),
				Name:       "Temp Files",
				TotalFiles: 3,
			},
		},
	}

	prepared, err := PrepareScanResultForClean(context.Background(), r, scan, nil, nil)
	if err != nil {
		t.Fatalf("PrepareScanResultForClean() error: %v", err)
	}

	if !prepared.Result.HasErrors {
		t.Fatal("HasErrors = false, want true")
	}
	if prepared.Result.Categories[0].ErrMsg == "" {
		t.Fatal("ErrMsg should be populated")
	}
}

func TestPrepareScanResultForClean_SkipsTypeChangedEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "swap")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("os.Mkdir() error: %v", err)
	}

	r := cleaner.NewRegistry()
	r.Register(&mockCleaner{category: cleaner.Category("temp_files"), name: "Temp Files"})

	scan := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Category: cleaner.Category("temp_files"),
				Name:     "Temp Files",
				Files: []cleaner.FileEntry{
					{Path: path, IsDir: false},
				},
			},
		},
	}

	prepared, err := PrepareScanResultForClean(context.Background(), r, scan, nil, nil)
	if err != nil {
		t.Fatalf("PrepareScanResultForClean() error: %v", err)
	}

	if prepared.TypeChangedFiles != 1 {
		t.Errorf("TypeChangedFiles = %d, want 1", prepared.TypeChangedFiles)
	}
	if prepared.Result.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d, want 0", prepared.Result.TotalFiles)
	}
	if prepared.EmptyCategories != 1 {
		t.Errorf("EmptyCategories = %d, want 1", prepared.EmptyCategories)
	}
}

// mockRevalidatingCleaner is a mockCleaner that also implements
// cleaner.EntryRevalidator, standing in for the virtual-resource cleaners
// (Docker, Time Machine) whose entries are not filesystem paths.
type mockRevalidatingCleaner struct {
	mockCleaner
	revalErr error
	called   bool
}

func (m *mockRevalidatingCleaner) RevalidateEntries(_ context.Context, entries []cleaner.FileEntry) ([]cleaner.FileEntry, int, int, error) {
	m.called = true
	if m.revalErr != nil {
		return nil, 0, 0, m.revalErr
	}
	// Keep only the first entry, so the test can tell this ran instead of the
	// os.Stat path (which would have dropped every non-existent path).
	if len(entries) == 0 {
		return nil, 0, 0, nil
	}
	return entries[:1], len(entries) - 1, 0, nil
}

func TestPrepareScanResultForClean_PrefersEntryRevalidator(t *testing.T) {
	virtual := &mockRevalidatingCleaner{mockCleaner: mockCleaner{category: cleaner.Category("docker"), name: "Docker"}}

	r := cleaner.NewRegistry()
	r.Register(virtual)

	scan := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Category:   cleaner.Category("docker"),
				Name:       "Docker",
				TotalFiles: 2,
				Files: []cleaner.FileEntry{
					{Path: "docker://image/abc123456789/nginx:latest", Size: 10},
					{Path: "docker://image/def123456789/redis:latest", Size: 20},
				},
			},
		},
	}

	prepared, err := PrepareScanResultForClean(context.Background(), r, scan, nil, nil)
	if err != nil {
		t.Fatalf("PrepareScanResultForClean() error: %v", err)
	}

	if !virtual.called {
		t.Fatal("RevalidateEntries was not called; os.Stat path was used instead")
	}
	if prepared.RevalidatedFiles != 1 {
		t.Errorf("RevalidatedFiles = %d, want 1", prepared.RevalidatedFiles)
	}
	if prepared.MissingFiles != 1 {
		t.Errorf("MissingFiles = %d, want 1", prepared.MissingFiles)
	}
	files := prepared.Result.Categories[0].Files
	if len(files) != 1 || files[0].Path != "docker://image/abc123456789/nginx:latest" {
		t.Fatalf("files = %+v, want the single docker entry kept", files)
	}
	if prepared.Result.TotalSize != 10 {
		t.Errorf("TotalSize = %d, want 10", prepared.Result.TotalSize)
	}
}

func TestPrepareScanResultForClean_RevalidatorErrorIsScopedToItsCategory(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.log")
	if err := os.WriteFile(keep, []byte("hello"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	virtual := &mockRevalidatingCleaner{
		mockCleaner: mockCleaner{category: cleaner.Category("docker"), name: "Docker"},
		revalErr:    errors.New("docker daemon not running"),
	}

	r := cleaner.NewRegistry()
	r.Register(virtual)
	r.Register(&mockCleaner{category: cleaner.Category("temp_files"), name: "Temp Files"})

	scan := ScanResult{
		Categories: []ScanCategoryResult{
			{
				Category:   cleaner.Category("docker"),
				Name:       "Docker",
				TotalFiles: 1,
				Files:      []cleaner.FileEntry{{Path: "docker://volume/orphan"}},
			},
			{
				Category:   cleaner.Category("temp_files"),
				Name:       "Temp Files",
				TotalFiles: 1,
				Files:      []cleaner.FileEntry{{Path: keep, Size: 1}},
			},
		},
	}

	prepared, err := PrepareScanResultForClean(context.Background(), r, scan, nil, nil)
	if err != nil {
		t.Fatalf("PrepareScanResultForClean() error: %v", err)
	}

	if !prepared.Result.HasErrors {
		t.Error("HasErrors = false, want true")
	}

	byCategory := make(map[cleaner.Category]ScanCategoryResult)
	for _, c := range prepared.Result.Categories {
		byCategory[c.Category] = c
	}

	docker := byCategory[cleaner.Category("docker")]
	if !strings.Contains(docker.ErrMsg, "docker daemon not running") {
		t.Errorf("docker ErrMsg = %q, want the revalidation error", docker.ErrMsg)
	}
	if len(docker.Files) != 0 {
		t.Errorf("docker files = %+v, want none", docker.Files)
	}

	temp := byCategory[cleaner.Category("temp_files")]
	if temp.ErrMsg != "" {
		t.Errorf("temp ErrMsg = %q, want empty (sibling category must be unaffected)", temp.ErrMsg)
	}
	if len(temp.Files) != 1 {
		t.Fatalf("temp files = %+v, want the os.Stat-revalidated entry", temp.Files)
	}
	if temp.Files[0].Size != 5 {
		t.Errorf("temp file size = %d, want 5 (os.Stat path must still run)", temp.Files[0].Size)
	}
}
