// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// degenerate_polygon_test.go — T-132 (wave 9b): direct unit coverage
// for Polygon.IsDegenerate/EffectiveVertexCount, beneath the HTTP-level
// proofs in pkg/server.

package loc

import "testing"

func TestPolygon_EffectiveVertexCount(t *testing.T) {
	cases := []struct {
		name string
		pts  []Point
		want int
	}{
		{"closed triangle (RFC 7946 closure repeat)", []Point{{0, 0}, {0, 10}, {10, 0}, {0, 0}}, 3},
		{"closed square", []Point{{0, 0}, {0, 10}, {10, 10}, {10, 0}, {0, 0}}, 4},
		{"all coincident", []Point{{5, 5}, {5, 5}, {5, 5}, {5, 5}}, 1},
		{"collinear line, closed", []Point{{0, 0}, {1, 1}, {2, 2}, {0, 0}}, 3}, // count doesn't detect collinearity, area does
		{"empty", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Polygon{Vertices: tc.pts}.EffectiveVertexCount()
			if got != tc.want {
				t.Errorf("EffectiveVertexCount() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPolygon_IsDegenerate(t *testing.T) {
	cases := []struct {
		name string
		pts  []Point
		want bool
	}{
		{"ordinary square", []Point{{0, 0}, {0, 10}, {10, 10}, {10, 0}, {0, 0}}, false},
		{"collinear (zero area)", []Point{{0, 0}, {1, 1}, {2, 2}, {0, 0}}, true},
		{"all coincident", []Point{{5, 5}, {5, 5}, {5, 5}, {5, 5}}, true},
		{"fewer than 3 effective vertices, not closed", []Point{{0, 0}, {1, 1}}, true},
		{"tiny but real square, well above epsilon", []Point{{0, 0}, {0, 0.001}, {0.001, 0.001}, {0.001, 0}, {0, 0}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Polygon{Vertices: tc.pts}.IsDegenerate()
			if got != tc.want {
				t.Errorf("IsDegenerate() = %v, want %v", got, tc.want)
			}
		})
	}
}
