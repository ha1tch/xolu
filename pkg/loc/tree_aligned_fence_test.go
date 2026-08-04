// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"errors"
	"testing"
)

// TestMove_TreeAlignedFenceAutoDerivation proves the "free tree walk"
// (loc-00-design.md §5): moving into a leaf several levels beneath a
// tree-aligned fence auto-enters it, with no caller-supplied fence
// key at all — closing the gap Stage 5's own DxpMoveParams doc
// comment named explicitly.
func TestMove_TreeAlignedFenceAutoDerivation(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	root := "site"
	mustDef(t, s, LocationDef{ID: root, Name: "site", Placement: rootAnchor()})
	yard := "site/yard"
	mustDef(t, s, LocationDef{ID: yard, ParentID: &root, Name: "yard"})
	dock := "site/yard/dock-zone"
	mustDefPostable(t, s, dock, &yard, nil)
	elsewhere := "site/office"
	mustDefPostable(t, s, elsewhere, &root, nil)

	yardCopy := yard
	if _, err := s.DefFence(ctx, "yard-fence", &yardCopy); err != nil {
		t.Fatalf("DefFence(aligned to yard): %v", err)
	}

	// Move into dock-zone, a DESCENDANT of yard, not yard itself —
	// the fence must still be entered, proving this walks ancestors,
	// not just an exact-match check.
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: dock}); err != nil {
		t.Fatalf("move into dock-zone: %v", err)
	}
	var fenceCount int
	if err := s.db.QueryRowContext(ctx, `SELECT count FROM loc_fence_capacity WHERE fence_key = (SELECT fence_key FROM fences WHERE fence_id='yard-fence')`).Scan(&fenceCount); err != nil {
		t.Fatal(err)
	}
	if fenceCount != 1 {
		t.Fatalf("yard-fence count after entering a descendant leaf: want 1, got %d", fenceCount)
	}

	// Move away to a leaf NOT under yard: must auto-exit.
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: elsewhere}); err != nil {
		t.Fatalf("move to elsewhere: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count FROM loc_fence_capacity WHERE fence_key = (SELECT fence_key FROM fences WHERE fence_id='yard-fence')`).Scan(&fenceCount); err != nil {
		t.Fatal(err)
	}
	if fenceCount != 0 {
		t.Fatalf("yard-fence count after moving away: want 0, got %d", fenceCount)
	}
}

// TestMove_TreeAlignedFenceCapacityRefused proves multi-target
// atomicity still holds through auto-derivation: a move into a leaf
// under a full tree-aligned fence is refused entirely, leaf capacity
// unaffected.
func TestMove_TreeAlignedFenceCapacityRefused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "site"
	mustDef(t, s, LocationDef{ID: root, Name: "site", Placement: rootAnchor()})
	yard := "site/yard"
	mustDef(t, s, LocationDef{ID: yard, ParentID: &root, Name: "yard"})
	dock := "site/yard/dock-zone"
	mustDefPostable(t, s, dock, &yard, nil) // unlimited leaf capacity

	yardCopy := yard
	fk, err := s.DefFence(ctx, "yard-fence", &yardCopy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE loc_fence_capacity SET ceiling = 0 WHERE fence_key = ?`, uint32(fk)); err != nil {
		t.Fatal(err)
	}

	err = s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: dock})
	var capErr *CapacityError
	if !errors.As(err, &capErr) || capErr.Kind != "fence" {
		t.Fatalf("move into a leaf under a full tree-aligned fence: want *CapacityError{Kind:fence}, got %v", err)
	}

	var leafCount int
	if err := s.db.QueryRowContext(ctx, `SELECT count FROM loc_capacity WHERE location_key=(SELECT location_key FROM locations WHERE location_id=?)`, dock).Scan(&leafCount); err != nil {
		t.Fatal(err)
	}
	if leafCount != 0 {
		t.Fatalf("leaf count after a fence-refused move: want 0 (rolled back), got %d", leafCount)
	}
}

// TestMove_ExplicitFenceKeysStillBypassAutoDerivation proves Stage 2's
// own test hook is unaffected: supplying explicit fence keys skips
// auto-derivation entirely — every earlier admission test continues
// to exercise the guard directly, not through tree alignment.
func TestMove_ExplicitFenceKeysStillBypassAutoDerivation(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "leaf", &root, nil)

	// A fence with NO tree alignment at all, capacity 0 -- if auto-
	// derivation were incorrectly running here too, this fence would
	// never even be considered (it's not aligned to "leaf" or any
	// ancestor), so explicitly supplying it must be what triggers the
	// refusal, not tree alignment.
	fk, err := s.DefFence(ctx, "standalone-full", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE loc_fence_capacity SET ceiling = 0 WHERE fence_key = ?`, uint32(fk)); err != nil {
		t.Fatal(err)
	}

	err = s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "leaf", EnteredFenceKeys: []FenceKey{fk}})
	var capErr *CapacityError
	if !errors.As(err, &capErr) || capErr.Kind != "fence" {
		t.Fatalf("explicit EnteredFenceKeys against a full standalone fence: want *CapacityError{Kind:fence}, got %v", err)
	}
}
