# dxp coordinator design — item 21

Updated: 2026-07-29
Status: proposal — designed in full across an extended session, nothing
implemented yet. Companion to `dxp-composed-commitment.md` (the
foundational proposal, item 18 and the participant contract) and
`oql-primitive-queries.md` (unrelated wave, no dependency either way).
This document exists because item 21's actual design — the
`ParticipantStore` abstraction, the attendance protocol, the
durability decision and its two independent justifications, and the
canonical-doctrine verification that shaped several of these choices —
had grown substantial enough that leaving it scattered across
conversation history rather than a real document was itself a risk.

## 1. Starting point: the interface's SQL coupling

`dxp.Participant.Execute(ctx, tx *sql.Tx, c Claim) error` hardcodes a
SQL transaction into the contract — confirmed directly against the
interface's own doc comment: *"every participant in one instance
shares this tx"* is not an incidental detail, it's the stated
assumption. A Pebble-only participant cannot honor this contract as
written; Pebble has no type compatible with `*sql.Tx`. Checked
directly, not assumed: zero references to `IndexStore` anywhere in
`pkg/cal/dxp_adapter.go` — the one existing adapter with a Pebble
satellite (H3, the occupancy index) never touches it, by design,
because there was nowhere for it to plug in.

## 2. `ParticipantStore` and `TxnStores`

```go
// ParticipantStore is the coordinator-supplied handle a participant's
// Execute writes through. Participants type-assert to their own
// engine-specific concrete type — the same pattern OpParams already
// uses (marker interface, concrete per adopter) — never see *sql.Tx
// or *pebble.Batch in the interface signature itself.
type ParticipantStore interface {
	Engine() string // diagnostic only — "sql", "pebble"

	// Ready is called BY the participant, from inside Execute, the
	// moment it is actually about to write — not before. This is what
	// starts the coordinator's guard for this participant; duration
	// is fixed coordinator-side at store construction, never visible
	// to or influenced by the participant. Only the participant knows
	// when its own internal work is actually done and it's about to
	// touch the store — only the coordinator owns how long it then
	// gets and what happens on timeout. Idempotent.
	Ready(ctx context.Context) error

	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
}

// TxnStores holds one store per participant, indexed by the def's own
// participant array order — the order participants are listed in the
// def's JSON, nothing more. This was originally written as "the def's
// canonical participant ordering," a concept later dropped entirely
// (§12, and item 20's own actual implementation has no such thing):
// Reserve never blocks, so no circular wait is possible regardless of
// ordering, and there is nothing for a computed ordering rule to
// protect against in this design. Corrected here rather than left
// stale once that correction was made elsewhere in this document.
type TxnStores []ParticipantStore
```

Two concrete implementations:

```go
// SQLStore wraps *sql.Tx, for bal/fsm/entity/cal-H1.
type SQLStore struct {
	Tx   *sql.Tx
	owns bool // false under collapse — see §3
}

// PebbleStore wraps *pebble.Batch, for ts/cal-H3/any future Pebble
// participant. No collapse case exists for Pebble — not because @D06
// names engine type (it doesn't; its stated condition, checked
// directly against the framework's own source, is single-tenancy),
// but because *pebble.Batch has no representation inside *sql.Tx, so
// "collapse into one SQL transaction" mechanically cannot include it
// regardless of tenant scope. A Pebble store is always genuinely
// independent.
type PebbleStore struct {
	Batch *pebble.Batch
}
```

`Batch.Commit(o *WriteOptions) error` and `Batch.Close() error` —
checked directly against the vendored `cockroachdb/pebble@v1.1.5`
source, not assumed from memory.

Native handles are passed **already open** — constructed and opened
by the coordinator, handed to the store, but not *usable* until
`Ready()` returns. "Open" and "usable" are deliberately two different
states; this is what lets `Execute` write through a real handle
without the coordinator having had to decide in advance exactly when
each participant would be ready to use it.

## 3. Collapse-to-ACID under this abstraction

Multiple `SQLStore` instances can wrap the *same* underlying `*sql.Tx`
when static analysis (item 20, per `§4`'s own registration-time
soundness check) proves the participant set collapses. Exactly one
carries `owns: true`; the rest are no-ops on `Commit`/`Abort`. The
coordinator calls the real `tx.Commit()` itself, once, outside any
individual store's own `Commit` — never routed through a "shared"
store's call. This preserves the *existing*, already-tested behavior
(`TestMultiParticipant_HotelStyle_AtomicCommit`,
`TestMultiParticipant_PartialFailure_NothingCommits`) exactly — those
tests exercise the collapsed path, unchanged by this design.

## 4. Attendance

Renamed from an earlier, ambiguous "quorum" — corrected specifically
to avoid collision with the canonical framework's own **Quorum
Modifier (QM)**, a genuinely different concept (see §8). The
coordinator waits for **all** N participants to succeed `Validate`
before calling `Execute` on *any* of them. Verified, not assumed, to
be exactly plain 3PS's own formal definition: `dxp-11-proof-3ps.md`'s
Theorem 1 requires `∀pᵢ ∈ P: Validate(pᵢ) = VALIDATED` for the
safe-to-execute case — unanimous, not a subset. `attendance` is a
correct instantiation of the canonical definition, not an extension of
it.

This does not conflict with 3PS's own distinguishing property
(`§2a`: *"independent per-participant phase progress... races caught
by validation rather than prevented by locks"*) — that property
describes relationships *between participants* (no participant blocks
on another participant directly). The coordinator has always had
global visibility participants don't (it already needs to track
commit progress); having it also gate Execute on full attendance is
the coordinator doing coordinator things, not participants losing
independence.

## 5. Execution: `Ready`, concurrent commit, `committed_through`

Per-participant sequence, all calls made by the coordinator except
`Ready` itself:

```go
err := participant.Execute(ctx, store, claim) // calls store.Ready() internally, once it's about to write
if err != nil { store.Abort(ctx); return }
store.Commit(ctx)
```

**Commits run concurrently across participants once attendance is
established**, not sequentially — verified safe, not merely
convenient. Non-collapsed: independent native handles, nothing shared,
nothing to race. Collapsed: only the `owns: true` wrapper's `Commit`
ever touches the real `Tx`; the rest are no-ops regardless of
dispatch order. Concurrency also *shrinks* the torn-commit window
(§6) — sequential, the exposure is the sum of every commit's
duration; concurrent, it's the max of one.

`committed_through int` tracks how many participants have committed —
**a bare cardinality, not positional or a bitmask.** Working through
why a bitmask isn't needed: there is no `Compensate`/selective-repair
verb anywhere in `dxp.Participant` (confirmed, deliberately absent —
see §7); a torn instance is handled wholesale, never per-participant.
Recovery only ever asks `count == total` or `count < total past
deadline` — order and identity were never load-bearing.

**Given §7's no-durability decision, `committed_through` is a bare
in-memory counter, incremented inside each store's own successful-
commit branch — no guarded SQL update, no database round-trip per
commit.** (An earlier pass of this design assumed durable tracking was
needed and specified a guarded `UPDATE ... SET committed_through =
committed_through + 1` write; that's now unnecessary given §7, and
recorded here as the simpler final shape, not the intermediate one.)

## 6. The torn-commit problem, and why tombstone+GC is the answer, not a compromise

The real question 2PC-lineage protocols exist to answer: participant
A's `Commit` succeeds, durably; participant B's then fails. A is now
permanently applied; B never happened. Genuine cross-engine atomic
commit is hard — this is *why*, not a gap specific to this design.

**Resolution, direct instruction (2026-07-29): tombstone the instances
that don't pan out, collect garbage periodically.** Not prevented —
detected, marked, cleaned up. This is the same *shape* of answer
`T-54`'s own abandoned-terminal-state-plus-sweep machinery already
gives for reserve-phase abandonment, applied one layer further in,
not a new mechanism.

**Given §7 (no durability across restarts), this doesn't even need its
own terminal state.** A mid-execute crash simply means the instance
sits until its existing deadline passes; the sweep worker that already
exists (item 18, `T-54`) picks it up as ordinary `expired` — no new
state, no new subsystem. One small, optional addition worth keeping:
log whether an expiring instance had `committed_through > 0` before it
expired, purely for operator visibility into how often the rare case
actually happens. Not required for correctness.

## 7. The durability decision, and its two independent justifications

**Direct instruction, unambiguous: "we're not going to implement
persistent transactions. We may never do that."** In-memory-only
reservations, crash-abandon semantics — a process restart loses every
live claim, by design (T-54's pivot away from persisted tentative
rows), not an interim state awaiting completion.

Two separate reasons this is sound, worked through rather than
assumed, and worth keeping distinct because they answer different
objections:

**Reason 1 — mid-execute crashes are expected to be extremely rare.**
`Ready()`'s guard window is short by construction; the common case
(collapsed ACID) doesn't have this failure mode at all, since nothing
is durably committed until the one final commit. Idempotent
re-execution is the expensive piece durability would actually require
— checked concretely, not hypothetically: bal's own shipped
`transfer_id` has no uniqueness constraint or dedup check today;
calling `Transfer` twice would silently double the movement. "Resume
by re-executing" is unsafe until every adapter is retrofitted for it —
a real migration against already-released code, not a detail. With
crashes this rare, that cost isn't justified by the risk it would
close.

**Reason 2 — hotswapping instances between servers doesn't require it
either, and for a different reason than crash rarity.** Direct
clarification (2026-07-29): xolu's intended hotswap model is
cooperative — the outgoing instance redirects what it can and *waits
for confirmation* the new instance is running smoothly before taking
itself down. That's a **nolu-owned mechanism**, not a dxp one. What
exists today toward it, checked directly: `cmd/xolu/main.go`'s SIGTERM
handler — stop accepting new requests, 15-second grace period for
in-flight ones, then close. A genuine minimal foundation (the
"let in-flight work finish" half) but no redirect-to-another-instance
mechanism and no await-confirmation mechanism yet — both remain
nolu's to build. Because the handoff is cooperative, an in-flight dxp
instance has time to reach a natural stopping point: Reserve/Validate
with no native handle open yet can simply be dropped, client retries
against the new instance; Execute/Commit already in its short guard
window can be allowed to finish before the swap completes. A drain
against a timeout, not a durability mechanism, and not a dxp concern
to build.

### Consequence: idempotency is not needed in the current design

Worked through directly: idempotency only mattered for *resuming* a
known in-flight instance and re-calling `Execute` against a fresh
handle for not-yet-committed participants. With zero memory of the
instance surviving a crash, the coordinator will never call `Execute`
a second time for it — nothing to resume means nothing to accidentally
double-apply through resumption. (Client-side retry idempotency — a
caller unsure whether its request succeeded, retrying with a fresh
instance ID — is a separate, pre-existing concern, not created or
worsened by this design; it's the same gap the `transfer_id` finding
already exposed in the plain, non-dxp `/bal/transfer` endpoint today.)

### The cost of "3PS proper" (durable, resume-to-completion), assessed for if this is ever revisited

Not a uniform tax — concentrated specifically on the genuinely-phased,
multi-native-handle case (SQL+Pebble), since the collapsed case can
always just retry the whole instance cheaply (nothing was durably
committed until the one final commit). For the phased case, true
resume would require: (1) durably persisting every participant's full
`OpParams` at instance creation, not the in-memory `pending` maps all
four current adapters use today; (2) a durable "decided: execute"
marker written strictly before any Execute begins; (3) genuine
idempotency retrofitted into bal, fsm, entity, and cal individually —
each its own adapter-specific migration and test effort, the same
kind of care this session already spent building each one; (4) a
standalone recovery/resume subsystem, comparable in scope to the
coordinator itself — not an extension of the sweep worker, since sweep
expires and releases while resume re-executes, a different operation.
Rough shape: could double or triple what's left of wave 5, to
correctly handle a failure mode expected to be rare by construction.
Filed as "pending, not abandoned" in `SUBSTRATE_DEVELOPMENT_PLAN.md`
§6 (Deferred), alongside 2PS and the wider phase-spectrum, with the
same trigger: cross-tenant and/or cross-instance dxp transactions,
should either ever materialize.

## 8. Canonical doctrine, verified directly against the source repo, not cited from memory

Corrections made while designing the above, recorded here rather than
left implicit:

- **The proofs are genuine.** `dxp-11-proof-3ps.md`,
  `dxp-12-proof-2ps.md`: real theorem statements, formal notation,
  structured case analysis (Atomicity, Consistency, Isolation,
  Durability). An earlier pass of this conversation claimed no such
  proofs existed anywhere in the doctrine — wrong, because only two of
  seventeen files in the repo's `doc/` tree had been checked (the
  README's own "Documentation Structure" links only seven; the repo
  has seventeen). Corrected by actually cloning the repo rather than
  fetching individual pages one at a time.
- **`QM` (Quorum Modifier) is not what an earlier pass claimed.**
  `dxp-13-quorum-modifier.md`/`dxp-14-proof-3ps-plus-qm.md`: relaxes
  3PS's unanimous-attendance requirement to a majority
  (`Q = ⌈n/2⌉+1`) **within one transaction's own participant set** —
  tolerating a minority of unavailable participants. Not a mechanism
  for replicating a transaction across independent xolu instances,
  which is what an earlier claim in `SUBSTRATE_DEVELOPMENT_PLAN.md`
  asserted by word-association alone. Fixed at the source in both that
  document and `dxp-composed-commitment.md`.
- **The canonical `Participant` interface is materially different
  from xolu's**, and the two should not be read as the same contract.
  Canonical (`dxp-01-guide.md §9`, five verbs): `Prepare`, `Execute`,
  `Compensate`, `Reserve`, `Validate` — plus a full `Messenger`
  interface (NATS/Kafka/RabbitMQ), built for genuinely separate
  network services. xolu (four verbs): `Reserve`, `Validate`,
  `Execute`, `Release` — no `Compensate` at all (tombstone+GC chosen
  instead, per §6), no message transport, because everything runs
  in-process against a shared file. xolu is a deliberate
  reinterpretation for a different deployment shape, not a literal
  implementation of the canonical interface.
- **Canonical 3PS's own recovery model is stronger than what xolu
  chose, and that's a real trade-off, not an oversight.** `dxp-11`'s
  own proof of atomicity, for coordinator failure during Execute,
  verbatim: *"Recovery protocol required. Participants query
  coordinator's persistent decision log. All see same decision.
  Ensures atomicity."* That's resume-to-completion via a durable
  decision log — nothing is ever left torn. xolu's tombstone+GC choice
  (§6) is a deliberate, pragmatic simplification relative to this,
  justified by §7's two reasons, not an equivalent restatement of the
  canonical guarantee. Worth being precise that "we're doing 3PS,
  proven correct" and "we're doing a deliberately weakened variant of
  3PS, for good pragmatic reasons matching the doctrine's own
  degrade-gracefully instinct" are different claims — this document
  asserts the second, not the first.

## 9. Deferred, deliberately: sequential/dependent participants

Not plain 3PS — a participant whose params depend on an earlier
participant's *result* is not exhibiting 3PS's own distinguishing
property (independent phase progress; §2a). Re-reading `§4` with this
in mind: *"the mixed intermediates (1.5ps, 2.5ps) as declared
per-participant mixtures"* — the framework already anticipates
heterogeneous method-mixing within one def. A dependent participant is
closer to `1ps` (saga-shaped, inherently sequential) for that specific
leg, mixed alongside genuinely-independent 3PS participants in the
same instance — a real, named idea in the doctrine, just not packaged
as one of the four (now five, with QM) modifiers. Checked directly:
none of OV/TBS/GA/SC/QM cover data-dependency between participants;
`SC` (Selective Consistency) is the closest in spirit — heterogeneous
per-participant treatment — but it's about consistency *strength* per
participant, not data flow between them.

**Design sketch, not committed:** each participant declares an
optional `depends_on: []string` (other participants' `id`s in the same
def); registration-time analysis (item 20) validates this forms a
DAG, a cycle refused at registration per `§4`'s own soundness-check
principle. The coordinator computes execution *waves* from the DAG
once, at instance start — wave 1 is every participant with no
unresolved dependency (concurrent, as in §5); once a wave fully
commits, the next becomes eligible.

**Result-passing, if this is ever built:** `Execute` returning
`Result` (§10) is captured by the coordinator the moment a
participant's `Execute` succeeds — before `Commit` even runs, which
sidesteps any read-after-write staleness question, since the
coordinator already holds the value rather than re-querying storage.
It must not be *exposed* to a later wave's `Reserve` until the
producing wave reaches full attendance-commit, not merely executed —
keeping this consistent with §6: if an earlier wave partially fails,
a later, dependent wave must never have started against its data at
all. A dependent chain doesn't introduce a new failure mode — if wave
1 commits fully and wave 2 tears, the whole *instance* tombstones,
exactly as any single wave's partial commit already would.

Binding mechanism, if built: reuse `pkg/fsm/eval`'s existing
`QualifiedIdentifier` → flat-key resolution unmodified —
`result.<participant_id>.<field>` parses and resolves through the
identical path already used for `payload.`/`query.`, confirmed by
tracing the actual evaluator code, not assumed by analogy. See
`SUBSTRATE_DEVELOPMENT_PLAN.md` §6 for the (separately deferred,
independent-in-time) `pkg/eval` extraction this would eventually want.

**Status: pending, not committed, not scoped into wave 5's exit
criterion** — the hotel worked example's four participants are
genuinely independent, so this isn't required for wave 5 to be
considered done.

## 10. `Result`

```go
// Result is the opaque JSON-shaped value a participant's Execute may
// return alongside success. Two independent consumers, never
// conflated:
//   - Dependency-binding (§9, deferred): only top-level scalar
//     fields, matching payload./query.'s own restriction exactly.
//   - Webhook/log delivery: the whole value, structure intact,
//     delivered only once the instance reaches a terminal state —
//     the same "commit-fed, strictly" rule (§5c of
//     dxp-composed-commitment.md) applied to a new consumer, not a
//     new principle.
// nil is valid — a participant with nothing worth reporting returns
// it; Result is opt-in per call, never mandatory.
type Result map[string]interface{}
```

`Execute`'s full, revised signature:

```go
Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error)
```

Added now rather than later specifically because the signature is
already changing for `ParticipantStore` — every current adapter's
return statement gets `, nil` instead of `nil`, effectively free,
avoiding a second breaking change to four adapters if either consumer
(§9's dependency-binding, or webhook/log delivery) is ever built.

**Explicitly not scoped by this addition:** the actual delivery
mechanism — POSTing to a webhook, writing a structured log line — is
not part of item 20, 21, or 23. Only capture (the return value
existing, the coordinator accumulating results per participant) is
being added now. Delivery is separate, unscoped, future work.

## 11. What this changes about existing code

- `pkg/dxp/dxp.go`: `Participant.Execute`'s signature changes
  (`tx *sql.Tx` → `store ParticipantStore`; return gains `Result`).
  New types: `ParticipantStore`, `SQLStore`, `PebbleStore`,
  `TxnStores`, `Result`.
- `pkg/bal`, `pkg/storage` (fsm, entity), `pkg/cal`: one-line change
  each in `Execute` — type-assert the concrete store instead of
  receiving `*sql.Tx` directly; add `, nil` to existing return
  statements. No other adapter logic changes. All four are already
  tested; this is the kind of change the existing test suites should
  catch immediately if done wrong.
- New: the coordinator itself (item 21) — attendance gate, per-
  participant `Ready`/`Execute`/`Commit` dispatch (concurrent once
  attendance is established), `committed_through` (in-memory
  counter), result accumulation. Builds on item 18's existing durable
  instance records and sweep worker; does not require the sweep
  worker to change beyond recognizing that a torn instance simply
  falls into its existing `expired` handling (§6).

## 12. Open, not resolved here

- Item 20's own registration-time static analysis needs to actually
  validate collapse-eligibility and (if §9 is ever built) `depends_on`
  DAG acyclicity — this document assumes item 20 provides these,
  doesn't design item 20 itself. Canonical participant ordering,
  originally listed here too, was dropped (2026-07-29): worked through
  directly and confirmed unnecessary — `Reserve` never blocks (it
  refuses immediately rather than waiting), every `Reserve` call is
  already serialized through one tenant lock, so there is no state
  where two participants each hold one resource and wait on the
  other. No circular wait is possible regardless of ordering, so there
  is nothing for an ordering rule to protect against in this design.
- The `torn`-instance-visibility logging suggested in §6 (whether
  `committed_through > 0` before expiry) is a nice-to-have, not
  specified in detail — exact log shape, whether it needs its own
  metric, left to implementation time.
- `PebbleStore` is designed but has no real consumer yet — `ts` has no
  dxp adapter, and `cal`'s H3 remains deliberately untouched by
  `Execute` (per `T-83`, still open). This document makes a Pebble
  participant *possible*; it doesn't make one exist.

## 13. The aci-D framework, and how this design maps onto it

Two real corrections happened working through this section, both
recorded here rather than smoothed over, because both are instances of
the same failure mode this whole document has otherwise been careful
about: constructing a plausible, specific-sounding claim and presenting
it with the confidence of a citation, without having actually checked
the source first.

**Correction 1.** First claimed durability was the one ACID property
genuinely weakened in this design, with atomicity/consistency/isolation
held at full strength. This is backwards. Checked directly against
`dxp-04-theoretical-foundations.md`, the framework's own definition:

```
- atomicity (lowercase 'a'): Best-effort atomicity within boundaries
- consistency (lowercase 'c'): Eventual consistency with convergence guarantees
- isolation-Deemphasized: Minimal isolation, managed through design
- Durability: Local durability with distributed backup
```

Atomicity, consistency, and isolation are the three explicitly
weakened properties; Durability is the one that stays capitalized —
the opposite of what was first asserted.

**Correction 2.** Separately, §3 above once attributed a specific
sentence — "every guard-bearing participant shares one tenant SQLite
file" — to `@D06` as its stated collapse condition. That sentence does
not exist verbatim anywhere in `dxp-composed-commitment.md`. `@D06`'s
actual, verbatim condition (`§6` of that document) is that the
participant set is **single-tenant** — nothing about engine type.
Whether a Pebble participant blocks collapse is a real conclusion, but
it's this document's own mechanical inference (`*pebble.Batch` has no
representation inside `*sql.Tx`), not a quotation from the doctrine.
Fixed at every location this citation had spread to: this document
(§3), `SUBSTRATE_DEVELOPMENT_PLAN.md` (item 40's row and the wave 5
narrative), and `TRACKING.md` (`T-86`).

### Working through the aci-D mapping properly, with the correct definition

- **a**tomicity, best-effort within boundaries — matches: attendance
  (nobody executes until everybody's validated) plus tombstone-on-tear
  is exactly this, bounded to one instance, not a promise across
  instances or across a coordinator restart.
- **c**onsistency, eventual with convergence — matches, and this is
  what actually explains why `T-83` (the post-commit invalidation
  verb, demoted off the critical path but still real and still open)
  is a coherent part of the design rather than an optional nicety:
  bal's rollup and cal's H3 catching up after commit, self-healing via
  the existing oracle machinery, is eventual consistency with a
  convergence guarantee, named precisely rather than just "good
  enough for now."
- **i**solation, deemphasized, managed through design — matches,
  checked structurally rather than asserted: no adapter's code
  references another primitive anywhere (confirmed directly — bal's
  `Execute` has zero awareness cal exists), and the shared `MemCache`
  doesn't undermine this either, since resource keys are
  primitive-prefixed (`"entity:widget:42"` vs `"cal:room-a:..."`) —
  one cache, disjoint namespaces, isolation handled in the claim
  layer rather than by a database lock level.
- **D**urability, local with distributed backup — matches, but the
  scope matters: each participant's own commit is exactly as durable
  as that engine's ordinary writes always are, unweakened. What's
  absent is the *coordinator's* own instance-tracking surviving a
  restart (§7's deliberate decision) — and that absence doesn't
  contradict aci-D's capital D, because ACID's own "D" was always
  about "once committed, stays committed," not about the coordinating
  process surviving a crash. Local durability without coordinator-level
  durability is a real instantiation of aci-D's D, not a violation of
  it.

### The ACID check, worked through property by property, direct instruction (2026-07-29)

Atomicity — "the whole process stops and undoes any changes" — holds
for the dominant case (attendance never reached, or a collapsed
instance's `Execute` failing pre-commit: nothing was ever committed,
so there is nothing to undo) and is **unambiguously** the intended
reading; the torn-commit case (participant A commits, B then fails) is
a separate, explicitly accepted philosophical stance, not a violation
of this "yes": atomicity is judged from the transaction's own
authoritative vantage — the coordinator's verdict — not from whether
every physical trace everywhere is erased. If the coordinator never
commands full commit, its answer is "this did not happen," regardless
of what a premature participant write physically did; the tombstone
*is* the undo, at the layer that actually matters to a caller relying
on dxp's own record of truth. Scope boundary, stated plainly rather
than left implicit: this guarantee holds for anyone treating the dxp
instance as their source of truth. It does not mean a participant's
own data is retroactively sanitized for someone querying that
primitive directly, bypassing dxp — A's orphaned row is a real row,
still there, discoverable in isolation. There is no `Compensate`
reaching back to remove it (§7's own point, restated in this context).

Consistency, isolation, and durability were each confirmed "yes" the
same way, checked rather than assumed — full detail folded into the
aci-D mapping above rather than repeated twice.

### Faithful participants

The coordinator's own correctness assumes participants are faithful to
the contract they've implicitly agreed to by implementing
`dxp.Participant`: `Abort()` really means abort, `Commit()` really
means commit (2026-07-29, direct instruction). Worked through rather
than accepted as a bare assumption: for every participant that exists
today, this mostly isn't trust in the adapter author at all — each
adapter's `Commit`/`Abort` is a thin pass-through to the underlying
engine's own primitive (`Tx.Commit()`/`Tx.Rollback()`,
`Batch.Commit()`/`Batch.Close()`), and *those* already can't lie: an
uncommitted or rolled-back SQL transaction leaves zero trace by
SQLite's own guarantee, not by anything an adapter has to get right
through careful coding; an unclosed Pebble batch touches nothing until
`Commit()` is explicitly called, enforced below the adapter entirely.

The one place this doesn't fully close: an adapter's `Commit()` could,
through a bug rather than malice, receive a real error back from
`tx.Commit()` and swallow it, returning `nil` anyway. The coordinator
has no way to detect this — `committed_through` increments, the
coordinator believes full success, and it's wrong. Not an adversarial-
trust risk (everything here is xolu's own in-process code, there is no
hostile participant to defend against) — a coding-discipline risk,
concrete and testable once `SQLStore`/`PebbleStore` exist: force the
underlying `tx.Commit()` to fail (closed connection, constraint
violation at the wrong moment) and confirm the adapter's own `Commit()`
propagates the error rather than reporting false success. Worth being
an actual test in the suite when item 21 is implemented, not merely
assumed correct because the design says so.

**Where this connects to the framework's own doctrine, checked directly
rather than guessed from a section title:** `dxp-04-theoretical-
foundations.md §4`, "Conventions Over Protocols," is related in spirit
— less enforced machinery, more reliance on shared understanding
rather than verified handshakes — but it is not precisely the same
claim. That section is about semantic agreement on what *state names*
mean ("Reserved means: resources allocated but not consumed"), not
about whether a participant's `Commit()` call, once invoked, actually
performs a real commit. Adjacent, not identical: one is "do we agree
on what 'committed' means," the other is "when you say you did it, did
you." The framework doesn't address the second question directly in
this section — the answer, for xolu's own design, is the engine-level
reduction above.

### A concrete recurrence of the same bias, this time about lifecycle shape rather than ACID properties

Working through `POST /dxp/txn`'s design (2026-07-29), a real
back-and-forth surfaced a second instance of exactly the instinct
named above — not a new failure mode, the same one, applied to a
different question. Asked to keep validating bindings "whenever new
ones arrive," the first reading defaulted to *one instance, assembled
incrementally across multiple requests, needing some separate signal
for "now begin the phases."* That's a database-transaction habit —
`BEGIN`, accumulate statements, only then commit — not anything the
design had actually specified.

The correct reading, once named plainly: `dxp/txn` is closer to a
stored procedure than a SQL transaction. The definition is the
procedure body, validated once at registration. `POST /dxp/txn
{def_id, bindings}` is one complete invocation — called many times
over the def's life, with different arguments each time, each call
fully self-contained and independently validated against
`bindings_schema_json`. "New ones keep arriving" meant *many separate
calls*, not one call built up in pieces. Under that reading, the
"implicit vs. explicit start" question this correction had been about
to design doesn't exist — there's no partial state to ever mark
complete, because nothing arrives partial in the first place.

Same root cause as the aci-D and durability-model corrections above:
the dominant training-data shape for "a transaction" is one
long-lived, stateful, assembled-then-committed SQL transaction, and
that shape gets reached for by default even when the actual design —
stated plainly, multiple times, across this whole effort — has never
worked that way.
