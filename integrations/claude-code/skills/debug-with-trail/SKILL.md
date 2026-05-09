---
name: debug-with-trail
description: Debug runtime issues for servers, daemons, workers, and test runs by querying captured stdout/stderr via trail's MCP tools. Walks through prerequisites check, optional targeted instrumentation, log analysis, and mandatory cleanup of any added logs.
when_to_use: User asks to "debug this", "why is this failing", "what's in the logs", "why did it crash", "investigate this error", "trace the bug", "what's happening when I do X", "this test is failing", "why does this test fail", "fix the failing test", "the test suite is broken", or pastes an error message / test failure output and asks for help diagnosing it.
allowed-tools: Bash Read Edit Grep Glob mcp__plugin_trail_trail__list_sessions mcp__plugin_trail_trail__get_logs
---

# Debug with trail

Use this workflow when investigating a runtime issue with the help of `trail` — a local CLI that captures process stdout/stderr into per-session JSONL files and exposes them via the MCP tools `list_sessions` and `get_logs`.

The workflow applies to two broad cases:

1. **Long-running processes** — servers, daemons, workers, anything that stays up and emits logs continuously. The user reproduces the issue by sending a request / triggering the code path, and you query the captured stream.
2. **Test failures** — a test (or a small subset) is failing and you need to figure out why. The "process" trail captures is the test invocation itself (`trail run -- go test ./...`, `trail run -- npm test`, `trail run -- pytest path/to/test.py`). The test runner's stdout/stderr (assertion failures, panics, framework output, plus any `print`/`console.log`/`fmt.Println` from the code under test) lands in a session you can query exactly the same way. Instrumentation goes into either the test file or the production code the test exercises; the marker-based cleanup rules are identical.

Most phases below are written with a server in mind because that's the more common case, but the test-debugging variant is called out wherever the flow differs (mainly around how the session is launched and how iteration works without a long-running daemon).

The workflow has six phases. Steps 1, 2, 4, 5, and 6 are **mandatory**. Step 3 is conditional — only do it when the existing logs don't already explain the issue. **Cleanup (Phase 6) only happens after the fix is verified in Phase 5** — never clean up instrumentation while the user might still want to keep digging or while a fix is unverified.

**Supporting files:**
- `reference.md` — auto-reloader / test-runner watcher tables, framework "ready" indicators, per-language instrumentation syntax. Load when the inline shortlists don't cover the user's stack.
- `examples.md` — three end-to-end worked traces (existing-logs diagnosis, closed-loop variant, failing test). Read when you want a concrete picture of phase compression.

---

## Working ledger (`debug-notes.md`) — maintained throughout the session

From the moment a debugging task begins, keep a running scratchpad in a temp file so both you and the user can see how the investigation is evolving. This is your *mental ledger* — it stops you from re-testing the same hypothesis twice, makes it obvious what you've ruled out, and gives the user a chance to redirect early if you're chasing the wrong thread.

**Location.** Write it to a temp directory that is clearly disposable — e.g. `/tmp/trail-debug-<marker>.md` (use the same `TRAIL-DEBUG-<4-CHAR>` marker from Phase 3.1 if instrumentation has started, otherwise pick a short slug). Never put it in the project repo — it must not get committed.

**Structure.** Plain markdown, kept short. Append as you go; don't rewrite history. Suggested sections:

```markdown
# Debug ledger — <one-line task summary>

## Problem statement
<exact failure as the user described it: error message, command, expected vs actual>

## Data & repro material
- Trail session id(s):
- Repro command (curl / test invocation / enqueue):
- Relevant files identified so far:
- Existing log lines that look suspicious:

## Hypotheses
1. [ ] <hypothesis> — basis: <why you think so> — how to test: <query / probe / fix attempt>
2. [ ] ...

## Test ledger
| # | Hypothesis | What I did | Finding | Verdict |
|---|------------|------------|---------|---------|
| 1 | H1 | added probe at orders.js:23, ran curl X | probe never fired | ruled out — code path not reached |
| 2 | H2 | queried level=error duration=5m | TypeError on req.body.user | likely root cause |

## Open questions / things still to try
- ...
```

**When to update.** At minimum:

- **Phase 1**, right after the active session is identified — write the problem statement, the repro command, and the session id.
- **Phase 2**, after reading existing logs and the suspect code — append the initial **Hypotheses** list. Tell the user what hypotheses you're forming before you start testing them, so they can correct course.
- **Phase 3.4 / Phase 5.2**, after each query of your marker output — append a row to the **Test ledger** with the hypothesis, what you did, what you found, and a verdict (confirmed / ruled out / inconclusive). Update or add hypotheses as new evidence comes in.
- **Phase 4**, before presenting analysis — re-read the ledger to make sure the hypothesis you're presenting is actually the one best supported, not just the most recent one.

**Deletion.** The ledger is removed in **Phase 6** after the user confirms satisfaction with the fix. See Phase 6.5.

---

## Closing the loop: hot-reload + self-driven repro (strongly recommended setup)

The default flow asks the user to manually `Ctrl+C`, re-run `trail run -- ...`, and reproduce the issue every time you add instrumentation (Phase 3.3) or apply a fix (Phase 5.2). That's three human-in-the-loop steps per iteration, which makes long debugging sessions painful and burns goodwill.

You can collapse all three by wrapping a **file-watching auto-reloader** under `trail run`. The reloader is the captured process; it restarts its *own* child whenever code changes, but from trail's point of view the parent process never exited — **the same session id keeps capturing across every restart**. Add a marker, save the file, the daemon restarts automatically, and the new logs land in the same session you're already querying. Then trigger the code path yourself (curl, CLI invocation, queue enqueue, etc.) instead of asking the user to "reproduce it."

When this is set up, the iteration loop becomes:

1. Edit code (instrument or fix) — daemon auto-restarts, same session id.
2. Wait for the "ready" line to appear in `get_logs` (server back up).
3. Trigger the code path yourself with the user-provided curl/command.
4. Query the marker.

No "please restart and tell me when you're done" round trips.

### Recommend this setup at Phase 1 if not already in place

If `list_sessions` shows the user is running their server *directly* under trail (e.g. `trail run -- node server.js`), proactively suggest the closed-loop variant **before** you start adding instrumentation in Phase 3. One line is enough:

> Heads-up: if you re-launch as `trail run -- nodemon server.js` (or whatever auto-reloader fits your stack), the session stays alive across restarts and I can iterate on instrumentation/fixes without asking you to restart each time. Worth it if we're going to make more than one or two changes.

### Auto-reloader by ecosystem

Pick whichever the user already has in `devDependencies` / `go.mod` / etc. — don't push a new dependency on them mid-debug. Common ones:

- **Servers**: `nodemon` / `tsx watch` (Node), `pulse` / `air` (Go), `uvicorn --reload` (Python), `cargo-watch -x run` (Rust), `mix phx.server` (Phoenix), `gradle --continuous` (Java/Kotlin)
- **Tests in watch mode**: `vitest` / `jest --watch` (JS/TS), `pytest-watch` (Python), `watchexec -r --exts go -- go test …` (Go), `cargo watch -x test` (Rust), `guard-rspec` (Ruby)
- **Generic**: `watchexec -r -- <cmd>`, `entr`

For the full per-ecosystem list, see `reference.md`.

```bash
trail run -- nodemon server.js              # Node server
trail run -- vitest                         # JS/TS tests, watch mode
trail run -- watchexec -r --exts go -- go test -run TestProcessOrder ./pkg/orders/...
```

When a single test reproduces the bug, **always narrow the runner to that test** in the wrapped command (`-run TestX` for Go, `--testNamePattern` / `-t` for Jest/Vitest, `pytest path::TestClass::test_x`, `cargo test test_x`). A 200-test suite rerunning on every save buries your marker output and slows the loop.

### Detecting "server is ready" or "test run finished" before querying

After a save-triggered restart, you must not curl until the new process has bound its port — otherwise you'll get a connection refused that has nothing to do with the bug. Poll for the framework's ready line via `get_logs`:

```json
get_logs({"session_id": "<sid>", "filters": {"query": "Application startup complete|Ready in|Listening on", "duration": "30s"}})
```

Adjust the regex to match the framework in use (Express → `Listening on`, Next → `Ready in`, FastAPI → `Application startup complete`, Spring Boot → `Started <App> in`, Rails → `Listening on`, etc.). For test runs in watch mode, the analogous check is "did this rerun finish?" — query for the runner's summary line (`test result:` for cargo, `--- FAIL:` / `^ok` for Go, `Tests:` for Jest/Vitest, `passed`/`failed` for pytest). See `reference.md` for the complete per-framework and per-runner regex tables.

A non-empty match means the process is ready (or the rerun is done); querying for your marker in the same time window then gives you the run's full instrumentation output.

### Try to derive the trigger yourself before asking

Before pinging the user for a curl/argv/enqueue command, spend a minute trying to construct it from the code. The user already told you what's failing — that usually points at a route, a handler, or a function whose call shape you can read directly. Doing this first respects the user's time and lets you propose something concrete ("does `curl -X POST localhost:3000/orders -H 'content-type: application/json' -d '{...}'` look right?") instead of an open-ended ask.

Where to look, in rough order of payoff:

- **OpenAPI / Swagger spec.** Search the repo for `openapi.json`, `openapi.yaml`, `openapi.yml`, `swagger.json`, `swagger.yaml`, or an `api/` / `docs/` folder. If found, the spec gives you the path, method, required headers, and an example request body — enough to assemble a valid curl in one shot.
- **Route definitions in the failing file's neighborhood.** If the user named a path or handler, grep for the route registration: `app.post('/orders', …)`, `@app.route('/orders', methods=['POST'])`, `router.HandleFunc("/orders", …)`, `@PostMapping("/orders")`, Rails `routes.rb`, etc. Read the handler signature and any request-validation schema (zod, pydantic, struct tags, DTOs) to figure out the body/headers it expects.
- **Existing tests or fixtures.** Integration tests and Postman/Bruno/`*.http` files often have a working request payload sitting right there — copy it.
- **gRPC `.proto` files / GraphQL schema.** For non-REST APIs, the schema tells you the method name and message/argument shape; you can build a `grpcurl` or GraphQL query from it.
- **CLI entry points.** For a CLI being debugged, read the argv parser (`cobra`, `argparse`, `commander`, `clap`) to see which flags reproduce the failing path.
- **Job/queue producers.** For a worker, search for callers of the producer (`enqueue(`, `LPUSH`, `publish(`) — one of them is usually a script or admin endpoint that's easier to invoke than crafting the raw payload.

Once you've assembled a candidate, **always confirm with the user before running it for real.** Show the exact command and ask: *"I pulled this from `<spec/route file>` — does this match the call you've been making to reproduce, or is the body/header shape different in your setup?"* Two reasons to confirm rather than just running it:

1. The spec or route may be out of date relative to what the user's client actually sends (extra auth headers, custom content-type, different host/port, a feature flag, a tenant id).
2. Running an unverified `POST` / mutation against the user's running process can have side effects (creates DB rows, sends emails, charges a card in dev mode). The user knows their environment; you don't.

If you can't find anything to derive a trigger from — no spec, route registration not obvious, multi-step UI flow, etc. — fall back to the next subsection and ask the user directly.

### Closing the trigger loop too: ask for the repro command

Once the daemon is auto-reloading, the only step still in the user's hands is *triggering the code path*. If the previous subsection didn't yield a confirmed trigger, ask up front — in the same message where you confirm the daemon setup — for whatever the user uses to provoke the bug. Then run it yourself in subsequent iterations.

Common cases and what to ask for:

- **HTTP server (REST/GraphQL)**: ask for a curl. "Can you paste the curl command (or HTTP request body) that hits the failing endpoint? I'll run it myself after each change so we don't need to round-trip."
- **CLI tool**: ask for the exact argv that reproduces the issue. Run it as a subprocess; its stdout/stderr won't be in trail unless the CLI is itself a long-running process — but the daemon you're debugging may be invoked by the CLI.
- **Background worker / queue consumer**: ask for the enqueue command (e.g. `redis-cli LPUSH …`, `psql -c "INSERT INTO jobs …"`, a small script that calls the producer). The worker is the captured process; the trigger is producing a job.
- **Cron-like scheduled job**: ask for the manual invocation that bypasses the schedule (most jobs have a `--run-now` or you can call the function directly via a REPL/oneshot script).
- **Frontend / browser-driven**: trail captures the *server* process; you can curl the server endpoints the browser would hit, but you cannot reproduce a click. Ask for the equivalent network request, or — if the bug is purely client-side and not visible to the server — note that trail is the wrong tool and suggest browser devtools.
- **Database-triggered code (triggers, hooks, webhooks)**: ask for the SQL/event payload that fires it. Run via `psql` / `mysql` / a small `curl` to the webhook source.
- **gRPC / WebSocket / SSE**: ask for `grpcurl`, `websocat`, or a small client snippet. Same principle — the long-running server is captured, you provide the trigger.
- **Process that runs to completion** (batch script, one-shot job): the daemon-watcher pattern doesn't apply — the process exits each time. Use the manual restart flow from Phase 3.3 / 5.2 instead.
- **Failing test (watch-mode)**: no curl needed — the watcher reruns the test on save automatically. Just confirm with the user *which test(s)* to narrow the runner to (e.g. `-run TestProcessOrder`, `pytest path::test_x`, `--testNamePattern=...`) so the rerun is fast and the marker output isn't buried under unrelated tests.
- **Failing test (one-shot, no watch mode)**: ask for the exact test invocation. You'll be re-running it yourself between iterations: `go test -run TestX ./pkg/...`, `npx jest -t 'X'`, `pytest path/to/test.py::TestClass::test_x -x`. Each invocation is its own trail session — call `list_sessions(active_only=false)` and pick the most recent matching `command`, or have the user paste the `capturing → <uuid>` line.

If the user can't or won't share a trigger command (e.g. the repro is a complex multi-step UI flow), fall back to "tell me when you've reproduced it" — but say so explicitly so the user knows why you're asking them to do the manual step.

### When *not* to recommend this

- Process is a **one-shot** (CLI that exits, batch job, non-watch-mode test invocation) — no daemon to keep alive. For tests specifically, prefer launching the runner in **watch mode** if it has one (see "Test-runner watchers" above) — that turns a one-shot into a closed-loop.
- Bug is a **startup crash** — the auto-reloader's value is preserving the session across runtime restarts; if every run dies in `init`, the session continuity doesn't help much, and the manual restart flow is fine.
- User is debugging on a **remote / production-like machine** where they don't have file-watcher tooling installed and adding it is more disruption than the iteration savings buy.
- The reloader itself is what's broken (rare, but it happens).

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

- **If exactly one active session matches what the user is debugging** (by name, command, or context — e.g. user is debugging an Express server and you see a session with command `node server.js`) → record its `session_id` and `file_path`, **create the working ledger** (`/tmp/trail-debug-<slug>.md`) with the problem statement, repro command, and session id (see "Working ledger" section above), and continue to Phase 2.

- **If multiple active sessions look plausible** → ask the user which one. Don't guess.

- **If no active session matches** (or no sessions at all) → tell the user to launch the relevant process under trail. Be specific to the case:

  **Server / long-running process:**
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

  **Failing test:**
  > Run the failing test under trail so I can see the assertion output and any logs from the code under test:
  >
  > ```bash
  > # Narrow to the failing test if you can, then wrap with trail:
  > trail run -- go test -run TestProcessOrder ./pkg/orders/...
  > # or:  trail run -- npx jest -t 'process order'
  > # or:  trail run -- pytest -x tests/test_orders.py::TestProcessOrder
  > ```
  >
  > Even better, if you have a watch-mode runner (`vitest`, `jest --watch`, `cargo watch -x test`, `pytest-watch`, `gow test`, etc.), wrap *that* under trail — same session stays alive across reruns and I can iterate without you restarting each time.

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

**Update the ledger** before deciding next steps: append the suspicious log lines you found and the files/symbols they point to under **Data & repro material**, then write an initial **Hypotheses** list (numbered, with the basis and the planned test for each). Surface those hypotheses to the user in your next message — *"current hypotheses are H1, H2; I'll test H1 first by …"* — so they can redirect before you spend iterations on the wrong thread.

**If you can diagnose the issue from existing logs → skip directly to Phase 4** (present your analysis to the user). Cleanup (Phase 6) still runs after the fix is verified, but with no instrumentation to remove it's brief.

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

**Always include the marker. Always flush stdout if the language buffers (Python, sometimes Node).**

```js
console.log("[TRAIL-DEBUG-7K3F] entering processOrder, orderId=", orderId);
console.error("[TRAIL-DEBUG-7K3F] caught error in payment flow:", err.message);
```

For Python (`print(..., flush=True)`), Go (`fmt.Fprintf(os.Stderr, ...)`), Ruby (`warn`), Java (`System.err.println`), and Rust (`eprintln!`) syntax, see `reference.md`.

### 3.3 Restart and reproduce

**If the closed-loop setup is in place** (auto-reloader wrapped under `trail run` — see "Closing the loop" section): the daemon restarted itself the moment you saved the file. The session id from Phase 1.2 is still valid — no new session, no user round-trip. Wait for the framework's ready line in `get_logs` (e.g. `Ready in`, `Listening on`, `Application startup complete`), then trigger the repro yourself with the curl/command the user provided. Skip the rest of this subsection.

**Otherwise, manual restart flow.** Tell the user:

> I added N debug lines marked `TRAIL-DEBUG-7K3F` at <files>. Restart the process so the new lines get captured and reproduce the issue once:
>
> ```bash
> # Ctrl+C the running process, then re-run it under trail (same command as before).
> trail run -- <their command>
> ```
>
> When you do, trail prints one line like `capturing → <uuid> (file: ...)` — paste it back to me so I know which session to query, then tell me when you've reproduced the issue.

**This creates a new active session.** Once the user pastes the printed `capturing → <uuid>` line, use that UUID directly. If they don't paste it (or you missed it), call `list_sessions(active_only=true)` and pick the active session with the matching `command` and most recent `started_at`.

If you find yourself about to do this manual round-trip for the second time in one debugging session, **pause and recommend the closed-loop setup** before continuing — the savings compound from iteration two onward.

### 3.4 Query the new instrumentation

Filter for your marker — this surfaces only the lines you added:

```json
get_logs({"session_id": "<new_session_id>", "filters": {"query": "TRAIL-DEBUG-7K3F"}})
```

Read the entries, form a hypothesis. **Append a row to the Test ledger** in `debug-notes.md`: which hypothesis you were testing, what probes you added, what the captured lines showed, and a verdict (confirmed / ruled out / inconclusive). If new hypotheses emerge, add them to the **Hypotheses** list. If you need more visibility (a branch that didn't get instrumented, a value you didn't capture), repeat Step 3.2 with the **same marker** and re-do 3.3 + 3.4. Iterate until you have enough information to diagnose — the ledger keeps you from re-running tests you've already done.

---

## Phase 4 — Present analysis & confirm with user (mandatory, loops until satisfied)

**Do not clean up yet.** Before touching any instrumentation, present your analysis to the user and wait for their confirmation. Cleanup is irreversible from a context standpoint — once the markers are gone, re-instrumenting costs another restart + reproduction. Stay in this phase until the user is satisfied.

### 4.1 Present your analysis

Before writing the message, re-read `debug-notes.md` end-to-end. Make sure the hypothesis you're about to present is the one the test ledger actually supports — not just the most recent thread you were on. If two hypotheses are both still live, say so explicitly rather than picking one.

Tell the user, concisely:

1. **What you instrumented (if anything)** — files and a one-line description of why each spot got a probe. Skip this bullet if Phase 3 was skipped.
2. **What you found** — the diagnosis from the captured output, with the most relevant log line(s) quoted verbatim so the user can verify the reasoning.
3. **Your hypothesis or root cause** — what you believe is happening and why.
4. **Recommended fix or next step** — concrete code change, config tweak, or further investigation. If you've already drafted a fix, show the diff but do not apply it yet unless the user asks.

End with an explicit ask: *"Does this match what you're seeing, or should I dig further? The debug instrumentation is still in place so we can keep iterating."*

### 4.2 Wait for the user's response, then branch

- **User is satisfied with the diagnosis** (confirms the diagnosis, accepts the proposed fix, says "looks good", "that's it", "ship it", etc.) → proceed to Phase 5 (apply & verify the fix).

- **User wants more investigation** ("not quite", "but why does X happen?", "can you also check Y?", "doesn't explain Z") → **do not clean up, do not apply a fix yet**. Loop back to whichever phase is appropriate:
  - More queries on existing logs → Phase 2
  - More instrumentation needed → Phase 3.2 with the **same marker** (then 3.3 + 3.4 with a fresh reproduction)
  - Then return here to 4.1 and present the updated analysis.

- **User proposes a different hypothesis** → treat it as "wants more investigation" — gather evidence for or against their hypothesis before re-presenting.

- **User is ambiguous** ("hmm, maybe", "I'm not sure") → ask one clarifying question rather than assuming. Do not advance to Phase 5 on ambiguous signal.

Repeat 4.1 → 4.2 as many times as needed. There is no iteration cap — the user decides when the analysis is good enough.

---

## Phase 5 — Apply fix & verify (mandatory, loops until the fix is confirmed working)

The diagnosis is agreed; now prove the fix actually works using the instrumentation that's still in place. **Do not clean up yet.** Markers are most valuable here — a one-liner saying "the bad branch no longer fires" is worth more than any amount of static reasoning.

### 5.1 Apply the fix

- Apply **one** fix at a time. If the diagnosis named multiple independent issues, address them one by one so each can be verified in isolation — a batched fix that mostly works hides which part regressed.
- Use `Edit` to make the code change. Keep it surgical — don't drag in unrelated cleanup, even if you spot it.
- If the user said "just recommend, I'll apply it myself" in Phase 4, hand them the diff and skip to 5.2 once they confirm they've applied it.

### 5.2 Restart, reproduce, and verify with the markers

**Closed-loop path** (auto-reloader under `trail run`): saving the fix already triggered a restart. Same session id. Poll for the framework's ready line in `get_logs`, then run the user-provided trigger (curl, CLI command, enqueue script, etc.) yourself. No user message needed for this iteration.

**Manual path** (no auto-reloader). Tell the user:

> Fix applied at `<files:lines>`. Restart under trail and reproduce the same scenario once so I can verify the markers show the fix worked:
>
> ```bash
> # Ctrl+C, then:
> trail run -- <their command>
> ```
>
> Paste the `capturing → <uuid>` line back when you've reproduced it.

Once you have the new session id (or are reusing the existing one in the closed-loop path), query the same marker:

```json
get_logs({"session_id": "<new_session_id>", "filters": {"query": "TRAIL-DEBUG-7K3F"}})
```

Read the captured lines and check, explicitly, against the hypothesis from Phase 4:
- Does the previously-firing bad branch / error path no longer appear?
- Does the previously-missing good path now appear with the expected values?
- Are there any new error lines that the fix introduced (`level: "error"` query as a sanity check)?

### 5.3 Branch on the result

- **Fix verified** (markers confirm the predicted behavior, no new errors) → append a final ledger row noting the fix and the verifying log lines, tell the user "verified by markers — <quote 1–2 lines>", and proceed to Phase 6 (cleanup).

- **Fix didn't work** (bad path still fires, or the symptom persists) → **do not clean up**. The diagnosis was incomplete or the fix was wrong. Options:
  - Look at the markers more carefully — they often show why the fix missed
  - Add more instrumentation (Phase 3.2, **same marker**) to localize what's still wrong
  - Loop back to Phase 4.1 with the updated analysis, then return here with a revised fix

- **Fix worked but exposed a second issue** (e.g. you fixed the TypeError and now a 404 surfaces) → tell the user, ask whether to address it in this session or defer. If addressing it now, treat it as a fresh fix: back to Phase 5.1. If deferring, note it and proceed to Phase 6.

- **Multiple fixes were planned** → after each one is verified, return to 5.1 with the next fix. Only proceed to Phase 6 once **every** planned fix has been verified individually.

Repeat 5.1 → 5.2 → 5.3 until every fix is verified. The user does not need to re-confirm the diagnosis at each iteration — only the fix-verification loop runs here.

---

## Phase 6 — Cleanup (mandatory, only after every fix is verified in Phase 5)

This is **non-optional once Phase 5 is complete**. Leftover debug logs are noise that becomes technical debt the moment you stop debugging. The cleanup is the difference between a useful debugging session and a code-base degradation.

### 6.1 Find every line carrying your marker

Prefer ripgrep — it respects `.gitignore` so `node_modules`, `vendor`, build artifacts, etc. are skipped automatically:

```bash
rg TRAIL-DEBUG-7K3F
```

Fallback if `rg` isn't available:

```bash
grep -rn --exclude-dir={node_modules,.git,dist,build,vendor,target} "TRAIL-DEBUG-7K3F" .
```

This lists every file:line that contains the marker. Each match should be a single log statement you added in Phase 3.

### 6.2 Remove each instrumentation block in full

For each match, use `Edit` to remove the **entire log statement** — not just the marker, the whole `console.log(...)` / `print(...)` / `fmt.Fprintf(...)` call. If a log statement spans multiple lines (long arg list), remove all of them.

**Watch out for:**
- Trailing semicolons or commas you may need to remove
- Empty lines left behind that look messy — collapse them
- Indentation if the line was inside a control block

### 6.3 Verify zero matches remain

```bash
rg TRAIL-DEBUG-7K3F   # or the grep fallback from 6.1
```

This **must** return nothing. If anything is left, remove it. Do not consider Phase 6 complete until this command returns empty.

### 6.4 Confirm cleanup to the user

A short, single message is enough — the analysis was delivered in Phase 4 and the fix was verified in Phase 5. Just confirm:

- **What was removed** — number of instrumentation lines and the files they were in
- **Verification** — "verified `rg TRAIL-DEBUG-7K3F` returns nothing"
- **Fix status recap** — which fix(es) were applied and verified

### 6.5 Delete the working ledger

The ledger in `/tmp/trail-debug-<slug>.md` has done its job. Remove it:

```bash
rm /tmp/trail-debug-<slug>.md
```

Do this **only after the user has confirmed satisfaction with the fix in Phase 5** — if they reopen the thread later in the same session and the ledger is gone, you've lost the audit trail of what was already ruled out. If the user explicitly asks you to keep the notes (rare), tell them where it lives and skip the deletion.

### 6.6 Optional: clean up the session files

The captured JSONL session files accumulate in `~/.config/trail/sessions/`. They're useful while debugging and harmless if left behind, but if the user wants them gone:

```bash
trail sessions rm <session_id>           # remove one
trail sessions rm --all                   # remove all
```

Don't do this without asking — the user might want to keep the session for further analysis or to share with a teammate.

---

## Worked examples

For three end-to-end traces showing the workflow in action — diagnosis from existing logs (Phase 3 skipped), the closed-loop variant with auto-reloader, and a failing-test debug — see `examples.md`.

---

## Anti-patterns to avoid

- **Don't skip Phase 1.** Without an active session you're guessing at logs that don't exist. Verify first.
- **Don't add log statements without the marker.** They become permanent technical debt the moment Phase 4 forgets about them.
- **Don't reuse marker IDs across debugging sessions.** A fresh ID per session means cleanup is surgical.
- **Don't clean up before the user confirms in Phase 4.** If they want to dig further, the markers need to still be in place — re-instrumenting costs another restart and reproduction.
- **Don't clean up before the fix is verified in Phase 5.** A fix that "looks right" in the diff is not the same as a fix proven by the markers. If you remove the instrumentation and the fix turns out to be wrong, you pay another full instrumentation cycle.
- **Don't batch multiple fixes through Phase 5 in one shot.** Apply and verify them one at a time, otherwise a regression in fix #2 hides which fix actually broke things.
- **Don't skip Phase 6.** "I'll clean it up later" almost never happens. Do it as soon as every fix is verified in Phase 5, while you remember exactly what you added.
- **Don't infer satisfaction from silence or ambiguity.** Ask one clarifying question if the user's reply is non-committal — never advance to cleanup on a maybe.
- **Don't curl the server before it's ready after a restart.** A connection-refused right after a save-triggered reload tells you nothing about the bug — it just means the new process hasn't bound the port yet. Always wait for a framework ready line in `get_logs` first.
- **Don't push a new auto-reloader on the user mid-debug.** Recommend one they already have (visible in `package.json`, `go.mod`, etc.). If nothing's installed and they don't want to add one, fall back to the manual restart flow without friction.
- **Don't assume the closed-loop pattern works for one-shot processes.** A CLI that runs and exits doesn't keep a session alive across restarts; the watcher just relaunches it, which is fine but each launch is a new session. Use the manual flow.
- **Don't run the entire test suite when one test reproduces the failure.** A 200-test rerun on every save buries marker output and slows iteration to a crawl. Narrow with `-run TestX` (Go), `-t 'name'` / `--testNamePattern` (Jest/Vitest), `pytest path::TestClass::test_x`, etc. Run the broader suite *once*, after the fix is verified, to catch regressions.
- **Don't confuse a failing assertion with a thrown error.** Test runners report assertion failures and panics differently — Go writes `--- FAIL` and `panic:` separately, pytest distinguishes `failed` from `error`. When querying, search for both patterns or your diagnosis will miss the kind of failure that's actually happening.
- **Don't instrument only the test file** when the bug is in the code under test. Probes inside the production function (entry, branches, return values) usually pinpoint the issue faster than probes inside the test setup, which mostly tells you what the test is doing — which you already know from reading the test.
- **Don't silently change unrelated code while you're in there.** Keep instrumentation surgical. If you spot another bug, mention it but don't quietly fix it.
- **Don't assume an existing session captures the right process.** Always check the `command` field — there might be three Node processes running and only one is the one the user means.
- **Don't try to filter by both `start_line` and `duration`.** Mutually exclusive — the tool will return an error. Use one of `{time-window}`, `{duration}`, `{line-range}` per call.
- **Don't `trail sessions rm --all` without asking.** That deletes every captured session, including ones from unrelated work.
