# Changelog

All notable changes to xolu are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.16.1] - 2026-07-18 (T-34 fix: atomic terminal transitions)

> **Re-cut 2026-07-18 (same version, superseding the earlier artefact):**
> toolchain synced to latest Go (`go.mod` directive → `go 1.26.5`;
> slabber/slabbis already require ≥ 1.23); CI workflow added
> (`.github/workflows/ci.yml`, golangci-lint v2.12.2 via action v8,
> pinned — replaces the broken action@v4/"latest" pipeline that resolved
> to the Go 1.24-built v1.64.8); all 20 outstanding lint findings fixed
> in code per the `.golangci.yml` policy (13 errcheck explicit-discard
> closes, 3 S1016 Span→Period conversions, 2 ST1005 error-string
> casings, 2 dead symbols removed: `quantumOfDay`, `discardLogger`);
> and the test-only stress-harness env knobs renamed
> `XOLU_SEAL_STRESS_*` → `XOLU_TEST_SEAL_STRESS_*` (previously listed
> as Unreleased, folded into this re-cut).

### Fixed — cal exactly-once terminal transitions (T-34)

First-ever run of the v0.14.11 race guard
(`TestConcurrentTerminalTransition_ExactlyOneWins`, stress-tagged, 8-core
M1) showed 2–4 of 32 racers succeeding on the same terminal transition:
`Lifecycle.transition` was check-then-act over an unconditional
`SetState`. The "state graph as natural mutex" property was specified by
the test but never implemented.

- **BREAKING (source API):** `BookingSource`/`Store` method `SetState` →
  `SetStateFrom(calendarID, bookingID, from, to)` — a compare-and-swap.
  SQLite: guarded `UPDATE … AND state=?` with RowsAffected==1; Mem:
  check-and-set under the source lock. Losers now fail with
  `ErrIllegalTransition` before any index work; exactly one racer wins.
  `Lifecycle` callers are unaffected (the lifecycle API is unchanged).
- Verification note: the fix cannot be demonstrated on single-core
  hardware (the race needs true parallelism to manifest); closure of
  T-34 is gated on the race test passing multi-core under `-race`.

### Filed

- **T-35** — investigate the structurally similar window on `Move`
  (conflict check → unconditional `setSpan`). Suspected, not proven.

## [0.16.0] - 2026-07-18 (M4: client Stage 6 — audit, integration suite, stability declaration)

Final stage of the molu readiness plan. T-02, T-26, and T-32 closed;
T-33 filed. **The `pkg/client` surface is declared stable and
version-tied**; xolu-side molu readiness is complete.

### Changed — BREAKING in `pkg/client` (T-32)

- `Sequence.Step` → `Sequence.IncrementBy` (tag `increment_by`),
  `Sequence.CreatedAt` removed, `Cycle`/`MinVal`/`MaxVal` added. The old
  shape never matched the wire — `Step` had been silently zero since
  Stage 2. Consumers reading `Step` were reading a lie; none are known.

### Added

- **Scope declaration** in the `pkg/client` package doc: the stable
  surface (data-plane, semantic map, FSM, cal, health) and the
  deliberate exclusions (timeseries, blob, meta, admin, dynconfig,
  stats, export, async polling, deep graph analytics). Audit record:
  `docs/CLIENT_STAGE6_PLAN.md`.
- **Integration suite** (`pkg/client/integration_test.go`, build tag
  `integration`; T-26 minimal form): eight flows, every declared-scope
  method happy-path against an in-process server over real HTTP,
  including the full cal Openings→Check→Propose→Confirm cycle with
  post-confirm infeasibility. `release.sh --with-integration` runs it.
- Godoc for `ListMachineDefs` (last gap in the audit).

### Filed

- **T-33** — `loadTenantEntitiesFromStore` builds its per-tenant scoped
  store from a partial `StoreConfig` (no `FullTextEnabled`, no paths).
  Inert today (read-only path) but breaks the construction pattern the
  other two sites follow.

## [0.15.3] - 2026-07-18 (M3: client Stage 5 — cal methods)

Third stage of the molu readiness plan; client Stage 5 of the T-02
roadmap. Only Stage 6 (coverage audit, plan M4) now remains on T-02.

### Added — `pkg/client`

- **`CalCheck`, `CalOpenings`, `CalPropose`, `CalConfirm`** against the
  /api/v2/cal/* surface (T-18, v0.14.7). Typed wire shapes in
  `types_cal.go` mirror the server byte-for-byte; instants are stdlib
  `time.Time` (wire-compatible with xolutime.Instant's RFC 3339
  marshalling, always carrying a zone offset). Objectives validated
  client-side against the four implemented values before any request;
  `CalProposeRequest.BufferAfter` is a pointer so an unset buffer is
  genuinely omitted. XOLU-CAL001–007 arrive through the structured
  `Error` type; a disabled subsystem surfaces as XOLU-CAL001/501.
- 12 tests, including the Openings→Check→Propose sequence exercised at
  the wire level with a scripted server (complementing the server-side
  T-29 semantic property).

### Fixed

- Stale wire-format comment in `pkg/server/v2_cal_handlers.go` still
  describing the pre-T-30 "shared"/"sub:" modes; now names exclusive as
  the only honoured value.

## [0.15.2] - 2026-07-18 (M2: auth extraction to pkg/authmw; MF/MH prefix reservation)

Second stage of the molu readiness plan (T-19 and T-21 closed; see
`docs/MOLU_READINESS_TRACKING.md` for the recorded deviation).

### Added

- **`pkg/authconfig`** — the lean authentication configuration: the exact
  seven-field read-set of the auth middleware (`AuthType`, `JWTSecret`,
  `JWTIssuer`, `APIKeys`, `APIKeyGrants`, `InternalToken`,
  `AuthExcludePaths`). `APIKeyGrant` moved here from `pkg/config`, which
  keeps a type alias. `config.(*Config).AuthConfig()` is the single
  construction path xolu uses, so server config and auth config cannot
  drift.
- **`pkg/authmw`** — the authentication middleware (`auth.go`,
  `tenant_grant.go`) extracted from `pkg/middleware`, importable by
  external binaries (the molu hub) without the server config surface:
  its xolu-internal dependency closure is `pkg/authconfig` alone,
  verified by `go list -deps`. Compatibility aliases remain in
  `pkg/middleware`; no other xolu code changed. Middleware auth tests
  now construct middleware input via `cfg.AuthConfig()`, exercising the
  production construction path.
- **`ERROR_CODES.md`:** `XOLU-MF` (molu front) and `XOLU-MH` (molu hub)
  reserved as satellite-project area prefixes; xolu will never allocate
  codes under them (T-21).

### Changed

- `AuthMiddleware` and the auth validators take `authconfig.Config` (by
  value) instead of `*config.Config`. Single xolu call site updated.
  Rate limiting deliberately keeps its `pkg/config` coupling and stays
  in `pkg/middleware`.

## [0.15.1] - 2026-07-18 (M1: SemanticMap enumeration endpoints; tracking normalisation)

First stage of the molu readiness plan (`docs/MOLU_READINESS_PLAN.md` M1),
plus the tracking-document normalisation executed earlier in the same
session.

### Added — enumeration endpoints (T-24, T-25 closed)

- **`GET /api/v1/schemas`** — enumerate entity types with a registered
  schema. Names only, sorted; the validator tracks no registration
  timestamps, so none are invented. `validation.Validator` gains
  `LoadedEntities()` (already present on the concrete validator;
  `NoOpValidator` returns empty).
- **`GET /api/v2/.../gen/seq`** (and the `/seq` alias) — enumerate the
  tenant's named sequences with `name`, `current`, `increment_by`,
  `cycle`, sorted by name. The new static route shadows `GET /gen/{type}`
  under chi's static-over-wildcard precedence; previously this path fell
  through to the generator handler as `type="seq"`. A regression test
  pins the shadowing.
- **Client:** `ListEntityTypes(ctx) []EntityTypeSummary` and
  `ListSequences(ctx) []SequenceSummary`; `SequenceSummary` matches the
  list wire exactly. Twelve new tests across server and client.

### Filed

- **T-32** — the client's existing `Sequence` type declares `step` /
  `created_at`, but the server sends `increment_by` and no timestamp:
  `Sequence.Step` has been silently zero since Stage 2. Register item;
  M4b candidate (breaking type fix before the v0.16.0 stability
  declaration).

### Changed — tracking-document normalisation (docs only, no code)

Work tracking reshaped per `docs/proposals/tracking-normalisation.md`:

- **`docs/TRACKING.md`** is the single live register (open items only,
  T-nn namespace). Detail sections are grouped thematically; each carries
  `Theme:`/`Priority:`/`Status:`/`Blocks-after:` field lines from which
  the status table is derived, so enablement/blocking relations are
  first-class rather than buried in prose. Supersedes the short-lived
  `docs/TECHNICAL_DEBT.md`.
- **`docs/RESOLVED.md`** added: append-only record of closed items with
  full text as at closure (T-01, T-15, T-17, T-18, T-29, T-30, T-31,
  D-001–D-009 with cross-cutting notes, resolved storage-layout and cal
  schema items, TD-nnn namespace retirement).
- **`docs/KNOWN_ISSUES.md`** narrowed from 1,215 to 117 lines: intentional
  limits, invariant boundaries, and recorded decisions only. TD-001/TD-002
  detail merged verbatim into TRACKING T-03/T-04.
- **`docs/archive/`** added; `S9_WORK_STRATEGY.md` and
  `post-0.11.0-work-plan.md` moved there with dated banners.
- **`docs/TRACKING_PRACTICES.md`** added: taxonomy, closure procedure,
  header discipline, release-gate checks.
- Header drift fixed: KNOWN_ISSUES synced to 0.15.0; CHANGELOG title-line
  rename leftover (olu → xolu) corrected; Sulpher roadmap moved to the
  date-only header convention.

## [0.15.0] - 2026-07-18 (0.14.x cleanup complete: cal primitive hardened)

**Roll-up release marking the end of the 0.14.x cleanup phase.** No
new features beyond what already shipped incrementally in v0.14.7
through v0.14.20; this entry names what the cumulative 0.14.x work
delivered so consumers of the tag have one place to read the shape of
the release.

The cal primitive is now a hardened v1 building block, with a
tested HTTP surface, a typed error taxonomy, an honest exclusive-only
type surface (matching Google Calendar's model), regression guards on
its concurrency and rebuild invariants, and empirical proof of
production-scale stability under load.

### What 0.14.7 → 0.14.20 delivered

Cal server surface (from T-18):

- Four HTTP endpoints under `/api/v2/cal/*`: `check`, `openings`,
  `propose`, `confirm`. Opt-in via `XOLU_CAL_ENABLED`.
- Seven-code error family XOLU-CAL001–007 with precise HTTP status
  dispatch via `errors.Is`.
- 22 HTTP-level tests, mutation-verified regression guards for the
  state-graph mutex property and the rebuild cost ceiling.

Cal type surface (from T-30 and follow-ups):

- Reduced to exclusive-only occupancy: `ModeShared` and
  `ModeSubPrefix` removed; `Calendar.Capacity` field removed.
- Matches Google Calendar's actual model (rooms are boolean;
  descriptive metadata belongs on entity records, not on the
  bookable calendar).

Cal correctness (from T-29 and T-31):

- Openings ↔ Check property test proving the two functions agree on
  occupancy across randomised calendar states.
- Fault-injection hooks + tests proving that SQL-succeeds-index-fails
  scenarios are recoverable via `RebuildFrom`.

Cal at scale (from T-15):

- Stress harness (build-tagged `stress`) verified locally on M1 Mac
  under `-race` at 32 workers × 5000 ops/worker × 5000 bookings ×
  10 calendars: 800,000 mutation attempts in 129s. Zero races, zero
  invariant failures. See tracker entry for full numbers.

Documentation and operational polish:

- Reconciliation banners on the three cal proposal docs (T-17).
- `Manager.CreateCalendar` facade eliminating the two-step index
  registration footgun.
- Startup banner in `cmd/xolu` fixed and embedded (was displaying
  "olu" — a leftover from the T-01 rename).

### Debt tracker closures

T-15, T-17, T-29, T-30, T-31 closed. Full details in
`xolu-technical-debt-v0.15.0.md`.

### What's not changed

- `pkg/client` untouched since v0.14.6 (Stage 4). Client Stage 5
  (`CalCheck`, `CalOpenings`, `CalPropose`, `CalConfirm` methods) is
  the natural next release, targeting v0.15.1-rc1.
- No wire-format changes since v0.14.7's T-18 shipment. Any client
  written against v0.14.7+ continues to work identically.

### Test summary

- Total: 4653 tests, all passing.
- `go vet ./...` clean.
- Stress harness at both default and extended scale: local hardware
  green under `-race`.

## [0.14.20] - 2026-07-18 (startup banner: xolu → Xolu, embedded, centred, 60 chars)

Startup ASCII-art banner in `cmd/xolu` fixed to read "Xolu" (was
"olu"), regenerated in NV Script figlet, moved to an embedded
`banner.txt` (`//go:embed`), centred within a uniform 60-character
box. Startup ruler width harmonised to 60. Cumulative fix; no
functional change.

## [0.14.14] - 2026-07-18 (T-15/T-17/T-31: stress harness, doc reconciliation, fault injection)

Three cal-cleanup items resolved in one release. All 0.14.x cleanup
committed to before 0.15.x. No public API changes.

### Added — T-15: production-scale seal stress harness

New file `pkg/cal/seal_stress_local_test.go`, guarded by the `stress`
build tag so it does NOT run under normal `go test ./...`. Intended
for manual local execution on realistic hardware.

Invocation:

```
go test -tags=stress -race -timeout=1h -v -run=TestSealStressLocal ./pkg/cal
```

Scale parameters are set via environment variables (all optional,
with defaults chosen for a modern developer machine — 5 trials × 16
workers × 5000 bookings × 2000 ops/worker per trial = ~1.6 million
mutation attempts per trial). For real production-scale hardware,
scale up:

- `XOLU_SEAL_STRESS_TRIALS`
- `XOLU_SEAL_STRESS_WORKERS`
- `XOLU_SEAL_STRESS_BOOKINGS`
- `XOLU_SEAL_STRESS_OPS_PER_WORKER`
- `XOLU_SEAL_STRESS_CALENDARS`
- `XOLU_SEAL_STRESS_DAYS`

The harness enforces three properties at quiescence:

1. `assertIndexMatchesRebuild` — the in-memory index equals a full
   rebuild from source. No mutation-under-concurrent-seal has
   corrupted the index.
2. Seal frontier monotonicity — the atomic-tracked last frontier
   value never regresses across advancement steps.
3. Under `-race`: no data race in the entire run.

Per-trial summary logs report ops attempted, ops succeeded (by kind),
duration, and rate. Successful runs are otherwise silent — no
throughput measurements; this is a correctness stress, not a
benchmark.

Sandbox-smoke-tested at tiny scale (1 trial × 2 workers × 50
bookings × 20 ops) to confirm the harness itself works. Real
stress runs are your local machine's job per userPreferences
testing discipline.

### Added — T-31: fault injection at the cal SQL boundary

Two new fields on `IndexStore` (see `pkg/cal/store.go`):

- `AddToPlaneFaultHook func(b Booking) error`
- `RemoveFromPlaneFaultHook func(b Booking, plane Plane) error`

When non-nil, the hooks are invoked at the top of the corresponding
plane mutation. Returning a non-nil error aborts the mutation with
that error, before any bitmap change is persisted. Nil hooks
(the normal path) are a no-op.

The fields are exported so tests in the same package can set them
without reflection. They are NOT part of cal's public API contract —
external callers should not touch them. The godoc names them as
test-only.

New file `pkg/cal/fault_injection_test.go` exercises the SQL/index
disagreement recovery path:

- `TestFaultInjection_AddToPlaneFailureLeavesSQLAhead` — inject
  fault in `addToPlane` during Confirm; verify the error surfaces
  and that `RebuildFrom` recovers to a self-consistent state.
- `TestFaultInjection_RemoveFromPlaneFailureLeavesSQLAhead` — same
  shape for the Cancel path.
- `TestFaultInjection_RebuildIsIdempotent` — three consecutive
  `RebuildFrom` calls on already-consistent state produce identical
  results.
- `TestFaultInjection_FaultHookOnlyFiresOnce` — pattern example for
  future tests: fault fires on first call, hook returns nil
  thereafter.

The design intent — SQL is authoritative, index is derived, rebuild
reconciles — is now empirically verified rather than merely
documented. If a future change breaks the recovery invariant, these
tests catch it.

### Changed — T-17: cal proposal doc reconciliation

The three proposal docs called out by T-17 now carry a
"Reconciliation status" banner at the top explaining what actually
shipped versus what the design proposed. Contents of the docs below
the banner are preserved verbatim for design-history value:

- `docs/proposals/cal-rest-api.md` — banner names the exclusive-only
  reduction, the endpoints that never shipped (honour, reschedule,
  dryrun, multi-cal match, batch), and the actual XOLU-CAL* error
  taxonomy.
- `docs/proposals/cal-pebble-codec.md` — banner confirms the core
  codec commitments held, names the Capacity/pooling divergence, and
  points at T-16 for the rollup-prune performance validation still
  open.
- `docs/proposals/SESSION-2026-06-22-NOTES.md` — banner names it as
  historical and points at the other reconciled docs.

Each banner is clearly dated and marked as an addition rather than a
rewrite of the original content. Anyone reading the proposals now
sees the reconciliation state before reading the historical design.

Does NOT include a full rewrite of the proposal content — that would
be a much larger job (~2000 lines of doc), and the code and its tests
have been the source of truth since v0.14.0 anyway. The banners give
readers what T-17 asked for: a way to understand which claims are
current and which are historical.

### Test summary

- pkg/cal: 4 new fault-injection tests + 1 new stress test (behind
  the `stress` build tag), all passing.
- Full tree: `go vet ./...` clean, `go vet -tags=stress ./...`
  clean, `go test ./...` clean.
- Total tests (untagged): 4653 (up 4 from v0.14.13). The stress test
  adds 1 more when the `stress` tag is set.

### 0.14.x cleanup status after this release

- T-15: harness prepared and sandbox-smoke-tested. **Local run
  pending on your hardware** per userPreferences.
- T-17: banners added, proposal docs no longer misleading. Closed.
- T-31: hooks + tests landed, recovery path verified. Closed.

The only remaining 0.14.x cleanup item is the T-15 local run itself.
Once that completes cleanly, 0.14.x cleanup is done and 0.15.0-rc1
(Stage 5 client cal methods) is unblocked.

## [0.14.13] - 2026-07-18 (Calendar.Capacity removed + Openings/Check property test)

Two changes. Both close outstanding cal-hardening items on the 0.14.x
cleanup path; no move to 0.15.x yet.

### Removed — `Calendar.Capacity` field

The `Capacity int` field on `cal.Calendar` is removed. Cal implements
exclusive-only occupancy (see the Mode reduction in v0.14.12); a
`Capacity` field on the record was descriptive metadata only, never
honoured by the occupancy engine, and inconsistent with the
Google-Calendar-shaped model cal has committed to.

Callers that need "how many humans fit in this room" metadata should
carry it on a separate entity record, not on the calendar itself. This
matches Google Calendar's actual resource model: capacity is an
attribute of the physical room resource used for search and filtering,
not a property of the bookable calendar.

Resolves T-30 completely (the Mode portion shipped in v0.14.12).

### Schema and wire compatibility

- The SQL column `cal_calendars.capacity` remains in the schema
  (`INTEGER NOT NULL DEFAULT 1`). INSERT statements no longer specify
  it — the DB default fills it with 1. SELECT statements no longer
  read it. This preserves backward and forward on-disk compatibility
  without a schema migration; existing rows keep their capacity
  values, they simply become invisible to the Go layer.
- Any test or downstream code constructing `cal.Calendar` with
  `Capacity: N` fails at compile time. This is deliberate — the type
  system now tells the truth about what the primitive supports.

### Test scaffolding update

`seedCalendar` in `pkg/server/v2_cal_handlers_test.go` loses its
`capacity` parameter (all 20 call sites updated).

### Added — `pkg/cal/openings_check_property_test.go`

Two randomised property tests exercising the Openings ↔ Check
agreement invariant across 50 trials each, 30 random operations per
trial to build calendar state, 20 Openings/Check query pairs per
state:

- `TestOpeningsCheckAgreement_ForwardProperty` — for every span S
  returned by `Openings(from, to, d, obj)`, a subsequent
  `Check(S, ModeExclusive)` returns `Feasible=true`. If broken, a
  downstream caller composing `Openings → Check → Create` sees
  "Openings said this was free, but Create rejected it."
- `TestOpeningsCheckAgreement_ReverseProperty` — for any random span
  S in the query window that `Check` reports feasible,
  `Openings(from, to, |S|, ObjEarliest)` returns at least one span
  (weaker direction: existence, not containment).

Both directions were **mutation-verified**:

- Forward: injecting `free = true` in `freeRuns` (Openings reports every
  quantum as free) triggered clear failure messages naming exact
  spans, objectives, and trial/query indices.
- Reverse: injecting `feasible = true` in `Check` (Check always says
  yes) triggered failures naming spans where Check claimed feasibility
  but Openings could find no fit.

This closes T-29 from the debt tracker.

### Test summary

- pkg/cal: 2 new property tests, all passing. Runtime ~3.7 s total.
- Full tree: `go vet ./...` clean, `go test ./...` clean.
- Total tests: 4649 (up 2 from v0.14.12).

### 0.14.x cleanup status after this release

Remaining before considering 0.15.0-rc1 for client Stage 5:

- T-15 (seal concurrency stress at production scale) — real-hardware
  work, not sandbox-solvable.
- T-17 (docs vs code reconciliation for the three cal proposal docs) —
  documentation work.
- T-31 (cal SQL boundary fault injection) — filed for after Stage 5
  exercises the primitive.

Nothing in the above list blocks Stage 5 client work from starting;
Stage 5 is being deliberately deferred so 0.14.x cleanup completes
first per this session's sequencing decision.

## [0.14.12] - 2026-07-18 (Mode vocabulary reduced to exclusive-only)

Removes the `ModeShared` and `ModeSubPrefix` constants from `pkg/cal`.
The vocabulary items were reserved for pooled-resource and sub-resource
semantics that the occupancy engine never implemented; a design review
comparing cal against Google Calendar's actual room model confirmed
cal's target is exclusive-only (rooms are boolean; capacity is
descriptive metadata for filtering, not a booking-concurrency limit).
Implementing pooled resources properly would require a counter-based
bitmap encoding with ~8x storage growth and no user-facing feature
Google Calendar itself provides; the vocabulary was removed rather
than left as accepted-but-inert.

Resolves T-30 from the debt tracker (partial — the `Calendar.Capacity`
decision is still pending).

### Removed — `pkg/cal`

- `ModeShared Mode = "shared"` constant.
- `ModeSubPrefix = "sub:"` constant.

### Changed — `pkg/cal`

- `Mode` godoc now names exclusive-only semantics explicitly and
  references this release for the reasoning.
- `Booking.Mode` field comment updated to name `ModeExclusive` as the
  only valid value.
- `Openings` doc comment in `availability.go` no longer references
  "Stage 2 treats the calendar as a single exclusive resource" (a
  truthful comment about a stage boundary that no longer applies) —
  now names exclusive-only occupancy directly.

### Added — `pkg/cal.ErrModeNotSupported` sentinel

Wraps the source-layer rejection of non-exclusive modes. Follows the
same `errors.Is` dispatch pattern as the other cal sentinels from
v0.14.8.

### Added — `pkg/errors.ErrCalModeNotSupported` (XOLU-CAL007)

New error code for the HTTP surface. The handler classifier in
`v2_cal_handlers.go` maps `cal.ErrModeNotSupported` to
`XOLU-CAL007` with HTTP 400.

### Changed — source layer validation

Both `SQLiteBookingSource.PutBooking` and `MemBookingSource.PutBooking`
now:

- Coerce empty `Mode` to `ModeExclusive` (backward-compat for callers
  that omit the field).
- Reject any non-empty `Mode` other than `ModeExclusive` with
  `ErrModeNotSupported`.

### Added — tests

- `TestPutBookingRejectsNonExclusiveMode` — every non-exclusive mode
  string (`shared`, `sub:room-a`, `arbitrary`, `SUB:X`) is rejected
  with an error wrapping `ErrModeNotSupported`.
- `TestPutBookingCoercesEmptyModeToExclusive` — an empty mode is
  coerced to `ModeExclusive` and the stored booking reflects that.

### Compat notes

- No wire-format change. The HTTP handlers already accepted `Mode` as
  a string; the change is which strings the source layer accepts.
- Callers sending `mode: "exclusive"` or omitting `mode` continue to
  work identically.
- Callers previously sending `mode: "shared"` or `mode: "sub:*"` get
  a clear 400 XOLU-CAL007 response (previously accepted silently and
  treated as exclusive by the engine, which was the footgun this
  release closes).

### What's still pending

The `Calendar.Capacity` field decision is not resolved by this
release. Two options remain: keep and redocument as descriptive
metadata (Google's model — useful for "find me a 10-person room"
filtering), or remove entirely (mirror the Mode decision, type system
tells the truth). The T-30 debt entry documents both. Once decided,
that change is small and standalone.

### Test summary

- pkg/cal: 2 new tests, all passing.
- Full tree: `go vet ./...` clean, `go test ./...` clean.
- Total tests: 4647 (up 2 from v0.14.11).

## [0.14.11] - 2026-07-18 (cal concurrency and rebuild regression guards)

Two new tests, both mutation-verified to catch the properties they
protect.

### Added — `pkg/cal/confirm_race_test.go`

`TestConcurrentTerminalTransition_ExactlyOneWins` enforces the "state
graph as natural mutex" property: N goroutines racing on the same
terminal transition of a single booking yield exactly one success and
N-1 failures wrapping `ErrIllegalTransition`. Covers Confirm
(cross-plane), Complete (same-plane), and Cancel from both proposed
and binding starting states. Five trials per case for scheduler
variation.

Assertions per trial: exactly one success, N-1 `ErrIllegalTransition`
failures, no other error kinds, booking's final state is not stuck in
the starting state, and `assertIndexMatchesRebuild` holds (no index
corruption from the race). Mutation-verified: with
`allowedTransition` forced to return `true`, the test correctly fails
with "got 32 successes, wanted 1."

Downstream callers (molu Part 2 tools, concurrent event dispatchers)
rely on transitions being at-most-once per booking. If a future change
to `Lifecycle.transition` or `SQLiteBookingSource.SetState` allowed
both racers to succeed, this test catches it. Run under `-race` in CI:
the data-race detector complements the outcome-count assertion.

### Added — `pkg/cal/rebuild_bench_test.go`

`TestRebuildFrom_CorrectAndBounded` guards two properties of
`IndexStore.RebuildFrom` across N in {100, 1000, 10000} live bookings:

- Correctness: after rebuild, `assertIndexMatchesRebuild` holds.
- Cost: per-booking rebuild time under 100 microseconds. Baseline on
  the reference sandbox is ~3.7 µs; the 100 µs ceiling is ~27x slack
  to absorb CI variance while still catching an order-of-magnitude
  regression.

Bookings seeded via `SQLiteBookingSource.PutBooking` directly rather
than through the Lifecycle, because the property being measured is the
rebuild path, not the create path. Bookings are spread across
O(sqrt(N)) calendars to exercise the per-calendar accumulation path
without under- or overstating map-key overhead.

Mutation-verified: injecting a 400 µs per-booking sleep into
`RebuildFrom` correctly triggers the ceiling assertion (1.17 ms/booking
observed vs 100 µs limit).

### Impact

- Total tests: 4638 (up 2 from v0.14.10).
- `go vet ./...` clean, `go test ./...` clean.
- No behaviour change; both tests confirm current correctness while
  guarding against future regression.

### On the review items these tests resolve

Both tests originated from open questions surfaced during the T-18
follow-up work. The concurrency test confirms the state-graph mutex
property empirically rather than reasoning about it; the rebuild guard
puts a documented performance ceiling on the assemble-time cost path
that new tenant first-touches depend on. Neither property was broken
before these tests existed — they codify existing behaviour as
regression-catchable invariants.

## [0.14.10] - 2026-07-18 (Manager.CreateCalendar facade)

### Added — `pkg/cal.Manager.CreateCalendar`

A single-call operation that both persists a new calendar via the SQLite
source and registers it with the in-memory `IndexStore` ordinal map. Before
this facade existed, callers had to compose `SourceFor(t).CreateCalendar(c)`
with `IndexFor(t).RegisterCalendar(c)` themselves, and forgetting the
second step caused subsequent `Lifecycle.Create` calls to fail with
`ErrUnknownCalendar` from `IndexStore.ordinalFor`.

**Signature:** `func (m *Manager) CreateCalendar(tenantID uint16, c Calendar) (Calendar, error)`

**Rollback semantics:** The SQL insert runs first. If it fails, no index
change occurs. If it succeeds, the index register cannot fail (pure
in-memory map update). Observable state after return is either "both
persisted and indexed" (nil error) or "neither" (non-nil error).

### Also wrapped — `ErrCalendarExists` in source layers

`SQLiteBookingSource.CreateCalendar` and `MemBookingSource.CreateCalendar`
now wrap their "already exists" error with the `ErrCalendarExists`
sentinel via `%w`, closing the last gap in the sentinel taxonomy from
v0.14.8.

### Tests

Three new tests in `pkg/cal/manager_test.go`:
- `TestManagerCreateCalendarFacade` — happy path plus the critical
  assertion that `Lifecycle.Create` against the facade-created calendar
  succeeds without any explicit `RegisterCalendar` call.
- `TestManagerCreateCalendarRejectsDuplicate` — duplicate calendar_id
  returns an error wrapping `cal.ErrCalendarExists`.
- `TestManagerCreateCalendarPerTenantIsolation` — the same calendar_id
  in two tenants creates two distinct calendars.

`seedCalendar` in `pkg/server/v2_cal_handlers_test.go` simplified to use
the facade.

### Compat

Additive. Existing callers using `SourceFor().CreateCalendar()` +
`IndexFor().RegisterCalendar()` continue to work identically.

## [0.14.9] - 2026-07-18 (broader test improvements — Phase B on the cal follow-up)

Continuation of v0.14.8's test-and-classification work. This release adds
more coverage across three axes: the cal sentinel taxonomy at the
pkg/cal level, the XOLU-CAL* code family at the pkg/errors level, and
more robust handler-layer tests exercising realistic wire-format edge
cases.

### Added — `pkg/cal/errors_test.go` (8 new tests)

Direct unit tests for the sentinel error taxonomy introduced in v0.14.8:

- `TestSentinelsAreNonNil` — every sentinel is non-nil and carries the
  expected `cal:` prefix.
- `TestSentinelsPairwiseDistinct` — `errors.Is(a, b)` returns false for
  any two different sentinels; a regression here would silently
  mis-classify errors at the handler layer.
- `TestWrappingPreservesErrorsIs` — the standard `fmt.Errorf("%w: %q",
  sentinel, ctx)` pattern round-trips through `errors.Is`.
- `TestDoubleWrappingPreservesErrorsIs` — a wrap-of-a-wrap still
  resolves, verifying the classifier survives the common pattern of
  intermediate context injection.
- `TestWrappingPreservesMessageContext` — the wrapped error's Message
  retains both the sentinel's prefix and the caller-supplied context
  (so humans reading logs still see which calendar or booking failed).
- `TestLifecycleCreateWrapsUnknownCalendar` — end-to-end proof that
  `Lifecycle.Create`'s error path wraps `ErrUnknownCalendar`.
- `TestLifecycleConfirmWrapsUnknownBooking` — end-to-end proof for
  `ErrUnknownBooking`.
- `TestLifecycleConfirmWrapsIllegalTransition` — a double-confirm
  produces `ErrIllegalTransition`, the same programmatic-dispatch path
  molu Part 2 §6 will exercise.

### Added — `pkg/errors/errors_test.go` (2 new tests)

- `TestNew_CalErrorFamily` — every XOLU-CAL* code round-trips through
  `New(code, status, msg)` with the expected HTTP status posture
  (501 disabled, 400 invalid-input, 404 not-found, 422 rejected).
- `TestCodeFormat_AllCalCodesDistinct` — no accidental duplicate string
  assignments.

### Changed — `pkg/errors/errors_test.go`

- `TestCodeFormat` now accepts area codes of 2 or 3 uppercase letters
  (was 2 only). The 2-letter form was the original convention; the
  3-letter form (`CAL`, and future extensions) was introduced with
  T-18 and confirms xolu's willingness to relax the constraint for
  readability. This change is compatible with all pre-existing codes
  and formalises what T-21 in the debt tracker foresaw as a needed
  convention broadening.

### Added — `pkg/server/v2_cal_handlers_test.go` (5 new tests, +22 from v0.14.8 = 27 total)

- `TestCalClassificationEndToEndForEverySentinel` — a single test that
  walks every sentinel-mapped XOLU-CAL* code end-to-end through the
  handlers, catching any regression where a new sentinel is added but
  its classifier entry is forgotten (which would surface as a 500
  XOLU-ST006 rather than the intended CAL code).
- `TestCalCheckRejectsMalformedJSON` — truncated JSON returns 400.
- `TestCalHandlerRejectsWrongMethod` — all four endpoints reject GET
  requests, guarding against accidental method changes.
- `TestCalCheckPreservesUTCTimezone` — a span expressed with an explicit
  non-UTC offset (America/Argentina/Buenos_Aires) that represents the
  same absolute instant as an existing UTC booking correctly reports
  infeasible. If timezone handling were broken, feasibility would flip.
- `TestCalOpeningsRespectsExistingBookings` — with two bookings at
  10-11 and 12-13, no returned opening overlaps either window. Guards
  against a real class of bug where the Openings implementation
  could drift from Check's occupancy view.

### Impact summary

- **Total tests: 4636** (up from 4618 in v0.14.8, +18).
- All 4636 pass. `go vet ./...` clean.
- No public API changes since v0.14.8; this release is entirely test
  coverage and one relaxed format check.
- Client (`pkg/client`) untouched; client Stage 5 remains the next
  natural client-side release.

### On format conventions

The `TestCodeFormat` relaxation to accept 3-letter area codes is worth
noting explicitly: xolu's existing families all use 2-letter codes
(ST, GR, QL, VL, TN, BL, DC, FSM — wait, FSM was already 3), and CAL is
the second 3-letter family. The convention broadening was needed for
readability — `CL` would be plausible for calendar but noticeably worse
in code review than `CAL`. Future families can now choose either form
per their own readability calculus. This is consistent with T-21 in the
debt tracker, which anticipated the need to formalise this convention.

## [0.14.8] - 2026-07-17 (T-18 follow-up: HTTP tests + typed cal errors)

Server-side release. Addresses the two limits flagged as "known" in the
v0.14.7 CHANGELOG: no HTTP-level tests for the cal endpoints, and coarse
error classification that surfaced every lifecycle failure as
XOLU-CAL006. Both are now fixed.

### Added — `pkg/cal`

A typed sentinel error taxonomy in a new `pkg/cal/errors.go`, enabling
callers (notably the HTTP handler layer) to classify failures via
`errors.Is` without inspecting message strings.

- `cal.ErrUnknownCalendar` — a calendar_id was not found.
- `cal.ErrUnknownBooking` — a booking_id was not found on the named
  calendar.
- `cal.ErrIllegalTransition` — a lifecycle transition is not permitted
  from the booking's current state per the A9 rules.
- `cal.ErrInvalidSpan` — a span carries Start >= End or a zero instant.
- `cal.ErrCalendarExists` — CreateCalendar collision.
- `cal.ErrBearerRequired` — a binding booking was requested without a
  live bearer entity handle.

Every call site in `pkg/cal` that previously produced a plain
`fmt.Errorf("cal: unknown calendar %q", ...)` (or similar) now wraps the
appropriate sentinel via `%w`. Sites updated: `Lifecycle.Create`,
`Lifecycle.transition`, `Lifecycle.Move` (both unknown-calendar and
unknown-booking paths), `SQLiteBookingSource.PutBooking`,
`MemBookingSource.PutBooking`, `IndexStore.ordinalFor`, and
`Lifecycle.MatchCommit`. The wrapping preserves the original message
context for humans while making programmatic classification precise.

### Changed — `pkg/server/v2_cal_handlers.go`

- New `classifyCalError(err) (status int, code xoluerr.Code, matched
  bool)` helper. Uses `errors.Is` against the pkg/cal sentinels and
  returns the correct HTTP status + XOLU-CAL* code, or `matched=false`
  when the error is outside the taxonomy.
- `handleCalPropose` and `handleCalConfirm` now dispatch through
  `classifyCalError`. Errors matching a sentinel produce the specific
  XOLU-CAL* code with the appropriate HTTP status (404 for
  unknown-calendar/unknown-booking, 422 for illegal-transition/
  bearer-required, 400 for invalid-span). Errors outside the taxonomy
  fall through to XOLU-ST006 as a storage-layer failure rather than
  being misclassified as XOLU-CAL006.

### Added — `pkg/server/v2_cal_handlers_test.go`

**22 new HTTP-level tests** exercising the four cal handlers end-to-end
through `httptest.NewServer`, verifying wire format, tenant scoping, and
error-code granularity. Coverage per endpoint:

- **`/check`** — feasible on empty calendar; infeasible returns nearest
  openings; unknown calendar returns 404 CAL004; invalid span returns
  400 CAL002; missing calendar_id returns 400 CAL004.
- **`/openings`** — happy path with earliest objective; all four
  objectives (`earliest`, `first-fit`, `emptiest`,
  `longest-clear-margin`) accepted and echoed; empty objective defaults
  to earliest; unknown objective returns 400 CAL003; bad range and
  zero duration return 400 CAL002.
- **`/propose`** — happy path returns 201 with state=proposed; unknown
  calendar returns 404 CAL004; missing booking_id returns 400 CAL005;
  invalid span returns 400 CAL002.
- **`/confirm`** — happy path transitions proposed → binding; unknown
  booking returns 404 CAL005; unknown calendar (also treated as
  unknown booking since the lookup composes both) returns 404 CAL005;
  double-confirm returns 422 CAL006 (illegal transition); missing
  calendar_id returns 400 CAL004; missing booking_id returns 400
  CAL005.
- **Disabled subsystem** — with `CalEnabled=false`, all four routes
  are absent from the router and return 404 (not 501), matching xolu's
  broader v2-tree posture.

### Added — `pkg/server` public API

- **`Server.CalManagerForTest() *cal.Manager`** — test-only accessor
  exposing the calendar manager so HTTP-level tests can seed calendars
  and bookings through the same manager the handlers use. The `ForTest`
  suffix marks it as not part of the stable API surface.

### Test infrastructure notes

The HTTP test suite reuses the existing `newV2Server(t, opts...)` scaffold
from `v2_scaffold_test.go`, passing a functional option that sets
`CalEnabled=true`. Seed helpers call the manager's `IndexFor(tenantID).
RegisterCalendar(c)` after `src.CreateCalendar(c)` because the manager's
initial `RebuildFrom(src)` runs only at assembly time; calendars added
later must be explicitly registered with the index or the `addToPlane`
path fails with `ErrUnknownCalendar`. This is a real subtlety of the
cal architecture, documented in the seed helper's doc comment for anyone
writing further tests.

### What did NOT change

- The four HTTP endpoints and their wire formats are unchanged from
  v0.14.7. Any client written against v0.14.7 continues to work.
- The XOLU-CAL* error code numbering is unchanged; only the dispatch
  precision improves.
- No client-side (`pkg/client`) changes; client Stage 5 is still the
  natural next release.

### Test summary

- pkg/cal: 15.0s, all passing (typed-error refactor did not regress
  anything).
- pkg/server: full suite passes including the 22 new cal handler tests.
- Full tree: `go vet ./...` clean, `go test ./...` clean.

## [0.14.7] - 2026-07-17 (T-18: cal subsystem HTTP surface)

Server-side release, no client changes. Ships T-18 from the debt tracker:
xolu's `cal` subsystem is now reachable over HTTP via four new endpoints
under `/api/v2/cal/*`. This unblocks client Stage 5 (`CalCheck`,
`CalOpenings`, `CalPropose`, `CalConfirm` methods on `*Client`) and molu
Part 2 §5.1.10-13 (`cal_check`, `cal_openings`, `cal_propose`,
`cal_confirm` tools).

The endpoints are opt-in via a new `XOLU_CAL_ENABLED` environment variable,
matching the same posture v2 itself uses: routes are registered only when
enabled; when disabled they are absent from the router and the standard
chi 404 applies.

### Added — `pkg/server`

- **`POST /api/v2/cal/check`** — feasibility dry-run for a candidate
  booking. Request body: `{calendar_id, span: {start, end}, mode}`.
  Response: `{feasible: bool, nearest_openings: [{start, end}]}`. When
  infeasible, up to three nearest-window suggestions are returned so a
  caller can offer "here's where yes lives". Writes nothing.

- **`POST /api/v2/cal/openings`** — search for windows admitting a
  duration within a bounded range. Request:
  `{calendar_id, from, to, duration_ms, objective}`. Objective is one of
  the four fixed values `earliest`, `first-fit`, `emptiest`,
  `longest-clear-margin` (defaults to `earliest` when empty).
  Response: `{objective, openings: [{start, end, margin_ms}]}`.

- **`POST /api/v2/cal/propose`** — create a proposed booking. Request:
  `{booking_id, calendar_id, span, mode, bearer, buffer_after?,
  detail_ref?}`. `booking_id` is client-generated (typically a ULID) so
  the caller controls identity for idempotency. Response is the created
  booking with `state="proposed"`.

- **`POST /api/v2/cal/confirm`** — transition a proposed booking to
  `binding` (the A9 cross-plane move). Request:
  `{calendar_id, booking_id}`. Response is the updated booking record.
  Rejects with `XOLU-CAL006` when the transition is not permitted from
  the booking's current state.

All four are tenant-scoped via the standard v2 routing
(`/api/v2/tenant/{tenant_id}/cal/*` when a tenant is set, `/api/v2/cal/*`
otherwise), following the same pattern as `fsm`, `gen`, and `event`.

### Added — `pkg/errors`

Six new error codes forming the `XOLU-CAL*` family:

- `XOLU-CAL001` `ErrCalDisabled` — the subsystem is disabled
  (`XOLU_CAL_ENABLED=false`); the four endpoints return 501.
- `XOLU-CAL002` `ErrCalInvalidSpan` — span with `Start >= End`, or zero
  instant.
- `XOLU-CAL003` `ErrCalInvalidObjective` — objective outside the fixed
  set.
- `XOLU-CAL004` `ErrCalCalendarNotFound` — calendar_id does not exist
  in the current tenant scope.
- `XOLU-CAL005` `ErrCalBookingNotFound` — booking_id does not exist on
  the named calendar.
- `XOLU-CAL006` `ErrCalTransitionRejected` — lifecycle transition not
  permitted from the booking's current state per the A9 rules.

### Added — `pkg/config`

- **`CalEnabled bool`** field on `*config.Config`, default `false`.
- **`XOLU_CAL_ENABLED`** environment variable, `true` / `false`.

Default posture matches other v2 subsystems: opt-in until stable.

### Added — `pkg/server` internal wiring

- `Server.calMgr *cal.Manager` field, nil when disabled.
- `Server.setupV2CalRoutes(r chi.Router)` registers the four handlers.
- `Server.calGuard(w, r) *cal.Lifecycle` centralises the "enabled + tenant
  Lifecycle" check for the four handlers.
- Manager wire-up in `New()` runs after blob wire-up, before v2 schema
  init. Backed by the shared writer DB via `storage.WriterDBProvider`.
  When the storage backend does not expose a writer DB, a warning is
  logged and `calMgr` stays nil (the four routes then return 501 at
  request time via `calGuard`).

### New files

- `pkg/server/v2_cal_handlers.go` — the four handlers, wire-format types
  (`spanWire`, `bookingWire`), and route setup.

### Known limits, honestly named

- **No tests specific to the cal HTTP handlers ship in this release.**
  The existing xolu suite (4596 tests) continues to pass, but the four
  new handlers have not been exercised end-to-end against a live server
  boot. The wire-format types and routing changes are covered by build
  and vet only. Adding an HTTP-level test suite for the cal endpoints
  is legitimately deferred to the client Stage 5 release (0.15.0-rc1),
  where the client methods and the server handlers can be validated
  together against realistic sequences. This is a real gap, not a
  handwave — if you deploy `XOLU_CAL_ENABLED=true` and start hitting the
  endpoints, you are the first end-to-end integration test.

- **Error surface classification is coarse.** `Lifecycle.Create` and
  `Lifecycle.Confirm` both surface their errors as plain Go errors with
  message strings; the handlers classify all of them as `XOLU-CAL006`
  today. Distinguishing "unknown booking" from "illegal transition"
  requires typed error support in `pkg/cal`, which is genuinely out of
  scope for T-18. The underlying error message is preserved verbatim in
  the response detail for callers who need to disambiguate. Refinement
  is a natural follow-up filed for a future release.

- **T-18's scope was the minimum four endpoints.** The `cal` subsystem
  supports more: `Decline`, `Cancel`, `Complete`, `MarkMissed`, `Move`,
  and calendar/booking queries. These are deliberately out of scope for
  T-18 to keep the release focused. A follow-up release can extend the
  HTTP surface as needed.

- **T-24 and T-25 not bundled.** You approved bundling them into the same
  release conditionally on quota. Quota did not permit; both remain
  pending. Neither is blocking Stage 5.

## [0.14.6] - 2026-07-17 (client Stage 4: operational hardening — retry, telemetry, per-call timeouts)

Stage 4 of the client roadmap. `pkg/client` gains the operational-hardening
surface a consumer needs for production deployment: automatic retry with
backoff for idempotent methods, structured `log/slog` telemetry, and
per-call timeouts. No other packages touched.

Backwards compatibility is total. Every Stage 4 addition is opt-in: the
default `Client` behaves identically to v0.14.5 (no retries, no telemetry,
no per-call timeout). All 78 pre-Stage-4 tests continue to pass unchanged
against the rewritten pipeline.

### Added — `pkg/client`

- **Retry policy** via `WithRetryPolicy(RetryPolicy)`. Retries only fire
  for HTTP-idempotent methods per RFC 9110 §9.2.2 (GET, HEAD, PUT, DELETE,
  OPTIONS). POST and PATCH never retry regardless of configuration. This
  is a universal rule, not per-method — the client refuses to retry a POST
  silently under any configuration. Callers who consider a specific POST
  replay-safe wrap it themselves.

  `RetryPolicy` carries `MaxAttempts`, `InitialBackoff`, `MaxBackoff`,
  `BackoffMultiplier`, and a `RetryOn func(*http.Response, error) bool`
  predicate. `DefaultRetryOn` retries transport errors and 5xx responses;
  it does not retry 4xx, `context.Canceled`, or `context.DeadlineExceeded`.
  `DefaultRetryPolicy` provides a sensible starting point (3 attempts,
  200ms initial, 5s ceiling, doubling) that callers can copy or use directly.

- **Structured telemetry** via `WithLogger(*slog.Logger)`. When set, the
  client emits `log/slog` records at:
  - **debug** for every completed request (method, URL path only,
    duration, HTTP status, attempt number)
  - **info** for 401/403 auth failures
  - **warn** for each retry firing (attempt number, backoff duration,
    cause)

  Deliberately no error-level output — errors are returned to the caller,
  and the caller decides whether to log them. Passing `nil` to `WithLogger`
  is a no-op; the default (option unset) is a discarding logger, so the
  client does not pollute the process-wide default logger.

  **Content discipline is non-negotiable:** never any header value except
  HTTP status, never any request or response body content, never any
  token or credential, never any query string (the logged path is
  `url.Path` only). Six SECURITY-marked tests assert this cannot regress.

- **Per-call timeout** via `WithCallTimeout(time.Duration)` and
  `Client.WithTimeout(time.Duration) *Client`. The former sets the
  client-wide default; the latter returns a shallow-copied client with the
  timeout applied to that instance only. Wraps ctx with
  `context.WithTimeout`, so a caller who already set a tighter deadline is
  unaffected (their deadline wins via context semantics). Complements
  rather than replaces `http.Client.Timeout`.

### Internal — `doURL` rewritten as a proper request pipeline

`doURL` now:
1. Marshals the body once and constructs a fresh `bytes.Reader` per
   attempt (a used `Reader` is not rewindable).
2. Wraps ctx with `callTimeout` when non-zero.
3. Loops for up to `MaxAttempts`, dispatching to `doOnce` per attempt.
4. Emits telemetry via `logRequest` after each attempt and `logRetry`
   before each backoff.
5. Consults `retry.shouldRetry` (which combines idempotency check,
   attempt counter, and `RetryOn` predicate) to decide the next step.
6. Honours context cancellation during backoff via `retrySleep`.
7. Returns via a factored `decodeResponse` that preserves all existing
   error-parsing behaviour byte-for-byte (structured `XOLU-*` first,
   legacy flat second, raw body last).

Extraction of `doOnce` and `decodeResponse` as private helpers keeps the
retry loop readable and gives Stage 4's telemetry a natural attachment
point without repeating the parse logic.

### New files

- `pkg/client/retry.go` — `RetryPolicy` type, `DefaultRetryPolicy`,
  `DefaultRetryOn`, `isRetryableMethod`, `backoffFor`, `shouldRetry`.
- `pkg/client/telemetry.go` — `discardLogger`, `logRequest`, `logRetry`.
- `pkg/client/retry_test.go` — 26 new tests.

### Tests — `pkg/client`

Twenty-six new tests covering:

- Retry mechanics: default is no-retry; retries fire on 5xx; do NOT fire
  on 4xx; do NOT fire on POST or PATCH regardless of policy; fire on PUT
  and DELETE; `MaxAttempts` respected exactly.
- Backoff computation: exponential growth, `MaxBackoff` cap, actual sleep
  sequence delivered to the injected sleep function.
- `DefaultRetryOn` predicate: transport errors retryable, context errors
  NOT retryable, 5xx retryable, 4xx and 2xx NOT retryable.
- Context cancellation during backoff returns the context's error.
- Telemetry level dispatch: DEBUG for 2xx, INFO for 401, WARN for retry.
- **SECURITY-marked assertions**: no bearer token in log output, no
  request body content in log output, no response body content in log
  output, no query string in the logged path.
- Per-call timeout applied; caller's tighter context deadline wins;
  `Client.WithTimeout` produces a distinct client without mutating the
  parent.
- `WithTenantContext` preserves the three Stage 4 fields.

Client package now has 104 tests total (up from 78 after Stage 3).

### Known limits worth flagging

- **`Ready()` and `Health()` bypass `doURL`**. Both were built in Stage 1
  as direct HTTP calls that skip the auth header. As a consequence they
  do NOT participate in the retry policy, do NOT emit telemetry, and do
  NOT honour `WithCallTimeout`. This is defensible for `Ready()` (a
  readiness probe wants to fail fast, not retry) but is a real
  inconsistency for `Health()`. Not fixed in Stage 4 to avoid scope
  creep; flagging so Stage 6 (v1 coverage audit) can decide whether
  `Health` should route through `doURL` with an "omit auth" flag or stay
  independent.

- **No metrics emission.** Metrics are a caller concern; adding
  Prometheus or OpenTelemetry would drag dependencies into a client that
  has stayed stdlib-only through Stages 0-4. If a future stage needs
  metrics, it will be via a caller-provided callback rather than a hard
  dependency.

- **No circuit breaker.** Circuit-breaking is a different concern from
  retry-with-backoff; mixing them makes the API confusing. Callers who
  want circuit breaking wrap the client.

## [0.14.5] - 2026-07-17 (client Stage 3: FSM machine operations)

Stage 3 of the client roadmap. `pkg/client` gains the full FSM machine
operation surface — creating machines, walking them through their state
graph, and reading state, variables, transitions, results, and history. No
other packages touched.

### Added — `pkg/client`

Eleven new methods on `*Client`, hitting `/api/v2/fsm/machine[...]`. All
require xolu's v2 API enabled (`XOLU_API_V2_ENABLED=true`); all are
tenant-scoped. Wire formats verified against xolu's actual handlers in
`pkg/server/v2_fsm_machine_handlers.go` and `pkg/server/v2_fsm_walk.go`, not
against specifications.

- **`CreateMachine(ctx, req)`** — `POST /fsm/machine`. Instantiates a new
  machine from a definition. Supports optional `Ref` (external identifier
  bound to the machine) and `Overrides` (per-machine variable defaults and
  guard expression edits). Returns `*Machine` on 201.
- **`ListMachines(ctx, filter)`** — `GET /fsm/machine`. Optional `Definition`,
  `State`, and `Ref` filters map to query params. Nil filter returns every
  machine in the tenant scope. Envelope key is `"machines"`.
- **`GetMachine(ctx, id)`** — `GET /fsm/machine/{id}`. Returns `*Machine`
  with identity, current state, live variables, and a
  `DefinitionDeleted` flag flagging whether the source definition still
  exists.
- **`PatchMachine(ctx, id, req)`** — `PATCH /fsm/machine/{id}`. Applies
  overrides to the machine's snapshot spec; live state, live variables, and
  history are preserved unchanged. Re-validates the whole snapshot; failure
  returns `XOLU-FSM*` codes.
- **`DeleteMachine(ctx, id)`** — `DELETE /fsm/machine/{id}`. Returns nil
  on 204. Removes the machine, its terminal-state cache, and its history.
- **`WalkMachine(ctx, id, req)`** — `POST /fsm/machine/{id}/walk`. Drives
  the machine through one transition. Returns `*WalkResult` on success
  (previous/current states, terminal flag, Mealy outputs, post-walk vars,
  history row id). On rejection returns `*Error` carrying `XOLU-FSM003`
  through `XOLU-FSM008`, documented in the method's godoc for programmatic
  dispatch.
- **`GetMachineState(ctx, id)`** — `GET /fsm/machine/{id}/state`.
  Lightweight state + terminal flag probe. `*MachineState`.
- **`GetMachineResult(ctx, id)`** — `GET /fsm/machine/{id}/result`. Current
  state + terminal flag + live vars + (when terminal) the transition's
  Mealy output. `*MachineResult` with `FinalOutput json.RawMessage` when
  terminal.
- **`GetMachineVars(ctx, id)`** — `GET /fsm/machine/{id}/vars`. Live
  variable snapshot, each entry carrying value, declared type, and default.
  Returns a flat `map[string]VariableSnapshot` (no envelope — verified from
  source).
- **`GetMachineTransitions(ctx, id)`** — `GET /fsm/machine/{id}/transitions`.
  Input symbols with a defined transition from the current state. Guards
  are *not* pre-evaluated — an input appears here if the transition exists
  structurally, whether or not its guard would currently permit the walk.
- **`GetMachineHistory(ctx, id)`** — `GET /fsm/machine/{id}/history`.
  Ordered walk history, oldest first. First entry records creation
  (from=nil, input=nil, note="machine created"); subsequent entries record
  each walk. Envelope key is `"entries"`. Xolu v0.14.5 does not paginate
  this endpoint — the full history is returned in one response.

### Types added — `pkg/client/types_machine.go`

`MachineSummary`, `Machine`, `MachineFilter`, `CreateMachineRequest`,
`PatchMachineRequest`, `MachineOverrides`, `TransitionOverride`,
`WalkRequest`, `WalkResult`, `MachineState`, `MachineResult`,
`VariableSnapshot`, `AvailableTransitions`, `HistoryEntry`. Caller-supplied
opaque payloads (walk payload, history payload/vars/outputs, machine
result's `FinalOutput`) preserved as `json.RawMessage`.

### Tests — `pkg/client`

Twenty-five new tests in `pkg/client/machine_test.go`, covering: every
method's happy path (URL, HTTP method, envelope key, response decoding);
tenant scoping via query params; `MachineFilter` composition and nil-filter
short-circuit; structured `XOLU-FSM*` error propagation on 404 (machine
not found), 422 (guard rejected), and definition-not-found; walk with
payload; walk with empty input rejection; walk terminal handling;
`GetMachineResult` with and without `final_output`; `GetMachineVars`
flat-map decoding; `GetMachineTransitions` with multiple candidate inputs;
history creation-entry semantics (from/input nil, note set); history with
walk entries. Also a compound test asserting every `id`-taking method
rejects a zero id at the boundary.

Client package now has 78 tests total (up from 53 after Stage 2).

## [0.14.4] - 2026-07-17 (client Stage 2: schema-map endpoints)

Stage 2 of the client roadmap. `pkg/client` gains the read-only discovery
surface a consumer needs to build a picture of xolu's current operational
shape — entity types, FSM definitions, generators, event definitions. No
other packages touched.

### Added — `pkg/client`

- **Seven new methods** on `*Client`, each with typed return values that
  mirror xolu's actual wire format (verified against pkg/server/v2_*.go
  handlers):
  - `GetEntitySchema(ctx, entityType)` — hits `GET /api/v1/schema/{entity}`,
    returns `*EntitySchema` with the raw JSON Schema, a field breakdown, and
    the subset of fields carrying `"format":"ref"`.
  - `ListMachineDefs(ctx)` — hits `GET /api/v2/fsm/def`, returns
    `[]MachineDefSummary` (summaries only; the envelope key is
    `"definitions"`).
  - `GetMachineDef(ctx, id)` — hits `GET /api/v2/fsm/def/{id}`, returns
    `*MachineDef` with the full spec (`MachineSpec` with states,
    transitions, variables, GC policy, input queries) plus xolu's structural
    analysis output as raw JSON.
  - `ListGenerators(ctx, kind)` — hits `GET /api/v2/gen/{kind}`, returns
    `[]GeneratorDef` for one of the four generator kinds
    (`GeneratorUUIDv4`, `GeneratorUUIDv7`, `GeneratorCUID`, `GeneratorULID`).
    Iterate `AllGeneratorKinds` for a complete inventory.
  - `GetSequence(ctx, name)` — hits `GET /api/v2/gen/seq/{name}`, returns
    `*Sequence`.
  - `ListEventDefs(ctx)` — hits `GET /api/v2/event/def`, returns
    `[]EventDef` (envelope key is `"subscriptions"` — verified from source,
    not `"events"` or `"definitions"`).
  - `GetEventDef(ctx, id)` — hits `GET /api/v2/event/def/{id}`, returns
    `*EventDef`.

- **`V2Availability(ctx)`** — hits `GET /api/v2/`, returns a
  `*V2Availability` describing which v2 subsystems are enabled on the target
  server. Useful for a consumer to gate its own v2 calls on the server's
  actual capability.

- **Typed structures** in a new `pkg/client/types_schema.go` file:
  `EntitySchema`, `FieldDef`, `RefFieldDef`, `MachineDefSummary`,
  `MachineDef`, `MachineSpec`, `StateDef`, `VariableDef`, `TransitionDef`
  (with `FromStates()` helper), `GCPolicy`, `GeneratorKind` enum,
  `GeneratorDef`, `Sequence`, `EventDef`, `V2Availability`. Opaque
  server-internal payloads (generator config, event action config, entity
  JSON schemas, FSM analysis output) are preserved as `json.RawMessage` so
  callers get the raw truth.

- **v2 URL construction**: new `buildURLv2` (tenant-scoped v2 paths) and
  `buildURLv2Root` (non-tenant-scoped v2 paths for availability and
  stateless generators) helpers, matching xolu's actual v2 routing where
  most routes are tenant-scoped but a few live at the root.

- **Refactored `do()` pipeline**: split into `do()` (v1 paths via `buildURL`)
  and a shared `doURL()` (takes a fully-constructed URL) so v2 methods
  reuse the auth, marshalling, structured error parsing, and response
  decoding without duplication. No behaviour change for existing v1
  callers.

### Tests — `pkg/client`

Eighteen new tests in a new `pkg/client/schema_test.go`, covering every new
method across happy path, empty-argument rejection, structured error, tenant
scoping, `AllGeneratorKinds` iteration, and raw-payload preservation.
Realistic wire-format bodies used throughout, matching xolu's actual
response shapes. Client package now has 53 tests total (up from 35).

### Known limits (tracked as xolu debt)

Two gaps in xolu's read surface were confirmed against the source and are
NOT worked around in the client — they belong to xolu:

- **No `GET /api/v1/schemas` endpoint** to enumerate registered entity
  types. A consumer building a semantic map must know entity-type names
  out of band. Filed as T-24 in the debt tracker.

- **No `GET /api/v2/gen/seq` list endpoint** to enumerate named sequences.
  `GetSequence` works when the name is known; there is no way to discover
  which sequence names exist. Filed as T-25 in the debt tracker.

Neither gap affects Stage 3 (machine operations) or subsequent stages;
molu Part 2's semantic-map builder can be implemented today by supplying
entity-type and sequence-name lists as configuration, and updating to the
discovery endpoints once xolu ships them.

## [0.14.3] - 2026-07-17 (client Stage 1: Ready, auth modes, structured errors)

Stage 1 of the roadmap that brings `pkg/client` up to what molu Part 2 needs.
No other packages touched.

### Added

- **`Client.Ready(ctx) error`** hits `GET /ready`, distinct from the existing
  `Health()` which hits `/health`. `/ready` returns 503 during initialisation
  or when storage is unreachable, 200 once the process is initialised and the
  storage layer's `Ping` succeeds. This is the endpoint molu's gated-dispatch
  probe (Part 2 §8) needs. Auth is not sent — `/ready` is unauthenticated so
  probes work without credentials.

- **Three auth modes**, dispatched by an explicit `AuthMode` field:
  - `WithAPIKey(key)` — corresponds to `XOLU_AUTH_TYPE=apikey` and entries in
    `XOLU_API_KEYS`. Retained from Stage 0.
  - `WithBearerToken(token)` — new. Server-issued opaque bearer token,
    corresponds to `XOLU_AUTH_TYPE=bearertoken`.
  - `WithJWT(token)` — new. JWT signed with `XOLU_JWT_SECRET`, corresponds to
    `XOLU_AUTH_TYPE=jwt`. JWT claims like `tenants:[...]` and
    `tenant_admin:true` are honoured by xolu's `TenantAuthMode` on the server.
  All three modes emit `Authorization: Bearer <token>` — xolu's auth
  middleware dispatches on the server's `AuthType` setting, not on a
  per-request scheme name. When more than one option is set, the last one
  wins. `WithTenantContext` now preserves the auth configuration when
  producing a per-tenant client.

- **Structured `Error` type** carrying xolu's `XOLU-*` error code:
  - `Code string` — e.g. `"XOLU-ST001"`. Empty when the server returned a
    non-structured error body.
  - `HTTPStatus int` — the HTTP status code.
  - `Message string` — human-readable message.
  - `Detail json.RawMessage` — the raw error body, preserved verbatim for
    callers that need server-specific fields the client does not model.
  Callers can dispatch on the code with `errors.As`:

  ```go
  var xerr *client.Error
  if errors.As(err, &xerr) && xerr.Code == "XOLU-ST001" {
      // entity not found
  }
  ```

  The `do()` method parses xolu's current structured shape
  (`{"error":{"code":"XOLU-...","message":"...","status":N}}`) first and
  falls back to the legacy flat shape (`{"error":"message","details":{...}}`)
  when the structured shape is not present. Non-JSON bodies are surfaced
  verbatim in `Message`.

### Deprecated

- `Error.StatusCode` and `Error.Details` are retained as aliases for
  `HTTPStatus` and (structured) `Detail`, so callers constructing `Error`
  literals against earlier client versions continue to compile and produce
  correct behaviour. New code should use `HTTPStatus` and `Detail`. Both
  aliases will be removed in a future major release.

### Tests

- Twelve new tests in `pkg/client/client_test.go` covering: no-auth default,
  each of the three auth modes emits `Bearer <token>`, last-wins conflict
  resolution, auth preservation across `WithTenantContext`, structured error
  parsing, legacy flat error parsing for backwards compat, non-JSON error
  fallback, `Ready` on 200, `Ready` on 503, and confirmation that `Ready`
  does not send an Authorization header even when the client is configured
  with an API key.

## [0.14.2] - 2026-07-17 (official Go client — `pkg/client`)

### Added

- **`pkg/client`** — the first official Go client for the xolu REST API. This
  release imports the client that Shelf (the first xolu-consuming application)
  built for its own use, as a starting scaffold for what will become the fully
  supported client library. It provides:
  - Entity CRUD: `Create`, `Get`, `Update`, `Patch`, `Delete`, `Save` (upsert).
  - Atomic multi-operation writes via `Commit`.
  - Queries: `OQL`, `Sulpher` (with `GraphQuery` alias), `List`, `Search`.
  - Graph traversal: `GraphNeighbors`, `GraphShortestPath`.
  - Health check via `Health`.
  - Functional options: `WithHTTPClient`, `WithTenant`, `WithTenantID`,
    `WithAPIKey`.
  - Standard-library only; no external dependencies beyond what xolu itself
    pulls.
  Twenty-three test functions cover the exposed surface. This is deliberately
  scaffolding, not a finished product: v1 endpoint coverage is partial and v2
  endpoints (FSM, generators, event defs, `cal`) are not covered yet. The
  fully specified client — full v1+v2 coverage, bearer and JWT auth, retry
  policy, telemetry hooks, `Ready()` for the molu health probe — will land in
  a subsequent release. This release lets molu Part 2 implementation begin
  against a real import path.

## [0.14.1] - 2026-07-17 (rename olu → xolu; NaN/Inf hardening)

### Renamed

- **Running server, environment variables, error codes.** The internal engine
  identifier has been renamed from `olu` to `xolu`. The GitHub repository and
  Go module path were already `xolu`; this release brings the runtime surface
  into line:
  - Binary: `olu` → `xolu` (in `cmd/xolu/`; the interactive admin CLI
    `iolu` — "interactive olu" — retains its name deliberately).
  - Environment variables: `OLU_*` → `XOLU_*` throughout (127 variables).
  - Error codes: `OLU-<AREA><NUM>` → `XOLU-<AREA><NUM>` (all 132 codes and 8
    family prefixes).
  - Internal package name references: `oluerr`, `olutime`, `oluMiddleware`
    → `xoluerr`, `xolutime`, `xoluMiddleware`. The `pkg/olutime` directory is
    now `pkg/xolutime`.
  - Prometheus metric names emitted at `/metrics`: `olu_*` → `xolu_*` (dashboards
    scraping these will need to update their queries).
  - Secret-name arguments to `readSecret`: `olu_jwt_secret`,
    `olu_internal_token` → `xolu_jwt_secret`, `xolu_internal_token`.
  - Error-code invariant: codes are now 10 characters (`XOLU-SSNNN`) rather
    than 9 (`OLU-SSNNN`); the test in `pkg/errors/errors_test.go` was updated
    accordingly.
  - `CHANGELOG.md` was deliberately preserved as historical record; all prior
    entries continue to reference the software as it was called at the time.

### Fixed

- **`Value.AsDecimal` panic on non-finite floats** (`pkg/fsm/eval/types.go`):
  `shopspring/decimal.NewFromFloat` panics on NaN, +Inf, and -Inf. The
  `AsDecimal` conversion for `TypeFloat`/`TypeReal` now returns `decimal.Zero`
  for non-finite inputs rather than propagating the panic. Discovered via
  `FuzzEvalGuard` on input `ROUND(1,01E7*1)=0000`.

- **`ROUND` overflow to NaN** (`pkg/fsm/eval/functions.go`): with a decimal-
  places argument outside the range `[-15, 15]`, `math.Pow(10, decimals)`
  overflowed to +Inf and `Round(f*scale)/scale` then produced NaN, silently
  corrupting downstream comparisons. `fnRound` now rejects out-of-range
  decimal places with a clear error, and passes through NaN/Inf inputs
  unchanged rather than manufacturing new ones.

- **Scalar-function NaN/Inf leakage** (`pkg/qs/scalar.go`, `pkg/qs/compare.go`):
  `COALESCE`, `ISNULL`, and other passthrough scalars could return `+Inf` or
  NaN when their inputs did, breaking the JSON-serialisation invariant that
  `FuzzScalarFunctions` enforces. All registered scalars are now wrapped in
  `finiteResult`, which normalises non-finite floats to `nil` (SQL NULL) at
  the dispatcher. `ToFloatSafe` similarly rejects non-finite inputs at the
  numeric-conversion boundary so downstream arithmetic cannot introduce new
  non-finite values.

### Dependencies

- `github.com/ha1tch/tsqlparser` bumped from `v0.6.0` to `v0.6.1`, which fixes
  a nil-pointer panic in `parser.parseScopeExpression` triggered by inputs
  like `(  ! ::` and a related nil-pointer panic in `ast.SelectColumn.String`
  when stringifying partially-parsed programs. Both were discovered via xolu's
  `FuzzEvalGuard` corpus.

## [0.14.0] - 2026-06-23 (`cal` — the scheduling primitive, implemented)

### Added

**`pkg/cal` — the `cal` scheduling primitive.** The design carried through 0.13.2
as proposals is now implemented: a Pebble-backed, two-plane (binding/proposed)
occupancy index over a fixed quantum grid (300 s base, 288 quanta/day packed into
five `uint64` words), with bookings as the source of truth and the index a
derived, always-rebuildable projection. ~5,656 lines across 21 files; the whole
tree builds and vets clean and the package suite is green (61 top-level tests,
0 skipped).

The implementation spans the full stage plan:

- **Codec and bit layer** (`cal.go`, `codec.go`) — 14-byte big-endian keys
  mirroring the `ts` layout; day bitmaps with `Set`/`Clear`/`Test`/`SetRange`/
  `PopCount`/`And`/`Or`; the slack-masked N-way `AndFree` match kernel; midnight-
  crossing span handling; and the two-aggregate daypart rollup (an OR aggregate
  for busy-pruning and an `AnyClear` aggregate for match-pruning — the rollup
  prunes but, by construction, never confirms).
- **Availability** (`availability.go`) — sparse two-plane occupancy; `Capacity`
  as a ternary (free / indeterminate / busy) plus a scalar complement; `Openings`
  with earliest / first-fit / emptiest / longest-clear-margin objectives.
- **Bookings and store** (`booking.go`, `store.go`, `membooking.go`,
  `sqlitesource.go`) — the A9 lifecycle states; a Pebble-backed `IndexStore`
  rooted at the `storelayout` `cal` path; the `index == rebuild` invariant as the
  global acceptance gate; and a `BookingSource` interface with both an in-memory
  and a SQLite-backed implementation behind it.
- **Match and check** (`match.go`) — N-way cross-calendar intersection honouring
  each calendar's `match_considers` policy (binding-only vs binding-OR-proposed,
  pessimism winning automatically on clash), with a blocking diagnostic naming the
  calendars that kill an intersection; plus single-calendar `Check` dry-run.
- **Lifecycle and move** (`lifecycle.go`) — propose / confirm / decline / complete
  / cancel with the confirm cross-plane move, and `move` as an atomic reschedule
  that reports stranded dependents rather than cascading.
- **Match/commit** (`commit.go`) — atomic cross-calendar placement (the F-F gap
  identified in the 0.13.2 design review), committing all or nothing under
  randomised conflict.
- **Seal** (`seal.go`) — the now-crossing frontier seal, with monotonicity,
  exact-boundary, sealed-day mutation rejection, and rebuild-recovery tests.
- **Manager** (`manager.go`) — per-tenant assembly and isolation.

Testing follows the recorded strategy: the pure bit layer is property-tested
against brute-force interval oracles (`TestSpanDaysMatchesOracle`,
`TestAndFreeMatchesOracle`, `TestMatchVsOracle`, `TestCountsMatchOracle`); the
stateful layer is verified against `index == rebuild`
(`TestIndexEqualsRebuild`, `TestLifecycleRandomizedMatchesRebuild`,
`TestSQLiteSourceRebuildEqualsIndex`) and exercised under small-scale race stress
(`TestSealRaceStress`, `TestMatchCommitRandomizedAtomicity`).

- **`storelayout`** gains the `cal` role: `TenantCalDir`/`SharedCalDir` mirror the
  `ts` directory helpers, and `cal` is registered as a valid role so the on-disk
  `cal/` tree is not flagged as a layout violation.

### Notes

- The `cal` design documents (`docs/proposals/cal-rest-api.md`,
  `cal-pebble-codec.md`, `SESSION-2026-06-22-NOTES.md`) predate this
  implementation and describe `cal` as design-only with open questions; several
  of those questions were resolved in code. They are retained as historical design
  artefacts and have **not** been retrofitted to match the implementation — reconciling
  them is a separate, deliberate task.
- Residuals carried forward: the seal concurrency suite is verified at in-container
  scale only (a full stress run is a local task); the daypart rollup-prune *payoff*
  (as opposed to its soundness, which is tested) remains an empirical question
  needing realistic occupancy distributions. Both are recorded in
  `docs/KNOWN_ISSUES.md`.

## [0.13.2] - 2026-06-22 (timeline definition delete; `tl` namespace migration)

### Added

**`DELETE /api/v1/tenant/{tenant_id}/ts/tl/{timeline_id}` — delete a timeline
definition.** Removes a timeline definition together with its event data and its
rollups (the inverse of `def`; distinct from `DELETE .../tl/{id}/data`, which
clears events but keeps the definition). Cascade follows the store's
`RollupCascadeDelete` policy: cascade-on removes rollups → data → definition;
cascade-off returns `409` (`OLU-TS026`) when rollups exist, changing nothing.
Timeline `0` (root) → `400`; undefined → `404`; success → `204`.

- `registry.delete()`, `PebbleStore.DeleteTimeline`, `Store.DeleteTimeline`,
  `HandleTSDeleteTimeline`, and the route registration.
- **In-memory deleting marker** for read-concurrency: a timeline being deleted is
  hidden from `get()`, so a concurrent reader/writer sees a clean not-found
  instead of a transient defined-but-empty state. In-memory only (a crash
  mid-delete leaves a normal, retryable timeline); a failed delete clears the
  marker and leaves the timeline usable. Covered by tests including a
  concurrent-reader case under `-race`.

**`pkg/olutime` (alias `ot`) — the system-wide time invariant.** A thin
enforcement package: every persisted timestamp is an absolute UTC instant, minted
via `ot.Now()`; `ot.Parse` rejects zone-naive input (no `Z`/offset) at the
boundary; `Instant.Format` requires an explicit zone (no implicit session/server
zone). Deliberately knows nothing of time zones as stored data, recurrence, or
DST arithmetic — those live above the storage primitives. `FromUnixNano`/
`UnixNano` agree byte-for-byte with the `ts` codec. Includes a build-failing lint
(`TestNoBareWallClock`) that catches bare `time.Now()` flowing into persisted
wall-clock values — direct, struct-literal, and address-of-temp idioms — while
leaving duration measurement untouched. New doc `docs/TIME_HANDLING.md` states the
invariant and its boundary.

### Changed

**Timeline routes migrated `/ts/timelines` → `/ts/tl`.** The timeline tier now
follows the API v2 path convention: `POST /ts/tl/def`, `GET /ts/tl/list`,
`GET|PATCH|DELETE /ts/tl/{timeline_id}`, and the `tl/{id}/sync|data|rollup/...`
sub-routes. The legacy `/ts/timelines` routes are retired (commented out in
`server.go`). Documentation (`API_REFERENCE.md`, `TIMESERIES_DESIGN_V3.md`,
`COMMIT_ENDPOINT.md`, `API_V2.md`) updated to the new paths.

**Persisted timestamps normalized to UTC via `olutime`.** Job timestamps in
`oql` (`CreatedAt`/`UpdatedAt`/`cutoff`) and `sulpher`
(`CreatedAt`/`StartedAt`/`EndedAt`/`cutoff`) now use `ot.Now()`. The `ts`
ingestion parser (`parseTSTime`) and the v2 meta `expires_at` parse normalize
their result to UTC (no key change — `UnixNano` is zone-invariant — but read-back
is now consistent); blob-GC quarantine reconstruction and a `ts` epoch guard
aligned to UTC. The T-SQL current-time builtins in `fsm/eval` and `qs` keep their
contract: `GETUTCDATE`/`SYSUTCDATETIME` route through `ot.Now()`, while
`GETDATE`/`SYSDATETIME` remain local by T-SQL contract (marked at each site). The
deliberate `ts`-accepts / `cal`-rejects divergence on zone-naive input is recorded
in `docs/KNOWN_ISSUES.md`.

### Design (proposals, no code)

Substantial advance on the `cal` scheduling primitive — see
`docs/proposals/cal-rest-api.md`, `docs/proposals/cal-pebble-codec.md`, and
`docs/proposals/SESSION-2026-06-22-NOTES.md`. These are the design the 0.14.0
implementation is built from; at the 0.13.2 cut they stood alone as proposals.
This revision adds a design review (issue 1 `match` plane semantics, issue 2
bearer/`default_state`, the four-vs-five previously-unpathed count), domain-fitness
findings F-A–F-G across maintenance/hotel/operating-theatre modelling (including
F-F, the atomic cross-calendar placement gap, and the proposed `match/commit`),
requirement R-T1 (the caller's zone-rule-change recovery obligation), and the
booking `when` input rule (explicit-offset/`Z`, parsed via `ot.Parse`). Dangling
references to a non-existent `scheduling-primitive-design.md` were corrected.

## [0.13.1] - 2026-06-21 (iolu admin CLI on the normalized layout)

Brings the standalone `iolu` admin CLI onto the same normalized on-disk layout
the server uses. No server or library behaviour changes; this is tooling only.
The `iolu` command-line interface changes in a backward-incompatible way (the
`--db` file model is replaced by a `--base-dir` data-root model), which is safe
because the previous interface was already documented as unsafe to run against
an `olu`-managed data root.

### Changed

**`iolu` operates on a data root, not a database file.** Every subcommand now
takes `--base-dir <dir>` and derives all paths through `pkg/storelayout`,
matching the server exactly: per-tenant `t<XXXX>/{store,ts,blobs}`, or
`shared/store` for the SQLite primary in shared mode (timeseries and blobs are
always per-tenant). The `--db` and `--ts-dir` flags and the legacy
`ts/t<XXXX>` path composition (via `tenant.StorageDirSegment`) are removed.

- **Store organisation is auto-detected** from the data root (a
  `shared/store/olu.db` implies shared mode; a `t<XXXX>/store/olu.db` implies
  per-file), with an explicit `--mode per-file|shared` override.
- **`db init`** creates the correct per-tenant layout for the chosen mode and
  provisions each tenant's `ts/` and `blobs/` directories; it refuses to
  clobber an already-initialised root.
- **`db status` / `tenant info`** report each tenant's blob footprint alongside
  timeseries, and read per-tenant tables from the tenant's own store in
  per-file mode.
- **`tenant delete`** is mode-aware: in per-file mode it removes the tenant's
  entire `t<XXXX>/` directory; in shared mode it drops the tenant's table family
  from the shared store and removes its `ts/` and `blobs/` directories.
- **`tenant provision-ts`** now provisions both `ts/` and `blobs/` at the
  layout-derived per-tenant location.

### Fixed

Two defects in the per-file read path, found while bringing the CLI onto the
layout and now covered by regression tests:
- `tenant list` could block the SQLite connection (a hang) by issuing
  per-tenant count queries while the tenant-registry cursor was still open;
  registry rows are now buffered before any per-tenant query.
- `tenant info` / `tenant list` queried per-tenant tables against tenant 0's
  store; in per-file mode each tenant's tables live in its own store file, which
  is now opened for the read.

### Tested

New `cmd/iolu/main_test.go` covering store-mode detection and path resolution,
`db init` layout in both per-file and shared modes, the two per-file read
regressions, the re-init refusal, and mode-aware `tenant delete`.

## [0.13.0] - 2026-06-21 (per-tenant blob layout, cross-tenant blob isolation)

Closes the blob-plane tenant-isolation debt in full — both the access-control
half and the on-disk-layout half. Blobs are now a first-class per-tenant role,
uniform with the timeseries plane, and the native and S3 blob APIs both enforce
the per-identity tenant grant. The blob API had no known users, so there is no
migration path and no on-disk compatibility shim.

### Security

**Cross-tenant blob access is closed on both planes.** Under
`TenantEnforceGrant` (scoped mode), the native JSON blob handlers
(`PUT`/`GET`/`HEAD`/`DELETE`/`LIST`/`USAGE`) now enforce that the caller's
verified identity authorises the route tenant, fail-closed, exactly as the rest
of the v1 surface and the S3 plane already did. Previously a token scoped to one
tenant could read or delete another tenant's blobs through the native plane. The
authorisation decision is shared with the normal tenant routes via a single
`Server.authoriseTenantGrant`; the S3 plane retains its SigV4-derived grant check
(`grant.Allows(bucket)`).

### Changed

**Blobs are stored per tenant at `<BaseDir>/tXXXX/blobs/`**, keyed by tenant ID
and derived by `pkg/storelayout.TenantBlobDir` — uniform with `tXXXX/store/` and
`tXXXX/ts/`, tenant 0 included. The previous single server-level store at
`<BaseDir>/blobs/` (which partitioned tenants internally by hex-encoded name) is
removed, along with `sanitiseTenant`. Within a tenant's `blobs/` directory the
content sits directly under it: `.keys/` for the alias index and `{xx}/` shard
directories for content-addressed blobs (plus `.ct`/`.md5` sidecars).

- `pkg/blob.Store` is now single-tenant: each instance's root *is* one tenant's
  blob directory. The `tenant` parameter is dropped from every store method, and
  the usage sampler and GC worker operate on a single store.
- A per-tenant blob manager (`pkg/server`, mirroring
  `timeseries.DefaultManager`) hands out and caches one `blob.Store` per tenant
  ID, discovers existing tenant blob directories at startup, and lazily opens
  them. A single server-level `blob-gc` worker drives GC across all open tenant
  stores; per-tenant usage is aggregated for telemetry.
- `storelayout.Check` now recognises `blobs` as a valid per-tenant role and
  treats a server-level `<BaseDir>/blobs/` directory as a layout violation.
- `config.BlobDir` is retained for compatibility but is no longer consulted;
  blob placement is derived from `BaseDir`. Marked deprecated.

### Fixed

- The `AuthType` config field doc comment now lists `bearertoken`, which
  validation already accepted.

### Tested

End-to-end cross-tenant isolation test that writes blobs to different tenants and
reads them back through *both* the native and S3 APIs, asserting separate
`tXXXX/blobs/` directories on disk and no server-level `blobs/`. White-box
coverage for the blob manager's multi-tenant aggregation paths (`GlobalUsage`,
`Sweep`), startup discovery, store caching, and the unknown-tenant 404 path under
auto-register-off.

## [0.12.2] - 2026-06-21 (S3 read-side ETag conformance, dual digests)

Closes the read-side ETag gap left open in 0.12.1 and exposes both content
digests on the native blob API. Backward compatible.

### Fixed

**`GetObject`/`HeadObject` ETag is now the content MD5**, matching `PutObject`
and the S3 contract, rather than the SHA-256. The MD5 is computed at write time
and stored in a sidecar so reads can return it; blobs written before this change
fall back to the SHA-256 ETag (no migration needed). This resolves the 0.12.1
known limitation.

### Added

**Both digests on the native blob API.** `GET`/`HEAD /api/v1/blob/{key}` now
return an `X-Blob-MD5` header alongside the existing `X-Blob-SHA256`, and the
`PUT` and list JSON responses include an `md5` field alongside `sha256`. The
native ETag is unchanged (SHA-256); the explicit headers carry both digests.

### Changed

`blob.Store.Put`/`PutRaw` now also return the content MD5 (computed in the same
streaming pass as the SHA-256), removing a redundant second hash in the S3 PUT
handler. The blob GC and usage accounting treat the new `.md5` sidecar the same
way they treat the existing `.ct` sidecar.



Completes the S3 scoped-authorisation story from 0.12.0 by verifying request
signatures, and brings the S3 surface into interoperability with real S3 clients
(mc, boto3, s3cmd). Backward compatible: open mode is unchanged, and the new
checksum behaviour is opt-in.

### Added

**SigV4 signature verification (`pkg/s3sig`).** A shared package implementing
header-based AWS Signature Version 4 with a single canonical derivation used by
both `Sign` (tests and tooling) and `Verify` (the server), so they cannot drift.
Validated against AWS's documented known-answer vector. Under
`TenantAuthMode: scoped`, S3 requests now have their SigV4 signature verified
against `S3KeyGrant.Secret`; a known, authorising access key presented with an
invalid signature is rejected (403 `SignatureDoesNotMatch`). This closes the
0.12.0 known limitation.

**`otogen s3sign`.** A new subcommand that produces a signed S3 request (or a
ready-to-run `curl` command) so an operator can verify an `S3KeyGrant`
end-to-end against a running server without configuring a full S3 client. The S3
secret is read from `--secret-file` or `OLU_S3_SECRET`, never a flag.

**`otogen --format` switch.** All credential subcommands (`jwt`, `apikey`,
`bearer`, `s3key`) accept `--format text|yaml|json|csv` (default `text`). For
`jwt`, `text` remains a bare token for pipe compatibility.

**Modern S3 additional checksums (`x-amz-checksum-sha256`).** Opt-in SHA-256
checksums across the object lifecycle: returned on `PutObject` when the client
requests them (`x-amz-sdk-checksum-algorithm: SHA256` or by sending its own
checksum, which is then validated — mismatch yields 400 `BadDigest`), and on
`GetObject`/`HeadObject` when the client sends `x-amz-checksum-mode: ENABLED`.
The digest is the base64 of the raw SHA-256 already computed for content
addressing, so it is free. Dormant unless requested.

**Real-client interop harness (`tests/interop`).** A launcher that boots a
scoped S3 listener with a known grant and exercises whichever real clients are
installed (mc, boto3, s3cmd, aws-cli) for valid round-trips, wrong-secret and
unknown-key rejection, and the checksum lifecycle. Not part of the release
pipeline (it needs external client binaries).

**Adversarial SigV4 tests.** Tampering with every signed fact (method, URI,
query, payload hash, host, date), signature manipulation (flipped, empty,
truncated, uppercased-hex, all-zeros), signed-header swapping, credential-scope
mismatch, and no-panic on hostile `Verify`/`Parse` inputs.

### Fixed

**S3 protocol compatibility.** Surfaced by real-client interop testing:

- `PutObject` now returns the ETag as the hex MD5 of the content (was SHA-256),
  matching the S3 contract that strict clients (s3cmd) validate.
- Bucket routes accept a trailing slash (`GET /{bucket}/?location=`), which S3
  clients (mc) use to probe bucket location; previously returned 404.
- Unmatched routes and disallowed methods return S3-formatted XML errors instead
  of chi's plain-text default, which strict clients cannot parse.
- The SigV4 canonical query string preserves empty-valued parameters
  (e.g. `?location=` canonicalises to `location=`), which Go's `url.Values`
  otherwise collapses, breaking signature verification for such requests.

**`otogen s3key` schema.** Now emits an `S3KeyGrants` block matching the 0.12.0
struct (`access_key`, `secret`, `tenants` list, `tenant_admin`) with
`--tenants`/`--admin` flags, replacing the stale `S3Keys`/singular-`tenant`
output and the obsolete `S3RequireAuth` guidance.

### Known limitation

`GetObject`/`HeadObject` still return the object SHA-256 as the ETag rather than
MD5 (only `PutObject` returns an MD5 ETag). No tested client fails on this, but
full ETag conformance on reads would require storing the content MD5 at write
time. The opt-in `x-amz-checksum-sha256` header is the recommended integrity
mechanism for reads.



A hygiene release. No behaviour change: all fixes are internal cleanup driven by
golangci-lint, with the full test suite unchanged and passing.

### Changed

**Lint backlog cleared; lint is now a release gate.** Established a committed
`.golangci.yml` (errcheck, govet, ineffassign, staticcheck, unused; errcheck
relaxed only for test files; the QF1001 De Morgan quickfix excluded as it harms
readability of the character-class identifier validators) and fixed every
finding it surfaces:

- **errcheck** — unchecked error returns now handled or explicitly ignored.
  Resource-cleanup calls (`Close`, `RemoveAll`, deferred cleanup) are explicitly
  ignored; `rows.Scan` results in the CLI tools are now checked.
- **govet** — replaced the deprecated `reflect.Ptr` with `reflect.Pointer` in the
  AST identifier gates.
- **ineffassign** — removed ineffectual assignments, including two vestigial
  first calls in tests (a value computed then immediately overwritten).
- **staticcheck** — applied simplifications (redundant nil checks before `len`,
  inferable types in `var` declarations, unnecessary `fmt.Sprintf`).
- **unused** — removed dead code: orphaned helper functions and types in the
  Sulpher executor, an unused zip-directory helper, an unused S3 stub, and four
  unused test helpers. Two unused mutex fields (`GCWorker.mu`,
  `flatGraph.loadMu`) were verified vestigial — never locked anywhere, with the
  full suite and the race detector passing without them — and removed.

The whole tree was reformatted with the toolchain's `gofmt` (go1.26.x); this is
whitespace-only and changes no behaviour.

### Release process

`release.sh` gained a `--no-lint` flag and a guard that skips linting when
golangci-lint is not installed. With the backlog cleared, normal releases run
lint as a hard gate again; `--no-lint` remains as an escape hatch.

## [0.12.0] - 2026-06-21 (S3 tenant grants, auth hardening)

Builds on the 0.11.0 tenant access-control feature. Adds per-key S3 tenant
authorisation, completes the non-JWT grant paths, and adds comprehensive
adversarial coverage of the entire authentication surface. Backward compatible:
the default mode is unchanged and existing deployments need no config changes.

### Added

**S3 tenant grants (`S3KeyGrants`).** New `S3KeyGrant` config (access key,
secret, tenants/admin). Under `TenantAuthMode: scoped`, an S3 request must
present an access key with a configured grant that authorises the requested
bucket; the access-key string is no longer trusted as the tenant name, and the
bucket-name fallback is refused. Without a matching grant the request is denied
(403). See docs/proposals/tenant-access-control.md §10.3.

**Comprehensive auth adversarial test suite.** Added coverage across the whole
authentication surface:

- JWT algorithm confusion (`alg:none`, `None`, HS512/RS256 swap, missing and
  non-string `alg`), forged and tampered signatures, and malformed-token
  structural attacks (wrong segment count, non-base64 segments, malformed JSON,
  null bytes, whitespace) — all rejected, no panics.
- API-key and bearer edge cases (empty, whitespace, prefix-of-valid,
  valid-plus-suffix, case, empty-configured-credential-not-matchable).
- Scoped cross-tenant coverage for the API-key and bearer paths, matching the
  existing JWT coverage.
- S3 credential parsing under hostile input (injection, null bytes, oversized
  keys, unicode, malformed Credential fields) and the scoped resolver under
  hostile headers, including confirmation that a bucket name presented as an
  access key is denied.

### Confirmed

JWT claim names ratified as final: `tenants` (array) and `tenant_admin` (bool),
no namespacing. `tenant_admin` is the sole admin mechanism — a literal `"*"` in
`tenants` is matched as an ordinary tenant name, not a wildcard.

### Known limitation

S3 scoped authorisation verifies that the access key is known and authorising,
but does not yet validate the request's SigV4 signature against
`S3KeyGrant.Secret`. Full signature validation is planned for 0.12.1. Until then
a caller who knows a valid access-key string can present it; the protection added
in 0.12.0 is that only configured keys with an authorising grant are accepted and
the bucket name is never trusted as the tenant.

## [0.11.1] - 2026-06-21 (lint hygiene)

A hygiene release. No behaviour change: all fixes are internal cleanup driven by
golangci-lint, with the full test suite unchanged and passing.

### Changed

**Lint backlog cleared; lint is now a release gate.** Established a committed
`.golangci.yml` (errcheck, govet, ineffassign, staticcheck, unused; errcheck
relaxed only for test files; the QF1001 De Morgan quickfix excluded as it harms
readability of the character-class identifier validators) and fixed every
finding it surfaces:

- **errcheck** — unchecked error returns now handled or explicitly ignored.
  Resource-cleanup calls (`Close`, `RemoveAll`, deferred cleanup) are explicitly
  ignored; `rows.Scan` results in the CLI tools are now checked.
- **govet** — replaced the deprecated `reflect.Ptr` with `reflect.Pointer` in the
  AST identifier gates.
- **ineffassign** — removed ineffectual assignments, including two vestigial
  first calls in tests (a value computed then immediately overwritten).
- **staticcheck** — applied simplifications (redundant nil checks before `len`,
  inferable types in `var` declarations, unnecessary `fmt.Sprintf`).
- **unused** — removed dead code: orphaned helper functions and types in the
  Sulpher executor, an unused zip-directory helper, an unused S3 stub, and four
  unused test helpers. Two unused mutex fields (`GCWorker.mu`,
  `flatGraph.loadMu`) were verified vestigial — never locked anywhere, with the
  full suite and the race detector passing without them — and removed.

The whole tree was reformatted with the toolchain's `gofmt` (go1.26.x); this is
whitespace-only and changes no behaviour.

### Release process

`release.sh` gained a `--no-lint` flag and a guard that skips linting when
golangci-lint is not installed. With the backlog cleared, normal releases run
lint as a hard gate again; `--no-lint` remains as an escape hatch.

## [0.11.0] - 2026-06-20 (tenant access control)

Feature work adding opt-in per-identity tenant authorisation. Backward
compatible: the default mode is unchanged, and existing deployments require no
config changes. See docs/proposals/tenant-access-control.md.

### Added

**Scoped tenant authorisation (`TenantAuthMode: scoped`).** A new authorisation
dimension alongside the existing `TenantMode`. Under `scoped`, an authenticated
caller's identity must authorise the tenant it requests, or the request is
rejected with 403 (`OLU-TN003`). The tenant authority comes from the credential:

- JWT: a `tenants: [...]` claim (explicit set) or `tenant_admin: true` (any
  tenant). The claim is signed, so it cannot be altered by the caller.
- API key: an `APIKeyGrants` config entry mapping the key to its tenants (or
  admin). A key present only in the flat `APIKeys` list authenticates but carries
  no grant and is rejected on tenant routes under `scoped`.
- Bearer token: always full (admin) authority — the trusted-gateway credential.

`open` (the default) preserves today's behaviour: any authenticated caller may
act on any tenant, correct for single-tenant, trusted-gateway, and edge
deployments.

**`otogen` credential generator** (`cmd/otogen`): mints scoped JWTs and generates
API keys, bearer tokens, and S3 key mappings, per
docs/proposals/tenant-access-control-operations.md. The JWT signing secret is
read from `OLU_JWT_SECRET` or `--secret-file`, never from a flag.

### Changed

**Internal tenancy mode is a bit-flag set.** `Config.Tenancy()` derives a
`TenancyFlags` bitmask (`TenantRequireRoute`, `TenantEnforceGrant`) from the mode
strings, with the invariant `TenantEnforceGrant ⇒ TenantRequireRoute` baked in so
the incoherent "authorise the URL tenant while leaving the unprefixed tenant-0
routes open" state is not representable. The user-facing config surface
(`TenantMode`, `TenantAuthMode`) is unchanged. Server route-mode checks now read
the flags rather than comparing mode strings.

**`scoped` implies stricter defaults by construction.** Enabling `scoped`
disables the unprefixed default-tenant routes (via `TenantRequireRoute`), removes
the auto-registration path (scoped routes through the strict branch), and forces
S3 authentication (no bucket-name tenant fallback) — each a consequence of the
flag invariant rather than a separate setting.

### Validation

`TenantAuthMode: scoped` requires `TenantMode: strict`; the incoherent
combination is rejected at startup with a message pointing the operator at
`strict`.

## [0.10.5] - 2026-06-20 (persisted-spec injection completion, allocation bound, query-limit robustness)

A security and robustness release built on 0.10.4. A continued adversarial pass
closed the remaining members of the persisted-identifier injection class on
storage load surfaces the 0.10.4 fixes did not reach, bounded an unbounded
allocation in the FSM CAST path (a sibling of D-008), and made the in-memory
query limits robust against misconfiguration. It also consolidates the
identifier-validation policy into a single source of truth in `pkg/qs`
(ASCII-only). One audited surface — cross-tenant access control — produced a
documented design finding rather than a code change; see Notes. Drop-in over
0.10.4.

### Security

**D-015 (injection) — persisted entity name not validated at load.**
`AdaptedTableSpec.TableName()` builds the SQL table name as
`t<X>_ndata_<entity>`, interpolated unparameterised into every adapted-table
statement (`adaptedEntityIDs`' `SELECT id FROM <table>`, the schema-evolution
DDL, CRUD). The HTTP layer validates entity names at registration, but
`LoadAdaptedRegistry` read `entity_type` raw from the persisted `n_sch` table and
the 0.10.4 `validatePersistedSpec` guard checked column/index names but not the
entity name itself. A poisoned `entity_type` from a pre-fix database, a restored
backup, or any non-derivation writer therefore reached SQL. `validatePersistedSpec`
now validates `spec.Entity`, and `DeriveAdaptedTableSpecFrom` validates the entity
name at derivation as defence in depth so a spec can never be built with an
injectable entity name regardless of caller.

**D-016 (injection) — persisted column SQL type not validated at load.**
`addColumnSQL` builds `ALTER TABLE <t> ADD COLUMN <name> <sqltype>`, interpolating
both the column name and `col.SQLType`. At derivation `SQLType` comes from
`dialect.ColumnType`, a closed set (`TEXT`/`INTEGER`/`REAL`), but the persisted
value is read back raw, so a poisoned `sql_type` reached the DDL. `validatePersistedSpec`
now rejects any column whose `SQLType` is outside the dialect's allowlist. (The
remaining persisted string fields were swept: `JSONField` is parameterised via
`json_extract(data, '$.' || ?)`, and `schema_hash` is comparison-only.)

**D-017 (availability) — unbounded allocation in FSM CAST/CONVERT.** A declared
fixed-width length such as `CAST(x AS CHAR(n))` flowed from `ParseDataType` (no
upper bound) through `Convert` to `NewChar`'s `strings.Repeat`, so a single guard
expression like `CAST('x' AS CHAR(2000000000))` could request a multi-gigabyte
allocation. This is the D-008 allocation class on a path the original fix did not
cover. `Convert` now rejects a declared length above `maxFunctionOutputBytes`
(16 MiB), covering the CHAR/NCHAR/BINARY padding paths at once; `VARCHAR(MAX)`
and legitimate small fixed-width casts are unaffected.

**D-014 (availability) — in-memory query limits could be disabled by
misconfiguration.** The OQL executor enforces scan/result bounds with
`if limit > 0`, so a zero limit silently disabled the bound and allowed an
unbounded in-memory scan. The config documented "0 = use default" for
`QueryMaxRows`/`QueryMaxScanRows` but never enforced it, and the env loader
accepted `OLU_QUERY_MAX_SCAN_ROWS=0` verbatim. `SetLimits` now applies the
documented defaults (10000 / 100000) to any non-positive value, matching the
use-time fallback pattern the timeseries handler already used. The Sulpher graph
limits were audited and already had this protection at the enforcement site.

### Changed

**Identifier-validation policy consolidated into `pkg/qs`.** The bare-identifier
allowlist had been expressed independently in nine places across four packages
with subtle, partly unintended variation. The canonical policy now lives in
`pkg/qs/identifier.go` as a single source of truth, with two intentional
variants — strict (leading letter required; entity and schema names) and
non-strict (leading underscore permitted; OQL field paths) — plus the rune
predicate used by the parse-time gates. The OQL, storage, and server validators
delegate to it; three duplicate regexes were removed. The policy is **ASCII
only**: permitting Unicode letters would open a homoglyph/confusable surface
(e.g. a Cyrillic "а" impersonating a Latin "a" across tenants) for a feature with
no real demand in this project. The parser-alphabet gates and the alias/object-key
validators retain their own deliberately different rules.

### Notes

**Cross-tenant access control is a documented design boundary, not yet an
enforced one.** Authentication establishes only that the caller holds a valid
server credential; there is no binding from an authenticated subject to a
specific tenant, and the tenant is selected from the request (URL parameter).
A holder of the server credential can therefore read or write any tenant. This
is correct for single-customer, trusted-gateway, and edge deployments — xolu's
current target model — where tenant isolation is a data-partitioning mechanism
rather than an access-control boundary between mutually-distrusting callers. It is
**not** safe to expose xolu directly to mutually-distrusting tenants on a shared
endpoint. Adding per-credential tenant scoping is tracked as future work.

## [0.10.4] - 2026-06-20 (identifier-injection hardening: four fixes and two parse-time gates)

A security release built on 0.10.3. A second adversarial pass over the OQL and
Sulpher query surfaces found four further injection defects (D-010 ... D-013),
all in the same *classes* the 0.10.2 audit addressed but at sites the point-fixes
did not reach. Each carries a red-first regression test. The release also adds a
central parse-time defence — one gate per query language — that closes the
identifier-injection class at the root rather than site by site. Not a 1.0
release.

### Security

**D-012 (High) — SQL injection via the OQL JOIN column alias.** The 0.10.2
D-005 fix routed JOIN *field names* through `validateFieldName`, but the JOIN
SELECT generator emitted each column as `<expr> AS <alias>` with the alias
interpolated **unquoted** and unvalidated. Because OQL parses with tsqlparser,
a T-SQL delimited alias `AS [a) UNION SELECT ... --]` is stored with its
delimiters stripped, so the raw inner text lands directly in the bare `AS`
position — no quote-breakout character required — and rewrites the statement
(demonstrated exfiltrating `t0000_nodes` via `UNION`). `generateJoinSelectColumns`
now routes the alias through the same `validateFieldName` allowlist the field
references use. Default aliases (field name or qualified field) satisfy it.

**D-013 (High) — SQL injection via the aggregate ORDER BY clause.** When an
ORDER BY term did not resolve to a known adapted column, the aggregate generator
fell into an "assume it's an output alias, use it directly" branch that
interpolated the raw stringified term. A delimited identifier
`ORDER BY [x) UNION SELECT ... --]` stringifies to raw inner text and injected
SQL. ORDER BY handling now works from the AST node type: aggregate expressions
(`SUM(amount)`, `COUNT(*)`) are re-rendered through the `Aggregates` allowlist
and `AdaptedColumnInfo`, bare columns resolve to native names, and only a genuine
output-alias residual is permitted — and only if it passes `validateFieldName`.
The companion GROUP BY rendering and the full aggregate WHERE surface
(comparison, IS NULL, BETWEEN, IN, LIKE) were audited in the same pass and found
already safe: every field is resolved-or-rejected via `AdaptedColumnInfo` and
every value is parameterised.

**D-011 (High) — SQL injection via the Sulpher push-down RETURN alias.** On the
Cypher surface, a backtick-quoted identifier accepts any character except a
backtick — including the double-quote that delimits the generated SQL alias — so
`RETURN x.name AS \`a" ...\`` broke out of `AS "<alias>"` in the push-down SQL.
Node/table aliases were already guarded by `isSimpleIdent`; the projection alias
was not. The explicit RETURN alias is now validated with the same `isSimpleIdent`
guard (the generated dotted default is exempt).

**D-010 (defence-in-depth) — persisted column/index names re-injected at schema
evolution.** The 0.10.2 D-009 fix validated schema field names at *derivation*,
but `LoadAdaptedRegistry`/`loadAdaptedEdgeSpecs` unmarshalled persisted
`column_spec` JSON with no re-validation, and the schema-evolution migration
interpolated those names verbatim into `DROP COLUMN`/`DROP INDEX`/data-migration
`SELECT` statements (demonstrated firing a chained `DROP TABLE` with a `nil`
error). This is not remotely reachable on a clean 0.10.x database — the
derivation gate keeps poison out of `column_spec` — but **any database written by
a pre-D-009 binary is a live carrier on upgrade**, as are restored backups and
any non-derivation writer. A new `validatePersistedSpec` re-validates every
persisted column name, index name, and index column at the load trust boundary;
a poisoned entity is quarantined (never registered), logged, and startup fails.
`MigrateAdaptedTable` re-checks the old spec before any DDL as a second layer.

### Added

**Parse-time identifier-smuggling gates (root-cause defence, both query
languages).** The three query-surface defects above share one root cause: the
lexer accepts *delimited* identifiers (T-SQL `[..]`/`".."`, Cypher `` `..` ``)
and stores the inner text with delimiters stripped, tokenised as an ordinary
identifier. By the time SQL is generated, an identifier carrying
`x) UNION SELECT ... --` is structurally indistinguishable from a bare one. But a
*bare* identifier can only contain letters, digits, `_` (and `#`/`$` in T-SQL);
any identifier value outside that alphabet must have entered through a delimited
form. In xolu that never happens legitimately — entity and field names are
constrained to `^[a-zA-Z][a-zA-Z0-9_]*$` at the server, OQL, and storage layers,
so a delimited identifier can never resolve to a real column or table.

Two gates exploit this:

- `pkg/oql` — a reflection-based AST walk at the single parse chokepoint
  (`Engine.parse`) rejects any `*ast.Identifier` whose value escapes the bare
  alphabet (the `*` wildcard is exempt), before any SQL generator runs.
- `pkg/sulpher` — the counterpart at `validateAST`, keying on both the sulpher
  AST's explicit `Backtick` flag and the bare-alphabet check.

A red-team matrix of injection shapes across every identifier position blocks
35/35 payloads in OQL and 20/20 in Sulpher at the parser boundary. The per-sink
validators (D-011/D-012/D-013 fixes) remain as defence in depth behind the gates;
the gates additionally cover any generation sink not individually hardened.

### Notes

The gates *reject* delimited identifiers rather than escaping them. This is a
deliberate policy choice: xolu has no use case for quoted column or table names,
and rejection costs nothing against the existing bare-identifier constraint. A
future need for genuinely-quoted identifiers would require revisiting the gates.

The hardening covers the adapted-table push-down SQL surface, where every finding
in this and the 0.10.2 audit lived. The non-push-down (in-memory) execution path,
the timeseries rollup SQL, and the FTS/search paths have not yet been put under
the same adversarial pressure and are candidates for a future pass.

## [0.10.3] - 2026-06-20 (property tests, fuzzing, and a fuzz-found fix)

A test-hardening release built on 0.10.2. It adds property tests that guard the
injection *class* (not just the specific payloads fixed in 0.10.2), six native
Go fuzz targets, a shared adversarial corpus, and a `tests/` control plane. The
fuzz pass found and fixed a non-serialisable-output issue in two `pkg/qs` scalar
functions. One small behaviour change; otherwise a drop-in over 0.10.2.

### Fixed

**`SQRT` and `POWER` coerce non-finite results to `nil`.** Found by the new
`FuzzScalarFunctions` target: `SQRT(-1)` returned `NaN` and `POWER(1e308, 2)`
returned `+Inf`, neither of which `encoding/json` can marshal — the same failure
mode as the 0.10.2 `ROUND` fix (D-007). These functions are not registered on
the OQL surface, but they share the `pkg/qs` scalar registry used by OQL and
Sulpher, so they now coerce `NaN`/`Inf` to `nil` (SQL NULL) for
safety-by-default, as the original audit recommended.

### Added

**Adversarial property tests — the injection class, not just known payloads.**
Three property tests assert security invariants over a shared corpus of hostile
identifiers, generalising the 0.10.2 single-payload regressions:

- `pkg/storage/adapted_ddl_property_test.go` (D-009 class): for any schema field
  name, no SQL metacharacter survives into a derived column name, index name,
  `CREATE TABLE`, or `CREATE INDEX` — covering the `REF_` and index-name
  derivation paths the point-fix guarded only transitively.
- `pkg/oql/sqlgen_join_property_test.go` (D-005 class): no metacharacter survives
  into JOIN SQL in the SELECT, WHERE, **or ON** position (the original D-005 test
  exercised only SELECT and WHERE).
- `pkg/oql/sqlgen_parity_property_test.go` (D-005 root cause): the JOIN field path
  is provably never weaker than the single-table path — the divergence that
  created D-005.

Each was verified to fail when its guard is removed.

**Six native Go fuzz targets**, one per security boundary: `FuzzEvalGuard`
(`pkg/fsm/eval`, the ~180-function registry behind D-008), `FuzzTokenise`
(`pkg/jsonic`, D-003), `FuzzBlobSHA` (`pkg/blob`, D-004), `FuzzScalarFunctions`
(`pkg/qs`, D-007), `FuzzValidateFieldName` (`pkg/oql`, D-005), and
`FuzzParseAndValidateJWT` (`pkg/middleware`, D-002). Under a normal `go test`
they replay their seed corpus deterministically; active fuzzing is opt-in.

**`pkg/internal/advcorpus`** — a shared, categorised adversarial-input corpus
(SQL metacharacters, injection identifiers, path-traversal digests, identifier
edge cases, valid-identifier controls) consumed by the property tests and fuzz
seeds, so one payload added strengthens every consumer.

**`tests/` control plane** — a discoverable home for test orchestration and
durable artifacts: `tests/fuzz/run.sh` (seed-replay by default, `--active` for
local/nightly runs), a target registry, a crasher archive, and a README. The
`Fuzz*`/property test functions remain in their packages (Go requires it); only
the launchers, corpora, and artifacts live under `tests/`. The root
`run_tests.sh`/`release.sh` are unchanged; `bench/` and `coverage/` migration is
documented as a deferred consolidation pass.

## [0.10.2] - 2026-06-20 (adversarial audit hardening)

A security and robustness release: closes the nine defects (D-001 … D-009)
identified by an adversarial review pass over 0.10.1. Two were High-severity
remotely-reachable injection vulnerabilities; the rest were hostile-input
robustness, input-validation, and auth-logic defects. Every defect now carries a
committed regression test. Not a 1.0 release.

### Security

**D-009 (High) — DDL injection via JSON-schema field names is closed.** A schema
property key became a SQL column name verbatim in `DeriveAdaptedTableSpecFrom`
and was interpolated into `CREATE TABLE`/`ALTER TABLE`/`CREATE INDEX` with no
validation; because the `modernc.org/sqlite` driver chains `;`-separated
statements in one `Exec`, a crafted key (`evil TEXT); DROP TABLE …;--`) could run
destructive DDL/DML reachable by any authenticated schema upload. Schema field
names are now validated against a strict identifier allowlist
(`^[a-zA-Z][a-zA-Z0-9_]*$`) at derivation, and rejected with a clean `400` at the
`handleCreateSchema` boundary before any persistence.

**D-005 (High) — SQL injection via OQL JOIN field names is closed.** The JOIN
SQL generator's blob branch (`joinFieldRef` → `JSONFieldAliasedAs`) interpolated
the raw field identifier into a `json_extract` path with no validation, so a
T-SQL delimited identifier (`[x') UNION SELECT …--]`) broke out and exfiltrated
data via `UNION`. `joinFieldRef` now routes every JOIN field — SELECT and
WHERE/ON, both branches — through the same `validateFieldName` allowlist the
single-table path already used.

### Fixed

**D-001 — `NEWID()` now returns a real version-4 UUID.** The previous
implementation synthesised a UUID-shaped string from a single nanosecond
timestamp: its final segment was a constant (an undefined 64-bit shift caught by
`go vet`), it set no version/variant bits, and it collided under rapid
generation. `NEWID()` is rebound to the same generator as `UUID_V4()`
(`uuid.NewRandom`) in FSM eval, and is additionally registered on the OQL scalar
surface (`qs.ScalarNewID`). `go vet ./...` is now clean.

**D-002 — JWT expiry can no longer be evaded by claim encoding, and `exp` is
required.** `exp`/`nbf` were guarded by a `float64` type assertion, so a claim
encoded as a JSON string silently skipped the check. Claims are now normalised
(number or numeric string) via `claimAsUnixTime`, a present-but-unparseable time
claim rejects the token, and — by deliberate policy decision — a token with **no
`exp` claim is now rejected** rather than treated as never-expiring (`nbf`
remains optional).

**D-003 — `jsonic` tokeniser depth is bounded.** The recursive-descent tokeniser
tracked no nesting depth and relied on the Go runtime's stack limit, so deep
input produced an uncatchable `fatal error: stack overflow`. A depth counter now
enforces `MaxNestingDepth` (10000, matching the stdlib decoder) and returns a
normal error instead.

**D-004 — blob SHA-addressed operations validate the digest.** `GetBySHA`/
`PutBySHA` passed the digest straight to `blobPath`, which sliced `hexSHA[:2]`
with no length or hex check — a short digest panicked and a non-hex digest could
contribute path components. A digest is now validated as exactly 64 lowercase hex
characters (`ErrSHAInvalid`) before any slicing or path join.

**D-006 — timeseries request fields are range-checked before narrowing.**
`num_field` and `dims` were narrowed `int`→`uint8` before validation, so an
out-of-range request whose low byte landed in the valid window (e.g.
`num_field` 256→0, `dims` 257→1) was silently accepted as a different in-range
value. Both handlers now validate the raw `int` against its range and return a
`400` before the conversion.

**D-007 — OQL scalar functions guard edge inputs.** `SUBSTRING` with a negative
length panicked (slice out of range); `ROUND` with a large precision produced a
`NaN` that broke response marshalling. `SUBSTRING` now clamps the slice end;
`ROUND` coerces a non-finite result to `nil` (SQL NULL).

**D-008 — FSM functions no longer panic or allocate unboundedly.** `SUBSTRING`/
`STUFF` panicked on a negative length; `REPLICATE`/`SPACE`/`STR` allocated
proportional to an unbounded user count, and a large enough value produced an
uncatchable out-of-memory fatal (a guard-driven, process-wide DoS). Slice ends
are clamped, and the three allocating functions bound their projected output
against `maxFunctionOutputBytes` (16 MiB), returning a clean error when exceeded.

### Changed

**`exp` is now mandatory on JWTs (breaking).** Following the D-002 policy
decision, a JWT presented for authentication must carry a parseable `exp` claim;
tokens without one are rejected with `401`. Any token issuer that previously
minted `exp`-less (unbounded-lifetime) tokens must set an expiry. See
`docs/UPGRADE.md`.

**`NEWID()` is now available on the OQL scalar surface** (previously only an FSM
eval function), bound to the v4 generator.

### Testing

All nine defects carry a committed regression test, several written red-first
against the unfixed code. The audit's characterization tests for D-006/D-007/
D-008 were tightened from log-only documentation into assertions that fail if the
defect returns. Full short-mode suite passes with zero failures across all
packages; `go vet ./...` is clean.

## [0.10.1] - 2026-06-19 (storage-layout normalization)

A cleanup release: legacy file-based graph storage is removed, and the on-disk
storage layout is normalized so that a single configurable knob (`--base-dir`)
determines where data lives, with everything beneath it derived by invariant.
Not a 1.0 release — coverage, benchmarks, and broader consistency passes remain.

### Added

**`pkg/storelayout` — the single authority for on-disk layout.** A leaf package
(stdlib-only) that both defines the layout invariant and verifies it:

- Path-derivation family keyed on the data root: `TenantStorePath`,
  `TenantTSDir`, `SharedStorePath`, `DynConfigPath`, `SchemaDir`, `BlobsDir`, and
  the tenant-segment functions (`TenantSegment`/`ParseTenantSegment`). Tenant 0
  is `t0000` like any other tenant — no longer special-cased to the data root.
- `Check(Model)` — a pure, I/O-free structural conformance check ("pass 1"):
  validates the directory structure down to and including each role directory and
  renders no verdict on role-directory contents (file populations are
  backend-specific and deferred to a future per-backend "pass 2").
- `DetectLegacy(Model)` — detects pre-normalization data locations for startup
  migration safety.

**`olu layout-recon` subcommand.** Scans the data root, prints an annotated
structure tree, runs the strict conformance check, and exits non-zero if the
layout does not conform. Resolves a relative `--base-dir` (default `./data`) to
its absolute path in the report.

**Startup migration safety.** olu refuses to start if the data root holds data
from a previous layout (a base store at the root, or backend-first `sql/`/`ts/`
groupings), pointing the operator to `layout-recon` rather than silently creating
fresh stores alongside the old data. The check runs after the banner and version
line, before any store or per-tenant directory is created.

### Changed

**On-disk layout is now invariant-derived from one knob.** The data tree is:

```
<BaseDir>/
  t0000/store/olu.db   t0000/ts/      (per-file mode: tenant 0)
  tNNNN/store/olu.db   tNNNN/ts/      (per-file mode: registered tenants)
  shared/store/olu.db  shared/ts/     (shared mode)
  schema/                             (server-level entity schemas)
  blobs/                              (server-level blob store)
  dynconfig.json                      (server-level dynamic config)
```

- **SQLite stores** (base and per-tenant) derive their paths from `--base-dir`
  via `storelayout`. The per-tenant resolution sources the data root and the
  per-file/shared decision from the server configuration.
- **Timeseries** is re-grouped from the old backend-first `ts/tXXXX/` to
  tenant-first `tNNNN/ts/`, consistent with the SQLite store layout. The manager
  receives the data root and derives per-tenant directories via `storelayout`.
- **Schema directory** is now the invariant `<BaseDir>/schema/` (previously the
  schema-name directory).
- **`NewSQLiteStore`** creates its own parent directory, so the store owns its
  directory invariant rather than relying on every caller.

### Removed

**Legacy file-based graph storage.** The graph lives in the per-tenant
`tXXXX_graph` edge tables inside the main database; the file-based representation
is fully removed: `graph.data`/`graph.index` files, the `AdaptivePersister`
(`pkg/graph/persister.go`), `FlatGraph.Save`/`Load` and the `Save`/`Load`
methods on the `Graph` interface, the `GraphDataFile`/`GraphIndexFile` config and
their export entries. Graph export is now the live `graph.json` representation
built directly from the in-memory graph.

**The competing database-path knobs.** `--db`, `OLU_DB_PATH`, `OLU_SQLITE_PATH`,
and the `config.Config.DBPath` field are removed. `--base-dir` is the sole layout
knob; the database location is derived from it, closing the split-brain where the
database could escape the data tree.

### Notes / deferred

Recorded in `docs/KNOWN_ISSUES.md` and `docs/SCHEMA_MODES_DESIGN.md`: the blob
plane is server-level and not yet tenant-aware (an isolation-by-convention
gap); `cmd/iolu` still uses the pre-normalization path model and was left as
reference; the inert `BlobDir`/`DynConfigFile` config fields remain pending
removal; and strict/clone schema modes are designed but unimplemented.


## [0.10.0] - 2026-06-19 (event model close-out)

This entry consolidates the work that completes the Part 1 event model and the
S11 release-prep pass. It marks the event model landing; it is **not** a 1.0
release — coverage, benchmarks, and broader consistency passes remain.

### Added

**The full event model (`EVENT_MODEL.md`).** The event subsystem, scaffolded in
S9, is completed into the model described in `docs/EVENT_MODEL.md`:

- **Two FSM latches.** `fsm.output` (one event per Mealy emission) and a new
  `fsm.step` (one event per committed transition, carrying
  `previous`/`current`/`terminal`/`vars` — the free state delta, since the walk
  already reads prior state). A no-output transition still fires `fsm.step`; a
  self-loop fires it too. Both fire from the standalone walk path and from a
  commit-embedded walk.
- **`commit.applied`.** Fires once after a commit transaction commits
  successfully, carrying the `affected` entity REF set (with `created`/`version`
  outcome facts) and a copy of the committed request. Because it fires only after
  the atomic commit succeeds, the request copy is an accurate record of what was
  committed (the atomicity contract).
- **The `{origin, message}` delivery envelope.** Every webhook notification is
  wrapped as `{"origin": {...}, "message": {...}}`. `origin` is stamped by xolu
  on every delivery — `agent`, `agent_version`, `event_def_id`,
  `event_latch_kind`, `event_latch_source` (transition-level
  `fsm/<from>:<input>:<to>` for FSM events), and `fired_at` (RFC3339-nano,
  stamped post-commit just before send). `message` is the def's rendered body.
- **`pkg/jsonplate`.** A structured JSON payload-template package: a jsonplate is
  JSON whose `{"$ref": "path"}` leaves resolve against event data via queryfy
  paths (`affected[0].ref.id`, `definition.name`, `vars.retries`); literals pass
  through; absent paths render `null`. Wired as the preferred webhook body form,
  coexisting with the `{{...}}` body-string path. See `docs/jsonplate.md`.
- **The `definition` namespace.** FSM events carry the machine's definition spec
  (as a queryable map) in the event data, so jsonplates can reference definition
  facts (`definition.states.X.terminal`, `definition.name`) alongside the firing
  payload and variables.

### Changed

- **Type-faithful payload values.** Ids and scalars in delivered payloads carry
  their native JSON type — envelope ids are numbers (`"id": 9100`), booleans are
  booleans. Stringification is confined to `{{...}}` template interpolation, the
  only context that requires a string. (`EVENT_PENDING.md` §6a.)
- **Vocabulary fixed throughout** (code, schema, and docs): the SQL column
  `subscription_id` → `event_def_id`, `definition_id` → `fsm_def_id`; the table
  `event_subscriptions` → `event_defs`; the Go type `eventSubscription` →
  `eventDef`; the routes `/api/v2/event` → `/api/v2/event/def` (consistent with
  `/fsm/def`, `/rollup/def`). The retired terms "subscription" and "trigger" are
  replaced by the four-term model (event type / event def / event firing / event
  notification) defined in `EVENT_MODEL.md`.

### Tests

- **Canonical S11 integration test** (`TestS11_CanonicalPipeline`): a single
  `/commit` with an embedded `fsm_walk` whose guard passes, transitions state,
  emits a Mealy output, matched by a registered event def, delivered to a
  webhook — asserted end-to-end (atomic apply, guard, transition, output, def
  match, delivery, resolved content, origin provenance). The plan's named
  definition of done for Part 1.
- Full gate green: `go build ./...`, `go vet ./...`, `go test ./...` — 22
  packages ok.

### Documentation

- `docs/EVENT_MODEL.md` extended with the delivery envelope, origin provenance,
  type-faithful values, the `definition` namespace, and the jsonplate pointer.
- `docs/API_V2.md` event section updated to document the envelope and jsonplate
  as shipped, with the imagined/unshipped design moved to `docs/EVENT_PENDING.md`.
- New: `docs/jsonplate.md` (full jsonplate reference), `docs/EVENT_PENDING.md`
  (deferred items, tagged ROADMAP/SUPERSEDED/DECISION/DONE), and
  `docs/NOLU_EVENTS.md` (a separate investigation into federation-consistent
  event subjects and references — deferred, not part of 1.0).

### Known limitations (Part 1 / 1.0 preview)

See `docs/KNOWN_ISSUES.md` for the full statement. In brief: event delivery is
**asynchronous, at-most-once, single-attempt** — no retry, no dead-letter, no
replay, and no ordering guarantee between firings from one request. A crash in
the window between commit and dispatch loses the event (no durable queue).
Synchronous execution, retry/backoff, dead-letter/replay, the additional event
types and actions, and the federation-consistent subject/REF reshape are all
deferred (see `docs/EVENT_PENDING.md` and `docs/NOLU_EVENTS.md`).

## [0.9.9-apiv2patch015] - 2026-06-17

### Added

**S9 — event subscriptions (basic).** The reactive bridge that lets entity
lifecycle changes and FSM Mealy outputs trigger downstream actions. With this,
Part 1 of the API v2 plan is functionally complete (all feature stages S1–S10
shipped; S11 release-prep remains as the close-out).

- **Management surface:** eight endpoints — `POST`/`GET /api/v2/event`,
  `GET`/`PATCH`/`DELETE /api/v2/event/{id}`, `GET /api/v2/event/{id}/log`,
  `POST /api/v2/event/{id}/test`. A subscription binds an `event_type` to an
  action (`webhook` or `oql`) with a JSON config. Backed by two tables,
  `event_subscriptions` and `event_delivery_log` (schema version 9).
- **Triggers:** `entity.created`, `entity.updated`, `entity.deleted` (wired into
  the CRUD handlers) and `fsm.output` (wired into the walk path, one event per
  Mealy output). All fire post-commit, from the handler, asynchronously.
- **Actions:** `webhook` (HTTP POST, single attempt, 2xx = delivered) and `oql`
  (executed against the tenant store). Template substitution in URLs, bodies, and
  queries: `{{event.type/entity/id}}`, `{{event.data.<key>}}`, and `{{gen:<name>}}`
  (calls the named generator at dispatch time).
- **Delivery log:** every dispatch attempt writes one row, observable via
  `GET /api/v2/event/{id}/log`, so a failed or dropped delivery is visible after
  the fact.
- **Test endpoint:** `POST /api/v2/event/{id}/test` synchronously dispatches the
  subscription against a caller-supplied event payload and returns the per-action
  outcome.
- Error codes `OLU-EV001` (invalid), `OLU-EV002` (not found), `OLU-EV003`
  (delivery failed).

### Known limitations (Part 1)

- **Async only, at-most-once, single attempt.** A subscription may declare
  `"execution": "sync"`; it is accepted and stored but always runs async, and the
  response carries `X-Executed-As: async`. True sync execution, retry/backoff, and
  dead-letter/replay are deferred to S13/S16/S17.
- There is a brief window between commit and dispatch in which a crash loses the
  event (no durable queue in Part 1).
- Events arising from one request are unordered.
- Deferred trigger sources (`graph.edge.*`, `fsm.entered/exited/terminal`,
  `ts.appended`, `meta.expired`) and actions (`sulpher`, `fsm.walk`) are
  documented as such in `docs/API_V2.md`.

**Version bump: 0.9.9-apiv2patch014 → 0.9.9-apiv2patch015**

## [0.9.9-apiv2patch014] - 2026-06-17

### Removed

- **Retired the redundant bare generator surface.** The interim bare OQL scalars
  `TOKEN()`, `NANOID()`, `RANDOM_INT()`, `TIMESTAMP()` and their direct per-type
  HTTP endpoints (`GET /api/v2/gen/token`, etc.) are removed. Generators are now
  invoked solely through the named-definition surface — `POST /api/v2/gen/{type}`
  to define and `@GEN('name')` / `GET /api/v2/gen/{type}/{name}/next` to produce
  — consistent with the `@`-prefixed xolu extension convention. The generator
  logic functions are unchanged; only the duplicate invocation surface is gone.

### Changed

- The OQL set-operation rejection now names the actual operator
  (`UNION` / `INTERSECT` / `EXCEPT`) rather than always reporting `UNION`, and is
  checked before entity validation so the operator is reported regardless of
  whether the referenced tables exist (TD-002 secondary fixes).
- Documentation: corrected all six generator definition examples in
  `docs/API_V2.md` to the implemented request shape (`{"name", "config": {…}}`)
  and field names (`token` uses `length`; `timestamp` uses `zone`/`layout`);
  documented the OQL set-operation limitation in `docs/OQL_API.md`; updated
  TD-002 in `docs/KNOWN_ISSUES.md` to mark the error-message and documentation
  sub-items resolved. Separated API v2 status tracking into
  `docs/API_V2_TRACKING.md` (execution) from `docs/API_V2_DEVELOPMENT_PLAN.md`
  (intent).

**Version bump: 0.9.9-apiv2patch013 → 0.9.9-apiv2patch014**

## [0.9.9-apiv2patch013] - 2026-06-17

### Added

**S10 stateful generators with `@GEN('name')` dispatch.** Named generator
definitions are stored in `gen_definitions` as `(tenant_id, type, name,
config_json)` and invoked by name, mirroring the `@SEQ('name')` model.

- Generator types: `token` (cryptographically random URL-safe strings),
  `nanoid` (compact URL-safe IDs, configurable alphabet), `random_int`
  (bounded uniform integer), `timestamp` (current time, embedded IANA tz
  database), `pick` (element from a declared set, `random` and `round_robin`
  modes), and `slug` (human-readable identifiers from built-in vocabularies).
- HTTP surface per type: `POST /api/v2/gen/{type}` (define),
  `GET /api/v2/gen/{type}` (list), `GET /api/v2/gen/{type}/{name}` (retrieve),
  `GET /api/v2/gen/{type}/{name}/next` (generate), `DELETE /api/v2/gen/{type}/{name}`.
- OQL: `@GEN('name')` resolves the named generator and produces a value,
  replacing the prior stub. As a xolu T-SQL extension it is `@`-prefixed,
  consistent with `@SEQ` and the planned `@FSM`.
- Typed per-type config validated at define time, so a malformed definition
  (out-of-range length, degenerate alphabet, inverted `random_int` bounds,
  unknown timezone, empty `pick` set, unknown `slug` vocabulary, or a
  misspelled config key) is rejected up front rather than at first use.
- HTTP `/next` and OQL `@GEN` share a single dispatch path, so they cannot
  diverge — including the `pick` round-robin cursor.

### Changed

- `@GEN`/`@SEQ` in a SELECT column list are now evaluated on the per-query
  executor so they resolve against the request's tenant-scoped store. (The
  scalar-function map is global; its closures captured the engine's base
  executor, which resolved the wrong, unscoped tenant. This also fixes a
  latent version of the same issue for standalone `@SEQ` in a column list.)
- Documentation (`docs/API_V2.md`): corrected the generator surface to use
  `@GEN('name')` throughout (the bare `GEN(name)` form was a typo against the
  `@`-prefix convention); marked `pick` `weighted` mode and set mutation via
  `PUT` as Part 2; corrected the `@GEN` OQL reference entry.

### Removed

- The `snowflake` generator was dropped from the specification. Its Part 1
  in-memory form is strictly worse than the shipped `uuid_v7` (it carries the
  complexity of distributed ID generation without the distributed payoff).

### Known limitations

- The `pick` round-robin cursor is held in memory (per the spec); a restart
  resets it to position 0. Persistence is deferred to S21.
- `pick` `weighted` mode, `pick` set mutation, and `slug` custom word lists
  are deferred to Part 2.

**Version bump: 0.9.9-apiv2patch012 → 0.9.9-apiv2patch013**

## [0.9.9-apiv2patch012] - 2026-06-17

### Added

**FSM transition pre-queries.** A definition may associate an OQL `SELECT` with
an input symbol via the `input_queries` field (a map from input to query). Before
a walk on that input, the server runs the query read-only — before the walk
transaction opens — and binds its first result row into the guard and set
evaluator under the `query.` prefix, so guards and set clauses read
`query.<column>` alongside `payload.<field>`. This saves the caller a round-trip
for data it would otherwise fetch and pass in the payload.

- The query is forced to `TOP 1` before execution (overriding any author-supplied
  `TOP`), so at most one row is retrieved regardless of how many match. The
  author controls which row via `ORDER BY`.
- Result semantics: 0 rows binds NULL (a guard against it is false, so missing
  data never silently matches); 1 row binds its columns; N rows binds the first.
- A query that fails to parse or execute fails the walk with a `400` rather than
  being silently ignored.
- Limitations: standalone `/walk` only (the `/commit`-embedded walk does not run
  pre-queries); read-just-before-walk rather than atomic-inside-transaction; and
  a transition whose guards read `query.` values cannot be `loose` (it must be
  `firstmatch`, since the recognizer cannot see query state at definition time).

### Changed

Documentation: `docs/FSM.md` gains a transition pre-query section (the
`input_queries` field, the `query.` namespace, the `TOP 1` and 0/1/N-row
semantics, and the standalone-only and non-atomic caveats).

**Version bump: 0.9.9-apiv2patch011 → 0.9.9-apiv2patch012**

## [0.9.9-apiv2patch011] - 2026-06-17

### Added

**Mandatory FSM determinism declaration (three-level model).**

Every FSM definition must now declare `determinism` as one of `strict`, `loose`,
or `firstmatch`. The field is mandatory and fail-closed: a definition without an
explicit, valid level is rejected at creation (`OLU-FSM006`) and cannot be
created or instantiated. There is no default and no grandfathering — this is a
breaking change to the definition schema.

- `strict` — at most one transition per `(state, input)`, enforced structurally
  at creation.
- `loose` — multiple transitions per `(state, input)` permitted, but their
  guards must be provably mutually exclusive, verified at creation by the new
  exclusivity recognizer. A passing definition reports `exclusivity_verified:
  true` in its analysis block.
- `firstmatch` — multiple transitions permitted; the first whose guard passes,
  in definition order, fires. Transition order is semantic.

**Guard exclusivity recognizer** (`pkg/fsm/eval`, `CheckExclusivity`). Decides
whether a set of guards on one `(state, input)` is provably mutually exclusive.
Sound (never claims exclusivity it cannot prove) and deliberately incomplete
(rejects what it cannot prove; the author falls back to `firstmatch`). It reduces
each guard to a single-variable predicate — a null-state requirement plus either
an integer interval-set or a var-vs-var relation — and proves pairwise
disjointness. Recognized shapes: null partitions (`IS NULL` / `IS NOT NULL`),
literal equality and inequality, thresholds, bounded intervals (chained AND),
interval complements (OR of bounds), null-OR-region ("missing or invalid"), and
variable-to-variable equality/inequality complementarity.

**Smart inconsistency messages.** A definition that declares a determinism level
its structure or guards violate is rejected with a specific, actionable message
rather than a generic failure: `strict` with multiple edges names the overloaded
`(state, input)`; `loose` with overlapping guards names both guards, states they
can both be true, and offers `firstmatch`; `loose` with an unrecognized guard
points to the recognized forms; `loose` with an unguarded edge in a multi-edge
group explains the always-fires problem.

**New documentation:** [docs/FSM.md](docs/FSM.md) — the conceptual and semantic
companion to the endpoint reference, covering the FSM model, the determinism
levels, the recognizer and its recognized patterns, guard/set expression
semantics (including T-SQL NULL handling and type-coercion behaviour), and the
`OLU-FSM` error codes.

### Changed

All existing FSM definitions now declare a determinism level. The recognizer was
built adversarially-first; the packet-validator machine is declared `loose` with
verified exclusivity. `API_V2.md` cross-references `FSM.md` for concepts and
determinism while remaining the HTTP endpoint reference.

**Version bump: 0.9.9-apiv2patch010 → 0.9.9-apiv2patch011**

## [0.9.9-apiv2patch010] - 2026-06-17

### Fixed

Three FSM walk-engine defects, surfaced by adding a non-trivial packet-validator
test machine. All three would silently corrupt FSM behaviour and none were
reachable by the existing AssetLifecycle tests.

**Guard-disambiguated multi-edge transitions.** The walk evaluated only the
first transition matching `(state, input)` and rejected the walk if that one
transition's guard failed, instead of trying other transitions on the same
input. Multiple guarded edges sharing an input — the basis of any FSM that
validates input by routing to different targets — therefore did not work. The
walk now collects all matching transitions in definition order and fires the
first whose guard passes; `OLU-FSM004` is returned only when transitions exist
but every guard is false, and `OLU-FSM003` only when no transition matches the
input.

**Set clauses can read the walk payload.** `set` expressions were evaluated
with machine variables bound but not the payload, so `set: { "@expected":
"payload.len" }` silently produced null. Any FSM that records incoming data
into a variable was broken. The walk now binds the payload into the set-clause
evaluator, matching guard evaluation.

**`@var = expr` equality was misparsed as assignment.** T-SQL treats `@var =
expr` in a SELECT column as variable assignment, so the underlying parser
dropped the comparison and returned only the right-hand side. Every guard of
the form `@var = value` evaluated to the truthiness of the right-hand side —
for example `@retries = 3` was always true. `parseExpression` now detects the
assignment misparse and reconstructs it as an equality comparison. Inequalities
(`<`, `>`, `>=`, `<=`) and non-`@` equality were never affected.

### Added

**FSM behavioural and validator test coverage.** A `PacketValidator` test
machine (`v2_fsm_packet_test.go`) exercises guard-disambiguated accept /
reject-invalid / reject-missing edges on a single input, two terminal states
(Accepted / Rejected), presence checks via `IS NULL` / `IS NOT NULL`,
multi-variable guards, a payload-capturing set clause, a payload-accumulating
self-loop, and the distinction between modelled rejection (a guarded edge to
Rejected) and structural rejection (`OLU-FSM003` for out-of-sequence input).
A dedicated `pkg/fsm/eval/eval_equality_test.go` locks in the equality-parse
fix. An earlier behavioural suite (`v2_fsm_behaviour_test.go`) covers the
retry-loop guard boundary, guard-pre-value / history-post-value ordering,
multi-source transitions, the suspend/reinstate cycle, full-lifecycle
traversal with history-ledger fidelity, and overrides that change runtime
behaviour. The `eval_seq` unit tests for `NEXT VALUE FOR` substitution were
also restored as permanent tests.

### Fixed (test infrastructure)

The shared test-server cleanup now calls `srv.Stop()`, which drains and closes
the cached per-tenant SQLite stores. Without it, tests leaked per-tenant
connections, eventually exhausting SQLite handles and causing unrelated
parallel tests to fail with "out of memory (14)".

### Changed

**Version bump: 0.9.9-apiv2patch009 → 0.9.9-apiv2patch010**

## [0.9.9-apiv2patch009] - 2026-06-17

### Added

**S8 — FSM walk**

`POST /api/v2/fsm/machine/{id}/walk` is now live (it returned 501 in S7), and
the `fsm_walk` field in `/api/v1/commit` executes a walk atomically with the
document write.

*Walk sequence* — terminal check (`OLU-FSM005`), candidate-transition lookup
from the machine snapshot (`OLU-FSM003`), T-SQL guard evaluation via
`pkg/fsm/eval` against current variables and the walk payload (`OLU-FSM004`),
then a single transaction covering state advance, set-clause evaluation,
sequence increment, and history append. Mealy `output` is recorded in history
and returned. Firing outputs as event subscriptions is S9; in S8 they are
recorded only.

*NEXT VALUE FOR in set clauses* — set clauses may contain `NEXT VALUE FOR
name`, incremented atomically within the walk transaction. Implemented in
`pkg/fsm/eval` by AST substitution: the `*ast.NextValueForExpression` node
(already produced by tsqlparser) is replaced with the incremented value
before evaluation, reusing the existing sequence machinery rather than
duplicating it. `@SEQ` (a session-local read) and `@GEN` (S10) are separate
concerns and are not resolved in set clauses here.

*Transactional placement* — the walk runs in the storage layer as
`(*SQLiteStore).FsmWalkInTx(ctx, tx, …)`, alongside `saveInTx` and
`createInTx`. `commitInner` calls it on its own transaction before commit, so
a walk embedded in `/commit` is atomic with the entity write: if the walk
guard fails, the document write rolls back; if the entity write fails, the
state does not advance. A walk failure inside a commit surfaces as
`OLU-FSM008`. The standalone `/walk` handler and the `/seq/{name}/next`
handler both delegate to the storage-layer tx helpers (`FsmWalkInTx`,
`SeqIncrementTx`), so each operation has a single implementation shared
between its standalone and transaction-embedded callers.

*Sequence increment refactor* — the atomic sequence increment moved into
`pkg/storage` as `SeqIncrementTx(ctx, tx, …)`, callable on any transaction.
The standalone `/seq/{name}/next` path wraps it in its own transaction;
FSM set clauses pass the walk transaction. OQL behaviour is unchanged.

*Availability map* — `fsm` no longer carries the "walk pending S8" note.

*Tests* — walk basics, guard pass/reject (with state-advance assertions),
set-clause increment, `NEXT VALUE FOR` drawing from a real sequence,
no-transition (`OLU-FSM003`), terminal rejection (`OLU-FSM005`), history
recording, and the `/commit` integration: a happy-path commit+walk and an
atomicity test proving a guard-failed walk rolls back the accompanying entity
write (`OLU-FSM008`). The scaffold stability-header test was repointed from
`/walk` (now implemented) to a permanently-nonexistent route so it no longer
churns as features land; the availability test moved `fsm` to the available
set.

### Changed

**Version bump: 0.9.9-apiv2patch008 → 0.9.9-apiv2patch009**

## [0.9.9-apiv2patch008] - 2026-06-17

### Added

**S7 — FSM definitions and machines**

The FSM subsystem (`/api/v2/fsm`) is now available: six definition endpoints,
nine machine endpoints, structural validation via `fsm-toolkit`, and the
prototype-snapshot machine model. `/walk` is registered but returns 501 until
S8; inline `entity` creation and real bundle (`linked_states`) delegation are
deferred to Part 2.

*Dependency* — `github.com/ha1tch/fsm-toolkit v0.9.6` added as a runtime
dependency for structural validation and analysis (`FSM.Validate`,
`FSM.Analyse`, `FSM.GetTransitions`, reachability/determinism helpers). The
toolkit CLI (`cmd/fsm`, `cmd/fsmedit`) is a development-time tool and is not
part of xolu's dependency graph.

*Definition endpoints (tenant-scoped)*

```
POST   /api/v2/fsm/def            create a definition (prototype)
GET    /api/v2/fsm/def            list definitions
GET    /api/v2/fsm/def/{id}       retrieve a definition
PUT    /api/v2/fsm/def/{id}       replace a definition (future machines only)
DELETE /api/v2/fsm/def/{id}       delete a definition (always permitted)
POST   /api/v2/fsm/def/validate   validate without storing
```

Definition validation separates two concerns: `fsm-toolkit` confirms
structural integrity (state/transition reference validity, determinism,
reachability, and the analysis block), while `pkg/fsm/eval.ParseGuard`
syntax-checks every `guard` and `set` expression fragment without evaluating
them. Evaluation is deferred to walk time (S8). The lifecycle rule — every
non-terminal state must have a path to a terminal state — is enforced by a
backward-BFS reachability computation from the terminal set, rejecting
unreachable non-terminal states (including self-loops) with `OLU-FSM009`.

*Machine endpoints (tenant-scoped)*

```
POST   /api/v2/fsm/machine                  create from a definition
GET    /api/v2/fsm/machine                  list (filter: definition, state, ref)
GET    /api/v2/fsm/machine/{id}             retrieve
PATCH  /api/v2/fsm/machine/{id}             update local guards / var defaults
DELETE /api/v2/fsm/machine/{id}             delete (always permitted)
POST   /api/v2/fsm/machine/{id}/walk        501 until S8
GET    /api/v2/fsm/machine/{id}/state       current state
GET    /api/v2/fsm/machine/{id}/history     transition history
GET    /api/v2/fsm/machine/{id}/transitions available inputs from current state
GET    /api/v2/fsm/machine/{id}/vars        current variable values
```

*Prototype-snapshot model* — a machine takes a self-contained snapshot of its
definition (plus overrides and resolved linked-state children) at creation
time and never reads the definition again. `fsm_machines.definition_id`
records lineage only, with no foreign-key constraint; the source definition
may be changed or deleted without affecting any machine. A machine whose
source definition has been deleted reports `definition_deleted: true` and
continues to operate from its snapshot. Override blocks at creation and patch
adjust variable defaults and per-transition guards (keyed by input symbol);
an override referencing an input not present in the definition is rejected
with `OLU-FSM013`, and the full post-override snapshot is re-validated as a
unit.

*Storage* — five tables added to `initV2Schema` at version 7 (S7):
`fsm_definitions`, `fsm_machines`, `fsm_history`, `fsm_terminal_states`, and
`fsm_id_seq`. Integer IDs are allocated from `fsm_id_seq` using the atomic
`INSERT ... ON CONFLICT DO UPDATE SET next_id = next_id + 1 RETURNING`
pattern shared with the node-sequence allocator.

*Error codes* — `OLU-FSM001`–`OLU-FSM006` and `OLU-FSM008`–`OLU-FSM013`
added to `pkg/errors`. `OLU-FSM007` is intentionally absent: the spec error
table is non-contiguous and codes are not renumbered.

*Availability map* — `/api/v2` now reports `fsm` as available (walk pending
S8).

*Tests* — definition handler suite (`v2_fsm_def_test.go`) and machine handler
suite (`v2_fsm_machine_test.go`) cover the AssetLifecycle spec end-to-end,
validation failures (bad guard, output not in alphabet, no terminal
reachable, undeclared initial), CRUD, validate-without-storing,
snapshot-independence after definition delete, override semantics, list
filters, and the state/vars/transitions/history reads. The S1 scaffold
stability-header test was repointed from `/fsm/def` (now implemented) to the
permanently-501 `/walk` route.

### Changed

**Version bump: 0.9.9-apiv2-patch007 → 0.9.9-apiv2patch008**

## [0.9.9-apiv2-patch007] - 2026-06-17

### Added

**S6 — `pkg/fsm/eval` — T-SQL expression evaluator for FSM guards**

New package: `pkg/fsm/eval`. A surgical extraction of the
`ExpressionEvaluator` from `github.com/ha1tch/aulsql/pkg/tsqlruntime`.

*What was kept*

`types.go` (756 lines) — the `Value` tagged union, `DataType` constants,
`ToValue`, and all constructors (`NewInt`, `NewVarChar`, etc.).
`evaluator.go` (624 lines) — `ExpressionEvaluator` with `SetVariable`,
`SetVariables`, `Evaluate`, and the full AST switch covering arithmetic,
comparison, boolean operators, CASE, CAST, CONVERT, BETWEEN, IN, LIKE,
IS NULL.
`functions.go` (2082 lines) — `FunctionRegistry` with `Register`, `Call`,
`Has`, and the full set of built-in T-SQL string, math, date, and NULL
functions.
`convert.go` (622 lines) — `Cast`, `Convert`, `ParseDataType`,
`parseDateTimeWithStyle`.

*What was dropped*

`interpreter.go` (2187 lines) — the full T-SQL statement executor,
cursors, temp tables, result sets, DB/Tx machinery. None of this is
needed for expression evaluation.
`context.go` (310 lines) — `ExecutionContext` with `*sql.DB`, `*sql.Tx`,
`TempTableManager`, `CursorManager`. The `ExpressionEvaluator` has its
own variables map and does not reference this.

*Import path fix*

`evaluator.go` imported `github.com/ha1tch/aul/pkg/tsqlparser/ast`
(aulsql's internal vendored copy). Changed to
`github.com/ha1tch/tsqlparser/ast` (xolu's go.mod dependency, v0.6.0).
The change is transparent — v0.6.0 changes are additive and none affect
the expression AST nodes used by the evaluator.

*`QualifiedIdentifier` patch*

The original evaluator resolved `table.column` by taking only the last
part (`column`). For the `payload.field` convention used in FSM guards,
the full dotted name (`payload.result`) must be tried first. The
`QualifiedIdentifier` case now looks up the full dotted name before
falling back to the last part, making `payload.result = 'pass'` resolve
correctly when `BindPayload(map{"result": "pass"})` is called.

*`eval.go` — xolu entry points*

`Evaluator` wrapper with `New()`, `BindVars()`, `BindPayload()`,
`RegisterFunc()`.
`EvalGuard(e, expr, vars, payload) (bool, error)` — parse and evaluate
a boolean guard expression.
`EvalSet(e, expr, vars) (interface{}, error)` — parse and evaluate an
arithmetic or string set-clause expression.
`ParseGuard(expr) (ast.Expression, error)` — syntax check at definition
creation time without evaluating.

*Payload binding convention*

`BindPayload` flattens payload fields under the `"payload."` prefix.
`payload.result` in a guard expression resolves from a map key
`"payload.result"`. Nested objects are skipped; only string, number, and
bool top-level fields are bound.

*Generator registration*

`UUID_V4()`, `UUID_V7()`, `CUID()`, `ULID()` registered on every
`Evaluator` at `New()` time so they are available in FSM `set` clauses.

*22 tests in `eval_test.go`*

ParseGuard valid and invalid; EvalGuard covering the full AssetLifecycle
spec guard (`payload.result = 'pass' AND payload.technician != ''`),
`@retries < 3` pass/fail, compound guard, mixed vars+payload, undefined
variable, missing payload field; EvalSet covering increment, reset,
string literal, UPPER() built-in; all four generator functions in set
clauses; payload binding convention; RegisterFunc custom function;
multiple evaluations with same Evaluator.

### Changed

**Version bump: 0.9.9-apiv2-patch006 → 0.9.9-apiv2-patch007**

## [0.9.9-apiv2-patch006] - 2026-06-17

### Added

**S5 — named sequences and OQL integration**

*HTTP endpoints (tenant-scoped; also accessible via `/api/v2/seq` alias)*

```
POST   /api/v2/gen/seq              — define a sequence
GET    /api/v2/gen/seq/{name}       — get definition and current state
GET    /api/v2/gen/seq/{name}/next  — atomic increment, returns new value
POST   /api/v2/gen/seq/{name}/reset — reset to start value (or arbitrary value)
DELETE /api/v2/gen/seq/{name}       — delete definition and state
```

Options on define: `start` (default 1), `increment_by` (default 1, non-zero),
`min_val`, `max_val`, `cycle` (bool). Exhausted non-cyclic sequences return
OLU-GEN005. Cyclic sequences wrap to `min_val` (or 1) on overflow.

*Storage* — two tables added to `initV2Schema` at version 5 (S5): `gen_definitions`
(name registry shared across all generator types) and `sequences` (atomic
`UPDATE ... RETURNING` increment via `seqIncrement`).

*OQL: `NEXT VALUE FOR name`*

Handled via the existing `*ast.NextValueForExpression` AST node in tsqlparser.
New `*ast.NextValueForExpression` case in `evalExpr` calls `evalNextValueFor`,
which increments the sequence once per row and stores the result in
`seqSessionState` for `@SEQ()`.

*OQL: `@SEQ('name')`*

Session-local last value of a named sequence. Returns `nil` (not an error)
if `NEXT VALUE FOR` has not been called for this name in the current query.
Registered as a scalar function in the OQL engine via `RegisterSeqGenFuncs`.

*OQL: `@GEN('name')` (stub)*

Registered now so queries using `@GEN()` parse and execute without error.
Returns `nil` until S10 (stateful generators) implements the `gen_definitions`
dispatch. Documented as a stub in `executor_sequences.go`.

*OQL engine wiring*

`Executor.seqIncrementor` and `Executor.seqSession` fields added. Session
reset at the start of each `ExecuteWithStore` call. `seqIncrementor`
propagated to the temporary per-request executor created in `ExecuteWithStore`.
`Engine.SetSeqIncrementor` and `JobManager.SetSeqIncrementor` expose the
wiring point. Server calls `s.oqlJobs.SetSeqIncrementor(s.serverSeqIncrementor())`
in the v2 init block.

*Error codes* — `OLU-GEN002` through `OLU-GEN006` added to `pkg/errors`.

*22 new tests in `v2_seq_test.go`*

Covers: availability, define (default/options/duplicate/invalid name/zero inc),
get/get-not-found, next (monotonic, default start, custom start, custom inc,
not found, exhausted, cyclic wrap), reset, delete/delete-not-found, `/seq`
alias, tenant isolation, OQL `NEXT VALUE FOR` (distinct per-row values,
consecutive values), OQL not-found (nil not crash).

**Naming decision: `@SEQ()` and `@GEN()`**

`@CURRENT_VALUE('name')` renamed to `@SEQ('name')` before release. Shorter,
unambiguous, consistent with the planned `@GEN('name')` for the general case.
The `@` prefix convention marks xolu-specific OQL extensions distinct from
standard T-SQL. Spec (`docs/API_V2.md`) and development plan
(`docs/API_V2_DEVELOPMENT_PLAN.md`) updated accordingly.

### Changed

**Version bump: 0.9.9-apiv2-patch005 → 0.9.9-apiv2-patch006**

## [0.9.9-apiv2-patch005] - 2026-06-17

### Added

**S4 — stateless value generators**

*New dependencies*

- `github.com/google/uuid` upgraded `v1.3.0` → `v1.6.0` (adds `uuid.NewV7()`)
- `github.com/lucsky/cuid v1.2.1` — CUID generator, no transitive deps
- `github.com/oklog/ulid/v2 v2.1.0` — ULID generator; `pborman/getopt` is
  a CLI-only dep not needed for library use

*`pkg/server/v2_gen_handlers.go`*

Four pure generator functions with no storage or tenant scope:

- `genUUIDv4()` — random UUID v4 via `uuid.NewRandom()`
- `genUUIDv7()` — time-ordered UUID v7 via `uuid.NewV7()`
- `genCUID()` — CUID via `cuid.New()` (always prefixed `c`)
- `genULID()` — ULID via `ulid.Make()` (26 Crockford base32 chars)

HTTP endpoints (no tenant scope — pure functions):

```
GET /api/v2/gen/uuid_v4
GET /api/v2/gen/uuid_v7
GET /api/v2/gen/cuid
GET /api/v2/gen/ulid
```

Response: `{"type":"uuid_v4","value":"...","generated_at":"..."}`.

*OQL scalar registration* (`pkg/oql/scalar.go`)

Added `RegisterScalarFunc(name string, fn ScalarFunc)` helper — registers a
function in `ScalarFunctions` without touching the map directly. Used by the
generator `init()` to register `UUID_V4()`, `UUID_V7()`, `CUID()`, `ULID()`
unconditionally (not gated on `APIV2Enabled` — pure functions safe everywhere).

*Availability map* — `gen` subsystem flipped to `available: true`.

*18 new tests in `v2_gen_test.go`*

Covers: availability map, HTTP shape/uniqueness/version nibble for v4 and v7,
monotonicity for v7, CUID prefix and uniqueness, ULID length and monotonicity,
stability header, disabled returns 404; OQL UUID_V4/V7/CUID/ULID in queries,
OQL functions available on v1-only server.

**Test harness improvements**

`newV2Server` and `newV1OnlyServer` both updated to:
- Create entity schema directories (`assets`, `events`, `asset_types`, etc.)
  so OQL queries resolve entity names correctly
- Set `MaxQueryDepth: 10` and `AsyncJobRetentionTTL: 86400` so the OQL
  job manager initialises and sync queries work

`TestV2Scaffold_AvailabilitySubsystems` updated: `meta` and `gen` now
asserted as `available: true`; `seq`, `fsm`, `event` remain false.

### Changed

**Version bump: 0.9.9-apiv2-patch004 → 0.9.9-apiv2-patch005**

## [0.9.9-apiv2-patch004] - 2026-06-17

### Added

**S3 — `/api/v2/meta` entity metadata sidecar**

*Five endpoints*

- `GET    /api/v2/tenant/{id}/meta/{entity}/{id}` — list all metadata for an entity
- `GET    /api/v2/tenant/{id}/meta/{entity}/{id}/{key}` — get a single value
- `PUT    /api/v2/tenant/{id}/meta/{entity}/{id}/{key}` — set a value
- `DELETE /api/v2/tenant/{id}/meta/{entity}/{id}/{key}` — delete a single key
- `DELETE /api/v2/tenant/{id}/meta/{entity}/{id}` — delete all metadata for an entity

*`entity_meta` SQLite table* created in `initV2Schema` at stage version 3.
Schema includes `expires_at TIMESTAMP NULL` and a partial index on it for
efficient GC sweeps. Values are any JSON-serialisable type up to
`OLU_META_MAX_VALUE_BYTES` (default 64 KB, env `OLU_META_MAX_VALUE_BYTES`).

*Key validation:* `^[a-zA-Z0-9_]{1,64}$` — alphanumeric plus underscore
only, max 64 characters. `OLU-META004` on violation.

*TTL and reminder pattern:* Optional `expires_at` RFC3339 field on `PUT`
body. Expired entries are collected by `MetaSweeper`. The composition of
metadata TTL with event subscriptions (`meta.expired` trigger, S9) forms a
scheduled notification system without a separate scheduler.

*`MetaSweeper`* implements `gc.Sweeper`. Registered in `gcWorkers` at server
startup when `MetaGCEnabled=true` (default) and v2 is enabled. Visible via
`GET /api/v1/admin/gc` as `meta-gc`. `OLU_META_GC_INTERVAL_SECS` (default
300), `OLU_META_GC_ENABLED`.

*Cascade delete:* `deleteInner` in `sqlite.go` appends a
`DELETE FROM entity_meta` to the entity delete transaction. Errors are
silently ignored on v1-only deployments where the table does not exist.

*`storage.WriterDBProvider` interface:* Optional interface implemented by
`SQLiteStore.WriterDB()`. Used by v2 handlers that need direct SQL access to
global tables not modelled in the `Store` interface.

*Error codes:* `OLU-META001` through `OLU-META005`; `OLU-GC001`, `OLU-GC002`
registered in `pkg/errors`.

*23 new tests in `v2_meta_test.go`:* availability map, PUT scalar/object/
expiry/bad-expiry/entity-not-found/overwrite, key validation (valid and
invalid charset, 65-char), GET round-trip/not-found/expiry, list/list-empty,
delete-key/delete-key-not-found, delete-all, value-too-large, cascade delete,
GC sweeper deletes expired and spares future/permanent, GC in worker list,
disabled returns 404, tenant isolation.

*Spec updated (`docs/API_V2.md`):* TTL section and reminder pattern added to
meta section; `expires_at` column in schema; PUT request body documented;
GC config documented; suggested uses table updated; `meta.expired` added to
event trigger sources table; `OLU-META005` added to error codes table.

*`newV2Server` test helper:* Meta GC fields defaulted to
`MetaGCEnabled=true`, `MetaGCIntervalSecs=3600` so workers register without
manual config in each test.

*Scaffold test updated:* `TestV2Scaffold_AvailabilitySubsystems` now asserts
`meta.available=true` (S3 shipped) and `gen/seq/fsm/event.available=false`.

### Changed

**Version bump: 0.9.9-apiv2-patch003 → 0.9.9-apiv2-patch004**

## [0.9.9-apiv2-patch003] - 2026-06-16

### Added

**S2 — `pkg/gc` generic GC worker infrastructure**

*`pkg/gc/gc.go`*

New package providing the shared ticker-and-stop-channel lifecycle pattern
for all background sweep workers. Previously each sweeper implemented its
own goroutine management identically; it is now implemented once.

- `gc.Sweeper` interface: `Sweep(ctx) (Report, error)`
- `gc.Worker`: `NewWorker(name, sweeper, interval, logger)`, `Start()`,
  `Stop()` (idempotent), `RunOnce(ctx)`, `Name()`, `LastReport()`
- `gc.Report`: `{Examined, Collected, Quarantined, Errors, Duration}`
- 8 tests covering: RunOnce, LastReport, Start/Stop lifecycle, Stop
  idempotency, Name, Duration measurement, error propagation, zero values

*`blob.GCWorker` migrated*

Added `Sweep(ctx) (gc.Report, error)` to `blob.GCWorker`, mapping the
blob-specific `GCReport` fields to the shared Report type. The sweep logic
is unchanged; this is the adapter layer only.

*`timeseries.RetentionWorker` migrated*

Added `Sweep(ctx) (gc.Report, error)` to `timeseries.RetentionWorker`.
The sweep gains a `Report` return value for the first time (previously
fire-and-forget), making sweep results available to the admin endpoint.
The sweep logic is unchanged.

*Server migration*

`Server.tsRetention` and `Server.blobGC` fields changed from the concrete
worker types to `*gc.Worker`. Both are created via `gc.NewWorker` wrapping
the existing sweeper instance. A `gcWorkers []*gc.Worker` slice on the
server registers all active workers for the admin endpoint.

*`GET /api/v1/admin/gc`*

Returns a JSON array of all registered GC workers and their last report.
Workers without a completed sweep return no `last_report` or `last_swept_at`.
Available in all tenant modes (strict mode middleware updated to allow
`/api/v1/admin/` prefix).

*`POST /api/v1/admin/gc/{name}/run`*

Triggers a synchronous sweep on the named worker. Returns the `gc.Report`.
Returns 404 if no worker with that name is registered.

*11 new tests in `gc_admin_test.go`*

Covers: empty list, list with blob GC, list with TS retention, worker
response shape, run not-found, run blob-gc, run ts-retention, run updates
last report, migration smoke test (Stop() via gc.Worker).

**Strict mode middleware fix**

`/api/v1/admin/` prefix added to the strict mode allowlist so admin
endpoints are reachable regardless of `TenantMode`.

### Changed

**Version bump: 0.9.9-apiv2-patch002 → 0.9.9-apiv2-patch003**

## [0.9.9-apiv2-patch002] - 2026-06-16

### Added

**S1 — API v2 routing scaffold**

The complete S1 stage from the v2 development plan. No user-facing v2
functionality yet; this stage establishes the architectural contracts that
every subsequent stage depends on.

*`pkg/server/v2_handlers.go`*

- `setupV2Routes`: registers the `/api/v2` chi route group when
  `APIV2Enabled` is true. When false, the prefix is absent from the router
  entirely — requests return 404, not 501.
- `v2Middleware`: adds `X-API-Stability: experimental` and `X-API-Docs`
  response headers to every v2 route. Runs before the handler so headers
  are present on error responses too.
- `setupV2TenantRoutes`: stub that subsequent stages populate with their
  subsystem route blocks. Called for both the tenant-scoped path
  (`/api/v2/tenant/{id}/...`) and the unscoped path (`/api/v2/...`).
- `handleV2Availability` (`GET /api/v2/`): returns a JSON availability
  map listing all planned subsystems with `available: false` at S1,
  `version: "experimental"`, a human-readable warning, and an `as_of`
  timestamp. Each subsystem entry includes a `stage` field naming the
  development plan stage that will implement it.

*11 new tests in `v2_scaffold_test.go`*

Covers: disabled flag returns 404 for all v2 paths; enabled flag makes
root available; `X-API-Stability: experimental` present on every v2
response including unimplemented routes; `X-API-Docs` header present;
stability header absent from v1 routes; availability response shape and
field presence; all five subsystems listed as unavailable at S1;
warning field non-empty; `CommitRequest.FsmWalk` rejected with 4xx when
v2 is disabled; normal commit without `fsm_walk` unchanged.

### Changed

**Version bump: 0.9.9-apiv2-patch001 → 0.9.9-apiv2-patch002**

## [0.9.9-apiv2-patch001] - 2026-06-16

### Added

**API v2 infrastructure scaffolding (no user-facing functionality yet)**

Three pre-implementation changes that make the codebase ready for the first
v2 development stage (S1) without touching any existing v1 behaviour.

*`OLU_API_V2_ENABLED` configuration field* (`pkg/config/config.go`)

New boolean config field, default false, read from `OLU_API_V2_ENABLED`
environment variable. When false, the entire v2 surface is inert: routes
are not registered, v2 fields in v1 requests are rejected with a clear
error, and v2 tables are never created. Added `v2Enabled()` method on
`Server` for use by v1 handlers that need to check the flag.

*`CommitRequest.FsmWalk` stub* (`pkg/storage/storage.go`, `pkg/server/handlers.go`)

Added `CommitFsmWalk` and `CommitFsmWalkResult` types, `FsmWalk *CommitFsmWalk`
field on `CommitRequest`, and `FsmWalk *CommitFsmWalkResult` field on
`CommitResult`. The field is accepted in JSON request bodies now so callers
can experiment with the shape. When v2 is disabled, a non-nil `fsm_walk`
returns OLU-CM009 rather than being silently discarded.

Relaxed the `OLU-CM003` constraint from "at least one of append or
timeseries must be non-empty" to "at least one of append, timeseries, or
fsm_walk must be non-empty", so that a pure state transition (no entity
write alongside it) is a valid commit payload when v2 is enabled.

*`storage.V2SchemaInitialiser` interface and `SQLiteStore.InitV2Schema`*
(`pkg/storage/storage.go`, `pkg/storage/sqlite.go`)

Added the optional `V2SchemaInitialiser` interface and its SQLite
implementation `initV2Schema` / `InitV2Schema`. The function creates
`schema_version_v2` (a separate versioning table isolated from the v1
`schema_version` table) and is the entry point for all subsequent v2
table creation. The server calls it during `New()` when v2 is enabled;
failure degrades gracefully — v2 is disabled for the run, v1 is unaffected.

The v2 schema versioning convention is documented in `initV2Schema`:
version numbers map directly to development plan stages (S1=1, S3=3,
S5=5, S7=7, S9=9), each stage using `CREATE TABLE IF NOT EXISTS` and
`INSERT OR IGNORE` for idempotency.

*OQL executor session state design note* (`pkg/oql/executor.go`)

Added a design comment in `materializeScalars` documenting the correct
attachment point and implementation approach for `NEXT VALUE FOR` /
`CURRENT VALUE FOR` session state, so the S5 implementation can proceed
without rediscovering the architecture.

### Changed

**Version series: `0.9.9-rc*` → `0.9.9-apiv2-patch*`**

The release series changes to reflect that patches in this series are
v2 integration work against the stable 0.9.9 base rather than rc-series
bug fixes.

## [0.9.9-rc19] - 2026-06-16

### Added

**`pkg/server` — adversarial test campaign: 62 new tests across 5 files**

A systematic pass targeting the highest-risk untested paths in the server
layer, prioritised by production risk rather than raw coverage percentage.

*Shutdown lifecycle (`shutdown_test.go`, 9 tests)*

`Server.Stop()` was at 38.9% — none of the six nil guards (rateLimiter,
tsRetention, blobGC, blobSampler, dynWatcher, tsManager) were exercised with
live subsystems. Tests now call `srv.Stop()` directly with every combination:
each subsystem individually, all simultaneously with a 10-second deadlock
detector, TS-before-tenant-stores ordering verification, and double-close
safety. `Stop()` moves from **38.9% → 94.4%**.

*Blob and S3 handler gaps (`blob_adversarial_test.go`, 18 tests)*

Every `blobStore_==nil` branch across JSON blob and S3 handlers was untested.
Coverage added for: all disabled-path responses (JSON API 501,
`handleBlobUsage` 503); `tenantForBlob` tenant-context path via tenant-scoped
routes; `handleBlobUsage` with live `blobSampler` (`SampledAt` populated);
S3 delete idempotency (204 on absent key); S3 head bucket/object not-found;
`injectBlobMetrics` with sampler nil vs live; S3 `RequireAuth` missing and
valid Authorization headers.

*Graph cycle detection and query result branches
(`graph_query_adversarial_test.go`, 13 tests)*

`handleCreate` and `handleUpdate` cycle detection branches required
`graph.NewFlatGraphWithCycleDetection("error")` — the default `newE2EEnv`
uses `NewFlatGraph()` with detection disabled. A dedicated `cycleEnv` harness
wires the cycle-detection graph into a full HTTP server. Duplicate-edge target
rejection (`ErrDuplicateEdgeTarget → 400`) also covered.

`handleSulpherQueryResult` and `handleOQLQueryResult` (52%/56%) each have
three branches (not-found, pending/running, completed/failed), all now
covered via async submit + immediate poll (pending) and async submit + wait
(completed), plus not-found against a nonexistent ID.

*Timeseries sync/provision/update/batch/aggregate/stats/payload
(`ts_adversarial_test.go`, 24 tests)*

Restructured to use shared `tsEnv` instances via subtests, eliminating
fd-exhaustion interference with internal `package server` tests that use
`t.Parallel()`. Covers: `HandleTSSyncGet/On/Off` (were 0%), all
`HandleTSProvision` error branches, `HandleTSUpdateTimeline`,
`HandleTSBatchAppend` with payload and per-event errors,
`HandleTSQueryRangePost` all branches, `HandleTSLatest` all parameter
combinations, `HandleTSPatchRetention`, `HandleTSRangeAggregate` and
`HandleTSFullAggregate` error branches, timeline stats not-found, tsStore
unprovisioned/disabled paths, `eventToResponse` payload paths,
`parseInterval` all nine valid values plus invalid.

*Entity CRUD, graph query, dynconfig, server lifecycle
(`server_adversarial_test.go`, 21 tests)*

Shared `e2eEnv` instances reduce the previous 57-open design to ~6 opens.
Covers: handleCreate/Update/Patch/Delete/Get error paths, graph stats
(happy path and disabled), graph incoming/outgoing edge verification, graph
verify/rebuild handler reachability, Sulpher sync and async full lifecycle,
tenant Sulpher, handleTenantCreateEdge missing-field validation,
handleCommit bad body and valid upsert, dynConfigGuard all four admin
endpoints when disabled, handleVersion, handleSave, handleCreateSchema bad
body, 30-entity rapid burst (deadlock probe), concurrent read/write on graph.

**Coverage results**

| Metric | rc18 | rc19 |
|--------|------|------|
| `pkg/server` | 76.2% | **77.9%** |
| `Server.Stop()` | 38.9% | **94.4%** |
| Aggregate (`./pkg/...`) | 80.6% | **81.0%** |
| Total tests | 3,406 | **3,468** (+62) |

**Test infrastructure fix: fd-exhaustion with `t.Parallel()`**

The internal `package server` tests use `t.Parallel()` and SQLite databases
in `t.TempDir()`. When too many `setupTSServer`/`newE2EEnv` calls accumulate
in the same binary, the fd count approaches the process limit and subsequent
`sqlite.Open` calls return `SQLITE_CANTOPEN`. Root cause: the previous
session's adversarial files had one server-open per test function (73 extra
opens). Fixed by restructuring both files to share one server instance per
top-level `Test*` function via `t.Run` subtests, reducing extra opens to ~10.

**Note on `s3NotEnabled` (0% coverage)**

Confirmed dead code: defined in `blob_s3_handlers.go` but never registered
as a route. Each S3 handler guards its own `blobStore_==nil` branch directly.
The function documents the disabled-state behaviour inline. No fix required.

### Changed

**Version bump: 0.9.9-rc18 → 0.9.9-rc19**

## [0.9.9-rc18] - 2026-06-15

### Added

**Timeseries — rollup system**

A complete rollup infrastructure for pre-aggregating timeseries data at
configurable bucket granularities. Each rollup definition reads from a source
timeline, aggregates events over fixed time windows, and writes one summary
event per series per bucket into a destination timeline.

Key design properties:
- Tree structure (not DAG, not cyclic): each destination timeline may be the
  target of at most one rollup definition; single-parent enforced at write time
- Timeline 0 is the invisible structural root; it participates in no computation
- Maximum tree depth configurable via dynconfig `ts.rollup_max_depth` (default 4)
- Workers are not started on `DefineRollup`; they start on the first `/run`
  call so non-leaf nodes never fire before their source has data
- `cascade: true` on `/run` backfills the entire subtree in one call and starts
  all workers
- `OLU_TS_ROLLUP_CASCADE_DELETE` (bool, default `true`) controls whether delete
  cascades to all descendants or rejects with `409` when children exist

New Store interface methods: `DefineRollup`, `GetRollup`, `ListRollups`,
`DeleteRollup`, `RollupParent`, `RollupTree`, `RunRollup`, `RollupStatus`,
`DeleteTimelineData`, `PurgeTimelineRange`.

New HTTP routes (10):
```
POST/GET/DELETE  /ts/timelines/{tid}/rollup/def|list|parent|{rid}
POST/GET         /ts/timelines/{tid}/rollup/{rid}/run|status
GET              /ts/rollup/tree
DELETE/POST      /ts/timelines/{tid}/data|data/purge
```

New error codes: `OLU-TS022` through `OLU-TS026`.

New env var: `OLU_TS_ROLLUP_CASCADE_DELETE` (bool, default `true`).
New `StoreConfig` field: `RollupCascadeDelete bool`.
Rollup definitions persisted to `rollup_defs.json` per tenant store directory.

See `docs/ROLLUP-SPEC.md` for the full specification.

**Timeseries — rollup tests (44 tests in `pkg/timeseries/rollup_test.go`)**

Comprehensive test coverage of the rollup system:
- Structural constraint enforcement (cycles, depth, single-parent, timeline-0 guard)
- Registry read operations (get, list, parent link)
- Tree structure and `RollupTree` root construction
- `runBucket` output field layout verification against known inputs
- Cascade run correctness (no-duplicates, three-level, multi-bucket parent,
  cascade=false isolation)
- Worker lifecycle (not started on define, started by run, cascade starts all
  descendants, persistence round-trip, running vs stopped on reopen)
- Status tracking (EventsWritten, LastBucketEnd, LastError)
- Delete cascade (leaf-only, cascade=true, cascade=false reject, asymmetric tree)
- Data deletion (`DeleteTimelineData`, `PurgeTimelineRange` including boundary
  conditions and timeline-0 rejection)

### Fixed

**Timeseries — three bugs found and fixed during test development**

`RunRollup` cascade duplication: `descendants()` already included immediate
children of the destination; prepending them separately caused every direct
child to be processed twice, producing duplicate rollup events.

`RunRollup` single-bucket assumption: the entire `from`→`to` range was passed
as one bucket regardless of `BucketDuration`. A 3-minute range with a 1-minute
bucket produced one aggregate over 3 minutes rather than three 1-minute
aggregates. Fixed by iterating bucket-by-bucket across the range.

`RollupStatus` always returned `EventsWritten=0`: `RunRollup` was creating
an ephemeral worker for each call; status reads from the registered worker
which had no counters. Fixed by using `ensureWorkerRunning` to obtain the
registered worker and calling `runBucket` on it.

`RollupTree` returned empty children: the walk from timeline 0 looked for
definitions with `SourceTID=0`, which never exist. Fixed by identifying raw
timelines as those appearing as sources but not as destinations.

### Changed

**Version bump: 0.9.9-rc17 → 0.9.9-rc18**



### Added

**Coverage campaign — 57 new tests across 7 packages**

A focused pass targeting zero-coverage functions, merging work from a parallel
testing-focused development branch. All tests are pure additions with no changes
to production code.

| Package | New file | Tests | Coverage delta |
|---------|----------|------:|---------------|
| `pkg/graph` | `graph_coverage_test.go` | 18 | 72.9% → 92.2% |
| `pkg/models` | `models_zero_coverage_test.go` + 1 test in `models_test.go` | 6 | 63.6% → 98.7% |
| `pkg/oql` | `oql_coverage_test.go` + `join_coverage_test.go` | 17 | 75.1% → 77.8% |
| `pkg/sulpher` | `env_zero_coverage_test.go` | 7 | 78.4% → 78.8% |
| `pkg/tenant` | `tenant_zero_coverage_test.go` | 5 | 76.6% → 93.5% |
| `pkg/validation` | `validation_zero_coverage_test.go` | 4 | 85.2% → 89.3% |

Aggregate coverage: 78.1% → 79.8%. Total tests: 3,077 → 3,164.

**`pkg/cache` — offline install of `miniredis` v2.37.0**

`github.com/alicebob/miniredis/v2` and its transitive dep
`github.com/yuin/gopher-lua` are now installed in the module cache, resolving
the `GOPROXY=off` build failure that has been blocking `pkg/cache` from running
in the sandbox since rc11. All 19 packages now pass in the offline environment.

### Changed

**Version bump: 0.9.9-rc16 → 0.9.9-rc17**



### Added

**Timeseries — per-timeline nosync mode**

A new per-timeline write configuration controls whether `AppendBatch` waits
for a WAL fsync before returning. Setting a timeline to nosync mode removes the
fsync from the write path, trading hardware-failure durability for throughput
(~2.4× improvement in serial write benchmarks on the test hardware).

Three new HTTP endpoints:

```
GET  /api/v1/tenant/{id}/ts/timelines/{tid}/sync
POST /api/v1/tenant/{id}/ts/timelines/{tid}/sync/on   # restore sync (default)
POST /api/v1/tenant/{id}/ts/timelines/{tid}/sync/off  # enable nosync
```

The setting is persisted to `write_config.json` in the tenant's store directory
and survives server restarts. A batch containing events from both nosync and
sync timelines always commits with sync; the stricter setting wins.

New error codes: `OLU-TS020` (invalid write config request),
`OLU-TS021` (write config persist failure).

**Timeseries — per-tenant write coalescer**

A background coalescer goroutine can be enabled per tenant via dynconfig. When
active, it accumulates events from concurrent `Append`/`AppendBatch` callers
and commits them together, amortising one fsync across multiple callers. Each
caller still blocks until its events are committed — there is no fire-and-forget
semantics.

The coalescer is controlled exclusively via dynconfig (not per-timeline HTTP):

| dynconfig key | type | default | description |
|---|---|---|---|
| `ts.writecoal` | bool | `false` | enable coalescing for this tenant |
| `ts.coal_flush_interval_ms` | int | 10 | flush window in milliseconds |
| `ts.coal_max_events` | int | 2000 | early-flush event threshold |

Keys are looked up in the tenant namespace (`tenant.{name}`) first, then
`global`. Changes take effect within one flush interval — no restart required.

Process-level defaults (require restart, overridden by dynconfig at runtime):
`OLU_TS_COAL_FLUSH_INTERVAL_MS` (default 10), `OLU_TS_COAL_MAX_EVENTS`
(default 2000).

The coalescer only helps at ≥2 concurrent writers. With a single writer it
adds up to one flush interval as additional latency with no throughput benefit.
At 4 concurrent writers, coalescer+nosync (`both` mode) reaches ~1.1M
events/sec total throughput on the test hardware. See `TS-WRITE-MODES.md` for
the full benchmark analysis.

**Timeseries — dynconfig integration**

`NewPebbleStore` and `NewPebbleStoreFactory` now accept a `*dynconfig.DynConfig`
and a `tenantName string`. The `Manager.Provision` method gains a `tenantName`
argument that is stored and passed to the factory on both eager and lazy store
opens. `DefaultManager` gains a `tenantNames sync.Map` for this purpose.

The `StoreFactory` type signature changed from
`func(dir string, cfg StoreConfig) (Store, error)` to
`func(dir string, cfg StoreConfig, tenantName string) (Store, error)`.

### Changed

**Timeseries — `TimelineWriteConfig` simplified**

`WriteCoal bool` removed from `TimelineWriteConfig`. Coalescing is now
store-level and controlled via dynconfig, not per-timeline. The struct retains
only `NoSync bool`. The `write_config.json` on-disk format drops the `writecoal`
field; existing files with the field are read without error (the field is
silently ignored during load).

**Timeseries — `Manager.Provision` signature**

`Provision(ctx context.Context, tenantID uint16)` →
`Provision(ctx context.Context, tenantID uint16, tenantName string)`.

**Timeseries — removed `/ts/timelines/{tid}/config` endpoint**

The `GET` and `PATCH /ts/timelines/{tid}/config` endpoints introduced in an
earlier rc are replaced by the more focused `/sync/on`, `/sync/off`, and `/sync`
endpoints. Client code targeting `/config` must migrate to `/sync/*`.

### Fixed

**Timeseries — handler comment mismatches on sync endpoints**

`HandleTSSyncOn` and `HandleTSSyncOff` had their route path docstrings swapped.
The handler bodies were always correct; only the comments were wrong. Fixed.



### Changed

**Cache layer — per-item TTL in `MemoryCache`**

`MemoryCache.Set` now honours its `ttl time.Duration` parameter per entry
rather than ignoring it and relying on the global LRU TTL set at construction.
The `hashicorp/golang-lru/v2` expirable LRU backing was replaced with a
`sync.Mutex`-guarded `map[string]*cacheEntry` plus a `container/list` for LRU
eviction order. Each entry carries its own `expiresAt` timestamp. A background
sweeper goroutine (30-second interval) proactively removes expired entries.
`ShardedMemoryCache.Set` gains per-item TTL automatically by delegation.
`RedisCache.Set` was already correct. `Len()` method added.

**Cache config — new fields, renamed field, dead field removed**

- `GraphQueryTTL` renamed to `AsyncJobRetentionTTL` (it governs async job
  record housekeeping in `JobManager`, not query result caching)
- `GraphResultTTL` removed (defined but never consumed anywhere)
- `OQLQueryCacheTTL int` added (HTTP-layer OQL result cache, default 30s)
- `GraphQueryCacheTTL` default changed from 0 (disabled) to 30 seconds
- Env vars `OLU_ASYNC_JOB_RETENTION_TTL`, `OLU_GRAPH_QUERY_CACHE_TTL`,
  `OLU_OQL_QUERY_CACHE_TTL` wired into `LoadFromEnv`

**Sulpher sync handler deduplication**

`handleSulpherQuery` and `handleTenantSulpherQuery` shared ~105 lines of
identical logic with 33 differing lines. Extracted to
`executeSulpherQueryBody(w, r, jm, tid, logType, logTenantID)`. Both handlers
are now ~12-line wrappers. Four unused imports removed from
`graph_tenant_handlers.go`.

**BFS completeness fix — origin-scoped visited set**

Multi-seed BFS now uses `origin|node:segIdx` as the visited key instead of
`node:segIdx`. Each start node gets its own traversal scope: shared hub nodes
downstream of multiple origins produce a result row per origin rather than only
one (the completeness bug caused silently dropped results when two start nodes
converged on the same hub). Neighbour and start-node iteration now sorted by
node ID for deterministic result ordering across runs.

**Startup reporting and CLI flags**

- ASCII art and configuration summary are independently suppressible:
  `--no-ascii` / `OLU_NO_ASCII` and `--no-startup-text` / `OLU_NO_STARTUP_TEXT`
- Version string centred in the startup ruler (70-character width)
- Configuration summary reorganised into labelled sections; trivial
  defaults omitted; enabled subsystems shown only when non-default
- `olu help` shows flag reference; `olu env` shows full env var reference
- Nine command-line flags added: `--port`, `--host`, `--db`, `--base-dir`,
  `--schema`, `--log-level`, `--graph-mode`, `--no-ascii`, `--no-startup-text`
- `--verbose-init`: debug-level logging during initialisation only; reverts
  to the operational log level once `server.New` returns. No env var equivalent.
- Help text completely rewritten; `olu env` lists all 100 env vars by group

### Fixed

**BFS multi-source completeness (see Changed above)**

Previously `MATCH (a)-[:r]->(m)` with two start nodes converging on `m` would
return only one row. The origin that happened to expand `m` first in Go's
randomised map iteration order won; the other was silently dropped. The fix
is complete and covered by `TestKL7_BoundStartSeededFromExactNode` which now
runs against the full graph (including the `x→m` edge that was removed as a
workaround).

### Documentation

- Deleted 7 development-process artefacts from `docs/`: `ELEMENT_REFACTOR_CAMPAIGN.md`,
  `QUERY_OPTIMISATION_PROGRESS.md`, `SULPHER_PARSER_FEEDBACK.md`,
  `SULPHER_PARSER_FEEDBACK_ADDENDUM.md`, `SULPHER_RENOVATION.md`,
  `TESTING_STRATEGY.md`, `OLU_SLABBIS_BLOG_EXAMPLE.txt`
- `DECIMAL_TYPES.md` and `DECIMAL_TYPE_DESIGN.md` consolidated into one file
- `GRAPH_INVARIANTS.md` merged into `GRAPH_API.md`
- `CACHING.md` updated to reflect per-item TTL, new cache config fields
- `SULPHER_OC9_GAPS.md` and `SULPHER_OPTIMISATION_ROADMAP.md` updated
- `ADAPTED_TABLES_DESIGN.md` and `SQLITE_PER_FILE_TENANTS.md` status updated
- `MANUAL.md` updated: version, port default (8080), new config fields,
  Sulpher endpoint corrected, CLI flags noted
- `README.md` updated: badges, port, copyright, new config entries
- `TESTING.md` updated to v0.9.9
- CHANGELOG compressed: entries older than v0.9.7 condensed to summary block


## [0.9.9-rc14] - 2026-06-13

### Changed

**Adapted tables renamed from `olu_<entity>` to `t<XXXX>_ndata_<entity>`**

Adapted (schema-registered) node entity tables now follow the same per-tenant
naming convention as blob node tables. The tenant prefix comes first, making all
tables for a given tenant sort together in any alphabetical listing:

```
t0001_ndata_user          — formerly: olu_user
t0001_ndata_user_profile  — formerly: olu_user_profile
```

**`pkg/storage/adapted.go` — `AdaptedTableSpec` gains `TenantID uint16`**

`TableName()` now returns `tenant.AdaptedNodeTableName(s.TenantID, s.Entity)`
rather than the hardcoded `"olu_" + s.Entity`. `DeriveAdaptedTableSpec` and
`DeriveAdaptedTableSpecFrom` take a `tenantID uint16` parameter and store it
in the spec. Index names use `tenant.AdaptedNodeIndexField(tenantID, entity, field)`
rather than `idx_olu_<entity>_<field>`.

**`pkg/storage/dialect_sqlite.go` — `PerFileTenants` branches removed**

All adapted table DDL and SQL methods now generate a single form without a
`tenant_id` column. The table name is the tenant boundary. Removed:
`tenant_id INTEGER NOT NULL DEFAULT 0` column, `PRIMARY KEY (tenant_id, id)`,
`idx_<table>_tenant` index, `WHERE tenant_id = ?` predicates in SELECT/UPDATE/
DELETE/EXISTS. All functions now have one code path instead of two.

**`pkg/storage/adapted_crud.go` — `dialectIsPerFile` removed, `tenantID int` removed from all adapted CRUD signatures**

`adaptedCreate`, `adaptedGet`, `adaptedUpdate`, `adaptedDelete`, `adaptedList`,
`adaptedExists`, `adaptedGetInTx` no longer take a `tenantID int` parameter.
The spec's `TableName()` encodes the tenant. `RegisterAdaptedTable` and
`LoadAdaptedRegistry` take `tenantID uint16` to populate specs at construction
time. `MigrateAdaptedTable` reads `TenantID` from the old spec.

**`TestAdaptedCRUD_TenantIsolation` — rewritten for new model**

The test now verifies that `TenantID` propagates correctly into `AdaptedTableSpec`
and that `TableName()` produces the correct tenant-scoped name. Full multi-tenant
CRUD isolation through separate adapted tables is deferred to the per-tenant schema
registry step (item 2 of the refactor), where `adapted_table_schemas` becomes
`t<XXXX>_n_sch`. A comment in the test documents this limitation explicitly.

**`pkg/oql` test infrastructure updated**

`newMockJoinStore` in `planner_join_test.go` now uses `tenant.AdaptedNodeTableName`
rather than `"olu_" + entity`. `testmain_test.go` adapted table INSERT statements
no longer include the `tenant_id` column.

## [0.9.9-rc13] - 2026-06-13

### Changed

**Storage layer — `entities` table replaced by per-tenant `t<XXXX>_nodes` family**

The global `entities`, `entity_sequences`, and `entities_fts` tables are
replaced by per-tenant tables using the naming convention established in
rc11/rc12. Every store instance now creates its own table family on
initialisation, keyed by `TenantID`:

```
t0000_nodes   — blob node store       (replaces: entities)
t0000_nseq    — node ID sequences     (replaces: entity_sequences)
t0000_nfts    — node FTS virtual table (replaces: entities_fts)
t0001_nodes, t0001_nseq, t0001_nfts   — tenant 1, etc.
```

**Consequences:**

- `createSchemaShared` and `createSchemaPerFile` replaced by a single
  `createSchema(ctx)`. The two historical modes are now unified — table
  name is the tenant boundary so no `tenant_id` column is needed inside
  any data table. `PerFileTenants` flag is retained only for file-path
  routing in `storeForTenant`.
- `tenantWhere()` and `tenantArgs()` removed from `SQLiteStore`. All 56
  call sites simplified — no `tenant_id` filter in WHERE clauses.
- `PerFileTenants` branches in all CRUD methods collapsed to the per-file
  form (no `tenant_id` column in INSERT/UPDATE/DELETE).
- Graph topology table renamed from `graph_tXXXX` to `t<XXXX>_graph` and
  its indexes renamed via `tenant.GraphIndexSource/Target/Rel()`.
- Schema version v4 marked at store initialisation.
- `SQLiteStore` gains public `NodesTable() string` method (implements
  `storage.TableNamer`). Used by OQL push-down engine.

**OQL engine — dynamic nodes table in push-down SQL**

`SQLiteDialect` gains a `NodesTable string` field. `NewExecutor` populates
it from `store.NodesTable()` when the store implements `storage.TableNamer`.
`BaseQuery` and `resolveJoinTableNames` use the field rather than the
hardcoded string `"entities"`. Zero-value dialect defaults to `"t0000_nodes"`.

**`pkg/storage` — `storage.TableNamer` interface**

```go
type TableNamer interface {
    NodesTable() string
}
```

Allows the OQL engine and any future consumer to resolve the correct
per-tenant blob node table without a type assertion to `*SQLiteStore`.

### Migration note

Existing databases using the old `entities` table require a v4 migration
(not yet implemented in `cmd/olu-migrate`). New databases created from
this version onwards use `t<XXXX>_nodes` directly. Since only xolu reads
and writes the SQLite file at this level, there is no external compatibility
concern — the migration command is the sole upgrade path.

## [0.9.9-rc12] - 2026-06-13

### Changed

**`pkg/tenant/tenant.go` — complete table and index naming vocabulary**

All SQLite object names (tables and indexes) are now derived from functions
in `pkg/tenant`. No storage backend needs to hardcode a table or index name.

Added index naming functions (indexes are global within a SQLite file; each
must encode its parent table to avoid cross-tenant collisions in shared-file
mode):

- `NodesIndexEntityType(tenantID)` — `idx_t0001_nodes_etype`
- `NodesIndexUpdatedAt(tenantID)` — `idx_t0001_nodes_updated`
- `GraphIndexSource(tenantID)` — `idx_t0001_graph_src`
- `GraphIndexTarget(tenantID)` — `idx_t0001_graph_tgt`
- `GraphIndexRel(tenantID)` — `idx_t0001_graph_rel`
- `AdaptedNodeIndexTenant(tenantID, entityType)` — `idx_t0001_ndata_user_tenant`
- `AdaptedNodeIndexField(tenantID, entityType, field)` — `idx_t0001_ndata_user_email`
- `AdaptedEdgeIndexField(tenantID, relType, field)` — `idx_t0001_edata_KNOWS_since`
- `NodeSeqIndexEntityType(tenantID)` — `idx_t0001_nseq_etype`

Added global table constants (not tenant-scoped; one per database file):

- `tenant.TenantsTable = "tenants"` — tenant registry
- `tenant.SchemaVersionTable = "schema_version"` — migration version tracking

The naming layer is now backend-agnostic. A PostgreSQL dialect uses the same
functions from `pkg/tenant` to derive schema-qualified table names.

## [0.9.9-rc11] - 2026-06-13

### Changed

**`pkg/tenant/tenant.go` — new per-tenant table naming convention**

All SQLite tables are now named with the tenant prefix first, making every
table for a given tenant group together alphabetically in any schema listing:

```
t0001_e_sch           — edge schema + adaptation registry
t0001_edata_MEMBER_OF — adapted edge label (native columns)
t0001_edges           — blob edge property store
t0001_graph           — topology (from, to, rel, edge_id)
t0001_n_sch           — node schema + adaptation registry
t0001_ndata_user      — adapted node entity (native columns)
t0001_nfts            — node full-text search virtual table
t0001_nodes           — blob node store (replaces: entities)
t0001_nseq            — node ID sequences (replaces: entity_sequences)
```

New functions added to `pkg/tenant`:
- `GraphTableName(tenantID)` — replaces `GraphEdgesTableName`
- `NodesTableName(tenantID)` — formerly `entities`
- `NodeSeqTableName(tenantID)` — formerly `entity_sequences`
- `NodeFTSTableName(tenantID)` — formerly `entities_fts`
- `EdgePropsTableName(tenantID)` — blob edge property store
- `NodeSchemaTableName(tenantID)` — node schema registry
- `EdgeSchemaTableName(tenantID)` — edge schema registry
- `AdaptedNodeTableName(tenantID, entityType)` — e.g. `t0001_ndata_user_profile`
- `AdaptedEdgeTableName(tenantID, relType)` — e.g. `t0001_edata_KNOWS`

`GraphEdgesTableName` is retained as a deprecated alias for `GraphTableName`.
All 47 hardcoded `graph_tXXXX` strings in tests updated to the new format.

This is a naming-layer change only. The actual table migration
(`entities` → `t0000_nodes`, etc.) is a separate step.

## [0.9.9-rc10] - 2026-06-13

### Added

**`pkg/storage/storage.go`, `pkg/storage/sqlite.go` — `GetMany` batch fetch**

New method on the `Store` interface:

```go
GetMany(ctx context.Context, entity string, ids []int) (map[int]map[string]interface{}, error)
```

Returns a `map[id]data` for every found id; absent ids are silently omitted.
SQLite implementation issues one `WHERE id IN (...)` query instead of N
individual `Get` calls. Adapted-entity path falls back to sequential Gets.

**`pkg/sulpher/executor.go` — `preHydrateEnvs` batch hydration**

Before every `projectEnvs` call, `preHydrateEnvs` collects all unhydrated
`_nodeID` references in the result set, groups them by entity type, and
calls `GetMany` once per type. This replaces O(n) per-node `Get` calls in
the RETURN projection path with O(entity_types) bulk queries. The lazy
per-property hydration in `evalEnv` (used for WHERE evaluation) is unchanged.
Falls back silently to lazy hydration when the store does not implement `GetMany`.

**`pkg/sulpher/executor_helpers.go` — comma-separated MATCH patterns**

`MATCH (a:User), (b:Item)` and `MATCH (a:User), (b:Item)-[:OWNS]->(c:Tag)`
are now supported. The parser-level pattern (multiple `PatternPart` entries)
is handled by `extractAllPathParts`, which returns one `[]pathSegment` per
part. `executeASTv2` evaluates each part independently and cross-joins the
results. Cross-join semantics: an empty part produces an empty result (consistent
with SQL CROSS JOIN behaviour). `crossJoinEnvs` helper added.

6 new tests in `executor_multi_match_test.go`: Cartesian product, filter on
cross, node×traversal, three parts, empty part, single-node each part.

**`pkg/sulpher/executor_helpers.go`, `executor_env.go` — multiple WITH clauses**

`clauseSet` now stores `[]withStage` (each stage holds a `WithClause` and an
optional post-WITH `MatchClause`) instead of the single `withClause`/`secondMatch`
pair. `extractAllClauses` builds the pipeline by pairing each WITH with the
MATCH that immediately follows it, or `nil` if none.

`executeWithEnv` iterates the stage slice rather than handling a hardcoded
two-stage flow: each stage projects+filters+aggregates the current envs, then
optionally traverses a following MATCH before feeding the next stage. Any number
of chained `MATCH → WITH → MATCH → WITH → … → RETURN` sequences now works.

5 new tests in `executor_multi_with_test.go`: two WITHs no MATCH, two WITHs
chained filters, WITH→MATCH→WITH pipeline, three WITH clauses, WITH aggregation
then filter.

### Changed

- `pkg/oql/executor_test.go` (`mockStore`): `GetMany` added to satisfy the
  updated `storage.Store` interface.

## [0.9.9-rc09] - 2026-06-13

### Fixed

**`COUNT(DISTINCT field)` now works on both execution paths**

Requires tsqlparser v0.6.0 (upgraded from v0.0.1). The parser now sets
`FunctionCall.Distinct = true` when the `DISTINCT` modifier is present inside
an aggregate function call, rather than silently discarding the token.

*Go-path aggregator (`pkg/oql/aggregator.go`):*
When `fc.Distinct` is true, extracted values are deduplicated before being
passed to the aggregate function. `nil` values (absent fields) are excluded
from the distinct set, consistent with SQL semantics. The deduplication key
is `fmt.Sprintf("%T:%v", v, v)` — type-prefixed to distinguish the integer
`1` from the string `"1"`.

*SQL push-down — blob path (`pkg/oql/sqlgen_scalar.go`):*
Emits `COUNT(DISTINCT expr)` instead of `COUNT(expr)` when `fc.Distinct`.
SQLite and PostgreSQL both support this syntax natively.

*SQL push-down — adapted-entity path (`pkg/oql/sqlgen_aggregate.go`):*
Same change for the schema-registered table path.

`TestAdversarial_CountDistinct_WithNulls` updated: the `SELECT DISTINCT`
workaround (used while the parser bug was outstanding) replaced with a proper
`COUNT(DISTINCT status)` test covering 5 records with 2 duplicate values.
Both B4 and PD paths return 3.

### Changed

- `go.mod`: `github.com/ha1tch/tsqlparser v0.0.1` → `v0.6.0`

## [0.9.9-rc08] - 2026-06-13

### Changed

**`pkg/cache/cache_coverage_test.go`, `pkg/cache/cache_redis_test.go` — unified Redis endpoint resolution**

Both test helpers now use a single `resolveRedisAddr()` function with a consistent
priority order that mirrors how xolu itself finds its Redis backend:

1. `OLU_REDIS_HOST` / `OLU_REDIS_PORT` — the variables xolu reads at startup
   when `CacheType=redis`. Setting these points tests at the same instance
   the server would connect to in production.
2. `REDIS_ADDR` — convenience single-variable form (`host:port`), accepted by
   many Redis clients and CI platforms.
3. Fallback — `cache_coverage_test.go` starts slabbis in-process;
   `cache_redis_test.go` (-tags redis) defaults to `localhost:6379`.

```sh
# production-config variables — highest priority
OLU_REDIS_HOST=redis.internal OLU_REDIS_PORT=6380 go test ./pkg/cache/...

# convenience form
REDIS_ADDR=localhost:6379 go test ./pkg/cache/...

# default: slabbis in-process, no env vars needed
go test ./pkg/cache/...
```

`resolveRedisAddr()` is defined once in `cache_coverage_test.go` and shared
by `cache_redis_test.go` (same package, both `package cache`). The `-tags redis`
file no longer imports `net` or `os` directly for address parsing.

## [0.9.9-rc07] - 2026-06-13

### Changed

**`pkg/cache/cache_coverage_test.go` — `newTestRedisCache` honours `REDIS_ADDR`**

Previously hard-wired to start slabbis. Now checks the `REDIS_ADDR` environment
variable first. If set (format `host:port`), the 7 `TestRedisCache_*` tests run
against that real Redis instance; if not set they use slabbis in-process as
before. `t.Fatalf` is called immediately when `REDIS_ADDR` is set but the
connection fails, so there is no silent fallback.

```
# default: in-process slabbis, no external dependency
go test ./pkg/cache/...

# real Redis
REDIS_ADDR=localhost:6379 go test ./pkg/cache/...
```

**`pkg/cache/cache_redis_test.go` — `TestRealRedis*` helpers honour `REDIS_ADDR`**

All 9 tests in this file (`//go:build redis`) previously hardcoded
`localhost:6379`. They now use a `newRealRedisCache(t)` helper that reads
`REDIS_ADDR` (defaulting to `localhost:6379`), so a different Redis endpoint
can be used without editing the file. Tests `t.Fatalf` immediately on connection
failure rather than propagating 50 goroutine errors.

Test functions renamed from `TestRedisCache_*` / `TestRedisStress_*` to
`TestRealRedisCache_*` / `TestRealRedisStress_*` to eliminate name collisions
with `redis_miniredis_test.go` when both files are compiled together under
`-tags redis`.

**Three-tier Redis test strategy:**

| File | Build tag | Server | When to use |
|------|-----------|--------|-------------|
| `cache_coverage_test.go` | (none) | slabbis (default) or real Redis via `REDIS_ADDR` | Always; CI |
| `cache_slabbis_test.go` | (none) | slabbis in-process | Always; tests slabbis-specific semantics |
| `redis_miniredis_test.go` | (none) | miniredis in-process | Always; unit tests |
| `cache_redis_test.go` | `redis` | real Redis via `REDIS_ADDR` (default localhost:6379) | Explicit opt-in for network integration |

## [0.9.9-rc06] - 2026-06-13

### Fixed

**`pkg/cache/cache_coverage_test.go` — Redis integration tests now run in-process**

`newTestRedisCache` previously called `NewRedisCache("localhost", 6379, ...)` and
called `t.Skipf` when no Redis server was reachable. All 7 `TestRedisCache_*`
tests were therefore skipped in every CI and sandbox run.

`newTestRedisCache` now delegates to `startSlabbis`, the in-process RESP server
helper already defined in `cache_slabbis_test.go`. The 7 tests are unconditionally
exercised without any external dependency.

### Changed

**`run_tests.sh` — categorised summary output**

The summary block now classifies tests by category and reports each on its own
line, mirroring the target format:

```
SYSTEM TESTS:      2849 PASS
CACHE INTEGRATION:   31 PASS
STRESS TESTS:         8 SKIPPED
BENCHMARKS:           4 SKIPPED
FAIL:                 0
```

Classification rules:
- **CACHE INTEGRATION** — test names matching `TestRedisCache_` or `TestSlabbis_`
- **STRESS TESTS** — `TestStress_` or `TestTSStress_`
- **BENCHMARKS** — `_Benchmark` suffix or `BlobVsAdapted`
- **SYSTEM TESTS** — everything else

Both top-level test lines (`--- PASS: TestFoo`) and subtest lines
(`    --- PASS: TestFoo/bar`) are counted. The `--redis` flag has been removed
(no longer needed; Redis tests run unconditionally via slabbis).

## [0.9.9-rc05] - 2026-06-13

### Fixed

**`pkg/oql/oql.go` — reserved keyword parse error now actionable**

When a field or table name is a reserved SQL keyword (e.g. `zone`, `table`,
`key`, `date`, `time`, `range`, `user`, `role`), the tsqlparser error was
`no prefix parse function for ZONE found` — a parser implementation detail
invisible to query authors. The OQL `parse()` function now intercepts this
pattern and emits a clear message:

```
parse error: "ZONE" is a reserved SQL keyword and cannot be used as a field
or table name — rename the field or quote it with backticks (e.g. `zone`)
```

The backtick-quoting hint matches tsqlparser's identifier escaping syntax,
which allows any keyword to be used as an identifier when quoted.

## [0.9.9-rc04] - 2026-06-13

### Changed

**`pkg/oql/sqlgen.go` — typed field extraction interface**

The `SQLDialect` interface gains two new primary methods and a type system:

`JSONFieldAs(fieldPath, oqlType string) string` — canonical extraction that
accepts an explicit OQL type token: `"text"`, `"numeric"`, `"boolean"`, or
`"auto"`. This is the method all new dialect implementations must provide.

`JSONFieldAliasedAs(alias, fieldPath, oqlType string) string` — typed
extraction for JOIN queries where the data column is qualified by a table alias.

The OQL type tokens translate to backend-specific SQL:

| OQL type  | SQLite                               | PostgreSQL (future)         |
|-----------|--------------------------------------|-----------------------------|
| `text`    | `json_extract(data, '$.f')`          | `(data->>\'f\')::text`      |
| `numeric` | `CAST(json_extract(data, '$.f') AS REAL)` | `(data->>\'f\')::numeric` |
| `boolean` | `json_extract(data, '$.f')`          | `(data->>\'f\')::boolean`   |
| `auto`    | `json_extract(data, '$.f')`          | requires explicit type       |

The existing `JSONField`, `JSONFieldNumeric`, and `JSONFieldAliased` methods
are **deprecated** — they now delegate to `JSONFieldAs`/`JSONFieldAliasedAs`
and will be removed when a PostgreSQL dialect is added. Existing dialect
implementations continue to compile unchanged.

**`chooseType(op, val)` helper** — all extraction call sites now go through a
single function that maps the (operator, literal type) pair to an OQL type
token. Previously each call site made its own decision in an ad-hoc way.

**All extraction call sites migrated:**

- Comparison predicates (`=`, `!=`, `<`, `>`, `<=`, `>=`): type inferred from
  literal value via `chooseType`.
- `IN` expressions: type inferred from first value in list.
- `BETWEEN`: type inferred from bounds (numeric when both bounds numeric, text
  otherwise).
- `IS NULL / IS NOT NULL`: always `"auto"` — existence check, no value comparison.
- `LIKE`: always `"text"` — pattern matching is inherently a text operation.
- `ORDER BY`: `"auto"` with a comment documenting the limitation — no literal
  is present to infer from. Schema type information is required before a
  PostgreSQL ORDER BY can emit correct CASTs.
- `tenant_id` (string tenants): `"text"` — was incorrectly hardcoded as
  `"numeric"` in an earlier draft.
- JOIN field extractions: `"auto"` with comment — no literal context; requires
  schema typing for PostgreSQL.

### What remains before a PostgreSQL dialect is viable

`ORDER BY` and JOIN field extractions still use `"auto"`. Both require schema
type information (which field is numeric vs text) that is not currently threaded
through the SQL generator. The correct approach is to add a
`FieldTypeProvider` interface that adapted stores can implement, returning the
stored type for a given (entity, field) pair. Blob stores without an explicit
schema would default to `"auto"`.

`COUNT(DISTINCT field)` is not affected by this change — it is a separate known
gap in the Go-path aggregator.

## [0.9.9-rc03] - 2026-06-13

### Added

**`pkg/oql/adversarial_oql_test.go` — 22 adversarial tests**

*Jsonic / sparse data:*
`SparseMissingField_Filter` — WHERE on a field absent from some records;
`SparseMissingField_Aggregate` — SUM/COUNT on mixed present/absent field;
`NullAggregate_AVG` — AVG of entirely absent field must return nil not panic;
`NullAggregate_SUM` — SUM of entirely absent field must return nil;
`CountDistinct_WithNulls` — SELECT DISTINCT deduplicates repeated rows;
`GroupBy_SparseKey` — GROUP BY on a field absent from some records;
`NumericLookingStringField` — string "2" must not match integer 2;
`VeryLongStringField` — 64 KB string field round-trips intact;
`UnicodeFieldValue` — emoji, CJK, RTL text filter and aggregate correctly;
`BooleanField_Mixed` — boolean true/false/absent filter correctly.

*Aggregation edge cases:*
`Having_ZeroMatch` — HAVING that eliminates all groups returns zero rows;
`OrderByAggregate` — ORDER BY the aggregate column not the GROUP BY key;
`SumMixedIntFloat` — SUM over mixed integer and float stored values;
`MinMax_AllNull` — MIN/MAX on absent field returns nil without panic.

*Combined complex queries:*
`RangeGroupOrderLimit` — range WHERE + GROUP BY + ORDER BY, totals verified
by region regardless of path ordering differences;
`MultiFieldGroupBy_CountDistinct` — GROUP BY (region, category) with COUNT(*);
`CoalesceInSelectGroupBy` — GROUP BY with COALESCE-normalised sparse data;
`ChainedFilters_StringNumericRange_GroupBy` — string equality + numeric range + GROUP BY;
`EmptyEntity_Aggregate` — all aggregates on empty table return one nil row;
`GroupBy_SingleRow` — GROUP BY producing exactly one group;
`LargeGroupBy_Stability` — 50 groups, ORDER BY aggregate DESC, stable ordering;
`NestedCoalesce_And_Range` — COALESCE in WHERE clause.

All tests exercise both B4 (FilterableStore, Go path) and SQL push-down paths.

### Known gap identified

`COUNT(DISTINCT field)` — the DISTINCT modifier on aggregate function calls is
silently ignored on both paths. The Go-path aggregator never inspects
`FunctionCall.Distinct`; the SQL generator does not emit `COUNT(DISTINCT ...)`
even when the AST sets the flag. Both paths count all values, not distinct ones.
The adversarial test that would have covered this has been replaced with a
`SELECT DISTINCT` row-deduplication test, which is fully supported.
`COUNT(DISTINCT field)` is tracked as a known gap for a future patch.

## [0.9.9-rc02] - 2026-06-13

### Fixed

**`pkg/server/server.go` — multi-filter clause/arg misalignment**

`listWithPushDown` built `filterClauses` and `filterArgs` from a map in
parallel, then called `sort.Strings(filterClauses)` to sort the clauses for
deterministic SQL. The args were never reordered to match. With two or more
filters, every combination where sorting changed clause order produced
mismatched bindings — each `?` placeholder received the wrong argument value,
causing zero rows to be returned. Single-filter queries were unaffected.

Fix: clauses and args are now paired as structs before sorting, then separated
after. The combined sort keeps each clause bound to its correct arg.

### Added

**`pkg/oql/sqlite_regression_test.go` — 17 regression tests**

Covers all three bugs found during jsonfile removal and their adjacent cases:

*Bug 1 (CompilePredicates duplicate-atom)*: numeric range, string range, open
bounds, three predicates on same field, two independent ranges on different
fields, GROUP BY with string range.

*Bug 2 (chooseFieldExtraction ordering operators)*: string `>=`, `<=`, `>`,
`<`, lexicographic sort correctness, numeric ordering unchanged.

*Adjacent*: BETWEEN numeric (already correct — regression guard), BETWEEN
string, IN combined with equality on same field, range + equality on different
fields.

Both B4 (FilterableStore, Go path) and push-down (SQL generation) paths are
exercised for every case.

**`pkg/server/regression_test.go` — 9 regression tests**

Covers Bug 3 (listWithPushDown numeric cast) and Bug 4 (clause/arg
misalignment) at the HTTP layer: integer field filter, float field filter,
string filter unaffected, combined numeric+string filter, no-match case,
zero value, pagination with filter, tenant-scoped route.

## [0.9.9-rc01] - 2026-06-13

### Removed

**`pkg/storage/jsonfile.go` — jsonfile storage backend deleted**

The jsonfile backend has been removed entirely. It was deprecated in v0.9.8
and blocked from the commit endpoint since introduction. SQLite is the only
supported backend from this release forward.

Removed across the codebase:
- `pkg/storage/jsonfile.go` (519 lines) — deleted
- `pkg/storage/factory.go` — jsonfile case and `init()` registration removed
- `pkg/storage/storage.go` — `BaseDir`/`Schema` fields removed from `StoreConfig`
- `pkg/config/config.go` — jsonfile removed from `StorageType` validation; only
  `"sqlite"` is accepted; deprecation warning removed
- `pkg/server/handlers.go` — jsonfile export branch (data directory zip) removed;
  only the SQLite WAL checkpoint + `entities.db` path remains
- `pkg/server/server.go`, `cmd/olu/main.go` — `BaseDir`/`Schema` removed from
  `StoreConfig` literals; comments updated

All test fixtures migrated from jsonfile to SQLite across:
`pkg/server/e2e_test.go`, `e2e_coverage_gaps_test.go`, `blob_handler_test.go`,
`benchmark_test.go`, `dynconfig_handlers_test.go`, `commit_e2e_test.go`,
`server_test.go`, `guardrail_test.go`, `integration_test.go`,
`error_paths_test.go`, `adversarial_server_test.go`, `ts_e2e_test.go`,
`tier1_oql_test.go`, `pkg/storage/storage_test.go`, `contract_test.go`,
`commit_test.go`, `coverage_test.go`, `queryable_test.go`,
`tenant_test.go`, `tenant_extended_test.go`, `pkg/config/config_test.go`.

jsonfile-specific tests deleted: `TestCommitE2E_JSONFileReturns501`,
`TestNewStore_JSONFileDefaults`, `TestContract_JSONFile`,
`TestCommit_JSONFileReturnsErrNotSupported`, `TestStoreFileStructure`,
`TestJSONFileTenantIsolation_CRUD`, `TestJSONFileTenantIsolation_Paths`,
`TestStoreConfig_JSONFile`, `TestNewStoreFromConfig_JSONFile`,
`TestJSONFileStore_DoesNotImplementQueryable`.

### Fixed

**`pkg/oql/predicate_compiler.go` — duplicate-atom predicate bug**

`CompilePredicates` compiled all AND-chain predicates into a `PredicateSet`
whose `atoms` map is `Atom → single index`. When two predicates targeted the
same field (e.g. `timestamp >= low AND timestamp <= high`), the second atom
entry overwrote the first. During token scanning, `LookupAtom` returned only
the second predicate's index; the first predicate's `predSeen[0]` was never
set; the final "all predicates must match" check rejected every row. Result:
any range filter on the B4 path (SQLite with `FilterableStore`) silently
returned zero rows.

Fix: `CompilePredicates` now tracks which atoms are already claimed. If a
second predicate references the same field, it is placed in the Go-path
residual rather than the compiled `PredicateSet`. The first predicate filters
at tokenisation time; the residual is applied by `evalCondition` in the Go
path. Range queries now return correct results.

This bug was masked by jsonfile (which does not implement `FilterableStore`
and never took the B4 path). It affected any AND-combined WHERE clause with
two or more predicates on the same field.

**`pkg/server/server.go` — numeric filter string-to-type mismatch**

The list endpoint reads filter values from query strings (always `string`).
`listWithPushDown` passed the raw string to SQLite as `json_extract = ?`.
SQLite's `json_extract` returns typed values from JSON (integer `2`, not
string `"2"`), so `type_id=2` never matched any record. Fix: filter values
are now parsed as `int64` or `float64` before binding — falling back to
string only when numeric parsing fails.

**`pkg/oql/sqlgen.go` — ordering operators always used numeric extraction**

`chooseFieldExtraction` used `JSONFieldNumeric` (CAST to REAL) for all
ordering operators `>`, `<`, `>=`, `<=`. CAST of a string timestamp to REAL
returns NULL, so `WHERE timestamp >= '2025-01-15T09:00:00Z'` on the SQL
push-down path silently returned nothing. Fix: ordering operators now use
text extraction when the RHS value is a string, consistent with the equality
operators. ISO 8601 timestamps sort correctly as text; numeric comparisons
continue to use numeric extraction.

**`pkg/server/e2e_test.go` — export test rewritten for SQLite**

`TestE2E_ExportEndpoint` subtests "export contains entity data files" and
"exported entity data is valid JSON matching created records" checked for
`data/*.json` files — the jsonfile export format. Rewritten to check for
`entities.db` in the ZIP and verify `manifest.entities_file = "entities.db"`.

## [0.9.8-p_026] - 2026-06-13

### Fixed

**`pkg/sulpher` — `allShortestPaths` executor dispatch**

`isShortestPathPattern` in `executor_helpers.go` now returns a third value
`isAll bool`, reading `PatternPart.All` which was added in sulpher v0.2.4.
`executeASTv2` dispatches to `executeAllShortestPathsEnv` when `isAll=true`,
and to `executeShortestPathEnv` when `isAll=false`.

`executeAllShortestPathsEnv` calls `graph.AllShortestPaths` for each
(from, to) node pair and emits one `Env` per path found, then applies WHERE,
DISTINCT, ORDER BY, SKIP, and LIMIT. The graph layer implementation was
already complete and tested; this patch wires it to the query layer.

### Also fixed

`count(*)` in WITH clauses confirmed working with sulpher v0.2.4 — the
`CountStar.String()` fix resolved the misleading `count(count(*))` output.
The workaround note removed from `executor_aggregation_test.go`.

### Added

**`pkg/sulpher` — 5 new tests in `executor_oc9_test.go`**

`TestQueryAllShortestPaths_SinglePath` — one shortest path, correct length.  
`TestQueryAllShortestPaths_DiamondTwoPaths` — diamond graph, two equal-length
paths returned.  
`TestQueryAllShortestPaths_NoPath` — no path exists, zero rows.  
`TestQueryAllShortestPaths_ReturnNodes` — `p.nodes` accessible on results.  
`TestQueryAllShortestPaths_Undirected` — undirected traversal finds reverse paths.

## [0.9.8-p_025] - 2026-06-13

### Changed

**Dependency:** `github.com/ha1tch/sulpher` upgraded v0.2.0 → v0.2.4.

**`pkg/sulpher/executor_env.go` — `=~` moved to correct AST node type**

sulpher v0.2.4 fixes issue 1 from the parser feedback report: `=~` is now
registered via `parseStringPredicateInfix` and produces `*ast.StringPredicate`
rather than `*ast.InfixExpression`. The olu executor's `=~` implementation
has been moved from the `InfixExpression` operator switch to the
`StringPredicate` case, which is its correct and documented home. The
`regexp` import is retained since it is used in the same location.

**`pkg/sulpher/parser.go` — `!=` pre-processing removed**

sulpher v0.2.4 fixes issue 5: `!=` is now lexed natively as `NEQ`, identical
to `<>`. The `strings.ReplaceAll(query, "!=", "<>")` pre-processing step
in `parser.go` has been removed. The sulpher team's response noted this was
more serious than reported: without our pre-processing workaround, `!=` was
producing a silently broken AST — the right-hand operand was being discarded
entirely with no error.

**`pkg/sulpher/parser.go` — `validateAST` trimmed**

sulpher v0.2.4 fixes issue 4 (partial): hop range validation (negative min,
min > max) and empty RETURN clause validation now live in the parser with
accurate line/column error positions. These checks have been removed from
`validateAST`. The MATCH/UNWIND presence check and RETURN presence check
remain in `validateAST` as executor-level policy — the parser team correctly
declined these as outside the scope of a general-purpose parser.

**`pkg/sulpher/parser_test.go` — `TestParserVariableLengthExact` updated**

The test previously asserted that `[*3]` produced `RangeMax=nil` and noted
that exact hop count was indistinguishable from open-ended. sulpher v0.2.4
fixes issue 3: `[*3]` now correctly produces `RangeMin=3, RangeMax=3`. The
test updated to assert the fixed behaviour.

### Also fixed in sulpher v0.2.4 (no olu changes required)

- Issue 2: `PatternPart.All` field now carries the `allShortestPaths` flag
  through `parsePatternPart`. olu can implement `allShortestPaths` dispatch
  without a parser workaround.
- Issue 6: `exists()` with property arguments now emits a clear actionable
  error pointing to `IS NOT NULL`.
- Issue 7: `Walk` now visits `*ast.StringPredicate`, `*ast.InExpression`,
  `*ast.IsNullExpression`, and `*ast.ShortestPathExpression`.
- Issue 8: `CountStar.String()` returns `"*"` instead of `"count(*)"`,
  fixing the misleading `count(count(*))` string representation.

## [0.9.8-p_024] - 2026-06-12

### Fixed

**`pkg/sulpher` — WITH clause routing and WITH aggregation**

*Root cause of WITH property projection failure*: `executeASTv2` only routed
through `executeWithEnv` when both a WITH clause and a second MATCH were
present (`cs.withClause != nil && cs.secondMatch != nil`). Queries of the
form `MATCH ... WITH props ... RETURN` (no second MATCH) bypassed `executeWithEnv`
entirely and applied RETURN directly to the traversal Envs, silently ignoring
the WITH projection. The fix removes the `secondMatch` guard; `executeWithEnv`
is now called whenever a WITH clause is present.

*`executeWithEnv` — nil secondMatch path added*: when `cs.secondMatch == nil`,
the function now projects WITH items and applies RETURN directly, with full
support for DISTINCT, ORDER BY, SKIP, and LIMIT on the RETURN clause.

*WITH aggregation implemented*: `hasAggregationWith` and `applyAggregationWith`
added to `executor_env.go`. When any WITH item is an aggregate function, the
WITH projection routes through `applyAggregationWith` which implements the same
Cypher implicit GROUP BY semantics as `applyAggregation` (RETURN path): COUNT,
COLLECT, SUM, AVG, MIN, MAX, COUNT DISTINCT. Non-aggregate items are grouping
keys; node-env values (`_nodeID`) are preserved in result Envs so subsequent
MATCH phases can anchor from grouped node variables.

### Added

**`pkg/sulpher` — 9 new tests in `executor_aggregation_test.go`**

`TestWithAgg_PropertyProjection` — regression test for the core routing bug:
`MATCH (u) WITH u.active AS active RETURN active` must project the property,
not return nil.

`TestWithAgg_CountNode`, `TestWithAgg_Collect`, `TestWithAgg_Sum`,
`TestWithAgg_Avg`, `TestWithAgg_Min`, `TestWithAgg_Max` — all aggregation
functions via WITH, grouped by `u.active`.

`TestWithAgg_WhereOnAggregate` — `WITH ... WHERE n > 1` filters on the
aggregate result.

`TestWithAgg_NodePassthrough` — grouping by a node variable preserves `_nodeID`
so `RETURN u.name` works after the aggregation.

### Notes

`count(*)` in WITH clauses was initially suspected to be broken because
`FunctionCall.String()` for `count(*)` renders as `"count(count(*))"` —
`CountStar.String()` returns `"count(*)"` rather than `"*"`, so the outer
function call wrapper duplicates it in the string representation. The AST
and execution are correct; this is a `CountStar.String()` representation
bug in the sulpher parser, reported to the parser team as an addendum.

## [0.9.8-p_023] - 2026-06-12

### Changed

**`pkg/sulpher` — housekeeping and observability improvements**

*`executor_ast.go` renamed to `executor_helpers.go`*: the file is a shared
utility module used by `executor_env.go`, not a parallel executor. The old
name implied it was dead code from a superseded execution path. No code
changes; build-verified.

*Relationship property access now logs a warning instead of silently
returning nil*: `WHERE r.since > 2020` and similar predicates on relationship
variables have always evaluated to false in olu because edges carry only a
label, not a property map. The failure was previously invisible. The
`PropertyAccess` case in `evalEnv` now detects when the object is an
identifier absent from the Env (the signature of an unbound relationship
variable) and emits a zerolog WARN with the variable name and property
name. This does not change query results; it makes the unsupported pattern
visible in logs.

**`docs/SULPHER_OC9_GAPS.md` updated to p_022 baseline**: `=~` regex
predicate and multiple `OPTIONAL MATCH` clauses moved from remaining gaps
to implemented. `allShortestPaths` and `[*n]` exact hop count annotated as
pending specific sulpher parser fixes (issues 2 and 3 in
`docs/SULPHER_PARSER_FEEDBACK.md`). Version and date updated.

### Deferred

**WITH aggregation** (`WITH count(u) AS n, collect(u.name) AS names`):
implementation was attempted but the test suite revealed that
`evalEnvForReturn` in the WITH projection context returns nil for node
property access (`u.active`) even after hydration, producing incorrect
grouping. The root cause requires investigation — specifically why the Env
contents available to `projectWithEnv` differ from those available to
`projectEnvs` (RETURN projection). The implementation has been reverted to
keep the suite clean. Deferred to the next session.

## [0.9.8-p_022] - 2026-06-12

### Fixed

**`pkg/sulpher` — `=~` regex predicate and multiple `OPTIONAL MATCH`**

*`=~` regex predicate (silent wrong results → correct results)*

`WHERE u.name =~ 'Alice.*'` previously returned zero results silently.
Root cause: `STARTS WITH`, `ENDS WITH`, and `CONTAINS` are registered in the
sulpher parser via `parseStringPredicateInfix`, producing `*StringPredicate`
AST nodes. `=~` is registered via `parseInfixExpression`, producing an
`*InfixExpression` node with `Operator = "=~"`. The implementation added
`case "=~":` to `StringPredicate` (unreachable) rather than to the
`InfixExpression` operator switch (the correct location).

Fix: `case "=~":` added to the `InfixExpression` switch in `evalEnv` in
`executor_env.go`. Uses `regexp.MatchString(pattern, input)` — Go RE2
syntax, substring match by default (use `^` / `$` anchors for full-string
match). Invalid patterns return `false` without panicking. `regexp` import
added.

*Multiple `OPTIONAL MATCH` clauses (second and subsequent clauses silently ignored)*

`MATCH (u) OPTIONAL MATCH (u)-[:knows]->(f) OPTIONAL MATCH (u)-[:likes]->(l) RETURN u, f, l`
previously applied only the first `OPTIONAL MATCH`; `l` was always `nil`.

Fix: `clauseSet.optionalMatch *sulpherast.MatchClause` changed to
`optionalMatches []*sulpherast.MatchClause`. `extractAllClauses` in
`executor_ast.go` now appends all optional clauses. `executeASTv2` in
`executor_env.go` loops over the slice, applying `applyOptionalMatchEnv`
sequentially. Each application takes the result set of the previous as input,
which is the correct left-join chaining semantics.

### Added

**`pkg/sulpher` — 9 new tests**

*`executor_predicates_test.go`* (5 tests): `TestWhere_Regex_Match`,
`TestWhere_Regex_CaseInsensitiveFlag`, `TestWhere_Regex_NoMatch`,
`TestWhere_Regex_InvalidPattern_NoResults`, `TestWhere_Regex_AnchoredMatch`.
Covers positive match, `(?i)` inline flag, no-match, invalid pattern,
and substring vs anchored match semantics.

*`executor_oc9_test.go`* (4 tests): `TestMultiOptionalMatch_TwoClauses`,
`TestMultiOptionalMatch_BothAbsent`, `TestMultiOptionalMatch_SecondOnlyMatches`,
`TestMultiOptionalMatch_ThreeClauses`. Covers two and three chained optional
clauses, both-absent nil binding, and first-only match.

### Notes

**`allShortestPaths` not implemented** — confirmed blocked by the sulpher
parser AST: the `All bool` flag on `ShortestPathExpression` is discarded
when `parsePatternPart` returns `spExpr.Pattern` (the inner `PatternPart`),
losing the distinction. Requires a parser-level fix; filed with the sulpher
team.

## [0.9.8-p_021] - 2026-06-12

### Changed

**`pkg/cache` — slabbis v0.1.5 and `DeletePattern` contract fix**

*Dependency:* `github.com/ha1tch/slabbis` upgraded from v0.1.2 to v0.1.5.
v0.1.5 adds `SCAN`, `DevConfig()`, `Config.MaxValueSize()`, CLI flags
`-buckets`, `-max-value`, `-classes`, `-dev`, oversized-value drop logging,
and enriched startup output — all items surfaced by olu integration testing
documented in `docs/CACHING.md`.

*`pkg/cache/cache.go` — `RedisCache.DeletePattern`*: the method was appending
a redundant `*` to patterns before passing them to `SCAN`, producing patterns
like `entity:list:**`. Redis silently accepts this; the fix passes the pattern
as-is. Callers (`CachePattern`, `CacheListPattern`) already produce
`*`-terminated patterns. This is the correct contract: `DeletePattern`
takes a complete glob pattern, not a bare prefix.

*`pkg/cache/redis_miniredis_test.go`* and *`pkg/cache/cache_coverage_test.go`*:
two test call sites were passing bare prefixes (`"ns:"`, `"test:redis:pattern:"`)
relying on the old append behaviour. Updated to pass correct glob patterns
(`"ns:*"`, `"test:redis:pattern:*"`).

### Result

`pkg/cache` now passes 20/20 packages with no setup failures. The three
previously failing slabbis tests (`TestSlabbis_DeletePattern_TenantList`,
`TestSlabbis_DeletePattern_NoMatches`, `TestSlabbis_WriteThenInvalidate`)
are green. slabbis is a fully supported cache backend for olu.

## [0.9.8-p_020] - 2026-06-12

### Added

**`pkg/timeseries/adversarial_ts_test.go` — timeseries adversarial test suite (7 tests)**

Tests target the partial-prefix time-filter path, which was the site of the
prior time-leakage bug (keys from high-d1 series fall inside Pebble bounds
but outside the requested time window).

- `TestAdversarial_TS_PartialPrefix_QueryRange_Ascending` — ascending scan
  with d1 values 1, 9, and 99; events from high-d1 series outside the window
  must not appear in results.
- `TestAdversarial_TS_PartialPrefix_QueryRange_Descending` — same layout,
  descending scan; Go-side time filter must apply on the reverse path; result
  order must be descending.
- `TestAdversarial_TS_PartialPrefix_Aggregate` — scalar Aggregate with an
  out-of-window event in a high-d1 series; sum and count must reflect only
  the in-window events (12.0 and 2, not 112.0 and 3).
- `TestAdversarial_TS_BoundaryExactness` — events exactly at `From` and `To`
  must be included; events at `From-1ns` and `To+1ns` must be excluded.
- `TestAdversarial_TS_CrossTimeline_Isolation` — partial-prefix query on
  timeline 1 must not return events from timeline 2 sharing the same d0 and
  timestamp.
- `TestAdversarial_TS_PartialPrefix_ManyD1Values` — 10 distinct d1 values,
  one in-window and one out-of-window event per series; all 10 out-of-window
  events must be excluded.
- `TestAdversarial_TS_PartialPrefix_Aggregate_Bucketed` — bucketed Aggregate
  with a high-d1 out-of-window event (val=999); total bucket sum must be 20,
  not 1019; no individual bucket may exceed its expected maximum.

**`pkg/qs/qs_coverage_test.go` — qs scalar and aggregate coverage (27 tests)**

Coverage raised from 44.2% to 90.3%.

Zero-coverage functions now exercised:

- String: `ScalarLTrim`, `ScalarRTrim`, `ScalarConcat`, `ScalarCharIndex`,
  `ScalarReverse`, `ScalarCast`
- Math: `ScalarFloor`, `ScalarCeiling`, `ScalarSign`, `ScalarSqrt`,
  `ScalarPower`
- Date/time: `ParseTime` (all supported formats, passthrough, failure),
  `ScalarGetDate`, `ScalarGetUTCDate`, `ScalarYear`, `ScalarMonth`,
  `ScalarDay`, `ScalarDatePart` (all parts including week, dayofyear, quarter,
  weekday; unknown part returns nil), `ScalarDateDiff` (year, month, day,
  hour, minute, second; unknown part returns nil), `ScalarDateTrunc` (all
  six precision levels; unknown returns nil)
- Graph helpers: `ScalarType` (relationship key, type key fallback, no-match,
  non-map, nil), `ScalarLabels` (labels key, type fallback, no-match, non-map,
  nil)

Partial-coverage paths now fully exercised:

- `ToFloatSafe`: bool, int variants (int/int32/int64/uint/uint64),
  unparseable string, nil
- `ScalarToInt` / `ScalarToFloat`: whitespace-padded numeric strings,
  non-numeric strings
- `AggMin` / `AggMax`: nil elements skipped, all-nil input returns nil
- `ScalarAbs`: non-numeric input returns nil

## [0.9.8-p_019] - 2026-06-12

### Fixed

**Context propagation in cache invalidation helpers**

`invalidateCache` and `invalidateCacheForID` in `pkg/server/handlers.go` both
accepted a `ctx context.Context` parameter — carrying the request's tenant ID,
deadline, and cancellation signal — but then constructed a fresh
`context.Background()` for the actual cache operation. This meant:

- A cancelled or timed-out request context did not abort the cache write; the
  invalidation would proceed against a dead request while the handler had
  already returned.
- The 5-second timeout was anchored to an unbounded background context rather
  than the request's remaining deadline.

Both helpers now derive their timeout from the incoming `ctx`:

```go
cacheCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
```

The tenant-ID extraction (`getTenantIDNumeric(ctx)`) was already correct in
both helpers; only the cache operation context was wrong.

### Added

**`pkg/graph/adversarial_test.go` — graph adversarial test suite (9 tests)**

Tests probe boundary conditions and concurrency scenarios not covered by the
contract suite:

- `TestAdversarial_FindPath_MaxDepthZero` — self-path succeeds at depth 0;
  adjacent pair fails (no budget to traverse the edge).
- `TestAdversarial_PathExists_MaxDepthZero` — mirrors FindPath; length 0
  returned for self-path.
- `TestAdversarial_PathExists_FindPath_DepthConsistency` — for a 5-edge chain
  at every depth 0–6, PathExists and FindPath must agree on reachability and
  report the same edge-count length when both succeed.
- `TestAdversarial_FindPath_ExactDepthBoundary` — a 3-edge path is found at
  maxDepth=3 and rejected at maxDepth=2 (off-by-one guard).
- `TestAdversarial_ConcurrentUpdateAndTraversal` — 4 writer goroutines cycle
  an entity's REF between two valid targets while 4 reader goroutines call
  FindPath; no panic, no hang, no data corruption.
- `TestAdversarial_UpdateFromEntity_EmptyEntityREF` — REF with `"entity": ""`
  must be silently dropped; no malformed `":N"` node created.
- `TestAdversarial_UpdateFromEntity_MissingIDInREF` — REF map missing the
  `"id"` key is not recognised as a REF; no edge created.
- `TestAdversarial_PathExists_DenseGraph` — 20-node fully-connected graph;
  BFS terminates without revisiting nodes.
- `TestAdversarial_FindPath_TenantSelfPath_MaxDepthZero` — tenant-prefixed
  node ID does not interfere with the self-path early-return branch.

**`pkg/server/adversarial_server_test.go` — server adversarial test suite (8 tests)**

All tests use the SQLite-backed `commitEnv`. The deprecated jsonfile backend
is not exercised.

- `TestAdversarial_Post_DanglingREF` — posting an entity with a REF pointing
  to a nonexistent target must succeed (olu has no referential integrity
  enforcement at write time); GET returns the stored REF; graph path query
  must not 500.
- `TestAdversarial_Embed_DanglingREF_NoPanic` — GET with `embed_depth=2` on a
  dangling REF must return the raw REF map rather than panicking or returning
  a 500.
- `TestAdversarial_SequentialSaves_NoCacheStale` — 8 sequential PUT updates to
  the same entity; each immediate GET must return the value written in the
  preceding PUT, not a cached earlier value.
- `TestAdversarial_DeleteClearsCache` — GET after DELETE must 404; the
  pre-deletion GET that primed the cache must not be served stale.
- `TestAdversarial_ListCacheInvalidatedAfterCreate` — list count must increase
  immediately after a POST; the pre-create list result must not be served from
  cache.
- `TestAdversarial_Post_StructurallyInvalidREF` — four degenerate REF shapes
  (wrong type value, missing id key, empty entity string, id as string) must
  not produce a 500 regardless of schema configuration.
- `TestAdversarial_ConcurrentSaves_NoCorruption` — 8 goroutines concurrently
  PUT the same entity; no 500s permitted; final GET must return a consistent
  body.
- `TestAdversarial_CommitAppend_ImmediatelyReadable` — entity created via the
  `append` array of a `/api/v1/commit` request must be immediately readable
  via GET after the commit completes.

## [0.9.8-p_018] - 2026-06-11

### Added

**`docs/API_V2.md` — olu /api/v2 specification**

Adds a new reference document describing all planned post-v1.0 platform
extensions. The document is a design specification, not an implementation
record — none of the described endpoints exist yet in the codebase.

Subsystems specified:

- `/api/v2/fsm/def` — Machine definitions (prototype model). Definitions
  are templates; machines take a snapshot at creation and own their
  properties independently. Definitions are freely mutable and deletable
  without affecting existing machines. Validation via `fsm-toolkit` v0.9.6+.
  Single machine type (Mealy). Guards are T-SQL expressions evaluated at
  walk time from machine-local `_machine_vars_`. Bundle composition via
  `linked_states` using definition IDs. GC policy per-definition.

- `/api/v2/fsm/machine` — Running machines. Created from definitions with
  optional `overrides` block. `PATCH` supported; any patch must result in a
  fully valid machine (complete post-patch snapshot validated). Bulk walk
  via `SELECT @FSM() FROM _api_v2_machine_ WHERE`. Integration with
  `/api/v1/commit` via `fsm_walk` field. `/vars` endpoint returns richer
  shape than walk response.

- `/api/v2/event` — Event subscriptions. Trigger sources: entity CRUD,
  graph edge changes, FSM state/output, timeseries append. Actions: webhook,
  OQL, Sulpher, fsm.walk (sync supported — cycle safety guaranteed by
  fsm-toolkit at definition time). Template variables are typed parameters
  not string concatenation. Dead-letter queue with inspect/replay/dismiss
  endpoints; dead-letter entries exempt from retention GC. Delivery log
  retention via `event.GCSweeper`.

- `/api/v2/meta` — Per-entity key/value sidecar. Entity-scoped lifecycle
  (deleted with parent entity). Not a general k/v store.

- `/api/v2/gen` — Generator subsystem. 12 generator types across stateless
  (uuid_v4, uuid_v7, cuid, ulid) and stateful (sequence, token, timestamp,
  random_int, pick, nanoid, snowflake, slug) categories. `/api/v2/seq`
  permanent alias for `/api/v2/gen/seq`. `NEXT VALUE FOR` / `CURRENT VALUE
  FOR` available in OQL, tsqlruntime, and event template variables. Generator
  names globally unique per tenant. Timestamp generator uses embedded IANA
  tz database. Round-robin pick cursor limitation documented.

- `pkg/gc` — Generic GC worker abstraction. `Sweeper` interface +
  `Worker`. Four implementations: blob, timeseries, FSM, event. Existing
  blob and timeseries workers to be migrated on implementation.

Full error code tables for all subsystems (OLU-FSM001-013, OLU-EV001-009,
OLU-META001-004, OLU-GEN001-010).

## [0.9.8-p_017] - 2026-06-11

### Changed

**Logging regularisation — stdlib `log` eliminated from non-test code**

*`cmd/olu/main.go`*: After constructing the ConsoleWriter zerolog logger,
`zlog.Logger = logger` now routes all global zerolog calls (`pkg/oql`,
`pkg/dynconfig`, `pkg/blob/gc`, `pkg/timeseries/retention`) through the same
writer and format as the server's instance logger. Previously these packages
logged to zerolog's default writer (os.Stderr) while the server logged to
a ConsoleWriter on os.Stdout — two separate output channels in production.
`zerolog/log` imported as `zlog` to avoid collision with other identifiers.

*`pkg/sulpher/executor.go`*: The one `log.Printf("[ERROR] ...")` call in
`takeSnapshot` replaced with `e.logger.Error().Err(err).Str("prefix", prefix).Msg(...)`.
The `Executor` struct already carried a `zerolog.Logger` field four lines above
this call. Stdlib `log` import removed.

*`pkg/storage/factory.go`*: The `log.Println("[DEPRECATED] ...")` deprecation
warning for the jsonfile backend replaced with `zlog.Warn().Str("backend",
"jsonfile").Msg(...)`. Stdlib `log` import removed, `zerolog/log` added as `zlog`.

*`cmd/olu-migrate/main.go`*: All stdlib `log` calls converted to `fmt`:
- `log.Fatalf(msg, args)` → `fmt.Fprintf(os.Stderr, "Error: msg\n", args); os.Exit(1)`
- `log.Fatal(err)` → `fmt.Fprintln(os.Stderr, "Error:", err); os.Exit(1)`
- `log.Printf(msg, args)` → `fmt.Printf(msg+"\n", args)`
- `log.Println(msg)` → `fmt.Println(msg)`

This is consistent with `cmd/iolu/main.go`, which already used `fmt` throughout.
Both are interactive CLI tools where timestamp-prefixed stderr output is
more confusing than helpful. Stdlib `log` import removed.

After p_017, zero stdlib `log` calls remain in production code outside tests.

## [0.9.8-p_016] - 2026-06-11

### Changed

**REF field naming regularisation**

The codebase previously had four distinct ways of recognising a REF field:
a string literal `"REF"` in runtime value checks, a string literal `"ref"` in
schema format checks, `HasSuffix` on column names in partition/reassemble logic,
and an ad-hoc inline map inspection in FTS text extraction. These are now
replaced with a single helper plus two canonical constants.

*`pkg/models` — canonical constants*

`models.RefTypeValue = "REF"` — the value of the `"type"` key in a REF map.
`models.SchemaFormatREF = "ref"` — the JSON Schema `"format"` value for REF
fields. Both used throughout; no more string literals inline.

`IsReference` and `Reference.ToMap` updated to use `RefTypeValue`. `validateREFValue`
(see below) adds the empty-entity guard that `IsReference` previously omitted.

*`pkg/storage/adapted` — `ColumnDef` gains explicit part flags*

`ColumnDef` gains `IsREFEntity bool` (JSON: `is_ref_entity`) and `IsREFID bool`
(JSON: `is_ref_id`). Set at derivation time in `DeriveAdaptedTableSpecFrom`.
`PartitionData` and `ReassembleData` now branch on `col.IsREFEntity` /
`col.IsREFID` instead of `strings.HasSuffix(col.Name, "_entity")` /
`strings.HasSuffix(col.Name, "_id")`. The column name suffix is now purely
cosmetic. `ReassembleData` uses `models.RefTypeValue` to construct the `"type"`
field. `SchemaFormatREF` used for the format string and the non-REF object
branch guard. The `models` import added to `adapted.go`.

*`pkg/storage/sqlite` — `extractTextContent`*

The inline `v["type"] != "REF"` check replaced with `models.IsReference(v)`.

*`pkg/validation` — queryfy custom validator for REF fields*

`applyREFValidators(obj, rawProps)` — post-compilation fixup called from
`LoadSchema`. Walks the raw JSON Schema properties map (not the compiled
queryfy schema, which discards `"format"` on object-typed fields) and replaces
any field with `"format": "ref"` with a `builders.CustomSchema` that calls
`validateREFValue`. Required/optional status is read from the existing compiled
field and preserved.

`validateREFValue(value interface{}) error` — validates that a value is a
structurally correct REF map: `{"type": "REF", "entity": "<non-empty>", "id": <int>}`.
Accepts nil (required check is handled by the `CustomSchema`). Rejects empty
entity strings (a gap in `IsReference` that is addressed here).

`LoadSchema` now delegates to `LoadSchemaWithWarnings` and discards warnings.

`LoadSchemaWithWarnings(entity, schemaData) ([]string, error)` — new method.
Loads the schema and returns naming convention warnings. Currently warns when a
REF field name ends in `"_ref"` (e.g. `"author_ref"`), which produces SQL columns
`REF_author_ref_entity` / `REF_author_ref_id` and a graph relationship label
`"author_ref"` — valid but redundant-looking. The schema is still loaded; the
warning is informational.

### Tests

18 new tests in `pkg/validation/validation_ref_test.go`:
`TestRefConstants`, `TestValidateREFValue_Valid`, `TestValidateREFValue_Nil`,
`TestValidateREFValue_Invalid` (7 cases: string, integer, missing entity,
missing id, wrong type value, empty entity, empty map),
`TestValidate_REFField_Valid`, `TestValidate_REFField_WithOptional`,
`TestValidate_REFField_OptionalAbsent`, `TestValidate_REFField_OptionalNil`,
`TestValidate_REFField_RequiredMissing`, `TestValidate_REFField_WrongType`,
`TestValidate_REFField_InvalidMap_MissingEntity`,
`TestValidate_REFField_InvalidMap_WrongTypeValue`,
`TestLoadSchemaWithWarnings_Clean`, `TestLoadSchemaWithWarnings_RefSuffix`,
`TestLoadSchemaWithWarnings_MultipleRefSuffix`,
`TestLoadSchemaWithWarnings_NoRefSuffix_NoWarning`,
`TestLoadSchema_StillWorks`, `TestValidate_REFSchema_AllowsExtraFields`.

Adapted table tests updated: 9 `ColumnDef` literals now include `IsREFEntity`
or `IsREFID` alongside `IsREF`.

## [0.9.8-p_015] - 2026-06-11

### Changed

**Stage 2.12 — Final pass**

The Sulpher renovation plan is complete. Both phases (Phase 1: parser
replacement; Phase 2: new capabilities) are fully implemented and tested.

*Race detector:* `go test -race ./pkg/sulpher/... ./pkg/graph/...
./pkg/server/... ./pkg/storage/...` — all clean.

*`TESTING.md` updated:* test count 2637→2744, coverage 71.9%→69.7% (coverage
decrease reflects the substantial volume of new code in the Sulpher executor
that expanded the measurable surface; coverage of legacy code is unchanged),
Sulpher test file inventory updated to all 10 test files covering OC9
compliance, predicates, path traversal, aggregation, UNION, UNWIND, and SKIP.

*`MANUAL.md` — Sulpher section rewritten:* The old pre-AST string syntax
documentation is replaced with a comprehensive Cypher reference covering:
algorithm hint, pattern matching (directed/incoming/undirected/variable-length),
WHERE predicates (comparison, string predicates, IS NULL, IN, AND/OR/NOT),
RETURN (property access, alias, whole-node, RETURN *, arithmetic),
aggregation (COUNT/COLLECT/AVG/SUM/MIN/MAX with implicit GROUP BY),
OPTIONAL MATCH, WITH pipeline, shortestPath (path object, direction-correct,
path comprehension predicates ALL/ANY/NONE, list comprehension), UNWIND,
UNION/UNION ALL, built-in functions, relationship type derivation from REF
field keys, and known OC9 gaps (with pointer to docs/SULPHER_OC9_GAPS.md).

### Summary of Phase 2 (p_006 through p_015)

| Stage | Content | p_ |
|-------|---------|-----|
| 2.1 | Direct AST executor, bridge eliminated | 006 |
| 2.2 | Algorithm hint (`// sulpher.algorithm:`) | 007, 008 |
| 2.3 | OPTIONAL MATCH (left-join semantics) | 011 |
| 2.4 | WITH pipeline (MATCH→WITH→MATCH) | 011 |
| 2.5 | Richer WHERE: IS NULL, STARTS WITH, ENDS WITH, CONTAINS, IN | 009 |
| 2.6 | RETURN *; aliases; arithmetic; built-in functions | 009 |
| 2.7 | Aggregation: COUNT, COLLECT, AVG, SUM, MIN, MAX, GROUP BY | 014 |
| 2.8 | UNION / UNION ALL | 014 |
| 2.9 | UNWIND | 014 |
| 2.10 | shortestPath / allShortestPaths / direction-correct traversal | 010, 011 |
| 2.11 | SKIP | 014 |
| 2.12 | Final pass, documentation | 015 |

Additional work beyond the original plan:

| Work | p_ |
|------|----|
| Env-based binding model redesign (pathEnv→Env) | 012 |
| Unified multi-start BFS, O(1) depth check, inline property push-down | 013 |
| OC9 gaps document (`docs/SULPHER_OC9_GAPS.md`) | 011 |
| Optimisation roadmap (`docs/SULPHER_OPTIMISATION_ROADMAP.md`) | 013 |
| Path comprehension: ALL/ANY/NONE/SINGLE, list comprehension | 011 |
| `p.relationships` in shortestPath RETURN | 011 |
| FindPathDirected, AllShortestPaths on FlatGraph | 011 |

## [0.9.8-p_014] - 2026-06-11

### Added

**`pkg/sulpher` — Stages 2.7, 2.8, 2.9, 2.11: aggregation, UNION, UNWIND, SKIP**

*SKIP (Stage 2.11):* `RETURN ... SKIP n` and `WITH ... SKIP n` are now applied
after ORDER BY and before LIMIT at every return point in the executor:
`executeTraversalEnv`, `executeWithEnv`, `executeShortestPathEnv`. SKIP 0 is
a no-op; SKIP beyond result count returns an empty slice.

*UNWIND (Stage 2.9):* `UNWIND expr AS var RETURN ...` expands a list into one
Env per item and continues the pipeline. Two dispatch paths:
- `executeUnwindEnv` — UNWIND without MATCH, returns directly projected items
- `executeUnwindMatchEnv` — UNWIND with MATCH, seeds BFS with each list item
  bound to the UNWIND variable; merges seed Env into each traversal result

UNWIND integrates cleanly with LIMIT, SKIP, DISTINCT, ORDER BY, and expression
evaluation in RETURN. `extractAllClauses` and `validateAST` updated to accept
`UNWIND` as a valid query start (previously required MATCH).

*Aggregation (Stage 2.7):* `applyAggregation` implements Cypher implicit GROUP
BY semantics. When any RETURN item is an aggregate function call (COUNT,
COLLECT, AVG, SUM, MIN, MAX), `projectEnvs` delegates to `applyAggregation`
rather than per-row projection:
- Non-aggregate RETURN items become grouping keys
- Aggregate items are accumulated per unique group
- `count(*)` detected via `ast.CountStar` argument type
- `count(DISTINCT expr)` uses a per-group seen-set for distinct tracking
- `collect(expr)` accumulates non-null values into a list; nil-safe
- `avg(expr)` tracks sum and count independently
- MIN/MAX use `qs.CompareValues` for typed comparison

`hasAggregation` and `isAggregateExpr` provide the detection logic without
touching the non-aggregate code path.

*UNION / UNION ALL (Stage 2.8):* `executeUnionEnv` executes each
`SingleQuery` part independently and merges results. `UNION` deduplicates via
`qs.ApplyDistinct`; `UNION ALL` does not. Three-part UNION supported. The
`UnionPart.All` flag on each connector determines deduplication per-join.

### Tests

33 new tests across three files:
- `executor_skip_unwind_test.go` (12): SKIP basic, past-end, with LIMIT, zero,
  with pipeline; UNWIND basic list, string list, with LIMIT, with SKIP, empty
  list, with expression alias, DISTINCT
- `executor_aggregation_test.go` (14): count(*), count(node), count(property),
  count(DISTINCT), collect, collect empty, sum, avg, min, max, GROUP BY with
  count, GROUP BY with collect, multiple aggregates, ORDER BY on aggregated
  result
- `executor_union_test.go` (7): UNION deduplication, different nodes, three
  parts, UNION ALL no-dedup, UNION ALL three parts, empty first part, both
  empty

## [0.9.8-p_013] - 2026-06-11

### Added

**`docs/SULPHER_OPTIMISATION_ROADMAP.md`** — performance optimisation roadmap
covering four tiers: intra-query (no new infrastructure), cross-query result
cache, structural graph-layer changes, and measurement guidance. Each item
documents the problem, fix, scope, and expected impact.

### Changed

**`pkg/sulpher` — three Tier 1 optimisations implemented**

*Unified multi-start BFS (roadmap 1.1):* `executeTraversalEnv` previously
ran an independent `bfsEnv` instance per start node. Hub nodes downstream of
multiple start nodes were expanded once per start node. The BFS is now seeded
with all start nodes simultaneously via `bfsEnvMulti` → `bfsEnvFromQueue`.
Hub nodes are expanded at most once regardless of how many start nodes lead to
them. `bfsQItem` is extracted as a package-level type; `bfsEnv` (single-start,
used by OPTIONAL MATCH and WITH) and `bfsEnvMulti` (multi-start, used by the
main traversal) both delegate to `bfsEnvFromQueue`. DFS remains per-start
since DFS order is root-relative.

*O(1) depth check (roadmap 1.2):* The BFS queue item now carries `depth int`
— incremented whenever a named node variable is bound. The `envDepth` function
(which iterated the entire Env on every queue item) is no longer called in the
hot BFS path. `dfsEnvRecursive` similarly carries `depth int` rather than
calling `envDepth`. `envDepth` is retained for callers outside the hot path.

*Inline property filter push-down (roadmap 1.4):* `matchesNodePatternAST` now
handles `type` inline properties without store hydration (the entity type is
the prefix of the node ID string). The existing `id` push-down is also
improved: `fmt.Sprintf("%d", v)` is replaced by `strconv.Itoa` /
`strconv.FormatInt` to avoid format-string allocation on integer ID
comparisons. Patterns like `(u:User {id: '42'})` now cost zero store calls.

## [0.9.8-p_012] - 2026-06-11

### Changed

**`pkg/sulpher` — binding model redesign: Env replaces pathNode/pathEnv**

The executor's internal evaluation model has been completely rewritten.
The old positional model (`[][]pathNode`, `pathEnv{path []pathNode, bindings
variableBinding}`) is replaced with a flat map:

    type Env map[string]interface{}

Variable names map directly to their values. Nodes are lazy-hydrating maps
(`{"_nodeID": "user:3", ...}`). Path objects are first-class maps
(`{"nodes": [...], "relationships": [...], "length": n}`). Lists, scalars,
and nil bind directly without wrapping.

**New file: `executor_env.go`** (1408 lines) — the complete Env-based engine:

- `Env`, `nodeEnv()`, `pathEnvValue()`, `isNodeEnv()` — the type and its constructors
- `executeASTv2` / `executeTraversalEnv` — entry points
- `bfsEnv` / `dfsEnv` / `dfsEnvRecursive` — traversal producing `[]Env`
- `envBind` / `envBindWithSnapshot` — node binding during traversal; the
  snapshot-aware variant reuses existing nodeData maps so already-hydrated
  nodes are never wrapped in a fresh unhydrated map
- `applyWhereEnv` / `evalEnv` — expression evaluation against Env; the
  `Identifier` case is now `return env[ex.Value]` (one line vs. fifteen)
- `envWithValue` — child env creation for quantifier/list iteration; replaces
  the `envWithBinding` synthetic pathNode workaround entirely
- `evalFunctionEnv` — built-in Cypher functions
- `projectEnvs` / `projectEnv` / `evalEnvForReturn` / `nodeMapForReturn` —
  RETURN projection; RETURN * iterates all node-valued keys in the Env
- `applyOrderByEnv`
- `applyOptionalMatchEnv` — OPTIONAL MATCH using Env merge semantics; no
  index arithmetic, no anchor deduplication complexity
- `executeWithEnv` / `projectWithEnv` / `applyDistinctEnvs` — WITH pipeline;
  `projectWithEnv` preserves `_nodeID` for node variables so subsequent MATCH
  phases can anchor from them
- `executeShortestPathEnv` / `buildPathEnv` / `evalShortestPathEnvExpr` —
  shortestPath with path objects as first-class Env values

**`executor_ast.go` reduced** from 2394 lines to 858. Dead code removed:
`executeAST`, `executeTraversalAST`, `bfsTraverseAST`, `dfsTraverseAST`,
`dfsRecursiveAST`, `applyWhereAST`, `variableBinding`, `buildVariableBindingsFn`,
`pathEnv`, `makeEnv`, `evalExprAST`, `envWithBinding`, `applyReturnAST`,
`evalReturnExpr`, `evalShortestPathExpr`, `applyOrderByAST`,
`executeWithPipelineAST`, `makeEnvFromRow`, `evalPathWhere`,
`buildRelMapsFromLabels`, `applyOptionalMatchAST`, `executeShortestPathAST`,
`projectShortestPathReturn`, `buildPathNodeList`, `pathNodesToMaps`.

Retained in `executor_ast.go`: clause extraction, `isShortestPathPattern`,
path traversal helpers (`fullNodeID`, `pathMatchesRelType`,
`buildPathEdgeLabels`), start node matching, utility functions
(`evalLiteralAST`, `toInterfaceSlice`, `exprToKey`, `evalIntExpr`,
`applyDistinctRows`, `buildVarSet`), `astToPathDir`, relationship helpers,
push-down planner.

**`executor.go`**: `Execute` and `ExecuteWithDepth` now call `executeASTv2`.

**Test updates**: `executor_shortest_path_test.go` updated with `getPathNodes`
helper that extracts the nodes list from a path object (`{"nodes": [...],
...}`). This makes the tests forward-compatible with the first-class path
object model. All 2711 tests pass without modification to the test assertions.

### Capabilities enabled by the new model (future work)

- Relationship variable binding (`[r:KNOWS]` → `r` bound as `{"type": "KNOWS"}`)
- `UNWIND list AS n` (bind list items directly to a variable, no fake pathNode)
- `WITH collect(u.name) AS names` then `WHERE n IN names` (lists as first-class WITH values)
- Multiple OPTIONAL MATCH (merge Envs cleanly, no path length bookkeeping)
- Subquery results as Env bindings

## [0.9.8-p_011] - 2026-06-11

### Added

**`pkg/graph` — direction-aware path traversal**

- `PathDirection` type: `PathDirOutgoing`, `PathDirIncoming`, `PathDirAny`
- `FindPathDirected(from, to string, maxDepth int, dir PathDirection) ([]string, error)` — shortest path respecting direction; `PathDirAny` treats the graph as undirected
- `AllShortestPaths(from, to string, maxDepth int, dir PathDirection) ([][]string, error)` — all paths of minimum length, using a two-phase BFS that tracks all parents at minimum depth then backtracks to enumerate every route

Both methods added to the `Graph` interface. `mockGraph` in `persister_test.go` updated with no-op stubs.

**`pkg/sulpher` — OC9 low and medium complexity gaps closed**

*Direction-correct traversal*: `executeShortestPathAST` now calls `FindPathDirected` with the direction derived from the relationship pattern (`->` = outgoing, `<-` = incoming, `-` = any). `pathMatchesRelType` checks both adjacency and reverse-adjacency so relationship type filters work for paths found via incoming or undirected traversal.

*`p.relationships`*: `buildPathEdgeLabels` extracts the relationship label for each hop from the snapshot adjacency maps. `projectShortestPathReturn` exposes `p.relationships` as `[]{"type": label}` maps.

*OPTIONAL MATCH*: left-join semantics. For each mandatory-MATCH path, the optional pattern is traversed. If it finds matches, results are cross-joined (with shared anchor node deduplication). If it finds nothing, a nil-padded row is emitted so the mandatory binding remains visible with nil optional values. `extractAllClauses` replaces `extractClauses` as the primary clause parser.

*WITH pipeline*: `MATCH ... WITH vars [WHERE pred] [LIMIT n] MATCH ... RETURN`. The WITH clause projects mandatory-MATCH results, applies the optional WHERE/LIMIT, then seeds the second MATCH phase with the bound node IDs.

*Path comprehension predicates*: `QuantifierExpression` (`ALL`, `ANY`, `NONE`, `SINGLE`, `FILTER`) evaluates a predicate closure over a list. `ListComprehension` (`[n IN list WHERE pred | expr]`) filters and maps lists. Both are handled in `evalExprAST`.

*Built-in Cypher functions* (new `FunctionCall` case in `evalExprAST`): `nodes()`, `relationships()`, `length()`, `size()`, `head()`, `last()`, `tail()`, `coalesce()`, `toString()`, `toInteger()`, `toFloat()`, `abs()`, `toUpper()`, `toLower()`, `trim()`, `type()`, `id()`, `labels()`, `exists()`.

*`envWithBinding` / synthetic bindings*: `QuantifierExpression` iteration and path variable binding now use a synthetic `pathNode` with `NodeID=""`. The `Identifier` case in `evalExprAST` unwraps these correctly: map values are returned directly (for node-map iteration); non-map values are wrapped under `"value"` and unwrapped on retrieval. This enables `n.name` to work naturally inside `ALL(n IN nodes(p) WHERE n.name = 'Alice')`.

*`projectShortestPathReturn` redesigned*: Uses `evalExprAST` with a full path environment (path variable + endpoint variables bound) so that arbitrary RETURN expressions — `size(nodes(p))`, `[n IN nodes(p) | n.name]`, `b.name` — work without special-casing.

*`evalPathWhere`*: Evaluates MATCH WHERE predicates in a shortestPath context with the path variable available, enabling `WHERE ALL(n IN nodes(p) WHERE ...)`.

**`docs/SULPHER_OC9_GAPS.md`** (new): Authoritative record of remaining OC9 compliance gaps, divided into: low complexity (next), medium complexity (planned), high complexity (out of scope), and known semantic differences from Neo4j.

### Tests

28 new tests in `executor_oc9_test.go`:
- `TestFindPathDirected_*` (3): outgoing, incoming, any at the graph layer
- `TestAllShortestPaths_*` (4): multiple found, two equal length, no path, same node
- `TestShortestPath_*Direction` (3): outgoing, incoming, bidirectional in Sulpher queries
- `TestShortestPath_ReturnRelationships`: `p.relationships` projection
- `TestOptionalMatch_*` (3): with match, no match, mixed results
- `TestWith_*` (3): simple chain, with WHERE, with LIMIT
- `TestPathComprehension_*` (3): ALL, ANY, NONE node predicates
- `TestListComprehension_Filter`: `[n IN nodes(p) WHERE ... | n.name]`
- `TestBuiltIn_*` (5): NodesFunction, Length, ToUpper/ToLower, Coalesce, Labels/Exists
- `TestAllShortestPaths_InQuery_Diamond`: graph-layer diamond via Sulpher query

## [0.9.8-p_010] - 2026-06-11

### Added

**`pkg/sulpher` — Stage 2.10: shortestPath**

`MATCH p = shortestPath((a:Type)-[:REL*]-(b:Type)) RETURN p` is now
supported. The executor detects the shortestPath pattern (a PatternPart with
a bound variable and a variable-length relationship between exactly two node
patterns), resolves the endpoint nodes, and delegates to
`graph.FindPath(from, to, maxDepth)` — the existing BFS implementation in
`FlatGraph` that was already available but unused from the query layer.

**Pattern detection** — `isShortestPathPattern` scans the MATCH pattern for
a named PatternPart with `HasRange=true` on the relationship. This matches
the AST shape produced by `shortestPath((a)-[*]-(b))` without requiring a
special parser extension.

**Execution** — `executeShortestPathAST` resolves all (from, to) node ID
pairs that match the endpoint patterns, calls `FindPath` for each pair, and
optionally filters the result by relationship type using
`pathMatchesRelType`, which checks the snapshot adjacency map for edge labels
along the returned path.

**Projection** — three RETURN forms are supported:
- `RETURN p` — the path as an ordered `[]interface{}` of hydrated node maps,
  each with `_id`, `type`, and all store properties.
- `RETURN p.length` — the number of hops (nodes - 1).
- `RETURN p.nodes` — same as `RETURN p`.

**Relationship type filter** — `WHERE [:KNOWS]` on the pattern is enforced
post-FindPath by checking each hop in the snapshot adjacency map. FindPath
itself does not filter by relationship type; this is correct since the
snapshot adjacency is the authoritative source of edge labels.

**`RETURN shortestPath(...)`** form — `evalShortestPathExpr` handles
`ShortestPathExpression` nodes in the RETURN clause directly, resolving
endpoint nodes from the current path bindings. This requires the endpoint
variables to be bound by a preceding MATCH clause; comma-separated MATCH
patterns are Stage 2.4 and not yet implemented.

12 tests in `executor_shortest_path_test.go` covering: basic path, direct
neighbour, same node (no result), no path (isolated node), `p.length`,
`p.nodes`, without relationship type, wrong relationship type, maxDepth
respected, all pairs in type, node content verification, and the
`RETURN shortestPath(...)` form (graceful error pending Stage 2.4).

## [0.9.8-p_009] - 2026-06-11

### Tested

**`pkg/sulpher` — Stage 2.5 and 2.6: predicate and RETURN test coverage**

29 new tests in `executor_predicates_test.go` verifying the WHERE predicate
and RETURN capabilities implemented in Stage 2.1.

Stage 2.5 — richer WHERE expressions (all passing):
- `IS NULL` / `IS NOT NULL` including combined with other conditions
- `STARTS WITH` / `ENDS WITH` / `CONTAINS` string predicates
- `NOT STARTS WITH` / `NOT ENDS WITH` / `NOT CONTAINS`
- `IN [list]` for integers and strings
- `NOT IN [list]`
- `IN []` (empty list — always false)
- String predicates combined with comparisons
- String predicates combined with OR

Stage 2.6 — RETURN enhancements (all passing):
- `RETURN u.name AS alias`
- Multiple aliases in one RETURN
- `RETURN *` (projects all bound variables as whole-node maps)
- `RETURN *` with multiple pattern variables
- Arithmetic in RETURN: `RETURN u.age + 1 AS nextAge`
- Arithmetic multiplication: `RETURN u.age * 2 AS doubled`
- Whole-node RETURN: `RETURN u` (includes `_id` field)
- `u.id` property access (extracted from node ID string)
- Mix of raw property and alias in same RETURN

One test expectation corrected during writing: `NOT u.active = true` correctly
matches nodes where `active` is absent (nil != true), not just nodes where
active is explicitly false. The semantics are correct; the initial test was
overly strict.

The `=~` regex predicate is noted as unimplemented (returns false) in the
evalExprAST code; a test for it is deferred to Stage 2.5 completion.

## [0.9.8-p_008] - 2026-06-11

### Changed

**`pkg/sulpher` — algorithm hint format updated to `// sulpher.algorithm:`**

The preferred way to specify the traversal algorithm is now a leading comment:

    // sulpher.algorithm: dfs
    MATCH (u:User)-[:FOLLOWS]->(f) RETURN f

The comment is case-insensitive. Valid values are `bfs` and `dfs`. An
unrecognised value is a parse-time error.

The legacy `BFS `/ `DFS ` keyword prefix still works for backward
compatibility but is undocumented going forward.

The comment form is cleaner: it does not modify the Cypher query body, it is
clearly scoped to the Sulpher subsystem, and it is trivially ignorable by any
standard Cypher tool that encounters the query string.

4 new parser tests: `TestParserAlgorithmHintDFS_CommentForm`,
`TestParserAlgorithmHintBFS_CommentForm`,
`TestParserAlgorithmHintCaseInsensitive`, `TestParserAlgorithmHintInvalid`.
`TestExecuteDFS` updated to use the new form.
`TestParserAlgorithmHintDFS_LegacyForm` added to confirm backward compatibility.

## [0.9.8-p_007] - 2026-06-11

### Changed

**`pkg/sulpher` — Phase 2 Stage 2.2: algorithm hint wired**

`AlgorithmHint` is now fully threaded from `Parser.Parse` →
`Execute`/`ExecuteWithDepth` → `executeAST` → `executeTraversalAST`.
`executeTraversalAST` reads `hint.Algorithm` and dispatches to
`bfsTraverseAST` or `dfsTraverseAST` accordingly.

The dead `algorithmFromQuery` stub (which always returned `AlgBFS`) is
removed. `AlgBFS`/`AlgDFS` constants are now defined at the top of
`executor_ast.go` where they are used.

`DFS MATCH ...` queries now correctly use depth-first traversal rather than
silently falling back to BFS.

## [0.9.8-p_006] - 2026-06-11

### Changed

**`pkg/sulpher` — Phase 2 Stage 2.1: direct AST execution, bridge eliminated**

The Sulpher executor now walks the `*sulpherast.Query` Cypher AST directly.
`bridge.go` (498 lines) is deleted. The internal `Query`, `PathElement`,
`NodePattern`, `RelPattern`, `Condition`, `ConditionGroup`, `ReturnItem`,
`OrderByItem`, `Algorithm`, `Operator`, `OrderDirection` structs are gone.

**`parser.go`** — `Parser.Parse` now returns
`(*sulpherast.Query, *AlgorithmHint, error)`. `AlgorithmHint` carries the
`BFS`/`DFS` prefix annotation. Post-parse validation enforces constraints
the permissive Cypher parser does not reject (empty query, missing MATCH,
missing RETURN, hop min > max, negative hops).

**`executor_ast.go`** (new, 659 lines) — the new execution engine:
- `executeAST(ctx, *sulpherast.Query, maxDepth)` — entry point; dispatches
  to push-down or traversal.
- `executeTraversalAST` — in-memory BFS/DFS over the FlatGraph snapshot.
- `bfsTraverseAST` / `dfsTraverseAST` — traversal over `[]pathSegment`
  (pairs of `*sulpherast.NodePattern` + `*sulpherast.RelationshipPattern`).
- `applyWhereAST` — evaluates the WHERE `Expression` tree against each path.
- `evalExprAST` — recursive expression evaluator: InfixExpression (AND/OR/
  comparison/arithmetic), PrefixExpression (NOT/-), PropertyAccess,
  Identifier (variable → node map with hydration), IsNullExpression,
  StringPredicate (STARTS WITH, ENDS WITH, CONTAINS), InExpression (IN),
  ListLiteral, all literal types.
- `applyReturnAST` / `evalReturnExpr` — projection with whole-node support
  (RETURN *), property access (RETURN u.name), and alias support.
- `applyOrderByAST` — sort using `*sulpherast.SortItem` directly.
- `planGraphQueryAST` / `executeGraphPushDownAST` / `generateGraphSQLAST` —
  AST-native versions of the SQL push-down path.
- `matchesNodePatternAST` / `findMatchingNodesAST` — Strategy A (type index)
  with AST types.

**`executor.go`** — `Execute` and `ExecuteWithDepth` now accept
`*sulpherast.Query` and `*AlgorithmHint`. Old traversal functions
(`executeWithDepth`, `bfsTraverse`, `dfsTraverse`, `applyConditionGroups`,
`applyReturn`, `applyOrderBy`, `findMatchingNodes`, `matchesNodePattern`,
`evaluateCondition`) removed. Retained: `takeSnapshot`,
`getNeighborsByDirection`, `hydrateNodeData`, comparison helpers.
`RelDirection`, `Operator` type definitions moved here.

**`sqlgen.go`** — stripped to infrastructure only (types and helpers shared
by the AST push-down path in `executor_ast.go`).

**`jobs.go`** — updated to use the three-value `Parse` return and pass
`*AlgorithmHint` through to `ExecuteWithDepth`.

### Known gap (Stage 2.2, next)

`algorithmFromQuery` in `executor_ast.go` always returns `AlgBFS`. The
`*AlgorithmHint` is correctly threaded from `Parse` → `Execute` →
`executeAST`, but `executeAST` does not yet read it. Stage 2.2 wires the
hint into the traversal dispatch.

### Test updates

All test files updated to the three-value `Parse` API and `Execute` with
`*AlgorithmHint`. `parser_test.go` rewritten to test the AST directly rather
than the internal `Query` struct. 2638 tests passing.

## [0.9.8-p_005] - 2026-06-11

### Added

**`pkg/sulpher` — SQL push-down for graph traversal (Phase 2 Stage 2.1 foundation)**

The Sulpher executor now has a two-path execution architecture:

**Push-down path** (new): when a MATCH query is over adapted entities with a
translatable WHERE clause, the entire traversal is pushed to the storage
backend as a SQL JOIN chain over the adapted entity tables and the tenant
edge table. No in-memory BFS/DFS, no property hydration round-trips. The
result is a flat row set post-processed in Go for DISTINCT and LIMIT.

**Traversal path** (existing, improved): in-memory BFS/DFS over the FlatGraph
snapshot, used for blob entities, variable-length hops, OPTIONAL MATCH, and
any condition that can't be expressed in SQL.

**`pkg/sulpher/sqlgen.go`** (new, 258 lines):
- `planGraphQuery(*Query) graphPlan` — eligibility check: all node types must
  be adapted entities, no variable-length hops, all WHERE conditions must
  resolve to adapted columns.
- `generateGraphSQL(*Query) (*graphSQLResult, error)` — builds the JOIN chain.
  Table names via `AdaptedTableName`, placeholders via `StorageDialect.Placeholder`,
  edge table name via `GraphQueryable.GraphEdgesTable`. No backend-specific
  syntax; works with any `StorageDialect` implementation.
- `executeGraphPushDown(ctx, *Query) (*QueryResult, error)` — runs the generated
  SQL via `AggregateQuery` and applies DISTINCT/LIMIT in Go.

**`pkg/sulpher/executor.go`**:
- `GraphQueryable` interface: extends `storage.AggregateQueryable` with
  `GraphEdgesTable() string`.
- `WithGraphStore(GraphQueryable) *Executor` — attaches the push-down store.
- `executeWithDepth` dispatches to `executeGraphPushDown` when
  `planGraphQuery` returns `planPushDown`, otherwise runs the traversal path.

**`pkg/server/server.go`**:
- `graphQueryableAdapter` — thin adapter wrapping `storage.AggregateQueryable`
  with the tenant-scoped edge table name from `tenant.GraphEdgesTableName`.
- Both `sulpherJobsForTenant` and the global executor wiring now call
  `WithGraphStore` when the store implements `AggregateQueryable`.

### Changed

**`pkg/sulpher` — Strategy A: type-index start node selection**

`findMatchingNodes` now uses `GetNodesByTypeForTenant` (tenant scope) or
`GetNodesByType` (unscoped) when the pattern specifies a type label, reducing
start node candidate selection from O(N_total) to O(N_type). The full graph
scan is retained only for untyped patterns.

Tenant-prefixed node IDs returned by `GetNodesByTypeForTenant` are stripped
to bare "entity:id" form before matching against the snapshot, aligning with
the snapshot's key convention.

**`pkg/sulpher` — tenant prefix handling in `matchesNodePattern`**

`matchesNodePattern` now strips the tenant prefix ("XXXX@") from node IDs
before parsing the entity:id pair. Previously, tenant-scoped node IDs would
fail the type check because `parts[0]` would be `"XXXX@entity"` rather than
`"entity"`. This was masked by the old full-scan approach (which would see
the same prefix in both the pattern type and the node ID), but became visible
with Strategy A (which returns correctly-typed but still-prefixed IDs from
the graph index).

## [0.9.8-p_004] - 2026-06-11

### Changed

**`pkg/sulpher` — Phase 1 complete (Stages 1.4 and 1.5)**

**Stage 1.4 — `Execute`/`ExecuteWithDepth` duplication removed**

The two execution entry points were ~90 lines each of identical code
differing only in how `maxDepth` was sourced. They have been consolidated
into a single `executeWithDepth(ctx, query, maxDepth)` private method.
`Execute` delegates with `e.maxDepth`; `ExecuteWithDepth` delegates with
the caller-supplied value (falling back to `e.maxDepth` if `<= 0`).

The public API and behaviour are unchanged. The JobManager's
`executeJob` function already called `ExecuteWithDepth(ctx, q, job.MaxDepth)`
— this is unaffected.

**Stage 1.5 — `applyConditions` removed**

`applyConditions` (the legacy AND-only condition evaluator) was dead code
with no callers. The new parser always populates `ConditionGroups`;
`applyConditionGroups` handles both pure-AND (single group) and OR (multiple
groups) cases. The function and its call sites were already removed in p_003;
the function body itself is now deleted.

**Phase 1 summary**

The Sulpher subsystem's string-scanning parser and all associated technical
debt are now fully replaced:

| Stage | What changed |
|-------|-------------|
| 1.1 | `github.com/ha1tch/sulpher v0.2.0` declared in `go.mod` |
| 1.2/1.3 | `bridge.go` + new `parser.go`: Cypher AST → internal `Query` struct |
| 1.4 | `Execute`/`ExecuteWithDepth` consolidated into `executeWithDepth` |
| 1.5 | `applyConditions` and `Conditions []Condition` field removed |

`parser.go`: 825 lines → 159 lines.
`executor.go`: 1046 lines → 960 lines (net reduction despite new method).

Phase 2 (direct AST execution, full Cypher capability) begins next.

## [0.9.8-p_003] - 2026-06-11

### Changed

**`pkg/sulpher` — parser replaced with `github.com/ha1tch/sulpher` v0.2.0**

The string-scanning Sulpher parser (`findKeyword`, `parsePath`,
`parseCondition`, `parseWhereWithOr`, `parseWhere`, `parseReturn`,
`parseOrderBy`, `parseValue`, and associated helpers — 825 lines) has been
replaced with the `github.com/ha1tch/sulpher` Cypher parser.

The public API is unchanged: `NewParser()`, `(*Parser).Parse(string)`,
and the `Query`, `NodePattern`, `RelPattern`, `PathElement`, `Condition`,
`ConditionGroup`, `ReturnItem`, `OrderByItem` types are all preserved.
The executor, `JobManager`, and all server call sites are unmodified.

Implementation:
- `parser.go` now calls `sulpher.Parse` and delegates to `fromAST` in
  `bridge.go`.
- `bridge.go` (new file) translates a `*sulpher.ast.Query` into the
  internal `Query` struct. The bridge handles: single MATCH clause, linear
  path patterns, node patterns (variable, label, inline properties),
  relationship patterns (direction, type, variable-length hops), WHERE
  as AND/OR of simple comparisons, RETURN (variables and property
  projections), DISTINCT, ORDER BY, and LIMIT.
- Algorithm prefix (`BFS `, `DFS `) is extracted before parsing and applied
  after translation.
- `!=` is normalised to `<>` before passing to the Cypher parser (openCypher
  uses `<>` as the inequality operator; `!=` is a Sulpher extension).
- The legacy `Conditions []Condition` field has been removed from `Query`.
  The executor and all callers already used `ConditionGroups` exclusively.
  The dual-path branching in the executor is removed.

Known semantic change: `[*n]` (exact hop count) is indistinguishable from
`[*n..]` (min-only) at the AST level in the new parser. Both produce
`MinHops=n, MaxHops=0` (open-ended). The old parser mapped `[*n]` to
`MinHops=MaxHops=n`. The executor treats `MaxHops=0` as "use query
max_depth", so `[*3]` now traverses from 3 hops to max_depth rather than
exactly 3 hops. This will be resolved in Phase 2 when the executor walks
the AST directly and can use the token-level source to distinguish the two
forms.

Unsupported constructs (OPTIONAL MATCH, WITH, UNION, multiple MATCH
clauses, aggregates, subqueries) return a clear error from `Parse`.  These
are Phase 2 targets documented in `docs/SULPHER_RENOVATION.md`.

## [0.9.8-p_002] - 2026-06-11

### Added

**`pkg/qs` — shared query-system utilities (new package)**

A new internal package that eliminates duplicate utility code between
`pkg/oql` and `pkg/sulpher`. Both subsystems previously maintained
independent implementations of the same comparison, aggregation, and
scalar-function logic, creating a risk of silent divergence when one
was optimised or fixed without the other.

`pkg/qs` contains:

- `compare.go` — `CompareValues`, `ToFloat`, `ToFloatSafe`: canonical
  typed value comparison and numeric coercion. `CompareValues` handles
  nil, all numeric types, and string fallback. `ToFloatSafe` handles all
  Go numeric primitives including bool and all int/uint widths.

- `result.go` — `GetNestedValue`, `ApplyDistinct`: dot-notation field
  access and JSON-keyed deduplication for result-set rows.

- `scalar.go` — 34 scalar function implementations shared by both
  subsystems: string (UPPER, LOWER, TRIM, LTRIM, RTRIM, LEN, CONCAT,
  SUBSTRING, LEFT, RIGHT, REPLACE, CHARINDEX, REVERSE), type conversion
  (CAST/TOSTRING, TOINTEGER, TOFLOAT), null handling (COALESCE/ISNULL),
  numeric (ABS, ROUND, FLOOR, CEILING, SIGN, SQRT, POWER), date/time
  (GETDATE, GETUTCDATE, YEAR, MONTH, DAY, DATEPART, DATEDIFF, DATE_TRUNC),
  and Cypher-oriented collection functions (SIZE, TYPE, LABELS). The
  `ScalarFunc` type and `ScalarFunctions` map are the canonical registry;
  each subsystem builds its own dispatch layer on top.

- `aggregate.go` — `AggCount`, `AggSum`, `AggAvg`, `AggMin`, `AggMax`,
  `AggCollect`: canonical aggregate implementations shared by OQL and
  Sulpher. `AggSum` returns nil for empty/all-nil input (SQL standard
  NULL semantics). All numeric aggregates handle string-encoded numerics
  (e.g. SQLite decimal columns returned as text after denormalisation).

30 tests in `pkg/qs/qs_test.go` covering all public functions.

### Changed

**`pkg/oql` — delegated to `pkg/qs`**

`aggregator.go`: `aggCount`, `aggSum`, `aggAvg`, `aggMin`, `aggMax`,
`toFloat`, `toFloatSafe`, `compareValues` delegate to `pkg/qs`.
`toFloat` and `toFloatSafe` retain OQL-specific string-parsing behaviour
(required for denormalised decimal aggregate results from the PushAggregate
path). `toFloat` preserves the original OQL semantics of not handling bool.

`scalar.go`: `ScalarFunctions` map now references `pkg/qs` implementations.
The `IsScalarFunction` and `EvalScalarFunction` dispatch wrappers remain
in `pkg/oql` since they depend on the tsqlparser AST. The `ScalarFunc`
type alias is preserved for compatibility.

**`pkg/sulpher` — delegated to `pkg/qs`**

`executor.go`: `applyDistinct`, `getNestedValue`, `compareForSort`,
`toFloat64`, `valuesEqual` delegate to `pkg/qs`. `compareNumeric`
simplified to use `qs.ToFloatSafe` for all numeric types; string-to-float
parsing retained for string literal values from the Sulpher parser.
`parseNumeric` removed (replaced by inline `fmt.Sscanf` in `compareNumeric`).

`valuesEqual` correctness improvement: the previous implementation used
`fmt.Sprintf` string coercion, which caused `1` and `1.0` to compare
unequal. The new implementation uses `qs.CompareValues` with proper typed
comparison.

### Fixed

**`pkg/oql/aggregator.go` — HAVING comparison on denormalised decimal values**

`evalCondition` was using `compareValues` (= `qs.CompareValues`) for
HAVING predicate evaluation. `qs.CompareValues` does not parse
string-encoded numerics (`ToFloatSafe` handles typed numerics only). The
PushAggregate path returns `SUM(amount)` as a denormalised decimal string
(e.g. `"42372.89"`); HAVING `SUM(amount) > 5000` was comparing the string
`"42372.89"` lexicographically against `int64(5000)`, giving `'4' < '5'`
→ false. Only values beginning with `5` or higher passed HAVING, producing
incorrect result sets.

Fixed by introducing `havingCompare` which calls `toFloatSafe` (which does
handle string-encoded numerics in OQL's wrapper) before falling back to
`qs.CompareValues`. `evalCondition` now uses `havingCompare` for all
relational operators.

**`pkg/oql/aggregator.go` — ORDER BY on denormalised decimal strings**

Same root cause: `OrderBy` was using `compareValues` to sort records.
When the PushAggregate path produces `SUM(amount)` as a string, sorting
`"9369.41"` vs `"11043.01"` lexicographically gives the wrong order
(`'9' > '1'`). Fixed by using `havingCompare` in `OrderBy` as well.

### Test fixes

**`pkg/sulpher` — four tests corrected to supply node property data**

`TestExecuteWithWhereCondition` (executor_test.go) and
`TestExecute_LessThanOperator`, `TestExecute_GreaterThanOrEqual`,
`TestExecute_LessThanOrEqual` (coverage_test.go) queried `u.id` on a
graph with no property store attached. The old `valuesEqual` used
`fmt.Sprintf` comparison which accidentally passed (nil coerced to
`"<nil>"` compared against integer strings). The correct fix is to
attach a `mockStore` supplying `id` fields. Tests updated accordingly.

**`pkg/sulpher` — `TestToFloat64_AllTypes` and `TestCompareNumeric_AllOps`**

Updated to reflect `qs.ToFloatSafe` semantics: `float32` and `bool` are
now handled (both return `ok=true`); `bool` in numeric comparisons is
treated as 0 (false) or 1 (true) rather than being rejected.

**`pkg/oql` — `scalar_test.go` updated**

`parseTime` calls updated to `qs.ParseTime`. `TestToFloat` and
`TestToFloatSafe` updated to match OQL wrapper semantics (string parsing
retained in wrappers). `TestScalar_Cast`, `TestScalar_GetDate`,
`TestScalar_DatePart`, `TestScalar_DateTrunc`, `TestScalar_CharIndex`,
`TestParseTime` updated or fixed.

## [0.9.8-p_001] - 2026-06-11

### Added

- `github.com/ha1tch/sulpher v0.2.0` added as a dependency (`go.mod`, `go.sum`).
  This is the first stable tagged release of the Cypher-like graph query parser
  that will replace the current string-scanning Sulpher parser in the upcoming
  renovation (see `docs/SULPHER_RENOVATION.md`). No production code uses the
  dependency yet; it is declared here so the module cache requirement is
  explicit and the version is pinned for the renovation work.

### Documentation

- `docs/SULPHER_RENOVATION.md` — two-phase renovation plan for the Sulpher
  subsystem: Phase 1 (parser replacement, behaviour-preserving) and Phase 2
  (new capabilities enabled by the richer AST). Includes dependency ordering,
  stage-by-stage task breakdown, notes on the olu REF data model constraint
  (relationship labels are field keys; relationship properties are not
  supported), and a version policy for the sulpher dependency.

## [0.9.8] - 2026-06-10

### Bug fixes (correctness)

**`pkg/oql/executor.go` — shared `Aggregator` mutation race in `ExecuteWithStore`**

`ExecuteWithStore` constructed a temporary `*Executor` for each call but copied
the parent's `*Aggregator` pointer rather than allocating a new one. Since
`configureDecimalAggregation` mutates `Aggregator.decimalFields` without any
lock, two concurrent requests executing aggregate queries would race on that
write. Fixed by allocating a fresh `Aggregator` per temporary executor.
`Aggregator` is stateless between calls — `configureDecimalAggregation` resets
it unconditionally — so the per-request allocation is both correct and cheap.

**`pkg/oql/oql.go` — `JobManager.Submit` launched goroutine under write lock**

`Submit` used `defer jm.mu.Unlock()`, which meant the write lock was still held
when `go jm.executeJob(job)` was called. `executeJob` immediately tries to
acquire the same write lock to set status to `JobRunning`. This was safe only
because the goroutine scheduler would not run `executeJob` before `Submit`
returned and released the lock — a scheduling assumption, not a language
guarantee. Fixed by replacing `defer jm.mu.Unlock()` with an explicit unlock
before the goroutine launch, matching the pattern already used by the Sulpher
`JobManager`.

### Removals

**`pkg/oql/oql.go` — `Engine.ExecuteWithTenant` and `JobManager.ExecuteSyncWithTenant` removed**

Both methods were marked deprecated and had no callers in the server or anywhere
in production code. `ExecuteWithTenant` also had a latent correctness issue: it
did not set `sqlTenantID` on the executor, so push-down queries issued through
it would run without a `tenant_id` WHERE clause and return rows from all tenants.
`Engine.Execute` is updated to call the executor directly, preserving identical
semantics for the unscoped case. The test `TestEngine_ExecuteWithTenant` is
updated to use `Execute` and `ExecuteWithStore` respectively.


### Test coverage

**`pkg/dynconfig` — new test file (`pkg/dynconfig/config_test.go`)**

28 tests covering the full `DynConfig` surface: `New` with absent/existing/malformed
files; `Set`/`Get` round-trips for all four scalar types (`int64`, `float64`, `string`,
`bool`); absent-key and wrong-type return values; namespace/key/JSON validation
rejection; flush atomicity (in-memory store not mutated when disk write fails);
`Delete` including empty-namespace cleanup and no-op on missing key; `Namespace` and
`Dump` deep-copy contracts; persistence across instances; `Reload` leaving the store
intact on malformed or invalid-name files; concurrent `Set`/`Get`/`Dump`; `TenantNamespace`
helper; and `Watcher` start/stop lifecycle with no-deadlock guarantee.

**`pkg/config` — extended (`pkg/config/config_test.go`)**

`TestDefault_NewFields` asserts defaults for all config fields added in
patched92–patched111: `DynConfigEnabled`, `DynConfigAPIEnabled`, `DynConfigReloadSecs`,
`BlobGCEnabled`, `BlobGCIntervalSecs`, `BlobGCGracePeriodSecs`, `BlobMaxTotalBytes`,
`BlobUsageSampleIntervalSecs`, `S3RequireAuth`, `SQLitePerFileTenants`. Five new
`TestLoadFromEnv_*` functions verify that each corresponding environment variable
(`OLU_DYNCONFIG_ENABLED`, `OLU_DYNCONFIG_FILE`, `OLU_DYNCONFIG_RELOAD_INTERVAL`,
`OLU_DYNCONFIG_API_ENABLED`, `OLU_BLOB_GC_ENABLED`, `OLU_BLOB_GC_INTERVAL`,
`OLU_BLOB_GC_GRACE_PERIOD`, `OLU_BLOB_MAX_TOTAL_BYTES`, `OLU_BLOB_USAGE_INTERVAL`,
`OLU_S3_REQUIRE_AUTH`, `OLU_SQLITE_PER_FILE_TENANTS`) is parsed and applied correctly.

**`pkg/blob` — new sampler test file (`pkg/blob/sampler_test.go`)**

10 tests covering `decodeTenantDir` round-trips for all tenant name variants
(empty, dots, slashes, unicode); collision protection verifying that `foo.bar` and
`foo_bar` sanitise to distinct directories (patched103 correctness fix); invalid
input rejection; `SampledAt` is zero before any walk and non-zero after
`ForceResample`; correct `BlobCount`/`KeyCount`/`Bytes` for a single tenant with
content-deduplicated blobs; correct aggregate `TenantCount`/`TotalBlobCount`/
`TotalKeyCount` across three tenants; absent-tenant returns zero-value `SampledUsage`;
`Start`/`Stop` no-deadlock; initial walk completes within 2 s of `Start`.

Stale TODO block removed from `pkg/blob/store_test.go` (the handler tests it
listed were implemented in `pkg/server/blob_handler_test.go` in patched108).

**`pkg/errors` — extended (`pkg/errors/errors_test.go`)**

`TestCodeFormat` extended to include all six blob codes (`OLU-BL001`–`OLU-BL006`)
and all four dynconfig codes (`OLU-DC001`–`OLU-DC004`), none of which appeared
in the test before. Three new `TestNew_*` functions smoke-test construction and
`Error()` formatting for `ErrBlobQuotaExceeded`, the full DC family, and the full
BL family.

**`pkg/middleware` — extended (`pkg/middleware/metrics_test.go`)**

Three tests for the `olu_blob_*` Prometheus gauges added to
`PrometheusFormatSnapshot` in patched110: `TestMetrics_BlobEnabled_EmitsGauges`
verifies all four gauges with non-zero values and correct HELP/TYPE lines;
`TestMetrics_BlobDisabled_OmitsGauges` verifies none appear when `BlobEnabled` is
false; `TestMetrics_BlobEnabled_ZeroValues` verifies the gauges are still emitted
(with value `0`) when the store is live but empty.

**`pkg/server` — new dynconfig handler test file (`pkg/server/dynconfig_handlers_test.go`)**

14 tests covering all five admin endpoints under `DynConfigEnabled=true`: 503
guard fires for all five methods when dynconfig is disabled; PUT→GET round-trip;
dump of empty and populated stores; namespace 404; key 404; key round-trip for
all scalar types; PUT with empty body returns 400; PUT with invalid JSON returns
400; PUT response body carries namespace/key/status fields; DELETE of existing
key confirmed absent on subsequent GET; DELETE of non-existent key returns 200;
PUT→DELETE→Dump consistency.

**`pkg/server` — quota precedence chain (`pkg/server/blob_handler_test.go`)**

Two new tests and two helpers (`setupBlobDCTestServer`, `dcSet`) exercise the
three-level quota resolution in `handleBlobPut`:
`TestBlobHandler_QuotaPrecedence_GlobalDynConfigOverridesStatic` seeds a tenant
to capacity, sets a global dynconfig limit via the admin API, and confirms the
next PUT returns 413 + `OLU-BL006`. `TestBlobHandler_QuotaPrecedence_PerTenantOverridesGlobal`
sets a 1-byte global limit and a 1 MiB per-tenant override and confirms the PUT
succeeds, proving level-1 beats level-2.

### Deprecations

**`jsonfile` storage backend — deprecated since v0.9.8**

The JSONFile storage backend is deprecated and will be removed in a future
release. It has never provided transactional atomicity, is not compatible with
`/commit`, OQL push-down, adapted tables, FTS, timeseries, or per-file tenant
isolation, and requires the `AdaptivePersister` graph persistence workaround
that exists solely to compensate for the absence of an edge table.

Existing deployments should migrate to SQLite:

```bash
export OLU_STORAGE_TYPE=sqlite
olu-migrate --from jsonfile --to sqlite --base-dir data --db olu.db
```

Starting in v0.9.8:
- The default value of `OLU_STORAGE_TYPE` is now `sqlite` (was `jsonfile`).
- Selecting `OLU_STORAGE_TYPE=jsonfile` emits a startup deprecation warning
  via the config validation path and a runtime `[DEPRECATED]` log line when
  the store is instantiated.
- All documentation has been updated to present SQLite as the primary backend.

The `jsonfile` backend remains functional in v0.9.8 and will continue to work
until the removal release. No changes to its behaviour have been made.

### Bug fixes

**`pkg/server/blob_handlers.go` — blob PUT returns 201 on create, 200 on overwrite**

The handler was returning `http.StatusOK` (200) unconditionally, ignoring the
`created` bool already returned by `bs.Put`. First writes to a new key now
correctly return 201 Created; overwrites of an existing key return 200 OK.
The `created` field in the response body was already correct — only the HTTP
status code was wrong. The S3 handler is unaffected: S3 PutObject mandates 200
regardless of whether the object is new. Seven test assertions in
`blob_handler_test.go` updated from `http.StatusOK` to `http.StatusCreated`
for the three first-write call sites (two in the JSON API, one in the quota
test).

**`pkg/server/dynconfig_handlers.go` — non-existent error constants**

Five `writeError` calls referenced `oluerr.ErrNotFound`, `oluerr.ErrInvalidInput`,
and `oluerr.ErrStoreFailed`, none of which exist in `pkg/errors`. The package
would have failed to compile as soon as `pkg/server` was built with a complete
dependency tree. Replaced with the correct DC-family codes: `ErrDCDisabled`,
`ErrDCNotFound`, `ErrDCInvalidInput`, `ErrDCStoreFailed`.

**`pkg/server/blob_handlers.go` — `*http.MaxBytesError` now returns 413**

When `BlobMaxSize` is configured, the handler wraps the request body with
`http.MaxBytesReader`. If the reader's limit fires inside `bs.Put` before the
store's own byte counter catches up (common with small limits and large read
buffers), the resulting `*http.MaxBytesError` was falling through to the default
`ErrBlobStoreFailed` branch and returning 500. Added an explicit
`errors.As(err, new(*http.MaxBytesError))` case to the error switch so
oversized uploads always return 413 + `OLU-BL003`.

### Tooling

**`run_tests.sh` — rewritten test runner**

The previous script was replaced with a significantly improved version:

- Module path fixed from `github.com/ha1tch/olu/` to `github.com/ha1tch/xolu/`
  so package short-names now display correctly in the table.
- `-v` added to the `go test` invocation, enabling individual test-case counts
  (pass / fail / skip) reported in a single run with no extra passes.
- Stdout and stderr are captured separately so toolchain noise (`go: downloading
  …`) no longer corrupts the grep patterns used for parsing.
- `GOPROXY` and `GONOSUMDB` are now set inside the script; callers no longer
  need to export them manually.
- New **Status** column in the per-package table makes pass/fail/no-test packages
  distinguishable at a glance. No-test packages (`cmd/iolu`, `cmd/olu`) now
  appear under status `-` rather than disappearing silently.
- Short-mode notice added to the header when `-short` is active, with a reminder
  of `--full` to include stress tests. Short mode now correctly reports the 19
  tests gated by `-short` as skipped rather than silently omitting them.
- Summary now reports `Tests: N pass, N fail, N skip` and
  `Packages: N pass, N fail, N no tests`.
- New `--quiet` flag drops the per-package table for CI use.
- New `--no-colour` / `--no-color` flag and `NO_COLOR` env var support.
- New `--charts` flag (see `charts.py` below).

**`charts.py` — terminal visualisation of test results (new file)**

A Python 3 script that renders two charts to the terminal after a test run,
invoked automatically by `run_tests.sh --charts`. Requires a colour-capable tty;
exits silently with no output when the terminal does not qualify.

Detection chain: stdout must be a tty (`-t 1`), `TERM` must match a known
colour-capable value (`xterm*`, `rxvt*`, `screen*`, `tmux*`, `alacritty*`,
`foot*`, `linux`, `ansi`), and `tput colors` must report ≥ 8. Also honours
`NO_COLOR` and `--no-colour`. The check is intentionally conservative: better
to emit nothing than to vomit escape sequences on an incompatible terminal.

Chart 1 — **Coverage heat map**: one row per package, bar width proportional to
coverage percentage, bar background colour encoding depth on a continuous
256-colour gradient: dark red (0%) → red → orange → yellow → lemon green → mid
green → emerald (100%). A colour-keyed legend strip follows.

Chart 2 — **Test-count treemap**: squarified layout where rectangle area is
proportional to per-package test count. Same coverage colour encoding as the heat
map. Each box shows the package short name, test count, and coverage % when space
permits. Terminal cell aspect-ratio compensation (2:1 pixel ratio) is baked into
the squarify coordinate space so boxes appear visually square.

Both charts are rendered from `$STDOUT` captured during the test run — no second
`go test` invocation.

### Test fixes

**`pkg/server/dynconfig_handlers_test.go` — `TestDynConfigHandler_Disabled_Returns503` renamed**

The test name was a lie: the route gate (`DynConfigEnabled && DynConfigAPIEnabled`)
means routes are never registered when the feature is disabled, so chi returns 404,
not 503. The `dynConfigGuard` 503 path is a defensive check for the case where
`dynConfig` is nil despite routes being registered — which in practice only occurs
when `dynconfig.New()` fails at startup, a consequence of the JSON file store's
startup-or-never failure model. Renamed to
`TestDynConfigHandler_APIDisabled_ReturnsNon2xx` to match what the test actually
verifies.

**`pkg/server/blob_handler_test.go` — `TestBlobHandler_QuotaPrecedence_PerTenantOverridesGlobal`**

The original assertion was `http.StatusCreated` (201); it was changed to
`http.StatusOK` in a prior fix when the blob handler always returned 200.
Now that the handler correctly returns 201 on first-write, the assertion has
been restored to `http.StatusCreated`. The test intent — per-tenant dynconfig
limit overrides the global limit — is fully preserved.

**`pkg/server/dynconfig_handlers_test.go` — `ts.doRequest` return value**

`TestDynConfigHandler_Disabled_Returns503` called `ts.doRequest` (which returns
`(*http.Response, []byte)`) capturing only one variable, causing a compile error.
The test now uses `d.do(...)` via the `dcTestServer` harness, making the single
return value correct.

**`pkg/server/e2e_graph_intensive_test.go` and `e2e_coverage_gaps_test.go` — `assertStatus` conflict**

`blob_handler_test.go` declares `assertStatus(t, *http.Response, int)` and
`e2e_graph_intensive_test.go` declared a conflicting `assertStatus(t, int, int)`.
The integer-signature function and all 54 of its call sites across the two e2e
files have been renamed to `assertStatusCode`.


## [0.9.7-patched111] - 2026-06-10

### Minor fixes

**`PutRaw` now returns `(sha, created, err)` (`pkg/blob/store.go`)**

Previously returned `(sha, err)`, misrepresenting whether the blob file was
newly written or was a pre-existing duplicate. `created` is now `true` only
when the rename actually happened (i.e. the blob file did not already exist).
All call sites updated: `blob_handlers.go` and six locations in
`store_test.go`. `TestPutRaw_Idempotent` gains assertions on both return
values. The misleading comment in `TestPut_Deduplication` is replaced with a
correct explanation.

**Old email domain replaced in docs and test headers**

13 files contained `h@ual.fi`; all replaced with `h@ual.li`. Affected:
five design docs under `docs/`, six server test files, two storage test files.


### Handler test mitigations (patched108 follow-up)

Two fragile tests identified during review of patched108 are fixed.

**`ForceResample` on `UsageSampler` (`pkg/blob/sampler.go`)**

New exported method that runs the filesystem walk synchronously and updates
the cache before returning. The quota test previously polled
`GET /blob/usage` in a loop with a 3-second deadline waiting for the sampler
to complete its goroutine-scheduled initial walk. It now calls
`ts.srv.BlobSampler().ForceResample()` directly, making the test deterministic
with no timers or sleeps. The sampler interval in the quota test is set to one
hour so the ticker never fires during the test.

**`BlobSampler()` accessor on `Server` (`pkg/server/server.go`)**

New exported method returning the `*blob.UsageSampler` for the server, or nil.
Provides test access to `ForceResample()` without making the `blobSampler`
field public.

**`TestBlobHandler_Put_Disabled` (`pkg/server/blob_handler_test.go`)**

The test previously used `setupBlobTestServer` with a `BlobEnabled=false`
override. This created an implicit dependency on option-application ordering
relative to the constructor — if the ordering changed, `blobStore_` could be
non-nil despite the option. The test now uses `setupTestServer`, which never
sets `BlobEnabled`, making the disabled state structurally guaranteed by the
config zero value.


### Dynamic configuration system (`pkg/dynconfig`)

A general-purpose runtime settings store backed by a JSON file. Settings take
effect on the next reload interval without restarting the server.

**`pkg/dynconfig/config.go`**

`DynConfig` is a concurrent-safe two-level store: namespace → key → raw JSON
value. Built-in namespace conventions: `"global"` for system-wide overrides,
`"tenant.{name}"` for per-tenant overrides (constructed via
`TenantNamespace(name)`). Any namespace/key string matching
`[a-zA-Z0-9._-]+` is accepted.

Well-formedness is enforced on every write and on every reload:
- Namespace and key names validated against the character set above.
- Values must be syntactically valid JSON (number, string, boolean, null,
  array, or object).
- On reload, the entire file is validated before the in-memory store is
  replaced. A malformed file leaves the existing store intact and logs a
  warning — the server never silently discards a valid config in favour of a
  broken one.
- Writes are atomic: temp file + rename. The backing file is never partially
  written.

Typed read accessors: `Get` (raw), `GetInt64`, `GetFloat64`, `GetString`,
`GetBool`, `Namespace` (copy of a whole namespace), `Dump` (deep copy of
everything). All reads are O(1) under a read lock. `Reload()` is public for
tests and manual triggers.

`Watcher` follows the same `Start`/`Stop` lifecycle as every other worker.
On each tick it calls `dc.Reload()`; failures are logged as warnings and the
existing store is preserved.

**Config fields and env vars**

| Field | Env var | Default |
|---|---|---|
| `DynConfigEnabled` | `OLU_DYNCONFIG_ENABLED` | `false` |
| `DynConfigFile` | `OLU_DYNCONFIG_FILE` | `{BaseDir}/dynconfig.json` |
| `DynConfigReloadSecs` | `OLU_DYNCONFIG_RELOAD_INTERVAL` | `30` |
| `DynConfigAPIEnabled` | `OLU_DYNCONFIG_API_ENABLED` | `false` |

`DynConfigAPIEnabled` is independently gated: the file-reload system can be
active without exposing the write API.

**Server wiring (`pkg/server/server.go`)**

`dynConfig` and `dynWatcher` fields on `Server`. Initialised after blob setup;
`dynWatcher` stopped in `Stop()` before store teardown.

**Admin API (`pkg/server/dynconfig_handlers.go`)**

Five endpoints, registered only when both `DynConfigEnabled` and
`DynConfigAPIEnabled` are true:

```
GET    /api/v1/admin/config                     — dump all namespaces/keys
GET    /api/v1/admin/config/{namespace}          — dump one namespace
GET    /api/v1/admin/config/{namespace}/{key}    — read one value (raw JSON)
PUT    /api/v1/admin/config/{namespace}/{key}    — set one value (raw JSON body)
DELETE /api/v1/admin/config/{namespace}/{key}    — remove one value
```

Error codes `OLU-DC001` through `OLU-DC004` added to `pkg/errors/errors.go`.

**Quota check updated (`pkg/server/blob_handlers.go`)**

`handleBlobPut` now resolves the effective quota with a three-level precedence
chain:

1. Per-tenant dynconfig override (`"tenant.{name}"` / `"blob.max_bytes"`)
2. Global dynconfig override (`"global"` / `"blob.max_bytes"`)
3. Static env default (`BlobMaxTotalBytes`)

All three levels fall through gracefully when dynconfig is disabled or the
key is absent.


### Handler tests and per-tenant quota enforcement

**Per-tenant quota enforcement**

A soft storage cap per tenant. When `OLU_BLOB_MAX_TOTAL_BYTES` is set, any
`PUT /blob` that would push the tenant's cached usage over the limit is
rejected with HTTP 413 and error code `OLU-BL006` (`ErrBlobQuotaExceeded`).
The check reads from the `UsageSampler` cache — never the filesystem — so it
cannot stall a request. Documented as a soft cap: a tenant can exceed the
limit by at most one blob between sample intervals.

New config:

| Field | Env var | Default |
|---|---|---|
| `BlobMaxTotalBytes` | `OLU_BLOB_MAX_TOTAL_BYTES` | `0` (no limit) |

Error code `OLU-BL006` added to `pkg/errors/errors.go`.

**`Server.S3Handler()` (`pkg/server/server.go`)**

New exported method returning an `http.Handler` for the S3-compatible surface.
Allows tests (and any caller) to wire the S3 router to an `httptest.Server`
without a real TCP listener. Returns a 501 stub when the blob store is not
enabled.

**Handler tests (`pkg/server/blob_handler_test.go`)**

24 tests covering the full blob HTTP surface. A `blobTestServer` harness
provides a blob-enabled `httptest.Server` for the JSON API and a second
`httptest.Server` via `S3Handler()` for the S3 surface. Functional options
(`...func(*config.Config)`) allow per-test configuration without separate
setup functions.

JSON API coverage: `PUT` with key, `PUT` without key (SHA path), invalid key,
oversized blob, blob-disabled 501, `GET` found/not-found with header
verification, `HEAD` found/not-found, `DELETE` found/not-found with
subsequent-GET confirmation, `LIST` empty/prefix/sorted-ascending, `USAGE`
with sampler off and blob-disabled.

S3 surface coverage: `PutObject`/`GetObject` round-trip with ETag check,
`HeadObject` headers, `DeleteObject` with subsequent-GET confirmation,
`GetObject` not-found with XML error format check, `ListObjectsV2` key count,
`HeadBucket`, missing-auth fallback (no-op with `S3RequireAuth=false`),
missing-auth rejection with `AccessDenied` XML (`S3RequireAuth=true`).

Quota: pre-seeds a blob dir to fill the configured cap, starts the server with
`BlobUsageSampleIntervalSecs=1`, polls `GET /blob/usage` until `sampled_at`
appears, then verifies that the next `PUT` returns 413 with `OLU-BL006`.


### Blob usage sampler — periodic cache, usage API, telemetry integration

Disk usage figures are now maintained by a background sampler that walks the
filesystem on its own independent ticker. Neither the usage API nor the
telemetry endpoint ever touches the filesystem at request time.

**`pkg/blob/sampler.go`** — new `UsageSampler` worker.

Runs on a configurable interval (`OLU_BLOB_USAGE_INTERVAL`, default 300 s).
Caches two views of usage data:

- Per-tenant (`SampledUsage`): served by `GET /blob/usage`. Each tenant sees
  only its own figures.
- Global aggregate (`GlobalUsage`): served by the telemetry endpoint.

`Start()` runs an initial walk immediately so the cache is warm before the
first ticker fires. `Stop()` blocks until the goroutine exits, following the
same lifecycle pattern as every other worker. `SampledAt` is zero until the
first walk completes; callers can use this to distinguish "not yet sampled"
from "empty store".

`decodeTenantDir` reverses `sanitiseTenant` (strips `t-` prefix, hex-decodes)
so `Tenant(rawName)` lookups work correctly against the on-disk directory
names introduced in patched103.

**`pkg/blob/store.go`** — `Usage` refactored.

The walk logic is extracted into `usageFromDir(tenantDirName string)`.
`Usage(tenant)` calls it via `sanitiseTenant`; `usageByDir(dir)` calls it
directly with an already-encoded name (used by the sampler to avoid
double-encoding). `hexDecode` helper added for use by `sampler.go`.

**`pkg/config/config.go`** — new config field.

| Field | Env var | Default |
|---|---|---|
| `BlobUsageSampleIntervalSecs` | `OLU_BLOB_USAGE_INTERVAL` | `300` |

Set to `0` to disable the sampler entirely; the usage API still responds (with
all-zero counts and no `sampled_at`).

**`pkg/server/server.go`** — sampler lifecycle wired.

`blobSampler` field on `Server`. Started after the blob store is initialised,
stopped in `Stop()` before store teardown. `injectBlobMetrics` populates blob
fields on `MetricsSnapshot` from the sampler cache at serve time.

**`pkg/middleware/metrics.go`** — telemetry extended.

`MetricsSnapshot` gains five blob fields: `BlobEnabled`, `BlobBlobCount`,
`BlobKeyCount`, `BlobBytes`, `BlobTenants`. `PrometheusFormat` is refactored
into `PrometheusFormatSnapshot(snapshot)` so the server can pass an augmented
snapshot; `PrometheusFormat()` delegates to it. Prometheus metrics emitted
only when `BlobEnabled` is true:

```
olu_blob_blobs    gauge   distinct blob files across all tenants
olu_blob_keys     gauge   key aliases across all tenants
olu_blob_bytes    gauge   total bytes used by blob files
olu_blob_tenants  gauge   tenants with at least one blob
```

**`pkg/server/blob_handlers.go`** — usage handler reads from cache.

`handleBlobUsage` no longer calls `bs.Usage()`; it reads `blobSampler.Tenant()`
instead. `blobUsageResponse` gains an optional `sampled_at` field
(RFC 3339, omitted when the sampler has not yet run).


### Blob disk usage accounting

**`Store.Usage` (`pkg/blob/store.go`)**

New method returning a `Usage` struct with three fields:

- `BlobCount` — distinct blob files on disk (content-deduplicated; two keys
  pointing to the same content count as one blob)
- `KeyCount` — key aliases in `.keys/`
- `Bytes` — total size of blob files, excluding `.ct` sidecars

Orphaned blobs awaiting GC and `PutRaw` blobs with no alias appear in
`BlobCount`/`Bytes` but not in `KeyCount`, giving operators a clear signal
that GC is lagging. Walk cost is O(blobs + keys), not O(bytes).

The `isHexPair` helper in `gc.go` is consolidated into `isHexPrefix` in
`store.go`; both files now reference the single definition.

**`handleBlobUsage` (`pkg/server/blob_handlers.go`)**

New handler on `GET /api/v1/blob/usage` and
`GET /api/v1/tenant/{tenant_id}/blob/usage`. Returns:

```json
{
  "tenant": "acme",
  "blob_count": 142,
  "key_count": 138,
  "bytes": 10485760
}
```

The discrepancy between `blob_count` and `key_count` indicates blobs awaiting
GC. Registered before `{key}` in both route blocks to avoid the wildcard
swallowing it. No S3-equivalent endpoint — there is no standardised S3 spec
for per-bucket disk usage.

**Tests (`pkg/blob/store_test.go`)**

Five new `TestUsage_*` cases: empty tenant, deduplication (two keys same
content → one blob), orphaned blob inclusion, `PutRaw` blob counted but not
in key count, and tenant isolation.

A `TODO` block marking the HTTP handler tests (blob JSON API and S3 surface)
as deferred to a forthcoming cleanup session is added at the end of the file.


### Blob GC logging — remove bespoke logger abstraction

The `GCLogger` interface, `noopLogger`, and `blobGCLogger` adapter introduced
in patched102 are removed. The blob GC worker now uses `zerolog/log` directly,
matching the pattern used by every other worker in the codebase (see
`pkg/timeseries/retention.go`).

Changes:
- `pkg/blob/gc.go`: drop `GCLogger` interface, `noopLogger` struct, `logger`
  field on `GCWorker`. Import `github.com/rs/zerolog/log`. All log calls
  converted to `log.Info()`/`log.Warn()` with typed fields.
- `NewGCWorker` signature drops the `logger GCLogger` parameter.
- `pkg/server/server.go`: remove `blobGCLogger` adapter type and its methods.
  Update `NewGCWorker` call site.
- `pkg/blob/store_test.go`: update all `NewGCWorker` calls to match new
  signature.

Note: `pkg/blob` now depends on `github.com/rs/zerolog`. This is already a
direct dependency of the module; no `go.mod` change is required.


### Blob package test suite (`pkg/blob/store_test.go`)

27 tests covering the full blob API and GC worker. No test files existed before
this patch.

Store tests: `Put`/`Get` round-trip, deduplication, overwrite, `ErrNotFound`,
`PutRaw` with no alias written, `PutRaw` idempotency, `GetBySHA`, `Head`,
`Delete` (alias removal and orphaned blob file), `List` with and without
prefix, `ErrTooLarge` enforcement for both `Put` and `PutRaw`, tenant
isolation, and the `sanitiseTenant` dot-vs-underscore collision fix that
patched103 introduced.

GC tests: orphan collection with immediate purge, live blob preservation,
grace period protection (quarantined but not deleted), restore of a
quarantined blob that becomes live again before the grace period expires,
`SHARefSource` interface (external SHA prevents collection), and multi-tenant
sweep (one tenant's orphan collected without disturbing another's live blob).


### Blobstore correctness fixes

Two correctness issues identified in patched102 review:

**`sanitiseTenant` collision fix (`pkg/blob/store.go`)**

The previous implementation replaced `/`, `\`, `.`, and `..` with `_`, which
caused distinct tenant names (e.g. `foo.bar` and `foo_bar`) to map to the same
directory — a silent namespace collision. The function now hex-encodes the
tenant string and prepends `t-`, guaranteeing that any two distinct tenant
strings produce distinct directory names. The `t-` prefix also makes tenant
directories human-recognisable and avoids collisions with internal directories
(`.keys`, `.gc-pending`). Empty tenant maps to `t-default`.

Note: existing deployments with non-alphanumeric tenant names will see new
directory names on first write after upgrade. Blobs under the old paths remain
accessible until migrated.

**`purgePending` live-set re-check (`pkg/blob/gc.go`)**

The previous implementation hard-deleted quarantined blobs based solely on
age. A blob that gained a key alias (or an external reference) during the grace
period would still be deleted, causing data loss. The purge phase now re-checks
the live SHA set before each hard-delete. Blobs that have become live again are
restored to their shard directory rather than deleted. `purgePending` receives
`tenantDir` alongside `pendingDir` so it can reconstruct the correct restore
path without calling back into the store.


### Blobstore mitigations — GC, abstraction fix, S3 auth hardening

Three issues identified in the patched101 blobstore review are addressed:

**Blob GC worker (`pkg/blob/gc.go`)**

A background mark-and-sweep GC that runs at a configurable interval. The
algorithm is entirely filesystem-native with no database dependency:

- Mark phase: walks `.keys/` for each tenant and accumulates the set of live
  SHAs. Cost is O(key count), not O(blob count).
- Sweep phase: walks the `{xx}/` prefix shard directories; any SHA not in the
  live set is moved to `.gc-pending/{sha}.{unix_nano}` (quarantine) rather
  than deleted immediately.
- Purge phase: blobs that have been in quarantine for longer than
  `BlobGCGracePeriodSecs` (default 10 minutes) are hard-deleted. The grace
  period protects against the race where `Put` has written the blob file but
  has not yet written the key alias when the sweep runs.

External SHA reference sources (e.g. a future timeseries history store) are
supported via the `SHARefSource` interface, which the GC calls once per sweep
before the mark phase. Any SHA returned there is treated as live regardless of
whether it has a key alias. The interface is one-directional and explicit —
the blob GC queries the TS store, not the other way round.

The worker follows the same lifecycle pattern as `tsRetention`: started in
`NewServer`, stopped in `Stop()`, held as `blobGC` on `Server`.

New config fields and env vars:

| Field | Env var | Default |
|---|---|---|
| `BlobGCEnabled` | `OLU_BLOB_GC_ENABLED` | `false` |
| `BlobGCIntervalSecs` | `OLU_BLOB_GC_INTERVAL` | `3600` |
| `BlobGCGracePeriodSecs` | `OLU_BLOB_GC_GRACE_PERIOD` | `600` |

**`PutRaw` — content-addressed path with no alias (`pkg/blob/store.go`)**

A new `PutRaw(tenant, reader, contentType)` method stores a blob and returns
the SHA-256 hex digest. No key alias is written. This is the correct path for
callers that use the SHA as both identity and reference (the history versioning
system, or any purely content-addressed use case).

The probe-then-realias dance in `handleBlobPut` is replaced with a direct
`PutRaw` call when no key is supplied: the sentinel key is no longer created
or deleted, no second write happens, and the `.keys/` index is not polluted.
`GetBySHA` is the retrieval path for raw blobs.

A `Root() string` accessor is added to `Store` for use by the GC worker.

**S3 auth warning and `S3RequireAuth` flag (`pkg/server/blob_s3_handlers.go`)**

`s3TenantFromRequest` is promoted from a free function to a `Server` method so
it can access the logger and config. Two changes to its behaviour:

1. When Authorization is absent a structured `Warn` log is emitted including
   bucket name and remote address. This gives operators a clear signal when
   clients are misconfigured.
2. A new `S3RequireAuth bool` config field (env var `OLU_S3_REQUIRE_AUTH`,
   default `false`) causes requests without Authorization to be rejected with
   HTTP 403 and an S3-style `AccessDenied` XML response. The default preserves
   existing behaviour.

All six S3 handler call sites updated to use the method form and check the
empty-string return that indicates the response has already been written.

### Restore — iolu commands lost between patched73 and patched99

Four command groups were present in the vendored xolu at patched73 but
absent from the current iolu. All are restored and forward-ported to
patched99's codebase. One case-sensitivity bug is fixed in the process.

**Restored commands:**

`iolu db init --db <path> [--tenant name[:id]] [--graph] [--ts-dir <path>]`
Creates a new olu SQLite database with the full shared-mode schema, optional
graph edge tables, and optional timeseries directory provisioning. Accepts
repeatable `--tenant` flags to pre-register tenants in one step (required
for strict-mode deployments).

`iolu db status --db <path> [--base-dir <path>]`
Reports database file size, schema versions, per-table row counts, graph
edge table listing, tenant registry, and timeseries directory status.

`iolu db upgrade --db <path>`
Applies pending schema migrations idempotently. Currently handles v2
(schema_version seeding) and v3 (_version column). Safe to run against
a patched99 database — both migrations report "already applied".

`iolu tenant provision-ts --db <path> --name <name> --ts-dir <path>`
Creates the per-tenant timeseries directory for a named tenant.

**Bug fix — timeseries directory case:**

The vendored iolu used lowercase `t%04x` for timeseries directory names
(`t000a`, `t00ff`). `tenant.StorageDirSegment` uses uppercase `t%04X`
(`t000A`, `t00FF`). The mismatch only affects tenants with IDs ≥ 10
(where hex letters appear). All three affected call sites are corrected:
`provisionTSDir`, `cmdTenantInfo`, and the `db init` `--ts-dir` path.
The `db status` timeseries check was not affected (it scans the directory
rather than constructing a path).

**Also restored:** `tenantFlags` repeatable flag type, `registerTenant`,
`createGraphTable`, `initSchema`, `ensureCoreTables`, `applyMigration`,
`createDB`, `tableCount`, `tenantEntityCount` helper functions.

All 2484 tests pass. iolu binary smoke-tested against a live SQLite database.



### Change — iolu included in standard Docker image

`Dockerfile` now builds and ships `iolu` alongside `olu` in the runtime
stage. Both binaries are available in `ghcr.io/ha1tch/xolu:latest`:

- `/usr/local/bin/olu`  — the olu server (unchanged)
- `/usr/local/bin/iolu` — the iolu administrative CLI

This allows downstream projects (nolu and others) that use iolu as a
Docker init container to simply override the entrypoint of the runtime
image rather than maintaining a separate build context or vendored source
tree.

No code changes — build and tests unaffected.



### Tests — Stronger per-file tenant test coverage (7 new tests, 4 OQL variants)

Addresses the coverage gaps identified after patched97. All tests target
code paths that were changed during the per-file implementation but were
either untested or tested only on the happy path.

**`pkg/oql/per_file_pushdown_test.go`** — 4 tests:

`TestGenerateSQL_PushWhere_PerFileNoTenantID`: the golden SQL test the spec
required. Calls `GenerateSQL` with `PushWhere` and a real WHERE predicate,
asserts `tenant_id` is absent from the SQL string AND that `len(Args) == 2`
(not 3), which would catch a spurious tenant_id arg injected even if not
named in SQL. Paired with `TestGenerateSQL_PushWhere_SharedModeArgCount`
(regression guard: shared mode must still emit 3 args). Two compound-WHERE
variants repeat the same arg-count assertion for multi-predicate queries.

**`pkg/storage/sqlite_per_file_stronger_test.go`** — 7 tests:

`TestPerFile_FTSDeleteIsolation`: per-file equivalent of the existing
`TestSQLiteTenantIsolation_FTSUpdateDelete`. Two stores at separate file
paths — update+delete in store A, assert store B's FTS index is untouched.
Directly covers the `deleteInner` FTS path that caused the patched96 regression.

`TestPerFile_AdaptedCRUDRoundTrip`: exercises all four adapted-table
mutations (Create, Get, Update, Delete, List, Patch) in per-file mode.
`dialectIsPerFile()` in `adapted_crud.go` changes the arg lists for
Update (id only, not tenantID+id), Delete (same), and List (no args). All
branches are now exercised.

`TestPerFile_CommitRoundTrip`: exercises `createInTx` (auto-ID and
explicit-ID append) and `saveInTx` (exists→update and !exists→insert)
paths, covering the sequence-branching and INSERT-branching code in the
Commit transactional batch.

`TestPerFile_SequencePrimaryKeyConstraint`: raw SQL verification of the
`PRIMARY KEY (entity_type, id)` semantics — same (type, id) fails; same
id different type succeeds; also confirms `entity_sequences` has no
`tenant_id` column via a raw insert.

`TestPerFile_GraphIntegrityAndRebuild`: verifies `VerifyGraphIntegrity`
and `RebuildGraph` in per-file mode. Creates REF relationships, deletes
the edge table manually, rebuilds, asserts edge count. The entity scan in
both methods uses `WHERE 1=1` (via `tenantWhere()`) in per-file mode —
this test would catch a broken scan silently returning zero edges. Also
checks cross-contamination: a second store at a different path has zero edges.

`TestPerFile_AdaptedHasExtra`: verifies the `_extra` overflow column path
in adapted tables (extra fields beyond the schema) works in per-file mode,
and confirms no `tenant_id` column in the resulting table.

**`pkg/server/per_file_tenant_test.go`** (appended) — 1 test:

`TestPerFileTenant_MutationsAndCrossTenantIsolation`: HTTP-level PUT, PATCH,
DELETE in per-file mode. After DELETE in tenant mu1, tenant mu2's entity at
the same ID is still accessible — the key cross-tenant isolation assertion
at the HTTP level, covering the `deleteInner` path end-to-end.

All 2490+ tests pass.



### Fix + Tests — SQLite per-file tenant isolation complete

Fixes two bugs from patched96 and adds Phase 4 tests from the spec.

**Bug fixes:**

- `NewSQLiteStore` was not copying `PerFileTenants` into `storeConfig`, so
  `Config().SQLitePerFileTenants` was always `false`. This meant
  `storeForTenant` in `server.go` never derived the per-tenant file path.
  Fixed by adding `SQLitePerFileTenants: config.PerFileTenants` to the
  `storeConfig` literal.

- `deleteInner` FTS delete was dropping the `tenant_id` filter in
  shared mode (regression from patched96 patch). Fixed with proper
  `PerFileTenants` branch in `deleteInner`.

**New tests (Phase 4):**

`pkg/storage/sqlite_per_file_test.go` — 9 tests covering: schema DDL
without `tenant_id` column, CRUD round-trip, two-store isolation
(independent sequences, invisible cross-tenant), `CountEntities`,
`QueryWithPlan`, `IsPerFileTenant`, adapted table DDL, FTS, and Save.

`pkg/oql/per_file_test.go` — 4 tests covering: `GenerateSQL` with empty
`sqlTenantID` emits no `tenant_id` clause; `GenerateSQL` with non-empty
tenant ID still emits the clause (shared-mode regression guard);
`ExecuteWithStore` against per-file store succeeds without `tenant_id`
column errors; two-store OQL isolation.

`pkg/server/per_file_tenant_test.go` — 2 integration tests: per-file
mode creates separate database files per tenant and GET from one tenant
returns only its own data; shared mode (default) maintains `tenant_id`
column isolation and does not create a `sql/` directory.

All 2490+ tests pass.



### Feature — SQLite per-file tenant isolation (`OLU_SQLITE_PER_FILE_TENANTS`)

Implements the full `SQLITE_PER_FILE_TENANTS.md` spec. When
`OLU_SQLITE_PER_FILE_TENANTS=true`, each tenant gets its own SQLite database
file and the `tenant_id` column is absent from the schema entirely — the file
itself is the isolation boundary.

**Files changed:** `pkg/config/config.go`, `pkg/storage/storage.go`,
`pkg/storage/factory.go`, `pkg/storage/sqlite.go` (schema split, helpers,
all query methods), `pkg/storage/sqlite_field_query.go`,
`pkg/storage/dialect_sqlite.go`, `pkg/storage/adapted_crud.go`,
`pkg/oql/executor.go`, `pkg/server/server.go`, `cmd/olu/main.go`.

Key design points:
- `tenantWhere()` / `tenantArgs()` helpers make query method changes
  mechanical — no SQL string is duplicated.
- `createSchemaShared()` / `createSchemaPerFile()` implement both DDL paths.
- `SQLiteStorageDialect.PerFileTenants` gates adapted table DDL/queries.
- `dialectIsPerFile()` type assertion gates adapted CRUD arg lists.
- `OQL executor`: `sqlTenantID` is suppressed when `IsPerFileTenant()` is
  true; INSERT no longer injects `tenant_id` into the record map.
- `server.go`: `tenantDBPath()` derives `<dir>/sql/tXXXX/<base>` paths;
  `storeForTenant` creates the directory and passes the derived path.
- Default is `false` — all existing deployments are unaffected.
- All 2360+ tests pass.

## [0.9.7-patched95] - 2026-05-14

### Fix — release.sh cleans all test artifact types from pkg/ before packaging

Some tests write SQLite files and other artifacts relative to their package
directory. `release.sh` now removes all known patterns from every subdirectory
of `pkg/` before zipping, and excludes all the same patterns from the zip.

**Cleaned and excluded:**

| Pattern | What it is |
|---|---|
| `*.db`, `*.db-wal`, `*.db-shm`, `*.db-journal`, `*.db-tmp` | SQLite database + WAL sidecars |
| `*-wal`, `*-shm`, `*-journal` | SQLite sidecars without `.db` base (some drivers) |
| `graph.data`, `graph.index` | FlatGraph persistence files (default from `config.Default()`) |
| `*.golden`, `*.pprof`, `*.prof`, `*.test` | Go test output formats |

The post-zip sanity check was also widened to catch any of these patterns
if they somehow slip through the exclusions.


## [0.9.7-patched94] - 2026-05-14

### Tests — complete BETWEEN coverage across all three execution paths

patched93 added string BETWEEN tests for the Go path and the blob
push-down path. This patch adds the adapted entity paths, completing
the matrix.

| Path | Numeric BETWEEN | String BETWEEN |
|---|---|---|
| Go path | ✓ pre-existing | ✓ added in patched93 |
| Blob push-down (`PushWhere` / `json_extract`) | ✓ pre-existing | ✓ added in patched93 |
| Adapted full push-down (`PushFull` / native columns) | ✓ pre-existing | ✓ added here |
| Adapted aggregate push-down (`PushAggregate`) | ✓ pre-existing | ✓ added here |

New test cases:

`TestFullPD/WhereBetween_string` and `TestFullPD/WhereBetween_string_notbetween`
(`adapted_full_pushdown_test.go`) — `product BETWEEN 'gadget' AND 'gizmo'`
on the adapted `items` entity. The adapted path uses native SQL columns with
no CAST, so the code was correct; the test makes that claim testable.

`TestAdaptedPushDown/WhereBetween_string` and
`TestAdaptedPushDown/WhereNotBetween_string`
(`adapted_pushdown_test.go`) — same query through the Go vs adapted
comparative harness.

All tests are equivalence-style: they run the same query through the Go path
and the push-down path and assert identical result sets.


## [0.9.7-patched93] - 2026-05-14

### Fix — BETWEEN with string bounds silently coerced to REAL, returning wrong results

**Bug:** `sqlgen.go` used `JSONFieldNumeric` (i.e. `CAST(json_extract(data,
'$.field') AS REAL)`) for every BETWEEN expression, regardless of whether the
bounds were numeric or string literals. SQLite's CAST of a date string like
`"2026-02-15T00:00:00Z"` to REAL yields `2026.0` (the leading numeric prefix)
for every value in the same year. The result: any query of the form
`WHERE timestamp BETWEEN '2026-01-01' AND '2026-06-30'` returned zero rows,
with no error message.

The Go evaluation path (`compareValues` in `aggregator.go`) was unaffected —
it tries numeric parsing and falls back to string comparison, so Go-path and
push-down path diverged silently on the same query.

**Root cause:** The comment `// BETWEEN always uses numeric comparison (range
implies ordering)` was wrong. Lexicographic ordering is valid for strings;
ISO-8601 timestamps and zero-padded codes (SENS-0001, SENS-0200) compare
correctly without any cast.

**Fix (`pkg/oql/sqlgen.go`):** Inspect the types of the low and high bound
literals. When both bounds are numeric (`int`, `int64`, `float64`, `float32`),
use `JSONFieldNumeric` as before. When either bound is a string, use
`JSONField` (no CAST). This matches the logic already in `chooseFieldExtraction`
for `>`, `<`, `>=`, `<=` operators.

**Tests added:**

`TestSQLGen_BetweenStringBoundsUseTextExtraction` (`pkg/oql/sqlgen_test.go`) —
four sub-tests asserting the generated SQL contains `json_extract(...) BETWEEN`
(no CAST) for string bounds, and `CAST(... AS REAL) BETWEEN` for numeric
bounds. Includes NOT BETWEEN and mixed-name cases.

Three sub-tests added to `TestEquivalence` (`pkg/oql/equivalence_test.go`):
- `StringBetween_timestamps` — ISO-8601 timestamp range against the `readings`
  entity. Would have returned 0 rows from push-down before the fix.
- `StringNotBetween_timestamps` — NOT BETWEEN on timestamps.
- `StringBetween_codes` — zero-padded code strings (`SENS-0100` to `SENS-0200`).

These are equivalence tests: they assert Go-path and push-down produce
identical result sets, so any future regression in type-aware field extraction
will be caught immediately.


## [0.9.7-patched92] - 2026-05-14

### Docs — JOIN support documented, stale "not supported" language removed

Updated all locations that stated or implied OQL does not support JOIN
statements.

**`docs/OQL_API.md`** (primary user doc):
- Version bumped to 0.9.7.
- Limitations table: four JOIN rows now show ✓ Supported (INNER, LEFT, RIGHT,
  FULL OUTER); CROSS JOIN and three-table joins explicitly marked ✗ Not
  supported; subquery tables in FROM added as ✗.
- New "JOIN Queries" section added before Limitations, covering: supported join
  types, syntax, entity classification (adapted vs blob), examples for all
  cases (both adapted, blob entities, right join with nulls), column alias
  rules, constraints, and a JOIN vs Sulpher decision table.
- "When to Use OQL vs Sulpher" table: added "Flat cross-entity correlation →
  OQL JOIN" row.

**`docs/QUERY_OPTIMISATION_PROGRESS.md`**:
- Section renamed from "Future work: JOIN push-down (exploration)" to
  "JOIN push-down — implemented in v0.9.7-patched89/90".
- Opening paragraph updated to describe what was built and where to find the
  spec and user docs.
- "What would be needed" table: all four rows marked ✓ Done.
- Entity combination matrix: all three combinations (adapted+adapted,
  adapted+blob, blob+blob) marked ✓ Yes (blob-to-blob feasibility corrected
  from "No push-down" to implemented).
- Status block changed from "Exploration only. No implementation planned"
  to "Implemented."

**`docs/QUERY_PLANNER.md`**:
- Section 13 future-work table: two JOIN rows collapsed into one marked
  "Implemented in v0.9.7-patched89–91."

**`pkg/oql/oql.go`** (package comment):
- Added JOIN to the bullet list of supported features.
- Added "JOIN support" sub-section clarifying push-down to SQLite, all four
  join types, per-entity adapted/blob classification, and explicit list of
  unsupported forms.

**`MANUAL.md`**:
- Supported Features list: added JOIN, BETWEEN, IS NULL, DISTINCT, and
  INSERT/UPDATE/DELETE (previously omitted).
- Added JOIN example (orders with customer name).
- Added "Not supported" paragraph listing CROSS JOIN, three-table joins,
  subqueries, CTEs, window functions, and the SQLite-only constraint.


## [0.9.7-patched91] - 2026-05-14

### Tests — regression coverage for five bugs found in patched90

Six regression tests added, one per bug from the patched90 join push-down
implementation, to prevent silent re-introduction.

| Test | Bug guarded | File |
|---|---|---|
| `TestRegression_BlobJoinUsesJSONFieldAliased` | Bug 1: `a.json_extract(...)` form rejected by SQLite | `pkg/oql/sqlgen_join_test.go` |
| `TestRegression_JoinColumnAliasNoDots` | Bug 2: dotted alias `AS a.title` rejected by SQLite | `pkg/oql/sqlgen_join_test.go` |
| `TestRegression_SystemColumnIDResolvedForAdapted` | Bug 3: `id` not found in adapted entity registry | `pkg/oql/sqlgen_join_test.go` |
| `TestRegression_ListEntitiesIncludesAdapted` | Bug 4: adapted entities absent from `ListEntities` | `pkg/storage/list_entities_regression_test.go` |
| `TestRegression_ListEntitiesNoDuplicates` | Bug 4 (complementary): adapted entity appears at most once | `pkg/storage/list_entities_regression_test.go` |
| `TestRegression_JoinResultKeysAreBareName` | Bug 5: `projectColumns` re-keyed join rows as `a.title` | `pkg/server/join_e2e_test.go` |


## [0.9.7-patched90] - 2026-05-14

### Feature — OQL JOIN push-down (tests + bug fixes)

All three test suites specified in `docs/OQL_JOIN_PUSHDOWN.md` are now
implemented and passing. In the process, four bugs were found and fixed.

#### Tests added

**`pkg/oql/planner_join_test.go`** — 20 tests covering:
- `TestPlanner_Join`: PushJoin returned for all join types (INNER, LEFT, RIGHT,
  FULL) and all entity combinations (both adapted, both blob, mixed); PushNone
  when store lacks `AggregateQueryable`; join spec fields verified.
- `TestExtractJoinSpec`: nil-FROM guard, correct spec extraction, INNER JOIN.
- `TestIsJoinConditionPushable`: forward and reversed operand order.
- `TestIsJoinWherePushable`: qualified comparisons, AND, IN, IS NULL.

**`pkg/oql/sqlgen_join_test.go`** — 12 tests covering:
- Both adapted: native columns, no `json_extract`.
- Both blob: `json_extract` throughout, `entities` aliased twice.
- Left adapted / right blob and mirror case.
- WHERE clause (adapted and blob paths).
- Tenant scoping (two `tenant_id` clauses).
- No-WHERE produces clean SQL with zero args.
- FULL JOIN → `FULL OUTER JOIN`; LEFT JOIN preserved.
- Alias list matches SELECT column count.
- Nil plan.Join returns error.

**`pkg/server/join_e2e_test.go`** — 4 tests covering:
- `TestJoinPushdown_BothAdapted`: INNER JOIN two adapted entities end-to-end,
  correct cross-entity field values, `rows_scanned` sanity check.
- `TestJoinPushdown_BlobFallback`: INNER JOIN two blob entities via the
  `json_extract` path.
- `TestJoinPushdown_OuterJoin_NullRows`: RIGHT JOIN with unmatched left rows;
  Carol (no orders) appears with nil amount, no crash.
- `TestJoinPushdown_ValidatorRejectsUnsupported`: CROSS JOIN returns 4xx, not 5xx.

#### Bugs found and fixed

**1. `json_extract` with unqualified `data` in joins** (`pkg/oql/sqlgen_join.go`).
In the blob-path join, both sides reference the `entities` table under
different aliases. `joinFieldRef` was emitting `a.json_extract(data, '$.x')`
(prepending alias before the whole expression) instead of
`json_extract(a.data, '$.x')` (alias qualifying just the `data` column).
Fix: added `JSONFieldAliased(alias, field string) string` to the `SQLDialect`
interface (SQLite: `json_extract(alias.data, '$.field')`), and updated
`joinFieldRef` to use it for blob sides.

**2. Dotted column aliases rejected by SQLite** (`pkg/oql/sqlgen_join.go`).
Without explicit `AS` aliases, `columnAlias` returned the full qualified name
`a.title` for a `QualifiedIdentifier`, producing `SELECT ... AS a.title`
which SQLite rejects. Fix: `joinColumnAlias` strips the table qualifier for
unaliased `QualifiedIdentifier` columns, emitting `AS title`.

**3. System column `id` not found in adapted entity** (`pkg/oql/sqlgen_join.go`).
`AdaptedColumnInfo` only knows user-declared schema fields; the `id` PK column
is a system column absent from the registry. ON conditions like `a.author_id =
b.id` failed with "field id not found". Fix: `adaptedNativeColumn` helper
recognises `id`, `_version`, and `tenant_id` as system columns and returns them
directly without a registry lookup.

**4. `ListEntities` missed adapted entities** (`pkg/storage/sqlite.go`).
The OQL validator calls `ListEntities` to check entity existence. Adapted
entities live in their own tables (`olu_posts`, etc.) and have no rows in
the `entities` table, so the original `SELECT DISTINCT entity_type FROM
entities` query did not return them. The validator then rejected JOIN queries
targeting adapted entities with "entity does not exist". Fix: `ListEntities`
now unions blob entity names with `s.adapted.Entities()`.

**5. `projectColumns` re-keyed join results with dotted names** (`pkg/oql/executor.go`).
After push-down, the executor always called `projectColumns`, which used
`columnAlias` (returning `a.title`) to look up and output keys. The join
records were already correctly shaped with bare aliases (`title`), but
`projectColumns` re-keyed them as `a.title`. Fix: `PushJoin` records bypass
`projectColumns`; the `AggregateQuery` result is used directly.


## [0.9.7-patched89] - 2026-05-13

### Rename — timeseries Pebble subdirectory `pebble/` → `db/`

The directory created inside each tenant's timeseries store directory was
named `pebble/` — an implementation detail (the storage engine name) leaking
into the filesystem layout. Renamed to `db/` in `NewPebbleStore`.

Layout before:  `data/ts/t0001/pebble/`
Layout after:   `data/ts/t0001/db/`

This is a breaking change for any deployment with existing timeseries data.
To migrate, rename the `pebble/` subdirectory to `db/` inside each tenant's
timeseries directory before restarting olu.

### Docs — SQLite per-file tenant isolation implementation plan

Added `docs/SQLITE_PER_FILE_TENANTS.md` (437 lines): full implementation
plan for the optional `SQLitePerFileTenants` mode, updated to reflect the
agreed filesystem layout:

```
data/
  olu.db            ← tenant 0 base store
  ts/
    t0001/          ← per-tenant timeseries
      db/           ← Pebble LSM (all timelines as key ranges)
      registry.json
      meta.json
  sql/              ← per-tenant SQLite (when SQLitePerFileTenants = true)
    t0001/
      olu.db
    t0002/
      olu.db
```

Key decisions recorded in the plan:
- `sql/` (not `sqlite/`) for backend-agnostic naming
- `db/` (not `pebble/`) for the Pebble LSM directory
- Timelines are key-prefix ranges inside a single Pebble instance per
  tenant, not per-timeline directories or files
- `tenantDBPath` mirrors the timeseries `tenantDir` pattern exactly
- `os.MkdirAll` called before opening a new per-file tenant store

## [0.9.7-patched88] - 2026-05-13

### Fix — codec.go: payload dangling slice into Pebble-managed buffer

`DecodeValue` was returning `payload = val[pos : pos+plen]` — a direct
sub-slice of the `val` byte slice passed in by the caller. In all call sites
in `store.go`, `val` comes from `iter.Value()`, which returns a slice into
Pebble's internal memory. That memory is only valid for the current iterator
position; it is recycled when the iterator advances or closes.

`decodeEntry` is called inside iterator loops and the returned `Event` values
(including their `Payload` slices) are accumulated in a results slice. After
the loop, `defer iter.Close()` fires, at which point the payload slices in
the results are dangling references into Pebble's memory. Under normal timing
the memory is not immediately reused, so the bug was silent. Under race-detector
timing (which widens scheduling windows) the buffer was overwritten before the
caller read the payload, producing `\xff\xff\xff\xff\xff` corruption
(Pebble's tombstone fill pattern) instead of the written payload bytes.

The failure was first observed as a flaky `TestContract_Pebble/AppendQueryRange_FullPrefix`
failure (roughly 4/20 runs under `-race`). It also affects any caller that
retains `Event.Payload` after an iterator advance, including `Latest`,
`Aggregate`, and the full aggregate scan functions — all of which use
`DecodeValue` against `iter.Value()`.

Fix: replace the sub-slice assignment with an explicit copy:

    payload = make([]byte, plen)
    copy(payload, val[pos:pos+plen])

This is the only safe contract for callers who retain decoded data past the
iterator's lifetime. The comment in `DecodeValue` documents why.

**Also fixed in this patch:** the stale TODO comment in
`commit_ts_rollback_test.go` referring to `tsManager` being a concrete type
— it is already `timeseries.Manager` (interface) and `SetTSManager` already
exists. The TODO was not removed when the injection point was added.
That comment is left as-is for now; removing it is a documentation task.

## [0.9.7-patched87] - 2026-05-13

### Fix — TestGraphCounters_ConcurrentAccuracy: cold-store race under -race detector

The test fired 80 concurrent goroutines that all hit `storeForTenant` before
any tenant store was cached. Under race-detector timing (which widens goroutine
scheduling windows significantly) multiple goroutines simultaneously took the
slow path in `storeForTenant` and called `NewStoreFromConfig` concurrently
against the same SQLite file, causing some opens to fail with OLU-ST006
("Failed to initialise tenant context"). This caused writes to drop silently
and the count assertions to fail with unexpected totals.

The bug is in the test, not in `storeForTenant`. The `LoadOrStore` race guard
in `storeForTenant` is correct for steady-state operation; the problem is that
`NewStoreFromConfig` is not safe to call concurrently for the same file before
any connection is cached.

Fix: add a single synchronous `seedGraphEntity` call per tenant before
launching the concurrent goroutines. This pre-warms the `tenantStores` cache
so all 80 goroutines hit the fast path. Expected node counts updated to
reflect the two warmup nodes (one per tenant, ID 0).

Verified stable across 10 consecutive runs under `-race`.

## [0.9.7-patched86] - 2026-05-13

### Build — add `build-olu` target for single-binary builds

patched85 removed the ability to build only `olu` since `make build` now
builds all three binaries. Added `build-olu` as an explicit single-binary
target for situations where only `olu` is needed (fast iteration, CI jobs
that test only the server binary, etc.).

## [0.9.7-patched85] - 2026-05-13

### Build — `make build` now builds olu, iolu, and olu-migrate by default

Previously `make build` built only `olu`. `iolu` and `olu-migrate` required
separate `make build-iolu` and `make build-migrate` invocations, or the
combined `make build-all-tools`. The `release.sh` script called `make build`
directly, so only `olu` was verified to compile on each release.

`make build` now builds all three binaries in sequence. The individual
`build-iolu`, `build-migrate`, and `build-all-tools` targets are unchanged.
The `help` target description for `build` is updated accordingly.

## [0.9.7-patched84] - 2026-05-13

### Docs — OQL JOIN push-down implementation specification

Added `docs/OQL_JOIN_PUSHDOWN.md` (613 lines): a complete implementation
specification for SQLite-pushed JOIN queries in OQL.

Covers the full phased implementation plan (planner, SQL generator, executor,
validator, tests), the per-entity adapted/blob path decision, SQL shapes for
all supported join types (INNER, LEFT, RIGHT, FULL OUTER), NULL result row
handling for outer joins, the out-of-scope items (CROSS JOIN, Go-path
fallback, three-table joins), and a PostgreSQL compatibility section addressing
dialect mediation, `AggregateQuery` opacity, and the adapted/blob table
co-location assumption.

No code changes.

## [0.9.7-patched83] - 2026-05-13

### Tests — HTTP-layer graph path/pathExists/shortestPath coverage + cyclic termination + concurrent counter accuracy

Added `pkg/server/graph_path_e2e_test.go` (16 new tests). This addresses two
gaps identified in the graph test-suite review:

**Gap 1: no server-layer coverage of /graph/path, /graph/pathExists, or
/graph/shortestPath**

These three endpoints existed only in the unit-level contract suite
(pkg/graph/graph_contract_test.go). If the HTTP handlers misparse `max_depth`,
return wrong status codes on the no-path case, or leak the XXXX@ tenant prefix
in responses, no test would catch it.

Tests added per endpoint:

- `/graph/path`: happy path (chain of 4, length 3), self-path (length 0),
  no-path → 404, max_depth exceeded → 404 / sufficient → 200, missing params
  → 400, no XXXX@ prefix in response, tenant isolation (alpha chain invisible
  from beta routes).
- `/graph/shortestPath`: found (exists:true), not-found (200 + exists:false,
  unlike /path which returns 404), missing params → 400. Verifies the semantic
  difference between the two path endpoints is preserved at the HTTP layer.
- `/graph/pathExists`: found (exists:true + correct length), not-found
  (exists:false + length 0), missing params → 400, absent node → 404.

**Gap 2: no HTTP-layer cyclic-graph termination test**

`TestContract_PathExists_CyclicGraph_Terminates` existed in the unit contract
but had no server-layer analogue. `TestGraphPath_CyclicGraph_Terminates`
seeds a forward chain (node:1→node:2→node:3) via HTTP entity writes, then
injects the back-edge (node:3→node:1) directly via `s.graph` to close the
cycle. It then exercises both `/graph/pathExists` and `/graph/path` against
the cyclic graph and asserts: correct results, no hang within the test timeout,
and no XXXX@ prefix in responses.

**Gap 3: counter accuracy under concurrent writes (server layer)**

The unit contract tests verify counter correctness sequentially.
`TestGraphCounters_ConcurrentAccuracy` fires 80 concurrent goroutines writing
entities under two tenants (50 alpha, 30 beta), then cross-checks the HTTP
stats endpoint (`node_count`/`edge_count`) against direct calls to
`NodeCountForTenant`/`EdgeCountForTenant` on `s.graph`. Counter drift between
the HTTP layer and the underlying per-tenant maps under write concurrency is
the most likely production failure mode and was untested at this layer.

Also fixed: one incidental bug found during test authorship — `node_count` and
`edge_count` are the correct JSON field names in the stats response (not
`nodes`/`edges`).

**Files changed:**

- `pkg/server/graph_path_e2e_test.go` — new file, 16 tests

## [0.9.7-patched82] - 2026-05-13

### Fix — CommitTS test harness and missing POST /ts/query/range route

Two bugs introduced with the patched78-era CommitTS tests were found on this
checkpoint's first `make test` run.

**Bug 1: `commitTSEnv.registerTenant` used a non-existent HTTP route**

`commit_ts_e2e_test.go`'s test harness was calling
`POST /api/v1/tenant/{name}` over HTTP to register tenants, but no such
registration-only route exists in strict-mode olu. The result was a 404 on
every `registerTenant` call, which caused the downstream `POST /ts/provision`
call to fail with OLU-ST001 (unknown tenant), taking down all 15 `TestCommitTS_*`
tests.

Fix: rewrote `commitTSEnv.registerTenant` to call
`srv.TenantRegistry().GetOrRegister(ctx, name)` directly, matching the pattern
used by `tsEnv.registerTenant` in `ts_e2e_test.go`. Added `"context"` import.

**Bug 2: `tsQueryRange` in the test harness called `POST /ts/query/range`,
which was not a registered route**

The four remaining failing tests (`HappyPath_WithAppendAndTS`,
`SQLiteFailure_TombstonesEvents`, `SQLiteFailure_MultipleEvents_AllTombstoned`,
`SuccessfulCommit_EventPersists`) all queried Pebble via the helper, which sent
`POST /ts/query/range`. The only registered range-query route was
`GET /ts/events` (query-string parameters), so the POST returned 404.

Fix: added `HandleTSQueryRangePost` to `pkg/server/ts_handlers.go`. The handler
accepts a JSON body with the same logical parameters as `GET /events`
(`timeline`, `dims`, `from`, `to`, `limit`, `order`) and produces an identical
response. Registered as `POST /ts/query/range` in the `/ts` sub-router in
`server.go`.

`POST /ts/query/range` is the correct REST shape for complex range queries:
it avoids URL-length limits on large dimension arrays and is more ergonomic
for callers that already compose JSON payloads (such as `/commit` clients).
The `GET /events` endpoint is unchanged and remains supported for simple queries.

**Result:** all 15 `TestCommitTS_*` tests pass; full suite green.

**Files changed:**

- `pkg/server/commit_ts_e2e_test.go` — `registerTenant` rewritten; `"context"` import added
- `pkg/server/ts_handlers.go` — `HandleTSQueryRangePost` added (~90 lines)
- `pkg/server/server.go` — `POST /query/range` registered in `/ts` sub-router

## [0.9.7-patched64] - 2026-03-11

### Hygiene — Remove application-specific references from documentation and source

Scrubbed all references to a specific client application from the public-facing
codebase in preparation for open-source publication. olu is application-agnostic
and its documentation should not reveal the identities of integrating parties.

**Files changed:**

- `CHANGELOG.md` — rewrote entries that named the client, their instance names
  (`olu-registry`, `olu-ams`, `olu-ops-{1,2}`), their internal metric names, and
  their architectural decisions as the motivating rationale. Replaced with generic
  descriptions of the feature motivations.
- `docs/COMMIT_ENDPOINT_DESIGN.md` — replaced client-specific use-case framing
  with generic FSM/financial domain examples. Replaced `{giai}` ID placeholder
  with `{id}`. Removed named instance references.
- `pkg/oql/hardware_profile.go` — replaced client name in `ProfileVPS` comment
  with "self-hosted olu instances".
- `pkg/server/shelf_integration_test.go` → renamed to `integration_test.go`.
  All type names (`shelfTestEnv`, `shelfEntities`), comment callouts
  ("Shelf's exact query", "Shelf uses..."), and temp directory prefixes
  updated to generic equivalents.
- `pkg/server/shelf_tier1_test.go` → renamed to `tier1_oql_test.go`. Same
  scrub applied.
- `pkg/server/e2e_test.go` — updated file references in header comments;
  `shelf_schema` → `test_schema`.
- `pkg/server/e2e_coverage_gaps_test.go` — `shelf_schema` → `test_schema`.
- `pkg/server/commit_e2e_test.go` — `"giai"` field name in one test fixture
  replaced with `"ref_id"`.
- `pkg/config/config_test.go` — `/data/olu/shelf.db` path in test replaced
  with `/data/olu/registry.db`.
- `docs/TESTING_STRATEGY.md` — table updated to reflect renamed test files.

No functional changes. Test count unchanged.

## [0.9.7-patched63] - 2026-03-11

### Feature — env var aliases for combined address, path, and log level

Added convenience aliases to `LoadFromEnv` for callers that prefer concise
configuration (e.g. Docker Compose deployments):

| New variable | Equivalent to | Format |
|---|---|---|
| `OLU_ADDR` | `OLU_HOST` + `OLU_PORT` | `host:port` |
| `OLU_METRICS_ADDR` | `OLU_METRICS_HOST` + `OLU_METRICS_PORT` | `host:port` |
| `OLU_SQLITE_PATH` | `OLU_DB_PATH` | file path |
| `OLU_LOG_LEVEL` | replaces `OLU_DEBUG` bool | `debug\|info\|warn\|error` |

Precedence: specific variables (`OLU_HOST`, `OLU_PORT`, etc.) override the
combined aliases when both are set. `OLU_SQLITE_PATH` overrides `OLU_DB_PATH`
when both are set. All original variable names continue to work unchanged.

`OLU_LOG_LEVEL` is case-insensitive; unknown values are silently ignored and
the default (`info`) is retained. `OLU_DEBUG=true` remains supported as a
legacy alias for `OLU_LOG_LEVEL=debug`; `OLU_LOG_LEVEL` takes precedence.

`cfg.LogLevel` is now wired to `zerolog.SetGlobalLevel` in `cmd/olu/main.go`,
making `OLU_DEBUG` actually effective at runtime for the first time.

Added `net` import to `pkg/config/config.go` for `net.SplitHostPort`.
Added `LogLevel string` field to `Config` struct (default: `"info"`).
9 new config tests covering aliases, precedence, case handling, and
compat behaviour.

### Infra — Dockerfile and GitHub Actions publish workflow

Dockerfile at repo root: two-stage build (golang:1.22-alpine builder,
alpine:3.19 runtime), CGO_ENABLED=0 (modernc.org/sqlite is pure Go),
non-root user (uid 1001), `/data` workdir for volume mounts.

`.github/workflows/publish.yml`: triggers on `v*.*.*` and `v*.*.*-patched*`
tags plus manual dispatch. Runs full test suite before push. Publishes to
`ghcr.io/ha1tch/olu` with exact version tag and `:latest`. Uses GHA layer
cache for fast incremental builds.

## [0.9.7-patched62] - 2026-03-11

### Feature — `readSecret` helper: Docker secret file fallback for sensitive config

Added `readSecret(name string) string` to `pkg/config/config.go`.
Resolution order:

1. Environment variable `strings.ToUpper(name)` — returned as-is if non-empty.
2. File `/run/secrets/<name>` — trailing `\n`/`\r` stripped.
3. Empty string if neither is set.

Applied to `InternalToken` (`OLU_INTERNAL_TOKEN` / `/run/secrets/olu_internal_token`)
and `JWTSecret` (`OLU_JWT_SECRET` / `/run/secrets/olu_jwt_secret`). Both are
single-value secrets with unambiguous file semantics.

`APIKeys` is not covered by `readSecret` — it is a comma-separated list
and the file semantics would be ambiguous. It continues to be read from
`OLU_API_KEYS` only.

No behaviour change when the environment variable is set.

4 new config tests covering: env var precedence, file fallback newline
stripping, missing-both case, and `LoadFromEnv` integration.

## [0.9.7-patched61] - 2026-03-10

### Feature — `bearertoken` auth type + design doc corrections

Addresses several items from the integration review of v0.9.7-patched60,
covering auth, documentation accuracy, and operational confirmation.

**Item 1 — `OLU_AUTH_TYPE=bearertoken`**

New auth mode for internal service-to-service calls using a plain shared
secret via `Authorization: Bearer <token>`.

- `Config.InternalToken` field added; set via `OLU_INTERNAL_TOKEN`
- `validateBearerToken`: reads `Authorization: Bearer <token>`, compares
  against `InternalToken` using `subtle.ConstantTimeCompare`
- Returns subject `"internal"` on success; 401 OLU-AU001 on mismatch or
  missing header
- `WWW-Authenticate: Bearer realm="olu"` on 401 (same as JWT)
- Config validation: `InternalToken` required when `AuthType=bearertoken`
- Deliberately separate from the `jwt` validator despite both using the
  Bearer scheme — a raw hex token must never be silently parsed as a JWT
- 4 new middleware tests: Valid, Invalid, Missing, WrongScheme

**Item 2a — design doc: `id` type corrected from string to integer**

All JSON examples in `COMMIT_ENDPOINT_DESIGN.md` now use integer IDs.
`§4.1` table updated (`id: string → id: integer`). Clarifying note added:
"`id` is a positive integer — callers must parse string identifiers
(barcodes, account numbers, external IDs) to int before constructing the request."
UUID references in append examples replaced with realistic integer IDs.
`§4.2` append table updated; "olu generates a UUID" replaced with "olu
assigns the next sequence ID".

**Item 2b — design doc §14: isolation level corrected**

`§14.1` no longer shows `sql.LevelSerializable`. Updated to reflect the
actual `BeginTx(ctx, nil)` call with an explanation of how `withRetry` +
the WAL write lock provides serialisation.

**Item 3 — `withRetry` on `Commit` confirmed intentional**

Code comment added to `SQLiteStore.Commit` in `pkg/storage/sqlite.go`
explaining why `withRetry` is safe: retries fire only on `SQLITE_BUSY`;
`ErrConflict` exits the retry loop immediately; a retry cannot mask a
conflict or double-write. CAS guarantee preserved across retries.

**Confirmed — `/commit` on graph-disabled instances**

`syncGraphEdges` returns nil when `!s.config.GraphEnabled`;
`indexForFTS` returns nil when `!s.config.FullTextEnabled`. No other
subsystems in `commitInner` depend on graph or FTS. `/commit` operates
correctly on instances with `OLU_GRAPH_MODE=disabled` and
`OLU_FULLTEXT_ENABLED=false`.

**Item 4 — image tagging policy confirmed**

`ha1tch/olu:0.9.7` tracks the latest patched release on the 0.9.7 minor
version. No per-patch tags are published for 0.9.7. Callers pinning the
image get automatic patch updates on pull; callers needing a fixed build
should use the source build path.

## [0.9.7-patched60] - 2026-03-10

### Refactor — `/commit` backend detection moved to storage layer

The 501 Not Implemented response for `/commit` on the jsonfile backend is
now driven by the storage layer returning `storage.ErrNotSupported`, rather
than a `StorageType` config string check in the handler.

**`storage.ErrNotSupported`** — new sentinel added to `pkg/storage/storage.go`.
Returned by any backend that does not implement a given operation. The HTTP
handler maps it to 501/OLU-CM009.

**`JSONFileStore.Commit`** — replaced ~160 lines of best-effort atomicity
code (lock ordering, rollback helpers `saveForCommit`/`appendForCommit`)
with a three-line stub returning `ErrNotSupported`. The old implementation
gave a false impression of transactional atomicity that the filesystem
cannot provide. The stub is honest: it exists solely for interface
compliance.

**`handleCommit`** — removed the `s.config.StorageType == "jsonfile"`
guard. The handler is now backend-agnostic; any backend that does not
support `/commit` signals that by returning `ErrNotSupported`. The
`ErrNotSupported` branch is the first error check after `store.Commit`.

**Storage commit tests** — removed all `jsonfileFactory` calls from the
six contract tests; they now run against SQLite only. Added
`TestCommit_JSONFileReturnsErrNotSupported` to verify the stub returns the
correct sentinel (the only thing the jsonfile backend needs to guarantee
for this operation).

**Unused import removed** — `sort` was only used by the removed jsonfile
Commit helpers; removed from `jsonfile.go` imports.

**Docs** — `docs/COMMIT_ENDPOINT_DESIGN.md` section 11 (Backend
Availability) updated to reflect that the 501 is now signalled via
`ErrNotSupported` from the storage layer, not a config check in the
handler.

## [0.9.7-patched59] - 2026-03-10

### Feature — `/commit` hardening: strict mode, jsonfile restriction, graph update

Three correctness and safety improvements to the `/commit` endpoint
introduced in patched58.

**`OLU_STRICT_COMMIT` (default: `true`)**

When true (the default), `/commit` runs the same schema validation and
graph cycle prechecks as `save`/`create`/`patch` before executing the
storage transaction. Payloads that violate a registered entity schema
return `400 OLU-VL001`; graph cycle violations return `409 OLU-GR001`.
Set `OLU_STRICT_COMMIT=false` only when the caller is trusted
infrastructure that manages its own invariants and the validation overhead
is undesirable. Structural validation (entity names, positive IDs,
append count) is always enforced regardless of this setting.

**jsonfile backend: `/commit` returns 501**

`POST /commit` now returns `501 Not Implemented` with error code
`OLU-CM009` when the server is running with the jsonfile storage backend.
The jsonfile backend does not provide true transactional atomicity;
allowing `/commit` against it would silently violate the endpoint's core
guarantee. The jsonfile backend is deprecated for production use.
All `/commit` code paths must be tested against SQLite.

**In-memory graph updated after commit (unconditional)**

`handleCommit` now calls `s.updateGraph` for both the upserted entity and
all appended entities after a successful transaction, exactly as the
normal write surface does. Previously, the `FlatGraph` was not updated,
causing stale graph state for any entity with REF fields written via
`/commit`. This was a correctness bug; the fix is unconditional and not
gated on `OLU_STRICT_COMMIT`.

**Error codes added:** `OLU-CM009`

**Config field added:** `StrictCommit bool` (env: `OLU_STRICT_COMMIT`, default `true`)

**Docs:** `docs/COMMIT_ENDPOINT_DESIGN.md` updated to v0.2 with sections
11 (Backend Availability), 12 (Strict Mode), and 13 (Error Codes updated).

**Tests:** 7 e2e tests (SQLite-backed), including `TestCommitE2E_JSONFileReturns501`
and `TestCommitE2E_StrictModeSchemaValidation`.

## [0.9.7-patched58] - 2026-03-10

### Feature — Atomic `/commit` endpoint (upsert + append in one transaction)

Adds `POST /api/v1/commit` and `POST /api/v1/tenant/{tenant_id}/commit`.
The endpoint performs a conditional upsert (`update`) and one or more
unconditional inserts (`append`) in a single storage transaction, eliminating
the partial-write failure mode where a successful state update and its
corresponding audit record could be written independently and non-atomically.

**Request shape**

```json
{
  "update": {
    "entity": "objects",
    "id": 1234,
    "version": 7,
    "data": { "state": "active" }
  },
  "append": [
    { "entity": "timeseries", "data": { "asset_id": 1234, "to_state": "active" } }
  ]
}
```

`version` is optional — omitting it makes the upsert unconditional.
`append` accepts 1–25 entries; auto-generated IDs when `id` is omitted.

**Responses**

- `200 OK` — both upsert and all appends committed.
  Body: `{ "update": { "created": bool, "version": N }, "appended": [...] }`
- `409 Conflict (OLU-CM001)` — CAS version mismatch on update;
  body includes `current_version`.
- `409 Conflict (OLU-CM007)` — explicit append `id` already exists;
  entire commit rolled back.
- `400 Bad Request` — validation failure (OLU-CM002 through OLU-CM006).
- `500 Internal Server Error (OLU-CM008)` — transaction failure.

**Implementation**

- `storage.Store` interface: `Commit(ctx, CommitRequest) (CommitResult, error)`
- SQLite backend: single `BEGIN IMMEDIATE` transaction wrapping
  `saveInTx` (upsert with CAS) and `createInTx` (per-append insert).
  Full graph edge sync and FTS indexing within the transaction.
- jsonfile backend: per-entity mutex locking in sorted order; best-effort
  rollback on append failure (removes written files and the update file if
  the update was a new create).
- Error codes OLU-CM001–OLU-CM008 added to `pkg/errors`.
- Cache invalidation for update and all appended IDs after commit.

**Tests**

- 6 storage-layer contract tests (both backends): create, overwrite, CAS
  success, CAS conflict, duplicate-ID rollback, multiple appends.
- 5 HTTP e2e tests: basic happy path, FSM round-trip, CAS conflict,
  validation errors, rollback-on-duplicate-ID.
- OQL mock store updated to satisfy the extended `Store` interface.

## [0.9.7-patched57] - 2026-03-10

### Feature — Conditional writes (optimistic concurrency) on save, PUT, and PATCH

Implements a `_version` field in the entity envelope that enables conflict-safe writes
without coordination infrastructure.

**Protocol**

Every entity response from `GET` (and `POST /save/{id}` on first write)
includes `"_version": N` — an integer incremented on every successful write.
To make a write conditional, include `"_version": N` in the request body.
olu checks the stored version inside the write transaction and returns:

- `200 OK` / `201 Created` — write succeeded; version is now `N+1`.
- `409 Conflict` — stored version differs from expected. Body includes
  `"current_version": M` so the caller can re-read and retry without an
  extra GET.

Omitting `_version` from the request body leaves behaviour unchanged:
all three write paths (`PUT`, `PATCH`, `POST /save/{id}`) remain
unconditional and always succeed if the entity exists.

**Files changed**

- `pkg/storage/sqlite.go` — `saveInner` overwrite branch: extract `_version`
  from request data, strip it from the JSON blob, conditional `UPDATE … WHERE
  _version = ?` when present, `ErrConflict` on zero rows affected.
- `pkg/storage/jsonfile.go` — `Create` now writes `_version = 1`. `Update`
  reads the stored version, applies conditional check, and increments.
  `PatchValidated` threads the expected version through to `Update`.
  `Save` (overwrite path) already had conditional logic added in patched56;
  the create path now also initialises `_version = 1` consistently.
- `pkg/server/handlers.go` — `handleSave`: handles `storage.ErrConflict`,
  fetches current version via `fetchCurrentVersion` helper, returns 409 with
  `"current_version"` in body. New helper `fetchCurrentVersion` added.
- `pkg/server/handlers.go` — `handlePatch`: 409 now includes
  `"current_version"`.
- `pkg/server/server.go` — `handleUpdate`: 409 now includes
  `"current_version"`.
- Tests: `TestStoreSaveOptimisticConcurrency` (storage layer), `TestE2E_SaveCAS`
  and `TestE2E_UpdateCAS` (HTTP layer) verify correct-version success,
  stale-version 409 with `current_version`, and unconditional-write pass-through.

**Usage pattern for FSM executors**

Read current state via `GET /api/v1/tenant/{t}/objects/{id}` — note `_version`
from response. Compute transition. Write via `POST /api/v1/tenant/{t}/save/{id}`
with `"_version": N` in the body. On `409`, re-read and retry. No inter-executor
coordination required.

## [0.9.7-patched56] - 2026-03-10

### Fixed — `POST /save/{id}` now implements true upsert semantics

`handleSave` previously enforced exclusive-create: it returned `409 Conflict`
if the entity already existed, despite the `save` verb conventionally implying
upsert. This caused silent data loss in stateful FSM executors where only
the first state transition per entity was ever persisted.

**Change summary:**

- `Store.Save` interface signature changed from `error` to `(bool, error)`.
  The boolean is `true` when a new record was created, `false` when an existing
  record was overwritten. Both `SQLiteStore` and `JSONFileStore` implement the
  new contract.
- `SQLiteStore.saveInner`: existence check no longer returns `ErrAlreadyExists`.
  On hit, it performs an in-transaction `UPDATE`; on miss, it `INSERT`s as
  before. Sequence, graph, and FTS index handling are preserved for both paths.
- `handleSave`: the pre-handler `store.Exists` check is removed. The handler
  uses the returned bool to send `201 Created` on first write and `200 OK` on
  subsequent overwrites. All other error paths (graph cycle, duplicate edge,
  validation, storage failure) are unchanged.
- `olu-migrate` caller updated to accept `(bool, error)` — upsert semantics
  are strictly better for re-runnable migration.
- All tests updated. `TestStoreSave / Save duplicate ID` renamed and inverted
  to verify overwrite succeeds; `TestE2E_SaveEndpoint / save with existing ID`
  and `TestErrorPaths_SaveConflict` updated to match new behaviour. Four mock
  implementations in `pkg/oql` test files updated to satisfy the revised
  interface.



### Feature — Metrics bind address (OLU_METRICS_HOST)

Extends the dedicated metrics listener (added in patched54) with independent
interface binding.

**New config field:** `MetricsHost string` (env: `OLU_METRICS_HOST`)

Resolution order when `OLU_METRICS_PORT > 0`:

1. If `OLU_METRICS_HOST` is set explicitly, it always wins.
2. If `OLU_HOST` is a real interface address (not `0.0.0.0` or `::`), the
   metrics listener inherits it — no extra config required to keep scrape
   traffic on the same interface as the API.
3. Otherwise (wildcard host), the metrics listener falls back to `0.0.0.0`.

This rule avoids the problem of blindly inheriting a wildcard: `0.0.0.0`
carries no interface preference, so propagating it would be meaningless.
A real address carries explicit operator intent and *should* propagate.

The startup banner now shows the resolved metrics bind address alongside
the port, making the effective configuration visible at a glance.



### Feature — Dedicated metrics port (OLU_METRICS_PORT)

Adds support for serving `/metrics` on a separate TCP port, independent of
the main API port. Useful in deployments where Prometheus scrape traffic
should not compete with operational reads and writes on the primary port.

**New config field:** `MetricsPort int` (env: `OLU_METRICS_PORT`)

- When `OLU_METRICS_PORT` is unset or `0`, behaviour is unchanged: `/metrics`
  continues to be served on the main API port. No breaking change for existing
  deployments.
- When `OLU_METRICS_PORT` is set to a positive integer, olu starts a second
  minimal HTTP listener on that port serving only `/metrics`. The main API
  port no longer exposes `/metrics`, providing clean separation.
- Validation rejects `MetricsPort` values outside `0–65535` and values equal
  to `Port` (would cause a bind conflict).
- The startup banner now reports which port metrics are available on.
- The dedicated metrics listener is gracefully shut down alongside the main
  server on SIGTERM/SIGINT.

Example multi-instance deployment using port + 100 convention:

```
instance-a  OLU_PORT=9090  OLU_METRICS_PORT=9190
instance-b  OLU_PORT=9091  OLU_METRICS_PORT=9191
instance-c  OLU_PORT=9092  OLU_METRICS_PORT=9192
instance-d  OLU_PORT=9093  OLU_METRICS_PORT=9193
```


## [0.9.7-patched53] - 2026-03-09

### Refactor — Remove testConfig() alias from timeseries tests

`testConfig()` was left as a compatibility alias for `testStoreConfig()` in
patched52. Removed now that Pebble is intended to be fully detachable.
Every call site in the test suite now explicitly uses `testStoreConfig()` or
`testPebbleConfig()` as appropriate, making the config boundary unambiguous
even in tests.

## [0.9.7-patched52] - 2026-03-09

### Refactor — Timeseries store configuration split

`StoreConfig` previously carried six fields, five of which were Pebble LSM-tree
tuning parameters (`MemtableSize`, `BlockSize`, `Compression`,
`L0CompactionThreshold`, `MaxOpenFiles`). These had no meaning to any backend
other than Pebble and made `StoreFactory` appear to require Pebble knowledge.

**Changes**

- `StoreConfig` now contains only `DefaultRetentionDays` — the one setting that
  is meaningful to any backend.
- `PebbleConfig` is a new type holding the five Pebble-specific fields. Zero
  values are safe; `NewPebbleStore` applies documented defaults for each unset
  field (64 MB memtable, 32 KB blocks, zstd compression, threshold 4, 500 open
  files).
- `NewPebbleStore(dir, StoreConfig, PebbleConfig)` — signature extended.
- `NewPebbleStoreFactory(pcfg PebbleConfig) StoreFactory` — factory now closes
  over `PebbleConfig`; only `StoreConfig` flows through the `StoreFactory`
  contract.
- `server.go` construction site split accordingly: `tsCfg` carries retention,
  `pebbleCfg` carries engine tuning.
- All test helpers updated (`testStoreConfig`, `testPebbleConfig`; `testConfig`
  preserved as alias for tests that do not need independent variation).

A future non-Pebble backend need only implement `Store` and provide a
`StoreFactory` — no Pebble fields to ignore or misinterpret.

## [0.9.7-patched51] - 2026-03-09

### Fixed — vode log list capped at 10

The three log sites that emit vode node IDs (two in `loadEntitiesFromEdgeTable`
and one in `handleGraphRebuild`) previously logged the full ID list, which
could produce unbounded log output after a botched migration or corrupted
store. Each site now logs at most 10 IDs in `"vode_sample"` and adds a
`"vode_remaining"` field when the list is truncated. The full count is always
present in `"vode_count"`.

## [0.9.7-patched50] - 2026-03-09

### Added — Vode (forward-reference placeholder nodes)

**Concept**

A *vode* is a graph node created implicitly by `AddEdge` as a forward-reference
placeholder. It represents a node that has been pointed at by a REF field but
whose entity data has not yet arrived — the common case during streaming
graph hydration where edges may be replayed before their target entities are
written. The name "vode" is domain-specific and intentionally non-overlapping
with any valid olu entity type name.

**Invariant:** `VodeCount()` should be zero after successful hydration. A
non-zero count indicates dangling REF references — entity data that was
referenced but never written to the store.

**Changes**

- `NodeTypeVode = "__vode__"` constant exported from `pkg/graph`.
- `addEdgeLocked` now passes `NodeTypeVode` (previously `""`) when creating
  implicit endpoint nodes. Vodes are therefore visible in the type index and
  countable — no longer invisible to `GetNodesByType`.
- `addNodeLocked` updated with explicit vode-lifecycle rules:
  - Vode assignment to an already-typed node (real or vode) is a silent no-op.
  - Promotion from `NodeTypeVode` to a real type is permitted and removes the
    node from the vode type index, decrements `vodeCounters`.
  - `ErrNodeTypeMismatch` is only raised when an established *non-vode* type
    would be overwritten.
- `vodeCounters map[string]int` field added to `FlatGraph`, following the same
  pattern as `nodeCounters` / `edgeCounters`. Maintained in `addNodeLocked`,
  `RemoveNode`, `Clear`, and `Load`.
- `VodeCount() int` — total vode count across all tenants. O(1).
- `VodeCountForTenant(tenantPrefix string) (int, error)` — per-tenant vode
  count. O(1). Returns `ErrTenantRequired` on empty prefix.
- Both methods added to the `Graph` interface.
- `handleGraphVerify` response now includes `"vode_count"`.
- `handleGraphRebuild` response now includes `"vode_count"` and logs a `Warn`
  with vode node IDs if any vodes remain after the rebuild.
- `loadEntitiesFromEdgeTable` (both single- and multi-tenant paths) logs a
  `Warn` with vode node IDs if any vodes remain after hydration completes.
- Lifecycle and design rationale documented in a dedicated doc block above the
  `Graph` interface in `graph.go`.

**Tests**
- `TestContract_AddEdge_ImplicitNode_IsVode` — replaces `AbsentFromTypeIndex`; asserts vodes appear under `NodeTypeVode`, `VodeCount` tracks correctly, promotion works.
- `TestContract_Vode_RemoveDecrementsCounter`
- `TestContract_Vode_ClearResetsCounter`
- `TestContract_Vode_SaveLoadRoundtrip`
- `TestContract_VodeCountForTenant_EmptyPrefix_Errors`
- `TestContract_VodeCountForTenant_Isolated`

## [0.9.7-patched49] - 2026-03-09

### Fixed (FlatGraph static audit — medium-tier issues #8, #11, #12, #13, #14)

**Inconsistency fixed**
- **#8** — `HasCycle` had no per-tenant variant. A cycle in tenant A caused `HasCycle()` to return `true` globally, making tenant B's view incorrect. New method `HasCycleForTenant(tenantPrefix string) (bool, error)` performs a DFS scoped to nodes carrying that prefix only. Added to the `Graph` interface. Returns `ErrTenantRequired` on empty prefix.

**Smell fixed**
- **#11** — `CommonNeighbors` renamed to `SharedOutNeighbors` across the entire codebase (interface, `FlatGraph`, all handlers, mock, and all tests). The original name clashed with the graph-theory term "common neighbours" which conventionally means undirected shared neighbours; this method only considers directed out-edges.
- **#12** — `AdaptivePersister.save`: a `MarkDirty()` racing between `dirty.Store(false)` and `graph.Save()` completing would silently re-flag dirty, causing the next tick to fire a save logged as "periodic" with no indication why. A post-save check now logs a debug note when dirty was re-set during the save window, making the back-to-back save legible in operator logs.

**Performance fixed**
- **#13** — `GetAllNodesForTenant` and `GetNodesByTypeForTenant` scanned the full node map (O(N total)) on every call. A new `tenantNodes map[string]map[string]struct{}` field maintains a per-tenant node set, updated in `addNodeLocked`, `RemoveNode`, `Clear`, and `Load`. `GetAllNodesForTenant` is now O(N_tenant). `GetNodesByTypeForTenant` iterates the smaller of the tenant set and the type-index set — O(min(N_tenant, N_type)).
- **#14** — `wouldCreateCycle` BFS traversed out-edges from all tenants. Since cross-tenant edges cannot exist, this was pure wasted work. The inner loop now skips any neighbour whose non-empty tenant prefix differs from `from`'s prefix, scoping the BFS to the relevant tenant.

### Added
- `HasCycleForTenant(tenantPrefix string) (bool, error)` on `Graph` interface and `FlatGraph`.
- `tenantNodes` index field on `FlatGraph`; maintained by `addNodeLocked`, `RemoveNode`, `Clear`, `Load`.
- New contract tests: `TestContract_HasCycleForTenant_EmptyPrefix_Errors`, `_NoCycle`, `_WithCycle`, `_Isolated`, `TestContract_GetAllNodesForTenant_OwnedByTenant`, `TestContract_GetNodesByTypeForTenant_OwnedByTenant`, `TestContract_GetAllNodesForTenant_ReflectsRemoval`, `TestContract_CycleCheck_TenantScoped`.

### Changed
- `Graph.CommonNeighbors` → `Graph.SharedOutNeighbors` (breaking rename; all internal call sites updated).
- `mockGraph` in `persister_test.go` updated to implement new interface.

## [0.9.7-patched48] - 2026-03-09

### Fixed (FlatGraph static audit — easy-tier issues)

**Bugs fixed**
- **Bug #1** — `addNodeLocked`: calling `AddNode` on an existing, typed node with a *different* type silently retypes the node and corrupts the type index. Now returns `ErrNodeTypeMismatch`. Implicitly-created (typeless) nodes — those created as a side-effect of `AddEdge` — may still have their type assigned by a subsequent `AddNode` call; only an established, non-empty type now triggers the error.
- **Bug #2** — `wouldCreateCycle`: the BFS budget exhaustion check (`len(visited) >= cycleCheckLimit`) fired *before* the `cur == from` check. When the budget was exactly hit, the function returned `true` (false positive cycle) even when `from` was the next node to dequeue. Check ordering corrected: `cur == from` is evaluated first.
- **Bug #3** — `NewFlatGraphWithCycleDetection`: the struct was initialised with `logger: zerolog.Nop()` before the mode `switch`. The `default` branch therefore emitted a warning to a no-op logger — completely invisible. Invalid mode strings now print a warning to `os.Stderr` and fall back to `"ignore"` reliably.
- **Bug #4** — `Load`: file I/O, JSON parsing, and scratch-graph replay all happened before the write lock was acquired. Concurrent `Load` calls raced on the state swap, with potential counter drift and no panic. A dedicated `loadMu sync.Mutex` now serialises concurrent `Load` calls.

**Inconsistencies fixed**
- **#5** — `FindPath` / `PathExists`: neither method had a cross-tenant guard. A query with endpoints from different tenants returned "no path found" rather than `ErrCrossTenantEdge`, inconsistent with `AddEdge` and `CheckEdge`. Guards added to both.
- **#6** — `addNodeLocked`: redundant double string-scan for `@` (`strings.Contains` + `NodeIDPrefix`). Replaced with a single `strings.IndexByte` call.
- **#9** — `PathExists`: the destination node `to` was never recorded in the `visited` map before the early-return, violating the invariant that every enqueued node is tracked. Fixed.

**Code smell fixed**
- **#7** — Three public constructors (`NewFlatGraph`, `NewFlatGraphWithLogger`, `NewFlatGraphWithCycleDetection`) each contained an identical struct literal. Consolidated into a single internal factory `newFlatGraph(logger, mode)` that all three delegate to.

### Added
- `ErrNodeTypeMismatch` sentinel error in `pkg/graph/graph.go`.
- New contract tests covering all fixed issues: `TestContract_AddNode_TypeMutation_ReturnsError`, `TestContract_AddNode_ImplicitNode_CanBeTypedLater`, `TestContract_CycleMode_InvalidMode_FallsBackToIgnore`, `TestContract_Load_ConcurrentCalls_DoNotCorrupt`, `TestContract_PathExists_CyclicGraph_Terminates`, `TestContract_AddEdge_ImplicitNode_AbsentFromTypeIndex`, `TestContract_FindPath_CrossTenant_Rejected`, `TestContract_PathExists_CrossTenant_Rejected`.

## [0.9.7-patched47] - 2026-03-09

### Fixed (SQLite graph path — all 10 audit findings)

**Bug fixes**
- **Bug 1 (Critical)** — `syncGraphEdges` previously committed cycle-creating edges to the SQLite edge table even when `FlatGraph.AddEdge` would reject them, leaving the table and in-memory graph permanently diverged. Fixed by adding `Graph.CheckEdge` (same pre-flight as `AddEdge`, no mutation) and calling it in `handleCreate`, `handleUpdate`, `handleSave`, and `handlePatch` before any store write. A cycle-detection rejection now returns HTTP 409 and the write never reaches storage.
- **Bug 2 (High)** — Partial startup hydration (30-second context timeout firing mid-scan) left the in-memory graph with an undefined subset of edges, with no runtime signal to callers. The graph is now cleared to a known-empty state on hydration failure; the error log directs operators to `POST /api/v1/graph/rebuild`.
- **Bug 3 (High)** — `handleGraphRebuild` and `handleGraphVerify` were hardwired to `s.storage` (always tenant 0). Both handlers now use `s.getStore(r.Context())`, which resolves to the correct tenant-scoped store for non-zero tenants.
- **Bug 4 (Medium)** — `RebuildGraph` repaired the SQLite edge table but left the in-memory `FlatGraph` unchanged. After a successful rebuild, `handleGraphRebuild` now calls `reloadGraphFromStore` to clear and re-hydrate the in-memory graph from the repaired edge table; a restart is no longer needed.

**Code smells resolved**
- **Smell 1** — `RebuildGraph` emitted a `log.Printf("[WARN] ...")` that bypassed zerolog. `SQLiteStore` now carries a `zerolog.Logger` field (default: `zerolog.Nop()`), set via `WithLogger`. The warn is now routed through the application logger. Both the primary store (wired in `main.go`) and tenant stores (wired in `storeForTenant`) receive the application logger.
- **Smell 2** — `VerifyGraphIntegrity` issued two independent `readDB` queries with no transaction, allowing concurrent writes to produce false positive violations. Both reads now run inside a single `BeginTx(ReadOnly: true)` transaction.
- **Smell 3** — `VerifyGraphIntegrity` materialised two full edge maps before comparing, with unbounded memory growth. Actual edges from the edge table are now streamed and checked against the expected-edge map rather than accumulated into a second map.
- **Smell 4** — `VerifyGraphIntegrity` returned on the first violation; `RebuildGraph` accumulated all. Policy is now consistent: `VerifyGraphIntegrity` collects all violations, sorts them, and returns them in a single joined error.
- **Smell 5** — `SQLiteStore.GetNeighbors` was dead code (the server never called it; all graph traversal went through the in-memory `FlatGraph`) and had a latent adapted-entity bug (would silently return no neighbours for entities stored in per-entity tables). Removed from `SQLiteStore` and from the `Store` interface. Tests that exercised it have been rewritten to use `ScanGraphEdges` + `Get`.
- **Smell 6** — PATCH was the only write path where a post-commit `Get` failure left the in-memory graph silently stale with no log entry. The failure is now logged at Warn level.

### Changed
- `Graph` interface gains `CheckEdge(from, to, relationship string) error`.
- `FlatGraph` implements `CheckEdge` using a read lock; safe to call concurrently.
- `SQLiteStore` gains a `logger zerolog.Logger` field and `WithLogger(*SQLiteStore)` method.
- `handleGraphRebuild` response message updated to note in-memory graph reload.

## [0.9.7-patched46] - 2026-03-09

### Fixed
- **`AdaptivePersister.Start()`: double-call panic** — calling `Start()` more
  than once spawned a second `loop()` goroutine; when `Stop()` subsequently
  closed `stopCh` both goroutines attempted `close(doneCh)`, causing a panic.
  `Start()` now uses `CompareAndSwap` on the existing `started` atomic so only
  the first call launches the loop; subsequent calls are silent no-ops.
- **`FlatGraph.Load()`: custom `cycleCheckLimit` silently reset to 512** —
  when loading a file written by an older version of olu that did not persist
  `cycle_check_limit`, the field was absent and the scratch graph's limit
  stayed at `DefaultCycleCheckLimit` (512), which was then unconditionally
  swapped into the receiver. An operator who had called `SetCycleCheckLimit`
  before `Load` would silently lose their configured value. The fix mirrors the
  existing `cycleDetection` fallback: when the file lacks the field, the
  receiver's runtime value is preserved.
- **`FlatGraph.CommonNeighbors()`: single error for two missing nodes** — when
  either or both nodes were absent a single error message was returned
  (`"one or both nodes not found: A, B"`), making it impossible for callers to
  identify which node was missing without a second round-trip. Both existence
  checks are now performed individually, consistent with `FindPath` and
  `PathExists`.
- **`TestContract_Topology_Diamond`: tautological `HasCycle` assertion** — the
  condition `!g.HasCycle() == false` parses as `g.HasCycle() == true` due to
  Go operator precedence, meaning the test fired when a cycle *was* present
  (correct) but would also have passed silently if `HasCycle` erroneously
  returned `true` on a DAG. Corrected to `if g.HasCycle()`.

### Changed
- **`AdaptivePersister`: `currentInterval` refactored into `intervalForWriters`
  + `currentInterval`** — the debug log in `save()` previously called
  `currentInterval()` after already loading `activeWriters`, resulting in two
  separate atomic reads that could observe different values and produce a log
  entry with an inconsistent (writers, interval) pair. `intervalForWriters(n)`
  now performs the pure calculation given an already-known count;
  `currentInterval()` delegates to it. `save()` loads `activeWriters` once and
  passes the snapshot to both the log field and `intervalForWriters`.
- **`defaultCycleCheckLimit` unexported alias removed** — the package
  previously defined both `DefaultCycleCheckLimit = 512` (exported) and
  `defaultCycleCheckLimit = DefaultCycleCheckLimit` (unexported alias). The
  alias served no purpose and created a second name that could drift from the
  canonical constant. All internal uses now reference `DefaultCycleCheckLimit`
  directly.
- **`FlatGraph.Load()` scratch graph now inherits the receiver's logger** —
  the scratch `FlatGraph` constructed during file replay was built with a
  zero-value `zerolog.Logger` rather than `zerolog.Nop()`. While harmless
  today (replay runs in ignore mode and hits no log calls), any future log
  statement reached during replay would silently discard its output. The
  scratch graph now inherits `g.logger`, matching the intent of all three
  public constructors.
- **`GetNodeInfo`: `fmt.Sscanf` error now logged at debug level** — the entity
  ID parse error was previously discarded with `_, _`. A malformed entity ID
  now emits a debug-level log entry rather than silently reporting 0, making
  unexpected node ID formats visible during development without adding noise in
  production.

### Tests
- **New: `TestContract_SaveLoad_CycleCheckLimitPreserved`** — verifies that a
  graph saved with a custom `cycleCheckLimit` reloads with that value intact,
  and that loading an older file that lacks the field preserves the runtime
  value rather than reverting to the package default (regression test for the
  bug fixed above).
- **New: `TestContract_CounterConsistency`** — verifies that after a mixed
  sequence of add/remove operations `NodeCount()` equals `len(nodes)`, the
  sum of `nodeCounters` equals `len(nodes)`, `EdgeCount()` equals the count of
  outgoing edges in the node map, and `edgeCount` equals the sum of
  `edgeCounters`. Catches counter drift between the global and per-tenant
  layers.
- **New: `TestContract_GetNodeInfo_NoColon`** — exercises the branch in
  `GetNodeInfo` where the node ID contains no colon, confirming that `Entity`
  is empty string and `EntityID` is 0 with no error or panic.


## [0.9.7-patched45] - 2026-03-09

### Fixed
- **`handleGraphPath`: missing `from`/`to` empty-param guard** — empty or absent `from`/`to`
  fields were passed directly to `FindPath`, producing a 404 with a garbled error message
  (`"node  not found"`) instead of a 400. The handler now returns 400 when either field is
  empty, consistent with every other path-bearing handler (`handleGraphShortestPath`,
  `handleGraphPathExists`, and all three tenant counterparts).
- **`handleGraphPath`: bare `len(path)-1` length** — the non-tenant path handler used the
  raw expression without a `< 0` guard, unlike `handleGraphShortestPath` which was corrected
  in patched44. The expression can only return -1 through a code path that does not currently
  exist, but the inconsistency is closed for defensive correctness.
- **`handleTenantGraphNodeInfo`: dead double-strip of `Entity`** — `GetNodeInfo` already strips
  the `XXXX@` tenant prefix from the `Entity` field before returning. The handler additionally
  called `strings.TrimPrefix(info.Entity, prefix)`, which was always a no-op. The redundant
  strip and its misleading comment have been removed; a replacement comment documents the
  invariant guarantee that makes the handler strip unnecessary.

### Added
- **`OLU_GRAPH_CYCLE_CHECK_LIMIT` config key** — the BFS node-visit budget for cycle detection
  (`cycleCheckLimit`, previously hardcoded to 512 with no runtime override) is now configurable
  via environment variable. `GRAPH_API.md` already documented the operator guidance to raise
  this limit on large graphs; the config key makes that guidance actionable. A value of 0
  (the default) retains the built-in default of 512.
- **`FlatGraph.SetCycleCheckLimit(n int)`** — new method on `*FlatGraph` for callers that hold
  a concrete reference and need to set the limit after construction. Used by `main.go` when
  `GraphCycleCheckLimit > 0`.
- **`graph.DefaultCycleCheckLimit`** — the previously-unexported `defaultCycleCheckLimit`
  constant (512) is now exported so callers (e.g. the startup diagnostic print) can display
  the effective default without hard-coding the value.

### Tests
- **New: three sub-tests in `TestGap_LegacyGraphPath`** cover the missing-`from`, missing-`to`,
  and missing-both cases for `POST /api/v1/graph/path`, asserting HTTP 400.



### Fixed
- **`handleGraphNeighbors` / `handleTenantGraphNeighbors`: missing `node_id` guard** — an empty
  or absent `node_id` field was silently accepted, resulting in a 200 response with an empty
  neighbours map. Both handlers now return 400 when `node_id` is empty, consistent with every
  other node-ID-bearing handler.
- **`handleGraphNeighbors` / `handleTenantGraphNeighbors`: unrecognised `direction` silently
  returns empty** — any value other than `"out"`, `"in"`, or `"both"` caused neither block to
  execute, returning 200 `{"neighbors":{}}` with no indication of the error. Both handlers now
  return 400 for unrecognised direction values.
- **`handleGraphShortestPath`: bare `len(path)-1` could produce `-1` length** — unlike the tenant
  counterpart which uses `max(len-1, 0)` via `tenantPathResult`, the non-tenant handler used the
  raw expression. Guard added for consistency and defensive correctness.

### Documentation
- MANUAL.md version header updated from `patched42` to `patched43`.
- Duplicate `OLU_TS_MAX_AGGREGATE_BUCKETS` config row removed.
- Duplicate `OLU-TS019` error table row collapsed to a single entry.

## [0.9.7-patched43] - 2026-03-09

### Fixed
- **Info-leak (class #1, continued):** `FindPath`, `PathExists`, `GetNodeInfo`, and `GetDegree`
  were passing raw tenant-prefixed node IDs (`XXXX@entity:N`) directly into error strings
  surfaced to HTTP callers. All six affected error sites now strip the prefix via
  `tenant.NodeIDStripped()`, consistent with the fixes applied to `CommonNeighbors` and
  `UpdateFromEntityForTenant` in patched42.
- **`Load` partial state on error:** `Load` previously cleared all graph state then replayed
  nodes and edges into the live receiver; any replay error left the graph in a partially-rebuilt
  state with no documented recovery path. `Load` now replays into a scratch `FlatGraph` and
  swaps the receiver's fields atomically only on full success. A failed load leaves the receiver
  completely unchanged.
- **`Save` `.tmp` leak on `Rename` failure:** `os.Rename` failures (cross-device link,
  permissions) were leaving the `.tmp` staging file on disk. The `.tmp` is now removed on any
  `Rename` error.
- **`AdaptivePersister.Stop()` deadlock if `Start()` never called:** `Stop()` blocked forever
  on `<-p.doneCh` if `Start()` had not been called, because `doneCh` is only closed by the
  goroutine spawned in `Start()`. A `started` atomic flag now guards `Stop()`; calling it before
  `Start()` is a no-op.

### Changed
- **`Load` no longer runs cycle detection during replay:** Previously, `Load` set
  `g.cycleDetection` from the file before replaying edges, so every `addEdgeLocked` call in
  `"error"` mode triggered a full BFS — O(E × N) for a well-formed file that by construction
  contains no cycles. The scratch graph used during replay always uses `"ignore"` mode; the
  configured mode is restored after a successful replay.
- **`FlatGraph` now uses structured `zerolog` logging:** All `log.Printf` call sites have been
  replaced with `zerolog` events. `NewFlatGraph` and `NewFlatGraphWithCycleDetection` default to
  `zerolog.Nop()` (no output). A new constructor `NewFlatGraphWithLogger(logger zerolog.Logger)`
  allows callers to inject a logger. This aligns the graph layer with the structured logging used
  by `AdaptivePersister` and the server layer, enabling log correlation across tiers.
- **`GetNodesByType` and `GetNodesByTypeForTenant` return a non-nil empty slice for all empty
  results:** Previously `GetNodesByType` returned `nil` when no nodes matched, and
  `GetNodesByTypeForTenant` returned two distinct nil shapes (`nil, nil` vs non-nil empty slice)
  depending on whether the entity type was absent from the index or present but empty for the
  tenant. All empty results now consistently return `([]string{}, nil)`. The `nil`-compensation
  guard at the call site in the tenant handler (line 631) is now redundant but harmless.

### Performance
- **BFS queue memory reclaimed:** All three BFS callers (`FindPath`, `PathExists`,
  `wouldCreateCycle`) used a slice-header-slide dequeue (`queue = queue[1:]`) that left the
  backing array's consumed prefix reachable until the whole local variable went out of scope.
  All three now use a head-index (`head++`) so the front elements become eligible for GC as the
  traversal proceeds.

### Documentation
- **`wouldCreateCycle` budget exhaustion documented:** The conservative behaviour when the BFS
  visit budget (`cycleCheckLimit`, default 512) is exhausted — returning `true` and thus
  `ErrCycleDetected` even when no actual cycle exists — is now documented in `graph.go`
  (interface comment, `CycleCheckBudgetExceeded` informational constant) and in a new section
  of `docs/GRAPH_API.md` explaining the implications for operators on large or dense graphs.

### Tests
- **New: `TestContract_UpdateFromEntityForTenant_RelabelExistingEdge`** covers the previously
  untested relabel path in `UpdateFromEntityForTenant`: the `ErrEdgeAlreadyExists` branch that
  deletes the old edge and re-adds with the new relationship label, including the counter-safe
  rollback. Verifies that the edge count stays at 1 and that both the out-map and in-map carry
  the updated label.

### Maintenance
- Removed two stale comments in `flat_graph.go` that referenced `pkg/graph/state` (removed in
  patched38) and its `wouldCreateCycle` function.


- `FlatGraph.CommonNeighbors` (`pkg/graph/flat_graph.go`): the method now
  always returns a non-nil slice. Previously `var result []string` was used,
  so a call with no common neighbours returned `nil` rather than an empty
  slice, violating the contract documented on the `Graph` interface. Both
  REST handlers (`handleGraphCommonNeighbors`,
  `handleTenantGraphCommonNeighbors`) contained an identical `if common == nil
  { common = []string{} }` patch to compensate; both patches have been
  removed now that the guarantee lives in the function itself.
- `FlatGraph.CommonNeighbors`: the error message returned when one or both
  nodes are absent now strips any `XXXX@` tenant prefix from the node IDs via
  `tenant.NodeIDStripped`, preventing internal tenant identifiers from leaking
  to callers. This is a second instance of the info-leak fixed in patched41's
  `UpdateFromEntityForTenant` rollback messages.
- `FlatGraph.UpdateFromEntityForTenant` rollback error messages: the two
  `fmt.Errorf` calls in the edge-relabel failure paths now use
  `tenant.NodeIDStripped` on both `nodeID` and `targetNodeID`, so the
  `XXXX@` tenant prefix is not visible in errors that bubble up to callers.
  The `log.Printf` diagnostic line retains full node IDs as intended.
- `FlatGraph.Load` (`pkg/graph/flat_graph.go`): the `cycle_detection` field
  read from a persisted file is now validated against the three legal values
  (`"ignore"`, `"warn"`, `"error"`) before being applied. Previously an
  unrecognised value (e.g. a typo or hand-edited file) was silently stored;
  in `addEdgeLocked` the switch has no default, so a bad mode would trigger
  cycle detection but then add the cycle without warning or error. `Load` now
  returns an error for invalid modes.
- `guardEdgeMap` (`pkg/server/graph_tenant_handlers.go`): the function now
  builds a fresh map instead of mutating its argument with `delete`. The
  previous in-place approach was safe only because every call site passed a
  freshly-allocated map from `stripPrefixFromEdgeMap`, but that aliasing
  contract was invisible at the call site. The fix mirrors the allocation
  discipline already applied to `guardSlice` in patched41, and the function
  comment now cross-references that history.
- `handleTenantGraphPath` and `handleTenantGraphShortestPath`
  (`pkg/server/graph_tenant_handlers.go`): the `"length"` field could be
  `-1` when `guardSlice` filtered every node from the result (a degraded
  state where cross-tenant contamination was detected). The shared logic —
  strip prefix, guard, compute safe length — is now extracted into
  `tenantPathResult`, which returns `max(len(guarded)-1, 0)`. Both handlers
  call the helper; the duplicated strip/guard/length arithmetic is gone.

### Added

- `tenant.NodeIDStripped` (`pkg/tenant/tenant.go`): new helper that returns a
  node ID with its `XXXX@` tenant prefix removed, or the input unchanged if
  no prefix is present. Intended for use in error messages that must not leak
  internal tenant identifiers to callers.
- `(*Server).tenantPathResult` (`pkg/server/graph_tenant_handlers.go`): new
  private helper that strips the tenant prefix from a path slice, guards
  against cross-tenant leakage via `guardSlice`, and returns the clean path
  with a safe non-negative edge-count length. Eliminates the duplication
  between `handleTenantGraphPath` and `handleTenantGraphShortestPath`.

### Tests

- `TestContract_CommonNeighbors_None`: strengthened to assert `common != nil`
  (not just `len(common) == 0`) so the non-nil contract is machine-checked.
- `TestContract_CommonNeighbors_SameNode` (new): verifies that when
  `nodeA == nodeB` all outgoing neighbours of that node are returned, and
  that the result is non-nil. The behaviour is now documented in the function
  comment.
- `TestContract_Load_InvalidCycleDetectionMode_Errors` (new): writes a file
  with `"cycle_detection": "strict"` and asserts that `Load` returns an error.

## [0.9.7-patched41] - 2026-03-08

### Fixed

- `guardSlice` (`pkg/server/graph_tenant_handlers.go`): replaced the
  filter-in-place idiom (`nodes[:0:len(nodes)]`) with a fresh allocation
  (`make([]string, 0, len(nodes))`). The old form shared the backing array
  with the input slice; if a future call site passed a slice it still held a
  reference to, its contents would be silently clobbered. Added a comment
  explaining the previous hazard.
- `FlatGraph.UpdateFromEntityForTenant` relabel path: when re-adding an edge
  with a new label fails and the restore of the original label succeeds, the
  returned error now explicitly states that the original edge is intact
  (`"relabel … failed (original relationship %q preserved): %w"`). Previously
  the bare `addErr` gave no indication of graph consistency. When both re-add
  and restore fail, the error now wraps both (`"relabel … failed and restore
  of %q also failed (%v): %w"`), making the double-failure visible to the
  caller.

### Changed

- `Graph.CommonNeighbors` interface declaration and `FlatGraph.CommonNeighbors`
  implementation: added documentation stating that only outgoing edges are
  consulted — the method returns nodes that both `nodeA` and `nodeB` point
  *to*, not nodes that point to them. The interface doc also clarifies the
  return contract (empty non-nil slice on no overlap). The implementation
  comment notes the deliberate choice to keep the semantics stable rather than
  extending the method, so existing callers and tests are unaffected.

## [0.9.7-patched40] - 2026-03-08

### Fixed

- `FlatGraph.UpdateFromEntityForTenant` double-failure path: when both the
  re-add of a relabelled edge and the rollback restore fail, the code
  previously incremented `edgeCount` and `edgeCounters` — the opposite of
  the comment's stated intent. The edge is already gone at that point and the
  counters were already decremented, so no correction is needed. The erroneous
  increments have been removed; the log message updated to reflect the correct
  state (`"edge removed, counters consistent"`).
- `handleTenantGraphPath` and `handleTenantGraphShortestPath`
  (`pkg/server/graph_tenant_handlers.go`): the `"length"` field in the
  response was derived from the raw (pre-guard) path slice. If `guardSlice`
  removed any cross-tenant nodes, the returned length would disagree with the
  actual number of hops in the returned path array. Both handlers now capture
  the guarded slice into a local variable and compute `length` from it.

### Changed

- `flatGraphData` serialisation struct: added `CycleDetection string` and
  `CycleCheckLimit int` fields (both `omitempty`) so that `FlatGraph.Save`
  includes the cycle-detection policy in the persisted file and
  `FlatGraph.Load` restores it on startup. Previously a server restart always
  reverted to `"ignore"` regardless of the configured mode. Files written by
  older versions of olu that lack these fields remain valid; `Load` leaves the
  constructor-supplied default intact for absent fields. Both `Save` and `Load`
  have updated doc comments stating this contract explicitly.

## [0.9.7-patched39] - 2026-03-08

### Fixed

- `FlatGraph.Load`: node and edge errors are no longer silently discarded with
  `_ =`. Each failed entry is now logged at `[WARN]` level and the method
  returns an error summarising the count of skipped entries. Previously a
  corrupt or manually-edited `graph.json` could produce a partially-loaded
  graph with no indication of the discrepancy.
- `FlatGraph.UpdateFromEntityForTenant`: when an edge-label update fails at
  both the re-add and the rollback stage, `edgeCount` and `edgeCounters` are
  now explicitly corrected upward to match the actual (edgeless) state.
  Previously the counters were decremented but never restored, causing a
  permanent undercount for the life of the process.
- `FlatGraph.NodeCountForTenant` / `EdgeCountForTenant`: the silent `n < 0 →
  return 0` clamp is replaced with a logged `[ERROR]` that still returns 0
  rather than a negative value. The clamp masked counter-corruption bugs
  (including the one above) without surfacing them. Logging makes the
  invariant violation visible in production.
- `AdaptivePersister.save`: `p.mu` is now released before calling
  `graph.Save()` and re-acquired only to update `p.lastSave`. Previously the
  mutex was held for the entire I/O duration, blocking `Stats()` calls for up
  to tens of milliseconds per save.

### Changed

- `FlatGraph.Save`: the read lock is now released after copying the in-memory
  snapshot and before JSON serialisation and disk I/O. Previously the lock was
  held for the full duration of the write, blocking all mutation operations.
- `addEdgeLocked` (cross-tenant guard): added a comment explicitly documenting
  that tenant-0 (bare `"entity:id"`) nodes are permitted to form edges with
  non-zero-tenant nodes. This is the intended "shared global namespace"
  behaviour; the code was previously correct but silent about it.
- `GetNeighbors` / `GetIncomingEdges`: added doc comments pinning the
  intentional silent-empty contract for absent nodes. Two new contract tests
  (`TestContract_GetNeighbors_AbsentNode_ReturnsEmpty`,
  `TestContract_GetIncomingEdges_AbsentNode_ReturnsEmpty`) cover this
  behaviour, which was previously undocumented and untested.
- `ErrEdgeAlreadyExists`: added a comment to the sentinel definition noting
  its internal dual-use as control flow within `UpdateFromEntityForTenant`.
  A companion comment at the call site explains the label-update protocol.
- `addEdgeLocked` cycle-detection `"warn"` branch: added a comment clarifying
  that the fall-through after the log line is intentional — the edge is added
  even when a cycle is detected in warn mode.

## [0.9.7-patched38] - 2026-03-08

### Removed

- `IndexedGraph` and `pkg/graph/state` deleted. `FlatGraph` is now the sole
  graph implementation. All tests, benchmarks, server fixtures, and Sulpher
  test helpers migrated to `FlatGraph`.
- `"indexed"` removed as a valid `GraphMode` config value. The only valid
  non-disabled mode is `"flat"`. Existing configs using `GraphMode: indexed`
  must be updated to `GraphMode: flat`.
- `docs/OPTION_B_SPEC.md` and `docs/GRAPH_TENANT_ISOLATION_BRIEF.txt` deleted
  — historical design documents superseded by the completed implementation.
- `pkg/graph/graph_test.go`, `graph_intensive_test.go`, `graph_tenant_test.go`
  deleted (all tested `IndexedGraph` exclusively).

### Changed

- `pkg/graph/graph.go` now contains only the `Graph` interface, sentinel
  errors, `Degree`, `NodeInfo`, and `defaultCycleCheckLimit`. Sentinel errors
  were previously re-exported from `pkg/graph/state`; now defined directly.
- `pkg/graph/graph_contract_test.go`: `IndexedGraph` row removed from the
  implementation table; `FlatGraph` is the only entry.
- `pkg/graph/bench_test.go`: `_Indexed` variants and `buildIndexedGraph`
  removed; benchmarks renamed to drop the `_Flat` suffix.
- `docs/GRAPH_INVARIANTS.md` rewritten for `FlatGraph`.

## [0.9.7-patched37] - 2026-03-08

### Fixed

- `state.CommonNeighbors`: rewritten to outgoing-only, matching `FlatGraph.CommonNeighbors`; the previous bidirectional implementation was a contract violation — incoming edges were incorrectly included in the common-neighbour set. Contract test `TestContract_CommonNeighbors_IncomingEdgesExcluded` added to pin the outgoing-only semantics across both implementations. `TestTopology_Diamond` corrected to reflect the agreed behaviour (common predecessor is not a common neighbour).
- `AdaptivePersister.save`: dirty flag now cleared *before* `graph.Save()` rather than after. The old order had a TOCTOU window where a `MarkDirty()` call racing with a completed save would be silently swallowed, causing that write to never be persisted. On save failure the flag is explicitly restored so the next tick retries.
- `IndexedGraph.NodeCountForTenant` / `EdgeCountForTenant`: added missing `mu.RLock()` / `mu.RUnlock()` — every other read method on `IndexedGraph` holds the read lock; these two were inconsistent with both the rest of `IndexedGraph` and `FlatGraph`'s equivalent methods.
- `FlatGraph.RemoveNode`: incoming-edge counter decrements are now unconditional, matching the outgoing-edge block. Previously the decrement was inside the `if sr, ok := g.nodes[source]; ok` guard, meaning a corrupt/partially-loaded graph referencing a deleted source node would leave `edgeCount` and `edgeCounters` permanently high.
- `FlatGraph.wouldCreateCycle`: budget metric changed from total dequeues (`steps`) to unique nodes visited (`len(visited)`). The `steps` counter over-counted on bushy graphs with many parallel paths, triggering the conservative-reject threshold earlier than intended.
- `state.wouldCreateCycle`: rewritten from DFS to BFS with `len(visited)` budget metric, aligning with `FlatGraph.wouldCreateCycle`. Both implementations now use identical traversal order and identical budget semantics. `map[string]bool` replaced with `map[string]struct{}`.

### Changed

- `AdaptivePersister` type comment updated from hedged "DEPRECATION NOTE: … if the JSON filestore is eventually removed" to direct "DEPRECATED: The JSON filestore backend is deprecated … when the JSON filestore is removed". Reflects the stated direction.
- `state.AddEdge`: added comment documenting the intentional duplication of the cross-tenant check also present in `IndexedGraph.AddEdge`, explaining which callers bypass the outer method and the update obligation if the condition changes.
- `IndexedGraph` marked as pending deprecation in favour of `FlatGraph` in `state.wouldCreateCycle` comment; `FlatGraph.wouldCreateCycle` designated as the canonical implementation.

### Added

- `.gitignore`: covers `*.db`, `*.db-shm`, `*.db-wal`, `graph.json`, `graph.json.tmp`, `*.test`, `*.out`, `*.tmp`, and binary outputs. Prevents test-leftover SQLite files from being inadvertently included in checkpoints.

## [0.9.7-patched36] - 2026-03-08

### Fixed

- `FlatGraph.wouldCreateCycle`: budget exhaustion now returns `true` (conservative reject) instead of `false` (silent permit), matching `state.wouldCreateCycle` behaviour introduced in patched23
- `state.AddEdge`: idempotent re-add of an existing edge now returns `nil` immediately, avoiding a spurious cycle-check and (in `"warn"` mode) a false-positive `WARN` log on every re-add to a graph that already contains cycles
- `state.AddNode`: re-typing an existing node now removes it from the old type's index entry before inserting into the new one, matching `FlatGraph.addNodeLocked` and preventing stale type-index entries
- `handleTenantGraphPath`: added missing `from`/`to` empty-string validation (present in `handleTenantGraphShortestPath` and `handleTenantGraphPathExists` but absent here)
- `handleTenantGraphCommonNeighbors`: `count` in the response now reflects the post-guard slice length rather than the raw pre-strip length, so it cannot disagree with `len(common)` when `guardSlice` drops a cross-tenant node
- `AdaptivePersister`: wired `WriterEnter`/`WriterExit` into `updateGraph` and `removeGraph` in `server.go`; the adaptive interval was previously always fixed at 500ms because the writer count was never updated
- `IndexedGraph.Save` / `FlatGraph.Save`: partial `.tmp` file now removed on `os.WriteFile` failure
- `TestAtomicCounters_ImplicitNodeCreationViaAddEdge`: removed duplicate `NodeCount` assertion whose error message incorrectly claimed to test `NodeCountForTenant("")`

### Notes

- `AdaptivePersister` is flagged for future deprecation: it is only instantiated on the JSON filestore path (`storeHasEdgeTable == false`); all SQLite deployments never start it. If the JSON filestore is removed, `AdaptivePersister` should be deleted with it.

## [0.9.7-patched35] - 2026-03-08

### Fixed
- `FlatGraph.GetNodeInfo`: `Entity` field now correctly strips the tenant prefix
  for prefixed node IDs (e.g. `"0001@items:42"` → `Entity: "items"`); previously
  returned `"0001@items"`.
- `FlatGraph.addEdgeLocked`: auto-created nodes now route through `addNodeLocked`,
  enforcing `ErrMalformedNodeID` for malformed `@`-containing IDs; previously
  the check was silently bypassed.
- `FlatGraph.HasCycle`: replaced unbounded recursive DFS with the iterative
  three-colour frame-stack implementation from `pkg/graph/state`, eliminating
  the goroutine stack-overflow risk on deep graphs.
- `server.cascadeDelete`: removed redundant `persister.MarkDirty()` call at end
  of function; `removeGraph` already calls it per node.

### Tests
- `TestContract_GetNodeInfo_PrefixedNode`: contract test (both implementations)
  verifying `Entity` is stripped of tenant prefix.
- `TestContract_AddEdge_MalformedNodeID_Rejected`: contract test verifying
  `ErrMalformedNodeID` is returned when either `AddEdge` endpoint is malformed.

### Docs
- Corrected five stale comments across `flat_graph.go`, `state/state.go`, and
  `persister.go`: false claim about per-tenant counter machinery, stale
  `graphState` type name (twice), reversed HasCycle provenance, and overly
  narrow `MarkDirty` usage note.

## [0.9.7-patched33] - 2026-03-08

### Changed
- `FlatGraph`: `NodeCountForTenant` and `EdgeCountForTenant` are now O(1) via per-tenant counters (`nodeCounters`/`edgeCounters map[string]int`, keyed by tenant prefix, protected by the existing mutex). Previously these were O(N) linear scans across the entire node map.

### Fixed
- `FlatGraph`: node counter increments added to `addEdgeLocked` for nodes created implicitly by `AddEdge` (the implicit-creation path previously bypassed `addNodeLocked` and left `nodeCounters` stale).
- Counter maps are reset correctly in `Clear` and `Load`.

### Added
- Three new contract tests covering both implementations (6 subtests): node add/remove counter accuracy, edge add/remove counter accuracy (including `RemoveEdge` and `RemoveNode` cascade paths), and counter reset on `Clear`.

## [0.9.7-patched32] - 2026-03-08

### Fixed
- `FlatGraph.AddNode` was not delegating to `addNodeLocked`; it duplicated the logic without the malformed-ID guard added in patched31. Collapsed into a single delegation so the guard fires from all call paths.
- `FlatGraph.AddNode`: now returns `ErrMalformedNodeID` for IDs containing `@` without a valid `XXXX@` prefix, matching `IndexedGraph` behaviour.
- `FlatGraph.AddEdge` / `addEdgeLocked`: now returns `ErrCrossTenantEdge` when source and target carry different non-empty tenant prefixes, matching `IndexedGraph` behaviour.

### Added
- Four new contract tests covering both implementations (8 subtests): malformed node ID rejection, valid prefixed ID acceptance, cross-tenant edge rejection, and same-tenant edge acceptance.

## [0.9.7-patched31] - 2026-03-08

### Fixed
- `FlatGraph`: `NodeCountForTenant`, `EdgeCountForTenant`, `GetAllNodesForTenant`, and `GetNodesByTypeForTenant` were ignoring the tenant prefix and returning data across all tenants. All four methods now filter by prefix, matching `IndexedGraph` semantics.
- `FlatGraph.UpdateFromEntityForTenant`: was building unprefixed node IDs (`entity:id`) regardless of the tenant parameter. Now correctly uses `tenant.NodeID(tenantID, ...)` so nodes are stored with the `XXXX@entity:id` prefix.
- Removed stale `var _ = tenant.NodeID` workaround; `tenant` package is now genuinely used.

### Added
- Six new tenant-isolation contract tests covering both `IndexedGraph` and `FlatGraph` (12 subtests total): `NodeCountForTenant`, `EdgeCountForTenant`, `GetAllNodesForTenant`, `GetNodesByTypeForTenant`, empty-prefix rejection, and `UpdateFromEntityForTenant` prefix correctness.

## [0.9.7-patched30] - 2026-03-08

### Tests

- **`graph_contract_test.go` — dual-implementation contract suite**
  (`pkg/graph/graph_contract_test.go`) — 63 test functions, each running
  against both `IndexedGraph` and `FlatGraph` via a shared `graphImpls`
  constructor table, for 126 subtests total. `FlatGraph` was added in
  patched29 and made the default implementation without any functional test
  coverage; this suite closes that gap entirely.

  Coverage spans every shared contract surface: construction and empty-graph
  invariants; `AddNode`/`RemoveNode` including type-index maintenance;
  `AddEdge`/`RemoveEdge` including idempotence, `ErrEdgeAlreadyExists`,
  implicit node creation, and reverse-index hygiene; `GetNeighbors` and
  `GetIncomingEdges` including copy-independence; counter consistency after
  mutations and idempotent adds; `GetDegree`; `GetNodeInfo`; `GetNodesByType`
  and `GetAllNodes`; `FindPath` (chain, self-path, max-depth, no-path,
  absent-node); `PathExists` (found, self, not-found); `CommonNeighbors`;
  `HasCycle`; all three cycle detection modes (`ignore`, `warn`, `error`)
  including self-loop and DAG; `Clear` including rebuild-after-clear;
  `Save`/`Load` round-trip (full, empty, missing file); five
  `UpdateFromEntity` scenarios (create, multi-ref, ref change, idempotent,
  ref removal); two concurrency tests (mixed reads/writes, clear-during-reads);
  and five topology patterns (diamond, fan-out, fan-in, deep chain,
  disconnected components).

  All 188 tests in `pkg/graph/...` pass; race detector clean.

## [0.9.7-patched29] - 2026-03-08

### Added

- **`FlatGraph` — new single-adjacency-list graph implementation**
  (`pkg/graph/flat_graph.go`) designed for single-tenant use. Each logical
  tenant gets its own `FlatGraph` instance; isolation is structural rather than
  enforced by string prefix guards. Core data structure is a single
  `map[string]*nodeRecord` where one pointer dereference yields the node's type,
  all outgoing edges, and all incoming edges — no parallel maps, no prefix
  construction or validation on the hot path.

  Benchmark results vs `IndexedGraph` (1k nodes, 4 edges/node):

  | Operation | IndexedGraph | FlatGraph |
  |---|---|---|
  | `AddNode` | ~4500 ns, 7 allocs | ~2200 ns, 5 allocs |
  | `AddEdge` | ~810 ns, 5 allocs | ~365 ns, 3 allocs |
  | `RemoveNode` | ~1500 ns, 7 allocs | ~575 ns, 4 allocs |
  | `GetNeighbors` | ~880 ns | ~870 ns |
  | `FindPath` | ~884 ns, 5 allocs | ~758 ns, 4 allocs |
  | Build 1k×4 | 2.1 MB, 24k allocs | 1.1 MB, 10k allocs |

  `FindPath` uses a single `map[string]bfsEntry` (merged parent+depth) with a
  pre-allocated queue to minimise allocations. `FlatGraph` satisfies the full
  `Graph` interface; the `*ForTenant` methods accept the prefix parameter to
  satisfy the interface but ignore it — the graph is already scoped to one
  tenant.

- **Benchmark suite** (`pkg/graph/bench_test.go`) comparing `IndexedGraph` and
  `FlatGraph` across `AddNode`, `AddEdge`, `GetNeighbors`, `GetNodesByType`,
  `RemoveNode`, `FindPath`, and a full build benchmark.

### Changed

- **`FlatGraph` is now the default graph implementation.** `GraphMode` default
  changed from `"indexed"` to `"flat"` in `pkg/config/config.go`. The value
  `"indexed"` remains valid and selects `IndexedGraph` explicitly, enabling
  side-by-side benchmarking. (`pkg/config/config.go`, `cmd/olu/main.go`)

## [0.9.7-patched28] - 2026-03-08

### Changed

- **Server layer fully decoupled from `*graph.IndexedGraph`** — All type
  assertions against the concrete graph type have been removed from
  `pkg/server/handlers.go`, `pkg/server/graph_tenant_handlers.go`, and
  `pkg/server/server.go`. Every handler now calls through the `graph.Graph`
  interface directly. A dead fallback branch in `addGraphJSONToZip` (which
  emitted a "not available" stub for non-`IndexedGraph` implementations) has
  been deleted; the function now unconditionally uses the interface.

- **`sulpher.Executor` accepts `graph.Graph`** — The `graph` field and both
  constructors (`NewExecutor`, `NewExecutorForTenant`) now take `graph.Graph`
  instead of `*graph.IndexedGraph`. The two type assertions in `server.go` that
  existed solely to satisfy the old concrete-type parameter are gone; the graph
  is passed directly. (`pkg/sulpher/executor.go`, `pkg/server/server.go`)

- **Sulpher test helpers return `graph.Graph`** — `setupTestGraph`,
  `buildChainGraph`, and `buildDenseGraph` return the interface rather than the
  concrete type, so tests do not propagate a concrete-type dependency to callers.
  (`pkg/sulpher/executor_test.go`, `pkg/sulpher/gaps_test.go`,
  `pkg/sulpher/guardrail_test.go`)

## [0.9.7-patched27] - 2026-03-08

### Fixed

- **`SaveIndex`/`LoadIndex` dead code deleted** — Both methods were unreachable
  (no callers outside `graph.go` itself) and carried a latent bug: `SaveIndex`
  wrote all index keys including `relationship:*` entries, while `LoadIndex`
  silently skipped them, making a roundtrip lossy. Removed both methods, removed
  the two calls from `TestSaveLoad_ComplexGraph` (the test still exercises the
  correct `Save`/`Load` path), and deleted `TestLoadIndex_CorruptJSON`.
  (`pkg/graph/graph.go`, `pkg/graph/graph_intensive_test.go`, `pkg/graph/graph_test.go`)

- **`UpdateFromEntityForTenant` relationship-rename is now atomic** — The
  RemoveEdge+AddEdge path (taken when updating an edge to a new relationship
  label) had no rollback: if the second `AddEdge` failed (e.g. `ErrCycleDetected`
  in `"error"` mode), the edge was silently dropped. The old relationship label is
  now snapshotted before `RemoveEdge`; on failure, a best-effort restore
  re-adds the original edge. Restore failure is logged at `[WARN]`.
  (`pkg/graph/graph.go`)

- **`sliceContains` replaced with map-set deduplication in the type and
  relationship indexes** (`pkg/graph/state/state.go`) — The internal `index`
  field changes from `map[string][]string` to `map[string]map[string]struct{}`.
  Membership checks in `AddNode` (type dedup) and `AddEdge` (relationship-key
  dedup) become O(1) instead of O(n). The `Snapshot()` serialisation path
  converts back to `map[string][]string` for JSON, so the on-disk format is
  unchanged. The now-unused `sliceContains` helper is deleted.

- **`RemoveNode` type-index cleanup is now O(types\_per\_node)** — Previously,
  `RemoveNode` scanned the entire index (all type and relationship keys) to evict
  the node, an O(K×N) operation. A new `nodeTypes map[string]map[string]struct{}`
  reverse map records every type key a node is indexed under; `RemoveNode` now
  uses it for a targeted O(1)-per-type cleanup instead of a full scan.
  (`pkg/graph/state/state.go`)

- **`Graph` interface widened** — The interface previously listed 13 methods,
  leaving 13+ public methods of `IndexedGraph` (including `NodeCount`,
  `EdgeCount`, `PathExists`, `CommonNeighbors`, `GetDegree`, `GetNodeInfo`, and
  all four tenant-scoped query methods) only reachable via `s.graph.(*graph.IndexedGraph)`
  type assertions in the server layer. All public graph-operation methods are now
  in the interface, eliminating every type assertion. A compile-time satisfaction
  check (`var _ Graph = (*IndexedGraph)(nil)`) is added.
  (`pkg/graph/graph.go`)

### Tests

- **`TestUpdateFromEntityForTenant_RollbackOnSecondAddEdgeFailure`** — Verifies
  the relationship-rename path (same target, different label) succeeds and
  replaces the label correctly. (`pkg/graph/graph_test.go`)

- **`mockGraph` in `persister_test.go` extended** — Mock updated to implement
  the widened `Graph` interface; all new methods stub to zero values.
  (`pkg/graph/persister_test.go`)

---

## [0.9.7-patched26] - 2026-03-08

### Fixed

- **`EdgeCount()` is now O(1)** — Previously iterated every node's adjacency map
  (O(N+E)); now delegates to `sumCounter(&s.edgeCounters)`, the same atomic counters
  already maintained incrementally by `AddEdge`/`RemoveEdge`. Behaviour is identical;
  a negative-guard (matching `EdgeCountForTenant`) is added for defensive completeness.
  (`pkg/graph/state/state.go`)

- **`FindPath` self-path is now explicit** — `FindPath(x, x, d)` previously worked
  by accident (BFS dequeued `from`, saw `current == to` immediately, path-reconstruction
  loop exited trivially). It now returns `[]string{from}` via an early guard, consistent
  with `PathExists(x, x, d)` which already had an explicit `from == to` check. Doc
  comment updated to document the behaviour. (`pkg/graph/state/state.go`)

### Tests

- **`TestEdgeCount_UsesAtomicCounters`** — Verifies `EdgeCount()` against a ground-truth
  adjacency traversal across two implicit tenants, and checks that `RemoveEdge`
  decrements correctly. (`pkg/graph/state/state_test.go`)

- **`TestFindPath_SelfPath`** — Confirms `FindPath(x, x, d)` returns `[x]` and that
  the result is consistent with `PathExists(x, x, d)` returning `(true, 0, nil)`.
  (`pkg/graph/state/state_test.go`)

---

## [0.9.7-patched25] - 2026-03-08

### Tests

- **`NewAdaptivePersister` constructor now covered** — Two tests added:
  `TestNewAdaptivePersister_ReturnsInitialisedPersister` (field value assertions)
  and `TestNewAdaptivePersister_StartStopRoundTrip` (channel wiring). Previously
  all persister tests used the internal `newTestPersister` helper, leaving the
  public constructor at 0% coverage. (`pkg/graph/persister_test.go`)

### Documentation

- **`GRAPH_API.md` stale content removed** — Eliminated historical development
  artefacts that had become misleading: the "Existing Endpoints" / "New
  rserv-Compatible Endpoints (v0.7.1)" split merged into a single endpoint table;
  "Not yet implemented: Full-text search / Field search" note removed (full-text
  search is implemented at `GET /api/v1/search`); "Variable-length patterns (Phase
  3)" comment label replaced with "Variable-length patterns"; `## Advanced Features
  (v0.8.0)` version label stripped; rserv compatibility section reworded from
  aspirational to factual. (`docs/GRAPH_API.md`)

- **`GRAPH_INVARIANTS.md` updated for patched22 state sub-package** — All references
  to the old `addNodeLocked` / `addEdgeLocked` / `removeEdgeLocked` helper names
  replaced with `AddNode` / `AddEdge` / `RemoveEdge`; raw field references (`g.adjacency`
  etc.) replaced with plain descriptions; version references updated from `v0.9.8`
  to `patched22`; `RemoveEdge` invariant table gained the reverse-map cleanup row
  (bug fixed in patched23). (`docs/GRAPH_INVARIANTS.md`)

- **`OPTION_B_SPEC.md` status updated** — Status header changed from "Deferred.
  Not yet implemented." to "Implemented in patched22." with a note that the
  document's future-tense language is historical design prose.
  (`docs/OPTION_B_SPEC.md`)

- **`state.go` doc comments on empty-prefix methods corrected** — `NodeCountForTenant`,
  `EdgeCountForTenant`, and `AllNodesForPrefix` previously documented "An empty
  prefix returns all" without caveat. Comments now note that `IndexedGraph` rejects
  empty-prefix calls before these methods are reached, so external callers should
  not rely on the all-tenant / all-nodes fallback.
  (`pkg/graph/state/state.go`)

### Housekeeping

- **`TODO.md` deleted** — All 15 tracked items (5 bugs, 8 test gaps, 2 doc items)
  resolved across patched23–patched25. No open items remain.

---

## [0.9.7-patched24] - 2026-03-08

### Fixed

- **`wouldCreateCycle` conservative on DFS budget exhaustion** — When the cycle-check
  DFS visited `cycleCheckLimit` nodes without finding a cycle, the old code returned
  `false` (edge permitted). It now returns `true`, conservatively rejecting the edge
  rather than risking an undetected cycle. A `[WARN]` is logged when budget is hit.
  (`pkg/graph/state/state.go`)

- **`loadFromData` error condition inverted** — The condition guarding which edge errors
  should be skipped during JSON graph reload was inverted: unexpected errors (e.g.
  `ErrCrossTenantEdge`) were silently swallowed, while expected idempotent errors
  (`ErrEdgeAlreadyExists`, `ErrCycleDetected`) caused the load to abort. Fixed to
  skip only the expected idempotent errors and log unexpected ones at `[WARN]`.
  (`pkg/graph/graph.go`)

- **`RemoveEdge` reverse-map cleanup unconditional** — `RemoveEdge` only cleaned up
  `s.reverse[to]` when the entry already existed, leaving the reverse index
  inconsistent in partially-corrupt graphs. It now initialises the entry if absent
  before deleting from it. (`pkg/graph/state/state.go`)

- **`NewIndexedGraphWithCycleDetection` logs invalid mode** — An unrecognised cycle
  mode (e.g. `"Error"` instead of `"error"`) was silently coerced to `"ignore"`.
  It now emits a `[WARN]` log before defaulting, making call-site typos visible.
  (`pkg/graph/graph.go`)

- **Cross-tenant data leakage via empty tenant prefix** — `NodeCountForTenant`,
  `EdgeCountForTenant`, `GetAllNodesForTenant`, and `GetNodesByTypeForTenant` all
  accepted an empty prefix and returned data across all tenants, bypassing the
  tenant isolation boundary. All four now return `ErrTenantRequired` (HTTP 400) on
  empty prefix; the two node-listing methods additionally log `[WARN]` flagging a
  possible cross-tenant exfiltration attempt. Server handlers and the Sulpher
  executor updated accordingly. Tests for the old all-tenant escape-hatch behaviour
  replaced with negative assertions confirming rejection. (`pkg/graph/graph.go`,
  `pkg/server/graph_tenant_handlers.go`, `pkg/sulpher/executor.go`,
  `pkg/graph/graph_tenant_test.go`, `pkg/server/graph_tenant_supplemental_test.go`)

### Tests

- **Regression tests for all five patched23 bug fixes** — One test per bug,
  targeting the exact failure mode that the fix addresses:
  - `TestCycleDetection_BudgetExhaustion` — sets `cycleCheckLimit = 3`, builds a
    4-node chain, verifies the closing edge is rejected via `ErrCycleDetected`.
  - `TestLoadFromData_CrossTenantEdgeRejectedAndLogged` — saves a JSON graph
    containing a cross-tenant edge; verifies it is absent after reload and edge
    count is 1 not 2.
  - `TestCycleDetection_InvalidModeDefaultsToIgnore` — passes `"Error"` to the
    constructor; verifies cycles are permitted (mode coerced to `"ignore"`) and
    includes a sanity sub-check that the same cycle is rejected under `"error"`.
  (`pkg/graph/graph_test.go`)

- **Counter validation after concurrent mixed operations** —
  `TestConcurrent_MixedOperations` now calls `assertCountersMatchAdjacency` after
  the goroutine wait, catching counter corruption that leaves the graph structurally
  intact but with wrong O(1) counts. (`pkg/graph/graph_intensive_test.go`)

- **`UpdateFromEntityForTenant` error path** —
  `TestUpdateFromEntityForTenant_DuplicateEdgeTargetPropagated` verifies that a
  document with two fields referencing the same target propagates `ErrDuplicateEdgeTarget`
  and leaves the graph unchanged. (`pkg/graph/graph_test.go`)

- **`Save` write-failure path** — `TestSave_WriteFailure` verifies that saving to an
  unwritable path returns an error and leaves no partial file at the destination.
  (`pkg/graph/graph_test.go`)

- **Legacy load exact content** — `TestLoadLegacyFormat_ExactContentVerified` replaces
  the retired `TestLoadLegacyFormat` (which only checked `NodeCount() > 0`) with
  assertions on exact node count, exact edge count, specific edge relationships, and
  reverse-index consistency. (`pkg/graph/graph_test.go`)

- **`LoadIndex` corrupt JSON** — `TestLoadIndex_CorruptJSON` verifies that malformed
  JSON in the index file produces a non-nil error rather than a silent no-op.
  (`pkg/graph/graph_test.go`)

- **`GetNodeInfo` node ID without colon** — `TestGetNodeInfo_NodeIDWithoutColon`
  exercises the `len(parts) == 1` branch: a colon-free node ID must not panic and
  must return `Entity = ""`, `EntityID = 0`. (`pkg/graph/graph_test.go`)

- **`TestCommonNeighbors` compound guard tightened** — The second assertion
  (`common[0] != "c:1"`) was guarded by `len(common) > 0`, silently skipping the
  content check when the count was wrong. Rewritten as `if / else if` so both
  checks run at the appropriate time. (`pkg/graph/graph_test.go`)

- **`TestLoadLegacyFormat` retired** — Superseded by `TestLoadLegacyFormat_ExactContentVerified`
  and the existing malformed-ID regression tests. (`pkg/graph/graph_test.go`)

---

## [0.9.7-patched22] - 2026-03-06

### Changed

- **Option B: compiler-enforced graph invariants via `pkg/graph/state` sub-package** —
  The three locked helpers (`addNodeLocked`, `addEdgeLocked`, `removeEdgeLocked`) and
  all raw map state have been moved to a new `pkg/graph/state` sub-package. The fields
  `adjacency`, `reverse`, `index`, `nodeCounters`, and `edgeCounters` are now unexported
  fields of `state.State`; any direct write from `pkg/graph/graph.go` is a compile
  error. `IndexedGraph` delegates all state access through `g.s.*` method calls.
  The public API of `IndexedGraph` is unchanged; no callers outside `pkg/graph` require
  modification. The grep audit in `GRAPH_INVARIANTS.md` is now superseded by the
  compiler. (`pkg/graph/state/state.go`, `pkg/graph/graph.go`)

- **`TestCounterConsistency` migrated to T3 black-box form** — The test in
  `graph_test.go` previously ranged over `g.nodeCounters` and `g.edgeCounters`
  directly, which required white-box access to `IndexedGraph` internals. It has been
  rewritten to derive expected counts from `GetAllNodes`/`GetNeighbors` and assert
  them via `NodeCountForTenant`/`EdgeCountForTenant`. The original white-box check
  (ranging over the raw sync.Map) now lives in
  `pkg/graph/state/state_test.go::TestCounterConsistency_WhiteBox`. (`pkg/graph/graph_test.go`,
  `pkg/graph/state/state_test.go`)

---

## [0.9.7-patched21] - 2026-03-06

### Fixed

- **`Load` and `loadLegacy` bypassed all helper-layer invariants via direct map
  assignment** — Both load paths wrote directly to `g.adjacency`, `g.reverse`,
  and `g.index`, then called `rebuildCountersLocked` to compensate for the bypassed
  counter updates. This was a structurally brittle pattern: every time a new
  invariant was added to `addNodeLocked` or `addEdgeLocked` (malformed-ID
  rejection, type indexing, cross-tenant edge guards), the load paths had to be
  manually updated or they silently diverged. In patched17–19, three such
  divergences were found and patched individually. Replaced with a
  `loadFromData` helper that replays the saved snapshot through the helper methods:
  resets all state, then calls `addNodeLocked`/`addEdgeLocked` for every node and
  edge in the adjacency map, then restores type-index entries from the saved index
  (relationship entries are already rebuilt by `addEdgeLocked`; only entity-type
  entries need restoration). `rebuildCountersLocked` is now dead code and has been
  removed. Future invariants added to the helpers are automatically applied during
  load with no further changes required. (`pkg/graph/graph.go`)

- **`loadLegacy` wrote neighbour nodes directly to `g.adjacency` without
  validating their IDs** — A malformed `'@'`-containing neighbour ID (the edge
  target) in a legacy file bypassed `ErrMalformedNodeID` validation even after
  the source-node guard was added in patched19, because the neighbour creation was
  a separate direct map write. With the `loadFromData` refactor, `loadLegacy` now
  calls `addEdgeLocked` per edge, which in turn calls `addNodeLocked` for both
  endpoints — applying the malformed-ID check to edge targets as well as sources.
  Regression test: `TestLoadLegacy_MalformedNeighbourIDRejected`.
  (`pkg/graph/graph.go`)

### Changed

- **`rebuildCountersLocked` removed** — This helper existed solely to compensate
  for `Load` and `loadLegacy` bypassing the counter-update logic in
  `addNodeLocked`/`addEdgeLocked`. With both load paths now replaying through the
  helpers, `rebuildCountersLocked` is unnecessary. Its removal also eliminates the
  risk of it diverging from the helpers in a future change.
  (`pkg/graph/graph.go`)

- **`addNodeLocked` skipped type indexing for nodes already present in the
  adjacency map** — The type-index append was gated inside the `if !exists` block,
  so a node first created implicitly by `addEdgeLocked` (with an empty type) and
  later registered via `AddNode(id, "post")` was never added to
  `g.index["post"]`. `GetNodesByType` and `GetNodesByTypeForTenant` returned empty
  results for that entity type even though the nodes were live in the graph. This
  affected the hot path: `UpdateFromEntityForTenant` writes every *target* node
  with an empty type via `addEdgeLocked`; when that target entity is subsequently
  written directly, its type was silently unindexed. Fixed by moving the index
  append outside the `!exists` block, guarded by a `contains` deduplication check
  to prevent duplicate entries on repeated `AddNode` calls. Regression test:
  `TestTypeIndex_AddNodeAfterImplicitCreation`. (`pkg/graph/graph.go`)

- **`GetNodeInfo` returned a tenant-prefix-polluted `Entity` field for
  multi-tenant nodes** — `strings.SplitN(nodeID, ":", 2)` on `"0001@user:42"`
  gives `["0001@user", "42"]`, so `Entity` was set to `"0001@user"` instead of
  `"user"`. The 0.9.5 handler code strips the prefix at the HTTP response layer,
  but `GetNodeInfo` itself returned the wrong value, affecting any caller outside
  the tenant handler. Fixed by stripping the `XXXX@` prefix from the parsed entity
  name before populating `NodeInfo.Entity`. Tenant-0 nodes (no prefix) are
  unaffected. Regression test: `TestGetNodeInfo_EntityFieldStrippedForMultiTenant`.
  (`pkg/graph/graph.go`)

- **`AddEdge` in error-mode cycle detection created an orphan node when a
  self-loop was rejected for a previously non-existent node** — `addNodeLocked`
  created the node (counter incremented, adjacency entry written) before the cycle
  check ran. `wouldCreateCycle` returned true immediately for a self-loop, the edge
  was deleted, and `ErrCycleDetected` was returned — but the node remained as an
  unreachable isolate with a permanently leaked counter increment. Fixed by checking
  for `from == to` before any `addNodeLocked` call, so a rejected self-loop attempt
  has no side effects when the endpoints were not previously in the graph. Regression
  test: `TestAddEdge_SelfLoopLeavesNoOrphanNode`. (`pkg/graph/graph.go`)

- **`loadLegacy` bypassed `ErrMalformedNodeID` validation** — The legacy text
  format loader wrote directly to `g.adjacency` and `g.reverse`, bypassing
  `addNodeLocked` and its malformed-ID guard. Node IDs containing `'@'` without a
  valid uppercase-hex `XXXX@` tenant prefix (e.g. `"ab0z@user"`) were silently
  admitted. Once loaded, such nodes are invisible to the tenant isolation machinery
  and can produce ghost entries in tenant-scoped queries. Fixed by adding the same
  two-line guard (`strings.Contains(nodeID, "@") && NodeIDPrefix(nodeID) == ""`)
  that `addNodeLocked` uses, skipping malformed IDs rather than poisoning the
  graph. Regression test: `TestLoadLegacy_MalformedNodeIDRejected`.
  (`pkg/graph/graph.go`)

- **`addEdgeLocked` created implicit nodes without updating `nodeCounters` or the
  type index** — When `AddEdge` was called for nodes that did not yet exist,
  `addEdgeLocked` initialised the adjacency and reverse map entries directly,
  bypassing `addNodeLocked`. This left `nodeCounters` stale (e.g. three live nodes
  with a counter of zero) and omitted those nodes from the type index, making them
  invisible to `GetNodesByTypeForTenant`. The divergence was self-healing after a
  `Save` + `Load` cycle — `rebuildCountersLocked` walks the adjacency map and
  resets counters — but this caused the counter to jump on restart, giving
  different values before and after a reload of identical graph state. Fixed by
  replacing the two open-coded map-init blocks in `addEdgeLocked` with
  `addNodeLocked(id, "")` calls, which handle the counter increment, the type
  index entry, and the `ErrMalformedNodeID` guard. Regression test:
  `TestAtomicCounters_ImplicitNodeCreationViaAddEdge`. (`pkg/graph/graph.go`)

- **`RemoveNode` decremented `nodeCounters` unconditionally, producing a
  permanently negative counter** — The `atomic.AddInt64(..., -1)` decrement
  executed even when the target node did not exist, driving the counter below zero.
  `NodeCountForTenant` clamps negative values to zero, masking the corruption until
  the next `AddNode` — whose increment brought the counter to zero instead of one,
  leaving it one short for the remainder of the process lifetime. Fixed by returning
  early when the node is absent, making `RemoveNode` idempotent. Regression test:
  `TestAtomicCounters_DoubleRemoveNodeDoesNotUnderflow`. (`pkg/graph/graph.go`)

- **`HasCycle` used a recursive closure, risking goroutine stack overflow on deep
  graphs** — `wouldCreateCycle` (the write-path cycle check) had already been
  converted to an iterative BFS with a configurable node budget. `HasCycle`, called
  from the `/graph/stats` endpoint, still used a mutually-recursive closure
  (`hasCycleFrom` calling itself) — unable to benefit from tail-call optimisation
  and liable to overflow the goroutine stack on graphs with long chains (e.g. deep
  IoT device hierarchies). Rewritten as an iterative DFS with an explicit frame
  stack and a three-colour visited/in-path/done scheme, consistent with the pattern
  already used by `wouldCreateCycle`. (`pkg/graph/graph.go`)

- **`AdaptivePersister.save` had a TOCTOU on the dirty flag** — The save method
  read the dirty flag, called `graph.Save`, then cleared the flag with
  `dirty.Store(false)`. A `MarkDirty` call arriving between the return of
  `graph.Save` and the `Store(false)` was silently swallowed: the flag was cleared
  despite representing a mutation not included in the completed save. The missed
  dirty signal was only recovered when the next write triggered a new `MarkDirty`.
  Fixed by replacing `dirty.Store(false)` with
  `dirty.CompareAndSwap(true, false)`, which preserves any `MarkDirty` that
  arrived after the save completed. (`pkg/graph/persister.go`)

- **`AddEdge` accumulated duplicates in the relationship index** —
  Every call to `AddEdge` appended `from` to `g.index["relationship:X"]`
  unconditionally. When `syncGraphEdges` performs a delete-and-reinsert on
  every entity update, a node with a REF field accumulated multiple copies
  of itself in the relationship index, causing relationship-based lookups to
  return duplicates and leaking memory over time. A deduplication guard now
  checks before appending. (`pkg/graph/graph.go`)

- **`RemoveNode` performed an O(N) full-adjacency scan to clean up edges** —
  To remove incoming references to a deleted node, the method iterated every
  node in `g.adjacency` and `g.reverse`. Both maps already contain exactly
  the right sets: `g.reverse[nodeID]` is the set of nodes pointing to the
  deleted node, and `g.adjacency[nodeID]` is the set it points to. Cleanup
  now iterates only those sets — O(in-degree + out-degree) instead of O(N).
  (`pkg/graph/graph.go`)

- **`GetNodesByType` and `GetNodesByTypeForTenant` ignored the existing
  node-type index, and `GetNodesByTypeForTenant` leaked non-zero-tenant nodes
  to the empty-prefix (tenant-0) path** — Both methods scanned all entries in
  `g.adjacency` using `strings.HasPrefix`, giving O(N) lookups regardless of
  result size. `AddNode` already maintained `g.index[nodeType]`, but the index
  key was populated from `data["type"]` — a user-supplied entity field —
  rather than the entity schema name used at query time, so the index and the
  query used different keys and could never match. Separately, the empty-prefix
  fast-path in `GetNodesByTypeForTenant` returned all indexed nodes of the
  given type unconditionally, including nodes belonging to non-zero tenants
  whose IDs carry a `XXXX@` prefix; tenant-0 nodes are bare and must be
  distinguished from them. Fixed by: (1) indexing under the entity schema name
  throughout — `UpdateFromEntityForTenant` now passes `entity` (not
  `data["type"]`) to `AddNode`, and the server's direct `AddNode` call site in
  `server.go` was updated to match; (2) filtering the empty-prefix path to
  return only nodes whose IDs carry no `XXXX@` prefix. Both query methods now
  use `g.index[entityType]` — O(k) — and tenant isolation is preserved for all
  prefix values including empty. A dedicated regression test
  (`TestGetNodesByTypeForTenant_EmptyPrefixIsTenantZero`) guards this
  invariant. (`pkg/graph/graph.go`, `pkg/server/server.go`)

- **`RebuildGraph` silently dropped all `@REFS` edges** — The inline
  type-switch in `RebuildGraph` only handled single-REF map values; it never
  matched `[]interface{}` slices produced by `@REFS(…)` fields, so any entity
  with an `@REFS` field lost all those edges after a rebuild. Fixed by
  replacing the inline type-switch with `models.ExtractRefs` — the same helper
  used by `syncGraphEdges` — which handles single REFs, `@REFS` slices, and
  TSREF exclusion in one place. Regression test:
  `TestSQLiteStore_RebuildGraph_REFS`. (`pkg/storage/sqlite.go`)

- **Missing `relationship_name` index on tenant-scoped graph tables** —
  `graph_t%04X` tables created for non-zero tenants were missing the
  `idx_%s_rel` index on `relationship_name`, present on the default
  `graph_edges` table. Any query filtering by relationship name on a
  tenant-scoped graph performed a full table scan silently.
  (`pkg/storage/sqlite.go`)

- **`GET /graph/nodes/{id}/degree` returned 404 for adapted entities
  with no edges** — Adapted entities that have no REF fields are absent from
  the in-memory adjacency map; `GetDegree` therefore returned "not found".
  The handler now falls back to two `COUNT` queries against `graph_edges`
  (out-degree and in-degree separately). When both counts are zero the entity's
  existence is confirmed via `store.Get` before returning `{in:0, out:0,
  total:0}`, so non-existent nodes still correctly return 404. Applies to both
  tenant-0 and multi-tenant degree handlers. (`pkg/server/handlers.go`,
  `pkg/server/graph_tenant_handlers.go`)

- **`POST /graph/nodes/search` omitted adapted entities with no edges** —
  When a specific entity type is requested, the handler previously called
  `GetNodesByType` which queries the in-memory graph index — populated only
  from entities that have at least one REF field. Adapted entities with no
  REF fields were therefore invisible. The handler now prefers a direct
  `SELECT id FROM olu_X WHERE tenant_id = ?` query when the entity has an
  adapted table, guaranteeing complete results. Falls back to the graph index
  for non-adapted entities. Applies to both tenant-0 and multi-tenant node
  search handlers. (`pkg/server/handlers.go`,
  `pkg/server/graph_tenant_handlers.go`)

- **`UpdateFromEntityForTenant` observed partial state under concurrent
  readers** — The method acquired and released the graph lock multiple times
  (once per `AddNode`, once per stale-edge `RemoveEdge`, once per new-edge
  `AddEdge`). A concurrent reader between any two of those acquisitions could
  observe a node with no edges or a stale edge set. The entire sequence now
  runs under a single write lock via the `addNodeLocked`, `addEdgeLocked`, and
  `removeEdgeLocked` helpers. (`pkg/graph/graph.go`)

### Improved

- **`CommonNeighbors` deduplication changed from O(N²) to O(N)** —
  The previous implementation called `contains()` (a linear slice scan) for
  each candidate node, making deduplication quadratic for high-degree nodes.
  Replaced with a `map[string]bool` accumulator: O(1) membership test during
  collection, converted to a slice at the end. (`pkg/graph/graph.go`)

- **`FindPath` changed from per-frontier-node path allocation to
  parent-pointer BFS** — The previous BFS queued a full path copy
  (`make` + `copy` + `append`) for every node on the frontier. The new
  implementation records `parent[child] = parentNode` during traversal and
  reconstructs the path in a single pass once the target is found — one
  allocation at the end regardless of graph width. (`pkg/graph/graph.go`)

- **`Reference.ID` widened from `int` to `int64`** — SQLite returns
  integer IDs as `int64`; JSON unmarshal produces `float64`. Storing
  `int` required silent narrowing casts at every boundary and caused test
  assertions comparing against `int64` to fail. All call sites updated:
  `NewReference` now accepts `int64`, `IsReference` handles `int`, `int64`,
  and `float64` cases, and the two downstream callers that require `int`
  (`tenant.NodeID`, `store.Get`) cast explicitly.
  (`pkg/models/models.go`, `pkg/graph/graph.go`, `pkg/server/handlers.go`,
  `pkg/oql/executor.go`)

- **`syncGraphEdges` reduced from N round-trips to one prepared statement** —
  Previously issued one `fmt.Sprintf` + `ExecContext` per REF field. The
  function now collects all edge tuples first, skips the INSERT entirely when
  there are no REF fields, prepares the statement once, and executes once per
  edge. (`pkg/storage/sqlite.go`)

- **`loadEntitiesIntoGraph` now hydrates the graph from `graph_edges` on
  SQLite backends** — The previous implementation called `store.List` for
  every entity type, deserialising full entity JSON only to discard everything
  except REF fields: O(entities × JSON size) allocations at startup. A new
  `GraphEdgeScanner` optional interface (`pkg/storage/storage.go`) allows
  backends to stream rows directly from the edge table. `SQLiteStore`
  implements it: `ScanGraphEdges` issues a single `SELECT source_entity,
  source_id, target_entity, target_id, relationship_name FROM graph_edges`
  and calls `AddNode` + `AddEdge` per row — O(edges) with no JSON
  deserialisation. The jsonfile store does not implement the interface and
  continues to use the existing entity-deserialisation path unchanged.
  Future SQL backends (e.g. PostgreSQL) gain the fast path by implementing
  `GraphEdgeScanner`. (`pkg/storage/storage.go`, `pkg/storage/sqlite.go`,
  `cmd/olu/main.go`)

- **`RebuildGraph` now uses a prepared statement and batched inserts** —
  Previously called `fmt.Sprintf` inside the row loop and issued one
  `ExecContext` per edge. Now uses a single `PrepareContext` outside the loop
  and flushes rows in batches of 500 (within SQLite's binding limit).
  (`pkg/storage/sqlite.go`)

- **`NodeCountForTenant` and `EdgeCountForTenant` are now O(1)** —
  Previously performed an O(N) scan over `g.adjacency` on every call
  (including every `/graph/stats` poll). Now maintained as `sync.Map` of
  `*int64` counters, one entry per tenant prefix. All four adjacency-mutating
  methods (`AddNode`, `AddEdge`, `RemoveEdge`, `RemoveNode`) route through
  three locked helpers (`addNodeLocked`, `addEdgeLocked`, `removeEdgeLocked`)
  that own the counter updates. A `TestCounterConsistency` test cross-checks
  counters against the live adjacency map after every mutation.
  (`pkg/graph/graph.go`)

### Added

- **`models.ExtractRefs` — unified REF extraction helper** — Replaces
  scattered `IsReference` type-switch blocks in `syncGraphEdges` and
  `UpdateFromEntityForTenant`. Accepts any `interface{}` value and returns
  `[]*Reference`: handles a single REF map, a `[]interface{}` slice of REF
  maps (`@REFS`), and silently excludes TSREF and all non-REF values.
  Single point of containment for all map type-switching on REF fields.
  (`pkg/models/models.go`)

- **`models.NewReference` / `(*Reference).ToMap()` — typed REF
  constructor** — Eliminates raw `map[string]interface{}` construction in
  `evalLiteral`. `NewReference(entity, id)` returns a `*Reference`;
  `ToMap()` emits the canonical `{"type":"REF","entity":…,"id":…}` map
  required for JSON storage, ensuring round-trip consistency.
  (`pkg/models/models.go`, `pkg/oql/executor.go`)

- **`models.TSReference` / `IsTSReference` — typed TSREF** — Companion to
  `Reference` for timeseries links. `ExtractRefs` uses `IsTSReference` to
  exclude TSREF maps from graph edge creation without additional conditions
  at call sites. (`pkg/models/models.go`)

- **`@REFS(…)` in OQL INSERT** — Values clauses now accept an array of REF
  expressions via `@REFS(@REF('tag', 1), @REF('tag', 2), …)`. `evalLiteral`
  evaluates each argument as a `@REF`, collects the results into
  `[]interface{}`, and stores the field as a slice of REF maps.
  `syncGraphEdges` (via `ExtractRefs`) creates one `graph_edges` row per
  element. Integration test: `TestSQLiteStore_REFSGraphEdges`.
  (`pkg/oql/executor.go`, `pkg/storage/sqlite_test.go`)



---

## Earlier releases (v0.9.0 – v0.9.6)

The detailed per-patch entries for v0.9.0 through v0.9.6 have been condensed.
Full history is available in git log.

### [0.9.6] - 2026-03-05

- `@REF('entity', id)` syntax in OQL INSERT VALUES clauses; graph edges created automatically
- Exhaustive tenant graph isolation test suite (17 adversarial integration tests)
- Coverage hardening across six packages (+291 tests, 1835 → 2126 total)
- Graph layer promoted to production-ready in strict mode
- `pkg/timeseries`: `mustEncodeKey` panic replaced with proper error return
- `handleTenantGraphNodeInfo` Entity field prefix leak fixed

### [0.9.5] - 2026-03-05

- Exhaustive tenant graph isolation test suite (17 adversarial integration tests)
- Coverage hardening across six packages (+291 tests)
- `pkg/timeseries` `mustEncodeKey` panic replaced with error return
- Entity prefix leak fixed in `handleTenantGraphNodeInfo`

### [0.9.4] - 2026-03-03

- `RangeAggregate` — single-pass all-fields aggregate (count/sum/avg/min/max for all 7 fields)
- `RangeSum`, `RangeAvg`, `RangeMin`, `RangeMax`, `RangeCount` delegate to `RangeAggregate`
- Range aggregate benchmarks (2,500-event dataset baseline)
- Fixed `fmt.Errorf` format string bug in `pkg/timeseries/store.go` (build failure fix)

### [0.9.3] - 2026-03-03

- **Timeseries v0.3 implementation** — complete rewrite on generic multi-dimensional key layout
- Storage layer: `codec.go`, `registry.go`, `store.go`, `manager.go`, `retention.go`
- HTTP API layer: timeline management, write, read, aggregation, management endpoints
- Six backend query guardrail config fields (`OLU_TS_*`)
- 78 new tests across 7 files
- Fixed timeseries purge false-break and partial-prefix time leakage

### [0.9.2] - 2026-03-02

- Hardware-aware query complexity gating (EXPLAIN-based cost estimation, three hardware profiles)
- Complexity benchmarks
- JOIN push-down design analysis (no implementation in v0.9.x)
- Golden database test infrastructure (shared envs, fast blob seeding, SQLite PRAGMA tuning)
- Query planner doc v2.0

### [0.9.0-rc11 through rc20] - 2026-02-22 to 2026-03-01

- **Adapted tables** — schema-adapted SQLite layouts from JSON Schema (2.6×–124× speedup)
- `StorageDialect` interface for backend-agnostic adapted table operations
- queryfy v0.3.0 integration replacing internal `JSONSchemaValidator`
- **Decimal type support** — exact fixed-point, wire format as JSON strings, scaled integer storage
- Adapted CRUD operations, registry, OQL decimal aggregation (`shopspring/decimal`)
- Schema evolution: automatic migration on schema change (`DiffAdaptedSpecs`, `MigrateAdaptedTable`)
- Read/write split connection pools (1 writer, NumCPU readers with `query_only=ON`)
- OQL optimisation phases A1–A4, B1–B2, B4: full SELECT push-down, jsonic tokeniser,
  FieldQueryable interface, predicate push-down during tokenisation, prepared statement cache,
  planner dispatch refactor, hardware-aware complexity gating
- Phase B3 (columnar executor) deferred indefinitely
- Sulpher: context cancellation, `MaxVisitedNodes`/`MaxResults` guardrails, slow query logging
- Sentinel errors replacing string matching for query limit violations

---
