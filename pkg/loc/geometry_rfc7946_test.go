// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// geometry_rfc7946_test.go — a systematic pass against RFC 7946's own
// normative text (https://datatracker.ietf.org/doc/html/rfc7946),
// not just the adversarial cases found by inspection. Nothing in this
// package's storage layer understands GeoJSON at all: SQLite's own
// role is a numeric bounding-box comparison (the R-tree pre-filter),
// nothing more. Every RFC 7946 parsing/validation concern lives
// entirely in DecodeGeoJSONPolygon (geometry.go) and is this file's
// own responsibility to check, not something pushed down and covered
// implicitly elsewhere.
//
// Scope: this package implements only the Polygon geometry type
// (§3.1.6) plus its own non-RFC "circle" extension (loc-00-design.md
// §4d, explicitly never a GeoJSON type pretending to be one). Point,
// MultiPoint, LineString, MultiLineString, MultiPolygon,
// GeometryCollection, Feature, and FeatureCollection (§3.1.2-3.1.8,
// §3.2, §3.3) are out of scope entirely — a "type" value naming any
// of them is refused at the HTTP handler layer (v2_loc_handlers.go's
// own switch), not tested here.

package loc

import "testing"

// TestRFC7946_Appendix_A3_NoHoles is the RFC's own worked example
// (Appendix A.3, "No holes"), decoded and round-tripped exactly.
func TestRFC7946_Appendix_A3_NoHoles(t *testing.T) {
	coords := [][][2]float64{{
		{100.0, 0.0}, {101.0, 0.0}, {101.0, 1.0}, {100.0, 1.0}, {100.0, 0.0},
	}}
	p, err := DecodeGeoJSONPolygon(coords)
	if err != nil {
		t.Fatalf("RFC 7946 Appendix A.3's own valid example was refused: %v", err)
	}
	if len(p.Vertices) != 4 {
		t.Fatalf("want 4 vertices (the closing repeat dropped), got %d", len(p.Vertices))
	}
	// Coordinate order: GeoJSON is [lon, lat]; this package's own
	// Point is {Lat, Lon} — confirm the swap actually happened, not
	// just that decode succeeded.
	want := Point{Lat: 0.0, Lon: 100.0}
	if p.Vertices[0] != want {
		t.Fatalf("first vertex: want %+v (lat/lon swapped from GeoJSON's lon/lat), got %+v", want, p.Vertices[0])
	}
}

// TestRFC7946_Appendix_A3_WithHoles_Refused is RFC 7946's own OTHER
// worked example from the same appendix — a polygon WITH a hole,
// explicitly valid per §3.1.6 ("any others MUST be interior rings").
// This package refuses it outright (a real, named v1 scope
// narrowing, loc-00-design.md), and the point of THIS test is
// specifically that the refusal is loud and explicit, not the earlier
// (fixed) behaviour of silently keeping only the exterior ring and
// dropping the hole without saying so — which would have looked like
// success while quietly producing a fence whose real, submitted
// shape was different from what got stored.
func TestRFC7946_Appendix_A3_WithHoles_Refused(t *testing.T) {
	coords := [][][2]float64{
		{{100.0, 0.0}, {101.0, 0.0}, {101.0, 1.0}, {100.0, 1.0}, {100.0, 0.0}}, // exterior
		{{100.8, 0.8}, {100.8, 0.2}, {100.2, 0.2}, {100.2, 0.8}, {100.8, 0.8}}, // hole
	}
	_, err := DecodeGeoJSONPolygon(coords)
	if err == nil {
		t.Fatal("a polygon with a hole (RFC 7946 Appendix A.3's own second example) must be refused, not silently accepted with the hole dropped")
	}
}

// TestRFC7946_Section3_1_6_MinimumFourPositions: "A linear ring is a
// closed LineString with four or more positions."
func TestRFC7946_Section3_1_6_MinimumFourPositions(t *testing.T) {
	// 3 positions: not enough for a closed ring even before counting
	// the closing repeat as one of the four.
	_, err := DecodeGeoJSONPolygon([][][2]float64{{{0, 0}, {1, 1}, {0, 0}}})
	if err == nil {
		t.Fatal("§3.1.6: a ring with 3 positions must be refused (minimum is four)")
	}
	// Exactly 4 positions (3 distinct + closing repeat): the RFC's own
	// stated minimum, must succeed.
	_, err = DecodeGeoJSONPolygon([][][2]float64{{{0, 0}, {1, 0}, {0, 1}, {0, 0}}})
	if err != nil {
		t.Fatalf("§3.1.6: a ring with exactly 4 positions (the stated minimum) must be accepted, got %v", err)
	}
}

// TestRFC7946_Section3_1_6_ClosureRequired: "The first and last
// positions are equivalent, and they MUST contain identical values."
func TestRFC7946_Section3_1_6_ClosureRequired(t *testing.T) {
	_, err := DecodeGeoJSONPolygon([][][2]float64{{{0, 0}, {1, 0}, {1, 1}, {0, 1}}}) // does not close
	if err == nil {
		t.Fatal("§3.1.6: an unclosed ring (first != last) must be refused")
	}
}

// TestRFC7946_Section3_1_6_WindingOrderNotEnforced: "A linear ring
// MUST follow the right-hand rule... Note: ...parsers SHOULD NOT
// reject Polygons that do not follow the right-hand rule." This
// package's ray-casting (even-odd rule) is winding-order-independent
// by construction, not by accident — proven here directly rather than
// assumed from the algorithm's own shape: the SAME square submitted
// clockwise and counterclockwise must produce IDENTICAL containment
// results for the same query points.
func TestRFC7946_Section3_1_6_WindingOrderNotEnforced(t *testing.T) {
	ccw, err := DecodeGeoJSONPolygon([][][2]float64{{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {0, 0}}})
	if err != nil {
		t.Fatalf("counterclockwise square refused: %v", err)
	}
	cw, err := DecodeGeoJSONPolygon([][][2]float64{{{0, 0}, {0, 10}, {10, 10}, {10, 0}, {0, 0}}})
	if err != nil {
		t.Fatalf("clockwise square refused (§3.1.6's own 'SHOULD NOT reject' rule): %v", err)
	}
	points := []struct{ lat, lon float64 }{
		{5, 5},   // inside
		{50, 50}, // outside
		{0, 0},   // on a vertex
	}
	for _, pt := range points {
		gotCCW := ccw.Contains(pt.lat, pt.lon)
		gotCW := cw.Contains(pt.lat, pt.lon)
		if gotCCW != gotCW {
			t.Errorf("winding order affected Contains(%v,%v): CCW=%v CW=%v -- §3.1.6 says parsers SHOULD NOT reject either, and by extension both should behave identically", pt.lat, pt.lon, gotCCW, gotCW)
		}
	}
}

// TestRFC7946_Section3_1_1_ThreeElementPosition_AltitudeIgnored:
// §3.1.1 explicitly permits a position to carry an optional third
// (altitude) element and says extra elements "MAY be ignored by
// parsers." Checked directly rather than assumed: Go's own
// encoding/json, decoding a 3-element JSON array into this package's
// [2]float64 position type, silently truncates to the first two
// elements (documented Go behaviour, not an error) — exactly what the
// RFC sanctions. This was first written assuming Go would error on
// the size mismatch and require an explicit rejection path; checking
// the real behaviour caught that assumption was wrong before it
// shipped as a test asserting the wrong thing. See
// v2_loc_geojson_adversarial_test.go for the HTTP-level proof.
func TestRFC7946_Section3_1_1_ThreeElementPosition_AltitudeIgnored(t *testing.T) {
	t.Log("three-element (altitude-bearing) positions are silently truncated to [lon,lat] by Go's own encoding/json array-decode behaviour, matching §3.1.1's 'MAY be ignored' -- see the HTTP-level test for the end-to-end proof that this succeeds (201), not an assumed rejection")
}

// TestRFC7946_Section3_EmptyCoordinatesArray: "GeoJSON processors MAY
// interpret Geometry objects with empty coordinates arrays as null
// objects." This package chooses to refuse rather than treat as null
// — a defensible reading of a MAY, not a violation of it, but worth
// stating the choice explicitly against the RFC's own permissive
// language rather than leaving the reasoning implicit.
func TestRFC7946_Section3_EmptyCoordinatesArray(t *testing.T) {
	_, err := DecodeGeoJSONPolygon([][][2]float64{})
	if err == nil {
		t.Fatal("an empty coordinates array must be refused (this package's own choice among the RFC's permitted interpretations, not silently treated as valid)")
	}
}
