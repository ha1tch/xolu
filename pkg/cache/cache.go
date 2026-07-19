// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cache

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// Cache interface defines caching operations.
//
// Two implementations are provided:
//   - MemoryCache: In-process LRU cache with per-item TTL support
//   - RedisCache: Distributed cache with per-item TTL support
//
// Use MemoryCache for development and single-instance deployments.
// Use RedisCache for horizontal scaling.
type Cache interface {
	Get(ctx context.Context, key string) (interface{}, error)
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	DeletePattern(ctx context.Context, pattern string) error
	Exists(ctx context.Context, key string) (bool, error)
	Close() error
}

// cacheEntry holds a cached value with its expiry time and LRU list pointer.
type cacheEntry struct {
	value     interface{}
	expiresAt time.Time // zero means no expiry
	key       string    // back-pointer for LRU eviction
	element   *list.Element
}

// MemoryCache is an in-process LRU cache with per-item TTL.
//
// Each call to Set may specify an independent TTL. A zero TTL means the
// item never expires (it may still be evicted if the cache is at capacity).
// A negative TTL is treated as zero (no expiry).
//
// When capacity is reached, the least-recently-used item is evicted.
//
// A background goroutine sweeps expired entries every sweepInterval. The
// goroutine is stopped when Close is called.
type MemoryCache struct {
	mu         sync.Mutex
	items      map[string]*cacheEntry
	lruList    *list.List // front = most recently used
	capacity   int
	sweepStop  chan struct{}
	defaultTTL time.Duration // used when Set receives ttl == 0
}

const defaultSweepInterval = 30 * time.Second

// NewMemoryCache creates a new in-memory LRU cache.
//
// capacity is the maximum number of items. When full, the LRU item is evicted.
// defaultTTL is used when Set is called with a zero TTL; pass 0 for no expiry.
func NewMemoryCache(capacity int, defaultTTL time.Duration) *MemoryCache {
	if capacity <= 0 {
		capacity = 1024
	}
	c := &MemoryCache{
		items:      make(map[string]*cacheEntry, capacity),
		lruList:    list.New(),
		capacity:   capacity,
		sweepStop:  make(chan struct{}),
		defaultTTL: defaultTTL,
	}
	go c.sweeper(defaultSweepInterval)
	return c
}

// sweeper periodically removes expired entries.
func (m *MemoryCache) sweeper(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.deleteExpired()
		case <-m.sweepStop:
			return
		}
	}
}

func (m *MemoryCache) deleteExpired() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.items {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			m.lruList.Remove(e.element)
			delete(m.items, k)
		}
	}
}

// Get retrieves a value. Returns an error on miss or if the item has expired.
func (m *MemoryCache) Get(ctx context.Context, key string) (interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[key]
	if !ok {
		return nil, fmt.Errorf("key not found")
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		m.lruList.Remove(e.element)
		delete(m.items, key)
		return nil, fmt.Errorf("key not found")
	}
	m.lruList.MoveToFront(e.element)
	return e.value, nil
}

// Set stores a value with the given TTL.
// If ttl is zero, the item inherits the cache's defaultTTL.
// If both ttl and defaultTTL are zero, the item never expires.
func (m *MemoryCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if ttl == 0 {
		ttl = m.defaultTTL
	}
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e, ok := m.items[key]; ok {
		// Update in place — move to front of LRU list.
		e.value = value
		e.expiresAt = expiresAt
		m.lruList.MoveToFront(e.element)
		return nil
	}

	// Evict LRU if at capacity.
	if len(m.items) >= m.capacity {
		oldest := m.lruList.Back()
		if oldest != nil {
			old := oldest.Value.(*cacheEntry)
			m.lruList.Remove(oldest)
			delete(m.items, old.key)
		}
	}

	e := &cacheEntry{value: value, expiresAt: expiresAt, key: key}
	e.element = m.lruList.PushFront(e)
	m.items[key] = e
	return nil
}

// Delete removes a key from the cache.
func (m *MemoryCache) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.items[key]; ok {
		m.lruList.Remove(e.element)
		delete(m.items, key)
	}
	return nil
}

// DeletePattern removes all keys that have the given prefix (pattern must end with "*").
func (m *MemoryCache) DeletePattern(ctx context.Context, pattern string) error {
	prefix := strings.TrimSuffix(pattern, "*")
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.items {
		if strings.HasPrefix(k, prefix) {
			m.lruList.Remove(e.element)
			delete(m.items, k)
		}
	}
	return nil
}

// Exists reports whether a non-expired key is present.
func (m *MemoryCache) Exists(ctx context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[key]
	if !ok {
		return false, nil
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		m.lruList.Remove(e.element)
		delete(m.items, key)
		return false, nil
	}
	return true, nil
}

// Close stops the background sweeper and purges all entries.
func (m *MemoryCache) Close() error {
	close(m.sweepStop)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = make(map[string]*cacheEntry)
	m.lruList.Init()
	return nil
}

// RedisCache implements a Redis-backed cache
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisCache creates a new Redis cache.
// poolSize and minIdleConns control the connection pool; zero values use
// defaults of 50 and 10 respectively.
func NewRedisCache(host string, port int, ttl time.Duration, poolSize int, minIdleConns int) (*RedisCache, error) {
	if poolSize <= 0 {
		poolSize = 50
	}
	if minIdleConns <= 0 {
		minIdleConns = 10
	}
	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%d", host, port),
		PoolSize:     poolSize,
		MinIdleConns: minIdleConns,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisCache{
		client: client,
		ttl:    ttl,
	}, nil
}

// Get retrieves a value from Redis
func (r *RedisCache) Get(ctx context.Context, key string) (interface{}, error) {
	val, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("key not found")
	}
	if err != nil {
		return nil, err
	}

	var result interface{}
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Set stores a value in Redis with the given TTL.
// A zero TTL falls back to the cache's default TTL.
func (r *RedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	if ttl == 0 {
		ttl = r.ttl
	}

	return r.client.Set(ctx, key, data, ttl).Err()
}

// Delete removes a key from Redis
func (r *RedisCache) Delete(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

// DeletePattern removes all keys matching a pattern
func (r *RedisCache) DeletePattern(ctx context.Context, pattern string) error {
	var cursor uint64
	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

// Exists checks if a key exists in Redis
func (r *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	count, err := r.client.Exists(ctx, key).Result()
	return count > 0, err
}

// Close closes the Redis connection
func (r *RedisCache) Close() error {
	return r.client.Close()
}

// Len returns the number of non-expired items currently in the cache.
// Items that have expired but not yet been swept are excluded.
func (m *MemoryCache) Len() int {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, e := range m.items {
		if e.expiresAt.IsZero() || !now.After(e.expiresAt) {
			count++
		}
	}
	return count
}
