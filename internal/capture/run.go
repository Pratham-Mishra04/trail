package capture

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

// RunOptions configures a single Run invocation. Args is the child command
// (Args[0] is the binary, Args[1:] are arguments). Store is the persistence
// backend the new session is created against.
type RunOptions struct {
	Store        store.Store
	Args         []string
	Name         string // optional; auto-generated from Args if empty
	Ephemeral    bool
	Passthrough  bool
	Stdout       io.Writer
	Stderr       io.Writer
	TrailVersion string // recorded in meta.Trail
}

// Run forks Args as a child process, captures stdout+stderr to a new session,
// forwards SIGINT/SIGTERM/SIGHUP to the child, waits for the child to exit,
// and returns its exit code.
//
// Exit code follows shell convention:
//   - normal exit → child's exit code
//   - signal-killed → 128 + signal number
//
// On clean exit with Ephemeral=true, the session file is deleted.
func Run(ctx context.Context, opts RunOptions) (int, error) {
	if len(opts.Args) == 0 {
		return -1, errors.New("no command provided")
	}
	if opts.Store == nil {
		return -1, errors.New("no store provided")
	}

	name := opts.Name
	if name == "" {
		name = autoName(opts.Args)
	}

	cmd := exec.CommandContext(ctx, opts.Args[0], opts.Args[1:]...)

	meta := logentry.MetaHeader{
		Name:        name,
		Source:      logentry.SourceRun,
		CapturerPID: os.Getpid(),
		Command:     strings.Join(opts.Args, " "),
		StartedAt:   time.Now().UTC(),
		Ephemeral:   opts.Ephemeral,
		Trail:       opts.TrailVersion,
		// CapturedPID is filled in by Exec after cmd.Start() succeeds.
	}

	return Exec(ctx, ExecOptions{
		Cmd:   cmd,
		Meta:  meta,
		Store: opts.Store,
		PipelineOpts: PipelineOptions{
			Passthrough: opts.Passthrough,
			Stdout:      opts.Stdout,
			Stderr:      opts.Stderr,
		},
	})
}

// autoName builds a session label from the child's argv:
//   - basename of args[0] joined with the rest of the args
//   - trimmed to 80 runes (rune-counted so multibyte chars don't split)
func autoName(args []string) string {
	out := filepath.Base(args[0])
	if len(args) > 1 {
		out = out + " " + strings.Join(args[1:], " ")
	}
	runes := []rune(out)
	if len(runes) > 80 {
		out = string(runes[:80])
	}
	return out
}
