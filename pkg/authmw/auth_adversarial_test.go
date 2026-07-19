// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Adversarial tests for the authentication middleware. These assert that the
// known attack classes against token/key auth are rejected:
//
//   - JWT algorithm confusion (alg:none, alg swapping, missing/non-string alg)
//   - JWT structural attacks (wrong segment count, non-base64, malformed JSON)
//   - API-key edge cases (empty, whitespace, case, prefix-of-valid)
//   - Bearer-token edge cases (empty, scheme confusion, near-miss)
//
// Time-based claim handling (exp/nbf) is covered in auth_d002_test.go and the
// no-panic property in fuzz_jwt_test.go; this file focuses on credential
// forgery and malformed input.

package authmw

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

const advSecret = "adversarial-test-secret-0000000000"

func advJWTConfig() *config.Config {
	cfg := config.Default()
	cfg.AuthType = "jwt"
	cfg.JWTSecret = advSecret
	return cfg
}

func b64url(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// mintRaw builds a token from raw header/payload JSON, signing with HS256 over
// the given secret (use the wrong secret to forge).
func mintRaw(headerJSON, payloadJSON, secret string) string {
	signingInput := b64url(headerJSON) + "." + b64url(payloadJSON)
	sig := base64.RawURLEncoding.EncodeToString(computeHS256(signingInput, secret))
	return signingInput + "." + sig
}

// runJWT sends a bearer token through AuthMiddleware and returns the status.
func runJWT(cfg *config.Config, token string) int {
	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

// ── JWT algorithm confusion ──────────────────────────────────────────────────

func TestAdvJWT_AlgorithmConfusion(t *testing.T) {
	cfg := advJWTConfig()
	validPayload := `{"sub":"x","exp":4102444800}`

	cases := []struct {
		name   string
		header string
		// secret used to sign; for "none" the signature is empty/irrelevant
		sign func(payload string) string
	}{
		{
			name:   "alg none lowercase",
			header: `{"alg":"none","typ":"JWT"}`,
			sign:   func(p string) string { return b64url(`{"alg":"none","typ":"JWT"}`) + "." + b64url(p) + "." },
		},
		{
			name:   "alg None mixed case",
			header: `{"alg":"None","typ":"JWT"}`,
			sign:   func(p string) string { return b64url(`{"alg":"None","typ":"JWT"}`) + "." + b64url(p) + "." },
		},
		{
			name:   "alg HS512 swap (signed with valid secret)",
			header: `{"alg":"HS512","typ":"JWT"}`,
			sign:   func(p string) string { return mintRaw(`{"alg":"HS512","typ":"JWT"}`, p, advSecret) },
		},
		{
			name:   "alg RS256 swap",
			header: `{"alg":"RS256","typ":"JWT"}`,
			sign:   func(p string) string { return mintRaw(`{"alg":"RS256","typ":"JWT"}`, p, advSecret) },
		},
		{
			name:   "alg missing",
			header: `{"typ":"JWT"}`,
			sign:   func(p string) string { return mintRaw(`{"typ":"JWT"}`, p, advSecret) },
		},
		{
			name:   "alg non-string",
			header: `{"alg":256,"typ":"JWT"}`,
			sign:   func(p string) string { return mintRaw(`{"alg":256,"typ":"JWT"}`, p, advSecret) },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := runJWT(cfg, c.sign(validPayload)); code != http.StatusUnauthorized {
				t.Errorf("%s: expected 401, got %d", c.name, code)
			}
		})
	}
}

// ── JWT forged / wrong-secret signatures ─────────────────────────────────────

func TestAdvJWT_ForgedSignature(t *testing.T) {
	cfg := advJWTConfig()
	payload := `{"sub":"attacker","exp":4102444800}`
	// Signed with the wrong secret.
	forged := mintRaw(`{"alg":"HS256","typ":"JWT"}`, payload, "wrong-secret")
	if code := runJWT(cfg, forged); code != http.StatusUnauthorized {
		t.Errorf("forged signature: expected 401, got %d", code)
	}
	// Valid token with one byte flipped in the signature.
	valid := mintRaw(`{"alg":"HS256","typ":"JWT"}`, payload, advSecret)
	tampered := valid[:len(valid)-2] + "XY"
	if code := runJWT(cfg, tampered); code != http.StatusUnauthorized {
		t.Errorf("tampered signature: expected 401, got %d", code)
	}
}

// ── JWT structural attacks ───────────────────────────────────────────────────

func TestAdvJWT_Malformed(t *testing.T) {
	cfg := advJWTConfig()
	good := mintRaw(`{"alg":"HS256","typ":"JWT"}`, `{"sub":"x","exp":4102444800}`, advSecret)

	cases := map[string]string{
		"empty":                  "",
		"single segment":         "abc",
		"two segments":           "abc.def",
		"four segments":          good + ".extra",
		"non-base64 header":      "!!!." + strings.SplitN(good, ".", 2)[1],
		"non-base64 payload":     strings.SplitN(good, ".", 2)[0] + ".!!!." + strings.Split(good, ".")[2],
		"malformed json header":  b64url(`{not json`) + "." + b64url(`{"exp":4102444800}`) + ".sig",
		"malformed json payload": b64url(`{"alg":"HS256"}`) + "." + b64url(`{not json`) + ".sig",
		"only dots":              "..",
		"null bytes":             "a\x00b.c\x00d.e\x00f",
		"whitespace":             "   .   .   ",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			// Must not panic and must not authenticate.
			if code := runJWT(cfg, tok); code != http.StatusUnauthorized {
				t.Errorf("%s: expected 401, got %d", name, code)
			}
		})
	}
}

// ── API-key edge cases ───────────────────────────────────────────────────────

func runAPIKey(cfg *config.Config, header, value string) int {
	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	if header != "" {
		req.Header.Set(header, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

func TestAdvAPIKey_EdgeCases(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "apikey"
	cfg.APIKeys = []string{"valid-key-abcdef0123456789"}

	cases := []struct {
		name   string
		header string
		value  string
		want   int
	}{
		{"exact valid", "X-API-Key", "valid-key-abcdef0123456789", http.StatusOK},
		{"empty value", "X-API-Key", "", http.StatusUnauthorized},
		{"whitespace value", "X-API-Key", "   ", http.StatusUnauthorized},
		{"prefix of valid", "X-API-Key", "valid-key-abcdef", http.StatusUnauthorized},
		{"valid plus suffix", "X-API-Key", "valid-key-abcdef0123456789X", http.StatusUnauthorized},
		{"case altered", "X-API-Key", "VALID-KEY-ABCDEF0123456789", http.StatusUnauthorized},
		{"leading space", "X-API-Key", " valid-key-abcdef0123456789", http.StatusUnauthorized},
		{"no header at all", "", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := runAPIKey(cfg, c.header, c.value); code != c.want {
				t.Errorf("%s: expected %d, got %d", c.name, c.want, code)
			}
		})
	}
}

// An empty configured key must not allow an empty presented key.
func TestAdvAPIKey_EmptyConfiguredKeyNotMatchable(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "apikey"
	cfg.APIKeys = []string{"", "real-key-000000"} // an accidental empty entry
	if code := runAPIKey(cfg, "X-API-Key", ""); code == http.StatusOK {
		t.Error("empty presented key matched an empty configured key entry")
	}
}

// ── Bearer-token edge cases ──────────────────────────────────────────────────

func runBearer(cfg *config.Config, rawAuthHeader string) int {
	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	if rawAuthHeader != "" {
		req.Header.Set("Authorization", rawAuthHeader)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

func TestAdvBearer_EdgeCases(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "bearertoken"
	cfg.InternalToken = "secret-internal-token-0000000000"

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"exact valid", "Bearer secret-internal-token-0000000000", http.StatusOK},
		{"empty token", "Bearer ", http.StatusUnauthorized},
		{"no scheme", "secret-internal-token-0000000000", http.StatusUnauthorized},
		{"wrong scheme", "Basic secret-internal-token-0000000000", http.StatusUnauthorized},
		{"prefix of valid", "Bearer secret-internal-token", http.StatusUnauthorized},
		{"valid plus suffix", "Bearer secret-internal-token-0000000000X", http.StatusUnauthorized},
		{"no header", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := runBearer(cfg, c.header); code != c.want {
				t.Errorf("%s: expected %d, got %d", c.name, c.want, code)
			}
		})
	}
}

// An empty InternalToken must not be matchable by an empty bearer.
func TestAdvBearer_EmptyTokenNotMatchable(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "bearertoken"
	cfg.InternalToken = "" // misconfigured: no token set
	if code := runBearer(cfg, "Bearer "); code == http.StatusOK {
		t.Error("empty bearer matched an empty InternalToken")
	}
}
