// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/tenant"
)

func testAdapter(t *testing.T) (*Store, *dxp.MemCache, *Adapter) {
	t.Helper()
	s := testStore(t)
	mustDefine(t, s, AccountDef{ID: "guest:1", Unit: "USD", Floor: 0, Postable: true})
	mustDefine(t, s, AccountDef{ID: "~received", Unit: "USD", Floor: -1 << 40, Postable: true})
	ctx := context.Background()
	if err := s.Transfer(ctx, "seed", "~received", "guest:1", 1000, "seed", now); err != nil {
		t.Fatalf("seed transfer: %v", err)
	}
	cache := dxp.NewMemCache()
	a := NewAdapter(s, cache)
	return s, cache, a
}

// futureDeadline uses real wall-clock time, not the package's fixed
// historical `now` (2026-07-21, used only for journal timestamps):
// MemCache's default clock is ot.Now (real time), so a deadline
// derived from the fixed `now` would already be in the past and every
// claim would lapse before ClaimsFor ever saw it.
func futureDeadline() int64 { return time.Now().Add(time.Minute).UnixNano() }

func TestAdapter_Reserve_Success(t *testing.T) {
	_, _, a := testAdapter(t)
	cl, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 300},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if cl.Txn != "txn-1" || cl.Primitive != "bal" || cl.Amount != 300 || cl.Resource != "acct:guest:1" {
		t.Fatalf("unexpected claim: %+v", cl)
	}
}

func TestAdapter_Reserve_InsufficientFunds(t *testing.T) {
	_, _, a := testAdapter(t)
	_, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 1500},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if _, ok := err.(*BoundsError); !ok {
		t.Fatalf("expected *BoundsError, got %v (%T)", err, err)
	}
}

func TestAdapter_Reserve_SecondReserveRespectsFirstHold(t *testing.T) {
	_, _, a := testAdapter(t)
	ctx := context.Background()
	if _, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 700},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	// Only 300 left of the 1000 balance; this must be refused, not
	// silently admitted as if txn-1's hold didn't exist.
	_, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 400},
		"txn-2", "p1", futureDeadline(), dxp.Pessimistic)
	if _, ok := err.(*BoundsError); !ok {
		t.Fatalf("expected second reserve to be refused by the first's hold, got %v", err)
	}
}

func TestAdapter_Execute_DoesNotDoubleCountItsOwnClaim(t *testing.T) {
	// Regression test for the self-counting bug caught during review:
	// reserving the FULL available balance must still be executable —
	// if Execute's guarded UPDATE subtracted the claim's own amount a
	// second time on top of the real debit, this would wrongly refuse.
	s, _, a := testAdapter(t)
	ctx := context.Background()
	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 1000},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("reserve full balance: %v", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	balance, _, err := s.Balance(ctx, "guest:1")
	if err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		t.Fatalf("expected guest:1 balance 0 after executing the full-balance transfer, got %d", balance)
	}
}

func TestAdapter_PostCommit_ClearsPending(t *testing.T) {
	s, _, a := testAdapter(t)
	ctx := context.Background()
	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 100},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	tx, _ := s.db.BeginTx(ctx, nil)
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// pending survives Execute now (T-62/T-83): PostCommit needs
	// tp.From/tp.To to fold the rollup deltas after genuine commit --
	// mirrors cal's own Execute/PostCommit lifecycle split (T-108).
	if err := a.PostCommit(ctx, cl); err != nil {
		t.Fatalf("postcommit: %v", err)
	}

	// A second Execute for the same txn must fail cleanly once
	// PostCommit has cleared pending, not silently re-run the transfer.
	tx2, _ := s.db.BeginTx(ctx, nil)
	defer func() { _ = tx2.Rollback() }()
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx2), cl); err == nil {
		t.Fatal("expected second Execute for the same txn to fail (pending cleared by PostCommit)")
	}
}

// TestAdapter_PostCommit_EmitsRollupDeltas proves PostCommit's actual
// substance, not just its pending-map bookkeeping: after a dxp-driven
// transfer commits and PostCommit runs, the rollup plane (@B05) must
// reflect it -- BalanceAsOf is derived entirely from the rollup
// cascade (checkpoint + bucket fold), so a wrong or missing EmitDeltas
// call would surface here as a wrong answer, not just a missing side
// effect.
func TestAdapter_PostCommit_EmitsRollupDeltas(t *testing.T) {
	s := rollupStore(t)
	mustDefine(t, s, AccountDef{ID: "guest:1", Unit: "USD", Floor: 0, Postable: true})
	mustDefine(t, s, AccountDef{ID: "~received", Unit: "USD", Floor: -1 << 40, Postable: true})
	ctx := context.Background()
	if err := s.Transfer(ctx, "seed", "~received", "guest:1", 1000, "seed", now); err != nil {
		t.Fatalf("seed transfer: %v", err)
	}

	cache := dxp.NewMemCache()
	a := NewAdapter(s, cache)

	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 250},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	tx, _ := s.db.BeginTx(ctx, nil)
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := a.PostCommit(ctx, cl); err != nil {
		t.Fatalf("postcommit: %v", err)
	}

	far := time.Now().Add(time.Hour)
	got, err := s.BalanceAsOf(ctx, "guest:1", far)
	if err != nil {
		t.Fatalf("BalanceAsOf: %v", err)
	}
	if want := int64(750); got != want {
		t.Fatalf("guest:1 rollup balance after PostCommit = %d, want %d (PostCommit's EmitDeltas call did not fold the dxp transfer)", got, want)
	}
}

func TestAdapter_Validate_OptimisticAlwaysPasses(t *testing.T) {
	_, _, a := testAdapter(t)
	// A wildly over-budget optimistic claim: Validate must not itself
	// refuse it (§7: optimistic claims are invisible to guard arithmetic).
	cl := dxp.Claim{
		Txn: "txn-x", Primitive: "bal", Tenant: tenant.TenantID(0).String(),
		Resource: "acct:guest:1", Weight: dxp.Optimistic, Amount: 999999999, Deadline: futureDeadline(),
	}
	if err := a.Validate(context.Background(), cl); err != nil {
		t.Fatalf("optimistic Validate must always pass, got %v", err)
	}
}

func TestAdapter_Validate_PessimisticStillHolds(t *testing.T) {
	_, _, a := testAdapter(t)
	ctx := context.Background()
	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 500},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := a.Validate(ctx, cl); err != nil {
		t.Fatalf("Validate immediately after Reserve should pass, got %v", err)
	}
}

func TestAdapter_Release_IdempotentOnUnknownTxn(t *testing.T) {
	_, _, a := testAdapter(t)
	cl := dxp.Claim{Txn: "never-reserved", Primitive: "bal", Tenant: tenant.TenantID(0).String(), Resource: "acct:guest:1"}
	if err := a.Release(context.Background(), cl); err != nil {
		t.Fatalf("Release on unknown txn must be a no-op, got %v", err)
	}
}

// TestOrdinaryTransfer_RespectsLiveDxpHold is the load-bearing proof
// of proposal §4's central requirement: "every write path, not only
// by the coordinator" must see dxp's holds. A plain, non-dxp
// Store.Transfer call must be refused the amount a live pessimistic
// dxp reservation is holding, exactly as if that amount were already
// spent.
func TestOrdinaryTransfer_RespectsLiveDxpHold(t *testing.T) {
	s, _, a := testAdapter(t)
	ctx := context.Background()
	if _, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 700},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Only 300 of the 1000 balance is unclaimed. An ordinary transfer
	// for 400 must be refused, not silently allowed to overcommit.
	err := s.Transfer(ctx, "ordinary-1", "guest:1", "~received", 400, "", now)
	if _, ok := err.(*BoundsError); !ok {
		t.Fatalf("expected ordinary Transfer to be refused by the live dxp hold, got %v", err)
	}

	// 300 must still succeed — exactly what's left.
	if err := s.Transfer(ctx, "ordinary-2", "guest:1", "~received", 300, "", now); err != nil {
		t.Fatalf("expected the exact remaining 300 to succeed, got %v", err)
	}
}
