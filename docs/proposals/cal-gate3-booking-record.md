# `cal` — booking record, lifecycle, and gate resolutions

Status: Design note (part of the `cal` design; unblocks Stage 3 of `cal-implementation-plan.md`)
Prepared: 2026-06-22
Author: haitch (h@ual.li)
Licence: Apache 2.0

> This note specifies the SQLite source-of-truth record (H1) and the A9 lifecycle
> that the bitmap index (Stages 0–2) derives from, and records the three gate
> decisions taken in the 2026-06-22 session. It is the design piece the
> implementation plan named as the real long pole: everything stateful (Stages
> 3–7) rests on it. Stages 0–2 (the pure bit layer and in-memory availability)
> are already built and green; nothing here changes them.

## Gate resolutions (2026-06-22)

### GATE-1 — `match` plane semantics: a per-calendar policy, not a per-request flag

`match`'s optimistic/pessimistic behaviour is fixed **at calendar creation**, as a
third per-calendar policy field alongside `default_state` and `optionality`:

- **`match_considers`**: `binding` (optimistic — proposals do not block matches) |
  `binding+proposed` (pessimistic — proposals block). Default: `binding`.

This is cleaner than the per-request `consider=` flag floated in the review of
issue 1: a calendar's matching semantics are a property of the *resource*, not of
each query. A high-contention resource (an operating theatre) is created
`binding+proposed`; a best-effort one is created `binding`.

**Mixed matches need no agreement, and pessimism wins on clash — for free.** When
`match` runs across calendars with different settings, each calendar contributes
its own busy-map to the N-way `AndFree`:

- optimistic calendar contributes its `binding` plane;
- pessimistic calendar contributes `binding.Or(proposed)`.

`AndFree` ANDs the free-masks, so the result is busy wherever *any* calendar's
contribution is busy. A pessimistic calendar's proposed bits therefore remove a
slot from the intersection; an optimistic calendar's proposals do not. "Pessimism
wins on clash" is thus automatic — it is the union-of-busy that `AndFree` already
computes, not a special case to code. No conflict resolution, no agreement check.

**Codec consequence (resolves §6.5):** the proposed plane is on the match hot path
**only for calendars that opt into `binding+proposed`**. Those calendars need a
proposed-plane rollup; `binding`-only calendars never do. So the proposed-plane
pyramid is built, but populated lazily — only for pessimistic calendars. This
keeps the common (optimistic) case free of proposed-plane rollup cost.

### GATE-2 — atomic cross-calendar placement (`match/commit`): in v1

`match/commit` (finding F-F) ships in v1. Within the settled one-store-per-tenant
layout it is a single-store multi-key Pebble batch plus a matching N-record SQLite
transaction (codec §6 item 10 confirms no structural obstacle). Built in Stage 6,
after the single-calendar write path (Stage 3) and `match` (Stage 4) are solid.

### GATE-3 — the booking record (this note)

See the schema and lifecycle below. Sub-decisions:

1. **Tenancy layout — follows xolu tenancy configuration, not `cal`'s choice.**
   `cal` does not decide one-table-with-`tenant_id` vs per-tenant tables; it uses
   whatever the tenant/storage layer provides (the same per-tenant `tXXXX/`
   storage scheme the rest of xolu uses). `cal`'s records live under the tenant's
   store like every other primitive's.
2. **`cal_ordinal` reuse on delete — configurable.** A per-deployment policy
   `OrdinalReuse: retire | reuse` (default `retire`). See "cal_ordinal allocation"
   below for why retire is the safe default and reuse the space-saving option.
3. **Cancelled/expired bookings — soft-delete configurable.** A per-deployment (or
   per-calendar) policy `BookingRetention: soft | hard` (default `soft`).
   Soft-delete keeps the row with a terminal state for the audit trail (the
   `honoured`/`missed`/`cancelled` history that compliance depends on); hard-delete
   removes it. The derived index is unaffected either way — a cancelled or
   hard-deleted booking contributes no occupancy, and `index == rebuild` holds
   because rebuild reads only live (non-terminal-excluded) occupancy.
4. **Stored time — resolved UTC instants only, consistent with `xolutime`.** The
   booking record stores `when` as absolute UTC instants (the `xolutime` invariant;
   R-T1). It does **not** store the caller's `local_time + zone_id`. Intention
   retention and zone-rule-change recovery are the owning layer's responsibility
   (R-T1 / `TIME_HANDLING.md`), not a `cal` column. This keeps `cal` consistent
   with `ts` and the system-wide rule: storage holds instants, calendars above
   hold intentions.
5. **`cal_ordinal` allocation — a per-tenant dense counter, not a reuse of an
   existing id scheme.** Reasoned below.

## `cal_ordinal` allocation (GATE-3 #5)

The question: derive `cal_ordinal` from an existing xolu id (the nolu/entity
handle) for safety, or allocate a fresh per-tenant counter for density?

**Decision: a fresh per-tenant dense `uint32` counter, allocated ascending from 1.**
Reasoning, weighing the safety of reuse against performance and space:

- **The ordinal is a key-space coordinate, not an identity.** It exists only to
  place a calendar's days adjacently in Pebble so scans are local (codec §3.2). It
  is `uint32` precisely to keep the key 14 bytes. The entity handle is `uint64`
  and sparse (allocated across the whole federated registry). Reusing it would
  widen the key by 4 bytes on every occupancy day *and* scatter calendars across
  the key-space instead of packing them — losing the scan locality the ordinal
  exists to provide. That is a real, permanent performance and space cost on the
  hot path.
- **Density is cheap and bounded.** A per-tenant ascending counter packs ordinals
  0..N with no gaps; `uint32` ceilings at ~4.3 billion calendars per tenant, far
  beyond any real tenant. The counter is one `uint32` in the tenant's `0x03` index
  metadata (codec §3.3).
- **Safety is recovered by the mapping, not by reuse.** The booking/calendar
  record stores the authoritative `(calendar_id, entity_ref)`; the ordinal is a
  derived index coordinate mapped in `0x03` metadata. `index == rebuild`
  reconstructs the ordinal map from the SQLite records, so a lost or corrupted
  ordinal map is never a data-loss event — it is rebuilt. The safety reuse would
  have bought (never colliding with an entity id) is not needed, because the
  ordinal never escapes the index; it is not an identity anyone outside the codec
  sees.

So: dense per-tenant counter. The `OrdinalReuse` policy (#2) governs whether a
deleted calendar's ordinal returns to the counter (reuse — denser, but a stale
index entry could in principle alias if rebuild is skipped) or is retired (safe
default — the counter only ever moves up). Retire is default because it is
trivially correct; reuse is available where a tenant churns calendars enough that
ordinal exhaustion or key-space sparsity is measured to matter (essentially never
at `uint32`, but the knob exists).

## The booking record (SQLite, H1)

The authoritative relational record. The bitmap index is a pure function of the
live bookings here (codec §3 "Time derivation, one source of truth"). Fields:

| Field | Type | Notes |
|---|---|---|
| `booking_id` | text, PK (per calendar) | caller-supplied, declare-at-known-id (idempotent create) |
| `calendar_id` | text | the owning calendar (its def carries `cal_ordinal`, policies) |
| `state` | text enum | A9 state (below) |
| `start_utc` | int64 | UnixNano, UTC — resolved instant (xolutime), never local |
| `end_utc` | int64 | UnixNano, UTC; half-open `[start, end)` |
| `mode` | text | `exclusive` \| `shared` \| `sub:<child_id>` (fixed enum, A11) |
| `bearer` | uint64 | entity handle (A10); required for binding, may be `EntityNil` for proposed |
| `buffer_after` | int64 | optional aftermath hold in ns past `end` (Part D) |
| `created_utc` | int64 | UnixNano UTC, via `xolutime.Now()` |
| `updated_utc` | int64 | UnixNano UTC, via `xolutime.Now()` |
| `detail_ref` | text, nullable | ref into the meta/detail document |

Participants (when `optionality = per-participant`) live in a child table keyed by
`booking_id`, each row `{entity, required bool}` (§2a). The occupancy plane a
booking contributes to is a function of `state`: `proposed` → proposed plane,
`binding`/`honoured` → binding plane, terminal exits → no plane.

## The A9 lifecycle

```
                ┌─────────── propose ───────────┐
                ▼                                │
   (create) ─► proposed ──confirm──► binding ──complete──► honoured
                │                       │
              decline                 cancel
                │                       │
                ▼                       ▼
           not-committed            cancelled
                                        ▲
   binding ───(sweeper, §7)──► missed ──┘  (non-occurrence: due window passed,
                                            never completed)
```

- **States:** `proposed`, `binding`, `honoured`, plus terminal exits
  `not-committed` (declined proposal), `cancelled`, `missed`.
- **Transitions (each a no-body PATCH, optional `{note}`):**
  `propose` (→ proposed), `confirm` (proposed → binding), `decline`
  (proposed → not-committed), `complete` (binding → honoured, deposits an
  occurrence), `cancel` (proposed|binding → cancelled).
- **System-written:** `missed` (binding → missed) by the sweeper when a binding
  booking's window passes with no `complete` (§7). This is the non-occurrence
  record; it is what makes `honoured` meaningful by contrast, and is load-bearing
  for compliance.
- **Plane mapping:** `proposed` occupies the proposed plane; `binding` and
  `honoured` occupy the binding plane; `not-committed`, `cancelled`, `missed`
  occupy no plane (they free their quanta). State change therefore drives an index
  update: e.g. `confirm` moves a span from the proposed plane to the binding
  plane (the one operation that touches both planes — relevant to the seal, Stage
  7).
- **Bearer rule (review issue 2):** `binding` requires a live `bearer`
  (`ValidEntity`). On a `default_state: binding` calendar, a bearer-less create is
  rejected at create time (there is no proposed state to park it in); `?propose=true`
  is the escape hatch.
- **Illegal transitions** return `409` with the current state and the allowed
  transitions from it.

## The `index == rebuild` invariant (the Stage-3 acceptance gate)

The Pebble bitmap is a derived index. After any sequence of create/confirm/
decline/complete/cancel and any sweeper-written `missed`, the index must equal
what a fresh rebuild from the live SQLite records would produce. Rebuild:

1. Drop the tenant's occupancy + rollup keyspaces (`0x01`, `0x02`).
2. Reconstruct the `cal_ordinal` map (`0x03`) from the calendar records.
3. For each live booking, OR its span (via `SpanDays`, Stage 1) into the plane
   its `state` maps to.
4. Recompute the rollups from the rebuilt fine planes.

This is both the recovery path and the test oracle: Stage 3 is not done until a
property test asserts `index == rebuild` after randomised operation sequences.

## What this unblocks

- **Stage 3** (this note's subject): the SQLite record, the per-tenant Pebble
  store, the create/cancel write path, and the rebuild invariant.
- **Stage 4** (GATE-1 closed): `match` with per-calendar `match_considers`.
- **Stage 6** (GATE-2 closed): `match/commit` in v1.

Stages 5 (lifecycle/move) and 7 (seal) follow from the A9 machine above; the seal
remains the design-then-race-test long pole (the `confirm` cross-plane move is the
operation it must make safe).
