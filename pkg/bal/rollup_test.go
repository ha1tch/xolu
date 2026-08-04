// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

// corruptBucket adds delta to every bucket at (accountKey, level),
// scanned across a window wide enough to catch any real bucket a test
// fixture could produce. The Pebble-native equivalent of the old
// direct `UPDATE bal_buckets SET value = value + ? WHERE ...` fault
// injection — same intent (perturb the derived plane without touching
// the journal, to prove the oracle catches it), different storage.
func corruptBucket(t *testing.T, s *Store, accountKey int64, level int, delta int64) {
	t.Helper()
	bs := s.newBucketStore(accountKey)
	from := time.Unix(0, 0).UTC()
	to := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	var found bool
	bs.RangeLevel(level, from, to, func(k chronicle.BucketKey, v int64) bool {
		found = true
		bs.Put(k, v+delta)
		return true
	})
	if !found {
		t.Fatalf("corruptBucket: no bucket found at account=%d level=%d", accountKey, level)
	}
}

func rollupStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	rp, err := OpenRollupPebble(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rp.Close() })
	s.SetRollupPebble(rp)
	if err := s.InitRollup(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// ─── BucketStore contract conformance (@B05: bal's store must pass) ─────────

// TestBucketStore_Contract runs the exported chronicle contract against
// bal's Pebble-backed bucket store (T-62). The contract was exported at
// 0.16.6 precisely so each consumer's store proves conformance rather
// than assuming it.
func TestBucketStore_Contract(t *testing.T) {
	s := rollupStore(t)
	mustDefine(t, s, AccountDef{ID: "contract", Unit: "u", Postable: true})
	chronicle.RunBucketStoreContract(t, func() chronicle.BucketStore[int64] {
		// Fresh account per store instance keeps contract runs isolated.
		return s.newBucketStore(1)
	})
}

// ─── Delta emission and the cascade ─────────────────────────────────────────

func TestRollup_DeltasFoldThroughCascade(t *testing.T) {
	s := rollupStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	base := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC) // a Monday
	// Three transfers spread across hours and days, so the cascade must
	// carry upward through hour → day → week.
	for i, at := range []time.Time{
		base,
		base.Add(3 * time.Hour),
		base.Add(30 * time.Hour), // next day
	} {
		if err := s.Transfer(ctx, "t", "~in", "acct", int64(10*(i+1)), "", at); err != nil {
			t.Fatal(err)
		}
	}

	// The coarsest grain must hold the whole sum: 10+20+30 = 60.
	eng, err := s.engineFor(2)
	if err != nil {
		t.Fatal(err)
	}
	got := eng.FoldRange(base.Add(-time.Hour), base.Add(60*time.Hour))
	if got != 60 {
		t.Fatalf("cascade fold: got %d, want 60", got)
	}

	// And the boundary account carries the mirrored negative.
	engSrc, _ := s.engineFor(1)
	if got := engSrc.FoldRange(base.Add(-time.Hour), base.Add(60*time.Hour)); got != -60 {
		t.Fatalf("source cascade fold: got %d, want -60", got)
	}
}

// ─── Balance-as-of: fast path vs exact path must agree ──────────────────────

func TestRollup_AsOfAgreesWithJournal(t *testing.T) {
	s := rollupStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	base := time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC)
	times := []time.Time{base, base.Add(2 * time.Hour), base.Add(26 * time.Hour), base.Add(50 * time.Hour)}
	for i, at := range times {
		amt := int64(5 * (i + 1))
		if err := s.Transfer(ctx, "t", "~in", "acct", amt, "", at); err != nil {
			t.Fatal(err)
		}
	}

	// At several instants the rollup (fast) and journal (exact) must agree.
	for _, probe := range []time.Time{
		base.Add(time.Hour),
		base.Add(3 * time.Hour),
		base.Add(30 * time.Hour),
		base.Add(72 * time.Hour),
	} {
		fast, err := s.BalanceAsOf(ctx, "acct", probe)
		if err != nil {
			t.Fatal(err)
		}
		exact, err := s.BalanceAsOfExact(ctx, "acct", probe)
		if err != nil {
			t.Fatal(err)
		}
		if fast != exact {
			t.Fatalf("as-of %s: rollup %d != journal %d", probe.Format(time.RFC3339), fast, exact)
		}
	}
}

// ─── Checkpoints make as-of independent of journal length ───────────────────

func TestRollup_CheckpointAnchorsAsOf(t *testing.T) {
	s := rollupStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	base := time.Date(2026, 5, 4, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * 6 * time.Hour)
		if err := s.Transfer(ctx, "t", "~in", "acct", 100, "", at); err != nil {
			t.Fatal(err)
		}
	}

	// Close a period mid-series: the checkpoint must record the closing
	// balance at that boundary.
	boundary := base.Add(13 * time.Hour) // after 3 transfers (0h, 6h, 12h)
	if err := s.Checkpoint(ctx, "acct", boundary); err != nil {
		t.Fatal(err)
	}
	var ckpt int64
	if err := s.db.QueryRow(
		`SELECT balance FROM t0000_bal_checkpoints WHERE account_key = 2`).Scan(&ckpt); err != nil {
		t.Fatal(err)
	}
	if ckpt != 300 {
		t.Fatalf("checkpoint balance: got %d, want 300", ckpt)
	}

	// As-of after the checkpoint must still agree with the journal —
	// proving the checkpoint+tail composition doesn't double-count the
	// boundary.
	probe := base.Add(30 * time.Hour)
	fast, err := s.BalanceAsOf(ctx, "acct", probe)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := s.BalanceAsOfExact(ctx, "acct", probe)
	if err != nil {
		t.Fatal(err)
	}
	if fast != exact {
		t.Fatalf("as-of past checkpoint: rollup %d != journal %d", fast, exact)
	}
}

// ─── Cascade-level rebuild oracle ───────────────────────────────────────────

// TestRollup_OracleDetectsCascadeDivergence asserts the oracle proves the
// derived plane against the journal THROUGH the cascade: corrupting a
// coarse bucket (a lost carry, which a leaf-only check would miss) must
// be caught.
func TestRollup_OracleDetectsCascadeDivergence(t *testing.T) {
	s := rollupStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := s.Transfer(ctx, "t", "~in", "acct", 250, "", at); err != nil {
		t.Fatal(err)
	}

	res, err := s.RollupOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Equal {
		t.Fatalf("clean rollup diverged: %s", res.FirstDivergence)
	}

	// Corrupt ONE BRANCH only. Level 2 is `week`; the month/quarter/year
	// branch is untouched and still reconciles. A single-branch oracle
	// would fold the year branch, find it correct, and report clean —
	// so this asserts the oracle checks EVERY branch independently.
	corruptBucket(t, s, 2, 2, 7)
	res, _ = s.RollupOracle().Check(ctx)
	if res.Equal {
		t.Fatal("oracle missed corruption confined to the week branch")
	}

	// And the mirror case: corrupt the year branch, leave week intact.
	corruptBucket(t, s, 2, 2, -7) // undo the week corruption
	corruptBucket(t, s, 2, 5, 3)
	res, _ = s.RollupOracle().Check(ctx)
	if res.Equal {
		t.Fatal("oracle missed corruption confined to the year branch")
	}

	// Rebuild must repair it.
	if err := s.RebuildRollup(ctx, "acct"); err != nil {
		t.Fatal(err)
	}
	res, _ = s.RollupOracle().Check(ctx)
	if !res.Equal {
		t.Fatalf("rollup still diverged after rebuild: %s", res.FirstDivergence)
	}
}

// TestTransfer_DegradesGracefullyWithoutRollupPlane pins the exact
// contract Transfer's own comment claims: "derived-plane failure must
// never fail an authoritative transfer that already committed." Found
// as a real bug during T-62 (the rollup migration to Pebble), not a
// hypothetical: a Store with no rollup plane attached (SetRollupPebble
// never called — the state testStore(t) alone produces, deliberately
// distinct from rollupStore(t)) previously nil-pointer PANICKED inside
// EmitDeltas rather than returning an error rollupDegraded could catch,
// silently defeating the contract this test now pins directly rather
// than relying on some other test happening to exercise the same path
// as a side effect.
func TestTransfer_DegradesGracefullyWithoutRollupPlane(t *testing.T) {
	s := testStore(t) // deliberately NOT rollupStore(t): no rollup plane attached
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	var degradedErr error
	s.onRollupError = func(err error) { degradedErr = err }

	// The authoritative transfer must succeed regardless of the
	// missing derived plane — this is the actual property under test,
	// not just "does not panic."
	if err := s.Transfer(ctx, "t", "~in", "acct", 50, "", time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Transfer must succeed even with no rollup plane attached, got %v", err)
	}
	if v, _, err := s.Balance(ctx, "acct"); err != nil || v != 50 {
		t.Fatalf("balance: %d (err %v), want 50 — the authoritative write must have landed", v, err)
	}
	if degradedErr == nil {
		t.Fatal("expected onRollupError to be notified of the missing rollup plane, got nil — degradation must be observable, not silent")
	}

	// A second transfer must ALSO succeed — this is not a one-shot
	// tolerance, the missing plane must not poison the Store.
	if err := s.Transfer(ctx, "t2", "~in", "acct", 25, "", time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("second Transfer must also succeed, got %v", err)
	}
	if v, _, err := s.Balance(ctx, "acct"); err != nil || v != 75 {
		t.Fatalf("balance after second transfer: %d (err %v), want 75", v, err)
	}
}
