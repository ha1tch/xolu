// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Adversarial tests for scoped tenant authorization on the non-JWT auth types
// (API key and bearer token). The JWT path is covered in
// tenant_scoped_auth_test.go; this file gives the API-key grant path and the
// bearer-token (admin) path equivalent cross-tenant coverage.
//
// See docs/proposals/tenant-access-control.md §8.5 step 8 and §9.4.

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/storage"

	"github.com/rs/zerolog"
)

// newScopedServerCfg builds a strict+scoped server with tenants alpha(1) and
// beta(2), letting the caller set auth-type-specific config (API keys, grants,
// internal token) via the mutator.
func newScopedServerCfg(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "scoped_nonjwt.db")
	baseStore, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type: "sqlite", DBPath: dbPath, TenantID: 0,
	})
	if err != nil {
		t.Fatalf("base store: %v", err)
	}
	t.Cleanup(func() { baseStore.Close() })

	cfg := config.Default()
	cfg.StorageType = "sqlite"
	cfg.BaseDir = filepath.Dir(dbPath)
	cfg.GraphEnabled = false
	cfg.TenantMode = "strict"
	cfg.TenantAuthMode = "scoped"
	cfg.MaxEntitySize = 1 << 20
	cfg.DefaultPageSize = 100
	mutate(cfg)

	if errs, _ := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("config should be valid, got: %v", errs)
	}

	s := New(cfg, baseStore, &noopCache{}, nil, &noopValidator{}, zerolog.Nop())
	s.tenantRegistry.Register(context.Background(), "alpha", 1)
	s.tenantRegistry.Register(context.Background(), "beta", 2)
	return s
}

func doKeyRequest(t *testing.T, s *Server, tenant, apiKey, bearer string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/"+tenant+"/users", nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	return rr.Code
}

// An API key granted to one tenant is allowed there and 403 on another.
func TestScopedAPIKey_CrossTenantForbidden(t *testing.T) {
	const alphaKey = "key-alpha-0000000000000000000000"
	const betaKey = "key-beta-00000000000000000000000"
	s := newScopedServerCfg(t, func(c *config.Config) {
		c.AuthType = "apikey"
		c.APIKeys = []string{alphaKey, betaKey} // authenticate
		c.APIKeyGrants = []config.APIKeyGrant{
			{Key: alphaKey, Tenants: []string{"alpha"}},
			{Key: betaKey, Tenants: []string{"beta"}},
		}
	})

	if code := doKeyRequest(t, s, "alpha", alphaKey, ""); code == http.StatusForbidden {
		t.Errorf("alpha key blocked on alpha: %d", code)
	}
	if code := doKeyRequest(t, s, "beta", alphaKey, ""); code != http.StatusForbidden {
		t.Errorf("alpha key on beta: want 403, got %d", code)
	}
	if code := doKeyRequest(t, s, "beta", betaKey, ""); code == http.StatusForbidden {
		t.Errorf("beta key blocked on beta: %d", code)
	}
	if code := doKeyRequest(t, s, "alpha", betaKey, ""); code != http.StatusForbidden {
		t.Errorf("beta key on alpha: want 403, got %d", code)
	}
}

// A flat API key (in APIKeys but with no APIKeyGrants entry) authenticates but
// carries an empty grant, so it is 403 on every tenant under scoped mode.
func TestScopedAPIKey_FlatKeyNoGrantForbidden(t *testing.T) {
	const flatKey = "flat-key-000000000000000000000000"
	s := newScopedServerCfg(t, func(c *config.Config) {
		c.AuthType = "apikey"
		c.APIKeys = []string{flatKey} // valid for auth, but no grant
	})
	for _, tn := range []string{"alpha", "beta"} {
		if code := doKeyRequest(t, s, tn, flatKey, ""); code != http.StatusForbidden {
			t.Errorf("flat (ungranted) key on %s: want 403, got %d", tn, code)
		}
	}
}

// An admin API key (Admin: true) reaches any tenant.
func TestScopedAPIKey_AdminKey(t *testing.T) {
	const adminKey = "admin-key-00000000000000000000000"
	s := newScopedServerCfg(t, func(c *config.Config) {
		c.AuthType = "apikey"
		c.APIKeys = []string{adminKey}
		c.APIKeyGrants = []config.APIKeyGrant{{Key: adminKey, Admin: true}}
	})
	for _, tn := range []string{"alpha", "beta"} {
		if code := doKeyRequest(t, s, tn, adminKey, ""); code == http.StatusForbidden {
			t.Errorf("admin key blocked on %s: %d", tn, code)
		}
	}
}

// The bearer token is treated as admin (the trusted-gateway credential): it
// reaches any tenant.
func TestScopedBearer_ReachesAnyTenant(t *testing.T) {
	const token = "internal-token-0000000000000000000"
	s := newScopedServerCfg(t, func(c *config.Config) {
		c.AuthType = "bearertoken"
		c.InternalToken = token
	})
	for _, tn := range []string{"alpha", "beta"} {
		if code := doKeyRequest(t, s, tn, "", token); code == http.StatusForbidden {
			t.Errorf("bearer token blocked on %s (should be admin): %d", tn, code)
		}
	}
	// A wrong bearer token is rejected at auth (401), never reaching the grant.
	if code := doKeyRequest(t, s, "alpha", "", "wrong-token"); code != http.StatusUnauthorized {
		t.Errorf("wrong bearer token: want 401, got %d", code)
	}
}
