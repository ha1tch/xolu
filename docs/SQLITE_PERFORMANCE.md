# SQLite Backend — Performance and Tuning

**Author:** Nadine Ostrovski  
**Date:** 2026-06-15  
**Scope:** `pkg/storage`, `pkg/config`, deployment configuration

---

## Overview

Xolu uses SQLite as its only storage backend. SQLite is an embedded,
serverless database: it runs inside the xolu process, accesses a local file
directly, and requires no network round-trips. This gives it a structural
advantage on read-heavy workloads but a structural disadvantage on
write-heavy ones compared to client-server databases like PostgreSQL.

This document explains why, what the current performance profile looks like
in practice, what is already done to mitigate write overhead, and what
operators can tune.

---

## Benchmark data

The figures below were collected with `olubench` against xolu v0.9.9-rc16 and
a local PostgreSQL instance, both running on the same host. 200 iterations,
20 warmup iterations, concurrency 1, single-node.

### Latency

| Operation | xolu (mean) | xolu (p99) | postgres (mean) | postgres (p99) |
|-----------|-----------|-----------|----------------|----------------|
| insert | 688 µs | 1.58 ms | 74 µs | 141 µs |
| insert\_bulk\_100 ¹ | 71.4 ms | 115.7 ms | 926 µs | 1.76 ms |
| read\_by\_id | 200 µs | 342 µs | 71 µs | 121 µs |
| list\_page20 | 229 µs | 406 µs | 254 µs | 399 µs |
| update | 1.21 ms | 1.68 ms | 133 µs | 254 µs |
| partial\_update | 1.40 ms | 1.89 ms | 125 µs | 237 µs |
| delete | 1.30 ms | 4.81 ms | 89 µs | 232 µs |
| filter\_query\_age | 184 µs | 284 µs | 124 µs | 348 µs |

¹ The `insert_bulk_100` benchmark was incorrectly formulated as 100 sequential
individual HTTP requests. This does not represent xolu's bulk write capability.
The correct measurement uses a single `/commit` request containing all 100
entities; see [Bulk writes and `/commit`](#bulk-writes-and-commit) below.

### Throughput (ops/sec)

| Operation | xolu | postgres |
|-----------|-----|---------|
| insert | 1,453 | 13,520 |
| read\_by\_id | 4,999 | 14,156 |
| list\_page20 | **4,377** | **3,932** |
| update | 830 | 7,523 |
| partial\_update | 714 | 7,974 |
| delete | 771 | 11,262 |
| filter\_query\_age | 5,438 | 8,053 |

### Graph operations (xolu only — PostgreSQL has no native graph layer)

| Operation | mean | p99 | ops/sec |
|-----------|------|-----|---------|
| graph:shortest\_path | 133 µs | 231 µs | 7,536 |
| graph:neighbours | 136 µs | 208 µs | 7,341 |

---

## Reading the numbers

### Where xolu is competitive

**`list_page20`** is the one operation where xolu beats PostgreSQL on both
mean latency (229 µs vs 254 µs) and throughput (4,377 vs 3,932 ops/sec).
The reason is simple: a local SQLite scan with a small result set avoids
the TCP round-trip that PostgreSQL pays even on loopback. For paginated
list operations, an embedded database has a structural edge.

**`filter_query_age`** is close — 184 µs vs 124 µs mean, but xolu's p99
(284 µs) is better than PostgreSQL's (348 µs). Filtered reads on warm
data benefit from the same zero-network-overhead advantage.

**Graph operations** have no PostgreSQL baseline because PostgreSQL does not
include a graph traversal engine. Xolu's FlatGraph runs in-memory and
achieves 7,500+ ops/sec for shortest-path and neighbourhood queries without
any external process.

### Where xolu is slower

**Writes are 9–10× slower than PostgreSQL** across insert, update,
partial\_update, and delete. This is the central performance characteristic
to understand, and the reason is not what it looks like.

---

## Why writes are slow — the detailed explanation

### What `synchronous = NORMAL` actually means

The natural assumption when seeing write latencies in the 700 µs–1.4 ms
range is that SQLite is fsyncing the disk on every commit. This is wrong
under the current configuration.

Xolu opens its SQLite database with `synchronous = NORMAL` and
`journal_mode = WAL`. Under this combination:

- Individual `COMMIT` calls write data into the WAL file in memory — no
  fsync.
- SQLite fsyncs the WAL file only at **checkpoints** — when the WAL grows
  beyond `wal_autocheckpoint` pages (default: 1,000 pages ≈ 4 MB) or when
  the database is closed.

This means xolu is not paying an fsync per write. The write path is faster
than `synchronous = FULL` but not as fast as `synchronous = OFF`.

### What is actually happening inside each write transaction

Every `Create`, `Update`, `Patch`, and `Delete` call opens and commits its
own transaction. Inside each transaction, xolu executes between four and six
SQL statements:

1. **Sequence upsert** — increments the per-entity ID counter in
   `t<XXXX>_node_seq`.
2. **Entity insert/update** — writes to the blob table
   (`t<XXXX>_nodes`) or to the entity's adapted table
   (`t<XXXX>_ndata_<entity>`).
3. **Graph edge sync** (`syncGraphEdges`) — inspects all fields for
   REF objects and inserts corresponding rows into the graph edge table.
   When the entity has no REFs this is a no-op, but the field scan still
   runs.
4. **FTS indexing** (`indexForFTS`) — when full-text search is enabled,
   deletes the existing FTS entry and inserts the new one. When FTS is
   disabled, this is a fast no-op (flag check only).
5. **Commit** — flushes the transaction to the WAL.

The overhead is not disk I/O on every call. It is the cumulative cost of
four to six SQL statement round-trips through the Go `database/sql` layer
to a local file descriptor, plus JSON marshalling for blob-storage entities,
per entity, per request.

### The `database/sql` overhead

Go's `database/sql` is designed around the assumption that the database is
a remote server. Each `ExecContext` and `QueryRowContext` call acquires a
connection from the pool, sends the statement, waits for a response, and
releases the connection — even when the database is an embedded file. This
overhead is small (typically 5–20 µs per call) but multiplies across the
four to six statements per write transaction.

This is not a bug in xolu or in `database/sql`. It is a structural cost of
using a file-based database through a connection pool abstraction designed
for network databases.

### Why `read_by_id` is slower than expected

The 200 µs mean for a single-record read by ID is higher than the raw SQLite
speed suggests. A direct `sqlite3` query against the same file returns in
under 50 µs. The gap is:

- `database/sql` connection acquisition and release (~10–20 µs)
- JSON unmarshalling of the blob data into a Go map (~30–80 µs depending on
  payload size)
- Ref embedding: every `Get` call may resolve embedded REF fields by issuing
  additional reads for each referenced entity

Entities stored in adapted tables (native columns, no JSON blob) are faster
to read because they skip the unmarshal step. The OQL push-down planner
further reduces latency for filtered queries on adapted entities by executing
a single SQL JOIN rather than multiple individual reads.

### PostgreSQL's advantage is the connection model, not the storage engine

PostgreSQL is a client-server database running over a Unix socket. Its
latency advantage on writes comes from:

- **Write batching**: the PostgreSQL backend processes multiple concurrent
  writes and groups WAL flushes across clients. Xolu has one writer.
- **Prepared statement caching**: PostgreSQL caches prepared statements
  server-side per connection. Xolu's `stmtCache` caches read-side statements
  but transaction-scoped write statements cannot be shared across
  transactions.
- **Asynchronous commit**: PostgreSQL allows `synchronous_commit = off` per
  transaction, which acknowledges writes before they are fsynced. Xolu's
  equivalent (`synchronous = OFF`) is a global setting.

---

## What xolu already does to mitigate this

### WAL mode with read/write pool split

The writer pool is limited to one connection (`MaxOpenConns = 1`). This
matches SQLite's WAL constraint — only one writer at a time — and prevents
the pointless contention that would result from multiple goroutines racing
for the write lock. The reader pool (`ReadPoolSize = NumCPU`) runs in
parallel against the same WAL-mode file. Readers never block the writer.

### AdaptiveLock

The `AdaptiveLock` monitors the write success rate in a sliding window. When
the success rate drops below the threshold (default: 95%), it engages a
`sync.RWMutex` to serialise writes at the Go level, eliminating
`SQLITE_BUSY` errors entirely. When the success rate recovers, it
disengages. This adds zero overhead under normal load.

### Adapted tables

Entities with a registered JSON Schema are stored in native-column adapted
tables rather than a JSON blob. This eliminates marshalling/unmarshalling on
both reads and writes and enables SQL push-down for filtered queries. The
OQL engine can execute a filtered list as a single `SELECT WHERE` rather than
fetching all rows and filtering in Go. For write-heavy workloads with
structured data, registering schemas is the single highest-leverage
optimisation available.

### Bulk writes and `/commit`

The `/commit` endpoint accepts a batch of creates and an optional upsert in
a single HTTP request and executes them inside **one transaction**. Instead
of N individual `BeginTx` / sequence-upsert / entity-insert / `Commit`
cycles, the entire batch pays one `BeginTx` and one `Commit`. For any
workload that needs to write multiple entities together — especially if those
entities reference each other — `/commit` is the correct tool.

See [COMMIT\_ENDPOINT.md](COMMIT_ENDPOINT.md) for the full API.

---

## Tuning parameters

All parameters are set via environment variables or the config file. Defaults
are conservative and safe. Aggressive settings improve throughput at the cost
of durability guarantees.

### `XOLU_SQLITE_BUSY_TIMEOUT` (default: 5000 ms)

How long a write waits before returning `SQLITE_BUSY`. Under normal load
with one writer this is never reached. Increase it to 10,000–30,000 ms if
you observe busy errors under burst traffic.

### `XOLU_SQLITE_CACHE_SIZE` (default: 2000 KB)

SQLite page cache per connection, in KB (stored negative to indicate KB
rather than pages). Larger values reduce disk I/O on repeated access to the
same pages. For workloads with hot data sets, 8,000–32,000 KB is a
reasonable range. Memory usage is approximately `CacheSize × connections`.

```
XOLU_SQLITE_CACHE_SIZE=16000
```

### `XOLU_SQLITE_READ_POOL_SIZE` (default: NumCPU)

Number of read connections. Increase for read-heavy concurrent workloads.
Each connection holds its own page cache, so setting this too high wastes
memory. Start at `NumCPU × 2` and watch memory.

### `XOLU_SQLITE_CONTENTION_THRESHOLD` (default: 95)

Success-rate percentage below which the adaptive lock engages. 0 disables
the adaptive lock entirely; 100 keeps it permanently engaged (equivalent to
a plain mutex on every write). The default of 95 means one SQLITE\_BUSY
error in twenty is enough to engage the lock. Lower values tolerate more
contention before engaging.

### `XOLU_SQLITE_MAX_OPEN_CONNS` (default: 1)

Number of write connections. **Do not increase this for a single SQLite
file.** SQLite WAL supports exactly one writer; multiple write connections
cause `SQLITE_BUSY` errors, which the retry loop and adaptive lock handle —
but at a cost. This setting is a forward-compatibility provision for
multi-file deployments.

---

## Advanced tuning not yet exposed via config

The following SQLite pragmas are not currently configurable via environment
variables. They can be set by modifying the DSN in `pkg/storage/sqlite.go`.

### `PRAGMA wal_autocheckpoint` (default: 1000 pages ≈ 4 MB)

Controls how frequently SQLite checkpoints the WAL file back to the main
database file and fsyncs. The default of 1,000 pages means a checkpoint
fires every ~4 MB of writes. Each checkpoint briefly stalls write
throughput.

For write-heavy deployments that can tolerate a larger WAL file and longer
crash-recovery time, increasing this to 10,000 (40 MB) or higher reduces
checkpoint frequency and smooths write throughput:

```sql
PRAGMA wal_autocheckpoint = 10000;
```

Setting it to 0 disables automatic checkpointing entirely. The application
must then call `PRAGMA wal_checkpoint(PASSIVE)` periodically — for example
at maintenance windows or low-traffic periods.

### `PRAGMA temp_store = MEMORY` (default: 0, meaning FILE)

SQLite creates temporary files for internal operations such as sorting and
index creation. Keeping these in memory eliminates those file operations:

```sql
PRAGMA temp_store = MEMORY;
```

Low risk. The only downside is additional memory usage for large sorts.

### `PRAGMA mmap_size` (default: 0, disabled)

Memory-mapped I/O allows SQLite to access the database file as if it were
a memory region, bypassing the `read()` system call for data that is already
in the OS page cache. This primarily benefits read latency:

```sql
PRAGMA mmap_size = 268435456; -- 256 MB
```

Set this to a value somewhat smaller than the expected hot dataset size.
Requires the OS to have sufficient virtual address space (not a concern on
64-bit systems).

### `PRAGMA synchronous` (default: NORMAL)

The durability setting. Under WAL mode the options are:

| Setting | Behaviour | Risk on crash |
|---------|-----------|---------------|
| `FULL` | fsync WAL on every commit | None — fully durable |
| `NORMAL` | fsync WAL at checkpoints only | Loss of last few transactions if OS crashes (not just process crash) |
| `OFF` | Never fsync | Loss of transactions since last checkpoint if power fails |

`NORMAL` is the current default and the right choice for production. `OFF`
is appropriate for development, test, and cache-tier deployments where data
loss on hard crash is acceptable. It roughly doubles write throughput.

To make this configurable at the operator level, add an `XOLU_SQLITE_SYNCHRONOUS`
environment variable and inject it into the DSN alongside the existing pragmas.

---

## Workload guidance

### Xolu is a good fit when

- The workload is read-heavy (list, filter, get by ID) and the data fits
  in the page cache.
- Graph traversal is a first-class requirement and you do not want to run
  a separate graph database.
- Bulk writes are batched through `/commit` rather than issued as
  individual requests.
- The deployment is single-node and operational simplicity matters more
  than raw write throughput.
- Data is structured and schemas are registered, enabling adapted tables
  and OQL push-down.

### Xolu is a poor fit when

- The workload requires sustained high write throughput (hundreds of
  individual writes per second per tenant).
- Bulk ingestion is time-critical and cannot be restructured to use
  `/commit`.
- The deployment requires multiple write processes (xolu is single-writer
  per file by design).

### Checklist before concluding that writes are too slow

1. **Are you using `/commit` for bulk writes?** Individual HTTP requests
   for each entity is the wrong approach. A `/commit` with 100 entities
   executes one transaction. Benchmark that, not 100 separate requests.

2. **Are your entities using adapted tables?** Register a JSON Schema for
   write-heavy entity types. Adapted-table writes skip JSON marshalling,
   which accounts for 30–100 µs of the blob-path write cost.

3. **Is FTS enabled unnecessarily?** `XOLU_FULLTEXT_ENABLED=true` adds a
   delete + insert to every write transaction. Disable it if you do not
   use full-text search.

4. **Is graph enabled unnecessarily?** Even when an entity has no REF
   fields, `syncGraphEdges` scans all fields. If graph is not used, set
   `XOLU_GRAPH_ENABLED=false`.

5. **Is `wal_autocheckpoint` causing spikes?** If write latency is
   normally low but spikes periodically (every few hundred writes), a
   checkpoint is firing. Increase `wal_autocheckpoint` or disable it and
   schedule checkpoints manually.

6. **Is the page cache large enough?** If the hot data set does not fit
   in the page cache, every read goes to disk and write transactions
   trigger additional page loads. Increase `XOLU_SQLITE_CACHE_SIZE`.

---

## Troubleshooting slow writes — step by step

### Step 1 — Establish a baseline

Run `olubench` or a comparable load test against a clean database with
default settings. Record mean, p99, and max for `insert`, `update`, and
`delete`. This is your baseline.

### Step 2 — Check for SQLITE\_BUSY errors

Look at the xolu logs. `SQLITE_BUSY` errors mean write contention — either
another process has the database open, or the write rate is high enough that
the retry loop is firing. The adaptive lock should engage automatically, but
if you see repeated busy errors in logs, increase `XOLU_SQLITE_BUSY_TIMEOUT`
and check whether any external process has the file open (`fuser <db_path>`).

### Step 3 — Profile the write transaction

Enable debug logging (`XOLU_LOG_LEVEL=debug`) and trace a single write
request. The log will show how long each phase takes. Look for:

- Long sequence upsert (indicates lock contention on `t<XXXX>_node_seq`).
- Long entity insert (indicates page cache pressure or a very large payload).
- Long graph edge sync (indicates an entity with many REF fields).
- Long commit (indicates a checkpoint firing mid-transaction).

### Step 4 — Try disabling optional features

Baseline → disable FTS → disable graph → measure each. If either
significantly improves latency, the feature is worth the cost only if
you are using it.

### Step 5 — Migrate hot entities to adapted tables

Register JSON Schemas for the entity types being written most frequently.
Adapted tables eliminate JSON marshalling on both read and write. Re-run
the benchmark. For structured data, this is typically the largest single
improvement available without changing xolu's code.

### Step 6 — Restructure bulk writes to use `/commit`

If the workload involves writing multiple entities per logical operation,
collect them into a single `/commit` request. Measure the throughput of
100 entities via 100 individual requests versus one `/commit` with 100
entities. The difference is typically an order of magnitude.

### Step 7 — Tune WAL checkpoint frequency

If writes are fast on average but show periodic latency spikes, a WAL
checkpoint is interrupting write throughput. Increase
`wal_autocheckpoint` in the DSN or disable it and schedule checkpoints
explicitly during low-traffic windows.

### Step 8 — Consider `synchronous = OFF` for non-durable workloads

For development environments, test fixtures, or cache-tier deployments
where crash durability is not required, setting `synchronous = OFF`
roughly doubles write throughput. This requires a code change to expose
the pragma as a config option (see Advanced tuning above).

### Step 9 — Accept the ceiling

If after steps 1–8 writes are still too slow for the target workload,
the ceiling has been reached. SQLite with a single writer, regardless of
tuning, is not a high-write-throughput database. The appropriate next
step is to re-evaluate whether the workload is a good match for xolu, or
to consider whether the write-heavy operations can be offloaded or
deferred.

---

## Summary table

| Technique | Effort | Risk | Expected gain |
|-----------|--------|------|---------------|
| Use `/commit` for bulk writes | Low (API change) | None | 10–50× on bulk |
| Register schemas (adapted tables) | Low | None | 20–40% on write + read |
| Disable FTS if unused | Low | None | 5–15% on write |
| Disable graph if unused | Low | None | 5–10% on write |
| Increase `XOLU_SQLITE_CACHE_SIZE` | Low | Higher memory | 5–20% on read |
| Increase `wal_autocheckpoint` | Medium (DSN change) | Larger WAL, slower recovery | Reduces latency spikes |
| Add `temp_store = MEMORY` | Medium (DSN change) | Higher memory | 2–5% overall |
| Add `mmap_size` | Medium (DSN change) | Higher memory | 5–15% on read |
| Set `synchronous = OFF` | Medium (config + DSN) | Data loss on hard crash | ~2× write throughput |
