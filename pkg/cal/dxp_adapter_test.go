// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal_test

// dxp_adapter_test.go exercises cal's dxp.Participant against a real
// SQLite-backed store (openSQLiteSource, sqlitesource_test.go's own
// helper) -- not MemBookingSource -- since putBookingInTx is what's
// actually under test here, and only SQLiteBookingSource has it.

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// testCalAdapter builds its own SQLite store directly (rather than
// reusing sqlitesource_test.go's openSQLiteSource, which does not
// expose the raw *sql.DB that Execute's shared-tx test needs) --
// same construction shape, kept separate to avoid changing a helper
// four other tests already depend on.
func testCalAdapter(t *testing.T) (*cal.SQLiteBookingSource, *sql.DB, *dxp.MemCache, *cal.Adapter) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cal-dxp-adapter")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	store, err := storage.NewSQLiteStore(dir+"/store.db", storage.SQLiteConfig{DBPath: dir + "/store.db"})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema: %v", err)
	}

	src := cal.NewSQLiteBookingSource(store.DB(), tenant.TenantID(0), false)
	if _, err := src.CreateCalendar(cal.Calendar{
		CalendarID:   "room-a",
		DefaultState: cal.StateBinding,
		MatchPolicy:  cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("create calendar: %v", err)
	}
	lc := cal.NewLifecycle(src, nil) // index not needed: adapter never touches H3
	cache := dxp.NewMemCache()
	a := cal.NewAdapter(lc, src, cache)
	return src, store.DB(), cache, a
}

func futureDeadline() int64 { return time.Now().Add(time.Minute).UnixNano() }

func TestCalAdapter_Reserve_Success(t *testing.T) {
	_, _, _, a := testCalAdapter(t)
	span := cal.Span{
		Start: mustInstant(t, "2026-08-01T09:00:00Z"),
		End:   mustInstant(t, "2026-08-01T10:00:00Z"),
	}
	cl, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		cal.CalTransitionParams{CalendarID: "room-a", BookingID: "b1", Span: span, Bearer: 1},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if cl.Primitive != "cal" || cl.Txn != "txn-1" {
		t.Fatalf("unexpected claim: %+v", cl)
	}
}

func TestCalAdapter_Reserve_MultiDaySpan_HoldsOneClaimPerDay(t *testing.T) {
	_, _, cache, a := testCalAdapter(t)
	// Spans three calendar days: Aug 1 22:00 -> Aug 3 02:00.
	span := cal.Span{
		Start: mustInstant(t, "2026-08-01T22:00:00Z"),
		End:   mustInstant(t, "2026-08-03T02:00:00Z"),
	}
	if _, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		cal.CalTransitionParams{CalendarID: "room-a", BookingID: "b1", Span: span, Bearer: 1},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	claims := cache.ClaimsByTxn(tenant.TenantID(0).String(), "txn-1")
	if len(claims) != 3 {
		t.Fatalf("expected 3 day-bucket claims for a 3-day span, got %d: %+v", len(claims), claims)
	}
}

func TestCalAdapter_Reserve_RefusesAgainstLiveOrdinaryBooking(t *testing.T) {
	src, _, _, a := testCalAdapter(t)
	span := cal.Span{
		Start: mustInstant(t, "2026-08-01T09:00:00Z"),
		End:   mustInstant(t, "2026-08-01T10:00:00Z"),
	}
	// An ordinary (non-dxp) booking already occupies this span.
	if err := src.PutBooking(cal.Booking{
		BookingID: "existing", CalendarID: "room-a", State: cal.StateBinding,
		Span: span, Mode: cal.ModeExclusive, Bearer: 1,
	}); err != nil {
		t.Fatalf("seed ordinary booking: %v", err)
	}

	_, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		cal.CalTransitionParams{CalendarID: "room-a", BookingID: "b1", Span: span, Bearer: 2},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if _, ok := err.(*cal.BookingConflictError); !ok {
		t.Fatalf("expected *cal.BookingConflictError (cross-path: ordinary booking must block a new dxp reservation), got %T: %v", err, err)
	}
}

func TestCalAdapter_Reserve_RefusesAgainstLiveDxpClaim(t *testing.T) {
	_, _, _, a := testCalAdapter(t)
	span := cal.Span{
		Start: mustInstant(t, "2026-08-01T09:00:00Z"),
		End:   mustInstant(t, "2026-08-01T10:00:00Z"),
	}
	if _, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		cal.CalTransitionParams{CalendarID: "room-a", BookingID: "b1", Span: span, Bearer: 1},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("first reserve: %v", err)
	}

	_, err := a.Reserve(context.Background(), tenant.TenantID(0).String(),
		cal.CalTransitionParams{CalendarID: "room-a", BookingID: "b2", Span: span, Bearer: 2},
		"txn-2", "p1", futureDeadline(), dxp.Pessimistic)
	if _, ok := err.(*cal.BookingConflictError); !ok {
		t.Fatalf("expected *cal.BookingConflictError (a live dxp claim must block a second reservation of the same day-bucket), got %T: %v", err, err)
	}
}

func TestCalAdapter_Execute_WritesRealBookingViaSharedTx(t *testing.T) {
	src, db, _, a := testCalAdapter(t)
	span := cal.Span{
		Start: mustInstant(t, "2026-08-01T09:00:00Z"),
		End:   mustInstant(t, "2026-08-01T10:00:00Z"),
	}
	ctx := context.Background()
	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		cal.CalTransitionParams{CalendarID: "room-a", BookingID: "b1", Span: span, Bearer: 1},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	// Execute against a REAL shared *sql.Tx, exactly the shape a
	// coordinator would drive it — the whole point of putBookingInTx.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		_ = tx.Rollback()
		t.Fatalf("Execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	b, ok := src.Booking("room-a", "b1")
	if !ok {
		t.Fatal("booking not found after Execute+commit — the write did not land")
	}
	if b.State != cal.StateBinding {
		t.Fatalf("booking state: got %s, want binding (room-a's DefaultState)", b.State)
	}
}

func TestCalAdapter_Release_ClearsPending(t *testing.T) {
	_, _, _, a := testCalAdapter(t)
	span := cal.Span{
		Start: mustInstant(t, "2026-08-01T09:00:00Z"),
		End:   mustInstant(t, "2026-08-01T10:00:00Z"),
	}
	ctx := context.Background()
	cl, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		cal.CalTransitionParams{CalendarID: "room-a", BookingID: "b1", Span: span, Bearer: 1},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := a.Release(ctx, cl); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// A second Release on the same (now-cleared) txn must be a no-op,
	// not an error — matching bal/fsm's idempotent contract.
	if err := a.Release(ctx, cl); err != nil {
		t.Fatalf("second Release must be idempotent, got %v", err)
	}
}

func mustInstant(t *testing.T, rfc3339 string) ot.Instant {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		t.Fatalf("parse %q: %v", rfc3339, err)
	}
	return ot.FromTime(tm)
}
