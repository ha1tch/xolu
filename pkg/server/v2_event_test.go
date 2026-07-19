// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_event_test.go — S9 Batch 1: event subscription management surface.
// Dispatch and trigger wiring are tested in later batches; this covers the
// CRUD lifecycle, validation, the sync-downgrade header, and the empty log.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func eventURL(env *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/event/def%s", env.ts.URL, path)
}

func defineSub(t *testing.T, env *stdTestServer, eventType, actionType string, config map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	body := map[string]interface{}{"event_type": eventType, "action_type": actionType}
	if config != nil {
		body["config"] = config
	}
	return doJSONRequest(t, "POST", eventURL(env, ""), body)
}

// ─── create + retrieve round-trip ─────────────────────────────────────────────

func TestEvent_CreateAndGet(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineSub(t, env, "entity.created", "webhook",
		map[string]interface{}{"url": "https://example.test/hook"})
	if st != http.StatusCreated {
		t.Fatalf("create subscription: want 201, got %d: %v", st, resp)
	}
	id := int64(resp["id"].(float64))
	if resp["event_type"] != "entity.created" || resp["action_type"] != "webhook" {
		t.Errorf("unexpected create echo: %v", resp)
	}
	if resp["execution"] != "async" {
		t.Errorf("default execution should be async, got %v", resp["execution"])
	}

	st, got := doJSONRequest(t, "GET", eventURL(env, fmt.Sprintf("/%d", id)), nil)
	if st != http.StatusOK {
		t.Fatalf("get subscription: want 200, got %d: %v", st, got)
	}
	if int64(got["id"].(float64)) != id || got["event_type"] != "entity.created" {
		t.Errorf("get returned wrong subscription: %v", got)
	}
}

// ─── validation ───────────────────────────────────────────────────────────────

func TestEvent_RejectsUnknownEventType(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineSub(t, env, "entity.exploded", "webhook", nil)
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-EV001" {
		t.Errorf("unknown event_type: want 400/XOLU-EV001, got %d/%v", st, resp["error"])
	}
}

func TestEvent_RejectsUnknownActionType(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineSub(t, env, "entity.created", "carrier_pigeon", nil)
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-EV001" {
		t.Errorf("unknown action_type: want 400/XOLU-EV001, got %d/%v", st, resp["error"])
	}
}

func TestEvent_RejectsBadExecution(t *testing.T) {
	env := newV2Server(t)
	st, resp := doJSONRequest(t, "POST", eventURL(env, ""), map[string]interface{}{
		"event_type": "fsm.output", "action_type": "oql", "execution": "eventually",
	})
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-EV001" {
		t.Errorf("bad execution: want 400/XOLU-EV001, got %d/%v", st, resp["error"])
	}
}

// ─── sync downgrade ───────────────────────────────────────────────────────────

func TestEvent_SyncDowngradeHeader(t *testing.T) {
	// A sync subscription is accepted and stored, but Part 1 always runs async;
	// the create response carries X-Executed-As: async.
	env := newV2Server(t)
	body := map[string]interface{}{
		"event_type": "entity.updated", "action_type": "oql",
		"config": map[string]interface{}{"query": "SELECT 1 FROM assets"}, "execution": "sync",
	}
	buf, _ := json.Marshal(body)
	resp, err := http.Post(eventURL(env, ""), "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create sync subscription: want 201, got %d", resp.StatusCode)
	}
	if h := resp.Header.Get("X-Executed-As"); h != "async" {
		t.Errorf("sync subscription should report X-Executed-As: async, got %q", h)
	}
}

// ─── update ───────────────────────────────────────────────────────────────────

func TestEvent_UpdateConfigAndExecution(t *testing.T) {
	env := newV2Server(t)
	_, resp := defineSub(t, env, "entity.created", "webhook",
		map[string]interface{}{"url": "https://a.test"})
	id := int64(resp["id"].(float64))

	st, upd := doJSONRequest(t, "PATCH", eventURL(env, fmt.Sprintf("/%d", id)),
		map[string]interface{}{
			"config":    map[string]interface{}{"url": "https://b.test"},
			"execution": "sync",
		})
	if st != http.StatusOK {
		t.Fatalf("update: want 200, got %d: %v", st, upd)
	}
	if upd["execution"] != "sync" {
		t.Errorf("execution should be updated to sync, got %v", upd["execution"])
	}
	cfg, _ := upd["config"].(map[string]interface{})
	if cfg["url"] != "https://b.test" {
		t.Errorf("config url should be updated, got %v", cfg["url"])
	}
}

// ─── list + delete ────────────────────────────────────────────────────────────

func TestEvent_ListAndDelete(t *testing.T) {
	env := newV2Server(t)
	defineSub(t, env, "entity.created", "webhook", map[string]interface{}{"url": "https://a.test"})
	_, r2 := defineSub(t, env, "fsm.output", "oql", map[string]interface{}{"query": "SELECT 1 FROM assets"})
	id2 := int64(r2["id"].(float64))

	_, listResp := doJSONRequest(t, "GET", eventURL(env, ""), nil)
	subs, _ := listResp["subscriptions"].([]interface{})
	if len(subs) != 2 {
		t.Errorf("list: want 2 subscriptions, got %d", len(subs))
	}

	st, _ := doJSONRequest(t, "DELETE", eventURL(env, fmt.Sprintf("/%d", id2)), nil)
	if st != http.StatusOK {
		t.Fatalf("delete: want 200, got %d", st)
	}
	st, _ = doJSONRequest(t, "GET", eventURL(env, fmt.Sprintf("/%d", id2)), nil)
	if st != http.StatusNotFound {
		t.Errorf("deleted subscription GET should 404, got %d", st)
	}
}

func TestEvent_GetNotFound(t *testing.T) {
	env := newV2Server(t)
	st, resp := doJSONRequest(t, "GET", eventURL(env, "/999999"), nil)
	if st != http.StatusNotFound || errCode(resp) != "XOLU-EV002" {
		t.Errorf("missing subscription: want 404/XOLU-EV002, got %d/%v", st, resp["error"])
	}
}

// ─── delivery log (empty in Batch 1; no dispatch yet) ─────────────────────────

func TestEvent_EmptyDeliveryLog(t *testing.T) {
	env := newV2Server(t)
	_, resp := defineSub(t, env, "entity.created", "webhook", map[string]interface{}{"url": "https://a.test"})
	id := int64(resp["id"].(float64))
	st, logResp := doJSONRequest(t, "GET", eventURL(env, fmt.Sprintf("/%d/log", id)), nil)
	if st != http.StatusOK {
		t.Fatalf("log: want 200, got %d: %v", st, logResp)
	}
	deliveries, _ := logResp["deliveries"].([]interface{})
	if len(deliveries) != 0 {
		t.Errorf("no dispatch yet, log should be empty, got %d entries", len(deliveries))
	}
}

// ─── test endpoint now dispatches (Batch 2) ───────────────────────────────────

func TestEvent_TestEndpointDispatches(t *testing.T) {
	env := newV2Server(t)
	_, resp := defineSub(t, env, "entity.created", "oql",
		map[string]interface{}{"query": "SELECT 1 FROM assets"})
	id := int64(resp["id"].(float64))
	seedAsset(t, env, "g", "active")
	st, out := doJSONRequest(t, "POST", eventURL(env, fmt.Sprintf("/%d/test", id)), map[string]interface{}{})
	if st != http.StatusOK {
		t.Fatalf("test dispatch: want 200, got %d: %v", st, out)
	}
	results, _ := out["results"].([]interface{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), out)
	}
	r0 := results[0].(map[string]interface{})
	if r0["status"] != "delivered" {
		t.Errorf("oql action should be delivered, got %v (%v)", r0["status"], r0["detail"])
	}
}
