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
   is the actual ceiling Shevo's mid-market SMB workloads hit
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
| 21 | dxp/txn coordinator: 2PS, degradation collapse, sweep, harnesses | @D04 (patterns), §6 (degradation to ACID + equivalence test), §7 (recovery), §8a (four fsm relations), §10 (errors), §11 (test obligations) | 3 | #18–20 | 5 |
| 22 | 3PS phase machine | @D04 | 2 | #21 | 5 |
| 23 | dxp client + def-as-tool surface for molu | dxp §9 | 1 | #21 | 5 |
| 24 | iolu: cal provisioning | iolu roadmap item 1; backend coupling section (logical-core side, Store interface) | 1 | — | 6 |
| 25 | iolu: query + REPL | iolu roadmap item 3 (surface + iaul template); target-directory resolution policy (`IOLU_BASE_DIR`, upward discovery); backend coupling (logical-core via executor) | 3 | — | 6 |
| 26 | iolu: db check (promote oracles) | iolu roadmap item 5 | 1 | #13 | 6 |
| 27 | iolu: reindex-fts | iolu roadmap item 2 | 1 | — | 6 |
| 28 | iolu: backup + verify | iolu roadmap item 4 | 2 | — | 6 |
| 29 | iolu: rebuild-graph | iolu roadmap item 6 | 0.5 | — | 6 |
| 30 | Event model part 2 | T-07 through T-13 (re-triage at wave entry); @D05c tier 3 (events are commit-fed) | 5 (provisional) | — | 7 |
| 31 | System stores content: ~metering (ts) and ~billing (bal) | @S03 (residents); tilde-sigil convention; #17 (bal molu surface) | 5 | #9, #17 | 7 |

Register items not listed by number here (T-03–T-06, T-14, T-16, T-20,
T-23, T-27, T-28, T-33) are lower-priority debt: swept into wave 7 or
closed opportunistically when touching their theme; re-triage at each
wave boundary.

## 3. The waves

**Wave 0 — Preflight (≈ 1 day; runs first, once).** Two acts before
any code. (a) Sync the design corpus (seven new documents from
2026-07-19: the six proposals and this plan) to GitHub so the source of
truth carries what was designed. (b) Audit @C04a (guard locality),
@C04b (finiteness), and @C04c (meta inert) against the existing tree —
cal, ts, storelayout, meta — and file findings as either "already
compliant" (recorded) or new register items. Existing incumbents were
designed before the laws were canonised; assuming compliance is the
class of guess the working agreement forbids.

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
harness; Shelf's stock model has its substrate.

**Wave 5 — dxp (≈ 16 days; the largest).** Items 18–23. Order: the
reserved-commit facility first (it dissolves the entity-staging gap
and thins every adapter), then adapters, then defs, then the 2PS
coordinator with the degradation-equivalence and outcome-uniqueness
harnesses, then 3PS, then the def-as-tool surface. Exit: the hotel
example (@D05a) runs as an integration test — cal + bal + fsm + ts
in one committed outcome — on both the collapsed and phased paths,
producing identical records.

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

## 4. Dependency spine

The critical path is: **wave 0 → 1 → (2 ∥ 3) → 4 → 5**, with wave 6
parallelisable against 4–5 after item 13 lands, and wave 7 last.
Waves 2 and 3 are independent of each other and may swap or
interleave. Item 18 (the facility) wants the RI edge-table work done
first so tentative-edge support lands once, not twice.

## 5. Totals

Ideal effort, waves 0–6 (including preflight): **≈ 56 days**. With wave 7: **≈ 65 days**.
Calendar, solo with founder duties at 1.5–2×: **roughly 4.5–6.5
months**, i.e. substantially all of the remainder of 2026 if execution
starts promptly and holds. Each wave boundary is a legitimate pause
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
- **nolu**: its own programme; this plan only exports its interfaces
  (participant contract, sysmask federation rule).

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
