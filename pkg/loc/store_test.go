// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	tmp, err := os.MkdirTemp("", "loc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	db, err := sql.Open("sqlite",
		tmp+"/loc.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
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

func rootAnchor() Placement {
	return Placement{Anchor: &GeoAnchor{Lat: 0, Lon: 0, Alt: 0, TrueNorth: 0}}
}

// TestRoundTrip covers Stage 1's own exit criterion: def/list/get/
// patch/delete round-trip clean.
func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	rootID := "site-mvd"
	if _, err := s.Def(ctx, LocationDef{
		ID: rootID, ParentID: nil, Name: "Montevideo Site", Postable: false,
		Placement: rootAnchor(),
	}); err != nil {
		t.Fatalf("Def(root): %v", err)
	}

	if _, err := s.Def(ctx, LocationDef{
		ID: "bin-1", ParentID: &rootID, Name: "Bin 1", Postable: true,
		Placement: Placement{OffsetX: 5, OffsetY: 5},
	}); err != nil {
		t.Fatalf("Def(child): %v", err)
	}

	all, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List: want 2 locations, got %d", len(all))
	}

	got, err := s.Get(ctx, "bin-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Bin 1" || !got.Postable {
		t.Fatalf("Get: unexpected row %+v", got)
	}
	if got.ParentKey == nil {
		t.Fatalf("Get: expected non-nil ParentKey")
	}

	newName := "Bin One"
	if err := s.Patch(ctx, "bin-1", PatchParams{Name: &newName}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	got, err = s.Get(ctx, "bin-1")
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}
	if got.Name != "Bin One" {
		t.Fatalf("Patch: name not updated, got %q", got.Name)
	}

	// Delete refuses on children without force (XOLU-LOC013).
	if err := s.Delete(ctx, rootID, false); err == nil {
		t.Fatalf("Delete(root, force=false): expected XOLU-LOC013 refusal, got nil")
	}
	// bin-1 has no children: deletes clean.
	if err := s.Delete(ctx, "bin-1", false); err != nil {
		t.Fatalf("Delete(leaf): %v", err)
	}
	if err := s.Delete(ctx, rootID, false); err != nil {
		t.Fatalf("Delete(now-empty root): %v", err)
	}
	if _, err := s.Get(ctx, rootID); err == nil {
		t.Fatalf("Get after delete: expected error, got nil")
	}
}

// TestDeleteForceCascadesEmptyOnly proves force=true removes an empty
// subtree, per loc-01-rest-api.md's own DELETE semantics.
func TestDeleteForceCascadesEmptyOnly(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "site-a"
	mustDef(t, s, LocationDef{ID: root, Name: "A", Placement: rootAnchor()})
	mustDef(t, s, LocationDef{ID: "a-1", ParentID: &root, Name: "A1"})
	mustDef(t, s, LocationDef{ID: "a-2", ParentID: &root, Name: "A2"})

	if err := s.Delete(ctx, root, false); err == nil {
		t.Fatalf("Delete(root, force=false) with children present: expected refusal")
	}
	if err := s.Delete(ctx, root, true); err != nil {
		t.Fatalf("Delete(root, force=true): %v", err)
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("force delete: expected 0 locations remaining, got %d", len(all))
	}
}

// TestRootWithoutAnchorRefused proves XOLU-LOC010.
func TestRootWithoutAnchorRefused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	_, err := s.Def(ctx, LocationDef{ID: "bad-root", ParentID: nil, Name: "No anchor", Placement: Placement{}})
	if err == nil {
		t.Fatalf("Def(root without anchor): expected XOLU-LOC010 refusal, got nil")
	}
}

// TestDefUnknownParentRefusedNotOrphaned proves the write-first
// INSERT...SELECT construction (fixed alongside the read-first
// concurrency bug it replaced) doesn't conflate "no parent wanted"
// with "parent given but not found": both resolve to NULL through
// the same subquery, so this distinguishes them explicitly and
// confirms the failed attempt leaves nothing behind (the transaction
// rolls back cleanly, not a location silently created as an
// unintended orphaned root).
func TestDefUnknownParentRefusedNotOrphaned(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	missing := "does-not-exist"
	_, err := s.Def(ctx, LocationDef{ID: "child", ParentID: &missing, Name: "child", Postable: true})
	var ule *UnknownLocationError
	if !errors.As(err, &ule) {
		t.Fatalf("Def with unknown parent_id: want *UnknownLocationError, got %v", err)
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("Def with unknown parent_id must roll back cleanly: want 0 locations, got %d", len(all))
	}
}

// TestDeleteOccupiedRefused proves XOLU-LOC012: a delete is refused,
// unconditionally (force or not), for an occupied node OR when an
// occupied node is a DESCENDANT several levels down — not just a
// direct-child check.
func TestDeleteOccupiedRefused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDef(t, s, LocationDef{ID: "mid", ParentID: &root, Name: "mid"})
	mustDefPostable(t, s, "leaf", strPtr("mid"), nil)

	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "leaf"}); err != nil {
		t.Fatal(err)
	}

	// The occupied leaf itself: refused regardless of force.
	if err := s.Delete(ctx, "leaf", false); err == nil {
		t.Fatal("expected XOLU-LOC012 refusal deleting an occupied leaf")
	}
	if err := s.Delete(ctx, "leaf", true); err == nil {
		t.Fatal("expected XOLU-LOC012 refusal deleting an occupied leaf even with force=true")
	}

	// An ANCESTOR of the occupied leaf, several levels up: also
	// refused, force or not — proving the check walks the whole
	// subtree, not just direct children.
	if err := s.Delete(ctx, root, true); err == nil {
		t.Fatal("expected XOLU-LOC012 refusal deleting an ancestor of an occupied descendant, even with force=true")
	}

	// Once the subject leaves, the delete succeeds.
	other := "elsewhere"
	mustDef(t, s, LocationDef{ID: other, Name: "root2", Placement: rootAnchor()})
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: other}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, root, true); err != nil {
		t.Fatalf("delete after subject left: %v", err)
	}
}

func strPtr(s string) *string { return &s }

func mustDef(t *testing.T, s *Store, def LocationDef) LocationKey {
	t.Helper()
	k, err := s.Def(context.Background(), def)
	if err != nil {
		t.Fatalf("Def(%q): %v", def.ID, err)
	}
	return k
}

const epsilon = 1e-9

func approxEqual(a, b float64) bool { return math.Abs(a-b) < epsilon }

// TestComposeAbsolutePosition is the placement-chain composition test
// named in loc-02-implementation.md Stage 1: hand-computed expected
// points at 1, 2, and 4 hops deep.
func TestComposeAbsolutePosition(t *testing.T) {
	ctx := context.Background()

	t.Run("1 hop", func(t *testing.T) {
		s := testStore(t)
		root := "r1"
		mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
		mustDef(t, s, LocationDef{ID: "child", ParentID: &root, Name: "child", Postable: true,
			Placement: Placement{OffsetX: 100, OffsetY: 0, OffsetZ: 0, Rotation: 0}})

		got, err := s.ComposeAbsolutePosition(ctx, "child")
		if err != nil {
			t.Fatal(err)
		}
		wantLon := 100.0 / metresPerDegreeLat // TrueNorth=0, anchor lat=0: metresPerDegreeLon == metresPerDegreeLat here
		if !approxEqual(got.Lat, 0) || !approxEqual(got.Lon, wantLon) || !approxEqual(got.Alt, 0) || !approxEqual(got.Heading, 0) {
			t.Fatalf("1-hop: got %+v, want Lat=0 Lon=%.10f Alt=0 Heading=0", got, wantLon)
		}
	})

	t.Run("2 hop", func(t *testing.T) {
		s := testStore(t)
		root := "r2"
		mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
		mid := "mid"
		mustDef(t, s, LocationDef{ID: mid, ParentID: &root, Name: "mid",
			Placement: Placement{OffsetX: 0, OffsetY: 100, OffsetZ: 0, Rotation: math.Pi / 2}})
		mustDef(t, s, LocationDef{ID: "leaf", ParentID: &mid, Name: "leaf", Postable: true,
			Placement: Placement{OffsetX: 50, OffsetY: 0, OffsetZ: 0, Rotation: 0}})

		got, err := s.ComposeAbsolutePosition(ctx, "leaf")
		if err != nil {
			t.Fatal(err)
		}
		wantLat := 150.0 / metresPerDegreeLat
		if !approxEqual(got.Lat, wantLat) || !approxEqual(got.Lon, 0) || !approxEqual(got.Alt, 0) || !approxEqual(got.Heading, math.Pi/2) {
			t.Fatalf("2-hop: got %+v, want Lat=%.10f Lon=0 Alt=0 Heading=pi/2", got, wantLat)
		}
	})

	t.Run("4 hop", func(t *testing.T) {
		s := testStore(t)
		root := "r4"
		mustDef(t, s, LocationDef{ID: root, Name: "root",
			Placement: Placement{Anchor: &GeoAnchor{Lat: 10, Lon: 20, Alt: 5, TrueNorth: 0}}})
		prev := root
		for i := 1; i <= 4; i++ {
			id := "hop" + string(rune('0'+i))
			postable := i == 4
			mustDef(t, s, LocationDef{ID: id, ParentID: &prev, Name: id, Postable: postable,
				Placement: Placement{OffsetX: 10, OffsetY: 0, OffsetZ: 1, Rotation: math.Pi / 2}})
			prev = id
		}
		got, err := s.ComposeAbsolutePosition(ctx, prev)
		if err != nil {
			t.Fatal(err)
		}
		// Four 90-degree turns of the same 10m step return to the
		// starting (Lat, Lon), 4m higher, heading a full turn (2*Pi).
		if !approxEqual(got.Lat, 10) || !approxEqual(got.Lon, 20) || !approxEqual(got.Alt, 9) || !approxEqual(got.Heading, 2*math.Pi) {
			t.Fatalf("4-hop: got %+v, want Lat=10 Lon=20 Alt=9 Heading=2*Pi", got)
		}
	})
}

// TestComposeLocalChain_InvertibleRoundTrip is the practical form of
// loc-00-design.md §10's "composing top-down and composing bottom-up-
// then-inverting agree" property: apply the same chain's hops in
// reverse via their algebraic inverse and confirm the composition
// returns exactly to the origin. This tests the transform's own
// invertibility directly — a sign or operation-order bug in
// composeLocalChain would show up here as a non-zero residual, which a
// forward-only table-driven test could miss if it happened to cancel
// out for the specific hand-picked cases above. A second,
// independently-implemented bottom-up composition (walking from the
// leaf toward the root using each node's own frame as reference,
// rather than inverting the top-down result) is real remaining work,
// not claimed as covered by this test — noted here rather than left
// implicit.
func TestComposeLocalChain_InvertibleRoundTrip(t *testing.T) {
	chain := []Placement{
		{OffsetX: 10, OffsetY: 3, OffsetZ: 1, Rotation: 0.3},
		{OffsetX: -4, OffsetY: 7, OffsetZ: -2, Rotation: 1.1},
		{OffsetX: 2, OffsetY: -6, OffsetZ: 0.5, Rotation: -0.7},
	}
	x, y, z, rot := composeLocalChain(chain)

	// Undo each hop in reverse order.
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		rot -= p.Rotation // recover the rotation THIS hop saw as its pre-hop frame
		x -= p.OffsetX*math.Cos(rot) - p.OffsetY*math.Sin(rot)
		y -= p.OffsetX*math.Sin(rot) + p.OffsetY*math.Cos(rot)
		z -= p.OffsetZ
	}
	if !approxEqual(x, 0) || !approxEqual(y, 0) || !approxEqual(z, 0) || !approxEqual(rot, 0) {
		t.Fatalf("inverse round trip did not return to origin: x=%v y=%v z=%v rot=%v", x, y, z, rot)
	}
}
