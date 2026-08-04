// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"testing"

	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// T-138: Execute no longer reads claim sums under the tenant cache
// lock — it consumes a snapshot captured at Reserve and refreshed at
// Validate. These tests pin the snapshot's arithmetic so the deadlock
// fix cannot silently change admission semantics.

// TestDxpSnapshot_ReserveExcludesSelf proves the Reserve-time snapshot
// excludes the claim's own amount without any subtraction: the sums
// are captured BEFORE Hold inserts the claim into the cache.
func TestDxpSnapshot_ReserveExcludesSelf(t *testing.T) {
	_, cache, a := testAdapter(t)
	ctx := context.Background()
	tenantKey := tenant.TenantID(0).String()

	// A pre-existing pessimistic claim from another instance against
	// the same debit account.
	other := dxp.Claim{
		Txn: "other-txn", Primitive: "bal", Tenant: tenantKey, ParticipantID: "p0",
		Resource: dxpResource("guest:1"), Weight: dxp.Pessimistic, Amount: 300, Deadline: futureDeadline(),
	}
	cache.Lock(tenantKey)
	if err := cache.Hold(other); err != nil {
		cache.Unlock(tenantKey)
		t.Fatalf("hold other: %v", err)
	}
	cache.Unlock(tenantKey)

	if _, err := a.Reserve(ctx, tenantKey,
		TransferParams{From: "guest:1", To: "~received", Amount: 500},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	a.mu.Lock()
	p, ok := a.pending[pendingKey("txn-1", "p1")]
	a.mu.Unlock()
	if !ok {
		t.Fatal("no pending entry stashed by Reserve")
	}
	// srcClaimed must be exactly the OTHER claim's amount: self (500)
	// excluded because the snapshot ran before Hold.
	if p.srcClaimed != 300 {
		t.Fatalf("srcClaimed = %d, want 300 (other claim only, self excluded)", p.srcClaimed)
	}
	if p.dstClaimed != 0 {
		t.Fatalf("dstClaimed = %d, want 0", p.dstClaimed)
	}
}

// TestDxpSnapshot_ValidateRefreshes proves Validate (pessimistic path)
// refreshes the snapshot with claims that appeared AFTER Reserve, and
// that the refreshed srcClaimed still excludes self via explicit
// subtraction.
func TestDxpSnapshot_ValidateRefreshes(t *testing.T) {
	_, cache, a := testAdapter(t)
	ctx := context.Background()
	tenantKey := tenant.TenantID(0).String()

	cl, err := a.Reserve(ctx, tenantKey,
		TransferParams{From: "guest:1", To: "~received", Amount: 500},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// A competitor lands against BOTH accounts after our Reserve.
	late1 := dxp.Claim{
		Txn: "late-txn", Primitive: "bal", Tenant: tenantKey, ParticipantID: "p0",
		Resource: dxpResource("guest:1"), Weight: dxp.Pessimistic, Amount: 200, Deadline: futureDeadline(),
	}
	late2 := dxp.Claim{
		Txn: "late-txn", Primitive: "bal", Tenant: tenantKey, ParticipantID: "p2",
		Resource: dxpResource("~received"), Weight: dxp.Pessimistic, Amount: 150, Deadline: futureDeadline(),
	}
	cache.Lock(tenantKey)
	for _, c := range []dxp.Claim{late1, late2} {
		if err := cache.Hold(c); err != nil {
			cache.Unlock(tenantKey)
			t.Fatalf("hold late: %v", err)
		}
	}
	cache.Unlock(tenantKey)

	if err := a.Validate(ctx, cl); err != nil {
		t.Fatalf("validate: %v", err)
	}

	a.mu.Lock()
	p, ok := a.pending[pendingKey("txn-1", "p1")]
	a.mu.Unlock()
	if !ok {
		t.Fatal("no pending entry after Validate")
	}
	// src: self (500) + late1 (200) live in the cache; refresh must
	// record 200 — self subtracted explicitly.
	if p.srcClaimed != 200 {
		t.Fatalf("refreshed srcClaimed = %d, want 200 (late claim only, self subtracted)", p.srcClaimed)
	}
	// dst: late2 (150) is a live pessimistic claim against the credit
	// account; must now be respected.
	if p.dstClaimed != 150 {
		t.Fatalf("refreshed dstClaimed = %d, want 150", p.dstClaimed)
	}
}

// TestDxpSnapshot_ExecuteTakesNoTenantLock proves Execute completes
// while another goroutine HOLDS the tenant cache lock for the whole
// duration — the exact acquisition that closed T-138's AB/BA cycle.
// Before the fix this test deadlocks (Execute blocks on the held
// lock); with it, Execute's only cache dependency is the snapshot.
func TestDxpSnapshot_ExecuteTakesNoTenantLock(t *testing.T) {
	s, cache, a := testAdapter(t)
	ctx := context.Background()
	tenantKey := tenant.TenantID(0).String()

	cl, err := a.Reserve(ctx, tenantKey,
		TransferParams{From: "guest:1", To: "~received", Amount: 500},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := a.Validate(ctx, cl); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// Hold the tenant lock for Execute's entire run. If Execute ever
	// reintroduces a cache.Lock acquisition, this test hangs here and
	// the package's own test timeout turns that into a failure, not a
	// silent pass.
	cache.Lock(tenantKey)
	defer cache.Unlock(tenantKey)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		t.Fatalf("execute under held tenant lock: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
