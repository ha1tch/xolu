// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// oracle_adversarial_test.go deliberately corrupts state that Move/
// Report would never produce, bypassing the guarded API entirely
// (direct SQL against the same db the Store wraps) — the only way to
// prove an oracle actually detects divergence rather than just
// agreeing with itself by construction on every input the guarded API
// happens to produce.

package loc

import (
	"context"
	"encoding/json"
	"testing"
)

// TestOracle_DetectsPhantomCapacityCount: directly UPDATE
// loc_capacity.count without a matching journal entry — a "phantom"
// occupant no move ever produced. The occupancy oracle must disagree.
func TestOracle_DetectsPhantomCapacityCount(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "bin", &root, nil)

	// Sanity: the oracle agrees before corruption.
	res, err := s.OccupancyFoldOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Equal {
		t.Fatalf("occupancy oracle disagrees BEFORE corruption — test setup itself is broken: %s", res.FirstDivergence)
	}

	// Corrupt directly: bump the count with no journal entry behind it.
	if _, err := s.db.ExecContext(ctx, `UPDATE loc_capacity SET count = 1 WHERE location_key = (SELECT location_key FROM locations WHERE location_id = 'bin')`); err != nil {
		t.Fatal(err)
	}

	res, err = s.OccupancyFoldOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Equal {
		t.Fatal("occupancy oracle failed to detect a phantom capacity count with no journal entry behind it — the oracle is not actually checking anything")
	}
}

// TestOracle_DetectsMissingAssignment: directly DELETE a
// loc_assignment row that the journal still says should exist (a move
// happened, but the live-tracked current-state row vanished without
// a corresponding journal entry recording why). The assignment oracle
// must disagree.
func TestOracle_DetectsMissingAssignment(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "bin", &root, nil)
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "bin"}); err != nil {
		t.Fatal(err)
	}

	res, err := s.AssignmentFoldOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Equal {
		t.Fatalf("assignment oracle disagrees BEFORE corruption: %s", res.FirstDivergence)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM loc_assignment WHERE subject_ref = 's1'`); err != nil {
		t.Fatal(err)
	}

	res, err = s.AssignmentFoldOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Equal {
		t.Fatal("assignment oracle failed to detect a journal-recorded move whose loc_assignment row was deleted out from under it")
	}
}

// TestOracle_DetectsPhantomFenceMembership: directly INSERT a
// loc_fence_membership row with no journal-recorded entry behind it —
// the fence-membership oracle's own version of the phantom-occupant
// case, exercising the json_each-based fold specifically (a genuinely
// different query shape from the leaf oracles, deserving its own
// direct corruption-detection proof, not assumed to inherit
// correctness from the simpler leaf case).
func TestOracle_DetectsPhantomFenceMembership(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	fk, err := s.DefFence(ctx, "yard", nil)
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.FenceMembershipFoldOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Equal {
		t.Fatalf("fence membership oracle disagrees BEFORE corruption: %s", res.FirstDivergence)
	}

	if _, err := s.db.ExecContext(ctx, `INSERT INTO loc_fence_membership (subject_ref, fence_key) VALUES ('ghost-subject', ?)`, uint32(fk)); err != nil {
		t.Fatal(err)
	}

	res, err = s.FenceMembershipFoldOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Equal {
		t.Fatal("fence membership oracle failed to detect a phantom loc_fence_membership row with no journal entries behind it")
	}
}

// TestOracle_DetectsUnbalancedFenceEntryExit: a subject with an
// entry-delta but no matching exit-delta anywhere in the journal
// should net to "still a member" — inject a journal row via a raw
// insert that claims an EXIT for a fence the subject was never
// recorded entering, netting the fold to a NEGATIVE (or zero) count
// for that pair, which the fold's own "HAVING SUM(delta) > 0" clause
// should correctly exclude — proving the fold doesn't just count rows,
// it nets deltas, checked against a case a naive row-count
// implementation would get wrong.
func TestOracle_DetectsUnbalancedFenceEntryExit(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	fk, err := s.DefFence(ctx, "yard", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A bare exit with no prior entry: net delta is -1 for this pair,
	// which must NOT appear as a current member on the derive side.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO loc_journal (subject_ref, kind, from_location_key, to_location_key, entered_fence_keys, exited_fence_keys, at)
		VALUES ('phantom-exit-subject', 'report', NULL, NULL, '[]', ?, datetime('now'))`,
		mustJSON(t, []uint32{uint32(fk)})); err != nil {
		t.Fatal(err)
	}
	derived, err := s.FenceMembershipFoldOracle().Derive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if derived != "" {
		t.Fatalf("a bare exit-with-no-entry must net to zero membership (excluded by HAVING SUM(delta)>0), got derive() = %q", derived)
	}
}

func mustJSON(t *testing.T, v []uint32) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
