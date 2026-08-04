// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_dxp_sweeper_test.go — DxpSweeper (T-100, T-102). No sweeper of
// any kind had a test before this file (grepped for
// MetaSweeper/WriterDBProvider usage across every _test.go in the
// tree first — nothing to mirror). Written directly against the
// schema (pkg/storage/sqlite.go's dxp_txn definition) rather than
// through the HTTP API, since the only way to get a row genuinely
// stuck 'active' is the same way it happens for real: something other
// than dispatchDxpTxn's own normal synchronous path leaving it there.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
)

func TestDxpSweeper_MarksStuckActiveInstanceExpired(t *testing.T) {
	env := newDxpServer(t)
	wdp, ok := env.store.(storage.WriterDBProvider)
	if !ok {
		t.Fatalf("test store does not implement storage.WriterDBProvider")
	}
	db := wdp.WriterDB()
	ctx := context.Background()

	pastDeadline := time.Now().UTC().Add(-1 * time.Hour).UnixNano()
	futureDeadline := time.Now().UTC().Add(1 * time.Hour).UnixNano()

	insert := func(id int64, status string, deadline int64) {
		t.Helper()
		_, err := db.ExecContext(ctx, `
			INSERT INTO dxp_txn (tenant_id, id, dxp_def_id, dxp_def_name, snapshot_json, status, committed_through, deadline_ns)
			VALUES (0, ?, 1, 'synthetic', '{}', ?, 0, ?)`,
			id, status, deadline)
		if err != nil {
			t.Fatalf("insert synthetic dxp_txn row %d: %v", id, err)
		}
	}

	// Case 1: stuck 'active' past its deadline -- the crash/panic case
	// this sweeper exists for. Should flip to 'expired'.
	insert(9001, "active", pastDeadline)
	// Case 2: 'active' but NOT past its deadline yet -- a genuinely
	// in-flight instance. Must NOT be touched.
	insert(9002, "active", futureDeadline)
	// Case 3: already terminal, past its deadline -- an ordinary
	// completed instance. Must NOT be touched or re-marked.
	insert(9003, "committed", pastDeadline)

	// retentionSecs=0 here deliberately -- this test is about the
	// expire-in-place behaviour only; retention purging is covered
	// separately below so the two concerns don't get tangled in one
	// test's assertions.
	sweeper := server.NewDxpSweeper(db, 0)
	report, err := sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Collected != 1 {
		t.Errorf("expected exactly 1 row collected, got %d", report.Collected)
	}

	checkStatus := func(id int64, want string) {
		t.Helper()
		var got string
		if err := db.QueryRowContext(ctx,
			`SELECT status FROM dxp_txn WHERE tenant_id = 0 AND id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("query row %d: %v", id, err)
		}
		if got != want {
			t.Errorf("row %d: want status %q, got %q", id, want, got)
		}
	}
	checkStatus(9001, "expired")   // swept
	checkStatus(9002, "active")    // untouched -- not past deadline
	checkStatus(9003, "committed") // untouched -- already terminal
}

// TestDxpSweeper_PurgesTerminalRowsPastRetention covers T-102: a
// configurable tombstone-retention window, direct instruction
// (2026-07-31), defaulting to 48h in production but set explicitly
// short here so the test doesn't need to fake created_at (SQLite sets
// it via CURRENT_TIMESTAMP on insert, not a value this test controls) --
// instead the test sleeps past a deliberately tiny retention window,
// which is fast and deterministic at this timescale.
func TestDxpSweeper_PurgesTerminalRowsPastRetention(t *testing.T) {
	env := newDxpServer(t)
	wdp, ok := env.store.(storage.WriterDBProvider)
	if !ok {
		t.Fatalf("test store does not implement storage.WriterDBProvider")
	}
	db := wdp.WriterDB()
	ctx := context.Background()

	insert := func(id int64, status string) {
		t.Helper()
		_, err := db.ExecContext(ctx, `
			INSERT INTO dxp_txn (tenant_id, id, dxp_def_id, dxp_def_name, snapshot_json, status, committed_through, deadline_ns)
			VALUES (0, ?, 1, 'synthetic', '{}', ?, 0, ?)`,
			id, status, time.Now().UTC().Add(time.Hour).UnixNano())
		if err != nil {
			t.Fatalf("insert synthetic dxp_txn row %d: %v", id, err)
		}
	}

	insert(9101, "committed") // terminal -- eligible for purge once past retention
	insert(9102, "released")  // terminal -- eligible for purge once past retention
	insert(9103, "active")    // NOT terminal -- must never be purged, no matter how old

	// created_at defaults to CURRENT_TIMESTAMP (insert time); a 1-second
	// retention window plus a short sleep reliably pushes all three rows
	// past it without needing to fabricate timestamps.
	time.Sleep(1100 * time.Millisecond)

	sweeper := server.NewDxpSweeper(db, 1) // retentionSecs=1
	report, err := sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Collected != 2 {
		t.Errorf("expected exactly 2 rows purged (committed + released), got %d", report.Collected)
	}

	exists := func(id int64) bool {
		t.Helper()
		var got int64
		err := db.QueryRowContext(ctx,
			`SELECT id FROM dxp_txn WHERE tenant_id = 0 AND id = ?`, id).Scan(&got)
		if err == sql.ErrNoRows {
			return false
		}
		if err != nil {
			t.Fatalf("query row %d: %v", id, err)
		}
		return true
	}
	if exists(9101) {
		t.Errorf("row 9101 (committed, past retention): expected purged, still present")
	}
	if exists(9102) {
		t.Errorf("row 9102 (released, past retention): expected purged, still present")
	}
	if !exists(9103) {
		t.Errorf("row 9103 (active): expected kept regardless of age, was purged")
	}
}

// TestDxpSweeper_ZeroRetentionDisablesPurging confirms retentionSecs
// <= 0 means "keep tombstones forever", matching how this codebase's
// other retention configs (BlobGCGracePeriodSecs) already treat
// non-positive as off, not as "purge immediately".
func TestDxpSweeper_ZeroRetentionDisablesPurging(t *testing.T) {
	env := newDxpServer(t)
	wdp, ok := env.store.(storage.WriterDBProvider)
	if !ok {
		t.Fatalf("test store does not implement storage.WriterDBProvider")
	}
	db := wdp.WriterDB()
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `
		INSERT INTO dxp_txn (tenant_id, id, dxp_def_id, dxp_def_name, snapshot_json, status, committed_through, deadline_ns)
		VALUES (0, 9201, 1, 'synthetic', '{}', 'committed', 0, ?)`,
		time.Now().UTC().Add(time.Hour).UnixNano())
	if err != nil {
		t.Fatalf("insert synthetic dxp_txn row: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	sweeper := server.NewDxpSweeper(db, 0) // retention disabled
	report, err := sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Collected != 0 {
		t.Errorf("expected 0 rows collected with retention disabled, got %d", report.Collected)
	}

	var got int64
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM dxp_txn WHERE tenant_id = 0 AND id = 9201`).Scan(&got); err != nil {
		t.Fatalf("row 9201 should still exist with retention disabled: %v", err)
	}
}
