// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package authmw

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

// Helper to create a valid JWT token for testing
func createTestJWT(secret string, claims map[string]interface{}) string {
	header := map[string]interface{}{
		"alg": "HS256",
		"typ": "JWT",
	}

	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signatureInput := headerB64 + "." + claimsB64
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(signatureInput))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	return headerB64 + "." + claimsB64 + "." + signature
}

func TestAuthMiddleware_NoAuth(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "none"

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ExcludedPath(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "apikey"
	cfg.APIKeys = []string{"test-key"}
	cfg.AuthExcludePaths = []string{"/health", "/version"}

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request to excluded path without auth should succeed
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 for excluded path, got %d", rec.Code)
	}
}

func TestAuthMiddleware_APIKey_Valid(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "apikey"
	cfg.APIKeys = []string{"test-key-123", "another-key"}

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := GetSubject(r.Context())
		if subject == "" {
			t.Error("Expected subject in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("X-API-Key", "test-key-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_APIKey_Invalid(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "apikey"
	cfg.APIKeys = []string{"test-key-123"}

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_APIKey_Missing(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "apikey"
	cfg.APIKeys = []string{"test-key-123"}

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWT_Valid(t *testing.T) {
	secret := "test-secret-key"
	cfg := config.Default()
	cfg.AuthType = "jwt"
	cfg.JWTSecret = secret

	token := createTestJWT(secret, map[string]interface{}{
		"sub": "user123",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := GetSubject(r.Context())
		if subject != "user123" {
			t.Errorf("Expected subject 'user123', got '%s'", subject)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWT_Expired(t *testing.T) {
	secret := "test-secret-key"
	cfg := config.Default()
	cfg.AuthType = "jwt"
	cfg.JWTSecret = secret

	token := createTestJWT(secret, map[string]interface{}{
		"sub": "user123",
		"exp": float64(time.Now().Add(-time.Hour).Unix()), // Expired
	})

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for expired token, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWT_InvalidSignature(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "jwt"
	cfg.JWTSecret = "correct-secret"

	// Create token with wrong secret
	token := createTestJWT("wrong-secret", map[string]interface{}{
		"sub": "user123",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for invalid signature, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWT_WrongIssuer(t *testing.T) {
	secret := "test-secret-key"
	cfg := config.Default()
	cfg.AuthType = "jwt"
	cfg.JWTSecret = secret
	cfg.JWTIssuer = "expected-issuer"

	token := createTestJWT(secret, map[string]interface{}{
		"sub": "user123",
		"iss": "wrong-issuer",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for wrong issuer, got %d", rec.Code)
	}
}

func TestAuthMiddleware_UnknownAuthType_Returns500(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "bogus" // not "none", "jwt", or "apikey"

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not have been called — unknown auth type should fail closed")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500 for unknown auth type, got %d", rec.Code)
	}

	// Verify structured error envelope
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Response is not valid JSON: %v", err)
	}
	errObj, ok := body["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected nested error object, got %T: %v", body["error"], body["error"])
	}
	if errObj["code"] != "XOLU-CF001" {
		t.Errorf("Expected error code XOLU-CF001, got %v", errObj["code"])
	}
}

func TestAuthMiddleware_BearerToken_Valid(t *testing.T) {
	token := "a3f8e2d1c4b5a6970123456789abcdef0123456789abcdef0123456789abcdef01"
	cfg := config.Default()
	cfg.AuthType = "bearertoken"
	cfg.InternalToken = token

	var capturedSubject string
	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSubject = GetSubject(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}
	if capturedSubject != "internal" {
		t.Errorf("Expected subject 'internal', got %q", capturedSubject)
	}
}

func TestAuthMiddleware_BearerToken_Invalid(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "bearertoken"
	cfg.InternalToken = "correct-token-abc123"

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called with wrong token")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token-xyz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != `Bearer realm="xolu"` {
		t.Errorf("Expected Bearer WWW-Authenticate, got %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestAuthMiddleware_BearerToken_Missing(t *testing.T) {
	cfg := config.Default()
	cfg.AuthType = "bearertoken"
	cfg.InternalToken = "correct-token-abc123"

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called with no token")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_BearerToken_WrongScheme(t *testing.T) {
	// A raw token sent via X-API-Key or ApiKey scheme must not be accepted
	// by the bearertoken validator.
	cfg := config.Default()
	cfg.AuthType = "bearertoken"
	cfg.InternalToken = "correct-token-abc123"

	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called with wrong scheme")
	}))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("X-API-Key", "correct-token-abc123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 for X-API-Key with bearertoken type, got %d", rec.Code)
	}
}
