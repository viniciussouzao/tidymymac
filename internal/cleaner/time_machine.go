package cleaner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	timeMachineSnapshotPrefix = "com.apple.TimeMachine."
	timeMachineSnapshotSuffix = ".local"
)

type TimeMachineCleaner struct{}

func NewTimeMachineCleaner() *TimeMachineCleaner {
	return &TimeMachineCleaner{}
}

func (c *TimeMachineCleaner) Category() Category { return CategoryTimeMachineSnapshots }

func (c *TimeMachineCleaner) Name() string { return "Time Machine Snapshots" }

func (c *TimeMachineCleaner) Description() string {
	return "Local Time Machine snapshots stored on disk"
}

func (c *TimeMachineCleaner) RequiresSudo() bool { return true }

func (c *TimeMachineCleaner) Scan(ctx context.Context, progress func(ScanProgress)) (*ScanResult, error) {
	start := time.Now()
	result := &ScanResult{
		Category:  CategoryTimeMachineSnapshots,
		SizeKnown: true,
	}

	if _, err := exec.LookPath("tmutil"); err != nil {
		result.Duration = time.Since(start)
		return result, nil
	}

	out, err := exec.CommandContext(ctx, "tmutil", "listlocalsnapshots", "/").Output()
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("tmutil listlocalsnapshots: %w", err))
		result.Duration = time.Since(start)
		return result, err
	}

	for _, snapshot := range parseLocalSnapshots(string(out)) {
		result.Entries = append(result.Entries, FileEntry{
			Path:     snapshot,
			Category: CategoryTimeMachineSnapshots,
		})
		result.TotalFiles++
	}

	if result.TotalFiles > 0 {
		result.SizeKnown = false
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (c *TimeMachineCleaner) Clean(ctx context.Context, entries []FileEntry, dryRun bool, progress func(CleanProgress)) (*CleanResult, error) {
	start := time.Now()
	result := &CleanResult{
		Category: CategoryTimeMachineSnapshots,
		DryRun:   dryRun,
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			result.Duration = time.Since(start)
			return result, err
		}

		snapshotDate, ok := snapshotDateFromEntry(entry.Path)
		if !ok {
			result.Errors = append(result.Errors, fmt.Errorf("invalid Time Machine snapshot entry: %s", entry.Path))
			continue
		}

		if !dryRun {
			out, err := exec.CommandContext(ctx, "tmutil", "deletelocalsnapshots", snapshotDate).CombinedOutput()
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("tmutil deletelocalsnapshots %s: %s: %w", snapshotDate, strings.TrimSpace(string(out)), err))
				continue
			}
		}

		result.FilesDeleted++

		if progress != nil {
			progress(CleanProgress{
				Category:     CategoryTimeMachineSnapshots,
				FilesDeleted: result.FilesDeleted,
				FilesTotal:   len(entries),
				BytesDeleted: result.BytesFreed,
				BytesTotal:   0,
				CurrentFile:  entry.Path,
			})
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

func parseLocalSnapshots(output string) []string {
	lines := strings.Split(output, "\n")
	snapshots := make([]string, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, timeMachineSnapshotPrefix) {
			continue
		}
		snapshots = append(snapshots, line)
	}

	return snapshots
}

func snapshotDateFromEntry(entry string) (string, bool) {
	if !strings.HasPrefix(entry, timeMachineSnapshotPrefix) || !strings.HasSuffix(entry, timeMachineSnapshotSuffix) {
		return "", false
	}

	date := strings.TrimPrefix(entry, timeMachineSnapshotPrefix)
	date = strings.TrimSuffix(date, timeMachineSnapshotSuffix)
	if date == "" {
		return "", false
	}

	return date, true
}
