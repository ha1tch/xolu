//go:build integration

// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client_test

// integration_test.go — T-26 (minimal form, decided in M4a / D-iii): the
// declared-scope client surface exercised against a real in-process xolu
// server over real HTTP. Happy paths only by design; the mock-based unit
// tests own the error matrices. The point of this suite is the class of
// defect the mocks structurally cannot catch — wire-shape drift like
// T-32, where the mock asserts the same wrong shape it constructs.
//
// Run with:
//
//	go test -tags integration ./pkg/client/ -count=1
//
// and locally (recommended before release) with -race.
//
// Seeding note: where the declared scope is read-only (schema create,
// sequence define, FSM/event def create), the suite seeds via raw HTTP
// against the server's own endpoints; calendar seeding goes through
// server.CalManagerForTest(), the same facade the server's HTTP tests
// use, since calendar provisioning is deliberately not on the wire.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/ha1tch/xolu/pkg/cache"
	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/client"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/validation"
)

type integrationEnv struct {
	srv *server.Server
	ts  *httptest.Server
	c   *client.Client
}

func bootServer(t *testing.T) *integrationEnv {
	t.Helper()
	cfg := config.Default()
	cfg.BaseDir = t.TempDir()
	cfg.AuthType = "none"
	cfg.APIV2Enabled = true
	cfg.CalEnabled = true
	cfg.FullTextEnabled = true

	dbPath := sl.SharedStorePath(cfg.BaseDir)
	if cfg.SQLitePerFileTenants {
		dbPath = sl.TenantStorePath(cfg.BaseDir, 0)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store, err := storage.NewStore("sqlite", map[string]interface{}{
		"db_path":           dbPath,
		"full_text_enabled": true, // NB: the store indexes only when ITS flag is set — the server flag alone just opens the 503 gate
	})
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	memCache := cache.NewMemoryCache(1000, 300*time.Second)
	g := graph.NewFlatGraph()
	validator := validation.NewJSONSchemaValidator(filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas"))
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Stop()
		store.Close()
	})
	return &integrationEnv{srv: srv, ts: ts, c: client.New(ts.URL)}
}

// seedJSON posts a raw JSON body to a server path and fails the test on a
// non-2xx response. Used for the surfaces the client deliberately treats
// as read-only.
func seedJSON(t *testing.T, env *integrationEnv, path string, body interface{}) map[string]interface{} {
	t.Helper()
	buf, _ := json.Marshal(body)
	resp, err := http.Post(env.ts.URL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		t.Fatalf("seed %s: status %d: %v", path, resp.StatusCode, out)
	}
	return out
}

// ─── Health / availability ──────────────────────────────────────────────────

func TestIntegration_HealthAndAvailability(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()
	if err := env.c.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if err := env.c.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	av, err := env.c.V2Availability(ctx)
	if err != nil {
		t.Fatalf("V2Availability: %v", err)
	}
	if av == nil {
		t.Fatal("V2Availability: nil result")
	}
}

// ─── Entity CRUD + commit + search + queries ────────────────────────────────

func TestIntegration_EntityLifecycle(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	created, err := env.c.Create(ctx, "widget", map[string]any{"name": "alpha", "size": 3})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("Create: zero ID")
	}

	got, err := env.c.Get(ctx, "widget", created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Data["name"] != "alpha" {
		t.Errorf("Get: name %v", got.Data["name"])
	}

	if _, err := env.c.Update(ctx, "widget", created.ID, map[string]any{"name": "alpha", "size": 4}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := env.c.Patch(ctx, "widget", created.ID, map[string]any{"size": 5}); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	list, err := env.c.List(ctx, "widget", nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Entities) != 1 {
		t.Errorf("List: want 1, got %d", len(list.Entities))
	}

	if _, err := env.c.Save(ctx, "widget", created.ID+100, map[string]any{"name": "beta"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := env.c.OQL(ctx, "SELECT name FROM widget WHERE size = 5")
	if err != nil {
		t.Fatalf("OQL: %v", err)
	}
	if len(res.Data) != 1 || res.Data[0]["name"] != "alpha" {
		t.Errorf("OQL: unexpected %v", res.Data)
	}

	sr, err := env.c.Search(ctx, "widget", client.SearchParams{Query: "alpha", Entity: "widget"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(sr) == 0 {
		t.Error("Search: expected at least one hit")
	}

	if err := env.c.Delete(ctx, "widget", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestIntegration_CommitAndGraph(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	// Create a user, then commit a post referencing it so the graph
	// gains an edge.
	u, err := env.c.Create(ctx, "users", map[string]any{"name": "ada"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	_, err = env.c.Commit(ctx, client.CommitRequest{
		Update: client.CommitUpdate{Entity: "users", ID: u.ID,
			Data: map[string]any{"name": "ada"}},
		Append: []client.CommitAppend{{Entity: "posts",
			Data: map[string]any{"title": "hello", "author_id": fmt.Sprintf("%d", u.ID)}}},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	q, err := env.c.GraphQuery(ctx, "MATCH (p:posts) RETURN p", 3)
	if err != nil {
		t.Fatalf("GraphQuery: %v", err)
	}
	if len(q.Result) == 0 {
		t.Error("GraphQuery: expected a row")
	}

	nodeID := fmt.Sprintf("users:%d", u.ID)
	if _, err := env.c.GraphNeighbors(ctx, nodeID, "both"); err != nil {
		t.Fatalf("GraphNeighbors: %v", err)
	}
}

// ─── Schema surface ─────────────────────────────────────────────────────────

func TestIntegration_SchemaGetAndList(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	seedJSON(t, env, "/api/v1/schema/asset", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"label": map[string]interface{}{"type": "string"},
		},
	})

	es, err := env.c.GetEntitySchema(ctx, "asset")
	if err != nil {
		t.Fatalf("GetEntitySchema: %v", err)
	}
	if es.Name != "asset" || len(es.Fields) != 1 {
		t.Errorf("schema shape: %+v", es)
	}

	types, err := env.c.ListEntityTypes(ctx)
	if err != nil {
		t.Fatalf("ListEntityTypes: %v", err)
	}
	if len(types) != 1 || types[0].Name != "asset" {
		t.Errorf("ListEntityTypes: %+v", types)
	}
}

// ─── Sequences and generators ───────────────────────────────────────────────

func TestIntegration_SequencesAndGenerators(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	seedJSON(t, env, "/api/v2/gen/seq", map[string]interface{}{
		"name": "orders", "start": 100, "increment_by": 2,
	})

	seq, err := env.c.GetSequence(ctx, "orders")
	if err != nil {
		t.Fatalf("GetSequence: %v", err)
	}
	// T-32 regression on the real wire: IncrementBy must round-trip.
	if seq.IncrementBy != 2 {
		t.Errorf("GetSequence: IncrementBy want 2, got %d (T-32 regression)", seq.IncrementBy)
	}
	if seq.Name != "orders" || seq.Start != 100 {
		t.Errorf("GetSequence shape: %+v", seq)
	}

	seqs, err := env.c.ListSequences(ctx)
	if err != nil || len(seqs) != 1 {
		t.Fatalf("ListSequences: %v / %+v", err, seqs)
	}
	if seqs[0].IncrementBy != 2 {
		t.Errorf("ListSequences: IncrementBy want 2, got %d", seqs[0].IncrementBy)
	}

	seedJSON(t, env, "/api/v2/gen/token", map[string]interface{}{
		"name": "sess", "config": map[string]interface{}{"length": 16},
	})
	gens, err := env.c.ListGenerators(ctx, client.GeneratorKind("token"))
	if err != nil || len(gens) != 1 {
		t.Fatalf("ListGenerators: %v / %+v", err, gens)
	}
}

// ─── FSM surface ────────────────────────────────────────────────────────────

func TestIntegration_FSMLifecycle(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	def := seedJSON(t, env, "/api/v2/fsm/def", map[string]interface{}{
		"name":        "toggle",
		"initial":     "Off",
		"determinism": "strict",
		"states": map[string]interface{}{
			"Off":  map[string]interface{}{"terminal": false},
			"On":   map[string]interface{}{"terminal": false},
			"Dead": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "Off", "input": "flip", "to": "On"},
			{"from": "On", "input": "flip", "to": "Off"},
			{"from": "On", "input": "kill", "to": "Dead"},
		},
	})
	defID := int64(def["id"].(float64))

	defs, err := env.c.ListMachineDefs(ctx)
	if err != nil || len(defs) != 1 {
		t.Fatalf("ListMachineDefs: %v / %+v", err, defs)
	}
	if _, err := env.c.GetMachineDef(ctx, defID); err != nil {
		t.Fatalf("GetMachineDef: %v", err)
	}

	m, err := env.c.CreateMachine(ctx, client.CreateMachineRequest{Definition: defID, Ref: "switch-1"})
	if err != nil {
		t.Fatalf("CreateMachine: %v", err)
	}

	wr, err := env.c.WalkMachine(ctx, m.ID, client.WalkRequest{Input: "flip"})
	if err != nil {
		t.Fatalf("WalkMachine: %v", err)
	}
	if wr.Current != "On" {
		t.Errorf("walk: want On, got %s", wr.Current)
	}

	st, err := env.c.GetMachineState(ctx, m.ID)
	if err != nil || st.State != "On" {
		t.Fatalf("GetMachineState: %v / %+v", err, st)
	}
	if _, err := env.c.GetMachineVars(ctx, m.ID); err != nil {
		t.Fatalf("GetMachineVars: %v", err)
	}
	tr, err := env.c.GetMachineTransitions(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMachineTransitions: %v", err)
	}
	found := false
	for _, in := range tr.Inputs {
		if in == "flip" {
			found = true
		}
	}
	if !found {
		t.Fatalf("GetMachineTransitions: 'flip' not in %v", tr.Inputs)
	}
	hist, err := env.c.GetMachineHistory(ctx, m.ID)
	if err != nil || len(hist) == 0 {
		t.Fatalf("GetMachineHistory: %v / %d entries", err, len(hist))
	}
	if _, err := env.c.ListMachines(ctx, nil); err != nil {
		t.Fatalf("ListMachines: %v", err)
	}
	if err := env.c.DeleteMachine(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMachine: %v", err)
	}
}

// ─── Event definitions (read surface) ───────────────────────────────────────

func TestIntegration_EventDefs(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	seedJSON(t, env, "/api/v2/event/def", map[string]interface{}{
		"event_type":  "entity.created",
		"action_type": "webhook",
		"config":      map[string]interface{}{"url": "https://hooks.example/x"},
	})

	defs, err := env.c.ListEventDefs(ctx)
	if err != nil || len(defs) != 1 {
		t.Fatalf("ListEventDefs: %v / %+v", err, defs)
	}
	if _, err := env.c.GetEventDef(ctx, defs[0].ID); err != nil {
		t.Fatalf("GetEventDef: %v", err)
	}
}

// ─── Cal: the real Openings→Check→Propose→Confirm flow ─────────────────────

func TestIntegration_CalFullFlow(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	mgr := env.srv.CalManagerForTest()
	if _, err := mgr.CreateCalendar(0, cal.Calendar{
		CalendarID:   "room-a",
		DefaultState: cal.StateBinding,
		MatchPolicy:  cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("seed calendar: %v", err)
	}

	from := time.Now().UTC().Truncate(time.Hour).Add(24 * time.Hour)
	to := from.Add(8 * time.Hour)

	res, err := env.c.CalOpenings(ctx, "room-a", from, to, time.Hour, client.ObjectiveEarliest)
	if err != nil {
		t.Fatalf("CalOpenings: %v", err)
	}
	if len(res.Openings) == 0 {
		t.Fatal("CalOpenings: empty calendar should have openings")
	}
	o := res.Openings[0]

	chk, err := env.c.CalCheck(ctx, "room-a", client.CalSpan{Start: o.Start, End: o.End})
	if err != nil {
		t.Fatalf("CalCheck: %v", err)
	}
	if !chk.Feasible {
		t.Fatalf("CalCheck: opening from CalOpenings must be feasible (T-29 property over the wire)")
	}

	b, err := env.c.CalPropose(ctx, client.CalProposeRequest{
		BookingID:  "it-b1",
		CalendarID: "room-a",
		Span:       client.CalSpan{Start: o.Start, End: o.End},
		Bearer:     1,
	})
	if err != nil {
		t.Fatalf("CalPropose: %v", err)
	}
	if b.State != "proposed" {
		t.Errorf("propose: state %s", b.State)
	}

	cb, err := env.c.CalConfirm(ctx, "room-a", "it-b1")
	if err != nil {
		t.Fatalf("CalConfirm: %v", err)
	}
	if cb.State != "binding" {
		t.Errorf("confirm: state %s", cb.State)
	}

	// The confirmed span must now be infeasible.
	chk2, err := env.c.CalCheck(ctx, "room-a", client.CalSpan{Start: o.Start, End: o.End})
	if err != nil {
		t.Fatalf("CalCheck after confirm: %v", err)
	}
	if chk2.Feasible {
		t.Error("confirmed span still reported feasible")
	}
}
