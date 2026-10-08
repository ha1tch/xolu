// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// commit_delete_test.go
//
// XOT233: POST /commit with a delete instead of an update.

import (
	"encoding/json"
	"net/http"
	"testing"

	xoluerr "github.com/ha1tch/xolu/pkg/errors"
)

func commitDeleteBody(entity string, id int, version *int, appendRows ...map[string]interface{}) map[string]interface{} {
	del := map[string]interface{}{"entity": entity, "id": id}
	if version != nil {
		del["version"] = *version
	}
	rows := make([]interface{}, 0, len(appendRows))
	for _, r := range appendRows {
		rows = append(rows, r)
	}
	return map[string]interface{}{"delete": del, "append": rows}
}

func TestCommitE2E_Delete(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()

	env.doJSON("POST", "/api/v1/assets/save/9001", map[string]interface{}{"state": "A"})
	env.doJSON("POST", "/api/v1/assets/save/9001", map[string]interface{}{"state": "A2"})
	_, asset := env.doJSON("GET", "/api/v1/assets/9001", nil)
	ver := int(asset["_version"].(float64))

	status, result := env.doJSON("POST", commitURL(""), commitDeleteBody("assets", 9001, &ver,
		map[string]interface{}{"entity": "events", "data": map[string]interface{}{"ev": "deleted"}}))
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, result)
	}
	del, _ := result["delete"].(map[string]interface{})
	if del == nil || del["entity"] != "assets" || int(del["id"].(float64)) != 9001 || int(del["version"].(float64)) != ver {
		t.Fatalf("delete result = %v, want assets 9001 at version %d", result["delete"], ver)
	}
	if _, hasUpdate := result["update"]; hasUpdate {
		t.Errorf("a delete commit answered with an update object: %v", result)
	}
	if rows, _ := result["appended"].([]interface{}); len(rows) != 1 {
		t.Errorf("appended = %v, want one row", result["appended"])
	}
	if status, _ := env.doJSON("GET", "/api/v1/assets/9001", nil); status != http.StatusNotFound {
		t.Errorf("GET after the delete = %d, want 404", status)
	}
	if rows := env.oqlData("SELECT id FROM events"); len(rows) != 1 {
		t.Errorf("events rows = %d, want 1", len(rows))
	}
}

func TestCommitE2E_DeleteWithAStaleVersionConflicts(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	env.doJSON("POST", "/api/v1/assets/save/9002", map[string]interface{}{"state": "A"})
	env.doJSON("POST", "/api/v1/assets/save/9002", map[string]interface{}{"state": "A2"})
	stale := 1

	status, result := env.doJSON("POST", commitURL(""), commitDeleteBody("assets", 9002, &stale,
		map[string]interface{}{"entity": "events", "data": map[string]interface{}{"ev": "must-not-land"}}))
	if status != http.StatusConflict {
		t.Fatalf("status %d, want 409: %v", status, result)
	}
	errObj, _ := result["error"].(map[string]interface{})
	if errObj == nil || errObj["code"] != "XOLU-CM001" {
		t.Errorf("code = %v, want XOLU-CM001", result)
	}
	if cv, ok := result["current_version"].(float64); !ok || cv <= 1 {
		t.Errorf("current_version = %v, want the entity's real version", result["current_version"])
	}
	if status, _ := env.doJSON("GET", "/api/v1/assets/9002", nil); status != http.StatusOK {
		t.Errorf("the entity is gone after a refused delete: %d", status)
	}
}

func TestCommitE2E_DeleteOfAMissingEntity(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	row := map[string]interface{}{"entity": "events", "data": map[string]interface{}{"ev": "x"}}

	status, result := env.doJSON("POST", commitURL(""), commitDeleteBody("assets", 9003, nil, row))
	if status != http.StatusNotFound {
		t.Errorf("without a version: status %d, want 404: %v", status, result)
	}
	one := 1
	status, result = env.doJSON("POST", commitURL(""), commitDeleteBody("assets", 9003, &one, row))
	if status != http.StatusConflict {
		t.Errorf("with a version: status %d, want 409: %v", status, result)
	}
	if cv, _ := result["current_version"].(float64); cv != -1 {
		t.Errorf("current_version = %v, want -1", result["current_version"])
	}
}

func TestCommitE2E_UpdateAndDeleteTogetherAreRefused(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	req := map[string]interface{}{
		"update": map[string]interface{}{"entity": "assets", "id": 9004, "data": map[string]interface{}{"a": 1}},
		"delete": map[string]interface{}{"entity": "assets", "id": 9004},
		"append": []interface{}{map[string]interface{}{"entity": "events", "data": map[string]interface{}{"ev": "x"}}},
	}
	status, result := env.doJSON("POST", commitURL(""), req)
	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %v", status, result)
	}
	if errObj, _ := result["error"].(map[string]interface{}); errObj == nil || errObj["code"] != "XOLU-CM017" {
		t.Errorf("code = %v, want XOLU-CM017", result)
	}
}

func TestCommitE2E_DeleteValidation(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	row := map[string]interface{}{"entity": "events", "data": map[string]interface{}{"ev": "x"}}
	for name, del := range map[string]map[string]interface{}{
		"no entity":   {"id": 5},
		"bad entity":  {"entity": "Bad Name!", "id": 5},
		"zero id":     {"entity": "assets", "id": 0},
		"negative id": {"entity": "assets", "id": -3},
	} {
		status, result := env.doJSON("POST", commitURL(""), map[string]interface{}{"delete": del, "append": []interface{}{row}})
		if status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %v", name, status, result)
		}
	}
	// A delete with nothing else to commit still needs the usual non-empty check.
	status, _ := env.doJSON("POST", commitURL(""), map[string]interface{}{"delete": map[string]interface{}{"entity": "assets", "id": 9005}})
	if status != http.StatusBadRequest {
		t.Errorf("a delete with no append, timeseries or fsm_walk: status %d, want 400", status)
	}
}

// The same restrict policy that refuses a plain DELETE refuses a commit's delete,
// and the append is not written.
func TestCommitE2E_DeleteHonoursRestrict(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	mustOK := func(method, path string, body interface{}) []byte {
		t.Helper()
		resp, b := ts.doRequest(method, path, body)
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, b)
		}
		return b
	}
	mustOK(http.MethodPost, "/api/v1/schema/users", map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}})
	mustOK(http.MethodPost, "/api/v1/schema/posts", map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{
			"title": map[string]interface{}{"type": "string"},
			"author_id": map[string]interface{}{"type": "object", "format": "ref",
				"x-ref": map[string]interface{}{"entity": "users", "on_delete": "restrict"}},
		}})
	var user struct {
		ID int `json:"id"`
	}
	json.Unmarshal(mustOK(http.MethodPost, "/api/v1/users", map[string]interface{}{"name": "alice"}), &user)
	mustOK(http.MethodPost, "/api/v1/posts", map[string]interface{}{
		"title": "hello", "author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": user.ID}})

	resp, body := ts.doRequest(http.MethodPost, "/api/v1/commit", commitDeleteBody("users", user.ID, nil,
		map[string]interface{}{"entity": "audit", "data": map[string]interface{}{"ev": "must-not-land"}}))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", resp.StatusCode, body)
	}
	var out map[string]interface{}
	json.Unmarshal(body, &out)
	if errObj, _ := out["error"].(map[string]interface{}); errObj == nil || errObj["code"] != string(xoluerr.ErrRIRestrictViolation) {
		t.Errorf("error = %v, want %s", out["error"], xoluerr.ErrRIRestrictViolation)
	}
	if resp, _ := ts.doRequest(http.MethodGet, "/api/v1/users/"+itoa(user.ID), nil); resp.StatusCode != http.StatusOK {
		t.Errorf("the restricted entity is gone: %d", resp.StatusCode)
	}
	if resp, _ := ts.doRequest(http.MethodGet, "/api/v1/audit", nil); resp.StatusCode == http.StatusOK {
		var list map[string]interface{}
		_, b := ts.doRequest(http.MethodGet, "/api/v1/audit", nil)
		json.Unmarshal(b, &list)
		if data, _ := list["data"].([]interface{}); len(data) != 0 {
			t.Errorf("the append of a refused commit landed: %v", data)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
