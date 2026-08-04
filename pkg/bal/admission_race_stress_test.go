// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

//go:build stress

package bal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBalAdmission_Race is the T-34-pattern harness the proposal
// mandates (@B06): N goroutines transferring against one near-floor
// account. The invariant: winners + refusals == N, and the final
// balance never goes below the floor. Under contention on the last
// unit, exactly one claimant wins; the rest are told, not fooled.
//
// Dormant guard G-13 (stress build tag): single-core passes are weak
// evidence for admission races — the exercise that counts is
// multi-core (`GOMAXPROCS=<cores> go test -tags stress ./pkg/bal/
// -run TestBalAdmission_Race -count=20 -race`).

// TestBalAdmission_Race_Ceiling mirrors TestBalAdmission_Race on the
// OTHER boundary the guard enforces: the existing test only ever raced
// claimants against a floor. The guarded UPDATE's credit leg checks
// `value + amount + claimed <= ceiling` — a structurally distinct
// predicate (COALESCE'd ceiling, addition instead of subtraction) that
// deserves its own adversarial proof, not an assumption that "the floor
// test passing means the ceiling side is fine too."
func TestBalAdmission_Race_Ceiling(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// hot sits one unit below its ceiling; N claimants race to be the
	// one who fills that last unit of headroom.
	mustDefine(t, s, AccountDef{ID: "source", Unit: "u", Floor: -1 << 40, Postable: true, Policy: "backdated"})
	const ceiling = 1000
	ceilingVal := int64(ceiling)
	if _, err := s.DefineAccount(ctx, AccountDef{ID: "hot", Unit: "u", Postable: true, Ceiling: &ceilingVal, Policy: "backdated"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Transfer(ctx, "seed", "source", "hot", ceiling-1, "", time.Now()); err != nil {
		t.Fatal(err)
	}

	const claimants = 32
	var wins, refusals int64
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := s.Transfer(ctx, fmt.Sprintf("claim-%d", n), "source", "hot", 1, "", time.Now())
			switch err.(type) {
			case nil:
				atomic.AddInt64(&wins, 1)
			case *BoundsError:
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
	if v, _, err := s.Balance(ctx, "hot"); err != nil || v != ceiling {
		t.Fatalf("hot account: %d (err %v), want %d — never above ceiling", v, err, ceiling)
	}
	if breaks, _ := s.VerifyChains(ctx); len(breaks) != 0 {
		t.Fatalf("chain breaks under contention: %+v", breaks)
	}
}

func TestBalAdmission_Race(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// One unit of headroom above the floor, N=32 claimants for it.
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "hot", Unit: "u", Postable: true, Policy: "backdated"})
	mustDefine(t, s, AccountDef{ID: "sink", Unit: "u", Postable: true, Policy: "backdated"})
	if err := s.Transfer(ctx, "seed", "~in", "hot", 1, "", time.Now()); err != nil {
		t.Fatal(err)
	}

	const claimants = 32
	var wins, refusals int64
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := s.Transfer(ctx, fmt.Sprintf("claim-%d", n), "hot", "sink", 1, "", time.Now())
			switch err.(type) {
			case nil:
				atomic.AddInt64(&wins, 1)
			case *BoundsError:
				atomic.AddInt64(&refusals, 1)
			default:
				t.Errorf("claimant %d: unexpected %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("exactly one claimant must win the last unit; %d won", wins)
	}
	if wins+refusals != claimants {
		t.Fatalf("winners %d + refusals %d != %d claimants", wins, refusals, claimants)
	}
	if v, _, err := s.Balance(ctx, "hot"); err != nil || v != 0 {
		t.Fatalf("hot account: %d (err %v), want 0 — never below floor", v, err)
	}
	// Conservation held throughout.
	var total int64
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(amount),0) FROM t0000_bal_journal`).Scan(&total)
	if total != 0 {
		t.Fatalf("conservation broken under contention: %d", total)
	}
	if breaks, _ := s.VerifyChains(ctx); len(breaks) != 0 {
		t.Fatalf("chain breaks under contention: %+v", breaks)
	}
}
