# Async Query Reference

Both Sulpher (graph) and OQL queries can run asynchronously. Use async when
a query might take longer than a few seconds — the client submits the query,
polls for completion, then retrieves the result separately.

---

## Sulpher (graph queries)

### Submit

```http
POST /api/v1/graph/query/async
Content-Type: application/json

{"query": "MATCH (u:user)-[:knows]->(f:user) RETURN f.name", "max_depth": 5}
```

**Response 202 Accepted:**

```json
{
  "query_id":   "a1b2c3d4",
  "status":     "pending",
  "created_at": "2026-06-14T10:00:00Z"
}
```

### Poll status

```http
GET /api/v1/graph/query/{query_id}
```

**Response 200 OK:**

```json
{
  "query_id":   "a1b2c3d4",
  "query":      "MATCH (u:user)-[:knows]->(f:user) RETURN f.name",
  "status":     "running",
  "created_at": "2026-06-14T10:00:00Z",
  "started_at": "2026-06-14T10:00:00.012Z"
}
```

`ended_at` is present once the query reaches `completed` or `failed`.
`error` is present when `status` is `failed`.

### Retrieve result

```http
GET /api/v1/graph/query/{query_id}/result
```

**While still running — 202 Accepted:**

```json
{
  "query_id": "a1b2c3d4",
  "status":   "running",
  "message":  "Query is still processing"
}
```

**On failure — 200 OK:**

```json
{
  "query_id": "a1b2c3d4",
  "status":   "failed",
  "error":    "visited node limit exceeded"
}
```

**On success — 200 OK:**

```json
{
  "query_id": "a1b2c3d4",
  "status":   "completed",
  "result":   [...],
  "stats": {
    "nodes_traversed":   847,
    "paths_found":       12,
    "execution_time_ms": 234
  }
}
```

`result` is the same structure as a sync query response.

### Tenant-scoped variants

All three endpoints have `/api/v1/tenant/{tenant_id}/graph/...` equivalents.
A job is only visible within the tenant that submitted it.

---

## OQL queries

### Submit

```http
POST /api/v1/oql/query/async
Content-Type: application/json

{"query": "SELECT * FROM users WHERE active = true ORDER BY name LIMIT 100"}
```

**Response 202 Accepted:**

```json
{"query_id": "x9y8z7", "status": "pending"}
```

### Poll status

```http
GET /api/v1/oql/query/{query_id}
```

Returns the same status fields as the Sulpher status endpoint above.

### Retrieve result

```http
GET /api/v1/oql/query/{query_id}/result
```

Returns the same shape as a sync OQL response once completed.

---

## Job lifecycle

```
pending → running → completed
                 ↘ failed
```

| Status | Meaning |
|---|---|
| `pending` | Job received; queued for execution |
| `running` | Executor is actively processing the query |
| `completed` | Result is available |
| `failed` | Execution failed; `error` field contains the reason |

Jobs are kept in memory for `XOLU_ASYNC_JOB_RETENTION_TTL` seconds after
reaching `completed` or `failed` (default 86400 — 24 hours). After that they
are evicted; polling an expired job returns `XOLU-QL012` (404).

Job IDs are not persisted across server restarts. A restart loses all
in-flight and completed jobs.

---

## Choosing sync vs async

Use **sync** when:
- The query is expected to complete in under a few seconds
- The client can hold the HTTP connection open
- Simplicity matters more than resilience

Use **async** when:
- The query touches a large graph or table and may take tens of seconds
- The client cannot hold a long-lived HTTP connection (mobile, serverless)
- The result needs to be retrieved by a different process or after a delay
