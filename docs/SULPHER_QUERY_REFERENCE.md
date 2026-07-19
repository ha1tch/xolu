# Sulpher Query Reference

Sulpher is xolu's graph query language — a substantial subset of openCypher 9
(OC9) that operates on the in-memory graph built from entity REF fields.
Queries are submitted as Cypher strings; the executor walks the graph using
BFS or DFS and returns structured result rows.

This document covers every query form that works. Features that are present
in OC9 but not implemented are collected in [Remaining gaps](#remaining-gaps).

---

## Contents

1. [How Sulpher works](#how-sulpher-works)
2. [Algorithm selection](#algorithm-selection)
3. [MATCH patterns](#match-patterns)
4. [WHERE predicates](#where-predicates)
5. [RETURN clause](#return-clause)
6. [Aggregation](#aggregation)
7. [WITH pipeline](#with-pipeline)
8. [OPTIONAL MATCH](#optional-match)
9. [Path queries: shortestPath and allShortestPaths](#path-queries)
10. [Path comprehension and quantifiers](#path-comprehension-and-quantifiers)
11. [UNION and UNION ALL](#union-and-union-all)
12. [UNWIND](#unwind)
13. [Built-in functions](#built-in-functions)
14. [Sending a query](#sending-a-query)
15. [Remaining gaps](#remaining-gaps)
16. [Semantic differences from Neo4j](#semantic-differences-from-neo4j)

---

## How Sulpher works

The graph is built automatically from entity REF fields. When an entity is
created with a REF field, xolu adds a directed edge from that entity to the
referenced entity. The edge label is the field name.

```json
{
  "type": "REF",
  "entity": "users",
  "id": 42
}
```

A field named `"author"` with the above value creates the edge:
`(current_entity) -[:author]-> (users:42)`.

Node IDs in Sulpher have the form `entity_type:id` — for example `users:1`,
`posts:7`. In tenant-scoped queries the executor strips the tenant prefix
transparently; queries use the plain form.

Property access (WHERE conditions, RETURN fields) requires entity data from
the store. Properties are hydrated lazily — the store is queried at most once
per node per query execution, and only when a condition or projection
references a property.

---

## Algorithm selection

Default algorithm is BFS. Select DFS with a leading comment:

```cypher
// sulpher.algorithm: dfs
MATCH (u:user)-[:FOLLOWS]->(f:user) RETURN f
```

The comment must be on the first line, before any clause keyword.
Legacy prefix forms `BFS MATCH ...` and `DFS MATCH ...` are also accepted
for backward compatibility.

BFS produces a result for every reachable (start, end) pair, with shared
hub nodes reachable from all origins independently (multi-source BFS is
complete). Result order is deterministic (lexicographic node ID) within a
single origin.

---

## MATCH patterns

### Node patterns

```cypher
MATCH (n)                    -- any node
MATCH (n:user)               -- nodes of type "user"
MATCH (n:user {id: '1'})    -- inline property filter
MATCH (n:user {active: true, role: 'admin'})  -- multiple inline properties
```

The inline `{key: value}` filter checks properties from the store. `id`
matches the entity ID portion of the node's internal ID string.

### Relationship patterns

```cypher
MATCH (a)-[:FOLLOWS]->(b)    -- outgoing
MATCH (a)<-[:FOLLOWS]-(b)    -- incoming
MATCH (a)-[:FOLLOWS]-(b)     -- undirected (both directions)
MATCH (a)-[r:FOLLOWS]->(b)   -- bind relationship to variable r
MATCH (a)-[]->(b)            -- any relationship type, outgoing
MATCH (a)-->(b)              -- same as above
```

### Variable-length paths

```cypher
MATCH (a)-[*]->(b)           -- any number of hops (outgoing)
MATCH (a)-[*1..3]->(b)       -- 1 to 3 hops
MATCH (a)-[*2..]->(b)        -- at least 2 hops
MATCH (a)-[*..4]->(b)        -- up to 4 hops
MATCH (a)-[:FOLLOWS*]->(b)   -- any hops, specific label
MATCH (a)-[:FOLLOWS*1..3]->(b) -- 1 to 3 hops, specific label
MATCH (a)-[*3]->(b)          -- exactly 3 hops
```

### Multi-hop (chained) patterns

Two relationships in the same MATCH clause:

```cypher
MATCH (a:user)-[:FOLLOWS]->(b:user)-[:FOLLOWS]->(c:user) RETURN c
```

### Comma-separated (Cartesian product) patterns

Multiple independent node patterns in one MATCH produce a cross-join:

```cypher
MATCH (u:user), (p:product) RETURN u.name, p.name
-- 2 users × 2 products = 4 rows

MATCH (u:user), (p:product)-[:TAGGED]->(t:tag) RETURN u.name, t.label
-- node set × traversal result
```

---

## WHERE predicates

### Comparison operators

```cypher
WHERE u.age = 30
WHERE u.age <> 30
WHERE u.age > 25
WHERE u.age >= 25
WHERE u.age < 35
WHERE u.age <= 35
WHERE u.name = 'Alice'
WHERE u.name < 'Carol'       -- lexicographic string comparison
```

### Boolean operators

```cypher
WHERE u.age > 25 AND u.active = true
WHERE u.age < 25 OR u.age > 35
WHERE NOT u.active = true
WHERE NOT (u.name = 'Alice' AND u.active = true)
```

### Null checks

```cypher
WHERE u.email IS NULL
WHERE u.email IS NOT NULL
WHERE u.email IS NULL AND u.active = true
```

### String predicates

```cypher
WHERE u.email STARTS WITH 'alice'
WHERE u.name ENDS WITH 'ol'
WHERE u.email CONTAINS 'example'
WHERE NOT u.email STARTS WITH 'alice'
WHERE NOT u.email CONTAINS 'example'
```

### Regex

```cypher
WHERE u.email =~ '.*@example\\.com'   -- Go RE2 syntax
WHERE u.name  =~ '(?i)alice'          -- case-insensitive flag
WHERE u.name  =~ 'Ali'                -- substring match (no anchors)
WHERE u.name  =~ '^Alice$'            -- full-string match (use anchors)
```

`=~` uses Go's `regexp.MatchString`, which matches substrings unless the
pattern is anchored. An invalid regex pattern matches nothing (no panic).

### IN list

```cypher
WHERE u.age IN [25, 30, 35]
WHERE u.name IN ['Alice', 'Carol']
WHERE u.age NOT IN [25, 30]
WHERE u.name IN ['Bob']              -- single-element list
WHERE u.age IN []                    -- empty list — never matches
```

### Arithmetic in WHERE

```cypher
WHERE u.score + 10 > 100
WHERE u.score * 2 < 200
WHERE u.total / u.count > 25
```

---

## RETURN clause

### Property access

```cypher
RETURN u.name
RETURN u.name, u.age
```

Result keys use the `variable.property` form: `u.name`, `u.age`.

### Whole-node

```cypher
RETURN u
```

Returns a map containing all hydrated entity fields plus `_id` (the full
internal node ID string).

### Aliases

```cypher
RETURN u.name AS username
RETURN u.name AS n, u.age AS a
RETURN u.name AS n, u.age AS years
```

### Star — all bound variables

```cypher
RETURN *
MATCH (u:user)-[:FOLLOWS]->(f:user) RETURN *   -- both u and f
```

### Arithmetic expressions

```cypher
RETURN u.age + 1 AS nextAge
RETURN u.age * 2 AS doubled
RETURN u.total / u.count AS avg
RETURN u.age - 5 AS adjusted
```

### DISTINCT

```cypher
RETURN DISTINCT u.role
MATCH (u)-[:FOLLOWS]->(f) RETURN DISTINCT f
```

### ORDER BY

```cypher
RETURN u ORDER BY u.name
RETURN u ORDER BY u.age DESC
RETURN u ORDER BY u.name ASC, u.age DESC
```

String and numeric types are ordered correctly. ORDER BY on aggregated results
(after grouping) orders the groups.

### LIMIT and SKIP

```cypher
RETURN u LIMIT 10
RETURN u SKIP 5
RETURN u ORDER BY u.age SKIP 2 LIMIT 3
RETURN u SKIP 0                 -- same as no SKIP
RETURN u SKIP 100               -- returns 0 rows when count < 100
```

### Path projections

When a query binds a path variable (`p`), the following are available:

```cypher
MATCH p = shortestPath((a:user {id: '1'})-[:knows*]->(b:user {id: '3'}))
RETURN p.length          -- number of hops (integer)
RETURN p.nodes           -- list of node maps
RETURN p.relationships   -- list of {type: "label"} maps
RETURN p                 -- the full path object
```

---

## Aggregation

Aggregation in RETURN uses Cypher's implicit GROUP BY: non-aggregate items
are grouping keys; aggregate functions reduce the remaining rows.

### Functions

```cypher
RETURN count(*) AS n
RETURN count(u) AS n                      -- non-null nodes
RETURN count(u.email) AS n                -- non-null values only
RETURN count(DISTINCT u.role) AS n        -- distinct non-null values

RETURN collect(u.name) AS names           -- list of non-null values
RETURN sum(u.age) AS total
RETURN avg(u.age) AS mean
RETURN min(u.age) AS youngest
RETURN max(u.age) AS oldest
```

### Grouping

```cypher
MATCH (u:user) RETURN u.active, count(u) AS n
-- One row per distinct value of u.active

MATCH (u:user) RETURN u.dept, collect(u.name) AS names, avg(u.score) AS mean
```

### Multiple aggregates

```cypher
MATCH (u:user)
WHERE u.age IS NOT NULL
RETURN count(u) AS n, sum(u.age) AS total, avg(u.age) AS mean
```

### ORDER BY on aggregates

```cypher
MATCH (u:user) RETURN u.dept, count(u) AS n ORDER BY n DESC
```

---

## WITH pipeline

WITH passes results from one clause to the next, optionally aggregating,
filtering, or projecting.

### Property and scalar projection

```cypher
MATCH (u:user)
WITH u.name AS name, u.score AS score
WHERE score > 75
RETURN name, score
```

### Node passthrough

```cypher
MATCH (a:user {id: '1'})-[:knows]->(f:user)
WITH f
WHERE f.active = true
MATCH (f)-[:knows]->(b:user)
RETURN f.name, b.name
```

### Aggregation in WITH

```cypher
MATCH (u:user)
WITH u.active AS active, count(u) AS n
WHERE n > 1
RETURN active, n
```

All aggregation functions are available in WITH: `count`, `collect`, `sum`,
`avg`, `min`, `max`.

### Chained WITH (multiple stages)

```cypher
MATCH (u:user)
WITH u.name AS name, u.dept AS dept, u.score AS score
WHERE dept = 'eng'
WITH name, score
WHERE score > 75
RETURN name, score
```

### WITH and MATCH interleaved

```cypher
MATCH (a:user {id: '1'})-[:knows]->(f:user)
WITH f
MATCH (f)-[:knows]->(b:user)
RETURN f.name, b.name
```

### SKIP in WITH

```cypher
MATCH (a:user {id: '1'})-[:knows]->(f:user)
WITH f SKIP 1
MATCH (f)-[:knows]->(b:user)
RETURN b.name
```

---

## OPTIONAL MATCH

Left-join semantics. Unmatched optional variables are `null` in the result.

### Single OPTIONAL MATCH

```cypher
MATCH (u:user {id: '1'})
OPTIONAL MATCH (u)-[:knows]->(f:user)
RETURN u.name, f.name          -- f.name is null if no match
```

### OPTIONAL MATCH for all nodes

```cypher
MATCH (u:user)
OPTIONAL MATCH (u)-[:knows]->(f:user)
RETURN u.name, f.name
-- Returns all users; f.name is null for users with no outgoing knows edge
```

### Multiple sequential OPTIONAL MATCH

```cypher
MATCH (u:user {id: '1'})
OPTIONAL MATCH (u)-[:knows]->(f:user)
OPTIONAL MATCH (f)-[:knows]->(g:user)
RETURN u.name, f.name, g.name
-- Chains: unmatched f produces null g; all rows have u.name
```

---

## Path queries

### shortestPath

```cypher
MATCH p = shortestPath((a:user {id: '1'})-[:knows*]->(b:user {id: '3'}))
RETURN p.length, p.nodes, p.relationships

-- Incoming direction
MATCH p = shortestPath((a:user {id: '3'})<-[:knows*]-(b:user {id: '1'}))
RETURN p.length

-- Undirected
MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '4'}))
RETURN p.length
```

When no path exists, the query returns 0 rows (not an error).

### allShortestPaths

```cypher
MATCH p = allShortestPaths((a:user {id: '1'})-[:knows*]->(b:user {id: '3'}))
RETURN p.length, p.nodes
-- Returns one row per shortest path when multiple exist at the same length
```

### Path properties

| Property | Type | Description |
|---|---|---|
| `p.length` | integer | Number of hops |
| `p.nodes` | list | Node maps along the path |
| `p.relationships` | list | `{type: "label"}` maps for each edge |

---

## Path comprehension and quantifiers

These apply to node lists derived from `nodes(p)` or other list expressions.

### Quantifiers

```cypher
MATCH p = shortestPath((a:user {id: '1'})-[:knows*]->(b:user))
WHERE ALL(n IN nodes(p) WHERE n.active = true)
RETURN b.name, p.length

WHERE ANY(n IN nodes(p) WHERE n.name = 'Bob')
WHERE NONE(n IN nodes(p) WHERE n.active = false)
WHERE SINGLE(n IN nodes(p) WHERE n.name = 'Bob')
```

### List comprehension

```cypher
MATCH p = shortestPath((a:user {id: '1'})-[:knows*]->(b:user))
RETURN [n IN nodes(p) WHERE n.active = true | n.name] AS activeNames
```

### ALL/ANY/NONE/SINGLE on IN lists

```cypher
WHERE ALL(x IN [1, 2, 3] WHERE x > 0)
WHERE ANY(x IN [1, -1, 2] WHERE x < 0)
WHERE NONE(x IN [1, 2, 3] WHERE x > 10)
WHERE SINGLE(x IN [1, 2, 2] WHERE x = 1)
```

---

## UNION and UNION ALL

Both halves must return the same column names.

### UNION — with deduplication

```cypher
MATCH (u:user {id: '1'}) RETURN u.name AS name
UNION
MATCH (u:user {id: '2'}) RETURN u.name AS name
```

Rows that are identical across all columns are deduplicated.

### UNION ALL — no deduplication

```cypher
MATCH (u:user {id: '1'}) RETURN u.name AS name
UNION ALL
MATCH (u:user {id: '1'}) RETURN u.name AS name
-- Returns 2 rows (duplicates preserved)
```

### Three or more parts

```cypher
MATCH (u:user {id: '1'}) RETURN u.name AS name
UNION
MATCH (u:user {id: '2'}) RETURN u.name AS name
UNION
MATCH (u:user {id: '3'}) RETURN u.name AS name
```

---

## UNWIND

Expands a list into one row per element.

```cypher
UNWIND [1, 2, 3] AS x RETURN x
UNWIND ['alice', 'bob', 'carol'] AS name RETURN name
UNWIND [] AS x RETURN x             -- 0 rows
UNWIND [1, 2, 3] AS x RETURN x * 2 AS doubled
UNWIND [1, 2, 2, 3, 1] AS x RETURN DISTINCT x   -- 3 rows
UNWIND [1, 2, 3, 4, 5] AS x RETURN x SKIP 2
UNWIND [10, 20, 30, 40, 50] AS x RETURN x LIMIT 3
```

### UNWIND then MATCH

UNWIND can precede a MATCH clause. The UNWIND variable is available in the
subsequent MATCH, including in inline property filters:

```cypher
UNWIND ['user', 'product'] AS entityType
MATCH (n)
WHERE labels(n)[0] = entityType
RETURN n

UNWIND ['1', '2'] AS uid
MATCH (u:user {id: uid})
RETURN u.name
```

---

## Built-in functions

### String functions

```cypher
toUpper(u.name)      -- "ALICE"
toLower(u.name)      -- "alice"
trim(u.name)         -- strips leading and trailing whitespace
toString(u.age)      -- converts any value to its string representation
```

### Numeric functions

```cypher
abs(u.delta)         -- absolute value
toInteger(u.score)   -- converts numeric types and numeric strings to int
toFloat(u.score)     -- converts numeric types and numeric strings to float64
```

`toInteger` and `toFloat` accept any numeric Go type, booleans, and strings
that represent valid numbers (e.g. `toInteger("42")` → `42`,
`toFloat("3.14")` → `3.14`). Strings that cannot be parsed as a number
return `null`. This means DECIMAL fields stored as strings work correctly
with arithmetic expressions and aggregate functions.

### List functions

```cypher
size(someList)       -- length of a list
size(nodes(p))       -- number of nodes in a path
head(someList)       -- first element, or null for empty list
last(someList)       -- last element, or null for empty list
tail(someList)       -- all elements except the first (empty list if ≤1 element)
```

### Path functions

```cypher
nodes(p)             -- list of node maps for path p
relationships(p)     -- list of {type: "label"} maps for path p
length(p)            -- number of hops (synonym for p.length)
```

`length()` also accepts a list and returns its length.

### Null handling

```cypher
coalesce(u.email, 'unknown')     -- first non-null argument
coalesce(u.a, u.b, u.c, 'none') -- variadic
```

### Node introspection

```cypher
labels(u)            -- ["user"] — list containing the entity type
id(u)                -- entity ID portion of the internal node ID
```

Note: `id(u)` returns the ID string (e.g. `"42"`), not an integer.

### Relationship introspection

```cypher
type(r)              -- returns the relationship label as a string
```

`type(r)` works for both directly-bound relationship variables
(`-[r:KNOWS]->`) and path relationships. It returns the relationship label
string (e.g. `"KNOWS"`), or `null` if `r` is not bound to a relationship.

### Deprecated

```cypher
exists(u.name)       -- returns true/false; prefer u.name IS NOT NULL
```

The `exists()` function form may be rejected by the parser (the `u.name`
argument conflicts with the Cypher grammar). Use `IS NOT NULL`.

---

## Sending a query

### Sync

```http
POST /api/v1/graph/query
Content-Type: application/json

{"query": "MATCH (u:user)-[:knows]->(f:user) RETURN f.name", "max_depth": 5}
```

`max_depth` defaults to `XOLU_GRAPH_MAX_VISITED_NODES` (default 10,000).

**Response:**

```json
{
  "result": [{"f.name": "Alice"}, {"f.name": "Bob"}],
  "stats": {
    "nodes_traversed":   48,
    "paths_found":       6,
    "execution_time_ms": 3
  }
}
```

The `X-Cache: HIT` / `X-Cache: MISS` header is present when query result
caching is enabled (`XOLU_GRAPH_QUERY_CACHE_TTL > 0`, default 30 s).

### Async

```http
POST /api/v1/graph/query/async
{"query": "...", "max_depth": 5}
```

Returns `{"query_id": "...", "status": "pending"}`. Poll via
`GET /api/v1/graph/query/{id}`, retrieve via
`GET /api/v1/graph/query/{id}/result`.
See [ASYNC_QUERIES.md](ASYNC_QUERIES.md).

### Tenant-scoped

All endpoints have `/api/v1/tenant/{tenant_id}/graph/...` equivalents.
In `strict` mode the tenant-scoped routes are the only available routes.

---

## Remaining gaps

These are OC9 features not implemented in Sulpher.

### Relationship properties

`WHERE r.since > 2020` — relationships have only a label, no property map.
This predicate silently returns false (no error, no results).

### `MERGE`

Not supported. Graph mutations go through the entity REST API.

### `FOREACH`

Not supported.

### `CALL` / subqueries

`CALL { MATCH ... RETURN ... }` — not supported.

### Multiple named paths in one MATCH

`MATCH p1 = ..., p2 = ...` — not supported.

### Full `WITH` aggregation with multiple MATCH stages

`MATCH ... WITH count(*) AS n MATCH ... RETURN n` — the second MATCH after
an aggregating WITH is not supported. The aggregate result passes through
correctly but another full traversal cannot be initiated from it.

---

## Semantic differences from Neo4j

**Regex matching** — Sulpher uses Go's `regexp.MatchString`, which matches
substrings by default. `WHERE u.name =~ 'Ali'` matches `"Alice"`. Neo4j
uses full-string anchored matching. Use `^...$` anchors for full-string
behaviour.

**Graph direction** — xolu's graph is always directed. Edges are created from
the entity that declares a REF field. Undirected patterns (`-[:LABEL]-`)
traverse both directions correctly, but the underlying data is directed.
Symmetrical relationships require REF fields on both entities.

**`[*3]` exact hop count** — `[*3]` means exactly 3 hops. `[*3..]` means
at least 3 hops. This matches the OC9 specification; earlier Sulpher versions
treated `[*3]` as "up to 3 hops".

**`exists()`** — `exists(u.name)` may be rejected by the parser. Use
`u.name IS NOT NULL` instead. This matches the OC9 recommendation (the
function form was deprecated in OC9).

**`id(n)` returns a string** — `id(u)` returns the entity ID as a string
(e.g. `"42"`), not an integer. Compare with string literals: `WHERE id(u) = '42'`.

**Multi-source BFS completeness** — When a query matches multiple start
nodes that converge on a shared hub, Sulpher's BFS produces a result row
for each origin independently. This matches OC9 semantics.
