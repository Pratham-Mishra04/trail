package cli

import (
	"flag"
	"fmt"

	"github.com/Pratham-Mishra04/trail/internal/docker"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

func DockerCmd(args []string) int {
	fs := flag.NewFlagSet("docker", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		name          string
		since         string
		ephemeral     bool
		noPassthrough bool
	)
	fs.StringVar(&name, "name", "", "Session label (default: container name)")
	fs.StringVar(&since, "since", "", `Pass-through to "docker logs --since" (e.g. "10m", "2h"). When unset, docker's own default applies (all logs from container start, then follow).`)
	fs.BoolVar(&ephemeral, "ephemeral", false, "Delete the session file when docker logs exits cleanly")
	fs.BoolVar(&noPassthrough, "no-passthrough", false, "Suppress echoing captured lines back to the terminal")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: trail docker [flags] <container>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Captures the container's stdout and stderr to a new session by")
		fmt.Fprintln(stderr, `shelling out to "docker logs -f <container>".`)
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Examples:")
		fmt.Fprintln(stderr, "  trail docker my-api-container")
		fmt.Fprintln(stderr, "  trail docker my-api-container --since 10m")
		fmt.Fprintln(stderr, "  trail docker --name api --ephemeral my-api-container")
	}

	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "trail docker: missing container name")
		fmt.Fprintln(stderr, "Usage: trail docker [flags] <container>")
		return 2
	}
	if len(rest) > 1 {
		fmt.Fprintf(stderr, "trail docker: unexpected arguments after container: %v\n", rest[1:])
		return 2
	}
	container := rest[0]

	s, err := store.NewJSONL()
	if err != nil {
		fmt.Fprintf(stderr, "trail docker: %v\n", err)
		return 1
	}

	ctx, stop := signalCtx()
	defer stop()
	code, err := docker.Run(ctx, docker.RunOptions{
		Store:        s,
		Container:    container,
		Name:         name,
		Since:        since,
		Ephemeral:    ephemeral,
		Passthrough:  !noPassthrough,
		Stdout:       stdout,
		Stderr:       stderr,
		TrailVersion: version,
	})
	if err != nil {
		fmt.Fprintf(stderr, "trail docker: %v\n", err)
		if code <= 0 {
			return 1
		}
	}
	return code
}
