# Sulpher Optimisation Roadmap

Updated: 2026-06-14 (written against v0.9.9; open items are
benchmark-gated — see the Measurement section — and deliberately not
register items in `docs/TRACKING.md`)

This document records known performance opportunities in the Sulpher executor,
ordered by implementation complexity and expected impact. Optimisations marked
**[done]** have been implemented.

---

## Background: where time goes

A Sulpher query spends time in roughly this order for a typical read-heavy
workload:

1. **Snapshot construction** — copying the adjacency map under a read lock.
   Cost is O(E) in edges, paid once per query.
2. **Start node scan** — `findMatchingNodesAST` iterating all nodes of the
   target type and applying inline property filters. Cost is O(N) in nodes of
   that type, paid for each start-node pattern.
3. **BFS/DFS traversal** — expanding neighbours and checking patterns at each
   hop. Cost is O(V + E) in the subgraph reachable from start nodes.
4. **WHERE evaluation** — evaluating the WHERE expression tree against each
   result Env. Cost is O(R * complexity(WHERE)) where R is result count.
5. **Hydration** — fetching entity properties from the store on first property
   access. Cost is O(H * store_latency) where H is unique hydrated nodes.
6. **RETURN projection** — building result maps from Envs. Cost is O(R).

For small graphs (< 10K nodes) steps 1-3 are negligible and hydration
dominates if many nodes are accessed. For larger graphs, the start node scan
and traversal become the bottleneck.

---

## Tier 0 — Implemented (v0.9.9)

### Whole-query result cache **[done]**

Sulpher query results are cached at the HTTP handler layer under a key derived
from `(tenantID, query string, maxDepth)` using FNV-64a. Cache hits return the
already-serialised JSON bytes with `X-Cache: HIT`. Writes (entity creates,
edge writes, graph rebuild) eagerly invalidate via `DeletePattern`. TTL is
controlled by `XOLU_GRAPH_QUERY_CACHE_TTL` (default 30s). When TTL is 0 the
cache is disabled and no `X-Cache` header is emitted.

### BFS completeness fix (origin-scoped visited set) **[done]**

The multi-seed BFS now uses `origin|node:segIdx` as the visited key instead of
`node:segIdx`. This ensures each start node has an independent traversal scope,
so shared hub nodes are reachable from multiple origins. Neighbours and start
nodes are sorted for deterministic result order. See changelog v0.9.9.

## Tier 1 — Intra-query (no new infrastructure, implement now)

### 1.1 Unified multi-start BFS **[done]**

**Problem:** BFS explores `user:1 → user:3 → user:7` and later
`user:2 → user:3 → user:7` independently. Everything downstream of `user:3`
at segment index 1 is recomputed.

**Note:** The BFS `visited` map already prevents re-*expanding* the same
`(node, segIdx)` pair within a single query execution. The remaining
redundancy is that different *start nodes* each run independent BFS instances
with independent visited maps, so hub nodes are expanded once per start node
rather than once globally.

**Fix:** For multi-start queries, run a single BFS instance with all start
nodes in the initial queue rather than running N independent BFS instances.
This requires the initial queue to be seeded with all start nodes at segIdx 0,
and the visited map to be shared across them. Hub nodes downstream of multiple
start nodes are then expanded only once.

**Scope:** `executeTraversalEnv` in `executor_env.go`. Change the outer loop
over start nodes into queue initialisation for a single BFS call.

**Expected impact:** High for all-type queries (`MATCH (u:User)`) where many
start nodes share downstream structure. Neutral for single-start queries.

---

### 1.2 `envDepth` O(1) via depth counter **[done]**

**Problem:** `envDepth` is called on every BFS queue item to enforce the depth
limit. It iterates the entire Env counting node-valued keys — O(k) in the
number of bindings. For a query with 5 bound variables, this is 5 map lookups
per queue item, every iteration.

**Fix:** Carry the depth as an integer in the BFS queue item alongside the
Env. Increment it when a node binding is added. `envDepth(cur.env)` becomes
`cur.depth`.

**Scope:** `qitem` struct and `bfsEnv` in `executor_env.go`. Trivial change.

**Expected impact:** Measurable on queries with many result paths. The saving
is small per item but adds up at scale.

---

### 1.3 Batch hydration

**Problem:** Hydration calls `store.Get(entity, id)` one node at a time.
RETURN projection iterates result Envs and hydrates each node individually.
For a query returning 500 rows each with 2 node variables, that's up to 1000
sequential store calls even if many nodes are shared across rows.

**Fix:** Collect all node IDs that need hydration before the projection loop,
deduplicate, and issue a single `store.GetMany(entity, []int{...})` call per
entity type. Requires adding `GetMany` to the `EntityGetter` interface and
implementing it in the store backend.

**Scope:** `EntityGetter` interface, `executor_env.go` projection path,
store implementation. Medium scope but entirely additive.

**Expected impact:** High when results are large and the store has non-trivial
call overhead (network-backed stores). Neutral for in-process stores.

---

### 1.4 Inline property filter push-down into start node scan **[done]**

**Problem:** `matchesNodePatternAST` checks inline properties (e.g.
`{id: '3'}`) by calling `hydrateNodeData` on each candidate node to fetch
all properties. For `{id: '3'}`, the entity ID is already encoded in the
node ID string — no hydration needed.

**Fix:** The `id` case is already special-cased in `matchesNodePatternAST`
(it parses from the node ID string directly). Extend this to other
topology-derivable properties: `type` is the entity type prefix. Any pattern
that only constrains `id` or `type` can be answered without any store call
at all.

**Scope:** `matchesNodePatternAST` in `executor_ast.go`. The special-case
already exists; just ensure hydration is not triggered for `id`-only patterns.

**Expected impact:** High for queries like `MATCH (u:User {id: '42'})` which
are the single most common graph query pattern. These should cost zero store
calls.

---

## Tier 2 — Cross-query result cache (new infrastructure, medium complexity)

### 2.1 Query result LRU cache

**Problem:** Identical queries re-execute the full BFS/snapshot cycle on every
call. For read-heavy workloads where the graph changes infrequently, this is
pure waste.

**Design:**
- Cache key: `hash(queryString + maxDepth + tenantPrefix)`
- Cache value: `([]map[string]interface{}, snapshotVersion)`
- Invalidation: on any `UpdateFromEntityForTenant` call for an entity type
  that appears in the cached query's MATCH pattern
- Eviction: LRU with configurable entry count and TTL

**Implementation notes:**
- The `Executor` struct gets a `resultCache *lru.Cache` field (or a simple
  `map[string]cacheEntry` with a `sync.RWMutex` for the initial version)
- The snapshot version is a monotonic counter incremented on every write
- A cache entry is valid if its snapshot version matches the current version
  for all entity types it depends on
- Dependency tracking: parse the query's MATCH pattern for entity types;
  store them alongside the cached result

**Scope:** New `cache.go` in `pkg/sulpher`; changes to `Executor` struct and
`executeASTv2` entry point; changes to the write path to increment version
counters.

**Expected impact:** Very high for read-heavy workloads with stable graphs.
Eliminates traversal cost entirely for hot queries.

---

### 2.2 Partial path cache (shortestPath)

**Problem:** `FindPathDirected` runs a full BFS every time even when the same
(from, to) pair is queried repeatedly.

**Design:**
- Cache key: `(fromID, toID, direction, maxDepth)`
- Cache value: `[]string` (path node IDs)
- Invalidation: when any edge on the cached path is added or removed

**Implementation notes:**
- `FlatGraph` maintains the adjacency map; edge additions/removals are
  the invalidation trigger
- Storing which edges a path passes through at cache time requires a small
  extension to the cache entry
- Alternatively, invalidate all cached paths when any edge changes (simpler,
  more conservative, still valuable for stable graphs)

**Scope:** `FlatGraph` in `pkg/graph`; new cache structure.

**Expected impact:** High for repeated shortestPath queries between known node
pairs (e.g. permission checking, reachability testing in operational graphs).

---

### 2.3 Segment result cache (intra-graph, cross-query)

**Problem:** Multiple different queries may traverse through the same hub node
at the same segment index. The per-query BFS memo from Tier 1.1 doesn't help
here because it's per-query.

**Design:**
- A shared `segmentCache map[segCacheKey][]Env` on the `Executor`
- Key: `(nodeID, segIdx, patternHash)` where `patternHash` hashes the
  remaining path pattern
- Invalidation: on any write affecting the cached node type

**Implementation notes:**
- This is more complex than the per-query memo because the Envs contain
  references to node maps that may be mutated by hydration
- Requires cloning Envs on cache retrieval, or making node maps immutable
  after hydration (copy-on-write)
- The patternHash needs to be stable and cheap to compute

**Scope:** Non-trivial. Adds shared mutable state to the executor.

**Expected impact:** High for hub-node graphs with repeated queries. Medium
for typical application workloads. Not worth doing before 2.1.

---

## Tier 3 — Structural (architectural changes, implement when needed)

### 3.1 Property index

**Problem:** `WHERE u.age > 30` with no inline property in the pattern
requires hydrating every node of type `user` to evaluate the condition.

**Fix:** A secondary index on property values, maintained alongside the
adjacency map. On hydration of a node, update the index. On property-equality
and property-range WHERE predicates that appear without a graph traversal
context, consult the index to get candidate node IDs before BFS.

**Scope:** Substantial. New index structure in `FlatGraph`, index maintenance
on write, planner changes in `executeTraversalEnv` to detect index-eligible
start conditions.

**Expected impact:** Transformative for property-filtered start conditions on
large graphs. Unnecessary for small graphs.

---

### 3.2 Bidirectional BFS for shortestPath

**Problem:** `FindPathDirected` with `PathDirAny` does a unidirectional BFS
from source to target. For large graphs the BFS frontier can grow very large
before reaching the target.

**Fix:** Bidirectional BFS — expand from both source and target simultaneously
and stop when the frontiers meet. Reduces the search space from O(b^d) to
O(b^(d/2)) where b is average branching factor and d is path length.

**Scope:** `FlatGraph.FindPathDirected`. Moderate complexity, self-contained.

**Expected impact:** High for long paths in dense graphs. Neutral for short
paths (most application use cases).

---

### 3.3 Snapshot warm-up / incremental snapshot

**Problem:** `takeSnapshot` copies the entire adjacency map on every query.
For a graph with 100K edges this is a meaningful allocation.

**Fix:** Maintain a persistent snapshot that is updated incrementally on each
edge addition/removal rather than rebuilt from scratch. The executor reads
the live snapshot under a read lock rather than copying it.

**Scope:** Significant. Requires lock-free or copy-on-write semantics for the
adjacency structure, or a versioned snapshot model.

**Expected impact:** Eliminates snapshot construction cost for large graphs.
Not worth the complexity until the graph is large enough to feel the snapshot
cost (typically > 500K edges).

---

### 3.4 Parallel start node BFS

**Problem:** When a query has many start nodes (e.g. `MATCH (u:User)` over a
graph with thousands of users), each start node is processed sequentially.

**Fix:** Fan out the per-start-node BFS across goroutines with a shared
`results` channel.

**Scope:** `executeTraversalEnv`. Moderate complexity; requires careful
handling of the stats counter and the visited map (per-goroutine visited maps
are correct since each start node is independent).

**Expected impact:** High for queries over large unlabelled starts. Neutral
for single-start queries (the common case).

---

## Measurement

Before implementing Tier 2 or Tier 3 items, benchmark with realistic graph
sizes. The benchmarks should cover:

- Single-start, single-hop: `MATCH (u:user {id: '1'})-[:R]->(b) RETURN b`
- Single-start, multi-hop: `MATCH (u:user {id: '1'})-[:R*..3]->(b) RETURN b`
- All-start scan: `MATCH (u:user) RETURN u` (tests start node cost)
- shortestPath: `MATCH p = shortestPath((a:user)-[*]-(b:user)) RETURN p.length`
- Hub-node: graph where 10% of nodes have 10x average degree

Run each benchmark at graph sizes of 1K, 10K, 100K, 1M nodes.
