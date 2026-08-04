# `obj` — staged implementation plan

`obj-02` of the obj document series: see `obj-00-design.md` for the
model and doctrine, `obj-01-rest-api.md` for the wire surface this
plan builds handlers for. Prepared: 2026-07-31, against v0.20.0.

> Unlike `loc-02-implementation.md`, this plan does **not** claim every
> gate is closed. `obj-00-design.md` §13 names four genuinely open
> questions (named attachment slots, non-versioned placement,
> `loc`+`cal` composability, the application-policy guarantee gap), and
> this plan does not resolve any of them — it sequences the stages that
> don't depend on their resolution, and flags plainly, at Stage 3 and
> Stage 8, exactly where an unresolved question could still change the
> shape of what's already built. Treat the presence of open items here,
> where `loc-02` had none, as accurate rather than as this plan being
> less finished — `obj`'s own design genuinely settled fewer things
> before implementation planning began, and pretending otherwise would
> just move the correction to a worse time to find it.

## Principles

- **Each stage ends green** — unchanged from `cal`'s and `loc`'s own
  discipline.
- **Mirror `bal`, not `loc`.** `obj` has no geometry of its own
  (`obj-00-design.md` §2) — position is always resolved through
  something else (§6). Package structure follows `pkg/bal`'s file
  layout for the same reason `loc` did (`model.go`, `store.go`,
  `dxp_adapter.go`, `verify.go`), plus `containment.go` (the cycle-
  safety guard, `pkg/bal` has no equivalent) and `graph_mirror.go`
  (Stage 5, no equivalent in either `bal` or `loc`).
- **The subject is always resolved, never stored redundantly.**
  `obj`'s own tables never duplicate entity data — every row is keyed
  by `(kind, key)` and everything else about the subject stays where
  it already lives, in the entity itself. This is the one discipline
  every stage below has to hold, and it's the thing an implementation
  is most likely to quietly violate under schedule pressure (caching
  entity fields "for convenience" on an `obj` row) — worth stating as
  a standing rule, not just an initial design choice.
- **Cycle safety is universal from Stage 2 on, never opt-in.**
  `obj-00-design.md` §5 rejected a flag-based ("only portable nodes
  need checking") alternative explicitly; this plan does not
  reintroduce it as a shortcut under schedule pressure either.

---

## Stage 0 — Package skeleton and decision capture

**Goal:** `pkg/obj` exists, compiles, pins the settled inputs as code-
level constants and doc comments: the `(kind, key)` subject format
(reusing `pkg/storage/meta_subject.go`'s validator, not reimplementing
it), the three position-termination kinds (§6), and multi-dimensional
capacity as the default guard shape from day one — not retrofitted
after a count-only version ships, which was `loc`'s own path and is
deliberately not repeated here since the multi-dimensional need is
already known up front.

**Exit:** `go build ./pkg/obj/...` green, zero behavior.

---

## Stage 1 — Attach, detach, and position without containment

**Goal:** `obj.Attach`/`obj.Detach` (`obj-01-rest-api.md` §1),
`Move` restricted to the two non-containment target kinds
(`loc_leaf`, `null`), and `Report` (fence resolution only, mirroring
`loc`'s own `report` handling directly — no new logic, just routing
through it). No `obj`-to-`obj` containment yet; that's Stage 2.

**Why containment is deliberately excluded here:** this stage exists
specifically to prove position resolution's two simpler termination
cases end-to-end — attach, position, resolve, detach — against real
entity data, before the genuinely novel risk (cycle safety, Stage 2)
enters the picture at all. Mirrors `loc-02`'s own Stage 1/Stage 2
split (tree+placement first, guards second) for the identical reason.

**Exit:** full round trip (attach → move to a `loc_leaf` → resolve →
detach) green under `-race`. No test in this stage exercises
concurrent writes to the same subject — that's Stage 2's job, on the
stage where it actually matters.

---

## Stage 2 — Containment, cycle safety, capacity (highest risk)

**Goal:** `obj`-to-`obj` `Move` targets, the containment edge table,
the universal cycle-safety guard, and multi-dimensional capacity CAS —
all guard-bearing, all in one transaction per move.

**This is the one stage in the whole plan that gets the same caution
`loc-02`'s own Stage 2 got, and for a related but sharper reason.**
`loc`'s risk was multi-target atomicity (guarding a leaf and N fences
in one CAS sequence) — real, but each individual guard was still a
simple count comparison. `obj`'s Stage 2 combines that same multi-
target shape with a genuinely more complex individual guard (weight
*and* volume simultaneously, §7 of `obj-00-design.md`) *and* a
traversal-shaped check (cycle safety) that has no analog anywhere else
in this codebase's guard-bearing write paths. Design-then-race, not
TDD, the same instruction `cal`'s hardest stage got.

**The cycle-safety extraction task, concretely:** `pkg/graph`'s
`FlatGraph.wouldCreateCycle` (bounded BFS, conservative on budget
exhaustion) reads `g.nodes` directly and cannot be called as-is
against `obj`'s own transaction-scoped rows (`obj-00-design.md` §5).
The task is a small, deliberate refactor of that algorithm — not of
`obj`'s own code, which doesn't exist yet — into a form parameterized
over a neighbor-lookup function, landing either as a shared internal
helper both `pkg/graph` and `pkg/obj` import, or duplicated with
attribution if a shared package isn't worth the coupling. Decide which
before writing `obj`'s own guard, not after — a duplicated-then-
diverged cycle check is worse than either clean option.

**Test shape, matching the risk:** a stress harness analogous to
`bal`'s own `admission_race_stress_test.go` (G-13) — concurrent moves
attempting to construct a cycle from multiple directions at once, plus
concurrent moves competing for the same capacity ceiling on both
dimensions independently. Sandbox pass is not confirmation; this joins
`G-13` and `G-01`/`G-11` in the dormant-guards table (`docs/
KNOWN_ISSUES.md`) as soon as it's written, needing the same multi-core
re-run before it's trusted.

**Exit:** the full stress harness passes locally (sandbox-bounded,
flagged as such), and a fresh dormant-guard entry exists before this
stage is called done — not after, matching the house rule that a
guard and its registration are one act.

---

## Stage 3 — Promote / demote

**Goal:** `obj-01-rest-api.md` §5's atomic transition — `bal`
decrement, entity create-or-reuse, `obj` attach or detach, one commit,
dispatched via `dxp`.

**Where an open question could still bite this stage, named plainly:**
`obj-00-design.md` §13 does not resolve whether attachment (a
prosthetic checked out to a body) needs its own edge shape distinct
from containment. Promote's own `entity.create` path (§5 of `obj-01`)
assumes ordinary containment semantics for wherever the newly-created
entity gets positioned. If attachment turns out to need a different
edge shape, promote's own request schema may need a third `position`
kind alongside `loc_leaf`/`obj` — flagged here so it's not a surprise
if that question resolves before this stage is built, rather than
treated as this stage's own scope creep if it does.

**Exit (corrected 2026-08-02 against what T-121 actually built and
verified — see `obj-00-design.md` §9's own correction):** a full
promote↔demote round trip, green under `-race`, proving atomicity
holds wherever dxp actually guarantees it for this composition — the
collapsed path, and any refusal caught during Reserve/Validate before
Execute runs. `/obj` and `bal` are separate SQL engines, so this
composition always dispatches phased; a refusal discovered only
during Execute can leave one leg committed while another is refused
(dxp's own "expired" outcome, `v2_dxp_dispatch.go`'s own documented,
accepted risk of the phased path generally). The original "never a
window where it's both or neither" phrasing overstated this — proven
false directly by the built end-to-end tests, not merely relaxed as a
precaution.

---

## Stage 4 — Journal and verification

**Goal:** the movement journal (every position/containment change,
immutable, append-only) and the rebuild oracle: `derive(journal) ==
current`, `obj`'s own version of `loc`'s spine invariant
(`loc-02-implementation.md`'s own Principles section) and `cal`'s
`index == rebuild`.

**Exit:** `iolu obj rebuild-check` (or wherever this lands in the
`iolu` command surface — cross-reference `iolu-operations-roadmap.md`,
wave 6, still 0% built as of this writing, so this exit criterion may
need to land as a package-internal check rather than a shipped `iolu`
subcommand until that wave catches up) confirms agreement on a
non-trivial fixture (attach, several moves including at least one
promote/demote cycle, one retire).

---

## Stage 5 — Graph mirroring

**Goal:** after each Stage 2 containment write commits, best-effort
`AddEdge` into the live graph between the two subjects' entity nodes —
the exact shape already built and proven this session for
`bal.Adapter.PostCommit`/`EmitDeltas` (commit first, authoritative;
mirror second, best-effort, degrade via a logged warning rather than
failing the write if the mirror step itself has trouble).

**Why this is its own stage, not folded into Stage 2:** Stage 2's own
guard must never depend on the graph (`obj-00-design.md` §10,
guard-locality) — keeping the mirror as a strictly later, separable
stage is a structural reminder of that boundary, not just a scheduling
convenience. A future maintainer reading the stage list should not be
able to accidentally fold mirroring into the guard transaction just
because they're adjacent in the code.

**Exit:** `FindPath`/`GetNeighbors` against the live graph correctly
answers containment-closure queries for a fixture built entirely
through Stage 2's own API — proving the mirror, not just that
`AddEdge` was called.

---

## Stage 6 — Events and dxp participant

**Goal:** the event feed (`obj-01-rest-api.md` §7) and `obj` as a
`dxp.Participant` — `Reserve`, `Validate`, `Execute`, `Release`,
`PostCommit`, the same five-verb shape every other primitive
implements, `PostCommit` folding the graph mirror (Stage 5) the same
way `bal`'s own folds its rollup.

**Exit (adjusted 2026-08-02, see `obj-00-design.md` §10's own note):**
not just a unit test — an HTTP-level proof through a real `dxp`
dispatch on the phased path (`bal`'s own precedent, this session, for
the shape of the proof: `TestDxpTxnAPI_PostCommit_
BalRollupReflectsCommittedTransfer`). "Both collapsed and phased"
(this stage's own original wording) is not achievable for `/obj`
specifically, and not a gap: `dxpEngineOf` tags `/obj` `"sql-obj"`
(its own dedicated per-tenant SQLite file, T-119), and
`EngineHomogeneous` checks for the literal string `"sql"` per
participant — any transaction touching `/obj` always forces phased,
collapsed is structurally unreachable. Written before `/loc` (and its
own identical `"sql-loc"` precedent) had actually been built and this
consequence understood — corrected here rather than chased as an
impossible target. Additionally, given
`obj`'s own T-109-shaped risk (two `obj` participants of the same
primitive colliding in one instance — two containment moves in one
`dxp` transaction), an adversarial test constructing exactly that case
explicitly, not left to "at least one transaction" to happen to cover
it — the gap this plan's own predecessor conversation identified as
missing from an earlier draft.

---

## Stage 7 — API surface and lifecycle completion

**Goal:** every remaining `obj-01-rest-api.md` endpoint (`capacity`,
`contents`, `retire`), full error-code coverage, and the tree-wide
completion sweep (§7.8-shaped: grep for every `XOLU-OBJ` code named in
`obj-01` and confirm each is actually reachable, not just documented).

**Exit:** full `obj-01-rest-api.md` surface green, including `retire`
refusing correctly when contents are non-empty (`XOLU-OBJ012`), and a
documentation accuracy pass — does `obj-01-rest-api.md` still match
what got built, or does it need the reconciliation banner its own
header already anticipates.

---

## What this plan does not attempt to resolve

Restated from the preamble, because a staged plan can make open
questions easy to lose track of once individual stages start looking
concrete: named attachment slots, non-versioned placement, `loc`+`cal`
composability, and the guard-strength gap for application-enforced
policy are not scheduled anywhere above. They remain
`obj-00-design.md` §13's open items, unchanged by anything in this
plan having been built.
