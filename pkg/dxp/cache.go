// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package dxp

import (
	"sync"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// MemCache is the in-memory Cache implementation: one shard per
// tenant, each guarded by its own mutex so unrelated tenants never
// contend. now is a test seam; callers never set it directly — use
// NewMemCache.
type MemCache struct {
	mu     sync.Mutex // protects shards map itself (shard creation only)
	shards map[string]*shard
	now    func() ot.Instant
}

type shard struct {
	mu     sync.Mutex
	claims []Claim // append-mostly; small N per tenant expected (live reservations, not history)
}

// NewMemCache creates an empty cache. Tenants are created lazily on
// first Lock or ClaimsFor.
func NewMemCache() *MemCache {
	return &MemCache{shards: make(map[string]*shard), now: ot.Now}
}

// shardFor returns tenant's shard, creating it on first use. The outer
// mutex is held only for the map lookup/insert, never across a caller's
// critical section — shard creation is O(1) and never blocks on
// unrelated tenant activity.
func (c *MemCache) shardFor(tenant string) *shard {
	c.mu.Lock()
	s, ok := c.shards[tenant]
	if !ok {
		s = &shard{}
		c.shards[tenant] = s
	}
	c.mu.Unlock()
	return s
}

// Lock acquires tenant's write exclusion (see the package doc's
// serialisation-rule discussion). Blocks until available.
func (c *MemCache) Lock(tenant string) {
	c.shardFor(tenant).mu.Lock()
}

// Unlock releases tenant's write exclusion. Unlocking a tenant not
// currently locked by the caller is a programmer error (as with any
// sync.Mutex) and panics via the standard library's own check.
func (c *MemCache) Unlock(tenant string) {
	c.shardFor(tenant).mu.Unlock()
}

// Hold registers a claim. Requires the caller to hold c.Lock(c.Tenant)
// — see the Cache interface doc. No conflict evaluation: the caller's
// guard has already decided.
func (c *MemCache) Hold(cl Claim) error {
	s := c.shardFor(cl.Tenant)
	s.claims = append(s.claims, cl)
	return nil
}

// ClaimsFor returns live claims against one resource. Safe to call
// with or without holding tenant's lock: when the calling goroutine
// already holds it (tier 1), the redundant lock below is skipped by
// using the shard's claims slice directly is NOT attempted here —
// Go's sync.Mutex is not reentrant, so this method takes its own lock
// unconditionally and tier-1 callers must NOT call it while already
// holding c.Lock(tenant) for THIS SAME cache instance in the SAME
// goroutine. Tier-1 guard code that needs claims while holding the
// lock uses ClaimsForLocked instead.
func (c *MemCache) ClaimsFor(tenant, primitive, resource string) []Claim {
	s := c.shardFor(tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	return c.liveMatching(s, primitive, resource)
}

// ClaimsForLocked is ClaimsFor for a caller that already holds
// c.Lock(tenant) in the current goroutine — no internal locking, so it
// cannot deadlock against the caller's own held lock. This is the
// tier-1 guard-evaluation path.
func (c *MemCache) ClaimsForLocked(tenant, primitive, resource string) []Claim {
	s := c.shardFor(tenant)
	return c.liveMatching(s, primitive, resource)
}

func (c *MemCache) liveMatching(s *shard, primitive, resource string) []Claim {
	now := c.now().Time().UnixNano()
	var out []Claim
	for _, cl := range s.claims {
		if cl.Deadline <= now {
			continue // lapsed — invisible unconditionally, no janitor dependency
		}
		if cl.Primitive == primitive && cl.Resource == resource {
			out = append(out, cl)
		}
	}
	return out
}

// ClaimsByTxn returns all live claims held by one instance. Requires
// the caller to hold tenant's lock.
func (c *MemCache) ClaimsByTxn(tenant, txn string) []Claim {
	s := c.shardFor(tenant)
	now := c.now().Time().UnixNano()
	var out []Claim
	for _, cl := range s.claims {
		if cl.Txn == txn && cl.Deadline > now {
			out = append(out, cl)
		}
	}
	return out
}

// ConfirmTxn removes txn's claims as satisfied and returns what was
// removed (lapsed claims are excluded — the coordinator treats an
// empty return where claims were expected as expiry, per the proposal).
// Requires the caller to hold tenant's lock.
func (c *MemCache) ConfirmTxn(tenant, txn string) []Claim {
	return c.removeTxn(tenant, txn, true)
}

// ReleaseTxn removes txn's claims as abandoned — explicit release,
// expiry, or invalidation-by-loss; the reason is the caller's
// bookkeeping. Requires the caller to hold tenant's lock.
func (c *MemCache) ReleaseTxn(tenant, txn string) []Claim {
	return c.removeTxn(tenant, txn, false)
}

// removeTxn removes every claim owned by txn from tenant's shard.
// liveOnly=true (ConfirmTxn) excludes already-lapsed claims from the
// returned set, matching the proposal's "empty return signals expiry"
// contract; ReleaseTxn removes lapsed remnants too so they cannot
// linger to be double-counted by a later janitor pass.
func (c *MemCache) removeTxn(tenant, txn string, liveOnly bool) []Claim {
	s := c.shardFor(tenant)
	now := c.now().Time().UnixNano()
	kept := s.claims[:0:0]
	var removed []Claim
	for _, cl := range s.claims {
		if cl.Txn != txn {
			kept = append(kept, cl)
			continue
		}
		if liveOnly && cl.Deadline <= now {
			continue // lapsed: dropped from the shard, not reported as confirmed
		}
		removed = append(removed, cl)
	}
	s.claims = kept
	return removed
}

// trimLapsed drops every lapsed claim from tenant's shard and returns
// (examined, dropped). Called only by Janitor — this is hygiene
// (bounding memory), never correctness: ClaimsFor already makes
// lapsed claims invisible before the janitor ever runs (proposal §3, §5).
func (c *MemCache) trimLapsed(tenant string) (examined, dropped int) {
	s := c.shardFor(tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := c.now().Time().UnixNano()
	examined = len(s.claims)
	kept := s.claims[:0:0]
	for _, cl := range s.claims {
		if cl.Deadline <= now {
			dropped++
			continue
		}
		kept = append(kept, cl)
	}
	s.claims = kept
	return examined, dropped
}

// tenants returns a snapshot of known tenant keys — used by Janitor to
// sweep every shard without needing its own registry.
func (c *MemCache) tenants() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.shards))
	for t := range c.shards {
		out = append(out, t)
	}
	return out
}
