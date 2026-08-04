// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_obj_events_test.go — T-123 (wave 10): obj-01-rest-api.md §7's
// own event feed, proven end-to-end through a real webhook delivery,
// mirroring v2_event_trigger_test.go's own established pattern
// exactly (hookRecorder, defineSub, waitForCall).

package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrigger_ObjMoveFiresWebhook(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newMetaServer(t)
	defineSub(t, env, "obj.move", "webhook", map[string]interface{}{"url": srv.URL})
	defineLocRoot(t, env, "evt-root")
	defineLocLeaf(t, env, "evt-root/bay", "evt-root", nil)
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "vehicles:1"})

	status, resp := doJSONRequest(t, "PUT", objURL(env, "/vehicles/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "loc_leaf", "location_id": "evt-root/bay"},
	})
	if status != http.StatusOK {
		t.Fatalf("move: want 200, got %d %v", status, resp)
	}

	body, ok := rec.waitForCall(2 * time.Second)
	if !ok {
		t.Fatal("obj.move did not reach the webhook within timeout")
	}
	msg := msgOf(body)
	if msg["event"] != "obj.move" {
		t.Errorf("event type: want obj.move, got %v", msg["event"])
	}
	if msg["entity"] != "vehicles" {
		t.Errorf("event entity: want vehicles, got %v", msg["entity"])
	}
	data, _ := msg["data"].(map[string]interface{})
	if data["position_kind"] != "loc_leaf" || data["loc_leaf_id"] != "evt-root/bay" {
		t.Errorf("event data should carry the resulting position, got %v", data)
	}
}

func TestTrigger_ObjPromoteFiresWebhook_OnlyWhenCommitted(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newBalServer(t)
	defineSub(t, env, "obj.promote", "webhook", map[string]interface{}{"url": srv.URL})
	defineBalAccount(t, env, "pallet-44-cases", "-1000")
	defineBalAccount(t, env, "pallet-44-cases-promoted", "")
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:44"})

	status, resp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "pallet-44-cases",
		"to_account":  "pallet-44-cases-promoted",
		"amount":      "1",
		"entity": map[string]interface{}{
			"kind":   "cases",
			"create": map[string]interface{}{"lot_code": "L44"},
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:44"},
	})
	if status != http.StatusCreated || resp["status"] != "committed" {
		t.Fatalf("promote: want 201/committed, got %d %v", status, resp)
	}

	body, ok := rec.waitForCall(2 * time.Second)
	if !ok {
		t.Fatal("obj.promote did not reach the webhook within timeout")
	}
	msg := msgOf(body)
	if msg["event"] != "obj.promote" {
		t.Errorf("event type: want obj.promote, got %v", msg["event"])
	}
}

func TestTrigger_ObjPromote_RefusedTransaction_NeverFiresEvent(t *testing.T) {
	rec := &hookRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	env := newBalServer(t)
	defineSub(t, env, "obj.promote", "webhook", map[string]interface{}{"url": srv.URL})
	defineBalAccount(t, env, "pallet-55-cases", "") // no floor override -- default 0, decrement always refused
	defineBalAccount(t, env, "pallet-55-cases-promoted", "")
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:55"})

	status, resp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "pallet-55-cases",
		"to_account":  "pallet-55-cases-promoted",
		"amount":      "1",
		"entity": map[string]interface{}{
			"kind":   "cases",
			"create": map[string]interface{}{"lot_code": "L55"},
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:55"},
	})
	if status != http.StatusCreated || resp["status"] != "released" {
		t.Fatalf("promote: want 201/released, got %d %v", status, resp)
	}

	if _, ok := rec.waitForCall(300 * time.Millisecond); ok {
		t.Error("obj.promote must not fire for a refused transaction, but the webhook was called")
	}
}
