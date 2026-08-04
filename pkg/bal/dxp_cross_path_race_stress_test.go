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

	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// TestOrdinaryTransfer_RespectsLiveDxpHold (dxp_adapter_test.go) proves
// the cross-path guarantee sequentially: Reserve, then Transfer, one
// after another. That proves the LOGIC is right; it says nothing about
// whether the guarantee survives an actual race window, which is
// exactly where a subtle admission bug would hide — a single
// unsynchronised read of "claimed so far" landing between another
// goroutine's read and its guarded UPDATE, for instance. This is the
// same T-34/G-13 pattern as TestBalAdmission_Race, aimed at the dxp
// boundary instead of the ordinary-path boundary alone.
//
// Dormant guard G-13 (stress build tag): single-core passes are weak
// evidence for admission races — the exercise that counts is
// multi-core (`GOMAXPROCS=<cores> go test -tags stress ./pkg/bal/
// -run TestOrdinaryTransfer_RespectsLiveDxpHold_Race -count=20 -race`).
func TestOrdinaryTransfer_RespectsLiveDxpHold_Race(t *testing.T) {
	s, _, a := testAdapter(t) // guest:1 seeded at 1000, floor 0
	ctx := context.Background()

	// Hold the FULL balance pessimistically — zero headroom must
	// remain for every ordinary Transfer racing against it.
	if _, err := a.Reserve(ctx, tenant.TenantID(0).String(),
		TransferParams{From: "guest:1", To: "~received", Amount: 1000},
		"txn-hold", "p1", futureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	const claimants = 32
	var refusals, unexpected int64
	var wg sync.WaitGroup
	for i := 0; i < claimants; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Every racer shares the package's fixed `now` (not a
			// fresh time.Now() per goroutine) — strict `at > ?`
			// backdating comparison against an identical timestamp is
			// never true, so this avoids the temporal-ordering
			// confound TestBalAdmission_Race's ceiling variant found
			// and had to isolate with a backdated policy. The
			// property under test here is admission against the live
			// claim specifically, not timestamp ordering.
			err := s.Transfer(ctx, fmt.Sprintf("race-%d", n), "guest:1", "~received", 1, "", now)
			switch err.(type) {
			case *BoundsError:
				atomic.AddInt64(&refusals, 1)
			case nil:
				t.Errorf("claimant %d: ordinary Transfer succeeded against a live pessimistic hold covering the full balance — the cross-path guarantee has a race window", n)
				atomic.AddInt64(&unexpected, 1)
			default:
				t.Errorf("claimant %d: unexpected error type %T: %v", n, err, err)
				atomic.AddInt64(&unexpected, 1)
			}
		}(i)
	}
	wg.Wait()

	if unexpected != 0 {
		t.Fatalf("%d claimant(s) got an unexpected outcome (see above)", unexpected)
	}
	if refusals != claimants {
		t.Fatalf("refusals %d != %d claimants — every ordinary Transfer must be refused while the hold is live", refusals, claimants)
	}

	// The claim must still be exactly what was reserved: no ordinary
	// Transfer partially succeeded then rolled back leaving drift.
	if v, _, err := s.Balance(ctx, "guest:1"); err != nil || v != 1000 {
		t.Fatalf("guest:1 balance: %d (err %v), want 1000 — untouched by every refused racer", v, err)
	}
}
