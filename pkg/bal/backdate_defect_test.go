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

// TestBackdatedTransfer_StaleCheckpoint is the executable record of
// T-51: a transfer dated at or before an existing checkpoint must not
// leave that checkpoint wrong, so BalanceAsOf (checkpoint + intervening
// buckets) cannot return a pre-backdate number while the journal holds
// the true one.
//
// Backdating is legitimate in domains bal must serve — a museum
// accessioning an artefact dated 1897 is not an anomaly.
//
// T-51's original fix (0.16.24) was lazy: mark the wrong checkpoint
// stale atomically with the backdated entry, skip stale checkpoints on
// read, recompute later. T-58 (this session) supersedes that shape:
// docs/proposals/bal-checkpoint-delta-propagation.md found the lazy
// design's recompute-from-journal repair path would silently break
// once item 16 (prefix-collapse retention) prunes old journal entries
// — a checkpoint is a fold under an associative monoid (SumInt64),
// exactly like a rollup bucket, and folds absorb a correction directly
// without needing the other terms, which is why EmitDeltas/Append
// already got this right for buckets before T-51 ever existed. T-58
// makes checkpoints do the same: eager delta-adjustment, in the same
// transaction as the entry, no staleness window, no recompute needed,
// and the fix stays correct after item 16 ships (it never reads the
// journal). The acceptance criteria below assert THAT shape now.
func TestBackdatedTransfer_StaleCheckpoint(t *testing.T) {
	s := rollupStore(t)
	ctx := context.Background()
	mustDefine(t, s, AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true,
		Policy: string(chronicle.Backdated)})
	mustDefine(t, s, AccountDef{ID: "acct", Unit: "u", Postable: true,
		Policy: string(chronicle.Backdated)})

	base := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	if err := s.Transfer(ctx, "t1", "~in", "acct", 100, "", base); err != nil {
		t.Fatal(err)
	}
	if err := s.Transfer(ctx, "t2", "~in", "acct", 50, "", base.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(ctx, "acct", base.Add(72*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Backdated: dated BEFORE the checkpoint that already exists.
	if err := s.Transfer(ctx, "t3", "~in", "acct", 7, "", base.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	probe := base.Add(96 * time.Hour)
	fast, err := s.BalanceAsOf(ctx, "acct", probe)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := s.BalanceAsOfExact(ctx, "acct", probe)
	if err != nil {
		t.Fatal(err)
	}
	if fast != exact {
		t.Errorf("as-of after backdated transfer: rollup %d != journal %d "+
			"(checkpoint went wrong and was not corrected)", fast, exact)
	}

	// T-58 acceptance criteria: the checkpoint must be CORRECT
	// immediately (not stale-then-pending) — no writer sets stale=1
	// anymore, and the checkpoint oracle finds nothing to exempt
	// because there is nothing wrong to exempt. The bucket oracle
	// stays equal (buckets were never the defect, before or after).
	var stale int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+s.checkpointsTable()+` WHERE stale = 1`,
	).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Errorf("T-58: no writer should ever set stale=1 anymore, got %d stale row(s)", stale)
	}
	div, err := s.VerifyCheckpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(div) != 0 {
		t.Errorf("VerifyCheckpoints must be clean: %+v", div)
	}
	res, err := s.RollupOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Equal {
		t.Error("bucket oracle must remain equal — buckets were never the defect")
	}
}
