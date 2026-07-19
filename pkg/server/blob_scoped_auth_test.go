// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Parity tests for the native blob plane under scoped tenant authorization.
//
// These prove that the JSON blob API (/api/v1/...) enforces the same
// per-identity tenant grant invariant the rest of the v1 surface and the S3
// plane already enforce: under TenantEnforceGrant a credential authorised for
// one tenant is rejected (403 XOLU-TN003) on another tenant's blob routes, while
// an admin credential reaches any tenant and open mode is unchanged.
//
// Authentication (producing the grant) is performed upstream by AuthMiddleware;
// the authorisation decision is shared with the normal v1 routes via
// Server.authoriseTenantGrant. The reused harness (newScopedServer, mintToken,
// tenantsClaim) lives in tenant_scoped_auth_test.go.

package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/storage"

	"github.com/rs/zerolog"
)

// newScopedBlobServer builds a strict+scoped+jwt server with the native blob
// plane enabled and tenants alpha(1)/beta(2) registered. It mirrors
// newScopedServer but turns blobs on.
func newScopedBlobServer(t *testing.T, secret string) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "scoped-blob.db")
	baseStore, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type: "sqlite", DBPath: dbPath, TenantID: 0,
	})
	if err != nil {
		t.Fatalf("base store: %v", err)
	}
	t.Cleanup(func() { baseStore.Close() })

	cfg := config.Default()
	cfg.StorageType = "sqlite"
	cfg.BaseDir = tmpDir
	cfg.GraphEnabled = false
	cfg.AuthType = "jwt"
	cfg.JWTSecret = secret
	cfg.TenantMode = "strict"
	cfg.TenantAuthMode = "scoped"
	cfg.MaxEntitySize = 1 << 20
	cfg.DefaultPageSize = 100

	cfg.BlobEnabled = true
	cfg.BlobMaxSize = 1 << 20
	cfg.BlobUsageSampleIntervalSecs = 0

	if errs, _ := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("scoped+strict+blob config should be valid, got: %v", errs)
	}

	logger := zerolog.Nop()
	s := New(cfg, baseStore, &noopCache{}, nil, &noopValidator{}, logger)
	s.tenantRegistry.Register(context.Background(), "alpha", 1)
	s.tenantRegistry.Register(context.Background(), "beta", 2)
	return s
}

// doBlobRequest issues an authenticated request with an optional body through
// the full router (so AuthMiddleware and tenantMiddleware run).
func doBlobRequest(t *testing.T, s *Server, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	return rr
}

// The central parity property: a token scoped to alpha may act on alpha's blob
// routes and is forbidden (403) on beta's, across every native blob verb.
func TestScopedBlob_CrossTenantForbidden(t *testing.T) {
	const secret = "test-secret-scoped-blob"
	s := newScopedBlobServer(t, secret)

	alphaTok := mintToken(secret, tenantsClaim("alpha"))

	// Seed a blob under alpha so GET/HEAD/DELETE have a real target on the
	// allowed side; the forbidden side must be blocked before it matters.
	put := doBlobRequest(t, s, "POST",
		"/api/v1/tenant/alpha/blob?key=secret.txt", alphaTok, []byte("alpha-only"))
	if put.Code != http.StatusCreated && put.Code != http.StatusOK {
		t.Fatalf("seed PUT under alpha: got %d, want 200/201", put.Code)
	}

	// Each native blob verb, alpha token aimed at beta's routes -> 403.
	type tc struct {
		name   string
		method string
		path   string
		body   []byte
	}
	forbidden := []tc{
		{"put", "POST", "/api/v1/tenant/beta/blob?key=x.txt", []byte("data")},
		{"get", "GET", "/api/v1/tenant/beta/blob/secret.txt", nil},
		{"head", "HEAD", "/api/v1/tenant/beta/blob/secret.txt", nil},
		{"delete", "DELETE", "/api/v1/tenant/beta/blob/secret.txt", nil},
		{"list", "GET", "/api/v1/tenant/beta/blob", nil},
		{"usage", "GET", "/api/v1/tenant/beta/blob/usage", nil},
	}
	for _, c := range forbidden {
		t.Run("forbidden_"+c.name, func(t *testing.T) {
			rr := doBlobRequest(t, s, c.method, c.path, alphaTok, c.body)
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s %s: got %d, want 403", c.method, c.path, rr.Code)
			}
		})
	}

	// The mirror: alpha token on alpha's own routes is NOT 403. (Exact 2xx/404
	// varies by verb and existence; the parity property is "not forbidden".)
	allowed := []tc{
		{"get", "GET", "/api/v1/tenant/alpha/blob/secret.txt", nil},
		{"head", "HEAD", "/api/v1/tenant/alpha/blob/secret.txt", nil},
		{"list", "GET", "/api/v1/tenant/alpha/blob", nil},
		{"usage", "GET", "/api/v1/tenant/alpha/blob/usage", nil},
	}
	for _, c := range allowed {
		t.Run("allowed_"+c.name, func(t *testing.T) {
			rr := doBlobRequest(t, s, c.method, c.path, alphaTok, c.body)
			if rr.Code == http.StatusForbidden {
				t.Errorf("%s %s: got 403, want not-forbidden for own tenant", c.method, c.path)
			}
		})
	}
}

// An admin grant (tenant_admin: true) reaches any tenant's blob routes.
func TestScopedBlob_AdminReachesAnyTenant(t *testing.T) {
	const secret = "test-secret-scoped-blob-admin"
	s := newScopedBlobServer(t, secret)

	adminClaims := map[string]interface{}{
		"sub":          "admin-subject",
		"exp":          4102444800,
		"tenant_admin": true,
	}
	adminTok := mintToken(secret, adminClaims)

	for _, tenant := range []string{"alpha", "beta"} {
		rr := doBlobRequest(t, s, "GET", "/api/v1/tenant/"+tenant+"/blob", adminTok, nil)
		if rr.Code == http.StatusForbidden {
			t.Errorf("admin LIST on %s: got 403, want reachable", tenant)
		}
	}
}

// A credential carrying no grant (ungranted) authorises nothing under scoped
// mode: blob routes fail closed with 403.
func TestScopedBlob_UngrantedForbidden(t *testing.T) {
	const secret = "test-secret-scoped-blob-ungranted"
	s := newScopedBlobServer(t, secret)

	// Valid signature, valid exp, but no tenants and no tenant_admin.
	ungranted := mintToken(secret, map[string]interface{}{
		"sub": "no-grant",
		"exp": 4102444800,
	})
	rr := doBlobRequest(t, s, "GET", "/api/v1/tenant/alpha/blob", ungranted, nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("ungranted LIST on alpha: got %d, want 403", rr.Code)
	}
}
