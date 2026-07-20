# dxp — Declarative Composed Commitment (proposal)

Updated: 2026-07-19
Status: proposal — not scheduled. Companion to
`chronicle-substrate.md` (whose commitment primitives are this
primitive's participants) and `bal-conservation-primitive.md` (whose
holds, @D10 there, are one participant mechanism). The pattern theory —
the phase spectrum, 2PS, 3PS, modifiers, and their proofs — lives in
the DXP framework repository (`github.com/ha1tch/dxp`); this document
is its executable instantiation on the xolu substrate. No register
items exist until execution is decided.

## 1. What dxp is

A substrate primitive for **composed atomic commitment across
heterogeneous primitives**: book the slot ∧ debit the account ∧ stage
the document ∧ advance the workflow — one outcome, all or nothing,
with the record proving which.

It has two faces, following the house's definition-first doctrine
(schemas for shape, `fsm/def` for process legality):

- **`dxp/def`** — a stored, versioned, validated *transaction
  definition*: pattern, participant roster with typed parameter slots,
  phase deadlines.
- **`dxp/txn`** — an *instance*: a definition instantiated with
  concrete bindings, driven through its phases to exactly one terminal
  outcome.

With this primitive in place the substrate reads cleanly top to
bottom: **fsm machines orchestrate business processes, whose
transitions fire dxp defs, which compose guard-bearing primitives (cal,
bal, blob, entities, fsm-as-participant), over the chronicle substrate,
on one transaction** (@D08a for the fsm relations; @D06 for the
one-transaction claim).

## 2. The claim, stated carefully

Multi-model databases (and FoundationDB's layer architecture, the
strongest prior art) compose heterogeneous **storage models** under one
atomic commit. What none of them ship is atomic commitment across
**promise-bearing primitives** — participants that each carry their own
admission guard: a calendar that refuses double-occupancy, a ledger
that refuses overdraft, a lease that refuses double-acquisition, a
state machine that refuses illegal transitions. A dxp commit means
*every guard consented, atomically, or nothing happened*.

That is not multi-model atomicity; it is **multi-promise atomicity**,
and we know of no shipping database that offers it.

The strength comes from where the reservations live. In the
microservice architectures the DXP framework was written for, a
participant's "prepare" is application code — advisory, hopefully
honoured. Here, a reservation is **guard-enforced by the substrate**: a
bal hold genuinely counts against admission, a cal proposal genuinely
occupies the slot, and no concurrent writer can violate either. Phases
over physics, not phases over promises.

## 2a. Position against the classical pattern lineage

The claim in @D02 concerns *what is composed*; this section positions
*how*, against the four fixed stars of the classical taxonomy — because
collapsing this design onto its oldest ancestor ("it's 2PC") misreads
both.

- **2PC**: blocking, lockstep, coordinator-fragile. **2PS is not 2PC**:
  non-blocking, parallel within phases, saga-lineage failure handling
  behind a prepare gate — a point the classical taxonomy does not name.
- **3PC (Skeen)**: still lockstep; aimed at coordinator failure.
  **3PS is not 3PC**: reserve/validate/execute with *independent
  per-participant phase progress* — participants simultaneously in
  different phases, races caught by validation rather than prevented by
  locks. No classical pattern has this property.
- **Saga**: no reservation at all; compensation as apology after the
  fact.
- **TCC**: the nearest ancestor — and its founding weakness is that
  *try* is advisory application code. This design's reserve verb is
  **guard-enforced by the substrate** (@D02, @D05), converting the pattern
  into a guarantee. Escrow transactions are the academic ancestry of
  the reservation idea; ancestry is not priority.

The framework this instantiates (`github.com/ha1tch/dxp`) contributes
the *spectrum* (0.5 → 3 phases, with the intermediate points named),
a composable modifier algebra (TBS, OV, GA, SC), and formal proofs for
2PS, 3PS, and 3PS+quorum — a synthesis the distributed-transactions
literature does not otherwise have in unified form. This document adds
three things on top: guard-enforced reservations, the degradation
theorem (@D06 — machinery that statically selects the cheapest viable
point on the spectrum, per definition), and declarative definitions
whose canonical participant ordering yields deadlock-freedom **by
construction** where the industry offers detection and retry.

Context that weighs the above: the modern industry has not hardened
distributed transactions — it has *retreated* from them (XA avoided by
folklore; "sagas and eventual consistency" as official doctrine). This
design advances where the field withdrew; the degradation theorem is
what makes the advance affordable rather than heroic.

## 3. Definitions — `dxp/def`

A definition declares:

```json
{
  "def_id": "place_order",
  "version": 3,
  "pattern": "2ps",
  "participants": [
    { "id": "stock",   "primitive": "bal",    "op": "transfer",
      "params": { "from": "$warehouse", "to": "$shelf", "amount": "$qty" } },
    { "id": "payment", "primitive": "bal",    "op": "transfer",
      "params": { "from": "$customer_acct", "to": "~received", "amount": "$price" } },
    { "id": "slot",    "primitive": "cal",    "op": "book",
      "params": { "calendar": "$courier_cal", "span": "$delivery_span" } },
    { "id": "order",   "primitive": "entity", "op": "create",
      "params": { "type": "orders", "data": "$order_doc" } }
  ],
  "phase_ttl": { "reserve": "PT2M" }
}
```

Parameter slots are **typed** (`$qty: int`, `$delivery_span:
cal.span`); bindings are validated against slot types at
instantiation.

What declaration-time buys — the reason definitions are the primitive's
centre of gravity rather than a convenience:

1. **Validation once, not per run.** Participants exist, ops are
   supported, TTLs are sane, slots are complete — checked when the def
   is registered, refusing a class of failure before any instance runs.
2. **The transaction becomes an artefact.** "What actually happens when
   we place an order" is today imperative code smeared across an
   application. A def is a versioned document in the substrate: the
   operator reads it, the auditor cites it, and every instance records
   `def_id@version` — so *"all `place_order` runs that expired in
   reserve this week"* is a query, not instrumentation.
3. **Static analysis.** At registration the substrate precomputes
   whether the participant set is single-tenant (fixing the execution
   strategy, @D06, before any instance exists) and fixes a **canonical
   participant ordering per def** — yielding deadlock-freedom by
   construction, a guarantee imperative composition cannot offer
   because imperative code chooses its ordering at runtime, per call
   site, differently.

Versioning inherits the fsm discipline: in-flight instances complete
under the version that started them; new instantiations take the
current version. Definitions are immutable per version.

## 4. Instances — `dxp/txn`

`POST dxp/txn {def_id, bindings}` creates a durable instance record:
def@version, resolved participants, phase, per-participant state,
deadlines, outcome.

The instance is a small state machine (concretely: guarded transitions
in the T-34 CAS pattern — per the chronicle document's refusal, *not*
ridden on `pkg/fsm`). Its admission guard — because every xolu
primitive is its guard — is **outcome uniqueness**:

- exactly one terminal state per instance: `committed`, `released`,
  or `expired`;
- participant executions occur iff the instance committed;
- phase-order violations are refused (no execute before the prepare
  gate closes; no action on a terminal or expired instance);
- all coordinator verbs are idempotent — retries are safe.

**Method is configuration.** The def-level `pattern` field selects the
transaction method across the framework's full spectrum, and the
engine representation underneath is **per-participant phase profile**,
with `pattern` as shorthand for the uniform cases: `0.5ps`
(fire-and-forget), `1ps` (saga: execute-only with compensations),
`2ps` (pessimistic reservations behind one gate), `3ps` (optimistic
reservations with validate), and the mixed intermediates (`1.5ps`,
`2.5ps`) as declared per-participant mixtures. True **2PC is the
degenerate label for the collapsed path** (@D06): when static analysis
proves the participant set single-tenant, prepare and commit are fused
by the shared file's physics. Registration-time analysis validates
method-mixture soundness (per the framework's pattern-mixing rules),
so an unsound combination is a def-registration refusal, never a
runtime discovery. Time-Bounded States is not a modifier here — it is
native (@D07, @D05b).

## 5. The participant contract

Four verbs, uniform across primitives:

| Verb | Meaning |
|---|---|
| `Reserve` | Take a guard-enforced prepared state, under TTL |
| `Validate` | Confirm the reservation still satisfies the guard |
| `Execute` | Convert reservation into committed effect |
| `Release` | Return the reservation; idempotent with expiry |

Mapping to the substrate:

| Primitive | Reserve | Execute | Status |
|---|---|---|---|
| cal | `propose` (TTL native) | `confirm` | **Shipped**, stress-verified |
| bal | hold (authorize) | capture | Designed (@B10; schema room left) |
| blob | lease + staged write | promote | Proposed (blob mutation arc) |
| entity | staged write — validated draft under TTL | promote atomically | Dissolved by @D05b: entities inherit tentative rows from the engine facility |
| fsm | tentative walk-log row (pointer unmoved) | `SetStateFrom` CAS | Resolved — @D05c |
| ts | — (execute-only) | append observation | Trivial by nature |

Participants come in two classes, and the split is the substrate's own
taxonomy surfacing inside the contract: **guard-bearing** primitives
(cal, bal, blob, entity, fsm) make promises, so they reserve, validate,
and can refuse; **observational** primitives (ts) answer questions, so
nothing about them can be refused — they participate **execute-only**,
their appends performed iff the instance commits. An observation of an
event that atomically did-or-didn't happen inherits the transaction's
outcome for free.

The contract is the primitive's **export**: it is written for two
consumers — dxp's own coordinator today, and nolu's tomorrow (@D08).

## 5b. Reserved commits: engine-level precommit semantics

The participant mechanisms in @D05 (cal proposals, bal holds, blob
leases, entity staged writes) are four bespoke implementations of one
lifecycle. The engine therefore owns that lifecycle as a substrate
fundamental — the **reserved commit** — and primitives supply only
their conflict predicate and a weight policy.

**The convention.** Guard-bearing tables adopt tentative rows:
`(txn_id, state, reserve_deadline)`, `state` defaulting to
`committed`. A reserved row walks exactly one path:

`reserved → confirmed | released | expired | invalidated`

- **confirmed**: the owning transaction executes within the TTL —
  a CAS flip (`… WHERE txn = ? AND state = 'reserved' AND deadline >
  now`), rows-affected checked;
- **released**: explicitly returned;
- **expired**: the TTL sweeper (@D07) collects it — a reservation is
  honoured only until its deadline;
- **invalidated**: a conflicting transaction confirmed first (below).

**The two weights.** Each participant declares how its reserved rows
count in admission:

- **Pessimistic** (2PS semantics): reserved rows are *honoured like
  real commits* by every guard — a reserved debit reduces available
  balance, a reserved slot refuses competitors — until confirmation or
  expiry.
- **Optimistic** (3PS semantics): reserved rows are invisible to
  admission; conflicting reservations coexist. Confirmation runs
  validate; the **first confirmer wins** — its committed state is what
  makes the losers' confirm-CAS fail (their validate sees the resource
  taken → XOLU-DXP003) — with the serialisation point provided for
  free by the shared file's single writer.

**Visibility rule.** Application reads see `committed` only. Guard
reads see committed plus reserved *per the declared weight*. Nothing
else, anywhere in the engine, ever needs to know reservations exist.

Every substrate law holds by construction: tentative rows live in the
same tables and transactions as committed rows (guard locality @D04a of
the chronicle doc), the same file (composability locality, @D06), and are
swept on expiry (finiteness @D04b). Consequences: the @D05 adapters
collapse to a predicate plus a weight bit; the **entity staged-write
gap dissolves** — entities inherit tentative rows from the facility;
and pattern mixing (@D04) is trivial because weight is per-participant
configuration. bal's journal `state` column (@B10) is this
convention's first schema instance, promoted here to substrate-wide
practice.

## 5c. Reservation visibility across the whole substrate

Extending @D05b beyond the commitment primitives — to entities, fsm
machines, and every derived plane — forces a complete visibility
taxonomy. Three tiers:

1. **Guard plane — tentative-aware, mandatory.** Every SQL table any
   guard reads carries the tentative convention. This includes the
   **edge table**: a pessimistic reserved entity's refs are state-tagged
   edge rows at reserve (flipped at confirm), so RI's restrict can see
   them — a pending order legitimately blocks deletion of the customer
   it references during its window. Corollary doctrine: **guards never
   use accelerators** — enforcement reads go to the guard plane always;
   in-memory accelerators serve application traversal only (a
   commit-fed accelerator answering a tentative-aware question would
   answer wrongly).
2. **Advisory planes — weight-optional, sweep-cleaned.** Derived
   structures serving availability-type reads (cal's occupancy index
   feeding openings search) may ingest reservations when the
   primitive's weight is pessimistic — otherwise search advertises
   slots that propose-storms then fight over. Correctness never
   depends on this tier; any plane that ingests reservations joins the
   sweeper's cleanup obligations. cal's existing proposal-visible
   occupancy is this tier, implemented before the taxonomy named it.
3. **Analytic and read planes — commit-fed, strictly.** Rollups
   (ts buckets, bal cascade deltas, checkpoints), FTS, events, caches,
   and the in-memory graph ingest at confirm only. A reserved document
   is unsearchable; `entity.created` fires when the entity is real;
   reserved state never cascades into any rollup — consistent with the
   standing law that no guard consults a rollup, and giving reserve its
   phase economics (one row insert, zero index churn; confirm carries
   the derived work via the RETURNING-fed pattern).

**fsm participation, resolved** (closing @D05's "design open"): Reserve
inserts a tentative walk-log row; the current-state pointer does not
move. Pessimistic weight: a reserved transition refuses competitors —
the machine is locked mid-step. Optimistic: competing reserved
transitions from one state coexist; Execute is the `SetStateFrom` CAS
(the T-34 mechanism, reused verbatim), and the first confirmer's
pointer move is what fails the losers'. Guard expressions re-evaluate
at Validate, since machine data may change during the window — 3PS
validate semantics arising from fsm's existing machinery.

## 5a. Worked example: a hotel reservation, whole-substrate

The difference this primitive makes is clearest when one business
operation touches *every* kind of primitive at once — which is what
real operations do:

```json
{
  "def_id": "hotel_reserve",
  "version": 1,
  "pattern": "2ps",
  "participants": [
    { "id": "room",    "primitive": "cal", "op": "book",
      "params": { "calendar": "$room_cal", "span": "$stay" } },
    { "id": "payment", "primitive": "bal", "op": "transfer",
      "params": { "from": "$guest_acct", "to": "~received", "amount": "$paid" } },
    { "id": "booking", "primitive": "fsm", "op": "transition",
      "params": { "machine": "$booking_fsm", "input": "confirm" } },
    { "id": "audit",   "primitive": "ts",  "op": "append",
      "params": { "series": "bookings.confirmed", "value": 1 } }
  ],
  "phase_ttl": { "reserve": "PT90S" }
}
```

One instantiation, one outcome. Prepare: the room's occupancy is
reserved in cal (guard: nobody else holds those nights), the payment is
held against the guest's balance in bal (guard: funds suffice), the
booking's `confirm` transition is reserved in fsm (guard: the walk is
legal from its current state). Any guard refuses → every reservation
releases, XOLU-DXP002 names the refuser carrying its own error
(XOLU-CAL, XOLU-BAL, XOLU-FSM), and *nothing* — not the calendar, not
the books, not the workflow, not the audit trail — records a
half-booking. All guards consent → execute: occupancy confirmed, money
moved and the accounting balance updated, machine advanced, and the ts
observation appended — the audit series recording only events that
atomically happened.

Every system that runs hotels today implements this as application
code praying across four stores. Here it is one declared artefact, one
guard-enforced outcome, and — when all participants resolve to one
tenant, which a hotel's always do — one SQL transaction (@D06).

## 6. Execution strategy: degradation to ACID

Every **guard-bearing** participant's tables for one tenant live in one
SQLite file — a deliberate layout fact, not an accident: entities,
graph, seq, meta, and cal's SQL records share `store/xolu.db` (bal
joins them), while observational and derived planes (ts, the cal
occupancy index, blobs) run on their own engines. This is
**composability locality** — primitives that must commit together live
together; primitives that never feed a guard get their own throughput
physics (Pebble group-commit for telemetry, at rates far above the SQL
writer's). Two consequences: the single-file rule is what makes the
collapse below *sound* (SQLite's cross-database atomicity does not
survive WAL mode, so scattered files would silently break it), and the
per-tenant writer means machine throughput aggregates linearly across
active tenants — the ~5k/s single-writer ceiling bounds one tenant's
*commitments*, not the machine.

When a def's static analysis proves the participant set single-tenant,
the coordinator **collapses the phases into a single SQL transaction**:
same API, same instance record, same guarantees — literal ACID, zero
coordination overhead. When a future driver spans that boundary (@D08),
the same definition runs true phased execution with enforced
reservations.

Applications therefore write against dxp once, and their code survives
every future topology decision unchanged. The consistency machinery
degrades gracefully into ACID when topology permits — the pattern
spectrum's cheapest point, chosen automatically.

A **degradation-equivalence test** is a standing obligation: the same
def driven both ways must produce identical outcomes and records.

## 7. Recovery

Coordinator death mid-flight is the classic wound of the 2PC lineage.
The answer is already in the house: instance records are durable;
every non-terminal phase carries a deadline; a sweep worker (the
existing GC-worker abstraction) expires overdue instances and releases
their reservations — participant TTLs guaranteeing that even an
unswept reservation self-releases. Time-Bounded States is not an
enhancement here; it is the crash-safety mechanism.

## 7a. Where the clock lives

Instance deadlines and TTL state live **in the instance's own rows**,
not in any participant primitive. The deadline is a guard input: the
expiry decision (past deadline → `expired` → release) is a guarded
transition whose read and write must commit together — guard locality
(@C04a) settles the placement before taste
enters. Concretely: deadline columns on the instance record, a partial
index over non-terminal instances, and the sweep as a query plus the
coordinator's own CAS transition machinery.

Explicitly rejected allocations:

- **cal is not a timer service.** cal models contended occupancy of
  bookable time — proposals, exclusivity, availability search, seal. A
  TTL is a timestamp nobody contends for or searches; routing it
  through occupancy machinery is a category error, and storing
  coordinator state inside a *participant* recreates the circularity
  @D08a refuses.
- **bal has no role** — nothing about a deadline is conserved.
- **ts: observational only.** dxp may emit lifecycle events
  (`dxp.committed`, `dxp.expired`, per-def series) into ts for
  operational dashboards — derived, non-authoritative, never read by
  the sweep or any guard. The telemetry mirror of the transaction,
  never its clock. Demand-driven, not v1.

TTL enforcement is therefore **two-level, each level guard-local**:
participants self-expire their reservations in their own tables (cal's
proposal TTL against cal's rows; bal's hold TTL against bal's), while
the coordinator's deadline governs the instance in the instance's rows
— @D07's belt-and-braces restated as @D04a applied twice. Nobody's clock
lives outside the transaction it times.

Future note: terminal instance records never mutate again, so the
completed-transaction archive is naturally seal-shaped; the extracted
Sealer could later give it provable immutability for audit. Recorded,
not scheduled.

## 8. Scope boundary: nolu owns distance

dxp v1 is **single-tenant**. Cross-tenant and cross-node transactions
are nolu territory, with xolu assisting: the participant contract (@D05)
is precisely the assistance — a future nolu coordinator drives the same
four verbs across nodes, inheriting enforced reservations instead of
advisory ones. xolu never learns distributed consensus; nolu never
learns what a hold means. Each layer's ignorance of the other's problem
is the design.

## 8a. dxp and fsm: the four relations

The question "should each transaction have a corresponding fsm
machine?" decomposes into four relations with different verdicts:

1. **fsm above dxp — yes; the intended pattern for multi-step
   transactions.** Business processes that span multiple atomic steps
   (booking → payment capture → fulfilment, across days) are fsm
   machines whose transition *effects instantiate dxp defs*. Machine
   guards shine here, guarding what they were built to guard — the
   machine's own walk: "fire `capture_payment` only from `confirmed`;
   no `fulfil` while `disputed`." The machine orchestrates atomic
   steps it never needs to see inside.
2. **fsm beside dxp — yes.** fsm is a guard-bearing participant (@D05):
   a def may include a transition among its atomically-committed
   effects.
3. **fsm as dxp's specification — yes, documentary.** The 2PS and 3PS
   phase structures are identical across all defs and instances, so
   they are published as canonical, versioned fsm definitions that
   `pattern:` fields resolve to — inspectable specifications of legal
   phase order, used in def-registration validation. Runtime
   enforcement does not run through them.
4. **fsm under dxp — refused.** The coordinator's phase state does not
   ride on fsm, for three reasons. *Guard locality:* the phase gate
   ("execute iff all participants reserved") is a predicate over the
   coordinator's participant rows, and a guard must live where its
   transaction lives — the gate's read and the phase write commit
   together (the guard-locality law, @C04a, which also
   pins bal's balances to its journal); hosting the phase in fsm splits
   that read from that write across primitive boundaries. Expressing it as an fsm guard instead
   would require guards that read other primitives' state — an
   imperative escape hatch that corrupts fsm's declarativeness for
   every other consumer. *Circularity:* fsm is a participant; a
   coordinator riding on a participant makes failure analysis
   recursive. *The fast path:* single-tenant instances commit inside
   one SQL transaction with no intermediate phase persists (@D06);
   machine-backed state would tax precisely the path dxp is proudest
   of. The instance remains ~six coordinator-internal guarded
   transitions in the T-34 CAS pattern.

## 9. Agents

A dxp def is **tool-shaped**. An agent cannot be trusted to improvise
multi-primitive sequencing — imperative composition is where creative
wrongness lives. Invoking a named definition with typed parameters is
the one thing agents do safely and natively. The application author
declares `place_order(customer, sku, qty, slot)` once; molu exposes it
as a single tool; the substrate guarantees every guard consented
atomically or nothing happened. Agents acquire transactional
superpowers with zero ability to compose unsafe sequences — the safety
argument that justified cal and bal individually, promoted to whole
business operations.

## 10. Errors

Reserve `XOLU-DXP`:

| Code | Meaning |
|---|---|
| XOLU-DXP001 | Instantiation refused: bindings fail slot types |
| XOLU-DXP002 | Reserve failed: a participant guard refused (named) |
| XOLU-DXP003 | Validate failed: reservation no longer holds |
| XOLU-DXP004 | Phase-order violation |
| XOLU-DXP005 | Instance expired; reservations released |
| XOLU-DXP006 | Definition invalid at registration (detail) |

XOLU-DXP002/003 carry the refusing participant's own error (e.g. a
XOLU-BAL001) — composition must not launder the underlying refusal.

## 11. Testing obligations

- **Outcome-uniqueness race harness** (T-34 pattern): concurrent
  commit/release/expire drivers against one instance; exactly one
  terminal state ever. Stress-tagged, **registered in the
  dormant-guards table in the session it is written**, exercised on
  multi-core CI.
- **Crash-recovery fault injection**: kill the coordinator in every
  phase; sweep + TTLs must converge every instance to a terminal state
  with no orphaned reservation.
- **Degradation equivalence** (@D06).
- Property test: for any def and binding set, participants' committed
  effects exist iff the instance record says `committed`.

## 12. Non-goals

- **Control flow. None. Ever.** A def is one atomic composed
  commitment with parameter slots — no branches, no loops, no
  conditional participants. The moment a def grows an `if`, this
  becomes a workflow engine (Temporal, Step Functions) rebuilt badly,
  and the guards stop meaning anything. Branching lives in the
  application or the agent, *between* defs.
- **Stored procedures.** A def is a declarative roster, inspectable
  and analysable — not imperative code inside the database.
- **External (non-xolu) participants** in v1: they reintroduce the
  advisory-prepare weakness this design exists to escape. The launch
  claim stays pure. Webhook-shaped participants are a possible later
  extension, clearly marked as weaker.
- **Cross-tenant coordination** (@D08 — nolu's).

## 13. Open questions

- **Def-as-participant** (composition of commitments): theoretically
  pretty, practically a recursion hazard. Non-goal for v1; door noted.
- ~~Staged entity writes: meta-TTL versus a staging table~~ —
  resolved by @D05b: tentative rows live in the entity tables themselves
  (meta-based staging would split the guard's read from its write;
  @C04c now bars engine-consumed meta outright).
- **fsm participation semantics**: what a "reserved transition" means
  for guarded walks.

## 14. Staging

1. Participant contract formalised; cal adapter over existing
   lifecycle (~1 d — mostly naming what ships).
2. Entity staged writes (~2 d).
3. `dxp/def`: registration, validation, versioning, static analysis
   (~2 d).
4. `dxp/txn` coordinator: 2PS, degradation path, sweep recovery,
   race harness (~3 d).
5. 3PS phase machine (~2 d).
6. bal holds land with the bal build (its @D10) and slot in as the
   second reserve-capable participant.
7. Client + molu def-as-tool surface (~1 d).

Roughly two weeks wholesale; stages 1–4 deliver the headline
(cross-substrate ACID with a declarative surface) even before 3PS and
bal arrive.
