# Blob-Backed Import — Design Specification

Updated: 2026-07-22
Status: design — new documentation; describes nothing that is implemented
Companions: `BLOB_EXTENDED_ROLES.md` (role 4), `BLOB_MANIFEST.md`
(manifest + `staging` lifecycle), `BLOB_BACKUP.md` (restore
composition), `BLOB_CONDITIONAL.md`

Import moves manifest-described content from a **source** xolu instance
to a **local** one, verifiably and incrementally, with no coordination
layer. Its consumers, in order of arrival: operator-driven store import
(read replicas, seeding, ETL), cross-instance restore
(import-then-restore, `BLOB_BACKUP.md` §4.2), and — if the Layer-B
proposal is accepted — the data plane for nolu transfer history
(`BLOB_EXTENDED_ROLES.md` §7).

---

## 1. The remote read surface: fetch-by-SHA

Backup members are stored via `PutRaw` and have no aliases, so
cross-instance fetch needs a true by-address read:

```
GET /api/v1/blob/sha/{sha256}
```

- Same response shape as `GET /blob/{key}` (streams bytes; `ETag`,
  `X-Blob-SHA256`, `X-Blob-Size`, `Content-Type` headers); honours
  `Range` and conditionals per `BLOB_CONDITIONAL.md` — `Range` is what
  makes resumable large-member fetch cheap.
- Digest validated at the boundary (`validateSHA256Hex`, D-004);
  malformed → 400 `XOLU-BL004`-family; absent → 404.
- Routing note: `/blob/sha/{sha}` is two path segments and cannot
  collide with `/blob/{key}` (keys cannot contain `/`); the literal
  key `sha` remains reachable at `/blob/sha`.
- Placed on **v1** beside the surface it reads (an additive read
  endpoint, like the W-1/W-2 corrections), and mirrored on the S3
  listener implicitly: self-aliased content is already fetchable there.
- **Tenant scoping, like every blob endpoint:** the default route
  reads tenant `"default"`; the scoped form is
  `GET /api/v1/tenant/{tenant_id}/blob/sha/{sha256}`. A SHA is only
  resolvable within one tenant's store — tenant isolation is
  structural (distinct store roots) and this endpoint does not cross
  it.
- **Discovery:** the importer learns manifest SHAs from the source's
  own surfaces — `GET /api/v2/backup/list` for backups, the export
  verbs' responses for store-kinds — or out of band (the nolu transfer
  record, in the Layer-B composition). Import takes a SHA; it does not
  browse.
- **No batch endpoint in v1.** The realistic member profile is few and
  large (SSTs, a SQLite file) plus already-present alias content;
  per-SHA requests with client-side concurrency suffice. A batch form
  is deferred until a measured need (many-small-member workloads)
  exists — recorded, not built.

## 2. Authentication and authorisation

By reuse, both directions:

- **At the source:** the importing side presents an **xotogen-minted
  JWT** scoped to the source tenant, validated by the source's existing
  `authmw` — the exact guarantee `cmd/xotogen`'s regression tests bind.
  Where a fleet-internal trust boundary (mTLS/token) exists, import
  rides inside it. No dedicated peer-credential scheme.
- **Locally:** starting an import is an administrative act on the local
  tenant and requires the local surface's normal (tenant-scoped or
  admin) auth. The source URL + credential are request parameters, not
  server configuration — imports are operator-initiated, not standing
  links, in v1.

**v1 trust policy, restated from the roles doc: owned instances only.**
The transport authenticates the peer; it does not make hostile data
safe — §5's gates do the latter, imperfectly, which is why the policy
exists.

## 3. The import job

Import is an async job in the established shape (submit → `202` + id →
poll → result), managed per tenant:

```
POST /api/v2/import/fetch
{ "source_url": "...", "manifest_sha": "<64hex>", "auth": { ... } }
```

Sequence:

1. Fetch `manifest_sha` from the source (§1), hash-verify, parse under
   `BLOB_MANIFEST.md`. Malformed or hash-mismatched → job fails, no
   local state.
2. **Register the registry row as `staging`** — from this instant every
   member already present locally and every member subsequently fetched
   is GC-pinned (`BLOB_MANIFEST.md` §2.2), and the verifier reads
   absences as in-progress.
3. Fetch loop over members **not already present** (presence = content
   file exists for the SHA — dedup makes re-sync incremental
   structurally). Each fetch streams through a hasher; a digest
   mismatch discards the bytes and retries. Transfer client is
   `pkg/client` with its retry machinery; concurrency is a small fixed
   fan-out (default 4), each stream verified independently.
4. On completion: **promote the row to `active`** (single UPDATE). The
   job result carries the manifest SHA and counts.
5. Optional materialisation (§4) as a second step or a job flag.

**Status** (`GET /api/v2/import/{job_id}`): state, members
total/present/fetched/failed, bytes fetched, current retry budget.

**Resumability is idempotence, not machinery.** Re-submitting the same
`(source_url, manifest_sha)` finds the `staging` row, re-runs the
presence scan, and fetches only what is missing — a crashed or
interrupted job is resumed by running it again. `Range` on fetch-by-SHA
additionally allows a partial large member to resume mid-file (partial
temp retained per job; optional optimisation, not correctness).

**Abort** (`DELETE /api/v2/import/{job_id}`): stop fetching, delete the
`staging` row; fetched members lose their pin and age out through
normal GC. The 24 h staleness rule (`BLOB_MANIFEST.md` §4) catches
abandoned jobs that were never aborted.

## 4. Materialisation and read-only tenancy

What "opening" imported content means depends on the manifest kind:

- **`backup`** → do not materialise here; use `iolu backup restore`
  (`BLOB_BACKUP.md` §4). Import is the transport leg of cross-instance
  restore, nothing more.
- **`sqlite-snapshot`** → materialise the single member and open
  read-only (`ATTACH … ?immutable=1` or a read-only connection), gated
  by §5.
- **`pebble-checkpoint`** → materialise members at their relative paths
  into a fresh directory (a checkpoint *is* a valid Pebble directory)
  and open read-only, gated by §5.

Materialised store-kinds are served as a **read-only tenant**: a fresh
local tenant id chosen by the operator, populated from the manifest,
and refused all mutation. Minimal v1 mechanism:

- The `tenants` record gains a lifecycle status; the fleet document
  already establishes the pattern with `migrating`. Import adds
  `read_only` to that vocabulary — **an extension of the fleet
  lifecycle states, not a parallel invention**; the naming is part of
  the fleet reconciliation (`BLOB_EXTENDED_ROLES.md` §7, open q4).
- Enforcement is one middleware check: mutating verbs against a
  `read_only` tenant → 403 with a typed error. Handlers need no
  individual awareness. **The check applies to both listeners** — the
  S3 surface's mutating operations (`PutObject`, `DeleteObject`)
  refuse identically (S3-XML error rendering); a read-only tenant
  that is writable over S3 would be no such thing.
- The `tenants` status column is a schema-ladder step (the table is
  global and unprefixed; the ladder owns its shape), landing with the
  first consumer of a non-default status.
- Import jobs are in-memory in the async-query precedent's shape; the
  durable facts are the `staging` registry row and the fetched
  members. A restart mid-import loses the job and resumption is
  re-submission (§3) — by design, not accident.
- `iolu db check` runs against the materialised tenant before it is
  served — the standing acceptance-gate pattern.

## 5. Trust gates on materialisation

An imported database file is untrusted input even from an owned
instance (defence in depth):

- **SQLite:** open read-only always; run `PRAGMA integrity_check`
  (minimum `quick_check`) before first query; the connection avoids
  evaluating stored triggers/views where the access path allows.
- **Pebble:** the manifest's `pebble-format` header
  (`BLOB_MANIFEST.md` §1.1, optional for `pebble-checkpoint`) is
  checked against the local library's supported format range **before
  materialisation**; a newer format is refused with a typed error. A
  manifest without the header falls back to Pebble's own open-time
  format handling, read-only.
- Failures at this gate leave the fetched content pinned (`active`
  manifest) but nothing served — the operator decides whether to
  retire it.

## 6. Events, error codes, guard

- Events (named now per the settled dotted-subject conventions; emitted
  when the event plumbing makes it natural): `import.completed`,
  `import.failed`, `import.aborted`.
- Error codes (new family, registered typed errors): import
  job-not-found, manifest-fetch-failed, manifest-verify-failed,
  member-verify-failed (with retry exhaustion), source-auth-failed,
  pebble-format-unsupported, integrity-check-failed,
  import-in-progress (one job per `(tenant, manifest_sha)` at a time —
  409).
- **Dormant guard, registered at birth:** a two-instance round trip —
  export a populated store from instance A (checkpoint → manifest),
  import into instance B, materialise read-only, `iolu db check`
  green, and read-equality spot checks. Requires two processes and
  real networking; it lives in the dormant-guards table with its
  canonical invocation, per the working agreement.

## 7. Non-goals (v1)

Standing sync/subscription imports (import is operator-initiated,
one-shot, re-runnable); batch fetch endpoints (§1); import from
non-xolu S3 sources (the S3 listener is a serving surface, not a
generic S3 client); tenant-id remapping (shared with
`BLOB_BACKUP.md` §4.1); any write path into a read-only tenant short
of retiring and re-importing.

## 8. Hot mount — serving a tenant materialised at runtime

Can a running xolu bring a tenant online from a manifest — local or
remote — without a restart? **Yes, by composition, and more cheaply
than it sounds**, because of a property the codebase already has:
every per-tenant subsystem is **lazy-open** (blob manager, ts manager,
cal manager all open a tenant on first touch, with startup discovery
as just one caller), and runtime tenant provisioning already exists as
a pattern (`POST /ts/provision`). A directory materialised at runtime
is indistinguishable from a pre-existing tenant discovered late.

**Sequence** (target tenant id absent locally; id = the manifest's
original id — see constraint below):

1. Import per §3 (remote) or resolve locally (the manifest already in
   a local store) — either way the members are pinned by an `active`
   registry row before materialisation.
2. Set the tenant's lifecycle status to **`materialising`** — a
   sibling of `read_only`/`migrating` in the same status vocabulary.
   This is the race gate: without it, a request arriving
   mid-materialisation lazy-opens a half-written directory. All
   requests for the tenant refuse (404-shaped) until the flip.
3. Materialise per kind (§4); for `backup` kind, additionally run the
   derived-plane rebuilds (`RebuildFrom`, `RebuildRollup`) — the
   restore steps, executed hot.
4. Run the rebuild oracles in-process (the `iolu db check` set) as the
   acceptance gate.
5. Flip status to `read_only` (store-kinds; replicas) or active
   (backup kind mounted as a **writable fork** — replica promotion).
   The first request lazy-opens everything through the normal paths.

**Same-id constraint (verified, binds both modes):** per-file tenant
databases carry `t<XXXX>_*` table names exactly as shared mode does
("for consistency and to allow future consolidation" —
`pkg/storage/sqlite.go`), so mounting under a different id is the F6
rename campaign (`BLOB_OPEN_QUESTIONS.md` §4) in *both* modes. Hot
mount v1 is same-id, absent-locally.

**Boundary refinement against `BLOB_BACKUP.md` §4:** the HTTP-restore
prohibition was argued from the footgun of overwriting a *live*
tenant. Hot-mounting into an **absent** tenant carries no such hazard —
nothing exists to destroy, the `materialising` gate covers the race,
and the oracle gate covers correctness. The v1 line is therefore
sharpened, not contradicted: replace-in-place restore stays iolu-only;
mount-into-absence may be served hot (decision D14).

**Local fast path — hard-link materialisation.** The blob store and
the tenant directories share the base filesystem, and checkpoint
already hard-links on the way out; mounting can hard-link on the way
back in — instant, zero additional space. The safety rule is precise
and asymmetric:

- **SST members: hard-link always**, read-only or writable. Pebble
  never rewrites SST bytes in place — it creates new files and
  unlinks obsolete ones, and unlinking one name never disturbs the
  blob store's link (GC quarantine likewise renames only its own
  directory entry).
- **The SQLite member: hard-link only for read-only mounts**
  (`immutable=1`). A *writable* mount MUST copy it — SQLite writes in
  place, and a shared inode would mutate content-addressed blob
  content through the back door: silent corruption of the store's
  identity guarantee.
- Sidecars (`.ct`, `.md5`) are never linked into tenant directories.

**The lazy-mount idea (recorded, not designed — O7):** a read-only
tenant served *without* full materialisation, fetching member bytes on
demand — a SQLite VFS and a Pebble `vfs.FS` over fetch-by-SHA with
`Range`, coherent because members are immutable and cacheable forever.
Every primitive it needs (§1's endpoint, `BLOB_CONDITIONAL.md`'s
ranges, manifest-as-file-map, immutability) is already specified; the
lift is the two VFS shims and their failure modes mid-query. Captured
so it doesn't fade; deliberately not committed.
