// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_loc_warnings_test.go — T-132 (wave 9b): populating loc-01-rest-
// api.md's warnings field for degenerate-polygon fences (§2) and
// mixed-CRS-anchor locations (§1). Never a hard refusal, either case.

package server_test

import (
	"net/http"
	"testing"
)

// TestLocAPI_FenceAttach_DegeneratePolygon_Warns covers a polygon
// collapsed to a line (three collinear points, effectively zero
// area) -- must still succeed (201), with a non-empty warnings array.
func TestLocAPI_FenceAttach_DegeneratePolygon_Warns(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:20",
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{0, 0}, {1, 1}, {2, 2}, {0, 0}}}, // collinear -- zero area
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("degenerate polygon attach: want 201 (legal, just useless), got %d %v", status, resp)
	}
	warnings, _ := resp["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("want a non-empty warnings array for a degenerate polygon, got %v", resp["warnings"])
	}
}

// TestLocAPI_FenceAttach_OrdinaryPolygon_NoWarning is the negative
// case: a normal, non-degenerate fence must not carry a warnings key
// at all.
func TestLocAPI_FenceAttach_OrdinaryPolygon_NoWarning(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:21",
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{0, 0}, {0, 10}, {10, 10}, {10, 0}, {0, 0}}},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("ordinary polygon attach: want 201, got %d %v", status, resp)
	}
	if _, present := resp["warnings"]; present {
		t.Errorf("want no warnings key for a normal fence, got %v", resp["warnings"])
	}
}

// TestLocAPI_FencePatch_DegeneratePolygon_Warns is the PATCH-side
// twin -- a fence PATCHed to a degenerate shape warns too, not just
// attach.
func TestLocAPI_FencePatch_DegeneratePolygon_Warns(t *testing.T) {
	env := newMetaServer(t)
	status, _ := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:22",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d", status)
	}
	status, resp := doJSONRequest(t, "PATCH", locURL(env, "/fences/zones/22"), map[string]interface{}{
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{5, 5}, {5, 5}, {5, 5}, {5, 5}}}, // all coincident -- degenerate
		},
	})
	if status != http.StatusOK {
		t.Fatalf("patch to degenerate polygon: want 200, got %d %v", status, resp)
	}
	warnings, _ := resp["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("want a non-empty warnings array, got %v", resp["warnings"])
	}
}

// TestLocAPI_LocationDef_MixedAnchor_Warns is the direct exit-
// criteria proof for locations: a child location anchored far from
// its already-anchored ancestor must still succeed, with a warning.
func TestLocAPI_LocationDef_MixedAnchor_Warns(t *testing.T) {
	env := newMetaServer(t)
	// Root anchored in Montevideo.
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "mix-root", "parent_id": nil, "name": "root", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": -34.9, "lon": -56.16, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("root def: want 201, got %d %v", status, resp)
	}

	// Child anchored in Tokyo -- ~18,000km away, well past the 500km
	// plausibility threshold.
	status, resp = doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "mix-root/child", "parent_id": "mix-root", "name": "child", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 35.68, "lon": 139.65, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("mixed-anchor child def: want 201 (legal, just worth flagging), got %d %v", status, resp)
	}
	warnings, _ := resp["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("want a non-empty warnings array for a far-mismatched anchor, got %v", resp["warnings"])
	}
}

// TestLocAPI_LocationDef_NearbyAnchor_NoWarning is the negative case:
// two anchors within ordinary facility-tree distance of each other
// must not warn.
func TestLocAPI_LocationDef_NearbyAnchor_NoWarning(t *testing.T) {
	env := newMetaServer(t)
	status, _ := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "near-root", "parent_id": nil, "name": "root", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": -34.90, "lon": -56.16, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("root def: want 201, got %d", status)
	}
	// A second site a few km away within the same metro area.
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "near-root/site2", "parent_id": "near-root", "name": "site2", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": -34.92, "lon": -56.18, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("nearby-anchor child def: want 201, got %d %v", status, resp)
	}
	if _, present := resp["warnings"]; present {
		t.Errorf("want no warnings for a nearby anchor, got %v", resp["warnings"])
	}
}

// TestLocAPI_LocationPatch_MixedAnchor_Warns is the PATCH-side twin.
func TestLocAPI_LocationPatch_MixedAnchor_Warns(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "patch-root", "parent_id": nil, "name": "root", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": -34.9, "lon": -56.16, "alt": 0, "true_north": 0},
		},
	})
	status, _ := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "patch-root/child", "parent_id": "patch-root", "name": "child", "postable": false,
		"placement": map[string]interface{}{"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0},
	})
	if status != http.StatusCreated {
		t.Fatalf("child def (no anchor of its own): want 201, got %d", status)
	}

	// PATCH the child to carry its own, wildly distant anchor.
	status, resp := doJSONRequest(t, "PATCH", locURL(env, "/patch-root%2Fchild"), map[string]interface{}{
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 35.68, "lon": 139.65, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("mixed-anchor patch: want 200, got %d %v", status, resp)
	}
	warnings, _ := resp["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatalf("want a non-empty warnings array, got %v", resp["warnings"])
	}
}

// TestLocAPI_LocationPatch_NonAnchorField_NeverTriggersMixedAnchorCheck
// proves the check is scoped to anchor-touching patches only -- a
// name-only patch on a location with a genuinely mismatched anchor
// tree must not spuriously warn every time, since nothing about the
// anchor changed in this specific request.
func TestLocAPI_LocationPatch_NonAnchorField_NeverTriggersMixedAnchorCheck(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "quiet-root", "parent_id": nil, "name": "root", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": -34.9, "lon": -56.16, "alt": 0, "true_north": 0},
		},
	})
	doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "quiet-root/child", "parent_id": "quiet-root", "name": "child", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 35.68, "lon": 139.65, "alt": 0, "true_north": 0},
		},
	})
	status, resp := doJSONRequest(t, "PATCH", locURL(env, "/quiet-root%2Fchild"), map[string]interface{}{
		"name": "renamed child",
	})
	if status != http.StatusOK {
		t.Fatalf("name-only patch: want 200, got %d %v", status, resp)
	}
	if _, present := resp["warnings"]; present {
		t.Errorf("a name-only patch must not re-trigger the mixed-anchor check: got %v", resp["warnings"])
	}
}
