package sysinfo

import (
	"context"
	"strconv"
	"strings"
)

// swVersPath is a var (not const) so tests can point it at a nonexistent
// path to exercise the "binary not found" degrade-to-unknown path.
var swVersPath = "/usr/bin/sw_vers"

// macOSMarketingNames maps a major version number to its marketing name.
// This table goes stale after each yearly macOS release; add a line per
// release rather than treating an unmapped version as an error - unknown
// major versions simply omit the marketing name.
var macOSMarketingNames = map[int]string{
	26: "Tahoe",
	15: "Sequoia",
	14: "Sonoma",
	13: "Ventura",
	12: "Monterey",
	11: "Big Sur",
}

type osVersionInfo struct {
	name    string // e.g. "macOS Tahoe"; marketing name omitted if unmapped
	version string // e.g. "26.5.2"
	build   string // e.g. "25F84"
}

// gatherOSVersion returns OS name/version/build via `sw_vers`.
func gatherOSVersion(ctx context.Context) (osVersionInfo, bool) {
	out, ok := runCommand(ctx, swVersPath)
	if !ok {
		return osVersionInfo{}, false
	}
	return parseSwVers(out)
}

// parseSwVers parses `sw_vers` output, which is a small set of
// "Key:\tValue" lines (ProductName, ProductVersion, BuildVersion). Missing
// individual fields degrade gracefully rather than failing the whole probe;
// only a completely unparseable/empty output is treated as unknown.
func parseSwVers(raw string) (osVersionInfo, bool) {
	fields := map[string]string{}
	for line := range strings.SplitSeq(raw, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			continue
		}
		fields[key] = value
	}

	productName := fields["ProductName"]
	version := fields["ProductVersion"]
	build := fields["BuildVersion"]

	if productName == "" && version == "" && build == "" {
		return osVersionInfo{}, false
	}

	name := productName
	if major, ok := majorVersion(version); ok {
		if marketing, known := macOSMarketingNames[major]; known {
			name = strings.TrimSpace(productName + " " + marketing)
		}
	}

	return osVersionInfo{name: name, version: version, build: build}, true
}

// majorVersion extracts the leading integer component of a version string
// like "26.5.2" -> 26. Unparseable input returns ok=false.
func majorVersion(version string) (int, bool) {
	if version == "" {
		return 0, false
	}
	major := strings.SplitN(version, ".", 2)[0]
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0, false
	}
	return n, true
}
