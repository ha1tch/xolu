# Work Plan — Post-0.11.0

> **Archived 2026-07-18.** Largely shipped by 0.13.0 (see the status note
> below, retained from the original). Moved from `docs/proposals/` under the
> design-only proposals policy; content unchanged.

Status: Largely shipped (see status note below)
Prepared: 2026-06-21
Baseline: xolu 0.11.0 (tenant access control shipped; release pipeline gates on lint)

> **Status note (updated 2026-06-21, as of 0.13.0).** Most of this plan has
> shipped; this banner records what is done so the body below reads as history.
> - **Phase 1 (lint hygiene, A→B→C): DONE.** golangci-lint runs as a clean
>   zero-issue release gate; releases use `release.sh <ver> --short`, not
>   `--no-lint`.
> - **D1 (non-JWT scoped test breadth): DONE.** `tenant_scoped_nonjwt_test.go`
>   covers API-key cross-tenant, flat-key-no-grant, admin-key, and bearer paths.
> - **D2 (S3 grant mapping): DONE (0.12.x).** The S3 plane derives a
>   `TenantGrant` from the SigV4 access-key's `S3KeyGrant` and enforces
>   `grant.Allows(bucket)` with signature verification.
> - **D3 (claim names): settled** on `tenants` / `tenant_admin` as shipped.
> - **E1 (stale `AuthType` doc comment): DONE (0.13.0).** Comment now lists
>   `bearertoken`.
> - **E3 (otogen test file): DONE.** `cmd/otogen/main_test.go` exists.
> - **Blob plane per-tenant layout + isolation: DONE (0.13.0).** Not in the
>   original plan; blobs moved to `<BaseDir>/tXXXX/blobs/` with per-identity
>   grant enforcement on both the native and S3 planes. See `CHANGELOG.md` and
>   `KNOWN_ISSUES.md`.
>
> **Still open (out of scope for 0.13.0):**
> - **`cmd/iolu` layout alignment** — the admin CLI still uses the legacy
>   `--db`/`--ts-dir` path model and does not know the normalized
>   `tXXXX/{store,ts,blobs}` layout. Do not run `iolu` against an `xolu`-managed
>   data root. ~870-line parallel rewrite; tracked in `KNOWN_ISSUES.md`.
> - **E2 (proposals doc location)** — cosmetic; the implemented tenancy docs
>   still live under `docs/proposals/` rather than the flat `docs/*.md`
>   convention.

This plan sequences the known backlog by **dependency**, not by preference. Each
item lists what it unblocks and what it requires, so the order is derivable
rather than asserted. Where two items are independent, that is stated so they can
be reordered or parallelised freely.

---

## The dependency graph (summary)

```
  A. .golangci.yml baseline ──┐
                              ├──> B. lint hygiene ──> C. drop --no-lint from release flow
  (independent) ──────────────┘
                                                       │
  D. tenancy follow-ups (test breadth, S3 grant) ──────┤ (independent of lint;
                                                       │  shares release gate)
  E. cosmetic cleanups ───────────────────────────────┘
                                                       │
                                              F. next release (0.11.1 / 0.12.0)
```

The single hard ordering constraint is **A → B → C**. Everything else is
independent and only converges at a release (F), which wants the tree green.

---

## Phase 1 — Lint hygiene (the critical path)

This is first because it is the only thing currently **blocking clean releases**:
installing golangci-lint turned its step into a release gate, so every release now
needs `--no-lint` until the backlog is cleared. Nothing else depends on it
functionally, but it taxes every future release until done.

### A. Establish `.golangci.yml` baseline  *(no dependencies)*

Create a committed linter config before fixing anything, because the config
decides *which* of the 100 findings are real versus intentionally-allowed. Doing
this first prevents wasted effort "fixing" findings you would rather exclude.

- Decide the enabled linter set (the current run uses defaults: errcheck, govet,
  ineffassign, staticcheck, unused).
- Decide policy on the noisy-but-often-acceptable class: unchecked errors on
  `defer x.Close()` and `w.Write(...)` in HTTP handlers (the bulk of the 50
  errcheck findings). Either exclude this pattern in config, or commit to
  handling each — a real decision, not a mechanical one.
- Output: `.golangci.yml`. After this, `golangci-lint run` reports only the
  findings you have agreed are worth fixing.

**Unblocks:** B (gives B a definitive target count).

### B. Fix the agreed findings  *(requires A)*

Current raw breakdown (pre-config): **100 issues** — errcheck 50, staticcheck 28,
unused 15, ineffassign 4, govet 3. Suggested internal order, easiest/safest first
so the tree stays green throughout:

1. **govet (3)** — the `reflect.Ptr` → `reflect.Pointer` modernizations in the two
   AST gate files. Already prototyped once this session; trivial, mechanical.
2. **ineffassign (4)** — ineffectual assignments; localised, low-risk.
3. **unused (15)** — dead funcs/types. Verify each is truly dead (some are in test
   files, some like `resetSeqSession` / `s3NotEnabled` may be intentional
   scaffolding) before deleting. This needs judgement, not bulk removal.
4. **staticcheck (28)** — style/correctness suggestions; review case by case.
5. **errcheck (50)** — largest; mostly `defer Close()` / `Write()`. If A excluded
   the handler-defer pattern, this number drops substantially and the remainder
   are genuine unchecked errors worth handling.

Build + test after each linter-class pass, not at the end — same discipline as the
mass-substitution work; broken state must not accumulate.

**Unblocks:** C.

### C. Drop `--no-lint` from the normal release flow  *(requires B)*

Once `golangci-lint run` is clean against the committed config, releases no longer
need `--no-lint`. The flag stays (it is a legitimate escape hatch), but the
default release returns to lint-as-hard-gate. This is the payoff that makes the
gate useful instead of an obstacle.

**This is the natural cut point for a lint-only release** (0.11.1, patch — it is
hygiene, no behaviour change).

---

## Phase 2 — Tenancy follow-ups  *(independent of Phase 1)*

The 0.11.0 feature is complete and safe as shipped. These items **broaden
assurance and close the first-cut deferrals**; none fixes a hole. They are
independent of the lint work and of each other except where noted, so they can be
done in any order or interleaved.

### D1. Test breadth for non-JWT grant paths

The JWT path has the full adversarial suite. The API-key (`APIKeyGrants`) and
bearer-admin paths are implemented but lightly tested. Add cross-tenant tests
mirroring `tenant_scoped_auth_test.go` for: an API key scoped to one tenant is
403 on another; a flat (`APIKeys`) key with no grant is 403 under scoped; the
bearer token reaches any tenant. No code change expected — this is coverage.

**Requires:** nothing. **Risk:** low (may surface a gap, which is the point).

### D2. S3 grant mapping (the real design gap)

The largest open design item. Currently scoped forces `S3RequireAuth` and uses
the SigV4 access-key as the tenant identity, but does **not** map an S3 key to a
`TenantGrant` the way the main server does. Closing this requires deciding how
SigV4 access-key identity binds to a grant (proposal §8.6). This is design +
implementation, not just tests.

**Requires:** the §8.6 SigV4-identity decision (a design step) before code.
**Independent of D1, D3.**

### D3. Confirm or revise first-cut claim choices

Claim names (`tenants` / `tenant_admin`) and the wildcard approach were
implemented with leans, not confirmed. If they change, `otogen`, the validator,
and the ops guide all move together. Cheapest to settle **before** D1/D2 harden
around the current names, but not a hard blocker.

**Note:** D3 ideally precedes D1 (so tests don't bake in names you later change).
Soft ordering: D3 → D1, with D2 independent.

---

## Phase 3 — Cosmetic cleanups  *(independent; batch anytime)*

Genuinely optional; fold into whatever release is next.

- **E1.** Stale `AuthType` doc comment in `config.go` (omits `bearertoken` though
  validation accepts it). One-line fix.
- **E2.** `docs/proposals/` does not match the repo's flat `docs/*.md`
  SCREAMING_CASE convention. Decide whether to move/rename the two tenancy docs
  now that the design is implemented (they are arguably no longer "proposals").
- **E3.** `otogen` has no permanent test file under `cmd/otogen/`. Add a
  `main_test.go` (token-validates, guardrails-fire, secret-handling) if it should
  carry regression coverage.

---

## Standing items (not work; verification debt)

- Every release this session ran `--short`. The full stress suite must run
  locally before any tag.
- golangci-lint and the Go toolchain re-install each session (sandbox resets);
  re-bootstrap at session start.

---

## Recommended path

1. **A → B → C** (lint hygiene → drop `--no-lint`), cut as **0.11.1**. Highest
   leverage: it unblocks every future release. Fold in E1/E2/E3 since the tree is
   already open.
2. **D3 → D1** (confirm claim names, then harden non-JWT test coverage), and
   **D2** (S3 grant mapping) when the SigV4 design is settled. These form the
   tenancy-completion release, **0.12.0** (D2 is feature-bearing, hence minor).

The only thing that *must* come first is A (config before fixing). Everything else
is schedulable around the two release cut-points.
