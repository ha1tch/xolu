# The Blob Extensions Corpus — Index

Updated: 2026-07-22
Status: design corpus — none of this is committed work. Produced in a
single design session against the xolu 0.16.19 checkpoint, with the
public xolu HEAD (0.16.16), nolu (0.7.9), `FLEET_ARCHITECTURE.md`
(2026-02), and the dxp proposal read for reconciliation. No existing
repository documentation was modified; every file here is new.

## Introduction

This corpus elaborates a single morning idea — that the /blob
primitive is an obvious candidate for backup, extended file handling,
replication participation, and remote import — into a specification
set grounded in the tree as it actually is. Along the way it found
four real gaps in the current implementation (one of them a silent
data-loss configuration), reconciled the design against three external
sources rather than memory, and pushed past the original four ideas
into hot mounting and time travel. Everything actionable, uncertain,
or impossible is registered in one place so nothing here can quietly
rot the way the export API did.

## The documents

| File | Describes |
|---|---|
| `BLOB_EXTENDED_ROLES.md` | The roles overview and entry point: the four extended roles (backup, file-like operations, replication territory, import), the four codebase findings W-1–W-4, the reuse map (fleet, xotogen, async jobs, export API, events, pkg/client, queryfy, nolu), the two-layer coordination split with the nolu transfer-history synthesis, the recorded dxp replica-write idea, staging, invariants, and the specification map. |
| `BLOB_MANIFEST.md` | The normative manifest specification: canonical line-oriented grammar and identity, kinds (`backup`, `pebble-checkpoint`, `sqlite-snapshot`, `chunked`), the registry with its `active`/`staging` lifecycle, GC-root semantics with `MultiSHARefSource` wiring topology and the fail-safe abort, and the status-aware `blob.manifest` verifier. |
| `BLOB_BACKUP.md` | Capture and restore: the audited four-plane tenant inventory (cal excluded as derived, ts included as authoritative), capture ordering with the GC deadline and its U1 cost caveat, the shared-mode logical export, the forward-compatibility rule and unknown-role guard, restore with per-plane-replacement idempotency and the oracle acceptance gate, async-job surface, and the `/export` re-basing that closes W-4. |
| `BLOB_CONDITIONAL.md` | The W-2 wire contract: strong-ETag semantics, the per-verb precondition matrix, striped per-key CAS mechanics, single-range `Range` with `If-Range`, copy-on-write PATCH with mandatory `If-Match`, size/quota interaction, S3 parity, error codes BL007–BL010, and the dxp stage/promote attachment seam. |
| `BLOB_IMPORT.md` | Cross-instance import: the tenant-scoped fetch-by-SHA endpoint, xotogen-based auth, the async import job whose resumability is idempotence over the `staging` state, materialisation by manifest kind, read-only tenancy, trust gates (integrity check, Pebble format), and hot mount — including the hard-link fast path with its SQLite-copy-if-writable rule and the recorded lazy-mount idea. |
| `BLOB_OPEN_QUESTIONS.md` | The design-stage register of everything unresolved: undecided decisions D1–D14 with standing proposals, uncertainties U1–U10 needing prototype or measurement, the truly impossible I1–I3, the foreclosed-but-liftable F1–F6 each with its lifting condition, and obligations O1–O8 owed to the repository register, the fleet and nolu reconciliations, and the dormant guards. |
| `BLOB_TIMETRAVEL.md` | The exploration of write-log time travel: alias-plane journal with per-key chains, replay-by-re-pointing, the bal isomorphism with both T-51 lessons adopted at birth, snapshot manifests and retention-as-GC-contract, the three read modes (as-of, time-travel mount, chronicle-indexed scrubbing), and the SQL-aliases-versus-WAL-intent fork. |

Suggested reading order: `BLOB_EXTENDED_ROLES.md` first (it maps
everything else), then `BLOB_MANIFEST.md` (the object the rest
consume), then whichever role is of interest; `BLOB_OPEN_QUESTIONS.md`
before any execution decision; `BLOB_TIMETRAVEL.md` last, as the
furthest horizon.

## What this is all about

The blob store was designed as a modest content-addressed key-value
surface, and it turns out to have been designed better than it knew.
Its guarantee structure — immutable content files, atomic writes,
structural deduplication, SHA-256 as identity, the alias as the only
mutable cell — is precisely the substrate that backup, replication,
import, and versioning mechanisms want, and most of this corpus is
less invention than recognition: writing down compositions the store
already permitted. Deduplication turns out to be incremental backup.
Immutability turns out to be replication safety. The SHA turns out to
be an ETag, which turns out to be a compare-and-swap token, which
turns out to be a tamper-evident receipt for a federation protocol
that had explicitly left its data plane as someone else's problem.

One genuinely new object binds the four original ideas together: the
**manifest**, a canonical, content-addressed listing of named content
references — itself a blob, itself immutable, itself a fingerprint of
the whole set it describes. It is the house two-identity pattern
(external mutable name, internal immutable identity) extended from
single objects to sets, and once it exists, every role becomes a thin
consumer of it. A backup is a manifest whose members are the tenant's
authoritative planes. An import is a manifest fetched and pinned
before its members arrive. A Pebble checkpoint *is* a manifest waiting
to be written down. A mount is a manifest materialised into a
directory that lazy-opening subsystems cannot distinguish from one
they created themselves. The furthest extrapolation — time travel —
adds only the temporal complement: a journal of alias mutations, at
which point snapshots become manifests along a timeline and replay
becomes pointer flipping over content that never moved.

The corpus is grounded rather than aspirational. Four defects and gaps
were found in the current implementation and verified byte-identical
at the public HEAD: content-addressed mode returns identifiers the
API cannot serve (W-1); no conditional requests or ranges exist
despite the ETag already being a content hash (W-2); content-addressed
mode combined with GC silently destroys data within about seventy
minutes at default settings (W-3, defect-class); and the existing
export predates the ts, cal, and blob planes, silently omitting
timeseries points and the entire blob namespace (W-4). These stand
regardless of whether any extension proceeds, and they are owed to the
repository register. The design also refuses to reinvent what exists:
authentication reuses xotogen, transport reuses pkg/client, capture
reuses the async-job pattern, verification reuses the rebuild-oracle
machinery, replication territory is ceded to the fleet architecture
and the nolu federation layer with one synthesis proposed back to
each, and the whole backup set is defended against future rot by a
plane-coverage rule and an unknown-role guard that make the W-4
failure mode fail loudly instead of silently.

If there is one architectural claim buried under all the prose, it is
this: the centre of gravity of the blob primitive is not the content
store but the **alias plane** — the thin mutable layer mapping names
to digests. Every hard question in the corpus eventually collapses
into it: concurrency lands there as CAS, dxp participation lands there
as staged-write-and-promote, replication's only genuine conflict
surface is there, and time travel's single deep fork asks whether
aliases should stop being files and become transactional rows in the
manner of bal's journal. The immutable majority of the system is easy
precisely because it is immutable; the corpus's real work is
quarantining all the difficulty into that one small plane and
specifying it exactly.

The risks are stated with the same candour. Everything now leans on
the garbage collector — the component this session caught being wrong
twice — and each new role adds a party whose survival depends on a
perfect mark phase; the specifications treat the mark phase as the
most safety-critical code in the store, but specification is not yet
fuzzing. The registry couples the blob store to SQLite, trading some
of its filesystem self-containment for transactional retention facts —
the right trade, but a trade. And a seven-document corpus for
unscheduled work is itself a hazard in a codebase whose history proves
that unmaintained design prose decays into W-4s; the open-questions
register, the reconciliation obligations, and the recommendation to
ship Stage 0 promptly (it is mostly bug-fixing) are the hedges.

Staged, the path runs: Stage 0 fixes the found defects and lands
conditionals; Stage 1 builds the manifest, registry, and backup with
its round-trip guard; Stage 2 delivers import and hot mounting;
Stage 3 adds copy-on-write patching; the coordination layers remain
with the documents that own them; and time travel waits at the
horizon as an exploration whose components are named and whose one
fork is identified. The compressed thesis, for the register: these
extensions are the two-identity pattern reaching its fixed point —
sets of immutable values acquiring stable names — at which point
backup, import, transfer, mount, and history stop being machinery and
become perspectives on content that was already there.
