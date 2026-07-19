# PostgreSQL Backend — Readiness Assessment and Roadmap

**Status:** Pre-implementation planning  
**Author:** Nadine Ostrovski  
**Date:** 2026-06-15  
**Scope:** `pkg/storage`, `pkg/oql`, `pkg/server`, `cmd/iolu`

---

## Background and motivation

Xolu is designed around a per-tenant instance model: each tenant runs as a
separate xolu process backed by its own SQLite file. This is a deliberate
architectural choice, not a compromise. The binary is under 40 MB, memory
footprint per instance is negligible by modern standards, and the model
provides perfect tenant isolation with no noisy-neighbour risk, no shared
connection pool contention, and no cross-tenant schema complexity.

SQLite's write throughput ceiling — approximately 1,500 single-entity
inserts per second and 800 updates per second under the current
implementation — is acceptable for the vast majority of tenants. When a
tenant's workload approaches or exceeds this ceiling, the correct response
is migration to a stronger backend, not a redesign of the product.

This document records the current readiness level for adding a PostgreSQL
backend as a graduation path for high-write-volume tenants, identifies the
specific gaps that must be closed before that backend can be implemented,
and defines a phased roadmap of preparatory work spread across four months.

The goal is to arrive at a state where adding the PostgreSQL `Store`
implementation is a matter of filling in well-defined interfaces rather
than untangling structural coupling — so that when the first tenant demands
it, the work is a month, not a quarter.

---

## Strategic position

### SQLite remains the default and primary backend

The per-tenant instance model is the product. PostgreSQL is a graduation
path. Nothing in this roadmap changes the default deployment model or
deprecates the SQLite backend. The two backends will coexist
indefinitely; operators choose per-tenant based on workload.

### The graduation trigger

A tenant is a candidate for PostgreSQL graduation when one or more of the
following is observed:

- Sustained `SQLITE_BUSY` retry frequency above 1% (visible via
  `/metrics` → `contention_lock_engaged` or `busy_retries_total`)
- Mean write latency consistently above 2 ms (indicates checkpoint
  interference or page-cache thrashing)
- Write queue depth growing faster than it drains under peak load
- Explicit client requirement for multi-writer concurrency that xolu's
  single-writer WAL model cannot satisfy

### What PostgreSQL buys

| Concern | SQLite | PostgreSQL |
|---------|--------|-----------|
| Single-entity write latency | ~700 µs | ~75 µs |
| Concurrent writers | 1 (serialised) | Many |
| Bulk write (`/commit`) | One tx, efficient | One tx, ~10× faster |
| Read scaling | Single file, N readers | Read replicas, PgBouncer |
| Decimal arithmetic | Scaled int64 in Go | Native `NUMERIC(p,s)` |
| JSON column | `json_extract()` | `jsonb` with GIN indexing |
| Operational dependency | None (embedded) | Running PostgreSQL instance |

### What PostgreSQL does not change

Graph traversal is entirely in-memory (FlatGraph) and is independent of
the storage backend. Graph performance is identical regardless of which
backend stores the entity data. Timeseries storage (Pebble-backed) is
also independent.

---

## Current readiness assessment

### What is already correct

**The `Store` interface** (`pkg/storage/storage.go`) is backend-neutral.
All CRUD, batch, search, and lifecycle operations are defined in terms of
`context.Context`, `map[string]interface{}`, and xolu error sentinels. A
PostgreSQL implementation satisfies this interface without changing any
layer above storage.

**The optional interface pattern** is well-established.
`AggregateQueryable`, `PagedLister`, `GraphEdgeScanner`,
`EdgePropertyStore`, `EdgeSchemaStore`, `GraphIntegrity`, and others
are declared as separate interfaces that the server checks via type
assertion before use. PostgreSQL can implement the full set
incrementally; the server handles absence gracefully.

**The `StorageDialect` interface** (`pkg/storage/dialect.go`) is
comprehensive and explicitly anticipates PostgreSQL. Every method has
documented PostgreSQL equivalents in its comments:

```
// Placeholder(n int) string
//   SQLite: "?"
//   PostgreSQL: "$1", "$2", ...
//
// ColumnType("integer", "") → "INTEGER" (SQLite) / "BIGINT" (PostgreSQL)
// ColumnType("number", "decimal", p, s) → scaled int64 (SQLite) / "NUMERIC(p,s)" (PostgreSQL)
// ColumnType("boolean", "") → "INTEGER" (SQLite) / "BOOLEAN" (PostgreSQL)
// ColumnType("object", "") → "TEXT" (SQLite) / "JSONB" (PostgreSQL)
```

**The OQL `SQLDialect` interface** (`pkg/oql/sqlgen.go`) separates query
generation from the storage layer and documents PostgreSQL behaviour:

```
// Placeholder(n int) string
//   SQLite: "?"
//   PostgreSQL: "$N"
//
// LimitClause: "LIMIT ?" (SQLite, PostgreSQL)
//   vs "TOP N ..." (T-SQL)
```

**The factory** (`pkg/storage/factory.go`) uses a registry pattern.
Adding a PostgreSQL backend is `RegisterStore("postgres", ...)` in an
`init()` call. No changes to the factory itself.

**The tenant table naming** (`pkg/tenant`) produces names of the form
`t<XXXX>_nodes`, `t<XXXX>_ndata_<entity>`, `t<XXXX>_graph`, etc. These
are valid PostgreSQL identifiers. No renaming needed.

**`RETURNING` in the sequence upsert** is used identically in SQLite
and PostgreSQL. The `INSERT ... ON CONFLICT DO UPDATE ... RETURNING next_id`
pattern in `createInner` is standard SQL supported by both databases.

---

### Gaps — specific and bounded

The following is an exhaustive list of what must change. Nothing requires
an architectural redesign. Every item is a targeted fix to a specific file.

#### Gap 1 — Server type-asserts to `*SQLiteStore` in 12 places

`server.go` (6) and `handlers.go` (6) cast the `Store` to
`*storage.SQLiteStore` to access methods not declared on the `Store`
interface. The capabilities needed:

| Capability | Used for | Current |
|-----------|----------|---------|
| `DB()` | `PRAGMA wal_checkpoint(TRUNCATE)` in export handler | `handlers.go:1577` |
| `DB()` | `PRAGMA wal_checkpoint(FULL)` before graph rebuild | `server.go:1813` |
| `DB()` | Raw file copy in backup export | `handlers.go` |
| `ContentionLock()` | Contention metrics | `server.go:219` |
| `RegisterAdaptedEntity()` | Schema registration on POST /schema | `server.go:496` |
| `RegisterAdaptedEdge()` | Edge schema registration | `server.go:520` |
| Various | Graph verify/rebuild internal paths | `server.go:1230`, `server.go:1812`, `server.go:1835` |

Each must become an optional interface declared in `pkg/storage/storage.go`.

#### Gap 2 — `sqlite_master` table existence checks in 4 locations

```
pkg/storage/adapted_crud.go
pkg/storage/adapted_edge.go
pkg/storage/edge_fts.go
pkg/storage/edge_schema.go
```

All four use:
```sql
SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?
```

PostgreSQL equivalent is `information_schema.tables`. This must be
abstracted as a `TableExistsSQL(tableName string) string` method on
`StorageDialect`, or replaced with `CREATE TABLE IF NOT EXISTS` which
both databases support.

#### Gap 3 — OQL executor hard-wires `SQLiteDialect`

`pkg/oql/executor.go` line 57:
```go
d := &SQLiteDialect{}
```

This is selected whenever the store implements `storage.Queryable`, with
no check for which backend is in use. A PostgreSQL store also implements
`Queryable`, so it would get the wrong dialect. The dialect must be
selected by backend name via `store.Config().Type` or a new
`DialectName() string` method on the `Store` interface.

#### Gap 4 — `StoreConfig` embeds SQLite-specific tuning fields

```go
type StoreConfig struct {
    Type   string
    DBPath string   // named as a file path, not a DSN
    SQLiteCacheSize           int
    SQLiteBusyTimeout         int
    SQLiteMaxOpenConns        int
    SQLiteMaxIdleConns        int
    SQLiteReadPoolSize        int
    SQLiteContentionThreshold int
    SQLitePerFileTenants      bool
}
```

`DBPath` is misleading for a PostgreSQL connection string
(`postgres://user:pass@host/db`). The `SQLiteXxx` fields are
meaningless for any other backend and pollute the shared config struct.

#### Gap 5 — `iolu` bypasses the `Store` interface

`cmd/iolu/main.go` calls `storage.NewSQLiteStore` directly and issues
`SELECT name FROM sqlite_master ...` queries. It cannot operate against
PostgreSQL. The admin tooling must be ported to use `Store` and the
`EntityLister` / `TenantIDLister` optional interfaces, both of which
`SQLiteStore` already implements.

#### Gap 6 — `PRAGMA wal_checkpoint` issued inline

Two inline `PRAGMA wal_checkpoint` calls (one in `handlers.go` at the
export endpoint, one in `server.go` during graph rebuild) are issued via
`sqlStore.DB().ExecContext(...)`. PostgreSQL has no equivalent pragma.
These must be wrapped in an optional `Checkpointer` interface so the
server calls it without knowing the backend.

#### Gap 7 — `BackupProvider` does not exist as an interface

The export endpoint copies the SQLite file byte-for-byte using the raw
`DB()` connection. PostgreSQL backup requires `pg_dump` or the logical
replication protocol. This must become a `BackupProvider` optional
interface with a `BackupTo(ctx, destPath)` method. PostgreSQL can
implement it using `pg_dump` subprocess; the export handler calls the
interface rather than the raw connection.

---

## Roadmap

### Month 1 — Interface cleanup in the server and storage layers

**Week 1–2: Declare the missing optional interfaces**

Add to `pkg/storage/storage.go`:

```go
// SchemaRegistrar is implemented by backends that support adapted table
// registration at runtime. The server calls these methods when a JSON
// Schema is POSTed.
type SchemaRegistrar interface {
    RegisterAdaptedEntity(ctx context.Context, entity string, schema map[string]interface{}) error
    RegisterAdaptedEdge(ctx context.Context, rel string, schema map[string]interface{}) error
}

// Checkpointer is implemented by backends that support an explicit
// checkpoint or flush operation. For SQLite this is PRAGMA wal_checkpoint;
// for PostgreSQL it is a no-op or pg_checkpoint() depending on privileges.
type Checkpointer interface {
    Checkpoint(ctx context.Context, mode string) error
}

// BackupProvider is implemented by backends that can produce a portable
// backup of their data. SQLite copies the file; PostgreSQL invokes pg_dump.
type BackupProvider interface {
    BackupTo(ctx context.Context, destPath string) error
}
```

Implement all three on `SQLiteStore`. Add `Checkpoint` to `SQLiteStore`
wrapping the existing `PRAGMA wal_checkpoint` call. Add `BackupTo`
wrapping the existing file-copy logic.

**Week 2–3: Replace all 12 type assertions in server.go and handlers.go**

Each `if sqlStore, ok := store.(*storage.SQLiteStore); ok { ... }` becomes
a check against the appropriate optional interface:

```go
// Before
if sqlStore, ok := store.(*storage.SQLiteStore); ok {
    sqlStore.DB().ExecContext(ctx, "PRAGMA wal_checkpoint(FULL)")
}

// After
if cp, ok := store.(storage.Checkpointer); ok {
    cp.Checkpoint(ctx, "FULL")
}
```

After this step, `pkg/server` has zero imports of `*storage.SQLiteStore`.
This is the readiness gate for any subsequent backend work.

**Week 3–4: Abstract `sqlite_master` table-existence checks**

Add `TableExistsSQL(tableName string) string` to `StorageDialect`.

SQLite implementation:
```go
func (d *SQLiteStorageDialect) TableExistsSQL(tableName string) string {
    return "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?"
}
```

PostgreSQL implementation (written now, used later):
```go
func (d *PostgreSQLStorageDialect) TableExistsSQL(tableName string) string {
    return "SELECT COUNT(*) FROM information_schema.tables WHERE table_name=$1"
}
```

Replace all four inline `sqlite_master` queries in `adapted_crud.go`,
`adapted_edge.go`, `edge_fts.go`, and `edge_schema.go` with
`dialect.TableExistsSQL(tableName)`.

**Week 4: Rename `StoreConfig.DBPath` to `DSN`**

Rename the field, update `factory.go`, `NewStoreFromConfig`, all call
sites, documentation, and env var handling (`XOLU_DB_PATH`, `XOLU_SQLITE_PATH`
both alias to `DSN`). The SQLite factory interprets DSN as a file path;
the PostgreSQL factory passes it to `pgxpool.ParseConfig`. Document the
PostgreSQL DSN format in MANUAL.md.

Deliverables at end of Month 1:

- Zero `*storage.SQLiteStore` type assertions outside `pkg/storage` itself
- `sqlite_master` removed from all non-SQLite-specific code
- `SchemaRegistrar`, `Checkpointer`, `BackupProvider` interfaces declared
  and implemented by `SQLiteStore`
- `StoreConfig.DSN` replaces `StoreConfig.DBPath`

---

### Month 2 — OQL dialect abstraction and PostgreSQL dialect implementation

**Week 1–2: Fix the OQL executor dialect wiring**

Replace the hard-wired `&SQLiteDialect{}` in `pkg/oql/executor.go` with
dialect selection by backend name:

```go
func selectDialect(store storage.Store) SQLDialect {
    switch store.Config().Type {
    case "postgres":
        return newPostgreSQLDialect(store)
    default: // "sqlite" and anything unrecognised
        d := &SQLiteDialect{}
        if tn, ok := store.(storage.TableNamer); ok {
            d.NodesTable = tn.NodesTable()
        }
        return d
    }
}
```

**Week 2–3: Write `PostgreSQLDialect` in `pkg/oql`**

The OQL `PostgreSQLDialect` replaces:

| SQLite | PostgreSQL |
|--------|-----------|
| `json_extract(data, '$.field')` | `data->>'field'` |
| `json_extract(data, '$.field')` cast to REAL | `(data->>'field')::double precision` |
| `?` placeholders | `$1`, `$2`, ... |
| `CAST(... AS REAL)` | `CAST(... AS DOUBLE PRECISION)` |
| `LENGTH(col)` | `LENGTH(col)` (identical) |

Write unit tests for `PostgreSQLDialect` using the existing OQL test
harness. No running PostgreSQL needed — the dialect methods are pure
functions. Add the dialect to the test matrix alongside `SQLiteDialect`.

**Week 3–4: Write `PostgreSQLStorageDialect` in `pkg/storage`**

The storage-layer `PostgreSQLStorageDialect` implements `StorageDialect`:

| Method | PostgreSQL implementation |
|--------|--------------------------|
| `Name()` | `"postgres"` |
| `Placeholder(n)` | `fmt.Sprintf("$%d", n)` |
| `ColumnType("integer", ...)` | `"BIGINT"` |
| `ColumnType("number", "decimal", p, s)` | `fmt.Sprintf("NUMERIC(%d,%d)", p, s)` |
| `ColumnType("boolean", ...)` | `"BOOLEAN"` |
| `ColumnType("object"/"array", ...)` | `"JSONB"` |
| `NormaliseDecimal(value, p, s)` | Return value unchanged (PostgreSQL handles it) |
| `DenormaliseDecimal(value, p, s)` | Return value unchanged |
| `SupportsNativeDecimalAggregation()` | `true` |
| `CreateTableSQL(spec)` | PostgreSQL DDL with `BIGINT`, `BOOLEAN`, `JSONB` |
| `TableExistsSQL(name)` | `information_schema.tables` query |

Write unit tests for all dialect methods. Add the dialect to the
`StorageDialect` test matrix alongside `SQLiteStorageDialect`.

**Week 4: Split `StoreConfig` backend-specific fields**

Introduce backend-specific option structs and deprecate the flat
`SQLiteXxx` fields:

```go
type SQLiteOptions struct {
    CacheSize           int
    BusyTimeout         int
    MaxOpenConns        int
    MaxIdleConns        int
    ReadPoolSize        int
    ContentionThreshold int
    PerFileTenants      bool
}

type PostgresOptions struct {
    MaxConns        int // pgxpool max_conns
    MinConns        int // pgxpool min_conns
    ConnectTimeout  int // seconds
    IdleTimeout     int // seconds
    MaxConnLifetime int // seconds
}

type StoreConfig struct {
    Type    string
    DSN     string
    FullTextEnabled bool
    GraphEnabled    bool
    TenantID        uint16
    SQLite   *SQLiteOptions   // nil for non-SQLite backends
    Postgres *PostgresOptions // nil for non-PostgreSQL backends
}
```

Deliverables at end of Month 2:

- `pkg/oql` has zero hard-wired dialect references; dialect is selected by
  backend name
- `PostgreSQLDialect` (OQL) fully implemented and tested
- `PostgreSQLStorageDialect` (storage) fully implemented and tested
- `StoreConfig` split into backend-neutral core + per-backend option structs

---

### Month 3 — Admin tooling and contract test harness

**Week 1–2: Port `iolu` to use the `Store` interface**

Replace the direct `storage.NewSQLiteStore` call with
`storage.NewStoreFromConfig`. Replace the two `sqlite_master` introspection
queries in `cmd/iolu/main.go` with calls through `EntityLister` and
`TenantIDLister` optional interfaces:

```go
// Before (sqlite_master query)
rows, err := db.QueryContext(ctx,
    `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 't%\_nodes' ...`)

// After (interface)
if lister, ok := store.(storage.EntityLister); ok {
    entities, err := lister.ListEntities(ctx)
}
```

After this step, `iolu` has zero SQLite-specific code. A PostgreSQL store
that implements `EntityLister` and `TenantIDLister` works automatically.

**Week 2–3: Extract the storage contract test suite**

The existing storage tests in `pkg/storage/*_test.go` already test the
`Store` interface behaviour exhaustively. Extract the core assertions into
a shared `StorageContractSuite` that can be run against any `Store`
implementation:

```go
// pkg/storage/testkit/contract_test.go
func RunContractSuite(t *testing.T, store storage.Store) {
    t.Run("Create", func(t *testing.T) { ... })
    t.Run("Get", func(t *testing.T) { ... })
    t.Run("Update", func(t *testing.T) { ... })
    t.Run("Patch", func(t *testing.T) { ... })
    t.Run("Delete", func(t *testing.T) { ... })
    t.Run("Commit", func(t *testing.T) { ... })
    t.Run("List", func(t *testing.T) { ... })
    t.Run("GetMany", func(t *testing.T) { ... })
    t.Run("ListPaged", func(t *testing.T) { ... })
    t.Run("CountEntities", func(t *testing.T) { ... })
    t.Run("Ping", func(t *testing.T) { ... })
    // ...
}
```

Run the suite against `SQLiteStore` as a regression gate. When the
PostgreSQL implementation exists, the same suite runs against it with
no additional test code. Behaviour differences (e.g. decimal precision,
JSON serialisation edge cases) become explicit exceptions in the suite
rather than silent divergences.

**Week 3–4: Document the PostgreSQL deployment model**

Write `docs/POSTGRES_DEPLOYMENT.md` covering:
- When to graduate a tenant (trigger criteria from this document)
- DSN configuration format and required PostgreSQL version (14+)
- Schema initialisation (xolu creates its own tables on first run)
- The `iolu migrate-tenant` command (to be written alongside the backend)
- Connection pool sizing guidance for `PostgresOptions`
- Monitoring: which metrics change meaning on a PostgreSQL backend

Deliverables at end of Month 3:

- `iolu` uses the `Store` interface exclusively; zero SQLite-specific code
- `StorageContractSuite` extracted and passing against `SQLiteStore`
- PostgreSQL deployment documentation written (against the planned interface)

---

### Month 4 — Minimal PostgreSQL `Store` implementation

**Week 1–2: Implement the core `Store` for PostgreSQL**

Use `pgx/v5` directly (not `database/sql`) for the PostgreSQL
implementation. `pgx` has its own connection pool (`pgxpool`), its own
prepared statement cache, and its own type system. It is measurably faster
than `database/sql + lib/pq` for PostgreSQL because it avoids the
reflection-heavy `database/sql` value scanning and maps Go types to
PostgreSQL wire types directly.

**Pipeline mode for `/commit`**

`pgx/v5` supports pipeline mode, which is a first-class design requirement
for the PostgreSQL `Commit` implementation — not an optimisation to
consider later.

In standard mode, each query sent to PostgreSQL requires a full round-trip:
send query → wait for response → send next query. For a `/commit` with N
entities, that is at minimum N + 2 round-trips (one per insert, plus
`BEGIN` and `COMMIT`).

In pipeline mode, `pgx` sends multiple queries over a single TCP
connection without waiting for each response before sending the next. The
server processes them in order and returns all responses together. For the
`commitInner` path, this means `BEGIN`, N `INSERT` statements, and
`COMMIT` can be sent in a single pipeline flush — effectively 1 round-trip
regardless of N.

The `Commit` implementation must use `pgx.Conn.SendBatch` (or the
higher-level `pgxpool.Pool.SendBatch`) for the append loop:

```go
func (s *PostgreSQLStore) commitInner(ctx context.Context, req CommitRequest) (CommitResult, error) {
    return s.pool.BeginTxFunc(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
        // Upsert (single round-trip)
        if err := s.saveInTx(ctx, tx, req.Update); err != nil {
            return err
        }

        // Batch inserts via pipeline — one flush for all N entities
        batch := &pgx.Batch{}
        for _, a := range req.Append {
            sql, args := s.insertSQL(a)
            batch.Queue(sql, args...)
        }
        results := tx.SendBatch(ctx, batch)
        defer results.Close()

        for range req.Append {
            if _, err := results.Exec(); err != nil {
                return err
            }
        }
        return nil
    })
}
```

This is the primary reason the write latency gap between PostgreSQL and
SQLite narrows further on bulk operations than on single-entity operations.
A `/commit` with 100 entities costs 1 pipeline round-trip on PostgreSQL
regardless of N; on SQLite it costs N sequential `database/sql` call
sequences inside one transaction.

**A note on the SQLite driver and `database/sql`**

The SQLite path cannot avoid `database/sql`. The current driver,
`modernc.org/sqlite`, is a `database/sql` driver, and `database/sql`'s
per-call overhead (connection acquisition, value marshalling via
reflection, response scanning) contributes a fixed ~10–20 µs per
statement regardless of what the database does. This is a structural cost
of using `database/sql` with an embedded database.

The alternative, `mattn/go-sqlite3`, is faster for write-heavy workloads
because it is a thin CGo binding to the SQLite C library with less
marshalling overhead — but it introduces a CGo build dependency, requires
a C compiler, and breaks the pure-Go binary model. The empirical
performance difference between the two drivers is characterised in
[`docs/SQLITE_DRIVER_BENCHMARK.md`](#sqlite-driver-benchmark) (generated
separately). Whether that difference justifies introducing a CGo
dependency is a product decision, not a technical one.

Schema initialisation on first connect: xolu creates its own tables using
`CREATE TABLE IF NOT EXISTS` with the `PostgreSQLStorageDialect` DDL.
No external migration tool is required for initial setup.

Implement in order:

1. `Ping` — simplest, establishes that the connection works
2. `Create` / `Get` / `Update` / `Patch` / `Delete` — core CRUD
3. `List` / `GetMany` / `ListPaged` / `CountEntities` — read paths
4. `Save` / `Commit` — batch and upsert paths
5. `Close` — connection pool teardown

Do not implement adapted tables, FTS, or graph edges in this phase.
`IsAdaptedEntity` returns false. `FullTextSearch` returns empty.
`ScanGraphEdges` returns an error with `ErrNotSupported`. The goal is a
`Store` that passes the contract test suite for the core CRUD surface.

**Week 3: Register and wire the backend**

Register in `init()`:

```go
func init() {
    storage.RegisterStore("postgres", func(config map[string]interface{}) (storage.Store, error) {
        dsn, _ := config["dsn"].(string)
        return NewPostgreSQLStore(dsn, PostgreSQLConfig{...})
    })
}
```

Wire in `config.go`: add `XOLU_PG_DSN` and `XOLU_STORAGE_TYPE=postgres`.
Update `server.go` to call `storage.NewStore("postgres", ...)` when the
type is `"postgres"`.

**Week 4: Integration test against a real PostgreSQL instance**

Run the `StorageContractSuite` against the PostgreSQL backend using a
Docker-based PostgreSQL instance in CI:

```yaml
# .github/workflows/postgres.yml
services:
  postgres:
    image: postgres:16-alpine
    env:
      POSTGRES_PASSWORD: test
    ports:
      - 5432:5432
```

Any contract test failures against PostgreSQL that pass against SQLite are
bugs in the PostgreSQL implementation, not in the test suite. Fix them
before considering this phase complete.

Deliverables at end of Month 4:

- `PostgreSQLStore` implementing core `Store` interface
- Passes `StorageContractSuite` against PostgreSQL 14, 15, 16
- Registered in the factory; selectable via `XOLU_STORAGE_TYPE=postgres`
- `iolu` can connect to and inspect a PostgreSQL-backed xolu instance
- No adapted tables, FTS, or graph edges yet — these are Phase 2

---

## What is explicitly deferred

The following PostgreSQL capabilities are not part of this roadmap. They
are Phase 2 work, each independently implementable once the core backend
is stable:

**Adapted tables on PostgreSQL.** The `PostgreSQLStorageDialect` DDL
methods are written in Month 2, but the adapted table CRUD paths
(`adaptedCreate`, `adaptedList`, etc.) require additional implementation
work. PostgreSQL adapted tables will use `NUMERIC(p,s)` for decimals and
`JSONB` for the `_extra` overflow field, both strictly better than the
SQLite equivalents.

**Full-text search on PostgreSQL.** FTS5 is SQLite-specific. PostgreSQL
uses `tsvector` / `tsquery` with GIN indexing. The FTS interface methods
(`FullTextSearch`, `IndexEdgeContent`, `SearchEdges`) need a
PostgreSQL-specific implementation.

**Graph edge tables on PostgreSQL.** The `t<XXXX>_graph` topology table
translates directly to PostgreSQL. `ScanGraphEdges` and `AddEdgeWithProps`
need PostgreSQL implementations. The table structure is identical.

**PostgreSQL sequences for ID generation.** The current `ON CONFLICT DO
UPDATE ... RETURNING next_id` sequence table approach works on PostgreSQL
but is not optimal. A native `CREATE SEQUENCE` with `nextval()` is faster
and avoids lock contention under concurrent inserts. This is an
optimisation for after the core backend is stable.

**Live tenant migration tooling.** Moving a tenant from SQLite to
PostgreSQL without downtime requires export, import, and cutover tooling.
This is a separate project with its own design requirements.

---

## Risk register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| `pgx/v5` API incompatibility | Low | Medium | Pin minor version; test in CI |
| PostgreSQL JSON semantics differ from SQLite | Medium | Medium | Contract test suite catches divergences |
| OQL push-down generates incorrect PostgreSQL SQL | Medium | High | OQL dialect unit tests run without a DB |
| Decimal precision differences between backends | Low | High | `SupportsNativeDecimalAggregation()` flag; Go-side fallback |
| Migration tooling lag — backend ready but no upgrade path | Medium | Low | Document manual `iolu export` + reimport as interim |
| Tests pass but production workload exposes edge cases | Low | High | Run `olubench` against PostgreSQL backend before any tenant migration |

---

## Files that will change or be created

### New files

| File | Purpose |
|------|---------|
| `pkg/storage/dialect_postgres.go` | `PostgreSQLStorageDialect` implementation |
| `pkg/storage/postgres.go` | `PostgreSQLStore` implementing `Store` |
| `pkg/storage/postgres_config.go` | `PostgreSQLConfig` and `PostgresOptions` |
| `pkg/oql/sqlgen_postgres.go` | `PostgreSQLDialect` implementing `SQLDialect` |
| `pkg/storage/testkit/contract.go` | `StorageContractSuite` shared test harness |
| `docs/POSTGRES_DEPLOYMENT.md` | Operator guide for PostgreSQL-backed deployments |
| `docs/SQLITE_DRIVER_BENCHMARK.md` | Empirical comparison of `modernc.org/sqlite` vs `mattn/go-sqlite3` |

### Files that change

| File | Change |
|------|--------|
| `pkg/storage/storage.go` | Add `SchemaRegistrar`, `Checkpointer`, `BackupProvider`; rename `DBPath` → `DSN`; split `SQLiteXxx` fields |
| `pkg/storage/dialect.go` | Add `TableExistsSQL(tableName string) string` to `StorageDialect` |
| `pkg/storage/dialect_sqlite.go` | Implement `TableExistsSQL`; implement `Checkpoint`; implement `BackupTo` |
| `pkg/storage/adapted_crud.go` | Replace `sqlite_master` query with `dialect.TableExistsSQL()` |
| `pkg/storage/adapted_edge.go` | Same |
| `pkg/storage/edge_fts.go` | Same |
| `pkg/storage/edge_schema.go` | Same |
| `pkg/storage/factory.go` | Register `"postgres"`; update `NewStoreFromConfig` |
| `pkg/oql/executor.go` | Replace hard-wired `&SQLiteDialect{}` with dialect selection by backend name |
| `pkg/server/server.go` | Replace 6 `*SQLiteStore` type assertions with optional interface checks |
| `pkg/server/handlers.go` | Replace 6 `*SQLiteStore` type assertions with optional interface checks |
| `cmd/iolu/main.go` | Replace `NewSQLiteStore` + `sqlite_master` queries with `Store` interface |
| `pkg/config/config.go` | Add `XOLU_PG_DSN`, `XOLU_STORAGE_TYPE`; rename `XOLU_DB_PATH` → `XOLU_DSN` (alias retained) |
| `MANUAL.md` | Document PostgreSQL configuration |

---

## Success criteria for Phase 1 completion

Phase 1 (this roadmap) is complete when all of the following hold:

1. `pkg/server` and `pkg/server/handlers.go` contain zero references to
   `*storage.SQLiteStore` outside of `_ = (*storage.SQLiteStore)(nil)`
   compile-time interface checks.

2. `pkg/storage/adapted_crud.go`, `adapted_edge.go`, `edge_fts.go`, and
   `edge_schema.go` contain zero references to `sqlite_master`.

3. `pkg/oql/executor.go` contains zero references to `SQLiteDialect`.

4. `cmd/iolu/main.go` contains zero references to
   `storage.NewSQLiteStore` or `*storage.SQLiteStore`.

5. `StorageContractSuite` passes against `SQLiteStore` with no
   regressions from the pre-roadmap baseline.

6. `PostgreSQLStore` passes `StorageContractSuite` against PostgreSQL 14,
   15, and 16 in CI.

7. A tenant can be configured to use PostgreSQL via
   `XOLU_STORAGE_TYPE=postgres XOLU_DSN=postgres://...` with no code
   changes.
