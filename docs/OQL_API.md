# OQL API Documentation

**Version:** 0.9.9  
**Status:** Active

OQL (Xolu Query Language) provides SQL-compatible query and mutation capabilities for xolu. It uses a subset of T-SQL syntax powered by the [tsqlparser](https://github.com/ha1tch/tsqlparser) library.

---

## Overview

OQL complements the Sulpher graph query language:

| Language | Syntax | Best For |
|----------|--------|----------|
| **Sulpher** | Cypher-like | Graph traversal, paths, relationships |
| **OQL** | SQL | Aggregates, tabular queries, bulk mutations |

---

## Endpoints

### Execute Query (Sync)

```
POST /api/v1/oql/query
```

Execute an OQL query synchronously.

**Request:**
```json
{
  "query": "SELECT category_id, COUNT(*) AS count FROM items GROUP BY category_id"
}
```

**Response (200 OK):**
```json
{
  "status": "completed",
  "data": [
    {"category_id": 1, "count": 42},
    {"category_id": 2, "count": 17}
  ],
  "stats": {
    "rows_scanned": 59,
    "rows_returned": 2,
    "rows_affected": 0,
    "execution_time_ms": 12
  }
}
```

---

### Execute Query (Async)

```
POST /api/v1/oql/query/async
```

Submit an OQL query for asynchronous execution.

**Request:**
```json
{
  "query": "SELECT * FROM items WHERE status = 'active'"
}
```

**Response (202 Accepted):**
```json
{
  "query_id": "oql_1704520800000000000",
  "status": "pending"
}
```

---

### Get Query Status

```
GET /api/v1/oql/query/{query_id}
```

Get the status of an async query.

**Response (200 OK):**
```json
{
  "query_id": "oql_1704520800000000000",
  "query": "SELECT * FROM items WHERE status = 'active'",
  "status": "completed",
  "created_at": "2025-01-06T12:00:00Z",
  "updated_at": "2025-01-06T12:00:01Z"
}
```

**Status values:** `pending`, `running`, `completed`, `failed`

---

### Get Query Result

```
GET /api/v1/oql/query/{query_id}/result
```

Get the result of a completed async query.

**Response (200 OK):**
```json
{
  "query_id": "oql_1704520800000000000",
  "status": "completed",
  "data": [...],
  "stats": {
    "rows_scanned": 100,
    "rows_returned": 42,
    "rows_affected": 0,
    "execution_time_ms": 15
  }
}
```

**Response (202 Accepted)** if still processing:
```json
{
  "query_id": "oql_1704520800000000000",
  "status": "running",
  "message": "Query is still processing"
}
```

---

## Supported SQL Syntax

### SELECT

```sql
SELECT [DISTINCT] [TOP n] columns
FROM entity
[WHERE conditions]
[GROUP BY columns]
[HAVING aggregate_conditions]
[ORDER BY columns [ASC|DESC]]
```

**Examples:**

```sql
-- Basic select
SELECT * FROM items

-- With conditions
SELECT id, name, value FROM items WHERE status = 'active'

-- With aggregates
SELECT category_id, COUNT(*) AS count, AVG(value) AS avg_value
FROM items
GROUP BY category_id

-- With HAVING
SELECT category_id, COUNT(*) AS count
FROM items
GROUP BY category_id
HAVING COUNT(*) > 5

-- With ORDER BY and TOP
SELECT TOP 10 * FROM items ORDER BY value DESC

-- DISTINCT
SELECT DISTINCT status FROM items
```

**Set operations: `UNION`, `UNION ALL`, `INTERSECT`, and `EXCEPT`.**

```sql
-- Combine two entities' own rows, deduplicated
SELECT name FROM deals UNION SELECT name FROM archived_deals

-- Keep duplicates
SELECT name FROM deals UNION ALL SELECT name FROM archived_deals

-- Rows present on both sides only
SELECT name FROM deals INTERSECT SELECT name FROM archived_deals

-- Left side's own rows not present on the right
SELECT name FROM deals EXCEPT SELECT name FROM archived_deals

-- A trailing ORDER BY / TOP applies to the combined result as a
-- whole, not just the last branch it's written against
SELECT name FROM deals UNION SELECT name FROM archived_deals ORDER BY name ASC
```

Each branch executes independently through the same query path any other
SELECT uses — full tenant scoping, decimal formatting, adapted/blob
resolution — then the branches are combined in Go (not pushed down to
SQLite as a single nested statement). Two restrictions, deliberate rather
than incomplete support: every branch in a chain must use the identical
operator (`A UNION B INTERSECT C` is rejected outright — SQL's own
precedence rules for mixed set operators are easy to get subtly wrong, so
this is rejected rather than guessed at); every branch must select the
same number of columns. `INTERSECT ALL`/`EXCEPT ALL` are rejected too —
not valid T-SQL syntax at all (SQL Server only supports `UNION ALL`), and
correct multiset semantics for the other two are a separate, unimplemented
piece of work, not a formality. (Sulpher, the graph query language, also
supports `UNION`/`UNION ALL` — the two languages' own implementations are
independent of each other.)

### INSERT

```sql
INSERT INTO entity (column1, column2, ...) VALUES (value1, value2, ...), ...
```

**Examples:**

```sql
-- Single row
INSERT INTO items (category_id, status, value) VALUES (1, 'active', 23.5)

-- Multiple rows
INSERT INTO items (category_id, status) VALUES 
  (1, 'active'),
  (2, 'active'),
  (3, 'inactive')
```

### UPDATE

```sql
UPDATE entity SET column = value, ... WHERE condition
```

**Note:** WHERE clause is **required**. UPDATE without WHERE is rejected.

**Examples:**

```sql
-- Update single field
UPDATE items SET status = 'inactive' WHERE category_id = 5

-- Update multiple fields
UPDATE items SET status = 'maintenance', value = 0 WHERE id = 123
```

### DELETE

```sql
DELETE FROM entity WHERE condition
```

**Note:** WHERE clause is **required**. DELETE without WHERE is rejected.

**Examples:**

```sql
-- Delete by condition
DELETE FROM items WHERE status = 'decommissioned'

-- Delete with multiple conditions
DELETE FROM items WHERE category_id = 5 AND last_value < '2024-01-01'
```

---

## Aggregate Functions

| Function | Description | Example |
|----------|-------------|---------|
| `COUNT(*)` | Count all rows | `SELECT COUNT(*) FROM items` |
| `COUNT(column)` | Count non-null values | `SELECT COUNT(value) FROM items` |
| `SUM(column)` | Sum of values | `SELECT SUM(value) FROM items` |
| `AVG(column)` | Average of values | `SELECT AVG(value) FROM items` |
| `MIN(column)` | Minimum value | `SELECT MIN(value) FROM items` |
| `MAX(column)` | Maximum value | `SELECT MAX(value) FROM items` |

---

## WHERE Clause Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `=` | Equal | `WHERE status = 'active'` |
| `!=`, `<>` | Not equal | `WHERE status != 'inactive'` |
| `<` | Less than | `WHERE value < 100` |
| `>` | Greater than | `WHERE value > 50` |
| `<=` | Less than or equal | `WHERE value <= 100` |
| `>=` | Greater than or equal | `WHERE value >= 50` |
| `AND` | Logical AND | `WHERE a = 1 AND b = 2` |
| `OR` | Logical OR | `WHERE a = 1 OR b = 2` |
| `NOT` | Logical NOT | `WHERE NOT status = 'inactive'` |
| `BETWEEN` | Range | `WHERE value BETWEEN 10 AND 100` |
| `IN` | Set membership | `WHERE status IN ('active', 'pending')` |
| `LIKE` | Pattern match | `WHERE name LIKE 'item%'` |
| `IS NULL` | Null check | `WHERE value IS NULL` |
| `IS NOT NULL` | Not null check | `WHERE value IS NOT NULL` |

---

## JOIN Queries

> **SQLite store only.** JOIN push-down requires `XOLU_STORAGE_TYPE=sqlite`.
> Only two-table joins are supported; three-or-more-table joins are rejected,
> and neither side of a JOIN may be a subquery (a subquery is supported
> elsewhere in a SELECT's own FROM clause -- see
> [Subqueries](#subqueries-derived-tables) -- just not as one side of a JOIN).

OQL supports two-table joins when the store backend is SQLite. All four standard
join types are pushed to SQLite as a single SQL statement — no application-side
stitching.

### Supported join types

| Type | Keyword | Behaviour |
|------|---------|-----------|
| Inner join | `INNER JOIN` or `JOIN` | Only rows with a match on both sides |
| Left outer join | `LEFT JOIN` | All left rows; NULL for right fields when no match |
| Right outer join | `RIGHT JOIN` | All right rows; NULL for left fields when no match |
| Full outer join | `FULL JOIN` or `FULL OUTER JOIN` | All rows from both sides; NULL for unmatched |

### Syntax

```sql
SELECT <alias>.<field> [AS <alias>], ...
FROM   <left_entity>  [AS <alias>]
{INNER | LEFT | RIGHT | FULL [OUTER]} JOIN
       <right_entity> [AS <alias>]
ON     <alias>.<field> = <alias>.<field>
[WHERE ...]
```

The `ON` condition must be a simple equality between two qualified field
references (`alias.field = alias.field`). Compound ON conditions and
non-equality comparisons are not supported.

Both table references must be plain entity names -- a subquery cannot appear
on either side of a JOIN (it can appear standalone in a SELECT's own FROM
clause; see [Subqueries](#subqueries-derived-tables)).

### Entity classification

Each entity in a join is classified independently:

- **Adapted entity** (registered with a schema via `POST /api/v1/schema/<entity>`):
  fields are accessed as native SQL columns.
- **Blob entity** (no schema registration): fields are accessed via
  `json_extract`.

Mixed joins (one adapted, one blob) are supported. The two classifications
can be combined freely.

### Examples

```sql
-- INNER JOIN: published posts with author names
SELECT a.title, b.name AS author
FROM   posts AS a
INNER JOIN authors AS b ON a.author_id = b.id
WHERE  a.status = 'published'

-- LEFT JOIN: all customers, orders where they exist
SELECT b.name, a.amount
FROM   orders AS a
RIGHT JOIN customers AS b ON a.customer_id = b.id

-- Blob entities (no schema registered)
SELECT a.post_id, b.label
FROM   tag_links AS a
INNER JOIN tags AS b ON a.tag_id = b.id
WHERE  b.label = 'go'
```

### Column aliases in results

Result row keys are determined by the column alias:

- With an explicit `AS alias`: the result key is `alias`.
- Without `AS`: the result key is the bare field name (`title`, not `a.title`).

Use explicit `AS` aliases when field names are ambiguous across both entities:

```sql
SELECT a.name AS post_name, b.name AS author_name
FROM   posts AS a
INNER JOIN authors AS b ON a.author_id = b.id
```

### Constraints and unsupported forms

- **Two tables only.** Three-or-more-table joins are not supported.
- **Plain table names only.** A subquery cannot appear as either side of a
  JOIN (`(SELECT ...) AS t`) -- rejected. Supported standalone; see
  [Subqueries](#subqueries-derived-tables).
- **Simple ON condition.** The `ON` clause must be a single equality between
  two qualified identifiers. Compound conditions (`ON a.x = b.y AND a.z = b.w`)
  are not supported.
- **SQLite store only.** JOIN push-down requires the SQLite backend.
- **CROSS JOIN is not supported.** Use a filtered INNER JOIN instead.

### JOIN vs Sulpher

JOINs address flat correlation: "give me posts with their author name." Use
Sulpher when the question involves graph structure — variable-depth
traversals, path finding, cycle detection, or multi-hop relationships.

| Query shape | Use |
|-------------|-----|
| Posts with author name | OQL JOIN |
| All users reachable within 3 hops from user 42 | Sulpher |
| Orders joined to customers | OQL JOIN |
| Shortest path between two nodes | Sulpher |


---

## Subqueries (Derived Tables)

A SELECT's own FROM clause may be a subquery instead of a plain entity
name: `SELECT ... FROM (SELECT ...) AS alias`. The inner subquery runs
first, independently, through the identical query path any other SELECT
uses -- full tenant scoping, decimal formatting, JOIN or blob/adapted
resolution, even a nested UNION -- and the outer query then treats its
result as a plain, in-memory table.

### Syntax

```sql
SELECT <column list>
FROM (<any valid SELECT>) AS <alias>
[WHERE ...]
[GROUP BY ...] [HAVING ...]
[ORDER BY ...]
[TOP N]
```

An explicit alias is required. The outer query's own column references
resolve against the inner subquery's own result columns by name (a
qualifier like `alias.column` is accepted and stripped -- the alias itself
doesn't need to match anything inside the subquery).

### Examples

```sql
-- Filter on a computed, aggregated value
SELECT cat, avgval
FROM (SELECT cat, AVG(val) AS avgval FROM readings GROUP BY cat) AS x
WHERE cat = 'temperature'

-- Nested subqueries
SELECT name FROM (SELECT name FROM (SELECT name FROM items) AS inner1) AS outer1

-- UNION inside a subquery
SELECT name FROM (
  SELECT name FROM active_items
  UNION
  SELECT name FROM archived_items
) AS combined
```

### Constraints and unsupported forms

- **An explicit alias is required.** `FROM (SELECT ...)` with no `AS alias`
  is rejected.
- **The subquery's own column alias list is not supported.**
  `AS x(col1, col2)` -- renaming the subquery's own output columns
  positionally -- is rejected. Alias individual columns inside the
  subquery's own SELECT list instead.
- **Nesting is capped at 10 levels.** A subquery containing a subquery
  containing a subquery, and so on; the 11th level is rejected outright
  rather than risking unbounded recursion.
- **Not on either side of a JOIN.** See [JOIN Queries](#join-queries)'s own
  constraints.
- **No SQL-level push-down.** The outer query's own WHERE/GROUP BY/ORDER
  BY/DISTINCT/TOP all run in Go over the subquery's already-materialised
  result, not as a single nested SQL statement. A known, accepted
  efficiency tradeoff for a subquery over a large inner result -- not a
  correctness concern, but worth knowing for query planning.
- **An outer SUM/AVG over a subquery's own decimal column** uses ordinary
  floating-point arithmetic rather than xolu's own decimal-precise
  aggregation path (there's no live schema to consult for a computed,
  synthetic result set) -- a small, honest floating-point-precision
  tradeoff specific to that one combination, not a correctness gap
  elsewhere.

---

## Limitations

| Feature | Status |
|---------|--------|
| SELECT with aggregates | ✓ Supported |
| INSERT with VALUES | ✓ Supported |
| UPDATE with WHERE | ✓ Supported |
| DELETE with WHERE | ✓ Supported |
| GROUP BY, HAVING | ✓ Supported |
| ORDER BY, TOP | ✓ Supported |
| DISTINCT | ✓ Supported |
| INNER JOIN | ✓ Supported |
| LEFT JOIN | ✓ Supported |
| RIGHT JOIN | ✓ Supported |
| FULL OUTER JOIN | ✓ Supported |
| UNION, UNION ALL | ✓ Supported |
| INTERSECT, EXCEPT | ✓ Supported |
| Subqueries (`SELECT ... FROM (SELECT ...) AS alias`) | ✓ Supported |
| CROSS JOIN | ✗ Not supported |
| Three-or-more-table joins | ✗ Not supported |
| JOIN with a subquery on either side | ✗ Not supported |
| INTERSECT ALL, EXCEPT ALL | ✗ Not supported (not valid T-SQL; SQL Server only supports UNION ALL) |
| Subquery's own column alias list (`AS x(col1, col2)`) | ✗ Not supported |
| INSERT ... SELECT | ✗ Not supported |
| UPDATE without WHERE | ✗ Rejected |
| DELETE without WHERE | ✗ Rejected |
| Window functions | ✗ Not supported |
| CTEs | ✗ Not supported |

---

## Error Responses

**400 Bad Request** - Invalid query syntax or validation error:
```json
{
  "error": "parse error: unexpected token at position 15"
}
```

**400 Bad Request** - Safety violation:
```json
{
  "error": "UPDATE without WHERE clause is not permitted"
}
```

**400 Bad Request** - Entity not found:
```json
{
  "error": "entity 'nonexistent' does not exist"
}
```

**404 Not Found** - Query ID not found:
```json
{
  "error": "Query not found"
}
```

---

## Usage Examples

### Analytics Query

```bash
curl -X POST http://localhost:9090/api/v1/oql/query \
  -H "Content-Type: application/json" \
  -d '{
    "query": "SELECT category_id, COUNT(*) as count, AVG(value) as avg FROM items WHERE status = '\''active'\'' GROUP BY category_id ORDER BY count DESC"
  }'
```

### Bulk Update

```bash
curl -X POST http://localhost:9090/api/v1/oql/query \
  -H "Content-Type: application/json" \
  -d '{
    "query": "UPDATE items SET status = '\''maintenance'\'' WHERE category_id = 5"
  }'
```

### Batch Insert

```bash
curl -X POST http://localhost:9090/api/v1/oql/query \
  -H "Content-Type: application/json" \
  -d '{
    "query": "INSERT INTO items (category_id, status, value) VALUES (1, '\''active'\'', 23.5), (2, '\''active'\'', 24.1)"
  }'
```

### Async Query

```bash
# Submit
QUERY_ID=$(curl -s -X POST http://localhost:9090/api/v1/oql/query/async \
  -H "Content-Type: application/json" \
  -d '{"query": "SELECT * FROM items"}' | jq -r '.query_id')

# Poll status
curl http://localhost:9090/api/v1/oql/query/$QUERY_ID

# Get result
curl http://localhost:9090/api/v1/oql/query/$QUERY_ID/result
```

---

## When to Use OQL vs Sulpher

| Use Case | Recommended |
|----------|-------------|
| Count records by category | OQL |
| Calculate averages, sums | OQL |
| Bulk update/delete | OQL |
| Batch insert | OQL |
| Find paths between nodes | Sulpher |
| Flat cross-entity correlation | OQL JOIN |
| Traverse relationships | Sulpher |
| Variable-length paths | Sulpher |
| Graph pattern matching | Sulpher |

**Combined Example:**

```
# Find users followed by user 123 (Sulpher)
POST /api/v1/graph/query
{"query": "MATCH (u:User)-[:FOLLOWS*1..3]->(f:User) WHERE u.id = 123 RETURN f"}

# Count followers per user (OQL)
POST /api/v1/oql/query
{"query": "SELECT user_id, COUNT(*) as followers FROM follows GROUP BY user_id ORDER BY followers DESC TOP 10"}
```
