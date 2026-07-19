# xolu Finite State Machines

**Version:** 0.9.9
**Status:** Active (API v2)

This document describes the FSM subsystem as a concept and a contract: what a
machine is, how transitions are chosen, the three-level determinism model, the
guard exclusivity recognizer behind `determinism: loose`, and the semantics of
guard and set expressions. For the HTTP endpoint reference (definition, machine,
walk, history, bulk walk), see [API_V2.md](API_V2.md); this document is the
conceptual and semantic companion to it.

---

## 1. What an FSM is in xolu

An xolu FSM is a lifecycle made into a queryable, transactional, auditable
primitive. A *definition* declares states, transitions, and variables; a
*machine* is a running instance of a definition. A machine advances only when
something calls `walk` with an input — the engine is purely reactive. It has no
clock, performs no cross-entity reads of its own accord, and produces no effects
on other machines. Everything that changes a machine's variables happens in a
transition's `set` clause during a walk, drawing on three sources: the walk
payload, the machine's own current variables, and `NEXT VALUE FOR` sequences.

The value of expressing a lifecycle this way rather than in imperative code is
that the state evolution becomes data in the same store as the entities it
governs: every transition is recorded in an append-only history ledger, machines
can be instantiated with per-instance guard overrides, and a walk can run
atomically inside a `/commit` alongside the document write. The cost is that the
engine must be held to a higher correctness bar than ordinary application code,
because a bug in the engine is silent and global. The determinism model and the
exclusivity recognizer described below exist to keep that bar high.

---

## 2. Transitions and how one is chosen

A transition declares a source state (or several), an input symbol, a target
state, and optionally a guard, a set of variable assignments, and a Mealy
output. When a machine is walked with an input:

1. The engine collects every transition matching `(current state, input)` in
   definition order.
2. If none match, the walk is rejected with `XOLU-FSM003` (no transition for this
   input from this state — a structural rejection).
3. Among the matches, the engine evaluates guards in definition order and fires
   the first transition whose guard passes. A transition with no guard always
   passes.
4. If matches exist but every guard evaluates false, the walk is rejected with
   `XOLU-FSM004`.
5. The chosen transition advances the state, evaluates its set clauses, appends a
   history entry, and records any Mealy output — all within one transaction.

Because several transitions may share a `(state, input)` pair, a machine can
validate its input by routing to different targets: an accept edge, a
reject-invalid edge, and a reject-missing edge, all on the same input,
distinguished by their guards. Whether that branching is *safe* — whether exactly
one guard can ever fire — is the subject of the determinism model.

### 2.1 Retrieving the final result

When a machine reaches a terminal (STOP) state it stops advancing; further walks
are rejected with `XOLU-FSM005`. `GET /api/v2/fsm/machine/{id}/result` returns the
outcome in one call: `terminal` (whether the machine has stopped), the final
`state`, the final `vars`, and `final_output` (the output emitted by the
transition that reached the terminal state). For a machine that has not yet
stopped, `terminal` is false and `final_output` is omitted, while `state` and
`vars` still reflect current values — so a caller can poll this endpoint and act
once `terminal` becomes true. A terminal transition that emitted no output
reports `final_output` as an empty list, distinguishing "stopped, no output"
from "not stopped".

---

## 3. The determinism model

Every definition **must** declare a `determinism` level. The field is mandatory
and fail-closed: a definition without an explicit, valid level is rejected at
creation (`XOLU-FSM006`) and therefore can never be created or instantiated. There
is no default and no grandfathering. Requiring the choice forces the author to
reason about whether their guards are mutually exclusive, which is valuable
independent of what the engine then enforces.

The three levels form a soundness ladder.

### 3.1 `strict`

At most one transition per `(state, input)`. This is checked structurally at
creation: a definition declaring `strict` with two transitions sharing a
`(state, input)` pair is rejected, with a message naming the offending pair and
suggesting `loose` or `firstmatch`. A `strict` edge may still carry a guard (to
reject an input by failing), but there is never a *choice* of edges, so static
analysis of the machine is exact.

### 3.2 `loose`

Multiple transitions per `(state, input)` are permitted, but their guards must be
**provably mutually exclusive** — for any input, at most one guard can be true.
This is the level for validators and for any machine that branches on guards
while still promising a single, order-independent outcome. Exclusivity is
verified at creation by the recognizer described in section 4. If the recognizer
cannot prove the guards mutually exclusive, the definition is rejected with a
message explaining which guards overlap and how to fix it. A `loose` definition
that passes carries `exclusivity_verified: true` in its analysis block.

### 3.3 `firstmatch`

Multiple transitions per `(state, input)` are permitted, with no exclusivity
requirement: the first transition whose guard passes, in definition order, fires.
Transition order is therefore semantic — reordering transitions can change
behaviour. This is the escape hatch for machines whose guards are genuinely
exclusive but in a form the recognizer cannot prove, or whose author
deliberately intends order to decide.

### 3.4 A note on the name `firstmatch`

None of the three levels are non-deterministic at runtime: the engine always
picks exactly one transition and produces the same result for the same input.
The levels differ in *what guarantees that single choice* — a single edge
(`strict`), proven exclusivity (`loose`), or definition order (`firstmatch`).
`firstmatch` is named for the rule it uses rather than for a non-determinism it
does not actually have.

### 3.5 Choosing a level

| If your state… | Use |
|----------------|-----|
| has one transition per input | `strict` |
| branches on guards you can express as null partitions, distinct equalities, disjoint ranges, or var-vs-var comparisons | `loose` |
| branches on guards too complex to prove exclusive, or where order is intended to decide | `firstmatch` |

When in doubt, prefer the strictest level the machine permits: `strict` gives
exact analysis, `loose` gives a verified single-outcome guarantee, and
`firstmatch` gives neither but always works.

---

## 4. The exclusivity recognizer

`determinism: loose` is enforced by a recognizer that decides whether a set of
guards on one `(state, input)` is provably mutually exclusive. Its design has two
non-negotiable properties:

- **Sound.** It never reports "exclusive" for guards that can both be true. A
  false positive here would be a silent correctness hole: a machine that claims a
  single deterministic outcome while two guards can fire.
- **Incomplete by design.** It reports "not proven" for anything outside its
  recognized patterns, even if those guards are in fact exclusive. This is
  acceptable because `firstmatch` is always available as a fallback;
  incompleteness is a usability cost, never a correctness risk.

Proving mutual exclusivity of arbitrary boolean expressions is undecidable in
general, so the recognizer does not attempt it. Instead it reduces each guard to
a predicate over a single variable and reasons about a fixed set of patterns it
can decide cheaply and soundly.

### 4.1 Recognized guard shapes

Each guard must reduce to a predicate over exactly one variable. A predicate is a
null-state requirement (must be null, or must be present) together with either a
value region (a union of integer intervals) or a relation to another variable.
The recognized shapes are:

| Shape | Example |
|-------|---------|
| presence / absence | `X IS NULL`, `X IS NOT NULL` |
| equality to a literal | `@v = 3` |
| inequality to a literal | `@v != 3` |
| threshold | `@v < 10`, `@v >= 0` |
| bounded interval (chained AND) | `@v > 0 AND @v <= 1024` |
| complement of an interval (OR of bounds) | `@v <= 0 OR @v > 1024` |
| missing-or-invalid (null OR region) | `@v IS NULL OR @v <= 0` |
| presence-qualified condition | `X IS NOT NULL AND <any of the above on X>` |
| variable-to-variable comparison | `@a = @b`, `@a != @b` |

### 4.2 How exclusivity is decided

Two predicates on the same variable are proven disjoint when their satisfying
sets cannot overlap in either dimension:

- **Null dimension.** A predicate requiring the variable to be present is disjoint
  from one requiring it to be null.
- **Value dimension.** Two value regions are disjoint when their interval
  intersection is empty. Distinct equality literals (`@v = 1` vs `@v = 2`),
  complementary thresholds (`@v < 10` vs `@v >= 10`), and non-overlapping
  intervals are all decided this way.
- **Relational.** `@a = @b` and `@a != @b` on the same variable pair are exact
  complements and therefore disjoint, regardless of the values involved.

Predicates on *different* variables are never proven disjoint — both can hold
independently. A guard that does not reduce to a recognized predicate makes the
whole set "not proven", and the definition is rejected with guidance to
restructure or declare `firstmatch`.

### 4.3 A worked example: the packet validator

A framed-packet validator checks each field and routes to `Accepted` or
`Rejected`. Its length-check state has three transitions on the `length` input:

```
length valid:    payload.len IS NOT NULL AND payload.len > 0 AND payload.len <= 1024   -> AwaitPayload
length invalid:  payload.len IS NOT NULL AND (payload.len <= 0 OR payload.len > 1024)   -> Rejected
length missing:  payload.len IS NULL                                                     -> Rejected
```

The recognizer proves these three pairwise disjoint: "valid" requires presence
and the interval `(0, 1024]`; "invalid" requires presence and the complementary
region; "missing" requires absence. No value of `payload.len` satisfies more than
one, so the state qualifies for `loose`.

---

## 5. Smart rejection messages

When a definition is inconsistent, the error names the specific problem rather
than reporting a generic failure. The cases:

- **`strict` declared, multiple edges on an input.** The message names the
  `(state, input)` pair that is overloaded and suggests `loose` (if the guards
  can be made exclusive) or `firstmatch`.
- **`loose` declared, guards overlap.** The message names both offending guards,
  states that they can both be true, and offers `firstmatch` as the alternative
  if transition order is intended to decide.
- **`loose` declared, a guard is not in a recognized pattern.** The message names
  the unrecognized guard and points to the recognized forms (null partition,
  distinct equality, interval, var-vs-var) or `firstmatch`.
- **`loose` declared, an edge in a multi-edge group has no guard.** An unguarded
  edge always fires and so cannot be exclusive; the message explains this and
  asks for a guard or `firstmatch`.

---

## 6. Guard and set expression semantics

Guards and set clauses are T-SQL expressions evaluated by the same engine xolu
uses for OQL, drawing on the [tsqlparser](https://github.com/ha1tch/tsqlparser)
library. Because they are T-SQL, several semantics are inherited rather than
designed, and authors should be aware of them.

### 6.1 What a guard or set can read

- **Machine variables** as `@name`.
- **Walk payload fields** as `payload.field` (top-level scalar fields only).
  Both guards and set clauses can read the payload; a set clause may capture an
  incoming value into a variable, for example `set: { "@expected": "payload.len" }`.
- **Sequences** in a set clause via `NEXT VALUE FOR name`, incremented atomically
  within the walk transaction.

### 6.2 Equality and operators

Equality is `=` (not `==`); inequality is `!=` or `<>`. Boolean connectives are
`AND`, `OR`, and `NOT` (not `&&` / `||`). A guard of the form `@var = value` is a
comparison; xolu reconstructs it correctly even though T-SQL would otherwise parse
`@var = value` in a column position as a variable assignment.

### 6.3 NULL handling

A missing payload field or unset variable evaluates as NULL. Comparisons against
NULL yield UNKNOWN, which a guard treats as false. The reliable way to test for
presence is `IS NULL` / `IS NOT NULL`, not a comparison against an absent value.
In a validator, prefer an explicit `field IS NULL` reject edge over relying on a
comparison silently failing.

### 6.4 Type coercion

Numeric payload values (JSON numbers) and integer variables compare numerically.
A genuinely string-typed value compared against a number is compared lexically,
not numerically — `"10" > 5` is false because the comparison is on strings. This
is T-SQL type-mismatch behaviour, not an xolu choice; compare like types, and do
not assume a string field that looks numeric will compare as a number.

---

## 7. Transition pre-queries

A definition may associate an OQL `SELECT` with an input symbol via the
`input_queries` field — a map from input to query text. Before a walk on that
input, the server runs the query and binds its result row into the guard and set
evaluator under the `query.` prefix, so guards and set clauses read
`query.<column>` exactly as they read `payload.<field>`. The purpose is to save
the caller a round-trip: data the application would otherwise have fetched and
passed in the payload is fetched by the engine instead.

```json
{
  "name": "OnboardingGate",
  "initial": "AwaitingKYC",
  "determinism": "firstmatch",
  "input_queries": {
    "check": "SELECT status FROM kyc_results WHERE user_id = 42"
  },
  "transitions": [
    { "from": "AwaitingKYC", "input": "check", "to": "Approved",
      "guard": "query.status = 'approved'" },
    { "from": "AwaitingKYC", "input": "check", "to": "Denied",
      "guard": "query.status != 'approved'" }
  ]
}
```

### 7.1 Why keyed by input, not by transition

A pre-query exists to feed the guards that *select* a transition, so it must run
before transition selection — which means it cannot belong to any one
transition, since the engine does not yet know which will fire. The map is keyed
by input: all candidate transitions for that input share one query, run once,
before guard evaluation chooses among them. This also avoids duplicating an
identical query across the disambiguated edges of a single input.

### 7.2 Execution and result semantics

The query runs through the standard OQL path, read-only, **before the walk
transaction opens**. The engine forces `TOP 1` onto the query (overriding any
`TOP` the author wrote) so that at most one row is retrieved regardless of how
many match — only the first row is used, and fetching more would be wasteful.
The author still controls *which* row via `ORDER BY`; `TOP 1` bounds the cost,
not the choice.

Result binding (semantics chosen deliberately, not inherited from any SQL
dialect):

- **0 rows** — nothing is bound; `query.<col>` references resolve to NULL, so a
  guard comparing against them is false. Missing query data never silently
  matches.
- **1 row** — each scalar column is bound as `query.<column>`.
- **N rows** — the first row is bound. A query that can match several rows should
  use `ORDER BY` to make "the first" deterministic.

A query that fails to parse or execute fails the walk with a `400` error rather
than being silently ignored.

### 7.3 Limitations

- **Standalone `/walk` only.** A walk embedded in a `/commit` (`fsm_walk`) does
  not run pre-queries; the commit path is for cases where the caller already
  holds the data and passes it directly.
- **Read-just-before-walk, not atomic.** Because the query runs before the walk
  transaction, its result reflects committed state immediately before the walk,
  not a snapshot atomic with the state advance. There is a small window in which
  the queried data could change between the read and the state advance. For
  step-gated workflows (where the queried facts do not flip moment to moment)
  this is acceptable; if true read-inside-transaction atomicity is required, the
  data should be passed in the payload instead.
- **Query-gated transitions cannot be `loose`.** A guard that reads `query.`
  values depends on state the exclusivity recognizer cannot see at definition
  time, so a machine with such guards must declare `firstmatch`.

---

## 8. Error codes

The FSM subsystem uses the `XOLU-FSM` code family. The codes relevant to
definition validity and walking:

| Code | Meaning |
|------|---------|
| `XOLU-FSM002` | machine not found |
| `XOLU-FSM003` | no transition for the input from the current state (structural) |
| `XOLU-FSM004` | a transition matched but its guard (or all candidate guards) evaluated false |
| `XOLU-FSM005` | the machine is already in a terminal state |
| `XOLU-FSM006` | definition invalid (includes missing/invalid determinism, `strict` multi-edge, and `loose` non-exclusivity) |
| `XOLU-FSM008` | a walk embedded in a `/commit` failed; the whole commit rolled back |
| `XOLU-FSM009` | a non-terminal state cannot reach any terminal state |
| `XOLU-FSM011` | a guard or set expression failed to parse or evaluate |

The `XOLU-FSM` table is intentionally non-contiguous; absent numbers are not
renumbered.

---

## 9. Pending and not yet implemented

The FSM core — defining machines, instantiating them, walking with guards and
sets, the determinism model and exclusivity recognizer, pre-queries, atomic
walk-in-commit, history, and result retrieval — is complete. The following are
known-incomplete, listed with their current state so callers know what they can
rely on today. The full staged roadmap is in
[API_V2_DEVELOPMENT_PLAN.md](API_V2_DEVELOPMENT_PLAN.md); this section is the
user-facing summary of what is not yet usable.

**Linked states / bundle composition** (`linked_states`). Child definitions are
resolved and snapshotted at machine creation — a machine declaring linked states
is created with each child's spec captured — but the walk does *not* yet compose
them: entering a linked state does not enter or run the child machine. The data
model is present; the execution is not. Treat `linked_states` as reserved.
(Dev-plan stage S12.)

**Output-triggered event dispatch** (`fsm.output`). A transition's Mealy output
is recorded in history and returned by the walk and result endpoints, but it does
not yet dispatch event defs. An FSM can report that it emitted an
output, but nothing downstream reacts to it automatically. To act on outputs
today, poll `GET /fsm/machine/{id}/result` or read history. (Dev-plan stage S9.)

**`@FSM()` in OQL.** Walking a machine from inside an OQL query is not
implemented. Walks go through the `/walk` endpoint or the `/commit` `fsm_walk`
field. (Dev-plan stage S14.)

**Inline entity creation in machine creation.** The `entity` field on machine
creation is rejected with a clear error; bind to an existing entity with `ref`
instead. (Dev-plan stage S19.)

**FSM garbage collection** (`gc` with `stalled_after` / `dead_after`). The GC
policy block is parsed and stored on a definition, but no sweep yet collects
stalled or dead machines based on it. The configuration is inert. (Dev-plan
stage S24.)

**Synchronous output actions.** Even once output-triggered dispatch lands, Part 1
executes triggered actions asynchronously regardless of any `sync` request;
true in-transaction synchronous execution is deferred. (Dev-plan, Part 2.)

---

## 10. Relationship to other documents

- [API_V2.md](API_V2.md) — the HTTP endpoint reference for definitions, machines,
  walking, history, and bulk walk, plus the rationale for FSMs as a first-class
  subsystem.
- [COMMIT_ENDPOINT.md](COMMIT_ENDPOINT.md) — the `/commit` endpoint, including the
  `fsm_walk` field that runs a walk atomically with a document write.
- [OQL_API.md](OQL_API.md) — the query language that shares the T-SQL expression
  engine used by guards and set clauses.
