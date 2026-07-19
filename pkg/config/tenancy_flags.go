// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package config

// TenancyFlags is the internal, bit-flag representation of the tenancy operating
// mode. The user-facing configuration surface remains the two named string
// fields (TenantMode = "path"|"strict", TenantAuthMode = "open"|"scoped"); this
// type is what the server consumes, so call sites express intent
// ("flags.Has(TenantRequireRoute)") instead of re-deriving meaning from string
// comparisons.
//
// The two concerns are genuinely independent properties, not points on one axis:
//
//	TenantRequireRoute  — unprefixed / default-tenant-0 routes are disabled;
//	                      all entity access must go through /tenant/{id}/.
//	                      (Set by TenantMode "strict".)
//	TenantEnforceGrant  — the caller's identity must authorise the requested
//	                      tenant; otherwise 403. (Set by TenantAuthMode "scoped".)
//
// Representing them as flags makes the design's coherence rule structural rather
// than a validation afterthought: TenantEnforceGrant *implies*
// TenantRequireRoute (you cannot enforce per-tenant authorisation while leaving
// the unauthenticated tenant-0 routes open), so the implication is baked into the
// derivation below and the incoherent combination is not representable.
type TenancyFlags uint8

const (
	// TenantRequireRoute disables the unprefixed default-tenant routes.
	TenantRequireRoute TenancyFlags = 1 << iota
	// TenantEnforceGrant enforces per-identity tenant authorisation.
	TenantEnforceGrant
)

// Has reports whether all bits in f are set.
func (t TenancyFlags) Has(f TenancyFlags) bool { return t&f == f }

// Tenancy derives the TenancyFlags from the configured mode strings. The
// implication TenantEnforceGrant ⇒ TenantRequireRoute is applied here, so a
// caller never has to remember it. (Config validation independently rejects the
// scoped+path string combination with a helpful error; this derivation is the
// structural backstop.)
func (c *Config) Tenancy() TenancyFlags {
	var f TenancyFlags
	if c.TenantMode == "strict" {
		f |= TenantRequireRoute
	}
	if c.TenantAuthMode == "scoped" {
		f |= TenantEnforceGrant
		f |= TenantRequireRoute // scoped implies strict routing
	}
	return f
}
