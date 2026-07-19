// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_behaviour_test.go — S7/S8: FSM *behavioural* correctness.
//
// The handler tests prove each endpoint works in isolation. This suite proves
// the machine behaves correctly as a state machine across multi-step walks:
// the retry-loop guard boundary, guard-sees-pre-value / history-records-post-
// value ordering, multi-source transitions, full-lifecycle traversal, history
// as a faithful append-only ledger, and overrides that actually change walk
// behaviour.

import (
	"fmt"
	"net/http"
	"testing"
)

// walk is a helper that performs a walk and returns (status, response).
func walk(t *testing.T, env *stdTestServer, id int64, input string, payload map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	body := map[string]interface{}{"input": input}
	if payload != nil {
		body["payload"] = payload
	}
	return doJSONRequest(t, "POST", fsmMachineURL(env, fmt.Sprintf("/%d/walk", id)), body)
}

// newAssetMachine creates an AssetLifecycle definition + machine and returns
// the machine id.
func newAssetMachine(t *testing.T, env *stdTestServer) int64 {
	t.Helper()
	defID := createAssetDef(t, env)
	_, resp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	return int64(resp["id"].(float64))
}

// retries reads @retries from a walk or vars response.
func retriesOf(resp map[string]interface{}) (float64, bool) {
	vars, ok := resp["vars"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	v, ok := vars["@retries"].(float64)
	return v, ok
}

// ─── Retry-loop guard boundary ────────────────────────────────────────────────
//
// This is the single most semantically delicate behaviour in the spec:
// inspection_failed (guard @retries < 3, set @retries = @retries + 1) may fire
// exactly three times. On the fourth attempt the guard 3 < 3 is false, so
// inspection_failed is rejected and only inspection_abandoned (guard
// @retries >= 3) is permitted.

func TestFSMBehaviour_RetryLoopBoundary(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)

	// Provisioning -> AwaitingInspection.
	if st, _ := walk(t, env, id, "ready_for_inspection", nil); st != http.StatusOK {
		t.Fatalf("ready_for_inspection: want 200, got %d", st)
	}

	// Three permitted failures: @retries 0->1, 1->2, 2->3.
	wantAfter := []float64{1, 2, 3}
	for i, want := range wantAfter {
		st, resp := walk(t, env, id, "inspection_failed", nil)
		if st != http.StatusOK {
			t.Fatalf("inspection_failed #%d: want 200, got %d: %v", i+1, st, resp)
		}
		got, ok := retriesOf(resp)
		if !ok || got != want {
			t.Fatalf("after inspection_failed #%d: want @retries=%v, got %v", i+1, want, resp["vars"])
		}
		// Still in AwaitingInspection (self-loop).
		if resp["current"] != "AwaitingInspection" {
			t.Fatalf("inspection_failed #%d: want stay in AwaitingInspection, got %v", i+1, resp["current"])
		}
	}

	// Fourth inspection_failed is now guard-rejected: @retries < 3 is false.
	st, resp := walk(t, env, id, "inspection_failed", nil)
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("4th inspection_failed: want 422 (guard rejects), got %d: %v", st, resp)
	}
	if errCode(resp) != "XOLU-FSM004" {
		t.Errorf("4th inspection_failed: want XOLU-FSM004, got %v", resp["error"])
	}

	// @retries must NOT have advanced past 3 on the rejected attempt.
	_, varsResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/vars", id)), nil)
	rv, _ := varsResp["@retries"].(map[string]interface{})
	if rv["value"].(float64) != 3 {
		t.Errorf("@retries after rejected 4th failure: want 3 (unchanged), got %v", rv["value"])
	}

	// inspection_abandoned is now permitted (guard @retries >= 3) and routes
	// back to Provisioning.
	st, resp = walk(t, env, id, "inspection_abandoned", nil)
	if st != http.StatusOK {
		t.Fatalf("inspection_abandoned: want 200, got %d: %v", st, resp)
	}
	if resp["current"] != "Provisioning" {
		t.Errorf("inspection_abandoned target: want Provisioning, got %v", resp["current"])
	}
}

// inspection_abandoned must be rejected before the retry budget is spent:
// guard @retries >= 3 is false at @retries < 3.
func TestFSMBehaviour_AbandonRejectedBeforeBudgetSpent(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	walk(t, env, id, "ready_for_inspection", nil)

	// One failure only: @retries = 1.
	walk(t, env, id, "inspection_failed", nil)

	// inspection_abandoned guard (@retries >= 3) is false → XOLU-FSM004.
	st, resp := walk(t, env, id, "inspection_abandoned", nil)
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("early abandon: want 422, got %d: %v", st, resp)
	}
	if errCode(resp) != "XOLU-FSM004" {
		t.Errorf("early abandon: want XOLU-FSM004, got %v", resp["error"])
	}
}

// ─── Guard sees pre-value, history records post-set value ─────────────────────
//
// On inspection_failed at @retries=2: the guard (@retries < 3) must evaluate
// against the PRE-transition value (2 < 3 = true, permitted), and the history
// entry + walk response must report the POST-set value (3).

func TestFSMBehaviour_GuardPreValueHistoryPostValue(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	walk(t, env, id, "ready_for_inspection", nil)
	walk(t, env, id, "inspection_failed", nil) // @retries 0->1
	walk(t, env, id, "inspection_failed", nil) // @retries 1->2

	// At @retries=2, guard 2<3 passes; post-set value is 3.
	st, resp := walk(t, env, id, "inspection_failed", nil)
	if st != http.StatusOK {
		t.Fatalf("3rd failure at retries=2: want 200, got %d: %v", st, resp)
	}
	got, _ := retriesOf(resp)
	if got != 3 {
		t.Errorf("walk response vars: want post-set @retries=3, got %v", got)
	}

	// History's latest entry must record the post-set snapshot (3), not 2.
	_, histResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/history", id)), nil)
	entries, _ := histResp["entries"].([]interface{})
	last, _ := entries[len(entries)-1].(map[string]interface{})
	lastVars, _ := last["vars"].(map[string]interface{})
	if lastVars["@retries"].(float64) != 3 {
		t.Errorf("history post-set snapshot: want @retries=3, got %v", lastVars["@retries"])
	}
}

// ─── Multi-source transition ──────────────────────────────────────────────────
//
// decommission has from: ["InService","Suspended"]. Both source states must
// resolve to the same transition. The Suspended path is the one no handler
// test exercises.

func TestFSMBehaviour_MultiSourceTransitionFromSuspended(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)

	// Drive to Suspended: -> AwaitingInspection -> InService -> Suspended.
	walk(t, env, id, "ready_for_inspection", nil)
	walk(t, env, id, "inspection_passed", map[string]interface{}{"result": "pass", "technician": "a"})
	st, resp := walk(t, env, id, "suspend", nil)
	if st != http.StatusOK || resp["current"] != "Suspended" {
		t.Fatalf("suspend: want 200 -> Suspended, got %d -> %v", st, resp["current"])
	}

	// decommission from Suspended (the multi-source branch).
	st, resp = walk(t, env, id, "decommission", nil)
	if st != http.StatusOK {
		t.Fatalf("decommission from Suspended: want 200, got %d: %v", st, resp)
	}
	if resp["current"] != "Decommissioned" || resp["terminal"] != true {
		t.Errorf("decommission from Suspended: want Decommissioned/terminal, got %v/%v", resp["current"], resp["terminal"])
	}
	outputs, _ := resp["outputs"].([]interface{})
	if len(outputs) != 1 || outputs[0] != "asset_decommissioned" {
		t.Errorf("decommission output: want [asset_decommissioned], got %v", outputs)
	}
}

func TestFSMBehaviour_MultiSourceTransitionFromInService(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	walk(t, env, id, "ready_for_inspection", nil)
	walk(t, env, id, "inspection_passed", map[string]interface{}{"result": "pass", "technician": "a"})

	// decommission directly from InService (the other source).
	st, resp := walk(t, env, id, "decommission", nil)
	if st != http.StatusOK || resp["current"] != "Decommissioned" {
		t.Fatalf("decommission from InService: want 200 -> Decommissioned, got %d -> %v", st, resp["current"])
	}
}

// ─── Suspend / reinstate cycle ────────────────────────────────────────────────

func TestFSMBehaviour_SuspendReinstateCycle(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	walk(t, env, id, "ready_for_inspection", nil)
	walk(t, env, id, "inspection_passed", map[string]interface{}{"result": "pass", "technician": "a"})

	// InService -> Suspended -> InService -> Suspended (cycle twice).
	for i := 0; i < 2; i++ {
		st, resp := walk(t, env, id, "suspend", nil)
		if st != http.StatusOK || resp["current"] != "Suspended" {
			t.Fatalf("suspend cycle %d: got %d -> %v", i, st, resp["current"])
		}
		st, resp = walk(t, env, id, "reinstate", nil)
		if st != http.StatusOK || resp["current"] != "InService" {
			t.Fatalf("reinstate cycle %d: got %d -> %v", i, st, resp["current"])
		}
	}
}

// ─── Full lifecycle traversal + history ledger fidelity ───────────────────────

func TestFSMBehaviour_FullLifecycleAndHistoryLedger(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)

	// A representative full run including a failure and recovery:
	// Provisioning -> AwaitingInspection -> (fail) -> (pass) -> InService
	//   -> Suspended -> InService -> Decommissioned
	steps := []struct {
		input   string
		payload map[string]interface{}
		want    string
	}{
		{"ready_for_inspection", nil, "AwaitingInspection"},
		{"inspection_failed", nil, "AwaitingInspection"},
		{"inspection_passed", map[string]interface{}{"result": "pass", "technician": "a"}, "InService"},
		{"suspend", nil, "Suspended"},
		{"reinstate", nil, "InService"},
		{"decommission", nil, "Decommissioned"},
	}
	for i, s := range steps {
		st, resp := walk(t, env, id, s.input, s.payload)
		if st != http.StatusOK {
			t.Fatalf("step %d (%s): want 200, got %d: %v", i, s.input, st, resp)
		}
		if resp["current"] != s.want {
			t.Fatalf("step %d (%s): want %s, got %v", i, s.input, s.want, resp["current"])
		}
	}

	// History ledger: 1 creation entry + 6 walk entries, ordered, with the
	// from/to chain forming an unbroken path.
	_, histResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/history", id)), nil)
	entries, _ := histResp["entries"].([]interface{})
	if len(entries) != 7 {
		t.Fatalf("history ledger: want 7 entries, got %d", len(entries))
	}
	// Entry 0 is creation (from=nil, to=Provisioning).
	e0, _ := entries[0].(map[string]interface{})
	if e0["from"] != nil || e0["to"] != "Provisioning" {
		t.Errorf("creation entry: want nil->Provisioning, got %v->%v", e0["from"], e0["to"])
	}
	// Each subsequent entry's `from` must equal the previous entry's `to`.
	for i := 1; i < len(entries); i++ {
		prev, _ := entries[i-1].(map[string]interface{})
		cur, _ := entries[i].(map[string]interface{})
		if cur["from"] != prev["to"] {
			t.Errorf("ledger break at entry %d: from=%v but previous to=%v", i, cur["from"], prev["to"])
		}
	}
	// Final entry lands on the terminal state.
	last, _ := entries[len(entries)-1].(map[string]interface{})
	if last["to"] != "Decommissioned" {
		t.Errorf("final ledger entry: want to=Decommissioned, got %v", last["to"])
	}
}

// ─── Override actually changes walk behaviour ─────────────────────────────────
//
// Two machines from the same definition: one with @retries guard widened to
// < 5 via override. The overridden machine must permit a 4th and 5th failure
// that the default machine rejects — proving the override changes runtime
// behaviour, not just that it is accepted at creation.

func TestFSMBehaviour_OverrideChangesGuardBehaviour(t *testing.T) {
	env := newV2Server(t)
	defID := createAssetDef(t, env)

	// Default machine.
	_, dResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	defaultID := int64(dResp["id"].(float64))

	// Overridden machine: inspection_failed guard @retries < 5.
	_, oResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{
		"definition": defID,
		"overrides": map[string]interface{}{
			"transitions": map[string]interface{}{
				"inspection_failed": map[string]interface{}{"guard": "@retries < 5"},
			},
		},
	})
	overrideID := int64(oResp["id"].(float64))

	// Both reach AwaitingInspection and fail 3 times (permitted on both).
	for _, id := range []int64{defaultID, overrideID} {
		walk(t, env, id, "ready_for_inspection", nil)
		for i := 0; i < 3; i++ {
			if st, _ := walk(t, env, id, "inspection_failed", nil); st != http.StatusOK {
				t.Fatalf("machine %d failure %d: want 200, got %d", id, i, st)
			}
		}
	}

	// 4th failure: default rejects (guard < 3), override permits (guard < 5).
	if st, _ := walk(t, env, defaultID, "inspection_failed", nil); st != http.StatusUnprocessableEntity {
		t.Errorf("default 4th failure: want 422, got %d", st)
	}
	st, resp := walk(t, env, overrideID, "inspection_failed", nil)
	if st != http.StatusOK {
		t.Fatalf("override 4th failure: want 200, got %d: %v", st, resp)
	}
	got, _ := retriesOf(resp)
	if got != 4 {
		t.Errorf("override @retries after 4th failure: want 4, got %v", got)
	}
}
