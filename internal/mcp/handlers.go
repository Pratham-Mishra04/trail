package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
	"github.com/Pratham-Mishra04/trail/internal/store"
)

type handlers struct {
	store store.Store
}

// --- response types (the JSON contract surface for the MCP tools) ---

// list_sessions returns []logentry.SessionView (defined in internal/logentry/
// so cli/ and mcp/ share the canonical shape). get_logs uses the local type
// below.

type getLogsResp struct {
	SessionID    string              `json:"session_id"`
	FilePath     string              `json:"file_path"`
	TotalMatched int                 `json:"total_matched"`
	Page         int                 `json:"page"`
	Limit        int                 `json:"limit"`
	Entries      []logentry.LogEntry `json:"entries"`
}

// --- handlers ---

func (h *handlers) listSessions(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args := req.GetArguments()
	activeOnly, _ := args["active_only"].(bool)
	limit := 20
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 100 {
		limit = 100
	}

	infos, err := h.store.List(ctx)
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("list sessions: %v", err)), nil
	}

	out := make([]logentry.SessionView, 0, len(infos))
	for _, info := range infos {
		if activeOnly && !info.IsActive {
			continue
		}
		out = append(out, logentry.NewSessionView(info.Meta, info.IsActive, info.ApproxEndedAt))
		if len(out) >= limit {
			break
		}
	}

	return jsonResult(out)
}

func (h *handlers) getLogs(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	args := req.GetArguments()
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return mcpsdk.NewToolResultError("session_id is required (use list_sessions to discover ids)"), nil
	}

	rawFilters, _ := args["filters"].(map[string]any)
	filter, err := parseFilter(rawFilters)
	if err != nil {
		return mcpsdk.NewToolResultError(err.Error()), nil
	}

	res, err := h.store.Read(ctx, sessionID, filter)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return mcpsdk.NewToolResultError(fmt.Sprintf("session %s not found", sessionID)), nil
		}
		return mcpsdk.NewToolResultError(fmt.Sprintf("read session: %v", err)), nil
	}

	// File path is best-effort: agents can use it to fall back to direct file
	// reads. If Get fails (e.g. session deleted between Read and Get), we
	// still return the entries we already have.
	var filePath string
	if info, err := h.store.Get(ctx, sessionID); err == nil {
		filePath = info.Meta.FilePath
	}

	return jsonResult(getLogsResp{
		SessionID:    sessionID,
		FilePath:     filePath,
		TotalMatched: res.TotalMatched,
		Page:         max(filter.Page, 1),
		Limit:        filter.Limit,
		Entries:      res.Entries,
	})
}

// --- filter parsing + mutual exclusivity ---

var validOrders = map[string]bool{
	store.OrderNewest: true,
	store.OrderOldest: true,
}

func parseFilter(raw map[string]any) (store.Filter, error) {
	f := store.Filter{Limit: 100, Page: 1, Order: store.OrderNewest}
	if raw == nil {
		return f, nil
	}

	if v, ok := raw["limit"].(float64); ok {
		if v <= 0 || math.Trunc(v) != v {
			return f, errors.New("limit must be a positive integer")
		}
		f.Limit = int(v)
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	if v, ok := raw["page"].(float64); ok {
		if v <= 0 || math.Trunc(v) != v {
			return f, errors.New("page must be a positive integer")
		}
		f.Page = int(v)
	}

	if v, ok := raw["query"].(string); ok && v != "" {
		re, err := regexp.Compile("(?i)" + v)
		if err != nil {
			return f, fmt.Errorf("invalid query regex: %w", err)
		}
		f.Query = re
	}

	if v, ok := raw["level"].(string); ok && v != "" {
		level := strings.ToLower(strings.TrimSpace(v))
		if level != "all" {
			if !logentry.IsValidLevel(level) {
				return f, fmt.Errorf("invalid level %q (allowed: error|warn|info|debug|unknown|all)", v)
			}
			f.Level = &level
		}
	}

	hasTimeWindow := false
	hasDuration := false
	hasLineRange := false

	if v, ok := raw["start_time"].(string); ok && v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("invalid start_time (must be RFC3339): %w", err)
		}
		f.StartTime = &t
		hasTimeWindow = true
	}
	if v, ok := raw["end_time"].(string); ok && v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("invalid end_time (must be RFC3339): %w", err)
		}
		f.EndTime = &t
		hasTimeWindow = true
	}
	if v, ok := raw["duration"].(string); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return f, fmt.Errorf("invalid duration (must be Go duration like \"10m\"): %w", err)
		}
		if d < 0 {
			return f, errors.New("duration must be >= 0")
		}
		end := time.Now().UTC()
		start := end.Add(-d)
		f.StartTime = &start
		f.EndTime = &end
		hasDuration = true
	}
	if v, ok := raw["start_line"].(float64); ok {
		if math.Trunc(v) != v {
			return f, errors.New("start_line must be an integer")
		}
		n := int64(v)
		if n < 1 {
			return f, fmt.Errorf("start_line must be >= 1 (lines are 1-indexed), got %d", n)
		}
		f.StartLine = &n
		hasLineRange = true
	}
	if v, ok := raw["end_line"].(float64); ok {
		if math.Trunc(v) != v {
			return f, errors.New("end_line must be an integer")
		}
		n := int64(v)
		if n < 1 {
			return f, fmt.Errorf("end_line must be >= 1 (lines are 1-indexed), got %d", n)
		}
		f.EndLine = &n
		hasLineRange = true
	}

	groups := 0
	if hasTimeWindow {
		groups++
	}
	if hasDuration {
		groups++
	}
	if hasLineRange {
		groups++
	}
	if groups > 1 {
		return f, errors.New("only one of {start_time/end_time, duration, line range} may be set per call")
	}
	if f.StartTime != nil && f.EndTime != nil && f.StartTime.After(*f.EndTime) {
		return f, errors.New("start_time must be <= end_time")
	}
	if f.StartLine != nil && f.EndLine != nil && *f.StartLine > *f.EndLine {
		return f, errors.New("start_line must be <= end_line")
	}

	if v, ok := raw["order"].(string); ok && v != "" {
		if !validOrders[v] {
			return f, fmt.Errorf("invalid order %q (allowed: newest|oldest)", v)
		}
		f.Order = v
	}

	return f, nil
}

// jsonResult marshals v to indented JSON and returns it as text content.
func jsonResult(v any) (*mcpsdk.CallToolResult, error) {
	b, err := sonic.ConfigStd.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcpsdk.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcpsdk.NewToolResultText(string(b)), nil
}
