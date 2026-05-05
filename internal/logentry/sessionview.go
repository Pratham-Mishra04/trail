package logentry

import "time"

// SessionView is the read-side projection of a session: meta header fields
// promoted to the top level, plus derived liveness (is_active,
// approx_ended_at). This is the JSON shape returned by both `trail sessions
// --json` and the MCP `list_sessions` tool — the contract is the same so
// agents and humans see identical structures.
//
// Lives in this package (rather than alongside one consumer) so it can't
// drift between cli/ and mcp/.
type SessionView struct {
	SessionID     string     `json:"session_id"`
	Name          string     `json:"name"`
	Source        string     `json:"source"`
	CapturedPID   int        `json:"captured_pid,omitempty"`
	Container     string     `json:"container,omitempty"`
	Command       string     `json:"command,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	IsActive      bool       `json:"is_active"`
	ApproxEndedAt *time.Time `json:"approx_ended_at"`
	FilePath      string     `json:"file_path"`
	SizeBytes     int64      `json:"size_bytes"`
}

// NewSessionView projects a MetaHeader plus liveness fields into the
// JSON-friendly SessionView shape. approxEndedAt is nil for active sessions.
func NewSessionView(m MetaHeader, isActive bool, approxEndedAt *time.Time, sizeBytes int64) SessionView {
	return SessionView{
		SessionID:     m.SessionID,
		Name:          m.Name,
		Source:        m.Source,
		CapturedPID:   m.CapturedPID,
		Container:     m.Container,
		Command:       m.Command,
		StartedAt:     m.StartedAt,
		IsActive:      isActive,
		ApproxEndedAt: approxEndedAt,
		FilePath:      m.FilePath,
		SizeBytes:     sizeBytes,
	}
}
