# The `cal` REST API — complete surface

> **Reconciliation status (added 2026-07-18, v0.14.13):** This document was
> written on 2026-06-22 before the `cal` implementation began. Sections
> describing endpoints, wire formats, and error codes are historical: the
> `cal` HTTP surface actually shipped as a deliberately-narrower minimum
> (four endpoints: `check`, `openings`, `propose`, `confirm`) in v0.14.7
> under T-18, with error taxonomy hardened through v0.14.13. See
> `CHANGELOG.md` entries v0.14.7 through v0.14.13 for what actually
> exists, and `pkg/server/v2_cal_handlers.go` for the authoritative
> handler signatures.
>
> Notable divergences from this proposal:
>
> - The exclusive-only model shipped, not the full `Mode`/`Capacity`
>   vocabulary described here. `ModeShared` and `ModeSubPrefix` were
>   removed in v0.14.12; `Calendar.Capacity` was removed in v0.14.13.
>   Cal's target is Google-Calendar-shaped exclusive-only occupancy.
> - `honour`, `reschedule`, `dryrun`, multi-calendar `match`, and
>   `batch` (the "required but not yet pathed" list in the original
>   preamble) did not ship. They are deliberately out of scope for
>   v0.14.x; whether they return in v0.15.x is undecided.
> - The `sub:<child_id>` mode described here does not exist in the
>   implementation.
> - Error codes: XOLU-CAL001 through XOLU-CAL007 exist as documented
>   in `pkg/errors/errors.go`. This proposal's separate error-code
>   scheme is superseded.
>
> This document is retained for design-history value. Anyone
> implementing or consuming cal today should read `CHANGELOG.md`,
> `pkg/cal/*.go`, and `pkg/server/v2_cal_handlers.go` as the sources
> of truth. This proposal's content below is preserved verbatim from
> 2026-06-22 for archival reference.

---

Status: Proposed endpoint specification (part of the `cal` design, with `cal-pebble-codec.md`)
Prepared: 2026-06-22
Author: haitch (h@ual.li)
Licence: Apache 2.0
Revision: 2026-06-22 design session (cal scheduling primitive)

> This pins every operation the model requires to a concrete path, request, and
> response — including the **five** the design left "required but not yet pathed"
> (`honour`, `reschedule`, `dryrun`, multi-calendar match, and the transactional
> `batch` of §9). It also renames the verbs that were legible only after reading
> the design doc. The test applied throughout: **a junior dev should guess what an
> endpoint does from its name and its example body, without reading Part F.**
>
> Path conventions mirror the existing v2 surface (`fsm`, `meta`) exactly:
> `POST .../def` creates, `GET .../def` lists, `.../validate` dry-checks, `{id}`
> path params. Nothing here invents a new routing idiom.

---

## Review issues (2026-06-22 design review)

Three issues raised against the REST + codec specs. Two are load-bearing for the
codec and should be settled before the bit layer is written; one is a doc-internal
count fix. Each is elaborated at its site below.

| # | Issue | Class | Where | Status |
|---|---|---|---|---|
| 1 | `match` does not state whether the AND runs over the **binding plane only** or **binding-OR-proposed**. This is a *meaning* question, not a tuning knob: it changes which calendars collide and whether the proposed plane is on the hot path. | Semantics — blocks codec | §8, and `cal-pebble-codec.md` §6.5 | **Open — decide before coding** |
| 2 | A `default_state: binding` calendar cannot accept a bearer-less create (a binding booking requires a live bearer), but no section states this. A junior following the "guess it from the example body" promise hits an unexplained `409`. | Legibility / correctness-adjacent | §2 (`bearer`, `default_state`) | **Open — needs one explicit sentence + example** |
| 3 | The preamble says the spec paths "the four" previously-unpathed operations (`honour`, `reschedule`, `dryrun`, multi-calendar match), but §9 `batch` is **also** labelled "was unpathed" — so the spec actually paths **five**. The "20 endpoints" total is correct and unaffected. | Doc-internal count | preamble vs §9 | **Fix: four → five (or fold `batch` into the enumeration)** |

> Review note on issue 3: an earlier pass of this review reported a "20 vs 21
> endpoint" discrepancy. That was a miscount on the reviewer's side — the index
> holds exactly 20, matching the prose. The genuine count mismatch is the
> four-vs-five "previously-unpathed" claim above, not the endpoint total.

---

## Aliasing decisions (the renames, with rationale — veto individually)

The design's internal names are precise but several are legible only to someone
who has read the ontology. The REST surface is the wrong place for ontology. Each
rename below keeps the design's concept; only the user-facing word changes.

| Design term | REST verb/noun | Why the rename |
|---|---|---|
| `findgap` / `gap` | `openings` | "gap" needs Part F to know it returns *free spans*. "openings" says it: places something could fit. |
| `findalloc` / `alloc` | `bookings` | "alloc" reads as memory allocation, and needs the doc to know it returns *commitment sets*, not spans. A junior asking "what's scheduled?" looks for `bookings`. |
| `book` (verb on a new id) | `POST .../bookings/def` | A booking is a `def`-constructed citizen of its calendar, exactly as a rollup is of a timeline (`POST /ts/tl/{id}/rollup/def`). Caller-supplied id in the body, idempotent. Keep `book` as the *concept*; the path is the `def` constructor. |
| `maybe` (PATCH) | `propose` | "maybe" is a mood, not an operation. `propose` is the A9 state ("tentative — proposed") and reads as an action. |
| `confirm` (PATCH) | `confirm` | Kept. Already legible: tentative → binding. |
| `reject` (PATCH) | `decline` | "reject" collides with HTTP-validation rejection. `decline` is unambiguously "this party says no to the proposal". |
| `honour` (PATCH) | `complete` | `honour` is the design's word for binding→done; juniors won't reach for it. `complete` is what they'll type. (British spelling stays in prose; the wire verb is `complete` — code is American English per house style.) |
| `dryrun` | `POST .../bookings/check` | A dry-run *is* a feasibility check that writes nothing. `check` (collection-level, POST, body = proposed booking) reads as "would this work?". Distinct from the occupancy `check/busy` reads, which become `availability/...` below — see next row. |
| `check/busy\|free\|capacity/<period>` | `availability/<period>?q=busy\|free\|capacity` | Frees the word `check` for the feasibility test (the thing juniors most need), and "availability" is what a human actually asks of a calendar. `capacity` stays a verb here but returns the ternary (`free`/`idk`/`busy`) plus `100 − confirmed%` and the raw counts. |
| `reschedule` (PATCH) | `POST .../bookings/{id}/move` | `move` says atomic relocation. Crucially it is a **POST that can fail with a conflict report**, not a PATCH that 4xxs — the path shape signals "this runs placement". |
| multi-calendar match (unpathed) | `POST /api/v2/cal/match` | Top-level, not under one `{calendar_id}`, because it spans calendars. Body lists the calendars; response is coincident free spans. |
| `calendar_no` | `calendar_id` | Consistency with every other xolu entity id. |

Everything below uses the renamed surface.

---

## 1. Calendars (first-class entities — F6 lean)

Single-population primitive: a calendar *is* the thing — there is no separate
"calendar definition" entity minted into "calendar instances" the way `fsm` has
def→machine. So `cal` follows the `rollup` shape (`def` is the **construction
verb**, not an addressable noun): `def` constructs, `list` enumerates, and the
constructed calendar is addressed directly by `{calendar_id}`. This is *not* the
`fsm/def/{id}` shape — that two-population pattern is for primitives whose
definitions persist independently of their instances, which a calendar's does not.

```
POST   /api/v2/cal/def                 Define a calendar (construct; caller-named id)
GET    /api/v2/cal/list                List calendars
GET    /api/v2/cal/{calendar_id}       Fetch one
PATCH  /api/v2/cal/{calendar_id}       Update metadata (not its bookings)
DELETE /api/v2/cal/{calendar_id}       Remove (see cascade note)
POST   /api/v2/cal/def/validate        Validate a definition without storing
```

`def` carries the **declaration** semantics (caller chooses the id, idempotent
define-at-known-id) — it is "define", not "new". The constructed calendar drops
`def` and is addressed at `cal/{calendar_id}`, exactly as a rollup is constructed
at `rollup/def` and then addressed at `rollup/{rollup_id}`.

> **Reserved calendar ids.** Because a calendar is addressed directly at
> `cal/{calendar_id}` (no `def` segment in front of it), the id must not collide
> with the primitive-level path segments that are siblings of it: `def`, `list`,
> and `match`. These are reserved — a calendar cannot be named `def`, `list`, or
> `match`. (`gen` has the same situation with its type names; the reserved-word
> list is the accepted xolu way to live with it.)

A calendar names a bookable thing (a resource) or a grouping. It carries the
per-resource quantum (F3) and the resource's capacity (A11). The junior never
sets the quantum by hand for the common case — `def` defaults it by `kind`.

**`POST /api/v2/cal/def`**
```json
{
  "calendar_id": "pool-main",
  "name": "Main swimming pool",
  "kind": "resource",
  "entity_ref": "01HQ…",
  "capacity": 30,
  "resolution": "15m",
  "horizon": "18mo",
  "default_state": "binding",
  "optionality": "whole-booking"
}
```
A calendar is the availability of **exactly one entity**, named by `entity_ref` (a
handle into the entity graph). One calendar, one entity — a room, a person, a
*team*, a pool. There is no special group/aggregate calendar: a team is simply an
entity that has its own calendar, like any other. If a calendar's availability must
reflect other entities (a team reflecting its members), that composition lives in
entity space — the target entity carries REF fields to the others, and some process
above `cal` keeps the calendar in sync. The calendar itself always points at one
entity and never holds a list.

`entity_ref` uses two reserved sentinels: **`EntityNil` (`0`)** — a standalone
availability track with no entity behind it — and **`EntityTombstone`
(`0xFFFFFFFFFFFFFFFF`)** — the bound entity was deleted (a dangling ref, distinct
from never-bound). Valid handles lie strictly between (`0 < h <= EntityMaxValid`);
the codec reserves the top of the `u64` range as a sentinel pool, so further
sentinels descend from the top without ever colliding with a real handle (see
`cal-pebble-codec.md` §3.2a).

`kind` is a **descriptive label** for what the entity is — `resource` (a thing
booked) | `actor` (a person or team) — used for sane per-kind defaults
(`capacity`, `resolution`, `horizon`) and for display. It is **not** a structural
distinction: every calendar, whatever its kind, is the single-entity, single-bitmap
shape above. (Earlier drafts treated `group` as a calendar with no timeline of its
own; that is gone — a group/team is an `actor`-or-`resource` entity with an ordinary
calendar.)
`capacity` defaults `1` (a plain exclusive resource). `resolution` and `horizon`
have sane per-kind defaults; all are optional except `calendar_id`.

Two policy fields fix per-calendar behaviour the substrate would otherwise have to
hardcode (both have per-`kind` defaults; a junior never sets them for the common
case):
- **`default_state`**: `proposed` | `binding` — the state a newly created booking
  lands in. `proposed` forces an explicit confirm step (the propose→confirm dance,
  good where bookings need agreement); `binding` confirms on create (good for
  low-contention maintenance where you are simply booking the slot). The per-call
  `?confirm=true` / `?propose=true` flags on create still override per booking.
- **`optionality`**: `whole-booking` | `per-participant` — whether optionality is a
  property of the whole booking, or each participant on a booking is independently
  required/optional. `per-participant` is strictly more general; `whole-booking`
  is the simpler stopping point where a calendar never needs mixed participants.

Response `201`:
```json
{ "calendar_id": "pool-main", "kind": "resource", "entity_ref": "01HQ…",
  "capacity": 30, "resolution": "15m", "horizon": "18mo",
  "default_state": "binding", "optionality": "whole-booking",
  "created_at": "2026-06-22T14:01:00Z" }
```

**`DELETE`** refuses by default if the calendar holds non-cancelled bookings,
returning `409` with a count — deleting a calendar out from under live bookings is
the kind of irreversible mistake the substrate should make a junior ask for twice.
`?force=true` overrides.

---

## 2. Bookings — create, read, cancel

A booking is a `def`-constructed citizen of its calendar, exactly as a rollup is a
`def`-constructed citizen of a timeline (`POST /ts/tl/{id}/rollup/def`). Its id is
**client-assigned in the body at create time**, so create is idempotent
(re-defining the same id is a no-op, not a duplicate) — the same declare-at-known-id
semantics `def` carries everywhere. List is `bookings/list`; the booking is then
addressed directly at `bookings/{booking_id}`.

```
POST   /api/v2/cal/{calendar_id}/bookings/def            Define (create) a booking
GET    /api/v2/cal/{calendar_id}/bookings/list           List/filter bookings
GET    /api/v2/cal/{calendar_id}/bookings/{booking_id}   Fetch one booking
DELETE /api/v2/cal/{calendar_id}/bookings/{booking_id}   Cancel
```

**`POST .../bookings/def`** — create. The body carries the booking id and is
deliberately small:
```json
{
  "booking_id": "pump-q3",
  "when":  { "start": "2026-07-07T08:00:00Z", "end": "2026-07-07T12:00:00Z" },
  "mode":  "exclusive",
  "bearer": "team-maintenance",
  "purpose": "Quarterly pump service",
  "buffer": { "after": "30m" }
}
```
- `booking_id`: caller-supplied (declare-at-known-id; idempotent).
- `when`: a concrete span — `{start, end}`, each an **absolute instant**. Both
  endpoints MUST be RFC 3339 with an explicit zone designator: a trailing `Z`, or
  a `±hh:mm` offset (e.g. `2026-07-07T08:00:00Z` or `2026-07-08T09:00:00-03:00`).
  A zone-naive string (`2026-07-08T09:00:00`, no `Z`, no offset) is **rejected at
  the boundary** with a `400`, not silently assigned a server zone — the
  PostgreSQL `timestamp`-without-zone footgun. Parsing goes through
  `xolutime.Parse` (`pkg/xolutime`, the system-wide time invariant); the stored
  instant is normalised to UTC. The wall-clock *intention* behind a span
  ("9 a.m. Montevideo, recurring") is the caller's to own and resolve to absolute
  instants before posting — `cal` stores the instant, never the intention (see
  requirement R-T1 below, and `docs/TIME_HANDLING.md`). (For "find me a slot" instead
  of a fixed span, use `openings` first, §3, then create with the chosen span.)
- `mode` (the exclusivity vocabulary, A11/Part D — a **fixed enum**, never an
  expression): `exclusive` (whole resource) | `shared` (consumes one unit of
  capacity) | `sub:<child_id>` (locks a child, e.g. a lane) . Defaults to
  `exclusive` for `capacity:1` calendars, `shared` otherwise.
- `bearer`: the liable party (A10) — an entity ref. Required for a binding
  booking; a proposal may omit it.
- `buffer`: optional aftermath hold (Part D) — the lock is held past `end`.
- `participants`: optional; required/optional participants (see §2a). Only present
  when the calendar's `optionality` is `per-participant`.
- `detail`: optional freeform object (goes to the meta/detail document).

> **R-T1 (design requirement) — the owning layer retains intention and recovers from zone-rule changes.**
> `cal` stores `when` as an absolute UTC instant and, by deliberate design, does
> **not** retain the originating wall-clock intention. A consequence follows that
> is a *requirement on every caller*, not an optional nicety:
>
> A change to a zone's DST or offset rules between booking-time and the booked
> moment leaves the stored instant denoting a different wall-clock time than the
> human intended — and `cal` **cannot detect this**, because it never held the
> intention. Therefore the layer that owns the user's zone MUST:
>
> 1. retain `local_time + zone_id` as *its own* source of truth (never inferring
>    it back from the stored instant — that information is gone);
> 2. recompute the absolute instant when the relevant zone's rules change; and
> 3. re-issue the correction to `cal` via `move` (§6).
>
> `cal`'s side of the contract is correspondingly bounded: it provides the instant
> and the instant arithmetic for the recomputation (`pkg/xolutime`), and `move`
> provides the atomic correction. It does **not** provide the retained intention,
> the change detection, or the trigger — those are above the primitive by design.
> This is the temporal analogue of the recurrence and ordering exclusions (F-C):
> a correct boundary, stated so an upstream author meets it deliberately rather
> than discovering it after a government moves a clock. Storage holds instants;
> calendars hold intentions; the two must not be conflated in one layer.

A create lands the booking in the calendar's **`default_state`** (`proposed` or
`binding`, set on the calendar def). The per-call flags `?confirm=true` (force
`binding`) and `?propose=true` (force `proposed`) override it for one booking, so a
caller is never forced through — or denied — the two-step dance against the
calendar's default.

> **⚠ Review issue 2 — the bearer/`default_state` interaction (unstated, surprises the junior).**
>
> A `binding` booking requires a live `bearer` (the rule above, and the codec's
> `confirm`-needs-a-live-handle check, `cal-pebble-codec.md` §3.2a). A `proposed`
> booking may omit it. These two facts are each stated separately; their
> *interaction* is not, and the interaction is where a junior gets hurt:
>
> - On a calendar with **`default_state: binding`**, a create **lands directly in
>   `binding`** with no intervening `proposed` step. So a create that omits
>   `bearer` must be **rejected at create time** — there is no proposed state for
>   it to sit in while a bearer is assigned later.
> - The example create body in §2 *does* include `bearer`, so the happy path looks
>   fine. But the doc also says `bearer` is "required for a binding booking; a
>   proposal may omit it" — and a junior reading that on a `binding`-default
>   calendar will reasonably believe omission is fine *until they confirm*. It is
>   not: on this calendar there is no propose step, so omission fails immediately.
> - The failure they get is a bare `400`/`409` that the "guess it from the example
>   body" promise (preamble) did not prepare them for: the example didn't omit
>   `bearer`, and the prose framed bearer-omission as a property of *proposals*,
>   not of *the calendar's default state*.
>
> **Required fix (one sentence + one example):** state explicitly that on a
> `default_state: binding` calendar, a bearer-less create is rejected at create
> time (not deferred), and that `?propose=true` is the escape hatch — it forces the
> booking into `proposed`, where a bearer may legitimately be absent until
> `confirm`. Add an infeasible-create example showing the exact rejection body, so
> the failure is as guessable as the success. The underlying behaviour is
> *correct*; only its legibility is missing.

### 2a. Optional participants

Many calendars need participants who *may* take part but are not obliged to —
attendees of a meeting who are optional, or a standby technician who is welcome
but not required for a repair. The rule is two orthogonal axes — do not conflate
them:
- **Confirmation drives capacity.** A *confirmed* (binding) participant consumes a
  capacity slot, **whether they were required or optional**. Confirmation is the
  trigger; the required/optional flag does not enter the capacity math at all.
- **Required-ness drives missed-reconciliation.** A *required* participant who
  never honours is a recorded non-occurrence (audit-grade missed, A9). An
  *optional* participant who never honours is a non-event — no missed record,
  nothing owed. "Optionals are not counted as missed" is exactly this, and *only*
  this.

So an optional who confirms consumes capacity like anyone else; an optional who
never confirms costs nothing and is never missed. The flag lives per-booking (or
per-participant under `per-participant`) and is consulted only at reconciliation,
never by the `capacity` read (§3b).

**Conflict response (`409`)** — the same rich shape every write-that-can-clash
returns (see §6, the universal conflict report). Never a bare boolean, never an
opaque constraint-violation string.

**`GET .../bookings/list`** — the read interface to the obligation record (was
`findalloc`). Fixed declarative filters (A4), no query language:
```
?state=binding              proposed|binding|honoured|missed|cancelled (repeatable)
?from=2026-07-01&to=2026-08-01   window; MAY straddle now (returns past + future)
?bearer=team-maintenance
?mode=exclusive
?significance=high
```
Returns commitment **sets**, with reconciliation verdicts inline when the window
straddles `now`:
```json
{ "bookings": [
  { "booking_id": "pump-q3", "state": "honoured",
    "when": {"start": "...", "end": "..."}, "verdict": "on-time",
    "bearer": "team-maintenance" },
  { "booking_id": "pump-q4", "state": "binding",
    "when": {"start": "...", "end": "..."}, "verdict": "upcoming" }
], "count": 2 }
```

---

## 3. Reading availability — two questions, kept distinct

The design's hardest legibility trap: "is it free?" and "does my booking fit?" are
**different questions**, and conflating them is the bug the doc warns about
(`check` answers occupancy, never placement). The surface keeps them apart by
name.

### 3a. `openings` — where could something fit? (was `findgap`)
```
GET /api/v2/cal/{calendar_id}/openings?duration=4h&from=now&to=2026-08-01
    [&mask=evenings] [&mode=exclusive] [&objective=earliest]
```
Returns **spans** wide enough for `duration`, honouring `mode`/`buffer`. This is
the placement question. `objective` is a **fixed enum** (`earliest`, `emptiest`,
`first-fit`, `longest-clear-margin`) — never a scoring function (A7 anti-solver).
```json
{ "openings": [
    { "start": "2026-07-07T08:00:00Z", "end": "2026-07-07T17:00:00Z", "margin": "5h" },
    { "start": "2026-07-09T08:00:00Z", "end": "2026-07-09T13:00:00Z", "margin": "1h" }
], "count": 2, "horizon_reached": true }
```

### 3b. `availability` — how occupied is this period? (was `check/busy|free|capacity`)
```
GET /api/v2/cal/{calendar_id}/availability/{period}?q=free
GET /api/v2/cal/{calendar_id}/availability/{period}?q=busy
GET /api/v2/cal/{calendar_id}/availability/{period}?q=capacity
```
`{period}` is human/ISO: `2027/month/05`, `2026/week/35`, `2026/day/07-07`.

Three reads over one scan, layered from coarse to quantitative. The split that
matters: **confirmation (binding) is what counts; proposals do not.**

- `q=free` → boolean: are there **zero** commitments of any kind in the period?
- `q=busy` → boolean: is there **one or more** commitment? (the exact complement
  of `free` — there is no both-true case)
- `q=capacity` → the **ternary**, plus numbers. The categorical value is
  `free` | `idk` | `busy`:
  - `free` — no commitments at all.
  - `busy` — the period is taken by **binding** (confirmed) commitments.
  - `idk` — there are commitments, but they are **proposed**, not binding:
    occupied-on-paper, uncertain-in-fact. This is the value `free`/`busy` cannot
    express, and it is the whole reason `capacity` is ternary: a period stacked
    with maybes is neither cleanly free nor genuinely taken.

`q=capacity` also returns the scalar **`capacity = 100 − confirmed%`** (the
percentage of the period *not* locked by binding commitments; ignores proposals),
**and** the raw counts that produced it, so a caller can compute any other measure
the substrate did not pre-name:

```json
{
  "period": "2027/month/05",
  "state": "idk",
  "capacity": 70,
  "counts": { "binding": 3, "proposed": 5, "free": 0 }
}
```
- `capacity: 70` ← 30% of the period is held by binding commitments; proposals do
  not reduce it.
- `counts` exposes binding / proposed / free tallies. The caller derives their own
  figures from these — e.g. worst case if every proposal firms up is
  `100 − (binding+proposed)%`; contention is `proposed ÷ binding`. The substrate
  ships the facts; projections live above it.
- Optional participation does **not** appear here: confirmation alone drives the
  binding count (§2a), so an optional that confirmed is already in `binding`, and
  one that did not is simply absent. `capacity` never branches on required/optional.

> **Guard rail in the docs, not just the schema:** `availability` answers
> *occupancy*, never *placement*. A period can read `capacity: 70` and still have
> no contiguous hole wide enough for your booking. Fit questions go to `openings`
> (§3a) or `check` (§5), never to `availability`.

---

## 4. Lifecycle — the A9 transitions

State machine: `proposed → confirm → binding → complete → honoured`, with
`decline` and `cancel` as exits, and `missed` written by the system (§7).

```
PATCH /api/v2/cal/{calendar_id}/bookings/{booking_id}/confirm    proposed → binding
PATCH /api/v2/cal/{calendar_id}/bookings/{booking_id}/decline    proposed → not-committed (exit)
PATCH /api/v2/cal/{calendar_id}/bookings/{booking_id}/propose    (re)enter proposed
PATCH /api/v2/cal/{calendar_id}/bookings/{booking_id}/complete   binding  → honoured (deposits occurrence)
```
Each is a no-body PATCH (optional `{ "note": "..." }`). Each returns the booking's
new state and, for `complete`, the deposited occurrence ref. `complete` is the one
the design left unpathed and is **load-bearing for compliance** — it is the only
way a human asserts the thing was done, which is what makes a non-occurrence
(§7) meaningful by contrast.

Illegal transitions (e.g. `complete` on a `proposed` booking) return `409` with
the current state and the allowed transitions from it — a junior gets told what
they *can* do, not just that they were wrong.

---

## 5. `check` — would this booking work? (the dry-run, was `dryrun`)

The single most important endpoint for junior usability: propose a hypothesis,
get back feasible-or-why-not, **write nothing**. Same engine as create, so check
and create cannot drift.

```
POST /api/v2/cal/{calendar_id}/bookings/check
```
Body is exactly a create body (§2) — so "check it" and "book it" differ only in
the path, never in what you assemble:
```json
{ "when": { "start": "2026-07-07T08:00:00Z", "end": "2026-07-07T12:00:00Z" },
  "mode": "exclusive", "bearer": "team-maintenance" }
```
Feasible response (`200`):
```json
{ "feasible": true,
  "would_book": { "when": {"start":"...","end":"..."}, "mode": "exclusive" } }
```
Infeasible response (`200`, **not** an error — a clash is a valid answer):
```json
{ "feasible": false,
  "conflicts": [ ...universal conflict report, §6... ],
  "nearest_openings": [
    { "start": "2026-07-07T13:00:00Z", "end": "2026-07-07T17:00:00Z" }
  ] }
}
```
The `nearest_openings` field is what turns "no" into "here's where yes lives" —
the propose→see→adjust→resubmit loop (A3) made concrete for someone who doesn't
yet know the calendar by heart.

---

## 6. `move` — atomic reschedule (was `reschedule`)

The design is emphatic this is **not** delete-then-create: it is an atomic move
that re-runs placement at the destination and **can fail**, leaving the original
exactly where it was. The path shape (POST to a `/move` sub-resource, not a PATCH
of `when`) signals that this *does work and can refuse*.

```
POST /api/v2/cal/{calendar_id}/bookings/{booking_id}/move
```
```json
{ "to": { "start": "2026-07-14T08:00:00Z", "end": "2026-07-14T12:00:00Z" } }
```
Success (`200`): the booking at its new span, prior span recorded in history (F10).
Failure (`409`): the **universal conflict report**, and the booking is untouched —
the response states `"moved": false` explicitly so there is no ambiguity about
whether the original still stands.

### The universal conflict report (one shape, everywhere)

`POST bookings`, `bookings/check`, and `bookings/{id}/move` all return the *same*
conflict structure. Learn it once, read it everywhere:
```json
{
  "conflicts": [
    { "with": "swim-lesson-tue",
      "held_by": "team-aquatics",
      "over": { "start": "2026-07-07T09:00:00Z", "end": "2026-07-07T10:00:00Z" },
      "mode": "exclusive",
      "reason": "exclusive-vs-exclusive overlap" }
  ],
  "nearest_openings": [ { "start": "...", "end": "..." } ]
}
```
`reason` is a fixed enum (`exclusive-vs-exclusive overlap`, `capacity-exhausted`,
`sub-resource-locked`, `buffer-overlap`), never free text — so a junior can branch
on it in code.

---

## 7. Non-occurrence — deliberately has no endpoint

There is **no** path to assert "this was missed". The reconciliation sweeper (an
internal `gc.Sweeper`, the same mechanism `meta` TTL already uses) writes a
`missed` disposition when a `binding` booking's deadline passes without a
`complete`. This is stated here precisely so nobody adds the endpoint: positive
fulfilment is human-asserted (`complete`, §4); negative non-fulfilment is
machine-recorded. A missed booking surfaces through `GET .../bookings/list?state=missed`
with `verdict: "missed"` and its bitemporal stamps (due-time vs detected-time).

---

## 8. Multi-calendar match — "when are these all free?" (was unpathed)

The operation the primitive most exists for (F16), and the one with no prior
sketch. Top-level because it spans calendars.

```
POST /api/v2/cal/match
```
```json
{
  "calendars": ["pool-main", "team-maintenance", "inspector-alice"],
  "duration": "2h",
  "from": "2026-07-01T00:00:00Z",
  "to":   "2026-07-15T00:00:00Z",
  "mask": "weekdays"
}
```
Returns spans where **every** named calendar is simultaneously free for
`duration` — the N-way intersection. The bitmap AND, rollup pruning, and grid
reconciliation (H3) are entirely invisible; the caller sees coincident openings.

> **⚠ Review issue 1 — "free" against which plane? (undecided, and it is a meaning question).**
>
> `match` returns spans where every calendar is "free," but the spec never says
> which occupancy counts as *not-free* for this operation:
>
> - **Binding-only** — a span is free for matching if no *confirmed* booking
>   overlaps it; proposals are ignored. Two people matching against the same
>   calendar can both be offered the same span (each sees it as free), and they
>   race to `confirm`. Optimistic: maximises offered coincidence, accepts that some
>   offers will lose the confirm race.
> - **Binding-OR-proposed** — a span is free only if neither a confirmed *nor* a
>   proposed booking overlaps it. Proposals reserve optimistically; `match` never
>   offers a span someone is already holding tentatively. Pessimistic: fewer
>   offered spans, far fewer confirm-time clashes.
>
> This is **not** a tuning knob — it changes what the primitive *means* to a
> caller, and it has three downstream consequences that all have to move together:
>
> 1. **Codec hot path (`cal-pebble-codec.md` §3.4, §6.5).** Binding-only ANDs one
>    plane. Binding-OR-proposed ANDs two planes per calendar (or a precomputed
>    union plane), doubling the fine-AND work and forcing a decision on issue §6.5
>    of the codec — *does the proposed plane need its own rollup pyramid?* Under
>    binding-only it never does; under binding-OR-proposed it is on the hot path and
>    probably does. The codec cannot be finished until this is decided.
> 2. **The `idk` ternary (§3b) becomes reachable through `match`.** `availability`
>    already exposes `idk` (proposed-but-not-binding). If `match` is binding-only it
>    is *inconsistent* with a caller who reads `availability?q=capacity`, sees
>    `idk`, and reasonably expects `match` to treat that period as not-clean-free.
>    Whichever plane `match` uses, it should agree with how `capacity` already
>    treats proposals, or the two reads contradict each other.
> 3. **Contention semantics (F16).** The whole point of `match` is finding joint
>    availability; under binding-only, "joint availability" can evaporate between
>    the match and the confirm for *every* participant at once (the N-way confirm
>    race), which is exactly the multi-party coordination `match` exists to make
>    easy.
>
> **Recommendation:** make it an explicit, fixed request field —
> `consider=binding` (default) | `consider=binding+proposed` — rather than an
> implicit constant, so the caller chooses optimistic vs pessimistic per match and
> the codec knows statically which planes a given request touches. Decide the
> *default* before writing the bit layer, because the default determines whether
> the proposed-plane pyramid (codec §6.5) is v1 or deferred.

```json
{ "matches": [
    { "start": "2026-07-08T10:00:00Z", "end": "2026-07-08T12:00:00Z" },
    { "start": "2026-07-10T14:00:00Z", "end": "2026-07-10T16:00:00Z" }
  ],
  "count": 2,
  "checked": ["pool-main", "team-maintenance", "inspector-alice"]
}
```
If a calendar in the set has zero free coincidence, the response is
`"matches": [], "blocking": ["inspector-alice"]` — naming *which* timeline killed
the intersection, so the human knows whose schedule to renegotiate rather than
staring at an empty list.

---

## 9. Batch / sequencing — one transactional placement (was unpathed)

A set placed all-or-nothing (F7). A **consistency unit, not a workflow** — it
answers "do these coexist", never "run these in order".

```
POST /api/v2/cal/{calendar_id}/bookings/batch
```
```json
{
  "atomic": true,
  "bookings": [
    { "booking_id": "check-1", "when": {...}, "mode": "exclusive" },
    { "booking_id": "check-2", "when": {...}, "mode": "exclusive" }
  ],
  "spacing": { "min_apart": "5d" }
}
```
- `atomic: true` → all land or none do; partial success is never silently
  committed.
- `spacing` (optional) is the only inter-member constraint exposed: F7(b),
  "at least N apart". **Ordering (F7c) and arbitrary joint constraints (F7d) are
  intentionally absent** — they are the solver tarpit (A4) and do not belong on
  this surface.

Rejection (`409`) names the specific colliding members and reports
**partial satisfiability** — the sequencing-specific value:
```json
{ "committed": false,
  "placeable": ["check-1", "check-2"],
  "unplaceable": [
    { "booking_id": "check-3",
      "reason": "no slot satisfies spacing within window",
      "relaxation": "needs a window extended to 2026-08-20, or min_apart=3d" }
  ] }
}
```

---

## Complete endpoint index

```
# Calendars
POST   /api/v2/cal/def
GET    /api/v2/cal/list
GET    /api/v2/cal/{calendar_id}
PATCH  /api/v2/cal/{calendar_id}
DELETE /api/v2/cal/{calendar_id}
POST   /api/v2/cal/def/validate

# Bookings — CRUD
POST   /api/v2/cal/{calendar_id}/bookings/def
GET    /api/v2/cal/{calendar_id}/bookings/list
GET    /api/v2/cal/{calendar_id}/bookings/{booking_id}
DELETE /api/v2/cal/{calendar_id}/bookings/{booking_id}

# Bookings — lifecycle (A9)
PATCH  /api/v2/cal/{calendar_id}/bookings/{booking_id}/confirm
PATCH  /api/v2/cal/{calendar_id}/bookings/{booking_id}/decline
PATCH  /api/v2/cal/{calendar_id}/bookings/{booking_id}/propose
PATCH  /api/v2/cal/{calendar_id}/bookings/{booking_id}/complete

# Feasibility & movement
POST   /api/v2/cal/{calendar_id}/bookings/check
POST   /api/v2/cal/{calendar_id}/bookings/{booking_id}/move
POST   /api/v2/cal/{calendar_id}/bookings/batch

# Reading availability
GET    /api/v2/cal/{calendar_id}/openings
GET    /api/v2/cal/{calendar_id}/availability/{period}

# Cross-calendar
POST   /api/v2/cal/match
```

20 endpoints. Every operation the model requires is pathed; nothing carries a name
that needs the design doc to decode.

---

## What stays off the surface (so it doesn't creep back on)

- **No non-occurrence write** — sweeper-only (§7).
- **No ordering or arbitrary-constraint batch** — F7c/d are out (§9).
- **No objective beyond the fixed enum** — `openings.objective` cannot become a
  scoring function (§3a).
- **No exposed quantum/bitmap/mask vocabulary** — the engine's grid is never an
  API word (H3); `availability` and `match` speak periods and spans only.
- **No auto-cascade on move/cancel** — F9(a) is inadmissible; dependents are
  reported for the human to resolve, never silently rescheduled. (A `move` that
  strands a dependent returns it in the report; it does not touch it.)

---

## Domain fitness findings (2026-06-22 modelling pass)

The surface was modelled against three domains of increasing resource-coupling
strength — physical asset maintenance, hotel scheduling, and a hospital operating
theatre suite — to find where it bends. The lifecycle spine and the two-plane
availability model fit all three with no strain; the findings cluster on one axis.

### F-A. The lifecycle spine is domain-independent (strength, not issue)

`proposed → confirm → binding → complete → honoured`, with sweeper-written
`missed`, fit all three domains unchanged. The binding/complete/missed asymmetry
models regulated maintenance ("prove the service happened or prove it was
skipped"), a surgical no-show (required participant missed = audit event; optional
observer absent = nothing recorded), and a hotel no-show identically. This is the
most reusable element of the design and needs no change.

### F-B. The two-plane model fits hotels almost exactly (strength, not issue)

The binding/proposed split and the `idk` ternary (§3b) map onto a hotel's
confirmed-vs-held-but-unpaid distinction with zero modelling effort:
`100 − confirmed%` *is* occupancy rate, and `idk` *is* "reserved, card not yet
charged." The abstract ternary is concrete and correct in the field.

### F-C. Recurrence and ordering are unlabelled deliberate exclusions

Maintenance is overwhelmingly recurring ("service every 90 days") and frequently
ordered ("drain before service"). The surface has neither a recurrence primitive
(`when` is always a concrete span, §2) nor an A-before-B constraint (F7c ordering
is out, §9). Both omissions are *correct* — recurrence-expansion and ordering are
solver tarpits — but they are not named in "What stays off the surface," so a
modeller cannot tell whether their absence is a decision or an oversight. This is
the same class as review issue 2: a correct exclusion that reads as a gap because
it is unlabelled.

**Fix:** add both to "What stays off the surface," stating that recurrence
expansion and inter-booking ordering live *above* `cal` (a scheduler process emits
individual `bookings/def` calls; `cal` validates each against live occupancy).

### F-D. `capacity` and `sub:` composition is undecided, and hotels need it

A hotel room-type is naturally `capacity: 30` with `shared` bookings — and for
*availability* this is a superb fit. But `shared` consumes a *fungible* unit; it
does not book *room 214*. The moment a guest needs "the same specific room for all
five nights" or "a particular accessible room," identity is required, and the only
tool for it is `mode: sub:<child_id>`. The spec neither promises nor forbids a
calendar being **both** capacity-counted (for availability) **and** sub-addressable
(for identity) — which is exactly what a hotel needs. This ambiguity is
load-bearing for the commonest scheduling domain there is.

**Fix:** state explicitly whether `capacity` and `sub:` compose on one calendar,
and if so, how an availability read counts a `sub:`-locked child against the pool.

### F-E. v1 single-grain penalises night-resolution domains

A hotel trades in *nights* (~21h, crossing UTC midnight — handled, codec §3.3),
not 5-minute quanta. Modelled in v1, a hotel room is a `k = 288` daily-cell
calendar forced to store at the 5-minute base grain — 288× more bitmap resolution
than it uses. Sparse-by-day storage (codec §2.4) softens this, but hotels are the
domain most penalised by the deferral of per-resource multiples (`k > 1`, codec
§2.1 "v1 simplification"). Not a correctness issue; a flag that the deferred
`k × Q_base` feature has a concrete first customer.

### F-F. No atomic cross-calendar placement (the category gap)

This is the load-bearing finding. An operating theatre event consumes an OR *and*
a surgeon *and* an anaesthetist *and* an equipment set, **simultaneously and
all-or-nothing**. The same shape recurs in every coupled-resource domain: a court
hearing (judge + room + clerk), a film shoot (actor + location + crew), a class
(teacher + room + lab-kit).

The surface has the two halves of this operation but not the whole:

- `match` (§8) spans calendars but is **read-only** — it *discovers* coincident
  free spans across N calendars, it does not seize them.
- `batch` (§9) places multiple bookings **atomically but within one calendar** —
  it cannot span calendars.

So **atomic multi-calendar placement falls in the crack between `match` and
`batch`.** A caller can only place optimistically: `match` to find the span, then
fire N independent `bookings/def` calls and hope none lost a race in between — and
on the Kth failure, manually compensate the K−1 that already committed. That
manual rollback is exactly the partial-failure hazard `batch`'s atomicity exists
to abolish, just not across the boundary that matters here. The design calls
`match` "the operation the primitive most exists for" (F16) — but for the domains
that motivate `match`, it only gives you the discovery half.

**Note — what entity composition does and does not reach.** The one-calendar /
one-entity model (§1; codec §3.2a) *does* dissolve two adjacent problems, and they
should not be confused with this one:

- **Plural liability is solved.** A booking's `bearer` is a single handle, but
  that handle may point at a *composite entity* — create an entity (its own id)
  holding REF fields to the surgeon and the anaesthetist, and point `bearer` at
  it. The graph does the fan-out; `cal` sees one accountable handle. (An earlier
  draft of this pass wrongly claimed the model "flattens" plural liability; it
  does not — composition expresses it exactly, the same way a team is just an
  entity with a calendar.)
- **Collective availability is solved the same way.** A team/composite entity has
  its own calendar; a process above `cal` keeps it in sync with its members
  (§1, codec §3.2a).

But composition **does not** close the placement gap. Booking a composite
entity's calendar does not atomically seize its *members'* calendars — those
remain distinct bitmaps whose only joint operation is the read-only `match`. So
"who is accountable" and "when is the team collectively free" are answered by
composition; "seize the OR and the surgeon and the cart in one all-or-nothing
act" is not, because that is a write across distinct calendars, and no such write
exists.

**Proposed fix (meaning-level — same severity class as review issue 1):** add a
`match`-then-seize counterpart — the atomic write that `match` is the read for.
Sketch:

```
POST /api/v2/cal/match/commit
```
```json
{
  "calendars": ["or-3", "surgeon-okonkwo", "anaesthetist-pool", "cart-laparoscopy"],
  "when": { "start": "2026-07-08T09:00:00Z", "end": "2026-07-08T12:00:00Z" },
  "per_calendar": {
    "or-3":              { "booking_id": "op-1142-room",   "mode": "exclusive" },
    "surgeon-okonkwo":   { "booking_id": "op-1142-surgeon","mode": "exclusive" },
    "anaesthetist-pool": { "booking_id": "op-1142-anaes",  "mode": "shared" },
    "cart-laparoscopy":  { "booking_id": "op-1142-cart",   "mode": "exclusive" }
  },
  "atomic": true
}
```
All N bookings land or none do; on conflict it returns the universal conflict
report (§6) naming the blocking calendar(s), and **nothing is committed** —
identical guarantee to `batch`, lifted to the cross-calendar boundary `match`
already spans. This keeps the anti-solver line intact (it is a consistency unit,
not a workflow — no ordering, no objective), and it is the natural completion of
`match`: discovery and seizure as a read/write pair over the same calendar set.

> **Open question for v1 scoping (flag, not a decision):** `match/commit` may
> interact with the codec's sealed-past / store-split decisions (codec §6.9) and
> with the cross-plane atomicity already required by `confirm` (codec §5). It is a
> cross-calendar, possibly cross-store transaction. Whether it belongs in v1 or is
> deferred behind a trigger is a scoping call to make against the codec, not in
> this REST doc alone.

### F-G. Composite-calendar sync has no stated consistency contract

F-F establishes that entity composition is how `cal` expresses multi-entity
relationships: a team/composite entity has its own calendar, and "some process
above `cal` keeps the calendar in sync" with its members (§1; codec §3.2a, "some
process composes the members and writes the team calendar"). That sync process is
named but never specified — in particular, its **consistency contract is absent.**

For the use this mechanism was introduced to serve, that is fine. Collective
*availability* is a read: a team calendar that lags its members by a sweep
interval is acceptable, the same way any materialised view is. Eventual
consistency is the right and obvious default, and most likely the intended one.

The gap only bites if anything ever needs a composite calendar to be
*transactionally* consistent with its members — and F-F's `match/commit` is
exactly the operation that could create that need. Consider the OR team modelled
*as a composite calendar* (rather than as N member calendars matched directly):

- If `match/commit` seizes the **member** calendars (or-3, surgeon, anaesthetist,
  cart) directly, the composite-calendar consistency question never arises —
  there is no composite calendar in the placement path, only in the accountability
  ref. **This is the clean modelling, and it is the one F-F's sketch already
  uses.** The composite entity is the `bearer`; the members are the seized
  calendars. No transactional sync needed.
- If instead a caller matches/commits against a **composite team calendar** and
  expects that to reflect — atomically — the members' occupancy, the "some process
  above keeps it in sync" hand-wave becomes load-bearing: a placement read against
  a stale composite calendar can offer a span a member no longer has free, and the
  commit either races or silently overbooks the member. Composition was a
  read-side projection; it cannot bear a transactional write without a stated
  contract.

So the finding is narrow and the fix is one sentence, not a mechanism: **state
that composite-calendar sync is eventually consistent, and therefore a composite
calendar is a valid target for availability reads (`availability`, `openings`,
`match`) but never for a binding write that must be atomic with its members'
occupancy.** Atomic placement always targets the underlying member calendars
directly (as F-F's `match/commit` does), never a composite projection of them.
This keeps composition firmly read-side, which is what makes "some process above
keeps it in sync" safe to leave unspecified: an eventually-consistent projection
needs no transaction, and the one operation that would need one (atomic placement)
is routed around it by construction.

> **Why this is a flag, not a defect.** The clean modelling (members as the seized
> calendars, composite entity as the `bearer` ref) already avoids the hazard
> entirely — F-F's sketch does it correctly without comment. F-G only asks that the
> docs *say* composite calendars are read-side, so a future modeller does not
> reach for "match/commit against the team calendar" and discover the missing
> contract the hard way. It is the same shape as F-C and review issue 2: a correct
> design boundary that is currently implicit and should be made explicit.

### Summary table

| Finding | Domain that surfaced it | Class | Status |
|---|---|---|---|
| F-A lifecycle spine fits all three | all | strength | — |
| F-B two-plane model fits hotels exactly | hotel | strength | — |
| F-C recurrence/ordering unlabelled | maintenance | legibility | **fix: label in "off the surface"** |
| F-D `capacity` × `sub:` composition undecided | hotel | semantics | **fix: one sentence either way** |
| F-E v1 single-grain penalises night domains | hotel | efficiency / deferral | flag — `k>1` has a first customer |
| F-F no atomic cross-calendar placement | operating theatre | **category gap** | **proposed: `match/commit`; v1-scoping open** |
| F-G composite-calendar sync has no consistency contract | operating theatre | legibility / scope boundary | **fix: declare composite calendars read-side only** |
