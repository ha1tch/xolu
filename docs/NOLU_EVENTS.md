# NOLU_EVENTS.md — federation-consistent event subjects and references

Status: **investigation / design proposal.** Not implemented. This documents a
considered design for reshaping xolu's event subjects and entity references to be
*consistent with* (not conformant to) nolu's federation model, plus a change to
propose back to the nolu team. It is separate from the shipped 1.0 event model
(see `EVENT_MODEL.md`) and from `EVENT_PENDING.md`.

The distinction this document rests on:

- **Conformance** = xolu obeys nolu's rules; xolu events are valid nolu events.
- **Consistency** = xolu *resembles* nolu — same idioms, shapes, and instincts —
  so translation between the layers is mechanical and a developer moving between
  them is not surprised.

xolu aims for **consistency, not conformance.** The two namespaces are peers at
different layers, not one obeying the other.

---

## 0. Settled decisions (final within xolu's scope)

These are **final** as conventions for xolu. They govern how xolu names things
even before the subject-matching reshape (§3.1) is implemented.

| Decision | Final form |
|----------|-----------|
| Outgoing message subjects | **Dotted**, NATS-style, general-to-specific |
| Root namespace of xolu's outgoing messages | **`xolu`** (e.g. `xolu.entity.asset.created`, `xolu.fsm.machine.step`), a peer of `nolu.events.*` |
| Portable string identity (when a string form is needed) | **URI with `/`** delimiters, consistent with nolu's GlobalID (`nolu://host/type/uuid`) |
| Structured references / handles | **Struct fields** (`entity_type`, `id`, `tenant_id`), consistent with nolu's `LocalRef` — **not** a delimited composite string |
| Double-underscore (`__`) namespace separator for Go/JSON | **Not adopted.** No context requires the dotted hierarchy as a single token: routing uses the dotted subject, structured access uses fields. A third separator convention nolu does not share would only add divergence. |
| Constraint on dynamic entity-type names appearing in subjects | **No dots** (the subject delimiter), validated at entity-schema registration. Single underscores remain legal (`audit_log` is fine). |

**Not settled within xolu alone — these remain open and are documented below:**

- **Segment ordering** (entity-type-first vs nolu's current kind-first) — a
  *cross-project consistency* decision to settle with the nolu team (§5).
  xolu should not unilaterally harden an ordering that diverges from nolu.
- **The subject-matching engine** (exact `event_type` equality → NATS-style
  pattern matching) — designed here (§3.1) but **deferred**, not part of the 1.0
  event model. The flat shipped subjects (`entity.created`, `fsm.step`,
  `commit.applied`) remain in force until that work is done (§7).

In other words: the *naming conventions* are final; the *subject reshape and
matcher* are a deferred, scoped effort, and the *ordering* is a shared decision
pending with nolu.

---

## 1. What nolu already does (v0.7.9, observed from source)

### 1.1 Identity

- **GlobalID** — a portable URI: `nolu://<registry-host>/<entity-type>/<uuid>`.
  Slash-delimited, entity-type as a single path segment, UUID (v7-intended) as
  the stable identity. Minted and owned by nolu; never reused.
- **LocalRef** — a *structured struct*, deliberately **not** a URI, "so nolu can
  construct the xolu call without string parsing":
  `{ InstanceURL, TenantID, EntityType, LocalID, TenantName? }`
  (`json:"instance_url" / "tenant_id" / "entity_type" / "local_id" / "tenant_name"`).
- The registry maintains `GlobalID ↔ LocalRef` and updates it as entities change
  hands.

The key lesson: nolu **separates portable string identity (URI, for transport)
from the structured local handle (fields, for use)**. It does not cram
type+instance+id into one delimited token.

### 1.2 Subjects (NATS)

- Convention: `nolu.events.<kind>.<entity-type>`.
  Examples: `nolu.events.registered.devices`, `nolu.events.transferred.shelves`,
  `nolu.events.retired.users`.
- Catch-all: `nolu.events.>`.
- **Ordering is kind-first**: `<kind>.<entity-type>`.
- Matching (`matchSubject`) currently supports only the **`>` trailing recursive
  wildcard** — exact match, or a pattern ending in `>` matched as a prefix. It
  does **not** yet support `*` (single-token wildcard).
- The `Envelope` redundantly carries the segments both in the composed `Subject`
  string and as structured fields (`Kind`, `EntityType`, `GlobalID`, `At`,
  `Payload`).

---

## 2. The two-namespace model

xolu and nolu emit **different classes of event** and should occupy **separate
top-level subject namespaces**, as peers:

- `nolu.events.*` — federation events: an entity was *registered*,
  *transferred*, *retired* across the network. About global identity and
  ownership moving through the federation.
- `xolu.*` — instance events: entity created/updated/deleted, an FSM stepped, a
  commit applied. About what happens inside one xolu instance.

These are not the same events seen from two angles. `transferred` only means
something in a federation; `fsm.step` only means something inside an instance.
nolu, when it federates an xolu entity, mints its **own** `nolu.events.*` subject
— xolu does not emit `nolu.events.*`. A subscriber picks the layer:
`nolu.events.>` for federation activity, `xolu.>` for instance activity, or both
on a shared bus.

So xolu does **not** conform to nolu's subjects (it has its own namespace); it is
**consistent** with nolu's *conventions*.

---

## 3. Proposed xolu subject design

Root namespace `xolu.`, three-level, general-to-specific:

```
xolu.<namespace>.<kind>.<event>
```

Examples (entity-type-first ordering — see §5):

```
xolu.entity.asset.created
xolu.entity.asset.updated
xolu.entity.sensor.created
xolu.fsm.machine.step
xolu.fsm.machine.output
xolu.commit.applied            (no kind segment — commits have no single object; see below)
```

Subscriptions become prefix/wildcard expressions:

```
xolu.entity.asset.>     all asset events
xolu.fsm.machine.>      all machine events
xolu.>                  all instance events
```

Note `commit.applied` has no entity-kind segment: a commit operates on no single
object, so its subject stays `xolu.commit.applied` (consistent with
`EVENT_PENDING.md` §6b recording commit source as intentionally coarse).

### 3.1 This requires a subject *matcher*, not a string rename

The shipped dispatch matches event types by **exact SQL equality**
(`WHERE event_type = ?`). Three-level subjects deliver no benefit under exact
equality — the entire point is wildcard subscription (`xolu.entity.asset.>`),
which equality cannot do. So this design requires introducing **NATS-style
subject matching** into dispatch:

- a def's `event_type` column becomes a *subject pattern* (may contain `>` and,
  if adopted, `*`),
- `matchEventDefs` matches the fired subject against stored *patterns*, not by
  equality,
- behaviour should mirror nolu's `matchSubject` for consistency (at minimum `>`;
  `*` single-token is a possible shared extension — nolu lacks it today).

This is a matching-engine change and a meaning change to the shipped
`event_type` column (exact type → subject pattern). It is the substantive part of
this work, and the delivery-critical part: a careless matcher silently drops
events.

---

## 4. Proposed xolu event reference (REF)

The event payload's subject identity should be a structured REF whose field names
are **consistent with** nolu's `LocalRef`, so the mapping to a nolu `LocalRef`
is mechanical and lossless — without xolu taking a hard dependency on the
federation model.

```json
{ "entity_type": "asset", "id": 9200, "tenant_id": 1 }
```

Consistency, not conformance, means:

- field names match `LocalRef` (`entity_type`, `tenant_id`, `id`↔`local_id`),
  so nolu can complete the REF into a full `LocalRef`/`GlobalID` at the
  federation boundary;
- xolu carries only what an instance **reliably knows** — entity type, id,
  tenant. It does **not** carry `instance_url` or a `GlobalID`: an instance may
  be unregistered, behind a proxy, or not federated at all. nolu, which knows
  the instance URL and owns the `GlobalID ↔ LocalRef` mapping, supplies the
  federation half.

So an xolu event REF is the *local half* of a nolu `LocalRef`, named
consistently, and is mappable-to (not identical-to) it. xolu stays usable
stand-alone; federation completes the reference when present.

This supersedes the earlier `{type, entity, id}` shape and the `type: "REF"`
marker debate, and it also retires the `__` double-underscore separator idea:
nolu shows the system's separators are **dot for subjects, `/` for portable URIs,
struct fields for handles** — no third separator is needed.

---

## 5. Change to propose back to the nolu team: segment ordering

nolu currently orders subjects **kind-first**:
`nolu.events.<kind>.<entity-type>` (`nolu.events.registered.devices`).

**Proposal: entity-type-first** — `nolu.events.<entity-type>.<kind>`
(`nolu.events.devices.registered`), and correspondingly
`xolu.entity.<entity-type>.<event>`.

Rationale — prefix-subscribability of the more common federation query:

- Kind-first makes "all events of a given *kind*" the prefix case
  (`nolu.events.registered.*`) and "all events about a given *entity-type*" a
  middle-wildcard (`nolu.events.*.devices`).
- Entity-type-first inverts it: "all events about *devices*"
  (`nolu.events.devices.>`) becomes a clean prefix, which is the more common
  "tell me everything about this thing" subscription.

Since nolu is incipient and the ordering should be **one decision applied to both
namespaces**, this is worth raising with the nolu team before either project
hardens its subject vocabulary. It is a *consistency* decision (symmetric —
either project can move), not conformance.

Dependency: entity-type-first also pairs better with a future `*` single-token
wildcard (e.g. `xolu.entity.*.created` = all creations across kinds), which nolu
does not yet support.

---

## 6. Naming conventions (resolved by observing nolu)

- **Subjects**: lowercase, **dot**-delimited, NATS-native. (`xolu.entity.asset.created`)
- **Portable identity**: URI with **`/`** delimiters. (`nolu://host/type/uuid`)
- **Handles / structured refs**: struct fields, no delimiter games. (`LocalRef`)
- The **double-underscore** separator considered earlier for Go/JSON is **not
  adopted** — nolu demonstrates the system already has a separator answer per
  context, and a third convention would only add divergence.
- Dynamic entity-type names embedded in subjects must avoid the subject
  delimiter (`.`); this is the one naming constraint the subject design imposes,
  validated at entity-schema registration. (Single underscores in names, e.g.
  `audit_log`, remain fine — they are not subject delimiters.)

---

## 7. Scope and sequencing

This is **not** a 1.0 close-out item. It is a coherent, separate body of work:

- it reshapes the shipped subject vocabulary (`validEventTypes`, the
  `event_latch_kind`/`event_latch_source` values),
- it changes the meaning of the `event_type` def column (exact → pattern),
- it adds a **subject-matching engine** to dispatch (delivery-critical),
- it changes the payload REF field names,
- and item §5 is a cross-project proposal that should be settled with the nolu
  team first.

Recommended sequencing: **close the 1.0 event model first** (canonical S11 test,
full gate, version bump), then take this on as its own focused effort with the
matcher built and verified on the wire — rather than reshape the subject contract
and add a matcher in the same pass that is meant to finish.

When implemented, the present flat subjects (`entity.created`, `fsm.step`,
`commit.applied`) migrate to the `xolu.`-rooted three-level forms, and the
`origin.event_latch_kind` / `event_latch_source` values move to the new subject
strings.
