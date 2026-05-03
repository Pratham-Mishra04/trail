// File banner renders the trail startup banner shown when `trail run` or
// `trail docker` begins capturing. The banner mirrors the visual identity of
// the project logo (off-white wordmark, amber dots-and-chevron mark).
//
// All output goes to stderr. When stderr isn't a TTY (piped output, CI,
// editor-spawned MCP wiring), PrintBanner falls back to a single plain line so
// downstream consumers don't receive terminal escape sequences.
package capture

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var version = "dev"

// SetVersion is called once from main.go with the value baked in via ldflags.
func SetVersion(v string) { version = v }

const wordmark = `████████╗██████╗  █████╗ ██╗██╗
╚══██╔══╝██╔══██╗██╔══██╗██║██║
   ██║   ██████╔╝███████║██║██║
   ██║   ██╔══██╗██╔══██║██║██║
   ██║   ██║  ██║██║  ██║██║███████╗
   ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝╚══════╝`

// Palette matched to docs/logo-with-text-dark.png:
//
//	dots + chevron → amber accent
//	wordmark       → off-white
//	labels + path  → mid/dark gray
var (
	colAccent = lipgloss.Color("214")
	colBright = lipgloss.Color("231")
	colDim    = lipgloss.Color("244")
	colMuted  = lipgloss.Color("238")

	styleAccent = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleBright = lipgloss.NewStyle().Foreground(colBright).Bold(true)
	styleDim    = lipgloss.NewStyle().Foreground(colDim)
	styleMuted  = lipgloss.NewStyle().Foreground(colMuted)
)

// PrintBanner renders the startup banner to stderr. Falls back to a single plain
// line when stderr isn't a TTY.
func PrintBanner(sessionName, sessionID, filePath string) {
	w := os.Stderr
	if !isTTY(w) {
		fmt.Fprintf(w, "trail: capturing %q → %s (file: %s)\n", sessionName, sessionID, filePath)
		return
	}
	render(w, sessionName, sessionID, filePath)
}

func render(w io.Writer, sessionName, sessionID, filePath string) {
	mark := styleAccent.Render("⬤  ⬤  ⬤  ▶")

	fmt.Fprintln(w)
	fmt.Fprintln(w, styleBright.Render(wordmark))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "   "+mark+"    "+styleDim.Render(version))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "   %s  %s\n", styleDim.Render("session name"), styleBright.Render(sessionName))
	fmt.Fprintf(w, "   %s    %s\n", styleDim.Render("session id"), styleAccent.Render(sessionID))
	fmt.Fprintf(w, "   %s     %s\n", styleDim.Render("file path"), styleMuted.Render(filePath))
	fmt.Fprintln(w)
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
