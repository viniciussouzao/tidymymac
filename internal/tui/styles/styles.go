package styles

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF6B6B")).
		MarginBottom(1)

	SuccessTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#10B981")).
			MarginBottom(1)

	Subtitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))

	Selected = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7C3AED")).
			Bold(true)

	Cursor = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B"))

	Size = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#10B981"))

	SizeLarge = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")).
			Bold(true)

	SizeMedium = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))

	Help = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#626262")).
		MarginTop(1)

	HelpBlock = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			Margin(1)

	Error = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#EF4444"))

	Success = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#10B981")).
		Bold(true)

	Warning = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F59E0B"))

	DryRunBanner = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B")).
			Bold(true).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#F59E0B")).
			Padding(0, 1).
			MarginBottom(1)

	Dim = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#555555"))

	Muted = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#AAAAAA"))

	CategoryHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7C3AED"))

	More = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Italic(true)

	Highlight = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00B881")).
			Bold(true)

	Plain = lipgloss.NewStyle()

	StorageLabel = lipgloss.NewStyle().
			Italic(true).
			Underline(true)

	Logo = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B")).
		Bold(true)
)

// SizeStyled returns the size string styled according to the size thresholds.
func SizeStyled(size int64, text string) string {
	const gb = 1 << 30
	const mb100 = 100 << 20
	switch {
	case size >= gb:
		return SizeLarge.Render(text)
	case size >= mb100:
		return SizeMedium.Render(text)
	default:
		return Size.Render(text)
	}
}

// SizeLevelStyle returns the style corresponding to the size thresholds.
func SizeLevelStyle(size int64) lipgloss.Style {
	const gb = 1 << 30
	const mb100 = 100 << 20
	switch {
	case size >= gb:
		return SizeLarge
	case size >= mb100:
		return SizeMedium
	default:
		return Size
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

// RenderLogo renders the ASCII art logo with colors.
func RenderLogo() string {
	colors := []string{"#FF6B6B", "#F59E0B", "#10B981", "#7C3AED"}
	lines := strings.Split(strings.Trim(asciiLogo, "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}

	var out []string
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

// RenderTagLine renders a subtitle below the logo.
func RenderTagLine() string {
	return Subtitle.Render("macOS Storage Cleanup Tool")
}
