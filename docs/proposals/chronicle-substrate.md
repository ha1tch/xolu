# The Chronicle Substrate — Factoring ts, cal, and bal (proposal)

Updated: 2026-07-19
Status: proposal — not scheduled. Companion to
`bal-conservation-primitive.md`, which is this substrate's first native
consumer. No register items exist until execution is decided.

## 1. Observation

xolu has two shipped time-indexed primitives and one prospective:

- **ts** records observations over time and answers windowed aggregate
  questions (min, max, sum, …).
- **cal** records commitments about time and refuses writes that would
  violate occupancy.
- **bal** (prospective) records conserved quantities over time and
  refuses writes that would violate bounds.

The machinery overlaps are not coincidental: cal's daypart rollup
hierarchy descends by adaptation from ts's rollup cascade, and bal's
design (journal, derived balances, period close, cumulative reads)
wants the same parts again. The rule of three is satisfied. This
document is the factoring analysis: what is genuinely shared, what
merely rhymes, and in which order extraction should happen.

One taxonomy line governs everything below: **ts answers questions; cal
and bal make promises.** (And promises compose: the commitment
primitives' shared prepare/confirm silhouette is formalised as a
uniform participant contract in `dxp-composed-commitment.md`, which
coordinates them under single atomic outcomes.) Codecs and rollups vary how answers are stored;
they can never express whether a write may be refused. The promise — the
admission guard — is what makes a primitive a primitive.

## 2. Invariant matrix

| Machinery / invariant | ts | cal | bal | Factor? |
|---|---|---|---|---|
| Append-only journal, immutable past | ✓ | partial — records mutate until sealed | ✓ | **No** (leaky) |
| Seal frontier: mutable present → immutable past | ✗ | ✓ (`Sealer`) | wanted (period close) | **Yes** — extract from cal |
| Rollup cascade over a grain hierarchy | ✓ (origin) | ✓ (descendant) | wanted | **Yes** — the big one |
| Derived index + rebuild oracle | ✓ | ✓ | ✓ | **Yes** — as a harness |
| Admission guard (refuse on fold-violating write) | ✗ | ✓ (occupancy) | ✓ (bounds) | **Doctrine, not code** |
| Two-phase lifecycle (propose/confirm; holds) | ✗ | ✓ | future | **Doctrine, not code** |
| Sized-id discipline at the wire boundary | corrected (int64 wire + helper) | ✓ (string id; no numeric surface) | inherited (§9a) | **Yes — law (§4d)** |

## 3. The mathematical core

The rollup cascade, in all three primitives, is one theorem implemented
three times: **a monoid homomorphism over a time-grain hierarchy.**

- ts folds `(number, +, 0)` and `(min/max with identities)`.
- cal's dayparts fold `(bitset, OR, ∅)`.
- bal folds `(int64, +, 0)` — and its cumulative read (balance as-of) is
  the fold of a prefix: the same monoid, chained across sealed
  checkpoints.

The extraction is therefore a **generic cascade engine parameterised by
the monoid**: an associative combine, an identity element, cascade
upward on append, invalidate downward on correction. Everything else in
each primitive's rollup code — bucket keying, grain hierarchy walking,
cascade ordering — is already identical in intent and duplicated in
implementation.

## 4. Extraction inventory

Accepted — three extractions, in dependency order:

1. **The cascade engine** (from ts, generalised): grain hierarchy,
   bucket store, monoid-parameterised fold, upward cascade, downward
   invalidation. ts's numeric aggregates and cal's occupancy dayparts
   become instantiations; bal's sum rollups arrive free.
2. **The Sealer** (from cal, lifted intact): frontier arithmetic,
   sealed-boundary admission checks, and — expensively learned — the
   serialisation discipline between frontier advance and cross-plane
   writes. bal's period close is a second consumer on day one; ts may
   optionally adopt it later for frozen-window semantics.
3. **The rebuild-oracle harness**: one test-and-tooling shape,
   `derive(journal) == current`, taking a per-primitive `deriveFn`,
   feeding `iolu db check` uniformly (operations roadmap, item 5).

Refused — recorded as deliberately as the acceptances:

- **A generalised Journal abstraction.** cal's records mutate through a
  lifecycle until sealed; ts and bal are pure-append. One interface over
  both shapes is an abstraction with caveats where three concrete tables
  have clarity. Not factored.
- **An admission-guard framework — refined.** The guard *predicates*
  are not factorable: their content differs completely (set
  intersection versus integer bounds), and a predicate framework would
  be scaffolding around ten lines of guarded SQL. The guard
  *discipline* is doctrine, written here. What IS factorable — the
  complement this refusal originally missed — is the **reservation
  lifecycle**: tentative rows (`txn_id, state, deadline`) walking
  `reserved → confirmed | released | expired | invalidated` are
  content-uniform across every commitment primitive, and the engine
  owns that machine as the reserved-commit facility
  (@D05b). Predicates stay per-primitive;
  the lifecycle is substrate.
- **Lifecycles on pkg/fsm.** cal's booking states and bal's future holds
  share the state-graph-as-mutex theology, not machinery. Coupling two
  primitives through a third for symmetry is refused.

## 4a. Substrate law: guard locality

One principle has now been derived independently by every commitment
design in this programme, which promotes it from decision to law:

> **A guard must live where its transaction lives.** The state a guard
> reads and the write it authorises must commit or abort together; any
> architecture that splits them across engines, tables-of-different-
> primitives, or planes reintroduces the check-then-act race the guard
> exists to prevent.

Independent derivations: **bal** pins current balances to the journal's
SQL plane because the bounds guard reads them in the entry's own
transaction (@B05); **cal** permits its Pebble plane only because no
guard ever consults it — admission runs against the SQLite record;
**referential integrity** runs its enforcement read inside the delete's
transaction (@R02.2, @C05); **dxp** refuses to host its phase state on
fsm because the phase gate reads participant rows that must commit with
the phase write (@D08a). Any future primitive proposing a guard that
reads derived, cached, or foreign-primitive state must answer to this
section.

## 4b. Substrate law: finiteness — forgetting is not editing

Timelines are not infinite; every chronicle primitive must state how
its past ends. The law reconciling retention with the seal:

> **The seal forbids rewriting the past; retention permits forgetting
> it** — wholesale, at sealed-period granularity, by declared policy.
> Deleting an entire frozen period leaves no seam to tamper with, so
> the two never conflict.

Per-primitive mechanisms:

- **ts** — shipped: per-timeline `RetentionDays` with store default and
  expiry sweep. Grain-tiered retention is the natural policy shape: raw
  buckets expire early, coarse rollups persist long.
- **bal** — **prefix-collapse via checkpoints**: entries older than a
  sealed checkpoint are derivationally redundant (balance = checkpoint
  + tail), so policy may archive-then-prune the pre-checkpoint journal
  while conservation survives through the checkpoint chain. The
  rebuild oracle re-scopes to "from the earliest retained checkpoint."
  The mechanism introduced for read efficiency is the retention
  mechanism — one design, two problems.
- **cal** — sealed periods beyond a per-calendar policy age are dropped
  whole; occupancy history optionally archives first.
- **dxp** — terminal instances archive (seal-shaped; @D07a) and prune
  by policy.

Archival, where wanted, targets blob cold storage before pruning; it is
optional everywhere, and never a precondition for correctness.

## 4c. Substrate law: meta is engine-inert

/meta (the per-subject key/value sidecar, optional TTL) serves every
primitive as the annotation slot — subjects generalise to
`dxp.def`, `dxp.txn`, `ts.timeline`, `cal.calendar`, `cal.booking`,
`bal.account`, `fsm.machine`, and entities — under one law:

> **No engine ever reads meta to decide behaviour.** Meta carries
> annotations for humans, applications, and exports (external
> reference codes, correlation IDs, labels, descriptions). Anything an
> engine consumes — retention settings, match policies, deadlines,
> TTLs that gate lifecycles — lives in that primitive's own
> definition or rows. Meta's TTL expires annotations, never
> lifecycles; meta is not a timer (@D07a's clock rule, generalised).

Corollary for high-volume immutable records (journal entries): their
descriptive text is **inline on the record**, written and frozen with
it — the sidecar is for long-lived mutable subjects.

## 4d. Substrate law: sized ids survive the wire

A fourth principle was derived the expensive way — a 32-bit CI break
and six latent runtime truncations in /ts (2026-07-20) — and, like the
others, was found to have already been solved correctly by *two* other
primitives independently. It promotes to law:

> **A primitive's id has one width, and that width is preserved at
> every boundary it crosses.** An id typed `uintN` internally must
> never be held, parsed, compared, or serialised through a narrower or
> platform-dependent type — not `int` (which is 32-bit on some targets,
> so a `uint32` id above 2³¹ truncates and a `uint32`-max constant
> converted to `int` fails to compile), not a `uintM` with M < N. The
> JSON/transport boundary is where this is lost: request and response
> structs, path- and query-parameter parsing, and any ceiling constant
> are the four sites where a sized id silently narrows.

### The preferred structure: two identities, not one

The deepest form of compliance is architectural, and **cal and bal both
arrived at it independently**: give a subject **two identities** —

- an **external identity**: a caller-chosen, opaque *string* that
  appears at the API boundary (`cal.CalendarID` = "room-204";
  `bal.account_id` = "warehouse:A/widget"). Strings have no numeric
  width to truncate, so the wire boundary is trivially safe, and the
  id is human-meaningful for operators and agents.
- an **internal identity**: a system-allocated dense `uintN` that never
  leaves the engine, used only for the compact storage codec
  (`cal.CalOrdinal uint32`, allocated ascending; bal's internal account
  key). Encoded with an explicit fixed-width codec, compared only
  inside the engine — it never crosses a boundary, so it cannot narrow.

This split is strictly superior to a single conflated id: the string
absorbs the boundary-safety obligation while the dense int keeps storage
compact, and the two concerns stop fighting. A primitive built this way
satisfies the law by construction — there is no numeric id on the wire
to protect.

### The fallback: one id, carried wide (ts's corrected form)

Where a primitive exposes a numeric id directly — /ts's historical
model, where `TimelineID uint32` is both the caller-supplied id and the
codec key — the law is satisfied defensively: carry the id as `int64`
in every wire struct (the only integer width holding a full `uint32` on
32-bit platforms), parse path/query ids with an explicit bit size
(`ParseUint(s, 10, 32)`, never 16), never convert the ceiling constant
to `int`, and route every crossing through a single validating helper
(`timeseries.TimelineIDFromJSON`) so no call site hand-narrows. This
works, but it is a correction, not a structure: the numeric id is still
on the wire, and the discipline must be actively maintained at every new
boundary rather than made impossible.

Independent derivations: **cal** and **bal** use the two-identity split
(string external id, dense `uintN` internal) and have no numeric-id
boundary surface at all; **ts** conflates the two into one exposed
`uint32` and, after the widening (@P item #8) left `int`/`uint16`
boundary fields that broke the 32-bit build and truncated ids above
65 535, now carries it as `int64` behind a validating helper.
Retrofitting ts to the two-identity model is the correct long-term
direction but a breaking API change (T-46, deferred); until then ts's
corrected wide-carry form holds the line. Any future primitive that
exposes an id must answer to this section: prefer the two-identity
split; if a numeric id must be exposed, carry it wide behind a helper;
and ship the range test (full `uintN` span including values above
2^(N-1) and the ceiling) with stage 1, not as an afterthought.

## 5. Sequencing

Extraction happens **as part of building bal**, which becomes the
cascade engine's and the extracted Sealer's first native consumer —
this is what makes bal's own proposal short and its implementation
thin.

**cal migrates opportunistically or never.** Its internals are
stress-verified on real hardware; re-plumbing working, race-hardened
code onto a fresh abstraction spends verified stability to purchase
tidiness. New consumers ride the substrate; stable incumbents earn
migration only when next touched for their own reasons, and the
register records that debt only if and when it exists.

ts's adoption of the generic engine follows the same rule.

## 6. Non-goals

- A general event-sourcing framework, a plugin system, or any
  abstraction without a named second consumer. The substrate exists
  because three concrete primitives share three concrete mechanisms;
  it grows only when a fourth concrete need arrives.
