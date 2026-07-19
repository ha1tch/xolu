// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// --- Seal semantics (functional) ---

// TestSealFrontierMonotone: the frontier only advances.
func TestSealFrontierMonotone(t *testing.T) {
	lc, _, _ := setupLifecycle(t, "c")
	sealer := NewSealer(lc)

	t1 := ot.MustParse("2026-07-10T00:00:00Z")
	t0 := ot.MustParse("2026-07-05T00:00:00Z")
	sealer.AdvanceTo(t1)
	if sealer.Frontier().UnixNano() != t1.UnixNano() {
		t.Fatal("frontier should be t1")
	}
	sealer.AdvanceTo(t0) // backward: no-op
	if sealer.Frontier().UnixNano() != t1.UnixNano() {
		t.Fatal("frontier must not move backward")
	}
}

// TestSealedDayRejectsMutation: a confirm whose span is wholly in a sealed day is
// rejected with SealedError; an unsealed-future confirm proceeds.
func TestSealedDayRejectsMutation(t *testing.T) {
	lc, _, _ := setupLifecycle(t, "c")
	sealer := NewSealer(lc)

	// past booking (in a day we will seal) and a future booking.
	past := bk("past", "c", StateProposed, "2026-07-05T09:00:00Z", "2026-07-05T10:00:00Z", 50)
	future := bk("future", "c", StateProposed, "2026-07-20T09:00:00Z", "2026-07-20T10:00:00Z", 51)
	if _, err := sealer.CreateSealed(past); err != nil {
		t.Fatal(err)
	}
	if _, err := sealer.CreateSealed(future); err != nil {
		t.Fatal(err)
	}

	// Seal everything up to 2026-07-10 (the past day's end 07-06 <= 07-10 -> sealed).
	sealer.AdvanceTo(ot.MustParse("2026-07-10T00:00:00Z"))

	// Confirm on the sealed past booking must be rejected.
	err := sealer.ConfirmSealed("c", "past")
	if !IsSealed(err) {
		t.Fatalf("confirm on sealed day should return SealedError, got %v", err)
	}
	// the past booking must still be proposed (untouched).
	if b, _ := lc.src.booking("c", "past"); b.State != StateProposed {
		t.Fatal("rejected confirm must leave booking state untouched")
	}

	// Confirm on the future booking proceeds.
	if err := sealer.ConfirmSealed("c", "future"); err != nil {
		t.Fatalf("confirm on unsealed future should succeed: %v", err)
	}
	if b, _ := lc.src.booking("c", "future"); b.State != StateBinding {
		t.Fatal("future confirm should have moved to binding")
	}
}

// TestSealBoundaryExact: a day whose end is exactly the frontier is sealed; the
// day the frontier falls inside is not.
func TestSealBoundaryExact(t *testing.T) {
	lc, _, _ := setupLifecycle(t, "c")
	sealer := NewSealer(lc)
	// frontier at exactly 2026-07-08T00:00:00Z = end of 07-07, start of 07-08.
	sealer.AdvanceTo(ot.MustParse("2026-07-08T00:00:00Z"))

	// a booking on 07-07 (ends 07-08T00:00) -> day_end == frontier -> sealed.
	b7 := bk("d7", "c", StateProposed, "2026-07-07T09:00:00Z", "2026-07-07T10:00:00Z", 70)
	if _, err := lc.Create(b7); err != nil {
		t.Fatal(err)
	}
	if err := sealer.ConfirmSealed("c", "d7"); !IsSealed(err) {
		t.Fatalf("07-07 booking should be sealed at frontier 07-08T00:00, got %v", err)
	}

	// a booking on 07-08 -> day_end 07-09 > frontier -> not sealed.
	b8 := bk("d8", "c", StateProposed, "2026-07-08T09:00:00Z", "2026-07-08T10:00:00Z", 71)
	if _, err := lc.Create(b8); err != nil {
		t.Fatal(err)
	}
	if err := sealer.ConfirmSealed("c", "d8"); err != nil {
		t.Fatalf("07-08 booking should NOT be sealed, got %v", err)
	}
}

// TestMoveSealedRejectsBothEnds: move is rejected if either source or destination
// is sealed.
func TestMoveSealedRejectsBothEnds(t *testing.T) {
	lc, _, _ := setupLifecycle(t, "c")
	sealer := NewSealer(lc)

	// future booking, will try to move it into the sealed past.
	fb := bk("fb", "c", StateBinding, "2026-07-20T09:00:00Z", "2026-07-20T10:00:00Z", 100)
	if _, err := lc.Create(fb); err != nil {
		t.Fatal(err)
	}
	sealer.AdvanceTo(ot.MustParse("2026-07-10T00:00:00Z"))

	// move into the sealed past -> rejected.
	into := Span{Start: ot.MustParse("2026-07-05T09:00:00Z"), End: ot.MustParse("2026-07-05T10:00:00Z")}
	if _, err := sealer.MoveSealed("c", "fb", into); !IsSealed(err) {
		t.Fatalf("move into sealed past should be rejected, got %v", err)
	}
	// the booking must be untouched.
	if b, _ := lc.src.booking("c", "fb"); b.Span.Start.UnixNano() != ot.MustParse("2026-07-20T09:00:00Z").UnixNano() {
		t.Fatal("rejected move must leave booking untouched")
	}

	// move to another future slot -> allowed.
	toFuture := Span{Start: ot.MustParse("2026-07-21T09:00:00Z"), End: ot.MustParse("2026-07-21T10:00:00Z")}
	if res, err := sealer.MoveSealed("c", "fb", toFuture); err != nil || !res.Moved {
		t.Fatalf("move to future should succeed: err=%v res=%+v", err, res)
	}
}

// --- The race/fault-injection stress (the actual Stage-7 gate) ---
//
// Concurrency correctness is not TDD-shaped: settle the invariant, then hammer
// the interleaving under -race. This runs many goroutines doing confirm/cancel/
// move on FUTURE bookings while another advances the seal frontier, then — at
// quiescence — asserts the index still equals a rebuild and no data race fired
// (the latter caught by `go test -race`).
func TestSealRaceStress(t *testing.T) {
	if testing.Short() {
		t.Skip("seal race stress skipped in -short")
	}
	rng := rand.New(rand.NewSource(70))
	base := ot.MustParse("2026-07-08T00:00:00Z")

	for trial := 0; trial < 10; trial++ {
		lc, s, src := setupLifecycle(t, "c0", "c1")
		sealer := NewSealer(lc)

		// Seed a population of bookings spread across ~40 future days, all created
		// before concurrency starts (creation is single-threaded here; the race we
		// target is seal vs confirm/cancel/move).
		type ref struct{ cid, bid string }
		var refs []ref
		for i := 0; i < 60; i++ {
			cid := fmt.Sprintf("c%d", i%2)
			dayOff := rng.Intn(40)
			st := base.Add(time.Duration(dayOff*24+rng.Intn(20)) * time.Hour)
			en := st.Add(time.Duration((rng.Intn(8) + 1)) * time.Hour)
			bid := fmt.Sprintf("b%d", i)
			st0 := StateProposed
			if rng.Intn(2) == 0 {
				st0 = StateBinding
			}
			b := Booking{BookingID: bid, CalendarID: cid, State: st0,
				Span: Span{Start: st, End: en}, Mode: ModeExclusive, Bearer: uint64(i + 100),
				CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
			if _, err := sealer.CreateSealed(b); err == nil {
				refs = append(refs, ref{cid, bid})
			}
		}

		var wg sync.WaitGroup

		// Goroutine 1: advance the seal frontier in steps across the population.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for step := 0; step <= 40; step += 2 {
				sealer.AdvanceTo(base.Add(time.Duration(step*24) * time.Hour))
				time.Sleep(time.Millisecond)
			}
		}()

		// Goroutines 2..K: mutate random bookings. Mutations on sealed days return
		// SealedError, which is expected and fine; the point is they never corrupt
		// the index or race the seal.
		const workers = 4
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(seed int) {
				defer wg.Done()
				r := rand.New(rand.NewSource(int64(seed)))
				for k := 0; k < 200; k++ {
					if len(refs) == 0 {
						return
					}
					rf := refs[r.Intn(len(refs))]
					switch r.Intn(3) {
					case 0:
						_ = sealer.ConfirmSealed(rf.cid, rf.bid)
					case 1:
						_ = sealer.CancelSealed(rf.cid, rf.bid)
					case 2:
						dayOff := r.Intn(40)
						st := base.Add(time.Duration(dayOff*24+r.Intn(20)) * time.Hour)
						en := st.Add(time.Duration(r.Intn(6)+1) * time.Hour)
						_, _ = sealer.MoveSealed(rf.cid, rf.bid, Span{Start: st, End: en})
					}
				}
			}(trial*100 + w)
		}

		wg.Wait()

		// At quiescence: the index must equal a rebuild from the (now mutated)
		// source. If any interleaving corrupted the index, this fails. (The -race
		// detector independently catches data races during the run.)
		assertIndexMatchesRebuild(t, s, src, fmt.Sprintf("seal-race trial %d", trial))
		s.Close()
	}
}

// TestSealRecoveryRebuild: after sealing and mutating, a rebuild reproduces the
// index exactly — the seal is logical (the frontier), so recovery is just the
// ordinary rebuild; a lost frontier is never a lost booking.
func TestSealRecoveryRebuild(t *testing.T) {
	lc, s, src := setupLifecycle(t, "c")
	sealer := NewSealer(lc)

	// some bookings, some confirmed, some not.
	for i := 0; i < 10; i++ {
		st := ot.MustParse("2026-07-08T00:00:00Z").Add(time.Duration(i*30) * time.Hour)
		en := st.Add(2 * time.Hour)
		b := Booking{BookingID: fmt.Sprintf("b%d", i), CalendarID: "c", State: StateProposed,
			Span: Span{Start: st, End: en}, Mode: ModeExclusive, Bearer: uint64(i + 100),
			CreatedAt: ot.Now(), UpdatedAt: ot.Now()}
		_, _ = sealer.CreateSealed(b)
		if i%2 == 0 {
			_ = sealer.ConfirmSealed("c", b.BookingID)
		}
	}
	sealer.AdvanceTo(ot.MustParse("2026-07-09T00:00:00Z"))

	// live index must already equal rebuild (incremental maintenance held).
	assertIndexMatchesRebuild(t, s, src, "seal pre-recovery")

	// simulate recovery: rebuild from SQLite into the same store.
	if err := s.RebuildFrom(src); err != nil {
		t.Fatal(err)
	}
	assertIndexMatchesRebuild(t, s, src, "seal post-recovery rebuild")
}
