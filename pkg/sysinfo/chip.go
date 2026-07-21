package sysinfo

import "context"

// sysctlPath is a var (not const) so tests can point it at a nonexistent
// path to exercise the "binary not found" degrade-to-unknown path.
var sysctlPath = "/usr/sbin/sysctl"

// gatherChipModel returns the CPU brand string (e.g. "Apple M2 Pro").
func gatherChipModel(ctx context.Context) (string, bool) {
	out, ok := runCommand(ctx, sysctlPath, "-n", "machdep.cpu.brand_string")
	if !ok {
		return "", false
	}
	return parseChipBrand(out)
}

// parseChipBrand validates raw sysctl output for machdep.cpu.brand_string.
func parseChipBrand(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	return raw, true
}
