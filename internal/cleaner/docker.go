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

// Values for FileEntry.ResourceKind produced by DockerCleaner.Scan. They exist
// so reporting code can group Docker findings without parsing Path (which
// cannot tell dangling images apart from images kept alive by a stopped
// container). Adding a new kind is just adding a constant here and setting it
// on the entries of a new scan step.
const (
	DockerResourceKindContainerStopped      = "container_stopped"
	DockerResourceKindImageDangling         = "image_dangling"
	DockerResourceKindImageStoppedContainer = "image_stopped_container"
	DockerResourceKindVolumeOrphaned        = "volume_orphaned"
)

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

func (c *DockerCleaner) Category() Category       { return CategoryDocker }
func (c *DockerCleaner) Name() string             { return "Docker" }
func (c *DockerCleaner) Description() string      { return "Unused Docker images and stopped containers" }
func (c *DockerCleaner) RequiresSudo() bool       { return false }
func (c *DockerCleaner) DeletesWholeDomain() bool { return false }

// SupportsItemSelection implements cleaner.ItemSelectable: each entry is an
// individually removable image, container, or volume.
func (c *DockerCleaner) SupportsItemSelection() bool { return true }

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
		entry := dockerContainerEntry(sc)
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
		entry := dockerImageEntry(img, DockerResourceKindImageDangling)
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
		entry := dockerImageEntry(img, DockerResourceKindImageStoppedContainer)
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

	for _, vol := range orphanedVolumes {
		entry := dockerVolumeEntry(vol)
		result.Entries = append(result.Entries, entry)
		result.TotalFiles++
	}

	if err := ctx.Err(); err != nil {
		result.Duration = time.Since(start)
		return result, err
	}

	result.Duration = time.Since(start)
	return result, nil
}

// dockerContainerEntry builds the entry for a stopped container. The
// docker://container/<id>/<name> Path shape is part of the contract with
// Clean and scriptgen and must not change.
func dockerContainerEntry(sc containerInfo) FileEntry {
	return FileEntry{
		Path:         fmt.Sprintf("docker://container/%s/%s", sc.ID[:12], strings.TrimPrefix(sc.Name, "/")),
		Size:         sc.Size,
		Category:     CategoryDocker,
		ResourceKind: DockerResourceKindContainerStopped,
	}
}

// dockerImageEntry builds the entry for an image. kind distinguishes dangling
// images from images kept around by a stopped container; the Path is identical
// in both cases.
func dockerImageEntry(img imageInfo, kind string) FileEntry {
	tag := "<none>"
	if len(img.Tags) > 0 {
		tag = img.Tags[0]
	}

	return FileEntry{
		Path:         fmt.Sprintf("docker://image/%s/%s", img.ID[:12], tag),
		Size:         img.Size,
		Category:     CategoryDocker,
		ResourceKind: kind,
	}
}

// dockerVolumeEntry builds the entry for an orphaned volume. Size stays 0:
// Docker does not expose volume sizes cheaply.
func dockerVolumeEntry(name string) FileEntry {
	return FileEntry{
		Path:         fmt.Sprintf("docker://volume/%s", name),
		Size:         0,
		Category:     CategoryDocker,
		ResourceKind: DockerResourceKindVolumeOrphaned,
	}
}

func (c *DockerCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{Category: CategoryDocker, DryRun: dryRun}
	total := totalSize(entries)

	if dryRun {
		for i, e := range entries {
			result.FilesDeleted++
			result.BytesFreed += e.Size

			if progress != nil && (i%10 == 0 || i == len(entries)-1) {
				progress(CleanProgress{
					Category:     CategoryDocker,
					FilesDeleted: result.FilesDeleted,
					FilesTotal:   len(entries),
					BytesDeleted: result.BytesFreed,
					BytesTotal:   total,
					CurrentFile:  e.Path,
				})
			}
		}
		result.Duration = time.Since(start)
		return result, nil
	}

	for i, e := range entries {
		if err := ctx.Err(); err != nil {
			result.Duration = time.Since(start)
			return result, err
		}

		// parts[0] = "docker:", parts[1] = "", parts[2] = type, parts[3] = id/...
		resourcePath := strings.TrimPrefix(e.Path, "docker://")
		segments := strings.SplitN(resourcePath, "/", 3) // type/id/name
		if len(segments) < 2 {
			result.Errors = append(result.Errors, fmt.Errorf("invalid docker entry path: %s", e.Path))
			continue
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

			if progress != nil && (i%10 == 0 || i == len(entries)-1) {
				progress(CleanProgress{
					Category:     CategoryDocker,
					FilesDeleted: result.FilesDeleted,
					FilesTotal:   len(entries),
					BytesDeleted: result.BytesFreed,
					BytesTotal:   total,
					CurrentFile:  e.Path,
				})
			}
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// dockerResourceSets holds the identifiers Docker currently reports, per
// resource type. A nil map means "that type was not queried" (no entry of that
// kind was up for revalidation), which is distinct from an empty map meaning
// "Docker has none of them left".
type dockerResourceSets struct {
	containers map[string]bool
	images     map[string]bool
	volumes    map[string]bool
}

// RevalidateEntries implements EntryRevalidator. Docker entries carry a
// docker://<type>/<id>/<name> pseudo-path, not a filesystem path, so the
// default os.Stat revalidation would drop all of them. Existence in the
// daemon's current listings is the whole contract here: whether an image is
// still dangling or a container has been stopped long enough is Scan's job,
// not revalidation's.
func (c *DockerCleaner) RevalidateEntries(ctx context.Context, entries []FileEntry) ([]FileEntry, int, int, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, 0, 0, fmt.Errorf("revalidate docker entries: docker not found: %w", err)
	}

	// Only list the resource types actually present in entries, so a clean of
	// (say) volumes alone never pays for an image listing.
	var wantContainers, wantImages, wantVolumes bool
	for _, entry := range entries {
		switch dockerResourceType(entry.Path) {
		case "container":
			wantContainers = true
		case "image":
			wantImages = true
		case "volume":
			wantVolumes = true
		}
	}

	var sets dockerResourceSets
	var err error
	if wantContainers {
		if sets.containers, err = dockerListIDs(ctx, "ps", "-a", "--format", "{{.ID}}"); err != nil {
			return nil, 0, 0, fmt.Errorf("revalidate docker containers: %w", err)
		}
	}
	if wantImages {
		if sets.images, err = dockerListIDs(ctx, "images", "-a", "--format", "{{.ID}}"); err != nil {
			return nil, 0, 0, fmt.Errorf("revalidate docker images: %w", err)
		}
	}
	if wantVolumes {
		if sets.volumes, err = dockerListIDs(ctx, "volume", "ls", "--format", "{{.Name}}"); err != nil {
			return nil, 0, 0, fmt.Errorf("revalidate docker volumes: %w", err)
		}
	}

	revalidated, missing := matchDockerEntries(entries, sets)
	// A Docker resource has no file type to change, so typeChanged is always 0.
	return revalidated, missing, 0, nil
}

// dockerListIDs runs a docker listing command and returns its non-empty output
// lines as a set. The returned map is never nil on success, so callers can
// tell "queried, none left" from "not queried".
func dockerListIDs(ctx context.Context, args ...string) (map[string]bool, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}

	ids := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids[line] = true
		}
	}
	return ids, nil
}

// matchDockerEntries keeps the entries whose identifier is still present in
// the corresponding current set, counting the rest as missing. Split out from
// RevalidateEntries so the matching logic is testable without a daemon.
func matchDockerEntries(entries []FileEntry, sets dockerResourceSets) ([]FileEntry, int) {
	revalidated := make([]FileEntry, 0, len(entries))
	var missing int

	for _, entry := range entries {
		resourceType, id, ok := parseDockerResourcePath(entry.Path)
		if !ok {
			// An unparseable path could never be deleted by Clean either, so
			// dropping it as missing is the honest outcome.
			missing++
			continue
		}

		var current map[string]bool
		switch resourceType {
		case "container":
			current = sets.containers
		case "image":
			current = sets.images
		case "volume":
			current = sets.volumes
		default:
			missing++
			continue
		}

		// Volumes are identified by NAME, not by a truncatable hex id, so
		// prefix matching would be meaningless there and dangerous ("db" would
		// revalidate against "db-backup").
		if !dockerIDPresent(current, id, resourceType != "volume") {
			missing++
			continue
		}
		revalidated = append(revalidated, entry)
	}

	return revalidated, missing
}

// dockerShortIDLen is docker's own short-ID width, and the minimum length a
// prefix match is allowed to be decided on.
const dockerShortIDLen = 12

// dockerIDPresent matches by ID prefix in both directions: entry paths carry a
// 12-char truncation of whatever Scan saw, which may itself have been a full
// (or sha256:-prefixed) ID, while the listing commands emit short IDs.
//
// allowPrefix is false for resources identified by name (volumes), where only
// exact equality is meaningful.
//
// The length floor is a safety bound, not an optimization. Revalidation is
// what licenses Clean to run "docker rmi -f" on an entry, and an unbounded
// prefix rule let a 1-char id from a stale or hand-edited saved scan match an
// unrelated live resource -- resurrecting an approval for something the user
// never reviewed. Requiring the SHORTER side of the comparison to be at least
// a docker short ID makes an accidental collision implausible; exact equality
// is always allowed, since a full id needs no prefix reasoning.
func dockerIDPresent(current map[string]bool, id string, allowPrefix bool) bool {
	if current == nil {
		return false
	}
	if current[id] {
		return true
	}
	if !allowPrefix {
		return false
	}

	normalized := strings.TrimPrefix(id, "sha256:")
	if normalized == "" {
		return false
	}
	for candidate := range current {
		candidate = strings.TrimPrefix(candidate, "sha256:")
		if candidate == "" {
			continue
		}
		if candidate == normalized {
			return true
		}
		if min(len(candidate), len(normalized)) < dockerShortIDLen {
			continue
		}
		if strings.HasPrefix(candidate, normalized) || strings.HasPrefix(normalized, candidate) {
			return true
		}
	}
	return false
}

// parseDockerResourcePath splits a docker://<type>/<id>[/<name>] path the same
// way Clean does -- that shape is the stable contract between Scan, Clean and
// scriptgen.
func parseDockerResourcePath(path string) (resourceType string, id string, ok bool) {
	segments := strings.SplitN(strings.TrimPrefix(path, "docker://"), "/", 3)
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return "", "", false
	}
	return segments[0], segments[1], true
}

func dockerResourceType(path string) string {
	resourceType, _, ok := parseDockerResourcePath(path)
	if !ok {
		return ""
	}
	return resourceType
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

		containers = append(containers, container)
	}

	if len(inspectErrs) > 0 {
		return containers, fmt.Errorf("docker inspect failed for some containers: %s", strings.Join(inspectErrs, "; "))
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

	size, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return containerInfo{}, false
	}

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
