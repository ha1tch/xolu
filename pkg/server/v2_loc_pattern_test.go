// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_loc_pattern_test.go — T-131 (wave 9b): fence-type patterns
// (loc-00-design.md §5d, loc-01-rest-api.md §2a).

package server_test

import (
	"net/http"
	"testing"
)

func TestLocAPI_Pattern_DefGetListDelete(t *testing.T) {
	env := newMetaServer(t)

	status, defResp := doJSONRequest(t, "POST", locURL(env, "/patterns/def"), map[string]interface{}{
		"name": "loading-dock-std", "capacity": 3.0,
	})
	if status != http.StatusCreated {
		t.Fatalf("pattern def: want 201, got %d %v", status, defResp)
	}
	if defResp["name"] != "loading-dock-std" || defResp["capacity"] != float64(3) {
		t.Errorf("unexpected def response: %v", defResp)
	}

	status, getResp := doJSONRequest(t, "GET", locURL(env, "/patterns/loading-dock-std"), nil)
	if status != http.StatusOK {
		t.Fatalf("pattern get: want 200, got %d %v", status, getResp)
	}
	if getResp["capacity"] != float64(3) {
		t.Errorf("pattern get: want capacity 3, got %v", getResp["capacity"])
	}

	status, listResp := doJSONRequest(t, "GET", locURL(env, "/patterns/list"), nil)
	if status != http.StatusOK {
		t.Fatalf("pattern list: want 200, got %d %v", status, listResp)
	}
	patterns, _ := listResp["patterns"].([]interface{})
	if len(patterns) != 1 {
		t.Fatalf("pattern list: want 1, got %d", len(patterns))
	}

	status, _ = doJSONRequest(t, "DELETE", locURL(env, "/patterns/loading-dock-std"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("pattern delete: want 204, got %d", status)
	}

	status, resp := doJSONRequest(t, "GET", locURL(env, "/patterns/loading-dock-std"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("pattern get after delete: want 404, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC024" {
		t.Errorf("want XOLU-LOC024, got %v", resp)
	}
}

func TestLocAPI_Pattern_DuplicateReturns409(t *testing.T) {
	env := newMetaServer(t)
	status, _ := doJSONRequest(t, "POST", locURL(env, "/patterns/def"), map[string]interface{}{
		"name": "dup-pattern", "capacity": 5.0,
	})
	if status != http.StatusCreated {
		t.Fatalf("first def: want 201, got %d", status)
	}
	status, resp := doJSONRequest(t, "POST", locURL(env, "/patterns/def"), map[string]interface{}{
		"name": "dup-pattern", "capacity": 5.0,
	})
	if status != http.StatusConflict {
		t.Fatalf("duplicate pattern: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC023" {
		t.Errorf("want XOLU-LOC023, got %v", resp)
	}
}

// TestLocAPI_LocationDef_WithPattern_ClonesCapacity is the direct
// exit-criteria proof for locations: a location def'd with a pattern
// clones that pattern's capacity at creation time.
func TestLocAPI_LocationDef_WithPattern_ClonesCapacity(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", locURL(env, "/patterns/def"), map[string]interface{}{
		"name": "bin-std", "capacity": 7.0,
	})

	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "pat-bin", "parent_id": nil, "name": "pat-bin", "postable": true,
		"pattern": "bin-std",
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 0, "lon": 0, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("def with pattern: want 201, got %d %v", status, resp)
	}
	if resp["capacity"] != float64(7) {
		t.Errorf("want cloned capacity 7, got %v", resp["capacity"])
	}
	if resp["pattern"] != "bin-std" || resp["pattern_id"] != "bin-std" {
		t.Errorf("want pattern/pattern_id echoed as bin-std, got pattern=%v pattern_id=%v", resp["pattern"], resp["pattern_id"])
	}
	if resp["pattern_deleted"] != false {
		t.Errorf("want pattern_deleted=false while the pattern still exists, got %v", resp["pattern_deleted"])
	}
}

// TestLocAPI_FenceAttach_WithPattern_ClonesCapacity is the fence-shaped
// twin of the location proof above.
func TestLocAPI_FenceAttach_WithPattern_ClonesCapacity(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", locURL(env, "/patterns/def"), map[string]interface{}{
		"name": "dock-std", "capacity": 3.0,
	})

	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:10", "pattern": "dock-std",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach with pattern: want 201, got %d %v", status, resp)
	}
	if resp["capacity"] != float64(3) {
		t.Errorf("want cloned capacity 3, got %v", resp["capacity"])
	}
	if resp["pattern"] != "dock-std" {
		t.Errorf("want pattern echoed as dock-std, got %v", resp["pattern"])
	}

	// GET independently confirms the same lineage, not just the
	// attach response's own echo.
	status, getResp := doJSONRequest(t, "GET", locURL(env, "/fences/zones/10"), nil)
	if status != http.StatusOK {
		t.Fatalf("fence get: want 200, got %d %v", status, getResp)
	}
	if getResp["pattern_id"] != "dock-std" || getResp["pattern_deleted"] != false {
		t.Errorf("fence get: want pattern_id=dock-std pattern_deleted=false, got %v", getResp)
	}
}

// TestLocAPI_PatternDeleted_LeavesClonesIntact is the second named
// exit criterion: deleting the source pattern leaves existing clones
// intact (their own snapshotted capacity untouched) with
// pattern_deleted: true on their next read.
func TestLocAPI_PatternDeleted_LeavesClonesIntact(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", locURL(env, "/patterns/def"), map[string]interface{}{
		"name": "ephemeral", "capacity": 4.0,
	})
	status, _ := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:11", "pattern": "ephemeral",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d", status)
	}

	status, _ = doJSONRequest(t, "DELETE", locURL(env, "/patterns/ephemeral"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("pattern delete: want 204, got %d", status)
	}

	status, resp := doJSONRequest(t, "GET", locURL(env, "/fences/zones/11"), nil)
	if status != http.StatusOK {
		t.Fatalf("fence get after pattern delete: want 200, got %d %v", status, resp)
	}
	if resp["pattern_id"] != "ephemeral" {
		t.Errorf("clone must keep its own lineage pointer even after the source is gone: want pattern_id=ephemeral, got %v", resp["pattern_id"])
	}
	if resp["pattern_deleted"] != true {
		t.Errorf("want pattern_deleted=true once the source pattern is gone, got %v", resp["pattern_deleted"])
	}
}

// TestLocAPI_PatternAndCapacity_Conflict is the third named exit
// criterion: XOLU-LOC022 refuses both capacity and pattern set
// together, for both locations and fences.
func TestLocAPI_PatternAndCapacity_Conflict(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", locURL(env, "/patterns/def"), map[string]interface{}{
		"name": "conflict-pattern", "capacity": 2.0,
	})

	t.Run("location def", func(t *testing.T) {
		status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
			"location_id": "conflict-loc", "parent_id": nil, "name": "x", "postable": true,
			"capacity": 5.0, "pattern": "conflict-pattern",
			"placement": map[string]interface{}{"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0},
		})
		if status != http.StatusBadRequest {
			t.Fatalf("want 400, got %d %v", status, resp)
		}
		errObj, ok := resp["error"].(map[string]interface{})
		if !ok || errObj["code"] != "XOLU-LOC022" {
			t.Errorf("want XOLU-LOC022, got %v", resp)
		}
	})

	t.Run("fence attach", func(t *testing.T) {
		status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
			"subject": "zones:12", "capacity": 5.0, "pattern": "conflict-pattern",
			"geometry": map[string]interface{}{
				"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
			},
		})
		if status != http.StatusBadRequest {
			t.Fatalf("want 400, got %d %v", status, resp)
		}
		errObj, ok := resp["error"].(map[string]interface{})
		if !ok || errObj["code"] != "XOLU-LOC022" {
			t.Errorf("want XOLU-LOC022, got %v", resp)
		}
	})
}

// TestLocAPI_Pattern_UnknownReference_Returns404 covers referencing a
// nonexistent pattern at attach/def time.
func TestLocAPI_Pattern_UnknownReference_Returns404(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:13", "pattern": "does-not-exist",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
		},
	})
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC024" {
		t.Errorf("want XOLU-LOC024, got %v", resp)
	}
}
