package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestJsonplate_FSMOutputDeliversRenderedPayload verifies the jsonplate body
// path end-to-end: an event def whose webhook config carries a structured
// "jsonplate" (with $ref path references into event.data) delivers a rendered
// payload — paths resolved against the firing's data — to the webhook.
//
// This exercises the new jsonplate dispatch path specifically (distinct from
// the {{...}} body-string path), asserting on the actual resolved content.
func TestJsonplate_FSMOutputDeliversRenderedPayload(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)
	// jsonplate config: a structured body with references into event.data.
	// fsm.output events carry data.output and data.machine_id.
	defineSub(t, env, "fsm.output", "webhook", map[string]interface{}{
		"url": srv.URL,
		"jsonplate": map[string]interface{}{
			"kind":    "fsm-output",
			"output":  map[string]interface{}{"$ref": "output"},
			"machine": map[string]interface{}{"$ref": "machine_id"},
			"literal": "unchanged",
		},
	})

	id := newAssetMachine(t, env)
	walk(t, env, id, "ready_for_inspection", nil)
	walk(t, env, id, "inspection_passed", map[string]interface{}{"result": "pass", "technician": "a"})

	// asset_activated fires on the inspection_passed transition.
	deadline := time.Now().Add(2 * time.Second)
	var match map[string]interface{}
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		for _, c := range rec.calls {
			if m := msgOf(c); m["output"] == "asset_activated" {
				match = m
			}
		}
		rec.mu.Unlock()
		if match != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if match == nil {
		rec.mu.Lock()
		got := rec.calls
		rec.mu.Unlock()
		t.Fatalf("jsonplate payload for asset_activated did not reach webhook; got %v", got)
	}

	// Assert the rendered structure: literals preserved, refs resolved.
	if match["kind"] != "fsm-output" {
		t.Errorf("kind: want fsm-output, got %v", match["kind"])
	}
	if match["literal"] != "unchanged" {
		t.Errorf("literal: want unchanged, got %v", match["literal"])
	}
	if match["output"] != "asset_activated" {
		t.Errorf("output ref: want asset_activated, got %v", match["output"])
	}
	// machine_id is numeric in the event data; JSON round-trips it as a number.
	if match["machine"] != float64(id) {
		t.Errorf("machine ref: want %v, got %v (%T)", float64(id), match["machine"], match["machine"])
	}
}

// TestJsonplate_CommitAppliedDeliversAffectedRefs verifies a commit.applied
// event def with a jsonplate can deliver the affected-entity REFs — the
// structured payload the old flat {{...}} substitution could not render
// cleanly — with path references reaching into the affected array.
func TestJsonplate_CommitAppliedDeliversAffectedRefs(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)
	defineSub(t, env, "commit.applied", "webhook", map[string]interface{}{
		"url": srv.URL,
		"jsonplate": map[string]interface{}{
			"first_entity": map[string]interface{}{"$ref": "affected[0].ref.entity"},
			"first_id":     map[string]interface{}{"$ref": "affected[0].ref.id"},
			"created":      map[string]interface{}{"$ref": "affected[0].created"},
		},
	})

	// A commit that creates an entity. The endpoint requires at least one of
	// append/timeseries/fsm_walk in addition to the update, so include an
	// append (which also adds a second affected entity).
	commitBody := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "asset",
			"id":     7001,
			"data":   map[string]interface{}{"state": "new"},
		},
		"append": []map[string]interface{}{
			{
				"entity": "audit_log",
				"data":   map[string]interface{}{"note": "created"},
			},
		},
	}
	st, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/commit", env.ts.URL), commitBody)
	if st != http.StatusOK {
		t.Fatalf("commit: want 200, got %d: %v", st, resp)
	}

	deadline := time.Now().Add(2 * time.Second)
	var match map[string]interface{}
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		for _, c := range rec.calls {
			if m := msgOf(c); m["first_entity"] == "asset" {
				match = m
			}
		}
		rec.mu.Unlock()
		if match != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if match == nil {
		rec.mu.Lock()
		got := rec.calls
		rec.mu.Unlock()
		t.Fatalf("commit.applied jsonplate did not reach webhook with affected refs; got %v", got)
	}
	if match["first_id"] != float64(7001) {
		t.Errorf("first_id ref: want 7001, got %v", match["first_id"])
	}
	if match["created"] != true {
		t.Errorf("created ref: want true, got %v", match["created"])
	}
}
