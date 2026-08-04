// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package obj

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	locpkg "github.com/ha1tch/xolu/pkg/loc"

	_ "modernc.org/sqlite"
)

// testStore mirrors pkg/loc's own testStore(t) exactly, obj's own
// dedicated per-tenant file.
func testStore(t *testing.T) *Store {
	t.Helper()
	tmp, err := os.MkdirTemp("", "obj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	db, err := sql.Open("sqlite", tmp+"/obj.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewStore(db, 0)
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// testLocStore mirrors pkg/loc/store_test.go's own testStore(t), for
// the Move-to-loc_leaf tests that need a real /loc store alongside
// obj's own.
func testLocStore(t *testing.T) *locpkg.Store {
	t.Helper()
	tmp, err := os.MkdirTemp("", "obj-loc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	db, err := sql.Open("sqlite", tmp+"/loc.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := locpkg.NewStore(db, 0)
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAttach_GetRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	weight := 12000.0
	if err := s.Attach(ctx, "vehicles:47", Capacity{MaxWeightKg: &weight}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub, err := s.Get(ctx, "vehicles:47")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sub.Ref != "vehicles:47" {
		t.Errorf("Ref: got %q", sub.Ref)
	}
	if sub.Capacity.MaxWeightKg == nil || *sub.Capacity.MaxWeightKg != 12000.0 {
		t.Errorf("MaxWeightKg: got %v", sub.Capacity.MaxWeightKg)
	}
	if sub.Position.Kind != PositionKindUnassigned {
		t.Errorf("a freshly-attached subject must start unassigned, got %q", sub.Position.Kind)
	}
}

func TestAttach_Duplicate_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "vehicles:1", Capacity{}); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	err := s.Attach(ctx, "vehicles:1", Capacity{})
	var already *AlreadyAttachedError
	if err == nil {
		t.Fatal("want AlreadyAttachedError, got nil")
	}
	if !errors.As(err, &already) {
		t.Fatalf("want *AlreadyAttachedError, got %T: %v", err, err)
	}
}

func TestGet_NeverAttached_Returns404Shaped(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, err := s.Get(ctx, "vehicles:999")
	var notAttached *NotAttachedError
	if !errors.As(err, &notAttached) {
		t.Fatalf("want *NotAttachedError, got %T: %v", err, err)
	}
}

func TestDetach_WhileUnassigned_Succeeds(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.Attach(ctx, "vehicles:2", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.Detach(ctx, "vehicles:2"); err != nil {
		t.Fatalf("detach while unassigned: %v", err)
	}
	if _, err := s.Get(ctx, "vehicles:2"); err == nil {
		t.Fatal("want NotAttachedError after detach, got nil")
	}
}

func TestDetach_WhilePositioned_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	locStore := testLocStore(t)

	mustDefLeaf(t, locStore, "bay-14")
	if err := s.Attach(ctx, "vehicles:3", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.Move(ctx, "vehicles:3", MoveTarget{Kind: PositionKindLocLeaf, LocLeafID: "bay-14"}, locStore); err != nil {
		t.Fatalf("move to loc_leaf: %v", err)
	}

	err := s.Detach(ctx, "vehicles:3")
	var refused *DetachRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("want *DetachRefusedError, got %T: %v", err, err)
	}
}

// mustDefLeaf defines a minimal anchored root plus one postable leaf
// under it — everything obj's own Move-to-loc_leaf tests need from
// /loc, nothing more.
func mustDefLeaf(t *testing.T, s *locpkg.Store, leafID string) {
	t.Helper()
	ctx := context.Background()
	rootID := "root-for-" + leafID
	if _, err := s.Def(ctx, locpkg.LocationDef{
		ID: rootID, Name: "root", Postable: false,
		Placement: locpkg.Placement{Anchor: &locpkg.GeoAnchor{Lat: 0, Lon: 0}},
	}); err != nil {
		t.Fatalf("Def(root): %v", err)
	}
	if _, err := s.Def(ctx, locpkg.LocationDef{
		ID: leafID, ParentID: &rootID, Name: leafID, Postable: true,
	}); err != nil {
		t.Fatalf("Def(leaf): %v", err)
	}
}

func TestMove_ToLocLeaf_ThenResolve(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	locStore := testLocStore(t)
	mustDefLeaf(t, locStore, "bay-1")

	if err := s.Attach(ctx, "vehicles:4", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.Move(ctx, "vehicles:4", MoveTarget{Kind: PositionKindLocLeaf, LocLeafID: "bay-1"}, locStore); err != nil {
		t.Fatalf("move: %v", err)
	}

	resolved, err := s.ResolvePosition(ctx, "vehicles:4")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Kind != PositionKindLocLeaf || resolved.LocLeafID != "bay-1" {
		t.Errorf("resolved position: got %+v", resolved)
	}
	if len(resolved.Chain) != 1 || resolved.Chain[0] != "vehicles:4" {
		t.Errorf("chain: got %v", resolved.Chain)
	}

	// The move must be real, not just recorded on obj's own side —
	// confirmed independently through /loc's own read path.
	locPos, err := locStore.SubjectPosition(ctx, "vehicles:4")
	if err != nil {
		t.Fatalf("loc SubjectPosition: %v", err)
	}
	if locPos.Leaf == nil || *locPos.Leaf != "bay-1" {
		t.Errorf("loc's own assignment: want bay-1, got %v", locPos.Leaf)
	}
}

func TestMove_ToLocLeaf_CapacityRefused_PassesThroughLocError(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	locStore := testLocStore(t)
	mustDefLeaf(t, locStore, "tiny-bay")
	zero := int64(0)
	zeroPtr := &zero
	if err := locStore.Patch(ctx, "tiny-bay", locpkg.PatchParams{Ceiling: &zeroPtr}); err != nil {
		t.Fatalf("set zero ceiling: %v", err)
	}
	if err := s.Attach(ctx, "vehicles:5", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	err := s.Move(ctx, "vehicles:5", MoveTarget{Kind: PositionKindLocLeaf, LocLeafID: "tiny-bay"}, locStore)
	var capErr *locpkg.CapacityError
	if !errors.As(err, &capErr) {
		t.Fatalf("want /loc's own *CapacityError to pass through, got %T: %v", err, err)
	}
}

func TestReport_RoutesThroughLoc(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	locStore := testLocStore(t)

	// §8a's own no-op-writes-nothing rule means a report with no
	// fence to enter produces no journal record at all -- define one
	// so this test actually proves routing, not just "no error".
	if _, err := locStore.DefFence(ctx, "svc-radius", nil); err != nil {
		t.Fatalf("DefFence: %v", err)
	}
	if err := locStore.SetFenceGeometry(ctx, "svc-radius", locpkg.Circle{CenterLat: -34.9, CenterLon: -56.16, RadiusMeters: 1000}); err != nil {
		t.Fatalf("SetFenceGeometry: %v", err)
	}

	if err := s.Attach(ctx, "vehicles:6", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.Report(ctx, "vehicles:6", -34.9, -56.16, locStore); err != nil {
		t.Fatalf("report: %v", err)
	}
	locPos, err := locStore.SubjectPosition(ctx, "vehicles:6")
	if err != nil {
		t.Fatalf("loc SubjectPosition: %v", err)
	}
	if locPos.LastReportPoint == nil {
		t.Fatal("want a recorded report point in /loc's own storage, got nil")
	}
	if len(locPos.Fences) != 1 || locPos.Fences[0] != "svc-radius" {
		t.Errorf("want fence membership [svc-radius], got %v", locPos.Fences)
	}
}
