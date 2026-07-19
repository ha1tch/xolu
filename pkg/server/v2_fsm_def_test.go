// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_def_test.go — S7 / B2: FSM definition endpoints.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func fsmDefURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/fsm/def%s", sts.ts.URL, path)
}

// errCode extracts the nested error code from the standard error envelope
// {"error":{"code":...,"message":...,"status":...}}.
func errCode(resp map[string]interface{}) string {
	e, ok := resp["error"].(map[string]interface{})
	if !ok {
		return ""
	}
	code, _ := e["code"].(string)
	return code
}

// errMessage extracts the error message from a single-error response or the
// first error of an errors list (the validate endpoint returns the latter).
func errMessage(resp map[string]interface{}) string {
	if e, ok := resp["error"].(map[string]interface{}); ok {
		if m, ok := e["message"].(string); ok {
			return m
		}
	}
	if errs, ok := resp["errors"].([]interface{}); ok && len(errs) > 0 {
		if e, ok := errs[0].(map[string]interface{}); ok {
			if m, ok := e["message"].(string); ok {
				return m
			}
		}
	}
	return ""
}

func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// assetLifecycleSpec is the canonical definition from API_V2.md, used across
// the definition and machine test suites.
func assetLifecycleSpec() map[string]interface{} {
	return map[string]interface{}{
		"name":        "AssetLifecycle",
		"description": "Lifecycle of a physical asset",
		"initial":     "Provisioning",
		"determinism": "firstmatch",
		"states": map[string]interface{}{
			"Provisioning":       map[string]interface{}{"terminal": false},
			"AwaitingInspection": map[string]interface{}{"terminal": false},
			"InService":          map[string]interface{}{"terminal": false},
			"Suspended":          map[string]interface{}{"terminal": false},
			"Decommissioned":     map[string]interface{}{"terminal": true},
		},
		"variables": map[string]interface{}{
			"@retries": map[string]interface{}{"type": "int", "default": 0},
		},
		"transitions": []map[string]interface{}{
			{"from": "Provisioning", "input": "ready_for_inspection", "to": "AwaitingInspection", "set": map[string]string{"@retries": "0"}},
			{"from": "AwaitingInspection", "input": "inspection_passed", "to": "InService",
				"guard": "payload.result = 'pass' AND payload.technician != ''", "output": "asset_activated", "set": map[string]string{"@retries": "0"}},
			{"from": "AwaitingInspection", "input": "inspection_failed", "to": "AwaitingInspection",
				"guard": "@retries < 3", "set": map[string]string{"@retries": "@retries + 1"}},
			{"from": "AwaitingInspection", "input": "inspection_abandoned", "to": "Provisioning", "guard": "@retries >= 3"},
			{"from": "InService", "input": "suspend", "to": "Suspended"},
			{"from": "Suspended", "input": "reinstate", "to": "InService"},
			{"from": []string{"InService", "Suspended"}, "input": "decommission", "to": "Decommissioned", "output": "asset_decommissioned"},
		},
		"output_alphabet": []string{"asset_activated", "asset_decommissioned"},
	}
}

// ─── Create ───────────────────────────────────────────────────────────────────

func TestFSMDef_CreateAssetLifecycle(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), assetLifecycleSpec())
	if status != http.StatusCreated {
		t.Fatalf("create def: want 201, got %d: %v", status, resp)
	}
	if resp["name"] != "AssetLifecycle" {
		t.Errorf("name: want AssetLifecycle, got %v", resp["name"])
	}
	if _, ok := resp["id"]; !ok {
		t.Errorf("response missing id")
	}
	analysis, ok := resp["analysis"].(map[string]interface{})
	if !ok {
		t.Fatalf("analysis missing or wrong type: %v", resp["analysis"])
	}
	if analysis["reachable"] != true {
		t.Errorf("analysis.reachable: want true, got %v", analysis["reachable"])
	}
	if analysis["deterministic"] != true {
		t.Errorf("analysis.deterministic: want true, got %v", analysis["deterministic"])
	}
}

func TestFSMDef_CreateRejectsBadGuard(t *testing.T) {
	env := newV2Server(t)
	spec := assetLifecycleSpec()
	spec["transitions"].([]map[string]interface{})[2]["guard"] = "@retries <<< 3" // syntax error
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), spec)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("bad guard: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM011" {
		t.Errorf("bad guard: want XOLU-FSM011, got %v", resp["error"])
	}
}

func TestFSMDef_CreateRejectsOutputNotInAlphabet(t *testing.T) {
	env := newV2Server(t)
	spec := assetLifecycleSpec()
	spec["output_alphabet"] = []string{"asset_activated"} // drop asset_decommissioned
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), spec)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("output not in alphabet: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM006" {
		t.Errorf("output not in alphabet: want XOLU-FSM006, got %v", resp["error"])
	}
}

func TestFSMDef_CreateRejectsNoTerminalReachable(t *testing.T) {
	env := newV2Server(t)
	// A two-state machine where the non-terminal state loops on itself and
	// never reaches a terminal state.
	spec := map[string]interface{}{
		"name":        "Stuck",
		"initial":     "A",
		"determinism": "strict",
		"states": map[string]interface{}{
			"A": map[string]interface{}{"terminal": false},
			"B": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "A", "input": "loop", "to": "A"},
		},
	}
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), spec)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("no terminal reachable: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM009" {
		t.Errorf("no terminal reachable: want XOLU-FSM009, got %v", resp["error"])
	}
}

func TestFSMDef_CreateRejectsUndeclaredInitial(t *testing.T) {
	env := newV2Server(t)
	spec := assetLifecycleSpec()
	spec["initial"] = "Nonexistent"
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), spec)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("undeclared initial: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM006" {
		t.Errorf("undeclared initial: want XOLU-FSM006, got %v", resp["error"])
	}
}

// ─── Validate (no store) ──────────────────────────────────────────────────────

func TestFSMDef_ValidateValid(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, "/validate"), assetLifecycleSpec())
	if status != http.StatusOK {
		t.Fatalf("validate: want 200, got %d: %v", status, resp)
	}
	if resp["valid"] != true {
		t.Errorf("validate valid spec: want valid=true, got %v", resp["valid"])
	}
}

func TestFSMDef_ValidateInvalidDoesNotStore(t *testing.T) {
	env := newV2Server(t)
	spec := assetLifecycleSpec()
	spec["initial"] = "Nonexistent"
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, "/validate"), spec)
	if status != http.StatusOK {
		t.Fatalf("validate invalid: want 200 envelope, got %d: %v", status, resp)
	}
	if resp["valid"] != false {
		t.Errorf("validate invalid spec: want valid=false, got %v", resp["valid"])
	}
	// Nothing stored: list must be empty.
	_, listResp := doJSONRequest(t, "GET", fsmDefURL(env, ""), nil)
	defs, _ := listResp["definitions"].([]interface{})
	if len(defs) != 0 {
		t.Errorf("validate must not store: want 0 definitions, got %d", len(defs))
	}
}

// ─── List / Get ───────────────────────────────────────────────────────────────

func TestFSMDef_ListAndGet(t *testing.T) {
	env := newV2Server(t)
	_, createResp := doJSONRequest(t, "POST", fsmDefURL(env, ""), assetLifecycleSpec())
	id := int64(createResp["id"].(float64))

	_, listResp := doJSONRequest(t, "GET", fsmDefURL(env, ""), nil)
	defs, _ := listResp["definitions"].([]interface{})
	if len(defs) != 1 {
		t.Fatalf("list: want 1 definition, got %d", len(defs))
	}

	status, getResp := doJSONRequest(t, "GET", fsmDefURL(env, fmt.Sprintf("/%d", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("get def: want 200, got %d: %v", status, getResp)
	}
	spec, _ := getResp["spec"].(map[string]interface{})
	if spec["name"] != "AssetLifecycle" {
		t.Errorf("get def spec.name: want AssetLifecycle, got %v", spec["name"])
	}
}

func TestFSMDef_GetNotFound(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET", fsmDefURL(env, "/9999"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("get missing def: want 404, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM001" {
		t.Errorf("get missing def: want XOLU-FSM001, got %v", resp["error"])
	}
}

// ─── Replace / Delete ─────────────────────────────────────────────────────────

func TestFSMDef_Replace(t *testing.T) {
	env := newV2Server(t)
	_, createResp := doJSONRequest(t, "POST", fsmDefURL(env, ""), assetLifecycleSpec())
	id := int64(createResp["id"].(float64))

	spec := assetLifecycleSpec()
	spec["description"] = "updated description"
	status, resp := doJSONRequest(t, "PUT", fsmDefURL(env, fmt.Sprintf("/%d", id)), spec)
	if status != http.StatusOK {
		t.Fatalf("replace def: want 200, got %d: %v", status, resp)
	}

	_, getResp := doJSONRequest(t, "GET", fsmDefURL(env, fmt.Sprintf("/%d", id)), nil)
	gotSpec, _ := getResp["spec"].(map[string]interface{})
	if gotSpec["description"] != "updated description" {
		t.Errorf("replace did not persist: got description %v", gotSpec["description"])
	}
}

func TestFSMDef_DeleteAlwaysPermitted(t *testing.T) {
	env := newV2Server(t)
	_, createResp := doJSONRequest(t, "POST", fsmDefURL(env, ""), assetLifecycleSpec())
	id := int64(createResp["id"].(float64))

	status, _ := doJSONRequest(t, "DELETE", fsmDefURL(env, fmt.Sprintf("/%d", id)), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete def: want 204, got %d", status)
	}

	status, resp := doJSONRequest(t, "GET", fsmDefURL(env, fmt.Sprintf("/%d", id)), nil)
	if status != http.StatusNotFound {
		t.Errorf("get after delete: want 404, got %d: %v", status, resp)
	}
}

func TestFSMDef_DeleteNotFound(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "DELETE", fsmDefURL(env, "/9999"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("delete missing def: want 404, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM001" {
		t.Errorf("delete missing def: want XOLU-FSM001, got %v", resp["error"])
	}
}
