// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Stage 3 types: the booking record (the SQLite source of truth, H1) and the A9
// lifecycle state, per cal-gate3-booking-record.md. The bitmap index is a pure
// function of the live bookings here.

// State is an A9 lifecycle state.
type State string

const (
	StateProposed     State = "proposed"      // tentative; occupies the proposed plane
	StateBinding      State = "binding"       // confirmed; occupies the binding plane
	StateHonoured     State = "honoured"      // completed; occupies the binding plane
	StateNotCommitted State = "not-committed" // declined proposal; occupies no plane
	StateCancelled    State = "cancelled"     // cancelled; occupies no plane
	StateMissed       State = "missed"        // sweeper-written non-occurrence; no plane
)

// occupiesPlane reports whether a state contributes occupancy, and on which
// plane. The second return is meaningful only when the first is true.
//
//	proposed            -> proposed plane
//	binding, honoured   -> binding plane
//	terminal exits      -> no plane (frees the quanta)
func (st State) occupiesPlane() (Plane, bool) {
	switch st {
	case StateProposed:
		return PlaneProposed, true
	case StateBinding, StateHonoured:
		return PlaneBinding, true
	default:
		return 0, false
	}
}

// Mode is the exclusivity vocabulary. Cal implements exclusive-only
// resource semantics matching Google Calendar's room model: a booking
// takes the whole calendar for its span, and any overlapping booking
// is a conflict.
//
// The wire vocabulary previously included ModeShared ("consumes one
// unit of capacity") and ModeSubPrefix ("sub:<child_id>") as reserved
// future extensions for pooled and sub-resource semantics. Both were
// removed in v0.14.12 after a design review compared cal against
// Google Calendar's actual model (rooms are boolean; capacity is
// descriptive metadata for filtering, not a booking-concurrency limit)
// and confirmed cal's target is the same shape. Implementing pooled
// resources properly would require a counter-based bitmap encoding
// with ~8x storage growth and no user-facing feature that Google
// Calendar itself provides; the vocabulary was removed rather than
// left as accepted-but-inert.
type Mode string

const (
	ModeExclusive Mode = "exclusive" // whole resource, the only valid value
)

// MatchConsiders is the GATE-1 per-calendar match policy.
type MatchConsiders string

const (
	ConsiderBinding         MatchConsiders = "binding"          // optimistic: proposals don't block
	ConsiderBindingProposed MatchConsiders = "binding+proposed" // pessimistic: proposals block
)

// Calendar is the calendar definition record (the policy fields the substrate
// would otherwise have to guess, cal-rest-api.md §1, plus GATE-1's match policy).
//
// Cal implements exclusive-only occupancy (see Mode godoc). The Capacity field
// that existed prior to v0.14.13 has been removed: it was descriptive metadata
// only, the occupancy engine never honoured N > 1, and the design review with
// Google Calendar's model confirmed exclusive-only as cal's target. Callers
// that need "how many humans fit in this room" metadata should carry it on a
// separate entity record, not on the calendar itself.
type Calendar struct {
	CalendarID   string
	Ordinal      CalOrdinal // dense per-tenant key-space coordinate (codec §3.2)
	EntityRef    uint64     // the one entity this calendar tracks (§1)
	DefaultState State      // proposed | binding (§1)
	MatchPolicy  MatchConsiders
}

// Booking is the authoritative booking record (H1). Times are absolute UTC
// instants (xolutime); the originating wall-clock intention is NOT stored here —
// it is the caller's, per R-T1.
type Booking struct {
	BookingID   string
	CalendarID  string
	State       State
	Span        Span       // [Start, End) UTC
	Mode        Mode       // exclusive (the only valid value; see Mode godoc)
	Bearer      uint64     // entity handle (A10); required live for binding
	BufferAfter ot.Instant // optional: end+buffer; zero Instant = no buffer
	CreatedAt   ot.Instant
	UpdatedAt   ot.Instant
	DetailRef   string // optional ref into the meta/detail document
}

// occupancySpan returns the span a booking contributes to the index, including
// its aftermath buffer if set (the lock is held past End, Part D). Returns the
// span and whether the booking occupies any plane in its current state.
func (b Booking) occupancySpan() (Span, Plane, bool) {
	plane, occ := b.State.occupiesPlane()
	if !occ {
		return Span{}, 0, false
	}
	s := b.Span
	if !b.BufferAfter.IsZero() && b.BufferAfter.After(s.End) {
		s.End = b.BufferAfter
	}
	return s, plane, true
}
