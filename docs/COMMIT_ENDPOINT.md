# olu `/commit` Endpoint Reference

**Version:** 0.3  
**Author:** haitch <h@ual.fi>  
**Date:** May 2026  
**Status:** Implemented (v0.9.7-patched78)  
**Supersedes:** v0.2 (patched59) — adds Pebble timeseries write path

---

## 1. Problem

olu's write API provides three independent paths:

- `POST /api/v1/tenant/{t}/objects/{id}` — create an entity
- `POST /api/v1/tenant/{t}/save/{id}` — upsert an entity
- `POST /api/v1/tenant/{t}/{entity}` — create a record of any entity type

These operations are independent HTTP calls. There is no mechanism to
execute two or more of them atomically. A caller that needs to update one
entity and append a record to another must make two separate requests,
and must accept that the following failure modes are possible:

- The first write commits; the second fails. The entity has advanced to a
  new state with no corresponding record in the audit trail.
- A concurrent writer observes the entity in the new state before the
  audit record exists.

These are not hypothetical edge cases. An FSM executor that writes a state
transition and a timeseries audit entry in two sequential requests will
produce a permanent gap in the audit trail if the timeseries write fails.

A financial application has the same structure: update an account balance
and append a transaction log entry. A balance update without a log entry
violates the audit trail. A log entry without a balance update produces an
incorrect balance.

This pattern — one conditional update plus one or more appended records —
recurs across domains. It is the dominant write pattern for any system that
maintains both current state and a history of how that state was reached.

---

## 2. The Pattern

**Current state** is represented by a single entity record overwritten on
each transition. It answers "what is the state of X right now?" in O(1)
without scanning history.

**History** is an append-only sequence of records that captures every
transition. It answers "how did X arrive at its current state?" and "what
was the state of X at time T?".

The invariant that must hold: **the most recent history entry always
corresponds to the current state entry**. When the two writes are separate
HTTP calls this invariant is best-effort. When they are a single atomic
operation the invariant is enforced by the storage engine.

As of v0.9.7-patched78, history can be written to two different stores:

- **SQLite entity appends** (`append` array) — write history records as
  entities into the SQLite store, within the same transaction as the state
  update. This was the only path prior to patched78 and remains fully
  supported.
- **Pebble timeseries events** (`timeseries` array) — write history to the
  Pebble-backed timeseries store with nanosecond-resolution keys and
  typed numeric fields, using a write-order atomicity protocol with
  tombstone-based rollback. See Section 6.

Both paths may be used in the same request. Most callers will use one or
the other.

---

## 3. Endpoint

    POST /api/v1/tenant/{tenant_id}/commit

---

## 4. Request Shape

### 4.1 Entity-append path (SQLite only)

```json
{
  "update": {
    "entity": "order",
    "id": 42,
    "version": 7,
    "data": { "state": "shipped", "updated_at": "2026-05-13T14:22:01Z" }
  },
  "append": [
    {
      "entity": "event_log",
      "data": { "order_id": 42, "transition": "submit->ship", "at": "2026-05-13T14:22:01Z" }
    }
  ]
}
```

### 4.2 Pebble timeseries path

```json
{
  "update": {
    "entity": "order",
    "id": 42,
    "version": 7,
    "data": { "state": "shipped", "updated_at": "2026-05-13T14:22:01Z" }
  },
  "timeseries": [
    {
      "timeline": 1,
      "dims":     [42],
      "time":     "2026-05-13T14:22:01.123456789Z",
      "nums":     [8.0],
      "payload":  "<optional raw bytes>"
    }
  ]
}
```

`append` may be omitted or empty when `timeseries` is non-empty.

### 4.3 Mixed path (both stores in one request)

```json
{
  "update":     { "entity": "order", "id": 42, "version": 7, "data": { ... } },
  "append":     [ { "entity": "event_log", "data": { ... } } ],
  "timeseries": [ { "timeline": 1, "dims": [42], "time": "...", "nums": [8.0] } ]
}
```

---

## 5. Field Reference

### 5.1 `update` object (required)

| Field     | Type    | Required | Description |
|-----------|---------|----------|-------------|
| `entity`  | string  | yes      | Entity type. Any type valid for `save/{id}`. |
| `id`      | integer | yes      | Entity ID. Must be a positive integer. |
| `data`    | object  | yes      | Full document to write. Replaces the existing document if the entity exists. |
| `version` | integer | no       | CAS check. Write proceeds only if stored `_version` equals this value. Omitting `version` is an unconditional upsert. |

`id` is a positive integer, not a string. olu's entity ID space is `int64`;
14-digit numeric strings fit without loss.

### 5.2 `append` array (optional)

| Field    | Type    | Required | Description |
|----------|---------|----------|-------------|
| `entity` | string  | yes      | Entity type. Any type valid for `POST /{entity}`. |
| `id`     | integer | no       | Record ID. Must be positive if supplied. If omitted, olu assigns the next sequence ID. |
| `data`   | object  | yes      | The document to insert. |

Maximum 25 entries. Requests exceeding this are rejected with OLU-CM004.

### 5.3 `timeseries` array (optional)

The timeline must be pre-defined for the tenant via `POST /ts/timelines`.
`/commit` does not define timelines.

| Field      | Type             | Required | Description |
|------------|------------------|----------|-------------|
| `timeline` | uint16           | yes      | Timeline ID. Must not be 0 (reserved). |
| `dims`     | []uint64         | yes      | Dimension values. Length must exactly match the timeline definition. |
| `time`     | RFC3339Nano      | yes      | Event timestamp. Must not be zero. Nanosecond precision preserved. |
| `nums`     | []float64        | no       | Up to 16 numeric fields. NaN rejected. |
| `payload`  | bytes (base64)   | no       | Raw bytes up to 64 KiB. Format is caller's choice. |

Maximum `OLU_TS_MAX_BATCH_SIZE` entries (default 5000). Requests exceeding
this are rejected with OLU-CM014.

### 5.4 Empty-array rule

At least one of `append` or `timeseries` must be non-empty. A request with
both absent or empty is rejected with OLU-CM003. Callers that need only a
conditional upsert with no history should use `save/{id}` directly.

---

## 6. Atomicity and Write Ordering

### 6.1 SQLite-only path (`append`, no `timeseries`)

The update upsert and all append inserts execute within a single SQLite
transaction. Either all commit or none do.

```
BEGIN IMMEDIATE
  SELECT _version ...       (CAS check, if version supplied)
  INSERT OR REPLACE ...     (update)
  INSERT ...                (append[0])
  ...
COMMIT
```

`BEGIN IMMEDIATE` acquires the write lock at the start of the transaction,
eliminating interleaving between the CAS check and the update. The
`withRetry` wrapper retries up to 7 times with exponential backoff on
`SQLITE_BUSY`; it exits immediately on `ErrConflict` so CAS is fully
preserved across retries.

### 6.2 Pebble timeseries path (`timeseries` present)

True cross-engine ACID atomicity across SQLite and Pebble is not possible
without a two-phase commit coordinator. `/commit` instead provides
**write-order atomicity with synchronous tombstone rollback**, which gives
equivalent observable behaviour on the happy path and a bounded, safe
failure surface.

Execution sequence when `timeseries` is non-empty:

```
1. Validate all fields — no writes yet.
   Pre-encode Pebble keys (needed for rollback).

2. AppendBatch -> write TS events to Pebble.
   On failure -> 500 OLU-CM015. SQLite untouched. Caller retries safely.

3. BEGIN IMMEDIATE / execute SQLite transaction.
   On success -> 200.
   On failure -> DeleteKeys(pre-encoded keys) synchronously.
     DeleteKeys success -> 409/500 as appropriate.
                           Entity unchanged. Pebble tombstoned.
     DeleteKeys failure -> 500 OLU-CM016. Manual remediation required.
```

**Why Pebble first?** A Pebble failure before SQLite is touched is a clean
no-op: neither store has been modified and the caller retries the entire
request. The alternative (SQLite first) requires a persistent recovery log
for the case where SQLite commits but Pebble fails — a substantially more
complex recovery path.

**Why pre-encode keys?** The rollback `DeleteKeys` call must use exactly
the same byte-level keys that were written. Pre-encoding before the first
write ensures the mapping is unambiguous and avoids re-encoding under error
conditions.

### 6.3 The OLU-CM016 double-failure case

If Pebble write succeeds (step 2), SQLite fails (step 3), and `DeleteKeys`
also fails:

- **Entity state is unchanged.** SQLite never committed.
- **Pebble may contain an orphaned event.** The tombstone write failed.

This is a monitoring alert condition. The structured log entry at `error`
level includes both the SQLite error and the DeleteKeys error. A
`DeleteKeys` failure against a healthy Pebble instance should be extremely
rare — it typically indicates a Pebble crash or filesystem issue that would
surface through other monitoring channels first.

### 6.4 Failure surface summary

| Failure point | Entity state | Pebble state | Recovery |
|---|---|---|---|
| Validation fails | Unchanged | Unchanged | Clean — 400, no writes |
| Pebble write fails | Unchanged | Unchanged | Clean — 500 OLU-CM015, retry |
| SQLite fails, tombstone succeeds | Unchanged | Tombstoned | Clean — 409/500, retry |
| SQLite fails, tombstone fails | Unchanged | Orphaned | OLU-CM016 alert, manual DeleteKeys |
| Both succeed | Advanced | Written | Happy path |

---

## 7. Response Shape

### 7.1 Success — `200 OK`

```json
{
  "update": {
    "entity":  "order",
    "id":      42,
    "created": false,
    "version": 8
  },
  "appended": [
    { "entity": "event_log", "id": 10042 }
  ],
  "ts_accepted": 1
}
```

`update.created` is `true` if the entity did not exist before this commit.
`update.version` is the new `_version` after the commit.
`appended` lists all inserted SQLite records in request order.
`ts_accepted` is the count of Pebble events written; omitted when zero.

### 7.2 Version conflict — `409 Conflict`

```json
{
  "error": {
    "code":    "OLU-CM001",
    "message": "Version conflict: order id 42 has been modified",
    "status":  409
  },
  "current_version": 9
}
```

No partial writes occur on a `409`. Re-read the entity, recompute the
transition, and retry with the new version.

---

## 8. CAS Protocol for FSM Executors

### 8.1 Entity-append path (current shelf-compose behaviour)

```
GET  /tenant/{t}/objects/{id}
<-  { "_version": 7, "state": "active" }

POST /tenant/{t}/commit
->  {
      "update": { "entity": "objects", "id": 42, "version": 7,
                  "data": { "state": "archived" } },
      "append": [ { "entity": "timeseries",
                    "data": { "asset_id": 42, "from": "active", "to": "archived" } } ]
    }
<-  200  { "update": { "version": 8 }, "appended": [{ "id": 10042 }] }
<-  409  { "current_version": 9 }  ->  re-read and retry
```

### 8.2 Pebble timeseries path (patched78 forward)

```
POST /tenant/{t}/commit
->  {
      "update": { "entity": "objects", "id": 42, "version": 7,
                  "data": { "state": "archived" } },
      "timeseries": [ { "timeline": 1, "dims": [42],
                        "time": "2026-05-13T14:22:01.123456789Z",
                        "nums": [7.0] } ]
    }
<-  200  { "update": { "version": 8 }, "ts_accepted": 1 }
<-  409  { "current_version": 9 }  ->  re-read and retry
<-  500  OLU-CM015                 ->  retry whole request (SQLite untouched)
```

Migration from 8.1 to 8.2 does not require a flag day. shelf-compose can
send both `append` and `timeseries` in parallel during a transition window,
then drop the entity append once the Pebble path is confirmed in production.

---

## 9. Financial Application Example

```json
{
  "update": {
    "entity": "account", "id": 42, "version": 311,
    "data": { "balance": 9432.17, "currency": "USD", "last_tx": "tx-001" }
  },
  "append": [
    {
      "entity": "transaction", "id": 20260513001,
      "data": { "account_id": 42, "amount": -567.83, "balance_after": 9432.17,
                "description": "Wire transfer", "ts": "2026-05-13T14:32:00Z" }
    }
  ]
}
```

The `version` check prevents a double-spend. `balance_after` is set by the
caller before the request; because the commit is atomic, the stored record
always reflects the actual resulting balance.

---

## 10. History Reconstruction

Because every state change passes through `/commit`, the append-only records
form a complete and ordered history. This makes current state reconstructable
from history, enables point-in-time queries, and satisfies common audit
requirements for regulated domains. This property holds only for writes made
through `/commit` — direct writes to `save/{id}` or `objects/{id}` bypass
the audit trail.

---

## 11. Relationship to Existing Endpoints

| Endpoint | Atomic | CAS | SQLite appends | Pebble TS | Use when |
|---|---|---|---|---|---|
| `POST objects/{id}` | yes | no | no | no | Create entity, no prior state |
| `POST save/{id}` | yes | optional | no | no | Upsert; no audit trail |
| `PUT {entity}/{id}` | yes | optional | no | no | Full replace |
| `PATCH {entity}/{id}` | yes | optional | no | no | Partial update |
| `POST commit` | yes | optional | optional | optional | State transition with audit trail |

---

## 12. Backend Availability

`/commit` is **only available on the SQLite backend**. The jsonfile backend
returns `501 OLU-CM009`. Per-test SQLite databases (temp directory) are the
correct substitute for integration tests.

The Pebble timeseries path additionally requires:

- `OLU_TIMESERIES_ENABLED=true`
- Tenant provisioned via `POST /ts/provision`
- Timelines pre-defined via `POST /ts/timelines`

A request carrying `timeseries` events where any of these conditions are
not met is rejected before any writes begin.

---

## 13. Strict Mode (`OLU_STRICT_COMMIT`)

| Setting | Default | Behaviour |
|---------|---------|-----------|
| `true` | **yes** | Schema validation and graph cycle prechecks run before the storage transaction. |
| `false` | no | Only structural checks (entity names, ID positivity, counts) are performed. |

Structural validation and in-memory graph updates are always performed
regardless of this setting.

---

## 14. Error Codes

### Entity path

| Code      | HTTP | Meaning |
|-----------|------|---------|
| OLU-CM001 | 409  | Version conflict. `current_version` present in response. |
| OLU-CM002 | 400  | `update` object missing. |
| OLU-CM003 | 400  | Both `append` and `timeseries` absent or empty. |
| OLU-CM004 | 400  | `append` array exceeds 25 entries. |
| OLU-CM005 | 400  | `update.entity` is not a valid entity type. |
| OLU-CM006 | 400  | One or more `append` entries reference an invalid entity type. |
| OLU-CM007 | 409  | An `append` entry specifies an explicit `id` that already exists. |
| OLU-CM008 | 500  | SQLite transaction failed. All changes rolled back. |
| OLU-CM009 | 501  | `/commit` not available on current backend. |

### Timeseries path

| Code      | HTTP | Meaning |
|-----------|------|---------|
| OLU-CM010 | 400  | `timeseries` present but `OLU_TIMESERIES_ENABLED` is false. |
| OLU-CM011 | 400  | Tenant not provisioned for timeseries. |
| OLU-CM012 | 400  | Unknown timeline, reserved timeline 0, or zero event time. |
| OLU-CM013 | 400  | Wrong number of dimension values for the timeline. |
| OLU-CM014 | 400  | `timeseries` array exceeds `OLU_TS_MAX_BATCH_SIZE`. |
| OLU-CM015 | 500  | Pebble write failed. SQLite untouched. Caller may retry the whole request. |
| OLU-CM016 | 500  | Pebble write succeeded, SQLite failed, AND tombstone (DeleteKeys) failed. Orphaned TS entry possible. Manual remediation required. |

OLU-CM007: `append` entries are inserts, not upserts. Use olu-generated
IDs (omit `id`) unless the ID is guaranteed unique by construction.

OLU-CM015 is safe to retry without any state cleanup: Pebble written before
SQLite means a Pebble failure leaves both stores untouched.

OLU-CM016 is a monitoring alert. The structured log at `error` level
identifies the orphaned Pebble keys by `(timeline, dims, time)`.

---

## 15. Configuration

| Variable | Default | Description |
|---|---|---|
| `OLU_STRICT_COMMIT` | `true` | Enable schema validation and graph prechecks. |
| `OLU_TIMESERIES_ENABLED` | `false` | Required for the `timeseries` array path. |
| `OLU_TS_MAX_BATCH_SIZE` | `5000` | Maximum `timeseries` entries per commit. |

All other Pebble tuning variables are documented in `TIMESERIES_DESIGN_V3.md`.

---

## 16. Implementation Notes

### 16.1 Write-order design rationale

Two orderings were considered:

**SQLite first** (rejected): if SQLite commits but Pebble fails, a
persistent recovery log (`_ts_pending` table, background worker, dead-letter
handling) is needed to replay the Pebble write. This is a non-trivial
subsystem.

**Pebble first** (adopted): a Pebble failure before SQLite is touched is a
clean no-op. If Pebble succeeds but SQLite fails, a single synchronous
`DeleteKeys` tombstone cleans up the orphaned entry. Tombstones are LSM
delete markers — cheap in Pebble's write path and immediately visible to
readers.

### 16.2 Key pre-encoding

Pebble keys are encoded from `(Timeline, Dims, Time)` before the first
write. This validates event coordinates early and ensures the rollback
`DeleteKeys` call uses exactly the same byte sequences that were written.

### 16.3 Storage types

```go
type CommitRequest struct {
    Update     CommitUpdate    `json:"update"`
    Append     []CommitAppend  `json:"append"`
    Timeseries []CommitTSEvent `json:"timeseries,omitempty"`
}

type CommitTSEvent struct {
    Timeline uint16    `json:"timeline"`
    Dims     []uint64  `json:"dims"`
    Time     time.Time `json:"time"`
    Nums     []float64 `json:"nums,omitempty"`
    Payload  []byte    `json:"payload,omitempty"`
}

type CommitResult struct {
    Update     CommitUpdateResult   `json:"update"`
    Appended   []CommitAppendResult `json:"appended"`
    TSAccepted int                  `json:"ts_accepted,omitempty"`
}
```

### 16.4 Deletion interface

The `timeseries.Store` interface exposes:

```go
Delete(ctx context.Context, e Event) error
DeleteKeys(ctx context.Context, keys [][]byte) error
```

Backends that do not support deletion return `timeseries.ErrDeleteNotSupported`.
`PebbleStore` implements both using Pebble's native tombstone mechanism.
`DeleteKeys` does not adjust event counters (counters are approximate by
design); `Delete` does.

### 16.5 tsManager field type

`Server.tsManager` is typed as `timeseries.Manager` (interface) to allow
test injection via `Server.SetTSManager(m)`. The production value is always
`*timeseries.DefaultManager` as returned by `timeseries.NewManager`.

---

