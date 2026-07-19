// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Stage 3 tests — FSM machine operations. Each method has at least a
// happy-path test that verifies the URL, method, and body/response shape
// against xolu's actual wire format.

// ─── CreateMachine ─────────────────────────────────────────────────────────

func TestCreateMachineHappyPath(t *testing.T) {
	var sawBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v2/fsm/machine" {
			t.Errorf("expected /api/v2/fsm/machine, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sawBody)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{
		  "id": 42,
		  "definition": 5,
		  "definition_name": "order_flow",
		  "definition_deleted": false,
		  "state": "draft",
		  "vars": {"attempts": 0},
		  "created_at": "2026-07-17T10:00:00Z",
		  "ref": "orders/1001"
		}`))
	}))
	defer server.Close()

	c := New(server.URL)
	m, err := c.CreateMachine(context.Background(), CreateMachineRequest{
		Definition: 5,
		Ref:        "orders/1001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID != 42 || m.Definition != 5 || m.State != "draft" || m.Ref != "orders/1001" {
		t.Errorf("machine fields wrong: %+v", m)
	}
	if m.Vars["attempts"] != 0.0 {
		t.Errorf("expected attempts=0, got %v", m.Vars["attempts"])
	}
	// Confirm the client sent the right body
	if sawBody["definition"] != 5.0 || sawBody["ref"] != "orders/1001" {
		t.Errorf("request body wrong: %v", sawBody)
	}
}

func TestCreateMachineWithOverrides(t *testing.T) {
	var sawBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sawBody)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1,"definition":1,"definition_name":"x","definition_deleted":false,"state":"init","vars":{},"created_at":"t"}`))
	}))
	defer server.Close()

	guard := "attempts < 5"
	c := New(server.URL)
	_, err := c.CreateMachine(context.Background(), CreateMachineRequest{
		Definition: 1,
		Overrides: &MachineOverrides{
			Variables: map[string]VariableDef{
				"max_retries": {Type: "integer", Default: 5},
			},
			Transitions: map[string]TransitionOverride{
				"submit": {Guard: &guard},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	overrides, ok := sawBody["overrides"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected overrides in body, got %v", sawBody)
	}
	if _, ok := overrides["variables"]; !ok {
		t.Errorf("expected variables in overrides")
	}
	if _, ok := overrides["transitions"]; !ok {
		t.Errorf("expected transitions in overrides")
	}
}

func TestCreateMachineRejectsZeroDefinition(t *testing.T) {
	c := New("http://example")
	_, err := c.CreateMachine(context.Background(), CreateMachineRequest{Definition: 0})
	if err == nil {
		t.Fatal("expected error for zero Definition")
	}
}

func TestCreateMachineDefNotFoundReturnsStructuredError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM001","message":"definition not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CreateMachine(context.Background(), CreateMachineRequest{Definition: 999})
	if err == nil {
		t.Fatal("expected error")
	}
	var xerr *Error
	if !errorsAs(err, &xerr) || xerr.Code != "XOLU-FSM001" {
		t.Errorf("expected XOLU-FSM001, got %v", err)
	}
}

// ─── ListMachines ──────────────────────────────────────────────────────────

func TestListMachinesHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/machine" {
			t.Errorf("expected /api/v2/fsm/machine, got %s", r.URL.Path)
		}
		w.Write([]byte(`{"machines":[
		  {"id":1,"definition":5,"definition_name":"order_flow","state":"draft",     "ref":"orders/1","created_at":"t1"},
		  {"id":2,"definition":5,"definition_name":"order_flow","state":"submitted", "ref":null,       "created_at":"t2"}
		]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	ms, err := c.ListMachines(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("expected 2 machines, got %d", len(ms))
	}
	if ms[0].ID != 1 || ms[0].DefinitionName != "order_flow" {
		t.Errorf("first machine wrong: %+v", ms[0])
	}
	if ms[0].Ref == nil || *ms[0].Ref != "orders/1" {
		t.Errorf("expected ref orders/1, got %v", ms[0].Ref)
	}
	if ms[1].Ref != nil {
		t.Errorf("expected nil ref on second machine, got %v", ms[1].Ref)
	}
}

func TestListMachinesWithFilter(t *testing.T) {
	var sawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Write([]byte(`{"machines":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ListMachines(context.Background(), &MachineFilter{
		Definition: 7,
		State:      "submitted",
		Ref:        "orders/42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Query key order is stable via url.Values encoding
	// (alphabetical): definition, ref, state.
	want := "definition=7&ref=orders%2F42&state=submitted"
	if sawQuery != want {
		t.Errorf("expected query %s, got %s", want, sawQuery)
	}
}

func TestListMachinesNilFilterNoQuery(t *testing.T) {
	var sawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Write([]byte(`{"machines":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ListMachines(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawQuery != "" {
		t.Errorf("expected empty query, got %s", sawQuery)
	}
}

// ─── GetMachine / PatchMachine / DeleteMachine ─────────────────────────────

func TestGetMachineHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/machine/42" {
			t.Errorf("expected /api/v2/fsm/machine/42, got %s", r.URL.Path)
		}
		w.Write([]byte(`{
		  "id": 42,
		  "definition": 5,
		  "definition_name": "order_flow",
		  "definition_deleted": true,
		  "state": "approved",
		  "vars": {"attempts": 2},
		  "created_at": "t"
		}`))
	}))
	defer server.Close()

	c := New(server.URL)
	m, err := c.GetMachine(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID != 42 || m.State != "approved" || !m.DefinitionDeleted {
		t.Errorf("machine wrong: %+v", m)
	}
}

func TestGetMachineNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM003","message":"machine not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.GetMachine(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error")
	}
	var xerr *Error
	if !errorsAs(err, &xerr) || xerr.Code != "XOLU-FSM003" {
		t.Errorf("expected XOLU-FSM003, got %v", err)
	}
}

func TestGetMachineRejectsZeroID(t *testing.T) {
	c := New("http://example")
	_, err := c.GetMachine(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error for zero id")
	}
}

func TestPatchMachineHappyPath(t *testing.T) {
	var sawMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		w.Write([]byte(`{"id":42,"definition":5,"definition_name":"x","definition_deleted":false,"state":"draft","vars":{},"created_at":"t"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	guard := "attempts < 10"
	_, err := c.PatchMachine(context.Background(), 42, PatchMachineRequest{
		Overrides: &MachineOverrides{
			Transitions: map[string]TransitionOverride{"submit": {Guard: &guard}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawMethod != http.MethodPatch {
		t.Errorf("expected PATCH, got %s", sawMethod)
	}
}

func TestDeleteMachineHappyPath(t *testing.T) {
	var sawMethod, sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL)
	if err := c.DeleteMachine(context.Background(), 42); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if sawMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", sawMethod)
	}
	if sawPath != "/api/v2/fsm/machine/42" {
		t.Errorf("path wrong: %s", sawPath)
	}
}

func TestDeleteMachineNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM003","message":"machine not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteMachine(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error")
	}
	var xerr *Error
	if !errorsAs(err, &xerr) || xerr.Code != "XOLU-FSM003" {
		t.Errorf("expected XOLU-FSM003, got %v", err)
	}
}

// ─── WalkMachine ───────────────────────────────────────────────────────────

func TestWalkMachineHappyPath(t *testing.T) {
	var sawBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/machine/42/walk" {
			t.Errorf("expected /api/v2/fsm/machine/42/walk, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &sawBody)
		w.Write([]byte(`{
		  "previous":   "draft",
		  "current":    "submitted",
		  "terminal":   false,
		  "outputs":    ["notify_customer"],
		  "vars":       {"attempts": 1},
		  "history_id": 101
		}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.WalkMachine(context.Background(), 42, WalkRequest{
		Input:   "submit",
		Payload: map[string]interface{}{"amount": 100},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Previous != "draft" || res.Current != "submitted" {
		t.Errorf("states wrong: %+v", res)
	}
	if len(res.Outputs) != 1 || res.Outputs[0] != "notify_customer" {
		t.Errorf("outputs wrong: %v", res.Outputs)
	}
	if res.HistoryID != 101 {
		t.Errorf("expected history_id 101, got %d", res.HistoryID)
	}
	if sawBody["input"] != "submit" {
		t.Errorf("input not sent: %v", sawBody)
	}
	payload, ok := sawBody["payload"].(map[string]interface{})
	if !ok || payload["amount"] != 100.0 {
		t.Errorf("payload wrong: %v", sawBody["payload"])
	}
}

func TestWalkMachineRejectsEmptyInput(t *testing.T) {
	c := New("http://example")
	_, err := c.WalkMachine(context.Background(), 42, WalkRequest{Input: ""})
	if err == nil {
		t.Fatal("expected error for empty Input")
	}
}

func TestWalkMachineGuardRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM005","message":"guard rejected transition","status":422}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.WalkMachine(context.Background(), 42, WalkRequest{Input: "submit"})
	if err == nil {
		t.Fatal("expected error")
	}
	var xerr *Error
	if !errorsAs(err, &xerr) || xerr.Code != "XOLU-FSM005" {
		t.Errorf("expected XOLU-FSM005, got %v", err)
	}
}

func TestWalkMachineTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"previous":"submitted","current":"approved","terminal":true,"outputs":[],"vars":{},"history_id":102}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.WalkMachine(context.Background(), 42, WalkRequest{Input: "approve"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Terminal {
		t.Errorf("expected terminal=true")
	}
}

// ─── State / Result / Vars / Transitions ───────────────────────────────────

func TestGetMachineStateHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/machine/42/state" {
			t.Errorf("path wrong: %s", r.URL.Path)
		}
		w.Write([]byte(`{"state":"approved","terminal":true}`))
	}))
	defer server.Close()

	c := New(server.URL)
	s, err := c.GetMachineState(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.State != "approved" || !s.Terminal {
		t.Errorf("state wrong: %+v", s)
	}
}

func TestGetMachineResultTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/machine/42/result" {
			t.Errorf("path wrong: %s", r.URL.Path)
		}
		w.Write([]byte(`{
		  "machine":42,"state":"approved","terminal":true,
		  "vars":{"attempts":1},"final_output":["notify_customer"]
		}`))
	}))
	defer server.Close()

	c := New(server.URL)
	r, err := c.GetMachineResult(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Terminal {
		t.Errorf("expected terminal=true")
	}
	if len(r.FinalOutput) == 0 {
		t.Errorf("expected FinalOutput preserved as raw JSON")
	}
}

func TestGetMachineResultNonTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No final_output field when not terminal.
		w.Write([]byte(`{"machine":42,"state":"submitted","terminal":false,"vars":{"attempts":1}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	r, err := c.GetMachineResult(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Terminal {
		t.Errorf("expected terminal=false")
	}
	if len(r.FinalOutput) != 0 {
		t.Errorf("expected FinalOutput to be nil/empty on non-terminal, got %q", r.FinalOutput)
	}
}

func TestGetMachineVarsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/machine/42/vars" {
			t.Errorf("path wrong: %s", r.URL.Path)
		}
		// Flat map, no envelope, per xolu's handleFSMMachineVars.
		w.Write([]byte(`{
		  "attempts": {"value":3,   "type":"integer","default":0},
		  "note":     {"value":"hi","type":"string", "default":""}
		}`))
	}))
	defer server.Close()

	c := New(server.URL)
	vs, err := c.GetMachineVars(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("expected 2 vars, got %d", len(vs))
	}
	if vs["attempts"].Value != 3.0 || vs["attempts"].Type != "integer" {
		t.Errorf("attempts wrong: %+v", vs["attempts"])
	}
	if vs["note"].Value != "hi" {
		t.Errorf("note wrong: %+v", vs["note"])
	}
}

func TestGetMachineTransitionsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":"draft","inputs":["submit","cancel"]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	tr, err := c.GetMachineTransitions(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.State != "draft" {
		t.Errorf("state wrong: %+v", tr)
	}
	if len(tr.Inputs) != 2 || tr.Inputs[0] != "submit" {
		t.Errorf("inputs wrong: %v", tr.Inputs)
	}
}

// ─── History ───────────────────────────────────────────────────────────────

func TestGetMachineHistoryHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/machine/42/history" {
			t.Errorf("path wrong: %s", r.URL.Path)
		}
		// A realistic history: creation entry (from null, input null,
		// note set), one walk entry (from set, input set, outputs).
		w.Write([]byte(`{
		  "machine": 42,
		  "entries": [
		    {"id":1,"from":null,   "to":"draft",    "input":null,     "payload":null, "vars":{"attempts":0}, "note":"machine created", "at":"t0"},
		    {"id":2,"from":"draft","to":"submitted","input":"submit", "payload":{"amount":100}, "vars":{"attempts":1}, "outputs":["notify_customer"], "at":"t1"}
		  ]
		}`))
	}))
	defer server.Close()

	c := New(server.URL)
	hist, err := c.GetMachineHistory(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(hist))
	}
	// Creation entry: from and input nil
	if hist[0].From != nil {
		t.Errorf("expected creation entry to have nil From, got %v", *hist[0].From)
	}
	if hist[0].Input != nil {
		t.Errorf("expected creation entry to have nil Input, got %v", *hist[0].Input)
	}
	if hist[0].Note != "machine created" {
		t.Errorf("expected creation note, got %q", hist[0].Note)
	}
	// Walk entry: from and input set
	if hist[1].From == nil || *hist[1].From != "draft" {
		t.Errorf("expected From=draft, got %v", hist[1].From)
	}
	if hist[1].Input == nil || *hist[1].Input != "submit" {
		t.Errorf("expected Input=submit, got %v", hist[1].Input)
	}
	if len(hist[1].Outputs) == 0 {
		t.Errorf("expected Outputs preserved as raw JSON")
	}
	if len(hist[1].Payload) == 0 {
		t.Errorf("expected Payload preserved as raw JSON")
	}
}

func TestGetMachineHistoryEmptyMachine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"machine":42,"entries":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	hist, err := c.GetMachineHistory(context.Background(), 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hist) != 0 {
		t.Errorf("expected empty history, got %d entries", len(hist))
	}
}

// ─── Zero-id rejection across the methods ──────────────────────────────────

func TestMachineMethodsRejectZeroID(t *testing.T) {
	c := New("http://example")
	ctx := context.Background()

	if _, err := c.GetMachine(ctx, 0); err == nil {
		t.Errorf("GetMachine did not reject zero id")
	}
	if _, err := c.PatchMachine(ctx, 0, PatchMachineRequest{}); err == nil {
		t.Errorf("PatchMachine did not reject zero id")
	}
	if err := c.DeleteMachine(ctx, 0); err == nil {
		t.Errorf("DeleteMachine did not reject zero id")
	}
	if _, err := c.WalkMachine(ctx, 0, WalkRequest{Input: "x"}); err == nil {
		t.Errorf("WalkMachine did not reject zero id")
	}
	if _, err := c.GetMachineState(ctx, 0); err == nil {
		t.Errorf("GetMachineState did not reject zero id")
	}
	if _, err := c.GetMachineResult(ctx, 0); err == nil {
		t.Errorf("GetMachineResult did not reject zero id")
	}
	if _, err := c.GetMachineVars(ctx, 0); err == nil {
		t.Errorf("GetMachineVars did not reject zero id")
	}
	if _, err := c.GetMachineTransitions(ctx, 0); err == nil {
		t.Errorf("GetMachineTransitions did not reject zero id")
	}
	if _, err := c.GetMachineHistory(ctx, 0); err == nil {
		t.Errorf("GetMachineHistory did not reject zero id")
	}
}
