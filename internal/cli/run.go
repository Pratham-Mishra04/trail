package cli

import (
	"flag"
	"fmt"

	"github.com/Pratham-Mishra04/trail/internal/capture"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

func RunCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		name          string
		ephemeral     bool
		noPassthrough bool
	)
	fs.StringVar(&name, "name", "", "Session label (default: derived from command, max 80 chars)")
	fs.BoolVar(&ephemeral, "ephemeral", false, "Delete the session file when the child exits cleanly")
	fs.BoolVar(&noPassthrough, "no-passthrough", false, "Suppress echoing captured lines back to the terminal")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: trail run [flags] -- <command> [args...]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Captures the child's stdout and stderr to a new session under")
		fmt.Fprintln(stderr, "~/.config/trail/sessions/. Forwards SIGINT/SIGTERM/SIGHUP to the child")
		fmt.Fprintln(stderr, "and exits with the child's exit code.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Examples:")
		fmt.Fprintln(stderr, "  trail run -- node server.js")
		fmt.Fprintln(stderr, `  trail run --name api -- python -m http.server 8000`)
		fmt.Fprintln(stderr, "  trail run --ephemeral -- ./run-tests.sh")
	}

	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	childArgs := fs.Args()
	if len(childArgs) == 0 {
		fmt.Fprintln(stderr, "trail run: missing command")
		fmt.Fprintln(stderr, "Usage: trail run [flags] -- <command> [args...]")
		return 2
	}

	s, err := store.NewJSONL()
	if err != nil {
		fmt.Fprintf(stderr, "trail run: %v\n", err)
		return 1
	}

	ctx, stop := signalCtx()
	defer stop()
	code, err := capture.Run(ctx, capture.RunOptions{
		Store:        s,
		Args:         childArgs,
		Name:         name,
		Ephemeral:    ephemeral,
		Passthrough:  !noPassthrough,
		Stdout:       stdout,
		Stderr:       stderr,
		TrailVersion: version,
	})
	if err != nil {
		fmt.Fprintf(stderr, "trail run: %v\n", err)
		if code <= 0 {
			return 1
		}
	}
	return code
}
