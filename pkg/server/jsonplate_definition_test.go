package server_test

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestJsonplate_DefinitionNamespaceResolves verifies that the FSM definition
// spec is forwarded into the event data as a "definition" namespace, so a
// jsonplate can reference definition facts (machine name, initial state, a
// destination state's terminal flag) alongside the firing's payload and vars.
//
// This is the schema-as-third-context capability: definition references resolve
// when the definition is present, and (per jsonplate's null-on-missing-path
// rule) degrade to null when a referenced path is absent.
func TestJsonplate_DefinitionNamespaceResolves(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)
	defineSub(t, env, "fsm.step", "webhook", map[string]interface{}{
		"url": srv.URL,
		"jsonplate": map[string]interface{}{
			"to":                map[string]interface{}{"$ref": "current"},
			"machine_name":      map[string]interface{}{"$ref": "definition.name"},
			"initial_state":     map[string]interface{}{"$ref": "definition.initial"},
			"dest_is_terminal":  map[string]interface{}{"$ref": "definition.states.AwaitingInspection.terminal"},
			"term_state_flag":   map[string]interface{}{"$ref": "definition.states.Decommissioned.terminal"},
			"absent_on_purpose": map[string]interface{}{"$ref": "definition.states.NoSuchState.terminal"},
		},
	})

	id := newAssetMachine(t, env)
	walk(t, env, id, "ready_for_inspection", nil) // Provisioning -> AwaitingInspection, a step

	deadline := time.Now().Add(2 * time.Second)
	var msg map[string]interface{}
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		for _, c := range rec.calls {
			if m := msgOf(c); m["to"] == "AwaitingInspection" {
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
		t.Fatal("fsm.step with definition refs did not reach webhook")
	}

	if msg["machine_name"] != "AssetLifecycle" {
		t.Errorf("definition.name: want AssetLifecycle, got %v", msg["machine_name"])
	}
	if msg["initial_state"] != "Provisioning" {
		t.Errorf("definition.initial: want Provisioning, got %v", msg["initial_state"])
	}
	if msg["dest_is_terminal"] != false {
		t.Errorf("definition.states.AwaitingInspection.terminal: want false, got %v", msg["dest_is_terminal"])
	}
	if msg["term_state_flag"] != true {
		t.Errorf("definition.states.Decommissioned.terminal: want true, got %v", msg["term_state_flag"])
	}
	// Absent path must degrade to null (the documented mitigation), present as a
	// JSON null -> Go nil after unmarshal.
	if v, ok := msg["absent_on_purpose"]; !ok || v != nil {
		t.Errorf("absent definition path: want null, got %v (present=%v)", v, ok)
	}
}
