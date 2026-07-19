# Proposal — Normalisation of Tracking Documents

> **Reconciliation status (2026-07-18): EXECUTED.** The migration shipped as
> proposed, with decisions D1=TRACKING.md, D2=verbatim, D3=archive/,
> D5=design-only proposals. D4 resolved as: the Sulpher roadmap stays a
> living benchmark-gated roadmap under the date-only header convention, with
> no umbrella register item — its open items are "implement when needed"
> design intent, not filed work. `TECHNICAL_DEBT.md` (referenced below) was
> the register's short-lived name; it is superseded by `docs/TRACKING.md`.
> The practices in §5 were adopted into the working agreement and mirrored
> in `docs/TRACKING_PRACTICES.md`. Content below is the proposal as written.

Status: PROPOSED (not yet executed)
Prepared: 2026-07-18
Baseline: xolu v0.15.0

---

## 1. Diagnosis

The repository currently tracks work across at least seven documents with
overlapping jurisdiction, four ID namespaces, and no documented rule for
where an item lives or where it goes when it dies.

### 1.1 Inventory at v0.15.0

| Document | Lines | Character | Health |
|----------|-------|-----------|--------|
| `CHANGELOG.md` | 8,700 | Append-only history of what shipped | Healthy; leave alone |
| `docs/KNOWN_ISSUES.md` | 1,215 | Mixed: intentional limits, open debt (TD-nnn), **fully resolved** defects (D-001–D-009, ~780 lines), resolved layout items, invariant boundaries | ~65% resolved prose |
| `docs/TECHNICAL_DEBT.md` | 359 | Task register T-01–T-31, open and closed interleaved | ~40% closed prose |
| `docs/API_V2_TRACKING.md` | 72 | Stage-status table S1–S26, plan/execution split, deviations section | **The model citizen** |
| `docs/API_V2_DEVELOPMENT_PLAN.md` | 849 | The plan half of the pair above | Live (Part 2 pending) |
| `docs/S9_WORK_STRATEGY.md` | 182 | Per-stage execution strategy | S9 shipped (patch015); doc is now history sitting in the live namespace |
| `docs/SULPHER_OPTIMISATION_ROADMAP.md` | ~200 | Roadmap with [done] markers | Header pinned at 0.9.9; ambiguous whether stale or provenance |
| `docs/proposals/post-0.11.0-work-plan.md` | 193 | Work plan, largely shipped, status banner | History, correctly bannered |
| `docs/proposals/*` (cal docs, session notes) | — | Design intent with reconciliation banners (T-17 pattern) | Healthy convention, applied ad hoc |

### 1.2 The specific failures

1. **No single answer to "what is open right now?"** Answering requires
   reading three documents and mentally subtracting the closed parts.
2. **Closed material accumulates in place.** D-001–D-009 are all remediated,
   yet their full defect analyses dominate KNOWN_ISSUES. Seven of thirty-one
   T-items are closed but retain full prose in the register.
3. **Four ID namespaces with overlap.** D-nnn (defects), TD-nnn (debt),
   T-nn (tasks), S-nn (API v2 stages) — and TD-001/TD-002 are duplicated
   verbatim as T-03/T-04. S13–S17 substantially overlap T-07/T-09/T-10.
4. **No header discipline.** The KNOWN_ISSUES header sat at 0.13.2 through
   two minor releases; the Sulpher roadmap sits at 0.9.9 with no way to tell
   whether that is staleness or provenance.
5. **The good patterns are local, not general.** The plan/tracking split and
   deviations-recorded-not-reconciled rule (API_V2_TRACKING) and the
   reconciliation-banner rule for proposals (T-17) are exactly right — but
   they exist as habits of specific documents, not as practice.

## 2. Principles

- **One live register.** A single document answers "what is open."
- **History is append-only and elsewhere.** Closed items move out of the
  live path, verbatim, with their closure records. CHANGELOG says *what
  shipped*; the historical record says *what was wrong and how it was
  resolved*. They reference, not duplicate, each other.
- **Intentional limits are documentation, not debt.** Design boundaries
  (event model Part 1, time-handling invariants) describe the product as it
  is meant to be; they never belong in a debt register.
- **Do not renumber.** Existing IDs (D-nnn, TD-nnn, T-nn, S-nn) appear in
  the changelog, commit messages, and session transcripts. All survive;
  retired namespaces get pointers, not rewrites.
- **Generalise what already works in-house.** The normalisation adopts the
  API_V2_TRACKING and T-17 patterns repo-wide rather than importing an
  external methodology.

## 3. Target structure

```
CHANGELOG.md                      what shipped, when (unchanged)
docs/
  TRACKING.md                     THE live register: open items only
  RESOLVED.md                     append-only historical record of closures
  KNOWN_ISSUES.md                 narrowed: intentional limits, invariant
                                  boundaries, recorded decisions only
  TRACKING_PRACTICES.md           the rules (short; see §5)
  API_V2_DEVELOPMENT_PLAN.md      unchanged (plan)
  API_V2_TRACKING.md              unchanged (stage execution)
  archive/                        shipped strategies and work plans
    S9_WORK_STRATEGY.md           (moved, banner added)
  proposals/                      design intent, bannered per T-17
```

### 3.1 `docs/TRACKING.md`

Successor to TECHNICAL_DEBT.md. Open items only. Format:

- A status table at the top (ID, one-line summary, priority, status,
  blockers) using the API_V2_TRACKING legend: ✓ done · ◐ partial ·
  ☐ not started · ✗ dropped — with ✓ items removed to RESOLVED at the
  next release rather than accumulating.
- Detail sections below the table, one per item, as today.
- Unified namespace: T-nn, continuing from T-31. TD-001/TD-002 are
  formally retired in favour of their existing mirrors T-03/T-04 (a
  one-line pointer each in RESOLVED records the retirement).
- Scope is wider than "debt": gaps, hardening, tooling, and features
  filed as prerequisites all qualify — T-18 already proved the register
  holds features. The name should say so; hence TRACKING, not
  TECHNICAL_DEBT.

### 3.2 `docs/RESOLVED.md`

Append-only, newest first. Each entry is the item's full text as it stood
at closure — heading, analysis, resolution prose, closure version and
date. Verbatim, because the resolution detail has lasting value (the
D-005 fix shape, the T-15 stress numbers) and disk is free; it simply
does not belong in the "what is wrong now" reading path.

Initial population, in closure order: D-001–D-009 (with their
cross-cutting notes), the two resolved storage-layout items, the resolved
cal schema-gap items, T-01, T-15, T-17, T-18, T-29, T-30, T-31.

### 3.3 `docs/KNOWN_ISSUES.md` (narrowed)

Keeps its name — the changelog and prior docs reference it by name, and
renames churn links for no gain. Scope narrows to what is *true of the
product now by design*: the Part 1 event-model boundaries, time-handling
invariant boundaries, recorded decisions (table conventions, latch-source
granularity). Header states the narrowed scope and points to TRACKING
(open work) and RESOLVED (history). Open defects, when they exist, get a
one-line index here pointing into TRACKING; currently that index is empty.

### 3.4 API v2 pair — unchanged, with a demarcation rule

The S-table tracks *stage delivery*; the T-register tracks *debt and
gaps*. Where both apply (S13–S17 vs T-07/T-09/T-10), one links to the
other and exactly one carries the detail — the stage table, since the
plan owns the design. The T-item keeps a one-line body plus the link.

### 3.5 `docs/archive/`

Completed execution-strategy docs and shipped work plans move here with a
dated status banner (T-17 pattern). Initial population:
`S9_WORK_STRATEGY.md`, and `proposals/post-0.11.0-work-plan.md` if
proposals/ is preferred to stay design-only. Session notes may stay in
proposals/ with banners — they are already correctly marked.

## 4. Migration plan (one session, mechanical)

1. Create RESOLVED.md; move closed entries out of KNOWN_ISSUES and
   TECHNICAL_DEBT verbatim, leaving one-line index stubs where prose was.
2. Rename/rebuild TECHNICAL_DEBT.md as TRACKING.md: status table + open
   items T-02(§5–6), T-03..T-14, T-16, T-19..T-28. Retire TD-nnn.
3. Narrow KNOWN_ISSUES.md to limits/boundaries/decisions; rewrite header.
4. `mkdir docs/archive`; move S9_WORK_STRATEGY with banner.
5. Write TRACKING_PRACTICES.md (§5).
6. Cross-check: every ID present exactly once as detail, links resolve,
   headers correct. Grep for dangling references to moved content.

No code changes. CHANGELOG entry under 0.15.x documenting the reshape.

## 5. Practices (to become `docs/TRACKING_PRACTICES.md`)

1. **Filing.** New actionable items go in TRACKING.md with the next T-nn,
   a priority, and a trigger line. Stage-scoped API v2 work goes in the
   S-table. Design intent goes in proposals/. Intentional limits go in
   KNOWN_ISSUES.
2. **Closure.** Closing an item = mark ✓ in the TRACKING table, move the
   detail to RESOLVED.md (top) with version + date, reference the
   changelog entry. Done in the same session as the closing release, not
   later. IDs are never reused.
3. **Headers.** Release-coupled documents (TRACKING, KNOWN_ISSUES) carry
   `Version:` matching VERSION plus `Last reviewed:`; both are updated by
   the release pass. Non-release-coupled documents (roadmaps, proposals)
   carry `Updated: <date>` only — never a bare product version, which is
   ambiguous between provenance and staleness.
4. **Proposals.** Any proposal overtaken by implementation gets a dated
   reconciliation banner (T-17 pattern) naming what shipped versus what
   was proposed. Historical content below the banner is not rewritten.
5. **Deviations.** Where execution departs from a plan, record the
   deviation in the tracking document; do not silently reconcile the plan.
6. **Release gate.** The release checklist (candidate for the T-22
   hygiene script) verifies: TRACKING contains no ✓ items, RESOLVED is
   append-only since the last tag, release-coupled headers match VERSION.

## 6. Open decisions (for Horatio)

- **D1 — Register name.** TRACKING.md (proposed; scope is wider than
  debt, and the file is a day old with one inbound link) vs keeping
  TECHNICAL_DEBT.md.
- **D2 — Verbatim vs condensed moves to RESOLVED.** Proposal says
  verbatim; condensing loses the fix-shape and stress-number provenance.
- **D3 — archive/ directory vs banners-in-place** for shipped strategies
  and work plans.
- **D4 — Sulpher roadmap.** Fold its open items into TRACKING as T-32+
  and archive the doc, or keep it as a living roadmap under the new
  header convention (Updated-date only).
- **D5 — proposals/ scope.** Design-only (work plans move to archive/) or
  design-plus-history as today.
