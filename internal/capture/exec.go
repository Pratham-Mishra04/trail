package capture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

// ExecOptions is what callers pass to Exec. The Cmd must be configured (path,
// args, env) but NOT yet started — Exec calls Start itself, after wiring the
// pipes. Meta is the session metadata (Source, Name, Container, Command, etc.)
// pre-built by the caller; Exec fills in CapturedPID after Start succeeds.
type ExecOptions struct {
	Cmd          *exec.Cmd
	Meta         logentry.MetaHeader
	Store        store.Store
	PipelineOpts PipelineOptions
}

// Exec starts the configured *exec.Cmd, opens stdout/stderr pipes, creates a
// session in Store, drains both streams through a Pipeline, forwards
// SIGINT/SIGTERM/SIGHUP to the child, waits for the child to exit, and
// returns its exit code (shell convention: 128+signal for signal-killed).
//
// On clean exit with Meta.Ephemeral=true, the session file is deleted.
//
// This is the shared backbone for capture.Run (trail run) and
// docker.Run (trail docker). Callers handle command-specific concerns
// (argv construction, name defaulting, error wrapping); Exec handles the
// generic process-lifecycle plumbing.
func Exec(ctx context.Context, opts ExecOptions) (int, error) {
	if opts.Cmd == nil {
		return -1, errors.New("nil cmd")
	}
	if opts.Store == nil {
		return -1, errors.New("nil store")
	}

	stdoutPipe, err := opts.Cmd.StdoutPipe()
	if err != nil {
		return -1, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := opts.Cmd.StderrPipe()
	if err != nil {
		return -1, fmt.Errorf("stderr pipe: %w", err)
	}

	// Put the child in its own process group. Two reasons:
	//   1. Wrappers like `air` or `nodemon` spawn their own children and may
	//      detach internally — without an explicit child pgid, terminal SIGINT
	//      can race or miss those grandchildren.
	//   2. We can then signal the entire tree at once with kill(-pgid, sig)
	//      below, instead of just the immediate child.
	if opts.Cmd.SysProcAttr == nil {
		opts.Cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	opts.Cmd.SysProcAttr.Setpgid = true

	if err := opts.Cmd.Start(); err != nil {
		return -1, fmt.Errorf("start %q: %w", opts.Cmd.Path, err)
	}

	// Patch CapturedPID now that we have one (only meaningful for `run`; for
	// `docker` the caller leaves Source=docker and the field stays unused).
	if opts.Meta.Source == logentry.SourceRun {
		opts.Meta.CapturedPID = opts.Cmd.Process.Pid
	}

	sess, err := opts.Store.Create(opts.Meta)
	if err != nil {
		_ = opts.Cmd.Process.Kill()
		_ = opts.Cmd.Wait()
		return -1, fmt.Errorf("create session: %w", err)
	}
	defer sess.Close()

	// Surface the session id + file path so the user (and any agent watching
	// stderr) can address the session immediately, without polling
	// list_sessions. This is what the debug-with-trail SKILL relies on to
	// follow up after a restart. Banner auto-degrades to a plain line when
	// stderr isn't a TTY (CI, piped output).
	PrintBanner(opts.Meta.Name, sess.SessionID(), sess.Path())

	pipeline := NewPipeline(sess, opts.PipelineOpts)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)
	sigDone := make(chan struct{})
	defer close(sigDone)

	go func() {
		for {
			select {
			case sig := <-sigCh:
				if opts.Cmd.Process == nil {
					continue
				}
				sysSig, ok := sig.(syscall.Signal)
				if !ok {
					continue
				}
				forwardSignal(opts.Cmd.Process.Pid, sysSig)
			case <-sigDone:
				return
			}
		}
	}()

	// Each Process call drains one of the child's stdio pipes. If one fails
	// (e.g. session append error) and the child keeps writing, the other
	// goroutine could block forever on a full pipe buffer. So: collect errors
	// via a channel; on the first non-nil, SIGTERM the child to unblock the
	// remaining reader.
	errCh := make(chan error, 2)
	go func() {
		errCh <- pipeline.Process(ctx, logentry.StreamStdout, stdoutPipe)
	}()
	go func() {
		errCh <- pipeline.Process(ctx, logentry.StreamStderr, stderrPipe)
	}()

	first := <-errCh
	if first != nil && opts.Cmd.Process != nil {
		forwardSignal(opts.Cmd.Process.Pid, syscall.SIGTERM)
	}
	second := <-errCh

	exitCode := exitCodeFrom(opts.Cmd.Wait())
	// On a pipeline error the child may still have exited normally — we return
	// the child's exit code (transparent-wrapper semantics) plus the pipeline
	// error so callers can decide whether to bump exit on trail-side problems.
	// CLI callers convert (code=0, err!=nil) → exit 1; scripted callers can
	// ignore the error and trust the child's code.
	if procErr := firstNonTrivial(first, second); procErr != nil {
		return exitCode, procErr
	}
	return exitCode, nil
}

// exitCodeFrom converts cmd.Wait's error into a shell-style exit code.
func exitCodeFrom(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 1
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
		switch {
		case status.Exited():
			return status.ExitStatus()
		case status.Signaled():
			return 128 + int(status.Signal())
		}
	}
	if code := exitErr.ExitCode(); code >= 0 {
		return code
	}
	return 1
}

func firstNonTrivial(errs ...error) error {
	for _, e := range errs {
		if e == nil {
			continue
		}
		if errors.Is(e, io.EOF) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			continue
		}
		return e
	}
	return nil
}

// forwardSignal sends sig to the immediate child, the child's process group,
// and every descendant we can find. Wrappers like air, nodemon, and tini
// often fork their own children into fresh process groups; signaling only
// the immediate child or its pgid leaves grandchildren orphaned. Walking
// the descendant tree catches them regardless of pgid layout.
//
// We snapshot descendants BEFORE signaling — pgrep -P walks ppid links, so
// once the parent dies its children get reparented to init and disappear.
// Parent goes last so it can't respawn anything we just killed. Errors are
// deliberately ignored: by the time we observe the tree some processes may
// already be exiting, and we'd rather over-signal a dying process than
// under-signal a live one.
func forwardSignal(rootPid int, sig syscall.Signal) {
	descs := descendants(rootPid)
	if pgid, err := syscall.Getpgid(rootPid); err == nil {
		_ = syscall.Kill(-pgid, sig)
	}
	for _, pid := range descs {
		_ = syscall.Kill(pid, sig)
	}
	_ = syscall.Kill(rootPid, sig)
}

// descendants returns every transitive child of root, in BFS order. Uses
// pgrep -P (POSIX-portable across macOS + Linux) to walk one generation at
// a time. Returns an empty slice on any error rather than panicking — a
// signal forwarder must never fail.
func descendants(root int) []int {
	var out []int
	queue := []int{root}
	seen := map[int]bool{root: true}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		raw, err := exec.Command("pgrep", "-P", strconv.Itoa(parent)).Output()
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
			if line == "" {
				continue
			}
			child, err := strconv.Atoi(line)
			if err != nil || seen[child] {
				continue
			}
			seen[child] = true
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}
