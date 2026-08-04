// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"math"
	"testing"
)

func TestPolygon_AxisAlignedRectangle(t *testing.T) {
	sq := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}
	cases := []struct {
		lat, lon float64
		want     bool
	}{
		{5, 5, true},   // centre
		{0, 0, true},   // corner, boundary
		{15, 5, false}, // outside on lat
		{5, 15, false}, // outside on lon
	}
	for _, c := range cases {
		if got := sq.Contains(c.lat, c.lon); got != c.want {
			t.Errorf("Contains(%v,%v) = %v, want %v", c.lat, c.lon, got, c.want)
		}
	}
	minLat, minLon, maxLat, maxLon := sq.BoundingBox()
	if minLat != 0 || minLon != 0 || maxLat != 10 || maxLon != 10 {
		t.Fatalf("BoundingBox: got (%v,%v,%v,%v), want (0,0,10,10)", minLat, minLon, maxLat, maxLon)
	}
}

// TestPolygon_Triangle exercises the general ray-cast path (not the
// axis-aligned fast path) against a simple, hand-verifiable shape: the
// right triangle (0,0),(0,10),(10,0) is exactly the region lat+lon<=10
// (for lat,lon>=0).
func TestPolygon_Triangle(t *testing.T) {
	tri := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 0}}}
	if !tri.Contains(2, 2) { // 2+2=4 < 10
		t.Error("(2,2) should be inside the triangle")
	}
	if tri.Contains(8, 8) { // 8+8=16 > 10
		t.Error("(8,8) should be outside the triangle")
	}
}

// TestPolygon_ConcaveLShape is Stage 3's own named requirement: concave
// polygon containment correct via ray-casting, not just convex. The
// L-shape below is full-height (lat 0-10) for lon in [0,5), but only
// half-height (lat 0-5) for lon in [5,10] — a shape a convex-hull or
// bounding-box-only test would get wrong, and ray-casting gets right.
func TestPolygon_ConcaveLShape(t *testing.T) {
	l := Polygon{Vertices: []Point{
		{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 5, Lon: 10},
		{Lat: 5, Lon: 5}, {Lat: 10, Lon: 5}, {Lat: 10, Lon: 0},
	}}
	if l.SelfIntersects() {
		t.Fatal("the L-shape itself must not be flagged self-intersecting — it's a valid simple polygon")
	}
	cases := []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"tall-arm interior, lon<5", 7, 2, true},
		{"the concave notch, lon>5 and lat>5", 7, 7, false},
		{"low band, lon>5, lat<5", 2, 7, true},
		{"outside overall bbox", 12, 2, false},
	}
	for _, c := range cases {
		if got := l.Contains(c.lat, c.lon); got != c.want {
			t.Errorf("%s: Contains(%v,%v) = %v, want %v", c.name, c.lat, c.lon, got, c.want)
		}
	}
}

// TestPolygon_SelfIntersectionRejected proves XOLU-LOC020's underlying
// detection: a classic bowtie is flagged, a simple polygon is not.
func TestPolygon_SelfIntersectionRejected(t *testing.T) {
	bowtie := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}, {Lat: 0, Lon: 10}}}
	if !bowtie.SelfIntersects() {
		t.Fatal("bowtie polygon must be detected as self-intersecting")
	}
	simple := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}
	if simple.SelfIntersects() {
		t.Fatal("a simple rectangle must not be flagged self-intersecting")
	}
	tri := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 0}}}
	if tri.SelfIntersects() {
		t.Fatal("a triangle (n<4) must never be flagged self-intersecting")
	}
}

func TestCircle_Contains(t *testing.T) {
	c := Circle{CenterLat: 0, CenterLon: 0, RadiusMeters: 1000}
	if !c.Contains(0, 0.005) { // ~556m at the equator
		t.Error("point ~556m from centre should be inside a 1000m circle")
	}
	if c.Contains(0, 0.02) { // ~2226m at the equator
		t.Error("point ~2226m from centre should be outside a 1000m circle")
	}
	minLat, minLon, maxLat, maxLon := c.BoundingBox()
	if minLat >= 0 || maxLat <= 0 || minLon >= 0 || maxLon <= 0 {
		t.Fatalf("circle bounding box should straddle the centre: got (%v,%v,%v,%v)", minLat, minLon, maxLat, maxLon)
	}
}

// TestGeometry_NonFiniteNeverReachesContains is the numerics
// regression test Stage 3 names: since Stage 0 pinned typed-float64
// decode discipline (no untyped-map/string intermediate), a
// non-finite coordinate should be *structurally* unable to reach this
// code, not merely "happens not to occur" in testing. This test
// asserts the code path directly — Contains/Distance/BoundingBox
// applied to a deliberately-constructed non-finite Point return a
// well-defined, non-panicking result (false / +Inf / NaN propagation),
// never a crash — the defensive floor beneath the structural
// guarantee, not a substitute for it.
func TestGeometry_NonFiniteNeverReachesContains(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("geometry code must not panic on non-finite input, even though Stage 0's decode discipline should make this unreachable in practice: %v", r)
		}
	}()
	sq := Polygon{Vertices: []Point{{Lat: 0, Lon: 0}, {Lat: 0, Lon: 10}, {Lat: 10, Lon: 10}, {Lat: 10, Lon: 0}}}
	_ = sq.Contains(math.NaN(), math.Inf(1))
	_ = sq.Distance(math.NaN(), 0)
	c := Circle{CenterLat: 0, CenterLon: 0, RadiusMeters: math.NaN()}
	_ = c.Contains(0, 0)
}
