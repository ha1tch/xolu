// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package obj

import (
	"context"
	"testing"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

// TestPositionFoldOracle_NonTrivialFixture is T-122's own filed exit
// criterion, verbatim: attach, several moves including at least one
// promote/demote-shaped cycle (attach-and-contain then unassign-and-
// detach — the store-level operations promote/demote's own dxp legs
// call, exercised directly here rather than through a full dxp
// dispatch, matching this item's own "package-internal check" scope),
// one retire.
func TestPositionFoldOracle_NonTrivialFixture(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	locStore := testLocStore(t)
	mustDefLeaf(t, locStore, "depot-1")

	// Attach several subjects.
	for _, ref := range []string{"lorries:1", "pallets:1", "pallets:2", "cases:1"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}

	// Several moves: lorry to a loc_leaf, pallet 1 into the lorry,
	// case 1 onto pallet 1, pallet 2 unassigned (a no-op move that
	// still journals) then into the lorry too.
	if err := s.Move(ctx, "lorries:1", MoveTarget{Kind: PositionKindLocLeaf, LocLeafID: "depot-1"}, locStore); err != nil {
		t.Fatalf("lorry to depot: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "lorries:1"); err != nil {
		t.Fatalf("pallet 1 into lorry: %v", err)
	}
	if err := s.MoveToContainer(ctx, "cases:1", "pallets:1"); err != nil {
		t.Fatalf("case onto pallet 1: %v", err)
	}
	if err := s.Move(ctx, "pallets:2", MoveTarget{Kind: PositionKindUnassigned}, locStore); err != nil {
		t.Fatalf("pallet 2 explicit unassign: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:2", "lorries:1"); err != nil {
		t.Fatalf("pallet 2 into lorry: %v", err)
	}

	// A promote/demote-shaped cycle: a new subject attached-and-
	// contained (promote's own obj leg), then unassigned-and-detached
	// (demote's own obj leg) -- the exact store operations
	// promote/demote's own dxp participant calls in Execute.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := attachAndContainInTx(ctx, tx, "cases:2", "pallets:1", Capacity{}); err != nil {
		tx.Rollback()
		t.Fatalf("attachAndContainInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := unassignAndDetachInTx(ctx, tx, "cases:2"); err != nil {
		tx.Rollback()
		t.Fatalf("unassignAndDetachInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// One retire: cases:1 (currently on pallets:1, no contents of its
	// own) retires in place -- its position row must persist unchanged.
	if err := s.Retire(ctx, "cases:1"); err != nil {
		t.Fatalf("retire cases:1: %v", err)
	}

	// The oracle itself: derive(journal) == current.
	results, err := chronicle.CheckAll(ctx, s.Oracles())
	if err != nil {
		t.Fatalf("CheckAll: %v", err)
	}
	for _, r := range results {
		if !r.Equal {
			t.Errorf("oracle %q: derive != current", r.Name)
		}
	}
	if len(results) == 0 {
		t.Fatal("want at least one oracle result")
	}

	// Confirm the retired subject's position genuinely persisted
	// (obj-00-design.md §12's own "closer to a bal closure than to
	// deletion" framing) -- not just that the fold happened to agree
	// by coincidence.
	retired, err := s.Get(ctx, "cases:1")
	if err != nil {
		t.Fatalf("Get retired subject: %v", err)
	}
	if retired.RetiredAt == nil {
		t.Error("want RetiredAt set")
	}
	if retired.Position.Kind != PositionKindObj || retired.Position.ContainedBy != "pallets:1" {
		t.Errorf("retired subject's position must persist unchanged, got %+v", retired.Position)
	}

	// Confirm the detached subject genuinely has no row at all
	// (store.go's own "bookkeeping cleanup" deletion, distinct from
	// retire).
	if _, err := s.Get(ctx, "cases:2"); err == nil {
		t.Error("want cases:2 to have no row after detach")
	}
}

// TestPositionFoldOracle_EmptyStore proves the oracle doesn't
// spuriously disagree on an empty, freshly-initialised store.
func TestPositionFoldOracle_EmptyStore(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	results, err := chronicle.CheckAll(ctx, s.Oracles())
	if err != nil {
		t.Fatalf("CheckAll: %v", err)
	}
	for _, r := range results {
		if !r.Equal {
			t.Errorf("oracle %q: want agreement on an empty store, got disagreement", r.Name)
		}
	}
}
