// Package logentry defines the on-disk schema for trail sessions.
//
// The JSONL file format is the public contract — agents and humans read it
// directly. The Go struct definitions in this package are internal; bumping
// them is a non-breaking change as long as the JSON tags stay stable.
package logentry

import "time"

const (
	SourceRun    = "run"
	SourceDocker = "docker"

	StreamStdout = "stdout"
	StreamStderr = "stderr"

	LevelError   = "error"
	LevelWarn    = "warn"
	LevelInfo    = "info"
	LevelDebug   = "debug"
	LevelUnknown = "unknown"

	// TypeMeta marks the first line of every session file.
	TypeMeta = "meta"
)

// validLevels is the canonical set used to validate user-supplied --level /
// filters.level inputs. "all" is included as the explicit "no filter" sentinel.
var validLevels = map[string]struct{}{
	LevelError:   {},
	LevelWarn:    {},
	LevelInfo:    {},
	LevelDebug:   {},
	LevelUnknown: {},
	"all":        {},
}

// IsValidLevel reports whether s is a recognized level string. Use it to
// validate user input from CLI flags and MCP tool arguments. Pass an
// already-lowercased string — case normalization is the caller's job.
func IsValidLevel(s string) bool {
	_, ok := validLevels[s]
	return ok
}

// LogEntry is one captured stdout/stderr line.
type LogEntry struct {
	Line      int64     `json:"line"`
	Stream    string    `json:"stream"`
	Level     string    `json:"level"`
	Raw       string    `json:"raw"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// MetaHeader is the first line of every session file. Type is always TypeMeta.
type MetaHeader struct {
	Type        string    `json:"type"`
	SessionID   string    `json:"session_id"`
	Name        string    `json:"name"`
	Source      string    `json:"source"`
	CapturerPID int       `json:"capturer_pid"`
	CapturedPID int       `json:"captured_pid,omitempty"`
	Container   string    `json:"container,omitempty"`
	Command     string    `json:"command,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	FilePath    string    `json:"file_path"`
	Ephemeral   bool      `json:"ephemeral"`
	Trail       string    `json:"trail_version"`
}
