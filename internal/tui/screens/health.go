package screens

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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

	if osInfo := formatOSInfo(*info); info.OSVersionKnown && osInfo != "" {
		lines = append(lines, "OS: "+osInfo)
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

// composeWithHealthPanel places the health block in the top-right corner of
// the screen, to the right of the main content, when the terminal is wide
// enough for both. Below that threshold (or when the width isn't known yet,
// e.g. before the first tea.WindowSizeMsg) it falls back to stacking the
// health block underneath the main content, since squeezing a fixed-width
// side panel into a narrow terminal would just wrap/clip the category list.
func composeWithHealthPanel(left, health string, totalWidth int) string {
	leftWidth := lipgloss.Width(left)
	healthWidth := lipgloss.Width(health)

	if totalWidth <= 0 || totalWidth < leftWidth+healthWidth+2 {
		return left + "\n\n" + health + "\n"
	}

	gap := strings.Repeat(" ", totalWidth-leftWidth-healthWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, gap, health)
}
