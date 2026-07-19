# Event Model

Status: design specification (towards xolu 1.0)
Supersedes: the "event subscription" / "trigger" terminology used in earlier
drafts of `API_V2.md` and `archive/S9_WORK_STRATEGY.md`.

This document is the single source of truth for event nomenclature and event
payload shapes. Where any other document disagrees with this one, this one is
correct and the other is to be corrected.

---

## 1. Nomenclature

The earlier drafts used "subscription", "trigger", and "event" interchangeably,
which conflated three distinct things and one process. This section fixes a
single vocabulary. It is not cosmetic: the words name distinct objects with
distinct lifetimes and cardinalities, and the payload shapes below depend on the
distinction being held.

The vocabulary aligns with the established `def` convention already used across
the v2 surface (`/fsm/def`, `/rollup/def`): `def` names the standing definition,
and the named runtime object it governs is separate.

### 1.1 The four terms

**Event type** — a *kind* of thing that can happen. Examples: `fsm.output`,
`fsm.step`, `entity.created`, `commit.applied`. Event types are a closed,
documented set. This is the most abstract term: it names a category, not an
occurrence.

**Event def** — a *standing definition*, created once via `POST /event/def`,
persistent, tenant-wide. An event def declares: when an event of a given type
occurs (optionally filtered), deliver to a target (a webhook, or an OQL action,
or another part of xolu). An event def has an id and exists whether or not it
ever activates. There is one event def per definition. Consistent with
`/fsm/def` and `/rollup/def`.

**Event firing** — the *process* launched when an event def matches an eventable
action (see 1.2). A firing is a runtime occurrence with a lifecycle (matched →
delivering → delivered/failed). It is recorded in the delivery log. There are
many firings per event def — one per activation. A firing owns the
moment-in-time payload: the linked-data REFs, the affected states, and a source
reference back to the event def that produced it and the event type it
instantiates.

**Event notification** — the *message delivered* by a firing to its target. The
notification is the content (the rendered payload sent to a webhook, or the OQL
executed). One firing may produce more than one notification (fan-out to
multiple targets, or a re-delivery on a future retry mechanism). The notification
is what travels; the firing is the process that produces and delivers it.

### 1.2 Eventable actions

An **eventable action** (equivalently **evented action**) is an operation that
*can* cause a firing. Not all operations are eventable. The eventable actions in
1.0 are:

- a machine **output** (a Mealy emission) — fires `fsm.output`
- a machine **step** (a committed state transition) — fires `fsm.step`
- a successful **commit** (an atomic entity write) — fires `commit.applied`

A read is not eventable. The term gives the design a precise word for "the kind
of operation that carries a firing check", and tells subscribers which
operations can set an event def in motion.

### 1.3 What replaces what

| Earlier draft term            | Correct term                          |
|-------------------------------|---------------------------------------|
| event subscription            | event def                             |
| subscription (the object)     | event def                             |
| `/event` (the route)          | `/event/def`                          |
| `subscription_id` (column)    | `event_def_id`                        |
| trigger (the standing rule)   | event def                             |
| trigger (the act of firing)   | event firing                          |
| the delivered payload         | event notification                    |
| "an event" (ambiguous)        | event firing (process) **or** event notification (message), as appropriate |

The word "trigger" is retired from the event vocabulary entirely. It carries
stored-procedure connotations and was used for both the standing rule and the
act of firing; both meanings now have precise terms.

---

## 2. Latch points

A machine has **two** independent latch points. They are orthogonal: a single
transition may activate either, both, or (for a no-output self-loop) only the
step latch.

### 2.1 `fsm.output` — per Mealy emission

Fires once per output string produced by a transition. Carries the minimal
Mealy fact and nothing more.

Payload (`event.data`):

```
output      : string   the Mealy output symbol (e.g. "asset_activated")
machine_id  : int      the machine that emitted it
```

This is the pure state-machine fact: "machine M emitted output O." A transition
that produces no output produces no `fsm.output` firing. A transition that
produces several outputs produces several `fsm.output` firings.

### 2.2 `fsm.step` — per committed transition

Fires once per committed state transition, regardless of whether the transition
produced any output. This is the latch a subscriber uses to react to a state
change as such ("machine M entered InService").

Payload (`event.data`):

```
machine_id  : int                     the machine that stepped
previous    : string                  the state before the transition
current     : string                  the state after the transition
terminal    : bool                    whether `current` is a terminal state
vars        : object                  the machine variables after the set-clause
```

The `previous`/`current`/`terminal`/`vars` facts are available at zero
additional cost: the walk reads prior state in order to evaluate guards and
select a transition, so the delta is a free byproduct of work the operation
must do regardless. This is the same principle as the commit event in §3 — carry
the now-or-never fact that the operation already holds; do not reconstruct it.

A `fsm.step` fired as part of a commit (an `fsm_walk` embedded in a `/commit`
request) additionally carries the commit's affected-entity context — see §3.3.

---

## 3. The commit event (`commit.applied`)

### 3.1 Shape

A successful commit fires one `commit.applied` event. Its payload reports the
free outcome facts plus a copy of the originating commit request. This is the
*firing-level* payload (`event.data`); on the wire it is wrapped in the delivery
envelope (§5) as the `message`, with xolu's `origin` provenance alongside.

Firing-level payload (`event.data`):

```
affected    : array of objects        one per entity the commit wrote, each:
                { ref      : REF       { "type":"REF", "entity":..., "id":... }
                  created  : bool      true if the entity was created, false if updated
                  version  : int }     the entity version after the commit
request     : object                  a copy of the CommitRequest that was applied
                                       (update, append, timeseries, fsm_walk)
```

The `affected` array enumerates **all** entities the transaction mutated: the
`update` and every `append`. Each is reported as a qualified REF (an address,
resolvable for current state) plus the two now-or-never outcome facts `created`
and `version`.

The `request` field is a copy of the commit request payload. It carries the
*intent of the mutation* — exactly what fields were written, to which entities,
with which appends and embedded walk.

### 3.2 Why the request copy is trustworthy: the atomicity contract

A request payload is normally only *intent*: "here is what I asked to be
written." Intent and outcome can diverge — a write can conflict, fail, or roll
back. Attaching a request payload to an event would therefore normally be
unreliable.

It is reliable here because of one ordering guarantee:

> **A `commit.applied` event fires only after the atomic transaction commits
> successfully. Therefore the attached `request` is an accurate record of what
> was committed, not merely what was requested.**

The transaction is all-or-nothing. Either the whole `CommitRequest` applied
(update, appends, embedded walk, atomically) or none of it did and the firing
never happened. There is no partial-application state. Atomicity collapses intent
and outcome into the same thing at the moment of success, so the request copy
*is* a record of what is now durable in the store.

This is the same guarantee a change-data-capture system builds elaborate
machinery to provide — that the captured change exactly matches what was durably
committed — obtained for free by firing post-success with the request in hand.
No read-before-write, no diff, no snapshot-consistency concern.

### 3.3 Relationship to embedded `fsm.step`

When a commit includes an `fsm_walk`, the embedded walk's `fsm.step` (and any
`fsm.output`) fire as well as the `commit.applied` event. The `fsm.step` from an
embedded walk carries its own state delta (§2.2) and, because it occurred in the
same atomic transaction as the entity write, the same `affected` REF context as
the `commit.applied` event. A walk is a walk regardless of how it was invoked:
standalone and commit-embedded walks fire the same latches with the same
semantics. The only difference is that a commit-embedded walk additionally has
affected-entity context to attach, because a commit occurred.

### 3.4 What is deliberately NOT in the 1.0 commit event

The following are deferred to Part 2 as an **opt-in event-def feature**, not
silently omitted:

- **Field-level old→new deltas.** Reporting "field X went from A to B" for an
  ordinary (non-FSM) field requires a read-before-write that the commit path
  does not otherwise perform. Unlike the FSM step (which reads prior state to
  function), commit does not need the prior document for its own work, so a
  field diff is a genuine added cost on the write hot path. It is therefore
  deferred and made opt-in per event def, so the cost is paid only when a def
  actually requests it.

- **Changed-REF / changed-edge reporting.** The graph layer maintains edges by
  delete-and-recreate on write rather than by diffing, so "which relationships
  changed" also requires capturing prior edge state. Deferred together with
  field-level deltas as one coherent opt-in feature.

The 1.0 commit event carries only facts available at zero added cost on the
commit path: the affected REFs, the `created`/`version` outcome facts, and the
request copy (which is already in hand from the moment the request is decoded).

### 3.5 Known 1.0 caveats

- **Notification payload size.** Unlike `fsm.output`/`fsm.step`, a commit
  request payload is unbounded (a large `update.data`, many `append` entries).
  The `commit.applied` notification is correspondingly unbounded, and the
  delivery log stores it per firing. Bounding/streaming large payloads is a
  Part-2 concern.

- **No field redaction.** The `request` copy carries the whole request,
  including any fields a target should not see. Field-level redaction is a
  Part-2 concern. For 1.0 the subscriber is assumed to be the system owner's own
  target.

---

## 4. Delivery semantics (1.0)

These are the Part-1 (preview) delivery properties, unchanged by this document
and restated here for completeness:

- Firings are dispatched **asynchronously**, after the originating transaction
  commits, never inside it. A firing cannot roll back the transaction that
  produced it. A failed notification does not fail the originating operation.
- Delivery is **at-most-once, single-attempt**: a firing attempts its
  notification delivery once. There is no retry, no dead-letter, no
  ordering guarantee between firings produced by one request.
- Making "delivered" mean "provably delivered" (a reconciliation sweep over
  firings whose notification delivery is unconfirmed) is a Part-2 concern. This
  matters specifically for critical-entity backup defs, where at-most-once is
  insufficient without a reconciliation sweep.

---

## 5. The delivered notification envelope

Every webhook notification is delivered as a two-part envelope:

```json
{
  "origin":  { ... },
  "message": { ... }
}
```

`origin` is **stamped by xolu on every delivery** and cannot be suppressed or
altered by the event def. `message` is what the def's body produces — a rendered
jsonplate, a `{{...}}` body-string, or (when the def specifies neither) a default
event envelope. The author controls `message`; xolu owns `origin`.

### 5.1 `origin` — provenance

| Field | Meaning |
|-------|---------|
| `agent` | Always `"xolu"`. |
| `agent_version` | The emitting xolu version (e.g. `"0.10.0"`). |
| `event_def_id` | The id of the event def that fired this notification. |
| `event_latch_kind` | The event type that fired (`fsm.step`, `fsm.output`, `commit.applied`, `entity.created`, …). |
| `event_latch_source` | The specific source within the kind. For FSM events, the transition coordinates `fsm/<from>:<input>:<to>` (e.g. `fsm/AwaitingInspection:inspection_passed:InService`). For `commit.applied`, equal to the kind (a commit has no single object). |
| `fired_at` | RFC3339-nano timestamp stamped **after** the originating commit and **immediately before** the notification is sent — the dispatch time, for downstream timeout/retry/latency measurement. |

Example `origin`:

```json
{
  "agent": "xolu",
  "agent_version": "0.10.0",
  "event_def_id": 1,
  "event_latch_kind": "fsm.step",
  "event_latch_source": "fsm/Provisioning:ready_for_inspection:AwaitingInspection",
  "fired_at": "2026-06-19T01:20:21.641610523Z"
}
```

### 5.2 Type-faithful values

Ids and other scalars in both `origin` and the structured `message` carry their
**native JSON type** — ids are numbers (`"id": 9100`, not `"9100"`), booleans are
booleans (`"terminal": false`), numbers are numbers. Stringification happens only
inside `{{...}}` text-template interpolation, the one context that requires a
string. See `EVENT_PENDING.md` §6a.

(`message.entity` / the envelope `entity` field remains a string: it is the
entity *name* for entity/commit events, and a machine-id label for fsm events —
the machine's numeric id is carried natively in the id field and in
`data.machine_id`.)

### 5.3 The `definition` namespace (FSM events)

FSM events (`fsm.step`, `fsm.output`) carry the machine's definition spec as a
`definition` namespace in the event data, alongside the firing facts
(`previous`/`current`/`terminal`/`vars`/`machine_id`) and, for commit-embedded
walks, `affected`. A jsonplate can reference definition facts:

```json
{ "machine": { "$ref": "definition.name" },
  "is_terminal": { "$ref": "definition.states.InService.terminal" } }
```

The definition is decoded fresh from the machine's snapshot (an independent copy;
it cannot mutate the running machine). References to absent definition paths
resolve to `null` (jsonplate's standard degradation), so the namespace is safe to
reference whether or not a given fact is present. See `EVENT_PENDING.md` §6b.

### 5.4 Shaping the message: jsonplate

The `message` body is shaped by the def's `config`. The preferred form is a
**jsonplate** — a JSON template whose `{"$ref": "path"}` leaves resolve against
the event data (via queryfy paths: `affected[0].ref.id`, `definition.name`,
`vars.retries`). Literals pass through; absent paths render `null`. A `{{...}}`
body-string remains supported for simple cases and coexists with jsonplate.

See `jsonplate.md` for the full jsonplate reference (what it is, when to use it,
how it differs from Go's `text/template`, and how to apply it).
