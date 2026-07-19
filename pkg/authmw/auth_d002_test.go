// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package authmw

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

// D-002: the exp/nbf checks are guarded by a type assertion to float64. When a
// claim is present but encoded as a JSON string (e.g. "exp":"1700000000"), the
// assertion yields ok == false, the check is skipped, and an otherwise-expired
// token passes. The attacker must already hold a validly-signed token (they
// know JWTSecret), so this is a defence-in-depth weakness, not an
// unauthenticated bypass — but a token's lifetime must not depend on the JSON
// type used to encode its expiry.
//
// Expected end state after the fix: a past exp encoded as a numeric string is
// rejected exactly like a past numeric exp.

func jwtAuthResult(t *testing.T, secret string, claims map[string]interface{}) int {
	t.Helper()
	cfg := config.Default()
	cfg.AuthType = "jwt"
	cfg.JWTSecret = secret
	token := createTestJWT(secret, claims)
	handler := AuthMiddleware(cfg.AuthConfig())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code
}

// A past exp encoded as a JSON string must be rejected, just like a numeric one.
func TestAuthMiddleware_JWT_ExpiredStringExp_Rejected(t *testing.T) {
	secret := "test-secret-key"
	past := time.Now().Add(-time.Hour).Unix()
	code := jwtAuthResult(t, secret, map[string]interface{}{
		"sub": "user123",
		"exp": fmt.Sprintf("%d", past), // string, not number
	})
	if code != http.StatusUnauthorized {
		t.Errorf("expired exp encoded as string: want 401, got %d", code)
	}
}

// An nbf in the future encoded as a JSON string must also be honoured.
func TestAuthMiddleware_JWT_FutureStringNbf_Rejected(t *testing.T) {
	secret := "test-secret-key"
	future := time.Now().Add(time.Hour).Unix()
	code := jwtAuthResult(t, secret, map[string]interface{}{
		"sub": "user123",
		"exp": fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()),
		"nbf": fmt.Sprintf("%d", future), // not yet valid, as a string
	})
	if code != http.StatusUnauthorized {
		t.Errorf("future nbf encoded as string: want 401, got %d", code)
	}
}

// Control: a valid (future) exp encoded as a string must still be accepted, so
// the normalisation does not reject legitimate string-encoded numeric claims.
func TestAuthMiddleware_JWT_ValidStringExp_Accepted(t *testing.T) {
	secret := "test-secret-key"
	future := time.Now().Add(time.Hour).Unix()
	code := jwtAuthResult(t, secret, map[string]interface{}{
		"sub": "user123",
		"exp": fmt.Sprintf("%d", future),
	})
	if code != http.StatusOK {
		t.Errorf("valid exp encoded as string: want 200, got %d", code)
	}
}

// Control: the numeric path is unaffected — a past numeric exp is still
// rejected (guards against a regression that weakens the existing check).
func TestAuthMiddleware_JWT_ExpiredNumericExp_StillRejected(t *testing.T) {
	secret := "test-secret-key"
	code := jwtAuthResult(t, secret, map[string]interface{}{
		"sub": "user123",
		"exp": float64(time.Now().Add(-time.Hour).Unix()),
	})
	if code != http.StatusUnauthorized {
		t.Errorf("expired numeric exp: want 401, got %d", code)
	}
}

// D-002 policy (Option B): a token with NO exp claim must be rejected. An
// absent exp previously meant the token never expired; under this policy every
// token must carry a parseable expiry, so an unbounded-lifetime token is no
// longer accepted.
func TestAuthMiddleware_JWT_MissingExp_Rejected(t *testing.T) {
	secret := "test-secret-key"
	code := jwtAuthResult(t, secret, map[string]interface{}{
		"sub": "user123",
		// no exp
	})
	if code != http.StatusUnauthorized {
		t.Errorf("token with no exp claim: want 401 (exp is required), got %d", code)
	}
}

// A token that carries exp but no nbf is still accepted — nbf remains optional;
// only exp is mandatory under Option B.
func TestAuthMiddleware_JWT_MissingNbf_Accepted(t *testing.T) {
	secret := "test-secret-key"
	code := jwtAuthResult(t, secret, map[string]interface{}{
		"sub": "user123",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
		// no nbf — fine
	})
	if code != http.StatusOK {
		t.Errorf("token with exp but no nbf: want 200, got %d", code)
	}
}
