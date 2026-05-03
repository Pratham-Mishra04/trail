// Package docker captures the log stream of a running Docker container by
// shelling out to `docker logs -f <container>` and feeding the result through
// the shared internal/capture pipeline.
//
// All the heavy lifting (pipes, signals, exit-code propagation, ephemeral
// cleanup) is delegated to capture.Exec. This package just builds the
// docker-specific argv and metadata.
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/capture"
	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

// RunOptions configures a single Run invocation against a Docker container.
type RunOptions struct {
	Store        store.Store
	Container    string // container name or ID
	Name         string // optional session label; defaults to container
	Since        string // pass-through to `docker logs --since`; "" → docker default
	Ephemeral    bool
	Passthrough  bool
	Stdout       io.Writer
	Stderr       io.Writer
	TrailVersion string
}

// Run shells out to `docker logs -f <container>`, captures its output to a
// new session, and returns the exit code of `docker logs` (0 on clean exit
// when the container stops or `docker logs` is signaled).
//
// Returns a clear error if the `docker` CLI isn't on PATH.
func Run(ctx context.Context, opts RunOptions) (int, error) {
	if opts.Container == "" {
		return -1, errors.New("no container provided")
	}
	if opts.Store == nil {
		return -1, errors.New("no store provided")
	}

	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return -1, fmt.Errorf(
			"docker CLI not found on PATH (install Docker: https://docs.docker.com/get-docker/, " +
				"or capture the process directly with: trail run -- <command>)",
		)
	}

	args := []string{"logs", "-f"}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}
	args = append(args, opts.Container)

	name := opts.Name
	if name == "" {
		name = opts.Container
	}

	cmd := exec.CommandContext(ctx, dockerPath, args...)

	meta := logentry.MetaHeader{
		Name:        name,
		Source:      logentry.SourceDocker,
		CapturerPID: os.Getpid(),
		Container:   opts.Container,
		StartedAt:   time.Now().UTC(),
		Ephemeral:   opts.Ephemeral,
		Trail:       opts.TrailVersion,
	}

	return capture.Exec(ctx, capture.ExecOptions{
		Cmd:   cmd,
		Meta:  meta,
		Store: opts.Store,
		PipelineOpts: capture.PipelineOptions{
			Passthrough: opts.Passthrough,
			Stdout:      opts.Stdout,
			Stderr:      opts.Stderr,
		},
	})
}
