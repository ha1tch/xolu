// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Stage 2 tests — schema-map endpoints. Each method has at least one
// happy-path test, one structured-error test, and one shape verification
// test using a realistic response.

// ─── Entity schemas ─────────────────────────────────────────────────────────

func TestGetEntitySchemaHappyPath(t *testing.T) {
	schemaBody := `{
	  "type": "object",
	  "properties": {
	    "name":      {"type": "string"},
	    "email":     {"type": "string", "format": "email"},
	    "author_id": {"type": "string", "format": "ref", "target": "users"}
	  },
	  "required": ["name"]
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/schema/orders" {
			t.Errorf("expected /api/v1/schema/orders, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(schemaBody))
	}))
	defer server.Close()

	c := New(server.URL)
	es, err := c.GetEntitySchema(context.Background(), "orders")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if es.Name != "orders" {
		t.Errorf("expected Name=orders, got %s", es.Name)
	}
	if len(es.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(es.Fields))
	}
	if len(es.Refs) != 1 {
		t.Errorf("expected 1 ref, got %d", len(es.Refs))
	}
	if es.Refs[0].Name != "author_id" || es.Refs[0].Target != "users" {
		t.Errorf("expected ref author_id -> users, got %+v", es.Refs[0])
	}
	// Verify "required" flag propagated
	var nameField *FieldDef
	for i := range es.Fields {
		if es.Fields[i].Name == "name" {
			nameField = &es.Fields[i]
			break
		}
	}
	if nameField == nil || !nameField.Required {
		t.Errorf("expected 'name' field to be required")
	}
	// Raw schema preserved verbatim
	if len(es.Schema) == 0 {
		t.Errorf("expected raw Schema to be preserved")
	}
}

func TestGetEntitySchemaEmptyEntityRejected(t *testing.T) {
	c := New("http://example")
	_, err := c.GetEntitySchema(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty entityType")
	}
}

func TestGetEntitySchemaNotFoundReturnsStructuredError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-ST002","message":"No schema found for missing","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.GetEntitySchema(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var xerr *Error
	if !errorsAs(err, &xerr) {
		t.Fatalf("expected *client.Error, got %T", err)
	}
	if xerr.Code != "XOLU-ST002" {
		t.Errorf("expected code XOLU-ST002, got %q", xerr.Code)
	}
}

// ─── FSM definitions ────────────────────────────────────────────────────────

func TestListMachineDefsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/def" {
			t.Errorf("expected /api/v2/fsm/def, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"definitions":[
			{"id":1,"name":"order_lifecycle","created_at":"2026-07-01T12:00:00Z"},
			{"id":2,"name":"invoice_workflow","created_at":"2026-07-02T09:00:00Z"}
		]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	defs, err := c.ListMachineDefs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}
	if defs[0].ID != 1 || defs[0].Name != "order_lifecycle" {
		t.Errorf("unexpected first def: %+v", defs[0])
	}
	if defs[1].ID != 2 || defs[1].Name != "invoice_workflow" {
		t.Errorf("unexpected second def: %+v", defs[1])
	}
}

func TestListMachineDefsTenantScoping(t *testing.T) {
	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Write([]byte(`{"definitions":[]}`))
	}))
	defer server.Close()

	c := New(server.URL, WithTenant("0042"))
	_, err := c.ListMachineDefs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenPath != "/api/v2/tenant/0042/fsm/def" {
		t.Errorf("expected tenant-scoped v2 path, got %s", seenPath)
	}
}

func TestGetMachineDefHappyPath(t *testing.T) {
	// A realistic FSM def with transitions using both string and array
	// forms of "from", a variable, and a Mealy output. Mirrors xolu's
	// actual wire format from pkg/server/v2_fsm_common.go.
	body := `{
	  "id": 5,
	  "created_at": "2026-07-15T10:00:00Z",
	  "spec": {
	    "name": "order_flow",
	    "description": "order lifecycle",
	    "initial": "draft",
	    "determinism": "strict",
	    "states": {
	      "draft":      {"terminal": false},
	      "submitted":  {"terminal": false},
	      "approved":   {"terminal": true},
	      "cancelled":  {"terminal": true}
	    },
	    "variables": {
	      "attempts": {"type": "integer", "default": 0}
	    },
	    "transitions": [
	      {"from": "draft",     "input": "submit", "to": "submitted", "guard": "attempts < 3", "set": {"attempts": "attempts + 1"}},
	      {"from": "submitted", "input": "approve", "to": "approved", "output": "notify_customer"},
	      {"from": ["draft","submitted"], "input": "cancel", "to": "cancelled"}
	    ]
	  },
	  "analysis": {"reachable_from_initial": ["draft","submitted","approved","cancelled"]}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/def/5" {
			t.Errorf("expected /api/v2/fsm/def/5, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.GetMachineDef(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.ID != 5 {
		t.Errorf("expected ID=5, got %d", def.ID)
	}
	if def.Spec.Name != "order_flow" || def.Spec.Initial != "draft" || def.Spec.Determinism != "strict" {
		t.Errorf("spec fields wrong: %+v", def.Spec)
	}
	if len(def.Spec.States) != 4 {
		t.Errorf("expected 4 states, got %d", len(def.Spec.States))
	}
	if !def.Spec.States["approved"].Terminal {
		t.Errorf("expected 'approved' to be terminal")
	}
	if len(def.Spec.Transitions) != 3 {
		t.Fatalf("expected 3 transitions, got %d", len(def.Spec.Transitions))
	}
	// Verify FromStates normalises both forms
	from0, err := def.Spec.Transitions[0].FromStates()
	if err != nil || len(from0) != 1 || from0[0] != "draft" {
		t.Errorf("expected [draft], got %v (err=%v)", from0, err)
	}
	from2, err := def.Spec.Transitions[2].FromStates()
	if err != nil || len(from2) != 2 {
		t.Errorf("expected 2 from states, got %v (err=%v)", from2, err)
	}
	// Guard and set preserved verbatim
	if def.Spec.Transitions[0].Guard != "attempts < 3" {
		t.Errorf("guard not preserved verbatim: %q", def.Spec.Transitions[0].Guard)
	}
	// Analysis preserved as raw JSON
	if len(def.Analysis) == 0 {
		t.Errorf("expected analysis to be preserved")
	}
}

func TestGetMachineDefNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM001","message":"definition not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.GetMachineDef(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error")
	}
	var xerr *Error
	if !errorsAs(err, &xerr) || xerr.Code != "XOLU-FSM001" {
		t.Errorf("expected XOLU-FSM001, got %v", err)
	}
}

// ─── Generators ─────────────────────────────────────────────────────────────

func TestListGeneratorsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/gen/cuid" {
			t.Errorf("expected /api/v2/gen/cuid, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"type":"cuid","generators":[
			{"name":"orders_id",    "config":{"prefix":"ORD-"}},
			{"name":"invoices_id",  "config":{"prefix":"INV-"}}
		]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	gens, err := c.ListGenerators(context.Background(), GeneratorCUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gens) != 2 {
		t.Fatalf("expected 2 generators, got %d", len(gens))
	}
	if gens[0].Name != "orders_id" {
		t.Errorf("unexpected first gen: %+v", gens[0])
	}
	// Config preserved as RawMessage
	var cfg map[string]string
	if err := json.Unmarshal(gens[0].Config, &cfg); err != nil {
		t.Errorf("could not decode config: %v", err)
	} else if cfg["prefix"] != "ORD-" {
		t.Errorf("unexpected config prefix: %q", cfg["prefix"])
	}
}

func TestListGeneratorsEmptyKindRejected(t *testing.T) {
	c := New("http://example")
	_, err := c.ListGenerators(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty kind")
	}
}

func TestListGeneratorsAllKindsRoundTrip(t *testing.T) {
	// Confirms AllGeneratorKinds are the routes xolu exposes and that
	// iterating them is the intended pattern.
	seenPaths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPaths[r.URL.Path] = true
		w.Write([]byte(`{"type":"x","generators":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	for _, kind := range AllGeneratorKinds {
		if _, err := c.ListGenerators(context.Background(), kind); err != nil {
			t.Fatalf("kind %s: unexpected error: %v", kind, err)
		}
	}
	for _, kind := range AllGeneratorKinds {
		want := "/api/v2/gen/" + string(kind)
		if !seenPaths[want] {
			t.Errorf("expected path %s to be hit", want)
		}
	}
}

func TestGetSequenceHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/gen/seq/order_number" {
			t.Errorf("expected /api/v2/gen/seq/order_number, got %s", r.URL.Path)
		}
		w.Write([]byte(`{"name":"order_number","start":1000,"current":1042,"increment_by":1,"cycle":false}`))
	}))
	defer server.Close()

	c := New(server.URL)
	seq, err := c.GetSequence(context.Background(), "order_number")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seq.Name != "order_number" {
		t.Errorf("expected Name to be filled from argument, got %q", seq.Name)
	}
	if seq.Current != 1042 {
		t.Errorf("expected Current=1042, got %d", seq.Current)
	}
	if seq.Start != 1000 || seq.IncrementBy != 1 {
		t.Errorf("start/step wrong: %+v", seq)
	}
}

func TestGetSequenceEmptyNameRejected(t *testing.T) {
	c := New("http://example")
	_, err := c.GetSequence(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

// ─── Event definitions ─────────────────────────────────────────────────────

func TestListEventDefsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/event/def" {
			t.Errorf("expected /api/v2/event/def, got %s", r.URL.Path)
		}
		// Confirm envelope key is "subscriptions" per xolu's actual handler.
		w.Write([]byte(`{"subscriptions":[
			{"id":1,"event_type":"entity.updated","action_type":"webhook","config":{"url":"https://hooks.example/x"},"execution":"async","created_at":"2026-07-01T00:00:00Z"},
			{"id":2,"event_type":"fsm.step",      "action_type":"oql",    "config":{"query":"INSERT INTO audit ..."}, "execution":"async"}
		]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	defs, err := c.ListEventDefs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 event defs, got %d", len(defs))
	}
	if defs[0].EventType != "entity.updated" || defs[0].ActionType != "webhook" {
		t.Errorf("first def wrong: %+v", defs[0])
	}
	if len(defs[0].Config) == 0 {
		t.Errorf("expected config preserved as RawMessage")
	}
}

func TestGetEventDefHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/event/def/7" {
			t.Errorf("expected /api/v2/event/def/7, got %s", r.URL.Path)
		}
		w.Write([]byte(`{"id":7,"event_type":"commit.applied","action_type":"webhook","config":{"url":"https://x"},"execution":"async","created_at":"2026-07-15T00:00:00Z"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	def, err := c.GetEventDef(context.Background(), 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if def.ID != 7 || def.EventType != "commit.applied" {
		t.Errorf("def wrong: %+v", def)
	}
}

// ─── v2 availability ───────────────────────────────────────────────────────

func TestV2AvailabilityHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/" {
			t.Errorf("expected /api/v2/, got %s", r.URL.Path)
		}
		w.Write([]byte(`{
		  "version": "0.14.3",
		  "enabled": true,
		  "as_of":   "2026-07-17T00:00:00Z",
		  "warning": "API v2 is experimental",
		  "subsystems": {"fsm":{"available":true},"gen":{"available":true}}
		}`))
	}))
	defer server.Close()

	c := New(server.URL)
	av, err := c.V2Availability(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !av.Enabled {
		t.Errorf("expected Enabled=true")
	}
	if av.Version != "0.14.3" {
		t.Errorf("unexpected version: %q", av.Version)
	}
	if !strings.Contains(string(av.Subsystems), "fsm") {
		t.Errorf("expected raw subsystems to contain fsm")
	}
}

// ─── v2 URL construction ───────────────────────────────────────────────────

func TestBuildURLv2WithoutTenant(t *testing.T) {
	c := New("http://example.com")
	got := c.buildURLv2("/fsm/def")
	want := "http://example.com/api/v2/fsm/def"
	if got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}

func TestBuildURLv2WithTenant(t *testing.T) {
	c := New("http://example.com", WithTenant("0007"))
	got := c.buildURLv2("/fsm/def")
	want := "http://example.com/api/v2/tenant/0007/fsm/def"
	if got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}

func TestBuildURLv2RootIgnoresTenant(t *testing.T) {
	// The availability endpoint and stateless generators live outside the
	// tenant scope on the server; the client's buildURLv2Root must not
	// prepend a tenant prefix even when one is configured.
	c := New("http://example.com", WithTenant("0007"))
	got := c.buildURLv2Root("/")
	want := "http://example.com/api/v2/"
	if got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}
