package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/logger"
)

// readReverseChunkSize controls how many bytes we slurp per backwards step.
// 64 KiB roughly matches a typical pagecache page chain and is enough to
// hold ~600 typical 100-byte log entries — most "newest 100" queries
// terminate within the first chunk.
const readReverseChunkSize = 64 * 1024

// readReverseEligible reports whether a Filter can be served by reverse scan.
// We require:
//   - Order=newest (the natural reverse direction)
//   - Limit>0 (gives us a stopping point)
//   - Page<=1 (deeper pages would need to walk N*Limit matches before
//     collecting Limit, defeating the purpose)
//   - No upper-bounded scan (start_time/end_time/start_line/end_line absent).
//     Reverse scan with these is doable but adds complexity; the forward
//     path with #1's early-exit already handles them well.
func readReverseEligible(f Filter) bool {
	if f.Order != OrderNewest {
		return false
	}
	if f.Limit <= 0 {
		return false
	}
	if f.Page > 1 {
		return false
	}
	if f.StartTime != nil || f.EndTime != nil || f.StartLine != nil || f.EndLine != nil {
		return false
	}
	return true
}

// readReverse seeks to the end of the file and walks backwards in chunks,
// collecting matching entries until f.Limit is reached or we hit the meta
// header (line 1, byte 0). Returns matches in newest-first order.
//
// TotalMatched semantics: when reverse-scan is used, TotalMatched counts only
// the matches we actually scanned through — NOT the total in the file. This
// is intentional: if we scanned the whole file every call to populate
// TotalMatched, we'd lose the optimization entirely. The MCP get_logs
// response uses this number, and "you have at least N matches" is more
// useful to an agent than "I traversed nothing" anyway.
func (s *jsonlStore) readReverse(ctx context.Context, sessionID string, f Filter) (ReadResult, error) {
	path := s.pathFor(sessionID)
	file, err := os.Open(path)
	if err != nil {
		return ReadResult{}, err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			logger.Default().Warn("session %s: close error: %v", sessionID, cerr)
		}
	}()

	stat, err := file.Stat()
	if err != nil {
		return ReadResult{}, fmt.Errorf("stat session: %w", err)
	}
	end := stat.Size()
	if end == 0 {
		return ReadResult{}, nil
	}

	matched := make([]logentry.LogEntry, 0, f.Limit)
	buf := make([]byte, readReverseChunkSize)
	// remainder holds bytes from the current chunk that belong to a line
	// straddling a chunk boundary; they get prepended to the previous chunk.
	var remainder []byte

	pos := end
	scanned := 0
	for pos > 0 && len(matched) < f.Limit {
		if err := ctx.Err(); err != nil {
			return ReadResult{}, err
		}

		readSize := min(int64(readReverseChunkSize), pos)
		pos -= readSize

		chunk := buf[:readSize]
		if _, err := file.ReadAt(chunk, pos); err != nil && !errors.Is(err, io.EOF) {
			return ReadResult{}, fmt.Errorf("read at %d: %w", pos, err)
		}

		// Combine current chunk with any leftover line fragment from the
		// previous (later-in-file) chunk.
		work := chunk
		if len(remainder) > 0 {
			work = append(append(make([]byte, 0, len(chunk)+len(remainder)), chunk...), remainder...)
			remainder = nil
		}

		// If we're not at the very start of the file, the first line in
		// `work` is almost certainly partial (cut by our chunk boundary).
		// Hold it back as remainder for the next (earlier) chunk to complete.
		lines := bytes.Split(work, []byte{'\n'})
		startIdx := 0
		if pos > 0 && len(lines) > 0 {
			remainder = lines[0]
			startIdx = 1
		}

		// Walk the complete lines in reverse (newest first within this chunk).
		// The very last element after Split is whatever follows the final
		// newline — usually empty, occasionally a partial trailing fragment
		// from a writer mid-append. Either way, skip it on this pass.
		usable := lines[startIdx:]
		if len(usable) > 0 && len(usable[len(usable)-1]) == 0 {
			usable = usable[:len(usable)-1]
		}

		for i := len(usable) - 1; i >= 0; i-- {
			line := usable[i]
			if len(line) == 0 {
				continue
			}
			// Skip the meta header (line 1, file position 0).
			if pos == 0 && i == 0 {
				continue
			}

			scanned++
			outcome, ok := preFilter(line, f)
			if !ok {
				logger.Default().Warn("session %s: skipping malformed entry during reverse scan", sessionID)
				continue
			}
			// In reverse scan, preStop never fires (no upper bounds — see
			// readReverseEligible) but be defensive for future filter types.
			if outcome == preStop || outcome == preReject {
				continue
			}
			entry, ok := decodeEntry(line)
			if !ok {
				logger.Default().Warn("session %s: skipping malformed entry during reverse scan", sessionID)
				continue
			}
			if matchEntry(entry, f) {
				matched = append(matched, entry)
				if len(matched) >= f.Limit {
					break
				}
			}
		}
	}

	_ = scanned // (reserved for future telemetry — avoids unused warning if we drop other uses)
	return ReadResult{Entries: matched, TotalMatched: len(matched)}, nil
}
