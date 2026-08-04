// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_obj_surface_test.go — T-124 (wave 10): the remaining
// obj-01-rest-api.md endpoints -- retire (§6), capacity (§4),
// contents (§3).

package server_test

import (
	"net/http"
	"testing"
)

func TestObjAPI_Retire_Succeeds(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "cases:1"})
	status, resp := doJSONRequest(t, "POST", objURL(env, "/cases/1/retire"), nil)
	if status != http.StatusOK {
		t.Fatalf("retire: want 200, got %d %v", status, resp)
	}
	// GET must still work -- the row persists (obj-00-design.md §12's
	// own "closer to a bal account closure than to deletion" framing).
	status, getResp := doJSONRequest(t, "GET", objURL(env, "/cases/1"), nil)
	if status != http.StatusOK {
		t.Fatalf("get retired subject: want 200, got %d %v", status, getResp)
	}
}

func TestObjAPI_Retire_RefusedWhenContainsSomething(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:1"})
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "cases:1"})
	doJSONRequest(t, "PUT", objURL(env, "/cases/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "pallets:1"},
	})
	status, resp := doJSONRequest(t, "POST", objURL(env, "/pallets/1/retire"), nil)
	if status != http.StatusConflict {
		t.Fatalf("retire while still containing something: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ012" {
		t.Errorf("want XOLU-OBJ012, got %v", resp)
	}
}

func TestObjAPI_Retire_TwiceRefused(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "cases:1"})
	doJSONRequest(t, "POST", objURL(env, "/cases/1/retire"), nil)
	status, resp := doJSONRequest(t, "POST", objURL(env, "/cases/1/retire"), nil)
	if status != http.StatusConflict {
		t.Fatalf("double retire: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ012" {
		t.Errorf("want XOLU-OBJ012, got %v", resp)
	}
}

func TestObjAPI_CapacityPatch_UpdatesFields(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:1"})
	status, resp := doJSONRequest(t, "PATCH", objURL(env, "/vehicles/1/capacity"), map[string]interface{}{
		"max_weight_kg": 12000.0, "max_volume_m3": 40.0, "max_count": nil,
	})
	if status != http.StatusOK {
		t.Fatalf("capacity patch: want 200, got %d %v", status, resp)
	}
	capOut, _ := resp["capacity"].(map[string]interface{})
	if capOut["max_weight_kg"] != 12000.0 || capOut["max_volume_m3"] != 40.0 {
		t.Errorf("capacity not applied: got %v", resp["capacity"])
	}
}

func TestObjAPI_CapacityPatch_AllNil_Returns400(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:1"})
	status, resp := doJSONRequest(t, "PATCH", objURL(env, "/vehicles/1/capacity"), map[string]interface{}{
		"max_weight_kg": nil, "max_volume_m3": nil, "max_count": nil,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("all-nil capacity: want 400, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ008" {
		t.Errorf("want XOLU-OBJ008, got %v", resp)
	}
}

func TestObjAPI_Contents_DirectOnly(t *testing.T) {
	env := newMetaServer(t)
	for _, ref := range []string{"lorries:1", "pallets:1", "cases:1"} {
		doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": ref})
	}
	doJSONRequest(t, "PUT", objURL(env, "/pallets/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "lorries:1"},
	})
	doJSONRequest(t, "PUT", objURL(env, "/cases/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "pallets:1"},
	})

	status, resp := doJSONRequest(t, "GET", objURL(env, "/lorries/1/contents"), nil)
	if status != http.StatusOK {
		t.Fatalf("contents: want 200, got %d %v", status, resp)
	}
	contents, _ := resp["contents"].([]interface{})
	if len(contents) != 1 || contents[0] != "pallets:1" {
		t.Errorf("direct contents: want [pallets:1] (not the case, one level down), got %v", contents)
	}
}

func TestObjAPI_Contents_DepthAll(t *testing.T) {
	env := newMetaServer(t)
	for _, ref := range []string{"lorries:1", "pallets:1", "cases:1"} {
		doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": ref})
	}
	doJSONRequest(t, "PUT", objURL(env, "/pallets/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "lorries:1"},
	})
	doJSONRequest(t, "PUT", objURL(env, "/cases/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": "pallets:1"},
	})

	status, resp := doJSONRequest(t, "GET", objURL(env, "/lorries/1/contents?depth=all"), nil)
	if status != http.StatusOK {
		t.Fatalf("contents?depth=all: want 200, got %d %v", status, resp)
	}
	contents, _ := resp["contents"].([]interface{})
	if len(contents) != 2 {
		t.Errorf("transitive contents: want 2 (pallet and case), got %v", contents)
	}
}

func TestObjAPI_Contents_EmptyContainer_ReturnsEmptyArray(t *testing.T) {
	env := newMetaServer(t)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "lorries:1"})
	status, resp := doJSONRequest(t, "GET", objURL(env, "/lorries/1/contents"), nil)
	if status != http.StatusOK {
		t.Fatalf("contents: want 200, got %d %v", status, resp)
	}
	contents, ok := resp["contents"].([]interface{})
	if !ok {
		t.Fatalf("want a contents array, got %v", resp["contents"])
	}
	if len(contents) != 0 {
		t.Errorf("want empty contents, got %v", contents)
	}
}
