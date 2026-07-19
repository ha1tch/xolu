// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package authmw

import "context"

// TenantGrant represents the tenant authority carried by an authenticated
// identity under TenantAuthMode "scoped". It is the single source of the
// authorization decision: a request for a tenant is permitted iff the caller's
// grant Allows it.
//
// A grant is produced by the auth layer from whatever the credential expresses:
//   - JWT: the "tenants" claim (→ Tenants) or "tenant_admin": true (→ Admin)
//   - API key: the matching APIKeyGrant in config
//   - bearer token: always Admin (the trusted-gateway credential)
//
// The zero value is an empty grant that authorises nothing — fail-closed.
type TenantGrant struct {
	// Admin authorises any tenant. Set for tenant_admin tokens, admin API keys,
	// and the bearer token.
	Admin bool
	// Tenants is the explicit set of tenant names this identity may act on.
	// Ignored when Admin is true.
	Tenants []string
}

// Allows reports whether this grant authorises the named tenant. An admin grant
// allows any tenant; otherwise the name must be an exact member of Tenants. An
// empty, non-admin grant allows nothing.
func (g TenantGrant) Allows(tenantName string) bool {
	if g.Admin {
		return true
	}
	for _, t := range g.Tenants {
		if t == tenantName {
			return true
		}
	}
	return false
}

// IsEmpty reports whether the grant authorises nothing (no admin, no tenants).
// Used to reject ungranted credentials under scoped mode before tenant
// resolution.
func (g TenantGrant) IsEmpty() bool {
	return !g.Admin && len(g.Tenants) == 0
}

// ContextKeyTenantGrant is the context key under which the auth layer stores the
// resolved TenantGrant for the request.
const ContextKeyTenantGrant ContextKey = "tenant_grant"

// TenantGrantFromContext returns the TenantGrant placed in the context by the
// auth middleware. The second return is false if no grant is present (e.g. auth
// is disabled, or the request did not pass through AuthMiddleware).
func TenantGrantFromContext(ctx context.Context) (TenantGrant, bool) {
	v := ctx.Value(ContextKeyTenantGrant)
	if v == nil {
		return TenantGrant{}, false
	}
	g, ok := v.(TenantGrant)
	return g, ok
}
