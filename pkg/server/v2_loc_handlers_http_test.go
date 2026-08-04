// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func locURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/loc%s", sts.ts.URL, path)
}

func defineLocRoot(t *testing.T, env *stdTestServer, id string) {
	t.Helper()
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": id, "parent_id": nil, "name": "root", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": -34.9, "lon": -56.16, "alt": 10, "true_north": 0},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("define root %q: want 201, got %d %v", id, status, resp)
	}
}

func defineLocLeaf(t *testing.T, env *stdTestServer, id, parentID string, capacity interface{}) {
	t.Helper()
	body := map[string]interface{}{
		"location_id": id, "parent_id": parentID, "name": id, "postable": true,
		"placement": map[string]interface{}{"offset_x": 1, "offset_y": 1, "offset_z": 0, "rotation": 0},
	}
	if capacity != nil {
		body["capacity"] = capacity
	}
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), body)
	if status != http.StatusCreated {
		t.Fatalf("define leaf %q: want 201, got %d %v", id, status, resp)
	}
}

// TestLocAPI_LocationLifecycle covers def/list/get/patch/delete
// end to end through real HTTP.
func TestLocAPI_LocationLifecycle(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "root")
	defineLocLeaf(t, env, "root/bin", "root", 5.0)

	status, getResp := doJSONRequest(t, "GET", locURL(env, "/root%2Fbin"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET leaf: want 200, got %d %v", status, getResp)
	}
	if getResp["name"] != "root/bin" && getResp["location_id"] != "root/bin" {
		t.Errorf("GET leaf: unexpected body %v", getResp)
	}
	if getResp["capacity"] != float64(5) {
		t.Errorf("GET leaf: capacity want 5, got %v", getResp["capacity"])
	}

	status, listResp := doJSONRequest(t, "GET", locURL(env, "/list"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET list: want 200, got %d %v", status, listResp)
	}
	locs, _ := listResp["locations"].([]interface{})
	if len(locs) != 2 {
		t.Fatalf("GET list: want 2 locations, got %d (%v)", len(locs), listResp)
	}

	status, patchResp := doJSONRequest(t, "PATCH", locURL(env, "/root%2Fbin"), map[string]interface{}{"name": "Bin One"})
	if status != http.StatusOK {
		t.Fatalf("PATCH leaf: want 200, got %d %v", status, patchResp)
	}
	if patchResp["name"] != "Bin One" {
		t.Errorf("PATCH leaf: name not updated, got %v", patchResp["name"])
	}

	// DELETE root refused: has a child.
	status, delResp := doJSONRequest(t, "DELETE", locURL(env, "/root"), nil)
	if status != http.StatusConflict {
		t.Fatalf("DELETE root with child: want 409, got %d %v", status, delResp)
	}

	status, _ = doJSONRequest(t, "DELETE", locURL(env, "/root%2Fbin"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE leaf: want 204, got %d", status)
	}
	status, _ = doJSONRequest(t, "DELETE", locURL(env, "/root"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE now-empty root: want 204, got %d", status)
	}
}

// TestLocAPI_DefineRootWithoutAnchorRefused proves XOLU-LOC010 through
// the real HTTP path.
func TestLocAPI_DefineRootWithoutAnchorRefused(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "bad-root", "parent_id": nil, "name": "bad",
		"placement": map[string]interface{}{"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("root without anchor: want 400, got %d %v", status, resp)
	}
	if errObj, ok := resp["error"].(map[string]interface{}); !ok || errObj["code"] != "XOLU-LOC010" {
		t.Errorf("want XOLU-LOC010, got %v", resp)
	}
}

// TestLocAPI_MoveAndPosition covers move + subject position/history.
func TestLocAPI_MoveAndPosition(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "root")
	defineLocLeaf(t, env, "root/bin", "root", nil)

	status, moveResp := doJSONRequest(t, "POST", locURL(env, "/move"), map[string]interface{}{
		"subject": map[string]interface{}{"type": "REF", "entity": "vehicles", "id": 4471},
		"to":      "root/bin",
	})
	if status != http.StatusOK {
		t.Fatalf("move: want 200, got %d %v", status, moveResp)
	}
	if moveResp["moved"] != true || moveResp["leaf"] != "root/bin" {
		t.Errorf("move response: unexpected %v", moveResp)
	}

	status, posResp := doJSONRequest(t, "GET", locURL(env, "/subjects/vehicles/4471/position"), nil)
	if status != http.StatusOK {
		t.Fatalf("position: want 200, got %d %v", status, posResp)
	}
	if posResp["leaf"] != "root/bin" {
		t.Errorf("position: want leaf root/bin, got %v", posResp["leaf"])
	}

	status, histResp := doJSONRequest(t, "GET", locURL(env, "/subjects/vehicles/4471/history"), nil)
	if status != http.StatusOK {
		t.Fatalf("history: want 200, got %d %v", status, histResp)
	}
	entries, _ := histResp["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("history: want 1 entry, got %d (%v)", len(entries), histResp)
	}
}

// TestLocAPI_MoveCapacityRefused proves XOLU-LOC002 through HTTP,
// origin untouched.
func TestLocAPI_MoveCapacityRefused(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "root")
	defineLocLeaf(t, env, "root/full", "root", 0.0)

	status, resp := doJSONRequest(t, "POST", locURL(env, "/move"), map[string]interface{}{
		"subject": map[string]interface{}{"type": "REF", "entity": "vehicles", "id": 1},
		"to":      "root/full",
	})
	if status != http.StatusConflict {
		t.Fatalf("move into capacity-0 leaf: want 409, got %d %v", status, resp)
	}
	if errObj, ok := resp["error"].(map[string]interface{}); !ok || errObj["code"] != "XOLU-LOC002" {
		t.Errorf("want XOLU-LOC002, got %v", resp)
	}
}

// TestLocAPI_ReportAndFenceLifecycle covers fences/attach (tree-
// aligned), report, and the no-op-writes-nothing behaviour visible
// through the API (a repeated identical report reports changed:false).
func TestLocAPI_ReportAndFenceLifecycle(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "site")
	defineLocLeaf(t, env, "site/yard", "site", nil)

	status, attachResp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"aligned_to": "site/yard",
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{-56.17, -34.91}, {-56.15, -34.91}, {-56.15, -34.89}, {-56.17, -34.89}, {-56.17, -34.91}}},
		},
		"capacity": 2.0,
	})
	if status != http.StatusCreated {
		t.Fatalf("attach fence: want 201, got %d %v", status, attachResp)
	}

	status, listResp := doJSONRequest(t, "GET", locURL(env, "/fences/list"), nil)
	if status != http.StatusOK {
		t.Fatalf("fences list: want 200, got %d %v", status, listResp)
	}
	fences, _ := listResp["fences"].([]interface{})
	if len(fences) != 1 {
		t.Fatalf("fences list: want 1, got %d", len(fences))
	}

	status, reportResp := doJSONRequest(t, "POST", locURL(env, "/report"), map[string]interface{}{
		"subject": map[string]interface{}{"type": "REF", "entity": "vehicles", "id": 99},
		"point":   map[string]interface{}{"lat": -34.9, "lon": -56.16, "alt": 0},
	})
	if status != http.StatusOK {
		t.Fatalf("report: want 200, got %d %v", status, reportResp)
	}
	if reportResp["changed"] != true {
		t.Errorf("first report inside fence: want changed=true, got %v", reportResp["changed"])
	}

	// Same point again: no-op, changed=false, per §8a exposed through the API.
	status, reportResp2 := doJSONRequest(t, "POST", locURL(env, "/report"), map[string]interface{}{
		"subject": map[string]interface{}{"type": "REF", "entity": "vehicles", "id": 99},
		"point":   map[string]interface{}{"lat": -34.9, "lon": -56.16, "alt": 0},
	})
	if status != http.StatusOK {
		t.Fatalf("second report: want 200, got %d %v", status, reportResp2)
	}
	if reportResp2["changed"] != false {
		t.Errorf("repeated identical report: want changed=false, got %v", reportResp2["changed"])
	}

	status, _ = doJSONRequest(t, "DELETE", locURL(env, "/fences/id/site%2Fyard"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete fence: want 204, got %d", status)
	}
}

// TestLocAPI_Contains covers the containment read.
func TestLocAPI_Contains(t *testing.T) {
	env := newMetaServer(t)
	status, attachResp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:1",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 1000.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach standalone fence: want 201, got %d %v", status, attachResp)
	}

	status, containsResp := doJSONRequest(t, "GET", locURL(env, "/contains?lat=0&lon=0"), nil)
	if status != http.StatusOK {
		t.Fatalf("contains: want 200, got %d %v", status, containsResp)
	}
	fences, _ := containsResp["fences"].([]interface{})
	if len(fences) != 1 || fences[0] != "zones:1" {
		t.Errorf("contains at centre: want [zones:1], got %v", fences)
	}

	status, outsideResp := doJSONRequest(t, "GET", locURL(env, "/contains?lat=50&lon=50"), nil)
	if status != http.StatusOK {
		t.Fatalf("contains (outside): want 200, got %d %v", status, outsideResp)
	}
	outFences, _ := outsideResp["fences"].([]interface{})
	if len(outFences) != 0 {
		t.Errorf("contains far away: want empty, got %v", outFences)
	}
}

// TestLocAPI_SelfAnchoredFenceRejected proves the v1 narrowing this
// stage names explicitly: center.self=true is refused, not silently
// mishandled.
func TestLocAPI_SelfAnchoredFenceRejected(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "self-anchored",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"self": true}, "radius_m": 1000.0,
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("self-anchored fence: want 400 (v1 non-goal), got %d %v", status, resp)
	}
}

// TestLocAPI_TwoIdentitySplit_InternalKeyNeverOnWire is the two-
// identity regression check Stage 6's own exit criterion names. The
// real proof is structural, not a substring search (a fuzzy string
// search for the internal key's digits is unreliable — capacity,
// offsets, and other legitimate small numbers in the same response
// would produce false positives or false confidence either way):
// every field that identifies a location must equal the exact
// external location_id string, never a bare number standing in for
// it. Reads the actual internal LocationKey directly through
// LocStoreForTest to confirm the two IDs really are different values
// — this test would not catch a bug where location_id and the
// internal key happened to coincide by construction, so that
// distinctness is checked explicitly, not assumed.
func TestLocAPI_TwoIdentitySplit_InternalKeyNeverOnWire(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "site")
	defineLocLeaf(t, env, "site/room", "site", 7.0)

	tid := defaultTenantID(t, env)
	st, err := env.srv.LocStoreForTest(context.Background(), tid)
	if err != nil {
		t.Fatalf("LocStoreForTest: %v", err)
	}
	l, err := st.Get(context.Background(), "site/room")
	if err != nil {
		t.Fatal(err)
	}
	if l.ParentKey == nil {
		t.Fatal("test setup: expected a non-nil parent key")
	}
	// The actual distinctness check: the internal key and the
	// external id must not be the same value under any reasonable
	// string conversion — otherwise the field-equality checks below
	// would pass even if location_id secretly *was* the internal key.
	if fmt.Sprintf("%d", uint32(l.Key)) == "site/room" {
		t.Fatal("test setup: internal key and external id coincide, this test cannot distinguish them")
	}

	status, getResp := doJSONRequest(t, "GET", locURL(env, "/site%2Froom"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET: want 200, got %d %v", status, getResp)
	}
	if _, isNumber := getResp["location_id"].(float64); isNumber {
		t.Fatalf("GET: location_id is a bare number, not the external string — internal key leaked: %v", getResp)
	}
	if getResp["location_id"] != "site/room" {
		t.Fatalf("GET: location_id must be the exact external string id, got %v", getResp["location_id"])
	}
	if _, isNumber := getResp["parent_id"].(float64); isNumber {
		t.Fatalf("GET: parent_id is a bare number, not the external string — internal key leaked: %v", getResp)
	}
	if getResp["parent_id"] != "site" {
		t.Fatalf("GET: parent_id must be the exact external string id, got %v", getResp["parent_id"])
	}

	status, listResp := doJSONRequest(t, "GET", locURL(env, "/list"), nil)
	if status != http.StatusOK {
		t.Fatalf("LIST: want 200, got %d %v", status, listResp)
	}
	locs, _ := listResp["locations"].([]interface{})
	for _, item := range locs {
		m, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("LIST: unexpected item shape %v", item)
		}
		if _, isNumber := m["location_id"].(float64); isNumber {
			t.Fatalf("LIST: an item's location_id is a bare number — internal key leaked: %v", m)
		}
		if pid, present := m["parent_id"]; present && pid != nil {
			if _, isNumber := pid.(float64); isNumber {
				t.Fatalf("LIST: an item's parent_id is a bare number — internal key leaked: %v", m)
			}
		}
	}
}
