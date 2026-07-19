# xolu Graph API Reference

> **Status: production-ready** — The graph layer is fully tenant-isolated and
> safe to use in both `path` (single-tenant) and `strict` (multi-tenant) modes.
> Tenant isolation is enforced at the graph snapshot layer, the handler layer,
> and the edge layer. All 12 handler surfaces and the Sulpher query engine are
> covered by an adversarial isolation test suite. See `CHANGELOG.md` [v0.9.5].

## Overview

xolu provides a graph layer for navigating relationships between entities. The graph is automatically maintained when entities with REF fields are created, updated, or deleted.

Graph features are available in both operational modes:

- **Single-tenant** (`XOLU_TENANT_MODE=path`): graph routes at `/api/v1/graph/...` and `/api/v1/sulpher/...`
- **Multi-tenant** (`XOLU_TENANT_MODE=strict`): graph routes at `/api/v1/tenant/{tenant_id}/graph/...` and `/api/v1/tenant/{tenant_id}/sulpher/...`

In strict mode the graph layer is fully tenant-isolated. Node IDs in requests and responses use the client-facing `entity:id` format; the internal `XXXX@entity:id` tenant prefix is added and stripped transparently. Cross-tenant edge leakage is enforced at both the snapshot layer (graph traversal) and the handler layer (node info, degree, in/out edges). The isolation guarantee is covered by an adversarial integration test suite (`graph_tenant_exhaustive_test.go`).

**Query Languages:**

| Language | Documentation | Best For |
|----------|---------------|----------|
| **Sulpher** | This document | Graph traversal, paths, relationships |
| **OQL** | [OQL_API.md](OQL_API.md) | SQL queries, aggregates, bulk mutations |

## Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/graph/stats` | Graph-wide statistics |
| POST | `/api/v1/graph/path` | Find path between two nodes (returns path array) |
| POST | `/api/v1/graph/neighbors` | Get neighbours of a node (by direction) |
| GET | `/api/v1/graph/nodes/{node_id}` | Full node info with edges |
| GET | `/api/v1/graph/nodes/{node_id}/degree` | In/out degree counts |
| GET | `/api/v1/graph/{node_id}/in` | Incoming edges |
| GET | `/api/v1/graph/{node_id}/out` | Outgoing edges |
| POST | `/api/v1/graph/shortestPath` | Find shortest path (BFS, includes `exists` flag) |
| POST | `/api/v1/graph/pathExists` | Check if path exists without computing it |
| POST | `/api/v1/graph/commonNeighbors` | Find shared outgoing neighbours |
| POST | `/api/v1/graph/nodes/search` | Search nodes by entity type |
| POST | `/api/v1/graph/query` | Execute a Sulpher query (sync) |
| POST | `/api/v1/graph/query/async` | Submit a Sulpher query (async) |
| GET | `/api/v1/graph/query/{query_id}` | Poll async Sulpher query status |
| GET | `/api/v1/graph/query/{query_id}/result` | Retrieve async Sulpher query result |

## Node ID Format

Nodes are identified as `{entity}:{id}`, e.g., `items:42`, `records:7`.

---

## Endpoint Details

### GET /api/v1/graph/stats

Returns graph-wide statistics.

**Response:**
```json
{
  "node_count": 150,
  "edge_count": 342,
  "has_cycle": false
}
```

---

### GET /api/v1/graph/nodes/{node_id}

Returns comprehensive information about a node.

**Example:** `GET /api/v1/graph/nodes/items:42`

**Response:**
```json
{
  "id": "items:42",
  "entity": "items",
  "entity_id": 42,
  "outgoing": {
    "locations:5": "location_ref",
    "records:12": "record_ref"
  },
  "incoming": {
    "events:7": "item_ref"
  },
  "degree": {
    "in": 1,
    "out": 2,
    "total": 3
  }
}
```

---

### GET /api/v1/graph/nodes/{node_id}/degree

Returns degree counts for a node.

For adapted entities that have no REF fields — and therefore no edges — the
endpoint returns `{in:0, out:0, total:0}` rather than 404. Non-existent nodes
still return 404.

**Example:** `GET /api/v1/graph/nodes/items:42/degree`

**Response:**
```json
{
  "node_id": "items:42",
  "degree": {
    "in": 1,
    "out": 2,
    "total": 3
  }
}
```

---

### GET /api/v1/graph/{node_id}/in

Returns all incoming edges to a node.

**Example:** `GET /api/v1/graph/items:42/in`

**Response:**
```json
{
  "node_id": "items:42",
  "edges": [
    {
      "source": "events:7",
      "target": "items:42",
      "relationship": "item_ref"
    }
  ],
  "count": 1
}
```

---

### GET /api/v1/graph/{node_id}/out

Returns all outgoing edges from a node.

**Example:** `GET /api/v1/graph/items:42/out`

**Response:**
```json
{
  "node_id": "items:42",
  "edges": [
    {
      "source": "items:42",
      "target": "locations:5",
      "relationship": "location_ref"
    },
    {
      "source": "items:42",
      "target": "records:12",
      "relationship": "record_ref"
    }
  ],
  "count": 2
}
```

---

### POST /api/v1/graph/shortestPath

Finds the shortest path between two nodes.

**Request:**
```json
{
  "from": "items:42",
  "to": "groups:3",
  "max_depth": 10
}
```

**Response (path found):**
```json
{
  "from": "items:42",
  "to": "groups:3",
  "exists": true,
  "path": ["items:42", "records:12", "groups:3"],
  "length": 2
}
```

**Response (no path):**
```json
{
  "from": "items:42",
  "to": "groups:99",
  "exists": false,
  "path": null,
  "length": 0
}
```

---

### POST /api/v1/graph/pathExists

Efficiently checks if a path exists (without computing full path).

**Request:**
```json
{
  "from": "items:42",
  "to": "groups:3",
  "max_depth": 10
}
```

**Response:**
```json
{
  "from": "items:42",
  "to": "groups:3",
  "exists": true,
  "length": 2
}
```

---

### POST /api/v1/graph/commonNeighbors

Finds nodes that both `node_a` and `node_b` have outgoing edges to — i.e.
shared out-neighbours in the directed graph. Nodes that point *to* `node_a`
or `node_b` via incoming edges are not included.

**Request:**
```json
{
  "node_a": "items:42",
  "node_b": "items:57"
}
```

**Response:**
```json
{
  "node_a": "items:42",
  "node_b": "items:57",
  "common": ["locations:5", "groups:3"],
  "count": 2
}
```

Use case: Finding shared outgoing relationships (e.g., "which location or group do
both these items reference?"). For shared in-neighbours, query the incoming edges
of each node separately and intersect the results in the application layer.

---

### POST /api/v1/graph/nodes/search

Searches for nodes by entity type.

**Request:**
```json
{
  "entity": "items",
  "limit": 100
}
```

**Response:**
```json
{
  "nodes": ["items:1", "items:2", "items:42", "..."],
  "count": 42
}
```

When `entity` is specified and the entity has an adapted table (schema
registered via `POST /api/v1/schema/{entity}`), results are drawn directly
from the adapted table and include all entities of that type regardless of
whether they have any edges. For non-adapted entities the in-memory graph
index is used — only nodes with at least one REF edge are returned.

If `entity` is omitted, returns all nodes known to the graph (up to limit).

---

## Configuration

Graph features are controlled by the following environment variables:

| Env var | Default | Description |
|---------|---------|-------------|
| `XOLU_GRAPH_MODE` | `flat` | `flat` (enabled) or `disabled` |
| `XOLU_GRAPH_CYCLE_DETECTION` | `warn` | `warn`, `error`, or `ignore` |
| `XOLU_GRAPH_CYCLE_CHECK_LIMIT` | `512` | BFS node budget for cycle detection per `AddEdge` call |
| `XOLU_GRAPH_MAX_VISITED_NODES` | `10000` | Max nodes visited during a single traversal |
| `XOLU_GRAPH_MAX_RESULTS` | `10000` | Max result paths returned by a graph query |
| `XOLU_GRAPH_QUERY_CACHE_TTL` | `30` | Sulpher result cache TTL in seconds; `0` disables |

The default query depth limit is 10 hops and is not currently configurable via env var; use `max_depth` in individual query requests to override it per call.

### Cycle Detection

When the graph is configured with `cycle_detection: "warn"` or `cycle_detection: "error"`, xolu
runs a BFS from the target node back towards the source before committing any new edge. If a
path is found, the edge would create a cycle.

The BFS is bounded by a node-visit budget (`cycle_check_limit`, default **512**). When the budget
is exhausted before a cycle is confirmed or ruled out, the check returns **true conservatively**:

- In `"error"` mode this causes `AddEdge` to return `ErrCycleDetected` (HTTP 409), even though
  no actual cycle was detected. A caller adding a legitimate edge to a large or dense graph may
  receive a 409 with no way to distinguish "genuine cycle" from "budget exhausted".
- In `"warn"` mode a log event is emitted but the edge is still added.
- In `"ignore"` mode (the default) no check is performed; budget is irrelevant.

**Implications for operators:**

- On graphs with more than a few hundred nodes per connected component, consider raising
  `cycle_check_limit` or switching to `"warn"` mode if false positives are observed.
- The error message for budget exhaustion and genuine cycle detection is identical at the API
  level (`"adding this edge would create a cycle"`). There is currently no way to distinguish
  the two from outside the server.

---

## Compatibility with rserv

Endpoint mapping for teams migrating from rserv v0.5.3:

| rserv Endpoint | xolu Equivalent |
|----------------|----------------|
| `GET /api/v1/graph/nodes/{id}` | `GET /api/v1/graph/nodes/{node_id}` |
| `GET /api/v1/graph/nodes/{id}/degree` | `GET /api/v1/graph/nodes/{node_id}/degree` |
| `GET /api/v1/graph/{entity}:{id}/in` | `GET /api/v1/graph/{node_id}/in` |
| `GET /api/v1/graph/{entity}:{id}/out` | `GET /api/v1/graph/{node_id}/out` |
| `POST /api/v1/graph/shortestPath` | `POST /api/v1/graph/shortestPath` |
| `POST /api/v1/graph/pathExists` | `POST /api/v1/graph/pathExists` |
| `POST /api/v1/graph/commonNeighbors` | `POST /api/v1/graph/commonNeighbors` |

**Note:** Full-text search is available via `GET /api/v1/search`.

---

## Sulpher Query Language

Sulpher is xolu's graph query language. It provides a Cypher-like syntax for traversing and querying the graph.

### Syntax

```
[BFS|DFS] MATCH <pattern> [WHERE <conditions>] RETURN <items>
```

**Components:**

| Component | Description | Example |
|-----------|-------------|---------|
| Algorithm | Optional traversal algorithm (default: BFS) | `BFS`, `DFS` |
| Pattern | Node and relationship patterns | `(u:User)-[r:FOLLOWS]->(f:User)` |
| WHERE | Optional filter conditions | `WHERE u.id = 123 AND u.active = true` |
| RETURN | Fields to return | `RETURN u, f.name` |

### Node Patterns

```
(variable:Type)           -- Variable and type
(variable:Type {props})   -- With inline properties
(variable)                -- Variable only (matches any type)
```

**Examples:**
```
(u:User)                      -- User node assigned to variable 'u'
(u:User {id: 123})            -- User with id=123
(u:User {active: true})       -- Active users
(p:Post {status: "published"}) -- Published posts
```

### Relationship Patterns

```
-[variable:TYPE]->            -- Directed relationship (single hop)
-[:TYPE]->                    -- Type only (no variable)
-[variable]->                 -- Variable only (any type)
-[]->                         -- Any relationship

-- Variable-length patterns:
-[:TYPE*1..5]->               -- 1 to 5 hops
-[:TYPE*..3]->                -- 1 to 3 hops (min defaults to 1)
-[:TYPE*2..]->                -- 2+ hops (uses max_depth limit)
-[:TYPE*]->                   -- 1+ hops (uses max_depth limit)
-[:TYPE*3]->                  -- Exactly 3 hops
-[r:TYPE*1..5]->              -- With variable binding
-[*1..3]->                    -- Any type, 1-3 hops
```

**Examples:**
```
-[r:FOLLOWS]->                -- FOLLOWS relationship
-[:MANAGES]->                 -- MANAGES (no variable needed)
-[r]->                        -- Any relationship, capture as 'r'
-[:FOLLOWS*1..3]->            -- 1-3 hops via FOLLOWS
-[*2..5]->                    -- 2-5 hops via any relationship
```

### WHERE Conditions

Conditions are joined with `AND`. Supported operators: `=`, `!=`, `<`, `>`, `<=`, `>=`

```
WHERE u.age >= 18
WHERE u.name = "Alice" AND u.active = true
WHERE f.score > 100
```

### RETURN Clause

Return whole nodes or specific properties:

```
RETURN u                      -- Whole node
RETURN u.name                 -- Specific property
RETURN u, f, u.name, f.email  -- Multiple items
```

---

### Sulpher Endpoints

#### POST /api/v1/graph/query

Execute a Sulpher query synchronously.

**Request:**
```json
{
  "query": "MATCH (u:User)-[:FOLLOWS]->(f:User) WHERE u.id = 123 RETURN f",
  "max_depth": 10
}
```

**Response:**
```json
{
  "status": "completed",
  "result": [
    {"f": {"_id": "User:456", "type": "User", "name": "Bob"}},
    {"f": {"_id": "User:789", "type": "User", "name": "Carol"}}
  ],
  "stats": {
    "nodes_traversed": 15,
    "paths_found": 2,
    "execution_time_ms": 5
  }
}
```

#### POST /api/v1/graph/query/async

Submit a query for asynchronous execution.

**Request:**
```json
{
  "query": "DFS MATCH (u:User)-[:FOLLOWS]->(f:User)-[:LIKES]->(p:Post) RETURN p",
  "max_depth": 5
}
```

**Response:**
```json
{
  "query_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "pending",
  "created_at": "2024-01-15T10:30:00Z"
}
```

#### GET /api/v1/graph/query/{query_id}

Check the status of an async query.

**Response:**
```json
{
  "query_id": "550e8400-e29b-41d4-a716-446655440000",
  "query": "DFS MATCH (u:User)-[:FOLLOWS]->(f:User)-[:LIKES]->(p:Post) RETURN p",
  "status": "completed",
  "created_at": "2024-01-15T10:30:00Z",
  "started_at": "2024-01-15T10:30:00Z",
  "ended_at": "2024-01-15T10:30:01Z"
}
```

#### GET /api/v1/graph/query/{query_id}/result

Retrieve results of a completed async query.

**Response:**
```json
{
  "query_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "completed",
  "result": [...],
  "stats": {
    "nodes_traversed": 150,
    "paths_found": 12,
    "execution_time_ms": 45
  }
}
```

---

### Example Queries

**Simple node lookup:**
```
MATCH (u:User {id: 123}) RETURN u
```

**Single-hop traversal:**
```
MATCH (u:User)-[:FOLLOWS]->(f:User) WHERE u.id = 123 RETURN f
```

**Multi-hop traversal:**
```
MATCH (u:User)-[:FOLLOWS]->(f:User)-[:LIKES]->(p:Post) RETURN p
```

**DFS traversal:**
```
DFS MATCH (a:items)-[:record_ref]->(s:records) WHERE a.status = "active" RETURN s
```

**Return specific properties:**
```
MATCH (u:User)-[r:MANAGES]->(e:Employee) RETURN u.name, e.email
```

**With multiple conditions:**
```
MATCH (a:items) WHERE a.status = "active" AND a.priority > 5 RETURN a
```

**Variable-length paths (1-5 hops):**
```
MATCH (u:User)-[:FOLLOWS*1..5]->(f:User) RETURN f
```

**Find all reachable nodes (any depth up to max_depth):**
```
MATCH (a:items)-[:connected_to*]->(b:items) WHERE a.id = 1 RETURN b
```

**Exactly 3 hops:**
```
MATCH (u:User)-[:FOLLOWS*3]->(f:User) RETURN f
```

**At least 2 hops:**
```
MATCH (u:User)-[:FOLLOWS*2..]->(f:User) RETURN f
```

---

### Limitations

| Feature | Status |
|---------|--------|
| BFS/DFS traversal | ✓ Supported |
| Node type matching | ✓ Supported |
| Inline properties | ✓ Supported |
| Relationship types | ✓ Supported |
| WHERE with AND | ✓ Supported |
| WHERE with OR | ✓ Supported |
| Comparison operators | ✓ Supported |
| Property returns | ✓ Supported |
| Variable-length paths `*1..5` | ✓ Supported |
| DISTINCT | ✓ Supported |
| LIMIT | ✓ Supported |
| ORDER BY | ✓ Supported |
| Incoming relationships `<-[]-` | ✓ Supported |
| Bidirectional `-[]-` | ✓ Supported |
| OPTIONAL MATCH | ✗ Future |

**Note:** For SQL-style aggregates (COUNT, SUM, AVG, MIN, MAX) and bulk mutations (UPDATE, DELETE with WHERE), see [OQL_API.md](OQL_API.md).

---

## Advanced Features

### DISTINCT

Remove duplicate results:

```
MATCH (u:User)-[:FOLLOWS*1..3]->(f:User) RETURN DISTINCT f
```

### LIMIT

Limit the number of results:

```
MATCH (u:User) RETURN u LIMIT 10
```

### ORDER BY

Sort results by one or more fields:

```
MATCH (u:User) RETURN u ORDER BY u.name
MATCH (u:User) RETURN u ORDER BY u.age DESC
MATCH (u:User) RETURN u ORDER BY u.name ASC, u.age DESC
```

### OR in WHERE

Combine conditions with OR (groups are AND-joined):

```
-- Match Alice OR Bob
MATCH (u:User) WHERE u.name = 'Alice' OR u.name = 'Bob' RETURN u

-- (age > 18 AND active) OR (role = 'admin' AND verified)
MATCH (u:User) WHERE u.age > 18 AND u.active = true OR u.role = 'admin' AND u.verified = true RETURN u
```

### Relationship Directions

**Outgoing (default):**
```
MATCH (u:User)-[:FOLLOWS]->(f:User) RETURN f
```

**Incoming:**
```
MATCH (u:User)<-[:FOLLOWS]-(f:User) RETURN f
```

**Bidirectional (either direction):**
```
MATCH (u:User)-[:KNOWS]-(f:User) RETURN f    -- undirected
MATCH (u:User)<-[:KNOWS]->(f:User) RETURN f  -- both arrows
```

### Combined Example

```
MATCH (u:User)-[:FOLLOWS*1..3]->(f:User)
WHERE u.id = 123 OR u.role = 'influencer'
RETURN DISTINCT f
ORDER BY f.followers DESC
LIMIT 20
```

---

## When to Use Sulpher vs OQL

| Use Case | Recommended | Example |
|----------|-------------|---------|
| Find paths between nodes | **Sulpher** | `MATCH (a)-[:KNOWS*1..5]->(b) RETURN b` |
| Traverse relationships | **Sulpher** | `MATCH (u)-[:FOLLOWS]->(f) RETURN f` |
| Variable-length paths | **Sulpher** | `MATCH (u)-[:FOLLOWS*2..]->(f) RETURN f` |
| Graph pattern matching | **Sulpher** | `MATCH (a)-[r]->(b)<-[s]-(c) RETURN a,b,c` |
| Count records by category | **OQL** | `SELECT zone, COUNT(*) FROM records GROUP BY zone` |
| Calculate averages, sums | **OQL** | `SELECT AVG(value) FROM records` |
| Bulk update/delete | **OQL** | `UPDATE records SET status='off' WHERE zone=5` |
| Batch insert | **OQL** | `INSERT INTO records (a,b) VALUES (1,2),(3,4)` |

See [OQL_API.md](OQL_API.md) for full OQL documentation.


---

## Implementation Invariants

This document describes the invariants enforced by the three mutation methods
in `pkg/graph/flat_graph.go`, why they matter, and what to do when adding
a new one.

---

## The three methods and what they enforce

All mutations to the node map and type index go through one of these three
methods on `FlatGraph`. They are the single point of enforcement for every
correctness property the graph layer provides. The lock (`mu sync.RWMutex`)
is acquired by the public caller; the unexported `*Locked` variants assume
the lock is already held.

### `addNodeLocked(nodeID, nodeType string) error`

| Invariant | Consequence of bypassing |
|-----------|-----------------------------|
| `ErrMalformedNodeID` — rejects IDs with `@` that lack a valid `XXXX@` tenant prefix | Corrupt node enters graph; invisible to tenant isolation machinery; can produce ghost entries in tenant-scoped queries |
| `nodeCounters` — increments the per-tenant counter for new nodes | `NodeCountForTenant` returns stale (low) values; stats dashboard shows wrong node counts |
| Type index (`g.index`) — records `nodeID` under `nodeType` when `nodeType != ""`, even for nodes that already exist | `GetNodesByType` / `GetNodesByTypeForTenant` returns empty results for nodes whose type was assigned after implicit creation |

### `addEdgeLocked(from, to, relationship string) error`

| Invariant | Consequence of bypassing |
|-----------|-----------------------------|
| `ErrCrossTenantEdge` — endpoints with different non-empty tenant prefixes rejected | Edge silently bridges two tenants; traversal leaks data across tenant boundary |
| `ErrMalformedNodeID` via `addNodeLocked` — both endpoints validated | Malformed IDs enter graph through edge creation even if `AddNode` would reject them |
| `nodeCounters` via `addNodeLocked` — both endpoints created and counted if absent | Implicitly created nodes not counted; `NodeCountForTenant` returns stale values |
| Cycle detection — `wouldCreateCycle` checked before any state mutation | Cycle enters graph before rejection in `"error"` mode |
| `ErrEdgeAlreadyExists` — same pair with different relationship name rejected | Silent relationship rename; stale relationship label stored |
| `edgeCount` — incremented for genuinely new edges only | `EdgeCount` / `EdgeCountForTenant` over-counts |
| Incoming index (`rec.in`) — target node's incoming set updated | `GetIncomingEdges` returns incomplete results; degree counts wrong |

### `RemoveEdge(from, to string) error`

| Invariant | Consequence of bypassing |
|-----------|-----------------------------|
| `edgeCount` — decremented unconditionally when edge existed | Counter underflows; poisons subsequent edge counts |
| Incoming index — `toRec.in[from]` always cleaned up | Stale reverse entries survive after edge removal; `GetIncomingEdges` returns phantom predecessors |

---

## When you add a new invariant

All mutation state is private to `FlatGraph` (`g.nodes`, `g.index`,
`g.nodeCounters`, `g.edgeCount`). Writes from outside the
`addNodeLocked` / `addEdgeLocked` / `RemoveEdge` methods are not
automatically prevented by the compiler, so discipline is required.

To add a new invariant:

1. Add the check to the appropriate unexported method (`addNodeLocked`,
   `addEdgeLocked`) or to `RemoveEdge` directly.
2. Add a test in `pkg/graph/graph_contract_test.go` verifying the new
   invariant through the `Graph` interface.
3. Run `go test ./pkg/graph/...`.

To audit that no direct map writes bypass the helpers:

```bash
# All writes to g.nodes outside addNodeLocked should be initialisation only.
grep -n 'g\.nodes\[' pkg/graph/flat_graph.go
# All writes to g.index outside addNodeLocked should be initialisation only.
grep -n 'g\.index\[' pkg/graph/flat_graph.go
```

---

## The load path pattern

`Load` resets all state to empty and then replays every node and edge
through `addNodeLocked` / `addEdgeLocked`, exactly as at runtime. This
ensures all invariants — counters, indexes, cycle state — are rebuilt
correctly from the file contents without a separate reconciliation pass.

If you add a load path it must follow this pattern. Direct map assignment
during load bypasses every invariant listed above.

### Cycle-detection policy persistence

Since v0.9.7-patched40, `flatGraphData` includes `cycle_detection` and
`cycle_check_limit` fields. `Save` writes them; `Load` restores them when
present. Files from older versions that lack these keys are still valid —
`Load` leaves the constructor-supplied mode (`NewFlatGraphWithCycleDetection`)
intact for absent fields. This means a graph configured with `"error"` mode
will resume that mode after restart without any extra caller configuration.

Since v0.9.7-patched42, `Load` validates the `cycle_detection` value against
the three legal modes (`"ignore"`, `"warn"`, `"error"`) and returns an error
for any other value. Previously an unrecognised mode was silently stored;
`addEdgeLocked`'s switch has no default case, so a bad mode would trigger the
cycle-detection path but then admit the cycle silently.

---

## Error message hygiene

Node IDs passed to callers in error messages must not contain the `XXXX@`
tenant prefix, which is an internal implementation detail. Use
`tenant.NodeIDStripped(nodeID)` in any `fmt.Errorf` or `errors.New` call
that will be returned to a caller outside the graph package. The internal
`log.Printf` lines may retain full node IDs for diagnostics.

The contract was tightened in v0.9.7-patched42:
- `FlatGraph.CommonNeighbors` error (node not found)
- `FlatGraph.UpdateFromEntityForTenant` relabel/rollback errors

---

## `CommonNeighbors` return contract

`FlatGraph.CommonNeighbors` always returns a non-nil slice (guaranteed since
v0.9.7-patched42). Callers must not add `if result == nil` guards — the
guarantee belongs in the implementation, not at each call site. The `Graph`
interface documents this explicitly. Any future re-implementation must honour
the same contract.

---

## History note

Prior to v0.9.7-patched38, the graph layer had two implementations:
`IndexedGraph` (backed by a `pkg/graph/state` sub-package) and `FlatGraph`.
`IndexedGraph` and `pkg/graph/state` were removed in patched38; `FlatGraph`
is now the sole implementation. The design history of the state sub-package
is in `CHANGELOG.md` entries for v0.9.5–v0.9.7.

**v0.9.7-patched40 counter-correction fix:** `UpdateFromEntityForTenant`
contained an inverted counter correction in the double-failure path of the
edge-relabel code. When both the re-add and the rollback restore failed,
the code incremented `edgeCount`/`edgeCounters` — the opposite of the
comment's stated intent. The counters were already consistent (decremented)
and the erroneous increments caused a permanent over-count. Fixed in
patched40 by removing the two increment lines.

**v0.9.7-patched42 correctness and hygiene sweep:** (a) `CommonNeighbors`
now returns a non-nil empty slice instead of nil when there is no overlap;
(b) `Load` rejects unrecognised `cycle_detection` values rather than
silently accepting them; (c) `guardEdgeMap` allocates a fresh map instead
of mutating its argument; (d) error messages from `CommonNeighbors` and
`UpdateFromEntityForTenant` rollback paths now strip the `XXXX@` tenant
prefix via `tenant.NodeIDStripped`; (e) `handleTenantGraphPath` and
`handleTenantGraphShortestPath` share a `tenantPathResult` helper that
guarantees `length >= 0`.


## Query result caching

When `XOLU_GRAPH_QUERY_CACHE_TTL > 0` (default: 30 seconds), sync query results are cached
at the HTTP handler layer. Repeated identical queries (same query string and `max_depth`)
are served from cache without re-running the BFS.

The `X-Cache` response header indicates whether the result came from cache:

| `X-Cache` | Meaning |
|---|---|
| `MISS` | Result was computed and is now cached |
| `HIT` | Result was served from cache; no BFS executed |

The header is absent when caching is disabled (`XOLU_GRAPH_QUERY_CACHE_TTL=0`).

Cache entries are invalidated immediately on any graph mutation: entity writes
that carry REF edges, direct edge writes (`POST /graph/edges`), and graph rebuild.
After `XOLU_GRAPH_QUERY_CACHE_TTL` seconds, entries expire regardless of whether
a write occurred.

See [Caching](CACHING.md) for configuration details.
