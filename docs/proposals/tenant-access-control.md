# Proposal: Tenant Access Control

Status: Implemented (see §9 for as-built notes)
Target: 0.11.0 (feature work; not a patch)
Author: prepared for haitch
Scope: binding authenticated identity to tenant authority in the xolu HTTP server

---

## 1. The problem, precisely stated

xolu authenticates at the **server** level and selects the tenant from the
**request**. There is no link between the two.

Concretely, in the current code:

- `middleware.AuthMiddleware` (pkg/middleware/auth.go) establishes only a
  `subject` string and an `auth_method`. For JWT the subject is the `sub` claim;
  for API key it is derived from which key matched; for the bearer token it is a
  single shared secret. Authentication answers *"does this caller hold a valid
  server credential?"* — nothing more.
- `tenantMiddleware` (pkg/server/server.go) reads `{tenant_id}` from the URL,
  resolves it to a numeric tenant ID, creates the tenant store, and places it in
  the request context. It validates that the tenant *exists*; it does **not**
  check that the caller is *entitled* to that tenant.

The consequence: any holder of a valid server credential can read or write any
tenant's data by changing the URL path. Tenant isolation today is a
**data-partitioning** mechanism (each tenant's rows live in `t<XXXX>_*` tables),
not an **access-control boundary** between mutually-distrusting callers.

This was confirmed by code audit, not exploitation, and is recorded as a design
note in the 0.10.5 CHANGELOG.

## 2. What is and isn't broken

This is not a bug in the sense the D-0xx findings were. Several signals show the
current behaviour is **intentional for xolu's present deployment model**:

- A single shared server credential (one JWT secret, one API-key list, or one
  internal token) — none of which inherently carries a tenant identity.
- `TenantAutoRegister`, which creates a tenant on first access in non-strict
  mode — only coherent if any authenticated caller may manage any tenant.

For the **single-customer, trusted-gateway, and edge deployments** that are
xolu's current target (e.g. Live.IO on an edge box, or a backend service that is
the sole client), this model is correct and adding per-tenant authorization would
be friction with no benefit.

The gap matters only for a specific future scenario: **exposing one xolu endpoint
directly to multiple mutually-distrusting tenants.** The proposal below makes that
scenario *possible to do safely* without imposing cost on the deployments that
don't need it.

## 3. Design principles

1. **Opt-in, not forced.** The default behaviour must remain today's: server-level
   auth, any tenant reachable. Operators who want tenant-scoped identity turn it
   on explicitly. This preserves every existing deployment.
2. **Fail closed when enabled.** When tenant scoping is on, a request whose
   identity does not authorise the requested tenant is rejected (403), never
   silently served or silently downgraded.
3. **Identity carries authority, not the URL.** When enabled, the tenant a caller
   may act on comes from the *authenticated identity*, and the URL `{tenant_id}`
   is checked against it — never the other way around.
4. **One enforcement point.** The authorization decision lives in exactly one
   place (`tenantMiddleware`), so there is no second, weaker door.
5. **Works across all three auth types**, with honest treatment of what each can
   express.

## 4. The mechanism

### 4.1 A new config switch

Add `TenantAuthMode` (string), independent of the existing `TenantMode`
(which governs registration/lookup, a separate concern):

| Value      | Meaning                                                            |
|------------|-------------------------------------------------------------------|
| `open`     | Default. Today's behaviour: any authenticated caller, any tenant. |
| `scoped`   | The caller's identity must authorise the requested tenant.        |

`open` is the default so the change is strictly backward-compatible.

> **Note:** §4.1 describes the switch as first drafted (independent of
> `TenantMode`). The self-review in §8.2 found the two dimensions are *not*
> independent. The matrix below reflects the corrected understanding and is the
> authoritative version; the prose immediately above is retained only to show the
> evolution. If the merged single-setting option (§8.2) is chosen, this becomes
> one `TenancyMode` enum and the matrix collapses to its diagonal.

### 4.1.1 Combination matrix

The existing `TenantMode` has two real values in the code today — `path`
(default: tenant routes with optional auto-registration; **unprefixed routes are
served as tenant 0**) and `strict` (all entity access must go through
`/tenant/{id}/`; unprefixed routes return 403; tenants must be pre-registered).
A `"none"` value is silently coerced to `path` by config validation, so there is
no distinct single-tenant *mode* — but single-tenant is the most common
*deployment shape* and must be reasoned about explicitly (see §4.1.2).

Crossing `TenantMode` with the proposed `TenantAuthMode` (`open` / `scoped`)
gives four cells, of which only some are coherent:

| `TenantMode` | `TenantAuthMode` | Valid? | Behaviour / why                                                                                                                                                              |
|--------------|------------------|--------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`       | `open`           | ✓      | **Today's default, and the single-tenant deployment shape.** Any authenticated caller reaches any tenant; unprefixed routes hit tenant 0. Correct for single-customer / trusted-gateway / edge. No change. |
| `strict`     | `open`           | ✓      | Routing is forced through `/tenant/{id}/` (no tenant-0 bypass), but **any** authenticated caller may still pick any tenant. Tighter routing, same trust model. Valid.         |
| `strict`     | `scoped`         | ✓      | **The target multi-tenant-untrusted configuration.** Unprefixed routes are blocked by strict routing, and identity must authorise the requested tenant. The only fully safe scoped cell. |
| `path`       | `scoped`         | ✗      | **Incoherent — rejected at config validation.** `scoped` authorises the URL tenant, but `path` mode leaves the unprefixed tenant-0 routes open and un-checked, so the authorization is trivially bypassed. Allowing this cell would give a false sense of isolation. |

**Rule enforced by config validation:** `TenantAuthMode: scoped` requires
`TenantMode: strict`. The `path` + `scoped` combination is a startup error with a
message pointing the operator at `strict`. (This is the §8.2 finding expressed as
a validation rule. If the single-`TenancyMode` enum is chosen instead, the
incoherent cell simply does not exist as a representable value — the preferred
outcome.)

### 4.1.2 Single-tenant deployments

A single-tenant deployment (xolu's most common shape — an edge box, an embedded
instance, or a backend that is the sole client) runs in `path` mode using only
the unprefixed `/api/v1/{entity}` routes against tenant 0. It never creates a
second tenant and never uses `/tenant/{id}/` paths.

For this shape, **`scoped` is not merely unnecessary — it is actively wrong:**

- There is no second tenant to isolate from, so per-tenant authorization solves a
  problem that does not exist.
- `scoped` requires `strict` (per the matrix), and `strict` returns 403 for the
  unprefixed routes — which are the *only* routes a single-tenant deployment
  uses. Forcing `scoped` on a single-tenant box would therefore **break its
  normal API**, requiring every call to move to `/tenant/0/...` with a tenant
  grant, for zero isolation benefit.

The correct guidance is explicit: **single-tenant deployments stay on
`path` + `open` (the default). Do not enable `scoped`.** The tenant-isolation
feature is opt-in precisely so that the common single-tenant case pays nothing
for it and is never pushed into a configuration that breaks its API. This is the
strongest argument for keeping `open` the default and for treating `scoped` as a
deliberate multi-tenant-untrusted opt-in rather than a "more secure" setting that
operators might enable reflexively.

> A subtle trap worth stating: `scoped` reads like "the hardened option," so a
> security-conscious operator of a single-tenant box might enable it by reflex
> and break their deployment. The documentation and the config error message
> should say plainly that `scoped` is for multi-tenant-untrusted endpoints and is
> the wrong choice for single-tenant or trusted-gateway deployments — security is
> not monotonic in this setting.

> **Cross-server caveat (S3).** This matrix governs the main HTTP server. The S3
> gateway (§8.1 path 3) resolves tenant independently and is **not** covered by
> these modes as drafted. Under any `scoped` configuration, `S3RequireAuth` must
> additionally be mandatory (no bucket-name fallback), or the S3 port is an
> unguarded tenant path regardless of what this matrix says. See §8.5 step 6.

### 4.2 Identity → tenant authority, per auth type

The three auth types differ in what identity they can express. The design is
honest about this rather than pretending they are uniform.

**JWT (the first-class path).** Extend the validated claims to read an optional
tenant grant. Two shapes, supporting both single- and multi-tenant tokens:

- `tenant` (string) — the one tenant this token may act on; or
- `tenants` (array of strings) — the set this token may act on; or
- a wildcard sentinel (e.g. `tenants: ["*"]`) — an administrative token allowed
  any tenant (this is how you keep an ops/superuser path under `scoped`).

`validateJWT` already extracts `sub`; it gains extraction of these claims and
returns them alongside the subject. The claim is cryptographically bound to the
token, so it cannot be forged by the caller — this is what makes JWT the right
vehicle for true multi-tenant-untrusted.

**API key (config-side mapping).** API keys are currently an undifferentiated
list (`cfg.APIKeys`). To scope them, the key→tenant relationship must live in
config, since the key itself carries no metadata. Introduce a structured form:

```
APIKeyGrants:
  - key: "<key-1>"
    tenants: ["acme"]
  - key: "<key-2>"
    tenants: ["*"]        # admin key
```

The flat `APIKeys` list remains valid and, under `scoped`, is treated as
unscoped/admin (or rejected — operator's choice via a sub-flag) so existing
configs don't silently lose access.

**Bearer token (the trusted-gateway credential).** The single `InternalToken`
has no per-identity notion by construction. Under `scoped` it is treated as a
**full-authority gateway credential** (equivalent to `tenants: ["*"]`). This is
the correct semantics: the bearer token is for a trusted front-end that has
*already* done its own per-user authorization and is calling xolu on their
behalf. It is documented as such so operators do not mistake it for a
tenant-isolating mechanism.

### 4.3 The enforcement point

`AuthMiddleware` gains responsibility for placing the resolved tenant grant into
the context (a new `ContextKeyTenantGrant`), since it is the only layer that sees
the credential. `tenantMiddleware` — which already resolves the requested `tid` —
gains a single check, only when `TenantAuthMode == "scoped"`:

```
grant := tenantGrantFromContext(ctx)   // set by AuthMiddleware
if !grant.Allows(requestedTenantName, requestedTID) {
    writeError(403, ErrTenantForbidden, "credential not authorised for tenant")
    return
}
```

`grant.Allows` returns true for a wildcard grant, or for an exact membership
match. No other code path changes: every tenant-scoped handler continues to read
the tenant from context exactly as today, so the single check governs all of
them.

### 4.4 Interaction with auto-registration

Under `scoped`, `TenantAutoRegister` must not let a caller conjure a tenant they
are then "authorised" for. Rule: auto-registration is permitted only for a
wildcard (admin) grant, or disabled entirely under `scoped`. A non-admin caller
hitting an unknown tenant gets 403/404, never an auto-created tenant.

## 5. What this is not

- **Not** per-user or per-row authorization within a tenant. This binds an
  identity to a *tenant boundary*; finer-grained access control inside a tenant
  is a separate, larger concern and explicitly out of scope.
- **Not** a change to the storage layer. Data partitioning by `t<XXXX>_` prefix
  already provides the isolation *substrate*; this proposal adds the
  *authorization* that decides which partition a caller may reach.
- **Not** a forced migration. `open` mode is the default and preserves every
  current deployment unchanged.

## 6. Implementation outline (when approved)

Ordered so each step is independently testable and the tree stays green:

1. Add `TenantAuthMode` config (default `open`) + validation. No behaviour change.
2. Define `TenantGrant` type and `Allows(name, tid)` with exhaustive unit tests
   (exact match, multi-tenant, wildcard, empty/deny).
3. Extend `validateJWT` to read `tenant`/`tenants` claims; thread the grant
   through `AuthMiddleware` into context. Default/absent claim → empty grant.
4. Add `APIKeyGrants` config structure; map matched key → grant. Keep flat
   `APIKeys` working.
5. Treat the bearer `InternalToken` as a wildcard grant; document it.
6. Add the single `Allows` check in `tenantMiddleware`, gated on
   `TenantAuthMode == "scoped"`.
7. Constrain `TenantAutoRegister` under `scoped` (admin-only or off).
8. Adversarial tests: under `scoped`, a token for tenant A is rejected (403) on
   tenant B's routes across every tenant-scoped endpoint family (graph, ts,
   crud, blob); wildcard/admin reaches all; `open` mode unchanged.
9. Documentation: a security section describing the three trust models and how to
   choose, plus an explicit statement that `open` is not safe for
   mutually-distrusting tenants.

## 7. Open questions for review

1. **Default under `scoped` for legacy flat `APIKeys`** — treat as admin
   (convenient, less safe) or reject (safe, requires config migration)? Lean:
   reject, with a clear error pointing to `APIKeyGrants`.
2. **Claim names** — `tenant`/`tenants` vs a namespaced claim
   (`https://oldbytes.space/tenants`) to avoid collision with other JWT
   consumers. Lean: simple names, documented, since xolu issues/consumes its own
   tokens.
3. **Wildcard sentinel** — `["*"]` vs a dedicated boolean claim (`tenant_admin:
   true`). Lean: explicit boolean is harder to set by accident than a string that
   could appear in a tenant list.
4. **Scope of the first cut** — JWT-only for the first release (the only auth
   type that can carry per-identity tenant authority natively), with API-key
   grants and the bearer-token semantics following? This reduces blast radius and
   gets the cryptographically-sound path shipped first.

---

## 8. Self-review: holes found in this proposal

A review of the draft against the actual code found four problems with the design
as first written. They are recorded here rather than silently edited, because the
errors are instructive about the real shape of the system.

### 8.1 "One enforcement point" was wrong — there are at least three tenant paths

The draft claimed a single check in `tenantMiddleware` would govern all
tenant-scoped access. The code has **three** distinct tenant-resolution paths:

1. **Prefixed REST** — `/api/v1/tenant/{tenant_id}/...`, via `tenantMiddleware`.
   (The one the draft covered.)
2. **Unprefixed / default-tenant REST** — `/api/v1/{entity}`, `/api/v1/commit`,
   etc., mounted with the *same handlers* but operating as tenant **0** with no
   `tenantMiddleware`. A check placed only in `tenantMiddleware` does not run
   here.
3. **S3 / blob gateway** — a separate `s3Server` on its own port that derives the
   tenant from the AWS SigV4 access-key ID, falling back to the **bucket name**
   (`s3TenantFromRequest`). It never touches `AuthMiddleware`, `tenantMiddleware`,
   or `TenantAuthMode` at all. Its no-auth fallback (bucket-name-as-tenant unless
   `S3RequireAuth` is set) is itself an open-tenant path.

**Correction:** the enforcement cannot live in one middleware. The design must
define a single *authorization function* (`grant.Allows(name, tid)`) and call it
at **every** tenant-resolution site: in `tenantMiddleware`, in the default-tenant
resolution (`getStore`/tenant-0 path), and in the S3 tenant resolver. The claim
to consolidate is the *decision logic*, not the *call site*.

### 8.2 `TenantAuthMode` and `TenantMode` are entangled, not orthogonal

The draft introduced `TenantAuthMode` as independent of the existing
`TenantMode`. They are not independent. `tenantStrictMiddleware` (active only when
`TenantMode == "strict"`) is what *blocks the unprefixed default-tenant routes*
(403 "Tenant context required"), forcing traffic through the `/tenant/{id}/`
tree. If an operator sets `TenantAuthMode: scoped` but leaves `TenantMode` open
(non-strict), the unprefixed tenant-0 routes stay reachable and bypass the
authorization check entirely.

**Correction:** `scoped` authorization must require strict tenant routing.
Either (a) `TenantAuthMode: scoped` implies/forces `TenantMode: strict` (and
config validation rejects the incoherent combination), or (b) the two are merged
into one tenancy-mode setting with values like `open` / `strict` / `scoped` that
are mutually exclusive by construction. Lean: a single setting, since the matrix
of independent values contains incoherent cells.

### 8.3 The default-tenant (tenant 0) exposure was understated

Even with the §8.1 fix, the draft did not address what `scoped` *means* for the
default tenant. Tenant 0 is a real tenant. Under `scoped`, a non-admin caller
must not reach tenant 0 via the unprefixed routes. The cleanest resolution is
that `scoped` mode disables the unprefixed routes entirely (they have no tenant in
the URL to authorize against), requiring all callers — including admins — to use
explicit `/tenant/{id}/` paths. This should be stated, not left implicit.

### 8.4 Existing isolation patterns were not acknowledged

The codebase already isolates some components per-tenant: each tenant has its own
`JobManager`, and there is a test asserting one tenant cannot see another's async
jobs (`graph_tenant_exhaustive_test.go`). This is a *positive* finding — async
job retrieval is already tenant-isolated by construction — but the proposal
should align with this existing pattern (per-tenant component instances) rather
than describe authorization as if starting from nothing. It also means the
adversarial test suite in §6.8 should extend the *existing* cross-tenant job
isolation tests, not invent a parallel scheme.

### 8.5 Revised implementation outline (supersedes §6 where they conflict)

1. Decide the config shape (§8.2): lean toward a single `TenancyMode` =
   `open` | `strict` | `scoped`, with validation rejecting incoherent legacy
   combinations.
2. `TenantGrant` type + `Allows(name, tid)` with exhaustive unit tests.
3. Thread the grant from each auth type into context (JWT claims first).
4. Under `scoped`: **disable the unprefixed default-tenant routes** so there is no
   un-authorized tenant-0 path (§8.3).
5. Call `Allows` in `tenantMiddleware` (prefixed REST).
6. Call `Allows` in the **S3 tenant resolver** (§8.1 path 3); decide how SigV4
   access-key identity maps to a grant, and make `S3RequireAuth` mandatory under
   `scoped` (no bucket-name fallback).
7. Constrain `TenantAutoRegister` under `scoped` (admin-only or off).
8. Adversarial tests across **all three** paths — extend the existing
   cross-tenant isolation tests — proving a tenant-A identity is rejected on
   tenant B for prefixed REST, default-tenant REST (should be disabled), and S3.
9. Documentation of the three trust models and the explicit statement that
   non-`scoped` modes are not safe for mutually-distrusting tenants.

### 8.6 Residual unknowns still to verify before implementation

A grep for all tenant-resolution sites was run as part of this review and found
**more paths than §8.1 listed** — the "three paths" figure was itself incomplete.
The corrected inventory of request-driven tenant resolution is:

1. Prefixed REST — `tenantMiddleware` (URL `{tenant_id}`).
2. Unprefixed / default-tenant REST — tenant 0, no middleware.
3. S3 / blob gateway — `s3TenantFromRequest` (SigV4 access key, bucket fallback).
4. **Regular (non-S3) blob handlers** — `tenantForBlob(r)`, which reads a
   `tenant` field from the **request body** (`blob_handlers.go`). A tenant taken
   from the request body is the most directly caller-controlled path of all and
   must be authorized under `scoped`.
5. **Timeseries handlers** — take `tenant_id` from the URL param *and* some
   request bodies carry a `tenant_id` JSON field (`ts_handlers.go`); needs
   confirmation of which is authoritative.

**This is the central lesson of the self-review:** the authorization design is
only as good as the completeness of the path inventory, and the inventory took
three passes to stabilise. Before implementation, a definitive enumeration is
required — every call to `getTenantIDNumeric`, `tenantForBlob`,
`s3TenantFromRequest`, every handler reading a `tenant`/`tenant_id` body or query
field, and every direct `tenant.*TableName(` constructed from request input. The
`Allows` check must be applied at **every** one of these, or the weakest
unguarded path defines the actual security posture.

Remaining specific unknowns:

- How SigV4 access-key identity (path 3) should bind to a grant — the S3 path has
  its own credential model that may not map cleanly onto JWT/API-key grants.
- Whether the timeseries body `tenant_id` (path 5) is ever authoritative over the
  URL param, which would be a body-controlled tenant selection like path 4.
- Whether any admin/export/GC endpoint resolves a tenant from a parameter.

---

## 9. As-built notes

The feature was implemented as designed, with two deliberate refinements decided
during implementation:

### 9.1 Two string fields, not one renamed enum

§8.2 leaned toward collapsing `TenantMode` + `TenantAuthMode` into a single
`TenancyMode` enum. Implementation kept them as two separate config strings —
`TenantMode` (`path`/`strict`, unchanged) and a new `TenantAuthMode`
(`open`/`scoped`) — because renaming `TenantMode` and its values would break every
existing deployment's config, violating principle #1 (nothing breaks). The
coherence the single enum would have provided is instead supplied by §9.2.

### 9.2 Internal representation is a bit-flag set (TenancyFlags)

The server does not branch on the mode strings directly. `Config.Tenancy()`
derives a `TenancyFlags` bitmask with two named bits:

- `TenantRequireRoute` — unprefixed / default-tenant-0 routes disabled.
- `TenantEnforceGrant` — per-identity tenant authorisation enforced.

The derivation bakes in the implication **TenantEnforceGrant ⇒
TenantRequireRoute**, so the incoherent "authorise the URL tenant while leaving
the tenant-0 routes open" state is not representable internally. This turned
several spec steps into structural consequences rather than separate guards:

- Step 4 (disable unprefixed routes under scoped): automatic — scoped sets
  `TenantRequireRoute`, which enables the existing strict-routing middleware.
- Step 7 (no auto-registration under scoped): automatic — scoped routes through
  the strict tenant-resolution branch, which only looks up pre-registered
  tenants; there is no `GetOrRegister` path to reach.
- Step 6 (S3): scoped implies `S3RequireAuth`, by the same pattern — the
  bucket-name fallback (an unauthenticated tenant selection) is refused.

The single genuinely new enforcement site is the grant check in
`tenantMiddleware` (step 5), which runs after tenant resolution and before the
store is created, failing closed on a missing or non-matching grant.

### 9.3 Path-inventory outcome (§8.6)

The five-path inventory was verified against the code. The blob handlers
(`tenantForBlob`) and timeseries handlers resolve tenant from the request
**context** (set by `tenantMiddleware`) or the URL parameter, not from request
bodies — so the single `tenantMiddleware` grant check governs them. The body
`tenant` / `tenant_id` JSON fields that prompted the §8.6 concern are metadata /
response echoes, not resolution inputs. The S3 path is the one separate
resolver, handled in step 6.

### 9.4 As-built grant semantics

- JWT: `tenant_admin: true` → admin grant; `tenants: [...]` → explicit set;
  absent → empty grant (fail-closed under scoped).
- API key: matched against `APIKeyGrants` (carries the grant); a key present only
  in the flat `APIKeys` list authenticates but carries an empty grant and is
  therefore rejected on tenant routes under scoped.
- Bearer token: always an admin grant (the trusted-gateway credential).

### 9.5 Coverage

Adversarial tests (`tenant_scoped_auth_test.go`) prove, through the real router:
cross-tenant requests are 403; multi-tenant and admin tokens reach their tenants;
grantless tokens are 403 everywhere; wrong-secret tokens are 401; and config
validation rejects scoped+path. Grant logic and flag derivation have their own
unit tests in `pkg/middleware` and `pkg/config`.

---

## 10. Follow-up work completed (post-0.11.0)

### 10.1 Claim names ratified (D3)

The first-cut claim names were confirmed as final: JWT `tenants` (array) and
`tenant_admin` (bool); no namespacing. `tenant_admin` is the sole wildcard
mechanism — a literal `"*"` in `tenants` is matched as an ordinary tenant name,
not a wildcard, so there is no second admin path. No code change.

### 10.2 Non-JWT grant paths covered (D1)

Adversarial cross-tenant tests added for the API-key and bearer paths
(`tenant_scoped_nonjwt_test.go`): an API key scoped to one tenant is 403 on
another; a flat `APIKeys` key with no grant is 403 under scoped; an admin key
reaches any tenant; the bearer token reaches any tenant and a wrong token is 401.

### 10.3 S3 grant mapping (D2)

Added `S3KeyGrant` (access_key + secret + tenants/admin) and the `S3KeyGrants`
config list. Under scoped, the S3 gateway now requires the request's access key
to have a configured grant authorising the requested bucket; the access-key
string is no longer trusted as the tenant name, and the bucket-name fallback is
refused. Tests in `tenant_scoped_s3_test.go` cover cross-bucket denial,
unknown-key denial, no-auth denial, and admin reach.

**Known limitation:** the access key is verified to be *known and authorising*,
but the request's SigV4 signature is not yet validated against
`S3KeyGrant.Secret`. Full signature validation (canonical request
reconstruction + HMAC chain) is a separate increment. Until then, a caller who
knows a valid access-key string can present it; what scoped adds over the prior
behaviour is that only configured keys with an authorising grant are accepted,
and the bucket name is never blindly trusted as the tenant.
