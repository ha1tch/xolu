# Extended Roles for the Blob Primitive — Design

Updated: 2026-07-22
Status: design — new documentation; nothing here is committed work.
Companions: `BLOB_MANIFEST.md` (normative manifest specification),
`BLOB_BACKUP.md` (capture/restore specification).

## 1. Motivation

The blob store's guarantee structure — immutable content files, atomic
writes, structural deduplication, SHA-256 as identity, the alias as the
only mutable cell — is the substrate that backup, replication, and
import mechanisms want. Four candidate roles, as originally sketched:

1. **Backups** produced by the engine, exposed through established APIs.
2. **Extended file handling** — opt-in file-like operations and REST
   patching over blobs.
3. **Sharding and replication**, coordinated in future via nolu.
4. **Import** — reading remote SQLite and Pebble stores exposed as blobs.

The sketch made 3 conditional on 2, and 4 conditional on 2-or-3.
Elaboration shows a flatter structure: all four are consumers of one
missing object — the **manifest** (see companion spec) — and import
requires neither 2 nor 3.

## 2. Findings against the current implementation (0.16.19)

Four gaps surfaced while grounding this design. None is recorded in
TRACKING or KNOWN_ISSUES as of 0.16.19. W-3 is defect-class and W-4 is
a data-loss-shaped feature gap; both stand regardless of whether this
design proceeds.

- **W-1 — content-addressed mode is write-only over HTTP.**
  `POST /blob` without a key calls `PutRaw` (no alias) and returns the
  SHA as the response `key`; `GET /blob/{key}` resolves only through
  `.keys/`, so that SHA 404s. `Store.GetBySHA` exists and is wired to
  no endpoint. The API hands back an identifier it cannot serve.

- **W-2 — no conditional requests, no ranges.** The handlers implement
  no `If-Match` / `If-None-Match` / `Range`. `ETag` is already the
  content SHA, so `If-Match` is a content-level compare-and-swap on the
  alias at near-zero design cost, and `Range` is the cheap half of
  file-like reads. (S3 clients also expect `Range` on GetObject; its
  absence is a latent S3-compat gap.)

- **W-3 — content-addressed mode + GC is silent data loss.** The only
  production caller of `PutRaw` is the content-addressed POST handler;
  the GC worker is constructed with `extRefs = nil`
  (`blob_manager.go`); the mark phase reads `.keys/` only. A
  content-addressed blob therefore contributes nothing to the live set
  and, with `XOLU_BLOB_GC_ENABLED=true`, is quarantined and
  hard-deleted within ~(Interval + GracePeriod) — ~70 minutes at
  defaults. GC defaults off, which is why this has drawn no blood, but
  the configuration combination is legal and undocumented as lethal.
  Related asymmetry: a `SHARefSource` error causes the sweep to proceed
  with an **empty** external live set, while a `.keys/` read failure
  aborts — the mark-integrity principle stated in the code's own
  comment is applied to one source and not the other.

- **W-4 — the existing export omits two authoritative planes.**
  `GET /api/v1/export` (`handleExport`, `pkg/server/handlers.go`)
  archives the SQLite file (after a WAL checkpoint) plus graph JSON —
  and nothing else. It predates the ts, cal and blob planes: an
  export→restore round trip today silently loses every timeseries
  point and the entire blob namespace. `EXPORT_API.md` describes the
  SQLite-only behaviour accurately, but nothing warns that it is no
  longer a full-fidelity export. (`backup_restore_test.go` exercises
  a restore flow against this format and must be examined when
  Stage 1 begins.)

**Proposed W-3/W-1 fix (one change closes both):** content-addressed
POST writes a **self-alias** — `Put` with the SHA as the key — instead
of `PutRaw`. The digest is a valid key under `validateKey` (64 hex
chars, no separators, no leading dot). Consequences: the blob is
GC-live through the ordinary mark phase (W-3 closed with no GC change);
`GET /blob/{sha}` works through the existing resolve path (W-1's wire
gap closed with no new endpoint); the blob appears in `List` (a
behaviour change to document — arguably a correction, since invisible
undeletable content was the alternative).

Collision note, for the record: a pre-existing *user* alias whose name
happens to be 64 hex characters is overwritten by a self-alias only if
newly stored content hashes to exactly that name — i.e. only under a
SHA-256 preimage, which is infeasible; and where the user chose the
name *because* it is that content's digest, the overwrite is
idempotent-correct. Safe, but stated rather than discovered.

A dedicated GET-by-SHA endpoint remains worthwhile for role 4 (fetch
*any* content by address regardless of alias), but is no longer the
urgent fix. `PutRaw` remains correct for internal producers that pin
through the manifest registry.

## 3. What already exists — the reuse map

This design invents as little as possible. The pieces it builds on,
and what each closes:

| Existing asset | Where | What it closes here |
|---|---|---|
| **Fleet architecture** | `FLEET_ARCHITECTURE.md` (0.2.2-draft, twice-reviewed) | Role 3's placement, migration lifecycle, split-brain (placement epochs), gateway, and internal trust boundary are *already designed there*. Role 3 in this document is a complement, not a parallel design — see §7. |
| **xotogen** | `cmd/xotogen` | Import peer authentication: the importing instance presents an xotogen-minted, tenant-scoped JWT validated by the same `authmw` middleware the server already uses. The regression tests binding xotogen output to the server validator are the existing guarantee. No new credential system. |
| **Async-job pattern** | `ASYNC_QUERIES.md`; per-tenant sulpher job managers | Backup capture (minutes-long, deadline-bounded) is an async job in the established submit→202+id→poll→result shape, not a synchronous POST. |
| **Export API** | `GET /api/v1/export`, `EXPORT_API.md` | The ancestor of the export-archive tier. Future: `/export` becomes a *rendering of a backup manifest* as a zip stream (manifest-backed), rather than a second capture mechanism — which also closes W-4, since the manifest covers all authoritative planes. |
| **Event model** | `EVENT_MODEL.md` (shipped 1.0), `NOLU_EVENTS.md` settled naming conventions | Lifecycle notifications (`backup.captured`, `import.completed`, …) are named now per the settled dotted-subject conventions and emitted through the existing event model — no bespoke notification machinery. Emission wires in when the relevant event plumbing (T-07/T-08 family) makes it natural; nothing here depends on it. |
| **pkg/client** | Go client library (incl. `retry.go`) | The import fetch client extends `pkg/client` — its retry machinery is exactly what bulk SHA fetching wants — rather than fresh HTTP code. |
| **queryfy** | `github.com/ha1tch/queryfy` | Candidate at the JSON request-validation boundary of the new v2 surfaces and iolu tooling. Deliberately *not* used for manifest parsing: the canonical line format wants the in-tree D-004-style validators, and xolu core already carries `pkg/validation`. |
| **Rebuild oracles / iolu db check** | `pkg/chronicle`, `iolu` | Restore verification and the `blob.manifest` verifier ride the existing oracle machinery; restore's acceptance gate is `iolu db check` green. |
| **nolu** | `github.com/ha1tch/nolu` (read at 0.7.9, 2026-05) | A **federated entity registry**: GlobalID ↔ LocalRef mapping across sovereign instances, a bilateral transfer-negotiation protocol with history portability, an event bus (memory/NATS), and a subscription router. It explicitly does **not** orchestrate instances, do placement, or replicate data — so "coordination via nolu" means federation-layer coordination, not fleet orchestration (see §7 for the two-layer split, and the transfer/manifest synthesis). `NOLU_EVENTS.md`'s consistency-not-conformance principle governs all new subject naming. Version-skew note: nolu 0.7.9 pins xolu `v0.9.7-patched98`; every reconciliation below must account for nolu's xolu client being far behind 0.16.x. |

## 4. The manifest object

Specified normatively in `BLOB_MANIFEST.md`. Summary of what it gives
each role: a canonical, content-addressed, immutable listing of
`(name, sha, size, content-type)` members; itself a blob (tamper-evident
fingerprint of the whole set, parse-once-cache-forever); a GC root via
the registry's `SHARefSource`, with an `active`/`staging` lifecycle
that pins in-flight imports; and the unit of set reconciliation
(manifest exchange = incremental sync). It is the house two-identity
pattern (external mutable name, internal immutable identity) extended
from single objects to sets, and its retention model is a single SQLite
row per manifest — deleting the row is the entire deletion story.

## 5. Role 1 — Backups

Fully specified in `BLOB_BACKUP.md`; grounded in a plane-by-plane audit
of the tree. The decisions, summarised:

- **The backup set is exactly the authoritative planes.** Audited
  inventory: the SQLite primary store (authoritative), the ts Pebble
  store (authoritative — the points live there), the blob alias set
  (authoritative), the tenant's dynconfig namespace. The cal Pebble
  occupancy index is **excluded**: `pkg/cal/cal.go` declares it a
  derived, rebuildable bitmap index with `index == rebuild` as its
  acceptance gate; restore rebuilds it.
- **New primitives cannot silently rot the backup set.** A
  plane-coverage rule (prefixed-table primitives are captured
  automatically — bal today, dxp by its own proposal) plus an
  unknown-role guard that fails capture loudly on unrecognised
  storelayout roles make the W-4 failure mode structurally impossible
  (`BLOB_BACKUP.md` §1.1).
- **Blob content is never copied** — it is referenced by the manifest,
  already content-addressed in place; successive backups share every
  unchanged byte (including unchanged ts SSTs) structurally. Every
  backup is logically full; incremental machinery is unnecessary by
  construction.
- **Shared-tenancy mode forks the SQLite capture** — `VACUUM INTO`
  would leak every tenant's tables — and the fork is resolved as a
  logical per-tenant export (one read transaction, `tXXXX_*` DDL+rows
  into a fresh database), converging both modes on one backup format.
- **Capture is an async job** (reuse map §3), ordered and
  deadline-bounded (GracePeriod/2) so that registry registration always
  lands inside the GC grace window; no write pause is needed, and the
  residual cross-plane skew maps onto already-tolerated orphan classes.
- **Restore is iolu-first**, same-tenant-id in v1, with derived-plane
  rebuild as an explicit step and **`iolu db check` green as the
  acceptance gate**.
- A backup→wipe→restore→verify round trip is a dormant guard,
  registered the day it is written.

Surface, error codes, failure modes and non-goals: `BLOB_BACKUP.md`
§5–6. One conventions note for the record: capture's id is
content-derived — it satisfies `def`'s idempotency but not its
caller-names-the-thing property, so the surface uses verbs (`capture`),
never `def`.

## 6. Role 2 — File-like operations and REST patching

**The discipline:** content files are never mutated. Every edit is
copy-on-write — read, apply, write new content (new SHA), re-point the
alias under `If-Match` CAS (W-2). Old content ages out through the
existing quarantine. This preserves atomicity, dedup, GC correctness
and integrity-by-identity; the alias remains the only mutable cell.

- `PATCH /blob/{key}` (append or byte-range) → server-side CoW;
  `If-Match: "<sha>"` required, `412` on mismatch. Lost updates become
  impossible rather than unlikely.
- `Range` on GET → stateless partial reads.
- **CAS mechanics:** honouring `If-Match` needs an actual
  compare-and-swap on the alias; today `Put` overwrites
  unconditionally, and read-compare-`atomicWrite` has a window. A
  per-key mutex in the (single-instance, per-tenant) store closes it
  locally; the distributed form of the question belongs to the fleet
  design's alias-plane territory.

The full wire contract (verb/precondition matrix, Range grammar, PATCH
representation, size/quota interaction, S3 conditional parity, mutex
lifetime) is the next specification file: `BLOB_CONDITIONAL.md`.

**Out of scope, explicitly:** true POSIX semantics — file handles,
in-place partial writes, seek state, mmap. Stateful, anti-REST, and
incompatible with content-addressing.

**Directories/paths:** keys deliberately cannot contain `/` — a
path-traversal guard, since keys map to filenames. Do not relax it. A
hierarchical namespace, if ever wanted, is a metadata index (a small
SQLite table `path → sha`) over the object store — the git
object-store/index split — which would incidentally fix `List`'s
O(all-keys) filesystem scan. Severable; nothing else here needs it.

**Chunking: deferred, with evidence.** The candidate large objects each
turn out not to need it:

- **Pebble SSTs are already chunks.** An LSM's files are immutable and
  bounded; the storage engine has already done the chunking, and
  manifests capture it directly (this is now load-bearing for backup,
  since ts is Pebble-backed).
- **SQLite snapshots dedup poorly at fixed-chunk granularity** across
  successive captures (page reordering shifts content), so fixed
  chunking buys little; content-defined chunking would help but is
  exactly the complexity this decision avoids buying unmeasured.
- **User blobs are capped** at `XOLU_BLOB_MAX_SIZE` (default 64 MiB) —
  the store's own ceiling says "modest objects".

So the chunked-blob representation (`kind: chunked`) is specified as a
manifest kind, reserved, and **built only when a consumer measures a
need** — most plausibly large-file PATCH economics. If built: fixed
8 MiB chunks (above S3's 5 MiB multipart floor, tiny manifests);
content-defined chunking only if measured churn justifies it.

## 7. Role 3 — the content plane under two coordination layers

The original sketch said "coordinated via nolu". Reading both designs
(rather than remembering them) shows role 3's territory is split across
**two distinct coordination layers solving different problems**, and
this document designs neither — it supplies the content plane beneath
both:

**Layer A — single-operator fleet** (`FLEET_ARCHITECTURE.md`,
0.2.2-draft, twice-reviewed, dated 2026-02): tenant placement and
epochs, the `migrating` lifecycle state with SLA and
tombstone/cool-down, gateway discovery and routing, the internal
mTLS/token trust boundary, per-tenant durability. This is one
operator's fleet of xolu instances. Caveat recorded: the document is
five months old and predates both nolu 0.7.x and current xolu; its
reconciliation (below) must check it against both.

**Layer B — cross-boundary federation** (nolu, read at 0.7.9): nolu is
a federated *entity registry* — GlobalID identity, bilateral transfer
negotiation, events, subscriptions — across **sovereign** instances,
and by declared principle it does not orchestrate, place, or replicate.
Nothing nolu does is sharding; "replicas coordinated via nolu" was a
misremembering of its role.

What this document contributes to both layers:

- **The content plane as substrate.** Content-addressed replication is
  idempotent and self-verifying (fetch SHA, verify, done); anti-entropy
  is manifest exchange; the `{xx}` shard layout is a natural 256-bucket
  hash ring if content-level sharding is ever wanted (a Layer-A
  concern, if ever). These properties make fleet-level tenant migration
  cheap to build correctly.
- **The manifest as the migration/transfer payload — the synthesis.**
  Layer A weighs migration archive-format trade-offs (with a SQLite
  `ATTACH` escape hatch); the backup manifest is a concrete candidate
  answer. Layer B is stronger still: nolu's transfer protocol
  negotiates history portability (`HistoryOffer`/`HistorySpec`) but
  **explicitly leaves the data movement to the application layer** —
  `history_from` is "advisory metadata" in nolu's own API document.
  The manifest + import path (§8) is that application layer: the
  source xolu materialises the negotiated history window as a manifest
  (entity snapshot + event-stream extract), the **manifest SHA rides
  in the nolu transfer record as a tamper-evident receipt** of exactly
  what travelled, and the receiving instance fetches it via the
  role-4 path under xotogen auth. nolu never touches the bytes —
  perfectly consistent with its no-data-replication principle. This is
  a proposal to raise with the nolu side (in the `NOLU_EVENTS.md`
  spirit of changes proposed back), recorded here, not silently
  assumed.
- **The distributed-GC constraint, named early.** Any multi-node
  arrangement of blob stores must mark from the union of every
  participant's alias plane and manifest registry, with quarantine far
  exceeding transfer/replication lag, and must *not run* when the
  union is unobtainable. Deletion is the only operation that destroys
  data; it gets the conservative default. The single-node fail-safe
  abort (`BLOB_MANIFEST.md` §3) is the local seed of this principle,
  and both layers inherit it as a constraint, not a mechanism built
  here.

**Recorded idea — dxp-coordinated replica writes (captured 2026-07-22,
deliberately not designed).** A third possible future: dxp as the
commitment mechanism for replicated writes — update two remote shards
in one composed commit, each remote a promise-bearing participant
whose guard is its own alias CAS. This is *not* the shipped dxp
instantiation (whose one-transaction claim, @D06, is single-database
by construction); it would be a **network instantiation of the DXP
framework's own distributed patterns** (the 2PS/3PS phase spectrum,
which the proposal states lives with proofs in the framework
repository). Two observations pinned with the idea so they aren't
re-derived later: (a) content-addressing makes the staged-write phase
of a distributed commit cheap and idempotent — the heavy bytes travel
by SHA outside the commit window (the import transport,
`BLOB_IMPORT.md`), and the commit itself reduces to small alias-CAS
promotes per replica (`BLOB_CONDITIONAL.md` §7's division of labour,
applied remotely); (b) dxp's deadline-bounded reservations with
idempotent release map directly onto the timeout-recovery shape
distributed commit needs. The classic hazards (coordinator failure,
blocking, availability trade-offs) are exactly why this stays an
idea until the framework's network patterns are deliberately
instantiated — embryo stage acknowledged by its author.

## 8. Role 4 — Import of remote SQLite and Pebble stores

**Revised dependency claim:** import needs the manifest object plus
remote fetch-by-SHA — the *client half* of the content plane — and none
of the fleet machinery. Usable point-to-point long before any of role 3
lands.

**Pebble maps almost one-to-one.** A Pebble checkpoint is a consistent
hard-linked set of immutable files — i.e. a blob manifest waiting to be
written down (`kind: pebble-checkpoint`). Export: checkpoint →
content-addressed store of each file → manifest. Import: fetch
manifest → **register it as `staging`** (which GC-pins every member
from that moment — the staging lifecycle in `BLOB_MANIFEST.md` §2.2
resolves the fetched-but-unregistered data-loss window and the
verifier's missing-member semantics in one mechanism) → fetch only
SHAs not already present (dedup makes sync incremental structurally —
unchanged SSTs never travel) → promote to `active` → materialise the
directory → open read-only. Successive syncs of a live store transfer
only newly flushed or compacted SSTs.

**SQLite is the single-file case** (`kind: sqlite-snapshot`): fetch,
verify SHA, open read-only (`ATTACH … ?immutable=1` or a read-only
connection).

**Authentication: resolved by reuse.** The importing instance
authenticates with an xotogen-minted JWT scoped to the source tenant,
validated by the source's existing `authmw` — the exact guarantee
`cmd/xotogen`'s regression tests already bind. Where the fleet's
internal trust boundary (mTLS/token) is deployed, import rides inside
it. No dedicated peer-credential scheme.

**Transport: resolved by reuse.** The fetch client extends
`pkg/client` (whose retry machinery is what bulk SHA fetching wants).
The remote surface is the blob GET path — self-aliased content (W-1
fix) or the dedicated GET-by-SHA endpoint; the S3-compatible listener
is a second, already-existing wire for the same bytes where that suits
the deployment.

**Trust boundary — do not skip.** An imported database file is
untrusted input; SQLite has a history of parsing CVEs against hostile
files. v1 policy, stated out loud: **owned instances only.**
Mitigations regardless: open read-only always; `PRAGMA integrity_check`
(at least `quick_check`) before first query; never evaluate
triggers/views from imported files where avoidable; gate Pebble
manifests on a format version the local library can read.

**What this buys:** read replicas without coordination, tenant
migration between instances (compose with restore: import, then
restore — and the Layer-A reconciliation, §7), node seeding,
cross-instance ETL, and **the data plane for nolu entity transfers**:
when a nolu-negotiated transfer completes, the receiving instance
imports the source-materialised history manifest referenced in the
transfer record (§7) — each a pull-based, verifiable, incremental
operation.

**Remaining for `BLOB_IMPORT.md` (next specification file), now
narrowed by the reuse map:** the fetch endpoint shape and batching;
resumability semantics over the `staging` state; and the **read-only
tenant** concept — which should be specified as an extension of the
fleet document's tenant lifecycle-state vocabulary (`migrating` is
already such a state), not as a blob-local invention.

## 9. Surface placement

Split by the nature of the change, not by novelty:

- **v1:** W-1/W-3's self-alias fix and W-2's `If-Match`/`Range` are
  corrections and standard-HTTP additions to the existing surface —
  non-breaking, and W-3 should not wait for a v2 programme slot. The
  S3 listener picks up `Range` in the same motion.
- **v2:** the new roles are v2 primitives under the established path
  conventions — `backup` per `BLOB_BACKUP.md` §6 (async-job shape);
  import as verbs (`POST /api/v2/import/…`) shaped in
  `BLOB_IMPORT.md`.
- `/api/v1/export` is retained and eventually re-based onto manifests
  (reuse map §3), closing W-4 without a breaking change to its wire
  contract.

## 10. Staging

```
Stage 0  v1 corrections (small, independently shippable, W-3 first)
         self-alias fix for content-addressed POST  (closes W-3 + W-1 wire gap)
         mark-phase fail-safe: any ref-source error aborts the sweep
         If-Match CAS + Range on GET (HTTP and S3)  — spec: BLOB_CONDITIONAL.md
         W-4: at minimum, document the export fidelity gap; full closure
         arrives with Stage 1's manifest-backed /export

Stage 1  Manifest spec + registry (with staging lifecycle) +
         MultiSHARefSource + blob.manifest verifier in iolu db check;
         ts Checkpointer seam; then Role 1 per BLOB_BACKUP.md
         (async capture, list, retire, iolu restore, round-trip guard);
         /export re-based onto manifests (closes W-4)

Stage 2  Role 4: import per BLOB_IMPORT.md — Pebble checkpoint
         export/import, SQLite snapshot import, read-only tenants
         (needs Stage 1 + fetch-by-SHA only; auth/transport by reuse)

Stage 3  Role 2 remainder: PATCH/append CoW under If-Match; path-index
         facade only if wanted; chunking stays deferred-until-measured

Stage 4  Role 3 territory: Layer A under FLEET_ARCHITECTURE.md (with
         the migration-archive reconciliation raised there); Layer B
         as the nolu transfer-history proposal (§7, open q5);
         distributed GC constraint carried into both as stated in §7
```

Deliberate reorderings versus the original sketch: import (4) before
generalised file handling (2), because it needs less and pays sooner;
chunking demoted out of the critical path entirely, on the evidence in
§6; replication (3) ceded to the fleet design rather than staged here.

## 11. Invariants preserved

- **Guard locality (@C04a):** untouched. Blob stays off the
  authoritative planes; backups, manifests, replicas and imports are
  derived artefacts. No guard reads any of this.
- **Content immutability:** every proposed write path is CoW; no
  operation mutates a content file in place.
- **Key rules:** unchanged. Dot-prefixes stay reserved; `/` stays
  forbidden (manifest member names are labels, not keys; paths, if
  ever, live in an index).
- **GC model:** extended through the existing `SHARefSource` seam and
  hardened (fail-safe abort), not restructured. Quarantine semantics
  unchanged; the backstop role of quarantine is made explicit.
- **Two-identity pattern:** alias/SHA is the same shape as
  `account_id`/`AccountKey` and `CalendarID`/`CalOrdinal`; manifests
  extend it to sets rather than introducing a third identity scheme.
- **Verification-first:** every new stateful artefact arrives with its
  verifier (`blob.manifest` in `iolu db check`; restore gated on the
  oracles; the round-trip dormant guard registered at birth).
- **Reuse-first:** each role names the existing asset it builds on
  (§3) and the one reconciliation it owes (fleet migration archive)
  rather than shipping a parallel mechanism.

## 12. Remaining open questions

Consolidated into **`BLOB_OPEN_QUESTIONS.md`** — one design-stage
register of everything undecided (D1–D14, each with a standing
proposal), uncertain (U1–U10, needing prototype, measurement, or
reading), **impossible** (I1–I3: theory and definition, which no
future work lifts), **foreclosed by construction** (F1–F6: blocked by
current choices or absent machinery, each with its lifting condition —
notably F1, the cross-plane instant, which dxp-family machinery could
one day anchor), and owed elsewhere (O1–O7: the W-1…W-4 register
filings, the fleet and nolu reconciliations, the dormant-guard
obligations, the gating prototypes, and the recorded ideas). Items
formerly listed here moved there verbatim or sharpened; on execution,
actionable entries migrate to the repository register per
`TRACKING_PRACTICES.md` and that file gains a reconciliation banner.

## 13. Specification map

| File | Covers | Status |
|---|---|---|
| `BLOB_EXTENDED_ROLES.md` | roles overview, findings, reuse map, staging, invariants | this file |
| `BLOB_MANIFEST.md` | manifest canonical form, registry + lifecycle, GC roots, verifier | written |
| `BLOB_BACKUP.md` | tenant plane audit, capture, restore, forward-compatibility guard, failure modes | written |
| `BLOB_CONDITIONAL.md` | W-2 wire contract: ETag/precondition matrix, CAS mechanics, Range, PATCH, S3 parity, dxp promote seam | written |
| `BLOB_IMPORT.md` | fetch-by-SHA surface, import job + resumability, read-only tenancy, trust gates | written |
| `BLOB_OPEN_QUESTIONS.md` | consolidated undecided / uncertain / impossible / foreclosed / owed (D, U, I, F, O items) | written |
| `BLOB_TIMETRAVEL.md` | exploration: alias-plane journal, snapshots, replayable images over a linked timeseries (bal-isomorphic) | exploration |
