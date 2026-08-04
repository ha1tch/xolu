// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package reserved

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// testTable creates a minimal convention-adopting table ("slots": a
// contended resource with a name) and returns the open handle.
func testTable(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "reserved_test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ddl := `CREATE TABLE slots (
		slot_id INTEGER PRIMARY KEY AUTOINCREMENT,
		name    TEXT NOT NULL,
		` + ConventionColumns() + `
	);
	` + ConventionIndexes("test_", "slots")
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	return db
}

// reserve inserts a tentative row the way a participant's reserve path
// would, using ReserveValues for the convention columns.
func reserve(t *testing.T, db *sql.DB, name, txnID string, deadline int64) {
	t.Helper()
	tx, st, dl := ReserveValues(txnID, deadline)
	if _, err := db.Exec(
		`INSERT INTO slots (name, txn_id, state, reserve_deadline) VALUES (?, ?, ?, ?)`,
		name, tx, string(st), dl); err != nil {
		t.Fatalf("reserve insert: %v", err)
	}
}

// commitPlain inserts an ordinary committed row via the default —
// exactly what a pre-convention writer does, unchanged.
func commitPlain(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO slots (name) VALUES (?)`, name); err != nil {
		t.Fatalf("plain insert: %v", err)
	}
}

// countWhere counts slots rows matching a predicate fragment with binds.
func countWhere(t *testing.T, db *sql.DB, fragment string, binds ...interface{}) int {
	t.Helper()
	var n int
	q := `SELECT COUNT(*) FROM slots WHERE ` + fragment
	if err := db.QueryRow(q, binds...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func nowPlus(d time.Duration) int64 { return Deadline(ot.Now(), d) }

// ── Lifecycle paths ─────────────────────────────────────────────────

// Test 1: reserved → confirmed. The CAS flips the row to committed,
// clears the deadline, retains txn_id; a subsequent Release is a no-op
// (release after confirm is not a rollback).
func TestLifecycle_ReserveConfirm(t *testing.T) {
	db := testTable(t)
	ctx := context.Background()
	reserve(t, db, "room-101", "txn-A", nowPlus(time.Minute))

	out, n, err := Confirm(ctx, db, "slots", "txn-A", ot.Now())
	if err != nil || out != OutcomeConfirmed || n != 1 {
		t.Fatalf("confirm: out=%v n=%d err=%v", out, n, err)
	}
	if got := countWhere(t, db, `state='committed' AND txn_id='txn-A' AND reserve_deadline IS NULL`); got != 1 {
		t.Fatalf("committed row shape: got %d", got)
	}
	rel, err := Release(ctx, db, "slots", "txn-A")
	if err != nil || rel != 0 {
		t.Fatalf("release after confirm must be a no-op: n=%d err=%v", rel, err)
	}
}

// Test 2: reserved → released. The row is deleted; committed rows under
// other provenance are untouched.
func TestLifecycle_ReserveRelease(t *testing.T) {
	db := testTable(t)
	ctx := context.Background()
	commitPlain(t, db, "room-100")
	reserve(t, db, "room-101", "txn-A", nowPlus(time.Minute))

	n, err := Release(ctx, db, "slots", "txn-A")
	if err != nil || n != 1 {
		t.Fatalf("release: n=%d err=%v", n, err)
	}
	if got := countWhere(t, db, `1=1`); got != 1 {
		t.Fatalf("only the plain committed row should remain, got %d rows", got)
	}
}

// Test 3: reserved → expired via the sweeper. A lapsed reservation is
// collected; a live one and a committed row survive the sweep.
func TestLifecycle_ReserveExpireSweep(t *testing.T) {
	db := testTable(t)
	commitPlain(t, db, "room-100")
	reserve(t, db, "room-101", "txn-lapsed", nowPlus(-time.Second))
	reserve(t, db, "room-102", "txn-live", nowPlus(time.Minute))

	s := NewSweeper()
	s.Register(db, "slots")
	r, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if r.Examined != 2 || r.Collected != 1 || r.Errors != 0 {
		t.Fatalf("report: %+v", r)
	}
	if got := countWhere(t, db, `txn_id='txn-lapsed'`); got != 0 {
		t.Fatalf("lapsed reservation survived the sweep")
	}
	if got := countWhere(t, db, `txn_id='txn-live' AND state='reserved'`); got != 1 {
		t.Fatalf("live reservation did not survive the sweep")
	}
}

// ── Confirm classification ──────────────────────────────────────────

// Test 4: confirming twice is classified, not double-counted — the
// retry sees its own committed rows and reports AlreadyConfirmed.
func TestConfirm_RetryIdempotent(t *testing.T) {
	db := testTable(t)
	ctx := context.Background()
	reserve(t, db, "room-101", "txn-A", nowPlus(time.Minute))

	if out, _, _ := Confirm(ctx, db, "slots", "txn-A", ot.Now()); out != OutcomeConfirmed {
		t.Fatalf("first confirm: %v", out)
	}
	out, n, err := Confirm(ctx, db, "slots", "txn-A", ot.Now())
	if err != nil || out != OutcomeAlreadyConfirmed || n != 1 {
		t.Fatalf("retry: out=%v n=%d err=%v", out, n, err)
	}
}

// Test 5: a txn_id no row carries classifies as Gone.
func TestConfirm_UnknownTxnGone(t *testing.T) {
	db := testTable(t)
	out, _, err := Confirm(context.Background(), db, "slots", "txn-nope", ot.Now())
	if err != nil || out != OutcomeGone {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

// Test 6: the deadline is authoritative over the sweeper. A lapsed but
// UNSWEPT reservation cannot be confirmed — the CAS refuses and the
// outcome names expiry, with the row still physically present.
func TestConfirm_ExpiredAuthoritativeUnswept(t *testing.T) {
	db := testTable(t)
	reserve(t, db, "room-101", "txn-A", nowPlus(-time.Second))

	if got := countWhere(t, db, `txn_id='txn-A' AND state='reserved'`); got != 1 {
		t.Fatalf("precondition: lapsed row must still be present (unswept)")
	}
	out, _, err := Confirm(context.Background(), db, "slots", "txn-A", ot.Now())
	if err != nil || out != OutcomeExpired {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

// ── Guard visibility, both weights ──────────────────────────────────

// Test 7: pessimistic guards honour a live reservation like a commit.
func TestGuardVisibility_PessimisticSeesLiveReserved(t *testing.T) {
	db := testTable(t)
	commitPlain(t, db, "room-100")
	reserve(t, db, "room-101", "txn-A", nowPlus(time.Minute))

	frag, binds := GuardPredicate(Pessimistic)
	if binds != 1 {
		t.Fatalf("pessimistic predicate binds: %d", binds)
	}
	if got := countWhere(t, db, frag, ot.Now().UnixNano()); got != 2 {
		t.Fatalf("pessimistic guard should see committed + live reserved, got %d", got)
	}
}

// Test 8: pessimistic guards stop honouring a reservation the instant
// its deadline lapses — before any sweep. Deadline authority again, on
// the admission side.
func TestGuardVisibility_PessimisticIgnoresLapsedUnswept(t *testing.T) {
	db := testTable(t)
	commitPlain(t, db, "room-100")
	reserve(t, db, "room-101", "txn-A", nowPlus(-time.Second))

	frag, _ := GuardPredicate(Pessimistic)
	if got := countWhere(t, db, frag, ot.Now().UnixNano()); got != 1 {
		t.Fatalf("lapsed unswept reservation must not count, got %d", got)
	}
}

// Test 9: optimistic guards see committed only; conflicting
// reservations coexist invisibly.
func TestGuardVisibility_OptimisticIgnoresReserved(t *testing.T) {
	db := testTable(t)
	commitPlain(t, db, "room-100")
	reserve(t, db, "room-101", "txn-A", nowPlus(time.Minute))
	reserve(t, db, "room-101", "txn-B", nowPlus(time.Minute)) // same slot, coexisting

	frag, binds := GuardPredicate(Optimistic)
	if binds != 0 {
		t.Fatalf("optimistic predicate binds: %d", binds)
	}
	if got := countWhere(t, db, frag); got != 1 {
		t.Fatalf("optimistic guard sees committed only, got %d", got)
	}
}

// ── First confirmer wins ────────────────────────────────────────────

// Test 10: under optimistic weight two reservations of one resource
// coexist; the first confirmation commits, and it is exactly that
// committed state that the loser's validate sees — the serialisation
// point. The loser's claim is then released; one committed row remains.
func TestFirstConfirmerWins_Optimistic(t *testing.T) {
	db := testTable(t)
	ctx := context.Background()
	reserve(t, db, "room-101", "txn-A", nowPlus(time.Minute))
	reserve(t, db, "room-101", "txn-B", nowPlus(time.Minute))

	// A confirms first — wins.
	if out, _, _ := Confirm(ctx, db, "slots", "txn-A", ot.Now()); out != OutcomeConfirmed {
		t.Fatalf("winner confirm: %v", out)
	}

	// B's validate: re-run its conflict predicate over the guard-visible
	// rows (optimistic = committed only). The resource is taken — this is
	// the XOLU-DXP003 discovery, made by B, not for it.
	taken := countWhere(t, db, ApplicationPredicate()+` AND name='room-101'`)
	if taken != 1 {
		t.Fatalf("loser's validate should see the resource taken, got %d", taken)
	}

	// B releases its claim; the winner's committed row is untouched.
	if n, err := Release(ctx, db, "slots", "txn-B"); err != nil || n != 1 {
		t.Fatalf("loser release: n=%d err=%v", n, err)
	}
	if got := countWhere(t, db, `name='room-101'`); got != 1 {
		t.Fatalf("exactly the winner's row should remain, got %d", got)
	}
}

// ── Visibility taxonomy ─────────────────────────────────────────────

// Test 11: PredicateFor implements the three-tier taxonomy: the guard
// plane follows the weight; the advisory plane ingests reservations
// only under pessimistic weight; the analytic plane is commit-fed
// regardless.
func TestVisibilityTaxonomy_PredicateFor(t *testing.T) {
	db := testTable(t)
	commitPlain(t, db, "room-100")
	reserve(t, db, "room-101", "txn-A", nowPlus(time.Minute))

	cases := []struct {
		tier VisibilityTier
		w    Weight
		want int
	}{
		{TierGuard, Pessimistic, 2},
		{TierGuard, Optimistic, 1},
		{TierAdvisory, Pessimistic, 2},
		{TierAdvisory, Optimistic, 1},
		{TierAnalytic, Pessimistic, 1},
		{TierAnalytic, Optimistic, 1},
	}
	for _, c := range cases {
		frag, binds := PredicateFor(c.tier, c.w)
		args := []interface{}{}
		if binds == 1 {
			args = append(args, ot.Now().UnixNano())
		}
		if got := countWhere(t, db, frag, args...); got != c.want {
			t.Fatalf("tier=%d weight=%s: got %d want %d", c.tier, c.w, got, c.want)
		}
	}
}

// ── Sweeper ─────────────────────────────────────────────────────────

// Test 12: the sweeper's report is exact — Examined counts reserved
// rows seen across all registered tables, Collected counts lapsed rows
// deleted, and committed rows are never touched.
func TestSweeper_ReportExact(t *testing.T) {
	db := testTable(t)
	commitPlain(t, db, "room-100")
	reserve(t, db, "a", "t1", nowPlus(-time.Second))
	reserve(t, db, "b", "t2", nowPlus(-time.Second))
	reserve(t, db, "c", "t3", nowPlus(time.Minute))

	s := NewSweeper()
	s.Register(db, "slots")
	r, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if r.Examined != 3 || r.Collected != 2 || r.Errors != 0 {
		t.Fatalf("report: %+v", r)
	}
	if got := countWhere(t, db, `state='committed'`); got != 1 {
		t.Fatalf("committed row touched by sweep")
	}
}

// Test 13: one adopter's failure must not starve the others' hygiene —
// the error is counted and the healthy table is still swept.
func TestSweeper_PerTableErrorDoesNotStarve(t *testing.T) {
	db := testTable(t)
	reserve(t, db, "a", "t1", nowPlus(-time.Second))

	s := NewSweeper()
	s.Register(db, "no_such_table") // registered first; fails
	s.Register(db, "slots")
	r, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep must not abort on per-table failure: %v", err)
	}
	if r.Errors != 1 {
		t.Fatalf("expected 1 per-table error, got %+v", r)
	}
	if r.Collected != 1 {
		t.Fatalf("healthy table was starved: %+v", r)
	}
	if got := fmt.Sprintf("%d", countWhere(t, db, `state='reserved'`)); got != "0" {
		t.Fatalf("lapsed row survived: %s reserved rows remain", got)
	}
}
