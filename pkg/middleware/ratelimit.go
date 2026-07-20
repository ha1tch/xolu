// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package middleware

import (
	"net"
	"strings"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

// RateLimiter implements a fixed-window rate limiter. This allows up to 2x
// burst at window boundaries; a sliding-window approach would prevent that
// but adds complexity. Acceptable for the current use case.
type RateLimiter struct {
	mu             sync.RWMutex
	windows        map[string]*window
	rate           int           // Max requests per window
	window         time.Duration // Window duration
	byIP           bool
	byKey          bool
	cleanup        time.Duration
	stopCh         chan struct{}
	trustedProxies []*net.IPNet // T-38: CIDR ranges whose XFF headers are honoured
}

type window struct {
	count     int
	startTime time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(cfg *config.Config) *RateLimiter {
	trusted := parseTrustedProxies(cfg.TrustedProxies)
	rl := &RateLimiter{
		windows:        make(map[string]*window),
		rate:           cfg.RateLimitRate,
		window:         time.Duration(cfg.RateLimitWindow) * time.Second,
		byIP:           cfg.RateLimitByIP,
		byKey:          cfg.RateLimitByKey,
		cleanup:        time.Minute * 5,
		stopCh:         make(chan struct{}),
		trustedProxies: trusted,
	}

	// Start cleanup goroutine
	go rl.cleanupLoop()

	return rl
}

// cleanupLoop periodically removes expired windows
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanupExpired()
		case <-rl.stopCh:
			return
		}
	}
}

// cleanupExpired removes expired rate limit windows
func (rl *RateLimiter) cleanupExpired() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for key, w := range rl.windows {
		if now.Sub(w.startTime) > rl.window*2 {
			delete(rl.windows, key)
		}
	}
}

// Stop stops the rate limiter cleanup goroutine
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// Allow checks if a request is allowed and increments the counter
func (rl *RateLimiter) Allow(key string) (bool, int, time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	w, exists := rl.windows[key]
	if !exists || now.Sub(w.startTime) > rl.window {
		// New window
		rl.windows[key] = &window{
			count:     1,
			startTime: now,
		}
		return true, rl.rate - 1, now.Add(rl.window)
	}

	// Existing window
	if w.count >= rl.rate {
		resetTime := w.startTime.Add(rl.window)
		return false, 0, resetTime
	}

	w.count++
	return true, rl.rate - w.count, w.startTime.Add(rl.window)
}

// RateLimitMiddleware creates a rate limiting middleware
func RateLimitMiddleware(cfg *config.Config, limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.RateLimitEnabled {
				next.ServeHTTP(w, r)
				return
			}

			// Skip rate limiting for excluded paths
			for _, path := range cfg.AuthExcludePaths {
				if r.URL.Path == path {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Determine rate limit key
			key := limiter.getRateLimitKey(r, cfg)
			if key == "" {
				// Can't determine key, allow through
				next.ServeHTTP(w, r)
				return
			}

			allowed, remaining, resetTime := limiter.Allow(key)

			// Set rate limit headers
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(cfg.RateLimitRate))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))

			if !allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(int64(time.Until(resetTime).Seconds())+1, 10))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"code":    "XOLU-RL001",
						"message": "Rate limit exceeded",
						"status":  http.StatusTooManyRequests,
					},
					"retry_after": int(time.Until(resetTime).Seconds()) + 1,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// getRateLimitKey determines the key for rate limiting
func (rl *RateLimiter) getRateLimitKey(r *http.Request, cfg *config.Config) string {
	var parts []string

	// Add IP if enabled
	if cfg.RateLimitByIP {
		ip := rl.getClientIP(r)
		if ip != "" {
			parts = append(parts, "ip:"+ip)
		}
	}

	// Add auth key/subject if enabled
	if cfg.RateLimitByKey {
		subject := GetSubject(r.Context())
		if subject != "" {
			parts = append(parts, "sub:"+subject)
		}
	}

	if len(parts) == 0 {
		// Fall back to IP
		return "ip:" + rl.getClientIP(r)
	}

	// Combine parts
	key := ""
	for i, p := range parts {
		if i > 0 {
			key += "|"
		}
		key += p
	}
	return key
}

// getClientIP extracts the client IP the rate limiter should attribute
// this request to. Trust model (T-38):
//
//   - The TCP peer (r.RemoteAddr) is always authoritative.
//   - X-Forwarded-For and X-Real-IP are consulted ONLY when the peer is
//     in the operator-configured trusted-proxy CIDR list. Otherwise
//     any client could set those headers and forge its rate-limiter
//     identity (GHSA-3fxj-6jh8-hvhx, the reason chi's middleware.RealIP
//     was retired).
//   - When honoured, XFF is walked right-to-left past any hop that is
//     also a trusted proxy, and the first non-trusted address is
//     returned. This yields the actual client behind a proxy chain and
//     resists appending forged hops at either end.
//
// Method receiver rather than free function so it can consult the
// limiter's configured trusted-proxy set.
func (rl *RateLimiter) getClientIP(r *http.Request) string {
	peer := stripPort(r.RemoteAddr)

	if len(rl.trustedProxies) == 0 || !ipInAnyCIDR(peer, rl.trustedProxies) {
		return peer
	}

	// Peer is a trusted proxy: honour XFF, walking past further trusted hops.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		hops := strings.Split(xff, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop := strings.TrimSpace(hops[i])
			if hop == "" {
				continue
			}
			if !ipInAnyCIDR(hop, rl.trustedProxies) {
				return hop
			}
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}

	// Header trust was granted but no header was set — fall back to peer.
	return peer
}

// stripPort removes the ":port" suffix from an address string. Returns
// the input unchanged if it carries no port. IPv6 addresses are handled
// via net.SplitHostPort's brackets convention.
func stripPort(addr string) string {
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// parseTrustedProxies parses a comma-separated list of CIDR ranges.
// Malformed entries are silently skipped; an empty input returns nil.
// Bare IPs are accepted and treated as /32 (v4) or /128 (v6).
func parseTrustedProxies(spec string) []*net.IPNet {
	if spec == "" {
		return nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(part); err == nil {
			out = append(out, cidr)
			continue
		}
		if ip := net.ParseIP(part); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				out = append(out, &net.IPNet{IP: ip4, Mask: net.CIDRMask(32, 32)})
			} else {
				out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
			}
		}
	}
	return out
}

// ipInAnyCIDR reports whether a plain IP string sits in any of the
// supplied CIDR ranges. Returns false on parse failure.
func ipInAnyCIDR(addr string, cidrs []*net.IPNet) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, c := range cidrs {
		if c.Contains(ip) {
			return true
		}
	}
	return false
}
