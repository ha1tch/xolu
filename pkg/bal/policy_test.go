// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

func policyStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(),
		"bal_policy.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewStore(db, 0)
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	rp, err := OpenRollupPebble(t.TempDir())
	if err != nil {
		t.Fatalf("open rollup pebble: %v", err)
	}
	t.Cleanup(func() { _ = rp.Close() })
	s.SetRollupPebble(rp)
	if err := s.InitRollup(context.Background()); err != nil {
		t.Fatalf("init rollup: %v", err)
	}
	return s
}

func mustAccount(t *testing.T, s *Store, id, policy string) {
	t.Helper()
	if _, err := s.DefineAccount(context.Background(), AccountDef{
		ID: id, Unit: "u", Scale: 0, Floor: -1 << 40, Postable: true,
		Policy: policy,
	}); err != nil {
		t.Fatalf("define %s: %v", id, err)
	}
}

// Default policy: a strictly-backdated entry is refused with
// XOLU-BAL006, classified (not a generic bounds refusal), and the
// error unwraps to the chronicle sentinel.
func TestPolicy_DefaultRefusesStrictBackdate(t *testing.T) {
	s := policyStore(t)
	ctx := context.Background()
	mustAccount(t, s, "src", "")
	mustAccount(t, s, "dst", "")
	t1 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	if err := s.Transfer(ctx, "a", "src", "dst", 10, "", t1); err != nil {
		t.Fatalf("forward entry: %v", err)
	}
	err := s.Transfer(ctx, "b", "src", "dst", 10, "", t1.Add(-time.Hour))
	var be *BackdatedError
	if !errors.As(err, &be) {
		t.Fatalf("want BackdatedError, got %v", err)
	}
	if !errors.Is(err, chronicle.ErrBackdatedRefused) {
		t.Fatalf("must unwrap to the chronicle sentinel")
	}
}

// Same-instant entries are admitted under append_only — the recorded
// deviation from T-55's filed wording, asserted so it cannot silently
// narrow later.
func TestPolicy_DefaultAdmitsSameInstant(t *testing.T) {
	s := policyStore(t)
	ctx := context.Background()
	mustAccount(t, s, "src", "")
	mustAccount(t, s, "dst", "")
	t1 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	if err := s.Transfer(ctx, "a", "src", "dst", 10, "", t1); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.Transfer(ctx, "b", "src", "dst", 10, "", t1); err != nil {
		t.Fatalf("same-instant must be admitted: %v", err)
	}
}

// The SQL-folded predicate and chronicle.CheckAdmission are two
// implementations of one rule; this is the conformance assertion the
// policy documentation demands.
func TestPolicy_SQLAgreesWithCheckAdmission(t *testing.T) {
	s := policyStore(t)
	ctx := context.Background()
	mustAccount(t, s, "src", "")
	mustAccount(t, s, "dst", "")
	latest := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if err := s.Transfer(ctx, "seed", "src", "dst", 1, "", latest); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for i, at := range []time.Time{
		latest.Add(-time.Nanosecond), latest, latest.Add(time.Nanosecond),
	} {
		goVerdict := chronicle.AppendOnly.CheckAdmission(at, latest) == nil
		sqlErr := s.Transfer(ctx, "c"+string(rune('0'+i)), "src", "dst", 1, "", at)
		sqlVerdict := sqlErr == nil
		var be *BackdatedError
		if !sqlVerdict && !errors.As(sqlErr, &be) {
			t.Fatalf("at=%v: refusal was not a BackdatedError: %v", at, sqlErr)
		}
		if goVerdict != sqlVerdict {
			t.Fatalf("at=%v: CheckAdmission=%v but SQL=%v", at, goVerdict, sqlVerdict)
		}
	}
}

// The T-51 reproduction, inverted: on a backdated-policy account, a
// transfer dated before an existing checkpoint marks it stale, as-of
// falls back to journal-derived truth, and fast path equals exact path.
func TestPolicy_BackdatedInvalidatesCheckpoint_T51(t *testing.T) {
	s := policyStore(t)
	ctx := context.Background()
	mustAccount(t, s, "src", string(chronicle.Backdated))
	mustAccount(t, s, "acct", string(chronicle.Backdated))
	t1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(24 * time.Hour)
	tCkpt := t1.Add(48 * time.Hour)
	tBack := t1.Add(12 * time.Hour) // before the checkpoint
	tQuery := t1.Add(72 * time.Hour)

	if err := s.Transfer(ctx, "a", "src", "acct", 100, "", t1); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := s.Transfer(ctx, "b", "src", "acct", 50, "", t2); err != nil {
		t.Fatalf("b: %v", err)
	}
	if err := s.Checkpoint(ctx, "acct", tCkpt); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	// The backdated entry that T-51 showed corrupting as-of reads.
	if err := s.Transfer(ctx, "c", "src", "acct", 7, "", tBack); err != nil {
		t.Fatalf("backdated entry must be admitted under policy: %v", err)
	}

	// T-58: the checkpoint itself must be correct IMMEDIATELY, by
	// eager delta-adjustment — not stale-then-skipped. Check the
	// stored row directly, not just the derived as-of answer, so this
	// test cannot pass by accident via some other fallback mechanism.
	var storedBalance, storedStale int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT balance, stale FROM `+s.checkpointsTable()+` WHERE at_unix = ?`,
		tCkpt.Unix()).Scan(&storedBalance, &storedStale); err != nil {
		t.Fatalf("read checkpoint row: %v", err)
	}
	if storedBalance != 157 {
		t.Fatalf("T-58: checkpoint must be delta-adjusted immediately, want stored balance 157, got %d", storedBalance)
	}
	if storedStale != 0 {
		t.Fatalf("T-58: no writer should ever set stale=1 anymore, got stale=%d", storedStale)
	}

	fast, err := s.BalanceAsOf(ctx, "acct", tQuery)
	if err != nil {
		t.Fatalf("as-of: %v", err)
	}
	exact, err := s.BalanceAsOfExact(ctx, "acct", tQuery)
	if err != nil {
		t.Fatalf("exact: %v", err)
	}
	if fast != exact || exact != 157 {
		t.Fatalf("T-51 regression: fast=%d exact=%d want 157", fast, exact)
	}

	// The oracle prong must be silent: the checkpoint is genuinely
	// correct now (T-58), not stale-and-exempt — this is the stronger
	// claim than the old design could make.
	div, err := s.VerifyCheckpoints(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(div) != 0 {
		t.Fatalf("unexpected divergences: %+v", div)
	}

	// A redundant Checkpoint call at the same boundary is harmless
	// (T-58: it was never the repair mechanism to begin with, just
	// recomputes the same already-correct value from source).
	if err := s.Checkpoint(ctx, "acct", tCkpt); err != nil {
		t.Fatalf("redundant checkpoint: %v", err)
	}
	fast2, err := s.BalanceAsOf(ctx, "acct", tQuery)
	if err != nil || fast2 != 157 {
		t.Fatalf("post-redundant-checkpoint fast=%d err=%v want 157", fast2, err)
	}
}

// TestPolicy_DeltaAdjustment_MultiCheckpointRange proves T-58's range
// behavior directly: several checkpoints spanning a backdated entry's
// position must ALL be adjusted (every one at-or-after the entry), and
// nothing before it is touched.
func TestPolicy_DeltaAdjustment_MultiCheckpointRange(t *testing.T) {
	s := policyStore(t)
	ctx := context.Background()
	mustAccount(t, s, "src", string(chronicle.Backdated))
	mustAccount(t, s, "acct", string(chronicle.Backdated))
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := s.Transfer(ctx, "seed", "src", "acct", 1000, "", t0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Four checkpoints a month apart.
	var boundaries []time.Time
	for i := 1; i <= 4; i++ {
		b := t0.AddDate(0, i, 0)
		boundaries = append(boundaries, b)
		if err := s.Checkpoint(ctx, "acct", b); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}

	// Backdated entry lands between boundaries[0] and boundaries[1] —
	// so boundaries[0] must be untouched, and boundaries[1..3] must
	// each carry the correction.
	backAt := boundaries[0].Add(15 * 24 * time.Hour)
	if err := s.Transfer(ctx, "back", "src", "acct", 42, "", backAt); err != nil {
		t.Fatalf("backdated transfer: %v", err)
	}

	want := []int64{1000, 1042, 1042, 1042}
	for i, b := range boundaries {
		var balance int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT balance FROM `+s.checkpointsTable()+` WHERE at_unix = ?`,
			b.Unix()).Scan(&balance); err != nil {
			t.Fatalf("read checkpoint %d: %v", i, err)
		}
		if balance != want[i] {
			t.Fatalf("checkpoint %d (boundary %s): want %d, got %d", i, b, want[i], balance)
		}
	}

	div, err := s.VerifyCheckpoints(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(div) != 0 {
		t.Fatalf("unexpected divergences after multi-checkpoint backdate: %+v", div)
	}
}

// TestPolicy_DeltaAdjustment_TwoLegOppositeSigns proves the debit and
// credit legs' checkpoints — different accounts, opposite-signed
// deltas — both land correctly from the two separate UPDATE
// statements transferInTx now issues.
func TestPolicy_DeltaAdjustment_TwoLegOppositeSigns(t *testing.T) {
	s := policyStore(t)
	ctx := context.Background()
	mustAccount(t, s, "debit_side", string(chronicle.Backdated))
	mustAccount(t, s, "credit_side", string(chronicle.Backdated))
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ckptAt := t0.Add(48 * time.Hour)
	backAt := t0.Add(12 * time.Hour)

	if err := s.Transfer(ctx, "seed", "credit_side", "debit_side", 500, "", t0); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.Checkpoint(ctx, "debit_side", ckptAt); err != nil {
		t.Fatalf("checkpoint debit_side: %v", err)
	}
	if err := s.Checkpoint(ctx, "credit_side", ckptAt); err != nil {
		t.Fatalf("checkpoint credit_side: %v", err)
	}

	// Backdated transfer: debit_side -> credit_side, 30 units.
	// debit_side's checkpoint must decrease by 30; credit_side's must
	// increase by 30 — opposite signs from the two separate UPDATEs.
	if err := s.Transfer(ctx, "back", "debit_side", "credit_side", 30, "", backAt); err != nil {
		t.Fatalf("backdated transfer: %v", err)
	}

	var debitBal, creditBal int64
	debitKey, err := s.accountKeyOf(ctx, "debit_side")
	if err != nil {
		t.Fatal(err)
	}
	creditKey, err := s.accountKeyOf(ctx, "credit_side")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT balance FROM `+s.checkpointsTable()+` WHERE account_key = ? AND at_unix = ?`,
		debitKey, ckptAt.Unix()).Scan(&debitBal); err != nil {
		t.Fatalf("read debit_side checkpoint: %v", err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT balance FROM `+s.checkpointsTable()+` WHERE account_key = ? AND at_unix = ?`,
		creditKey, ckptAt.Unix()).Scan(&creditBal); err != nil {
		t.Fatalf("read credit_side checkpoint: %v", err)
	}

	// Pre-backdate: debit_side=500 (credited by seed), credit_side=-500.
	// Post-backdate: debit_side loses 30 (it's now the debit leg too),
	// credit_side gains 30.
	if debitBal != 470 {
		t.Fatalf("debit_side checkpoint: want 470, got %d", debitBal)
	}
	if creditBal != -470 {
		t.Fatalf("credit_side checkpoint: want -470, got %d", creditBal)
	}

	div, err := s.VerifyCheckpoints(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(div) != 0 {
		t.Fatalf("unexpected divergences after two-leg backdate: %+v", div)
	}
}

// The oracle prong catches what T-51's second defect shipped silently:
// a non-stale checkpoint whose frozen balance disagrees with the
// journal. Manufactured directly, since the write path now prevents
// the honest route to it.
func TestPolicy_VerifyCheckpointsCatchesDivergence(t *testing.T) {
	s := policyStore(t)
	ctx := context.Background()
	mustAccount(t, s, "src", "")
	mustAccount(t, s, "acct", "")
	t1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Transfer(ctx, "a", "src", "acct", 100, "", t1); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := s.Checkpoint(ctx, "acct", t1.Add(time.Hour)); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE `+s.checkpointsTable()+` SET balance = balance + 5`); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	div, err := s.VerifyCheckpoints(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(div) != 1 {
		t.Fatalf("want exactly 1 divergence (only acct has a checkpoint), got %+v", div)
	}
	if div[0].Stored == div[0].Journal {
		t.Fatalf("divergence must show the disagreement: %+v", div[0])
	}
}

// Widening is legal, narrowing is not — the transition matrix.
func TestPolicy_Transitions(t *testing.T) {
	if !chronicle.CanTransition(chronicle.AppendOnly, chronicle.Backdated) {
		t.Fatal("widening must be legal")
	}
	if chronicle.CanTransition(chronicle.Backdated, chronicle.AppendOnly) {
		t.Fatal("narrowing must be refused in v1")
	}
}
