// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"errors"
	"testing"
)

func TestSetFenceGeometry_RejectsSelfIntersection(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	fk, err := s.DefFence(ctx, "bad", nil)
	if err != nil {
		t.Fatal(err)
	}
	bowtie := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}, {Lat: 0, Lon: 10}}}
	err = s.SetFenceGeometry(ctx, "bad", bowtie)
	if err == nil {
		t.Fatal("expected XOLU-LOC020 refusal for a self-intersecting polygon")
	}
	_ = fk
}

// TestResolveFenceMembership_PrefilterNotTrustedAlone is the test that
// actually distinguishes the two-stage design from a bounding-box-only
// shortcut: a point sits inside a circle fence's square BOUNDING BOX
// (near a corner) but outside the circle's real radius. If the
// pre-filter's box were ever trusted alone (§7b's own named risk),
// this point would be wrongly reported as a member.
func TestResolveFenceMembership_PrefilterNotTrustedAlone(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.DefFence(ctx, "circle-fence", nil); err != nil {
		t.Fatal(err)
	}
	circle := Circle{CenterLat: 0, CenterLon: 0, RadiusMeters: 1000}
	if err := s.SetFenceGeometry(ctx, "circle-fence", circle); err != nil {
		t.Fatal(err)
	}

	minLat, minLon, maxLat, maxLon := circle.BoundingBox()
	// A corner of the bounding box is inside the BOX but, for a circle
	// inscribed in a square, well outside the actual circle (the
	// corner-to-centre distance exceeds the radius by construction).
	cornerLat, cornerLon := maxLat, maxLon
	if cornerLat <= minLat || cornerLon <= minLon {
		t.Fatal("test setup: degenerate bounding box")
	}

	member, err := s.ResolveFenceMembership(ctx, cornerLat, cornerLon)
	if err != nil {
		t.Fatal(err)
	}
	if len(member) != 0 {
		t.Fatalf("bounding-box corner must NOT be a member of the inscribed circle — pre-filter box was trusted alone: %v", member)
	}

	// The actual centre must resolve as a member.
	member, err = s.ResolveFenceMembership(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(member) != 1 {
		t.Fatalf("circle centre must resolve as a member of its own fence, got %v", member)
	}
}

func TestResolveFenceMembership_Polygon(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.DefFence(ctx, "yard", nil); err != nil {
		t.Fatal(err)
	}
	sq := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}
	if err := s.SetFenceGeometry(ctx, "yard", sq); err != nil {
		t.Fatal(err)
	}
	member, err := s.ResolveFenceMembership(ctx, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(member) != 1 {
		t.Fatalf("(5,5) should be inside the yard fence, got %v", member)
	}
	member, err = s.ResolveFenceMembership(ctx, 50, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(member) != 0 {
		t.Fatalf("(50,50) should be outside every fence, got %v", member)
	}
}

// TestReport_EndToEnd proves the two-write-path distinction directly:
// Report changes fence membership and nothing else — loc_assignment
// (tree-leaf position) must be completely untouched by it.
func TestReport_EndToEnd(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.DefFence(ctx, "yard", nil); err != nil {
		t.Fatal(err)
	}
	sq := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}
	if err := s.SetFenceGeometry(ctx, "yard", sq); err != nil {
		t.Fatal(err)
	}

	if err := s.Report(ctx, "vehicle-1", 5, 5); err != nil {
		t.Fatalf("report into yard: %v", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_fence_membership WHERE subject_ref='vehicle-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want 1 fence membership row after entering, got %d", count)
	}
	var assignmentCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_assignment WHERE subject_ref='vehicle-1'`).Scan(&assignmentCount); err != nil {
		t.Fatal(err)
	}
	if assignmentCount != 0 {
		t.Fatalf("Report must never touch loc_assignment (two-write-path distinction) — found %d rows", assignmentCount)
	}

	// Report elsewhere, outside the fence: membership must clear.
	if err := s.Report(ctx, "vehicle-1", 50, 50); err != nil {
		t.Fatalf("report outside yard: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_fence_membership WHERE subject_ref='vehicle-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("want 0 fence membership rows after leaving, got %d", count)
	}

	var journalRows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_journal WHERE subject_ref='vehicle-1' AND kind='report'`).Scan(&journalRows); err != nil {
		t.Fatal(err)
	}
	if journalRows != 2 {
		t.Fatalf("want 2 report journal rows (enter + exit), got %d", journalRows)
	}
}

func TestReport_RefusesAtFenceCapacity(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	fk, err := s.DefFence(ctx, "small-yard", nil)
	if err != nil {
		t.Fatal(err)
	}
	sq := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}
	if err := s.SetFenceGeometry(ctx, "small-yard", sq); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE loc_fence_capacity SET ceiling = 1 WHERE fence_key = ?`, uint32(fk)); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(ctx, "v1", 5, 5); err != nil {
		t.Fatalf("first report into capacity-1 fence: %v", err)
	}
	err = s.Report(ctx, "v2", 5, 5)
	var capErr *CapacityError
	if !errors.As(err, &capErr) || capErr.Kind != "fence" {
		t.Fatalf("second report into full fence: want *CapacityError{Kind:fence}, got %v", err)
	}
}
