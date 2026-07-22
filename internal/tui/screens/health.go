package screens

import (
	"fmt"
	"strings"

	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/sysinfo"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// renderHealthBlock renders a single dimmed context line summarizing the
// machine (OS, chip, memory, and battery when running on a laptop). It sits
// near the top of the dashboard, framing the category list below it, rather
// than competing for attention as an actionable element. Unknown fields
// render as "unknown ..." rather than being silently omitted, except for
// battery which is only shown at all when the host is known to be a laptop.
func renderHealthBlock(info *sysinfo.Info, gathering bool) string {
	if gathering || info == nil {
		return styles.Muted.Render("Gathering machine info...")
	}

	var parts []string

	if osInfo := formatOSInfo(*info); info.OSVersionKnown && osInfo != "" {
		parts = append(parts, osInfo)
	} else {
		parts = append(parts, "unknown OS")
	}

	if info.ChipKnown {
		parts = append(parts, info.ChipModel)
	} else {
		parts = append(parts, "unknown chip")
	}

	if info.MemoryKnown {
		parts = append(parts, utils.FormatBytes(info.TotalMemory))
	} else {
		parts = append(parts, "unknown memory")
	}

	if info.IsLaptop {
		switch {
		case info.BatteryKnown && info.CycleCountKnown:
			parts = append(parts, fmt.Sprintf("Battery %d%% (%d cycles)", info.BatteryPercent, info.CycleCount))
		case info.BatteryKnown:
			parts = append(parts, fmt.Sprintf("Battery %d%%", info.BatteryPercent))
		default:
			parts = append(parts, "unknown battery")
		}
	}

	return styles.Muted.Render(strings.Join(parts, " · "))
}

func formatOSInfo(info sysinfo.Info) string {
	parts := make([]string, 0, 2)
	if info.OSName != "" {
		parts = append(parts, info.OSName)
	}
	if info.OSVersion != "" {
		parts = append(parts, info.OSVersion)
	}

	formatted := strings.Join(parts, " ")
	if info.OSBuild == "" {
		return formatted
	}
	if formatted == "" {
		return fmt.Sprintf("(%s)", info.OSBuild)
	}
	return fmt.Sprintf("%s (%s)", formatted, info.OSBuild)
}
