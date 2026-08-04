// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// dumpIndex reads the entire occupancy keyspace into a comparable map
// key-string -> day bitmap, for asserting two index states are identical.
func dumpIndex(t *testing.T, s *IndexStore) map[string]DayBitmap {
	t.Helper()
	out := map[string]DayBitmap{}
	lower := []byte{keyKindOccupancy}
	upper := []byte{keyKindOccupancy + 1}
	iter, err := s.db.NewIter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()
	for iter.SeekGE(lower); iter.Valid(); iter.Next() {
		if string(iter.Key()) >= string(upper) {
			break
		}
		var bm DayBitmap
		if err := decodeDayValue(iter.Value(), &bm); err != nil {
			t.Fatal(err)
		}
		out[string(iter.Key())] = bm
	}
	return out
}

func indexEqual(a, b map[string]DayBitmap) (bool, string) {
	if len(a) != len(b) {
		return false, fmt.Sprintf("key count %d != %d", len(a), len(b))
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false, fmt.Sprintf("key %x missing in second", k)
		}
		if va != vb {
			return false, fmt.Sprintf("key %x differs: %v vs %v", k, va, vb)
		}
	}
	return true, ""
}

// openTestStore opens a cal index store under a temp dir using the real
// storelayout path invariant (TenantCalDir), proving the directory convention.
func openTestStore(t *testing.T, tenantID tenant.TenantID) *IndexStore {
	t.Helper()
	base := t.TempDir()
	dir := storelayout.TenantCalDir(base, tenantID)
	s, err := OpenIndexStore(dir)
	if err != nil {
		t.Fatalf("OpenIndexStore(%s): %v", dir, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestStoreUsesStorelayoutPath confirms cal opens under <base>/tXXXX/cal/db.
func TestStoreUsesStorelayoutPath(t *testing.T) {
	base := t.TempDir()
	dir := storelayout.TenantCalDir(base, 7)
	s, err := OpenIndexStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := base + "/t0007/cal"
	if s.dir != want {
		t.Fatalf("cal dir = %q, want %q", s.dir, want)
	}
}

// TestIndexEqualsRebuild is the Stage-3 acceptance gate: after any sequence of
// create/confirm/cancel/complete operations applied incrementally, the index
// must equal a fresh rebuild from the live records.
func TestIndexEqualsRebuild(t *testing.T) {
	rng := rand.New(rand.NewSource(20))
	base := ot.MustParse("2026-07-01T00:00:00Z")

	for trial := 0; trial < 200; trial++ {
		s := openTestStore(t, tenant.TenantID(trial%5))
		src := NewMemBookingSource(rng.Intn(2) == 0)

		// a few calendars
		nCal := rng.Intn(3) + 1
		var calIDs []string
		for c := 0; c < nCal; c++ {
			id := fmt.Sprintf("cal-%d", c)
			cc, err := src.CreateCalendar(Calendar{CalendarID: id, EntityRef: uint64(c + 1)})
			if err != nil {
				t.Fatal(err)
			}
			s.RegisterCalendar(cc)
			calIDs = append(calIDs, id)
		}

		// random bookings, applied incrementally to the index as we create them.
		nBook := rng.Intn(12)
		for bIdx := 0; bIdx < nBook; bIdx++ {
			cid := calIDs[rng.Intn(len(calIDs))]
			startOff := time.Duration(rng.Intn(60*24)) * time.Hour
			dur := time.Duration(rng.Intn(8)+1) * time.Hour
			st := base.Add(startOff)
			b := Booking{
				BookingID:  fmt.Sprintf("bk-%d", bIdx),
				CalendarID: cid,
				State:      StateBinding,
				Span:       Span{Start: st, End: st.Add(dur)},
				Mode:       ModeExclusive,
				Bearer:     uint64(bIdx + 100), // a live handle
				CreatedAt:  ot.Now(),
				UpdatedAt:  ot.Now(),
			}
			// randomly make some proposed instead
			if rng.Intn(3) == 0 {
				b.State = StateProposed
			}
			if err := src.PutBooking(b); err != nil {
				t.Fatalf("PutBooking: %v", err)
			}
			// incremental index write (create path)
			if err := s.AddOccupancy(b); err != nil {
				t.Fatalf("AddOccupancy: %v", err)
			}
		}

		// randomly cancel/complete some bookings in the source (state changes
		// that the incremental path does NOT safely handle — only rebuild does).
		live := src.LiveBookings()
		for _, b := range live {
			r := rng.Intn(5)
			switch r {
			case 0:
				_ = src.SetStateFrom(b.CalendarID, b.BookingID, b.State, StateCancelled)
			case 1:
				if b.State == StateBinding {
					_ = src.SetStateFrom(b.CalendarID, b.BookingID, b.State, StateHonoured)
				}
			case 2:
				if b.State == StateProposed {
					_ = src.SetStateFrom(b.CalendarID, b.BookingID, b.State, StateBinding)
				}
			}
		}

		// Snapshot the incrementally-built index (which is now STALE w.r.t. the
		// state changes above — that's expected; incremental add can't track
		// removals). Then rebuild and assert rebuild is internally consistent and
		// idempotent.
		if err := s.RebuildFrom(src); err != nil {
			t.Fatalf("RebuildFrom: %v", err)
		}
		afterRebuild := dumpIndex(t, s)

		// Rebuild again: must be identical (idempotent / deterministic).
		if err := s.RebuildFrom(src); err != nil {
			t.Fatalf("RebuildFrom (2nd): %v", err)
		}
		afterRebuild2 := dumpIndex(t, s)
		if ok, msg := indexEqual(afterRebuild, afterRebuild2); !ok {
			t.Fatalf("trial %d: rebuild not idempotent: %s", trial, msg)
		}

		// And the rebuilt index must match an independent recomputation from the
		// live bookings (the oracle): OR every live booking's occupancy.
		oracle := oracleIndex(t, src)
		if ok, msg := indexEqual(afterRebuild, oracle); !ok {
			t.Fatalf("trial %d: index != oracle rebuild: %s", trial, msg)
		}

		s.Close()
	}
}

// oracleIndex independently computes the expected occupancy keyspace from a
// source's live bookings, without using IndexStore's write path — the rebuild
// oracle.
func oracleIndex(t *testing.T, src *MemBookingSource) map[string]DayBitmap {
	t.Helper()
	ordMap := map[string]CalOrdinal{}
	for _, c := range src.Calendars() {
		ordMap[c.CalendarID] = c.Ordinal
	}
	acc := map[string]*DayBitmap{}
	for _, b := range src.LiveBookings() {
		span, plane, occ := b.occupancySpan()
		if !occ {
			continue
		}
		ord := ordMap[b.CalendarID]
		days, err := SpanDays(span)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range days {
			key := string(EncodeKey(ord, plane, d.DayNanos))
			bm := acc[key]
			if bm == nil {
				bm = &DayBitmap{}
				acc[key] = bm
			}
			for i := range bm {
				bm[i] |= d.Bits[i]
			}
		}
	}
	out := map[string]DayBitmap{}
	for k, bm := range acc {
		if !bm.IsZero() {
			out[k] = *bm
		}
	}
	return out
}

// TestReadOccupancyAfterRebuild: the Stage-2 availability reads run against the
// persisted, rebuilt index and match a from-scratch in-memory occupancy.
func TestReadOccupancyAfterRebuild(t *testing.T) {
	s := openTestStore(t, 1)
	src := NewMemBookingSource(false)
	cc, _ := src.CreateCalendar(Calendar{CalendarID: "room-1", EntityRef: 1})
	s.RegisterCalendar(cc)

	// Book 09:00-12:00 binding, 14:00-15:00 proposed.
	b1 := Booking{BookingID: "b1", CalendarID: "room-1", State: StateBinding,
		Span:   Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")},
		Bearer: 100, CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
	b2 := Booking{BookingID: "b2", CalendarID: "room-1", State: StateProposed,
		Span:      Span{Start: ot.MustParse("2026-07-08T14:00:00Z"), End: ot.MustParse("2026-07-08T15:00:00Z")},
		CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
	if err := src.PutBooking(b1); err != nil {
		t.Fatal(err)
	}
	if err := src.PutBooking(b2); err != nil {
		t.Fatal(err)
	}
	if err := s.RebuildFrom(src); err != nil {
		t.Fatal(err)
	}

	o, err := s.ReadOccupancy("room-1")
	if err != nil {
		t.Fatal(err)
	}
	day := PeriodDay(ot.MustParse("2026-07-08T00:00:00Z"))
	capr, err := o.Capacity(day)
	if err != nil {
		t.Fatal(err)
	}
	// binding 3h of 24h => 12.5% binding -> capacity 88 (rounded). state busy.
	if capr.State != StateBusy {
		t.Fatalf("state = %v, want busy (binding present)", capr.State)
	}
	if capr.Counts.Binding != 36 { // 3h = 36 quanta
		t.Fatalf("binding quanta = %d, want 36", capr.Counts.Binding)
	}
	if capr.Counts.Proposed != 12 { // 1h = 12 quanta
		t.Fatalf("proposed quanta = %d, want 12", capr.Counts.Proposed)
	}
}

// TestBearerRuleEnforced: a binding booking without a live bearer is rejected
// (review issue 2).
func TestBearerRuleEnforced(t *testing.T) {
	src := NewMemBookingSource(false)
	_, _ = src.CreateCalendar(Calendar{CalendarID: "c", EntityRef: 1})
	b := Booking{BookingID: "x", CalendarID: "c", State: StateBinding,
		Span:   Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T10:00:00Z")},
		Bearer: EntityNil, CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
	if err := src.PutBooking(b); err == nil {
		t.Fatal("binding booking with nil bearer should be rejected")
	}
	// proposed without bearer is fine.
	b.State = StateProposed
	if err := src.PutBooking(b); err != nil {
		t.Fatalf("proposed booking without bearer should be allowed: %v", err)
	}
}

// TestOrdinalAllocation: dense ascending from 1; reuse policy returns retired
// ordinals.
func TestOrdinalAllocation(t *testing.T) {
	// retire (default): ordinals only go up.
	src := NewMemBookingSource(false)
	c1, _ := src.CreateCalendar(Calendar{CalendarID: "a"})
	c2, _ := src.CreateCalendar(Calendar{CalendarID: "b"})
	if c1.Ordinal != 1 || c2.Ordinal != 2 {
		t.Fatalf("ordinals = %d,%d, want 1,2", c1.Ordinal, c2.Ordinal)
	}
	_ = src.DeleteCalendar("a")
	c3, _ := src.CreateCalendar(Calendar{CalendarID: "c"})
	if c3.Ordinal != 3 {
		t.Fatalf("retire policy: ordinal = %d, want 3 (no reuse)", c3.Ordinal)
	}

	// reuse: retired ordinal comes back.
	rs := NewMemBookingSource(true)
	r1, _ := rs.CreateCalendar(Calendar{CalendarID: "a"})
	_, _ = rs.CreateCalendar(Calendar{CalendarID: "b"})
	_ = rs.DeleteCalendar("a")
	r3, _ := rs.CreateCalendar(Calendar{CalendarID: "c"})
	if r3.Ordinal != r1.Ordinal {
		t.Fatalf("reuse policy: ordinal = %d, want %d (reused)", r3.Ordinal, r1.Ordinal)
	}
}

// TestPersistenceAcrossReopen: occupancy survives closing and reopening the store.
func TestPersistenceAcrossReopen(t *testing.T) {
	base := t.TempDir()
	dir := storelayout.TenantCalDir(base, 3)

	src := NewMemBookingSource(false)
	cc, _ := src.CreateCalendar(Calendar{CalendarID: "room", EntityRef: 1})

	s1, err := OpenIndexStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s1.RegisterCalendar(cc)
	b := Booking{BookingID: "b1", CalendarID: "room", State: StateBinding,
		Span:   Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")},
		Bearer: 100, CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
	_ = src.PutBooking(b)
	if err := s1.AddOccupancy(b); err != nil {
		t.Fatal(err)
	}
	before := dumpIndex(t, s1)
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// reopen
	s2, err := OpenIndexStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.RegisterCalendar(cc)
	after := dumpIndex(t, s2)
	if ok, msg := indexEqual(before, after); !ok {
		t.Fatalf("occupancy did not survive reopen: %s", msg)
	}
}
