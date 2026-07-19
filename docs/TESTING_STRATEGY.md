# xolu Test Suite — Narrative

## Overview

The test suite for xolu is co-located with the implementation: every package
under `pkg/` carries its own `*_test.go` files. The scope for the standard
test run and for coverage reporting is `./pkg/...`; the two entry points
(`cmd/xolu` and `cmd/iolu`) have no unit-testable logic and are excluded.

Tests are run with `-count=1` to suppress caching and ensure each run
reflects the current state of the codebase. The release run uses no `-short`
flag; the full suite including stress and comparative benchmark tests must
pass before a checkpoint zip is cut.

## Test categories

### Contract tests

Each major subsystem defines a contract test that exercises the public
interface exhaustively without reference to implementation details. These
are the primary correctness gate:

- `pkg/graph/graph_contract_test.go` — `FlatGraph` invariants: node/edge
  creation, cycle detection, tenant isolation, counter correctness.
- `pkg/storage/contract_test.go` — `Store` interface: CRUD, batch, FTS,
  pagination, versioning.
- `pkg/timeseries/contract_test.go` — `PebbleStore` interface: append,
  range query, aggregate, retention.
- `pkg/cache/cache_test.go` — `Cache` interface: get/set/delete/pattern,
  TTL, shard correctness.

### Integration and end-to-end tests

`pkg/server/` is the primary integration layer. Tests stand up a real
`Server` instance (with SQLite, in-memory graph, and in-memory cache) and
exercise the full HTTP stack:

- `e2e_test.go` — entity CRUD, REF embedding, OQL, export, search.
- `integration_test.go` — multi-tenant scenarios, commit endpoint, blob API.
- `graph_tenant_exhaustive_test.go` — adversarial tenant isolation: 12
  handler surfaces verified to prevent cross-tenant data leakage.
- `adapted_e2e_test.go`, `join_e2e_test.go` — adapted table and OQL JOIN
  push-down end-to-end coverage.
- `ts_e2e_test.go` — timeseries provision, append, query, retention through
  the HTTP layer.

### Adversarial tests

Several packages carry tests specifically designed to find edge cases that
correctness tests miss:

- `pkg/graph/adversarial_test.go` — race conditions, concurrent mutation,
  malformed node IDs, cross-tenant edge attempts.
- `pkg/oql/adversarial_oql_test.go` — malformed SQL, injection attempts,
  guardrail limits.
- `pkg/sulpher/executor_oc9_test.go` — OC9 specification compliance:
  variable scoping, path semantics, UNWIND, OPTIONAL MATCH.
- `pkg/storage/adversarial_server_test.go` — cache invalidation correctness
  under concurrent writes.
- `pkg/timeseries/adversarial_ts_test.go` — corrupt data, concurrent
  append/purge, scan limit enforcement.
- `pkg/server/ts_adversarial_test.go` — timeseries HTTP layer: sync
  handlers, provision/update/batch error paths, aggregate error branches,
  all nine `parseInterval` values, payload round-trip, tsStore error paths.
- `pkg/server/server_adversarial_test.go` — entity CRUD error paths, graph
  edge verification, Sulpher and OQL async lifecycle, dynConfigGuard disabled
  branch, handleVersion/handleSave/handleCreateSchema, concurrent read/write.
- `pkg/server/shutdown_test.go` — `Server.Stop()` with each optional
  subsystem live (rateLimiter, tsRetention, blobGC, blobSampler, dynWatcher,
  tsManager), all simultaneously with deadlock detection, shutdown ordering.
- `pkg/server/blob_adversarial_test.go` — all `blobStore_==nil` branches,
  `tenantForBlob` tenant-context path, `handleBlobUsage` with live sampler,
  S3 delete idempotency, head bucket/object not-found, RequireAuth paths.
- `pkg/server/graph_query_adversarial_test.go` — cycle detection via
  `NewFlatGraphWithCycleDetection("error")`, duplicate-edge rejection,
  Sulpher/OQL query result all branches (not-found, pending, completed, failed).

### Stress tests

Stress tests are `Test*` functions that call `t.Skip` under `-short`. They
run in the full release suite and are the primary signal for performance
regressions and concurrency bugs under load:

- `pkg/storage/stress_test.go` — bulk create/query/update/delete at 500,
  2000, and 5000 entity counts; concurrent worker scenarios.
- `pkg/storage/adapted_comparative_test.go` — blob vs adapted table
  performance comparison at the same sizes.
- `pkg/timeseries/ts_stress_test.go` — bulk append, bulk query, concurrent
  workers, mixed workload with purge.

### Slabbis cache integration tests

`pkg/cache/cache_slabbis_test.go` runs `RedisCache` against an in-process
`slabbis` instance (no external process required). This verifies the full
RESP2 command surface including `SCAN`-based `DeletePattern`, which is the
most complex cache operation and the one most likely to fail against a
non-Redis backend.

### Property and regression tests

- `pkg/timeseries/codec_property_test.go` — codec round-trip for all
  numeric types and edge cases (NaN, Inf, zero, max uint64).
- `pkg/sulpher/gaps_test.go` — known Sulpher gaps confirmed to return
  expected results (zero rows or null) rather than panicking.
- `pkg/storage/*_regression_test.go` — specific bugs fixed in past releases,
  kept to prevent reintroduction.

## Coverage rationale

Aggregate coverage is reported but not used as a hard gate. The codebase
contains several categories of intentionally low-coverage code:

- Error paths that require filesystem or network fault injection (blob GC,
  Pebble compaction errors, SQLite busy-timeout retries).
- Dead fallback branches retained for future backend support.
- Admin CLI (`cmd/iolu`) excluded from `./pkg/...` scope.

The meaningful coverage signal is per-package: any package falling below 65%
warrants review. Packages above 85% (cache, dynconfig, errors, graph, jsonic,
middleware, models, qs, tdigest, tenant, validation) are considered
well-covered.

## Release gate

A release checkpoint is only cut when the full test suite (`./pkg/...`,
no `-short`) passes with zero failures and zero skips. The `release.sh`
script enforces this: a non-zero test exit code aborts before the zip is
produced.
