// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// T-29 regression guard: Openings and Check must agree on occupancy.
//
// Both functions consume the same underlying occupancy (via ReadOccupancy
// -> Occupancy.dayOn / quantaInPeriod), so agreement is the intended
// property. This test proves it empirically across randomised calendar
// states rather than relying on the shared-primitive argument.
//
// The property, stated precisely:
//
//   For any calendar state and any query window [from, to) with duration
//   d > 0, if Openings(from, to, d, objective) returns a span S, then
//   Check(S, ModeExclusive).Feasible == true.
//
// If the property is broken, a downstream caller (client Stage 5, molu
// Part 2 tools) that composes Openings -> Check -> Create will see
// "Openings said this was free, but Create rejected it as a conflict."
// That failure mode is user-hostile and this test exists to prevent it
// from returning.
//
// Also tested in reverse (weaker direction): for a random span S in
// [from, to) that Check reports feasible, Openings(from, to, |S|,
// ObjEarliest) must return at least one span. This is weaker than
// "returns an opening containing S" because ObjEarliest returns the
// EARLIEST fitting span, not necessarily one containing S — but if any
// feasible S exists, some opening must exist too.

// The seed generator produces bookings across a small calendar surface
// with modest span diversity. The point is to exercise
// occupancy-boundary alignments, not to model realistic usage; 200
// random operations per trial produces enough variety in the resulting
// bitmap to expose off-by-one and quantum-boundary bugs if they exist.

const (
	oncPropTrials     = 50  // outer trials with fresh state per iteration
	oncPropOpsPerTrial = 30  // random ops per trial to build calendar state
	oncPropQueriesPer  = 20  // Openings/Check queries per state
)

func TestOpeningsCheckAgreement_ForwardProperty(t *testing.T) {
	// Each returned Opening must pass Check with Feasible=true.
	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < oncPropTrials; trial++ {
		lc, idx, src := setupLifecycle(t, "cal-onc")
		calRec, ok := src.calendar("cal-onc")
		if !ok {
			t.Fatalf("trial %d: seed calendar missing", trial)
		}

		// Build random calendar state.
		buildRandomState(t, trial, rng, lc, "cal-onc")

		// Occupancy read once for all Openings/Check calls in this trial.
		occ, err := idx.ReadOccupancy("cal-onc")
		if err != nil {
			t.Fatalf("trial %d: ReadOccupancy: %v", trial, err)
		}

		for q := 0; q < oncPropQueriesPer; q++ {
			from, to, dur, obj := randOpeningsQuery(rng)

			openings, err := occ.Openings(from, to, dur, obj)
			if err != nil {
				// Invalid query params are a caller bug, not a
				// property violation. Skip and continue.
				continue
			}

			for _, op := range openings {
				// Take a leading sub-span of the exact duration
				// (Openings returns whole free runs; the caller
				// picks a sub-span of the requested duration).
				checkSpan := Span{Start: op.Start, End: op.Start.Add(dur)}

				res, err := idx.Check(calRec, checkSpan, ModeExclusive)
				if err != nil {
					t.Errorf(
						"trial %d q %d: Check on returned Opening errored: %v (span=%v opening=%v)",
						trial, q, err, checkSpan, op)
					continue
				}
				if !res.Feasible {
					t.Errorf(
						"trial %d q %d: Openings returned span that Check rejects. "+
							"opening=%v checkSpan=%v objective=%s",
						trial, q, op, checkSpan, obj)
				}
			}
		}
	}
}

func TestOpeningsCheckAgreement_ReverseProperty(t *testing.T) {
	// If Check(S) is feasible for a random S in [from, to), then
	// Openings(from, to, |S|, ObjEarliest) must return at least one
	// span.
	//
	// This is the weaker of the two directions. It does NOT assert
	// that the returned opening contains S — ObjEarliest returns the
	// EARLIEST fitting opening, which may precede S. The property is
	// existence, not containment.
	rng := rand.New(rand.NewSource(9001))

	for trial := 0; trial < oncPropTrials; trial++ {
		lc, idx, src := setupLifecycle(t, "cal-onc-rev")
		calRec, ok := src.calendar("cal-onc-rev")
		if !ok {
			t.Fatalf("trial %d: seed calendar missing", trial)
		}

		buildRandomState(t, trial, rng, lc, "cal-onc-rev")

		occ, err := idx.ReadOccupancy("cal-onc-rev")
		if err != nil {
			t.Fatalf("trial %d: ReadOccupancy: %v", trial, err)
		}

		for q := 0; q < oncPropQueriesPer; q++ {
			from, to, _, _ := randOpeningsQuery(rng)

			// Random candidate span inside [from, to).
			candStart, candEnd, ok := randSubspan(rng, from, to)
			if !ok {
				continue
			}
			candSpan := Span{Start: candStart, End: candEnd}
			candDur := candEnd.Sub(candStart)

			res, err := idx.Check(calRec, candSpan, ModeExclusive)
			if err != nil || !res.Feasible {
				continue
			}

			openings, err := occ.Openings(from, to, candDur, ObjEarliest)
			if err != nil {
				t.Errorf(
					"trial %d q %d: Openings errored on window where Check says feasible: %v",
					trial, q, err)
				continue
			}
			if len(openings) == 0 {
				t.Errorf(
					"trial %d q %d: Check says feasible for %v but Openings returned no fits "+
						"(window=[%v,%v) duration=%v)",
					trial, q, candSpan, from, to, candDur)
			}
		}
	}
}

// buildRandomState applies a sequence of random operations to grow a
// non-trivial calendar occupancy. Operations are Create with random
// spans, plus a small chance of Confirm on a previously-created
// booking.
func buildRandomState(t *testing.T, trial int, rng *rand.Rand, lc *Lifecycle, calID string) {
	t.Helper()
	// Base window: bookings spread over ~30 days from a fixed origin,
	// chosen so quantum boundaries and midnight crossings both occur.
	base := ot.MustParse("2027-01-01T00:00:00Z")
	created := make([]string, 0, oncPropOpsPerTrial)

	for op := 0; op < oncPropOpsPerTrial; op++ {
		switch rng.Intn(4) {
		case 0, 1, 2: // create (weighted higher — need occupancy)
			offsetMin := rng.Intn(30 * 24 * 60)
			durMin := rng.Intn(180) + 5 // 5..185 min
			start := base.Add(time.Duration(offsetMin) * time.Minute)
			end := start.Add(time.Duration(durMin) * time.Minute)
			bid := fmt.Sprintf("t%d-b%d", trial, op)
			state := StateProposed
			if rng.Intn(2) == 0 {
				state = StateBinding
			}
			_, err := lc.Create(Booking{
				BookingID:  bid,
				CalendarID: calID,
				State:      state,
				Span:       Span{Start: start, End: end},
				Bearer:     uint64(op + 1),
				Mode:       ModeExclusive,
			})
			if err == nil {
				created = append(created, bid)
			}
			// A Create conflict is a legitimate outcome — just skip.
		case 3: // confirm a previous proposed if any
			if len(created) == 0 {
				continue
			}
			bid := created[rng.Intn(len(created))]
			_ = lc.Confirm(calID, bid) // best-effort; may already be binding
		}
	}
}

func randOpeningsQuery(rng *rand.Rand) (from, to ot.Instant, dur time.Duration, obj Objective) {
	base := ot.MustParse("2027-01-01T00:00:00Z")

	// Query window: 1h .. 5 days.
	winMin := rng.Intn(30 * 24 * 60)
	winLenMin := rng.Intn(5*24*60) + 60 // 60min .. 5 days
	from = base.Add(time.Duration(winMin) * time.Minute)
	to = from.Add(time.Duration(winLenMin) * time.Minute)

	// Duration: at most the window length, at least 5 minutes.
	if winLenMin <= 5 {
		dur = 5 * time.Minute
	} else {
		dur = time.Duration(rng.Intn(winLenMin-4)+5) * time.Minute
	}

	// Objective: one of the four.
	switch rng.Intn(4) {
	case 0:
		obj = ObjEarliest
	case 1:
		obj = ObjFirstFit
	case 2:
		obj = ObjEmptiest
	case 3:
		obj = ObjLongestClr
	}
	return
}

// randSubspan chooses a random [a, b) span strictly inside [from, to)
// with a duration between 5 minutes and (window length / 2).
// Returns ok=false if the window is too small.
func randSubspan(rng *rand.Rand, from, to ot.Instant) (ot.Instant, ot.Instant, bool) {
	winMin := int(to.Sub(from) / time.Minute)
	if winMin < 10 {
		return ot.Instant{}, ot.Instant{}, false
	}
	maxDur := winMin / 2
	if maxDur < 5 {
		maxDur = 5
	}
	dur := rng.Intn(maxDur-4) + 5
	offset := rng.Intn(winMin - dur)
	a := from.Add(time.Duration(offset) * time.Minute)
	b := a.Add(time.Duration(dur) * time.Minute)
	return a, b, true
}
