// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package obj

import (
	"context"
	"errors"
	"testing"
)

func TestSetCapacity_UpdatesFields(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "vehicles:1", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	weight := 5000.0
	count := int64(10)
	if err := s.SetCapacity(ctx, "vehicles:1", Capacity{MaxWeightKg: &weight, MaxCount: &count}); err != nil {
		t.Fatalf("SetCapacity: %v", err)
	}
	sub, err := s.Get(ctx, "vehicles:1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sub.Capacity.MaxWeightKg == nil || *sub.Capacity.MaxWeightKg != 5000.0 {
		t.Errorf("MaxWeightKg: got %v", sub.Capacity.MaxWeightKg)
	}
	if sub.Capacity.MaxCount == nil || *sub.Capacity.MaxCount != 10 {
		t.Errorf("MaxCount: got %v", sub.Capacity.MaxCount)
	}
	if sub.Capacity.MaxVolumeM3 != nil {
		t.Errorf("MaxVolumeM3 should remain nil, got %v", sub.Capacity.MaxVolumeM3)
	}
}

func TestSetCapacity_AllDimensionsNil_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "vehicles:1", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	err := s.SetCapacity(ctx, "vehicles:1", Capacity{})
	var capInvalid *CapacityInvalidError
	if !errors.As(err, &capInvalid) {
		t.Fatalf("want *CapacityInvalidError, got %T: %v", err, err)
	}
}

func TestSetCapacity_NeverAttached_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	weight := 100.0
	err := s.SetCapacity(ctx, "vehicles:999", Capacity{MaxWeightKg: &weight})
	var notAttached *NotAttachedError
	if !errors.As(err, &notAttached) {
		t.Fatalf("want *NotAttachedError, got %T: %v", err, err)
	}
}

func TestDirectContents_ReturnsOnlyImmediateChildren(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, ref := range []string{"lorries:1", "pallets:1", "pallets:2", "cases:1"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "lorries:1"); err != nil {
		t.Fatalf("pallet 1 into lorry: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:2", "lorries:1"); err != nil {
		t.Fatalf("pallet 2 into lorry: %v", err)
	}
	if err := s.MoveToContainer(ctx, "cases:1", "pallets:1"); err != nil {
		t.Fatalf("case onto pallet 1: %v", err)
	}

	direct, err := s.DirectContents(ctx, "lorries:1")
	if err != nil {
		t.Fatalf("DirectContents: %v", err)
	}
	if len(direct) != 2 || direct[0] != "pallets:1" || direct[1] != "pallets:2" {
		t.Errorf("want direct contents [pallets:1 pallets:2] (not including the case, one level down), got %v", direct)
	}
}

func TestDirectContents_UnknownContainer_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, err := s.DirectContents(ctx, "lorries:999")
	var notAttached *NotAttachedError
	if !errors.As(err, &notAttached) {
		t.Fatalf("want *NotAttachedError, got %T: %v", err, err)
	}
}

func TestTransitiveContents_WalksFullClosure(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, ref := range []string{"lorries:1", "pallets:1", "pallets:2", "cases:1", "cases:2"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "lorries:1"); err != nil {
		t.Fatalf("pallet 1 into lorry: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:2", "lorries:1"); err != nil {
		t.Fatalf("pallet 2 into lorry: %v", err)
	}
	if err := s.MoveToContainer(ctx, "cases:1", "pallets:1"); err != nil {
		t.Fatalf("case 1 onto pallet 1: %v", err)
	}
	if err := s.MoveToContainer(ctx, "cases:2", "pallets:1"); err != nil {
		t.Fatalf("case 2 onto pallet 1: %v", err)
	}

	all, err := s.TransitiveContents(ctx, "lorries:1")
	if err != nil {
		t.Fatalf("TransitiveContents: %v", err)
	}
	want := map[string]bool{"pallets:1": true, "pallets:2": true, "cases:1": true, "cases:2": true}
	if len(all) != len(want) {
		t.Fatalf("want %d transitive contents, got %d: %v", len(want), len(all), all)
	}
	for _, ref := range all {
		if !want[ref] {
			t.Errorf("unexpected subject in transitive contents: %q", ref)
		}
	}
}

func TestTransitiveContents_EmptyContainer(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "lorries:1", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	all, err := s.TransitiveContents(ctx, "lorries:1")
	if err != nil {
		t.Fatalf("TransitiveContents: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("want empty transitive contents, got %v", all)
	}
}
