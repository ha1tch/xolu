// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func checkAllOracles(t *testing.T, ctx context.Context, s *Store) {
	t.Helper()
	for _, o := range s.Oracles() {
		res, err := o.Check(ctx)
		if err != nil {
			t.Fatalf("oracle %q: %v", o.Name, err)
		}
		if !res.Equal {
			t.Fatalf("oracle %q diverged: %s\nderived:\n%s\ncurrent:\n%s", o.Name, res.FirstDivergence, res.Derived, res.Current)
		}
	}
}

// TestOracles_AgreeFromEmpty proves §8c's stated acceptance criterion
// directly: both current assignment and current capacity counts are
// exactly reconstructible from empty.
func TestOracles_AgreeFromEmpty(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	checkAllOracles(t, ctx, s)
}

// TestOracles_AgreeAfterBasicSequence exercises moves, a leaf-to-leaf
// move, and fence reports together, then checks every oracle.
func TestOracles_AgreeAfterBasicSequence(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "a", &root, nil)
	mustDefPostable(t, s, "b", &root, nil)
	if _, err := s.DefFence(ctx, "yard", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFenceGeometry(ctx, "yard", Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}); err != nil {
		t.Fatal(err)
	}

	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ctx, MoveParams{SubjectRef: "s1", ToLocationID: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ctx, MoveParams{SubjectRef: "s2", ToLocationID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(ctx, "s1", 5, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(ctx, "s2", 50, 50); err != nil {
		t.Fatal(err) // outside the fence: no-op, per §8a
	}

	checkAllOracles(t, ctx, s)
}

// TestReport_NoOpWritesNothing is §8a's own rule, proven directly: a
// report producing no containment change writes NO journal row at
// all, not a journal row that happens to carry empty deltas.
func TestReport_NoOpWritesNothing(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	// No fences defined at all: every report is necessarily a no-op.
	if err := s.Report(ctx, "s1", 5, 5); err != nil {
		t.Fatal(err)
	}
	var journalRows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_journal WHERE subject_ref='s1'`).Scan(&journalRows); err != nil {
		t.Fatal(err)
	}
	if journalRows != 0 {
		t.Fatalf("no-op report must write zero journal rows, got %d", journalRows)
	}

	// Reporting the SAME point twice: the second call is a no-op
	// (membership unchanged), even though the first call was real.
	if _, err := s.DefFence(ctx, "yard", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFenceGeometry(ctx, "yard", Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(ctx, "s2", 5, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(ctx, "s2", 5, 5); err != nil { // same point again
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_journal WHERE subject_ref='s2'`).Scan(&journalRows); err != nil {
		t.Fatal(err)
	}
	if journalRows != 1 {
		t.Fatalf("repeating the same report must not write a second journal row, got %d", journalRows)
	}
}

// TestOracles_RandomizedSequenceWithRefusals is Stage 4's own named
// testing requirement: derive(journal) == current after a randomised
// sequence of moves and reports, including refused attempts that must
// leave no trace — mirroring the ts dxp adapter's own
// aborted-batch-leaves-no-trace proof (T-86), applied here to a
// refused CAS rather than an aborted Pebble batch.
func TestOracles_RandomizedSequenceWithRefusals(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})

	const nLeaves = 4
	const nSubjects = 6
	ceiling := int64(2) // deliberately tight: forces real refusals under random churn
	for i := 0; i < nLeaves; i++ {
		mustDefPostable(t, s, fmt.Sprintf("leaf-%d", i), &root, &ceiling)
	}
	if _, err := s.DefFence(ctx, "yard", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFenceGeometry(ctx, "yard", Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}); err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(42))
	refusals, admits := 0, 0

	for step := 0; step < 300; step++ {
		subject := fmt.Sprintf("subj-%d", rng.Intn(nSubjects))
		if rng.Intn(2) == 0 {
			leaf := fmt.Sprintf("leaf-%d", rng.Intn(nLeaves))

			// Snapshot the pre-attempt oracle fingerprints for the
			// no-trace proof on refusal.
			preAssign, err := s.AssignmentFoldOracle().Current(ctx)
			if err != nil {
				t.Fatal(err)
			}
			preOccup, err := s.OccupancyFoldOracle().Current(ctx)
			if err != nil {
				t.Fatal(err)
			}

			err = s.Move(ctx, MoveParams{SubjectRef: subject, ToLocationID: leaf})
			if err == nil {
				admits++
				continue
			}
			var capErr *CapacityError
			if !errors.As(err, &capErr) {
				t.Fatalf("step %d: unexpected Move error: %v", step, err)
			}
			refusals++
			postAssign, err := s.AssignmentFoldOracle().Current(ctx)
			if err != nil {
				t.Fatal(err)
			}
			postOccup, err := s.OccupancyFoldOracle().Current(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if preAssign != postAssign {
				t.Fatalf("step %d: refused move left a trace in loc_assignment", step)
			}
			if preOccup != postOccup {
				t.Fatalf("step %d: refused move left a trace in loc_capacity", step)
			}
		} else {
			lat := rng.Float64() * 20 // sometimes inside [0,10]x[0,10], sometimes outside
			lon := rng.Float64() * 20
			if err := s.Report(ctx, subject, lat, lon); err != nil {
				t.Fatalf("step %d: unexpected Report error: %v", step, err)
			}
		}
	}

	if refusals == 0 {
		t.Fatal("test setup: a tight ceiling under 300 random moves across 6 subjects/4 leaves produced zero refusals — the refusal path was never actually exercised")
	}
	t.Logf("randomised sequence: %d admits, %d refusals", admits, refusals)

	checkAllOracles(t, ctx, s)
}
