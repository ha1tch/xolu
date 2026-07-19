# molu Readiness — Stage Tracking

Version: 0.16.1
Last reviewed: 2026-07-18

Execution status for `docs/MOLU_READINESS_PLAN.md` (frozen 2026-07-18).
The plan defines the stages; this table records what actually happened,
including deviations, per `docs/TRACKING_PRACTICES.md` §5.

Status legend: ✓ done · ◐ partial · ☐ not started · ✗ dropped

| Stage | Summary | Status | Shipped | Notes |
|-------|---------|--------|---------|-------|
| M1 | SemanticMap API surface (T-24 + T-25) | ✓ done | v0.15.1 | `created_at` omitted from both envelopes (no backing data). T-32 filed en route. |
| M2 | Conventions + auth extraction (T-21 + T-19) | ✓ done | v0.15.2 | **Deviation** (see below). |
| M3 | Client Stage 5: cal methods | ✓ done | v0.15.3 | 4 methods, 12 tests incl. wire-level Openings→Check→Propose sequence. Stale mode comment in the cal handler header (pre-T-30) fixed en route. |
| M4a | Stage 6 sizing spike | ✓ done | — | Output: `docs/CLIENT_STAGE6_PLAN.md`. D-iii decided: T-26 folds in, minimal form. Headline finding: declared-scope coverage already complete; M4b is hardening, not endpoint work. ~2–2.5 d. |
| M4b | Stage 6 execution | ✓ done | v0.16.0 | Executed per plan. Integration suite surfaced the FTS double-gate and T-33 (filed). Client declared stable. **Plan complete: xolu-side molu readiness achieved** — blessing gated on T-34 multi-core verification (fix shipped v0.16.1). |
| M5 | Recorded deferrals | ✓ by definition | — | No work; the deferral decisions are recorded in the plan. |

## Deviations from the plan

- **M2 / T-19.** The plan said "refactor `AuthMiddleware` and its helpers
  to the lean type" within `pkg/middleware`. Executed that way, the
  acceptance check failed: Go dependencies are per-package, and
  `ratelimit.go` in the same package imports `pkg/config`, so any
  importer of `pkg/middleware` still dragged the full config surface.
  Resolution (approved by the project owner): `auth.go` and
  `tenant_grant.go` moved to a new **`pkg/authmw`** package (rate
  limiting deliberately left behind), with type-alias and function
  compatibility shims in `pkg/middleware` so no other xolu code changed.
  Acceptance now holds mechanically: `go list -deps ./pkg/authmw` shows
  `pkg/authconfig` and `pkg/authmw` as the only xolu-internal
  dependencies. Five rate-limiter tests that had lived in the combined
  `auth_test.go` were relocated to
  `pkg/middleware/ratelimit_relocated_test.go`.
