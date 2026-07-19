// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package authconfig holds the authentication configuration subset of the
// xolu server config, extracted (T-19) so external binaries — the molu hub
// in particular — can import xolu's auth middleware without importing the
// full pkg/config surface.
//
// The xolu server constructs this from its full configuration at startup
// via config.(*Config).AuthConfig(); external consumers populate it
// directly. The field set is the exact read-set of pkg/authmw/auth.go:
// nothing more is included, so the import carries no unrelated server
// concerns.
package authconfig

// APIKeyGrant binds a single API key to the tenants it may act on under
// TenantAuthMode "scoped". Exactly one of Tenants or Admin should be set.
//
// Moved here from pkg/config (which retains a type alias for
// compatibility) as part of the T-19 auth extraction.
type APIKeyGrant struct {
	Key     string   // the API key value
	Tenants []string // tenant names this key may act on
	Admin   bool     // true → authorised for any tenant
}

// Config is the authentication configuration consumed by
// pkg/authmw.AuthMiddleware and its validators.
type Config struct {
	AuthType         string        // "none", "jwt", "apikey", "bearertoken"
	JWTSecret        string        // Secret for JWT validation (HS256)
	JWTIssuer        string        // Expected issuer claim
	APIKeys          []string      // Valid API keys
	APIKeyGrants     []APIKeyGrant // Per-key tenant grants under "scoped" mode
	InternalToken    string        // Shared secret for the "bearertoken" type
	AuthExcludePaths []string      // Path prefixes excluded from auth
}
