# molu Readiness Plan

Updated: 2026-07-18

Staged plan bringing xolu to the state molu Parts 2 and 3 assume. Grounded
in the `Blocks/after` fields of `docs/TRACKING.md`; this plan owns the
sequencing and stage detail, the register items link here. Per
`docs/TRACKING_PRACTICES.md`, this document freezes once execution starts;
execution status will live in a `MOLU_READINESS_TRACKING.md` stage table
created at that point, and deviations are recorded there, not edited here.

Scope boundary: xolu-side work only. molu's own implementation state is
tracked in the molu repository.

---

## What molu assumes, and where xolu stands

| molu assumption | Source | xolu state at v0.15.0 |
|-----------------|--------|-----------------------|
| cal reachable over HTTP: check/openings/propose/confirm, four objectives | Part 2 §5.1.10–13 | ✓ shipped v0.14.7–v0.14.11 (T-18, closed) |
| cal model is exclusive-only | Part 2 review | ✓ v0.14.12/13 (T-30, closed) |
| Go client covers cal | Part 2 tools | ✗ client Stage 5 not started (T-02) |
| Client is a stable, fully-typed surface | Part 2 | ✗ Stage 6 not started, unsized (T-02) |
| SemanticMap can discover entity types at runtime | Part 2 §4 | ✗ no list endpoint (T-24) |
| SemanticMap can advertise sequences | Part 2 §4 | ✗ no list endpoint (T-25) |
| Hub reuses xolu auth without the full config surface | Part 3 §5 | ✗ auth coupled to `config.Config` (T-19) |
| `MOLU-*` error codes cannot collide with xolu's | Part 2 §8.5, Part 3 §5.2 | ✗ prefixes unreserved (T-21) |
| Schema refresh by polling is acceptable | Part 2 §4.3 | ✓ by design; events are a future option (T-20, deferred) |
| Events are NOT the source of truth for state change | Part 1 event model limits | ✓ documented boundary; durability is post-readiness (T-07, deferred) |

## Stages

Dependency shape: M1 and M2 are independent of each other and of M3;
M3 depends on nothing open (its server surface shipped); M4 runs last
because its audit must see the M1/M3 client additions. Everything before
M4b is estimable; M4b is sized by M4a.

### M1 — SemanticMap API surface (T-24 + T-25)

One server-side release plus one client pass, shipped together.

- Server: `handleListSchemas` (`pkg/server/handlers.go`) with
  `GET /api/v1/schemas`, envelope consistent with existing v1 list
  endpoints; `handleSeqList` (`pkg/server/v2_seq_handlers.go`) with
  `GET /api/v2/gen/seq` coexisting with the existing POST (chi routes by
  method), mirrored at `/seq` if the alias convention holds.
- Client: `ListEntityTypes(ctx)`, `ListSequences(ctx)` with typed
  summaries, tests per the established `httptest` pattern.
- Docs: `API_REFERENCE.md` and `API_V2.md` entries.
- Acceptance: both endpoints enumerable end-to-end from the client; molu
  Part 2 §4 SemanticMap has no remaining configuration-only inputs.
- Estimate: 2 days.

### M2 — Conventions and auth extraction (T-21 + T-19)

- T-21: reserve `MF` (molu front) and `MH` (molu hub) in
  `ERROR_CODES.md` as satellite-project area prefixes (**decided**, per
  D-i). No code change; molu defines the actual codes in its own repo.
- T-19: extract the lean auth-config subset into a new `pkg/authconfig`
  package (**decided**: new package, per D-ii). The extracted field set is
  the source-verified read-set of `pkg/middleware/auth.go` at v0.15.0 —
  `AuthType`, `JWTSecret`, `JWTIssuer`, `APIKeys`, `APIKeyGrants`,
  `AuthExcludePaths`, `InternalToken` — not the register's original list
  (which named `TenantAuthMode`, unread by auth.go, and omitted
  `InternalToken`); re-verify the read-set at implementation time.
  Refactor `AuthMiddleware` and helpers to the lean type; xolu server
  wires the subset from full config at startup, behaviour unchanged;
  auth package published as a stable import surface with documented
  semantics. Verified: `pkg/dynconfig` has no auth coupling in either
  direction, so no runtime-refresh path is needed; if dynconfig ever
  grows auth keys, that becomes a new register item.
- Acceptance: existing auth test suite green unchanged; a toy external
  binary imports the auth package without importing `pkg/config`.
- Estimate: T-21 hours; T-19 one to two days.

### M3 — Client Stage 5: cal methods (T-02)

- `CalCheck`, `CalOpenings`, `CalPropose`, `CalConfirm` against the
  shipped `/api/v2/cal/*` surface; typed request/response mirroring the
  server shapes byte-for-byte per the Stage 2 convention; objective
  values validated client-side against the four implemented objectives;
  `XOLU-CAL001–007` mapped through the structured `Error` type.
- Tests per the established client pattern; the Openings→Check→Propose
  sequence exercised explicitly (the T-29 property guards the server
  side; the client test guards the wire).
- Acceptance: molu Part 2 cal tools have a complete client surface.
- Estimate: 1–2 days.

### M4 — Client Stage 6: coverage audit (T-02, unsized)

Split deliberately, because "full v1 endpoint coverage audit" is scope
that grows on contact.

- **M4a — sizing spike.** Enumerate every v1/v2 endpoint against client
  coverage; list every `map[string]any` site where structure exists;
  list godoc gaps; decide whether T-26 (in-process integration suite)
  folds in (**decision input D-iii**). Output: a concrete M4b task list
  with an estimate, filed before any M4b work starts.
  Estimate: ½–1 day.
- **M4b — execution.** Per the M4a output. Not estimated here by design.
- Acceptance: client declared version-tied and stable for molu
  consumption; T-02 closes via the standard closure procedure.

### M5 — Recorded deferrals (no work)

Readiness explicitly does **not** include, and molu's design already
tolerates: T-20 (schema-change events — polling stands, per Part 2
§4.3), T-07 (durable dispatch — events remain advisory, polling remains
authoritative, per the Part 1 boundary), T-27/T-28 (FSM read-surface
polish — wanted before real-world load, not before molu lands). These
stay in the register at their current priorities; this stage exists so
the deferral is a recorded decision rather than an omission.

## Decision inputs required before execution

All four resolved 2026-07-18; the plan is fully decided and frozen on
execution start (this session):

- **D-i:** `MF` / `MH` short area codes. Approved.
- **D-ii:** new `pkg/authconfig` package. Approved (drives the "auth
  importable by molu, wired from full config by xolu" requirement).
- **D-iii:** the M4a spike decides T-26 fold-in.
- **D-iv:** M1 → v0.15.1, M2 → v0.15.2, M3 → v0.15.3, M4b → v0.16.0.

## Critical path

M1 + M2 + M3 ≈ 4–6 days, parallelisable to ~3–4 elapsed if M1 and M2
interleave. M4 gates final readiness and is unsized until M4a reports.
The register remains the authority on item status; this plan is the
authority on sequencing.
