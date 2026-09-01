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

func TestDockerEntryBuilders(t *testing.T) {
	tests := []struct {
		name     string
		entry    FileEntry
		wantPath string
		wantSize int64
		wantKind string
	}{
		{
			name:     "stopped container",
			entry:    dockerContainerEntry(containerInfo{ID: "abc123456789def", Name: "/web", Size: 4096}),
			wantPath: "docker://container/abc123456789/web",
			wantSize: 4096,
			wantKind: DockerResourceKindContainerStopped,
		},
		{
			name:     "dangling image",
			entry:    dockerImageEntry(imageInfo{ID: "def123456789abc", Tags: []string{"<none>"}, Size: 2048}, DockerResourceKindImageDangling),
			wantPath: "docker://image/def123456789/<none>",
			wantSize: 2048,
			wantKind: DockerResourceKindImageDangling,
		},
		{
			name:     "dangling image without tags falls back to <none>",
			entry:    dockerImageEntry(imageInfo{ID: "aaaaaaaaaaaabbb", Size: 1}, DockerResourceKindImageDangling),
			wantPath: "docker://image/aaaaaaaaaaaa/<none>",
			wantSize: 1,
			wantKind: DockerResourceKindImageDangling,
		},
		{
			name:     "image tied to stopped container",
			entry:    dockerImageEntry(imageInfo{ID: "111122223333444", Tags: []string{"nginx:latest"}, Size: 8192}, DockerResourceKindImageStoppedContainer),
			wantPath: "docker://image/111122223333/nginx:latest",
			wantSize: 8192,
			wantKind: DockerResourceKindImageStoppedContainer,
		},
		{
			name:     "orphaned volume",
			entry:    dockerVolumeEntry("my-volume"),
			wantPath: "docker://volume/my-volume",
			wantSize: 0,
			wantKind: DockerResourceKindVolumeOrphaned,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.entry.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", tt.entry.Path, tt.wantPath)
			}
			if tt.entry.Size != tt.wantSize {
				t.Errorf("Size = %d, want %d", tt.entry.Size, tt.wantSize)
			}
			if tt.entry.ResourceKind != tt.wantKind {
				t.Errorf("ResourceKind = %q, want %q", tt.entry.ResourceKind, tt.wantKind)
			}
			if tt.entry.Category != CategoryDocker {
				t.Errorf("Category = %q, want %q", tt.entry.Category, CategoryDocker)
			}
			if tt.entry.Protected {
				t.Error("Protected = true, want false (Scan must never set it)")
			}
		})
	}
}

// The four kinds must stay distinct: reporting groups on them, and images have
// two kinds behind an identical Path.
func TestDockerResourceKindsAreDistinct(t *testing.T) {
	kinds := []string{
		DockerResourceKindContainerStopped,
		DockerResourceKindImageDangling,
		DockerResourceKindImageStoppedContainer,
		DockerResourceKindVolumeOrphaned,
	}

	seen := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		if k == "" {
			t.Error("resource kind must not be empty")
		}
		if seen[k] {
			t.Errorf("duplicate resource kind %q", k)
		}
		seen[k] = true
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

func TestParseDockerResourcePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantType string
		wantID   string
		wantOK   bool
	}{
		{name: "container", path: "docker://container/abc123456789/web", wantType: "container", wantID: "abc123456789", wantOK: true},
		{name: "image", path: "docker://image/def123456789/nginx:latest", wantType: "image", wantID: "def123456789", wantOK: true},
		{name: "volume has no name segment", path: "docker://volume/my-volume", wantType: "volume", wantID: "my-volume", wantOK: true},
		{name: "missing id", path: "docker://image", wantOK: false},
		{name: "empty id", path: "docker://image/", wantOK: false},
		{name: "not a docker path", path: "/tmp/file", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceType, id, ok := parseDockerResourcePath(tt.path)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if resourceType != tt.wantType || id != tt.wantID {
				t.Errorf("got (%q, %q), want (%q, %q)", resourceType, id, tt.wantType, tt.wantID)
			}
		})
	}
}

func TestMatchDockerEntries(t *testing.T) {
	sets := dockerResourceSets{
		containers: map[string]bool{"abc123456789": true},
		images:     map[string]bool{"def123456789": true},
		volumes:    map[string]bool{"kept-volume": true},
	}

	tests := []struct {
		name        string
		sets        dockerResourceSets
		entries     []FileEntry
		wantKept    []string
		wantMissing int
	}{
		{
			name: "every kind still present",
			sets: sets,
			entries: []FileEntry{
				{Path: "docker://container/abc123456789/web", ResourceKind: DockerResourceKindContainerStopped},
				{Path: "docker://image/def123456789/nginx:latest", ResourceKind: DockerResourceKindImageDangling},
				{Path: "docker://volume/kept-volume", ResourceKind: DockerResourceKindVolumeOrphaned},
			},
			wantKept: []string{
				"docker://container/abc123456789/web",
				"docker://image/def123456789/nginx:latest",
				"docker://volume/kept-volume",
			},
		},
		{
			name: "resources removed since the scan",
			sets: sets,
			entries: []FileEntry{
				{Path: "docker://container/999999999999/gone", ResourceKind: DockerResourceKindContainerStopped},
				{Path: "docker://image/888888888888/gone:latest", ResourceKind: DockerResourceKindImageStoppedContainer},
				{Path: "docker://volume/gone-volume", ResourceKind: DockerResourceKindVolumeOrphaned},
			},
			wantMissing: 3,
		},
		{
			name:        "malformed path counts as missing",
			sets:        sets,
			entries:     []FileEntry{{Path: "docker://image"}},
			wantMissing: 1,
		},
		{
			name:        "unknown resource type counts as missing",
			sets:        sets,
			entries:     []FileEntry{{Path: "docker://network/abc123456789/bridge"}},
			wantMissing: 1,
		},
		{
			name: "a type that was never queried is missing, not kept",
			// containers was not listed (nil map), so a container entry cannot
			// be confirmed and must not survive revalidation.
			sets:        dockerResourceSets{images: map[string]bool{"def123456789": true}},
			entries:     []FileEntry{{Path: "docker://container/abc123456789/web"}},
			wantMissing: 1,
		},
		{
			// Was previously accepted. A 6-char id is far too short to identify
			// a resource Clean will then "docker rmi -f".
			name: "a truncation shorter than a docker short id is not a match",
			sets: dockerResourceSets{images: map[string]bool{"def123456789": true}},
			entries: []FileEntry{
				{Path: "docker://image/sha256:def123/nginx:latest"},
			},
			wantMissing: 1,
		},
		{
			name: "11 characters is still one short of the floor",
			sets: dockerResourceSets{images: map[string]bool{"def123456789abcdef": true}},
			entries: []FileEntry{
				{Path: "docker://image/def12345678/nginx:latest"},
			},
			wantMissing: 1,
		},
		{
			name: "12 characters is the docker short-id width and matches",
			sets: dockerResourceSets{images: map[string]bool{"def123456789abcdef": true}},
			entries: []FileEntry{
				{Path: "docker://image/def123456789/nginx:latest"},
			},
			wantKept: []string{"docker://image/def123456789/nginx:latest"},
		},
		{
			name: "a sha256-prefixed full id matches the short id docker lists",
			sets: dockerResourceSets{images: map[string]bool{"def123456789": true}},
			entries: []FileEntry{
				{Path: "docker://image/sha256:def123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd/nginx:latest"},
			},
			wantKept: []string{"docker://image/sha256:def123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd/nginx:latest"},
		},
		{
			name: "a volume name matches only exactly, never by prefix",
			sets: dockerResourceSets{volumes: map[string]bool{"db-backup-volume": true}},
			entries: []FileEntry{
				{Path: "docker://volume/db-backup-volume"},
				{Path: "docker://volume/db-backup"},
			},
			wantKept:    []string{"docker://volume/db-backup-volume"},
			wantMissing: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, missing := matchDockerEntries(tt.entries, tt.sets)
			if missing != tt.wantMissing {
				t.Errorf("missing = %d, want %d", missing, tt.wantMissing)
			}
			if len(kept) != len(tt.wantKept) {
				t.Fatalf("kept %d entries, want %d (%+v)", len(kept), len(tt.wantKept), kept)
			}
			for i, want := range tt.wantKept {
				if kept[i].Path != want {
					t.Errorf("kept[%d] = %q, want %q", i, kept[i].Path, want)
				}
			}
		})
	}
}

func TestCleanersImplementEntryRevalidator(t *testing.T) {
	// Both cleaners produce non-filesystem Paths, so the os.Stat-based default
	// revalidation would drop all their entries -- they must opt in.
	if _, ok := any(NewDockerCleaner()).(EntryRevalidator); !ok {
		t.Error("DockerCleaner does not implement EntryRevalidator")
	}
	if _, ok := any(NewTimeMachineCleaner()).(EntryRevalidator); !ok {
		t.Error("TimeMachineCleaner does not implement EntryRevalidator")
	}
}
