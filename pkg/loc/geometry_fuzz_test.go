// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// geometry_fuzz_test.go — coverage-guided crash-only proofs, not
// correctness proofs (those are the targeted unit and adversarial
// tests elsewhere in this package). Go's native fuzzer requires fixed-
// arity primitive parameters, so a variable-length polygon is
// decomposed into a fixed set of vertex coordinates and reconstructed
// inside each fuzz function — the standard pattern for fuzzing
// structured data with go test -fuzz.

package loc

import (
	"math"
	"testing"
)

// FuzzPolygonQuad fuzzes every Geometry method against an arbitrary
// 4-vertex polygon (covering both the general ray-cast path and the
// axis-aligned-rectangle fast path, depending on what values the
// fuzzer finds) plus an arbitrary query point. The only assertion is
// "never panics" — NaN/Inf/denormal/extreme inputs are all fair game
// for the fuzzer to discover, deliberately not filtered out, since
// Stage 0's decode discipline is a claim about the JSON boundary, not
// a guarantee this code can rely on for every possible Go-level
// caller.
func FuzzPolygonQuad(f *testing.F) {
	f.Add(0.0, 0.0, 0.0, 10.0, 10.0, 10.0, 10.0, 0.0, 5.0, 5.0)
	f.Add(0.0, 0.0, 10.0, 10.0, 10.0, 0.0, 0.0, 10.0, 5.0, 5.0) // bowtie (self-intersecting)
	f.Add(math.NaN(), 0.0, 0.0, 10.0, 10.0, 10.0, 10.0, 0.0, 5.0, 5.0)
	f.Add(math.Inf(1), 0.0, 0.0, 10.0, 10.0, 10.0, 10.0, 0.0, 5.0, 5.0)
	f.Add(0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0) // all-identical vertices
	f.Add(1e300, 1e300, -1e300, -1e300, 1e300, -1e300, -1e300, 1e300, 0.0, 0.0)

	f.Fuzz(func(t *testing.T, lat1, lon1, lat2, lon2, lat3, lon3, lat4, lon4, qLat, qLon float64) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on vertices (%v,%v)(%v,%v)(%v,%v)(%v,%v) query (%v,%v): %v",
					lat1, lon1, lat2, lon2, lat3, lon3, lat4, lon4, qLat, qLon, r)
			}
		}()
		p := Polygon{Vertices: []Point{
			{Lat: lat1, Lon: lon1}, {Lat: lat2, Lon: lon2},
			{Lat: lat3, Lon: lon3}, {Lat: lat4, Lon: lon4},
		}}
		_ = p.Contains(qLat, qLon)
		_ = p.Distance(qLat, qLon)
		_ = p.SelfIntersects()
		_, _, _, _ = p.BoundingBox()
	})
}

// FuzzPolygonSelfIntersects targets the self-intersection detector
// specifically with a larger, still-fixed vertex count (6) — the
// adjacency-check bug T-116 found (SelfIntersects flagging every
// valid simple polygon) lived specifically in the multi-edge
// adjacency logic, which a 4-vertex-only fuzz target exercises less
// thoroughly than one with more edges to get adjacency wrong on.
func FuzzPolygonSelfIntersects(f *testing.F) {
	f.Add(0.0, 0.0, 0.0, 10.0, 5.0, 15.0, 10.0, 10.0, 10.0, 0.0, 5.0, -5.0)
	f.Add(0.0, 0.0, 10.0, 10.0, 10.0, 0.0, 0.0, 10.0, 5.0, 5.0, 5.0, 5.0)

	f.Fuzz(func(t *testing.T, lat1, lon1, lat2, lon2, lat3, lon3, lat4, lon4, lat5, lon5, lat6, lon6 float64) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic in SelfIntersects on a 6-vertex polygon: %v", r)
			}
		}()
		p := Polygon{Vertices: []Point{
			{Lat: lat1, Lon: lon1}, {Lat: lat2, Lon: lon2}, {Lat: lat3, Lon: lon3},
			{Lat: lat4, Lon: lon4}, {Lat: lat5, Lon: lon5}, {Lat: lat6, Lon: lon6},
		}}
		first := p.SelfIntersects()
		// Determinism check gets a free ride here too: repeated calls
		// on the SAME polygon must agree.
		for i := 0; i < 3; i++ {
			if got := p.SelfIntersects(); got != first {
				t.Fatalf("SelfIntersects non-deterministic across repeated calls: %v then %v", first, got)
			}
		}
	})
}

// FuzzCircle fuzzes Circle's own three Geometry methods, including
// deliberately allowing a negative radius through (SetFenceGeometry
// refuses that at the write-time boundary, Stage 6's own adversarial
// finding — but Circle itself, as a bare Go value, has no such
// guard, and a caller constructing one directly must still not crash
// it).
func FuzzCircle(f *testing.F) {
	f.Add(0.0, 0.0, 1000.0, 5.0, 5.0)
	f.Add(90.0, 0.0, 1000.0, 90.0, 0.0)    // exact pole
	f.Add(0.0, 0.0, -1000.0, 5.0, 5.0)     // negative radius
	f.Add(0.0, 0.0, math.Inf(1), 5.0, 5.0) // infinite radius
	f.Add(math.NaN(), 0.0, 1000.0, 5.0, 5.0)

	f.Fuzz(func(t *testing.T, centerLat, centerLon, radius, qLat, qLon float64) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on Circle{%v,%v,%v} query (%v,%v): %v", centerLat, centerLon, radius, qLat, qLon, r)
			}
		}()
		c := Circle{CenterLat: centerLat, CenterLon: centerLon, RadiusMeters: radius}
		_ = c.Contains(qLat, qLon)
		_ = c.Distance(qLat, qLon)
		_, _, _, _ = c.BoundingBox()
	})
}

// FuzzSafeLongitudeDelta targets the shared division-guard helper
// itself directly — the fix this whole file's neighbouring
// division_adversarial_test.go exists because of — with arbitrary
// offset and denominator values, proving the +/-180 bound holds for
// every input the fuzzer can construct, not just the pole cases
// reasoned about by hand.
func FuzzSafeLongitudeDelta(f *testing.F) {
	f.Add(1000.0, 6.8e-12)
	f.Add(0.0, 0.0)
	f.Add(math.Inf(1), 0.0)
	f.Add(math.NaN(), 1.0)
	f.Add(1e300, 1e-300)

	f.Fuzz(func(t *testing.T, offset, denom float64) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on safeLongitudeDelta(%v, %v): %v", offset, denom, r)
			}
		}()
		got := safeLongitudeDelta(offset, denom)
		if math.IsNaN(offset) || math.IsNaN(denom) {
			return // NaN in, NaN-shaped out is expected; not a bound violation
		}
		if math.IsInf(offset, 0) {
			return // an infinite offset legitimately produces an unbounded delta; the guard's job is the near-zero DENOMINATOR case, not this
		}
		if math.Abs(got) > 180 && !math.IsNaN(got) {
			t.Fatalf("safeLongitudeDelta(%v, %v) = %v exceeds the +/-180 bound", offset, denom, got)
		}
	})
}
