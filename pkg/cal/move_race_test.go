// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// T-35 investigation: Move is check-then-act.
//
// destinationConflicts reads the calendar's occupancy (excluding this
// booking's own contribution), tests the destination span for
// conflicts, then setSpan overwrites the span guarded only by
// booking-id existence — not by an expected old span. Two concurrent
// Moves of *different* bookings into the *same* free window each pass
// their own destinationConflicts check (each excludes only itself),
// then both setSpan succeed: the calendar ends with two exclusive-mode
// bookings sharing one window.
//
// This test races N Moves of N distinct bookings into one free target
// window and asserts exactly one wins. If both/all win, the assertion
// fires and T-35 is confirmed as a real race the CAS pattern must fix
// (setSpan gains a WHERE start_utc=? AND end_utc=? clause on the
// expected old span, symmetric to SetStateFrom).
//
// Assertions:
//
//  1. Exactly 1 goroutine returns Moved=true with no error.
//  2. All others return Moved=false with a non-empty Conflicts list —
//     OR they too return Moved=true, in which case we have proof of
//     the race and the test fails naming how many raced-in winners
//     landed.
//  3. The final index shows exactly one booking occupying the target
//     window (not N, not zero).
//  4. The index rebuild from the source matches the live index —
//     no half-applied state.
//
// Runs under -race in CI (G-04): the data-race detector complements
// the outcome-count assertion.

const moveRacers = 16
const moveTrials = 5

func TestConcurrentMove_ExactlyOneOccupiesTarget(t *testing.T) {
	for trial := 0; trial < moveTrials; trial++ {
		t.Run(fmt.Sprintf("trial-%d", trial), func(t *testing.T) {
			runMoveRace(t, trial)
		})
	}
}

func runMoveRace(t *testing.T, trial int) {
	t.Helper()

	calID := fmt.Sprintf("cal-move-t%d", trial)
	lc, idx, src := setupLifecycle(t, calID)

	// Seed N proposed bookings at distinct, non-overlapping windows.
	// Then race them all onto ONE common target window that starts far
	// enough after every source window to guarantee no
	// self-vs-self conflict during the pre-check.
	baseNs := int64(1_700_000_000_000_000_000) + int64(trial)*int64(24*3600*1e9)
	hourNs := int64(3600 * 1e9)

	// Target window: hour +100 through hour +101, well beyond every seed
	// so no source window can accidentally overlap it.
	targetStart := ot.FromUnixNano(baseNs + 100*hourNs)
	targetEnd := ot.FromUnixNano(baseNs + 101*hourNs)
	target := Span{Start: targetStart, End: targetEnd}

	bookIDs := make([]string, moveRacers)
	for i := 0; i < moveRacers; i++ {
		bookIDs[i] = fmt.Sprintf("mover-%02d", i)
		start := ot.FromUnixNano(baseNs + int64(i)*hourNs)
		end := ot.FromUnixNano(baseNs + int64(i+1)*hourNs)
		if _, err := lc.Create(Booking{
			BookingID:  bookIDs[i],
			CalendarID: calID,
			State:      StateBinding, // occupies exclusive mode for a real conflict test
			Span:       Span{Start: start, End: end},
			Bearer:     uint64(i + 1),
		}); err != nil {
			t.Fatalf("seed create %s: %v", bookIDs[i], err)
		}
	}

	// Fire the racers.
	var winners, losers, other int64
	var otherErrs []string
	var otherMu sync.Mutex

	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(moveRacers)
	for i := 0; i < moveRacers; i++ {
		bid := bookIDs[i]
		go func() {
			defer done.Done()
			start.Wait()
			res, err := lc.Move(calID, bid, target)
			if err != nil {
				otherMu.Lock()
				otherErrs = append(otherErrs, fmt.Sprintf("%s: %v", bid, err))
				otherMu.Unlock()
				atomic.AddInt64(&other, 1)
				return
			}
			if res.Moved {
				atomic.AddInt64(&winners, 1)
			} else {
				atomic.AddInt64(&losers, 1)
			}
		}()
	}
	start.Done()
	done.Wait()

	// The property: exactly one winner.
	w := atomic.LoadInt64(&winners)
	l := atomic.LoadInt64(&losers)
	o := atomic.LoadInt64(&other)
	if w != 1 {
		t.Errorf("T-35: expected exactly 1 winner into target window, got %d (losers=%d, errors=%d)",
			w, l, o)
	}
	if w+l+o != int64(moveRacers) {
		t.Errorf("account mismatch: winners=%d losers=%d errors=%d, expected sum=%d",
			w, l, o, moveRacers)
	}
	if o > 0 {
		t.Errorf("unexpected non-conflict errors: %v", otherErrs)
	}

	// Verify at most one booking sits at the target span in the source.
	// If T-35 fires, we'll see multiple.
	count := 0
	for _, bid := range bookIDs {
		b, ok := src.booking(calID, bid)
		if !ok {
			t.Fatalf("booking %s vanished", bid)
		}
		if b.Span.Start.UnixNano() == target.Start.UnixNano() &&
			b.Span.End.UnixNano() == target.End.UnixNano() {
			count++
		}
	}
	if count != 1 {
		t.Errorf("T-35: %d bookings landed on the target window, expected exactly 1", count)
	}

	// Rebuild the index from the source and diff — races often corrupt
	// derived state even when the source ends consistent. Reuse the
	// helper the T-34 confirm-race harness uses.
	assertIndexMatchesRebuild(t, idx, src, fmt.Sprintf("Move race trial %d", trial))
}
