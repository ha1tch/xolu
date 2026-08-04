# OQL primitive queries: @FSM/@TS/@CAL/@BAL — proposal

Updated: 2026-08-02
Status: proposal — sequencing agreed, dispatch design sketched, several
open questions unresolved. No implementation started.

## Context

`/ts`, `/cal`, and `/bal` have no query language. Entities and the graph
are queryable through OQL (T-SQL subset over SQLite push-down); the
Pebble-backed and mixed-storage primitives are not queryable at all —
an operator or application wanting to ask "which fsm machines are
stuck in state X" or "what's the ts metering total for tenant Y last
week" has no path except writing Go directly against each primitive's
package.

Proposed: extend OQL with `@`-prefixed pseudo-identifiers naming a
primitive, in two syntactic shapes, dispatched to primitive-specific
query providers rather than SQL push-down.

## Validated: the syntax already works, unmodified

Before any design work, the two proposed shapes were checked directly
against the real `tsqlparser` v0.6.1 library (not inferred from
reading grammar code) — both parse with **zero parser changes**:

**Shape 1 — pseudo-table source**, for `FROM`:

```sql
SELECT * FROM @FSM(1) AS x
SELECT x.state FROM @FSM(machine_id) AS x WHERE x.state = 'active'
```

Parses as `*ast.TableValuedFunction` (`Function *ast.QualifiedIdentifier`,
`Arguments []Expression`, `Alias *ast.Identifier`) — tsqlparser's own
existing node type for `dbo.func(...)`-style table-valued function
calls in `FROM` position, reused here unmodified: nobody designed
"an `@`-prefixed name as a table-valued function," it just satisfies
the same grammar production a real TVF call would. `extractEntityFromSelect`
(`pkg/oql/executor.go`) currently does a bare
`s.From.Tables[0].(*ast.TableName)` type assertion — a
`TableValuedFunction`-shaped FROM item fails this silently today and
falls through to an empty entity name. The door is open; nothing
walks through it yet.

A third shape belongs here structurally but not syntactically — a bare
scalar call with no `FROM` at all:

```sql
SELECT @TS('metering', from_time, to_time) AS pts
```

This parses as `*ast.FunctionCall` (`Function` an `*ast.Variable`,
`Arguments []Expression`) in the SELECT-list's own expression position,
not as a FROM-clause table source — `@`-prefixed tokens lex as
`token.VARIABLE`, and `LPAREN` is registered as a fully generic infix
"call" parselet applying to *any* preceding expression, so
"`@variable(...)`" falls out of the parser's generic Pratt architecture
the same way the FROM-position shape does, just landing as a different
node type because it's in a different grammar position.

**Shape 2 — method call**, for `WHERE`/scalar position:

```sql
SELECT * FROM x WHERE @FSM.walk() IS NOT NULL
UPDATE x SET y = 1 WHERE @FSM.walk() IS NOT NULL
SELECT @FSM.state(1)
```

Parses as a **distinct, dedicated node type**: `*ast.MethodCallExpression`
(`Object`, `MethodName string`, `Arguments []Expression`) — not
`FunctionCall`. tsqlparser already separates "table source" from
"method call" at the grammar level; the two shapes are structurally
distinguishable by AST node type alone, no ambiguity to resolve.
(The doc comment on `MethodCallExpression` lists T-SQL's native XML
method names — `value`, `nodes`, `query`, `exist`, `modify` — as the
feature's original purpose; `MethodName` is a bare string, so it
accepts `walk` or any other name without modification.)

One wrinkle found, now understood: `SELECT @FSM.state(1).name`
round-trips through the pretty-printer as `SELECT name`, silently
dropping the qualifier — and this is structural, not a `String()`
display artifact. `Parse()` itself returns a bare `*ast.QualifiedIdentifier`
for `name` alone; the `@FSM.state(1)` call is gone from the AST before
printing ever happens. Filed as T-137 (`docs/TRACKING.md`) rather than
chased here, since no wave-8 item's own filed scope needs chained
access yet — chained access beyond one level needs its own check
before anything is built on it. The user's own example (`@FSM.walk()`,
no further chaining) parses and round-trips correctly.

## Architectural fit: PushNone already exists

`PushNone` ("stay in Go") is already a first-class `PushDecision` in
`pkg/oql/planner.go` — OQL already has a row-by-row, Go-evaluated
fallback path for predicates it can't push to SQLite. A
`MethodCallExpression` predicate is architecturally the same shape as
whatever `PushNone` already does: evaluate per row, in Go, not in SQL.
This is a real, existing pattern to extend, not a new architecture to
invent from scratch.

## Open questions (unresolved, not decided here)

- **Correlation.** `WHERE @FSM.walk() IS NOT NULL` in `UPDATE x SET y
  = 1 WHERE ...` — does `@FSM.walk()` need access to `x`'s current row
  (e.g. an implicit `machine_id` column binding), and if so, by what
  convention? Or is a bare `@FSM.walk()` always uncorrelated, with
  correlation only available via explicit arguments
  (`@FSM.walk(x.machine_id)`)? This needs a ruling before `@FSM.walk()`
  can mean anything precise.
- **Per-primitive query shape.** `@FSM(...)` as a pseudo-table
  presumably yields rows (state, entity_ref, ...) — natural, close to
  fsm's existing SQL shape. `@TS(...)` in `SELECT @TS('metering',
  from, to)` looks scalar (an aggregate?) rather than row-producing —
  the two primitives may want genuinely different call shapes, not one
  uniform contract.
- **Chained/nested calls** (`@FSM.state(1).name`) — status unknown,
  see the wrinkle above.
- **Performance signalling.** A `PushNone`-forced query already costs
  more than a pushed one; a `@TS()`/`@CAL()` call inside a WHERE
  clause potentially costs a full Pebble/store round-trip per outer
  row. Needs the same complexity-estimation treatment
  `EstimateComplexity` already gives ordinary push-down decisions, or
  an explicit warning path, before this ships without a footgun.

## Proposed sequencing (difficulty-ordered, not importance-ordered)

The four primitives have genuinely different storage shapes; sequence
by how much new translation logic each needs, not by which matters
most.

1. **Dispatch infrastructure.** Recognise a `TableValuedFunction`/
   `MethodCallExpression` FROM/WHERE item, extract name + args, route
   to a new provider interface. Pure plumbing — no primitive-specific
   logic. Everything else depends on this.
2. **`@FSM()`.** fsm is fully SQL-backed already — closest to what OQL
   already knows how to do. Proves the dispatch mechanism end to end
   with the least new surface area.
3. **`@BAL()`.** SQL-authoritative with a Pebble satellite (rollup);
   moderate. bal's query shape (accounts, journal, balances) is
   well-understood after this session's work.
4. **`@CAL()`.** Same SQL+Pebble shape as bal, but occupancy/
   availability queries (spans, not point lookups) are a genuinely
   different query pattern.
5. **`@TS()`** last. Fully Pebble, no SQL fallback at all — the
   hardest, most novel translation problem.

`iolu`/`xia` split (renaming the current admin CLI to `xia`,
repurposing `iolu` around querying the substrate) is a separable
track — 43 files reference `iolu` today, mostly mechanical (Makefile
already conceptually separates `ADMIN_BINARY`/`ADMIN_PATH`; the ask is
renaming, not restructuring). The renamed `iolu` keeps a normal,
scriptable CLI surface — `iolu query --oql "..."` stays a one-shot
command like any other subcommand, T-70's own scoping — with `iolu
repl` as the *additional*, invoked-when-wanted mode that behaves like
`iaul`. "More like iaul" describes the REPL subcommand specifically,
not a change to the tool's fundamental nature; `iolu` does not become
interactive-only.

**Default invocation, refined against a named concrete precedent
(direct instruction).** The split follows `mysqladmin`/`mysql`
precisely: `xia` is the `mysqladmin` analog (administrative
operations, no interactive mode expected); the renamed `iolu` is the
`mysql` analog. `mysql`'s own behaviour is more precise than "bare
args means REPL" — it dispatches on whether **stdin is a TTY**, not
argument count alone:

- Interactive terminal stdin → REPL (prompt, readline, history).
- Piped or redirected stdin (`iolu < script.oql`, `cat queries.oql |
  iolu`) → read statements from stdin and execute as a batch — no
  prompt, no readline, exits when stdin is exhausted. Distinct from
  both the REPL and from `iolu query --oql "..."` (a single statement
  via flag) — `mysql -e "..."` vs `mysql < file` are both
  non-interactive but different from each other too.

`iolu <subcommand> ...` still runs as the ordinary scriptable CLI
regardless of stdin's TTY status — the TTY check only governs the
fully-bare invocation's dispatch between REPL and batch-from-stdin.
Not verified against a live `mysql` binary (unavailable in this
sandbox) — recorded as the standard, documented `mysql`/`psql`/
`sqlite3` convention, not something tested live in this session.
Applies to the renamed `iolu` specifically, per the transitional-state
caveat above.

Does not block and is not blocked by any of the above.

## Non-goals (for now)

- Resolving the open questions above — each gets settled when its
  item is actually worked, not speculatively here.
- `iaul`'s full interactive REPL shape for `iolu repl` — tracked
  separately under T-70 (query + REPL), which predates this proposal
  and already scopes both the one-shot CLI command and the REPL as
  distinct subcommands, not REPL as `iolu`'s only mode.
