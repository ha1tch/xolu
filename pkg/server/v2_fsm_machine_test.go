// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_machine_test.go — S7 / B3: FSM machine endpoints.

import (
	"fmt"
	"net/http"
	"testing"
)

func fsmMachineURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/fsm/machine%s", sts.ts.URL, path)
}

// createAssetDef creates the AssetLifecycle definition and returns its id.
func createAssetDef(t *testing.T, env *stdTestServer) int64 {
	t.Helper()
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), assetLifecycleSpec())
	if status != http.StatusCreated {
		t.Fatalf("setup: create def want 201, got %d: %v", status, resp)
	}
	return int64(resp["id"].(float64))
}

// ─── Create ───────────────────────────────────────────────────────────────────

func TestFSMMachine_CreateUnbound(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)

	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, ""),
		map[string]interface{}{"definition": defID})
	if status != http.StatusCreated {
		t.Fatalf("create machine: want 201, got %d: %v", status, resp)
	}
	if resp["state"] != "Provisioning" {
		t.Errorf("initial state: want Provisioning, got %v", resp["state"])
	}
	if resp["definition_deleted"] != false {
		t.Errorf("definition_deleted: want false, got %v", resp["definition_deleted"])
	}
	vars, _ := resp["vars"].(map[string]interface{})
	if vars["@retries"].(float64) != 0 {
		t.Errorf("@retries default: want 0, got %v", vars["@retries"])
	}
}

func TestFSMMachine_CreateWithRef(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, ""),
		map[string]interface{}{"definition": defID, "ref": "asset:123"})
	if status != http.StatusCreated {
		t.Fatalf("create bound machine: want 201, got %d: %v", status, resp)
	}
	if resp["ref"] != "asset:123" {
		t.Errorf("ref: want asset:123, got %v", resp["ref"])
	}
}

func TestFSMMachine_CreateWithOverrides(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, ""),
		map[string]interface{}{
			"definition": defID,
			"ref":        "asset:9",
			"overrides": map[string]interface{}{
				"variables": map[string]interface{}{
					"@retries": map[string]interface{}{"type": "int", "default": 5},
				},
				"transitions": map[string]interface{}{
					"inspection_failed": map[string]interface{}{"guard": "@retries < 5"},
				},
			},
		})
	if status != http.StatusCreated {
		t.Fatalf("create with overrides: want 201, got %d: %v", status, resp)
	}
	vars, _ := resp["vars"].(map[string]interface{})
	if vars["@retries"].(float64) != 5 {
		t.Errorf("overridden @retries default: want 5, got %v", vars["@retries"])
	}
}

func TestFSMMachine_CreateRejectsUnknownOverrideInput(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, ""),
		map[string]interface{}{
			"definition": defID,
			"overrides": map[string]interface{}{
				"transitions": map[string]interface{}{
					"no_such_input": map[string]interface{}{"guard": "1 = 1"},
				},
			},
		})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown override input: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM013" {
		t.Errorf("unknown override input: want XOLU-FSM013, got %v", resp["error"])
	}
}

func TestFSMMachine_CreateRejectsInlineEntity(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, ""),
		map[string]interface{}{
			"definition": defID,
			"entity":     map[string]interface{}{"type": "asset", "data": map[string]interface{}{"name": "X"}},
		})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("inline entity: want 422 (deferred to Part 2), got %d: %v", status, resp)
	}
}

func TestFSMMachine_CreateMissingDefinition(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, ""),
		map[string]interface{}{"definition": 9999})
	if status != http.StatusNotFound {
		t.Fatalf("missing def: want 404, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM001" {
		t.Errorf("missing def: want XOLU-FSM001, got %v", resp["error"])
	}
}

// ─── Snapshot independence (the prototype model) ──────────────────────────────

func TestFSMMachine_SurvivesDefinitionDelete(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""),
		map[string]interface{}{"definition": defID})
	machineID := int64(createResp["id"].(float64))

	// Delete the source definition.
	status, _ := doJSONRequest(t, "DELETE", fsmDefURL(env, fmt.Sprintf("/%d", defID)), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete def: want 204, got %d", status)
	}

	// Machine still operates, but reports definition_deleted=true.
	status, getResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d", machineID)), nil)
	if status != http.StatusOK {
		t.Fatalf("get machine after def delete: want 200, got %d: %v", status, getResp)
	}
	if getResp["definition_deleted"] != true {
		t.Errorf("definition_deleted after delete: want true, got %v", getResp["definition_deleted"])
	}
	if getResp["state"] != "Provisioning" {
		t.Errorf("machine state after def delete: want Provisioning, got %v", getResp["state"])
	}
}

// ─── List / filters ───────────────────────────────────────────────────────────

func TestFSMMachine_ListWithFilters(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID, "ref": "asset:1"})
	doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID, "ref": "asset:2"})

	_, all := doJSONRequest(t, "GET", fsmMachineURL(env, ""), nil)
	if len(all["machines"].([]interface{})) != 2 {
		t.Errorf("list all: want 2, got %d", len(all["machines"].([]interface{})))
	}

	_, byRef := doJSONRequest(t, "GET", fsmMachineURL(env, "?ref=asset:1"), nil)
	if len(byRef["machines"].([]interface{})) != 1 {
		t.Errorf("list by ref: want 1, got %d", len(byRef["machines"].([]interface{})))
	}

	_, byState := doJSONRequest(t, "GET", fsmMachineURL(env, "?state=Provisioning"), nil)
	if len(byState["machines"].([]interface{})) != 2 {
		t.Errorf("list by state: want 2, got %d", len(byState["machines"].([]interface{})))
	}
}

// ─── State / vars / transitions / history ─────────────────────────────────────

func TestFSMMachine_State(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("state: want 200, got %d: %v", status, resp)
	}
	if resp["state"] != "Provisioning" || resp["terminal"] != false {
		t.Errorf("state/terminal: got %v / %v", resp["state"], resp["terminal"])
	}
}

func TestFSMMachine_Vars(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/vars", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("vars: want 200, got %d: %v", status, resp)
	}
	retries, ok := resp["@retries"].(map[string]interface{})
	if !ok {
		t.Fatalf("@retries entry missing or wrong shape: %v", resp["@retries"])
	}
	if retries["type"] != "int" {
		t.Errorf("@retries.type: want int, got %v", retries["type"])
	}
	if retries["value"].(float64) != 0 {
		t.Errorf("@retries.value: want 0, got %v", retries["value"])
	}
}

func TestFSMMachine_Transitions(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/transitions", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("transitions: want 200, got %d: %v", status, resp)
	}
	if resp["state"] != "Provisioning" {
		t.Errorf("transitions.state: want Provisioning, got %v", resp["state"])
	}
	inputs, _ := resp["inputs"].([]interface{})
	if len(inputs) != 1 || inputs[0] != "ready_for_inspection" {
		t.Errorf("transitions.inputs: want [ready_for_inspection], got %v", inputs)
	}
}

func TestFSMMachine_HistoryHasCreationEntry(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/history", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("history: want 200, got %d: %v", status, resp)
	}
	entries, _ := resp["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("history: want 1 creation entry, got %d", len(entries))
	}
	e0, _ := entries[0].(map[string]interface{})
	if e0["to"] != "Provisioning" {
		t.Errorf("creation entry to: want Provisioning, got %v", e0["to"])
	}
	if e0["from"] != nil {
		t.Errorf("creation entry from: want nil, got %v", e0["from"])
	}
	if e0["note"] != "machine created" {
		t.Errorf("creation entry note: want 'machine created', got %v", e0["note"])
	}
}

// ─── Patch / Delete ───────────────────────────────────────────────────────────

func TestFSMMachine_PatchGuard(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "PATCH", fsmMachineURL(env, fmt.Sprintf("/%d", id)),
		map[string]interface{}{
			"overrides": map[string]interface{}{
				"transitions": map[string]interface{}{
					"inspection_failed": map[string]interface{}{"guard": "@retries < 10"},
				},
			},
		})
	if status != http.StatusOK {
		t.Fatalf("patch machine: want 200, got %d: %v", status, resp)
	}
}

func TestFSMMachine_PatchUnknownInputRejected(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "PATCH", fsmMachineURL(env, fmt.Sprintf("/%d", id)),
		map[string]interface{}{
			"overrides": map[string]interface{}{
				"transitions": map[string]interface{}{
					"bogus": map[string]interface{}{"guard": "1 = 1"},
				},
			},
		})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("patch unknown input: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM013" {
		t.Errorf("patch unknown input: want XOLU-FSM013, got %v", resp["error"])
	}
}

func TestFSMMachine_Delete(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, _ := doJSONRequest(t, "DELETE", fsmMachineURL(env, fmt.Sprintf("/%d", id)), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete machine: want 204, got %d", status)
	}
	status, resp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d", id)), nil)
	if status != http.StatusNotFound {
		t.Errorf("get after delete: want 404, got %d: %v", status, resp)
	}
}

func TestFSMMachine_GetNotFound(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET", fsmMachineURL(env, "/9999"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing machine: want 404, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM002" {
		t.Errorf("missing machine: want XOLU-FSM002, got %v", resp["error"])
	}
}

// ─── Walk (S8) ────────────────────────────────────────────────────────────────

func TestFSMMachine_WalkBasic(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "ready_for_inspection"})
	if status != http.StatusOK {
		t.Fatalf("walk: want 200, got %d: %v", status, resp)
	}
	if resp["previous"] != "Provisioning" || resp["current"] != "AwaitingInspection" {
		t.Errorf("walk states: got %v -> %v", resp["previous"], resp["current"])
	}
	if resp["terminal"] != false {
		t.Errorf("terminal: want false, got %v", resp["terminal"])
	}

	// State endpoint reflects the advance.
	_, stateResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", id)), nil)
	if stateResp["state"] != "AwaitingInspection" {
		t.Errorf("state after walk: want AwaitingInspection, got %v", stateResp["state"])
	}
}

func TestFSMMachine_WalkGuardPasses(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "ready_for_inspection"})

	// inspection_passed has guard payload.result = 'pass' AND payload.technician != ''
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "inspection_passed",
			"payload": map[string]interface{}{"result": "pass", "technician": "alice"}})
	if status != http.StatusOK {
		t.Fatalf("guarded walk: want 200, got %d: %v", status, resp)
	}
	if resp["current"] != "InService" {
		t.Errorf("guarded walk current: want InService, got %v", resp["current"])
	}
	outputs, _ := resp["outputs"].([]interface{})
	if len(outputs) != 1 || outputs[0] != "asset_activated" {
		t.Errorf("Mealy output: want [asset_activated], got %v", outputs)
	}
}

func TestFSMMachine_WalkGuardRejects(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "ready_for_inspection"})

	// Guard fails: result is not 'pass'.
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "inspection_passed",
			"payload": map[string]interface{}{"result": "fail", "technician": "alice"}})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("rejected guard: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM004" {
		t.Errorf("rejected guard: want XOLU-FSM004, got %v", resp["error"])
	}
	// State did not advance.
	_, stateResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", id)), nil)
	if stateResp["state"] != "AwaitingInspection" {
		t.Errorf("state after rejected guard: want AwaitingInspection, got %v", stateResp["state"])
	}
}

func TestFSMMachine_WalkSetClauseIncrement(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "ready_for_inspection"})

	// inspection_failed has guard @retries < 3 and set @retries = @retries + 1.
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "inspection_failed"})
	if status != http.StatusOK {
		t.Fatalf("set-clause walk: want 200, got %d: %v", status, resp)
	}
	vars, _ := resp["vars"].(map[string]interface{})
	if vars["@retries"].(float64) != 1 {
		t.Errorf("@retries after one failure: want 1, got %v", vars["@retries"])
	}

	// A second failure → 2.
	_, resp2 := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "inspection_failed"})
	vars2, _ := resp2["vars"].(map[string]interface{})
	if vars2["@retries"].(float64) != 2 {
		t.Errorf("@retries after two failures: want 2, got %v", vars2["@retries"])
	}
}

func TestFSMMachine_WalkNextValueForInSetClause(t *testing.T) {
	env := newV2Server(t)
	// Define a sequence the set clause will draw from.
	seqStatus, seqResp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v2/tenant/default/seq", env.ts.URL),
		map[string]interface{}{"name": "case_seq", "start": 1000})
	if seqStatus != http.StatusCreated {
		t.Fatalf("seq define: want 201, got %d: %v", seqStatus, seqResp)
	}

	// A minimal definition whose transition assigns NEXT VALUE FOR to a var.
	def := map[string]interface{}{
		"name":        "Ticket",
		"initial":     "New",
		"determinism": "strict",
		"states": map[string]interface{}{
			"New":    map[string]interface{}{"terminal": false},
			"Open":   map[string]interface{}{"terminal": false},
			"Closed": map[string]interface{}{"terminal": true},
		},
		"variables": map[string]interface{}{
			"@case_number": map[string]interface{}{"type": "int", "default": 0},
		},
		"transitions": []map[string]interface{}{
			{"from": "New", "input": "open", "to": "Open", "set": map[string]string{"@case_number": "NEXT VALUE FOR case_seq"}},
			{"from": "Open", "input": "close", "to": "Closed"},
		},
	}
	_, defResp := doJSONRequest(t, "POST", fsmDefURL(env, ""), def)
	defID := int64(defResp["id"].(float64))
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "open"})
	if status != http.StatusOK {
		t.Fatalf("NEXT VALUE FOR walk: want 200, got %d: %v", status, resp)
	}
	vars, _ := resp["vars"].(map[string]interface{})
	if vars["@case_number"].(float64) != 1000 {
		t.Errorf("@case_number from NEXT VALUE FOR: want 1000 (first value = start), got %v", vars["@case_number"])
	}
}

func TestFSMMachine_WalkNoTransition(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	// "suspend" is not valid from Provisioning.
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "suspend"})
	if status != http.StatusConflict {
		t.Fatalf("invalid input: want 409, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM003" {
		t.Errorf("invalid input: want XOLU-FSM003, got %v", resp["error"])
	}
}

func TestFSMMachine_WalkTerminalRejected(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(createResp["id"].(float64))

	// Drive to Decommissioned: Provisioning -> AwaitingInspection -> InService -> Decommissioned.
	doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)), map[string]interface{}{"input": "ready_for_inspection"})
	doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "inspection_passed", "payload": map[string]interface{}{"result": "pass", "technician": "bob"}})
	_, decomResp := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)), map[string]interface{}{"input": "decommission"})
	if decomResp["terminal"] != true {
		t.Fatalf("expected terminal after decommission, got %v", decomResp["terminal"])
	}

	// A further walk on a terminal machine → 409 XOLU-FSM005.
	status, resp := doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)),
		map[string]interface{}{"input": "suspend"})
	if status != http.StatusConflict {
		t.Fatalf("walk on terminal: want 409, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM005" {
		t.Errorf("walk on terminal: want XOLU-FSM005, got %v", resp["error"])
	}
}

// ─── /commit + fsm_walk integration (S8 step 4) ───────────────────────────────

func fsmCommitURL(sts *stdTestServer) string {
	return fmt.Sprintf("%s/api/v1/tenant/default/commit", sts.ts.URL)
}

func TestFSMCommit_WalkAtomicWithEntityWrite(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	machineID := int64(createResp["id"].(float64))

	// Commit an entity update together with a walk, atomically.
	status, resp := doJSONRequest(t, "POST", fsmCommitURL(env), map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "asset",
			"id":     123,
			"data":   map[string]interface{}{"name": "Pump A"},
		},
		"fsm_walk": map[string]interface{}{
			"machine": machineID,
			"input":   "ready_for_inspection",
		},
	})
	if status != http.StatusOK {
		t.Fatalf("commit+walk: want 200, got %d: %v", status, resp)
	}
	fsmWalk, ok := resp["fsm_walk"].(map[string]interface{})
	if !ok {
		t.Fatalf("commit response missing fsm_walk: %v", resp)
	}
	if fsmWalk["previous"] != "Provisioning" || fsmWalk["current"] != "AwaitingInspection" {
		t.Errorf("commit walk result: got %v -> %v", fsmWalk["previous"], fsmWalk["current"])
	}

	// The machine actually advanced.
	_, stateResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", machineID)), nil)
	if stateResp["state"] != "AwaitingInspection" {
		t.Errorf("machine state after commit walk: want AwaitingInspection, got %v", stateResp["state"])
	}
}

func TestFSMCommit_WalkGuardFailureRollsBackEntity(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, createResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	machineID := int64(createResp["id"].(float64))

	// Advance to AwaitingInspection first (standalone walk).
	doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", machineID)),
		map[string]interface{}{"input": "ready_for_inspection"})

	// Commit an entity write together with a walk whose guard will fail
	// (inspection_passed requires result='pass'). The whole commit must roll
	// back: neither the entity write nor the state advance happens.
	status, resp := doJSONRequest(t, "POST", fsmCommitURL(env), map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "asset",
			"id":     777,
			"data":   map[string]interface{}{"name": "ShouldNotPersist"},
		},
		"fsm_walk": map[string]interface{}{
			"machine": machineID,
			"input":   "inspection_passed",
			"payload": map[string]interface{}{"result": "fail", "technician": "x"},
		},
	})
	if status != http.StatusConflict {
		t.Fatalf("commit with failing walk: want 409, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM008" {
		t.Errorf("commit walk failure: want XOLU-FSM008, got %v", resp["error"])
	}

	// The machine state did NOT advance (still AwaitingInspection).
	_, stateResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", machineID)), nil)
	if stateResp["state"] != "AwaitingInspection" {
		t.Errorf("state after rolled-back commit: want AwaitingInspection, got %v", stateResp["state"])
	}

	// The entity write was rolled back: asset 777 must not exist.
	getStatus, _ := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/tenant/default/asset/777", env.ts.URL), nil)
	if getStatus == http.StatusOK {
		t.Errorf("entity write should have rolled back, but asset 777 exists")
	}
}
