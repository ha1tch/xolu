# API v2 — Development Plan

**Author:** haitch
**Date:** 2026-06-16
**Scope:** `/api/v2` subsystems: FSM, events, metadata, generators, GC infrastructure
**Status tracking:** This file is the *plan* (intent). For the live status of each
stage — what has shipped, what is in progress, and where execution diverged from
this plan — see [API_V2_TRACKING.md](API_V2_TRACKING.md).

---

## Background

The `/api/v1` surface covers xolu's core data model and is stable by design.
`/api/v2` introduces five new first-class subsystems — FSM machines, event
event defs, entity metadata, generators, and a generic GC worker — all
of which are post-v1.0 and carry an explicit experimental stability
guarantee.

This document describes the staged development plan for reaching two
milestones. The first is a working draft shipped with xolu 1.0, gated behind
a feature flag, with the understanding that the implementation is
demonstrably correct but not hardened. The second is a functionally complete
implementation shipped with xolu 2.0, where every behaviour specified in
`API_V2.md` is correctly implemented and the API surface is stable.

The plan is split into two parts accordingly.

---

## Constraints

**Optionality.** The entire v2 surface must be inert when `XOLU_API_V2_ENABLED`
is false. Routes not registered cannot be reached. Tables not created cannot
be corrupted. Code paths not exercised cannot interfere with v1 behaviour.
This is a hard constraint for both parts, not a Part 1 nicety.

**Non-interference.** Any failure in v2 subsystem initialisation — a bad
schema migration, a misconfigured generator, a GC worker that cannot start —
must log a warning and degrade gracefully rather than preventing the server
from starting. The v2 flag enables experimental functionality; it should
never take down a production v1 deployment.

**Demonstrability.** Part 1 sets a floor, not a ceiling. Each subsystem must
be correct enough to run the spec's own examples without crashing or
producing wrong output. That is a different bar from "production-hardened
against adversarial inputs" or "correct across the full error matrix."

**Atomicity is not optional.** There is one exception to the leniency above:
the FSM walk transaction. A walk that partially commits — advancing state
without recording history, or updating variables without advancing state —
is actively harmful, not merely incomplete. It silently corrupts machine
state in a way that may not be discovered until much later. The walk
transaction must be fully atomic from Part 1.

---

## External dependencies

Three things need to be confirmed before implementation begins, and none of
them should take more than a day each:

**CUID and ULID libraries.** `google/uuid` is already in `go.mod` and covers
`uuid_v4` and `uuid_v7`. CUID and ULID require new dependencies. The most
likely candidates are `github.com/lucsky/cuid` and `github.com/oklog/ulid/v2`;
these should be evaluated briefly against the spec's semantics before being
committed to `go.mod`.

**fsm-toolkit API stability.** `github.com/ha1tch/fsm-toolkit/pkg/fsm`
provides `FSM.Validate()`, `FSM.Analyse()`, `FSM.GetTransitions()`, and
`BundleRunner`. The plan assumes the package API is stable at v0.9.6+. If
the API surface has shifted since the spec was written, the definition of
"stable" here is what the plan can actually import — a brief read of the
current package before S7 begins is sufficient.

**`pkg/fsm/eval` payload binding convention.** The amputated
`ExpressionEvaluator` from aulsql needs a decision about how `payload.field`
access works before the first line of `pkg/fsm/eval` is written. The
`QualifiedIdentifier` case in the evaluator already handles dot-separated
names. The question is whether `payload.result` resolves from a flattened
map keyed by `"payload.result"`, or from a nested structure. Flattened is
simpler and sufficient for the spec's guard expressions; decide this once,
encode it in the package's documentation, and never revisit it.

**tsqlparser version mismatch.** aulsql vendors tsqlparser at v0.5.2; xolu
is on v0.6.0. The delta between the two versions is: the `FunctionCall.Distinct`
bool field added for `COUNT(DISTINCT x)` support, and six parser bug fixes for
obscure T-SQL constructs (parameterised `EXEC AT`, double-dot four-part names,
inline `CREATE SCHEMA`, `WAITFOR (RECEIVE ...)`, `CONTAINS` column argument
forms, bracket-escaped identifier lexer). None of these touch the expression
evaluation surface that `pkg/fsm/eval` uses — guard arithmetic, comparisons,
boolean operators, and variable references are unchanged across both versions.

The risk does not materialise if `pkg/fsm/eval` is implemented by copying the
relevant source from aulsql into the xolu module rather than importing aulsql
as a Go dependency. In that case the extracted code compiles against xolu's
own tsqlparser 0.6.0 as declared in `go.mod`, and there is no version conflict.
If aulsql were imported as a module dependency instead, the two copies of
tsqlparser would coexist in the build with different versions, which Go's
module system handles via its minimum version selection but which could produce
confusing type-incompatibility errors if aulsql types and xolu types cross
package boundaries. The copy-and-amputate approach avoids this cleanly.

---

## Part 1 — Draft towards xolu 1.0

### Guiding principle

Each stage in Part 1 delivers something independently demonstrable. Nothing
requires the entire v2 surface to be complete before it can be shown working.
The scaffolding stages (S1, S2) are prerequisites for everything else but
are small. The payoff stages (S7, S8, S9) are where the interesting
functionality lives and each builds directly on the one before.

### S1 — Flag and routing scaffold

**What ships:** `XOLU_API_V2_ENABLED` config field read from environment;
v2 route group registered only when the flag is true; `X-API-Stability:
experimental` response header on all v2 routes via middleware; startup log
warning when v2 is enabled; `GET /api/v2` returning a JSON map of which
subsystems are available and their status.

This stage has no user-facing functionality beyond the availability map, but
it establishes the architectural contract that every subsequent stage depends
on. The rule that v2 tables are created lazily — never during normal v1
startup — must be encoded here in a way that future stages cannot accidentally
violate. The `GET /api/v2` endpoint is useful during development as a
quick health check.

**Estimated:** 0.5 weeks.

### S2 — `pkg/gc`

**What ships:** `gc.Sweeper` interface; `gc.Worker` struct with `Start()`,
`Stop()`, `RunOnce()`; `gc.Report` struct; `blob.GCWorker` and
`timeseries.RetentionWorker` migrated to use `gc.Worker` for lifecycle
management; `GET /api/v1/admin/gc` listing workers and their last report;
`POST /api/v1/admin/gc/{name}/run` triggering a synchronous sweep.

This is placed in Part 1 rather than deferred because the FSM and event GC
sweepers in Part 1 depend on it, and migrating the existing blob and
timeseries workers is low-risk work that improves the codebase now rather
than later. The existing sweep logic is unchanged — only the ticker-and-stop-
channel lifecycle pattern moves to the shared implementation. The one
observable behaviour change is that the timeseries sweeper gains a `Report`
return value (currently fire-and-forget), which is what the admin endpoint
reads.

Note that `pkg/gc` admin endpoints live under `/api/v1` by design. GC is
operational infrastructure, not a versioned feature; it should be reachable
regardless of whether the v2 flag is set.

**Estimated:** 1 week.

### S3 — `/api/v2/meta`

**What ships:** Five endpoints for entity metadata (`GET`, `PUT`, `DELETE`
per key; `GET` and `DELETE` for all keys on an entity); `entity_meta` SQLite
table with `expires_at` column and a partial index on it; cascade delete from
inside the existing entity delete transaction; `XOLU_META_MAX_VALUE_BYTES`
config; optional `expires_at` field on `PUT` body; `meta.GCSweeper`
implementing `gc.Sweeper` and registered in `gcWorkers`; `XOLU-META001`
through `XOLU-META005`. The `meta.expired` event type is wired in
S9 when the event system lands; at S3 the GC sweep deletes expired entries
and records them in a log but does not yet dispatch event defs.

Metadata is deliberately the simplest v2 subsystem: no new dependencies, no
complex runtime, no inter-subsystem integration beyond the cascade delete.
It is a good first integration target because the cascade delete is the one
place where Part 1 touches existing v1 code, and handling that cleanly
establishes the pattern for the more consequential integrations in S8 and S9.

This stage also introduces the `FsmWalk` field to `CommitRequest` as a typed
but inert stub — present in the struct, validated for JSON shape, but
returning a "v2 not enabled" error if populated when the flag is off. Doing
this now avoids a disruptive struct change during S8.

**Estimated:** 1 week.

### S4 — Stateless generators

**What ships:** `GET /api/v2/gen/uuid_v4`, `uuid_v7`, `cuid`, `ulid`; OQL
scalar functions `UUID_V4()`, `UUID_V7()`, `CUID()`, `ULID()` registered
directly in the scalar map; `{{gen:uuid_v4}}` etc. available in event
template context (implemented as a simple function call — event template
substitution is not fully wired in Part 1, but the values are reachable).

These are pure functions with no storage and no configuration. The OQL
registration is a map entry; the HTTP endpoints are thin wrappers. The
main work is selecting and adding the CUID and ULID libraries to `go.mod`
and verifying their output shape matches the spec examples.

**Estimated:** 0.5 weeks.

### S5 — Sequences

**What ships:** `POST /api/v2/gen/seq` (define a sequence); `GET
/api/v2/gen/seq/{name}/next`; `GET /api/v2/gen/seq/{name}`; `DELETE
/api/v2/gen/seq/{name}`; `/api/v2/seq` alias via router-level redirect;
`sequences` and `gen_definitions` tables; `NEXT VALUE FOR name`,
`@SEQ('name')`, and `@GEN('name')` OQL extensions; `@GEN('name')` OQL
dispatch for sequences; `XOLU-GEN001` through `XOLU-GEN007`.

Sequences are the one piece of Part 1 that requires a non-trivial OQL
executor change. `NEXT VALUE FOR` cannot be implemented as a scalar function
because its correct semantics — increment once per row in a multi-row SELECT,
session-local for `@SEQ()` — require state that persists across
the evaluation of multiple rows in the same query. This is the same pattern
as a window function rather than a scalar. The OQL executor will need a new
node type for it, similar to how aggregate functions are handled.

The `UPDATE ... RETURNING` atomic increment in SQLite is straightforward.
The name-to-sequence lookup from the executor is the main new wiring.

**Estimated:** 1.5 weeks.

### S6 — `pkg/fsm/eval`

**What ships:** A new package containing a surgical extraction of
`ExpressionEvaluator`, `Value`, `ToValue`, and the `FunctionRegistry` from
aulsql's `pkg/tsqlruntime`. Added on top: `EvalGuard(expr string, vars
map[string]interface{}, payload map[string]interface{}) (bool, error)` and
`EvalSet(expr string, vars map[string]interface{}) (interface{}, error)`;
`tsqlparser` integration for parsing expression strings before evaluation;
payload binding convention documented and enforced (`payload.field` flattened
as `"payload.field"` key in the variable map); all four stateless generator
functions registered in the evaluator's `FunctionRegistry` so they are
available in FSM `set` clauses.

The key insight here is that the FSM walk runtime needs far less than the
full aulsql `ExecutionContext`. It needs the expression evaluator alone: a
variable map, a function registry, and a way to evaluate a parsed expression
against them. The `Interpreter`, `ExecutionContext`, `TempTables`, `Cursors`,
`ResultSets`, and the entire database/transaction machinery are left behind.
The amputated result is a package that has no transitive xolu dependencies
and can be tested entirely in isolation.

This is the package that must be most carefully tested in Part 1, because
every subsequent FSM operation depends on its correctness. The test suite
should cover the guard expressions and `set` clauses from the spec's
`AssetLifecycle` example in full, including the `@retries < 3` guard, the
`@retries + 1` set clause, and the `payload.result = 'pass' AND
payload.technician != ''` compound guard.

**Estimated:** 1 week.

### S7 — FSM definitions and machines

**What ships:** All six definition endpoints (create, list, get, replace,
delete, validate); all nine machine endpoints except `/walk`; the four FSM
SQLite tables (`fsm_definitions`, `fsm_machines`, `fsm_history`,
`fsm_terminal_states`); `fsm-toolkit` `pkg/fsm` integrated for structural
validation (`FSM.Validate()`, `FSM.Analyse()`) and reachability analysis at
definition creation time; guard expression strings parsed and syntax-checked
at definition creation time via `pkg/fsm/eval` (structural validity confirmed
by the toolkit; guard expression syntactic validity confirmed by the eval
package — these are separate checks on separate concerns); machine creation
with overrides; the `definition_deleted` flag in machine responses;
`XOLU-FSM001` through `XOLU-FSM013`.

To be explicit about the division: `fsm-toolkit` validates and traverses the
topology — states, transitions, reachability, determinism, output alphabet
consistency. It knows nothing about guards or variables. `pkg/fsm/eval`
parses and evaluates the T-SQL expression fragments in `guard` and `set`
fields at walk time. At definition creation time, `pkg/fsm/eval` is used
only to syntax-check guard expressions — confirm they parse without error —
not to evaluate them. Evaluation happens at walk time against live machine
variable values.

The prototype-snapshot design of the spec is unusual enough to deserve
attention. Definitions and machines are not in a class-instance relationship
where the machine defers to the definition at runtime. A machine is a
self-contained copy of its definition's properties at the moment of creation,
plus any overrides. After that moment, the machine and the definition are
independent. This means the `fsm_machines` table stores a full snapshot of
the transition table, guard expressions, variable declarations, and output
alphabet — not a foreign key to the definition. The `fsm_def_id` column
in `fsm_machines` records lineage but has no referential integrity constraint;
the definition may be deleted without affecting any machine.

Bundle composition (`linked_states`) is accepted in the definition schema
and stored without error. A machine created from a definition with linked
states is created successfully. A walk that reaches a linked state returns
`XOLU-FSM012` with a body clearly stating "bundle composition is not yet
implemented in the v2 preview." This is preferable to rejecting definitions
with linked states at creation time, which would prevent testing the
definition and machine lifecycle for any complex real-world machine.

Inline entity creation (the `entity` field in machine creation) is deferred
to Part 2. Machine creation with `ref` binding to an existing entity works.
Machine creation with no `ref` works.

**Estimated:** 2 weeks.

**Implementation decisions (accepted patch008):**

1. *Error code 007 gap is intentional.* The spec error table
   (`API_V2.md`) defines `XOLU-FSM001`–`006` and `XOLU-FSM008`–`013`.
   `XOLU-FSM007` is deliberately absent. The plan's phrasing "XOLU-FSM001
   through XOLU-FSM013" is not contiguous; the gap is preserved and codes
   are not renumbered. `pkg/errors` defines twelve FSM codes, not thirteen.

2. *`/walk` in S7 returns 501.* `/walk` belongs to S8. In S7 the route
   is registered and returns `501 Not Implemented` with a body pointing
   to S8, rather than 404 (route absent) or a structural FSM error. This
   keeps the route discoverable and consistent with the per-subsystem
   availability map, and means S8 replaces a stub rather than adding a
   route.

3. *Storage shape.* Definitions store the full spec as `spec_json`; the
   fsm-toolkit `FSM` is rebuilt from it on demand, not persisted as a
   binary. Machines store a self-contained `snapshot_json` (states,
   transitions, guards, variable declarations, output alphabet, and
   snapshotted child definitions for linked states). `fsm_machines`
   records `fsm_def_id` for lineage only, with no foreign-key
   constraint — the definition may be deleted without affecting any
   machine, per the prototype-snapshot model. `fsm_terminal_states` is a
   denormalised per-machine terminal-state index for fast walk checks.
   Integer IDs for definitions, machines, and history rows are allocated
   from a dedicated `fsm_id_seq(tenant_id, kind, next_id)` table using the
   atomic `INSERT ... ON CONFLICT DO UPDATE SET next_id = next_id + 1
   RETURNING` pattern already used by the node-sequence allocator, chosen
   over a per-insert `MAX(id)+1` scan for consistency with existing storage
   conventions and to avoid read-modify-write contention.

4. *Validation splits structural from expression concerns.* fsm-toolkit
   confirms topology (`Validate()`, reachability, determinism) and
   produces the analysis block (`Analyse()`); `pkg/fsm/eval.ParseGuard`
   syntax-checks every `guard` and `set` fragment without evaluating them.
   Evaluation happens only at walk time (S8).

5. *Code lands in three confirm-gated batches:* B1 schema + error codes +
   route skeleton + shared spec→FSM builder (build green, no endpoint
   logic); B2 the six definition handlers + tests; B3 the nine machine
   handlers + snapshot/override logic + tests. Version bump and CHANGELOG
   entry at the end.

### S8 — FSM walk

**Implementation note (patch009):** The walk runs in the storage layer as
`(*SQLiteStore).FsmWalkInTx(ctx, tx, …)`, alongside `saveInTx`/`createInTx`,
rather than in `handleCommit`. The plan's wording ("extends handleCommit by
executing the walk steps inside the same transaction") is satisfied by
calling `FsmWalkInTx` from `commitInner` on its existing transaction — the
walk is just another `*InTx` operation, so atomicity is automatic and no
cross-layer callback is needed. `pkg/storage` imports `pkg/fsm/eval` for
guard/set evaluation (no import cycle). The atomic sequence increment moved
to `storage.SeqIncrementTx`; the standalone `/walk` and `/seq/{name}/next`
handlers delegate to these storage helpers so each operation has one
implementation shared between standalone and transaction-embedded callers.
`NEXT VALUE FOR` in set clauses is resolved by AST substitution in
`pkg/fsm/eval` (reusing the tsqlparser `NextValueForExpression` node), with
the increment running on the walk transaction.

**Walk-engine fixes (patch010), surfaced by a packet-validator test machine:**

1. *Guard-disambiguated multi-edges.* The walk previously evaluated only the
   first transition matching `(state, input)` and rejected if its guard
   failed, so multiple guarded edges on one input did not work. The walk now
   collects all matching transitions in definition order and fires the first
   whose guard passes (`findWalkTransitions`); only if every guard is false is
   it `XOLU-FSM004`, and `XOLU-FSM003` is reserved for no matching input. This
   is what lets a state validate input by routing to accept/reject targets.
2. *Set clauses read payload.* `set` expressions could previously read only
   `@var`, not `payload.field`, so a transition could not capture incoming
   data into a variable. The walk now binds the payload into the set-clause
   evaluator, matching guard behaviour.
3. *`@var = expr` equality.* T-SQL parses `@var = expr` in a SELECT column as
   variable assignment, which silently dropped the comparison — every
   `@var = ...` guard evaluated to the truthiness of the right-hand side
   (e.g. `@retries = 3` was always true). `parseExpression` now detects the
   assignment misparse (`SelectColumn.Variable != nil`) and reconstructs it as
   an equality comparison. Inequalities and non-`@` equality were unaffected.

All three have permanent regression coverage: `v2_fsm_packet_test.go`
(the validator) and `pkg/fsm/eval/eval_equality_test.go`.

### Determinism declaration

Every definition must declare `determinism` as `strict`, `loose`, or
`firstmatch`. The field is mandatory and fail-closed: a definition without a
valid level is rejected at creation (`XOLU-FSM006`) and cannot be instantiated.
No default, no grandfathering.

- `strict` — at most one transition per `(state, input)`, enforced structurally
  via `firstDuplicateEdge`.
- `loose` — multiple edges permitted, guards must be provably mutually
  exclusive. Verified at creation by the exclusivity recognizer
  (`pkg/fsm/eval/exclusivity.go`, `CheckExclusivity`). A passing definition
  reports `exclusivity_verified: true`.
- `firstmatch` — multiple edges, first matching guard in definition order wins;
  order is semantic. This is the runtime behaviour the walk already had.

The recognizer is sound (never claims exclusivity it cannot prove) and
deliberately incomplete (rejects what it cannot prove; the author falls back to
`firstmatch`). It reduces each guard to a single-variable predicate (null state
plus an integer interval-set, or a var-vs-var relation) and proves pairwise
disjointness. Recognized shapes: null partitions, literal equality/inequality,
thresholds, bounded intervals, interval complements, null-OR-region, and
var-vs-var complementarity. It was built adversarially-first
(`pkg/fsm/eval/exclusivity_adversarial_test.go`), and the two extensions beyond
the initial scope — interval atoms and the null-OR-region disjunction — were
forced by the packet validator's real guards, confirming the implement-first
sequencing.

Inconsistent definitions get specific, actionable messages rather than a generic
failure: `strict` with multi-edges names the overloaded pair; `loose` with
overlapping guards names both guards and offers `firstmatch`; `loose` with an
unrecognized guard points to the recognized forms; `loose` with an unguarded
edge in a multi-edge group explains the always-fires problem. Tests:
`pkg/server/v2_fsm_determinism_test.go`.

Full conceptual and semantic documentation: [FSM.md](FSM.md).

**What ships:** `POST /api/v2/fsm/machine/{id}/walk`; guard evaluation via
`pkg/fsm/eval`; the atomic SQLite transaction covering state advance, `set`
clause evaluation, sequence increment (if S5 is complete), and history
append; Mealy output recorded in history and returned in the response;
`fsm_walk` field in `CommitRequest` wired into `handleCommit` atomically;
`XOLU-FSM003`, `XOLU-FSM004`, `XOLU-FSM005`, `XOLU-FSM008`, `XOLU-FSM011`.

The walk sequence has two distinct evaluation steps that must not be
confused. First, `fsm-toolkit`'s `FSM.GetTransitions(currentState, input)`
returns the candidate transitions from the structural machine — this is a
pure graph lookup with no expression evaluation. Second, if the candidate
transition has a `guard` field, `pkg/fsm/eval` evaluates the T-SQL guard
expression against the machine's current variable values and the walk
payload. Only if the guard passes does the walk proceed to modify state.

The full sequence: look up the machine; check it is not in a terminal state
(XOLU-FSM005); call `GetTransitions` for the current state and input
(XOLU-FSM003 if none found); evaluate the `guard` expression via
`pkg/fsm/eval` if present (XOLU-FSM004 if false); open the SQLite
transaction; advance state; evaluate `set` clauses via `pkg/fsm/eval`
updating variable values; if any `set` clause contains `NEXT VALUE FOR`,
increment the sequence atomically within the same transaction; append the
history entry including the payload, the variable snapshot after `set`
evaluation, and any outputs; commit.

If any step from the state advance onwards fails, the transaction is rolled
back and nothing is written. This is the atomicity requirement referred to
in the constraints section. It is not sufficient to advance the state and
then record the history in a second write — these must be one transaction.

Walk does not fire event defs in Part 1. Mealy outputs are recorded
in the walk response and in the history entry. The event-def dispatch
mechanism is wired in S9; if S8 is deployed before S9, outputs are silently
recorded but not acted upon.

`fsm_walk` in `/api/v1/commit` adds the walk to the same SQLite transaction
as the entity upsert. If the walk guard fails, the entire commit fails. If
the entity write fails, the state does not advance. The implementation
extends `handleCommit` by checking for the `FsmWalk` field after the entity
transaction opens and executing the walk steps inside the same transaction
before the commit.

**Estimated:** 2 weeks.

### S9 — Event defs (basic)

**What ships:** All eight event-def management endpoints; `event_defs`
and `event_delivery_log` tables; `entity.created`, `entity.updated`, and
`entity.deleted` event types wired into the existing CRUD handlers;
`fsm.output` event type wired into the S8 walk path so Mealy outputs dispatch
event defs; `webhook` action (async, one delivery attempt, failure logged);
`oql` action (async); delivery log accessible via `GET /api/v2/event/{id}/log`;
`XOLU-EV001`, `XOLU-EV002`, `XOLU-EV003`.

All actions in Part 1 execute asynchronously regardless of the `execution`
field. An event def declaring `"execution": "sync"` is accepted, stored,
and executed, but the execution is async and the response includes an
`X-Executed-As: async` header noting the downgrade. This is intentional:
sync execution requires the action to run inside the triggering transaction,
which interacts with the SQLite busy-timeout in ways that need careful
handling. That belongs in Part 2.

Template variable substitution (`{{event.id}}`, `{{event.entity}}`, etc.)
is implemented for `oql` and `webhook` actions. Generator template variables
(`{{gen:uuid_v4}}`, `{{gen:name}}`) are substituted by calling the relevant
generator function at dispatch time.

Several features are explicitly deferred to Part 2 and their absence
documented: dead-letter and replay; retry and backoff (one attempt only in
Part 1); `POST /api/v2/event/{id}/test`; the `fsm.walk`, `sulpher`,
`graph.edge.*`, `ts.appended`, `fsm.entered`, `fsm.exited`, and `fsm.terminal`
event types and action types.

**Estimated:** 2 weeks.

### S10 — Remaining stateful generators

**What ships:** `token`, `timestamp`, `random_int`, `pick` (random mode and
round-robin mode), `nanoid`, and `slug` generator types;
`GET /api/v2/gen/{type}/{name}/next` for all types; `@GEN('name')` OQL dispatch
extended to all stateful types; `XOLU-GEN001` through `XOLU-GEN010`.

Each generator is a small, self-contained function. The interesting pieces
are: `timestamp` uses Go's embedded IANA tz database (`time/tzdata`) so no
OS timezone files are required;
round-robin cursor for `pick` is in memory (acceptable in Part 1, persisted
in Part 2 via S21); `pick` weighted mode and
configurable `slug` vocabulary are deferred to Part 2.

The `@GEN('name')` OQL function dispatches to the `gen_definitions` table to
find the generator type, then calls the appropriate `/next` implementation.
The dispatch is a table lookup at query time, not at registration time.

**Estimated:** 1.5 weeks.

### S11 — Error codes, tests, documentation, release prep

**What ships:** All `XOLU-FSM*`, `XOLU-EV*`, `XOLU-META*`, and `XOLU-GEN*`
codes registered in `pkg/errors`; a happy-path end-to-end test for each
subsystem covering the core scenario from the spec; the composite pipeline
integration test (commit + walk + output + event def); an addition to
`docs/CHANGELOG.md`; a note in `docs/KNOWN_ISSUES.md` listing what is not
yet implemented in the v2 preview; version bump to `1.0.0`.

The integration test target is the full pipeline from the spec:

```
POST /api/v1/commit  (document update + FSM walk, atomic)
  └── fsm_walk: machine 1001, input "inspection_passed"
        └── guard: payload.result = 'pass' AND @retries < 3  →  true
        └── state: AwaitingInspection → InService
        └── set: @retries = 0
        └── Mealy output: "asset_activated"
              └── event def "asset-activated-notify"
                    └── webhook: POST https://...  (async, one attempt)
```

This test demonstrates that the core value proposition of the subsystem
works end-to-end. If this test passes, Part 1 has achieved its goal.

**Estimated:** 1 week.

**Part 1 total: approximately 14 weeks.**

---

## Part 2 — Functionally correct towards xolu 2.0

Part 2 completes the specification. The API surface may change from Part 1;
breaking changes to v2 endpoints are acceptable before 2.0. The flag default
flips to `true` during this phase once the implementation is judged stable
enough to be on by default for new deployments.

### S12 — Bundle composition

**What ships:** `linked_states` in definitions resolved and executed at walk
time; child machine snapshot taken from the child definition at parent machine
creation time; `BundleRunner` from fsm-toolkit wired into the walk path;
correct history entries for delegated steps referencing the child machine;
`XOLU-FSM012` replaced with real delegation behaviour.

The toolkit's `BundleRunner` handles the delegation stack mechanics: the
parent pauses, the child runs to a terminal state, the child's result
(`accept` or `reject`) is returned to the parent, the parent continues or
faults according to its linked-state transition table. The xolu-specific
work is wiring the BundleRunner into the walk transaction and ensuring that
history entries across parent and child machines are written atomically.

The child machine snapshot model from S7 is reused: at parent machine
creation time, the linked definition IDs are resolved and the child
definitions are snapshotted alongside the parent. If a child definition does
not exist at that moment, `XOLU-FSM012` is returned. If the child definition
is later deleted, the parent machine's snapshot contains the child state it
needs and continues to operate.

**Estimated:** 2 weeks.

### S13 — Sync event actions

**What ships:** `"execution": "sync"` executes `oql` and `sulpher` actions
inside the triggering SQLite transaction; failure rolls back the triggering
operation with `XOLU-EV005`; timeout is enforced via `XOLU_SQLITE_BUSY_TIMEOUT`;
the Part 1 async-downgrade is removed; the `X-Executed-As: async` header is
no longer emitted.

This is an intentional breaking change for callers that relied on the Part 1
downgrade behaviour. The change is declared in the `UPGRADE.md` document with
a migration note: callers that want the old async behaviour should set
`"execution": "async"` explicitly.

Webhooks are always async regardless of the `execution` field. This is by
design and specified in the API.

**Estimated:** 1.5 weeks.

### S14 — `fsm.walk` event action and `@FSM()` OQL function

**What ships:** `fsm.walk` action type in event defs; `@FSM(input,
payload_json)` OQL function evaluating against `_api_v2_machine_`; `WHERE`
clause parsing and validation with `XOLU-EV006`; bulk walk dispatch returning
`{machine_id, previous, current, success, error}` result rows; `{{gen:*}}`
template variables in the `where` and `payload` fields of `fsm.walk` actions.

The `@FSM()` OQL function is a new executor node type, similar in structure
to `NEXT VALUE FOR`. It takes an input name and a JSON-serialised payload,
evaluates the WHERE clause to select machines, walks each matching machine,
and returns a result set. It is the mechanism that the event system uses for
reactive FSM walking.

The `where` field in an `fsm.walk` action is an OQL WHERE clause fragment
evaluated against `_api_v2_machine_` at dispatch time. It is not a template —
it is a parameterised query with `{{event.*}}` variables substituted as typed
parameters before execution. The distinction matters for injection safety.

**Estimated:** 2 weeks.

### S15 — Additional event types

**What ships:** `graph.edge.added` and `graph.edge.removed` event types wired
into the graph edge write path; `ts.appended` event type; `fsm.entered`,
`fsm.exited`, and `fsm.terminal` event types wired into the walk path;
`meta.expired` event type wired into the `meta.GCSweeper` so that expiry
events dispatch registered event defs with context
`{ entity, id, key, value, expired_at }`.

The `ts.appended` event type deserves specific attention. Timeseries append
throughput can be high — a single timeline may receive thousands of events
per second. Dispatching an event def for every appended event is a
plausible way to create a delivery storm that overwhelms the webhook
delivery queue. A debounce or sample-rate option on `ts.appended`
event defs is introduced here: `"sample_rate": 0.01` fires for
approximately 1% of events; `"debounce_ms": 1000` fires at most once per
second per timeline. Both are optional and default to no throttling. The
option exists to be used deliberately, not imposed by default.

**Estimated:** 1.5 weeks.

### S16 — Dead-letter and replay

**What ships:** Dead-letter promotion after all retry attempts are exhausted;
`GET /api/v2/event/{id}/deadletter`; `GET /api/v2/event/{id}/deadletter/{entry_id}`;
`POST /api/v2/event/{id}/deadletter/{entry_id}/replay`; `DELETE` for
dismissal without retry; `XOLU-EV007`, `XOLU-EV008`; `event.GCSweeper` for
delivery log retention with dead-letter entries exempt from purge.

The exemption from log retention purge is the critical correctness
requirement here. The `event.GCSweeper` must track which entries are in
dead-letter status and skip them during retention sweeps. An entry that has
been promoted to dead-letter must not be silently deleted before an operator
has had the opportunity to inspect and replay it. The GCSweeper is
consequently also registered with `pkg/gc` and available via the admin
endpoint.

Replay re-enqueues the entry with the original event context but executes
against the event def's current configuration — if the event def has
been updated since the failure, the updated configuration applies to the
replay. If the event def has been disabled, replay returns `XOLU-EV008`.

**Estimated:** 1.5 weeks.

### S17 — Retry and backoff

**What ships:** Configurable retry policy on webhook event defs
(`max_attempts`, `backoff` with values `exponential`, `linear`, `fixed`);
at-least-once delivery guarantee; retry state persisted across server restarts
via an `event_delivery_queue` table.

Part 1 made one delivery attempt per event-def firing. Correct retry
behaviour requires the delivery queue to survive a server restart — the
simplest durable implementation is a SQLite table that the async delivery
worker drains. The worker reads pending entries, attempts delivery, records
the outcome, schedules the next attempt according to the backoff policy, and
eventually promotes to dead-letter after `max_attempts`.

At-least-once means a delivery may be attempted more than once even if the
first attempt succeeded, in the case of a crash between delivery and result
recording. Event-def actions must be idempotent if exact-once semantics
are required; this is documented in the API.

**Estimated:** 1.5 weeks.

### S18 — Event test invocation

**What ships:** `POST /api/v2/event/{id}/test` with context shape validation
against the event def's event type; synthetic execution with no state
change; `XOLU-EV009`; synchronous return regardless of the event def's
execution model.

The test endpoint is small but depends on the delivery path being solid.
It fires the event def once with a synthetic event context, validates
that the context shape matches the event type (a `fsm.output`
context must have `machine`, `output`, and `payload`; an `entity.created`
context must have `entity`, `id`, and `data`), executes the action, and
returns the result synchronously. Nothing in xolu's persistent state is
modified.

**Estimated:** 0.5 weeks.

### S19 — Inline entity creation in machine

**What ships:** The `entity` field in `POST /api/v2/fsm/machine`, atomically
creating the entity and the machine in the same transaction; automatic `ref`
binding to the new entity; rollback of both entity and machine if either
fails.

This stage extends the machine creation handler to participate in the SQLite
write transaction for the entity. The entity creation path in v1 produces an
integer ID; the machine creation uses that ID to populate `ref` before
committing. If the machine creation fails validation after the entity is
created (for example, the definition has been deleted between the creation
request and the transaction), both are rolled back.

**Estimated:** 1 week.

### S20 — `/api/v1/commit` + FSM walk hardening

**What ships:** Full correctness audit of the `fsm_walk` field in
`CommitRequest`; version conflict detection producing `XOLU-FSM008`; guard
failure rolling back the entire commit including any timeseries writes; all
error codes from the FSM walk spec returned with the full `detail` fields
specified.

The Part 1 implementation wires the walk field and achieves the basic
atomicity requirement, but the failure path detail may be incomplete —
particularly the interaction between a failed walk guard and a successful
timeseries write that has already been made to Pebble (which requires a
`DeleteKeys` rollback, the same pattern used by the existing commit path for
timeseries failures). This stage audits and corrects every failure branch.

**Estimated:** 1 week.

### S21 — Generator hardening

**What ships:** `pick` weighted mode; configurable `slug` vocabulary (both
built-in presets and custom word lists); round-robin cursor for `pick`
persisted across restarts; `@GEN('name')` available in the tsqlruntime expression evaluator so
it can be used in FSM `set` clauses alongside `NEXT VALUE FOR`.

The Part 1 round-robin cursor used in-memory state that resets on restart. The spec documents the known limitation for
round-robin explicitly; this stage eliminates it. Persistence is via the
`gen_definitions` table with a `state` JSON column updated on each call —
simple enough to be correct, fast enough given generator call rates.

`@GEN('name')` in the tsqlruntime evaluator is the piece that allows event
template variables and FSM `set` clauses to call stateful generators. The
Part 1 `pkg/fsm/eval` package registers stateless generator functions
directly; this stage registers a `GEN` dispatcher that looks up the named
generator and calls its `/next` logic.

**Estimated:** 1 week.

### S22 — `@SEQ()` session semantics

**What ships:** True session-local last-value tracking for `@SEQ()`
in OQL; `XOLU-GEN006` returned when called before `NEXT VALUE FOR` in the
same session; correct per-row increment semantics for `NEXT VALUE FOR` in
multi-row SELECT results.

The Part 1 implementation may have used the sequences table's current value
as a proxy for the session-local last value, which is incorrect when multiple
callers are using the same sequence concurrently. Correct session-local state
requires per-connection or per-query-context tracking rather than reading
from the shared table. The OQL executor already manages per-query context for
aggregate functions; the same mechanism is extended here.

**Estimated:** 0.5 weeks.

### S23 — Full error code coverage

**What ships:** Every `XOLU-FSM*`, `XOLU-EV*`, `XOLU-META*`, and `XOLU-GEN*`
code exercised by a test; all error responses carrying `detail` fields as
specified in the API; guard failure responses including the evaluated values
in the `detail` field (`"guard: @retries < 3 evaluated false (current: 3)"`).

This is an audit stage rather than new functionality. The test suite from
S11 covers the happy path. This stage adds adversarial test coverage
comparable to the v1 adversarial test campaign — error paths, boundary
conditions, missing fields, invalid values, and cross-subsystem failure
interactions.

**Estimated:** 1 week.

### S24 — FSM GC

**What ships:** `fsm.GCSweeper` implementing stalled machine collection
(`stalled_after` policy) and dead machine collection (`dead_after` policy);
the `on_gc_collect` input walk before deletion, producing a proper history
entry; `XOLU_FSM_GC_MIN_AGE` floor preventing collection of newly-created
machines; per-definition `gc` policy respected; integrated into `pkg/gc.Worker`
and registered at server startup.

The FSM GC is straightforward once the walk runtime (S8) and bundle
composition (S12) are both correct. Stalled machines are those in a
non-terminal state with `last_walked_at` older than `stalled_after`. Dead
machines are those in a terminal state with `last_walked_at` older than
`dead_after`. The GC sweeper runs the `on_gc_collect` input against the
machine before deletion if configured; if the walk fails (because the machine
is already in a terminal state, for example), the machine is collected anyway
with a `gc_collected` history entry.

The minimum age floor `XOLU_FSM_GC_MIN_AGE` (default `1h`) prevents the GC
from touching machines that have never been walked. Without this floor, a
machine created against a definition with a short `stalled_after` policy
could be collected before the owning process makes its first walk.

**Estimated:** 1 week.

### S25 — API surface stabilisation

**What ships:** All intentional breaking changes from Part 1 made and
documented in `UPGRADE.md`; endpoint shapes normalised against the spec;
field renames or semantic corrections applied; `X-API-Stability` header
updated from `experimental` to `stable` or removed; flag default flipped to
`true`; complete review of `API_V2.md` against the implementation for any
deviations.

This stage exists because the "API surface may vary" promise in the v2
preview is a real promise, not just a disclaimer. If any endpoint shape,
error code, or request body field has drifted from the spec during
implementation, this is the stage to either align the implementation or
deliberately update the spec with a rationale. After this stage the v2
surface is frozen for 2.0.

**Estimated:** 1.5 weeks.

### S26 — Documentation, tests, release

**What ships:** Full adversarial test coverage for all v2 subsystems;
updated `docs/API_V2.md` reflecting any surface changes from S25; errors
document updated; CHANGELOG; version 2.0.0.

**Estimated:** 1.5 weeks.

**Part 2 total: approximately 19 weeks.**

---

## Summary

| | Duration | What it delivers |
|--|----------|-----------------|
| **Part 1** | ~14 weeks | Demonstrably working v2 draft, flag-gated, ships with xolu 1.0. Every subsystem runs the spec's own examples. Walk is atomic. Event delivery is async and single-attempt. Bundle composition is stubbed with a clear error. |
| **Part 2** | ~19 weeks | Functionally complete v2. Sync event execution. Dead-letter and replay. Retry and backoff. Bundle composition. Full error matrix. GC for FSM and events. API surface frozen for 2.0. |
| **Combined** | ~33 weeks | |

The sequencing within each part is designed so that every stage ships
something independently demonstrable. The constraint that nothing in Part 1
may interfere with v1 behaviour is enforced architecturally by the flag and
the lazy table creation, not by convention.

---

*The API surface described here is subject to change before xolu 2.0.*
*See `docs/API_V2.md` for the current specification.*
