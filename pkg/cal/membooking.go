// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"sync"
)

// MemBookingSource is an in-memory implementation of BookingSource: the
// authoritative booking + calendar records (H1) held in maps. Stage 3 uses it to
// exercise the index write path and the index == rebuild invariant before the
// SQLite wiring lands. The SQLite-backed source will implement the same
// BookingSource interface, so Rebuild and the invariant test are unchanged.
//
// It also owns the dense per-tenant cal_ordinal counter (GATE-3 #5): a uint32
// allocated ascending from 1. OrdinalReuse policy governs whether a deleted
// calendar's ordinal returns to the pool; the default is retire (counter only
// moves up).
type MemBookingSource struct {
	mu          sync.Mutex
	calendars   map[string]Calendar
	bookings    map[string]Booking // keyed by calendarID + "\x00" + bookingID
	nextOrdinal CalOrdinal
	reuse       bool
	freeOrds    []CalOrdinal // retired ordinals available for reuse (when reuse=true)
}

// NewMemBookingSource returns an empty source. reuse selects the OrdinalReuse
// policy (false = retire, the safe default; true = reuse retired ordinals).
func NewMemBookingSource(reuse bool) *MemBookingSource {
	return &MemBookingSource{
		calendars:   map[string]Calendar{},
		bookings:    map[string]Booking{},
		nextOrdinal: 1, // ordinals allocate ascending from 1; 0 is reserved
		reuse:       reuse,
	}
}

func bookingKey(calendarID, bookingID string) string {
	return calendarID + "\x00" + bookingID
}

// CreateCalendar registers a calendar, allocating its dense ordinal. The caller
// supplies policy fields; Ordinal is assigned here and returned in the stored
// record.
func (m *MemBookingSource) CreateCalendar(c Calendar) (Calendar, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.CalendarID == "" {
		return Calendar{}, fmt.Errorf("cal: CreateCalendar: empty calendar_id")
	}
	if _, exists := m.calendars[c.CalendarID]; exists {
		return Calendar{}, fmt.Errorf("cal: CreateCalendar: %w: %q", ErrCalendarExists, c.CalendarID)
	}
	c.Ordinal = m.allocOrdinalLocked()
	if c.DefaultState == "" {
		c.DefaultState = StateBinding
	}
	if c.MatchPolicy == "" {
		c.MatchPolicy = ConsiderBinding
	}
	m.calendars[c.CalendarID] = c
	return c, nil
}

func (m *MemBookingSource) allocOrdinalLocked() CalOrdinal {
	if m.reuse && len(m.freeOrds) > 0 {
		ord := m.freeOrds[len(m.freeOrds)-1]
		m.freeOrds = m.freeOrds[:len(m.freeOrds)-1]
		return ord
	}
	ord := m.nextOrdinal
	m.nextOrdinal++
	return ord
}

// DeleteCalendar removes a calendar and (under reuse policy) returns its ordinal
// to the pool. Its bookings must already be gone.
func (m *MemBookingSource) DeleteCalendar(calendarID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.calendars[calendarID]
	if !ok {
		return fmt.Errorf("cal: DeleteCalendar: unknown %q", calendarID)
	}
	delete(m.calendars, calendarID)
	if m.reuse {
		m.freeOrds = append(m.freeOrds, c.Ordinal)
	}
	return nil
}

// PutBooking inserts or updates a booking record (the authoritative H1 write).
func (m *MemBookingSource) PutBooking(b Booking) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b.BookingID == "" || b.CalendarID == "" {
		return fmt.Errorf("cal: PutBooking: empty booking_id or calendar_id")
	}
	if _, ok := m.calendars[b.CalendarID]; !ok {
		return fmt.Errorf("cal: PutBooking: %w: %q", ErrUnknownCalendar, b.CalendarID)
	}
	if !b.Span.Valid() {
		return fmt.Errorf("cal: PutBooking: invalid span")
	}
	// Mode rule: only ModeExclusive is valid. See sqlitesource.go for
	// full rationale.
	if b.Mode == "" {
		b.Mode = ModeExclusive
	}
	if b.Mode != ModeExclusive {
		return fmt.Errorf("cal: PutBooking: %w: %q", ErrModeNotSupported, string(b.Mode))
	}
	// Bearer rule (review issue 2): a binding booking requires a live bearer.
	if b.State == StateBinding && !ValidEntity(b.Bearer) {
		return fmt.Errorf("cal: PutBooking: binding booking requires a live bearer")
	}
	m.bookings[bookingKey(b.CalendarID, b.BookingID)] = b
	return nil
}

// SetStateFrom transitions a booking to a new state (the A9 lifecycle
// write) if and only if it is still in the expected from-state — the
// compare half of the T-34 compare-and-swap, evaluated under the source
// lock so concurrent racers serialise here.
func (m *MemBookingSource) SetStateFrom(calendarID, bookingID string, from, to State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := bookingKey(calendarID, bookingID)
	b, ok := m.bookings[k]
	if !ok {
		return fmt.Errorf("cal: SetStateFrom: unknown booking %q/%q", calendarID, bookingID)
	}
	if b.State != from {
		return fmt.Errorf("%w: %s -> %s for %q (state is now %s; lost the race)",
			ErrIllegalTransition, from, to, bookingID, b.State)
	}
	if to == StateBinding && !ValidEntity(b.Bearer) {
		return fmt.Errorf("cal: SetStateFrom: binding requires a live bearer")
	}
	b.State = to
	m.bookings[k] = b
	return nil
}

// Calendars implements BookingSource.
func (m *MemBookingSource) Calendars() []Calendar {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Calendar, 0, len(m.calendars))
	for _, c := range m.calendars {
		out = append(out, c)
	}
	return out
}

// LiveBookings implements BookingSource: only plane-occupying states.
func (m *MemBookingSource) LiveBookings() []Booking {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Booking
	for _, b := range m.bookings {
		if _, occ := b.State.occupiesPlane(); occ {
			out = append(out, b)
		}
	}
	return out
}

// LiveBookingsOn implements PlaneBookingSource: live bookings for one calendar
// that occupy the given plane.
func (m *MemBookingSource) LiveBookingsOn(calendarID string, plane Plane) []Booking {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Booking
	for _, b := range m.bookings {
		if b.CalendarID != calendarID {
			continue
		}
		p, occ := b.State.occupiesPlane()
		if occ && p == plane {
			out = append(out, b)
		}
	}
	return out
}

// calendar returns a calendar record by id.
func (m *MemBookingSource) calendar(calendarID string) (Calendar, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.calendars[calendarID]
	return c, ok
}

// booking returns a booking record by calendar+id.
func (m *MemBookingSource) booking(calendarID, bookingID string) (Booking, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bookings[bookingKey(calendarID, bookingID)]
	return b, ok
}

// setSpan updates a booking's span (the move write). Times remain UTC instants.
func (m *MemBookingSource) setSpan(calendarID, bookingID string, to Span) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := bookingKey(calendarID, bookingID)
	b, ok := m.bookings[k]
	if !ok {
		return fmt.Errorf("cal: setSpan: unknown booking %q/%q", calendarID, bookingID)
	}
	if !to.Valid() {
		return fmt.Errorf("cal: setSpan: invalid span")
	}
	b.Span = to
	m.bookings[k] = b
	return nil
}
