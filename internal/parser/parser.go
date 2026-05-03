// Package parser performs conservative log-level detection on captured lines.
//
// Detection is intentionally narrow: a line gets a non-"unknown" level only
// when there is unambiguous evidence (JSON level field, logfmt level= pair,
// or a recognized level keyword at the start of the line). Free-text matches
// like substring "info" or stderr-stream-implies-error are deliberately not
// supported — false positives degrade the get_logs filter UX.
package parser

import (
	"regexp"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
)

// Detect inspects raw (one captured line, no trailing newline) and returns:
//   - level: one of logentry.Level* constants
//   - message: cleaned message (level prefix stripped only when a line-prefix
//     pattern matched; otherwise == raw)
//
// stream is currently unused — kept on the signature for future stream-aware
// heuristics so callers don't have to update their call sites later.
func Detect(stream, raw string) (level, message string) {
	if lvl, ok := detectJSONLevel(raw); ok {
		return lvl, raw
	}
	if lvl, ok := detectLogfmtLevel(raw); ok {
		return lvl, raw
	}
	if lvl, msg, ok := detectLinePrefix(raw); ok {
		return lvl, msg
	}
	return logentry.LevelUnknown, raw
}

// --- JSON ---

var jsonLevelKeys = []string{"level", "severity", "lvl"}

func detectJSONLevel(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", false
	}
	for _, key := range jsonLevelKeys {
		v := gjson.Get(trimmed, key)
		if v.Type != gjson.String {
			continue
		}
		if lvl := normalizeLevel(v.Str); lvl != logentry.LevelUnknown {
			return lvl, true
		}
	}
	return "", false
}

// --- Logfmt ---

// Matches `level=<word>` anywhere on the line (case-insensitive). The word is
// captured as group 1 and run through normalizeLevel.
var logfmtLevelRE = regexp.MustCompile(`(?i)\blevel=([A-Za-z]+)`)

func detectLogfmtLevel(raw string) (string, bool) {
	m := logfmtLevelRE.FindStringSubmatch(raw)
	if m == nil {
		return "", false
	}
	if lvl := normalizeLevel(m[1]); lvl != logentry.LevelUnknown {
		return lvl, true
	}
	return "", false
}

// --- Line prefix ---

// Matches one of the recognized level keywords at the start of a line, after
// optional ISO-style timestamp and whitespace. The level keyword must end on
// a word boundary so INFOTAINMENT etc. don't false-match. An optional
// bracketed token (e.g. `INFO[0034]` from logrus) and any trailing colons or
// whitespace are also consumed so the message after stripping is clean.
var levelLinePrefixRE = regexp.MustCompile(
	`(?i)^\s*` +
		`(?:\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}\S*\s+)?` +
		`(ERROR|ERR|FATAL|PANIC|CRITICAL|CRIT|WARN|WARNING|INFO|NOTICE|DEBUG|TRACE|VERBOSE)\b` +
		`(?:\[[^\]]*\])?` +
		`[:\s]*`,
)

func detectLinePrefix(raw string) (level, stripped string, ok bool) {
	loc := levelLinePrefixRE.FindStringSubmatchIndex(raw)
	if loc == nil {
		return "", "", false
	}
	word := raw[loc[2]:loc[3]]
	lvl := normalizeLevel(word)
	if lvl == logentry.LevelUnknown {
		return "", "", false
	}
	return lvl, raw[loc[1]:], true
}

// --- normalization ---

func normalizeLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "error", "err", "fatal", "panic", "critical", "crit":
		return logentry.LevelError
	case "warn", "warning":
		return logentry.LevelWarn
	case "info", "notice":
		return logentry.LevelInfo
	case "debug", "trace", "verbose":
		return logentry.LevelDebug
	default:
		return logentry.LevelUnknown
	}
}
