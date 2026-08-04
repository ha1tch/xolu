// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package obj

import (
	"context"
	"errors"
	"testing"
)

func TestMoveToContainer_BasicContainment(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "pallets:1", Capacity{}); err != nil {
		t.Fatalf("attach pallet: %v", err)
	}
	if err := s.Attach(ctx, "vehicles:1", Capacity{}); err != nil {
		t.Fatalf("attach vehicle: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:1"); err != nil {
		t.Fatalf("MoveToContainer: %v", err)
	}
	sub, err := s.Get(ctx, "pallets:1")
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
		t.Errorf("want container's cur_count 1, got %d", container.Capacity.CurCount)
	}
}

func TestMoveToContainer_SelfContainment_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "pallets:1", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	err := s.MoveToContainer(ctx, "pallets:1", "pallets:1")
	var cycleErr *ContainmentCycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("want *ContainmentCycleError for self-containment, got %T: %v", err, err)
	}
}

func TestMoveToContainer_DirectCycle_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, ref := range []string{"a:1", "b:1"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	// b is already inside a.
	if err := s.MoveToContainer(ctx, "b:1", "a:1"); err != nil {
		t.Fatalf("first move: %v", err)
	}
	// Now try to put a inside b -- would close a 2-node cycle.
	err := s.MoveToContainer(ctx, "a:1", "b:1")
	var cycleErr *ContainmentCycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("want *ContainmentCycleError, got %T: %v", err, err)
	}
}

func TestMoveToContainer_TransitiveCycle_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, ref := range []string{"a:1", "b:1", "c:1"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	// c -> b -> a (c is inside b, b is inside a).
	if err := s.MoveToContainer(ctx, "b:1", "a:1"); err != nil {
		t.Fatalf("move b into a: %v", err)
	}
	if err := s.MoveToContainer(ctx, "c:1", "b:1"); err != nil {
		t.Fatalf("move c into b: %v", err)
	}
	// a into c would close a 3-node cycle: a -> c -> b -> a.
	err := s.MoveToContainer(ctx, "a:1", "c:1")
	var cycleErr *ContainmentCycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("want *ContainmentCycleError for the transitive case, got %T: %v", err, err)
	}
	// Original chain must be completely undamaged by the refused attempt.
	subA, err := s.Get(ctx, "a:1")
	if err != nil {
		t.Fatalf("Get a: %v", err)
	}
	if subA.Position.Kind != PositionKindUnassigned {
		t.Errorf("a must remain unassigned after the refused cycle attempt, got %+v", subA.Position)
	}
}

func TestMoveToContainer_ContainerNotAttached_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "pallets:1", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	err := s.MoveToContainer(ctx, "pallets:1", "vehicles:999")
	var notAttached *ContainerNotAttachedError
	if !errors.As(err, &notAttached) {
		t.Fatalf("want *ContainerNotAttachedError, got %T: %v", err, err)
	}
}

func TestMoveToContainer_CountCapacity_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	one := int64(1)
	if err := s.Attach(ctx, "vehicles:1", Capacity{MaxCount: &one}); err != nil {
		t.Fatalf("attach vehicle: %v", err)
	}
	if err := s.Attach(ctx, "pallets:1", Capacity{}); err != nil {
		t.Fatalf("attach pallet 1: %v", err)
	}
	if err := s.Attach(ctx, "pallets:2", Capacity{}); err != nil {
		t.Fatalf("attach pallet 2: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:1"); err != nil {
		t.Fatalf("first pallet into vehicle: %v", err)
	}
	err := s.MoveToContainer(ctx, "pallets:2", "vehicles:1")
	var capErr *CapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("want *CapacityError, got %T: %v", err, err)
	}
	if capErr.Dimension != "count" {
		t.Errorf("want dimension \"count\", got %q", capErr.Dimension)
	}
	// The refused attempt must not have touched the container's count.
	container, err := s.Get(ctx, "vehicles:1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if container.Capacity.CurCount != 1 {
		t.Errorf("count must stay at 1 after the refused second pallet, got %d", container.Capacity.CurCount)
	}
}

// TestResolvePosition_MultiHopChain proves obj-00-design.md §6's own
// worked example: a case on a pallet, on a lorry, resolving
// transitively through two containment hops before terminating at a
// loc_leaf.
func TestResolvePosition_MultiHopChain(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	locStore := testLocStore(t)
	mustDefLeaf(t, locStore, "depot-3")

	for _, ref := range []string{"cases:1", "pallets:1", "lorries:1"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	if err := s.Move(ctx, "lorries:1", MoveTarget{Kind: PositionKindLocLeaf, LocLeafID: "depot-3"}, locStore); err != nil {
		t.Fatalf("lorry to depot: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "lorries:1"); err != nil {
		t.Fatalf("pallet into lorry: %v", err)
	}
	if err := s.MoveToContainer(ctx, "cases:1", "pallets:1"); err != nil {
		t.Fatalf("case onto pallet: %v", err)
	}

	resolved, err := s.ResolvePosition(ctx, "cases:1")
	if err != nil {
		t.Fatalf("ResolvePosition: %v", err)
	}
	if resolved.Kind != PositionKindLocLeaf || resolved.LocLeafID != "depot-3" {
		t.Fatalf("want termination at depot-3, got %+v", resolved)
	}
	wantChain := []string{"cases:1", "pallets:1", "lorries:1"}
	if len(resolved.Chain) != len(wantChain) {
		t.Fatalf("chain length: want %d, got %d (%v)", len(wantChain), len(resolved.Chain), resolved.Chain)
	}
	for i, want := range wantChain {
		if resolved.Chain[i] != want {
			t.Errorf("chain[%d]: want %q, got %q", i, want, resolved.Chain[i])
		}
	}
}

func TestMoveToContainer_RelinquishesPreviousContainer(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, ref := range []string{"pallets:1", "vehicles:1", "vehicles:2"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:1"); err != nil {
		t.Fatalf("move into vehicle 1: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:2"); err != nil {
		t.Fatalf("move into vehicle 2: %v", err)
	}
	v1, err := s.Get(ctx, "vehicles:1")
	if err != nil {
		t.Fatalf("Get v1: %v", err)
	}
	if v1.Capacity.CurCount != 0 {
		t.Errorf("vehicle 1 must be relinquished (count 0), got %d", v1.Capacity.CurCount)
	}
	v2, err := s.Get(ctx, "vehicles:2")
	if err != nil {
		t.Fatalf("Get v2: %v", err)
	}
	if v2.Capacity.CurCount != 1 {
		t.Errorf("vehicle 2 must now show count 1, got %d", v2.Capacity.CurCount)
	}
	pallet, err := s.Get(ctx, "pallets:1")
	if err != nil {
		t.Fatalf("Get pallet: %v", err)
	}
	if pallet.Position.ContainedBy != "vehicles:2" {
		t.Errorf("want contained by vehicles:2, got %q", pallet.Position.ContainedBy)
	}
}
