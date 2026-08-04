# The `obj` REST API — wire surface and error codes

> **Reconciliation status (added 2026-08-02, against the v0.22.4
> checkpoint).** This is the banner the header immediately below
> predicted by name — `/obj` shipped as wave 10 (T-119–T-124,
> `CHANGELOG.md` v0.22.1 through v0.22.4), and this document's own
> earlier bet ("expect this to earn a reconciliation banner the day
> implementation reveals a narrower or different shape") paid off
> again, the same way `loc-01-rest-api.md`'s own banner already did.
> What actually shipped narrower or different, checked directly
> against `pkg/server/v2_obj_handlers.go`,
> `pkg/server/v2_obj_promote_handlers.go`, and `pkg/obj/errors.go`:
>
> - **§4a, Patterns — not built at all.** Deferred deliberately as its
>   own item (T-134), confirmed directly with Horacio rather than
>   silently built or silently dropped, once its real scope (five
>   endpoints plus `attach`-time wiring plus two new `GET` response
>   fields) was clear against the time remaining in T-124. `XOLU-OBJ013`
>   (the "more than one of `capacity`/`pattern`/`pattern_after` set"
>   refusal) is unreachable until T-134 lands.
> - **§5, Promote/demote — two real wire-contract corrections**, both
>   confirmed directly before building, not assumed. `amount` is a
>   decimal *string*, not the bare number this document's own example
>   showed — @B04's own established discipline, already enforced
>   identically everywhere else `bal` appears in this codebase's `dxp`
>   surface, and the original example simply hadn't been checked
>   against it. `bal` has no single-sided decrement/increment, only
>   two-sided `Transfer(from, to, amount)` — this document's own
>   example showed only `bal_account` with no counterparty; shipped
>   with an explicit, caller-supplied `to_account` (promote) /
>   `from_account` (demote) field instead of a fixed system sink,
>   confirmed directly rather than invented.
> - **§5 — `position.kind` scoped to `"obj"` only, this release.**
>   Promote's own `position` field accepts only containment
>   (`obj-00-design.md` §9's own worked example: a case pulled off a
>   pallet's bulk count becomes a child of something) — `loc_leaf`/
>   `null` positioning via promote is not built. Not named as a
>   limitation in the original draft; narrowed deliberately to keep
>   T-121's own scope to the one case requiring genuine `dxp`-participant
>   integration on `/obj`'s own side.
> - **Error table — one likely-orphaned code**, found by the
>   completion sweep, not by inspection. `XOLU-OBJ020` ("Unknown
>   `(kind, key)` subject") appears only in the table below, never
>   described anywhere in this document's own prose — `patterns/
>   extract`'s own "source doesn't resolve" case is documented as
>   `XOLU-OBJ001` instead, the same code `attach` itself already uses
>   for an unresolved subject. Left in the table rather than removed
>   unilaterally; worth a decision, not assumed to need new
>   reachability work of its own.
> - **What held exactly as specified**, worth naming so this doesn't
>   read as entirely corrections: §0's three-termination-kind model,
>   the report/move split, `attach`/`detach` (§1), `contents` including
>   the `depth=all` transitive case (§3), `capacity` (§4), `retire`
>   including its non-empty-contents refusal (§6), and the event feed
>   (§7) all shipped matching this document almost verbatim.
>
> §5's own body below is marked up in place with the precise,
> code-verified corrections rather than rewritten, so the intended
> design stays legible alongside what actually shipped — the same
> discipline `loc-01-rest-api.md`'s own banner established.

`obj-01` of the obj document series: see `obj-00-design.md` for the
model and doctrine this surface implements, and
`obj-02-implementation.md` for the staged build. Status: proposal, not
implemented — expect this to earn a reconciliation banner the day
implementation reveals a narrower or different shape, exactly as
`cal-rest-api.md` did for `cal`, and as `loc-01-rest-api.md` itself
will if `loc-02`'s build surfaces a correction. Nothing here should be
read as more settled than that.

Prepared: 2026-07-31 (against v0.20.0, and against `obj-00-design.md`
as fixed in this same pass — no drift between the two yet, unlike
`loc-01`'s own history against an earlier `loc-00`).

---

## 0. Every position is one call, whichever of three things it resolves to

`obj-00-design.md` §6 draws three termination cases for a resolved
position, and this surface deliberately does **not** give them three
different verbs. `move` takes one `to`, which names *what kind* of
target it is:

```json
{ "to": { "kind": "loc_leaf", "location_id": "bay-14" } }
{ "to": { "kind": "obj", "subject": "pallets:88" } }
{ "to": null }
```

- **`loc_leaf`** — anchored at a `/loc` tree leaf. Identical in effect
  to `/loc`'s own `move`; a subject that is never contained by another
  `/obj` and never off-site behaves exactly like an ordinary `/loc`
  subject.
- **`obj`** — contained by another `/obj`-tracked subject. This *is*
  containment (`obj-00-design.md` §5) — there is no separate attach/
  detach-containment verb, because containment is not a different fact
  from position, it's this termination case specifically. The subject
  named must itself already be `/obj`-attached (§1) or the call refuses
  (`XOLU-OBJ005`).
- **`null`** — unassign. Position becomes explicitly unknown/off-site
  (`obj-00-design.md` §12), a first-class, ordinary state, not an
  error and not a workaround synthetic location. This is the operation
  `/loc`'s own surface never had a clean way to express, and its
  absence there was a named, real gap this design specifically closes.

Raw geo-reports use a separate verb, `report`, mirroring `/loc`'s own
`report`/`move` split (`loc-01-rest-api.md` §0) for the identical
reason: a report is a coordinate, not a `to` target, and resolves
fence membership only — it never sets `move`'s canonical position,
regardless of which of the three kinds above that position currently
is.

---

## 1. Attach — giving an existing entity `obj` capability

No `def`, no independently-caller-named id (`obj-00-design.md` §4).
The subject is always an entity that already exists; `attach` gives it
position and containment capability, addressed at its own `(kind,
key)` — the same convention `pkg/storage/meta_subject.go` already
uses for `cal.calendar`, `bal.account`, and the rest.

```
POST   /api/v2/obj/attach                          Attach obj capability to an existing subject
GET    /api/v2/obj/{kind}/{key}                     Fetch one subject's obj state
DELETE /api/v2/obj/{kind}/{key}                      Detach (see below — not the same as retire)
```

**`POST /api/v2/obj/attach`**

```json
{
  "subject": "vehicles:47",
  "capacity": { "max_weight_kg": 12000, "max_volume_m3": 40 }
}
```

`subject` follows `meta_subject.go`'s `kind/key` form. Exactly one of
`capacity`, `pattern`, or `pattern_after` may be set — all three
populate the same underlying capacity fields, by different means (§4a
covers the latter two); a subject with none of the three set can be
positioned and can itself be contained, but cannot hold anything (§4).
`XOLU-OBJ001` if `subject` does not resolve to a real entity;
`XOLU-OBJ006` if `subject` is already attached; `XOLU-OBJ013` if more
than one of `capacity`/`pattern`/`pattern_after` is set.

`DELETE` removes `obj` capability entirely — refused
(`XOLU-OBJ007`) while the subject currently contains anything or is
itself positioned anywhere other than unassigned. This is bookkeeping
cleanup, not the lifecycle operation: compare §6.

---

## 2. Position

```
PUT    /api/v2/obj/{kind}/{key}/move                Set canonical position (one of three kinds, §0)
POST   /api/v2/obj/{kind}/{key}/report               Raw geo-report (fence resolution only)
GET    /api/v2/obj/{kind}/{key}/position             Resolved position, walking the full chain
```

**`PUT .../move`** body is the `to` object from §0. Refusals:
`XOLU-OBJ002` (destination `loc_leaf` at capacity), `XOLU-OBJ003`
(destination `obj` subject at capacity, weight or volume — either
dimension alone can refuse, §7 of `obj-00-design.md`), `XOLU-OBJ004`
(the move would create a containment cycle — checked in the same
transaction as the write, `obj-00-design.md` §5), `XOLU-OBJ005`
(target `obj` subject not attached).

**`GET .../position`** resolves the chain to its actual termination —
a `loc_leaf` with its own fully-composed placement, a raw coordinate,
or `unassigned` — never a partial answer. Response shape:

```json
{
  "resolved": { "kind": "loc_leaf", "location_id": "bay-14", "lat": ..., "lon": ... },
  "chain": ["pallets:88", "vehicles:47", "loc:bay-14"],
  "as_of": "live"
}
```

`chain` is included specifically so a caller can see how many hops
were walked and through what — useful for debugging a surprising
resolution, and a deliberate, honest admission that `as_of` is always
`"live"`: this surface does not support point-in-time resolution
(`obj-00-design.md` §13's open non-versioned-placement question). A
future revision that resolves that question changes this field's
contract explicitly, not silently.

---

## 3. Contents

```
GET    /api/v2/obj/{kind}/{key}/contents             Direct contents only
GET    /api/v2/obj/{kind}/{key}/contents?depth=all    Full transitive closure
```

Direct contents by default — deliberately not transitive, since a
lorry carrying forty individually-tracked cases returning all forty by
default would make the common call (what's directly on this pallet)
expensive by accident. `depth=all` walks the full closure via the same
mirrored-graph traversal `obj-00-design.md` §10 describes
(`FindPath`/neighbor-walk against the live graph, read-only, never a
guard input).

---

## 4. Capacity

Set at `attach` time (§1) or updated after:

```
PATCH  /api/v2/obj/{kind}/{key}/capacity
```

```json
{ "max_weight_kg": 12000, "max_volume_m3": 40, "max_count": null }
```

Any dimension may be `null` (unconstrained on that axis). At least one
dimension must be set, or the subject cannot hold anything — matches
`/loc`'s own "capacity set on a non-postable node" refusal shape
(`XOLU-LOC011`'s `/obj` counterpart, `XOLU-OBJ008`).

---

## 4a. Patterns

`obj-00-design.md` §7a's mechanism, in wire form. A pattern is not an
`/obj` subject — no `(kind, key)`, no position, addressed by plain
`(tenant, id)` like an `fsm` definition:

```
POST   /api/v2/obj/patterns/def         Draft a pattern from scratch
POST   /api/v2/obj/patterns/extract     Draft a pattern from an existing subject's current fields
GET    /api/v2/obj/patterns/list        List patterns
GET    /api/v2/obj/patterns/{id}        Fetch one
DELETE /api/v2/obj/patterns/{id}        Delete
```

**`POST /api/v2/obj/patterns/def`**:
```json
{ "name": "europallet-std", "max_weight_kg": 1000, "max_volume_m3": 1.5 }
```

**`POST /api/v2/obj/patterns/extract`** — reads `source`'s current
capacity fields once, at the moment of the call; `source` itself is
untouched, and the new pattern carries no lineage back to it:
```json
{ "source": "pallets:88", "name": "europallet-loaded-std" }
```
`XOLU-OBJ001` if `source` does not resolve to an attached subject.

**Applying a pattern** happens at `attach` (§1), never here — `pattern`
for the durable form:
```json
{ "subject": "pallets:9001", "pattern": "europallet-std" }
```
— or `pattern_after` for the one-shot form, reading `source`'s current
fields and applying them directly with nothing persisted in between:
```json
{ "subject": "pallets:9002", "pattern_after": "pallets:88" }
```
A `pattern`-attached subject's `GET` (§1) surfaces `pattern`,
`pattern_id`, and a computed `pattern_deleted`. A `pattern_after`-
attached subject surfaces none of these — nothing was persisted, so
there is nothing to point back to; its capacity fields are
indistinguishable from having been typed in directly. This is the
correct, direct consequence of not persisting anything, not a gap.

`DELETE /api/v2/obj/patterns/{id}` removes the pattern definition
outright — no cascade refusal. Already-cloned subjects keep their own
snapshotted fields regardless; only `pattern_deleted` on their next
`GET` reflects the change.

---

## 5. Promote / demote

```
POST   /api/v2/obj/promote
POST   /api/v2/obj/demote
```

**`POST .../promote`** — the atomic transition `obj-00-design.md` §9
describes: decrement a `bal` account's count by one unit, create-or-
reuse the corresponding entity, and attach `obj` position to it, all
in one commit (dispatched via `dxp`, mirroring the pattern already
proven for multi-leg `bal` transfers).

```json
{
  "bal_account": "pallet-88-cases",
  "to_account": "pallet-88-cases-promoted",
  "amount": "1",
  "entity": { "kind": "cases", "existing_key": null,
              "create": { "lot_code": "L4471", "condition": "damaged" } },
  "position": { "kind": "obj", "subject": "pallets:88" }
}
```

> **As shipped:** `to_account` is required — `bal` has no single-sided
> decrement, only two-sided `Transfer(from, to, amount)`, so promote's
> own destination for the decremented unit is caller-supplied, not a
> fixed system sink. `amount` is a decimal *string* (@B04), not the
> bare number shown in this document's own earlier draft.
> `position.kind` accepts only `"obj"` in this release — `loc_leaf`/
> `null` positioning via promote is not built (T-121's own scope,
> narrowed deliberately). Demote's own request shape mirrors this
> exactly, with `from_account` in place of `to_account`.

`entity.existing_key` set means reuse an already-registered entity
(the case had prior identity, e.g. from a supplier record) instead of
creating one. `XOLU-OBJ009` if the `bal_account`'s balance is
insufficient for `amount`; `XOLU-OBJ010` if both `existing_key` and
`create` are set or both are empty.

**`POST .../demote`** — the reverse: retire (§6) or detach (§1) the
`obj` subject and increment the target `bal` account by the
equivalent amount, atomically. Refuses (`XOLU-OBJ011`) if the subject
currently contains anything — dissolve contents first.

```json
{
  "subject": "cases:4471",
  "bal_account": "pallet-88-cases",
  "from_account": "pallet-88-cases-promoted",
  "amount": "1"
}
```

---

## 6. Retire

```
POST   /api/v2/obj/{kind}/{key}/retire
```

The terminal lifecycle state `obj-00-design.md` §12 names — distinct
from detach (§1, bookkeeping cleanup for a subject that was never
correctly positioned) and distinct from demote (§5, dissolves into a
`bal` count). Retire means the physical thing itself has ceased to
exist. Irreversible — no `un-retire`; a genuinely new physical
instance is a new `attach`, not a state change on the old one.
Refuses (`XOLU-OBJ012`) if the subject currently contains anything.

---

## 7. Events

Mirrors `/loc`'s own event feed (`loc-00-design.md` §9a) exactly in
shape: every guard-bearing write — `move`, `report`-driven fence
transition, `promote`/`demote`, `retire` — emits an event a consuming
application can act on. This is the mechanism `obj-00-design.md` §2's
policy non-goal points to explicitly: "surgical instruments never
leave the ward" is enforced by an application watching this feed and
refusing to issue the call, not by a guard inside `/obj` itself.

---

## What's deliberately not here

- **No separate attach/detach-containment verb** — §0's whole point.
  Containment is `move`'s `obj`-kind target, not a different
  operation.
- **No point-in-time position resolution** — §2's `as_of: "live"` is
  the whole story today; `obj-00-design.md` §13 names this as the
  strongest open question in the entire design, not resolved by
  omission here.
- **No bulk/batch move** — moving a pallet does not cascade-move its
  contents' own `move` calls automatically. A caller re-resolving
  §2's `GET .../position` for each contained subject after moving the
  container gets the right answer for free (resolution is always
  live), but nothing here issues forty individual `move` calls on a
  caller's behalf.
- **No entity authorization or ownership check** — `attach` succeeds
  for any entity the caller can otherwise reach; who's allowed to
  attach `obj` capability to what is `tenant-access-control.md`'s
  concern, unchanged by this surface.

---

## Error codes — `XOLU-OBJ` reserved, first pass

Following the same convention `loc-01-rest-api.md` already uses, and
the same honest caveat: this table exists so `obj` doesn't start from
zero, not because every code below survives contact with
`obj-02-implementation.md`'s own build.

| Code | Meaning |
|---|---|
| `XOLU-OBJ001` | `attach`: `subject` does not resolve to a real entity |
| `XOLU-OBJ002` | `move` refused: destination `loc_leaf` is at capacity |
| `XOLU-OBJ003` | `move` refused: destination `obj` subject is at capacity (weight or volume) |
| `XOLU-OBJ004` | `move` refused: would create a containment cycle |
| `XOLU-OBJ005` | `move` target `obj` subject is not attached |
| `XOLU-OBJ006` | `attach` refused: subject already attached |
| `XOLU-OBJ007` | detach refused: subject currently contains something, or is positioned somewhere other than unassigned |
| `XOLU-OBJ008` | `capacity` set with every dimension null |
| `XOLU-OBJ009` | `promote`: insufficient `bal` balance for requested amount |
| `XOLU-OBJ010` | `promote`: exactly one of `entity.existing_key`/`entity.create` must be set |
| `XOLU-OBJ011` | `demote` refused: subject currently contains something |
| `XOLU-OBJ012` | `retire` refused: subject currently contains something |
| `XOLU-OBJ013` | `attach` refused: more than one of `capacity`/`pattern`/`pattern_after` set |
| `XOLU-OBJ020` | Unknown `(kind, key)` subject |

Deliberately not yet coded: any code for the non-versioned-placement
question (§2) — there is no refusal to have yet, since the surface
doesn't attempt point-in-time resolution at all rather than attempting
it incorrectly.
