// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_dxp_dispatch_core_test.go — deterministic adversarial tests
// against dispatchDxpTxnCore directly (T-103's own point: this file
// could not exist before the T-103 refactor). T-99's fix was verified
// two ways before this file existed: a standalone reproduction
// entirely outside dispatchDxpTxn (proving the pattern is unsafe/safe
// in isolation), and an HTTP-level test using only real adapters
// (T-99's own TestDxpTxnAPI_Create_TwoParticipants_DispatchesBothAtomically,
// which cannot force any particular timing). Neither actually forces
// the interleaving that exposed the bug against the real coordinator
// code. This file does.

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/bal"
	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/rs/zerolog"
)

// slowFakeParticipant is a dxp.Participant whose Execute sleeps
// before writing — used to force one participant to still be
// mid-Execute while a sibling (a real, fast bal transfer) finishes
// and, before T-99's fix, would have raced its own real Commit
// against this one's still-in-flight write.
type slowFakeParticipant struct {
	delay time.Duration
}

func (f *slowFakeParticipant) Reserve(ctx context.Context, tnt string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {
	return dxp.Claim{Txn: txn, Primitive: "entity", Tenant: tnt, ParticipantID: participantID,
		Resource: "faketest:" + txn, Weight: w, Amount: 1, Deadline: deadline}, nil
}

func (f *slowFakeParticipant) Validate(ctx context.Context, c dxp.Claim) error { return nil }

func (f *slowFakeParticipant) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	time.Sleep(f.delay)
	sqlStore, ok := store.(*dxp.SQLStore)
	if !ok {
		return nil, fmt.Errorf("slowFakeParticipant: expected sql-backed store, got %s", store.Engine())
	}
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}
	if _, err := sqlStore.Tx.ExecContext(ctx, `INSERT INTO dxp_test_marker (txn) VALUES (?)`, c.Txn); err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *slowFakeParticipant) Release(ctx context.Context, c dxp.Claim) error { return nil }

func (f *slowFakeParticipant) PostCommit(ctx context.Context, c dxp.Claim) error { return nil }

// failingFakeParticipant always fails Execute — used for the
// companion adversarial case: a slow participant that never succeeds
// must still leave nothing committed (T-99's fix strengthened this to
// an all-or-nothing guarantee for the collapsed path specifically).
type failingFakeParticipant struct {
	delay time.Duration
}

func (f *failingFakeParticipant) Reserve(ctx context.Context, tnt string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {
	return dxp.Claim{Txn: txn, Primitive: "entity", Tenant: tnt, ParticipantID: participantID,
		Resource: "faketest-fail:" + txn, Weight: w, Amount: 1, Deadline: deadline}, nil
}

func (f *failingFakeParticipant) Validate(ctx context.Context, c dxp.Claim) error { return nil }

func (f *failingFakeParticipant) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	time.Sleep(f.delay)
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("failingFakeParticipant: deliberate failure")
}

func (f *failingFakeParticipant) Release(ctx context.Context, c dxp.Claim) error { return nil }

func (f *failingFakeParticipant) PostCommit(ctx context.Context, c dxp.Claim) error { return nil }

// dxpCoreTestSetup builds a real SQLite-backed store, a real bal
// store/adapter with two accounts (~in funding, acct receiving), and
// the dxp_test_marker scratch table the fake participants write to.
func dxpCoreTestSetup(t *testing.T) (*storage.SQLiteStore, *bal.Store, *bal.Adapter, *dxp.MemCache) {
	t.Helper()
	st := testDxpStore(t)
	ctx := context.Background()
	db := st.DB()

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS dxp_test_marker (txn TEXT)`); err != nil {
		t.Fatalf("create marker table: %v", err)
	}

	balStore := bal.NewStore(db, 0)
	if err := balStore.Init(ctx); err != nil {
		t.Fatalf("bal Init: %v", err)
	}
	if _, err := balStore.DefineAccount(ctx, bal.AccountDef{ID: "~in", Unit: "unit", Floor: -1000000, Postable: true}); err != nil {
		t.Fatalf("define ~in: %v", err)
	}
	if _, err := balStore.DefineAccount(ctx, bal.AccountDef{ID: "acct", Unit: "unit", Postable: true}); err != nil {
		t.Fatalf("define acct: %v", err)
	}

	cache := dxp.NewMemCache()
	return st, balStore, bal.NewAdapter(balStore, cache), cache
}

// TestDispatchDxpTxnCore_ConcurrentExecuteCommit_SurvivesSlowParticipant
// forces T-99's exact scenario: a fast, real participant (bal
// transfer, index 0 — the store that owns the real Commit) paired
// with a deliberately slow one (index 1). Before the fix, index 0's
// real Commit fired the moment its own Execute finished, with no
// barrier waiting for index 1 — this test's 100ms delay reliably
// exposes that ordering if it still existed. Checked against the
// actual dispatchDxpTxnCore, not a standalone reproduction of the
// pattern elsewhere.
func TestDispatchDxpTxnCore_ConcurrentExecuteCommit_SurvivesSlowParticipant(t *testing.T) {
	st, _, balAdapter, cache := dxpCoreTestSetup(t)
	ctx := context.Background()
	db := st.DB()
	slow := &slowFakeParticipant{delay: 100 * time.Millisecond}

	registry := map[string]dxp.Participant{
		"bal":    balAdapter, // real, fast — index 0 in the snapshot below
		"entity": slow,       // fake, deliberately slow — index 1
	}

	tenantID := tenant.TenantID(0)
	txnID := insertTestDxpTxn(t, st, tenantID)
	txn := strconv.FormatInt(txnID, 10)

	snapshot := dxpTxnSnapshot{
		Pattern: "3ps",
		Participants: []dxpParticipantSpec{
			{ID: "payment", Primitive: "bal", Op: "transfer",
				Params: map[string]interface{}{"from": "~in", "to": "acct", "amount": "50"}},
			{ID: "slowleg", Primitive: "entity", Op: "create",
				Params: map[string]interface{}{"entity": "assets", "data": map[string]interface{}{}}},
		},
		PhaseTTL: dxpPhaseTTLSpec{Reserve: "PT2M"},
	}
	analysis := dxpAnalysis{CollapseEligible: true, EngineHomogeneous: true}
	deadlineNs := time.Now().Add(time.Minute).UnixNano()

	result, err := dispatchDxpTxnCore(ctx, db, nil, nil, nil, cache, registry, tenantID, txnID, snapshot, analysis, deadlineNs, zerolog.Nop())
	if err != nil {
		t.Fatalf("dispatchDxpTxnCore: %v", err)
	}
	if result.Status != "committed" {
		t.Fatalf("expected committed, got %q (reason: %s)", result.Status, result.Reason)
	}
	if result.CommittedThrough != 2 {
		t.Errorf("expected committed_through 2, got %d", result.CommittedThrough)
	}

	// Real side effects from BOTH participants, not just the
	// coordinator's own say-so.
	var markerCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dxp_test_marker WHERE txn = ?`, txn).Scan(&markerCount); err != nil {
		t.Fatalf("query marker: %v", err)
	}
	if markerCount != 1 {
		t.Errorf("expected exactly 1 marker row from the slow participant, got %d", markerCount)
	}

	status, committedThrough := dxpTxnStatus(t, st, tenantID, txnID)
	if status != "committed" || committedThrough != 2 {
		t.Errorf("dxp_txn row: want committed/2, got %s/%d", status, committedThrough)
	}
}

// TestDispatchDxpTxnCore_SlowParticipantFails_NothingCommits is the
// companion adversarial case: the slow participant fails instead of
// succeeding. Post-T-99, this must be all-or-nothing for the
// collapsed path — the fast bal leg must NOT have landed even though
// its own goroutine finished first, since the barrier means Commit is
// never even attempted until every participant's Execute has
// returned.
func TestDispatchDxpTxnCore_SlowParticipantFails_NothingCommits(t *testing.T) {
	st, balStore, balAdapter, cache := dxpCoreTestSetup(t)
	ctx := context.Background()
	db := st.DB()
	slow := &failingFakeParticipant{delay: 100 * time.Millisecond}

	registry := map[string]dxp.Participant{
		"bal":    balAdapter,
		"entity": slow,
	}

	tenantID := tenant.TenantID(0)
	txnID := insertTestDxpTxn(t, st, tenantID)

	snapshot := dxpTxnSnapshot{
		Pattern: "3ps",
		Participants: []dxpParticipantSpec{
			{ID: "payment", Primitive: "bal", Op: "transfer",
				Params: map[string]interface{}{"from": "~in", "to": "acct", "amount": "50"}},
			{ID: "slowleg", Primitive: "entity", Op: "create",
				Params: map[string]interface{}{"entity": "assets", "data": map[string]interface{}{}}},
		},
		PhaseTTL: dxpPhaseTTLSpec{Reserve: "PT2M"},
	}
	analysis := dxpAnalysis{CollapseEligible: true, EngineHomogeneous: true}
	deadlineNs := time.Now().Add(time.Minute).UnixNano()

	result, err := dispatchDxpTxnCore(ctx, db, nil, nil, nil, cache, registry, tenantID, txnID, snapshot, analysis, deadlineNs, zerolog.Nop())
	if err != nil {
		t.Fatalf("dispatchDxpTxnCore: %v", err)
	}
	if result.Status != "expired" {
		t.Fatalf("expected expired, got %q", result.Status)
	}
	if result.CommittedThrough != 0 {
		t.Errorf("expected committed_through 0 (all-or-nothing, T-99's own strengthened guarantee), got %d", result.CommittedThrough)
	}

	var balance int64
	balance, _, err = balStore.Balance(ctx, "acct")
	if err != nil {
		t.Fatalf("query acct balance: %v", err)
	}
	if balance != 0 {
		t.Errorf("acct balance: want 0 (nothing committed), got %d", balance)
	}

	status, committedThrough := dxpTxnStatus(t, st, tenantID, txnID)
	if status != "expired" || committedThrough != 0 {
		t.Errorf("dxp_txn row: want expired/0, got %s/%d", status, committedThrough)
	}
}
