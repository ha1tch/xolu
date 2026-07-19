// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package middleware

// Rate-limiter tests relocated from the former combined auth_test.go
// when the auth middleware moved to pkg/authmw (T-19).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

func TestRateLimiter_Allow(t *testing.T) {
	cfg := config.Default()
	cfg.RateLimitRate = 5
	cfg.RateLimitWindow = 60

	limiter := NewRateLimiter(cfg)
	defer limiter.Stop()

	// First 5 requests should be allowed
	for i := 0; i < 5; i++ {
		allowed, remaining, _ := limiter.Allow("test-key")
		if !allowed {
			t.Errorf("Request %d should be allowed", i+1)
		}
		if remaining != 5-i-1 {
			t.Errorf("Expected remaining %d, got %d", 5-i-1, remaining)
		}
	}

	// 6th request should be denied
	allowed, remaining, _ := limiter.Allow("test-key")
	if allowed {
		t.Error("6th request should be denied")
	}
	if remaining != 0 {
		t.Errorf("Expected remaining 0, got %d", remaining)
	}
}

func TestRateLimiter_DifferentKeys(t *testing.T) {
	cfg := config.Default()
	cfg.RateLimitRate = 2
	cfg.RateLimitWindow = 60

	limiter := NewRateLimiter(cfg)
	defer limiter.Stop()

	// Key1: 2 requests
	limiter.Allow("key1")
	limiter.Allow("key1")

	// Key1: 3rd should fail
	allowed, _, _ := limiter.Allow("key1")
	if allowed {
		t.Error("Key1 3rd request should be denied")
	}

	// Key2: should still be allowed
	allowed, _, _ = limiter.Allow("key2")
	if !allowed {
		t.Error("Key2 1st request should be allowed")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	cfg := config.Default()
	cfg.RateLimitEnabled = true
	cfg.RateLimitRate = 2
	cfg.RateLimitWindow = 60
	cfg.RateLimitByIP = true
	cfg.AuthExcludePaths = []string{"/health"}

	limiter := NewRateLimiter(cfg)
	defer limiter.Stop()

	handler := RateLimitMiddleware(cfg, limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 2 requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/v1/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Expected 429, got %d", rec.Code)
	}

	// Check rate limit headers
	if rec.Header().Get("X-RateLimit-Limit") != "2" {
		t.Error("Missing or wrong X-RateLimit-Limit header")
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Missing Retry-After header")
	}
}

func TestRateLimitMiddleware_ExcludedPath(t *testing.T) {
	cfg := config.Default()
	cfg.RateLimitEnabled = true
	cfg.RateLimitRate = 1
	cfg.RateLimitWindow = 60
	cfg.AuthExcludePaths = []string{"/health"}

	limiter := NewRateLimiter(cfg)
	defer limiter.Stop()

	handler := RateLimitMiddleware(cfg, limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Multiple requests to excluded path should all succeed
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Request %d to /health: expected 200, got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimitMiddleware_ErrorEnvelope(t *testing.T) {
	cfg := config.Default()
	cfg.RateLimitEnabled = true
	cfg.RateLimitRate = 1
	cfg.RateLimitWindow = 60
	cfg.RateLimitByIP = true

	limiter := NewRateLimiter(cfg)
	defer limiter.Stop()

	handler := RateLimitMiddleware(cfg, limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust the rate limit
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.RemoteAddr = "10.0.0.1:9999"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req) // uses the one allowed request

	// This request should be rate-limited
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("Expected 429, got %d", rec.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Response is not valid JSON: %v", err)
	}

	// Verify nested error envelope
	errObj, ok := body["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected nested error object, got %T: %v", body["error"], body["error"])
	}
	if errObj["code"] != "XOLU-RL001" {
		t.Errorf("Expected error code XOLU-RL001, got %v", errObj["code"])
	}

	// Verify retry_after is a sibling, not inside the error object
	if _, ok := body["retry_after"]; !ok {
		t.Error("Expected retry_after as sibling of error object")
	}
	if _, ok := errObj["retry_after"]; ok {
		t.Error("retry_after should not be inside the error object")
	}
}
