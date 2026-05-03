package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/logger"
)

// jsonlStore is the filesystem-backed Store implementation.
type jsonlStore struct {
	dir string // absolute path to ~/.config/trail/sessions
}

// sessionIDPattern matches a UUIDv4 (the only format we generate). Validating
// inputs against this prevents path traversal in Delete/Read/Get — without
// the check, "../etc/passwd" would resolve to <dir>/../etc/passwd.jsonl.
var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validateSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("invalid session id %q (expected UUID, e.g. 9c5e1b7e-...-...-...-...)", id)
	}
	return nil
}

// jsonlSession is the writable handle returned by jsonlStore.Create.
type jsonlSession struct {
	mu       sync.Mutex
	file     *os.File
	meta     logentry.MetaHeader
	nextLine int64
	closed   bool
}

// pathFor returns the absolute file path for a session ID.
func (s *jsonlStore) pathFor(sessionID string) string {
	return filepath.Join(s.dir, sessionID+".jsonl")
}

func (s *jsonlStore) Create(meta logentry.MetaHeader) (Session, error) {
	meta.SessionID = uuid.NewString()
	meta.Type = logentry.TypeMeta
	meta.FilePath = s.pathFor(meta.SessionID)

	// O_EXCL guards against the (vanishingly unlikely) UUID collision.
	f, err := os.OpenFile(meta.FilePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create session file: %w", err)
	}
	if err := writeMeta(f, meta); err != nil {
		_ = f.Close()
		_ = os.Remove(meta.FilePath)
		return nil, err
	}
	return &jsonlSession{file: f, meta: meta, nextLine: 1}, nil
}

func (s *jsonlStore) List(ctx context.Context) ([]SessionInfo, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	out := make([]SessionInfo, 0, len(entries))
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		sessionID := strings.TrimSuffix(e.Name(), ".jsonl")
		info, err := s.readInfo(sessionID)
		if err != nil {
			logger.Default().Warn("skipping session %s: %v", sessionID, err)
			continue
		}
		out = append(out, info)
	}

	// active first, then by StartedAt desc.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsActive != out[j].IsActive {
			return out[i].IsActive
		}
		return out[i].Meta.StartedAt.After(out[j].Meta.StartedAt)
	})
	return out, nil
}

func (s *jsonlStore) Get(ctx context.Context, sessionID string) (SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return SessionInfo{}, err
	}
	if err := validateSessionID(sessionID); err != nil {
		return SessionInfo{}, err
	}
	return s.readInfo(sessionID)
}

func (s *jsonlStore) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	if err := os.Remove(s.pathFor(sessionID)); err != nil {
		return fmt.Errorf("delete session %s: %w", sessionID, err)
	}
	return nil
}

func (s *jsonlStore) DeleteAll(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, fmt.Errorf("read sessions dir: %w", err)
	}
	var firstErr error
	count := 0
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, e.Name())); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		count++
	}
	return count, firstErr
}

func (s *jsonlStore) Read(ctx context.Context, sessionID string, f Filter) (ReadResult, error) {
	if err := validateSessionID(sessionID); err != nil {
		return ReadResult{}, err
	}
	// Single-source the Order default so callers can pass Filter{} without
	// caring; reject anything outside the enum to catch drift early.
	switch f.Order {
	case "":
		f.Order = OrderNewest
	case OrderNewest, OrderOldest:
		// ok
	default:
		return ReadResult{}, fmt.Errorf("invalid order %q (allowed: newest|oldest)", f.Order)
	}

	// Fast path: when the caller wants the newest N entries with no upper-
	// bounded filter, walk the file backwards from the end. Bounded by Limit
	// instead of file size — turns "newest 100 from a 100K-entry file" from
	// O(file) into O(N).
	if readReverseEligible(f) {
		return s.readReverse(ctx, sessionID, f)
	}

	path := s.pathFor(sessionID)
	file, err := os.Open(path)
	if err != nil {
		return ReadResult{}, err
	}
	defer func() { _ = file.Close() }()

	br := bufio.NewReaderSize(file, 64*1024)
	matched := make([]logentry.LogEntry, 0, 256)

	first := true
	lineNum := int64(0) // file-line counter, used in malformed-entry warnings
	for {
		if err := ctx.Err(); err != nil {
			return ReadResult{}, err
		}
		line, readErr := br.ReadBytes('\n')
		// A non-newline-terminated trailing fragment is silently dropped — it's
		// a partial write from a capturer that's still mid-append.
		if len(line) > 0 && line[len(line)-1] == '\n' {
			lineNum++
			line = line[:len(line)-1]
			if first {
				first = false // line 1 is the meta header; skip it
				continue
			}
			// Cheap pre-filter via gjson before the expensive sonic.Unmarshal.
			// gjson scans the JSON without building a struct, so we can
			// reject most non-matching entries without paying full decode cost.
			// Also handles time/line early-exit since both are upper-bounded.
			outcome, ok := preFilter(line, f)
			if !ok {
				logger.Default().Warn("session %s: skipping malformed entry at file line %d", sessionID, lineNum)
				continue
			}
			if outcome == preStop {
				goto done
			}
			if outcome == preReject {
				continue
			}
			// Pre-filter passed (or wasn't applicable for some fields) — full decode + final check.
			entry, ok := decodeEntry(line)
			if !ok {
				logger.Default().Warn("session %s: skipping malformed entry at file line %d", sessionID, lineNum)
				continue
			}
			if matchEntry(entry, f) {
				matched = append(matched, entry)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return ReadResult{}, fmt.Errorf("read session: %w", readErr)
		}
	}
done:
	total := len(matched)
	if f.Order == OrderNewest {
		// File is in append order (oldest first); reverse for newest-first.
		for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
			matched[i], matched[j] = matched[j], matched[i]
		}
	}
	// OrderOldest: already in append order, no reordering needed.

	page := max(f.Page, 1)
	if f.Limit > 0 {
		start := (page - 1) * f.Limit
		if start >= len(matched) {
			matched = nil
		} else {
			end := min(start+f.Limit, len(matched))
			matched = matched[start:end]
		}
	}

	return ReadResult{Entries: matched, TotalMatched: total}, nil
}

// readInfo reads the meta header for one session and derives its liveness.
func (s *jsonlStore) readInfo(sessionID string) (SessionInfo, error) {
	path := s.pathFor(sessionID)
	f, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer func() { _ = f.Close() }()

	meta, err := readMeta(f)
	if err != nil {
		return SessionInfo{}, err
	}

	info := SessionInfo{Meta: meta, IsActive: pidAlive(meta.CapturerPID)}
	if !info.IsActive {
		st, err := f.Stat()
		if err == nil {
			t := st.ModTime().UTC()
			info.ApproxEndedAt = &t
		}
	}
	return info, nil
}

// pidAlive reports whether sending signal 0 to pid succeeds. EPERM means the
// process exists but we lack permission to signal it — still alive for our
// purposes. ESRCH means it's gone.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

func decodeEntry(line []byte) (logentry.LogEntry, bool) {
	var e logentry.LogEntry
	if err := sonic.Unmarshal(line, &e); err != nil {
		return logentry.LogEntry{}, false
	}
	return e, true
}

// preOutcome is the result of a cheap pre-filter pass over a JSONL line. It
// lets the read loop skip malformed lines, reject filter misses without
// paying full-decode cost, and stop scanning entirely when a monotonic upper
// bound is exceeded.
type preOutcome int

const (
	preAccept preOutcome = iota // line passes cheap checks; do full decode
	preReject                   // line fails a cheap check; skip
	preStop                     // upper bound exceeded; stop scanning
)

// preFilter runs cheap field extractions (level, timestamp, line) via gjson
// and applies the corresponding filters before the caller does the expensive
// sonic.Unmarshal. Returns ok=false if the line isn't valid JSON.
//
// query / regex filters can't be applied here because they need the parsed
// Message field — those still run after full decode in matchEntry.
func preFilter(line []byte, f Filter) (preOutcome, bool) {
	// Validate cheaply by checking the level field first — every valid entry
	// has one. If gjson can't even find that, the line is malformed.
	parsed := gjson.ParseBytes(line)
	if !parsed.IsObject() {
		return preReject, false
	}

	// Line filter (cheapest — no parsing of strings).
	if f.StartLine != nil || f.EndLine != nil {
		ln := parsed.Get("line").Int()
		if f.EndLine != nil && ln > *f.EndLine {
			return preStop, true
		}
		if f.StartLine != nil && ln < *f.StartLine {
			return preReject, true
		}
	}

	// Level filter (string compare on a small enum).
	if f.Level != nil {
		lvl := parsed.Get("level").Str
		if lvl != *f.Level {
			return preReject, true
		}
	}

	// Time filter — only parse the timestamp if a time bound is set.
	if f.StartTime != nil || f.EndTime != nil {
		tsStr := parsed.Get("timestamp").Str
		if tsStr == "" {
			return preReject, true
		}
		ts, err := time.Parse(time.RFC3339Nano, tsStr)
		if err != nil {
			return preReject, true
		}
		// Don't preStop on EndTime: stdout and stderr drain concurrently, so
		// timestamps inside the file aren't strictly monotonic in append order.
		// Stopping early here would drop later in-window entries.
		if f.EndTime != nil && ts.After(*f.EndTime) {
			return preReject, true
		}
		if f.StartTime != nil && ts.Before(*f.StartTime) {
			return preReject, true
		}
	}

	return preAccept, true
}

func matchEntry(e logentry.LogEntry, f Filter) bool {
	if f.Level != nil && *f.Level != "" && *f.Level != "all" && e.Level != *f.Level {
		return false
	}
	if f.StartTime != nil && e.Timestamp.Before(*f.StartTime) {
		return false
	}
	if f.EndTime != nil && e.Timestamp.After(*f.EndTime) {
		return false
	}
	if f.StartLine != nil && e.Line < *f.StartLine {
		return false
	}
	if f.EndLine != nil && e.Line > *f.EndLine {
		return false
	}
	if f.Query != nil && !f.Query.MatchString(e.Message) {
		return false
	}
	return true
}

// --- jsonlSession ---

func (s *jsonlSession) Append(e logentry.LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("append on closed session")
	}
	e.Line = s.nextLine
	b, err := sonic.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}
	b = append(b, '\n')
	if _, err := s.file.Write(b); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}
	s.nextLine++
	return nil
}

func (s *jsonlSession) Path() string      { return s.meta.FilePath }
func (s *jsonlSession) SessionID() string { return s.meta.SessionID }

func (s *jsonlSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	closeErr := s.file.Close()
	var cleanupErr error
	if s.meta.Ephemeral {
		if err := os.Remove(s.meta.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = fmt.Errorf("ephemeral cleanup: %w", err)
		}
	}
	return errors.Join(closeErr, cleanupErr)
}

// compile-time interface assertions
var (
	_ Store   = (*jsonlStore)(nil)
	_ Session = (*jsonlSession)(nil)
)
