# Worked examples — debug-with-trail

Three end-to-end traces of the workflow in `SKILL.md`, each demonstrating a different decision branch:

1. **Diagnosis from existing logs** — Phase 3 (instrumentation) is skipped because Phase 2 already explained the issue.
2. **Closed-loop variant** — auto-reloader keeps the trail session alive across restarts, collapsing the iteration loop.
3. **Failing test** — the captured process is the test runner itself, in watch mode.

Read these when you want a concrete picture of phase compression, closed-loop iteration, or test debugging.

---

## Express 500 — diagnosis from existing logs (Phase 3 skipped)

**User:** *"My Express server is returning 500 on POST /orders but I can't tell why. Help me debug."*

**Phase 1.** I run `trail version` → `trail 0.1.0`. Good.
I call `list_sessions(active_only=true)` → sees one session named `node server.js`, started 12 min ago. Matches an Express server. Got `session_id=abc-123`.

**Phase 2.** I call `get_logs("abc-123", {level: "error"})` → returns one entry:
```
[line 47] error  stderr  TypeError: Cannot read property 'id' of undefined at /app/routes/orders.js:23
```
I read `routes/orders.js` line 23 and see `req.body.user.id`. The user object is missing from the request body.

**Phase 3.** Skipped — Phase 2 already explained the issue.

**Phase 4.** I present the analysis:
> Found it on line 47 of the captured log. Your handler at `routes/orders.js:23` reads `req.body.user.id` but POST /orders is being called without a `user` object in the body — TypeError on every request. Two ways to fix:
>
> - Add a guard: `if (!req.body.user?.id) return res.status(400).json({ error: 'user.id required' })`
> - Or fix the caller to include `user` in the payload
>
> Does this match what you're seeing, or should I dig further? No code instrumentation was added so there's nothing to roll back if you want to keep investigating.

User replies "yep, that's it — go with the guard". → Phase 5.

**Phase 5.** I apply the guard at `routes/orders.js:23`. Ask the user to restart under trail and reproduce a POST without `user`. New session id `def-456`. I call `get_logs("def-456", {level: "error"})` → no TypeError. I call `get_logs("def-456", {query: "user.id required"})` → confirms the 400 response is now being returned. Fix verified.

**Phase 6.** No markers were added so `rg TRAIL-DEBUG-...` returns nothing immediately. I confirm: "Guard applied at `routes/orders.js:23` and verified with the new session — TypeError gone, 400 returned as expected. No debug instrumentation to remove."

---

## Closed-loop variant — silent failure with auto-reloader

**User:** *"Order creation is silently failing for some users. No error returned, just a 200 with an empty body. Can you debug?"*

**Phase 1.** `trail version` ok. `list_sessions(active_only=true)` → one session, command `node server.js`. Single match. I notice this is *not* under a watcher, so before doing anything else I recommend:

> Looks like we'll likely need to add a few probes and iterate. Mind relaunching as `trail run -- nodemon server.js`? That keeps the same trail session alive across restarts so I can iterate without asking you to ctrl+c each time. Also — can you paste the curl that triggers a failing order so I can run it myself between iterations?

User restarts, pastes new session id `aaa-111` and a curl: `curl -X POST localhost:3000/orders -H 'content-type: application/json' -d '{...}'`.

**Phase 2.** `get_logs("aaa-111", {level: "error"})` → empty. The failure is silent, as the user said.

**Phase 3.** Need instrumentation. Marker `TRAIL-DEBUG-9P2Q`. I add probes around `routes/orders.js` handler entry, the validation step, and the DB write. Save.

**Phase 3.3.** Closed-loop path: nodemon auto-restarts. I poll `get_logs("aaa-111", {query: "Listening on", duration: "30s"})` until I see the ready line. Then I run the user's curl myself. Then `get_logs("aaa-111", {query: "TRAIL-DEBUG-9P2Q"})` → I see entry probe fired, validation probe fired, DB probe **never fired**. Found a silent early-return.

**Phase 4.** Present analysis: validation is rejecting the payload silently — the handler returns 200 with empty body when validation fails instead of 400. User confirms diagnosis.

**Phase 5.** I apply the fix (return 400 on validation failure). Save. Nodemon restarts. Wait for ready line. Run user's curl with the original (failing) payload → now returns 400. Run with a corrected payload → 201. Markers confirm the DB probe fires on the corrected payload. Verified.

**Phase 6.** `rg TRAIL-DEBUG-9P2Q` finds 3 matches. Remove each. Re-run `rg` → empty. Confirm: "Removed 3 instrumentation lines from `routes/orders.js`. Fix verified end-to-end. Done."

Total user round-trips: **two** (initial setup + diagnosis confirmation), regardless of how many probes/fixes the loop needed. Compare to the manual flow which would need a round-trip per iteration.

---

## Failing test — Go test debugging

**User:** *"`TestProcessOrder/handles_partial_refund` is failing on main and I can't tell why. The diff is in CI. Help."*

**Phase 1.** `trail version` ok. `list_sessions(active_only=true)` → empty (tests aren't running yet). I ask the user:

> Run the failing test under trail in watch mode so I can iterate:
>
> ```bash
> trail run -- watchexec -r --exts go -- go test -run 'TestProcessOrder/handles_partial_refund' -v ./pkg/orders/...
> ```
>
> Paste the `capturing → <uuid>` line back when it's running.

User replies with `bbb-222`.

**Phase 2.** `get_logs("bbb-222", {filters: {query: "FAIL|--- FAIL"}})` → I see:

```
--- FAIL: TestProcessOrder/handles_partial_refund (0.04s)
    orders_test.go:142: expected refund amount 750, got 0
```

The assertion tells me the refund calculation returned 0 instead of 750. Existing output doesn't show the inputs going in — the test asserts on the output but doesn't log intermediate state.

**Phase 3.** Marker `TRAIL-DEBUG-4M8N`. I add probes inside `CalculateRefund` (the function under test): one at entry showing the input order + `refundType`, one before the early-return guard, one at each branch of the refund-type switch. Save.

Watcher fires automatically. I poll for `test result:` in the recent window, then query my marker:

```json
get_logs({"session_id": "bbb-222", "filters": {"query": "TRAIL-DEBUG-4M8N", "duration": "30s"}})
```

Output shows: entry probe fires with `refundType="partial"`, then the **early-return guard fires** because `order.Status == "pending"` — the test's fixture creates the order in pending state, but the refund logic was written assuming completed orders. The switch never executes; that's why the result is 0.

**Phase 4.** Present analysis: `CalculateRefund` early-returns 0 for pending orders, but the test fixture creates a pending order. Either the fixture should mark the order completed before refund, or `CalculateRefund` should accept pending orders for partial refunds (the spec says partial refunds are allowed pre-fulfillment). Ask the user which is correct.

User confirms the spec allows partial refunds on pending orders → fix is in `CalculateRefund`.

**Phase 5.** Remove the `Status == "pending"` guard for `refundType == "partial"`. Save. Watcher reruns. Poll for `PASS|FAIL`, then check the marker output: early-return no longer fires, switch executes, `partial` branch returns 750. Run the broader test file (`go test ./pkg/orders/...`) once via a one-off `trail run --` invocation to make sure no other tests regressed → all pass. Verified.

**Phase 6.** `rg TRAIL-DEBUG-4M8N` → 4 matches in `calculate_refund.go`. Remove each. Re-`rg` → empty. Confirm to user: "Removed the pending-order guard for partial refunds. 4 instrumentation lines cleaned up. Test now passes; full package suite green."
