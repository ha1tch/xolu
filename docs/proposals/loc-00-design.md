# loc — The Spatial Primitive: model and doctrine

`loc-00` of the loc document series: `loc-00-design.md` (this
document, the model and doctrine), `loc-01-rest-api.md` (wire surface
and error codes), `loc-02-implementation.md` (staged build plan).
`loc-03-spatial-index.md` is reserved, not written — see §6c and §15.
Sibling to `obj-00-design.md`: standalone fences compose onto entity
identity (§5), the same convention `/obj`'s subjects use, and `/obj`
position resolution's `loc`-leaf termination case depends on this
document in turn (`obj-00-design.md` §6). Read together, not
separately.

Updated: 2026-07-31 (checked against v0.20.0). ~~Status: proposal —
not scheduled.~~ **Status, corrected 2026-08-01 against v0.21.2:
implemented.** Wave 9 (T-115 through T-118) shipped `/loc` in full —
`CHANGELOG.md` v0.21.0/v0.21.1/v0.21.2, `RESOLVED.md`'s T-115–T-118
closure records — with a follow-on adversarial hardening pass finding
and fixing five real bugs. ~~No register items exist until execution
is decided.~~ Execution was decided and completed; register items
T-115–T-118 are closed, not open. Successor to
`loc-spatial-primitive-inception.md`, which resolved the model's
shape; this document is the design. First native consumer of bal's §3a
hierarchical-account machinery for a second purpose (containment rather
than conservation) and of the chronicle substrate's guard-locality law
applied to geometry rather than balances.

**What shipped narrower than this document describes, named once here
rather than scattered:** standalone-fence identity (§5) shipped as a
bare, caller-chosen `fence_id`, not the entity-composition model this
document specifies — `/meta` wiring needed for the real thing was
out of wave 9's scope. Self-anchored fences (§5, `center.self=true`)
are rejected outright in v1, correctly, since they depend on `/obj`
(wave 10, not built). Neither is a correction to this document's own
reasoning — both are the intended design, not yet fully built. See
§5's own reconciliation note for the precise, code-verified detail,
and `loc-00-addendum.md`'s later sections for the full account.

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

**Subject registry: resolved — entities only, no parallel registry.**
loc does not invent a subject model of its own. A subject is always an
entity, addressed the same way a `REF` field already addresses one
(§2). A technician, a vehicle, a piece of equipment — each is an
entity of its own domain type (`technicians`, `vehicles`, whatever the
tenant's schema calls it), never a loc-specific record. This is the
same reuse discipline §3a already applies to bal's tree and §9b now
applies to dxp's coordinator: xolu's entity/graph layer already solves
"what is a thing with an identity," and loc's own problem statement
(§2) is refusing an illegal position for a thing, not re-deciding what
counts as a thing. A raw-coordinate subject with no tree membership
(the parenthetical above) is still an entity — it simply has no
current leaf, only a last-reported point.

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

**Extension, 2026-08-01, from adversarial testing (`RESOLVED.md`
T-116):** "simple polygon" governs self-intersection; it says nothing
about **holes** — a GeoJSON polygon's interior rings (RFC 7946 §3.1.6:
"any others MUST be interior rings"), a separate, entirely valid
structure a self-intersection check doesn't catch. An earlier
implementation took only the exterior ring and silently dropped any
further ones — a caller submitting a fully RFC-compliant polygon with
holes got a fence whose hole area silently read as "inside." Fixed by
refusing outright: `pkg/loc/geometry.go`'s `DecodeGeoJSONPolygon`
rejects any GeoJSON polygon with more than one ring, with a message
naming why, rather than accepting and silently mis-resolving. This is
this design's own stated position (a single exterior ring only, no
interior-ring support) made concrete, not a new decision — the
implementation had simply not enforced it yet.

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

**A specific numerics hazard, found by adversarial testing and
fuzzing, worth naming precisely rather than leaving under the general
watch-item above (2026-08-01, `RESOLVED.md` T-115, `pkg/loc/
placement.go`).** Converting a local east/west metre offset to a
longitude delta divides by `metresPerDegreeLon`, which is proportional
to `cos(lat)`. `math.Cos(90°)` is `6.12e-17` in float64, not exactly
`0.0` — an exact-zero guard on the denominator misses the near-pole
case entirely, and a 1km offset at `lat=90` produced a "valid"
longitude delta of roughly 1.47×10¹⁴ degrees before this was caught,
silently corrupting whatever it fed into rather than erroring. The
correct fix shape is non-obvious and was itself wrong once: an initial
attempt guarded with a fixed epsilon on the denominator (reject
anything below 1.0 m/degree), which `go test -fuzz` broke within
seconds — `offset=1000, denom=4.22` is well above that threshold and
still produces a delta of 236.8, past the ±180° bound the function
exists to guarantee, because the actual violation condition is
`|offset| > 180×denom`, which scales with the offset, not a constant a
fixed denominator epsilon can ever bound. The fix that actually holds
clamps the **division's result** to ±180°, not the denominator — the
only form of the guard that bounds the output for every combination of
inputs, not just the ones hand-reasoning about "the pole case"
happened to consider.

## 5. Fences

A fence is a `Geometry` (§4) with a guard attached. Two containment
mechanisms exist, following directly from §3c:

- **Tree-aligned**: a fence *is* a location's own boundary — "the yard"
  is both a §3a node and a polygon — and membership follows the tree's
  parent-chain walk, free. A tree-aligned fence's identity is the
  location node it coincides with, and a location node is a
  `/loc`-native construct (defined via `def`), not required to be an
  entity — §3c's "a subject is always an entity" law governs the
  things being *positioned*, not the fixed structure they're
  positioned within.
- **Standalone**: a fence maps to no tree node (a 20 km service radius
  around HQ, crossing jurisdiction lines) and needs an actual
  `Contains` test against its stored geometry.

A standalone fence's identity is an entity it composes `loc.fence`
capability onto — the same `(kind, key)` addressing `/obj` and
`/meta`'s namespaced subjects use, never a freely caller-chosen
`fence_id`. §3c's "a subject is always an entity" law governs a
fence's identity the same way it governs a subject's.

> **Reconciliation, 2026-08-01, checked directly against
> `pkg/loc/store.go`.** ~~Never a freely caller-chosen `fence_id`~~ —
> that is what v1 actually shipped. `DefFence(ctx, fenceID string,
> alignedLocationID *string)` takes a bare string, no entity
> resolution, no `(kind, key)` structure enforced. The wire surface
> (`v2_loc_handlers.go`) is subtler than a plain reversion, worth being
> precise about rather than waving at: the route is still
> `POST /loc/fences/attach` accepting a `subject` field, and `GET`/
> `DELETE` still route through `/loc/fences/{kind}/{key}` — the shapes
> this document specifies. But `handleLocFenceAttach` does
> `fenceID := req.Subject` and stops there — no lookup, no existence
> check, no `XOLU-LOC005`/`006`. And `handleLocFenceGet`/`Delete` read
> only the `key` path segment; `kind` is parsed by the router and then
> never referenced by either handler. The URL shape survived; the
> entity-resolution semantics it implies did not. `RESOLVED.md`'s
> T-115 record confirms this was a conscious staging call (*"fences —
> bare identity — real geometry is Stage 3"*), not an oversight, and
> `CHANGELOG.md`'s v0.21.0 entry names it as an explicit, deliberate
> v1 scope narrowing pending `/meta` wiring. The design below is still
> the intended target — nothing here argues it was wrong — only that
> it isn't what a caller gets today.

This is not one shape, though — two genuinely different cases sit
under "attaches to an entity," and conflating them would be wrong:

- **A dedicated place-entity with no position of its own** — a
  jurisdiction, a designated exclusion zone, a numbered ward. This
  entity composes `loc.fence` only; it was never a `/loc` or `/obj`
  subject and never moves. Islington, as an administrative
  neighbourhood, is this case: independently meaningful, named,
  regardless of whether anything is ever fenced against it.
- **The same entity that already composes a position, via `/obj` or
  `/loc`.** A service radius around headquarters is not a place in its
  own right — it's a property of HQ itself. HQ's own entity composes
  both `obj.position` (or a `/loc` tree-anchor) *and* `loc.fence`,
  self-anchored to its own resolved position — one entity, two
  capabilities, the fence referencing itself rather than pointing at
  something external. A convoy's moving exclusion zone and a ship's
  own hold-tree anchor (§3b, revised below) are the same shape: the
  fence or the tree root resolves through the *same* entity's own
  position, not a foreign one. See `obj-00-design.md` §11 for the full
  treatment — this is a fact about composition generally, not a
  `/loc`-specific mechanism, and is documented there once rather than
  duplicated here.

**5a. Fence capacity: resolved — the same guard, keyed by fence
instead of leaf.** Where a fence carries a capacity ceiling (a loading
dock zone limited to three trucks; a hazmat containment area limited
to two people — both real, named, not hypothetical), it gets exactly
§3d's leaf-capacity mechanism: a bounded count on the fence's own
guard-bearing row, incremented on entry, decremented on exit, refused
at the ceiling — not a new mechanism, the same admission guard applied
to a fence id instead of a leaf id. This holds identically for both
tree-aligned and standalone fences; a tree-aligned fence's capacity row
is simply keyed by the location node it coincides with, no different
in kind from any other capacity-bearing leaf. Most fences carry no
ceiling at all (a jurisdiction boundary has no capacity), so this is
an optional guard per fence, not a mandatory field.

**Consequence for §7a:** a capacity-bearing fence's admission check is
guard-bearing, so it cannot be evaluated only in step (4)'s post-guard
closure computation — it must be evaluated inside the same
guard-bearing transaction as the leaf check, alongside it, before the
move commits. §7a is revised below to reflect this; a fence with no
capacity ceiling still has its crossing detected in step (4) exactly
as originally described, since there is nothing there to guard.

**Cost worth naming rather than letting the composition read as
free:** every standalone fence needs an entity behind it —
`"dock-zone"`, `"svc-radius-hq"` are not bare strings but entity
subjects. A genuinely disposable fence with no independent meaning (a
six-week construction safety perimeter) still needs some entity to
compose onto; a minimal, throwaway one is the honest answer, not a
reason to carve out an identity-free exception for this one case.

**Fence membership testing inherits `/obj`'s deeper-resolution
requirement.** Where an `/obj` subject is nested several containment
levels deep, its position has no coordinate of its own to test against
a fence's geometry directly — the chain must be walked down to wherever
it actually terminates (a `/loc` leaf or a raw report) before `Contains`
can run at all. See `obj-00-design.md` §6.

## 5b. Historical placement for a moving anchor

§3b's placement-chain composition resolves against a location's
*current* anchor. For a node whose anchor never changes (a building, a
room) that's the whole story. For one whose anchor does change — a
ship at berth, a relocatable site trailer, an entity composing
`loc.fence` self-anchored to its own moving position (§5, above) — a
historical query ("where was subject X on July 15th") needs the
anchor's position *as it was then*, not as it is now.

`/loc` does not version placement to answer this. A location's
placement chain is two things composed: a static relative offset (a
ship's hold-3, relative to the ship's own hull — this never changes)
and an absolute anchor (the ship's position in the world — this does).
Only the second half has history worth keeping, and `/ts` is where
continuous position data already belongs. An anchor with a device
feeding it — a ship's AIS transponder, a tracked convoy — reports into
a `/ts` timeline the same way any other continuous signal does. A
historical position query composes the subject's unchanging relative
offset with the anchor's `/ts`-queried position as of the requested
date. No versioning mechanism lives inside `/loc` itself, because the
thing that varies over time was never `/loc`'s to store.

This covers any anchor with a continuous position feed. It does not
cover a discretely-repositioned one — a site trailer an administrator
relocates twice a year via ordinary `PATCH`, with no device and no
continuous signal to feed `/ts`. §8's movement journal records subject
movement only, never a location node's own anchor changing, so a node
like this has no historical record of its own past position at all.
The fix is small and belongs with §8, not `/ts`: `PATCH`'s
anchor-update path should append one journal entry, the same
discipline §8 already applies to subject moves — a few writes a year,
not a timeline, since there's no continuous signal to store. Not yet
built.

## 5c. Boundary mutation: reconciliation via the rollup pattern

A fence's geometry can change (`PATCH`, not yet in `loc-01-rest-api.md`'s
drafted surface, but the underlying question doesn't wait for the
endpoint to exist). History is already safe without any addition here:
a `report`'s journal entry stores the *computed* crossing fact at write
time (§8), never a raw coordinate re-resolved later, so a geometry
change can't retroactively corrupt what the journal says already
happened. What isn't safe without an addition: a subject that entered
a fence under its *old* geometry and hasn't reported since has its
membership — and its contribution to `loc_fence_capacity.count` — go
stale the moment the fence's shape changes, with nothing to notice or
correct it.

**The live guard is unaffected by anything in this section.** §7b's
rule stands exactly as written: an admission decision always runs the
exact `Contains` test against canonical, current geometry, in the
write's own transaction. Nothing below changes that, adds a second
guard path, or makes the guard consult anything derived.

**The mechanism is `chronicle.RebuildOracle` — checked directly against
`pkg/chronicle/oracle.go`, and this section's own citation should
actually be sharper than "the same discipline as `bal`'s rollup":
`RebuildOracle` is shared, generic infrastructure (`{Name, Derive,
Current}`), not `bal`-specific machinery being analogised loosely.
`bal.RebuildRollup`, `pkg/storage`'s graph oracle, and — found while
reconciling this document against the shipped v0.21.2 checkpoint —
`loc`'s own `FenceMembershipFoldOracle` (`pkg/loc/verify.go`) are all
instances of the same type.** A transfer's authoritative commit
happens first; the derived plane folds the change into the cascade
*after*, best-effort — a crash or error leaves it stale, which the
oracle detects and a rebuild repairs from source, and no guard ever
reads the derived plane, so staleness there is a performance/
observability matter, never a correctness one. Applied to a fence
whose geometry just changed, this section's own reconcile function is
a close sibling of `FenceMembershipFoldOracle`, sitting in the same
file, sharing its `Current` step exactly: **`loc_fence_membership`
already exists** (`(subject_ref, fence_key)`, live-maintained on every
`report`/`move`, confirmed directly against `pkg/loc/store.go`/
`geometry.go`) — the reverse index an earlier pass of this design
believed still needed building turned out to already be shipped, for
a different reason (fold-oracle verification), reusable here as-is. No
bounding-box candidate search is needed to find who to re-check —
`SELECT subject_ref FROM loc_fence_membership WHERE fence_key = ?`
gives the exact current set directly. What differs from
`FenceMembershipFoldOracle` is only the `Derive` step: instead of
replaying the journal, this reconcile function re-tests each of those
exact subjects' last-known points against the fence's *current*
geometry, producing a fresh, **advisory** view of what membership
actually is right now.

**This view is read-only and never writes `loc_fence_capacity.count`
or `loc_fence_membership` directly.** Both are guard-bearing —
`loc_fence_membership` is read and written inside the same
transaction as every ordinary `report`/`move`'s delta computation,
confirmed directly against `geometry.go` — and `bal`'s own rollup
bucket is not, so letting a derived reconcile process write into
either would be exactly what "no guard reads the rollup" exists to
forbid, the same rule running in reverse: reading guard-bearing state
for an advisory comparison is fine, writing it from outside the
ordinary guarded path is not. If the advisory view disagrees with
current membership, that disagreement is surfaced the way
`rollupDegraded`/`onRollupError` surfaces a lagging rollup — an
operator-facing signal, not a silent correction. Fixing it, when it
needs fixing, happens through an ordinary `report` call for the
affected subject — the same CAS path every other capacity change
already uses, which naturally updates `loc_fence_membership` and
`loc_fence_capacity.count` together, correctly. This is `cal`'s R-T1
discipline again (a definitional parameter changing after dependent
state already exists against the old value gets corrected by the
caller re-issuing through the primitive's own ordinary write path,
never by silent retroactive reinterpretation) — sharpened, not
replaced: the caller no longer has to already know who's affected,
because the advisory view answers that cheaply, the same way
`RebuildRollup` answers "what should this bucket say" without ever
being allowed to touch a balance directly.

**Distinct from §5b's residual, not the same fix wearing a new name.**
§5b's open item is a *missing history* problem — no journal entry
records a discretely-repositioned anchor's past value at all. This
section is a *stale derived aggregate* problem — the guard-bearing
count is exactly correct as a record of recorded entries and exits,
and has simply drifted from what fresh geometry would now say. §5b's
fix is still the small one already named there (append one journal
entry on anchor `PATCH`); this section's fix is the rollup pattern
above. Solving one does not solve the other.

## 5d. Fence-type patterns: the same mechanism applied a fourth time

Many fences of a shared kind — every loading-dock zone across every
warehouse — carry the same capacity default. Retyping a ceiling at
every `def` is the identical friction `obj-00-design.md` §7a names for
a fleet of standard containers, and the fix is the identical mechanism,
not a new one: a `loc_patterns` definitional record — **not** a
`/loc` fence or location, not addressed via any subject convention, the
same category as `fsm_definitions`/`obj_patterns`, a pure definitional
row with no position of its own. A fence or location `def` gains an
optional `pattern` field, mutually exclusive with inline `capacity`
(mirroring `obj-01-rest-api.md`'s `XOLU-OBJ013` shape — a new
`XOLU-LOC` code for loc's own version), snapshotted at creation into a
**cloned child** carrying a lineage pointer back to its pattern and a
computed, `definitionExists`-shaped `pattern_deleted` check — not
stored, not requiring invalidation. A pattern changing later never
retroactively touches already-cloned fences, for the same reason
§7a's obj patterns don't and a running `fsm` machine doesn't. (Named
"pattern," not "template," for the same reason `obj-00-design.md` §7a
gives — the noun shouldn't presuppose a definition was authored in
advance rather than recognised from something that already exists,
even though §5d itself only currently offers the `def`-from-scratch
creation path, not `obj`'s own `extract`.)

**Detecting drift needs no new bookkeeping either**, for the same
reason §5c's reconciliation doesn't: comparing a cloned fence's current
fields against its pattern's current fields, on demand, is the whole
mechanism — the identical recompute-and-compare shape `RebuildRollup`
uses, not per-field version tags.

**What this is not.** Not the bigger case named in §15 — a whole
location-and-fence *subtree*, defined in relative terms, applied at a
new anchor to bootstrap many similar sites at once (a new store's
entrance/checkout/stockroom layout, stamped from one pattern). That
case needs many rows produced from one definition, translated to a new
root, closer to Maximo's own Item Assembly Structure "apply" action in
shape — materially more machinery than one row cloning from one
pattern. §5d is the flat, single-record version; the subtree case is
covered separately (§15).

## 6. Storage: guard-bearing SQL, advisory index deferred

**6a. Everything guard-bearing lives in SQL, always** — the containment
tree, every node's `Placement`, canonical geometry for every fence,
current assignment per subject, and capacity counts per leaf and per
capacity-bearing fence (§5a). This is
non-negotiable per guard locality (§4a of the chronicle substrate): a
guard's read and the write it authorises must commit together, in
whatever engine hosts them.

**Why this SQL lives in `loc`'s own dedicated database file rather
than the shared one — a write-throughput argument, not just a
storage-layout one, verified directly against the real mechanism
rather than assumed.** `pkg/storage/sqlite.go`'s own comment states it
plainly: even under WAL mode, the connection pool is capped at one —
*"MaxOpenConns=1 (WAL single-writer)"* — because SQLite allows exactly
one writer per database file, WAL or not; WAL's actual benefit is that
readers never block that writer and vice versa, not concurrent
writers. `bal`, `cal`, `fsm`, and `entity` all share one file
(`store/xolu.db`), so their guard-bearing writes serialise against
each other through that single lock, full stop — moving `bal`'s
rollup cascade and `cal`'s H3 occupancy index onto Pebble (T-62;
`pkg/bal/rollup_pebble.go`'s own comment states the rule directly:
guard-bearing state stays SQL-colocated with its guard, and only what
"nothing ever refuses" moves to Pebble) doesn't relieve this at
all, because that derived-plane write happens strictly after commit —
by the time it touches Pebble, the shared file's lock has already been
acquired and released. `ts` is the one primitive that already escapes
this entirely: its `Store` is Pebble from top to bottom, no SQLite
component at all. `loc` getting its own file (§9b's storage-separation
finding) puts it in the same position — a genuinely separate lock
domain, not a shared one with a faster mirror bolted on the side. The
throughput consequence is real but conditional, not a flat multiplier:
it's an aggregate-throughput gain under concurrent, storage-domain-
mixed load — a `loc`-only write can commit while the shared file's
lock is held by an unrelated `bal`/`cal`/`fsm`/`entity` write, but any
single `dxp` transaction spanning both domains still needs both locks,
so its own latency is unchanged. `/obj` getting its own file follows
the identical reasoning, a third domain rather than a fourth primitive
added to the shared one.

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

**7a. A move, worked through — revised for §5a's fence-capacity
mechanism.** Subject S requests a move from leaf A to leaf B. In one
transaction: (1) read S's current position (A), B's current occupancy
count, and canonical geometry for every fence B's new position would
enter or leave (§3c's closure, computed here rather than deferred, so
any capacity-bearing fence among them can be checked before commit);
(2) if B has a capacity ceiling and is at it, refuse; if any
newly-entered fence has a capacity ceiling and is at it, refuse; (3)
decrement A's count, increment B's count, apply the same
decrement/increment to every capacity-bearing fence S is leaving or
entering, write S's new canonical position, append one journal entry
recording the transition; (4) stage the full crossing-fact set —
capacity-bearing and non-capacity-bearing fences alike — for
post-commit delivery (§9). Everything in (1)–(3), including every
capacity-bearing fence's admission check, is one guard-bearing SQL
transaction; only the non-guard-bearing crossing facts in (4) are
read for post-commit staging rather than for a write-time decision,
and even those are read from the same transaction's snapshot, never a
derived index, per §6a.

**7b. Fence containment as a guard input.** Where a fence carries its
own guard (capacity, or a future authorisation predicate — §7c), the
`Contains` test that decides admission runs against canonical geometry
in the move's own transaction — never against the §6b/§6c pre-filter's
cached bounding boxes, which exist to narrow candidates, not to decide
membership. The pre-filter says "check these three fences"; the guard
decision is always the exact test.

**7c. Entry/exit authorisation is out of scope for v1 — signed off.**
loc does not evaluate who or what may enter a fence. Capacity and
exclusivity only; "who's allowed" is an application-layer check made
before calling loc. Precedent: the chronicle substrate already
declined to build a general admission-rule engine for bal, on the
grounds that rule content differs per deployment and does not belong
in the substrate (§4 of `chronicle-substrate.md`) — only the
reservation lifecycle does. The same logic applies here. Formally
closed in this pass (§15 previously carried this as proposed-but-not-
signed-off); no objection was raised when it was first stated in the
inception discussion, and none has arisen since.

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

**Registration is a sixth entry in an already-concrete, five-entry
pattern**, not new machinery — corrected in this pass against v0.20.0,
one release past this section's original v0.19.3 check: `T-86` landed
a `ts` dxp adapter in 0.20.0, so `dxpParticipantRegistry` now
constructs `bal`, `cal`, `fsm`, `entity`, and `ts` on demand from a
`needed` map (`pkg/server/v2_dxp_def_handlers.go`) — `loc` is a sixth
key in that same map, sharing the tenant's `dxp.MemCache` the same way
the existing five do. `decodeDxpParticipantParams` will need a
`loc.MoveParams` (or equivalent) case alongside the existing six
`OpParams` types (`bal.TransferParams`, `cal.CalTransitionParams`,
`storage.FsmTransitionParams`, `storage.EntityUpdateParams`,
`storage.EntityAppendParams`, `timeseries.AppendParams` — enumerated
directly against `decodeDxpParticipantParams`, not assumed), with the
same JSON-tag discipline the register already learned the hard way for
bal's `Amount` field (`json:"-"`, excluded from generic decode, decoded
through a dedicated safe path instead). This is no longer an
open question deferred to arrival: §4e's numerics doctrine already
requires it, so loc's geometry and coordinate-float fields get the
same `json:"-"`-plus-guarded-decode treatment as a stage-0 obligation —
pinned concretely in `loc-02-implementation.md`, not left for whoever
writes the handler to notice.

**What this section didn't anticipate, found the hard way and fixed
(2026-08-01, `CHANGELOG.md` v0.21.0, `pkg/server/v2_dxp_dispatch.go`).**
"Sharing the tenant's `dxp.MemCache` the same way the existing five do"
is still true — the gap was one level lower. `bal`/`cal`/`fsm`/`entity`
share one SQL database; `loc` has its own (`storelayout.TenantLocDir`,
§6a). Tagging loc's participant with the same `"sql"` engine string the
shared-database four use was wrong, and it wasn't caught until the
first real end-to-end dispatch: it silently collapsed a mixed
transaction onto the wrong database, surfacing as `"no such table:
loc_capacity"` rather than a clean refusal. The fix follows `ts`'s own
precedent exactly rather than inventing a new one: `dispatchDxpTxnCore`
now takes a third database handle (`locDB *sql.DB`, alongside the
shared `db` and `ts`'s own `pebbleDB`), constructed via `s.locStore(r)`
whenever `needed["loc"]`. Worth stating for whoever reads this
section as a template for the next primitive to join dxp with its own
dedicated storage: "joins the registry" and "shares the storage" are
two separate facts, and this section conflated them.

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

**Superseded by `loc-01-rest-api.md`.** The endpoint sketch below is
retained for continuity with earlier discussion, not as the current
surface reference — `loc-01-rest-api.md` carries the actual
request/response bodies, field-by-field, and the reserved `XOLU-LOC`
error-code table. Read that document for anything wire-shaped; this
section is history the moment loc-01 exists.

Sketch, not final: `POST /api/v2/loc/report` (position report, may or
may not produce a write, per §8a); `POST /api/v2/loc/move` (explicit
move between tree leaves, bypassing coordinate resolution); `POST
/api/v2/loc/fences` (define a fence, tree-aligned or standalone, now
possibly carrying a capacity ceiling per §5a); `GET
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
| dxp `Participant` interface + `dispatchDxpTxn` (live as of v0.19.3) | §9b's move lifecycle — Reserve/Validate/Execute/Release, a sixth registry entry alongside bal/cal/fsm/entity/ts, not new machinery. |
| bal's rollup discipline (`PostCommit`/`EmitDeltas`/`RebuildRollup`/`rollupDegraded`) | §5c's boundary-mutation reconciliation — commit-first-authoritative, best-effort derived view, never guard-consulted, degrades via a signal, never a silent write. |
| `fsm`/`dxp`'s definition-snapshot-lineage pattern (`obj-00-design.md` §7a, applied a third time there) | §5d's fence-type patterns — cloned child, lineage pointer, computed deletion check, applied a fourth time. |

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

- **Historical placement for a discretely-repositioned anchor (§5b)**
  — a location node relocated by ordinary `PATCH`, no device, no
  continuous signal. §8's journal needs to record anchor changes, not
  just subject moves. Not yet built as of v0.21.2 — no evidence of
  this in `CHANGELOG.md` through the v0.21.x line.
- **Spatial index structure at scale (§6c)** — deferred by design, not
  a blocker; revisit against real fence counts and query volume once
  a real deployment has some. Reserved as `loc-03-spatial-index.md` in the
  document series; not written until that revisit happens. Unaffected
  by v0.21.0's shipped `USING rtree(...)` pre-filter, which is §6b's
  scope, not §6c's — the SQL-plane pre-filter was never the deferred
  part; a further Pebble-based advisory plane at larger scale is.
- ~~**Wave placement**~~ — **resolved, shipped as wave 9.** All four
  items (T-115–T-118) created and closed 2026-08-01, `CHANGELOG.md`
  v0.21.0. Item #7 was not, in the end, the anchor — wave 9 stood on
  its own.
- **Stage 2 throughput** — checked against `CHANGELOG.md`/`RESOLVED.md`
  through v0.21.2: no benchmark number recorded anywhere in the shipped
  release notes for the guard-bearing write path specifically (adversarial
  *correctness* testing is extensive and real — ~50 new tests in
  v0.21.1 alone — but throughput, as distinct from correctness, is not
  named). Still genuinely open, not silently dropped.
- ~~**Subject registry**~~ — **resolved, §3c.** Entities only, via the
  existing `REF`/graph mechanism; no loc-specific subject model.
- ~~**§7c's default (no authorisation in v1)**~~ — **resolved, §7c.**
- ~~**Standalone fence identity**~~ — **resolved as design, §5; shipped
  narrower.** The entity-composition model is still the design this
  document specifies. v0.21.0 shipped a bare, caller-chosen `fence_id`
  instead — a conscious, named v1 scope narrowing (`/meta` wiring
  needed for the real thing was out of wave 9's scope), not a rejection
  of the design. §5's own reconciliation note has the precise,
  code-verified account of what shipped versus what's specified here.
- ~~**Historical placement, general case**~~ — **resolved, §5b.**
  Any moving anchor with a continuous position feed resolves through
  `/ts`; only the discretely-repositioned case above remains.
- **Boundary mutation (fence geometry changing while subjects are
  already inside)** — designed, §5c: reconciliation via the same
  rollup discipline `bal` already uses. **Not yet built** — no evidence
  in `CHANGELOG.md` through v0.21.2 of a reconcile function, and §5c
  postdates wave 9's own implementation work, so this couldn't have
  been in scope for it. Genuinely open against the shipped code, even
  though the design itself is settled.
- **Fence-type shared defaults** — designed, §5d: the same cloned-child
  pattern `obj-00-design.md` §7a proves, applied a fourth
  time. **Not yet built**, same reasoning as boundary mutation above —
  postdates wave 9, no `loc_patterns` table or `pattern` field
  anywhere in the shipped `pkg/loc`.
- ~~**Subtree bootstrap patterns**~~ — **resolved.** A whole
  location-and-fence subtree, defined in relative terms, applied at a
  new anchor to bootstrap many similar sites at once (store layouts,
  warehouse rack patterns), covered on top of §5d's flat pattern.
- **Two new gaps found by v0.21.x's own adversarial hardening, not
  originally named as open items because they weren't yet known:**
  `loc-01-rest-api.md`'s error-code table is missing entries for
  duplicate `location_id`/`fence_id` (shipped as `XOLU-LOC014`/`015`)
  and has no reserved code at all for `ValidationError`'s generic
  400 class (malformed GeoJSON, unsupported geometry type, empty
  required fields) — both gaps flagged directly in `pkg/loc/errors.go`'s
  own comments, not inferred. See `loc-01-rest-api.md`'s error table for
  the correction.

## References

- `obj-loc-industry-practices.md` — the full external-research
  narrative behind every citation below: what was learned from each
  system, what was adopted, what was rejected, and why. This section
  and §12 cite individual sources; that document is where the
  reasoning connecting them to specific decisions actually lives.
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
- Internal, added this pass: `pkg/bal/rollup.go` (`RebuildRollup`),
  `pkg/bal/store.go` (`rollupDegraded`, `onRollupError`, the
  commit-first-then-fold-best-effort pattern, confirmed directly
  against source) — §5c's reconciliation mechanism, reused as-is.
  `obj-00-design.md` §7a — the cloned-child/lineage/computed-deletion
  pattern §5d applies a fourth time; §5d assumes that section's own
  citations rather than re-deriving them here.
- Internal, added 2026-08-01 against the v0.21.2 checkpoint (wave 9's
  shipped implementation, checked directly, not summarised from
  `CHANGELOG.md` alone): `pkg/loc/store.go` (`DefFence`'s bare-string
  identity), `pkg/loc/errors.go` (the full shipped `XOLU-LOC` family,
  including `XOLU-LOC014`/`015` and the uncoded `ValidationError`
  class, neither in `loc-01-rest-api.md`'s table), `pkg/loc/
  placement.go` (`safeLongitudeDelta`, the near-pole clamp), `pkg/loc/
  geometry.go` (`DecodeGeoJSONPolygon`'s holes rejection),
  `pkg/server/v2_loc_handlers.go` (`handleLocFenceAttach`/`Get`/
  `Delete` — the `subject`-field-as-bare-string and unused-`kind`
  findings), `pkg/server/v2_dxp_dispatch.go` (`locDB`'s separate
  handle in `dispatchDxpTxnCore`). `CHANGELOG.md` v0.21.0/v0.21.1/
  v0.21.2; `RESOLVED.md` T-115 (dense-key allocation race, fences'
  bare-identity staging note).
- Internal, added 2026-08-02: `pkg/chronicle/oracle.go`
  (`RebuildOracle`'s `{Name, Derive, Current}` shape — shared, generic,
  not `bal`-specific, confirmed by its three independent instantiators);
  `pkg/loc/verify.go` (`FenceMembershipFoldOracle`, §5c's direct
  sibling); `pkg/loc/store.go`/`geometry.go` (`loc_fence_membership`,
  live-maintained on every `report`/`move` — the reverse index §5c
  needed, already shipped for a different reason).
