# xolu Caching

This document describes how xolu's caching layer works, the RESP commands it
requires from an external cache backend, the key namespace it uses, and what
was learned during integration testing against slabbis.

---

## Overview

xolu supports two cache backends, selected at startup via `XOLU_CACHE_TYPE`:

| Backend | Value | Notes |
|---------|-------|-------|
| In-memory (default) | `memory` | Sharded LRU with per-item TTL and background sweeper |
| RESP-compatible | `redis` | Any server speaking RESP2 on TCP |

The in-memory backend is `pkg/cache.ShardedMemoryCache`. It requires no
external process and is appropriate for single-instance deployments.

The Redis backend is `pkg/cache.RedisCache`. It connects to any TCP server
that speaks RESP2 — Redis, slabbis, or another compatible implementation —
and uses JSON encoding for all values.

Both backends implement the same `Cache` interface:

```go
type Cache interface {
    Get(ctx context.Context, key string) (interface{}, error)
    Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
    DeletePattern(ctx context.Context, pattern string) error
    Exists(ctx context.Context, key string) (bool, error)
    Close() error
}
```

---

## RESP commands required

The following table lists every RESP command that `RedisCache` issues, the
cache operation that triggers it, and what the server must implement correctly
for xolu to work.

| RESP command | Issued by | Purpose |
|---|---|---|
| `PING` | `NewRedisCache` (startup) | Connection health check; must return `+PONG` |
| `GET key` | `Cache.Get` | Retrieve a JSON-encoded value by key |
| `SET key value EX seconds` | `Cache.Set` | Store a JSON-encoded value with TTL; `EX` (seconds) is always provided |
| `DEL key [key ...]` | `Cache.Delete`, `Cache.DeletePattern` | Remove one or more keys |
| `SCAN cursor MATCH pattern COUNT count` | `Cache.DeletePattern` | Iterate keys matching a glob pattern; used to enumerate keys for bulk deletion |
| `EXISTS key` | `Cache.Exists` | Check whether a key is present |

No other RESP commands are issued by xolu's production code.

### Notes on individual commands

**`SET` always uses `EX`** — values are never stored without an expiry.
The TTL is either the per-call value or, when the caller passes `0`, the
cache-level default (`XOLU_CACHE_TTL`, default 300 seconds).

For the in-memory backend, TTL is honoured per-item; the `MemoryCache` now
stores `expiresAt` per entry and checks it on `Get`. A background sweeper
goroutine (30-second interval) proactively removes expired entries. This means
different categories of data can have different TTLs within the same cache instance.

**`DEL` is variadic** — when `DeletePattern` matches multiple keys in a
single `SCAN` cursor page (up to 100 at a time), they are batched into a
single `DEL` call. The server must accept multi-key `DEL`.

**`SCAN` is required for `DeletePattern`** — there is no fallback to `KEYS`.
Any RESP-compatible backend used with xolu must implement `SCAN cursor MATCH pattern COUNT count`.
Both Redis and slabbis (since v1.5) satisfy this requirement.

**`PING` is issued once** — at connection time only, not on every request.
A backend that accepts the TCP connection but delays or refuses `PING` will
cause xolu to fall back to the in-memory cache with a warning in the log.

### Encoding

Values are JSON-marshalled before `SET` and JSON-unmarshalled after `GET`.
The stored payload for an entity GET cache entry is a `map[string]interface{}`
(the raw entity document). The stored payload for a list cache entry is a
`models.PagedResponse` struct, which JSON-serialises to an object with `data`
and `pagination` keys.

Keys are plain ASCII strings. No binary or UTF-8 content appears in keys.

---

## Cache key namespace

Keys follow a hierarchical `tenant:entity:operation` convention.
`XXXX` is the four-digit uppercase hex tenant ID (e.g. `0001` for tenant 1).
Tenant 0 (unscoped operation) omits the prefix entirely.

| Key format | Produced by | Cached value |
|---|---|---|
| `entity:id` | `CacheKey(0, entity, id)` | Single entity document (tenant 0) |
| `XXXX:entity:id` | `CacheKey(tid, entity, id)` | Single entity document (tenant N) |
| `entity:list:page:perPage` | `buildListCacheKey` + `ScopeKey(0, …)` | Paginated list response (no filters, tenant 0) |
| `XXXX:entity:list:page:perPage` | `buildListCacheKey` + `ScopeKey(tid, …)` | Paginated list response (no filters, tenant N) |
| `entity:list:page:perPage:k1=v1,k2=v2` | `buildListCacheKey` + `ScopeKey` | Paginated list with filter params, sorted deterministically |

Filter key–value pairs in list cache keys are sorted alphabetically to ensure
the same filter set always maps to the same cache key regardless of query
parameter order.

### Pattern matching

Two patterns are used for bulk invalidation:

| Pattern | Produced by | Matches |
|---|---|---|
| `XXXX:entity:*` | `CachePattern(tid, entity)` | All cache entries for an entity type in a tenant |
| `XXXX:entity:list:*` | `CacheListPattern(tid, entity)` | Only list entries; preserves individual GET entries |

The patterns already end with `*` and are passed to `SCAN` as-is.
No additional `*` is appended; the effective pattern is `XXXX:entity:*`.

---

## What is cached and when

### Read path

`GET /api/v1/{entity}/{id}` checks the cache before hitting the store.
On a miss, the entity document is fetched from storage, cached, and returned.

`GET /api/v1/{entity}` (list) checks the cache before hitting the store.
On a miss, the paginated result (including total counts) is cached and returned.
List cache keys include page number, per-page size, and any filter parameters.

The cache is consulted before reference embedding. The entity document is
cached in its raw (pre-embedding) form and embedded on each cache hit. This
means different `embed_depth` values for the same entity always produce
consistent base data, and the embedding work happens in memory rather than
touching the database.

### Write path

Every write operation (POST/PUT/PATCH/DELETE/commit) invalidates the cache
before returning the response to the caller.

| Write operation | Invalidation method | Keys removed |
|---|---|---|
| `POST /api/v1/{entity}` (create) | `invalidateCacheForID` | Entity GET key + entity list pattern |
| `PUT /api/v1/{entity}/{id}` (replace) | `invalidateCache` | All keys for that entity type |
| `PATCH /api/v1/{entity}/{id}` | `invalidateCacheForID` | Entity GET key + entity list pattern |
| `DELETE /api/v1/{entity}/{id}` | `invalidateCacheForID` | Entity GET key + entity list pattern |
| `/api/v1/commit` (update + append) | `invalidateCacheForID` × N | Each updated/appended entity |
| `/api/v1/{entity}/save/{id}` | `invalidateCacheForID` | Entity GET key + entity list pattern |

`invalidateCacheForID` is the surgical helper: it removes the one GET key
and the list pattern, leaving GET cache entries for all other IDs of that
type intact. `invalidateCache` is the broad helper: it removes all entries
for the entire entity type — used only by `PUT` (replace), which replaces
the complete document and so cannot guarantee other cached documents are
unaffected.

Cache invalidation errors are silently discarded (`_ = s.cache.Delete(…)`).
A failed invalidation means a stale entry may be served until TTL expiry;
this is an acceptable trade-off for a cache that is not the source of truth.

### No cache for writes

Write responses (the entity body returned from POST/PUT) are not cached.
Only read responses (GET, list) are cached. This avoids a class of bugs
where a write response body is served from cache as if it were a read.

---

## Configuration

| Env var | Config field | Default | Notes |
|---|---|---|---|
| `XOLU_CACHE_TYPE` | `CacheType` | `memory` | `"memory"` or `"redis"` |
| `XOLU_CACHE_TTL` | `CacheTTL` | `300` | Seconds; applies to all backends |
| `XOLU_CACHE_SIZE` | `CacheSize` | `1024` | Max entries for in-memory cache |
| `XOLU_CACHE_SHARDS` | `CacheShards` | `16` | Shard count for in-memory cache; must be power of 2 |
| `XOLU_REDIS_HOST` | `RedisHost` | `localhost` | Required when `CacheType=redis` |
| `XOLU_REDIS_PORT` | `RedisPort` | `6379` | |
| `XOLU_REDIS_POOL_SIZE` | `RedisPoolSize` | `50` | go-redis connection pool size |
| `XOLU_REDIS_MIN_IDLE_CONNS` | `RedisMinIdleConns` | `10` | Minimum warm connections |
| `XOLU_GRAPH_QUERY_CACHE_TTL` | `GraphQueryCacheTTL` | `30` | Sulpher whole-query result cache TTL in seconds; `0` disables |
| `XOLU_OQL_QUERY_CACHE_TTL` | `OQLQueryCacheTTL` | `30` | OQL whole-query result cache TTL in seconds; `0` disables |

On startup, if `CacheType=redis` and the `PING` to `RedisHost:RedisPort`
fails, xolu logs a warning and falls back to the in-memory cache automatically.
The server does not exit. This means a Redis/slabbis process that is down at
startup does not block xolu from serving — it simply runs uncached.

---

## Slabbis compatibility

[slabbis](https://github.com/ha1tch/slabbis) is xolu's own RESP-compatible
cache server, designed and tested alongside xolu.

### Commands supported

All RESP commands that xolu issues are implemented by slabbis v1.5 and later:

| Command | Result |
|---|---|
| `PING` | ✓ Returns `PONG` |
| `GET` | ✓ |
| `SET key value EX seconds` | ✓ TTL enforced by slabbis reaper |
| `DEL key [key ...]` | ✓ Variadic delete works |
| `EXISTS key` | ✓ |
| `SCAN cursor MATCH pattern COUNT count` | ✓ Implemented since slabbis v1.5 |

`DeletePattern` works correctly against slabbis. The `cache_slabbis_test.go`
integration tests, including `TestSlabbis_DeletePattern_TenantList` and
`TestSlabbis_WriteThenInvalidate`, verify this end-to-end.

### TTL reaper

The TTL reaper in slabbis is configurable. For test use, setting it to 50ms
ensures fast expiry verification.

### Memory footprint

slabbis uses slabber, a slab allocator with pre-allocated fixed-size buckets.
The default configuration allocates 256MB per bucket × 5 size classes =
1.28GB at startup, which exceeds sandbox limits.

For test use, initialise slabbis via the Go API with small size classes:

```go
slabbis.New(slabbis.Config{
    Shards:          1,
    Classes:         []slabber.SizeClass{
        {MaxSize: 64,   SlotSize: 64},
        {MaxSize: 4096, SlotSize: 4096},
    },
    BucketsPerShard: 1,
    ReaperInterval:  50 * time.Millisecond,
})
```

This allocates approximately 260MB total, which is safe in the sandbox.
The CLI (`slabbis -shards 1`) does not expose custom size classes; use the
Go API for memory-constrained environments.


---


---

## Testing

| Test file | Backend | Scope |
|---|---|---|
| `pkg/cache/cache_test.go` | In-memory | `ShardedMemoryCache` contract |
| `pkg/cache/redis_miniredis_test.go` | miniredis (in-process) | `RedisCache` unit tests |
| `pkg/cache/cache_redis_test.go` | Real Redis (build tag `redis`) | `RedisCache` integration |
| `pkg/cache/cache_slabbis_test.go` | slabbis (in-process) | `RedisCache` vs slabbis — full operation coverage including `DeletePattern` and cache invalidation |
| `pkg/server/adversarial_server_test.go` | In-memory (via `commitEnv`) | Cache invalidation correctness through HTTP layer |

The slabbis tests run in-process using the Go API and do not require an
external process. They are included in the standard `go test ./pkg/cache/...`
run without any build tags.
