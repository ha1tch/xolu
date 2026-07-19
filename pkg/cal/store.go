// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cockroachdb/pebble"
)

// IndexStore is the Pebble-backed occupancy index (the derived bitmap, H3). It
// is NOT authoritative: it is a pure function of the live booking records (H1),
// and can be discarded and rebuilt at any time (Rebuild). The booking records
// themselves live in the tenant's primary store (SQLite); this package's Stage 3
// works against an in-memory BookingSource for them, so the index logic and the
// `index == rebuild` invariant can be exercised before the SQLite wiring lands.
//
// Storage layout mirrors ts: a Pebble db under <calDir>/db. Keys are the
// 14-byte occupancy keys (codec §3.2); values are 40-byte day bitmaps. Only days
// with occupancy have a value (sparse). The store opens at the directory
// returned by storelayout.TenantCalDir(base, tenantID).
type IndexStore struct {
	mu       sync.Mutex
	db       *pebble.DB
	dir      string
	ordinals map[string]CalOrdinal // calendar_id -> ordinal (codec 0x03), rebuildable

	// Fault-injection hooks (T-31, v0.14.14). Non-nil hooks are called
	// at the top of the corresponding mutation and, if they return a
	// non-nil error, the mutation aborts with that error before any
	// bitmap change is persisted. Nil hooks are the normal path.
	//
	// Intended for tests exercising the SQL/index disagreement recovery
	// path — see cal_fault_injection_test.go. Not part of the public
	// API contract; the fields are exported only so tests in the same
	// package can set them without a reflection dance.
	AddToPlaneFaultHook      func(b Booking) error
	RemoveFromPlaneFaultHook func(b Booking, plane Plane) error
}

// OpenIndexStore opens (creating if needed) the cal occupancy index at dir.
// dir is typically storelayout.TenantCalDir(base, tenantID); the Pebble database
// lives in dir/db, matching the ts convention.
func OpenIndexStore(dir string) (*IndexStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cal: mkdir %s: %w", dir, err)
	}
	dbDir := filepath.Join(dir, "db")
	db, err := pebble.Open(dbDir, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("cal: open %s: %w", dbDir, err)
	}
	return &IndexStore{db: db, dir: dir}, nil
}

// Close closes the underlying Pebble database.
func (s *IndexStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// dayValue reads a day bitmap; a missing key is the zero (all-free) day.
func (s *IndexStore) dayValue(ord CalOrdinal, plane Plane, dayNanos int64) (DayBitmap, error) {
	var bm DayBitmap
	key := EncodeKey(ord, plane, dayNanos)
	val, closer, err := s.db.Get(key)
	if err == pebble.ErrNotFound {
		return bm, nil
	}
	if err != nil {
		return bm, fmt.Errorf("cal: get %x: %w", key, err)
	}
	defer func() { _ = closer.Close() }()
	if err := decodeDayValue(val, &bm); err != nil {
		return bm, err
	}
	return bm, nil
}

// encodeDayValue serialises a DayBitmap to 40 bytes (5 × big-endian uint64).
func encodeDayValue(bm DayBitmap) []byte {
	out := make([]byte, WordsPerDay*8)
	for i, w := range bm {
		binary.BigEndian.PutUint64(out[i*8:i*8+8], w)
	}
	return out
}

// decodeDayValue reverses encodeDayValue.
func decodeDayValue(val []byte, bm *DayBitmap) error {
	if len(val) != WordsPerDay*8 {
		return fmt.Errorf("cal: day value len %d, want %d", len(val), WordsPerDay*8)
	}
	for i := range bm {
		bm[i] = binary.BigEndian.Uint64(val[i*8 : i*8+8])
	}
	return nil
}

// applyBooking ORs (add=true) or recomputes-without (add=false) a booking's
// occupancy into the index. Because clearing one booking's bits could wrongly
// clear bits another booking shares, removal is not a simple AND-NOT here; the
// authoritative way to remove is Rebuild, or the caller re-derives the affected
// days. applyBooking with add=true is the incremental write path; add=false is
// provided only for the single-booking-on-an-empty-day fast path and is not used
// where overlaps are possible. The safe, always-correct mutation is Rebuild.
func (s *IndexStore) applyBooking(b Booking, add bool) error {
	span, plane, occ := b.occupancySpan()
	if !occ {
		return nil // terminal state: contributes nothing
	}
	ord, err := s.ordinalFor(b.CalendarID)
	if err != nil {
		return err
	}
	days, err := SpanDays(span)
	if err != nil {
		return err
	}
	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	for _, d := range days {
		cur, err := s.dayValue(ord, plane, d.DayNanos)
		if err != nil {
			return err
		}
		var next DayBitmap
		if add {
			for i := range next {
				next[i] = cur[i] | d.Bits[i]
			}
		} else {
			for i := range next {
				next[i] = cur[i] &^ d.Bits[i]
			}
		}
		key := EncodeKey(ord, plane, d.DayNanos)
		if next.IsZero() {
			if err := batch.Delete(key, nil); err != nil {
				return err
			}
		} else {
			if err := batch.Set(key, encodeDayValue(next), nil); err != nil {
				return err
			}
		}
	}
	return batch.Commit(pebble.Sync)
}

// --- Ordinal map (codec §3.3, 0x03 metadata) ---
//
// Stage 3 keeps the calendar_id -> ordinal map in memory, populated from the
// BookingSource's calendars on Rebuild and on demand. The persistent 0x03
// metadata encoding is a later refinement; the map is always reconstructable
// from the calendar records (the same rebuild guarantee as the occupancy index).

func (s *IndexStore) ordinalFor(calendarID string) (CalOrdinal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ordinals == nil {
		return 0, fmt.Errorf("cal: ordinal map not initialised (call Rebuild or RegisterCalendar)")
	}
	ord, ok := s.ordinals[calendarID]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownCalendar, calendarID)
	}
	return ord, nil
}

// RegisterCalendar records a calendar's ordinal in the in-memory map.
func (s *IndexStore) RegisterCalendar(c Calendar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ordinals == nil {
		s.ordinals = map[string]CalOrdinal{}
	}
	s.ordinals[c.CalendarID] = c.Ordinal
}

// --- BookingSource: the authoritative records (H1), abstracted ---

// BookingSource provides the live bookings and calendars the index derives from.
// Stage 3 implements it in memory (MemBookingSource); Stage 3's SQLite wiring
// will provide a Pebble/SQLite-backed implementation behind the same interface,
// so Rebuild and the invariant test are storage-agnostic.
type BookingSource interface {
	// Calendars returns every calendar definition.
	Calendars() []Calendar
	// LiveBookings returns every booking in a state that occupies a plane
	// (proposed/binding/honoured); terminal-state bookings are excluded.
	LiveBookings() []Booking
}

// AddOccupancy is the incremental write path: register a newly-created or
// state-changed booking's occupancy into the index. For a create or a confirm
// (which adds to a plane) this is an OR. For a state change that MOVES a booking
// between planes (proposed->binding on confirm) or REMOVES it (cancel), the
// caller must remove the old contribution too; the always-correct path for any
// removal is RebuildFrom, since overlapping bookings make incremental clear
// unsafe. Stage 5 (lifecycle) refines this; Stage 3 uses AddOccupancy for create
// and RebuildFrom as the removal/correctness path.
func (s *IndexStore) AddOccupancy(b Booking) error {
	return s.applyBooking(b, true)
}

// RebuildFrom discards the entire occupancy keyspace and reconstructs it from the
// source's live bookings. This is both the recovery path and the test oracle for
// the index == rebuild invariant.
func (s *IndexStore) RebuildFrom(src BookingSource) error {
	s.mu.Lock()
	// reset the ordinal map from the calendar records
	s.ordinals = map[string]CalOrdinal{}
	for _, c := range src.Calendars() {
		s.ordinals[c.CalendarID] = c.Ordinal
	}
	s.mu.Unlock()

	// Drop all occupancy keys (kind 0x01). Lower/upper bounds over the whole
	// occupancy keyspace: kind byte 0x01, everything after.
	lower := []byte{keyKindOccupancy}
	upper := []byte{keyKindOccupancy + 1}
	if err := s.db.DeleteRange(lower, upper, pebble.Sync); err != nil {
		return fmt.Errorf("cal: rebuild: clear: %w", err)
	}

	// Re-apply every live booking.
	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	// accumulate per-key bitmaps in memory first so overlaps OR correctly within
	// the rebuild without repeated read-modify-write.
	acc := map[string]*DayBitmap{}
	for _, b := range src.LiveBookings() {
		span, plane, occ := b.occupancySpan()
		if !occ {
			continue
		}
		ord, ok := s.ordinals[b.CalendarID]
		if !ok {
			return fmt.Errorf("cal: rebuild: booking %q references unknown calendar %q", b.BookingID, b.CalendarID)
		}
		days, err := SpanDays(span)
		if err != nil {
			return err
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
	for key, bm := range acc {
		if bm.IsZero() {
			continue
		}
		if err := batch.Set([]byte(key), encodeDayValue(*bm), nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

// ReadOccupancy loads the index into an in-memory Occupancy window for a single
// calendar, so the Stage-2 availability reads run against persisted data. It
// scans the calendar's occupancy keys on both planes.
func (s *IndexStore) ReadOccupancy(calendarID string) (*Occupancy, error) {
	ord, err := s.ordinalFor(calendarID)
	if err != nil {
		return nil, err
	}
	o := NewOccupancy()
	for _, plane := range []Plane{PlaneBinding, PlaneProposed} {
		lower := EncodeKey(ord, plane, 0)
		iter, err := s.db.NewIter(&pebble.IterOptions{
			LowerBound: lower,
			UpperBound: incrementPlanePrefix(ord, plane),
		})
		if err != nil {
			return nil, err
		}
		for iter.First(); iter.Valid(); iter.Next() {
			_, _, dayNanos, derr := DecodeKey(iter.Key())
			if derr != nil {
				_ = iter.Close()
				return nil, derr
			}
			var bm DayBitmap
			if derr := decodeDayValue(iter.Value(), &bm); derr != nil {
				_ = iter.Close()
				return nil, derr
			}
			o.planeMap(plane)[dayNanos] = &bm
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// incrementPlanePrefix returns the exclusive upper bound for scanning all days
// of one (ordinal, plane): the key prefix [kind][ord][plane] with the plane byte
// incremented (days are the low 8 bytes).
func incrementPlanePrefix(ord CalOrdinal, plane Plane) []byte {
	b := make([]byte, 6)
	b[0] = keyKindOccupancy
	binary.BigEndian.PutUint32(b[1:5], uint32(ord))
	b[5] = byte(plane) + 1
	return b
}
