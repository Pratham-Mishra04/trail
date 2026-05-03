// Package store persists capture sessions and serves reads against them.
//
// The Store interface is the single boundary between persistence and the rest
// of the codebase. NewJSONL returns the filesystem-backed implementation that
// writes one .jsonl file per session under ~/.config/trail/sessions/.
// Alternate backends (SQLite, remote, etc.) implement the same interface and
// nothing in capture/, cli/, or mcp/ has to change.
package store

import (
	"context"
	"regexp"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
)

// Store is the persistence interface.
type Store interface {
	// Create generates a new session, writes the meta header, and returns a
	// writable handle. The caller's meta.SessionID and meta.FilePath are
	// overwritten by the implementation.
	Create(meta logentry.MetaHeader) (Session, error)

	// List returns every session known to the store, sorted active-first then
	// by StartedAt descending. Files that fail to parse are skipped (logged
	// to stderr) rather than failing the whole call.
	List(ctx context.Context) ([]SessionInfo, error)

	// Get returns metadata for one session. Returns os.ErrNotExist if missing.
	Get(ctx context.Context, sessionID string) (SessionInfo, error)

	// Delete removes a session. Returns os.ErrNotExist if missing.
	Delete(ctx context.Context, sessionID string) error

	// DeleteAll removes every session. Returns count deleted and the first
	// error encountered (if any), continuing through the rest.
	DeleteAll(ctx context.Context) (int, error)

	// Read applies f to the session's entries and returns the matching slice
	// plus the total match count (before limit/page).
	Read(ctx context.Context, sessionID string, f Filter) (ReadResult, error)
}

// Session is a writable handle to one capture session. Owned by the capturer
// for the lifetime of the capture process — single-writer invariant.
type Session interface {
	// Append writes one LogEntry. Sets entry.Line from an in-memory counter.
	// Safe to call concurrently from multiple goroutines (e.g. one per stream).
	Append(e logentry.LogEntry) error

	// Path returns the absolute file path (also stored in meta.FilePath).
	Path() string

	// SessionID returns the UUID assigned at Create time.
	SessionID() string

	// Close flushes and closes the session. If Ephemeral was set on the meta,
	// also deletes the underlying file. Idempotent.
	Close() error
}

// SessionInfo is the read-side projection: meta + derived liveness.
type SessionInfo struct {
	Meta          logentry.MetaHeader
	IsActive      bool       // kill(capturer_pid, 0) == nil (or EPERM)
	ApproxEndedAt *time.Time // nil if active, else file mtime
}

// Filter is the input shape for Read. nil pointers mean "no filter on that
// dimension." Mutual exclusivity of (StartTime/EndTime), StartLine/EndLine
// is enforced by the *caller* (CLI or MCP handler), not the store.
type Filter struct {
	Limit     int            // 0 = unlimited
	Page      int            // 1-based; 0 → 1
	Query     *regexp.Regexp // nil = no regex
	Level     *string        // nil = no level filter
	StartTime *time.Time
	EndTime   *time.Time
	StartLine *int64
	EndLine   *int64
	Order     string // "newest" (default) | "oldest"
}

// ReadResult is what Read returns.
type ReadResult struct {
	Entries      []logentry.LogEntry
	TotalMatched int // count before limit/page applied
}

// Order constants.
const (
	OrderNewest = "newest"
	OrderOldest = "oldest"
)

// NewJSONL constructs the filesystem-backed store, rooted at
// ~/.config/trail/sessions/. Errors out if os.UserHomeDir() fails or the
// sessions directory can't be created.
func NewJSONL() (Store, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	return &jsonlStore{dir: dir}, nil
}
