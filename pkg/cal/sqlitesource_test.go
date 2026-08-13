// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal_test

// This test lives in cal_test (external) and imports both cal and storage to
// drive the SQLite-backed source through the real lifecycle/match/commit logic
// against a properly-migrated database. It proves the SQLiteBookingSource is a
// drop-in for MemBookingSource: the same Store interface, the same index ==
// rebuild invariant.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// openSQLiteSource creates a migrated SQLite store and a cal source over it,
// plus a cal index store, returning all three with cleanup registered.
func openSQLiteSource(t *testing.T, tenantID tenant.TenantID, reuse bool) (*cal.SQLiteBookingSource, *cal.IndexStore) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cal-sqlite-src")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	store, err := storage.NewSQLiteStore(dir+"/store.db", storage.SQLiteConfig{DBPath: dir + "/store.db"})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema: %v", err)
	}

	src := cal.NewSQLiteBookingSource(store.DB(), tenantID, reuse)

	idxDir := storelayout.TenantCalDir(dir, tenantID)
	idx, err := cal.OpenIndexStore(idxDir)
	if err != nil {
		t.Fatalf("OpenIndexStore: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return src, idx
}

// dumpIdx reads the whole occupancy keyspace via the exported rebuild path is not
// available externally; instead we compare two stores by rebuilding a scratch one
// and reading occupancy per calendar through ReadOccupancy. To keep the external
// test simple we assert via ReadOccupancy capacities at known windows.

func TestSQLiteSourceLifecycleIntegration(t *testing.T) {
	src, idx := openSQLiteSource(t, 1, false)

	// Register calendars via the source (allocates ordinals through cal_ord_seq).
	roomCal, err := src.CreateCalendar(cal.Calendar{CalendarID: "room", EntityRef: 1, DefaultState: cal.StateBinding})
	if err != nil {
		t.Fatal(err)
	}
	idx.RegisterCalendar(roomCal)

	lc := cal.NewLifecycle(src, idx)

	// Create a binding booking 09:00-12:00.
	b := cal.Booking{
		BookingID: "b1", CalendarID: "room", State: cal.StateBinding,
		Span: cal.Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")},
		Mode: cal.ModeExclusive, Bearer: 100,
		CreatedAt: ot.Now(), UpdatedAt: ot.Now(),
	}
	if _, err := lc.Create(b); err != nil {
		t.Fatal(err)
	}

	// Read occupancy back from the index and check the window is busy.
	o, err := idx.ReadOccupancy("room")
	if err != nil {
		t.Fatal(err)
	}
	day := cal.PeriodDay(ot.MustParse("2026-07-08T00:00:00Z"))
	capr, err := o.Capacity(day)
	if err != nil {
		t.Fatal(err)
	}
	if capr.State != cal.StateBusy {
		t.Fatalf("state=%v, want busy", capr.State)
	}
	if capr.Counts.Binding != 36 {
		t.Fatalf("binding quanta=%d, want 36", capr.Counts.Binding)
	}

	// Persistence proof: the booking is in SQLite — re-read via the source.
	if _, ok := src.Booking("room", "b1"); !ok {
		t.Fatal("booking did not persist to SQLite")
	}

	// Move it, then confirm the index follows.
	to := cal.Span{Start: ot.MustParse("2026-07-08T14:00:00Z"), End: ot.MustParse("2026-07-08T17:00:00Z")}
	res, err := lc.Move("room", "b1", to)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved {
		t.Fatalf("move should succeed: %+v", res)
	}
	o2, _ := idx.ReadOccupancy("room")
	oldWin := cal.Period{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")}
	if free, _ := o2.IsFree(oldWin); !free {
		t.Fatal("old span should be free after move")
	}
	newWin := cal.Period{Start: ot.MustParse("2026-07-08T14:00:00Z"), End: ot.MustParse("2026-07-08T17:00:00Z")}
	if busy, _ := o2.IsBusy(newWin); !busy {
		t.Fatal("new span should be busy after move")
	}
}

// TestSQLiteSourceRebuildEqualsIndex: build occupancy incrementally through the
// SQLite source, then rebuild the index from the SQLite records and confirm the
// occupancy reads identically — the index == rebuild invariant against the real
// persistent source.
func TestSQLiteSourceRebuildEqualsIndex(t *testing.T) {
	src, idx := openSQLiteSource(t, 2, false)
	c, _ := src.CreateCalendar(cal.Calendar{CalendarID: "c", EntityRef: 1, DefaultState: cal.StateBinding})
	idx.RegisterCalendar(c)
	lc := cal.NewLifecycle(src, idx)

	base := ot.MustParse("2026-07-08T00:00:00Z")
	for i := 0; i < 8; i++ {
		st := base.Add(time.Duration(i*3) * time.Hour)
		en := st.Add(90 * time.Minute)
		b := cal.Booking{BookingID: fmt.Sprintf("b%d", i), CalendarID: "c", State: cal.StateBinding,
			Span: cal.Span{Start: st, End: en}, Mode: cal.ModeExclusive, Bearer: uint64(i + 100),
			CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
		if _, err := lc.Create(b); err != nil {
			t.Fatal(err)
		}
	}

	// occupancy before rebuild
	before, _ := idx.ReadOccupancy("c")
	day := cal.PeriodDay(base)
	capBefore, _ := before.Capacity(day)

	// rebuild from the SQLite source
	if err := idx.RebuildFrom(src); err != nil {
		t.Fatal(err)
	}
	after, _ := idx.ReadOccupancy("c")
	capAfter, _ := after.Capacity(day)

	if capBefore.Counts != capAfter.Counts {
		t.Fatalf("rebuild changed occupancy: before=%+v after=%+v", capBefore.Counts, capAfter.Counts)
	}
	if capBefore.Counts.Binding == 0 {
		t.Fatal("expected some binding occupancy")
	}
}

// TestSQLiteSourceBookingsInRange proves the overlap predicate directly
// (start < to AND end > from, not exact containment) plus calendar
// scoping, using five bookings chosen to cover every boundary case: one
// fully inside the query window, one fully outside (before), one fully
// outside (after), one straddling the query window's own start, one
// straddling its own end, and one on a DIFFERENT calendar with a span
// that would otherwise match -- to prove calendar_id is genuinely part
// of the WHERE clause, not just the overlap predicate alone.
func TestSQLiteSourceBookingsInRange(t *testing.T) {
	src, _ := openSQLiteSource(t, 1, false)
	if _, err := src.CreateCalendar(cal.Calendar{
		CalendarID: "room-a", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("CreateCalendar room-a: %v", err)
	}
	if _, err := src.CreateCalendar(cal.Calendar{
		CalendarID: "room-b", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("CreateCalendar room-b: %v", err)
	}

	base := ot.FromUnixNano(1750000000 * int64(time.Second))
	hour := time.Hour
	put := func(id, calendarID string, startOffset, endOffset time.Duration) {
		t.Helper()
		b := cal.Booking{
			BookingID: id, CalendarID: calendarID, State: cal.StateBinding,
			Span: cal.Span{Start: base.Add(startOffset), End: base.Add(endOffset)},
			Mode: cal.ModeExclusive, Bearer: 1,
			CreatedAt: base, UpdatedAt: base,
		}
		if err := src.PutBooking(b); err != nil {
			t.Fatalf("PutBooking %s: %v", id, err)
		}
	}

	// Query window: [base+2h, base+6h).
	put("fully-inside", "room-a", 3*hour, 4*hour)
	put("fully-before", "room-a", -2*hour, -1*hour)
	put("fully-after", "room-a", 8*hour, 9*hour)
	put("straddles-start", "room-a", 1*hour, 3*hour)
	put("straddles-end", "room-a", 5*hour, 7*hour)
	put("other-calendar", "room-b", 3*hour, 4*hour) // would match if calendar_id weren't scoped

	got := src.BookingsInRange("room-a", base.Add(2*hour), base.Add(6*hour))
	gotIDs := make(map[string]bool, len(got))
	for _, b := range got {
		gotIDs[b.BookingID] = true
	}

	want := []string{"fully-inside", "straddles-start", "straddles-end"}
	for _, id := range want {
		if !gotIDs[id] {
			t.Errorf("expected %q in range result, got %v", id, gotIDs)
		}
	}
	notWant := []string{"fully-before", "fully-after", "other-calendar"}
	for _, id := range notWant {
		if gotIDs[id] {
			t.Errorf("did not expect %q in range result, got %v", id, gotIDs)
		}
	}
	if len(got) != len(want) {
		t.Errorf("want exactly %d bookings, got %d: %v", len(want), len(got), gotIDs)
	}
}

// TestSQLiteSourceOrdinalAllocation: ordinals come from cal_ord_seq, dense
// ascending.
func TestSQLiteSourceOrdinalAllocation(t *testing.T) {
	src, _ := openSQLiteSource(t, 3, false)
	c1, _ := src.CreateCalendar(cal.Calendar{CalendarID: "a"})
	c2, _ := src.CreateCalendar(cal.Calendar{CalendarID: "b"})
	c3, _ := src.CreateCalendar(cal.Calendar{CalendarID: "c"})
	if c1.Ordinal != 1 || c2.Ordinal != 2 || c3.Ordinal != 3 {
		t.Fatalf("ordinals = %d,%d,%d, want 1,2,3", c1.Ordinal, c2.Ordinal, c3.Ordinal)
	}
	// duplicate rejected
	if _, err := src.CreateCalendar(cal.Calendar{CalendarID: "a"}); err == nil {
		t.Fatal("duplicate calendar should be rejected")
	}
}

// TestSQLiteSourceTimePersistsAsUTC: a booking created with an offset-bearing
// instant round-trips as the same absolute instant (the xolutime invariant).
func TestSQLiteSourceTimePersistsAsUTC(t *testing.T) {
	src, idx := openSQLiteSource(t, 4, false)
	c, _ := src.CreateCalendar(cal.Calendar{CalendarID: "c", EntityRef: 1})
	idx.RegisterCalendar(c)

	// An instant expressed with a +05:30 offset is the same absolute instant as
	// its UTC equivalent; storage must preserve the instant, not the offset.
	withOffset := ot.MustParse("2026-07-08T14:30:00+05:30") // == 09:00:00Z
	b := cal.Booking{BookingID: "tz", CalendarID: "c", State: cal.StateBinding,
		Span: cal.Span{Start: withOffset, End: withOffset.Add(time.Hour)},
		Mode: cal.ModeExclusive, Bearer: 100, CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
	if err := src.PutBooking(b); err != nil {
		t.Fatal(err)
	}
	got, ok := src.Booking("c", "tz")
	if !ok {
		t.Fatal("booking not found")
	}
	// the stored start must equal the UTC instant 09:00:00Z, byte-for-byte by nanos.
	wantUTC := ot.MustParse("2026-07-08T09:00:00Z")
	if got.Span.Start.UnixNano() != wantUTC.UnixNano() {
		t.Fatalf("stored start %d != UTC equivalent %d (offset not normalised to instant)",
			got.Span.Start.UnixNano(), wantUTC.UnixNano())
	}
}

// TestSQLiteSourceBookingsForBearer proves the cross-calendar bearer
// query (XOT180, 2026-08-11): bearer 100 holds bookings on two
// different calendars plus one non-live (declined) booking that must
// be excluded; bearer 200 holds one booking that must not appear in
// bearer 100's own result.
func TestSQLiteSourceBookingsForBearer(t *testing.T) {
	src, _ := openSQLiteSource(t, 1, false)
	for _, id := range []string{"room-a", "room-b"} {
		if _, err := src.CreateCalendar(cal.Calendar{
			CalendarID: id, DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
		}); err != nil {
			t.Fatalf("CreateCalendar %s: %v", id, err)
		}
	}

	base := ot.FromUnixNano(1750000000 * int64(time.Second))
	hour := time.Hour
	put := func(id, calendarID string, bearer uint64, state cal.State, startOffset, endOffset time.Duration) {
		t.Helper()
		b := cal.Booking{
			BookingID: id, CalendarID: calendarID, State: state,
			Span: cal.Span{Start: base.Add(startOffset), End: base.Add(endOffset)},
			Mode: cal.ModeExclusive, Bearer: bearer,
			CreatedAt: base, UpdatedAt: base,
		}
		if err := src.PutBooking(b); err != nil {
			t.Fatalf("PutBooking %s: %v", id, err)
		}
	}

	put("b100-room-a", "room-a", 100, cal.StateBinding, hour, 2*hour)
	put("b100-room-b", "room-b", 100, cal.StateBinding, 3*hour, 4*hour)
	put("b100-declined", "room-a", 100, cal.StateCancelled, 5*hour, 6*hour)
	put("b200-room-a", "room-a", 200, cal.StateBinding, hour, 2*hour)

	got := src.BookingsForBearer(100)
	gotIDs := make(map[string]bool, len(got))
	for _, b := range got {
		gotIDs[b.BookingID] = true
	}

	for _, id := range []string{"b100-room-a", "b100-room-b"} {
		if !gotIDs[id] {
			t.Errorf("expected %q in bearer 100's own result, got %v", id, gotIDs)
		}
	}
	for _, id := range []string{"b100-declined", "b200-room-a"} {
		if gotIDs[id] {
			t.Errorf("did not expect %q in bearer 100's own result, got %v", id, gotIDs)
		}
	}
	if len(got) != 2 {
		t.Errorf("want exactly 2 bookings for bearer 100, got %d: %v", len(got), gotIDs)
	}
}

// TestSQLiteSourceBookingsForBearer_TenantIsolation proves isolation
// directly for this new query, matching XOT180's own general
// discipline of not assuming a new query is safe by construction.
func TestSQLiteSourceBookingsForBearer_TenantIsolation(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(dir+"/store.db", storage.SQLiteConfig{DBPath: dir + "/store.db"})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema: %v", err)
	}

	srcA := cal.NewSQLiteBookingSource(store.DB(), 1, false)
	srcB := cal.NewSQLiteBookingSource(store.DB(), 2, false)
	if _, err := srcA.CreateCalendar(cal.Calendar{CalendarID: "room", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding}); err != nil {
		t.Fatalf("tenant 1 CreateCalendar: %v", err)
	}
	if _, err := srcB.CreateCalendar(cal.Calendar{CalendarID: "room", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding}); err != nil {
		t.Fatalf("tenant 2 CreateCalendar: %v", err)
	}

	base := ot.FromUnixNano(1750000000 * int64(time.Second))
	// Same bearer ID (100) on both tenants -- the case most likely to
	// leak if tenant scoping were ever forgotten on this new query.
	if err := srcA.PutBooking(cal.Booking{
		BookingID: "a-booking", CalendarID: "room", State: cal.StateBinding,
		Span: cal.Span{Start: base, End: base.Add(time.Hour)}, Mode: cal.ModeExclusive, Bearer: 100,
		CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatalf("tenant 1 PutBooking: %v", err)
	}
	if err := srcB.PutBooking(cal.Booking{
		BookingID: "b-booking", CalendarID: "room", State: cal.StateBinding,
		Span: cal.Span{Start: base, End: base.Add(time.Hour)}, Mode: cal.ModeExclusive, Bearer: 100,
		CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatalf("tenant 2 PutBooking: %v", err)
	}

	gotA := srcA.BookingsForBearer(100)
	if len(gotA) != 1 || gotA[0].BookingID != "a-booking" {
		t.Fatalf("tenant isolation violated: tenant 1 wants exactly its own booking, got %+v", gotA)
	}
	gotB := srcB.BookingsForBearer(100)
	if len(gotB) != 1 || gotB[0].BookingID != "b-booking" {
		t.Fatalf("tenant isolation violated: tenant 2 wants exactly its own booking, got %+v", gotB)
	}
}
