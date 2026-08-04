# `/obj` and `/loc` — worked examples

Fifteen full sequences, starting small and growing progressively:
three simple, four average, four complex (multi-primitive, `dxp`-
coordinated), four edge cases. Every example is written as one
continuous request/response walkthrough — read top to bottom, each
call's response is the input the next call depends on, nothing is a
disconnected snapshot. Consistent conventions throughout: tenant `1`
everywhere, `/api/v1` for bare entity creation, `/api/v2` for every
`loc`/`obj`/`bal`/`fsm`/`dxp` capability. Later examples reuse earlier
IDs where the scenario naturally continues (§11 picks up the pallet
from §2/§5; §13 reuses the same pallet again) rather than inventing
disconnected fixtures for every example.

---

## 1. Field technician `report` against a pre-fenced customer site

Simplest possible case: one primitive, one call, and the point is what
*doesn't* happen.

```json
POST /api/v2/tenant/1/loc/fences/attach

{
  "subject": "customers:acme-hq",
  "geometry": { "type": "circle", "center": { "lat": 40.7128, "lon": -74.0060 }, "radius_m": 150 }
}
```
```json
POST /api/v2/tenant/1/loc/report

{
  "subject": { "type": "REF", "entity": "technicians", "id": 12 },
  "point": { "lat": 40.7129, "lon": -74.0061 },
  "reported_at": "2026-08-02T09:00:00Z"
}
```
```json
200
{ "changed": true, "fences": ["customers:acme-hq"], "entered": ["customers:acme-hq"], "exited": [] }
```
A second report from the same spot ten minutes later:
```json
POST /api/v2/tenant/1/loc/report
{ "subject": { "type": "REF", "entity": "technicians", "id": 12 },
  "point": { "lat": 40.7129, "lon": -74.0061 }, "reported_at": "2026-08-02T09:10:00Z" }
```
```json
200
{ "changed": false, "fences": ["customers:acme-hq"] }
```
No journal entry, no event, nothing written on the second call — §8a's
whole point, made concrete rather than described.

---

## 2. One pallet: entity → attach → move → resolve

```json
POST /api/v1/tenant/1/pallets
{ "id": "88" }
```
```json
POST /api/v2/tenant/1/obj/attach
{ "subject": "pallets:88", "capacity": { "max_weight_kg": 1000, "max_volume_m3": 1.5 } }
```
```json
PUT /api/v2/tenant/1/obj/pallets/88/move
{ "to": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/yard/dock-zone" } }
```
```json
200
{ "moved": true, "resolved": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/yard/dock-zone" } }
```
```json
GET /api/v2/tenant/1/obj/pallets/88/position

{
  "resolved": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/yard/dock-zone", "lat": -34.9015, "lon": -56.1650 },
  "chain": ["pallets:88", "loc:site-mvd/bldg-a/yard/dock-zone"],
  "as_of": "live"
}
```

---

## 3. A location with a hard capacity ceiling, refused on the fourth entry

```json
POST /api/v2/tenant/1/loc/def
{ "location_id": "site-mvd/bldg-a/yard/dock-zone", "parent_id": "site-mvd/bldg-a/yard",
  "name": "Dock 2", "postable": true, "capacity": 3,
  "placement": { "offset_x": 12.0, "offset_y": 4.0, "offset_z": 0.0, "rotation": 0.0 } }
```
Three trucks move in cleanly:
```json
PUT /api/v2/tenant/1/obj/trucks/1/move
{ "to": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/yard/dock-zone" } }
```
```json
200 { "moved": true }
```
(identically for `trucks:2`, `trucks:3` — both `200`). The fourth:
```json
PUT /api/v2/tenant/1/obj/trucks/4/move
{ "to": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/yard/dock-zone" } }
```
```json
409
{ "error": { "code": "XOLU-LOC002", "message": "leaf at capacity",
             "status": 409, "location_id": "site-mvd/bldg-a/yard/dock-zone" } }
```
`trucks:4` stays wherever it was. No partial state, no queue — refused
outright, same CAS shape every capacity guard in this design uses.

---

## 4. Vehicle fleet, continuous GPS, crossing a standalone service-radius fence

```json
POST /api/v2/tenant/1/loc/fences/attach
{ "subject": "zones:svc-radius-hq",
  "geometry": { "type": "circle", "center": { "lat": -34.9011, "lon": -56.1645 }, "radius_m": 20000 } }
```
A delivery van, tracked continuously, currently outside the radius:
```json
POST /api/v2/tenant/1/loc/report
{ "subject": { "type": "REF", "entity": "vehicles", "id": 47 },
  "point": { "lat": -35.10, "lon": -56.30 }, "reported_at": "2026-08-02T07:00:00Z" }
```
```json
200 { "changed": false, "fences": [] }
```
An hour later, now inside:
```json
POST /api/v2/tenant/1/loc/report
{ "subject": { "type": "REF", "entity": "vehicles", "id": 47 },
  "point": { "lat": -34.95, "lon": -56.20 }, "reported_at": "2026-08-02T08:00:00Z" }
```
```json
200
{ "changed": true, "fences": ["zones:svc-radius-hq"], "entered": ["zones:svc-radius-hq"], "exited": [] }
```
The `entered` fact is what a consuming application (dispatch, billing
for in-radius service) watches the event feed for — nothing inside
`/loc` itself acts on it.

---

## 5. A pallet with real contents, repositioned as one unit

Continuing from §2 — `pallets:88` is already `attach`ed and positioned.
Three cases get created and loaded:
```json
POST /api/v1/tenant/1/cases
{ "id": "4471", "lot_code": "L4471" }
```
```json
POST /api/v2/tenant/1/obj/attach
{ "subject": "cases:4471", "capacity": null }
```
```json
PUT /api/v2/tenant/1/obj/cases/4471/move
{ "to": { "kind": "obj", "subject": "pallets:88" } }
```
```json
200 { "moved": true, "resolved": { "kind": "obj", "subject": "pallets:88" } }
```
(identically for `cases:4472`, `cases:4473`). Now the pallet itself
moves — every contained case follows automatically, because their
position is resolved *through* the pallet's, not stored independently:
```json
PUT /api/v2/tenant/1/obj/pallets/88/move
{ "to": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/warehouse/aisle-7/bay-3" } }
```
```json
GET /api/v2/tenant/1/obj/cases/4471/position

{
  "resolved": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/warehouse/aisle-7/bay-3" },
  "chain": ["cases:4471", "pallets:88", "loc:site-mvd/bldg-a/warehouse/aisle-7/bay-3"],
  "as_of": "live"
}
```
One `move` call on the pallet; the case's own chain shows it never
needed its own `move` at all.

---

## 6. An asset shipped to third-party repair — off-site as a first-class state

```json
PUT /api/v2/tenant/1/obj/vehicles/47/move
{ "to": null }
```
```json
200 { "moved": true, "resolved": { "kind": "unassigned" } }
```
```json
GET /api/v2/tenant/1/obj/vehicles/47/position

{ "resolved": { "kind": "unassigned" }, "chain": ["vehicles:47"], "as_of": "live" }
```
Not an error, not a synthetic "REPAIR-VENDOR" location standing in for
"we don't know" — `unassigned` is an ordinary, valid resolution. Six
weeks later it comes back:
```json
PUT /api/v2/tenant/1/obj/vehicles/47/move
{ "to": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/yard/dock-zone" } }
```

---

## 7. A lorry refused for weight while still under its volume limit, and the reverse

```json
POST /api/v1/tenant/1/lorries
{ "id": "5" }
```
```json
POST /api/v2/tenant/1/obj/attach
{ "subject": "lorries:5", "capacity": { "max_weight_kg": 5000, "max_volume_m3": 30 } }
```
A dense load — well under volume, over weight:
```json
PUT /api/v2/tenant/1/obj/pallets/9002/move
{ "to": { "kind": "obj", "subject": "lorries:5" } }
```
```json
409
{ "error": { "code": "XOLU-OBJ003", "message": "destination obj subject at capacity: max_weight_kg",
             "status": 409 } }
```
Unload some, retry with a bulky-but-light load instead — under weight,
over volume:
```json
PUT /api/v2/tenant/1/obj/pallets/9003/move
{ "to": { "kind": "obj", "subject": "lorries:5" } }
```
```json
409
{ "error": { "code": "XOLU-OBJ003", "message": "destination obj subject at capacity: max_volume_m3",
             "status": 409 } }
```
Same code, different dimension named in the message — either
constraint alone is sufficient to refuse, checked independently in the
same CAS.

---

## 8. `promote` — a `/bal` bulk count converts to one tracked `/obj` entity, atomically

```json
POST /api/v2/tenant/1/bal/define
{ "account_id": "pallet-88-cases", "scale": 0 }
```
```json
POST /api/v2/tenant/1/bal/transfer
{ "from": null, "to": "pallet-88-cases", "amount": "24" }
```
24 loose cases sitting as a bulk count. One gets pulled out for
individual tracking because it's damaged and needs its own record:
```json
POST /api/v2/tenant/1/obj/promote

{
  "bal_account": "pallet-88-cases",
  "amount": 1,
  "entity": { "kind": "cases", "existing_key": null,
              "create": { "lot_code": "L4471", "condition": "damaged" } },
  "position": { "kind": "obj", "subject": "pallets:88" }
}
```
```json
200
{ "promoted": true, "entity": "cases:4471", "bal_balance": "23" }
```
One commit: the count drops from 24 to 23, `cases:4471` is created,
and it's positioned inside `pallets:88` — never a window where the
count dropped but the entity didn't exist yet, or the reverse.

---

## 9. Ship → containers → pallets, the ship's own hold-tree anchored to its own position

```json
POST /api/v1/tenant/1/ships
{ "id": "1" }
```
```json
POST /api/v2/tenant/1/obj/attach
{ "subject": "ships:1", "capacity": { "max_weight_kg": 4000000, "max_volume_m3": 9000 } }
```
```json
PUT /api/v2/tenant/1/obj/ships/1/move
{ "to": { "kind": "loc_leaf", "location_id": "harbour-mvd/berth-4" } }
```
The ship's own hold-tree is `/loc`-native, but its root anchor
resolves through the ship's own live position rather than a fixed
`GeoAnchor`:
```json
POST /api/v2/tenant/1/loc/def
{ "location_id": "ships:1/hold-a", "parent_id": null, "name": "Hold A", "postable": false,
  "placement": { "offset_x": 0, "offset_y": 0, "offset_z": -8.0, "rotation": 0.0,
                 "anchor": { "self": "ships:1" } } }
```
A container inside the hold, a pallet inside that:
```json
PUT /api/v2/tenant/1/obj/containers/200/move
{ "to": { "kind": "loc_leaf", "location_id": "ships:1/hold-a" } }
```
```json
PUT /api/v2/tenant/1/obj/pallets/9004/move
{ "to": { "kind": "obj", "subject": "containers:200" } }
```
The ship moves to a new berth — everything inside resolves through the
new position without a single `move` call on the container or the
pallet:
```json
PUT /api/v2/tenant/1/obj/ships/1/move
{ "to": { "kind": "loc_leaf", "location_id": "harbour-mvd/berth-7" } }
```
```json
GET /api/v2/tenant/1/obj/pallets/9004/position

{
  "resolved": { "kind": "loc_leaf", "location_id": "ships:1/hold-a" },
  "chain": ["pallets:9004", "containers:200", "loc:ships:1/hold-a", "obj:ships:1", "loc:harbour-mvd/berth-7"],
  "as_of": "live"
}
```

---

## 10. One `dxp` transaction: complete a work order and admit a truck into a capacity-guarded dock, atomically

```json
POST /api/v2/tenant/1/dxp/def

{
  "def_id": "close-workorder-and-dock",
  "participants": [
    { "primitive": "fsm", "role": "workorder" },
    { "primitive": "loc", "role": "dock-entry" }
  ],
  "bindings_schema": { "workorder_id": "string", "truck_subject": "string", "dock_location_id": "string" }
}
```
```json
POST /api/v2/tenant/1/dxp/txn

{
  "def_id": "close-workorder-and-dock",
  "bindings": {
    "workorder_id": "wo-8821",
    "truck_subject": "trucks:9",
    "dock_location_id": "site-mvd/bldg-a/yard/dock-zone"
  }
}
```
```json
200
{ "status": "committed", "reason": null }
```
Both legs land together — the work order machine transitions to
`closed`, and `trucks:9` is admitted into the dock, refusing the whole
transaction (work order stays open, truck stays outside) if the dock
happens to be full at the moment of dispatch, not partially applying
either half.

---

## 11. `pattern_after` end to end

```json
POST /api/v1/tenant/1/pallets
{ "id": "9001" }
```
```json
POST /api/v2/tenant/1/obj/attach
{ "subject": "pallets:9001", "pattern_after": "pallets:88" }
```
```json
201
{ "subject": "pallets:9001", "capacity": { "max_weight_kg": 1000, "max_volume_m3": 1.5, "max_count": null } }
```
Identical to `pallets:88`'s capacity, read live at the moment of
`attach`, nothing persisted linking the two afterward — no `pattern_id`
on `pallets:9001` to inspect, because `pattern_after` never created a
named, reusable pattern, only copied one live object's fields once.

---

## 12. A fence's geometry changes while a subject is already inside it

```json
PATCH /api/v2/tenant/1/loc/fences/dock-zone
{
  "geometry": {
    "type": "Polygon",
    "coordinates": [[[-56.1651,-34.9014],[-56.1649,-34.9014],
                      [-56.1649,-34.9012],[-56.1651,-34.9012],
                      [-56.1651,-34.9014]]]
  }
}
```
`trucks:1` (still in the dock from §3) hasn't reported since the
change. The advisory reconciliation view surfaces the drift:
```json
GET /api/v2/tenant/1/loc/fences/dock-zone/reconcile

{
  "fence_id": "dock-zone",
  "recorded_count": 3,
  "observed_count": 2,
  "drift": [
    { "subject_ref": "trucks:1", "recorded": "member", "observed": "outside_new_boundary" }
  ]
}
```
Read-only — `loc_fence_capacity.count` still says 3, untouched by this
call. The fix is an ordinary `report`, not a write this endpoint
performs on the caller's behalf:
```json
POST /api/v2/tenant/1/loc/report
{ "subject": { "type": "REF", "entity": "trucks", "id": 1 },
  "point": { "lat": -34.9013, "lon": -56.1650 }, "reported_at": "2026-08-02T11:00:00Z" }
```
```json
200
{ "changed": true, "fences": [], "entered": [], "exited": ["dock-zone"] }
```
`trucks:1`'s exit is now recorded for real, through the same CAS path
every other capacity change uses — the reconcile call only ever told
someone where to look.

---

## 13. Moving a pallet onto something it already, transitively, contains

`pallets:88` contains `cases:4471` (from §5). Attempting the reverse:
```json
PUT /api/v2/tenant/1/obj/pallets/88/move
{ "to": { "kind": "obj", "subject": "cases:4471" } }
```
```json
409
{ "error": { "code": "XOLU-OBJ004", "message": "move would create a containment cycle", "status": 409 } }
```
Checked in the same transaction as the write, not as a separate
validation pass — the universal cycle-safety guard `obj-00-design.md`
§5 requires, refusing regardless of how many hops deep the cycle would
be (a pallet inside a container inside a lorry attempting to contain
that same lorry refuses identically, not just the direct one-hop case
shown here).

---

## 14. A barrel that fails inspection, then gets scrapped

```json
POST /api/v1/tenant/1/barrels
{ "id": "4471" }
```
```json
POST /api/v2/tenant/1/obj/attach
{ "subject": "barrels:4471", "capacity": { "max_volume_m3": 0.2 } }
```
```json
PUT /api/v2/tenant/1/obj/barrels/4471/move
{ "to": { "kind": "loc_leaf", "location_id": "customers:acme-hq" } }
```
Routine inspection flags it — an entity-level fact, nothing to do with
`/obj` at all:
```json
PATCH /api/v1/tenant/1/barrels/4471
{ "condition": "failed_inspection" }
```
It still needs to move — back to the depot, unaffected by the status
change just recorded:
```json
PUT /api/v2/tenant/1/obj/barrels/4471/move
{ "to": { "kind": "loc_leaf", "location_id": "site-mvd/bldg-a/yard/depot" } }
```
```json
200 { "moved": true }
```
On to the recycling facility:
```json
PUT /api/v2/tenant/1/obj/barrels/4471/move
{ "to": { "kind": "loc_leaf", "location_id": "recycling-mvd/intake" } }
```
Only once it's actually crushed does the object's own lifecycle end —
not at the inspection failure, not at either of the two moves after it:
```json
POST /api/v2/tenant/1/obj/barrels/4471/retire
```
```json
200 { "retired": true }
```
Three separate facts, three separate moments: entity status changed;
the object kept moving, twice, completely indifferent to that change;
the object's tracking ended only when the physical thing itself did.

---

## 15. "Loaded lorries must never enter the parking lot" — the wrong answer, then the right one

**The wrong answer, shown so it's visibly wrong rather than just
asserted to be:** a new guard, invented specifically for this rule —
`/obj`'s containment-closure state feeding directly into `/loc`'s
admission CAS, so a `move` targeting the parking lot would evaluate
whatever the lorry currently contains before admitting it:

```json
PUT /api/v2/tenant/1/obj/lorries/5/move
{ "to": { "kind": "loc_leaf", "location_id": "site-mvd/parking-lot" } }
```
```json
409 (hypothetical)
{ "error": { "code": "XOLU-LOC-POLICY-001",
             "message": "refused: obj containment closure non-empty, policy forbids entry", "status": 409 } }
```
This is not how it actually works, and the reason matters: it would
make `/loc`'s guard depend on `/obj`'s state, breaking guard locality
across two primitives' own separately-committed transactions, and it
would hardcode one deployment's policy content into the substrate
itself — exactly what §7c already declined to do for entry/exit
authorisation generally.

**The actual answer:** the `move` succeeds unconditionally as far as
`/loc`/`/obj` are concerned —
```json
PUT /api/v2/tenant/1/obj/lorries/5/move
{ "to": { "kind": "loc_leaf", "location_id": "site-mvd/parking-lot" } }
```
```json
200 { "moved": true }
```
— and it's the event this produces that an application watches and
acts on *before* ever issuing the call, not after:
```json
POST /api/v2/tenant/1/obj/lorries/5/contents

{ "contents": [ { "subject": "pallets:9002", "kind": "pallets" } ] }
```
A dispatch application checks exactly this — non-empty contents —
before deciding whether to issue the `move` at all, and refuses to
send it if the check fails. The guarantee this gives is real but
weaker than a substrate-enforced one, and that's the honest, stated
trade-off: `/obj-00-design.md` §13's own guarantee-strength gap, made
concrete instead of left abstract. Two concurrent dispatch requests
checking contents at the same instant could both see "empty" and both
issue a `move` that, combined, violates the rule — a substrate guard
can't be raced this way; an application-level check, by construction,
can.
