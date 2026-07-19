// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package blob

// UsageSampler runs periodic filesystem walks against a single-tenant Store and
// caches the result, so neither the usage API nor the telemetry endpoint ever
// touches the filesystem at request time. The sampler is independent of the GC
// worker — it runs on its own ticker at its own interval.
//
// A Store is single-tenant (its root is the tenant's blobs directory), so the
// sampler caches exactly one SampledUsage. Aggregation across tenants is the
// responsibility of the owner of the per-tenant stores (the blob manager),
// which composes the per-store SampledUsage values into a GlobalUsage.
//
// A zero SampledUsage (SampledAt is zero) is returned before the first walk
// completes. Callers should treat this as "not yet sampled".

import (
	"sync"
	"time"
)

// SampledUsage is the cached result of one store's usage walk.
type SampledUsage struct {
	// BlobCount is the number of distinct blob files on disk (deduplicated).
	BlobCount int64
	// KeyCount is the number of key aliases.
	KeyCount int64
	// Bytes is the total size of all blob files in bytes, excluding sidecars.
	Bytes int64
	// SampledAt is when this entry was last refreshed.
	// Zero means no walk has completed yet.
	SampledAt time.Time
}

// GlobalUsage is an aggregate across stores, suitable for telemetry. It is
// composed by the blob manager from per-store SampledUsage values.
type GlobalUsage struct {
	TotalBlobCount int64
	TotalKeyCount  int64
	TotalBytes     int64
	TenantCount    int64
	SampledAt      time.Time
}

// UsageSampler periodically walks one Store and caches its usage figures.
type UsageSampler struct {
	store    *Store
	interval time.Duration

	mu      sync.RWMutex
	current SampledUsage

	stop chan struct{}
	done chan struct{}
}

// NewUsageSampler creates a UsageSampler for a single-tenant store. Call Start
// to begin sampling.
func NewUsageSampler(s *Store, interval time.Duration) *UsageSampler {
	return &UsageSampler{
		store:    s,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the sampler goroutine. An initial walk runs immediately so
// the cache is warm before the first ticker fires.
func (u *UsageSampler) Start() {
	go u.run()
}

// Stop signals the sampler to stop and blocks until it has exited.
func (u *UsageSampler) Stop() {
	close(u.stop)
	<-u.done
}

// Current returns the most recently cached usage for this store's tenant.
// SampledAt is zero if no walk has completed yet.
func (u *UsageSampler) Current() SampledUsage {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.current
}

// ForceResample runs a usage walk synchronously and updates the cache before
// returning. Intended for tests that need a deterministic cache state without
// waiting for the ticker. Safe to call from any goroutine.
func (u *UsageSampler) ForceResample() {
	u.sample()
}

func (u *UsageSampler) run() {
	defer close(u.done)
	u.sample()
	ticker := time.NewTicker(u.interval)
	defer ticker.Stop()
	for {
		select {
		case <-u.stop:
			return
		case <-ticker.C:
			u.sample()
		}
	}
}

func (u *UsageSampler) sample() {
	usage, err := u.store.Usage()
	if err != nil {
		return
	}
	now := time.Now()
	u.mu.Lock()
	u.current = SampledUsage{
		BlobCount: usage.BlobCount,
		KeyCount:  usage.KeyCount,
		Bytes:     usage.Bytes,
		SampledAt: now,
	}
	u.mu.Unlock()
}
