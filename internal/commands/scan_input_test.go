package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
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

	prepared, err := PrepareScanResultForClean(r, scan, nil)
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

	prepared, err := PrepareScanResultForClean(r, scan, nil)
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

	prepared, err := PrepareScanResultForClean(r, scan, nil)
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
