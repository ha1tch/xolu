# xolu — Tracking (live register)

Version: 0.16.7
Last reviewed: 2026-07-21

Open actionable items only — debt, defects, gaps, hardening, tooling, and
features filed as prerequisites. A closed item still present here is itself
a defect. Practices: `docs/TRACKING_PRACTICES.md`.

Related documents:
- `docs/RESOLVED.md` — closed items, append-only (T-01, T-15, T-17, T-18,
  T-29, T-30, T-31, D-001–D-009, and the retired TD-nnn namespace live there).
- `docs/KNOWN_ISSUES.md` — intentional limits, invariant boundaries, and
  recorded decisions; not actionable work.
- `docs/API_V2_TRACKING.md` — API v2 stage delivery. Demarcation: the S-table
  owns stage detail; where a register item overlaps a stage (T-07/T-09/T-10 vs
  S13–S17), the item links to the stage rather than duplicating it.
- `docs/SULPHER_OPTIMISATION_ROADMAP.md` — Sulpher performance opportunities,
  deliberately kept as a benchmark-gated roadmap rather than register items.
- `CHANGELOG.md` — what shipped, when.

Status legend: ✓ done · ◐ partial · ☐ not started · ✗ dropped

## Status table

The table is derived from the `Theme / Priority / Status / Blocks` field
lines carried by every detail section below; the two must not diverge
(consistency is a T-22 release-gate check). Priority is importance only
(P1 highest); themes group the detail sections; provenance lives in each
item's Trigger line.

| ID | Summary | Theme | Pri | Status | Blocks / after |
|----|---------|-------|-----|--------|----------------|
| T-07 | Durable dispatch, at-least-once delivery | events | P3 | ☐ | Blocks: agentic reliance on events as source of truth (molu included). Overlaps: S16 (d… |
| T-08 | Wire deferred event types | events | P3 | ☐ | Overlaps: S15 (additional trigger sources). Companion: T-20 could ship together. |
| T-09 | Wire deferred event actions (`sulpher`, `fsm.walk`) | events | P3 | ☐ | Overlaps: S14 (`fsm.walk` action); `sulpher` action in the S13–S17 family. |
| T-10 | True synchronous event execution | events | P3 | ☐ | Overlaps: S13 (sync event actions). |
| T-11 | Delivery ordering guarantees | events | P3 | ☐ | — |
| T-27 | Guard-evaluated `GetMachineTransitions` | fsm-read | P4 | ☐ | Pairs: T-28 (single FSM read-surface polish release). Not required by molu Part 2 today. |
| T-28 | Paginate FSM machine history | fsm-read | P4 | ☐ | Pairs: T-27 (single FSM read-surface polish release). Not required by molu Part 2 today. |
| T-20 | Emit schema-change events | events | P4 | ☐ | Non-blocking (molu can poll). Companion: T-08; and T-12 if new types adopt the dotted-s… |
| T-03 | Unified function registry across OQL / Sulpher / FSM eval | query-engines | P4 | ☐ | After: S14 (`@FSM` OQL function — see `API_V2_TRACKING.md`); registry taxonomy is final… |
| T-05 | Remove inert config fields `BlobDir` / `DynConfigFile` | storage-config | P4 | ☐ | Standalone since T-01 shipped. |
| T-23 | `syncver.sh` / `release.sh` for tsqlparser | tooling | P4 | ☐ | Lands in the tsqlparser repo, not xolu. |
| T-12 | Federation-consistent subjects and references | events | P5 | ☐ | After: 1.0 (naming settled; matching reshape is post-1.0). Companion: T-20 if new types… |
| T-13 | Author-named transitions in `event_latch_source` | events | P5 | ☐ | — |
| T-04 | OQL executor: UNION / INTERSECT / EXCEPT | query-engines | P5 | ☐ | After: a concrete need for set operations arises (explicitly deferred-until-need). |
| T-06 | Schema modes (b) strict and (c) clone | storage-config | P5 | ☐ | No timeline committed; design in `SCHEMA_MODES_DESIGN.md`. |
| T-33 | Partial `StoreConfig` in `loadTenantEntitiesFromStore` scoped store | storage-config | P5 | ☐ | Inert today (read-only path); fragile against future writes. |
| T-16 | `cal` daypart rollup-prune performance validation | cal | P5 | ☐ | After: realistic occupancy distributions are available to measure against. |
| T-42 | Dockerfile audit and refresh before wave-programme close | ops | P4 | ☐ | End-of-programme item so shipped images carry release-quality xolu, not interim wave states. |
| T-14 | SSA-based dataflow analysis for wall-clock usage | tooling | P5 | ☐ | — |
| T-46 | Retrofit /ts to the two-identity model (@C04d): external string timeline id at the wire, internal uint32 as codec key — the design cal and bal already use. Breaking API change; eliminates ts's numeric-id boundary surface entirely | ts | P4 | ☐ | After: a v2 ts API window (breaking). Correct long-term direction; ts's int64+helper form holds the line until then. |
| T-47 | Deflake `TestDeleteTimeline_DeletingMarker` concurrent-reader subtest | ts | P4 | ☐ | — |

---



## API surface



---

## FSM read surface



## Plan deviations (recorded per @P header rule)

- **2026-07-21 — item 7 (/meta subject-addressing generalisation)
  resequenced: wave 0 → wave-4 opening act.** Audit found /meta still
  entity-only (routes hard-wired to `(entity, positive-int)`; @C04c's
  subject list unimplemented). Deliberately deferred, not overlooked:
  nothing in waves 2–3 consumes generalised subjects (RI never reads
  meta — @C04c engine-inert; chronicle extraction doesn't touch it),
  the costliest slice (cal delete-path cascade hooks) would be
  refactored by wave 3's Sealer lift if done first, and bal
  (`bal.account`) is the first real non-entity consumer — landing it as
  wave 4 opens registers the subject kind live in the same breath.
  Effort estimate at decision time: ~1.25d (storage migration 0.25,
  subject model 0.25, handlers 0.25, cascade wiring 0.25–0.5,
  tests+docs 0.25). Trigger to pull earlier: a non-entity annotation
  consumer materialising before bal (none on the horizon).

### T-46. Retrofit /ts to the two-identity model (@C04d)

Theme: ts · Priority: P4 · Status: ☐
Blocks/after: needs a breaking-change window (v2 ts API). Not urgent — ts's corrected int64+helper wire form (@C04d fallback) is safe today; this is the structural upgrade from "corrected" to "correct by construction".

- **What:** give a ts timeline two identities like cal and bal — a
  caller-chosen external *string* id at the API boundary, and the
  existing `TimelineID uint32` demoted to an engine-internal dense codec
  key allocated by the store. Removes the numeric id from the wire
  entirely, so @C04d is satisfied by construction rather than by the
  int64+helper discipline.
- **Why deferred (P4, breaking):** every current ts client references
  timelines by number; moving to a string external id changes every ts
  endpoint's contract. It is the right model — cal and bal both chose it
  — but it is a v2-ts-sized redesign with client/molu/wire impact, not a
  bug fix. The 32-bit bug that motivated @C04d is already fixed by the
  wide-carry form; this item is the architectural follow-through, done
  when a breaking ts-API window opens.
- **Reference:** chronicle-substrate.md §4d (the two-identity preferred
  structure); cal (`CalendarID` string + `CalOrdinal uint32`) and bal
  (`account_id` string + internal key) as the pattern to match.



### T-47. Deflake `TestDeleteTimeline_DeletingMarker` concurrent-reader subtest

Theme: ts · Priority: P4 · Status: ☐
Blocks/after: —

Trigger: 2026-07-21 sandbox full-suite run — subtest
`concurrent_reader_during_delete_never_sees_defined-but-empty` failed once
("reader observed a defined-but-empty timeline during delete — marker did
not fence the read"), then passed 5/5 isolated reruns and a full-suite
rerun, all on the single-CPU sandbox. One spurious failure in ~4 full-suite
runs that day.

Two live hypotheses, unresolved: (a) the test's reader/delete
choreography assumes an interleaving that single-CPU scheduling only
sometimes produces — a test-harness timing assumption, not a product
defect; or (b) the deleting-marker fence has a genuine narrow window that
single-CPU scheduling occasionally exposes and multi-core would expose
more often. The distinction matters: (a) is a deflake, (b) is a defect.
Next step is characterisation on real multi-core silicon
(`GOMAXPROCS=<cores> go test ./pkg/timeseries/ -run
TestDeleteTimeline_DeletingMarker -count=200 -race`) before any code
change; do not "fix" the test blind. Until characterised, treat sandbox
full-suite failures of this one subtest as suspect-flake: rerun isolated
before diagnosing unrelated work.

### T-42. Dockerfile audit and refresh (end-of-programme)

Theme: ops · Priority: P4 · Status: ☐
Blocks/after: Scheduled deliberately late — after most of the wave programme has landed, so the image ships the best available substrate rather than a mid-flight version.

- **Trigger:** During v0.16.1 re-cut the Dockerfile was bumped `golang:1.22-alpine` → `golang:1.26-alpine` alongside the toolchain sync, but the image itself has not been rebuilt or exercised since. Wave-era changes (uint32 tenant IDs, chronicle extraction, bal, dxp, iolu operations) will each touch things a Docker build could regress on quietly.
- **Work required:** rebuild the image against latest main; smoke-test it (`docker run xolu` → binaries respond on `/health`, `/version`); confirm the base image is still current stable Go; align any CMD/ENTRYPOINT changes wave work introduces.
- **Timing:** after wave 5 (dxp) or wave 6 (iolu), whichever settles the substrate for a release deemed "shippable to a customer."
- **Estimate:** ~half a day if nothing has broken; a day if wave changes have reshaped configuration surface.

### T-27. Guard-evaluated `GetMachineTransitions` (server-side option)

Theme: fsm-read · Priority: P4 · Status: ☐
Blocks/after: Pairs: T-28 (single FSM read-surface polish release). Not required by molu Part 2 today.

- **Trigger:** the client's `GetMachineTransitions` returns input symbols with a structurally-defined transition from the current state. xolu's handler at `handleFSMMachineTransitions` deliberately does not evaluate guards. If molu's `machine_state` tool needs to advertise "walks that will actually succeed right now," there is no way to compute that without attempting each walk and handling `XOLU-FSM005` rejections — which mutates history for a probe.
- **Impact:** low today. Molu Part 2 §5.1.9 does not currently require guard-evaluated transitions. Filing so that if it comes up during molu implementation, the option exists rather than being papered over client-side.
- **Work required:**
  - Add a `?evaluate=true` query parameter to `GET /fsm/machine/{id}/transitions`. When set, the handler runs each candidate transition's guard against the current variable state and returns only those whose guards permit the walk.
  - Alternative: a separate route `GET /fsm/machine/{id}/available` that always evaluates guards.
  - Client method: extend `GetMachineTransitions` to accept an `evaluate bool` parameter, or add a sibling `GetAvailableWalks(ctx, id)`.
- **Estimate:** half a day for the server-side; guard evaluation code already exists in `pkg/fsm/eval`.

### T-28. Paginate `GET /api/v2/fsm/machine/{id}/history`

Theme: fsm-read · Priority: P4 · Status: ☐
Blocks/after: Pairs: T-27 (single FSM read-surface polish release). Not required by molu Part 2 today.

- **Trigger:** xolu v0.14.5's `handleFSMMachineHistory` returns the full history in one response. The client's `GetMachineHistory` reflects that: no pagination parameters. For long-running machines with thousands of walks, a single response can be arbitrarily large.
- **Impact:** unbounded response size on long-lived machines. Not urgent — most machines in the intended use cases are short-lived. But once molu is in real use, this becomes a real concern for any tenant with high-cardinality state processes.
- **Work required:**
  - Add pagination parameters to `GET /fsm/machine/{id}/history`: `?limit=N` (bounded by a server-side maximum), `?before_id=N` and `?after_id=N` for cursor-based paging, or `?since=<timestamp>` for time-anchored paging. Cursor-based is the safer choice.
  - Response envelope grows to include a `next_cursor` field when more history remains.
  - Client method: extend `GetMachineHistory` to accept a `HistoryFilter` struct, matching the pattern used by `ListMachines`. Existing callers passing only `(ctx, id)` continue to work; the new filter is optional.
- **Sequencing:** clean pairing with T-27 (both are FSM-machine read-surface improvements). Could ship as a single "FSM machine read-surface polish" release.
- **Estimate:** 1 day for server, half a day for client.

---


## Event model

### T-07. Durable dispatch and at-least-once delivery

Theme: events · Priority: P3 · Status: ☐
Blocks/after: Blocks: agentic reliance on events as source of truth (molu included). Overlaps: S16 (dead-letter/replay), S17 (retry/backoff).

- Current model: at-most-once, single attempt, no retry, no backoff, no dead-letter, no replay.
- Crash window between commit and dispatch loses firings.
- Requires reconciliation sweep to make "delivered" mean "provably delivered".
- Blocks any consumer (molu included) treating events as source of truth for state change.

### T-08. Wire deferred event types

Theme: events · Priority: P3 · Status: ☐
Blocks/after: Overlaps: S15 (additional trigger sources). Companion: T-20 could ship together.

- Live today: `entity.created`, `entity.updated`, `entity.deleted`, `fsm.output`, `fsm.step`, `commit.applied`.
- Not yet shipped: `graph.edge.*`, `fsm.entered`, `fsm.exited`, `fsm.terminal`, `ts.appended`, `meta.expired`.
- See `docs/EVENT_PENDING.md`.

### T-09. Wire deferred event actions

Theme: events · Priority: P3 · Status: ☐
Blocks/after: Overlaps: S14 (`fsm.walk` action); `sulpher` action in the S13–S17 family.

- Live today: `webhook`, `oql`.
- Not yet shipped: `sulpher`, `fsm.walk`.
- `fsm.walk` enables machine-reacts-to-machine without external subscriber loop.

### T-10. True synchronous event execution

Theme: events · Priority: P3 · Status: ☐
Blocks/after: Overlaps: S13 (sync event actions).

- A def may declare `"execution": "sync"`; currently stored and always runs async (response carries `X-Executed-As: async`).
- In-transaction, roll-back-on-failure execution required.

### T-11. Delivery ordering guarantees

Theme: events · Priority: P3 · Status: ☐
Blocks/after: —

- Currently: events from a single request delivered unordered; subscribers must not assume any order.
- Design and ship per-firing / per-machine / per-transaction ordering as appropriate.

### T-12. Federation-consistent subjects and references

Theme: events · Priority: P5 · Status: ☐
Blocks/after: After: 1.0 (naming settled; matching reshape is post-1.0). Companion: T-20 if new types adopt dotted subjects from day one.

- Designed but not implemented: `xolu.` dotted-subject namespace, three-level wildcard matching, `LocalRef`-consistent references for nolu interoperability.
- Naming conventions settled; matching reshape is post-1.0 work.
- See `docs/NOLU_EVENTS.md`.

### T-13. Author-named transitions in `event_latch_source`

Theme: events · Priority: P5 · Status: ☐
Blocks/after: —

- Currently element-level for FSM events: `fsm/<from>:<input>:<to>`; kind-level for `commit.applied`.
- Author-named transition support deferred.

### T-20. Emit schema-change events for event-driven schema refresh

Theme: events · Priority: P4 · Status: ☐
Blocks/after: Non-blocking (molu can poll). Companion: T-08; and T-12 if new types adopt the dotted-subject namespace.

- **Trigger:** molu Part 2 §4.3 documents that molu polls the schema endpoints because xolu does not emit events for schema, FSM def, generator, or event-def mutations.
- **Current state:** verified by source inspection at v0.14 — `handleCreateSchema`, `handleFSMDefCreate`, `handleFSMDefReplace`, `handleFSMDefDelete`, `handleSeqDefine`, `handleGenDefine`, `handleEventCreate`, `handleEventUpdate`, and `handleEventDelete` do not fire events.
- **Work required:**
  - Design a `schema.*` event type family covering entity-schema, FSM-def, generator, and event-def mutations. Candidate names: `schema.entity.created/updated/deleted`, `schema.fsm_def.created/updated/deleted`, `schema.gen.created/deleted`, `schema.event_def.created/updated/deleted`.
  - Wire firings into the mutation handlers, following the existing `commit.applied` pattern.
  - Document under `docs/EVENT_MODEL.md` and add to the "live" list in `docs/EVENT_PENDING.md`.
- **Not blocking:** molu can continue polling indefinitely. This task exists so that when the polling cost becomes visible or when real-time responsiveness matters, the substrate can support it.
- **Sequencing:** natural companion to T-08 (wire deferred event types) and could ship together with the federation-consistent subject reshape T-12 if the new event types adopt the `xolu.` dotted-subject namespace from day one.

---

## Query engines (OQL / Sulpher / FSM eval)

### T-03. TD-001 — unified function registry across OQL / Sulpher / FSM eval

Theme: query-engines · Priority: P4 · Status: ☐
Blocks/after: After: S14 (`@FSM` OQL function — see `API_V2_TRACKING.md`); registry taxonomy is finalised once `@FSM` exists.

- Current state: three subsystems with incompatible dispatch mechanisms.
  - `pkg/oql`: `map[string]ScalarFunc` (`func([]interface{}) interface{}`).
  - `pkg/sulpher`: hardcoded `switch` in `evalFunctionEnv`, no extension point.
  - `pkg/fsm/eval`: tsqlruntime `FunctionRegistry`, tagged-union `Value` type.
- Design sketch recorded in KNOWN_ISSUES:
  - `XoluFunc` interface with `Identifier()`, `Category()`, `Description()`.
  - Categories: `Pure`, `Stateful`, `Transactional`. Taxonomy to be finalised now that `@FSM` (S8) exists.
  - Per-subsystem adapter interfaces (`OQLAdapter`, `FSMAdapter`, etc.) hanging off `XoluFunc`.
- Package placement decision open: extend `pkg/qs`, or new `pkg/funcregistry`.
- Current impact: low (internal organisation only). Debt grows with new call sites.

#### Extended detail (from retired TD-001)

**Packages:** `pkg/qs`, `pkg/oql`, `pkg/sulpher`, `pkg/fsm/eval` (planned)
**Introduced:** patch006 (S5 sequences)
**Deferred until:** after `@FSM` is implemented (S8)

There is currently no unified registry for the named extension functions
(`@SEQ`, `@GEN`, `@FSM`) that are intended to be callable across OQL,
Sulpher, and FSM guard expressions. Instead each subsystem maintains its
own dispatch mechanism:

- `pkg/oql/scalar.go` — a `map[string]ScalarFunc` (`func([]interface{}) interface{}`),
  extended at engine creation time via `RegisterScalarFunc`. Backing store
  for the standard T-SQL scalar functions from `pkg/qs`.
- `pkg/sulpher/executor_env.go` — a hardcoded `switch name` inside
  `evalFunctionEnv`, with access to `ctx`, `env Env`, and `*graphSnapshot`.
  No extension point. Graph-specific functions (`NODES`, `RELATIONSHIPS`,
  `PATH`) are compiled in.
- `pkg/fsm/eval` (S6, planned) — will use aulsql's `ExpressionEvaluator`
  with its own `FunctionRegistry`, typed as `func([]Value) (Value, error)`
  where `Value` is the tsqlruntime tagged union — a different type system
  from the `interface{}` world of the OQL scalar map.

The three extension functions being built across S5–S8 have incompatible
runtime contracts. `@SEQ` is a pure read of session state. `@GEN` will
dispatch to the `gen_definitions` table and call a generator's `/next`
logic. `@FSM` will issue SQL writes (a full state machine walk) and needs
access to the server's FSM walk runtime. Forcing all three into a single
`func([]interface{}) interface{}` signature loses the context access and
type safety each genuinely requires.

A unified registry that papers over those differences would be a leaky
abstraction. The correct factoring will be visible once `@FSM` exists and
the full shape of all three function surfaces is known. The refactor is
deferred to after S8.

**Current impact:** Low. The three extension functions are registered
independently in their respective subsystems. Duplicate registration logic
exists in `pkg/server/v2_gen_handlers.go` (OQL `init()`), `pkg/oql/oql.go`
(`RegisterSeqGenFuncs` at engine creation), and will exist in `pkg/fsm/eval`
(S6). No user-visible defect; internal organisation only.

**Resolution path:** After S8, review the full function surface across OQL,
Sulpher, and FSM eval. The likely shape is an `XoluFunc` interface that
describes what a function *is* rather than what it takes and returns:

```go
type XoluFunc interface {
    Identifier()  string       // "@SEQ", "@GEN", "@FSM", "UUID_V4"
    Category()    FuncCategory // Pure, Stateful, Transactional
    Description() string       // human-readable, for docs and error messages
}
```

`Category` replaces the temptation to put `InParams()`/`OutParams()` on the
interface. Parameter types belong to per-subsystem adapter interfaces, not
to the shared identity contract. A `Pure` function closes over nothing and
can be registered directly as a scalar. A `Stateful` function needs sequence
or generator state. A `Transactional` function (`@FSM`) needs a DB
transaction and the walk runtime. Each category has a corresponding adapter
type per subsystem:

```go
type OQLAdapter interface {
    XoluFunc
    EvalOQL(args []interface{}) interface{}
}

type FSMAdapter interface {
    XoluFunc
    EvalFSM(args []fsm.Value) (fsm.Value, error)
}
```

Each subsystem registers only the functions it has adapters for. The central
registry holds `XoluFunc` values; subsystems query it for functions of
categories they support. No function is forced to implement an adapter it
does not need.

The exact category taxonomy — whether `Transactional` is one category or
splits by read vs write semantics — should not be decided before `@FSM`
is implemented. The taxonomy will be obvious from the actual runtime
requirements once S8 exists. Designing it earlier risks getting the
categories wrong and needing a second refactor.

Consider whether `pkg/qs` is the right home or whether a new
`pkg/funcregistry` package is warranted. `pkg/qs` currently holds scalar
math and string functions; a registry with category-aware dispatch is a
different concern.

**Related:** D-001. Retiring the malformed `NEWID()` generator in favour
of `UUID_V4()` is part of the same FSM eval function-surface work and is
best resolved alongside this refactor.

### T-04. TD-002 — OQL executor support for UNION / INTERSECT / EXCEPT

Theme: query-engines · Priority: P5 · Status: ☐
Blocks/after: After: a concrete need for set operations arises (explicitly deferred-until-need).

- Parser AST already models these (`ast.SelectStatement.Union`, `ast.UnionClause.Type`). Validator rejects before execution.
- Error-message wording and documentation fixed in patch014. Executor implementation still absent.
- Work required: run both arms, merge results, deduplicate for plain UNION vs UNION ALL, integrate with push-down and scan-limit machinery.
- Current impact: low. Applications run two queries and merge client-side.
- Deferred until a concrete need arises.

#### Extended detail (from retired TD-002)

**Package:** `pkg/oql`
**Introduced:** pre-existing (predates the apiv2 patch series)
**Deferred until:** when a concrete need for set operations arises

The OQL executor rejects set operations. The underlying tsqlparser models
them (`ast.SelectStatement.Union`, an `ast.UnionClause` whose `Type` is
`UNION`, `INTERSECT`, or `EXCEPT`), so such a query *parses* — but
`pkg/oql/validator.go` rejects any statement with a non-nil `Union` before
execution. A query that needs "rows from A together with rows from B" cannot
be expressed in a single OQL statement and must be run as two queries.

Two secondary issues compounded this; both are now resolved (patch014):

- ~~**The error message names the wrong operator.**~~ Fixed: the validator now
  reads `ast.UnionClause.Type` and names the actual operator (e.g.
  `INTERSECT is not supported`) rather than always saying `UNION`.
- ~~**The limitation is undocumented.**~~ Fixed: `docs/OQL_API.md` now states
  that set operations are unsupported under the SELECT syntax section, and notes
  that Sulpher does support `UNION` / `UNION ALL` (the two languages diverge).

The remaining debt is the executor implementation itself. Implementing UNION is
not blocked by the parser (the AST is already present). The executor would need
to run both arms, merge results, and — for plain `UNION` versus `UNION ALL` —
deduplicate, which interacts with the push-down and scan-limit machinery. The
work is deferred until a concrete need appears; the prequery feature (which
forces `TOP 1` on a single SELECT) does not need it.

**Current impact:** Low. Set operations are uncommon in the current query
surface, and applications can run two queries and merge client-side. With the
error message and documentation now corrected, the remaining gap is purely the
unimplemented executor support, deferred until needed.

---

## Storage and configuration

### T-05. Remove inert config fields `BlobDir` and `DynConfigFile`

Theme: storage-config · Priority: P4 · Status: ☐
Blocks/after: Standalone since T-01 shipped.

- Fields remain on `config.Config` but are no longer read after v0.13.0 — paths derive from `BaseDir` via `pkg/storelayout`.
- Env vars `OLU_BLOB_DIR` and `OLU_DYNCONFIG_FILE` are accepted and silently ignored: a quiet footgun.
- Removal touches: fields, env vars, defaults, config validation warning logic, startup display.
- Sequence with T-01: either rename first and remove the `XOLU_*` variants, or remove first and rename what remains. Do not touch twice.

### T-06. Implement schema modes (b) strict and (c) clone

Theme: storage-config · Priority: P5 · Status: ☐
Blocks/after: No timeline committed; design in `SCHEMA_MODES_DESIGN.md`.

- Only growth mode (schema-optional) exists today.
- Default schema set at `<BaseDir>/schema/`.
- Design recorded in `docs/SCHEMA_MODES_DESIGN.md`.
- Open sub-decision: disk-vs-database home for schema-set content.
- No timeline committed.

---







### T-33. Per-tenant scoped store built with partial `StoreConfig`

Theme: storage-config · Priority: P5 · Status: ☐
Blocks/after: Nothing; hardening. Found during M4b integration-suite work (v0.16.0).

- **Trigger:** `cmd/xolu/main.go` (`loadTenantEntitiesFromStore`) constructs
  a per-tenant scoped store passing only `Type`, `TenantID`, and
  `SQLitePerFileTenants` — omitting `FullTextEnabled`, `BaseDir`, `DBPath`
  and the SQLite tuning fields that every other construction site carries
  (`cmd/xolu/main.go` startup and `pkg/server` `storeForTenant` both pass
  the full set).
- **Impact:** none today — the function only calls `List()`, and FTS
  indexing gates on writes. But the construction is a trap: any future
  write through this store silently skips FTS indexing, and the omitted
  path fields rely on factory defaulting that the other sites do not.
- **Work required:** derive the scoped `StoreConfig` from `store.Config()`
  wholesale (copy + override `TenantID`) rather than naming three fields;
  or extract a `ScopedConfig(tid)` helper on `StoreConfig` so all three
  construction sites share one derivation.
- **Context:** FTS is deliberately double-gated — the server flag opens
  the API (else 503), the store flag controls indexing. Both derive from
  the same `config.FullTextEnabled` at the wired sites, so the design is
  sound; this item is about the one site that breaks the pattern.
- **Estimate:** an hour with a construction-site sweep.

## cal



### T-16. `cal` daypart rollup-prune performance validation

Theme: cal · Priority: P5 · Status: ☐
Blocks/after: After: realistic occupancy distributions are available to measure against.

- Correctness of the rollup-prune is tested.
- Performance payoff (versus doing the work without the rollup) is an open empirical question.
- Requires realistic occupancy distributions.

---


## Tooling and verification

### T-14. SSA-based dataflow analysis for wall-clock time usage

Theme: tooling · Priority: P5 · Status: ☐
Blocks/after: —

- `TestNoBareWallClock` catches syntactic shapes only (direct field set, struct literal, address-of-temp idiom, listed persisting constructors).
- Does not track cross-function dataflow.
- Does not catch `time.Now()` passed to unlisted functions that store their argument.
- Requires an SSA-based `go/analysis` pass for full coverage.


### T-23. Add `syncver.sh` and `release.sh` to tsqlparser

Theme: tooling · Priority: P4 · Status: ☐
Blocks/after: Lands in the tsqlparser repo, not xolu.

- **Trigger:** during the tsqlparser v0.6.1 release, version bumping had to be done by hand: `VERSION` file, `pkg/version/version.txt`, and four hardcoded string literals inside `pkg/version/version_test.go`. xolu's `syncver.sh` handles the equivalent job for that repo in one command.
- **Work required:**
  - Copy the `syncver.sh` pattern from xolu, adapted for tsqlparser's simpler version layout (VERSION + version.txt only).
  - Add a lightweight `release.sh` covering: version format validation, syncver, test run, CHANGELOG top-entry check, tag creation.
  - Both scripts live in the tsqlparser repo, not xolu.
- **Impact:** trivial cost, guards against the four-place version bump growing into more places as tsqlparser evolves.

---
