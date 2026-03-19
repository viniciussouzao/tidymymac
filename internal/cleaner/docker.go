package cleaner

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const stoppedThreshold = 7 * 24 * time.Hour

const dockerStoppedContainerInspectFormat = `{{.Id}}|{{.Name}}|{{.Config.Image}}|{{.State.FinishedAt}}|{{.SizeRw}}|{{.Image}}`

type containerInfo struct {
	ID         string
	Name       string
	Image      string
	ImageID    string
	Size       int64
	FinishedAt time.Time
}

type imageInfo struct {
	ID   string
	Tags []string
	Size int64
}

// DockerCleaner scans for unused Docker resources like stopped containers and untagged images.
type DockerCleaner struct{}

// NewDockerCleaner creates a new instance of DockerCleaner.
func NewDockerCleaner() *DockerCleaner {
	return &DockerCleaner{}
}

func (c *DockerCleaner) Category() Category  { return CategoryDocker }
func (c *DockerCleaner) Name() string        { return "Docker" }
func (c *DockerCleaner) Description() string { return "Unused Docker images and stopped containers" }
func (c *DockerCleaner) RequiresSudo() bool  { return false }

func (c *DockerCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	start := time.Now()
	result := &ScanResult{Category: CategoryDocker}

	// Check if docker is installed
	if _, err := exec.LookPath("docker"); err != nil {
		result.Duration = time.Since(start)
		return result, nil // Docker not installed, return empty result
	}

	// Check if daemon is running
	cmdOut, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(cmdOut))
		if msg != "" {
			result.Errors = append(result.Errors, fmt.Errorf("docker info: %s", msg))
			return result, nil // Docker not running, return empty result with error
		} else {
			result.Errors = append(result.Errors, fmt.Errorf("docker info: %w", err))
			return result, nil
		}
	}

	// 1. find stopped containers > 7 days
	stoppedContainers, err := findStoppedContainers(ctx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("finding stopped containers: %w", err))
	}

	stoppedImageIDs := make(map[string]bool)
	for _, sc := range stoppedContainers {
		entry := FileEntry{
			Path:     fmt.Sprintf("docker://container/%s/%s", sc.ID[:12], strings.TrimPrefix(sc.Name, "/")),
			Size:     sc.Size,
			Category: CategoryDocker,
		}
		result.Entries = append(result.Entries, entry)
		result.TotalSize += sc.Size
		result.TotalFiles++

		// Track image IDs for later
		if sc.ImageID != "" {
			stoppedImageIDs[sc.ImageID] = true
		}
	}

	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	// 2. find untagged images
	untaggedImages, err := findImagesWithoutTags(ctx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("finding untagged images: %w", err))
	}

	for _, img := range untaggedImages {
		tag := "<none>"
		if len(img.Tags) > 0 {
			tag = img.Tags[0]
		}

		entry := FileEntry{
			Path:     fmt.Sprintf("docker://image/%s/%s", img.ID[:12], tag),
			Size:     img.Size,
			Category: CategoryDocker,
		}
		result.Entries = append(result.Entries, entry)
		result.TotalSize += img.Size
		result.TotalFiles++
	}

	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	// 3. find images used by stopped containers
	imagesForStoppedContainers, err := findImagesForStoppedContainers(ctx, stoppedImageIDs)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("finding images for stopped containers: %w", err))
	}

	imagesForStoppedContainers = excludeImagesUsedByStoppedContainers(imagesForStoppedContainers, stoppedImageIDs)

	for _, img := range imagesForStoppedContainers {
		tag := "<none>"
		if len(img.Tags) > 0 {
			tag = img.Tags[0]
		}

		entry := FileEntry{
			Path:     fmt.Sprintf("docker://image/%s/%s", img.ID[:12], tag),
			Size:     img.Size,
			Category: CategoryDocker,
		}
		result.Entries = append(result.Entries, entry)
		result.TotalSize += img.Size
		result.TotalFiles++
	}

	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	// 4. find orphaned volumes
	orphanedVolumes, err := findOrphanedVolumes(ctx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("finding orphaned volumes: %w", err))
	}

	var orphanedCount int64

	for _, vol := range orphanedVolumes {
		entry := FileEntry{
			Path:     fmt.Sprintf("docker://volume/%s", vol),
			Size:     0, // Docker doesn't provide size for volumes easily
			Category: CategoryDocker,
		}
		result.Entries = append(result.Entries, entry)
		result.TotalFiles++
		orphanedCount++
	}

	if orphanedCount > 0 && result.TotalSize == 0 {
		result.TotalSize = orphanedCount
	}

	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (c *DockerCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{Category: CategoryDocker, DryRun: dryRun}

	if dryRun {
		for _, e := range entries {
			result.FilesDeleted++
			result.BytesFreed += e.Size
		}
		result.Duration = time.Since(start)
		return result, nil
	}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			result.Duration = time.Since(start)
			return result, err
		}

		// parts[0] = "docker:", parts[1] = "", parts[2] = type, parts[3] = id/...
		resourcePath := strings.TrimPrefix(e.Path, "docker://")
		segments := strings.SplitN(resourcePath, "/", 3) // type/id/name
		if len(segments) < 3 {
			if !strings.HasPrefix(e.Path, "docker://") {
				result.Errors = append(result.Errors, fmt.Errorf("invalid docker entry path: %s", e.Path))
			}
		}

		resourceType := segments[0]
		resourceID := segments[1]

		var cmd *exec.Cmd
		switch resourceType {
		case "container":
			cmd = exec.CommandContext(ctx, "docker", "rm", "-f", resourceID)
		case "image":
			cmd = exec.CommandContext(ctx, "docker", "rmi", "-f", resourceID)
		case "volume":
			cmd = exec.CommandContext(ctx, "docker", "volume", "rm", resourceID)
		default:
			continue
		}

		if out, err := cmd.CombinedOutput(); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("docker %s %s: %s: %w", resourceType, resourceID, strings.TrimSpace(string(out)), err))
		} else {
			result.FilesDeleted++
			result.BytesFreed += e.Size
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

func findStoppedContainers(ctx context.Context) ([]containerInfo, error) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "status=exited", "--format", "{{.ID}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}

	ids := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(ids) == 0 || (len(ids) == 1 && ids[0] == "") {
		return nil, nil // no stopped containers
	}

	var containers []containerInfo
	var inspectErrs []string
	now := time.Now()

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}

		// check container details
		inspectOut, err := exec.CommandContext(ctx, "docker", "inspect",
			"--format", dockerStoppedContainerInspectFormat,
			"--size", id).Output()
		if err != nil {
			inspectErrs = append(inspectErrs, fmt.Sprintf("docker inspect %s: %s", id, err))
			continue // skip this container but keep going
		}

		container, ok := parseStoppedContainerInspectLine(strings.TrimSpace(string(inspectOut)), now)
		if !ok {
			continue
		}

		if len(inspectErrs) > 0 {
			return containers, fmt.Errorf("docker inspect failed for some containers: %s", strings.Join(inspectErrs, "; "))
		}

		containers = append(containers, container)
	}

	return containers, nil
}

func parseStoppedContainerInspectLine(line string, now time.Time) (containerInfo, bool) {
	fields := strings.SplitN(line, "|", 6)
	if len(fields) < 6 {
		return containerInfo{}, false
	}

	finishedAt, err := time.Parse(time.RFC3339Nano, fields[3])
	if err != nil {
		// Keep a second parse path in case Docker emits a non-standard fractional precision.
		finishedAt, err = time.Parse("2006-01-02T15:04:05.999999999Z", fields[3])
		if err != nil {
			return containerInfo{}, false
		}
	}

	if now.Sub(finishedAt) < stoppedThreshold {
		return containerInfo{}, false
	}

	size, _ := strconv.ParseInt(fields[4], 10, 64)
	if size < 0 {
		size = 0
	}

	return containerInfo{
		ID:         fields[0],
		Name:       fields[1],
		Image:      fields[2],
		ImageID:    fields[5],
		Size:       size,
		FinishedAt: finishedAt,
	}, true
}

func findImagesWithoutTags(ctx context.Context) ([]imageInfo, error) {
	out, err := exec.CommandContext(ctx, "docker", "images",
		"--filter", "dangling=true",
		"--format", "{{.ID}}\t{{.Repository}}:{{.Tag}}\t{{.Size}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker images: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, nil
	}

	var images []imageInfo
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}

		size := parseDockerSize(parts[2])
		tag := parts[1]
		if tag == "<none>:<none>" {
			tag = "<none>"
		}

		images = append(images, imageInfo{
			ID:   parts[0],
			Tags: []string{tag},
			Size: size,
		})
	}

	return images, nil
}

func findImagesForStoppedContainers(ctx context.Context, imageIDs map[string]bool) ([]imageInfo, error) {
	if len(imageIDs) == 0 {
		return nil, nil
	}

	// Get all images as JSON for reliable parsing.
	out, err := exec.CommandContext(ctx, "docker", "images", "--no-trunc",
		"--format", "{{json .}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker images: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var images []imageInfo
	seen := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var img struct {
			ID         string `json:"ID"`
			Repository string `json:"Repository"`
			Tag        string `json:"Tag"`
			Size       string `json:"Size"`
		}
		if err := json.Unmarshal([]byte(line), &img); err != nil {
			continue
		}

		// Check if this image's full ID matches a stale container's image.
		fullID := "sha256:" + img.ID
		if !imageIDs[img.ID] && !imageIDs[fullID] && !imageIDs[img.Repository+":"+img.Tag] {
			continue
		}

		// Skip dangling images (already captured separately).
		if img.Repository == "<none>" {
			continue
		}

		if seen[img.ID] {
			continue
		}
		seen[img.ID] = true

		tag := img.Repository + ":" + img.Tag
		size := parseDockerSize(img.Size)

		images = append(images, imageInfo{
			ID:   img.ID,
			Tags: []string{tag},
			Size: size,
		})
	}

	return images, nil
}

func findOrphanedVolumes(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "docker", "volume", "ls",
		"--filter", "dangling=true",
		"--format", "{{.Name}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker volume ls: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var volumes []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			volumes = append(volumes, line)
		}
	}

	return volumes, nil
}

func excludeImagesUsedByStoppedContainers(images []imageInfo, stoppedImageIDs map[string]bool) []imageInfo {
	var filtered []imageInfo
	for _, img := range images {
		if stoppedImageIDs[img.ID] || stoppedImageIDs["sha256:"+img.ID] {
			continue
		}
		filtered = append(filtered, img)
	}

	return filtered
}
