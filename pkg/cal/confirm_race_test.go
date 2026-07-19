// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// This file enforces the "state graph as natural mutex" property: when N
// goroutines race on the same terminal transition of a single booking,
// exactly one wins and the others get ErrIllegalTransition.
//
// This is not a nice-to-have. Downstream callers (molu Part 2 tools,
// concurrent event dispatchers) rely on transitions being at-most-once
// per booking. If a future change to `Lifecycle.transition` or
// `SQLiteBookingSource.SetState` allowed both racers to succeed (for
// example by removing the state-graph check after SetState, or by
// converting to an unconditional overwrite), that would be a silent
// correctness break. This test catches it.
//
// Run under `-race` in CI: the data-race detector complements the
// outcome-count assertion by catching mid-transition failure modes this
// test cannot observe from outcomes alone.
//
// The property is exercised for the cross-plane transition (Confirm:
// proposed -> binding), a same-plane terminal transition (Complete:
// binding -> honoured), and Cancel from both starting states, so the
// guarantee covers the whole terminal-transition surface, not just
// Confirm.

// concurrencyN is intentionally small: enough to stress the read-check-write
// window under GOMAXPROCS > 1, small enough to keep the test fast. Larger
// N does not increase confidence — the property either holds or it does
// not, and 32 is well past the point where a broken implementation would
// fail on at least one goroutine of the batch.
const concurrencyN = 32

// trials repeats the race a few times per invocation. A single trial has
// a small but non-zero chance of accidentally serialising all N
// goroutines through scheduling luck. Multiple trials give the scheduler
// more opportunities to interleave, reducing the false-pass rate against
// a hypothetical broken implementation.
const trials = 5

func TestConcurrentTerminalTransition_ExactlyOneWins(t *testing.T) {
	cases := []struct {
		name       string
		fromState  State
		transition func(lc *Lifecycle, cal, book string) error
	}{
		{
			name:       "Confirm proposed to binding (cross-plane)",
			fromState:  StateProposed,
			transition: func(lc *Lifecycle, cal, book string) error { return lc.Confirm(cal, book) },
		},
		{
			name:       "Complete binding to honoured (same-plane)",
			fromState:  StateBinding,
			transition: func(lc *Lifecycle, cal, book string) error { return lc.Complete(cal, book) },
		},
		{
			name:       "Cancel from proposed",
			fromState:  StateProposed,
			transition: func(lc *Lifecycle, cal, book string) error { return lc.Cancel(cal, book) },
		},
		{
			name:       "Cancel from binding",
			fromState:  StateBinding,
			transition: func(lc *Lifecycle, cal, book string) error { return lc.Cancel(cal, book) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for trial := 0; trial < trials; trial++ {
				runTerminalTransitionRace(t, trial, tc.fromState, tc.transition)
			}
		})
	}
}

// runTerminalTransitionRace exercises one race trial: seed a fresh
// booking in fromState, fire N goroutines racing on `transition`, then
// verify the outcome distribution and index consistency.
//
// Assertions:
//
//  1. Exactly 1 goroutine returns nil.
//  2. The remaining N-1 return an error wrapping ErrIllegalTransition.
//  3. No goroutine returns any other error kind.
//  4. The booking's final state reflects a completed transition — not
//     stuck in fromState (which would indicate everyone failed and no
//     one committed).
//  5. The in-memory occupancy index matches a full rebuild from the
//     source — the race did not corrupt the index.
func runTerminalTransitionRace(
	t *testing.T,
	trial int,
	fromState State,
	transition func(lc *Lifecycle, cal, book string) error,
) {
	t.Helper()

	// A distinct calendar per trial so multiple trials do not interact
	// through the shared IndexStore.
	calID := fmt.Sprintf("cal-t%d", trial)
	lc, idx, src := setupLifecycle(t, calID)

	// Seed via Create (always legal from empty). If we want to start the
	// race from binding, walk one legal step forward via Confirm.
	start := ot.FromUnixNano(int64(1_700_000_000_000_000_000) + int64(trial)*int64(3_600_000_000_000))
	end := start.Add(3600 * 1e9)
	bookID := "b1"
	if _, err := lc.Create(Booking{
		BookingID:  bookID,
		CalendarID: calID,
		State:      StateProposed,
		Span:       Span{Start: start, End: end},
		Bearer:     1,
	}); err != nil {
		t.Fatalf("seed create: %v", err)
	}
	if fromState == StateBinding {
		if err := lc.Confirm(calID, bookID); err != nil {
			t.Fatalf("seed confirm to binding: %v", err)
		}
	}

	var okCount, illegalCount, otherCount int64
	var otherErrs []string
	var otherMu sync.Mutex

	var wg sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < concurrencyN; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-release
			err := transition(lc, calID, bookID)
			switch {
			case err == nil:
				atomic.AddInt64(&okCount, 1)
			case errors.Is(err, ErrIllegalTransition):
				atomic.AddInt64(&illegalCount, 1)
			default:
				atomic.AddInt64(&otherCount, 1)
				otherMu.Lock()
				otherErrs = append(otherErrs, err.Error())
				otherMu.Unlock()
			}
		}()
	}
	close(release)
	wg.Wait()

	if got := atomic.LoadInt64(&okCount); got != 1 {
		t.Errorf("trial %d: expected exactly 1 success, got %d (illegal=%d other=%d)",
			trial, got, illegalCount, otherCount)
	}
	if got := atomic.LoadInt64(&illegalCount); got != concurrencyN-1 {
		t.Errorf("trial %d: expected %d ErrIllegalTransition, got %d",
			trial, concurrencyN-1, got)
	}
	if got := atomic.LoadInt64(&otherCount); got != 0 {
		otherMu.Lock()
		samples := otherErrs
		if len(samples) > 3 {
			samples = samples[:3]
		}
		otherMu.Unlock()
		t.Errorf("trial %d: unexpected non-illegal errors: %d (samples: %v)",
			trial, got, samples)
	}

	b, ok := src.booking(calID, bookID)
	if !ok {
		t.Fatalf("trial %d: booking vanished after race", trial)
	}
	if b.State == fromState {
		t.Errorf("trial %d: booking still in fromState=%s after race — nobody committed",
			trial, fromState)
	}

	assertIndexMatchesRebuild(t, idx, src, fmt.Sprintf("terminal-transition race trial %d", trial))
}
