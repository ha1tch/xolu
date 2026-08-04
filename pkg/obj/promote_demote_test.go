// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package obj

import (
	"context"
	"errors"
	"testing"
)

func TestAttachAndContainInTx_ComposesCorrectly(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "vehicles:1", Capacity{}); err != nil {
		t.Fatalf("attach container: %v", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := attachAndContainInTx(ctx, tx, "cases:1", "vehicles:1", Capacity{}); err != nil {
		tx.Rollback()
		t.Fatalf("attachAndContainInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	sub, err := s.Get(ctx, "cases:1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sub.Position.Kind != PositionKindObj || sub.Position.ContainedBy != "vehicles:1" {
		t.Errorf("want contained by vehicles:1, got %+v", sub.Position)
	}
	container, err := s.Get(ctx, "vehicles:1")
	if err != nil {
		t.Fatalf("Get container: %v", err)
	}
	if container.Capacity.CurCount != 1 {
		t.Errorf("want container cur_count 1, got %d", container.Capacity.CurCount)
	}
}

func TestAttachAndContainInTx_RollsBackCleanlyOnCapacityRefusal(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	zero := int64(0)
	if err := s.Attach(ctx, "vehicles:1", Capacity{MaxCount: &zero}); err != nil {
		t.Fatalf("attach zero-capacity container: %v", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	err = attachAndContainInTx(ctx, tx, "cases:1", "vehicles:1", Capacity{})
	tx.Rollback()
	var capErr *CapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("want *CapacityError, got %T: %v", err, err)
	}

	// The rollback must mean cases:1 was never attached at all.
	if _, err := s.Get(ctx, "cases:1"); err == nil {
		t.Fatal("cases:1 must not exist after a rolled-back attachAndContainInTx")
	}
}

func TestUnassignAndDetachInTx_ClearsPositionAndRelinquishes(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "vehicles:1", Capacity{}); err != nil {
		t.Fatalf("attach container: %v", err)
	}
	if err := s.Attach(ctx, "cases:1", Capacity{}); err != nil {
		t.Fatalf("attach case: %v", err)
	}
	if err := s.MoveToContainer(ctx, "cases:1", "vehicles:1"); err != nil {
		t.Fatalf("move case into vehicle: %v", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := unassignAndDetachInTx(ctx, tx, "cases:1"); err != nil {
		tx.Rollback()
		t.Fatalf("unassignAndDetachInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if _, err := s.Get(ctx, "cases:1"); err == nil {
		t.Fatal("cases:1 must no longer exist after demote's own detach")
	}
	container, err := s.Get(ctx, "vehicles:1")
	if err != nil {
		t.Fatalf("Get container: %v", err)
	}
	if container.Capacity.CurCount != 0 {
		t.Errorf("container's count must be relinquished (0), got %d", container.Capacity.CurCount)
	}
}

func TestUnassignAndDetachInTx_RefusesWhenStillContainsSomething(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, ref := range []string{"lorries:1", "pallets:1", "cases:1"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "lorries:1"); err != nil {
		t.Fatalf("pallet into lorry: %v", err)
	}
	if err := s.MoveToContainer(ctx, "cases:1", "pallets:1"); err != nil {
		t.Fatalf("case onto pallet: %v", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Demoting the pallet while it still holds a case must be refused.
	err = unassignAndDetachInTx(ctx, tx, "pallets:1")
	tx.Rollback()
	var demoteErr *DemoteRefusedError
	if !errors.As(err, &demoteErr) {
		t.Fatalf("want *DemoteRefusedError, got %T: %v", err, err)
	}
}

func TestUnassignAndDetachInTx_NeverAttached_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	err = unassignAndDetachInTx(ctx, tx, "cases:999")
	tx.Rollback()
	var notAttached *NotAttachedError
	if !errors.As(err, &notAttached) {
		t.Fatalf("want *NotAttachedError, got %T: %v", err, err)
	}
}
