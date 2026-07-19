# `cal` — staged implementation plan

Status: Implementation plan (part of the `cal` design, with `cal-rest-api.md` and `cal-pebble-codec.md`)
Prepared: 2026-06-22
Author: haitch (h@ual.li)
Licence: Apache 2.0

> This sequences the build of the `cal` scheduling primitive. It exists because
> `cal` must **not** be attacked as one undertaking: the design has settled inputs
> and open decisions interleaved, and a monolithic push hits the open semantics
> and the under-specified source-of-truth record partway in. The order below is
> derived from the codec's own readiness self-assessment (§5 "Testing strategy"
> and the closing caution): the pure bit layer is "most likely correct, least
> likely to hurt"; the seal concurrency is "most likely to hurt, least amenable
> to TDD". Build outward from certainty.

## Principles

- **Each stage ends green.** Build + test pass before the next stage starts.
  No stage leaves the tree broken for the next to "fix on the way through".
- **`index == rebuild` is the spine.** The Pebble bitmap is a derived index over
  the SQLite record (H1). Every stateful stage carries the invariant that the
  index always equals what a rebuild-from-SQLite would produce; it is the free,
  strongest oracle and the acceptance gate for stateful work.
- **Mirror `ts`.** Package shape, codec/property-test/contract-test split, store
  and registry separation all follow `pkg/timeseries`, the verified sibling the
  codec is explicitly modelled on.
- **Gates are decisions, not work.** Two open decisions block specific stages.
  They are flagged inline; they need judgement, not implementation, and the
  blocked stages must not start before they land.

## Dependency gates (RESOLVED 2026-06-22 — see `cal-gate3-booking-record.md`)

All three gates are now closed; the resolutions are recorded in the booking-record
design note. Summary:

- **GATE-1 — `match` plane semantics: RESOLVED.** `match_considers` is a
  per-calendar def policy (`binding` default | `binding+proposed`), not a
  per-request flag. Mixed matches need no agreement; pessimism wins on clash
  automatically via the union-of-busy in `AndFree`. The proposed-plane pyramid
  (codec §6.5) is built but populated only for `binding+proposed` calendars.
  Unblocks **Stage 4** and the proposed side of Stage 2 (already built).
- **GATE-2 — `match/commit` in v1: RESOLVED — yes, in v1.** Unblocks **Stage 6**.
- **GATE-3 — SQLite booking record + A9 lifecycle: RESOLVED** (the design note).
  Tenancy follows xolu config; `cal_ordinal` reuse and booking retention are
  configurable; stored time is UTC instants only (xolutime-consistent); ordinal is
  a per-tenant dense `uint32` counter. Unblocks **Stage 3 onward**.

---

## Stage 0 — Package skeleton and decision capture

**Goal:** `pkg/cal` exists, compiles, and pins the settled inputs as code-level
constants so later stages cannot drift from the design.

- Create `pkg/cal/` mirroring `pkg/timeseries/` layout.
- `cal.go` / `doc.go`: package doc stating the H1/H3 split (SQLite = source of
  truth, Pebble = derived index) and the conversion invariant (§2.2a) in prose.
- Constants from the settled codec decisions, with the design section cited at
  each: `QBaseMinutes = 5`, `QuantaPerDay = 288`, `WordsPerDay = 5` (`[5]uint64`),
  the plane enum (`PlaneBinding`, `PlaneProposed`), the entity-handle sentinels
  (`EntityNil = 0`, `EntityTombstone = MaxUint64`, `EntityMaxValid`), and the key
  layout constants (`kind`, `cal_ordinal:uint32`, `plane:1`, `day_unixnano:8`).
- No behaviour yet beyond the constants and types.

**Gate:** none. **Exit:** `go build ./pkg/cal/` clean; `go vet` clean.

---

## Stage 1 — The pure bit layer (the shovel-ready core)

**Goal:** the complete, correct, branch-free occupancy bitmap engine, with no
storage and no state. This is the two-thirds of the codec that is safe regardless
of every open decision.

- `dayKey(cal_ordinal, plane, dayUnixNano)` — the key encoder, mirroring the `ts`
  codec's big-endian UTC-`UnixNano` discipline. Day flooring by **integer
  division** on `86_400_000_000_000`, not a bitmask (the design's explicit
  flooring note — the day span is not a power of two).
- Span → bit range: convert a `{start, end}` UTC span (an `xolutime.Instant` pair)
  to the set of quanta it occupies within a day, handling the midnight-crossing
  case (a span sets bits in two adjacent day-values, codec §3.3).
- `[5]uint64` set/test/clear; population count for capacity.
- Cross-calendar **AND** of equal-resolution day-words (the F16 matching kernel).
- The daypart **rollup** (one v1 level, 3-hour, 8 bits/day): fine → coarse
  down-projection, and the prune-but-never-confirm read (the lossy direction).
- The conversion helpers (§2.2a): coarse→fine lossless expansion; reconcile
  toward the finer grain.

**Testing (TDD, this is the TDD-shaped part):**
- `codec_property_test.go`: property tests against a **brute-force
  interval-overlap oracle**. The bitmap is an optimisation of "do these intervals
  overlap"; the slow correct version is the oracle. Random spans, random
  calendars, assert AND-of-bitmaps == intersection-of-interval-sets.
- Midnight-crossing, empty-day, full-day, single-quantum, and capacity-popcount
  edge cases as explicit table tests.
- Rollup soundness: assert the rollup can only *prune* a match the fine layer
  would reject, never *confirm* one the fine layer denies.

**Gate:** none — every input is settled. **Exit:** property suite green; oracle
agreement across a large random corpus; `go test ./pkg/cal/ -run 'Codec|Bit|Rollup'`
passes.

> This stage can start immediately and is the right place to begin. It will also
> surface any latent codec-spec ambiguity early, while it is cheap to fix.

---

## Stage 2 — Single-calendar availability reads (still index-only)

**Goal:** answer "is this span free/busy?" and "what is capacity here?" against an
**in-memory** bitmap, before any persistence. Exercises the bit layer through the
read surface without the storage or lifecycle complexity.

- `free`/`busy` boolean complement over the binding plane.
- `q=capacity` ternary: `free` / `idk` (proposed-present) / `busy`, plus
  `capacity = 100 − confirmed%` and the raw `counts` (per `cal-rest-api.md` §3b).
  NOTE: the binding-plane reads are unconditional; the **`idk`/proposed** path is
  the part touched by GATE-1 — implement the binding side now, leave the proposed
  contribution behind a clearly-marked seam.
- `openings` (was `findgap`): free spans of a given duration, fixed `objective`
  enum only (no scoring function — the anti-solver line).

**Gate:** partial GATE-1 (proposed-plane contribution to `idk`). Binding-side
ships now; proposed-side waits. **Exit:** availability reads correct against
in-memory bitmaps, validated by the same oracle.

---

## Stage 3 — SQLite source-of-truth record + persistence + rebuild

**Goal:** the bitmap becomes a *derived* index over a real SQLite booking record,
with the `index == rebuild` invariant enforced.

**Prerequisite: GATE-3 must be closed first** — the booking record schema and the
A9 lifecycle must be specified before this stage, not during it.

- SQLite calendar-def and booking records (state, A9 lifecycle fields, bearer
  ref, participants, `when` span, mode). `cal/def` allocates and records the
  `cal_ordinal` (codec §3.2) and the entity handle.
- Pebble store per tenant (settled: one store, §3.1), keyed by the Stage-0 layout.
- **Write path:** create/cancel mutate the SQLite record *first* (source of
  truth), then update the derived index (codec §4.5). Bearer/`default_state`
  validation including the review-issue-2 rule (a `binding`-default calendar
  rejects a bearer-less create at create time).
- **Rebuild:** reconstruct the entire Pebble index from the SQLite records. This
  is both a recovery path and the test oracle.

**Testing:**
- `contract_test.go`: after any sequence of create/cancel, assert
  `index == rebuild` — the global invariant.
- Persistence round-trips; per-tenant isolation; `cal_ordinal` allocation and the
  delete/reuse policy (codec §6 item 7 — settle the reuse question here).

**Gate:** GATE-3 (blocking). **Exit:** full create/cancel/read cycle persists,
survives a rebuild bit-for-bit, per-tenant isolated.

---

## Stage 4 — Multi-calendar match

**Goal:** the N-way "when are all these free?" operation — the operation the
primitive most exists for (F16).

**Prerequisite: GATE-1 must be closed** — match's plane semantics (binding-only
vs binding-OR-proposed) determines whether the proposed plane needs a pyramid and
whether it is on this hot path.

- N-way AND across calendars using the Stage-1 kernel and the Stage-1 rollup to
  prune (rollup prunes candidate days; fine layer confirms).
- `match` read endpoint; `check` (was `dryrun`) sharing the create body and
  returning `feasible:false` + `nearest_openings` as a 200.
- The `consider=binding | binding+proposed` request field if GATE-1 resolves
  toward caller choice (the recommendation in review issue 1).

**Gate:** GATE-1 (blocking). **Exit:** match correct against the oracle for N
calendars; rollup-pruned path proven equivalent to the fine-only path.

---

## Stage 5 — Lifecycle, buffers, participants, move

**Goal:** the full A9 lifecycle and the remaining single/cross-calendar
operations that are not atomic-placement.

- `propose` → `confirm` → `binding`; `complete` → `honoured`; sweeper-written
  `missed` for required-participant no-shows; `cancel`.
- `buffer` (aftermath hold past `end`); optional participants and the
  confirmation-drives-capacity / required-drives-missed split (§2a).
- `move` (was `reschedule`): atomic single-booking reschedule, **reporting**
  stranded dependents, never cascading (the F9(a) exclusion).
- The sweeper for `missed` and (deferred-trigger aside) the now-crossing seal —
  see Stage 7 for the seal's concurrency.

**Gate:** none beyond Stage 3. **Exit:** lifecycle transitions correct;
`index == rebuild` holds across every transition; move reports rather than
cascades.

---

## Stage 6 — Atomic cross-calendar placement (`match/commit`)

**Goal:** the write counterpart to `match` — place one booking per calendar across
N calendars, all-or-nothing (finding F-F).

**Prerequisite: GATE-2 must be closed** — whether this is in v1 at all.

- `POST .../cal/match/commit`: N bookings, atomic. Within v1's one-store-per-tenant
  layout this is a single-store multi-key Pebble batch plus a matching N-record
  SQLite transaction (codec §6 item 10 confirms no structural obstacle).
- Universal conflict report (§6) naming blocking calendars; nothing commits on
  conflict. No ordering, no objective (anti-solver line preserved).

**Gate:** GATE-2 (blocking). **Exit:** all-or-nothing proven under partial-failure
injection; `index == rebuild` holds after both success and rolled-back attempts.

---

## Stage 7 — The now-crossing seal (the hard part, last)

**Goal:** seal each day at the now-crossing (H2) — the live-lean-future /
sealed-past boundary — correctly under concurrency.

This is the part the design is most explicit about being hard and least able to
de-risk on paper. It is **design-then-race-test**, not TDD: you cannot write the
test first for a race you have not characterised.

- Characterise the seal invariant: what must hold as a day crosses from mutable
  future to immutable past while writers and the sweeper interleave.
- Implement the seal as a sweeper concern (not a write path, codec §4.5).
- **Race/fault-injection test under `-race`** that hammers the interleaving:
  writes landing exactly at the now-crossing, seal racing concurrent
  create/confirm/move, crash-mid-seal recovery.
- `index == rebuild` remains the acceptance oracle: after any interleaving and
  any recovery, the index must equal a fresh rebuild.

**Gate:** none, but depends on Stages 3 and 5 being solid. **Exit:** `-race`
clean under sustained interleaving; recovery correct; the global invariant holds.

---

## Deferred beyond v1 (scale-triggered, not in this plan)

Per codec §6, with concrete triggers, explicitly out of the v1 build:

- Daily rollup level (second pyramid level) — add when the daypart level's read
  cost is measured insufficient.
- Sealed-past / live-future **store split** — add at the scale where cold-archive
  compaction measurably hurts the hot path.
- Per-resource quantum multiples (`k > 1`, the `k × Q_base` coarser calendars) —
  the deferred feature whose first concrete customer is the hotel/night-resolution
  domain (finding F-E).
- `delta` (sub-quantum grid offset) — kept as keyspace reservation; the slow
  mixed-`delta` match path is not built until a real calendar needs it (codec
  §6 item 6 / review of finding on `delta`).

---

## Summary: what to do now

| Stage | Ready? | Gate | Shape |
|---|---|---|---|
| 0 Skeleton + constants | **now** | — | mechanical |
| 1 Pure bit layer | **now** | — | TDD vs oracle |
| 2 Single-cal availability | **now** (binding side) | GATE-1 (proposed side) | TDD vs oracle |
| 3 SQLite record + persist + rebuild | after GATE-3 | GATE-3 | design-then-build |
| 4 Multi-cal match | after GATE-1 | GATE-1 | TDD vs oracle |
| 5 Lifecycle + move | after Stage 3 | — | build + invariant |
| 6 `match/commit` | after GATE-2 | GATE-2 | fault-injection |
| 7 Now-crossing seal | last | — | design-then-race |

**Start Stages 0–1 immediately** (decision-free, TDD-shaped, the safe core). **In
parallel, close GATE-1 and GATE-2** (your judgement, no implementation) and
**write the GATE-3 SQLite-record/A9 design note** (the one genuine missing design
piece). That unblocks Stages 3–4 by the time the bit layer is green.
