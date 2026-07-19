# Tracking Document Practices

Updated: 2026-07-18

The rules governing work-tracking documents in this repository. Adopted via
`docs/proposals/tracking-normalisation.md`; this file mirrors the project
owner's working-agreement Part 3 so the repository is self-describing.

## Taxonomy

| Kind | File | Contains | Mutability |
|------|------|----------|------------|
| History | `CHANGELOG.md` | What shipped, when | Append-only |
| Live register | `docs/TRACKING.md` | Open actionable items ONLY | Edited freely |
| Resolution record | `docs/RESOLVED.md` | Closed items, full text as at closure, version + date | Append-only, newest first |
| Limits | `docs/KNOWN_ISSUES.md` | Intentional limits, invariant boundaries, recorded decisions | Edited freely |
| Plan | `*_DEVELOPMENT_PLAN.md` | Design intent for staged work | Frozen once execution starts |
| Stage tracker | `API_V2_TRACKING.md` | Stage-status table for one staged plan | Edited freely |
| Proposal | `docs/proposals/*.md` | Design intent | Reconciliation banner when overtaken |
| Archive | `docs/archive/` | Shipped strategies and completed work plans | Frozen; banner added on move |

Never introduce a new tracking-flavoured document without confirming no
existing kind covers it.

## The live register

Open items only — a closed item still present in the register is itself a
defect. Status table at the top; detail sections below, **grouped by
theme**. Status legend: ✓ done · ◐ partial · ☐ not started · ✗ dropped.
One ID namespace (T-nn); IDs are never reused and never renumbered;
retired namespaces get pointers in `RESOLVED.md`, not rewrites. Register
scope is any actionable item: debt, defects, gaps, hardening, tooling,
and features filed as prerequisites.

Each detail section carries field lines directly under its heading:
`Theme:` (what code the work lives in), `Priority:` (importance only,
P1 highest), `Status:`, and `Blocks/after:` (what the item gates or is
gated by — register items, plan stages, or external consumers as
first-class targets). Theme and enablement are deliberately separate
axes: a debt item's subject and its beneficiary usually differ.
Provenance is not a grouping; it lives in each item's Trigger line. The
status table is derived from the field lines and must not diverge from
them — consistency is a release-gate check, not a convention.

Demarcation with the stage tracker: the S-table owns stage delivery
detail; where a register item overlaps a stage, the item links to the
stage rather than duplicating it.

## Closure procedure

In the same session as the closing release:

1. Move the item's full detail text **verbatim** to the top of
   `RESOLVED.md`, stamped with closing version and date.
2. Delete it from the register — row and detail section. No tombstones.
3. Cross-reference the changelog entry. The changelog says *what
   shipped*; the resolution record says *what was wrong and how it was
   resolved*. The two reference, never duplicate, each other.

## Header discipline

Release-coupled documents (`TRACKING.md`, `KNOWN_ISSUES.md`) carry
`Version:` matching the `VERSION` file plus `Last reviewed:`, both updated
as part of the release pass. Non-release-coupled documents (roadmaps,
proposals, strategies) carry `Updated: <date>` only — never a bare product
version, which is ambiguous between provenance and staleness.

## Proposals, plans, and deviations

A proposal overtaken by implementation gets a dated reconciliation banner
naming what actually shipped versus what was proposed; content below the
banner is historical and is not rewritten. Where execution departs from a
plan, record the deviation in the tracking document — never silently edit
the plan to match reality.

## Release gate

The release checklist verifies: the register contains no ✓ items;
`RESOLVED.md` has changed append-only since the last tag; release-coupled
headers match `VERSION`; and the register's status table agrees with the
per-item `Theme:`/`Priority:`/`Status:` field lines. These checks belong
in the release-hygiene script (register item T-22) rather than in human
memory.
