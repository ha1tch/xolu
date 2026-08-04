// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_obj_handlers_http_test.go — T-119 (wave 10), Stage 0-1's own
// exit criterion, at HTTP level: full round trip (attach -> move to a
// loc_leaf -> resolve -> detach) green under -race.

package server_test

import (
	"net/http"
	"testing"
)

func objURL(sts *stdTestServer, path string) string {
	return sts.ts.URL + "/api/v2/tenant/default/obj" + path
}

func TestObjAPI_Attach_GetRoundTrip(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{
		"subject":  "vehicles:47",
		"capacity": map[string]interface{}{"max_weight_kg": 12000.0, "max_volume_m3": 40.0},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d %v", status, resp)
	}
	if resp["subject"] != "vehicles:47" {
		t.Errorf("subject: got %v", resp["subject"])
	}
	if resp["position_kind"] != "" {
		t.Errorf("a freshly-attached subject must start unassigned, got position_kind=%v", resp["position_kind"])
	}

	status, getResp := doJSONRequest(t, "GET", objURL(env, "/vehicles/47"), nil)
	if status != http.StatusOK {
		t.Fatalf("get: want 200, got %d %v", status, getResp)
	}
	capOut, _ := getResp["capacity"].(map[string]interface{})
	if capOut["max_weight_kg"] != 12000.0 {
		t.Errorf("capacity not round-tripped: got %v", getResp["capacity"])
	}
}

func TestObjAPI_Attach_Duplicate_Returns409(t *testing.T) {
	env := newMetaServer(t)
	status, _ := doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:1"})
	if status != http.StatusCreated {
		t.Fatalf("first attach: want 201, got %d", status)
	}
	status, resp := doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:1"})
	if status != http.StatusConflict {
		t.Fatalf("duplicate attach: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ006" {
		t.Errorf("want XOLU-OBJ006, got %v", resp)
	}
}

func TestObjAPI_Get_NeverAttached_Returns404(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "GET", objURL(env, "/vehicles/999"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ001" {
		t.Errorf("want XOLU-OBJ001, got %v", resp)
	}
}

// TestObjAPI_FullRoundTrip_AttachMoveResolveDetach is T-119's own
// filed exit criterion, verbatim: attach -> move to a loc_leaf ->
// resolve -> detach, all at HTTP level. Detach only succeeds once
// unassigned again (XOLU-OBJ007 otherwise), so this round trip moves
// to the leaf, resolves, moves back to unassigned, then detaches --
// exercising every verb for real rather than glossing over the
// refusal detach-while-positioned would otherwise produce.
func TestObjAPI_FullRoundTrip_AttachMoveResolveDetach(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "obj-root")
	defineLocLeaf(t, env, "obj-root/bay", "obj-root", nil)

	status, attachResp := doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:10"})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d %v", status, attachResp)
	}

	status, moveResp := doJSONRequest(t, "PUT", objURL(env, "/vehicles/10/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "loc_leaf", "location_id": "obj-root/bay"},
	})
	if status != http.StatusOK {
		t.Fatalf("move to loc_leaf: want 200, got %d %v", status, moveResp)
	}
	if moveResp["position_kind"] != "loc_leaf" || moveResp["loc_leaf_id"] != "obj-root/bay" {
		t.Errorf("move response: got %v", moveResp)
	}

	status, posResp := doJSONRequest(t, "GET", objURL(env, "/vehicles/10/position"), nil)
	if status != http.StatusOK {
		t.Fatalf("position: want 200, got %d %v", status, posResp)
	}
	resolved, _ := posResp["resolved"].(map[string]interface{})
	if resolved["kind"] != "loc_leaf" || resolved["location_id"] != "obj-root/bay" {
		t.Errorf("resolved position: got %v", posResp)
	}
	if posResp["as_of"] != "live" {
		t.Errorf("as_of: want \"live\", got %v", posResp["as_of"])
	}

	// Move back to unassigned so detach can succeed.
	status, unassignResp := doJSONRequest(t, "PUT", objURL(env, "/vehicles/10/move"), map[string]interface{}{"to": nil})
	if status != http.StatusOK {
		t.Fatalf("move to unassigned: want 200, got %d %v", status, unassignResp)
	}
	if unassignResp["position_kind"] != "" {
		t.Errorf("want position_kind unassigned, got %v", unassignResp["position_kind"])
	}

	status, _ = doJSONRequest(t, "DELETE", objURL(env, "/vehicles/10"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("detach: want 204, got %d", status)
	}
	status, resp := doJSONRequest(t, "GET", objURL(env, "/vehicles/10"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("get after detach: want 404, got %d %v", status, resp)
	}
}

func TestObjAPI_Detach_WhilePositioned_Returns409(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "occ-root")
	defineLocLeaf(t, env, "occ-root/bay", "occ-root", nil)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:11"})
	doJSONRequest(t, "PUT", objURL(env, "/vehicles/11/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "loc_leaf", "location_id": "occ-root/bay"},
	})
	status, resp := doJSONRequest(t, "DELETE", objURL(env, "/vehicles/11"), nil)
	if status != http.StatusConflict {
		t.Fatalf("detach while positioned: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ007" {
		t.Errorf("want XOLU-OBJ007, got %v", resp)
	}
}

func TestObjAPI_Move_ToLocLeaf_CapacityRefused(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "cap-root")
	defineLocLeaf(t, env, "cap-root/tiny", "cap-root", 0.0) // zero ceiling
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:12"})
	status, resp := doJSONRequest(t, "PUT", objURL(env, "/vehicles/12/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "loc_leaf", "location_id": "cap-root/tiny"},
	})
	if status != http.StatusConflict {
		t.Fatalf("move to zero-capacity leaf: want 409 (loc's own XOLU-LOC002 passed through), got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC002" {
		t.Errorf("want /loc's own XOLU-LOC002 to pass through unwrapped, got %v", resp)
	}
}

func TestObjAPI_Report_ReturnsOK(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:13"})
	status, _ := doJSONRequest(t, "POST", objURL(env, "/vehicles/13/report"), map[string]interface{}{
		"point": map[string]interface{}{"lat": -34.9, "lon": -56.16, "alt": 0},
	})
	if status != http.StatusOK {
		t.Fatalf("report: want 200, got %d", status)
	}
}

// TestObjAPI_Move_ContainmentKind_Works is T-120's own HTTP-level
// proof of what T-119's own test (same name, "RefusedAsNotYetBuilt")
// used to assert was refused — containment now works, so the
// assertion flips from "must be 400" to "must actually contain it".
func TestObjAPI_Move_ContainmentKind_Works(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:1"})
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:14"})
	status, resp := doJSONRequest(t, "PUT", objURL(env, "/pallets/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "vehicles:14"},
	})
	if status != http.StatusOK {
		t.Fatalf("containment move: want 200, got %d %v", status, resp)
	}
	if resp["position_kind"] != "obj" {
		t.Errorf("want position_kind \"obj\", got %v", resp["position_kind"])
	}

	status, posResp := doJSONRequest(t, "GET", objURL(env, "/pallets/1/position"), nil)
	if status != http.StatusOK {
		t.Fatalf("position: want 200, got %d %v", status, posResp)
	}
	chain, _ := posResp["chain"].([]interface{})
	if len(chain) != 2 || chain[0] != "pallets:1" || chain[1] != "vehicles:14" {
		t.Errorf("want chain [pallets:1 vehicles:14], got %v", chain)
	}
}

func TestObjAPI_Move_SelfContainment_Returns409(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:2"})
	status, resp := doJSONRequest(t, "PUT", objURL(env, "/pallets/2/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "pallets:2"},
	})
	if status != http.StatusConflict {
		t.Fatalf("self-containment: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ004" {
		t.Errorf("want XOLU-OBJ004, got %v", resp)
	}
}

func TestObjAPI_Move_CycleRefused_Returns409(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "a:1"})
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "b:1"})
	doJSONRequest(t, "PUT", objURL(env, "/b/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "a:1"},
	})
	status, resp := doJSONRequest(t, "PUT", objURL(env, "/a/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "b:1"},
	})
	if status != http.StatusConflict {
		t.Fatalf("cycle: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ004" {
		t.Errorf("want XOLU-OBJ004, got %v", resp)
	}
}

func TestObjAPI_Move_ContainerNotAttached_Returns409(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:3"})
	status, resp := doJSONRequest(t, "PUT", objURL(env, "/pallets/3/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "vehicles:999"},
	})
	if status != http.StatusConflict {
		t.Fatalf("container not attached: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ005" {
		t.Errorf("want XOLU-OBJ005, got %v", resp)
	}
}

func TestObjAPI_Move_CountCapacityRefused_Returns409(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{
		"subject": "vehicles:15", "capacity": map[string]interface{}{"max_count": 1.0},
	})
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:4"})
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:5"})
	status, resp := doJSONRequest(t, "PUT", objURL(env, "/pallets/4/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "vehicles:15"},
	})
	if status != http.StatusOK {
		t.Fatalf("first pallet: want 200, got %d %v", status, resp)
	}
	status, resp = doJSONRequest(t, "PUT", objURL(env, "/pallets/5/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "vehicles:15"},
	})
	if status != http.StatusConflict {
		t.Fatalf("second pallet over count capacity: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ003" {
		t.Errorf("want XOLU-OBJ003, got %v", resp)
	}
}
