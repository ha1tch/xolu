// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func mustDefPostable(t *testing.T, s *Store, id string, parent *string, ceiling *int64) LocationKey {
	t.Helper()
	k := mustDef(t, s, LocationDef{ID: id, ParentID: parent, Name: id, Postable: true, Placement: Placement{}})
	if ceiling != nil {
		c := ceiling
		if err := s.Patch(context.Background(), id, PatchParams{Ceiling: &c}); err != nil {
			t.Fatalf("Patch(ceiling) on %q: %v", id, err)
		}
	}
	return k
}

// TestLeafCapacityCAS proves the leaf entry/exit predicate directly:
// admits up to ceiling, refuses the one that would exceed it, and a
// subsequent exit frees the slot for a new entry.
func TestLeafCapacityCAS(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	ceiling := int64(2)
	mustDefPostable(t, s, "bin", &root, &ceiling)

	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "bin"}); err != nil {
		t.Fatalf("move 1: %v", err)
	}
	if err := s.Move(ctx, MoveParams{SubjectRef: "s2", ToLocationID: "bin"}); err != nil {
		t.Fatalf("move 2: %v", err)
	}
	err := s.Move(ctx, MoveParams{SubjectRef: "s3", ToLocationID: "bin"})
	var capErr *CapacityError
	if !errors.As(err, &capErr) || capErr.Kind != "leaf" {
		t.Fatalf("move 3 (over ceiling): want *CapacityError{Kind:leaf}, got %v", err)
	}

	// s1 leaves; now there's room for s3.
	other := "elsewhere"
	mustDefPostable(t, s, other, &root, nil)
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: other}); err != nil {
		t.Fatalf("move s1 away: %v", err)
	}
	if err := s.Move(ctx, MoveParams{SubjectRef: "s3", ToLocationID: "bin"}); err != nil {
		t.Fatalf("move 3 after s1 left: %v", err)
	}
}

// TestFenceCapacityCAS proves the fence entry/exit predicate in
// isolation, the same shape as the leaf test but through
// EnteredFenceKeys/ExitedFenceKeys — Stage 2's test hook standing in
// for real geometry (Stage 3).
func TestFenceCapacityCAS(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "leaf", &root, nil)

	fk, err := s.DefFence(ctx, "yard", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Directly set the fence's ceiling via a second DefFence-adjacent
	// path isn't exposed yet (Stage 6 wires PATCH for fences) — set it
	// straight against loc_fence_capacity for this test's own purposes.
	if _, err := s.db.ExecContext(ctx, `UPDATE loc_fence_capacity SET ceiling = 1 WHERE fence_key = ?`, uint32(fk)); err != nil {
		t.Fatal(err)
	}

	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "leaf", EnteredFenceKeys: []FenceKey{fk}}); err != nil {
		t.Fatalf("move into fence at capacity 1: %v", err)
	}
	err = s.Move(ctx, MoveParams{SubjectRef: "s2", ToLocationID: "leaf", EnteredFenceKeys: []FenceKey{fk}})
	var capErr *CapacityError
	if !errors.As(err, &capErr) || capErr.Kind != "fence" {
		t.Fatalf("second entry into full fence: want *CapacityError{Kind:fence}, got %v", err)
	}
}

// TestMultiTargetAtomicity proves the rule Stage 2 exists to prove: a
// move whose leaf CAS succeeds but whose fence CAS fails must leave
// the leaf's count unchanged after rollback — never a partial
// application.
func TestMultiTargetAtomicity(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "leaf", &root, nil) // unlimited leaf capacity

	fullFence, err := s.DefFence(ctx, "full-fence", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE loc_fence_capacity SET ceiling = 0 WHERE fence_key = ?`, uint32(fullFence)); err != nil {
		t.Fatal(err)
	}

	err = s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "leaf", EnteredFenceKeys: []FenceKey{fullFence}})
	var capErr *CapacityError
	if !errors.As(err, &capErr) || capErr.Kind != "fence" {
		t.Fatalf("want fence CapacityError, got %v", err)
	}

	// The leaf CAS ran first and succeeded within the same transaction
	// as the fence CAS that then failed. Prove the rollback actually
	// undid it: leaf count must still be 0, not 1.
	var leafCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count FROM loc_capacity WHERE location_key = (SELECT location_key FROM locations WHERE location_id = 'leaf')`).Scan(&leafCount); err != nil {
		t.Fatal(err)
	}
	if leafCount != 0 {
		t.Fatalf("multi-target atomicity violated: leaf count is %d after a rolled-back move, want 0", leafCount)
	}
	// And no assignment was recorded either.
	if _, err := s.Get(ctx, "leaf"); err != nil {
		t.Fatal(err)
	}
	var assigned int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_assignment WHERE subject_ref = 's1'`).Scan(&assigned); err != nil {
		t.Fatal(err)
	}
	if assigned != 0 {
		t.Fatalf("multi-target atomicity violated: loc_assignment has a row for s1 after a rolled-back move")
	}
}

// TestMoveExitsAndEntersLeaf proves the leaf-to-leaf case: moving away
// from a leaf frees its capacity, moving into the new one claims it,
// and exactly one loc_journal row lands per move.
func TestMoveExitsAndEntersLeaf(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "a", &root, nil)
	mustDefPostable(t, s, "b", &root, nil)

	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "b"}); err != nil {
		t.Fatal(err)
	}

	var aCount, bCount int
	s.db.QueryRowContext(ctx, `SELECT count FROM loc_capacity WHERE location_key=(SELECT location_key FROM locations WHERE location_id='a')`).Scan(&aCount)
	s.db.QueryRowContext(ctx, `SELECT count FROM loc_capacity WHERE location_key=(SELECT location_key FROM locations WHERE location_id='b')`).Scan(&bCount)
	if aCount != 0 || bCount != 1 {
		t.Fatalf("after moving s1 from a to b: a=%d (want 0) b=%d (want 1)", aCount, bCount)
	}

	var journalRows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_journal WHERE subject_ref='s1'`).Scan(&journalRows); err != nil {
		t.Fatal(err)
	}
	if journalRows != 2 {
		t.Fatalf("want exactly 2 journal rows (one per move), got %d", journalRows)
	}
}

// BenchmarkMove is Stage 2's own newly-added exit criterion: a
// write-path throughput number recorded, however rough, against the
// guard-bearing core with no geometry involved (single-leaf CAS, no
// fences) — matching what this stage actually builds.
func BenchmarkMove(b *testing.B) {
	tmp := b.TempDir()
	db, err := sql.Open("sqlite", tmp+"/loc.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	s := NewStore(db, 0)
	ctx := context.Background()
	if err := s.Init(ctx); err != nil {
		b.Fatal(err)
	}
	root := "root"
	if _, err := s.Def(ctx, LocationDef{ID: root, Name: "root", Placement: rootAnchor()}); err != nil {
		b.Fatal(err)
	}
	if _, err := s.Def(ctx, LocationDef{ID: "bin", ParentID: &root, Name: "bin", Postable: true}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subject := "s" + string(rune(i))
		if err := s.Move(ctx, MoveParams{SubjectRef: subject, ToLocationID: "bin"}); err != nil {
			b.Fatal(err)
		}
	}
}
