// Package mcp implements trail's stdio MCP server. The server is read-only:
// it never starts, modifies, or stops captures. It enumerates session files
// on disk via store.Store and serves two tools, list_sessions and get_logs.
//
// IMPORTANT: stdout is the JSON-RPC transport. Diagnostics MUST go to stderr
// (or be silenced) — printing to stdout corrupts the protocol stream.
package mcp

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/Pratham-Mishra04/trail/internal/store"
)

// New constructs an MCP server with the two tools registered. version is
// reported in the server's serverInfo response.
func New(version string, s store.Store) *server.MCPServer {
	srv := server.NewMCPServer(
		"trail",
		version,
		server.WithToolCapabilities(true),
	)

	h := &handlers{store: s}
	srv.AddTool(listSessionsTool(), h.listSessions)
	srv.AddTool(getLogsTool(), h.getLogs)

	return srv
}

// Serve constructs the server and runs it on stdio. Blocks until stdin
// closes (i.e. the editor disconnects).
func Serve(version string, s store.Store) error {
	return server.ServeStdio(New(version, s))
}
