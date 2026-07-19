# Blob Storage API

Blob storage provides a key-addressed binary object store. Content is stored on
disk in a content-addressed layout (SHA-256); caller-supplied keys are aliases
pointing to SHA-256 digests. Blobs are tenant-isolated: each tenant has a
separate namespace with its own quota and GC.

Enable with `XOLU_BLOB_ENABLED=true`. Endpoints return `XOLU-BL001` (501) when
blob storage is not enabled.

An S3-compatible surface is available separately; see [S3 API](#s3-compatible-api).

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `XOLU_BLOB_ENABLED` | `false` | Enable blob storage |
| `XOLU_BLOB_DIR` | `{base_dir}/blobs` | Root directory for blob files |
| `XOLU_BLOB_MAX_SIZE` | `67108864` | Max bytes per individual blob (0 = unlimited) |
| `XOLU_BLOB_MAX_TOTAL_BYTES` | `0` | Soft per-tenant storage quota in bytes (0 = unlimited) |
| `XOLU_BLOB_GC_ENABLED` | `false` | Enable background GC of unreferenced blobs |
| `XOLU_BLOB_GC_INTERVAL` | `3600` | GC sweep interval in seconds |
| `XOLU_BLOB_GC_GRACE_PERIOD` | `600` | Seconds an unreferenced blob must remain in quarantine before hard deletion |

The quota (`XOLU_BLOB_MAX_TOTAL_BYTES`) is enforced against a sampled usage
figure — not a real-time counter. It is a soft cap: writes are blocked only if
the sampler has already observed the tenant exceeding the limit. A per-tenant
override can be set at runtime via the dynamic configuration system:

```
PUT /api/v1/admin/config/tenant.{name}/blob.max_bytes   <number>
PUT /api/v1/admin/config/global/blob.max_bytes           <number>
```

Per-tenant overrides take precedence over global overrides, which take
precedence over the static environment variable.

---

## Storage model

Blobs are stored under `{blob_dir}/t-{tenant}/{xx}/{sha256hex}` where `xx` is
the first two hex characters of the SHA-256 (sharding to keep directory entry
counts bounded). An alias index under `{blob_dir}/t-{tenant}/.keys/{key}`
contains the SHA-256 digest the key points to.

`DELETE` removes the key alias only. The blob file is not removed immediately;
the background GC (`XOLU_BLOB_GC_ENABLED=true`) sweeps unreferenced blobs after
a configurable grace period (`XOLU_BLOB_GC_GRACE_PERIOD`).

---

## Key rules

A key is any non-empty string except:
- Cannot contain `/` or `\`
- Cannot be `.` or `..`
- Cannot start with `.`

Keys that violate these rules return `XOLU-BL004` (400).

---

## Endpoints

All endpoints have tenant-scoped variants under `/api/v1/tenant/{tenant_id}/blob/...`.
The default (non-tenant) route uses the tenant name `"default"`.

---

### `POST /api/v1/blob`

Store a blob. The request body is the raw content.

**Key selection (precedence order):**

1. `X-Blob-Key` request header
2. `?key=` query parameter
3. If neither is supplied, the SHA-256 of the content is used as the key
   (content-addressed mode, no alias written)

**Headers:**

| Header | Description |
|---|---|
| `X-Blob-Key` | Caller-supplied key (optional) |
| `Content-Type` | Preserved as-is; defaults to `application/octet-stream` |

**Response: 201 Created** (new blob) or **200 OK** (key already pointed to same content):

```json
{
  "key":     "my-document.pdf",
  "sha256":  "e3b0c44298fc1c149afb...",
  "size":    204800,
  "created": true
}
```

In content-addressed mode (no key supplied), `key` in the response is the
SHA-256 digest itself.

**Error codes:**

| Code | HTTP | Condition |
|---|---|---|
| `XOLU-BL001` | 501 | Blob storage not enabled |
| `XOLU-BL003` | 413 | Content exceeds `XOLU_BLOB_MAX_SIZE` |
| `XOLU-BL004` | 400 | Invalid key |
| `XOLU-BL006` | 413 | Tenant quota exceeded |
| `XOLU-BL005` | 500 | Filesystem write failure |

---

### `GET /api/v1/blob/{key}`

Retrieve blob content. Streams the raw bytes.

**Response headers:**

| Header | Value |
|---|---|
| `Content-Type` | Preserved from the original `PUT` |
| `X-Blob-SHA256` | Hex SHA-256 of the content |
| `X-Blob-MD5` | Hex MD5 of the content (S3-style digest) |
| `X-Blob-Size` | Size in bytes |
| `ETag` | `"<sha256hex>"` |

**Error codes:**

| Code | HTTP | Condition |
|---|---|---|
| `XOLU-BL002` | 404 | Key not found |
| `XOLU-BL004` | 400 | Invalid key |
| `XOLU-BL005` | 500 | Filesystem read failure |

---

### `HEAD /api/v1/blob/{key}`

Returns metadata without body. Same response headers as `GET`, no body.

Returns 404 without a JSON body when the key is not found.

---

### `DELETE /api/v1/blob/{key}`

Remove the key alias. The blob file is not immediately deleted; GC handles
unreferenced content.

**Response 200 OK:**

```json
{
  "key":     "my-document.pdf",
  "deleted": true
}
```

**Error codes:**

| Code | HTTP | Condition |
|---|---|---|
| `XOLU-BL002` | 404 | Key not found |
| `XOLU-BL005` | 500 | Filesystem error |

---

### `GET /api/v1/blob`

List all blobs for the tenant. Optionally filter by key prefix.

**Query parameters:**

| Parameter | Description |
|---|---|
| `prefix` | Filter to keys starting with this prefix (optional) |

**Response 200 OK:**

```json
{
  "tenant": "default",
  "prefix": "images/",
  "count":  3,
  "blobs": [
    {
      "key":          "images/avatar.png",
      "sha256":       "e3b0c44...",
      "size":         4096,
      "content_type": "image/png",
      "stored_at":    "2026-06-01T12:00:00Z"
    }
  ]
}
```

Results are sorted by key ascending.

---

### `GET /api/v1/blob/usage`

Returns cached disk usage for the tenant. Data is served from the background
usage sampler; the filesystem is never walked at request time.

**Response 200 OK:**

```json
{
  "tenant":     "default",
  "blob_count": 142,
  "key_count":  156,
  "bytes":      10485760,
  "sampled_at": "2026-06-01T12:03:00Z"
}
```

`blob_count` is the number of unique content blobs (distinct SHA-256 values).
`key_count` is the number of key aliases, which may exceed `blob_count` when
multiple keys point to the same content. `sampled_at` is absent if the sampler
has not yet completed its first sweep (all counts will be zero in this case).

No S3-compatible equivalent exists for this endpoint.

---

## S3-compatible API

An S3-compatible surface runs on a separate listener when `XOLU_S3_ENABLED=true`.

| Variable | Default | Description |
|---|---|---|
| `XOLU_S3_ENABLED` | `false` | Enable S3-compatible listener |
| `XOLU_S3_PORT` | `9091` | Port for S3 listener (must differ from `XOLU_PORT` and `XOLU_METRICS_PORT`) |
| `XOLU_S3_HOST` | `0.0.0.0` | Bind address for S3 listener |
| `XOLU_S3_REQUIRE_AUTH` | `false` | Reject requests without an `Authorization` header |

**Tenant mapping:** The bucket name is the tenant name. The access key ID from
the AWS Signature V4 `Authorization` header is used as the tenant name when
present, falling back to the bucket name when absent. The signature itself is
not verified; configure S3 clients with any non-empty secret key.

**Operations supported:**

| S3 operation | Method + Path | Notes |
|---|---|---|
| PutObject | `PUT /{bucket}/{key}` | Stores blob; returns 200 with `ETag` header only (no body) |
| GetObject | `GET /{bucket}/{key}` | Streams content with `Content-Type`, `ETag`, `Content-Length` |
| HeadObject | `HEAD /{bucket}/{key}` | Returns metadata headers, no body |
| DeleteObject | `DELETE /{bucket}/{key}` | Removes key alias |
| ListObjectsV2 | `GET /{bucket}?list-type=2` | Returns XML; supports `prefix` and `max-keys` parameters |
| HeadBucket | `HEAD /{bucket}` | Returns 200 if tenant exists, 404 otherwise |

Errors are returned as S3-format XML rather than xolu's JSON error envelope.

`BlobEnabled=true` is required for `S3Enabled=true`; the configuration
validator rejects the combination if only S3 is enabled.
