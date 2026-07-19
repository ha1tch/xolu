# Dynamic Configuration

Dynamic configuration (`dynconfig`) provides a runtime-settable key-value store
that takes effect without restarting the server. Settings are organised in a
two-level hierarchy: namespace → key → JSON value. The store is backed by a
single JSON file that is reloaded periodically.

Enable with `XOLU_DYNCONFIG_ENABLED=true`. The admin API additionally requires
`XOLU_DYNCONFIG_API_ENABLED=true`.

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `XOLU_DYNCONFIG_ENABLED` | `false` | Enable the dynamic configuration system |
| `XOLU_DYNCONFIG_FILE` | `{base_dir}/dynconfig.json` | Path to the backing JSON file |
| `XOLU_DYNCONFIG_RELOAD_INTERVAL` | `30` | File reload interval in seconds |
| `XOLU_DYNCONFIG_API_ENABLED` | `false` | Expose the admin API endpoints |

The file is read at startup and then reloaded every `XOLU_DYNCONFIG_RELOAD_INTERVAL`
seconds. If the file does not exist at startup, the store begins empty. If a
reload finds a malformed file, the existing in-memory store is left unchanged
and a warning is logged.

Writes via the API update the in-memory store and flush to disk atomically
(write to a temp file, then rename). The flushed file is authoritative; a
subsequent reload will see the written values.

---

## Data model

### File format

```json
{
  "global": {
    "blob.max_bytes": 104857600
  },
  "tenant.acme": {
    "blob.max_bytes": 10485760,
    "feature.beta": true
  }
}
```

### Namespaces

A namespace is any non-empty string matching `[a-zA-Z0-9._-]+`. Two namespaces
have conventional meaning within xolu:

| Namespace | Meaning |
|---|---|
| `global` | System-wide overrides consulted when no per-tenant value exists |
| `tenant.{name}` | Overrides for one tenant, taking precedence over `global` |

Any other namespace string is accepted. xolu does not interpret it.

### Keys

A key is any non-empty string matching `[a-zA-Z0-9._-]+`.

### Values

Values are raw JSON: number, string, boolean, null, array, or object. The
package validates that every value is well-formed JSON before accepting a write.

---

## Known keys used by xolu

| Namespace | Key | Type | Effect |
|---|---|---|---|
| `global` | `blob.max_bytes` | number | Per-tenant blob quota fallback (bytes) |
| `tenant.{name}` | `blob.max_bytes` | number | Per-tenant blob quota override (bytes) |

Only these two keys are actively consumed by xolu's production code. All other
keys are stored but not read by xolu itself; application code can use the store
for its own configuration.

---

## Admin API

Routes are registered only when both `XOLU_DYNCONFIG_ENABLED=true` and
`XOLU_DYNCONFIG_API_ENABLED=true`. All endpoints return `XOLU-DC001` (503) when
dynconfig is disabled.

---

### `GET /api/v1/admin/config`

Returns the entire configuration store as a JSON object.

**Response 200 OK:**

```json
{
  "global": {
    "blob.max_bytes": 104857600
  },
  "tenant.acme": {
    "blob.max_bytes": 10485760
  }
}
```

---

### `GET /api/v1/admin/config/{namespace}`

Returns all keys in one namespace.

**Response 200 OK:**

```json
{
  "blob.max_bytes": 104857600,
  "feature.beta":  true
}
```

Returns `XOLU-DC002` (404) if the namespace has no keys.

---

### `GET /api/v1/admin/config/{namespace}/{key}`

Returns the raw JSON value for one key.

**Response 200 OK** — raw JSON value (not wrapped in an object):

```
104857600
```

```
true
```

```
"some-string"
```

Returns `XOLU-DC002` (404) if the key is not set.

---

### `PUT /api/v1/admin/config/{namespace}/{key}`

Set a value. The request body must be a valid JSON value.

```http
PUT /api/v1/admin/config/global/blob.max_bytes
Content-Type: application/json

104857600
```

**Response 200 OK:**

```json
{
  "namespace": "global",
  "key":       "blob.max_bytes",
  "status":    "set"
}
```

**Error codes:**

| Code | HTTP | Condition |
|---|---|---|
| `XOLU-DC003` | 400 | Namespace or key fails character validation, empty body, or body is not valid JSON |
| `XOLU-DC004` | 500 | Filesystem flush failed |

The write is applied to the in-memory store and flushed to disk before the
response is returned. A failed flush returns `XOLU-DC004`; the value may or may
not have been applied to the in-memory store in this case.

---

### `DELETE /api/v1/admin/config/{namespace}/{key}`

Remove one key. Deleting the last key in a namespace removes the namespace
from the store entirely. Deleting a non-existent key is a no-op (returns 200).

**Response 200 OK:**

```json
{
  "namespace": "global",
  "key":       "blob.max_bytes",
  "status":    "deleted"
}
```

**Error codes:**

| Code | HTTP | Condition |
|---|---|---|
| `XOLU-DC004` | 500 | Filesystem flush failed |

---

## Error codes reference

| Code | HTTP | Meaning |
|---|---|---|
| `XOLU-DC001` | 503 | Dynamic configuration is not enabled |
| `XOLU-DC002` | 404 | Namespace or key not found |
| `XOLU-DC003` | 400 | Invalid namespace, key, or value |
| `XOLU-DC004` | 500 | Storage write failed |
