package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF6B6B")).
			MarginBottom(1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7C3AED")).
			Bold(true)

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B"))

	sizeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981"))

	sizeLargeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")).
			Bold(true)

	sizeMediumStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginTop(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444"))

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981")).
			Bold(true)

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))

	dryRunBannerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F59E0B")).
				Bold(true).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#F59E0B")).
				Padding(0, 1).
				MarginBottom(1)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555555"))

	logoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B")).
			Bold(true)
)

// SizeStyled returns the size string styled according to the size thresholds
func SizeStyled(size int64, text string) string {
	const gb = 1 << 30
	const mb100 = 100 << 20
	switch {
	case size >= gb:
		return sizeLargeStyle.Render(text)
	case size >= mb100:
		return sizeMediumStyle.Render(text)
	default:
		return sizeStyle.Render(text)
	}
}

// generated at https://patorjk.com/software/taag/#p=display&f=DOS+Rebel&t=Tidy+My+Mac&x=none&v=4&h=4&w=80&we=false
const asciiLogo = `
 ███████████  ███      █████               ██████   ██████               ██████   ██████                   
░█░░░███░░░█ ░░░      ░░███               ░░██████ ██████               ░░██████ ██████                    
░   ░███  ░  ████   ███████  █████ ████    ░███░█████░███  █████ ████    ░███░█████░███   ██████    ██████ 
    ░███    ░░███  ███░░███ ░░███ ░███     ░███░░███ ░███ ░░███ ░███     ░███░░███ ░███  ░░░░░███  ███░░███
    ░███     ░███ ░███ ░███  ░███ ░███     ░███ ░░░  ░███  ░███ ░███     ░███ ░░░  ░███   ███████ ░███ ░░░ 
    ░███     ░███ ░███ ░███  ░███ ░███     ░███      ░███  ░███ ░███     ░███      ░███  ███░░███ ░███  ███
    █████    █████░░████████ ░░███████     █████     █████ ░░███████     █████     █████░░████████░░██████ 
   ░░░░░    ░░░░░  ░░░░░░░░   ░░░░░███    ░░░░░     ░░░░░   ░░░░░███    ░░░░░     ░░░░░  ░░░░░░░░  ░░░░░░  
                              ███ ░███                      ███ ░███                                       
                             ░░██████                      ░░██████                                        
                              ░░░░░░                        ░░░░░░                                         
`

// Logo renders the ASCII art logo with colors
func Logo() string {
	colors := []string{"#FF6B6B", "#F59E0B", "#10B981", "#7C3AED"}
	lines := strings.Split(strings.Trim(asciiLogo, "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}

	var out []string
	// Cycle through the colors for each line
	steps := len(lines) - 1
	if steps <= 0 {
		steps = 1
	}

	for i, line := range lines {
		idx := i * (len(colors) - 1) / steps
		color := colors[idx]
		out = append(out, lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(line))
	}

	return strings.Join(out, "\n")

}

// TagLine renders a subtitle below the logo
func TagLine() string {
	return subtitleStyle.Render("macOS Storage Cleanup Tool")
}
