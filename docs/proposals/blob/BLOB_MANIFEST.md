# Blob Manifests — Design Specification

Updated: 2026-07-22
Status: design — new documentation; describes nothing that is implemented
Companions: `BLOB_EXTENDED_ROLES.md` (roles overview), `BLOB_BACKUP.md`
(capture/restore specification)

A **manifest** is a content-addressed document listing a set of blob
references. It is the shared object underlying the extended blob roles
(backup, import, replication, chunked content). This file is the
normative specification: canonical form, identity, registry, GC-root
semantics, and verification.

---

## 1. Canonical form: line-oriented text

**Decision: line-oriented text, not canonical JSON.** Grounds:

- **House precedent.** The chronicle rebuild oracle already defines the
  house canonical style: one fact per line, sorted, so that the first
  differing line localises a divergence. Manifests inherit that
  property: two manifests differing in one member differ in one line.
- **No canonical-JSON emitter exists in-house.** `pkg/jsonic` is a
  tokeniser/extractor, not a canonical serialiser. Canonical JSON
  (key ordering, number formatting, escaping — RFC 8785 territory) is
  a well-known source of subtle cross-implementation hash mismatches,
  and taking a dependency to solve a problem a line format doesn't
  have is the wrong trade.
- **Byte-stability is trivial in a line format** and auditable by eye,
  diffable with standard tools.
- **The wire is a separate concern.** API responses render manifests as
  ordinary JSON; the canonical bytes are what gets hashed and stored.
  This is the same split bal uses (decimal strings on the wire,
  integers inside): representation at the boundary, canonical form at
  the core.

### 1.1 Grammar

A manifest is UTF-8 text, LF line endings only, no trailing whitespace
on any line, exactly one trailing LF at end of file.

```
manifest      = magic-line header-lines separator member-lines
magic-line    = "xolu-manifest " version LF          ; version = "1"
header-lines  = 1*( header-key SP header-value LF )
separator     = "." LF
member-lines  = 1*( member LF )
member        = name HT sha256 HT size HT content-type
```

**Header keys (version 1).** Required: `kind`, `tenant`, `created`
(RFC 3339 UTC), `xolu-version`. Optional: `note` (single line),
`schema-position` (ladder position at capture, backup kind only),
`source` (provenance URL or instance identifier; export kinds —
`pebble-checkpoint`, `sqlite-snapshot`), `pebble-format` (Pebble
format major version at checkpoint time; `pebble-checkpoint` kind
only — the import-side compatibility gate reads it, see
`BLOB_IMPORT.md` §5). Header lines appear in the order listed here;
unknown keys are forbidden in version 1 (readers reject, so that
version-2 additions cannot be silently mis-hashed by version-1
writers).

**Header values:** non-empty; no control characters (bytes < 0x20,
which excludes `HT` and `LF`); no leading or trailing whitespace.
`tenant` is the canonical decimal of the uint16 tenant id (no
padding — `7`, not `t0007`; the segment encoding is a filesystem
concern, not a manifest one).

**Member fields:**

| Field | Form | Rules |
|---|---|---|
| `name` | role or path | Non-empty; no control characters (bytes < 0x20), no `HT`, no `LF`. May contain `/` — names are manifest-internal labels, **not** blob keys, and are never used as filesystem paths by the store. |
| `sha256` | 64 lowercase hex | Same validation as `validateSHA256Hex` (D-004 boundary guard). |
| `size` | decimal int64 | Canonical decimal, no leading zeros, no sign. |
| `content-type` | token or `-` | `-` when unknown; no whitespace. |

**Ordering.** Members sorted by `name`, bytewise ascending. Duplicate
names are invalid. Duplicate SHAs across members are valid (two names
may reference identical content — dedup is the point).

**Size bounds.** A manifest is a blob and is subject to
`XOLU_BLOB_MAX_SIZE` like any other (64 MiB default ≈ several hundred
thousand member lines — no realistic backup or checkpoint approaches
it). No separate limit is defined.

### 1.2 Identity

The manifest's identity is the SHA-256 of its canonical bytes, i.e. its
blob content address. Manifests are stored via the content-addressed
path and never mutated; a "changed" manifest is a different manifest.
Consequences:

- Storing the same logical set twice yields the same manifest SHA — the
  idempotency is structural, not implemented.
- A manifest SHA is a tamper-evident fingerprint of the entire set:
  verify the manifest bytes against its SHA, then each member against
  its listed SHA, and the whole tree is proven.
- Parsing is cache-forever: a manifest SHA never maps to different
  content.

### 1.3 Kinds (version 1)

| Kind | Members are | Producer |
|---|---|---|
| `backup` | SQLite capture + ts Pebble checkpoint files + blob alias set + dynconfig extract — see `BLOB_BACKUP.md` §2 | backup capture |
| `pebble-checkpoint` | files of a standalone Pebble checkpoint (SSTs, MANIFEST, OPTIONS, CURRENT) | export for import/replication of external Pebble stores |
| `sqlite-snapshot` | a single snapshot/`VACUUM INTO` product | export for import |
| `chunked` | ordered chunks of one logical object | chunked-blob layer (deferred; see roles doc §5) |

New kinds are additive; readers ignore manifests of unknown kind rather
than rejecting them (a registry may legitimately hold kinds written by
a newer xolu).

### 1.4 The `tenant` header across instances

The `tenant` header records **provenance** — the tenant id at the
producing instance — and is never an addressing instruction. Rules:

- Same-instance operations (backup restore in v1) require it to match
  the target tenant (`BLOB_BACKUP.md` §4.1 — id remap is a v1
  non-goal, because shared-mode table names carry the encoding).
- Cross-instance import treats it as informational: the importing side
  chooses its own local tenant context, and a mismatch is recorded,
  not refused. When id remap becomes real work (role 3 era), this
  header is the fact remapping starts from.

---

## 2. Registry

**Decision: retention facts live in the tenant's SQLite; content facts
live in the manifest blobs.** The two kinds of fact separate cleanly:

- The manifest blob is already self-describing, immutable, and
  replicable — it *is* the export/replication unit. Mirroring the
  registry as a blob adds nothing to it.
- The only authoritative mutable fact is *which manifests this tenant
  retains, and in what lifecycle state*. That is a row, and it belongs
  in SQLite: transactional, queryable, and — decisively — **captured by
  the tenant's own SQLite plane**, so a backup records which backups
  existed, and restores compose.

### 2.1 Schema

Per-tenant table via `tenant.TablePrefix`:

```sql
CREATE TABLE {prefix}blob_manifests (
    manifest_sha  TEXT PRIMARY KEY,   -- 64 lowercase hex
    kind          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active','staging')),
    created_at    TEXT NOT NULL,      -- RFC 3339 UTC
    member_count  INTEGER NOT NULL,
    total_bytes   INTEGER NOT NULL,
    note          TEXT
);
```

Members are deliberately **not** duplicated into SQL. The row pins the
root; the manifest pins the members. Enumeration of members = fetch the
manifest blob (content-addressed, cacheable forever) and parse.

### 2.2 Lifecycle and the `staging` state

`status` resolves the import/GC/verifier three-way interaction that a
single-state registry cannot express:

- **`active`** — the manifest's members are asserted present. Producers
  that create members locally before registering (backup capture)
  insert directly as `active`; the capture deadline
  (`BLOB_BACKUP.md` §3.1) guarantees members outlive the gap.
- **`staging`** — the manifest is being materialised: members are being
  fetched and are *expected* to be partially absent. Import inserts the
  row as `staging` **immediately after fetching and verifying the
  manifest blob itself, before fetching any member** — from that moment
  every already-fetched member is GC-pinned, closing the
  fetched-but-unregistered data-loss window (the W-3 pattern) without
  making the verifier scream about members still in flight.
  On completion: promote to `active` (single UPDATE). On abort: delete
  the row; fetched members age out through normal GC.

**Register** = insert row (as part of the producing operation, never
deferred). **Retire** = delete row; the manifest blob and any members
no longer referenced elsewhere become GC-eligible and age out through
the normal quarantine. Backup retention is *only* row deletion; no
bespoke deletion machinery exists or is needed.

---

## 3. GC-root semantics

The registry is a `SHARefSource`. Its `CollectLiveSHAs` is two-level
and **status-blind** (both `active` and `staging` rows pin — staging
exists precisely to pin in-flight members):

1. Read all `manifest_sha` values from the registry → each is live.
2. Fetch and parse each manifest (immutable ⇒ parse-once cache keyed by
   SHA) → every member SHA is live. A `staging` row whose manifest blob
   is itself not yet fetched contributes only the manifest SHA — which
   is the correct pin for that instant.

Cost per sweep: O(registered manifests) small cached reads, consistent
with the existing mark phase's O(key count) budget. The parse cache is
LRU-bounded (manifests are small; a few hundred cached parses cover any
realistic tenant), keyed by SHA — immutability makes invalidation a
non-concept.

**Wiring topology (currently absent, must be specified).** The
registry source needs the tenant's `*sql.DB` from inside the GC
goroutine, but `blobManager` is constructed with no storage access at
all — the dependency does not exist today and its direction matters.
Resolution: the **server** (which owns both the storage layer and the
blob manager) constructs the per-tenant registry source with the
tenant's DB handle and the tenant's blob `Store`, assembles the
`MultiSHARefSource` (registry source + any future sources), and
injects it into the manager at construction — replacing today's
hard-coded `nil` at the `NewGCWorker` call. `pkg/blob` continues to
know nothing about SQL; the composite is assembled where both halves
are already visible. Registry reads from the GC goroutine use the
same pooled `*sql.DB` as request traffic (SQLite WAL read concurrency
covers it); a read failure is a mark-phase failure and aborts the
sweep per this section.

**Schema-ladder placement.** The `{prefix}blob_manifests` table joins
the schema ladder as the next step at implementation time and the
fresh-database base schema, in the S13 pattern (legacy detection,
idempotent re-init). It is not created ad hoc by feature code.

**Composite source (required change).** The GC worker takes exactly one
`extRefs SHARefSource`, currently wired to `nil`. Introduce a composite:

```go
// MultiSHARefSource fans in several sources. If ANY source fails,
// CollectLiveSHAs fails: a partial external live set must abort the
// sweep, never shrink it.
type MultiSHARefSource struct { sources []SHARefSource }
```

**Fail-safe hardening (required change).** The current sweep, on an
`extRefs` error, logs and proceeds with an empty external live set —
while the same function aborts on a `.keys/` read failure with the
comment that an untrustworthy live set must not be swept against. Once
manifests are GC roots this asymmetry is a data-loss path: a transient
registry error would quarantine every manifest-pinned blob. The rule
must become uniform: **any mark-phase failure aborts the sweep** —
including the purge of already-expired quarantine entries, whose
resurrection check needs the same live set. Quarantine still bounds the
damage of a missed abort, but the abort is the invariant; quarantine is
the backstop.

---

## 4. Verification

`blob.manifest` verifier, wired into `iolu db check` beside `bal.fold`
and `bal.chain`, per tenant where the registry table exists. Rules are
status-aware:

For every registry row:
1. the manifest blob resolves and its bytes hash to `manifest_sha`;
   for `staging` rows whose manifest is not yet present, this is
   reported as in-progress, not divergence — unless the row is stale
   (rule 4);
2. it parses under this specification, and `member_count`/`total_bytes`
   agree with the parsed members (row-vs-content consistency);
3. **`active` rows:** every member SHA is present on disk — in a shard
   directory or in `.gc-pending/` (quarantined-but-live is a detectable
   in-between state the next sweep will resurrect); missing outright is
   a divergence. **`staging` rows:** absent members are expected and
   reported as in-progress counts only;
4. a `staging` row older than a staleness threshold (default: 24 h) is
   a divergence — an abandoned import that should have been promoted or
   deleted.

Divergences are results, not errors, in the established oracle shape:
one broken manifest must not hide another.

---

## 5. Relation to the meta subject model

If manifests ever need annotations, `blob.manifest` registers as a
dotted subject kind under @C04c (opaque key = the manifest SHA). Nothing
in this specification reads meta, and no guard reads any of this —
manifests are derived-plane artefacts throughout.
