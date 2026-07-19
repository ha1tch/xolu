// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"sort"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Stage 6: match/commit (cal-rest-api.md F-F, §9) — atomic cross-calendar
// placement. The write counterpart to match (Stage 4): match finds WHEN all
// calendars are free; commit SEIZES a chosen when, placing one booking per
// calendar, all-or-nothing.
//
// This is GATE-2 (in v1). It is a consistency unit, not a workflow: no ordering,
// no objective, no joint constraints beyond coexistence — the anti-solver line
// (A4) stays intact. The guarantee is identical to a single-calendar batch,
// lifted to the cross-calendar boundary that match already spans: every booking
// lands or none does; on conflict nothing is committed and the blocking
// calendars are named.
//
// Atomicity discipline: check ALL calendars feasible at the span FIRST, against
// the unmodified index; only if every check passes do we commit any booking.
// Because the placements are at the same span on DISTINCT calendars, they cannot
// conflict with each other — so a clean pre-check guarantees a clean commit, and
// there is no interleaving hazard within the set. (Within xolu's settled
// one-store-per-tenant layout this maps to a single Pebble batch plus an N-record
// SQLite transaction; the in-memory source models the same all-or-nothing
// boundary.)

// CommitMember is one calendar's part of a cross-calendar placement.
type CommitMember struct {
	CalendarID string
	BookingID  string
	Mode       Mode
	Bearer     uint64 // required live for a binding placement
}

// CommitResult reports the outcome. On success Committed is true and Placed
// lists the booking ids. On conflict Committed is false, nothing was written,
// and Blocking names the calendars whose occupancy prevented placement.
type CommitResult struct {
	Committed bool
	Placed    []string // booking ids placed (only when Committed)
	Blocking  []string // calendars that blocked (only when not Committed)
	Conflicts []Conflict
}

// resolvedMember pairs a member with its resolved calendar record.
type resolvedMember struct {
	cal    Calendar
	member CommitMember
}

// MatchCommit places one booking per calendar at the given span, atomically.
// Each member's booking lands in its calendar's default state (proposed or
// binding). All N land or none do.
func (l *Lifecycle) MatchCommit(when Span, members []CommitMember) (CommitResult, error) {
	if len(members) == 0 {
		return CommitResult{}, fmt.Errorf("cal: MatchCommit: no members")
	}
	if !when.Valid() {
		return CommitResult{}, fmt.Errorf("cal: MatchCommit: invalid span")
	}

	// Resolve every calendar up front; an unknown calendar fails the whole set
	// before any write.
	res := make([]resolvedMember, 0, len(members))
	seenBooking := map[string]struct{}{}
	for _, m := range members {
		cal, ok := l.src.calendar(m.CalendarID)
		if !ok {
			return CommitResult{}, fmt.Errorf("cal: MatchCommit: %w: %q", ErrUnknownCalendar, m.CalendarID)
		}
		if m.BookingID == "" {
			return CommitResult{}, fmt.Errorf("cal: MatchCommit: empty booking_id for calendar %q", m.CalendarID)
		}
		// guard against the same booking id being placed twice in one set
		if _, dup := seenBooking[m.CalendarID+"\x00"+m.BookingID]; dup {
			return CommitResult{}, fmt.Errorf("cal: MatchCommit: duplicate booking %q on %q", m.BookingID, m.CalendarID)
		}
		seenBooking[m.CalendarID+"\x00"+m.BookingID] = struct{}{}
		res = append(res, resolvedMember{cal: cal, member: m})
	}

	// PHASE 1 — check all feasible, writing nothing. Placements are at the same
	// span on distinct calendars, so they never conflict with one another; each
	// is checked independently against its calendar's current occupancy on the
	// plane its default state targets.
	var blocking []string
	var allConflicts []Conflict
	for _, r := range res {
		plane := planeForDefaultState(r.cal.DefaultState)
		conflicts, err := l.spanConflicts(r.cal, when, plane, "")
		if err != nil {
			return CommitResult{}, err
		}
		if len(conflicts) > 0 {
			blocking = append(blocking, r.cal.CalendarID)
			allConflicts = append(allConflicts, conflicts...)
		}
	}
	if len(blocking) > 0 {
		sort.Strings(blocking)
		return CommitResult{Committed: false, Blocking: blocking, Conflicts: allConflicts}, nil
	}

	// PHASE 2 — commit all. Every check passed and the members cannot conflict
	// with each other, so each Create succeeds. (A Create error here would be an
	// integrity fault, not a placement conflict; surface it and stop. In the
	// SQLite-backed implementation phases 1-2 run inside one transaction so a
	// phase-2 fault rolls the whole set back; the in-memory model places
	// sequentially after a clean pre-check.)
	var placed []string
	for _, r := range res {
		b := Booking{
			BookingID:  r.member.BookingID,
			CalendarID: r.cal.CalendarID,
			State:      r.cal.DefaultState,
			Span:       when,
			Mode:       r.member.Mode,
			Bearer:     r.member.Bearer,
			CreatedAt:  ot.Now(),
			UpdatedAt:  ot.Now(),
		}
		if _, err := l.Create(b); err != nil {
			// Integrity fault mid-commit: roll back what we placed to honour
			// all-or-nothing, then surface the error.
			l.rollback(placed, res)
			return CommitResult{}, fmt.Errorf("cal: MatchCommit: commit fault on %q/%q: %w", r.cal.CalendarID, r.member.BookingID, err)
		}
		placed = append(placed, r.member.BookingID)
	}
	return CommitResult{Committed: true, Placed: placed}, nil
}

// rollback cancels already-placed members after a mid-commit integrity fault, so
// the set honours all-or-nothing even on an unexpected error.
func (l *Lifecycle) rollback(placedBookingIDs []string, res []resolvedMember) {
	placedSet := map[string]struct{}{}
	for _, id := range placedBookingIDs {
		placedSet[id] = struct{}{}
	}
	for _, r := range res {
		if _, ok := placedSet[r.member.BookingID]; ok {
			_ = l.Cancel(r.cal.CalendarID, r.member.BookingID)
		}
	}
}

// planeForDefaultState returns the plane a freshly-created booking in the given
// default state occupies.
func planeForDefaultState(st State) Plane {
	if p, occ := st.occupiesPlane(); occ {
		return p
	}
	return PlaneBinding // default state is always proposed or binding; this is a guard
}

// spanConflicts checks whether placing an exclusive booking at `when` on a
// calendar's plane would clash, excluding the booking id `exclude` (empty for a
// fresh placement). Shared by MatchCommit and reusable by check/create.
func (l *Lifecycle) spanConflicts(cal Calendar, when Span, plane Plane, exclude string) ([]Conflict, error) {
	live := l.src.LiveBookingsOn(cal.CalendarID, plane)
	var conflicts []Conflict
	for _, other := range live {
		if other.BookingID == exclude {
			continue
		}
		ov, overlaps := spanOverlap(other.Span, when)
		if overlaps {
			conflicts = append(conflicts, Conflict{
				With:   other.BookingID,
				Over:   ov,
				Reason: "exclusive-vs-exclusive overlap",
			})
		}
	}
	return conflicts, nil
}
