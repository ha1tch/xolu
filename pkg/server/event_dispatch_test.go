// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// event_dispatch_test.go — S9 Batch 2: the dispatcher, exercised via the
// /event/{id}/test endpoint. Covers webhook delivery (success + failure),
// template substitution including {{gen:}}, and delivery-log recording.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// testDispatch invokes the test endpoint with an event payload and returns the
// first result's status/detail.
func testDispatch(t *testing.T, env *stdTestServer, subID int64, payload map[string]interface{}) (string, string) {
	t.Helper()
	st, out := doJSONRequest(t, "POST", eventURL(env, fmt.Sprintf("/%d/test", subID)), payload)
	if st != http.StatusOK {
		t.Fatalf("test dispatch: want 200, got %d: %v", st, out)
	}
	results, _ := out["results"].([]interface{})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), out)
	}
	r0 := results[0].(map[string]interface{})
	status, _ := r0["status"].(string)
	detail, _ := r0["detail"].(string)
	return status, detail
}

func TestDispatch_WebhookDeliveredOn2xx(t *testing.T) {
	var got struct {
		mu   sync.Mutex
		body map[string]interface{}
		hit  bool
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		_ = json.Unmarshal(b, &got.body)
		got.hit = true
		got.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env := newV2Server(t)
	_, resp := defineSub(t, env, "entity.created", "webhook", map[string]interface{}{"url": srv.URL})
	id := int64(resp["id"].(float64))

	status, detail := testDispatch(t, env, id, map[string]interface{}{
		"entity": "asset", "id": "42", "data": map[string]interface{}{"status": "active"},
	})
	if status != "delivered" {
		t.Errorf("webhook to 200 endpoint should be delivered, got %q (%s)", status, detail)
	}
	got.mu.Lock()
	defer got.mu.Unlock()
	if !got.hit {
		t.Fatal("webhook endpoint was never called")
	}
	msg, _ := got.body["message"].(map[string]interface{})
	if msg["id"] != "42" || msg["entity"] != "asset" {
		t.Errorf("webhook envelope missing event fields: %v", got.body)
	}
}

func TestDispatch_WebhookFailureLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	env := newV2Server(t)
	_, resp := defineSub(t, env, "entity.created", "webhook", map[string]interface{}{"url": srv.URL})
	id := int64(resp["id"].(float64))

	status, _ := testDispatch(t, env, id, map[string]interface{}{"id": "1"})
	if status != "failed" {
		t.Errorf("webhook to 500 endpoint should be failed, got %q", status)
	}
	// The failure must be recorded in the delivery log, not swallowed.
	_, logResp := doJSONRequest(t, "GET", eventURL(env, fmt.Sprintf("/%d/log", id)), nil)
	deliveries, _ := logResp["deliveries"].([]interface{})
	if len(deliveries) != 1 {
		t.Fatalf("failure should produce 1 log entry, got %d", len(deliveries))
	}
	if deliveries[0].(map[string]interface{})["status"] != "failed" {
		t.Errorf("log entry should record failed status: %v", deliveries[0])
	}
}

func TestDispatch_TemplateSubstitution(t *testing.T) {
	var gotURL string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotURL = r.URL.RequestURI()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env := newV2Server(t)
	// URL template references event id and a data field.
	_, resp := defineSub(t, env, "entity.updated", "webhook",
		map[string]interface{}{"url": srv.URL + "/hook/{{event.id}}/{{event.data.kind}}"})
	id := int64(resp["id"].(float64))

	status, detail := testDispatch(t, env, id, map[string]interface{}{
		"id": "777", "data": map[string]interface{}{"kind": "premium"},
	})
	if status != "delivered" {
		t.Fatalf("expected delivered, got %q (%s)", status, detail)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotURL != "/hook/777/premium" {
		t.Errorf("template not substituted: got %q, want /hook/777/premium", gotURL)
	}
}

func TestDispatch_GenTemplateSubstitution(t *testing.T) {
	var gotURL string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotURL = r.URL.RequestURI()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env := newV2Server(t)
	// Define a token generator, then reference it via {{gen:name}} in the webhook URL.
	defineGen(t, env, "token", "corr", map[string]interface{}{"length": 12})
	_, resp := defineSub(t, env, "entity.created", "webhook",
		map[string]interface{}{"url": srv.URL + "/h?cid={{gen:corr}}"})
	id := int64(resp["id"].(float64))

	status, _ := testDispatch(t, env, id, map[string]interface{}{"id": "1"})
	if status != "delivered" {
		t.Fatalf("expected delivered, got %q", status)
	}
	mu.Lock()
	defer mu.Unlock()
	// The {{gen:corr}} token should have been replaced by a 12-char value.
	const prefix = "/h?cid="
	if len(gotURL) != len(prefix)+12 {
		t.Errorf("{{gen:corr}} should yield a 12-char token; got URL %q", gotURL)
	}
}
