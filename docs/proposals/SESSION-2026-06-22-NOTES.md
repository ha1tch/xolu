# Session notes — 2026-06-22 (cal scheduling primitive)

> **Reconciliation status (added 2026-07-18, v0.14.13):** These are
> session notes from the design phase before cal implementation began.
> Historical value only. The ts (timeseries) changes described in the
> first section did ship in v0.13.2 and are current. The cal design
> commitments described below were substantially realised in v0.14.0
> through v0.14.5, with T-18 client-facing work in v0.14.7 and cal
> hardening through v0.14.13. For what is actually in the code today,
> see `CHANGELOG.md` and the "Reconciliation status" banners on
> `cal-rest-api.md` and `cal-pebble-codec.md` in this directory.

---

Author: haitch (h@ual.li) · Licence: Apache 2.0

This session advanced two threads on top of the xolu v0.13.1 checkpoint: a small
code change in the timeseries (`ts`) primitive, and a large design advance on the
`cal` scheduling primitive (design docs only, no `cal` code yet).

## Code changes (ts) — built, vetted, tested green

Closed the missing `DELETE` for a timeline *definition* (distinct from
`DELETE .../data`, which clears events but keeps the definition):

- `registry.delete()` — removes a timeline definition (reverses `define`).
- `PebbleStore.DeleteTimeline` — orchestrates the cascade in safe order
  (rollups → data → definition), gated on the existing `RollupCascadeDelete`
  policy (cascade-off refuses with `ErrTSRollupDestInUse` when rollups exist,
  leaving everything intact), honouring the timeline-0 root guard.
- `Store` interface entry; `HandleTSDeleteTimeline` handler using the house
  `errContains` idiom (409 cascade-conflict, 400 root, 404 unknown);
  route `DELETE /ts/tl/{timeline_id}`.
- In-memory **deleting marker** answering "what happens when a timeline is queried
  while being emptied/deleted": `get()` reports a marked timeline as not-found, so
  concurrent readers get a clean not-found instead of a confusing defined-but-empty
  read. In-memory only (a crash mid-delete leaves a normal retryable timeline),
  zero changes to the ~22 `reg.get` call sites, with `getForDelete` for the delete
  path and `markDeleting`/`unmarkDeleting` rollback.
- Tests: e2e `TestTSDeleteTimeline`, store-level `TestDeleteTimeline_StoreLevel`
  (reaches the cascade-off path the e2e server can't), and
  `TestDeleteTimeline_DeletingMarker` (concurrent reader under `-race`).

Self-check notes from the session (patterns to watch): `RollupID` formats with
`%s` not `%d` (vet caught it); `ErrTSRollupDestInUse` is a `Code` string, so use
`errContains(err, string(code))`, not `errors.Is`.

## Design changes (cal) — proposal docs only

`cal-rest-api.md` and `cal-pebble-codec.md` were substantially advanced. Settled:

- **Bookings are `def`-constructed** like rollups: `POST .../bookings/def`,
  `GET .../bookings/list`; calendar update is `PATCH` (matching `tl`). Path
  conventions reconciled with `ts`/`tl`/`rollup`.
- **Availability**: `free`/`busy` boolean complements; `q=capacity` returns the
  ternary `free`/`idk`/`busy` (`idk` = proposed-but-not-binding present) plus
  `capacity = 100 − confirmed%` and the raw `counts` so callers derive their own
  measures.
- **Optional participants** (renamed from "attendees"): confirmation drives
  capacity (a confirmed optional consumes, required or not); required-ness drives
  missed-reconciliation (optional no-show = nothing recorded).
- **Per-calendar policy fields**: `default_state` (proposed|binding),
  `optionality` (whole-booking|per-participant).
- **Codec**: fine plane = 5-min quanta, 288 bits = `[5]uint64` per UTC day, keyed
  by UTC-midnight `UnixNano` (the `ts` time encoding, floored by integer div, not
  a bitmask). Two planes (binding/proposed). One Pebble store per tenant.
- **Conversion invariant** (the load-bearing fact): coarse converts down to fine
  losslessly; equal-resolution calendars compare by bitwise AND; reconcile toward
  the finer grain; rollups (the lossy up-conversion) prune but never confirm.
- **Rollup**: one v1 level, 3-hour daypart (8 bits = 1 byte/day; one month = 31
  bytes in one read; backs `availability/{month}` directly). Daily deferred.
- **Per-calendar `delta`** (opt-in, default 0): a sub-quantum grid offset for
  calendars needing precise off-grid session origins; same-delta matches AND
  directly, mixed-delta coarsen to the daypart rollup and verify.
- **Entity binding**: one calendar → one entity (`entity_ref`); composition lives
  in the entity graph (a team is just an entity with a calendar; no special group
  construct; `kind` demoted to a label). Handle policy: `EntityNil` (0),
  `EntityTombstone` (MaxUint64), `EntityMaxValid` boundary; real handles allocate
  upward, sentinels reserve descending from the top; validation is one range check.
- **Testing strategy** recorded: TDD + property-tests-against-oracle for the pure
  bit layer; design-then-race-test for the stateful layer; `index == rebuild` as
  the global invariant.

## Open (not settled — flagged in cal-pebble-codec.md §6)

- Proposed-plane match semantics (binding-only vs binding-OR-proposed AND).
- Now-crossing / pyramid seal concurrency.
- Daily rollup and sealed-past/live-future store split (both scale-triggered,
  deferred with concrete triggers).
- The SQLite booking record (E-R) — understood, deliberately not the focus.
