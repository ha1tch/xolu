# Client Stage 6 — Sized Task List (M4a output)

Updated: 2026-07-18

Output of the M4a sizing spike (`docs/MOLU_READINESS_PLAN.md`). Executed at
v0.16.0 as written, with one addition recorded in the tracker: T-33
filed from the integration-suite work (FTS double-gate investigation).

## Spike findings

- **Coverage.** The server exposes ~180 routes; the client covers 43
  methods spanning entity CRUD (`Create/Get/Update/Patch/Delete/List/
  Save`), `Commit`, `Search`, `OQL`, `Sulpher`, graph basics
  (`Neighbors/Query/ShortestPath`), schema (get + list), gen/seq (full),
  FSM (full), event defs (read), cal (full), and health/availability.
  Every capability molu Parts 2–3 assume is covered as of v0.15.3.
- **Uncovered families** are whole subsystems, not scattered holes:
  timeseries (`tl`/`timelines`, 33 routes), blob (10), meta (5),
  admin (9), dynconfig, stats, export, async-query polling, and the
  deep graph analytics (`pathExists`, `commonNeighbors`, per-node
  inspection, `edges`, admin rebuild/verify). None appears in molu's
  assumption set.
- **`map[string]any` census (non-test):** 23 sites. Nearly all are
  legitimately dynamic — `Entity.Data` (schemaless by design), FSM
  `Vars`/`Payload`, OQL/Sulpher result rows, error `Details`. The
  "structure exists" criterion catches almost nothing; no bulk typing
  pass is warranted.
- **Godoc gaps:** exactly one (`ListMachineDefs`).
- **Known defect queued:** T-32 (`Sequence` type wire mismatch).

## Decisions taken by this spike

- **Scope declaration (the audit's real output).** The client's declared
  v0.16.0 surface is the data-plane + semantic-map + FSM + cal set it
  already covers. Timeseries, blob, meta, admin, dynconfig, stats,
  export, async polling, and deep graph analytics are **declared out of
  scope** — documented, not accidental — and revisitable when a consumer
  needs them. Rationale: molu needs none of them, and stability declared
  over a surface nobody exercises is a liability, not an asset.
- **D-iii / T-26: YES, minimal form, folded into M4b.** A build-tagged
  (`//go:build integration`) suite booting an in-process xolu server and
  exercising every declared-scope method's happy path. The T-32
  discovery is the concrete argument: the mock-based tests passed for
  eighteen releases while asserting the same wrong shape they
  constructed. The minimal form (happy paths only, no exhaustive error
  matrices) keeps it ~1–1.5 days instead of T-26's original 2–3.

## M4b task list

| # | Task | Estimate |
|---|------|----------|
| 1 | T-32: fix `Sequence` type (`Step`→`IncrementBy`, add `Cycle`, drop `CreatedAt`), migrate `GetSequence` tests, breaking-change note | 1 h |
| 2 | Godoc: `ListMachineDefs` | 10 min |
| 3 | Scope declaration: package doc section naming the declared surface and the explicit exclusions | 1 h |
| 4 | T-26 minimal integration suite, build-tagged, every declared-scope method happy-path against an in-process server | 1–1.5 d |
| 5 | Stability pass: version-tie note, `release.sh --with-integration` hook, close T-02 and T-26/T-32 per procedure | 0.5 d |

**Total: ~2–2.5 days.** Release: v0.16.0, client declared stable for
molu consumption. No new endpoint work — the audit's conclusion is that
coverage is already complete for the declared scope.
