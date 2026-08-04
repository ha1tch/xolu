# dxp reservation cache — Part 1: the participant contract (proposal)

Updated: 2026-07-22
Status: proposal — T-54, complete (parts 1-3). Supersedes the persisted
reservation medium of `dxp-composed-commitment.md` §5b for dxp's use
(see the reconciliation banner there); everything else in that proposal
stands.

## 1. Provenance and constraints

Three decisions are recorded in T-54 and are treated here as fixed:

1. **Reservations live in memory for their TTL**, not in any engine's
   storage. Crash means abandon, never resume; startup tombstones any
   half-written remains (part 2's subject).
2. **The cache is a substrate-level facility** defined by an interface
   **every participant primitive must honour** to engage in dxp
   transactions — consulted by the primitive's *own guard* on every
   write path, not only by the coordinator. A bal transfer arriving via
   the ordinary HTTP path must see dxp's holds, or it could spend
   balance a reservation is holding.
3. **Invalidation-by-loss is its own error fact**, distinct from
   expiry. Losing because a competitor committed is not "your
   reservation lapsed."

What survives from `pkg/reserved` (0.16.20) unchanged: the two weights
and their admission semantics, the three-tier visibility taxonomy,
deadline authority as a principle, and the doctrine that guards never
use accelerators. What changes is only the *medium*: claims live in a
process-memory structure instead of tentative rows. bal's journal
`state` column remains bal's own device.

## 1a. Terminology convention (repo-wide, adopted here)

Slash-prefixed names — **/fsm, /bal, /cal, /dxp, /meta, /blob** — always
denote the xolu-managed primitive of that name. The words **"state
machine"**, spelled out, always denote the abstract concept. Bare "fsm"
is avoided in prose from this document onward, because this design has
both in play at once: /fsm is a *participant* in dxp transactions,
while the claim lifecycle, the coordinator's phase structure, and the
canonical phase definitions of the older proposal's §8a are state
machines in the abstract sense. Go identifiers and registry-key string
literals (`"fsm"`, `pkg/fsm`) are code, not prose, and stay as they
are. Documents predating this convention (notably
`dxp-composed-commitment.md`) are historical and are not rewritten;
read their "fsm" per context.

## 2. The claim

A claim is one reserved resource, held by one dxp instance:

```go
// Claim is immutable once held; the cache never mutates one in place.
type Claim struct {
    Txn       string  // owning dxp instance id
    Primitive string  // participant registry key: "bal", "cal", "fsm" (the /fsm primitive), "blob", "entity"
    Tenant    string  // tenant scope — claims never cross tenants (v1, @D08)
    Resource  string  // primitive-scoped key: "acct:42", "cal:room7:2026-08-01", ...
    Weight    Weight  // Pessimistic | Optimistic (pkg/reserved semantics)
    Amount    int64   // magnitude for conserved resources (bal minor units);
                      // 1 for slot-shaped resources
    Deadline  int64   // unix nanoseconds, UTC. AUTHORITATIVE (§5).
}
```

`Resource` is opaque to the cache: the cache stores and filters, it
never interprets. Conflict is the primitive's judgment (§6) — the
facility owns lifecycle and time, the participant owns meaning. This is
the same division `pkg/reserved` drew ("a conflict predicate plus a
weight bit"), carried into memory.

## 3. The cache contract

One cache per xolu process, sharded by tenant internally:

```go
type Cache interface {
    // Hold registers a claim. It performs NO conflict evaluation —
    // admission is the participant guard's job (§6), and it has already
    // happened, under the same exclusion (§4), when Hold is called.
    Hold(c Claim) error

    // ClaimsFor returns the LIVE claims against a resource: lapsed
    // claims (Deadline <= now) are filtered here, unconditionally —
    // no caller ever sees a dead claim (§5). This is the guard-side
    // read.
    ClaimsFor(tenant, primitive, resource string) []Claim

    // ClaimsByTxn returns all live claims held by one instance —
    // the coordinator's view of what it holds.
    ClaimsByTxn(txn string) []Claim

    // ConfirmTxn removes an instance's claims as SATISFIED: the
    // owning transaction executed. Returns what was removed.
    ConfirmTxn(txn string) []Claim

    // ReleaseTxn removes an instance's claims as ABANDONED: explicit
    // release, instance expiry, or invalidation-by-loss. The reason
    // is the coordinator's bookkeeping, not the cache's. Returns what
    // was removed.
    ReleaseTxn(txn string) []Claim
}
```

Deliberately absent: `Invalidate`. Whether the winner eagerly marks
losers or losers lazily discover at their own Validate is item 21's
decision (recorded open in T-54); the contract above supports both —
eager is `ReleaseTxn` on the loser driven by the winner's coordinator
with a DXP invalidation error attached, lazy is the loser's Validate
finding the resource committed (§6). Nothing here forecloses either.

A janitor trims lapsed claims from memory on a timer. That is hygiene
(finiteness, @D04b's analogue) — never correctness, because §5 makes
lapsed claims invisible the instant they lapse. The janitor is the
in-memory descendant of `pkg/reserved`'s sweeper and, like it, plugs
into the gc worker abstraction for observability.

## 4. Serialisation: how guard locality survives the cache

This is the crux. The guard-locality law (@C04a) says a guarded
transition's read and write commit together. A participant's guard now
reads two places — its SQL tables (inside its transaction) and the
cache (outside any transaction). Unmanaged, that splits the read from
the write: a claim could appear between the guard's cache read and its
SQL commit, admitting a write the claim should have blocked.

The resolution uses a property the substrate already has: **per-tenant
single-writer exclusion**. Every write to a tenant's file is already
serialised. The contract therefore requires:

> **Cache mutations for a tenant happen only while holding that
> tenant's write exclusion.** Reserve (guard-evaluate + Hold),
> Execute (apply + ConfirmTxn), and every ordinary guarded write
> (guard-evaluate over tables + live claims, then commit) all occur
> under the same exclusion.

Under that rule the cache and the tenant's tables form ONE
serialisation domain: no claim can appear or vanish between a guard's
read and its commit, because whoever would change the cache is waiting
for the same writer. Guard locality holds not because the cache is in
the transaction but because nothing can interleave with the pair.
ClaimsFor from read-only paths (tier 2 advisory ingestion) needs no
exclusion — advisory planes tolerate staleness by definition.

Cross-tenant instances (multiple tenants in one def) acquire
exclusions in canonical tenant order at each phase, released between
phases — order fixed to make deadlock impossible, held briefly to keep
the fast path fast. This also gives the parked cross-tenant idea in
T-54 its natural opening, with tokens as an authorisation layer above
an already-correct serialisation layer.

## 5. Deadline authority, in memory

Unchanged in principle from 0.16.20, simplified in practice: the
deadline lives in the claim, `ClaimsFor` filters lapsed claims at read
time, and therefore a lapsed reservation stops counting the instant it
expires — no janitor run required, no confirmation possible afterward
(`ConfirmTxn` returns only live claims; the coordinator treats an
empty return where claims were expected as expiry). Coordinator death
is survivable by clock alone: its claims stop counting at deadline and
evaporate at the janitor, with nothing persisted to reconcile. That
last property — nothing persisted to reconcile — is the pivot's whole
payoff, and part 2 reduces crash recovery to tombstoning half-written
*effects*, never reservations.

## 6. The participant contract

What a primitive implements to be reserve-capable — the four verbs the
composed-commitment proposal names, with the cache threaded through:

```go
type Participant interface {
    // Reserve evaluates the primitive's guard with live claims applied
    // (per §7's weight rules), and on consent Holds a claim. Runs under
    // the tenant's write exclusion (§4). A refusal carries the
    // primitive's own error (XOLU-BAL001, XOLU-CAL...) for DXP002.
    Reserve(ctx context.Context, tenant string, op OpParams,
        txn string, deadline int64, w Weight) (Claim, error)

    // Validate re-evaluates the guard for a held claim without
    // executing (3PS). Under lazy invalidation this is where a loser
    // discovers the resource is committed to a competitor.
    Validate(ctx context.Context, c Claim) error

    // Execute applies the effect inside the participant's transaction
    // and is called under the same exclusion in which the coordinator
    // will ConfirmTxn — effect and claim retirement cannot interleave
    // with a competing writer.
    Execute(ctx context.Context, tx *sql.Tx, c Claim) error

    // Release abandons a claim's local consequences, if any. For most
    // primitives this is a no-op (the cache entry itself is removed by
    // the coordinator's ReleaseTxn); it exists for participants that
    // stage anything alongside the claim.
    Release(ctx context.Context, c Claim) error
}
```

The registry of participants (primitive name → implementation) is the
coordinator's routing table and the substrate's definition of
"reserve-capable." A primitive not in the registry cannot appear in a
def — enforced at def registration (DXP006), not discovered at run
time.

## 7. Weights at the guard

The weight semantics are `pkg/reserved`'s, re-expressed over claims:

- **Pessimistic:** the guard folds live claims into its admission
  arithmetic as if committed — available balance is `balance −
  Σ(pessimistic claim Amounts)`; a slot with a live pessimistic claim
  refuses competitors. `GuardPredicate(Pessimistic)` still governs the
  SQL side of the read; `ClaimsFor` supplies the memory side.
- **Optimistic:** guards ignore claims entirely; conflicting claims
  coexist; the first ConfirmTxn+Execute wins, and it is the winner's
  *committed state* — visible to every subsequent Validate — that
  defeats the losers. First-confirmer-wins serialisation comes free
  from §4's exclusion, exactly as it came free from the single writer
  in the persisted design.

Tier-3 (analytic) planes never see claims — nothing changes there.
Tier-2 (advisory) ingestion of pessimistic claims reads `ClaimsFor`
without exclusion and tolerates staleness, per the taxonomy.

## 8. Error surface (part 3's frontier, staked here)

New code, per the recorded decision:

| Code | Meaning |
|---|---|
| XOLU-DXP007 | Reservation invalidated: superseded by a committed competitor (winner's txn named) |

DXP003 narrows to validate-failed-for-data-drift (guard inputs changed
under an optimistic window for reasons *other* than a competitor's
commit — e.g. a /fsm machine-guard expression now false). DXP005 remains
instance expiry. Part 3 walks every failure path in the four verbs and
assigns each a single unambiguous code; the principle, fixed now, is
that **a loser is told it lost, to whom, and that this is different
from lapsing** — composition must not launder outcomes any more than
it launders refusals.

## 9. Non-goals and boundaries

- **Cross-process visibility of claims** — a claim is invisible outside
  its xolu process by construction. Multi-node reservation is nolu's
  (@D08); the participant contract is deliberately the same four verbs
  nolu would drive.
- **Persisted claims as an option** — not in v1. If a future
  participant needs reservations visible to other processes, that is a
  new weight-like axis to design, not a flag on this cache.
- **The cache as a general lock service** — Hold/ClaimsFor exist for
  the dxp lifecycle only. The moment something non-transactional wants
  a claim, it is asking for a lease, and leases are blob's department.

## 10. Open questions

- Eager vs lazy invalidation (§3) — decide at item 21 with the
  coordinator in hand.
- `OpParams` shape — per-primitive typed params vs a JSON-native bag;
  leaning typed-per-primitive with the def layer (item 20) owning the
  JSON boundary, consistent with slot-type validation at DXP001.
- Whether `Execute` receives `*sql.Tx` or a narrower store handle —
  decided when item 19 adapts the first two participants (cal, bal)
  and the real signatures are on the table.

---

# Part 2: crash-abandon and the tombstone startup pass

## 11. What can actually be half-written

The pivot's payoff is that this section is short. Enumerate every
durable write dxp v1 performs and check it against crash:

1. **Claims** — memory only. Crash clears them; nothing to clean.
2. **Effects** — v1 is single-tenant (@D08 boundary), so Execute
   applies every participant effect inside ONE SQL transaction on one
   tenant file. A crash mid-Execute is a SQLite rollback: effects are
   all-or-nothing by the engine's own atomicity. No half-written
   effects can exist in v1 — *by construction, and §13 verifies it
   anyway.*
3. **Instance records** — durable, deliberately (the transaction's
   identity, phase, deadline, and audit thread; §7a of the older
   proposal). The phase write that accompanies Execute commits IN the
   effects' transaction (@D06 degradation), so the record and the
   effects cannot disagree. The only crash residue possible in v1 is
   therefore: **an instance record resting in a non-terminal phase
   whose claims have evaporated.**

That residue is exactly what the mount-time pass tombstones.

## 12. The instance walk gains one terminal state

`abandoned` joins the terminal set:

    instantiated → reserving → reserved → executing → committed
                                  |            |
                                  +→ released  +→ (rollback → released)
                                  +→ expired
                                  +→ ABANDONED   (startup only)

`abandoned` is written by exactly one author — the startup pass — and
means: *a coordinator restart found this instance mid-flight; its
claims are gone; its effects either fully committed (then the record
already says committed and this state is unreachable) or fully rolled
back.* It is deliberately distinct from `expired` (the clock ran out
in a live process) because the two have different operational
meanings: expiry at any volume is business weather; abandonment is
always a restart, and a spike of them is an incident signature.

## 13. The startup pass (the mount-time fsck)

Runs after storage opens and BEFORE the /dxp surface accepts
instantiations — the rest of xolu serves normally meanwhile; only dxp
gates on it. Steps, each idempotent, the pass itself safe to die in:

1. **Sweep non-terminal instances.** Query the partial index over
   non-terminal phases (§7a machinery, reused). For each: CAS the
   phase to `abandoned` (`WHERE phase = <observed non-terminal>`),
   rows-affected checked. The CAS makes the pass re-runnable and safe
   against a concurrent pass — first writer wins, second no-ops.
2. **Assert the §11 invariant.** For each abandoned instance, verify
   no participant effect row exists bearing its txn id (participants'
   Execute writes carry the txn id — the `pkg/reserved` convention's
   audit thread, retained in the pivot for precisely this check). In
   v1 a hit is IMPOSSIBLE by §11's argument; the check exists because
   "impossible" is an invariant worth a guard, not a shrug — exactly
   the class of assertion fsck performs on journals it believes clean.
   A hit logs at error, marks the instance `abandoned-dirty`, and
   fails the pass loudly under XOLU_STRICT_DXP: this is the one state
   the design says cannot happen, so its observation means the design
   or the code is wrong, and serving traffic on top of it would be
   malpractice.
3. **Emit observability.** One ts event per tombstone
   (`dxp.abandoned`, per-def series) — tier 3, commit-fed, never read
   back by any guard (§7a's telemetry doctrine).
4. **Open the surface.**

Ordering note: the pass needs no lock against live traffic because
step 4 hasn't happened — the surface is closed, and non-dxp writers
cannot create dxp instance records. The pass is single-threaded
sequential; at any plausible instance cardinality it is milliseconds.

## 14. What part 2 pre-shapes for cross-tenant (and refuses to build)

The future multi-tenant instance (T-54's parked idea) commits effects
per tenant file, and a crash between two tenants' commits creates the
partial state v1 cannot have. Part 2 does NOT design that recovery; it
only keeps two doors open at zero v1 cost: (a) participant effect rows
carry the txn id (already true), so a future pass can determine
per-participant completion from each tenant's own file; (b) the
instance record stores its participant roster, so the future pass
knows what to look for. With those two facts durable, cross-tenant
recovery becomes a deterministic per-participant audit rather than a
forensic one. Everything further is nolu-adjacent design and is
refused here.

---

# Part 3: the error taxonomy — every failure path of the four verbs

## 15. Principles

Fixed by T-54's recorded decision and §8: (1) a loser is told it
lost, to whom, and that losing differs from lapsing; (2) composition
never launders a participant's refusal — the underlying error rides
inside the DXP error; (3) every terminal outcome of an instance is
distinguishable from every other by code alone. One new principle,
earned by part 2: (4) *crash-abandonment is its own fact* — an
operator must be able to tell "the clock ran out" from "we restarted"
without reading logs.

## 16. The full walk

Instantiation:

| Path | Code |
|---|---|
| Bindings fail slot types | XOLU-DXP001 |
| Def references an unregistered participant | XOLU-DXP006 (at def registration — unreachable at instantiation by construction) |

Reserve:

| Path | Code |
|---|---|
| A participant guard refuses (funds, slot, walk-legality) | XOLU-DXP002, refusing participant named, its error carried |
| Verb arrives out of phase (double-reserve, reserve-after-release) | XOLU-DXP004 |
| Instance deadline lapsed before/while reserving | XOLU-DXP005; claims released |

Validate (3PS; also the eager/lazy pivot point):

| Path | Code |
|---|---|
| Resource committed to a competitor since reserve | **XOLU-DXP007** — winner's txn named |
| Guard inputs drifted for any non-competitor reason (/fsm machine-guard now false, balance moved by non-dxp traffic under optimistic weight) | XOLU-DXP003 (narrowed), participant error carried where one exists |
| Claim lapsed (instance reserve-phase TTL) | XOLU-DXP005 |
| Verb out of phase | XOLU-DXP004 |

Execute:

| Path | Code |
|---|---|
| Participant Execute errors; transaction rolls back whole | **XOLU-DXP008** — participant named, error carried; instance → released (effects rolled back, claims released; the caller may re-drive from reserve) |
| Confirm finds no live claims (deadline passed between validate and execute) | XOLU-DXP005 |
| Phase CAS loses to a concurrent driver | XOLU-DXP004 (exactly-one-driver is the caller's contract; the CAS is its enforcement) |

Release: idempotent and unconditional — releasing a terminal or
unknown instance returns success with a no-op indication, never an
error. A cleanup verb that can fail invites the retry loops it exists
to end.

Startup:

| Path | Code |
|---|---|
| Instance tombstoned by the mount-time pass | **XOLU-DXP009** — surfaced to any later status query: "abandoned at coordinator restart, <timestamp>" |
| §13 step-2 invariant violated | XOLU-DXP010 — never returned to API callers; an internal alarm code for logs, strict-mode fatality, and the register |

## 17. The complete family, restated

001 bindings · 002 reserve refused (carried) · 003 validate drift
(carried) · 004 phase order · 005 expiry (instance or claim; the
clock) · 006 definition invalid · **007 invalidated-by-loss (winner
named)** · **008 execute failed (carried; rolled back)** · **009
abandoned at restart** · **010 abandonment invariant violated
(internal)**. Codes 003/005/007/009 partition "your transaction did
not happen" into drift / clock / competitor / crash — the four
different facts T-54's decision demanded be distinguishable. Reserved
against pkg/errors in the implementing release; the 0.16.20 comments
on 001–006 stand.

## 18. Taxonomy non-goals

No retry-advice encoding in codes (whether 003 or 007 warrants a
retry is the def author's business logic, not the substrate's); no
partial-success codes (there is no partial success to report — §11);
no error aggregation across participants (the FIRST refusal aborts
reserve; a def wanting full-roster diagnostics is asking for a
dry-run verb, which is future surface, not error design).
