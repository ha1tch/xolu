# The `loc` REST API — wire surface and error codes

> **Reconciliation status (added 2026-08-01, against the v0.21.2
> checkpoint).** This is the banner the header immediately below
> predicted by name — `/loc` shipped as wave 9 (T-115–T-118,
> `CHANGELOG.md` v0.21.0/v0.21.1/v0.21.2), and this document's own
> earlier bet ("expect this to earn a reconciliation banner the day
> implementation reveals a narrower or different shape") paid off
> literally. What actually shipped narrower or different, checked
> directly against `pkg/server/v2_loc_handlers.go` and
> `pkg/loc/errors.go`:
>
> - **§2, Fences — the headline divergence.** This document's own
>   revision (below) specifies entity-`(kind, key)` fence identity.
>   v1 shipped a bare, caller-chosen `fence_id` instead — `subject` is
>   accepted on the wire (the field name survived) but used as an
>   opaque string with no entity resolution, no existence check,
>   neither `XOLU-LOC005` nor `006` returned by anything. The
>   `/loc/fences/{kind}/{key}` route also survived, but `kind` is
>   parsed and then never read by either handler. §2 below is marked
>   up in place with the precise, code-verified correction rather than
>   rewritten, so the intended design stays legible alongside what v1
>   actually does.
> - **§2, self-anchoring — confirmed, not contradicted.** `center:
>   {"self": true}` is real in the shipped wire format and is checked
>   — then explicitly refused, citing the `/obj` dependency this
>   document already names. This part of the original draft held.
> - **Error table — two real, code-confirmed gaps**, both flagged by
>   the implementation's own comments, not found by inspection here:
>   no code existed for duplicate `location_id`/`fence_id` (shipped as
>   `XOLU-LOC014`/`015`), and none exists at all for `ValidationError`'s
>   generic 400 class. Added below.
> - **What held exactly as specified**, worth naming so this doesn't
>   read as entirely corrections: §0's two-write-path distinction, the
>   report/move split, the full fifteen-endpoint index, and the general
>   JSON body shapes throughout §1/§3/§4 all match the shipped surface.
>
> Full account, including the wrong turns and why the entity-composition
> model is still correct as a target even though v1 didn't build it:
> `loc-00-addendum.md`.

`loc-01` of the loc document series: see `loc-00-design.md` for the
model and doctrine this surface implements, and `loc-02-implementation.md`
for the staged build. This document specifies request/response bodies
field-by-field and reserves the `XOLU-LOC` error-code prefix. ~~Status:
proposal, not implemented~~ — **implemented, wave 9, v0.21.0. See the
banner above for what shipped narrower than specified below.**

Prepared: 2026-07-31 (against v0.20.0; revised same day — §2's fence
definition rewritten from caller-chosen `fence_id` to entity-subject
addressing, matching `loc-00-design.md`'s own §5 revision. The rest of
this document is unchanged from its first pass.)

---

## 0. Two write paths, not one — read this before anything else

loc has two ways to change a subject's position, and they resolve
against different guard-bearing state because they resolve against
different *kinds* of shape:

- **`move`** — explicit tree-leaf reassignment, by location id. No
  geometry involved: a leaf's membership in its ancestors is identity
  (prefix parentage, §3a of `loc-00-design.md`), not a geometric test.
  This is the only way a subject's canonical tree-leaf position ever
  changes.
- **`report`** — a raw coordinate. Location nodes carry a `Placement`
  (§3b of `loc-00-design.md`) — an offset-and-rotation transform, not
  a boundary shape — so a coordinate cannot be tested against a leaf
  the way it can against a fence, which does carry real `Geometry`
  (§4). **`report` therefore only ever resolves fence membership. It
  never sets or changes tree-leaf assignment.**

A subject can be tree-assigned (via `move`), fence-tracked (via
`report`), both, or neither. Nothing in this surface silently promotes
a `report` into an implicit `move`, and nothing infers a fence
crossing from a `move` alone unless the destination leaf happens to be
tree-aligned with a fence (§5 of `loc-00-design.md`), in which case
that fence's membership follows the free tree walk automatically — no
separate `report` needed for that specific fence.

---

## 1. Locations — the containment tree

Declare-at-known-id, like `bal`'s accounts and `cal`'s calendars: the
caller names the id, `def` constructs it, and the constructed node is
addressed directly at `loc/{location_id}` — no separate definition
population.

```
POST   /api/v2/loc/def                 Define a location node (construct; caller-named id)
GET    /api/v2/loc/list                List location nodes (optionally filtered by parent_id)
GET    /api/v2/loc/{location_id}       Fetch one
PATCH  /api/v2/loc/{location_id}       Update name/placement/capacity (not assignments)
DELETE /api/v2/loc/{location_id}       Remove (cascade rules below)
```

**`POST /api/v2/loc/def`** — an interior (non-postable) node:
```json
{
  "location_id": "site-mvd",
  "parent_id": null,
  "name": "Montevideo Site",
  "postable": false,
  "placement": {
    "offset_x": 0.0, "offset_y": 0.0, "offset_z": 0.0, "rotation": 0.0,
    "anchor": { "lat": -34.9011, "lon": -56.1645, "alt": 15.0, "true_north": 0.0 }
  }
}
```

— and a postable leaf beneath it, with a capacity ceiling:
```json
{
  "location_id": "site-mvd/bldg-a/floor-3/room-204",
  "parent_id": "site-mvd/bldg-a/floor-3",
  "name": "Room 204",
  "postable": true,
  "capacity": 12,
  "placement": { "offset_x": 4.2, "offset_y": 0.0, "offset_z": 9.5, "rotation": 0.0 }
}
```

- `location_id`: caller-supplied external string id (§11a of
  `loc-00-design.md`'s two-identity split). Path-structured by
  convention, not by requirement — the parent-chain relationship comes
  from `parent_id`, never parsed out of the string.
- `parent_id`: `null` only for a tree root. A root **must** carry an
  `anchor` (below) or every node beneath it is relative-only with no
  absolute position ever resolvable — refused otherwise
  (`XOLU-LOC010`).
- `postable`: defaults `true`. An interior summary node (§3a) must set
  it `false` explicitly — subjects can never be assigned to it, and
  `capacity` is invalid on a non-postable node (`XOLU-LOC011`).
- `capacity`: optional integer ceiling. Present only on a `postable`
  node. Absent means unlimited — no guard, no count maintained.
- `placement`: required. `offset_x/y/z` (metres, relative to the
  parent's frame) and `rotation` (radians about Z) are always present;
  `anchor` is present only where this node is where a subtree meets
  the real world (typically a site) — `{lat, lon, alt, true_north}`,
  WGS84. Mixing anchors from different real-world references within
  one tree is accepted but returns a `warnings` array in the response,
  never a hard refusal (§3b of `loc-00-design.md`'s documented BIM
  caution) — **not populated in v0.21.0**, see §2's own note on this
  same field for the shipped fence-geometry case; the gap is identical
  here.

Response `201` echoes the stored record. The internal dense `uint32`
key never appears on the wire, at create or anywhere else — satisfying
§4d/§11a of `loc-00-design.md` by construction, not by convention
someone has to remember.

**`PATCH`** — `name`, `placement`, and `capacity` are mutable.
`postable` and `parent_id` are not: changing whether a node can hold
subjects, or where it sits in the tree, is a structural move with its
own admission questions (does a newly-postable node inherit a
capacity default? does re-parenting orphan a placement chain mid-air?)
that this surface deliberately does not open in v1. Reparenting a
subtree, if ever needed, is a `loc-02-implementation.md` non-goal
until a real need names it.

**`DELETE`** — refuses (`409`, `XOLU-LOC012`) if the node currently
holds any assigned subject, full stop, regardless of `force` — silently
vacating a subject's canonical position is a correctness violation,
not a convenience, and no flag overrides it. Refuses (`409`,
`XOLU-LOC013`) if the node has children, unless `?force=true`, which
cascades to remove *empty* descendants only; any occupied descendant
anywhere in the subtree still blocks the whole delete with
`XOLU-LOC012` and reports which one.

---

## 2. Fences

**Revised in this pass:** a standalone fence's identity is an entity it
composes `loc.fence` capability onto (`loc-00-design.md` §5, revised),
not a freely caller-chosen `fence_id` — the shape originally drafted
here and now superseded. A tree-aligned fence is unaffected: its
identity is still the location node it coincides with, addressed
exactly as before.

> **Shipped narrower, 2026-08-01 — checked directly against
> `pkg/server/v2_loc_handlers.go`, not assumed from the route shape.**
> The endpoints below, the `subject` field name, and the `{kind}/{key}`
> path all shipped exactly as written. ~~What didn't ship is
> everything the shape implies~~: `handleLocFenceAttach` does
> `fenceID := req.Subject` and stops — no entity lookup, no existence
> check, no `XOLU-LOC005`/`006` (neither is returned by anything in
> v1). `handleLocFenceGet`/`Delete` read only the `key` segment;
> `kind` is routed and then never referenced. `subject` today is a
> bare opaque string wearing this document's field name, not the
> `(kind, key)` entity address specified below — `RESOLVED.md`'s T-115
> confirms this in the implementers' own words ("fences — bare
> identity — real geometry is Stage 3"), a conscious v1 narrowing
> pending `/meta` wiring, not an oversight. The self-anchoring
> field (`center.self`, below) is the one part of this section that
> shipped exactly as specified, including the check that rejects it.
> Read every `subject`/`(kind, key)` reference below as the intended
> target, not the current wire contract.

```
POST   /api/v2/loc/fences/attach                    Compose loc.fence onto an existing subject
GET    /api/v2/loc/fences/list                      List fences
GET    /api/v2/loc/fences/{kind}/{key}              Fetch one (standalone) or {location_id} (tree-aligned)
DELETE /api/v2/loc/fences/{kind}/{key}               Detach
```

**`POST /api/v2/loc/fences/attach`** — tree-aligned, with a capacity
guard (unchanged in shape from the earlier draft; tree-aligned fences
were never independently identified in the first place):
```json
{
  "aligned_to": "site-mvd/bldg-a/yard/dock-zone",
  "geometry": {
    "type": "Polygon",
    "coordinates": [[[-56.1652,-34.9015],[-56.1648,-34.9015],
                      [-56.1648,-34.9011],[-56.1652,-34.9011],
                      [-56.1652,-34.9015]]]
  },
  "capacity": 3
}
```

— standalone, composed onto a dedicated place-entity (a jurisdiction
or designated zone with no position of its own — `loc-00-design.md`
§5's first composition case):
```json
{
  "subject": "zones:svc-radius-hq",
  "geometry": {
    "type": "circle",
    "center": { "lat": -34.9011, "lon": -56.1645 },
    "radius_m": 20000
  }
}
```

— standalone, **self-anchored** to an entity that already composes a
position (`loc-00-design.md` §5's second composition case — the fence
references its own subject's resolved position rather than a fixed
center):
```json
{
  "subject": "sites:hq",
  "geometry": {
    "type": "circle",
    "center": { "self": true },
    "radius_m": 20000
  }
}
```
`center: {"self": true}` resolves through `subject`'s own `/obj`
position (or `/loc` tree-anchor) at query time, live — per
`loc-00-design.md` §5b, this inherits the non-versioned-placement
limitation directly: a self-anchored fence's every membership test
depends on wherever `subject` resolves to *right now*, with no
point-in-time equivalent.

- `subject`: required for a standalone fence, the `(kind, key)`
  address of the entity it composes onto — the same convention `/obj`
  and `/meta`'s namespaced subjects use. `XOLU-LOC005` if `subject`
  does not resolve to a real entity; `XOLU-LOC006` if the subject
  already has `loc.fence` composed. ~~As specified.~~ **As shipped in
  v1:** `subject` is taken as a bare string and used directly as the
  fence's identity — no resolution against any entity registry, so
  `XOLU-LOC005`/`006` are reserved codes with no implemented path that
  returns them yet, not live refusals a caller will ever see today.
- `geometry`: **always absolute WGS84** unless `center: {"self": true}`
  is used, in which case it is resolved dynamically rather than stored
  fixed. Regardless of `aligned_to` or self-anchoring, `Placement` (§1,
  above) is the relative-transform concept for tree nodes; `Geometry`
  is always the real-world shape a fence tests a coordinate against,
  per §4a's `Contains(lat, lon)` signature. A tree-aligned fence still
  carries its own geometry — membership just additionally follows the
  free tree walk instead of requiring the exact test, per §5 of
  `loc-00-design.md`.
- Two `geometry.type` values: `"Polygon"` — plain GeoJSON (RFC 7946),
  simple (non-self-intersecting) only, rejected at write time
  otherwise (`XOLU-LOC020`) — and `"circle"` — loc's own typed field,
  never a GeoJSON polygon approximation on the wire (§4d). An
  axis-aligned rectangle is still submitted as a 4-vertex `"Polygon"`;
  the O(1) bounding-box fast path (§4c) is an internal detection, not
  a wire-level type.
- `aligned_to`: optional `location_id`. When present, no `subject` is
  needed or accepted — a tree-aligned fence's identity is the location
  node itself. The stored geometry is still used for `nearby`/export
  and for the exact test §7b requires whenever a guard decision is
  actually being made (the tree walk is what says "check this fence,"
  never what decides membership on its own once a guard is live).
- `capacity`: optional, exactly §3d's leaf mechanism (§5a). Valid on
  any fence, tree-aligned or standalone, self-anchored or fixed.

Response `201` echoes the stored record, plus a `warnings` array
(never a hard failure) if the polygon degenerates to zero area or has
fewer than three effective vertices after simplification — informative
only, since a degenerate fence is legal, just useless. **Shipped
narrower, 2026-08-02:** the `Warnings []string` field exists in v0.21.0's
response struct (`pkg/server/v2_loc_handlers.go`), but nothing
populates it — checked directly, no code path sets it. The field is a
wire placeholder; the degenerate-polygon and mixed-CRS-anchor
(§1, above) detection logic itself was never built.

**`DELETE`** detaches `loc.fence` from the subject — no cascade
refusal. Fence membership is derived (§3c), never a subject's
canonical fact the way a leaf assignment is, so there is nothing to
orphan. Historical crossing facts already written to the journal and
the `ts`-shaped feed (§9) are untouched; they describe events that
happened, not a live reference to the fence. This is distinct from
retiring or detaching the underlying entity itself, which is that
entity's own concern, not this endpoint's.

---

## 2a. Fence-type patterns

`loc-00-design.md` §5d's mechanism, in wire form — the same shape
`obj-01-rest-api.md` §4a specifies, applied a fourth time. A pattern
is not a fence or a location — addressed by plain `(tenant, id)`, no
subject convention:

```
POST   /api/v2/loc/patterns/def     Draft a fence-capacity pattern
GET    /api/v2/loc/patterns/list    List patterns
GET    /api/v2/loc/patterns/{id}    Fetch one
DELETE /api/v2/loc/patterns/{id}    Delete
```

```json
{ "name": "loading-dock-std", "capacity": 3 }
```

A fence's `attach` (§2) or a location's `def` (§1) gains an optional
`pattern` field, mutually exclusive with inline `capacity`
(`XOLU-LOC022`, mirroring `obj-01-rest-api.md`'s `XOLU-OBJ013` shape):

```json
{ "subject": "zones:dock-9", "pattern": "loading-dock-std" }
```

A cloned fence's `GET` surfaces `pattern`, `pattern_id`, and a computed
`pattern_deleted`. A pattern changing later never retroactively
touches already-cloned fences, for the identical reason `obj-00-
design.md` §7a's patterns and a running `fsm` machine don't. `loc`
doesn't currently offer `obj`'s `extract` (drafting a pattern from an
existing fence's current fields) — every `loc` pattern is drafted from
scratch via `def`; a real need for `extract` here hasn't been named
yet, and it isn't added speculatively.

---

## 2b. Fence geometry updates and reconciliation

`loc-00-design.md` §5c's mechanism, in wire form — designed, and not
yet built in shipped code (`loc-00-design.md` §15 carries this status
precisely; specifying the wire shape now doesn't change that).

```
PATCH  /api/v2/loc/fences/{kind}/{key}            Update a fence's geometry
GET    /api/v2/loc/fences/{kind}/{key}/reconcile  Advisory drift view
```

**`PATCH`** — same geometry validation as `attach` (§2): rejected
outright on self-intersection (`XOLU-LOC020`).
```json
{
  "geometry": {
    "type": "Polygon",
    "coordinates": [[[-56.1651,-34.9014],[-56.1649,-34.9014],
                      [-56.1649,-34.9012],[-56.1651,-34.9012],
                      [-56.1651,-34.9014]]]
  }
}
```
Never touches `loc_fence_capacity.count` or `loc_fence_membership` —
both are guard-bearing; only the fence's own stored geometry changes.

**`GET .../reconcile`** — read-only, `chronicle.RebuildOracle`-shaped:
re-tests every subject currently in `loc_fence_membership` for this
fence against its *current* geometry, from source, the same way
`FenceMembershipFoldOracle` recomputes from the journal rather than
trusting a cached value.
```json
{
  "fence_id": "dock-zone",
  "recorded_count": 3,
  "observed_count": 2,
  "drift": [
    { "subject_ref": "trucks:1", "recorded": "member", "observed": "outside_new_boundary" }
  ]
}
```
Advisory only — never writes `loc_fence_capacity.count` or
`loc_fence_membership`. Fixing drift, when wanted, is an ordinary
`report` call for the affected subject, the same CAS path every other
capacity change already uses; this endpoint only ever says where to
look.

---

## 3. Position — report and move

**`POST /api/v2/loc/report`** — §8a: writes only on change.
```json
{
  "subject": { "type": "REF", "entity": "vehicles", "id": 4471 },
  "point": { "lat": -34.9013, "lon": -56.1638, "alt": 12.0 },
  "reported_at": "2026-07-31T14:22:05Z"
}
```
Response `200`, nothing changed — no journal entry, no event, no `ts`
write, per §8a:
```json
{ "changed": false, "fences": ["dock-zone"] }
```
Response `200`, a crossing occurred:
```json
{
  "changed": true,
  "fences": ["dock-zone", "zones:svc-radius-hq"],
  "entered": ["dock-zone"],
  "exited": []
}
```
Response `409`, an entered fence is at capacity — the report is
refused outright, not partially applied; the subject's fence set is
unchanged:
```json
{ "error": { "code": "XOLU-LOC001", "message": "fence at capacity",
             "status": 409, "fence_id": "dock-zone" } }
```
No `leaf` field appears anywhere in this response — per §0, `report`
never resolves tree-leaf membership.

**`POST /api/v2/loc/move`** — explicit leaf reassignment, bypassing
coordinate resolution entirely (§0):
```json
{
  "subject": { "type": "REF", "entity": "vehicles", "id": 4471 },
  "to": "site-mvd/bldg-a/yard/dock-zone"
}
```
Response `200`:
```json
{
  "moved": true,
  "leaf": "site-mvd/bldg-a/yard/dock-zone",
  "fences": { "entered": ["dock-zone"], "exited": [] }
}
```
`fences.entered`/`exited` here come only from tree-aligned fences the
move's destination and origin do or don't coincide with (§5) — a
`move` never runs a geometric `Contains` test, because it never
resolves a coordinate at all; a standalone fence's membership for a
tree-assigned subject is only ever established by that subject also
being `report`-tracked (§0).

Response `409`, destination leaf at capacity — the universal shape
below, one occupant reported, the move refused, origin untouched:
```json
{ "error": { "code": "XOLU-LOC002", "message": "leaf at capacity",
             "status": 409, "location_id": "site-mvd/bldg-a/yard/dock-zone" } }
```

**`GET /api/v2/loc/subjects/{entity}/{id}/position`** — current
canonical state:
```json
{
  "subject": { "type": "REF", "entity": "vehicles", "id": 4471 },
  "leaf": "site-mvd/bldg-a/yard/dock-zone",
  "fences": ["dock-zone"],
  "last_report_point": { "lat": -34.9013, "lon": -56.1638, "alt": 12.0 },
  "as_of": "2026-07-31T14:22:05Z"
}
```
`leaf` is `null` for a subject that has only ever been `report`-tracked
and never `move`d. `last_report_point` is `null` for a subject that
has only ever been `move`d and never `report`ed.

**`GET /api/v2/loc/subjects/{entity}/{id}/history`** — the movement
journal (§8 of `loc-00-design.md`), paginated, newest first:
```json
{
  "entries": [
    { "at": "2026-07-31T14:22:05Z", "kind": "move",
      "from": "site-mvd/bldg-a/yard", "to": "site-mvd/bldg-a/yard/dock-zone" },
    { "at": "2026-07-31T09:03:11Z", "kind": "report",
      "entered": ["zones:svc-radius-hq"], "exited": [] }
  ],
  "next_cursor": null
}
```

---

## 4. Containment reads

Pure reads, never a write, never subject-scoped — for "what would this
point be inside" questions independent of any tracked subject.

```
GET /api/v2/loc/contains?lat={lat}&lon={lon}
GET /api/v2/loc/nearby?lat={lat}&lon={lon}&radius_m={radius_m}
```

This revises §11's original sketch, which named `GET
/api/v2/loc/{id}/contains` — a coordinate lookup has a point, not a
location id, so query parameters are the correct shape; the sketch
predates this document and is superseded by it.

**`contains`** response — the same closure shape `report` and `move`
already use:
```json
{ "fences": ["dock-zone", "zones:svc-radius-hq"] }
```
No `leaf` field here either, and for the same reason as `report` (§0)
— an arbitrary coordinate can no more resolve a tree leaf than a
reported one can.

**`nearby`** response — §6c's SQL-plane bounding-box pre-filter
followed by the exact test, advisory ordering by distance:
```json
{
  "locations": [
    { "location_id": "site-mvd/bldg-a/yard/dock-zone", "distance_m": 12.4 }
  ],
  "fences": [
    { "fence_id": "dock-zone", "distance_m": 0.0 },
    { "fence_id": "zones:svc-radius-hq", "distance_m": 0.0 }
  ]
}
```
`distance_m` is `0.0` when the point is inside the shape (fences) or
at the node's own placement-derived point (locations); this is a read
convenience, never a guard input (§7d).

---

## Complete endpoint index

```
# Locations
POST   /api/v2/loc/def
GET    /api/v2/loc/list
GET    /api/v2/loc/{location_id}
PATCH  /api/v2/loc/{location_id}
DELETE /api/v2/loc/{location_id}

# Fences
POST   /api/v2/loc/fences/attach
GET    /api/v2/loc/fences/list
GET    /api/v2/loc/fences/{kind}/{key}
DELETE /api/v2/loc/fences/{kind}/{key}

# Patterns
POST   /api/v2/loc/patterns/def
GET    /api/v2/loc/patterns/list
GET    /api/v2/loc/patterns/{id}
DELETE /api/v2/loc/patterns/{id}

# Fence geometry updates and reconciliation
PATCH  /api/v2/loc/fences/{kind}/{key}
GET    /api/v2/loc/fences/{kind}/{key}/reconcile

# Position
POST   /api/v2/loc/report
POST   /api/v2/loc/move
GET    /api/v2/loc/subjects/{entity}/{id}/position
GET    /api/v2/loc/subjects/{entity}/{id}/history

# Containment reads
GET    /api/v2/loc/contains
GET    /api/v2/loc/nearby
```

21 endpoints. All `/api/v2/loc/...` paths are tenant-scoped the
standard way (`/api/v2/tenant/{tenant_id}/loc/...`), per `API_V2.md`
§"Multi-tenancy" — loc introduces no tenancy convention of its own;
that question belongs to `loc-02-implementation.md`'s storage layer,
not to this surface.

---

## What stays off the surface (so it doesn't creep back on)

- **No entry/exit authorisation** — §7c of `loc-00-design.md`,
  formally signed off. `move` and `report` check capacity only; "who's
  allowed" is the caller's problem.
- **No routing or wayfinding** between locations — §13's non-goal,
  restated here so it doesn't reappear as an unpathed convenience
  method on `move`.
- **No `report` resolving tree-leaf membership** — §0, the load-bearing
  distinction this whole document is built around. If a future
  revision changes this, it changes §0 explicitly, not by adding a
  quiet `leaf` field to `report`'s response.
- **No reparenting endpoint** — `PATCH` excludes `parent_id` (§1); a
  structural tree move is out of scope until a real need names it.
- **No CAD/IFC import or floor-plan rendering** — §13's non-goal;
  `placement` and `geometry` are consumed as submitted, never derived
  from an uploaded file.

---

## Error codes — `XOLU-LOC` reserved, first pass

Following `ERROR_CODES.md`'s convention (`error.code` in a stable
envelope, `error.message` free text, `error.status` mirroring HTTP).
Unlike `bal`'s table, which was settled early and stayed close to
final, this list should be read the way `cal`'s history actually
went: `cal-rest-api.md` shipped with no formal error-code table at
all, and `XOLU-CAL001`–`007` were only hardened during and after
implementation (`CHANGELOG.md`, v0.14.7–v0.14.13). This table exists
so loc doesn't repeat that gap from zero, not because every code below
is guaranteed to survive contact with `loc-02-implementation.md`'s
Stage 1.

| Code | Meaning |
|---|---|
| `XOLU-LOC001` | `report`/`move` refused: an entered fence is at capacity |
| `XOLU-LOC002` | `move` refused: destination leaf is at capacity |
| `XOLU-LOC003` | Unknown `location_id` |
| `XOLU-LOC004` | Unknown fence (bad `location_id` for a tree-aligned fence, or unresolvable `(kind, key)` for a standalone one) |
| `XOLU-LOC005` | Unknown subject — entity ref does not resolve (used both for `report`/`move`'s own subject and for `fences/attach`'s `subject`, revised in this pass) |
| `XOLU-LOC006` | `fences/attach` refused: subject already has `loc.fence` composed |
| `XOLU-LOC010` | A tree root (`parent_id: null`) was defined without an `anchor` |
| `XOLU-LOC011` | `capacity` set on a non-`postable` node |
| `XOLU-LOC012` | Delete refused: node (or a descendant) currently holds an assigned subject |
| `XOLU-LOC013` | Delete refused: node has children and `force` was not set |
| `XOLU-LOC020` | Fence geometry rejected: self-intersecting polygon |
| `XOLU-LOC021` | Coordinate field rejected: non-finite float (§4e's numerics doctrine) |
| `XOLU-LOC022` | `def`/`attach` refused: more than one of `capacity`/`pattern` set |
| `XOLU-LOC014` | `def` refused: `location_id` already defined — **added 2026-08-01**, shipped in v0.21.1, found by adversarial testing rather than written to spec (`pkg/loc/errors.go`'s own comment: "no XOLU-LOC code in loc-01-rest-api.md's own table covers this case"). `bal`'s `DefineAccount` had the identical gap, confirmed and fixed the same session — worth treating as a systemic pattern for any future `def`-shaped endpoint, not a `loc`-specific miss. |
| `XOLU-LOC015` | `fences/attach` (or `def`, pending the naming reconciliation above) refused: `fence_id` already defined — same finding and same session as `XOLU-LOC014`. |

**Reserved, not currently live — added 2026-08-01.** `XOLU-LOC005`/
`006` above are specified against the entity-composition fence model
this document describes; per §2's own reconciliation banner, that
model didn't ship in v1. Neither code has an implemented path that
returns it today — kept in the table because they're the correct
codes for when `/meta` wiring lands the real thing, not removed for
now being aspirational.

**A confirmed, real gap this table never had a code for, found by the
implementation, not by review here.** `pkg/loc/errors.go`'s
`ValidationError` is a generic 400 covering malformed GeoJSON,
unsupported geometry types, and empty required fields — its own
comment states plainly: *"a generic 400 for malformed input that has
no XOLU-LOC code reserved for it in loc-01-rest-api.md's own table...
a real gap in the table, not glossed over."* Two of its concrete
triggers are worth naming specifically rather than left generic,
since both were found by adversarial testing after this table was
first written: a negative-radius circle (`Contains` uses
`distance <= radius`, so a negative radius silently creates a fence
nobody could ever enter rather than erroring, unless refused at write
time — v0.21.1) and a GeoJSON polygon with interior rings/holes (RFC
7946 §3.1.6 — a normal valid structure this design was always meant
to refuse per `loc-00-design.md` §4b, but which an earlier
implementation silently accepted with the hole dropped before this
was caught — v0.21.1). No numbered code assigned to any of these yet;
they are correctly 400s today, just not distinguishable from each
other by code alone.

Deliberately not yet coded, pending `loc-02-implementation.md`'s own
resolution: mixed-CRS-anchor warnings (§1, above) are non-fatal by
design and may never need a code at all, only the `warnings` array
already shown.
