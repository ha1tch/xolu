// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"sync"
	"fmt"

	"github.com/cockroachdb/pebble"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Stage 5: the A9 lifecycle (cal-rest-api.md §4) and move (§6), with INCREMENTAL
// index maintenance — the work Stage 3 deferred to RebuildFrom.
//
// The hazard Stage 3 flagged: a booking's quanta cannot be cleared with a naive
// AND-NOT, because overlapping bookings on the same plane share quanta — clearing
// one would wrongly free a quantum another still holds. The correct, safe
// incremental operation is a SCOPED recompute: for the affected (calendar, plane,
// day) cells only, rebuild the day bitmap from the *remaining* live bookings on
// that plane. This is far cheaper than a whole-store RebuildFrom (it touches only
// the days a single booking spanned) and is exactly as correct, because each
// recomputed day is derived from the authoritative records.
//
// A confirm (proposed -> binding) is two scoped recomputes: remove the booking's
// contribution from the proposed plane's affected days, and add it to the binding
// plane's. A cancel/decline is one scoped recompute on the booking's plane. This
// is also the cross-plane move that Stage 7's seal must make safe.

// PlaneBookingSource extends BookingSource with the scoped query the incremental
// maintenance needs: the live bookings on one calendar that occupy a given plane.
// The in-memory and (future) SQLite sources both implement it.
type PlaneBookingSource interface {
	BookingSource
	// LiveBookingsOn returns live bookings for calendarID whose state occupies
	// the given plane.
	LiveBookingsOn(calendarID string, plane Plane) []Booking
}

// recomputeDays rebuilds, for one (ordinal, plane), exactly the given day-floored
// nanos from the supplied live bookings on that plane. Days that end up empty are
// deleted; non-empty days are written. Only the listed days are touched.
func (s *IndexStore) recomputeDays(ord CalOrdinal, plane Plane, dayNanos map[int64]struct{}, live []Booking) error {
	// Accumulate each affected day's bits from the live bookings.
	acc := map[int64]*DayBitmap{}
	for d := range dayNanos {
		acc[d] = &DayBitmap{} // start empty; may stay empty (delete)
	}
	for _, b := range live {
		span, p, occ := b.occupancySpan()
		if !occ || p != plane {
			continue
		}
		days, err := SpanDays(span)
		if err != nil {
			return err
		}
		for _, dd := range days {
			bm, affected := acc[dd.DayNanos]
			if !affected {
				continue // this booking touches a day outside the scope; ignore
			}
			for i := range bm {
				bm[i] |= dd.Bits[i]
			}
		}
	}

	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	for d, bm := range acc {
		key := EncodeKey(ord, plane, d)
		if bm.IsZero() {
			if err := batch.Delete(key, nil); err != nil {
				return err
			}
		} else {
			if err := batch.Set(key, encodeDayValue(*bm), nil); err != nil {
				return err
			}
		}
	}
	return batch.Commit(pebble.Sync)
}

// affectedDays returns the set of day-floored nanos a span touches.
func affectedDays(span Span) (map[int64]struct{}, error) {
	days, err := SpanDays(span)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]struct{}, len(days))
	for _, d := range days {
		out[d.DayNanos] = struct{}{}
	}
	return out, nil
}

// removeFromPlane scoped-recomputes the days a booking spanned on one plane,
// using the remaining live bookings (the booking itself must already be in a
// non-occupying state, or excluded, in src). Safe under overlap.
func (s *IndexStore) removeFromPlane(src PlaneBookingSource, b Booking, plane Plane) error {
	if s.RemoveFromPlaneFaultHook != nil {
		if err := s.RemoveFromPlaneFaultHook(b, plane); err != nil {
			return err
		}
	}
	ord, err := s.ordinalFor(b.CalendarID)
	if err != nil {
		return err
	}
	// Days are computed from the booking's raw span (buffer included), matching
	// what its occupancy would have covered.
	span := b.Span
	if !b.BufferAfter.IsZero() && b.BufferAfter.After(span.End) {
		span.End = b.BufferAfter
	}
	days, err := affectedDays(span)
	if err != nil {
		return err
	}
	live := src.LiveBookingsOn(b.CalendarID, plane)
	return s.recomputeDays(ord, plane, days, live)
}

// addToPlane ORs a booking's occupancy onto its plane (the create/confirm add
// path). Adding is always safe incrementally (OR cannot corrupt shared bits).
func (s *IndexStore) addToPlane(b Booking) error {
	if s.AddToPlaneFaultHook != nil {
		if err := s.AddToPlaneFaultHook(b); err != nil {
			return err
		}
	}
	return s.applyBooking(b, true)
}

// --- The lifecycle engine ---

// Store is the full authoritative-record interface the Lifecycle engine drives:
// the calendar/booking CRUD plus the scoped queries. Both MemBookingSource and
// the SQLite-backed source implement it, so the lifecycle, commit, and seal
// logic (and every test) are storage-agnostic. It extends PlaneBookingSource
// (which extends BookingSource) with the mutating and lookup operations.
type Store interface {
	PlaneBookingSource

	CreateCalendar(c Calendar) (Calendar, error)
	DeleteCalendar(calendarID string) error
	PutBooking(b Booking) error
	SetStateFrom(calendarID, bookingID string, from, to State) error
	setSpan(calendarID, bookingID string, to Span) error
	calendar(calendarID string) (Calendar, bool)
	booking(calendarID, bookingID string) (Booking, bool)
}

// Lifecycle wires booking-state transitions to both the authoritative record
// (via the source) and the derived index (incrementally). The source is the H1
// truth; the index is kept in step on each transition.
type Lifecycle struct {
	src   Store
	index *IndexStore

	// moveMu serialises Move per calendar. Move is check-then-act
	// (destinationConflicts → setSpan) over shared calendar occupancy;
	// two concurrent Moves of different bookings into one free window
	// both pass the check and both land (T-35). Per-calendar
	// serialisation is preferred over CAS-on-span because the shared
	// state the race contends over is the calendar's occupancy, not
	// any single booking's span. Contention is naturally bounded (Moves
	// are cross-plane in effect and rare compared with reads), and the
	// serialisation is per-calendar so distinct calendars remain parallel.
	moveLoks   map[string]*sync.Mutex
	moveLoksMu sync.Mutex
}

// NewLifecycle binds a source and an index.
func NewLifecycle(src Store, index *IndexStore) *Lifecycle {
	return &Lifecycle{
		src:      src,
		index:    index,
		moveLoks: make(map[string]*sync.Mutex),
	}
}

// moveLockFor returns the per-calendar mutex used to serialise Move
// against races on shared occupancy (T-35). Distinct calendars remain
// parallel; only concurrent Moves *within* one calendar are
// serialised, and only within Move itself — reads and other operations
// are unaffected.
func (l *Lifecycle) moveLockFor(calendarID string) *sync.Mutex {
	l.moveLoksMu.Lock()
	defer l.moveLoksMu.Unlock()
	m, ok := l.moveLoks[calendarID]
	if !ok {
		m = &sync.Mutex{}
		l.moveLoks[calendarID] = m
	}
	return m
}

// allowedTransition reports whether from -> to is a legal A9 transition.
func allowedTransition(from, to State) bool {
	switch from {
	case StateProposed:
		return to == StateBinding || to == StateNotCommitted || to == StateCancelled || to == StateProposed
	case StateBinding:
		return to == StateHonoured || to == StateCancelled || to == StateMissed
	default:
		return false // honoured/cancelled/missed/not-committed are terminal
	}
}

// Create inserts a new booking in the calendar's default state and adds its
// occupancy to the index. Returns the created booking.
func (l *Lifecycle) Create(b Booking) (Booking, error) {
	cal, ok := l.src.calendar(b.CalendarID)
	if !ok {
		return Booking{}, fmt.Errorf("%w: %q", ErrUnknownCalendar, b.CalendarID)
	}
	if b.State == "" {
		b.State = cal.DefaultState
	}
	if b.CreatedAt.IsZero() {
		b.CreatedAt = ot.Now()
	}
	b.UpdatedAt = ot.Now()
	if err := l.src.PutBooking(b); err != nil {
		return Booking{}, err
	}
	if _, occ := b.State.occupiesPlane(); occ {
		if err := l.index.addToPlane(b); err != nil {
			return Booking{}, err
		}
	}
	return b, nil
}

// transition applies a state change to both the record and the index. It handles
// the three index cases: add (entering an occupying state), remove (leaving one),
// and cross-plane move (proposed -> binding).
func (l *Lifecycle) transition(calendarID, bookingID string, to State) error {
	b, ok := l.src.booking(calendarID, bookingID)
	if !ok {
		return fmt.Errorf("%w: %q/%q", ErrUnknownBooking, calendarID, bookingID)
	}
	from := b.State
	if !allowedTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s for %q (allowed from %s: see A9)", ErrIllegalTransition, from, to, bookingID, from)
	}

	fromPlane, fromOcc := from.occupiesPlane()
	toPlane, toOcc := to.occupiesPlane()

	// Apply the authoritative state change first (H1 source of truth).
	// SetStateFrom is a compare-and-swap on the state we just validated:
	// under concurrent racers on the same booking, exactly one write lands
	// and the rest fail with ErrIllegalTransition (T-34 — the "state graph
	// as natural mutex" property the race test enforces).
	if err := l.src.SetStateFrom(calendarID, bookingID, from, to); err != nil {
		return err
	}

	switch {
	case fromOcc && toOcc && fromPlane != toPlane:
		// Cross-plane move (proposed -> binding on confirm): remove from the old
		// plane (scoped recompute, the booking is now in the new state so it no
		// longer contributes to the old plane), then add to the new plane.
		if err := l.index.removeFromPlane(l.src, b, fromPlane); err != nil {
			return err
		}
		// re-read the booking in its new state for the add
		nb, _ := l.src.booking(calendarID, bookingID)
		if err := l.index.addToPlane(nb); err != nil {
			return err
		}
	case fromOcc && !toOcc:
		// Leaving an occupying state (cancel/decline/missed): scoped-remove.
		if err := l.index.removeFromPlane(l.src, b, fromPlane); err != nil {
			return err
		}
	case !fromOcc && toOcc:
		// Entering an occupying state (re-propose from a terminal-ish state):
		nb, _ := l.src.booking(calendarID, bookingID)
		if err := l.index.addToPlane(nb); err != nil {
			return err
		}
	case fromOcc && toOcc && fromPlane == toPlane:
		// Same plane (binding -> honoured): occupancy unchanged, nothing to do.
	}
	return nil
}

// Confirm: proposed -> binding (the cross-plane move).
func (l *Lifecycle) Confirm(calendarID, bookingID string) error {
	return l.transition(calendarID, bookingID, StateBinding)
}

// Decline: proposed -> not-committed.
func (l *Lifecycle) Decline(calendarID, bookingID string) error {
	return l.transition(calendarID, bookingID, StateNotCommitted)
}

// Complete: binding -> honoured (same plane).
func (l *Lifecycle) Complete(calendarID, bookingID string) error {
	return l.transition(calendarID, bookingID, StateHonoured)
}

// Cancel: proposed|binding -> cancelled.
func (l *Lifecycle) Cancel(calendarID, bookingID string) error {
	return l.transition(calendarID, bookingID, StateCancelled)
}

// MarkMissed: binding -> missed (the sweeper-written non-occurrence, §7). Exposed
// for the reconciliation sweeper; not a user endpoint.
func (l *Lifecycle) MarkMissed(calendarID, bookingID string) error {
	return l.transition(calendarID, bookingID, StateMissed)
}

// --- move (§6): atomic reschedule ---

// MoveResult reports the outcome of a move. On conflict the booking is untouched
// and Moved is false.
type MoveResult struct {
	Moved              bool
	Conflicts          []Conflict
	StrandedDependents []string // dependents left behind (reported, never cascaded)
}

// Conflict is one entry of the universal conflict report (§6), shared by create,
// check, and move.
type Conflict struct {
	With   string // booking id clashed with
	Over   Span   // the overlapping region
	Reason string // fixed enum (exclusive-vs-exclusive overlap, etc.)
}

// Move atomically reschedules a booking to a new span. It re-runs placement at
// the destination: if the destination is infeasible the booking is left exactly
// where it was and Moved is false with the conflict report. On success the
// booking occupies the new span and the prior span is gone from the index.
//
// move never cascades to dependents; if any exist they are reported as stranded.
// (Dependency tracking is not in v1's record, so StrandedDependents is always
// empty here; the field exists so the contract is visible and stable.)
func (l *Lifecycle) Move(calendarID, bookingID string, to Span) (MoveResult, error) {
	// T-35: Move is check-then-act over shared calendar occupancy.
	// Serialise per-calendar so exactly one Move at a time can pass
	// destinationConflicts and commit setSpan, closing the window
	// where two concurrent Moves into one free target both land.
	l.moveLockFor(calendarID).Lock()
	defer l.moveLockFor(calendarID).Unlock()

	b, ok := l.src.booking(calendarID, bookingID)
	if !ok {
		return MoveResult{}, fmt.Errorf("cal: Move: %w: %q/%q", ErrUnknownBooking, calendarID, bookingID)
	}
	if !to.Valid() {
		return MoveResult{}, fmt.Errorf("cal: Move: invalid destination span")
	}
	cal, ok := l.src.calendar(calendarID)
	if !ok {
		return MoveResult{}, fmt.Errorf("cal: Move: %w: %q", ErrUnknownCalendar, calendarID)
	}

	plane, occ := b.State.occupiesPlane()
	if !occ {
		return MoveResult{}, fmt.Errorf("cal: Move: booking in non-occupying state %s cannot be moved", b.State)
	}

	// Feasibility at the destination, EXCLUDING this booking's own current
	// occupancy (a booking never conflicts with itself). Read the calendar's
	// occupancy, subtract this booking, then test the destination span.
	conflicts, err := l.destinationConflicts(cal, b, to, plane)
	if err != nil {
		return MoveResult{}, err
	}
	if len(conflicts) > 0 {
		// Refuse: booking untouched.
		return MoveResult{Moved: false, Conflicts: conflicts}, nil
	}

	// Commit the move.
	oldSpan := b.Span
	if err := l.src.setSpan(calendarID, bookingID, to); err != nil {
		return MoveResult{}, err
	}
	// remove old: scoped recompute over old days using remaining live bookings
	// (the booking now has its NEW span in src, so it won't re-add old quanta).
	bOld := b
	bOld.Span = oldSpan
	if err := l.index.removeFromPlane(l.src, bOld, plane); err != nil {
		return MoveResult{}, err
	}
	// add new
	nb, _ := l.src.booking(calendarID, bookingID)
	if err := l.index.addToPlane(nb); err != nil {
		return MoveResult{}, err
	}
	return MoveResult{Moved: true}, nil
}

// destinationConflicts checks whether placing booking b at span `to` would clash
// on its plane, excluding b's own current occupancy. Delegates to spanConflicts
// (the shared conflict-detection used by MatchCommit), excluding b's own id.
func (l *Lifecycle) destinationConflicts(cal Calendar, b Booking, to Span, plane Plane) ([]Conflict, error) {
	return l.spanConflicts(cal, to, plane, b.BookingID)
}

// spanOverlap returns the overlapping region of two half-open spans and whether
// they overlap at all.
func spanOverlap(a, b Span) (Span, bool) {
	startN := a.Start.UnixNano()
	if b.Start.UnixNano() > startN {
		startN = b.Start.UnixNano()
	}
	endN := a.End.UnixNano()
	if b.End.UnixNano() < endN {
		endN = b.End.UnixNano()
	}
	if startN < endN {
		return Span{Start: ot.FromUnixNano(startN), End: ot.FromUnixNano(endN)}, true
	}
	return Span{}, false
}
