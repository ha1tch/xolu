// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal_test

// TDD: this test specifies the Manager contract before the implementation exists.
// The Manager is the per-tenant assembly of (SQLite source + Pebble index +
// lifecycle) that the REST handlers sit on. It opens the index at the
// storelayout path, binds the source to the tenant DB, and hands back a ready
// lifecycle; CalFor is idempotent per tenant (one assembly, reused).

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ha1tch/xolu/pkg/cal"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
	"github.com/ha1tch/xolu/pkg/storage"
)

func newManagerForTest(t *testing.T) (*cal.Manager, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cal-mgr")
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(dir+"/store.db", storage.SQLiteConfig{DBPath: dir + "/store.db"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitV2Schema(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The manager needs: a base dir for index stores, and a way to get the
	// tenant's *sql.DB. We pass the DB handle and base dir directly.
	mgr := cal.NewManager(dir, store.DB())
	cleanup := func() {
		mgr.Close()
		store.Close()
		os.RemoveAll(dir)
	}
	return mgr, cleanup
}

// TestManagerCalForAssembles: CalFor returns a working lifecycle for a tenant.
func TestManagerCalForAssembles(t *testing.T) {
	mgr, cleanup := newManagerForTest(t)
	defer cleanup()

	lc, err := mgr.CalFor(1)
	if err != nil {
		t.Fatalf("CalFor: %v", err)
	}
	if lc == nil {
		t.Fatal("CalFor returned nil lifecycle")
	}

	// It should be usable end-to-end: create a calendar, then a booking, then
	// read occupancy.
	src := mgr.SourceFor(1)
	c, err := src.CreateCalendar(cal.Calendar{CalendarID: "room", EntityRef: 1, DefaultState: cal.StateBinding})
	if err != nil {
		t.Fatalf("CreateCalendar: %v", err)
	}
	idx := mgr.IndexFor(1)
	idx.RegisterCalendar(c)

	b := cal.Booking{
		BookingID: "b1", CalendarID: "room", State: cal.StateBinding,
		Span: cal.Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")},
		Mode: cal.ModeExclusive, Bearer: 100,
		CreatedAt: ot.Now(), UpdatedAt: ot.Now(),
	}
	if _, err := lc.Create(b); err != nil {
		t.Fatalf("Create booking: %v", err)
	}
	o, err := idx.ReadOccupancy("room")
	if err != nil {
		t.Fatal(err)
	}
	day := cal.PeriodDay(ot.MustParse("2026-07-08T00:00:00Z"))
	capr, _ := o.Capacity(day)
	if capr.State != cal.StateBusy {
		t.Fatalf("state=%v, want busy", capr.State)
	}
}

// TestManagerCalForIdempotent: two CalFor calls for the same tenant return the
// same assembly (not a fresh index each time).
func TestManagerCalForIdempotent(t *testing.T) {
	mgr, cleanup := newManagerForTest(t)
	defer cleanup()

	lc1, err := mgr.CalFor(1)
	if err != nil {
		t.Fatal(err)
	}
	lc2, err := mgr.CalFor(1)
	if err != nil {
		t.Fatal(err)
	}
	if lc1 != lc2 {
		t.Fatal("CalFor should return the same lifecycle for the same tenant")
	}
}

// TestManagerPerTenantIsolation: different tenants get different assemblies, and
// a calendar in tenant 1 is not visible in tenant 2.
func TestManagerPerTenantIsolation(t *testing.T) {
	mgr, cleanup := newManagerForTest(t)
	defer cleanup()

	if _, err := mgr.CalFor(1); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CalFor(2); err != nil {
		t.Fatal(err)
	}
	src1 := mgr.SourceFor(1)
	src2 := mgr.SourceFor(2)

	if _, err := src1.CreateCalendar(cal.Calendar{CalendarID: "shared-name", EntityRef: 1}); err != nil {
		t.Fatal(err)
	}
	// tenant 2 must not see tenant 1's calendar.
	if _, ok := src2.Calendar("shared-name"); ok {
		t.Fatal("tenant 2 must not see tenant 1's calendar (isolation breach)")
	}
	// tenant 2 can create its own calendar with the same id.
	if _, err := src2.CreateCalendar(cal.Calendar{CalendarID: "shared-name", EntityRef: 9}); err != nil {
		t.Fatalf("tenant 2 should create its own same-named calendar: %v", err)
	}
}

// TestManagerCreateCalendarFacade: the CreateCalendar facade both persists
// the calendar and registers it with the in-memory index, eliminating the
// class of bug where Lifecycle.Create later fails with ErrUnknownCalendar
// because the ordinal map was never updated. Introduced with v0.14.10.
func TestManagerCreateCalendarFacade(t *testing.T) {
	mgr, cleanup := newManagerForTest(t)
	defer cleanup()

	c, err := mgr.CreateCalendar(1, cal.Calendar{
		CalendarID:   "room-1",
		EntityRef:    1,
		DefaultState: cal.StateBinding,
	})
	if err != nil {
		t.Fatalf("CreateCalendar: %v", err)
	}
	if c.CalendarID != "room-1" {
		t.Errorf("CalendarID = %q, want %q", c.CalendarID, "room-1")
	}
	if c.Ordinal == 0 {
		t.Error("expected non-zero Ordinal after CreateCalendar")
	}

	// The critical assertion: Lifecycle.Create against this calendar
	// succeeds immediately WITHOUT any explicit RegisterCalendar call.
	// Before the facade existed, this exact sequence failed with
	// ErrUnknownCalendar because the index ordinal map wasn't updated.
	lc, err := mgr.CalFor(1)
	if err != nil {
		t.Fatalf("CalFor: %v", err)
	}
	start := ot.FromUnixNano(1_700_000_000_000_000_000)
	end := ot.FromUnixNano(1_700_000_003_600_000_000)
	_, err = lc.Create(cal.Booking{
		BookingID:  "b1",
		CalendarID: "room-1",
		State:      cal.StateProposed,
		Span:       cal.Span{Start: start, End: end},
		Bearer:     1,
	})
	if err != nil {
		t.Errorf("Lifecycle.Create against facade-created calendar failed: %v", err)
	}
}

// TestManagerCreateCalendarRejectsDuplicate: the facade surfaces the source
// layer's collision error, wrapped with the sentinel so callers can
// distinguish via errors.Is.
func TestManagerCreateCalendarRejectsDuplicate(t *testing.T) {
	mgr, cleanup := newManagerForTest(t)
	defer cleanup()

	if _, err := mgr.CreateCalendar(1, cal.Calendar{CalendarID: "same", EntityRef: 1}); err != nil {
		t.Fatalf("first CreateCalendar: %v", err)
	}
	_, err := mgr.CreateCalendar(1, cal.Calendar{CalendarID: "same", EntityRef: 2})
	if err == nil {
		t.Fatal("expected error for duplicate calendar id")
	}
	if !errors.Is(err, cal.ErrCalendarExists) {
		t.Errorf("expected error to wrap ErrCalendarExists, got %v", err)
	}
}

// TestManagerCreateCalendarPerTenantIsolation: creating "room-1" in tenant
// 1 must not prevent creating "room-1" in tenant 2. Calendar IDs are
// tenant-scoped in the schema; the facade must honour that.
func TestManagerCreateCalendarPerTenantIsolation(t *testing.T) {
	mgr, cleanup := newManagerForTest(t)
	defer cleanup()

	if _, err := mgr.CreateCalendar(1, cal.Calendar{CalendarID: "room", EntityRef: 1}); err != nil {
		t.Fatalf("tenant 1 CreateCalendar: %v", err)
	}
	if _, err := mgr.CreateCalendar(2, cal.Calendar{CalendarID: "room", EntityRef: 1}); err != nil {
		t.Fatalf("tenant 2 CreateCalendar (should be allowed): %v", err)
	}
	// Both tenants' lifecycles can now create bookings against their own
	// same-named calendar.
	lc1, _ := mgr.CalFor(1)
	lc2, _ := mgr.CalFor(2)
	start := ot.FromUnixNano(1_700_000_000_000_000_000)
	end := ot.FromUnixNano(1_700_000_003_600_000_000)
	if _, err := lc1.Create(cal.Booking{BookingID: "b1", CalendarID: "room", State: cal.StateProposed, Span: cal.Span{Start: start, End: end}, Bearer: 1}); err != nil {
		t.Errorf("tenant 1 booking: %v", err)
	}
	if _, err := lc2.Create(cal.Booking{BookingID: "b1", CalendarID: "room", State: cal.StateProposed, Span: cal.Span{Start: start, End: end}, Bearer: 1}); err != nil {
		t.Errorf("tenant 2 booking: %v", err)
	}
}
