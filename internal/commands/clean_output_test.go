package commands

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteCleanOutput_JSON(t *testing.T) {
	output := CleanOutput{
		Result: CleanResult{
			TotalFiles:     2,
			TotalSize:      1536,
			TotalSizeHuman: "1.5 KB",
			Categories: []CleanCategoryResult{
				{Name: "Temporary Files", DeletedFiles: 2, DeletedSize: 1536},
			},
		},
		Revalidation: &RevalidationSummary{
			RevalidatedFiles: 2,
			MissingFiles:     1,
			TypeChangedFiles: 0,
			EmptyCategories:  0,
		},
	}

	var buf bytes.Buffer
	if err := WriteCleanOutput(&buf, output, "json"); err != nil {
		t.Fatalf("WriteCleanOutput() error: %v", err)
	}

	got := buf.String()
	for _, want := range []string{`"result"`, `"revalidation"`, `"revalidated_files": 2`, `"total_files": 2`} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
}

func TestWriteCleanOutput_UnsupportedFormat(t *testing.T) {
	var buf bytes.Buffer
	err := WriteCleanOutput(&buf, CleanOutput{}, "csv")
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}
