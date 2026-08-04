// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// create_calendar_adversarial_test.go — the systemic-gap follow-up
// named in T-125's own closure: loc and bal both had a read-first
// dense-key-allocation race and a missing-typed-error gap for
// duplicate ids. cal's own CreateCalendar looks different at first
// read — it has an EXPLICIT pre-check with a real typed error
// (ErrCalendarExists), and its ordinal allocation is already a
// genuine atomic INSERT...ON CONFLICT...RETURNING upsert, not a
// separate read. But the explicit duplicate-existence check is
// STILL a plain SELECT as the transaction's first statement, purely
// a validation read with no atomicity guarantee of its own — the
// same structural shape as loc/bal's original bug, just serving a
// different purpose. Tested directly rather than assumed either safe
// (because of the existing pre-check) or broken (because of the
// read-first shape) — the whole point of this file.

package cal_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// TestCreateCalendar_ConcurrentSameID_ExactlyOneSucceeds races N
// concurrent CreateCalendar calls for the IDENTICAL calendar_id.
// Exactly one must actually succeed; every other attempt must return
// a real, typed error (either ErrCalendarExists from the pre-check,
// or — if the pre-check itself races — SOME error, but the critical
// assertion is that it's never left unmapped as a raw driver panic
// or a silently-corrupted duplicate row.
func TestCreateCalendar_ConcurrentSameID_ExactlyOneSucceeds(t *testing.T) {
	src, _ := openSQLiteSource(t, tenant.TenantID(1), false)

	const n = 20
	var wg sync.WaitGroup
	var succeeded, expectedErr, unexpectedErr int64
	var mu sync.Mutex
	var unexpectedSamples []string
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := src.CreateCalendar(cal.Calendar{CalendarID: "race-target"})
			switch {
			case err == nil:
				atomic.AddInt64(&succeeded, 1)
			case errors.Is(err, cal.ErrCalendarExists):
				atomic.AddInt64(&expectedErr, 1)
			default:
				// The pre-check is a plain SELECT before allocOrdinalTx's
				// own write — if it ever raced, a losing attempt would
				// reach the final INSERT and hit a raw, unmapped
				// PRIMARY KEY violation here instead of the typed
				// ErrCalendarExists the pre-check returns. This is
				// exactly the shape the fix would need to close, so it
				// is checked for directly rather than folded into a
				// generic "failed" bucket that couldn't tell the two
				// apart.
				atomic.AddInt64(&unexpectedErr, 1)
				mu.Lock()
				unexpectedSamples = append(unexpectedSamples, err.Error())
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("want exactly 1 of %d concurrent identical CreateCalendar calls to succeed, got %d", n, succeeded)
	}
	if unexpectedErr > 0 {
		t.Fatalf("%d of %d losing attempts got an UNMAPPED error instead of ErrCalendarExists — the pre-check raced and a raw constraint violation leaked through: %v", unexpectedErr, n, unexpectedSamples)
	}
	if expectedErr != n-1 {
		t.Fatalf("want %d ErrCalendarExists failures, got %d", n-1, expectedErr)
	}
}

// TestCreateCalendar_ConcurrentDistinctIDs_NoOrdinalCollision proves
// allocOrdinalTx's own atomic upsert genuinely holds under real
// concurrent load — the part of CreateCalendar this audit expected
// to already be safe, confirmed rather than assumed.
func TestCreateCalendar_ConcurrentDistinctIDs_NoOrdinalCollision(t *testing.T) {
	src, _ := openSQLiteSource(t, tenant.TenantID(1), false)

	const n = 30
	var wg sync.WaitGroup
	var errs int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("concurrent-cal-%d", i)
			if _, err := src.CreateCalendar(cal.Calendar{CalendarID: id}); err != nil {
				atomic.AddInt64(&errs, 1)
				t.Logf("CreateCalendar(%q) failed: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	if errs > 0 {
		t.Fatalf("%d of %d concurrent CreateCalendar calls for DISTINCT calendar_ids failed", errs, n)
	}

	all := src.Calendars()
	seen := map[cal.CalOrdinal]bool{}
	for _, c := range all {
		if seen[c.Ordinal] {
			t.Fatalf("ordinal %d assigned to more than one calendar — allocOrdinalTx's own upsert did not hold under concurrency", c.Ordinal)
		}
		seen[c.Ordinal] = true
	}
	if len(seen) != n {
		t.Fatalf("want %d distinct ordinals, got %d", n, len(seen))
	}
}
