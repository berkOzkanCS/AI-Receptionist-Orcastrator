package tui

import "github.com/charmbracelet/lipgloss"

// Palette — mirrors the STT-system house style so the orchestrator dashboard
// looks of a piece with the rest of the pipeline. Retheme here.
const (
	colDim       = "#7a7a7a"
	colBright    = "#f5f5f5"
	colTitle     = "#7dd3fc" // cyan pane titles
	colAccent    = "#c4b5fd" // violet accents
	colCategory  = "#fde68a" // amber labels
	colMuted     = "#525252" // borders, placeholders
	colOK        = "#a3e635" // good / ready
	colWarn      = "#fbbf24" // mid latency
	colErr       = "#fb7185" // error / dead
	colStatusBg  = "#1f2937" // status bar background
)

var (
	styleStatusBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colBright)).
			Background(lipgloss.Color(colStatusBg)).
			Padding(0, 1)

	styleDot = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colOK)).
			Background(lipgloss.Color(colStatusBg)).
			Bold(true)

	stylePaneBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colMuted)).
			Padding(0, 1)

	styleTitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colTitle)).
			Bold(true)

	styleHeader = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colMuted)).
			Bold(true)

	styleDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colDim))

	styleBright = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colBright))

	styleAccent = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colAccent)).
			Bold(true)

	styleCategory = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colCategory))

	stylePlaceholder = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colMuted)).
				Italic(true)

	styleOK   = lipgloss.NewStyle().Foreground(lipgloss.Color(colOK))
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color(colWarn))
	styleErr  = lipgloss.NewStyle().Foreground(lipgloss.Color(colErr))
)

// latencyStyle color-codes a millisecond value green/amber/red by thresholds.
func latencyStyle(ms float64, warn, bad float64) lipgloss.Style {
	switch {
	case ms >= bad:
		return styleErr
	case ms >= warn:
		return styleWarn
	default:
		return styleOK
	}
}
