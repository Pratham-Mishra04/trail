---
name: debug-with-trail
description: This skill should be used when the user asks to "debug this", "why is this failing", "what's in the logs", "why did it crash", "investigate this error", "trace the bug", "what's happening when I do X", or pastes an error message and asks for help diagnosing it. Walks through prerequisites check, optional targeted instrumentation, query via MCP, and mandatory cleanup of any added logs using captured stdout/stderr from trail.
version: 0.1.0
---

# Debug with trail

Use this workflow when investigating a runtime issue with the help of `trail` — a local CLI that captures process stdout/stderr into per-session JSONL files and exposes them via the MCP tools `list_sessions` and `get_logs`.

The workflow has four phases. Steps 1, 2, and 4 are **mandatory**. Step 3 is conditional — only do it when the existing logs don't already explain the issue.

---

## Phase 1 — Prerequisites check (always do this first)

Before any debugging work, verify two things in order:

### 1.1 Is the `trail` binary installed?

Run:

```bash
trail version
```

- **If the command runs and prints a version line** (e.g. `trail 0.1.0 (commit abc123, built ...)`) → continue to 1.2.
- **If you get `command not found`** → tell the user:

  > trail isn't installed yet. Install it first, then I can debug this for you:
  >
  > ```bash
  > curl -fsSL https://raw.githubusercontent.com/Pratham-Mishra04/trail/main/install.sh | sh
  > # or:  go install github.com/Pratham-Mishra04/trail@latest
  > ```
  >
  > More options at https://github.com/Pratham-Mishra04/trail.

  **Then stop. Do not attempt to debug without trail — you'd be guessing.** Wait for the user to confirm install, then re-run `trail version`.

### 1.2 Is the relevant process being captured by trail?

Call the `list_sessions` MCP tool with `active_only: true` to see what's currently being captured:

```json
list_sessions({"active_only": true})
```

Inspect the returned sessions. For each, look at:
- `name` — the human label (often the binary name + args)
- `command` — the full argv that was launched
- `started_at` — recency

**Decide:**

- **If exactly one active session matches what the user is debugging** (by name, command, or context — e.g. user is debugging an Express server and you see a session with command `node server.js`) → record its `session_id` and `file_path`, continue to Phase 2.

- **If multiple active sessions look plausible** → ask the user which one. Don't guess.

- **If no active session matches** (or no sessions at all) → tell the user to restart their process under trail and reproduce the issue. Be specific:

  > I don't see your process being captured. Restart it under trail so I can see its output:
  >
  > ```bash
  > # Stop your current process (Ctrl+C), then run:
  > trail run -- <the command you normally run>
  >
  > # For example, instead of `npm run dev`, run:
  > trail run -- npm run dev
  > ```
  >
  > Then reproduce the issue once and tell me when you're ready.

  **Stop and wait.** Do not proceed to Phase 2 without an active session.

  **Tip — when the user starts trail, it prints the session id to stderr:**
  ```
  19:24:42  [info]   capturing → 39f6875e-3417-4146-867b-c430971b7489 (file: /Users/.../39f6875e-...jsonl)
  ```
  If the user pastes that line back to you, use the printed UUID directly — no need to call `list_sessions` again.

---

## Phase 2 — Read what's already captured

Try the cheapest queries first. The existing logs may already explain the issue with no instrumentation needed.

Use the `get_logs` MCP tool. Order of tries:

```json
// 1. All errors so far — if the issue surfaced as an error, this finds it.
get_logs({"session_id": "<session_id>", "filters": {"level": "error"}})

// 2. Recent activity (last 5 minutes) — useful right after the user reproduces.
get_logs({"session_id": "<session_id>", "filters": {"duration": "5m"}})

// 3. Targeted regex if you have a keyword from the user's report.
get_logs({"session_id": "<session_id>", "filters": {"query": "ECONNREFUSED|timeout|panic"}})

// 4. A specific window if the user said when it happened.
get_logs({"session_id": "<session_id>", "filters": {"start_time": "2026-05-03T14:20:00Z", "end_time": "2026-05-03T14:25:00Z"}})
```

**Important constraints on the filter:**
- Only **one** of `{start_time/end_time}`, `{duration}`, `{start_line/end_line}` per call. They're mutually exclusive — combining them returns an error.
- `query` is a regex matched against the message field, case-insensitive.
- If results look truncated or you need surrounding context not captured by your filter → **read the file directly** at the `file_path` returned in the response (it's a plain JSONL file, one entry per line).

**If you can diagnose the issue from existing logs → skip directly to Phase 4** (cleanup is still mandatory if you read or modified any code, but with no instrumentation to remove the cleanup is brief).

---

## Phase 3 — Add targeted instrumentation (only when Phase 2 isn't enough)

Sometimes existing logs don't show enough — you need to add temporary log statements at suspect points to surface what's happening. This phase has strict rules to keep cleanup reliable.

### 3.1 Pick a unique cleanup marker

Pick **one** marker for this entire debugging session. Format:

```
TRAIL-DEBUG-<4-CHAR-RANDOM>
```

Example: `TRAIL-DEBUG-7K3F`. **Do not reuse markers across sessions** — pick a fresh one each time so Phase 4's cleanup doesn't accidentally remove instrumentation from a different debugging effort.

**Every line you add must include this marker verbatim.** No exceptions. The marker is the only mechanism that lets you find and remove the instrumentation cleanly.

### 3.2 Add log statements at suspect points

Where to instrument:
- **Function entry** of suspect functions — confirms the code path is reached
- **Just before/after suspicious calls** — what does the call return? did it throw?
- **Inside conditional branches** — which branch fired?
- **Around error returns / catch blocks** — what's the actual error vs what's reported to the user?
- **Loop iterations** — does the loop run the expected number of times with the expected values?
- **Async boundaries** — promise resolution, callback invocation, channel sends/receives

**Use plain stdout/stderr writes — don't try to integrate with the project's logger.**

trail captures whatever the process writes to stdout/stderr, full stop. You do **not** need to discover, configure, or import the project's existing logging framework (zap, winston, pino, logrus, slog, structlog, log4j, …). A bare `console.log` / `print` / `fmt.Println` lands in the captured session the same as a structured log line would. Reaching for the project's logger adds friction (you might have to wire a new logger instance, match a config, or learn its API) and increases the surface area you have to clean up in Phase 4.

The one rule: if you use the project's logger anyway (because the file already imports it and a stray `console.log` would look out of place in code review), still keep the marker in the message string — Phase 4's `grep` finds it identically.

**Examples by language. Always include the marker. Always flush stdout if the language buffers (Python, sometimes Node).**

#### JavaScript / TypeScript / Node

```js
console.log("[TRAIL-DEBUG-7K3F] entering processOrder, orderId=", orderId);
console.log("[TRAIL-DEBUG-7K3F] db.findUser returned:", user);
console.error("[TRAIL-DEBUG-7K3F] caught error in payment flow:", err.message, err.stack);
```

#### Python

```python
import sys
print(f"[TRAIL-DEBUG-7K3F] entering process_order, order_id={order_id}", flush=True)
print(f"[TRAIL-DEBUG-7K3F] db result type={type(user)}, value={user!r}", flush=True)
print(f"[TRAIL-DEBUG-7K3F] EXCEPTION: {type(e).__name__}: {e}", file=sys.stderr, flush=True)
```

#### Go

```go
fmt.Fprintf(os.Stderr, "[TRAIL-DEBUG-7K3F] entering ProcessOrder, orderID=%v\n", orderID)
fmt.Fprintf(os.Stderr, "[TRAIL-DEBUG-7K3F] db.FindUser returned: user=%+v err=%v\n", user, err)
```

#### Ruby

```ruby
warn "[TRAIL-DEBUG-7K3F] entering process_order, order_id=#{order_id}"
warn "[TRAIL-DEBUG-7K3F] error in payment flow: #{e.class}: #{e.message}"
```

#### Java

```java
System.err.println("[TRAIL-DEBUG-7K3F] entering processOrder, orderId=" + orderId);
System.err.println("[TRAIL-DEBUG-7K3F] db result: " + user);
```

#### Rust

```rust
eprintln!("[TRAIL-DEBUG-7K3F] entering process_order, order_id={:?}", order_id);
eprintln!("[TRAIL-DEBUG-7K3F] db.find_user returned: {:?}", user);
```

### 3.3 Restart and reproduce

After adding the instrumentation, tell the user:

> I added N debug lines marked `TRAIL-DEBUG-7K3F` at <files>. Restart the process so the new lines get captured and reproduce the issue once:
>
> ```bash
> # Ctrl+C the running process, then re-run it under trail (same command as before).
> trail run -- <their command>
> ```
>
> When you do, trail prints one line like `capturing → <uuid> (file: ...)` — paste it back to me so I know which session to query, then tell me when you've reproduced the issue.

**This creates a new active session.** Once the user pastes the printed `capturing → <uuid>` line, use that UUID directly. If they don't paste it (or you missed it), call `list_sessions(active_only=true)` and pick the active session with the matching `command` and most recent `started_at`.

### 3.4 Query the new instrumentation

Filter for your marker — this surfaces only the lines you added:

```json
get_logs({"session_id": "<new_session_id>", "filters": {"query": "TRAIL-DEBUG-7K3F"}})
```

Read the entries, form a hypothesis. If you need more visibility (a branch that didn't get instrumented, a value you didn't capture), repeat Step 3.2 with the **same marker** and re-do 3.3 + 3.4. Iterate until you have enough information to diagnose.

---

## Phase 4 — Cleanup (mandatory)

This is **non-optional**. Leftover debug logs are noise that becomes technical debt the moment you stop debugging. The cleanup is the difference between a useful debugging session and a code-base degradation.

### 4.1 Find every line carrying your marker

Prefer ripgrep — it respects `.gitignore` so `node_modules`, `vendor`, build artifacts, etc. are skipped automatically:

```bash
rg TRAIL-DEBUG-7K3F
```

Fallback if `rg` isn't available:

```bash
grep -rn --exclude-dir={node_modules,.git,dist,build,vendor,target} "TRAIL-DEBUG-7K3F" .
```

This lists every file:line that contains the marker. Each match should be a single log statement you added in Phase 3.

### 4.2 Remove each instrumentation block in full

For each match, use `Edit` to remove the **entire log statement** — not just the marker, the whole `console.log(...)` / `print(...)` / `fmt.Fprintf(...)` call. If a log statement spans multiple lines (long arg list), remove all of them.

**Watch out for:**
- Trailing semicolons or commas you may need to remove
- Empty lines left behind that look messy — collapse them
- Indentation if the line was inside a control block

### 4.3 Verify zero matches remain

```bash
rg TRAIL-DEBUG-7K3F   # or the grep fallback from 4.1
```

This **must** return nothing. If anything is left, remove it. Do not consider Phase 4 complete until this command returns empty.

### 4.4 Report to the user

Tell the user, concisely:

1. **What you instrumented** — files and a one-line description of why each spot got a probe
2. **What you found** — the diagnosis from the captured output, with the most relevant log line(s) quoted
3. **What you changed** — the actual fix (if you applied one) or your recommendation
4. **Confirmation that all instrumentation is removed** — "verified `grep TRAIL-DEBUG-7K3F .` returns nothing"

### 4.5 Optional: clean up the session files

The captured JSONL session files accumulate in `~/.config/trail/sessions/`. They're useful while debugging and harmless if left behind, but if the user wants them gone:

```bash
trail sessions rm <session_id>           # remove one
trail sessions rm --all                   # remove all
```

Don't do this without asking — the user might want to keep the session for further analysis or to share with a teammate.

---

## Worked example

**User:** *"My Express server is returning 500 on POST /orders but I can't tell why. Help me debug."*

**Phase 1.** I run `trail version` → `trail 0.1.0`. Good.
I call `list_sessions(active_only=true)` → sees one session named `node server.js`, started 12 min ago. Matches an Express server. Got `session_id=abc-123`.

**Phase 2.** I call `get_logs("abc-123", {level: "error"})` → returns one entry:
```
[line 47] error  stderr  TypeError: Cannot read property 'id' of undefined at /app/routes/orders.js:23
```
I read `routes/orders.js` line 23 and see `req.body.user.id`. The user object is missing from the request body.

**Phase 3.** Skipped — Phase 2 already explained the issue.

**Phase 4.** I report:
> Found it on line 47 of the captured log. Your handler at `routes/orders.js:23` reads `req.body.user.id` but POST /orders is being called without a `user` object in the body — TypeError on every request. Either:
>
> - Add a guard: `if (!req.body.user?.id) return res.status(400).json({ error: 'user.id required' })`
> - Or fix the caller to include `user` in the payload
>
> No code instrumentation was added, so nothing to clean up.

---

## Anti-patterns to avoid

- **Don't skip Phase 1.** Without an active session you're guessing at logs that don't exist. Verify first.
- **Don't add log statements without the marker.** They become permanent technical debt the moment Phase 4 forgets about them.
- **Don't reuse marker IDs across debugging sessions.** A fresh ID per session means cleanup is surgical.
- **Don't skip Phase 4.** "I'll clean it up later" almost never happens. Do it now while you remember exactly what you added.
- **Don't silently change unrelated code while you're in there.** Keep instrumentation surgical. If you spot another bug, mention it but don't quietly fix it.
- **Don't assume an existing session captures the right process.** Always check the `command` field — there might be three Node processes running and only one is the one the user means.
- **Don't try to filter by both `start_line` and `duration`.** Mutually exclusive — the tool will return an error. Use one of `{time-window}`, `{duration}`, `{line-range}` per call.
- **Don't `trail sessions rm --all` without asking.** That deletes every captured session, including ones from unrelated work.
