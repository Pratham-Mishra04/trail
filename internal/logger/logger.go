// Package logger is trail's own diagnostic logger — distinct from the
// captured application logs that the rest of the codebase deals with.
//
// All output goes to stderr, which is critical: in `trail mcp` mode stdout
// is the JSON-RPC protocol channel and any byte written there corrupts the
// stream. Lipgloss auto-degrades to plain text when stderr is not a TTY
// (piped to a file, captured by an editor's MCP wiring), so the same code
// works in both interactive and non-interactive contexts.
package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Level controls how much output the logger emits.
type Level int

const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
)

// Pretty styles match the captured-log theme in internal/cli/theme.go so the
// two visual surfaces feel like one tool.
var (
	styleError = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	styleWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleInfo  = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleDebug = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	styleTs    = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	stylePath  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

// Logger is a small structured logger with a fixed pretty format. Safe for
// concurrent use from multiple goroutines (writes are serialized by a mutex
// so two log lines don't interleave bytes on the same writer). Level is
// fixed at construction — replace the singleton via SetDefault to change it.
type Logger struct {
	mu    sync.Mutex
	out   io.Writer
	level Level
}

// New constructs a logger writing to out. Pass os.Stderr in production.
// level controls the minimum severity that gets emitted.
func New(out io.Writer, level Level) *Logger {
	return &Logger{out: out, level: level}
}

// Path renders a file path with the consistent dim style. Use it when you
// want to embed a path inside a log message.
func Path(p string) string { return stylePath.Render(p) }

func (l *Logger) Error(format string, args ...any) {
	l.emit(LevelError, "[error]", styleError, format, args...)
}

func (l *Logger) Warn(format string, args ...any) {
	l.emit(LevelWarn, "[warn] ", styleWarn, format, args...)
}

func (l *Logger) Info(format string, args ...any) {
	l.emit(LevelInfo, "[info] ", styleInfo, format, args...)
}

func (l *Logger) Debug(format string, args ...any) {
	l.emit(LevelDebug, "[debug]", styleDebug, format, args...)
}

func (l *Logger) emit(lvl Level, label string, style lipgloss.Style, format string, args ...any) {
	if lvl > l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	ts := styleTs.Render(time.Now().Format("15:04:05"))
	tag := style.Render(label)

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.out, "%s  %s  %s\n", ts, tag, msg)
}

// --- package-level default ---

var (
	defaultMu sync.RWMutex
	defaultLg = New(os.Stderr, LevelInfo)
)

// Default returns the package-level singleton logger. Most call sites should
// use this rather than constructing their own Logger; main.go configures the
// default once at startup.
func Default() *Logger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultLg
}

// SetDefault replaces the package-level singleton. main.go calls this once
// during startup to wire the logger to the right writer + level for the
// current subcommand. Tests can swap in a buffer-backed logger.
func SetDefault(l *Logger) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultLg = l
}
