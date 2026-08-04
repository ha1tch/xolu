# Blob-Backed Tenant Backup and Restore — Design Specification

Updated: 2026-07-22
Status: design — new documentation; describes nothing that is implemented
Companions: `BLOB_EXTENDED_ROLES.md` (roles overview), `BLOB_MANIFEST.md`
(manifest specification)

This file specifies role 1: what a tenant backup captures, how capture
runs, and how restore works. It is grounded in an audit of the tree at
0.16.19, not in assumption; the audit's conclusions are stated with
their evidence.

---

## 1. The tenant state inventory (audited)

A tenant's on-disk state spans four planes. Their authoritative-vs-
derived status determines the backup set: **backup captures exactly the
authoritative planes; derived planes are rebuilt on restore.**

| Plane | Storage | Status | In backup? |
|---|---|---|---|
| Primary store | SQLite — per-file mode: `tXXXX/store/store.db`; shared mode: `tXXXX_*`-prefixed tables in one shared file | **Authoritative** (entities, graph, bal journal+accounts, cal bookings, /fsm machines, meta, events, manifest registry) | Yes |
| Timeseries | Pebble store per tenant at `tXXXX/ts/` | **Authoritative** — the points live here (`pkg/timeseries/store.go` is Pebble-backed); co-resident rollups are derived but are captured atomically with the points at no meaningful cost | Yes (Pebble checkpoint) |
| cal occupancy index | Pebble store per tenant at `tXXXX/cal/` | **Derived, explicitly**: `pkg/cal/cal.go` — "a *derived, rebuildable bitmap index*… never authoritative; it can always be discarded and rebuilt from the SQLite records, and the invariant `index == rebuild` is the acceptance gate" | **No** — restore rebuilds via `RebuildFrom` |
| Blob namespace | Content files (immutable) + `.keys/` aliases (mutable) | Content authoritative+immutable; alias set authoritative+mutable | Aliases captured as manifest members; content referenced, never copied |

Ancillary state:

- **Dynamic configuration** — one base-level JSON file
  (`pkg/dynconfig`), containing per-tenant namespaced overrides
  (`tenant.{name}/…`). The tenant's namespace entries are captured as a
  small member; global entries are not tenant state and are excluded.
- **Schema position** — the unprefixed `schema_version` and
  `schema_version_v2` tables. Their content rides inside the SQLite
  member; the manifest additionally records `schema-position` in its
  header so restore can gate on version *before* touching the database.
- **Tenant registry row** — the tenant's row in the unprefixed
  `tenants` table (shared mode) / the tenant's identity facts.
  Captured as part of the SQLite plane (see §3.2 for the shared-mode
  handling).

A layout note that simplifies everything: `SharedTSDir` / `SharedCalDir`
/ `SharedBlobDir` exist in `pkg/storelayout` but have **no callers** —
ts, cal and blob are per-tenant directories in both tenancy modes. Only
the SQLite plane forks on mode.

### 1.1 Forward compatibility — new primitives must not rot this spec

W-4 (roles doc §2) is the cautionary tale: `/export` was correct when
written, then the ts/cal/blob planes arrived and nothing forced an
update, and it silently became a partial export. This specification
defends against the same rot with a rule and a guard:

**The plane-coverage rule (structural).** A primitive whose
authoritative state lives in `tXXXX_`-prefixed tables inside the
primary store is captured automatically by the SQLite plane — nothing
to amend. This covers bal today and **dxp by its own design**: the
dxp proposal places every guard-bearing participant's tables for a
tenant in the one primary database, tentative rows included, on one
transaction. Only a primitive that adds a **new per-tenant storelayout
role directory** (as ts did) extends the plane inventory, and that is
the only event requiring a capture amendment.

**The unknown-role guard (loud, not silent).** Capture enumerates the
tenant's on-disk role directories and compares them against its known
set — `store` (captured), `ts` (captured via checkpoint), `cal`
(**known and excluded by decision**, §1 — recorded per role name so
the guard distinguishes excluded from unknown), `blobs` (aliases
captured, content referenced). An unrecognised role directory **fails
the capture with a typed error** rather than producing a silently
partial backup; an explicit operator acknowledgement flag exists for
the deliberate case. `pkg/storelayout` already carries role-name
validation and a `Check(Model)` issue reporter — the guard composes
with that machinery rather than duplicating it. House precedent: the
`rebuildRIRegistry` parity guard / `XOLU_STRICT_SUBSYSTEMS` pattern —
a configuration the code cannot honestly serve fails loudly.

**dxp forward pointer, recorded not designed:** the dxp proposal lists
blob among its participants ("stage the document"), which implies
tentative alias operations joining a composed commit. That lands on
the alias-CAS seam (`BLOB_CONDITIONAL.md`) if and when dxp executes;
nothing in this specification anticipates it beyond noting that the
CAS mechanics are the natural attachment point.

## 2. The backup manifest

`kind: backup`, per `BLOB_MANIFEST.md`. Member names use role prefixes
(names are manifest-internal labels and may contain `/`):

| Member name | Content | Notes |
|---|---|---|
| `store/store.db` | the SQLite capture product | per-file: snapshot; shared: logical export (§3.2) |
| `ts/{relpath}` | each file of the ts Pebble checkpoint | SSTs are immutable ⇒ unchanged SSTs dedup across successive backups structurally; only the small MANIFEST/OPTIONS/WAL files differ every time |
| `alias/{key}` | the content the alias points at | `sha`/`size`/`content-type` describe the *referenced content*; content is already in the store and is never copied |
| `config/dynconfig.json` | the tenant's dynconfig namespace entries | canonical-ordered JSON; small |

Required headers: `kind backup`, `tenant`, `created`, `xolu-version`,
`schema-position`. The manifest's SHA is the backup's identity.

## 3. Capture

### 3.1 Ordering and the GC deadline

One capture operation, strictly ordered:

1. **Blob alias scan** → `alias/…` members.
2. **ts checkpoint**: Pebble `Checkpoint(dir)` (hard links, cheap and
   atomic) → store each file content-addressed (`PutRaw`) → `ts/…`
   members → remove the checkpoint directory.
3. **SQLite capture** (per mode, §3.2) → store content-addressed →
   `store/store.db` member.
4. **dynconfig extract** → member.
5. **Write the manifest blob**; **insert the registry row** — same
   operation, never deferred.

SQLite deliberately captures **after** ts and aliases: SQLite is where
cross-plane references originate (meta subjects → ts timelines), so
snapshot-last means the SQLite plane may reference ts state *newer*
than the checkpoint — which restores as a dangling meta annotation, a
state the meta cascade already documents as harmless and
TTL-reclaimable. The reverse order would instead strand slightly-newer
timelines unreferenced; both are tolerable, one is chosen and stated.

The registry row for a backup is inserted **after** its own SQLite
capture, so a backup never contains its own row — only prior backups'
rows. Backups therefore record backup history without self-reference.

**Deadline (GC safety):** every `PutRaw` product in steps 2–3 is
alias-less and thus GC-visible only through the registry row written in
step 5. The whole capture must complete within a budget of
**GracePeriod/2** (5 minutes at defaults) or abort and clean up. The
quarantine grace then guarantees no capture product can be hard-deleted
before registration. (A sweep may *quarantine* a capture product
mid-capture; `purgePending`'s resurrection check plus registration
within grace makes that harmless.)

**Cost caveat (U1/D11 in `BLOB_OPEN_QUESTIONS.md`):** `PutRaw` streams
into a temp file *before* its existence check, so ts capture reads
**and writes** every checkpoint byte on every capture even when fully
deduplicated — dedup saves storage, not capture I/O. On large ts
stores this collides with the budget above; the standing proposal is
an mtime+size→SHA skip cache seeded from the previous backup manifest
(with a `--verify` mode that re-hashes everything). Until decided and
measured, the deadline claim is conditional on ts size.

**Concurrency:** one capture per tenant at a time; a second
`POST /backup/capture` while one runs returns `409` with a
capture-in-progress error code. No queueing in v1.

**Two mechanical notes:** (a) a tenant whose ts store is lazily
unopened is opened via the ts manager for the checkpoint; a tenant
with no `ts/` directory at all simply contributes no `ts/…` members —
legal. (b) dynconfig namespaces are keyed by tenant **name**
(`tenant.{name}/…`) while capture is keyed by tenant **id** — capture
resolves the name from the tenant's registry row and records the
extract in the id-keyed manifest; v1's same-id restore implies
same-name, and a future id-remap inherits this as one more rename
site.

### 3.2 The SQLite plane, by tenancy mode

**Per-file mode.** The tenant's `store.db` is snapshotted with the
SQLite backup API (or `VACUUM INTO`); both fold the WAL, both yield a
transactionally consistent single file. No machinery for this exists in
the tree today — greenfield, but standard.

**Shared mode — the fork, resolved as logical export.** `VACUUM INTO`
of the shared file would copy **every tenant's tables** into one
tenant's backup: a cross-tenant data leak, categorically unacceptable.
Instead, capture is a logical per-tenant export into a fresh database:

1. `ATTACH` an empty target database.
2. Open one read transaction over the shared store (WAL snapshot
   isolation makes every read in the transaction see one instant).
3. For each `sqlite_master` entry whose name begins with this tenant's
   `tXXXX_` prefix (`tenant.TablePrefix`): execute its recorded DDL
   against the target, then `INSERT INTO target.T SELECT * FROM T`.
   Indexes and triggers copy by their recorded DDL likewise.
4. Copy the global rows this tenant owns: its `tenants` row, plus the
   full `schema_version` / `schema_version_v2` tables (tiny, and
   restore needs them).
5. Commit/detach. The product is a per-file-shaped database for one
   tenant — **the two modes converge on one backup format**, which is
   what makes cross-mode restore possible at all.

The export runs inside one transaction, so it is exactly as consistent
as the per-file snapshot. Cost is O(tenant's rows), not O(shared file).

**dxp forward-consistency note.** When dxp lands, its tentative rows
(`txn_id, state, reserve_deadline` per its convention) live in the same
prefixed tables as committed state, on the same database — so a capture
taken mid-transaction snapshots reservations *together with* their txn
records, transactionally consistently, in either tenancy mode. A
restore of such a snapshot resurrects in-flight reservations whose
deadlines then expire under dxp's own idempotent-release machinery;
backup neither knows nor needs to know about dxp. This is the
plane-coverage rule (§1.1) doing its job, stated once so nobody
re-derives it under pressure.

### 3.3 New seams required

- `Checkpointer` — optional interface on the ts `Store`
  (`Checkpoint(dir string) error`); the Pebble backend implements it
  via `db.Checkpoint`. The `StoreFactory` abstraction is preserved:
  capture fails cleanly with a typed error if a backend does not
  implement it.
- The capture engine itself (`pkg/backup` or similar), owning the
  ordering, the deadline, and the manifest assembly.
- Registry + `MultiSHARefSource` + fail-safe abort, per
  `BLOB_MANIFEST.md` §2–3 — prerequisites, not parts.

## 4. Restore

Restore is **iolu-first**: `iolu backup restore` against a stopped or
tenant-quiesced instance. iolu operates offline on the data root —
opening the per-file or shared store directly, as `iolu db check`
already does — so restore needs no running server. An HTTP restore
endpoint is deliberately not part of v1 — restoring a live tenant over
the wire is a footgun with no motivating use case yet.

**Re-run mechanics (what "idempotent" means here).** A restore
interrupted partway is repaired by running it again, and that claim is
only true with these rules: per-file target — the store file is
replaced wholesale (copy member over `TenantStorePath`), inherently
re-runnable; shared target — under `--replace`, restore **first drops
every `tXXXX_*` object for the tenant** (tables, indexes, triggers,
from `sqlite_master`) and then copies, so a partial prior attempt
cannot collide with `CREATE`; ts — the target `ts/` directory is
removed and re-materialised whole, never merged; aliases — alias
writes are last-write-wins by construction; derived-plane rebuilds are
re-runnable by their own contracts. Restore is therefore idempotent
*per plane by replacement*, not by merging — a deliberate property,
since merging partial restore state is exactly the accounting §5
declines to attempt.

**Job persistence (capture side, stated once for both specs):** the
capture job is in-memory in the async-query precedent's shape; the
durable facts are the registry row and the stored members. A server
restart mid-capture loses the job, nothing was registered, and the
products age out via GC — recovery is re-running capture.

### 4.1 Preconditions

- The manifest resolves, parses, and verifies (§4.4 step 0).
- **Version gate, checked before touching any database:** the
  manifest's `xolu-version`/`schema-position` must be ≤ the running
  version's. Older backups migrate forward through the schema ladder on
  first open — that is what the ladder is for. A backup from a *newer*
  xolu is refused outright.
- **Target tenant id = source tenant id.** Shared-mode table names and
  intra-database references carry the `tXXXX` encoding; remapping ids
  means rewriting prefixes and is a declared **non-goal for v1**
  (recorded limitation, revisit with role 3).
- The target tenant is absent, or the operator passes an explicit
  `--replace` acknowledging destruction of current state.

### 4.2 Materialisation order

1. Create the tenant directory skeleton (storelayout roles).
2. **SQLite**: per-file target — place the member at
   `TenantStorePath`; shared target — reverse of §3.2: attach the
   member database, copy `tXXXX_*` DDL+rows into the shared store,
   merge the `tenants` row (schema-version tables are not overwritten —
   the running instance's ladder position governs; the member's copies
   served the version gate).
3. **ts**: materialise `ts/…` members into `TenantTSDir` at their
   relative paths. The result is a valid Pebble directory (that is what
   a checkpoint is).
4. **Blob aliases**: write `.keys/{key}` files from `alias/…` members.
   Content resolution: same-instance restore finds every referenced SHA
   already present (pinned by the registry the whole time);
   cross-instance restore requires the content to have been fetched
   first — restore composes with role 4 (**import, then restore**) and
   does not duplicate its transport.
5. **dynconfig**: merge the tenant's namespace entries.

### 4.3 Derived-plane rebuild

6. cal: `RebuildFrom` the SQLite booking records (the package's own
   stated contract).
7. bal rollup: `RebuildRollup` from the journal.
8. Any other rebuild-oracle-backed derived state follows the same
   pattern: rebuild, then prove.

### 4.4 Verification is the oracles

0. (Pre-restore) `blob.manifest`-style verification of the backup
   itself: manifest bytes hash to its SHA; every member resolvable;
   sizes match.
9. (Post-restore) **`iolu db check` green is the restore acceptance
   gate** — the rebuild oracles (`graph.edges`, `bal.fold`,
   `bal.chain`, `blob.manifest`, and successors) are precisely the
   instrument for proving a restored tenant self-consistent. This is
   the payoff of the house's verification-first architecture: restore
   does not need bespoke validation machinery.

### 4.5 The round-trip guard (dormant guard, registered at birth)

A full backup→wipe→restore→verify round trip on a populated tenant
(entities+graph, bal transfers, ts points, cal bookings, blob keys,
meta annotations) with (a) `iolu db check` green and (b) plane-by-plane
data equality against the pre-backup state. This is long-running and
belongs to the dormant-guards table with its canonical invocation the
day it is written, per the working agreement — a backup path whose
restore has never been exercised is the canonical unexercised-guard
failure waiting to happen.

## 5. Failure modes and non-goals

| Case | Behaviour |
|---|---|
| Capture exceeds deadline | Abort; remove staged capture products' registry state (none was written); products age out via normal GC |
| Capture step fails midway | Same: nothing registered ⇒ nothing retained; partial products are GC-reclaimed |
| Restore fails midway | The tenant directory is the unit: operator re-runs restore (idempotent materialisation) or removes the directory; no partial-restore accounting is attempted in v1 |
| Backup of a tenant mid-heavy-write | Valid: each plane is consistent at its instant; cross-plane skew bounded by capture duration; dangling cross-plane references are the already-tolerated orphan classes |
| Tenant-id remap on restore | Non-goal v1 (§4.1) |
| Restore over HTTP | Non-goal v1 (§4) |
| Incremental/differential backup *machinery* | Non-goal — unnecessary: content-addressing already makes successive backups structurally incremental in storage; every backup is logically full |

## 6. Surface

Capture is an **async job** in the established async-query shape
(`ASYNC_QUERIES.md`: submit → `202` + id → poll → result), managed by a
per-tenant job manager on the existing sulpher-job-manager precedent —
capture runs minutes, and a synchronous POST holding a connection open
for that long is the wrong shape:

- `POST /api/v2/backup/capture` → `202` `{job_id, status}`;
  `409` if a capture is already running for the tenant
- `GET /api/v2/backup/capture/{job_id}` → status; on completion carries
  the manifest SHA + summary (member count, total bytes, duration)
- `GET /api/v2/backup/list`, `GET /api/v2/backup/{sha}`,
  `DELETE /api/v2/backup/{sha}` (retire = registry row deletion)
- `iolu backup capture|list|verify|restore` — restore exists **only**
  here in v1
- Gating: `XOLU_BACKUP_ENABLED` (requires `XOLU_BLOB_ENABLED`), in the
  established `XOLU_BAL_ENABLED` pattern; the configuration validator
  rejects backup-without-blob as it does S3-without-blob

**Lifecycle events:** completion and failure are announced through the
shipped event model as dotted subjects named per the `NOLU_EVENTS.md`
settled conventions (`backup.captured`, `backup.capture_failed`,
`backup.retired`). Names are fixed now; emission wires in when the
event-plumbing work (T-07/T-08 family) makes it natural — nothing in
capture depends on it.

**Relation to `GET /api/v1/export` (and W-4):** the existing export is
this feature's ancestor and currently captures the SQLite plane only —
an export today silently omits every timeseries point and the blob
namespace (finding W-4, roles doc §2). Once capture exists, `/export`
is re-based to **render a backup manifest as its zip stream** (capture
if none is fresh, then stream members): one capture mechanism, two
presentations, W-4 closed without breaking the v1 wire contract. The
existing `backup_restore_test.go` exercises a restore flow against the
old zip format and must be examined — and either migrated to the
manifest-backed format or retained as a legacy-format guard — when this
stage begins.

Error-code allocation (registered typed errors, house pattern):
capture-in-progress (409), capture-deadline-exceeded, backup-not-found,
version-newer-than-instance, checkpoint-unsupported-backend, and
manifest-parse/verify failures shared with `BLOB_MANIFEST.md`.

## 7. What this section deliberately does not cover

Import transport, peer auth, staging-vs-GC state, and the read-only
tenant concept belong to the role-4 specification (`BLOB_IMPORT.md`,
next); the conditional-request wire contract (`If-Match`/`Range`/PATCH)
belongs to its own W-2 specification. The registry `status` column that
import staging requires is specified in `BLOB_MANIFEST.md` §2 so that
the schema is created once, correctly.
