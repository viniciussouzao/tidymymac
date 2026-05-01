package cleaner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// totalSize calculates the total size of a list of FileEntry objects.
func totalSize(entries []FileEntry) int64 {
	var total int64
	for _, entry := range entries {
		total += entry.Size
	}

	return total
}

// parseDockerSize parses a Docker size string like "1.2GB", "500MB", "0B".
func parseDockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0B" {
		return 0
	}

	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "TB"):
		multiplier = 1 << 40
		s = strings.TrimSuffix(s, "TB")
	case strings.HasSuffix(s, "GB"):
		multiplier = 1 << 30
		s = strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		multiplier = 1 << 20
		s = strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "kB"):
		multiplier = 1 << 10
		s = strings.TrimSuffix(s, "kB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}

	return int64(val * float64(multiplier))
}

func getPathSize(ctx context.Context, path string) (int64, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	cmd := exec.CommandContext(ctx, "du", "-sk", path)

	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	// saída: "12345\t/path"
	var sizeKB int64
	_, err = fmt.Sscanf(string(out), "%d", &sizeKB)
	if err != nil {
		return 0, err
	}

	return sizeKB * 1024, nil
}

type permissionStatus struct {
	FullDiskAccess bool
	CheckedPaths   map[string]error
}

func checkFullDiskAccess(homedir string) permissionStatus {
	paths := []string{
		filepath.Join(homedir, ".Trash"),
		filepath.Join(homedir, "Library"),
	}

	result := permissionStatus{
		FullDiskAccess: true,
		CheckedPaths:   make(map[string]error),
	}

	for _, p := range paths {
		err := testReadDir(p)
		if err != nil {
			result.CheckedPaths[p] = err

			if isPermissionError(err) {
				result.FullDiskAccess = false
			}
		}
	}

	return result
}

func testReadDir(path string) error {
	_, err := os.ReadDir(path)
	return err
}

func isPermissionError(err error) bool {
	return errors.Is(err, os.ErrPermission)
}
