# xolu Manual

Complete reference documentation for xolu v0.10.1.

## Table of Contents

0. [API Reference](docs/API_REFERENCE.md) — complete REST endpoint reference
1. [Installation](#installation)
2. [Configuration](#configuration)
3. [API Reference](#api-reference)
4. [Platform Extensions (/api/v2)](#platform-extensions-apiv2)
5. [Query Languages](#query-languages)
6. [Authentication](#authentication)
7. [Rate Limiting](#rate-limiting)
8. [Metrics & Monitoring](#metrics--monitoring)
9. [Storage Backends](#storage-backends)
10. [Graph Features](#graph-features)
11. [Testing & Benchmarks](#testing--benchmarks)
12. [Deployment](#deployment)
13. [Blob Storage](docs/BLOB_API.md)
14. [Dynamic Configuration](docs/DYNCONFIG.md)
15. [Async Queries](docs/ASYNC_QUERIES.md)
16. [JSON Schema & Adapted Tables](docs/JSON_SCHEMA.md)
17. [iolu — interactive xolu](docs/IOLU.md)
18. [Error Code Reference](docs/ERROR_CODES.md)
19. [Upgrade Guide](docs/UPGRADE.md)

Timeseries storage has its own design document: [Timeseries Design](docs/TIMESERIES_DESIGN_V3.md).

---

## Installation

### From Source

```bash
git clone https://github.com/ha1tch/xolu.git
cd xolu
make build
./bin/xolu
```

### Using Go Install

```bash
go install github.com/ha1tch/xolu/cmd/xolu@latest
```

### Docker

```bash
docker pull ghcr.io/ha1tch/xolu:latest
docker run -p 9090:9090 -v $(pwd)/data:/data ghcr.io/ha1tch/xolu:latest
```

### Build Options

```bash
make build          # Build binary
make build-all      # Build for all platforms (18 OS/arch)
make docker-build   # Build Docker image
make install        # Install to $GOPATH/bin
```

---

## Configuration

All configuration is via environment variables. The most commonly set options also have command-line flag equivalents; run `xolu help` to see them, or `xolu env` for the full environment variable reference.

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_HOST` | `0.0.0.0` | Server bind address |
| `XOLU_PORT` | `9090` | Server port |

### Storage

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_STORAGE_TYPE` | `sqlite` | Storage backend. Only `sqlite` is supported. |
| `XOLU_BASE_DIR` | `data` | Data root. All storage paths are derived from this; there is no separate database-path setting. |
| `XOLU_SCHEMA_NAME` | `default` | Schema/namespace name |

### Cache

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_CACHE_TYPE` | `memory` | Cache type: `memory` or `redis` |
| `XOLU_CACHE_TTL` | `300` | Entity GET and list cache TTL in seconds |
| `XOLU_CACHE_SIZE` | `1024` | In-memory cache capacity (entries) |
| `XOLU_CACHE_SHARDS` | `16` | Shard count for in-memory cache |
| `XOLU_GRAPH_QUERY_CACHE_TTL` | `30` | Sulpher query result cache TTL in seconds; `0` disables |
| `XOLU_OQL_QUERY_CACHE_TTL` | `30` | OQL query result cache TTL in seconds; `0` disables |
| `XOLU_REDIS_HOST` | `localhost` | Redis host (if using redis cache) |
| `XOLU_REDIS_PORT` | `6379` | Redis port |
| `XOLU_REDIS_POOL_SIZE` | `50` | Redis connection pool size |
| `XOLU_REDIS_MIN_IDLE_CONNS` | `10` | Redis minimum idle connections |

### Graph

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_GRAPH_MODE` | `flat` | Graph mode: `flat` or `disabled` |
| `XOLU_GRAPH_CYCLE_DETECTION` | `warn` | Cycle handling: `warn`, `error`, `ignore` |
| `XOLU_GRAPH_MAX_VISITED_NODES` | `10000` | Max nodes visited during a single traversal |
| `XOLU_GRAPH_MAX_RESULTS` | `10000` | Max result paths returned by a graph query |
| `XOLU_ASYNC_JOB_RETENTION_TTL` | `86400` | How long completed async job records are kept (seconds) |

When a graph limit is exceeded, the server returns a specific error code:

| Code | Meaning | HTTP Status |
|------|---------|-------------|
| `XOLU-GR005` | Visited-node limit exceeded | 413 |
| `XOLU-GR006` | Result limit exceeded | 413 |

Graph queries also respect the shared `XOLU_QUERY_TIMEOUT` and
`XOLU_QUERY_MAX_RESPONSE_BYTES` limits documented in the Query Guardrails
section below.

### Features

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_FULLTEXT_ENABLED` | `false` | Enable FTS5 full-text search (SQLite only) |
| `XOLU_CASCADING_DELETE` | `false` | Delete referencing entities on delete |
| `XOLU_REF_EMBED_DEPTH` | `3` | Default reference embedding depth |
| `XOLU_MAX_EMBED_DEPTH` | `10` | Maximum allowed embed depth |
| `XOLU_MAX_ENTITY_SIZE` | `1048576` | Maximum entity size in bytes |
| `XOLU_TENANT_MODE` | `path` | Tenant mode: `path` or `strict` |
| `XOLU_TENANT_AUTO_REGISTER` | `true` | Auto-create tenants on first access (path mode only) |
| `XOLU_TIMESERIES_ENABLED` | `false` | Enable Pebble-backed timeseries storage |
| `XOLU_TS_MEMTABLE_SIZE` | `67108864` | Pebble memtable size in bytes (64 MB) |
| `XOLU_TS_BLOCK_SIZE` | `32768` | Pebble block size in bytes (32 KB) |
| `XOLU_TS_COMPRESSION` | `zstd` | Compression: `zstd`, `snappy`, or `none` |
| `XOLU_TS_L0_COMPACTION_THRESHOLD` | `4` | L0 files before compaction trigger |
| `XOLU_TS_MAX_OPEN_FILES` | `500` | Per-tenant Pebble file descriptor limit |
| `XOLU_TS_DEFAULT_RETENTION_DAYS` | `90` | Default retention policy for new tenants |
| `XOLU_TS_COMPACTION_INTERVAL` | `3600` | Retention sweep interval in seconds |
| `XOLU_TS_RETENTION_ENABLED` | `false` | Run background retention goroutine |
| `XOLU_TS_QUERY_TIMEOUT` | `30` | Per-query context deadline in seconds |
| `XOLU_TS_MAX_QUERY_EVENTS` | `10000` | Maximum events returned by a single range query or Latest |
| `XOLU_TS_MAX_SCAN_EVENTS` | `500000` | Maximum events scanned before aborting (returns XOLU-TS013) |
| `XOLU_TS_MAX_RANGE_DAYS` | `366` | Maximum From→To window in days (returns XOLU-TS011 if exceeded) |
| `XOLU_TS_MAX_BATCH_SIZE` | `5000` | Maximum events per batch append (returns XOLU-TS006 if exceeded) |
| `XOLU_TS_MAX_RESPONSE_BYTES` | `10485760` | Maximum JSON response size in bytes (10 MB) |
| `XOLU_TS_MAX_AGGREGATE_BUCKETS` | `10000` | Maximum time buckets in a windowed aggregate (returns XOLU-TS019 if exceeded) |
| `XOLU_TS_COAL_FLUSH_INTERVAL_MS` | `10` | Write coalescer flush window in milliseconds. Only relevant when `ts.writecoal` is enabled via dynconfig. Lower values reduce per-call latency jitter; higher values increase events per fsync at high concurrency. Live-overridable per tenant via dynconfig. |
| `XOLU_TS_COAL_MAX_EVENTS` | `2000` | Write coalescer early-flush threshold. The coalescer commits immediately when this many events are queued, regardless of the flush interval. Prevents unbounded memory use under very high ingest rates. Live-overridable per tenant via dynconfig. |
| `XOLU_TS_ROLLUP_CASCADE_DELETE` | `true` | When `true`, deleting a rollup definition automatically removes all descendant definitions and stops their workers (leaves deleted first). When `false`, deleting a definition that has descendants returns `409 XOLU-TS026`; the caller must delete bottom-up manually. |

### SQLite Tuning

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_SQLITE_MAX_OPEN_CONNS` | `0` | Writer pool max connections (0 = backend default: 1 for WAL) |
| `XOLU_SQLITE_MAX_IDLE_CONNS` | `0` | Writer pool idle connections (0 = backend default: 1) |
| `XOLU_SQLITE_READ_POOL_SIZE` | `0` | Reader pool max connections (0 = backend default: NumCPU) |
| `XOLU_SQLITE_BUSY_TIMEOUT` | `5000` | SQLite busy timeout in milliseconds |
| `XOLU_SQLITE_CACHE_SIZE` | `2000` | SQLite page cache size (pages) |
| `XOLU_SQLITE_CONTENTION_THRESHOLD` | `95` | Adaptive lock contention threshold (0-100) |
| `XOLU_PATCH_NULL` | `store` | Null handling in PATCH: `store` or `delete` |

### Query Guardrails

Server-side limits that prevent runaway queries from becoming outages.
All limits are on by default and enforced consistently across OQL, search,
and list endpoints.

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_QUERY_TIMEOUT` | `30` | Max query execution time in seconds |
| `XOLU_QUERY_MAX_ROWS` | `10000` | Max rows returned by a single query |
| `XOLU_QUERY_MAX_SCAN_ROWS` | `100000` | Max rows scanned before query is aborted |
| `XOLU_QUERY_MAX_RESPONSE_BYTES` | `10485760` | Max JSON response size in bytes (10 MB) |

When a limit is exceeded, the server returns a specific error code:

| Code | Meaning | HTTP Status |
|------|---------|-------------|
| `XOLU-QL008` | Query timed out | 504 |
| `XOLU-QL009` | Too many rows returned | 413 |
| `XOLU-QL010` | Too many rows scanned | 413 |
| `XOLU-QL011` | Response too large | 413 |

### Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_AUTH_TYPE` | `none` | Auth type: `none`, `jwt`, `apikey`, `bearertoken` |
| `XOLU_JWT_SECRET` | | Secret key for JWT validation |
| `XOLU_JWT_ISSUER` | | Expected JWT issuer claim |
| `XOLU_API_KEYS` | | Comma-separated list of valid API keys |

### Rate Limiting

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_RATE_LIMIT_ENABLED` | `false` | Enable rate limiting |
| `XOLU_RATE_LIMIT_RATE` | `100` | Requests per window |
| `XOLU_RATE_LIMIT_WINDOW` | `60` | Window duration in seconds |
| `XOLU_RATE_LIMIT_BY_IP` | `true` | Rate limit by client IP |
| `XOLU_RATE_LIMIT_BY_KEY` | `false` | Rate limit by auth key/subject |

### Startup Output

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_NO_ASCII` | `false` | Suppress ASCII art banner at startup |
| `XOLU_NO_STARTUP_TEXT` | `false` | Suppress configuration summary at startup |

### Observability

| Variable | Default | Description |
|----------|---------|-------------|
| `XOLU_METRICS_ENABLED` | `true` | Enable Prometheus metrics |
| `XOLU_METRICS_PORT` | `0` | Dedicated metrics port (0 = shared with API port) |
| `XOLU_METRICS_HOST` | `` | Bind address for dedicated metrics listener |
| `XOLU_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `XOLU_DEBUG` | `false` | Legacy alias for `XOLU_LOG_LEVEL=debug` (deprecated; use `XOLU_LOG_LEVEL`) |

---

## API Reference

### Entity Operations

#### Create Entity
```http
POST /api/v1/{entity}
Content-Type: application/json

{"name": "Alice", "email": "alice@example.com"}
```

Response: `201 Created`
```json
{"id": 1, "name": "Alice", "email": "alice@example.com"}
```

#### Get Entity
```http
GET /api/v1/{entity}/{id}
```

Query parameters:
- `embed=false` - Disable reference embedding
- `embed_depth=N` - Override default embed depth

#### List Entities
```http
GET /api/v1/{entity}?page=1&per_page=20
```

Response includes pagination:
```json
{
  "data": [...],
  "pagination": {
    "page": 1,
    "per_page": 20,
    "total_items": 100,
    "total_pages": 5
  }
}
```

#### Update Entity (Full)
```http
PUT /api/v1/{entity}/{id}
Content-Type: application/json

{"name": "Alice Smith", "email": "alice.smith@example.com"}
```

#### Update Entity (Partial)
```http
PATCH /api/v1/{entity}/{id}
Content-Type: application/json

{"email": "newemail@example.com"}
```

#### Delete Entity
```http
DELETE /api/v1/{entity}/{id}
```

#### Save Entity (Upsert with Caller-Specified ID)
```http
POST /api/v1/{entity}/save/{id}
Content-Type: application/json

{"name": "Bob", "email": "bob@example.com"}
```

Creates the entity if it does not exist; overwrites it completely if it does.
Never returns a conflict error for a duplicate ID.

Responses:
- `201 Created` — new record created with the specified ID.
- `200 OK` — existing record replaced.

Use this endpoint when the caller controls the ID (e.g. importing records
with known keys, idempotent configuration writes, migration). For
server-assigned IDs use `POST /api/v1/{entity}` instead.

### Optimistic Concurrency (Conditional Writes)

All three write endpoints — `PUT /{entity}/{id}`, `PATCH /{entity}/{id}`, and
`POST /{entity}/save/{id}` — support conditional writes via an integer version
field embedded in every entity response.

**Reading the version**

Every `GET` response includes `"_version": N` in the entity body:

```json
{
  "id": 42,
  "state": "in-transit",
  "_version": 7
}
```

**Writing conditionally**

Include `"_version": N` in the request body to make the write conditional.
xolu checks the stored version inside the write transaction:

```http
POST /api/v1/tenant/acme/objects/save/device-001
Content-Type: application/json

{"state": "delivered", "_version": 7}
```

- `200 OK` / `201 Created` — stored version matched; version is now `8`.
- `409 Conflict` — stored version differed. Response body includes
  `"current_version"` so the caller can retry without an extra `GET`:

```json
{
  "error": {"code": "XOLU-ST005", "message": "Version conflict ...", "status": 409},
  "current_version": 8
}
```

Omitting `_version` from the request body makes the write unconditional —
existing behaviour is preserved.

**FSM / CAS pattern**

```
1. GET  /tenant/{t}/objects/{giai}           → {"state": "A", "_version": 7}
2. Compute transition A → B
3. POST /tenant/{t}/objects/save/{giai}      body: {"state": "B", "_version": 7}
   → 200 OK  (version is now 8)
   → 409 Conflict + current_version  (another writer got there first; re-read and retry)
```

No locking or inter-process coordination is required. SQLite's transaction
semantics guarantee the check-and-write is atomic on a single xolu instance.

### Multi-Tenant Operations

All entity operations support tenant isolation via URL prefix:

```http
GET /api/v1/tenant/{tenant_id}/{entity}
POST /api/v1/tenant/{tenant_id}/{entity}
GET /api/v1/tenant/{tenant_id}/{entity}/{id}
```

### Graph Operations

#### Shortest Path
```http
GET /api/v1/graph/shortestPath?from={entity}:{id}&to={entity}:{id}
```

#### Path Exists
```http
GET /api/v1/graph/pathExists?from={entity}:{id}&to={entity}:{id}
```

#### Common Neighbors
```http
GET /api/v1/graph/commonNeighbors?node1={entity}:{id}&node2={entity}:{id}
```

#### Node Information
```http
GET /api/v1/graph/node/{entity}:{id}
GET /api/v1/graph/node/{entity}:{id}/degree
GET /api/v1/graph/node/{entity}:{id}/neighbors?direction=out
```

### Search Operations

#### Full-Text Search (SQLite only)
```http
GET /api/v1/search?q={query}&entity={entity}
```

#### Field Search
```http
GET /api/v1/{entity}/search?field={field}&query={value}&match={type}
```

Match types: `exact`, `contains`, `prefix`, `suffix`

### Export Operations

```http
GET /api/v1/export
```

Returns a ZIP archive containing:
- `manifest.json` - Export metadata
- `xolu.db` - Entity data (the store database)
- `graph.json` - Graph structure (built from the in-database graph)

### Schema Operations

See [JSON Schema & Adapted Tables](docs/JSON_SCHEMA.md) for the full reference on supported keywords, adapted table layout, REF fields, decimal types, and schema evolution.

```http
GET /api/v1/schemas             # List registered entity types (v0.15.1)
GET /api/v1/schema/{entity}     # Retrieve schema for one entity
POST /api/v1/schema/{entity}    # Register or update schema; creates adapted table
```

### System Operations

```http
GET /health        # Health check
GET /version       # Version info
GET /metrics       # Prometheus metrics
```

### Timeseries Operations

Available only when `XOLU_TIMESERIES_ENABLED=true` and `XOLU_TENANT_MODE=strict`.
All timeseries endpoints are tenant-scoped under `/api/v1/tenant/{id}/ts/`.

Data is stored in per-tenant Pebble (LSM) instances with Zstd compression.
~30 bytes per event effective. See [Timeseries Design](docs/TIMESERIES_DESIGN_V3.md)
for the full specification.

#### Provisioning

```http
POST /api/v1/tenant/{id}/ts/provision
```

Enables timeseries storage for a tenant. Idempotent. The tenant must already
exist in the registry (`XOLU_TENANT_MODE=strict` requires this). Returns 201
on creation, 200 if already provisioned.

#### Timeline Management

Each tenant has up to 65535 named timelines (IDs 1–0xFFFF). A timeline
declares a fixed number of *dimensions* (1–5 uint64 values) used as the
sort key prefix. Dimensions are immutable after the first event is written.

```http
POST   /api/v1/tenant/{id}/ts/timelines              # Define a timeline
GET    /api/v1/tenant/{id}/ts/timelines              # List all timelines
GET    /api/v1/tenant/{id}/ts/timelines/{tid}        # Get a timeline
PATCH  /api/v1/tenant/{id}/ts/timelines/{tid}        # Update name / retention
GET    /api/v1/tenant/{id}/ts/timelines/{tid}/stats  # Timeline diagnostics
GET    /api/v1/tenant/{id}/ts/timelines/{tid}/sync   # Read nosync setting
POST   /api/v1/tenant/{id}/ts/timelines/{tid}/sync/on   # Enable WAL sync (default)
POST   /api/v1/tenant/{id}/ts/timelines/{tid}/sync/off  # Disable WAL sync (nosync mode)
# Rollup management
POST   /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/def                 # Define rollup
GET    /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/list                # List rollups for source
GET    /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/parent              # Get rollup parent
GET    /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/{rid}               # Get rollup def
DELETE /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/{rid}               # Delete rollup (cascades per XOLU_TS_ROLLUP_CASCADE_DELETE)
POST   /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/{rid}/run           # Run rollup manually
GET    /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/{rid}/status        # Worker status
GET    /api/v1/tenant/{id}/ts/rollup/tree                                # Full rollup tree
# Timeline data deletion
DELETE /api/v1/tenant/{id}/ts/timelines/{tid}/data                       # Clear all data (not definition)
POST   /api/v1/tenant/{id}/ts/timelines/{tid}/data/purge                 # Delete time range
```

Define request body:
```json
{
  "id": 1,
  "dims": 2,
  "name": "temperature",
  "retention_days": 90
}
```

`dims` is required on define and immutable after the first write. `name` and
`retention_days` can be changed freely via PATCH. `retention_days: 0` inherits
the store-level default (`XOLU_TS_DEFAULT_RETENTION_DAYS`); a negative value
disables expiry for the timeline.

#### Write Modes

Each timeline's write behaviour is controlled by two independent mechanisms that
can be changed at runtime without restarting the server.

**Per-timeline sync mode** controls whether each `AppendBatch` call waits for
the WAL to be flushed to durable storage before returning.

```http
# Disable WAL sync for timeline 3 (nosync mode — faster, less durable)
POST /api/v1/tenant/{id}/ts/timelines/3/sync/off

# Re-enable WAL sync for timeline 3 (default — durable)
POST /api/v1/tenant/{id}/ts/timelines/3/sync/on

# Read current setting
GET /api/v1/tenant/{id}/ts/timelines/3/sync
# → {"timeline_id": 3, "nosync": false}
```

With `nosync` off (default), events are on durable storage when the call
returns — crash safe at all levels. With `nosync` on, events survive a process
crash (they are in kernel page cache) but may be lost on a kernel panic or
power loss. The setting is persisted and survives server restarts. If a batch
mixes events from nosync and sync timelines, the entire batch commits with sync;
the stricter requirement always wins.

**Per-tenant write coalescing** routes events through a background goroutine
that groups them into batches, amortising one fsync across many concurrent
callers. This only helps when multiple goroutines are writing simultaneously.
With a single writer it adds up to `XOLU_TS_COAL_FLUSH_INTERVAL_MS` of latency
per call with no throughput benefit.

Coalescing is controlled via dynconfig, not the HTTP API, because it is a
store-level setting rather than a per-timeline one:

```http
# Enable coalescing for tenant "acme" via the admin API
PUT /admin/config/tenant.acme/ts.writecoal
Body: true

# Tune the flush window to 5ms for tenant "acme"
PUT /admin/config/tenant.acme/ts.coal_flush_interval_ms
Body: 5

# Enable coalescing globally for all tenants
PUT /admin/config/global/ts.writecoal
Body: true
```

Dynconfig changes are live — the coalescer reads the interval on every flush
tick and resets without restart. The tenant namespace (`tenant.{name}`) takes
precedence over `global`. See `TS-WRITE-MODES.md` for observed performance
under each mode combination.

#### Writing Events

```http
POST /api/v1/tenant/{id}/ts/events         # Single event
POST /api/v1/tenant/{id}/ts/events/batch   # Atomic batch (up to XOLU_TS_MAX_BATCH_SIZE)
```

Single event body:
```json
{
  "timeline": 1,
  "dims":     [42, 7],
  "time":     "2026-01-15T10:30:00Z",
  "nums":     [23.4, 1013.25],
  "payload":  "base64encodedopaquebytes"
}
```

`dims` must match the timeline's declared dimension count. `time` must be an
RFC 3339 timestamp at or after the Unix epoch. `nums` holds up to 7 float64
values (no NaN). `payload` is optional opaque bytes (up to 64 KB, base64).

Batch body:
```json
{ "events": [ ...event objects... ] }
```

A batch is atomic: if any event fails validation, no events are written.

#### Querying Events

```http
GET /api/v1/tenant/{id}/ts/events?timeline=1&dims=42,7&from=...&to=...
```

Query parameters: `timeline` (required), `dims` (required, comma-separated),
`from` / `to` (required, RFC 3339), `limit` (default 1000, max capped by
`XOLU_TS_MAX_QUERY_EVENTS`), `order` (`asc` or `desc`, default `asc`).

`dims` may be a *prefix*: supplying fewer values than the timeline's dimension
count returns all events matching that leading prefix across all remaining
dimension values. A Go-side time filter is applied to prevent out-of-range
events leaking through the prefix scan.

```http
GET /api/v1/tenant/{id}/ts/events/latest?timeline=1&dims=42,7&n=10
```

Returns the N most recent events (default 10, max capped by
`XOLU_TS_MAX_QUERY_EVENTS`) matching the dimension prefix.

#### Aggregation

```http
POST /api/v1/tenant/{id}/ts/aggregate
```

```json
{
  "timeline":  1,
  "dims":      [42],
  "from":      "2026-01-01T00:00:00Z",
  "to":        "2026-01-08T00:00:00Z",
  "function":  "avg",
  "num_field": 0,
  "interval":  "1h"
}
```

`function`: `avg`, `min`, `max`, `sum`, `count`. `num_field`: 0-based index
into the event's `nums` array (0–6). `interval`: one of `1m 5m 15m 30m 1h
6h 12h 1d 7d`. Omit `interval` for a scalar result. Partial-prefix `dims`
are supported with the same time filter as range queries.

#### Range Aggregate (single-pass, all fields)

```http
POST /api/v1/tenant/{id}/ts/range_aggregate
```

```json
{
  "timeline": 1,
  "dims":     [42],
  "from":     "2026-01-01T00:00:00Z",
  "to":       "2026-01-08T00:00:00Z"
}
```

Computes count, sum, avg, min, and max for **all seven numeric fields** in a
single Pebble scan pass. More efficient than issuing multiple `/aggregate`
calls when several fields are needed. No `function` or `num_field` parameter —
the result always covers all fields.

Response:

```json
{
  "timeline": 1,
  "count":  12500,
  "fields": [true, true, true, false, false, false, false],
  "sums":   [1230.5, 98432.1, 0.0, 0, 0, 0, 0],
  "avgs":   [0.098,  7.874,   0.0, 0, 0, 0, 0],
  "mins":   [0.001,  1.2,     0.0, 0, 0, 0, 0],
  "maxs":   [0.999,  99.9,    0.0, 0, 0, 0, 0]
}
```

`fields[i]` is `true` if field `i` was present in at least one event. Entries for absent fields are zero.

#### Full Aggregate (statistics + quantiles, single pass)

```http
POST /api/v1/tenant/{id}/ts/full_aggregate
```

```json
{
  "timeline":        1,
  "dims":            [42],
  "from":            "2026-01-01T00:00:00Z",
  "to":              "2026-01-08T00:00:00Z",
  "quantiles":       [0.5, 0.9, 0.99],
  "quantile_fields": [0, 1]
}
```

Single-pass combination of exact statistics (same as `range_aggregate`) plus
approximate quantile estimates (t-digest, compression=100) for selected fields.

`quantiles`: list of float64 values in [0, 1]. If absent or empty, no t-digest
is allocated and the result is identical to `range_aggregate`.

`quantile_fields`: which numeric fields (0–6) to estimate quantiles for. If
absent or null, all seven fields receive estimates.

Response:

```json
{
  "timeline":  1,
  "count":     12500,
  "fields":    [true, true, false, false, false, false, false],
  "sums":      [1230.5, 98432.1, 0, 0, 0, 0, 0],
  "avgs":      [0.098, 7.874, 0, 0, 0, 0, 0],
  "mins":      [0.001, 1.2, 0, 0, 0, 0, 0],
  "maxs":      [0.999, 99.9, 0, 0, 0, 0, 0],
  "quantiles": [[50.1, 90.3, 99.1], [500.2, 903.1, 991.4], null, null, null, null, null]
}
```

`quantiles[i]` is a slice of estimates for field `i`, one per requested
quantile, in the same order as the request's `quantiles` array. `null` when
field `i` was not requested or carried no events.

#### Rollup Management

Rollups maintain pre-aggregated summaries at configurable granularities so
dashboard queries scan a small fixed number of events regardless of time range.
All rollup timelines must be defined before rollup definitions are created.
Workers are not started automatically — trigger the first `/run` with
`cascade: true` to start the whole tree and optionally backfill history.
Timeline 0 is rejected on all rollup endpoints (`XOLU-TS022`).

```http
POST   /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/def
# Body: {"dest_tid": 101, "bucket_duration": "1m", "late_window": "10s"}
# Does not start the worker. Returns the assigned rollup ID.

GET    /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/list
GET    /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/parent
GET    /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/{rid}
DELETE /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/{rid}
# Cascade behaviour controlled by XOLU_TS_ROLLUP_CASCADE_DELETE (default true).

POST   /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/{rid}/run
# Body (optional): {"from":"2026-01-01T00:00:00Z","to":"2026-06-01T00:00:00Z","cascade":true}
# cascade:true starts all descendant workers and backfills their windows.

GET    /api/v1/tenant/{id}/ts/timelines/{tid}/rollup/{rid}/status
GET    /api/v1/tenant/{id}/ts/rollup/tree
```

Rollup event value layout: `val0`=mean[0], `val1`=min[0], `val2`=max[0],
`val3`=sum[0], `val4`=count, `val5`=mean[1], `val6`=mean[2].

See `docs/ROLLUP-SPEC.md` for the full specification including tree
constraints, cascade semantics, and deployment examples.

#### Timeline Data Deletion

```http
DELETE /api/v1/tenant/{id}/ts/timelines/{tid}/data
# Removes all events. Timeline definition is preserved. Timeline 0 rejected.

POST   /api/v1/tenant/{id}/ts/timelines/{tid}/data/purge
# Body: {"from":"2026-06-01T00:00:00Z","to":"2026-06-15T00:00:00Z"}
# Removes events in [from, to). Timeline 0 rejected.
```

#### Retention and Diagnostics

```http
GET   /api/v1/tenant/{id}/ts/retention    # View store-level default retention
PATCH /api/v1/tenant/{id}/ts/retention    # Update store-level default retention
GET   /api/v1/tenant/{id}/ts/stats        # Tenant store diagnostics
```

Retention PATCH body: `{ "default_retention_days": 90 }`. Setting `0` disables expiry at the store level; per-timeline retention is updated via `PATCH /ts/timelines/{id}`.

Stats response includes `timelines` (count) and `disk_bytes` (Pebble estimate).
Per-timeline stats (via `GET /ts/timelines/{tid}/stats`) include
`total_events` (approximate — eventually consistent after crash),
`oldest_event`, and `newest_event`.

#### Error Codes

| Code | HTTP | Meaning |
|------|------|---------|
| XOLU-TS002 | 404/405 | Timeseries not enabled |
| XOLU-TS003 | 400 | Tenant not provisioned for timeseries |
| XOLU-TS004 | 400 | Timeline not defined |
| XOLU-TS005 | 400 | Timestamp before Unix epoch or invalid format |
| XOLU-TS006 | 400 | Batch exceeds `XOLU_TS_MAX_BATCH_SIZE` |
| XOLU-TS007 | 400 | Dimension count mismatch |
| XOLU-TS008 | 400 | Unknown aggregate function |
| XOLU-TS009 | 400 | `num_field` out of range (0–6) |
| XOLU-TS010 | 400 | Invalid interval value |
| XOLU-TS011 | 400 | Query window exceeds `XOLU_TS_MAX_RANGE_DAYS` |
| XOLU-TS013 | 400 | Scan aborted — exceeded `XOLU_TS_MAX_SCAN_EVENTS` |
| XOLU-TS016 | 409 | Attempt to change dims after first write |
| XOLU-TS017 | 400 | NaN in numeric field |
| XOLU-TS018 | 400 | Reserved timeline ID (0) |
| XOLU-TS019 | 400 | Aggregate bucket limit exceeded `XOLU_TS_MAX_AGGREGATE_BUCKETS` |
| XOLU-TS020 | 400 | Invalid write config request (missing or unrecognised field) |
| XOLU-TS021 | 500 | Write config could not be persisted to disk |
| XOLU-TS022 | 400 | Timeline 0 used in rollup or data deletion operation |
| XOLU-TS023 | 400 | Rollup definition would create a cycle |
| XOLU-TS024 | 400 | Rollup definition would exceed `ts.rollup_max_depth` |
| XOLU-TS025 | 404 | Rollup definition not found |
| XOLU-TS026 | 400/409 | Destination already targeted by another rollup (400); definition has descendants and `XOLU_TS_ROLLUP_CASCADE_DELETE=false` (409) |

---

## Platform Extensions (`/api/v2`)

Everything added after the 1.0 stability commitment lives under `/api/v2`, so the `/api/v1` contract stays stable while these subsystems evolve. Each is a first-class persistence-layer primitive rather than an application-level convention. This section is an operational overview; the detailed endpoint and model references are in the linked documents.

| Endpoint | Subsystem | Purpose |
|----------|-----------|---------|
| `/api/v2/fsm/def` | FSM definitions | Immutable executable state-machine specifications |
| `/api/v2/fsm/machine` | FSM machines | Running machine instances |
| `/api/v2/event/def` | Event definitions | Reactive definitions wiring subsystems to webhooks/actions |
| `/api/v2/meta` | Entity metadata | Per-entity key/value sidecar with entity-scoped lifecycle (TTL) |
| `/api/v2/gen` | Generators | Stateless and named value generators (UUID, sequence, token, timestamp, pick) |
| `/api/v2/gen/seq` (GET) | Sequence listing | Enumerate the tenant's named sequences (v0.15.1) |
| `/api/v2/seq` | Sequences | Convenience alias for `/api/v2/gen/seq` |

### Finite State Machines

The FSM subsystem moves state machines from the application layer into the persistence layer. A definition (`POST /api/v2/fsm/def`) is an immutable, executable specification: states, inputs, transitions, guard expressions, and an output alphabet. A machine (`POST /api/v2/fsm/machine`) is a running instance of a definition. Transition legality is enforced at write time — an illegal state change is rejected at the same layer that rejects an invalid field type, not by application discipline.

Three usage patterns are supported without forcing any into the shape of the others: a pure state transition (no document involved); a combined data-plus-state transition that participates in `/api/v1/commit` (both the write and the transition succeed or neither does); and an ordinary document write with no transition. See [FSM.md](docs/FSM.md) for the conceptual model, the three-level determinism declaration, and guard/set expression semantics, and [API_V2.md](docs/API_V2.md) for the endpoint reference.

### Events

Event definitions (`POST /api/v2/event/def`) declare reactions: when a given FSM transition or commit completes, a payload is delivered to a webhook. Two FSM latches are available — `fsm.output` (one event per Mealy emission) and `fsm.step` (one event per committed transition, carrying the state delta) — plus `commit.applied`, which fires once after a commit transaction succeeds, carrying the affected entity set.

Every notification is wrapped in an `{origin, message}` envelope. `origin` is stamped by xolu on each delivery (agent, agent version, event-def ID, latch kind and source, and the post-commit `fired_at` timestamp); `message` is the rendered body. Delivery is asynchronous, at-most-once, single-attempt — there is no retry, backoff, dead-letter, or replay — and each firing is recorded in the delivery log (`GET /api/v2/event/def/{id}/log`). Payloads may be rendered with [jsonplate](docs/jsonplate.md), a JSON-template form whose `{"$ref": "path"}` leaves resolve against the event data. See [EVENT_MODEL.md](docs/EVENT_MODEL.md) for the full model and the deliberate Part 1 boundaries.

### Entity Metadata

`/api/v2/meta` provides a per-entity key/value sidecar with an entity-scoped lifecycle, including TTL-based expiry swept in the background. It stores operational metadata alongside an entity without widening the entity's own schema. See [API_V2.md](docs/API_V2.md).

### Generators and Sequences

`/api/v2/gen` produces values from named and stateless generators — UUIDs, tokens, timestamps, a `pick` from a set, and monotonic sequences. Sequences are also reachable as the alias `/api/v2/seq`, and within OQL via `NEXT VALUE FOR` and the `@SEQ('name')` form, which atomically increments the named sequence. See [API_V2.md](docs/API_V2.md).

---

## Query Languages


### OQL (xolu Query Language)

SQL-like query language for entities.

```http
POST /api/v1/oql/query
Content-Type: application/json

{"query": "SELECT * FROM users WHERE age > 25 ORDER BY name LIMIT 10"}
```

#### Supported Features

- `SELECT` with field selection and `*`
- `WHERE` with operators: `=`, `!=`, `>`, `<`, `>=`, `<=`, `LIKE`, `IN`, `BETWEEN`, `IS NULL`
- `ORDER BY` with `ASC`/`DESC`
- `LIMIT` and `OFFSET`
- `GROUP BY` with aggregates: `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`
- `DISTINCT`
- `INNER`, `LEFT`, `RIGHT`, and `FULL OUTER JOIN` (two tables, SQLite store)
- `INSERT`, `UPDATE`, `DELETE`

#### Examples

```sql
-- Basic query
SELECT name, email FROM users WHERE status = 'active'

-- Aggregation
SELECT department, COUNT(*) as count, AVG(salary) as avg_salary
FROM employees
GROUP BY department

-- Pattern matching
SELECT * FROM products WHERE name LIKE '%widget%'

-- Sorting and pagination
SELECT * FROM orders ORDER BY created_at DESC LIMIT 20 OFFSET 40

-- JOIN: orders with customer name
SELECT a.amount, b.name AS customer
FROM orders AS a
INNER JOIN customers AS b ON a.customer_id = b.id
WHERE a.status = 'pending'
```

#### Not supported

CROSS JOIN, three-or-more-table joins, subqueries, CTEs, window functions.
JOINs require the SQLite store. See `docs/OQL_API.md` for the full feature
matrix.

#### Async Queries

For long-running queries:

```http
POST /api/v1/oql/async
Content-Type: application/json

{"query": "SELECT * FROM large_table"}
```

Returns job ID:
```json
{"job_id": "abc123", "status": "pending"}
```

Check status:
```http
GET /api/v1/oql/job/{job_id}
```

### Sulpher (Graph Query Language)

Sulpher is xolu's graph query language, implementing a substantial subset of
openCypher 9 (OC9). Queries are submitted as Cypher strings; the executor
walks the graph in memory using BFS or DFS and returns structured result rows.

```http
POST /api/v1/graph/query
Content-Type: application/json

{"query": "MATCH (u:user {id: '1'})-[:knows]->(f:user) RETURN f.name"}
```

#### Algorithm hint

By default Sulpher uses BFS. To select DFS, add a leading comment:

```cypher
// sulpher.algorithm: dfs
MATCH (u:user)-[:knows]->(f) RETURN f
```

#### Pattern matching

```cypher
-- Single hop (outgoing)
MATCH (u:user)-[:knows]->(f:user) RETURN f.name

-- Single hop (incoming)
MATCH (u:user)<-[:knows]-(f:user) RETURN f.name

-- Undirected
MATCH (u:user)-[:knows]-(f:user) RETURN f.name

-- Variable-length (1 to 3 hops)
MATCH (u:user)-[:knows*1..3]->(f:user) RETURN f.name

-- Any edge type, unlimited depth
MATCH (u:user)-[*]->(f) RETURN f
```

#### Filtering with WHERE

```cypher
-- Comparison
MATCH (u:user) WHERE u.age >= 18 RETURN u.name

-- String predicates
MATCH (u:user) WHERE u.email STARTS WITH 'alice' RETURN u

-- IS NULL / IS NOT NULL
MATCH (u:user) WHERE u.email IS NOT NULL RETURN u.name

-- IN list
MATCH (u:user) WHERE u.role IN ['admin', 'owner'] RETURN u.name

-- AND / OR / NOT
MATCH (u:user) WHERE u.age > 18 AND NOT u.active = false RETURN u
```

#### RETURN enhancements

```cypher
-- Property access
MATCH (u:user) RETURN u.name, u.age

-- Alias
MATCH (u:user) RETURN u.name AS username

-- Whole-node
MATCH (u:user) RETURN u

-- All bound variables
MATCH (u:user)-[:knows]->(f) RETURN *

-- Arithmetic
MATCH (u:user) RETURN u.age + 1 AS nextAge

-- DISTINCT / ORDER BY / SKIP / LIMIT
MATCH (u:user) RETURN DISTINCT u.role ORDER BY u.name SKIP 5 LIMIT 10
```

#### Aggregation

Cypher implicit GROUP BY: non-aggregate items are grouping keys.

```cypher
-- Count all
MATCH (u:user) RETURN count(*) AS total

-- Count non-null property
MATCH (u:user) RETURN count(u.email) AS withEmail

-- Group by + count
MATCH (u:user) RETURN u.role, count(u) AS n ORDER BY n DESC

-- collect / avg / sum / min / max
MATCH (u:user) WHERE u.age IS NOT NULL
RETURN u.active, collect(u.name) AS names, avg(u.age) AS meanAge

-- DISTINCT aggregate
MATCH (u:user) RETURN count(DISTINCT u.role) AS distinctRoles
```

#### OPTIONAL MATCH

Left-join semantics: unmatched optional variables are null.

```cypher
MATCH (u:user)
OPTIONAL MATCH (u)-[:knows]->(f:user)
RETURN u.name, f.name
```

#### WITH pipeline

Chain two MATCH phases, filtering between them.

```cypher
MATCH (a:user {id: '1'})-[:knows]->(f:user)
WITH f
WHERE f.active = true
MATCH (f)-[:knows]->(b:user)
RETURN f.name, b.name
```

#### shortestPath

```cypher
-- Shortest path between two nodes (returns path object)
MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '5'}))
RETURN p.length, p.nodes, p.relationships

-- All-pairs within a type
MATCH p = shortestPath((a:user)-[:knows*]->(b:user))
WHERE ALL(n IN nodes(p) WHERE n.active = true)
RETURN p.length
```

Path object properties: `p.nodes` (list of node maps), `p.relationships`
(list of `{"type": label}` maps), `p.length` (number of hops).

#### Path comprehension

```cypher
MATCH p = shortestPath((a:user {id: '1'})-[:knows*]->(b:user))
WHERE ALL(n IN nodes(p) WHERE n.active = true)
  AND NONE(n IN nodes(p) WHERE n.role = 'banned')
RETURN b.name, p.length

-- List comprehension on path nodes
MATCH p = shortestPath((a:user)-[:knows*]->(b:user))
RETURN [n IN nodes(p) WHERE n.active = true | n.name] AS activeNames
```

#### UNWIND

```cypher
-- Expand a list into rows
UNWIND [1, 2, 3] AS x RETURN x * 2 AS doubled

-- UNWIND then traverse
UNWIND ['alice', 'bob'] AS name
MATCH (u:user {name: name})
RETURN u
```

#### UNION / UNION ALL

```cypher
MATCH (u:user {id: '1'}) RETURN u.name AS name
UNION
MATCH (u:user {id: '2'}) RETURN u.name AS name

-- UNION ALL preserves duplicates
MATCH (u:user {id: '1'}) RETURN u.name AS name
UNION ALL
MATCH (u:user {id: '1'}) RETURN u.name AS name
```

#### Built-in functions

`nodes(p)`, `relationships(p)`, `length(list)`, `size(list)`, `head(list)`,
`last(list)`, `tail(list)`, `coalesce(a, b)`, `toUpper(s)`, `toLower(s)`,
`trim(s)`, `toString(v)`, `toInteger(v)`, `toFloat(v)`, `abs(v)`,
`type(r)`, `id(n)`, `labels(n)`, `exists(v)`.

#### Relationship types in xolu

Relationship labels are derived from REF field keys. A field
`"knows": {"type": "REF", "entity": "user", "id": 7}` creates an edge with
label `"knows"`. Use the field key as the relationship type in Cypher patterns.
Relationship properties are not stored; filtering on `r.since` etc. is not
supported.

For the full language reference including all supported syntax, predicates, functions, and known gaps, see [Sulpher Query Reference](docs/SULPHER_QUERY_REFERENCE.md).

---

## Authentication

As of v0.15.2 the authentication middleware lives in `pkg/authmw` with its
configuration subset in `pkg/authconfig`, importable by external binaries
(the molu hub) without the full server config; the server wires it from
`config.(*Config).AuthConfig()` at startup. `pkg/middleware` retains
compatibility aliases. Behaviour is unchanged.

### JWT Authentication

```bash
export XOLU_AUTH_TYPE=jwt
export XOLU_JWT_SECRET=your-secret-key-min-32-chars
export XOLU_JWT_ISSUER=your-app  # Optional
```

Request with JWT:
```http
GET /api/v1/users
Authorization: Bearer eyJhbGciOiJIUzI1NiIs...
```

JWT requirements:
- Algorithm: HS256
- Claims: `sub` (subject), `exp` (expiration)
- Optional: `iss` (issuer), `nbf` (not before)

### API Key Authentication

```bash
export XOLU_AUTH_TYPE=apikey
export XOLU_API_KEYS=key1,key2,key3
```

Request with API key:
```http
GET /api/v1/users
X-API-Key: key1
```

Or:
```http
GET /api/v1/users
Authorization: ApiKey key1
```

### Bearer Token Authentication

```bash
export XOLU_AUTH_TYPE=bearertoken
export XOLU_INTERNAL_TOKEN=your-shared-secret-token
```

Request with bearer token:
```http
GET /api/v1/users
Authorization: Bearer your-shared-secret-token
```

This mode validates the `Authorization: Bearer <token>` header against the
static `XOLU_INTERNAL_TOKEN` value. It is intended for machine-to-machine use
where a shared secret is sufficient and JWT lifecycle management is unnecessary.

### Excluded Paths

By default, these paths don't require authentication:
- `/health`
- `/version`
- `/metrics`

---

## Rate Limiting

Enable rate limiting to protect your API:

```bash
export XOLU_RATE_LIMIT_ENABLED=true
export XOLU_RATE_LIMIT_RATE=100      # requests
export XOLU_RATE_LIMIT_WINDOW=60     # seconds
export XOLU_RATE_LIMIT_BY_IP=true
```

### Response Headers

All responses include rate limit headers:

```http
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 95
X-RateLimit-Reset: 1704067200
```

### Rate Limited Response

When limit exceeded:
```http
HTTP/1.1 429 Too Many Requests
Retry-After: 45

{"error": "Too Many Requests", "message": "Rate limit exceeded", "retry_after": 45}
```

---

## Metrics & Monitoring

### Prometheus Endpoint

```http
GET /metrics
```

### Available Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `xolu_uptime_seconds` | gauge | Server uptime |
| `xolu_requests_total` | counter | Total HTTP requests |
| `xolu_requests_by_status_total` | counter | Requests by status code |
| `xolu_request_errors_total` | counter | Total 4xx/5xx responses |
| `xolu_active_requests` | gauge | Current in-flight requests |
| `xolu_request_duration_seconds_bucket` | histogram | Request latency distribution |
| `xolu_entity_operations_total` | counter | CRUD operations by type |
| `xolu_cache_total` | counter | Cache hits/misses |
| `xolu_queries_total` | counter | Query operations by type |

### JSON Format

```http
GET /metrics
Accept: application/json
```

### Prometheus Configuration

```yaml
scrape_configs:
  - job_name: 'xolu'
    static_configs:
      - targets: ['localhost:8080']
```

---

## Storage Backends

### SQLite Storage

Production-ready storage with ACID guarantees, WAL mode, and read/write connection pool split.

```bash
export XOLU_STORAGE_TYPE=sqlite
export XOLU_BASE_DIR=data
export XOLU_FULLTEXT_ENABLED=true
```

**Advantages:**
- ACID transactions with WAL mode for concurrent reads
- Separate reader and writer connection pools
- Full-text search support (FTS5)
- Adaptive lock contention monitoring
- Single-file database

#### Read/Write Connection Pool Split

SQLite in WAL mode supports concurrent readers alongside a single writer, but only if they use separate database connections. xolu maintains two connection pools:

**Writer pool** (`XOLU_SQLITE_MAX_OPEN_CONNS`, default: 1): Handles all INSERT, UPDATE, DELETE, and transaction operations. Default of 1 matches SQLite's single-writer constraint under WAL. A future PostgreSQL backend would use a higher default.

**Reader pool** (`XOLU_SQLITE_READ_POOL_SIZE`, default: NumCPU): Handles all SELECT, COUNT, and search queries. Uses `PRAGMA query_only=ON` to prevent accidental writes. Scales with available CPU cores.

Both pools share identical WAL, synchronous, cache, and busy_timeout pragmas. Pool size defaults are 0, meaning "let the backend decide" — this keeps the configuration backend-neutral for future storage backends.

#### Adaptive Concurrency

Under high write contention, SQLite returns SQLITE_BUSY. xolu's adaptive lock monitors contention rates and automatically backs off when the threshold is exceeded (`XOLU_SQLITE_CONTENTION_THRESHOLD`, default 95%). This prevents cascading failures under burst write loads.

### Storage Layout

xolu's on-disk layout is derived from a single configurable knob — the data root, set by `--base-dir` (or `XOLU_BASE_DIR`, default `data`). There is no separate database-path setting: every store, the time-series plane, schemas, blobs, and runtime config are placed by a fixed invariant beneath the data root.

```
<data-root>/
  t0000/store/xolu.db   t0000/ts/      tenant 0 (per-file mode)
  tNNNN/store/xolu.db   tNNNN/ts/      registered tenants (per-file mode)
  shared/store/xolu.db  shared/ts/     shared-tenancy mode
  schema/                             entity schemas
  blobs/                              blob store
  dynconfig.json                      runtime configuration
```

In per-file tenant mode (`XOLU_SQLITE_PER_FILE_TENANTS=true`) each tenant has its own store at `tNNNN/store/xolu.db`, with tenant 0 at `t0000/`. Otherwise a single shared store lives under `shared/`. The tenant segment is the tenant ID as four uppercase hex digits (`t0001`, `t00ff`, …).

**Inspecting the layout.** The `layout-recon` subcommand walks the data root, prints the structure annotated against the invariant, and exits non-zero if anything does not conform:

```bash
xolu --base-dir data layout-recon
```

**Migration safety.** On startup xolu refuses to run against a data root written by a pre-normalization layout (a base store at the root, or the old backend-first `sql/` or `ts/` groupings) rather than silently creating fresh stores beside the old data. If this happens, run `layout-recon` to inspect the directory, then migrate the data or point `--base-dir` at a fresh directory.

The path-derivation rules are owned by `pkg/storelayout`, which is the single authority for both producing and validating the layout.

---

## Graph Features

### Reference Format

Create relationships using REF objects:

```json
{
  "name": "Alice",
  "manager": {
    "type": "REF",
    "entity": "users",
    "id": 42
  },
  "department": {
    "type": "REF",
    "entity": "departments",
    "id": 5
  }
}
```

### Automatic Graph Sync

References are automatically:
- Added to graph on entity creation
- Updated when entity is modified
- Removed when entity is deleted

### Cycle Detection

Configure cycle handling:

```bash
export XOLU_GRAPH_CYCLE_DETECTION=warn   # Log warning, allow
export XOLU_GRAPH_CYCLE_DETECTION=error  # Reject edge creation
export XOLU_GRAPH_CYCLE_DETECTION=ignore # Allow silently
```

### Reference Embedding

Fetch entities with references resolved:

```http
GET /api/v1/users/1
```

Returns:
```json
{
  "id": 1,
  "name": "Alice",
  "manager": {
    "id": 42,
    "name": "Bob",
    "manager": {
      "id": 10,
      "name": "Carol"
    }
  }
}
```

Control embedding:
```http
GET /api/v1/users/1?embed=false
GET /api/v1/users/1?embed_depth=1
```

### Cascading Deletes

When enabled, deleting an entity also deletes entities that reference it:

```bash
export XOLU_CASCADING_DELETE=true
```

---

## Testing & Benchmarks

### Running Tests

```bash
make test           # Quick tests
make test-v         # Verbose output
make test-race      # With race detector
make test-full      # Full suite + stress tests
make coverage       # With coverage report
```

### Package Tests

```bash
make test-storage   # Storage tests
make test-sqlite    # SQLite-specific
make test-server    # HTTP server tests
make test-graph     # Graph operations
make test-oql       # OQL parser/executor
make test-sulpher   # Sulpher queries
```

### Benchmarks

```bash
make bench          # All benchmarks
make bench-storage  # Storage benchmarks
make bench-server   # HTTP benchmarks
```

### Stress Tests

```bash
make stress         # 10k record stress test
make stress-race    # With race detector
```

---

## Deployment

### Docker Compose Profiles

xolu ships with a multi-profile `docker-compose.yml` for different scenarios:

```bash
# Basic: memory cache, no auth
docker compose up

# With Redis cache
docker compose --profile redis up

# Full features: Redis, SQLite+FTS, auth, rate limiting, metrics
docker compose --profile full up

# Run integration tests
docker compose --profile test up
```

Or use the Makefile shortcuts:

```bash
make docker-up          # Basic
make docker-up-redis    # With Redis
make docker-up-full     # All features
make docker-test        # Integration tests
make docker-down        # Stop all
make docker-clean       # Stop and remove volumes
```

### Building the Docker Image

```bash
make docker-build
```

This builds `xolu:latest` using Go 1.22. No CGO is required — `modernc.org/sqlite` is a pure-Go SQLite port.

### Development Configuration

For local development with minimal setup:

```yaml
services:
  xolu:
    build: .
    ports:
      - "8080:8080"
    environment:
      - XOLU_STORAGE_TYPE=sqlite
      - XOLU_BASE_DIR=/app/data
      - XOLU_CACHE_TYPE=memory
      - XOLU_AUTH_TYPE=none
      - XOLU_GRAPH_MODE=flat
    volumes:
      - ./data:/app/data
      - ./schema:/app/schema
```

### Production Configuration

For production with all security and performance features:

```yaml
services:
  xolu:
    image: xolu:latest
    ports:
      - "9090:9090"
    environment:
      # Storage
      - XOLU_STORAGE_TYPE=sqlite
      - XOLU_BASE_DIR=/app/data
      - XOLU_FULLTEXT_ENABLED=true
      # Cache
      - XOLU_CACHE_TYPE=redis
      - XOLU_CACHE_TTL=300
      - XOLU_REDIS_HOST=redis
      - XOLU_REDIS_PORT=6379
      # Graph
      - XOLU_GRAPH_MODE=flat
      - XOLU_GRAPH_CYCLE_DETECTION=error
      # Authentication
      - XOLU_AUTH_TYPE=apikey
      - XOLU_API_KEYS=${API_KEYS}
      # Rate limiting
      - XOLU_RATE_LIMIT_ENABLED=true
      - XOLU_RATE_LIMIT_RATE=100
      - XOLU_RATE_LIMIT_WINDOW=60
      - XOLU_RATE_LIMIT_BY_KEY=true
      # Metrics
      - XOLU_METRICS_ENABLED=true
      # Multi-tenancy
      - XOLU_TENANT_MODE=strict
      - XOLU_TENANT_AUTO_REGISTER=false
    volumes:
      - xolu-data:/app/data
      - ./schema:/app/schema
    depends_on:
      - redis
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3

  redis:
    image: redis:7-alpine
    volumes:
      - redis-data:/data
    command: redis-server --appendonly yes
    restart: unless-stopped

volumes:
  xolu-data:
  redis-data:
```

### Testing with Containers

#### Unit Tests with Redis

```bash
# Run Redis cache tests (starts/stops Redis automatically)
make test-redis

# Run Redis stress tests (concurrent access, large payloads)
make test-redis-stress
```

#### Integration Tests

The `make docker-test` target runs a comprehensive integration test suite:

```bash
make docker-test
```

Tests include:
- Health, version, and Prometheus metrics endpoints
- Authentication (API key validation)
- Entity CRUD operations
- Full-text search (SQLite FTS)
- Graph operations
- Multi-tenancy (tenant-scoped routes)
- Rate limiting (verifies 429 responses)

Expected output:
```
========================================
Running xolu Integration Tests
========================================

--- Health & System Endpoints ---
✓ Health check (200)
✓ Version (200)
✓ Metrics (Prometheus) (200)

--- Authentication ---
✓ No auth rejected (401)
✓ Bad API key rejected (401)
✓ Valid API key accepted (200)

--- Entity CRUD ---
✓ Create entity (201)
✓ Get entity (200)
✓ List entities (200)
✓ Update entity (200)
✓ Patch entity (200)
✓ Save entity — create (201)
✓ Save entity — overwrite (200)

--- Full-Text Search (SQLite FTS) ---
✓ FTS search (200)

--- Graph Operations ---
✓ Graph stats (200)

--- Multi-Tenancy ---
✓ Tenant create (201)
✓ Tenant list (200)

--- Cleanup ---
✓ Delete entity (200)

--- Rate Limiting ---
✓ Rate limiting triggered (429)

========================================
Results: 17 passed, 0 failed
========================================
```

### Reverse Proxy (nginx)

```nginx
upstream xolu {
    server 127.0.0.1:9090;
}

server {
    listen 443 ssl;
    server_name api.example.com;

    ssl_certificate /etc/ssl/certs/api.crt;
    ssl_certificate_key /etc/ssl/private/api.key;

    location / {
        proxy_pass http://xolu;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

### Health Checks

```bash
# Simple health check
curl -f http://localhost:8080/health

# Version info
curl http://localhost:8080/version

# Prometheus metrics
curl http://localhost:8080/metrics
```

Kubernetes probes:

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 9090
  initialDelaySeconds: 5
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /health
    port: 9090
  initialDelaySeconds: 3
  periodSeconds: 5
```

### Backup and Restore

#### SQLite Backup

```bash
# Using sqlite3
sqlite3 /app/data/xolu.db ".backup /backup/xolu-$(date +%Y%m%d).db"

# Using export endpoint (includes graph data)
curl http://localhost:8080/api/v1/export > backup-$(date +%Y%m%d).zip
```

#### Timeseries Backup

Timeseries backup tooling is **not yet available**. The `iolu` binary
referenced in the timeseries design documents has not been implemented.

If timeseries is enabled, the underlying Pebble data directories can be
backed up manually by copying each tenant's directory while the server is
stopped. Pebble SSTables are immutable, so a filesystem-level copy of a
quiescent directory is consistent. However, copying while the server is
running risks capturing an inconsistent state.

For production use with timeseries enabled, plan for a brief maintenance
window (stop the server, copy the directories, restart) until checkpoint-
based backup tooling is shipped.

See [Timeseries Design](docs/TIMESERIES_DESIGN_V3.md) Section 13 for the
planned backup architecture using Pebble checkpoints.

#### Scheduled Backup (cron)

```bash
0 2 * * * docker exec xolu sqlite3 /app/data/xolu.db ".backup /backup/xolu-daily.db"
```

### Scaling Considerations

**Single instance with SQLite (recommended for most use cases):**
- Read/write split provides concurrent reads with WAL mode
- SQLite handles ~2,100 writes/sec (single-writer) and 16,000+ reads/sec
- Memory cache is sufficient for single instances
- Capacity for 10,000 to 1,900,000 IoT sensors depending on reporting interval

**SQLite capacity estimates (single instance):**

| Reporting interval | Max sensors | Binding constraint |
|--------------------|-------------|-------------------|
| 1 second | ~10,000 | Write throughput (~2,100 w/s) |
| 5 seconds | ~50,000 | Write throughput |
| 30 seconds | ~300,000 | Working set / cache pressure |
| 5 minutes | ~1,000,000 | Database size (~50 GB) |
| 15 minutes | ~1,900,000 | Database size (~100 GB) |

**Multiple instances:**
- Use Redis cache for shared state
- Each instance needs its own SQLite database (sharing a single file across instances is not supported)
- See [Fleet Architecture](docs/FLEET_ARCHITECTURE.md) for multi-instance deployment with tenant placement

---

## Implementation Notes

### Cache Backends

xolu supports two cache backends:

#### Memory Cache (Default)

Simple in-process LRU cache. Good for development and single-instance deployments.

```bash
export XOLU_CACHE_TYPE=memory
export XOLU_CACHE_TTL=300
```

**Characteristics:**
- Fast (no network overhead)
- Not shared between instances
- Lost on restart
- Uses global TTL (per-item TTL not supported)

#### Redis Cache

Production-grade distributed cache. Use when running multiple instances or when you need per-item TTL control.

```bash
export XOLU_CACHE_TYPE=redis
export XOLU_CACHE_TTL=300
export XOLU_REDIS_HOST=localhost
export XOLU_REDIS_PORT=6379
```

**Characteristics:**
- Shared across all xolu instances
- Survives restarts (if Redis persistence enabled)
- Supports per-item TTL
- Network latency on every operation
- Requires Redis infrastructure

#### When to Use Redis

- Running multiple xolu instances behind a load balancer
- Need cache to survive restarts
- Need per-item TTL control
- Want to inspect cache contents via redis-cli

For single-instance deployments or development, the memory cache is simpler and faster.

### OQL Entity Discovery

OQL validates that entity types exist before executing queries. Entity discovery happens automatically:

1. On startup, OQL scans the schema directory for entity folders
2. When a query references an unknown entity, OQL automatically rescans the directory
3. If the entity still doesn't exist, the query fails with "entity does not exist"

This means **newly created entity types are recognised automatically** without server restart or manual refresh. The first query against a new entity type may incur a small overhead for the rescan.

### Multi-Tenancy

xolu supports two operational modes for tenant isolation.

#### Operational Modes

| Mode | CRUD | REFs | OQL | FTS | Graph | Non-tenant routes |
|------|------|------|-----|-----|-------|-------------------|
| `path` (single-tenant) | \u2713 | \u2713 | \u2713 | \u2713 | \u2713 | Available (default store) |
| `strict` (multi-tenant) | \u2713 | \u2713 | \u2713 | \u2713 | \u2713 | Blocked |

In strict mode, graph queries are available exclusively via tenant-scoped routes
(`/api/v1/tenant/{tenant_id}/graph/...`). The graph layer is fully tenant-isolated:

- Node IDs in requests and responses use the client-facing `entity:id` format; the internal `XXXX@entity:id` prefix is added and stripped transparently
- Graph traversal operates on a per-request snapshot that contains only the requesting tenant's nodes and edges; cross-tenant edges are detected and excluded with a WARN log
- All 12 handler surfaces (stats, nodeInfo, nodeDegree, in/out edges, path, neighbors, shortestPath, pathExists, commonNeighbors, nodeSearch, Sulpher sync and async) are covered by an adversarial isolation test suite
- Non-tenant graph routes (`/api/v1/graph/...`) are blocked in strict mode along with all other non-tenant routes

Configure via environment:
```bash
export XOLU_TENANT_MODE=strict
export XOLU_TENANT_AUTO_REGISTER=false
```

#### Tenant Scoping Architecture

Tenant isolation is enforced at the **storage layer**. Each tenant gets a scoped `Store` instance that filters all operations by `tenant_id`. This means:

- Every CRUD operation (Create, Get, List, Update, Patch, Delete) is scoped to the tenant's store
- OQL queries (both sync and async) execute against the tenant-scoped store
- OQL SQL push-down includes `AND tenant_id = ?` in generated SQL
- Async OQL jobs capture the tenant-scoped store at submission time, so background goroutines execute in the correct scope
- Full-text search queries include tenant_id filtering
- REF resolution only resolves references within the same tenant

#### Auto-Registration

In `path` mode, the `XOLU_TENANT_AUTO_REGISTER` flag controls whether unknown tenant names in the URL automatically create new tenants:

| `XOLU_TENANT_AUTO_REGISTER` | Behaviour |
|-----------------------------|-----------|
| `true` | `/api/v1/tenant/new-name/...` creates tenant "new-name" on first access |
| `false` (default) | Unknown tenants return 404 |

In `strict` mode, auto-registration is ignored; tenants must be pre-registered.

#### Security Model

This is **application-level isolation** designed for trusted environments (internal services, not adversarial internet clients). The isolation model prevents accidental cross-tenant data access in normal CRUD, OQL, and search flows.

It is not a compliance-grade security boundary. For hostile multi-tenancy, use separate xolu instances per tenant with separate databases.

#### Example Usage

```bash
# Create entity in tenant "acme"
curl -X POST http://localhost:8080/api/v1/tenant/acme/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Alice"}'

# List only acme's users
curl http://localhost:8080/api/v1/tenant/acme/users

# OQL scoped to tenant
curl -X POST http://localhost:8080/api/v1/tenant/acme/oql/query \
  -d '{"query": "SELECT * FROM users WHERE status = '"'"'active'"'"'"}'

# In strict mode, non-tenant routes return 403:
curl http://localhost:8080/api/v1/users
# {"error": "Tenant context required. Use /api/v1/tenant/{tenant_id}/... routes"}
```

---

## Versioning and Compatibility

### Version Scheme

xolu follows semantic versioning: `MAJOR.MINOR.PATCH`. During the `0.x`
series, minor versions may include breaking changes to the database format
or API. The current version is `0.26.0`.

### Database Format Stability

**Within `0.9.x`:** The SQLite schema is stable. Patch releases (`0.9.1`,
`0.9.2`, etc.) will not require migrations. If a schema change is needed,
it will be shipped as part of `0.10.0` or later with an explicit migration.

**Across minor versions (`0.9` → `0.10`):** Schema changes are possible.
When they occur, a migration procedure will be documented in [UPGRADE.md](docs/UPGRADE.md).
subcommand. Release notes will state whether a migration is required.

### When Migrations Are Required

Migrations are required when:

- A new column is added to the `entities` or `tenants` table.
- A new system table is created (e.g., `entity_sequences` was added in
  the v1 → v2 schema migration).
- An index is changed or added.

Migrations are **not** required for:

- New API endpoints.
- Configuration changes.
- Bug fixes that don't alter stored data.

Migration steps are described in [UPGRADE.md](docs/UPGRADE.md). Running them
already-migrated database is safe and produces no changes.

### Rollback

Rollback is **not supported**. Migrations are forward-only. Before
upgrading, take a backup:

```bash
sqlite3 /app/data/xolu.db ".backup /backup/xolu-pre-upgrade.db"
```

If the upgrade fails, restore from the backup and stay on the previous
version.

### Safe Upgrade Steps

1. **Back up** the SQLite database and any Pebble timeseries directories.
2. **Stop** the running server.
3. **Replace** the `xolu` binary with the new version.
4. **Run migrations** if the release notes require it:
   See [UPGRADE.md](docs/UPGRADE.md) for the migration procedure.
5. **Start** the server.
6. **Verify** via `/health` and `/ready`.

For zero-downtime upgrades behind a load balancer, run step 4 before
starting the new instance. The migrated database is backwards-compatible
within the same minor version, so the old binary can still read it if
you need to roll back the binary without rolling back the database.

### API Compatibility

REST API endpoints are stable within a minor version. New endpoints may
be added in patch releases but existing endpoints will not change their
request/response format. Deprecations will be announced at least one
minor version before removal.

## Troubleshooting

For a complete list of error codes and their meanings, see [Error Code Reference](docs/ERROR_CODES.md).


For a quick-reference operational guide covering health checks, common
failure modes, and emergency procedures, see [docs/RUNBOOK.md](docs/RUNBOOK.md).

### Common Issues

**"Database is locked"**
- SQLite concurrent write issue
- Solution: Ensure single writer or use WAL mode

**"Entity not found" after creation**
- Cache may be stale
- Solution: Check cache TTL or disable caching for debugging

**Graph queries return empty**
- Graph may not be initialized
- Check: `XOLU_GRAPH_MODE=flat`

**Rate limiting too aggressive**
- Adjust `XOLU_RATE_LIMIT_RATE` and `XOLU_RATE_LIMIT_WINDOW`
- Consider `XOLU_RATE_LIMIT_BY_KEY=true` for authenticated clients

### Debug Mode

```bash
export XOLU_DEBUG=true
```

Enables verbose logging including:
- Request/response details
- Query execution
- Graph operations

---

## License

Apache 2.0 - See [LICENSE](LICENSE)
