package utils

import "fmt"

const (
	_  = iota             // ignore first value by assigning to blank identifier
	kB = 1 << (10 * iota) // 1024 = 1kb
	mB                    // 1048576 = 1mb
	gB                    // 1073741824 = 1gb
	tB                    // 1099511627776 = 1tb
)

// FormatBytes converts a byte count into a human-readable string with appropriate units (B, KB, MB, GB, TB).
func FormatBytes(bytes int64) string {
	switch {
	case bytes >= tB:
		return fmt.Sprintf("%.1f TB", float64(bytes)/float64(tB))
	case bytes >= gB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gB))
	case bytes >= mB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mB))
	case bytes >= kB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
