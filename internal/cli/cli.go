package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/bytedance/sonic"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// stdout/stderr are package-level so tests can swap them.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

func SetVersion(v, c, d string) {
	version, commit, date = v, c, d
}

// signalCtx returns a context that cancels on SIGINT/SIGTERM. Used by every
// subcommand so Ctrl+C interrupts an in-flight Read/DeleteAll cleanly instead
// of killing the process mid-syscall and skipping defers.
//
// Caller must invoke the returned cancel func (typically via defer) to release
// the signal handler — otherwise it's a process-global leak.
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// parseFlags wraps fs.Parse with the standard subcommand exit-code contract.
// Caller pattern:
//
//	if code, ok := parseFlags(fs, args); !ok {
//	    return code
//	}
//
// Returns (0, false) for -h/--help (cleanly exit 0) and (2, false) for any
// other parse error (exit 2 = usage). On success returns (0, true).
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	return 0, true
}

// printJSON marshals v as indented JSON to stdout. Returns 0 on success or 1
// if marshaling failed (the failure is reported to stderr, prefixed with the
// subcommand name for consistency with the rest of CLI errors).
func printJSON(subcmd string, v any) int {
	b, err := sonic.ConfigStd.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "trail %s: marshal: %v\n", subcmd, err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
