// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"fmt"
	"testing"
)

// TestDef_SelfParent_Refused: a caller submitting a location whose own
// parent_id equals its own not-yet-existing location_id must be
// refused, not silently create a root (the self-lookup necessarily
// fails since the row doesn't exist yet when the parent subquery
// runs) and never a cycle.
func TestDef_SelfParent_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	selfID := "self-parent"
	_, err := s.Def(ctx, LocationDef{ID: selfID, ParentID: &selfID, Name: "self", Postable: true})
	if err == nil {
		t.Fatal("Def with parent_id equal to its own not-yet-existing location_id: expected refusal, got nil")
	}
	if _, ok := err.(*UnknownLocationError); !ok {
		t.Fatalf("want *UnknownLocationError (the self-lookup can't find itself, since it doesn't exist yet), got %T: %v", err, err)
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("self-parent Def must roll back cleanly: want 0 locations, got %d", len(all))
	}
}

// TestDef_DuplicateLocationID_Refused: location_id is UNIQUE; a
// second Def with the same id must fail cleanly (a constraint
// violation surfaced as a real Go error, not silently overwriting the
// first location or corrupting loc_capacity's own paired row).
func TestDef_DuplicateLocationID_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	mustDef(t, s, LocationDef{ID: "dup", Name: "first", Placement: rootAnchor()})
	_, err := s.Def(ctx, LocationDef{ID: "dup", Name: "second", Placement: rootAnchor()})
	if _, ok := err.(*DuplicateLocationError); !ok {
		t.Fatalf("duplicate location_id: want *DuplicateLocationError, got %T: %v", err, err)
	}

	// The FIRST definition must be completely intact — not partially
	// overwritten by the failed second attempt.
	l, err := s.Get(ctx, "dup")
	if err != nil {
		t.Fatalf("original location damaged by the failed duplicate Def: %v", err)
	}
	if l.Name != "first" {
		t.Fatalf("original location's name changed by the failed duplicate Def: want %q, got %q", "first", l.Name)
	}
	var capacityRows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_capacity WHERE location_key = ?`, uint32(l.Key)).Scan(&capacityRows); err != nil {
		t.Fatal(err)
	}
	if capacityRows != 1 {
		t.Fatalf("want exactly 1 loc_capacity row for the surviving location, got %d — the failed duplicate's own paired insert may have leaked", capacityRows)
	}
}

// TestDef_DuplicateFenceID_Refused is DefFence's own version of the
// same proof.
func TestDef_DuplicateFenceID_Refused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.DefFence(ctx, "dup-fence", nil); err != nil {
		t.Fatal(err)
	}
	_, err := s.DefFence(ctx, "dup-fence", nil)
	if _, ok := err.(*DuplicateFenceError); !ok {
		t.Fatalf("duplicate fence_id: want *DuplicateFenceError, got %T: %v", err, err)
	}
	var fenceRows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fences WHERE fence_id = 'dup-fence'`).Scan(&fenceRows); err != nil {
		t.Fatal(err)
	}
	if fenceRows != 1 {
		t.Fatalf("want exactly 1 fence row surviving the duplicate attempt, got %d", fenceRows)
	}
}

// TestDeepTree_1000Levels exercises every recursive/iterative tree
// walk this package has against a genuinely deep tree — SQLite's own
// CTE recursion (its own separate limits, not this package's
// concern) and deleteSubtree's own Go-level recursion (unlike the
// SQL-side walks, a real stack-depth question for a language runtime,
// not assumed safe just because Go grows goroutine stacks
// dynamically) — proven at a depth deliberately beyond anything a
// real facility hierarchy would need, not just deep enough to look
// thorough.
func TestDeepTree_1000Levels(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	const depth = 1000

	root := "level-0"
	mustDef(t, s, LocationDef{ID: root, Name: root, Placement: rootAnchor()})
	prev := root
	for i := 1; i < depth; i++ {
		id := fmt.Sprintf("level-%d", i)
		if _, err := s.Def(ctx, LocationDef{ID: id, ParentID: &prev, Name: id}); err != nil {
			t.Fatalf("defining level %d of %d: %v", i, depth, err)
		}
		prev = id
	}
	leaf := prev
	if _, err := s.Def(ctx, LocationDef{ID: "leaf", ParentID: &leaf, Name: "leaf", Postable: true}); err != nil {
		t.Fatalf("defining the final postable leaf: %v", err)
	}

	// ComposeAbsolutePosition: a Go-level loop up 1000 ancestors.
	pos, err := s.ComposeAbsolutePosition(ctx, "leaf")
	if err != nil {
		t.Fatalf("ComposeAbsolutePosition at depth %d: %v", depth, err)
	}
	if isNaN(pos.Lat) || isNaN(pos.Lon) {
		t.Fatalf("ComposeAbsolutePosition at depth %d produced NaN: %+v", depth, pos)
	}

	// The recursive-CTE occupied-check (Delete's own SQL-side walk)
	// against the full 1000-level chain.
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "leaf"}); err != nil {
		t.Fatalf("move into the deep leaf: %v", err)
	}
	if err := s.Delete(ctx, root, true); err == nil {
		t.Fatal("expected XOLU-LOC012 refusal deleting the root of an occupied 1000-level tree, got nil")
	}

	// Move s1 fully away so the whole chain is empty, then force-delete
	// the entire 1000-level chain in one call — deleteSubtree's own
	// Go-level recursion at real depth.
	otherRoot := "other-root"
	mustDef(t, s, LocationDef{ID: otherRoot, Name: otherRoot, Placement: rootAnchor()})
	mustDefPostable(t, s, "other-leaf", &otherRoot, nil)
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "other-leaf"}); err != nil {
		t.Fatalf("move s1 away to clear the deep chain: %v", err)
	}
	if err := s.Delete(ctx, root, true); err != nil {
		t.Fatalf("force-delete of the full 1000-level empty chain: %v", err)
	}
}

func isNaN(f float64) bool { return f != f }
