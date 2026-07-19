# S9 — Event Defs: Work Strategy

> **Archived 2026-07-18.** S9 shipped in patch015 (event defs; see
> `CHANGELOG.md` 0.10.0 and `API_V2_TRACKING.md` S9 row). This execution
> strategy is retained as history and is no longer maintained.

**Stage:** S9 (Part 1, final remaining feature stage)
**Status at time of writing:** not started; greenfield
**Plan estimate:** 2 weeks
**Prerequisite state:** tree green (22/22), CRUD and walk paths stable

This document is the execution strategy for S9. It is grounded in the actual
structure of the handlers S9 must hook into, not the plan's prose. The plan
([API_V2_DEVELOPMENT_PLAN.md](API_V2_DEVELOPMENT_PLAN.md#s9--event-defs-basic))
defines *what* ships; this defines *how* and *in what order*, and records the
architectural facts that shape both.

---

## 1. Architectural ground truth (verified against code)

Three facts about the existing code determine the whole design. Each was checked
against the source, not assumed.

### 1.1 Entity events are post-commit by construction

`handleCreate` (`server.go:1198`) does: validate → `store.Create(ctx, entity,
data)` → write response. The storage `Create(ctx, entity, data) (int, error)`
signature is **self-contained**: it opens its own transaction, commits
internally, and returns the new ID. The handler never holds an open transaction.

**Consequence:** `entity.created` / `.updated` / `.deleted` can only fire *after*
the store call returns — i.e. after commit. There is no in-transaction hook to
attach to without changing the storage interface, which is out of scope for S9.
This is not a limitation to work around; it *aligns* with the plan's decision
that Part 1 dispatch is async. The entity event reflects committed state.

### 1.2 The FSM walk path is the opposite shape — and that is fine

`FsmWalkInTx` (`storage/fsm_walk.go:147`) holds a `*sql.Tx`, writes `output_json`
into `fsm_history` **inside** that transaction, and returns `Outputs []string`
(WalkResult.Outputs) to the handler. So `fsm.output` does **not** need an
in-transaction dispatch hook either: the handler receives the outputs *after* the
walk commits and dispatches from there, post-commit, exactly like entity events.

**Consequence:** both event families converge on the same dispatch model —
**fire after the originating operation commits, from the handler, asynchronously.**
We do not touch `FsmWalkInTx` or the storage `Create` path internals. We add a
call at the post-commit point in each handler.

### 1.3 Reusable infrastructure already exists

- `pkg/gc`'s `Worker` (`gc.go:40`) — interval-driven background worker with a
  clean Start/Stop lifecycle. Precedent for any background side of the dispatcher
  (e.g. a future retry sweeper — though retry is Part 2).
- `oqlJobs` (the OQL `JobManager`) — precedent for async job submission and for
  the `oql` action type (we already have a sync OQL execution path to call).
- `writeError` / `writeJSON`, the tenant-scoped store resolution
  (`getStore` / `getTenantIDNumeric`), and the `gen_definitions`-style table +
  handler pattern from S5/S10 — directly reusable for the event-def table and
  its eight management endpoints.

---

## 2. The async-after-commit window (the one real hazard)

Because dispatch is post-commit and async, there is a window between "operation
committed" and "action executed" in which:

- the action runs against state that may have changed again (a second update
  lands before the first event's webhook fires); and
- a process crash after commit but before dispatch loses the event (no
  durable queue in Part 1).

**This is acceptable for Part 1 and must be documented, not hidden.** The plan
already defers durable delivery (dead-letter, replay, retry) to Part 2. The S9
contract is explicitly *at-most-once, best-effort, single attempt*. The strategy
is to make this honest:

- The event payload captures the entity/output state **at firing time** (snapshot
  into the event), so the action sees what triggered it, not whatever state exists
  when the webhook finally fires.
- The delivery log records the attempt and its outcome, so a dropped delivery is
  at least *observable* after the fact.
- Documentation states the at-most-once contract plainly at the point of use.

We do **not** try to close the window in Part 1 (that is the durable-queue work of
S16/S17). We make it visible and bounded.

## 3. Sync downgrade (decided by the plan, mechanised here)

An event def may declare `"execution": "sync"`. Part 1 accepts, stores, and
honours it — but executes async regardless, returning `X-Executed-As: async`.
Rationale (from the plan): true sync execution must run the action inside the
triggering transaction, which interacts with the SQLite busy-timeout; that is
Part 2. Our job in S9 is only to (a) accept and persist the field, (b) always
downgrade, (c) emit the header so the caller knows. No sync path is built.

---

## 4. Component inventory

What S9 ships, mapped to where it lives:

| Component | Location (new unless noted) | Notes |
|-----------|------------------------------|-------|
| `event_defs` table | `storage/sqlite.go` schema | `(tenant_id, id, event_type, action_type, config_json, execution, created_at)` |
| `event_delivery_log` table | `storage/sqlite.go` schema | `(tenant_id, id, event_def_id, event_type, status, detail, attempted_at)` |
| 8 management endpoints | `v2_event_handlers.go` | create/list/get/update/delete/log + (test → 501 in Part 1) |
| Event source: entity CRUD | hook in `handleCreate`/`Update`/`Delete` | post-commit call; one line + the dispatch entry |
| Event source: `fsm.output` | hook in the walk handler | uses returned `WalkResult.Outputs` |
| Dispatcher | `event_dispatch.go` | match event defs → substitute templates → run action async → log |
| Action: `webhook` | `event_dispatch.go` | async HTTP POST, one attempt, outcome logged |
| Action: `oql` | `event_dispatch.go` | async, via existing OQL sync-execute path |
| Template substitution | `event_dispatch.go` | `{{event.*}}` + `{{gen:name}}` (calls generators) |
| Error codes | `pkg/errors` | `XOLU-EV001/002/003` |

---

## 5. Build order (batched, each ending green)

Sequenced so each batch is independently verifiable and the risky parts come
*after* the safe scaffolding is proven — the same discipline that worked for S10.

**Batch 1 — storage + registry, no dispatch yet.**
Add the two tables; add the eight management endpoints (create/list/get/update/
delete/log; `test` returns 501). No firings, no dispatch. Verifiable in
isolation: define an event def, read it back, list, delete. This is the S5/S10
named-definition pattern again — low risk, establishes the surface.

**Batch 2 — the dispatcher, driven by a manual/test firing.**
Build match + template substitution + the two action types + delivery-log
writing, but invoke it from a test seam (or the `test` endpoint promoted from
501), *not* yet from the CRUD/walk paths. This isolates the dispatcher's logic
(matching, templating, webhook I/O, OQL execution, logging) from the event
wiring, so each is debugged separately. Webhook I/O and template substitution are
the parts most likely to surprise; prove them here against a controlled firing.

**Batch 3 — wire the entity events.**
Add the post-commit dispatch call to `handleCreate`/`handleUpdate`/`handleDelete`.
One insertion point each, all post-commit (per §1.1). Verify end-to-end: create an
entity → event def's webhook/oql action fires → delivery log records it.

**Batch 4 — wire the `fsm.output` event.**
Add the post-commit dispatch from the walk handler using `WalkResult.Outputs`
(per §1.2). This is the highest-value wire — it's the bridge that makes FSM
outputs actuate. Verify: a walk emitting a Mealy output dispatches an event def.

**Batch 5 — sync downgrade + headers + docs + error codes.**
Mechanise the `X-Executed-As: async` downgrade, finalise `XOLU-EV001/002/003`,
and document: the eight endpoints, the at-most-once contract, the async-after-
commit window, the sync downgrade, and what is deferred to Part 2.

**Throughout:** mutation-test the load-bearing pieces (does the dispatcher
actually fire on a matching event? does a non-matching event correctly *not*
fire? does a webhook failure get logged rather than swallowed?). A green test
that passes when dispatch is disabled is decoration — the S10 audit lesson.

---

## 6. Explicitly out of scope for S9 (deferred, do not build)

From the plan, recorded here so they are not accidentally pulled in:
sync execution (the real in-transaction kind); retry and backoff; dead-letter and
replay; `POST /api/v2/event/{id}/test` beyond a stub; and the additional event
types (`fsm.walk`, `sulpher`, `graph.edge.*`, `ts.appended`,
`fsm.entered/exited/terminal`) and their action types. These are S13–S17.

---

## 7. Open questions to resolve before Batch 3

Two questions the code reading surfaced that are cheap to answer now and
expensive to discover mid-build:

1. **Update/delete event payloads.** `entity.created` snapshots the new entity.
   What does `entity.updated` carry — the new state, the old state, or a diff?
   And `entity.deleted` — just the ID, or the last-known state? The plan does not
   say. Decide before Batch 3; it shapes the event payload schema.
2. **Event ordering with the FSM-walk-in-commit path.** A `/commit` can carry
   an `fsm_walk`. If that walk emits an output *and* the commit creates an entity,
   two events fire from one request. Confirm the ordering is defined (or
   explicitly unordered) and documented, so a subscriber can reason about it.

Neither blocks Batch 1 or 2. Both must be settled before Batch 3 wires real
events.
