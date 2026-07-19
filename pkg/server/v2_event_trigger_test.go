// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_event_trigger_test.go — S9 Batch 3: entity CRUD triggers fire events
// end-to-end. Dispatch is async (fire-and-forget post-commit), so these tests
// poll the webhook receiver / delivery log with a timeout rather than asserting
// synchronously.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// hookRecorder is a test webhook endpoint that records the bodies it receives.
type hookRecorder struct {
	mu    sync.Mutex
	calls []map[string]interface{}
}

func (h *hookRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		h.mu.Lock()
		h.calls = append(h.calls, m)
		h.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
}

func (h *hookRecorder) waitForCall(timeout time.Duration) (map[string]interface{}, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		n := len(h.calls)
		var first map[string]interface{}
		if n > 0 {
			first = h.calls[0]
		}
		h.mu.Unlock()
		if n > 0 {
			return first, true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, false
}

// msgOf returns the "message" half of a delivered payload. Every webhook
// delivery is wrapped as {"origin": {...}, "message": <body>}; the message is
// what the def's body/jsonplate produced. Returns nil if absent.
func msgOf(call map[string]interface{}) map[string]interface{} {
	m, _ := call["message"].(map[string]interface{})
	return m
}

// originOf returns the "origin" provenance half of a delivered payload.
func originOf(call map[string]interface{}) map[string]interface{} {
	o, _ := call["origin"].(map[string]interface{})
	return o
}

func TestTrigger_EntityCreatedFiresWebhook(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)
	defineSub(t, env, "entity.created", "webhook", map[string]interface{}{"url": srv.URL})

	// Creating an asset should fire entity.created → the webhook.
	id := seedAsset(t, env, "widget", "active")

	body, ok := rec.waitForCall(2 * time.Second)
	if !ok {
		t.Fatal("entity.created did not reach the webhook within timeout")
	}
	msg := msgOf(body)
	if msg["event"] != "entity.created" {
		t.Errorf("event type: want entity.created, got %v", msg["event"])
	}
	if msg["id"] != float64(id) {
		t.Errorf("event id: want %d, got %v (%T)", id, msg["id"], msg["id"])
	}
	data, _ := msg["data"].(map[string]interface{})
	if data["status"] != "active" {
		t.Errorf("event data should carry the created entity fields, got %v", data)
	}
}

func TestTrigger_EntityDeletedFiresWebhook(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)
	id := seedAsset(t, env, "doomed", "active")
	defineSub(t, env, "entity.deleted", "webhook", map[string]interface{}{"url": srv.URL})

	st, _ := doJSONRequest(t, "DELETE",
		fmt.Sprintf("%s/api/v1/tenant/default/assets/%d", env.ts.URL, id), nil)
	if st != http.StatusOK {
		t.Fatalf("delete: want 200, got %d", st)
	}
	body, ok := rec.waitForCall(2 * time.Second)
	if !ok {
		t.Fatal("entity.deleted did not reach the webhook within timeout")
	}
	msgD := msgOf(body)
	if msgD["event"] != "entity.deleted" {
		t.Errorf("event type: want entity.deleted, got %v", msgD["event"])
	}
	if msgD["id"] != float64(id) {
		t.Errorf("deleted event id: want %d, got %v (%T)", id, msgD["id"], msgD["id"])
	}
}

func TestTrigger_NonMatchingEventDoesNotFire(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)
	// Subscribe to deleted only; then create. Create must NOT fire this hook.
	defineSub(t, env, "entity.deleted", "webhook", map[string]interface{}{"url": srv.URL})
	seedAsset(t, env, "kept", "active")

	if _, ok := rec.waitForCall(500 * time.Millisecond); ok {
		t.Error("entity.created should not have triggered a entity.deleted subscription")
	}
}

// ─── Batch 4: fsm.output trigger ──────────────────────────────────────────────

func TestTrigger_FSMOutputFiresWebhook(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)
	defineSub(t, env, "fsm.output", "webhook", map[string]interface{}{
		"url":  srv.URL,
		"body": `{"out":"{{event.data.output}}","machine":"{{event.id}}"}`,
	})

	id := newAssetMachine(t, env)
	// Drive to a transition that emits a Mealy output. Note: earlier
	// transitions in this sequence also emit outputs (e.g. asset_activated),
	// so we assert the decommission output is among those received, not that
	// it is the first.
	walk(t, env, id, "ready_for_inspection", nil)
	walk(t, env, id, "inspection_passed", map[string]interface{}{"result": "pass", "technician": "a"})
	walk(t, env, id, "suspend", nil)
	st, resp := walk(t, env, id, "decommission", nil)
	if st != http.StatusOK {
		t.Fatalf("decommission: want 200, got %d: %v", st, resp)
	}

	// Wait until the decommission output specifically has been delivered.
	deadline := time.Now().Add(2 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		rec.mu.Lock()
		for _, c := range rec.calls {
			m := msgOf(c)
			if m["out"] == "asset_decommissioned" && m["machine"] == fmt.Sprintf("%d", id) {
				found = true
			}
		}
		rec.mu.Unlock()
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		rec.mu.Lock()
		got := rec.calls
		rec.mu.Unlock()
		t.Fatalf("fsm.output for asset_decommissioned did not reach the webhook; got %v", got)
	}
}

func TestTrigger_FSMNoOutputNoFire(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newV2Server(t)
	defineSub(t, env, "fsm.output", "webhook", map[string]interface{}{"url": srv.URL})

	id := newAssetMachine(t, env)
	// A transition with no Mealy output must not fire an fsm.output event.
	walk(t, env, id, "ready_for_inspection", nil)

	if _, ok := rec.waitForCall(400 * time.Millisecond); ok {
		t.Error("a transition with no output should not fire fsm.output")
	}
}
