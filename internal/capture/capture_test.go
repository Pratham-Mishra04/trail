package capture

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

// fakeSession implements store.Session in memory. Append assigns Line just
// like jsonlSession does, so Pipeline tests don't need to coordinate with the
// real filesystem-backed store.
type fakeSession struct {
	mu       sync.Mutex
	entries  []logentry.LogEntry
	closed   bool
	appendFn func(logentry.LogEntry) error // optional override for fault-injection
}

func (s *fakeSession) Append(e logentry.LogEntry) error {
	if s.appendFn != nil {
		return s.appendFn(e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Line = int64(len(s.entries) + 1)
	s.entries = append(s.entries, e)
	return nil
}
func (s *fakeSession) Path() string      { return "/fake" }
func (s *fakeSession) SessionID() string { return "fake" }
func (s *fakeSession) Close() error      { s.closed = true; return nil }

func (s *fakeSession) snapshot() []logentry.LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]logentry.LogEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

var _ store.Session = (*fakeSession)(nil)

func TestProcess_BasicLines(t *testing.T) {
	s := &fakeSession{}
	p := NewPipeline(s, PipelineOptions{})

	in := strings.NewReader("hello\nworld\n")
	if err := p.Process(context.Background(), logentry.StreamStdout, in); err != nil {
		t.Fatal(err)
	}
	got := s.snapshot()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Raw != "hello" || got[0].Line != 1 {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Raw != "world" || got[1].Line != 2 {
		t.Errorf("entry 1 = %+v", got[1])
	}
	for _, e := range got {
		if e.Stream != logentry.StreamStdout {
			t.Errorf("entry stream = %q, want stdout", e.Stream)
		}
		if e.Timestamp.IsZero() {
			t.Errorf("entry timestamp is zero")
		}
	}
}

func TestProcess_StreamAttribution(t *testing.T) {
	s := &fakeSession{}
	p := NewPipeline(s, PipelineOptions{})
	ctx := context.Background()

	if err := p.Process(ctx, logentry.StreamStderr, strings.NewReader("err1\n")); err != nil {
		t.Fatal(err)
	}
	if err := p.Process(ctx, logentry.StreamStdout, strings.NewReader("out1\n")); err != nil {
		t.Fatal(err)
	}
	got := s.snapshot()
	if len(got) != 2 {
		t.Fatalf("got %d entries", len(got))
	}
	if got[0].Stream != logentry.StreamStderr || got[1].Stream != logentry.StreamStdout {
		t.Errorf("stream attribution wrong: %+v", got)
	}
}

func TestProcess_LevelDetectionWired(t *testing.T) {
	s := &fakeSession{}
	p := NewPipeline(s, PipelineOptions{})

	in := strings.NewReader("ERROR: boom\nplain text\n")
	if err := p.Process(context.Background(), logentry.StreamStdout, in); err != nil {
		t.Fatal(err)
	}
	got := s.snapshot()
	if got[0].Level != logentry.LevelError || got[0].Message != "boom" {
		t.Errorf("entry 0: level=%q message=%q", got[0].Level, got[0].Message)
	}
	if got[1].Level != logentry.LevelUnknown || got[1].Message != "plain text" {
		t.Errorf("entry 1: level=%q message=%q", got[1].Level, got[1].Message)
	}
}

func TestProcess_PartialLastLineCaptured(t *testing.T) {
	s := &fakeSession{}
	p := NewPipeline(s, PipelineOptions{})

	in := strings.NewReader("first\nno-newline-final")
	if err := p.Process(context.Background(), logentry.StreamStdout, in); err != nil {
		t.Fatal(err)
	}
	got := s.snapshot()
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[1].Raw != "no-newline-final" {
		t.Errorf("trailing partial line not captured: %q", got[1].Raw)
	}
}

func TestProcess_LevelDetectionUsesUntruncatedLine(t *testing.T) {
	// Build a line that's long enough to trigger truncation but starts with
	// "ERROR:" so the line-prefix detector picks up the level.
	long := "ERROR: " + strings.Repeat("x", 5000)
	s := &fakeSession{}
	p := NewPipeline(s, PipelineOptions{})

	if err := p.Process(context.Background(), logentry.StreamStdout, strings.NewReader(long+"\n")); err != nil {
		t.Fatal(err)
	}
	got := s.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].Level != logentry.LevelError {
		t.Errorf("level = %q, want %q (parser must run on original, not truncated, line)", got[0].Level, logentry.LevelError)
	}
	// Raw should still be truncated.
	if len(got[0].Raw) > maxRawLen+50 {
		t.Errorf("Raw not truncated: %d bytes", len(got[0].Raw))
	}
}

func TestProcess_TruncatesLongLines(t *testing.T) {
	s := &fakeSession{}
	p := NewPipeline(s, PipelineOptions{})

	long := strings.Repeat("x", 5000) + "\n"
	if err := p.Process(context.Background(), logentry.StreamStdout, strings.NewReader(long)); err != nil {
		t.Fatal(err)
	}
	got := s.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d entries", len(got))
	}
	raw := got[0].Raw
	if !strings.HasPrefix(raw, strings.Repeat("x", maxRawLen)) {
		t.Errorf("truncation prefix wrong (first 20 = %q)", raw[:20])
	}
	if !strings.Contains(raw, "[truncated 1000 bytes]") {
		t.Errorf("missing truncation suffix: %q", raw[len(raw)-40:])
	}
}

func TestProcess_Passthrough(t *testing.T) {
	s := &fakeSession{}
	var stdoutBuf, stderrBuf bytes.Buffer
	p := NewPipeline(s, PipelineOptions{
		Passthrough: true,
		Stdout:      &stdoutBuf,
		Stderr:      &stderrBuf,
	})
	ctx := context.Background()

	if err := p.Process(ctx, logentry.StreamStdout, strings.NewReader("out\n")); err != nil {
		t.Fatal(err)
	}
	if err := p.Process(ctx, logentry.StreamStderr, strings.NewReader("err\n")); err != nil {
		t.Fatal(err)
	}

	if stdoutBuf.String() != "out\n" {
		t.Errorf("stdout passthrough = %q", stdoutBuf.String())
	}
	if stderrBuf.String() != "err\n" {
		t.Errorf("stderr passthrough = %q", stderrBuf.String())
	}
}

func TestProcess_PassthroughOff(t *testing.T) {
	s := &fakeSession{}
	var buf bytes.Buffer
	p := NewPipeline(s, PipelineOptions{Passthrough: false, Stdout: &buf, Stderr: &buf})

	if err := p.Process(context.Background(), logentry.StreamStdout, strings.NewReader("out\n")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("passthrough leaked %d bytes", buf.Len())
	}
}

func TestProcess_PassthroughEchoesUntruncatedLine(t *testing.T) {
	s := &fakeSession{}
	var stdoutBuf bytes.Buffer
	p := NewPipeline(s, PipelineOptions{Passthrough: true, Stdout: &stdoutBuf})

	long := strings.Repeat("x", 5000)
	if err := p.Process(context.Background(), logentry.StreamStdout, strings.NewReader(long+"\n")); err != nil {
		t.Fatal(err)
	}

	// Pass-through should see the *full* original line, not the truncated one.
	if stdoutBuf.String() != long+"\n" {
		t.Errorf("passthrough emitted %d bytes, want %d", stdoutBuf.Len(), len(long)+1)
	}
	// Storage should still be truncated.
	got := s.snapshot()
	if len(got[0].Raw) > maxRawLen+50 {
		t.Errorf("stored Raw not truncated: %d bytes", len(got[0].Raw))
	}
}

func TestProcess_ConcurrentStreams(t *testing.T) {
	s := &fakeSession{}
	p := NewPipeline(s, PipelineOptions{})
	ctx := context.Background()

	stdoutLines := make([]string, 0, 200)
	stderrLines := make([]string, 0, 200)
	for i := range 200 {
		stdoutLines = append(stdoutLines, fmt.Sprintf("o%d", i))
		stderrLines = append(stderrLines, fmt.Sprintf("e%d", i))
	}

	var wg sync.WaitGroup
	wg.Add(2)
	errCh := make(chan error, 2)
	go func() {
		defer wg.Done()
		errCh <- p.Process(ctx, logentry.StreamStdout, strings.NewReader(strings.Join(stdoutLines, "\n")+"\n"))
	}()
	go func() {
		defer wg.Done()
		errCh <- p.Process(ctx, logentry.StreamStderr, strings.NewReader(strings.Join(stderrLines, "\n")+"\n"))
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
	}

	got := s.snapshot()
	if len(got) != 400 {
		t.Fatalf("got %d entries, want 400", len(got))
	}

	// Line numbers must be 1..400 with no duplicates.
	seen := make(map[int64]bool, 400)
	for _, e := range got {
		if seen[e.Line] {
			t.Fatalf("duplicate line %d", e.Line)
		}
		seen[e.Line] = true
	}

	// Each stream's content should be present in original order, even if
	// interleaved with the other stream.
	var gotStdout, gotStderr []string
	for _, e := range got {
		if e.Stream == logentry.StreamStdout {
			gotStdout = append(gotStdout, e.Raw)
		} else {
			gotStderr = append(gotStderr, e.Raw)
		}
	}
	if !equalSlice(gotStdout, stdoutLines) {
		t.Errorf("stdout content reorder: got first 3 = %v, want %v", gotStdout[:3], stdoutLines[:3])
	}
	if !equalSlice(gotStderr, stderrLines) {
		t.Errorf("stderr content reorder: got first 3 = %v, want %v", gotStderr[:3], stderrLines[:3])
	}
}

func TestProcess_ContextCancellation(t *testing.T) {
	s := &fakeSession{}
	p := NewPipeline(s, PipelineOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Process even starts

	in := strings.NewReader("a\nb\nc\n")
	err := p.Process(ctx, logentry.StreamStdout, in)
	if err == nil {
		t.Fatal("expected ctx cancellation error")
	}
}

func TestProcess_PropagatesAppendError(t *testing.T) {
	want := io.ErrUnexpectedEOF
	s := &fakeSession{appendFn: func(logentry.LogEntry) error { return want }}
	p := NewPipeline(s, PipelineOptions{})

	err := p.Process(context.Background(), logentry.StreamStdout, strings.NewReader("oops\n"))
	if err != want {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
