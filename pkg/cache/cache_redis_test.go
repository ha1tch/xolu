// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

//go:build redis

package cache

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// Redis Cache Tests
// ============================================================================

// These tests require a running Redis instance.
//
// By default they connect to localhost:6379. Set REDIS_ADDR=host:port to
// point at a different instance. Run with:
//
//	go test -tags=redis ./pkg/cache/...
//	REDIS_ADDR=redis.example.com:6379 go test -tags=redis ./pkg/cache/...

// newRealRedisCache connects to the Redis instance determined by resolveRedisAddr
// (defined in cache_coverage_test.go). Resolution order: XOLU_REDIS_HOST/XOLU_REDIS_PORT,
// then REDIS_ADDR, then localhost:6379. Fails the test immediately if the connection fails.
func newRealRedisCache(t *testing.T) *RedisCache {
	t.Helper()
	host, port, source := resolveRedisAddr()
	if source == "" {
		// No env var set — fall back to the default localhost:6379 for the
		// -tags redis path, which is always an explicit opt-in for network testing.
		host, port, source = "localhost", 6379, "default (localhost:6379)"
	}
	rc, err := NewRedisCache(host, port, time.Second*60, 0, 0)
	if err != nil {
		t.Fatalf("could not connect to Redis at %s:%d (via %s): %v", host, port, source, err)
	}
	t.Cleanup(func() { rc.Close() })
	return rc
}

func TestRealRedisCache_SetGet(t *testing.T) {
	cache := newRealRedisCache(t)
	var err error

	ctx := context.Background()

	// Clean up any existing test key
	cache.Delete(ctx, "test:key1")

	// Set a value
	err = cache.Set(ctx, "test:key1", "value1", time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Get the value
	val, err := cache.Get(ctx, "test:key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "value1" {
		t.Errorf("Expected 'value1', got '%v'", val)
	}

	// Clean up
	cache.Delete(ctx, "test:key1")
}

func TestRealRedisCache_PerItemTTL(t *testing.T) {
	// Create cache with long default TTL
	cache := newRealRedisCache(t)
	var err error

	ctx := context.Background()

	// Clean up
	cache.Delete(ctx, "test:shortttl")

	// Set with short per-item TTL (Redis honours this)
	err = cache.Set(ctx, "test:shortttl", "expires-soon", time.Millisecond*100)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Should exist immediately
	_, err = cache.Get(ctx, "test:shortttl")
	if err != nil {
		t.Fatalf("Get failed immediately after set: %v", err)
	}

	// Wait for expiration
	time.Sleep(time.Millisecond * 200)

	// Should be expired (Redis per-item TTL works)
	_, err = cache.Get(ctx, "test:shortttl")
	if err == nil {
		t.Error("Expected error for expired key - Redis should honour per-item TTL")
	}
}

func TestRealRedisCache_Delete(t *testing.T) {
	cache := newRealRedisCache(t)
	var err error

	ctx := context.Background()

	cache.Set(ctx, "test:delete", "value", time.Minute)

	err = cache.Delete(ctx, "test:delete")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = cache.Get(ctx, "test:delete")
	if err == nil {
		t.Error("Expected error after delete")
	}
}

func TestRealRedisCache_Exists(t *testing.T) {
	cache := newRealRedisCache(t)
	var err error

	ctx := context.Background()

	// Clean up
	cache.Delete(ctx, "test:exists")

	// Should not exist
	exists, err := cache.Exists(ctx, "test:exists")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("Key should not exist")
	}

	// Set it
	cache.Set(ctx, "test:exists", "value", time.Minute)

	// Should exist now
	exists, err = cache.Exists(ctx, "test:exists")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("Key should exist")
	}

	// Clean up
	cache.Delete(ctx, "test:exists")
}

func TestRealRedisCache_ComplexValues(t *testing.T) {
	cache := newRealRedisCache(t)
	var err error

	ctx := context.Background()

	// Clean up
	cache.Delete(ctx, "test:complex")

	// Store a map (Redis cache serialises to JSON)
	data := map[string]interface{}{
		"id":   float64(1), // JSON numbers become float64
		"name": "test",
	}

	err = cache.Set(ctx, "test:complex", data, time.Minute)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := cache.Get(ctx, "test:complex")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	retrieved, ok := val.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map, got %T", val)
	}
	if retrieved["name"] != "test" {
		t.Errorf("Expected name 'test', got '%v'", retrieved["name"])
	}

	// Clean up
	cache.Delete(ctx, "test:complex")
}

// ============================================================================
// Redis Stress Tests
// ============================================================================

func TestRealRedisStress_ConcurrentAccess(t *testing.T) {
	cache := newRealRedisCache(t)

	ctx := context.Background()
	numWorkers := 50
	opsPerWorker := 100
	var wg sync.WaitGroup
	errors := make(chan error, numWorkers*opsPerWorker)

	start := time.Now()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				key := fmt.Sprintf("stress:%d:%d", workerID, i)
				value := fmt.Sprintf("value-%d-%d", workerID, i)

				// Set
				if err := cache.Set(ctx, key, value, time.Minute); err != nil {
					errors <- fmt.Errorf("Set failed: %v", err)
					continue
				}

				// Get
				got, err := cache.Get(ctx, key)
				if err != nil {
					errors <- fmt.Errorf("Get failed: %v", err)
					continue
				}
				if got != value {
					errors <- fmt.Errorf("Value mismatch: expected %s, got %v", value, got)
				}

				// Delete
				if err := cache.Delete(ctx, key); err != nil {
					errors <- fmt.Errorf("Delete failed: %v", err)
				}
			}
		}(w)
	}

	wg.Wait()
	close(errors)

	elapsed := time.Since(start)
	totalOps := numWorkers * opsPerWorker * 3 // Set + Get + Delete
	opsPerSec := float64(totalOps) / elapsed.Seconds()

	var errCount int
	for err := range errors {
		errCount++
		if errCount <= 5 {
			t.Logf("Error: %v", err)
		}
	}

	t.Logf("Concurrent stress: %d workers x %d ops = %d total ops", numWorkers, opsPerWorker, totalOps)
	t.Logf("Completed in %v (%.0f ops/sec)", elapsed, opsPerSec)
	t.Logf("Errors: %d", errCount)

	if errCount > 0 {
		t.Errorf("Had %d errors during stress test", errCount)
	}
}

func TestRealRedisStress_LargeValues(t *testing.T) {
	cache := newRealRedisCache(t)

	ctx := context.Background()

	// Test various payload sizes
	sizes := []int{1024, 10 * 1024, 100 * 1024, 1024 * 1024} // 1KB, 10KB, 100KB, 1MB

	for _, size := range sizes {
		key := fmt.Sprintf("stress:large:%d", size)

		// Create payload of specified size using printable ASCII
		// (avoids JSON escaping issues with binary data)
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte('A' + (i % 26))
		}
		value := string(payload)

		start := time.Now()

		// Set
		if err := cache.Set(ctx, key, value, time.Minute); err != nil {
			t.Errorf("Set %d bytes failed: %v", size, err)
			continue
		}
		setTime := time.Since(start)

		// Get
		start = time.Now()
		got, err := cache.Get(ctx, key)
		if err != nil {
			t.Errorf("Get %d bytes failed: %v", size, err)
			continue
		}
		getTime := time.Since(start)

		gotStr, ok := got.(string)
		if !ok {
			t.Errorf("Expected string, got %T", got)
			continue
		}
		if len(gotStr) != size {
			t.Errorf("Size mismatch: expected %d, got %d", size, len(gotStr))
		}

		// Clean up
		cache.Delete(ctx, key)

		t.Logf("Payload %7d bytes: Set %v, Get %v", size, setTime, getTime)
	}
}

func TestRealRedisStress_PatternDelete(t *testing.T) {
	cache := newRealRedisCache(t)

	ctx := context.Background()
	numKeys := 500

	// Create many keys with a common prefix
	start := time.Now()
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("stress:pattern:%d", i)
		if err := cache.Set(ctx, key, i, time.Minute); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}
	setTime := time.Since(start)

	// Verify a few exist
	for _, i := range []int{0, 100, 499} {
		key := fmt.Sprintf("stress:pattern:%d", i)
		exists, err := cache.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if !exists {
			t.Errorf("Key %s should exist", key)
		}
	}

	// Delete by pattern
	start = time.Now()
	if err := cache.DeletePattern(ctx, "stress:pattern:*"); err != nil {
		t.Fatalf("DeletePattern failed: %v", err)
	}
	deleteTime := time.Since(start)

	// Verify all are gone
	for _, i := range []int{0, 100, 499} {
		key := fmt.Sprintf("stress:pattern:%d", i)
		exists, err := cache.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Exists failed: %v", err)
		}
		if exists {
			t.Errorf("Key %s should be deleted", key)
		}
	}

	t.Logf("Pattern delete: Created %d keys in %v, deleted in %v", numKeys, setTime, deleteTime)
}

func TestRealRedisStress_RapidReconnect(t *testing.T) {
	// Verify connectivity before spawning goroutines — a missing Redis would
	// produce 50 goroutine errors that obscure the root cause.
	probe := newRealRedisCache(t)
	probe.Close()

	ctx := context.Background()
	numConnections := 50

	start := time.Now()
	var wg sync.WaitGroup
	errors := make(chan error, numConnections)

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(connID int) {
			defer wg.Done()

			h, p, src := resolveRedisAddr()
			if src == "" {
				h, p = "localhost", 6379
			}
			cache, err := NewRedisCache(h, p, time.Second*60, 0, 0)
			if err != nil {
				errors <- fmt.Errorf("Connection %d failed: %v", connID, err)
				return
			}

			// Do a quick operation
			key := fmt.Sprintf("reconnect:%d", connID)
			if err := cache.Set(ctx, key, connID, time.Second*10); err != nil {
				errors <- fmt.Errorf("Set %d failed: %v", connID, err)
			}

			cache.Delete(ctx, key)
			cache.Close()
		}(i)
	}

	wg.Wait()
	close(errors)

	elapsed := time.Since(start)

	var errCount int
	for err := range errors {
		errCount++
		if errCount <= 5 {
			t.Logf("Error: %v", err)
		}
	}

	t.Logf("Rapid reconnect: %d connections in %v (%.0f conn/sec)",
		numConnections, elapsed, float64(numConnections)/elapsed.Seconds())

	if errCount > 0 {
		t.Errorf("Had %d connection errors", errCount)
	}
}
