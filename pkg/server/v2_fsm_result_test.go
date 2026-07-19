// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_result_test.go — GET /fsm/machine/{id}/result.
//
// The result endpoint reports whether a machine has reached a terminal (STOP)
// state and, when it has, its final state, final variables, and the output
// emitted by the terminating transition. These tests pin both halves: a
// non-terminal machine must NOT report a final output, and a terminal machine
// must report the correct one.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func resultURL(env *stdTestServer, id int64) string {
	return fsmMachineURL(env, fmt.Sprintf("/%d/result", id))
}

// A freshly created machine has not stopped: terminal is false and no
// final_output is present. This is the control that makes the terminal
// assertion load-bearing — if the endpoint always reported an output, this
// would fail.
func TestResult_NonTerminalHasNoFinalOutput(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)

	st, resp := doJSONRequest(t, "GET", resultURL(env, id), nil)
	if st != http.StatusOK {
		t.Fatalf("result: want 200, got %d: %v", st, resp)
	}
	if resp["terminal"] != false {
		t.Errorf("fresh machine should be non-terminal, got terminal=%v", resp["terminal"])
	}
	if _, present := resp["final_output"]; present {
		t.Errorf("non-terminal machine must not report final_output, got %v", resp["final_output"])
	}
	// State and vars are still reported (current, non-final).
	if resp["state"] != "Provisioning" {
		t.Errorf("want current state Provisioning, got %v", resp["state"])
	}
}

// Driving the asset lifecycle to its terminal Decommissioned state must yield
// terminal=true, the final state, and the asset_decommissioned output emitted
// by the terminating transition.
func TestResult_TerminalReportsFinalStateAndOutput(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)

	// Provisioning -> AwaitingInspection -> InService -> Decommissioned.
	walk(t, env, id, "ready_for_inspection", nil)
	walk(t, env, id, "inspection_passed", map[string]interface{}{"result": "pass", "technician": "alice"})
	walk(t, env, id, "decommission", nil)

	st, resp := doJSONRequest(t, "GET", resultURL(env, id), nil)
	if st != http.StatusOK {
		t.Fatalf("result: want 200, got %d: %v", st, resp)
	}
	if resp["terminal"] != true {
		t.Fatalf("machine should be terminal after decommission, got terminal=%v", resp["terminal"])
	}
	if resp["state"] != "Decommissioned" {
		t.Errorf("final state should be Decommissioned, got %v", resp["state"])
	}
	// final_output should contain asset_decommissioned.
	raw, present := resp["final_output"]
	if !present {
		t.Fatalf("terminal machine must report final_output")
	}
	outputs := decodeOutputs(t, raw)
	if len(outputs) != 1 || outputs[0] != "asset_decommissioned" {
		t.Errorf("final_output should be [asset_decommissioned], got %v", outputs)
	}
}

// A terminal transition that emits no output reports final_output as an empty
// list, not a missing field — so a caller can distinguish "stopped, no output"
// from "not stopped".
func TestResult_TerminalWithNoOutputReportsEmpty(t *testing.T) {
	env := newV2Server(t)
	// A minimal machine whose terminal transition has no output.
	def := map[string]interface{}{
		"name":        "SilentStop",
		"initial":     "A",
		"determinism": "strict",
		"states": map[string]interface{}{
			"A":    map[string]interface{}{"terminal": false},
			"Done": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "A", "input": "go", "to": "Done"}, // no output
		},
	}
	st, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), def)
	if st != http.StatusCreated {
		t.Fatalf("create SilentStop: %d %v", st, resp)
	}
	defID := int64(resp["id"].(float64))
	_, m := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(m["id"].(float64))
	walk(t, env, id, "go", nil)

	_, rResp := doJSONRequest(t, "GET", resultURL(env, id), nil)
	if rResp["terminal"] != true {
		t.Fatalf("should be terminal, got %v", rResp["terminal"])
	}
	raw, present := rResp["final_output"]
	if !present {
		t.Fatalf("terminal machine must report final_output (empty list), got missing")
	}
	if outputs := decodeOutputs(t, raw); len(outputs) != 0 {
		t.Errorf("silent terminal transition should report empty final_output, got %v", outputs)
	}
}

func TestResult_NotFound(t *testing.T) {
	env := newV2Server(t)
	st, resp := doJSONRequest(t, "GET", resultURL(env, 999999), nil)
	if st != http.StatusNotFound || errCode(resp) != "XOLU-FSM002" {
		t.Errorf("missing machine: want 404/XOLU-FSM002, got %d/%v", st, resp["error"])
	}
}

// decodeOutputs normalizes the final_output field (which arrives as a JSON
// value) into a []string for assertion.
func decodeOutputs(t *testing.T, raw interface{}) []string {
	t.Helper()
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			t.Fatalf("final_output not decodable: %v (%q)", err, v)
		}
		return out
	default:
		t.Fatalf("unexpected final_output type %T: %v", raw, raw)
		return nil
	}
}
