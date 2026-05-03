package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Pratham-Mishra04/trail/internal/capture"
	"github.com/Pratham-Mishra04/trail/internal/cli"
)

// Set by goreleaser via -ldflags "-X main.version=... -X main.commit=... -X main.date=..."
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli.SetVersion(version, commit, date)
	capture.SetVersion(version)

	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	sub, rest := os.Args[1], os.Args[2:]

	switch sub {
	case "run":
		os.Exit(cli.RunCmd(rest))
	case "sessions":
		os.Exit(cli.SessionsCmd(rest))
	case "logs":
		os.Exit(cli.LogsCmd(rest))
	case "mcp":
		os.Exit(cli.MCPCmd(rest))
	case "docker":
		os.Exit(cli.DockerCmd(rest))
	case "version", "-v", "--version":
		os.Exit(cli.VersionCmd(rest))
	case "help", "-h", "--help":
		usage(os.Stdout)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "trail: unknown subcommand %q\n\n", sub)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `trail — capture process logs and feed them to AI agents via MCP

Usage:
  trail <subcommand> [flags]

Subcommands:
  run       Run a command and capture its stdout/stderr to a session
  docker    Capture logs from a running Docker container
  mcp       Run the stdio MCP server (spawned by your editor)
  logs      View logs from a captured session
  sessions  List or delete capture sessions
  version   Print version information
  help      Show this help

Run 'trail <subcommand> -h' for subcommand-specific flags.
`)
}
