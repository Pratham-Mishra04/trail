package cli

import (
	"flag"
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

func SessionsCmd(args []string) int {
	if len(args) > 0 && args[0] == "rm" {
		return sessionsRm(args[1:])
	}
	return sessionsList(args)
}

func sessionsList(args []string) int {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Emit JSON instead of a table")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: trail sessions [flags]")
		fmt.Fprintln(stderr, "       trail sessions rm <session-id> [...]")
		fmt.Fprintln(stderr, "       trail sessions rm --all")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Lists sessions captured under ~/.config/trail/sessions/.")
		fmt.Fprintln(stderr, "Active sessions are shown first; ended sessions follow by start time desc.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	s, err := store.NewJSONL()
	if err != nil {
		fmt.Fprintf(stderr, "trail sessions: %v\n", err)
		return 1
	}
	ctx, stop := signalCtx()
	defer stop()
	infos, err := s.List(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "trail sessions: %v\n", err)
		return 1
	}

	if jsonOut {
		return emitSessionsJSON(infos)
	}
	return emitSessionsTable(infos)
}

func sessionsRm(args []string) int {
	fs := flag.NewFlagSet("sessions rm", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var all bool
	fs.BoolVar(&all, "all", false, "Delete every session")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: trail sessions rm <session-id> [...]")
		fmt.Fprintln(stderr, "       trail sessions rm --all")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Deletes session files. With --all, removes every session.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	s, err := store.NewJSONL()
	if err != nil {
		fmt.Fprintf(stderr, "trail sessions rm: %v\n", err)
		return 1
	}
	ctx, stop := signalCtx()
	defer stop()

	if all {
		if rest := fs.Args(); len(rest) > 0 {
			fmt.Fprintln(stderr, "trail sessions rm: --all does not take session ids")
			return 2
		}
		n, err := s.DeleteAll(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "trail sessions rm: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "deleted %d session(s)\n", n)
		return 0
	}

	ids := fs.Args()
	if len(ids) == 0 {
		fmt.Fprintln(stderr, "trail sessions rm: missing session id (use --all to delete every session)")
		return 2
	}

	failed := 0
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			fmt.Fprintf(stderr, "trail sessions rm: %s: %v\n", id, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "deleted %s\n", id)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// --- output formats ---

func emitSessionsJSON(infos []store.SessionInfo) int {
	views := make([]logentry.SessionView, 0, len(infos))
	for _, info := range infos {
		views = append(views, logentry.NewSessionView(info.Meta, info.IsActive, info.ApproxEndedAt))
	}
	return printJSON("sessions", views)
}

func emitSessionsTable(infos []store.SessionInfo) int {
	if len(infos) == 0 {
		fmt.Fprintln(stdout, styleHint.Render("no sessions yet — capture one with: trail run -- <command>"))
		return 0
	}

	rows := make([][]string, 0, len(infos))
	for _, info := range infos {
		rows = append(rows, []string{
			info.Meta.SessionID,
			statusBadge(info.IsActive),
			truncate(info.Meta.Name, 30),
			info.Meta.Source,
			relativeTime(info.Meta.StartedAt),
			truncate(info.Meta.Command, 50),
		})
	}

	cellPadding := lipgloss.NewStyle().Padding(0, 1)
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleBorder).
		Headers("SESSION ID", "STATUS", "NAME", "SOURCE", "STARTED", "COMMAND").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return cellPadding.Foreground(colorHint).Bold(true)
			}
			return cellPadding
		})

	fmt.Fprintln(stdout, t.Render())
	return 0
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 0:
		return t.Format(time.RFC3339)
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func truncate(s string, n int) string {
	// Collapse newlines/tabs first — multiline values explode tabular layouts.
	s = collapseWhitespace(s)
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 3 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}

func collapseWhitespace(s string) string {
	var b []byte
	prevSpace := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' || c == '\t' {
			c = ' '
		}
		if c == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		b = append(b, c)
	}
	return string(b)
}
