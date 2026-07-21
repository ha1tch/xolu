# Known Issues and Intentional Limits

Version: 0.16.5
Last reviewed: 2026-07-21

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
`m1` (Horacio's Apple M1, 8-core), `gh-runner` (GitHub Actions
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
- **Last exercised:** 2026-07-19 env:sandbox — full suite green.

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

### G-12. RI restrict race harness (`pkg/server/ri_restrict_race_test.go`)

- **Gate:** none; runs by default and since 2026-07-21 **ASSERTS** the invariant: the check-then-act window was closed by `DeleteWithRestrict`'s in-transaction referrer check (@C04a), ahead of the stage-3 schedule, so any dangling reference is now a failure.
- **Hardware:** multi-core essential; the window is a logic race between a delete's enforcement read and a concurrent referrer create, and only manifests under true parallelism. A single-core pass is not evidence either way.
- **Invocation:** `GOMAXPROCS=<cores> go test ./pkg/server/ -run TestRIRestrict_Race -count=20 -race`.
- **Last exercised:** 2026-07-21 env:sandbox (single-CPU) — asserting mode, 0/8 dangling (single-core cannot open the window; vacuous pass, not evidence). **Owed:** a real-silicon multi-core run to verify the closure under true parallelism, handed to the operator: `GOMAXPROCS=<cores> go test ./pkg/server/ -run TestRIRestrict_Race -count=20 -race`
- **Conversion:** done 2026-07-21 — the harness asserts `dangling == 0`; the diagnostic mode is retired.

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
