package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestS11_CanonicalPipeline is the canonical Part-1 integration test named in
// API_V2_DEVELOPMENT_PLAN.md as the definition of done: a single /commit that
// carries an embedded fsm_walk, whose guard passes, which transitions state and
// emits a Mealy output, where a registered event def matches that output and
// delivers a webhook notification — asserted end-to-end as one pipeline.
//
// If this test passes, Part 1 has achieved its goal: the document store, the
// FSM subsystem, the commit atomicity contract, and the event subsystem compose
// into a single reactive operation.
//
// The chain, each link asserted:
//
//	/commit { update + fsm_walk(inspection_passed) }
//	  -> commit applies atomically (entity written AND walk applied)
//	  -> guard "payload.result = 'pass' AND payload.technician != ''" passes
//	  -> transition AwaitingInspection -> InService
//	  -> Mealy output "asset_activated" emitted
//	  -> registered fsm.output event def matches the output
//	  -> webhook receives the notification (origin + message)
func TestS11_CanonicalPipeline(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)

	// A registered event def: on fsm.output, deliver a jsonplate carrying the
	// output, the destination state, and the machine's definition name — so the
	// assertion exercises output matching, the definition namespace, and
	// delivery together.
	defineSub(t, env, "fsm.output", "webhook", map[string]interface{}{
		"url": srv.URL,
		"jsonplate": map[string]interface{}{
			"output":   map[string]interface{}{"$ref": "output"},
			"machine":  map[string]interface{}{"$ref": "machine_id"},
			"def_name": map[string]interface{}{"$ref": "definition.name"},
		},
	})

	// Machine positioned at AwaitingInspection (the state from which
	// inspection_passed fires the output-producing transition).
	id := newAssetMachine(t, env)
	st, resp := walk(t, env, id, "ready_for_inspection", nil)
	if st != http.StatusOK {
		t.Fatalf("setup walk: want 200, got %d: %v", st, resp)
	}

	// The canonical operation: one atomic /commit that writes an entity AND
	// runs the FSM walk whose guard must pass to produce the output.
	commitBody := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "asset",
			"id":     7700,
			"data":   map[string]interface{}{"state": "inspection_done"},
		},
		"fsm_walk": map[string]interface{}{
			"machine": id,
			"input":   "inspection_passed",
			"payload": map[string]interface{}{"result": "pass", "technician": "s11"},
		},
	}
	st, commitResp := doJSONRequest(t, "POST", env.ts.URL+commitURL("default"), commitBody)

	// --- Link 1: the commit applied atomically (entity write + walk together). ---
	if st != http.StatusOK {
		t.Fatalf("commit: want 200, got %d: %v", st, commitResp)
	}
	walkRes, ok := commitResp["fsm_walk"].(map[string]interface{})
	if !ok {
		t.Fatalf("commit response missing fsm_walk result: %v", commitResp)
	}

	// --- Link 2: the guard passed -> the transition occurred. ---
	if walkRes["current"] != "InService" {
		t.Fatalf("guard/transition: want current InService, got %v (guard may have rejected)", walkRes["current"])
	}
	if walkRes["previous"] != "AwaitingInspection" {
		t.Errorf("transition from-state: want AwaitingInspection, got %v", walkRes["previous"])
	}

	// --- Link 3: the Mealy output was emitted by the transition. ---
	outputs, _ := walkRes["outputs"].([]interface{})
	foundOutput := false
	for _, o := range outputs {
		if o == "asset_activated" {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatalf("output emission: want asset_activated in walk outputs, got %v", outputs)
	}

	// --- Link 4: the registered event def matched and the webhook received it. ---
	deadline := time.Now().Add(3 * time.Second)
	var msg map[string]interface{}
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		for _, c := range rec.calls {
			if m := msgOf(c); m["output"] == "asset_activated" {
				msg = m
			}
		}
		rec.mu.Unlock()
		if msg != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if msg == nil {
		rec.mu.Lock()
		got := rec.calls
		rec.mu.Unlock()
		t.Fatalf("event def did not deliver fsm.output notification to webhook; got %v", got)
	}

	// --- Link 5: the delivered notification carries the correct, resolved content. ---
	if msg["machine"] != float64(id) {
		t.Errorf("delivered machine: want %d, got %v", id, msg["machine"])
	}
	if msg["def_name"] != "AssetLifecycle" {
		t.Errorf("delivered definition.name: want AssetLifecycle, got %v", msg["def_name"])
	}

	// --- Link 6: the delivery carries xolu's origin provenance. ---
	var originCall map[string]interface{}
	rec.mu.Lock()
	for _, c := range rec.calls {
		if m := msgOf(c); m["output"] == "asset_activated" {
			originCall = originOf(c)
		}
	}
	rec.mu.Unlock()
	if originCall == nil {
		t.Fatal("delivered notification missing origin block")
	}
	if originCall["agent"] != "xolu" {
		t.Errorf("origin.agent: want xolu, got %v", originCall["agent"])
	}
	if originCall["event_latch_kind"] != "fsm.output" {
		t.Errorf("origin.event_latch_kind: want fsm.output, got %v", originCall["event_latch_kind"])
	}
	if originCall["event_latch_source"] != "fsm/AwaitingInspection:inspection_passed:InService" {
		t.Errorf("origin.event_latch_source: want fsm/AwaitingInspection:inspection_passed:InService, got %v", originCall["event_latch_source"])
	}
	if _, ok := originCall["fired_at"]; !ok {
		t.Error("origin.fired_at missing")
	}
}
