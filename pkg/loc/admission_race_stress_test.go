// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

//go:build stress

package loc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestLocAdmission_Race is the T-34-pattern harness Stage 2 mandates
// (loc-02-implementation.md): N goroutines moving into one near-
// ceiling leaf. The invariant: winners + refusals == N, and the final
// count never exceeds the ceiling.
//
// Dormant guard G-14 (stress build tag): single-core passes are weak
// evidence for admission races — the exercise that counts is
// multi-core (`GOMAXPROCS=<cores> go test -tags stress ./pkg/loc/
// -run TestLocAdmission_Race -count=20 -race`).
func TestLocAdmission_Race(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	const ceiling = 1000
	c := int64(ceiling)
	mustDefPostable(t, s, "hot", &root, &c)

	// Pre-fill to ceiling-1: one unit of headroom, N claimants race for it.
	for i := 0; i < ceiling-1; i++ {
		if err := s.Move(ctx, MoveParams{SubjectRef: fmt.Sprintf("seed-%d", i), ToLocationID: "hot"}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	const claimants = 32
	var wins, refusals int64
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := s.Move(ctx, MoveParams{SubjectRef: fmt.Sprintf("claim-%d", n), ToLocationID: "hot"})
			switch err.(type) {
			case nil:
				atomic.AddInt64(&wins, 1)
			case *CapacityError:
				atomic.AddInt64(&refusals, 1)
			default:
				t.Errorf("claimant %d: unexpected %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("exactly one claimant must fill the last unit of ceiling headroom; %d won", wins)
	}
	if wins+refusals != claimants {
		t.Fatalf("winners %d + refusals %d != %d claimants", wins, refusals, claimants)
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count FROM loc_capacity WHERE location_key = (SELECT location_key FROM locations WHERE location_id = 'hot')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != ceiling {
		t.Fatalf("hot leaf count: %d, want %d — never above ceiling", count, ceiling)
	}
}

// TestLocAdmission_Race_MultiTarget races concurrent moves that each
// touch a leaf AND a shared near-ceiling fence — proving multi-target
// atomicity holds under real contention, not just the single-threaded
// rollback proof in admission_test.go.
func TestLocAdmission_Race_MultiTarget(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "leaf", &root, nil) // unlimited leaf capacity

	fk, err := s.DefFence(ctx, "hot-fence", nil)
	if err != nil {
		t.Fatal(err)
	}
	const ceiling = 1000
	if _, err := s.db.ExecContext(ctx, `UPDATE loc_fence_capacity SET ceiling = ? WHERE fence_key = ?`, ceiling, uint32(fk)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < ceiling-1; i++ {
		if err := s.Move(ctx, MoveParams{SubjectRef: fmt.Sprintf("seed-%d", i), ToLocationID: "leaf", EnteredFenceKeys: []FenceKey{fk}}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	const claimants = 32
	var wins, refusals int64
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := s.Move(ctx, MoveParams{SubjectRef: fmt.Sprintf("claim-%d", n), ToLocationID: "leaf", EnteredFenceKeys: []FenceKey{fk}})
			switch err.(type) {
			case nil:
				atomic.AddInt64(&wins, 1)
			case *CapacityError:
				atomic.AddInt64(&refusals, 1)
			default:
				t.Errorf("claimant %d: unexpected %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("exactly one claimant must fill the last unit of fence-ceiling headroom; %d won", wins)
	}
	// Leaf has unlimited capacity, so every winner's leaf entry always
	// succeeds; only the fence CAS discriminates. A refused claimant's
	// leaf entry must have been rolled back too — assert the leaf
	// count equals exactly the number of winners (seeds + 1), not
	// seeds + claimants, which is what a partial-application bug would
	// produce.
	var leafCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count FROM loc_capacity WHERE location_key = (SELECT location_key FROM locations WHERE location_id = 'leaf')`).Scan(&leafCount); err != nil {
		t.Fatal(err)
	}
	if int64(leafCount) != ceiling {
		t.Fatalf("multi-target atomicity violated under contention: leaf count %d, want %d (partial application would over-count)", leafCount, ceiling)
	}
}
