package cli

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
)

// ANSI 256 color codes — palette matches bifrost/cli for visual consistency
// across the suite. lipgloss auto-degrades to plain text when stdout is not
// a TTY (piped to less, jq, file), so the same code path works in both modes.
var (
	colorError   = lipgloss.Color("196") // bright red
	colorWarn    = lipgloss.Color("214") // orange
	colorInfo    = lipgloss.Color("39")  // blue
	colorDebug   = lipgloss.Color("245") // gray
	colorUnknown = lipgloss.Color("241") // darker gray

	colorActive = lipgloss.Color("42")  // green (matches bifrost selected)
	colorEnded  = lipgloss.Color("245") // gray

	colorDim    = lipgloss.Color("245") // body dim text (timestamps, etc.)
	colorHint   = lipgloss.Color("241") // even dimmer (footnotes, headers)
	colorBorder = lipgloss.Color("240") // table borders

	styleError   = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	styleWarn    = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
	styleInfo    = lipgloss.NewStyle().Foreground(colorInfo)
	styleDebug   = lipgloss.NewStyle().Foreground(colorDebug)
	styleUnknown = lipgloss.NewStyle().Foreground(colorUnknown)

	styleActive = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	styleEnded  = lipgloss.NewStyle().Foreground(colorEnded)

	styleDim    = lipgloss.NewStyle().Foreground(colorDim)
	styleHint   = lipgloss.NewStyle().Foreground(colorHint).Italic(true)
	styleBorder = lipgloss.NewStyle().Foreground(colorBorder)
)

// levelBadge renders a fixed-width, color-coded level token suitable for
// columnar log output.
func levelBadge(level string) string {
	const width = 5
	switch level {
	case logentry.LevelError:
		return styleError.Width(width).Render("ERROR")
	case logentry.LevelWarn:
		return styleWarn.Width(width).Render("WARN")
	case logentry.LevelInfo:
		return styleInfo.Width(width).Render("INFO")
	case logentry.LevelDebug:
		return styleDebug.Width(width).Render("DEBUG")
	default:
		return styleUnknown.Width(width).Render("?")
	}
}

// statusBadge renders an active/ended status with appropriate color.
func statusBadge(active bool) string {
	if active {
		return styleActive.Render("active")
	}
	return styleEnded.Render("ended")
}
