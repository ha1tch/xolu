# Blob Time Travel — Write Logs, Snapshots, and Replayable Images

Updated: 2026-07-22
Status: exploration — earlier-stage than the proposal-grade BLOB_* set;
captures a design direction and its requirements, decides nothing.
Companions: the BLOB_* specification set; `bal-conservation-primitive.md`
and the chronicle substrate, whose shapes this borrows deliberately.

## 1. The reframe: this is not event sourcing

The question — write logs that let /blob objects act as images that
time-travel by playing, fast-forwarding and rewinding changesets over a
linked timeseries — sounds like event sourcing. It is something easier.

Content-addressing already retains **every version of every blob's
content** as an immutable, deduplicated object, for as long as GC is
told to keep it. The store discards exactly one kind of information:
the *temporal* mapping — which SHA a key held at which moment. Restore
that mapping and history is complete, because the states themselves
never left.

Two consequences define the whole design:

- **Replay is re-pointing, never re-execution.** A changeset entry
  says "key K went from sha₁ to sha₂ at t"; playing it forward or
  backward re-points an alias to content that already exists. Nothing
  is recomputed. There are no determinism hazards, no side effects, no
  failed replays, no "the patch behaves differently on replay" class
  of bug. This is event sourcing's payoff without its fragility: the
  log carries pointers to materialised states, not instructions.
- **The problem decomposes into exactly two mechanisms:** journal the
  alias plane's mutations, and pin historical content against GC for
  the retention window. Everything else — cursors, images, scrubbing —
  is a read mode over those two.

## 2. The isomorphism: bal already built this shape

The architecture this wants is structurally the one bal ships today:

| bal | blob time travel |
|---|---|
| journal of signed entries, per-account chain (previous/current/version) | journal of alias mutations, per-key chain (prev_sha/new_sha/version) |
| sealed checkpoints: closing balance + journal position (`bal_checkpoints`) | snapshot manifests: full alias map + journal position |
| `BalanceAsOf` = nearest checkpoint + fold of intervening buckets | `StateAsOf` = nearest snapshot + replay of intervening entries |
| `BalanceAsOfExact` = full journal fold | full-replay fallback, same role |
| chronicle cascade over the journal for rollups | chronicle cascade over the journal for **activity density** (the scrub bar) |
| `VerifyChains` / `ChainOracle` — per-entry arithmetic, linkage, contiguous versions | the identical oracle over the alias log |
| Sealer (calendar windows) decides checkpoint boundaries | the same Sealer decides snapshot cadence |

Borrowing the shape buys the verification machinery with it — and one
scar, purchasable in advance:

**The T-51 lesson, applied before a line is written.** bal's open P1
defect is a checkpoint gone stale after a backdated entry, *and* —
the worse half — the discovery that no oracle verified checkpoints at
all. Two mandates follow for blob history, from day one:

1. **The log admits no backdating.** Log time is commit time, assigned
   under the alias mutation's own serialisation (§3), strictly
   monotonic per tenant. Blob mutations, unlike museum accessions,
   have no legitimate backdating case — so the entire T-51 defect
   class is excluded *by construction*, not by care.
2. **The snapshot oracle exists at birth**: `snapshot == replay-to-
   position`, verified per snapshot, wired into `iolu db check`
   beside the chain oracle. A checkpoint nothing verifies is the
   known failure; it does not get a second occurrence.

## 3. The journal, and the fork it forces

Every alias mutation — put, delete, CAS re-point, PATCH promote —
appends one entry:

```
(seq, key, op, prev_sha, new_sha, committed_at, actor?)
```

per tenant, globally sequenced, with per-key `(prev_sha, new_sha,
version)` chains in the bal manner. The linearisation point already
exists: `BLOB_CONDITIONAL.md` §3 serialises **all** alias mutations
for a key through the per-key lock, and the under-lock compare knows
`prev_sha` authoritatively. The journal appends at exactly that point.

But the alias is a file and the journal is a table, and no transaction
spans them — the cross-plane atomicity problem in miniature. Three
resolutions, one of which changes the store's nature:

- **(A) Promote the alias plane into SQL** — the alias map becomes an
  authoritative table; mutation + journal append become **one
  transaction** (the T-34 guard-in-predicate CAS, exactly bal's
  admission pattern); the `.keys/` directory becomes a **derived,
  rebuildable cache** with a rebuild oracle (`derive(sql) == keys
  dir`), kept for filesystem debuggability and the fast read path.
  This also hands dxp its blob participant on a plate: leases and
  tentative alias rows land in the tentative-row convention with no
  special casing. Cost: the store's core migrates; "understand it
  with ls" survives only as a derived view.
- **(B) Keep file aliases, journal as write-ahead intent** — append
  intent, swap, mark applied; crash recovery reconciles intents
  against actual alias state. Preserves the store; buys a recovery
  protocol and a two-phase write on the hottest path.
- **(C) Journal after the swap, best-effort** — a crash loses history
  entries. Disqualified: a history with silent gaps is worse than no
  history, because it will be *believed*.

The honest lean is (A): it is where bal, dxp, and the CAS work all
already point, and it converts this feature from bolt-on to
consequence. But it is the bigger surgery, and (B) is the reversible
first step if the migration is not yet wanted. This is the design's
one genuine fork; everything else composes either way.

## 4. Retention, pinning, and snapshots

Time travel reaches exactly as far as content survives. The journal
becomes a **SHARefSource**: every SHA appearing in the retained log
window is live. Retention is a per-tenant policy (window by age or by
version count); **pruning the log is the act that releases history to
GC** — one mechanism, no second deletion path.

Snapshots make retention and seek cost independent: a `snapshot`
manifest — the full alias map at a sealed journal position, a new
manifest kind beside `backup` — is produced by the chronicle Sealer's
calendar windows and registered like any manifest. A retained snapshot
pins its point **forever, even after the log window prunes past it**:
coarse long-term history (monthly images) coexisting with fine recent
history (every write, last N days), each priced separately. Backups
compose for free: a backup's alias members already are a snapshot;
recording the journal position in the backup manifest (an optional
`log-position` header) gives every backup an exact seat on the
timeline.

One property worth savouring against F1: **within the alias plane, a
true instant exists.** The journal's total order means an image at
position S is exactly "all mutations ≤ S" — consistent by
construction, not skew-bounded. The cross-plane instant remains F1;
the single-plane instant is free, and it is the plane time travel
lives on.

## 5. Read modes: playing, seeking, scrubbing

- **As-of read**: `GET /blob/{key}?at=T` (or `?seq=S`) — nearest
  snapshot at-or-before, replay the intervening entries for that key,
  serve the resolved SHA's content. Cost bounded by snapshot cadence,
  in the `BalanceAsOf` manner.
- **Time-travel mount**: the whole namespace at T, materialised as a
  read-only image and served through the hot-mount machinery
  (`BLOB_IMPORT.md` §8). Materialisation is only the alias map — a
  directory of tiny files or a table load; the content is already
  present, byte for byte. A **cursor** over the timeline follows:
  seek(T) re-derives the map (cheap: snapshot + fold); step
  forward/backward applies journal entries as pointer flips. Rewind
  and fast-forward are alias-map diffs, never content movement.
- **Scrubbing — the linked timeseries.** The journal's time axis feeds
  a chronicle cascade (the `SumInt64` monoid counting ops per bucket,
  hour→day→month) as a **derived, rebuildable activity index** — the
  density map that lets fast-forward skip dead regions and a scrub
  bar know where change clusters. Journal authoritative, index
  derived, rebuild oracle proving it: the standing pattern, third
  instance.
- **Changesets as objects**: a journal range [S₁, S₂] is a changeset;
  rendered as a manifest kind it becomes exportable — and log
  shipping between instances quietly becomes a delta transport for
  replication, a cheaper sibling to full manifest exchange. Noted,
  not designed.

## 6. What is needed — the componentised answer

1. The journal (fork §3 decided first), with per-key chains and
   commit-ordered, backdate-free sequencing.
2. Chain oracle and snapshot oracle, both at birth (§2's mandates).
3. Journal-as-SHARefSource with windowed retention; pruning as the
   sole release mechanism.
4. `snapshot` manifest kind + Sealer-driven cadence + the optional
   `log-position` header (grammar addition — nothing shipped, still
   free).
5. As-of resolver (checkpoint + replay), shared by the read modes.
6. The activity-index cascade over the journal + its rebuild oracle.
7. Surface: `?at=`/`?seq=` on GET; a mount-at verb composing with hot
   mount; changeset export later.
8. Guards: a travel round trip (write history → seek → verify each
   image against recorded expectations) — dormant, registered at
   birth.

## 7. Costs and hard edges, stated now

- **History begins when logging begins.** Nothing reconstructs the
  pre-feature past; the first journal entry is the horizon. Impossible
  in the strict sense — the information was never recorded.
- **Travel depth is retention.** Beyond the window and off-snapshot,
  the past is gone *by policy*, and the policy is the entire GC
  contract — there is no "undelete past retention" appeal.
- **Storage is churn-priced.** Cost is the sum of retained unique
  versions; dedup absorbs repetition, but a large, hot,
  frequently-rewritten blob pays full freight per version — the one
  workload that would drag chunking (D5) out of deferral.
- **Journal write on every alias mutation** — one small row on a path
  that is currently one file rename. Fork (A) makes it the same
  transaction; fork (B) makes it two steps. Either way the hot path
  gains weight, and it should be measured, not assumed.
- **The changeset is pointer-level, deliberately.** It records that K
  became sha₂, not how; PATCH parameters are not replayed. Semantic
  re-execution is a different, worse feature.
