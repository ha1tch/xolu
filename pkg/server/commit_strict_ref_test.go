// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// commit_strict_ref_test.go
//
// XOT238 over HTTP: strict_ref on a /commit fsm_walk.

import (
	"fmt"
	"net/http"
	"testing"
)

func TestCommitE2E_StrictRef(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	machine := func(ref string) int64 {
		body := map[string]interface{}{"definition": defID}
		if ref != "" {
			body["ref"] = ref
		}
		_, resp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), body)
		return int64(resp["id"].(float64))
	}
	commit := func(machineID int64, strict bool, entityID int) (int, map[string]interface{}) {
		walk := map[string]interface{}{"machine": machineID, "input": "ready_for_inspection"}
		if strict {
			walk["strict_ref"] = true
		}
		return doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v1/tenant/default/commit", env.ts.URL), map[string]interface{}{
			"update":   map[string]interface{}{"entity": "asset", "id": entityID, "data": map[string]interface{}{"n": 1}},
			"fsm_walk": walk,
		})
	}
	code := func(r map[string]interface{}) interface{} {
		if e, ok := r["error"].(map[string]interface{}); ok {
			return e["code"]
		}
		return nil
	}

	bound := machine("asset:9301")
	if st, r := commit(bound, true, 9301); st != http.StatusOK {
		t.Fatalf("machine bound to this entity: %d %v", st, r)
	}
	other := machine("asset:9999")
	if st, r := commit(other, true, 9302); st != http.StatusConflict || code(r) != "XOLU-FSM014" {
		t.Fatalf("machine bound to another entity: status %d code %v, want 409 XOLU-FSM014: %v", st, code(r), r)
	}
	if st, _ := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v1/tenant/default/asset/9302", env.ts.URL), nil); st != http.StatusNotFound {
		t.Errorf("the entity of a refused commit exists: %d", st)
	}
	unbound := machine("")
	if st, r := commit(unbound, true, 9303); st != http.StatusConflict || code(r) != "XOLU-FSM014" {
		t.Fatalf("machine with no ref: status %d code %v, want 409 XOLU-FSM014", st, code(r))
	}
	// Without strict_ref the walk goes ahead exactly as before.
	if st, r := commit(machine("asset:9999"), false, 9304); st != http.StatusOK {
		t.Fatalf("not strict: %d %v", st, r)
	}
}
