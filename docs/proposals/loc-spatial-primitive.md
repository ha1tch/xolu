# loc — The Spatial Primitive (proposal)

> **Reconciliation status (added 2026-07-31):** This single-file
> version is superseded by a revised, split doc series:
> `loc-00-design.md` (model and doctrine, revised against v0.20.0 —
> closes three of this document's own §15 open items: subject registry
> scope, standalone-fence capacity mechanics, and §7c's formal
> treatment), `loc-01-rest-api.md` (wire surface and error codes, new —
> no equivalent existed in this version), and `loc-02-implementation.md`
> (staged build plan, new). `loc-03-spatial-index.md` is reserved in
> the new series, not yet written. Nothing here was implemented before
> being superseded — this is a proposal-to-proposal revision, not a
> proposal-to-shipped-code reconciliation (contrast `cal-rest-api.md`'s
> own banner, which is the latter). Retained for design-history value —
> anyone designing against `loc` today should start with `loc-00-design.md`.

Updated: 2026-07-31 (checked against v0.19.3; originally written
against v0.19.2 — see §6b and §9b for what changed)
Status: proposal — not scheduled. Successor to
`loc-spatial-primitive-inception.md`, which resolved the model's shape;
this document is the design. First native consumer of bal's §3a
hierarchical-account machinery for a second purpose (containment rather
than conservation) and of the chronicle substrate's guard-locality law
applied to geometry rather than balances. No register items exist until
execution is decided.

## 1. What loc is

A substrate primitive for **where things are**: a containment hierarchy
of locations, each with a placement relative to its parent and,
optionally, an absolute real-world anchor; some of them fenced regions
with guarded entry; against which subjects (typically entities) hold
one canonical position and a permanent movement history — with
capacity and exclusivity enforced at write time, the same way bal
enforces bounds and cal enforces occupancy.

## 2. Problem

An entity field with a `REF` to a "locations" entity already exists
today and costs nothing. What it cannot do is what every commitment
primitive exists to do: refuse a write that violates a rule. Nothing
stops two exclusive assets from both holding the same slot; nothing
enforces a room's capacity; nothing computes whether a point sits
inside a fence; nothing fires an event when a boundary is crossed. This
is bal's problem statement (§2 of `bal-conservation-primitive.md`)
transplanted from quantity to space — the check-then-act race
reimplemented at the application layer, forever, for every consumer,
because the substrate offers no atomic way to say "this subject is now
here, and only here, and there was room."

## 3. Model

**3a. Structure — the containment tree.** Adopted from bal §3a outright,
unchanged: decimal or path-structured ids, parentage by prefix, a
`postable` flag distinguishing leaf locations (subjects can actually be
placed here) from interior summary nodes (occupancy is a derived
rollup of the subtree, never posted to directly). A site, a building, a
floor, a room, a bin, a cost centre — all nodes in one tree, some
physical, some organisational.

**3b. Placement — one relative transform per node, not two coordinate
types.** Checked against established practice rather than invented:
IBM TRIRIGA and Maximo, the two most-cited names in this exact
CMMS/EAM/space-management space, do not build their own coordinate
scheme. They derive their location hierarchy from CAD/BIM data via a
Revit/IFC connector and layer indoor positioning on top of that same
hierarchy [Naviam; Quantum Strides — see References]. The standard
underneath is IFC's placement model (`IfcLocalPlacement`), and it
resolves loc's coordinate question directly: every spatial element
carries a placement — an offset plus a rotation — expressed relative to
its *parent's* placement, not in a disconnected frame of its own. A
storey's placement is relative to its building; a building's is
relative to its site. The chain terminates wherever a node is placed
*absolutely*: typically a site, carrying a real-world anchor (WGS84
longitude, latitude, elevation) and a true-north rotation connecting
the whole relative chain to the outside world [buildingSMART, IFC4.3
`IfcLocalPlacement`/`IfcSite` — see References].

loc adopts this outright. A location node's placement is:

```go
type Placement struct {
    OffsetX, OffsetY, OffsetZ float64 // relative to parent's frame; metres
    Rotation                  float64 // radians, about Z
    Anchor                    *GeoAnchor // nil unless this node is georeferenced
}

type GeoAnchor struct {
    Lat, Lon, Alt float64 // WGS84
    TrueNorth     float64 // radians, orientation of this node's local axes
}
```

Most nodes carry no `Anchor` — their placement is relative only (a
bin's position in its aisle). A node with an `Anchor` is where a
subtree meets the real world (a site, typically) — exactly `IfcSite`.
**Absolute position for any node is composing its placement chain up to
the nearest ancestor carrying an `Anchor`.** No separate "local vs geo"
mechanism, no unscoped transform to design later: the §3a tree already
adopted from bal *is* the transform hierarchy, once each edge carries a
`Placement` instead of a bare parent pointer.

One documented caution carried over directly: mixing subtrees anchored
to *different* real-world references in one tree is technically
representable but is flagged repeatedly in BIM practice as something to
avoid [thinkmoult.com — see References]. loc does not forbid it
structurally, but a validation warning on write is worth having from
day one rather than discovering the failure mode in production.

**3c. Assignment — one canonical position, apparent multiplicity is
derived.** A subject's position is a single fact: which leaf node it
currently occupies (or, for a subject tracked purely by coordinates
with no tree membership, a raw geo point). "Where is asset #4471" is
never a set of independently-asserted facts. A vehicle at a point is
simultaneously within its country, its department, its jurisdiction,
its parking lot — but that is a **derived containment closure** over
the one position, not five separate writes: where the tree nests
cleanly, closure is a free parent-chain walk (exactly bal's §3a
subtree-sum pattern); where fences are standalone and possibly
overlapping, closure needs an actual geometric test per fence (§5).

A "move" writes exactly one fact — the subject's new canonical position
— and the engine derives which locations and fences that position now
falls inside. This is fsm-shaped: current state, changed by one
transition, logged in full.

**3d. Capacity — bal-shaped, and where rollups and guards genuinely
differ.** How many subjects a leaf will hold is a bounded count,
incremented on entry, decremented on exit, refused when it would
exceed a ceiling — the same floor/ceiling admission guard as a bal
account. Ancestor-level occupancy (how many vehicles in the whole
department) is a **free derived rollup** the instant §3a's machinery
exists — the same subtree-sum bal already computes for interior account
balances, nothing new to build. But that rollup can never *back* a
hard, write-time-refusing cap: bal's own storage design states plainly
that no rollup is ever consulted by a guard (§5 of
`bal-conservation-primitive.md`), because a rollup is derived and
eventually-consistent while a guard's read must be exact in the same
transaction as the write it authorises (§4a of the chronicle
substrate). A hard ancestor-level ceiling is therefore not a rollup
feature — it requires promoting that specific node to its own
guard-bearing account, maintained by direct increment/decrement in the
same transaction as every leaf move beneath it. **Default: capacity is
guard-bearing at the leaf only.** Ancestor rollups are free and always
available for reads; a hard ancestor cap is a distinct, explicitly
deferred feature, promoted only for a named node against a named need
— the same demand-gates principle `SUBSTRATE_DEVELOPMENT_PLAN.md` §1
already states for the rest of the programme.

## 4. Geometry doctrine

**4a. Two shapes, one interface.** Circle (centre + radius) and polygon
(ordered vertex list) cover every case named — square, rectangle,
triangle, and irregular perimeter are polygons with different vertex
counts, not separate types:

```go
type Geometry interface {
    Contains(lat, lon float64) bool
    Distance(lat, lon float64) float64       // to boundary, metres
    BoundingBox() (minLat, minLon, maxLat, maxLon float64)
}
```

`Circle` and `Polygon` are the two implementations. `BoundingBox`
exists purely as a cheap pre-filter, per §6.

**4b. Containment is ray-casting, not triangulation.** Point-in-polygon
is answered directly off the raw vertex list by the ray-casting
(even-odd) or winding-number test — O(n) in vertex count, correct on
concave (non-convex) perimeters without decomposition. Real
jurisdiction and facility boundaries are routinely concave. Geometry is
restricted to **simple polygons** (boundary does not self-intersect) —
this is not a loc-specific restriction invented for convenience; it is
the same restriction SQLite's own polygon extension imposes for
exactly this reason [SQLite Geopoly documentation — see References],
and self-intersecting fence data is vanishingly rare in practice.
Triangulation is the right technique for area computation, clipping,
or polygon union — none of which loc needs (§13); it is not part of
this design.

**4c. One fast path.** An axis-aligned rectangle, detected from its
four vertices, gets an O(1) bounding-box containment check instead of
the general ray-cast — cheap, and probably the commonest real shape
(yards, parking lots, warehouse zones).

**4d. Wire format: GeoJSON, with one gap named.** Polygon geometry
travels as GeoJSON (RFC 7946) — the interoperable standard, readable by
every mapping tool and BIM/GIS pipeline a CMMS/EAM customer is likely
to already have. One gap: GeoJSON has no native Circle type. loc's API
represents a circle as its own typed field (centre + radius), never
forced into a GeoJSON polygon approximation on the wire; only exported
as an approximated polygon on request, for tools that need one.

**4e. Numerics.** Coordinates are `float64` throughout — adequate
precision at facility and city scale; BIM practice's own documented
caveat is that components far from a project's local origin accumulate
large magnitudes once precisely georeferenced at a local datum
[buildingSMART forum — see References], which stresses floating-point
precision at continental scale, not at the scale loc actually targets
(one tenant's facilities, not a national cadastre). A watch-item, not a
blocker. Distance uses the haversine formula for geo coordinates
(sufficient accuracy for facility/service-radius scale; no ellipsoidal
correction needed) and plain Euclidean distance for local-frame
coordinates.

## 5. Fences

A fence is a `Geometry` (§4) with a guard attached. Two containment
mechanisms exist, following directly from §3c:

- **Tree-aligned**: a fence *is* a location's own boundary — "the yard"
  is both a §3a node and a polygon — and membership follows the tree's
  parent-chain walk, free.
- **Standalone**: a fence maps to no tree node (a 20 km service radius
  around HQ, crossing jurisdiction lines) and needs an actual
  `Contains` test against its stored geometry.

Both are real, named cases (jurisdictional nesting is tree-aligned; a
service radius is standalone); the model supports both without forcing
a location node into existence just to hang a fence off it.

## 6. Storage: guard-bearing SQL, advisory index deferred

**6a. Everything guard-bearing lives in SQL, always** — the containment
tree, every node's `Placement`, canonical geometry for every fence,
current assignment per subject, and capacity counts per leaf. This is
non-negotiable per guard locality (§4a of the chronicle substrate): a
guard's read and the write it authorises must commit together, in
whatever engine hosts them.

**6b. The MVP spatial pre-filter is also SQL — not a separate Pebble
plane.** This revises the inception document's default. SQLite ships an
R*Tree virtual-table module purpose-built for exactly this: bounding-box
range queries over stored min/max coordinate pairs, with support for
custom exact-geometry callbacks on top of the box pre-filter — the
module's own documentation uses a circle as its worked example
[SQLite R*Tree documentation — see References]. Built directly on it,
SQLite's Geopoly extension does precisely what §4b describes: store
simple 2D polygons in GeoJSON, pre-filter by R-tree bounding box, then
resolve exactly with `geopoly_contains_point` / `geopoly_overlap` /
`geopoly_within` [SQLite Geopoly documentation; SQLite extensions blog
— see References] — the identical two-stage design this document
proposes for §4, already built, in the engine xolu already runs on.

**Confirmed empirically, 2026-07-31, against the exact pinned dependency
— not inferred.** xolu's SQLite driver is `modernc.org/sqlite` (a
CGo-free transpiled port, per `go.mod`), not the reference C library,
so the question from the earlier draft of this document was genuinely
open until tested directly rather than assumed from the C library's
own documentation. Test method: a scratch Go module pulling the exact
pinned `modernc.org/sqlite v1.29.0` (matching xolu's own `go.mod`
precisely, resolved fresh via the module proxy, not approximated from a
different version), running against an in-memory database:

```
SELECT compile_options FROM pragma_compile_options()
  WHERE compile_options LIKE '%RTREE%' OR compile_options LIKE '%GEOPOLY%'
  → ENABLE_GEOPOLY
  → ENABLE_RTREE

CREATE VIRTUAL TABLE demo_rtree USING rtree(id, minLat, maxLat, minLon, maxLon)
  → OK; insert + bounding-box range query both correct

CREATE VIRTUAL TABLE demo_geopoly USING geopoly()
  → OK; insert a square, geopoly_contains_point(shape, 5, 5) on
    [[0,0],[0,10],[10,10],[10,0],[0,0]] → 2 (strictly inside), correct

sqlite_version() → 3.45.1
```

**Both extensions are compiled in and working, exactly as shipped, no
custom build flags needed.** The two-stage bounding-box-then-exact-test
design in §4/§6 is not merely buildable on this codebase — it is
already present in the dependency already pinned, ready to use as-is.
The earlier draft's fallback (two indexed columns, a plain `BETWEEN`
query) is no longer a contingency this design depends on; it remains
worth knowing about only as a hand-rolled alternative if a future
`modernc.org/sqlite` upgrade ever drops these compile options, which is
now a regression to watch for, not an open question to resolve.

**6c. A derived, eventually-consistent Pebble index is deferred, not
cancelled.** SQL-plane R-tree-shaped filtering serves guard decisions
correctly at any scale and serves *read* queries (nearest, proximity)
well at the scale of one tenant's facilities — hundreds to low
thousands of locations and fences. A separate advisory plane (cal's
H1/H3 split is the precedent: SQLite guard-bearing, Pebble advisory,
never guard-consulted) becomes worth building only if read volume or
fence count outgrows what an indexed SQL scan serves well — a concrete,
measurable trigger, not a day-one requirement. Demand gates hold here
too.

## 7. Admission and concurrency

**7a. A move, worked through.** Subject S requests a move from leaf A
to leaf B. In one transaction: (1) read S's current position (A) and
B's current occupancy count; (2) if B has a capacity ceiling and is at
it, refuse; (3) decrement A's count, increment B's count, write S's new
canonical position, append one journal entry recording the transition;
(4) compute B's containment closure (§3c) — which fences B's position
newly enters, which it no longer sits in relative to A's closure — and
stage the resulting crossing facts (§9) for post-commit delivery.
Everything in (1)–(3) is one guard-bearing SQL transaction; (4) reads
canonical geometry from the same transaction's snapshot, never a
derived index, per §6a.

**7b. Fence containment as a guard input.** Where a fence carries its
own guard (capacity, or a future authorisation predicate — §7c), the
`Contains` test that decides admission runs against canonical geometry
in the move's own transaction — never against the §6b/§6c pre-filter's
cached bounding boxes, which exist to narrow candidates, not to decide
membership. The pre-filter says "check these three fences"; the guard
decision is always the exact test.

**7c. Entry/exit authorisation is out of scope for v1.** loc does not
evaluate who or what may enter a fence. Capacity and exclusivity only;
"who's allowed" is an application-layer check made before calling loc.
Precedent: the chronicle substrate already declined to build a general
admission-rule engine for bal, on the grounds that rule content differs
per deployment and does not belong in the substrate (§4 of
`chronicle-substrate.md`) — only the reservation lifecycle does. The
same logic applies here.

**7d. Guard locality summary.** Canonical geometry, placements,
assignments, and capacity counts are all guard-bearing SQL state. The
§6b/§6c spatial pre-filter is advisory at every tier — it narrows a
candidate set, it never decides a write. This is cal's H1/H3 split,
applied to geometry instead of an occupancy bitmap [`RESOLVED.md`'s own
framing of that split — see References].

## 8. Movement journal

**8a. Reporting is not writing.** Whatever produces a raw position — a
GPS tracker pushing every few seconds — calls loc to *report* a
position, not necessarily to *move* it. loc computes the containment
closure for the reported position and compares it to the subject's last
recorded one; if nothing changed (same leaf, same fences), there is no
journal entry, no event, no ts write. Only an actual change produces a
write. loc's own storage and event volume are proportional to real
movement, not to reporting frequency — sensor-pulse ingestion frequency
is the calling application's concern, not loc's (§13).

**8b. Retention: permanent by default.** The reporting/tracing split in
8a keeps journal volume bounded by actual movement rather than sensor
rate, which makes permanent retention of the movement journal a
reasonable default rather than a storage risk — the opposite of bal's
period-close assumption, for a defensible reason: bal's volume driver
is transaction count, loc's is filtered to state changes only.
Revisit if real movement rates, once observed, contradict this.

**8c. Verification.** A rebuild-oracle harness — `derive(journal) ==
current` — following the chronicle substrate's existing shape (§4,
extraction inventory item 3): current assignment and current capacity
counts must both be exactly reconstructible by replaying the journal
from empty. Feeds `iolu db check` uniformly with ts/cal/bal's own
oracles.

## 9. Events and analytics

**9a. Two consumers per crossing, not one.** A fence entry/exit fires
both a declared reaction (the existing webhook/event mechanism, for
external side effects) and a timestamped record into a ts-shaped feed
(for internal analytics: dwell time, occupancy-over-time). This mirrors
bal, which already emits deltas into the chronicle rollup plane while
staying guard-bearing on the SQL side: the crossing is the one
guard-bearing fact computed in §7a step 4; the event and the ts record
are both derived consumers of it. Event delivery mechanics reuse the
existing model as-is — T-07 through T-13 in the register are still
open, and a loc event source inherits that model's current gaps rather
than getting ahead of them.

**9b. loc as a dxp participant — corrected against the real interface,
which is no longer just a design document.** As of v0.19.3, item 21's
coordinator is live: `dispatchDxpTxn` drives every participant through
Reserve, an attendance gate, Validate, then a collapsed Execute+Commit,
ending in exactly one terminal state — no longer `dxp-coordinator-
design.md`'s description of intended behaviour, but the actual shipped
path a real transaction now runs through end to end. The earlier draft
of this section described a generic "prepare/confirm" shape; the real
`dxp.Participant` interface (`pkg/dxp/dxp.go`) is four methods, and a
`locParticipant` maps onto it directly, not by analogy:

- **Reserve** evaluates loc's guard (capacity, exclusivity — §7) with
  live claims applied, and on consent holds a claim, inside the
  tenant's lock/unlock critical section — the same critical section
  §7a's move transaction already requires, not an additional one.
- **Validate** re-checks the held claim without executing — where a
  loser discovers the destination filled up under it since Reserve, the
  same lazy-invalidation shape bal and cal's own participants already
  use.
- **Execute** applies the effect (the decrement/increment/journal-write
  sequence in §7a step 3) through the coordinator-supplied
  `ParticipantStore`, calling `Ready()` the moment it is actually about
  to write — the point that starts the coordinator's own guard, per the
  existing contract, unchanged for loc.
- **Release** abandons a held claim's local consequences on abort — a
  no-op for loc in the common case, same as most existing participants,
  unless a future staged write needs more.

**Registration is a fifth entry in an already-concrete, four-entry
pattern**, not new machinery: `dxpParticipantRegistry` currently
constructs `bal`, `cal`, `fsm`, and `entity` on demand from a `needed`
map (`pkg/server/v2_dxp_def_handlers.go`) — `loc` is a fifth key in
that same map, sharing the tenant's `dxp.MemCache` the same way the
existing four do. `decodeDxpParticipantParams` will need a
`loc.MoveParams` (or equivalent) case alongside the existing five
`OpParams` types, with the same JSON-tag discipline the register
already learned the hard way for bal's `Amount` field (`json:"-"`,
excluded from generic decode, decoded through a dedicated safe path
instead) — worth checking on arrival whether any of loc's own fields
(geometry payloads, coordinate floats) need the same treatment, per
§4e's numerics doctrine, rather than assuming plain tags are safe by
default.

## 10. Verification

Beyond the rebuild oracle (§8c): geometry edge cases (self-intersecting
input rejected at write time, not silently accepted; concave polygons
verified correct via ray-casting, not just convex ones — real fence
data is routinely concave); guard-locality race tests on capacity
(concurrent moves into a leaf at its ceiling — exactly one wins, per
T-34's precedent for this defect class); placement-chain composition
tests (a node three levels deep under a georeferenced ancestor resolves
to the same absolute point regardless of which intermediate frame is
queried); numerics doctrine compliance (no attempt to smuggle a
non-finite float through any coordinate field, mirroring bal's own
float-smuggle test, §4 of `bal-conservation-primitive.md`).

## 11. Surface

Sketch, not final: `POST /api/v2/loc/report` (position report, may or
may not produce a write, per §8a); `POST /api/v2/loc/move` (explicit
move between tree leaves, bypassing coordinate resolution); `POST
/api/v2/loc/fences` (define a fence, tree-aligned or standalone); `GET
/api/v2/loc/{id}/contains` (containment closure for a position); `GET
/api/v2/loc/nearby` (proximity read, §6c's SQL-plane filter until
demand justifies more).

**11a. Identity: the two-identity split, satisfying §4d by
construction.** Every location and fence gets an **external**,
caller-chosen string id at the wire boundary (`site-mvd/bldg-a/floor-3/
room-204`) and an **internal**, system-allocated dense `uint32` used only
for the storage codec and the §6b spatial index, never exposed. cal and
bal both arrived at this independently; ts didn't and is still paying
for the retrofit (T-46, open). loc should not create the same debt on
day one.

## 12. Relation to prior art

| Precedent | What loc takes from it |
|---|---|
| bal §3a (hierarchical accounts) | The containment tree itself, adopted outright — decimal ids, prefix parentage, postable-leaf discipline. |
| bal §5 ("no rollup is ever consulted by a guard") | The read/guard distinction in §3d — ancestor rollups are free, hard ancestor caps are not derivable from them. |
| cal's H1/H3 split | The guard-locality template for §6/§7: canonical geometry guard-bearing in SQL, any derived index strictly advisory. |
| chronicle substrate §4 (bal's predicate-framework refusal) | The basis for deferring entry/exit authorisation (§7c) rather than building a rule engine. |
| IFC `IfcLocalPlacement`/`IfcSite` | The placement-chain model in §3b — relative transforms composing to an absolute, georeferenced anchor. |
| IBM TRIRIGA / Maximo | Confirmation that vendors in this exact domain build location hierarchies on CAD/BIM data rather than inventing a coordinate scheme, validating §3b's approach. |
| SQLite R-tree / Geopoly | The two-stage bounding-box-then-exact-test design in §4/§6, already implemented in the engine xolu runs on. |
| dxp `Participant` interface + `dispatchDxpTxn` (live as of v0.19.3) | §9b's move lifecycle — Reserve/Validate/Execute/Release, a fifth registry entry alongside bal/cal/fsm/entity, not new machinery. |

## 13. Non-goals

- Not a GIS/BIM system: no floor-plan rendering, no CAD/IFC import or
  authoring — loc *borrows the placement model*, it does not become a
  BIM tool.
- Not a raw GPS ingestion pipeline: continuous device tracking,
  dead-reckoning, and sensor fusion are the caller's problem, same as
  xolu never ingests raw sensor streams for ts.
- Not a general computational-geometry library: geometry support goes
  exactly as deep as guards need (point-in-shape, distance, bounding
  box) and no further — no clipping, no union, no triangulation.
- No routing or wayfinding between locations.
- No entry/exit authorisation in v1 (§7c).

## 14. Staging

Rough, pending wave placement:

1. §3a/§3b: tree + placement model, no guards yet. Rebuild-oracle
   harness for the tree itself.
2. §3c/§3d + §7a/§7d: assignment, capacity, the move transaction, guard
   locality tests. This is the primitive's guard-bearing core.
3. §4/§5/§6: geometry doctrine, fence containment, the SQL-plane
   pre-filter — confirmed as of this compatibility pass to use the
   native R-tree/Geopoly extension directly (§6b), not the two-column
   fallback; no verification step remains before this stage can start.
4. §8: movement journal, retention, verification.
5. §9: events, ts emission, dxp participant adoption — lower risk than
   when this document was first drafted, since item 21's coordinator
   (`dispatchDxpTxn`, live as of v0.19.3) is real code to register
   against now, not a design this stage would have been first to prove.
6. §11a: two-identity split, API surface, client.

## 15. Open items carried forward

- **Spatial index structure at scale (§6c)** — deferred by design, not
  a blocker; revisit against real fence counts and query volume once
  a real deployment has some.
- **Wave placement** — loc is absent from
  `SUBSTRATE_DEVELOPMENT_PLAN.md` entirely; needs a decision on new
  wave vs. folding into an existing one (item #7, /meta subject-
  addressing generalisation, is a plausible anchor).
- **Subject registry** — tracked subjects limited to existing entities
  (reuse the REF/graph mechanism), or does loc need subjects that
  aren't entities in the domain sense (a technician, a vehicle)?
- **§7c's default (no authorisation in v1)** — proposed, not yet
  formally signed off, though it was stated plainly and not objected to
  in the inception discussion.

## References

- buildingSMART, *IFC4.3 Documentation — 8.7.3.14 IfcLocalPlacement*.
  https://ifc43-docs.standards.buildingsmart.org/IFC/RELEASE/IFC4x3/HTML/lexical/IfcLocalPlacement.htm
- buildingSMART, *IFC4.3 Documentation — 5.4.3.63 IfcSite* (WGS84
  georeferencing, true-north rotation).
  http://www.bim-times.com/ifc/IFC4_3/buildingsmart/IfcSite.htm
- buildingSMART Forums, *Coordinates of the IfcLocalPlacement/
  IfcCartesianPoint* (precision-at-distance-from-origin caveat).
  https://forums.buildingsmart.org/t/coordinates-of-the-ifclocalplacement-ifccartesianpoint/1809
- thinkmoult.com, *IFC Coordinate Reference Systems and Revit* (caution
  against mixing CRSes within one placement tree).
  https://thinkmoult.com/ifc-coordinate-reference-systems-and-revit.html
- Naviam, *IBM Maximo Real Estate & Facilities* (BIM Connector, space
  management).
  https://www.naviam.io/products/ibm-maximo-application-suite/ibm-maximo-real-estate-facilities
- Quantum Strides, *Planning a move? With IBM Maximo Integrators for
  TRIRIGA* (property → building → floor → space → subspace hierarchy
  via CAD Integrator/Publisher).
  https://www.ibm.com/support/pages/planning-move-ibm-maximo-integrators-tririga-you-can-put-your-feet-and-let-somebody-else-do-work
- Quantum Strides, *Learn How IBM TRIRIGA Indoor Maps Can Positively
  Transform Employee's Occupancy Experiences* (Esri ArcGIS Indoors
  layered on BIM-derived floor plans).
  https://www.quantumstrides.com/learn-how-ibm-tririga-indoor-maps-can-positively-transform-employees-occupancy-experiences
- SQLite, *The SQLite R*Tree Module* (virtual table, bounding-box
  queries, custom geometry callbacks — circle as the worked example).
  https://www.sqlite.org/rtree.html
- SQLite, *The Geopoly Interface To The SQLite R*Tree Module* (GeoJSON
  polygons, simple-polygon restriction, `geopoly_contains_point` /
  `geopoly_overlap` / `geopoly_within`).
  https://www.sqlite.org/geopoly.html
- SQLite.ai, *SQLite Extensions: Intro to Geopoly* (geofencing use
  case, two-stage bounding-box-then-exact filtering explained plainly).
  https://blog.sqlite.ai/sqlite-extensions-intro-to-geopoly
- Go Packages, *modernc.org/sqlite* and *modernc.org/sqlite/lib*
  (CGo-free port; R-tree callback symbols present in the transpiled
  low-level package).
  https://pkg.go.dev/modernc.org/sqlite ·
  https://pkg.go.dev/modernc.org/sqlite/lib
- Go Packages, *zombiezen.com/go/sqlite* (independent pure-Go binding
  on the same transpiled source, confirmed compiled-in extensions
  include RTree and Geopoly — existence proof, not a claim about
  xolu's own dependency).
  https://pkg.go.dev/zombiezen.com/go/sqlite
- Internal: `bal-conservation-primitive.md`, `chronicle-substrate.md`,
  `dxp-composed-commitment.md`, `RESOLVED.md` (cal H1/H3 framing),
  `SUBSTRATE_DEVELOPMENT_PLAN.md`, `loc-spatial-primitive-inception.md`.
- Internal, added in the v0.19.3 compatibility pass: `pkg/dxp/dxp.go`
  (the real `Participant` interface — Reserve/Validate/Execute/
  Release); `pkg/server/v2_dxp_dispatch.go` (`dispatchDxpTxn`, the live
  coordinator); `pkg/server/v2_dxp_def_handlers.go`
  (`dxpParticipantRegistry`'s bal/cal/fsm/entity pattern); `CHANGELOG.md`
  §0.19.3 (T-93–T-97, item 21's coordinator landing; JSON-tag/decode
  discipline for `OpParams` types).
- Verification method (this pass, not from a published source): a
  scratch Go module pulling `modernc.org/sqlite v1.29.0` — the exact
  version pinned in xolu's own `go.mod` — tested directly against an
  in-memory database for `ENABLE_RTREE`/`ENABLE_GEOPOLY` compile
  options and working `CREATE VIRTUAL TABLE ... USING rtree/geopoly`
  statements. Reproducible from `go.mod` alone; not dependent on this
  environment specifically.
