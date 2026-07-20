# Substrate-law audit against the existing tree — 2026-07-19 preflight

Scope: verify @C04a (guard locality), @C04b (finiteness), @C04c (meta
engine-inert) against every current implementation. The laws were
canonised after cal, ts, and meta shipped; assumption of compliance is
the class of guess the working agreement forbids.

## @C04a — Guard locality

**Verdict: compliant. Every extant guard reads and writes inside one
transaction against the same primitive's own tables.**

Evidence:

- **Cal lifecycle transitions (`SetStateFrom`)** — the substantive
  commitment guard. `pkg/cal/sqlitesource.go:208`: one `UPDATE …
  WHERE state=? AND … AND booking_id=?` with `RowsAffected` verdict.
  Read (state check) is the WHERE clause; write is the SET; commit
  is atomic by construction. Explicitly documented as T-34's fix
  and hardened on multi-core CI. Compliant.
- **Cal calendar creation (`sqlitesource.go:99–124`)** — the
  duplicate-check `SELECT COUNT(*)` and the `INSERT` share one
  `sql.Tx` opened at method entry, committed at the end. Compliant.
- **Cal cross-calendar `MatchCommit` (`pkg/cal/commit.go`)** — the
  documented "check all calendars feasible FIRST against the
  unmodified index, only then commit any booking" is the guard-
  locality pattern for a set: pre-check and writes share one Pebble
  batch plus one SQLite tx. Compliant.
- **Cascade delete stub (`pkg/server/handlers.go:1505`)** — T-41
  already covers this defect (referent discovery never appends). It
  does not violate guard locality (the empty cascade never reads
  cross-primitive derived state); it fails to enforce anything.
  Compliant with the *law*; register item covers the semantic gap.

No new register items owed.

## @C04b — Finiteness (forgetting is not editing)

**Verdict: two of three planes compliant; one gap exists and is
already the intended shape.**

- **ts** — `RetentionDays` per timeline with store default and expiry
  sweep (`pkg/timeseries/registry.go`). @C04b itself cites this as
  the shipped mechanism. Compliant.
- **cal** — no retention machinery in `pkg/cal`. The occurrences of
  "prune" refer to bitmap algebra (impossibility rollups), not
  temporal retention. @C04b acknowledges this and names its fix
  (sealed periods drop whole beyond policy age) as future work
  tied to bal's construction. This is design intent, not a defect.
- **graph** — no retention on nodes/edges. As a derived index over
  entities, not a chronicle plane, @C04b does not govern it; nodes
  live and die with their entities.
- **bal** — not yet implemented; @C04b's prefix-collapse is written
  into the bal proposal (@B05).

Findings: one *documented* future task (cal periodic-retention),
which the plan will not act on until a consumer demands it. No new
register items.

## @C04c — Meta is engine-inert

**Verdict: compliant. No engine code path reads `entity_meta` to
decide behaviour.**

Evidence:

- Only reader of `entity_meta` outside handlers/tests:
  `pkg/storage/schema_browser.go:131` — but the `GetMeta` there is
  a call into `queryfy.BaseSchema.GetMeta`, i.e. **schema-definition
  metadata**, not the `/meta` per-subject sidecar. Different object,
  same name. Not a violation.
- The `meta` HTTP surface (`v2_meta_handlers.go`) is consumer-only:
  reads and writes flow to/from application clients. The store's
  own expiry sweeper reads meta rows to *delete expired ones*, which
  is meta managing itself, not an engine consuming meta.
- `pkg/tenant/registry.go` uses `RetentionDays` from ts's own
  registry — engine consumption of primitive config, not meta.

No new register items.

## Summary

Three laws, zero new register items, zero unrecorded violations.
The audit is the working agreement's §3.1 discipline applied to the
substrate laws' own claims: verified, not assumed. Preflight
part (b) complete; wave 0 can open.
