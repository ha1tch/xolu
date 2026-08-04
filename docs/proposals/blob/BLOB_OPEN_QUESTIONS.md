# Blob Extended Roles — Open, Undecided, Uncertain, Impossible

Updated: 2026-07-22
Status: design-stage register for the BLOB_* specification set. This is
**not** a repository tracking document (per `TRACKING_PRACTICES.md`, no
new tracking-flavoured kinds without cause): while the design is
unexecuted, its unknowns live here in one place instead of scattered
through five files. If execution is decided, actionable items migrate
to the repository register (T-nn) and this file gains a reconciliation
banner; it does not become a second register.

Companions: `BLOB_EXTENDED_ROLES.md`, `BLOB_MANIFEST.md`,
`BLOB_BACKUP.md`, `BLOB_CONDITIONAL.md`, `BLOB_IMPORT.md`.

---

## 1. Undecided — decisions deliberately parked, each with a standing proposal

| # | Decision | Standing proposal | Decides when |
|---|---|---|---|
| D1 | Record blob `StoredAt` in alias members? | Omit — mtime is derived state, not authoritative | Stage 1 |
| D2 | Backfill self-aliases for pre-existing alias-less blobs? | One-shot repair sweep at startup; the alternative is permanently invisible content | Stage 0 |
| D3 | `staging` staleness threshold fixed (24 h) or configurable? | Fixed until someone needs otherwise | Stage 1 |
| D4 | Batch fetch endpoint? | Defer until a many-small-member workload is measured | Stage 2+ |
| D5 | Chunked-blob representation? | Defer until a consumer measures need; fixed 8 MiB if built | Stage 3+ |
| D6 | Metrics/observability beyond job status? | Conscious omission; add a metrics section only if wanted before execution | pre-Stage 1 |
| D7 | `/export` re-basing: keep the legacy zip layout (`manifest.json` field names, `entities.db`) byte-compatible, or version the archive? | Keep the wire contract, extend the manifest fields additively; examine `backup_restore_test.go` first | Stage 1 |
| D8 | JSON wire rendering of manifests (field names, member array shape) | Unspecified beyond "ordinary JSON"; shape it with the first `GET /backup/{sha}` implementation | Stage 1 |
| D9 | Error-code numeric allocation for the backup/import families | Named, not numbered — the registered-errors table in code is the allocator | per stage |
| D10 | iolu verb naming (`iolu backup capture\|list\|verify\|restore`, `iolu import …`) | As written; unratified | Stage 1/2 |
| D11 | Capture deadline vs large-ts cost (see U1) — raise the budget, add an mtime-keyed skip cache, or accept long captures with a bigger grace? | mtime+size→sha skip cache seeded from the previous backup manifest, with a `--verify` mode that re-hashes everything; budget stays GracePeriod/2 | Stage 1 |
| D12 | Event emission timing | Names fixed now (`backup.*`, `import.*` per settled conventions); wiring lands with the T-07/T-08 family, no dependency | events work |
| D13 | Read-only tenancy naming | `read_only` as an extension of the fleet lifecycle vocabulary; final name belongs to the fleet reconciliation (O2) | Stage 2 |
| D14 | Hot mount of `backup`-kind into an **absent** tenant over the running server (`BLOB_IMPORT.md` §8)? | Allow — the HTTP-restore footgun was about overwriting live tenants; absence + the `materialising` gate + the oracle gate removes it. Replace-in-place stays iolu-only | Stage 2 |

## 2. Uncertain — needs prototyping, measurement, or reading before trust

- **U1 — Capture I/O cost on large ts stores (discovered compiling this
  document).** `PutRaw` streams content into a temp file *before* the
  existence check, so ts capture **reads and writes every SST byte on
  every capture, even when fully deduplicated** — dedup saves storage,
  not capture I/O. On a large ts store this collides with the
  GracePeriod/2 deadline. D11 carries the candidate fix; until it is
  decided and measured, the deadline claim in `BLOB_BACKUP.md` §3.1 is
  **conditional on ts size**. (Transient disk is bounded — one temp
  file at a time — but the read+write+hash pass is total-size,
  per capture.)
- **U2 — Shared-mode logical export vs real `sqlite_master` DDL.** The
  copy-DDL-then-INSERT procedure is unprototyped against triggers that
  reference other tables, generated columns, FTS/virtual tables (if any
  ever appear in tenant table families), and DDL ordering constraints.
  Needs a prototype against a populated shared store before the
  procedure is trusted.
- **U3 — `PRAGMA integrity_check` runtime on large imported databases**
  can be minutes; the import materialisation gate may need
  `quick_check` default + `integrity_check` opt-in. Measure.
- **U4 — Offline-restore file locking.** `iolu backup restore` assumes
  a stopped/quiesced instance; nothing *enforces* that a server isn't
  holding the shared store. Whether to add an advisory lock (flock on
  the data root) or leave it operational discipline is unexamined.
- **U5 — `backup_restore_test.go`** exercises a restore flow against
  the legacy export zip; its exact behaviour is unread and it gates D7.
- **U6 — nolu `pkg/hotswap`** is unread; if it is instance-replacement
  machinery it bears on the Layer-A reconciliation (O2).
- **U7 — `FLEET_ARCHITECTURE.md` staleness**: dated 2026-02, predates
  nolu 0.7.x and current xolu; every claim this design takes from it is
  provisional until the reconciliation.
- **U8 — `If-Range` date-form-as-mismatch**: chosen for simplicity;
  whether any real client (S3 SDKs included) is surprised by
  always-full-body on date-form `If-Range` is untested.
- **U9 — Tunables**: lock-stripe count, manifest parse-cache size,
  import fetch fan-out (default 4) — all named with defaults, none
  measured.
- **U10 — Hard-link materialisation vs GC quarantine** (the fast path
  in `BLOB_IMPORT.md` §8): safety is argued from rename/unlink
  semantics (each name is an independent directory entry; quarantine
  renames only the blob store's own entry; Pebble unlinks, never
  rewrites) — reasoned, not tested. The mount round-trip guard should
  include a case where GC quarantines and purges a blob whose other
  hard link is live in a mounted tenant.

## 3. Impossible — impossible means impossible

No future work lifts these; they are theory or definition, not
missing machinery.

- **I1 — GC under partial knowledge cannot be safe.**
  Reachability-based deletion with incomplete reachability information
  is unsafe *by the definition of reachability*. Machinery changes how
  complete the knowledge is, never what incomplete knowledge licenses:
  a sweep against a possibly-partial live set converts uncertainty
  into deletion, always. Locally this is abort-on-any-mark-failure;
  distributed, GC does not run during partition.
- **I2 — Linearisable multi-writer alias updates *without
  coordination*.** Excluded by distributed-systems fundamentals.
  Layer-A/B machinery does not lift the impossibility — it removes
  the "without". The clause is the claim.
- **I3 — Hash-stable and lenient in the same manifest version.**
  Mutually exclusive by what canonical *means*: a tolerant reader is
  a licence for two implementations to hash one logical manifest
  differently. Extensibility arrives only as version 2, never as
  leniency. (Kin: deriving MD5 from SHA-256 or vice versa is likewise
  impossible — see F4 for what that does and does not foreclose.)

## 4. Foreclosed by construction — blocked, not impossible; each with its lifting condition

Consequences of deliberate current choices or machinery that does not
exist yet. Each names what would lift it.

- **F1 — A cross-plane backup instant** (was overclaimed as
  impossible). No transaction spans SQLite + Pebble-ts + the alias
  filesystem *today*, and the global write pause was rejected by
  choice — so per-plane-at-its-instant with bounded skew is the
  **current** ceiling, not a law. **Lifting condition:** write-time
  coordination — an epoch/barrier mechanism, or capture anchored to a
  commitment boundary. **dxp note (it is being built and will
  exist):** dxp provides atomic multi-promise *commitment* across
  SQL-resident guard state; it is not a snapshot barrier, and ts is
  not among its participants — so dxp landing does not by itself
  deliver this, but it is exactly the machinery family a future
  consistent cut could anchor to. Revisit when dxp lands.
- **F2 — In-place content mutation.** Foreclosed by the identity
  model: every guarantee (dedup, integrity-by-identity, GC,
  replication idempotence) rests on immutability. Lifting it means
  abandoning content-addressing — a different store, never proposed.
  CoW PATCH is the ceiling *of this store*, by choice.
- **F3 — Immediate hard-deletion of backup-pinned content.** Policy,
  stated as one: a backup that can silently lose members is not a
  backup. Erasure obligations are honoured by retiring every pinning
  manifest first — an operator workflow. **Lifting condition:** an
  automated cascade-retirement flow, if compliance pressure ever
  wants the workflow mechanised; the ordering (retire, then delete)
  is what is non-negotiable, not the manual step.
- **F4 — Validator interchange between listeners.** Both digests are
  already stored (SHA content path, `.md5` sidecar); accepting either
  validator on either surface is *buildable*. It is declined for
  surface consistency — each listener honours the validator it
  advertises. The only impossible kernel is digest conversion (I3
  kin); the feature is a choice.
- **F5 — Cross-backup dedup of the SQLite member is structurally
  poor.** `VACUUM INTO` reorders pages; fixed-granularity sharing
  across snapshots is minimal. A cost characteristic of the current
  representation. **Lifting condition:** content-defined chunking
  (D5), if measurement ever justifies it.
- **F6 — Tenant-id remap on restore/mount.** The `t<XXXX>` encoding
  is baked into table names in **both** tenancy modes; remap is a
  rename campaign across DDL, not a parameter. **Lifting condition:**
  Layer-A migration work, where the campaign would be built once,
  deliberately.

## 5. Obligations owed elsewhere — recorded here so nothing dangles

- **O1 — Register filings (other thread / repository register):**
  W-1 (content-addressed mode write-only over HTTP), W-2 (no
  conditionals/ranges), **W-3 (content-addressed + GC = silent data
  loss; defect-class)**, **W-4 (`/export` omits the ts and blob
  planes; data-loss-shaped)**. All verified byte-identical at public
  HEAD and the 0.16.19 checkpoint.
- **O2 — Fleet reconciliation** (`BLOB_EXTENDED_ROLES.md` §7):
  manifest as the Layer-A migration archive; read-only tenancy naming;
  checked against current xolu *and* nolu 0.7.x; read `pkg/hotswap`
  (U6) first.
- **O3 — nolu propose-back**: manifest-carried transfer history
  implementing advisory `history_from`, manifest SHA in the transfer
  record; `transfer-history` manifest kind joins the spec only if
  accepted.
- **O4 — Dormant guards, registered at birth when written:** the
  backup→wipe→restore→verify round trip (`BLOB_BACKUP.md` §4.5); the
  two-instance import round trip (`BLOB_IMPORT.md` §6). Both are
  long-running, both need canonical invocations in the dormant-guards
  table the day they exist.
- **O5 — Prototypes gating trust:** U2 (shared export) and U1/D11
  (capture-cost measurement) before Stage 1 is declared shippable.
- **O6 — Recorded idea, no work implied:** dxp-coordinated replica
  writes (`BLOB_EXTENDED_ROLES.md` §7) — revisit only when the DXP
  framework's distributed patterns are deliberately instantiated on
  the network.
- **O7 — Recorded idea, no work implied:** lazy mount
  (`BLOB_IMPORT.md` §8) — a read-only tenant served without full
  materialisation via SQLite-VFS / Pebble-`vfs.FS` shims over
  fetch-by-SHA + `Range`; every primitive it needs is already
  specified, the shims and their mid-query failure modes are not.
- **O8 — Recorded exploration:** blob time travel
  (`BLOB_TIMETRAVEL.md`) — alias-plane journal + snapshot manifests +
  replay-by-re-pointing, bal-isomorphic, with the T-51 lessons
  (no backdating by construction; snapshot oracle at birth) adopted
  up front. Carries the design's one genuine fork, decided before
  anything else if ever executed: **(A)** promote the alias plane to
  SQL (one-transaction journal+mutation, dxp participant for free,
  `.keys/` demoted to a derived cache with a rebuild oracle) versus
  **(B)** keep file aliases with a write-ahead intent journal.
  Standing lean: (A).
