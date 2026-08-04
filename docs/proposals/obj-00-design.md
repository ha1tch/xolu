# obj — Distinguishable, Manipulable Units (proposal)

Updated: 2026-07-31
Status: proposal — not scheduled. Sibling to `/loc` (`loc-00-design.md`),
which it depends on directly (position resolution's third termination
case is a `/loc` leaf) and which depends on it in turn (`/loc`'s own
fence-composition rule, this document's central move applied a second
time). Neither document is complete without the other; read as a pair.
Emerged from extended adversarial stress-testing of an earlier,
single-primitive `/loc` design against real CMMS/EAM/logistics
scenarios — this document is the resolution, not the starting
proposal, and several of its central decisions (subject-is-entity,
promote/demote, the manipulability test) exist specifically because a
first attempt got them wrong and was corrected against concrete
counter-examples. Where useful, a decision's own rejected alternative
is named, not just the answer.

## 1. What obj is, in one paragraph

`/obj` tracks things distinguishable enough to be individually
addressed and mobile enough to be repositioned by ordinary operation —
a lorry, a shipping container, a pallet, a specific serialized asset —
and the containment relationships between them. It does not invent
identity for these things: every `/obj`-tracked thing is an entity
`/obj` attaches position and containment capability to, the same way
`/loc` attaches tree position to entities and `/meta` attaches
annotations to entities and primitive subjects. It is not a general
inventory system, not a replacement for `bal`'s conservation accounting,
and not a place to model quantities — it answers exactly two questions,
guard-bearing and exact: *where is this, right now,* and *what does it
currently hold.*

## 2. Non-goals

- **Not a subject model of its own.** Same law as `/loc`'s §3c,
  restated here because it's the single decision everything else in
  this document depends on: `/obj` never mints an independent identity.
  See §4.
- **Not a quantity ledger.** Counting fungible stock is `bal`'s job.
  `/obj` tracks the container the count rides on, never the count
  itself. See §8.
- **Not a rule engine.** "Surgical instruments may never leave the
  surgery ward," "controlled substances stay in pharmacology" —
  real, common requirements this design deliberately does not
  enforce. `/obj`'s own guards are physical-impossibility guards only
  (capacity, exclusivity); policy of this shape is the calling
  application's responsibility, notified via the same event feed
  `/loc` already commits to, matching `/loc`'s own §7c boundary
  exactly and for the same reason — a general predicate engine over
  arbitrary object/location facts is the thing chronicle already
  refused to build for `bal`, and refusing it here again rather than
  reintroducing it one layer up is deliberate, not an oversight.
- **Not a spatial index.** `/obj` has no geometry of its own.
  Anything spatial about an `/obj`-tracked thing comes from wherever
  its position resolves to — a `/loc` leaf's fence membership, or
  nothing at all if it resolves through raw report only.
- **Not versioned placement.** Position resolution, like `/loc`'s own,
  is live-only. A historical "where was this on July 15th" query
  resolves through whatever the chain's anchors are *today*, not what
  they were then. Real limitation, not deferred lightly — see §13.

## 3. The manipulability test

Two properties, not one, decide whether something is `/obj`-shaped —
conflating them was an early mistake in this design worth naming
explicitly, because the two properties pull apart on real examples.

**Distinguishable**: can this specific instance be told apart from
another of the same kind. A serialized asset passes; a bottle of Coke
does not.

**Independently manipulable**: can this thing be picked up and moved as
one coherent unit, on its own, without falling apart or scattering.
Bricks fail this in isolation, and it doesn't matter that they're
distinguishable-in-principle — a thousand loose bricks on a pallet is
not one thing anyone can move. A shrink-wrapped pack of fifty is.

**Something is `/obj`-worthy only if it clears the second bar.**
Distinguishability alone is not sufficient — it answers "could I tell
these apart," not "is this a thing." A pallet, a shipping container, a
case of twenty-four bottles, a lorry, a specific serialized pump: all
clear it. A loose bottle, a loose brick, raw granular material: none
do, regardless of how precisely each unit could in principle be
identified.

This is real-world physics, not a schema-checkable fact, and this
document does not propose enforcing it in code — see §2's rule-engine
non-goal. It's stated here as explicit modeling guidance a schema
designer has to get right, the same way declaring a `bal` account's
unit as something a customer would recognize already is.

## 4. Subject model: obj attaches to entity identity

**The central decision, and the one everything else follows from: an
`/obj`-tracked thing is always an entity. `/obj` never mints its own
independent identity for anything it tracks.**

This was not the first design considered. The initial framing treated
`/obj` as a new primitive with its own independently-addressed rows —
the same shape as `bal` accounts or `cal` bookings — and ran directly
into two compounding problems once containment and cross-references
were worked through: entities would have needed a new addressing
extension to `x-ref` just to point at an `/obj` record (a real, if
solvable, generalization of the referential-integrity system), and
`/obj`'s own rows, registered as new graph node types, would have been
silently opaque to `Sulpher`'s query executor — matched at the topology
level, then hydrated as empty against every field predicate, because
`Sulpher`'s hydration path (`preHydrateEnvs`/`hydrateNodeData`) calls
the generic entity `Store.GetMany` keyed by a node's type prefix, with
no fallback for a type the `Store` has never heard of, and no error
raised when that lookup comes back empty. A query touching an `/obj`
node's fields would run cleanly and silently return nothing — a worse
failure mode than an outright rejection, because nothing would signal
that anything had gone wrong.

Both problems dissolve under the entity-subject model, not because
they were solved but because they were never real: **an `/obj`-tracked
lorry already has a graph node — its own entity node, hydrating
through the path `Sulpher` already gets right — and an entity that
wants to reference it uses an ordinary `x-ref`, unchanged, because
there was never a new non-entity identity to point at.**

This is not a novel pattern invented for `/obj`. It is the third
application of one already established twice:

- **`loc`'s own subjects** (`loc-00-design.md` §3c): *"loc does not
  invent a subject model of its own. A subject is always an entity."*
- **`/meta`'s namespaced subjects** (`pkg/storage/meta_subject.go`,
  @C04c): entity kinds (undotted) and namespaced primitive kinds
  (dotted — `cal.calendar`, `cal.booking`, `fsm.machine`, `ts.timeline`
  are live today; `bal.account`, `dxp.def`, `dxp.txn` are already
  reserved, gated purely on "has the primitive landed," a condition
  now true for both). `/obj` reuses this exact `(kind, key)`
  addressing convention for its own subject.

**What does not carry over from `/meta`, and this distinction matters:**
`/meta`'s own defining law is that it is engine-inert — @C04c states
plainly that nothing reads meta data to make a decision. `/obj` takes
only the addressing convention, not the inertness. Position and
containment are exactly the guard-bearing facts an admission decision
gets made against. `/obj`'s own storage — position, containment edges,
capacity — is `/obj`'s own guard-bearing SQL, mirroring `bal`'s own
account/journal split in shape, not `/meta`'s key-value table.

**Consequence, stated plainly:** every `/obj`-tracked thing must be a
real entity, with whatever schema and validation weight entity records
carry. This is a real cost, not a free move — see §12's discussion of
what this does and doesn't cost. `/obj` also becomes unusable for a
tenant that has disabled entity/graph, the same constraint `loc`'s
subjects already accepted.

## 5. Containment

Containment is an edge between two subjects, each independently
addressed per §4 — a pallet's entity, a case's entity, connected by an
`/obj`-owned relation, not a `/loc`-shaped tree.

**Why containment cannot reuse `/loc`'s tree machinery unchanged.**
`/loc`'s tree assumes structural change is rare, deliberate, and
administrator-controlled (`def`/`patch` only — reparenting is refused
outright, §1 of `loc-01-rest-api.md`). That assumption is true for
buildings and false for pallets: a container's entire purpose is being
repositioned by ordinary, high-frequency operation. Force that through
`/loc`'s tree unchanged and the safety net `/loc` gets for free (only
an administrator ever reshapes it) disappears, and nothing replaces
it — any ordinary move could in principle create a cycle (a pallet
placed inside one of its own contents), and nothing would catch it.

**The guard: cycle safety on every containment write, universally, not
opt-in.** An earlier version of this design proposed a `portable` flag
— only explicitly-flagged nodes could be repositioned, everything else
kept `/loc`'s refuse-reparenting default. Rejected: it silently
reintroduces the exact risk it was meant to close, for anything
someone forgot to flag. Under the entity-subject model the flag
approach doesn't even apply cleanly — any entity can in principle
become a container for any other — so the cycle check has to be
universal: every containment write checks, in the same guard-bearing
transaction, whether it would create a cycle.

**Where the check's algorithm comes from, and where it doesn't.**
`pkg/graph`'s `FlatGraph.wouldCreateCycle` already implements exactly
this shape — bounded BFS from the destination back toward the source,
capped by `cycleCheckLimit`, and critically: *if the budget is
exhausted, it assumes a cycle exists rather than risking a false
negative.* That conservative-on-exhaustion behavior is easy to get
wrong on a first attempt and this codebase has already gotten it
right, with real testing behind it — worth reusing the *shape*
directly. What can't be reused is the method itself: `wouldCreateCycle`
reads `g.nodes` directly, the live in-memory graph's own state, which
(§10) is a derived, hydrated-from-storage mirror — using it to
authorize a guard decision would violate the same guard-locality law
`cal`'s H1/H3 split and `bal`'s rollup plane already exist to respect.
`/obj`'s own cycle check must run against freshly-read, transaction-
scoped containment edges from `/obj`'s own tables. The concrete task:
extract the bounded-BFS-with-conservative-budget algorithm into a form
parameterized over an edge-lookup function, so `FlatGraph` keeps
supplying `g.nodes[cur].out` and `/obj`'s guard supplies a closure over
its own transaction's rows. Same proven shape, two different data
sources, one small and deliberate refactor — not a rewrite.

**What containment is not.** A prosthetic attached to a patient's body,
or a device checked out to a person, is not containment — a case on a
pallet is interchangeable (any pallet will do); a checked-out
prosthetic is singular and assigned, tied to that specific subject for
reasons that have nothing to do with physical stacking. Whether this
needs its own edge variant (named attachment slots — left-arm,
right-wrist) or can reuse containment's edge shape unmodified is a
genuinely open question, not resolved here — see §14.

## 6. Position resolution

An `/obj` subject's fully-resolved position terminates one of three
ways:

1. **A `/loc` leaf**, anchored — parked at depot-3, stowed in hold 3.
2. **A raw geo-report**, no `/loc` anchor at all — driving through
   Camden, tracked purely by coordinate, the same `report`-only shape
   `/loc`'s own mobile subjects already use.
3. **Another entity's resolved position** — a case is wherever its
   pallet is, a pallet is wherever its lorry is. Walk the chain
   transitively until it terminates at (1) or (2).

Resolving a subject nested several containment levels deep — a case,
on a pallet, on a lorry, in a ship's hold, at berth — means walking
through cases 3, 3, 1 in sequence, the ship's own hold-leaf placement
itself potentially resolving through the ship entity's own `obj`
position (§11). Fence membership for a deeply-nested subject requires
walking the *entire* chain down to wherever it actually touches the
real world before a geometric test can run at all — a case has no
coordinate of its own to test against anything.

## 7. Capacity

`/loc`'s own capacity guard is a bare count — `count + 1 ≤ ceiling`.
Real cargo capacity is routinely two simultaneous, independent
constraints — logistics calls this cubing out versus weighing out, and
a lorry is exactly where both are live at once. The CAS mechanism
survives the generalization without changing shape, only its
predicate: `SUM(weight) + Δweight ≤ max_weight AND SUM(volume) +
Δvolume ≤ max_volume`, still one guarded `UPDATE ... WHERE`, still
atomic, still checked in the same transaction as the containment write
it guards.

## 7a. Patterns: a mechanism already twice-proven, applied a third time

**Motivation.** A fleet of europallets, or a yard of standard shipping
containers, all share the same capacity profile. Retyping `{
"max_weight_kg": 1000, "max_volume_m3": 1.5 }` at every `attach` for
every one of them is real friction the moment a fleet exists, and it's
exactly the kind of repeated fact a definition should carry once. The
question is what mechanism carries it — and xolu already has the
answer, verified directly against source rather than assumed: `fsm`
and `dxp` both already do this, independently, the same way.

**The precedent, checked precisely rather than taken on faith.**
`pkg/server/v2_fsm_machine_handlers.go`: creating a machine loads the
source definition's `spec_json` and writes a `snapshot_json` onto the
machine row itself; every subsequent transition
(`v2_fsm_walk.go`) reads the machine's own `snap.Spec`, never a live
re-fetch of `fsm_definitions`. `pkg/server/v2_dxp_def_handlers.go`,
line 450, states its own version outright: `dxpTxnSnapshot is the
fully-resolved def cloned into dxp_txn.snapshot_json`. Both do three
things together, not one: **snapshot** the fully-resolved spec at
creation (decoupling the clone's behavior from the definition's
future mutability), **retain an explicit lineage reference** anyway
(`fsm_def_id`, `dxp_def_id` — provenance survives even though the
operational copy is independent), and **track deletion of the source
explicitly** — `definitionExists` is a live, computed-at-read check
(`SELECT COUNT(*) FROM fsm_definitions WHERE ...`), surfaced as a
`definition_deleted` field on every read, not a stored flag requiring
separate invalidation the moment a definition is deleted. Computed,
not stored, is the better choice for the same reason a `bal` rollup is
never itself guard-bearing: a stored flag can go stale the instant
something forgets to update it; a computed one cannot.

**Naming the cloning mechanism precisely, since "definition and
instance" invites the wrong family.** That phrase reads as class-based
— an object holding a live reference to a mutable class, dispatch
resolved through that reference, a class edit visible to every
existing object immediately. That is exactly the behavior neither
`fsm` nor `dxp` has. What both actually implement is closer to
**prototype-based cloning** in the Self/Io sense: each `fsm` machine or
`dxp` txn is a **cloned child** of its definition — an independent copy
taken once, at creation, carrying no live behavioral link back to its
parent afterward. Under a class-based reading, "a definition change
shouldn't retroactively touch already-cloned children" needs arguing
for, against the metaphor's own grain; under prototype cloning, it
doesn't — independence from the parent *is* what cloning means, not a
special case carved out of it. Two things go beyond even classical
prototype semantics, though, and are xolu's own addition rather than
inherited from either paradigm: each clone retains a lineage pointer
back to its parent, and live tracking of whether that parent still
exists. A Self or Io clone does neither on its own — closer to a
version-controlled snapshot than to either classical model by itself.

**Also worth naming precisely: "template" is the wrong noun for what
gets cloned from, and not for the reason it first looks like.** A
template is authored in advance, before any instance exists — a
stencil. That's the right word for one of the two ways a definition
gets created here (typing literal values into a fresh record), but
it's a forced metaphor for the other: reading an existing, concrete
`/obj` subject's own fields and turning them into a reusable
definition has no "authored in advance" about it at all. **Pattern**
is the noun used throughout this section instead, precisely because it
doesn't presuppose which direction creation runs — a pattern can be
drafted from scratch or recognised from something that already exists,
and both are legitimate, covered below as `def` and `extract`
respectively.

**What this document deliberately does not copy from IAS, having now
looked at it closely.** Maximo's Item Assembly Structure applies a
pattern by *creating child asset records* — the structure's contents
get stamped into existence on apply. A container pattern must not do
this. `/obj`'s containment (§5) exists specifically because contents
change by ordinary, high-frequency operation, not administrative
action — a pattern that auto-populates contents on `attach` would
silently reintroduce the administrative-rarity assumption §5 already
rejected for containment generally, through the back door of "but this
part is just patterning." **A pattern governs the capacity profile
only, never contents.** What a pallet is *carrying* is always
established through ordinary `move` calls after `attach`, exactly as
today; what a pallet's capacity *ceiling* is can now come from a
pattern instead of being retyped.

**The mechanism.** A container pattern is **not** an `/obj` subject and
must not be forced through §4's entity-subject law — it has no
position, clears none of §3's manipulability test (it isn't a physical
thing at all), and belongs in the same category as
`fsm_definitions`/`dxp_defs`: a pure definitional record, addressed by
`(tenant_id, id)` or `(tenant_id, name)` in its own table
(`obj_patterns`), not by §4's `(kind, key)` subject convention.
Conflating the two would be the same category error as treating a
`fsm` definition as if it were a machine.

Two ways to create a pattern, both producing the identical kind of
record — only how the fields get populated differs:

`POST /api/v2/obj/patterns/def` — drafted from scratch:

```json
{ "name": "europallet-std", "max_weight_kg": 1000, "max_volume_m3": 1.5 }
```

`POST /api/v2/obj/patterns/extract` — read from an existing subject's
current fields instead of typed in:

```json
{ "source": "pallets:88", "name": "europallet-loaded-std" }
```

`extract` reads `source`'s current capacity fields once, at the moment
of the call, and writes a new pattern from them; `source` itself is
untouched, and no lineage runs from the new pattern back to `source` —
`source` was a subject the read happened to be taken from, not a
parent the way a pattern is a parent to what gets cloned from it.

**Applying a pattern — the durable form.** `attach`
(`obj-01-rest-api.md` §1) gains an optional `pattern` field, mutually
exclusive with inline `capacity` — a pattern *is* the capacity
declaration, the same way an `fsm` machine doesn't partially override
its def's state graph at creation:

`POST /api/v2/obj/attach`:

```json
{ "subject": "pallets:9001", "pattern": "europallet-std" }
```

The attached subject's capacity fields are snapshotted from the
pattern at `attach` time, exactly like a machine's `snapshot_json`;
`GET` on the subject surfaces `pattern`, `pattern_id`, and a computed
`pattern_deleted`, exactly like a machine's `definition`/
`definition_deleted` pair. A pattern changing later — a regulation
lowers the standard pallet weight ceiling — never retroactively
touches already-attached subjects, for the identical reason a `fsm`
definition change never retroactively touches a running machine's
snapshot. Propagating a changed ceiling to existing subjects, if ever
wanted, is a separate, explicit, caller-driven bulk operation, never an
automatic cascade — the same discipline `cal`'s R-T1 already
established for a differently-shaped version of this exact problem (a
definitional parameter changing after dependent state already exists
on the old value).

**`pattern_after` — the one-shot form, composing on top of the same
two primitive operations rather than a third mechanism.** Both paths
above reduce to *read capacity fields off a live subject* and *write
capacity fields onto a live subject*. `extract` is read, persisted.
`attach`'s `pattern` field is write, sourced from something persisted.
`pattern_after` is read immediately followed by write, with nothing
persisted in between — for the common case of "make this new thing
match that one good example," with no intent to reuse the shape again:

```json
{ "subject": "pallets:9002", "pattern_after": "pallets:88" }
```

Exactly one of `capacity`, `pattern`, or `pattern_after` may be set on
`attach` — all three set the same underlying fields, and mixing them is
a new refusal (`XOLU-OBJ013`, mirroring the existing `XOLU-OBJ010`
mutual-exclusivity shape from `promote`), not a merge-and-hope. The
honest cost of `pattern_after`, stated plainly rather than left
implicit: the resulting subject carries no `pattern`/`pattern_id` —
nothing was persisted, so there's nothing to point back to, and a
`pattern_after`-created subject's capacity fields are indistinguishable
from having been typed in by hand. That's the correct, direct
consequence of not persisting anything, not a gap to close later.

**Detecting drift needs no new bookkeeping.** A regulation lowers the
standard pallet weight ceiling; which already-attached subjects are
running under the old value? The answer is a plain recompute-and-
compare, the identical shape `pkg/bal/rollup.go`'s `RebuildRollup`
already uses to answer "what should this bucket say" from the journal
rather than trusting a cached value: compare a cloned child's current
capacity fields against its pattern's current fields, on demand. No
per-field version tags, no incremental patch-tracking — the lineage
pointer already names which pattern to compare against, and the
comparison itself is cheap and stateless. This view is advisory only,
the same discipline §5c of `loc-00-design.md` states explicitly for
its own version of this problem: it never writes back into an
already-attached subject's own capacity fields. Fixing a drifted
subject, if wanted, is an ordinary `capacity` `PATCH` (§4 of
`obj-01-rest-api.md`) on that subject specifically, not something this
comparison does on a caller's behalf. A `pattern_after`-created subject
has nothing to compare against and is correctly excluded from this
check entirely, for the same reason it has no `pattern_id`.

**Recursive patterns** (a lorry pattern referencing a pallet pattern
referencing a case pattern, mirroring IAS's own
sub-assembly-of-a-sub-assembly structure) **are covered**, on top of
the flat, single-level mechanism above — the demand-gated first pass
this section originally described as sufficient on its own has been
superseded by the fuller version.

## 8. The bal boundary: object versus quantity

**A `/bal` quantity is never attached directly to a containment edge —
only to whatever `/obj` is the smallest cohesive package at that
interface (§3).** A pallet cannot contain "1,200 loose bottles" as a
fact, physically or in this model. It contains cases, and the cases
carry the countable quantity — "this pallet's account counts case-units
of Coke 330ml×24" is a legitimate `bal` declaration, because what's
being counted already clears the manipulability bar; "counts loose
bottles" is not, even though it's the identical information one level
too fine.

**Commercial units are never guard inputs.** "Buy 1,000 bricks" is an
approximate conversion (packs × ~50 ≈ bricks — nobody counts exactly at
bind time), and an approximate number can never back a guard decision,
for the same reason a rollup bucket can't: the guard's read has to be
exact, in the same transaction as the write it authorizes. The
resolution: the commercial unit is converted to the physical unit
(*"1,000 bricks"* → *"~20 packs"*) entirely upstream, by application
logic, before anything reaches a guard. The transaction that actually
gets refused or admitted is "reserve 20 packs" — exact, ordinary `bal`
semantics. When a pack is finally opened and its actual contents
counted, the correction is an ordinary `bal` transfer into a shrinkage/
adjustment account, the same accounting pattern used for any physical
recount against an estimate — not new machinery.

## 9. Promote / demote

The transition between a `bal`-tracked bulk quantity and an
individually-`/obj`-tracked unit — case #4471-C17 pulled aside as
damaged, or an undamaged return folded back into bulk stock. Refined
from an earlier version of this design, which treated promotion as
"create an `/obj` record" — under §4's entity-subject model, that's
imprecise: promotion is **create-or-reuse the entity, then attach
`/obj` position to it, atomically with the `bal` decrement that removed
it from the bulk count.** Same dxp-composed shape either way (decrement
one leg, entity-create-or-position-attach the other, one commit) — the
atomicity requirement just spans one more fact than originally scoped.

**Correction (T-121, built 2026-08-02): the "never a window" framing
above overstated what dxp actually guarantees for this composition,**
confirmed directly by the built stress/end-to-end tests, not assumed.
`/obj` and `bal` are different SQL engines (`/obj` has its own
dedicated per-tenant file), so this always dispatches via dxp's
*phased* path — each participant commits independently, in its own
transaction. A refusal caught during Reserve/Validate (e.g.
insufficient `bal` balance) is still genuinely atomic: nothing has
executed yet. But a refusal discovered only during Execute (e.g.
`/obj`'s own XOLU-OBJ011, "still contains something") can leave one
leg already committed while another is refused — dxp's own phased
path calls this outcome "expired" (a documented, accepted risk of
that path generally, `v2_dxp_dispatch.go`'s own §6 comment, not
specific to promote/demote), and it is genuinely observable, not
merely theoretical. A caller receiving "expired" must treat it as
needing reconciliation, the same as any other phased dxp transaction.

## 10. Graph integration

`/obj` never depends on the graph for a guard decision (§5, §10 of
`loc-00-design.md`'s own H1/H3 precedent) — `pkg/graph.FlatGraph` is
explicitly documented as an in-memory structure, populated by
hydration from the durable storage layer, the same derived-and-
advisory category as `cal`'s H3 index and `bal`'s rollup plane.

What it is genuinely useful for: after a containment write commits in
`/obj`'s own guard-bearing tables, best-effort mirror the edge into the
live graph via `AddEdge` — the identical shape already built this
session for `bal.Adapter.PostCommit` (commit first, authoritative; then
`EmitDeltas`, best-effort, degrading via `rollupDegraded` rather than
failing the write if the derived-plane update itself has trouble).
Once mirrored, every read-only traversal function `FlatGraph` already
exposes — `FindPath`, `AllShortestPaths`, `PathExists`, `GetNeighbors`,
`SharedOutNeighbors` — answers `/obj`'s own closure questions ("what
does this contain, transitively") directly, with no new traversal code
to write, because both endpoints of every mirrored edge are already
correctly-typed, already-`Sulpher`-hydratable entity nodes (§4).

**Note (T-123, built 2026-08-02): `/obj`'s own `dxp.Participant`
always dispatches via the coordinator's phased path, never collapsed.**
`/obj` has its own dedicated per-tenant SQLite file (§4's own storage
shape, T-119), tagged `"sql-obj"` in `dxpEngineOf` — a genuinely
separate engine from the shared tenant primary store `bal`/`entity`/
`fsm`/`cal` share. The coordinator's `EngineHomogeneous` check
requires every participant to be tagged the literal `"sql"`, so any
transaction touching `/obj`, in any combination, is never eligible for
the single-transaction collapsed path. This was not anticipated when
this document's own multi-primitive composition language was first
drafted, before `/loc` (which carries the identical `"sql-loc"`
consequence) had actually been built — stated here plainly rather
than left as an implicit assumption a later reader might get wrong.

## 11. Composition with /loc: not opposition

An entity can compose both `obj` position and `loc.fence` (or `loc`
tree ownership) at once, and this is expected, not exceptional — the
two capabilities describe different facts about the same subject, not
competing claims to it.

- **A moving reference frame** (a convoy, a ship): the entity composes
  `obj.position` (so it can be tracked and repositioned) *and* `loc`
  tree ownership or `loc.fence` anchored to its *own* resolved
  position — self-reference, not cross-reference. A ship's hold-tree
  placement resolves through the ship's own `obj` position; a
  convoy's exclusion fence tests membership against its own live
  coordinate. One entity, two capabilities, one referencing the other
  on itself.
- **A container composing both `obj` containment and `loc.fence`
  simultaneously is not redundant.** `obj` containment is formal —
  cargo actually declared, loaded, capacity-guarded. `loc.fence` on
  the same container is a raw geometric test — anything physically
  co-located, whether or not it was ever formally admitted. A worker
  briefly inside a container during loading was never `obj`-contained
  cargo, but is inside the fence. Same subject, two capabilities,
  catching two genuinely different facts.

A self-anchored fence on an entity with a continuous position feed —
the moving-reference-frame case above — inherits `loc-00-design.md`
§5b's own `/ts` resolution directly: the entity's position history
lives in `/ts`, and a historical query resolves the same way. An
entity that's discretely repositioned rather than continuously
tracked has no historical record of its own position at all, for the
same reason a discretely-`PATCH`ed `/loc` node doesn't (§5b) — the
same fix applies, reached from `/obj`'s side instead of `/loc`'s.

**Two blockers stack here, not one — added 2026-08-02, checked
directly against the shipped v0.21.2 checkpoint.** This section reads
as if the self-anchored case is ready the moment `/obj` exists. It
isn't, and the second reason is easy to miss without reading
`loc-00-design.md` §5's own reconciliation note directly: self-
anchoring needs `loc.fence` to resolve through an entity's `(kind,
key)` identity, and that model — described throughout this section
and §5 as *the* design — did not ship in `/loc` v1. `pkg/loc/
store.go`'s `DefFence` takes a bare string; `center.self=true` is
correctly rejected today citing the `/obj` dependency, but even once
`/obj` exists, self-anchoring stays blocked on a second, separate
prerequisite this section doesn't currently name: `/meta` wiring for
`loc`'s own fence identity, independent of whether `/obj` itself is
built. Whether that `/meta` work is the same effort `/obj`'s own
construction would do anyway, or a genuinely separate piece of work,
isn't established — flagged here as an open question, not assumed
either way.

## 12. Lifecycle states

Three, not the one ("has a position") the original design implicitly
assumed:

- **Positioned** — resolves per §6.
- **Unknown/off-site** — nil position is a first-class, ordinary
  state, not a workaround. An asset shipped to a third-party repair
  vendor, or genuinely lost, has no clean representation without
  this: the alternative (a synthetic "off-site" or per-vendor `/loc`
  leaf) was tried and rejected as real, unforced data-modeling debt —
  one shared leaf collapses "who has what," one leaf per vendor is
  administrative overhead nobody asked for.
- **Permanently retired** — a genuinely new terminal state, distinct
  from both of the above and from demotion into a bulk count. Found
  via a real, if uncomfortable, example: a decedent's body, tracked
  by `/obj` like any other subject through a hospital until
  incineration, at which point the physical thing itself ceases to
  exist — not "position unknown" (which still assumes the thing
  exists somewhere), not demotion (nothing it dissolves into). Closer
  in shape to a `bal` account closure or a `cal` booking's terminal
  status than to anything else discussed so far.

Worth noting explicitly, since it surfaced from that same example: an
`/obj`'s tracking was never conditional on any particular *entity*
state being active. The entity governing an object can change state
(a patient's care-relationship ending) without the object's own
tracking needing to change shape at all — what changes is which
governing relationship the object sits under, not whether it continues
being tracked.

## 13. Open questions — deliberately unresolved here

- **Named attachment slots** (§5): does a prosthetic-to-body or
  device-to-person relationship need its own edge variant distinct
  from containment, or can containment's shape be reused unmodified
  with per-slot exclusivity instead of a numeric ceiling? Leaning
  toward the latter (the CAS discipline already accommodates per-slot
  exclusivity the same way `/loc`'s own single-occupant fences do) but
  not committed.
- **Historical placement for a discretely-repositioned anchor**
  (`loc-00-design.md` §5b/§15) — a subject-side instance of the same
  gap: an entity composing `loc.fence` self-anchored to itself, with
  no continuous position feed, has no historical record of its own
  past position.
- **Guarantee-strength gap for application-enforced policy** (§2): a
  substrate guard (`/obj`'s own capacity/exclusivity CAS) is safe
  under concurrent access by construction. An application-enforced
  rule ("never leaves the ward") checked before issuing a call has no
  equivalent protection — two concurrent, individually-legal-looking
  moves can still combine into a state the policy meant to forbid.
  Probably acceptable for most policy content (a violation gets
  caught and corrected, not a physical impossibility), but the two
  are different strengths of guarantee and worth stating as such
  rather than letting "the application won't allow it" quietly stand
  in for "the substrate can't produce it."
- **Stage 2 throughput** — no benchmark exists for high-frequency
  small-move workloads against the guard-bearing write path.
  `/loc`'s own R-tree read-side performance was checked empirically
  (`loc-00-design.md` §6b); the write side, for both primitives, has
  not been. `loc-02-implementation.md` states this as an exit
  criterion.

## References

- `obj-loc-industry-practices.md` — the external-research narrative:
  what was learned from Maximo/TRIRIGA/IFC/RTLS-vendor/logistics
  practice, what was adopted, what was rejected, and why. Complements
  `obj-loc-stress-test-findings.md`'s internal adversarial record.
- `loc-00-design.md`, `loc-01-rest-api.md`, `loc-02-implementation.md`
  — sibling series, direct dependency both directions.
- `pkg/storage/meta_subject.go` — the namespaced-subject convention
  §4 reuses directly.
- `pkg/refintegrity` — `x-ref` scanning, currently entity-schema-only;
  not extended by this design, deliberately (§4).
- `pkg/graph/flat_graph.go` — `wouldCreateCycle`, `AddEdge`, `FindPath`
  family; §5 and §10's precise citations.
- `pkg/bal/dxp_adapter.go`, `pkg/bal/rollup.go` — `PostCommit`,
  `EmitDeltas`, `accountKeyOf`, `journalInstant` — the guard-commit-
  then-best-effort-mirror shape §10 reuses; `RebuildRollup` specifically
  — §7a's drift-detection paragraph reuses its recompute-and-compare
  shape a second time, distinct from §10's mirror-on-commit use.
- `loc-00-design.md` §5c — the identical advisory-never-guard-bearing
  discipline applied to fence-geometry drift; §7a's drift-detection
  paragraph states its own version of the same rule rather than
  re-deriving it.
- `pkg/dxp/dxp.go` — `Participant` interface (`Reserve`, `Validate`,
  `Execute`, `Release`, `PostCommit`); `/obj` registers as a
  participant the same shape as every other primitive.
- `docs/proposals/tenant-access-control.md` §8/§9 — the self-review
  and as-built-drift convention this document follows in spirit.
- `pkg/server/v2_fsm_machine_handlers.go`, `pkg/server/v2_fsm_walk.go`
  — the def-snapshot-lineage-deletion-tracking pattern §7a extracts a
  third time: `snapshot_json` captured at machine creation, `snap.Spec`
  read at every transition (never a live `fsm_definitions` re-fetch),
  `definitionExists` as a computed-at-read check, not a stored flag.
- `pkg/server/v2_dxp_def_handlers.go` line 450 (`dxpTxnSnapshot`),
  `pkg/server/v2_dxp_dispatch.go` — the identical pattern's second,
  independent implementation; §7a's `XOLU-OBJ013` mirrors this
  document's own `XOLU-OBJ010` mutual-exclusivity shape from §9.
