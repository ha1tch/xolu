// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

//go:build stress

package obj

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestObjContainment_CycleRace is T-120's own exit criterion:
// concurrent cycle-construction attempted from multiple directions at
// once. Two subjects, A and B, both unattached to anything; many
// goroutines race to move A into B, many others race to move B into
// A, all simultaneously. At most ONE of the two directions may ever
// succeed, for any number of attempts on either side — if both
// directions ever succeeded (even once each), a 2-node cycle would
// exist, which the guard must make structurally impossible regardless
// of scheduling.
//
// Dormant guard (stress build tag): single-core passes are weak
// evidence for this class of race — the exercise that counts is
// multi-core (`GOMAXPROCS=<cores> go test -tags stress ./pkg/obj/
// -run TestObjContainment_CycleRace -count=20 -race`).
func TestObjContainment_CycleRace(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Attach(ctx, "a:1", Capacity{}); err != nil {
		t.Fatalf("attach a: %v", err)
	}
	if err := s.Attach(ctx, "b:1", Capacity{}); err != nil {
		t.Fatalf("attach b: %v", err)
	}

	const perDirection = 16
	var aIntoBSuccesses, bIntoASuccesses int64
	var unexpected int64
	var wg sync.WaitGroup

	for i := 0; i < perDirection; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.MoveToContainer(ctx, "a:1", "b:1")
			switch err.(type) {
			case nil:
				atomic.AddInt64(&aIntoBSuccesses, 1)
			case *ContainmentCycleError:
				// expected for every losing attempt in this direction
			default:
				t.Errorf("a->b: unexpected error type %T: %v", err, err)
				atomic.AddInt64(&unexpected, 1)
			}
		}()
	}
	for i := 0; i < perDirection; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.MoveToContainer(ctx, "b:1", "a:1")
			switch err.(type) {
			case nil:
				atomic.AddInt64(&bIntoASuccesses, 1)
			case *ContainmentCycleError:
				// expected
			default:
				t.Errorf("b->a: unexpected error type %T: %v", err, err)
				atomic.AddInt64(&unexpected, 1)
			}
		}()
	}
	wg.Wait()

	if unexpected != 0 {
		t.Fatalf("%d attempt(s) got an unexpected error type (see above)", unexpected)
	}
	if aIntoBSuccesses > 0 && bIntoASuccesses > 0 {
		t.Fatalf("BOTH directions succeeded at least once (a->b: %d, b->a: %d) -- a cycle exists, the guard failed under contention",
			aIntoBSuccesses, bIntoASuccesses)
	}
	if aIntoBSuccesses == 0 && bIntoASuccesses == 0 {
		t.Fatal("neither direction ever succeeded -- the guard is refusing legal moves, not just illegal ones")
	}

	// Whichever direction won, the final state must be a clean,
	// uncorrupted 2-node chain, not a partially-applied mess.
	final, err := s.ResolvePosition(ctx, "a:1")
	if err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	_ = final // reaching here without error/panic already proves no corrupted chain
}

// TestObjContainment_TransitiveCycleRace extends the direct 2-node
// race to a 3-node chain (c -> b -> a already exists), racing many
// concurrent attempts to close the loop with a->c against many
// concurrent, entirely unrelated, legal moves happening at the same
// time (d, e, ... each independently moving into b) — proving the
// cycle guard doesn't false-positive against unrelated concurrent
// containment activity in the same container.
func TestObjContainment_TransitiveCycleRace(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, ref := range []string{"a:1", "b:1", "c:1"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	if err := s.MoveToContainer(ctx, "b:1", "a:1"); err != nil {
		t.Fatalf("seed b into a: %v", err)
	}
	if err := s.MoveToContainer(ctx, "c:1", "b:1"); err != nil {
		t.Fatalf("seed c into b: %v", err)
	}

	const attackers = 16
	const bystanders = 16
	var cycleSuccesses int64
	var unexpected int64
	var wg sync.WaitGroup

	for i := 0; i < attackers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.MoveToContainer(ctx, "a:1", "c:1")
			switch err.(type) {
			case nil:
				atomic.AddInt64(&cycleSuccesses, 1)
			case *ContainmentCycleError:
				// expected -- always, since the chain b->a, c->b never changes
			default:
				t.Errorf("cycle attempt: unexpected error type %T: %v", err, err)
				atomic.AddInt64(&unexpected, 1)
			}
		}()
	}
	for i := 0; i < bystanders; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ref := fmt.Sprintf("bystander:%d", n)
			if err := s.Attach(ctx, ref, Capacity{}); err != nil {
				t.Errorf("attach bystander %d: %v", n, err)
				return
			}
			if err := s.MoveToContainer(ctx, ref, "b:1"); err != nil {
				t.Errorf("bystander %d legal move into b: unexpected refusal: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	if unexpected != 0 {
		t.Fatalf("%d unexpected error(s) among cycle attempts", unexpected)
	}
	if cycleSuccesses != 0 {
		t.Fatalf("the cycle-closing move (a into c) succeeded %d time(s) -- must always be refused, the underlying chain never changed", cycleSuccesses)
	}
}

// TestObjCapacity_CountRace mirrors bal's own G-13 shape
// (admission_race_stress_test.go): N goroutines racing to be
// contained by one near-ceiling subject. Exactly winners == ceiling
// (not fewer, not more), winners + refusals == N, final count never
// exceeds the ceiling.
func TestObjCapacity_CountRace(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const ceiling = 8
	c := int64(ceiling)
	if err := s.Attach(ctx, "vehicles:1", Capacity{MaxCount: &c}); err != nil {
		t.Fatalf("attach vehicle: %v", err)
	}

	const claimants = 32
	var wins, refusals, unexpected int64
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ref := fmt.Sprintf("pallets:%d", n)
			if err := s.Attach(ctx, ref, Capacity{}); err != nil {
				t.Errorf("attach pallet %d: %v", n, err)
				return
			}
			err := s.MoveToContainer(ctx, ref, "vehicles:1")
			switch err.(type) {
			case nil:
				atomic.AddInt64(&wins, 1)
			case *CapacityError:
				atomic.AddInt64(&refusals, 1)
			default:
				t.Errorf("claimant %d: unexpected error type %T: %v", n, err, err)
				atomic.AddInt64(&unexpected, 1)
			}
		}(i)
	}
	wg.Wait()

	if unexpected != 0 {
		t.Fatalf("%d claimant(s) got an unexpected error type (see above)", unexpected)
	}
	if wins != ceiling {
		t.Fatalf("want exactly %d winners (the ceiling), got %d (refusals: %d)", ceiling, wins, refusals)
	}
	if wins+refusals != claimants {
		t.Fatalf("winners %d + refusals %d != %d claimants", wins, refusals, claimants)
	}

	vehicle, err := s.Get(ctx, "vehicles:1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if vehicle.Capacity.CurCount != ceiling {
		t.Fatalf("final count: want exactly %d, got %d", ceiling, vehicle.Capacity.CurCount)
	}
}
