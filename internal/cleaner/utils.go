package cleaner

import (
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
