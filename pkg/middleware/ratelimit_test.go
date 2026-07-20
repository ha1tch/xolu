// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

// ---------------------------------------------------------------------------
// RateLimiter.Allow — core semantics
// ---------------------------------------------------------------------------

func TestAllow_PermitsUpToRate(t *testing.T) {
	rl := newTestLimiter(3, time.Second)
	defer rl.Stop()

	for i := 0; i < 3; i++ {
		allowed, remaining, _ := rl.Allow("k")
		if !allowed {
			t.Fatalf("request %d should be allowed, got denied", i+1)
		}
		want := 3 - (i + 1)
		if remaining != want {
			t.Errorf("request %d: remaining want %d, got %d", i+1, want, remaining)
		}
	}
}

func TestAllow_DeniesOnceRateExceeded(t *testing.T) {
	rl := newTestLimiter(2, time.Second)
	defer rl.Stop()

	rl.Allow("k")
	rl.Allow("k")
	allowed, remaining, _ := rl.Allow("k")
	if allowed {
		t.Error("3rd request should be denied")
	}
	if remaining != 0 {
		t.Errorf("remaining after deny: want 0, got %d", remaining)
	}
}

func TestAllow_NewWindowResetsCounter(t *testing.T) {
	rl := newTestLimiter(1, 50*time.Millisecond)
	defer rl.Stop()

	allowed, _, _ := rl.Allow("k")
	if !allowed {
		t.Fatal("first request should be allowed")
	}
	denied, _, _ := rl.Allow("k")
	if denied {
		t.Fatal("second request should be denied")
	}

	// Wait for window to expire.
	time.Sleep(60 * time.Millisecond)

	allowed2, _, _ := rl.Allow("k")
	if !allowed2 {
		t.Error("first request in new window should be allowed")
	}
}

func TestAllow_IndependentKeys(t *testing.T) {
	rl := newTestLimiter(1, time.Second)
	defer rl.Stop()

	rl.Allow("a")
	rl.Allow("a") // exhausts "a"

	allowed, _, _ := rl.Allow("b")
	if !allowed {
		t.Error("key b should be independent of key a")
	}
}

func TestAllow_ResetTimeIsInFuture(t *testing.T) {
	rl := newTestLimiter(5, time.Second)
	defer rl.Stop()

	before := time.Now()
	_, _, reset := rl.Allow("k")
	if !reset.After(before) {
		t.Errorf("reset time %v should be after %v", reset, before)
	}
}

// ---------------------------------------------------------------------------
// cleanupExpired
// ---------------------------------------------------------------------------

func TestCleanupExpired_RemovesOldWindows(t *testing.T) {
	rl := newTestLimiter(10, 10*time.Millisecond)
	defer rl.Stop()

	rl.Allow("stale")

	// Wait for window to be well past expiry (cleanup threshold is 2x window).
	time.Sleep(30 * time.Millisecond)

	rl.cleanupExpired()

	rl.mu.RLock()
	_, exists := rl.windows["stale"]
	rl.mu.RUnlock()
	if exists {
		t.Error("stale window should have been removed by cleanupExpired")
	}
}

func TestCleanupExpired_RetainsActiveWindows(t *testing.T) {
	rl := newTestLimiter(10, time.Second)
	defer rl.Stop()

	rl.Allow("active")
	rl.cleanupExpired()

	rl.mu.RLock()
	_, exists := rl.windows["active"]
	rl.mu.RUnlock()
	if !exists {
		t.Error("active window should not be removed by cleanupExpired")
	}
}

// ---------------------------------------------------------------------------
// getClientIP — trusted-proxy-aware, T-38
//
// The pre-fix tests codified the very vulnerability T-38 exists to close:
// they treated "X-Forwarded-For sets client IP unconditionally" as the
// expected behaviour. Replaced with the correct trust regime.
// ---------------------------------------------------------------------------

// newTestLimiter builds a RateLimiter with a specific TrustedProxies spec
// for isolated getClientIP testing.
func newIPTestLimiter(trustedProxies string) *RateLimiter {
	return &RateLimiter{
		trustedProxies: parseTrustedProxies(trustedProxies),
	}
}

// TestGetClientIP_NoTrustedProxies_HeadersIgnored — the default deployment:
// no trusted proxies configured, so any X-Forwarded-For or X-Real-IP header
// from an arbitrary peer is untrusted and must be ignored.
func TestGetClientIP_NoTrustedProxies_HeadersIgnored(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.5:12345"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	r.Header.Set("X-Real-IP", "9.9.9.9")
	rl := newIPTestLimiter("")
	if got := rl.getClientIP(r); got != "203.0.113.5" {
		t.Errorf("headers must be ignored without trusted proxies: want 203.0.113.5, got %q", got)
	}
}

// TestGetClientIP_UntrustedPeer_HeadersIgnored — even when *some* proxies
// are configured, a peer not in the list has its XFF ignored.
func TestGetClientIP_UntrustedPeer_HeadersIgnored(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.5:12345" // not in the trusted set
	r.Header.Set("X-Forwarded-For", "attacker-forged-ip")
	rl := newIPTestLimiter("10.0.0.0/8")
	if got := rl.getClientIP(r); got != "203.0.113.5" {
		t.Errorf("untrusted peer must not be able to forge IP: want 203.0.113.5, got %q", got)
	}
}

// TestGetClientIP_TrustedProxy_XFFHonoured — the intended flow: peer is a
// configured proxy, XFF's rightmost non-trusted hop is honoured.
func TestGetClientIP_TrustedProxy_XFFHonoured(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:12345" // trusted
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	rl := newIPTestLimiter("10.0.0.0/8")
	if got := rl.getClientIP(r); got != "198.51.100.7" {
		t.Errorf("trusted proxy XFF should be honoured: want 198.51.100.7, got %q", got)
	}
}

// TestGetClientIP_TrustedChain_WalksPastTrusted — the client sent its
// request through two trusted proxies; the algorithm walks past all
// trusted hops (rightmost first) and returns the first non-trusted one.
func TestGetClientIP_TrustedChain_WalksPastTrusted(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:12345"
	// XFF format: client, proxy1, proxy2 (left-to-right appended)
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.9, 10.0.0.10")
	rl := newIPTestLimiter("10.0.0.0/8")
	if got := rl.getClientIP(r); got != "198.51.100.7" {
		t.Errorf("must walk past trusted hops: want 198.51.100.7, got %q", got)
	}
}

// TestGetClientIP_AttackerPrependsForgedHop — attacker sends XFF starting
// with a forged IP. Since the algorithm walks right-to-left past trusted
// hops and stops at the first non-trusted hop, it correctly ignores what
// the attacker prepended.
func TestGetClientIP_AttackerPrependsForgedHop(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:12345"
	// Attacker (192.0.2.66) prepends a forged 1.1.1.1 hop.
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 192.0.2.66")
	rl := newIPTestLimiter("10.0.0.0/8")
	if got := rl.getClientIP(r); got != "192.0.2.66" {
		t.Errorf("must ignore attacker-prepended forgery: want 192.0.2.66, got %q", got)
	}
}

// TestGetClientIP_TrustedProxy_XRealIPFallback — no XFF, but a trusted
// peer sets X-Real-IP.
func TestGetClientIP_TrustedProxy_XRealIPFallback(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:12345"
	r.Header.Set("X-Real-IP", "198.51.100.7")
	rl := newIPTestLimiter("10.0.0.0/8")
	if got := rl.getClientIP(r); got != "198.51.100.7" {
		t.Errorf("trusted X-Real-IP should be honoured: want 198.51.100.7, got %q", got)
	}
}

// TestGetClientIP_TrustedProxy_NoHeaders — trusted peer sends no headers.
// Fall back to the peer.
func TestGetClientIP_TrustedProxy_NoHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:12345"
	rl := newIPTestLimiter("10.0.0.0/8")
	if got := rl.getClientIP(r); got != "10.0.0.5" {
		t.Errorf("no headers should fall back to peer: want 10.0.0.5, got %q", got)
	}
}

// TestGetClientIP_RemoteAddr — the trivial case: no headers, no proxies.
func TestGetClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.100:54321"
	rl := newIPTestLimiter("")
	if got := rl.getClientIP(r); got != "192.168.1.100" {
		t.Errorf("RemoteAddr: want 192.168.1.100, got %q", got)
	}
}

// TestGetClientIP_BareIP_TrustedProxy — CIDR spec supports bare IPs.
func TestGetClientIP_BareIP_TrustedProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:12345"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	rl := newIPTestLimiter("10.0.0.5") // bare IP
	if got := rl.getClientIP(r); got != "198.51.100.7" {
		t.Errorf("bare-IP trust spec: want 198.51.100.7, got %q", got)
	}
}

// TestGetClientIP_XFFTakesPrecedence — legacy name preserved but rewritten
// for the correct trust regime: with a trusted peer, XFF beats X-Real-IP.
func TestGetClientIP_XFFTakesPrecedence(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:12345" // trusted
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	r.Header.Set("X-Real-IP", "2.2.2.2")
	rl := newIPTestLimiter("10.0.0.0/8")
	if got := rl.getClientIP(r); got != "1.1.1.1" {
		t.Errorf("precedence: want 1.1.1.1, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// RateLimitMiddleware — HTTP integration
// ---------------------------------------------------------------------------

func TestRateLimitMiddleware_Disabled(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.RateLimitEnabled = false
	rl := newTestLimiter(1, time.Second)
	defer rl.Stop()

	mw := RateLimitMiddleware(cfg, rl)
	handler := mw(echoHandler(200))

	// First request allowed.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
	// Second request — would be denied if enabled, should pass since disabled.
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, httptest.NewRequest("GET", "/", nil))
	if rec2.Code != 200 {
		t.Errorf("disabled: want 200, got %d", rec2.Code)
	}
}

func TestRateLimitMiddleware_AllowsUnderLimit(t *testing.T) {
	cfg := defaultTestConfig()
	rl := newTestLimiter(5, time.Second)
	defer rl.Stop()

	mw := RateLimitMiddleware(cfg, rl)
	handler := mw(echoHandler(200))

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestFromIP("1.2.3.4"))
		if rec.Code != 200 {
			t.Errorf("request %d: want 200, got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimitMiddleware_Denies429WhenExceeded(t *testing.T) {
	cfg := defaultTestConfig()
	rl := newTestLimiter(2, time.Second)
	defer rl.Stop()

	mw := RateLimitMiddleware(cfg, rl)
	handler := mw(echoHandler(200))

	handler.ServeHTTP(httptest.NewRecorder(), requestFromIP("1.1.1.1"))
	handler.ServeHTTP(httptest.NewRecorder(), requestFromIP("1.1.1.1"))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestFromIP("1.1.1.1"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("want 429, got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_SetsRateLimitHeaders(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.RateLimitRate = 10
	rl := newTestLimiter(10, time.Second)
	defer rl.Stop()

	mw := RateLimitMiddleware(cfg, rl)
	handler := mw(echoHandler(200))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestFromIP("5.5.5.5"))

	if rec.Header().Get("X-RateLimit-Limit") != "10" {
		t.Errorf("X-RateLimit-Limit: want 10, got %q", rec.Header().Get("X-RateLimit-Limit"))
	}
	if rec.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("X-RateLimit-Remaining should be set")
	}
	if rec.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("X-RateLimit-Reset should be set")
	}
}

func TestRateLimitMiddleware_SetsRetryAfterOn429(t *testing.T) {
	cfg := defaultTestConfig()
	rl := newTestLimiter(1, time.Second)
	defer rl.Stop()

	mw := RateLimitMiddleware(cfg, rl)
	handler := mw(echoHandler(200))

	handler.ServeHTTP(httptest.NewRecorder(), requestFromIP("6.6.6.6"))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestFromIP("6.6.6.6"))

	ra := rec.Header().Get("Retry-After")
	if ra == "" {
		t.Error("Retry-After header should be set on 429")
	}
	v, err := strconv.Atoi(ra)
	if err != nil || v < 1 {
		t.Errorf("Retry-After should be >= 1, got %q", ra)
	}
}

func TestRateLimitMiddleware_ExcludedPathBypasses(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.RateLimitRate = 1
	cfg.AuthExcludePaths = []string{"/health"}
	rl := newTestLimiter(1, time.Second)
	defer rl.Stop()

	mw := RateLimitMiddleware(cfg, rl)
	handler := mw(echoHandler(200))

	// Exhaust the limit on /api.
	handler.ServeHTTP(httptest.NewRecorder(), requestFromIP("7.7.7.7"))

	// /health should still pass even though rate is exhausted for the IP,
	// because excluded paths skip the limiter entirely.
	rec := httptest.NewRecorder()
	req := requestFromIP("7.7.7.7")
	req.URL.Path = "/health"
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("excluded path: want 200, got %d", rec.Code)
	}
}

func TestRateLimitMiddleware_ByIPIsolatesClients(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.RateLimitByIP = true
	cfg.RateLimitByKey = false
	rl := newTestLimiter(1, time.Second)
	defer rl.Stop()

	mw := RateLimitMiddleware(cfg, rl)
	handler := mw(echoHandler(200))

	// Exhaust IP A.
	handler.ServeHTTP(httptest.NewRecorder(), requestFromIP("10.0.0.1"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestFromIP("10.0.0.1"))
	if rec.Code != 429 {
		t.Fatalf("IP A second request: want 429, got %d", rec.Code)
	}

	// IP B should be unaffected.
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, requestFromIP("10.0.0.2"))
	if rec2.Code != 200 {
		t.Errorf("IP B should be independent, got %d", rec2.Code)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestLimiter(rate int, dur time.Duration) *RateLimiter {
	return &RateLimiter{
		windows: make(map[string]*window),
		rate:    rate,
		window:  dur,
		cleanup: time.Hour, // disable automatic cleanup in tests
		stopCh:  make(chan struct{}),
	}
}

func defaultTestConfig() *config.Config {
	return &config.Config{
		RateLimitEnabled: true,
		RateLimitRate:    100,
		RateLimitWindow:  1,
		RateLimitByIP:    true,
		RateLimitByKey:   false,
		AuthExcludePaths: []string{},
	}
}

func echoHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	})
}

func requestFromIP(ip string) *http.Request {
	r := httptest.NewRequest("GET", "/api/test", nil)
	r.RemoteAddr = ip + ":12345"
	return r
}
