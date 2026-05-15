# Changelog

All notable changes to olu are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.9.7-patched100] - 2026-05-14

### Restore — iolu commands lost between patched73 and patched99

Four command groups were present in the vendored xolu at patched73 but
absent from the current iolu. All are restored and forward-ported to
patched99's codebase. One case-sensitivity bug is fixed in the process.

**Restored commands:**

`iolu db init --db <path> [--tenant name[:id]] [--graph] [--ts-dir <path>]`
Creates a new olu SQLite database with the full shared-mode schema, optional
graph edge tables, and optional timeseries directory provisioning. Accepts
repeatable `--tenant` flags to pre-register tenants in one step (required
for strict-mode deployments).

`iolu db status --db <path> [--base-dir <path>]`
Reports database file size, schema versions, per-table row counts, graph
edge table listing, tenant registry, and timeseries directory status.

`iolu db upgrade --db <path>`
Applies pending schema migrations idempotently. Currently handles v2
(schema_version seeding) and v3 (_version column). Safe to run against
a patched99 database — both migrations report "already applied".

`iolu tenant provision-ts --db <path> --name <name> --ts-dir <path>`
Creates the per-tenant timeseries directory for a named tenant.

**Bug fix — timeseries directory case:**

The vendored iolu used lowercase `t%04x` for timeseries directory names
(`t000a`, `t00ff`). `tenant.StorageDirSegment` uses uppercase `t%04X`
(`t000A`, `t00FF`). The mismatch only affects tenants with IDs ≥ 10
(where hex letters appear). All three affected call sites are corrected:
`provisionTSDir`, `cmdTenantInfo`, and the `db init` `--ts-dir` path.
The `db status` timeseries check was not affected (it scans the directory
rather than constructing a path).

**Also restored:** `tenantFlags` repeatable flag type, `registerTenant`,
`createGraphTable`, `initSchema`, `ensureCoreTables`, `applyMigration`,
`createDB`, `tableCount`, `tenantEntityCount` helper functions.

All 2484 tests pass. iolu binary smoke-tested against a live SQLite database.



### Change — iolu included in standard Docker image

`Dockerfile` now builds and ships `iolu` alongside `olu` in the runtime
stage. Both binaries are available in `ghcr.io/ha1tch/xolu:latest`:

- `/usr/local/bin/olu`  — the olu server (unchanged)
- `/usr/local/bin/iolu` — the iolu administrative CLI

This allows downstream projects (nolu and others) that use iolu as a
Docker init container to simply override the entrypoint of the runtime
image rather than maintaining a separate build context or vendored source
tree.

No code changes — build and tests unaffected.



### Tests — Stronger per-file tenant test coverage (7 new tests, 4 OQL variants)

Addresses the coverage gaps identified after patched97. All tests target
code paths that were changed during the per-file implementation but were
either untested or tested only on the happy path.

**`pkg/oql/per_file_pushdown_test.go`** — 4 tests:

`TestGenerateSQL_PushWhere_PerFileNoTenantID`: the golden SQL test the spec
required. Calls `GenerateSQL` with `PushWhere` and a real WHERE predicate,
asserts `tenant_id` is absent from the SQL string AND that `len(Args) == 2`
(not 3), which would catch a spurious tenant_id arg injected even if not
named in SQL. Paired with `TestGenerateSQL_PushWhere_SharedModeArgCount`
(regression guard: shared mode must still emit 3 args). Two compound-WHERE
variants repeat the same arg-count assertion for multi-predicate queries.

**`pkg/storage/sqlite_per_file_stronger_test.go`** — 7 tests:

`TestPerFile_FTSDeleteIsolation`: per-file equivalent of the existing
`TestSQLiteTenantIsolation_FTSUpdateDelete`. Two stores at separate file
paths — update+delete in store A, assert store B's FTS index is untouched.
Directly covers the `deleteInner` FTS path that caused the patched96 regression.

`TestPerFile_AdaptedCRUDRoundTrip`: exercises all four adapted-table
mutations (Create, Get, Update, Delete, List, Patch) in per-file mode.
`dialectIsPerFile()` in `adapted_crud.go` changes the arg lists for
Update (id only, not tenantID+id), Delete (same), and List (no args). All
branches are now exercised.

`TestPerFile_CommitRoundTrip`: exercises `createInTx` (auto-ID and
explicit-ID append) and `saveInTx` (exists→update and !exists→insert)
paths, covering the sequence-branching and INSERT-branching code in the
Commit transactional batch.

`TestPerFile_SequencePrimaryKeyConstraint`: raw SQL verification of the
`PRIMARY KEY (entity_type, id)` semantics — same (type, id) fails; same
id different type succeeds; also confirms `entity_sequences` has no
`tenant_id` column via a raw insert.

`TestPerFile_GraphIntegrityAndRebuild`: verifies `VerifyGraphIntegrity`
and `RebuildGraph` in per-file mode. Creates REF relationships, deletes
the edge table manually, rebuilds, asserts edge count. The entity scan in
both methods uses `WHERE 1=1` (via `tenantWhere()`) in per-file mode —
this test would catch a broken scan silently returning zero edges. Also
checks cross-contamination: a second store at a different path has zero edges.

`TestPerFile_AdaptedHasExtra`: verifies the `_extra` overflow column path
in adapted tables (extra fields beyond the schema) works in per-file mode,
and confirms no `tenant_id` column in the resulting table.

**`pkg/server/per_file_tenant_test.go`** (appended) — 1 test:

`TestPerFileTenant_MutationsAndCrossTenantIsolation`: HTTP-level PUT, PATCH,
DELETE in per-file mode. After DELETE in tenant mu1, tenant mu2's entity at
the same ID is still accessible — the key cross-tenant isolation assertion
at the HTTP level, covering the `deleteInner` path end-to-end.

All 2490+ tests pass.



### Fix + Tests — SQLite per-file tenant isolation complete

Fixes two bugs from patched96 and adds Phase 4 tests from the spec.

**Bug fixes:**

- `NewSQLiteStore` was not copying `PerFileTenants` into `storeConfig`, so
  `Config().SQLitePerFileTenants` was always `false`. This meant
  `storeForTenant` in `server.go` never derived the per-tenant file path.
  Fixed by adding `SQLitePerFileTenants: config.PerFileTenants` to the
  `storeConfig` literal.

- `deleteInner` FTS delete was dropping the `tenant_id` filter in
  shared mode (regression from patched96 patch). Fixed with proper
  `PerFileTenants` branch in `deleteInner`.

**New tests (Phase 4):**

`pkg/storage/sqlite_per_file_test.go` — 9 tests covering: schema DDL
without `tenant_id` column, CRUD round-trip, two-store isolation
(independent sequences, invisible cross-tenant), `CountEntities`,
`QueryWithPlan`, `IsPerFileTenant`, adapted table DDL, FTS, and Save.

`pkg/oql/per_file_test.go` — 4 tests covering: `GenerateSQL` with empty
`sqlTenantID` emits no `tenant_id` clause; `GenerateSQL` with non-empty
tenant ID still emits the clause (shared-mode regression guard);
`ExecuteWithStore` against per-file store succeeds without `tenant_id`
column errors; two-store OQL isolation.

`pkg/server/per_file_tenant_test.go` — 2 integration tests: per-file
mode creates separate database files per tenant and GET from one tenant
returns only its own data; shared mode (default) maintains `tenant_id`
column isolation and does not create a `sql/` directory.

All 2490+ tests pass.



### Feature — SQLite per-file tenant isolation (`OLU_SQLITE_PER_FILE_TENANTS`)

Implements the full `SQLITE_PER_FILE_TENANTS.md` spec. When
`OLU_SQLITE_PER_FILE_TENANTS=true`, each tenant gets its own SQLite database
file and the `tenant_id` column is absent from the schema entirely — the file
itself is the isolation boundary.

**Files changed:** `pkg/config/config.go`, `pkg/storage/storage.go`,
`pkg/storage/factory.go`, `pkg/storage/sqlite.go` (schema split, helpers,
all query methods), `pkg/storage/sqlite_field_query.go`,
`pkg/storage/dialect_sqlite.go`, `pkg/storage/adapted_crud.go`,
`pkg/oql/executor.go`, `pkg/server/server.go`, `cmd/olu/main.go`.

Key design points:
- `tenantWhere()` / `tenantArgs()` helpers make query method changes
  mechanical — no SQL string is duplicated.
- `createSchemaShared()` / `createSchemaPerFile()` implement both DDL paths.
- `SQLiteStorageDialect.PerFileTenants` gates adapted table DDL/queries.
- `dialectIsPerFile()` type assertion gates adapted CRUD arg lists.
- `OQL executor`: `sqlTenantID` is suppressed when `IsPerFileTenant()` is
  true; INSERT no longer injects `tenant_id` into the record map.
- `server.go`: `tenantDBPath()` derives `<dir>/sql/tXXXX/<base>` paths;
  `storeForTenant` creates the directory and passes the derived path.
- Default is `false` — all existing deployments are unaffected.
- All 2360+ tests pass.

## [0.9.7-patched95] - 2026-05-14

### Fix — release.sh cleans all test artifact types from pkg/ before packaging

Some tests write SQLite files and other artifacts relative to their package
directory. `release.sh` now removes all known patterns from every subdirectory
of `pkg/` before zipping, and excludes all the same patterns from the zip.

**Cleaned and excluded:**

| Pattern | What it is |
|---|---|
| `*.db`, `*.db-wal`, `*.db-shm`, `*.db-journal`, `*.db-tmp` | SQLite database + WAL sidecars |
| `*-wal`, `*-shm`, `*-journal` | SQLite sidecars without `.db` base (some drivers) |
| `graph.data`, `graph.index` | FlatGraph persistence files (default from `config.Default()`) |
| `*.golden`, `*.pprof`, `*.prof`, `*.test` | Go test output formats |

The post-zip sanity check was also widened to catch any of these patterns
if they somehow slip through the exclusions.


## [0.9.7-patched94] - 2026-05-14

### Tests — complete BETWEEN coverage across all three execution paths

patched93 added string BETWEEN tests for the Go path and the blob
push-down path. This patch adds the adapted entity paths, completing
the matrix.

| Path | Numeric BETWEEN | String BETWEEN |
|---|---|---|
| Go path | ✓ pre-existing | ✓ added in patched93 |
| Blob push-down (`PushWhere` / `json_extract`) | ✓ pre-existing | ✓ added in patched93 |
| Adapted full push-down (`PushFull` / native columns) | ✓ pre-existing | ✓ added here |
| Adapted aggregate push-down (`PushAggregate`) | ✓ pre-existing | ✓ added here |

New test cases:

`TestFullPD/WhereBetween_string` and `TestFullPD/WhereBetween_string_notbetween`
(`adapted_full_pushdown_test.go`) — `product BETWEEN 'gadget' AND 'gizmo'`
on the adapted `items` entity. The adapted path uses native SQL columns with
no CAST, so the code was correct; the test makes that claim testable.

`TestAdaptedPushDown/WhereBetween_string` and
`TestAdaptedPushDown/WhereNotBetween_string`
(`adapted_pushdown_test.go`) — same query through the Go vs adapted
comparative harness.

All tests are equivalence-style: they run the same query through the Go path
and the push-down path and assert identical result sets.


## [0.9.7-patched93] - 2026-05-14

### Fix — BETWEEN with string bounds silently coerced to REAL, returning wrong results

**Bug:** `sqlgen.go` used `JSONFieldNumeric` (i.e. `CAST(json_extract(data,
'$.field') AS REAL)`) for every BETWEEN expression, regardless of whether the
bounds were numeric or string literals. SQLite's CAST of a date string like
`"2026-02-15T00:00:00Z"` to REAL yields `2026.0` (the leading numeric prefix)
for every value in the same year. The result: any query of the form
`WHERE timestamp BETWEEN '2026-01-01' AND '2026-06-30'` returned zero rows,
with no error message.

The Go evaluation path (`compareValues` in `aggregator.go`) was unaffected —
it tries numeric parsing and falls back to string comparison, so Go-path and
push-down path diverged silently on the same query.

**Root cause:** The comment `// BETWEEN always uses numeric comparison (range
implies ordering)` was wrong. Lexicographic ordering is valid for strings;
ISO-8601 timestamps and zero-padded codes (SENS-0001, SENS-0200) compare
correctly without any cast.

**Fix (`pkg/oql/sqlgen.go`):** Inspect the types of the low and high bound
literals. When both bounds are numeric (`int`, `int64`, `float64`, `float32`),
use `JSONFieldNumeric` as before. When either bound is a string, use
`JSONField` (no CAST). This matches the logic already in `chooseFieldExtraction`
for `>`, `<`, `>=`, `<=` operators.

**Tests added:**

`TestSQLGen_BetweenStringBoundsUseTextExtraction` (`pkg/oql/sqlgen_test.go`) —
four sub-tests asserting the generated SQL contains `json_extract(...) BETWEEN`
(no CAST) for string bounds, and `CAST(... AS REAL) BETWEEN` for numeric
bounds. Includes NOT BETWEEN and mixed-name cases.

Three sub-tests added to `TestEquivalence` (`pkg/oql/equivalence_test.go`):
- `StringBetween_timestamps` — ISO-8601 timestamp range against the `readings`
  entity. Would have returned 0 rows from push-down before the fix.
- `StringNotBetween_timestamps` — NOT BETWEEN on timestamps.
- `StringBetween_codes` — zero-padded code strings (`SENS-0100` to `SENS-0200`).

These are equivalence tests: they assert Go-path and push-down produce
identical result sets, so any future regression in type-aware field extraction
will be caught immediately.


## [0.9.7-patched92] - 2026-05-14

### Docs — JOIN support documented, stale "not supported" language removed

Updated all locations that stated or implied OQL does not support JOIN
statements.

**`docs/OQL_API.md`** (primary user doc):
- Version bumped to 0.9.7.
- Limitations table: four JOIN rows now show ✓ Supported (INNER, LEFT, RIGHT,
  FULL OUTER); CROSS JOIN and three-table joins explicitly marked ✗ Not
  supported; subquery tables in FROM added as ✗.
- New "JOIN Queries" section added before Limitations, covering: supported join
  types, syntax, entity classification (adapted vs blob), examples for all
  cases (both adapted, blob entities, right join with nulls), column alias
  rules, constraints, and a JOIN vs Sulpher decision table.
- "When to Use OQL vs Sulpher" table: added "Flat cross-entity correlation →
  OQL JOIN" row.

**`docs/QUERY_OPTIMISATION_PROGRESS.md`**:
- Section renamed from "Future work: JOIN push-down (exploration)" to
  "JOIN push-down — implemented in v0.9.7-patched89/90".
- Opening paragraph updated to describe what was built and where to find the
  spec and user docs.
- "What would be needed" table: all four rows marked ✓ Done.
- Entity combination matrix: all three combinations (adapted+adapted,
  adapted+blob, blob+blob) marked ✓ Yes (blob-to-blob feasibility corrected
  from "No push-down" to implemented).
- Status block changed from "Exploration only. No implementation planned"
  to "Implemented."

**`docs/QUERY_PLANNER.md`**:
- Section 13 future-work table: two JOIN rows collapsed into one marked
  "Implemented in v0.9.7-patched89–91."

**`pkg/oql/oql.go`** (package comment):
- Added JOIN to the bullet list of supported features.
- Added "JOIN support" sub-section clarifying push-down to SQLite, all four
  join types, per-entity adapted/blob classification, and explicit list of
  unsupported forms.

**`MANUAL.md`**:
- Supported Features list: added JOIN, BETWEEN, IS NULL, DISTINCT, and
  INSERT/UPDATE/DELETE (previously omitted).
- Added JOIN example (orders with customer name).
- Added "Not supported" paragraph listing CROSS JOIN, three-table joins,
  subqueries, CTEs, window functions, and the SQLite-only constraint.


## [0.9.7-patched91] - 2026-05-14

### Tests — regression coverage for five bugs found in patched90

Six regression tests added, one per bug from the patched90 join push-down
implementation, to prevent silent re-introduction.

| Test | Bug guarded | File |
|---|---|---|
| `TestRegression_BlobJoinUsesJSONFieldAliased` | Bug 1: `a.json_extract(...)` form rejected by SQLite | `pkg/oql/sqlgen_join_test.go` |
| `TestRegression_JoinColumnAliasNoDots` | Bug 2: dotted alias `AS a.title` rejected by SQLite | `pkg/oql/sqlgen_join_test.go` |
| `TestRegression_SystemColumnIDResolvedForAdapted` | Bug 3: `id` not found in adapted entity registry | `pkg/oql/sqlgen_join_test.go` |
| `TestRegression_ListEntitiesIncludesAdapted` | Bug 4: adapted entities absent from `ListEntities` | `pkg/storage/list_entities_regression_test.go` |
| `TestRegression_ListEntitiesNoDuplicates` | Bug 4 (complementary): adapted entity appears at most once | `pkg/storage/list_entities_regression_test.go` |
| `TestRegression_JoinResultKeysAreBareName` | Bug 5: `projectColumns` re-keyed join rows as `a.title` | `pkg/server/join_e2e_test.go` |


## [0.9.7-patched90] - 2026-05-14

### Feature — OQL JOIN push-down (tests + bug fixes)

All three test suites specified in `docs/OQL_JOIN_PUSHDOWN.md` are now
implemented and passing. In the process, four bugs were found and fixed.

#### Tests added

**`pkg/oql/planner_join_test.go`** — 20 tests covering:
- `TestPlanner_Join`: PushJoin returned for all join types (INNER, LEFT, RIGHT,
  FULL) and all entity combinations (both adapted, both blob, mixed); PushNone
  when store lacks `AggregateQueryable`; join spec fields verified.
- `TestExtractJoinSpec`: nil-FROM guard, correct spec extraction, INNER JOIN.
- `TestIsJoinConditionPushable`: forward and reversed operand order.
- `TestIsJoinWherePushable`: qualified comparisons, AND, IN, IS NULL.

**`pkg/oql/sqlgen_join_test.go`** — 12 tests covering:
- Both adapted: native columns, no `json_extract`.
- Both blob: `json_extract` throughout, `entities` aliased twice.
- Left adapted / right blob and mirror case.
- WHERE clause (adapted and blob paths).
- Tenant scoping (two `tenant_id` clauses).
- No-WHERE produces clean SQL with zero args.
- FULL JOIN → `FULL OUTER JOIN`; LEFT JOIN preserved.
- Alias list matches SELECT column count.
- Nil plan.Join returns error.

**`pkg/server/join_e2e_test.go`** — 4 tests covering:
- `TestJoinPushdown_BothAdapted`: INNER JOIN two adapted entities end-to-end,
  correct cross-entity field values, `rows_scanned` sanity check.
- `TestJoinPushdown_BlobFallback`: INNER JOIN two blob entities via the
  `json_extract` path.
- `TestJoinPushdown_OuterJoin_NullRows`: RIGHT JOIN with unmatched left rows;
  Carol (no orders) appears with nil amount, no crash.
- `TestJoinPushdown_ValidatorRejectsUnsupported`: CROSS JOIN returns 4xx, not 5xx.

#### Bugs found and fixed

**1. `json_extract` with unqualified `data` in joins** (`pkg/oql/sqlgen_join.go`).
In the blob-path join, both sides reference the `entities` table under
different aliases. `joinFieldRef` was emitting `a.json_extract(data, '$.x')`
(prepending alias before the whole expression) instead of
`json_extract(a.data, '$.x')` (alias qualifying just the `data` column).
Fix: added `JSONFieldAliased(alias, field string) string` to the `SQLDialect`
interface (SQLite: `json_extract(alias.data, '$.field')`), and updated
`joinFieldRef` to use it for blob sides.

**2. Dotted column aliases rejected by SQLite** (`pkg/oql/sqlgen_join.go`).
Without explicit `AS` aliases, `columnAlias` returned the full qualified name
`a.title` for a `QualifiedIdentifier`, producing `SELECT ... AS a.title`
which SQLite rejects. Fix: `joinColumnAlias` strips the table qualifier for
unaliased `QualifiedIdentifier` columns, emitting `AS title`.

**3. System column `id` not found in adapted entity** (`pkg/oql/sqlgen_join.go`).
`AdaptedColumnInfo` only knows user-declared schema fields; the `id` PK column
is a system column absent from the registry. ON conditions like `a.author_id =
b.id` failed with "field id not found". Fix: `adaptedNativeColumn` helper
recognises `id`, `_version`, and `tenant_id` as system columns and returns them
directly without a registry lookup.

**4. `ListEntities` missed adapted entities** (`pkg/storage/sqlite.go`).
The OQL validator calls `ListEntities` to check entity existence. Adapted
entities live in their own tables (`olu_posts`, etc.) and have no rows in
the `entities` table, so the original `SELECT DISTINCT entity_type FROM
entities` query did not return them. The validator then rejected JOIN queries
targeting adapted entities with "entity does not exist". Fix: `ListEntities`
now unions blob entity names with `s.adapted.Entities()`.

**5. `projectColumns` re-keyed join results with dotted names** (`pkg/oql/executor.go`).
After push-down, the executor always called `projectColumns`, which used
`columnAlias` (returning `a.title`) to look up and output keys. The join
records were already correctly shaped with bare aliases (`title`), but
`projectColumns` re-keyed them as `a.title`. Fix: `PushJoin` records bypass
`projectColumns`; the `AggregateQuery` result is used directly.


## [0.9.7-patched89] - 2026-05-13

### Rename — timeseries Pebble subdirectory `pebble/` → `db/`

The directory created inside each tenant's timeseries store directory was
named `pebble/` — an implementation detail (the storage engine name) leaking
into the filesystem layout. Renamed to `db/` in `NewPebbleStore`.

Layout before:  `data/ts/t0001/pebble/`
Layout after:   `data/ts/t0001/db/`

This is a breaking change for any deployment with existing timeseries data.
To migrate, rename the `pebble/` subdirectory to `db/` inside each tenant's
timeseries directory before restarting olu.

### Docs — SQLite per-file tenant isolation implementation plan

Added `docs/SQLITE_PER_FILE_TENANTS.md` (437 lines): full implementation
plan for the optional `SQLitePerFileTenants` mode, updated to reflect the
agreed filesystem layout:

```
data/
  olu.db            ← tenant 0 base store
  ts/
    t0001/          ← per-tenant timeseries
      db/           ← Pebble LSM (all timelines as key ranges)
      registry.json
      meta.json
  sql/              ← per-tenant SQLite (when SQLitePerFileTenants = true)
    t0001/
      olu.db
    t0002/
      olu.db
```

Key decisions recorded in the plan:
- `sql/` (not `sqlite/`) for backend-agnostic naming
- `db/` (not `pebble/`) for the Pebble LSM directory
- Timelines are key-prefix ranges inside a single Pebble instance per
  tenant, not per-timeline directories or files
- `tenantDBPath` mirrors the timeseries `tenantDir` pattern exactly
- `os.MkdirAll` called before opening a new per-file tenant store

## [0.9.7-patched88] - 2026-05-13

### Fix — codec.go: payload dangling slice into Pebble-managed buffer

`DecodeValue` was returning `payload = val[pos : pos+plen]` — a direct
sub-slice of the `val` byte slice passed in by the caller. In all call sites
in `store.go`, `val` comes from `iter.Value()`, which returns a slice into
Pebble's internal memory. That memory is only valid for the current iterator
position; it is recycled when the iterator advances or closes.

`decodeEntry` is called inside iterator loops and the returned `Event` values
(including their `Payload` slices) are accumulated in a results slice. After
the loop, `defer iter.Close()` fires, at which point the payload slices in
the results are dangling references into Pebble's memory. Under normal timing
the memory is not immediately reused, so the bug was silent. Under race-detector
timing (which widens scheduling windows) the buffer was overwritten before the
caller read the payload, producing `\xff\xff\xff\xff\xff` corruption
(Pebble's tombstone fill pattern) instead of the written payload bytes.

The failure was first observed as a flaky `TestContract_Pebble/AppendQueryRange_FullPrefix`
failure (roughly 4/20 runs under `-race`). It also affects any caller that
retains `Event.Payload` after an iterator advance, including `Latest`,
`Aggregate`, and the full aggregate scan functions — all of which use
`DecodeValue` against `iter.Value()`.

Fix: replace the sub-slice assignment with an explicit copy:

    payload = make([]byte, plen)
    copy(payload, val[pos:pos+plen])

This is the only safe contract for callers who retain decoded data past the
iterator's lifetime. The comment in `DecodeValue` documents why.

**Also fixed in this patch:** the stale TODO comment in
`commit_ts_rollback_test.go` referring to `tsManager` being a concrete type
— it is already `timeseries.Manager` (interface) and `SetTSManager` already
exists. The TODO was not removed when the injection point was added.
That comment is left as-is for now; removing it is a documentation task.

## [0.9.7-patched87] - 2026-05-13

### Fix — TestGraphCounters_ConcurrentAccuracy: cold-store race under -race detector

The test fired 80 concurrent goroutines that all hit `storeForTenant` before
any tenant store was cached. Under race-detector timing (which widens goroutine
scheduling windows significantly) multiple goroutines simultaneously took the
slow path in `storeForTenant` and called `NewStoreFromConfig` concurrently
against the same SQLite file, causing some opens to fail with OLU-ST006
("Failed to initialise tenant context"). This caused writes to drop silently
and the count assertions to fail with unexpected totals.

The bug is in the test, not in `storeForTenant`. The `LoadOrStore` race guard
in `storeForTenant` is correct for steady-state operation; the problem is that
`NewStoreFromConfig` is not safe to call concurrently for the same file before
any connection is cached.

Fix: add a single synchronous `seedGraphEntity` call per tenant before
launching the concurrent goroutines. This pre-warms the `tenantStores` cache
so all 80 goroutines hit the fast path. Expected node counts updated to
reflect the two warmup nodes (one per tenant, ID 0).

Verified stable across 10 consecutive runs under `-race`.

## [0.9.7-patched86] - 2026-05-13

### Build — add `build-olu` target for single-binary builds

patched85 removed the ability to build only `olu` since `make build` now
builds all three binaries. Added `build-olu` as an explicit single-binary
target for situations where only `olu` is needed (fast iteration, CI jobs
that test only the server binary, etc.).

## [0.9.7-patched85] - 2026-05-13

### Build — `make build` now builds olu, iolu, and olu-migrate by default

Previously `make build` built only `olu`. `iolu` and `olu-migrate` required
separate `make build-iolu` and `make build-migrate` invocations, or the
combined `make build-all-tools`. The `release.sh` script called `make build`
directly, so only `olu` was verified to compile on each release.

`make build` now builds all three binaries in sequence. The individual
`build-iolu`, `build-migrate`, and `build-all-tools` targets are unchanged.
The `help` target description for `build` is updated accordingly.

## [0.9.7-patched84] - 2026-05-13

### Docs — OQL JOIN push-down implementation specification

Added `docs/OQL_JOIN_PUSHDOWN.md` (613 lines): a complete implementation
specification for SQLite-pushed JOIN queries in OQL.

Covers the full phased implementation plan (planner, SQL generator, executor,
validator, tests), the per-entity adapted/blob path decision, SQL shapes for
all supported join types (INNER, LEFT, RIGHT, FULL OUTER), NULL result row
handling for outer joins, the out-of-scope items (CROSS JOIN, Go-path
fallback, three-table joins), and a PostgreSQL compatibility section addressing
dialect mediation, `AggregateQuery` opacity, and the adapted/blob table
co-location assumption.

No code changes.

## [0.9.7-patched83] - 2026-05-13

### Tests — HTTP-layer graph path/pathExists/shortestPath coverage + cyclic termination + concurrent counter accuracy

Added `pkg/server/graph_path_e2e_test.go` (16 new tests). This addresses two
gaps identified in the graph test-suite review:

**Gap 1: no server-layer coverage of /graph/path, /graph/pathExists, or
/graph/shortestPath**

These three endpoints existed only in the unit-level contract suite
(pkg/graph/graph_contract_test.go). If the HTTP handlers misparse `max_depth`,
return wrong status codes on the no-path case, or leak the XXXX@ tenant prefix
in responses, no test would catch it.

Tests added per endpoint:

- `/graph/path`: happy path (chain of 4, length 3), self-path (length 0),
  no-path → 404, max_depth exceeded → 404 / sufficient → 200, missing params
  → 400, no XXXX@ prefix in response, tenant isolation (alpha chain invisible
  from beta routes).
- `/graph/shortestPath`: found (exists:true), not-found (200 + exists:false,
  unlike /path which returns 404), missing params → 400. Verifies the semantic
  difference between the two path endpoints is preserved at the HTTP layer.
- `/graph/pathExists`: found (exists:true + correct length), not-found
  (exists:false + length 0), missing params → 400, absent node → 404.

**Gap 2: no HTTP-layer cyclic-graph termination test**

`TestContract_PathExists_CyclicGraph_Terminates` existed in the unit contract
but had no server-layer analogue. `TestGraphPath_CyclicGraph_Terminates`
seeds a forward chain (node:1→node:2→node:3) via HTTP entity writes, then
injects the back-edge (node:3→node:1) directly via `s.graph` to close the
cycle. It then exercises both `/graph/pathExists` and `/graph/path` against
the cyclic graph and asserts: correct results, no hang within the test timeout,
and no XXXX@ prefix in responses.

**Gap 3: counter accuracy under concurrent writes (server layer)**

The unit contract tests verify counter correctness sequentially.
`TestGraphCounters_ConcurrentAccuracy` fires 80 concurrent goroutines writing
entities under two tenants (50 alpha, 30 beta), then cross-checks the HTTP
stats endpoint (`node_count`/`edge_count`) against direct calls to
`NodeCountForTenant`/`EdgeCountForTenant` on `s.graph`. Counter drift between
the HTTP layer and the underlying per-tenant maps under write concurrency is
the most likely production failure mode and was untested at this layer.

Also fixed: one incidental bug found during test authorship — `node_count` and
`edge_count` are the correct JSON field names in the stats response (not
`nodes`/`edges`).

**Files changed:**

- `pkg/server/graph_path_e2e_test.go` — new file, 16 tests

## [0.9.7-patched82] - 2026-05-13

### Fix — CommitTS test harness and missing POST /ts/query/range route

Two bugs introduced with the patched78-era CommitTS tests were found on this
checkpoint's first `make test` run.

**Bug 1: `commitTSEnv.registerTenant` used a non-existent HTTP route**

`commit_ts_e2e_test.go`'s test harness was calling
`POST /api/v1/tenant/{name}` over HTTP to register tenants, but no such
registration-only route exists in strict-mode olu. The result was a 404 on
every `registerTenant` call, which caused the downstream `POST /ts/provision`
call to fail with OLU-ST001 (unknown tenant), taking down all 15 `TestCommitTS_*`
tests.

Fix: rewrote `commitTSEnv.registerTenant` to call
`srv.TenantRegistry().GetOrRegister(ctx, name)` directly, matching the pattern
used by `tsEnv.registerTenant` in `ts_e2e_test.go`. Added `"context"` import.

**Bug 2: `tsQueryRange` in the test harness called `POST /ts/query/range`,
which was not a registered route**

The four remaining failing tests (`HappyPath_WithAppendAndTS`,
`SQLiteFailure_TombstonesEvents`, `SQLiteFailure_MultipleEvents_AllTombstoned`,
`SuccessfulCommit_EventPersists`) all queried Pebble via the helper, which sent
`POST /ts/query/range`. The only registered range-query route was
`GET /ts/events` (query-string parameters), so the POST returned 404.

Fix: added `HandleTSQueryRangePost` to `pkg/server/ts_handlers.go`. The handler
accepts a JSON body with the same logical parameters as `GET /events`
(`timeline`, `dims`, `from`, `to`, `limit`, `order`) and produces an identical
response. Registered as `POST /ts/query/range` in the `/ts` sub-router in
`server.go`.

`POST /ts/query/range` is the correct REST shape for complex range queries:
it avoids URL-length limits on large dimension arrays and is more ergonomic
for callers that already compose JSON payloads (such as `/commit` clients).
The `GET /events` endpoint is unchanged and remains supported for simple queries.

**Result:** all 15 `TestCommitTS_*` tests pass; full suite green.

**Files changed:**

- `pkg/server/commit_ts_e2e_test.go` — `registerTenant` rewritten; `"context"` import added
- `pkg/server/ts_handlers.go` — `HandleTSQueryRangePost` added (~90 lines)
- `pkg/server/server.go` — `POST /query/range` registered in `/ts` sub-router

## [0.9.7-patched64] - 2026-03-11

### Hygiene — Remove application-specific references from documentation and source

Scrubbed all references to a specific client application from the public-facing
codebase in preparation for open-source publication. olu is application-agnostic
and its documentation should not reveal the identities of integrating parties.

**Files changed:**

- `CHANGELOG.md` — rewrote entries that named the client, their instance names
  (`olu-registry`, `olu-ams`, `olu-ops-{1,2}`), their internal metric names, and
  their architectural decisions as the motivating rationale. Replaced with generic
  descriptions of the feature motivations.
- `docs/COMMIT_ENDPOINT_DESIGN.md` — replaced client-specific use-case framing
  with generic FSM/financial domain examples. Replaced `{giai}` ID placeholder
  with `{id}`. Removed named instance references.
- `pkg/oql/hardware_profile.go` — replaced client name in `ProfileVPS` comment
  with "self-hosted olu instances".
- `pkg/server/shelf_integration_test.go` → renamed to `integration_test.go`.
  All type names (`shelfTestEnv`, `shelfEntities`), comment callouts
  ("Shelf's exact query", "Shelf uses..."), and temp directory prefixes
  updated to generic equivalents.
- `pkg/server/shelf_tier1_test.go` → renamed to `tier1_oql_test.go`. Same
  scrub applied.
- `pkg/server/e2e_test.go` — updated file references in header comments;
  `shelf_schema` → `test_schema`.
- `pkg/server/e2e_coverage_gaps_test.go` — `shelf_schema` → `test_schema`.
- `pkg/server/commit_e2e_test.go` — `"giai"` field name in one test fixture
  replaced with `"ref_id"`.
- `pkg/config/config_test.go` — `/data/olu/shelf.db` path in test replaced
  with `/data/olu/registry.db`.
- `docs/TESTING_STRATEGY.md` — table updated to reflect renamed test files.

No functional changes. Test count unchanged.

## [0.9.7-patched63] - 2026-03-11

### Feature — env var aliases for combined address, path, and log level

Added convenience aliases to `LoadFromEnv` for callers that prefer concise
configuration (e.g. Docker Compose deployments):

| New variable | Equivalent to | Format |
|---|---|---|
| `OLU_ADDR` | `OLU_HOST` + `OLU_PORT` | `host:port` |
| `OLU_METRICS_ADDR` | `OLU_METRICS_HOST` + `OLU_METRICS_PORT` | `host:port` |
| `OLU_SQLITE_PATH` | `OLU_DB_PATH` | file path |
| `OLU_LOG_LEVEL` | replaces `OLU_DEBUG` bool | `debug\|info\|warn\|error` |

Precedence: specific variables (`OLU_HOST`, `OLU_PORT`, etc.) override the
combined aliases when both are set. `OLU_SQLITE_PATH` overrides `OLU_DB_PATH`
when both are set. All original variable names continue to work unchanged.

`OLU_LOG_LEVEL` is case-insensitive; unknown values are silently ignored and
the default (`info`) is retained. `OLU_DEBUG=true` remains supported as a
legacy alias for `OLU_LOG_LEVEL=debug`; `OLU_LOG_LEVEL` takes precedence.

`cfg.LogLevel` is now wired to `zerolog.SetGlobalLevel` in `cmd/olu/main.go`,
making `OLU_DEBUG` actually effective at runtime for the first time.

Added `net` import to `pkg/config/config.go` for `net.SplitHostPort`.
Added `LogLevel string` field to `Config` struct (default: `"info"`).
9 new config tests covering aliases, precedence, case handling, and
compat behaviour.

### Infra — Dockerfile and GitHub Actions publish workflow

Dockerfile at repo root: two-stage build (golang:1.22-alpine builder,
alpine:3.19 runtime), CGO_ENABLED=0 (modernc.org/sqlite is pure Go),
non-root user (uid 1001), `/data` workdir for volume mounts.

`.github/workflows/publish.yml`: triggers on `v*.*.*` and `v*.*.*-patched*`
tags plus manual dispatch. Runs full test suite before push. Publishes to
`ghcr.io/ha1tch/olu` with exact version tag and `:latest`. Uses GHA layer
cache for fast incremental builds.

## [0.9.7-patched62] - 2026-03-11

### Feature — `readSecret` helper: Docker secret file fallback for sensitive config

Added `readSecret(name string) string` to `pkg/config/config.go`.
Resolution order:

1. Environment variable `strings.ToUpper(name)` — returned as-is if non-empty.
2. File `/run/secrets/<name>` — trailing `\n`/`\r` stripped.
3. Empty string if neither is set.

Applied to `InternalToken` (`OLU_INTERNAL_TOKEN` / `/run/secrets/olu_internal_token`)
and `JWTSecret` (`OLU_JWT_SECRET` / `/run/secrets/olu_jwt_secret`). Both are
single-value secrets with unambiguous file semantics.

`APIKeys` is not covered by `readSecret` — it is a comma-separated list
and the file semantics would be ambiguous. It continues to be read from
`OLU_API_KEYS` only.

No behaviour change when the environment variable is set.

4 new config tests covering: env var precedence, file fallback newline
stripping, missing-both case, and `LoadFromEnv` integration.

## [0.9.7-patched61] - 2026-03-10

### Feature — `bearertoken` auth type + design doc corrections

Addresses several items from the integration review of v0.9.7-patched60,
covering auth, documentation accuracy, and operational confirmation.

**Item 1 — `OLU_AUTH_TYPE=bearertoken`**

New auth mode for internal service-to-service calls using a plain shared
secret via `Authorization: Bearer <token>`.

- `Config.InternalToken` field added; set via `OLU_INTERNAL_TOKEN`
- `validateBearerToken`: reads `Authorization: Bearer <token>`, compares
  against `InternalToken` using `subtle.ConstantTimeCompare`
- Returns subject `"internal"` on success; 401 OLU-AU001 on mismatch or
  missing header
- `WWW-Authenticate: Bearer realm="olu"` on 401 (same as JWT)
- Config validation: `InternalToken` required when `AuthType=bearertoken`
- Deliberately separate from the `jwt` validator despite both using the
  Bearer scheme — a raw hex token must never be silently parsed as a JWT
- 4 new middleware tests: Valid, Invalid, Missing, WrongScheme

**Item 2a — design doc: `id` type corrected from string to integer**

All JSON examples in `COMMIT_ENDPOINT_DESIGN.md` now use integer IDs.
`§4.1` table updated (`id: string → id: integer`). Clarifying note added:
"`id` is a positive integer — callers must parse string identifiers
(barcodes, account numbers, external IDs) to int before constructing the request."
UUID references in append examples replaced with realistic integer IDs.
`§4.2` append table updated; "olu generates a UUID" replaced with "olu
assigns the next sequence ID".

**Item 2b — design doc §14: isolation level corrected**

`§14.1` no longer shows `sql.LevelSerializable`. Updated to reflect the
actual `BeginTx(ctx, nil)` call with an explanation of how `withRetry` +
the WAL write lock provides serialisation.

**Item 3 — `withRetry` on `Commit` confirmed intentional**

Code comment added to `SQLiteStore.Commit` in `pkg/storage/sqlite.go`
explaining why `withRetry` is safe: retries fire only on `SQLITE_BUSY`;
`ErrConflict` exits the retry loop immediately; a retry cannot mask a
conflict or double-write. CAS guarantee preserved across retries.

**Confirmed — `/commit` on graph-disabled instances**

`syncGraphEdges` returns nil when `!s.config.GraphEnabled`;
`indexForFTS` returns nil when `!s.config.FullTextEnabled`. No other
subsystems in `commitInner` depend on graph or FTS. `/commit` operates
correctly on instances with `OLU_GRAPH_MODE=disabled` and
`OLU_FULLTEXT_ENABLED=false`.

**Item 4 — image tagging policy confirmed**

`ha1tch/olu:0.9.7` tracks the latest patched release on the 0.9.7 minor
version. No per-patch tags are published for 0.9.7. Callers pinning the
image get automatic patch updates on pull; callers needing a fixed build
should use the source build path.

## [0.9.7-patched60] - 2026-03-10

### Refactor — `/commit` backend detection moved to storage layer

The 501 Not Implemented response for `/commit` on the jsonfile backend is
now driven by the storage layer returning `storage.ErrNotSupported`, rather
than a `StorageType` config string check in the handler.

**`storage.ErrNotSupported`** — new sentinel added to `pkg/storage/storage.go`.
Returned by any backend that does not implement a given operation. The HTTP
handler maps it to 501/OLU-CM009.

**`JSONFileStore.Commit`** — replaced ~160 lines of best-effort atomicity
code (lock ordering, rollback helpers `saveForCommit`/`appendForCommit`)
with a three-line stub returning `ErrNotSupported`. The old implementation
gave a false impression of transactional atomicity that the filesystem
cannot provide. The stub is honest: it exists solely for interface
compliance.

**`handleCommit`** — removed the `s.config.StorageType == "jsonfile"`
guard. The handler is now backend-agnostic; any backend that does not
support `/commit` signals that by returning `ErrNotSupported`. The
`ErrNotSupported` branch is the first error check after `store.Commit`.

**Storage commit tests** — removed all `jsonfileFactory` calls from the
six contract tests; they now run against SQLite only. Added
`TestCommit_JSONFileReturnsErrNotSupported` to verify the stub returns the
correct sentinel (the only thing the jsonfile backend needs to guarantee
for this operation).

**Unused import removed** — `sort` was only used by the removed jsonfile
Commit helpers; removed from `jsonfile.go` imports.

**Docs** — `docs/COMMIT_ENDPOINT_DESIGN.md` section 11 (Backend
Availability) updated to reflect that the 501 is now signalled via
`ErrNotSupported` from the storage layer, not a config check in the
handler.

## [0.9.7-patched59] - 2026-03-10

### Feature — `/commit` hardening: strict mode, jsonfile restriction, graph update

Three correctness and safety improvements to the `/commit` endpoint
introduced in patched58.

**`OLU_STRICT_COMMIT` (default: `true`)**

When true (the default), `/commit` runs the same schema validation and
graph cycle prechecks as `save`/`create`/`patch` before executing the
storage transaction. Payloads that violate a registered entity schema
return `400 OLU-VL001`; graph cycle violations return `409 OLU-GR001`.
Set `OLU_STRICT_COMMIT=false` only when the caller is trusted
infrastructure that manages its own invariants and the validation overhead
is undesirable. Structural validation (entity names, positive IDs,
append count) is always enforced regardless of this setting.

**jsonfile backend: `/commit` returns 501**

`POST /commit` now returns `501 Not Implemented` with error code
`OLU-CM009` when the server is running with the jsonfile storage backend.
The jsonfile backend does not provide true transactional atomicity;
allowing `/commit` against it would silently violate the endpoint's core
guarantee. The jsonfile backend is deprecated for production use.
All `/commit` code paths must be tested against SQLite.

**In-memory graph updated after commit (unconditional)**

`handleCommit` now calls `s.updateGraph` for both the upserted entity and
all appended entities after a successful transaction, exactly as the
normal write surface does. Previously, the `FlatGraph` was not updated,
causing stale graph state for any entity with REF fields written via
`/commit`. This was a correctness bug; the fix is unconditional and not
gated on `OLU_STRICT_COMMIT`.

**Error codes added:** `OLU-CM009`

**Config field added:** `StrictCommit bool` (env: `OLU_STRICT_COMMIT`, default `true`)

**Docs:** `docs/COMMIT_ENDPOINT_DESIGN.md` updated to v0.2 with sections
11 (Backend Availability), 12 (Strict Mode), and 13 (Error Codes updated).

**Tests:** 7 e2e tests (SQLite-backed), including `TestCommitE2E_JSONFileReturns501`
and `TestCommitE2E_StrictModeSchemaValidation`.

## [0.9.7-patched58] - 2026-03-10

### Feature — Atomic `/commit` endpoint (upsert + append in one transaction)

Adds `POST /api/v1/commit` and `POST /api/v1/tenant/{tenant_id}/commit`.
The endpoint performs a conditional upsert (`update`) and one or more
unconditional inserts (`append`) in a single storage transaction, eliminating
the partial-write failure mode where a successful state update and its
corresponding audit record could be written independently and non-atomically.

**Request shape**

```json
{
  "update": {
    "entity": "objects",
    "id": 1234,
    "version": 7,
    "data": { "state": "active" }
  },
  "append": [
    { "entity": "timeseries", "data": { "asset_id": 1234, "to_state": "active" } }
  ]
}
```

`version` is optional — omitting it makes the upsert unconditional.
`append` accepts 1–25 entries; auto-generated IDs when `id` is omitted.

**Responses**

- `200 OK` — both upsert and all appends committed.
  Body: `{ "update": { "created": bool, "version": N }, "appended": [...] }`
- `409 Conflict (OLU-CM001)` — CAS version mismatch on update;
  body includes `current_version`.
- `409 Conflict (OLU-CM007)` — explicit append `id` already exists;
  entire commit rolled back.
- `400 Bad Request` — validation failure (OLU-CM002 through OLU-CM006).
- `500 Internal Server Error (OLU-CM008)` — transaction failure.

**Implementation**

- `storage.Store` interface: `Commit(ctx, CommitRequest) (CommitResult, error)`
- SQLite backend: single `BEGIN IMMEDIATE` transaction wrapping
  `saveInTx` (upsert with CAS) and `createInTx` (per-append insert).
  Full graph edge sync and FTS indexing within the transaction.
- jsonfile backend: per-entity mutex locking in sorted order; best-effort
  rollback on append failure (removes written files and the update file if
  the update was a new create).
- Error codes OLU-CM001–OLU-CM008 added to `pkg/errors`.
- Cache invalidation for update and all appended IDs after commit.

**Tests**

- 6 storage-layer contract tests (both backends): create, overwrite, CAS
  success, CAS conflict, duplicate-ID rollback, multiple appends.
- 5 HTTP e2e tests: basic happy path, FSM round-trip, CAS conflict,
  validation errors, rollback-on-duplicate-ID.
- OQL mock store updated to satisfy the extended `Store` interface.

## [0.9.7-patched57] - 2026-03-10

### Feature — Conditional writes (optimistic concurrency) on save, PUT, and PATCH

Implements a `_version` field in the entity envelope that enables conflict-safe writes
without coordination infrastructure.

**Protocol**

Every entity response from `GET` (and `POST /save/{id}` on first write)
includes `"_version": N` — an integer incremented on every successful write.
To make a write conditional, include `"_version": N` in the request body.
olu checks the stored version inside the write transaction and returns:

- `200 OK` / `201 Created` — write succeeded; version is now `N+1`.
- `409 Conflict` — stored version differs from expected. Body includes
  `"current_version": M` so the caller can re-read and retry without an
  extra GET.

Omitting `_version` from the request body leaves behaviour unchanged:
all three write paths (`PUT`, `PATCH`, `POST /save/{id}`) remain
unconditional and always succeed if the entity exists.

**Files changed**

- `pkg/storage/sqlite.go` — `saveInner` overwrite branch: extract `_version`
  from request data, strip it from the JSON blob, conditional `UPDATE … WHERE
  _version = ?` when present, `ErrConflict` on zero rows affected.
- `pkg/storage/jsonfile.go` — `Create` now writes `_version = 1`. `Update`
  reads the stored version, applies conditional check, and increments.
  `PatchValidated` threads the expected version through to `Update`.
  `Save` (overwrite path) already had conditional logic added in patched56;
  the create path now also initialises `_version = 1` consistently.
- `pkg/server/handlers.go` — `handleSave`: handles `storage.ErrConflict`,
  fetches current version via `fetchCurrentVersion` helper, returns 409 with
  `"current_version"` in body. New helper `fetchCurrentVersion` added.
- `pkg/server/handlers.go` — `handlePatch`: 409 now includes
  `"current_version"`.
- `pkg/server/server.go` — `handleUpdate`: 409 now includes
  `"current_version"`.
- Tests: `TestStoreSaveOptimisticConcurrency` (storage layer), `TestE2E_SaveCAS`
  and `TestE2E_UpdateCAS` (HTTP layer) verify correct-version success,
  stale-version 409 with `current_version`, and unconditional-write pass-through.

**Usage pattern for FSM executors**

Read current state via `GET /api/v1/tenant/{t}/objects/{id}` — note `_version`
from response. Compute transition. Write via `POST /api/v1/tenant/{t}/save/{id}`
with `"_version": N` in the body. On `409`, re-read and retry. No inter-executor
coordination required.

## [0.9.7-patched56] - 2026-03-10

### Fixed — `POST /save/{id}` now implements true upsert semantics

`handleSave` previously enforced exclusive-create: it returned `409 Conflict`
if the entity already existed, despite the `save` verb conventionally implying
upsert. This caused silent data loss in stateful FSM executors where only
the first state transition per entity was ever persisted.

**Change summary:**

- `Store.Save` interface signature changed from `error` to `(bool, error)`.
  The boolean is `true` when a new record was created, `false` when an existing
  record was overwritten. Both `SQLiteStore` and `JSONFileStore` implement the
  new contract.
- `SQLiteStore.saveInner`: existence check no longer returns `ErrAlreadyExists`.
  On hit, it performs an in-transaction `UPDATE`; on miss, it `INSERT`s as
  before. Sequence, graph, and FTS index handling are preserved for both paths.
- `handleSave`: the pre-handler `store.Exists` check is removed. The handler
  uses the returned bool to send `201 Created` on first write and `200 OK` on
  subsequent overwrites. All other error paths (graph cycle, duplicate edge,
  validation, storage failure) are unchanged.
- `olu-migrate` caller updated to accept `(bool, error)` — upsert semantics
  are strictly better for re-runnable migration.
- All tests updated. `TestStoreSave / Save duplicate ID` renamed and inverted
  to verify overwrite succeeds; `TestE2E_SaveEndpoint / save with existing ID`
  and `TestErrorPaths_SaveConflict` updated to match new behaviour. Four mock
  implementations in `pkg/oql` test files updated to satisfy the revised
  interface.



### Feature — Metrics bind address (OLU_METRICS_HOST)

Extends the dedicated metrics listener (added in patched54) with independent
interface binding.

**New config field:** `MetricsHost string` (env: `OLU_METRICS_HOST`)

Resolution order when `OLU_METRICS_PORT > 0`:

1. If `OLU_METRICS_HOST` is set explicitly, it always wins.
2. If `OLU_HOST` is a real interface address (not `0.0.0.0` or `::`), the
   metrics listener inherits it — no extra config required to keep scrape
   traffic on the same interface as the API.
3. Otherwise (wildcard host), the metrics listener falls back to `0.0.0.0`.

This rule avoids the problem of blindly inheriting a wildcard: `0.0.0.0`
carries no interface preference, so propagating it would be meaningless.
A real address carries explicit operator intent and *should* propagate.

The startup banner now shows the resolved metrics bind address alongside
the port, making the effective configuration visible at a glance.



### Feature — Dedicated metrics port (OLU_METRICS_PORT)

Adds support for serving `/metrics` on a separate TCP port, independent of
the main API port. Useful in deployments where Prometheus scrape traffic
should not compete with operational reads and writes on the primary port.

**New config field:** `MetricsPort int` (env: `OLU_METRICS_PORT`)

- When `OLU_METRICS_PORT` is unset or `0`, behaviour is unchanged: `/metrics`
  continues to be served on the main API port. No breaking change for existing
  deployments.
- When `OLU_METRICS_PORT` is set to a positive integer, olu starts a second
  minimal HTTP listener on that port serving only `/metrics`. The main API
  port no longer exposes `/metrics`, providing clean separation.
- Validation rejects `MetricsPort` values outside `0–65535` and values equal
  to `Port` (would cause a bind conflict).
- The startup banner now reports which port metrics are available on.
- The dedicated metrics listener is gracefully shut down alongside the main
  server on SIGTERM/SIGINT.

Example multi-instance deployment using port + 100 convention:

```
instance-a  OLU_PORT=9090  OLU_METRICS_PORT=9190
instance-b  OLU_PORT=9091  OLU_METRICS_PORT=9191
instance-c  OLU_PORT=9092  OLU_METRICS_PORT=9192
instance-d  OLU_PORT=9093  OLU_METRICS_PORT=9193
```


## [0.9.7-patched53] - 2026-03-09

### Refactor — Remove testConfig() alias from timeseries tests

`testConfig()` was left as a compatibility alias for `testStoreConfig()` in
patched52. Removed now that Pebble is intended to be fully detachable.
Every call site in the test suite now explicitly uses `testStoreConfig()` or
`testPebbleConfig()` as appropriate, making the config boundary unambiguous
even in tests.

## [0.9.7-patched52] - 2026-03-09

### Refactor — Timeseries store configuration split

`StoreConfig` previously carried six fields, five of which were Pebble LSM-tree
tuning parameters (`MemtableSize`, `BlockSize`, `Compression`,
`L0CompactionThreshold`, `MaxOpenFiles`). These had no meaning to any backend
other than Pebble and made `StoreFactory` appear to require Pebble knowledge.

**Changes**

- `StoreConfig` now contains only `DefaultRetentionDays` — the one setting that
  is meaningful to any backend.
- `PebbleConfig` is a new type holding the five Pebble-specific fields. Zero
  values are safe; `NewPebbleStore` applies documented defaults for each unset
  field (64 MB memtable, 32 KB blocks, zstd compression, threshold 4, 500 open
  files).
- `NewPebbleStore(dir, StoreConfig, PebbleConfig)` — signature extended.
- `NewPebbleStoreFactory(pcfg PebbleConfig) StoreFactory` — factory now closes
  over `PebbleConfig`; only `StoreConfig` flows through the `StoreFactory`
  contract.
- `server.go` construction site split accordingly: `tsCfg` carries retention,
  `pebbleCfg` carries engine tuning.
- All test helpers updated (`testStoreConfig`, `testPebbleConfig`; `testConfig`
  preserved as alias for tests that do not need independent variation).

A future non-Pebble backend need only implement `Store` and provide a
`StoreFactory` — no Pebble fields to ignore or misinterpret.

## [0.9.7-patched51] - 2026-03-09

### Fixed — vode log list capped at 10

The three log sites that emit vode node IDs (two in `loadEntitiesFromEdgeTable`
and one in `handleGraphRebuild`) previously logged the full ID list, which
could produce unbounded log output after a botched migration or corrupted
store. Each site now logs at most 10 IDs in `"vode_sample"` and adds a
`"vode_remaining"` field when the list is truncated. The full count is always
present in `"vode_count"`.

## [0.9.7-patched50] - 2026-03-09

### Added — Vode (forward-reference placeholder nodes)

**Concept**

A *vode* is a graph node created implicitly by `AddEdge` as a forward-reference
placeholder. It represents a node that has been pointed at by a REF field but
whose entity data has not yet arrived — the common case during streaming
graph hydration where edges may be replayed before their target entities are
written. The name "vode" is domain-specific and intentionally non-overlapping
with any valid olu entity type name.

**Invariant:** `VodeCount()` should be zero after successful hydration. A
non-zero count indicates dangling REF references — entity data that was
referenced but never written to the store.

**Changes**

- `NodeTypeVode = "__vode__"` constant exported from `pkg/graph`.
- `addEdgeLocked` now passes `NodeTypeVode` (previously `""`) when creating
  implicit endpoint nodes. Vodes are therefore visible in the type index and
  countable — no longer invisible to `GetNodesByType`.
- `addNodeLocked` updated with explicit vode-lifecycle rules:
  - Vode assignment to an already-typed node (real or vode) is a silent no-op.
  - Promotion from `NodeTypeVode` to a real type is permitted and removes the
    node from the vode type index, decrements `vodeCounters`.
  - `ErrNodeTypeMismatch` is only raised when an established *non-vode* type
    would be overwritten.
- `vodeCounters map[string]int` field added to `FlatGraph`, following the same
  pattern as `nodeCounters` / `edgeCounters`. Maintained in `addNodeLocked`,
  `RemoveNode`, `Clear`, and `Load`.
- `VodeCount() int` — total vode count across all tenants. O(1).
- `VodeCountForTenant(tenantPrefix string) (int, error)` — per-tenant vode
  count. O(1). Returns `ErrTenantRequired` on empty prefix.
- Both methods added to the `Graph` interface.
- `handleGraphVerify` response now includes `"vode_count"`.
- `handleGraphRebuild` response now includes `"vode_count"` and logs a `Warn`
  with vode node IDs if any vodes remain after the rebuild.
- `loadEntitiesFromEdgeTable` (both single- and multi-tenant paths) logs a
  `Warn` with vode node IDs if any vodes remain after hydration completes.
- Lifecycle and design rationale documented in a dedicated doc block above the
  `Graph` interface in `graph.go`.

**Tests**
- `TestContract_AddEdge_ImplicitNode_IsVode` — replaces `AbsentFromTypeIndex`; asserts vodes appear under `NodeTypeVode`, `VodeCount` tracks correctly, promotion works.
- `TestContract_Vode_RemoveDecrementsCounter`
- `TestContract_Vode_ClearResetsCounter`
- `TestContract_Vode_SaveLoadRoundtrip`
- `TestContract_VodeCountForTenant_EmptyPrefix_Errors`
- `TestContract_VodeCountForTenant_Isolated`

## [0.9.7-patched49] - 2026-03-09

### Fixed (FlatGraph static audit — medium-tier issues #8, #11, #12, #13, #14)

**Inconsistency fixed**
- **#8** — `HasCycle` had no per-tenant variant. A cycle in tenant A caused `HasCycle()` to return `true` globally, making tenant B's view incorrect. New method `HasCycleForTenant(tenantPrefix string) (bool, error)` performs a DFS scoped to nodes carrying that prefix only. Added to the `Graph` interface. Returns `ErrTenantRequired` on empty prefix.

**Smell fixed**
- **#11** — `CommonNeighbors` renamed to `SharedOutNeighbors` across the entire codebase (interface, `FlatGraph`, all handlers, mock, and all tests). The original name clashed with the graph-theory term "common neighbours" which conventionally means undirected shared neighbours; this method only considers directed out-edges.
- **#12** — `AdaptivePersister.save`: a `MarkDirty()` racing between `dirty.Store(false)` and `graph.Save()` completing would silently re-flag dirty, causing the next tick to fire a save logged as "periodic" with no indication why. A post-save check now logs a debug note when dirty was re-set during the save window, making the back-to-back save legible in operator logs.

**Performance fixed**
- **#13** — `GetAllNodesForTenant` and `GetNodesByTypeForTenant` scanned the full node map (O(N total)) on every call. A new `tenantNodes map[string]map[string]struct{}` field maintains a per-tenant node set, updated in `addNodeLocked`, `RemoveNode`, `Clear`, and `Load`. `GetAllNodesForTenant` is now O(N_tenant). `GetNodesByTypeForTenant` iterates the smaller of the tenant set and the type-index set — O(min(N_tenant, N_type)).
- **#14** — `wouldCreateCycle` BFS traversed out-edges from all tenants. Since cross-tenant edges cannot exist, this was pure wasted work. The inner loop now skips any neighbour whose non-empty tenant prefix differs from `from`'s prefix, scoping the BFS to the relevant tenant.

### Added
- `HasCycleForTenant(tenantPrefix string) (bool, error)` on `Graph` interface and `FlatGraph`.
- `tenantNodes` index field on `FlatGraph`; maintained by `addNodeLocked`, `RemoveNode`, `Clear`, `Load`.
- New contract tests: `TestContract_HasCycleForTenant_EmptyPrefix_Errors`, `_NoCycle`, `_WithCycle`, `_Isolated`, `TestContract_GetAllNodesForTenant_OwnedByTenant`, `TestContract_GetNodesByTypeForTenant_OwnedByTenant`, `TestContract_GetAllNodesForTenant_ReflectsRemoval`, `TestContract_CycleCheck_TenantScoped`.

### Changed
- `Graph.CommonNeighbors` → `Graph.SharedOutNeighbors` (breaking rename; all internal call sites updated).
- `mockGraph` in `persister_test.go` updated to implement new interface.

## [0.9.7-patched48] - 2026-03-09

### Fixed (FlatGraph static audit — easy-tier issues)

**Bugs fixed**
- **Bug #1** — `addNodeLocked`: calling `AddNode` on an existing, typed node with a *different* type silently retypes the node and corrupts the type index. Now returns `ErrNodeTypeMismatch`. Implicitly-created (typeless) nodes — those created as a side-effect of `AddEdge` — may still have their type assigned by a subsequent `AddNode` call; only an established, non-empty type now triggers the error.
- **Bug #2** — `wouldCreateCycle`: the BFS budget exhaustion check (`len(visited) >= cycleCheckLimit`) fired *before* the `cur == from` check. When the budget was exactly hit, the function returned `true` (false positive cycle) even when `from` was the next node to dequeue. Check ordering corrected: `cur == from` is evaluated first.
- **Bug #3** — `NewFlatGraphWithCycleDetection`: the struct was initialised with `logger: zerolog.Nop()` before the mode `switch`. The `default` branch therefore emitted a warning to a no-op logger — completely invisible. Invalid mode strings now print a warning to `os.Stderr` and fall back to `"ignore"` reliably.
- **Bug #4** — `Load`: file I/O, JSON parsing, and scratch-graph replay all happened before the write lock was acquired. Concurrent `Load` calls raced on the state swap, with potential counter drift and no panic. A dedicated `loadMu sync.Mutex` now serialises concurrent `Load` calls.

**Inconsistencies fixed**
- **#5** — `FindPath` / `PathExists`: neither method had a cross-tenant guard. A query with endpoints from different tenants returned "no path found" rather than `ErrCrossTenantEdge`, inconsistent with `AddEdge` and `CheckEdge`. Guards added to both.
- **#6** — `addNodeLocked`: redundant double string-scan for `@` (`strings.Contains` + `NodeIDPrefix`). Replaced with a single `strings.IndexByte` call.
- **#9** — `PathExists`: the destination node `to` was never recorded in the `visited` map before the early-return, violating the invariant that every enqueued node is tracked. Fixed.

**Code smell fixed**
- **#7** — Three public constructors (`NewFlatGraph`, `NewFlatGraphWithLogger`, `NewFlatGraphWithCycleDetection`) each contained an identical struct literal. Consolidated into a single internal factory `newFlatGraph(logger, mode)` that all three delegate to.

### Added
- `ErrNodeTypeMismatch` sentinel error in `pkg/graph/graph.go`.
- New contract tests covering all fixed issues: `TestContract_AddNode_TypeMutation_ReturnsError`, `TestContract_AddNode_ImplicitNode_CanBeTypedLater`, `TestContract_CycleMode_InvalidMode_FallsBackToIgnore`, `TestContract_Load_ConcurrentCalls_DoNotCorrupt`, `TestContract_PathExists_CyclicGraph_Terminates`, `TestContract_AddEdge_ImplicitNode_AbsentFromTypeIndex`, `TestContract_FindPath_CrossTenant_Rejected`, `TestContract_PathExists_CrossTenant_Rejected`.

## [0.9.7-patched47] - 2026-03-09

### Fixed (SQLite graph path — all 10 audit findings)

**Bug fixes**
- **Bug 1 (Critical)** — `syncGraphEdges` previously committed cycle-creating edges to the SQLite edge table even when `FlatGraph.AddEdge` would reject them, leaving the table and in-memory graph permanently diverged. Fixed by adding `Graph.CheckEdge` (same pre-flight as `AddEdge`, no mutation) and calling it in `handleCreate`, `handleUpdate`, `handleSave`, and `handlePatch` before any store write. A cycle-detection rejection now returns HTTP 409 and the write never reaches storage.
- **Bug 2 (High)** — Partial startup hydration (30-second context timeout firing mid-scan) left the in-memory graph with an undefined subset of edges, with no runtime signal to callers. The graph is now cleared to a known-empty state on hydration failure; the error log directs operators to `POST /api/v1/graph/rebuild`.
- **Bug 3 (High)** — `handleGraphRebuild` and `handleGraphVerify` were hardwired to `s.storage` (always tenant 0). Both handlers now use `s.getStore(r.Context())`, which resolves to the correct tenant-scoped store for non-zero tenants.
- **Bug 4 (Medium)** — `RebuildGraph` repaired the SQLite edge table but left the in-memory `FlatGraph` unchanged. After a successful rebuild, `handleGraphRebuild` now calls `reloadGraphFromStore` to clear and re-hydrate the in-memory graph from the repaired edge table; a restart is no longer needed.

**Code smells resolved**
- **Smell 1** — `RebuildGraph` emitted a `log.Printf("[WARN] ...")` that bypassed zerolog. `SQLiteStore` now carries a `zerolog.Logger` field (default: `zerolog.Nop()`), set via `WithLogger`. The warn is now routed through the application logger. Both the primary store (wired in `main.go`) and tenant stores (wired in `storeForTenant`) receive the application logger.
- **Smell 2** — `VerifyGraphIntegrity` issued two independent `readDB` queries with no transaction, allowing concurrent writes to produce false positive violations. Both reads now run inside a single `BeginTx(ReadOnly: true)` transaction.
- **Smell 3** — `VerifyGraphIntegrity` materialised two full edge maps before comparing, with unbounded memory growth. Actual edges from the edge table are now streamed and checked against the expected-edge map rather than accumulated into a second map.
- **Smell 4** — `VerifyGraphIntegrity` returned on the first violation; `RebuildGraph` accumulated all. Policy is now consistent: `VerifyGraphIntegrity` collects all violations, sorts them, and returns them in a single joined error.
- **Smell 5** — `SQLiteStore.GetNeighbors` was dead code (the server never called it; all graph traversal went through the in-memory `FlatGraph`) and had a latent adapted-entity bug (would silently return no neighbours for entities stored in per-entity tables). Removed from `SQLiteStore` and from the `Store` interface. Tests that exercised it have been rewritten to use `ScanGraphEdges` + `Get`.
- **Smell 6** — PATCH was the only write path where a post-commit `Get` failure left the in-memory graph silently stale with no log entry. The failure is now logged at Warn level.

### Changed
- `Graph` interface gains `CheckEdge(from, to, relationship string) error`.
- `FlatGraph` implements `CheckEdge` using a read lock; safe to call concurrently.
- `SQLiteStore` gains a `logger zerolog.Logger` field and `WithLogger(*SQLiteStore)` method.
- `handleGraphRebuild` response message updated to note in-memory graph reload.

## [0.9.7-patched46] - 2026-03-09

### Fixed
- **`AdaptivePersister.Start()`: double-call panic** — calling `Start()` more
  than once spawned a second `loop()` goroutine; when `Stop()` subsequently
  closed `stopCh` both goroutines attempted `close(doneCh)`, causing a panic.
  `Start()` now uses `CompareAndSwap` on the existing `started` atomic so only
  the first call launches the loop; subsequent calls are silent no-ops.
- **`FlatGraph.Load()`: custom `cycleCheckLimit` silently reset to 512** —
  when loading a file written by an older version of olu that did not persist
  `cycle_check_limit`, the field was absent and the scratch graph's limit
  stayed at `DefaultCycleCheckLimit` (512), which was then unconditionally
  swapped into the receiver. An operator who had called `SetCycleCheckLimit`
  before `Load` would silently lose their configured value. The fix mirrors the
  existing `cycleDetection` fallback: when the file lacks the field, the
  receiver's runtime value is preserved.
- **`FlatGraph.CommonNeighbors()`: single error for two missing nodes** — when
  either or both nodes were absent a single error message was returned
  (`"one or both nodes not found: A, B"`), making it impossible for callers to
  identify which node was missing without a second round-trip. Both existence
  checks are now performed individually, consistent with `FindPath` and
  `PathExists`.
- **`TestContract_Topology_Diamond`: tautological `HasCycle` assertion** — the
  condition `!g.HasCycle() == false` parses as `g.HasCycle() == true` due to
  Go operator precedence, meaning the test fired when a cycle *was* present
  (correct) but would also have passed silently if `HasCycle` erroneously
  returned `true` on a DAG. Corrected to `if g.HasCycle()`.

### Changed
- **`AdaptivePersister`: `currentInterval` refactored into `intervalForWriters`
  + `currentInterval`** — the debug log in `save()` previously called
  `currentInterval()` after already loading `activeWriters`, resulting in two
  separate atomic reads that could observe different values and produce a log
  entry with an inconsistent (writers, interval) pair. `intervalForWriters(n)`
  now performs the pure calculation given an already-known count;
  `currentInterval()` delegates to it. `save()` loads `activeWriters` once and
  passes the snapshot to both the log field and `intervalForWriters`.
- **`defaultCycleCheckLimit` unexported alias removed** — the package
  previously defined both `DefaultCycleCheckLimit = 512` (exported) and
  `defaultCycleCheckLimit = DefaultCycleCheckLimit` (unexported alias). The
  alias served no purpose and created a second name that could drift from the
  canonical constant. All internal uses now reference `DefaultCycleCheckLimit`
  directly.
- **`FlatGraph.Load()` scratch graph now inherits the receiver's logger** —
  the scratch `FlatGraph` constructed during file replay was built with a
  zero-value `zerolog.Logger` rather than `zerolog.Nop()`. While harmless
  today (replay runs in ignore mode and hits no log calls), any future log
  statement reached during replay would silently discard its output. The
  scratch graph now inherits `g.logger`, matching the intent of all three
  public constructors.
- **`GetNodeInfo`: `fmt.Sscanf` error now logged at debug level** — the entity
  ID parse error was previously discarded with `_, _`. A malformed entity ID
  now emits a debug-level log entry rather than silently reporting 0, making
  unexpected node ID formats visible during development without adding noise in
  production.

### Tests
- **New: `TestContract_SaveLoad_CycleCheckLimitPreserved`** — verifies that a
  graph saved with a custom `cycleCheckLimit` reloads with that value intact,
  and that loading an older file that lacks the field preserves the runtime
  value rather than reverting to the package default (regression test for the
  bug fixed above).
- **New: `TestContract_CounterConsistency`** — verifies that after a mixed
  sequence of add/remove operations `NodeCount()` equals `len(nodes)`, the
  sum of `nodeCounters` equals `len(nodes)`, `EdgeCount()` equals the count of
  outgoing edges in the node map, and `edgeCount` equals the sum of
  `edgeCounters`. Catches counter drift between the global and per-tenant
  layers.
- **New: `TestContract_GetNodeInfo_NoColon`** — exercises the branch in
  `GetNodeInfo` where the node ID contains no colon, confirming that `Entity`
  is empty string and `EntityID` is 0 with no error or panic.


## [0.9.7-patched45] - 2026-03-09

### Fixed
- **`handleGraphPath`: missing `from`/`to` empty-param guard** — empty or absent `from`/`to`
  fields were passed directly to `FindPath`, producing a 404 with a garbled error message
  (`"node  not found"`) instead of a 400. The handler now returns 400 when either field is
  empty, consistent with every other path-bearing handler (`handleGraphShortestPath`,
  `handleGraphPathExists`, and all three tenant counterparts).
- **`handleGraphPath`: bare `len(path)-1` length** — the non-tenant path handler used the
  raw expression without a `< 0` guard, unlike `handleGraphShortestPath` which was corrected
  in patched44. The expression can only return -1 through a code path that does not currently
  exist, but the inconsistency is closed for defensive correctness.
- **`handleTenantGraphNodeInfo`: dead double-strip of `Entity`** — `GetNodeInfo` already strips
  the `XXXX@` tenant prefix from the `Entity` field before returning. The handler additionally
  called `strings.TrimPrefix(info.Entity, prefix)`, which was always a no-op. The redundant
  strip and its misleading comment have been removed; a replacement comment documents the
  invariant guarantee that makes the handler strip unnecessary.

### Added
- **`OLU_GRAPH_CYCLE_CHECK_LIMIT` config key** — the BFS node-visit budget for cycle detection
  (`cycleCheckLimit`, previously hardcoded to 512 with no runtime override) is now configurable
  via environment variable. `GRAPH_API.md` already documented the operator guidance to raise
  this limit on large graphs; the config key makes that guidance actionable. A value of 0
  (the default) retains the built-in default of 512.
- **`FlatGraph.SetCycleCheckLimit(n int)`** — new method on `*FlatGraph` for callers that hold
  a concrete reference and need to set the limit after construction. Used by `main.go` when
  `GraphCycleCheckLimit > 0`.
- **`graph.DefaultCycleCheckLimit`** — the previously-unexported `defaultCycleCheckLimit`
  constant (512) is now exported so callers (e.g. the startup diagnostic print) can display
  the effective default without hard-coding the value.

### Tests
- **New: three sub-tests in `TestGap_LegacyGraphPath`** cover the missing-`from`, missing-`to`,
  and missing-both cases for `POST /api/v1/graph/path`, asserting HTTP 400.



### Fixed
- **`handleGraphNeighbors` / `handleTenantGraphNeighbors`: missing `node_id` guard** — an empty
  or absent `node_id` field was silently accepted, resulting in a 200 response with an empty
  neighbours map. Both handlers now return 400 when `node_id` is empty, consistent with every
  other node-ID-bearing handler.
- **`handleGraphNeighbors` / `handleTenantGraphNeighbors`: unrecognised `direction` silently
  returns empty** — any value other than `"out"`, `"in"`, or `"both"` caused neither block to
  execute, returning 200 `{"neighbors":{}}` with no indication of the error. Both handlers now
  return 400 for unrecognised direction values.
- **`handleGraphShortestPath`: bare `len(path)-1` could produce `-1` length** — unlike the tenant
  counterpart which uses `max(len-1, 0)` via `tenantPathResult`, the non-tenant handler used the
  raw expression. Guard added for consistency and defensive correctness.

### Documentation
- MANUAL.md version header updated from `patched42` to `patched43`.
- Duplicate `OLU_TS_MAX_AGGREGATE_BUCKETS` config row removed.
- Duplicate `OLU-TS019` error table row collapsed to a single entry.

## [0.9.7-patched43] - 2026-03-09

### Fixed
- **Info-leak (class #1, continued):** `FindPath`, `PathExists`, `GetNodeInfo`, and `GetDegree`
  were passing raw tenant-prefixed node IDs (`XXXX@entity:N`) directly into error strings
  surfaced to HTTP callers. All six affected error sites now strip the prefix via
  `tenant.NodeIDStripped()`, consistent with the fixes applied to `CommonNeighbors` and
  `UpdateFromEntityForTenant` in patched42.
- **`Load` partial state on error:** `Load` previously cleared all graph state then replayed
  nodes and edges into the live receiver; any replay error left the graph in a partially-rebuilt
  state with no documented recovery path. `Load` now replays into a scratch `FlatGraph` and
  swaps the receiver's fields atomically only on full success. A failed load leaves the receiver
  completely unchanged.
- **`Save` `.tmp` leak on `Rename` failure:** `os.Rename` failures (cross-device link,
  permissions) were leaving the `.tmp` staging file on disk. The `.tmp` is now removed on any
  `Rename` error.
- **`AdaptivePersister.Stop()` deadlock if `Start()` never called:** `Stop()` blocked forever
  on `<-p.doneCh` if `Start()` had not been called, because `doneCh` is only closed by the
  goroutine spawned in `Start()`. A `started` atomic flag now guards `Stop()`; calling it before
  `Start()` is a no-op.

### Changed
- **`Load` no longer runs cycle detection during replay:** Previously, `Load` set
  `g.cycleDetection` from the file before replaying edges, so every `addEdgeLocked` call in
  `"error"` mode triggered a full BFS — O(E × N) for a well-formed file that by construction
  contains no cycles. The scratch graph used during replay always uses `"ignore"` mode; the
  configured mode is restored after a successful replay.
- **`FlatGraph` now uses structured `zerolog` logging:** All `log.Printf` call sites have been
  replaced with `zerolog` events. `NewFlatGraph` and `NewFlatGraphWithCycleDetection` default to
  `zerolog.Nop()` (no output). A new constructor `NewFlatGraphWithLogger(logger zerolog.Logger)`
  allows callers to inject a logger. This aligns the graph layer with the structured logging used
  by `AdaptivePersister` and the server layer, enabling log correlation across tiers.
- **`GetNodesByType` and `GetNodesByTypeForTenant` return a non-nil empty slice for all empty
  results:** Previously `GetNodesByType` returned `nil` when no nodes matched, and
  `GetNodesByTypeForTenant` returned two distinct nil shapes (`nil, nil` vs non-nil empty slice)
  depending on whether the entity type was absent from the index or present but empty for the
  tenant. All empty results now consistently return `([]string{}, nil)`. The `nil`-compensation
  guard at the call site in the tenant handler (line 631) is now redundant but harmless.

### Performance
- **BFS queue memory reclaimed:** All three BFS callers (`FindPath`, `PathExists`,
  `wouldCreateCycle`) used a slice-header-slide dequeue (`queue = queue[1:]`) that left the
  backing array's consumed prefix reachable until the whole local variable went out of scope.
  All three now use a head-index (`head++`) so the front elements become eligible for GC as the
  traversal proceeds.

### Documentation
- **`wouldCreateCycle` budget exhaustion documented:** The conservative behaviour when the BFS
  visit budget (`cycleCheckLimit`, default 512) is exhausted — returning `true` and thus
  `ErrCycleDetected` even when no actual cycle exists — is now documented in `graph.go`
  (interface comment, `CycleCheckBudgetExceeded` informational constant) and in a new section
  of `docs/GRAPH_API.md` explaining the implications for operators on large or dense graphs.

### Tests
- **New: `TestContract_UpdateFromEntityForTenant_RelabelExistingEdge`** covers the previously
  untested relabel path in `UpdateFromEntityForTenant`: the `ErrEdgeAlreadyExists` branch that
  deletes the old edge and re-adds with the new relationship label, including the counter-safe
  rollback. Verifies that the edge count stays at 1 and that both the out-map and in-map carry
  the updated label.

### Maintenance
- Removed two stale comments in `flat_graph.go` that referenced `pkg/graph/state` (removed in
  patched38) and its `wouldCreateCycle` function.


- `FlatGraph.CommonNeighbors` (`pkg/graph/flat_graph.go`): the method now
  always returns a non-nil slice. Previously `var result []string` was used,
  so a call with no common neighbours returned `nil` rather than an empty
  slice, violating the contract documented on the `Graph` interface. Both
  REST handlers (`handleGraphCommonNeighbors`,
  `handleTenantGraphCommonNeighbors`) contained an identical `if common == nil
  { common = []string{} }` patch to compensate; both patches have been
  removed now that the guarantee lives in the function itself.
- `FlatGraph.CommonNeighbors`: the error message returned when one or both
  nodes are absent now strips any `XXXX@` tenant prefix from the node IDs via
  `tenant.NodeIDStripped`, preventing internal tenant identifiers from leaking
  to callers. This is a second instance of the info-leak fixed in patched41's
  `UpdateFromEntityForTenant` rollback messages.
- `FlatGraph.UpdateFromEntityForTenant` rollback error messages: the two
  `fmt.Errorf` calls in the edge-relabel failure paths now use
  `tenant.NodeIDStripped` on both `nodeID` and `targetNodeID`, so the
  `XXXX@` tenant prefix is not visible in errors that bubble up to callers.
  The `log.Printf` diagnostic line retains full node IDs as intended.
- `FlatGraph.Load` (`pkg/graph/flat_graph.go`): the `cycle_detection` field
  read from a persisted file is now validated against the three legal values
  (`"ignore"`, `"warn"`, `"error"`) before being applied. Previously an
  unrecognised value (e.g. a typo or hand-edited file) was silently stored;
  in `addEdgeLocked` the switch has no default, so a bad mode would trigger
  cycle detection but then add the cycle without warning or error. `Load` now
  returns an error for invalid modes.
- `guardEdgeMap` (`pkg/server/graph_tenant_handlers.go`): the function now
  builds a fresh map instead of mutating its argument with `delete`. The
  previous in-place approach was safe only because every call site passed a
  freshly-allocated map from `stripPrefixFromEdgeMap`, but that aliasing
  contract was invisible at the call site. The fix mirrors the allocation
  discipline already applied to `guardSlice` in patched41, and the function
  comment now cross-references that history.
- `handleTenantGraphPath` and `handleTenantGraphShortestPath`
  (`pkg/server/graph_tenant_handlers.go`): the `"length"` field could be
  `-1` when `guardSlice` filtered every node from the result (a degraded
  state where cross-tenant contamination was detected). The shared logic —
  strip prefix, guard, compute safe length — is now extracted into
  `tenantPathResult`, which returns `max(len(guarded)-1, 0)`. Both handlers
  call the helper; the duplicated strip/guard/length arithmetic is gone.

### Added

- `tenant.NodeIDStripped` (`pkg/tenant/tenant.go`): new helper that returns a
  node ID with its `XXXX@` tenant prefix removed, or the input unchanged if
  no prefix is present. Intended for use in error messages that must not leak
  internal tenant identifiers to callers.
- `(*Server).tenantPathResult` (`pkg/server/graph_tenant_handlers.go`): new
  private helper that strips the tenant prefix from a path slice, guards
  against cross-tenant leakage via `guardSlice`, and returns the clean path
  with a safe non-negative edge-count length. Eliminates the duplication
  between `handleTenantGraphPath` and `handleTenantGraphShortestPath`.

### Tests

- `TestContract_CommonNeighbors_None`: strengthened to assert `common != nil`
  (not just `len(common) == 0`) so the non-nil contract is machine-checked.
- `TestContract_CommonNeighbors_SameNode` (new): verifies that when
  `nodeA == nodeB` all outgoing neighbours of that node are returned, and
  that the result is non-nil. The behaviour is now documented in the function
  comment.
- `TestContract_Load_InvalidCycleDetectionMode_Errors` (new): writes a file
  with `"cycle_detection": "strict"` and asserts that `Load` returns an error.

## [0.9.7-patched41] - 2026-03-08

### Fixed

- `guardSlice` (`pkg/server/graph_tenant_handlers.go`): replaced the
  filter-in-place idiom (`nodes[:0:len(nodes)]`) with a fresh allocation
  (`make([]string, 0, len(nodes))`). The old form shared the backing array
  with the input slice; if a future call site passed a slice it still held a
  reference to, its contents would be silently clobbered. Added a comment
  explaining the previous hazard.
- `FlatGraph.UpdateFromEntityForTenant` relabel path: when re-adding an edge
  with a new label fails and the restore of the original label succeeds, the
  returned error now explicitly states that the original edge is intact
  (`"relabel … failed (original relationship %q preserved): %w"`). Previously
  the bare `addErr` gave no indication of graph consistency. When both re-add
  and restore fail, the error now wraps both (`"relabel … failed and restore
  of %q also failed (%v): %w"`), making the double-failure visible to the
  caller.

### Changed

- `Graph.CommonNeighbors` interface declaration and `FlatGraph.CommonNeighbors`
  implementation: added documentation stating that only outgoing edges are
  consulted — the method returns nodes that both `nodeA` and `nodeB` point
  *to*, not nodes that point to them. The interface doc also clarifies the
  return contract (empty non-nil slice on no overlap). The implementation
  comment notes the deliberate choice to keep the semantics stable rather than
  extending the method, so existing callers and tests are unaffected.

## [0.9.7-patched40] - 2026-03-08

### Fixed

- `FlatGraph.UpdateFromEntityForTenant` double-failure path: when both the
  re-add of a relabelled edge and the rollback restore fail, the code
  previously incremented `edgeCount` and `edgeCounters` — the opposite of
  the comment's stated intent. The edge is already gone at that point and the
  counters were already decremented, so no correction is needed. The erroneous
  increments have been removed; the log message updated to reflect the correct
  state (`"edge removed, counters consistent"`).
- `handleTenantGraphPath` and `handleTenantGraphShortestPath`
  (`pkg/server/graph_tenant_handlers.go`): the `"length"` field in the
  response was derived from the raw (pre-guard) path slice. If `guardSlice`
  removed any cross-tenant nodes, the returned length would disagree with the
  actual number of hops in the returned path array. Both handlers now capture
  the guarded slice into a local variable and compute `length` from it.

### Changed

- `flatGraphData` serialisation struct: added `CycleDetection string` and
  `CycleCheckLimit int` fields (both `omitempty`) so that `FlatGraph.Save`
  includes the cycle-detection policy in the persisted file and
  `FlatGraph.Load` restores it on startup. Previously a server restart always
  reverted to `"ignore"` regardless of the configured mode. Files written by
  older versions of olu that lack these fields remain valid; `Load` leaves the
  constructor-supplied default intact for absent fields. Both `Save` and `Load`
  have updated doc comments stating this contract explicitly.

## [0.9.7-patched39] - 2026-03-08

### Fixed

- `FlatGraph.Load`: node and edge errors are no longer silently discarded with
  `_ =`. Each failed entry is now logged at `[WARN]` level and the method
  returns an error summarising the count of skipped entries. Previously a
  corrupt or manually-edited `graph.json` could produce a partially-loaded
  graph with no indication of the discrepancy.
- `FlatGraph.UpdateFromEntityForTenant`: when an edge-label update fails at
  both the re-add and the rollback stage, `edgeCount` and `edgeCounters` are
  now explicitly corrected upward to match the actual (edgeless) state.
  Previously the counters were decremented but never restored, causing a
  permanent undercount for the life of the process.
- `FlatGraph.NodeCountForTenant` / `EdgeCountForTenant`: the silent `n < 0 →
  return 0` clamp is replaced with a logged `[ERROR]` that still returns 0
  rather than a negative value. The clamp masked counter-corruption bugs
  (including the one above) without surfacing them. Logging makes the
  invariant violation visible in production.
- `AdaptivePersister.save`: `p.mu` is now released before calling
  `graph.Save()` and re-acquired only to update `p.lastSave`. Previously the
  mutex was held for the entire I/O duration, blocking `Stats()` calls for up
  to tens of milliseconds per save.

### Changed

- `FlatGraph.Save`: the read lock is now released after copying the in-memory
  snapshot and before JSON serialisation and disk I/O. Previously the lock was
  held for the full duration of the write, blocking all mutation operations.
- `addEdgeLocked` (cross-tenant guard): added a comment explicitly documenting
  that tenant-0 (bare `"entity:id"`) nodes are permitted to form edges with
  non-zero-tenant nodes. This is the intended "shared global namespace"
  behaviour; the code was previously correct but silent about it.
- `GetNeighbors` / `GetIncomingEdges`: added doc comments pinning the
  intentional silent-empty contract for absent nodes. Two new contract tests
  (`TestContract_GetNeighbors_AbsentNode_ReturnsEmpty`,
  `TestContract_GetIncomingEdges_AbsentNode_ReturnsEmpty`) cover this
  behaviour, which was previously undocumented and untested.
- `ErrEdgeAlreadyExists`: added a comment to the sentinel definition noting
  its internal dual-use as control flow within `UpdateFromEntityForTenant`.
  A companion comment at the call site explains the label-update protocol.
- `addEdgeLocked` cycle-detection `"warn"` branch: added a comment clarifying
  that the fall-through after the log line is intentional — the edge is added
  even when a cycle is detected in warn mode.

## [0.9.7-patched38] - 2026-03-08

### Removed

- `IndexedGraph` and `pkg/graph/state` deleted. `FlatGraph` is now the sole
  graph implementation. All tests, benchmarks, server fixtures, and Sulpher
  test helpers migrated to `FlatGraph`.
- `"indexed"` removed as a valid `GraphMode` config value. The only valid
  non-disabled mode is `"flat"`. Existing configs using `GraphMode: indexed`
  must be updated to `GraphMode: flat`.
- `docs/OPTION_B_SPEC.md` and `docs/GRAPH_TENANT_ISOLATION_BRIEF.txt` deleted
  — historical design documents superseded by the completed implementation.
- `pkg/graph/graph_test.go`, `graph_intensive_test.go`, `graph_tenant_test.go`
  deleted (all tested `IndexedGraph` exclusively).

### Changed

- `pkg/graph/graph.go` now contains only the `Graph` interface, sentinel
  errors, `Degree`, `NodeInfo`, and `defaultCycleCheckLimit`. Sentinel errors
  were previously re-exported from `pkg/graph/state`; now defined directly.
- `pkg/graph/graph_contract_test.go`: `IndexedGraph` row removed from the
  implementation table; `FlatGraph` is the only entry.
- `pkg/graph/bench_test.go`: `_Indexed` variants and `buildIndexedGraph`
  removed; benchmarks renamed to drop the `_Flat` suffix.
- `docs/GRAPH_INVARIANTS.md` rewritten for `FlatGraph`.

## [0.9.7-patched37] - 2026-03-08

### Fixed

- `state.CommonNeighbors`: rewritten to outgoing-only, matching `FlatGraph.CommonNeighbors`; the previous bidirectional implementation was a contract violation — incoming edges were incorrectly included in the common-neighbour set. Contract test `TestContract_CommonNeighbors_IncomingEdgesExcluded` added to pin the outgoing-only semantics across both implementations. `TestTopology_Diamond` corrected to reflect the agreed behaviour (common predecessor is not a common neighbour).
- `AdaptivePersister.save`: dirty flag now cleared *before* `graph.Save()` rather than after. The old order had a TOCTOU window where a `MarkDirty()` call racing with a completed save would be silently swallowed, causing that write to never be persisted. On save failure the flag is explicitly restored so the next tick retries.
- `IndexedGraph.NodeCountForTenant` / `EdgeCountForTenant`: added missing `mu.RLock()` / `mu.RUnlock()` — every other read method on `IndexedGraph` holds the read lock; these two were inconsistent with both the rest of `IndexedGraph` and `FlatGraph`'s equivalent methods.
- `FlatGraph.RemoveNode`: incoming-edge counter decrements are now unconditional, matching the outgoing-edge block. Previously the decrement was inside the `if sr, ok := g.nodes[source]; ok` guard, meaning a corrupt/partially-loaded graph referencing a deleted source node would leave `edgeCount` and `edgeCounters` permanently high.
- `FlatGraph.wouldCreateCycle`: budget metric changed from total dequeues (`steps`) to unique nodes visited (`len(visited)`). The `steps` counter over-counted on bushy graphs with many parallel paths, triggering the conservative-reject threshold earlier than intended.
- `state.wouldCreateCycle`: rewritten from DFS to BFS with `len(visited)` budget metric, aligning with `FlatGraph.wouldCreateCycle`. Both implementations now use identical traversal order and identical budget semantics. `map[string]bool` replaced with `map[string]struct{}`.

### Changed

- `AdaptivePersister` type comment updated from hedged "DEPRECATION NOTE: … if the JSON filestore is eventually removed" to direct "DEPRECATED: The JSON filestore backend is deprecated … when the JSON filestore is removed". Reflects the stated direction.
- `state.AddEdge`: added comment documenting the intentional duplication of the cross-tenant check also present in `IndexedGraph.AddEdge`, explaining which callers bypass the outer method and the update obligation if the condition changes.
- `IndexedGraph` marked as pending deprecation in favour of `FlatGraph` in `state.wouldCreateCycle` comment; `FlatGraph.wouldCreateCycle` designated as the canonical implementation.

### Added

- `.gitignore`: covers `*.db`, `*.db-shm`, `*.db-wal`, `graph.json`, `graph.json.tmp`, `*.test`, `*.out`, `*.tmp`, and binary outputs. Prevents test-leftover SQLite files from being inadvertently included in checkpoints.

## [0.9.7-patched36] - 2026-03-08

### Fixed

- `FlatGraph.wouldCreateCycle`: budget exhaustion now returns `true` (conservative reject) instead of `false` (silent permit), matching `state.wouldCreateCycle` behaviour introduced in patched23
- `state.AddEdge`: idempotent re-add of an existing edge now returns `nil` immediately, avoiding a spurious cycle-check and (in `"warn"` mode) a false-positive `WARN` log on every re-add to a graph that already contains cycles
- `state.AddNode`: re-typing an existing node now removes it from the old type's index entry before inserting into the new one, matching `FlatGraph.addNodeLocked` and preventing stale type-index entries
- `handleTenantGraphPath`: added missing `from`/`to` empty-string validation (present in `handleTenantGraphShortestPath` and `handleTenantGraphPathExists` but absent here)
- `handleTenantGraphCommonNeighbors`: `count` in the response now reflects the post-guard slice length rather than the raw pre-strip length, so it cannot disagree with `len(common)` when `guardSlice` drops a cross-tenant node
- `AdaptivePersister`: wired `WriterEnter`/`WriterExit` into `updateGraph` and `removeGraph` in `server.go`; the adaptive interval was previously always fixed at 500ms because the writer count was never updated
- `IndexedGraph.Save` / `FlatGraph.Save`: partial `.tmp` file now removed on `os.WriteFile` failure
- `TestAtomicCounters_ImplicitNodeCreationViaAddEdge`: removed duplicate `NodeCount` assertion whose error message incorrectly claimed to test `NodeCountForTenant("")`

### Notes

- `AdaptivePersister` is flagged for future deprecation: it is only instantiated on the JSON filestore path (`storeHasEdgeTable == false`); all SQLite deployments never start it. If the JSON filestore is removed, `AdaptivePersister` should be deleted with it.

## [0.9.7-patched35] - 2026-03-08

### Fixed
- `FlatGraph.GetNodeInfo`: `Entity` field now correctly strips the tenant prefix
  for prefixed node IDs (e.g. `"0001@items:42"` → `Entity: "items"`); previously
  returned `"0001@items"`.
- `FlatGraph.addEdgeLocked`: auto-created nodes now route through `addNodeLocked`,
  enforcing `ErrMalformedNodeID` for malformed `@`-containing IDs; previously
  the check was silently bypassed.
- `FlatGraph.HasCycle`: replaced unbounded recursive DFS with the iterative
  three-colour frame-stack implementation from `pkg/graph/state`, eliminating
  the goroutine stack-overflow risk on deep graphs.
- `server.cascadeDelete`: removed redundant `persister.MarkDirty()` call at end
  of function; `removeGraph` already calls it per node.

### Tests
- `TestContract_GetNodeInfo_PrefixedNode`: contract test (both implementations)
  verifying `Entity` is stripped of tenant prefix.
- `TestContract_AddEdge_MalformedNodeID_Rejected`: contract test verifying
  `ErrMalformedNodeID` is returned when either `AddEdge` endpoint is malformed.

### Docs
- Corrected five stale comments across `flat_graph.go`, `state/state.go`, and
  `persister.go`: false claim about per-tenant counter machinery, stale
  `graphState` type name (twice), reversed HasCycle provenance, and overly
  narrow `MarkDirty` usage note.

## [0.9.7-patched33] - 2026-03-08

### Changed
- `FlatGraph`: `NodeCountForTenant` and `EdgeCountForTenant` are now O(1) via per-tenant counters (`nodeCounters`/`edgeCounters map[string]int`, keyed by tenant prefix, protected by the existing mutex). Previously these were O(N) linear scans across the entire node map.

### Fixed
- `FlatGraph`: node counter increments added to `addEdgeLocked` for nodes created implicitly by `AddEdge` (the implicit-creation path previously bypassed `addNodeLocked` and left `nodeCounters` stale).
- Counter maps are reset correctly in `Clear` and `Load`.

### Added
- Three new contract tests covering both implementations (6 subtests): node add/remove counter accuracy, edge add/remove counter accuracy (including `RemoveEdge` and `RemoveNode` cascade paths), and counter reset on `Clear`.

## [0.9.7-patched32] - 2026-03-08

### Fixed
- `FlatGraph.AddNode` was not delegating to `addNodeLocked`; it duplicated the logic without the malformed-ID guard added in patched31. Collapsed into a single delegation so the guard fires from all call paths.
- `FlatGraph.AddNode`: now returns `ErrMalformedNodeID` for IDs containing `@` without a valid `XXXX@` prefix, matching `IndexedGraph` behaviour.
- `FlatGraph.AddEdge` / `addEdgeLocked`: now returns `ErrCrossTenantEdge` when source and target carry different non-empty tenant prefixes, matching `IndexedGraph` behaviour.

### Added
- Four new contract tests covering both implementations (8 subtests): malformed node ID rejection, valid prefixed ID acceptance, cross-tenant edge rejection, and same-tenant edge acceptance.

## [0.9.7-patched31] - 2026-03-08

### Fixed
- `FlatGraph`: `NodeCountForTenant`, `EdgeCountForTenant`, `GetAllNodesForTenant`, and `GetNodesByTypeForTenant` were ignoring the tenant prefix and returning data across all tenants. All four methods now filter by prefix, matching `IndexedGraph` semantics.
- `FlatGraph.UpdateFromEntityForTenant`: was building unprefixed node IDs (`entity:id`) regardless of the tenant parameter. Now correctly uses `tenant.NodeID(tenantID, ...)` so nodes are stored with the `XXXX@entity:id` prefix.
- Removed stale `var _ = tenant.NodeID` workaround; `tenant` package is now genuinely used.

### Added
- Six new tenant-isolation contract tests covering both `IndexedGraph` and `FlatGraph` (12 subtests total): `NodeCountForTenant`, `EdgeCountForTenant`, `GetAllNodesForTenant`, `GetNodesByTypeForTenant`, empty-prefix rejection, and `UpdateFromEntityForTenant` prefix correctness.

## [0.9.7-patched30] - 2026-03-08

### Tests

- **`graph_contract_test.go` — dual-implementation contract suite**
  (`pkg/graph/graph_contract_test.go`) — 63 test functions, each running
  against both `IndexedGraph` and `FlatGraph` via a shared `graphImpls`
  constructor table, for 126 subtests total. `FlatGraph` was added in
  patched29 and made the default implementation without any functional test
  coverage; this suite closes that gap entirely.

  Coverage spans every shared contract surface: construction and empty-graph
  invariants; `AddNode`/`RemoveNode` including type-index maintenance;
  `AddEdge`/`RemoveEdge` including idempotence, `ErrEdgeAlreadyExists`,
  implicit node creation, and reverse-index hygiene; `GetNeighbors` and
  `GetIncomingEdges` including copy-independence; counter consistency after
  mutations and idempotent adds; `GetDegree`; `GetNodeInfo`; `GetNodesByType`
  and `GetAllNodes`; `FindPath` (chain, self-path, max-depth, no-path,
  absent-node); `PathExists` (found, self, not-found); `CommonNeighbors`;
  `HasCycle`; all three cycle detection modes (`ignore`, `warn`, `error`)
  including self-loop and DAG; `Clear` including rebuild-after-clear;
  `Save`/`Load` round-trip (full, empty, missing file); five
  `UpdateFromEntity` scenarios (create, multi-ref, ref change, idempotent,
  ref removal); two concurrency tests (mixed reads/writes, clear-during-reads);
  and five topology patterns (diamond, fan-out, fan-in, deep chain,
  disconnected components).

  All 188 tests in `pkg/graph/...` pass; race detector clean.

## [0.9.7-patched29] - 2026-03-08

### Added

- **`FlatGraph` — new single-adjacency-list graph implementation**
  (`pkg/graph/flat_graph.go`) designed for single-tenant use. Each logical
  tenant gets its own `FlatGraph` instance; isolation is structural rather than
  enforced by string prefix guards. Core data structure is a single
  `map[string]*nodeRecord` where one pointer dereference yields the node's type,
  all outgoing edges, and all incoming edges — no parallel maps, no prefix
  construction or validation on the hot path.

  Benchmark results vs `IndexedGraph` (1k nodes, 4 edges/node):

  | Operation | IndexedGraph | FlatGraph |
  |---|---|---|
  | `AddNode` | ~4500 ns, 7 allocs | ~2200 ns, 5 allocs |
  | `AddEdge` | ~810 ns, 5 allocs | ~365 ns, 3 allocs |
  | `RemoveNode` | ~1500 ns, 7 allocs | ~575 ns, 4 allocs |
  | `GetNeighbors` | ~880 ns | ~870 ns |
  | `FindPath` | ~884 ns, 5 allocs | ~758 ns, 4 allocs |
  | Build 1k×4 | 2.1 MB, 24k allocs | 1.1 MB, 10k allocs |

  `FindPath` uses a single `map[string]bfsEntry` (merged parent+depth) with a
  pre-allocated queue to minimise allocations. `FlatGraph` satisfies the full
  `Graph` interface; the `*ForTenant` methods accept the prefix parameter to
  satisfy the interface but ignore it — the graph is already scoped to one
  tenant.

- **Benchmark suite** (`pkg/graph/bench_test.go`) comparing `IndexedGraph` and
  `FlatGraph` across `AddNode`, `AddEdge`, `GetNeighbors`, `GetNodesByType`,
  `RemoveNode`, `FindPath`, and a full build benchmark.

### Changed

- **`FlatGraph` is now the default graph implementation.** `GraphMode` default
  changed from `"indexed"` to `"flat"` in `pkg/config/config.go`. The value
  `"indexed"` remains valid and selects `IndexedGraph` explicitly, enabling
  side-by-side benchmarking. (`pkg/config/config.go`, `cmd/olu/main.go`)

## [0.9.7-patched28] - 2026-03-08

### Changed

- **Server layer fully decoupled from `*graph.IndexedGraph`** — All type
  assertions against the concrete graph type have been removed from
  `pkg/server/handlers.go`, `pkg/server/graph_tenant_handlers.go`, and
  `pkg/server/server.go`. Every handler now calls through the `graph.Graph`
  interface directly. A dead fallback branch in `addGraphJSONToZip` (which
  emitted a "not available" stub for non-`IndexedGraph` implementations) has
  been deleted; the function now unconditionally uses the interface.

- **`sulpher.Executor` accepts `graph.Graph`** — The `graph` field and both
  constructors (`NewExecutor`, `NewExecutorForTenant`) now take `graph.Graph`
  instead of `*graph.IndexedGraph`. The two type assertions in `server.go` that
  existed solely to satisfy the old concrete-type parameter are gone; the graph
  is passed directly. (`pkg/sulpher/executor.go`, `pkg/server/server.go`)

- **Sulpher test helpers return `graph.Graph`** — `setupTestGraph`,
  `buildChainGraph`, and `buildDenseGraph` return the interface rather than the
  concrete type, so tests do not propagate a concrete-type dependency to callers.
  (`pkg/sulpher/executor_test.go`, `pkg/sulpher/gaps_test.go`,
  `pkg/sulpher/guardrail_test.go`)

## [0.9.7-patched27] - 2026-03-08

### Fixed

- **`SaveIndex`/`LoadIndex` dead code deleted** — Both methods were unreachable
  (no callers outside `graph.go` itself) and carried a latent bug: `SaveIndex`
  wrote all index keys including `relationship:*` entries, while `LoadIndex`
  silently skipped them, making a roundtrip lossy. Removed both methods, removed
  the two calls from `TestSaveLoad_ComplexGraph` (the test still exercises the
  correct `Save`/`Load` path), and deleted `TestLoadIndex_CorruptJSON`.
  (`pkg/graph/graph.go`, `pkg/graph/graph_intensive_test.go`, `pkg/graph/graph_test.go`)

- **`UpdateFromEntityForTenant` relationship-rename is now atomic** — The
  RemoveEdge+AddEdge path (taken when updating an edge to a new relationship
  label) had no rollback: if the second `AddEdge` failed (e.g. `ErrCycleDetected`
  in `"error"` mode), the edge was silently dropped. The old relationship label is
  now snapshotted before `RemoveEdge`; on failure, a best-effort restore
  re-adds the original edge. Restore failure is logged at `[WARN]`.
  (`pkg/graph/graph.go`)

- **`sliceContains` replaced with map-set deduplication in the type and
  relationship indexes** (`pkg/graph/state/state.go`) — The internal `index`
  field changes from `map[string][]string` to `map[string]map[string]struct{}`.
  Membership checks in `AddNode` (type dedup) and `AddEdge` (relationship-key
  dedup) become O(1) instead of O(n). The `Snapshot()` serialisation path
  converts back to `map[string][]string` for JSON, so the on-disk format is
  unchanged. The now-unused `sliceContains` helper is deleted.

- **`RemoveNode` type-index cleanup is now O(types\_per\_node)** — Previously,
  `RemoveNode` scanned the entire index (all type and relationship keys) to evict
  the node, an O(K×N) operation. A new `nodeTypes map[string]map[string]struct{}`
  reverse map records every type key a node is indexed under; `RemoveNode` now
  uses it for a targeted O(1)-per-type cleanup instead of a full scan.
  (`pkg/graph/state/state.go`)

- **`Graph` interface widened** — The interface previously listed 13 methods,
  leaving 13+ public methods of `IndexedGraph` (including `NodeCount`,
  `EdgeCount`, `PathExists`, `CommonNeighbors`, `GetDegree`, `GetNodeInfo`, and
  all four tenant-scoped query methods) only reachable via `s.graph.(*graph.IndexedGraph)`
  type assertions in the server layer. All public graph-operation methods are now
  in the interface, eliminating every type assertion. A compile-time satisfaction
  check (`var _ Graph = (*IndexedGraph)(nil)`) is added.
  (`pkg/graph/graph.go`)

### Tests

- **`TestUpdateFromEntityForTenant_RollbackOnSecondAddEdgeFailure`** — Verifies
  the relationship-rename path (same target, different label) succeeds and
  replaces the label correctly. (`pkg/graph/graph_test.go`)

- **`mockGraph` in `persister_test.go` extended** — Mock updated to implement
  the widened `Graph` interface; all new methods stub to zero values.
  (`pkg/graph/persister_test.go`)

---

## [0.9.7-patched26] - 2026-03-08

### Fixed

- **`EdgeCount()` is now O(1)** — Previously iterated every node's adjacency map
  (O(N+E)); now delegates to `sumCounter(&s.edgeCounters)`, the same atomic counters
  already maintained incrementally by `AddEdge`/`RemoveEdge`. Behaviour is identical;
  a negative-guard (matching `EdgeCountForTenant`) is added for defensive completeness.
  (`pkg/graph/state/state.go`)

- **`FindPath` self-path is now explicit** — `FindPath(x, x, d)` previously worked
  by accident (BFS dequeued `from`, saw `current == to` immediately, path-reconstruction
  loop exited trivially). It now returns `[]string{from}` via an early guard, consistent
  with `PathExists(x, x, d)` which already had an explicit `from == to` check. Doc
  comment updated to document the behaviour. (`pkg/graph/state/state.go`)

### Tests

- **`TestEdgeCount_UsesAtomicCounters`** — Verifies `EdgeCount()` against a ground-truth
  adjacency traversal across two implicit tenants, and checks that `RemoveEdge`
  decrements correctly. (`pkg/graph/state/state_test.go`)

- **`TestFindPath_SelfPath`** — Confirms `FindPath(x, x, d)` returns `[x]` and that
  the result is consistent with `PathExists(x, x, d)` returning `(true, 0, nil)`.
  (`pkg/graph/state/state_test.go`)

---

## [0.9.7-patched25] - 2026-03-08

### Tests

- **`NewAdaptivePersister` constructor now covered** — Two tests added:
  `TestNewAdaptivePersister_ReturnsInitialisedPersister` (field value assertions)
  and `TestNewAdaptivePersister_StartStopRoundTrip` (channel wiring). Previously
  all persister tests used the internal `newTestPersister` helper, leaving the
  public constructor at 0% coverage. (`pkg/graph/persister_test.go`)

### Documentation

- **`GRAPH_API.md` stale content removed** — Eliminated historical development
  artefacts that had become misleading: the "Existing Endpoints" / "New
  rserv-Compatible Endpoints (v0.7.1)" split merged into a single endpoint table;
  "Not yet implemented: Full-text search / Field search" note removed (full-text
  search is implemented at `GET /api/v1/search`); "Variable-length patterns (Phase
  3)" comment label replaced with "Variable-length patterns"; `## Advanced Features
  (v0.8.0)` version label stripped; rserv compatibility section reworded from
  aspirational to factual. (`docs/GRAPH_API.md`)

- **`GRAPH_INVARIANTS.md` updated for patched22 state sub-package** — All references
  to the old `addNodeLocked` / `addEdgeLocked` / `removeEdgeLocked` helper names
  replaced with `AddNode` / `AddEdge` / `RemoveEdge`; raw field references (`g.adjacency`
  etc.) replaced with plain descriptions; version references updated from `v0.9.8`
  to `patched22`; `RemoveEdge` invariant table gained the reverse-map cleanup row
  (bug fixed in patched23). (`docs/GRAPH_INVARIANTS.md`)

- **`OPTION_B_SPEC.md` status updated** — Status header changed from "Deferred.
  Not yet implemented." to "Implemented in patched22." with a note that the
  document's future-tense language is historical design prose.
  (`docs/OPTION_B_SPEC.md`)

- **`state.go` doc comments on empty-prefix methods corrected** — `NodeCountForTenant`,
  `EdgeCountForTenant`, and `AllNodesForPrefix` previously documented "An empty
  prefix returns all" without caveat. Comments now note that `IndexedGraph` rejects
  empty-prefix calls before these methods are reached, so external callers should
  not rely on the all-tenant / all-nodes fallback.
  (`pkg/graph/state/state.go`)

### Housekeeping

- **`TODO.md` deleted** — All 15 tracked items (5 bugs, 8 test gaps, 2 doc items)
  resolved across patched23–patched25. No open items remain.

---

## [0.9.7-patched24] - 2026-03-08

### Fixed

- **`wouldCreateCycle` conservative on DFS budget exhaustion** — When the cycle-check
  DFS visited `cycleCheckLimit` nodes without finding a cycle, the old code returned
  `false` (edge permitted). It now returns `true`, conservatively rejecting the edge
  rather than risking an undetected cycle. A `[WARN]` is logged when budget is hit.
  (`pkg/graph/state/state.go`)

- **`loadFromData` error condition inverted** — The condition guarding which edge errors
  should be skipped during JSON graph reload was inverted: unexpected errors (e.g.
  `ErrCrossTenantEdge`) were silently swallowed, while expected idempotent errors
  (`ErrEdgeAlreadyExists`, `ErrCycleDetected`) caused the load to abort. Fixed to
  skip only the expected idempotent errors and log unexpected ones at `[WARN]`.
  (`pkg/graph/graph.go`)

- **`RemoveEdge` reverse-map cleanup unconditional** — `RemoveEdge` only cleaned up
  `s.reverse[to]` when the entry already existed, leaving the reverse index
  inconsistent in partially-corrupt graphs. It now initialises the entry if absent
  before deleting from it. (`pkg/graph/state/state.go`)

- **`NewIndexedGraphWithCycleDetection` logs invalid mode** — An unrecognised cycle
  mode (e.g. `"Error"` instead of `"error"`) was silently coerced to `"ignore"`.
  It now emits a `[WARN]` log before defaulting, making call-site typos visible.
  (`pkg/graph/graph.go`)

- **Cross-tenant data leakage via empty tenant prefix** — `NodeCountForTenant`,
  `EdgeCountForTenant`, `GetAllNodesForTenant`, and `GetNodesByTypeForTenant` all
  accepted an empty prefix and returned data across all tenants, bypassing the
  tenant isolation boundary. All four now return `ErrTenantRequired` (HTTP 400) on
  empty prefix; the two node-listing methods additionally log `[WARN]` flagging a
  possible cross-tenant exfiltration attempt. Server handlers and the Sulpher
  executor updated accordingly. Tests for the old all-tenant escape-hatch behaviour
  replaced with negative assertions confirming rejection. (`pkg/graph/graph.go`,
  `pkg/server/graph_tenant_handlers.go`, `pkg/sulpher/executor.go`,
  `pkg/graph/graph_tenant_test.go`, `pkg/server/graph_tenant_supplemental_test.go`)

### Tests

- **Regression tests for all five patched23 bug fixes** — One test per bug,
  targeting the exact failure mode that the fix addresses:
  - `TestCycleDetection_BudgetExhaustion` — sets `cycleCheckLimit = 3`, builds a
    4-node chain, verifies the closing edge is rejected via `ErrCycleDetected`.
  - `TestLoadFromData_CrossTenantEdgeRejectedAndLogged` — saves a JSON graph
    containing a cross-tenant edge; verifies it is absent after reload and edge
    count is 1 not 2.
  - `TestCycleDetection_InvalidModeDefaultsToIgnore` — passes `"Error"` to the
    constructor; verifies cycles are permitted (mode coerced to `"ignore"`) and
    includes a sanity sub-check that the same cycle is rejected under `"error"`.
  (`pkg/graph/graph_test.go`)

- **Counter validation after concurrent mixed operations** —
  `TestConcurrent_MixedOperations` now calls `assertCountersMatchAdjacency` after
  the goroutine wait, catching counter corruption that leaves the graph structurally
  intact but with wrong O(1) counts. (`pkg/graph/graph_intensive_test.go`)

- **`UpdateFromEntityForTenant` error path** —
  `TestUpdateFromEntityForTenant_DuplicateEdgeTargetPropagated` verifies that a
  document with two fields referencing the same target propagates `ErrDuplicateEdgeTarget`
  and leaves the graph unchanged. (`pkg/graph/graph_test.go`)

- **`Save` write-failure path** — `TestSave_WriteFailure` verifies that saving to an
  unwritable path returns an error and leaves no partial file at the destination.
  (`pkg/graph/graph_test.go`)

- **Legacy load exact content** — `TestLoadLegacyFormat_ExactContentVerified` replaces
  the retired `TestLoadLegacyFormat` (which only checked `NodeCount() > 0`) with
  assertions on exact node count, exact edge count, specific edge relationships, and
  reverse-index consistency. (`pkg/graph/graph_test.go`)

- **`LoadIndex` corrupt JSON** — `TestLoadIndex_CorruptJSON` verifies that malformed
  JSON in the index file produces a non-nil error rather than a silent no-op.
  (`pkg/graph/graph_test.go`)

- **`GetNodeInfo` node ID without colon** — `TestGetNodeInfo_NodeIDWithoutColon`
  exercises the `len(parts) == 1` branch: a colon-free node ID must not panic and
  must return `Entity = ""`, `EntityID = 0`. (`pkg/graph/graph_test.go`)

- **`TestCommonNeighbors` compound guard tightened** — The second assertion
  (`common[0] != "c:1"`) was guarded by `len(common) > 0`, silently skipping the
  content check when the count was wrong. Rewritten as `if / else if` so both
  checks run at the appropriate time. (`pkg/graph/graph_test.go`)

- **`TestLoadLegacyFormat` retired** — Superseded by `TestLoadLegacyFormat_ExactContentVerified`
  and the existing malformed-ID regression tests. (`pkg/graph/graph_test.go`)

---

## [0.9.7-patched22] - 2026-03-06

### Changed

- **Option B: compiler-enforced graph invariants via `pkg/graph/state` sub-package** —
  The three locked helpers (`addNodeLocked`, `addEdgeLocked`, `removeEdgeLocked`) and
  all raw map state have been moved to a new `pkg/graph/state` sub-package. The fields
  `adjacency`, `reverse`, `index`, `nodeCounters`, and `edgeCounters` are now unexported
  fields of `state.State`; any direct write from `pkg/graph/graph.go` is a compile
  error. `IndexedGraph` delegates all state access through `g.s.*` method calls.
  The public API of `IndexedGraph` is unchanged; no callers outside `pkg/graph` require
  modification. The grep audit in `GRAPH_INVARIANTS.md` is now superseded by the
  compiler. (`pkg/graph/state/state.go`, `pkg/graph/graph.go`)

- **`TestCounterConsistency` migrated to T3 black-box form** — The test in
  `graph_test.go` previously ranged over `g.nodeCounters` and `g.edgeCounters`
  directly, which required white-box access to `IndexedGraph` internals. It has been
  rewritten to derive expected counts from `GetAllNodes`/`GetNeighbors` and assert
  them via `NodeCountForTenant`/`EdgeCountForTenant`. The original white-box check
  (ranging over the raw sync.Map) now lives in
  `pkg/graph/state/state_test.go::TestCounterConsistency_WhiteBox`. (`pkg/graph/graph_test.go`,
  `pkg/graph/state/state_test.go`)

---

## [0.9.7-patched21] - 2026-03-06

### Fixed

- **`Load` and `loadLegacy` bypassed all helper-layer invariants via direct map
  assignment** — Both load paths wrote directly to `g.adjacency`, `g.reverse`,
  and `g.index`, then called `rebuildCountersLocked` to compensate for the bypassed
  counter updates. This was a structurally brittle pattern: every time a new
  invariant was added to `addNodeLocked` or `addEdgeLocked` (malformed-ID
  rejection, type indexing, cross-tenant edge guards), the load paths had to be
  manually updated or they silently diverged. In patched17–19, three such
  divergences were found and patched individually. Replaced with a
  `loadFromData` helper that replays the saved snapshot through the helper methods:
  resets all state, then calls `addNodeLocked`/`addEdgeLocked` for every node and
  edge in the adjacency map, then restores type-index entries from the saved index
  (relationship entries are already rebuilt by `addEdgeLocked`; only entity-type
  entries need restoration). `rebuildCountersLocked` is now dead code and has been
  removed. Future invariants added to the helpers are automatically applied during
  load with no further changes required. (`pkg/graph/graph.go`)

- **`loadLegacy` wrote neighbour nodes directly to `g.adjacency` without
  validating their IDs** — A malformed `'@'`-containing neighbour ID (the edge
  target) in a legacy file bypassed `ErrMalformedNodeID` validation even after
  the source-node guard was added in patched19, because the neighbour creation was
  a separate direct map write. With the `loadFromData` refactor, `loadLegacy` now
  calls `addEdgeLocked` per edge, which in turn calls `addNodeLocked` for both
  endpoints — applying the malformed-ID check to edge targets as well as sources.
  Regression test: `TestLoadLegacy_MalformedNeighbourIDRejected`.
  (`pkg/graph/graph.go`)

### Changed

- **`rebuildCountersLocked` removed** — This helper existed solely to compensate
  for `Load` and `loadLegacy` bypassing the counter-update logic in
  `addNodeLocked`/`addEdgeLocked`. With both load paths now replaying through the
  helpers, `rebuildCountersLocked` is unnecessary. Its removal also eliminates the
  risk of it diverging from the helpers in a future change.
  (`pkg/graph/graph.go`)

- **`addNodeLocked` skipped type indexing for nodes already present in the
  adjacency map** — The type-index append was gated inside the `if !exists` block,
  so a node first created implicitly by `addEdgeLocked` (with an empty type) and
  later registered via `AddNode(id, "post")` was never added to
  `g.index["post"]`. `GetNodesByType` and `GetNodesByTypeForTenant` returned empty
  results for that entity type even though the nodes were live in the graph. This
  affected the hot path: `UpdateFromEntityForTenant` writes every *target* node
  with an empty type via `addEdgeLocked`; when that target entity is subsequently
  written directly, its type was silently unindexed. Fixed by moving the index
  append outside the `!exists` block, guarded by a `contains` deduplication check
  to prevent duplicate entries on repeated `AddNode` calls. Regression test:
  `TestTypeIndex_AddNodeAfterImplicitCreation`. (`pkg/graph/graph.go`)

- **`GetNodeInfo` returned a tenant-prefix-polluted `Entity` field for
  multi-tenant nodes** — `strings.SplitN(nodeID, ":", 2)` on `"0001@user:42"`
  gives `["0001@user", "42"]`, so `Entity` was set to `"0001@user"` instead of
  `"user"`. The 0.9.5 handler code strips the prefix at the HTTP response layer,
  but `GetNodeInfo` itself returned the wrong value, affecting any caller outside
  the tenant handler. Fixed by stripping the `XXXX@` prefix from the parsed entity
  name before populating `NodeInfo.Entity`. Tenant-0 nodes (no prefix) are
  unaffected. Regression test: `TestGetNodeInfo_EntityFieldStrippedForMultiTenant`.
  (`pkg/graph/graph.go`)

- **`AddEdge` in error-mode cycle detection created an orphan node when a
  self-loop was rejected for a previously non-existent node** — `addNodeLocked`
  created the node (counter incremented, adjacency entry written) before the cycle
  check ran. `wouldCreateCycle` returned true immediately for a self-loop, the edge
  was deleted, and `ErrCycleDetected` was returned — but the node remained as an
  unreachable isolate with a permanently leaked counter increment. Fixed by checking
  for `from == to` before any `addNodeLocked` call, so a rejected self-loop attempt
  has no side effects when the endpoints were not previously in the graph. Regression
  test: `TestAddEdge_SelfLoopLeavesNoOrphanNode`. (`pkg/graph/graph.go`)

- **`loadLegacy` bypassed `ErrMalformedNodeID` validation** — The legacy text
  format loader wrote directly to `g.adjacency` and `g.reverse`, bypassing
  `addNodeLocked` and its malformed-ID guard. Node IDs containing `'@'` without a
  valid uppercase-hex `XXXX@` tenant prefix (e.g. `"ab0z@user"`) were silently
  admitted. Once loaded, such nodes are invisible to the tenant isolation machinery
  and can produce ghost entries in tenant-scoped queries. Fixed by adding the same
  two-line guard (`strings.Contains(nodeID, "@") && NodeIDPrefix(nodeID) == ""`)
  that `addNodeLocked` uses, skipping malformed IDs rather than poisoning the
  graph. Regression test: `TestLoadLegacy_MalformedNodeIDRejected`.
  (`pkg/graph/graph.go`)

- **`addEdgeLocked` created implicit nodes without updating `nodeCounters` or the
  type index** — When `AddEdge` was called for nodes that did not yet exist,
  `addEdgeLocked` initialised the adjacency and reverse map entries directly,
  bypassing `addNodeLocked`. This left `nodeCounters` stale (e.g. three live nodes
  with a counter of zero) and omitted those nodes from the type index, making them
  invisible to `GetNodesByTypeForTenant`. The divergence was self-healing after a
  `Save` + `Load` cycle — `rebuildCountersLocked` walks the adjacency map and
  resets counters — but this caused the counter to jump on restart, giving
  different values before and after a reload of identical graph state. Fixed by
  replacing the two open-coded map-init blocks in `addEdgeLocked` with
  `addNodeLocked(id, "")` calls, which handle the counter increment, the type
  index entry, and the `ErrMalformedNodeID` guard. Regression test:
  `TestAtomicCounters_ImplicitNodeCreationViaAddEdge`. (`pkg/graph/graph.go`)

- **`RemoveNode` decremented `nodeCounters` unconditionally, producing a
  permanently negative counter** — The `atomic.AddInt64(..., -1)` decrement
  executed even when the target node did not exist, driving the counter below zero.
  `NodeCountForTenant` clamps negative values to zero, masking the corruption until
  the next `AddNode` — whose increment brought the counter to zero instead of one,
  leaving it one short for the remainder of the process lifetime. Fixed by returning
  early when the node is absent, making `RemoveNode` idempotent. Regression test:
  `TestAtomicCounters_DoubleRemoveNodeDoesNotUnderflow`. (`pkg/graph/graph.go`)

- **`HasCycle` used a recursive closure, risking goroutine stack overflow on deep
  graphs** — `wouldCreateCycle` (the write-path cycle check) had already been
  converted to an iterative BFS with a configurable node budget. `HasCycle`, called
  from the `/graph/stats` endpoint, still used a mutually-recursive closure
  (`hasCycleFrom` calling itself) — unable to benefit from tail-call optimisation
  and liable to overflow the goroutine stack on graphs with long chains (e.g. deep
  IoT device hierarchies). Rewritten as an iterative DFS with an explicit frame
  stack and a three-colour visited/in-path/done scheme, consistent with the pattern
  already used by `wouldCreateCycle`. (`pkg/graph/graph.go`)

- **`AdaptivePersister.save` had a TOCTOU on the dirty flag** — The save method
  read the dirty flag, called `graph.Save`, then cleared the flag with
  `dirty.Store(false)`. A `MarkDirty` call arriving between the return of
  `graph.Save` and the `Store(false)` was silently swallowed: the flag was cleared
  despite representing a mutation not included in the completed save. The missed
  dirty signal was only recovered when the next write triggered a new `MarkDirty`.
  Fixed by replacing `dirty.Store(false)` with
  `dirty.CompareAndSwap(true, false)`, which preserves any `MarkDirty` that
  arrived after the save completed. (`pkg/graph/persister.go`)

- **`AddEdge` accumulated duplicates in the relationship index** —
  Every call to `AddEdge` appended `from` to `g.index["relationship:X"]`
  unconditionally. When `syncGraphEdges` performs a delete-and-reinsert on
  every entity update, a node with a REF field accumulated multiple copies
  of itself in the relationship index, causing relationship-based lookups to
  return duplicates and leaking memory over time. A deduplication guard now
  checks before appending. (`pkg/graph/graph.go`)

- **`RemoveNode` performed an O(N) full-adjacency scan to clean up edges** —
  To remove incoming references to a deleted node, the method iterated every
  node in `g.adjacency` and `g.reverse`. Both maps already contain exactly
  the right sets: `g.reverse[nodeID]` is the set of nodes pointing to the
  deleted node, and `g.adjacency[nodeID]` is the set it points to. Cleanup
  now iterates only those sets — O(in-degree + out-degree) instead of O(N).
  (`pkg/graph/graph.go`)

- **`GetNodesByType` and `GetNodesByTypeForTenant` ignored the existing
  node-type index, and `GetNodesByTypeForTenant` leaked non-zero-tenant nodes
  to the empty-prefix (tenant-0) path** — Both methods scanned all entries in
  `g.adjacency` using `strings.HasPrefix`, giving O(N) lookups regardless of
  result size. `AddNode` already maintained `g.index[nodeType]`, but the index
  key was populated from `data["type"]` — a user-supplied entity field —
  rather than the entity schema name used at query time, so the index and the
  query used different keys and could never match. Separately, the empty-prefix
  fast-path in `GetNodesByTypeForTenant` returned all indexed nodes of the
  given type unconditionally, including nodes belonging to non-zero tenants
  whose IDs carry a `XXXX@` prefix; tenant-0 nodes are bare and must be
  distinguished from them. Fixed by: (1) indexing under the entity schema name
  throughout — `UpdateFromEntityForTenant` now passes `entity` (not
  `data["type"]`) to `AddNode`, and the server's direct `AddNode` call site in
  `server.go` was updated to match; (2) filtering the empty-prefix path to
  return only nodes whose IDs carry no `XXXX@` prefix. Both query methods now
  use `g.index[entityType]` — O(k) — and tenant isolation is preserved for all
  prefix values including empty. A dedicated regression test
  (`TestGetNodesByTypeForTenant_EmptyPrefixIsTenantZero`) guards this
  invariant. (`pkg/graph/graph.go`, `pkg/server/server.go`)

- **`RebuildGraph` silently dropped all `@REFS` edges** — The inline
  type-switch in `RebuildGraph` only handled single-REF map values; it never
  matched `[]interface{}` slices produced by `@REFS(…)` fields, so any entity
  with an `@REFS` field lost all those edges after a rebuild. Fixed by
  replacing the inline type-switch with `models.ExtractRefs` — the same helper
  used by `syncGraphEdges` — which handles single REFs, `@REFS` slices, and
  TSREF exclusion in one place. Regression test:
  `TestSQLiteStore_RebuildGraph_REFS`. (`pkg/storage/sqlite.go`)

- **Missing `relationship_name` index on tenant-scoped graph tables** —
  `graph_t%04X` tables created for non-zero tenants were missing the
  `idx_%s_rel` index on `relationship_name`, present on the default
  `graph_edges` table. Any query filtering by relationship name on a
  tenant-scoped graph performed a full table scan silently.
  (`pkg/storage/sqlite.go`)

- **`GET /graph/nodes/{id}/degree` returned 404 for adapted entities
  with no edges** — Adapted entities that have no REF fields are absent from
  the in-memory adjacency map; `GetDegree` therefore returned "not found".
  The handler now falls back to two `COUNT` queries against `graph_edges`
  (out-degree and in-degree separately). When both counts are zero the entity's
  existence is confirmed via `store.Get` before returning `{in:0, out:0,
  total:0}`, so non-existent nodes still correctly return 404. Applies to both
  tenant-0 and multi-tenant degree handlers. (`pkg/server/handlers.go`,
  `pkg/server/graph_tenant_handlers.go`)

- **`POST /graph/nodes/search` omitted adapted entities with no edges** —
  When a specific entity type is requested, the handler previously called
  `GetNodesByType` which queries the in-memory graph index — populated only
  from entities that have at least one REF field. Adapted entities with no
  REF fields were therefore invisible. The handler now prefers a direct
  `SELECT id FROM olu_X WHERE tenant_id = ?` query when the entity has an
  adapted table, guaranteeing complete results. Falls back to the graph index
  for non-adapted entities. Applies to both tenant-0 and multi-tenant node
  search handlers. (`pkg/server/handlers.go`,
  `pkg/server/graph_tenant_handlers.go`)

- **`UpdateFromEntityForTenant` observed partial state under concurrent
  readers** — The method acquired and released the graph lock multiple times
  (once per `AddNode`, once per stale-edge `RemoveEdge`, once per new-edge
  `AddEdge`). A concurrent reader between any two of those acquisitions could
  observe a node with no edges or a stale edge set. The entire sequence now
  runs under a single write lock via the `addNodeLocked`, `addEdgeLocked`, and
  `removeEdgeLocked` helpers. (`pkg/graph/graph.go`)

### Improved

- **`CommonNeighbors` deduplication changed from O(N²) to O(N)** —
  The previous implementation called `contains()` (a linear slice scan) for
  each candidate node, making deduplication quadratic for high-degree nodes.
  Replaced with a `map[string]bool` accumulator: O(1) membership test during
  collection, converted to a slice at the end. (`pkg/graph/graph.go`)

- **`FindPath` changed from per-frontier-node path allocation to
  parent-pointer BFS** — The previous BFS queued a full path copy
  (`make` + `copy` + `append`) for every node on the frontier. The new
  implementation records `parent[child] = parentNode` during traversal and
  reconstructs the path in a single pass once the target is found — one
  allocation at the end regardless of graph width. (`pkg/graph/graph.go`)

- **`Reference.ID` widened from `int` to `int64`** — SQLite returns
  integer IDs as `int64`; JSON unmarshal produces `float64`. Storing
  `int` required silent narrowing casts at every boundary and caused test
  assertions comparing against `int64` to fail. All call sites updated:
  `NewReference` now accepts `int64`, `IsReference` handles `int`, `int64`,
  and `float64` cases, and the two downstream callers that require `int`
  (`tenant.NodeID`, `store.Get`) cast explicitly.
  (`pkg/models/models.go`, `pkg/graph/graph.go`, `pkg/server/handlers.go`,
  `pkg/oql/executor.go`)

- **`syncGraphEdges` reduced from N round-trips to one prepared statement** —
  Previously issued one `fmt.Sprintf` + `ExecContext` per REF field. The
  function now collects all edge tuples first, skips the INSERT entirely when
  there are no REF fields, prepares the statement once, and executes once per
  edge. (`pkg/storage/sqlite.go`)

- **`loadEntitiesIntoGraph` now hydrates the graph from `graph_edges` on
  SQLite backends** — The previous implementation called `store.List` for
  every entity type, deserialising full entity JSON only to discard everything
  except REF fields: O(entities × JSON size) allocations at startup. A new
  `GraphEdgeScanner` optional interface (`pkg/storage/storage.go`) allows
  backends to stream rows directly from the edge table. `SQLiteStore`
  implements it: `ScanGraphEdges` issues a single `SELECT source_entity,
  source_id, target_entity, target_id, relationship_name FROM graph_edges`
  and calls `AddNode` + `AddEdge` per row — O(edges) with no JSON
  deserialisation. The jsonfile store does not implement the interface and
  continues to use the existing entity-deserialisation path unchanged.
  Future SQL backends (e.g. PostgreSQL) gain the fast path by implementing
  `GraphEdgeScanner`. (`pkg/storage/storage.go`, `pkg/storage/sqlite.go`,
  `cmd/olu/main.go`)

- **`RebuildGraph` now uses a prepared statement and batched inserts** —
  Previously called `fmt.Sprintf` inside the row loop and issued one
  `ExecContext` per edge. Now uses a single `PrepareContext` outside the loop
  and flushes rows in batches of 500 (within SQLite's binding limit).
  (`pkg/storage/sqlite.go`)

- **`NodeCountForTenant` and `EdgeCountForTenant` are now O(1)** —
  Previously performed an O(N) scan over `g.adjacency` on every call
  (including every `/graph/stats` poll). Now maintained as `sync.Map` of
  `*int64` counters, one entry per tenant prefix. All four adjacency-mutating
  methods (`AddNode`, `AddEdge`, `RemoveEdge`, `RemoveNode`) route through
  three locked helpers (`addNodeLocked`, `addEdgeLocked`, `removeEdgeLocked`)
  that own the counter updates. A `TestCounterConsistency` test cross-checks
  counters against the live adjacency map after every mutation.
  (`pkg/graph/graph.go`)

### Added

- **`models.ExtractRefs` — unified REF extraction helper** — Replaces
  scattered `IsReference` type-switch blocks in `syncGraphEdges` and
  `UpdateFromEntityForTenant`. Accepts any `interface{}` value and returns
  `[]*Reference`: handles a single REF map, a `[]interface{}` slice of REF
  maps (`@REFS`), and silently excludes TSREF and all non-REF values.
  Single point of containment for all map type-switching on REF fields.
  (`pkg/models/models.go`)

- **`models.NewReference` / `(*Reference).ToMap()` — typed REF
  constructor** — Eliminates raw `map[string]interface{}` construction in
  `evalLiteral`. `NewReference(entity, id)` returns a `*Reference`;
  `ToMap()` emits the canonical `{"type":"REF","entity":…,"id":…}` map
  required for JSON storage, ensuring round-trip consistency.
  (`pkg/models/models.go`, `pkg/oql/executor.go`)

- **`models.TSReference` / `IsTSReference` — typed TSREF** — Companion to
  `Reference` for timeseries links. `ExtractRefs` uses `IsTSReference` to
  exclude TSREF maps from graph edge creation without additional conditions
  at call sites. (`pkg/models/models.go`)

- **`@REFS(…)` in OQL INSERT** — Values clauses now accept an array of REF
  expressions via `@REFS(@REF('tag', 1), @REF('tag', 2), …)`. `evalLiteral`
  evaluates each argument as a `@REF`, collects the results into
  `[]interface{}`, and stores the field as a slice of REF maps.
  `syncGraphEdges` (via `ExtractRefs`) creates one `graph_edges` row per
  element. Integration test: `TestSQLiteStore_REFSGraphEdges`.
  (`pkg/oql/executor.go`, `pkg/storage/sqlite_test.go`)


## [0.9.6] - 2026-03-05

### Added

- **`@REF('entity', id)` in OQL INSERT** — Values clauses now accept
  structured REF expressions directly. The tsqlparser parses
  `@REF('author', 1)` as a `FunctionCall` with a `*ast.Variable`
  function; `evalLiteral` intercepts it and constructs
  `{"type":"REF","entity":"author","id":1}`. Since `store.Create`
  already calls `syncGraphEdges`, graph edges are created automatically
  with no additional work. Case-insensitive (`@ref` and `@REF` both
  work). Tests: `TestEngineInsertWithREF`, `TestEngineInsertWithREFMultipleRows`,
  `TestEngineInsertREFCaseInsensitive`.

- **`TODO.md`** — Design documentation for the next phase of OQL
  extensions, all syntax verified against tsqlparser before writing:
  - `BatchCreate` REST endpoint (bulk entity insert)
  - Multi-statement OQL execution separated by `;`
  - `@REFS(@REF(...), ...)` for array-of-REFs on a single field
  - `#graph` virtual table: `@NODE`, `@NEIGHBORS`, `@PATH`, `@COMMON`,
    and deferred `@BFS`/`@DFS`
  - `@TIMESERIES` / `@TS` document-to-timeseries reference stored as
    `{"type":"TSREF","timeline":N,"dims":[...]}`
  - `#timeseries` virtual table: range queries, `@LATEST`, `@AGG`,
    standard SQL aggregates (`COUNT`, `MIN`, `MAX`, `AVG`, `SUM`),
    bucketed aggregates via `GROUP BY @BUCKET('1h')`, and `@DIM` as
    both a selector and a value filter
  - `@TIMELINE(@NODE(...))` node-driven timeseries queries (deferred)


## [0.9.5] - 2026-03-05

### Added

- **Exhaustive tenant graph isolation test suite** — New file
  `pkg/server/graph_tenant_exhaustive_test.go` with 17 adversarial integration
  tests covering every graph handler surface under maximum-stress conditions:
  - Same entity type and numeric ID in both tenants (e.g. `post:1` in alpha
    and beta simultaneously) — the highest-risk collision scenario
  - All 12 handler surfaces tested individually: stats, nodeInfo, nodeDegree,
    out, in, path, neighbors, shortestPath, pathExists, commonNeighbors,
    nodeSearch, Sulpher sync and async
  - Multi-hop traversal (4-node chain): confirms traversal terminates at
    tenant boundary even when both tenants use identical node type names and IDs
  - Adversarial Sulpher `MATCH (n) RETURN n` query: confirms only the
    submitting tenant's nodes are returned (not the full 12-node union)
  - Sulpher typed queries and traversal field checks: `_id` fields in query
    results carry no `XXXX@` prefix
  - Sulpher async full cycle: submit to poll to result, with cross-tenant job
    visibility check (each tenant's `JobManager` is isolated)
  - Foreign-node leak test: any node existing only in tenant A must return 404
    or empty when queried via tenant B's routes

- **Coverage hardening across six packages** — New test files targeting
  previously uncovered production paths. Test count: 1835 → 2126 (+291).

  `pkg/timeseries/manager_gaps_test.go`:
  - `parseTenantDirName`: valid hex tenant dirs, invalid prefix, non-hex, empty
  - `DefaultManager.IsProvisioned`: before and after `Provision`
  - `DefaultManager.StoreFor`: error on unprovisioned tenant, lazy open,
    same-instance return on second call, dir-scan discovery across manager
    restart
  - `PebbleStore.UpdateTimeline`: name and retention mutation, error on missing ID
  - `PebbleStore.Stats`: empty store, store with populated timelines
  - `NewManager` dir scan: ignores non-directory entries and invalid names

  `pkg/sulpher/gaps_test.go`:
  - `NewExecutorForTenant`: tenant prefix and maxDepth wired correctly
  - `WithLogger`: returns same executor instance
  - `SetLimits` / `JobManager.SetLimits` / `JobManager.SetQueryTimeout`
  - `JobManager.failJob`: sets status and error message
  - DFS variable-length traversal: `*` (any hops), `*N` (exact), `*min..max`
    (range), context cancellation, `MaxVisitedNodes` limit enforcement
  - `applyConditions`: equality and inequality WHERE filtering
  - `compareForSort`: nil handling, numeric types, string fallback
  - `toFloat64`: all six type branches including string parse and unknown type
  - `compareNumeric`: all four operators across int, int64, float64, numeric
    strings; non-numeric type rejection
  - `compareValues`: `OpEq`, `OpNe`, numeric delegation, unknown operator, nil

  `pkg/jsonic/gaps_test.go`:
  - `MustRegister`: happy path; panic path via injected collision
  - `VerifyMatch`: short name (no full check), long name match, long name
    mismatch, unregistered atom with `needsFull=true`
  - `Count`, `FilterIndicesBool`, `SortIndicesByString` (asc and desc),
    `GroupSumIndices` (full and index subset), `GroupCountIndices` (full and
    subset), `ColumnStore.String`
  - `tokenToGoValue` (all branches): string, integer number, float number,
    true, false, object, array — exercised via `FilterExtractFromTokens`
  - `evalInt` with `int64` target and `OpIn` (hit and miss)
  - `evalFloat` with equality and `OpIn`
  - `evalInNumeric` with `int64` list items
  - `CoercePredicateValue`: all 17 input/field-type combinations including
    all error paths and unknown field type

  `pkg/cache/redis_miniredis_test.go`:
  - All six `RedisCache` methods: `Get`, `Set`, `Delete`, `DeletePattern`,
    `Exists`, `Close` — using `miniredis` in-process server (no real Redis)
  - `Set` with explicit and zero TTL (falls back to cache default)
  - `DeletePattern`: matching keys deleted, non-matching keys preserved
  - `Exists`: true for present key, false for absent
  - `NewRedisCache` connection failure path

- **`github.com/alicebob/miniredis/v2 v2.37.0`** added as a test dependency.

### Fixed

- **`pkg/timeseries/store.go` — `mustEncodeKey` panic replaced with proper
  error return** — `encodeEndKey` now propagates encoding errors from
  `EncodeKey` instead of calling `panic()`. Affected call sites: `QueryRange`,
  `RangeCount`, `Aggregate`, `Latest`. A malformed dimension vector that
  triggered encoding failure previously crashed the server process; it now
  returns a 422 error to the caller.

- **`cmd/olu/main.go` line 325 — deprecated `UpdateFromEntity` call
  updated** — Replaced `g.UpdateFromEntity(entity, id, data)` with
  `g.UpdateFromEntityForTenant(0, entity, id, data)` to use the explicit
  tenant-scoped form. `UpdateFromEntity` is still present but marked
  deprecated; this removes the last call site in the main binary.

- **`handleTenantGraphNodeInfo` — `Entity` field prefix leak** — `GetNodeInfo`
  parses `Entity` from the raw internal node ID, so it carries the `XXXX@`
  tenant prefix. The handler now strips the prefix from `info.Entity` before
  responding, matching the existing strip logic for `info.ID`, `info.Outgoing`,
  and `info.Incoming`.

### Changed

- **Graph layer promoted to production-ready in strict mode** — The graph
  layer is now safe to use in `OLU_TENANT_MODE=strict`. Documentation in
  `docs/GRAPH_API.md`, `README.md`, and `MANUAL.md` updated to reflect this.
  The previous "graph disabled in strict mode" notices have been removed.
  Tenant isolation is enforced at the graph snapshot layer (traversal sees
  only the requesting tenant's nodes), the handler layer (node IDs stripped
  of internal prefix), and the edge layer (cross-tenant edges detected and
  excluded with a WARN log).



## [0.9.4] - 2026-03-03

### Added

- **RangeAggregate — single-pass all-fields aggregate** — New `RangeAggregate`
  method on `Store` computing count, sum, avg, min, max for all seven numeric
  fields simultaneously in one Pebble iterator pass. New types:
  - `RangeAllQuery` — query shape for `RangeAggregate`; no `NumField` (covers
    all fields; distinct from `RangeNumQuery` to prevent silent ignored-field bugs)
  - `RangeAggregateResult` — `Count uint64`, `Sums/Avgs/Mins/Maxs [7]float64`,
    `Fields [7]bool` (which fields were present in at least one event)

- **RangeSum, RangeAvg, RangeMin, RangeMax, RangeCount** — Single-field
  convenience functions retained as syntax sugar. All now delegate to
  `RangeAggregate` internally; `rangeNumIter` removed. Benchmark data confirms
  the delegation costs nothing: all six functions are within measurement noise
  of each other on a 2,500-event dataset (776–834 ns/op, ~300 KB/op, ~10K
  allocs/op). The Pebble scan dominates; accumulating 7×4 float64s vs 1×2
  is immeasurable.

- **Range aggregate benchmarks** — `BenchmarkRangeSum`, `BenchmarkRangeAvg`,
  `BenchmarkRangeMin`, `BenchmarkRangeMax`, `BenchmarkRangeCount`,
  `BenchmarkRangeAggregate` in `range_agg_test.go`. Seed volume: 2,500 events,
  all seven fields populated. Baseline reading (Xeon 8581C, 2.10 GHz):
  single-field 777–834 ns/op, RangeAggregate 829 ns/op.

### Changed

- **Sugar functions delegate to RangeAggregate** — `RangeSum`, `RangeAvg`,
  `RangeMin`, `RangeMax`, `RangeCount` are now one-scan functions via
  `RangeAggregate`. `NumField` validation (0–6) is still performed before
  delegation. `RangeCount` returns the count of events carrying the specific
  field (`Fields[i]` true), not total event count.

### Fixed

- **`pkg/timeseries/store.go` line 712** — `fmt.Errorf` format string had one
  `%d` verb but two arguments (`len(q.Dims)`, `cfg.Dims`); corrected to
  `"ts: query dims %d out of range 1–%d (OLU-TS007)"`. Build failure in the
  `timeseries` package on all prior releases.

## [0.9.3] - 2026-03-03

### Added

- **Timeseries v0.3 implementation complete** — Full rewrite of the
  timeseries subsystem on a generic multi-dimensional key layout. Phase 1
  (storage layer) and Phase 2 (HTTP API layer) are production-ready.

  Storage layer (`pkg/timeseries/`):
  - `codec.go` — variable-length `[tid:2][d0..dN][ts:8]` big-endian key
    layout; compact flags+numerics+payload value encoding; `incrementKey`
    for exclusive upper-bound generation
  - `registry.go` — per-tenant JSON registry with atomic tmp+rename writes;
    dims immutability enforced across sessions; per-timeline and store-level
    retention both persisted
  - `store.go` — `PebbleStore` implementing the `Store` interface:
    `DefineTimeline`, `UpdateTimeline`, `Append`, `AppendBatch`,
    `QueryRange`, `Latest`, `Aggregate`, `Purge`, `Stats`, `TimelineStats`,
    `DefaultRetentionDays`, `SetDefaultRetentionDays`
  - `manager.go` — `DefaultManager` with lazy store open, tenant dir
    scanning on startup, idempotent `Provision`
  - `retention.go` — `RetentionWorker` goroutine with configurable interval
    and clean stop/wait lifecycle

  HTTP API layer (`pkg/server/ts_handlers.go`):
  - Timeline management: `POST /ts/provision`, `POST /ts/timelines`,
    `GET /ts/timelines`, `GET /ts/timelines/{id}`, `PATCH /ts/timelines/{id}`
  - Write: `POST /ts/events` (single), `POST /ts/events/batch`
  - Read: `GET /ts/events` (range query), `GET /ts/events/latest`
  - Aggregation: `POST /ts/aggregate` (scalar and time-bucketed)
  - Management: `GET /ts/retention`, `PATCH /ts/retention`, `GET /ts/stats`,
    `GET /ts/timelines/{id}/stats`
  - All routes tenant-scoped under `/api/v1/tenant/{id}/ts/`

- **Timeseries backend query limits** — Six new config fields enforced at
  the handler layer on every read and aggregate operation:
  - `OLU_TS_QUERY_TIMEOUT` (default 30s) — context deadline per read
  - `OLU_TS_MAX_QUERY_EVENTS` (default 10000) — caps returned events
  - `OLU_TS_MAX_SCAN_EVENTS` (default 500000) — aborts scan mid-flight
  - `OLU_TS_MAX_RANGE_DAYS` (default 366) — caps From→To window
  - `OLU_TS_MAX_BATCH_SIZE` (default 5000) — caps batch append size
  - `OLU_TS_MAX_RESPONSE_BYTES` (default 10 MB) — caps JSON response size

- **Timeseries test suite** — 78 new tests across 7 files:
  - `pkg/timeseries/store_test.go` (15) — store correctness, codec round-trips,
    purge, aggregation, scan-limit
  - `pkg/timeseries/codec_property_test.go` (14) — key/value property tests
    across full dims×value matrix and edge cases
  - `pkg/timeseries/registry_persist_test.go` (6) — close/reopen durability,
    dims immutability across sessions, atomic tmp-file write
  - `pkg/timeseries/concurrent_test.go` (5) — parallel appends, append+query,
    idempotent define, purge+append; all under `-race`
  - `pkg/timeseries/ts_stress_test.go` (4) — 5k bulk, 10-worker concurrent,
    100 bulk queries, mixed append+purge; skipped in `-short`
  - `pkg/server/ts_e2e_test.go` (9) — full HTTP lifecycle, multi-tenant
    isolation, partial-prefix queries, batch atomicity, ordering, aggregate
  - `pkg/server/ts_error_paths_test.go` (21) — every OLU-TS error branch
  - `pkg/server/ts_guardrail_test.go` (8) — all six backend limits enforced
  - `pkg/config/config_test.go` (+3) — guardrail env vars, defaults,
    conditional validation for `TimeseriesEnabled`

### Fixed

- **Timeseries purge false break** — `purgeTimeline` was stopping on the
  first non-expired event encountered, silently skipping all events after a
  gap. Fixed to `continue`, scanning the full key space.
- **QueryRange and Aggregate partial-prefix time leakage** — Partial-prefix
  scans (fewer dims than the timeline's declared count) leaked events outside
  the `From`/`To` window because the Pebble key bounds spanned multiple
  series that are not time-ordered relative to each other. Added a Go-side
  time filter for all partial-prefix queries.

### Changed

- **Timeseries batch max configurable** — `AppendBatch` limit raised from a
  hardcoded 5000 to the server-configured `TSMaxBatchSize` (default 5000).
  Tests updated accordingly.

## [0.9.2-rc1] - 2026-03-02

### Changed

- **Timeseries design v0.3** — Complete redesign of the timeseries
  subsystem from domain-specific (asset_id, sensor_id, trigger_type
  hardcoded in keys) to a generic multi-timeline store. Key changes:
  variable-length `[timeline_id:2][dims][ts:8]` key layout with uint64
  dimensions (1–5 per timeline); per-timeline retention replacing the
  single store-level policy; secondary index removed; domain-specific
  value fields replaced with generic numeric fields (up to 7 float64)
  plus a caller-defined opaque payload. Documented in
  `docs/TIMESERIES_DESIGN_V3.md`.
- **Timeseries doc consolidation** — `docs/TIMESERIES_DESIGN.md`,
  `docs/TIMESERIES_DESIGN_V2.md`, and `docs/TIMESERIES_IMPL_PLAN.md`
  retired. `docs/TIMESERIES_DESIGN_V3.md` is the single authoritative
  reference.
- **Storage estimate corrected** — Effective bytes/event updated from
  ~52 bytes (v0.1 estimate based on old value encoding) to ~30 bytes
  (v0.3 generic encoding with Zstd compression) in MANUAL.md.

## [0.9.2] - 2026-03-02

### Added

- **Hardware-aware query complexity gating** — EXPLAIN-based cost
  estimation for adapted full push-down. Complex queries (non-covering
  aggregates, temp B-tree sorts) are gated against hardware-specific
  thresholds. Three preset profiles (VPS, dedicated, bare-metal) and a
  runtime `CalibrateProfile()` function. New files:
  `complexity_estimator.go`, `complexity_profiles.go`,
  `complexity_planner.go`. 8 test functions / 26 subtests including
  result-correctness tests that verify both paths produce identical
  output.
- **Complexity benchmarks** — `BenchmarkComplexity_Generate`,
  `BenchmarkComplexity_Execute`, `BenchmarkComplexity_Full`,
  `BenchmarkComplexity_GoPath`, `BenchmarkComplexity_SQLPlan` in
  `complexity_bench_test.go`. Validated on Apple M1 (calibrated profile:
  blob=127, nonCovering=142, tempBTree1=161, tempBTree2=219) and
  container amd64 (VPS preset correctly gates regressions).
- **JOIN push-down exploration** — Design analysis documented in
  `QUERY_OPTIMISATION_PROGRESS.md`: relationship metadata requirements,
  backend-compatibility tension (three options evaluated), entity
  combination matrix (adapted-adapted, adapted-blob, blob-blob), and
  graph-layer boundary definition. No implementation planned for v0.9.x.

### Changed

- **Query planner doc v2.0** — `QUERY_PLANNER.md` updated: removed stale
  section 13 entries (GROUP BY/DISTINCT push-down, startup calibration)
  that are now implemented; added JOIN push-down entry.
- **Progress tracker** — Phase A4 added to `QUERY_OPTIMISATION_PROGRESS.md`
  with dependency graph, test count (1744), and full JOIN exploration
  section.

### Fixed

- **Tautological aggregate tests** — `aggEnv.run()` was passing the raw
  `env.store` to `ExecuteWithStore`, bypassing the `nonAggStore` wrapper.
  Both "Go path" and "push-down path" were effectively running push-down,
  making the comparison meaningless. The `WithWhere` and `WithWhereEquality`
  tests now correctly exercise the Go-side filtering pipeline.

### Changed

- **Golden database test infrastructure** — Converted 112 independent test
  functions (each copying the golden DB and creating a fresh env) into 4
  table-driven parent tests with shared environments. Read-only query tests
  now share a single store per group.
- **Fast blob entity seeding** — Rewrote `seedEquivalence` to use raw SQL
  in a single transaction with a prepared statement instead of 2000+
  individual `store.Create` calls. Seeding time: 3.7s to 0.4s.
- **SQLite PRAGMA tuning** — `synchronous=OFF` and `journal_mode=MEMORY`
  during golden database seeding (ephemeral file, durability irrelevant).
- **Total test suite speedup** — Previously-slow test groups reduced from
  7-11s each (frequently timing out at 15s) to 2.5-2.8s each.


## [0.9.0-rc20] - 2026-03-01

### Added

- **Schema evolution:** Automatic migration when JSON Schemas change
  after initial adapted table registration. `DiffAdaptedSpecs` computes
  a migration plan (added/dropped columns, index changes, type
  conflicts). `MigrateAdaptedTable` executes the plan in a single
  transaction: ALTER TABLE ADD/DROP COLUMN, index updates, metadata
  sync. Dropped column data is preserved in the `_extra` overflow
  column. Incompatible type changes and loss-of-additionalProperties
  are rejected with clear errors. 12 new tests (7 diff unit, 5
  migration integration).

### Changed

- **RegisterAdaptedTable:** Now calls `MigrateAdaptedTable` on schema
  hash mismatch instead of returning a hard error. Compatible changes
  (add/drop columns) proceed automatically; incompatible changes
  (type changes) still error.


- **B3 (columnar executor) deferred:** Cost-benefit analysis concluded
  that B4's inline predicate filtering already delivers the primary
  allocation win. Columnar rewrite would touch ~30% of the codebase
  for ~15-20% throughput gain on the Go fallback path only. Deferred
  with full design notes in QUERY_OPTIMISATION_PROGRESS.md.


## [0.9.0-rc19] - 2026-03-01

### Added

- **Prepared statement cache (A3):** LRU cache (`StmtCache`) for
  `*sql.Stmt` objects with generation-based eviction. Wired into
  `SQLiteStore` for `CountEntities`, `QueryWithPlan`,
  `AggregateQuery`, `ListWithFields`, and `QueryWithFields`.
  Default capacity 256 entries. 10 new tests.

- **Predicate push-down during tokenisation (B4):** Inline predicate
  evaluation in the jsonic token walk. New types: `FieldPredicate`,
  `PredicateSet`, `PredicateOp` (Eq/Neq/Lt/Lte/Gt/Gte/In/Like).
  `FilterExtractFromTokens` performs single-pass field extraction +
  predicate evaluation — rows that fail predicates never allocate a
  `map[string]interface{}`.

- **Predicate compiler:** `CompilePredicates()` decomposes OQL WHERE
  AST into a `jsonic.PredicateSet` (pushable AND-combined simple
  comparisons) plus a residual expression for unpushable terms (OR,
  NOT, IS NULL, BETWEEN, functions, subqueries).

- **FilterableStore interface:** Extension of `FieldQueryable` with
  `ListWithFieldsAndFilter` for inline predicate filtering during
  blob reads. SQLite implementation wired.

- **Executor B4 integration:** Go-path fallback now checks for
  `FilterableStore`, compiles WHERE predicates, and passes them into
  the tokenisation loop. Residual WHERE terms still evaluated in Go.

- **B4 test coverage:** 19 jsonic predicate/filter tests, 12
  predicate compiler tests.

### Changed

- **A-track complete:** All adapted-table optimisation phases (A1, A2,
  A3) are done.

- **B-track nearly complete:** B1, B2, B4 done. Only B3 (columnar
  executor) remains.


## [0.9.0-rc18] - 2026-03-01

### Added

- **PushFull planner decision:** New `PushFull` variant in
  `PushDecision` enum. The planner now detects adapted entities and
  returns `PushFull` or `PushAggregate` directly, skipping the
  `CountEntities` database round-trip entirely for adapted tables.

- **FieldQueryable interface:** Optional `FieldQueryable` interface
  (`ListWithFields`, `QueryWithFields`) for selective field
  extraction from blob entities. SQLite implementation uses jsonic
  tokenisation with atom-based key matching — extracts only requested
  fields without deserialising full JSON objects.

- **Executor FieldQueryable integration:** When the OQL query's
  SELECT list names specific fields (not `SELECT *`), the executor
  routes through `FieldQueryable` for both the Go-path fallback
  (`ListWithFields`) and the WHERE push-down path
  (`QueryWithFields`).

- **B2 test coverage:** 13 storage-level tests (type preservation,
  nested objects, arrays, null handling, long field names, missing
  fields, empty-fields fallback, comparative oracle vs `List` and
  `QueryWithPlan`). 7 executor E2E tests (basic select, WHERE,
  ORDER BY, TOP, push-down vs Go-path comparison, SELECT * bypass).

### Changed

- **Executor dispatch refactored:** Replaced cascading
  `fullPushed`/`aggregatePushed` boolean flags with a clean
  `switch` on `plan.pushed()` checks and a single `fetched` flag.
  Three strategies with graceful fallback: PushFull, PushAggregate,
  blob push-down, Go-path.

- **Planner adapted-entity fast path:** `Plan()` checks
  `AggregateQueryable.IsAdaptedEntity()` before calling
  `CountEntities`. For adapted entities, push-down is always
  beneficial regardless of row count.

### Fixed

- **ListWithFields empty-fields fallback:** Passing `nil` or empty
  field list to `ListWithFields` now correctly delegates to `List`
  (full deserialisation) instead of returning rows with zero fields.


## [0.9.0-rc11] - 2026-02-22

### Added

- **Adapted tables:** Schema-adapted table layouts that generate
  optimised SQLite tables from JSON Schema definitions instead of
  storing entities as JSON blobs. Benchmarks show 2.6x–124x speedup
  for common query patterns.

- **StorageDialect interface:** Backend-agnostic abstraction for
  adapted table operations. SQLite implementation provided;
  PostgreSQL can be added without changing the core layer.

- **queryfy v0.3.0 integration:** Replaced the internal
  `JSONSchemaValidator` with queryfy-based validation. Schema
  introspection via `SchemaBrowser` provides field metadata (type,
  format, precision, scale) used by the adapted table layer.

- **Decimal type support:** Fixed-point decimal fields with exact
  arithmetic, no floating-point approximation at any stage.

  - Schema declaration: `type: "string"`, `format: "decimal"` with
    `decimalPrecision` and `decimalScale` metadata.
  - Wire format: JSON strings (not numbers) to preserve exactness.
  - Validation: parse, precision bounds, scale bounds — rejects
    rather than silently truncates.
  - Storage (SQLite): scaled integer — value × 10^scale stored as
    `INTEGER`. Correct ordering, range queries, and indexing across
    the full signed range. Maximum 18 digits of precision (int64).
  - Storage (PostgreSQL, future): native `NUMERIC(p,s)`.
  - OQL aggregation: `SUM`, `AVG`, `MIN`, `MAX` use
    `shopspring/decimal` for exact Go-side arithmetic on SQLite.
    PostgreSQL will use native SQL aggregation.
  - Documentation: design doc (`docs/DECIMAL_TYPE_DESIGN.md`) and
    user guide (`docs/DECIMAL_TYPES.md`).

- **Adapted CRUD operations:** `adaptedCreate`, `adaptedUpdate`,
  `adaptedGet`, `adaptedList`, `adaptedGetInTx` with column-level
  partitioning (`PartitionData`), reassembly (`ReassembleData`), and
  decimal normalisation/denormalisation on write and read paths.

- **Adapted table registry:** `AdaptedRegistry` tracks which entities
  have adapted table specs, used by OQL executor for decimal-aware
  aggregation dispatch.

- **OQL decimal aggregation:** `Aggregator` detects decimal fields
  via `AdaptedRegistry` and dispatches to `DecimalAggregates` map
  (`shopspring/decimal`-based `SUM`, `AVG`, `MIN`, `MAX`) instead of
  float64 aggregation.

### Changed

- **Validation pipeline:** Validation now delegates to queryfy's
  compiled `ObjectSchema` with transform pipeline. Decimal fields are
  wrapped in a transform closure that captures precision and scale
  from field metadata.

### Dependencies

- Added `shopspring/decimal` v1.4.0 for exact decimal arithmetic.
- Updated `queryfy` to v0.3.0 for schema introspection and
  validation.


## Release candidate history

| RC | Date | Description |
|---|---|---|
| rc1 | 2026-02 | Initial 0.9.0 candidate. No detailed record survives. |
| rc2 | 2026-02 | No detailed record survives. |
| rc3 | 2026-02 | Query guardrail tests (scan limit, row limit, response size, timeout). Backup/restore drill test. Sulpher context cancellation, `MaxVisitedNodes`/`MaxResults` replacing hardcoded BFS limits. Slow query logging (>5 s) for OQL and Sulpher. Bug fixes: `ExecuteWithStore` not copying `QueryLimits`; export handler not checkpointing WAL. |
| rc4 | 2026-02-22 | Sentinel errors replace string matching for query limit violations (`oql.ErrScanLimit`, `oql.ErrResultLimit`, `sulpher.ErrVisitedNodeLimit`, `sulpher.ErrResultLimit`). Dedicated graph error codes OLU-GR005, OLU-GR006. Sulpher guardrail tests. 698 tests passing. Baseline for adapted tables work. |
| rc5 | 2026-02-22 | Adapted tables Phase 1: metadata layer (`AdaptedTableSpec`, `ColumnDef`, `ColumnType`), `StorageDialect` interface, SQLite implementation, adapted CRUD operations, registry. Schema introspection via `SchemaBrowser` (queryfy v0.3.0). |
| rc6 | 2026-02-22 | queryfy validation delegation: `JSONSchemaValidator` replaced with queryfy transform pipeline. 36 validation tests. |
| rc7 | 2026-02-22 | Decimal type design document (`docs/DECIMAL_TYPE_DESIGN.md`). |
| rc8 | 2026-02-22 | Decimal storage layer: `NormaliseDecimal`/`DenormaliseDecimal` on `StorageDialect`, adapted CRUD wiring, `shopspring/decimal` v1.4.0 dependency. 269 storage tests. |
| rc9 | 2026-02-22 | Decimal OQL aggregation: `DecimalAggregates` (SUM/AVG/MIN/MAX with exact arithmetic), executor integration via `AdaptedRegistry`. User documentation (`docs/DECIMAL_TYPES.md`). 285 OQL tests. Signed decimal design (N/P text prefix, unsigned implementation). |
| rc10 | 2026-02-22 | Simplified signed decimal text storage (N/P prefix, straight digits, accepted reversed intra-negative sort). |
| rc11 | 2026-02-22 | Switched to scaled integer storage. Decimal values stored as int64 (value x 10^scale) in INTEGER columns. Correct ordering for full signed range. Both docs updated. Changelog added. |
| rc12 | 2026-02-23 | `StorageDialect` interface expanded: `SupportsNativeDecimalAggregation()`, `ColumnType` signature formalised with precision/scale parameters. |
| rc13 | 2026-02-23 | Full adapted CRUD wiring. All Store operations branch on `AdaptedRegistry.IsAdapted()`. Auto-registration on schema load and startup sync. REST list handler routes adapted entities away from blob push-down. Read/write split: separate writer (1 conn) and reader (NumCPU conns, `query_only=ON`) pools. 16 CRUD tests, comparative benchmarks, E2E HTTP test. |
| rc14-15 | 2026-02-23 | Aggregate push-down for adapted tables. `AggregateQueryable` interface, `GenerateAggregateSQL` for native GROUP BY + aggregates against adapted columns. Three-tier executor dispatch. Decimal denormalisation from scaled integers. `hasScalarFunctions` guard. |
| rc16 | 2026-02-23 | Query optimisation roadmap phases 0 (abstraction cleanup), B1 (jsonic tokeniser), A1 (full SELECT push-down for adapted tables). 44 comparative push-down tests. Decimal MIN/MAX bugfix. |
| rc17 | 2026-02-23 | Checkpoint. 1645 tests, 16 packages, all passing. Roadmap document updated. |
| rc18 | 2026-03-01 | Phase A2: planner integration. `PushFull` decision type; planner owns adapted-entity routing (skips `CountEntities`); executor dispatch refactored from cascading booleans to strategy switch. Phase B2: `FieldQueryable` interface wired into executor (`ListWithFields`, `QueryWithFields`); jsonic selective extraction on blob reads; empty-fields fallback bug fixed; 20 new tests (13 storage, 7 executor E2E). |
| rc19 | 2026-03-01 | Phase A3: prepared statement cache (LRU, generation-based eviction, 10 tests). Phase B4: predicate push-down during tokenisation — inline filtering in jsonic token walk, predicate compiler (AST → PredicateSet + residual), FilterableStore interface, executor wiring. 31 new tests. 1706 total. |
| rc20 | 2026-03-01 | Schema evolution: automatic adapted table migration on schema change. DiffAdaptedSpecs, MigrateAdaptedTable (transactional ADD/DROP COLUMN, _extra data preservation, index sync). Type change rejection. 12 new tests. 1718 total. |
| patched1–16 | 2026-03-05 | Graph layer refactor: 20 tracked items completed across in-memory graph, SQLite sync, startup, and architectural integration. See [0.9.7] entry. |
| patched17 | 2026-03-06 | Graph layer audit. Identified four bugs: implicit node creation in `addEdgeLocked` bypassing counter/index updates; `RemoveNode` unconditional counter decrement; `HasCycle` recursive closure; `AdaptivePersister` dirty-flag TOCTOU. No code changes; produced bug report. |
| patched18 | 2026-03-06 | Fixed all four bugs from patched17 audit. Two regression tests added. Completed session tracking files (TRACKER.md, TODO.md, TS_PROGRESS.md) removed. |
| patched19 | 2026-03-06 | Four further graph layer bugs fixed: type index skipped for pre-existing nodes (Bug 1 — serious, hot path); GetNodeInfo tenant-prefix pollution in Entity field (Bug 2); self-loop rejection leaving orphan node in error mode (Bug 3); loadLegacy bypassing ErrMalformedNodeID (Bug 4). Four regression tests added. |
| patched20 | 2026-03-06 | Structural fix: Load and loadLegacy now replay through addNodeLocked/addEdgeLocked (Option C) rather than writing directly to the adjacency maps. rebuildCountersLocked removed. loadLegacy neighbour-node malformed-ID gap closed (Option A). Three regression tests added. |
| patched21 | 2026-03-06 | Option D: invariant documentation hardened. Struct comment expanded to list all five invariant classes with consequence-of-bypass. Each of the three helper methods now carries a full invariant table in its doc comment. docs/GRAPH_INVARIANTS.md added: authoritative audit reference with grep commands, exemption table (verified clean), load-path pattern note, and deferred Option B pointer. docs/OPTION_B_SPEC.md added: full implementation specification for the compiler-enforcement refactor. |
