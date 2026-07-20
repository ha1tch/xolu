# Referential Integrity — Prospective Design (proposal)

Updated: 2026-07-19
Status: proposal — not scheduled. T-41 (register) records the defect that
motivated this document; its minimum fix is stage one below. No other
register items exist until execution is decided.

## 1. Problem

xolu today enforces referential integrity nowhere. References between
entities (extracted on every write and materialised as graph edges) are
advisory: a ref may be created pointing at a nonexistent target, and
deleting an entity leaves every inbound ref dangling. The one gesture at
enforcement — the global `CascadingDelete` flag — is a stub: its
referent-discovery step was never implemented, so enabling it changes
the response's claims (`cascaded_deletes`) but not the behaviour (T-41).

A global on/off switch is also the wrong shape. Deletion semantics are a
property of a *relationship*, not of a deployment: an order's line items
should die with the order (cascade), a customer with outstanding
invoices should be undeletable (restrict), and an optional
"recommended_by" ref should simply clear (nullify) — all three in the
same application. The schema, where refs are declared, is where their
semantics belong.

## 2. Design

### 2.1 Schema annotation

A ref field's schema entry gains an `x-ref` annotation:

```json
"author_id": {
  "type": "integer",
  "x-ref": {
    "entity": "users",
    "on_delete": "restrict",
    "validate": false
  }
}
```

- `entity` — the referenced entity type. Required.
- `on_delete` — `restrict` (default when `x-ref` is present) | `cascade`
  | `nullify`. Restrict is the default because it is the only policy
  that destroys nothing: refusal is recoverable, deletion is not.
- `validate` — write-time target-existence check for this ref. Default
  `false`; a separate knob from `on_delete` because its cost profile is
  different (@R04).

**Compatibility:** a ref field without `x-ref` behaves exactly as today
— extracted, edged into the graph, unenforced. Existing deployments and
schemas change nothing until they opt in, relationship by relationship.

### 2.2 Delete-time enforcement

On `DELETE` of entity E:

1. Query the graph for inbound edges to E's node (tenant-scoped).
2. Partition referrers by the `on_delete` policy of the ref field that
   produced each edge:
   - any **restrict** referrer → the delete fails with
     `XOLU-RI001` (409), naming up to N referrers so the operator can
     act;
   - **cascade** referrers → enqueued for deletion, recursively, within
     the existing `MaxCascadeDeletions` budget; exceeding the budget
     fails the whole operation with `XOLU-RI002` before anything is
     deleted;
   - **nullify** referrers → the ref field is set to null (entity
     updated, graph edge removed).
3. All of it — checks, cascaded deletes, nullifications, the root
   delete, and the corresponding graph mutations — executes as **one
   transaction**. Partial cascades must be impossible.

The response's `cascaded_deletes` field becomes truthful: the actual
list of deleted node keys, plus a `nullified_refs` sibling.

### 2.3 Write-time validation (per-ref opt-in)

On create/update/commit of an entity, every ref field with
`"validate": true` is checked for target existence. Checks are batched:
one `SELECT id WHERE id IN (…)` per referenced entity type per write.
A missing target fails the write with `XOLU-RI003` (422), naming the
field and the absent target.

This is deliberately a separate knob from `on_delete`: delete-time
policies cost only when deleting; write-time validation costs on every
guarded write (@R04), so hot ingest paths must be able to decline it
per-relationship.

### 2.4 The global flag retires

`config.CascadingDelete` and its stub are removed once per-ref policies
ship. Between T-41's minimum fix and this design landing, the flag
either gains real (graph-backed, cascade-only) behaviour or is removed
outright — a truthless flag must not survive either way.

## 3. Enforcement engine: the edge table, with the graph as accelerator

Referent discovery is the expensive half of any RI system — and xolu
already maintains the index **in SQL**: every write materialises ref
edges into `t<X>_graph`, whose DDL already carries an index on
`(target_entity, target_id)`. Inbound-reference discovery is therefore
one indexed SQL lookup (~tens of microseconds) on every deployment that
exists today, with no configuration dependency. The in-memory graph,
where enabled, is a cache of the same rows accelerating *application
traversal*; it is never required — and it is never used by the
enforcement read itself. Guards read the edge table directly: under
reserved-commit semantics (D5b–5c) the
edge table is tentative-aware while the accelerator is commit-fed, so
only the table answers the guard's question correctly. **Guards never
use accelerators.**

Consequences, stated plainly:

- **RI has no `GraphEnabled` dependency.** The durable edge table is
  the enforcement index; the memory graph is an optimisation. There is
  no fail-closed mode and no "RI requires the graph" error.
- **The index is verifiable.** The edge table participates in the graph
  rebuild oracle; `iolu db check` (operations roadmap, item 5) answers
  "can I trust the RI index?" the same way it answers everything else.
- **Discovery and mutation share one transaction** (@R02.2), which on the
  same-database edge table is natural: the enforcement read, the
  cascaded/nullified writes, and the edge-row maintenance are ordinary
  rows in one SQLite transaction. Any future async edge maintenance
  invalidates this design and must revisit it.

## 4. Performance

- **Steady state: zero.** Policies engage on delete and (opt-in) on
  guarded writes. Reads, queries, search, traversal pay nothing. This
  is the decisive difference from classical FK constraints, which tax
  every insert unconditionally.
- **Restrict:** one inbound-degree query against the in-memory graph —
  microseconds. The cheapest enforcement RI can have.
- **Cascade / nullify:** proportional to the affected subtree / fan-in
  — the semantics, not overhead; the deletion budget bounds it. Under
  the @R05a set-based strategy the constant factor collapses: closure,
  budget check, and bulk mutation are a fixed handful of statements
  regardless of subtree size, with `RETURNING` feeding derived-state
  maintenance.
- **Write validation:** one indexed PK lookup per validated ref,
  batched per referenced type. Microseconds each; single-digit percent
  on ref-heavy bulk ingest; zero where not enabled. Ref extraction is
  already paid for by graph sync.

## 5. Concurrency

Restrict is check-then-act: a delete verifying "no inbound refs" races
a concurrent write creating one. On SQLite, the single-writer property
serialises the two **provided the enforcement read runs inside the
delete's transaction** — which @R02.2 requires. This is safety by
construction on the current backend and an explicit obligation on any
future one: a server-based SQL backend must either lock the referenced
row range or re-verify inside its transaction. (One derivation of the
guard-locality law, @C04a: the enforcement read
and the mutation it authorises commit together.) Recorded here so the
backend-coupling analysis (iolu operations roadmap) and any future
backend proposal inherit the requirement rather than rediscover it.

## 5a. Backend pushdown

The schema annotation is the backend-neutral contract; enforcement is a
per-backend capability with a defined, asymmetric split:

**Restrict may push down — as reinforcement, never replacement.** The
edge table is already relational (`t<X>_graph(target_entity, target_id,
…)` against the composite node PK), so a composite FK expressing "no
edge may reference a missing node" is pure SQL even on the current
SQLite blob model. Where a backend can express native restrict — the
coarse edge-table FK on SQLite (usable when a deployment's policies are
uniformly restrict), or true per-column FKs on a future backend with
materialised ref columns — the engine uses it as defence-in-depth: it
upgrades the @R05 race defence to database-native enforcement and catches
out-of-band writes the engine never mediated. The engine-level check
always still runs, because per-field policy granularity and
referrer-naming errors (XOLU-RI001) are inexpressible as a bare
constraint violation.

**Constraint-driven mutation must never push down; engine-issued
set-based SQL is the preferred execution strategy.** The distinction is
visibility. A backend-side `ON DELETE CASCADE` constraint deletes rows
the engine never observes — orphaning the memory graph, the FTS index,
`entity.deleted` events (which silently never fire), and cache state,
while honouring neither the deletion budget nor truthful reporting.
That remains banned; any future backend offering native cascade is
answered with this paragraph.

But the engine issuing set-based SQL *itself* is not delegation of
authority — it is the engine using its backend properly, and it is
where the real performance lives:

- **Closure in one query.** The cascade subtree is computed with a
  single `WITH RECURSIVE` over the indexed edge table, replacing
  app-side breadth-first traversal (a query per node or level).
- **Budget before mutation.** The closure is counted before anything is
  deleted; exceeding `MaxCascadeDeletions` aborts with XOLU-RI002 and
  zero rows touched — semantics no constraint can express.
- **Mutation with `RETURNING`.** One bulk `DELETE … WHERE (entity_type,
  id) IN closure RETURNING entity_type, id` (and for nullify, one
  `UPDATE … json_set(…) … RETURNING`) hands the engine the exact
  affected set in the same statement, from which it maintains the
  memory graph, FTS rows, events, and cache — full visibility, O(1)
  round trips. A 500-node cascade is three statements, not a thousand.
  modernc's SQLite supports `RETURNING` (3.35+); Postgres has always.

On a future Postgres backend the same strategy gains further: network
round trips make set-based execution matter more, not less; generated
columns over JSONB make **per-field** native restrict FKs expressible
(upgrading the coarse edge-table FK); MVCC row locks on FK checks solve
the @R05 race natively rather than by single-writer serendipity; and
`DEFERRABLE INITIALLY DEFERRED` constraints allow bulk `Commit` imports
with forward references validated at transaction commit — an ergonomic
and performance win the engine-level check would otherwise forbid.

**The kernel fence (Postgres posture; verified 2026-07-19).** On PG,
every `x-ref` field materialises as a generated column carrying a
kernel FK declared `ON DELETE RESTRICT` — **for all policies, not just
restrict** — and `DEFERRABLE INITIALLY DEFERRED`. Consequences: no
writer of any kind (out-of-band sessions included) can create a
dangling ref (insert/update validated by the kernel) or delete a
referenced row unmediated (kernel-refused), so every RI-governed
mutation is *forced through the engine* — while the engine's own
cascade/nullify transactions delete in any order under deferred
checking, keeping mutation semantics, budgets, and truthful reporting
engine-owned per this section's ban. The bypass closes not by pushing
mutation down but by kernel-refusing unmediated mutation. Expressible
even on a shared nodes table (constant generated column + composite FK
`(target_entity, ref_id) REFERENCES nodes(entity_type, id)`); the PG
backend proposal, when written, inherits ref-column materialisation as
a requirement.

A future multi-backend Store interface grows a capability declaration
(native restrict: none | coarse | per-field; deferrable: yes/no) so the
engine requests the strongest reinforcement each backend can express.

## 6. Error taxonomy

Reserve the `XOLU-RI` prefix:

| Code        | Meaning                                              |
|-------------|------------------------------------------------------|
| XOLU-RI001  | Delete refused: restrict-policy referrers exist      |
| XOLU-RI002  | Cascade exceeds deletion budget; nothing deleted     |
| XOLU-RI003  | Write refused: validated ref targets missing entity  |

(An earlier draft reserved XOLU-RI004 for "policies present but graph
disabled"; @R03's edge-table engine removed the dependency and the code
with it. RI004 stays unassigned.)

## 7. Testing obligations

House style applies:

- Property test: after any sequence of policy-governed writes and
  deletes, no edge in the graph points at a nonexistent node whose ref
  field carried a policy (the "no dangling under policy" invariant).
- Atomicity test: fault-injected mid-cascade failure leaves the store
  byte-identical to pre-delete (transaction property).
- The restrict race: a concurrent-writers test in the
  T-34 harness pattern — N goroutines creating refs against one
  deleting — asserting the delete either beat every ref (all writes
  fail XOLU-RI003 / target gone) or lost to at least one (delete fails
  XOLU-RI001). **Stress-tagged, registered in the dormant-guards table
  in the same session it is written, exercised on multi-core CI.**
- Budget test: cascade exactly at and one past `MaxCascadeDeletions`.

## 7a. Open design questions

Two classics every RI implementation eventually meets, recorded now so
stage 2's design answers them rather than discovers them:

- **Cycles.** A→B→A under cascade must terminate: the closure CTE uses
  UNION (not UNION ALL) or explicit cycle marking, and the property
  test in @R07 gains a cyclic fixture.
- **Mixed-policy paths.** A node reachable through a cascade path while
  also restrict-referenced from outside the subtree: restrict must win
  globally — the entire delete refuses (XOLU-RI001) rather than
  cascading part of the tree. Stated as the rule now; the enforcement
  read therefore computes the closure first and checks ALL inbound
  edges into it, not just edges into the root.

## 8. Staging

1. **T-41 minimum fix** (independent, ~half a day): graph-backed
   discovery behind the existing flag, or flag removal. Ships whenever
   convenient; unblocks nothing and lies to no one.
2. **Schema annotation + restrict** (~2 days): parsing, the enforcement
   read, XOLU-RI001/004, tests. Restrict alone already delivers the
   safety half of RI.
3. **Cascade + nullify** (~2 days): transactional multi-delete,
   budget semantics, response truthfulness, fault-injection tests.
4. **Write-time validation** (~1 day): batched existence checks,
   XOLU-RI003, ingest benchmarks before/after.
5. **Client + molu surface** (~1 day): typed errors in `pkg/client`;
   molu's semantic map exposes per-ref policies so agents can predict
   refusals instead of colliding with them.

Roughly a week wholesale; stages 2–5 wait for a consumer that wants
them. Stage 1 does not wait — it is repair, not feature.

## 9. Non-goals

- **Cross-tenant references** — refs never cross tenant boundaries
  today; RI does not change that.
- **A general constraint engine** — declarative cross-entity invariants
  ("sum of X ≤ Y") are a different, larger animal; this document is
  refs only.
- **Full delegation of enforcement to any SQL backend** — see @R05a: the
  engine remains the policy authority. Native restrict is welcome
  reinforcement where expressible; mutating policies (cascade, nullify)
  are engine-only by commitment, because the engine owns the derived
  state their deletions must maintain.


## 10. Comparative positioning

An honest assessment of where this design lands relative to the systems
it will be measured against.

### Against a typical RDBMS

**Semantic parity.** Restrict / cascade / set-null is the SQL standard's
own triad, declared per relationship. Nothing in the policy vocabulary
is lost.

**Operational advantages over standard FK practice:**

- **Bounded cascade.** No mainstream RDBMS bounds `ON DELETE CASCADE`;
  a mis-aimed constraint deletes without limit and reports a row count.
  The deletion budget aborts before touching anything (XOLU-RI002).
- **Truthful reporting.** RDBMS cascades do not report what they
  deleted; the response's affected set does.
- **Error quality.** A constraint violation names a constraint;
  XOLU-RI001 names the blocking referrers.
- **Event integration.** Cascaded deletions flow through the entity
  event stream as first-class deletions; the RDBMS equivalent is
  trigger machinery maintained by the application.

**The genuine loss: enforcement locus.** RDBMS foreign keys bind at the
storage kernel — every writer, every tool, every ad-hoc session is
subject unconditionally. Engine-level enforcement binds writes through
the engine; a code path that touches the database directly bypasses
policy (such paths exist today — e.g. iolu's raw-SQL reads — and are
themselves tracked for remediation). The coarse edge-table FK (@R05a)
narrows the gap; categorically it remains invariant-by-architecture
versus invariant-by-physics. Sizing the loss honestly: in the intended
deployment xolu is the sole writer, so the bypass surface is first-party
code — auditable and shrinking — and on a Postgres backend the gap
**closes by construction** via the kernel fence (@R05a): write-time
validation and delete refusal become kernel-enforced for every writer,
with cascade/nullify semantics fenced behind, not delegated down. The
residuals are a superuser's ability to drop constraints (parity with
any RDBMS — nil relative to the comparator) and the backend's
obligation to materialise ref columns. The gap is therefore
SQLite-specific and scheduled to die with the backend that has it. What is permanently absent is the RDBMS's decades of edge-case
hardening, which is why @R07a records the classic traps (cycles,
mixed-policy paths) ahead of implementation.

### Against MongoDB

A category difference rather than a comparison. MongoDB enforces
referential integrity nowhere: no foreign keys, `$lookup` joins without
enforcement, DBRefs as naming convention, cascade as application code,
and denormalisation ("embed, don't reference") as the doctrinal answer.
Every guarantee in this proposal — enforced per-ref policy, atomic
budgeted cascade, write-time validation, truthful reporting — is
something that model delegates to application authors. One fair caveat:
that abdication partly prices horizontal sharding, where cross-shard
integrity is genuinely hard; xolu's per-tenant single-node model does
not face that class, so this comparison holds at xolu's deployment
scale.

### The resulting position

Implemented, xolu occupies an unusual quadrant: **document-model
flexibility with relational-grade referential integrity and
graph-native discovery**. Its only close neighbour is Postgres with
JSONB and generated-column FKs — which reframes the prospective
Postgres backend not as a compromise but as a meeting of kin: xolu
keeping its model and borrowing the one thing engine-level enforcement
cannot manufacture, a kernel.

Summary answer to "what do we lose": one honest gap — kernel-level
unbypassability — well understood, architecturally mitigated,
backend-recoverable; purchased alongside operational properties the
incumbents do not offer.
