package sysinfo

import (
	"context"
	"strconv"
)

// gatherTotalMemory returns total physical memory in bytes.
func gatherTotalMemory(ctx context.Context) (int64, bool) {
	out, ok := runCommand(ctx, sysctlPath, "-n", "hw.memsize")
	if !ok {
		return 0, false
	}
	return parseMemSize(out)
}

// parseMemSize validates raw sysctl output for hw.memsize.
func parseMemSize(raw string) (int64, bool) {
	mem, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || mem <= 0 {
		return 0, false
	}
	return mem, true
}
