# xolu — Resolution Record

Append-only record of closed register items and resolved issues. Each entry
is the item's full text as it stood at closure, stamped with closing version
and date, newest first. `CHANGELOG.md` says what shipped; this file records
what was wrong or needed and how it was resolved — the two reference, never
duplicate, each other. Never edit or delete existing entries.

---

## T-40 — closed v0.16.2 (2026-07-19)

**Verification record:** GitHub Actions run on commit 5332542
(ubuntu-latest multi-core runner, 2026-07-19): conclusion success, both
jobs, two runs. The immediately preceding commit (5037943, carrying only
the T-39 fix) failed on this defect's `fatal error: concurrent map
writes` — demonstrating the fix was necessary — and the fixed commit
went green — demonstrating it sufficient. Discovered, located (by the
runtime's own fatal with full stack), fixed, and verified within the CI
runner's first day of service.

### T-40. Data race in pkg/server process-shared state (unlocated)

Theme: server · Priority: P1 · Status: ◐
Blocks/after: Blocks trusting any multi-core -race run of pkg/server; every parallel graph e2e test fails with "race detected during execution of test" on M1 (count=5) while single-core runs are silent. Post-0.9.9 regression window (older versions ran -race clean per Horacio).

- **Evidence:** detector-confirmed on M1; even near-no-op tests
  (`TestGraphPath_MissingParams`) trip it, and each test boots its own
  server — so the racing state is process-global, not per-server or
  per-test.
- **Suspects eliminated by inspection (2026-07-19):** oql profile
  presets (Calibrate/DefaultProfile return fresh values; ProfileByName
  pointees never mutated); oql Executor.SetProfile (instance-scoped
  planner swap); pkg/tenant registry (instance state under RWMutex);
  zerolog package-global logger (never assigned anywhere; only atomic
  SetGlobalLevel in oql tests). The concurrent-calibration garbage
  thresholds (82–90k ns/row vs normal 3–8k under load) remain a
  separate robustness observation, not the race.
- **Next step (required, cannot be generated in-sandbox):** the race
  detector's WARNING: DATA RACE report — two stack traces naming the
  writing and reading file:line — from a multi-core run of any single
  test in graph_path_e2e_test.go.
- **Estimate:** unknown until located; the fix is usually an hour once
  the stacks land.
- **LOCATED (2026-07-19)** by the runtime's own fatal on the CI runner:
  `fatal error: concurrent map writes` at `RegisterScalarFunc`
  (scalar.go:54) ← `RegisterSeqGenFuncs` ← `NewEngineWithSchemaValidator`
  ← `server.New`. Every engine construction registered @SEQ/@GEN closures
  — **capturing that engine's executor** — into the package-global
  `ScalarFunctions` map. Two defects in one: concurrent map writes when
  servers boot in parallel, and last-writer-wins meaning every engine's
  @SEQ/@GEN dispatched to the most recently built executor (cross-engine
  sequence-session leakage; invisible in single-server production).
- **Fix shipped (v0.16.2):** Executor gains an instance `scalars` overlay
  consulted before the package defaults (`EvalScalarFunctionWith`);
  `RegisterSeqGenFuncs` writes the overlay; the package map carries inert
  @SEQ/@GEN stubs so membership checks stay correct; `RegisterScalarFunc`
  contract hardened to init()-only. The four stateless generator
  registrations in v2_gen_handlers were audited and exonerated (init()-
  time, within contract). **Closure on** a multi-core CI/M1 green run.

---

## T-39 — closed v0.16.2 (2026-07-19)

**Verification record:** same CI run as T-40 (commit 5332542, success ×2,
2026-07-19). Note the intermediate commit 5037943 carried this fix alone
and still failed — on T-40, not on this test — confirming the two
defects were independent.

### T-39. `TestBlobManager_GlobalUsage_MultiTenant` races the sampler's initial sample

Theme: server · Priority: P1 · Status: ◐
Blocks/after: Blocks CI green (the sole plain-run failure on multi-core; passes single-core). Fix shape pending design confirmation from Horacio.

- **Trigger:** first multi-core executions of the full suite (GitHub runner,
  then reproduced on M1: `TenantCount = 3, want 2`).
- **Mechanism (diagnosed):** `pkg/blob` `UsageSampler.run()` takes an
  immediate `u.sample()` on goroutine start (sampler.go:100). The test
  opens tenant 3 via `StoreFor(3)` and asserts its `SampledAt` is still
  zero — a state the implementation makes deliberately transient. On
  multi-core the sampler's first sample completes before `GlobalUsage()`;
  single-core scheduling always let the assertion win, masking it since
  the test shipped.
- **Reading:** `GlobalUsage`'s skip-unsampled contract exists to cover the
  startup window, implying the immediate initial sample is intended and
  the TEST is the defect (asserting transient state timing-dependently).
- **Fix shape (pending confirmation):** prove the skip-unsampled contract
  deterministically — construct a sampler with zero `SampledAt` directly
  at unit level, or gate tenant 3's sampler start — rather than racing
  `StoreFor`.
- **Estimate:** an hour once the design ruling lands.
- **Fix shipped (2026-07-19):** the test now injects tenant 3 as an open
  store with a constructed-but-never-Started sampler — zero SampledAt by
  construction, no timing anywhere. Passes ×3 in-sandbox; **closure on a
  multi-core run confirming** (single-core cannot arbitrate this class).

---

## T-34 — closed v0.16.1 (2026-07-18)

Fix shipped in v0.16.1 (see that changelog entry); **verified on 8-core
M1 under `-race`: five consecutive runs of
`TestConcurrentTerminalTransition_ExactlyOneWins` pass** (previously
2–4 of 32 racers won in every trial). The defect existed since the cal
lifecycle shipped; the v0.14.11 race guard encoded the invariant
correctly but was never executed until 2026-07-18 — a lesson now
carried by T-22's remit: a shipped guard that never runs guards
nothing. Single-core environments cannot reproduce or verify this
class; the 1-CPU sandbox passed the failing code and the fixed code
identically.

### T-34. Terminal transitions are check-then-act, not atomic — multiple racers win

Theme: cal · Priority: P1 · Status: ◐
Blocks/after: molu Part 2 booking tools (which will drive concurrent confirms); the v0.16.0 stress blessing. Fix verification requires multi-core hardware — the 1-CPU sandbox cannot reproduce.

- **Trigger:** first-ever local run of the stress-tagged
  `TestConcurrentTerminalTransition_ExactlyOneWins` (shipped v0.14.11,
  never executed in any recorded campaign — the T-15 record covers the
  seal harness only). On an 8-core M1: 2–4 of 32 racers succeed in every
  trial across all four transition kinds; `success + illegal = 32`
  always; zero data races.
- **Diagnosis:** `Lifecycle.transition` reads the booking, checks
  `allowedTransition`, then calls `BookingSource.SetState` — an
  unconditional overwrite. All goroutines that read the stale state pass
  the check and return nil. The invariant the test encodes ("state graph
  as natural mutex") was specified but never implemented.
- **Fix shape:** compare-and-swap at the source: `SetState` gains the
  expected from-state (`SetStateFrom(cal, id, from, to)`); SQLite:
  `UPDATE … SET state=? WHERE … AND state=?` with RowsAffected==1, else
  `ErrIllegalTransition`; Mem source: mutex-guarded check+set. Losers
  return before touching the index; the winner does index work once.
- **Consequence unfixed:** N callers each believe they performed the
  terminal transition — compensation logic, notifications, and any
  exactly-once accounting downstream silently multiply.
- **Estimate:** half a day including both sources, call-site sweep, and
  local re-verification on multi-core hardware.
- **Fix shipped (v0.16.1):** `SetState` → `SetStateFrom(cal, id, from, to)`
  across the Store interface and both sources; SQLite guarded UPDATE
  (`AND state=?`) with RowsAffected==1, Mem check-and-set under the
  source lock; losers get `ErrIllegalTransition` before any index work.
  **Closure gated on** the race test passing on multi-core hardware.

---

## T-02 — closed v0.16.0 (2026-07-18)

The client roadmap completed: Stages 0–4 shipped v0.14.2–v0.14.6,
Stage 5 (cal methods) v0.15.3, Stage 6 (coverage audit) v0.16.0 per
`docs/CLIENT_STAGE6_PLAN.md`. The audit's conclusion: declared-scope
coverage was already complete; Stage 6 delivered the scope declaration
itself (package doc naming the stable surface and the deliberate
exclusions), the T-32 wire fix, and the T-26 integration suite. The
client is declared stable and version-tied at v0.16.0.

### T-02. Ship an official Go client as `pkg/client` in the xolu repo — ↻ **partially shipped through v0.14.6 (Stages 0–4 complete)**

Theme: client · Priority: P1 · Status: ◐
Blocks/after: Blocks: molu Part 2 tools; only Stage 6 (coverage audit) remains. Plan: MOLU_READINESS_PLAN.md M4.

Being executed as a staged roadmap. Progress to date:

- **Stage 0 (v0.14.2)** — ✓ Import shelf's `internal/olu` client as `pkg/client` scaffolding. Package renamed to `client`; doc comments and identifiers brought into line with the T-01 rename. 638 lines of client + 810 lines of tests, stdlib only.
- **Stage 1 (v0.14.3)** — ✓ `Ready(ctx)` for the molu health probe (`/ready`, distinct from `/health`); three auth modes (`WithAPIKey`, `WithBearerToken`, `WithJWT`) via explicit `AuthMode` field; structured `Error` type carrying `XOLU-*` code, HTTP status, message, and raw detail — parses both xolu's current structured shape and the legacy flat shape. 12 new tests.
- **Stage 2 (v0.14.4)** — ✓ Semantic-map endpoints: `GetEntitySchema`, `ListMachineDefs`, `GetMachineDef`, `ListGenerators` (per kind, plus `AllGeneratorKinds` for iteration), `GetSequence`, `ListEventDefs`, `GetEventDef`, `V2Availability`. Full typed structures in `types_schema.go` mirroring xolu's internal `fsmDefinitionSpec`, `eventDef`, and related shapes byte-for-byte. 18 new tests.
- **Stage 3 (v0.14.5)** — ✓ FSM machine operations: `CreateMachine`, `ListMachines` (with `Definition`/`State`/`Ref` filter), `GetMachine`, `PatchMachine`, `DeleteMachine`, `WalkMachine`, `GetMachineState`, `GetMachineResult`, `GetMachineVars`, `GetMachineTransitions`, `GetMachineHistory`. Walk returns `*WalkResult` on success or `*Error` with `XOLU-FSM003` through `XOLU-FSM008` on rejection. 25 new tests. Client now has 78 tests total.
- **Stage 4 (v0.14.6)** — ✓ Operational hardening: retry policy via `WithRetryPolicy` (HTTP-idempotent methods only per RFC 9110 §9.2.2; POST/PATCH never retry under any configuration), structured `log/slog` telemetry via `WithLogger`, per-call timeouts. All opt-in; default client behaves identically to v0.14.5. All 78 pre-Stage-4 tests pass unchanged.

Remaining stages:

- **Stage 5 (v0.15.3)** — ✓ `cal` HTTP methods: `CalCheck`, `CalOpenings`, `CalPropose`, `CalConfirm`, with typed wire shapes (`types_cal.go`), client-side objective validation, XOLU-CAL error mapping, and 12 tests including the Openings→Check→Propose sequence at the wire level.
- **Stage 6** — Full v1 endpoint coverage audit; type-safe request/response models where the current code uses `map[string]any` and structure exists; complete godoc coverage.

Original acceptance criteria still apply to the final release: unified auth handling (done in Stage 1), sensible timeouts, retry policy for idempotent operations only (done in Stage 4), telemetry hooks via `slog` (done in Stage 4), zero non-stdlib dependencies beyond what xolu itself already pulls (maintained throughout), version-tied to the server.

Of the xolu-side gaps discovered during client work, T-24 and T-25 closed in v0.15.1 (see RESOLVED.md); T-28 (history pagination) remains open below.

---

## T-26 — closed v0.16.0 (2026-07-18)

Shipped in minimal form per the M4a decision (D-iii):
`pkg/client/integration_test.go`, build-tagged `integration`, eight
flows covering every declared-scope method's happy path against an
in-process server over real HTTP, wired into `release.sh
--with-integration`. It earned its keep before it was even finished:
first runs surfaced three wire-shape assumptions in the harness itself
(FSM defs require a terminal state; event defs use flat `action_type`;
FTS is double-gated server+store) and led to filing T-33. Run locally
with `-race` before blessing a release; the sandbox runs it without.

### T-26. In-process xolu integration test suite for `pkg/client`

Theme: client · Priority: P4 · Status: ☐
Blocks/after: Optional standalone, or fold into T-02 Stage 6 — both approaches are honest. Plan: MOLU_READINESS_PLAN.md M4a decides (D-iii).

- **Trigger:** every client test written through Stages 0-3 uses `httptest.NewServer` with hand-constructed responses that match what xolu's handlers actually write. This is a real-enough approximation for unit tests but not an end-to-end verification. Wire-format drift on the server side would not be caught.
- **Impact:** medium. False confidence in client correctness against future server versions; regressions in either direction (server response shape changes, client parsing changes) would go undetected until a real deployment.
- **Work required:**
  - Add `pkg/client/integration_test.go` guarded by a build tag (`//go:build integration`) so it only runs when explicitly requested.
  - The suite boots an in-process xolu server via `server.New(config.Default())` against a memory-backed storage.
  - Exercises every client method against the running server, asserting shape and behaviour, not just HTTP status.
  - Wire into `release.sh` as an optional `--with-integration` flag.
  - Could roll into Stage 6 (full v1 endpoint coverage audit) instead of shipping as a separate task; both approaches are honest.
- **Estimate:** 2-3 days if done as a standalone task; less if bundled with Stage 6.

---

## T-32 — closed v0.16.0 (2026-07-18)

Fixed as specified: `Step` → `IncrementBy` (`increment_by`), `Cycle`
added, `CreatedAt` dropped, plus the optional `MinVal`/`MaxVal` bounds
the original shape never captured. `GetSequence` unit tests migrated to
the true wire; the integration suite asserts `IncrementBy` round-trips
against a real server, which is the regression class this item came
from. Deliberate breaking change recorded in the v0.16.0 changelog.

### T-32. Client `Sequence` type does not match the wire

Theme: client · Priority: P4 · Status: ☐
Blocks/after: Candidate for M4b (Stage 6 type audit); breaking client type change, so land before the v0.16.0 stability declaration.

- **Trigger:** found during M1 (T-25 client work). `pkg/client/types_schema.go`
  declares `Sequence.Step` with tag `json:"step"` and a `CreatedAt` field —
  but `handleSeqGet` sends `increment_by` and no timestamp. `Step` has
  therefore been silently zero since Stage 2, and `Cycle` is not captured
  at all.
- **Work required:** rename `Step` → `IncrementBy` with the correct tag,
  add `Cycle bool`, drop `CreatedAt` (or keep only if the server ever
  grows the column); update `GetSequence` tests, which currently pass
  because they assert the same wrong shape they construct.
- **Impact:** any consumer reading `Sequence.Step` gets zero. No known
  consumers today; molu would have been the first.
- **Estimate:** an hour, plus the deliberate breaking-change note in the
  client changelog entry.

---

## T-19 — closed v0.15.2 (2026-07-18)

Shipped in the M2 stage of `docs/MOLU_READINESS_PLAN.md`; see the 0.15.2
changelog entry. Executed with a recorded deviation from both the item
text below and the frozen plan: the lean type went into a new
`pkg/authconfig` package (per decision D-ii) and the middleware itself
moved to a new `pkg/authmw` package rather than being refactored in
place, because per-package Go dependencies meant `ratelimit.go` kept
dragging `pkg/config` into any importer of `pkg/middleware`. Full
deviation record in `docs/MOLU_READINESS_TRACKING.md`. The field set
extracted is the source-verified read-set (including `InternalToken`,
which the text below omits, and excluding `TenantAuthMode`, which
auth.go never read).

### T-19. Extract auth machinery for reuse by molu hub

Theme: auth · Priority: P2 · Status: ☐
Blocks/after: Blocks: molu hub (else it duplicates auth or drags the full config surface). After: land with or shortly after T-02 completion. Plan: MOLU_READINESS_PLAN.md M2.

- **Trigger:** molu Part 3 §5 specifies that the molu hub reuses xolu's authentication code (bearer, API key, JWT modes) rather than reimplementing it.
- **Current state:** `pkg/middleware/auth.go` is under `pkg/` and therefore importable, but it depends on `pkg/config.Config` — the whole xolu server config surface. Importing it into a separate binary (molu hub) means dragging in every unrelated config field.
- **Work required:**
  - Extract a lean auth-config subset (`AuthType`, `JWT` settings, `APIKeys`, `APIKeyGrants`, `TenantAuthMode`, `AuthExcludePaths`) into its own type — either as a new package `pkg/authconfig` or as a struct embedded in `pkg/config.Config`.
  - Refactor `AuthMiddleware` and its helpers to take the lean type rather than `*config.Config`.
  - Preserve backward compatibility: the xolu server continues to work unchanged by wiring the lean subset from its full config at startup.
  - Publish the auth package as a stable import surface with documented semantics.
- **Estimate:** small — one to two days of refactoring plus tests.
- **Sequencing:** should land before or with T-02 so the hub can consume both packages together at v0.15.0.

---

## T-21 — closed v0.15.2 (2026-07-18)

Shipped in the M2 stage; see the 0.15.2 changelog entry. Decision taken
(D-i): option (a), short area codes `MF` and `MH`, reserved in
`ERROR_CODES.md`'s category table with a satellite-project reservation
note. No code change, as specified.

### T-21. Add `MOLU-FRONT-*` and `MOLU-HUB-*` error-code prefixes to the reserved catalogue

Theme: conventions · Priority: P2 · Status: ☐
Blocks/after: Blocks: molu error-code allocation (collision prevention; trivial size, non-negotiable importance). Plan: MOLU_READINESS_PLAN.md M2.

- **Trigger:** molu Part 2 §8.5 defines a family of error codes (`XOLU-MOLU-FRONT-UNAVAILABLE`, `XOLU-MOLU-FRONT-STARTUP`, `XOLU-MOLU-FRONT-TIMEOUT`, `XOLU-MOLU-FRONT-CONTRACT`, `XOLU-MOLU-FRONT-HUB-UNAVAILABLE`) following the xolu error-code convention `XOLU-<AREA><NUM>`. molu Part 3 §5.2 defines the `MOLU-HUB-NS001` diagnostic in the same family.
- **Current state:** xolu's error catalogue in `pkg/errors/errors.go` uses two-letter to three-letter area codes (`ST`, `GR`, `QL`, `VL`, `TN`, and so on). The `MOLU-FRONT` and `MOLU-HUB` families would break the pattern by being longer than three characters.
- **Work required:**
  - Decide whether to (a) shorten to `MF` and `MH` at three characters, or (b) formalise a longer area code convention for extension products.
  - Reserve the chosen prefixes in xolu's error documentation as belonging to satellite projects, not to xolu itself.
  - No code change in xolu — this is a documentation-and-convention task to prevent future conflict.
- **Impact:** trivial in size, non-negotiable in importance: without this, molu error codes could collide with xolu's own if xolu expands its area-code space.
- **Note:** T-01 (rename) already updates the prefix from `OLU-*` to `XOLU-*`. T-21 is downstream of that rename and can land at the same time.

---

## T-25 — closed v0.15.1 (2026-07-18)

Shipped in the M1 stage of `docs/MOLU_READINESS_PLAN.md`; see the
0.15.1 changelog entry. The `created_at` field suggested below was not
implemented — the `sequences` table has no such column. Found and filed
during this work: T-32 (client `Sequence` wire mismatch).

### T-25. Add `GET /api/v2/gen/seq` list endpoint for named sequences

Theme: api-surface · Priority: P2 · Status: ☐
Blocks/after: Blocks: molu Part 2 §4 SemanticMap (sequence advertisement). Pairs: T-24 (one server-side release). Plan: MOLU_READINESS_PLAN.md M1.

- **Trigger:** discovered during client Stage 2 source reconnaissance. xolu v0.14.5 exposes `POST /api/v2/gen/seq` (define), `GET /api/v2/gen/seq/{name}` (get), and `GET /api/v2/gen/seq/{name}/next` (increment) but has no route to enumerate named sequences.
- **Impact:** consumers can only access sequences whose names they already know. Molu's semantic-map builder cannot advertise available sequences.
- **Work required:**
  - Add `handleSeqList` in `pkg/server/v2_seq_handlers.go`, returning `{"sequences": [{"name": "...", "current": N, "created_at": "..."}]}`.
  - Register `r.Get("/gen/seq", s.handleSeqList)` — will conflict with the existing `POST /gen/seq` route only if chi's routing is method-blind (it isn't; they can coexist).
  - Consider mirroring at `/seq` for the permanent alias.
  - Add a client method `ListSequences(ctx) ([]SequenceSummary, error)` once the endpoint ships.
- **Estimate:** half a day.

---

## T-24 — closed v0.15.1 (2026-07-18)

Shipped in the M1 stage of `docs/MOLU_READINESS_PLAN.md`; see the
0.15.1 changelog entry. The `created_at` field suggested below was not
implemented — the validator tracks no registration timestamps.

### T-24. Add `GET /api/v1/schemas` list endpoint for entity types

Theme: api-surface · Priority: P2 · Status: ☐
Blocks/after: Blocks: molu Part 2 §4 SemanticMap (entity-type discovery). Pairs: T-25 (one server-side release). Plan: MOLU_READINESS_PLAN.md M1.

- **Trigger:** discovered during client Stage 2 source reconnaissance. xolu v0.14.5 exposes `GET /api/v1/schema/{entity}` for a single type but has no route to enumerate registered entity types.
- **Impact:** blocks the molu Part 2 §4 SemanticMap builder from discovering entity types at runtime. Consumers must currently supply entity-type names as configuration.
- **Work required:**
  - Add `handleListSchemas` in `pkg/server/handlers.go`, returning `{"schemas": [{"name": "...", "created_at": "..."}]}` or similar. Envelope key choice should be consistent with existing v1 list endpoints.
  - Route registration under `r.Get("/schemas", s.handleListSchemas)` next to the existing `/schema/{entity}` routes.
  - Add a client method `ListEntityTypes(ctx) ([]EntityTypeSummary, error)` in `pkg/client/schema.go` once the endpoint ships.
- **Estimate:** half a day.

---

## T-15 — closed v0.15.0 (2026-07-18)

### T-15. `cal` seal concurrency stress at production scale — CLOSED (v0.15.0)

- **Status:** closed 2026-07-18.
- **Resolution:** stress harness (`pkg/cal/seal_stress_local_test.go`,
  build-tagged `stress`, shipped in v0.14.14) run on local hardware
  (M1 Mac, 8 GOMAXPROCS) under `-race`.
- **Default scale run:** 5 trials × 16 workers × 5000 bookings ×
  2000 ops/worker × 10 calendars × 90 days = 160,000 mutation
  attempts across all trials in 118.85s. All trials passed. Zero
  data races, zero invariant failures, zero seal frontier
  regressions.
- **Extended scale run:** 5 trials × 32 workers × 5000 ops/worker
  = 800,000 mutation attempts in 129.59s at 6,048–6,295 ops/s
  (under `-race`; ~5-10x higher without race detection). All
  trials passed with the same clean signals.
- **Observed success ratios:** Confirm ~0.24%, Cancel ~0.68%,
  Move ~0.27% — the natural rates for random-selection stress
  against a mostly-terminal-state population, and stable across
  trials. Consistency itself is a healthy sign; a race under
  contention would have produced variable ratios.

---

## T-31 — closed v0.14.14 (2026-07-18)

### T-31. `cal` fault injection at the SQL boundary — CLOSED (v0.14.14)

- **Status:** closed 2026-07-18. `AddToPlaneFaultHook` and `RemoveFromPlaneFaultHook` hooks on `IndexStore` shipped in v0.14.14, plus four tests in `pkg/cal/fault_injection_test.go` exercising SetState-succeeds-index-fails scenarios and verifying `RebuildFrom` reconciles.
- **Trigger:** `Lifecycle.transition` applies the SQL state change (`SetState`) first, then updates the in-memory index. If the SQL succeeds but the subsequent `removeFromPlane` or `addToPlane` fails (Pebble I/O error, disk full, corruption), the SQL source of truth reflects the new state but the index does not. The scoped-recompute-from-source pattern is designed to make this recoverable via the next `RebuildFrom`, but the recovery path has never been exercised under injected failure — only under natural operation where it never fires.
- **Impact:** low frequency, unknown blast radius. Real production databases do hiccup. Without evidence for the recovery behaviour, the first time a production tenant sees an index/source disagreement, whoever's debugging it will have to reason from first principles rather than pointing at a passing test.
- **Work required:**
  - Introduce a fault-injection hook in `IndexStore.addToPlane` and `removeFromPlane` accepting an optional `errAfter` counter for tests.
  - Test scenarios: `SetState` succeeds then `addToPlane` fails; `SetState` succeeds then `removeFromPlane` fails; both operations succeed but a subsequent operation on the same booking sees a partially-updated state.
  - After each injected failure, verify `assertIndexMatchesRebuild` holds after a rebuild (recovery works) even though it did not hold immediately (mid-failure state is genuinely inconsistent).
  - Document the observed behaviour in `pkg/cal` package godoc: "under SQL/index disagreement, the next rebuild reconciles."
- **Estimate:** 1-2 days. The fault-injection hook is the most invasive piece; the test scenarios are straightforward once it exists.

---

## T-17 — closed v0.14.14 (2026-07-18)

### T-17. Reconcile `cal` proposal docs with the v0.14 implementation — CLOSED (v0.14.14)

- **Status:** closed 2026-07-18. Each doc now carries a dated "Reconciliation status" banner naming what actually shipped vs. what was proposed, without rewriting the historical content. Full rewrites deferred as unwarranted scope given the code and CHANGELOG are the source of truth.
- `docs/proposals/cal-rest-api.md`, `docs/proposals/cal-pebble-codec.md`, `docs/proposals/SESSION-2026-06-22-NOTES.md` predate the implementation.
- Describe `cal` as design-only with open questions; several were resolved in code during v0.14.0.
- Called out in the v0.14 changelog as "a separate, deliberate task".

---

## T-29 — closed v0.14.13 (2026-07-18)

### T-29. `cal` Openings ↔ Check agreement property test — CLOSED (v0.14.13)

- **Status:** closed 2026-07-18. Property test shipped in v0.14.13 as `TestOpeningsCheckAgreement_ForwardProperty` and `TestOpeningsCheckAgreement_ReverseProperty` in `pkg/cal/openings_check_property_test.go`. 50 trials × 20 queries per state per direction. Mutation-verified: forcing `freeRuns` to report every quantum as free triggers the forward test; forcing `Check` to always feasible triggers the reverse test.
- **Trigger:** during T-18 wire-up work (v0.14.7 through v0.14.11), the HTTP-level tests confirmed that `Openings` results do not overlap existing bookings, but did NOT confirm the stronger property: every span returned by `Openings(from, to, dur, obj)` passes `Check(span, mode)` with `feasible=true`. The two functions share the underlying `quantaInPeriod` and `dayOn` primitives, so the drift surface is narrow, but "narrow" is not "proven." A downstream caller (client Stage 5, molu Part 2 tools) will exercise `Openings → Check → Create` sequences immediately; if there is a boundary-condition drift between the two functions, that caller will hit it first.
- **Impact:** low likelihood, medium blast radius. A drift means callers are told a window is free and then have `Create` refuse a booking there — a confusing failure mode that surfaces only under specific quantum-boundary alignments.
- **Work required:**
  - Property test in `pkg/cal/availability_test.go` (or new `openings_check_property_test.go`) using the existing property-test harness pattern from `codec_property_test.go`.
  - Generate random calendar states via sequences of Create/Confirm/Cancel/Move.
  - For each state, call `Openings` with a range of `(from, to, duration, objective)` inputs and assert every returned span passes `Check` with `feasible=true`.
  - Reverse direction as a bonus: sample random spans in the query window; if `Check` says `feasible=true`, assert `Openings` (with objective `earliest`) returned an opening containing or preceding the sampled span.
- **Estimate:** half a day. If the test finds a bug, fix cost is separately scoped.

---

## T-30 — closed v0.14.12/v0.14.13 (2026-07-18)

### T-30. Remove `ModeShared` and `ModeSubPrefix` from the `cal` type surface — CLOSED (v0.14.12/v0.14.13)

- **Status:** closed 2026-07-18. Mode reduction shipped in v0.14.12 (`ModeShared` and `ModeSubPrefix` removed from types, source layers reject non-`ModeExclusive` with `ErrModeNotSupported` / XOLU-CAL007). `Calendar.Capacity` removed entirely in v0.14.13.
- **Trigger:** the `Mode` constants declare vocabulary the occupancy engine does not honour. `ModeShared` and `ModeSubPrefix` are stored on Booking records but the engine treats every booking as exclusive regardless. The v0.14.10 review with Google Calendar comparison confirmed cal's target model is "exclusive-only, like Google Calendar" (Option A); pooled resources (Option B) were explicitly rejected as 8x storage and disproportionate implementation cost. The vocabulary items existing but doing nothing is a footgun for anyone reading the code who assumes they work.
- **Impact:** low. Nothing in xolu or any downstream consumer references `ModeShared` or `ModeSubPrefix`. Removal is a compile-time signal of the truthful state.
- **Work required:**
  - Remove `ModeShared` and `ModeSubPrefix` constants from `pkg/cal/booking.go`.
  - Update `Booking.Mode` godoc to name `ModeExclusive` as the only valid value.
  - Reject any non-`ModeExclusive` value at `SQLiteBookingSource.PutBooking` and `MemBookingSource.PutBooking` with a new `ErrModeNotSupported` sentinel wrapped via `%w` per the taxonomy from v0.14.8.
  - Reject at the HTTP handler layer via the existing `classifyCalError` helper (needs a new mapping entry).
  - Decide separately (still pending) on `Calendar.Capacity`: keep-and-redocument as descriptive metadata (Google's model) or remove entirely. This decision is a prerequisite for closing this item.
  - Update the stale comment in `pkg/cal/availability.go:339` naming "Stage 2 treats the calendar as a single exclusive resource" — the comment is truthful today but references a stage boundary that no longer applies.
  - Add tests for the new rejections.
- **Estimate:** half a day once the Capacity decision is in.

---

## T-18 — shipped v0.14.7, hardened through v0.14.11 (2026-07-18)

### T-18. Expose `cal` via HTTP endpoints — ✓ **SHIPPED in v0.14.7 (hardened through v0.14.11)**

- **Status:** shipped. Four endpoints under `/api/v2/cal/*` in `pkg/server/v2_cal_handlers.go`: `check`, `openings`, `propose`, `confirm`. Opt-in via `XOLU_CAL_ENABLED`.
- The minimum surface required by molu (Part 2 §5.1.10–§5.1.13) is covered: `openings` accepts an `objective` parameter with the four implemented values `earliest`, `first-fit`, `emptiest`, `longest-clear-margin`, validated at the handler.
- Follow-up hardening shipped in v0.14.8 through v0.14.11: typed error taxonomy (XOLU-CAL001–007 with `errors.Is` status dispatch), 22 HTTP-level tests, `Manager.CreateCalendar` facade, concurrent-transition and rebuild regression guards.
- Still future, once agentic usage patterns are observed: `move` (atomic reschedule), `complete` / `cancel` (terminal lifecycle transitions).

---

## T-01 — shipped v0.14.1 (2026-07-18)

### T-01. Rename `olu` to `xolu` project-wide — ✓ **SHIPPED in v0.14.1**

- Go module path: `github.com/ha1tch/olu` → `github.com/ha1tch/xolu`.
- All `OLU_*` environment variables → `XOLU_*` (127 variables).
- Binary names: `cmd/olu` → `cmd/xolu`; `cmd/iolu` retained deliberately (interactive olu → interactive xolu-admin was rejected in favour of preserving the well-known name).
- Error code prefix `OLU-*` → `XOLU-*` (132 codes and 8 family prefixes).
- Internal package renames: `pkg/olutime` → `pkg/xolutime`; import aliases `oluerr`/`oluMiddleware` → `xoluerr`/`xoluMiddleware`.
- Prometheus metric names emitted at `/metrics`: `olu_*` → `xolu_*`.
- Secret-name arguments to `readSecret`: `olu_jwt_secret` / `olu_internal_token` → `xolu_jwt_secret` / `xolu_internal_token`.
- API paths `/api/v1/…` and `/api/v2/…` do **not** carry the product name — wire protocol untouched, as originally planned.
- CHANGELOG.md deliberately preserved as historical record; entries prior to v0.14.1 continue to reference the software as it was called at the time.
- Actual scope: 3120 hits across 205 files, 4 path renames, executed via a resumable classifier/applier pipeline with role-based classification (ENV, ERRCODE, IDENT_LC, IDENT_UC, IDENT_CMP, STRING_LC, ENV_GLOB) so false positives (`column`, `resolution`, `volume`, `solution`, and so on) were provably untouched.

---

## Namespace retirement: TD-nnn — 2026-07-18 (v0.15.0)

The TD-nnn namespace from `docs/KNOWN_ISSUES.md` is retired. Its two items
were already mirrored in the register and their full detail now lives there:

- TD-001 (unified function registry) → **T-03** in `docs/TRACKING.md`.
- TD-002 (OQL set operations) → **T-04** in `docs/TRACKING.md`.

No content was lost; the KNOWN_ISSUES detail text was merged verbatim into
the register entries as "Extended detail (from retired TD-00n)".

---

## `cal` SQLite secondary indices — resolved in the S11 cal migration (v0.14.0)

From the former KNOWN_ISSUES `cal` schema-gaps section:

- **The `cal` SQLite booking/calendar schema specifies no secondary indices
  (RESOLVED in the S11 migration; recorded here for provenance).** The
  booking-record design (`docs/proposals/cal-gate3-booking-record.md`) lists the
  `cal_calendars` / `cal_bookings` / `cal_participants` field sets and names their
  primary keys in prose, but specified **no `CREATE INDEX`** for the non-PK query
  patterns the tables face. Every use of "index" across the three `cal` proposal
  docs refers to the *Pebble occupancy bitmap* (the derived index), never to a
  SQLite secondary index. This was an omission in the spec.

  Resolved when the schema was implemented: the S11 cal stage in
  `pkg/storage/sqlite.go` (`initV2Schema`) creates the index set the
  `pkg/cal` query patterns require:
  - `idx_cal_bookings_cal_state` on `(tenant_id, calendar_id, state)` — the hot
    path for `LiveBookingsOn(calendarID, plane)`, used by every lifecycle
    mutation, `Move` feasibility check, and `MatchCommit` pre-check; also covers
    `RebuildFrom` / `LiveBookings` per-calendar scans via the leading columns.
  - `idx_cal_bookings_state` on `(tenant_id, state)` — the cross-calendar
    `bookings/list?state=missed` (§7 non-occurrence) scan.
  - `idx_cal_calendars_ordinal` (unique) on `(tenant_id, ordinal)` — enforces the
    dense ordinal's uniqueness within a tenant.
  - `booking(calendarID, bookingID)` point lookups and the `cal_participants`
    join are covered by the respective composite primary keys.

  The absence of these indices never affected correctness — the derived Pebble
  index and the `index == rebuild` invariant are independent of SQLite indices;
  the gap was purely query-performance (table scans where lookups belong), now
  closed.

---

## `cmd/iolu` normalized layout — resolved v0.13.1

From the former KNOWN_ISSUES storage-layout deferred-items section:

- **`cmd/iolu` does not use the normalized layout. — RESOLVED in 0.13.1.**
  The standalone admin CLI previously used its own `--db`/`--ts-dir` flag model
  and composed the old backend-first `ts/tXXXX/` paths via
  `tenant.StorageDirSegment`, so `iolu` and `xolu` disagreed about on-disk paths.
  All eight subcommands now operate on a `--base-dir` data root and derive every
  path through `pkg/storelayout`, matching exactly what the server writes:
  per-tenant `tXXXX/{store,ts,blobs}` (or `shared/store` for the SQLite primary
  in shared mode; ts and blobs are always per-tenant). The store organisation is
  auto-detected from disk with a `--mode per-file|shared` override. `tenant
  delete` is mode-aware (removes the whole `tXXXX/` directory in per-file mode;
  drops the tenant table family and removes ts/blobs dirs in shared mode), and
  inspection commands report per-tenant blob footprint alongside ts. A new
  `cmd/iolu/main_test.go` covers both modes, the layout assertions, the two
  per-file read regressions found during the rework (a connection-blocking hang
  in `tenant list`, and reads issued against tenant 0's store instead of the
  tenant's own store), the re-init refusal, and mode-aware delete. The `--db`
  flag is gone with no compatibility shim; this is an intentional CLI break, as
  the previous interface was already documented as unsafe against an
  `xolu`-managed root.

---

## Blob plane tenant-awareness — resolved v0.13.0

From the former KNOWN_ISSUES storage-layout deferred-items section:

- **Blob plane is not tenant-aware (security-relevant). — RESOLVED in 0.13.0.**
  Previously blobs were a single server-level store at `<BaseDir>/blobs/` that
  partitioned tenants internally by name (isolation-by-convention, not a security
  boundary). Both halves of the debt are now closed:
  - *Layout:* blobs are a first-class per-tenant role at `<BaseDir>/tXXXX/blobs/`,
    keyed by tenant ID and uniform with the timeseries plane (tenant 0 included).
    A per-tenant blob manager (mirroring `timeseries.DefaultManager`) hands out
    one single-tenant `blob.Store` per tenant; `sanitiseTenant` and the
    server-level `<BaseDir>/blobs/` path are removed, and `storelayout.Check`
    now treats a server-level `blobs/` directory as a violation. The
    tenant-name-vs-ID addressing is resolved at the handler seam (route tenant
    string → ID via the registry; tenant 0 for the unscoped route).
  - *Enforcement:* both the native JSON plane and the S3 plane enforce the
    per-identity tenant grant under `TenantEnforceGrant`, fail-closed, so a
    credential scoped to one tenant is rejected (403) on another tenant's blobs.
  There were no known users of the blob API, so no migration path was required.
  See D-004 below (SHA-validation guard), which was folded into this rework.

---

## Security defects D-001 – D-009 — fixed 2026-06-20 (v0.10.2 – v0.10.5 remediation series)

Full text of the adversarial-audit defect entries and their cross-cutting
notes, moved verbatim from `docs/KNOWN_ISSUES.md`. All nine are FIXED with
committed regression tests (named per entry below). Note: subsequent
hardening passes found further defects **D-010 through D-017**, fixed in
v0.10.4 and v0.10.5; those never had KNOWN_ISSUES entries and are recorded
in `CHANGELOG.md` only.

## Defects

### Summary

| ID | Defect | Severity | Reachable | Committed test |
|------|--------|----------|-----------|----------------|
| D-001 | `NEWID()` malformed UUID (undefined 64-bit shift) — **FIXED** | Low | Latent (no FSM calls `NEWID()`) | ✓ |
| D-002 | JWT `exp`/`nbf` expiry skipped for non-numeric claim type — **FIXED** | Low | Secret-gated | ✓ |
| D-003 | `jsonic` tokeniser has no nesting-depth guard — **FIXED** | Low | Latent (stdlib decoder shields write path) | ✓ |
| D-004 | `blob` SHA-addressed read accepts unvalidated digest (panic) — **FIXED** | Low | Unwired (no SHA-addressed handler yet) | ✓ |
| D-005 | SQL injection via OQL JOIN field names — **FIXED** | **High** | Yes — default storage mode | ✓ |
| D-006 | Timeseries `int`→`uint8` narrowing before range check — **FIXED** | Low | Yes (silent-correctness only) | ✓ |
| D-007 | OQL scalar functions panic / emit non-serialisable values — **FIXED** | Low (contained to a 500) | Yes (contained to a 500) | ✓ |
| D-008 | FSM functions panic on bad indices; unbounded allocation — **FIXED** | Low panic / **High** OOM | Yes — guard eval at transition time | ✓ |
| D-009 | DDL injection via JSON-schema field names — **FIXED** | **High** | Yes — any authenticated caller | ✓ |

Severity legend: **High** = remotely reachable integrity/availability impact;
Low = contained (caught by existing recovery, bounded by a downstream guard, or
gated behind a secret or unwired path). "Committed test" = a regression test for
this defect exists in the source tree; ✗ means it was confirmed during review
with a one-off harness that was not committed (see *Regression-coverage status*
under Cross-cutting notes). Full detail for each defect follows.

### D-001 — `NEWID()` produces a malformed UUID via undefined 64-bit shift

**Package:** `pkg/fsm/eval`
**Location:** `functions.go:1218` (`fnNewID`), registered at `functions.go:123`
**Introduced:** patch007 (S6, extracted verbatim from `aulsql/pkg/tsqlruntime`)
**Detected by:** `go vet ./pkg/fsm/...` —
`functions.go:1227:3: now (64 bits) too small for shift of 64`

`fnNewID` synthesises a UUID-shaped string from a single `int64`
timestamp (`now := time.Now().UnixNano()`):

```go
now := time.Now().UnixNano()        // int64, 64 bits
uuid := fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
    now&0xFFFFFFFF,
    (now>>32)&0xFFFF,
    (now>>48)&0xFFFF,
    uint16(now>>56)&0xFFFF,
    now>>64&0xFFFFFFFFFFFF,          // <- shift of 64 on a 64-bit value
)
```

The final field shifts a 64-bit value right by 64. In Go a shift count
equal to the operand width is well-defined for unsigned operands and
yields zero, but here the operand is signed and the constant shift is
caught by `vet` as a likely mistake. The field is always `0` regardless
of input, so the last 48 bits of every generated identifier are constant.
The output is therefore not a real UUID (no version/variant bits, last
segment fixed) and collides trivially under rapid generation, since all
remaining entropy derives from one nanosecond timestamp.

The code itself flags its own provisional status: `// In production, use
a proper UUID library`.

**Why this is reachable in xolu.** `NEWID()` is registered on the FSM
`FunctionRegistry` and so is callable from FSM `set` clauses. However,
patch007 also registers proper generators (`UUID_V4()`, `UUID_V7()`,
`CUID()`, `ULID()`) on every `Evaluator` at `New()` time. `NEWID()` is
thus a redundant, lower-quality alternative to `UUID_V4()` that survived
the aulsql extraction.

**Current impact:** Low. Build is clean; `vet` exits 0 (this is a
diagnostic, not a hard error). No FSM spec in the repository calls
`NEWID()` — guards and set clauses use `UUID_V4()`/`UUID_V7()`. The
defect is latent: it bites only if a future FSM definition uses
`NEWID()` and relies on its output being unique or well-formed.

**Candidate resolutions (not yet decided — deferred for deeper analysis):**

1. Reroute `NEWID()` to `UUID_V4()` and delete `fnNewID` entirely.
   Removes the malformed generator; preserves T-SQL `NEWID()` surface
   compatibility. Likely correct, but changes the registered function's
   behaviour and so is a design decision, not a mechanical fix.
2. Repair `fnNewID` to emit a well-formed value (e.g. combine two
   independent 64-bit sources, set version/variant bits). More code to
   maintain for a function option 1 makes redundant.
3. Drop `NEWID()` from the registry if T-SQL `NEWID()` surface
   compatibility is not required for FSM expressions.

The intended semantics of the original `now>>64` field cannot be
recovered from the source — any in-place repair is a guess at intent.
Resolution is left to a deeper review that can weigh T-SQL surface
compatibility against the existing first-class generators.

**Related:** TD-001. The `NEWID()`/`UUID_V4()` consolidation is part of
the same function-surface work as the post-S8 registry refactor. Whoever
retires the redundant generator (option 1 above) should do it alongside,
not before, that refactor — they touch the same FSM eval function
registry.

**Resolution (FIXED, 2026-06-20).** Decision taken by the project owner:
**keep the `NEWID()` surface** (it is wanted in OQL) and **bind it to the
correct UUID v4 implementation** rather than dropping or aliasing-away the
function. This is a deliberate hybrid of candidate options 1 and 2 — the name is
retained on both surfaces, and the generator now produces a real v4 UUID:

- **FSM eval.** `fnNewID` (`pkg/fsm/eval/functions.go`) now returns
  `uuid.NewRandom().String()` — the same generator backing `UUID_V4()`. The
  timestamp-synthesis code (and its undefined `now>>64` shift) is gone; `go vet
  ./pkg/fsm/eval/` is now clean, closing the diagnostic that opened this entry.
- **OQL.** `NEWID()` was **added** to the OQL scalar surface: a new
  `qs.ScalarNewID` (`pkg/qs/scalar.go`), backed by the same `uuid.NewRandom`, is
  registered as `"NEWID"` in `pkg/oql.ScalarFunctions`. It was previously not in
  the OQL map at all; it is now reachable through the normal OQL dispatch path.

Both bindings produce a unique, unpredictable, structurally valid version-4
UUID. The TD-001 registry-refactor note no longer gates this: the redundant
malformed generator is not being *retired*, it is being *corrected* in place, so
there is no surface change to coordinate.

This defect had no committed test in the audit bundle; tests were added across
all three surfaces: `pkg/fsm/eval/newid_d001_test.go` (valid v4, version nibble,
no constant tail, uniqueness over 1000 calls, parity with `UUID_V4()`),
`pkg/qs/scalar_newid_test.go` (the OQL-bound scalar), and
`pkg/oql/scalar_newid_test.go` (NEWID registered in the map and dispatching to a
v4 UUID through `EvalScalarFunction`). Full `pkg/fsm`, `pkg/qs`, and `pkg/oql`
suites pass.

---

### D-002 — JWT `exp`/`nbf` expiry check silently skipped for non-numeric claim types

**Package:** `pkg/middleware`
**Location:** `auth.go:163` (`exp`) and `auth.go:170` (`nbf`), in `parseAndValidateJWT`
**Detected by:** adversarial test — a token whose `exp` is a JSON string
(rather than a number) is accepted even when the encoded time is in the past.

The expiry and not-before checks are each guarded by a type assertion to
`float64`:

```go
if exp, ok := claims["exp"].(float64); ok {
    if time.Now().Unix() > int64(exp) {
        return nil, false
    }
}
```

When `exp` is present but not a JSON number — for example `"exp":"1700000000"`
(a string) — the assertion yields `ok == false`, the body is skipped, and the
token passes expiry validation unconditionally. The numeric path is correct: a
past numeric `exp` is rejected. Only the non-numeric branch is affected. The
same pattern applies to `nbf`.

**Why this is reachable.** The check sits after signature verification, so an
attacker must already hold a token bearing a valid HS256 signature — i.e. they
must know `JWTSecret`. This is therefore a defence-in-depth weakness, not an
unauthenticated bypass: it does not let an outsider forge a token, but it does
let a holder of an otherwise-expired (or leaked, or insider-minted) token evade
the expiry control by encoding `exp` as a string. A token's lifetime should not
depend on the JSON type used to encode its expiry.

**Related (same location, policy rather than defect):** a token with **no**
`exp` claim never expires — the `if ok` guard simply does not fire. This is a
silent default; whether unbounded-lifetime tokens are acceptable should be a
deliberate policy decision, not a side effect of an absent claim.

**Candidate resolution (not yet decided).** Normalise the claim before
comparison — decode with `json.Number`, or accept both `float64` and a
string-parsed numeric form — and decide explicitly whether a missing `exp` is
permitted. Changing the missing-`exp` behaviour alters auth semantics, so it is
a design decision, not a mechanical fix.

**Current impact:** Low, contingent on secret secrecy. No change to the
unauthenticated attack surface.

**Resolution (claim-type FIXED, 2026-06-20; missing-`exp` policy OPEN).** The
type-assertion weakness is closed in `pkg/middleware/auth.go`. A helper
`claimAsUnixTime` normalises an `exp`/`nbf` claim from either a JSON number
(`float64`/`json.Number`) or a numeric string to a Unix timestamp; both `exp`
and `nbf` are checked through it. Two behaviour points:

- A token's lifetime no longer depends on the JSON type of its expiry: a past
  `exp` encoded as the string `"…"` is now rejected exactly like a past numeric
  `exp`.
- A claim that is **present but not a parseable number** now **rejects** the
  token (`!ok → return nil, false`) rather than silently skipping the check —
  the secure default for a malformed time claim.

**Missing-`exp` policy — RESOLVED (Option B, 2026-06-20).** A token with **no**
`exp` claim is now **rejected**. The project owner chose to make `exp` mandatory
rather than treat an absent expiry as never-expiring: every token must carry a
parseable expiry, so a leaked or insider-minted token cannot be valid
indefinitely. `nbf` remains optional (a token without it is valid from
issuance). Regression tests added: `TestAuthMiddleware_JWT_MissingExp_Rejected`
(no `exp` → 401) and `TestAuthMiddleware_JWT_MissingNbf_Accepted` (`exp` present,
`nbf` absent → 200). This is a deliberate auth-semantics change: any issuer that
previously minted `exp`-less tokens must now set an expiry.

This defect had no committed test in the audit bundle; a regression test was
written first (red), then the fix applied: `pkg/middleware/auth_d002_test.go` —
a past string `exp` and a future string `nbf` are rejected (401); a valid
(future) string `exp` is accepted; and the numeric path still rejects a past
`exp` (control against weakening the existing check). The four pre-existing JWT
tests still pass. Full `pkg/middleware` suite passes.

---

### D-003 — `jsonic` tokeniser has no nesting-depth guard (stack-overflow DoS)

**Package:** `pkg/jsonic`
**Location:** `tokeniser.go` — `parseValue` → `parseObject`/`parseArray` →
`parseValue` recursion; no depth counter
**Detected by:** adversarial test — input nested to ~2,000,000 levels triggers
`fatal error: stack overflow` (Go's 1 GB goroutine-stack limit). Depths up to
~500,000 parse without error; the failure is an unrecoverable runtime fatal,
not a `panic`, so it cannot be contained by `recover()` and kills the process.

The recursive-descent tokeniser tracks no depth and relies entirely on the Go
runtime's stack-growth limit as its only bound. Once that limit is crossed the
process dies; there is no graceful error return.

**Why this is currently NOT remotely reachable.** `jsonic` tokenises document
rows *already stored* in the database, not raw request bodies. The entity write
path decodes through the standard library (`json.NewDecoder().Decode`,
`handlers.go:1357`), whose `maxNestingDepth` rejects input beyond 10,000 levels
regardless of body size. A document deep enough to overflow `jsonic` therefore
cannot be stored through the normal write path — the stdlib decoder rejects it
first. The current safety is provided by that upstream decoder, **not** by
`jsonic` itself.

**Residual exposure.** Any path that writes raw JSON bytes into a document
column while bypassing the stdlib decoder — bulk import, backup restore, a
future direct-ingestion route, or a backend that stores client bytes verbatim —
would reintroduce the DoS, because `jsonic` would then tokenise attacker-shaped
nesting on read. The guarantee rests on an upstream coincidence rather than a
local invariant.

**Candidate resolution (not yet decided).** Add an explicit depth counter to
`parseObject`/`parseArray` with a configurable maximum (defaulting at or below
the stdlib's 10,000) that returns a normal error rather than recursing into a
fatal. This makes `jsonic` self-protecting independent of who fills the document
column.

**Related hygiene gap.** `config.MaxEntitySize` is validated only as `> 0`
(`config.go:1012`) with no upper bound. It is not the active guard here (the
stdlib depth limit is), but an unbounded value removes one layer of the
body-size defence; an explicit ceiling would be prudent.

**Current impact:** Low under the default write path; latent for any
decoder-bypassing ingestion path.

**Resolution (FIXED, 2026-06-20).** The tokeniser now tracks nesting depth. A
`depth` field on `Tokeniser` is incremented on entry to `parseObject`/
`parseArray` and decremented on exit (via `defer`); when it would exceed the new
`MaxNestingDepth` constant (10000, matching the stdlib json decoder ceiling),
the parser returns a normal error (`jsonic: maximum nesting depth N exceeded`)
instead of recursing into an unrecoverable fatal. `depth` is reset at the start
of every `Tokenise` call and cleared in `PutTokeniser`, so a pooled tokeniser
carries no depth state between uses. jsonic is now self-protecting independent of
who fills the document column, closing the residual exposure for any
decoder-bypassing ingestion path.

This defect had no committed test in the audit bundle; a regression test was
written first (it would not even compile before the fix, since `MaxNestingDepth`
did not exist), then the fix applied: `pkg/jsonic/tokeniser_depth_test.go` —
excessive array and object nesting each return a clean error, within-limit
nesting tokenises normally, and a pooled-reuse test confirms depth does not leak
across uses. Verified the deep-input error is the depth-limit error specifically.
Full `pkg/jsonic` suite passes, as do the `pkg/oql` and `pkg/storage` consumers.

The related `config.MaxEntitySize` hygiene gap (validated only as `> 0`, no upper
bound) is noted in this entry but not the active guard here; it is left as a
separate prudence item.

---

### D-004 — `blob` SHA-addressed reads accept an unvalidated digest (panic; latent traversal)

**Package:** `pkg/blob`
**Location:** `store.go:546` (`blobPath`, `hexSHA[:2]`), reached via
`GetBySHA`/`getBySHA` (`store.go:308`/`312`) and `PutBySHA` (`store.go:199`)
**Detected by:** adversarial test — `GetBySHA(tenant, key, "")` panics with
`slice bounds out of range [:2] with length 0`.

`blobPath` slices the first two characters of the SHA for git-style prefix
sharding (`hexSHA[:2]`) and joins the digest into the on-disk path. The
SHA argument to `GetBySHA`/`PutBySHA` is passed straight through with **no
validation** — there is no length check and no hex check, unlike the caller-key
path, which is guarded by `validateKey`. Two consequences:

1. **Panic / DoS.** A SHA shorter than two characters (`""` or `"a"`) panics in
   `hexSHA[:2]`. The panic is in an exported method.
2. **Path components from the digest.** Because the digest is never constrained
   to hex, characters such as `.` and `/` reach `filepath.Join` in the SHA
   position. `filepath.Join` normalises the result, but a relative digest can
   still resolve outside the intended shard layout.

**Relationship to the documented blob-isolation debt.** The *cross-tenant read
by content hash* risk is already recorded under "Storage-layout normalization —
deferred items" (blob plane not tenant-aware; a leaked hash could allow
cross-tenant read). That entry concerns a **well-formed** hash and the
isolation policy. D-004 is the distinct **mechanism** gap: a **malformed** SHA
reaches the filesystem layer at all, giving a panic and an unvalidated path
component. The two should be fixed together when the blob plane is reworked.

**Why this is currently NOT remotely reachable.** `GetBySHA`/`PutBySHA` are not
wired to any HTTP handler in this revision — the blob handlers route exclusively
through `Put`/`Get` by key, and keys *are* validated. The methods are exported
and callable internally, and the blob handler already emits an
`X-Blob-SHA256`/`ETag` header; the moment a SHA-addressed retrieval endpoint is
added (a natural part of the deferred tenant-aware blob rework) the panic and
the unvalidated-path behaviour become remotely reachable.

**Candidate resolution (not yet decided).** Validate the digest at the entry of
`GetBySHA`/`PutBySHA`/`blobPath`: require exactly 64 lowercase hex characters
and return `ErrNotFound` (or a dedicated `ErrSHAInvalid`) otherwise, before any
slicing or path join. Fold this into the blob-plane tenant-isolation rework
rather than shipping it standalone.

**Current impact:** Low while unwired; a loaded gun for the blob-plane rework.

**Resolution (FIXED, 2026-06-20).** A boundary validator
`validateSHA256Hex` (exactly 64 lowercase hex characters) and a dedicated
`ErrSHAInvalid` were added in `pkg/blob/store.go`. It is called at the entry of
`getBySHA` and `PutBySHA`, before `blobPath` is reached — so a short digest no
longer panics in `hexSHA[:2]`, and a non-hex digest (path separators, `..`,
wrong length) is rejected cleanly instead of contributing path components to the
on-disk layout. The internal `Put`/`PutRaw` paths are unaffected: they compute
the digest with `sha256` and always produce valid lowercase hex.

This defect had no committed test in the audit bundle; a regression test was
written first (red), then the fix applied:
`pkg/blob/store_sha_validation_test.go` — `TestGetBySHA_ShortDigest_NoPanic`
(empty/short digests return a clean error, no panic, for both Get and Put),
`TestGetBySHA_NonHexDigest_Rejected` (path-bearing and wrong-length digests
rejected), and `TestGetBySHA_ValidDigest_RoundTrip` (a real 64-hex digest is
accepted). The broader blob-plane tenant-isolation debt this was to be folded
into is now itself **resolved in 0.13.0** (see "Storage-layout normalization —
deferred items"); the per-tenant layout removes the server-level shared blob
root entirely, so the malformed-digest guard now protects a per-tenant store
root. Full `pkg/blob` suite passes.

---

### D-005 — SQL injection via OQL JOIN field names (delimited identifier bypass) — HIGH

**Package:** `pkg/oql`
**Location:** `joinFieldRef` (`sqlgen_join.go:278`), blob branches at
`sqlgen_join.go:298` and `:309`, which call `SQLDialect.JSONFieldAliasedAs`
(`sqlgen.go:157` → `:159`/`:161`); the latter interpolates the field name into
`json_extract(<alias>.data, '$.<field>')` with `fmt.Sprintf`, no
parameterisation and no escaping.
**Detected by:** adversarial test — a JOIN query whose field identifier is a
T-SQL delimited identifier (`[ ... ]`) carrying a quote/paren breakout produces
SQL with attacker-controlled text outside any bound parameter.

**Root cause.** OQL field names are validated by `validateFieldName`
(`sqlgen.go:250`), whose allowlist regex `^[a-zA-Z_][a-zA-Z0-9_.]*$` correctly
rejects quotes, parentheses, semicolons and whitespace. The **single-table**
generator routes every field through `g.fieldPath()` (`sqlgen.go:582`), which
calls `validateFieldName`. The **JOIN** generator does not: `joinFieldRef` reads
the raw `Identifier.Value` / `QualifiedIdentifier` field part (`sqlgen_join.go`
~`:248`, `:365`, and the WHERE translator ~`:362`) and passes it to
`JSONFieldAliasedAs` for blob (non-adapted) entities **without ever calling
`validateFieldName`**. The adapted-entity branch is safe because it resolves
through `adaptedNativeColumn`, a column-existence lookup that returns `""` for
unknown names; only the blob branch interpolates the raw field.

The character-level breakout is supplied by the parser. tsqlparser
(`lexer/lexer.go:318`, `readBracketedIdentifier`) strips the surrounding
brackets and stores the inner text verbatim, so `[x') UNION SELECT ...--]`
becomes an `Identifier.Value` of `x') UNION SELECT ...--`. Double-quoted
delimited identifiers (`readQuotedIdentifier`, `:343`) behave the same way.

**Confirmed payloads (generated SQL, abridged):**

- SELECT-list field `a.[x') UNION SELECT data,_version FROM t0000_nodes--]`
  produces
  `SELECT json_extract(a.data, '$.x') UNION SELECT data,_version FROM t0000_nodes--') AS ...`
  — a `UNION` that exfiltrates the entire nodes table.
- WHERE field `a.[x' OR '1'='1]` produces a predicate containing
  `... OR '1'='1` outside any placeholder.

Both the SELECT and WHERE/ON clauses of a join route through `joinFieldRef`, so
the whole join surface is affected, not just the projection list.

**Reachability.** OQL is exposed over HTTP (graph/query handlers in
`pkg/server`) and via OQL event actions (`event_dispatch.go:386`,
`runOQLAction`). Blob storage (non-adapted entities) is the default, so the
vulnerable branch is the common case, not an edge configuration. Any caller able
to submit an OQL JOIN query can reach it. In shared-tenancy mode a `UNION` over
`t0000_nodes` crosses tenant boundaries; even in per-tenant mode it defeats
entity-type scoping within the tenant. This is an integrity/confidentiality
defect, not merely a panic or a defence-in-depth gap.

**Severity:** HIGH. Unlike D-002/D-003/D-004 (latent or secret-gated), this is
reachable by an authenticated-but-unprivileged query caller against the default
storage mode and yields arbitrary read (and, depending on the executing
statement context, potentially more) outside the intended query.

**Fix (mechanical, low-risk, recommended now — not deferred).** Route the JOIN
path through the same validation as the single-table path: call
`validateFieldName` on the field inside `joinFieldRef` before the blob-branch
`JSONFieldAliasedAs`, returning an error on rejection. This mirrors
`g.fieldPath()` and is consistent with the existing allowlist; it does not change
the layout or public API. A regression test is provided
(`sqlgen_injection_test.go`): bracketed-identifier payloads in both the SELECT
and WHERE positions must be rejected at generation time, and the single-table
path is asserted to already reject the same payload.

**Note on the parser.** The bracketed-identifier passthrough in tsqlparser is
correct T-SQL lexing (delimited identifiers are supposed to hold arbitrary
text); the defect is xolu trusting that text as SQL-safe on one code path. The
fix belongs in `pkg/oql`, not in tsqlparser.

**Resolution (FIXED, 2026-06-20).** `joinFieldRef` (`pkg/oql/sqlgen_join.go`)
now calls `validateFieldName(field)` at the top of the function, before the
alias switch — so every JOIN field, in both the SELECT and the WHERE/ON
positions and on both the blob and adapted branches, is validated against the
existing allowlist (`validFieldName` `^[a-zA-Z_][a-zA-Z0-9_.]*$` plus the
`dangerousFieldChars` blocklist) before it can reach `JSONFieldAliasedAs`. This
brings the JOIN path to parity with the single-table path (`g.fieldPath` →
`validateFieldName`). A bracketed-identifier breakout is rejected at generation
time with a non-nil error; no raw payload reaches SQL text. Dotted nested paths
(e.g. `address.city`) remain allowed, as on the single-table path. Regression
test: `pkg/oql/sqlgen_injection_test.go` (both JOIN payloads rejected; the
single-table control still rejects). Full `pkg/oql` and `pkg/server` suites
pass.

---

### D-006 — Timeseries request fields narrowed `int`→`uint8` before range validation

**Package:** `pkg/server` (timeseries handlers), validated downstream in
`pkg/timeseries`
**Locations:**
- `ts_handlers.go:886` — `NumField: uint8(req.NumField)`; validated as
  `q.NumField > 6` afterwards in `store.go:600`.
- `ts_handlers.go:407` — `Dims: uint8(req.Dims)`; validated as
  `cfg.Dims < MinDims || cfg.Dims > MaxDims` afterwards in `registry.go:113`.
**Detected by:** characterization test — request integers whose low byte lands
in the valid window pass the range check as a different value.

Both request structs declare the field as `int` (decoded from JSON), then
convert to `uint8` *before* the range check runs on the already-truncated value.
The high bits are discarded, so an out-of-range request can alias an in-range
one:

- `num_field`: `256 → 0`, `262 → 6`, `513 → 1` — all `≤ 6`, all pass the `> 6`
  guard. The request is served against the wrong numeric field.
- `dims`: `257 → 1` … `261 → 5` — all land in `[1,5]` and are accepted; the
  timeline is defined with a different dimension count than requested. (`256 → 0`
  is correctly rejected because 0 is below `MinDims`, so only the `257..261`
  band aliases.)

**Impact: low (correctness, not memory safety).** This is *not* an out-of-bounds
read: the aggregate path has a second guard, `int(q.NumField) >= len(nums)`
(`store.go:668`/`:887`), that clamps the field index against the actual array,
and the dims value is internally consistent with whatever was stored. The defect
is that an invalid request is silently accepted as a valid-but-different one
instead of being rejected — the caller's intent is changed with no error
returned. For `dims` this also persists: a timeline is created with the wrong
shape.

**Root cause is a single pattern.** Narrowing a request `int` to `uint8` before
validating its range. It appears in (at least) the two sites above; any future
handler that follows the same shape will inherit the bug.

**Candidate resolution (mechanical).** Validate the request value as an `int`
against its intended inclusive range *before* the `uint8` conversion, e.g.
reject `req.NumField < 0 || req.NumField > 6` and
`req.Dims < int(MinDims) || req.Dims > int(MaxDims)` at the handler, then
convert. Low-risk; does not change the wire format or the valid-input behaviour.
A characterization test is provided (`ts_numeric_validation_test.go`)
demonstrating the aliasing; it should be tightened to assert handler-level
rejection once the guard is added.

**Resolution (FIXED, 2026-06-20).** Both handler sites in
`pkg/server/ts_handlers.go` now validate the raw request `int` against its
intended range *before* the `uint8` conversion:

- `HandleTSAggregate`: rejects `num_field < 0 || num_field > 6` with a `400`
  (`XOLU-TS009`, the established num-field code) before building the
  `AggregateQuery`.
- `HandleTSDefineTimeline`: rejects `dims` outside `[MinDims, MaxDims]` with a
  `400` before constructing the `TimelineConfig`.

The characterization test was replaced with handler-level tripwires
(`ts_numeric_validation_test.go`, now package `server_test`): the aliasing
values that previously slipped past the downstream `uint8` guards — `num_field`
256/262/513 (→0/6/1) and `dims` 257–261 (→1–5) — are now asserted to return
`400`, with a control test confirming legitimate `num_field` 0–6 and `dims` 1–5
are still accepted. Verified as a genuine tripwire (fails with the guard
removed: the aliasing values return 200).

Two pre-existing tests were updated to match the now-earlier, more precise
rejection: `TestTSError_NumFieldOutOfRange` still expects `XOLU-TS009` (the guard
preserves that code), and `TestTSError_MalformedBody_Define/missing_dims` now
expects `400` instead of `409` — `dims=0` is a bad request, which the handler
previously could not discriminate from a conflict (the test's own comment
anticipated this). Full `pkg/server` and `pkg/timeseries` suites pass.

---

### D-007 — OQL-exposed scalar functions panic / produce non-serialisable values on edge inputs

**Package:** `pkg/qs` (function bodies), exposed via `pkg/oql/scalar.go`,
executed per-row by the OQL executor and marshalled in `handleOQLQuery`
**Detected by:** adversarial test — `SUBSTRING` with a negative length panics;
`ROUND` with a large precision yields NaN, which cannot be JSON-encoded.

Two reachable edge cases in functions registered on the OQL scalar surface
(`pkg/oql/scalar.go`) and callable from `SELECT`:

1. **`SUBSTRING` slice panic.** `ScalarSubstring` (`scalar.go:169`) computes
   `end := start + length` and clamps only `end > len(s)` (`:184`), not
   `end < start`. A negative length (`SELECT SUBSTRING(field, 2, -5)`) makes
   `end < start`, so `s[start:end]` panics with `slice bounds out of range`.
   The same applies across a range of start/length combinations.

2. **`ROUND` → NaN → response marshal failure.** `ScalarRound` (`scalar.go:317`)
   computes `shift := math.Pow(10, precision)` (`:331`); for precision ≳ 309 the
   shift overflows to `+Inf` and `math.Round(f*Inf)/Inf` is `NaN`. `ROUND` is
   OQL-exposed, so `SELECT ROUND(field, 400)` puts a `NaN` in the result row.
   `encoding/json` rejects `NaN`/`Inf`, so the entire response fails to marshal.

**Impact: low, contained by existing infrastructure.**
- The `SUBSTRING` panic is caught by chi's `Recoverer` middleware
  (`server.go:691`), so the process survives; the request returns a 500 rather
  than crashing the server. It is a robustness defect (a query function should
  return a value or a typed error, not panic), not an availability threat.
- The `ROUND`/`NaN` case is handled gracefully at the response layer: the
  marshal error is caught (`handlers.go:1117`) and converted to a clean
  `500 failed to encode response`. The query cannot return a result, but
  nothing crashes.

Both are reachable by any caller able to submit an OQL `SELECT` with these
functions; neither crosses a tenant or data boundary.

**Note on scope.** `SQRT` and `POWER` (which also produce NaN/Inf on
`SQRT(-1)` / `POWER(1e308, 2)`) exist in `pkg/qs` but are **not** registered in
`pkg/oql/scalar.go`, so they are not reachable through OQL today. Only
OQL-registered functions (`SUBSTRING`, `ROUND`, and any other numeric/index
function with unguarded edges) are in scope. Any future registration of an
unguarded `qs` scalar would extend this surface.

**Candidate resolution (mechanical).** Guard the edge cases at the function
boundary: in `ScalarSubstring`, clamp `end = max(end, start)` (or return `""`
when `end < start`); in numeric functions that can yield `NaN`/`Inf` (e.g.
`ROUND` with extreme precision), bound the precision argument or coerce a
non-finite result to `nil` before it reaches the result set. Returning `nil`
for non-finite scalar results would also make a future `SQRT`/`POWER`
registration safe by default. A regression test is provided
(`scalar_adversarial_test.go`): the `SUBSTRING` panic tests must go green
(clamped, no panic) after the fix.

**Resolution (FIXED, 2026-06-20).** Both edge cases are guarded in
`pkg/qs/scalar.go`:

- **`ScalarSubstring` panic.** After the existing upper-bound clamp, `end` is
  now clamped to `max(end, start)`, so a negative length yields an empty result
  instead of slicing backwards. No panic.
- **`ScalarRound` non-finite result.** The rounded value is checked with
  `math.IsNaN`/`math.IsInf` and coerced to `nil` (SQL NULL) when non-finite, so
  a large precision can no longer put a NaN/Inf into a result row and break
  response marshalling. (Per the audit's note, coercing non-finite scalars to
  `nil` would also make a future `SQRT`/`POWER` registration safe by default;
  those remain unregistered in `pkg/oql/scalar.go` and out of scope here.)

The `ROUND` test was tightened from characterization to a tripwire: it now
asserts the result is `nil` and JSON-serialisable. Full `pkg/qs`, `pkg/oql`, and
`pkg/server` suites pass.

**Follow-up (2026-06-20).** The fuzz target `FuzzScalarFunctions`
(`pkg/qs/fuzz_scalar_test.go`), added during the post-release property/fuzz
hardening pass, surfaced the same non-finite issue in `SQRT` (`SQRT(-1) = NaN`)
and `POWER` (`POWER(1e308, 2) = +Inf`). These are **not** registered on the OQL
surface (so they were correctly out of D-007's original scope), but they share
the `pkg/qs` scalar registry used by OQL and Sulpher. Per the audit's own
recommendation that coercing non-finite results to `nil` would make a future
`SQRT`/`POWER` registration safe by default, both now coerce `NaN`/`Inf` to `nil`
(`pkg/qs/scalar.go`), pinned by `TestScalarSqrt_NonFinite_CoercedToNil` and
`TestScalarPower_NonFinite_CoercedToNil`.

---

### D-008 — FSM functions panic on bad indices and allocate unboundedly (guard-driven DoS)

**Package:** `pkg/fsm/eval` (`functions.go`), reached via
`ExpressionEvaluator.Evaluate` → `FunctionRegistry.Call` (`evaluator.go:346`)
**Detected by:** a systematic panic-fuzz of all ~180 registered functions plus
bounded allocation probes. Across the whole registry, exactly two functions
panic on hostile arguments and three allocate without bound.

The FSM function library (extracted from aulsql; the same lineage as D-001 and
the `qs` scalars in D-007) is exposed to guard and set expressions. There is no
function allowlist: a guard may call any registered function.

**Panics (invalid slice).**
- `SUBSTRING` (`functions.go:247`): `length` is read from the third argument with
  no lower bound (`:239`); `end = start + length` is clamped only for
  `end > len(s)` (`:243`), not for `end < start`. A negative length
  (`SUBSTRING('hello', 1, -5)`) makes `end < 0`, so `s[start:end]` panics with
  `slice bounds out of range`.
- `STUFF` (`functions.go:449`): identical shape — `end = start + length`
  (`:444`) clamped only on the upper side, so a negative length panics in
  `s[end:]`.

**Unbounded allocation (OOM).**
- `REPLICATE` (`functions.go:481`): `strings.Repeat(s, n)` with `n` a
  user-supplied int guarded only `n < 0`. `REPLICATE('x', 1e12)` allocates ~1 TB.
- `SPACE` (`functions.go:495`): `strings.Repeat(" ", n)`, same.
- `STR` (`functions.go:517`): `length` becomes a `fmt` field width
  (`fmt.Sprintf("%%%d.%df", length, decimals)`); a huge width allocates a
  correspondingly huge padded string.

**Severity split.**
- The **panics** are caught per-request by chi's `Recoverer` (`server.go:691`):
  the process survives, the triggering request 500s. Robustness defect, low
  severity — consistent with D-007.
- The **allocations** are the more serious case. A large enough count produces
  `fatal error: runtime: out of memory`, which is **not** a `panic` and **cannot
  be caught by `recover()`** — `Recoverer` does not help. One evaluated
  `REPLICATE('x', <huge>)` can kill the whole server process. This is a
  guard-driven, process-wide DoS.

**Reachability.** FSM definitions are accepted over HTTP (`pkg/server`,
`v2_fsm_common.go`); each transition carries `guard`/set expression strings.
Definition validation is **parse-only** — "Guard and set expression syntax
(parse only, no evaluation)" (`v2_fsm_common.go:420`, `ParseGuard` at `:424`) —
so neither the panic nor the OOM fires at definition time. They fire when the
guard is **evaluated at transition time**, i.e. when an event drives the machine
through that transition. The expressions can be constant
(`REPLICATE('x', 1000000000) = ''`), so no attacker-controlled data is required
beyond the definition and an event that triggers the transition. An actor able
to register an FSM definition and fire a matching event can therefore OOM the
server.

**Note on existing coverage.** The existing `eval` adversarial suite
(`eval_adversarial_test.go`, `TestAdversarial_NoPanicOnHostileInput`) exercises
operator and structural hostility (chained equality, deep nesting, mixed
null/bool) but contains no function-call payloads, which is why this surface was
not previously caught. `pkg/fsm/eval/functions.go` (2082 lines, ~40% of the
package) had no adversarial coverage.

**Candidate resolution (mechanical).**
- `SUBSTRING`/`STUFF`: clamp `end = max(end, start)` (and `end ≥ 0`) before
  slicing, returning an empty/`NULL` result for a negative length, matching
  T-SQL semantics.
- `REPLICATE`/`SPACE`/`STR`: bound the count/width argument against a configured
  maximum (e.g. the existing query/response size limits) and return a clean
  error when exceeded, so a guard cannot request an arbitrarily large
  allocation. A shared helper for "function output size limit" would cover all
  three and any future allocator.

A regression test is provided (`functions_adversarial_test.go`): the
`SUBSTRING`/`STUFF` panic tests must go green after the fix; the allocation test
documents the unbounded behaviour and should assert a bounded error once a limit
is added.

**Resolution (FIXED, 2026-06-20).** Both sub-classes are closed in
`pkg/fsm/eval/functions.go`:

- **Slice panics.** `fnSubstring` and `fnStuff` now clamp `end = max(end, start)`
  after the existing upper-bound clamp, so a negative `length` yields an empty
  selection (matching T-SQL) instead of slicing backwards. No panic.
- **Unbounded allocation.** A package-level limit `maxFunctionOutputBytes`
  (16 MiB) and a shared `checkOutputSize(fn, n)` helper were added. `fnReplicate`
  (projected size `len(s)*n`), `fnSpace` (`n`), and `fnStr` (the format field
  width) check their projected output against the limit *before* allocating and
  return a clean error when it is exceeded — so a guard can no longer drive an
  out-of-memory fatal. The limit is well above any legitimate guard/set output.

The allocation test was tightened from characterization to a tripwire: it now
asserts an attack-scale count (`~1e12`) returns a clean error (no panic, no
allocation) and that small counts still succeed. Verified end-to-end: an
attack-scale `REPLICATE` surfaces through `EvalGuard` as
`REPLICATE output size … exceeds limit 16777216`. Full `pkg/fsm/eval` suite
passes.

---

### D-009 — DDL injection via JSON-schema field names (adapted-table registration) — HIGH

**Package:** `pkg/storage` (DDL construction), reached from `pkg/server`
**Location:** field name → column name verbatim in `DeriveAdaptedTableSpecFrom`
(`adapted.go:218`, from `schema.FieldNames()` at `:151`); interpolated into DDL
by `CreateTableSQL` (`dialect_sqlite.go:78`) and `schema_evolution.go`
(`ALTER TABLE … DROP COLUMN %s` `:221`, `CREATE … INDEX %s ON %s (%s)` `:251`);
executed by `RegisterAdaptedTable` (`adapted_crud.go:127`,
`db.ExecContext(ddl)`).
**Detected by:** end-to-end test — a schema property key
`evil TEXT); DROP TABLE t0000_nodes;--` becomes a column name and is emitted
into the `CREATE TABLE` verbatim.

**Root cause.** When an entity schema is registered, each JSON-schema property
key is turned directly into a SQL column name (`Name: fieldName`,
`adapted.go:218`) with **no identifier validation** — no allowlist, no quoting,
no rejection of non-identifier characters. The keys come straight from
`schema.FieldNames()` (`:151`), i.e. the `properties` object of the uploaded
schema, which is entirely caller-controlled. The resulting column name is
interpolated with `fmt.Sprintf("%s …")` into `CREATE TABLE`, `ALTER TABLE …
ADD/DROP COLUMN`, and `CREATE INDEX` statements, none of which can parameterise
an identifier, and all of which are executed via `ExecContext`.

**Confirmed output (abridged).** A property key
`evil TEXT); DROP TABLE t0000_nodes;--` yields:

```sql
CREATE TABLE … (
    evil TEXT); DROP TABLE t0000_nodes;-- TEXT,
    …
```

The key closes the column definition and the `CREATE TABLE` with `)`, then
introduces a second statement.

**The injection chains.** `modernc.org/sqlite` (the configured driver) executes
multiple `;`-separated statements in a single `ExecContext` call — confirmed by
test (a chained `DROP TABLE` in one `Exec` drops the table). So the payload is
not limited to corrupting the single `CREATE`; it can append arbitrary
DDL/DML — `DROP TABLE`, `ALTER TABLE`, `UPDATE`/`DELETE`, or `ATTACH DATABASE`
(which can create files on disk). This is materially more powerful than the
read-oriented `UNION` of D-005.

**Reachability.** `POST /api/v1/schema/{entity}` (`server.go:903`,
`handleCreateSchema`) decodes the schema body and calls
`RegisterAdaptedEntity` → `RegisterAdaptedTable` for the default `SQLiteStore`
(`handlers.go:1292`), which generates and executes the DDL. The `entity` URL
segment is validated (`validateEntityName`), but the **property keys in the body
are not**. The route is exempt from the tenant-context requirement in strict
mode (`server.go:376` allows the `/api/v1/schema/` prefix through), so the only
gate is global authentication: any authenticated caller, with no tenant scoping
or elevated privilege, can register a schema. The injected DDL runs against the
store backing the adapted tables.

**Severity:** HIGH — the most serious issue in this document. Unlike D-005 (read
via `UNION`, JOIN queries only), this permits destructive and schema-altering
SQL (`DROP`/`ALTER`/`ATTACH`) reachable by any authenticated user through a
single schema upload, against the default storage backend.

**Fix (recommended now).** Validate every schema field name as a strict SQL
identifier before it becomes a column or index name — reuse the same allowlist
discipline as OQL's `validateFieldName` (`^[a-zA-Z_][a-zA-Z0-9_]*$`), applied at
the boundary in `DeriveAdaptedTableSpecFrom` (and to index names/columns in
`schema_evolution.go`), rejecting the schema with a 400 on any non-identifier
key. Identifier quoting alone is insufficient and error-prone here; an allowlist
that rejects is safer. This is an additive guard, not a redesign.

**Regression guard.** The provided test
(`adapted_injection_test.go`) asserts a malicious field name is rejected at
derivation; the companion test pins the driver's multi-statement behaviour so
the chained-DDL assumption is documented. A property test over
`DeriveAdaptedTableSpecFrom` — for any property key, every emitted column/index
name matches the identifier allowlist — would be the stronger long-term guard,
matching the D-005 recommendation.

**Related:** D-005. Both are identifier-trust failures where one code path skips
a validation that the allowlist already expresses; D-005 is read-scoped in OQL
JOINs, D-009 is write/DDL-scoped in schema registration. They share a fix shape
(validate identifiers at the boundary) and should be closed together.

**Resolution (FIXED, 2026-06-20).** Identifier validation was added at both
boundaries:

- **Storage layer (primary fix).** `validateAdaptedFieldName` (allowlist
  `^[a-zA-Z][a-zA-Z0-9_]*$`) is called for every schema field name at the top of
  the per-field loop in `DeriveAdaptedTableSpecFrom` (`pkg/storage/adapted.go`),
  before any column or index name is derived. A non-identifier key now returns
  an error from derivation, so no injected DDL can be generated. Both derivation
  entry points are covered (`DeriveAdaptedTableSpec` delegates to `…From`).
- **HTTP layer (clean 400).** `validateSchemaFieldNames` runs in
  `handleCreateSchema` (`pkg/server/handlers.go`) immediately after JSON decode
  and *before* `LoadSchema`/`SaveSchema`, so a malicious schema is rejected with
  a `400` (`XOLU-ST003`) before any persistence, rather than failing late with a
  `500` after the schema is half-committed.

The allowlist is letter-first to match the existing entity/field-name
convention (`identifierRe`, OQL `validFieldName`). Regression tests:
`pkg/storage/adapted_injection_test.go` (derivation rejects; driver chains
statements) and `pkg/server/schema_injection_test.go`
(`TestHandleCreateSchema_MaliciousFieldName_Rejected` → 400;
`TestHandleCreateSchema_ValidFieldNames_Accepted` control). Full `pkg/storage`
and `pkg/server` suites pass.

---

No other open issues at this revision.

---

## Cross-cutting notes (D-005 – D-009)

These defects fall into two families, each with a light, low-risk fix shape and
a matching way to keep it from regressing. None of this is a remediation plan —
just the recommended direction.

### Input-validation defects (D-005, D-006, D-009)

Well-formed input is mishandled: a delimited identifier reaches SQL unescaped in
OQL JOINs (D-005); a schema field name reaches DDL unescaped (D-009); an
out-of-range integer is narrowed before it is checked (D-006). D-005 and D-009
are the same identifier-trust failure on two different write/read paths and
should be fixed together.

- **Fix shape.** Validate at the boundary the safe path already uses. D-005 and
  D-009: route the field/column name through an identifier allowlist (OQL's
  `validateFieldName`, `^[a-zA-Z_][a-zA-Z0-9_]*$`) — the allowlist already
  exists; the JOIN generator and the schema-derivation path each skip it. D-006:
  check the request `int` against its intended range *before* the `uint8`
  conversion. All are additive guards, not redesigns.
- **Regression guard.** Table-driven unit tests asserting the boundary rejects
  the bad value. For D-005/D-009 the stronger guard is a *property* test over
  the SQL/DDL generator: for any identifier, the emitted SQL must contain no
  unescaped quote, parenthesis, or semicolon from that identifier. A property
  check catches breakouts that a fixed payload list would miss.

### Hostile-input robustness defects (D-003, D-004, D-007, D-008)

Malformed or extreme input causes a panic or an unbounded allocation: deep JSON
nesting (D-003), a short/invalid SHA (D-004), out-of-range string indices and
non-finite numerics (D-007), negative lengths and unbounded `REPLICATE`/`SPACE`/
`STR` (D-008).

- **Fix shape.** Clamp or bound at the function boundary and return a value or a
  typed error instead of slicing/allocating blindly: clamp slice indices to
  `[0, len]` with `end ≥ start`; cap user-supplied repeat counts and format
  widths against a configured maximum; coerce non-finite numerics to `NULL`
  before they reach a result set; add an explicit depth counter to the
  tokeniser. The OOM cases (D-008 `REPLICATE`/`SPACE`/`STR`) deserve priority
  within this family: a fatal out-of-memory is **not** a `panic` and is **not**
  caught by chi's `Recoverer`, so it takes the whole process down, unlike the
  slice panics which currently degrade to a per-request 500.
- **Regression guard.** These are the textbook case for Go's native fuzzing
  (`go test -fuzz`). A fuzz target per boundary — the JSON tokeniser, the blob
  SHA path, the OQL/`qs` scalar functions, and the FSM function registry — with
  a simple invariant ("must not panic; output size bounded") will re-find this
  whole class automatically and persist any crasher into `testdata/` as a
  permanent seed. The one-off harnesses used to find D-007/D-008 can be promoted
  into such targets later; the committed regression tests
  (`scalar_adversarial_test.go`, `functions_adversarial_test.go`) already pin the
  specific cases found here.

### Auth-logic defects (D-002)

A security check is silently skipped because of a type assumption rather than a
malformed or out-of-range value: a JWT `exp`/`nbf` encoded as a non-numeric JSON
type fails the `.(float64)` assertion, so the expiry branch never runs (D-002).
This is neither an injection sink nor a hostile-input crash — the token is
well-formed and validly signed; the bug is that one encoding evades a control.

- **Fix shape.** Normalise the claim before comparison (decode with
  `json.Number`, or accept both a number and a numeric string) and decide
  explicitly whether a missing `exp` is permitted. The missing-`exp` decision
  changes auth semantics, so it is a design choice, not a mechanical edit.
- **Regression guard.** Unit tests asserting that a token with `exp`/`nbf` in a
  non-numeric type, or with the claim absent, is treated per the chosen policy
  (rejected when past / when required). Fuzzing does not help here — the input is
  well-formed; only a test that knows the intended security semantics catches it.

### Regression-coverage status

Committed regression tests now exist for every defect: D-002
(`auth_d002_test.go`), D-003 (`tokeniser_depth_test.go`), D-004
(`store_sha_validation_test.go`), D-005 (`sqlgen_injection_test.go`), D-006
(`ts_numeric_validation_test.go`), D-007 (`scalar_adversarial_test.go`), D-008
(`functions_adversarial_test.go`), and D-009 (`adapted_injection_test.go`).
D-005–D-009 came from the original audit bundle; D-002, D-003, and D-004 were
added when those defects were fixed (each written red-first). D-001 remains a
deferred design decision (see its entry) rather than a code defect with a guard.

### Scope note

Fuzzing covers the robustness family (D-003/D-004/D-007/D-008) but not the
auth-logic or input-validation families (D-002/D-005/D-006/D-009), which were
found by reading the relevant code paths and which random input does not
surface. Both bug
classes are present here, so both kinds of check are worth keeping:
property/unit tests for the logic defects, fuzz targets for the robustness
defects.

---

---
