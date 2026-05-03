package parser

import (
	"testing"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name        string
		stream      string
		raw         string
		wantLevel   string
		wantMessage string // empty means: expect message == raw
	}{
		// Plain text → unknown.
		{
			name:      "plain text",
			stream:    logentry.StreamStdout,
			raw:       "Server listening on :3000",
			wantLevel: logentry.LevelUnknown,
		},
		{
			name:      "empty",
			stream:    logentry.StreamStdout,
			raw:       "",
			wantLevel: logentry.LevelUnknown,
		},
		{
			name:      "stderr alone is not error",
			stream:    logentry.StreamStderr,
			raw:       "progress: 50%",
			wantLevel: logentry.LevelUnknown,
		},

		// JSON.
		{
			name:      "json level=error",
			stream:    logentry.StreamStdout,
			raw:       `{"level":"error","msg":"connection refused"}`,
			wantLevel: logentry.LevelError,
		},
		{
			name:      "json severity=WARN",
			stream:    logentry.StreamStdout,
			raw:       `{"severity":"WARN","ts":1234,"msg":"slow query"}`,
			wantLevel: logentry.LevelWarn,
		},
		{
			name:      "json lvl=fatal",
			stream:    logentry.StreamStdout,
			raw:       `{"lvl":"fatal","detail":"oom"}`,
			wantLevel: logentry.LevelError,
		},
		{
			name:      "json without level field",
			stream:    logentry.StreamStdout,
			raw:       `{"ts":1234,"msg":"hello"}`,
			wantLevel: logentry.LevelUnknown,
		},
		{
			name:      "json with non-string level",
			stream:    logentry.StreamStdout,
			raw:       `{"level":5}`,
			wantLevel: logentry.LevelUnknown,
		},
		{
			name:      "malformed json falls through",
			stream:    logentry.StreamStdout,
			raw:       `{not real json`,
			wantLevel: logentry.LevelUnknown,
		},

		// Logfmt.
		{
			name:      "logfmt level=warn",
			stream:    logentry.StreamStdout,
			raw:       `ts=2026-05-03T10:00:00Z level=warn msg="slow"`,
			wantLevel: logentry.LevelWarn,
		},
		{
			name:      "logfmt LEVEL uppercase",
			stream:    logentry.StreamStdout,
			raw:       `LEVEL=ERROR msg=oops`,
			wantLevel: logentry.LevelError,
		},
		{
			name:      "logfmt level=unknown-value falls through",
			stream:    logentry.StreamStdout,
			raw:       `level=quux msg=foo`,
			wantLevel: logentry.LevelUnknown,
		},

		// Line-prefix patterns.
		{
			name:        "ERROR: prefix",
			stream:      logentry.StreamStdout,
			raw:         "ERROR: connection refused",
			wantLevel:   logentry.LevelError,
			wantMessage: "connection refused",
		},
		{
			name:        "INFO logrus brackets",
			stream:      logentry.StreamStdout,
			raw:         "INFO[0034] starting server",
			wantLevel:   logentry.LevelInfo,
			wantMessage: "starting server",
		},
		{
			name:        "go log std prefix with date and ERROR",
			stream:      logentry.StreamStdout,
			raw:         "2026/05/03 10:00:00 ERROR something failed",
			wantLevel:   logentry.LevelError,
			wantMessage: "something failed",
		},
		{
			name:        "ISO date with WARN",
			stream:      logentry.StreamStdout,
			raw:         "2026-05-03T10:00:00.123Z WARN deprecated API",
			wantLevel:   logentry.LevelWarn,
			wantMessage: "deprecated API",
		},
		{
			name:        "FATAL: prefix",
			stream:      logentry.StreamStderr,
			raw:         "FATAL: out of memory",
			wantLevel:   logentry.LevelError,
			wantMessage: "out of memory",
		},
		{
			name:        "lowercase debug prefix",
			stream:      logentry.StreamStdout,
			raw:         "debug: cache miss for key foo",
			wantLevel:   logentry.LevelDebug,
			wantMessage: "cache miss for key foo",
		},

		// False-positive guards.
		{
			name:      "substring info in middle of line",
			stream:    logentry.StreamStdout,
			raw:       "Starting info gathering subsystem",
			wantLevel: logentry.LevelUnknown,
		},
		{
			name:      "INFOTAINMENT does not match INFO",
			stream:    logentry.StreamStdout,
			raw:       "INFOTAINMENT system online",
			wantLevel: logentry.LevelUnknown,
		},
		{
			name:      "ERRORS plural does not match ERROR",
			stream:    logentry.StreamStdout,
			raw:       "ERRORS were detected later",
			wantLevel: logentry.LevelUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lvl, msg := Detect(tc.stream, tc.raw)
			if lvl != tc.wantLevel {
				t.Errorf("level = %q, want %q", lvl, tc.wantLevel)
			}
			want := tc.wantMessage
			if want == "" {
				want = tc.raw
			}
			if msg != want {
				t.Errorf("message = %q, want %q", msg, want)
			}
		})
	}
}

func TestNormalizeLevel(t *testing.T) {
	cases := map[string]string{
		"error":    logentry.LevelError,
		"ERR":      logentry.LevelError,
		"Fatal":    logentry.LevelError,
		"PANIC":    logentry.LevelError,
		"critical": logentry.LevelError,
		"crit":     logentry.LevelError,
		"warn":     logentry.LevelWarn,
		"WARNING":  logentry.LevelWarn,
		"info":     logentry.LevelInfo,
		"notice":   logentry.LevelInfo,
		"debug":    logentry.LevelDebug,
		"trace":    logentry.LevelDebug,
		"verbose":  logentry.LevelDebug,
		"weird":    logentry.LevelUnknown,
		"":         logentry.LevelUnknown,
	}
	for in, want := range cases {
		if got := normalizeLevel(in); got != want {
			t.Errorf("normalizeLevel(%q) = %q, want %q", in, got, want)
		}
	}
}
