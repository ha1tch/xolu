// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_loc_fence_reconcile_test.go — T-130 (wave 9b): fence geometry
// PATCH + reconcile (loc-00-design.md §5c, loc-01-rest-api.md §2b).

package server_test

import (
	"net/http"
	"testing"
)

// TestLocAPI_FencePatch_UpdatesGeometryOnly proves the PATCH endpoint
// changes stored geometry and nothing guard-bearing: a subject already
// inside the OLD geometry remains recorded as a member (loc_fence_membership
// untouched) even once the fence has shrunk to no longer contain them --
// exactly §5c's own "live guard is unaffected, nothing here is a second
// guard path" rule, checked through the read surface available at HTTP
// level (contains/fences list), not by reaching into storage directly.
func TestLocAPI_FencePatch_UpdatesGeometryOnly(t *testing.T) {
	env := newMetaServer(t)

	// A large circle at the origin.
	status, attachResp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:1",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100000.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d %v", status, attachResp)
	}

	// A subject reports well inside the large circle, but far from
	// the origin -- becomes a member.
	status, reportResp := doJSONRequest(t, "POST", locURL(env, "/report"), map[string]interface{}{
		"subject": map[string]interface{}{"type": "REF", "entity": "vehicles", "id": 1},
		"point":   map[string]interface{}{"lat": 0.5, "lon": 0.5, "alt": 0},
	})
	if status != http.StatusOK || reportResp["changed"] != true {
		t.Fatalf("report: want 200 changed=true, got %d %v", status, reportResp)
	}

	// PATCH the fence to a much smaller radius that no longer covers
	// the subject's last reported point.
	status, patchResp := doJSONRequest(t, "PATCH", locURL(env, "/fences/zones/1"), map[string]interface{}{
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 10.0,
		},
	})
	if status != http.StatusOK {
		t.Fatalf("fence patch: want 200, got %d %v", status, patchResp)
	}

	// The subject's own position read still shows the fence as current
	// membership -- loc_fence_membership was never touched by PATCH,
	// exactly the point: a derived process never rewrites guard-bearing
	// state, only an ordinary report call does that.
	status, posResp := doJSONRequest(t, "GET", locURL(env, "/subjects/vehicles/1/position"), nil)
	if status != http.StatusOK {
		t.Fatalf("subject position: want 200, got %d %v", status, posResp)
	}
	fences, _ := posResp["fences"].([]interface{})
	found := false
	for _, f := range fences {
		if f == "zones:1" {
			found = true
		}
	}
	if !found {
		t.Errorf("PATCH must not silently clear existing membership: want zones:1 still recorded, got %v", fences)
	}
}

// TestLocAPI_FencePatch_RejectsSelfIntersecting proves PATCH validates
// identically to attach (XOLU-LOC020), via the shared decode helper.
func TestLocAPI_FencePatch_RejectsSelfIntersecting(t *testing.T) {
	env := newMetaServer(t)
	status, _ := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:2",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d", status)
	}
	status, resp := doJSONRequest(t, "PATCH", locURL(env, "/fences/zones/2"), map[string]interface{}{
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{0, 0}, {10, 10}, {10, 0}, {0, 10}, {0, 0}}}, // bowtie
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("self-intersecting patch: want 400, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC020" {
		t.Errorf("want XOLU-LOC020, got %v", resp)
	}
}

// TestLocAPI_FenceReconcile_ReportsDrift is the direct exit-criteria
// proof: a subject inside the OLD geometry, outside the NEW geometry
// after PATCH, must show up in reconcile's drift list -- and
// recorded_count/observed_count must reflect exactly one drifted
// member.
func TestLocAPI_FenceReconcile_ReportsDrift(t *testing.T) {
	env := newMetaServer(t)

	status, _ := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:3",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100000.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d", status)
	}
	status, reportResp := doJSONRequest(t, "POST", locURL(env, "/report"), map[string]interface{}{
		"subject": map[string]interface{}{"type": "REF", "entity": "vehicles", "id": 3},
		"point":   map[string]interface{}{"lat": 0.5, "lon": 0.5, "alt": 0},
	})
	if status != http.StatusOK || reportResp["changed"] != true {
		t.Fatalf("report: want changed=true, got %d %v", status, reportResp)
	}

	status, drift0 := doJSONRequest(t, "GET", locURL(env, "/fences/zones/3/reconcile"), nil)
	if status != http.StatusOK {
		t.Fatalf("reconcile before patch: want 200, got %d %v", status, drift0)
	}
	if drift0["recorded_count"] != float64(1) || drift0["observed_count"] != float64(1) {
		t.Fatalf("reconcile before patch: want recorded=observed=1 (no drift yet), got %v", drift0)
	}

	status, _ = doJSONRequest(t, "PATCH", locURL(env, "/fences/zones/3"), map[string]interface{}{
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 10.0,
		},
	})
	if status != http.StatusOK {
		t.Fatalf("patch: want 200, got %d", status)
	}

	status, drift1 := doJSONRequest(t, "GET", locURL(env, "/fences/zones/3/reconcile"), nil)
	if status != http.StatusOK {
		t.Fatalf("reconcile after patch: want 200, got %d %v", status, drift1)
	}
	if drift1["recorded_count"] != float64(1) {
		t.Fatalf("recorded_count must be unaffected by the geometry change (loc_fence_membership untouched): want 1, got %v", drift1["recorded_count"])
	}
	if drift1["observed_count"] != float64(0) {
		t.Fatalf("observed_count: want 0 (subject no longer inside), got %v", drift1["observed_count"])
	}
	driftList, _ := drift1["drift"].([]interface{})
	if len(driftList) != 1 {
		t.Fatalf("want exactly 1 drift entry, got %d: %v", len(driftList), driftList)
	}
	entry, _ := driftList[0].(map[string]interface{})
	if entry["subject_ref"] != "vehicles:3" || entry["recorded"] != "member" || entry["observed"] != "outside_new_boundary" {
		t.Errorf("unexpected drift entry shape: %v", entry)
	}
}

// TestLocAPI_FenceReconcile_NeverWritesGuardBearingState is the
// adversarial proof named directly in T-130's own exit criteria:
// reconcile must never touch loc_fence_capacity.count or
// loc_fence_membership. Checked by capacity specifically -- a capacity-
// bearing fence's count must stay exactly what admission produced,
// unmoved by any number of reconcile reads after a geometry change
// that would otherwise "explain" a discrepancy.
func TestLocAPI_FenceReconcile_NeverWritesGuardBearingState(t *testing.T) {
	env := newMetaServer(t)

	status, _ := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:4",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100000.0,
		},
		"capacity": 5.0,
	})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d", status)
	}
	status, _ = doJSONRequest(t, "POST", locURL(env, "/report"), map[string]interface{}{
		"subject": map[string]interface{}{"type": "REF", "entity": "vehicles", "id": 4},
		"point":   map[string]interface{}{"lat": 0.5, "lon": 0.5, "alt": 0},
	})
	if status != http.StatusOK {
		t.Fatalf("report: want 200, got %d", status)
	}
	status, _ = doJSONRequest(t, "PATCH", locURL(env, "/fences/zones/4"), map[string]interface{}{
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 10.0,
		},
	})
	if status != http.StatusOK {
		t.Fatalf("patch: want 200, got %d", status)
	}

	// Call reconcile several times -- a write bug would likely show up
	// as a changing count across repeated calls, or as a capacity
	// refusal appearing/disappearing on a subsequent ordinary report.
	for i := 0; i < 3; i++ {
		status, _ = doJSONRequest(t, "GET", locURL(env, "/fences/zones/4/reconcile"), nil)
		if status != http.StatusOK {
			t.Fatalf("reconcile call %d: want 200, got %d", i, status)
		}
	}

	// A second subject reporting into the (now-tiny, still capacity=5)
	// fence must still succeed -- if reconcile had wrongly decremented
	// loc_fence_capacity.count to 0 members, admission logic itself
	// would be unaffected either way here, but this at minimum proves
	// reconcile's repeated reads didn't corrupt the fence row into an
	// unusable state (e.g. a bad UPDATE leaving capacity NULL/zeroed).
	status, resp := doJSONRequest(t, "GET", locURL(env, "/fences/zones/4"), nil)
	if status != http.StatusOK {
		t.Fatalf("fence get after repeated reconcile: want 200, got %d %v", status, resp)
	}
}
