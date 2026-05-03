package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

// --- parseFilter ---

func TestParseFilter_Defaults(t *testing.T) {
	f, err := parseFilter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.Limit != 100 || f.Page != 1 || f.Order != store.OrderNewest {
		t.Errorf("defaults wrong: %+v", f)
	}
	if f.Level != nil || f.Query != nil || f.StartTime != nil || f.EndTime != nil ||
		f.StartLine != nil || f.EndLine != nil {
		t.Errorf("expected all optional fields nil: %+v", f)
	}
}

func TestParseFilter_Clamps(t *testing.T) {
	f, err := parseFilter(map[string]any{"limit": float64(5000)})
	if err != nil {
		t.Fatal(err)
	}
	if f.Limit != 1000 {
		t.Errorf("limit not clamped: %d", f.Limit)
	}
}

func TestParseFilter_RejectsNonPositiveOrFractional(t *testing.T) {
	cases := []map[string]any{
		{"limit": float64(-3)},
		{"limit": float64(0)},
		{"limit": 1.5},
		{"page": float64(-3)},
		{"page": float64(0)},
		{"page": 2.5},
		{"start_line": 1.5},
		{"end_line": 1.5},
	}
	for _, in := range cases {
		if _, err := parseFilter(in); err == nil {
			t.Errorf("parseFilter(%v) returned no error", in)
		}
	}
}

func TestParseFilter_LevelEnum(t *testing.T) {
	tests := []struct {
		in      string
		wantPtr bool
		wantErr bool
	}{
		{"", false, false},    // unset → nil
		{"all", false, false}, // explicit "all" → nil
		{"error", true, false},
		{"warn", true, false},
		{"info", true, false},
		{"debug", true, false},
		{"unknown", true, false},
		{"weird", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			f, err := parseFilter(map[string]any{"level": tc.in})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (f.Level != nil) != tc.wantPtr {
				t.Errorf("level pointer presence = %v, want %v", f.Level != nil, tc.wantPtr)
			}
		})
	}
}

func TestParseFilter_Query(t *testing.T) {
	f, err := parseFilter(map[string]any{"query": "ECONNREFUSED"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Query == nil || !f.Query.MatchString("econnrefused") { // (?i) prefix
		t.Errorf("query not compiled or not case-insensitive: %v", f.Query)
	}

	if _, err := parseFilter(map[string]any{"query": "[unclosed"}); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestParseFilter_TimeWindow(t *testing.T) {
	f, err := parseFilter(map[string]any{
		"start_time": "2026-05-03T10:00:00Z",
		"end_time":   "2026-05-03T11:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.StartTime == nil || f.EndTime == nil {
		t.Fatalf("times not set: %+v", f)
	}
	if f.StartTime.Hour() != 10 || f.EndTime.Hour() != 11 {
		t.Errorf("times wrong: start=%v end=%v", f.StartTime, f.EndTime)
	}

	if _, err := parseFilter(map[string]any{"start_time": "not-a-time"}); err == nil {
		t.Fatal("expected error for invalid start_time")
	}
	if _, err := parseFilter(map[string]any{"end_time": "not-a-time"}); err == nil {
		t.Fatal("expected error for invalid end_time")
	}
}

func TestParseFilter_Duration(t *testing.T) {
	before := time.Now()
	f, err := parseFilter(map[string]any{"duration": "30m"})
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()

	if f.StartTime == nil || f.EndTime == nil {
		t.Fatalf("duration didn't set both times: %+v", f)
	}
	gap := f.EndTime.Sub(*f.StartTime)
	if gap != 30*time.Minute {
		t.Errorf("window width = %v, want 30m", gap)
	}
	if f.EndTime.Before(before) || f.EndTime.After(after.Add(time.Second)) {
		t.Errorf("EndTime not anchored to now: %v (before=%v after=%v)", f.EndTime, before, after)
	}

	if _, err := parseFilter(map[string]any{"duration": "not-a-duration"}); err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestParseFilter_LineRange(t *testing.T) {
	f, err := parseFilter(map[string]any{
		"start_line": float64(10),
		"end_line":   float64(50),
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.StartLine == nil || *f.StartLine != 10 {
		t.Errorf("start_line wrong: %v", f.StartLine)
	}
	if f.EndLine == nil || *f.EndLine != 50 {
		t.Errorf("end_line wrong: %v", f.EndLine)
	}
}

func TestParseFilter_LineRangeRejectsZeroAndNegative(t *testing.T) {
	for _, tc := range []struct {
		field string
		val   float64
	}{
		{"start_line", 0},
		{"start_line", -1},
		{"end_line", 0},
		{"end_line", -5},
	} {
		_, err := parseFilter(map[string]any{tc.field: tc.val})
		if err == nil {
			t.Errorf("parseFilter(%s=%v) returned no error; expected rejection", tc.field, tc.val)
		}
	}
}

func TestParseFilter_LevelCaseInsensitive(t *testing.T) {
	for _, in := range []string{"ERROR", "Warn", "INFO", " debug "} {
		f, err := parseFilter(map[string]any{"level": in})
		if err != nil {
			t.Errorf("parseFilter(level=%q) returned error: %v", in, err)
			continue
		}
		if f.Level == nil {
			t.Errorf("parseFilter(level=%q) didn't set Level", in)
		}
	}
}

func TestParseFilter_MutualExclusivity(t *testing.T) {
	combos := []map[string]any{
		{"start_time": "2026-05-03T10:00:00Z", "duration": "10m"},
		{"start_time": "2026-05-03T10:00:00Z", "start_line": float64(1)},
		{"end_time": "2026-05-03T10:00:00Z", "end_line": float64(5)},
		{"duration": "10m", "start_line": float64(1)},
		{"duration": "10m", "end_line": float64(5)},
		{"start_time": "2026-05-03T10:00:00Z", "duration": "10m", "start_line": float64(1)},
	}
	for i, combo := range combos {
		_, err := parseFilter(combo)
		if err == nil {
			t.Errorf("combo %d: expected mutual-exclusivity error, got nil (%v)", i, combo)
			continue
		}
		if !strings.Contains(err.Error(), "only one of") {
			t.Errorf("combo %d: error %q doesn't mention mutual exclusivity", i, err.Error())
		}
	}
}

func TestParseFilter_OrderEnum(t *testing.T) {
	for _, v := range []string{"newest", "oldest"} {
		f, err := parseFilter(map[string]any{"order": v})
		if err != nil {
			t.Errorf("order=%q rejected: %v", v, err)
		} else if f.Order != v {
			t.Errorf("order = %q, want %q", f.Order, v)
		}
	}
	if _, err := parseFilter(map[string]any{"order": "random"}); err == nil {
		t.Fatal("expected error for invalid order")
	}
}

// --- handlers integration via fakeStore ---

type mcpFakeStore struct {
	infos    []store.SessionInfo
	readResp store.ReadResult
	readErr  error
	getErr   error
}

func (s *mcpFakeStore) Create(logentry.MetaHeader) (store.Session, error) { return nil, nil }
func (s *mcpFakeStore) List(context.Context) ([]store.SessionInfo, error) { return s.infos, nil }
func (s *mcpFakeStore) Get(_ context.Context, id string) (store.SessionInfo, error) {
	if s.getErr != nil {
		return store.SessionInfo{}, s.getErr
	}
	for _, i := range s.infos {
		if i.Meta.SessionID == id {
			return i, nil
		}
	}
	return store.SessionInfo{}, os.ErrNotExist
}
func (s *mcpFakeStore) Delete(context.Context, string) error   { return nil }
func (s *mcpFakeStore) DeleteAll(context.Context) (int, error) { return 0, nil }
func (s *mcpFakeStore) Read(context.Context, string, store.Filter) (store.ReadResult, error) {
	return s.readResp, s.readErr
}

func makeInfo(id, name string, active bool) store.SessionInfo {
	return store.SessionInfo{
		Meta: logentry.MetaHeader{
			SessionID: id, Name: name, Source: logentry.SourceRun,
			StartedAt: time.Now().UTC(), FilePath: "/fake/" + id + ".jsonl",
		},
		IsActive: active,
	}
}

func decodeContent(t *testing.T, res *mcpsdk.CallToolResult) (any, bool) {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}
	tc, ok := res.Content[0].(mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want TextContent", res.Content[0])
	}
	if res.IsError {
		return tc.Text, true
	}
	var out any
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("decode result text: %v\nraw: %s", err, tc.Text)
	}
	return out, false
}

func TestListSessions_Empty(t *testing.T) {
	h := &handlers{store: &mcpFakeStore{}}
	res, _ := h.listSessions(context.Background(), mcpsdk.CallToolRequest{})
	out, isErr := decodeContent(t, res)
	if isErr {
		t.Fatalf("unexpected error: %v", out)
	}
	arr, ok := out.([]any)
	if !ok || len(arr) != 0 {
		t.Errorf("expected empty array, got %v", out)
	}
}

func TestListSessions_FiltersAndLimits(t *testing.T) {
	infos := []store.SessionInfo{
		makeInfo("a", "alpha", true),
		makeInfo("b", "beta", false),
		makeInfo("c", "gamma", true),
	}
	h := &handlers{store: &mcpFakeStore{infos: infos}}

	t.Run("active_only", func(t *testing.T) {
		req := mcpsdk.CallToolRequest{}
		req.Params.Arguments = map[string]any{"active_only": true}
		res, _ := h.listSessions(context.Background(), req)
		out, isErr := decodeContent(t, res)
		if isErr {
			t.Fatalf("error: %v", out)
		}
		if arr, ok := out.([]any); !ok || len(arr) != 2 {
			t.Errorf("got %d sessions, want 2", len(arr))
		}
	})

	t.Run("limit", func(t *testing.T) {
		req := mcpsdk.CallToolRequest{}
		req.Params.Arguments = map[string]any{"limit": float64(2)}
		res, _ := h.listSessions(context.Background(), req)
		out, _ := decodeContent(t, res)
		if arr, ok := out.([]any); !ok || len(arr) != 2 {
			t.Errorf("got %d sessions, want 2 (limited)", len(arr))
		}
	})
}

func TestGetLogs_MissingSessionID(t *testing.T) {
	h := &handlers{store: &mcpFakeStore{}}
	res, _ := h.getLogs(context.Background(), mcpsdk.CallToolRequest{})
	if !res.IsError {
		t.Fatal("expected isError=true")
	}
	tc, ok := res.Content[0].(mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want TextContent", res.Content[0])
	}
	if !strings.Contains(tc.Text, "session_id is required") {
		t.Errorf("error message wrong: %q", tc.Text)
	}
}

func TestGetLogs_SessionNotFound(t *testing.T) {
	h := &handlers{store: &mcpFakeStore{readErr: os.ErrNotExist}}
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{"session_id": "missing"}
	res, _ := h.getLogs(context.Background(), req)
	if !res.IsError {
		t.Fatal("expected isError=true")
	}
	tc, ok := res.Content[0].(mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want TextContent", res.Content[0])
	}
	if !strings.Contains(tc.Text, "missing") {
		t.Errorf("error doesn't mention session id: %q", tc.Text)
	}
}

func TestGetLogs_HappyPath(t *testing.T) {
	id := "abc"
	infos := []store.SessionInfo{makeInfo(id, "test", true)}
	entries := []logentry.LogEntry{
		{Line: 1, Stream: "stdout", Level: "info", Raw: "hi", Message: "hi", Timestamp: time.Now().UTC()},
		{Line: 2, Stream: "stderr", Level: "error", Raw: "boom", Message: "boom", Timestamp: time.Now().UTC()},
	}
	h := &handlers{store: &mcpFakeStore{
		infos:    infos,
		readResp: store.ReadResult{Entries: entries, TotalMatched: 2},
	}}

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{"session_id": id}
	res, _ := h.getLogs(context.Background(), req)
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content[0])
	}
	out, _ := decodeContent(t, res)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("result not an object: %T", out)
	}
	if m["session_id"] != id {
		t.Errorf("session_id = %v", m["session_id"])
	}
	if v, ok := m["total_matched"].(float64); !ok || v != 2 {
		t.Errorf("total_matched = %v", m["total_matched"])
	}
	if v, ok := m["file_path"].(string); !ok || !strings.HasSuffix(v, id+".jsonl") {
		t.Errorf("file_path = %v", m["file_path"])
	}
	if e, ok := m["entries"].([]any); !ok || len(e) != 2 {
		t.Errorf("entries = %v", m["entries"])
	}
}

func TestGetLogs_FilterErrorSurfacedToCaller(t *testing.T) {
	h := &handlers{store: &mcpFakeStore{}}
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"session_id": "x",
		"filters":    map[string]any{"start_line": float64(1), "duration": "10m"},
	}
	res, _ := h.getLogs(context.Background(), req)
	if !res.IsError {
		t.Fatal("expected isError")
	}
	tc, ok := res.Content[0].(mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want TextContent", res.Content[0])
	}
	if !strings.Contains(tc.Text, "only one of") {
		t.Errorf("error didn't surface mutual exclusivity: %q", tc.Text)
	}
}
