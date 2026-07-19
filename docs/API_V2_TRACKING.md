# API v2 — Development Tracking

This file tracks the **actual** state of API v2 development: what has shipped,
what is in progress, and what remains. It is the single source of truth for
status.

Planning lives in [API_V2_DEVELOPMENT_PLAN.md](API_V2_DEVELOPMENT_PLAN.md);
this file records execution. The two are deliberately separate: the plan
captures intent, this table captures what actually happened — and the two are
allowed to diverge. Where execution departed from the plan (responding to
evidence found along the way), the divergence is noted in the row.

Each stage links back to its detailed section in the plan.

**Status legend:** ✓ done · ◐ partial · ☐ not started · ✗ dropped

## Part 1 — Draft towards xolu 1.0

| Stage | Summary | Status | Shipped | Notes |
|-------|---------|--------|---------|-------|
| [S1](API_V2_DEVELOPMENT_PLAN.md#s1--flag-and-routing-scaffold) | Flag and routing scaffold | ✓ done | patch002 | |
| [S2](API_V2_DEVELOPMENT_PLAN.md#s2--pkggc) | `pkg/gc` subsystem | ✓ done | patch003 | |
| [S3](API_V2_DEVELOPMENT_PLAN.md#s3--apiv2meta) | `/api/v2/meta` | ✓ done | patch004 | |
| [S4](API_V2_DEVELOPMENT_PLAN.md#s4--stateless-generators) | Stateless generators (uuid_v4/v7, cuid, ulid) | ✓ done | patch005 | |
| [S5](API_V2_DEVELOPMENT_PLAN.md#s5--sequences) | Sequences (`NEXT VALUE FOR`, `@SEQ`) | ✓ done | patch006 | |
| [S6](API_V2_DEVELOPMENT_PLAN.md#s6--pkgfsmeval) | `pkg/fsm/eval` expression engine | ✓ done | patch007 | |
| [S7](API_V2_DEVELOPMENT_PLAN.md#s7--fsm-definitions-and-machines) | FSM definitions and machines | ✓ done | patch008 | Extended later: mandatory three-level determinism model and exclusivity recognizer (patch011), transition pre-queries (patch012), `/result` endpoint (patch012). |
| [S8](API_V2_DEVELOPMENT_PLAN.md#s8--fsm-walk) | FSM walk | ✓ done | patch009 | |
| [S9](API_V2_DEVELOPMENT_PLAN.md#s9--event-defs-basic) | Event defs (basic) — `fsm.output` dispatch, webhook/oql actions, delivery log | ✓ done | patch015 | Eight management endpoints; `entity.created/updated/deleted` + `fsm.output` event types (post-commit, async); `webhook` + `oql` actions; template substitution incl. `{{gen:name}}`; delivery log; `XOLU-EV001/002/003`. **Async-only in Part 1** (sync downgraded with `X-Executed-As: async`); at-most-once, single attempt. Deferred to S13–S17: sync execution, retry/backoff, dead-letter/replay, `sulpher`/`fsm.walk` actions, and the other event types. |
| [S10](API_V2_DEVELOPMENT_PLAN.md#s10--remaining-stateful-generators) | Stateful generators (token, nanoid, random_int, timestamp, pick, slug) + `@GEN('name')` dispatch | ✓ done | patch013 | **Deviated from plan:** `snowflake` was dropped (see [Deviations](#deviations-from-the-plan)); generators invoked as `@GEN('name')` (the `@`-prefixed extension convention), not bare scalars. `pick` round-robin cursor is in-memory (resets on restart); `pick` weighted mode, `pick` set mutation, and `slug` custom vocabularies deferred to S21. |
| [S11](API_V2_DEVELOPMENT_PLAN.md#s11--error-codes-tests-documentation-release-prep) | Error codes, tests, documentation, release prep | ✓ done | — | Consolidated Part-1 release pass complete: canonical end-to-end integration test (`TestS11_CanonicalPipeline` — commit→walk→guard→output→event def→webhook); `CHANGELOG.md` Unreleased/towards-1.0 entry; `KNOWN_ISSUES.md` Part-1 event-model limits; error-code families confirmed (`XOLU-EV001/002/003` live, deferred codes recorded in `EVENT_PENDING.md`); docs reconciled (`EVENT_MODEL.md`, `API_V2.md`, `jsonplate.md`). Full gate green (22 packages). Version set to **0.10.0** (event model landing). 1.0.0 deliberately deferred — coverage, benchmarks, and broader consistency passes remain. |

## Part 2 — Functionally correct towards xolu 2.0

| Stage | Summary | Status | Notes |
|-------|---------|--------|-------|
| [S12](API_V2_DEVELOPMENT_PLAN.md#s12--bundle-composition) | Bundle composition (linked states) | ◐ partial | Data model present: children resolved and snapshotted at machine creation. Walk does **not** compose them yet. |
| [S13](API_V2_DEVELOPMENT_PLAN.md#s13--sync-event-actions) | Sync event actions | ☐ not started | Part 1 executes all actions async regardless of `execution` field. |
| [S14](API_V2_DEVELOPMENT_PLAN.md#s14--fsmwalk-event-action-and-fsm-oql-function) | `fsm.walk` event action + `@FSM()` OQL function | ☐ not started | |
| [S15](API_V2_DEVELOPMENT_PLAN.md#s15--additional-event-trigger-sources) | Additional event trigger sources | ☐ not started | |
| [S16](API_V2_DEVELOPMENT_PLAN.md#s16--dead-letter-and-replay) | Dead-letter and replay | ☐ not started | |
| [S17](API_V2_DEVELOPMENT_PLAN.md#s17--retry-and-backoff) | Retry and backoff | ☐ not started | Part 1 is single delivery attempt. |
| [S18](API_V2_DEVELOPMENT_PLAN.md#s18--event-test-invocation) | Event test invocation | ☐ not started | |
| [S19](API_V2_DEVELOPMENT_PLAN.md#s19--inline-entity-creation-in-machine) | Inline entity creation in machine | ◐ stubbed | Stubbed with a clear error; bind with `ref` instead. |
| [S20](API_V2_DEVELOPMENT_PLAN.md#s20--apiv1commit--fsm-walk-hardening) | `/api/v1/commit` + FSM walk hardening | ☐ not started | |
| [S21](API_V2_DEVELOPMENT_PLAN.md#s21--generator-hardening) | Generator hardening | ☐ not started | `pick` round-robin cursor persistence, `pick` weighted mode, `slug` custom vocabularies, `@GEN` in the tsqlruntime evaluator. |
| [S22](API_V2_DEVELOPMENT_PLAN.md#s22--seq-session-semantics) | `@SEQ()` session semantics | ◐ partial | `@SEQ` works; full session semantics pending. |
| [S23](API_V2_DEVELOPMENT_PLAN.md#s23--full-error-code-coverage) | Full error code coverage | ☐ not started | |
| [S24](API_V2_DEVELOPMENT_PLAN.md#s24--fsm-gc) | FSM GC | ◐ partial | `gc`/`stalled_after`/`dead_after` config parsed and stored; no sweep yet. |
| [S25](API_V2_DEVELOPMENT_PLAN.md#s25--api-surface-stabilisation) | API surface stabilisation | ☐ not started | |
| [S26](API_V2_DEVELOPMENT_PLAN.md#s26--documentation-tests-release) | Documentation, tests, release | ☐ not started | |

## Deviations from the plan

These are places where execution departed from the original plan in response to
evidence found during development. They are recorded here rather than silently
reconciled into the plan.

- **`snowflake` generator dropped (S10).** The plan listed `snowflake` among the
  stateful generators. Its Part 1 in-memory form is strictly worse than the
  already-shipped `uuid_v7` — it carries the complexity of distributed ID
  generation (worker IDs, clock-drift handling, bit-layout) without the
  distributed payoff, since the per-worker counter is in memory. Removed from
  the specification rather than shipping the weak form.
- **Generator invocation convention (S10).** Generators are invoked as
  `@GEN('name')` — the `@`-prefixed xolu T-SQL extension convention, consistent
  with `@SEQ` and the planned `@FSM` — rather than bare type-named scalars. The
  spec's `GEN(name)` references were corrected to `@GEN('name')`.
- **FSM determinism model (S7, post-plan).** A mandatory three-level determinism
  model (`strict`/`loose`/`firstmatch`) with a sound-but-incomplete exclusivity
  recognizer was added beyond the original S7 scope, after the packet-validator
  work showed guard-disambiguated transitions needed a safety guarantee.
