// Package capture turns raw stdout/stderr byte streams into LogEntries and
// appends them to a store.Session.
//
// One Pipeline owns one session for life. Process is safe to call from
// multiple goroutines concurrently (one per stream) — the underlying
// Session.Append is responsible for serialization.
package capture

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/parser"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

// maxRawLen caps the stored Raw field. Longer lines get truncated with a
// suffix indicating how many bytes were dropped. Keeps the marshaled JSON
// comfortably under PIPE_BUF (4096) so kernel writes stay atomic.
const maxRawLen = 4000

// Pipeline ties a store.Session to a parser. Construct one per session, then
// call Process for each input stream (typically once for stdout, once for
// stderr, each on its own goroutine).
type Pipeline struct {
	session store.Session
	opts    PipelineOptions
}

// PipelineOptions controls pass-through behavior. When Passthrough is true,
// every captured line is also written to the matching writer (Stdout for
// stdout-stream lines, Stderr for stderr-stream lines) with a trailing
// newline restored.
type PipelineOptions struct {
	Passthrough bool
	Stdout      io.Writer
	Stderr      io.Writer
}

func NewPipeline(s store.Session, opts PipelineOptions) *Pipeline {
	return &Pipeline{session: s, opts: opts}
}

// Process reads r line-by-line until EOF (or ctx cancellation), feeding each
// line through the parser and appending it to the session. stream must be one
// of logentry.StreamStdout / StreamStderr — the caller knows which fd r came
// from. A trailing partial line (no '\n' at EOF) is captured as a final entry.
func (p *Pipeline) Process(ctx context.Context, stream string, r io.Reader) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		if len(line) > 0 {
			if err := p.processLine(stream, string(line)); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("capture %s: %w", stream, readErr)
		}
	}
}

func (p *Pipeline) processLine(stream, raw string) error {
	// Echo the original (un-truncated) line to the user's terminal first, so
	// pass-through behaves identically to running the app without trail.
	if p.opts.Passthrough {
		w := p.opts.Stdout
		if stream == logentry.StreamStderr {
			w = p.opts.Stderr
		}
		if w != nil {
			_, _ = io.WriteString(w, raw)
			_, _ = io.WriteString(w, "\n")
		}
	}

	// Detect on the *full* line so a truncated tail doesn't break JSON parse
	// or strip a level keyword that lives near the end of a long line.
	level, message := parser.Detect(stream, raw)

	stored := raw
	if len(stored) > maxRawLen {
		dropped := len(stored) - maxRawLen
		stored = stored[:maxRawLen] + fmt.Sprintf(" ... [truncated %d bytes]", dropped)
		// Truncate Message to the same prefix so it doesn't dwarf Raw on disk.
		// (For line-prefix detections Message is shorter than Raw, so this is
		// usually a no-op; for JSON/logfmt it's cosmetic — Raw is the truth.)
		if len(message) > maxRawLen {
			message = message[:maxRawLen] + fmt.Sprintf(" ... [truncated %d bytes]", len(message)-maxRawLen)
		}
	}

	return p.session.Append(logentry.LogEntry{
		Stream:    stream,
		Level:     level,
		Raw:       stored,
		Message:   message,
		Timestamp: time.Now().UTC(),
	})
}
