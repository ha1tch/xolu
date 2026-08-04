// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package integration proves how much of a /dxp transaction is
// verifiable today WITHOUT the coordinator (item 21, not built): three
// participant adapters (bal, fsm, entity) hand-wired the way a
// coordinator eventually would — one shared *sql.Tx across all three,
// reservations made first, effects applied second, one commit for all
// or nothing for any.
//
// This is explicitly NOT a test of the coordinator, def parsing (item
// 20), the 2PS/3PS phase machine, error-code classification, or the
// mount-time crash-abandon pass — none of that exists yet. It proves
// the SUBSTRATE PRIMITIVES compose correctly: that three independently
// -built adapters, none of which knows the others exist, correctly
// share one transaction's atomicity when driven by hand in the shape a
// coordinator would drive them.
package integration

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/bal"
	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// world bundles one shared SQLite file carrying every primitive's
// tables (proving @D06 composability locality: bal.Store takes an
// EXTERNAL *sql.DB rather than opening its own, which is the seam
// that makes one shared file possible at all) plus one shared dxp
// cache and the three adapters built this session.
type world struct {
	sqlite *storage.SQLiteStore
	bal    *bal.Store
	cache  *dxp.MemCache

	balAdapter    *bal.Adapter
	fsmAdapter    *storage.FsmAdapter
	entityAdapter *storage.EntityAdapter
}

// tenantKey is the ONE canonical dxp tenant key for tenant 0, shared
// by every participant. Until 2026-07-28 this test needed two
// DIFFERENT strings here — bal had no tenantID field, only a derived
// table-prefix string, so its adapter's dxp tenant key and fsm/
// entity's were two independently-invented encodings of the same
// tenant. Fixed at the root: bal.Store now carries tenantID uint16
// like every other primitive, and every primitive's dxp tenant key
// derives from the same pkg/tenant.IDString. This constant existing
// as ONE value, used by all three adapters below, is the proof the
// invariant now holds rather than a description of it.
var tenantKey = tenant.TenantID(0).String()

func newWorld(t *testing.T) *world {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "dxp-integration")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	sqlite, err := storage.NewSQLiteStore(tmpDir+"/shared.db", storage.SQLiteConfig{DBPath: tmpDir + "/shared.db"})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	if err := sqlite.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema: %v", err)
	}

	// The seam: bal.NewStore takes sqlite's OWN *sql.DB rather than
	// opening a second connection/file — this is what makes "one SQL
	// transaction for every participant" (proposal §11) physically
	// possible rather than aspirational.
	balStore := bal.NewStore(sqlite.DB(), 0)
	ctx := context.Background()
	if err := balStore.Init(ctx); err != nil {
		t.Fatalf("bal Init: %v", err)
	}
	// T-62: the rollup plane moved to Pebble, opened separately from
	// the SQL store (composability locality doesn't apply to it — it's
	// advisory, never guard-consulted, so it doesn't need to share
	// the tenant's SQL file the way the guard-bearing tables do).
	rollupPebble, err := bal.OpenRollupPebble(tmpDir + "/bal_rollup")
	if err != nil {
		t.Fatalf("bal OpenRollupPebble: %v", err)
	}
	t.Cleanup(func() { _ = rollupPebble.Close() })
	balStore.SetRollupPebble(rollupPebble)
	if err := balStore.InitRollup(ctx); err != nil {
		t.Fatalf("bal InitRollup: %v", err)
	}

	cache := dxp.NewMemCache()
	w := &world{
		sqlite:        sqlite,
		bal:           balStore,
		cache:         cache,
		balAdapter:    bal.NewAdapter(balStore, cache),
		fsmAdapter:    storage.NewFsmAdapter(sqlite, cache),
		entityAdapter: storage.NewEntityAdapter(sqlite, cache),
	}
	return w
}

const bookingFsmSpec = `{
	"name": "booking",
	"initial": "reserved",
	"states": {"reserved": {}, "confirmed": {"terminal": true}},
	"transitions": [
		{"from": "reserved", "input": "confirm", "to": "confirmed"}
	]
}`

func seedBookingMachine(t *testing.T, sqlite *storage.SQLiteStore) int64 {
	t.Helper()
	ctx := context.Background()
	snapshot, err := json.Marshal(map[string]interface{}{"spec": json.RawMessage(bookingFsmSpec)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.DB().ExecContext(ctx,
		`INSERT INTO fsm_definitions (tenant_id, id, name, spec_json) VALUES (0, 1, 'booking', ?)`,
		bookingFsmSpec); err != nil {
		t.Fatalf("insert fsm def: %v", err)
	}
	if _, err := sqlite.DB().ExecContext(ctx,
		`INSERT INTO fsm_machines (tenant_id, id, fsm_def_id, definition_name, snapshot_json, state, vars_json)
		 VALUES (0, 1, 1, 'booking', ?, 'reserved', '{}')`,
		string(snapshot)); err != nil {
		t.Fatalf("insert fsm machine: %v", err)
	}
	return 1
}

func futureDeadline() int64 { return time.Now().Add(time.Minute).UnixNano() }

// TestMultiParticipant_HotelStyle_AtomicCommit is the fullest /dxp
// transaction shape provable today: three participants, one shared
// tx, one commit. Modelled on the composed-commitment proposal's own
// hotel worked example (§5a) minus the ts observation (execute-only,
// trivial by construction) and minus anything the coordinator would
// own (def parsing, phase sequencing, error classification).
func TestMultiParticipant_HotelStyle_AtomicCommit(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	if _, err := w.bal.DefineAccount(ctx, bal.AccountDef{ID: "guest:1", Unit: "USD", Floor: 0, Postable: true}); err != nil {
		t.Fatalf("define guest:1: %v", err)
	}
	if _, err := w.bal.DefineAccount(ctx, bal.AccountDef{ID: "~received", Unit: "USD", Floor: -1 << 40, Postable: true}); err != nil {
		t.Fatalf("define ~received: %v", err)
	}
	if err := w.bal.Transfer(ctx, "seed", "~received", "guest:1", 1000, "seed", time.Now()); err != nil {
		t.Fatalf("seed guest:1: %v", err)
	}
	machineID := seedBookingMachine(t, w.sqlite)
	noteID, err := w.sqlite.Create(ctx, "booking_note", map[string]interface{}{"note": "pending", "room": "12"})
	if err != nil {
		t.Fatalf("create booking_note: %v", err)
	}

	const txn = "hotel-txn-1"
	deadline := futureDeadline()

	balClaim, err := w.balAdapter.Reserve(ctx, tenantKey,
		bal.TransferParams{From: "guest:1", To: "~received", Amount: 300, Memo: "hotel stay"},
		txn, "p1", deadline, dxp.Pessimistic)
	if err != nil {
		t.Fatalf("bal reserve: %v", err)
	}
	fsmClaim, err := w.fsmAdapter.Reserve(ctx, tenantKey,
		storage.FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "confirm"},
		txn, "p1", deadline, dxp.Pessimistic)
	if err != nil {
		t.Fatalf("fsm reserve: %v", err)
	}
	entityClaim, err := w.entityAdapter.Reserve(ctx, tenantKey,
		storage.EntityUpdateParams{Entity: "booking_note", ID: noteID, Data: map[string]interface{}{"note": "confirmed", "room": "12"}},
		txn, "p1", deadline, dxp.Pessimistic)
	if err != nil {
		t.Fatalf("entity reserve: %v", err)
	}

	tx, err := w.sqlite.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin shared tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := w.balAdapter.Execute(ctx, dxp.NewSharedSQLStore(tx), balClaim); err != nil {
		t.Fatalf("bal execute: %v", err)
	}
	if _, err := w.fsmAdapter.Execute(ctx, dxp.NewSharedSQLStore(tx), fsmClaim); err != nil {
		t.Fatalf("fsm execute: %v", err)
	}
	if _, err := w.entityAdapter.Execute(ctx, dxp.NewSharedSQLStore(tx), entityClaim); err != nil {
		t.Fatalf("entity execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	committed = true

	confirmed := w.cache.ConfirmTxn(tenantKey, txn)
	if len(confirmed) != 3 {
		t.Fatalf("expected all 3 claims (bal+fsm+entity) confirmed together from one shard, got %d", len(confirmed))
	}

	balance, _, err := w.bal.Balance(ctx, "guest:1")
	if err != nil {
		t.Fatal(err)
	}
	if balance != 700 {
		t.Errorf("guest:1 balance: want 700, got %d", balance)
	}

	var state string
	if err := w.sqlite.DB().QueryRow(`SELECT state FROM fsm_machines WHERE tenant_id=0 AND id=?`, machineID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "confirmed" {
		t.Errorf("machine state: want confirmed, got %q", state)
	}

	var noteJSON string
	if err := w.sqlite.DB().QueryRow(`SELECT data FROM `+w.sqlite.NodesTable()+` WHERE entity_type='booking_note' AND id=?`, noteID).Scan(&noteJSON); err != nil {
		t.Fatal(err)
	}
	if !jsonContains(noteJSON, "confirmed") {
		t.Errorf("booking_note data: want it to contain 'confirmed', got %s", noteJSON)
	}
}

// TestMultiParticipant_PartialFailure_NothingCommits is the other
// half of the atomicity claim: if any one participant's Execute fails
// after others already succeeded WITHIN the shared (uncommitted) tx,
// rolling back must discard every effect together — not just the
// failing one.
func TestMultiParticipant_PartialFailure_NothingCommits(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	if _, err := w.bal.DefineAccount(ctx, bal.AccountDef{ID: "guest:1", Unit: "USD", Floor: 0, Postable: true}); err != nil {
		t.Fatalf("define guest:1: %v", err)
	}
	if _, err := w.bal.DefineAccount(ctx, bal.AccountDef{ID: "~received", Unit: "USD", Floor: -1 << 40, Postable: true}); err != nil {
		t.Fatalf("define ~received: %v", err)
	}
	if err := w.bal.Transfer(ctx, "seed", "~received", "guest:1", 1000, "seed", time.Now()); err != nil {
		t.Fatalf("seed guest:1: %v", err)
	}
	machineID := seedBookingMachine(t, w.sqlite)
	noteID, err := w.sqlite.Create(ctx, "booking_note", map[string]interface{}{"note": "pending"})
	if err != nil {
		t.Fatalf("create booking_note: %v", err)
	}

	const txn = "hotel-txn-2"
	deadline := futureDeadline()
	expectVersion := 1

	balClaim, err := w.balAdapter.Reserve(ctx, tenantKey,
		bal.TransferParams{From: "guest:1", To: "~received", Amount: 300},
		txn, "p1", deadline, dxp.Pessimistic)
	if err != nil {
		t.Fatalf("bal reserve: %v", err)
	}
	fsmClaim, err := w.fsmAdapter.Reserve(ctx, tenantKey,
		storage.FsmTransitionParams{TenantID: 0, MachineID: machineID, Input: "confirm"},
		txn, "p1", deadline, dxp.Pessimistic)
	if err != nil {
		t.Fatalf("fsm reserve: %v", err)
	}
	entityClaim, err := w.entityAdapter.Reserve(ctx, tenantKey,
		storage.EntityUpdateParams{Entity: "booking_note", ID: noteID, Data: map[string]interface{}{"note": "confirmed"}, ExpectVersion: &expectVersion},
		txn, "p1", deadline, dxp.Pessimistic)
	if err != nil {
		t.Fatalf("entity reserve: %v", err)
	}

	// Manufacture the failure directly against the row (bypassing the
	// ordinary Save path on purpose — Save would be correctly REFUSED
	// by the live pessimistic entity claim, the cross-path guarantee
	// already proved earlier this session; this simulates drift the
	// only way available without a second dxp instance to drive it).
	if _, err := w.sqlite.DB().ExecContext(ctx,
		`UPDATE `+w.sqlite.NodesTable()+` SET _version = _version + 1 WHERE entity_type='booking_note' AND id=?`, noteID); err != nil {
		t.Fatalf("manufacture drift: %v", err)
	}

	tx, err := w.sqlite.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin shared tx: %v", err)
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			_ = tx.Rollback()
		}
	}()

	if _, err := w.balAdapter.Execute(ctx, dxp.NewSharedSQLStore(tx), balClaim); err != nil {
		t.Fatalf("bal execute (expected to succeed within the tx): %v", err)
	}
	if _, err := w.fsmAdapter.Execute(ctx, dxp.NewSharedSQLStore(tx), fsmClaim); err != nil {
		t.Fatalf("fsm execute (expected to succeed within the tx): %v", err)
	}
	_, err = w.entityAdapter.Execute(ctx, dxp.NewSharedSQLStore(tx), entityClaim)
	if err != storage.ErrConflict {
		t.Fatalf("entity execute: want ErrConflict from the manufactured drift, got %v", err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	rolledBack = true
	released := w.cache.ReleaseTxn(tenantKey, txn)
	if len(released) != 3 {
		t.Fatalf("expected all 3 claims released together (Execute failing does not itself release a claim -- that's the coordinator's job, done here by hand), got %d", len(released))
	}

	balance, _, err := w.bal.Balance(ctx, "guest:1")
	if err != nil {
		t.Fatal(err)
	}
	if balance != 1000 {
		t.Errorf("guest:1 balance must be untouched: want 1000, got %d", balance)
	}

	var state string
	if err := w.sqlite.DB().QueryRow(`SELECT state FROM fsm_machines WHERE tenant_id=0 AND id=?`, machineID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "reserved" {
		t.Errorf("machine state must be untouched: want reserved, got %q", state)
	}

	if err := w.bal.Transfer(ctx, "after", "guest:1", "~received", 50, "", time.Now()); err != nil {
		t.Fatalf("ordinary transfer after rollback+release should succeed, got %v", err)
	}
}

func jsonContains(haystack, needle string) bool {
	var v interface{}
	if err := json.Unmarshal([]byte(haystack), &v); err != nil {
		return false
	}
	b, _ := json.Marshal(v)
	s := string(b)
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
