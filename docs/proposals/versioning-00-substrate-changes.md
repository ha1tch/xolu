# xolu: changes that would remove the client-side workarounds in xoluver

Status: proposal, filed in the register as XOT228 to XOT240 (X1 to X13 here) · Basis: xolu v0.30.38 source, live experiments against a built v0.30.38 server, and the xoluver test suite

xoluver versions entities using only xolu's public API. Everything below is something xoluver does *because xolu does not*. Each item names the evidence, the change, and the xoluver code it would delete. Items are ordered by how much they matter.

## Summary

| # | Change in xolu | Kind | xoluver workaround it removes |
|---|----------------|------|-------------------------------|
| X1 | A filtered `SELECT` on a schema-registered type returns no rows once the table has more than 30 rows | **Bug** | The advice not to register a history schema (see "A hazard in xoluver's own documents") |
| X2 | OQL rejects tenant entities (the validator checks the default store) | **Bug** | `ErrTenantOQL`, reads by id for `CheckIndex`/`RebuildIndex`, the "fast path and safety net exclude each other" problem |
| X3 | `/commit` with `update.version` on a missing entity creates it instead of failing | **Bug** | The existence pre-check in `Save`, `ErrEntityRecreated` |
| X4 | An oversize `/commit` body answers `400 Invalid JSON`, not 413 (one handler skips the helper that already does it) | **Bug** | Part of the local size check's reason to exist |
| X5 | JSON numbers pass through float64: integers above 2^53 change | **Bug** | `MaxEntityID` (about 9 billion), the "send big integers as strings" rule |
| X6 | `/commit` has no `delete` | Missing | Tombstone-first deletion, five states, `ErrDeleteIncomplete`, `PendingDelete`, `ErrDeleted` |
| X7 | `/commit` cannot refer to the new version, the written document or the previous document, or ignore a row that exists | Missing | Sending the document 2 or 3 times, the baseline rule and its pre-read, the `base+1` assumption, 3 to 4 requests per save |
| X8 | No server-assigned, monotonic commit time | Missing | Writers' clocks, `OutOfOrder`, `BadTime`, the version-order walk |
| X9 | No unique or composite index on a schema | Missing | The `entity*1,000,000+version` id trick and its limits, stray rows, `Misplaced` |
| X10 | No way to forbid plain writes to a managed type | Missing | Bypass states (gaps, `CurrentMissing`, `Revived`, `UntrackedDelete`), the bypass lint |
| X11 | `fsm_walk` does not check that the machine belongs to the entity | Missing | The "pass the right machine" rule |
| X12 | /ts is served only on tenant routes | Limit | `Client.Tenant`, `ErrTenantRequired` |
| X13 | /ts cannot remove one event, is capped at 10,000 events per query, and its commit leg is not atomic | Limit | Orphans and duplicates needing an administrator, the 10,000-event rule |

If X1, X8 and X9 land, `AsOf` becomes one indexed OQL query on the history type, and the /ts index is no longer needed for it (see "What xoluver would look like").

## Bugs

### X1. Filtered `SELECT` on a schema-registered type returns no rows above 30 rows

**Evidence.** Register a JSON Schema for a type: xolu creates an adapted table with native columns, and `x-xolu-index: true` on a field adds a SQLite index (`pkg/storage/adapted.go:349`). Then:

| Rows in the type | `SELECT id, version FROM hc_version WHERE entity_id = 7` (20 rows match) |
|------------------|------|
| 20 | 20 rows |
| 100 | 0 rows |
| 500 to 10,000 | 0 rows |

The same predicates inside aggregates are correct and fast: `SELECT MAX(version) ... WHERE entity_id = 7` took 0.61 ms against 43.32 ms for the schemaless twin at 20,020 rows (`rows_scanned` 0 against 20,020). Row-returning queries also fail with `WHERE change_kind = 'save'`, `WHERE id = 7000010` and `WHERE version = 3`. Without a `WHERE` they work.

**Cause (confirmed from the debug log and the database file).** The planner logs `push=WHERE reason="count 10000 > threshold 30, pushing WHERE"` for these queries: it took the *blob* push-down path. For an adapted type the rows live only in the adapted table (`t0000_ndata_hb_version`, 20,021 rows); the blob table `t0000_nodes` holds none for that type. `pkg/oql/planner.go` falls to the blob logic with the comment "Adapted entity but not translatable — fall through to blob logic. (The adapted entity still has a blob row in the entities table.)" That comment is no longer true. The pushed `WHERE` runs against an empty table and returns nothing, while `rows_scanned` still reports the table size. Below the threshold the planner uses the Go path, which reads the adapted table and is correct. Not identified: why `isFullyTranslatable` rejects these simple projections.

**Change.** When an adapted entity is not fully translatable, take the Go path (or mirror rows into the blob table), never the blob push-down. The place is the fall-through at `pkg/oql/planner.go` ~line 290. Make simple projections with `WHERE`, `ORDER BY` and `TOP` translatable. Add a regression test with more than 30 rows.

**Why it matters.** Silent wrong answers: a query for an entity's history returns "none". For xoluver that would turn `AsOf` into "no history" and `CheckHistory` into a clean report for a history that exists.

### X2. OQL cannot query tenant entities

**Evidence.** Every OQL query on `/api/v1/tenant/<t>/oql/query` for a type that exists only under the tenant answers `400 XOLU-QL004 entity '<x>' does not exist`, in default and strict tenant modes and with a pre-registered tenant. `GET` by id and `POST /commit` work for the same entities.

**Cause.** `Engine.ExecuteWithStore` (`pkg/oql/oql.go:141-154`) calls `e.validator.Validate(stmt)` on one engine-wide validator whose entity list comes from the default store, then hands the tenant store to the executor.

**Change.** Validate against the store passed in: the validator already accepts an `EntityChecker` with `ListEntities` (`pkg/oql/validator.go:20`) and `SQLiteStore.ListEntities` exists (`sqlite.go:3615`), so a per-store check is a small step. Or skip name validation and let the executor report a missing type for that store. Test in default, strict and per-file tenant modes.

**Removes.** `ErrTenantOQL` and the three reads that fail on tenant routes (`ListVersions`, `CheckHistory`, the `AsOf` scan), the by-id history reads in `historyForCheck`, and the situation where a damaged index leaves `AsOf` with no fallback.

### X3. Compare-and-set on a missing entity creates it

**Evidence.** `POST /commit` with `update.version: 5` for an entity that was deleted answers 200 with `created: true` at version 1. `pkg/storage/sqlite.go` `saveInTx`: `if exists { ...CAS... } else { create path }` ignores `u.Version` on the create path.

**Change.** If `update.version` is present and the entity does not exist, fail with the conflict error (`current_version: 0`). Define `version: 0` as "must not exist" so create-only has an explicit spelling. Without a version, keep the upsert. `saveInTx` has two callers: `/commit` and the dxp entity adapter (`entity_dxp_adapter.go:345`), which updates an entity it has just read, so the change should not affect it.

**Removes.** The `GET` before every commit that exists only to avoid resurrecting a deleted entity, `ErrEntityRecreated`, and the one state xoluver cannot undo.

### X4. Oversize body answers `400 Invalid JSON`

**Evidence.** A `/commit` body over `XOLU_MAX_ENTITY_SIZE` (1 MiB) returns `400 XOLU-VL002 Invalid JSON in request body`.

**Cause.** xolu already has the right behavior in one place: `decodeJSON` (`pkg/server/handlers.go:1444` to `1451`) maps `http.MaxBytesError` to `413 XOLU-ST007`. The `/commit` handler (`handlers.go:2330`) decodes the body directly and answers `Invalid JSON in request body`.

**Change.** Make the `/commit` handler call `s.decodeJSON`. Separately consider a commit limit that is not the entity limit: a commit carries N documents.

**Removes.** Nothing alone; it makes the failure legible without a local size check.

### X5. Numbers pass through float64

**Evidence.** An integer field `9007199254740993` is stored and returned as `9007199254740992`. OQL returns ids as float64, so history-row ids above 2^53 lose digits. `UseNumber` appears only in the bal and dxp handlers (`v2_bal_handlers.go:258`, `v2_dxp_def_handlers.go:883`).

**Change.** Decode request bodies with `UseNumber` (or keep the raw JSON), encode `json.Number`, and return integer columns as integers in OQL results.

**Removes.** `MaxEntityID` (9,007,199,253 instead of about 9.2 trillion for the id scheme), the rule to send large integers as strings, and the float-exactness tests.

## Missing capabilities

### X6. `delete` in `/commit`

Add `"delete": {"entity", "id", "version"}` with compare-and-set, running in the same transaction as `append`, `fsm_walk` and `timeseries`, and reporting cascaded deletions. A deletion plus its history row then commits or fails as one.

**Removes.** The two-step tombstone-first protocol, `Delete`'s five states, `ErrDeleteIncomplete`, `PendingDelete`, and the `ErrDeleted` guard in `Save`. (A plain `DELETE` still bypasses; see X10.)

### X7. Templates and conditional appends in `/commit`

Let `append` rows and `timeseries` events refer to values the commit computes: `"$update.version"` (the new version), `"$update.data"` (the document written), `"$previous.data"` (the document it replaced, read in the transaction), `"$created.id"` (for a `create` operation), and `"on_conflict": "ignore"` for an explicit-id append whose id already exists.

**Removes.**
- The document sent twice, or three times with a baseline: the practical document limit rises from about 524,000 characters (349,000 on a first save) to the entity limit.
- The baseline rule: today `Save` reads the row for the base version to decide whether to write a baseline. `"on_conflict": "ignore"` plus `$previous.data` does it in the transaction.
- The `base+1` assumption and its post-commit check.
- Requests per save: 3 to 4 today (entity, history row, commit, plus the optional /ts leg) become 1.
- With a `create` operation, history can start at creation. Then "did not exist at T" is knowable for entities created that way, instead of "no record" for every instant before the first save.

### X8. A server-assigned, monotonic commit time

xolu already stamps `updated_at = CURRENT_TIMESTAMP` on entity rows (`sqlite.go`, in the update path) but at one-second resolution, and returns it nowhere. `pkg/xolutime` defines the sanctioned `ot.Now()`. Provide `"$now"` in appended rows and /ts events, return `committed_at` (nanoseconds) and a per-tenant monotonic `commit_seq` in the commit response, and guarantee `committed_at` never decreases per tenant.

**Removes.** Writers' clocks as the source of `saved_at`, the `OutOfOrder` and `BadTime` reports, and the rule that `AsOf` walks in version order and stops at the first version saved after `t`. For one entity, time order would equal version order. `commit_seq` would also allow a database-wide "as of this commit" read, which xoluver cannot offer today.

### X9. Unique and composite indexes on a schema

`x-xolu-index: true` creates one non-unique single-column index. `IndexDef.Unique` exists in `pkg/storage/adapted.go:156`, but no schema keyword sets it. Add `x-xolu-unique` and a composite form, for example `"x-xolu-index": [["entity_id", "version"]]` and `"x-xolu-unique": [["entity_id", "version"]]`.

**Removes.**
- The deterministic row id (`entity*1,000,000+version`), and with it the 999,999-version and entity-id limits.
- Stray rows, `Misplaced`, and `ErrCorruptHistory` for duplicates: xolu would refuse a second row for one (entity, version).
- Reads by the encoded id: history is read with an indexed query.

With X1 fixed, `SELECT TOP 1 ... WHERE entity_id = ? AND saved_at <= ? ORDER BY version DESC` runs on the composite index.

### X10. Forbid plain writes to a managed type

A schema or registration flag, for example `"x-xolu-write": "commit-only"`, that makes `POST`, `PUT`, `PATCH` and `DELETE` on the type fail with a clear code. Applying it to the entity type and to its history type closes the bypass.

**Removes.** The bypass states (`CurrentMissing`, gaps, `Revived`, `UntrackedDelete`), the planned bypass lint, and a large part of why `CheckHistory` exists. Direct writes to a history type (stray rows) become impossible.

### X11. `fsm_walk` checks the machine's `ref`

Refuse a walk when the machine's `ref` is not `update.entity:update.id`, as an opt-in or as the default. Today saving entity A while walking entity B's machine succeeds and moves B's machine (tested).

**Removes.** The caller's duty to pass the right machine. A later step could also let a type refuse plain saves while its machine is terminal; today a terminal machine does not stop a `Save` without a walk (tested).

## /ts

### X12. Serve /ts on the ordinary routes

/ts exists only under `/api/v1/tenant/<t>/ts/...`. Serving it for the default tenant too removes the coupling between the index and tenant routes. Together with X2 it makes the fast path and the safety net available together.

**Removes.** `Client.Tenant` as a requirement of the index, `ErrTenantRequired`.

### X13. Event removal, pagination, atomicity and retention

| Gap | Evidence | Change |
|-----|----------|--------|
| No removal of one event | xolu removes /ts events only by time-range purge | `DELETE` by exact dims and time, so a library can repair orphans and duplicates itself |
| 10,000 events per query | `latest` returns at most 10,000; xoluver does not trust a full page | A cursor |
| The /ts leg of `/commit` is not atomic with SQLite | Pebble is written first and undone if SQLite fails; a failed undo leaves an orphan (`XOLU-CM016`) | Write the events from an outbox row in the same SQLite transaction and apply them to Pebble afterwards, idempotently |
| Retention is easy to get wrong | `POST ts/tl/def` on an existing timeline updates its retention until the first write; `0` means the store default, which can expire | An explicit "forever" value, and refuse a redefinition that changes retention |

**Removes.** The orphan and duplicate classes that "need an administrator", the 10,000-event rule, and the retention check in `DefineTimeIndex` and `CheckIndex`.

## Smaller items

| Item | Detail |
|------|--------|
| OQL documentation | xolu's README shows `LIMIT`, but OQL is T-SQL: `LIMIT 10` parses as a second statement and fails with "only single statements are supported". `TOP n` works (tested on a schemaless type). xoluver can use `TOP` for `ListVersions` with a limit; that needs no xolu change |
| Unknown type in OQL | A type never written answers `400 QL004 does not exist`. Types are created implicitly on first write, so an empty result, or a distinct code, would be less hostile. xoluver treats the error as "no rows" off tenant routes |
| `DELETE` response | `cascaded_deletes` lists the deleted entity itself. A plain `DELETE` of a deleted entity and of one that never existed both answer 404. For ids from the type's own sequence an id below the high-water mark that is absent was deleted, but explicit-id creates also raise the mark, so a 404 versus 410 split would be approximate |
| Entity timestamps | `created_at` and `updated_at` exist as columns on the entity table; return them in `GET` at nanosecond precision (part of X8) |

## Dependencies

X1 and X2 are independent bug fixes and should go first. X3 and X5 are independent of everything else. X7 needs X6 only for the delete form. X9 needs X1 to be usable. The /ts items (X12, X13) matter less once X1, X8 and X9 exist.

## What xoluver would look like

Assuming X1, X2, X3, X5, X6, X7, X8, X9 and X10:

| Operation | Today | After |
|-----------|-------|-------|
| `Save` | `GET` entity, `GET` history row, `/commit` (document twice or three times), optionally a /ts leg | One `/commit`: update, history row with `$update.data`, `$now`, and `$previous.data` for a baseline, ignored if present |
| `Delete` | Tombstone commit, then `DELETE`; five states | One `/commit` with `delete` |
| `AsOf` | One OQL scan of the whole type, or a /ts query plus 3 row reads | One indexed query: `TOP 1 ... WHERE entity_id = ? AND saved_at <= ? ORDER BY version DESC` |
| Row identity | `entity*1,000,000+version` | A unique composite index |
| Time | Writers' clocks | `committed_at` from xolu |
| Large numbers | Capped ids, strings for big integers | Exact |
| Consistency checks | `CheckHistory`, `CheckIndex` for bypass and damage | Mostly unnecessary on a commit-only type |

These are expectations from reading the code, not tested designs. Nothing is promised about xolu's effort for any item: I did not estimate it, and X7, X8 and X9 are design work, not patches.

## A hazard in xoluver's own documents

`docs/DESIGN.md` section 4.2 tells the application to register a schema for the history type, and section 8 says to ship it with the module. On xolu v0.30.38 that is unsafe because of X1: once the history type has more than 30 rows, `ListVersions`, `CheckHistory` and the `AsOf` scan silently return nothing, and `AsOf` would answer "no history". The tests did not catch it because they use schemaless history types. The advice should be removed (or marked unsafe until X1 is fixed), and a live test should assert the behavior so it fails loudly when xolu changes.

## Not xolu's to fix

| Review point | Where it belongs |
|--------------|------------------|
| True multi-core contention (design section 11 item 6) | This sandbox has one CPU. Run the contention tests on a multi-core runner. SQLite's single writer, not CPU count, serializes commits, so the claim rests on the write lock; the multi-core run mainly exercises goroutine scheduling and the /ts leg |
| No retention or purge: history grows without bound | A xoluver feature (for example `Purge(before, keep-last-n)`), or a xolu TTL on a history type |
| The README repeats DESIGN (the existence and deletion tables appear in both) | Documentation: keep one source and link to it |
| Design section 7 maps an oversize commit to `413 XOLU-ST007`, while section 8 says xolu answers `400 XOLU-VL002` | A stale row in `docs/DESIGN.md` line 349; xolu answers 400 today (X4 would make the 413 row correct) |
| Reconciliation job and bypass lint (phase 4) | Not delivered. `CheckHistory` and `CheckIndex` are the building blocks. X10 would shrink the problem |

## Reproducing the evidence

| Item | How |
|------|-----|
| X1 | `POST /api/v1/schema/hc_version` with integer fields `entity_id`, `version` (`"x-xolu-index": true`); write 100 rows with `/commit` `append` (explicit ids), 20 of them with `entity_id` 7; `POST /api/v1/oql/query {"query":"SELECT id, version FROM hc_version WHERE entity_id = 7"}` returns `data: null`. Start xolu with `XOLU_LOG_LEVEL=debug` to see the planner line |
| X2 | Start xolu with `XOLU_TIMESERIES_ENABLED=true XOLU_TENANT_AUTO_REGISTER=true`; create an entity under `/api/v1/tenant/acme/`; the same OQL query answers `QL004` |
| X3 | Create and delete an entity; `POST /commit` with `update: {entity, id, version: 5, data}` returns 200 with `created: true` |
| X5 | `POST` a document with `"n": 9007199254740993`; `GET` returns `...992` |
