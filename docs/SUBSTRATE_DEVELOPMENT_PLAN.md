# Substrate Development Plan — debt closure and the primitive programme

Updated: 2026-07-19
Kind: Plan (per tracking practices: frozen once execution starts;
deviations are recorded in the register, never edited into this
document).
Status: DRAFT — becomes frozen when the first wave begins.

Scope: everything currently open — the register's technical debt
(docs/TRACKING.md) and the full proposal programme
(docs/proposals/) — sequenced into executable waves with effort,
dependencies, and exit criteria. This document references the register
and the proposals; it does not duplicate them. Where an estimate here
disagrees with a proposal's, this document's is the later and governs.

Effort figures are **ideal focused days** for one implementer who knows
the codebase. Calendar reality for a solo founder with interleaved
duties runs 1.5–2× ideal; totals below state both.

## 0. Document codes

Cross-references throughout the programme use single-letter,
zero-padded codes for grep and scan:

| Code | Document |
|---|---|
| B | `docs/proposals/bal-conservation-primitive.md` |
| C | `docs/proposals/chronicle-substrate.md` |
| D | `docs/proposals/dxp-composed-commitment.md` |
| I | `docs/proposals/iolu-operations-roadmap.md` |
| P | this document (`SUBSTRATE_DEVELOPMENT_PLAN.md`) |
| R | `docs/proposals/referential-integrity.md` |
| S | `docs/proposals/system-bookkeeping-scope.md` |

Reference form: `@B03a`, `@C04c`, `@D05b`, `@R02.2`, `@P00`. The
`@` sigil marks the token as a citation, preventing collision with
register IDs, ADR numbers, and other short alphanumeric codes elsewhere
in the ecosystem. The section number takes each document's own
numbering unchanged; the letter prefixes it.

## 0a. Governing references

Every wave operates under these, cited by their canonical homes rather
than repeated here:

- **Working agreement** (userPreferences, sessions): batching (§2),
  guarded edits with `&&`-chained dependents (§6.2), tree-wide
  completion sweeps (§7.8), documentation voice (repo-bound vs
  ledger-bound), and §3.1 (verify against artefacts on any challenge).
- **Tracking practices** (part 3): closure procedure (§3), header
  discipline (§4), release gate (§6), dormant guards (§8 — critical:
  every stress-tagged, tag-gated, or hardware-gated test is registered
  in the dormant-guards table in the same session it is written).
- **Substrate laws** (`docs/proposals/chronicle-substrate.md`): §4a
  guard locality, §4b finiteness, §4c meta is engine-inert. Every
  guard, every retention policy, every annotation-vs-config choice
  answers to these.
- **Composability locality** (`docs/proposals/dxp-composed-commitment.md`
  §6): guard-bearing primitives share one SQLite file per tenant;
  future work must not scatter them.

## 1. Principles

1. **Every wave ends green and releasable**: full suite, lint, gate,
   multi-core CI, a version tag, and register closures with
   verification records. No wave leaves the tree between states.
2. **Dormant guards are registered in the session they are written**
   (tracking practices §8) — several waves create race harnesses; the
   dormant-guards table (T-36, wave 0) must exist before they arrive.
3. **Demand gates hold.** Deferred items (§6) build when a named
   consumer exists, not before. The plan closes the *committed* gaps;
   it does not commit the optional ones.
4. **Per-tenant per-primitive ID widening precedes any production
   deployment.** `/ts`'s `TimelineID` is `uint16` (0xFFFF cap) and packed
   as a 2-byte big-endian prefix in every Pebble key. `/cal` and
   (future) `/bal` internal IDs need the same audit before ship. This
   is the actual ceiling mid-market SMB workloads hit
   (retail SKUs, hotel rooms, hospital patients within one tenant reach
   65k long before a machine reaches its tenant count). Wave 1's
   position is fixed because on-disk key formats must not change after
   production data exists.

   Cross-tenant scaling is *not* here: tenant ID stays `uint16`. A
   production server hits its throughput or storage ceiling long before
   65k tenants, and the answer if it ever did would be a second xolu,
   not a wider ID.

## 2. Inventory

| # | Workstream | Source | Effort (ideal days) | Depends on | Wave |
|---|---|---|---|---|---|
| 1 | Git retrofit of the checkpoint | T-37 | 0.5 | — | 0 |
| 2 | Dormant-guards table | T-36 | 1 | — | 0 |
| 3 | Release-hygiene script + pin-coherence check | T-22 | 1 | — | 0 |
| 4 | Move-window race investigation on multi-core CI | T-35 | 1 | CI (done) | 0 |
| 5 | Cascade-delete minimum fix | T-41; @R02.2, §8 stage 1 | 0.5 | — | 0 |
| 6 | Trusted-proxy client-IP extraction | T-38 | 0.5 | — | 0 |
| 7 | /meta subject-addressing generalisation | @B03a; @C04c (meta engine-inert) | 1 | — | 0 |
| 8 | Tenant ID → uint32 (218 sites, 2 codec sites, dir format, fixtures) | @S02; working agreement §7 (mass substitution) | 3 | — | 1 |
| 9 | Sysmask: immutable uint8 width partitioning the 32-bit primitive ID space (default 0 = all user); `IsSystem` predicate; two-allocation-path enforcement; metadata storage; `iolu db status` display; transvasing designed-in (built wave 6) | @S (rewritten 2026-07-20, width-model) | 2 | #8 | 1 |
| 10 | RI stages 2–5: x-ref schema, restrict, cascade/nullify, write validation, client | @R02 (schema), §3 (engine), §5 (concurrency), §5a (kernel fence for PG), §7 (tests), §8 (staging) | 6 | #5 | 2 |
| 11 | Chronicle extraction: monoid cascade engine | @C03 (monoid theorem), §4 (extraction inventory), §5 (sequencing: extract with bal, cal opportunistic) | 3 | — | 3 |
| 12 | Chronicle extraction: Sealer lift from cal | @C04 (extraction #2); cal-pebble-codec.md (existing Sealer); working agreement §5 (serialisation discipline) | 2 | — | 3 |
| 13 | Chronicle extraction: rebuild-oracle harness | @C04 (extraction #3); iolu roadmap item 5 (harness feeds `iolu db check`) | 1 | — | 3 |
| 14 | bal core | @B03 (model + chain triple), §3a (hierarchy, XOLU-BAL005), §4 (numerics + float-smuggle test), §5 (two-plane storage, guard coupling), §6 (CAS, T-34 recap and race harness), §9 (memo on transfer), §10 (errors), §11a (comparison); @C04a (guard locality) | 4 | #11–13, #2 | 4 |
| 15 | bal rollups over ts: deltas, checkpoints, as-of | @B05 (rollup plane), §8 (dual verifiers) | 2 | #11, #14 | 4 |
| 16 | bal seal/close + prefix-collapse retention | @B05 (checkpoints), §7 (close); @C04b (finiteness law) | 1 | #12, #14 | 4 |
| 17 | bal client, integration suite, molu tools | @B09 (surface); working agreement §9.1 (naming: `bal`, not `BAL`) | 1 | #14–16 | 4 |
| 18 | Reserved-commit facility: tentative rows, weights, visibility tiers, tentative edges, sweeper | @D05b (lifecycle + weights), §5c (visibility tiers + fsm), §7 (recovery), §7a (clock placement); @C04 (refined) (lifecycle factorable, predicates not) | 5 | #10 (edge table), #2 | 5 |
| 19 | Participant adapters: cal, bal, entity, fsm | @D05 (contract), §5c (fsm resolution via `SetStateFrom`) | 3 | #18, #14 | 5 |
| 20 | dxp/def: registration, validation, versioning, static analysis | @D03 (def structure), §4 (method as configuration), §12 (no control flow) | 2 | — | 5 |
| 21 | dxp/txn coordinator: **3PS as the sole execution model for v1** (2PS and the wider phase-spectrum beyond 3PS deferred — pending, not a hard requirement; §6 Deferred names the trigger), degradation collapse to ACID, sweep, harnesses, across all four substrates | **dxp-coordinator-design.md** (full design: ParticipantStore, attendance, no-durability decision, T-85); @D04 (patterns), §6 (degradation to ACID + equivalence test), §7 (recovery), §8a (four fsm relations), §10 (errors), §11 (test obligations) | 5 | #18–20, #38 | 5 |
| 22 | ~~3PS phase machine~~ — **RETIRED 2026-07-29, merged into item 21** (3PS is no longer a second phase-count model layered on a 2PS coordinator; it is item 21's only model) | — | — | — | 5 |
| 23 | dxp client + def-as-tool surface for molu | dxp §9 | 1 | #21 | 5 |
| 38 | Entity CREATE as a dxp op (EntityAdapter currently UPDATE-only; the proposal's own §3 worked example assumes CREATE exists) | dxp-composed-commitment.md §3 (worked example uses entity/create); item 19's own EntityAdapter doc ("CREATE... is future work, not folded in here") | 1.5 | #19 | 5 |
| 39 | Cross-substrate invalidation: the dxp.Participant post-commit verb (T-57) — derived planes (bal rollup, cal H3 index) must ingest at confirm only, per §5c tier 3. **Not an item-21 prerequisite (2026-07-29, reconsidered — neither plane is guard-bearing, the exit gate doesn't need it)**, real and open, lower priority | dxp-composed-commitment.md §5c (three-tier visibility taxonomy, tier 3: "commit-fed, strictly"); T-57; T-83 | 2 | #19 | 5 |
| 40 | `ts` dxp adapter (T-86): restores `ts` to the hotel gate's own participant list (§5a's original worked example — dropped without comment somewhere in this session's own narrative, corrected 2026-07-29). The first real Pebble-backed participant. `@D06`'s own stated condition is single-tenancy, not engine type -- but a Pebble store mechanically cannot join "a single SQL transaction" regardless, so its presence is what makes the gate's "phased path" a genuine cross-engine proof rather than an all-SQL stand-in (see dxp-coordinator-design.md for the corrected attribution) | dxp-composed-commitment.md §5a (original worked example); T-86 (adapter investigated: near-trivial Reserve/Validate, one real complication — the write-coalescer bypass — found and resolved as safe) | 2 | #18 | 5 |
| 24 | iolu: cal provisioning | iolu roadmap item 1; backend coupling section (logical-core side, Store interface) | 1 | — | 6 |
| 25 | iolu: query + REPL | iolu roadmap item 3 (surface + iaul template); target-directory resolution policy (`IOLU_BASE_DIR`, upward discovery); backend coupling (logical-core via executor) | 3 | — | 6 |
| 26 | iolu: db check (promote oracles) | iolu roadmap item 5 | 1 | #13 | 6 |
| 27 | iolu: reindex-fts | iolu roadmap item 2 | 1 | — | 6 |
| 28 | iolu: backup + verify | iolu roadmap item 4 | 2 | — | 6 |
| 29 | iolu: rebuild-graph | iolu roadmap item 6 | 0.5 | — | 6 |
| 30 | Event model part 2 | T-07 through T-13 (re-triage at wave entry); @D05c tier 3 (events are commit-fed) | 5 (provisional) | — | 7 |
| 31 | System stores content: ~metering (ts) and ~billing (bal) | @S03 (residents); tilde-sigil convention; #17 (bal molu surface) | 5 | #9, #17 | 7 |
| 32 | OQL primitive-query dispatch: recognise FunctionCall/MethodCallExpression FROM/WHERE items, route to a provider interface | oql-primitive-queries.md (syntax validated against tsqlparser directly, zero parser changes needed); PushNone (existing Go-side eval path) is the architectural fit | 2 | — | 8 |
| 33 | `@FSM()` query provider | oql-primitive-queries.md §sequencing (easiest: fsm is fully SQL-backed already) | 2 | #32 | 8 |
| 34 | `@BAL()` query provider | oql-primitive-queries.md §sequencing; T-65 (bal's query shape well-understood after wave 4) | 2 | #32 | 8 |
| 35 | `@CAL()` query provider | oql-primitive-queries.md §sequencing (span/availability queries, not point lookups) | 2.5 | #32 | 8 |
| 36 | `@TS()` query provider | oql-primitive-queries.md §sequencing (hardest: fully Pebble, no SQL fallback) | 3 | #32 | 8 |
| 37 | iolu / xia split: rename current admin CLI to xia, repurpose iolu around querying the substrate (scriptable CLI plus an iaul-style REPL as one subcommand, not REPL-only; bare invocation defaults to REPL per the mysql/mysqladmin precedent) | oql-primitive-queries.md (separable track); T-70 (query+REPL mechanics) | 1.5 | T-70 | 8 |
| 41 | `loc` core: package skeleton + 3 pinned decisions, containment tree + placement-chain composition (no guards), assignment/capacity/move CAS with multi-target atomicity — the guard-bearing core | loc-02-implementation.md Stages 0–2; loc-00-design.md §3a/§3b/§3c/§3d/§5a/§7a | 4 | — | 9 |
| 42 | `loc` geometry: `Circle`/`Polygon`, ray-casting containment, R-tree/Geopoly SQL-plane pre-filter wired into #41's fence-membership test hook | loc-02-implementation.md Stage 3; loc-00-design.md §4/§5/§6 (R-tree/Geopoly compiled-in, verified empirically already — no re-verification owed) | 1.75 | #41 | 9 |
| 43 | `loc` journal + rebuild oracle: append-only movement journal, `derive(journal) == current` proof, `iolu db check` hook point | loc-02-implementation.md Stage 4; loc-00-design.md §8/§8c | 1 | #41 | 9 |
| 44 | `loc` events + dxp participant + REST API surface: `locParticipant` (Reserve/Validate/Execute/Release), all 15 endpoints from loc-01-rest-api.md, two-identity regression check | loc-02-implementation.md Stages 5–6; loc-01-rest-api.md complete index | 2.25 | #41, #42, #43 | 9 |
| 45 | `obj` core: package skeleton (reusing `pkg/storage/meta_subject.go`'s validator), attach/detach/position for the two non-containment termination kinds, no cycle safety yet | obj-02-implementation.md Stages 0–1; obj-00-design.md §6 | 1.5 | #42 (needs `loc`'s real, not test-hooked, `report` resolution) | 10 |
| 46 | `obj` containment + cycle safety + multi-dimensional capacity — the highest-risk stage in either primitive: 2D (weight+volume) CAS combined with a traversal-shaped guard with no analog elsewhere in this codebase; includes the `pkg/graph.wouldCreateCycle` extraction into a form callable against transaction-scoped rows | obj-02-implementation.md Stage 2; obj-00-design.md §5/§7/§10 | 3.5 | #45 | 10 |
| 47 | `obj` promote/demote: `bal` decrement + entity create-or-reuse + `obj` attach/detach as one dxp-dispatched atomic transition | obj-02-implementation.md Stage 3; obj-01-rest-api.md §5. One open design question (named attachment slots, obj-00-design.md §13) could still reshape this item's request schema | 1.75 | #46 | 10 |
| 48 | `obj` journal + rebuild oracle, `obj`'s own `derive(journal) == current` | obj-02-implementation.md Stage 4 | 1 | #46 | 10 |
| 49 | `obj` graph mirroring (`bal.Adapter.PostCommit`/`EmitDeltas` shape, reused near-verbatim) + events + dxp participant, including the adversarial same-primitive-collision test (T-109-shaped) | obj-02-implementation.md Stages 5–6 | 2 | #46, #48 | 10 |
| 50 | `obj` API surface completion: `capacity`/`contents`/`retire` endpoints, full `XOLU-OBJ` error-code reachability sweep | obj-02-implementation.md Stage 7; obj-01-rest-api.md | 1 | #49 | 10 |

Register items not listed by number here (T-03–T-06, T-14, T-16, T-20,
T-23, T-27, T-28, T-33) are lower-priority debt: swept into wave 7 or
closed opportunistically when touching their theme; re-triage at each
wave boundary.

## 3. The waves

**Wave 0 — Preflight (≈ 1 day; runs first, once).** Two acts before
any code. (a) Sync the design corpus (seven new documents from
2026-07-19: the six proposals and this plan) to GitHub so the source of
truth carries what was designed. (b) Audit @C04a (guard locality),
@C04b (finiteness), @C04c (meta inert), and @C04d (sized ids survive
the wire) against the existing tree — cal, ts, storelayout, meta — and
file findings as either "already compliant" (recorded) or new register
items. Existing incumbents were designed before the laws were
canonised; assuming compliance is the class of guess the working
agreement forbids. (@C04d was itself canonised late, after a uint32
widening broke the 32-bit build and truncated ids in /ts — a law that,
had it existed and been audited at wave entry, would have caught the
defect before it shipped. Its retroactive audit obligation is: every
primitive exposing a numeric id names its boundary pattern and ships
the range test. ts is corrected; cal is compliant by design; bal
inherits it pre-build.)

**Wave 0 — Process foundation and quick debt (≈ 5.5 days).**
Items 1–7. Exit: register at its lowest open count since inception;
dormant-guards table live and seeded with the existing verification
records (cal stress, CI runs); git history flowing; hygiene script
guarding releases; meta ready for every later reference-code use.

**Wave 1 — Per-primitive ID widening and system-scope reservation (≈ 4 days).**
Items 8–9. `/ts` codec widens its tenant-key prefix from 2 bytes to
4 bytes so per-tenant `TimelineID` becomes uint32; `/cal` gets an
audit for analogous caps and either widens or documents "no cap
today". `/bal`'s ID width is chosen at implementation (@B) with
uint32 as the default; this wave records the choice. Sysmask partitions
the now-32-bit primitive ID space via an immutable uint8 width
(default 0 = entirely user-space); the substrate provides only the
partition predicate and two-allocation-path enforcement, with
system-region meaning deferred to later convention (@S, A-flat).
Transvasing — the offline, non-lossy migration that makes the
init-frozen width survivable — is designed in now and built wave 6.
Exit: suite green on the new codec; `iolu db status` displays the
sysmask width;
existing timelines migrate cleanly (see @P01 principle #4 — before
any production deployment).
**Hard gate: no production deployment before this wave completes.**

**Wave 2 — Referential integrity (≈ 6 days).** Item 10, per the RI
proposal's staging: restrict first (the safety half), then
cascade/nullify with fault-injection atomicity tests, then opt-in
write validation with before/after ingest benchmarks, then client and
molu surface. The restrict race harness registers as a dormant guard
on the day it is written. Exit: the RI property test green on
multi-core CI; `CascadingDelete` flag retired.

**Wave 3 — Chronicle substrate (≈ 6 days).** Items 11–13. The cascade
engine generalises ts's machinery behind the monoid parameter; ts's
own aggregates become its first instantiation (cal migrates
opportunistically or never, per @C05). Sealer lifts with its
serialisation discipline intact. Exit: ts suite green on the new
engine; oracle harness running the existing cal and graph oracles.

**Wave 4 — bal (≈ 8 days).** Items 14–17, per the bal proposal with
every session amendment included: chain triple, inline memo,
hierarchical accounts with XOLU-BAL005, int64 doctrine with the
float-smuggling test, checkpoints, seal, prefix-collapse. Exit: the
near-floor race harness green on multi-core CI (registered wave 0
table); conservation and chain verifiers wired into the oracle
harness; the reference stock-tracking consumer has its substrate.

**Wave 5 — dxp (≈ 21.5 days; the largest).** Items 18–21, 23, 38–40
(item 22 retired, merged into 21 — see the 2026-07-29 deviation note).
Order: the reserved-commit facility first (it dissolves the
entity-staging gap and thins every adapter), then adapters, then defs,
then entity CREATE and the `ts` adapter as companion pieces the
coordinator needs (cross-substrate invalidation, item 39, no longer
one of these — reconsidered 2026-07-29, not gate-blocking), then the
coordinator itself — **3PS as the sole execution model for v1, 2PS and
the wider phase-spectrum beyond 3PS pending — deferred unless
cross-tenant or cross-instance dxp transactions demand them (§6,
Deferred)** — with the degradation-equivalence and outcome-uniqueness
harnesses, then the def-as-tool surface. Item 21's own design —
`ParticipantStore`, the attendance protocol, the no-durability
decision and its two independent justifications, canonical-doctrine
verification — is complete and recorded in full in
`dxp-coordinator-design.md` (T-85); nothing implemented yet. Exit: the
hotel example (§5a) runs as an integration test — cal + bal + fsm +
ts + entity in one committed outcome (§5a's own participant list, `ts`
restored per T-86 after being dropped without comment somewhere in
this session's own narrative; entity's leg a real CREATE, independently
load-bearing per the other worked example, §3) — on both the collapsed
and phased paths, producing identical records. `ts`'s presence is what
makes "phased" a genuine cross-engine proof rather than an all-SQL
stand-in — not because `@D06` names engine type (it states
single-tenancy, checked directly against the framework's own source),
but because a Pebble store mechanically cannot join one SQL
transaction regardless. Full detail, including the correction, in
dxp-coordinator-design.md.

**Wave 6 — iolu operations (≈ 8.5 days).** Items 24–29. cal
provisioning first (the only no-workaround gap), REPL second
(leverage), then the maintenance set. All new commands written against
the Store interface per the backend-coupling strategy. Exit: an
operator can provision, inspect, query, verify, back up, and repair a
deployment without writing Go.

**Wave 7 — Events, system stores, and residual debt (≈ 10 days,
provisional).** Items 30–31 plus the register sweep. ~metering and
~billing are the substrate dogfooding its own primitives; their
completion is the natural demo artefact for fundraising conversations.

**Wave 8 — OQL primitive queries (≈ 13 days, added 2026-07-28).**
Items 32–37, per oql-primitive-queries.md. The Pebble-backed and
mixed-storage primitives (ts, cal, bal) have no query language today;
this wave extends OQL with `@`-prefixed dispatch to primitive-specific
providers — validated to parse against tsqlparser unmodified before
any of this was scheduled, not assumed to work. Sequenced by
translation difficulty (fsm first, ts last), not by importance. Item
37 (the iolu/xia rename) rides along as a separable track on the
same wave since it shares no code dependency with 32–36 but was raised
in the same planning pass. Depends on nothing upstream; may run
alongside wave 5/6 remainder or after, since it touches pkg/oql and
cmd/iolu exclusively — no shared surface with dxp's coordinator or
iolu's operations commands.

**Wave 9 — `/loc` (≈ 9 days, added 2026-08-01).** Items 41–44, per
loc-00/01/02-design.md. Explicitly mirrors `bal`'s shape, not `cal`'s
or `ts`'s — SQL-only canonical state, no bit-packed codec, no Pebble
plane. Reuses wave 4's CAS-predicate discipline, rebuild-oracle
pattern, and wave 5's live `dispatchDxpTxn` coordinator directly;
`/loc` pays none of wave 4 or wave 5's own novel-infrastructure cost.
Genuinely new work is concentrated in item 41 (multi-target atomicity
— one CAS sequence spanning a leaf and N fences, structurally more
complex than any existing primitive's single-resource guard) and item
42 (ray-casting polygon containment; R-tree/Geopoly compiled-in and
working, verified empirically in an earlier session, so the geometry
work itself is new but its storage substrate is de-risked). Client
library and `iolu` CLI are explicit v1 non-goals, matching `bal`'s own
T-67 deferral. Exit: item 41's multi-target atomicity race harness
registered as a dormant guard (sandbox pass only — multi-core
confirmation is Horacio's to run, same convention as `bal`'s G-13);
all 15 endpoints from loc-01-rest-api.md's index green; a write-path
throughput number recorded for the guard-bearing core, however rough
(loc-00-design.md §13's own named gap).

**Wave 10 — `/obj` (≈ 10.75 days, added 2026-08-01).** Items 45–50,
per obj-00/01/02-design.md. Depends on wave 9: item 45 routes through
`/loc`'s own `report` handling directly ("no new logic, just routing
through it," obj-02-implementation.md Stage 1), so `/loc`'s geometry
(item 42) needs to be real, not test-hooked, before item 45 is
meaningful. Mirrors `bal`, not `loc` — `/obj` has no geometry of its
own; position is always resolved through something else. The single
highest-risk item in either wave 9 or wave 10 is item 46: a
two-dimensional (weight and volume) capacity CAS combined with a
traversal-shaped cycle-safety guard in one transaction, a combination
with no analog anywhere else in this codebase's guard-bearing write
paths, requiring a real (if small) extraction of `pkg/graph`'s
`wouldCreateCycle` into a form callable against `/obj`'s own
transaction-scoped rows rather than `pkg/graph`'s live node map.
Unlike wave 9, four design questions remain genuinely open
(obj-00-design.md §13: named attachment slots, non-versioned
placement for a discretely-repositioned anchor, `/loc`+`/cal`
composability, the guarantee-strength gap for application-enforced
policy) — none block the items above, but item 47 specifically is
flagged as possibly needing a schema change if the attachment-slots
question resolves before it's built. Client library and `iolu` CLI
deferred, same as wave 9. Exit: item 46's stress harness (concurrent
cycle-construction attempts from multiple directions, concurrent
capacity contention on both dimensions independently) registered as a
dormant guard the same session it's written; item 49's `PostCommit`
proven through a real `dxp` dispatch on both collapsed and phased
paths (one path passing is not evidence for the other, this session's
own established standard) plus the adversarial same-primitive
collision test named in obj-02-implementation.md Stage 6; full
obj-01-rest-api.md surface green including `retire`'s non-empty
refusal (`XOLU-OBJ012`).


**Wave 12 — pkg/client blob support (≈ 2.0d, added 2026-08-03).** Reopens the client's own documented v0.16.0 exclusion of /blob (pkg/client/client.go's own package doc, drawn by the T-02/Stage-6 audit -- docs/CLIENT_STAGE6_PLAN.md), triggered by a real consumer needing it: Seam AMS. Mirrors T-67's own shape exactly (bal's own client addition -- bal.go+types_bal.go, matching cal.go's established pattern), the closest precedent for adding client support to an already-shipped primitive; T-67 itself was never wave-numbered, so this is the first wave whose purpose is closing a documented client-scope exclusion rather than building a new primitive. Wraps the six native (non-S3-compat) blob endpoints only -- the S3-compatible surface exists for real S3 SDKs, not xolu's own first-party client. Timeseries is the other primitive on the same original exclusion list, named for the same reason in the same audit; not part of this wave, an obvious sibling candidate if reopened later.

**Not scheduled here, deliberately:** `/far` and `/dxp/mxn`
(far-and-dxp-mxn.md) are reviewed proposals, not a wave — sequenced
behind waves 9 and 10 plus Pablo's CMMS-side teams reaching
productivity and nolu's own development resuming (currently paused),
per the 2026-08-01 decision recorded in §6 (Deferred) and `TRACKING.md`'s
Plan deviations log. If scheduled later, it is plausibly wave 11, not
9 or 10 — not decided here.

## 4. Dependency spine

The critical path is: **wave 0 → 1 → (2 ∥ 3) → 4 → 5**, with wave 6
parallelisable against 4–5 after item 13 lands, and wave 7 last.
Waves 2 and 3 are independent of each other and may swap or
interleave. Item 18 (the facility) wants the RI edge-table work done
first so tentative-edge support lands once, not twice. **Waves 9 → 10
form their own short spine, added 2026-08-01**, gated only on wave 4
and wave 5 already being complete (both are, as of this writing) —
`/loc` and `/obj` share no code dependency with waves 6/7/8 and may
run alongside or after them; the only hard internal ordering is 9
before 10, per item 45's dependency on item 42.

## 5. Totals

Ideal effort, waves 0–6 (including preflight): **≈ 61.5 days**. With wave 7: **≈ 70.5 days**. With wave 8: **≈ 83.5 days**. With waves 9–10 (**≈ 19.75 days**, added 2026-08-01): **≈ 103.25 days**.
Calendar, solo with founder duties at 1.5–2×: **roughly 4.5–6.5
months** through wave 8, **roughly 5.5–8 months** through wave 10 —
though 9–10 specifically are likely to land nearer the low end of
their own range, since neither pays for new wave-level infrastructure
the way earlier waves did (no preflight audit, no chronicle/dxp
buildout) and item 45 benefits from item 41–44's patterns being freshly
proven rather than learned twice. Each wave boundary is a legitimate pause
point with a releasable, documented, closed-out tree — the plan
degrades gracefully if interrupted, by construction.

## 6. Deferred — demand-gated, explicitly not committed

- **Postgres backend** (the kernel fence, pgledger-class hot-account
  throughput, MVCC locks): estimated 20–40 days; builds when a
  customer's deployment profile demands it. Inherits requirements
  already recorded: ref-column materialisation (@R05a), sysmask
  federation checks, guard-locality obligations (@R05).
- **Blob mutation + lock primitive + deltas**: proposal owed before
  estimation is honest (~0.5 day to write; implementation previously
  sketched at 2–3 weeks). Builds on a content-workspace product
  signal.
- **Holds in production use** beyond the facility (item 18 ships the
  machinery; consumer wiring waits for a paying flow).
- **Persistent (durable, cross-process-visible) dxp transactions**
  (2026-07-29, direct instruction: "we're not going to implement
  persistent transactions. We may never do that."): the shipped model
  is in-memory-only reservations with crash-abandon semantics (T-54's
  pivot away from persisted tentative rows) — a process restart loses
  every live claim, by design, not as an interim limitation awaiting
  completion. A durable transaction log surviving process death, or
  reservations visible to a second process, is explicitly not planned
  and may never be built. If ever revisited, this is nolu territory
  per @D08, not a dxp v2.
  **Why this is sound, worked through directly rather than assumed
  (2026-07-29):** the one failure mode durability would actually
  guard against — a coordinator crash strictly between an earlier
  participant's successful Commit and a later one's, in a genuinely
  phased (non-collapsed, e.g. SQL+Pebble) instance — is expected to be
  extremely rare, given `Ready()`'s guard window is short by
  construction and the collapsed-ACID path (the common case today)
  doesn't have this failure mode at all. Idempotent re-execution is
  the expensive piece durability would require (checked concretely:
  bal's own shipped `transfer_id` has no uniqueness constraint or
  dedup check today — calling `Transfer` twice would silently double
  the movement — so "resume by re-executing" is unsafe until every
  adapter is retrofitted for it, a real per-adapter migration, not a
  detail) — and with mid-execute crashes this rare, that cost isn't
  justified by the risk it would close.
  **Separately, hotswapping instances between servers — a distinct
  reason durability might otherwise seem needed — turns out not to
  require it either, and for a different reason than crash rarity:**
  xolu's intended hotswap model is cooperative, not an instant kill —
  the outgoing instance redirects what it can and waits for
  confirmation the new instance is running smoothly before taking
  itself down (2026-07-29, direct clarification). That's a nolu-owned
  mechanism, not a dxp one. What exists today toward it, checked
  directly rather than assumed: `cmd/xolu/main.go`'s SIGTERM handler —
  stop accepting new requests, 15-second grace period for in-flight
  ones to finish, then close. That's a genuine, minimal foundation
  (the "let in-flight work finish" half), but there is no redirect-to-
  another-instance mechanism and no await-confirmation-from-the-other-
  side mechanism yet — those remain nolu's to build. Because the
  handoff is meant to be cooperative, an in-flight dxp instance simply
  has time to reach a natural stopping point (Reserve/Validate with no
  native handle open yet: drop and let the client retry against the
  new instance; Execute/Commit already in its short guard window: let
  it finish before completing the swap) — a drain against a timeout,
  not a durability mechanism, and not a dxp concern to build.
- **2PS and the wider phase-spectrum beyond 3PS** (2026-07-29, direct
  instruction, refining the entry above — a different posture, not the
  same decision): "pending", not a hard requirement — deferred unless
  needed, not abandoned. The external dxp framework's own formal
  spectrum (§2a: "0.5 → 3 phases, with the intermediate points named")
  includes proofs for 2PS and 3PS (`dxp-11`/`dxp-12` in the framework's
  own `doc/` tree — read directly, not merely cited; genuine theorem-
  and-proof documents, not the informal prose the main guide's tone
  suggested at first pass) and a **Quorum Modifier (QM)** extending
  3PS (`dxp-13`/`dxp-14`), proven separately. **Correction (2026-07-29,
  found while re-checking against the framework's actual doc, not
  re-derived from memory): QM is not what an earlier pass of this
  document claimed.** It relaxes 3PS's unanimous-attendance
  requirement to a majority (`Q = ⌈n/2⌉+1`) **within one transaction's
  own participant set**, so the transaction can proceed despite a
  minority of participants being unavailable — a fault-tolerance
  relaxation for participant availability, not a mechanism for
  replicating a transaction across independent xolu instances. The
  earlier claim that "quorum is what cross-instance dxp would
  actually need" conflated the two by word association and was wrong;
  corrected here rather than left standing. Named trigger, unchanged:
  cross-tenant and/or cross-instance dxp transactions — QM may still
  be relevant to either, if a participant becomes unavailable within
  either scope, but it is not itself the cross-instance mechanism.
  Cross-tenant dxp is already parked (T-54: token-bearing transactions
  mounting a remote /dxp/txn object from another tenant of the same
  instance — its stated prerequisite, item 18, is now done, so the
  parking is live, not stale). Cross-instance dxp is the larger scope
  again — nolu territory per @D08 regardless of tenant scope, not a
  dxp v1 concern either way.
  **2026-08-01: this entry's own named trigger has fired.**
  `docs/proposals/far-and-dxp-mxn.md` (`/far` + `/dxp/mxn`) is the
  proposal written in response — cross-tenant and cross-instance dxp
  both, per its own scope. Not pulled forward into a wave: implementation
  is sequenced behind Pablo's CMMS-side development teams reaching
  productivity on xolu, `/loc` and `/obj` shipping, and nolu's own
  development resuming (currently paused) — instance discovery in
  particular is expected to route through nolu rather than `/far`
  inventing its own mechanism (far-and-dxp-mxn.md §5.4). Real
  reactivation condition, not a "maybe never": all three of the above.
- **nolu**: its own programme; this plan only exports its interfaces
  (participant contract, sysmask federation rule).
- **Extracting `pkg/fsm/eval` into its own `pkg/eval` package**
  (2026-07-29, direct instruction): raised while designing dxp's
  dependency-chaining/result-binding idea (a later wave's params
  reading an earlier wave's committed result, `result.<id>.<field>`,
  reusing `pkg/fsm/eval`'s existing `QualifiedIdentifier` → flat-key
  resolution rather than building a second evaluator). Living inside
  `pkg/fsm` is the "less-than-clean" part — the evaluator itself is
  substrate-neutral (no fsm types imported anywhere in it, confirmed
  directly), but its package path implies it's fsm-specific to any
  future importer. First proposed as `pkg/qs`, a loose catch-all for
  query-related code generally; corrected to **`pkg/eval`** once size
  was actually checked — 6,655 lines is substantial enough to warrant
  its own name, not a shared bucket.
  **Cost, already assessed directly rather than left for whoever picks
  this up (2026-07-29):** zero circular imports (`pkg/fsm/eval` never
  imports `pkg/fsm` itself) and only 5 real external call sites in the
  whole tree (4 in `pkg/server`'s fsm handlers, 1 in
  `pkg/storage/fsm_walk.go`) — a small, enumerable blast radius, well
  under a day of mechanical work: move files, fix 5 import sites and
  their package-qualified calls, rename the package declaration, no
  logic changes, existing `pkg/fsm` test suite should pass unmodified
  as the verification. One genuine nuance, not a blocker: `eval_seq.go`
  (`NEXT VALUE FOR` sequence increments) and `prequery.go` (transition
  pre-queries) carry fsm-specific *conceptual* coupling in their own
  doc comments ("supplied by the walk runtime," "run before a walk"),
  even though neither imports fsm types. Natural split: these two stay
  in `pkg/fsm`, importing `pkg/eval` rather than being part of it —
  dxp's own use case never needs either of them, only the generic
  variable-binding core.
  **Not urgent — deferred until the need actually arises, not
  scheduled:** dxp can import `pkg/fsm/eval` directly, as-is, whenever
  dependency-chaining actually gets built (itself still pending, see
  the wave 5 discussion — not committed work yet). The extraction is
  independent of that decision in time — doing it later costs the same
  as doing it now, so there's no reason to front-load it. Revisit when
  a second real consumer beyond fsm and (eventually) dxp materialises,
  or when carrying the fsm-shaped import path starts actually bothering
  someone rather than just reading slightly wrong.

## 7. Risks, named

- **Solo bandwidth** is the plan's binding constraint; the mitigation
  is the wave structure itself (every boundary releasable) plus the
  option of delegating bounded items (e.g. items 24–29) to Claude
  Code-style subcontracting.
- **Paper meeting production**: waves 4–5 implement designs with zero
  production hours; the harnesses and CI are the defence, and the
  proposals' testing obligations are contractual, not aspirational.
- **Scope creep**: this plan freezes at wave 0 start; anything
  discovered mid-wave files as a register item and queues — the
  session that produced eight proposals in one night must not become
  the session pattern that prevents shipping any of them.
