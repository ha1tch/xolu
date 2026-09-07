# Client Completeness — Wave 16 Staged Plan (extends Wave 15)

Updated: 2026-08-19

Origin: xoluman's own seed-system request that produced Wave 15
("Wave of Mutilation," XOT209-213, closed v0.30.32). Reviewing what
that wave left unfinished or newly exposed surfaced three genuinely
different findings, not one undifferentiated backlog -- this document
stages them by what actually justifies doing the work, not just by
what's missing.

## Why this isn't "finish the API"

`docs/CLIENT_STAGE6_PLAN.md` already made a deliberate, principled
call about `ts`: declared the unread-side surface out of scope, in
writing, on the grounds that "stability declared over a surface
nobody exercises is a liability, not an asset," revisitable only when
a real consumer needs it. That reasoning doesn't stop being sound just
because Wave 15 later added two `ts` write methods for a narrow,
specific, consumer-articulated reason (xoluman's own open question
about whether writes were deliberately excluded).

This plan's own default position, going in, was to extend that same
declared-out-of-scope stance for `ts`'s remaining surface -- narrower
than "build everything," proportionate to what's actually been asked
for. **That recommendation was overridden directly: `ts` is now
explicitly in scope, fully, not deferred.** Recorded here plainly
rather than silently rewritten as if it were the plan's own idea from
the start -- the reasoning above is preserved because it's still the
right way to evaluate scope in general; this particular case is a
deliberate, named exception to it, not a refutation of it.

## Stage 1 — Tenant-summary blob correctness fix (not new scope, a bug)

`Summarize` (XOT213) reuses `ExportTenant`'s own authoritative table
list, which never captured a tenant's own uploaded blob content --
`blob.Store` is only ever used as the *destination* for a packaged
export, not as a source of tenant data being counted. A tenant with
real uploaded files and nothing else currently reports `Empty: true`.
`blob.Store` already has `List`, so this is closing a real answer with
existing infrastructure, not building anything new.

**Scope:** add a blob key count to `TenantDataSummary`/`TenantSummary`
(both the `pkg/tenantexport` struct and the wire type), sourced from
`bs.List("")`, folded into `Empty()`'s own check. Requires threading a
`*blob.Store` into `Summarize` (currently only takes `primaryDB` +
`basePath` + `tenantID`) -- a signature change, so every existing
caller (just the one handler today) gets updated in the same pass.

**Estimate:** 0.5 day, including a real test proving a blob-only
tenant now correctly reports `Empty: false`.

## Stage 2 — `loc` completeness (consumer-justified, not speculative)

Xoluman's own document asked generally for "typed request/response
structs and `Client` methods for `loc` write operations," not a
narrowly scoped subset -- and their own seed system's stated ambition
(seeding varied primitive data, not just plain locations) plausibly
reaches fences and patterns even though neither was named explicitly.
Real signal, not "coverage for its own sake."

**Scope, fourteen routes, all found by reading the complete route
table directly rather than assumed complete after Wave 15:**
- Fences: attach, list, get, delete, patch, reconcile (6) --
  `XOT214`'s own already-filed scope, unchanged.
- Subject lookups: `/loc/subjects/{entity}/{id}/position`,
  `/loc/subjects/{entity}/{id}/history` (2) -- newly found this pass,
  not in `XOT214`'s original filing.
- Geospatial queries: `/loc/contains`, `/loc/nearby` (2) -- also newly
  found, also not in the original filing.
- Patterns: def, list, get, delete (4).

**Estimate:** ~2 days total, reusing `LocSubject` and the existing
path-escaping helpers `loc.go`'s core set already established.
`XOT214` gets widened to the full fourteen-route scope rather than
left at its original six-route filing plus a "maybe."

## `ts` — five sub-stages, foundation first

Verified precisely against the real route table before staging
anything, not estimated from memory: 7 methods are covered today
(`TSListTimelines`, `TSGetTimeline`, `TSQueryRange` -- which hits
`POST /query/range`, confirmed directly, distinct from the GET-based
`/events` query variant below -- `TSRollupList`, `TSRollupGet`,
`TSAppend`, `TSBatchAppend`). 24 routes remain, grouped by what
actually depends on what, not by route-table order.

### Stage 3 — Timeline lifecycle + sync (foundation, must come first)

`provision`, `tl/def`, `tl/{id}` update, `tl/{id}` delete, `tl/{id}/
sync` get/on/off -- 7 routes. Ordered first among the `ts` stages
because rollup and aggregate work only make sense against timelines
that can actually be created, updated, and retired through the
client, not just appended to.

**Estimate:** 1.5 days -- the largest single `ts` stage, since timeline
definition needs care about which fields are creation-only versus
update-eligible (verify against `HandleTSDefineTimeline` vs
`HandleTSUpdateTimeline` directly, not assumed symmetric), and sync
on/off's own semantics haven't been read yet.

### Stage 4 — Query completeness

`GET /events` (the query-string-based alternative to the already-
covered `POST /query/range`), `GET /events/latest` -- 2 routes.

**Estimate:** 0.5 day.

### Stage 5 — Rollup completeness

`tl/{id}/rollup/def`, `.../rollup/parent`, `.../rollup/{id}` delete,
`.../rollup/{id}/run`, `.../rollup/{id}/status`, `/rollup/tree` -- 6
routes, building on `list`/`get` already covered. Bucket-duration and
late-window semantics are already partially understood from the
read-side work (`TSRollup`'s own doc comment); definition and run are
the genuinely new pieces.

**Estimate:** 1.25 days.

### Stage 6 — Aggregates

`/aggregate`, `/range_aggregate`, `/full_aggregate` -- only 3 routes,
but likely the most semantically complex request/response shapes in
this whole plan (grouping functions, time-bucket parameters) --
nothing about their real shape has been read yet, so this estimate
carries the most uncertainty of the five `ts` stages.

**Estimate:** 1.25 days.

### Stage 7 — Retention, stats, data lifecycle

`GET`/`PATCH /retention`, `GET /stats`, `GET /stats/{id}`, `DELETE
/tl/{id}/data`, `POST /tl/{id}/data/purge` -- 6 routes, more
admin/ops-flavored than the others, likely simpler per route.

**Estimate:** 1 day.

## Total and sequencing

Stage 1 → 2 → 3 → 4 → 5 → 6 → 7, in that order -- Stage 1 first
regardless of size (correctness over everything else queued), Stage 3
before Stages 4-7 (foundation before what depends on it), no other
hard ordering constraint between Stage 2 (`loc`) and the `ts` stages,
placed second as the smaller, quicker complete win before the larger
`ts` body of work begins.

**~8 days total** (0.5 + 2 + 1.5 + 0.5 + 1.25 + 1.25 + 1), at the same
rigor Wave 15 itself was held to throughout: every request/response
shape verified against real server source before being typed, every
method proven against a real running server via integration tests, not
`gofmt`'s syntax validation standing in for correctness. Pagination on
`obj/list`/`loc/list` (found during Wave 15's own review, zero
consumer pressure on it today) remains backlog, not staged here.

Register items filed at the start of each stage, matching Wave 15's
own practice -- not all thirty-nine routes' worth filed in advance of
work neither this document nor Wave 16 itself has begun.
