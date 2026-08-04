// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"testing"
	"time"
)

// pruneScenario builds a two-month history for one account, seals
// through the end of the first month, and returns the store plus the
// instant used, so each test can prune and assert against a known
// shape rather than re-deriving it.
func pruneScenario(t *testing.T) (s *Store, sealAt time.Time) {
	t.Helper()
	s = sealedStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})

	// Three June entries, two July entries.
	for i, at := range []time.Time{
		time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	} {
		if err := s.Transfer(ctx, "seed", "~in", "acct", int64(10*(i+1)), "", at); err != nil {
			t.Fatalf("seed transfer %d: %v", i, err)
		}
	}

	sealAt = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) // seals all of June
	if _, err := s.SealPeriod(ctx, sealAt); err != nil {
		t.Fatal(err)
	}
	return s, sealAt
}

func journalCount(t *testing.T, s *Store, accountID string) int {
	t.Helper()
	key, err := s.accountKeyOf(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+s.journalTable()+` WHERE account_key = ?`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneJournal_RemovesOnlySealedCoveredEntries(t *testing.T) {
	s, _ := pruneScenario(t)
	ctx := context.Background()

	if got := journalCount(t, s, "acct"); got != 5 {
		t.Fatalf("before prune: %d journal rows, want 5", got)
	}

	n, err := s.PruneJournal(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) // generous floor
	if err != nil {
		t.Fatalf("PruneJournal: %v", err)
	}
	// Every transfer touches BOTH ~in and acct; three June entries on
	// each side of the ledger is 6 rows total across the whole tenant.
	if n != 6 {
		t.Fatalf("PruneJournal removed %d rows, want 6 (three June entries x two accounts)", n)
	}
	if got := journalCount(t, s, "acct"); got != 2 {
		t.Fatalf("after prune: %d journal rows, want 2 (the two July entries)", got)
	}
}

func TestPruneJournal_RetentionFloorCanKeepMoreThanTheSealRequires(t *testing.T) {
	s, _ := pruneScenario(t)
	ctx := context.Background()

	// A retention floor before the sealed checkpoint means NOTHING is
	// safe to prune yet, even though the period is sealed -- `before`
	// only lets a caller retain MORE than the seal requires, never less.
	n, err := s.PruneJournal(ctx, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PruneJournal: %v", err)
	}
	if n != 0 {
		t.Fatalf("PruneJournal with an early retention floor removed %d rows, want 0", n)
	}
	if got := journalCount(t, s, "acct"); got != 5 {
		t.Fatalf("journal rows after a no-op prune: %d, want 5 (untouched)", got)
	}
}

func TestPruneJournal_NeverPrunesUnsealedData(t *testing.T) {
	s := sealedStore(t) // sealer attached, but SealPeriod never called
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true})
	if err := s.Transfer(ctx, "t", "~in", "acct", 10, "", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	n, err := s.PruneJournal(ctx, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)) // generous floor, nothing sealed
	if err != nil {
		t.Fatalf("PruneJournal: %v", err)
	}
	if n != 0 {
		t.Fatalf("PruneJournal on a never-sealed account removed %d rows, want 0", n)
	}
}

// TestPruneJournal_ConservationAndAllThreeOraclesSurvive is the
// property that actually matters: after pruning, the account's
// CURRENT balance must be unchanged (conservation didn't just
// disappear along with the deleted rows), and all three rebuild
// oracles -- GlobalFoldOracle, VerifyCheckpoints, ChainOracle -- must
// report clean, not just "not obviously broken." This is what
// chronicle-substrate.md §4b's "conservation survives through the
// checkpoint chain" actually requires proving, not just asserting.
func TestPruneJournal_ConservationAndAllThreeOraclesSurvive(t *testing.T) {
	s, _ := pruneScenario(t)
	ctx := context.Background()

	balanceBefore, _, err := s.Balance(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.PruneJournal(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("PruneJournal: %v", err)
	}

	balanceAfter, _, err := s.Balance(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	if balanceAfter != balanceBefore {
		t.Fatalf("balance changed by pruning: before %d, after %d", balanceBefore, balanceAfter)
	}

	if res, err := s.GlobalFoldOracle().Check(ctx); err != nil {
		t.Fatalf("GlobalFoldOracle.Check: %v", err)
	} else if !res.Equal {
		t.Fatalf("GlobalFoldOracle diverged after prune: %s", res.FirstDivergence)
	}

	if divs, err := s.VerifyCheckpoints(ctx); err != nil {
		t.Fatalf("VerifyCheckpoints: %v", err)
	} else if len(divs) != 0 {
		t.Fatalf("VerifyCheckpoints found divergence after prune: %+v", divs)
	}

	if breaks, err := s.VerifyChains(ctx); err != nil {
		t.Fatalf("VerifyChains: %v", err)
	} else if len(breaks) != 0 {
		t.Fatalf("VerifyChains found a false-positive break after prune: %+v", breaks)
	}
}

// TestVerifyChains_StillCatchesARealBreakAfterPruning is the
// counterpart to the test above: the VerifyChains relaxation must not
// have become a blindfold. A genuine arithmetic corruption on a
// RETAINED (post-prune) entry must still be caught, proving the
// relaxation is scoped to the one assertion that needed it and nothing
// broader.
func TestVerifyChains_StillCatchesARealBreakAfterPruning(t *testing.T) {
	s, _ := pruneScenario(t)
	ctx := context.Background()
	if _, err := s.PruneJournal(ctx, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	key, err := s.accountKeyOf(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt a RETAINED (July) entry's arithmetic directly.
	if _, err := s.db.Exec(
		`UPDATE `+s.journalTable()+` SET amount = amount + 1 WHERE account_key = ? AND entry_id = (
		   SELECT MIN(entry_id) FROM `+s.journalTable()+` WHERE account_key = ?)`,
		key, key); err != nil {
		t.Fatal(err)
	}

	breaks, err := s.VerifyChains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(breaks) == 0 {
		t.Fatal("VerifyChains missed a genuine arithmetic corruption on a retained entry after pruning")
	}
}
