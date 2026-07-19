# xolu /api/v2 — Experimental Platform Extensions

Version: draft  
Status: post-v1.0 experimental  
Author: haitch  
Last updated: 2026-06-11

---

## Overview

The `/api/v1` surface covers xolu's core data model: entities, graph,
timeseries, blobs, schemas, tenants, and the `/commit` endpoint for atomic
multi-entity writes. It is stable by design and changes conservatively.

`/api/v2` introduces new first-class subsystems, all post-v1.0. Every feature
introduced after the 1.0 stability commitment lives at `/api/v2` regardless
of complexity, so the version prefix is a reliable indicator of when a
feature was introduced and what stability guarantees it carries.

| Prefix | Subsystem | Purpose |
|--------|-----------|---------|
| `/api/v2/fsm/def` | FSM Definitions | Immutable executable state machine specifications |
| `/api/v2/fsm/machine` | FSM Machines | Running machine instances |
| `/api/v2/event/def` | Event Definitions | Reactive event defs wiring subsystems together |
| `/api/v2/meta` | Entity Metadata | Per-entity key/value sidecar, entity-scoped lifecycle |
| `/api/v2/gen` | Generators | Named and stateless value generators (UUID, sequence, token, timestamp, pick, etc.) |
| `/api/v2/seq` | Sequence alias | Convenience alias for `/api/v2/gen/seq` |

**Internal infrastructure (not HTTP-exposed):**

| Package | Component | Purpose |
|---------|-----------|---------|
| `pkg/gc` | `gc.Worker` / `gc.Sweeper` | Generic background sweep worker used by all GC subsystems |

These subsystems are **peers** of the existing data model — not features of
entities, not wrappers around transactions, not application-level middleware.
Each owns its own storage, its own identity space, and its own API surface.
They interact with documents, graph, timeseries, and each other through
well-defined integration points rather than by embedding themselves into those
subsystems.

---

## Path conventions: primitives, `def`, and the verb/noun distinction

### Primitives vs entities (the premise)

Two different kinds of thing live under the API, and they are **defined
differently**, which is why `def` appears on one and never on the other:

- A **primitive** (`fsm`, `event`, `ts`, `cal`, `rollup`) is a *capability the
  substrate offers*. It is a namespace you declare instances into, and you declare
  them with `def`. `fsm/def`, `cal/def`, `ts/tl/def`.
- An **entity** (`/api/v2/entity/watercloset`, an asset, a document) is *defined by
  its schema*, not by a `def` endpoint. There is no `entity/watercloset/def` — the
  schema is the definition. You create entities by writing them (via `/commit` or
  the entity surface), and their shape is governed by the schema registered for
  that entity type.

So **`def` is a primitive-namespace construct, never an entity construct.** If you
find yourself reaching for `def` on something defined by schema, it is an entity,
and it does not get one. This is the first filter; everything below applies only
*within* a primitive's namespace.

A primitive's prefix (`fsm`, `event`, `ts`, `cal`, `rollup`) is a **namespace**,
like a Go package. Its path segments are the namespace's exports: some construct
things (`def`), some enumerate (`list`), some operate (`walk`, `run`, `sync`),
some read (`stats`, `state`). The rules below keep that export surface consistent
across every primitive.

### `def` is a constructor, and `def` means *define*, not *new*

`def` is the endpoint that **brings an addressable thing into being**. It is named
`def` (not `new`) deliberately: `new` is not a verb ("new a calendar" is
ungrammatical), whereas *define* is a transitive verb with declaration semantics.
Those semantics are load-bearing, not cosmetic:

- **`def` declares; it does not allocate.** The caller supplies the id (as Lisp's
  `defun square` or Python's `def square` name the thing being defined). A `def`
  call is therefore **idempotent define-at-known-id** — re-defining the same id is
  a no-op or a replace, never a duplicate. A primitive whose constructor
  *allocates* a server-side id is misusing `def`; that is a `new`, and should be
  flagged, not accommodated as a second convention.
- **A thing exists if and only if you address it by id afterwards.** `def` yields
  an `{id}`; the `{id}` is what `def` returns. If construction yields nothing
  addressable (e.g. `provision`, which merely enables a tenant), it is **not** a
  `def`.

### The trap: `def` as verb vs `def` as noun

The same three letters get used two ways, and conflating them is the single
easiest API-shape mistake to make here (it was made repeatedly during the `cal`
design before being caught):

- **`def` as a verb** — "*to define*". The construction endpoint. `POST .../def`.
- **`def` as a noun** — "*the definition*", a persisted, addressable specification
  with its own lifecycle. `GET .../def/{id}`, `PUT .../def/{id}`.

Whether a primitive may use the **noun** form depends entirely on its population
structure:

### The deciding rule: one population or two

**Ask: does defining leave behind a separate, persisted *definition* that you
address independently of the *instances* constructed from it?**

- **Two populations → noun-`def`.** The definition is a first-class entity distinct
  from its instances. It earns `def/{id}`, `GET /def` (list the definitions),
  `def/validate`. **`fsm` and `event` are this kind**: an `fsm/def` is a prototype,
  and `fsm/machine` instances are minted from it — two populations, the def
  persists and is addressed in its own right.

  ```
  POST   /api/v2/fsm/def              construct a definition
  GET    /api/v2/fsm/def              list definitions      (def is a NOUN here)
  GET    /api/v2/fsm/def/{id}         address a definition
  PUT    /api/v2/fsm/def/{id}         replace a definition
  POST   /api/v2/fsm/machine          construct an instance from a def
  GET    /api/v2/fsm/machine/{id}     address an instance
  ```

- **One population → verb-`def`.** The thing you define *is* the thing; there is no
  separate instance tier. `def` is **only** the construction verb. Listing is
  `list` (never `GET /def`), and the constructed thing is addressed directly by
  `{id}` (never `def/{id}`). **`ts` timelines, `ts` rollups, and `cal` are this
  kind**: a timeline/rollup/calendar is a single citizen, not a definition that
  spawns instances.

  ```
  POST   .../def              construct the thing      (def is a VERB here)
  GET    .../list             list the things
  GET    .../{id}             address the thing directly  — NOT .../def/{id}
  PUT    .../{id}             replace
  DELETE .../{id}             remove
  POST   .../def/validate     validate without storing (optional)
  ```

  `rollup` is the in-tree exemplar of the verb form: `rollup/def` constructs,
  `rollup/list` lists, `rollup/{rollup_id}` addresses. `ts` timelines and `cal`
  follow it.

### Consequences and known deviations

- **`ts` timeline tier — migrated to `tl`.** Its timeline tier predated the
  path convention and used `POST /ts/timelines` (no `def`) while its own `rollup`
  tier correctly used `rollup/def`. This has now been corrected: the live routes
  are `ts/tl/def` + `ts/tl/list` + `ts/tl/{id}` (plus `PATCH` and `DELETE` on
  `{id}`), matching the convention; the legacy `timelines` routes are retired
  (commented out in `server.go`). Historical references to `ts/timelines` describe
  the deprecated surface, not the current one.
- **`cal` is specified to the verb form** (`cal/def`, `cal/list`,
  `cal/{calendar_id}`) — see `docs/proposals/cal-rest-api.md`.
- **Reserved ids.** When a thing is addressed directly under its namespace
  (`cal/{calendar_id}`, `ts/tl/{timeline_id}`), the id must not collide with
  sibling segments (`def`, `list`, and any primitive-level verb such as `match`).
  Those words are reserved as ids — the same situation `gen` has with its type
  names.

---

## Why FSMs as a first-class subsystem

> For the conceptual model, the three-level determinism declaration, the guard
> exclusivity recognizer, and guard/set expression semantics, see
> [FSM.md](FSM.md). This section and those below are the HTTP endpoint reference.

### The dropdown problem

Every data modelling tool — Airtable, Notion, every ORM, every database schema
designer — offers the same primitive for fields with a constrained set of
values: an enum, a dropdown, a string column with a CHECK constraint. The user
declares the possible states. Thinking stops there.

The consequence is universal and well-known: the transitions between those
states, the guards that determine whether a transition is legal, and the side
effects that accompany a transition all live in application code. There is no
single place that says "an asset may only transition from `AwaitingInspection`
to `InService` if an inspection result is present and the technician is
authorised." That rule is scattered across service methods, controllers,
webhooks, and migration scripts, expressed in whatever language the application
is written in, invisible to the data layer, and bypassable by anything that
writes to the field directly.

xolu's FSM subsystem moves the state machine from the application layer to the
persistence layer. A machine definition is an executable specification.
Transition legality is enforced at write time. Invalid state changes are
rejected at the same layer that rejects invalid field types. The state machine
exists as a structural guarantee, not as a convention enforced by discipline.

### Three distinct usage patterns

Not every state change is a complex business transaction. The FSM subsystem
recognises three patterns and supports all three without forcing any of them
into the shape of the others.

**Pure state transition.** The machine walks. No document is involved. No
payload is required beyond the input. A service transitions from `Enabled` to
`Disabled`. A background job moves from `Running` to `Completed`. The FSM owns
the operation entirely.

**Data plus state transition.** An inspection result is recorded and the asset
lifecycle advances simultaneously. The payload is part of the transition: if
the document write fails, the state does not advance; if the state transition
is illegal, the document is not written. Both must succeed or neither does.
This pattern participates in `/api/v1/commit`.

**Data mutation with no state transition.** An asset's serial number is
corrected. The machine remains `InService`. The document changes, the FSM is
not consulted. This is a normal `/api/v1` write.

### FSMs as infrastructure primitives

First-class FSMs are useful beyond user-facing business objects. xolu itself
has informal state machines running internally: blob GC cycles, tenant
provisioning sequences, background job lifecycles, import pipeline stages.
When FSMs are a peer subsystem rather than an application feature, they are
available to model any stateful process — not just business documents.

---

## /api/v2/fsm/def — Machine Definitions

A **definition** is a prototype — a template that describes the default
behaviour of a class of machines. It declares the state space, transitions,
default guard expressions, default machine variables, and default outputs.

Definitions behave like prototypes, not classes. When a machine is created
from a definition, it takes a snapshot of the definition's properties at
that moment. The machine owns its own copy of those properties and can
diverge from the definition independently. Changing a definition has no
effect on existing machines; it only affects machines created after the
change. Deleting a definition does not affect machines already derived
from it — those machines are self-contained.

This model is deliberately permissive. Per-instance behavioural variation
is supported by design: two machines derived from the same definition may
have different guard expressions, different variable defaults, different
output mappings. Governance of which machines may deviate from definition
defaults is a future ACL concern, not a structural constraint.

Definitions are validated at creation time using `fsm-toolkit` (v0.9.6+).
Structural guarantees provided at creation time:

- All states declared in transitions exist in the state list
- Every non-terminal state has at least one path to a terminal state
- No transition is ambiguous (same `from` + `input`, multiple `to`)
- All default guard expressions parse successfully

No structurally invalid definition is ever stored.

### Guard expressions

Guards in a transition definition are default T-SQL expression fragments,
evaluated by the tsqlruntime expression evaluator against the machine's
own `_machine_vars_` and the walk payload at runtime. They are stored with
the machine (copied from the definition at creation time), not with the
definition. The machine's copy is what the walk runtime reads.

This means:

- A machine's guard expressions can be updated via
  `PATCH /api/v2/fsm/machine/{id}` without affecting any other machine
  or the definition
- Two machines derived from the same definition can have different guards
- If the definition's default guard is changed, existing machines are
  unaffected; only new machines take the updated default

OQL WHERE clause filtering — selecting which machines to walk — is a
separate, caller-side concern specified at walk time in the `/walk` request
body or the event system's `fsm.walk` `where` field. It is never stored
in the definition or the machine. The transition `guard` field is always
a T-SQL expression over machine-local data only.

### Machine variables

Variable declarations in the definition are defaults. The machine takes
a snapshot at creation time and owns its own variable state independently.
Variable declarations can be overridden at machine creation via the
`overrides.variables` block.

### Endpoints

```
POST   /api/v2/fsm/def              Create a new definition (prototype)
GET    /api/v2/fsm/def              List definitions
GET    /api/v2/fsm/def/{id}         Retrieve a definition
PUT    /api/v2/fsm/def/{id}         Replace a definition (affects future machines only)
DELETE /api/v2/fsm/def/{id}         Delete a definition (always permitted; existing machines unaffected)
POST   /api/v2/fsm/def/validate     Validate without storing
```

### Creating a definition

```http
POST /api/v2/fsm/def
Content-Type: application/json

{
  "name":        "AssetLifecycle",
  "description": "Lifecycle of a physical asset from provisioning to decommission",
  "initial":     "Provisioning",
  "states": {
    "Provisioning":       { "terminal": false },
    "AwaitingInspection": { "terminal": false },
    "InService":          { "terminal": false },
    "Suspended":          { "terminal": false },
    "Decommissioned":     { "terminal": true  }
  },
  "variables": {
    "@retries": { "type": "int", "default": 0 }
  },
  "transitions": [
    {
      "from":  "Provisioning",
      "input": "ready_for_inspection",
      "to":    "AwaitingInspection",
      "set":   { "@retries": "0" }
    },
    {
      "from":   "AwaitingInspection",
      "input":  "inspection_passed",
      "to":     "InService",
      "guard":  "payload.result = 'pass' AND payload.technician != ''",
      "output": "asset_activated",
      "set":    { "@retries": "0" }
    },
    {
      "from":  "AwaitingInspection",
      "input": "inspection_failed",
      "to":    "AwaitingInspection",
      "guard": "@retries < 3",
      "set":   { "@retries": "@retries + 1" }
    },
    {
      "from":  "AwaitingInspection",
      "input": "inspection_abandoned",
      "to":    "Provisioning",
      "guard": "@retries >= 3"
    },
    {
      "from":  "InService",
      "input": "suspend",
      "to":    "Suspended"
    },
    {
      "from":  "Suspended",
      "input": "reinstate",
      "to":    "InService"
    },
    {
      "from":   ["InService", "Suspended"],
      "input":  "decommission",
      "to":     "Decommissioned",
      "output": "asset_decommissioned"
    }
  ],
  "output_alphabet": ["asset_activated", "asset_decommissioned"],
  "gc": {
    "stalled_after": "7d",
    "dead_after":    "30d",
    "on_gc_collect": "decommission"
  }
}
```

Response on success:

```json
{
  "id":         42,
  "name":       "AssetLifecycle",
  "created_at": "2026-06-11T14:00:00Z",
  "analysis": {
    "reachable":       true,
    "deterministic":   true,
    "terminal_states": ["Decommissioned"],
    "cycles":          ["InService → Suspended → InService"]
  }
}
```

### Lifecycle constraint

Every definition must have at least one terminal state reachable from every
non-terminal state. This is a hard validation rule. A definition with
unreachable terminal states is rejected at creation time regardless of
whether it is `PUT` or `POST`.

### Output alphabet

`output_alphabet` is required in every definition that declares one or more
transition `output` fields. Any transition output not listed in
`output_alphabet` causes creation to fail with `XOLU-FSM006`. Definitions
with no transition `output` fields omit `output_alphabet` entirely; an
empty array is also accepted but unnecessary.

If a walk fires an output for which no matching `fsm.output` event def
exists at walk time, the walk proceeds normally. The output is recorded in
the history entry. No error is raised. Event defs are decoupled from
definitions — the definition author does not need to know what event defs
exist.

### Updating a definition

`PUT /api/v2/fsm/def/{id}` replaces the definition in full. The change
affects only machines created after the update. All existing machines
retain the snapshot they took at their own creation time and are
unaffected.

### Deleting a definition

`DELETE /api/v2/fsm/def/{id}` is always permitted. Existing machines
derived from this definition are self-contained and continue to operate
normally. The definition ID is recorded in each machine's metadata; after
deletion the definition field in machine responses returns the ID with a
`"deleted": true` flag.

### Bundle composition

Definitions may reference child definitions via `linked_states`. A linked
state delegates to a named child definition when entered; the child runs
until it reaches a terminal state and returns `accept` or `reject` to
the parent. Circular links are rejected at validation time.

```json
{
  "linked_states": {
    "validating": 17
  }
}
```

`linked_states` values are definition **IDs**, not names. IDs are
guaranteed unique; names are not. Using IDs ensures the correct definition
is snapshotted at machine creation time regardless of subsequent definition
renames or new definitions sharing the same name.

Child definitions are resolved by ID at machine creation time — the
machine takes a snapshot of the child definition alongside the parent.
If the child definition does not exist at machine creation time,
`XOLU-FSM012` is returned.

### Validating without storing

```http
POST /api/v2/fsm/def/validate
Content-Type: application/json

{ ...same body as creation... }
```

Returns the analysis block if valid, or an `errors` array if not.
Nothing is stored.


## /api/v2/fsm/machine — Running Machines

A **machine** is a running instance of a definition. It holds current state,
variable values, and transition history. The word "machine" is chosen over
"instance" deliberately: when you walk a machine, query its state, or read
its history, you are interacting with a running machine, not an instantiated
data structure.

### Lifecycle responsibility

The system that creates a machine is responsible for its lifecycle. It is
expected to walk the machine to a terminal state and delete it when the
business context ends. xolu does not enforce terminal-state arrival before
deletion — `DELETE /api/v2/fsm/machine/{id}` is always available — but the
history is preserved or discarded according to the configured retention policy.

The FSM GC (see `pkg/gc`) can be configured to collect stalled and dead
machines automatically as a safety net for machines whose owning system
has crashed or been decommissioned.

### Endpoints

```
POST   /api/v2/fsm/machine                    Create a machine from a definition
GET    /api/v2/fsm/machine                    List machines (filterable by definition, state, ref)
GET    /api/v2/fsm/machine/{id}               Retrieve a machine
PATCH  /api/v2/fsm/machine/{id}               Update machine-local guard expressions or variable defaults
DELETE /api/v2/fsm/machine/{id}               Delete a machine

POST   /api/v2/fsm/machine/{id}/walk          Execute a transition
GET    /api/v2/fsm/machine/{id}/state         Current state
GET    /api/v2/fsm/machine/{id}/result        Final result: terminal status, final state, vars, and output
GET    /api/v2/fsm/machine/{id}/history       Full transition history
GET    /api/v2/fsm/machine/{id}/transitions   Available transitions from current state
GET    /api/v2/fsm/machine/{id}/vars          Current variable values
```

### Creating a machine

**Unbound (no entity association):**

```http
POST /api/v2/fsm/machine
Content-Type: application/json

{ "definition": 42 }
```

**Bound to an existing entity:**

```http
POST /api/v2/fsm/machine
Content-Type: application/json

{ "definition": 42, "ref": "asset:123" }
```

**Bound to a new entity created inline (atomic):**

```http
POST /api/v2/fsm/machine
Content-Type: application/json

{
  "definition": 42,
  "entity": {
    "type": "asset",
    "data": { "name": "Pump A", "serial": "SN-4471" }
  }
}
```

When `entity` is provided inline, the entity creation and machine creation
are atomic. A single entity may participate in multiple machines simultaneously.
The binding is on the machine, not the entity document.

**With overrides (prototype deviation):**

```http
POST /api/v2/fsm/machine
Content-Type: application/json

{
  "definition": 42,
  "ref": "asset:123",
  "overrides": {
    "variables": {
      "@retries": { "default": 5 }
    },
    "transitions": {
      "inspection_failed": {
        "guard": "@retries < 5"
      }
    }
  }
}
```

The `overrides` block deviates from the definition's defaults at creation
time. The machine takes the definition's full snapshot and applies the
overrides on top. Only the specified fields are overridden; everything
else is taken from the definition unchanged. The definition itself is
not modified.

Response:

```json
{
  "id":              1001,
  "definition":      42,
  "definition_name": "AssetLifecycle",
  "definition_deleted": false,
  "state":           "Provisioning",
  "ref":             "asset:123",
  "vars":            { "@retries": 0 },
  "created_at":      "2026-06-11T14:05:00Z"
}
```

`definition_deleted` is `true` if the source definition has been deleted
since this machine was created. The machine continues to operate normally
using its own snapshot.

### Updating a machine

`PATCH /api/v2/fsm/machine/{id}` updates the machine's own guard
expressions or variable defaults without affecting the definition or
any other machine.

```http
PATCH /api/v2/fsm/machine/1001
Content-Type: application/json

{
  "overrides": {
    "variables": {
      "@retries": { "default": 5 }
    },
    "transitions": {
      "inspection_failed": {
        "guard": "@retries < 5"
      }
    }
  }
}
```

Only the fields specified in `overrides` are changed. The machine's
current state, history, and variable values are not affected. Any patch
must result in a fully valid machine — the complete post-patch snapshot
is validated as a unit before the change is committed. State reachability,
transition determinism, guard expression syntax, and output alphabet
consistency are all checked, exactly as a new definition is validated at
creation time. If the result would be invalid for any reason, the patch
is rejected with `XOLU-FSM006` and the machine is unchanged.

### Walking a machine

```http
POST /api/v2/fsm/machine/{id}/walk
Content-Type: application/json

{
  "input":   "inspection_passed",
  "payload": { "result": "pass", "technician": "alice" }
}
```

The runtime:

1. Evaluates the OQL guard against `_api_v2_machine_` — the machine must
   satisfy the WHERE clause to proceed
2. Collects every transition matching `(current state, input)` in definition
   order. Where several transitions share the same `(state, input)`, they are
   disambiguated by their T-SQL `guard` expressions: the runtime evaluates the
   guards in order and fires the first whose guard passes (a transition with
   no guard always passes). This is how a state validates input by routing to
   different targets — e.g. an accept edge guarded `field IS NOT NULL AND
   field = expected`, a reject-invalid edge, and a reject-missing edge guarded
   `field IS NULL`, all on one input.
3. Advances state
4. Evaluates `set` expressions and updates `_machine_vars_`. Set expressions
   may read both machine variables (`@var`) and the walk payload
   (`payload.field`), so a transition can capture incoming data into a
   variable (e.g. `set: { "@expected": "payload.len" }`).
5. Evaluates any `NEXT VALUE FOR` sequence references atomically
6. Records the history entry (including payload and variable snapshot)
7. Fires any Mealy `output`, matched against registered event defs
8. Returns the result

All steps from 3 onwards are committed atomically. If any step fails, nothing
is written. If transitions exist for the input but every guard evaluates
false, the walk is rejected with `XOLU-FSM004`; if no transition matches the
input at all, it is rejected with `XOLU-FSM003`.

Guard expressions are T-SQL boolean expressions. Equality uses `=` (e.g.
`@received = @expected`); presence is tested with `IS NULL` / `IS NOT NULL`,
which is the reliable way to reject a missing payload field rather than
relying on a comparison against an absent value.

Response on success:

```json
{
  "previous":   "AwaitingInspection",
  "current":    "InService",
  "terminal":   false,
  "outputs":    ["asset_activated"],
  "vars":       { "@retries": 0 },
  "history_id": 8
}
```

Response on guard failure (`422`):

```json
{
  "error":   "XOLU-FSM004",
  "message": "transition guard rejected",
  "detail":  "guard: @retries < 3 evaluated false (current: 3)",
  "state":   "AwaitingInspection"
}
```

Response on invalid input for current state (`409`):

```json
{
  "error":             "XOLU-FSM003",
  "message":           "no transition for input \"inspection_passed\" from state \"InService\"",
  "state":             "InService",
  "available_inputs":  ["suspend", "decommission"]
}
```

A walk on a machine in a terminal state always returns `409`. There are no
`/start` or `/stop` endpoints; the machine's own terminal states define when
it is done.

### Bulk walk via OQL

To walk multiple machines matching a condition in one call, use the `@FSM()`
OQL function against `_api_v2_machine_`. The WHERE clause is the guard:

```sql
SELECT @FSM('inspection_passed', '{"result":"pass","technician":"alice"}')
FROM _api_v2_machine_
WHERE definition = 42
  AND state = 'AwaitingInspection'
  AND ref IN (SELECT 'asset:' || id FROM asset WHERE region = 'north')
```

`@FSM()` returns a result set of `{machine_id, previous, current, success, error}`.
This is the mechanism used internally by the event system's `fsm.walk` action.

### Available transitions

```http
GET /api/v2/fsm/machine/{id}/transitions
```

```json
{
  "state":  "InService",
  "inputs": ["suspend", "decommission"]
}
```

Returns only the inputs for which all guard conditions could potentially be
satisfied from the current state. Guards referencing external data are not
pre-evaluated; those inputs are included if the transition exists.

### Variables

```http
GET /api/v2/fsm/machine/{id}/vars
```

```json
{
  "@retries":      { "value": 2, "type": "int", "default": 0 },
  "@case_number":  { "value": 1042, "type": "int", "default": 0 }
}
```

Returns the machine's current variable values alongside the declared type
and default from the machine's snapshot. The `value` field is the same
value that appears in the `vars` map of walk responses and history entries.
The richer shape here — with `type` and `default` — is available on this
endpoint only; walk responses and history entries return the compact
`{ "@name": value }` map for brevity.

### History

```http
GET /api/v2/fsm/machine/{id}/history
```

```json
{
  "machine": 1001,
  "entries": [
    {
      "id":      1,
      "from":    null,
      "to":      "Provisioning",
      "input":   null,
      "payload": null,
      "vars":    { "@retries": 0 },
      "at":      "2026-06-11T14:05:00Z",
      "note":    "machine created"
    },
    {
      "id":      2,
      "from":    "Provisioning",
      "to":      "AwaitingInspection",
      "input":   "ready_for_inspection",
      "payload": null,
      "vars":    { "@retries": 0 },
      "at":      "2026-06-11T14:08:00Z"
    },
    {
      "id":      3,
      "from":    "AwaitingInspection",
      "to":      "InService",
      "input":   "inspection_passed",
      "payload": { "result": "pass", "technician": "alice" },
      "vars":    { "@retries": 0 },
      "outputs": ["asset_activated"],
      "at":      "2026-06-11T14:12:00Z"
    }
  ]
}
```

History is append-only and immutable. Variable snapshots are recorded at each
transition so the full variable evolution is auditable.

### Integration with /api/v1/commit

When a state transition must be atomic with a document write, the walk
participates in `/api/v1/commit` via the `fsm_walk` field:

```http
POST /api/v1/commit
Content-Type: application/json

{
  "update": {
    "entity":  "asset",
    "id":      123,
    "version": 7,
    "data": {
      "last_inspection": {
        "result":     "pass",
        "technician": "alice",
        "at":         "2026-06-11T14:12:00Z"
      }
    }
  },
  "fsm_walk": {
    "machine": 1001,
    "input":   "inspection_passed",
    "payload": { "result": "pass", "technician": "alice" }
  }
}
```

The document write, state advance, variable updates, and any sequence
increments are all committed in the same SQLite transaction. If any fails,
none happen. Walking via `/walk` and separately committing a document is not
atomic; use this path when atomicity is required.

---

## /api/v2/event/def — Event Definitions

The event subsystem is the connective tissue between xolu's subsystems. An
**event def** is a registered, stored, inspectable definition: when an event of
a given type occurs, deliver a notification to a target. The reactive pipeline
is visible to xolu rather than hidden in application code.

This section describes the **shipped 1.0 surface only**. Design material that is
not yet implemented — synchronous execution, retry, dead-letter/replay, the
unwired event types and actions, and the older nested request shape — has been
moved to `EVENT_PENDING.md` and is not described here as though it were live.
The authoritative description of the event model and payloads is
`EVENT_MODEL.md`.

### Nomenclature

This subsystem uses the vocabulary fixed in `EVENT_MODEL.md`. Briefly:

- **event type** — a kind of occurrence (`fsm.output`, `entity.created`, …).
- **event def** — a standing definition, created once via `POST /event/def`,
  consistent with `/fsm/def` and `/rollup/def`.
- **event firing** — the process launched when an event def matches an eventable
  action; recorded in the delivery log. Many firings per def.
- **event notification** — the message a firing delivers to its target.

The terms "subscription" and "trigger" are not used.

### Wire contract

An event def is a flat object:

```json
{
  "event_type":  "fsm.output",
  "action_type": "webhook",
  "config":      { "url": "https://operations.example.com/hooks/asset-activated" },
  "execution":   "async"
}
```

- `event_type` — one of the live event types (see below).
- `action_type` — one of the live action types (`webhook`, `oql`).
- `config` — an opaque per-action configuration object (e.g. the webhook URL, or
  the OQL query). Action-type specific.
- `execution` — `"async"`. `"sync"` is accepted and stored but always runs async
  in 1.0 (see Execution below).

There is no `name`, `trigger`, nested `action`, `source`, top-level `filter`, or
`retry` field in the 1.0 contract.

### Endpoints

```
POST   /api/v2/event/def              Create an event def
GET    /api/v2/event/def              List event defs
GET    /api/v2/event/def/{id}         Retrieve an event def
PATCH  /api/v2/event/def/{id}         Update fields
DELETE /api/v2/event/def/{id}         Delete an event def

GET    /api/v2/event/def/{id}/log     Recent delivery log (last N firings)
POST   /api/v2/event/def/{id}/test    Synthetic test firing
```

`PUT` (replace) is not implemented; use `PATCH`. The `test` endpoint
synchronously runs the def against a caller-supplied event payload and returns
the per-action outcome — it is the one place delivery is synchronous, by design,
for testing.

### Live event types

| Event type | Description |
|------------|-------------|
| `entity.created` | An entity of a given type was created |
| `entity.updated` | An entity was updated |
| `entity.deleted` | An entity was deleted |
| `fsm.output` | A Mealy machine produced a named output on a transition |

Two further event types are specified in `EVENT_MODEL.md` and being implemented
towards 1.0 — `fsm.step` (one firing per committed state transition, carrying
`previous`/`current`/`terminal`/`vars`) and `commit.applied` (one firing per
successful commit, carrying affected-entity REFs, `created`/`version`, and a copy
of the committed request). Event types beyond these — `graph.edge.*`,
`fsm.entered/exited/terminal`, `ts.appended`, `meta.expired` — are not wired; see
`EVENT_PENDING.md`.

`fsm.output` is the path through which Mealy transition outputs become firings.
When a machine produces output `"asset_activated"`, any event def with
`event_type: "fsm.output"` filtering on that output fires. This decouples the
definition author from the event-def author: the machine definition declares
outputs, event defs react to them independently. If no matching event def exists,
the walk still proceeds and the output is recorded in the machine history
regardless.

### Live action types

| Action type | Description |
|-------------|-------------|
| `webhook` | HTTP POST to an external URL (2xx = delivered) |
| `oql` | Execute an OQL query against xolu |

`sulpher` and `fsm.walk` actions are not implemented; see `EVENT_PENDING.md`.

### Execution

Execution is **always asynchronous** in 1.0. A firing is dispatched *after* the
originating transaction commits, from the handler, never inside the transaction.
A failed notification does not fail the originating operation, and cannot roll it
back.

A def may declare `"execution": "sync"`; it is accepted and stored, but 1.0
always runs async and the create/update response carries `X-Executed-As: async`.
True synchronous (in-transaction, roll-back-on-failure) execution is deferred —
see `EVENT_PENDING.md`.

### Delivery semantics

Delivery is **at-most-once, single-attempt**: a firing attempts its notification
delivery once. There is no retry, no dead-letter, and no replay in 1.0. There is
a brief window between commit and delivery in which a process crash loses the
firing; 1.0 is best-effort. Every attempt writes one `event_delivery_log` row, so
a failed or dropped delivery is observable after the fact via
`GET /event/def/{id}/log`.

Events arising from a single request (e.g. a `/commit` that both writes an entity
and runs an output-producing walk) are **unordered** in 1.0; a subscriber must
not assume one arrives before another.

> Note: at-most-once single-attempt delivery is insufficient for critical-entity
> backup defs without a reconciliation sweep. See `EVENT_PENDING.md`.

### Delivered payload envelope

Every webhook notification is delivered as `{"origin": {...}, "message": {...}}`.
`origin` is stamped by xolu on every delivery (provenance: `agent`,
`agent_version`, `event_def_id`, `event_latch_kind`, `event_latch_source`,
`fired_at`) and cannot be altered by the def. `message` is what the def's body
produces. Ids and scalars carry their native JSON type (ids are numbers, not
strings); stringification occurs only inside `{{...}}` template interpolation.
FSM events additionally expose a `definition` namespace (the machine spec) in the
event data. The authoritative description is `EVENT_MODEL.md` §5.

### Shaping the message: jsonplate

A def's webhook `config` may carry a **jsonplate** — a JSON template whose
`{"$ref": "path"}` leaves resolve against the event data via queryfy paths
(`affected[0].ref.id`, `definition.name`, `vars.retries`); literals pass through,
absent paths render `null`. This is the preferred way to shape a structured
payload and is what lets a webhook receive well-formed, typed JSON (e.g. the
`affected` REF set of a `commit.applied`). The `{{...}}` body-string form below
remains supported for simple cases and coexists with jsonplate. See
`jsonplate.md` for the full reference.

### Template variables

`webhook` URLs/bodies and `oql` queries may contain template tokens substituted
at dispatch time:

- `{{event.type}}`, `{{event.entity}}`, `{{event.id}}`
- `{{event.data.<key>}}` — type-specific payload detail
- `{{gen:<name>}}` — calls the named generator (stateful), or a stateless
  generator (`{{gen:uuid_v4}}`, `{{gen:uuid_v7}}`, `{{gen:cuid}}`, `{{gen:ulid}}`)

In `oql` actions, template values are bound as typed parameters, not concatenated
into the query string, preventing injection.

### Examples

**Webhook on FSM output:**

```http
POST /api/v2/event/def
Content-Type: application/json

{
  "event_type":  "fsm.output",
  "action_type": "webhook",
  "config": {
    "url":        "https://operations.example.com/hooks/asset-activated",
    "filter":     { "output": "asset_activated" }
  },
  "execution": "async"
}
```

**OQL on entity delete:**

```http
POST /api/v2/event/def
Content-Type: application/json

{
  "event_type":  "entity.deleted",
  "action_type": "oql",
  "config": {
    "filter": { "entity": "asset" },
    "query":  "DELETE FROM inspection WHERE asset_id = {{event.id}}"
  },
  "execution": "async"
}
```

(The exact `config` sub-fields are action-type specific; the filter and query
live inside `config`, not as top-level request fields.)

### Delivery log

```http
GET /api/v2/event/def/{id}/log?limit=50
```

Returns recent firings for the def, each with its event type, status
(`delivered` / `failed`), detail, and timestamp.

### Error codes

| Code | Meaning |
|------|---------|
| `XOLU-EV001` | Invalid event def (bad `event_type`, `action_type`, or config) |
| `XOLU-EV002` | Event def not found |
| `XOLU-EV003` | Delivery failed — recorded in the log, not surfaced synchronously (dispatch is async) |

---

## The complete composition

All subsystems compose through shared identity — entity refs, definition IDs,
machine IDs, sequence names — without any subsystem owning another.

A complete reactive pipeline expressed entirely through xolu definitions:

```
POST /api/v1/commit  (document update + FSM walk, atomic)
  └── fsm_walk: machine 1001, input "inspection_passed"
        └── guard evaluated: payload.result = 'pass' AND @retries < 3
        └── state advances: AwaitingInspection → InService
        └── set: @retries = 0
        └── sequence: @case_number = NEXT VALUE FOR case_seq  (atomic, via /api/v2/gen/seq)
        └── Mealy output fires: "asset_activated"
              └── event def "asset-activated-notify"
                    └── webhook: POST https://operations.example.com/hooks/...
              └── event def "cleanup-stale-inspections"
                    └── oql: DELETE FROM pending_inspection WHERE asset_ref = 'asset:123'  (sync deferred; see EVENT_PENDING.md)
              └── event def "region-check"
                    └── (fsm.walk action deferred; see EVENT_PENDING.md)
```

Each step is a registered definition. Each link is explicit and auditable
through `/api/v2/event/def/{id}/log` and `/api/v2/fsm/machine/{id}/history`.

---

## Tenancy

All `/api/v2` resources are tenant-scoped. Definitions, machines, event
defs, metadata, and sequences registered under tenant A are not
visible to tenant B. The tenant-prefixed URL pattern from `/api/v1` applies:

```
POST /api/v2/tenant/{tenant_id}/fsm/def
POST /api/v2/tenant/{tenant_id}/fsm/machine
POST /api/v2/tenant/{tenant_id}/event
POST /api/v2/tenant/{tenant_id}/meta/{entity}/{id}/{key}
POST /api/v2/tenant/{tenant_id}/gen/{type}
POST /api/v2/tenant/{tenant_id}/seq
```

Unscoped paths operate under tenant 0, consistent with the `/api/v1`
convention.

---

## /api/v2/meta — Entity Metadata

A per-entity key/value sidecar for values that belong to an entity by
identity but not by business model: UI state, computed derived values,
temporary annotations, per-entity feature flags, integration markers,
and time-bounded reminders.

Metadata entries do not participate in schema validation, FTS indexing, graph
edge extraction, or adapted table columns. They are automatically deleted when
the parent entity is deleted (same transaction). They are not a general
key/value store — for pub/sub or session storage, use slabbis or any
Redis-compatible server.

### TTL and the reminder pattern

Individual metadata entries may carry an optional `expires_at` timestamp.
Expired entries are collected by the `meta.GCSweeper` on its sweep interval
and fire a `meta.expired` event trigger on deletion. This composes with the
event def system to produce scheduled notifications without a
separate scheduler:

```
PUT /api/v2/meta/asset/123/remind_technician
{ "value": "Inspection overdue", "expires_at": "2026-07-01T09:00:00Z" }

POST /api/v2/event/def
{
  "trigger": { "source": "meta.expired",
               "filter": { "entity": "asset", "key": "remind_technician" } },
  "action":  { "type": "webhook", "url": "https://ops.example.com/reminders" },
  "execution": "async"
}
```

When the GC sweep collects the entry, the matching event def fires with event
context `{ entity, id, key, value, expired_at }`. The entity itself is
unaffected — only the metadata entry is deleted. Multiple entries on the same
entity may have independent expiry times.

Entries without `expires_at` never expire via the GC — they persist until
explicitly deleted or until the entity is deleted.

### Endpoints

```
GET    /api/v2/meta/{entity}/{id}            List all metadata for an entity
GET    /api/v2/meta/{entity}/{id}/{key}      Get a value
PUT    /api/v2/meta/{entity}/{id}/{key}      Set a value (body includes optional expires_at)
DELETE /api/v2/meta/{entity}/{id}/{key}      Delete a value
DELETE /api/v2/meta/{entity}/{id}            Delete all metadata for an entity
```

Values are any JSON-serialisable type up to `XOLU_META_MAX_VALUE_BYTES`
(default 64 KB). Larger values should use the blob store.

### Storage

```sql
CREATE TABLE entity_meta (
    tenant_id  INTEGER NOT NULL DEFAULT 0,
    entity     TEXT    NOT NULL,
    id         INTEGER NOT NULL,
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    expires_at TIMESTAMP NULL DEFAULT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, entity, id, key)
);
CREATE INDEX idx_entity_meta_entity ON entity_meta(tenant_id, entity, id);
CREATE INDEX idx_entity_meta_expires ON entity_meta(expires_at)
    WHERE expires_at IS NOT NULL;
```

The partial index on `expires_at` makes the GC sweep (`DELETE FROM
entity_meta WHERE expires_at IS NOT NULL AND expires_at < ?`) efficient
regardless of how many entries have no expiry.

### PUT request body

```http
PUT /api/v2/meta/asset/123/remind_technician
Content-Type: application/json

{
  "value":      "Inspection overdue",
  "expires_at": "2026-07-01T09:00:00Z"
}
```

`expires_at` is optional. Omitting it (or setting it to `null`) stores the
entry with no expiry. Setting it on an existing entry that previously had no
expiry enables expiry; clearing it (by sending `null`) removes the expiry.
The `value` field is required. `XOLU-META005` is returned if `expires_at` is
not a valid RFC3339 timestamp.

### GC configuration

```
XOLU_META_GC_INTERVAL_SECS   — how often the sweep runs (default 300)
XOLU_META_GC_ENABLED         — enable/disable the sweeper (default true when v2 enabled)
```

The `meta.GCSweeper` is registered in `pkg/gc` alongside `blob.GCSweeper`
and `timeseries.RetentionSweeper` and is visible at `GET /api/v1/admin/gc`.

### Suggested uses

| Key pattern | Value | Purpose |
|-------------|-------|---------|
| `ui_state` | object | Panel state, column widths |
| `last_viewed_by` | string | Application-layer annotation |
| `computed_score` | number | Derived value, should not trigger re-validation |
| `integration_{system}` | object | External system reference IDs |
| `flag_{name}` | boolean | Per-entity feature flags |
| `annotation` | string | Free-text notes outside the document |
| `remind_{reason}` | string/object | Scheduled reminder — combine with `expires_at` and a `meta.expired` event def (deferred — see EVENT_PENDING.md) |
```

### Suggested uses

| Key pattern | Value | Purpose |
|-------------|-------|---------|
| `ui_state` | object | Panel state, column widths |
| `last_viewed_by` | string | Application-layer annotation |
| `computed_score` | number | Derived value, should not trigger re-validation |
| `integration_{system}` | object | External system reference IDs |
| `flag_{name}` | boolean | Per-entity feature flags |
| `annotation` | string | Free-text notes outside the document |

---

## /api/v2/gen — Generators

The generator subsystem provides named, tenant-scoped value generation
primitives. Generators produce values on demand with no input — they are
distinct from transforms (hashing, encoding) which derive a value from
an existing input.

Generators fall into two categories:

**Stateless generators** require no definition and no name. Each call
produces a fresh independent value. They are always available as OQL
functions.

**Stateful generators** require a named definition that declares their
configuration. The definition is registered once and called by name.

`/api/v2/seq` is a convenience alias for `/api/v2/gen/seq`. All other
generator types are accessible only under `/api/v2/gen`.

### Generator types

| Type | Category | Description | OQL function |
|------|----------|-------------|--------------|
| `sequence` | Stateful | Named monotonic integer counter, atomic increment | `NEXT VALUE FOR name`, `@SEQ('name')` |
| `uuid_v4` | Stateless | Random UUID, opaque, non-guessable, no coordination | `UUID_V4()` |
| `uuid_v7` | Stateless | Time-ordered UUID, index-friendly, monotonic | `UUID_V7()` |
| `token` | Stateful | Cryptographically secure random bytes, configurable length and encoding | `@GEN('name')` |
| `timestamp` | Stateful | Current time, IANA timezone-aware, configurable format and precision | `@GEN('name')` |
| `random_int` | Stateful | Random integer within a declared range | `@GEN('name')` |
| `pick` | Stateful | Random element from a declared set | `@GEN('name')` |
| `nanoid` | Stateful | URL-safe random string, configurable alphabet and length | `@GEN('name')` |
| `cuid` | Stateless | Collision-resistant, roughly time-ordered, URL-safe string | `CUID()` |
| `ulid` | Stateless | Universally unique, lexicographically sortable | `ULID()` |
| `slug` | Stateful | Human-readable random words from a configurable vocabulary | `@GEN('name')` |

### Endpoints

```
-- Stateful generator definitions
POST   /api/v2/gen/{type}              Define a named generator
GET    /api/v2/gen/{type}              List generators of a type
GET    /api/v2/gen/{type}/{name}       Retrieve a definition
DELETE /api/v2/gen/{type}/{name}       Delete a definition

-- Invocation (stateful)
GET    /api/v2/gen/{type}/{name}/next  Generate next value
GET    /api/v2/gen/{type}/{name}       Definition metadata (not a value)

-- Invocation (stateless)
GET    /api/v2/gen/uuid_v4             Generate a UUID v4
GET    /api/v2/gen/uuid_v7             Generate a UUID v7
GET    /api/v2/gen/cuid                Generate a CUID
GET    /api/v2/gen/ulid                Generate a ULID

-- Sequence alias
/api/v2/seq → /api/v2/gen/seq         (all paths mirrored)
```

---

### Stateless generators

No definition required. Call directly.

**UUID v4**

```http
GET /api/v2/gen/uuid_v4
```
```json
{ "value": "f47ac10b-58cc-4372-a567-0e02b2c3d479" }
```

Random 128-bit UUID. Opaque, non-guessable, no coordination between callers.
The dominant pattern for entity IDs in REST APIs and distributed systems.
OQL: `UUID_V4()`.

**UUID v7**

```http
GET /api/v2/gen/uuid_v7
```
```json
{ "value": "018f5e3a-1b2c-7d4e-9f0a-1b2c3d4e5f6a", "timestamp_ms": 1749686461234 }
```

Time-ordered UUID. Monotonically increasing, index-friendly (no B-tree
fragmentation), carries embedded millisecond timestamp. Preferred over v4
for database primary keys. OQL: `UUID_V7()`.

**CUID**

```http
GET /api/v2/gen/cuid
```
```json
{ "value": "clh3m2x0g0000qzrm5x5x5x5x" }
```

Collision-resistant, URL-safe, roughly time-ordered. Popular in JavaScript
ecosystems. OQL: `CUID()`.

**ULID**

```http
GET /api/v2/gen/ulid
```
```json
{ "value": "01ARZ3NDEKTSV4RRFFQ69G5FAV" }
```

Universally unique, lexicographically sortable, UUID-compatible. 48-bit
millisecond timestamp + 80 bits of randomness. OQL: `ULID()`.

---

### Stateful generators

Each requires a named definition. Called via `/next`.

**Sequence**

The most common stateful generator. See `/api/v2/seq` alias for full
documentation. Canonical path: `/api/v2/gen/seq`.

```http
POST /api/v2/gen/seq
Content-Type: application/json

{
  "name":    "invoice_number",
  "start":   1000,
  "step":    1,
  "prefix":  "INV-",
  "padding": 6
}
```

```http
GET /api/v2/gen/seq/invoice_number/next
```
```json
{ "value": 1000, "formatted": "INV-001000" }
```

```http
GET /api/v2/gen/seq
```
```json
{ "sequences": [ { "name": "invoice_number", "current": 1000,
                   "increment_by": 1, "cycle": false } ], "count": 1 }
```

Lists the tenant's named sequences, sorted by name (mirrored at the
`/seq` alias like every sequence route).

OQL: `NEXT VALUE FOR invoice_number`, `@SEQ('invoice_number')`.
`NEXT VALUE FOR` increments once per row in multi-row SELECT results.
`@SEQ('name')` returns the session-local last value of the named sequence without advancing it. `@GEN('name')` dispatches to the named stateful generator and produces a value (random/round-robin/etc. per the generator's type and config).

**Token**

```http
POST /api/v2/gen/token
Content-Type: application/json

{
  "name":   "api_key",
  "config": { "length": 32 }
}
```

```http
GET /api/v2/gen/token/api_key/next
```
```json
{ "value": "xLm3Kv9...Q4w" }
```

Cryptographically secure random bytes via `crypto/rand`. Encoding options:
`hex`, `base64`, `base64url`. Default 32 bytes. Used for API keys, webhook
secrets, password reset tokens, session identifiers. OQL: `@GEN('api_key')`.

**Timestamp**

```http
POST /api/v2/gen/timestamp
Content-Type: application/json

{
  "name":   "invoice_ts",
  "config": {
    "zone":   "Pacific/Auckland",
    "layout": "2006-01-02T15:04:05Z07:00"
  }
}
```

```http
GET /api/v2/gen/timestamp/invoice_ts/next
```
```json
{
  "type":  "timestamp",
  "name":  "invoice_ts",
  "value": "2026-06-12T09:41:00+12:00"
}
```

Timezone resolution uses Go's embedded IANA tz database (`time/tzdata`).
No OS tz files required. The `value` is the current time formatted by the
generator's `layout` in its configured `zone`.

`precision` controls truncation: `nanosecond`, `microsecond`, `millisecond`,
`second`, `minute`. `format` uses Go time layout syntax; common presets
(`iso8601`, `rfc2822`, `date`, `time`, `unix`, `unix_ms`) are also accepted.

OQL: `@GEN('invoice_ts')`.

**random_int**

```http
POST /api/v2/gen/random_int
Content-Type: application/json

{
  "name":   "ab_bucket",
  "config": { "min": 0, "max": 99 }
}
```

```http
GET /api/v2/gen/random_int/ab_bucket/next
```
```json
{ "value": 42 }
```

Uniform random integer in `[min, max]` inclusive. Cryptographically secure
(`crypto/rand`). Used for A/B test bucket assignment, jitter, random
sampling indices. OQL: `@GEN('ab_bucket')`.

**pick**

```http
POST /api/v2/gen/pick
Content-Type: application/json

{
  "name":   "on_call_engineer",
  "config": {
    "set":  ["alice", "bob", "carol", "dave"],
    "mode": "random"
  }
}
```

```http
GET /api/v2/gen/pick/on_call_engineer/next
```
```json
{ "value": "carol" }
```

Randomly selects one element from the declared set. `mode` options:
`random` (uniform, with replacement) and `round_robin` (sequential, wraps)
are available. `weighted` (a `weights` array parallel to `set`) is reserved
and rejected at define time pending Part 2. Set mutation via
`PUT /api/v2/gen/pick/{name}` is also a Part 2 addition; in Part 1 the set is
fixed at definition and a generator is replaced by deleting and redefining it.

Used for assignment routing, random sampling, load distribution across a
small fixed pool. OQL: `@GEN('on_call_engineer')`.

**Known limitation:** Round-robin cursor position is held in memory and
periodically persisted. A restart may reset the cursor to position 0,
producing up to one repeated pick sequence. Applications requiring strict
fairness guarantees should use `sequence` with modulo arithmetic against
the set size rather than `round_robin` mode.

**nanoid**

```http
POST /api/v2/gen/nanoid
Content-Type: application/json

{
  "name":   "voucher_code",
  "config": {
    "alphabet": "ABCDEFGHJKLMNPQRSTUVWXYZ23456789",
    "length":   8
  }
}
```

```http
GET /api/v2/gen/nanoid/voucher_code/next
```
```json
{ "value": "K7RNMX4P" }
```

URL-safe random string of configurable alphabet and length.
Cryptographically secure. Default alphabet is URL-safe base64 characters.
Used for voucher codes, short IDs, OTP tokens, invite codes.
OQL: `@GEN('voucher_code')`.

**slug**

```http
POST /api/v2/gen/slug
Content-Type: application/json

{
  "name":   "readable_id",
  "config": {
    "words":      2,
    "separator":  "-",
    "vocabulary": "adjective-noun"
  }
}
```

```http
GET /api/v2/gen/slug/readable_id/next
```
```json
{ "value": "quiet-river" }
```

Human-readable random identifier from a configurable vocabulary.
`vocabulary` options: `adjective-noun`, `adjective-adjective-noun`,
`word` (single word list). Useful for human-facing temporary identifiers,
room names, branch names, readable references. Collision probability
increases with usage; not suitable as a primary key without a uniqueness
check. OQL: `@GEN('readable_id')`.

---

### OQL function reference

| Function | Generator | Notes |
|----------|-----------|-------|
| `UUID_V4()` | uuid_v4 | Stateless, always available |
| `UUID_V7()` | uuid_v7 | Stateless, always available |
| `CUID()` | cuid | Stateless, always available |
| `ULID()` | ulid | Stateless, always available |
| `NEXT VALUE FOR name` | sequence | Increments once per row in multi-row results |
| `@SEQ('name')` | sequence | Session-local last value, no increment |
| `@GEN('name')` | any stateful generator | Session-local last value (S10) |
| `@GEN('name')` | any stateful | Calls `/next` on the named generator |

All functions are also available in the tsqlruntime expression evaluator
(FSM `set` clauses and guard expressions) and in event action payloads
via `{{gen:name}}` template variables. Stateless generators use
`{{gen:uuid_v4}}`, `{{gen:uuid_v7}}`, `{{gen:cuid}}`, `{{gen:ulid}}`.

Generator names are globally unique per tenant across all stateful generator
types. `@GEN('api_key')` resolves unambiguously to whichever stateful
generator is registered under that name, regardless of type. Attempting to
create a generator whose name already exists under any type returns
`XOLU-GEN002`.

---

### /api/v2/seq alias

All `/api/v2/seq` paths are mirrored to `/api/v2/gen/seq`:

```
/api/v2/seq              → /api/v2/gen/seq
/api/v2/seq/{name}       → /api/v2/gen/seq/{name}
/api/v2/seq/{name}/next  → /api/v2/gen/seq/{name}/next
/api/v2/seq/{name}/reset → /api/v2/gen/seq/{name}/reset
```

The alias exists because sequences are by far the most common stateful
generator and callers that only use sequences should not need to know about
the broader generator subsystem. The alias is permanent and will not be
deprecated.

---


## pkg/gc — Generic GC Worker Infrastructure

Several xolu subsystems require periodic background sweeps. `pkg/gc` provides
a shared worker abstraction so the ticker-and-stop-channel pattern is
implemented once rather than per-subsystem.

### Interface

```go
type Sweeper interface {
    Sweep(ctx context.Context) (Report, error)
}

type Report struct {
    Examined    int
    Collected   int
    Quarantined int           // two-phase sweepers (blob GC)
    Errors      int
    Duration    time.Duration
}

func NewWorker(name string, s Sweeper, interval time.Duration,
               logger zerolog.Logger) *Worker

func (w *Worker) Start()
func (w *Worker) Stop()
func (w *Worker) RunOnce(ctx context.Context) (Report, error)
```

`RunOnce` is used by tests and the admin trigger endpoint. The worker logs
each completed sweep at INFO level with name, counts, and duration.

### Sweeper implementations

| Package | Type | What it collects |
|---------|------|-----------------|
| `pkg/blob` | `blob.Sweeper` | Unreferenced blobs (filesystem, two-phase quarantine) |
| `pkg/timeseries` | `timeseries.RetentionSweeper` | Expired timeseries events |
| `pkg/fsm` | `fsm.GCSweeper` | Stalled and dead machine instances |
| `pkg/event` | `event.GCSweeper` | Expired delivery log entries, dead-letter cleanup |

The existing `blob.GCWorker` and `timeseries.RetentionWorker` are migrated to
use `pkg/gc.Worker` when the package is introduced. Their sweep logic is
unchanged; only the lifecycle management moves to the shared implementation.
The timeseries sweeper gains a `Report` return value (currently fire-and-forget)
as part of this migration, making it available to the admin endpoint.

### FSM GC configuration

Per-definition GC policy:

```json
{
  "gc": {
    "stalled_after": "7d",
    "dead_after":    "30d",
    "on_gc_collect": "decommission"
  }
}
```

- `stalled_after`: collect machines not in a terminal state and not walked
  within this duration
- `dead_after`: collect machines in a terminal state older than this duration
- `on_gc_collect`: if set, the GC walks the machine to this input before
  deletion, producing a proper history entry; if absent, a `gc_collected`
  history entry is recorded and the machine is deleted

A definition with `"gc": null` opts out entirely. The minimum age floor
`XOLU_FSM_GC_MIN_AGE` (default `1h`) prevents collection of machines that
have not yet had their first walk — no machine younger than the floor is
touched regardless of definition policy.

### Admin endpoint

```
POST /api/v1/admin/gc/{name}/run   — trigger a sweep synchronously
GET  /api/v1/admin/gc              — list workers and last report
```

The endpoint lives in `/api/v1` because GC is operational infrastructure,
not a versioned feature. The response body is always a `gc.Report`.

---

## Error codes

### FSM

| Code | HTTP | Meaning |
|------|------|---------|
| `XOLU-FSM001` | 404 | Definition not found |
| `XOLU-FSM002` | 404 | Machine not found |
| `XOLU-FSM003` | 409 | No transition for input in current state |
| `XOLU-FSM004` | 422 | Transition guard rejected |
| `XOLU-FSM005` | 409 | Machine is in a terminal state |
| `XOLU-FSM006` | 422 | Definition or machine validation failed |
| `XOLU-FSM008` | 409 | FSM walk conflict in commit (version mismatch or guard failure) |
| `XOLU-FSM009` | 422 | No terminal state reachable from one or more non-terminal states |
| `XOLU-FSM010` | 422 | Variable declaration invalid |
| `XOLU-FSM011` | 422 | Set clause expression failed to parse or evaluate |
| `XOLU-FSM012` | 404 | Child definition (linked state) not found at machine creation time |
| `XOLU-FSM013` | 422 | Override block references transition input not present in definition |

### Event

| Code | HTTP | Meaning |
|------|------|---------|
| `XOLU-EV001` | 422 | Invalid event def (bad `event_type`, `action_type`, or config) |
| `XOLU-EV002` | 404 | Event def not found |
| `XOLU-EV003` | — | Delivery failed — recorded in the log, not surfaced synchronously (dispatch is async) |

Codes for deferred features (sync-rollback, FSM-walk actions, dead-letter,
replay) are listed in `EVENT_PENDING.md`, not here, since those features are not
implemented at 1.0.

### Metadata

| Code | HTTP | Meaning |
|------|------|---------|
| `XOLU-META001` | 404 | Entity not found |
| `XOLU-META002` | 404 | Metadata key not found |
| `XOLU-META003` | 413 | Value exceeds `XOLU_META_MAX_VALUE_BYTES` |
| `XOLU-META004` | 400 | Key contains invalid characters |
| `XOLU-META005` | 400 | `expires_at` is not a valid RFC3339 timestamp |

### Generators

| Code | HTTP | Meaning |
|------|------|---------|
| `XOLU-GEN001` | 404 | Generator definition not found |
| `XOLU-GEN002` | 409 | Generator name already exists for this type |
| `XOLU-GEN003` | 400 | Invalid configuration for generator type |
| `XOLU-GEN004` | 422 | Reset value less than start (sequence only) |
| `XOLU-GEN005` | 422 | Generator name referenced in expression does not exist |
| `XOLU-GEN006` | 422 | `@SEQ()` called before `NEXT VALUE FOR` in this session |
| `XOLU-GEN007` | 400 | Invalid step value (sequence only) |
| `XOLU-GEN008` | 422 | Pick set is empty |
| `XOLU-GEN009` | 422 | Weights array length does not match set length (pick weighted mode) |
| `XOLU-GEN010` | 400 | Unknown timezone (timestamp generator) |

---

## Implementation notes

**FSM toolkit.** Structural validation is delegated to
`github.com/ha1tch/fsm-toolkit` (v0.9.6+). xolu adds the guard evaluation
layer, machine variables, and sequence integration on top of the toolkit's
type model. The toolkit validates topology; xolu validates and evaluates the
runtime semantics.

**Guard evaluation.** OQL WHERE clause guards are evaluated by the OQL
predicate engine against `_api_v2_machine_`. T-SQL expression guards in
`guard` and `set` fields are evaluated by the `tsqlruntime` expression
evaluator from `github.com/ha1tch/aulsql/pkg/tsqlruntime`. No stored
procedure registry is required for the baseline — the expression evaluator
alone handles `@variable` arithmetic. A `guard_proc` field naming a
registered aulsql procedure is a planned future extension for complex
multi-step guard logic.

**FSM storage.** Four dedicated tables: `fsm_definitions` (definition JSON
as authored, prototype template), `fsm_machines` (machine snapshot taken
at creation — full copy of definition properties plus any overrides —
current state, variable values, `last_walked_at`, ref binding,
`fsm_def_id` for lineage tracking), `fsm_history` (append-only
transition log), `fsm_terminal_states` (denormalised cache of which states
are terminal per machine, not per definition, populated at creation time
from the machine's snapshot). Because machines are self-contained snapshots,
`fsm_definitions` has no referential integrity relationship with
`fsm_machines` — deletion of a definition does not cascade.

**Event delivery.** Async webhook delivery uses an internal queue with
Delivery is at-most-once and single-attempt in 1.0 (no retry/backoff; see
EVENT_PENDING.md). Event-def actions should be idempotent regardless.
Sync OQL and Sulpher actions run in the same SQLite transaction as the
triggering write and must complete within `XOLU_SQLITE_BUSY_TIMEOUT`.

**Generators.** Stateless generators (`uuid_v4`, `uuid_v7`, `cuid`, `ulid`)
are pure functions registered directly as OQL functions and tsqlruntime
functions — no storage, no configuration. Stateful generators persist their
definitions in a `gen_definitions` table keyed by `(tenant_id, type, name)`.
Sequence state lives in a separate `sequences` table for efficient
`UPDATE ... RETURNING` increment. All other stateful generator state (pick
mode cursor for round-robin) is held
in memory per worker with periodic persistence. Timezone resolution uses
Go's embedded IANA tz database (`time/tzdata`) — no OS tz files required.
All random generation uses `crypto/rand`. The `@GEN('name')` OQL function
dispatches to the appropriate stateful generator at query time. The `/api/v2/seq`
alias is implemented as a router-level redirect to `/api/v2/gen/seq` with
no additional logic.

**GC worker.** `pkg/gc` provides `Sweeper` and `Worker`. All four sweeper
implementations (`blob`, `timeseries`, `fsm`, `event`) use `gc.Worker` for
lifecycle management. Workers are registered at server startup and exposed
via `/api/v1/admin/gc`.

---

*This document describes planned post-v1.0 functionality. The API surface,
error codes, and integration points are subject to change before the first
experimental release.*
