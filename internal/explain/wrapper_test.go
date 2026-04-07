package explain

import (
	"errors"
	"testing"
	"time"

	"github.com/viniciussouzao/tidymymac/internal/cleaner"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// scannerContributor.Run

func TestScannerContributorRunRuntimeErr(t *testing.T) {
	c := scannerContributor{
		name:       ContributorCaches,
		runtimeErr: errors.New("setup failed"),
	}

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !result.HasError {
		t.Error("HasError = false, want true")
	}
	if result.ErrorMessage != "setup failed" {
		t.Errorf("ErrorMessage = %q, want %q", result.ErrorMessage, "setup failed")
	}
}

func TestScannerContributorRunNilCleaner(t *testing.T) {
	c := scannerContributor{
		name:    ContributorCaches,
		cleaner: nil,
	}

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !result.HasError {
		t.Error("HasError = false, want true when cleaner is nil")
	}
}

func TestScannerContributorRunScanSuccess(t *testing.T) {
	c := scannerContributor{
		name: ContributorCaches,
		cleaner: stubCleaner{
			category: cleaner.CategoryApplicationCaches,
			scanResult: &cleaner.ScanResult{
				TotalSize:  300,
				TotalFiles: 2,
				Entries: []cleaner.FileEntry{
					{Path: "/tmp/a", Size: 100},
					{Path: "/tmp/b", Size: 200},
				},
			},
		},
	}

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.HasError {
		t.Errorf("HasError = true: %s", result.ErrorMessage)
	}
	if result.TotalSize != 300 {
		t.Errorf("TotalSize = %d, want 300", result.TotalSize)
	}
	if result.TotalItems != 2 {
		t.Errorf("TotalItems = %d, want 2", result.TotalItems)
	}
	if result.TotalSizeHuman != utils.FormatBytes(300) {
		t.Errorf("TotalSizeHuman = %q, want %q", result.TotalSizeHuman, utils.FormatBytes(300))
	}
	if len(result.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(result.Items))
	}
	if result.Items[0].Path != "/tmp/a" || result.Items[0].Size != 100 {
		t.Errorf("Items[0] = %+v, want {Path:/tmp/a Size:100}", result.Items[0])
	}
}

func TestScannerContributorRunScanError(t *testing.T) {
	c := scannerContributor{
		name: ContributorCaches,
		cleaner: stubCleaner{
			category: cleaner.CategoryApplicationCaches,
			scanErr:  errors.New("permission denied"),
		},
	}

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !result.HasError {
		t.Error("HasError = false, want true on scan error")
	}
	if result.TotalSizeHuman != "0 B" {
		t.Errorf("TotalSizeHuman = %q, want %q", result.TotalSizeHuman, "0 B")
	}
}

func TestScannerContributorRunScanPartialResult(t *testing.T) {
	c := scannerContributor{
		name: ContributorCaches,
		cleaner: stubCleaner{
			category:   cleaner.CategoryApplicationCaches,
			scanResult: &cleaner.ScanResult{TotalSize: 512, TotalFiles: 1},
			scanErr:    errors.New("partial failure"),
		},
	}

	result, err := c.Run(t.Context())
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !result.HasError {
		t.Error("HasError = false, want true when scan returns error")
	}
	if result.TotalSize != 512 {
		t.Errorf("TotalSize = %d, want 512 (partial data preserved)", result.TotalSize)
	}
}

// RunProfile

func TestRunProfileEmptyProfiles(t *testing.T) {
	_, err := RunProfile(t.Context(), ProfileDefinition{})
	if err == nil {
		t.Fatal("RunProfile() expected error for empty Profile slice, got nil")
	}
}

func TestRunProfileContributorErrorDoesNotAbort(t *testing.T) {
	def := ProfileDefinition{
		Profile: []Profile{ProfileSystemData},
		Contributors: []Contributor{
			stubContributor{name: ContributorCaches, err: errors.New("scan failed")},
			stubContributor{name: ContributorLogs, result: ContributorResult{TotalSize: 500}},
		},
	}

	result, err := RunProfile(t.Context(), def)
	if err != nil {
		t.Fatalf("RunProfile() error: %v", err)
	}
	if len(result.Contributors) != 2 {
		t.Fatalf("len(Contributors) = %d, want 2", len(result.Contributors))
	}
	if !result.Contributors[0].HasError {
		t.Error("Contributors[0].HasError = false, want true")
	}
	if result.Contributors[1].HasError {
		t.Error("Contributors[1].HasError = true, want false")
	}
	if !result.HasErrors {
		t.Error("HasErrors = false, want true")
	}
}

func TestRunProfileAggregatesSize(t *testing.T) {
	def := ProfileDefinition{
		Profile: []Profile{ProfileSystemData},
		Contributors: []Contributor{
			stubContributor{name: ContributorCaches, result: ContributorResult{TotalSize: 1000}},
			stubContributor{name: ContributorLogs, result: ContributorResult{TotalSize: 2000}},
			stubContributor{name: ContributorTempFiles, result: ContributorResult{TotalSize: 9999, HasError: true}},
		},
	}

	result, err := RunProfile(t.Context(), def)
	if err != nil {
		t.Fatalf("RunProfile() error: %v", err)
	}
	if result.TotalSize != 3000 {
		t.Errorf("TotalSize = %d, want 3000 (errored contributor excluded)", result.TotalSize)
	}
	if result.TotalSizeHuman != utils.FormatBytes(3000) {
		t.Errorf("TotalSizeHuman = %q, want %q", result.TotalSizeHuman, utils.FormatBytes(3000))
	}
}

func TestRunProfileHasErrorsSetWhenContributorErrors(t *testing.T) {
	def := ProfileDefinition{
		Profile: []Profile{ProfileSystemData},
		Contributors: []Contributor{
			stubContributor{name: ContributorCaches, result: ContributorResult{HasError: true, ErrorMessage: "oops"}},
		},
	}

	result, err := RunProfile(t.Context(), def)
	if err != nil {
		t.Fatalf("RunProfile() error: %v", err)
	}
	if !result.HasErrors {
		t.Error("HasErrors = false, want true")
	}
}

func TestRunProfileScannedAtIsUTC(t *testing.T) {
	def := ProfileDefinition{Profile: []Profile{ProfileSystemData}}

	result, err := RunProfile(t.Context(), def)
	if err != nil {
		t.Fatalf("RunProfile() error: %v", err)
	}
	if result.ScannedAt.Location() != time.UTC {
		t.Errorf("ScannedAt.Location() = %v, want UTC", result.ScannedAt.Location())
	}
}
