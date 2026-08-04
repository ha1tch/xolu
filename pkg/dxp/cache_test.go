// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package dxp

import (
	"context"
	"sync"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

func fixedNow(c *MemCache, when time.Time) {
	c.now = func() ot.Instant { return ot.FromTime(when) }
}

func TestHold_ClaimsFor_VisibleAndScoped(t *testing.T) {
	c := NewMemCache()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	fixedNow(c, base)

	c.Lock("t1")
	err := c.Hold(Claim{
		Txn: "txn-1", Primitive: "bal", Tenant: "t1", Resource: "acct:42",
		Weight: Pessimistic, Amount: 500, Deadline: base.Add(time.Minute).UnixNano(),
	})
	c.Unlock("t1")
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}

	got := c.ClaimsFor("t1", "bal", "acct:42")
	if len(got) != 1 || got[0].Txn != "txn-1" {
		t.Fatalf("ClaimsFor same tenant/primitive/resource: got %+v", got)
	}
	if got := c.ClaimsFor("t2", "bal", "acct:42"); len(got) != 0 {
		t.Fatalf("ClaimsFor wrong tenant must be empty, got %+v", got)
	}
	if got := c.ClaimsFor("t1", "cal", "acct:42"); len(got) != 0 {
		t.Fatalf("ClaimsFor wrong primitive must be empty, got %+v", got)
	}
	if got := c.ClaimsFor("t1", "bal", "acct:99"); len(got) != 0 {
		t.Fatalf("ClaimsFor wrong resource must be empty, got %+v", got)
	}
}

func TestClaimsFor_LapsedInvisibleBeforeJanitor(t *testing.T) {
	c := NewMemCache()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	fixedNow(c, base)

	c.Lock("t1")
	_ = c.Hold(Claim{
		Txn: "txn-1", Primitive: "bal", Tenant: "t1", Resource: "acct:42",
		Weight: Pessimistic, Amount: 100, Deadline: base.Add(-time.Second).UnixNano(), // already lapsed
	})
	c.Unlock("t1")

	// No janitor run has happened. ClaimsFor must already hide it —
	// the deadline is authoritative, not the sweep (proposal §5).
	if got := c.ClaimsFor("t1", "bal", "acct:42"); len(got) != 0 {
		t.Fatalf("lapsed claim must be invisible without a janitor run, got %+v", got)
	}
}

func TestClaimsForLocked_NoDeadlockUnderHeldLock(t *testing.T) {
	c := NewMemCache()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	fixedNow(c, base)

	c.Lock("t1")
	defer c.Unlock("t1")
	_ = c.Hold(Claim{
		Txn: "txn-1", Primitive: "bal", Tenant: "t1", Resource: "acct:42",
		Weight: Pessimistic, Amount: 100, Deadline: base.Add(time.Minute).UnixNano(),
	})
	// This is the tier-1 guard path: already holding c.Lock("t1"). The
	// self-locking ClaimsFor would deadlock here; ClaimsForLocked must not.
	got := c.ClaimsForLocked("t1", "bal", "acct:42")
	if len(got) != 1 {
		t.Fatalf("ClaimsForLocked under held lock: got %+v", got)
	}
}

func TestConfirmTxn_RemovesLiveExcludesLapsed(t *testing.T) {
	c := NewMemCache()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	fixedNow(c, base)

	c.Lock("t1")
	_ = c.Hold(Claim{Txn: "txn-1", Primitive: "bal", Tenant: "t1", Resource: "acct:1",
		Deadline: base.Add(time.Minute).UnixNano()})
	_ = c.Hold(Claim{Txn: "txn-1", Primitive: "bal", Tenant: "t1", Resource: "acct:2",
		Deadline: base.Add(-time.Second).UnixNano()}) // lapsed sibling claim
	confirmed := c.ConfirmTxn("t1", "txn-1")
	c.Unlock("t1")

	if len(confirmed) != 1 || confirmed[0].Resource != "acct:1" {
		t.Fatalf("ConfirmTxn must return only live claims, got %+v", confirmed)
	}
	// Both must be gone from the shard now (the lapsed one is dropped,
	// not left to linger for a janitor pass to find later).
	c.Lock("t1")
	remaining := c.ClaimsByTxn("t1", "txn-1")
	c.Unlock("t1")
	if len(remaining) != 0 {
		t.Fatalf("expected no remaining claims for txn-1, got %+v", remaining)
	}
}

func TestReleaseTxn_RemovesLiveAndLapsed(t *testing.T) {
	c := NewMemCache()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	fixedNow(c, base)

	c.Lock("t1")
	_ = c.Hold(Claim{Txn: "txn-1", Primitive: "bal", Tenant: "t1", Resource: "acct:1",
		Deadline: base.Add(time.Minute).UnixNano()})
	_ = c.Hold(Claim{Txn: "txn-1", Primitive: "bal", Tenant: "t1", Resource: "acct:2",
		Deadline: base.Add(-time.Second).UnixNano()})
	released := c.ReleaseTxn("t1", "txn-1")
	c.Unlock("t1")

	if len(released) != 2 {
		t.Fatalf("ReleaseTxn must return both live and lapsed claims, got %+v", released)
	}
}

func TestClaimsByTxn_OtherTxnUnaffected(t *testing.T) {
	c := NewMemCache()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	fixedNow(c, base)

	c.Lock("t1")
	_ = c.Hold(Claim{Txn: "txn-1", Primitive: "bal", Tenant: "t1", Resource: "acct:1",
		Deadline: base.Add(time.Minute).UnixNano()})
	_ = c.Hold(Claim{Txn: "txn-2", Primitive: "bal", Tenant: "t1", Resource: "acct:2",
		Deadline: base.Add(time.Minute).UnixNano()})
	c.ReleaseTxn("t1", "txn-1")
	remaining := c.ClaimsByTxn("t1", "txn-2")
	c.Unlock("t1")

	if len(remaining) != 1 || remaining[0].Txn != "txn-2" {
		t.Fatalf("txn-2's claim must survive txn-1's release, got %+v", remaining)
	}
}

func TestJanitor_TrimsLapsedAcrossTenants(t *testing.T) {
	c := NewMemCache()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	fixedNow(c, base)

	c.Lock("t1")
	_ = c.Hold(Claim{Txn: "a", Primitive: "bal", Tenant: "t1", Resource: "x", Deadline: base.Add(-time.Second).UnixNano()})
	_ = c.Hold(Claim{Txn: "b", Primitive: "bal", Tenant: "t1", Resource: "y", Deadline: base.Add(time.Minute).UnixNano()})
	c.Unlock("t1")

	c.Lock("t2")
	_ = c.Hold(Claim{Txn: "c", Primitive: "cal", Tenant: "t2", Resource: "z", Deadline: base.Add(-time.Second).UnixNano()})
	c.Unlock("t2")

	j := NewJanitor(c)
	report, err := j.Sweep(context.TODO())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Examined != 3 {
		t.Fatalf("expected 3 examined across both tenants, got %d", report.Examined)
	}
	if report.Collected != 2 {
		t.Fatalf("expected 2 lapsed claims collected, got %d", report.Collected)
	}

	c.Lock("t1")
	live := c.ClaimsByTxn("t1", "b")
	c.Unlock("t1")
	if len(live) != 1 {
		t.Fatalf("live claim must survive the sweep, got %+v", live)
	}
}

// TestLock_SerialisesConcurrentHolds is a small in-container
// concurrency check (10-100 iterations, per the working agreement's
// §5.1) — not a substitute for a multi-core race-detector run, but
// enough to catch a broken exclusion mechanically rather than by
// reasoning about it. Run with -race.
func TestLock_SerialisesConcurrentHolds(t *testing.T) {
	c := NewMemCache()
	base := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	fixedNow(c, base)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Lock("t1")
			defer c.Unlock("t1")
			_ = c.Hold(Claim{
				Txn: "txn", Primitive: "bal", Tenant: "t1",
				Resource: "acct:1", Deadline: base.Add(time.Minute).UnixNano(),
			})
		}(i)
	}
	wg.Wait()

	c.Lock("t1")
	got := c.ClaimsByTxn("t1", "txn")
	c.Unlock("t1")
	if len(got) != n {
		t.Fatalf("expected %d claims after %d concurrent Holds, got %d", n, n, len(got))
	}
}
