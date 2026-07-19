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

// setupLifecycle builds a store + source + lifecycle with the given calendars.
func setupLifecycle(t *testing.T, calIDs ...string) (*Lifecycle, *IndexStore, *MemBookingSource) {
	t.Helper()
	s := openTestStore(t, 1)
	src := NewMemBookingSource(false)
	for i, id := range calIDs {
		cc, err := src.CreateCalendar(Calendar{CalendarID: id, EntityRef: uint64(i + 1), DefaultState: StateBinding})
		if err != nil {
			t.Fatal(err)
		}
		s.RegisterCalendar(cc)
	}
	return NewLifecycle(src, s), s, src
}

// assertIndexMatchesRebuild verifies the live incremental index equals what a
// rebuild from the source would produce — the Stage-5 correctness gate for
// incremental maintenance. It compares against an in-memory recomputation
// (oracleIndex) rather than opening a fresh Pebble store, so it can be called
// after every operation in a long randomised sequence without exhausting disk.
func assertIndexMatchesRebuild(t *testing.T, s *IndexStore, src *MemBookingSource, ctx string) {
	t.Helper()
	live := dumpIndex(t, s)
	rebuilt := oracleIndex(t, src)
	if ok, msg := indexEqual(live, rebuilt); !ok {
		t.Fatalf("%s: incremental index != rebuild: %s", ctx, msg)
	}
}

// TestConfirmMovesPlane: confirm moves a booking's occupancy from the proposed
// plane to the binding plane, incrementally, matching a rebuild.
func TestConfirmMovesPlane(t *testing.T) {
	lc, s, src := setupLifecycle(t, "room")

	b := bk("b1", "room", StateProposed, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100)
	if _, err := lc.Create(b); err != nil {
		t.Fatal(err)
	}
	// after create: proposed plane has it, binding plane empty.
	o, _ := s.ReadOccupancy("room")
	day := PeriodDay(ot.MustParse("2026-07-08T00:00:00Z"))
	cap0, _ := o.Capacity(day)
	if cap0.State != StateIdk {
		t.Fatalf("after propose: state=%v, want idk (proposed only)", cap0.State)
	}
	assertIndexMatchesRebuild(t, s, src, "after create-proposed")

	// confirm: should move to binding.
	if err := lc.Confirm("room", "b1"); err != nil {
		t.Fatal(err)
	}
	o2, _ := s.ReadOccupancy("room")
	cap1, _ := o2.Capacity(day)
	if cap1.State != StateBusy {
		t.Fatalf("after confirm: state=%v, want busy (binding)", cap1.State)
	}
	if cap1.Counts.Proposed != 0 {
		t.Fatalf("after confirm: proposed quanta = %d, want 0 (moved off proposed plane)", cap1.Counts.Proposed)
	}
	if cap1.Counts.Binding != 36 {
		t.Fatalf("after confirm: binding quanta = %d, want 36", cap1.Counts.Binding)
	}
	assertIndexMatchesRebuild(t, s, src, "after confirm")
}

// TestCancelRemovesOccupancyUnderOverlap: the hazard Stage 3 deferred. Two
// overlapping bookings; cancelling one must NOT free the quanta the other still
// holds. Incremental removal (scoped recompute) must match rebuild.
func TestCancelRemovesOccupancyUnderOverlap(t *testing.T) {
	lc, s, src := setupLifecycle(t, "room")

	// b1: 09:00-12:00, b2: 10:00-13:00 — overlap 10:00-12:00.
	b1 := bk("b1", "room", StateBinding, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100)
	b2 := bk("b2", "room", StateBinding, "2026-07-08T10:00:00Z", "2026-07-08T13:00:00Z", 101)
	if _, err := lc.Create(b1); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.Create(b2); err != nil {
		t.Fatal(err)
	}

	// Cancel b1. The overlap region 10:00-12:00 must REMAIN busy (b2 holds it);
	// only 09:00-10:00 should free.
	if err := lc.Cancel("room", "b1"); err != nil {
		t.Fatal(err)
	}
	o, _ := s.ReadOccupancy("room")

	// 09:00-10:00 should now be free.
	freeWin := Period{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T10:00:00Z")}
	if free, _ := o.IsFree(freeWin); !free {
		t.Fatal("09:00-10:00 should be free after cancelling b1")
	}
	// 10:00-12:00 must still be busy (b2).
	busyWin := Period{Start: ot.MustParse("2026-07-08T10:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")}
	if busy, _ := o.IsBusy(busyWin); !busy {
		t.Fatal("10:00-12:00 must still be busy (b2 holds it) — shared-bit hazard!")
	}
	// 12:00-13:00 still busy (b2).
	tailWin := Period{Start: ot.MustParse("2026-07-08T12:00:00Z"), End: ot.MustParse("2026-07-08T13:00:00Z")}
	if busy, _ := o.IsBusy(tailWin); !busy {
		t.Fatal("12:00-13:00 must still be busy (b2)")
	}
	assertIndexMatchesRebuild(t, s, src, "after cancel under overlap")
}

// TestCompleteStaysOnBindingPlane: binding -> honoured does not change occupancy.
func TestCompleteStaysOnBindingPlane(t *testing.T) {
	lc, s, src := setupLifecycle(t, "room")
	b := bk("b1", "room", StateBinding, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100)
	if _, err := lc.Create(b); err != nil {
		t.Fatal(err)
	}
	before := dumpIndex(t, s)
	if err := lc.Complete("room", "b1"); err != nil {
		t.Fatal(err)
	}
	after := dumpIndex(t, s)
	if ok, _ := indexEqual(before, after); !ok {
		t.Fatal("complete (binding->honoured) should not change occupancy")
	}
	assertIndexMatchesRebuild(t, s, src, "after complete")
}

// TestIllegalTransitionRejected: complete on a proposed booking is illegal.
func TestIllegalTransitionRejected(t *testing.T) {
	lc, _, _ := setupLifecycle(t, "room")
	b := bk("b1", "room", StateProposed, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100)
	if _, err := lc.Create(b); err != nil {
		t.Fatal(err)
	}
	if err := lc.Complete("room", "b1"); err == nil {
		t.Fatal("complete on proposed booking should be illegal")
	}
	// confirm then complete is legal.
	if err := lc.Confirm("room", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := lc.Complete("room", "b1"); err != nil {
		t.Fatalf("confirm->complete should be legal: %v", err)
	}
}

// TestMoveSuccess: moving to a free destination relocates occupancy atomically.
func TestMoveSuccess(t *testing.T) {
	lc, s, src := setupLifecycle(t, "room")
	b := bk("b1", "room", StateBinding, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100)
	if _, err := lc.Create(b); err != nil {
		t.Fatal(err)
	}
	to := Span{Start: ot.MustParse("2026-07-08T14:00:00Z"), End: ot.MustParse("2026-07-08T17:00:00Z")}
	res, err := lc.Move("room", "b1", to)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved {
		t.Fatalf("move should succeed: %+v", res)
	}
	o, _ := s.ReadOccupancy("room")
	// old span free, new span busy.
	oldWin := Period{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")}
	if free, _ := o.IsFree(oldWin); !free {
		t.Fatal("old span should be free after move")
	}
	newWin := Period{Start: ot.MustParse("2026-07-08T14:00:00Z"), End: ot.MustParse("2026-07-08T17:00:00Z")}
	if busy, _ := o.IsBusy(newWin); !busy {
		t.Fatal("new span should be busy after move")
	}
	assertIndexMatchesRebuild(t, s, src, "after successful move")
}

// TestMoveConflictLeavesUntouched: moving onto another booking refuses and leaves
// the original exactly where it was.
func TestMoveConflictLeavesUntouched(t *testing.T) {
	lc, s, src := setupLifecycle(t, "room")
	b1 := bk("b1", "room", StateBinding, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100)
	b2 := bk("b2", "room", StateBinding, "2026-07-08T14:00:00Z", "2026-07-08T16:00:00Z", 101)
	if _, err := lc.Create(b1); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.Create(b2); err != nil {
		t.Fatal(err)
	}
	before := dumpIndex(t, s)

	// try to move b1 onto b2's slot.
	to := Span{Start: ot.MustParse("2026-07-08T14:30:00Z"), End: ot.MustParse("2026-07-08T15:30:00Z")}
	res, err := lc.Move("room", "b1", to)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved {
		t.Fatal("move onto an occupied slot should refuse")
	}
	if len(res.Conflicts) == 0 {
		t.Fatal("refused move should report conflicts")
	}
	if res.Conflicts[0].Reason != "exclusive-vs-exclusive overlap" {
		t.Fatalf("conflict reason = %q", res.Conflicts[0].Reason)
	}
	// index must be UNCHANGED.
	after := dumpIndex(t, s)
	if ok, msg := indexEqual(before, after); !ok {
		t.Fatalf("refused move must leave index untouched: %s", msg)
	}
	// and the record's span must be unchanged.
	bb, _ := src.booking("room", "b1")
	if bb.Span.Start.UnixNano() != ot.MustParse("2026-07-08T09:00:00Z").UnixNano() {
		t.Fatal("refused move must leave the booking's span untouched")
	}
}

// TestMoveOntoOwnSpanOverlapAllowed: a booking can move to a span that overlaps
// its OWN current position (it never conflicts with itself).
func TestMoveOntoOwnOverlap(t *testing.T) {
	lc, s, src := setupLifecycle(t, "room")
	b := bk("b1", "room", StateBinding, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100)
	if _, err := lc.Create(b); err != nil {
		t.Fatal(err)
	}
	// shift by one hour — overlaps its own current span.
	to := Span{Start: ot.MustParse("2026-07-08T10:00:00Z"), End: ot.MustParse("2026-07-08T13:00:00Z")}
	res, err := lc.Move("room", "b1", to)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved {
		t.Fatalf("move overlapping own span should succeed (no self-conflict): %+v", res)
	}
	assertIndexMatchesRebuild(t, s, src, "after self-overlapping move")
}

// TestLifecycleRandomizedMatchesRebuild is the Stage-5 acceptance gate: random
// sequences of create/confirm/cancel/complete/move applied incrementally must
// keep the index equal to a full rebuild at every step. This is where the
// shared-bit hazard would surface if incremental maintenance were wrong.
func TestLifecycleRandomizedMatchesRebuild(t *testing.T) {
	rng := rand.New(rand.NewSource(50))
	base := ot.MustParse("2026-07-08T00:00:00Z")

	for trial := 0; trial < 120; trial++ {
		lc, s, src := setupLifecycle(t, "c0", "c1")
		var ids []string

		nOps := rng.Intn(20) + 5
		bookingSeq := 0
		for op := 0; op < nOps; op++ {
			switch rng.Intn(5) {
			case 0, 1: // create
				cid := fmt.Sprintf("c%d", rng.Intn(2))
				stMin := rng.Intn(20 * 12)
				durMin := (rng.Intn(20) + 1) * 5
				st := base.Add(time.Duration(stMin*5) * time.Minute)
				en := st.Add(time.Duration(durMin) * time.Minute)
				id := fmt.Sprintf("b%d", bookingSeq)
				bookingSeq++
				state := StateBinding
				bearer := uint64(bookingSeq + 100)
				if rng.Intn(2) == 0 {
					state = StateProposed
				}
				bb := Booking{BookingID: id, CalendarID: cid, State: state,
					Span: Span{Start: st, End: en}, Mode: ModeExclusive, Bearer: bearer,
					CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
				if _, err := lc.Create(bb); err == nil {
					ids = append(ids, cid+"/"+id)
				}
			case 2: // confirm a proposed
				if len(ids) > 0 {
					pick := ids[rng.Intn(len(ids))]
					cid, bid := splitID(pick)
					if b, ok := src.booking(cid, bid); ok && b.State == StateProposed {
						_ = lc.Confirm(cid, bid)
					}
				}
			case 3: // cancel
				if len(ids) > 0 {
					pick := ids[rng.Intn(len(ids))]
					cid, bid := splitID(pick)
					if b, ok := src.booking(cid, bid); ok {
						if _, occ := b.State.occupiesPlane(); occ {
							_ = lc.Cancel(cid, bid)
						}
					}
				}
			case 4: // move
				if len(ids) > 0 {
					pick := ids[rng.Intn(len(ids))]
					cid, bid := splitID(pick)
					if b, ok := src.booking(cid, bid); ok {
						if _, occ := b.State.occupiesPlane(); occ {
							stMin := rng.Intn(20 * 12)
							durMin := (rng.Intn(20) + 1) * 5
							st := base.Add(time.Duration(stMin*5) * time.Minute)
							en := st.Add(time.Duration(durMin) * time.Minute)
							_, _ = lc.Move(cid, bid, Span{Start: st, End: en})
						}
					}
				}
			}
			// invariant must hold after EVERY operation.
			assertIndexMatchesRebuild(t, s, src, fmt.Sprintf("trial %d op %d", trial, op))
		}
		s.Close()
	}
}

func splitID(s string) (cid, bid string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
