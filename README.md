# xolu

**An operational data engine: documents, graph, time-series, state machines, and reactive events behind one HTTP API.**

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.25+-00ADD8.svg)](https://golang.org/)

<!-- RELEASE_BADGE -->
> **v0.27.2** — 5484 tests passing. See [MANUAL.md](MANUAL.md) for complete documentation.
<!-- /RELEASE_BADGE -->

---

## Naming: xolu, xolu, nolu

The running server and the internal system are called **xolu**. The binary is `xolu`; configuration is `XOLU_*`; the data lives in an xolu data root.

The **project** — this repository on GitHub — is called **xolu**. The distinction exists because xolu is now one member of a small family:

- **xolu** — a single local instance: the engine in this repository.
- **nolu** — the federation layer that manages *N* xolu instances (networked olus): a registry and entity-transfer protocol across instances.
- **xolu** — *one* xolu. This project implements the single, local xolu that nolu networks together. The `x` marks "exactly one instance," the unit that nolu composes.

So: you run `xolu`, you clone `xolu`, and `nolu` (a separate project) is what turns many olus into a fleet.

## What xolu is

xolu is an HTTP server that stores documents and, from the same data, gives you a graph of their relationships, a SQL-like query language, full-text search, append-optimised time-series, executable state machines, and reactive events — without bolting five systems together.

You `POST` a JSON document to `/api/v1/users`. If a field is a `REF` to another entity, xolu records a graph edge automatically. You can then query those documents with OQL (SQL-like), traverse the relationships with a graph query language (Sulpher), search them with FTS5, attach a state machine that enforces legal transitions at write time, and fire a webhook when a transition commits — all against the one store, in one transaction where it matters.

It is a single Go binary over SQLite (with a Pebble-backed time-series plane), designed to run at the edge or on one box, not a distributed cluster. Federating multiple instances is nolu's job, not xolu's.

## Why this is not just an RDBMS

A relational database stores rows and enforces types and constraints. xolu does that too — but it treats several things as *first-class persistence-layer primitives* that an RDBMS leaves to application code:

**Relationships are data, not joins you remember to write.** A `REF` field creates a real graph edge. You get relational queries (OQL) *and* graph traversal (Sulpher) over the same entities, without maintaining a separate graph database or hand-writing recursive joins.

**State machines live in the persistence layer.** Every modelling tool offers a dropdown / enum / `CHECK` constraint for a field with a fixed set of values — and then the *transitions* between those values, the guards that make a transition legal, and the effects of a transition all scatter into application code, invisible to the data layer and bypassable by any direct write. xolu's FSM subsystem moves the machine into the store: a definition is an executable specification, transition legality is enforced at write time, and an illegal state change is rejected at the same layer that rejects an invalid field type. The rule lives in one place, as a structural guarantee rather than a convention.

**Reactions are declared, not wired by hand.** An event definition says "when this FSM transition commits, deliver this payload to this webhook." The engine stamps provenance, renders the payload from the committed data, and delivers — the reaction is part of the data model, not a trigger you maintain elsewhere.

**Time-series is a native plane.** Sensor and event streams go to an append-optimised Pebble store, not into row tables that were never meant for them.

The point is composition: a single `/api/v1/commit` can write a document, advance its state machine, and fire an event atomically — succeed together or not at all.

## Quick start

```bash
git clone https://github.com/ha1tch/xolu.git && cd xolu
make build-xolu && ./xolu
```

Create an entity:
```bash
curl -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Alice", "email": "alice@example.com"}'
```

Create one that references it (this also creates a graph edge):
```bash
curl -X POST http://localhost:8080/api/v1/posts \
  -H "Content-Type: application/json" \
  -d '{"title": "Hello", "author": {"type": "REF", "entity": "users", "id": 1}}'
```

Query with OQL:
```bash
curl -X POST http://localhost:8080/api/v1/oql/query \
  -d '{"query": "SELECT * FROM users WHERE age > 25 ORDER BY name LIMIT 10"}'
```

Inspect the on-disk layout at any time:
```bash
./xolu layout-recon
```

## The two API surfaces

xolu has a stable **`/api/v1`** surface and an experimental **`/api/v2`** surface. Everything added after the 1.0 stability commitment lives under `/api/v2`, so the v1 contract stays stable while the platform extensions evolve.

### `/api/v1` — the stable core

| Endpoint | Description |
|----------|-------------|
| `POST /api/v1/{entity}` | Create entity |
| `GET /api/v1/{entity}/{id}` | Get entity (with embedded refs) |
| `GET /api/v1/{entity}` | List entities (paginated) |
| `PUT /api/v1/{entity}/{id}` | Full update |
| `PATCH /api/v1/{entity}/{id}` | Partial update |
| `DELETE /api/v1/{entity}/{id}` | Delete entity |
| `POST /api/v1/commit` | Atomic multi-entity write (+ optional FSM walk) |
| `POST /api/v1/oql/query` | Run OQL query (sync; `…/async` for async) |
| `GET /api/v1/search?q=term` | Full-text search (FTS5) |
| `POST /api/v1/graph/shortestPath` | Graph traversal |
| `POST /api/v1/blob` | Content-addressed blob storage |
| `GET /metrics` | Prometheus metrics |

All entity and query endpoints have tenant-scoped variants at `/api/v1/tenant/{tenant_id}/...`.

### `/api/v2` — first-class platform subsystems

| Endpoint | Subsystem | What it is |
|----------|-----------|------------|
| `/api/v2/fsm/def` | FSM definitions | Immutable executable state-machine specifications |
| `/api/v2/fsm/machine` | FSM machines | Running machine instances |
| `/api/v2/event/def` | Event definitions | Reactive defs wiring subsystems to webhooks/actions |
| `/api/v2/meta` | Entity metadata | Per-entity key/value sidecar with entity-scoped lifecycle (TTL) |
| `/api/v2/gen` | Generators | Named/stateless value generators — UUID, sequence, token, timestamp, pick |
| `/api/v2/seq` | Sequences | Convenience alias for `/api/v2/gen/seq` |

See [API_V2.md](docs/API_V2.md) for the endpoint reference, [FSM.md](docs/FSM.md) for the state-machine model, and [EVENT_MODEL.md](docs/EVENT_MODEL.md) for the event/delivery model.

## Subsystems at a glance

| Subsystem | Package | Role |
|-----------|---------|------|
| **Document store** | `pkg/storage` | SQLite, WAL mode, read/write connection-pool split, adaptive concurrency, FTS5 |
| **Graph** | `pkg/graph` | In-database per-tenant edge tables; `REF`s become edges; tenant-isolated traversal |
| **OQL** | `pkg/oql` | SQL-like query language with an adaptive push-down planner |
| **Sulpher** | `pkg/sulpher` | Cypher-style graph query engine over the entity graph |
| **Time-series** | `pkg/timeseries` | Pebble-backed, append-optimised, per-tenant |
| **FSM** | `pkg/fsm` | Definitions + running machines; transitions enforced at write time |
| **Events** | `pkg/server` + `pkg/jsonplate` | Reactive event defs; `{origin, message}` delivery envelope; templated payloads |
| **Blob store** | `pkg/blob` | Content-addressed object storage with GC and an S3-compatible surface |
| **Generators / meta / sequences** | `pkg/server` + `pkg/oql` | Stateless and named value generators; per-entity metadata with TTL |
| **Dynamic config** | `pkg/dynconfig` | Runtime key/value overrides (e.g. per-tenant blob quotas) |
| **Tenancy** | `pkg/tenant` | Tenant registry; path and strict isolation modes |

## Multi-tenancy

**Single-tenant** (`XOLU_TENANT_MODE=path`): all features on. Non-tenant routes use the default store (tenant 0); tenant-prefixed routes give optional scoping.

**Multi-tenant strict** (`XOLU_TENANT_MODE=strict`): tenant context required for all data operations; graph, OQL, search, and export are reached only through tenant-prefixed routes. Graph traversal is fully tenant-isolated — node IDs are prefixed and stripped transparently and cross-tenant traversal is blocked at the snapshot layer. Auto-registration is governed by `XOLU_TENANT_AUTO_REGISTER`.

See the [Multi-Tenancy section of the manual](MANUAL.md#multi-tenancy) for the full security model.

## Storage layout

xolu's on-disk layout is derived from a single configurable knob — the data root (`--base-dir`, default `./data`). Everything beneath it is fixed by invariant; there is no separate database-path setting.

```
<data-root>/
  t0000/store/xolu.db   t0000/ts/      tenant 0 (per-file mode)
  tNNNN/store/xolu.db   tNNNN/ts/      registered tenants (per-file mode)
  shared/store/xolu.db  shared/ts/     shared-tenancy mode
  schema/                             entity schemas
  blobs/                              blob store
  dynconfig.json                      runtime config
```

`xolu layout-recon` walks the data root, prints the structure annotated against this invariant, and reports any non-conformance. On startup xolu refuses to run against a data root written by a pre-normalization layout rather than silently creating fresh stores beside the old data. The path-derivation authority is `pkg/storelayout`.

## Configuration

Settings are environment variables (`xolu env` lists them all; `xolu help` lists command-line flags). The essentials:

```bash
# Storage
XOLU_STORAGE_TYPE=sqlite          # only supported backend
XOLU_BASE_DIR=data                # data root; all storage derives from this
XOLU_FULLTEXT_ENABLED=true        # enable FTS5

# Multi-tenancy
XOLU_TENANT_MODE=strict           # path or strict
XOLU_TENANT_AUTO_REGISTER=false   # explicit tenant creation only

# SQLite tuning (0 = backend default)
XOLU_SQLITE_MAX_OPEN_CONNS=0      # writer pool (default 1 for WAL)
XOLU_SQLITE_READ_POOL_SIZE=0      # reader pool (default NumCPU)

# Caches (0 = disabled)
XOLU_GRAPH_QUERY_CACHE_TTL=30     # Sulpher result cache TTL
XOLU_OQL_QUERY_CACHE_TTL=30       # OQL result cache TTL

# Auth
XOLU_AUTH_TYPE=jwt                # none, jwt, apikey, or bearertoken
```

See [MANUAL.md](MANUAL.md) for the complete list.

## Development workflow

Build, test, version, and release each have a canonical tool. Prefer these over invoking `go` or the Python scripts directly — they encode conventions (flags, scopes, output paths) that are easy to get wrong by hand. **[docs/TOOLING.md](docs/TOOLING.md)** is the full operating manual for the `scripts/` toolset (checkpoint intake, the live register, dormant guards, mid-session regression, cutting a release) — written to assume zero session context; read it before touching the tree.

### Building

```bash
make build-xolu          # server binary → ./xolu
make build-iolu         # admin CLI binary → ./iolu
make build              # xolu + iolu
make clean              # remove compiled binaries
```

### Testing

`run_tests.sh` is the standard runner; it always produces a coverage profile and a per-package summary.

```bash
./run_tests.sh                   # standard run (-short; skips stress tests)
./run_tests.sh --full            # full suite incl. stress and comparative tests
./run_tests.sh --race            # full suite with the race detector
./run_tests.sh --threshold 75    # fail if aggregate coverage drops below 75%
./run_tests.sh --html            # generate coverage.html
./run_tests.sh --quiet           # summary only — no per-package table
./run_tests.sh --charts          # coverage heat map + test-count treemap (needs python3)
```

The `--charts` flag renders a terminal coverage heat map and a test-count treemap (256-colour terminal required; it exits silently otherwise). `scripts/charts.py` can also export a two-panel figure to SVG, PNG, or PDF via matplotlib — see its `--help`.

Scoped and CI targets are also exposed via the Makefile (`make test`, `make test-full`, `make test-race`, `make coverage`, `make coverage-check`, `make test-storage`, `make test-graph`, `make test-oql`, `make test-sulpher`, `make test-server`, `make stress`, `make pre-commit`, `make ci`).

**Release validation** requires the full suite — `./run_tests.sh --full` or `make test-full` — not `--short`.

### Version management

`syncver.sh` is the single source of truth; it keeps `VERSION` and `pkg/version/version.go` in sync atomically. Never edit either file directly.

```bash
./syncver.sh show                  # print current version
./syncver.sh check                 # verify the two files match
./syncver.sh set 0.10.1            # set an explicit version
./syncver.sh bump-patch            # 0.10.1 → 0.10.2
./syncver.sh bump-minor            # 0.10.1 → 0.11.0
./syncver.sh bump-major            # 0.10.1 → 1.0.0
```

### Release

`release.sh` is the only correct way to cut a checkpoint: it runs the full suite, regenerates `TESTING.md`, updates version badges, verifies version consistency, removes stale artifacts, and produces the zip. Do not assemble a zip by hand.

```bash
make release VERSION=0.10.2          # full release: test → generate → zip
make release-dry VERSION=0.10.2      # everything except the zip
```

### Benchmarks

```bash
make bench            # all benchmarks, single iteration
make bench-long       # all benchmarks, 5 s each
make bench-oql        # OQL only   (also: bench-storage, bench-sulpher, bench-server)
```

## Project structure

```
xolu/
├── cmd/xolu/              # server entry point (the xolu binary)
├── cmd/iolu/             # offline admin CLI
├── pkg/
│   ├── blob/             # content-addressed blob store with GC
│   ├── cache/            # sharded in-memory & Redis cache
│   ├── config/           # configuration with validation
│   ├── dynconfig/        # runtime key/value configuration
│   ├── errors/           # structured error codes
│   ├── fsm/              # finite state machines (definitions, machines, eval)
│   ├── gc/               # generic background sweep worker (blob/TS/FSM/event GC)
│   ├── graph/            # in-memory graph rebuilt from in-database edge tables
│   ├── jsonic/           # JSON field extractor / column store (OQL internal)
│   ├── jsonplate/        # JSON payload templating ({"$ref": "path"})
│   ├── middleware/       # auth, rate limiting, metrics
│   ├── models/           # shared entity and reference types
│   ├── oql/              # OQL engine, planner, SQL generator
│   ├── qs/               # query scalar functions (OQL internal)
│   ├── server/           # HTTP handlers, routing, event dispatch
│   ├── storage/          # SQLite (read/write split, WAL)
│   ├── storelayout/      # on-disk layout invariant + conformance check
│   ├── sulpher/          # Sulpher graph query engine
│   ├── tdigest/          # streaming quantile estimator
│   ├── tenant/           # tenant registry
│   ├── timeseries/       # Pebble-backed timeseries
│   ├── validation/       # JSON Schema validation
│   └── version/          # build version constant
└── docs/                 # architecture and API documentation
```

## Documentation

Core references:

- **[Manual](MANUAL.md)** — configuration and operational reference
- **[Development Tooling](docs/TOOLING.md)** — the `scripts/` toolset: checkpoint intake, the live register, dormant guards, mid-session regression, releases
- **[API Reference](docs/API_REFERENCE.md)** — complete `/api/v1` REST reference
- **[API v2](docs/API_V2.md)** — FSM, events, meta, generators, sequences
- **[Error Codes](docs/ERROR_CODES.md)** — complete error-code reference
- **[Runbook](docs/RUNBOOK.md)** — operational procedures
- **[Upgrade Guide](docs/UPGRADE.md)** — upgrade and migration steps

Subsystems:

- **[FSM](docs/FSM.md)** — state-machine model, determinism, guards
- **[Event Model](docs/EVENT_MODEL.md)** — events, delivery envelope, latches
- **[jsonplate](docs/jsonplate.md)** — JSON payload templating
- **[OQL API](docs/OQL_API.md)** · **[Query Planner](docs/QUERY_PLANNER.md)** · **[OQL Join Push-down](docs/OQL_JOIN_PUSHDOWN.md)** (design) — relational query language and planner
- **[Sulpher Query Reference](docs/SULPHER_QUERY_REFERENCE.md)** · **[Graph API](docs/GRAPH_API.md)** — graph query and traversal
- **[Timeseries Design](docs/TIMESERIES_DESIGN_V3.md)** — Pebble-backed time-series
- **[Blob API](docs/BLOB_API.md)** — binary object storage and S3 surface
- **[Commit Endpoint](docs/COMMIT_ENDPOINT.md)** — atomic multi-entity writes
- **[JSON Schema](docs/JSON_SCHEMA.md)** · **[Schema Modes (design)](docs/SCHEMA_MODES_DESIGN.md)** — validation and planned schema modes
- **[Dynamic Config](docs/DYNCONFIG.md)** · **[Caching](docs/CACHING.md)** · **[Async Queries](docs/ASYNC_QUERIES.md)** — runtime config, caches, async execution
- **[Export API](docs/EXPORT_API.md)** — data export

Fleet / federation:

- **[Fleet Architecture](docs/FLEET_ARCHITECTURE.md)** — multi-instance deployment design
- **[nolu Events](docs/NOLU_EVENTS.md)** — federation event namespace

Operations & admin:

- **[iolu — interactive xolu](docs/IOLU.md)** — offline tenant/database administration
- **[Testing Strategy](docs/TESTING_STRATEGY.md)** — test taxonomy and coverage approach
- **[Known Issues](docs/KNOWN_ISSUES.md)** — open issues and deferred work

## License

Copyright (c) 2026 haitch

Apache 2.0 — see [LICENSE](LICENSE).

---

**[Full documentation →](MANUAL.md)**
