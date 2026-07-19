// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package authmw is xolu's authentication middleware, extracted from
// pkg/middleware (T-19) so external binaries — the molu hub in
// particular — can import authentication without pulling in the rest of
// the middleware package or the full server config. Its configuration
// type lives in pkg/authconfig; the xolu server constructs that from its
// full config via config.(*Config).AuthConfig().
package authmw

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ha1tch/xolu/pkg/authconfig"
)

// ContextKey type for context values
type ContextKey string

const (
	// ContextKeySubject is the key for the authenticated subject
	ContextKeySubject ContextKey = "auth_subject"
	// ContextKeyAuthMethod is the key for the auth method used
	ContextKeyAuthMethod ContextKey = "auth_method"
)

// AuthMiddleware creates an authentication middleware based on config
func AuthMiddleware(cfg authconfig.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for excluded paths
			for _, path := range cfg.AuthExcludePaths {
				if strings.HasPrefix(r.URL.Path, path) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// No auth configured
			if cfg.AuthType == "none" || cfg.AuthType == "" {
				next.ServeHTTP(w, r)
				return
			}

			var subject string
			var authMethod string
			var authenticated bool
			var grant TenantGrant

			switch cfg.AuthType {
			case "jwt":
				subject, grant, authenticated = validateJWT(r, cfg)
				authMethod = "jwt"
			case "apikey":
				subject, grant, authenticated = validateAPIKey(r, cfg)
				authMethod = "apikey"
			case "bearertoken":
				subject, grant, authenticated = validateBearerToken(r, cfg)
				authMethod = "bearertoken"
			default:
				// Unknown auth type — refuse to serve. Config validation
				// should prevent this, but defence in depth.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"code":    "XOLU-CF001",
						"message": "server misconfiguration: unknown auth type",
						"status":  http.StatusInternalServerError,
					},
				})
				return
			}

			if !authenticated {
				writeAuthError(w, cfg.AuthType)
				return
			}

			// Add auth info to context
			ctx := context.WithValue(r.Context(), ContextKeySubject, subject)
			ctx = context.WithValue(ctx, ContextKeyAuthMethod, authMethod)
			ctx = context.WithValue(ctx, ContextKeyTenantGrant, grant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// validateJWT validates a JWT token from Authorization header. It returns the
// subject, the tenant grant extracted from claims (tenants / tenant_admin), and
// whether the token is valid.
func validateJWT(r *http.Request, cfg authconfig.Config) (string, TenantGrant, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", TenantGrant{}, false
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", TenantGrant{}, false
	}

	token := parts[1]

	// Parse and validate JWT (simplified HS256 validation)
	claims, valid := parseAndValidateJWT(token, cfg.JWTSecret, cfg.JWTIssuer)
	if !valid {
		return "", TenantGrant{}, false
	}

	subject, _ := claims["sub"].(string)
	return subject, grantFromClaims(claims), true
}

// grantFromClaims builds a TenantGrant from JWT claims. "tenant_admin": true
// yields an admin grant; "tenants" (a JSON array of strings) yields an explicit
// tenant set. Absent claims yield an empty grant (authorises nothing under
// scoped mode).
func grantFromClaims(claims JWTClaims) TenantGrant {
	if admin, ok := claims["tenant_admin"].(bool); ok && admin {
		return TenantGrant{Admin: true}
	}
	var tenants []string
	if raw, ok := claims["tenants"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				tenants = append(tenants, s)
			}
		}
	}
	return TenantGrant{Tenants: tenants}
}

// JWTClaims represents standard JWT claims
type JWTClaims map[string]interface{}

// parseAndValidateJWT parses and validates a JWT token
// Supports HS256 algorithm only for simplicity
func parseAndValidateJWT(token, secret, expectedIssuer string) (JWTClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}

	// Decode header
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}
	var header map[string]interface{}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, false
	}

	// Check algorithm
	if alg, ok := header["alg"].(string); !ok || alg != "HS256" {
		return nil, false
	}

	// Decode payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims JWTClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, false
	}

	// Verify signature
	signatureInput := parts[0] + "." + parts[1]
	expectedSig := computeHS256(signatureInput, secret)
	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, false
	}
	if !hmac.Equal(expectedSig, actualSig) {
		return nil, false
	}

	// Check expiration. The claim is normalised so its acceptance does not
	// depend on the JSON type used to encode it: a numeric value and a numeric
	// string are treated identically (D-002). A claim that is present but not a
	// parseable number is treated as invalid and rejects the token.
	//
	// Policy (D-002, Option B): exp is REQUIRED. A token with no exp claim is
	// rejected rather than treated as never-expiring — every token must carry a
	// parseable expiry, so a leaked token cannot be valid indefinitely.
	raw, present := claims["exp"]
	if !present {
		return nil, false
	}
	exp, ok := claimAsUnixTime(raw)
	if !ok || time.Now().Unix() > exp {
		return nil, false
	}

	// Check not before, with the same type normalisation. Unlike exp, nbf is
	// optional: a token without it is simply valid from issuance.
	if rawNbf, hasNbf := claims["nbf"]; hasNbf {
		nbf, okNbf := claimAsUnixTime(rawNbf)
		if !okNbf || time.Now().Unix() < nbf {
			return nil, false
		}
	}

	// Check issuer if configured
	if expectedIssuer != "" {
		if iss, ok := claims["iss"].(string); !ok || iss != expectedIssuer {
			return nil, false
		}
	}

	return claims, true
}

// claimAsUnixTime normalises a JWT time claim (exp/nbf) to a Unix timestamp,
// accepting either a JSON number or a numeric JSON string. It returns ok ==
// false if the value is present but not a parseable integer time, so a
// malformed time claim rejects the token rather than silently skipping the
// check. This makes a token's lifetime independent of the JSON type used to
// encode its expiry (D-002).
func claimAsUnixTime(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		if f, err := n.Float64(); err == nil {
			return int64(f), true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(n)
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// computeHS256 computes HMAC-SHA256 signature
func computeHS256(input, secret string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(input))
	return h.Sum(nil)
}

// validateAPIKey validates an API key from header or query param. It returns the
// subject, the tenant grant resolved from APIKeyGrants (empty if the key has no
// grant), and whether the key is valid.
func validateAPIKey(r *http.Request, cfg authconfig.Config) (string, TenantGrant, bool) {
	// Check X-API-Key header first
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" {
		// Check Authorization header with ApiKey scheme
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "ApiKey ") {
			apiKey = strings.TrimPrefix(authHeader, "ApiKey ")
		}
	}
	if apiKey == "" {
		// Check query parameter as fallback
		apiKey = r.URL.Query().Get("api_key")
	}

	if apiKey == "" {
		return "", TenantGrant{}, false
	}

	subjectOf := func(k string) string {
		if len(k) > 8 {
			return k[:8] + "..."
		}
		return k
	}

	// Prefer an explicit grant entry: it both authenticates the key and carries
	// its tenant authority.
	for _, g := range cfg.APIKeyGrants {
		if g.Key != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(g.Key)) == 1 {
			return subjectOf(apiKey), TenantGrant{Admin: g.Admin, Tenants: g.Tenants}, true
		}
	}

	// Fall back to the flat key list. These keys authenticate but carry no grant
	// (empty grant). Under scoped mode an empty grant is rejected at the
	// enforcement point, which is the intended "migrate flat keys to APIKeyGrants"
	// behaviour.
	for _, validKey := range cfg.APIKeys {
		if validKey != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(validKey)) == 1 {
			return subjectOf(apiKey), TenantGrant{}, true
		}
	}

	return "", TenantGrant{}, false
}

// validateBearerToken validates a plain shared secret sent as
// Authorization: Bearer <token>.
//
// This is the "bearertoken" auth type, intended for internal service-to-service
// calls where the caller holds a pre-shared hex token (e.g. generated with
// `openssl rand -hex 32`). The token is compared against cfg.InternalToken
// using subtle.ConstantTimeCompare to prevent timing attacks.
//
// It is deliberately separate from the "jwt" auth type, which also uses the
// Bearer scheme but expects a structured HS256 JWT. The two types must not
// share a code path: a raw hex token must not be silently accepted by the JWT
// validator (it would fail the dot-split check), and a JWT must not be
// accepted by this validator (lengths would differ).
func validateBearerToken(r *http.Request, cfg authconfig.Config) (string, TenantGrant, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", TenantGrant{}, false
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", TenantGrant{}, false
	}
	token := parts[1]
	if token == "" || cfg.InternalToken == "" {
		return "", TenantGrant{}, false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(cfg.InternalToken)) != 1 {
		return "", TenantGrant{}, false
	}
	// The bearer token is the trusted-gateway credential: under scoped mode it
	// carries full authority. The gateway is responsible for its own per-user
	// authorization before calling xolu.
	return "internal", TenantGrant{Admin: true}, true
}

// writeAuthError writes an authentication error response
func writeAuthError(w http.ResponseWriter, authType string) {
	w.Header().Set("Content-Type", "application/json")

	switch authType {
	case "jwt", "bearertoken":
		w.Header().Set("WWW-Authenticate", `Bearer realm="xolu"`)
	case "apikey":
		w.Header().Set("WWW-Authenticate", `ApiKey realm="xolu"`)
	}

	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    "XOLU-AU001",
			"message": "Authentication required",
			"status":  http.StatusUnauthorized,
		},
	})
}

// GetSubject retrieves the authenticated subject from context
func GetSubject(ctx context.Context) string {
	if subject, ok := ctx.Value(ContextKeySubject).(string); ok {
		return subject
	}
	return ""
}

// GetAuthMethod retrieves the auth method from context
func GetAuthMethod(ctx context.Context) string {
	if method, ok := ctx.Value(ContextKeyAuthMethod).(string); ok {
		return method
	}
	return ""
}
