package mcp

import (
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

const (
	toolListSessions = "list_sessions"
	toolGetLogs      = "get_logs"
)

func listSessionsTool() mcpsdk.Tool {
	return mcpsdk.NewTool(toolListSessions,
		mcpsdk.WithDescription(
			`List all log capture sessions stored by trail. Returns session metadata `+
				`including the absolute file path of each session's JSONL file. If get_logs `+
				`results look incomplete or you need raw context, you can read the file_path `+
				`directly with your file tools — the JSONL format is documented and stable.`,
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(false),
		mcpsdk.WithBoolean("active_only",
			mcpsdk.Description("If true, return only sessions whose capture process is still running."),
			mcpsdk.DefaultBool(false),
		),
		mcpsdk.WithNumber("limit",
			mcpsdk.Description("Maximum number of sessions to return (default 20, max 100)."),
			mcpsdk.DefaultNumber(20),
			mcpsdk.Max(100),
		),
	)
}

func getLogsTool() mcpsdk.Tool {
	return mcpsdk.NewTool(toolGetLogs,
		mcpsdk.WithDescription(
			`Query log entries from a capture session. Filters AND together. Time-based `+
				`filters (start_time/end_time), the duration filter, and line-based filters `+
				`(start_line/end_line) are mutually exclusive — use at most one of those `+
				`three groups per call. Line numbers refer to log entries (entry 1 = first `+
				`real log line, not the meta header). The response includes the absolute `+
				`file path; if results look truncated or you need full context, read the `+
				`JSONL file directly with your file tools.`,
		),
		mcpsdk.WithReadOnlyHintAnnotation(true),
		mcpsdk.WithDestructiveHintAnnotation(false),
		mcpsdk.WithIdempotentHintAnnotation(true),
		mcpsdk.WithOpenWorldHintAnnotation(false),
		mcpsdk.WithString("session_id",
			mcpsdk.Description("Session UUID. List sessions first via list_sessions to discover IDs."),
			mcpsdk.Required(),
		),
		mcpsdk.WithObject("filters",
			mcpsdk.Description("Optional filters applied to the session's entries."),
			mcpsdk.Properties(map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max entries to return (default 100, max 1000).",
					"default":     100,
					"maximum":     1000,
				},
				"page": map[string]any{
					"type":        "integer",
					"description": "1-based page over (limit) results.",
					"default":     1,
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Case-insensitive regex matched against the message field.",
				},
				"level": map[string]any{
					"type":        "string",
					"description": "Filter by level. Use \"all\" or omit to disable.",
					"enum":        []string{"error", "warn", "info", "debug", "unknown", "all"},
					"default":     "all",
				},
				"start_time": map[string]any{
					"type":        "string",
					"description": "ISO8601 (RFC3339) timestamp. Mutually exclusive with duration and line range.",
				},
				"end_time": map[string]any{
					"type":        "string",
					"description": "ISO8601 (RFC3339) timestamp. Only valid alongside start_time.",
				},
				"duration": map[string]any{
					"type":        "string",
					"description": "Go-style duration like \"10m\", \"2h\", \"1h30m\". Window is [now - duration, now]. Mutually exclusive with start_time/end_time and line range.",
				},
				"start_line": map[string]any{
					"type":        "integer",
					"description": "1-based log-entry index. Mutually exclusive with time-based filters.",
				},
				"end_line": map[string]any{
					"type":        "integer",
					"description": "1-based log-entry index. Use with start_line.",
				},
				"order": map[string]any{
					"type":        "string",
					"description": "Result ordering.",
					"enum":        []string{"newest", "oldest"},
					"default":     "newest",
				},
			}),
		),
	)
}
