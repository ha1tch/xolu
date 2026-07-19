// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// This file guards two properties of IndexStore.RebuildFrom:
//
//  1. Correctness: after rebuild from N live bookings, the index equals
//     the source's derived occupancy (the standard rebuild invariant).
//  2. Cost: per-booking rebuild cost stays under a ceiling that is
//     generous enough not to flake on slow CI but tight enough to catch
//     an order-of-magnitude regression.
//
// The ceiling is 100 microseconds per booking. Measured baseline on
// the reference sandbox at v0.14.10 is ~3.7 µs per booking; 100 µs is
// ~27x slack, which absorbs CI variance and shared-runner noise while
// still failing if a change makes rebuild an order of magnitude slower
// (for example by reintroducing per-booking Pebble writes rather than
// the current single-batch approach).
//
// Bookings are seeded via SQLiteBookingSource.PutBooking directly rather
// than through Lifecycle.Create. This is deliberate: we are measuring
// the rebuild path, not the create path, and PutBooking is closer to
// the "seed a realistic tenant history" shape than
// Create+Confirm+Complete would be.

// perBookingCeiling is the maximum acceptable wall-clock rebuild cost per
// live booking, averaged across the size steps below. See file-level
// comment for the derivation.
const perBookingCeiling = 100 * time.Microsecond

func TestRebuildFrom_CorrectAndBounded(t *testing.T) {
	// Size steps chosen to cover a realistic range: a small tenant, a
	// moderately-loaded one, and a heavy one. 10,000 live bookings is
	// past the point where a single tenant would be considered typical;
	// beyond that we would expect operational sharding.
	for _, sz := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("N=%d", sz), func(t *testing.T) {
			runRebuildGuard(t, sz)
		})
	}
}

func runRebuildGuard(t *testing.T, n int) {
	t.Helper()

	// Spread bookings across O(sqrt(N)) calendars to exercise the
	// per-calendar accumulation path in RebuildFrom. All in one calendar
	// would understate the map-key overhead; one per booking would
	// overstate it.
	calCount := 1
	for calCount*calCount < n {
		calCount++
	}
	calIDs := make([]string, calCount)
	for i := range calIDs {
		calIDs[i] = fmt.Sprintf("cal%d", i)
	}

	lc, idx, src := setupLifecycle(t, calIDs...)
	_ = lc

	base := ot.MustParse("2026-07-08T00:00:00Z")
	for i := 0; i < n; i++ {
		cid := calIDs[i%calCount]
		st := base.Add(time.Duration(i) * time.Hour)
		en := st.Add(time.Hour)
		if err := src.PutBooking(Booking{
			BookingID:  fmt.Sprintf("b%d", i),
			CalendarID: cid,
			State:      StateBinding,
			Span:       Span{Start: st, End: en},
			Mode:       ModeExclusive,
			Bearer:     uint64(i + 1),
			CreatedAt:  ot.Now(),
			UpdatedAt:  ot.Now(),
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// Measure rebuild.
	start := time.Now()
	if err := idx.RebuildFrom(src); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	elapsed := time.Since(start)
	perBooking := elapsed / time.Duration(n)

	// Correctness: rebuild produced an index that matches the source's
	// derived occupancy. This is the standard invariant asserted after
	// any operation that touches the index.
	assertIndexMatchesRebuild(t, idx, src, fmt.Sprintf("post-rebuild N=%d", n))

	// Cost: per-booking time under the ceiling.
	if perBooking > perBookingCeiling {
		t.Errorf("N=%d: per-booking rebuild cost %s exceeds ceiling %s (total %s)",
			n, perBooking, perBookingCeiling, elapsed)
	}

	// Log the numbers regardless — useful diagnostic on both pass and
	// fail, and makes performance drift visible in CI history.
	t.Logf("N=%d calendars=%d rebuild=%s (%s/booking, ceiling %s)",
		n, calCount, elapsed, perBooking, perBookingCeiling)
}
