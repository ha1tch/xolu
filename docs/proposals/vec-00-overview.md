# /vec — Part 0: Overview, Invariants, and Scope

Updated: 2026-08-20
Status: Proposal. Supersedes the seven-part draft of 2026-08 in full.

---

## 1. Supersession and delta

This revision replaces the earlier seven-part draft (`vec-01-intro.md` through
`vec-07-semantic-units.md`). The conceptual core of that draft is retained
unchanged: `/vec` is semantic addressing over xolu, implemented as a derived
materialization, returning references rather than copies. What changed:

| # | Change | Reason |
|---|--------|--------|
| 1 | Consolidated seven parts into four; duplicated treatments of generations, projections, and consistency (which appeared in three parts each) now appear once | The draft was a design exploration; this is a specification candidate |
| 2 | The maintenance mechanism is decided, not hedged: V1 is reconciliation-driven with a persistent work table owned by `/vec`; the substrate change feed is an adapter added later, not a dependency | The draft assumed a durable commit-event stream that xolu does not have (open items T-07 through T-11, T-148). A proposal must not build on unbuilt floor |
| 3 | Scheduled reconciliation is specified as a future `/cron` verb (XOT197–XOT202), never a private scheduler | xolu must not grow a fourth ad hoc timer loop |
| 4 | Storage shape is decided: a dedicated per-tenant SQLite database (the vec store), with the full derived-state paperwork specified — tenant-emptiness inventory, tenantexport policy, physical-backup policy, `iolu db check` oracle, dormant guards | The draft left storage "implementation-specific", which in this codebase means a known set of integration points silently missed (the XOT216 bug class) |
| 5 | A reference and addressing model is specified: structured references with explicit fields, named constructors, a defined reversible string form, and a JSON wire shape. No overloaded strings, no fragment syntax parsed by guesswork | The draft used ad hoc forms (`/assets/123`, `/manuals/42#chunk/17`, `similar//assets/1842`) — the exact single-overloaded-string defect class fixed in XOT224/XOT226 |
| 6 | The wire contract is POST-body-first. Natural-language queries and references never travel in URL path segments | Same defect class as above, applied to the transport |
| 7 | The composition story is grounded in wave 8: an `@VEC()` OQL table-valued provider over the T-76 dispatch infrastructure, replacing the draft's abstract "query planner opportunity" sections | xolu already designed the mechanism the draft was reinventing |
| 8 | Authorization claims are reduced to what the substrate provides: per-tenant partition plus existence revalidation at retrieval time. No per-object ACL language | The draft repeatedly promised "inherit xolu ACLs"; xolu has tenant isolation and sysmask, not per-object per-principal authority. A spec must not document guarantees the substrate cannot back |
| 9 | A complete client story is specified: `pkg/client` typed methods in the waves 15–16 pattern, `iolu`/`xia` verbs, and the molu surface | The draft ended at the REST idea |
| 10 | American English throughout code, routes, and identifiers (`materialize`, not `materialise`), per the project's identifier-language law | The draft mixed the API verb spelling into its prose spelling |

Sections of the draft that were correct are carried forward compressed, not
re-argued. Where this document is silent, the draft's reasoning stands as
background rationale but is not normative.

---

## 2. The core proposition

> **`/vec` gives xolu semantic addressing without making semantic
> representations authoritative.**

Operationally: `/vec` turns a semantic description into ranked references to
xolu objects. The vector index is the mechanism. The materialization is the
derived state. The xolu object is the authority. The reference is the result.

xolu already provides several coordinate systems over the same information:
structural addressing (a document by collection and id), predicate addressing
(OQL), graph addressing (edges), temporal addressing (`/ts`, `/cal`,
chronicle), and geospatial addressing (`/loc`). `/vec` adds one more:
addressing by meaning. It is a fifth way of *finding* things xolu already
*has* — not a second place to put them.

The canonical interaction, in client terms (wire contract in Part 3):

```
VecRetrieve("assets", "slightly elongated wooden cylinders used for writing on paper", limit=5)
    →
[ {ref: <assets, A-1842>, score: 0.94, rank: 1}, ... ]
```

The result rows are references. Everything downstream — fetching the object,
joining, filtering, graph traversal, timeseries lookups — is ordinary xolu.

---

## 3. Design principles (normative)

These bind every later decision. They are the draft's principles, consolidated
and renumbered, with three additions (P13–P15) forced by integration reality.

- **P1 — xolu remains authoritative.** A materialization never becomes the
  source of truth for anything it indexes. The dependency is strictly one-way.
- **P2 — Materializations are derived and rebuildable.** Source data plus the
  materialization definition plus the representation contract must suffice to
  reconstruct the index from nothing. No irreplaceable state accumulates in
  the vec store.
- **P3 — Results are references.** Retrieval returns xolu references and
  retrieval metadata, never object copies. There is no vec-side document API.
- **P4 — Composition over duplication.** Filtering, graph traversal,
  aggregation, temporal queries, transactions, tenancy, and document fetch are
  xolu concerns. `/vec` returns control to xolu instead of reimplementing them.
- **P5 — Implementation is hidden.** ANN algorithms, vector dimensions,
  distance metrics, and storage layout never appear in the semantic contract.
  The user sees `score`, not geometry.
- **P6 — Embedding providers are replaceable.** No vendor, model, or inference
  architecture is part of the primitive's identity. Model identity is
  generation metadata.
- **P7 — Representations are disposable.** Changing model, projection, or
  chunking creates a new generation; it never mutates source objects and never
  invalidates authoritative data.
- **P8 — Semantic units, not chunks, are the primitive.** Records, chunks,
  fields, relationships, and blobs are all ways of deriving semantic units.
  V1 implements two kinds (object, chunk); the model admits the rest.
- **P9 — Writes never wait for embeddings.** A source commit is immediately
  authoritative; the materialization converges asynchronously. Embedding
  infrastructure must never sit on the transaction path.
- **P10 — Failure isolation is absolute.** Embedder outage or index corruption
  degrades `/vec` only. Retrieval against an unavailable index returns an
  explicit unavailability error, never a silent empty result.
- **P11 — References are structured.** Every reference is a struct with
  explicit fields, built by named constructors, with one defined, reversible
  string serialization. No API, internal or external, packs identity into a
  string another component must split by guesswork (the XOT226 rule).
- **P12 — Zero results are valid.** Retrieval must be able to say "nothing
  sufficiently relevant" rather than fabricating the K least-wrong neighbors.
- **P13 — Derived state does its paperwork.** The vec store registers in the
  tenant-emptiness inventory, declares its tenantexport and physical-backup
  policy, ships an `iolu db check` oracle, and registers its dormant guards in
  the session they are written. Non-negotiable; this is where derived-state
  bugs live (XOT216).
- **P14 — No private schedulers.** Anything periodic in `/vec` is either
  explicitly invoked or, once `/cron` exists, a registered `/cron` verb.
- **P15 — Honest guarantees only.** The specification claims tenant-level
  isolation and existence revalidation, because that is what the substrate
  provides. Stronger authority claims wait for stronger substrate.

---

## 4. Non-goals

`/vec` is not, and must not drift toward: a general-purpose vector database; an
embedding or LLM runtime; a RAG framework; an agent framework; a document
parser or chunking toolkit as a product surface; a reranking framework; a
second query language; a second authorization or tenancy model; a vector CRUD
API (`insert`/`upsert`/`delete` on vectors would imply the index is
authoritative — it is not: sources change, materializations follow).

RAG is one consumer of semantic addressing, not its definition. `/vec` has no
dependency on LLMs, prompts, or agents, and remains useful without them.

---

## 5. V1 scope

### V1 ships

| Capability | Notes |
|---|---|
| Document-collection sources | One source kind; others (obj subjects, blobs) are model-admitted, deferred |
| Object units and bounded-text chunk units | Chunking policy `none` or `fixed`, per materialization |
| Field-selection projection with labeled canonical serialization | No graph traversal, no computed values, no templating DSL |
| Projection hashing and source-revision tracking | The churn insulator: telemetry-field changes never touch the embedder |
| One embedder interface; HTTP provider (OpenAI-compatible and Ollama shapes); deterministic test provider | Batch-first. No in-process model inference |
| Brute-force index over per-tenant SQLite-persisted vectors | RAM-resident per materialization with a stated ceiling; `Index` interface admits ANN later |
| Reconciliation-driven maintenance with a persistent work table | No change-feed dependency; explicit `materialize` reconciles |
| Generations: fingerprinted, built via snapshot plus catch-up, atomically published, rollback-capable | |
| Retrieve and similar operations; explain introspection | POST-body wire contract |
| Two-stage retrieval: candidates, then per-tenant existence revalidation | |
| Full derived-state paperwork (P13) | Emptiness inventory, export/backup policy, check oracle, dormant guards |
| `pkg/client` typed surface; `xia` admin verbs; molu status | Part 3 |

### V1 does not ship

HNSW or any ANN structure; sharding or replication; vector-side metadata
filtering; BM25/lexical/hybrid retrieval; reranking; multimodal or multiple
representations per unit; graph-traversing projections; relationship,
field-level, or collection units; per-object authorization; cross-
materialization retrieval; raw-vector queries; transaction-aware retrieval;
the `@VEC()` OQL provider (specified here, built as a wave-8 adjunct once
T-76 lands).

---

## 6. Sequencing and dependencies

| Dependency | Relationship |
|---|---|
| T-76 (OQL primitive-query dispatch) | Required before `@VEC()` only. The REST/client surface has no dependency on it; `/vec` V1 can precede or follow wave 8's providers, but `@VEC()` must reuse T-76's dispatch, never a parallel mechanism |
| T-07..T-11, T-148 (events) | **Not dependencies.** V1 is reconciliation-driven. When durable dispatch ships, a change-feed adapter (Part 2 §4) becomes an optimization; reconciliation remains the correctness backstop permanently |
| XOT197–XOT202 (`/cron`) | Not a dependency. Scheduled reconciliation is specified as a `/cron` verb and is simply absent until `/cron` exists; V1 reconciles on explicit invocation |
| XOT203 (physical backup) | The vec store adds one SQLite file to XOT203's enumeration, with a declared may-rebuild-instead restore policy (Part 2 §6) |
| T-73 (`iolu db check` oracle promotion) | The vec oracle (Part 2 §6) joins the same wiring |
| T-137 (tsqlparser chained method-call defect) | Constrains `@VEC()` syntax: table-valued call form only, no chained-access form, until T-137 is fixed (Part 3 §3) |

Recommended build order inside a future wave: store and definitions; projector
and hashing; embedder providers; index, retrieve, and similar; generations and
rebuild/activate; REST, client, oracle, guards, docs. The `@VEC()` provider is
a separate item gated on T-76.

---

## 7. Document map

- **Part 0 (this document)** — invariants, scope, sequencing.
- **Part 1 — Model and addressing.** References and string forms, sources,
  projections, semantic units, generations, consistency contract.
- **Part 2 — Execution and integration.** Internal layers, maintenance,
  storage, derived-state paperwork, authorization reality, failure handling,
  testing discipline.
- **Part 3 — Surfaces.** REST wire contract, `@VEC()` OQL provider,
  `pkg/client`, `iolu`/`xia`, molu, documentation and tracking obligations.
