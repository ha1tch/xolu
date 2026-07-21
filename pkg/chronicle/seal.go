// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"fmt"
	"sync"
	"time"
)

// The Sealer, lifted from cal (@C §4 extraction #2; cal's stage-7
// now-crossing seal, cal-pebble-codec.md §4.5/§5), window-generalised.
//
// THE INVARIANT (cal's, generalised): a window is SEALED once it lies
// entirely in the past of the frontier (window end <= frontier). A
// sealed window is immutable — never mutated, never recomputed. The
// mutable present stays incrementally maintained; the sealed past is
// read-only.
//
// THE DISCIPLINE (the expensively learned part, lifted intact): frontier
// advance and every seal-sensitive mutation take THE SAME mutex. The
// seal can therefore never observe or freeze a half-applied mutation,
// and a mutation can never write into a window mid-seal. A mutation
// touching a window at or before the frontier is touching the immutable
// past — rejected with *SealedError*, never silently dropped.
//
// What did NOT lift (deliberately): cal's Lifecycle binding, its
// day/pyramid arithmetic, and its recovery wiring (frontier recomputed
// from the store on restart) — recovery is the consumer's, because the
// consumer owns the durable record. cal itself is NOT re-plumbed onto
// this type (@C §5: opportunistically or never); bal's period close
// (item 16) is the first native consumer, sealing calendar months.

// WindowFn maps an instant to the half-open window [start, end)
// containing it, in UTC. Windows must tile time: contiguous,
// non-overlapping, end(t) == start of the next window. cal instantiates
// UTC days; bal instantiates calendar months; fixed-width consumers use
// GrainWindows.
type WindowFn func(t time.Time) (start, end time.Time)

// GrainWindows adapts a fixed-width Grain to a WindowFn.
func GrainWindows(g Grain) WindowFn {
	return func(t time.Time) (time.Time, time.Time) {
		s := g.Truncate(t)
		return s, s.Add(g.Width)
	}
}

// MonthWindows tiles time into UTC calendar months — bal's period shape.
func MonthWindows(t time.Time) (time.Time, time.Time) {
	t = t.UTC()
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// Sealer manages a monotone seal frontier over a window tiling, and
// serialises frontier advance against guarded mutations. The zero
// frontier means nothing is sealed.
type Sealer struct {
	mu       sync.Mutex
	frontier time.Time
	window   WindowFn
}

// NewSealer constructs a sealer over the given window tiling.
func NewSealer(window WindowFn) (*Sealer, error) {
	if window == nil {
		return nil, fmt.Errorf("chronicle: NewSealer requires a WindowFn")
	}
	return &Sealer{window: window}, nil
}

// Frontier returns the current seal frontier. Windows ending at or
// before it are immutable.
func (s *Sealer) Frontier() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frontier
}

// AdvanceTo advances the frontier to now (monotone: moving backward is a
// no-op). Sealing is a logical freeze — the frontier IS the seal; no
// per-window rewrite happens here (cal's model, kept: a physical cold
// move is a consumer/store concern).
func (s *Sealer) AdvanceTo(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.After(s.frontier) {
		s.frontier = now.UTC()
	}
}

// sealedLocked reports whether the window containing t is sealed under
// the current frontier. Caller holds s.mu.
func (s *Sealer) sealedLocked(t time.Time) bool {
	if s.frontier.IsZero() {
		return false
	}
	_, end := s.window(t)
	return !end.After(s.frontier) // end <= frontier
}

// Sealed reports whether the window containing t is sealed.
func (s *Sealer) Sealed(t time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sealedLocked(t)
}

// spanTouchesSealedLocked reports whether any window intersecting the
// half-open span [from, to) is sealed. Caller holds s.mu. Walks window
// by window, so it is exact for irregular tilings (months).
func (s *Sealer) spanTouchesSealedLocked(from, to time.Time) bool {
	if !from.Before(to) {
		return s.sealedLocked(from)
	}
	for t := from; t.Before(to); {
		if s.sealedLocked(t) {
			return true
		}
		_, end := s.window(t)
		if !end.After(t) { // defensive: a non-advancing tiling would loop forever
			return true
		}
		t = end
	}
	return false
}

// Guard runs fn under the seal lock iff the span [from, to) touches no
// sealed window; otherwise it returns *SealedError and fn never runs.
// This is the lifted serialisation discipline: Guard and AdvanceTo are
// mutually exclusive, so the seal observes only fully-applied states
// and mutations never land in the immutable past.
//
// fn runs WITH the lock held — it must be the consumer's short critical
// section (the index write, the bucket update), not long I/O. cal's
// hazard analysis applies verbatim: the one operation touching two
// planes must sit entirely inside the guard or the interleaving returns.
func (s *Sealer) Guard(from, to time.Time, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spanTouchesSealedLocked(from, to) {
		return &SealedError{From: from, To: to, Frontier: s.frontier}
	}
	return fn()
}

// SealedError reports a mutation refused because its span touches the
// sealed (immutable) past.
type SealedError struct {
	From, To time.Time
	Frontier time.Time
}

func (e *SealedError) Error() string {
	return fmt.Sprintf("chronicle: span [%s, %s) touches the sealed past (frontier %s)",
		e.From.Format(time.RFC3339), e.To.Format(time.RFC3339), e.Frontier.Format(time.RFC3339))
}
