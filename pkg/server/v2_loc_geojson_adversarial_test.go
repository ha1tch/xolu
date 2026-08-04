// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"bytes"
	"math"
	"net/http"
	"testing"
	"time"
)

// TestLocAPI_GeoJSON_EmptyCoordinates_Returns400 covers the JSON-valid
// but semantically-empty case: "coordinates": [] decodes cleanly as
// an empty [][][2]float64, which DecodeGeoJSONPolygon must refuse
// with a clean 400, not a panic from indexing coordinates[0].
func TestLocAPI_GeoJSON_EmptyCoordinates_Returns400(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject":  "zones:1",
		"geometry": map[string]interface{}{"type": "Polygon", "coordinates": [][][2]float64{}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("empty coordinates array: want 400, got %d %v", status, resp)
	}
}

// TestLocAPI_GeoJSON_TooFewPositions_Returns400: a ring with fewer
// than 4 positions (3 distinct vertices + closing repeat) can't be a
// closed simple polygon.
func TestLocAPI_GeoJSON_TooFewPositions_Returns400(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:1",
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{0, 0}, {1, 1}, {0, 0}}}, // 3 positions
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("ring with 3 positions: want 400, got %d %v", status, resp)
	}
}

// TestLocAPI_GeoJSON_UnclosedRing_Returns400: first position must
// equal the last (RFC 7946's own closure requirement).
func TestLocAPI_GeoJSON_UnclosedRing_Returns400(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:1",
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{0, 0}, {0, 10}, {10, 10}, {10, 0}}}, // does NOT close back to {0,0}
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("unclosed ring: want 400, got %d %v", status, resp)
	}
}

// TestLocAPI_GeoJSON_SelfIntersectingViaHTTP_Returns400 is the
// end-to-end HTTP proof of XOLU-LOC020, complementing the store-level
// test — confirming the handler's own error path, not just the
// store's.
func TestLocAPI_GeoJSON_SelfIntersectingViaHTTP_Returns400(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:1",
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{0, 0}, {10, 10}, {10, 0}, {0, 10}, {0, 0}}},
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("self-intersecting polygon via HTTP: want 400, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC020" {
		t.Errorf("want XOLU-LOC020, got %v", resp)
	}
}

// TestLocAPI_GeoJSON_UnknownGeometryType_Returns400: a "type" value
// that's neither "Polygon" nor "circle" must be refused cleanly.
func TestLocAPI_GeoJSON_UnknownGeometryType_Returns400(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject":  "zones:1",
		"geometry": map[string]interface{}{"type": "Hexagon", "coordinates": [][][2]float64{{{0, 0}, {1, 1}, {2, 2}, {0, 0}}}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("unknown geometry type: want 400, got %d %v", status, resp)
	}
}

// TestLocAPI_GeoJSON_WithHoles_Refused is the HTTP-level companion to
// TestRFC7946_Appendix_A3_WithHoles_Refused — RFC 7946's own worked
// "with holes" example (Appendix A.3), submitted through the real
// /fences/attach endpoint, must be refused with a clean 400, not
// silently accepted with the hole dropped.
func TestLocAPI_GeoJSON_WithHoles_Refused(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:1",
		"geometry": map[string]interface{}{
			"type": "Polygon",
			"coordinates": [][][2]float64{
				{{100.0, 0.0}, {101.0, 0.0}, {101.0, 1.0}, {100.0, 1.0}, {100.0, 0.0}},
				{{100.8, 0.8}, {100.8, 0.2}, {100.2, 0.2}, {100.2, 0.8}, {100.8, 0.8}},
			},
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("RFC 7946 Appendix A.3's own 'with holes' example via HTTP: want 400, got %d %v", status, resp)
	}
}

// TestLocAPI_GeoJSON_ThreeElementPosition_AltitudeSilentlyIgnored:
// RFC 7946 §3.1.1 permits an optional third (altitude) position
// element and explicitly says "additional elements MAY be ignored by
// parsers" — Go's own encoding/json, decoding a 3-element JSON array
// into this package's [2]float64 position type, does exactly that
// (documented behaviour: "If the Go array is smaller than the JSON
// array, the additional JSON array elements are discarded"),
// confirmed directly rather than assumed. This was initially written
// as a "must return 400" test on the assumption that Go would error
// on the size mismatch — it doesn't, and checking the actual
// behaviour caught that the test's own expectation was wrong, not
// the code: the RFC sanctions ignoring the extra element, so silent
// truncation here is correct, not a gap.
func TestLocAPI_GeoJSON_ThreeElementPosition_AltitudeSilentlyIgnored(t *testing.T) {
	env := newMetaServer(t)
	req, err := http.NewRequest("POST", locURL(env, "/fences/attach"), bytes.NewBufferString(
		`{"subject":"zones:1","geometry":{"type":"Polygon","coordinates":[[[0,0,10],[1,0,10],[1,1,10],[0,0,10]]]}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a 3-element (altitude-bearing) position: want 201 (altitude legitimately ignored per §3.1.1), got %d", resp.StatusCode)
	}
}

// many thousands of vertices — a DoS-shaped input — must either
// succeed within a bounded time or fail cleanly, never hang the
// handler or the self-intersection check (an O(n^2) algorithm, so
// this is exactly the shape adversarial size testing should target).
func TestLocAPI_GeoJSON_ExtremelyLargeRing_HandledSanely(t *testing.T) {
	env := newMetaServer(t)
	const n = 3000
	ring := make([][2]float64, 0, n+1)
	// A large, valid, non-self-intersecting near-circle — deliberately
	// NOT degenerate, so a slow response time is attributable to
	// vertex count alone, not early-exit validation refusing
	// something malformed.
	for i := 0; i < n; i++ {
		angle := float64(i) / float64(n) * 2 * math.Pi
		ring = append(ring, [2]float64{10 * math.Cos(angle), 10 * math.Sin(angle)})
	}
	ring = append(ring, ring[0]) // close it

	done := make(chan struct{})
	var status int
	var resp map[string]interface{}
	go func() {
		status, resp = doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
			"subject":  "zones:1",
			"geometry": map[string]interface{}{"type": "Polygon", "coordinates": [][][2]float64{ring}},
		})
		close(done)
	}()
	select {
	case <-done:
		if status == http.StatusInternalServerError {
			t.Fatalf("a %d-vertex ring must never produce a 500, got %d %v", n, status, resp)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("a %d-vertex ring did not complete within 20s — likely an O(n^2) blowup with no bound", n)
	}
}
