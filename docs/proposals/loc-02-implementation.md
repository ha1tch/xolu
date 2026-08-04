# `loc` — staged implementation plan

**Status, added 2026-08-01 against the v0.21.2 checkpoint:
executed.** All six stages below shipped as wave 9 (T-115–T-118,
`CHANGELOG.md` v0.21.0), with a follow-on adversarial hardening pass
(v0.21.1) finding and fixing five real bugs and a cross-primitive
audit (v0.21.2) checking `cal`/`fsm`/`entity` for the same two bug
classes. This document is retained as the plan that was actually
followed — Stage boundaries, exit criteria, and the sequencing
rationale all held up — not rewritten into a fictional past tense.
Two real findings from the build itself, not anticipated by this
plan, are added in place below (Stage 2, Stage 5); everything else
here matches what shipped closely enough that no other correction was
needed.

`loc-02` of the loc document series: see `loc-00-design.md` for the
model and doctrine, `loc-01-rest-api.md` for the wire surface this
plan builds handlers for. Prepared: 2026-07-31, against v0.20.0.
Revised same day for Stage 2's throughput-benchmark addition, below —
the rest of this plan is unaffected by `loc-00`/`loc-01`'s own
same-day revision, since standalone-fence identity (now entity-
addressed rather than a caller-chosen `fence_id`) never appeared as a
concrete schema detail in this document to begin with; it stayed
abstract at the `fence_key` surrogate-key level throughout. **That
turned out to matter twice over: it also meant this document made no
claim the shipped, narrower fence-identity model (see `loc-00-design.md`
§5's own reconciliation note) could contradict.**

> This sequences the build of the `loc` spatial primitive, the way
> `cal-implementation-plan.md` sequenced `cal`'s. The two situations
> differ in one important respect, worth stating up front rather than
> discovering partway in: `cal` needed that separate staging document
> because its design still carried three open semantic gates (match
> plane semantics, `match/commit` in v1 or not, the booking-record
> schema) at the point staging began. `loc-00-design.md`'s own revision
> pass closed the equivalent gates for loc — subject registry scope,
> standalone-fence capacity mechanics, §7c's authorisation deferral —
> before this document was written. What's left here is sequencing and
> concrete exit criteria, not open decisions. Treat the absence of a
> "Dependency gates" section, present in `cal`'s plan, as a fact about
> where loc's design already stood, not an omission.

## Principles

- **Each stage ends green.** Build + test pass before the next stage
  starts. No stage leaves the tree broken for the next to "fix on the
  way through" — the same discipline `cal-implementation-plan.md`
  states, unchanged for loc.
- **`derive(journal) == current` is the spine** — loc's own version of
  `cal`'s `index == rebuild`. §8c of `loc-00-design.md` already commits
  to this as the acceptance oracle for stateful work; this document
  pins the actual SQL (Stage 4) rather than leaving it as a cross-
  reference to "the chronicle substrate's existing shape."
- **Mirror `bal`, not `cal` or `ts`.** loc has no bit-packed codec, no
  Pebble-plane source of truth — canonical state is SQL throughout
  (§6a of `loc-00-design.md`), the same shape `bal` already has. Package
  and test structure follow `pkg/bal`'s file layout
  (`model.go`, `store.go`, `dxp_adapter.go`, `verify.go`, plus a
  `geometry.go` `bal` has no equivalent of), not `pkg/cal`'s
  bitmap/codec split or `pkg/timeseries`'s Pebble-primary shape.
- **No blocking gates remain, only sequencing.** Six stages, numbered
  to match `loc-00-design.md` §14's own item list (§14's items 1–6 map
  to Stages 1–6 below; Stage 0 is new, added here the way `cal`'s plan
  added its own Stage 0).

---

## Stage 0 — Package skeleton and decision capture

**Goal:** `pkg/loc` exists, compiles, and pins the settled inputs —
including the three decisions this plan resolves that `loc-00-design.md`
left to implementation time — as code-level constants and doc comments,
so later stages cannot drift from the design.

- Create `pkg/loc/` mirroring `pkg/bal/`'s layout: `model.go` (types —
  `Placement`, `GeoAnchor`, the `Geometry` interface and its `Circle`/
  `Polygon` implementations from §4a of `loc-00-design.md`), `store.go`
  (SQL schema and CRUD), `admission.go` (the capacity guard, Stage 2),
  `geometry.go` (containment tests and the R-tree/Geopoly wiring,
  Stage 3), `journal.go` (Stage 4), `dxp_adapter.go` (Stage 5),
  `verify.go` (the rebuild oracle, Stage 4).
- `doc.go`: package doc stating §0 of `loc-01-rest-api.md`'s two-write-
  path distinction in prose — `move` is identity-based tree
  reassignment, `report` resolves fence geometry only, and nothing in
  this package ever conflates the two. This is the single fact about
  loc most likely to be silently violated by a later contributor who
  hasn't read the design doc; it goes in the package doc precisely so
  `go doc` surfaces it.
- **Decision, pinned here: tenancy follows `bal`'s pattern, not
  `cal`'s.** `cal` needed a `calMgr` (enable/disable, per-tenant
  lifecycle, `s.calMgr.CalFor(tenantID)`) because `cal` can be absent
  from a given server instance. Nothing in `loc-00-design.md` names an
  equivalent reason for loc to be independently enable/disable-able —
  no feature exists yet that depends on loc being optional. `s.locStore(r)`,
  a plain per-request store constructor mirroring `s.balStore(r)`, is
  the default; a manager type is added later only against a concrete
  need, per the demand-gates principle `SUBSTRATE_DEVELOPMENT_PLAN.md`
  §1 already states for the rest of the programme. Storage lives at
  `<data-root>/tNNNN/loc/` in the per-tenant directory layout, a new
  sibling to `store/` and `ts/`, not folded into the existing
  `store/xolu.db` — loc's tables are numerous enough (locations,
  fences, capacity, journal) to warrant their own file, the same
  reasoning `ts` already applies with its own `ts/` directory.
- **Decision, pinned here: JSON-decode discipline for coordinate
  fields, resolving §9b's "worth checking on arrival."** The concrete
  risk bal's `Amount` precedent guards against is a raw-map decode
  path: `decodeDxpParticipantParams`'s `"bal"` case decodes into
  `map[string]interface{}` first specifically because `Amount` needs
  custom decimal parsing, and that intermediate step is where an
  unvalidated string could smuggle something the typed path would have
  rejected. loc's coordinate fields have no equivalent reason to go
  through a raw-map intermediate — a bare JSON number decoded directly
  into a Go `float64` struct field is already rejected by
  `encoding/json` if the token is malformed, and standard JSON has no
  literal `NaN`/`Infinity` token at all. **The actual obligation is
  narrower than "check on arrival" suggested: never decode a
  coordinate field through `map[string]interface{}` or a string
  intermediate.** If a string-based coordinate representation is ever
  introduced (a GeoJSON string export/import path, say), *that* code
  path needs an explicit `math.IsInf`/`math.IsNaN` guard after
  `strconv.ParseFloat` — `strconv.ParseFloat` accepts `"NaN"`/`"Inf"`
  as valid input even though the JSON spec never permits them as bare
  tokens, which is exactly the gap a naive string-based path would
  open. `loc.MoveParams` and every geometry-bearing request type
  decode coordinate fields as plain typed `float64` struct fields,
  full stop; a lint-level check (`grep -n 'map\[string\]interface{}'
  pkg/loc/*.go` returning nothing outside test files) is the cheap
  regression guard, run as part of this stage's exit criteria.
- **Decision, pinned here: client library and iolu CLI are v1 non-goals,
  stated rather than left silent.** `bal`'s own `pkg/client` methods
  shipped as `T-67`, well after `bal` itself was solid — the same
  deferral is correct for loc, and this plan states it explicitly so
  it surfaces as a deliberate choice rather than a gap discovered
  later. No iolu subcommand for loc in this plan either; `iolu`'s own
  wave-6 backlog (`T-69`–`T-75`) is unrelated primitive work already
  in flight, and loc doesn't compete for that queue's attention until
  a real operator need names it.
- `XOLU-LOC` error constants from `loc-01-rest-api.md`'s table, as Go
  constants in `pkg/errors` alongside the existing `XOLU-BAL`/`XOLU-CAL`/
  `XOLU-DXP` families (confirmed present there, though `ERROR_CODES.md`
  itself is stale for all three and stays that way — a pre-existing gap,
  not this stage's to fix).

**Gate:** none. **Exit:** `go build ./pkg/loc/` clean; `go vet` clean;
the raw-map lint check above returns nothing; package doc states the
two-write-path distinction in the exact terms `loc-01-rest-api.md` §0
uses, checked by eye against that section, not paraphrased from memory.

---

## Stage 1 — Tree + placement, no guards yet

**Goal:** the containment tree (§3a/§3b of `loc-00-design.md`) exists
in SQL, structurally correct, with no capacity or admission behaviour
yet — `bal`'s own account-definition layer, adapted.

- Schema:
```sql
CREATE TABLE IF NOT EXISTS locations (
    location_key INTEGER PRIMARY KEY,          -- internal uint32, dense
    location_id  TEXT    NOT NULL UNIQUE,       -- external string (§11a)
    parent_key   INTEGER NULL REFERENCES locations(location_key),
    name         TEXT    NOT NULL,
    postable     INTEGER NOT NULL DEFAULT 1,
    offset_x     REAL    NOT NULL DEFAULT 0,
    offset_y     REAL    NOT NULL DEFAULT 0,
    offset_z     REAL    NOT NULL DEFAULT 0,
    rotation     REAL    NOT NULL DEFAULT 0,
    anchor_lat   REAL    NULL,
    anchor_lon   REAL    NULL,
    anchor_alt   REAL    NULL,
    anchor_true_north REAL NULL,
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```
  A root row (`parent_key IS NULL`) with any `anchor_*` column NULL is
  rejected at write time (`XOLU-LOC010`) — enforced in `store.go`, not
  left to a `CHECK` constraint, since the rule spans four nullable
  columns and SQLite's `CHECK` diagnostics on a multi-column condition
  are unhelpful to whoever hits it first.
- `def`/`list`/`get`/`patch`/`delete`, structural only — `patch`
  refuses a `postable`/`parent_key` change outright (§1 of
  `loc-01-rest-api.md`), `delete` refuses on children without
  `force` (occupancy refusal is Stage 2's concern, not reachable yet
  since nothing can be assigned until then).
- Placement-chain composition: given a node N levels deep, walk
  `parent_key` to the nearest ancestor with a non-NULL `anchor_lat`,
  composing `offset_x/y/z` + `rotation` at each hop into one absolute
  point. Pure function, no guard, table-driven test against hand-
  computed expected points at 1, 2, and 4 hops deep.

**Testing:** placement-chain composition test (§10 of `loc-00-design.md`
— a node three levels deep under a georeferenced ancestor resolves to
the same absolute point regardless of which intermediate frame is
queried, i.e. composing top-down and composing bottom-up-then-inverting
agree); mixed-anchor warning fires on a synthetic two-site tree without
hard-refusing it (§1 of `loc-01-rest-api.md`).

**Gate:** none. **Exit:** `def`/`list`/`get`/`patch`/`delete` round-trip
clean; placement-chain composition test green; root-without-anchor
refusal (`XOLU-LOC010`) proven.

---

## Stage 2 — Assignment, capacity, the move transaction — the
guard-bearing core

**Goal:** §3c/§3d and §5a's capacity guard (leaf **and** fence,
resolved identically per `loc-00-design.md`'s revision), §7a's move
transaction, and the T-34-class race proof. This is the highest-risk
stage in the whole primitive — §10 names the race class by name, and
this is where the discipline that prevents it actually gets written.

**Deliberately built here without real fence geometry.** Stage 3
brings the geometry engine; this stage builds and race-tests the
capacity guard against a `fences` table that exists (id, `capacity`,
`count`) but whose membership-determination is a direct test hook, not
a geometric one — the guard doesn't care *how* a fence's membership
changed, only that it's asked to admit or refuse one. Wiring real
`Contains` tests into this same guard is Stage 3's job, not a
rebuild of Stage 2's guard logic.

- Schema:
```sql
CREATE TABLE IF NOT EXISTS loc_capacity (
    location_key INTEGER PRIMARY KEY REFERENCES locations(location_key),
    ceiling      INTEGER NULL,
    count        INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS loc_fence_capacity (
    fence_key    INTEGER PRIMARY KEY REFERENCES fences(fence_key),
    ceiling      INTEGER NULL,
    count        INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS loc_assignment (
    subject_ref  TEXT PRIMARY KEY,              -- entity REF, canonical string form
    location_key INTEGER NULL REFERENCES locations(location_key),
    updated_at   TIMESTAMP NOT NULL
);
```
- **The CAS predicate — the house discipline, `bal`'s §6 pattern
  applied to admission instead of bounds:**
```sql
-- entry (leaf or fence, same shape, table name varies):
UPDATE loc_capacity
   SET count = count + 1
 WHERE location_key = ?
   AND (ceiling IS NULL OR count + 1 <= ceiling);
-- rows affected 1 -> admitted; 0 -> refused (XOLU-LOC002), nothing written.

-- exit:
UPDATE loc_capacity
   SET count = count - 1
 WHERE location_key = ?
   AND count > 0;
-- rows affected 0 here is not a normal refusal -- it is an exit with
-- no matching entry, an invariant violation. Asserted, not silently
-- ignored, the same fsck-style treatment dxp gives its own impossible
-- "abandoned-dirty" case.
```
  Decision inside the write's predicate, rows-affected as the verdict
  — never read-then-decide-then-write. Any loc code path that reads a
  count and then writes based on it in a second statement is T-34
  reborn, exactly the language `bal-conservation-primitive.md` §6 uses
  for its own equivalent case.
- **Multi-target atomicity.** A single move can touch the destination
  leaf's capacity row *and* every capacity-bearing fence being entered
  or exited, all in one transaction. Apply every CAS in sequence
  within the transaction; the **first** zero-rows-affected result
  refuses the whole transaction (rollback), not a partial application
  — an entry that succeeded against the leaf but failed against a
  fence must not commit the leaf's half. This is the one place loc's
  guard is structurally more complex than `bal`'s own single-account
  CAS: `bal` never guards more than one account's bound in one
  transfer leg, loc routinely guards N targets in one move.
- `move` (§3 of `loc-01-rest-api.md`): read current assignment,
  evaluate every CAS for the destination leaf and the (test-hook,
  Stage 3 will make it real) fence-membership delta, write
  `loc_assignment`, append one journal-entry stub (Stage 4 makes the
  journal itself real; this stage needs only that the table exists
  and gets one row per move, since Stage 4's rebuild oracle depends on
  every move having produced exactly one entry from day one).

**Testing:** a T-34-pattern race harness — N goroutines moving into
one near-ceiling leaf, asserting winners + refusals = N and final
count ≤ ceiling, mirroring `bal`'s own admission-race test shape
directly. Per the testing-discipline convention: this needs true
multi-core parallelism to mean anything (a single-core sandbox pass is
not evidence), so it's stress-tagged, registered in the dormant-guards
table in this same session, and handed to the team with its exact
invocation rather than trusted from a sandbox run. A second test
proves the multi-target atomicity rule directly: a synthetic move
where the leaf CAS succeeds and a fence CAS is forced to fail,
asserting the leaf's count is unchanged after rollback.

**Added in this pass — a benchmark, not just a correctness test, that
was never stated anywhere before this revision:** adversarial testing
against real high-frequency scenarios (warehouse bin-to-bin putaway,
hundreds of small moves per minute) surfaced that this stage's own
write-path throughput has no stated target at all. §6b's empirical
verification covered the R-tree/Geopoly *read* side only. A sustained-
load benchmark against this stage's own guard-bearing write path
(single-leaf and single-fence CAS, no geometry, matching what this
stage actually builds) belongs here, with a number recorded — even a
rough one — before Stage 2 is called done, not deferred to whenever
someone notices a busy warehouse is slow.

**Gate:** none beyond Stage 1. **Exit:** leaf and fence capacity CAS
both proven correct in single-threaded tests; multi-target atomicity
proven; the race harness exists, is registered, and has a sandbox pass
recorded — full multi-core confirmation is the team's to run and report
back, per the house convention for this defect class; a write-path
throughput number recorded, however rough.

**Recorded, 2026-08-02 (T-129, wave 9b) — closing this exit criterion,
never met before v0.21.0 shipped.** On inspection, the single-leaf
half already existed: `admission_test.go`'s own `BenchmarkMove`,
written when this requirement was first added, was never actually
run or its number written down anywhere — the gap was "not recorded,"
not "not built." The multi-target half genuinely didn't exist and was
added (`throughput_bench_test.go`, `BenchmarkMove_LeafAndFence`, via
the same `EnteredFenceKeys`/`ExitedFenceKeys` test hook the
correctness tests already use). Both run sequentially, single
goroutine, unlimited capacity throughout (never refused, so every
call measures the guarded write itself, not refusal handling) —
sandbox hardware, `-benchtime=1s`:

| Benchmark | ns/op | ops/sec (derived) | allocs/op |
|---|---|---|---|
| `BenchmarkMove` (single-leaf CAS, no fence) | 1,584,287 | ≈630 | — |
| `BenchmarkMove_LeafAndFence` (multi-target CAS) | 1,272,100 | ≈786 | 96 |

Rough, exactly as the exit criterion asks for — one sandbox run, not a
calibrated baseline. The multi-target case reading faster is a real
artefact of the two benchmarks' different write shapes (`BenchmarkMove`
allocates a fresh subject and a fresh `loc_assignment` row every
iteration, including a `TreeAlignedFenceDelta` ancestor-walk query
Stage 5 later added; `BenchmarkMove_LeafAndFence` reuses one subject —
an `UPDATE`, not an `INSERT` — and bypasses that walk entirely via the
explicit fence keys), not evidence that adding a fence makes a move
cheaper. Re-run with `go test ./pkg/loc/ -bench BenchmarkMove
-benchtime=1s -run '^$'`.

**Reading the number against a real comparable, not in isolation.**
`bal-conservation-primitive.md`'s own comparison table records a
directly comparable figure for a same-shaped guarded write ("two
entries, two balance updates"): **~5.9–6.3k guarded ops/s, measured on
an M1**, via `cal`'s stress harness used as bal's own proxy. This
sandbox's ~630–786 ops/s is roughly 8–10x slower — almost certainly an
environment gap, not an algorithmic one: this container is
single-vCPU (`nproc`=1) writing to a virtio-backed `ext4` disk behind
a hypervisor, against the M1 figure's real dedicated cores and direct
NVMe path. The write shapes are comparable in statement count (leaf
CAS + fence CAS + one journal insert here, versus two entries plus two
balance updates there); a real algorithmic 10x between them would be
surprising and is not what this data shows — it shows two different
machines. Putting the two numbers side by side as an apples-to-apples
verdict on `loc` vs `bal`/`cal` would be the actual mistake here, not
either number on its own. A same-hardware re-run (this benchmark
alongside `cal`'s own stress harness, on the same machine) is the
follow-up that would actually settle it — not filed as its own item
yet, since T-129's own bar ("a number, however rough") is met by what's
recorded above, but worth naming as the natural next step if a
calibrated comparison is ever wanted.

**Settled, 2026-08-02, same day — the team ran both benchmarks on our
own M1 (`darwin/arm64`, `cpu: Apple M1`), `-benchtime=1s`, plus a
`-count=5` repeat pass:**

| Benchmark | M1 ns/op (mean of 5, first `LeafAndFence` sample dropped as a cold-start outlier — 19.2% spread with it vs 1.1% without) | M1 ops/sec | Sandbox ns/op | Sandbox/M1 ratio |
|---|---|---|---|---|
| `BenchmarkMove` | 150,113.6 | ≈6,662 | 1,584,287 | 10.55x |
| `BenchmarkMove_LeafAndFence` | 121,111.8 | ≈8,257 | 1,272,100 | 10.50x |

Both ratios land within 0.5% of each other despite two entirely
different write shapes — exactly what a single dominant environment
confound (this sandbox's `nproc=1`, virtio-backed `ext4`, vs the M1's
real cores and NVMe) produces, and not what an algorithmic slowdown
specific to one shape would produce. The relative ordering also
reproduces almost exactly across both machines: `BenchmarkMove` reads
1.245x slower than `LeafAndFence` on the sandbox, 1.239x slower on the
M1 (outlier dropped) — independent confirmation that the mechanistic
explanation above (fresh subject/`INSERT`/ancestor-walk vs. reused
subject/`UPDATE`/no walk) is the real reason, not a sandbox artifact.
`B/op`/`allocs/op` match closely too (sandbox 3,144B/96 allocs vs M1
3,195–3,208B/95 allocs) — same code path exercised on both machines,
not an accidental divergence.

**The actual answer this was chasing:** `loc`'s own M1 figure for
`BenchmarkMove` (≈6,662 ops/s) lands in the same band as — slightly
above — the `cal`-proxy figure `bal-conservation-primitive.md`
already records for a comparable write (5.9–6.3k ops/s, also M1).
`loc`'s guarded write path is not slower than `cal`'s in the class
that matters; the sandbox number was correct all along, just not
comparable to anything on its own. This closes the "environment vs.
algorithm" question this section opened with real evidence rather
than inference.

**Against the workload that actually motivated this benchmark**
("hundreds of small moves per minute," warehouse bin-to-bin putaway,
named earlier in this same section), even the slower sandbox number is
close to two orders of magnitude of headroom — a few ops/sec needed,
630+ available sequentially, on weaker hardware than a real deployment
is likely to run on. The M1 figures widen that margin further.

**What this number is not evidence of:** concurrent throughput.
Both benchmarks are one goroutine, sequential — WAL's single-writer-
per-file lock means real concurrent callers serialise against each
other for the write itself, so this is a ceiling on one writer, not a
claim about aggregate throughput under contention. This sandbox's own
`nproc`=1 means concurrent behaviour literally could not have been
measured here even if attempted — the same reason G-14's own race
harness (above) is stress-tagged and deferred to a real multi-core
run rather than trusted from a sandbox pass.

**A second, separate race found at build time, not anticipated by
this plan — added 2026-08-01, `RESOLVED.md` T-115.** Everything above
covers the *capacity* CAS. `Def`/`DefFence`'s own **dense-key
allocation** — assigning the internal `location_key`/`fence_key`
surrogate — is a different write path with its own race, and this
plan never named it as a second place the same class of bug could
hide. An initial read-first implementation (`SELECT MAX(key)+1` as
its own statement, before the `INSERT`) produced real `SQLITE_BUSY`
failures under concurrent load — 9 failures out of 30 concurrent `Def`
calls on the first adversarial run, not a theoretical concern. Fixed
the same way `Move` already was: write-first, `INSERT...SELECT...
RETURNING`, one statement, no read-then-write gap for a second writer
to land in. Worth stating as a general rule for whoever builds the
next primitive's own key-allocation path: **the CAS discipline
`bal-conservation-primitive.md` §6 establishes applies to *every*
write path that reads a value and then writes based on it, not only
the one this plan happened to call out in detail** — capacity
admission was this plan's obvious, named risk; key allocation was the
same risk wearing a shape unremarkable enough to not get its own
warning, and it needed the identical fix regardless.

---

## Stage 3 — Geometry doctrine, fence containment, SQL-plane pre-filter

**Goal:** §4/§5/§6 of `loc-00-design.md` — real `Circle`/`Polygon`
geometry, ray-casting containment, the R-tree/Geopoly pre-filter — and
wiring it into Stage 2's previously test-hooked fence-membership
determination so `report` resolves against real shapes.

- `Geometry` interface and the two implementations (§4a); ray-casting
  containment (§4b), simple-polygon-only, self-intersection rejected
  at write time (`XOLU-LOC020`); the axis-aligned-rectangle fast path
  (§4c).
- GeoJSON decode for `Polygon`, loc's own typed field for `circle`
  (§4d of `loc-00-design.md`, confirmed again here: never approximate
  a circle into a GeoJSON polygon on the wire).
- `CREATE VIRTUAL TABLE ... USING rtree(...)` / `USING geopoly()`,
  confirmed compiled into `modernc.org/sqlite v1.29.0` empirically in
  `loc-00-design.md` §6b — no verification step needed here, that work
  is already done and cited, not re-derived.
- Replace Stage 2's test hook: `report`'s fence-membership delta is
  now computed by the bounding-box pre-filter narrowing candidates,
  then the exact `Contains` test deciding membership — never the
  pre-filter's cached box alone, per §7b's guard-locality rule.

**Testing:** geometry edge cases from §10 of `loc-00-design.md` —
concave polygon containment correct via ray-casting (not just convex);
self-intersecting input rejected, not silently accepted; a numerics
regression test confirming no non-finite coordinate reaches storage,
now meaningfully testable since Stage 0 pinned the decode discipline
that should make this untestable-because-impossible rather than
tested-and-passing — assert the *code path*, not just the outcome
(the raw-map lint check from Stage 0's exit criteria still returning
nothing is part of this stage's own regression suite, re-run here).

**Gate:** none — `loc-00-design.md` §14 confirms no verification step
remains before this stage, the R-tree/Geopoly question having already
been closed empirically. **Exit:** geometry edge-case tests green;
`report` end-to-end resolves real fence membership through real
geometry, not the Stage 2 test hook.

---

## Stage 4 — Movement journal, retention, verification

**Goal:** §8 of `loc-00-design.md` made real — the report-vs-write
split, permanent retention, and the rebuild oracle this plan's own
Principles section names as the spine.

- `loc_journal` append-only table (`entry_id`, `subject_ref`, `kind`
  [`move`|`report`], `from_location_key`/`to_location_key` for moves,
  `entered_fence_keys`/`exited_fence_keys` for either kind, `at`).
  §8a's rule enforced at the write layer: a `report` producing no
  containment change writes nothing here, no event, no `ts` record —
  the same "decision inside the predicate" discipline as Stage 2,
  applied to whether a write happens at all rather than whether it's
  admitted.
- **The rebuild-oracle fold, concrete SQL — `bal`'s §8 pattern, loc's
  own targets:**
```sql
-- derived current assignment per subject (last move, if any):
SELECT subject_ref, to_location_key AS leaf
FROM (
  SELECT subject_ref, to_location_key,
         ROW_NUMBER() OVER (PARTITION BY subject_ref ORDER BY entry_id DESC) AS rn
  FROM loc_journal
  WHERE kind = 'move'
) WHERE rn = 1;
-- compared row-for-row against loc_assignment.

-- derived leaf occupancy, folding every move's entries and exits:
SELECT to_location_key AS location_key, COUNT(*) AS derived_count
FROM loc_journal WHERE kind = 'move' AND to_location_key = (last leaf per subject)
GROUP BY to_location_key;
-- compared against loc_capacity.count.
```
  loc has no equivalent of `bal`'s *local chain* verification (§8 of
  `bal-conservation-primitive.md` — `previous_balance + amount =
  current_balance` per entry) because a move's journal entry doesn't
  carry a running total the way a ledger entry does; loc's oracle is
  global-fold-only, and that asymmetry is worth stating rather than
  silently having a thinner verification story than bal's without
  saying so.
- Hook point for `iolu db check`, matching ts/cal/bal's own oracles —
  the hook itself, not the `iolu` command surface, which stays out of
  scope per Stage 0's own decision.

**Testing:** `derive(journal) == current` after a randomised sequence
of moves and reports, including refused attempts that must leave no
trace (mirroring the `ts` dxp adapter's own "aborted-batch-leaves-no-
trace" proof, `T-86`'s precedent, applied here to a refused CAS rather
than an aborted Pebble batch).

**Gate:** none, after Stage 2/3. **Exit:** the fold query matches
`loc_assignment` and `loc_capacity` exactly after a randomised
move/report sequence, including refusals; §8c's stated acceptance
criterion — both current assignment and current capacity counts
exactly reconstructible from empty — proven, not assumed.

---

## Stage 5 — Events, ts emission, dxp participant adoption

**Goal:** §9 of `loc-00-design.md` — the two-consumer crossing (event +
`ts`-shaped feed) and `locParticipant` as a real `dxp.Participant`,
registered as the sixth entry `loc-00-design.md` §9b now correctly
counts it as.

- Crossing delivery reuses the existing event/webhook mechanism as-is
  — `T-07` through `T-13` are still open in the register, and loc
  inherits that model's current gaps rather than getting ahead of
  them, exactly as §9a already states.
- `locParticipant` implementing the real four-method `dxp.Participant`
  (`pkg/dxp/dxp.go`) per §9b: `Reserve` evaluates the Stage 2 guard
  with live claims applied inside the tenant's lock/unlock critical
  section; `Validate` re-checks without executing (the same lazy-
  invalidation shape `bal`/`cal`/`ts` already use); `Execute` applies
  the Stage 2 CAS sequence through the coordinator-supplied
  `ParticipantStore`; `Release` is a no-op in the common case.
- `dxpParticipantRegistry` gains a `needed["loc"]` branch alongside
  the existing five; `decodeDxpParticipantParams` gains a `"loc"` case
  decoding `loc.MoveParams` — direct typed-`float64` decode per Stage
  0's pinned discipline, no raw-map intermediate, checked against the
  Stage 0 lint rule as part of this stage's own exit criteria, not
  re-derived.

**A real architectural gap this stage's own text didn't anticipate,
found by the first real end-to-end dispatch and fixed — added
2026-08-01, `CHANGELOG.md` v0.21.0.** This stage's own bullet above
treats registration as the whole story: join `dxpParticipantRegistry`,
add a decode case, done. It isn't. `bal`/`cal`/`fsm`/`entity` share
one SQL database; `loc` has its own dedicated file (Stage 0's own
`storelayout.TenantLocDir` decision). Tagging `loc`'s participant with
the same `"sql"` engine string the shared-database four use silently
collapsed a mixed transaction onto the wrong database — surfaced as
`"no such table: loc_capacity"`, not a clean refusal, precisely
because nothing caught the mismatch before dispatch tried to use it.
The fix follows `ts`'s own precedent for the identical shape of
problem rather than inventing a new one: `dispatchDxpTxnCore` gained a
third database-handle parameter (`locDB *sql.DB`, alongside the shared
`db` and `ts`'s own `pebbleDB`), constructed via `s.locStore(r)`
whenever `needed["loc"]`. The lesson for whoever stages the next
primitive with its own dedicated storage: **"registers with dxp" and
"shares dxp's storage plumbing" are separable facts, and a plan that
only states the first will hit this exact gap at first real dispatch,
not before.**

**Testing:** mirroring the `ts` dxp adapter's own precedent (`T-86`) —
standalone tests against a real store, a read-back-after-commit proof,
and a refused-CAS-leaves-no-trace proof (this stage's version of
`ts`'s aborted-batch proof); at least one genuine end-to-end
multi-participant dxp transaction touching loc alongside another
primitive, run through the real coordinator, not a hand-wired test
double.

**Gate:** none — lower risk than when `loc-00-design.md` was first
drafted, since `dispatchDxpTxn` is live code to register against now
(§9b), not a design this stage would have been first to prove.
**Exit:** `locParticipant` compiles against the real interface; a real
multi-participant dxp transaction touching loc executes end-to-end.

---

## Stage 6 — Two-identity split, API surface, client

**Goal:** `loc-01-rest-api.md`'s endpoints wired as real HTTP handlers;
§11a of `loc-00-design.md`'s two-identity split confirmed at the
handler boundary (the internal `uint32` never appears in any request
or response body — a grep-level regression check, the same shape as
Stage 0's raw-map lint check).

- All fifteen endpoints from `loc-01-rest-api.md`'s complete index,
  tenant-scoped via the standard `/api/v2/tenant/{tenant_id}/loc/...`
  pattern (`API_V2.md`), no loc-specific tenancy convention invented.
  **Fifteen is accurate for what this stage actually shipped in wave
  9 — not stale, don't update it.** `loc-01-rest-api.md`'s index has
  since grown to 21: the six added (patterns `def`/`list`/`get`/
  `delete`, fence-geometry `PATCH`, `reconcile`) are tied to §5c/§5d,
  which `loc-00-design.md` §15 correctly lists as designed but not yet
  built. This stage doesn't cover them for the same reason — a future
  stage, or an extension of this one, does if and when §5c/§5d
  actually ship.
- Error responses use the `XOLU-LOC` table from `loc-01-rest-api.md`,
  Stage 0's Go constants wired to actual handler refusals — this is
  where the table's first-pass status gets tested against real usage;
  expect some codes to need hardening the way `cal`'s did, and update
  `loc-01-rest-api.md` in place when that happens rather than letting
  the doc and the code drift apart silently.
- **Client library and iolu CLI: still out of scope**, per Stage 0's
  decision, restated here at the point it would be tempting to fold
  them in "since the surface exists now anyway." It doesn't change
  the decision; a plain HTTP surface existing is not itself a reason
  to build a typed client on top of it before something needs one.

**Testing:** every endpoint in `loc-01-rest-api.md`'s complete index
exercised at least once via a real HTTP request/response round-trip;
the two-identity grep check; the §0/`loc-01-rest-api.md` two-write-path
distinction proven at the HTTP layer specifically — a `report` that
would, if mishandled, resolve a tree leaf, asserted to leave `leaf`
`null` in the response exactly as documented.

**Gate:** none, after Stage 5. **Exit:** full HTTP surface green;
two-identity check clean; `loc-01-rest-api.md` updated in place for
any error code or field shape that changed contact with real handler
code, with the update noted in `CHANGELOG.md` the way any other
release-visible correction would be.

---

## Summary: what to do now

| Stage | Ready? | Gate | Shape |
|---|---|---|---|
| 0 Skeleton + decisions | **now** | — | mechanical + 3 pinned decisions |
| 1 Tree + placement | **now** | — | structural, TDD vs composition oracle |
| 2 Assignment + capacity + move | after Stage 1 | — | design-then-race (highest risk) |
| 3 Geometry + containment | after Stage 2 | — | TDD vs geometry oracle |
| 4 Journal + verification | after Stage 2/3 | — | build + fold-oracle invariant |
| 5 Events + dxp adapter | after Stage 4 | — | build + adapter-test precedent (`T-86`) |
| 6 API surface + client scope | after Stage 5 | — | mechanical wiring + two regression greps |

**Start Stages 0–1 immediately** — both are decision-free once Stage
0's three pinned calls are accepted, and neither touches the
concurrency risk Stage 2 carries. **Stage 2 is the one stage worth
treating with `cal`'s Stage 7 caution** — "design-then-race-test, not
TDD" — even though it isn't gated on anything, because the multi-target
atomicity rule is new relative to every other primitive's own capacity
guard and deserves the same care `cal`'s now-crossing seal got, not
less because it arrived without a formal gate attached to it.
