# Tenant Access Control — Operations Guide

Status: Companion to `tenant-access-control.md` (design)
Audience: operators deploying and credentialing an xolu server
Assumes: the tenant access-control spec is implemented (single `TenancyMode`
setting with values `open` / `strict` / `scoped`, and JWT/API-key tenant grants).

This guide explains how to choose an operating mode and how to mint credentials
for each. For *why* the modes exist and how the authorization is enforced, see the
design document.

---

## 1. Choosing a mode

| Deployment shape                                          | `TenancyMode` | Auth type            |
|----------------------------------------------------------|---------------|----------------------|
| Single-tenant edge / embedded / sole-client backend      | `open`        | any (or `none`)      |
| Trusted gateway in front of xolu (gateway does its own authz) | `open` or `strict` | `bearertoken`   |
| Multiple tenants, callers all trusted (internal platform) | `strict`      | any                  |
| Multiple **mutually-distrusting** tenants on one endpoint | `scoped`      | `jwt` (or scoped API keys) |

The one rule to internalise: **`scoped` is the only mode that isolates
mutually-distrusting callers, and it is the wrong choice for everything else.**
It forces all traffic through `/tenant/{id}/` routes and rejects the unprefixed
single-tenant API. Do not enable it reflexively because it sounds "more secure" —
on a single-tenant box it breaks the normal API for zero benefit.

`open` is the default. A single-tenant deployment needs nothing from this guide
beyond §2.

## 2. `open` mode — single-tenant and trusted deployments

No tenant authority is carried in credentials. Authentication is a gate: a valid
credential reaches any tenant. Mint tokens exactly as you would for a
single-tenant server.

### 2.1 `AuthType: none`

No credential required. Appropriate only for a closed network or local edge box.
Nothing to mint.

### 2.2 `AuthType: apikey`

Configure one or more keys (`XOLU_API_KEYS`, comma-separated, or `APIKeys` in
config). A client presents one of:

```
X-API-Key: <key>
Authorization: ApiKey <key>
?api_key=<key>          # query fallback; avoid in logs-sensitive contexts
```

To "mint" a key, generate a high-entropy random string and add it to the
configured set:

```
openssl rand -hex 32
```

Rotate by adding the new key, deploying, moving clients over, then removing the
old key.

### 2.3 `AuthType: bearertoken`

A single shared secret (`InternalToken`). This is the trusted-gateway credential:
the gateway authenticates end users itself and calls xolu with the one token. The
client presents:

```
Authorization: Bearer <InternalToken>
```

Generate it the same way (`openssl rand -hex 32`). There is one such token for the
whole server; it carries no identity and, in `scoped` mode, is treated as full
authority (see §4.3).

### 2.4 `AuthType: jwt`

Even in `open` mode, JWT works — the token just needs to validate; no tenant claim
is consulted. See §3 for the minting mechanics, which are identical except that
the `tenants` grant claim is ignored in `open` mode.

## 3. `scoped` mode — minting JWTs with tenant grants

`scoped` requires `TenancyMode: strict` (config rejects `scoped` without it). JWT
is the recommended auth type because the tenant grant is carried *inside the
signed token*, so it cannot be altered by the caller.

### 3.1 Token mechanics (what the server enforces)

The xolu JWT validator is deliberately small. A token MUST satisfy all of:

- **Algorithm `HS256`.** The `alg` header must be exactly `HS256`; anything else
  is rejected. (No `none`, no RS/ES.)
- **Signature** over `base64url(header).base64url(payload)` using the server's
  `JWTSecret` as the HMAC-SHA256 key.
- **`exp` present and in the future.** A token with no `exp`, or an unparseable
  `exp`, is rejected (policy D-002 — no immortal tokens). `exp` may be a JSON
  number or a numeric string; both are accepted.
- **`iss` matches** `JWTIssuer` *if* the server has one configured; ignored if
  not.
- **`nbf`**, if present, must be in the past; absent `nbf` means valid from
  issuance.

Beyond those, `scoped` mode reads the **grant claim**:

- `tenants`: array of tenant names the token may act on, e.g.
  `["acme", "globex"]`; or
- `tenant_admin: true`: a token authorised for **any** tenant (the administrative
  / ops credential).

A request to `/tenant/<name>/...` is allowed iff `<name>` is in `tenants` or
`tenant_admin` is true. Anything else is `403`.

### 3.2 Symmetric-secret consequence (read this)

xolu uses **HS256 — a symmetric secret**. The same `JWTSecret` that validates
tokens also mints them. There is no public/private split. Operationally:

- Whoever holds `JWTSecret` can mint a token for **any** tenant, including
  `tenant_admin`. Treat the secret as a tenant-isolation root key.
- The minting service and the xolu server share this secret. Keep minting on a
  trusted component (an issuer service, a CI secret, an ops workstation) — never
  ship `JWTSecret` to clients.
- Rotation invalidates every outstanding token at once. Plan for short `exp`
  lifetimes so rotation is cheap, rather than long-lived tokens.

If you need issuer/verifier separation (mint with a private key, verify with a
public key), note that xolu uses HS256 only and has no asymmetric option: the
minting component must therefore be as trusted as the server, since both hold the
same secret.

### 3.3 Minting a single-tenant token

A token for a tenant `acme`, valid one hour, issuer `oldbytes`:

Header:

```json
{ "alg": "HS256", "typ": "JWT" }
```

Payload:

```json
{
  "sub": "user-1234",
  "iss": "oldbytes",
  "tenants": ["acme"],
  "iat": 1750000000,
  "exp": 1750003600
}
```

Sign: `HMAC-SHA256(base64url(header) + "." + base64url(payload), JWTSecret)`,
then `token = base64url(header) + "." + base64url(payload) + "." + base64url(sig)`.

Reference one-liner (jose-style libraries are preferred over hand-rolling):

```bash
# Using a library is strongly recommended. Conceptually:
#   payload.exp   = now + 3600          # REQUIRED
#   payload.tenants = ["acme"]
#   sign HS256 with $JWT_SECRET
```

### 3.4 Minting a multi-tenant token

Same as §3.3 with more entries:

```json
{ "sub": "svc-reporting", "iss": "oldbytes",
  "tenants": ["acme", "globex", "initech"],
  "iat": 1750000000, "exp": 1750003600 }
```

### 3.5 Minting an admin / ops token

```json
{ "sub": "ops-jane", "iss": "oldbytes",
  "tenant_admin": true,
  "iat": 1750000000, "exp": 1750000900 }
```

Give admin tokens the **shortest** lifetime you can tolerate (minutes, not
hours). They are the highest-value credential in `scoped` mode.

### 3.6 Presenting the token

```
Authorization: Bearer <token>
```

Against a tenant route:

```
GET /api/v1/tenant/acme/users   →  200 if "acme" ∈ tenants (or tenant_admin)
GET /api/v1/tenant/globex/users →  403 if "globex" ∉ tenants and not admin
```

## 4. `scoped` mode — non-JWT auth types

### 4.1 Scoped API keys

API keys carry no claims, so the key→tenant grant lives in config:

```yaml
APIKeyGrants:
  - key: "<key-acme>"
    tenants: ["acme"]
  - key: "<key-ops>"
    tenant_admin: true
```

The client presents the key exactly as in §2.2; the server resolves the grant
from config. A bare key in the legacy flat `APIKeys` list is **rejected** under
`scoped` (it has no grant) — migrate flat keys into `APIKeyGrants` before enabling
`scoped`. Mint a key the same way (`openssl rand -hex 32`) and assign its tenants
in config.

### 4.2 No tenant claim / unknown key

A `scoped` request whose credential resolves to an empty grant is `403` on every
tenant route. There is no implicit default tenant in `scoped` mode.

### 4.3 Bearer token under `scoped`

The single `InternalToken` is treated as `tenant_admin` (full authority). This is
intentional: it is the trusted-gateway credential, and the gateway is responsible
for its own per-user authorization before calling xolu. **Do not** use the bearer
token as a tenant-isolation mechanism — it isolates nothing. If end-user
isolation matters at the xolu layer, use `scoped` JWTs instead.

## 5. The S3 / blob gateway (separate surface)

The S3-compatible endpoint runs on its own port and resolves tenant from the
SigV4 access-key identity (or bucket name). It is **not** governed by
`TenancyMode`. Under any `scoped` deployment:

- Set `S3RequireAuth: true`. Without it, the bucket name is used as the tenant —
  an unauthenticated tenant-selection path that defeats `scoped` entirely.
- Each S3 access key maps to a tenant; treat the access-key→tenant mapping with
  the same care as `APIKeyGrants`.

If you do not use the S3 endpoint, disable it (`S3Enabled: false`) so it is not an
unguarded surface.

## 6. Operational checklist before enabling `scoped`

- [ ] `TenancyMode: strict` set (config rejects `scoped` otherwise).
- [ ] `JWTSecret` is high-entropy and held only by the server and the minting
      component.
- [ ] Token `exp` lifetimes are short; admin tokens shortest of all.
- [ ] `JWTIssuer` configured and tokens carry a matching `iss`.
- [ ] All flat `APIKeys` migrated to `APIKeyGrants` (or removed).
- [ ] `TenantAutoRegister` is off or admin-only (a non-admin must not conjure a
      tenant it is then "authorised" for).
- [ ] S3 endpoint either disabled or `S3RequireAuth: true` with per-key tenant
      mapping.
- [ ] A test token for tenant A is verified to receive `403` on tenant B's
      routes across every endpoint family (crud, graph, ts, blob).

## 7. Quick reference: claims

| Claim          | Required          | Meaning                                            |
|----------------|-------------------|----------------------------------------------------|
| `alg` (header) | yes (`HS256`)     | Only HS256 accepted.                                |
| `exp`          | **yes**           | Expiry; absent or unparseable → token rejected.    |
| `sub`          | recommended       | Subject; used for logging/identity.                |
| `iss`          | if server configured | Must match `JWTIssuer` when one is set.         |
| `nbf`          | optional          | Not-before; if present, must be in the past.       |
| `tenants`      | for scoped grants | Array of tenant names the token may act on.        |
| `tenant_admin` | optional          | `true` → authorised for any tenant.                |
