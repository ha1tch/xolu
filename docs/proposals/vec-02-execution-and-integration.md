# /vec — Part 2: Execution and Integration

Updated: 2026-08-20
Status: Proposal. Supersedes the seven-part draft of 2026-08 (see Part 0 §1).

---

## 1. Architectural goal

Implement Part 1's model inside xolu such that: `/assets`-class sources remain
authoritative; vectorization is asynchronous; retrieval is fast; a failed
embedding never fails a source transaction; a corrupt index is rebuildable
from xolu; the index implementation is replaceable without touching any
surface; and the whole thing starts small enough to ship as one wave.

```
            AUTHORITATIVE XOLU (documents, graph, /ts, /loc, /obj, /blob)
                     │
              committed state          ← the only input /vec ever reads
                     │
             ┌───────▼────────┐
             │  reconciler /  │  work table (durable), batching,
             │  worker        │  revision guards, retry
             └───────┬────────┘
               projection → hash ── unchanged? → record revision, done
                     │ changed
                 embedder (batch)
                     │
                vector index  ── generations, atomic publication
                     │
                 retrieve / similar
                     │
              two-stage validation
                     │
               ranked structured refs → ordinary xolu
```

## 2. The four internal boundaries

```go
type Projector interface {
    Project(ctx context.Context, src SourceRef) (Projection, error)
}
type Projection struct {
    Source SourceRef
    Units  []ProjectedUnit // one for object policy; N for chunk policy
}
type ProjectedUnit struct {
    Fragment Fragment
    Text     string
    Hash     Hash // canonical-text hash, Part 1 §3.2
}

type Embedder interface {
    // Batch-first by contract. Implementations must not degrade to N
    // single-item calls internally when the provider supports batching.
    Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

type Index interface {
    Upsert(ctx context.Context, entries []Entry) error
    Delete(ctx context.Context, units []UnitKey) error
    Search(ctx context.Context, vector []float32, limit int) ([]Match, error)
}

type WorkQueue interface {
    Enqueue(ctx context.Context, item WorkItem) error // coalescing on (materialization, source)
    Next(ctx context.Context, batch int) ([]WorkItem, error)
    Ack(ctx context.Context, items []WorkItem) error
    Fail(ctx context.Context, item WorkItem, cause error) error
}
```

Package boundary: `pkg/vec/` with `definition.go`, `ref.go`, `projector.go`,
`embedder.go`, `index.go`, `store.go`, `queue.go`, `reconcile.go`,
`generation.go`, `retrieve.go`. The name is `vec`, not anything suggesting a
vector database; the naming is part of the architecture.

Enumeration of populations reuses the existing query machinery (the
`SourceDecl.Where` predicate is evaluated by the same engine OQL uses). `/vec`
owns no scanner, no predicate evaluator, and no second query subsystem.

---

## 3. Storage: the vec store (decided)

Each tenant gets one dedicated SQLite database, the **vec store**, following
the established per-tenant satellite pattern. Not Pebble, for three reasons:
unit+vector+revision+hash updates need multi-row transactional atomicity,
which SQLite gives directly; XOT203's physical-backup path already handles
SQLite files; and the brute-force V1 gains nothing from an LSM shape.
The `Index` interface (§2) is where a Pebble- or ANN-backed implementation
plugs in later if measurement demands it — a storage migration behind an
interface, not a redesign.

Logical schema (columns, not packed strings, per P11):

| Table | Contents |
|---|---|
| `vec_def` | name, description, source kind/collection/where, projection select (JSON array), unit policy, created/updated |
| `vec_generation` | materialization, generation number, full fingerprint components (provider, model, model version, dims, metric, normalization, definition fingerprint), status, counts (units, indexed, pending, failed), source revision watermark, timestamps |
| `vec_unit` | materialization, generation, source kind, source collection, source id, fragment kind, fragment ordinal, source revision, projection hash, state, failure reason |
| `vec_vector` | internal rowid → unit key, dims, vector BLOB (little-endian float32) |
| `vec_work` | materialization, source ref columns, enqueued revision, attempts, next-attempt time; unique on (materialization, source) — the coalescing constraint |

Internal vector rowids never escape the store (Part 1 §2.5).

**Memory model.** Retrieval loads a generation's vectors into RAM on first use
and serves brute-force search from memory; SQLite is the durable copy.
Capacity arithmetic is stated, not hidden: 100,000 units at 768 dims × 4 bytes
is ~300 MB per resident generation. A deployment-configured resident ceiling
(bytes) applies per tenant across materializations, with least-recently-
queried eviction back to cold (reload on demand). Exceeding the ceiling at
load time is an explicit retrieval error naming the ceiling — never a silent
partial search. Multi-tenant deployments size this ceiling deliberately; the
status surface reports resident bytes so the operator can see it.

Brute-force cost at this scale is ~77M multiply-adds per query — single-digit
milliseconds — which is precisely why V1 refuses ANN complexity (P5, and the
draft's own correct argument).

---

## 4. Maintenance: reconciliation-driven, by decision

The draft assumed subscription to a durable commit-event stream. xolu does not
have one: durable dispatch, deferred event wiring, and ordering are open
(T-07..T-11), and the one dispatch path that exists misfires on v1-only
deployments (T-148). This proposal therefore **decides** rather than hedges:

> **V1 `/vec` is reconciliation-driven. Its only input is committed source
> state read at reconcile time. There is no change-feed dependency.**

Mechanics:

1. `materialize(name)` runs a reconciliation pass against the current
   generation: enumerate the population (via the query machinery), diff
   against `vec_unit` (present-not-in-population → remove; in-population with
   moved revision → enqueue; missing → enqueue), then drain `vec_work` through
   the worker pipeline (project → hash-compare → embed if changed → upsert,
   under revision guards).
2. `rebuild(name)` creates a new generation and runs the same pass against an
   empty unit set, from a snapshot watermark, with catch-up (Part 1 §4).
3. The work table is durable, so an interrupted pass resumes; processing is
   idempotent-with-durable-progress — exactly-once is explicitly not required
   and not pretended.
4. Failures: transient embedder errors retry with exponential backoff and an
   attempts cap; permanent failures (unprojectable source, oversize input)
   mark the unit `failed` with an inspectable reason and do not block the
   pass. Partial coverage is a reported number, not an availability cliff.

**Scheduling (P14).** V1 has no timer of its own. Reconciliation runs when
invoked — by an operator, a client, or a deployment's external scheduler
calling the endpoint. When `/cron` (XOT197–XOT202) ships, `/vec` registers a
`vec_reconcile` verb (cancellable, progress-reporting — both capabilities the
/cron design discovers by interface assertion), and scheduled freshness
becomes a /cron recurrence like any other system job. This proposal
deliberately specifies the verb now so /cron's verb registry has a named
consumer waiting, and deliberately ships nothing periodic before then.

**The change-feed adapter (later, optional).** When durable dispatch (T-07)
exists, a thin consumer maps commit events for a materialization's collection
to `WorkQueue.Enqueue` calls — reducing convergence lag from
"since last reconcile" to "queue depth". It is an optimization layer only:
events are maintenance, reconciliation is the correctness backstop, permanently
(missed events, bugs, imports predating `/vec`, manual repairs). The adapter's
event shape is `(source ref, revision, operation)` — never the object payload,
which the worker reads authoritatively at processing time.

---

## 5. Retrieval path

```
query text
  → serving generation's representation contract
  → Embedder.Embed([query])
  → Index.Search(vector, limit × oversample)
  → candidate unit keys
  → revalidate: source exists in caller's tenant (batch lookup);
                cheap population predicates where available
  → rank, apply threshold, group_by_source if requested, truncate to limit
  → rows of (SourceRef, Fragment, score, rank, xref)
```

Oversampling covers revalidation losses; the factor is internal and adaptive
(if validation discards too many, search widens once before returning fewer
than `limit`, which is a legitimate outcome). `similar` short-circuits the
embed step by reading the stored vector for the given source's object unit.

Unavailability (store unreadable, generation resident-load failure, no
published generation) is an explicit typed error; it is never rendered as an
empty result set (P10).

---

## 6. Derived-state paperwork (P13, normative)

These are requirements on the implementing wave, not suggestions. Each is the
generalization of a defect this codebase has already fixed once.

| Obligation | Specification |
|---|---|
| **Tenant emptiness** | The vec store registers in the combined store inventory that backs the single-round-trip tenant-summary endpoint (XOT213), so `Empty()` accounts for it. Policy: definitions make a tenant non-empty (they are user-created configuration, exactly as exportable as any other user data); generations, units, and vectors never do (they are derived and rebuildable). The inventory must know the store exists so this distinction is applied in the summary, not absent from it. The XOT216 lesson: a store the summary does not know about is a wrong answer waiting |
| **tenantexport (T-71)** | Definitions export (they are configuration: small, meaningful, needed to rebuild). Generations, units, and vectors are excluded as rebuildable derived state, and the exclusion is stated in the export manifest, not implied by absence |
| **Physical backup (XOT203)** | The vec store's SQLite file joins XOT203's enumerated targets with a declared policy: back up if present, and restore may legitimately substitute "recreate definitions + rebuild" — documented as the supported alternative, since rebuild cost is dominated by embedding spend and a deployment may prefer paying it over storing vectors |
| **`iolu db check` oracle (T-73 adjacency)** | A vec oracle ships with the wave: for each ready generation, re-project a sample (or, in `--full` mode, all) of units from current source state and compare projection hashes and population membership against the store. Embedder-free by design — it verifies the derived state's bookkeeping without network or model access, so it can run offline like the cal and bal oracles. Full re-embed equivalence is *not* an offline check and lives in the dormant guards instead |
| **Dormant guards (§8 of the tracking practices)** | Registered in the same session they are written: (a) rebuild-equivalence — a from-scratch rebuild with the deterministic test embedder produces a unit/hash set identical to the incrementally maintained store; (b) concurrency — reconciler vs. retrieval vs. generation-publication races under `-race` on real multi-core hardware (single-CPU container passes are not evidence, per standing testing discipline); (c) resident-ceiling behavior under concurrent load |
| **Version/journal discipline** | The vec store carries its own schema version row and participates in whatever migration discipline the other satellites follow (verify the current mechanism at implementation time rather than inventing a parallel one) |

---

## 7. Embedding providers

V1 ships exactly three `Embedder` implementations:

- **`HTTPEmbedder`** — OpenAI-compatible `/v1/embeddings` request shape, which
  also covers Ollama's compatibility endpoint; configured per deployment with
  base URL, model name, and auth. Batch requests whenever the provider
  supports them (§2's contract).
- **`OllamaEmbedder`** — the native Ollama shape, if the compatibility
  endpoint proves insufficient at implementation time; otherwise dropped in
  favor of one HTTP implementation. Decide by testing, not by speculation.
- **`TestEmbedder`** — deterministic, offline, dependency-free: a seeded hash
  of the input expanded to the configured dimensionality, normalized. Its
  purpose is that the *entire* `/vec` test suite — units, chunking, hashing,
  generations, reconciliation, retrieval ranking, oracles, the rebuild-
  equivalence guard — runs with zero network and full determinism. Semantic
  *quality* is a model property outside the test suite's claims; the suite
  verifies the machinery.

Explicitly out of scope: in-process model inference, GPU management, model
downloading. xolu defines the provider boundary; deployments choose what
stands behind it (P6, and the draft's correct "don't make xolu own an LLM
runtime" boundary). Provider identity, model, version, and dimensionality are
recorded per generation and are immutable within one (Part 1 §4).

Determinism caveat, stated plainly: remote providers do not guarantee
bit-identical embeddings across their own infrastructure changes. The
generation fingerprint scopes the damage — a provider-side model change that
alters vectors is operationally a reason to `rebuild` — and the projection
hash ensures xolu-side reproducibility of everything up to the embedding call.

---

## 8. Authorization and tenancy: what is actually guaranteed

What the substrate provides today: per-tenant isolation and the sysmask
mechanism. What it does not provide: per-object, per-principal authority
within a tenant. The draft's recurring "semantic retrieval inherits xolu
ACLs" language is therefore replaced with the guarantee V1 can keep:

1. **Partition by construction.** The vec store, its work table, its resident
   vectors, and every retrieval are tenant-scoped. No cross-tenant index
   exists, so cross-tenant leakage is structurally impossible rather than
   filtered after the fact.
2. **Existence revalidation.** Every candidate is checked against current
   source state in the caller's tenant before it is returned (§5). A deleted
   or population-departed object can linger as a vector; it cannot appear in
   a result.
3. **Projection runs in the tenant's context** and reads only the declared
   collection's declared fields. With traversal excluded from V1 projections,
   the class of "embedding leaked adjacent data" defects is excluded with it.

If per-object authority ever exists in xolu, retrieval's revalidation stage is
the single insertion point, and this section is where the specification grows.
Until then, no surface, error message, or document claims finer-grained
authority than the three points above — including the `explain` operation,
which reveals projected text only for sources the caller's tenant can read
anyway.

---

## 9. Failure, backpressure, and limits

- **Isolation (P10):** embedder down → sources unaffected, freshness lags,
  status says so. Index corrupt → sources unaffected; `rebuild` is the repair;
  the check oracle is the detector.
- **Backpressure:** write bursts grow the work table; that affects semantic
  freshness only, never source write throughput. Worker limits (max workers,
  batch size, queue drain rate, max concurrent embedder calls) are deployment
  runtime settings, invisible to `/vec` semantics.
- **Poisoned work:** an attempts cap converts eternal retry into `failed` with
  a reason; failed units are listed by status and re-attempted only by an
  explicit reconcile after the cause is fixed.
- **Limits are explicit:** maximum projection text size (oversize →
  unit `failed`, reason recorded), maximum chunk count per source, resident
  memory ceiling (§3), retrieval limit maximum. Every limit that fires
  produces a typed, named error.

---

## 10. Testing discipline

Consistent with how the other primitives were hardened:

- The full suite runs on `TestEmbedder` — deterministic, offline, `-race`able
  in-container for logic races (with the standing caveat that scheduling races
  need the multi-core dormant guard, §6).
- **Oracle first:** the rebuild-equivalence property (incremental maintenance
  converges to the same unit/hash set as a from-scratch build) is implemented
  early and used adversarially throughout development, in the pattern of the
  journal/rebuild oracles elsewhere in the tree.
- Adversarial cases to write before they are "needed": source id containing
  `:`, `#`, `%`, `/`, and whitespace round-tripping through xref and the
  store; empty projection (all fields absent) — a unit with no semantic text
  is `failed`, not embedded as ""; chunk-boundary shifts; population predicate
  flapping; delete-during-retrieve (the two-stage validation's reason to
  exist); concurrent reconcile invocations (second is refused or queued,
  decided at implementation, but never interleaved); generation publication
  concurrent with retrieval.
- Live verification of the eventual UI/client surfaces follows the standing
  two-locale rule where user-facing strings exist.
