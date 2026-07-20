# iolu — Operations Roadmap (proposal)

Updated: 2026-07-19
Status: proposal — not scheduled. No register items exist for this work;
filing happens per item when execution is decided.

## Context

iolu currently covers deployment *bootstrap*: `db init`, `db status`,
`db upgrade`, and the tenant registry (`create`, `list`, `info`,
`delete`, `provision-ts`). Eight commands, all concerned with bringing a
deployment into existence and describing it.

What it does not yet cover is a deployment's *life*: provisioning
runtime resources that have no other operator path, migrating data that
configuration changes leave behind, and verifying or repairing state.
This document records those gaps as a coherent roadmap so they are
designed once, not rediscovered piecemeal.

## Proposed commands

### 1. Calendar provisioning — `iolu cal create | list | info | delete`

Calendars are created through the cal `Manager` only — deliberately not
exposed on the HTTP API, not in the Go client. The consequence: there is
currently **no operator path to create a bookable calendar** short of
writing Go against `pkg/cal`. iolu, which already speaks to the store
directly, is the natural and intended-shaped home.

Sketch: `iolu cal create --base-dir D --tenant NAME --calendar-id ID
[--default-state binding|proposed] [--match-policy optimistic|pessimistic]`,
mirroring `cal.Calendar`'s fields; `list`/`info` read the calendar
registry; `delete` refuses while live bookings exist unless `--force`.
Uses `Manager` via the same construction path the server uses, keeping
iolu and xolu in agreement about layout and pragmas (the existing iolu
principle).

Priority: highest of this set — the only gap with no workaround.
Effort: ~1 day including tests.

### 2. Full-text backfill — `iolu db reindex-fts`

FTS indexing happens at write time only: a deployment that enables
full-text search after accumulating data has a permanently unsearchable
past, with no error and no signal. The missing treatment is an offline
reindex: walk every entity row per tenant, rebuild the FTS table
content, report counts.

Sketch: `iolu db reindex-fts --base-dir D [--tenant NAME]`; idempotent
(drop-and-rebuild per entity type inside a transaction); prints per-type
row counts so the operator can reconcile against `db status`.

Effort: ~1 day. Depends on nothing.

### 3. Query and REPL — `iolu query`, `iolu repl`

iolu's expanded name — interactive xolu — argues for this directly, and
the house already has the pattern: aulsql ships `iaul` (interactive
aul), a mature readline REPL whose conventions transfer wholesale.

Sketch, one-shot: `iolu query --base-dir D --tenant NAME --oql "..."`
executes through the pkg/oql executor against the Store interface
(backend-neutral by construction — this is the logical-core side of the
backend split); `--sulpher "..."` for graph queries; `--sql "..."` as a
raw passthrough living in the per-backend module, read-only unless
`--write`.

Sketch, interactive: `iolu repl` borrows iaul's shape — chzyer/readline
(pure Go, cross-compiles clean), persistent history at
`~/.iolu_history` with case-insensitive search, dynamic tab completion
seeded from the tenant's entity types and OQL keywords, meta-commands
(`\tenants`, `\entities`, `\use <tenant>`, `help`, `history`,
`quit`), table-formatted output, query timeout. OQL is the default
dialect; `\sql` switches to raw mode with the same read-only guard.

Effort: ~2–3 days with iaul as the template and the executor in-tree.

> **Struck (2026-07-19): `db migrate-layout`.** Previously proposed here
> on the inference that startup legacy detection implied a migration
> need. It does not: the detector's contract is to *refuse to start*
> when pointed at pre-normalisation dev-era directories, preventing data
> from forking across layouts — and zero pre-tenant deployments exist in
> production. The detector stays (cheap corruption interlock); the
> migration tool has no customer.

### 4. Backup and verify — `iolu db backup`, `iolu db verify`

No supported backup path exists; operators hand-roll file copies with no
integrity guarantee and no handling of the cal Pebble index. Sketch:
`backup` uses the SQLite online-backup API for each database file plus a
Pebble checkpoint for cal state, into a timestamped directory; `verify`
runs `PRAGMA integrity_check` across all databases and confirms the
backup is self-consistent. Restore remains a documented manual procedure
initially (copy back onto a stopped server) rather than a command.

Effort: ~2 days.

### 5. Integrity suite — `iolu db check`

The strongest consistency oracles in the project already exist as test
code: cal's index-equals-rebuild equality, graph-versus-store
comparison, and blob usage walking. Promoting them to operator tooling
is mostly plumbing: `iolu db check --base-dir D [--tenant NAME]` runs
each applicable oracle against a stopped deployment and reports
pass/fail per subsystem with counts.

Effort: ~2 days; highest value-per-effort after item 1, since the hard
parts are already written and battle-proven.

### 6. Graph rebuild — `iolu db rebuild-graph`

The server rebuilds the graph from the store at boot; a deployment whose
graph state is suspect currently has no offline path — the fix is
"restart and hope". An explicit offline rebuild with a before/after
node/edge count report gives operators a deliberate repair action and a
verification artefact.

Effort: ~0.5 day (the rebuild logic exists; this is invocation and
reporting).

### 7. Minor candidates (demand-driven)

- `iolu blob sweep` — orphan scan and optional removal.
- `iolu cal seal-status` — seal frontier inspection per calendar.
- `iolu seq list | info` — sequence inspection without the HTTP API.
- `iolu db vacuum` — SQLite maintenance with size before/after.

None of these justifies building ahead of demand; listed so they land in
an existing structure when demand arrives.

## Target-directory resolution (design decision)

iolu stays stateless about which deployment it targets: no config file,
no remembered directory. Every invocation's target is derivable from the
invocation and its environment, in this order:

1. `--base-dir` flag (always wins);
2. `IOLU_BASE_DIR` environment variable (session-scoped, visible, the
   PGDATA convention; deliberately in iolu's own namespace, not
   `XOLU_*`, so the tool can never inherit a variable exported for the
   server in the same environment);
3. upward discovery: walk from the current directory toward root until a
   conforming xolu base layout is found (`pkg/storelayout` already knows
   the shape), git-style. `cd /data && iolu db status` just works.

Statelessness is deliberate, not an omission: iolu carries destructive
verbs, and a tool that acts on a *remembered* target rather than a
*named or derivable* one is how wrong-deployment accidents happen. A
persistent default-directory config is explicitly rejected.

Safety asymmetry: read commands (`status`, `info`, `list`, `query`,
`repl`) accept the resolved default silently. Destructive commands
(`tenant delete`, future backup-restore and sweeps) always echo the
resolved base directory prominently, and when it was resolved from a
default rather than the flag, require confirmation (`--yes` or
interactive). The REPL binds its directory for the session at launch —
session state, not persistence.

## Backend coupling (audit 2026-07-19) and decoupling strategy

Should a second SQL backend ever land, iolu as written does not follow.
An audit of `cmd/iolu` found three classes of sqlite coupling:

1. **Constructor** — iolu calls `storage.NewSQLiteStore` directly rather
   than the `storage.NewStore` factory. Trivial to fix; fix it first.
2. **Raw SQL reads** — despite the package comment's promise that data
   access goes through `pkg/storage`, the read paths use `database/sql`
   directly: `schema_version`, the `tenants` table, per-entity counts.
   These belong behind Store-interface methods (several already exist;
   the rest — e.g. schema-version and entity-count reads — are natural
   interface additions). Moving iolu onto the interface makes the
   logical commands backend-agnostic automatically and restores the
   stated single-codepath principle, which is currently only half true.
3. **Inherent backend-isms** — `sqlite_master` catalogue queries for
   table discovery, and pervasive file-layout semantics (`init` creates
   files, `tenant delete` removes them, `provision-ts` makes
   directories). These cannot be abstracted away; a server-based backend
   has no files and its own catalogue.

Strategy, mirroring the storage factory's own shape: split iolu into a
**backend-neutral logical core** (tenant registry, status, counts —
everything expressible through the Store interface) and **per-backend
maintenance modules** (backup, verify, vacuum, layout migration,
catalogue-dependent discovery). Every command proposed in this roadmap
should declare which side it lives on when implemented; the cal
provisioning command (item 1) is logical-core and should be written
against the interface from day one.

Classes 1–2 are worth fixing opportunistically — they are hygiene
regardless of whether a second backend ever exists. Class 3 waits for a
real second backend, whose own semantics will dictate the module's
shape.

## Related proposals

The proposals directory carries the wider design programme this roadmap
serves: `referential-integrity.md`, `chronicle-substrate.md`,
`bal-conservation-primitive.md`, and `dxp-composed-commitment.md` —
each of which will, when implemented, bring iolu obligations
(provisioning, inspection, `db check` oracles) back into this document.

## Non-goals

- **Credentials and tokens** — xotogen's territory.
- **Online administration** — anything sensible against a *running*
  server belongs on the HTTP API; iolu operates on stopped deployments
  by design.
- **Schema, FSM, and event definitions** — first-class HTTP citizens,
  managed online.

## Sequencing

Item 1 (cal provisioning) leads: it is the only gap with no workaround
and it unblocks calendar-using deployments without Go in the loop. Item
3 (query/REPL) is the strongest candidate for second place — it makes
every other diagnostic conversation with a deployment cheaper, and the
name now promises it. Item 2 is a migration tool whose demand is
triggered by enabling FTS on existing data; build on first real need. Items 4–6 are steady-state operations
hygiene, natural to batch as an "iolu operations release" when a
production deployment is imminent. Item 7 waits for demand.

Total, if executed wholesale: roughly a week and a half. Nothing here is
speculative infrastructure; every item names the operator situation that
demands it.
