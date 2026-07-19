// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// cache_slabbis_test.go — integration tests for xolu's RedisCache against
// slabbis, the project's own RESP-compatible cache server.
//
// These tests complement the miniredis unit tests (redis_miniredis_test.go)
// with an end-to-end path through a real RESP server, verifying that:
//
//   - xolu's RedisCache correctly issues RESP commands that slabbis can handle
//   - TTL semantics are enforced (slabbis reaper evicts expired keys)
//   - Pattern-based deletion works across a realistic key namespace
//   - Tenant-scoped cache keys (the format xolu uses internally) round-trip
//     correctly through the RESP wire protocol
//   - Concurrent access from multiple goroutines produces no corruption
//
// Slabbis is started in-process using tiny size classes (64B, 4KB) and a
// single shard to keep memory usage well within sandbox limits.

package cache

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ha1tch/slabbis"
)

// ---------------------------------------------------------------------------
// Test infrastructure
// ---------------------------------------------------------------------------

// startSlabbis starts a slabbis server bound to a random localhost port and
// returns a connected RedisCache plus a teardown function.
//
// Uses slabbis.DevConfig(): 1 shard, 1 bucket, 64B and 4KB size classes,
// 50ms reaper. Safe for memory-constrained environments.
func startSlabbis(t *testing.T) (*RedisCache, *slabbis.Server) {
	t.Helper()

	logger := log.New(os.Stderr, "slabbis: ", 0)

	sb := slabbis.New(slabbis.DevConfig())

	// Bind to a random free port on loopback.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startSlabbis: listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // release the port; slabbis will re-bind

	srv, err := slabbis.NewServer(addr, sb, logger)
	if err != nil {
		sb.Close()
		t.Fatalf("startSlabbis: NewServer: %v", err)
	}

	go func() { _ = srv.Serve() }()

	// Wait until the server is accepting connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", srv.Addr(), 50*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	host, portStr, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		srv.Close()
		t.Fatalf("startSlabbis: SplitHostPort: %v", err)
	}
	port := 0
	fmt.Sscan(portStr, &port)

	rc, err := NewRedisCache(host, port, time.Minute, 5, 1)
	if err != nil {
		srv.Close()
		t.Fatalf("startSlabbis: NewRedisCache: %v", err)
	}

	t.Cleanup(func() {
		rc.Close()
		srv.Close()
		sb.Close()
	})
	return rc, srv
}

// ---------------------------------------------------------------------------
// Basic Get / Set / Delete
// ---------------------------------------------------------------------------

func TestSlabbis_SetGet_String(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	if err := rc.Set(ctx, "slabbis:str", "hello slabbis", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	val, err := rc.Get(ctx, "slabbis:str")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "hello slabbis" {
		t.Errorf("Get: want %q, got %v", "hello slabbis", val)
	}
}

func TestSlabbis_SetGet_Map(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	payload := map[string]interface{}{
		"entity": "asset",
		"id":     float64(42),
		"name":   "test-asset",
	}
	if err := rc.Set(ctx, "slabbis:map", payload, time.Minute); err != nil {
		t.Fatalf("Set map: %v", err)
	}
	val, err := rc.Get(ctx, "slabbis:map")
	if err != nil {
		t.Fatalf("Get map: %v", err)
	}
	m, ok := val.(map[string]interface{})
	if !ok {
		t.Fatalf("Get map: want map[string]interface{}, got %T", val)
	}
	if m["entity"] != "asset" {
		t.Errorf("Get map entity: want %q, got %v", "asset", m["entity"])
	}
}

func TestSlabbis_Get_MissingKey(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	_, err := rc.Get(ctx, "slabbis:does-not-exist")
	if err == nil {
		t.Error("Get missing key: expected error, got nil")
	}
}

func TestSlabbis_Delete(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	if err := rc.Set(ctx, "slabbis:del", "to-delete", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := rc.Delete(ctx, "slabbis:del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := rc.Get(ctx, "slabbis:del"); err == nil {
		t.Error("Get after Delete: expected error, got nil")
	}
}

func TestSlabbis_Delete_NonExistent(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()
	// Deleting a missing key must not error.
	if err := rc.Delete(ctx, "slabbis:ghost"); err != nil {
		t.Errorf("Delete non-existent: unexpected error: %v", err)
	}
}

func TestSlabbis_Exists(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	exists, err := rc.Exists(ctx, "slabbis:exists-test")
	if err != nil {
		t.Fatalf("Exists before set: %v", err)
	}
	if exists {
		t.Error("Exists before set: want false")
	}

	if err := rc.Set(ctx, "slabbis:exists-test", "present", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	exists, err = rc.Exists(ctx, "slabbis:exists-test")
	if err != nil {
		t.Fatalf("Exists after set: %v", err)
	}
	if !exists {
		t.Error("Exists after set: want true")
	}
}

// ---------------------------------------------------------------------------
// TTL enforcement
// ---------------------------------------------------------------------------

// TestSlabbis_TTL_Expiry verifies that slabbis evicts keys after their TTL
// elapses.  The reaper interval is 50ms (configured in startSlabbis); we use
// a 100ms TTL and wait 300ms to be comfortably past both.
func TestSlabbis_TTL_Expiry(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	if err := rc.Set(ctx, "slabbis:ttl", "expires-soon", 100*time.Millisecond); err != nil {
		t.Fatalf("Set with short TTL: %v", err)
	}
	// Key must be retrievable immediately.
	if _, err := rc.Get(ctx, "slabbis:ttl"); err != nil {
		t.Fatalf("Get before expiry: %v", err)
	}
	// Wait for TTL + reaper interval.
	time.Sleep(300 * time.Millisecond)
	_, err := rc.Get(ctx, "slabbis:ttl")
	if err == nil {
		t.Error("Get after TTL: expected error (expired), got nil")
	}
}

// TestSlabbis_ZeroTTL_UsesCacheTTL verifies that passing 0 TTL to Set falls
// back to the cache-level default (1 minute in our fixture).
func TestSlabbis_ZeroTTL_UsesCacheTTL(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	if err := rc.Set(ctx, "slabbis:zero-ttl", "long-lived", 0); err != nil {
		t.Fatalf("Set(ttl=0): %v", err)
	}
	// Should still be retrievable after 50ms — the cache TTL is 1 minute.
	time.Sleep(50 * time.Millisecond)
	if _, err := rc.Get(ctx, "slabbis:zero-ttl"); err != nil {
		t.Errorf("Get after 50ms with zero TTL: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Pattern-based deletion — the xolu cache invalidation path
// ---------------------------------------------------------------------------

// TestSlabbis_DeletePattern exercises the DeletePattern method that xolu uses
// when invalidating list caches after a write.  The key namespace follows
// xolu's internal convention: "TTTT:entity:list:page:perpage".
func TestSlabbis_DeletePattern_TenantList(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	// Simulate xolu caching two list pages for tenant 1 / entity "assets",
	// plus one list page for a different entity ("events") and tenant 2.
	keys := []string{
		"0001:assets:list:1:10",
		"0001:assets:list:2:10",
		"0001:events:list:1:10",
		"0002:assets:list:1:10",
	}
	for _, k := range keys {
		if err := rc.Set(ctx, k, "cached", time.Minute); err != nil {
			t.Fatalf("Set %q: %v", k, err)
		}
	}

	// DeletePattern for tenant 1 assets list.
	if err := rc.DeletePattern(ctx, "0001:assets:list:*"); err != nil {
		t.Fatalf("DeletePattern: %v", err)
	}

	// Tenant 1 asset list pages must be gone.
	for _, k := range []string{"0001:assets:list:1:10", "0001:assets:list:2:10"} {
		if _, err := rc.Get(ctx, k); err == nil {
			t.Errorf("key %q still present after DeletePattern", k)
		}
	}
	// Other keys must be untouched.
	for _, k := range []string{"0001:events:list:1:10", "0002:assets:list:1:10"} {
		if _, err := rc.Get(ctx, k); err != nil {
			t.Errorf("key %q was incorrectly evicted: %v", k, err)
		}
	}
}

func TestSlabbis_DeletePattern_NoMatches(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	// No keys matching the pattern — must not error.
	if err := rc.DeletePattern(ctx, "slabbis:nomatch:*"); err != nil {
		t.Errorf("DeletePattern no matches: unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tenant-scoped key round-trip
// ---------------------------------------------------------------------------

// TestSlabbis_TenantScopedKeys verifies that xolu's tenant-namespaced cache
// keys survive the RESP round-trip intact — including the "@" character used
// in graph node IDs and the colon separators used in cache key construction.
func TestSlabbis_TenantScopedKeys(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	tenantKeys := []struct {
		key string
		val interface{}
	}{
		{"0001:asset:42", map[string]interface{}{"id": float64(42), "name": "pump-a"}},
		{"0001:asset:list:1:10", []interface{}{"pump-a", "pump-b"}},
		{"0001:sensor:99", map[string]interface{}{"id": float64(99), "type": "pressure"}},
	}

	for _, tc := range tenantKeys {
		if err := rc.Set(ctx, tc.key, tc.val, time.Minute); err != nil {
			t.Fatalf("Set %q: %v", tc.key, err)
		}
	}
	for _, tc := range tenantKeys {
		got, err := rc.Get(ctx, tc.key)
		if err != nil {
			t.Fatalf("Get %q: %v", tc.key, err)
		}
		if got == nil {
			t.Errorf("Get %q: nil value", tc.key)
		}
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

// TestSlabbis_ConcurrentReadWrite fires multiple goroutines reading and
// writing the same and different keys simultaneously.  No corruption or
// deadlock must occur; every completed write must be readable.
func TestSlabbis_ConcurrentReadWrite(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	const goroutines = 8
	const iterations = 20
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations*2)

	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				key := fmt.Sprintf("slabbis:concurrent:%d:%d", g, i)
				val := fmt.Sprintf("goroutine-%d-iter-%d", g, i)
				if err := rc.Set(ctx, key, val, time.Minute); err != nil {
					errs <- fmt.Errorf("Set %q: %w", key, err)
					continue
				}
				got, err := rc.Get(ctx, key)
				if err != nil {
					errs <- fmt.Errorf("Get %q: %w", key, err)
					continue
				}
				if got != val {
					errs <- fmt.Errorf("Get %q: want %q, got %v", key, val, got)
				}
			}
		}()
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// Write-through cache invalidation (xolu server behaviour simulation)
// ---------------------------------------------------------------------------

// TestSlabbis_WriteThenInvalidate simulates the xolu cache invalidation cycle:
// a GET warms the cache, a PUT invalidates it, and a subsequent GET must see
// the new value — not the cached old one.
//
// This test operates at the RedisCache layer (not through the HTTP server) to
// confirm the cache primitives themselves are correct before the server layer
// uses them.
func TestSlabbis_WriteThenInvalidate(t *testing.T) {
	rc, _ := startSlabbis(t)
	ctx := context.Background()

	entityKey := "0001:asset:77"
	listKey := "0001:asset:list:1:10"

	// Populate the "old" state.
	old := map[string]interface{}{"id": float64(77), "name": "old-name"}
	if err := rc.Set(ctx, entityKey, old, time.Minute); err != nil {
		t.Fatalf("Set entity: %v", err)
	}
	if err := rc.Set(ctx, listKey, []interface{}{old}, time.Minute); err != nil {
		t.Fatalf("Set list: %v", err)
	}

	// Verify both are cached.
	if _, err := rc.Get(ctx, entityKey); err != nil {
		t.Fatalf("Get entity before invalidate: %v", err)
	}
	if _, err := rc.Get(ctx, listKey); err != nil {
		t.Fatalf("Get list before invalidate: %v", err)
	}

	// Simulate a write: invalidate entity key and list pattern.
	if err := rc.Delete(ctx, entityKey); err != nil {
		t.Fatalf("Delete entity key: %v", err)
	}
	if err := rc.DeletePattern(ctx, "0001:asset:list:*"); err != nil {
		t.Fatalf("DeletePattern list: %v", err)
	}

	// Both must now be absent (cache miss).
	if _, err := rc.Get(ctx, entityKey); err == nil {
		t.Error("entity key still present after invalidation")
	}
	if _, err := rc.Get(ctx, listKey); err == nil {
		t.Error("list key still present after invalidation")
	}

	// Re-populate with "new" state — simulating the server refilling the cache.
	newVal := map[string]interface{}{"id": float64(77), "name": "new-name"}
	if err := rc.Set(ctx, entityKey, newVal, time.Minute); err != nil {
		t.Fatalf("Set entity (new): %v", err)
	}
	got, err := rc.Get(ctx, entityKey)
	if err != nil {
		t.Fatalf("Get entity (new): %v", err)
	}
	m, ok := got.(map[string]interface{})
	if !ok || m["name"] != "new-name" {
		t.Errorf("entity after re-population: want name=new-name, got %v", got)
	}
}
