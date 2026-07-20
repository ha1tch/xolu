# iolu — interactive xolu

`iolu` (interactive xolu) is the offline administrative CLI. For planned
operational commands beyond the current set, see
[the operations roadmap](proposals/iolu-operations-roadmap.md). It operates directly against
the SQLite database file without starting the HTTP server. Use it to initialise
new deployments, manage the tenant registry, and inspect database state.

> **Compatibility note (v0.9.9):** Several `iolu` commands were written against
> the v0.9.7/v0.9.8 schema and query the old `entities` table, which no longer
> exists in v0.9.9. Commands that are currently stale are marked below.
> Commands that only touch the `tenants` table work correctly against v0.9.9.

---

## Build

`iolu` is built separately from `xolu`:

```bash
go build -o iolu ./cmd/iolu
```

---

## Command reference

### `iolu db init`

Initialises a new xolu database file, creating all required tables.

```bash
iolu db init \
  --db /path/to/xolu.db \
  [--tenant name[:id]] \
  [--graph] \
  [--ts-dir /path/to/ts]
```

| Flag | Description |
|---|---|
| `--db` | Path to the SQLite database to create (required) |
| `--tenant` | Pre-register a tenant (`name` or `name:id`). Repeatable. |
| `--graph` | Create graph tables for tenant 0 |
| `--ts-dir` | Create timeseries directory structure here |

> **⚠ Stale against v0.9.9.** This command creates the old `entities` table
> schema. For new v0.9.9 deployments, start `xolu` with an empty `XOLU_DB_PATH`
> and it will initialise the correct schema automatically.

---

### `iolu db status`

Prints a summary of a database file: size, schema versions, table row counts,
graph tables, registered tenants, and timeseries directories.

```bash
iolu db status \
  --db /path/to/xolu.db \
  [--base-dir /path/to/data]
```

> **⚠ Partially stale against v0.9.9.** The table row counts section queries
> the old `entities` table. Tenant and graph table counts are accurate.
> Schema versions and database size are always accurate.

---

### `iolu db upgrade`

Applies numbered schema migrations in sequence.

```bash
iolu db upgrade --db /path/to/xolu.db
```

Currently applies:
- **v2** — inserts a `schema_version` record
- **v3** — adds `_version INTEGER` column to the `entities` table

> **⚠ Stale against v0.9.9.** This covers migrations up to the v0.9.8 era
> only. The v0.9.9 table rename (`entities` → `t0000_nodes` family) is not
> handled here. See [UPGRADE.md](UPGRADE.md) for the v0.9.9 migration path.

---

### `iolu tenant create`

Registers a new tenant name in the `tenants` table. Does not create any
entity tables (those are created by `xolu` at startup or first request).

```bash
iolu tenant create \
  --db /path/to/xolu.db \
  --name acme \
  [--id 5]
```

| Flag | Description |
|---|---|
| `--db` | Database path (required) |
| `--name` | Tenant name (required) |
| `--id` | Numeric tenant ID; auto-assigned if omitted |

✓ **Works correctly against v0.9.9.**

---

### `iolu tenant list`

Lists all registered tenants with their IDs, creation time, and entity counts.

```bash
iolu tenant list --db /path/to/xolu.db
```

> **⚠ Partially stale against v0.9.9.** The entity count column queries the
> old `entities` table and will show 0 for all tenants. Tenant ID, name, and
> created-at are accurate.

---

### `iolu tenant info`

Shows detailed information about a single tenant: entity breakdown by type,
graph edge count, and timeseries status.

```bash
iolu tenant info \
  --db /path/to/xolu.db \
  --name acme \
  [--base-dir /path/to/data]
```

> **⚠ Partially stale against v0.9.9.** The entity breakdown queries the old
> `entities` table and will return empty. Graph edge count (reads
> `graph_tXXXX` table) and timeseries directory check are accurate.

---

### `iolu tenant delete`

Removes a tenant from the `tenants` registry. Does not delete entity data.

```bash
iolu tenant delete \
  --db /path/to/xolu.db \
  --name acme \
  [--force]
```

| Flag | Description |
|---|---|
| `--force` | Delete even if entity data exists under this tenant ID |

Without `--force`, the command warns and aborts if it detects entity rows for
the tenant.

> **⚠ Partially stale against v0.9.9.** The entity data check queries the old
> `entities` table. On a v0.9.9 database, `--force` is never required (the
> check finds nothing) but entity data in `t0000_nodes` etc. is not removed.
> The `tenants` table removal itself works correctly.

✓ Tenant registry removal works. Manual cleanup of `tXXXX_*` tables required.

---

### `iolu tenant provision-ts`

Creates the timeseries directory structure on disk for a tenant. Required
before the HTTP timeseries API can write to that tenant.

```bash
iolu tenant provision-ts \
  --db /path/to/xolu.db \
  --name acme \
  --ts-dir /path/to/ts
```

Creates `{ts-dir}/tXXXX/` where `XXXX` is the hex tenant ID. Equivalent to
calling `POST /api/v1/tenant/{id}/ts/provision` through the HTTP API, but
usable offline before `xolu` starts.

✓ **Works correctly against v0.9.9.**

---

## What to use iolu for in v0.9.9

Given the stale commands above, the reliable uses of `iolu` against a v0.9.9
database are:

- **`tenant create`** — pre-register tenants before starting xolu in strict mode
- **`tenant provision-ts`** — provision timeseries offline
- **`tenant delete`** (registry only) — remove a tenant from the tenants table

For everything else — inspecting entity counts, row breakdown by type, entity
data itself — use the xolu HTTP API (`GET /api/v1/tenant/{id}/...`) or `sqlite3`
directly against the `tXXXX_nodes` tables.
