package store

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
)

// populateSession writes n entries to a fresh session and returns its ID. Used
// to stage large files for benchmarks without polluting the actual code paths
// that produce the entries.
func populateSession(tb testing.TB, s *jsonlStore, n int) string {
	tb.Helper()
	sess, err := s.Create(newMeta("perf", os.Getpid()))
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	levels := []string{
		logentry.LevelInfo, logentry.LevelWarn, logentry.LevelError,
		logentry.LevelDebug, logentry.LevelUnknown,
	}
	streams := []string{logentry.StreamStdout, logentry.StreamStderr}
	keywords := []string{"db", "auth", "cache", "queue", "http"}
	base := time.Now().UTC()

	for i := range n {
		raw := fmt.Sprintf(
			"line %d: %s op id=%d status=%s duration=%dms",
			i, keywords[i%len(keywords)], i,
			[]string{"ok", "fail", "retry"}[i%3],
			i%500,
		)
		if err := sess.Append(logentry.LogEntry{
			Stream:    streams[i%2],
			Level:     levels[i%5],
			Raw:       raw,
			Message:   raw,
			Timestamp: base.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			tb.Fatal(err)
		}
	}
	return sess.SessionID()
}

// --- benchmarks: cold reads against a static file at scale ---

func benchmarkReadAtSize(b *testing.B, n int, f Filter) {
	b.Helper()
	s := &jsonlStore{dir: b.TempDir()}
	id := populateSession(b, s, n)
	ctx := context.Background()

	// Report the file size so we can see what we're scanning.
	if fi, err := os.Stat(s.pathFor(id)); err == nil {
		b.ReportMetric(float64(fi.Size())/1024, "KB-on-disk")
		b.ReportMetric(float64(n), "entries-in-file")
	}

	b.ResetTimer()
	for range b.N {
		if _, err := s.Read(ctx, id, f); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRead_10K_NoFilter(b *testing.B) {
	benchmarkReadAtSize(b, 10_000, Filter{Limit: 100})
}

func BenchmarkRead_10K_LevelOnly(b *testing.B) {
	lv := logentry.LevelError
	benchmarkReadAtSize(b, 10_000, Filter{Limit: 100, Level: &lv})
}

func BenchmarkRead_10K_RegexQuery(b *testing.B) {
	benchmarkReadAtSize(b, 10_000, Filter{Limit: 100, Query: regexp.MustCompile(`(?i)cache`)})
}

func BenchmarkRead_10K_LineRange(b *testing.B) {
	start, end := int64(5000), int64(5100)
	benchmarkReadAtSize(b, 10_000, Filter{Limit: 100, StartLine: &start, EndLine: &end})
}

func BenchmarkRead_10K_TimeWindow(b *testing.B) {
	now := time.Now().UTC()
	start := now.Add(-10 * time.Second)
	end := now
	benchmarkReadAtSize(b, 10_000, Filter{Limit: 100, StartTime: &start, EndTime: &end})
}

func BenchmarkRead_10K_CombinedHeavy(b *testing.B) {
	lv := logentry.LevelError
	start, end := int64(1000), int64(9000)
	benchmarkReadAtSize(b, 10_000, Filter{
		Limit:     50,
		Level:     &lv,
		Query:     regexp.MustCompile(`(?i)(cache|db)`),
		StartLine: &start,
		EndLine:   &end,
	})
}

func BenchmarkRead_100K_NoFilter(b *testing.B) {
	benchmarkReadAtSize(b, 100_000, Filter{Limit: 100})
}

func BenchmarkRead_100K_RegexQuery(b *testing.B) {
	benchmarkReadAtSize(b, 100_000, Filter{Limit: 100, Query: regexp.MustCompile(`(?i)cache`)})
}

// --- benchmarks targeting the three Tier-1 optimizations ---

// timeWindowEarly puts the time filter on a slice that ends well before the
// end of the file. With monotonic-time early-exit, the scan should stop at the
// first out-of-window entry (~1% of the file) instead of reading all 10K lines.
func BenchmarkRead_100K_TimeWindowEarly(b *testing.B) {
	s := &jsonlStore{dir: b.TempDir()}
	id := populateSession(b, s, 100_000)

	// populateSession spaces entries 1ms apart starting at "now"-ish. Ask for
	// only the first ~1000 entries by timestamp.
	info, _ := s.Get(context.Background(), id)
	start := info.Meta.StartedAt
	end := start.Add(1 * time.Second) // ~1000 entries
	f := Filter{Limit: 100, StartTime: &start, EndTime: &end}

	b.ResetTimer()
	for range b.N {
		if _, err := s.Read(context.Background(), id, f); err != nil {
			b.Fatal(err)
		}
	}
}

// 100K-level-only is the big payoff for the gjson pre-filter — most entries
// fail the level check; without pre-filter we still pay sonic.Unmarshal cost
// for every line.
func BenchmarkRead_100K_LevelOnly(b *testing.B) {
	lv := logentry.LevelError
	benchmarkReadAtSize(b, 100_000, Filter{Limit: 100, Level: &lv})
}

// "Newest N" is the canonical query shape for `get_logs` with no other
// filters — the case reverse-scan turns from O(file) into O(N).
func BenchmarkRead_100K_NewestN(b *testing.B) {
	benchmarkReadAtSize(b, 100_000, Filter{Limit: 100, Order: OrderNewest})
}

func BenchmarkRead_1M_NewestN(b *testing.B) {
	benchmarkReadAtSize(b, 1_000_000, Filter{Limit: 100, Order: OrderNewest})
}

// --- assertion-based perf budget ---

// TestRead_PerformanceBudget guards against a regression: a Read with filters
// against a 10K-entry session should complete in well under a second on any
// realistic developer machine. If this fails, something is fundamentally
// wrong with the read path (e.g. quadratic decode, missed early-exit).
func TestRead_PerformanceBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test")
	}
	s := &jsonlStore{dir: t.TempDir()}
	id := populateSession(t, s, 10_000)

	lv := logentry.LevelError
	filter := Filter{
		Limit: 100,
		Level: &lv,
		Query: regexp.MustCompile(`(?i)cache`),
	}

	const budget = 250 * time.Millisecond
	const samples = 5
	var slowest time.Duration
	for range samples {
		start := time.Now()
		if _, err := s.Read(context.Background(), id, filter); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > slowest {
			slowest = d
		}
	}
	t.Logf("Read on 10K entries with level+regex filter: slowest of %d samples = %v (budget %v)",
		samples, slowest, budget)
	if slowest > budget {
		t.Errorf("Read exceeded budget: slowest=%v, budget=%v", slowest, budget)
	}
}

// --- concurrent read-while-writing ---

// TestRead_ConcurrentWithWriter mirrors the live-debug scenario: a capturer
// is appending entries continuously (~10/sec, like a chatty service or build
// loop), and the agent calls get_logs against the same session. We assert
// that (a) reads return without error, (b) latency stays bounded, and (c)
// the writer keeps making progress (no reader-side lock contention starves
// the append path).
func TestRead_ConcurrentWithWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test")
	}
	s := &jsonlStore{dir: t.TempDir()}

	// Pre-populate so reads have something substantial to scan from the start.
	id := populateSession(t, s, 10_000)

	// Re-open the same session for append by directly constructing a
	// jsonlSession bound to the existing file (white-box: this package can
	// touch internals). The capturer in production is the same goroutine
	// that called Create, so we don't need a public API for this.
	path := s.pathFor(id)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	writerSess := &jsonlSession{
		file:     f,
		meta:     logentry.MetaHeader{SessionID: id, FilePath: path},
		nextLine: 10_001, // continue line counter
	}
	defer func() { _ = writerSess.Close() }()

	// Background writer: ~10 entries/sec for the duration of the test.
	const writeInterval = 100 * time.Millisecond
	var written int64
	stopWriter := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(writeInterval)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-stopWriter:
				return
			case <-ticker.C:
				err := writerSess.Append(logentry.LogEntry{
					Stream:    logentry.StreamStdout,
					Level:     logentry.LevelInfo,
					Raw:       fmt.Sprintf("live-write %d cache lookup", i),
					Message:   fmt.Sprintf("live-write %d cache lookup", i),
					Timestamp: time.Now().UTC(),
				})
				if err != nil {
					t.Errorf("writer append: %v", err)
					return
				}
				atomic.AddInt64(&written, 1)
				i++
			}
		}
	}()

	// Reader loop: run queries for a few seconds, measure latency.
	const testDuration = 3 * time.Second
	const perReadBudget = 500 * time.Millisecond
	deadline := time.Now().Add(testDuration)

	lv := logentry.LevelError
	queryRE := regexp.MustCompile(`(?i)cache`)
	filters := []Filter{
		{Limit: 100, Level: &lv},
		{Limit: 100, Query: queryRE},
		{Limit: 50, Level: &lv, Query: queryRE},
		{Limit: 200, Order: OrderOldest},
	}

	var (
		readCount   int
		totalLat    time.Duration
		maxLat      time.Duration
		filterCycle int
	)
	for time.Now().Before(deadline) {
		f := filters[filterCycle%len(filters)]
		filterCycle++
		start := time.Now()
		res, err := s.Read(context.Background(), id, f)
		lat := time.Since(start)
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		if len(res.Entries) == 0 && f.Level == nil && f.Query == nil {
			t.Errorf("Read returned no entries with no filter")
		}
		readCount++
		totalLat += lat
		if lat > maxLat {
			maxLat = lat
		}
		if lat > perReadBudget {
			t.Errorf("Read latency exceeded budget: %v > %v", lat, perReadBudget)
		}
	}

	close(stopWriter)
	<-writerDone

	w := atomic.LoadInt64(&written)
	avgLat := totalLat / time.Duration(readCount)
	t.Logf(
		"over %v: reads=%d (avg %v, max %v), writes=%d (~%.1f/sec)",
		testDuration, readCount, avgLat, maxLat, w, float64(w)/testDuration.Seconds(),
	)
	if w < 20 { // expect at least 20 writes in 3s at 10/sec — gives slack
		t.Errorf("writer made too few writes (%d) — reader may be starving the append path", w)
	}
	if readCount < 5 {
		t.Errorf("reader made too few queries (%d) — Read is too slow", readCount)
	}
}
