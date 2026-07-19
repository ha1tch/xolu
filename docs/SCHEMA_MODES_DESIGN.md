# Schema Handling — Design Notes

Status: design notes only. None of the modes below beyond (a) growth are
implemented. This records the intended direction so the layout normalization did
not have to pre-commit to it.

Copyright (c) 2026 haitch · h@ual.li

## Context

The storage-layout normalization fixed where the schema directory lives: schema
files are server-level, at `<BaseDir>/schema/` (the invariant location, derived
via `pkg/storelayout`). That is the *default* schema set. What follows is the
design for richer schema handling layered on top of that, captured for later
implementation. It is a validation-layer and tenant-model feature, distinct from
the on-disk layout.

## The three modes

### (a) Growth mode — schema-optional (implemented today)

Development starts without a schema. The developer adds schema definitions entity
by entity; some entities have a schema, others do not. Entities without a schema
file are unconstrained. This is the current behaviour: the validator loads
whatever `<entity>.json` files exist under the schema directory and validates only
those entities; an absent schema means validation passes.

### (b) App mode — strict (not implemented)

A serious application already has a well-defined schema, and the database must
reject anything the schema does not allow. The schema may still change as
development progresses, but at any moment it is authoritative.

This is a *validation policy* over the same schema files, not a different layout:
the distinction from growth mode is reject-unknown vs allow-unknown. Today the
validator does field-level strict checking *within* a loaded schema
(`queryfy.Strict`) but has no "reject entities that have no schema" policy. App
mode needs that policy as a configurable switch.

### (c) Tenant clone mode — shared versioned schema (not implemented)

A tenant runs an instance of a known application. The same schema is used by many
tenants who run the same application. This is the mode that introduces schema
*versioning* and shared schema *sets*.

## Tenant ↔ (schema, version) association

Anchoring decision: **every tenant is associated with a fixed `(schema, version)`
pair.** The association is stored and explicit, not inferred at request time. A
tenant is on schema X version N until something deliberately changes it.

- The association lives in the tenant record (alongside the tenant's ID and
  name), persisted via the existing tenant registry persister.
- There is no runtime resolution: because the pair is pinned, a request never has
  to compute "which schema version applies to me." Resolution happens only when a
  tenant is moved between versions.
- Promotion (move a tenant to a newer schema version) and demotion (move it back)
  are deferred mechanisms. The fixed association is the durable fact they operate
  on; they are out of scope for the initial design.

## Schema versioning

Schema versions are classified by **app and schema-version**, where the
schema-version is **not** the app version. The key rule:

> Only app versions that actually introduce a schema change get a schema-set
> entry.

If an app ships versions 1.0, 1.1, 1.2, 1.3 and the schema changed only at 1.0
and 1.2, there are **two** schema sets, not four. A tenant on app 1.1 uses the
1.0 schema; a tenant on app 1.3 uses the 1.2 schema. The schema-version axis is
sparse relative to app releases — it advances only when the schema genuinely
changes.

Because each tenant stores its pinned `(schema, version)` directly, the
"which schema-version applies to app version 1.3" resolution is done once, at
pin/promotion time, not on every request.

## Open question — storage location of versioned schema content

Not yet decided: does the versioned, tenant-pinned schema *content* live

- **on disk** — e.g. `<BaseDir>/schema-sets/<schema>/<version>/<entity>.json`,
  in which case `pkg/storelayout` would own the path invariant for schema sets
  and the recon could verify their structure; or
- **in the database** — a `schema_sets` table keyed by `(schema, version)`, in
  which case schema sets are not a filesystem concern and `storelayout` is not
  involved.

The database option is more consistent with the system's recent direction (the
graph was moved out of files and into the per-tenant database in this same line
of work, precisely to avoid file-layout fragility). A versioned artifact shared
across tenants with promotion/demotion mechanics resembles transactional database
state more than loose files. This is the main fork to resolve before
implementing clone mode.

## Relationship to the layout invariant

- Growth and app mode operate on the single default schema set at
  `<BaseDir>/schema/` — already the invariant location.
- If clone-mode schema sets are stored on disk, they would be a *sibling* of
  `schema/` (e.g. `schema-sets/`), not a change to it — so adopting clone mode
  later does not require re-normalizing the existing layout.
- If they are stored in the database, the on-disk layout is unaffected entirely.

Either way, the layout normalization completed without needing this decision,
which is why it was deferred to these notes.
