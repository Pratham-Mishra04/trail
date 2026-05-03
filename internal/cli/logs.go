package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

func LogsCmd(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		sessionID string
		n         int
		level     string
		query     string
		format    string
	)
	fs.StringVar(&sessionID, "session", "", "Session ID (required)")
	fs.IntVar(&n, "n", 100, "Maximum number of entries to show (most recent N)")
	fs.StringVar(&level, "level", "", "Filter by level: error|warn|info|debug|unknown|all")
	fs.StringVar(&query, "query", "", "Case-insensitive regex matched against the message field")
	fs.StringVar(&format, "format", "pretty", "Output format: pretty|json")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: trail logs --session <id> [flags]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Reads entries from one capture session, applies optional filters,")
		fmt.Fprintln(stderr, "and prints them with the most recent at the bottom.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Find a session id with: trail sessions")
	}

	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	if sessionID == "" {
		fmt.Fprintln(stderr, "trail logs: --session is required")
		fmt.Fprintln(stderr, "List sessions with: trail sessions")
		return 2
	}
	level = strings.ToLower(strings.TrimSpace(level))
	if level != "" && !logentry.IsValidLevel(level) {
		fmt.Fprintf(stderr, "trail logs: invalid --level %q (allowed: error|warn|info|debug|unknown|all)\n", level)
		return 2
	}
	if format != "pretty" && format != "json" {
		fmt.Fprintf(stderr, "trail logs: invalid --format %q (allowed: pretty|json)\n", format)
		return 2
	}
	if n <= 0 {
		fmt.Fprintln(stderr, "trail logs: --n must be > 0")
		return 2
	}

	filter := store.Filter{
		Limit: n,
		Page:  1,
		Order: store.OrderNewest,
	}
	if query != "" {
		re, err := regexp.Compile("(?i)" + query)
		if err != nil {
			fmt.Fprintf(stderr, "trail logs: invalid --query regex: %v\n", err)
			return 2
		}
		filter.Query = re
	}
	if level != "" && level != "all" {
		filter.Level = &level
	}

	s, err := store.NewJSONL()
	if err != nil {
		fmt.Fprintf(stderr, "trail logs: %v\n", err)
		return 1
	}

	ctx, stop := signalCtx()
	defer stop()
	res, err := s.Read(ctx, sessionID, filter)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "trail logs: session %s not found\n", sessionID)
			fmt.Fprintln(stderr, "List sessions with: trail sessions")
			return 1
		}
		fmt.Fprintf(stderr, "trail logs: %v\n", err)
		return 1
	}

	if format == "json" {
		return emitLogsJSON(res.Entries)
	}
	return emitLogsPretty(res.Entries, res.TotalMatched)
}

func emitLogsPretty(entries []logentry.LogEntry, totalMatched int) int {
	if len(entries) == 0 {
		fmt.Fprintln(stdout, styleHint.Render("no entries match"))
		return 0
	}

	// store.Read returned newest-first (most recent at index 0). We print
	// chronologically — oldest at top, newest at bottom — so reading
	// top-to-bottom matches the natural log scan and tail-like UX.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		ts := styleDim.Render(e.Timestamp.Local().Format("15:04:05.000"))
		lvl := levelBadge(e.Level)
		stream := styleHint.Render(fmt.Sprintf("%-6s", e.Stream))
		fmt.Fprintf(stdout, "%s  %s  %s  %s\n", ts, lvl, stream, e.Message)
	}

	if totalMatched > len(entries) {
		fmt.Fprintln(stdout, styleHint.Render(
			fmt.Sprintf("\n— showing the most recent %d of %d matching entries (use -n to change limit) —", len(entries), totalMatched),
		))
	}
	return 0
}

func emitLogsJSON(entries []logentry.LogEntry) int {
	return printJSON("logs", entries)
}
