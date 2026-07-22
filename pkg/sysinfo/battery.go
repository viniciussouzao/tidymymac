package sysinfo

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ioregPath is a var (not const) so tests can point it at a nonexistent
// path to exercise the "binary not found" degrade-to-unknown path.
var ioregPath = "/usr/sbin/ioreg"

type batteryInfo struct {
	isLaptop      bool
	hostTypeKnown bool
	percent       int
	percentKnown  bool
	cycles        int
	cyclesKnown   bool
}

// gatherBattery detects host type and battery health via the presence and
// contents of the AppleSmartBattery IOKit service, the standard registry
// entry for the internal battery on every Mac laptop (and absent on every
// desktop Mac). This single call answers both "is this a laptop" and
// "battery stats" without a second exec call.
func gatherBattery(ctx context.Context) batteryInfo {
	out, ok := runCommand(ctx, ioregPath, "-r", "-c", "AppleSmartBattery")
	if !ok {
		// Indeterminate: fail closed, treat as non-laptop and hide battery.
		return batteryInfo{}
	}
	return parseBatteryIoreg(out)
}

var ioregIntPattern = func(key string) *regexp.Regexp {
	return regexp.MustCompile(`"` + key + `"\s*=\s*(-?\d+)`)
}

var (
	currentCapacityRe = ioregIntPattern("CurrentCapacity")
	maxCapacityRe     = ioregIntPattern("MaxCapacity")
	cycleCountRe      = ioregIntPattern("CycleCount")
)

// parseBatteryIoreg parses `ioreg -r -c AppleSmartBattery` output. Absence
// of any AppleSmartBattery block is treated as "not a laptop" (fail-closed),
// not as an error. Every field is defensively bounded; unparseable or
// out-of-range values degrade to unknown rather than being shown as valid.
func parseBatteryIoreg(raw string) batteryInfo {
	info := batteryInfo{}

	if !strings.Contains(raw, "AppleSmartBattery") {
		info.isLaptop = false
		info.hostTypeKnown = true // positively confirmed desktop/no-battery
		return info
	}

	info.isLaptop = true
	info.hostTypeKnown = true

	current, curOK := extractIoregInt(currentCapacityRe, raw)
	max, maxOK := extractIoregInt(maxCapacityRe, raw)
	if curOK && maxOK && max > 0 && current >= 0 {
		pct := int(math.Round(float64(current) / float64(max) * 100))
		if pct >= 0 && pct <= 100 {
			info.percent, info.percentKnown = pct, true
		}
	}

	if cycles, ok := extractIoregInt(cycleCountRe, raw); ok && cycles >= 0 {
		info.cycles, info.cyclesKnown = cycles, true
	}

	return info
}

// extractIoregInt finds the first regex match and parses it as an integer.
// Missing keys or unparseable values return ok=false, never panic.
func extractIoregInt(re *regexp.Regexp, raw string) (int, bool) {
	m := re.FindStringSubmatch(raw)
	if len(m) != 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}
