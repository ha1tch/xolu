// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// commit_versioning_test.go
//
// Handler-level regression test for a /commit defect found by a client that
// keeps entity history with each save:
//
//   - XOT230: a compare-and-set against an entity that does not exist created it.

import (
	"net/http"
	"testing"
)

func TestCommitE2E_CASOnMissingEntityConflicts(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()

	env.doJSON("POST", "/api/v1/assets/save/8101", map[string]interface{}{"state": "A"})
	if status, _ := env.doJSON("DELETE", "/api/v1/assets/8101", nil); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("DELETE: status %d", status)
	}

	req := map[string]interface{}{
		"update": map[string]interface{}{
			"entity":  "assets",
			"id":      8101,
			"version": 1,
			"data":    map[string]interface{}{"state": "B"},
		},
		"append": []interface{}{
			map[string]interface{}{"entity": "events", "data": map[string]interface{}{"ev": "must-not-land"}},
		},
	}
	status, result := env.doJSON("POST", commitURL(""), req)
	if status != http.StatusConflict {
		t.Fatalf("status %d, want 409: %v", status, result)
	}
	errObj, _ := result["error"].(map[string]interface{})
	if errObj == nil || errObj["code"] != "XOLU-CM001" {
		t.Errorf("code: %v, want XOLU-CM001", result)
	}
	// There is no current version to report: the handler's existing "unreadable" value.
	if cv, ok := result["current_version"].(float64); !ok || cv != -1 {
		t.Errorf("current_version = %v, want -1", result["current_version"])
	}
	if status, _ := env.doJSON("GET", "/api/v1/assets/8101", nil); status != http.StatusNotFound {
		t.Errorf("the deleted entity answers %d after the refused commit, want 404", status)
	}
	// The refused commit's append must not have landed. The type may not exist at all.
	if status, res := env.doJSON("GET", "/api/v1/events", nil); status == http.StatusOK {
		if data, _ := res["data"].([]interface{}); len(data) != 0 {
			t.Errorf("the append of a refused commit landed: %v", data)
		}
	}
}
