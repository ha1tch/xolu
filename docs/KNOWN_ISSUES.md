# Known Issues and Intentional Limits

Version: 0.27.2
Last reviewed: 2026-08-04

Intentional limits, invariant boundaries, and recorded decisions — what is
true of the product now **by design**. This document is not a work register:
open actionable items live in `docs/TRACKING.md`; closed items and resolved
defects are recorded append-only in `docs/RESOLVED.md`.

**Known open defect:** `chronicle.Engine.Append` has no locking around its
read-modify-write — a lost-update race under concurrent same-bucket appends,
currently masked for its only production consumer (bal's rollup plane) by
SQL commit latency naturally spacing the calls. Tracked as T-66 in
`docs/TRACKING.md`.

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

- **Test-file timestamp constants overflow int on 32-bit (recorded
  2026-07-20).** A few auth test fixtures use a far-future Unix timestamp
  constant (`4102444800`, year 2100) in `int`-typed map literals
  (`blob_scoped_auth_test.go`, `tenant_scoped_auth_test.go`). These
  overflow `int` when the *test suite* is compiled for a 32-bit target.
  This does NOT break CI: `cross-build.yml` compiles only `./cmd/...`
  (no tests), and `ci.yml` runs the suite on a 64-bit runner. It would
  only surface if someone ran `go test` on a 32-bit host. Low priority;
  fix by typing those constants `int64` when convenient. Distinct from
  the production TimelineID width bug fixed the same day (that one broke
  the cross-build; this is test-only).

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

## Tenant ID width — recorded decision (closed 2026-08-01)

**Decision: `TenantID` stays `uint16` (max 65,535 tenants per instance).
Not revisited unless a named consumer's projected tenant count
approaches the ceiling with a concrete timeline, not a hypothetical
one.**

This closes a question `SUBSTRATE_DEVELOPMENT_PLAN.md` §1 had already
answered in its own rationale text but that a 2026-08-01 tracking pass
briefly reopened by mistake (see `docs/SUBSTRATE_TRACKING.md` §3's
second discipline note — an item labelled "tenant ID → uint32" was
misread as targeting the tenant count, when the plan's own words
already say tenant ID stays `uint16` and the actual widened field is
`/ts`'s `TimelineID`, a per-tenant object count, already `uint32` since
v0.16.3).

**Reasoning, reconfirmed 2026-08-01 against `nolu` (github.com/ha1tch/nolu,
v0.7.9) directly, not just against the plan's own prose:**

- A single xolu instance hits its throughput or storage ceiling long
  before 65,535 tenants for the workloads this project targets.
- If that ceiling is ever reached, the answer is a second xolu
  instance, not a wider ID — and that answer is not hypothetical.
  `nolu`'s own `LocalRef.TenantID` (`pkg/identity/identity.go`)
  independently encodes `uint16`, arrived at separately from xolu's own
  design — two independent passes landing on the same ceiling is
  stronger evidence than either alone.
- `nolu` ships a real, tested tenant hotswap mechanism
  (`pkg/hotswap`, ~2,500 lines, unit + e2e coverage): a full state
  machine (`REQUESTED → PREPARING → QUIESCING → MIGRATING → VALIDATING
  → CUTTING_OVER → COMPLETE`, with rollback) that live-migrates a
  tenant to a new xolu instance with a brief write-outage window,
  GlobalIDs cut over atomically so nothing client-facing changes. This
  is operational tooling, not an architectural intention on paper.

**What this decision does not claim:** that 65,535 is provably enough
forever, or that xolu has ever had a real deployment approaching it.
It closes the question of whether to pre-emptively widen `TenantID` —
the answer is no, because the actual escape valve (federation via
nolu) already exists and works, so there is nothing to hedge against by
widening speculatively.

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


## `bal` design — recorded decisions

- **Journal stays SQL-resident; Pebble-native was assessed and set aside
  (recorded 2026-07-28).** `bal`'s admission guard is genuinely complex —
  `transferInTx`'s guarded `UPDATE` spans three tables (`balances`,
  `accounts`, `journal`) in one statement: resolving `account_key` from
  `account_id`, checking `postable`, computing the new balance against a
  dynamically-read `floor`/`ceiling`, and a correlated `NOT EXISTS`
  subquery against the journal itself refusing backdated writes unless
  the account's policy allows them. SQL gives all of that as one atomic
  round-trip with the engine's own locking making it race-free by
  construction. Checked directly against the vendored Pebble source
  (`cockroachdb/pebble@v1.1.5/batch.go`): every `Batch` write method
  (`Set`/`Merge`/`Delete`/`DeleteRange`/`RangeKeySet`) is unconditional —
  no CAS, no unfamiliar-value guard, nothing. A Pebble-native journal
  would need the same guard built from hand-rolled application-level
  locking (per-account, ordered consistently across both legs of a
  transfer to avoid deadlock) plus an atomic batch for the durable
  write — buildable, but a genuinely new concurrency-control design, not
  a storage-engine swap of an interface that was already engine-agnostic
  (contrast the rollup plane, T-62, which needed exactly that swap and
  nothing more).
  The throughput case for switching is weaker than "Pebble is faster"
  suggests: `bal`'s own cited "~5–6k/s per tenant"
  (`bal-conservation-primitive.md`) is an explicitly-flagged *proxy*
  borrowed from `cal`'s stress harness, not bal's own measured code, and
  SQLite's single-writer ceiling is **per tenant file** in this
  substrate (each tenant already gets its own file) — the doc's own
  throughput line notes twenty busy tenants already add roughly
  linearly to ~100k+/s. The only throughput case Pebble-native actually
  wins on principle is finer-than-tenant-file locking (per-account
  rather than per-file), which is a real, distinct win — but it is a
  concurrency-granularity argument, not a "Pebble beats SQLite" one, and
  a tenant-scoped lock (the natural first cut, matching
  `dxp.MemCache`'s own granularity) would likely land at roughly the
  same ceiling SQLite already gives for free.
  What a migration would give up, concretely: the ledger's existing OQL
  queryability, the relational-model RI infrastructure it inherits for
  free, and a guard that is one readable, auditable SQL statement, in
  exchange for hand-written lock-and-batch logic in the one primitive
  where a subtle race means money moving wrong.
  **Not pursued now.** Revisit only against a concrete, measured
  throughput problem in bal specifically — not a general instinct that
  Pebble is faster, and not without a per-account (not per-tenant)
  locking design done first, since that is where the actual upside
  would have to come from.

- **PruneJournal is Go-only, not exposed over HTTP (recorded
  2026-07-28).** Item 16's prefix-collapse retention
  (chronicle-substrate.md §4b) ships as `bal.Store.PruneJournal`, called
  from Go (tests, and `cmd/iolu`'s `bal prune` command) but with no
  `/bal/prune` REST route. Deliberate, not an oversight: pruning is
  irreversibly destructive (unlike `bal/close`, which only writes —
  seal-frontier enforcement blocks bad writes going forward but changes
  nothing already committed), reads more like an operations action than
  a routine API call a client should be able to trigger casually, and
  `cmd/iolu` already owns the equivalent-shaped `db check`/oracle
  surface. If usage patterns later show a real need for programmatic,
  in-process triggering (e.g. an automated retention job running
  inside the server rather than as a separate `iolu` invocation), add
  `POST bal/prune` then, with an explicit confirmation/dry-run
  parameter shaped for that risk — don't retrofit HTTP access onto a
  destructive operation without designing that safety surface
  deliberately.

## Referential integrity — recorded decisions and stage boundaries

- **Restrict check-then-act window (recorded 2026-07-20, wave 2 stage 2).**
  Stage-2 restrict enforcement reads inbound edges (via the graph) and
  then issues the delete (via the store) as **two separate operations**,
  not one transaction. @R §5 requires the enforcement read to run inside
  the delete's transaction for the single-writer property to serialise a
  delete against a concurrent referrer-create; stage 2 does not yet do
  this, so a check-then-act window is **open by design** at this stage.
  Under true multi-core parallelism a referrer created in that window can
  leave a dangling reference. This is a known, bounded limitation of the
  safety half shipping first — restrict correctly refuses in the
  sequential and common cases, which is what the initial consumer (junior
  dev teams onboarding from SQL) needs, while the concurrency-tight
  version follows. **Fix path:** stage 3 brings transactional edge-table
  enforcement and the @R05a composite-FK pushdown, which close the window
  database-natively. **Guard:** G-12 (dormant-guards table) measures the
  window now and converts to an assertion when stage 3 closes it.

  **Architectural constraint discovered 2026-07-20 (a first transactional
  fix attempt, reverted):** the naive fix — read inbound referrers from
  the SQL edge table (`t<X>_graph`) inside the delete's transaction — does
  NOT work as-is, because on the server's write path the SQL edge table is
  **not the authoritative edge state**. The server maintains edges in the
  in-memory FlatGraph via `updateGraph` (a "derived index"), and the
  store's `syncGraphEdges` SQL path is not invoked on that flow, so the
  edge table is empty at delete time and a transactional SQL read finds no
  referrers. Closing the window therefore requires one of: (a) making the
  SQL edge table authoritative and synced on every write (then the
  transactional read works), or (b) spanning the in-memory graph's lock
  and the store delete under one atomicity boundary across the two
  subsystems. Both are larger than stage 3 as originally scoped; whichever
  is chosen must be designed before code. Until then the stage-2 window
  stands, measured by G-12.

  **FALSIFIED-THEN-RECLOSED 2026-07-21: the delete-side fix below was
  necessary but not sufficient — GitHub CI's multi-core run failed the
  assertion (1/8 dangling). The residual window was the CREATE side:
  the REF target check ran against the in-memory graph before the write
  transaction opened. Closed the same day by an in-transaction
  target-existence check in syncGraphEdges (@R02.3 shipped early,
  XOLU-RI003), covering create/update/patch/save and both batch paths.
  With both halves in-transaction the pair is linearisable under
  serialised writers in either commit order.** The original (partial)
  resolution record follows.

  **RESOLVED 2026-07-21 — window closed by the in-transaction check; the
  2026-07-20 constraint was re-audited and found inverted.** The code's
  own comment (handlers.go, post-commit graph update) states the truth:
  *the SQL edge table is updated atomically in the store's transaction*
  (`syncGraphEdges` runs inside `createInner`/`updateInner`/`patchInner`);
  the in-memory FlatGraph is the best-effort derived cache, not the
  authority. The 2026-07-20 "edge table empty" observation traced to
  **adapted entities**, whose write path never populates the edge table —
  their REFs live decomposed in `REF_{field}_entity/_id` columns instead.
  Neither option (a) nor (b) was needed. The shipped fix
  (`SQLiteStore.DeleteWithRestrict`, optional capability interface):
  referrer check inside the delete's own transaction (@C04a), two-pronged —
  edge-table SELECT for blob referrers, spec-driven REF-column probe
  (IsREFEntity/IsREFID) for adapted referrers. Zero write-path cost. The
  handler's in-memory pre-check remains as a cheap fast path; the store
  check is authoritative. Prong tests:
  `pkg/storage/restrict_tx_test.go` (blob, adapted, mixed). REF
  compose/decompose invariants hardened the same day
  (`pkg/storage/ref_invariants_test.go`) so the two REF pipelines cannot
  silently diverge under the check. G-12 remains registered for a
  real-silicon multi-core run to confirm the closure under true
  parallelism (single-core cannot open the window; not evidence).

- **CascadingDelete flag coexistence (recorded 2026-07-20).** The legacy
  `CascadingDelete` config flag and stage-2 restrict enforcement coexist
  without conflict: restrict is checked first (a restrict-referrer blocks
  the delete with XOLU-RI001 regardless of the flag), and only if no
  restrict-referrer exists does the flag's cascade-or-plain-delete path
  run. The flag is **not** retired at stage 2 — @R02.4 retires it only
  after stage 3 replaces its coarse binary (cascade-all / restrict-all)
  with per-x-ref policy. Until then it remains the mechanism for cascade
  behaviour, now gated behind restrict.


## Dormant guards — verification records

Per Part 3 §8 of the working agreement: every verification that does not
run in the default test invocation is registered here, with its gating
condition and last-exercised environment. A shipped guard that never
runs guards nothing; the release gate refuses unexercised guards, or
records the skip explicitly.

Convention: **Last exercised** is `YYYY-MM-DD env:<where>` — env values
`sandbox` (single-core Linux, this project's default CI runner class),
`m1` (the team's Apple M1, 8-core), `gh-runner` (GitHub Actions
`ubuntu-latest`, multi-core). Race-class tests that only manifest
under true parallelism require `m1` or `gh-runner`.

### G-01. cal seal stress (`pkg/cal/seal_stress_local_test.go`)

- **Gate:** build tag `stress`; env `XOLU_TEST_SEAL_STRESS_*` (six knobs — trials, workers, bookings, ops-per-worker, calendars, days).
- **Hardware:** multi-core essential — the T-34 defect class this guards only manifests under true parallelism.
- **Invocation:** `go test -tags stress ./pkg/cal/ -run TestSealFrontier_Stress -v` (adjust env for scale).
- **Last exercised:** 2026-07-18 env:m1 — 194.6 s stress tier + 800 k extended-scale ops at 5.9–6.3 k ops/s. Zero races.

### G-02. Client integration suite (`pkg/client/integration_test.go`)

- **Gate:** build tag `integration`.
- **Hardware:** any; boots an in-process server, no external services.
- **Invocation:** `go test -tags integration ./pkg/client/ -count=1`.
- **Last exercised:** 2026-08-04 env:sandbox, go1.26.5, T-153's own verification — run repeatedly for the FSM definition write surface (T-153), both -short and -tags integration, all passing Previous: 2026-08-04 env:sandbox, go1.26.5, this session's own repeated runs — run repeatedly throughout this session (blob/export/promotion feature work), both -short and -tags integration, all passing

### G-03. Fuzz targets

Injection-class property tests written during the adversarial security
pass; run as native Go fuzz targets. Not race-shaped — each is a
correctness envelope over parser/validator input space.

| ID | Target | File | Invocation |
|---|---|---|---|
| G-03a | JSON tokeniser | `pkg/jsonic/fuzz_tokeniser_test.go` | `go test -run=^$ -fuzz=FuzzTokenise -fuzztime=60s ./pkg/jsonic/` |
| G-03b | OQL field-name validator | `pkg/oql/fuzz_validatefield_test.go` | `go test -run=^$ -fuzz=FuzzValidateFieldName -fuzztime=60s ./pkg/oql/` |
| G-03c | Blob SHA computation | `pkg/blob/fuzz_sha_test.go` | `go test -run=^$ -fuzz=FuzzBlobSHA -fuzztime=60s ./pkg/blob/` |
| G-03d | JWT parse+validate | `pkg/authmw/fuzz_jwt_test.go` | `go test -run=^$ -fuzz=FuzzParseAndValidateJWT -fuzztime=60s ./pkg/authmw/` |
| G-03e | Scalar functions | `pkg/qs/fuzz_scalar_test.go` | `go test -run=^$ -fuzz=FuzzScalarFunctions -fuzztime=60s ./pkg/qs/` |
| G-03f | FSM eval guard | `pkg/fsm/eval/fuzz_eval_test.go` | `go test -run=^$ -fuzz=FuzzEvalGuard -fuzztime=60s ./pkg/fsm/eval/` |

- **Last exercised (all G-03x):** 2026-07-21 env:sandbox (Go 1.26.x, single-CPU) — all six targets 60s each, PASS, zero crashers, no repro artifacts. (Fuzzing is input-space work, so single-CPU exercise is valid evidence for this class, unlike the race guards.) Previous exercise: the v0.9.7-era adversarial audit.

### G-11. cal Move race harness (`pkg/cal/move_race_test.go`)

- **Gate:** none; runs by default. But like the T-34 confirm race, it only *fires* under multi-core — single-core sandbox runs are always green regardless of correctness.
- **Hardware:** multi-core essential; the T-35 defect class only manifests under true parallelism.
- **Invocation:** `GOMAXPROCS=8 go test ./pkg/cal/ -run TestConcurrentMove -count=20 -race`.
- **Last exercised (pre-fix reproduction):** 2026-07-19 — T-35 reproduced under `-race`, single-CPU host, 2 winners into one window. Sufficient to confirm the defect exists.
- **Last exercised (post-fix, real silicon):** 2026-07-20 env:m1 `GOMAXPROCS=8 -race -count=20` — 10.8 s, green. T-35 fully verified.

### G-13. bal admission race harness (`pkg/bal/admission_race_stress_test.go`)

- **What it guards:** the transfer admission CAS (@B06, T-34 discipline):
  N goroutines contending for one near-floor unit — exactly one wins,
  winners + refusals == N, balance never below floor, conservation and
  the chain triple intact throughout.
- **Gate:** build tag `stress`. Canonical invocation:
  `GOMAXPROCS=<cores> go test -tags stress ./pkg/bal/ -run TestBalAdmission_Race -count=20 -race`
- **Hardware:** meaningful evidence requires multi-core; single-core
  passes are weak for admission races (T-34's own history).
- **Environment contract:** WAL + busy_timeout (house defaults) only.
  The harness first exposed WAL's read→write snapshot invalidation
  (SQLITE_BUSY past the busy handler) in a read-first Transfer; the fix
  was structural, not contractual — Transfer is now WRITE-FIRST (the
  guarded UPDATE with subquery-bound accounts is the transaction's
  opening statement), verified queueing on plain deferred transactions.
- **Last exercised:** 2026-07-21 env:sandbox (single-CPU, -race,
  count=5, 32 claimants) — PASS; weak evidence per above. **2026-08-02:
  the team's first real multi-core attempt (env:m1) never ran** — a
  sibling stress-tagged file in the same package,
  `dxp_cross_path_race_stress_test.go`, failed to build (stale
  `Reserve` call, missing the `participantID` argument T-109 added;
  invisible until now because `-tags stress` is never part of a normal
  build). Fixed same session, filed and closed as T-133 — see
  `docs/RESOLVED.md`. **2026-08-02 env:m1, re-run against the fix**
  (`GOMAXPROCS=8 go test -tags stress ./pkg/bal/ -run
  TestBalAdmission_Race -count=20 -race`) — **PASS, 53.336s, real
  multi-core.** Owed status closed for real: this guard has now
  actually executed under true parallelism, not just been reproduced
  and inferred-fixed on single-core evidence. **2026-08-03 env:m1,
  re-run unprompted alongside G-17's own confirmation** (same
  invocation) — **PASS, 48.518s.** This session's `pkg/bal/dxp_adapter.go`
  change (T-138) touches Reserve/Validate/Execute in the same file as
  this guard's own admission CAS; re-confirms no regression to the
  adjacent path.

### G-14. loc admission race harness (`pkg/loc/admission_race_stress_test.go`)

- **What it guards:** the leaf/fence capacity CAS and multi-target
  atomicity (loc-02-implementation.md Stage 2, T-115): N goroutines
  contending for one near-ceiling leaf (`TestLocAdmission_Race`) and,
  separately, for a shared near-ceiling fence while a leaf move rides
  along in the same transaction (`TestLocAdmission_Race_MultiTarget`)
  — exactly one wins each time, winners + refusals == N, and (for the
  multi-target case specifically) the leaf's own count matches the
  number of actual winners exactly, never winners+refusals, which is
  what a partial-application bug under contention would produce.
- **Gate:** build tag `stress`. Canonical invocation:
  `GOMAXPROCS=<cores> go test -tags stress ./pkg/loc/ -run TestLocAdmission_Race -count=20 -race`
- **Hardware:** meaningful evidence requires multi-core; single-core
  passes are weak for admission races, same as G-13's own history.
- **Environment contract:** WAL + busy_timeout (house defaults) only.
  This harness reproduced G-13's own historical failure mode on its
  first sandbox run: `Move` originally resolved `location_id` via a
  preceding `SELECT` before the guarded `UPDATE`, a read-first shape
  that hit WAL's snapshot invalidation (`SQLITE_BUSY` past the busy
  handler) under 32-way contention. Fixed the same way `bal.Transfer`
  was: `Move` is now WRITE-FIRST — the leaf entry CAS, with
  `location_id` resolved via subquery, is the transaction's opening
  statement; diagnosis of *why* a refusal happened (unknown location
  vs. at capacity) runs only on the failure path, after the write.
- **Last exercised:** 2026-08-01 env:sandbox (single-CPU, -race,
  count=5, 32 claimants, both tests) — PASS; weak evidence per above.
  **2026-08-02 env:m1** (`GOMAXPROCS=8 go test -tags stress ./pkg/loc/
  -run TestLocAdmission_Race -count=20 -race`) — **PASS, 109.276s, real
  multi-core.** This closes the "owed" status: the T-34-class defect
  this guards has now been exercised under true parallelism, not just
  reproduced-then-assumed-fixed on single-core evidence.

### G-15. obj containment/capacity race harness (`pkg/obj/containment_race_stress_test.go`)

- **What it guards:** the universal cycle-safety guard and the
  `max_count` capacity CAS (obj-00-design.md §5/§7, T-120): concurrent
  cycle-construction attempted from multiple directions at once —
  `TestObjContainment_CycleRace` (a direct 2-node race: many goroutines
  racing `a→b`, many others racing `b→a`, simultaneously; at most one
  direction may ever succeed, never both) and
  `TestObjContainment_TransitiveCycleRace` (a 3-node chain already
  established, many goroutines racing to close the loop while many
  unrelated, legal moves happen concurrently in the same container) —
  plus `TestObjCapacity_CountRace`, the identical shape to G-13/G-14's
  own admission-race proof, applied to `obj_subjects.cur_count`.
- **Gate:** build tag `stress`. Canonical invocation:
  `GOMAXPROCS=<cores> go test -tags stress ./pkg/obj/ -run TestObjContainment_CycleRace -count=20 -race`
  (also run `TestObjContainment_TransitiveCycleRace` and
  `TestObjCapacity_CountRace` the same way).
- **Hardware:** meaningful evidence requires multi-core; single-core
  passes are weak for admission/cycle races, same as G-13/G-14's own
  history.
- **Environment contract:** WAL + busy_timeout (house defaults) only.
  This harness reproduced G-13/G-14's own historical failure mode on
  its first sandbox run: `MoveToContainer` originally checked subject/
  container existence and walked the cycle check *before* any write —
  real `SQLITE_BUSY` failures under 32-goroutine contention, not
  assumed. Fixed the identical way `loc.Move`/`bal.Transfer` were:
  the `max_count` CAS is now the transaction's opening statement;
  diagnosis of *why* a refusal happened (container never attached vs.
  genuinely at capacity) runs only on the failure path, after the
  write, mirroring `loc.diagnoseLeafRefusal` exactly.
- **Last exercised:** 2026-08-02 env:sandbox (single-CPU, -race,
  count=10, 16 goroutines per direction/32 claimants) — PASS, all
  three tests, 10/10 runs each. Weak evidence per above. **Owed:**
  multi-core exercise (operator or CI stress lane).

### G-16. dxp coordinator adversarial race (`pkg/server/v2_obj_adversarial_test.go`)

- **What it guards:** two real, confirmed data races in foundational
  `dxp`/storage coordinator code, found via `/obj` adversarial testing
  (T-135) but neither specific to `/obj` — both affect every
  primitive's own `dxp` transactions. (1) `SQLiteStore.dxpClaims`, a
  shared-instance field read/written across concurrent HTTP requests,
  now `atomic.Pointer[dxp.MemCache]`. (2) All six
  `cache.ConfirmTxn`/`ReleaseTxn` call sites in `dispatchPhased`
  (`v2_dxp_dispatch.go`) were missing the lock both functions' own
  doc comments require ("requires the caller to hold tenant's lock")
  — a systemic bug across the phased path's own success, failure, and
  torn-commit branches, not one isolated spot. `TestObjAdversarial_
  EnsureSystemDxpDef_ConcurrentFirstUse` (12-way concurrent `POST
  /obj/promote`, each a real 3-leg `bal`+`entity`+`obj` `dxp`
  transaction) is the harness: the race detector fired on its very
  first run before the fix, silent on repeated re-runs after.
- **Gate:** ordinary build, no tag — `-race` is the gate here, not a
  separate build constraint. Canonical invocation:
  `go test ./pkg/server/ -run TestObjAdversarial_EnsureSystemDxpDef_ConcurrentFirstUse -race -count=5`
- **Hardware:** meaningful evidence for the two fixed races does not
  require multi-core — a data race is a data race under `-race`
  regardless of core count, unlike the admission-CAS races G-13/G-14/
  G-15 guard. Multi-core did matter for the separate contention lead
  this same harness surfaced (T-136) — see G-17, confirmed and closed
  2026-08-03.
- **Environment contract:** WAL + busy_timeout (house defaults) only.
- **Last exercised:** 2026-08-02 env:sandbox (-race, count=5) — PASS,
  race detector silent across all 5 runs post-fix. **Separate lead
  found the same session, since resolved:** 1 of those 5 runs hit a
  60s server-side request timeout on some requests despite the race
  detector staying silent — not a correctness failure. Filed as T-136,
  its connection-pool hypothesis refuted and the real cause (a lock-
  order deadlock) diagnosed, fixed, and confirmed on real M1 hardware
  as T-138 / G-17, closed 2026-08-03.

### G-17. dxp/bal lock-order deadlock (T-138) — multi-core rerun of the reproducer

- **What it guards:** the AB/BA lock-order inversion T-138 diagnosed
  from the team's own M1 goroutine dump: `bal.Adapter.Execute` (holding
  the coordinator's open `*sql.Tx` on the `MaxOpenConns=1` writer
  pool) acquired the tenant `MemCache` lock while `bal`
  Reserve/Validate/Transfer held that lock waiting for the same pool —
  presenting as ~60s full-tenant stalls, 9-of-10 runs on 8 real cores
  vs 1-in-5 in the single-vCPU sandbox. Fixed by moving Execute's
  claim sums to a `pendingTransfer` snapshot (captured at Reserve,
  refreshed at Validate); Execute now takes no tenant lock at all,
  pinned by `TestDxpSnapshot_ExecuteTakesNoTenantLock` (deadlocks by
  construction if the acquisition is ever reintroduced).
- **Gate:** ordinary build, no tag — the dormant part is the hardware:
  the interleaving needs true multi-core parallelism, per the same
  reasoning as G-13/G-14/G-15. A sandbox pass is necessary but
  explicitly NOT sufficient evidence for this guard. Canonical
  invocation (identical to the run that produced the diagnosing dump):
  `GOMAXPROCS=8 go test ./pkg/server/ -run TestObjAdversarial_EnsureSystemDxpDef_ConcurrentFirstUse -race -count=20`
  Pass condition: 20/20, zero 60s-class request timeouts, race
  detector silent.
- **Environment contract:** WAL + busy_timeout (house defaults) only.
- **Last exercised:** 2026-08-03 env:m1 (real Apple M1 silicon,
  the team's own run) — `GOMAXPROCS=8 go test ./pkg/server/ -run
  TestObjAdversarial_EnsureSystemDxpDef_ConcurrentFirstUse -race
  -count=20 -v` — **PASS, 20/20, 15.962s total (~0.8s/run), zero 60s-
  class timeouts, race detector silent throughout.** Against the
  pre-fix log's 9-of-10 failures at 60.5s each on the same hardware.
  Both halves of the guard's own pass condition met — correctness and
  throughput judged acceptable by the team directly, closing the "probe"
  framing our own decision named. T-138 and T-136 closed this release
  (`docs/RESOLVED.md`). Bonus same-session confirmation: G-13 (`bal`
  admission race, `-tags stress`, count=20) also re-run on the M1,
  PASS, 48.518s — not itself re-recorded here since G-13 owns its own
  entry, but noted as further evidence the fix introduced no
  regression to the adjacent admission-CAS path.

### bal: as-of is wrong after a backdated transfer (T-51)

**Status: open defect, present since 0.16.17.** `BalanceAsOf` reads
`nearest checkpoint + intervening buckets`. A transfer dated at or
before an existing checkpoint does not invalidate that checkpoint, so
the checkpoint remains a frozen pre-backdate number and as-of inherits
the error. Reproduced: as-of returned 150 where the journal held 157.

The rollup oracle does **not** detect this — it reconciles bucket sums
against the journal, and both include the backdated entry, so they
agree. No oracle currently verifies checkpoints at all.

**Scope of the wrongness:** the authoritative planes are unaffected.
The journal, `balances`, the chain triple and every guard remain
correct; `BalanceAsOfExact` returns the right answer. Only the derived
fast path is wrong, and only for accounts that have both checkpoints
and backdated entries.

**Workaround until T-51:** call `BalanceAsOfExact` (or
`RebuildRollup` + re-`Checkpoint`) where backdating occurs. Do not
rely on `/bal/asof` for an account that receives backdated transfers.

**Not fixed by sealing.** Sealing refuses entries into closed periods,
which suits a ledger but not domains where backdating is legitimate
(museum accessions, historical inventories). The stale checkpoint is a
bug in both cases.

### G-12. RI restrict race harness (`pkg/server/ri_restrict_race_test.go`)

- **Gate:** none; runs by default and ASSERTS `dangling == 0`.
- **Hardware:** multi-core essential; a single-core pass is vacuous.
- **Invocation:** `GOMAXPROCS=<cores> go test ./pkg/server/ -run TestRIRestrict_Race -count=80 -race` (macOS: `GOMAXPROCS=$(sysctl -n hw.ncpu)`).
- **RESOLVED 2026-07-21.** The repeated multi-core falsifications were NOT a concurrency defect. Root cause: the test harness built its store via the map-based `storage.NewStore`, which silently defaulted the graph subsystem OFF — so `syncGraphEdges` short-circuited and the in-transaction RI enforcement never executed. Every strategy "failure" (serialize, intx-only, serialize-intx; all 1/8–4/80) was the same artifact: no strategy can close a race whose enforcement code is skipped. Production was never affected — it builds stores via `NewStoreFromConfig`, which propagates `GraphEnabled`. Diagnosis was pinned by instrumentation showing zero enforcement calls during a run.
- **Fix:** (1) the map builder now honours `graph_enabled` and defaults it on; (2) graph and timeseries default on generally; (3) the harness store enables graph, matching production; (4) a parity guard in `rebuildRIRegistry` fails loudly (error log; fatal under `XOLU_STRICT_SUBSYSTEMS`) when x-ref policies exist with the graph disabled, so this cannot silently recur. With enforcement actually running, the PLAIN in-transaction check closes the race — verified 0/80 under `-race` on multi-core (macOS, no strategy machinery). All three strategies and their apparatus were removed as dead weight.



### G-04. Full suite with `-race` on multi-core

- **Gate:** none in principle; every test is race-safe, but a full multi-core -race run is not the default because of runtime cost.
- **Hardware:** multi-core essential — single-core Linux (sandbox default) cannot manifest logic races.
- **Invocation:** `go test -race ./... -count=1 -v`.
- **Last exercised:** 2026-07-19 env:gh-runner — surfaced T-39 (blob usage) and T-40 (@SEQ/@GEN global). Both closed same day, verified by two green runs. Best-in-class conscience for this class of defect.

### G-05. Full 16-target cross-build matrix

- **Gate:** every push (`cross-build.yml`) exercises this; strictly speaking a CI check rather than a dormant guard, but recorded here for completeness of the matrix's health record.
- **Hardware:** `gh-runner` cross-compiles all targets from Linux, no per-OS pool.
- **Invocation:** on GitHub push; locally, `for t in <matrix>; do GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 go build ./cmd/{xolu,iolu,xotogen}; done`.
- **Last exercised:** 2026-07-19 env:gh-runner — 15 targets green (windows/386 removed per operator decision; empirical failure, no i386 Windows server use case).

## Guards owed by future work

Per each proposal's testing obligations (@P0 governing references),
the following guards are **specified** and will be **registered here in
the sessions they are written**, per Part 3 §8's rule that
specification and registration are one act:

- **G-06 bal near-floor race harness** — owed by @B06; wave 4.
- **G-07 RI restrict race** — owed by @R07; wave 2.
- **G-08 dxp outcome-uniqueness race** — owed by @D11; wave 5.
- **G-09 dxp crash-recovery fault injection** — owed by @D11; wave 5.
- **G-10 dxp degradation-equivalence** — owed by @D06; wave 5.

None of these tests exists yet. They are registered as owed so the
release gate can refuse to promote a version whose owning wave has
shipped without them.
