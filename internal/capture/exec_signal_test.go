package capture

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
)

// runForSignals is a small harness: launch a child via Run in a goroutine,
// wait for the session to exist (so we know cmd.Start succeeded), then send
// `sig` to the trail process (this test process). The captured stdout
// + entry timeline + exit code come back so individual tests can assert.
//
// Returns ok=false + a reason if trail didn't terminate within deadline,
// which is the canonical failure mode for "signal didn't propagate."
type signalRunResult struct {
	exitCode int
	err      error
	entries  []logentry.LogEntry
	timedOut bool
}

func runForSignals(t *testing.T, args []string, sig syscall.Signal, startupGrace, deadline time.Duration) signalRunResult {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("signal forwarding tests are POSIX-only")
	}

	st := &fakeStore{}
	resCh := make(chan signalRunResult, 1)

	go func() {
		code, err := Run(context.Background(), RunOptions{
			Store: st,
			Args:  args,
		})
		entries := []logentry.LogEntry{}
		if sess := st.only(); sess != nil {
			entries = sess.snapshot()
		}
		resCh <- signalRunResult{exitCode: code, err: err, entries: entries}
	}()

	// Wait for the child to actually start (Run prints session info via the
	// logger before draining; once the session exists in the fakeStore we're
	// safely past Start).
	deadlineStart := time.Now().Add(startupGrace)
	for st.only() == nil && time.Now().Before(deadlineStart) {
		time.Sleep(10 * time.Millisecond)
	}
	if st.only() == nil {
		t.Fatalf("child didn't start within %v", startupGrace)
	}

	// Send the signal to ourselves; trail's signal.Notify will catch it
	// just like it would from a terminal Ctrl+C.
	if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
		t.Fatalf("sending signal: %v", err)
	}

	select {
	case r := <-resCh:
		return r
	case <-time.After(deadline):
		return signalRunResult{timedOut: true}
	}
}

// --- 1. Naive case: simple direct child, no nesting ---

func TestRun_SIGINT_ToSimpleSleep(t *testing.T) {
	r := runForSignals(t,
		[]string{"/bin/sh", "-c", "echo started; sleep 30"},
		syscall.SIGINT,
		2*time.Second, 4*time.Second,
	)
	if r.timedOut {
		t.Fatal("trail did not exit within deadline — SIGINT didn't propagate to /bin/sh")
	}
	if r.exitCode == 0 {
		t.Errorf("exitCode = 0, want non-zero (signal-killed)")
	}
}

// --- 2. Nested case: child is a shell that backgrounds another process and
//        installs an explicit SIGINT trap so it actually responds (POSIX sh's
//        non-interactive `wait` ignores SIGINT by default — that's a sh
//        behavior, not a trail one).

func TestRun_SIGINT_ToNestedSubshellWithTrap(t *testing.T) {
	r := runForSignals(t,
		[]string{"/bin/sh", "-c", `
			sleep 30 & PID=$!
			trap "kill $PID; exit 130" INT
			echo started
			wait $PID
		`},
		syscall.SIGINT,
		2*time.Second, 4*time.Second,
	)
	if r.timedOut {
		t.Fatal("trail did not exit within deadline — nested subshell wasn't reached by SIGINT")
	}
}

// --- 3. The hard case: grandchild in a different process group (mimics what
//        air does when it spawns its built binary). Builds a tiny Go helper at
//        test-run time that sets Setpgid:true on its own child, so the
//        grandchild lives in a fresh pgid that escapes our pgid broadcast.

const detachingHelperSrc = `
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Spawn a child sleep in a NEW process group — exactly what air does
	// with the binary it manages.
	cmd := exec.Command("/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "spawn failed:", err)
		os.Exit(1)
	}
	fmt.Println("helper started, child pid:", cmd.Process.Pid)

	// Forward SIGINT/SIGTERM to our own child (this is what a well-behaved
	// supervisor does; we want to verify trail ALSO reaches the grandchild
	// directly, in case the supervisor doesn't or is slow).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		// Deliberately slow — pretend the supervisor is doing graceful work.
		time.Sleep(2 * time.Second)
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}()

	_ = cmd.Wait()
	fmt.Println("helper exiting")
}
`

func buildDetachingHelper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := writeFile(src, detachingHelperSrc); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "helper")
	out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Fatalf("build helper: %v\n%s", err, out)
	}
	return bin
}

func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o600)
}

func TestRun_SIGINT_ReachesGrandchildInDetachedPgroup(t *testing.T) {
	helper := buildDetachingHelper(t)

	r := runForSignals(t,
		[]string{helper},
		syscall.SIGINT,
		3*time.Second, 5*time.Second,
	)
	if r.timedOut {
		t.Fatal("trail did not exit within deadline — grandchild in detached pgroup escaped signal forwarding")
	}
}

// --- 4. SIGTERM should behave the same way ---

func TestRun_SIGTERM_PropagatesIdentically(t *testing.T) {
	r := runForSignals(t,
		[]string{"/bin/sh", "-c", "echo started; sleep 30"},
		syscall.SIGTERM,
		2*time.Second, 4*time.Second,
	)
	if r.timedOut {
		t.Fatal("trail did not exit on SIGTERM")
	}
}

// --- 5. Trap-handling child: assert the child's trap fires, proving the
//        signal arrived and was handled (not just that the process died).

func TestRun_SIGINT_FiresChildTrap(t *testing.T) {
	r := runForSignals(t,
		[]string{"/bin/sh", "-c", `
			trap "echo GOT_SIGINT; exit 0" INT
			echo started
			sleep 30
		`},
		syscall.SIGINT,
		2*time.Second, 4*time.Second,
	)
	if r.timedOut {
		t.Fatal("trap test timed out")
	}
	// Check the trap output landed in the captured stream.
	found := false
	for _, e := range r.entries {
		if strings.Contains(e.Raw, "GOT_SIGINT") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("trap message not captured. entries: %+v", r.entries)
	}
	if r.exitCode != 0 {
		t.Errorf("exitCode = %d, want 0 (trap exited cleanly)", r.exitCode)
	}
}

// --- 6. descendants() unit test ---

func TestDescendants_FindsTransitiveChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX only")
	}
	// Spawn a small process tree: sh that sleeps holding two child sleeps.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "(sleep 5) & (sleep 5) & wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	// Poll until the subshells have forked the sleeps, with a generous
	// timeout so the test stays robust on slow CI workers.
	deadline := time.Now().Add(2 * time.Second)
	var got []int
	for time.Now().Before(deadline) {
		got = descendants(cmd.Process.Pid)
		if len(got) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) < 2 {
		t.Errorf("descendants(%d) = %v, want at least 2 (the two sleeps)", cmd.Process.Pid, got)
	}
	for _, pid := range got {
		if pid == cmd.Process.Pid {
			t.Errorf("descendants included root pid %d", pid)
		}
		if pid <= 0 {
			t.Errorf("descendants returned non-positive pid: %d", pid)
		}
	}
	_ = strconv.Itoa // satisfy import for future expansion
}

// --- 7. Optional integration test against real `air` if it's installed.
//        This is the actual user scenario. Skipped when air isn't on PATH so
//        CI in a barebones container still passes.

func TestRun_SIGINT_PropagatesThroughRealAir(t *testing.T) {
	if _, err := exec.LookPath("air"); err != nil {
		t.Skip("air not on PATH; install with: go install github.com/air-verse/air@latest")
	}
	// Build the same detaching-helper trick but use air as the wrapper.
	// We give air a config that runs our helper as its "build target", so
	// air spawns it the same way it would spawn a real binary. If signal
	// propagation works through air, both air and the helper exit on SIGINT.
	dir := t.TempDir()
	helper := buildDetachingHelper(t)
	cfg := filepath.Join(dir, ".air.toml")
	cfgContents := "root = \"" + dir + "\"\n" +
		"tmp_dir = \"" + dir + "/tmp\"\n" +
		"[build]\n" +
		"  bin = \"" + helper + "\"\n" +
		"  cmd = \"true\"\n" + // no-op build (helper already exists)
		"  delay = 100\n" +
		"  exclude_dir = [\"tmp\"]\n"
	if err := writeFile(cfg, cfgContents); err != nil {
		t.Fatal(err)
	}

	r := runForSignals(t,
		[]string{"air", "-c", cfg},
		syscall.SIGINT,
		5*time.Second, 8*time.Second,
	)
	if r.timedOut {
		// Best-effort cleanup of any leaked processes from the failed test.
		_ = exec.Command("pkill", "-f", helper).Run()
		t.Fatal("trail did not exit when SIGINT was sent — air's grandchild escaped signal forwarding")
	}
}

// silence unused-import warning if test build prunes anything unexpectedly.
var (
	_ = errors.Is
	_ = sync.Mutex{}
)
