# /vec — Part 1: Model and Addressing

Updated: 2026-08-20
Status: Proposal. Supersedes the seven-part draft of 2026-08 (see Part 0 §1).

---

## 1. Terminology

Seven terms, used precisely throughout:

| Term | Meaning |
|---|---|
| **Source** | Authoritative xolu data a materialization derives from. V1: a document collection, optionally filtered |
| **Population** | The subset of the source the materialization represents: `select(source, where)` |
| **Projection** | The function extracting semantic content from a population member; deterministic for a fixed source revision and projection definition |
| **Semantic unit** | The independently retrievable piece of meaning: what one vector represents. One source object yields one or more units |
| **Materialization** | A named, maintained semantic view: population + projection + unit policy + representation, plus its derived index state |
| **Generation** | One complete build of a materialization under one fingerprint (definition + projection + representation). Model or projection changes create a new generation |
| **Retrieval** | A semantic lookup against a materialization's current generation, returning ranked references |

---

## 2. The addressing model

This section is new relative to the draft and is normative for every surface
in Parts 2 and 3. It exists because the draft's ad hoc reference spellings
(`/assets/123`, `/manuals/42#chunk/17`, `similar//assets/1842`) are instances
of the single-overloaded-string defect class this codebase has already paid
for twice: `fenceLookupPath` collapsing the server's own two-segment
`{kind}/{key}` shape into one guessed-apart string (XOT224, fixed by
`LocFenceRef` in XOT226), and the general lesson already encoded in
`dxp.Claim`, which carries `Primitive` and `Resource` as two explicit fields
precisely so that a colon inside `Resource` is harmless.

The rule, stated once and applied everywhere:

> **Identity is a struct. Strings are serializations of structs. Construction
> happens through named constructors; decomposition happens through the one
> parser. Nothing else ever splits a reference.**

### 2.1 Source references

A source reference names an authoritative xolu entity. V1 defines one kind and
reserves the enumeration for later kinds:

```go
// SourceKind is the addressing space of a reference. Values are internal
// constants (English, per the identifier-language law) and are part of the
// wire contract.
type SourceKind string

const (
    SourceDocument SourceKind = "document" // a document in a collection
    // Reserved, model-admitted, not in V1:
    //   SourceObj  — an /obj subject
    //   SourceLoc  — a /loc location
    //   SourceBlob — a /blob entry
)

// SourceRef identifies one authoritative xolu entity. Fields are explicit;
// there is no packed single-string form in memory or on the wire.
type SourceRef struct {
    Kind       SourceKind `json:"kind"`
    Collection string     `json:"collection"`         // document kind: the collection name
    ID         string     `json:"id"`                 // document kind: the document id
}

// Named constructors are the only sanctioned construction sites.
func DocumentRef(collection, id string) SourceRef
```

Implementation note (verify at implementation time): the `Collection`/`ID`
pair must match how the primary store actually addresses adapted-table and
document rows, including any id-type constraints. If the store's identity for
some collections is typed (integer, ULID), `ID` remains the canonical string
rendering of that identity and the constructor validates it. This is a
deliberate check against inventing an identity scheme the substrate does not
have.

### 2.2 Unit references

A unit reference names one semantic unit inside one materialization. The
fragment is a struct, never a suffix syntax to be parsed:

```go
type FragmentKind string

const (
    FragmentNone  FragmentKind = ""      // object unit: the whole source
    FragmentChunk FragmentKind = "chunk" // bounded-text chunk
    // Reserved: "field", "relationship", "collection"
)

type Fragment struct {
    Kind    FragmentKind `json:"kind,omitempty"`
    Ordinal int          `json:"ordinal,omitempty"` // 0-based, chunk kind only
}

type UnitRef struct {
    Materialization string    `json:"materialization"`
    Source          SourceRef `json:"source"`
    Fragment        *Fragment `json:"fragment,omitempty"` // nil = object unit.
    // Pointer deliberately: encoding/json's omitempty has no effect on
    // struct values, and the wire contract (§2.3, Part 3 §2) omits the
    // field for object units. nil is the object-unit spelling everywhere.
}

func ObjectUnitRef(materialization string, src SourceRef) UnitRef // Fragment nil
func ChunkUnitRef(materialization string, src SourceRef, ordinal int) UnitRef
```

### 2.3 The wire form is structured JSON

Every API — REST bodies and responses, `pkg/client` structs, internal queue
records — exchanges references as the JSON object shapes above. Example
retrieval result row:

```json
{
    "source":   { "kind": "document", "collection": "assets", "id": "A-1842" },
    "fragment": { "kind": "chunk", "ordinal": 17 },
    "score":    0.94,
    "rank":     1,
    "xref":     "xref:document:assets:A-1842#chunk.17"
}
```

The `xref` field is a convenience rendering (§2.4), present for display,
logging, and copy-paste into CLI and OQL contexts. Consumers that need the
identity use the structured fields; a consumer that parses `xref` instead of
reading `source` is wrong by specification.

### 2.4 The canonical string form (`xref`)

One reversible string serialization exists, for exactly four uses: human
display, logs, CLI arguments, and OQL string literals. Grammar:

```
xref      = "xref:" kind ":" path [ "#" fragment ]
kind      = 1*(ALPHA)                       ; SourceKind value
path      = segment *( ":" segment )        ; segment arity is defined per
                                            ; kind: document = collection ":" id
segment   = 1*(unreserved / pct-encoded)    ; RFC 3986 unreserved set
fragment  = fragkind "." ordinal
fragkind  = 1*(ALPHA)
ordinal   = 1*DIGIT
```

Encoding rules:

- Within a segment, every character outside RFC 3986 `unreserved`
  (`ALPHA / DIGIT / "-" / "." / "_" / "~"`) is percent-encoded. In particular
  `:`, `#`, `%`, `/`, and whitespace always appear encoded. A collection or id
  may therefore contain any character, including the separators, without
  ambiguity — the property `fenceLookupPath` lacked.
- Serialization is total and canonical: one struct value has exactly one
  string form (uppercase hex in percent-escapes, no gratuitous escaping of
  unreserved characters), so xref strings are comparable byte-wise and usable
  as map keys in tooling.
- Round-trip laws: `ParseXref(String(r)) == r` for every valid `r`, and
  `String(ParseXref(s))` is byte-identical to `s` for every canonical `s`.
  `ParseXref` rejects non-canonical input rather than normalizing it.
- `ParseXref` is the only decomposition function in the codebase. Grepping for
  `strings.Split` near xref values should find nothing, ever.

Examples:

```
xref:document:assets:A-1842
xref:document:assets:A-1842#chunk.17
xref:document:manuals:pump-manual%3Av2          (id "pump-manual:v2"; ':' encoded, '-' unreserved so never encoded)
```

The scheme prefix `xref:` makes the form greppable and self-announcing in
logs. It deliberately does not reuse REST path syntax: a reference is not a
URL, and making it look like one invites exactly the path-embedding mistakes
§2 exists to prevent.

### 2.5 Reference integration with the rest of xolu

How results re-enter the substrate, made concrete:

- **Document fetch.** `result.Source.Collection` and `.ID` feed the existing
  document read path directly. No vec-side fetch API exists (P3).
- **OQL.** The `@VEC()` provider (Part 3 §3) returns `collection` and `id` as
  separate columns alongside `score` and `rank`, so joins and predicates bind
  to real columns; the `xref` column is display-only. Nothing in a query plan
  parses strings.
- **Graph and other primitives.** Where an edge or a primitive record refers
  to a document by collection and id today, the same pair from a retrieval row
  slots in unchanged. `/vec` introduces no shadow identity namespace: there is
  no such thing as "the vec id of an asset" visible to applications; internal
  vector row ids exist (Part 2 §5) and never escape the vec store.
- **dxp.** If a future capability reserves against retrieval results, the
  claim shape is already compatible: `Claim.Primitive` plus a `Resource`
  rendered from the structured ref. Not V1; noted so the shapes stay aligned.
- **Provenance in both directions.** Every unit row in the vec store carries
  its `SourceRef` fields as columns (not a packed string), so "which units
  derive from this document" is one indexed SQL lookup — the reconciler's and
  the explain operation's primary access path.

### 2.6 Materialization names

`{name}` in every route and struct obeys the grammar `[a-z0-9][a-z0-9-]{0,63}`.
Names are per-tenant unique. The restricted grammar means names never need
escaping in routes, xrefs, logs, or OQL literals — the cheapest possible
addressing story is the one where escaping cannot arise.

---

## 3. The materialization definition

A materialization is declared as data (inspectable, exportable as
configuration, diffable), with this logical shape:

```go
type Definition struct {
    Name        string      // §2.6 grammar
    Description string      // one line, for discovery and agents
    Source      SourceDecl
    Projection  ProjectionDecl
    Units       UnitPolicy
    // Representation is deployment configuration resolved at generation
    // creation time, and is recorded per generation, not per definition:
    // the definition says what the view means; the generation records what
    // produced its vectors.
}

type SourceDecl struct {
    Kind       SourceKind // V1: SourceDocument
    Collection string
    Where      string     // OQL predicate over the collection; empty = all.
                          // Evaluated by the existing query machinery —
                          // /vec owns no predicate language (P4).
}

type ProjectionDecl struct {
    Select []string // field names, in order. V1: scalar/string fields of the
                    // collection only. No traversal, no computation, no
                    // templating (deferred; the model admits them).
}

type UnitPolicy struct {
    Kind      FragmentKind // FragmentNone (object units) or FragmentChunk
    ChunkSize int          // chunk kind only: max runes per chunk; bounded
                           // fixed chunking in V1. Structural chunking is a
                           // later policy value, not a V1 option.
}
```

Population membership is derived, never enumerated: an object entering or
leaving the `Where` predicate enters or leaves the population, and
reconciliation (Part 2 §4) converges the index accordingly. Definitions with
an explicit id list are rejected by design.

### 3.1 Projection semantics

Projection is a pure function of (source revision, projection definition)
producing canonical semantic text:

```
Name: HB graphite pencil
Type: writing instrument
Manufacturer: Faber-Castell
Description: Wooden-bodied graphite writing instrument
```

Rules, carried from the draft and made normative:

- **Labeled lines, declared field order, one field per line.** Labels carry
  semantic structure to the model; declared order makes the serialization
  deterministic and the projection hash meaningful.
- **Label text is the field name rendered for humans** (implementation detail;
  must be deterministic and recorded in the projection fingerprint).
- **Absent and null fields are omitted entirely** — not rendered as empty
  labels — so sparse objects do not hash-collide with differently sparse ones.
- **Projection is read-only** and runs in the tenant's context (Part 2 §8).
- **No field weighting.** The draft's own skepticism was correct: repetition
  is not a linear weight in modern embedding models. Emphasis, when it is ever
  needed, is a projection-content concern, not a numeric knob.

### 3.2 Semantic units and their hashes

Each population member yields units per `UnitPolicy`. Every unit tracks two
distinct facts, because they answer different questions:

- **Source revision** — did the source change? (staleness accounting)
- **Projection hash** — did the *meaning* change? (embedding avoidance)

The hash is computed over the canonical projected text. An object whose
`quantity` field ticks every minute while its projected fields are stable
produces a changed revision and an unchanged hash: the reconciler records the
new revision and skips the embedder. This separation of database churn from
semantic churn is the load-bearing optimization of the whole design for
telemetry-adjacent workloads, and it is why the projection serialization must
be canonical to the byte.

Chunk units additionally record their ordinal and their own chunk-text hash.
V1 accepts that inserting text early in a document may shift subsequent chunk
ordinals and force re-embedding of shifted chunks; the per-chunk hash limits
the damage to genuinely shifted content. Content-anchored chunk identity is a
documented deferral, not an oversight.

Unit lifecycle: `derived → projected → embedded → indexed`, with `stale`
(revision moved), `failed` (with inspectable reason), and `removed` (source
deleted or left the population). The reconciler is the only writer of unit
state.

---

## 4. Generations

A generation is one complete build under one **fingerprint**:

```
fingerprint = hash(
    definition (source decl + projection decl + unit policy)
  + representation contract (provider, model, model version, dimensions,
    metric, normalization)
)
```

Rules:

- Any fingerprint change — model swap, projection edit, chunk-size change —
  requires a new generation. Vectors from different fingerprints are never
  mixed in one searchable space, and a query is always embedded with the
  serving generation's own representation contract.
- A new generation builds from a consistent snapshot of the source, then
  replays accumulated changes (snapshot plus catch-up), while the previous
  generation continues serving. Source writes are never blocked by a build.
- Publication is atomic: `current` moves from N to N+1 as one metadata write.
  Readers see whole generations only.
- Rollback is the same mechanism pointed backward: `activate(N)` while N is
  retained. Retention policy: keep the previous generation until explicitly
  discarded or a deployment-configured limit evicts it.
- The **initial** build has a publication barrier: a materialization with no
  published generation is not queryable (`building`, not `ready`). Once a
  generation is live, incremental staleness never unpublishes it: `ready`
  with pending work is a normal state.
- Crash during a build leaves `current` untouched; the incomplete generation
  is resumed or discarded on restart. Crash during incremental work is
  harmless because unit updates are idempotent (same revision, same hash,
  same vector) and guarded (a stale revision never overwrites a newer one).

Generation metadata records: fingerprint components in full, unit count,
indexed/pending/failed counts, coverage, source revision watermark, build and
publication timestamps. This is what makes any retrieval result explainable
end to end: materialization → generation → unit → projection hash →
representation contract.

---

## 5. Retrieval semantics

`retrieve(M, Q, C) → R`: within the population represented by materialization
M's current generation, rank units by semantic relevance to query Q under
constraints C, and return rows of (structured source ref, fragment, score,
rank).

Normative points, consolidated from the draft:

- **Query is meaning, not tokens.** "Wooden writing instrument" matches an
  "HB graphite pencil with cedar casing" that shares no words with it. The
  query is embedded with the serving generation's representation contract;
  the user never supplies a vector (raw-vector queries are a deferred expert
  surface, generation-pinned when they arrive).
- **`score` is deliberately generic**: relevance under this materialization's
  representation and metric. It is not a probability, and scores are not
  comparable across materializations or generations. The wire name is `score`,
  never `cosine` or `distance`.
- **Ranking is descending score with a stable tie-break** (source ref, then
  fragment ordinal), so identical state and query give identical order —
  needed by tests, caching, and agents alike.
- **`limit` (top-K) is a retrieval parameter** with a small server default and
  a server maximum. **`threshold`** is optional and, when present, is applied
  after ranking; K and threshold compose (at most K, all above threshold).
  Zero rows is a valid, distinguishable outcome (P12).
- **Chunk results carry their fragment** and group naturally by source ref;
  a `group_by_source` retrieval option collapses multiple chunks of one
  document to its best-scoring chunk. This is a retrieval option, not a unit-
  model concept.
- **`similar`** is retrieval where the query representation is an existing
  unit's stored vector: `similar(M, SourceRef, C)`. If the source is in the
  current generation, its vector is reused without an embedder call; if not
  yet indexed, the response distinguishes `not indexed` from `no similar
  objects`. The source itself is excluded from its own results.
- **Two-stage retrieval is mandatory.** Candidates come from the index with
  internal oversampling; every candidate is then revalidated against current
  source state in the caller's tenant (exists, still in population where
  cheaply checkable) before it is returned. A deleted document may briefly
  retain a vector; it must never appear in a result. This is the whole of the
  V1 authority story, stated honestly (Part 2 §8).

---

## 6. The consistency contract

> A successful source transaction is immediately authoritative. Semantic
> materializations converge asynchronously toward committed source state.
> Convergence lag is observable, never hidden.

Consequences:

- `/vec` never participates in source transaction atomicity, and `/commit`
  guarantees nothing about semantic indexes (P9).
- Staleness is exposed as data: per materialization, the source revision
  watermark versus the indexed watermark, plus pending/failed counts and
  coverage. "How stale am I" is a status read, not archaeology.
- Retrieval against a stale generation is permitted and normal; retrieval
  against an unavailable index is an explicit error (P10). The two are never
  conflated.
- Read-your-own-semantic-writes is not offered in V1. A targeted
  `refresh(SourceRef)` operation (bring this one source current, synchronously
  bounded) is admitted by the model as a later addition; it must not be
  smuggled in as a side effect of retrieval.
- Transaction-aware retrieval (seeing uncommitted state) is explicitly out of
  scope and requires its own design if ever wanted.
