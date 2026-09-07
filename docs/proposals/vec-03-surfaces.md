# /vec — Part 3: Surfaces

Updated: 2026-08-20
Status: Proposal. Supersedes the seven-part draft of 2026-08 (see Part 0 §1).

---

## 1. Surface principles

- **POST bodies carry queries and references.** Natural-language text and
  structured refs never travel in URL path segments (Part 1 §2; the XOT226
  rule applied to the transport). Path segments carry only the materialization
  name, whose restricted grammar (Part 1 §2.6) needs no escaping.
- **One engine, several spellings.** REST, `pkg/client`, `@VEC()`, and `xia`
  are views over the same `pkg/vec` operations. No surface has semantics of
  its own.
- **Structured refs on the wire, xref for humans.** Every response row carries
  the JSON ref fields as data and the canonical `xref` string as display
  convenience (Part 1 §2.3–2.4).
- **The draft's charming path form is documentation only.** Prose may still
  say `/vec/assets/retrieve/"…"` as shorthand for the concept; no such route
  exists.

---

## 2. REST wire contract (`/api/v2/vec`)

All routes tenant-scoped like the rest of the v2 surface. `{name}` obeys the
Part 1 §2.6 grammar.

| Route | Body | Returns |
|---|---|---|
| `POST /api/v2/vec/def` | Definition (Part 1 §3 shape, JSON) | created definition; conflict if name exists |
| `PATCH /api/v2/vec/{name}` | partial definition | updated definition. Any change touching the fingerprint marks the current generation superseded and requires `rebuild` to take effect; the response says so explicitly |
| `GET /api/v2/vec/list` | — | names + descriptions + state summaries (the discovery surface; agents read this) |
| `GET /api/v2/vec/{name}` | — | definition + current generation metadata + status (state, generation, units, indexed, pending, failed, coverage, source watermark, indexed watermark, resident bytes) |
| `DELETE /api/v2/vec/{name}` | — | removes definition and derived state. Never touches sources (P1) |
| `POST /api/v2/vec/{name}/materialize` | `{}` | starts/continues a reconciliation pass against the current generation; returns `{state, generation, pending}` immediately (asynchronous; poll status) |
| `POST /api/v2/vec/{name}/rebuild` | `{}` | creates the next generation from a snapshot and builds it; previous generation keeps serving |
| `POST /api/v2/vec/{name}/activate` | `{generation}` | atomic switch of `current` (publication and rollback are the same verb) |
| `POST /api/v2/vec/{name}/retrieve` | `{query, limit?, threshold?, group_by_source?, include_snippets?}` | result rows (below) |
| `POST /api/v2/vec/{name}/similar` | `{source: SourceRef, limit?, threshold?, include_snippets?}` | result rows; distinguishes `not indexed` from empty (typed error vs. empty array) |
| `POST /api/v2/vec/{name}/explain` | `{source: SourceRef}` | the unit(s) for that source: projected text, projection hash, source revision, state, generation fingerprint — the debugging surface |

Result row shape (retrieve and similar; this example shows a request with
`include_snippets: true`):

```json
{
    "source":   { "kind": "document", "collection": "assets", "id": "A-1842" },
    "fragment": { "kind": "chunk", "ordinal": 17 },
    "score":    0.94,
    "rank":     1,
    "xref":     "xref:document:assets:A-1842#chunk.17",
    "snippet":  "Name: HB graphite pencil\n..."
}
```

`fragment` is omitted for object units. `snippet` is the projected text
(derived content, regenerable, clearly not authoritative source data) and is
returned only when the request sets `include_snippets: true` — RAG callers
want it; discovery callers do not need the payload.

Error taxonomy (typed, named, consistent with the tree's sentinel-error
practice): `vec_not_found`, `vec_not_ready` (no published generation),
`vec_unavailable` (store/resident failure — never an empty result),
`vec_not_indexed` (similar's source has no vector yet), `vec_limit_exceeded`
(named limit and value), `vec_conflict` (concurrent reconcile/rebuild),
`vec_generation_unknown` (activate target missing).

---

## 3. OQL: the `@VEC()` provider (wave-8 adjunct)

Once T-76's primitive-query dispatch exists, `/vec` joins `@FSM`, `@BAL`,
`@CAL`, and `@TS` as the fifth provider — reusing the TableValuedFunction
dispatch, never a parallel mechanism.

```sql
SELECT v.id, v.score, a.name, a.site
FROM @VEC('assets', 'high pressure centrifugal pump', 20) AS v
JOIN assets AS a ON a.id = v.id
WHERE v.score > 0.8 AND a.status = 'active'
ORDER BY v.rank
```

Provider contract:

- **Signature:** `@VEC(name, query [, limit])` — string literals or bound
  parameters; `limit` defaults to the server retrieval default.
- **Columns:** `collection` (string), `id` (string), `fragment_kind` (string,
  empty for object units), `fragment_ordinal` (int, null for object units),
  `score` (float), `rank` (int), `xref` (string, display-only by contract).
  Joins bind to `collection`/`id`; nothing in a plan parses `xref` — the
  Part 1 §2.5 rule expressed as a column layout.
- **Semantics:** identical to REST retrieve on the current generation,
  including the two-stage revalidation. The provider is candidate generation;
  predicates, joins, grouping, and ordering are the ordinary engine's job —
  which resolves the draft's "score should be available to queries" and
  "query planner opportunity" sections with machinery that already has a
  design (T-76) instead of a new one.
- **Syntax restriction (T-137):** the table-valued call form above is the only
  supported form. Chained method-call access (`@VEC.something(...)`) is
  excluded while tsqlparser drops the base of chained calls (T-137); the
  restriction is documented with the provider and lifted only after T-137
  closes.
- **Filter push-down** into the index is explicitly a future optimization
  behind the same provider, contingent on measurement (the draft's correct
  boundary): V1 plans are always retrieve-then-filter.

---

## 4. `pkg/client`

Typed methods in the waves 15–16 pattern (the bal/cal/dxp/loc/obj shape:
request/response structs, named ref constructors, no stringly-typed identity):

```go
// Definitions and lifecycle
func (c *Client) VecDefine(ctx, def VecDefinition) (VecInfo, error)
func (c *Client) VecPatch(ctx, name string, patch VecDefinitionPatch) (VecInfo, error)
func (c *Client) VecList(ctx) ([]VecSummary, error)
func (c *Client) VecGet(ctx, name string) (VecInfo, error)
func (c *Client) VecDelete(ctx, name string) error

// Maintenance
func (c *Client) VecMaterialize(ctx, name string) (VecMaintenance, error)
func (c *Client) VecRebuild(ctx, name string) (VecMaintenance, error)
func (c *Client) VecActivate(ctx, name string, generation uint64) (VecInfo, error)
func (c *Client) VecStatus(ctx, name string) (VecStatus, error)

// Retrieval
func (c *Client) VecRetrieve(ctx, name, query string, opt *VecRetrieveOptions) ([]VecResult, error)
func (c *Client) VecSimilar(ctx, name string, src vec.SourceRef, opt *VecRetrieveOptions) ([]VecResult, error)
func (c *Client) VecExplain(ctx, name string, src vec.SourceRef) (VecExplanation, error)
```

With:

```go
type VecRetrieveOptions struct {
    Limit           int
    Threshold       *float64 // nil = no threshold (explicit tri-state, the LocPatch lesson)
    GroupBySource   bool
    IncludeSnippets bool
}

type VecResult struct {
    Source   vec.SourceRef
    Fragment *vec.Fragment // nil for object units (Part 1 §2.2)
    Score    float64
    Rank     int
    Xref     string
    Snippet  string // empty unless requested
}
```

Ref construction goes through the Part 1 constructors re-exported by the
client (`vec.DocumentRef(collection, id)`), so no caller ever hand-assembles
identity, mirroring `LocFenceRefByLocation`/`BySubject`. `VecSimilar` takes a
`SourceRef`, not a string — the client-side embodiment of the XOT226 fix.

Typed errors mirror §2's taxonomy as sentinel errors so callers distinguish
`ErrVecNotIndexed` from an empty slice without string matching.

---

## 5. `iolu` / `xia`

Under the T-81 split (admin CLI → `xia`, query CLI → `iolu`), the verbs land
as follows; until the split ships they live on the current admin CLI with the
same names.

**xia (admin, some offline-capable):**

| Verb | Online? | Does |
|---|---|---|
| `xia vec list` / `xia vec info <name>` | offline | read definitions and status from the vec store directly |
| `xia vec define/delete <name>` | offline | manage definitions (offline against a stopped server, the iolu convention) |
| `xia vec materialize/rebuild <name>` | **online only** | both need the embedder; refused offline with an explanatory error rather than attempted degraded |
| `xia vec activate <name> <gen>` | offline | a metadata write; legitimate offline rollback tool |
| `xia db check` | offline | gains the vec oracle (Part 2 §6): projection-hash and population verification, embedder-free by construction — joins the cal/bal oracle wiring tracked under T-73 |

**iolu (query):** `iolu query` and the REPL reach `/vec` through `@VEC()` once
that lands; additionally `iolu vec retrieve <name> "<query>"` gives a direct
one-shot spelling (client-backed, online) so semantic retrieval is usable from
the shell before wave 8. Output rows print the xref string plus score — the
xref form's intended habitat.

**molu:** materialization list with state, generation, coverage, staleness
watermark, and resident bytes — a status panel, not a management surface, in
keeping with molu's read-mostly role. Any user-facing strings follow the
standing two-locale verification rule.

---

## 6. Documentation and tracking obligations

For the implementing wave (obligations, not filed here — this document is a
proposal):

- These four documents move to `docs/proposals/` now; when implementation
  starts they receive the standard reconciliation banner as reality diverges,
  per the tracking practices — never silent rewrites.
- Register items at filing time: one per build-order stage (Part 0 §6), plus
  the `@VEC()` provider as a wave-8-adjacent item gated on T-76, plus the
  `/cron` verb registration gated on XOT197–XOT202.
- `EVENT_MODEL.md` gains nothing in V1 (no events are emitted or consumed);
  the change-feed adapter's future event consumption is noted there when T-07
  work begins, not before.
- The dormant-guards table gains the Part 2 §6 entries in the same session the
  guards are written.
- MANUAL/README coverage: the consistency contract (Part 1 §6) and the
  authorization reality (Part 2 §8) are user-facing documentation, stated in
  the same plain terms as here — the guarantees offered, and nothing stronger.
