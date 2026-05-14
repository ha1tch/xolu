# OQL JOIN Push-down — Implementation Specification

**Status:** Design  
**Author:** Nadine Ostrovski  
**Date:** 2026-05-13  
**Scope:** `pkg/oql` only. No storage layer changes required.

---

## Overview

This document is a working specification for an implementor. It covers every
file that must change, every function that must be added or modified, the exact
decision logic in each phase, and the test strategy. Read it top to bottom
before touching any code.

The goal is to allow OQL queries of the form:

```sql
SELECT a.title, b.name
FROM   post AS a
JOIN   author AS b ON a.author_id = b.id
WHERE  a.status = 'published'
```

to be executed as a single SQLite query rather than two separate fetches merged
in Go. The result shape, the fallback behaviour, and the tenant scoping rules
are all specified here.

---

## Constraint: per-entity path decision

Every entity in the join is classified independently:

- **Fully adapted** — all fields in the query are in native columns. Emit
  native column references throughout (SELECT, ON, WHERE).
- **Not fully adapted** — at least one referenced field is not in a native
  column, or the entity is not adapted at all. Use the `data` blob and
  `json_extract()` for all field access for that entity.

There is no partial hydration. If an entity is in the blob path, every field
access for that entity goes through `json_extract(a.data, '$.field')`. This
keeps the implementation straightforward and the SQL auditable.

The implementor must check this on a per-entity basis, not per-query. A join
between a fully adapted entity and a blob entity is valid and must produce
correct SQL.

---

## Phase 1 — Planner (`planner.go`)

### 1.1 New `PushDecision` value

Add `PushJoin` to the existing `PushDecision` enum after `PushFull`:

```go
PushJoin  // Push a two-table JOIN to SQLite
```

Add its `String()` case: `"JOIN"`.

### 1.2 New helper: `extractJoinSpec`

Add this function to `planner.go`. It inspects a `SelectStatement` and returns
a populated `joinSpec` if the FROM clause contains exactly one `*ast.JoinClause`
with a condition that is a simple equality between two field references. Returns
`nil, ""` if the query is not a supported join shape.

```go
type joinSpec struct {
    LeftEntity  string        // e.g. "post"
    LeftAlias   string        // e.g. "a" (falls back to entity name if no AS)
    RightEntity string        // e.g. "author"
    RightAlias  string        // e.g. "b"
    JoinType    string        // "INNER", "LEFT", "RIGHT", "FULL" — passed through to SQL
    Condition   *ast.InfixExpression // the ON a.x = b.y expression
}

func extractJoinSpec(s *ast.SelectStatement) (*joinSpec, error)
```

Rules for `extractJoinSpec`:

1. `s.From` must be non-nil and `len(s.From.Tables) == 1`.
2. `s.From.Tables[0]` must be `*ast.JoinClause`.
3. `jc.Type` must be one of `"INNER"`, `"LEFT"`, `"RIGHT"`, or `"FULL"`.
   CROSS JOIN (no ON condition) is not supported — return `nil, ""` with no
   error; the planner will fall through to `PushNone`.
4. Both `jc.Left` and `jc.Right` must be `*ast.TableName`. Subqueries and
   derived tables are not supported.
5. `jc.Condition` must be `*ast.InfixExpression` with operator `"="` where
   both sides are `*ast.QualifiedIdentifier` (i.e. `alias.field`).
6. Extract entity names by normalising `TableName.Name.String()` with
   `normalizeEntityName`. Extract aliases from `TableName.Alias.Value`; if
   absent, use the entity name as the alias.

If any rule fails, return `nil, ""` (not an error — caller treats this as
"not a join query").

### 1.3 New helper: `isJoinConditionPushable`

```go
func isJoinConditionPushable(cond ast.Expression, leftAlias, rightAlias string) bool
```

Returns true if `cond` is `*ast.InfixExpression` with `"="` and both sides are
`*ast.QualifiedIdentifier` whose table qualifiers are exactly `leftAlias` or
`rightAlias`. The sides do not need to be in a particular order (LEFT.x =
RIGHT.y and RIGHT.y = LEFT.x are both valid).

### 1.4 Modify `Plan`

At the top of `Plan`, before the existing adapted-entity fast path, add:

```go
// --- JOIN path ---
if js, _ := extractJoinSpec(s); js != nil {
    return p.planJoin(ctx, s, js, store)
}
```

### 1.5 New method: `planJoin`

```go
func (p *Planner) planJoin(
    ctx context.Context,
    s *ast.SelectStatement,
    js *joinSpec,
    store storage.Store,
) QueryPlan
```

Logic:

1. Assert `store` implements `storage.AggregateQueryable`. If not, return
   `PushNone` with reason `"store does not support AggregateQueryable"`.

2. Verify both entities exist in the store (use `IsAdaptedEntity` as a proxy
   — it returning false is fine; what matters is that `CountEntities` doesn't
   error). If either entity cannot be counted, return `PushNone`.

3. Classify each entity:
   - `leftAdapted  = aggStore.IsAdaptedEntity(js.LeftEntity)`
   - `rightAdapted = aggStore.IsAdaptedEntity(js.RightEntity)`

4. Check the ON condition is pushable via `isJoinConditionPushable`. If not,
   return `PushNone`.

5. Check the WHERE clause (if present) is pushable for a join context. Add
   a new helper `isJoinWherePushable(where, leftAlias, rightAlias)` — same
   logic as `isWherePushable` but accepts qualified identifiers as simple
   fields (i.e. `a.field` is valid). If not pushable, return `PushNone`.

6. Return:

```go
return QueryPlan{
    Push:   []PushDecision{PushJoin},
    Reason: fmt.Sprintf(
        "join %s(%s) %s %s(%s): left_adapted=%v right_adapted=%v",
        js.LeftEntity, js.LeftAlias, js.JoinType,
        js.RightEntity, js.RightAlias,
        leftAdapted, rightAdapted,
    ),
}
```

Store `js`, `leftAdapted`, and `rightAdapted` in the `QueryPlan`. Add these
fields to `QueryPlan`:

```go
Join         *joinSpec // non-nil when Push contains PushJoin
LeftAdapted  bool
RightAdapted bool
```

---

## Phase 2 — SQL generator (`sqlgen_join.go`, new file)

Create `pkg/oql/sqlgen_join.go`. Do not modify `sqlgen.go` or
`sqlgen_adapted.go` — the join generator is self-contained.

### 2.1 `JoinSQL` struct

```go
type JoinSQL struct {
    SQL     string
    Args    []interface{}
    Aliases []string // result column aliases in SELECT order
}
```

### 2.2 `GenerateJoinSQL`

```go
func GenerateJoinSQL(
    stmt *ast.SelectStatement,
    plan QueryPlan,
    tenantID string,
    store storage.AggregateQueryable,
    dialect SQLDialect,
) (*JoinSQL, error)
```

This is the entry point called by the executor. It builds a complete two-table
SELECT using the `joinSpec` in `plan.Join`.

**SQL shape — both adapted:**

```sql
SELECT a.<col1>, b.<col2>, ...
FROM   <left_table> a
{INNER|LEFT|RIGHT|FULL OUTER} JOIN <right_table> b
  ON a.<left_field> = b.<right_field>
WHERE  a.entity_type_col = ? AND b.entity_type_col = ?
  AND  <translated WHERE>
```

The join type is taken directly from `plan.Join.JoinType` and emitted
verbatim. `"FULL"` is emitted as `"FULL OUTER JOIN"` since that is the
standard form accepted by both SQLite and PostgreSQL.

Note: adapted tables have their own table names (from `AdaptedTableName`).
They do not use the `entities` table. The `entity_type` filter may not apply
depending on how the adapted table is structured — consult
`store.AdaptedTableName` to get the actual table name, then look at how
`GenerateAdaptedSQL` scopes its queries. Match that pattern exactly.

**SQL shape — at least one side not adapted (blob path):**

```sql
SELECT a.data, b.data
FROM   entities a
JOIN   entities b ON json_extract(a.data, '$.<left_field>') 
                   = json_extract(b.data, '$.<right_field>')
WHERE  a.entity_type = ? AND b.entity_type = ?
  AND  <translated WHERE using json_extract for both sides>
```

Both sides use the `entities` table with aliased references. The `entity_type`
column disambiguates which rows belong to which entity.

**NULL result rows for outer joins**

For LEFT, RIGHT, and FULL OUTER JOIN, rows where the outer side has no match
produce NULL values in the result. For adapted columns these are plain SQL
NULLs and arrive as `nil` in the result map — no special handling needed.
For blob columns the `data` field itself will be NULL. `AggregateQuery` may
return a `nil` or empty-string value for that alias. The executor must treat
a NULL or missing blob as an empty map `map[string]interface{}{}` rather than
attempting to JSON-parse it. Add a nil-guard in the `PushJoin` case in
`executeSelect` before passing records to the rest of the pipeline.

**Mixed case (one adapted, one blob):**

Left side adapted, right side blob:

```sql
SELECT a.<col>, json_extract(b.data, '$.<col>')
FROM   <left_adapted_table> a
JOIN   entities b ON a.<left_field> = json_extract(b.data, '$.<right_field>')
WHERE  b.entity_type = ?
  AND  <translated WHERE>
```

Right side adapted, left side blob: mirror image.

### 2.3 Column generation

For each `SelectColumn` in `stmt.Columns`:

1. Parse the qualifier from `QualifiedIdentifier` to determine which alias it
   belongs to.
2. If that entity is adapted: emit `alias.native_column_name`.
3. If that entity is not adapted: emit `json_extract(alias.data, '$.field')`.
4. Emit `AS <result_alias>` using `columnAlias(col)` as the result alias.

For `SELECT *`: expand to all native columns for adapted entities, `alias.data`
for blob entities. Keep the two blobs keyed by alias in the result.

### 2.4 ON condition generation

```go
func generateJoinOnClause(
    cond *ast.InfixExpression,
    js *joinSpec,
    plan QueryPlan,
    store storage.AggregateQueryable,
    dialect SQLDialect,
) (string, error)
```

Walk the condition. For each `QualifiedIdentifier`:

- If the qualifier matches `js.LeftAlias` and `plan.LeftAdapted`: emit
  `a.native_column`.
- If the qualifier matches `js.LeftAlias` and not adapted: emit
  `json_extract(a.data, '$.field')`.
- Mirror for right side.

### 2.5 WHERE generation for joins

Add `generateJoinWhereClause` — same walk as the existing `translateExpr` in
`sqlgen.go` but resolves `QualifiedIdentifier` table qualifiers to the correct
alias and extraction method. Both sides use the same per-entity adapted/blob
decision.

### 2.6 Tenant scoping

When `tenantID` is non-empty, append `AND a.tenant_id = ? AND b.tenant_id = ?`
(or the equivalent for adapted tables — follow the pattern in
`GenerateAdaptedSQL`). Add both as args.

---

## Phase 3 — Executor (`executor.go`)

### 3.1 New case in `executeSelect`

Add `PushJoin` to the `switch` in `executeSelect`, immediately after the
`PushFull` case:

```go
case plan.pushed(PushJoin):
    aggStore, ok := e.store.(storage.AggregateQueryable)
    if !ok {
        break // fall through to Go path
    }
    joinSQL, genErr := GenerateJoinSQL(s, plan, sqlTID, aggStore, e.dialect)
    if genErr != nil {
        log.Debug().Err(genErr).Msg("Join SQL generation failed, falling back")
        break
    }
    joinRecords, queryErr := aggStore.AggregateQuery(
        ctx, joinSQL.SQL, joinSQL.Args, joinSQL.Aliases,
    )
    if queryErr != nil {
        log.Debug().Err(queryErr).Str("sql", joinSQL.SQL).
            Msg("Join push-down query failed, falling back")
        break
    }
    records = joinRecords
    fetched = true
```

The fallback path (`fetched == false`) will then attempt to fetch both entities
separately and join in Go. **Do not implement the Go-path join in this
iteration.** If the push-down fails, return an error explaining that join
execution requires push-down. This keeps the scope contained. The Go-path join
can be added in a follow-on if needed.

### 3.2 Result shape

`AggregateQuery` returns `[]map[string]interface{}` keyed by the column aliases
from `JoinSQL.Aliases`. This flows through `NewSelectResult` unchanged — the
result rows are flat maps of alias → value, exactly as for any other pushed
query. No changes to `result.go` are needed.

For the blob path the result contains two keys — one per entity alias — each
holding the raw JSON blob. The caller is responsible for unpacking. Document
this in the package comment.

### 3.3 `extractEntityFromSelect` — do not modify

`extractEntityFromSelect` currently returns `""` for join queries (the first
table is a `*ast.JoinClause`, not a `*ast.TableName`). Since `executeSelect`
now handles `PushJoin` before calling `extractEntityFromSelect` for entity
count and listing, this is fine — the join path never reaches the code that
uses `entity` for entity-type filtering. Verify this by tracing through the
code after the `switch` block.

---

## Phase 4 — Validator (`validator.go`)

Find the validation rule that rejects JOIN clauses. It will be in the
`Validate` method or a helper it calls. Change it to allow `*ast.JoinClause`
in `SelectStatement.From.Tables[0]` as a valid form. Add a check that both
`Left` and `Right` are `*ast.TableName` (not subqueries) with non-empty names.

Do not validate that the entity names exist at this point — that happens in the
planner. The validator's job is structural correctness only.

Update the package-level comment in `oql.go`:

```go
// JOINs are supported for SELECT queries when both tables are entity types
// in the same SQLite store. INNER JOIN is pushed to SQLite; other join types
// are not currently supported.
```

Remove the line: `// JOINs are not supported as relationships are handled by
the graph layer.`

---

## Phase 5 — Tests

### 5.1 Planner tests (`planner_test.go`)

Add a test group `TestPlanner_Join` covering:

- `PushJoin` returned for INNER JOIN between two adapted entities with simple
  ON condition.
- `PushJoin` returned for INNER JOIN where one or both sides are blob entities.
- `PushJoin` returned for LEFT JOIN, RIGHT JOIN, and FULL OUTER JOIN.
- `PushNone` returned for CROSS JOIN (no ON condition).
- `PushNone` returned when ON condition is not a simple equality.
- `PushNone` returned when ON condition references a non-existent alias.
- `PushNone` returned when store does not implement `AggregateQueryable`.
- `LeftAdapted`/`RightAdapted` fields correctly set for each entity
  combination.

Use `NewPlannerWithDialectAndThreshold` with a mock store implementing
`AggregateQueryable` — follow the pattern in the existing planner tests.

### 5.2 SQL generator tests (`sqlgen_test.go` or new `sqlgen_join_test.go`)

Use golden SQL output assertions. For each scenario, assert the exact SQL
string and args slice. Scenarios:

- Both adapted: native columns on both sides, no `json_extract`.
- Left adapted, right blob: mixed extraction in SELECT, ON, and WHERE.
- Both blob: `json_extract` throughout, `entities` table aliased twice.
- With WHERE clause: correct per-entity field extraction in the WHERE fragment.
- With tenant scoping: `tenant_id` params appended correctly for each side.
- No WHERE clause: clean minimal SQL.

### 5.3 Integration tests (new `join_e2e_test.go` in `pkg/server`)

Two tests against a real SQLite store:

**`TestJoinPushdown_BothAdapted`** — create two adapted entity types with a
REF between them. Execute a join query via the OQL engine. Assert the result
contains merged rows with correct field values from both entities. Assert
`stats.rows_scanned` reflects the push-down (not the sum of both entity
counts).

**`TestJoinPushdown_BlobFallback`** — same setup with at least one non-adapted
entity. Assert correct results and correct SQL path via plan inspection.

**`TestJoinPushdown_OuterJoin_NullRows`** — execute a LEFT JOIN where some
left-side rows have no matching right-side row. Assert that result rows for
unmatched left entities contain an empty map for the right-side fields rather
than a parse error or a missing key.

CROSS JOIN is rejected by the validator — no integration test needed.

### 5.4 Error path tests

- JOIN with no ON condition: validator rejects.
- JOIN with subquery as table: validator rejects.
- JOIN where push-down SQL generation fails: executor returns error (not a
  silent empty result).

---

## Sequence summary

```
planner.go          extractJoinSpec         new function
planner.go          isJoinConditionPushable new function
planner.go          isJoinWherePushable     new function
planner.go          planJoin                new method on Planner
planner.go          Plan                   add JOIN detection at top
planner.go          QueryPlan              add Join, LeftAdapted, RightAdapted fields
sqlgen_join.go      GenerateJoinSQL         new file, new function
sqlgen_join.go      generateJoinOnClause    new function
sqlgen_join.go      generateJoinWhereClause new function
executor.go         executeSelect           add PushJoin case
validator.go        Validate / helper       allow JoinClause, reject subqueries
oql.go              package comment         update JOIN support statement
```

Total new code: approximately 400–500 lines excluding tests. The test suite
will be comparable in size to the implementation.

---

## What is explicitly out of scope

- LEFT, RIGHT, FULL OUTER JOIN — supported, join type passed through to SQL.
- CROSS JOIN — no ON condition; decline at `extractJoinSpec`, document as
  future work.
- Go-path join fallback — if push-down fails, return an error.
- Three-table joins — `extractJoinSpec` returns nil for any FROM clause with
  more than one join level.
- Subquery tables — validator rejects.
- JOIN in UPDATE or DELETE — not meaningful for olu's mutation model.
- Result flattening or schema inference — result rows are flat maps of alias →
  value; the caller unpacks.

---

## PostgreSQL compatibility notes

These notes are addressed to the team developing the PostgreSQL adapter. No
changes to this document's implementation plan are required for SQLite. The
three concerns below must be resolved before the join push-down system can be
considered compatible with both backends.

### 1. ON clause generation must be fully dialect-mediated

This is the highest-risk point. The ON clause generator (`generateJoinOnClause`)
is new code with no existing analogue in the codebase to follow. The existing
push-down paths — `GenerateSQL`, `GenerateAdaptedSQL`, `GenerateAggregateSQL`
— all go through `dialect.JSONField` and `dialect.JSONFieldNumeric` for every
blob-side field extraction. The ON clause generator must do the same without
exception.

Concretely: the string `json_extract` must not appear anywhere in
`sqlgen_join.go`. Every blob-side field reference in the ON condition, the
WHERE clause, and the SELECT column list must be emitted via
`dialect.JSONField(fieldPath)`. For SQLite this produces
`json_extract(alias.data, '$.field')`; for PostgreSQL the adapter's dialect
implementation will produce `alias.data->>'field'` or the equivalent.

The risk is that an implementor, working only against SQLite, writes something
like:

```go
fmt.Sprintf("json_extract(%s.data, '$.%s')", alias, field)
```

instead of:

```go
fmt.Sprintf("%s.%s", alias, dialect.JSONField(field))
```

The first form passes all SQLite tests and silently breaks PostgreSQL. The
second form is correct for both. Code review for this file should specifically
check that `json_extract` does not appear as a literal string.

Note also that parameter placeholders follow the same rule. SQLite uses `?`;
PostgreSQL uses `$1`, `$2`, and so on. Every parameter in the join SQL must
be emitted via `dialect.Placeholder(n)`, including the entity-type filter args
and the tenant-scoping args. Again, this is the existing pattern — the join
generator must follow it without exception.

### 2. `AggregateQuery` must treat the join SQL as opaque

The join generator produces a complete SQL string and an args slice and passes
both to `aggStore.AggregateQuery`. For this to be backend-agnostic,
`AggregateQuery` on the PostgreSQL adapter must execute the SQL string against
its own connection without inspecting or rewriting it. It must not assume the
SQL contains SQLite-specific syntax, and it must not attempt to translate
parameter placeholders — the dialect has already handled that during generation.

Before the join implementation ships, confirm with the PostgreSQL team that
their `AggregateQuery` implementation satisfies this contract. The SQLite
implementation (`sqlite_aggregate.go`) executes the SQL directly against the
`*sql.DB` connection, which is the correct model. The PostgreSQL implementation
must do the same.

If the PostgreSQL adapter's `AggregateQuery` does any SQL introspection or
rewriting internally, that must be resolved before join push-down is enabled
for that backend. The fix belongs in the adapter, not in the join generator.

### 3. Adapted and blob tables must be reachable in the same database connection

The mixed-case SQL shape — one adapted table joined to the `entities` blob
table — assumes that both tables are accessible within a single SQL statement
on a single connection. In SQLite this is always true: everything is in the
same file. In PostgreSQL it may not be.

Specifically, if the PostgreSQL adapter places adapted tables in a different
schema, a different database, or a different connection string from the
`entities` table, the generated JOIN SQL will be syntactically valid but will
fail at runtime with a table-not-found error. PostgreSQL can join across
schemas within the same database using schema-qualified table names
(`schema.tablename`), but it cannot join across separate database connections
in a single query.

The join generator assumes it can emit:

```sql
FROM adapted_table a JOIN entities b ON ...
```

and have both tables be visible to the executing connection. This is an
architectural assumption that must be verified with the PostgreSQL team before
the mixed-case path is enabled for that backend.

The recommended resolution is one of:

- Confirm that adapted tables and the `entities` table always share the same
  PostgreSQL database and schema, and that `AggregateQuery` uses a connection
  that can see both. If so, no code change is required — just document the
  constraint.

- If adapted tables live in a separate schema, extend `AdaptedTableName` to
  return schema-qualified names (e.g. `"analytics.post"` instead of `"post"`).
  The join generator already uses `AdaptedTableName` to obtain the table name,
  so this change would be transparent to the join implementation.

- If adapted tables genuinely live in a separate database, the mixed-case JOIN
  cannot be pushed down for PostgreSQL. The planner's `planJoin` method should
  check this and return `PushNone` for mixed-case queries on the PostgreSQL
  backend. A backend capability flag on `AggregateQueryable` — something like
  `CanJoinAdaptedWithBlob() bool` — is the cleanest way to express this. The
  SQLite adapter returns `true`; the PostgreSQL adapter returns `true` or
  `false` depending on its deployment configuration.

This concern does not affect the fully-adapted case (both tables are adapted,
both are in the adapted table store) or the fully-blob case (both tables use
`entities`). It applies only to the mixed case. If the PostgreSQL team cannot
confirm co-location, the simplest safe approach is to disable the mixed-case
path for PostgreSQL until the deployment model is settled.
