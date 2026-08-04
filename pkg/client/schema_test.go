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

// ─── DefineEntitySchema (T-147) ─────────────────────────────────────────────

func TestDefineEntitySchemaHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/schema/widgets" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]interface{}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		// The body must be the raw schema itself, no envelope wrapping it.
		if decoded["type"] != "object" {
			t.Errorf("expected the raw schema as the body, got %v", decoded)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"message":"Schema for widgets created/updated successfully"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
		"required": []string{"name"},
	}
	err := c.DefineEntitySchema(context.Background(), "widgets", schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefineEntitySchemaInvalidName_RejectedClientSide(t *testing.T) {
	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL)
	cases := []string{"", "1widget", "widget-name", "widget name", "widget.name"}
	for _, name := range cases {
		err := c.DefineEntitySchema(context.Background(), name, map[string]interface{}{"type": "object"})
		if err == nil {
			t.Errorf("name %q: expected a validation error, got nil", name)
		}
	}
	if serverCalled {
		t.Error("server was called despite every name being invalid -- validation should happen before the request")
	}
}

func TestDefineEntitySchemaValidNameShapes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	cases := []string{"widgets", "Widget2", "widget_type", "a"}
	for _, name := range cases {
		if err := c.DefineEntitySchema(context.Background(), name, map[string]interface{}{"type": "object"}); err != nil {
			t.Errorf("name %q: unexpected error: %v", name, err)
		}
	}
}

func TestDefineEntitySchemaServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":"XOLU-VL003","message":"invalid entity name","status":400}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DefineEntitySchema(context.Background(), "widgets", map[string]interface{}{"type": "object"})
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusBadRequest {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}

func TestDefineEntitySchemaUpdatesExisting(t *testing.T) {
	// The server's own response says "created/updated" regardless --
	// this client makes no distinction, and calling it twice for the
	// same entity type must both succeed identically.
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"message":"Schema for widgets created/updated successfully"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	schema := map[string]interface{}{"type": "object"}
	if err := c.DefineEntitySchema(context.Background(), "widgets", schema); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := c.DefineEntitySchema(context.Background(), "widgets", schema); err != nil {
		t.Fatalf("second call (update): %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 requests, got %d", callCount)
	}
}

// ─── ListEntities ───────────────────────────────────────────────────────────

func TestListEntitiesHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entities" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string when includeGraph=false, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"count":2,"entities":[
			{"entity_type":"gizmos","count":1,"has_schema":true,"adapted":true,"columns":["sku"]},
			{"entity_type":"widgets","count":2,"has_schema":false,"adapted":false,"first_seen":"2026-08-04 00:00:00","last_update":"2026-08-04 00:00:00"}
		]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	entities, err := c.ListEntities(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("got %d entities, want 2", len(entities))
	}
	if entities[0].EntityType != "gizmos" || !entities[0].Adapted || !entities[0].HasSchema {
		t.Errorf("gizmos entry: got %+v", entities[0])
	}
	if entities[1].EntityType != "widgets" || entities[1].Adapted || entities[1].HasSchema {
		t.Errorf("widgets entry: got %+v", entities[1])
	}
	if entities[1].FirstSeen == "" {
		t.Error("widgets: expected FirstSeen populated")
	}
}

func TestListEntitiesIncludeGraphSetsQueryParam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("include_graph") != "true" {
			t.Errorf("expected include_graph=true in the query, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"count":1,"entities":[{"entity_type":"authors","count":1,"has_schema":false,"adapted":false,
			"graph":{"out_edges":0,"in_edges":2,"relationship_types":["author"]}}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	entities, err := c.ListEntities(context.Background(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entities[0].Graph == nil {
		t.Fatal("expected Graph to be populated")
	}
	if entities[0].Graph.InEdges != 2 {
		t.Errorf("InEdges: got %d, want 2", entities[0].Graph.InEdges)
	}
	if len(entities[0].Graph.RelationshipTypes) != 1 || entities[0].Graph.RelationshipTypes[0] != "author" {
		t.Errorf("RelationshipTypes: got %v", entities[0].Graph.RelationshipTypes)
	}
}

func TestListEntitiesEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"count":0,"entities":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	entities, err := c.ListEntities(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entities == nil {
		t.Error("expected an empty slice, not nil")
	}
	if len(entities) != 0 {
		t.Errorf("got %d entities, want 0", len(entities))
	}
}

func TestListEntitiesServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte(`{"error":{"code":"XOLU-ST001","message":"not supported","status":501}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ListEntities(context.Background(), false)
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotImplemented {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}

// ─── FSM definition writes ──────────────────────────────────────────────────

func TestCreateMachineDefHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/def" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"name":"traffic-light"`) {
			t.Errorf("expected the spec in the body, got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1,"name":"traffic-light","created_at":"2026-08-04T00:00:00Z",
			"analysis":{"reachable":true,"deterministic":true,"determinism":"firstmatch",
			"terminal_states":[],"warnings":["state 'blink' is unreachable"]}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	spec := MachineSpec{
		Name:        "traffic-light",
		Initial:     "red",
		Determinism: "firstmatch",
		States:      map[string]StateDef{"red": {}, "green": {}},
		Transitions: []TransitionDef{},
	}
	result, err := c.CreateMachineDef(context.Background(), spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != 1 || result.Name != "traffic-light" {
		t.Errorf("got %+v", result)
	}
	if result.Analysis == nil {
		t.Fatal("expected Analysis to be populated")
	}
	if !result.Analysis.Reachable || !result.Analysis.Deterministic {
		t.Errorf("Analysis: got %+v", result.Analysis)
	}
	if len(result.Analysis.Warnings) != 1 {
		t.Errorf("Warnings: got %v", result.Analysis.Warnings)
	}
}

func TestCreateMachineDefValidationRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM006","message":"determinism must be declared","status":422}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CreateMachineDef(context.Background(), MachineSpec{Name: "bad"})
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusUnprocessableEntity {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
	if xoluErr.Code != "XOLU-FSM006" {
		t.Errorf("Code: got %q", xoluErr.Code)
	}
}

func TestReplaceMachineDefHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/def/7" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodPut {
			t.Errorf("method: got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":7,"name":"updated","analysis":{"reachable":true,"deterministic":true,"determinism":"firstmatch","terminal_states":[]}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.ReplaceMachineDef(context.Background(), 7, MachineSpec{Name: "updated"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != 7 || result.Name != "updated" {
		t.Errorf("got %+v", result)
	}
}

func TestReplaceMachineDefInvalidID(t *testing.T) {
	c := New("http://example.com")
	_, err := c.ReplaceMachineDef(context.Background(), 0, MachineSpec{})
	if err == nil {
		t.Fatal("expected an error for id=0")
	}
	_, err = c.ReplaceMachineDef(context.Background(), -1, MachineSpec{})
	if err == nil {
		t.Fatal("expected an error for a negative id")
	}
}

func TestReplaceMachineDefNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM001","message":"definition not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ReplaceMachineDef(context.Background(), 999, MachineSpec{Name: "x"})
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}

func TestDeleteMachineDefHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/def/3" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("method: got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL)
	if err := c.DeleteMachineDef(context.Background(), 3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteMachineDefInvalidID(t *testing.T) {
	c := New("http://example.com")
	if err := c.DeleteMachineDef(context.Background(), 0); err == nil {
		t.Fatal("expected an error for id=0")
	}
}

func TestDeleteMachineDefNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM001","message":"definition not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	err := c.DeleteMachineDef(context.Background(), 999)
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}

func TestValidateMachineDefValid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/fsm/def/validate" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // always 200, even though this test is the "valid" case
		w.Write([]byte(`{"valid":true,"analysis":{"reachable":true,"deterministic":true,"determinism":"firstmatch","terminal_states":["done"]}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.ValidateMachineDef(context.Background(), MachineSpec{Name: "ok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Error("Valid: got false, want true")
	}
	if result.Analysis == nil {
		t.Fatal("expected Analysis on a valid result")
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors should be empty on a valid result, got %v", result.Errors)
	}
}

func TestValidateMachineDefInvalid_StillNoGoError(t *testing.T) {
	// The whole point of this endpoint: an invalid spec is a normal,
	// successful (200) response, never a Go error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"valid":false,"errors":[{"code":"XOLU-FSM006","message":"determinism must be declared"}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.ValidateMachineDef(context.Background(), MachineSpec{Name: "bad"})
	if err != nil {
		t.Fatalf("an invalid spec must not produce a Go error, got: %v", err)
	}
	if result.Valid {
		t.Error("Valid: got true, want false")
	}
	if len(result.Errors) != 1 || result.Errors[0].Code != "XOLU-FSM006" {
		t.Errorf("Errors: got %+v", result.Errors)
	}
	if result.Analysis != nil {
		t.Error("Analysis should be nil/absent on an invalid result")
	}
}

func TestValidateMachineDefTransportErrorIsGoError(t *testing.T) {
	// Distinguishing case: an actual transport-level failure (this
	// endpoint returning something other than 200, which shouldn't
	// normally happen but the client must still handle correctly)
	// DOES produce a Go error, unlike an invalid spec.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ValidateMachineDef(context.Background(), MachineSpec{Name: "x"})
	if err == nil {
		t.Fatal("expected an error for a genuine 500")
	}
}

// ─── MachineDef.ParsedAnalysis ──────────────────────────────────────────────

func TestParsedAnalysis_HappyPath(t *testing.T) {
	def := &MachineDef{
		Analysis: json.RawMessage(`{"reachable":true,"deterministic":false,"determinism":"exclusive","terminal_states":["end"],"cycles":["a->b->a"],"warnings":["w1"]}`),
	}
	a, err := def.ParsedAnalysis()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected a non-nil result")
	}
	if !a.Reachable || a.Deterministic {
		t.Errorf("got %+v", a)
	}
	if len(a.Cycles) != 1 || a.Cycles[0] != "a->b->a" {
		t.Errorf("Cycles: got %v", a.Cycles)
	}
}

func TestParsedAnalysis_Empty(t *testing.T) {
	def := &MachineDef{}
	a, err := def.ParsedAnalysis()
	if err != nil {
		t.Fatalf("unexpected error for empty Analysis: %v", err)
	}
	if a != nil {
		t.Errorf("expected nil for empty Analysis, got %+v", a)
	}
}

func TestParsedAnalysis_Malformed(t *testing.T) {
	def := &MachineDef{Analysis: json.RawMessage(`not valid json`)}
	_, err := def.ParsedAnalysis()
	if err == nil {
		t.Fatal("expected an error for malformed Analysis JSON")
	}
}
