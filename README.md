# trail

Capture stdout/stderr from any process into per-session JSONL files on disk, and expose those logs to AI coding agents over a stdio MCP server. Single static binary. No daemon, no database, no cloud.

```text
your-app                trail run / docker
                                │
                                ▼
                  ~/.config/trail/sessions/<uuid>.jsonl
                                ▲
                  trail mcp (stdio, spawned by your editor)
                                ▲
                                │
            Claude Code · Cursor · Windsurf · Claude Desktop
                       (any MCP-capable agent)
```

Run your app under trail, ask your AI agent what went wrong.

## What it actually does

- **`trail run -- <cmd>`** — wraps any command, captures both stdout and stderr to a session file, forwards signals, mirrors the child's exit code. Works for `npm run dev`, `python -m http.server`, `cargo run`, anything.
- **`trail docker <container>`** — same, but for an already-running Docker container (wraps `docker logs -f`).
- **`trail mcp`** — runs a stdio MCP server (`list_sessions`, `get_logs`) so any MCP-capable agent can query the captured logs without having to read raw files into its context.
- **`trail logs --session <id>`** — same query surface as the MCP tools, but in the terminal for humans.
- **`trail sessions`** — list / inspect / delete captured sessions.

Captured logs are plain JSONL on disk. Any tool that can read a text file can read them — `cat`, `jq`, `grep`, your editor, your scripting language. The MCP server is just one way to query them.

## Install

### From source (recommended for now)

```bash
git clone https://github.com/Pratham-Mishra04/trail
cd trail
make install
```

This runs `go install` with version metadata baked in. The binary lands at `$(go env GOPATH)/bin/trail` — make sure that's on your `$PATH`.

### Via go install

```bash
go install github.com/Pratham-Mishra04/trail@latest
```

### Verify

```bash
trail version
# trail dev (commit ..., built ...)
```

**Supported platforms:** macOS and Linux on amd64/arm64. Windows is not supported.

## Quickstart

```bash
# 1. Wrap your app under trail.
trail run -- node server.js

# trail prints the session id + file path to stderr at startup:
#   18:04:22  [info]   capturing → 9c5e1b7e-... (file: /Users/.../sessions/9c5e1b7e-....jsonl)
#
# Your app runs normally; trail forwards stdout/stderr through to your terminal
# AND captures every line to the session file.
```

In another terminal (or in your AI editor):

```bash
# 2a. Query from the CLI:
trail sessions
trail logs --session 9c5e1b7e-... --level error
trail logs --session 9c5e1b7e-... --query "ECONNREFUSED"
```

```text
# 2b. Or just ask your AI agent:
"List my trail sessions, then show me the errors from the most recent one."
```

The agent calls `list_sessions` and `get_logs` over MCP and gets back structured results.

## Agentic debug sessions

The Claude Code plugin ships with a debugging skill (`debug-with-trail`) that turns your editor into a full debug-mode-style workflow — comparable to dedicated debug features in other AI editors, but built on the open MCP layer so it works with any agent that loads the skill.

You ask in plain English:

> *"my Express server is throwing 500s on POST /orders, debug it"*

Claude (via the skill) walks through a four-phase workflow without further prompting:

**Phase 1 — Prerequisites.** Verifies `trail` is installed and that the right process is being captured. If your app isn't currently wrapped under trail, Claude tells you to restart it with `trail run -- <your command>` and waits.

**Phase 2 — Read existing logs first.** Calls `get_logs(level="error")` and `get_logs(query="orders")` on the active session. If the bug is already visible in the captured output, Claude diagnoses from there — no instrumentation, no restart, no waiting.

**Phase 3 — Targeted instrumentation when existing logs aren't enough.** Picks a unique marker (e.g. `TRAIL-DEBUG-7K3F`) and adds temporary log statements at suspect points: function entry, before/after risky calls, inside conditional branches, around error returns. Asks you to restart and reproduce. Captures the new run, queries with the marker, iterates until it has enough information.

**Phase 4 — Mandatory cleanup.** This is non-optional. Claude greps for the marker, removes every line that carries it, verifies a second grep returns zero results, and reports to you what was instrumented, what was found, and confirmation that nothing was left behind.

The whole flow is opinionated for safety: every added log line has a unique session-scoped marker so cleanup is surgical, the marker rule is "fresh ID per debug session" so parallel debugging efforts don't interfere, and the `grep == 0` verification is a hard gate before Claude considers the task done. Read the full skill at [`integrations/claude-code/skills/debug-with-trail/SKILL.md`](integrations/claude-code/skills/debug-with-trail/SKILL.md) — it's the exact instructions Claude follows.

## Editor setup

### Claude Code (recommended: plugin)

The plugin auto-wires the MCP server and registers the debug skill described above:

```text
/plugin marketplace add Pratham-Mishra04/trail
/plugin install trail@pratham
/reload-plugins
```

Verify by asking *"What skills are available?"* — `pratham:trail:debug-with-trail` should appear.

### Claude Code (manual, no plugin)

```bash
claude mcp add trail -s user -- trail mcp
```

You get the MCP tools without the bundled debug skill — Claude figures out how to use the tools from their descriptions.

### Cursor

`.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "trail": {
      "command": "trail",
      "args": ["mcp"]
    }
  }
}
```

### Windsurf

Settings → MCP, same shape:

```json
{
  "mcpServers": {
    "trail": {
      "command": "trail",
      "args": ["mcp"]
    }
  }
}
```

### Claude Desktop

`claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "trail": {
      "command": "trail",
      "args": ["mcp"]
    }
  }
}
```

In every case `trail` must be on the `$PATH` of the shell that launches the editor. macOS app launches don't always inherit your shell `$PATH` — if the MCP server fails to start, that's usually why; `which trail` from the editor's terminal is the fastest check.

## Why server-side queries

When an agent reads raw log files into its own context to filter them, every query pays for re-loading the file's tokens through the LLM. Trail's MCP tools run the filter (level, regex, time window, line range, pagination) in the Go process and return only the matching entries. The agent sees a structured result, not a wall of text.

This means:
- The agent doesn't burn its context window on log lines that won't end up mattering.
- Filters are deterministic — a regex returns exactly what matches, not "what the model decided was interesting."
- Latency stays bounded by the filter (typically sub-100ms) rather than by token-generation speed.

## What it captures

| Source | Command | Notes |
|---|---|---|
| Wrapped command | `trail run -- <cmd>` | Wraps any binary; `exec.Cmd` with separate stdout/stderr pipes preserves stream attribution. |
| Docker container | `trail docker <name>` | Wraps `docker logs -f`; passes `--since` through. |

What it does **not** capture:
- A bare PID you didn't start under trail (would require `ptrace` / `eBPF` — out of scope).
- Arbitrary log files written to disk by some other tool (use `tail`, `lnav`, etc).

## Querying captured logs

### From an AI agent (via MCP)

The MCP server exposes two tools:

- **`list_sessions(active_only?, limit?)`** — returns session metadata including the absolute file path of each session's JSONL file, ordered active-first.
- **`get_logs(session_id, filters?)`** — returns matching entries. Filters: `limit`, `page`, `query` (case-insensitive regex), `level` (`error|warn|info|debug|unknown|all`), `start_time`/`end_time` (RFC3339), `duration` (Go-style: `"10m"`, `"2h"`), `start_line`/`end_line`, `order` (`newest` default, or `oldest`). Time-window, duration, and line-range filters are mutually exclusive — combining them returns an error.

If the result looks incomplete, the response includes the absolute `file_path` and the agent can read the JSONL file directly with its file tools.

### From the terminal

```bash
trail sessions                                       # table
trail sessions --json                                # JSON for scripting
trail sessions rm <id>                               # delete one
trail sessions rm --all                              # delete all

trail logs --session <id>                            # last 100, pretty
trail logs --session <id> -n 50                      # last 50
trail logs --session <id> --level error              # errors only
trail logs --session <id> --query "ECONNREFUSED"     # case-insensitive regex
trail logs --session <id> --format json              # raw JSON entries
```

`trail logs --session` is required (no implicit "most recent" default — the same rule the MCP tool enforces, on purpose).

## How it works

- **Two-process model.** Capture processes (`trail run` / `trail docker`) own session files. The MCP server is read-only and spawned by the editor on demand; it never starts, modifies, or stops captures. They share only the filesystem.
- **One JSONL file per session** at `~/.config/trail/sessions/<uuid>.jsonl`. First line is a meta header (session id, name, source, capturer pid, started_at, file path). Every subsequent line is one captured entry.
- **Append-only writes** under `PIPE_BUF` so kernel writes are atomic; no locking. Concurrent reads work without coordination.
- **Server-side filtering** uses `gjson` for cheap pre-filter peeks (level, time, line) and `sonic` for full decode only on entries that pass. "Newest N" queries use a reverse scan: seek to file end, walk backwards in chunks, stop after N matches — constant time regardless of file size.
- **Conservative log-level detection.** Only sets a non-`unknown` level when there's clear evidence: a JSON `level` field, a logfmt `level=` pair, or an anchored line-prefix pattern (`ERROR:`, `INFO[...]`, `WARN ...`). Free-text matches like substring `"info"` are deliberately not supported to avoid false positives.

For full design context, see `private/PRD.md` in the repo.

## Status

- v0.1.0 — early but feature-complete for the documented scope.
- 110+ tests, including a real subprocess e2e test that pushes 100K log entries to the MCP server under concurrent write load.
- Tested on Apple M3 Pro (macOS) and Linux x86_64.

Sample query latencies on the e2e test (100K-entry session):
- `list_sessions`: ~1ms
- `get_logs(level=error, limit=100)`: ~1ms (newest reverse-scan)
- `get_logs(query="cache", limit=100)`: ~1–2ms
- `get_logs(start_line=N, end_line=N+100)`: ~10ms

Run `make test-perf` and `make test-e2e` to reproduce on your hardware.

## License

MIT

