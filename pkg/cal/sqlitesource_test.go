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
	ot "github.com/ha1tch/xolu/pkg/xolutime"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/storelayout"
)

// openSQLiteSource creates a migrated SQLite store and a cal source over it,
// plus a cal index store, returning all three with cleanup registered.
func openSQLiteSource(t *testing.T, tenantID uint16, reuse bool) (*cal.SQLiteBookingSource, *cal.IndexStore) {
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
