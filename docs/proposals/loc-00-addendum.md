# loc-00 — Addendum: what changed since the original design, and why

Companion to `loc-00-design.md`, not a substitute for it. Written
because `/loc`'s first implementation is being wrapped up in parallel
with this document's own writing — an implementer reading `loc-00`
today sees the current shape but not the reasoning trail behind the
parts that moved, and that trail is currently scattered across a long
conversation history. This document recovers it, once, in one place,
with the supporting evidence cited rather than asserted.

Baseline for comparison throughout: `loc-spatial-primitive.md`, the
original single-file proposal, preserved in the document series with
its own reconciliation banner. Every claim below about "the original
said X" is checked directly against that file, not recalled from
memory.

`/obj`'s own changes are not covered here — `/obj` has no shipped
implementation yet, so its design simply carries the current, corrected
shape directly; there is no earlier-implemented state to reconcile
against.

**Extended 2026-08-01** with a second phase this document's original
framing didn't anticipate: `/loc` finished implementation (wave 9,
T-115–T-118, `CHANGELOG.md` v0.21.0) and then went through a real
adversarial hardening pass (v0.21.1) shortly after. That surfaced its
own round of findings — some requiring correction of what the design
documents claim, most extending them with detail no design pass could
have had before real code existed to test against. Numbered 11 onward
below, continuing the sequence rather than starting a new one, because
the reasoning discipline is identical: what changed, why, and what
evidence supports it.

---

## Quick reference

| # | Change | Section | Driven by |
|---|---|---|---|
| 1 | dxp participant count corrected: "fifth of four" → "sixth of five" | §9b, §12 | Factual correction against shipped v0.20.0 code |
| 2 | Subject registry resolved: entities only | §3c | Reuse-discipline review |
| 3 | Standalone-fence capacity mechanics resolved | §5a | Reuse-discipline review |
| 4 | Move sequencing revised (fence checks join the guard-bearing transaction) | §7a | Consequence of #3 |
| 5 | §7c formally signed off | §7c | Reuse-discipline review |
| 6 | Surface sketch marked superseded by `loc-01-rest-api.md` | §11 | Documentation-series split |
| 7 | Standalone-fence *identity* resolved: composes onto an entity, not a caller-chosen `fence_id` | §5 | Adversarial stress-testing against `/obj`'s design |
| 8 | Historical placement for a moving anchor resolved | §5b | Adversarial stress-testing, finding #9 |
| 9 | Boundary mutation resolved: reconciliation via `bal`'s rollup pattern | §5c | This session, corrected twice before landing |
| 10 | Fence-type patterns added (named "templates" originally, renamed later — see entry #10's own note) | §5d | This session, direct extension of `/obj` §7a |
| 11 | Standalone-fence identity: design confirmed correct, **shipped narrower** — bare `fence_id`, not entity-composed | §5, `loc-01-rest-api.md` §2 | v0.21.0's own conscious v1 scope narrowing, found against real code |
| 12 | dxp storage separation named precisely: `loc` needs its own database handle, not just its own registry entry | §9b, `loc-02-implementation.md` Stage 5 | Real bug (`"no such table: loc_capacity"`), fixed following `ts`'s precedent |
| 13 | §4e extended: the near-pole division singularity, exact fix shape | §4e | v0.21.1 adversarial testing + `go test -fuzz` |
| 14 | §4b extended: GeoJSON polygon holes explicitly refused | §4b | v0.21.1 adversarial testing, RFC 7946 §3.1.6 |
| 15 | Error table gaps closed: `XOLU-LOC014`/`015`, `ValidationError`'s uncoded class | `loc-01-rest-api.md` error table | Flagged by the implementation's own code comments |
| 16 | `loc-02-implementation.md` Stage 2 extended: dense-key allocation is a second, separate CAS race | Stage 2 | v0.21.1, real `SQLITE_BUSY` failures under load |
| 17 | "Wave placement" struck from §15 — resolved by shipping | §15 | Wave 9 completed 2026-08-01 |

---

## 1. dxp participant count: a factual correction, not a design change

**Before:** §9b and §12 described `loc`'s future registration as "a
fifth entry in an already-concrete, four-entry pattern," with
`dxpParticipantRegistry` holding `bal`, `cal`, `fsm`, `entity`.

**After:** "a sixth entry in an already-concrete, five-entry pattern" —
`bal`, `cal`, `fsm`, `entity`, `ts`.

**Why.** Not a judgement call — the document's own header said it was
checked against v0.19.3, and the codebase had moved to v0.20.0 by the
time of this pass. `T-86` landed a `ts` dxp adapter in that release.
Checked directly against `pkg/server/v2_dxp_def_handlers.go` rather
than trusting the document's own prior claim: `dxpParticipantRegistry`
already had five keys, and `decodeDxpParticipantParams` already had six
concrete `OpParams` types (`bal.TransferParams`,
`cal.CalTransitionParams`, `storage.FsmTransitionParams`,
`storage.EntityUpdateParams`, `storage.EntityAppendParams`,
`timeseries.AppendParams` — enumerated by reading the switch statement,
not assumed from the count of primitives). The lesson generalised
beyond this one fix: a design document that cites live code needs
re-checking every time the code has moved, not just when the document
itself was last edited.

---

## 2–6. Resolved from the original's own §15

The original document's own open-items list named these as unresolved.
Each was closed by direct reasoning against the codebase's established
conventions, not by inventing new ones.

**Subject registry (§3c).** The original's open question: "tracked
subjects limited to existing entities, or does loc need subjects that
aren't entities in the domain sense?" Resolved: entities only. `loc`
was about to invent a subject model of its own when the entity/graph
layer already answers "what is a thing with an identity" — the same
question `bal`'s account tree and (later) `/obj`'s own subject
addressing both declined to re-answer independently. A raw-coordinate
subject with no tree membership is still an entity; it simply has no
current leaf.

**Standalone-fence capacity (§5a) and the §7a consequence.** The
original had no capacity mechanism for fences at all — only leaves.
Resolved by recognising it doesn't need a new mechanism: a
capacity-bearing fence gets exactly §3d's leaf-capacity guard, keyed by
fence id instead of location id. The real consequence was structural,
not cosmetic: because a capacity-bearing fence's admission check is
now guard-bearing, it can't be evaluated only in §7a's original
step (4) (post-commit closure computation) — it has to move into the
same guard-bearing transaction as the leaf check, before commit. §7a
was rewritten to reflect this, not just annotated.

**§7c formal signoff.** The original proposed "no entry/exit
authorisation in v1" but left it as proposed-not-signed-off. Closed on
the same precedent the chronicle substrate already established for
`bal` — a general admission-rule engine was already declined there, on
the grounds that rule content differs per deployment and doesn't belong
in the substrate. No new argument was needed; the existing one already
covered this case.

**Surface pointer (§11).** Once `loc-01-rest-api.md` existed as its own
document with real request/response bodies, §11's original endpoint
sketch became something to supersede explicitly, not leave sitting
next to the real thing looking equally authoritative.

---

## 7. Standalone-fence identity: composes onto an entity

**Before:** §5 described tree-aligned and standalone fences but never
specified how a standalone fence gets an identity at all — not named
as an open item in the original's own §15, because the gap wasn't
noticed at that stage, not because it was considered and deferred.

**After:** a standalone fence's identity is an entity it composes
`loc.fence` capability onto, addressed via the same `(kind, key)`
convention `/obj` and `/meta`'s namespaced subjects use — never a
freely caller-chosen `fence_id` string.

**Why, and where this came from.** This was found and closed during the
adversarial stress-testing pass recorded in
`obj-loc-stress-test-findings.md` — specifically the finding "should
`/loc`/fence compose onto entities the same way?", resolved and
sharpened from a first proposal: not every fence needs a *new*
dedicated place-entity — some compose onto an entity that already has
a position (a service radius self-anchored to HQ's own resolved
position), some need a dedicated position-free place-entity
(Islington, as an administrative neighbourhood, independently
meaningful whether or not anything is ever fenced against it). Both
are legitimate; conflating them would have been wrong. This is also
where §5's cost-of-composition honesty comes from: a genuinely
disposable fence with no independent meaning — a six-week construction
safety perimeter — still needs some entity to compose onto, and the
document says so plainly rather than carving out a quiet exception.

---

## 8. Historical placement for a moving anchor

**Before:** the original had no treatment of this at all. §3b's
placement-chain composition assumed a node's anchor was fixed; nothing
addressed what "where was this on July 15th" means for an anchor that
itself moves.

**After (§5b):** any anchor with a continuous position feed — a ship's
AIS transponder, a tracked convoy — reports into a `/ts` timeline the
same way any other continuous signal does; a historical query composes
the subject's unchanging relative offset with the anchor's
`/ts`-queried position as of the requested date. No versioning
mechanism lives inside `/loc` itself, because the thing that varies
over time was never `/loc`'s to store. A residual is named honestly,
not solved: a *discretely*-repositioned anchor (a site trailer an
administrator relocates twice a year via ordinary `PATCH`, no device,
no continuous signal) still has no historical record of its own past
position, and the fix — `PATCH`'s anchor-update path should append one
journal entry, mirroring §8's existing discipline for subject moves —
is named but not yet built.

**Why.** This is stress-test finding #9 — "mobile location itself
moving (ship, site trailer)" — pursued to resolution rather than left
at first answer. The `/ts` mechanism is the load-bearing insight: it
recognised that `/loc`'s own journal (§8) records *subject* movement
only, never a location node's own anchor changing, so reaching for a
new versioning mechanism inside `/loc` would have been solving a
problem `/ts` already solves, one layer in the wrong place. Directly
connects to this session's §5c: the same "don't invent versioning
machinery when the substrate already has a place for time-varying
values" instinct shows up twice.

---

## 9. Boundary mutation: reconciliation via the rollup pattern

The longest reasoning trail of any change here, including two real
corrections along the way — worth showing the wrong turns, not just
the destination, since an implementer is more likely to make the same
wrong turns without seeing them named.

**The problem, first identified in conversation, not in any prior
document.** A fence's geometry can change. History turns out to be
safe without any addition — a `report`'s journal entry stores the
*computed* crossing fact at write time, never a raw coordinate
re-resolved later — but *current* state isn't: a subject that entered a
fence under its old geometry and hasn't reported since has its
membership, and its contribution to `loc_fence_capacity.count`, go
stale the moment the geometry changes, with nothing to notice or
correct it.

**First wrong turn: a bespoke `/resync` endpoint.** The initial
proposal was a new endpoint that would re-evaluate affected subjects on
demand. Corrected on review: `cal-rest-api.md`'s own R-T1 requirement
— a definitional parameter (a timezone rule) changing after dependent
state already exists against the old value — had already solved the
identically-shaped problem, and its answer was explicitly *not* a
special fix-up endpoint: the caller detects the change and re-issues
the correction through the primitive's own *ordinary* write path
(`move`, for `cal`). Building a new endpoint would have duplicated
machinery that already existed one document over.

**Second wrong turn: geometry-version tags on every capacity
increment.** Applying R-T1's shape faithfully still left a gap R-T1's
own caller doesn't have: `cal`'s caller is self-sufficient (it already
retains `local_time + zone_id`, so it already knows which of its own
bookings are affected). `/loc`'s hypothetical corrector isn't — the
membership bookkeeping lives inside `/loc`'s own tables, which had no
reverse index at all. The fix proposed was per-increment version
tagging so staleness could be detected incrementally.

**The correction that actually landed.** This turned out to be solving
a problem `bal`'s rollup discipline already solves, under a different
name, and version-tagging was unneeded machinery on top of a simpler,
already-proven mechanism. Checked directly against `pkg/bal/rollup.go`
and `pkg/bal/store.go`: a transfer's authoritative commit happens
first; the rollup plane folds the change into the cascade *after*,
best-effort; a crash or error there leaves the rollup stale, which
`RebuildRollup` repairs *from the journal* — a full recompute from
source, not incremental patch-tracking — and no guard ever reads the
rollup, so staleness there is a performance/observability matter, never
a correctness one. Applied to `/loc`: a reconcile function, triggered
best-effort after a geometry `PATCH` commits, uses §6b's existing
bounding-box pre-filter to find candidate subjects (no new indexing),
re-tests each against the fence's *current* geometry, and produces a
fresh, **advisory** view — never writing `loc_fence_capacity.count`
directly, since that table is guard-bearing and `bal`'s own rollup
bucket explicitly isn't; letting a derived process write into
guard-bearing state would be the exact violation "no guard reads the
rollup" exists to forbid, run in reverse. Drift surfaces the way
`rollupDegraded`/`onRollupError` surfaces a lagging rollup; the actual
fix, when needed, is an ordinary `report` call through the same CAS
path every other capacity change already uses — R-T1's original
answer, restored, now with the caller no longer having to guess who's
affected.

**Why this belongs in the addendum rather than just the design text.**
The final version (§5c) is intentionally terse about the two wrong
turns — a design document states the answer, not the search for it.
An implementer benefits from knowing the search happened: the instinct
to reach for new machinery (a resync endpoint, then version tags) was
strong both times, and both times the correct move was recognising an
already-proven pattern elsewhere in the codebase rather than building
forward from the specific problem in front of us.

---

## 10. Fence-type patterns

**What.** Many fences of a shared kind — every loading-dock zone across
every warehouse — carry the same capacity default. §5d gives this a
`loc_patterns` definitional record (not a fence, not an entity, not
addressed via any subject convention — the same category as
`fsm_definitions`), with fences cloned from it at creation and never
retroactively affected by a later pattern change.

**Terminology note, added 2026-08-02 — this entry's own narrative below
still says "template" throughout, deliberately, because that's what it
genuinely was called at the point this entry describes.** A later
session found "template" itself was the wrong noun — a forced
metaphor once a definition could be drafted *from* an existing subject
rather than only authored in advance — and renamed the mechanism
"pattern" everywhere in `loc-00-design.md`/`obj-00-design.md` and both
API documents, adding `extract` (draft a pattern from a live subject)
and `pattern_after` (a one-shot, non-persisting form) alongside it.
Rewriting this entry's own history to say "pattern" throughout would
misrepresent when that correction actually happened; the "What." line
above uses the current name since it's describing the live mechanism,
not the history.

**Why, and the correction inside this one too.** The pattern was
initially discussed using "class and instance" language, prompted by a
direct question: does `/loc` already do this kind of thing? It does —
verified directly against `pkg/server/v2_fsm_machine_handlers.go` and
`v2_fsm_walk.go` (a machine snapshots its definition's spec at
creation; every transition reads the machine's own copy, never a live
re-fetch) and `pkg/server/v2_dxp_def_handlers.go` line 450 (`dxpTxnSnapshot
is the fully-resolved def cloned into dxp_txn.snapshot_json`) — the
same mechanism, independently built twice already. But "class and
instance" was corrected, twice, before the terminology settled:
first to **prototype-based cloning** (Self/Io sense — an independent
copy taken once, with no live behavioural link back to its source,
which is what `fsm` and `dxp` actually do; a class-based reading
implies exactly the live coupling neither has), then to **cloned
child** specifically (pairing correctly with "lineage," which the
mechanism already needed a word for — each clone retains a pointer
back to its source and live tracking of whether that source still
exists, going beyond what either classical model provides on its own).
Also worth naming: detecting drift between a cloned fence and its
current template needs no version-tagging either, for the identical
reason §5c's reconciliation doesn't — a plain recompute-and-compare,
the same shape `RebuildRollup` already uses.

**External grounding, not just internal precedent.** Maximo's Item
Assembly Structure (IAS) was researched directly as the nearest
industry analogue — a Bill of Materials, applied as a template to stamp
out matching asset or spare-parts structures. Useful for confirming the
general shape has real demand, and useful for what *not* to copy: IAS's
apply action creates child asset records — the structure's contents get
stamped into existence on apply. §5d's template governs the capacity
profile only, never contents, because contents are exactly the
high-frequency, ordinary-operation fact `/obj`'s own containment design
already distinguishes from administrative structure — copying IAS's
content-stamping behaviour would have reintroduced that exact
distinction's failure mode through the back door of "but this part is
just templating." Also useful, less directly: IAS is copy-on-apply with
no lineage tracking and no signal for "has the source template changed
since" visible anywhere in the material reviewed — real practitioner
accounts describe Maximo's related decommissioned-asset handling as
clunky in production for a structurally similar reason (a workaround
standing in for a fact the system doesn't track natively). `/loc`'s
version tracks both from the start, for the same reason `fsm` and
`dxp` already do.

The bigger case this doesn't attempt — a whole location-and-fence
*subtree*, defined in relative terms, applied at a new anchor to
bootstrap many similar sites at once, closer to IAS's own recursive
"apply" action in shape — is named as a real, deferred item in §15,
not designed here. §5d is deliberately the flat, single-record version
only.

---

## 11. Standalone-fence identity: the design held, v1 shipped narrower

**Before (this document's own entry #7):** resolved — a standalone
fence composes onto an entity via `(kind, key)`, not a caller-chosen
`fence_id`.

**After v0.21.0, checked directly against `pkg/loc/store.go` and
`pkg/server/v2_loc_handlers.go`:** shipped as a bare, caller-chosen
`fence_id` after all — `DefFence(ctx, fenceID string, alignedLocationID
*string)` takes a plain string, no entity resolution. Subtler than a
plain reversion, worth stating precisely: the wire shape didn't
regress. `POST /loc/fences/attach` still accepts a `subject` field,
`GET`/`DELETE` still route through `/loc/fences/{kind}/{key}` — but
`handleLocFenceAttach` does `fenceID := req.Subject` and stops, and
`kind` is parsed by the router and never read by either handler
afterward. The field names and the URL shape survived; the resolution
semantics they were built to carry did not.

**Why this is a scope narrowing, not a wrong turn.** `RESOLVED.md`'s
own T-115 closure record states it as a conscious decision, not a
discovered gap: *"fences — bare identity — real geometry is Stage
3."* `CHANGELOG.md`'s v0.21.0 entry names it explicitly as one of two
v1 scope narrowings, citing the real dependency — entity resolution
needs `/meta` wiring this wave didn't include. This is the same shape
of judgement call `cal`'s own implementation made repeatedly (`cal-
rest-api.md`'s reconciliation banner lists several endpoints and the
full `Mode`/`Capacity` vocabulary that didn't ship either) — building
the narrower, immediately-achievable thing and naming the gap plainly,
rather than either blocking wave 9 on `/meta` work or silently
building the entity model halfway.

**What this means for anyone reading `loc-00-design.md` §5 or
`loc-01-rest-api.md` §2 today.** Both documents describe the
entity-composition model as *the* design, correctly — that reasoning,
from the stress-testing pass, hasn't changed and shouldn't be
rewritten to match what shipped. Both now carry an inline
reconciliation note at the point of the claim, marking what's
specified versus what a caller actually gets from v1, rather than
either silently drifting out of sync with the code or being rewritten
to describe only the narrower, currently-true thing.

## 12. dxp storage separation: a real gap in §9b's own account

**Before:** §9b described `loc` joining `dxpParticipantRegistry` as "a
sixth entry... sharing the tenant's `dxp.MemCache` the same way the
existing five do." True, and incomplete — the sentence describes the
*registry*, not the *storage* the coordinator dispatches through.

**After, checked against `pkg/server/v2_dxp_dispatch.go`:**
`dispatchDxpTxnCore` needed a third database-handle parameter
(`locDB *sql.DB`), separate from the shared `db` the four
same-database primitives use and separate from `ts`'s own `pebbleDB`.

**Why this was missed, and why it's a useful lesson beyond this one
document.** `loc-02-implementation.md`'s own Stage 0 correctly decided
`loc` needed its own dedicated SQLite file, for storage-layout
reasons, and that decision was right. What didn't get traced through
was the *consequence* of that decision for the dxp coordinator's own
dispatch signature — "loc has its own storage" and "the dxp
coordinator needs to know that when dispatching a mixed transaction"
are two separate facts, and only the first got written down. The gap
was invisible until the first real end-to-end dispatch actually tried
to use loc's storage through the path built for the shared four,
surfacing as `"no such table: loc_capacity"` — a wrong-database error,
not a missing-table error, dressed up identically. The fix reused
`ts`'s own precedent exactly (a distinct handle threaded through
dispatch) rather than treating this as a novel problem, which is
itself worth noting: the second time a new primitive brings its own
storage to `dxp`, this shouldn't be a surprise at all, and both
`loc-00-design.md` §9b and `loc-02-implementation.md` Stage 5 now say
so directly for whoever builds the third.

## 13–14. Two numerics/geometry hazards, found by adversarial testing and fuzzing

**§4e, near-pole division.** Not a correction — an extension of the
existing "watch-item, not a blocker" language with a specific,
concrete case that language didn't cover. `math.Cos(90°)` is
`6.12e-17` in float64, not exactly `0.0`; an exact-zero guard on the
denominator in the local-offset-to-longitude conversion misses the
near-pole singularity entirely, producing a "valid" longitude delta of
roughly 10¹⁴ degrees for an ordinary 1km offset rather than an error.
Worth preserving the *fix shape* precisely, not just the bug, because
the fix was wrong once before it was right: an epsilon guard on the
denominator doesn't bound the result for arbitrary offsets (the
violation condition scales with the offset itself), caught by
`go test -fuzz` finding a concrete counter-example within seconds of
the fuzz target existing. The correct fix clamps the division's
*result* to ±180°, not the input.

**§4b, GeoJSON polygons with holes.** Also an extension, not a
correction — §4b's own "simple polygons" restriction was always about
self-intersection, and holes (RFC 7946 §3.1.6's interior rings) are a
separate, valid structure the restriction never addressed either way.
An earlier implementation took only the first ring and silently
dropped the rest; fixed to refuse outright, on the grounds that a
silently-dropped hole reads as "inside the fence" when the submitter's
intent was the opposite — worse than an explicit refusal explaining
why.

**Why both are grouped here rather than given their own numbered
entries:** neither changed a design decision. Both are cases where
adversarial testing found a specific instance of something the design
already gestured at generally (§4e's watch-item, §4b's restriction to
simple shapes) and the addendum's job is recording where the general
language now has a concrete, named case attached to it.

## 15. Error-code table gaps, found and named by the implementation itself

Not discovered by review — `pkg/loc/errors.go`'s own comments state
both gaps directly. `XOLU-LOC014`/`015` (duplicate `location_id`/
`fence_id`, 409) had no corresponding table entry: *"Found by
adversarial testing, not written to spec... flagged as a likely
systemic gap rather than silently fixed in isolation"* — and it
wasn't isolated, since `bal.DefineAccount` had the identical missing
code for the identical reason, found and fixed the same session.
`ValidationError`, a generic 400 for malformed GeoJSON, unsupported
geometry types, and empty required fields, has no reserved code at
all: *"a real gap in the table, not glossed over."* Both are now in
`loc-01-rest-api.md`'s table. Worth noting as a general pattern for
future `def`-shaped endpoints across any primitive: a duplicate-key
refusal is easy to omit from a first-pass error table because it's not
a *guard* refusal in the capacity-check sense — it's a plain uniqueness
constraint — and both `loc` and `bal` independently made the identical
omission for the identical reason.

## 16. Dense-key allocation: a second CAS race, distinct from capacity

**Before:** `loc-02-implementation.md` Stage 2 documented one CAS
pattern in detail — the capacity guard, keyed by leaf or fence,
`bal`'s §6 pattern applied to admission.

**After:** a second, separate write path had the identical race class
and wasn't named as a risk anywhere in the plan. `Def`/`DefFence`'s
own dense-key allocation — assigning the internal surrogate key a new
location or fence gets — used a read-first pattern (`SELECT MAX(key)+1`
as its own statement before the `INSERT`), and produced real
`SQLITE_BUSY` failures under concurrent load: 9 out of 30 concurrent
`Def` calls failed on the first adversarial test run. Fixed the same
way `Move` already was — write-first, `INSERT...SELECT...RETURNING` —
and `loc-02-implementation.md` Stage 2 now states the general rule
this specific miss implies: the CAS discipline applies to *every*
write path that reads a value and then writes based on it, not only
the one path a plan happened to call out by name. Capacity admission
got the warning because it was the obviously risky part; key
allocation needed the identical fix without ever looking risky enough
to earn one in advance.

## 17. "Wave placement" resolved — struck from §15

The original document's own open item — no decision existed on
whether `loc` would get its own development wave or fold into an
existing one. Resolved by simply happening: wave 9, created and
closed the same day, item #7 (`/meta` subject-addressing
generalisation) never actually serving as the anchor it was floated
as a candidate for. Struck from §15 rather than left as a resolved
item needing its own explanation — this one closed by execution, not
by a design decision worth narrating.

---

## What this doesn't cover

**Original scope, still accurate:** `/obj`'s own design changes aren't
covered here — see this document's own opening note.

**Updated 2026-08-02 — corrected a second time, since the previous
correction is now also out of date.** The 2026-08-01 update above said
§5c/§5d's wire consequences "correctly still aren't applied to either
document," on the stated principle that a design addition not yet
built in shipped code stays named and deferred rather than specified
on the wire. That principle has since been superseded, deliberately,
not by accident: `loc-01-rest-api.md` now specifies both `§5c`'s
`PATCH`/`reconcile` endpoints and `§5d`'s pattern mechanism in full,
`obj-01-rest-api.md` now specifies `§7a`'s patterns likewise — not
because either mechanism shipped, but because a *worked-examples*
document (`obj-loc-worked-examples.md`) had already started using
`pattern_after` and `.../reconcile` as if they had a spec to point to,
and they didn't. That's a sharper problem than "not yet built": an
orphaned reference in one document to a wire shape no other document
defines. The corrected principle: **a design mechanism gets its wire
surface specified the moment anything else in this document series
assumes that surface exists, regardless of shipped-code status** — the
shipped-vs-designed distinction still matters and is still stated
plainly wherever it applies (`loc-00-design.md` §15's own accounting
is unchanged by any of this), but it no longer gates whether the wire
shape itself gets written down.
