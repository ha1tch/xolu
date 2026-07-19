# Known Issues and Intentional Limits

Version: 0.16.1
Last reviewed: 2026-07-18

Intentional limits, invariant boundaries, and recorded decisions — what is
true of the product now **by design**. This document is not a work register:
open actionable items live in `docs/TRACKING.md`; closed items and resolved
defects are recorded append-only in `docs/RESOLVED.md`.

There are currently **no known open defects**. When open defects exist, they
are indexed here with one line each and tracked in `docs/TRACKING.md`.

---

## Part 1 (1.0 preview) — event model limitations

These are not defects; they are the deliberate boundaries of the Part 1 event
model. The full design and the deferred items are in `docs/EVENT_PENDING.md`.

- **Asynchronous only.** Event firings dispatch after the originating
  transaction commits, never inside it. A def may declare `"execution": "sync"`;
  it is accepted and stored but always runs async (the response carries
  `X-Executed-As: async`). True synchronous, in-transaction, roll-back-on-failure
  execution is deferred.
- **At-most-once, single attempt.** A firing attempts its notification delivery
  once. There is **no retry, no backoff, no dead-letter, and no replay**. A
  failed delivery is recorded in the delivery log
  (`GET /api/v2/event/def/{id}/log`) but not retried.
- **No ordering guarantee.** Events arising from a single request (e.g. a
  `/commit` that writes an entity and runs an output-producing walk) are
  delivered unordered; a subscriber must not assume one arrives before another.
- **Crash window.** There is a brief window between commit and dispatch in which
  a process crash loses the firing — there is no durable queue in Part 1.
  Making "delivered" mean "provably delivered" (a reconciliation sweep) is a
  Part-2 concern, relevant to critical-entity backup defs.
- **Not all event types / actions are wired.** Live event types:
  `entity.created/updated/deleted`, `fsm.output`, `fsm.step`, `commit.applied`.
  Live actions: `webhook`, `oql`. The deferred types
  (`graph.edge.*`, `fsm.entered/exited/terminal`, `ts.appended`, `meta.expired`)
  and actions (`sulpher`, `fsm.walk`) are documented in `docs/EVENT_PENDING.md`.

### Recorded decisions and deferrals (event model)

- **`event_latch_source`** is element-level for FSM events
  (`fsm/<from>:<input>:<to>`) and intentionally kind-level for `commit.applied`
  (a commit has no single object). Author-named transitions are deferred.
  (`EVENT_PENDING.md` §6b.)
- **Federation-consistent subjects/references** (the `xolu.` subject namespace,
  three-level dotted subjects with wildcard matching, and `LocalRef`-consistent
  references for nolu interoperability) are designed but **not implemented** —
  the shipped flat event types remain in force. See `docs/NOLU_EVENTS.md`. The
  naming conventions there (dotted subjects, `xolu` root, field-based references)
  are settled; the subject-matching reshape is a separate, post-1.0 effort.

---

---

## Time handling — invariant boundaries (not defects)

The system-wide time invariant and its package (`pkg/xolutime`, alias `ot`) are
documented in `docs/TIME_HANDLING.md`. The following are the deliberate limits of
that enforcement, recorded so a passing build is read honestly.

- **The lint guard is a regression catcher, not a proof.** `TestNoBareWallClock`
  flags bare `time.Now()` flowing into persisted/compared wall-clock values, by
  syntactic shape: direct field set, struct literal, the address-of-temp idiom
  (`x := time.Now(); rec.At = &x`), and an explicit list of persisting
  constructors. It does **not** track dataflow across functions, does **not**
  catch a `time.Now()` passed to an arbitrary (unlisted) function that stores its
  argument, and is name-based on the persisted-field list. A green result means
  "no known-shape regression," not "the tree is provably UTC-clean." Full
  coverage would need an SSA-based `go/analysis` pass, deferred.

- **OQL/FSM evaluator time builtins follow T-SQL local/UTC contract (resolved
  2026-06-22).** `pkg/fsm/eval` and `pkg/qs/scalar.go` implement the T-SQL
  current-time builtins. By T-SQL contract `GETDATE()`/`SYSDATETIME()` return
  **local** server time and `GETUTCDATE()`/`SYSUTCDATETIME()` return **UTC**; the
  local ones are therefore deliberately *not* routed through `xolutime` (which has
  no local-now), while the UTC ones now source from `ot.Now()`. This is marked in
  the code at each site. The lint's `persistingConstructors` list stays empty:
  `NewDateTime` is correctly local in the local builtins, so flagging it wholesale
  would be wrong. No further action.

> The `cal` requirement that an upstream layer retain wall-clock intention and
> re-issue a `move` on a zone-rule change is **not** a known issue — it is a
> design requirement of `cal` (requirement R-T1), stated in
> `docs/proposals/cal-rest-api.md`. It is recorded there, not here.

- **`ts` accepts zone-naive timestamps; `cal` rejects them (deliberate
  divergence).** The `ts` ingestion parser (`parseTSTime`, `pkg/server/ts_handlers.go`)
  accepts an RFC 3339 string with no zone designator and interprets it as UTC, for
  backward compatibility with existing `ts` clients. `cal`'s `ot.Parse` rejects the
  same input (R-T1 / `docs/TIME_HANDLING.md`). This is a *policy* difference, not a
  storage bug: `ts` normalises the parsed value to UTC and the codec encodes
  `UnixNano()` (zone-invariant), so stored keys are correct regardless. Aligning
  `ts` to reject zone-naive input would be a **breaking API change** and is left as
  a deliberate decision, not an oversight. The `ts`/rollup range-query parsers
  (`from`/`to` in `ts_handlers.go` and `ts_rollup_handlers.go`) likewise accept
  zone-naive input; they are query bounds reduced to `UnixNano`, so the offset is
  immaterial to the scan.

---

## `cal` design — recorded decisions

- **Intent preservation vs grid occupancy (recorded 2026-07-18).** Booking
  spans store the caller's exact instants in the SQLite record (H1); the
  bitmap index (H2) derives occupancy by conservative outward rounding
  (floored start quantum, ceiled end — `SpanDays`). A 9:57–10:15 booking
  occupies the 09:55–10:15 quanta but is stored, returned, and displayed
  as 9:57–10:15. The design-stage "3-bit minute modifier" (add/subtract
  up to 4 minutes to recover true start time from a bitmap-centric
  record) was superseded by the H1/H2 split and never reached code: the
  offset is recoverable by arithmetic from the exact stored instant, at
  full precision, for zero stored bits. Pinned by
  `TestSpanIntentPreservedOffGrid`. (Distinct from the per-calendar grid
  `delta` of the codec proposal §2.1a, which phase-shifts a whole
  calendar's grid and remains unimplemented design intent.)

- **Table convention (recorded decision).** `cal`'s tables follow the **fsm
  family convention** (`tenant_id` column + `PRIMARY KEY (tenant_id, ...)`,
  unprefixed table names), not the prefixed per-tenant data-table convention used
  by the entity/graph blob tables (`tXXXX_nodes`, no `tenant_id` column). `cal`'s
  records are definition/instance/history rows analogous to
  `fsm_definitions`/`fsm_machines`/`fsm_history`, not high-volume blob/graph data,
  so they sit in the v2 staged-migration schema (stage S11) alongside the fsm and
  meta tables. This realises the GATE-3 "tenancy follows xolu config" decision: the
  tenant_id column discriminates in shared-file mode and is a constant 0 in
  per-file mode, the same as every other v2 table.
