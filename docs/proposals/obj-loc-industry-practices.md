# `/obj` and `/loc` — industry practices review

Updated: 2026-08-02. Not a proposal document — a citable record of
the external research that ran alongside `/loc` and `/obj`'s own
design work, so a future reader can see what was learned from
existing systems, what was taken from each, what was deliberately
left out, and why, without re-mining a chat transcript. Companion to
`obj-loc-stress-test-findings.md`, which records the *internal*
adversarial review; this document records the *external* one. Where
the two touch the same decision, this document cites the other rather
than repeating it.

Systems reviewed, in the order they were actually researched: IBM
Maximo (core Location/Asset model, Item Assembly Structure, Location
Types, Network Systems, decommissioning), IBM TRIRIGA (occupancy
monitoring, space management), the IFC/buildingSMART standards
(placement and georeferencing), commercial RTLS/geofencing platforms
(Litum, Navigine, Inpixon, LocaXion, SmartX HUB), yard/dock capacity
and logistics planning (Oracle OTM, general TMS/YMS practice),
mid-market CMMS (Fiix, UpKeep, Limble), and SQLite's own R*Tree/Geopoly
extensions. None of this research happened in the abstract — every
entry below exists because it changed, confirmed, or explicitly failed
to change a specific decision in `loc-00-design.md` or
`obj-00-design.md`, cited by section throughout.

## How to read the labels

- **Adopted** — taken directly, the design follows the external
  precedent closely.
- **Adopted in spirit, rejected in mechanism** — the underlying idea
  was right and used; the specific implementation the external system
  chose was examined and deliberately not copied, for a stated reason.
- **Rejected** — considered and declined. The reason is the point of
  the entry, not the rejection itself.
- **Confirmed, not adopted** — the research didn't change a decision;
  it validated one already made, independently, before the precedent
  was found. Included because a validated decision and an untested
  assumption look identical from outside without this record.

---

## IBM Maximo — the core Location/Asset split

**Adopted.** Maximo's Location and Asset are separate structures for
a reason directly relevant to `/loc` vs `/obj`: a Location is fixed
and doesn't move (pull a pump for repair, the Location stays); an
Asset is the physical thing, carries its own history, and gets
reinstalled somewhere else later, its own history following it. This
was found *after* `obj-loc-stress-test-findings.md`'s own "containment
vs. location — one primitive or two?" question had already been
resolved the same way, independently, through adversarial scenario
testing — not copied from Maximo and retrofitted with a citation. The
value here isn't that Maximo told us the answer; it's that a mature,
widely-deployed system arrived at the identical structural split for
adjacent reasons, which is real evidence the split is a correct
answer to the underlying problem, not an artifact of how the
scenarios happened to be worded. `obj-00-design.md` §2, §11;
`loc-00-design.md` §1.

## IBM Maximo — Item Assembly Structure (IAS)

**Adopted in spirit, rejected in mechanism — the most consequential
single piece of research in this document.** IAS is a Bill of
Materials, applied as a template to stamp a repeated sub-assembly
structure onto many assets at once. The idea — a reusable structure
definition, applied to instantiate concrete instances — is real and
valuable, and became `obj-00-design.md` §7a's container patterns and
`loc-00-design.md` §5d's fence-type patterns. What was explicitly
**not** copied: IAS's apply action *creates child asset records* —
contents get stamped into existence on apply. `obj-00-design.md` §7a
states directly why this was rejected: `/obj`'s containment exists
specifically because contents change by ordinary, high-frequency
operation (loading and unloading a pallet), not administrative action;
copying IAS's content-stamping behaviour would have reintroduced that
exact rejected assumption through the back door of "but this part is
just templating." The mechanism actually used instead — snapshot the
definition at creation, retain a lineage pointer, track deletion of
the source explicitly — was not invented in response to IAS at all.
It was found already built, independently, twice, in `fsm` and `dxp`
(`pkg/server/v2_fsm_machine_handlers.go`'s `snapshot_json`,
`pkg/server/v2_dxp_def_handlers.go`'s `dxpTxnSnapshot`), checked
directly against source before IAS was ever researched. IAS confirmed
the general shape of problem was real and worth solving; it did not
supply the solution, which the codebase already had. Worth naming
precisely what IAS's own mechanism is missing that xolu's has: no
lineage pointer back to the source template visible anywhere in the
material reviewed, and no signal for "has the source template changed
since" — real practitioner accounts of Maximo's *decommissioning*
workflow (below) show the cost of that same absence in a different
part of the product.

## IBM Maximo — Location Types

**Rejected — composition instead of a type enum.** Maximo names eight
Location Types (OPERATING, VENDOR, SALVAGE, REPAIR, LABOR, COURIER,
HOLDING, STOREROOM), each with different behavioural implications —
LABOR/COURIER track inventory balance in transit, HOLDING is
mandatory-one-per-site. Rejected as a `/loc`-native concept: LABOR/
COURIER's balance-tracking behaviour is exactly what `/bal` already
owns, and growing `/loc`'s own type taxonomy to match would duplicate
a primitive that already exists rather than composing with it — a
Seam AMS location needing transit-inventory semantics gets a `/bal`
account associated via `REF`, not a new `postable`-adjacent enum value
inside `/loc`. The one part of this genuinely missing from `/loc` —
some richness beyond the bare `postable` boolean — is partially
recovered a different way: `loc-00-design.md` §5d's fence-type
patterns, and the equivalent idea for location *kinds* (§15's
"subtree bootstrap patterns" item), get some of the same value back
through composition and
defaults rather than a hardcoded enum, which is the more xolu-idiomatic
shape for the same underlying need. The mandatory-one-HOLDING-per-site
invariant is a different kind of rejection: correctly an application-
layer convention (Seam AMS can enforce it if it wants that pattern),
not something the substrate should know about — the same shape of
argument `loc-00-design.md` §13 already makes for excluding CAD/BIM
import.

## IBM Maximo — Network Systems

**Rejected as `/loc`-native; redirected to `/graph`.** A Maximo
location can belong to a primary hierarchy (geographic, one parent,
matching `/loc`'s own tree) *and* secondary Network Systems — a
transmission tower on two electrical circuits has two parents, one
per circuit, so an outage on one circuit can enumerate every location
wired to it. `/loc`'s single-parent tree can't express this, and —
this was the actual finding worth recording, not just "loc can't do
X" — fences can't substitute for it either, even though fences are
`/loc`'s own answer to "inside multiple things at once": a fence
decides membership by a geometric `Contains` test, and a circuit has
no shape. "Which locations are wired to circuit 3" is a pure
topological relationship, categorically different from anything
`Geometry` represents. Confirmed `pkg/graph` exists (`FlatGraph`,
arbitrary node types, both edge directions) before concluding
anything, not assumed: a circuit membership is a `/graph` edge between
location entities, entirely outside `/loc`'s own guard-bearing tree —
the identical reuse-discipline argument already applied to the subject
registry question (`loc-00-design.md` §3c: subjects are entities,
not a new registry). Not built; named as a real capability gap only
against Maximo's own top tier, not against `/loc`'s actual mid-market
competitive set, none of which has this either.

## IBM Maximo — decommissioned-asset handling

**Rejected — and independently validated, after the fact, a decision
already made.** Maximo's actual answer to "this asset is gone" is a
status flag plus a move to a synthetic SALVAGE-type location — the
exact synthetic-location workaround `obj-00-design.md` §12 names and
rejects explicitly in favour of three first-class lifecycle states
(positioned / unknown-off-site / permanently retired). The lifecycle
split was designed first, via the hospital-mortuary stress-test
scenario (`obj-loc-stress-test-findings.md`, pass 2) — this research
came afterward, as a check, not as the source of the idea. What it
added was real, independent evidence the rejected pattern actually
costs something in production: a Maximo practitioner's own account
calls the out-of-the-box handling "clunky," citing decommissioned
assets skewing inventory counts and lingering in views they shouldn't;
a documented IBM defect shows the system permitting an asset to be
decommissioned while still referenced on an open work order, despite
the user guide stating that should be blocked. `/obj`'s `retire`
(`obj-01-rest-api.md` §6) refusing outright when a subject currently
contains anything (`XOLU-OBJ012`) is a guard-enforced version of
precisely the invariant Maximo's own bug tracker shows failing.
Confirmed, not adopted — the value is that a market-leading system's
real production pain matches the exact shape this design predicted
and avoided.

## IBM TRIRIGA — occupancy monitoring vs. a live guard

**Rejected as the primary mechanism; kept as a legitimate fallback.**
TRIRIGA's capacity/occupancy feature is built on Cisco DNA Spaces
density heatmapping with a 15-minute refresh, alerting when a
threshold is crossed — monitoring and alerting, not a transactional
gate. `/loc`'s own §3d/§5a capacity guard and `/obj`'s §7 capacity CAS
both refuse a write at the moment of attempted entry, a stronger
correctness guarantee than anything found in this research across the
whole CMMS/EAM/IWMS landscape — real, and worth stating plainly rather
than modestly, with the standing caveat that "stronger design" and
"battle-tested in production" are different claims and only one of
them is currently true for xolu. Where TRIRIGA's periodic-refresh
model earned a second look: `loc-00-design.md` §5c's boundary-mutation
reconciliation considered, and did not fully rule out, a
simpler periodic-rescan trigger as a lower-effort alternative to
event-triggered reconciliation — noted explicitly in the design
discussion that produced §5c as a legitimate option TRIRIGA's own
production experience suggests is viable, even though the mechanism
ultimately specified (`chronicle.RebuildOracle`-shaped, triggered
after a geometry `PATCH`) is event-driven, not periodic.

## IBM TRIRIGA / Maximo Spatial — CAD/BIM import

**Rejected — correctly, against the actual competitive set.**
TRIRIGA's space management and Maximo Spatial both build on imported
CAD drawings, BIM models, and (for Maximo Spatial specifically) a full
ArcGIS/Esri GIS layer. `loc-00-design.md` §13 names this as an
explicit non-goal. Checked against `/loc`'s actual competitive tier
before treating this as settled: none of Fiix, UpKeep, or Limble —
the mid-market CMMS systems Seam AMS actually competes with — have
CAD/BIM import either. Rejected relative to the enterprise tier
(TRIRIGA, Maximo Spatial), not rejected relative to anything `/loc`
needs to match to compete where it actually competes.

## The IFC / buildingSMART standards — placement and georeferencing

**Adopted directly, not reinvented.** `loc-00-design.md` §3b's entire
`Placement`/`GeoAnchor` model — an offset-and-rotation transform
relative to the parent, terminating at an absolute WGS84 anchor with
true-north rotation — is IFC's `IfcLocalPlacement`/`IfcSite` model,
adopted outright rather than designed from a blank page, on the
strength of two independent confirmations that both TRIRIGA and
Maximo build their own indoor placement on the identical standard via
a Revit/IFC connector rather than inventing their own coordinate
scheme (Naviam, Quantum Strides — both cited in `loc-00-design.md`'s
own References). The buildingSMART forum's own documented caveat about
precision loss for components far from a project's local origin
directly informed §4e's numerics doctrine as a general "watch-item" —
later given a specific, concrete instance by the real near-pole
division bug found in `pkg/loc/placement.go` (`loc-00-addendum.md`
entry 13), which the general watch-item language named the right
category of risk for without predicting the exact form it would take.
thinkmoult.com's caution against mixing coordinate-reference-systems
within one placement tree informed the mixed-CRS-anchor warning design
(`loc-01-rest-api.md` §1) — **not fully carried through to
implementation**, confirmed while researching this document: the
`Warnings` field exists in the shipped v0.21.0 response struct but is
never populated by any code path (`loc-01-rest-api.md` §1/§2's own
notes, added 2026-08-02). The design decision stands; the detection
logic itself was never built.

## Commercial RTLS/geofencing platforms (Litum, Navigine, Inpixon, LocaXion, SmartX HUB)

**Confirmed, not adopted — validated `/loc`'s own scope boundary
rather than changing it.** Every commercial platform researched
integrates *into* a CMMS/EAM via API rather than the EAM building its
own positioning stack — Maximo licenses ArcGIS, TRIRIGA integrates
Cisco DNA Spaces and Esri ArcGIS Indoors, and the RTLS vendors
themselves exist specifically because sub-metre UWB precision and
cheaper BLE zone-level accuracy are a hardware-fusion problem
different in kind from anything a CMMS should own. `/loc` was never
designed to produce coordinates, only consume them (`loc-00-design.md`
§2), and this research is why that boundary is stated with confidence
rather than left as an assumption: it's the same boundary every
researched market leader draws, not a `/loc`-specific limitation.
Also confirmed, on the other side: RTLS-vendor geofencing is
overwhelmingly *alerting* (a webhook fires after a crossing, security
gets notified) rather than *access control* (nothing in any
researched platform physically refuses a crossing) — the same
distinction as the TRIRIGA finding above, generalised. `/loc`'s own
fence capacity guard, where it exists, refuses the crossing at write
time; nothing found in this research does that natively anywhere in
the commercial RTLS landscape.

## Yard/dock capacity and logistics planning (Oracle OTM, general TMS/YMS practice)

**Confirmed the problem; rejected the planning-time-only
implementation.** "Cubing out" and "weighing out" — a container
filling by volume before weight, or the reverse — are established,
real industry terms, confirming `/obj` §7's dual-constraint capacity
CAS targets a genuine, named problem rather than an invented one.
Where this was actually implemented in researched systems (Oracle
OTM's location-capacity bulk planning), it's consumed by an
optimisation engine deciding shipment timing ahead of time — a
scheduling input, not a live write-time refusal. `/obj`'s own
capacity guard was kept as a transactional CAS specifically because
the actual worked example this research was checked against (a
loading dock limited to three trucks) needs to refuse the fourth truck
*now*, at the moment it attempts to dock, not flag a scheduling
conflict for a planner to resolve later. This is the third independent
confirmation of the same pattern in this document (TRIRIGA, RTLS
geofencing, now logistics planning) — worth trusting more as a
generalisation after three independent data points than after one.

## Mid-market CMMS (Fiix, UpKeep, Limble)

**Confirmed — established the actual competitive baseline rather than
the aspirational one.** Location hierarchies exist in all three, but
are largely descriptive/organisational — asset-to-location
association and multi-site filtering for reporting, not a
transactionally-guarded capacity or geofencing primitive. One source
described "visual floorplans that show asset location, condition, and
status in real time" as a differentiator for "top-performing teams,"
implying it's not standard even at this tier. This is the finding
that calibrates every "rejected, enterprise-tier-only" entry above:
`/loc`'s actual competitive set doesn't have live capacity guards,
geofencing, or CAD-derived floor plans either, so gaps relative to
Maximo/TRIRIGA are gaps relative to the ceiling of the market, not
gaps relative to what Seam AMS needs to compete where it actually
competes.

## SQLite R*Tree and Geopoly

**Adopted directly, verified empirically rather than assumed from
documentation.** `loc-00-design.md` §6b's SQL-plane spatial pre-filter
is SQLite's own R*Tree virtual-table module (bounding-box range
queries, the module's own worked example is a circle) with the
Geopoly extension built directly on it (simple 2D polygons in
GeoJSON, bounding-box pre-filter, exact resolution via
`geopoly_contains_point`). Not taken on faith from the C library's
documentation: since xolu runs `modernc.org/sqlite` (a transpiled,
CGo-free port, not the reference implementation), whether these
extensions were actually compiled in was a genuinely open question
until tested directly — a scratch Go module against the exact pinned
`v1.29.0`, confirming `ENABLE_RTREE`/`ENABLE_GEOPOLY` and working
`CREATE VIRTUAL TABLE` statements before the design relied on either.
Confirmed still true and load-bearing in the shipped implementation:
`CHANGELOG.md`'s v0.21.0 entry states the R-tree pre-filter is real,
not a fallback, and names a specific test proving the pre-filter is
never trusted alone (a point inside a circle's bounding box but
outside the circle itself correctly resolves as non-member).

---

## Summary: what came from outside versus what was already there

The pattern worth naming explicitly, visible only by looking across
every entry above at once: external research in this project mostly
**confirmed or sharpened** decisions already reached through internal
adversarial testing (`obj-loc-stress-test-findings.md`), rather than
**originating** them. The Location/Asset split, the three-state
lifecycle, the live-transactional capacity guard, and the `/loc`
scope boundary against RTLS were all designed first and validated by
industry precedent second. The two clear exceptions — where research
directly produced a decision rather than confirming one — are IFC's
placement model (adopted wholesale, because inventing a competing
coordinate scheme would have been pure waste against an
already-solved, already-adopted-by-the-nearest-competitors problem)
and IAS's general templating idea (adopted in spirit, though its own
specific mechanism was rejected in favour of a pattern the codebase
already had). Worth keeping this distinction visible for whoever reads
this document next: it is evidence the design choices survive contact
with the industry, not evidence the industry supplied the design.
