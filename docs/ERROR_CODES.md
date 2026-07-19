# Error Code Reference

Every error response from xolu includes a stable machine-readable code in the
`error.code` field. Client code should switch on the code, not the message.

## Response envelope

All error responses share a common JSON envelope:

```json
{
  "error": {
    "code":    "XOLU-ST001",
    "message": "Entity not found",
    "status":  404
  }
}
```

| Field | Type | Notes |
|---|---|---|
| `error.code` | string | Stable identifier; does not change across versions within the same major version |
| `error.message` | string | Human-readable description; not stable; do not parse |
| `error.status` | integer | Mirrors the HTTP status code |

The rate-limit response (`XOLU-RL001`) uses a different envelope:

```json
{
  "error":       "Too Many Requests",
  "message":     "Rate limit exceeded",
  "retry_after": 45
}
```

---

## Code categories

| Prefix | Subsystem |
|---|---|
| `XOLU-ST` | Storage (entity CRUD) |
| `XOLU-VL` | Validation |
| `XOLU-GR` | Graph |
| `XOLU-QL` | Query (OQL) |
| `XOLU-AU` | Authentication |
| `XOLU-RL` | Rate limiting |
| `XOLU-TN` | Tenancy |
| `XOLU-CM` | Commit endpoint |
| `XOLU-TS` | Timeseries |
| `XOLU-BL` | Blob storage |
| `XOLU-DC` | Dynamic configuration |
| `XOLU-CF` | Server configuration |
| `XOLU-MF` | *Reserved:* molu front (satellite project) |
| `XOLU-MH` | *Reserved:* molu hub (satellite project) |

### Reserved prefixes for satellite projects

`XOLU-MF` (molu front) and `XOLU-MH` (molu hub) are reserved for the molu
satellite project (T-21). xolu itself will never allocate codes under
these areas; the code definitions live in the molu repository. Any future
satellite product reserves its area prefix here before allocating codes.

---

## ST — Storage

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-ST001` | 404 | Entity not found |
| `XOLU-ST002` | 409 | Entity already exists |
| `XOLU-ST003` | 400 | Invalid entity name |
| `XOLU-ST004` | 400 | Invalid or malformed ID |
| `XOLU-ST005` | 409 | Optimistic concurrency version conflict |
| `XOLU-ST006` | 500 | Storage backend failure |
| `XOLU-ST007` | 413 | Entity document exceeds `XOLU_MAX_ENTITY_SIZE` |
| `XOLU-ST008` | 404 | Schema not found for entity type |
| `XOLU-ST009` | 500 | Schema load failed |

---

## VL — Validation

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-VL001` | 422 | JSON Schema validation failed; `message` includes field-level details |
| `XOLU-VL002` | 400 | Request body is not valid JSON |
| `XOLU-VL003` | 400 | Required query parameter or path segment missing |

---

## GR — Graph

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-GR001` | 409 | Cycle detected (only when `XOLU_GRAPH_CYCLE_DETECTION=error`) |
| `XOLU-GR002` | 501 | Graph operations disabled (`XOLU_GRAPH_MODE=disabled`) |
| `XOLU-GR003` | 501 | Graph operation not supported on this storage backend |
| `XOLU-GR004` | 400 | Graph query failed (syntax or executor error; see message) |
| `XOLU-GR005` | 413 | BFS visited-node limit exceeded (`XOLU_GRAPH_MAX_VISITED_NODES`) |
| `XOLU-GR006` | 413 | Result limit exceeded (`XOLU_GRAPH_MAX_RESULTS`) |
| `XOLU-GR007` | 400 | Entity document contains two REF fields pointing to the same target |

---

## QL — Query (OQL)

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-QL001` | 400 | Query parse error |
| `XOLU-QL002` | 400 | Unsupported query feature |
| `XOLU-QL003` | 400 | Invalid query structure |
| `XOLU-QL004` | 500 | Query executor failure |
| `XOLU-QL005` | 501 | Query engine not initialised |
| `XOLU-QL006` | 400 | Invalid column reference |
| `XOLU-QL007` | 400 | Invalid aggregate expression |
| `XOLU-QL008` | 504 | Query timed out (`XOLU_QUERY_TIMEOUT`) |
| `XOLU-QL009` | 413 | Too many rows returned (`XOLU_QUERY_MAX_ROWS`) |
| `XOLU-QL010` | 413 | Too many rows scanned (`XOLU_QUERY_MAX_SCAN_ROWS`) |
| `XOLU-QL011` | 413 | Response too large (`XOLU_QUERY_MAX_RESPONSE_BYTES`) |
| `XOLU-QL012` | 404 | Async job not found |
| `XOLU-QL013` | 400 | Query string required |

---

## AU — Authentication

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-AU001` | 401 | No credentials or unrecognised credentials |
| `XOLU-AU002` | 401 | JWT is present but invalid (expired, bad signature, wrong issuer) |
| `XOLU-AU003` | 403 | Credentials are valid but the operation is not permitted |

---

## RL — Rate Limiting

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-RL001` | 429 | Request rate limit exceeded; `retry_after` field gives seconds to wait |

---

## TN — Tenancy

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-TN001` | 404 | Tenant not found |
| `XOLU-TN002` | 400 | Tenant context required but not supplied (strict mode) |

---

## CM — Commit endpoint

These codes are specific to `POST /api/v1/commit`.

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-CM001` | 409 | Version conflict in the `update` payload |
| `XOLU-CM002` | 400 | `update` object missing or malformed |
| `XOLU-CM003` | 400 | `append` array is present but empty |
| `XOLU-CM004` | 413 | `append` array exceeds the maximum allowed size |
| `XOLU-CM005` | 400 | Invalid entity type in `update` |
| `XOLU-CM006` | 400 | Invalid entity type in `append` |
| `XOLU-CM007` | 409 | An `append` entity ID already exists |
| `XOLU-CM008` | 500 | Transaction failed |
| `XOLU-CM009` | 501 | Commit endpoint not available on this configuration |
| `XOLU-CM010` | 501 | Timeseries write requested but timeseries not enabled |
| `XOLU-CM011` | 400 | Timeseries timeline not provisioned |
| `XOLU-CM012` | 400 | Invalid timeline ID in timeseries payload |
| `XOLU-CM013` | 400 | Invalid dimension values in timeseries payload |
| `XOLU-CM014` | 413 | Timeseries batch exceeds maximum size |
| `XOLU-CM015` | 500 | Timeseries write failed |
| `XOLU-CM016` | 500 | Timeseries write failed after SQLite commit succeeded (partial write; see `docs/COMMIT_ENDPOINT.md`) |

---

## TS — Timeseries

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-TS001` | 403 | Timeseries not available in this tenant mode |
| `XOLU-TS002` | 403 | Timeseries feature not enabled on this server |
| `XOLU-TS003` | 404 | Tenant not provisioned for timeseries |
| `XOLU-TS004` | 400 | Timeline not defined, or invalid `timeline_id` in path |
| `XOLU-TS005` | 400 | Invalid or unparseable timestamp |
| `XOLU-TS006` | 400 | Batch size exceeds `XOLU_TS_MAX_BATCH_SIZE` |
| `XOLU-TS007` | 400 | Dimension count mismatch for timeline |
| `XOLU-TS008` | 400 | Invalid aggregate function name |
| `XOLU-TS009` | 400 | `num_field` index out of range (0–6) |
| `XOLU-TS010` | 400 | Invalid interval value in windowed aggregate |
| `XOLU-TS011` | 400 | Query window exceeds `XOLU_TS_MAX_RANGE_DAYS` |
| `XOLU-TS012` | 400 | Result limit (`XOLU_TS_MAX_QUERY_EVENTS`) exceeded |
| `XOLU-TS013` | 400 | Scan aborted — `XOLU_TS_MAX_SCAN_EVENTS` exceeded |
| `XOLU-TS014` | 500 | Retention sweep failure |
| `XOLU-TS015` | 500 | Timeseries provisioning failure (disk error, permissions) |
| `XOLU-TS016` | 409 | Attempt to change `dims` after first write (immutable) |
| `XOLU-TS017` | 400 | NaN value in numeric field |
| `XOLU-TS018` | 400 | Reserved timeline ID 0x0000 used |
| `XOLU-TS019` | 400 | Windowed aggregate would exceed `XOLU_TS_MAX_AGGREGATE_BUCKETS` |
| `XOLU-TS020` | 400 | Invalid write-config request body (missing or unrecognised field) |
| `XOLU-TS021` | 500 | Write config could not be persisted to disk |
| `XOLU-TS022` | 400 | Timeline 0 used in rollup or data deletion operation |
| `XOLU-TS023` | 400 | Rollup definition would create a cycle in the rollup tree |
| `XOLU-TS024` | 400 | Rollup definition would exceed `ts.rollup_max_depth` |
| `XOLU-TS025` | 404 | Rollup definition not found |
| `XOLU-TS026` | 400/409 | Destination timeline already targeted by another rollup (400 on define); definition has descendants and cascade delete is disabled (409 on delete) |

---

## BL — Blob storage

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-BL001` | 501 | Blob storage not enabled (`XOLU_BLOB_ENABLED` required) |
| `XOLU-BL002` | 404 | Blob key not found |
| `XOLU-BL003` | 413 | Content exceeds `XOLU_BLOB_MAX_SIZE` |
| `XOLU-BL004` | 400 | Invalid key (contains `/`, `\`, starts with `.`, or is `.`/`..`) |
| `XOLU-BL005` | 500 | Filesystem read or write failure |
| `XOLU-BL006` | 413 | Tenant blob quota exceeded |

---

## DC — Dynamic configuration

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-DC001` | 503 | Dynamic configuration not enabled |
| `XOLU-DC002` | 404 | Namespace or key not found |
| `XOLU-DC003` | 400 | Invalid namespace, key, or value |
| `XOLU-DC004` | 500 | Configuration file write failure |

---

## CF — Server configuration

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-CF001` | 500 | Internal configuration error (should not occur in production) |
