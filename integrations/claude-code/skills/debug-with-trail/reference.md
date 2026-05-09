# Reference — debug-with-trail

Lookup tables and per-language detail referenced from `SKILL.md`. Load this file when:

- You need an auto-reloader for a stack not covered by the short list in `SKILL.md`
- You need the framework "ready" / test-runner "finished" regex for polling
- You're adding instrumentation in Phase 3.2 in a language other than JavaScript

---

## Auto-reloaders by ecosystem (servers / long-running processes)

Pick whichever the user already has in `devDependencies` / `go.mod` / etc. Don't push a new dependency on them mid-debug.

- **Node / TypeScript**: `nodemon`, `ts-node-dev`, `tsx watch`, `node --watch` (Node ≥ 18.11), `next dev`, `vite` (for SSR/server-side), `wrangler dev`
- **Go**: [`pulse`](https://github.com/Pratham-Mishra04/pulse), `air`, `reflex`, `modd`, `CompileDaemon`, `gow`
- **Python**: `uvicorn --reload`, `flask --debug`, `watchmedo auto-restart`, `watchexec -r --`, `hupper`, `gunicorn --reload`
- **Ruby**: `rerun`, `guard`, Rails dev server, `foreman` with a reloader
- **Rust**: `cargo-watch -x run`, `bacon`, `systemfd + cargo-watch` for socket-preserving restarts
- **Elixir**: `mix phx.server` (Phoenix has built-in reload), `iex -S mix`
- **Java/Kotlin**: `gradle --continuous`, Spring Boot DevTools, Quarkus dev mode
- **Generic fallback**: `watchexec -r -- <cmd>`, `entr`, `nodemon --exec` for non-Node commands, `reflex -r '\\.ext$' -- <cmd>`

Wrap any of them under trail:

```bash
trail run -- nodemon server.js
trail run -- pulse                     # Go, https://github.com/Pratham-Mishra04/pulse
trail run -- air                       # Go, uses .air.toml in cwd
trail run -- uvicorn app:main --reload
trail run -- cargo watch -x run
trail run -- watchexec -r -- ./bin/start.sh
```

---

## Test-runner watchers (when debugging a failing test)

The same closed-loop trick works for tests — wrap a watch-mode test runner under `trail run`. The runner stays alive (single trail session), reruns the test on file save, and you query the marker after each rerun. Triggering the test is also automatic since the watcher does it; you only need to manually trigger if you want a one-off rerun of a specific test.

- **JS/TS**: `jest --watch`, `vitest` (watch by default), `mocha --watch`, `playwright test --watch`, `node --test --watch` (Node ≥ 19)
- **Python**: `pytest-watch` (`ptw`), `pytest --looponfail` (with `pytest-xdist`), `watchexec -r -- pytest -x`
- **Go**: no first-party watch flag, but `watchexec -r --exts go -- go test ./...`, `reflex -r '\.go$' -- go test ./...`, or `gow test ./...`
- **Ruby**: `guard-rspec`, `bundle exec rspec --watch` (via `rspec-watcher`), `watchexec -r -- bundle exec rspec`
- **Rust**: `cargo watch -x test`, `bacon test`
- **Java/Kotlin**: `gradle test --continuous`, `mvn -Dtest=… test` under `watchexec`
- **Generic**: `watchexec -r -- <test-cmd>`, `entr -r <test-cmd>`

```bash
trail run -- vitest                                     # JS/TS
trail run -- pytest-watch -- -x tests/test_orders.py    # Python, narrow to one file
trail run -- watchexec -r --exts go -- go test -run TestProcessOrder ./...
trail run -- cargo watch -x 'test process_order'
```

When a single test reproduces the bug, **always narrow the runner to that test** in the wrapped command (`-run TestX` for Go, `--testNamePattern` / `-t` for Jest/Vitest, `pytest path::TestClass::test_x`, `cargo test test_x`). A 200-test suite rerunning on every save buries your marker output and slows the loop.

---

## Framework "ready" indicators (server back up after restart)

Most frameworks emit something distinctive on startup. Use these as the regex in a `get_logs` query before sending traffic at the freshly-restarted process.

- **Express**: `Server listening on`, `Listening on port`
- **Next.js**: `Ready in`, `started server on`
- **FastAPI / uvicorn**: `Application startup complete`, `Uvicorn running on`
- **Spring Boot**: `Started <App> in`
- **Rails**: `Listening on`, `Puma starting`
- **Go (custom)**: whatever the user logs at boot — ask if unsure.

```json
get_logs({"session_id": "<sid>", "filters": {"query": "Application startup complete|Ready in|Listening on", "duration": "30s"}})
```

Backup: poll the port with a short retry, but only after at least one ready-indicator line is in the logs:

```bash
for i in {1..20}; do curl -fsS http://localhost:8080/health && break; sleep 0.5; done
```

---

## Test-runner "finished" indicators (rerun complete in watch mode)

For test runs in watch mode, the analogous check is "did this rerun finish?" — query for the runner's per-run summary line.

- **Go**: `PASS`, `FAIL`, `--- FAIL:`, `ok\s+`, `FAIL\s+`
- **Jest / Vitest**: `Tests:`, `Test Files`, `Duration`, `Ran all test suites`
- **Pytest**: `passed`, `failed`, `error in`, `===.*seconds ===`
- **Cargo test**: `test result:`, `running \d+ tests`
- **RSpec**: `examples,`, `Finished in`

```json
get_logs({"session_id": "<sid>", "filters": {"query": "test result:|--- FAIL:|^FAIL\\s|^ok\\s", "duration": "60s"}})
```

A non-empty match means the rerun finished; querying for your marker in the same time window then gives you the run's full instrumentation output.

---

## Instrumentation code by language (Phase 3.2)

Always include the cleanup marker (`TRAIL-DEBUG-<4-CHAR>`) verbatim. Always flush stdout if the language buffers (Python, sometimes Node).

### JavaScript / TypeScript / Node

```js
console.log("[TRAIL-DEBUG-7K3F] entering processOrder, orderId=", orderId);
console.log("[TRAIL-DEBUG-7K3F] db.findUser returned:", user);
console.error("[TRAIL-DEBUG-7K3F] caught error in payment flow:", err.message, err.stack);
```

### Python

```python
import sys
print(f"[TRAIL-DEBUG-7K3F] entering process_order, order_id={order_id}", flush=True)
print(f"[TRAIL-DEBUG-7K3F] db result type={type(user)}, value={user!r}", flush=True)
print(f"[TRAIL-DEBUG-7K3F] EXCEPTION: {type(e).__name__}: {e}", file=sys.stderr, flush=True)
```

### Go

```go
fmt.Fprintf(os.Stderr, "[TRAIL-DEBUG-7K3F] entering ProcessOrder, orderID=%v\n", orderID)
fmt.Fprintf(os.Stderr, "[TRAIL-DEBUG-7K3F] db.FindUser returned: user=%+v err=%v\n", user, err)
```

### Ruby

```ruby
warn "[TRAIL-DEBUG-7K3F] entering process_order, order_id=#{order_id}"
warn "[TRAIL-DEBUG-7K3F] error in payment flow: #{e.class}: #{e.message}"
```

### Java

```java
System.err.println("[TRAIL-DEBUG-7K3F] entering processOrder, orderId=" + orderId);
System.err.println("[TRAIL-DEBUG-7K3F] db result: " + user);
```

### Rust

```rust
eprintln!("[TRAIL-DEBUG-7K3F] entering process_order, order_id={:?}", order_id);
eprintln!("[TRAIL-DEBUG-7K3F] db.find_user returned: {:?}", user);
```
