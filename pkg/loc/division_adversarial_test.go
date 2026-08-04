// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"math"
	"testing"
)

// TestSafeLongitudeDelta_PoleCase is the direct regression test for
// the bug this file's own name documents: at lat=90 exactly,
// math.Cos(90*math.Pi/180) is 6.12e-17, not exactly 0.0, so the
// original "!= 0" guard let the division through with an
// almost-zero denominator. Confirmed directly before this fix: a 1km
// offset at lat=90 produced a "valid" float64 longitude delta of
// ~1.47e14 degrees — not a panic, silently corrupting whatever it
// fed into. This test proves the fixed behaviour is bounded.
func TestSafeLongitudeDelta_PoleCase(t *testing.T) {
	cases := []struct {
		name string
		lat  float64
	}{
		{"exact north pole", 90.0},
		{"exact south pole", -90.0},
		{"a hair from the north pole", 89.9999999},
		{"a hair from the south pole", -89.9999999},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			metresPerDegreeLon := metresPerDegreeLat * math.Cos(c.lat*math.Pi/180.0)
			delta := safeLongitudeDelta(1000, metresPerDegreeLon) // 1km offset
			if math.IsInf(delta, 0) || math.IsNaN(delta) {
				t.Fatalf("safeLongitudeDelta at %s produced Inf/NaN: %v", c.name, delta)
			}
			if math.Abs(delta) > 180 {
				t.Fatalf("safeLongitudeDelta at %s exceeded the +/-180 bound: %v", c.name, delta)
			}
		})
	}
}

// TestSafeLongitudeDelta_ZeroOffsetStaysZero: a zero offset at the
// pole must stay exactly zero, not clamp to +/-180 — the clamp is
// only for a genuinely nonzero offset that would otherwise blow up.
func TestSafeLongitudeDelta_ZeroOffsetStaysZero(t *testing.T) {
	if got := safeLongitudeDelta(0, 6.8e-12); got != 0 {
		t.Fatalf("zero offset at a near-zero denominator: want exactly 0, got %v", got)
	}
}

// TestSafeLongitudeDelta_NormalCaseUnaffected: away from the poles,
// the guard must not perturb an ordinary, well-defined division.
func TestSafeLongitudeDelta_NormalCaseUnaffected(t *testing.T) {
	metresPerDegreeLon := metresPerDegreeLat * math.Cos(0) // equator
	got := safeLongitudeDelta(1000, metresPerDegreeLon)
	want := 1000 / metresPerDegreeLon
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("ordinary case perturbed by the guard: want %v, got %v", want, got)
	}
}

// TestCircle_BoundingBox_AtPole_NeverProducesInsaneValues is the
// end-to-end proof at the actual call site, not just the shared
// helper in isolation.
func TestCircle_BoundingBox_AtPole_NeverProducesInsaneValues(t *testing.T) {
	c := Circle{CenterLat: 90, CenterLon: 0, RadiusMeters: 1000}
	minLat, minLon, maxLat, maxLon := c.BoundingBox()
	for name, v := range map[string]float64{"minLat": minLat, "minLon": minLon, "maxLat": maxLat, "maxLon": maxLon} {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			t.Fatalf("BoundingBox at the pole produced Inf/NaN for %s: %v", name, v)
		}
	}
	if math.Abs(minLon) > 180 || math.Abs(maxLon) > 180 {
		t.Fatalf("BoundingBox at the pole exceeded +/-180 longitude: minLon=%v maxLon=%v", minLon, maxLon)
	}
}

// TestComposeAbsolutePosition_AnchorAtPole_NeverProducesInsaneValues
// is ComposeAbsolutePosition's own end-to-end proof — the highest-
// stakes call site, since its result gets stored and used for real
// guard decisions, not just a read-side convenience like Nearby.
func TestComposeAbsolutePosition_AnchorAtPole_NeverProducesInsaneValues(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "pole-root"
	mustDef(t, s, LocationDef{ID: root, Name: "pole", Placement: Placement{
		Anchor: &GeoAnchor{Lat: 90, Lon: 0, Alt: 0, TrueNorth: 0},
	}})
	child := "pole-root/offset"
	mustDef(t, s, LocationDef{ID: child, ParentID: &root, Name: "offset",
		Placement: Placement{OffsetX: 1000, OffsetY: 1000, OffsetZ: 0, Rotation: 0}})

	pos, err := s.ComposeAbsolutePosition(ctx, child)
	if err != nil {
		t.Fatalf("ComposeAbsolutePosition with an anchor at the pole: %v", err)
	}
	if math.IsInf(pos.Lat, 0) || math.IsNaN(pos.Lat) || math.IsInf(pos.Lon, 0) || math.IsNaN(pos.Lon) {
		t.Fatalf("ComposeAbsolutePosition with an anchor at the pole produced Inf/NaN: %+v", pos)
	}
	if math.Abs(pos.Lon) > 1e6 {
		t.Fatalf("ComposeAbsolutePosition with an anchor at the pole produced an absurd longitude (pre-fix behaviour was ~1.47e14): %v", pos.Lon)
	}
}

// TestNearby_AnchorNearPole_NeverPanicsOrHangs is Nearby's own
// end-to-end proof, including the query it builds from the (now
// bounded) delta actually executing successfully against SQLite.
func TestNearby_AnchorNearPole_NeverPanicsOrHangs(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Nearby near the pole panicked: %v", r)
		}
	}()
	if _, err := s.DefFence(ctx, "pole-fence", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFenceGeometry(ctx, "pole-fence", Circle{CenterLat: 89.999999, CenterLon: 0, RadiusMeters: 500}); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Nearby(ctx, 90, 0, 1000)
	if err != nil {
		t.Fatalf("Nearby at the pole: %v", err)
	}
}
