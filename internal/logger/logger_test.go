package logger

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestEmit_RespectsLevel(t *testing.T) {
	cases := []struct {
		name     string
		level    Level
		emit     func(*Logger)
		wantSeen bool
	}{
		{"error at LevelError", LevelError, func(l *Logger) { l.Error("e") }, true},
		{"warn at LevelError", LevelError, func(l *Logger) { l.Warn("w") }, false},
		{"info at LevelError", LevelError, func(l *Logger) { l.Info("i") }, false},
		{"warn at LevelWarn", LevelWarn, func(l *Logger) { l.Warn("w") }, true},
		{"info at LevelWarn", LevelWarn, func(l *Logger) { l.Info("i") }, false},
		{"info at LevelInfo", LevelInfo, func(l *Logger) { l.Info("i") }, true},
		{"debug at LevelInfo", LevelInfo, func(l *Logger) { l.Debug("d") }, false},
		{"debug at LevelDebug", LevelDebug, func(l *Logger) { l.Debug("d") }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(&buf, tc.level)
			tc.emit(l)
			seen := buf.Len() > 0
			if seen != tc.wantSeen {
				t.Errorf("emit seen=%v, want %v (output=%q)", seen, tc.wantSeen, buf.String())
			}
		})
	}
}

func TestEmit_FormatsAndContainsLabel(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelDebug)

	l.Error("session %s missing", "abc-123")
	l.Warn("skipping %d files", 3)
	l.Info("capturing → %s", "9c5e")
	l.Debug("parsed level=%s", "error")

	out := buf.String()
	for _, want := range []string{
		"[error]", "session abc-123 missing",
		"[warn]", "skipping 3 files",
		"[info]", "capturing → 9c5e",
		"[debug]", "parsed level=error",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestConcurrentEmit_NoInterleaving(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, LevelInfo)

	const goroutines = 16
	const perGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(g int) {
			defer wg.Done()
			for i := range perGoroutine {
				l.Info("g%d-i%d", g, i)
			}
		}(g)
	}
	wg.Wait()

	// Every line should be a complete log entry (timestamp + label + message + newline).
	// If the mutex isn't doing its job, lines interleave and the count is off.
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	want := goroutines * perGoroutine
	if len(lines) != want {
		t.Fatalf("got %d lines, want %d (interleaving suggests mutex isn't working)", len(lines), want)
	}
}

func TestDefault_SwapAndRestore(t *testing.T) {
	original := Default()
	t.Cleanup(func() { SetDefault(original) })

	var buf bytes.Buffer
	custom := New(&buf, LevelInfo)
	SetDefault(custom)

	Default().Info("via default")
	if !strings.Contains(buf.String(), "via default") {
		t.Errorf("SetDefault didn't apply: %q", buf.String())
	}
}
