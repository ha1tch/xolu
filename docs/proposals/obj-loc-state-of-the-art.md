# `/obj` and `/loc` — state of the art, and where our design stands

Updated: 2026-08-02. Purpose-built for colleagues who weren't in the
design sessions: everything here is explained from scratch, every
vendor and every piece of jargon defined on first use. This is not a
competitive teardown of other companies' software. Seam AMS is a new
product built on a new model; the honest way to build something new is
to study what the best existing systems do, take what's genuinely
good, and be explicit about what's deliberately different and why.
Companion to two shorter documents already in this series —
`obj-loc-industry-practices.md` (a terser, source-by-source adopted/
rejected record) and `obj-loc-stress-test-findings.md` (the internal
design review that produced `/obj` in the first place) — this document
is the long-form version meant to stand on its own for someone who
hasn't read either.

One framing worth stating before anything else: **we are not
comparing ourselves to see who wins.** We're comparing ourselves to
make sure our design still makes sense five years from now, once it's
been hit by real deployments, real edge cases, and real operator
mistakes the way every system discussed below already has been. A
design that looks good today and can't absorb what we learn from
other systems' twenty-year histories is a design that goes stale on
contact with reality. That's the actual point of this document.

---

## Part 1 — What we built, in plain terms

### The problem, before any of the jargon

A maintenance/asset-management system (a "CMMS" — Computerized
Maintenance Management System — or "EAM" — Enterprise Asset
Management — the two terms are used almost interchangeably in the
industry, EAM usually implying a bigger, more expensive product) needs
to answer two questions constantly: **where is something**, and **what
is inside something else**. Those sound like the same question. They
aren't, and getting them tangled together is where most of the pain
documented later in this file comes from.

### `/loc` — where fixed things are, and what boundaries exist

`/loc` is xolu's primitive for **places**: a site, a building, a
floor, a room, a loading dock, a fenced yard. Places don't move (a
room doesn't relocate itself), and they don't get created or
restructured often — you set up your warehouse layout once, and it
stays mostly the same for years. `/loc` also owns **fences** —
geometric boundaries (a circle or a polygon) that things can be
"inside" or "outside" of, with an optional hard capacity ceiling (a
dock that holds at most 3 trucks, refusing the 4th at the exact
moment it tries to enter, not after the fact).

### `/obj` — where movable things are, and what's inside them

`/obj` is xolu's primitive for **things that move and can contain
other things**: a pallet, a shipping container, a lorry, a returnable
barrel. These change constantly — a pallet gets loaded and unloaded
dozens of times a shift — and they can be *inside* each other (cases
inside a pallet, a pallet inside a lorry), with the containment itself
changing just as often as the position does.

### Why two primitives, not one

The distinguishing fact is **how often the structure itself changes**.
A warehouse's room layout changes maybe once a year — administrative,
planned, rare. A pallet's contents change every few minutes —
ordinary, constant, operational. A system built to handle rare,
planned structural change safely (which needs to guard against
accidentally corrupting a stable structure) has different needs from
a system built to handle constant, high-frequency change safely
(which needs to guard against two forklifts racing to load the same
truck at once). Building one primitive that tried to do both well
turned out, in our own early design work, to do neither well — this
is recorded in detail in `obj-loc-stress-test-findings.md`.

### The one thing both primitives insist on: refuse it now, not later

Both `/loc` and `/obj` can enforce **capacity** — a hard limit on how
much can be in a place or in a container — and they enforce it *at
the exact moment* something tries to exceed it, refusing the
operation outright rather than allowing it and flagging the problem
afterward. This turns out to be the single most distinctive thing
about our design compared to every other system researched for this
document, and it's covered in depth in Part 3.

---

## Part 2 — Vendor and term glossary

Read this section once; every term here is used without re-explanation
in Part 3.

- **IBM Maximo** — the most widely deployed EAM product in heavy
  industry (oil & gas, utilities, manufacturing). Owned by IBM. The
  system most of this document compares against, because it has the
  richest publicly documented feature set and the largest body of
  practitioner writing about its real-world behavior.
- **Item Assembly Structure (IAS)** — a specific feature *inside*
  Maximo, not a separate product. A reusable template listing the
  sub-parts a kind of asset is made of (a 5-horsepower motor's IAS
  might list an impeller, a seal, a bearing set); "applying" an IAS to
  a real asset record creates matching child records automatically.
- **Functional Location (FLOC)** — Maximo's rough equivalent, "Location," but this
  term is actually from **SAP**, IBM's biggest EAM competitor. SAP's
  Plant Maintenance module (**SAP PM**, part of SAP's larger ERP
  suite) uses Functional Locations the way Maximo uses Locations — a
  fixed installation point in a hierarchy.
- **IBM TRIRIGA** — a separate IBM product from Maximo, focused on
  facilities and real-estate management (space planning, occupancy,
  move requests) rather than maintenance work orders. Sometimes
  integrated with Maximo, sometimes used standalone.
- **IWMS** — Integrated Workplace Management System, the product
  category TRIRIGA belongs to, alongside competitors **Planon** and
  **Archibus**. IWMS products focus on space and occupancy, not
  maintenance — the closest category to `/loc`'s own capacity/fence
  concept, though built for buildings and desks, not industrial
  equipment.
- **RTLS** — Real-Time Location System: hardware-plus-software for
  tracking tagged people or things indoors, where GPS doesn't work.
  **Litum**, **Navigine**, **Inpixon**, **LocaXion**, and **SmartX
  HUB** are commercial RTLS vendors researched for this document —
  none of them are EAM/CMMS companies; they sell tracking technology
  that *integrates into* systems like Maximo, TRIRIGA, or a warehouse
  system.
- **YMS** — Yard Management System: software specifically for
  scheduling trucks, trailers, and dock doors at a distribution
  facility. **C3 Solutions** is a real YMS vendor cited directly in
  Part 3. **Oracle Transportation Management (Oracle OTM)** is a much
  larger logistics-planning product from Oracle that includes
  yard/dock capacity planning as one feature among many.
- **WMS** — Warehouse Management System: software for managing bins,
  picking, and inventory movement inside a warehouse. Distinct from a
  YMS (which manages the yard/trucks outside the building) and from a
  CMMS/EAM (which manages maintenance of the equipment, not the
  inventory itself).
- **Fiix, UpKeep, Limble** — mid-market CMMS products, the tier Seam
  AMS actually competes in, as distinct from Maximo/SAP's
  enterprise tier.
- **IFS** — IFS Cloud (formerly IFS Applications), another major
  EAM/ERP competitor to Maximo and SAP, historically strong in
  aerospace, defense, and other asset-heavy industries.
- **Oracle EAM** — Oracle's own Enterprise Asset Management module,
  a different Oracle product from Oracle OTM (above) — one does
  maintenance/asset management, the other does transportation
  planning. **Oracle Property Manager** is a further separate Oracle
  product that owns Oracle EAM's own location hierarchy.
- **Siemens Teamcenter** — Siemens' PLM (Product Lifecycle
  Management) product — engineering and CAD data, not maintenance
  work orders. Siemens doesn't sell a direct EAM competitor to
  Maximo/SAP/IFS/Oracle; its relevant offering, **CALM** (Capital
  Asset Lifecycle Management), instead synchronizes engineering data
  *into* those other vendors' systems.
- **IFC / buildingSMART** — Industry Foundation Classes, an open
  standard (maintained by the buildingSMART organization) for
  representing buildings and their contents in software — the
  standard `/loc`'s own placement model is built on, covered in Part
  4.

---

## Part 3 — System by system: what each does, and what we found

### IBM Maximo — the core Location/Asset model

**What it does.** Maximo splits "where" (Location — fixed, doesn't
move) from "what" (Asset — the physical thing, has its own history,
gets reinstalled elsewhere). Locations form a tree: site, then plant
or vessel, then area or system, then the functional installation
point. This structural split — independently, we found — is the same
one we arrived at for `/loc` vs `/obj`.

**Where it's genuinely good, and what we adopted.** The Location/Asset
split itself. Confirming a design decision that already existed
independently is more valuable than it sounds — it's evidence the
split solves a real, general problem, not an artifact of how our own
test scenarios happened to be worded.

**Where the seams show — documented, not speculated.** Crossing a
Maximo *site* boundary (a bigger organizational unit than a plain
Location) is not an ordinary move. A practitioner's own detailed
account states it plainly: moving an asset to a different site sets
its status to **DECOMMISSIONED** in the old site and **recreates the
asset from scratch** in the new one — a new record, a new identity.
For an asset with a hierarchy of child assets underneath it, every
child gets decommissioned and recreated too — described in the same
account as a heavy operation worth taking care around. Maintenance
schedules don't reliably survive the move either: only the PM records
tied to a shared master template get recreated on the new side, the
rest left behind; a reader comment on the same post separately
reports the move operation simply stalling for hours with no error
shown at all. A second, separate source — an official IBM defect
record, APAR IZ48070 — documents that moving an asset through
Maximo's integration API doesn't reliably write to the move-history
table at all, with IBM's own response stating plainly: "We don't
support Asset/Location Move History (Creating records in the history
table)."

**Why this matters for us, precisely.** `/loc`'s own design keeps a
subject's identity completely separate from where it currently sits —
an internal, permanent key that never changes, with the
human-readable string id and the current tree position both able to
change independently of it (`loc-00-design.md` §11a). Crossing any
boundary, including a tenant boundary in the rare case that's ever
needed, was deliberately never designed to require destroying and
recreating the record — because Maximo's own documented experience is
the clearest evidence available of what happens when identity and
position get tangled together: history gets silently lost, on two
separate documented occasions, in two different Maximo subsystems.

### SAP PM — Functional Locations

**What it does.** SAP's equivalent of Maximo's Location hierarchy.
The distinguishing detail: a Functional Location's hierarchical
position is encoded **directly into its own identifier string** — an
"edit mask" defines which characters represent which hierarchy level,
so `PLANT-01-PROD-LINE01-CONV01` *is* the location's id, and that id
literally spells out its position in the tree.

**Where the seams show.** A real SAP practitioner's own blog post
("Lessons learned from EAM Enterprise Structure and Master Data")
describes what happens when a hierarchy needs restructuring: every
maintenance item, open order, and notification tied to the old
locations has to be found and manually repointed at the new ones, and
in the author's own case this left two parallel hierarchies
representing the same physical system, with maintenance history the
author describes as "not consistent" between them afterward. A
separate, live SAP Community forum thread shows a company mid-process
of splitting one Functional Location hierarchy into two, asking in
real time what breaks downstream — this is not a historical problem,
it's one practitioners are still actively working through.

**Why this matters for us, precisely — the sharpest single piece of
evidence in this whole document.** SAP's identifier baking in
hierarchy position is exactly the failure mode `/loc`'s two-identity
split (an external string id, a separate internal dense key, neither
one encoding tree position) was built to structurally prevent.
Reparenting a `/loc` node changes one column (`parent_key`); the
node's own identity, and everything referencing it — journal entries,
fence memberships, everything — is completely untouched, because
nothing about that identity ever encoded where in the tree the node
sat. SAP's own practitioner community independently discovered, the
hard way, exactly the problem this design choice exists to avoid.

### IFS Cloud — two overlapping hierarchies, and the same identity problem a third time

**What it does.** IFS Cloud (formerly IFS Applications) is a major
EAM/ERP competitor to Maximo and SAP, historically strong in
aerospace, defense, and other asset-intensive industries. It offers
both a Functional Structure and a separate Location Structure for
representing the same assets.

**Where the seams show — two findings, both from IFS's own community
forum.** First, genuine, first-hand user confusion about why both
structures exist at all: one practitioner's question states plainly
that they expected the Location Structure to just be "a different
view of the same assets" organized around locations, and asks what it
actually adds beyond a visual layout that the Functional Structure
couldn't already provide — a real user, mid-implementation, unable to
articulate a functional reason to maintain two parallel hierarchies
for the same physical things. Second, and sharper: a separate thread
describes a legacy pattern where an object's own visible identifier
encodes exactly which facility and process area it sits in, so moving
the object requires renumbering the identifier itself — the same
identity-encodes-position anti-pattern already found independently at
SAP, now confirmed a third time, at a different vendor, with a
different codebase. The thread's own question is telling: whether a
newer internal key IFS added in a recent release could finally let the
visible identifier stay stable across a move — IFS's own user base
independently arriving at the need for a two-identity split, uncertain
whether a recent feature actually delivers one.

**Why this matters for us, precisely.** Two things converge here that
matter differently. The identity question is the third independent
confirmation (Maximo implicitly, SAP explicitly, now IFS) that baking
position into identity is a recurring, cross-vendor mistake — not a
SAP-specific quirk, a pattern. The dual-hierarchy confusion is a
different, adjacent lesson: `/loc` keeps exactly one primary,
geometric containment tree, and anything that needs a second,
non-geometric way to group the same locations (a circuit, a service
zone, a reporting rollup) is a `/graph` edge, not a second competing
native hierarchy — the same reuse-discipline argument already applied
to Maximo's own Network Systems. IFS's confusion is what happens when
that second need gets solved as a first-class, parallel structure
instead.

### Oracle EAM — location and asset as genuinely separate products

**What it does.** Oracle's Enterprise Asset Management module
(distinct from Oracle Transportation Management, already covered
above) — Oracle's own competitor to Maximo/SAP/IFS in this space.

**Where the seams show.** Oracle's own documentation describes how
location data reaches eAM at all: locations are defined in a
*separate Oracle product*, Property Manager, and reach eAM only
through an explicit, asynchronous export step — Property Manager's
own hierarchy is pushed into eAM via a batch process, not read live
from one shared model. A real practitioner's own support-forum
question shows the resulting uncertainty directly: after moving an
asset's location inside eAM, they weren't sure whether that same
change had correctly propagated into Oracle's separate Fixed Assets
module, and asked the community rather than trusting the system to
have kept the two in sync. A second practitioner thread documents a
genuine, awkward constraint discovered only by searching Oracle's own
support articles: transferring an asset between organizations
requires the asset record to be flagged transactable and stocked in
inventory — not applicable to their actual case, forcing a workaround.
A third source, Oracle's own implementation documentation, shows the
shape of that workaround pattern generally: representing a
non-physical hierarchy placeholder (a location, area, or department
that isn't really an asset) requires deliberately creating a disabled,
non-maintainable, non-transactable "asset" record just to hold a
place in the tree.

**Why this matters for us, precisely.** Every one of these is the same
underlying gap, from a different angle: Oracle's location and asset
concepts don't live in one model at all, they live in two products
bridged by a batch job, so whether a change has actually taken effect
everywhere it should have becomes a real, standing question a user
has to ask rather than a guarantee the system provides. `/loc`'s own
non-postable interior nodes (§3a) exist natively, specifically so a
pure organisational placeholder never needs a fake positioned "thing"
standing in for it the way Oracle's workaround requires.

### Siemens — not a competitor in this space, and its own product line shows why that gap exists

**What it does.** Siemens' relevant product is Teamcenter, a PLM
(Product Lifecycle Management) tool — fundamentally about engineering
and CAD data, not maintenance work orders. Siemens doesn't sell a
Maximo/SAP/IFS/Oracle-equivalent EAM product directly; instead it
sells "Capital Asset Lifecycle Management" (CALM), whose explicit job
is *handing data over* to separate EAM systems.

**The finding is in Siemens' own marketing copy, which is unusually
revealing precisely because it isn't trying to document a problem —
it's trying to sell the fix for one.** Siemens' own description of
CALM states that it synchronizes Teamcenter's engineering-side plant
data with external EAM tools, naming SAP Plant Maintenance and IBM
Maximo specifically as the destinations, and states this synchronization
process is what saves "hours of manual entry and validation" —
meaning, by Siemens' own account, populating a functional-location
hierarchy in those systems from engineering data is a genuinely
manual, hours-long, error-prone task without a dedicated product built
to automate it. A second piece of Siemens' own material names the
deeper reason this gap exists at all: plant/process manufacturers
address their equipment by physical "tags," while discrete
manufacturers address theirs by part numbers inside a bill of
materials, and plant data itself needs viewing multiple ways — plant,
area, unit — that don't reduce to one convention.

**Why this matters for us, precisely.** This is the clearest evidence
in this whole document that "location" and "what something is
composed of / how it's addressed" being separate concerns, living in
separate *products*, is not a minor integration detail anywhere in
this industry — it's substantial enough that a company the size of
Siemens sells a dedicated product line specifically to bridge it, and
markets the hours saved as the headline value. `/loc` and `/obj` are
two primitives for exactly that same underlying split (place versus
composition) — but they share one subject-addressing convention, one
guard-locality discipline, and compose directly rather than requiring
a synchronization layer between them at all. The industry's version of
solving this problem is an entire product category. Ours is meant to
be two primitives agreeing on how to name things.

### IBM TRIRIGA — occupancy monitoring and space management

**What it does.** Tracks how full a space is using indoor positioning
hardware (Cisco DNA Spaces heatmapping), refreshing roughly every 15
minutes, and alerts facility staff when occupancy crosses a threshold.

**Where it's genuinely good.** The idea that occupancy matters and
should be monitored is correct, and confirms `/loc`'s own capacity
concept targets a real problem.

**Where it differs from ours, and why that's not automatically a
flaw.** TRIRIGA's occupancy check is *monitoring*, not *access
control* — it tells you a room is over capacity after the fact,
on a periodic refresh cycle; it never physically prevents the
crossing. `/loc`'s capacity guard refuses the entry at the instant
it's attempted, a stronger correctness guarantee for the operational
case. TRIRIGA's periodic-refresh approach is genuinely a reasonable,
lower-cost alternative for cases where "eventually consistent within
15 minutes" is good enough — worth keeping in mind rather than
assuming a live guard is always the better trade-off; it depends what
you're protecting.

### RTLS vendors (Litum, Navigine, Inpixon, LocaXion, SmartX HUB)

**What they do.** Sell the actual hardware-and-software stack for
tracking tagged objects or people indoors — the part GPS can't do.
None of them are CMMS/EAM companies; every one exists specifically to
be integrated *into* a system like Maximo, TRIRIGA, or a WMS.

**What this confirms about our own design.** `/loc` was deliberately
never built to produce coordinates, only to consume them — the same
boundary every vendor here draws, and every EAM/IWMS product
researched (Maximo licenses Esri's ArcGIS rather than building its
own GIS engine; TRIRIGA integrates Cisco DNA Spaces and Esri ArcGIS
Indoors rather than building indoor positioning itself). This is
strong, convergent evidence the boundary is correctly drawn, not an
assumption.

**Where the seams show.** A peer-reviewed academic study ("Cost and
Complexity as Barriers to RTLS Adoption in SMEs," Moeini and Coates,
McGill University) found that cost and installation complexity are a
genuine, measured barrier to smaller companies adopting RTLS at all —
not a solved problem, an actively studied one. And every commercial
RTLS platform researched treats a geofence crossing as something to
*alert on*, not something to *refuse* — nothing in the RTLS landscape
physically prevents a crossing the way `/loc`'s own fence-capacity
guard can.

### Yard/dock scheduling (C3 Solutions, a real YMS vendor; Oracle OTM)

**What it does.** Schedules which truck gets which dock door at which
time.

**Documented pain point, from the vendor's own marketing content —
worth reading precisely because a vendor admitting this about the
category is unusually candid.** C3 Solutions' own blog post on
double-booked dock appointments describes the actual mechanism
plainly: most setups don't stop a conflicting booking from being
created at all, they only record it, so the conflict isn't visible
until well after the damage is done. The most common trigger it names
is two coordinators editing the same shared schedule at once — not,
in the post's own framing, because either person is worse at their
job, but because, as it puts it, "the system can't handle two
simultaneous editors." That phrase doesn't use the words "race
condition," but that's precisely what's being described: two writers,
no atomic check, a conflict that only becomes visible after both
writes have already landed.

**Why this matters for us, precisely.** This is the exact class of
bug `/obj`'s and `/loc`'s CAS-based (Compare-And-Swap — a single
atomic database operation that checks a condition and applies a write
together, so nothing can slip in between the check and the write)
admission guard exists to make structurally impossible, not just less
likely. A fourth truck attempting to enter a 3-truck dock doesn't get
recorded and flagged later — the write itself fails, atomically,
every time, regardless of how many concurrent attempts are racing
each other. Oracle OTM, researched separately, confirms the same
industry pattern at a larger scale: its own capacity constraints feed
an optimization engine that plans shipments *ahead of time* — a
scheduling input, not a live, write-time refusal.

### Mid-market CMMS (Fiix, UpKeep, Limble)

**What it does.** The tier of CMMS software Seam AMS actually
competes against — smaller, cheaper, more limited than Maximo or SAP.

**Why this section exists.** Every "we're behind Maximo" finding
elsewhere in this document needs this counterweight: none of Fiix,
UpKeep, or Limble have live capacity guards, geofencing, or
CAD-derived floor plans either. One researched source described
"visual floorplans that show asset location, condition, and status in
real time" as a differentiator for "top-performing teams" — implying
it's not standard even at this tier. Gaps relative to Maximo's top
tier are gaps relative to the ceiling of the market, not gaps
relative to what Seam AMS needs to compete where it actually competes.

### Warehouse Management Systems (WMS) — bin and location capacity

**What it does.** Manages bins, picking routes, and inventory
placement inside a warehouse — a category adjacent to but distinct
from CMMS/EAM. Researched specifically for documented pain points
around bin capacity and concurrent access; the available public
material was largely vendor marketing rather than practitioner
accounts, so this section is honestly thinner than the others rather
than padded with weak evidence. What is consistent across every
source found: bin capacity is treated as a planning constraint feeding
slotting algorithms, the same planning-not-guard pattern already
confirmed independently for Oracle OTM and TRIRIGA.

### The IFC / buildingSMART standard — not a vendor, a foundation

**What it is.** An open, vendor-neutral standard for representing
buildings digitally — not a product, something both Maximo and
TRIRIGA build their own indoor-positioning features *on top of* via a
Revit/IFC connector, rather than each inventing a competing coordinate
scheme.

**What we adopted, directly.** `/loc`'s entire placement model — an
offset-and-rotation transform relative to a parent, terminating at an
absolute geographic anchor — is IFC's own `IfcLocalPlacement`/
`IfcSite` model, taken deliberately rather than invented from a blank
page, on the strength of both Maximo and TRIRIGA independently
building on the same foundation.

---

## Part 4 — Edge cases the incumbents can't model, or can't model *together*

Three ways a case can defeat an incumbent system, worth distinguishing
precisely rather than lumping together as "can't do it":

**Cannot model at all.** SAP's Functional Location identifiers can't
represent a hierarchy reorganization without breaking history — not a
missing feature, a structural consequence of how identity works in
that system. Maximo's Location/Asset split has no mechanism for a
location's own root to be anchored to a moving asset's position — the
connection between the two only ever runs one direction (an asset
references its current location, never the reverse).

**Cannot model without integrating a separate product.** Live indoor
positioning, in every system researched, requires bolting on a
third-party RTLS platform (Litum, Navigine, Cisco DNA Spaces, Esri
ArcGIS Indoors) — never native. This is not a criticism (`/loc` draws
the identical boundary, deliberately), but it means anything spanning
"where exactly is this, live, right now" and "what work order does
this affect" crosses a product boundary in every incumbent system
researched, with the integration itself being a real, separate cost
and failure surface.

**Cannot model easily, even natively.** Maximo's decommissioning
workflow (status flag plus a move to a synthetic location) technically
represents "this asset is gone," but real practitioner accounts
describe it as "clunky," citing inventory counts skewed by
decommissioned assets and a real, documented defect allowing an asset
to be decommissioned while still referenced on an open work order. The
capability exists; using it correctly requires working around its own
rough edges.

**What's easy for us that's hard or impossible for all three
categories above, concretely:**

- A ship's cargo hold, positioned relative to the ship's own live,
  moving location, with everything inside it — containers, pallets,
  cases — resolving automatically through that chain with zero extra
  writes when the ship moves. No incumbent researched connects a
  location subtree's root to a moving asset's position at all.
- Atomically converting a bulk inventory count into one
  individually-tracked, positioned object — decrement the count,
  create the record, position it — as one indivisible operation,
  reusable anywhere the boundary between bulk and individual tracking
  needs crossing. Maximo's nearest equivalent (receive, autonumber,
  apply an IAS) is a multi-step workflow specific to receiving, not a
  general operation.
- One transaction spanning a work-order state change and a
  capacity-guarded location admission, committing or refusing as a
  single unit. Every system researched keeps its work-order engine and
  its location/GIS layer as separate products connected by application
  code, never one atomic commit.
- Detecting drift when a fence's boundary itself changes shape after
  things are already inside it, using the same generic reconciliation
  mechanism (`chronicle.RebuildOracle`) already proven three times over
  elsewhere in the system — not a bespoke fix built just for this case.

**Bulk pattern application — applying one structure across many
assets or locations at once, the way Maximo's IAS stamps a motor's
parts list across ten physical motors — is covered.** This was the
only capability, across everything researched for this document, that
this research surfaced as worth having and this design hadn't already
covered. Worth being precise about what kind of gap it ever was: not
an edge case — the underlying end state (many correctly-configured,
individually-tracked records, each built from a shared definition) was
always reachable through the flat pattern mechanism already in this
design (`obj-00-design.md` §7a, `loc-00-design.md` §5d), one call per
object. Bulk application removes the need for that many calls; it was
never a question of whether the model could represent the result.

---

## Part 5 — Built for evolution, not just for today's edge cases

The question this document is really trying to answer isn't "are we
ahead right now" — it's "will this design still make sense after five
more years of real deployments finding real problems in it, the way
every system in Part 3 has already been through." Two structural
habits, visible across the whole design rather than in any one
feature, are the actual answer:

**Nothing gets solved twice, under a different name, by accident.**
`chronicle.RebuildOracle` — the mechanism for detecting when a derived
view has drifted from its source of truth — is used identically by
`bal`'s rollup, `pkg/storage`'s graph consistency check, and two
separate places inside `/loc`. The definition-snapshot-with-lineage
pattern (clone a definition into something that used it, keep a
pointer back, track deletion of the source) is used identically by
`fsm`, `dxp`, and now `/obj`'s and `/loc`'s own patterns. When the
next primitive after `/obj` needs either of these, it reuses the same
mechanism rather than inventing a third version. Part 3 shows what the
alternative costs, and by now it isn't one data point: SAP's
Functional Location identifiers, an IFS customer's own legacy
identifier scheme, and Maximo's site-crossing behavior are three
unrelated vendors making the identical mistake independently — baking
position into identity — and Oracle's split between Property Manager
and eAM, plus Siemens' entire CALM product line existing specifically
to bridge PLM and EAM, show the same root cause one level up: each
subsystem solved "how do I represent this" its own way, and the seams
between those separate solutions are precisely where the documented
pain points in Part 3 live, across five of the largest vendors in this
industry, not one.

**Every deferred capability is named, not silently dropped.** The
discretely-repositioned-anchor residual and the guarantee-strength gap
between a substrate guard and an application-enforced policy are not
secretly assumed solved. They're written down as open items, in the
design documents themselves, specifically so the day a real deployment
needs one of them, the answer is "we already know this is missing and
roughly what it would take," not a surprise discovered in production
the way IBM's
own APAR IZ48070 or the SAP practitioner's fragmented hierarchy were
discovered.

Whether this design is still the right one in five years isn't
something this document can prove — no document can. What it can do
is point at what already broke for the systems that came before it,
take their genuinely good ideas, leave out their documented failure
modes, and name plainly where it still doesn't have an answer yet.
