// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

// T-31 resolution: cal fault injection at the SQL boundary.
//
// The Lifecycle mutation pattern is:
//
//   1. SQL SetState (writes the new lifecycle state to cal_bookings)
//   2. Index removeFromPlane (drops occupancy from the old plane)
//   3. Index addToPlane (adds occupancy to the new plane, if occupying)
//
// If step 1 succeeds and step 2 or 3 fails (Pebble I/O error, disk
// full, corruption), the SQL source of truth reflects the new state
// but the in-memory index does not. The design intent is that the
// scoped-recompute-from-source pattern makes this recoverable via the
// next RebuildFrom — the source is authoritative and the index is
// derived, so a rebuild reconciles.
//
// This file exercises that recovery path under injected failure. If
// the design ever changes such that SQL/index disagreement becomes
// unrecoverable, these tests catch it.
//
// The fault-injection hooks live on IndexStore itself (see store.go).
// They are exported (capital first letter) so tests in the same
// package can set them without reflection. They are NOT documented in
// package-external godoc — they are not part of cal's public API
// contract.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Sentinel error the tests inject via the hooks.
var errInjectedFault = errors.New("test: injected pebble fault")

// setupFaultInjection creates a lifecycle with an installed calendar and one
// seeded booking in the given start state. Returns the lifecycle, index, and
// source so tests can inject faults and inspect state.
func setupFaultInjection(t *testing.T, startState State) (*Lifecycle, *IndexStore, *MemBookingSource, string) {
	t.Helper()
	lc, idx, src := setupLifecycle(t, "cal1")

	start := ot.FromUnixNano(1_700_000_000_000_000_000)
	end := start.Add(time.Hour)
	bid := "b1"
	if _, err := lc.Create(Booking{
		BookingID:  bid,
		CalendarID: "cal1",
		State:      StateProposed,
		Span:       Span{Start: start, End: end},
		Bearer:     1,
		Mode:       ModeExclusive,
	}); err != nil {
		t.Fatalf("seed create: %v", err)
	}
	// If the caller wanted binding as the starting state, walk one step.
	if startState == StateBinding {
		if err := lc.Confirm("cal1", bid); err != nil {
			t.Fatalf("seed confirm: %v", err)
		}
	}
	return lc, idx, src, bid
}

// TestFaultInjection_AddToPlaneFailureLeavesSQLAhead exercises the
// Confirm path: proposed→binding involves removing from the proposed
// plane and adding to the binding plane. When addToPlane fails, SQL
// state is already binding (SetState ran) but the binding-plane
// occupancy is missing. The next rebuild reconciles.
func TestFaultInjection_AddToPlaneFailureLeavesSQLAhead(t *testing.T) {
	lc, idx, src, bid := setupFaultInjection(t, StateProposed)

	// Sanity: before the fault, the source shows StateProposed and the
	// proposed plane has occupancy.
	b, ok := src.booking("cal1", bid)
	if !ok {
		t.Fatal("seeded booking missing")
	}
	if b.State != StateProposed {
		t.Fatalf("expected StateProposed before fault, got %s", b.State)
	}

	// Install fault: addToPlane fails on the next call.
	idx.AddToPlaneFaultHook = func(b Booking) error {
		return fmt.Errorf("add fault: %w", errInjectedFault)
	}
	defer func() { idx.AddToPlaneFaultHook = nil }()

	// Attempt Confirm. The transition performs SetState → removeFromPlane
	// → addToPlane. Depending on order, either the confirm returns the
	// injected error OR (if the implementation happens to run SetState
	// last on error paths) succeeds — either way we then inspect state.
	confirmErr := lc.Confirm("cal1", bid)
	if confirmErr == nil {
		t.Fatal("expected Confirm to surface the injected fault")
	}
	if !errors.Is(confirmErr, errInjectedFault) {
		t.Errorf("expected error to wrap errInjectedFault, got %v", confirmErr)
	}

	// The SQL source may or may not reflect the new state depending on
	// where in the sequence the fault fired. What we care about is that
	// AFTER RebuildFrom, the index matches whatever the source says.
	// This is the recovery path.
	idx.AddToPlaneFaultHook = nil // clear before rebuild
	if err := idx.RebuildFrom(src); err != nil {
		t.Fatalf("rebuild after fault: %v", err)
	}
	assertIndexMatchesRebuild(t, idx, src, "post-add-fault rebuild")
}

// TestFaultInjection_RemoveFromPlaneFailureLeavesSQLAhead exercises
// the same shape from the other side: the transition tries to remove
// from a plane, that fails, and the recovery path via rebuild must
// restore consistency.
func TestFaultInjection_RemoveFromPlaneFailureLeavesSQLAhead(t *testing.T) {
	lc, idx, src, bid := setupFaultInjection(t, StateBinding)

	// Install fault: removeFromPlane fails on the next call.
	idx.RemoveFromPlaneFaultHook = func(b Booking, plane Plane) error {
		return fmt.Errorf("remove fault: %w", errInjectedFault)
	}
	defer func() { idx.RemoveFromPlaneFaultHook = nil }()

	// Attempt Cancel (binding → cancelled removes from binding plane).
	cancelErr := lc.Cancel("cal1", bid)
	if cancelErr == nil {
		t.Fatal("expected Cancel to surface the injected fault")
	}
	if !errors.Is(cancelErr, errInjectedFault) {
		t.Errorf("expected error to wrap errInjectedFault, got %v", cancelErr)
	}

	// Recovery via rebuild.
	idx.RemoveFromPlaneFaultHook = nil
	if err := idx.RebuildFrom(src); err != nil {
		t.Fatalf("rebuild after fault: %v", err)
	}
	assertIndexMatchesRebuild(t, idx, src, "post-remove-fault rebuild")
}

// TestFaultInjection_RebuildIsIdempotent verifies that RebuildFrom
// can be called multiple times on an already-consistent state without
// changing behaviour. This matters because operational recovery may
// call RebuildFrom defensively when it isn't sure whether a fault
// occurred; that call should be safe.
func TestFaultInjection_RebuildIsIdempotent(t *testing.T) {
	_, idx, src, _ := setupFaultInjection(t, StateBinding)

	// First rebuild — establishes baseline.
	if err := idx.RebuildFrom(src); err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	assertIndexMatchesRebuild(t, idx, src, "first rebuild")

	// Second rebuild — must not change anything.
	if err := idx.RebuildFrom(src); err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	assertIndexMatchesRebuild(t, idx, src, "second rebuild")

	// Third rebuild — same.
	if err := idx.RebuildFrom(src); err != nil {
		t.Fatalf("third rebuild: %v", err)
	}
	assertIndexMatchesRebuild(t, idx, src, "third rebuild")
}

// TestFaultInjection_FaultHookOnlyFiresOnce demonstrates the standard
// pattern for tests that want a fault to fire once and then get out of
// the way. Not a regression guard on cal itself — it exercises the
// hook wiring so future test authors know the pattern.
func TestFaultInjection_FaultHookOnlyFiresOnce(t *testing.T) {
	lc, idx, src, bid := setupFaultInjection(t, StateProposed)

	var fired int
	idx.AddToPlaneFaultHook = func(b Booking) error {
		fired++
		if fired == 1 {
			return errInjectedFault
		}
		return nil // subsequent calls succeed
	}
	defer func() { idx.AddToPlaneFaultHook = nil }()

	// First Confirm attempt: fault fires.
	if err := lc.Confirm("cal1", bid); err == nil {
		t.Error("expected first Confirm to fail with injected fault")
	}

	// Rebuild to reconcile whatever partial state exists.
	if err := idx.RebuildFrom(src); err != nil {
		t.Fatalf("rebuild after first fault: %v", err)
	}

	if fired != 1 {
		t.Errorf("expected hook to fire exactly once, fired %d times", fired)
	}

	// State after rebuild should be self-consistent regardless of whether
	// the original Confirm's SetState landed. That's what matters — not
	// which state we ended in.
	assertIndexMatchesRebuild(t, idx, src, "post-single-fault rebuild")
}
