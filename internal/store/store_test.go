package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
)

func newTestStore(t *testing.T) *jsonlStore {
	t.Helper()
	return &jsonlStore{dir: t.TempDir()}
}

func newMeta(name string, pid int) logentry.MetaHeader {
	return logentry.MetaHeader{
		Name:        name,
		Source:      logentry.SourceRun,
		CapturerPID: pid,
		StartedAt:   time.Now().UTC(),
		Trail:       "test",
	}
}

func newEntry(level, msg string) logentry.LogEntry {
	return logentry.LogEntry{
		Stream:    logentry.StreamStdout,
		Level:     level,
		Raw:       msg,
		Message:   msg,
		Timestamp: time.Now().UTC(),
	}
}

func TestCreate_AssignsIDAndWritesMeta(t *testing.T) {
	s := newTestStore(t)
	sess, err := s.Create(newMeta("api", os.Getpid()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if sess.SessionID() == "" {
		t.Fatal("SessionID empty")
	}
	if sess.Path() == "" || filepath.Dir(sess.Path()) != s.dir {
		t.Fatalf("Path() = %q, expected to live under %q", sess.Path(), s.dir)
	}

	// File exists, has a parseable meta line.
	f, err := os.Open(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	meta, err := readMeta(f)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if meta.SessionID != sess.SessionID() {
		t.Fatalf("meta.SessionID = %q, want %q", meta.SessionID, sess.SessionID())
	}
	if meta.Name != "api" {
		t.Fatalf("meta.Name = %q", meta.Name)
	}
	if meta.FilePath != sess.Path() {
		t.Fatalf("meta.FilePath = %q, want %q", meta.FilePath, sess.Path())
	}
}

func TestAppendAndRead_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sess, err := s.Create(newMeta("api", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := sess.Append(newEntry(logentry.LevelInfo, fmt.Sprintf("line %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	res, err := s.Read(ctx, sess.SessionID(), Filter{Order: OrderOldest})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 || res.TotalMatched != 3 {
		t.Fatalf("got %d entries (total %d), want 3", len(res.Entries), res.TotalMatched)
	}
	for i, e := range res.Entries {
		if e.Line != int64(i+1) {
			t.Errorf("entry %d: Line = %d, want %d", i, e.Line, i+1)
		}
		if e.Message != fmt.Sprintf("line %d", i+1) {
			t.Errorf("entry %d: Message = %q", i, e.Message)
		}
	}
}

func TestList_SortsActiveFirstThenByStartedAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Two active (current pid), one inactive (impossibly-high pid).
	older := newMeta("older", os.Getpid())
	older.StartedAt = time.Now().Add(-1 * time.Hour).UTC()
	newer := newMeta("newer", os.Getpid())
	newer.StartedAt = time.Now().UTC()
	dead := newMeta("dead", 999_999_999) // unlikely to be a live pid
	dead.StartedAt = time.Now().Add(-30 * time.Minute).UTC()

	for _, m := range []logentry.MetaHeader{older, newer, dead} {
		sess, err := s.Create(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := sess.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3", len(got))
	}

	// First two should be active, ordered newer→older.
	if !got[0].IsActive || got[0].Meta.Name != "newer" {
		t.Errorf("got[0] = %+v, want active 'newer'", got[0].Meta)
	}
	if !got[1].IsActive || got[1].Meta.Name != "older" {
		t.Errorf("got[1] = %+v, want active 'older'", got[1].Meta)
	}
	if got[2].IsActive || got[2].Meta.Name != "dead" {
		t.Errorf("got[2] = %+v, want inactive 'dead'", got[2].Meta)
	}
	if got[2].ApproxEndedAt == nil {
		t.Error("inactive session missing ApproxEndedAt")
	}
}

func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	// Valid UUID format but no file backing it.
	_, err := s.Get(context.Background(), "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestGet_InvalidSessionID(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"", "not-a-uuid", "../etc/passwd", "9c5e1b7e"} {
		_, err := s.Get(context.Background(), id)
		if err == nil {
			t.Errorf("Get(%q) returned no error", id)
		}
	}
}

func TestDelete_InvalidSessionID(t *testing.T) {
	s := newTestStore(t)
	if err := s.Delete(context.Background(), "../etc/passwd"); err == nil {
		t.Error("Delete on path-traversal id returned no error")
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess, err := s.Create(newMeta("x", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	id := sess.SessionID()
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("after delete, Get err = %v, want os.ErrNotExist", err)
	}
	if err := s.Delete(ctx, id); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete missing: err = %v, want os.ErrNotExist", err)
	}
}

func TestDeleteAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for range 3 {
		sess, err := s.Create(newMeta("s", os.Getpid()))
		if err != nil {
			t.Fatal(err)
		}
		if err := sess.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	n, err := s.DeleteAll(ctx)
	if err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if n != 3 {
		t.Errorf("deleted %d, want 3", n)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("after DeleteAll, list has %d entries", len(got))
	}
}

func TestEphemeral_DeletedOnClose(t *testing.T) {
	s := newTestStore(t)
	meta := newMeta("ephemeral", os.Getpid())
	meta.Ephemeral = true

	sess, err := s.Create(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(newEntry(logentry.LevelInfo, "hi")); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file missing before close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral file not deleted: stat err = %v", err)
	}
}

func TestRead_TolerantOfPartialLastLine(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess, err := s.Create(newMeta("partial", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(newEntry(logentry.LevelInfo, "first")); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(newEntry(logentry.LevelInfo, "second")); err != nil {
		t.Fatal(err)
	}
	id := sess.SessionID()
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Append a partial (no trailing newline) fragment to simulate a writer
	// crashing mid-append.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"line":99,"stream":"stdout","level":"info","raw":"truncated`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	res, err := s.Read(ctx, id, Filter{Order: OrderOldest})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("got %d entries, want 2 (partial last line should be ignored)", len(res.Entries))
	}
}

func TestRead_Filters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess, err := s.Create(newMeta("filt", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []logentry.LogEntry{
		{Stream: "stdout", Level: "info", Raw: "starting", Message: "starting", Timestamp: base},
		{Stream: "stderr", Level: "error", Raw: "ECONNREFUSED", Message: "ECONNREFUSED", Timestamp: base.Add(1 * time.Minute)},
		{Stream: "stdout", Level: "warn", Raw: "deprecated", Message: "deprecated", Timestamp: base.Add(2 * time.Minute)},
		{Stream: "stderr", Level: "error", Raw: "panic: foo", Message: "panic: foo", Timestamp: base.Add(3 * time.Minute)},
	}
	for _, e := range entries {
		if err := sess.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	id := sess.SessionID()
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	errStr := logentry.LevelError
	startLine := int64(2)
	endLine := int64(3)
	tStart := base.Add(30 * time.Second)
	tEnd := base.Add(2*time.Minute + 30*time.Second)

	tests := []struct {
		name        string
		filter      Filter
		wantTotal   int
		wantContent []string // expected Raw fields, in oldest order
	}{
		{
			name:        "level=error",
			filter:      Filter{Level: &errStr, Order: OrderOldest},
			wantTotal:   2,
			wantContent: []string{"ECONNREFUSED", "panic: foo"},
		},
		{
			name:        "regex query",
			filter:      Filter{Query: regexp.MustCompile(`(?i)econn`), Order: OrderOldest},
			wantTotal:   1,
			wantContent: []string{"ECONNREFUSED"},
		},
		{
			name:        "time window",
			filter:      Filter{StartTime: &tStart, EndTime: &tEnd, Order: OrderOldest},
			wantTotal:   2,
			wantContent: []string{"ECONNREFUSED", "deprecated"},
		},
		{
			name:        "line range",
			filter:      Filter{StartLine: &startLine, EndLine: &endLine, Order: OrderOldest},
			wantTotal:   2,
			wantContent: []string{"ECONNREFUSED", "deprecated"},
		},
		{
			name:        "newest order",
			filter:      Filter{Order: OrderNewest},
			wantTotal:   4,
			wantContent: []string{"panic: foo", "deprecated", "ECONNREFUSED", "starting"},
		},
		{
			name:        "limit + page",
			filter:      Filter{Limit: 2, Page: 2, Order: OrderOldest},
			wantTotal:   4,
			wantContent: []string{"deprecated", "panic: foo"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := s.Read(ctx, id, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if res.TotalMatched != tc.wantTotal {
				t.Errorf("TotalMatched = %d, want %d", res.TotalMatched, tc.wantTotal)
			}
			if len(res.Entries) != len(tc.wantContent) {
				t.Fatalf("got %d entries, want %d", len(res.Entries), len(tc.wantContent))
			}
			for i, want := range tc.wantContent {
				if res.Entries[i].Raw != want {
					t.Errorf("entry %d: Raw = %q, want %q", i, res.Entries[i].Raw, want)
				}
			}
		})
	}
}

func TestConcurrentAppend_NoCorruption(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess, err := s.Create(newMeta("conc", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 8
	const perGoroutine = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(g int) {
			defer wg.Done()
			for i := range perGoroutine {
				if err := sess.Append(newEntry(logentry.LevelInfo, fmt.Sprintf("g%d-i%d", g, i))); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	res, err := s.Read(ctx, sess.SessionID(), Filter{Order: OrderOldest})
	if err != nil {
		t.Fatal(err)
	}
	want := goroutines * perGoroutine
	if res.TotalMatched != want {
		t.Fatalf("got %d entries, want %d", res.TotalMatched, want)
	}

	// Line numbers must be 1..want with no duplicates and no gaps.
	lines := make([]int64, len(res.Entries))
	for i, e := range res.Entries {
		lines[i] = e.Line
	}
	slices.Sort(lines)
	for i, l := range lines {
		if l != int64(i+1) {
			t.Fatalf("line %d: got %d, want %d (gap or duplicate)", i, l, i+1)
		}
	}
}

func TestRead_NormalizesEmptyOrderToNewest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess, _ := s.Create(newMeta("ord", os.Getpid()))
	for i := 1; i <= 3; i++ {
		sess.Append(newEntry(logentry.LevelInfo, fmt.Sprintf("line %d", i)))
	}
	id := sess.SessionID()
	sess.Close()

	// Filter{} with no Order should normalize to newest-first.
	res, err := s.Read(ctx, id, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 {
		t.Fatalf("got %d entries", len(res.Entries))
	}
	if res.Entries[0].Line != 3 {
		t.Errorf("first entry line = %d, want 3 (newest first)", res.Entries[0].Line)
	}
}

func TestRead_RejectsInvalidOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess, _ := s.Create(newMeta("ord", os.Getpid()))
	id := sess.SessionID()
	sess.Close()

	_, err := s.Read(ctx, id, Filter{Order: "random"})
	if err == nil {
		t.Fatal("expected error for invalid Order")
	}
}

func TestList_SkipsCorruptFiles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// One valid session.
	sess, _ := s.Create(newMeta("good", os.Getpid()))
	sess.Close()

	// One corrupt file in the same dir.
	corrupt := filepath.Join(s.dir, "corrupt.jsonl")
	if err := os.WriteFile(corrupt, []byte("not json at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Meta.Name != "good" {
		t.Fatalf("got %+v, want only the 'good' session", got)
	}
}
