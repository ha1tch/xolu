# Event subsystem — pending resolution

Status: working scratch document. **Disposable.** Delete once the items here are
either implemented (and folded into `API_V2.md`) or formally abandoned.

This document collects everything about the event subsystem that is *not* part
of the shipped 1.0 surface. It exists because earlier drafts of `API_V2.md`
described a large imagined design in present tense, as though it were live. That
material has been removed from `API_V2.md` (which now describes only what
shipped) and parked here.

Two distinct kinds of thing live below, and they are **not** equivalent. Each
item is tagged:

- **[ROADMAP]** — sound design, simply not implemented yet. Intended for a later
  stage (S13–S17 / Part 2). Safe to build from.
- **[SUPERSEDED]** — described in the old draft but *wrong* — contradicted by what
  was actually built. Kept only so the divergence is on record. **Do not build
  from these without redesign.**
- **[DECISION]** — needs a call before it can move to ROADMAP or be dropped.

The authoritative description of the *shipped* event model is `EVENT_MODEL.md`.
The authoritative description of the *live API surface* is `API_V2.md`.

---

## 1. Request shape

### [SUPERSEDED] The nested `trigger` / `action` / `name` request body

The old draft documented event defs created with a nested body:

```json
{
  "name": "asset-activated-notify",
  "trigger": { "source": "fsm.output", "filter": { "output": "asset_activated" } },
  "action":  { "type": "webhook", "url": "...", "retry": { ... } },
  "execution": "async"
}
```

**This shape was never implemented.** The shipped contract is flat:

```json
{
  "event_type":  "fsm.output",
  "action_type": "webhook",
  "config":      { "url": "..." },
  "execution":   "async"
}
```

There is no `name`, no `trigger` object, no nested `action` object, no `source`,
no top-level `filter`, no `retry` in the live API. Filtering, where it exists,
is carried inside the opaque `config` blob, not a structured `filter`/`source`
pair. Any future enrichment of the request shape must start from the flat
contract that exists, not from this superseded nested design.

---

## 2. Execution

### [ROADMAP] Synchronous (in-transaction) execution

The old draft described `"execution": "sync"` as running the action *inside* the
triggering transaction, rolling the transaction back on action failure, and
returning the action result (e.g. an `fsm.walk` result) to the caller.

Shipped reality (see `EVENT_MODEL.md` §4): execution is **always asynchronous**.
A def may declare `"execution": "sync"`; it is accepted and stored, but Part 1
always runs async and the response carries `X-Executed-As: async`. True
sync-with-rollback is deferred. This is a legitimate future capability, not a
mistake — but it is not live.

---

## 3. Delivery reliability

### [ROADMAP] Retry with backoff

The old draft showed `"retry": { "max_attempts": 3, "backoff": "exponential" }`
on the action. Not implemented. Delivery is single-attempt, at-most-once.

### [ROADMAP] Dead-letter status, inspection, and replay

The old draft described a full dead-letter sub-API:

```
GET    /api/v2/event/{id}/deadletter
GET    /api/v2/event/{id}/deadletter/{entry_id}
POST   /api/v2/event/{id}/deadletter/{entry_id}/replay
DELETE /api/v2/event/{id}/deadletter/{entry_id}
```

with entries moving to dead-letter status after exhausting retries, exempt from
retention purge, replayable. **None of this is implemented.** It is a coherent
Part-2 design (and a necessary one for any at-least-once guarantee), but at 1.0
there is no retry, therefore no dead-letter, therefore no replay.

### [ROADMAP] Delivery-log retention GC

The old draft described `XOLU_EVENT_LOG_RETENTION` (default `30d`) and an
`event.GCSweeper` purging old log rows. Not implemented as described.

### [DECISION] Reconciliation sweep for critical-entity backup

At-most-once single-attempt delivery is insufficient for the critical-entity
backup pattern (an event def whose action copies entities to a blob store):
"wired up" is not "provably complete". A reconciliation sweep over firings whose
notification delivery is unconfirmed would close this. Needs a design decision on
where the sweep lives and how it detects unconfirmed firings. Until resolved,
critical-entity backup via event def is best-effort only.

---

## 4. Event types not yet wired

The old draft's trigger-source table listed event types beyond what ships. Live
event types are exactly: `entity.created`, `entity.updated`, `entity.deleted`,
`fsm.output`. Additionally, `fsm.step` and `commit.applied` are **in progress**
(specified in `EVENT_MODEL.md`, being implemented towards 1.0) — they are not yet
in the live `validEventTypes` set.

### [ROADMAP] The unwired sources

| Source | Status |
|--------|--------|
| `graph.edge.added` / `graph.edge.removed` | [ROADMAP] not wired |
| `fsm.entered` / `fsm.exited` / `fsm.terminal` | [ROADMAP] not wired — note `fsm.step` (in progress) partly subsumes the entered/exited intent, carrying `previous`/`current`; whether the granular entered/exited/terminal types are still wanted alongside `fsm.step` is a [DECISION] |
| `ts.appended` | [ROADMAP] not wired |
| `meta.expired` | [ROADMAP] not wired |

---

## 5. Actions not yet wired

Live action types are exactly: `webhook`, `oql`.

| Action | Status |
|--------|--------|
| `sulpher` (Cypher query action) | [ROADMAP] not implemented |
| `fsm.walk` (walk machines matching an OQL WHERE clause) | [ROADMAP] not implemented — the old draft's `where`/`input`/`payload` action shape is design-only |

---

## 6. Endpoints not implemented

| Endpoint | Status |
|----------|--------|
| `PUT /api/v2/event/def/{id}` (replace) | [ROADMAP] not implemented — use `PATCH` |
| the `/deadletter/*` family | [ROADMAP] see §3 |

### [ROADMAP] Error codes reserved for deferred features

The live error codes are `XOLU-EV001` (invalid event def), `XOLU-EV002` (not
found), `XOLU-EV003` (delivery failed). The following were documented in the old
draft for features that do not exist at 1.0; preserved here as the intended
numbering should the features be built:

| Code | Intended meaning | Blocked on |
|------|------------------|------------|
| `XOLU-EV004` | Template variable resolution failed | (may be folded into EV001) |
| `XOLU-EV005` | Sync action failed; operation rolled back | sync execution (§2) |
| `XOLU-EV006` | FSM-walk `where` clause failed to parse | `fsm.walk` action (§5) |
| `XOLU-EV007` | Dead-letter entry not found | dead-letter (§3) |
| `XOLU-EV008` | Replay rejected — def disabled | replay (§3) |
| `XOLU-EV009` | Test context shape mismatch | richer test validation |

---

## 6a. Type-faithful ids in delivered payloads — RESOLVED

### [DONE] Envelope ids are now native-typed (not strings)

**Resolved.** Earlier, a delivered notification carried the *same identifier* in
two JSON types: the top-level envelope serialised `id`/`entity` as **strings**
(`"id":"9100"`) while the structured `data` carried the same ids as **numbers**
(`affected[0].ref.id` → `9100`). The envelope now carries the native numeric id,
so the wire is type-consistent:

```
fsm.step:       {... "machine_id":1 ...,"id":1 ...}
commit.applied: {... "ref":{"entity":"asset","id":9100,...} ...,"id":9100 ...}
```

**Diagnosis (retained as the record of why).** The `event` struct typed `ID` as
`string` because that field fed `substituteTemplate`'s `{{event.id}}` token,
which interpolates into string-built template output. The default JSON envelope
reused that string field, so it serialised as a string. The string typing was
load-bearing **only** for template interpolation. The entity model is
unambiguous that an id is numeric (`models.Reference.ID` is `int64`,
`models.Entity.ID` is `int`); nothing declared it a string, so the string
envelope form was a deviation from the schema, not a valid alternative
representation.

**The principle adopted (general, not id-only).** Structured output carries every
value's schema-true type; **stringification is confined to text-template
interpolation**, the one context that genuinely requires a string. Concretely:

- `event.ID` is now `interface{}`, carrying the native `int64`/`int`. The
  envelope serialises it natively.
- `substituteTemplate` stringifies `ev.ID` (`%v`) **at the `{{event.id}}` token
  site only** — the sole place a string form is required.
- Floats and booleans were verified already native (they were never routed
  through the string path): an `fsm.step`'s `terminal` is JSON `true`/`false`,
  `@retries` is a number — not `"true"`/`"0"`.
- The existing `fsm.output`/`entity.*`/commit envelope tests were migrated to the
  native-typed envelope and re-verified green.

`event.Entity` remains a **string**: for entity/commit events it is the entity
*name* (`"asset"`, genuinely a string), and for fsm events it carries the machine
id as a label — but the machine's numeric id is already available natively in the
envelope `id` and in `data.machine_id`, so `entity` is kept as a stable string
label rather than a type-varying field.

This was settled deliberately (not slipped into the close-out): the nolu review
established that numeric is the canonical type, and the change was made and
verified as an explicit decision. The broader federation-consistent reference
shape (`entity_type` field naming, mappable to nolu `LocalRef`) is a separate,
deferred effort — see `NOLU_EVENTS.md`.

## 6b. event_latch_source granularity (partly resolved)

### [DONE for FSM] Element-level source for fsm.step / fsm.output

`origin.event_latch_source` now identifies the specific FSM transition that
fired the event, composed from the transition's intrinsic coordinates:

```
"event_latch_source": "fsm/AwaitingInspection:inspection_passed:InService"
```

i.e. `fsm/<from>:<input>:<to>`. A subscriber can distinguish which transition of
a machine produced an `fsm.step` or `fsm.output`, including self-loops
(`fsm/AwaitingInspection:inspection_failed:AwaitingInspection`). Composed at the
fire site from `previous`/`input`/`current`, which were already in scope — no
change to `FsmWalkResult` or the walk contract.

### [DONE] Definition as a third event-data context

FSM events (`fsm.step`, `fsm.output`) now carry the machine's definition spec as
a `definition` namespace in the event data, alongside the firing facts. A
jsonplate can reference definition facts:

```json
{
  "machine":  { "$ref": "definition.name" },
  "terminal": { "$ref": "definition.states.InService.terminal" }
}
```

resolving to the machine name, a state's terminal flag, transitions, variables,
etc. The spec travels with the machine (the prototype-snapshot model), so no DB
round-trip is needed.

**Schema-when-available policy (the absent-case mitigation).** A reference to a
definition path that is not present resolves to `null` — jsonplate's standard
null-on-missing-path behaviour. So `{"$ref":"definition.states.X.terminal"}`
yields the value when present and `null` when absent, a clear documented
degradation with no special-casing. (Verified: an absent state path renders
null.)

**Implementation note — the definition is a decoded map, which is an
independent copy.** The forwarded `definition` MUST be a `map[string]interface{}`,
not the `fsmDefinitionSpec` struct: queryfy's path engine traverses maps but not
arbitrary structs (a struct path resolves to "field not found"). The map is
decoded fresh from the machine's snapshot JSON at the walk's result-construction
site. This decode makes it an *independent copy* with no aliasing back to the
running machine's spec — so the earlier question of "share by reference vs deep
copy" is moot: the map form is necessarily a copy, and the running machine
cannot be mutated through it regardless of any future spec caching. The copy is
required for queryability, not (as first assumed) only for safety; the safety
falls out for free.

### [ROADMAP] Remaining: author-named transitions

The coordinate form is stable and unique within a machine, but transitions still
have no author-assigned *name*. If a more human/stable identifier than
`from:input:to` is wanted (e.g. `fsm/approve`), add an optional `name` field to
the transition spec and prefer it in the source when present. This is a schema
change to FSM definitions (validation, docs, migration), deferred.

### [BY DESIGN] commit.applied source is intentionally coarse

`commit.applied` carries `event_latch_source: "commit.applied"`. This is **not**
a gap: the `/commit` endpoint operates on no single FSM object, so there is no
element-level object to name. The kind-level source is the correct terminal
value for commit events, not a placeholder awaiting finer granularity.

## 7. Field-level commit deltas (from EVENT_MODEL.md §3.4)

Carried here for visibility; the authoritative statement is `EVENT_MODEL.md`.

### [ROADMAP] Opt-in field-level old→new deltas on commit events

Reporting "field X went A→B" for a non-FSM field requires a read-before-write the
commit path does not otherwise perform. Deferred and made opt-in per event def so
the cost is paid only when requested.

### [ROADMAP] Changed-REF / changed-edge reporting on commit events

The graph layer maintains edges by delete-and-recreate, so "which relationships
changed" also requires capturing prior edge state. Deferred together with §7's
field deltas as one coherent opt-in feature.
