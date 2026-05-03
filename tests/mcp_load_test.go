// Package tests holds end-to-end tests that exercise the full trail stack
// from the outside: build the real binary, spawn `trail mcp` as a real
// subprocess, talk to it over real stdio with real JSON-RPC framing.
//
// Internal-package tests (under internal/.../*_test.go) cover unit behavior.
// These tests cover what the editor actually does: spawn the binary, send
// JSON-RPC requests, read responses. If something breaks at the framing or
// transport layer, only these tests catch it.
package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

// TestE2E_MCPUnderConcurrentWriteLoad spins up the real MCP server as a
// subprocess and pounds it with `get_logs` queries while a writer is still
// appending to the same session — the actual debug-session pattern. Phase 1
// queries run while writes are in flight; phase 2 runs after the file is
// fully populated, so we can compare latency under load vs at rest.
func TestE2E_MCPUnderConcurrentWriteLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (slow: builds the binary, spawns subprocess)")
	}

	const (
		totalEntries  = 100_000
		triggerAt     = 50_000                // start querying after this many writes
		writeBatch    = 100                   // entries per batch
		batchInterval = 10 * time.Millisecond // → ~10K entries/sec
		queryBudget   = 1500 * time.Millisecond
	)

	// 1. Build the trail binary into a temp dir. We use the real binary
	// rather than `go run` so we measure cold start + protocol framing as
	// the editor would experience it.
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "trail")
	buildOut, err := exec.Command("go", "build", "-o", bin, "..").CombinedOutput()
	if err != nil {
		t.Fatalf("build trail: %v\n%s", err, buildOut)
	}

	// 2. Isolate $HOME so the test doesn't touch the user's real
	// ~/.config/trail/sessions/ directory.
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// 3. Open a session via the same store the binary will read from. Both
	// processes resolve their data dir from $HOME, so they see the same file.
	s, err := store.NewJSONL()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create(logentry.MetaHeader{
		Name:        "e2e-load",
		Source:      logentry.SourceRun,
		CapturerPID: os.Getpid(),
		StartedAt:   time.Now().UTC(),
		Trail:       "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	sessionID := sess.SessionID()
	t.Logf("session: %s", sessionID)
	t.Logf("file:    %s", sess.Path())

	startTime := time.Now()

	// 4. Writer goroutine: 100K entries, ~10K/sec, signaling at 50K.
	var written int64
	writerErr := make(chan error, 1)
	halfwayDone := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		levels := []string{
			logentry.LevelInfo, logentry.LevelWarn, logentry.LevelError,
			logentry.LevelDebug, logentry.LevelUnknown,
		}
		keywords := []string{"db", "auth", "cache", "queue", "http"}
		i := 0
		for i < totalEntries {
			for j := 0; j < writeBatch && i < totalEntries; j++ {
				entry := logentry.LogEntry{
					Stream:    logentry.StreamStdout,
					Level:     levels[i%len(levels)],
					Raw:       fmt.Sprintf("entry %d %s op id=%d status=ok", i, keywords[i%len(keywords)], i),
					Message:   fmt.Sprintf("entry %d %s op id=%d status=ok", i, keywords[i%len(keywords)], i),
					Timestamp: time.Now().UTC(),
				}
				if err := sess.Append(entry); err != nil {
					writerErr <- err
					return
				}
				i++
				if i == triggerAt {
					close(halfwayDone)
				}
			}
			atomic.StoreInt64(&written, int64(i))
			time.Sleep(batchInterval)
		}
		atomic.StoreInt64(&written, int64(i))
	}()

	// 5. Wait until the writer has put down enough entries to be interesting.
	select {
	case <-halfwayDone:
	case e := <-writerErr:
		t.Fatalf("writer failed before reaching %d entries: %v", triggerAt, e)
	case <-time.After(30 * time.Second):
		t.Fatalf("writer didn't reach %d entries in 30s (got %d)", triggerAt, atomic.LoadInt64(&written))
	}
	t.Logf("[%v] %d writes in — spawning MCP subprocess", time.Since(startTime).Round(time.Millisecond), atomic.LoadInt64(&written))

	// 6. Spawn the real `trail mcp` subprocess with the isolated $HOME.
	mcp := exec.Command(bin, "mcp")
	mcp.Env = append(os.Environ(), "HOME="+homeDir)
	stdin, err := mcp.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := mcp.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	mcp.Stderr = io.Discard // suppress logger output during the test
	if err := mcp.Start(); err != nil {
		t.Fatalf("start mcp: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = mcp.Wait()
	})

	// 7. Initialize the JSON-RPC client.
	client := newMCPClient(stdin, stdout)
	if err := client.initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// 8. Phase 1: query under concurrent write pressure.
	t.Logf("[%v] phase 1: querying while writer continues", time.Since(startTime).Round(time.Millisecond))
	phase1 := runQueryBatch(t, client, sessionID, queryBudget)

	// 9. Wait for the writer to finish.
	select {
	case <-writerDone:
	case <-time.After(30 * time.Second):
		t.Fatalf("writer didn't finish in 30s (got %d / %d)", atomic.LoadInt64(&written), totalEntries)
	}
	select {
	case e := <-writerErr:
		t.Fatalf("writer failed: %v", e)
	default:
	}
	t.Logf("[%v] writer done (%d entries)", time.Since(startTime).Round(time.Millisecond), atomic.LoadInt64(&written))

	// 10. Phase 2: query the now-fully-populated session.
	t.Logf("[%v] phase 2: querying after writer finished", time.Since(startTime).Round(time.Millisecond))
	phase2 := runQueryBatch(t, client, sessionID, queryBudget)

	// 11. Report stats and assert.
	reportLatencies(t, "phase 1 (concurrent write)", phase1)
	reportLatencies(t, "phase 2 (writer done)     ", phase2)

	for _, q := range append(phase1, phase2...) {
		if q.err != nil {
			t.Errorf("query %q failed: %v", q.name, q.err)
		}
		if q.latency > queryBudget {
			t.Errorf("query %q exceeded budget: %v > %v", q.name, q.latency, queryBudget)
		}
	}
}

// --- query batch ---

type queryResult struct {
	name    string
	latency time.Duration
	err     error
}

func runQueryBatch(t *testing.T, client *mcpClient, sessionID string, _ time.Duration) []queryResult {
	t.Helper()
	queries := []struct {
		name string
		args map[string]any
	}{
		{
			"list_sessions",
			nil, // marker — handled below
		},
		{
			"get_logs level=error limit=100",
			map[string]any{
				"session_id": sessionID,
				"filters":    map[string]any{"level": "error", "limit": float64(100)},
			},
		},
		{
			"get_logs query=cache limit=100",
			map[string]any{
				"session_id": sessionID,
				"filters":    map[string]any{"query": "cache", "limit": float64(100)},
			},
		},
		{
			"get_logs line_range[40000:40100]",
			map[string]any{
				"session_id": sessionID,
				"filters": map[string]any{
					"start_line": float64(40000),
					"end_line":   float64(40100),
				},
			},
		},
		{
			"get_logs level=error+query=queue limit=50",
			map[string]any{
				"session_id": sessionID,
				"filters": map[string]any{
					"level": "error",
					"query": "queue",
					"limit": float64(50),
				},
			},
		},
		{
			"get_logs order=oldest limit=200",
			map[string]any{
				"session_id": sessionID,
				"filters":    map[string]any{"limit": float64(200), "order": "oldest"},
			},
		},
	}

	out := make([]queryResult, 0, len(queries))
	for _, q := range queries {
		start := time.Now()
		var err error
		if q.args == nil {
			_, err = client.call("tools/call", map[string]any{
				"name": "list_sessions", "arguments": map[string]any{},
			})
		} else {
			_, err = client.call("tools/call", map[string]any{
				"name": "get_logs", "arguments": q.args,
			})
		}
		out = append(out, queryResult{name: q.name, latency: time.Since(start), err: err})
	}
	return out
}

func reportLatencies(t *testing.T, label string, results []queryResult) {
	t.Helper()
	if len(results) == 0 {
		return
	}
	lats := make([]time.Duration, 0, len(results))
	for _, r := range results {
		lats = append(lats, r.latency)
	}
	slices.Sort(lats)
	var total time.Duration
	for _, d := range lats {
		total += d
	}
	median := lats[len(lats)/2]
	t.Logf("--- %s ---", label)
	for _, r := range results {
		mark := "✓"
		if r.err != nil {
			mark = "✗"
		}
		t.Logf("  %s  %-40s  %v", mark, r.name, r.latency)
	}
	t.Logf("  → min=%v  median=%v  max=%v  avg=%v",
		lats[0], median, lats[len(lats)-1], total/time.Duration(len(lats)))
}

// --- minimal JSON-RPC client over stdio ---

type mcpClient struct {
	in     io.WriteCloser
	out    *bufio.Reader
	nextID int
}

func newMCPClient(in io.WriteCloser, out io.Reader) *mcpClient {
	return &mcpClient{in: in, out: bufio.NewReader(out)}
}

func (c *mcpClient) initialize() error {
	if _, err := c.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "1"},
	}); err != nil {
		return err
	}
	// initialized is a notification — no id, no response.
	notif := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}` + "\n")
	_, err := c.in.Write(notif)
	return err
}

func (c *mcpClient) call(method string, params any) (json.RawMessage, error) {
	c.nextID++
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      c.nextID,
	}
	if params != nil {
		req["params"] = params
	}
	frame, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.in.Write(append(frame, '\n')); err != nil {
		return nil, fmt.Errorf("write frame: %w", err)
	}
	line, err := c.out.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w (raw: %s)", err, line)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}
