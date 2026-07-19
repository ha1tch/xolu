// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"sync"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Stage 7: the now-crossing seal (cal-pebble-codec.md §4.5, §5).
//
// THE INVARIANT (characterised before the code, per the design's design-then-
// race-test mandate):
//
//   A day is SEALED once its window is entirely in the past (day_end <= now).
//   A sealed day is immutable: its bits and pyramid entries are frozen and never
//   recompute. The mutable future stays incrementally maintained; the sealed past
//   is read-only. After ANY interleaving of seal advance and lifecycle mutation,
//   and after ANY crash-and-recover, the index must still equal
//   rebuild_from_sqlite() — the global oracle.
//
// THE HAZARD:
//
//   The seal runs concurrently with mutations. The dangerous interleaving the
//   design names is the confirm cross-plane move (the one operation touching both
//   planes) racing the seal at the now-boundary: a booking sits in the day being
//   sealed while a confirm moves its bits proposed->binding. Without
//   synchronisation the seal could freeze a half-moved state, or a mutation could
//   write a day the seal has declared immutable.
//
// THE MECHANISM:
//
//   A monotone seal FRONTIER: an instant before which every day is sealed. The
//   frontier only advances (monotone), guarded by a mutex shared with mutations.
//   - Advancing the frontier and any index mutation are mutually exclusive: they
//     take the same lock, so a seal can never observe or freeze a half-applied
//     mutation, and a mutation can never write into a day mid-seal.
//   - A mutation whose affected days are entirely after the frontier proceeds
//     normally. A mutation that would touch a day AT OR BEFORE the frontier is
//     touching the sealed past — rejected, because a correctly-behaving system
//     does not mutate the immutable past (a fully-past booking is the sweeper's
//     missed/honoured territory, not a confirm/move target).
//   - Recovery: the frontier and seal state are derived. On restart the frontier
//     is recomputed (max sealed day) and the index is rebuilt from SQLite for any
//     day marked dirty; a lost seal is never a lost booking (H1).
//
//   This is the ts timeline-delete deleting-marker model: settle the invariant,
//   guard the critical section, then hammer the interleaving under -race.

// Sealer manages the now-crossing seal frontier for an IndexStore + Lifecycle.
// It serialises frontier advances against mutations via a single mutex, so the
// seal and the confirm cross-plane move can never destructively interleave.
type Sealer struct {
	mu       sync.Mutex
	frontier ot.Instant // every day with day_end <= frontier is sealed; zero = nothing sealed
	lc       *Lifecycle
}

// NewSealer binds a sealer to a lifecycle. The frontier starts at zero (nothing
// sealed).
func NewSealer(lc *Lifecycle) *Sealer {
	return &Sealer{lc: lc}
}

// Frontier returns the current seal frontier (the instant before which days are
// sealed). Days whose end is at or before the frontier are immutable.
func (s *Sealer) Frontier() ot.Instant {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frontier
}

// dayIsSealed reports whether the day containing dayNanos is sealed under the
// current frontier (caller holds s.mu). A day is sealed once its END (next
// midnight) is at or before the frontier.
func (s *Sealer) dayIsSealedLocked(dayNanos int64) bool {
	if s.frontier.IsZero() {
		return false
	}
	dayEnd := dayNanos + NsPerDay
	return dayEnd <= s.frontier.UnixNano()
}

// spanTouchesSealedLocked reports whether a span touches any sealed day (caller
// holds s.mu).
func (s *Sealer) spanTouchesSealedLocked(span Span) (bool, error) {
	days, err := SpanDays(span)
	if err != nil {
		return false, err
	}
	for _, d := range days {
		if s.dayIsSealedLocked(d.DayNanos) {
			return true, nil
		}
	}
	return false, nil
}

// AdvanceTo advances the seal frontier to `now`, sealing every day whose window
// has fully passed. It is monotone: a request to move the frontier backward is a
// no-op. Advancing takes the mutex, so it cannot interleave with a guarded
// mutation — the seal observes only fully-applied index states.
//
// Sealing here is a logical freeze: because the sealed past is simply never
// mutated again (guarded below), no per-day rewrite is required at seal time. The
// frontier IS the seal. (A physical freeze — copying sealed days to a cold store —
// is the deferred store-split, codec §6.9; v1 seals logically.)
func (s *Sealer) AdvanceTo(now ot.Instant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.After(s.frontier) {
		s.frontier = now
	}
}

// guardMutation runs fn under the seal lock, first rejecting it if the span
// touches a sealed day. This is the synchronisation point: every seal-sensitive
// mutation (confirm, cancel, move) goes through here, so it is mutually exclusive
// with AdvanceTo and can never write a sealed day.
func (s *Sealer) guardMutation(span Span, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sealed, err := s.spanTouchesSealedLocked(span)
	if err != nil {
		return err
	}
	if sealed {
		return &SealedError{Span: span, Frontier: s.frontier}
	}
	return fn()
}

// SealedError is returned when a mutation targets a sealed (immutable past) day.
type SealedError struct {
	Span     Span
	Frontier ot.Instant
}

func (e *SealedError) Error() string {
	return fmt.Sprintf("cal: cannot mutate sealed day: span ends %s, seal frontier %s",
		e.Span.End.Format(time.RFC3339, time.UTC),
		e.Frontier.Format(time.RFC3339, time.UTC))
}

// IsSealed reports whether err is a SealedError.
func IsSealed(err error) bool {
	_, ok := err.(*SealedError)
	return ok
}

// --- Seal-guarded lifecycle operations ---
//
// These wrap the Stage-5 lifecycle operations in the seal guard. They are the
// operations a live system calls when a sealer is present; the unguarded
// Lifecycle methods remain for contexts with no seal (and for rebuild, which is
// not a mutation in the seal's sense).

// ConfirmSealed runs Confirm under the seal guard. The confirm cross-plane move
// (the hazardous one) is rejected if the booking's span touches a sealed day.
func (s *Sealer) ConfirmSealed(calendarID, bookingID string) error {
	b, ok := s.lc.src.booking(calendarID, bookingID)
	if !ok {
		return fmt.Errorf("cal: ConfirmSealed: unknown booking %q/%q", calendarID, bookingID)
	}
	return s.guardMutation(b.Span, func() error {
		return s.lc.Confirm(calendarID, bookingID)
	})
}

// CancelSealed runs Cancel under the seal guard.
func (s *Sealer) CancelSealed(calendarID, bookingID string) error {
	b, ok := s.lc.src.booking(calendarID, bookingID)
	if !ok {
		return fmt.Errorf("cal: CancelSealed: unknown booking %q/%q", calendarID, bookingID)
	}
	return s.guardMutation(b.Span, func() error {
		return s.lc.Cancel(calendarID, bookingID)
	})
}

// MoveSealed runs Move under the seal guard, rejecting it if EITHER the current
// span or the destination touches a sealed day (you can move neither out of nor
// into the sealed past).
func (s *Sealer) MoveSealed(calendarID, bookingID string, to Span) (MoveResult, error) {
	b, ok := s.lc.src.booking(calendarID, bookingID)
	if !ok {
		return MoveResult{}, fmt.Errorf("cal: MoveSealed: unknown booking %q/%q", calendarID, bookingID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, span := range []Span{b.Span, to} {
		sealed, err := s.spanTouchesSealedLocked(span)
		if err != nil {
			return MoveResult{}, err
		}
		if sealed {
			return MoveResult{}, &SealedError{Span: span, Frontier: s.frontier}
		}
	}
	return s.lc.Move(calendarID, bookingID, to)
}

// CreateSealed runs Create under the seal guard: a new booking cannot be created
// into the sealed past.
func (s *Sealer) CreateSealed(b Booking) (Booking, error) {
	var out Booking
	err := s.guardMutation(b.Span, func() error {
		created, err := s.lc.Create(b)
		out = created
		return err
	})
	return out, err
}
