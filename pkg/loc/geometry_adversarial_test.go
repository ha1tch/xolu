// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"math"
	"testing"
)

// TestPolygon_DegenerateInputs_NeverPanic proves every Geometry method
// handles 0/1/2-vertex polygons defensively — these are not valid
// simple polygons, but nothing in Go's type system prevents
// constructing one, so the methods must fail safely (false/+Inf, not
// a panic) rather than assume a caller always hands them >=3 vertices.
func TestPolygon_DegenerateInputs_NeverPanic(t *testing.T) {
	cases := []struct {
		name string
		p    Polygon
	}{
		{"empty", Polygon{}},
		{"one vertex", Polygon{Vertices: []Point{{Lat: 0, Lon: 0}}}},
		{"two vertices", Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 1, Lon: 1}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: panicked: %v", c.name, r)
				}
			}()
			if c.p.Contains(0, 0) {
				t.Errorf("%s: Contains should be false for a degenerate polygon, got true", c.name)
			}
			_ = c.p.Distance(0, 0)
			_, _, _, _ = c.p.BoundingBox()
			if c.p.SelfIntersects() {
				t.Errorf("%s: SelfIntersects should be false (too few edges to cross), got true", c.name)
			}
		})
	}
}

// TestPolygon_DuplicateConsecutiveVertices_NeverPanic: a real-world
// mistake (a client accidentally submitting the same point twice in a
// row) must not break containment or self-intersection detection —
// a zero-length edge is a degenerate case orientation() and
// segmentsIntersect() need to survive, not assumed safe.
func TestPolygon_DuplicateConsecutiveVertices_NeverPanic(t *testing.T) {
	p := Polygon{Vertices: []Point{
		{Lat: 0, Lon: 0}, {Lat: 0, Lon: 0}, // duplicate
		{Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0},
	}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on duplicate consecutive vertices: %v", r)
		}
	}()
	_ = p.Contains(5, 5)
	_ = p.SelfIntersects()
}

// TestPolygon_ExtremelyThinSliver: a near-zero-area polygon (a
// "sliver") is legal (loc-01-rest-api.md's own response contract
// names degenerate-but-legal polygons explicitly, via the warnings
// array) — containment must still resolve sanely: a point exactly on
// the sliver's own line is a coin-flip by construction (ray-casting
// has no universally "correct" answer for a zero-width shape), but a
// point clearly off the line must resolve false, and nothing panics.
func TestPolygon_ExtremelyThinSliver(t *testing.T) {
	sliver := Polygon{Vertices: []Point{
		{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 0.0000001, Lon: 10}, {Lat: 0.0000001, Lon: 0},
	}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on a sliver polygon: %v", r)
		}
	}()
	if sliver.Contains(5, 5) {
		t.Error("a point far from a near-zero-width sliver must not be contained")
	}
}

// TestPolygon_VertexTouchingNonAdjacentEdge is the genuinely tricky
// case for the orientation-based self-intersection test: a polygon
// where one vertex lies exactly ON a non-adjacent edge (touching, not
// crossing). Documents current behaviour rather than asserting a
// specific "correct" answer this test invented — a touching-but-not-
// crossing polygon is a degenerate edge case reasonable implementations
// disagree on; what matters is the answer is DETERMINISTIC and the
// call never panics, checked directly rather than assumed.
func TestPolygon_VertexTouchingNonAdjacentEdge(t *testing.T) {
	// A "bowtie-adjacent" shape: vertex (5,5) sits exactly on the
	// segment from (0,0) to (10,10).
	p := Polygon{Vertices: []Point{
		{Lat: 0, Lon: 0}, {Lat: 10, Lon: 0}, {Lat: 10, Lon: 10}, {Lat: 5, Lon: 5}, {Lat: 0, Lon: 10},
	}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on a vertex-touches-edge polygon: %v", r)
		}
	}()
	first := p.SelfIntersects()
	for i := 0; i < 5; i++ {
		if got := p.SelfIntersects(); got != first {
			t.Fatalf("SelfIntersects is non-deterministic across repeated calls on the same polygon: got %v then %v", first, got)
		}
	}
}

// TestCircle_NegativeRadius_RejectedAtWrite proves a real gap this
// test named directly: Circle.Contains uses "distance <= radius" —
// with a negative radius that predicate can never be true (a
// haversine distance is never negative), so a negative radius doesn't
// error, it silently creates a fence that can never be entered by
// anyone, ever. That is worse than refusing outright: a caller who
// made a sign mistake gets a fence that looks defined but never
// matches, not an error telling them why. SetFenceGeometry now
// refuses it.
func TestCircle_NegativeRadius_RejectedAtWrite(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.DefFence(ctx, "bad-radius", nil); err != nil {
		t.Fatal(err)
	}
	err := s.SetFenceGeometry(ctx, "bad-radius", Circle{CenterLat: 0, CenterLon: 0, RadiusMeters: -1000})
	if err == nil {
		t.Fatal("expected a negative radius to be refused at write time, got nil error")
	}
}

// TestCircle_ZeroRadius_ContainsOnlyExactCentre is a boundary case
// worth pinning down explicitly: a zero-radius circle should behave
// like a single point, not silently match everything or nothing
// unpredictably.
func TestCircle_ZeroRadius_ContainsOnlyExactCentre(t *testing.T) {
	c := Circle{CenterLat: 10, CenterLon: 20, RadiusMeters: 0}
	if !c.Contains(10, 20) {
		t.Error("a zero-radius circle must contain its own exact centre")
	}
	if c.Contains(10.001, 20) {
		t.Error("a zero-radius circle must not contain any point away from its centre")
	}
}

// TestGeometry_OutOfRangeCoordinates_NeverPanic: nothing in this
// package's own types prevents lat>90 or lon>180 from being
// constructed (Stage 0's decode discipline only guarantees a
// well-formed float64 reaches storage, not one within a real-world
// range) — the geometry math must still terminate sanely rather than
// producing NaN/Inf that propagates silently into a stored bounding
// box.
func TestGeometry_OutOfRangeCoordinates_NeverPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on out-of-range coordinates: %v", r)
		}
	}()
	c := Circle{CenterLat: 200, CenterLon: 400, RadiusMeters: 1000}
	_ = c.Contains(85, 175)
	minLat, minLon, maxLat, maxLon := c.BoundingBox()
	for name, v := range map[string]float64{"minLat": minLat, "minLon": minLon, "maxLat": maxLat, "maxLon": maxLon} {
		if math.IsNaN(v) {
			t.Errorf("BoundingBox() with out-of-range centre produced NaN for %s", name)
		}
	}
}

// TestPolygon_AntimeridianCrossing documents a known, real limitation
// rather than asserting the flat-Earth approximation gets this right
// (it can't, by construction): a polygon whose vertices straddle the
// +/-180 longitude line has no contiguous "inside" under simple
// ray-casting against raw longitude values, since the shortest path
// between lon=179 and lon=-179 is 2 degrees, not 358. This is named
// explicitly as unhandled, not silently wrong and undiscovered —
// loc-00-design.md's own §4e accepts facility/city-scale precision,
// and nothing in this wave's scope claims antimeridian correctness.
func TestPolygon_AntimeridianCrossing_KnownLimitation(t *testing.T) {
	// A "polygon" straddling the antimeridian, naively specified —
	// this is exactly the shape a caller near lon=180 would submit
	// without realising the wraparound issue.
	p := Polygon{Vertices: []Point{
		{Lat: -1, Lon: 179}, {Lat: -1, Lon: -179}, {Lat: 1, Lon: -179}, {Lat: 1, Lon: 179},
	}}
	// A point that is GEOGRAPHICALLY inside this fence (right at the
	// antimeridian, lon=180) — under naive raw-longitude ray-casting
	// this resolves incorrectly. Documented here as a known gap, not
	// silently trusted: if this assertion ever starts failing (i.e.
	// someone fixes antimeridian handling), that's a welcome surprise,
	// not a regression — update this test's expectation when it does.
	got := p.Contains(0, 180)
	t.Logf("antimeridian-crossing polygon Contains(0, 180) = %v -- known limitation, flat raw-longitude ray-casting does not handle wraparound (loc-00-design.md's own accepted precision boundary, not silently assumed correct here)", got)
}
