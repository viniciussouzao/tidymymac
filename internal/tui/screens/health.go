package screens

import (
	"fmt"
	"strings"

	"github.com/viniciussouzao/tidymymac/internal/tui/styles"
	"github.com/viniciussouzao/tidymymac/pkg/sysinfo"
	"github.com/viniciussouzao/tidymymac/pkg/utils"
)

// renderHealthBlock renders the machine health info box (OS, chip, memory,
// and battery when running on a laptop). Unknown fields render as a dimmed
// "unknown" rather than being omitted, except for battery which is only
// shown at all when the host is known to be a laptop.
func renderHealthBlock(info *sysinfo.Info, gathering bool) string {
	if gathering || info == nil {
		return styles.HealthBox.Render(styles.Dim.Render("Gathering machine info..."))
	}

	var lines []string

	if info.OSVersionKnown {
		lines = append(lines, fmt.Sprintf("OS: %s %s (%s)", info.OSName, info.OSVersion, info.OSBuild))
	} else {
		lines = append(lines, "OS: "+styles.Dim.Render("unknown"))
	}

	if info.ChipKnown {
		lines = append(lines, fmt.Sprintf("Chip: %s", info.ChipModel))
	} else {
		lines = append(lines, "Chip: "+styles.Dim.Render("unknown"))
	}

	if info.MemoryKnown {
		lines = append(lines, fmt.Sprintf("Memory: %s", utils.FormatBytes(info.TotalMemory)))
	} else {
		lines = append(lines, "Memory: "+styles.Dim.Render("unknown"))
	}

	if info.IsLaptop {
		switch {
		case info.BatteryKnown && info.CycleCountKnown:
			lines = append(lines, fmt.Sprintf("Battery: %d%% (%d cycles)", info.BatteryPercent, info.CycleCount))
		case info.BatteryKnown:
			lines = append(lines, fmt.Sprintf("Battery: %d%%", info.BatteryPercent))
		default:
			lines = append(lines, "Battery: "+styles.Dim.Render("unknown"))
		}
	}

	return styles.HealthBox.Render(strings.Join(lines, "\n"))
}
