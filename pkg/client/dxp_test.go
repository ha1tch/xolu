// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// dxp_test.go — mock-based tests per the Stage 2 convention: happy
// path, structured error, and client-side validation per method. The
// real-server round trip (register a def -> instantiate it ->
// re-list/re-get both) lives in integration_test.go's
// TestIntegration_DxpFullFlow, which is what catches wire-shape drift
// these mocks structurally cannot.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func validDxpDefReq() DxpDefCreateRequest {
	return DxpDefCreateRequest{
		Name:    "simple_payment",
		Pattern: "3ps",
		Participants: []DxpParticipant{
			{ID: "payment", Primitive: "bal", Op: "transfer",
				Params: map[string]interface{}{"from": "~in", "to": "acct", "amount": map[string]interface{}{"$ref": "amount"}}},
		},
		PhaseTTL: DxpPhaseTTL{Reserve: "PT2M"},
	}
}

// ─── DxpDefCreate ───────────────────────────────────────────────────────────

func TestDxpDefCreateHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/dxp/def" {
			t.Errorf("expected /api/v2/dxp/def, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["name"] != "simple_payment" {
			t.Errorf("name: got %v", req["name"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1,"name":"simple_payment","created_at":"2026-06-15T12:00:00Z","analysis":{"collapse_eligible":true,"engine_homogeneous":true}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.DxpDefCreate(context.Background(), validDxpDefReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.ID != 1 || !def.Analysis.CollapseEligible || !def.Analysis.EngineHomogeneous {
		t.Errorf("unexpected def: %+v", def)
	}
}

func TestDxpDefCreateValidationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":{"code":"XOLU-DXP006","message":"unknown primitive \"widget\"","status":422}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	req := validDxpDefReq()
	req.Participants[0].Primitive = "widget"
	_, err := c.DxpDefCreate(context.Background(), req)
	ce, ok := err.(*Error)
	if !ok || ce.Code != "XOLU-DXP006" {
		t.Fatalf("expected XOLU-DXP006 *client.Error, got %T: %v", err, err)
	}
}

func TestDxpDefCreateRequiresName(t *testing.T) {
	c := New("http://unused")
	req := validDxpDefReq()
	req.Name = ""
	if _, err := c.DxpDefCreate(context.Background(), req); err == nil {
		t.Fatal("expected client-side validation error for empty Name")
	}
}

func TestDxpDefCreateRequiresPattern(t *testing.T) {
	c := New("http://unused")
	req := validDxpDefReq()
	req.Pattern = ""
	if _, err := c.DxpDefCreate(context.Background(), req); err == nil {
		t.Fatal("expected client-side validation error for empty Pattern")
	}
}

func TestDxpDefCreateRequiresParticipants(t *testing.T) {
	c := New("http://unused")
	req := validDxpDefReq()
	req.Participants = nil
	if _, err := c.DxpDefCreate(context.Background(), req); err == nil {
		t.Fatal("expected client-side validation error for empty Participants")
	}
}

func TestDxpDefCreateRequiresPhaseTTL(t *testing.T) {
	c := New("http://unused")
	req := validDxpDefReq()
	req.PhaseTTL.Reserve = ""
	if _, err := c.DxpDefCreate(context.Background(), req); err == nil {
		t.Fatal("expected client-side validation error for empty PhaseTTL.Reserve")
	}
}

// ─── DxpDefList ─────────────────────────────────────────────────────────────

func TestDxpDefListHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/dxp/def" {
			t.Errorf("expected /api/v2/dxp/def, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"definitions":[{"id":1,"name":"simple_payment","created_at":"2026-06-15T12:00:00Z"}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.DxpDefList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Definitions) != 1 || res.Definitions[0].Name != "simple_payment" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestDxpDefListEmptyNeverNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"definitions":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.DxpDefList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Definitions == nil {
		t.Error("Definitions must never be nil, even when empty")
	}
}

// ─── DxpDefGet ──────────────────────────────────────────────────────────────

func TestDxpDefGetHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/dxp/def/1" {
			t.Errorf("expected /api/v2/dxp/def/1, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":1,"name":"simple_payment","created_at":"2026-06-15T12:00:00Z","spec":{"name":"simple_payment","pattern":"3ps","participants":[],"phase_ttl":{"reserve":"PT2M"}},"analysis":{"collapse_eligible":true,"engine_homogeneous":true},"bindings_schema":{}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.DxpDefGet(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.Spec == nil || def.Spec.Pattern != "3ps" {
		t.Errorf("expected Spec to be populated by DxpDefGet, got %+v", def)
	}
}

func TestDxpDefGetNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-DXP006","message":"dxp/def not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.DxpDefGet(context.Background(), 999)
	ce, ok := err.(*Error)
	if !ok || ce.Code != "XOLU-DXP006" {
		t.Fatalf("expected XOLU-DXP006 *client.Error, got %T: %v", err, err)
	}
}

func TestDxpDefGetRequiresPositiveID(t *testing.T) {
	c := New("http://unused")
	if _, err := c.DxpDefGet(context.Background(), 0); err == nil {
		t.Fatal("expected client-side validation error for id <= 0")
	}
}

// ─── DxpTxnCreate ───────────────────────────────────────────────────────────

func TestDxpTxnCreateHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/dxp/txn" {
			t.Errorf("expected /api/v2/dxp/txn, got %s", r.URL.Path)
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["def_id"] != float64(1) {
			t.Errorf("def_id: got %v", req["def_id"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":10,"def_id":1,"status":"committed","committed_through":1,"created_at":"2026-06-15T12:00:00Z","snapshot":{"pattern":"3ps","participants":[],"phase_ttl":{"reserve":"PT2M"}}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	txn, err := c.DxpTxnCreate(context.Background(), DxpTxnCreateRequest{
		DefID: 1, Bindings: map[string]interface{}{"amount": "150"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txn.Status != "committed" || txn.CommittedThrough != 1 {
		t.Errorf("unexpected txn: %+v", txn)
	}
}

func TestDxpTxnCreateReleasedIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":11,"def_id":1,"status":"released","committed_through":0,"reason":"XOLU-DXP002: reserve refused","created_at":"2026-06-15T12:00:00Z","snapshot":{"pattern":"3ps","participants":[],"phase_ttl":{"reserve":"PT2M"}}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	txn, err := c.DxpTxnCreate(context.Background(), DxpTxnCreateRequest{DefID: 1})
	if err != nil {
		t.Fatalf("a released outcome must be a 201 response, not a client error: %v", err)
	}
	if txn.Status != "released" || txn.Reason == "" {
		t.Errorf("expected released status with a reason, got %+v", txn)
	}
}

func TestDxpTxnCreateBindingsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":{"code":"XOLU-DXP001","message":"amount: required","status":422}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.DxpTxnCreate(context.Background(), DxpTxnCreateRequest{DefID: 1})
	ce, ok := err.(*Error)
	if !ok || ce.Code != "XOLU-DXP001" {
		t.Fatalf("expected XOLU-DXP001 *client.Error, got %T: %v", err, err)
	}
}

func TestDxpTxnCreateRequiresPositiveDefID(t *testing.T) {
	c := New("http://unused")
	if _, err := c.DxpTxnCreate(context.Background(), DxpTxnCreateRequest{}); err == nil {
		t.Fatal("expected client-side validation error for DefID <= 0")
	}
}

// ─── DxpTxnList ─────────────────────────────────────────────────────────────

func TestDxpTxnListHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/dxp/txn" {
			t.Errorf("expected /api/v2/dxp/txn, got %s", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string for an unfiltered list, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"instances":[{"id":10,"def_id":1,"def_name":"simple_payment","status":"committed","committed_through":1,"created_at":"2026-06-15T12:00:00Z"}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.DxpTxnList(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Instances) != 1 || res.Instances[0].Status != "committed" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestDxpTxnListStatusFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("status"); got != "expired" {
			t.Errorf("expected status=expired, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"instances":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	if _, err := c.DxpTxnList(context.Background(), "expired"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDxpTxnListEmptyNeverNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"instances":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.DxpTxnList(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Instances == nil {
		t.Error("Instances must never be nil, even when empty")
	}
}

// ─── DxpTxnGet ──────────────────────────────────────────────────────────────

func TestDxpTxnGetHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/dxp/txn/10" {
			t.Errorf("expected /api/v2/dxp/txn/10, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":10,"def_id":1,"def_name":"simple_payment","status":"committed","committed_through":1,"deadline_ns":1234567890,"created_at":"2026-06-15T12:00:00Z","snapshot":{"pattern":"3ps","participants":[],"phase_ttl":{"reserve":"PT2M"}}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	txn, err := c.DxpTxnGet(context.Background(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txn.DefName != "simple_payment" || txn.DeadlineNs == 0 {
		t.Errorf("expected DefName and DeadlineNs to be populated by DxpTxnGet, got %+v", txn)
	}
}

func TestDxpTxnGetNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-ST001","message":"dxp/txn not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.DxpTxnGet(context.Background(), 999)
	if _, ok := err.(*Error); !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
}

func TestDxpTxnGetRequiresPositiveID(t *testing.T) {
	c := New("http://unused")
	if _, err := c.DxpTxnGet(context.Background(), -1); err == nil {
		t.Fatal("expected client-side validation error for id <= 0")
	}
}
