// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_walk_adversarial_test.go — adversarial battery at the walk level.
//
// These attack the walk engine rather than the expression evaluator: guard
// errors mid-walk and rollback, multiple simultaneously-true guards (selection
// order), set clauses referencing undefined variables, empty/whitespace input,
// re-entry into a terminal state, and walks on machines whose definition has
// changed underneath them. The expression-level battery lives in
// pkg/fsm/eval/eval_adversarial_test.go.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ambiguousDef has two transitions on the same input whose guards are BOTH
// true. The walk must deterministically fire the first in definition order.
func ambiguousGuardDef() map[string]interface{} {
	return map[string]interface{}{
		"name":        "Ambiguous",
		"initial":     "Start",
		"determinism": "firstmatch",
		"states": map[string]interface{}{
			"Start":  map[string]interface{}{"terminal": false},
			"First":  map[string]interface{}{"terminal": true},
			"Second": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "Start", "input": "go", "to": "First", "guard": "1 = 1"},  // always true
			{"from": "Start", "input": "go", "to": "Second", "guard": "2 = 2"}, // also always true
		},
	}
}

func TestWalkAdversarial_AmbiguousGuardsFireFirstInOrder(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), ambiguousGuardDef())
	if status != http.StatusCreated {
		t.Fatalf("create ambiguous def: %d %v", status, resp)
	}
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	// Both guards are true; the first transition (to "First") must win.
	st, wResp := walk(t, env, id, "go", nil)
	if st != http.StatusOK {
		t.Fatalf("ambiguous walk: want 200, got %d: %v", st, wResp)
	}
	if wResp["current"] != "First" {
		t.Errorf("ambiguous guards: want first-in-order (First), got %v", wResp["current"])
	}
}

// A guard that fails to evaluate (references something that errors) must abort
// the walk with XOLU-FSM011 and leave the machine unchanged.
func guardErrorDef() map[string]interface{} {
	return map[string]interface{}{
		"name":        "GuardError",
		"initial":     "A",
		"determinism": "strict",
		"states": map[string]interface{}{
			"A": map[string]interface{}{"terminal": false},
			"B": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			// Division by zero inside a guard — an evaluation error, not a
			// false result.
			{"from": "A", "input": "x", "to": "B", "guard": "(@a / 0) = 1"},
		},
	}
}

func TestWalkAdversarial_GuardErrorLeavesMachineUnchanged(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), guardErrorDef())
	if status != http.StatusCreated {
		// If the definition is rejected at creation for the guard, that is also
		// an acceptable outcome — the point is the error is not silently
		// swallowed. Record and stop.
		t.Skipf("guard-error def rejected at creation (acceptable): %d %v", status, resp)
	}
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	st, wResp := walk(t, env, id, "x", map[string]interface{}{})
	// Either a clean guard-evaluation error, or a guard-false rejection — but
	// never a panic or a silent state change.
	if st == http.StatusOK {
		t.Errorf("guard-error walk unexpectedly succeeded: %v", wResp)
	}
	// State must still be A.
	_, stateResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", id)), nil)
	if stateResp["state"] != "A" {
		t.Errorf("after guard-error walk: state must be unchanged (A), got %v", stateResp["state"])
	}
}

// A set clause referencing an undefined variable must not corrupt state. The
// walk either errors cleanly or treats the missing var as null; either way the
// transition's atomicity holds.
func setUndefinedVarDef() map[string]interface{} {
	return map[string]interface{}{
		"name":        "SetUndef",
		"initial":     "A",
		"determinism": "strict",
		"states": map[string]interface{}{
			"A": map[string]interface{}{"terminal": false},
			"B": map[string]interface{}{"terminal": true},
		},
		"variables": map[string]interface{}{
			"@known": map[string]interface{}{"type": "int", "default": 0},
		},
		"transitions": []map[string]interface{}{
			{"from": "A", "input": "x", "to": "B",
				"set": map[string]string{"@known": "@undefined_var + 1"}},
		},
	}
}

func TestWalkAdversarial_SetUndefinedVarDoesNotCorrupt(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), setUndefinedVarDef())
	if status != http.StatusCreated {
		t.Skipf("set-undef def rejected at creation (acceptable): %d %v", status, resp)
	}
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	st, wResp := walk(t, env, id, "x", nil)
	// Acceptable outcomes: success with @known set to some value, or a clean
	// error. Not acceptable: panic (caught by the test harness) or partial
	// state change. If it succeeded, the machine must be in B atomically.
	if st == http.StatusOK {
		if wResp["current"] != "B" {
			t.Errorf("set-undef succeeded but state is %v, not B", wResp["current"])
		}
	} else {
		_, stateResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", id)), nil)
		if stateResp["state"] != "A" {
			t.Errorf("set-undef errored but state advanced to %v (should be A)", stateResp["state"])
		}
	}
}

// Empty / whitespace input must be handled as "no such transition", never as a
// match.
func TestWalkAdversarial_EmptyInput(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)

	for _, in := range []string{"", "   "} {
		st, resp := walk(t, env, id, in, nil)
		if st == http.StatusOK {
			t.Errorf("empty/whitespace input %q unexpectedly walked: %v", in, resp)
		}
	}
	// Machine unmoved.
	_, stateResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", id)), nil)
	if stateResp["state"] != "Provisioning" {
		t.Errorf("after empty inputs: state must be Provisioning, got %v", stateResp["state"])
	}
}

// An unknown input symbol (not present anywhere in the definition) is a clean
// structural rejection.
func TestWalkAdversarial_UnknownInputSymbol(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	st, resp := walk(t, env, id, "this_is_not_a_real_input", nil)
	if st != http.StatusConflict {
		t.Fatalf("unknown input: want 409, got %d: %v", st, resp)
	}
	if errCode(resp) != "XOLU-FSM003" {
		t.Errorf("unknown input: want XOLU-FSM003, got %v", resp["error"])
	}
}

// Re-walking a machine already in a terminal state is always XOLU-FSM005,
// regardless of which input is sent.
func TestWalkAdversarial_TerminalReentryAlwaysRejected(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	// Drive to terminal.
	walk(t, env, id, "ready_for_inspection", nil)
	walk(t, env, id, "inspection_passed", map[string]interface{}{"result": "pass", "technician": "a"})
	walk(t, env, id, "decommission", nil)

	for _, in := range []string{"suspend", "reinstate", "decommission", "ready_for_inspection"} {
		st, resp := walk(t, env, id, in, nil)
		if st != http.StatusConflict || errCode(resp) != "XOLU-FSM005" {
			t.Errorf("terminal re-entry with %q: want 409/XOLU-FSM005, got %d/%v", in, st, resp["error"])
		}
	}
}

// Walking after the source definition is deleted must still work (snapshot
// model) — the walk reads the machine snapshot, not the definition.
func TestWalkAdversarial_WalkAfterDefinitionDeleted(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	// Delete the definition, then walk.
	doJSONRequest(t, "DELETE", fsmDefURL(env, fmt.Sprintf("/%d", defID)), nil)

	st, resp := walk(t, env, id, "ready_for_inspection", nil)
	if st != http.StatusOK {
		t.Fatalf("walk after def delete: want 200 (snapshot model), got %d: %v", st, resp)
	}
	if resp["current"] != "AwaitingInspection" {
		t.Errorf("walk after def delete: want AwaitingInspection, got %v", resp["current"])
	}
}

// ─── Guard-syntax warning and payload key validation (Seam AMS report) ────
//
// Reported by the Seam AMS team (2026-08-04): xolu's guard syntax is
// T-SQL, where '...' is a string literal and "..." is a quoted
// IDENTIFIER -- the opposite of JSON/JavaScript convention. A guard
// written payload.x != "" parses without error but silently compares
// against nothing, never what the author meant. Horacio's own
// decision, directly relevant to why auto-correcting instead of
// warning was rejected: key names with spaces (and anything else
// outside strict-identifier characters) are illegal in xolu, closing
// the one legitimate reason a double-quoted identifier could ever
// have been needed in a guard (payload."odd key").

func TestValidateDefinition_DoubleQuotedGuard_WarnsWithoutRejecting(t *testing.T) {
	env := newV2Server(t)
	spec := map[string]interface{}{
		"name":        "guard_syntax_bad",
		"determinism": "strict",
		"initial":     "draft",
		"states": map[string]interface{}{
			"draft":     map[string]interface{}{},
			"completed": map[string]interface{}{"terminal": true},
		},
		"transitions": []interface{}{
			map[string]interface{}{
				"from": "draft", "input": "complete", "to": "completed",
				"guard": `payload.technician_signature != ""`,
			},
		},
	}
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), spec)
	if status != http.StatusCreated {
		t.Fatalf("a double-quoted guard should still be accepted (a warning, not a rejection): got %d: %v", status, resp)
	}
	analysis, ok := resp["analysis"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected an analysis object in the response: %v", resp)
	}
	warnings, _ := analysis["warnings"].([]interface{})
	found := false
	for _, w := range warnings {
		if s, ok := w.(string); ok && strings.Contains(s, "double-quoted") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a double-quoted-string warning in analysis.warnings, got: %v", warnings)
	}
}

func TestValidateDefinition_SingleQuotedGuard_NoWarning(t *testing.T) {
	env := newV2Server(t)
	spec := map[string]interface{}{
		"name":        "guard_syntax_good",
		"determinism": "strict",
		"initial":     "draft",
		"states": map[string]interface{}{
			"draft":     map[string]interface{}{},
			"completed": map[string]interface{}{"terminal": true},
		},
		"transitions": []interface{}{
			map[string]interface{}{
				"from": "draft", "input": "complete", "to": "completed",
				"guard": `payload.technician_signature != ''`,
			},
		},
	}
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), spec)
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", status, resp)
	}
	analysis, ok := resp["analysis"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected an analysis object: %v", resp)
	}
	warnings, _ := analysis["warnings"].([]interface{})
	for _, w := range warnings {
		if s, ok := w.(string); ok && strings.Contains(s, "double-quoted") {
			t.Errorf("a correctly single-quoted guard should never produce this warning, got: %v", warnings)
		}
	}
}

func TestValidateDefinition_DoubleQuotedSetClause_Warns(t *testing.T) {
	env := newV2Server(t)
	spec := map[string]interface{}{
		"name":        "set_syntax_bad",
		"determinism": "strict",
		"initial":     "draft",
		"states": map[string]interface{}{
			"draft":     map[string]interface{}{},
			"completed": map[string]interface{}{"terminal": true},
		},
		"transitions": []interface{}{
			map[string]interface{}{
				"from": "draft", "input": "complete", "to": "completed",
				"set": map[string]interface{}{"@status": `"done"`},
			},
		},
	}
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), spec)
	if status != http.StatusCreated {
		t.Fatalf("a double-quoted set clause should still be accepted: got %d: %v", status, resp)
	}
	analysis, _ := resp["analysis"].(map[string]interface{})
	warnings, _ := analysis["warnings"].([]interface{})
	found := false
	for _, w := range warnings {
		if s, ok := w.(string); ok && strings.Contains(s, "double-quoted") && strings.Contains(s, "set clause") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a double-quoted set-clause warning, got: %v", warnings)
	}
}

// TestWalk_PayloadKeyWithSpace_Rejected is the direct XOLU-FSM014
// regression: a transition payload key outside strict-identifier
// characters must be rejected before it can ever reach a guard.
func TestWalk_PayloadKeyWithSpace_Rejected(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	st, resp := walk(t, env, id, "ready_for_inspection", map[string]interface{}{
		"technician signature": "x",
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("payload key with a space: want 422, got %d: %v", st, resp)
	}
	if errCode(resp) != "XOLU-FSM014" {
		t.Errorf("payload key with a space: want XOLU-FSM014, got %v", resp["error"])
	}
}

func TestWalk_PayloadKeyValid_StillWorks(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	st, resp := walk(t, env, id, "ready_for_inspection", map[string]interface{}{
		"technician_signature": "x",
	})
	if st != http.StatusOK {
		t.Fatalf("a validly-named payload key should not be rejected: got %d: %v", st, resp)
	}
}
