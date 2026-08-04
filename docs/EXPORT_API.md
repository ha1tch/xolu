# Export API

The export endpoint creates a point-in-time snapshot of the server's data as a
zip archive. The archive contains the raw SQLite database file plus any graph
data files that were written to disk.

## Endpoint

```
GET /api/v1/export
```

No query parameters. No request body. Authentication headers apply if auth is
enabled.

## Response

Returns a zip archive with `Content-Type: application/zip` and a timestamped
filename:

```
Content-Disposition: attachment; filename="xolu-export-2026-06-14T120000Z.zip"
```

The archive always contains:

```
manifest.json     Export metadata (see below)
xolu.db           SQLite database — the primary data file
```

When the graph is enabled, a human-readable JSON representation of the graph is
included (the graph itself lives in the per-tenant edge tables inside the main
database, which is exported as part of the database file):

```
graph.json        Human-readable graph export
```

## Manifest

```json
{
  "version":      "0.9.9",
  "exported_at":  "2026-06-14T12:00:00Z",
  "storage_type": "sqlite",
  "graph_enabled": true,
  "database_file": "xolu.db",
  "graph_json":   "graph.json"
}
```

| Field | Description |
|---|---|
| `version` | xolu server version at export time |
| `exported_at` | RFC 3339 timestamp |
| `storage_type` | Always `"sqlite"` |
| `graph_enabled` | Whether the graph subsystem was active |
| `database_file` | Always `"xolu.db"` for SQLite deployments |
| `graph_json` | `"graph.json"` if a JSON graph export was included |

## The `xolu.db` file

For SQLite deployments (the only production-supported backend), `xolu.db`
is a copy of the live SQLite database file. The server issues
`PRAGMA wal_checkpoint(TRUNCATE)` immediately before copying to ensure all
recent writes are flushed from the WAL into the main file.

**Schema note:** The internal table structure has changed across xolu versions.
As of v0.9.9 (rc11), entity data is stored in per-tenant tables named
`t0000_nodes`, `t0001_nodes`, etc., not a global `entities` table. Do not
rely on specific table names when querying the exported file directly; the
schema may change between minor versions.

### Recommended uses of the exported database

**Reimport into a fresh xolu instance:**

```bash
# Extract the export zip first, then stop the new instance and replace
# its database with the extracted xolu.db, then restart
unzip xolu-export-*.zip -d extracted/
cp extracted/xolu.db /path/to/new-instance/xolu.db
```

**Offline read-only queries:**

Use `sqlite3` or any SQLite-aware tool and inspect `sqlite_master` first to
discover the current table names:

```bash
sqlite3 xolu.db ".tables"
# t0000_nodes  t0000_nseq  t0001_nodes  ...

sqlite3 xolu.db "SELECT entity_type, COUNT(*) FROM t0000_nodes GROUP BY entity_type"
```

**Backup and disaster recovery:**

The export is safe to call during normal operation (WAL checkpoint is
non-blocking). Schedule it via cron or a monitoring job:

```bash
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
curl -sf -o "xolu-backup-${TIMESTAMP}.zip" http://localhost:8080/api/v1/export
```

## Limitations

- The export streams directly to the response; no temporary file is created on
  the server. For large databases this means the client must maintain the
  connection for the full download duration.
- Blob storage (`XOLU_BLOB_ENABLED`) and timeseries data
  (`XOLU_TIMESERIES_ENABLED`) are not included in the export. Back these up
  separately from the filesystem (`XOLU_BLOB_DIR` and the timeseries directory).
- There is no import endpoint. Restore by stopping the server and replacing the
  SQLite file.
- The export does not include schema files from `XOLU_SCHEMA_DIR`. These should
  be version-controlled separately.

## Tenant-scoped variant

There is no per-tenant export endpoint. The export always covers the entire
database file including all tenants.

**Disabled entirely under `XOLU_TENANT_MODE=strict`.** `/export` is
registered only among xolu's non-tenant-scoped routes (it operates
against the default/tenant-0 store, matching `/oql/query` and
`/search`'s own scoping); strict mode disables that whole route group
to prevent accidental unscoped queries. A client that prefixes requests
with a tenant path (the usual pattern for every other endpoint) must
not do so here — `/export` does not exist under any tenant-prefixed
path, only the bare, unprefixed one, and only outside strict mode.
