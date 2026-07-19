// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cache

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// MemoryCache — previously uncovered methods
// ---------------------------------------------------------------------------

func TestMemoryCache_DeletePattern(t *testing.T) {
	cache := NewMemoryCache(100, time.Second*60)
	ctx := context.Background()

	// Populate keys with a common prefix
	cache.Set(ctx, "tenant:001:item:1", "a", time.Minute)
	cache.Set(ctx, "tenant:001:item:2", "b", time.Minute)
	cache.Set(ctx, "tenant:001:item:3", "c", time.Minute)
	cache.Set(ctx, "tenant:002:item:1", "d", time.Minute)

	// Delete all keys matching "tenant:001:*"
	err := cache.DeletePattern(ctx, "tenant:001:*")
	if err != nil {
		t.Fatalf("DeletePattern failed: %v", err)
	}

	// tenant:001 keys should be gone
	for _, key := range []string{"tenant:001:item:1", "tenant:001:item:2", "tenant:001:item:3"} {
		_, err := cache.Get(ctx, key)
		if err == nil {
			t.Errorf("Expected key %q to be deleted", key)
		}
	}

	// tenant:002 key should still exist
	val, err := cache.Get(ctx, "tenant:002:item:1")
	if err != nil {
		t.Fatalf("Expected tenant:002:item:1 to survive, got error: %v", err)
	}
	if val != "d" {
		t.Errorf("Expected 'd', got %v", val)
	}
}

func TestMemoryCache_DeletePatternNoMatch(t *testing.T) {
	cache := NewMemoryCache(100, time.Second*60)
	ctx := context.Background()

	cache.Set(ctx, "alpha:1", "x", time.Minute)

	// Delete with non-matching pattern should be a no-op
	err := cache.DeletePattern(ctx, "beta:*")
	if err != nil {
		t.Fatalf("DeletePattern failed: %v", err)
	}

	val, err := cache.Get(ctx, "alpha:1")
	if err != nil || val != "x" {
		t.Errorf("Expected 'x' to survive, got val=%v err=%v", val, err)
	}
}

func TestMemoryCache_DeletePatternAll(t *testing.T) {
	cache := NewMemoryCache(100, time.Second*60)
	ctx := context.Background()

	cache.Set(ctx, "a:1", "x", time.Minute)
	cache.Set(ctx, "a:2", "y", time.Minute)

	// Pattern with empty prefix after trimming '*' matches everything starting with ""
	err := cache.DeletePattern(ctx, "*")
	if err != nil {
		t.Fatalf("DeletePattern failed: %v", err)
	}

	_, err = cache.Get(ctx, "a:1")
	if err == nil {
		t.Error("Expected all keys to be deleted")
	}
}

func TestMemoryCache_Exists(t *testing.T) {
	cache := NewMemoryCache(100, time.Second*60)
	ctx := context.Background()

	// Key doesn't exist yet
	exists, err := cache.Exists(ctx, "key1")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("Expected key1 to not exist")
	}

	// Set and check
	cache.Set(ctx, "key1", "value1", time.Minute)
	exists, err = cache.Exists(ctx, "key1")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("Expected key1 to exist")
	}

	// Delete and check
	cache.Delete(ctx, "key1")
	exists, err = cache.Exists(ctx, "key1")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("Expected key1 to not exist after delete")
	}
}

func TestMemoryCache_Close(t *testing.T) {
	cache := NewMemoryCache(100, time.Second*60)
	ctx := context.Background()

	cache.Set(ctx, "key1", "value1", time.Minute)
	cache.Set(ctx, "key2", "value2", time.Minute)

	err := cache.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// After close/purge, keys should be gone
	_, err = cache.Get(ctx, "key1")
	if err == nil {
		t.Error("Expected key1 to be gone after Close")
	}
}

// ---------------------------------------------------------------------------
// RedisCache — integration tests
// ---------------------------------------------------------------------------
//
// newTestRedisCache returns a *RedisCache for testing.
//
// Resolution order for the Redis endpoint (first match wins):
//
//  1. XOLU_REDIS_HOST / XOLU_REDIS_PORT — the variables xolu itself reads when
//     configured with CacheType=redis. Set these to run the cache tests against
//     the same Redis instance the server would use.
//
//  2. REDIS_ADDR — convenience variable accepted by many Redis clients and CI
//     setups; format "host:port".
//
//  3. In-process slabbis server — used when neither variable is set, so the
//     tests run without any external dependency.
//
// Examples:
//
//	# xolu-native variables (matches production config)
//	XOLU_REDIS_HOST=redis.example.com XOLU_REDIS_PORT=6380 go test ./pkg/cache/...
//
//	# convenience single-variable form
//	REDIS_ADDR=localhost:6379 go test ./pkg/cache/...
//
//	# default: no env vars, uses slabbis in-process
//	go test ./pkg/cache/...

func newTestRedisCache(t *testing.T) *RedisCache {
	t.Helper()

	host, port, source := resolveRedisAddr()
	if source != "" {
		rc, err := NewRedisCache(host, port, time.Minute, 0, 0)
		if err != nil {
			t.Fatalf("%s set but could not connect to %s:%d: %v", source, host, port, err)
		}
		t.Logf("using real Redis at %s:%d (via %s)", host, port, source)
		t.Cleanup(func() { rc.Close() })
		return rc
	}

	rc, _ := startSlabbis(t)
	return rc
}

// resolveRedisAddr returns (host, port, sourceDescription) for the Redis
// endpoint to use in tests. sourceDescription is empty when no env var is set.
// Resolution order: XOLU_REDIS_HOST/XOLU_REDIS_PORT → REDIS_ADDR → ("", 0, "").
func resolveRedisAddr() (host string, port int, source string) {
	// 1. xolu-native variables — highest priority
	oluHost := os.Getenv("XOLU_REDIS_HOST")
	oluPort := os.Getenv("XOLU_REDIS_PORT")
	if oluHost != "" || oluPort != "" {
		h := oluHost
		if h == "" {
			h = "localhost"
		}
		p := 6379
		if oluPort != "" {
			fmt.Sscan(oluPort, &p)
		}
		return h, p, "XOLU_REDIS_HOST/XOLU_REDIS_PORT"
	}

	// 2. Generic REDIS_ADDR convenience variable
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		h, portStr, err := net.SplitHostPort(addr)
		if err == nil {
			p := 6379
			fmt.Sscan(portStr, &p)
			return h, p, "REDIS_ADDR"
		}
	}

	// 3. No env var set — caller should use slabbis
	return "", 0, ""
}

func TestRedisCache_SetGetDelete(t *testing.T) {
	rc := newTestRedisCache(t)
	defer rc.Close()
	ctx := context.Background()

	// Set
	err := rc.Set(ctx, "test:redis:1", map[string]interface{}{"name": "alice"}, time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get
	val, err := rc.Get(ctx, "test:redis:1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	m, ok := val.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map, got %T", val)
	}
	if m["name"] != "alice" {
		t.Errorf("Expected name 'alice', got %v", m["name"])
	}

	// Delete
	err = rc.Delete(ctx, "test:redis:1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Get after delete
	_, err = rc.Get(ctx, "test:redis:1")
	if err == nil {
		t.Error("Expected error after delete")
	}
}

func TestRedisCache_GetMiss(t *testing.T) {
	rc := newTestRedisCache(t)
	defer rc.Close()
	ctx := context.Background()

	_, err := rc.Get(ctx, "test:redis:nonexistent")
	if err == nil {
		t.Error("Expected error for cache miss")
	}
}

func TestRedisCache_SetWithDefaultTTL(t *testing.T) {
	rc := newTestRedisCache(t)
	defer rc.Close()
	ctx := context.Background()

	// TTL=0 should fall back to the cache's default TTL
	err := rc.Set(ctx, "test:redis:default-ttl", "hello", 0)
	if err != nil {
		t.Fatalf("Set with zero TTL failed: %v", err)
	}

	val, err := rc.Get(ctx, "test:redis:default-ttl")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "hello" {
		t.Errorf("Expected 'hello', got %v", val)
	}

	// Cleanup
	rc.Delete(ctx, "test:redis:default-ttl")
}

func TestRedisCache_ExistsCoverage(t *testing.T) {
	rc := newTestRedisCache(t)
	defer rc.Close()
	ctx := context.Background()

	key := "test:redis:exists-check"

	// Should not exist
	exists, err := rc.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("Expected key to not exist")
	}

	// Set and check
	rc.Set(ctx, key, "val", time.Minute)
	exists, err = rc.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("Expected key to exist")
	}

	// Cleanup
	rc.Delete(ctx, key)
}

func TestRedisCache_DeletePattern(t *testing.T) {
	rc := newTestRedisCache(t)
	defer rc.Close()
	ctx := context.Background()

	// Set multiple keys with a common prefix
	for i := 0; i < 5; i++ {
		rc.Set(ctx, fmt.Sprintf("test:redis:pattern:%d", i), i, time.Minute)
	}
	rc.Set(ctx, "test:redis:other:1", "keep", time.Minute)

	// Delete pattern
	err := rc.DeletePattern(ctx, "test:redis:pattern:*")
	if err != nil {
		t.Fatalf("DeletePattern failed: %v", err)
	}

	// Pattern keys should be gone
	for i := 0; i < 5; i++ {
		_, err := rc.Get(ctx, fmt.Sprintf("test:redis:pattern:%d", i))
		if err == nil {
			t.Errorf("Expected key pattern:%d to be deleted", i)
		}
	}

	// Other key should survive
	val, err := rc.Get(ctx, "test:redis:other:1")
	if err != nil {
		t.Fatalf("Expected other:1 to survive: %v", err)
	}
	if val != "keep" {
		t.Errorf("Expected 'keep', got %v", val)
	}

	// Cleanup
	rc.Delete(ctx, "test:redis:other:1")
}

func TestRedisCache_ComplexValuesCoverage(t *testing.T) {
	rc := newTestRedisCache(t)
	defer rc.Close()
	ctx := context.Background()

	key := "test:redis:complex"
	data := map[string]interface{}{
		"id":     float64(42),
		"name":   "widget",
		"tags":   []interface{}{"a", "b"},
		"nested": map[string]interface{}{"x": float64(1)},
	}

	err := rc.Set(ctx, key, data, time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := rc.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	m, ok := val.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map, got %T", val)
	}
	if m["name"] != "widget" {
		t.Errorf("Expected 'widget', got %v", m["name"])
	}

	// Cleanup
	rc.Delete(ctx, key)
}

func TestRedisCache_Close(t *testing.T) {
	rc := newTestRedisCache(t)

	err := rc.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestNewRedisCache_BadHost(t *testing.T) {
	_, err := NewRedisCache("nonexistent-host", 9999, time.Minute, 0, 0)
	if err == nil {
		t.Error("Expected error for bad Redis host")
	}
}
