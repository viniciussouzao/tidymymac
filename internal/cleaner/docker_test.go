package cleaner

import (
	"testing"
	"time"
)

func TestParseStoppedContainerInspectLineIncludesOldContainer(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	line := "a2520505417d2eb423e1cf41eb67e6b371b818ca61941bc4494558aca6206109|/start-rs|mongo:8.2|2026-02-21T22:20:32.007787342Z|4096|sha256:7f5bbdafebde7c42e42e33396d01c0eda3eb753da8dae99071a30e350568a0a4"

	container, ok := parseStoppedContainerInspectLine(line, now)
	if !ok {
		t.Fatalf("expected stale container to be included")
	}

	if container.Name != "/start-rs" {
		t.Fatalf("unexpected container name: %q", container.Name)
	}

	if container.Image != "mongo:8.2" {
		t.Fatalf("unexpected image name: %q", container.Image)
	}

	if container.ImageID != "sha256:7f5bbdafebde7c42e42e33396d01c0eda3eb753da8dae99071a30e350568a0a4" {
		t.Fatalf("unexpected image ID: %q", container.ImageID)
	}
}

func TestParseStoppedContainerInspectLineSkipsRecentContainer(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	line := "abc|/recent|mongo:8.2|2026-03-17T22:20:32.007787342Z|4096|sha256:image"

	if _, ok := parseStoppedContainerInspectLine(line, now); ok {
		t.Fatalf("expected recent container to be skipped")
	}
}

func TestParseStoppedContainerInspectLineRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)

	if _, ok := parseStoppedContainerInspectLine("invalid", now); ok {
		t.Fatalf("expected malformed inspect output to be rejected")
	}
}

func TestParseDockerSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"1.2GB", 1288490188},
		{"500MB", 524288000},
		{"0B", 0},
		{"2TB", 2199023255552},
		{"100kB", 102400},
	}

	for _, test := range tests {
		result := parseDockerSize(test.input)
		if result != test.expected {
			t.Errorf("parseDockerSize(%q) = %d; expected %d", test.input, result, test.expected)
		}
	}
}

func TestFindImagesForStoppedContainers(t *testing.T) {
	tests := []struct {
		name     string
		imageIds map[string]bool
	}{
		{"test-image:latest", map[string]bool{"sha256:7f5bbdafebde7c42e42e33396d01c0eda3eb753da8dae99071a30e350568a0a4": true}},
		{"another-image:1.0", map[string]bool{"sha256:f226345927d7e348497136874b6d207e0b32cc52154ad8323129352923a3142f": true}},
	}

	for _, test := range tests {
		result, err := findImagesForStoppedContainers(t.Context(), test.imageIds)
		if err != nil {
			t.Errorf("findImagesForStoppedContainers returned error: %v", err)
			continue
		}

		if len(result) != len(test.imageIds) {
			t.Errorf("findImagesForStoppedContainers(%v) = %d; expected %d", test.imageIds, len(result), len(test.imageIds))
		}

	}

}
