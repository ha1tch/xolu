// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Regression tests binding otogen's output to the server's auth infrastructure.
// The central guarantee: a JWT minted by otogen is accepted by the same
// middleware validator the server uses, with the tenant grant otogen intended.
// This catches drift between the credential generator and the validator.

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/middleware"
	"github.com/ha1tch/xolu/pkg/s3sig"
)

const testSecret = "otogen-regression-secret-00000000"

// runThroughAuth pushes a bearer token through the real AuthMiddleware in JWT
// mode and returns the status plus the resolved tenant grant (if any).
func runThroughAuth(t *testing.T, token string) (int, middleware.TenantGrant, bool) {
	t.Helper()
	cfg := config.Default()
	cfg.AuthType = "jwt"
	cfg.JWTSecret = testSecret

	var gotGrant middleware.TenantGrant
	var gotOK bool
	handler := middleware.AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotGrant, gotOK = middleware.TenantGrantFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, gotGrant, gotOK
}

// A scoped (tenants) token minted by otogen is accepted and carries the grant.
func TestOtogenJWT_ScopedTokenAccepted(t *testing.T) {
	claims := map[string]interface{}{
		"sub":     "user-1",
		"exp":     4102444800,
		"tenants": []string{"acme", "globex"},
	}
	token, err := signHS256(claims, testSecret)
	if err != nil {
		t.Fatalf("signHS256: %v", err)
	}
	code, grant, ok := runThroughAuth(t, token)
	if code != http.StatusOK {
		t.Fatalf("otogen token rejected: %d", code)
	}
	if !ok {
		t.Fatal("no tenant grant in context")
	}
	if grant.Admin {
		t.Error("scoped token should not be admin")
	}
	if !grant.Allows("acme") || !grant.Allows("globex") {
		t.Errorf("grant missing tenants: %+v", grant)
	}
	if grant.Allows("evil") {
		t.Error("grant authorised an ungranted tenant")
	}
}

// An admin token minted by otogen is accepted and carries an admin grant.
func TestOtogenJWT_AdminTokenAccepted(t *testing.T) {
	claims := map[string]interface{}{
		"sub":          "ops",
		"exp":          4102444800,
		"tenant_admin": true,
	}
	token, err := signHS256(claims, testSecret)
	if err != nil {
		t.Fatalf("signHS256: %v", err)
	}
	code, grant, ok := runThroughAuth(t, token)
	if code != http.StatusOK {
		t.Fatalf("otogen admin token rejected: %d", code)
	}
	if !ok || !grant.Admin {
		t.Errorf("expected admin grant, got %+v (ok=%v)", grant, ok)
	}
	if !grant.Allows("any-tenant-name") {
		t.Error("admin grant should allow any tenant")
	}
}

// A token signed with the wrong secret is rejected (guards the signing path).
func TestOtogenJWT_WrongSecretRejected(t *testing.T) {
	claims := map[string]interface{}{"sub": "x", "exp": 4102444800, "tenant_admin": true}
	token, err := signHS256(claims, "a-different-secret")
	if err != nil {
		t.Fatalf("signHS256: %v", err)
	}
	if code, _, _ := runThroughAuth(t, token); code != http.StatusUnauthorized {
		t.Errorf("wrong-secret token: expected 401, got %d", code)
	}
}

// s3sign must produce a signature that verifies with the same secret via the
// shared s3sig package — i.e. the exact parameters cmdS3Sign uses (signed
// headers, payload hash, canonical URI) round-trip. This locks the command's
// signing contract against the server's verifier.
func TestS3Sign_RoundTrips(t *testing.T) {
	const (
		secret    = "s3sign-roundtrip-secret"
		accessKey = "AKIAALPHA"
		host      = "localhost:9091"
		amzDate   = "20260101T000000Z"
		yyyymmdd  = "20260101"
		payload   = "UNSIGNED-PAYLOAD"
	)
	comp := s3sig.Components{
		AccessKey: accessKey,
		Date:      yyyymmdd,
		Region:    "us-east-1",
		Service:   "s3",
		Method:    "GET",
		CanonURI:  "/acme",
		Headers: map[string]string{
			"host":                 host,
			"x-amz-date":           amzDate,
			"x-amz-content-sha256": payload,
		},
		PayloadHash: payload,
		AmzDate:     amzDate,
	}
	signedHeaders := []string{"host", "x-amz-date", "x-amz-content-sha256"}
	auth := s3sig.Sign(secret, comp, signedHeaders)

	// Re-parse and verify, as the server does.
	parsed, err := s3sig.ParseAuthorization(auth)
	if err != nil {
		t.Fatalf("ParseAuthorization: %v", err)
	}
	parsed.Method = comp.Method
	parsed.CanonURI = comp.CanonURI
	parsed.Headers = comp.Headers
	parsed.PayloadHash = comp.PayloadHash
	parsed.AmzDate = comp.AmzDate
	if err := parsed.Verify(secret); err != nil {
		t.Errorf("s3sign signature did not verify: %v", err)
	}
	if err := parsed.Verify("wrong"); err == nil {
		t.Error("verified with wrong secret")
	}
}

func TestHostFromEndpoint(t *testing.T) {
	cases := map[string]string{
		"http://localhost:9091":     "localhost:9091",
		"https://s3.example.com":    "s3.example.com",
		"http://10.0.0.1:9091/path": "10.0.0.1:9091",
	}
	for in, want := range cases {
		got, err := hostFromEndpoint(in)
		if err != nil || got != want {
			t.Errorf("hostFromEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := hostFromEndpoint("://bad"); err == nil {
		t.Error("expected error for malformed endpoint")
	}
}

func TestParseFormat(t *testing.T) {
	for _, f := range []string{"text", "yaml", "json", "csv"} {
		if got := parseFormat(f); string(got) != f {
			t.Errorf("parseFormat(%q) = %q", f, got)
		}
	}
}

// orderedFields drives json/csv; verify the columns per kind.
func TestOrderedFields(t *testing.T) {
	cases := []struct {
		rec      credRecord
		wantCols []string
	}{
		{
			credRecord{Kind: "s3key", AccessKey: "AK", Secret: "S", Tenants: []string{"acme"}},
			[]string{"kind", "access_key", "secret", "tenant_admin", "tenants"},
		},
		{
			credRecord{Kind: "apikey", Key: "K", Admin: true},
			[]string{"kind", "key", "tenant_admin", "tenants"},
		},
		{
			credRecord{Kind: "jwt", Token: "T", Tenants: []string{"acme"}},
			[]string{"kind", "token", "tenant_admin", "tenants"},
		},
		{
			// bearer must not duplicate token as both token and internal_token
			credRecord{Kind: "bearer", Token: "T", InternalTok: "T"},
			[]string{"kind", "internal_token"},
		},
	}
	for _, c := range cases {
		names, _ := c.rec.orderedFields()
		if len(names) != len(c.wantCols) {
			t.Errorf("%s: cols %v, want %v", c.rec.Kind, names, c.wantCols)
			continue
		}
		for i := range names {
			if names[i] != c.wantCols[i] {
				t.Errorf("%s: col[%d]=%q, want %q", c.rec.Kind, i, names[i], c.wantCols[i])
			}
		}
	}
}

// csvField quotes per RFC 4180.
func TestCSVField(t *testing.T) {
	cases := map[string]string{
		"plain":         "plain",
		"with,comma":    `"with,comma"`,
		"with\"quote":   `"with""quote"`,
		"with\nnewline": "\"with\nnewline\"",
	}
	for in, want := range cases {
		if got := csvField(in); got != want {
			t.Errorf("csvField(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOtogen_Helpers(t *testing.T) {
	// splitCSV trims and drops empties.
	got := splitCSV(" acme , globex ,, ")
	if len(got) != 2 || got[0] != "acme" || got[1] != "globex" {
		t.Errorf("splitCSV: %#v", got)
	}
	// quoteList produces a valid quoted, comma-separated list.
	if q := quoteList([]string{"acme", "globex"}); q != `"acme", "globex"` {
		t.Errorf("quoteList: %q", q)
	}
	// access key IDs are non-empty and reasonably long.
	ak, err := randomAccessKeyID()
	if err != nil || len(ak) < 16 {
		t.Errorf("randomAccessKeyID: %q err=%v", ak, err)
	}
}
