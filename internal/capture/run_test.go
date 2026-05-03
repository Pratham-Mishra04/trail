package capture

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

// fakeStore is a minimal in-memory Store for run_test.go. Only Create is
// implemented — Run never calls the read/list methods.
type fakeStore struct {
	mu       sync.Mutex
	sessions []*runFakeSession
	createFn func(logentry.MetaHeader) (store.Session, error) // optional override
}

func (s *fakeStore) Create(meta logentry.MetaHeader) (store.Session, error) {
	if s.createFn != nil {
		return s.createFn(meta)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := &runFakeSession{meta: meta}
	s.sessions = append(s.sessions, sess)
	return sess, nil
}

func (s *fakeStore) List(context.Context) ([]store.SessionInfo, error) { return nil, nil }
func (s *fakeStore) Get(context.Context, string) (store.SessionInfo, error) {
	return store.SessionInfo{}, nil
}
func (s *fakeStore) Delete(context.Context, string) error   { return nil }
func (s *fakeStore) DeleteAll(context.Context) (int, error) { return 0, nil }
func (s *fakeStore) Read(context.Context, string, store.Filter) (store.ReadResult, error) {
	return store.ReadResult{}, nil
}

func (s *fakeStore) only() *runFakeSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) != 1 {
		return nil
	}
	return s.sessions[0]
}

type runFakeSession struct {
	mu      sync.Mutex
	meta    logentry.MetaHeader
	entries []logentry.LogEntry
}

func (s *runFakeSession) Append(e logentry.LogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Line = int64(len(s.entries) + 1)
	s.entries = append(s.entries, e)
	return nil
}
func (s *runFakeSession) Path() string      { return "/fake/" + s.meta.SessionID }
func (s *runFakeSession) SessionID() string { return s.meta.SessionID }
func (s *runFakeSession) Close() error      { return nil }
func (s *runFakeSession) snapshot() []logentry.LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]logentry.LogEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("trail targets macOS+Linux only")
	}
}

func TestRun_EchoStdout(t *testing.T) {
	skipIfWindows(t)
	st := &fakeStore{}
	code, err := Run(context.Background(), RunOptions{
		Store: st,
		Args:  []string{"/bin/echo", "hello world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	sess := st.only()
	if sess == nil {
		t.Fatal("expected exactly one session")
	}
	entries := sess.snapshot()
	if len(entries) != 1 || entries[0].Raw != "hello world" || entries[0].Stream != logentry.StreamStdout {
		t.Errorf("entries = %+v", entries)
	}
}

func TestRun_BothStreamsAttributed(t *testing.T) {
	skipIfWindows(t)
	st := &fakeStore{}
	_, err := Run(context.Background(), RunOptions{
		Store: st,
		Args:  []string{"/bin/sh", "-c", "echo to-stdout; echo to-stderr >&2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := st.only().snapshot()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}

	var stdoutEntry, stderrEntry *logentry.LogEntry
	for i, e := range entries {
		switch e.Stream {
		case logentry.StreamStdout:
			stdoutEntry = &entries[i]
		case logentry.StreamStderr:
			stderrEntry = &entries[i]
		}
	}
	if stdoutEntry == nil || stdoutEntry.Raw != "to-stdout" {
		t.Errorf("stdout entry = %+v", stdoutEntry)
	}
	if stderrEntry == nil || stderrEntry.Raw != "to-stderr" {
		t.Errorf("stderr entry = %+v", stderrEntry)
	}
}

func TestRun_PropagatesExitCode(t *testing.T) {
	skipIfWindows(t)
	st := &fakeStore{}
	code, err := Run(context.Background(), RunOptions{
		Store: st,
		Args:  []string{"/bin/sh", "-c", "exit 7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

func TestRun_PassthroughEchoesToTerminal(t *testing.T) {
	skipIfWindows(t)
	st := &fakeStore{}
	var stdoutBuf, stderrBuf bytes.Buffer
	_, err := Run(context.Background(), RunOptions{
		Store:       st,
		Args:        []string{"/bin/sh", "-c", "echo out; echo err >&2"},
		Passthrough: true,
		Stdout:      &stdoutBuf,
		Stderr:      &stderrBuf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stdoutBuf.String() != "out\n" {
		t.Errorf("stdout passthrough = %q", stdoutBuf.String())
	}
	if stderrBuf.String() != "err\n" {
		t.Errorf("stderr passthrough = %q", stderrBuf.String())
	}
}

func TestRun_NonExistentBinary(t *testing.T) {
	skipIfWindows(t)
	st := &fakeStore{}
	_, err := Run(context.Background(), RunOptions{
		Store: st,
		Args:  []string{"/definitely/not/a/real/binary/anywhere"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(st.sessions) != 0 {
		t.Errorf("session created despite Start failure")
	}
}

func TestRun_PopulatesMeta(t *testing.T) {
	skipIfWindows(t)
	st := &fakeStore{}
	_, err := Run(context.Background(), RunOptions{
		Store:        st,
		Args:         []string{"/bin/echo", "x"},
		Name:         "explicit-name",
		TrailVersion: "0.1.0-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := st.only()
	if sess.meta.Name != "explicit-name" {
		t.Errorf("Name = %q", sess.meta.Name)
	}
	if sess.meta.Source != logentry.SourceRun {
		t.Errorf("Source = %q", sess.meta.Source)
	}
	if sess.meta.CapturedPID == 0 {
		t.Error("CapturedPID not set")
	}
	if sess.meta.Command != "/bin/echo x" {
		t.Errorf("Command = %q", sess.meta.Command)
	}
	if sess.meta.Trail != "0.1.0-test" {
		t.Errorf("Trail = %q", sess.meta.Trail)
	}
}

func TestRun_AutoNameFromArgs(t *testing.T) {
	skipIfWindows(t)
	st := &fakeStore{}
	_, err := Run(context.Background(), RunOptions{
		Store: st,
		Args:  []string{"/bin/echo", "hi", "there"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if name := st.only().meta.Name; name != "echo hi there" {
		t.Errorf("auto Name = %q, want %q", name, "echo hi there")
	}
}

func TestAutoName_TrimsTo80Chars(t *testing.T) {
	args := []string{"/bin/cmd"}
	for range 20 {
		args = append(args, "verylongargument")
	}
	got := autoName(args)
	if len(got) != 80 {
		t.Errorf("len = %d, want 80", len(got))
	}
	if !strings.HasPrefix(got, "cmd ") {
		t.Errorf("prefix = %q, want 'cmd '", got[:8])
	}
}

func TestRun_ContextCancellationKillsChild(t *testing.T) {
	skipIfWindows(t)
	st := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())

	resultCh := make(chan int, 1)
	go func() {
		code, _ := Run(ctx, RunOptions{
			Store: st,
			Args:  []string{"/bin/sh", "-c", "sleep 60"},
		})
		resultCh <- code
	}()

	// Give the child time to start.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case code := <-resultCh:
		// Killed by signal → 128 + signal_number, never 0.
		if code == 0 {
			t.Errorf("expected non-zero exit code on cancellation, got 0")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}

func TestRun_NoCommand(t *testing.T) {
	st := &fakeStore{}
	_, err := Run(context.Background(), RunOptions{Store: st, Args: []string{}})
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}

func TestRun_NoStore(t *testing.T) {
	_, err := Run(context.Background(), RunOptions{Args: []string{"/bin/echo", "hi"}})
	if err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestRun_StoreCreateFailure_KillsChild(t *testing.T) {
	skipIfWindows(t)
	wantErr := errors.New("store unavailable")
	st := &fakeStore{
		createFn: func(logentry.MetaHeader) (store.Session, error) { return nil, wantErr },
	}
	_, err := Run(context.Background(), RunOptions{
		Store: st,
		Args:  []string{"/bin/sh", "-c", "sleep 30"},
	})
	if err == nil || !strings.Contains(err.Error(), "store unavailable") {
		t.Fatalf("err = %v, want wrapped 'store unavailable'", err)
	}
	// We can't easily assert the child was reaped, but if it weren't,
	// the test process would leak it. A subsequent ps would catch a leak.
}

// Sanity check: the real exec.ExitError shape exits the way exitCodeFrom
// expects on this platform.
func TestExitCodeFrom_RealProcess(t *testing.T) {
	skipIfWindows(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "exit 42")
	err := cmd.Run()
	if got := exitCodeFrom(err); got != 42 {
		t.Errorf("exitCodeFrom = %d, want 42", got)
	}
	if got := exitCodeFrom(nil); got != 0 {
		t.Errorf("exitCodeFrom(nil) = %d, want 0", got)
	}
}
