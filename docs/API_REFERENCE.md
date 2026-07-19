# xolu REST API Reference

Complete reference for every HTTP endpoint exposed by the xolu server.

All data endpoints live under `/api/v1`. Non-data endpoints (`/health`,
`/ready`, `/version`, `/metrics`) are at the root.

**Tenant scoping.** Every data endpoint has a tenant-scoped variant under
`/api/v1/tenant/{tenant_id}/`. In `strict` mode the tenant-scoped routes are
the only available routes for data operations; the top-level routes below are
only available in `path` mode. The tenant-scoped variant behaves identically
except that all reads and writes are isolated to that tenant.

**Error envelope.** All errors return:
```json
{"error": {"code": "XOLU-XXNNN", "message": "...", "status": 4xx}}
```
See [ERROR_CODES.md](ERROR_CODES.md) for the full code catalogue.

---

## Contents

1. [System](#system)
2. [Entities (CRUD)](#entities-crud)
3. [Full-text Search](#full-text-search)
4. [Commit](#commit)
5. [OQL Queries](#oql-queries)
6. [Graph Traversal](#graph-traversal)
7. [Graph Admin](#graph-admin)
8. [Sulpher (Cypher Queries)](#sulpher-cypher-queries)
9. [Timeseries](#timeseries)
10. [Blob Storage](#blob-storage)
11. [Schema](#schema)
12. [Dynamic Configuration](#dynamic-configuration)
13. [Export](#export)
14. [S3-compatible Blob API](#s3-compatible-blob-api)

---

## System

These endpoints have no `/api/v1` prefix and require no authentication.

### `GET /health`

Liveness probe. Pings the storage backend.

**Response 200 OK:**
```json
{"status": "ok"}
```

**Response 503 Service Unavailable** (storage unreachable):
```json
{"status": "degraded", "error": "..."}
```

---

### `GET /ready`

Readiness probe. Same check as `/health`; intended for Kubernetes readiness gates.

---

### `GET /version`

Returns the server version string.

**Response 200 OK:**
```json
{"version": "0.9.9"}
```

---

### `GET /metrics`

Prometheus-format metrics. Available only when `XOLU_METRICS_ENABLED=true` (default).

Returns `text/plain; version=0.0.4` in the Prometheus exposition format.

---

## Entities (CRUD)

All routes accept and return `application/json`. The `{entity}` path segment
is the entity type name (e.g. `users`, `orders`).

### `POST /api/v1/{entity}`

Create a new entity. The request body is the entity document. `id` is
assigned automatically; do not include it.

**Response 201 Created:**
```json
{"message": "Resource of entity users created successfully", "id": 42}
```

**Notes:**
- Validated against the registered JSON Schema if one exists (`XOLU-VL001` on failure).
- REF fields create graph edges immediately.
- `_version` is initialised to `1` and included in all subsequent reads.

---

### `GET /api/v1/{entity}/{id}`

Retrieve one entity by ID.

**Query parameters:**

| Parameter | Default | Description |
|---|---|---|
| `embed` | enabled | Set `embed=false` or `embed=0` to skip reference embedding |
| `embed_depth` | `XOLU_REF_EMBED_DEPTH` | Override embedding depth (capped at `XOLU_MAX_EMBED_DEPTH`) |

**Response 200 OK** — the entity document, including `id` and `_version`:
```json
{"id": 42, "name": "Alice", "_version": 3, "manager": {"id": 7, "name": "Bob"}}
```

With `embed=false`:
```json
{"id": 42, "name": "Alice", "_version": 3, "manager": {"type": "REF", "entity": "users", "id": 7}}
```

---

### `GET /api/v1/{entity}`

List entities of a type. Paginated.

**Query parameters:**

| Parameter | Default | Description |
|---|---|---|
| `page` | `1` | Page number (1-indexed) |
| `per_page` | `10` (configurable) | Results per page (max 100) |
| `embed` | enabled | Set `embed=false` to skip embedding |
| `embed_depth` | `0` for lists | Embedding depth; disabled by default for performance |
| `{field}={value}` | — | Filter by field value; multiple filters are ANDed |

**Response 200 OK:**
```json
{
  "data": [...],
  "pagination": {
    "page": 1,
    "per_page": 10,
    "total_items": 142,
    "total_pages": 15
  }
}
```

---

### `PUT /api/v1/{entity}/{id}`

Full replacement of an entity. The request body completely replaces the
stored document. Fields absent from the body are deleted.

Supply `_version` in the body for optimistic concurrency control:

```json
{"name": "Alice", "role": "admin", "_version": 3}
```

**Response 200 OK:**
```json
{"message": "Resource of entity users with id 42 updated successfully"}
```

**Response 409 Conflict** (version mismatch):
```json
{
  "error": {"code": "XOLU-ST005", "message": "Version conflict", "status": 409},
  "current_version": 4
}
```

---

### `PATCH /api/v1/{entity}/{id}`

Partial update. Only fields present in the body are changed.

Null handling is controlled by `XOLU_PATCH_NULL`:
- `store` (default) — null fields are stored as null
- `delete` — null fields are removed from the document

Optimistic concurrency: include `_version` in the body to enable.

**Response 200 OK:**
```json
{"message": "Resource of entity users with id 42 updated successfully"}
```

---

### `DELETE /api/v1/{entity}/{id}`

Delete an entity. Removes the entity and its graph edges.

When `XOLU_CASCADING_DELETE=true`, entities with REF fields pointing to this
entity are also deleted recursively.

**Response 200 OK:**
```json
{"message": "Resource of entity users with id 42 deleted successfully"}
```

---

### `POST /api/v1/{entity}/save/{id}`

Upsert — create if the ID does not exist, replace if it does. The `id` in
the path is used as the entity ID; do not include `id` in the body.

Supports optimistic concurrency (include `_version` in body when updating).

**Response 200 OK** (updated) or **201 Created** (new).

---

## Full-text Search

Requires `XOLU_FULLTEXT_ENABLED=true` and SQLite backend.

### `GET /api/v1/search`

Full-text search across entity data using SQLite FTS5.

**Query parameters:**

| Parameter | Required | Description |
|---|---|---|
| `q` | yes | Search query string |
| `entity` | no | Restrict to one entity type |

**Response 200 OK:**
```json
{
  "query":   "alice",
  "entity":  "users",
  "count":   2,
  "results": [...]
}
```

**Response 503** (`XOLU-ST008`) when full-text search is not enabled.

---

## Commit

Atomic multi-entity write. See [COMMIT_ENDPOINT.md](COMMIT_ENDPOINT.md) for the
full specification.

### `POST /api/v1/commit`

Execute an update + optional entity appends + optional timeseries events
atomically. SQLite backend only.

**Request:**
```json
{
  "update": {
    "entity": "orders",
    "id": 7,
    "_version": 3,
    "status": "shipped"
  },
  "append": [
    {"entity": "order_events", "data": {"order_id": 7, "type": "shipped"}}
  ],
  "timeseries": [
    {"timeline": 1, "dims": [7], "time": "2026-06-14T12:00:00Z", "nums": [1.0]}
  ]
}
```

**Response 200 OK** — summary of what was written.

---

## OQL Queries

SQL-like queries against entity data. See [OQL_API.md](OQL_API.md) for the full
language reference.

### `POST /api/v1/oql/query`

Execute a query synchronously.

**Request:**
```json
{"query": "SELECT * FROM users WHERE active = true ORDER BY name LIMIT 20"}
```

**Response 200 OK:**
```json
{
  "data": [...],
  "stats": {
    "rows_scanned":      142,
    "rows_returned":     20,
    "rows_affected":     0,
    "execution_time_ms": 4
  }
}
```

---

### `POST /api/v1/oql/query/async`

Submit a query for background execution.

**Response 202 Accepted:**
```json
{"query_id": "abc123", "status": "pending"}
```

---

### `GET /api/v1/oql/query/{query_id}`

Poll job status.

**Response 200 OK:**
```json
{
  "query_id":   "abc123",
  "query":      "SELECT ...",
  "status":     "completed",
  "created_at": "2026-06-14T12:00:00Z",
  "updated_at": "2026-06-14T12:00:00Z"
}
```

`status` values: `pending`, `running`, `completed`, `failed`.

---

### `GET /api/v1/oql/query/{query_id}/result`

Retrieve a completed job's result.

**Response 202 Accepted** while running. **Response 200 OK** when complete:
```json
{
  "query_id": "abc123",
  "status":   "completed",
  "data":     [...],
  "stats":    {"rows_scanned": 142, "rows_returned": 20, "execution_time_ms": 4}
}
```

---

## Graph Traversal

Requires `XOLU_GRAPH_MODE=flat`. All graph endpoints return `XOLU-GR002` (501)
when the graph is disabled.

### `GET /api/v1/graph/stats`

Summary statistics for the in-memory graph.

**Response 200 OK:**
```json
{"node_count": 1842, "edge_count": 5613, "has_cycle": false}
```

---

### `GET /api/v1/graph/nodes/{node_id}`

Detailed information about a node.

`{node_id}` format: `entity:id` (e.g. `users:42`).

**Response 200 OK:** node info map with `id`, `entity`, `outgoing`, `incoming` edge counts.

---

### `GET /api/v1/graph/nodes/{node_id}/degree`

Edge degree counts for a node.

**Response 200 OK:**
```json
{"node_id": "users:42", "degree": {"in": 3, "out": 5}}
```

---

### `GET /api/v1/graph/{node_id}/out`

All outgoing edges from a node.

**Response 200 OK:**
```json
{
  "node_id": "users:42",
  "edges":   [{"source": "users:42", "target": "teams:7", "relationship": "member_of"}],
  "count":   1
}
```

---

### `GET /api/v1/graph/{node_id}/in`

All incoming edges to a node.

**Response 200 OK:**
```json
{
  "node_id": "teams:7",
  "edges":   [{"source": "users:42", "target": "teams:7", "relationship": "member_of"}],
  "count":   1
}
```

---

### `POST /api/v1/graph/path`

Find any path between two nodes using BFS.

**Request:**
```json
{"from": "users:1", "to": "users:99", "max_depth": 5}
```

`max_depth` defaults to `XOLU_GRAPH_MAX_VISITED_NODES` when omitted.

**Response 200 OK:**
```json
{"from": "users:1", "to": "users:99", "path": ["users:1", "teams:4", "users:99"], "length": 2}
```

**Response 404** when no path exists within `max_depth`.

---

### `POST /api/v1/graph/shortestPath`

Find the shortest path between two nodes. Returns the result even when no
path exists (unlike `/graph/path`).

**Request:** `{"from": "...", "to": "...", "max_depth": 5}`

**Response 200 OK:**
```json
{
  "from":   "users:1",
  "to":     "users:99",
  "exists": true,
  "path":   ["users:1", "teams:4", "users:99"],
  "length": 2
}
```

When no path: `{"exists": false, "path": null, "length": 0}`.

---

### `POST /api/v1/graph/pathExists`

Check whether a path exists without returning it.

**Request:** `{"from": "...", "to": "...", "max_depth": 5}`

**Response 200 OK:**
```json
{"from": "users:1", "to": "users:99", "exists": true, "length": 2}
```

---

### `POST /api/v1/graph/neighbors`

Get neighbours of a node by direction.

**Request:**
```json
{"node_id": "users:42", "direction": "out"}
```

`direction`: `"out"` (default), `"in"`, or `"both"`.

**Response 200 OK:**
```json
{
  "neighbors": {
    "outgoing": {"teams:7": {"rel": "member_of"}},
    "incoming": {}
  }
}
```

---

### `POST /api/v1/graph/commonNeighbors`

Shared outgoing neighbours of two nodes.

**Request:** `{"node_a": "users:1", "node_b": "users:2"}`

**Response 200 OK:**
```json
{"node_a": "users:1", "node_b": "users:2", "common": ["teams:4", "projects:9"], "count": 2}
```

---

### `POST /api/v1/graph/nodes/search`

List nodes in the graph, optionally filtered by entity type.

**Request:**
```json
{"entity": "users", "limit": 100}
```

Omit `entity` to list all node IDs. `limit` defaults to no limit.

**Response 200 OK:**
```json
{"nodes": ["users:1", "users:2", "users:42"], "count": 3}
```

---

### `POST /api/v1/graph/edges`

Write a graph edge with properties. Creates the edge in both the SQLite edge
table and the in-memory graph. Use this when you need edges that don't
correspond to REF fields in entity documents.

**Request:**
```json
{
  "from":  "users:1",
  "to":    "projects:7",
  "rel":   "contributes_to",
  "props": {"since": "2026-01-01", "role": "maintainer"}
}
```

**Response 201 Created:**
```json
{"edge_id": 83, "rel": "contributes_to", "from": "users:1", "to": "projects:7"}
```

---

## Graph Admin

Admin endpoints for verifying and repairing the edge table. Available in
`path` mode only; `strict` mode blocks unscoped routes.

### `GET /api/v1/graph/admin/verify`

Verify that the SQLite edge table is consistent with entity REF fields.
Non-destructive.

**Response 200 OK:**
```json
{"status": "ok", "message": "tenant edge table is consistent with entity REF fields", "vode_count": 0}
```

A non-zero `vode_count` means there are forward-reference placeholder nodes
(vodes) — REF targets that don't yet exist as entity records.

**Response 409 Conflict** when inconsistencies are found.

---

### `POST /api/v1/graph/admin/rebuild`

Drop and rebuild the SQLite edge table from entity JSON, then reload the
in-memory graph. Use after a manual data migration or when `verify` reports
inconsistencies.

**Response 200 OK:**
```json
{"status": "ok", "message": "tenant edge table rebuilt from entity data; in-memory graph reloaded", "vode_count": 0}
```

---

## Sulpher (Cypher Queries)

graph queries using the Sulpher openCypher 9 subset. Requires
`XOLU_GRAPH_MODE=flat`. See [SULPHER_QUERY_REFERENCE.md](SULPHER_QUERY_REFERENCE.md) for the
full language reference and known gaps.

When caching is enabled (`XOLU_GRAPH_QUERY_CACHE_TTL > 0`, default 30 s),
responses include `X-Cache: HIT` or `X-Cache: MISS`.

### `POST /api/v1/graph/query`

Execute a Sulpher query synchronously.

**Request:**
```json
{
  "query":     "MATCH (u:user)-[:knows]->(f:user) RETURN f.name",
  "max_depth": 5
}
```

`max_depth` defaults to `XOLU_GRAPH_MAX_VISITED_NODES` when omitted.

**Response 200 OK:**
```json
{
  "result": [...],
  "stats": {
    "nodes_traversed":   48,
    "paths_found":       6,
    "execution_time_ms": 3
  }
}
```

---

### `POST /api/v1/graph/query/async`

Submit a Sulpher query for background execution.

**Request:** `{"query": "...", "max_depth": 5}`

**Response 202 Accepted:**
```json
{"query_id": "x9y8z7", "status": "pending", "created_at": "2026-06-14T12:00:00Z"}
```

---

### `GET /api/v1/graph/query/{query_id}`

Poll async Sulpher job status.

**Response 200 OK:**
```json
{
  "query_id":   "x9y8z7",
  "query":      "MATCH ...",
  "status":     "running",
  "created_at": "...",
  "started_at": "..."
}
```

`ended_at` and `error` appear once the job finishes.

---

### `GET /api/v1/graph/query/{query_id}/result`

Retrieve a completed Sulpher job result.

**Response 202 Accepted** while running.

**Response 200 OK** when complete:
```json
{
  "query_id": "x9y8z7",
  "status":   "completed",
  "result":   [...],
  "stats":    {"nodes_traversed": 48, "paths_found": 6, "execution_time_ms": 3}
}
```

**Response 200 OK** on failure:
```json
{"query_id": "x9y8z7", "status": "failed", "error": "visited node limit exceeded"}
```

---

## Timeseries

Pebble-backed append-optimised event storage. Available only in
`XOLU_TENANT_MODE=strict` with `XOLU_TIMESERIES_ENABLED=true`. All routes are
tenant-scoped: `/api/v1/tenant/{tenant_id}/ts/...`.

See [TIMESERIES_DESIGN_V3.md](TIMESERIES_DESIGN_V3.md) for the storage design.

### `POST /api/v1/tenant/{tenant_id}/ts/provision`

Enable timeseries storage for a tenant. Idempotent.

**Response 201 Created** (new) or **200 OK** (already provisioned):
```json
{"tenant_id": "acme", "timeseries": "enabled"}
```

---

### `POST /api/v1/tenant/{tenant_id}/ts/tl/def`

Define a new timeline. `dims` is immutable after the first write.

**Request:**
```json
{"id": 1, "name": "temperature", "dims": 2, "retention_days": 90}
```

`retention_days`: 0 = inherit store default; negative = no expiry.

**Response 201 Created:**
```json
{"id": 1, "name": "temperature", "dims": 2, "retention_days": 90, "created_at": "..."}
```

---

### `GET /api/v1/tenant/{tenant_id}/ts/tl/list`

List all defined timelines.

---

### `GET /api/v1/tenant/{tenant_id}/ts/tl/{timeline_id}`

Get one timeline definition.

---

### `PATCH /api/v1/tenant/{tenant_id}/ts/tl/{timeline_id}`

Update timeline `name` or `retention_days`. Changing `dims` after the first
write returns 409 (`XOLU-TS016`).

---

### `DELETE /api/v1/tenant/{tenant_id}/ts/tl/{timeline_id}`

Delete a timeline **definition**, together with its event data and its rollups.
This is the inverse of `def`, and is distinct from `DELETE .../tl/{id}/data`
(which clears events but keeps the definition).

Cascade follows the store's `RollupCascadeDelete` policy:
- cascade on (default): the timeline's rollups are removed first, then its data,
  then its definition.
- cascade off: if the timeline still has rollups, the call returns **409**
  (`XOLU-TS026`, `ErrTSRollupDestInUse`) and changes nothing — delete the rollups
  first.

Timeline `0` (the structural root) cannot be deleted: **400** (`ErrTSRootTimeline`).
An undefined timeline returns **404** (`XOLU-TS004`).

**Response 204 No Content** on success.

Concurrency: the timeline is marked *deleting* before any teardown, so a
concurrent read or write observes a clean not-found (the timeline is hidden from
lookups) rather than a transient "defined but empty" state. The marker is
in-memory only; a process crash mid-delete leaves a normal, retryable timeline,
and the index is rebuilt from the source of truth. A failed delete (e.g. the
cascade-off 409) clears the marker and leaves the timeline fully usable.

---

### `POST /api/v1/tenant/{tenant_id}/ts/events`

Append one event.

**Request:**
```json
{
  "timeline": 1,
  "dims":     [42, 7],
  "time":     "2026-06-14T10:30:00Z",
  "nums":     [23.4, 1013.25],
  "payload":  "base64encodedopaquebytes"
}
```

`dims` must match the declared dimension count. `nums` holds up to 7 float64
values (no NaN). `payload` is optional opaque bytes (up to 64 KB, base64).

**Response 201 Created.**

---

### `POST /api/v1/tenant/{tenant_id}/ts/events/batch`

Append up to `XOLU_TS_MAX_BATCH_SIZE` events atomically.

**Request:** `{"events": [...event objects...]}`

**Response 200 OK:**
```json
{"total": 100, "accepted": 100, "failed": 0}
```

---

### `GET /api/v1/tenant/{tenant_id}/ts/events`

Range query. Returns events in the `[from, to]` window matching the dimension prefix.

**Query parameters:**

| Parameter | Required | Description |
|---|---|---|
| `timeline` | yes | Timeline ID (uint16) |
| `dims` | yes | Comma-separated leading dimension values |
| `from` | yes | Start timestamp (RFC 3339, inclusive) |
| `to` | yes | End timestamp (RFC 3339, inclusive) |
| `limit` | no | Max results (default 1000, capped by `XOLU_TS_MAX_QUERY_EVENTS`) |
| `order` | no | `asc` (default) or `desc` |

Supplying fewer `dims` values than the timeline's declared count returns all
events matching the leading prefix.

**Response 200 OK:**
```json
{"count": 42, "events": [{"timeline": 1, "dims": [42, 7], "time": "...", "nums": [...], "payload": "..."}]}
```

---

### `POST /api/v1/tenant/{tenant_id}/ts/query/range`

POST equivalent of `GET /ts/events`. Accepts the same parameters as a JSON
body instead of query string — useful for clients that cannot send bodies
with GET, or for queries with many dimension values.

---

### `GET /api/v1/tenant/{tenant_id}/ts/events/latest`

Latest N events for a dimension prefix, in reverse-chronological order.

**Query parameters:** `timeline`, `dims` (required); `n` (default 10, capped
by `XOLU_TS_MAX_QUERY_EVENTS`).

---

### `POST /api/v1/tenant/{tenant_id}/ts/aggregate`

Bucketed or scalar aggregate over a single numeric field.

**Request:**
```json
{
  "timeline":  1,
  "dims":      [42],
  "from":      "2026-01-01T00:00:00Z",
  "to":        "2026-01-08T00:00:00Z",
  "num_field": 0,
  "function":  "avg",
  "interval":  "1h"
}
```

`function`: `avg`, `min`, `max`, `sum`, `count`.
`num_field`: 0–6 (index into `nums`).
`interval`: `1m 5m 15m 30m 1h 6h 12h 1d 7d`; omit for a scalar result.

**Response 200 OK (bucketed):** `{"buckets": [{"time": "...", "value": 21.3, "count": 60}]}`

**Response 200 OK (scalar):** `{"value": 21.7, "count": 1440, "from": "...", "to": "..."}`

---

### `POST /api/v1/tenant/{tenant_id}/ts/range_aggregate`

Count, sum, avg, min, max for **all seven numeric fields** in a single scan.

**Request:** `{"timeline", "dims", "from", "to"}`

**Response 200 OK:**
```json
{
  "timeline": 1,
  "count":    12500,
  "fields":   [true, true, false, false, false, false, false],
  "sums":     [1230.5, 98432.1, 0, 0, 0, 0, 0],
  "avgs":     [0.098, 7.874, 0, 0, 0, 0, 0],
  "mins":     [0.001, 1.2, 0, 0, 0, 0, 0],
  "maxs":     [0.999, 99.9, 0, 0, 0, 0, 0]
}
```

`fields[i]` is `true` if field `i` appeared in at least one event. Entries for absent fields are zero.

---

### `POST /api/v1/tenant/{tenant_id}/ts/full_aggregate`

Single-pass combination of exact statistics (same as `range_aggregate`) plus
approximate quantile estimates via t-digest.

**Request:**
```json
{
  "timeline":        1,
  "dims":            [42],
  "from":            "...",
  "to":              "...",
  "quantiles":       [0.5, 0.9, 0.99],
  "quantile_fields": [0, 1]
}
```

`quantiles`: values in [0, 1]. Absent or empty = no quantile estimation (same cost as `range_aggregate`).
`quantile_fields`: which `nums` fields (0–6) to compute quantiles for. Null = all fields.

**Response 200 OK:** Same as `range_aggregate` plus:
```json
{"quantiles": [[50.1, 90.3, 99.1], [500.2, 903.1, 991.4], null, null, null, null, null]}
```

`quantiles[i]` is null when field `i` was not requested or carried no events.

---

### `GET /api/v1/tenant/{tenant_id}/ts/retention`

View retention configuration: store-level default and per-timeline overrides.

**Response 200 OK:**
```json
{
  "default_retention_days": 90,
  "timelines": [{"id": 1, "name": "temperature", "retention_days": 90}]
}
```

---

### `PATCH /api/v1/tenant/{tenant_id}/ts/retention`

Update the store-level default retention. Per-timeline retention is updated
via `PATCH /ts/tl/{id}`.

**Request:** `{"default_retention_days": 30}`

**Response 200 OK:** `{"default_retention_days": 30, "status": "updated"}`

---

### `GET /api/v1/tenant/{tenant_id}/ts/stats`

Store-level diagnostics.

**Response 200 OK:**
```json
{"tenant_id": "acme", "timelines": 10, "disk_bytes": 1340000000}
```

---

### `GET /api/v1/tenant/{tenant_id}/ts/stats/{timeline_id}`

Per-timeline diagnostics. `total_events` is approximate (eventually consistent after crash).

**Response 200 OK:**
```json
{
  "timeline_id": 1,
  "name":        "temperature",
  "total_events": 21600000,
  "oldest_event": "2026-01-01T00:00:00Z",
  "newest_event": "2026-06-14T12:00:00Z"
}
```

---

## Blob Storage

Binary object store. Requires `XOLU_BLOB_ENABLED=true`. See [BLOB_API.md](BLOB_API.md)
for full details including quota, GC, and S3 compatibility.

All routes have tenant-scoped variants under `/api/v1/tenant/{tenant_id}/blob/...`.

### `POST /api/v1/blob`

Store a blob. Body is the raw content.

Key selection order: `X-Blob-Key` header → `?key=` query param → SHA-256 of
content (content-addressed mode, no key alias written).

**Response 201 Created** (new) or **200 OK** (key already pointed to same content):
```json
{"key": "my-file.pdf", "sha256": "e3b0c44...", "size": 4096, "created": true}
```

---

### `GET /api/v1/blob/{key}`

Retrieve blob content. Streams raw bytes with original `Content-Type`.

**Response headers:** `X-Blob-SHA256`, `X-Blob-MD5`, `X-Blob-Size`, `ETag`.

---

### `HEAD /api/v1/blob/{key}`

Metadata without body. Same response headers as GET.

---

### `DELETE /api/v1/blob/{key}`

Remove key alias. GC handles unreferenced content.

**Response 200 OK:** `{"key": "my-file.pdf", "deleted": true}`

---

### `GET /api/v1/blob`

List blobs. Optional `?prefix=` filter. Sorted by key ascending.

**Response 200 OK:**
```json
{
  "tenant": "default",
  "prefix": "",
  "count":  3,
  "blobs":  [{"key": "...", "sha256": "...", "size": 4096, "content_type": "...", "stored_at": "..."}]
}
```

---

### `GET /api/v1/blob/usage`

Cached disk usage for the tenant.

**Response 200 OK:**
```json
{"tenant": "default", "blob_count": 142, "key_count": 156, "bytes": 10485760, "sampled_at": "..."}
```

`sampled_at` absent if the sampler has not yet completed its first sweep.

---

## Schema

Register JSON Schemas to enable validation and adapted table storage. See
[JSON_SCHEMA.md](JSON_SCHEMA.md) for the supported keywords and field types.

### `POST /api/v1/schema/{entity}`

Register or update a schema. Two effects:
1. All subsequent writes to `{entity}` are validated.
2. An adapted (native-column) SQLite table is created or migrated.

**Request body:** A JSON Schema object.

**Response 201 Created:**
```json
{"message": "Schema for users created/updated successfully"}
```

---

### `GET /api/v1/schema/{entity}`

Retrieve the registered schema for an entity type.

**Response 200 OK:** The raw JSON Schema object.
**Response 404** (`XOLU-ST008`) when no schema exists.

### `GET /api/v1/schemas`

Enumerate the entity types that currently have a registered schema.
Names only (the server tracks no registration timestamps), sorted.

**Response 200 OK:**

```json
{ "schemas": [ { "name": "asset" }, { "name": "widget" } ], "count": 2 }
```

---

## Dynamic Configuration

Runtime key-value configuration. Requires `XOLU_DYNCONFIG_ENABLED=true` and
`XOLU_DYNCONFIG_API_ENABLED=true`. See [DYNCONFIG.md](DYNCONFIG.md) for details.

### `GET /api/v1/admin/config`

Dump all namespaces and keys.

---

### `GET /api/v1/admin/config/{namespace}`

All keys in one namespace. **Response 404** if namespace has no keys.

---

### `GET /api/v1/admin/config/{namespace}/{key}`

Single value as raw JSON. **Response 404** if not set.

---

### `PUT /api/v1/admin/config/{namespace}/{key}`

Set a value. Body must be a valid JSON value (number, string, boolean, object,
array, or null).

**Response 200 OK:** `{"namespace": "global", "key": "blob.max_bytes", "status": "set"}`

---

### `DELETE /api/v1/admin/config/{namespace}/{key}`

Remove a key. No-op if the key does not exist.

**Response 200 OK:** `{"namespace": "global", "key": "blob.max_bytes", "status": "deleted"}`

---

## Export

### `GET /api/v1/export`

Download a zip archive of the current database. Checkpoints the SQLite WAL
before copying. Safe to call during normal operation.

Does not include blob storage or timeseries data — back those up separately
from the filesystem.

**Response:** `application/zip` attachment.

Archive contents:
```
manifest.json     Export metadata (version, timestamp, storage_type)
entities.db       SQLite database file
graph.json        Graph export (if graph enabled and JSON persistence active)
```

See [EXPORT_API.md](EXPORT_API.md) for notes on querying `entities.db` directly
and the internal table naming scheme.

---

## S3-compatible Blob API

Separate listener on `XOLU_S3_PORT` (default 9091) when `XOLU_S3_ENABLED=true`.
Bucket name = tenant name. Implements the minimal S3 surface for existing
S3 client libraries.

Authentication: access key ID from the AWS Signature V4 `Authorization` header
is treated as the tenant name. Signature is not verified; use any non-empty
secret key. When `XOLU_S3_REQUIRE_AUTH=true`, requests without `Authorization`
are rejected with 403.

| Operation | Method + Path | Notes |
|---|---|---|
| PutObject | `PUT /{bucket}/{key}` | 200 with `ETag` header only (no body) |
| GetObject | `GET /{bucket}/{key}` | Streams content |
| HeadObject | `HEAD /{bucket}/{key}` | Metadata headers only |
| DeleteObject | `DELETE /{bucket}/{key}` | Removes key alias |
| ListObjectsV2 | `GET /{bucket}?list-type=2` | XML response; supports `prefix` and `max-keys` |
| HeadBucket | `HEAD /{bucket}` | 200 if tenant exists, 404 otherwise |

Errors are returned as S3-format XML rather than xolu's JSON error envelope.
