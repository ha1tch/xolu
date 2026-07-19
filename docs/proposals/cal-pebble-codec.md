# The `cal` Pebble codec — keyspace and bitmap granularity

> **Reconciliation status (added 2026-07-18, v0.14.13):** This document was
> written on 2026-06-22 before the `cal` implementation began. The
> codec's core structural commitments — two-plane bitmap (Proposed +
> Binding), 5-minute quantum resolution, dense per-tenant `uint32`
> ordinals, day-aligned bitmap keys — did ship in v0.14.0 and remain
> in force through v0.14.13. The overall codec is faithful to this
> proposal.
>
> Notable divergences from this proposal:
>
> - The Capacity/pooling considerations mentioned in this doc for
>   supporting `Capacity > 1` calendars were never implemented. Cal
>   ships exclusive-only occupancy (a booking takes the whole
>   calendar for its span). Implementing pooled resources properly
>   would require a counter-based bitmap encoding with ~8x storage
>   growth; the decision made in v0.14.13 was to align with Google
>   Calendar's actual model instead. See CHANGELOG v0.14.12 and
>   v0.14.13.
> - The `sub:<child_id>` sub-slot ordering considerations here have
>   no corresponding code — sub-mode was removed in v0.14.12.
> - Rollup pyramid details described here are implemented; the
>   performance validation of the rollup-prune payoff is filed as
>   T-16 in the debt tracker (open, requires realistic occupancy
>   distributions to measure).
>
> The authoritative codec surface is in `pkg/cal/codec.go`,
> `pkg/cal/store.go`, and `pkg/cal/codec_property_test.go`. Property
> tests in the last of those confirm the invariants this proposal
> committed to.

---

Status: Proposed codec specification (part of the `cal` design, with `cal-rest-api.md`)
Prepared: 2026-06-22
Author: haitch (h@ual.li)
Licence: Apache 2.0
Revision: 2026-06-22 design session (cal scheduling primitive)

> This pins the on-disk Pebble encoding for `cal`'s **temporal occupancy index** —
> the derived bitmap acceleration structure (A8/F3/H3), *not* the relational
> booking record, which lives in SQLite (H1). It specifies the key layout, the
> quantum-grid granularity model that makes cross-calendar bitmaps AND-able, and
> the rollup pyramid used to prune multi-calendar matching.
>
> Two design commitments of the `cal` design (stated in `cal-rest-api.md` and
> settled in the 2026-06-22 session) are load-bearing here and are treated as
> settled inputs:
> - **H1:** the SQLite record is the source of truth; the Pebble structure is a
>   *derived, rebuildable index over booking spans* — never authoritative.
> - **F3(c):** a **common base quantum with per-resource integer multiples** (the
>   buddy-allocator shape). This is the single decision the whole codec rests on:
>   it is what makes any two calendars' bitmaps line up for an N-way bitwise AND
>   (the F16 matching operation) *by construction*, without a per-match projection
>   step.
>
> House consistency: the layout follows the `ts` codec discipline exactly —
> fixed-width big-endian fields, id-prefixed, lexicographic byte order = scan
> order, time as a derived integer index. See `pkg/timeseries/codec.go`.

---

## 1. What this codec stores, and what it does not

`cal` has two storage planes (H1):

- **The relational booking record** — state, the A9 lifecycle, references/edges to
  the entity graph (resource, bearer, participants), the bitemporal trail. This is
  **SQLite**, served by the document/graph store. It is the source of truth. It is
  out of scope for this document.
- **The temporal occupancy index** — a per-calendar bitmap raster of occupancy
  over a quantum grid, plus a coarse-to-fine rollup pyramid over it. This is
  **Pebble**, and it is what this codec specifies. It is derived: it can be dropped
  and rebuilt from the SQLite record at any time.

The index answers exactly two question-families fast (H3): single-calendar
`openings`/`availability` (forward scans), and multi-calendar `match` (N-way
bitwise AND with pyramid pruning). Everything else (`findalloc`/`bookings/list`
relational filtering, the lifecycle) is SQLite's job.

> **Bitmaps are never the source of truth** (design, "actively distrust"): a bit
> records *that a quantum is occupied*, not *by what*. The "by what" — which
> booking, which participant, binding vs proposed — is resolved against SQLite. The
> bitmap is the allocator's free/occupied map (A8), nothing more.

---

## 2. The granularity model (F3(c) — the core)

### 2.1 The common base quantum

There is **one global base quantum** `Q_base` for the whole `cal` plane: the
smallest indivisible time cell the bitmap layer can represent. Every calendar's
own resolution is an **integer multiple** of `Q_base`:

```
resolution(calendar) = k × Q_base,   k ∈ {1, 2, 3, …}
```

`Q_base` is the finest resolution any *matched* calendar will ever need — set by
the most precise participant in any joint booking (F3(c)'s stated cost). It is
**5 minutes** (`Q_base = 300s`): the grain evidenced by the Xamin predecessor,
which ran courts, rooms, and equipment at 5-minute resolution successfully (§2.4).
It is a deployment constant, fixed at store creation and **immutable** thereafter
— changing it would invalidate every stored bitmap, exactly as a timeline's
`dims` are immutable after first write.

A calendar declares its own `resolution` (from `cal/def`, see `cal-rest-api.md`)
as `k × Q_base`. The pool-for-maintenance might use `k` = 288 (one-day cells); a
contended lane might use `k` = 3 (15-minute cells); a calendar that wants the
finest grain uses `k` = 1 (5-minute cells, the base). All are exact multiples of
the same 5-minute base, so all bitmaps line up for the cross-calendar AND.

> **v1 simplification.** Per-resource multiples (`k > 1`) are a documented
> forward-compatible extension, **not built in v1**. v1 stores every calendar at
> the single 5-minute base grain (`k = 1` for all). This is the Xamin-evidenced
> single-grain model — Xamin never matched across differing grains, so there is no
> evidence the per-resource-grain complexity is needed yet — and it dissolves the
> coarse-vs-fine match-granularity hazard entirely for v1. The `k × Q_base`
> structure is specified here so the keyspace does not need redesigning when a
> calendar first genuinely demands a coarser grain.

### 2.1a Per-calendar grid offset (`delta`) — opt-in, default 0

The 5-minute grid is :00-aligned: quanta begin at :00, :05, :10, … of each UTC
day. This matches the domain — real bookings land on 5-minute marks, because the
administrative overhead of sub-5-minute scheduling exceeds any benefit, so almost
no calendar needs to start a session off-grid. **The common case is therefore
`delta = 0`.**

A calendar that genuinely needs a precise off-grid session origin (a session that
must start at, say, :03 past) may set a **per-calendar `delta`** — a fixed offset,
0–4 minutes, that phase-shifts that calendar's whole grid. It is **3 bits**, set
at `cal/def`, stored in the calendar metadata (`0x03`), validated to `0 ≤ delta <
5` (an offset of a whole quantum or more is meaningless — reject `delta ≥ 5`
rather than silently alias it). It is a fixed property of the calendar, like
`resolution`; it never varies per booking or per day.

`delta` interacts with matching, but **never breaks it** — the earlier worry was
overstated. Two cases:

- **Same-delta (the common case, including all-zero) — direct AND.** Calendars
  sharing a `delta` share a grid origin, so their day bitmaps AND directly, the
  full-speed five-word-AND. A deployment whose calendars all use the default
  `delta = 0` (or all share one offset within a domain) always takes this path.
- **Mixed-delta — coarsen and verify.** Calendars with different deltas have grids
  offset by a sub-quantum amount, so a direct fine AND would intersect misaligned
  cells. Match then falls back to the **3-hour daypart rollup** (§4) as the common
  grid — any two 5-minute grids offset by < 5 minutes agree at daypart granularity
  — ANDs there to find candidate windows, then verifies candidates exactly against
  the SQLite booking records (H1). This is a bounded slower path, not a correctness
  failure, and the rollup level it needs is one we build anyway. The cost lands
  only on calendars that opted into off-grid precision.

So `delta` is free in the common case and self-funding in the rare one: a calendar
pays the mixed-delta match cost only because it chose precision the domain usually
does not need.

### 2.2 Why the base grid makes matching free

Because every calendar's grid is `k_i × Q_base`, all grids share the **same
origin and the same base tick**. A quantum index `q` means the same absolute
instant on every calendar:

```
absolute_time(q) = epoch + q × Q_base
```

So intersecting calendars A (k=15) and B (k=60) does **not** require projecting
one grid onto the other at match time (option (b)'s cost). Each calendar's bitmap
is already addressed in `Q_base` units; a coarser calendar simply sets runs of
`k` consecutive base-quanta together. The N-way `match` is a literal bitwise AND
of bit-slices over the same `[q_start, q_end)` base-quantum range — a few
instructions per 64-bit word regardless of how many bookings underlie it (H3).

This is the buddy-allocator transposition (A8): fixed-size blocks on one shared
address space are AND-able precisely because they are all multiples of one base
block on one origin.

### 2.2a The conversion invariant (the load-bearing fact)

Everything about matching, mixed grains, and the rollup pyramid follows from a
single asymmetry:

> **A coarser calendar always converts *down* to a finer one losslessly. Two
> calendars at the same resolution compare with a plain bitwise AND. So to compare
> any two calendars, convert both down to their common (finer) resolution and AND
> — and the common resolution is always reachable, because every grid is an
> integer multiple of `Q_base`.**

The two halves:

- **Down-conversion (coarse → fine) is exact.** A coarse quantum *is* a whole
  number `k` of base quanta — that is what `k × Q_base` guarantees — so a booked
  night is exactly 288 booked 5-minute slots. Expanding is "repeat each bit `k`
  times": total, lossless, no information invented or discarded. A coarse calendar
  can always step down to meet a finer one.
- **Equal-resolution comparison is one instruction.** Same resolution ⇒ bit `q`
  denotes the same instant in both ⇒ AND, word by word. No projection, no
  alignment, no interpretation.

The asymmetry is the whole point, and it dictates the one rule every cross-grain
operation obeys:

> **Always reconcile toward the *finer* grain. Expand the coarse calendar up to
> the fine one; never collapse the fine calendar down to the coarse one.**

Up-conversion (fine → coarse) is **lossy** — collapsing 288 fine bits into one
coarse bit keeps only "any / none" and discards *which* slots. So a match runs at
the finest resolution present in the participant set, and every coarser
participant expands to meet it (the room expands to 288 bits to meet the
housekeeper; the housekeeper is never collapsed to a night-bit to meet the room).
The toward-finer direction is always safe; the other always loses information.

This is also the exact reason the rollup pyramid (§4) can **only prune, never
confirm.** A rollup entry is an up-conversion — the lossy direction — so a rollup
AND can prove a match is *impossible* (a coarse bucket with no common free unit
cannot contain a fine match) but can never prove one *exists* (a coarse bucket
that looks free may still have no aligned fine slot). Confirmation always drops
back to the fine AND. Pruning is sound on the lossy side; confirmation is not.

### 2.3 Absolute grid, not calendar-aligned (H3)

The quantum grid aligns to **absolute time from a fixed epoch**, never to calendar
periods. Bits do not nest in Decembers; there is no year-boundary seam. Human
query periods (`2027/month/05`) are resolved to an absolute `[q_start, q_end)`
base-quantum range at query time (the `cal-rest-api.md` `availability/{period}`
contract) and never appear in storage. The epoch is the same store-creation
constant as `Q_base`.

### 2.4 Bit budget (sanity check)

At `Q_base` = 5 minutes the day bitmap is **288 bits = 5 × uint64 = 40 bytes**
(320 bits allocated, 288 used, 32 trailing bits unused — see §3.3). One Pebble
value holds exactly one day. A fully-dense calendar-year is therefore `365 × 40 B
≈ 14.6 KB` per occupancy plane — but the future is lean (H3) and the store is
**sparse by day**: only days that contain occupancy have a value at all. A
calendar with a quarterly maintenance booking stores four day-values, not 365.
The dense figure is the worst-case ceiling for a fully-contended resource, never
the norm.

5 minutes is the **evidenced** base grain: the Xamin predecessor ran courts,
rooms, and equipment at exactly 5-minute resolution and its bitwise clash
detection worked. Its mistake was making 5 minutes *global and sole*, not the
value. `cal` keeps 5 minutes as the finest grain and (in a later phase, F3(c))
lets coarser calendars allocate in integer multiples of it.

---

## 3. Pebble keyspace layout

### 3.1 Top-level partition (key prefix byte)

All `cal` Pebble keys live under the per-tenant `cal/` directory
(`storelayout`, sibling to `ts/` and `blobs/` — design H1), in a **single Pebble
store per tenant** that holds every calendar, distinguished by a key prefix —
mirroring exactly how `ts` stores all of a tenant's timelines in one store keyed
by `timeline_id` (verified: `pkg/timeseries/store.go` opens one `db` per tenant;
`timeline_id` is a 2-byte key prefix, not a separate database). A `registry.json`
in the same directory tracks calendar definitions, the direct analogue of the `ts`
timeline registry. Within the store the keyspace is partitioned by a leading
**1-byte kind tag**, so the sub-spaces scan independently and never collide:

```
0x01  occupancy bitmap days     (the raster, one value per day, §3.3)
0x02  rollup pyramid entries     (coarse occupancy summaries, §4)
0x03  index metadata            (calendar ordinal map, entity ref, per-calendar delta, dirty marks)
```

(The relational record — calendar def, bookings — is **not** here; it is SQLite.
The Pebble store holds only the derived index. `0x00` is reserved/unused so a
zero-byte key never collides with a live tag, mirroring `ts`'s reserved
timeline 0.)

**One store per tenant — settled.** The alternatives were assessed and rejected:
a store *per calendar* would put each calendar's bitmap in its own DB, turning
cross-calendar match (the hot path) into N cross-store reads and giving a hotel
thousands of Pebble instances (file-descriptor and memory blowout) — it defeats
the single-keyspace AND the whole codec rests on. A store *per plane* (binding vs
proposed) would force a cross-store transaction on `confirm`, the one operation
that moves a span between planes atomically. Both separations belong in the key
prefix, not a store boundary. The only split with a real physical rationale —
**sealed past vs live future** (immutable cold archive vs hot mutable working set,
different LSM profiles) — is a scale-triggered archival optimization, not a v1
need; it is deferred (§6).

### 3.2 Calendar id in keys

A calendar's id is a tenant-scoped string (`cal-rest-api.md`), not a `uint16` like
a timeline. To keep keys fixed-width and lexicographically ordered (the `ts`
discipline), the codec maps each `calendar_id` to a **stable `uint32` ordinal**
assigned at `cal/def` time and recorded in the SQLite calendar record (and
mirrored under the `0x03` metadata tag for index-rebuild without SQLite). Keys use
the ordinal; the string id never enters a Pebble key. This is the one addition
over the `ts` codec (timelines are natively numeric); it is necessary because the
bitmap layer must have a compact fixed-width calendar key.

### 3.2a Entity binding (one calendar, one entity)

A calendar is the availability of **exactly one entity** — a room, a person, a
team, a pool. The entity is whatever the entity graph says it is; `cal` does not
distinguish atomic from composite. A team is just an entity with a calendar, like
any other; there is no special group/aggregate construct in the codec. If a team's
availability needs to reflect its members, that synchronisation lives *above*
`cal` in entity space (some process composes the members and writes the team
calendar); the codec sees one entity, one calendar, one bitmap, uniformly.

So the calendar carries a **single entity reference** — a `u64` entity handle —
stored in the SQLite calendar record and mirrored in `0x03` metadata (so rebuild
can re-derive the binding). Multi-entity relationships are never expressed by a
calendar holding several refs; they are expressed by the *target entity* holding
multiple REF fields in the graph. The calendar stays single-target; the graph does
the fan-out.

**Reserved sentinels and the handle-space policy.** Every entity ref in `cal`
(this calendar → entity binding, and the booking → bearer ref in the SQLite
record) is a `u64` drawn from a partitioned handle space: **real handles allocate
upward from 1; sentinels reserve downward from `MaxUint64`.** The two grow toward
each other and never meet, because a fixed boundary, `EntityMaxValid`, separates
them. The reserved high zone is the sentinel pool; everything from 1 up to
`EntityMaxValid` is a valid handle; `0` is the floor sentinel.

```go
const (
    EntityNil       uint64 = 0x0000000000000000 // floor: no binding by design
    EntityMaxValid  uint64 = 0xFFFFFFFFFFFFFF00 // allocator ceiling
    // reserved sentinel pool: (EntityMaxValid, MaxUint64], allocated DESCENDING:
    EntityTombstone uint64 = 0xFFFFFFFFFFFFFFFF // bound, target entity deleted
    // future sentinels take the next value down: 0x…FE, 0x…FD, …
)
```

- `EntityNil` (`0`) — **no binding by design**: a standalone availability track
  with no entity behind it.
- `EntityTombstone` (`MaxUint64`) — **was bound, target entity deleted**: a
  dangling ref, kept distinct from `EntityNil`.
- Valid handles: `EntityNil < h <= EntityMaxValid`. The allocator must **never**
  hand out a value above `EntityMaxValid` — that range is the reserved pool, fixed
  now so a future sentinel can never collide with an already-issued handle. The top
  256 values are reserved (room for 255 sentinels beyond `Nil`); the exact count is
  an implementation choice, but the boundary is reserved from day one because
  carving it later, after handles exist, is a migration.
- Validation is **one range check**: a live handle satisfies
  `h > EntityNil && h <= EntityMaxValid`. This stays correct no matter how many
  sentinels are later carved descending from the top.

The two defined sentinels are kept distinct on purpose: `Nil` and `Tombstone` need
different handling. Match resolution skips `Nil` refs (free, but nobody's entity)
and flags `Tombstone` refs as an integrity loose end rather than resolving them.
Because tombstoning is a *derivable* fact (the entity is absent from the graph),
index rebuild (H1) can re-stamp it — referential integrity stays observable rather
than silently corrupting. Entity deletion can therefore be lazy: mark, or let
resolve-time discover the dead ref, instead of eagerly cascading. The `confirm`
transition (proposed → binding) requires a *live* bearer, so its check is exactly
the valid-handle range test above — rejecting both the never-assigned (`Nil`, legal
on a proposal per A10) and the assigned-then-deleted (`Tombstone`) cases that a
binding commitment cannot have.

### 3.3 Occupancy bitmap day key (`0x01`)

The bitmap is stored **one value per (calendar, plane, day)**. The day is the
natural chunk: 288 quanta = one UTC day = 5 × uint64. Only days that contain
occupancy exist as values, so the store stays sparse over the lean future (§2.4).
Within the occupancy space there are **two parallel planes** per calendar —
binding and proposed — because the `availability` ternary (`free`/`idk`/`busy`)
and `capacity = 100 − confirmed%` both depend on distinguishing confirmed
occupancy from tentative (`cal-rest-api.md` §3b). Optionality does **not** get its
own plane: a confirmed optional is in the binding plane, an unconfirmed one is
absent (§2a).

Key layout (all big-endian, fixed width):

```
[kind:1=0x01][cal_ordinal:4][plane:1][day_unixnano:8]
```
- `kind`   — `0x01`.
- `cal_ordinal` — the `uint32` from §3.2.
- `plane`  — `0x00` = binding, `0x01` = proposed.
- `day_unixnano` — `int64`, the `UnixNano` of **00:00:00 UTC of that day**,
  big-endian. This is the same time encoding `ts` uses for its event timestamps
  (`uint64(ts.UnixNano())`, big-endian — verified in `pkg/timeseries/codec.go`),
  snapped to the day boundary. Big-endian so days scan in chronological order: the
  forward-scan-from-`now` that serves `openings`/`availability` (H3) is a direct
  Pebble range scan from the day containing `now` upward.

Value: the **288-bit day bitmap**, 5 × uint64 = 40 bytes. Bit `q` (0–287) set
means quantum `q` of that day is occupied on that plane, where
`q = (seconds_into_day) / 300`. Bits 288–319 (the slack in the 5th uint64) are
unused and always zero. Set bit ⇒ non-free; clear bit ⇒ free (the design's
allocator free-map, A8).

Key size: `1 + 4 + 1 + 8 = 14 bytes`, fixed — the `ts` property (lexicographic =
scan order, fixed width) is preserved.

#### Time derivation (one source of truth, H1)

Every time value derives from the booking's exact `UnixNano` start/end held in the
SQLite record. The bitmap is a pure function of it:

```go
const nsPerDay = 86_400 * 1_000_000_000          // 86_400_000_000_000
const nsPerQuantum = 300 * 1_000_000_000          // 5 minutes

dayKey := (tNano / nsPerDay) * nsPerDay           // UTC midnight of t's day, in ns
secsIntoDay := (tNano - dayKey) / 1_000_000_000
q := int(secsIntoDay / 300)                       // quantum 0..287
```

**Flooring is integer division, not a bitmask.** A day in nanoseconds
(`86_400_000_000_000`) is **not a power of two**, so no `& mask` produces a day
boundary — `value & mask` floors to the nearest 2^k ns (~19.5 h or ~39 h), which
is not a day. The quantum modulus (300 s) is likewise not a power of two. Both
floors are therefore `div`/`mul`, two integer ops, as cheap as a mask and
actually correct. (If a defensive snap is wanted so a stray sub-day timestamp
cannot pollute a day key, `(tNano / nsPerDay) * nsPerDay` *is* that snap.)

#### Zone: UTC, like `ts`

`day_unixnano` is zone-free in storage — `UnixNano` is an absolute instant
regardless of the originating zone — and canonicalised to **UTC** at the day
boundary, because UTC is the only zone `ts` ever uses (`ts` decodes every stored
timestamp as `time.Unix(0, ns).UTC()` and buckets/retains in UTC — verified). A
booking that crosses a UTC-day boundary simply sets bits in two adjacent
`day_unixnano` values, the clean two-day case (Xamin handled the same "spanning
midnight" split in its §6). Human-facing local-time periods
(`availability/2026/day/06-22`) are resolved to absolute UTC day-keys **at the API
edge** (design H3 — human periods resolved to absolute ranges at query time),
never in storage. Storage is absolute and zone-free; localisation is presentation.

### 3.4 Scan and match mechanics

- **Single-calendar `openings`/`availability`** — range-scan `0x01` day-values for
  one `cal_ordinal`+plane from the `now` day upward; within each day's 5 × uint64,
  scan for zero-runs (openings) or popcount/any-set (availability). Forward, lean
  (H3).
- **Multi-calendar `match`** — for each calendar in the set, fetch the day-values
  overlapping the query window on the binding plane; AND them uint64-by-uint64 at
  the same `day_unixnano` (the day grids are identical absolute UTC days, so they
  align by construction); the cleared bits of the AND are the coincident-free
  quanta. The pyramid (§4) prunes which days to fetch at all. (When F3(c)
  per-resource multiples arrive, a coarser calendar still stores in the same 5-min
  day grid, setting `k` consecutive bits per coarse cell, so the AND stays
  alignment-free.)

---

## 4. The rollup pyramid (`0x02`) — match pruning

Multi-calendar matching is the hot path (H3), and most query windows have no
common free slot. A coarse-to-fine occupancy pyramid lets `match` skip fine ANDs
over windows that cannot possibly coincide, and gives mixed-`delta` matches their
common grid (§2.1a). This reuses the `ts` rollup *code*, not its API or semantics
(H1/H3): an internal acceleration structure, never an exposed vocabulary.

### 4.1 Which levels earn their place

A rollup is an up-conversion (fine → coarse), so by the conversion invariant
(§2.2a) it can **only prune, never confirm**: a coarse AND proves a match
impossible but never proves one exists, and confirmation always drops back to the
fine AND. A rollup level is therefore worth building only if it changes the *unit
of the question* enough to prune real work — not merely trims resolution.
Coarsenings of the 5-minute fine plane were assessed against that bar:

| Coarsening | Bits/day | Bytes/day | Verdict |
|---|---|---|---|
| 15-min | 96 | 12 | **rejected** — 3:1; saves one word-AND of five; the fine plane is already cheap, barely prunes |
| 30-min | 48 | 6 | **rejected** — 6:1 dead zone; same question as the fine plane at not-much-less cost |
| hourly | 24 | 3 | **rejected** — its economic chunk (10 days) lands on no calendar unit, and 3-hour below it serves the daypart query more cheaply |
| 2-hour / 4-hour | 12 / 6 | 1.5 / 0.75 | **rejected** — byte-ragged (straddle byte boundaries), no clean human unit |
| **3-hour** | **8** | **1** | **kept** — exactly one byte/day; one month = 31 bytes in a single read; clears the economic bar at the month; backs `availability/{month}` directly |
| daily | 1 | 0.12 | **deferred** — needs a ~224-day value to be economic, and only answers *whole-day* emptiness; build only if long-horizon whole-day match queries appear (see §6) |

So the v1 pyramid is **one rollup level — 3-hour daypart — above the 5-minute fine
plane.** 3-hour wins uniquely because it is the only divisor of 24 that is
simultaneously byte-aligned (8 bits = 1 byte/day), economically self-sufficient at
exactly a calendar month (31 B ≥ 2× the 14 B key), and human-aligned (the month is
both the economic chunk and the natural query unit). The daily level is deferred
behind an explicit trigger, not built on spec.

### 4.2 The economic sizing rule (chunk = many coarse units per value)

A key is pure overhead: it buys only the ability to find the value. So **a value
must carry more information than its key costs, or the entry is anti-economic** —
you would be spending more bytes addressing the value than storing it. The bar
adopted here is **value ≥ 2 × key size** (key = 14 bytes, so **value ≥ 28 bytes**)
— a real margin over the strict floor, not just "bigger than the key."

The fine plane already clears this: 40-byte day value vs 14-byte key (2.9×). But a
rollup's *per-unit* data is tiny — one day at 3-hour daypart is 8 bits (1 byte),
far smaller than the key — so **the rollup level must batch many days into one
Pebble value** to be economic. The chunk size is not chosen by taste; it is the
smallest span whose value clears 28 bytes, and for 3-hour that span lands exactly
on a calendar month:

| Level | Bits per day | Chunk span | Value size | Clears 28 B? |
|---|---|---|---|---|
| **3-hour daypart** | 8 (= 1 byte) | **one month (28–31 days)** | 28–31 B | yes — a month is the economic floor *and* the natural unit |
| daily (deferred) | 1 | ~year-block (≥224 days) | ~46 B/year | yes, but long & sparse; deferred (§6) |

The month chunk is the codec's happy coincidence: at 1 byte/day the economic floor
(224 bits ÷ 8 = 28 days) and the human unit (the month) are the *same span*. One
31-byte value = one month of daypart occupancy = one read, and it backs the
`availability/{period=month}` API query directly. (General rule: *the coarser a
level, the longer the span it must chunk to stay economic.* A level that cannot be
made economic without a span so large it is useless for pruning should not exist —
which is why the deferred daily level, needing ~224 days per value, is on the
margin of worth building.)

### 4.3 What each level stores

Each level holds, per chunk, the bitmasked analogues of the `ts` rollup
aggregates:
- **OR** — "is any quantum in this coarse unit occupied?" (prunes
  `availability?q=busy`, gates fine scans).
- **any-clear** — "is any quantum in this coarse unit free?" (prunes `match`: if N
  calendars share no commonly-free coarse unit, skip every fine day under it).
- **popcount** — "how many quanta occupied?" (a coarse `capacity = 100 −
  confirmed%` estimate before the exact fine count).

### 4.4 Key layout

```
[kind:1=0x02][cal_ordinal:4][plane:1][level:1][chunk_start_unixnano:8]
```
- `level` — `0x01` = 3-hour daypart (the v1 rollup). `0x02` reserved for the
  deferred daily level.
- `chunk_start_unixnano` — the `day_unixnano` (§3.3) of the **first** day in the
  chunk (the first day of the month), same big-endian UTC-midnight encoding, so
  pyramid entries scan chronologically alongside the fine days they summarise. A
  chunk is one calendar month, so `availability/{period=month}` resolves to a
  single key.

### 4.5 Maintenance and sealing

The pyramid is maintained **incrementally** over the mutable future as bookings
change, and **sealed at the now-crossing** (H2): once a day is entirely in the
past it is immutable and its pyramid entries never recompute (unlike `ts` rollups,
which roll continuously). This is the live-lean-future / sealed-past boundary (H3)
expressed in the index.

---

## 5. Lifecycle interactions

- **Booking create/confirm/cancel/move** mutate the SQLite record first (source of
  truth), then update the affected `0x01` day-values and `0x02` pyramid entries. If the
  process crashes between the SQLite write and the Pebble update, the index is
  stale-but-detectably-so (the `0x03` dirty mark) and is rebuilt from SQLite — the
  index being derived (H1) is what makes this safe; a lost index update is never a
  lost booking.
- **proposed → binding (confirm)** moves a span from the proposed plane to the
  binding plane (clear in `0x01` plane `0x01`, set in plane `0x00`), and updates
  both planes' pyramids. This is the one operation that touches both planes.
- **The now-crossing** (H2) is a sweeper concern, not a write: it seals days
  whose window has fully passed and freezes their pyramid entries. Same forward
  cursor as the dominant read and the A9 reconciliation sweep.
- **Index rebuild** — drop all `0x01`/`0x02` keys for a `cal_ordinal` and replay
  the SQLite bookings for that calendar, re-setting bits and recomputing the
  pyramid. O(bookings), not O(quanta), because the future is lean.

---

## 5a. Testing strategy

The codec splits cleanly into a part that suits test-first development and a part
that does not, and the two should be tested differently.

**Test-first (TDD) — the pure bit layer.** The codec functions are pure, total,
and deterministic: `dayKey(unixNano)`, `quantum(unixNano)`, the `delta` offset,
coarse→fine expansion (`k`-bit replication), the match AND, popcount-for-capacity.
The conversion invariant (§2.2a) *is* the test oracle, so the tests can be written
before the code: expansion round-trips, a booked night ANDed with anything is
all-set, an hour is 12 contiguous bits, 288 bits fill 5 × uint64 with the last 32
zero, one month of daypart is 31 bytes. Prefer **property-based tests** (the
codebase already uses fuzz targets and a shared adversarial corpus): generate
random bookings and assert the bitmap match agrees with a brute-force
interval-overlap oracle — the bitmap is an *optimisation* of "do these intervals
overlap", and the slow correct version is the oracle. These catch every off-by-one
in the bit math, and would have caught the `[5]uint64` arithmetic and
day-floor-by-mask slips at design time.

**Design-then-test — the stateful layer.** The now-crossing seal concurrency and
the proposed-vs-binding match semantics (§6) are *not* TDD-shaped: you cannot write
the test first for a race you have not characterised or semantics you have not
decided. Follow the model used for the `ts` timeline-delete deleting-marker:
settle the invariant, then write a race/fault-injection test that hammers the
interleaving under `-race`. TDD-the-ritual does not fit concurrency; design-the-
invariant-then-stress-it does.

**The global invariant — index == rebuild.** Because the Pebble structure is
derived (H1), the strongest possible oracle is free: the index must always equal
what `rebuild_from_sqlite()` produces. Assert it after every mutating operation in
the suite. This is what makes the derived structure trustworthy, and it is the
single check that subsumes most others.

**Caution.** The bit layer is the part most likely to be correct and least likely
to hurt; the seal concurrency is the part most likely to hurt and least amenable to
TDD. A green bar on the codec unit tests must not be read as confidence about the
concurrency, which those tests do not reach.

---

## 6. Open decisions carried forward (not settled here)

1. **`Q_base` value and immutability.** **Settled at 5 minutes** for v1 — the
   evidenced finest grain (Xamin ran real scheduling at 5-minute resolution).
   Open: whether a deployment can ever re-base to a finer grain (which would
   rebuild every bitmap), or whether 5 minutes is truly write-once. Treated as
   immutable for now, like a timeline's `dims`.
2. **Day as the storage chunk** — **settled**: one value per (calendar, plane,
   day) = 288 bits = 5 × uint64, keyed by UTC-midnight `UnixNano`. Replaces the
   earlier arbitrary fixed-chunk idea; the day is the natural, calendar-meaningful,
   `ts`-consistent unit and removes fractional-chunk edge cases. No tuning knob.
3. **Pyramid levels** — **settled at one v1 rollup: 3-hour daypart** (§4.1),
   chunked to one calendar month (the economic floor and the human unit coincide
   at 1 byte/day). 15/30-min, 2/4-hour, and hourly all rejected (dead zone or
   no clean unit). The **daily** level is deferred behind a trigger: build it if
   multi-calendar match queries are observed routinely spanning more than ~6 months
   and caring about *whole-day* availability (the hotel/time-share long-horizon
   case). Until then, the 3-hour level scanning a quarter (~92 bytes) is cheap
   enough.
4. **The economic margin bar.** Set to **value ≥ 2× key (28 bytes)** here. A
   stricter bar (value > key) admits smaller chunks; a page-aligned bar (4 KB
   values) makes far fatter chunks with fewer Pebble entries. 2× is the working
   default; revisit under load measurement.
5. **Whether the proposed plane needs a pyramid at all,** or whether proposed
   occupancy is rare enough that fine scans suffice and only the binding plane gets
   a pyramid. Depends on how heavily `idk`/proposal contention is used in practice.
   **This is not independent of `cal-rest-api.md` review issue 1** (the `match`
   plane-semantics decision): if `match` defaults to binding-only, the proposed
   plane is never on the match hot path and this pyramid is safely deferred; if
   `match` considers `binding+proposed`, the proposed plane *is* on the hot path
   and almost certainly needs a pyramid in v1. Decide the `match` default first;
   this decision falls out of it.
6. **`delta` realism.** Kept as an opt-in per-calendar setting (§2.1a, default 0),
   on the judgement that a few calendars genuinely need precise off-grid session
   origins. Open: whether any real calendar in the target domains ever sets it —
   if none do in practice, the mixed-`delta` match path is dead code and `delta`
   could be dropped to simplify. Carried as low-cost optionality until evidence
   says otherwise.
7. **`cal_ordinal` reuse on calendar delete** — whether an ordinal is retired
   permanently or recycled, and how that interacts with index rebuild. (Mirrors the
   timeline-id reuse question.)
8. **Engine choice confirmation (H1/H3):** this spec assumes Pebble for the index;
   the design left "SQLite index vs Pebble forward-time keyspace" as an empirical
   call. This codec is the Pebble branch; if benchmarks favour a SQLite index, the
   granularity model (§2) is unchanged but §3–§4 are replaced.
9. **Sealed-past / live-future store split (deferred, scale-triggered).** One store
   per tenant is settled for v1 (§3.1). The sealed past (H2) is immutable, cold, and
   grows without bound; the live future is small, hot, mutated. In a single LSM,
   compaction keeps touching the cold archive. **Trigger to revisit:** if a tenant's
   sealed-past archive grows large enough that compaction of cold data measurably
   affects live write latency — a measurement, not a guess; plausible only for
   very large, very long-lived tenants (a decades-old time-share, a chain with years
   of nightly history). First remedy is leaning on LSM bottom-level settling (sealed
   data stops being rewritten once it sinks); only past that, split the sealed past
   into a separate cold store keyed identically and read-stitched at the
   now-crossing. This interacts with the deferred daily rollup (a long history is
   when whole-day coarse archival summaries also earn their place).
10. **Atomic cross-calendar placement (`match/commit`) — codec implications.**
    The domain modelling pass (`cal-rest-api.md` finding F-F) proposes a
    `match`-then-seize write: place one booking per calendar across N calendars,
    all-or-nothing. This is a cross-calendar — and therefore potentially
    cross-store, once the §6.9 sealed/live split exists — transaction. Within the
    settled one-store-per-tenant v1 (§3.1) it is a single-store multi-key atomic
    write, which Pebble batches support directly, so v1 has no structural
    obstacle; the SQLite source-of-truth side (H1) must commit the N records in one
    transaction with the same all-or-nothing guarantee. The interaction to watch is
    with the future store split: if the sealed past is ever a separate store, a
    `match/commit` that touches both a sealed and a live day becomes genuinely
    cross-store. Since placement is always in the *future* (the lean, live side),
    this is unlikely in practice — a flag, not a blocker. Whether `match/commit` is
    v1 or deferred is a REST-scoping call; this entry only records that the codec
    does not obstruct it under the v1 single-store layout.
