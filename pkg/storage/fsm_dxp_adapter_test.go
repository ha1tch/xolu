// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// A tiny two-state machine: idle --start--> running --finish--> done,
// with a guard on start (payload.ok must be true) so guard-rejection
// paths are exercisable.
const testFsmSpecJSON = `{
	"name": "adapter-test",
	"initial": "idle",
	"states": {"idle": {}, "running": {}, "done": {"terminal": true}},
	"transitions": [
		{"from": "idle", "input": "start", "to": "running", "guard": "payload.ok = 'yes'"},
		{"from": "running", "input": "finish", "to": "done"}
	]
}`

func testFsmStore(t *testing.T) *SQLiteStore {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "fsm-adapter")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	store, err := NewSQLiteStore(tmpDir+"/fsm.db", SQLiteConfig{DBPath: tmpDir + "/fsm.db"})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema: %v", err)
	}
	return store
}

// testFsmMachine inserts a definition and one machine directly (the
// def/machine creation handlers live in pkg/server, not pkg/storage,
// so this mirrors their shape at the SQL level rather than depending
// on that package). Returns the machine id. tenantID 0 throughout,
// matching SQLiteConfig's "0 = no tenant scoping" default.
func testFsmMachine(t *testing.T, store *SQLiteStore, initialState string) int64 {
	t.Helper()
	ctx := context.Background()

	snapshot, err := json.Marshal(map[string]interface{}{"spec": json.RawMessage(testFsmSpecJSON)})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.DB().ExecContext(ctx,
		`INSERT INTO fsm_definitions (tenant_id, id, name, spec_json) VALUES (0, 1, 'adapter-test', ?)`,
		testFsmSpecJSON); err != nil {
		t.Fatalf("insert def: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx,
		`INSERT INTO fsm_machines (tenant_id, id, fsm_def_id, definition_name, snapshot_json, state, vars_json)
		 VALUES (0, 1, 1, 'adapter-test', ?, ?, '{}')`,
		string(snapshot), initialState); err != nil {
		t.Fatalf("insert machine: %v", err)
	}
	return 1
}

func fsmFutureDeadline() int64 { return time.Now().Add(time.Minute).UnixNano() }

func TestFsmAdapter_Reserve_Success(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)

	cl, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}},
		"txn-1", "p1", fsmFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if cl.Resource != "machine:1" || cl.Primitive != "fsm" {
		t.Fatalf("unexpected claim: %+v", cl)
	}
}

func TestFsmAdapter_Reserve_GuardRejected(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)

	_, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "no"}},
		"txn-1", "p1", fsmFutureDeadline(), dxp.Pessimistic)
	fe, ok := err.(*FsmWalkError)
	if !ok || fe.Code != "XOLU-FSM004" {
		t.Fatalf("expected XOLU-FSM004 guard rejection, got %v (%T)", err, err)
	}
}

func TestFsmAdapter_Reserve_TenantKeyMismatchRefused(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)

	_, err := a.Reserve(context.Background(), "not-the-right-key",
		FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}},
		"txn-1", "p1", fsmFutureDeadline(), dxp.Pessimistic)
	if err == nil {
		t.Fatal("expected a tenant-key mismatch error")
	}
}

func TestFsmAdapter_Reserve_PessimisticExcludesSecondPessimistic(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)
	ctx := context.Background()
	tenant := tenant.TenantID(0).String()
	params := FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}}

	if _, err := a.Reserve(ctx, tenant, params, "txn-1", "p1", fsmFutureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	_, err := a.Reserve(ctx, tenant, params, "txn-2", "p1", fsmFutureDeadline(), dxp.Pessimistic)
	fe, ok := err.(*FsmWalkError)
	if !ok || fe.Code != "XOLU-FSM004" {
		t.Fatalf("expected second pessimistic reserve to be excluded, got %v", err)
	}
}

func TestFsmAdapter_Reserve_OptimisticSiblingsCoexist(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)
	ctx := context.Background()
	tenant := tenant.TenantID(0).String()
	params := FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}}

	if _, err := a.Reserve(ctx, tenant, params, "txn-1", "p1", fsmFutureDeadline(), dxp.Optimistic); err != nil {
		t.Fatalf("first optimistic reserve: %v", err)
	}
	if _, err := a.Reserve(ctx, tenant, params, "txn-2", "p1", fsmFutureDeadline(), dxp.Optimistic); err != nil {
		t.Fatalf("second optimistic reserve should coexist, got %v", err)
	}
}

func TestFsmAdapter_Reserve_OptimisticRefusedByExistingPessimistic(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)
	ctx := context.Background()
	tenant := tenant.TenantID(0).String()
	params := FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}}

	if _, err := a.Reserve(ctx, tenant, params, "txn-1", "p1", fsmFutureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("pessimistic reserve: %v", err)
	}
	_, err := a.Reserve(ctx, tenant, params, "txn-2", "p1", fsmFutureDeadline(), dxp.Optimistic)
	if err == nil {
		t.Fatal("expected optimistic reserve to be refused by the existing pessimistic hold")
	}
}

func TestFsmAdapter_Execute_AdvancesStateAndClearsPending(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)
	ctx := context.Background()

	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}},
		"txn-1", "p1", fsmFutureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var state string
	if err := store.DB().QueryRow(`SELECT state FROM fsm_machines WHERE tenant_id=0 AND id=?`, machineID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "running" {
		t.Fatalf("expected machine advanced to running, got %q", state)
	}

	// Pending must be cleared: a second Execute for the same txn fails.
	tx2, _ := store.DB().BeginTx(ctx, nil)
	defer func() { _ = tx2.Rollback() }()
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx2), cl); err == nil {
		t.Fatal("expected second Execute for the same txn to fail (pending cleared)")
	}
}

func TestFsmAdapter_Execute_CASRefusesIfMachineMoved(t *testing.T) {
	// The load-bearing CAS proof: if the machine's state changes between
	// Reserve and Execute (simulated here by writing it directly, as an
	// ordinary walk elsewhere in the process might), Execute must refuse
	// rather than silently applying a decision made against stale data.
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)
	ctx := context.Background()

	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}},
		"txn-1", "p1", fsmFutureDeadline(), dxp.Optimistic)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Simulate a competitor moving the machine directly (bypassing the
	// adapter entirely, as an unrelated optimistic Execute would).
	if _, err := store.DB().ExecContext(ctx,
		`UPDATE fsm_machines SET state='running' WHERE tenant_id=0 AND id=?`, machineID); err != nil {
		t.Fatal(err)
	}

	tx, _ := store.DB().BeginTx(ctx, nil)
	defer func() { _ = tx.Rollback() }()
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err == nil {
		t.Fatal("expected Execute to refuse: the machine no longer matches the resolved transition's guard/state")
	}
}

func TestFsmAdapter_Release_IdempotentOnUnknownTxn(t *testing.T) {
	store := testFsmStore(t)
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)
	cl := dxp.Claim{Txn: "never-reserved", Primitive: "fsm", Tenant: tenant.TenantID(0).String(), Resource: "machine:1"}
	if err := a.Release(context.Background(), cl); err != nil {
		t.Fatalf("Release on unknown txn must be a no-op, got %v", err)
	}
}

// TestOrdinaryFsmWalk_RefusedByLivePessimisticHold is fsm's version of
// bal's load-bearing cross-path proof: an ordinary (non-dxp)
// FsmWalkInTx call must be refused while a live PESSIMISTIC dxp claim
// holds the machine — "every write path, not only the coordinator,
// must see dxp's holds."
func TestOrdinaryFsmWalk_RefusedByLivePessimisticHold(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)
	ctx := context.Background()

	if _, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}},
		"txn-1", "p1", fsmFutureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = store.FsmWalkInTx(ctx, tx, 0, machineID, "start", map[string]interface{}{"ok": "yes"}, nil)
	fe, ok := err.(*FsmWalkError)
	if !ok || fe.Code != "XOLU-FSM004" {
		t.Fatalf("expected ordinary walk refused by the live pessimistic hold (XOLU-FSM004), got %v", err)
	}
}

// TestOrdinaryFsmWalk_UnaffectedByOptimisticHold confirms the flip
// side: an optimistic claim must NOT block ordinary traffic (§7:
// "guards ignore claims entirely" under optimistic weight).
func TestOrdinaryFsmWalk_UnaffectedByOptimisticHold(t *testing.T) {
	store := testFsmStore(t)
	machineID := testFsmMachine(t, store, "idle")
	cache := dxp.NewMemCache()
	a := NewFsmAdapter(store, cache)
	ctx := context.Background()

	if _, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "start", Payload: map[string]interface{}{"ok": "yes"}},
		"txn-1", "p1", fsmFutureDeadline(), dxp.Optimistic); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := store.FsmWalkInTx(ctx, tx, 0, machineID, "start", map[string]interface{}{"ok": "yes"}, nil); err != nil {
		t.Fatalf("ordinary walk should proceed past an optimistic hold, got %v", err)
	}
}
