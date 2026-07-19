// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_determinism_test.go — batch 1: mandatory determinism declaration.
//
// Every definition must declare determinism as one of "strict", "loose", or
// "firstmatch". A definition without an explicit, valid level is rejected and
// cannot be created or instantiated. "strict" additionally requires at most one
// transition per (state, input). The "loose" exclusivity recognizer is a later
// batch; here "loose" is accepted without exclusivity verification.

import (
	"net/http"
	"testing"
)

// minimal valid one-edge definition with a placeholder for the determinism
// level, so each test can set the level under examination.
func detDef(level string) map[string]interface{} {
	d := map[string]interface{}{
		"name":    "DetTest",
		"initial": "A",
		"states": map[string]interface{}{
			"A": map[string]interface{}{"terminal": false},
			"B": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "A", "input": "go", "to": "B"},
		},
	}
	if level != "" {
		d["determinism"] = level
	}
	return d
}

// two edges on the same (state, input) — only valid for loose/firstmatch.
func detMultiEdgeDef(level string) map[string]interface{} {
	d := map[string]interface{}{
		"name":    "DetMulti",
		"initial": "A",
		"states": map[string]interface{}{
			"A":  map[string]interface{}{"terminal": false},
			"B1": map[string]interface{}{"terminal": true},
			"B2": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "A", "input": "go", "to": "B1", "guard": "@x = 1"},
			{"from": "A", "input": "go", "to": "B2", "guard": "@x = 2"},
		},
	}
	if level != "" {
		d["determinism"] = level
	}
	return d
}

func TestDeterminism_MissingFieldRejected(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), detDef(""))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("missing determinism: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM006" {
		t.Errorf("missing determinism: want XOLU-FSM006, got %v", resp["error"])
	}
}

func TestDeterminism_InvalidValueRejected(t *testing.T) {
	env := newV2Server(t)
	for _, bad := range []string{"deterministic", "none", "STRICT", "loose ", "true", "1"} {
		status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), detDef(bad))
		if status != http.StatusUnprocessableEntity {
			t.Errorf("invalid determinism %q: want 422, got %d", bad, status)
			continue
		}
		if errCode(resp) != "XOLU-FSM006" {
			t.Errorf("invalid determinism %q: want XOLU-FSM006, got %v", bad, resp["error"])
		}
	}
}

func TestDeterminism_ValidLevelsAccepted(t *testing.T) {
	env := newV2Server(t)
	for _, level := range []string{"strict", "loose", "firstmatch"} {
		status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), detDef(level))
		if status != http.StatusCreated {
			t.Errorf("determinism %q (single edge): want 201, got %d: %v", level, status, resp)
		}
	}
}

func TestDeterminism_StrictRejectsMultipleEdges(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), detMultiEdgeDef("strict"))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("strict + multi-edge: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM006" {
		t.Errorf("strict + multi-edge: want XOLU-FSM006, got %v", resp["error"])
	}
}

func TestDeterminism_LooseAndFirstMatchAllowMultipleEdges(t *testing.T) {
	env := newV2Server(t)
	for _, level := range []string{"loose", "firstmatch"} {
		status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), detMultiEdgeDef(level))
		if status != http.StatusCreated {
			t.Errorf("determinism %q (multi edge): want 201, got %d: %v", level, status, resp)
		}
	}
}

func TestDeterminism_StrictSingleEdgeAccepted(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), detDef("strict"))
	if status != http.StatusCreated {
		t.Fatalf("strict + single edge: want 201, got %d: %v", status, resp)
	}
}

func TestDeterminism_LevelReportedInAnalysis(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, "/validate"), detDef("firstmatch"))
	if status != http.StatusOK {
		t.Fatalf("validate: want 200, got %d: %v", status, resp)
	}
	analysis, ok := resp["analysis"].(map[string]interface{})
	if !ok {
		t.Fatalf("no analysis block: %v", resp)
	}
	if analysis["determinism"] != "firstmatch" {
		t.Errorf("analysis determinism: want firstmatch, got %v", analysis["determinism"])
	}
}

// A definition rejected for missing determinism must not be instantiable —
// there is no definition to instantiate.
func TestDeterminism_RejectedDefinitionCannotBeInstantiated(t *testing.T) {
	env := newV2Server(t)
	status, _ := doJSONRequest(t, "POST", fsmDefURL(env, ""), detDef(""))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("setup: missing-determinism def should be rejected, got %d", status)
	}
	mStatus, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""),
		map[string]interface{}{"definition": 99999})
	if mStatus == http.StatusCreated {
		t.Errorf("instantiating a non-existent definition should fail, got 201: %v", mResp)
	}
}

// ─── loose exclusivity enforcement + smart messages ───────────────────────────

// helper: build a two-edge loose def with the given guards on (A, "go").
func looseTwoEdge(gA, gB string) map[string]interface{} {
	return map[string]interface{}{
		"name":        "LooseCheck",
		"initial":     "A",
		"determinism": "loose",
		"states": map[string]interface{}{
			"A":  map[string]interface{}{"terminal": false},
			"B1": map[string]interface{}{"terminal": true},
			"B2": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "A", "input": "go", "to": "B1", "guard": gA},
			{"from": "A", "input": "go", "to": "B2", "guard": gB},
		},
	}
}

func TestDeterminism_LooseRejectsOverlappingGuards(t *testing.T) {
	env := newV2Server(t)
	// @x >= 5 and @x <= 5 both fire at 5.
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), looseTwoEdge("@x >= 5", "@x <= 5"))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("loose + overlapping guards: want 422, got %d: %v", status, resp)
	}
	if errCode(resp) != "XOLU-FSM006" {
		t.Errorf("want XOLU-FSM006, got %v", resp["error"])
	}
	// The message must be informative: name both guards and say they can both
	// be true, plus offer firstmatch as the alternative.
	msg := errMessage(resp)
	for _, want := range []string{"@x >= 5", "@x <= 5", "both be true", "firstmatch"} {
		if !containsStr(msg, want) {
			t.Errorf("message missing %q; got: %s", want, msg)
		}
	}
}

func TestDeterminism_LooseRejectsUnrecognizedGuardWithGuidance(t *testing.T) {
	env := newV2Server(t)
	// Arithmetic guards the recognizer cannot prove exclusive.
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""),
		looseTwoEdge("@a + @b > 10", "@a + @b <= 10"))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("loose + unrecognized: want 422, got %d: %v", status, resp)
	}
	msg := errMessage(resp)
	// Must guide the author toward a recognized form or firstmatch, not just
	// say "invalid".
	for _, want := range []string{"not in a recognized", "firstmatch"} {
		if !containsStr(msg, want) {
			t.Errorf("guidance message missing %q; got: %s", want, msg)
		}
	}
}

func TestDeterminism_LooseRejectsUnguardedEdgeInGroup(t *testing.T) {
	env := newV2Server(t)
	// One edge has no guard → always fires → can't be exclusive.
	def := looseTwoEdge("@x = 1", "")
	// remove the guard key from the second transition to make it truly unguarded
	tx := def["transitions"].([]map[string]interface{})
	delete(tx[1], "guard")
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), def)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("loose + unguarded edge: want 422, got %d: %v", status, resp)
	}
	msg := errMessage(resp)
	if !containsStr(msg, "no guard") && !containsStr(msg, "always fires") {
		t.Errorf("unguarded-edge message should explain the always-fires problem; got: %s", msg)
	}
}

func TestDeterminism_LooseAcceptsProvablyExclusive(t *testing.T) {
	env := newV2Server(t)
	// Distinct equality — provably exclusive.
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), looseTwoEdge("@x = 1", "@x = 2"))
	if status != http.StatusCreated {
		t.Fatalf("loose + exclusive guards: want 201, got %d: %v", status, resp)
	}
	// validate endpoint should report exclusivity_verified.
	vStatus, vResp := doJSONRequest(t, "POST", fsmDefURL(env, "/validate"), looseTwoEdge("@x = 1", "@x = 2"))
	if vStatus != http.StatusOK {
		t.Fatalf("validate: want 200, got %d", vStatus)
	}
	analysis, _ := vResp["analysis"].(map[string]interface{})
	if analysis["exclusivity_verified"] != true {
		t.Errorf("want exclusivity_verified=true, got %v", analysis["exclusivity_verified"])
	}
}
