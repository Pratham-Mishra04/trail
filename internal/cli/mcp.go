package cli

import (
	"flag"
	"fmt"

	logmcp "github.com/Pratham-Mishra04/trail/internal/mcp"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

func MCPCmd(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: trail mcp")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Runs the stdio MCP server. Spawned by editors (Claude Code,")
		fmt.Fprintln(stderr, "Cursor, Windsurf, Claude Desktop), not invoked manually.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Reads JSON-RPC frames from stdin, writes responses to stdout.")
		fmt.Fprintln(stderr, "Diagnostics go to stderr — never stdout (that's the protocol channel).")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Exposes two read-only tools:")
		fmt.Fprintln(stderr, "  list_sessions  — enumerate sessions on disk")
		fmt.Fprintln(stderr, "  get_logs       — query entries from one session, with filters")
	}

	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "trail mcp: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	s, err := store.NewJSONL()
	if err != nil {
		fmt.Fprintf(stderr, "trail mcp: %v\n", err)
		return 1
	}

	if err := logmcp.Serve(version, s); err != nil {
		fmt.Fprintf(stderr, "trail mcp: %v\n", err)
		return 1
	}
	return 0
}
