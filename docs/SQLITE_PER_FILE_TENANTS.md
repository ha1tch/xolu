# SQLite Per-File Tenant Isolation — Implementation Plan

**Status:** Implemented (`XOLU_SQLITE_PER_FILE_TENANTS=true`)  
**Author:** Nadine Ostrovski  
**Date:** 2026-05-13  
**Scope:** `pkg/storage`, `pkg/oql`, `pkg/config`, `pkg/server`

> **Note (2026-06-19, v0.10.1):** the *filesystem layout* described in this
> historical plan (e.g. `data/xolu.db`, `data/sql/tXXXX/`, `data/ts/tXXXX/`) has
> been superseded by the storage-layout normalization. The current layout is
> tenant-first and derived by invariant from `--base-dir` via `pkg/storelayout`:
> per-file tenant stores live at `<BaseDir>/tXXXX/store/xolu.db` (tenant 0 at
> `t0000/store/`), timeseries at `<BaseDir>/tXXXX/ts/`, and shared mode under
> `<BaseDir>/shared/`. There is no separate `--db`/`XOLU_DB_PATH` setting. See the
> 0.10.1 CHANGELOG entry and `pkg/storelayout`. The *tenant-isolation semantics*
> documented below remain accurate; only the paths changed.

---

> **Naming note:** Table naming now follows the per-tenant `t<XXXX>_*` convention
> established in v0.9.9-rc11–rc14 rather than the `entities` + `tenant_id` column
> approach described in the background below.

## Background

Currently all tenants share a single SQLite database file. The `entities`
table carries a `tenant_id INTEGER` column; isolation is enforced at the
query level by appending `WHERE tenant_id = ?` to every read and write.
This is correct but has a cost: the `tenant_id` column participates in
every primary key and index, the OQL SQL generators must inject tenant
scoping into every pushed query, and the Go-path executor must filter
records by `tenant_id` after retrieval.

The timeseries layer already uses per-tenant directories (`tXXXX/`). This
plan makes SQLite isolation optionally match that model: when
`SQLitePerFileTenants = true`, each tenant gets its own SQLite file and
the `tenant_id` column is absent from the schema entirely.

The flag is a deployment-time decision. Both modes are fully supported;
the choice is made at server startup and does not change while the server
is running. Migration between modes is addressed at the end of this document.

---

## Files that change

| File | Nature of change |
|------|-----------------|
| `pkg/storage/storage.go` | Add `SQLitePerFileTenants bool` to `StoreConfig` |
| `pkg/storage/sqlite.go` | Conditional schema DDL; conditional `tenant_id` in all queries |
| `pkg/storage/sqlite_field_query.go` | Conditional `tenant_id` in field queries |
| `pkg/storage/dialect_sqlite.go` | Conditional `tenant_id` in adapted table DDL and queries |
| `pkg/storage/factory.go` | Pass `SQLitePerFileTenants` through to `SQLiteConfig` |
| `pkg/oql/executor.go` | Skip `sqlTenantID` injection and `filterByTenant` when per-file |
| `pkg/oql/sqlgen.go` | Conditional `AND tenant_id = ?` in blob push-down SQL |
| `pkg/oql/sqlgen_adapted.go` | Conditional `tenant_id = ?` in adapted push-down SQL |
| `pkg/oql/sqlgen_aggregate.go` | Conditional `tenant_id = ?` in aggregate push-down SQL |
| `pkg/config/config.go` | Add `SQLitePerFileTenants bool`; wire to env/config parsing |
| `pkg/server/server.go` | Per-file path derivation in `storeForTenant` |

---

## Step 1 — Config (`pkg/config/config.go`)

Add the field to `Config`:

```go
// SQLitePerFileTenants controls whether each tenant gets its own SQLite
// database file. When false (default), all tenants share one file and
// are isolated by the tenant_id column. When true, each tenant's data
// lives in a separate file derived from the base DBPath:
//
//   tenant 0 (unscoped): <DBPath>                   (e.g. data/xolu.db)
//   tenant 1:            <DBDir>/sql/t0001/<base>   (e.g. data/sql/t0001/xolu.db)
//   tenant 2:            <DBDir>/sql/t0002/<base>   (e.g. data/sql/t0002/xolu.db)
//
// The two modes use different schemas. Choose at deployment time and do
// not change while data exists — migration requires an explicit export/import.
// Ignored when StorageType is not "sqlite".
SQLitePerFileTenants bool
```

Default is `false` — existing deployments are unchanged.

Add env/config key `SQLITE_PER_FILE_TENANTS` / `sqlite_per_file_tenants`
alongside the existing storage config keys. Wire through `parseConfig` and
`Validate` (no validation constraint needed — it's a boolean).

---

## Step 2 — StoreConfig and SQLiteConfig (`pkg/storage/storage.go`, `sqlite.go`)

Add to `StoreConfig`:

```go
SQLitePerFileTenants bool // when true, tenant_id column is absent; file = isolation
```

Add to `SQLiteConfig`:

```go
PerFileTenants bool // mirrors StoreConfig.SQLitePerFileTenants
```

Add a method to `SQLiteStore` for querying the mode — used by the OQL
layer to decide whether to inject tenant scoping:

```go
func (s *SQLiteStore) IsPerFileTenant() bool {
    return s.config.PerFileTenants
}
```

Add this to an interface `TenantModeProvider` in `storage.go` so the OQL
executor can check it without a type assertion:

```go
type TenantModeProvider interface {
    IsPerFileTenant() bool
}
```

---

## Step 3 — Schema DDL (`pkg/storage/sqlite.go`)

The schema creation block currently hardcodes `tenant_id` in every table.
Replace the single schema string with a branch:

```go
func (s *SQLiteStore) createSchema(ctx context.Context) error {
    if s.config.PerFileTenants {
        return s.createSchemaPerFile(ctx)
    }
    return s.createSchemaShared(ctx)
}
```

**`createSchemaShared`** — the existing schema verbatim. No changes.

**`createSchemaPerFile`** — identical structure but:

- `entities` table: remove `tenant_id` column; primary key becomes
  `(entity_type, id)`; remove `idx_tenant_entity` index.
- `entity_sequences` table: remove `tenant_id` column; primary key
  becomes `(entity_type)`.
- `entities_fts` virtual table: remove `tenant_id UNINDEXED` column.
- `tenants` table: retained as-is (it lives in the base store file,
  which is always the shared store for tenant 0; per-file tenant databases
  do not need it — see Step 6).
- Graph edge table: unchanged — it is already per-tenant-ID named
  (`graph_t0001` etc.) and has no `tenant_id` column.

---

## Step 4 — Query methods (`pkg/storage/sqlite.go`, `sqlite_field_query.go`)

Every method that currently passes `int(s.config.TenantID)` as a
`tenant_id` parameter must branch on `s.config.PerFileTenants`.

The pattern is the same throughout:

```go
// Shared mode (current behaviour)
rows, err := stmt.QueryContext(ctx, int(s.config.TenantID), entity)

// Per-file mode
rows, err := stmt.QueryContext(ctx, entity)
```

And correspondingly the SQL strings:

```go
// Shared
const q = `SELECT data, _version FROM entities WHERE tenant_id = ? AND entity_type = ?`

// Per-file
const q = `SELECT data, _version FROM entities WHERE entity_type = ?`
```

**Implementation approach:** add a helper method pair to `SQLiteStore`:

```go
// tenantArg returns the tenant_id arg for shared mode, or nothing for per-file.
func (s *SQLiteStore) tenantArgs(extra ...interface{}) []interface{} {
    if s.config.PerFileTenants {
        return extra
    }
    return append([]interface{}{int(s.config.TenantID)}, extra...)
}

// tenantWhere returns the WHERE fragment for tenant scoping.
func (s *SQLiteStore) tenantWhere() string {
    if s.config.PerFileTenants {
        return ""
    }
    return "tenant_id = ? AND "
}
```

Using these two helpers, most query methods become one-liners rather than
full branches. The SQL string can be constructed once per method using
`tenantWhere()`, and args assembled using `tenantArgs(...)`. This avoids
duplicating 40+ query strings.

**Specific methods to update** (not exhaustive — every method that
currently passes `TenantID`):

- `Create`, `Get`, `Update`, `Patch`, `PatchValidated`, `Delete`, `Save`
- `List`, `Search`, `Count`
- `CountEntities`, `QueryWithPlan`
- `ListWithFields`, `QueryWithFields`, `ListWithFieldsAndFilter`
- FTS write methods (`indexForFTS`, `deleteFromFTS`)
- `NextID`

The `ScanGraphEdges` method already uses a per-tenant-named table
(`graph_tXXXX`) so it does not filter by `tenant_id` and requires no
change.

---

## Step 5 — Adapted table dialect (`pkg/storage/dialect_sqlite.go`)

The `SQLiteStorageDialect` builds adapted table DDL with a `tenant_id`
column. In per-file mode this column is unnecessary.

Add `PerFileTenants bool` to `SQLiteStorageDialect` and branch in:

- `BuildCreateTableSQL` — omit `tenant_id` column and its index
- `BuildSelectByIDSQL` — remove `tenant_id = ?` from WHERE
- `BuildSelectAllSQL` — remove `tenant_id = ?` from WHERE
- `BuildUpdateSQL` — remove `tenant_id = ?` from WHERE
- `BuildDeleteSQL` — remove `tenant_id = ?` from WHERE
- `BuildExistsSQL` — remove `tenant_id = ?` from WHERE

The `dialect.go` interface comment referencing `tenant_id` as a required
system column should be updated to note it is absent in per-file mode.

`SQLiteStore.RegisterAdaptedEntity` passes the dialect to
`RegisterAdaptedTable` — the dialect already carries the per-file flag at
this point, so adapted tables created in per-file mode will have the
correct schema automatically.

---

## Step 6 — `storeForTenant` (`pkg/server/server.go`)

In per-file mode, derive the per-tenant file path from the base `DBPath`:

```go
func tenantDBPath(basePath string, tenantID uint16) string {
    // tenant 0 (unscoped): basePath unchanged (e.g. data/xolu.db)
    // tenant N: <dir>/sql/tXXXX/<base>  (e.g. data/sql/t0001/xolu.db)
    // Mirrors the timeseries layout: data/ts/tXXXX/
    dir := filepath.Dir(basePath)
    base := filepath.Base(basePath)
    return filepath.Join(dir, "sql", tenant.StorageDirSegment(tenantID), base)
}
```

Update `storeForTenant` to pass the derived path when `SQLitePerFileTenants`
is true:

```go
dbPath := baseCfg.DBPath
if baseCfg.SQLitePerFileTenants {
    dbPath = tenantDBPath(baseCfg.DBPath, tenantID)
    if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
        return nil, fmt.Errorf("storeForTenant mkdir: %w", err)
    }
}
store, err := storage.NewStoreFromConfig(storage.StoreConfig{
    ...
    DBPath:               dbPath,
    SQLitePerFileTenants: true,
    TenantID:             tenantID, // retained for graph edge table naming only
    ...
})
```

Note: `TenantID` is still passed even in per-file mode because
`GraphEdgesTableName(tenantID)` uses it to name the graph edge table
(`graph_t0001` etc.). This is intentional — the graph edge table naming
is independent of the `tenant_id` column.

The `tenants` table (registry persistence) lives in the base store file
(tenant 0). Per-tenant files do not have or need a `tenants` table. The
`TenantPersister` already operates against the base store; no change needed.

**Agreed filesystem layout** (with `data/` as `BaseDir`, `data/xolu.db` as `DBPath`):

```
data/
  xolu.db              ← tenant 0: entities, tenant registry, schema_version
  ts/
    t0001/            ← tenant 1 timeseries
      db/             ← Pebble LSM (all timelines as key ranges; not per-timeline)
      registry.json   ← timeline definitions
      meta.json       ← event counters
    t0002/
      db/
      registry.json
      meta.json
  sql/                ← only present when SQLitePerFileTenants = true
    t0001/
      xolu.db          ← tenant 1: entities, graph_t0001 edge table
    t0002/
      xolu.db
```

Note: timelines are key-prefix ranges inside a single `db/` instance per
tenant, not directories. The `tXXXX/` directories are tenant boundaries.

---

## Step 7 — OQL layer (`pkg/oql/executor.go`, `sqlgen.go`, `sqlgen_adapted.go`, `sqlgen_aggregate.go`)

The OQL layer currently injects `tenant_id` scoping in two places:

**Push-down path (SQL generators):** `sqlgen.go`, `sqlgen_adapted.go`,
and `sqlgen_aggregate.go` each append `AND tenant_id = ?` when
`tenantID` is non-empty. In per-file mode this is unnecessary — the
store is already scoped. The generators must not emit this clause.

The cleanest approach: the executor checks `IsPerFileTenant()` on the
store before setting `sqlTenantID`:

```go
sqlTenantID := ""
if cfg := store.Config(); cfg.TenantID != 0 {
    if tp, ok := store.(storage.TenantModeProvider); !ok || !tp.IsPerFileTenant() {
        sqlTenantID = fmt.Sprintf("%d", cfg.TenantID)
    }
}
```

When `sqlTenantID` is empty the SQL generators already skip the
`tenant_id` clause — no changes to the generators are needed.

**Go-path (executor):** `filterByTenant` filters records by the
`tenant_id` field in the record map. In per-file mode records have no
`tenant_id` field, so `filterByTenant` returns them all unchanged
(the `if tid, ok := rec["tenant_id"]` guard already handles this
correctly). No change needed.

**INSERT path:** `executeInsert` injects `record["tenant_id"] = tenantID`
into inserted records. In per-file mode this must be skipped — there is
no `tenant_id` column in the target table. Add the same `IsPerFileTenant`
check here.

---

## Step 8 — Factory (`pkg/storage/factory.go`)

Pass `SQLitePerFileTenants` from `StoreConfig` to `SQLiteConfig`:

```go
return NewSQLiteStore(cfg.DBPath, SQLiteConfig{
    ...
    PerFileTenants: cfg.SQLitePerFileTenants,
    ...
})
```

---

## Step 9 — Tests

### Unit tests

Add tests to `pkg/storage/sqlite_test.go` (or a new
`sqlite_per_file_test.go`):

- Schema created without `tenant_id` column when `PerFileTenants = true`.
- `Create`/`Get`/`Update`/`Delete` round-trip in per-file mode.
- Two stores for different tenants opened at different paths do not
  interfere with each other.
- `CountEntities` and `QueryWithPlan` produce correct results without
  `tenant_id` filtering.
- Adapted table DDL in per-file mode omits `tenant_id`.

### OQL integration tests

Add to `pkg/oql/executor_test.go` or a new file:

- Push-down query against a per-file store does not include
  `tenant_id` in generated SQL (assert via golden SQL output).
- `filterByTenant` is a no-op when records have no `tenant_id` field.

### Server integration tests

Add to `pkg/server/`:

- Two tenants each with a `product` entity: in per-file mode, each has
  its own database file; GET from one tenant does not return the other's
  data.
- In shared mode (default), same test passes via `tenant_id` column
  isolation — ensures the flag does not break existing behaviour.

---

## Migration (informational, not part of this implementation)

Migrating an existing shared-schema deployment to per-file mode requires:

1. For each tenant N, export all rows where `tenant_id = N` from the
   shared `entities`, `entity_sequences`, and `entities_fts` tables.
2. Open a new per-file store at the derived path for tenant N.
3. Insert the exported rows (without `tenant_id`).
4. Repeat for adapted tables if any exist.

This is a data migration, not a schema migration. A dedicated
A migration command is the appropriate delivery vehicle. That work
is out of scope for this implementation; the migration path should be
documented before the feature is enabled in any deployment with existing
data.

---

## What does not change

- The timeseries layer — already per-file; no changes.
- The in-memory graph — already prefix-isolated; no changes.
- The tenant registry — lives in the base store (tenant 0); unchanged.
- The `graph_tXXXX` edge tables — already per-tenant-named; unchanged.
- The `storeForTenant` `LoadOrStore` race guard — unchanged.
- The `AdaptivePersister` — deprecated and unused in production; unchanged.
- Backup/restore (`Litestream`) — operates at file level; per-file mode
  is strictly easier (each tenant file can be backed up independently).

---

## Sequence summary

```
pkg/config/config.go          Add SQLitePerFileTenants bool + config key
pkg/storage/storage.go        Add SQLitePerFileTenants to StoreConfig; TenantModeProvider interface
pkg/storage/sqlite.go         Add PerFileTenants to SQLiteConfig; IsPerFileTenant(); tenantWhere()/tenantArgs(); branch createSchema; update all 40+ query methods
pkg/storage/sqlite_field_query.go  Update ListWithFields / QueryWithFields
pkg/storage/dialect_sqlite.go Update adapted DDL and query builders
pkg/storage/factory.go        Pass PerFileTenants through
pkg/oql/executor.go           Conditional sqlTenantID; skip tenant injection on INSERT
pkg/server/server.go          tenantDBPath(); storeForTenant path derivation
Tests (storage, oql, server)  Per-file mode correctness and isolation
```

Estimated effort: one week. The `sqlite.go` query method updates are the
bulk of the work; the `tenantWhere()` / `tenantArgs()` helpers make that
mechanical rather than risky. The OQL layer change is small — two lines
in the executor. The config and server wiring is straightforward.
