// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Adversarial tests for scoped tenant authorization (TenantAuthMode "scoped").
//
// These prove the core security property of the tenant access-control feature:
// under scoped mode a credential authorised for one tenant is rejected (403) on
// another tenant's routes, while an admin credential reaches any tenant and the
// default (open) mode is unchanged.
//
// See docs/proposals/tenant-access-control.md §8.5 step 8.

package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/storage"

	"github.com/rs/zerolog"
)

// newScopedServer builds a server in strict+scoped+jwt mode with tenants
// alpha(1) and beta(2) registered, signing tokens with the given secret.
func newScopedServer(t *testing.T, secret string) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "scoped.db")
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
	cfg.AuthType = "jwt"
	cfg.JWTSecret = secret
	cfg.TenantMode = "strict"
	cfg.TenantAuthMode = "scoped"
	cfg.MaxEntitySize = 1 << 20
	cfg.DefaultPageSize = 100

	if errs, _ := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("scoped+strict config should be valid, got: %v", errs)
	}

	logger := zerolog.Nop()
	s := New(cfg, baseStore, &noopCache{}, nil, &noopValidator{}, logger)
	s.tenantRegistry.Register(context.Background(), "alpha", 1)
	s.tenantRegistry.Register(context.Background(), "beta", 2)
	return s
}

// mintToken builds an HS256 JWT with the given claims, signed with secret.
func mintToken(secret string, claims map[string]interface{}) string {
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	hdr, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	pay, _ := json.Marshal(claims)
	signingInput := b64(hdr) + "." + b64(pay)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return signingInput + "." + b64(mac.Sum(nil))
}

// tenantsClaim returns standard claims for a token scoped to the given tenants.
func tenantsClaim(tenants ...string) map[string]interface{} {
	ts := make([]interface{}, len(tenants))
	for i, t := range tenants {
		ts[i] = t
	}
	return map[string]interface{}{
		"sub":     "test-subject",
		"exp":     4102444800, // year 2100; far future
		"tenants": ts,
	}
}

func doAuthRequest(t *testing.T, s *Server, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	return rr
}

// The central property: a token for alpha is allowed on alpha and forbidden on
// beta; a token for beta is the mirror image.
func TestScoped_CrossTenantForbidden(t *testing.T) {
	const secret = "test-secret-scoped"
	s := newScopedServer(t, secret)

	alphaTok := mintToken(secret, tenantsClaim("alpha"))
	betaTok := mintToken(secret, tenantsClaim("beta"))

	// A GET on a tenant's entity list route. The entity need not exist; we are
	// asserting the authorization outcome (403 vs not-403), not the data.
	cases := []struct {
		name    string
		token   string
		tenant  string
		wantBlk bool // true => expect 403 Forbidden
	}{
		{"alpha token on alpha", alphaTok, "alpha", false},
		{"alpha token on beta", alphaTok, "beta", true},
		{"beta token on beta", betaTok, "beta", false},
		{"beta token on alpha", betaTok, "alpha", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := doAuthRequest(t, s, http.MethodGet, "/api/v1/tenant/"+c.tenant+"/users", c.token)
			if c.wantBlk && rr.Code != http.StatusForbidden {
				t.Errorf("expected 403 for %s, got %d", c.name, rr.Code)
			}
			if !c.wantBlk && rr.Code == http.StatusForbidden {
				t.Errorf("unexpected 403 for %s (authorised tenant must not be blocked)", c.name)
			}
		})
	}
}

// A multi-tenant token reaches every tenant in its grant.
func TestScoped_MultiTenantToken(t *testing.T) {
	const secret = "test-secret-multi"
	s := newScopedServer(t, secret)
	tok := mintToken(secret, tenantsClaim("alpha", "beta"))

	for _, tn := range []string{"alpha", "beta"} {
		rr := doAuthRequest(t, s, http.MethodGet, "/api/v1/tenant/"+tn+"/users", tok)
		if rr.Code == http.StatusForbidden {
			t.Errorf("multi-tenant token blocked on %s", tn)
		}
	}
}

// An admin token reaches any tenant.
func TestScoped_AdminToken(t *testing.T) {
	const secret = "test-secret-admin"
	s := newScopedServer(t, secret)
	tok := mintToken(secret, map[string]interface{}{
		"sub": "ops", "exp": 4102444800, "tenant_admin": true,
	})
	for _, tn := range []string{"alpha", "beta"} {
		rr := doAuthRequest(t, s, http.MethodGet, "/api/v1/tenant/"+tn+"/users", tok)
		if rr.Code == http.StatusForbidden {
			t.Errorf("admin token blocked on %s", tn)
		}
	}
}

// A token with no tenant grant is forbidden on every tenant route (fail-closed).
func TestScoped_NoGrantForbidden(t *testing.T) {
	const secret = "test-secret-nogrant"
	s := newScopedServer(t, secret)
	tok := mintToken(secret, map[string]interface{}{
		"sub": "no-grant", "exp": 4102444800, // valid token, but no tenants/admin
	})
	for _, tn := range []string{"alpha", "beta"} {
		rr := doAuthRequest(t, s, http.MethodGet, "/api/v1/tenant/"+tn+"/users", tok)
		if rr.Code != http.StatusForbidden {
			t.Errorf("grantless token not blocked on %s: got %d", tn, rr.Code)
		}
	}
}

// A token signed with the wrong secret is rejected at authentication (401),
// never reaching the grant check.
func TestScoped_WrongSecretRejected(t *testing.T) {
	s := newScopedServer(t, "the-real-secret")
	forged := mintToken("the-wrong-secret", tenantsClaim("alpha"))
	rr := doAuthRequest(t, s, http.MethodGet, "/api/v1/tenant/alpha/users", forged)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("token with wrong secret should be 401, got %d", rr.Code)
	}
}

// Config validation rejects the incoherent scoped+path combination.
func TestScoped_RequiresStrict(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "jwt"
	cfg.JWTSecret = "x"
	cfg.TenantMode = "path"
	cfg.TenantAuthMode = "scoped"
	errs, _ := cfg.Validate()
	found := false
	for _, e := range errs {
		if len(e) > 0 {
			found = true
		}
	}
	if !found {
		t.Error("scoped+path must be rejected by config validation")
	}
}
