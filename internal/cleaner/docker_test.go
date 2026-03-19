package cleaner

import (
	"context"
	"testing"
	"time"
)

func TestParseStoppedContainerInspectLine(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		line      string
		wantOK    bool
		wantName  string
		wantImage string
		wantSize  int64
	}{
		{
			name:      "stale container included",
			line:      "a2520505417d2eb423e1cf41eb67e6b371b818ca61941bc4494558aca6206109|/start-rs|mongo:8.2|2026-02-21T22:20:32.007787342Z|4096|sha256:7f5bbdafebde7c42e42e33396d01c0eda3eb753da8dae99071a30e350568a0a4",
			wantOK:    true,
			wantName:  "/start-rs",
			wantImage: "mongo:8.2",
			wantSize:  4096,
		},
		{
			name:   "recent container skipped",
			line:   "abc123|/recent|nginx:latest|2026-03-17T22:20:32.007787342Z|4096|sha256:image123",
			wantOK: false,
		},
		{
			name:      "exactly at threshold boundary is included",
			line:      "abc123|/boundary|redis:7|2026-03-11T12:00:00.000000000Z|1024|sha256:img",
			wantOK:    true,
			wantName:  "/boundary",
			wantImage: "redis:7",
			wantSize:  1024,
		},
		{
			name:      "just past threshold",
			line:      "abc123|/old|redis:7|2026-03-11T11:59:59.000000000Z|1024|sha256:img",
			wantOK:    true,
			wantName:  "/old",
			wantImage: "redis:7",
			wantSize:  1024,
		},
		{
			name:   "malformed too few fields",
			line:   "abc|/name|image",
			wantOK: false,
		},
		{
			name:   "empty line",
			line:   "",
			wantOK: false,
		},
		{
			name:   "invalid timestamp",
			line:   "abc|/name|image|not-a-date|4096|sha256:img",
			wantOK: false,
		},
		{
			name:   "invalid size",
			line:   "abc|/name|image|2026-01-01T00:00:00.000000000Z|notanumber|sha256:img",
			wantOK: false,
		},
		{
			name:      "negative size clamped to zero",
			line:      "abc|/name|image|2026-01-01T00:00:00.000000000Z|-500|sha256:img",
			wantOK:    true,
			wantName:  "/name",
			wantImage: "image",
			wantSize:  0,
		},
		{
			name:      "zero size accepted",
			line:      "abc|/name|image|2026-01-01T00:00:00.000000000Z|0|sha256:img",
			wantOK:    true,
			wantName:  "/name",
			wantImage: "image",
			wantSize:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container, ok := parseStoppedContainerInspectLine(tt.line, now)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if container.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", container.Name, tt.wantName)
			}
			if container.Image != tt.wantImage {
				t.Errorf("Image = %q, want %q", container.Image, tt.wantImage)
			}
			if container.Size != tt.wantSize {
				t.Errorf("Size = %d, want %d", container.Size, tt.wantSize)
			}
		})
	}
}

func TestParseStoppedContainerInspectLineFieldMapping(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	line := "full-id-here|/mycontainer|postgres:16|2026-01-01T00:00:00.000000000Z|8192|sha256:imagesha"

	c, ok := parseStoppedContainerInspectLine(line, now)
	if !ok {
		t.Fatal("expected container to be included")
	}

	if c.ID != "full-id-here" {
		t.Errorf("ID = %q, want %q", c.ID, "full-id-here")
	}
	if c.ImageID != "sha256:imagesha" {
		t.Errorf("ImageID = %q, want %q", c.ImageID, "sha256:imagesha")
	}
	if c.FinishedAt.IsZero() {
		t.Error("FinishedAt should not be zero")
	}
}

func TestExcludeImagesUsedByStoppedContainers(t *testing.T) {
	tests := []struct {
		name    string
		images  []imageInfo
		stopped map[string]bool
		wantLen int
		wantIDs []string
	}{
		{
			name:    "nil inputs",
			images:  nil,
			stopped: nil,
			wantLen: 0,
		},
		{
			name: "no match keeps all",
			images: []imageInfo{
				{ID: "img1"},
				{ID: "img2"},
			},
			stopped: map[string]bool{"other": true},
			wantLen: 2,
			wantIDs: []string{"img1", "img2"},
		},
		{
			name: "exact match excluded",
			images: []imageInfo{
				{ID: "sha256:abc"},
				{ID: "sha256:def"},
			},
			stopped: map[string]bool{"sha256:abc": true},
			wantLen: 1,
			wantIDs: []string{"sha256:def"},
		},
		{
			name: "prefix match excluded",
			images: []imageInfo{
				{ID: "abc"},
			},
			stopped: map[string]bool{"sha256:abc": true},
			wantLen: 0,
		},
		{
			name: "all excluded",
			images: []imageInfo{
				{ID: "sha256:a"},
				{ID: "sha256:b"},
			},
			stopped: map[string]bool{"sha256:a": true, "sha256:b": true},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := excludeImagesUsedByStoppedContainers(tt.images, tt.stopped)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for i, wantID := range tt.wantIDs {
				if got[i].ID != wantID {
					t.Errorf("got[%d].ID = %q, want %q", i, got[i].ID, wantID)
				}
			}
		})
	}
}

func TestDockerCleanerMetadata(t *testing.T) {
	c := NewDockerCleaner()

	if c.Category() != CategoryDocker {
		t.Errorf("Category() = %q, want %q", c.Category(), CategoryDocker)
	}
	if c.Name() != "Docker" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Docker")
	}
	if c.RequiresSudo() {
		t.Error("RequiresSudo() = true, want false")
	}
	if c.Description() == "" {
		t.Error("Description() is empty")
	}
}

func TestDockerCleanDryRun(t *testing.T) {
	c := NewDockerCleaner()
	entries := []FileEntry{
		{Path: "docker://container/abc123456789/test", Size: 1024, Category: CategoryDocker},
		{Path: "docker://image/def123456789/nginx:latest", Size: 2048, Category: CategoryDocker},
		{Path: "docker://volume/my-volume", Size: 0, Category: CategoryDocker},
	}

	result, err := c.Clean(t.Context(), entries, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true) error: %v", err)
	}

	if !result.DryRun {
		t.Error("result.DryRun = false, want true")
	}
	if result.FilesDeleted != 3 {
		t.Errorf("FilesDeleted = %d, want 3", result.FilesDeleted)
	}
	if result.BytesFreed != 3072 {
		t.Errorf("BytesFreed = %d, want 3072", result.BytesFreed)
	}
	if result.Category != CategoryDocker {
		t.Errorf("Category = %q, want %q", result.Category, CategoryDocker)
	}
}

func TestDockerCleanDryRunEmpty(t *testing.T) {
	c := NewDockerCleaner()
	result, err := c.Clean(t.Context(), nil, true, nil)
	if err != nil {
		t.Fatalf("Clean(dryRun=true, nil) error: %v", err)
	}
	if result.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0", result.FilesDeleted)
	}
}

func TestDockerCleanInvalidPathIsReported(t *testing.T) {
	c := NewDockerCleaner()
	entries := []FileEntry{
		{Path: "docker://badpath", Size: 100, Category: CategoryDocker},
	}

	result, err := c.Clean(t.Context(), entries, false, nil)
	if err != nil {
		t.Fatalf("Clean() error: %v", err)
	}

	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestDockerCleanContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := NewDockerCleaner()
	entries := []FileEntry{
		{Path: "docker://container/abc123456789/test", Size: 1024, Category: CategoryDocker},
	}

	result, err := c.Clean(ctx, entries, false, nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if result.FilesDeleted != 0 {
		t.Errorf("FilesDeleted = %d, want 0 after cancellation", result.FilesDeleted)
	}
}
