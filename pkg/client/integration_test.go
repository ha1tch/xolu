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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestIntegration_SchemaEndpoints_TenantConfigured is the direct
// regression test for the xoluman-reported bug: a tenant-scoped client
// (the normal, expected configuration for any real multi-tenant
// deployment) calling any of the three schema methods must reach the
// server's actual global schema endpoints, not a mis-routed
// tenant-prefixed URL. Uses a second client against the same live
// server as bootServer's own default client, configured with a tenant
// that is never actually provisioned -- deliberately, since these
// calls must succeed without ever touching tenant resolution at all
// once correctly routed to the tenant-independent endpoint.
func TestIntegration_SchemaEndpoints_TenantConfigured(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()
	tenantClient := client.New(env.ts.URL, client.WithTenant("acme_crm_unprovisioned"))

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		"required":   []string{"name"},
	}
	if err := tenantClient.DefineEntitySchema(ctx, "companies", schema); err != nil {
		t.Fatalf("DefineEntitySchema with a tenant configured: %v", err)
	}

	got, err := tenantClient.GetEntitySchema(ctx, "companies")
	if err != nil {
		t.Fatalf("GetEntitySchema with a tenant configured: %v", err)
	}
	foundName := false
	for _, f := range got.Fields {
		if f.Name == "name" && f.Required {
			foundName = true
		}
	}
	if !foundName {
		t.Errorf("GetEntitySchema did not read back the 'name' field as required: %+v", got.Fields)
	}

	types, err := tenantClient.ListEntityTypes(ctx)
	if err != nil {
		t.Fatalf("ListEntityTypes with a tenant configured: %v", err)
	}
	found := false
	for _, ty := range types {
		if ty.Name == "companies" {
			found = true
		}
	}
	if !found {
		t.Errorf("ListEntityTypes did not include 'companies': %+v", types)
	}
}

func bootServer(t *testing.T) *integrationEnv {
	t.Helper()
	cfg := config.Default()
	cfg.BaseDir = t.TempDir()
	cfg.AuthType = "none"
	cfg.APIV2Enabled = true
	cfg.CalEnabled = true
	cfg.BalEnabled = true
	cfg.BlobEnabled = true
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

// bootServerWithTS mirrors bootServer exactly, with TimeseriesEnabled
// added -- kept as its own dedicated helper (matching the existing
// bootServerWithAPIKeyAuth precedent) rather than folding into the
// shared bootServer, so enabling timeseries here can never affect any
// other test's own boot config.
func bootServerWithTS(t *testing.T) *integrationEnv {
	t.Helper()
	cfg := config.Default()
	cfg.BaseDir = t.TempDir()
	cfg.AuthType = "none"
	cfg.APIV2Enabled = true
	cfg.CalEnabled = true
	cfg.BalEnabled = true
	cfg.BlobEnabled = true
	cfg.FullTextEnabled = true
	cfg.TimeseriesEnabled = true
	cfg.TenantMode = "path"
	cfg.TenantAutoRegister = true

	dbPath := sl.SharedStorePath(cfg.BaseDir)
	if cfg.SQLitePerFileTenants {
		dbPath = sl.TenantStorePath(cfg.BaseDir, 0)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store, err := storage.NewStore("sqlite", map[string]interface{}{
		"db_path":           dbPath,
		"full_text_enabled": true,
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
	return &integrationEnv{srv: srv, ts: ts, c: client.New(ts.URL, client.WithTenant("default"))}
}

// bootServerMultiTenant mirrors bootServer exactly, with TenantAutoRegister
// added -- its own dedicated helper (matching bootServerWithTS's own
// precedent) since named-tenant auto-registration is off by default
// and no existing helper turns it on. Added directly off the back of
// finding CalListBookings itself had no tenant-isolation test, despite
// the JOIN fix earlier in this same session (XOT173) getting exactly
// this kind of check and CalListBookings not -- the returned
// integrationEnv's own client has no fixed tenant; callers construct
// their own per-tenant clients against env.ts.URL as needed.
func bootServerMultiTenant(t *testing.T) *integrationEnv {
	t.Helper()
	cfg := config.Default()
	cfg.BaseDir = t.TempDir()
	cfg.AuthType = "none"
	cfg.APIV2Enabled = true
	cfg.CalEnabled = true
	cfg.BalEnabled = true
	cfg.BlobEnabled = true
	cfg.FullTextEnabled = true
	cfg.TenantMode = "path"
	cfg.TenantAutoRegister = true

	dbPath := sl.SharedStorePath(cfg.BaseDir)
	if cfg.SQLitePerFileTenants {
		dbPath = sl.TenantStorePath(cfg.BaseDir, 0)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store, err := storage.NewStore("sqlite", map[string]interface{}{
		"db_path":           dbPath,
		"full_text_enabled": true,
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

// TestIntegration_APIKeyAuth_CorrectAndWrongCredential is the direct
// end-to-end proof for T-160 (the client's own apikey auth header
// format was wrong -- "Bearer", never accepted by the server's own
// apikey validator) and T-161 (TestConnection, which needed T-160
// fixed to be able to tell a valid credential from an invalid one at
// all) together, against a real, credential-enforcing server -- not
// a mock that would just capture whatever the client sends.
func TestIntegration_APIKeyAuth_CorrectAndWrongCredential(t *testing.T) {
	env := bootServerWithAPIKeyAuth(t, "the-real-key")
	ctx := context.Background()

	if err := env.c.TestConnection(ctx); err != nil {
		t.Fatalf("TestConnection with the correct key: %v", err)
	}

	wrongClient := client.New(env.ts.URL, client.WithAPIKey("a-wrong-key"))
	err := wrongClient.TestConnection(ctx)
	if err == nil {
		t.Fatal("TestConnection with a wrong key should fail, got nil error")
	}
	xoluErr, ok := err.(*client.Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("HTTPStatus: got %d, want 401", xoluErr.HTTPStatus)
	}

	noCredClient := client.New(env.ts.URL)
	if err := noCredClient.TestConnection(ctx); err == nil {
		t.Fatal("TestConnection with no credential configured against an apikey-enforcing server should fail, got nil error")
	}
}

// configured server-side instead of AuthType "none" -- for testing
// T-160 (the client's own apikey auth header format) and T-161
// (TestConnection) together against a real, credential-enforcing
// server, not a mock that would just capture whatever the client
// happens to send.
func bootServerWithAPIKeyAuth(t *testing.T, validKey string) *integrationEnv {
	t.Helper()
	cfg := config.Default()
	cfg.BaseDir = t.TempDir()
	cfg.AuthType = "apikey"
	cfg.APIKeys = []string{validKey}
	cfg.APIV2Enabled = true

	dbPath := sl.SharedStorePath(cfg.BaseDir)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": dbPath})
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
	return &integrationEnv{srv: srv, ts: ts, c: client.New(ts.URL, client.WithAPIKey(validKey))}
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

// ─── ts ──────────────────────────────────────────────────────────────────

// TestIntegration_TSReadSlice exercises xoluman's own "minimum slice"
// (XM-2, XOT172) against a real server: provision the tenant, define a
// timeline, append two events, define a rollup -- all seeded via raw
// HTTP since write-side ts methods are a separate, later follow-up,
// not in this slice -- then exercise all 5 client methods
// (TSListTimelines, TSGetTimeline, TSQueryRange, TSRollupList,
// TSRollupGet) against that seeded state.
func TestIntegration_TSReadSlice(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	seedJSON(t, env, "/api/v1/tenant/default/ts/provision", nil)

	tlResp := seedJSON(t, env, "/api/v1/tenant/default/ts/tl/def", map[string]interface{}{
		"id": 1, "name": "cpu_usage", "dims": 1, "retention_days": 30,
	})
	if tlResp["id"] == nil {
		t.Fatalf("seed timeline: unexpected response %v", tlResp)
	}

	now := time.Now().UTC().Truncate(time.Second)
	seedJSON(t, env, "/api/v1/tenant/default/ts/events", map[string]interface{}{
		"timeline": 1, "dims": []int{7}, "time": now.Add(-time.Hour).Format(time.RFC3339), "nums": []float64{1.5},
	})
	seedJSON(t, env, "/api/v1/tenant/default/ts/events", map[string]interface{}{
		"timeline": 1, "dims": []int{7}, "time": now.Format(time.RFC3339), "nums": []float64{2.5},
	})

	seedJSON(t, env, "/api/v1/tenant/default/ts/tl/def", map[string]interface{}{
		"id": 2, "name": "cpu_usage_hourly", "dims": 1,
	})

	rollupResp := seedJSON(t, env, "/api/v1/tenant/default/ts/tl/1/rollup/def", map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1h",
	})
	rollupID, _ := rollupResp["id"].(string)
	if rollupID == "" {
		t.Fatalf("seed rollup: unexpected response %v", rollupResp)
	}

	// TSListTimelines
	tls, err := env.c.TSListTimelines(ctx)
	if err != nil {
		t.Fatalf("TSListTimelines: %v", err)
	}
	if len(tls) == 0 {
		t.Fatal("TSListTimelines: expected at least the seeded timeline, got none")
	}
	found := false
	for _, tl := range tls {
		if tl.ID == 1 && tl.Name == "cpu_usage" {
			found = true
		}
	}
	if !found {
		t.Errorf("TSListTimelines: seeded timeline (id=1, name=cpu_usage) not found in %+v", tls)
	}

	// TSGetTimeline
	tl, err := env.c.TSGetTimeline(ctx, 1)
	if err != nil {
		t.Fatalf("TSGetTimeline: %v", err)
	}
	if tl.Name != "cpu_usage" || tl.Dims != 1 {
		t.Errorf("TSGetTimeline: unexpected timeline %+v", tl)
	}

	// TSQueryRange
	qr, err := env.c.TSQueryRange(ctx, client.TSQueryRangeRequest{
		Timeline: 1, Dims: []uint64{7},
		From: now.Add(-2 * time.Hour), To: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("TSQueryRange: %v", err)
	}
	if qr.Count != 2 || len(qr.Events) != 2 {
		t.Fatalf("TSQueryRange: want 2 events, got count=%d len=%d: %+v", qr.Count, len(qr.Events), qr.Events)
	}

	// TSRollupList
	rollups, err := env.c.TSRollupList(ctx, 1)
	if err != nil {
		t.Fatalf("TSRollupList: %v", err)
	}
	if len(rollups) != 1 || rollups[0].ID != rollupID {
		t.Fatalf("TSRollupList: want 1 rollup with id %q, got %+v", rollupID, rollups)
	}
	if rollups[0].SourceTID != 1 || rollups[0].DestTID != 2 {
		t.Errorf("TSRollupList: want source_tid=1 dest_tid=2, got %+v", rollups[0])
	}

	// TSRollupGet
	rollup, err := env.c.TSRollupGet(ctx, 1, rollupID)
	if err != nil {
		t.Fatalf("TSRollupGet: %v", err)
	}
	if rollup.ID != rollupID || rollup.BucketDuration == "" {
		t.Errorf("TSRollupGet: unexpected rollup %+v", rollup)
	}
}

// TestIntegration_TSWriteFlow exercises TSAppend and TSBatchAppend
// (XOT212, wave 15) against a real server -- the write-side gap
// types_ts.go's own original header named explicitly as a follow-up.
// Timeline provisioning and definition remain out of this item's own
// scope, still done via raw seedJSON calls exactly as
// TestIntegration_TSReadSlice already does; only the event writes
// themselves go through the new typed methods.
func TestIntegration_TSWriteFlow(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	seedJSON(t, env, "/api/v1/tenant/default/ts/provision", nil)
	seedJSON(t, env, "/api/v1/tenant/default/ts/tl/def", map[string]interface{}{
		"id": 1, "name": "cpu_usage", "dims": 1, "retention_days": 30,
	})

	now := time.Now().UTC().Truncate(time.Second)
	if err := env.c.TSAppend(ctx, client.TSEvent{
		Timeline: 1, Dims: []uint64{7}, Time: now.Add(-time.Hour), Nums: []float64{1.5},
	}); err != nil {
		t.Fatalf("TSAppend: %v", err)
	}

	batch := []client.TSEvent{
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-30 * time.Minute), Nums: []float64{2.0}},
		{Timeline: 1, Dims: []uint64{7}, Time: now, Nums: []float64{2.5}},
	}
	batchRes, err := env.c.TSBatchAppend(ctx, batch)
	if err != nil {
		t.Fatalf("TSBatchAppend: %v", err)
	}
	if batchRes.Total != 2 || batchRes.Accepted != 2 || batchRes.Failed != 0 {
		t.Fatalf("TSBatchAppend: want total=2 accepted=2 failed=0, got %+v", batchRes)
	}

	// All three events (one single-append, two batch) should now be
	// readable through the ALREADY-verified read path -- proving the
	// new write methods and the existing read methods agree on the
	// same real data, not just that each returned 2xx in isolation.
	sliced, err := env.c.TSQueryRange(ctx, client.TSQueryRangeRequest{
		Timeline: 1, Dims: []uint64{7},
		From: now.Add(-2 * time.Hour), To: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("TSQueryRange: %v", err)
	}
	if len(sliced.Events) != 3 {
		t.Fatalf("TSQueryRange after TSAppend+TSBatchAppend: want 3 events, got %d: %+v",
			len(sliced.Events), sliced.Events)
	}

	// A malformed batch (dims count mismatch against the timeline's own
	// definition) should refuse with a structured error, not a partial
	// accept -- confirmed directly against pkg/timeseries's own
	// AppendBatch before writing TSBatchAppendResult's own doc comment;
	// this is that confirmation exercised for real.
	_, err = env.c.TSBatchAppend(ctx, []client.TSEvent{
		{Timeline: 1, Dims: []uint64{7, 8}, Time: now, Nums: []float64{9}},
	})
	if err == nil {
		t.Errorf("TSBatchAppend with a dims mismatch: want a refusal, got success")
	}
}

// TestIntegration_TSLifecycleAndSync exercises timeline lifecycle and
// sync (XOT217, wave 16 Stage 3) against a real server -- provision,
// define, sync toggle both directions, update (verified through the
// already-covered TSGetTimeline as an independent read path, not just
// trusting the write call's own response, and specifically confirming
// Dims is truly unaffected by update, matching what
// TSUpdateTimelineRequest's own doc comment claims), and delete.
// Uses the real typed methods for setup throughout, not raw seedJSON
// the way TestIntegration_TSWriteFlow still does (that test predates
// this stage's own coverage).
func TestIntegration_TSLifecycleAndSync(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	provisioned, err := env.c.TSProvision(ctx)
	if err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	if provisioned.Timeseries != "enabled" {
		t.Fatalf("TSProvision: want timeseries=enabled, got %+v", provisioned)
	}

	tl, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 1, Name: "cpu_usage", Dims: 1, RetentionDays: 30,
	})
	if err != nil {
		t.Fatalf("TSDefineTimeline: %v", err)
	}
	if tl.ID != 1 || tl.Name != "cpu_usage" || tl.Dims != 1 {
		t.Fatalf("TSDefineTimeline: got %+v", tl)
	}

	// Sync defaults to on (NoSync=false) -- confirmed directly, not
	// assumed.
	syncStatus, err := env.c.TSSyncGet(ctx, 1)
	if err != nil {
		t.Fatalf("TSSyncGet: %v", err)
	}
	if syncStatus.NoSync {
		t.Fatalf("TSSyncGet: want NoSync=false by default, got true: %+v", syncStatus)
	}

	off, err := env.c.TSSyncOff(ctx, 1)
	if err != nil {
		t.Fatalf("TSSyncOff: %v", err)
	}
	if !off.NoSync {
		t.Fatalf("TSSyncOff: want NoSync=true, got false: %+v", off)
	}
	on, err := env.c.TSSyncOn(ctx, 1)
	if err != nil {
		t.Fatalf("TSSyncOn: %v", err)
	}
	if on.NoSync {
		t.Fatalf("TSSyncOn: want NoSync=false, got true: %+v", on)
	}

	if _, err := env.c.TSUpdateTimeline(ctx, 1, client.TSUpdateTimelineRequest{
		Name: "cpu_usage_renamed", RetentionDays: 60,
	}); err != nil {
		t.Fatalf("TSUpdateTimeline: %v", err)
	}
	afterUpdate, err := env.c.TSGetTimeline(ctx, 1)
	if err != nil {
		t.Fatalf("TSGetTimeline (after update): %v", err)
	}
	if afterUpdate.Name != "cpu_usage_renamed" || afterUpdate.RetentionDays != 60 {
		t.Fatalf("TSUpdateTimeline: want name/retention updated, got %+v", afterUpdate)
	}
	if afterUpdate.Dims != 1 {
		t.Fatalf("TSUpdateTimeline: want Dims unaffected (immutable), got %d", afterUpdate.Dims)
	}

	if err := env.c.TSDeleteTimeline(ctx, 1); err != nil {
		t.Fatalf("TSDeleteTimeline: %v", err)
	}
	if _, err := env.c.TSGetTimeline(ctx, 1); err == nil {
		t.Errorf("TSGetTimeline after delete: want an error, got a live record")
	}
}

// TestIntegration_TSQueryCompleteness exercises TSQueryRangeGet and
// TSLatest (XOT218, wave 16 Stage 4) against a real server -- the
// GET-based query-string alternative to the already-covered
// TSQueryRange, and the most-recent-events read. Confirms the GET
// variant and the already-verified POST variant (TSQueryRange) agree
// on the same real data, not just that each independently returns
// 2xx -- exactly the kind of cross-check this session's own
// integration suite header explains mocks cannot provide.
func TestIntegration_TSQueryCompleteness(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	if _, err := env.c.TSProvision(ctx); err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 1, Name: "cpu_usage", Dims: 1, RetentionDays: 30,
	}); err != nil {
		t.Fatalf("TSDefineTimeline: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	batch := []client.TSEvent{
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-2 * time.Hour), Nums: []float64{1.0}},
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-1 * time.Hour), Nums: []float64{2.0}},
		{Timeline: 1, Dims: []uint64{7}, Time: now, Nums: []float64{3.0}},
	}
	if _, err := env.c.TSBatchAppend(ctx, batch); err != nil {
		t.Fatalf("TSBatchAppend: %v", err)
	}

	viaPost, err := env.c.TSQueryRange(ctx, client.TSQueryRangeRequest{
		Timeline: 1, Dims: []uint64{7}, From: now.Add(-3 * time.Hour), To: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("TSQueryRange (POST): %v", err)
	}
	viaGet, err := env.c.TSQueryRangeGet(ctx, client.TSQueryRangeRequest{
		Timeline: 1, Dims: []uint64{7}, From: now.Add(-3 * time.Hour), To: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("TSQueryRangeGet: %v", err)
	}
	if len(viaGet.Events) != len(viaPost.Events) || len(viaGet.Events) != 3 {
		t.Fatalf("TSQueryRangeGet vs TSQueryRange: want both to agree on 3 events, got GET=%d POST=%d",
			len(viaGet.Events), len(viaPost.Events))
	}

	latest, err := env.c.TSLatest(ctx, client.TSLatestRequest{Timeline: 1, Dims: []uint64{7}, N: 2})
	if err != nil {
		t.Fatalf("TSLatest: %v", err)
	}
	if len(latest.Events) != 2 {
		t.Fatalf("TSLatest: want the 2 most recent events, got %d: %+v", len(latest.Events), latest.Events)
	}
	if latest.Events[0].Time.Before(latest.Events[len(latest.Events)-1].Time) {
		t.Errorf("TSLatest: want newest-relevant events among the returned set, got %+v", latest.Events)
	}
}

// TestIntegration_TSRollupCompleteness exercises the rollup lifecycle
// (XOT219, wave 16 Stage 5) against a real server -- define,
// parent, run, status, tree, and delete -- building on the
// already-covered TSRollupList/TSRollupGet.
func TestIntegration_TSRollupCompleteness(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	if _, err := env.c.TSProvision(ctx); err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 1, Name: "cpu_raw", Dims: 1,
	}); err != nil {
		t.Fatalf("TSDefineTimeline (source): %v", err)
	}
	if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 2, Name: "cpu_5m", Dims: 1,
	}); err != nil {
		t.Fatalf("TSDefineTimeline (dest): %v", err)
	}

	rollup, err := env.c.TSRollupDefine(ctx, 1, client.TSRollupDefineRequest{
		DestTID: 2, BucketDuration: "5m",
	})
	if err != nil {
		t.Fatalf("TSRollupDefine: %v", err)
	}
	if rollup.SourceTID != 1 || rollup.DestTID != 2 || rollup.BucketDuration != "5m0s" {
		t.Fatalf("TSRollupDefine: got %+v", rollup)
	}

	parent, err := env.c.TSRollupParent(ctx, 2)
	if err != nil {
		t.Fatalf("TSRollupParent: %v", err)
	}
	if parent.ID != rollup.ID {
		t.Fatalf("TSRollupParent: want the rollup just defined (id=%s), got %+v", rollup.ID, parent)
	}

	now := time.Now().UTC()
	if err := env.c.TSAppend(ctx, client.TSEvent{
		Timeline: 1, Dims: []uint64{7}, Time: now.Add(-10 * time.Minute), Nums: []float64{42},
	}); err != nil {
		t.Fatalf("TSAppend (source data for the rollup to consume): %v", err)
	}

	if err := env.c.TSRollupRun(ctx, 1, rollup.ID, client.TSRollupRunRequest{
		From: now.Add(-1 * time.Hour), To: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("TSRollupRun: %v", err)
	}

	status, err := env.c.TSRollupStatus(ctx, 1, rollup.ID)
	if err != nil {
		t.Fatalf("TSRollupStatus: %v", err)
	}
	if status.ID != rollup.ID || status.SourceTID != 1 || status.DestTID != 2 {
		t.Fatalf("TSRollupStatus: got %+v", status)
	}

	tree, err := env.c.TSRollupTree(ctx)
	if err != nil {
		t.Fatalf("TSRollupTree: %v", err)
	}
	if tree == nil {
		t.Fatalf("TSRollupTree: want a non-nil tree -- timelines and a rollup are defined, got nil")
	}

	if err := env.c.TSRollupDelete(ctx, 1, rollup.ID); err != nil {
		t.Fatalf("TSRollupDelete: %v", err)
	}
	if _, err := env.c.TSRollupGet(ctx, 1, rollup.ID); err == nil {
		t.Errorf("TSRollupGet after delete: want an error, got a live record")
	}
}

// TestIntegration_TSAggregates exercises all three aggregate methods
// (XOT220, wave 16 Stage 6) against a real server with real,
// predictable data -- four events on field 0 with known values
// (10, 20, 30, 40), so sum/avg/min/max/count all have a verifiable
// answer, not just "didn't error." Covers both of TSAggregate's own
// response shapes (scalar with no interval, bucketed with one) plus
// TSRangeAggregate and TSFullAggregate with quantiles.
func TestIntegration_TSAggregates(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	if _, err := env.c.TSProvision(ctx); err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 1, Name: "readings", Dims: 1,
	}); err != nil {
		t.Fatalf("TSDefineTimeline: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	batch := []client.TSEvent{
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-3 * time.Hour), Nums: []float64{10}},
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-2 * time.Hour), Nums: []float64{20}},
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-1 * time.Hour), Nums: []float64{30}},
		{Timeline: 1, Dims: []uint64{7}, Time: now, Nums: []float64{40}},
	}
	if _, err := env.c.TSBatchAppend(ctx, batch); err != nil {
		t.Fatalf("TSBatchAppend: %v", err)
	}
	from, to := now.Add(-4*time.Hour), now.Add(time.Minute)

	// Scalar case: no Interval set.
	scalar, err := env.c.TSAggregate(ctx, client.TSAggregateRequest{
		Timeline: 1, Dims: []uint64{7}, From: from, To: to, NumField: 0, Function: "sum",
	})
	if err != nil {
		t.Fatalf("TSAggregate (scalar): %v", err)
	}
	if scalar.Value == nil || *scalar.Value != 100 {
		t.Fatalf("TSAggregate (scalar sum): want 100, got %+v", scalar)
	}
	if scalar.Count == nil || *scalar.Count != 4 {
		t.Fatalf("TSAggregate (scalar count): want 4, got %+v", scalar)
	}
	if len(scalar.Buckets) != 0 {
		t.Errorf("TSAggregate (scalar): want no buckets, got %+v", scalar.Buckets)
	}

	// Bucketed case: Interval set.
	bucketed, err := env.c.TSAggregate(ctx, client.TSAggregateRequest{
		Timeline: 1, Dims: []uint64{7}, From: from, To: to, NumField: 0, Function: "avg", Interval: "1h",
	})
	if err != nil {
		t.Fatalf("TSAggregate (bucketed): %v", err)
	}
	if len(bucketed.Buckets) == 0 {
		t.Fatalf("TSAggregate (bucketed): want at least one bucket, got none")
	}
	if bucketed.Value != nil || bucketed.Count != nil {
		t.Errorf("TSAggregate (bucketed): want scalar fields nil, got Value=%v Count=%v", bucketed.Value, bucketed.Count)
	}

	rangeAgg, err := env.c.TSRangeAggregate(ctx, client.TSRangeAggregateRequest{
		Timeline: 1, Dims: []uint64{7}, From: from, To: to,
	})
	if err != nil {
		t.Fatalf("TSRangeAggregate: %v", err)
	}
	if !rangeAgg.Fields[0] {
		t.Fatalf("TSRangeAggregate: want Fields[0]=true, got %+v", rangeAgg.Fields)
	}
	if rangeAgg.Sums[0] != 100 || rangeAgg.Mins[0] != 10 || rangeAgg.Maxs[0] != 40 || rangeAgg.Count != 4 {
		t.Fatalf("TSRangeAggregate: want sum=100 min=10 max=40 count=4, got sum=%v min=%v max=%v count=%v",
			rangeAgg.Sums[0], rangeAgg.Mins[0], rangeAgg.Maxs[0], rangeAgg.Count)
	}

	fullAgg, err := env.c.TSFullAggregate(ctx, client.TSFullAggregateRequest{
		Timeline: 1, Dims: []uint64{7}, From: from, To: to,
		Quantiles: []float64{0.5}, QuantileFields: []uint8{0},
	})
	if err != nil {
		t.Fatalf("TSFullAggregate: %v", err)
	}
	if fullAgg.Sums[0] != 100 || fullAgg.Count != 4 {
		t.Fatalf("TSFullAggregate: want the same exact stats as TSRangeAggregate, got sum=%v count=%v",
			fullAgg.Sums[0], fullAgg.Count)
	}
	if len(fullAgg.Quantiles[0]) != 1 {
		t.Fatalf("TSFullAggregate: want 1 quantile estimate for field 0, got %v", fullAgg.Quantiles[0])
	}
	if len(fullAgg.Quantiles[1]) != 0 {
		t.Errorf("TSFullAggregate: want field 1 (not in QuantileFields) to have no quantiles, got %v", fullAgg.Quantiles[1])
	}
}

// TestIntegration_TSRetentionStatsAndDataLifecycle exercises
// retention, stats, and data lifecycle (XOT221, wave 16 Stage 7, the
// final ts stage) against a real server. Two timelines: one has its
// own data fully deleted (TSDeleteTimelineData, keeping the
// definition -- confirmed via TSGetTimeline still finding it, and
// TSTimelineStats now showing zero events), the other has a bounded
// range purged (TSPurgeTimelineRange) and the survivors confirmed
// through the already-covered TSQueryRange, not just trusting the
// purge call's own success response.
func TestIntegration_TSRetentionStatsAndDataLifecycle(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	if _, err := env.c.TSProvision(ctx); err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 1, Name: "to-be-emptied", Dims: 1, RetentionDays: 7,
	}); err != nil {
		t.Fatalf("TSDefineTimeline (1): %v", err)
	}
	if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 2, Name: "to-be-purged", Dims: 1, RetentionDays: 14,
	}); err != nil {
		t.Fatalf("TSDefineTimeline (2): %v", err)
	}

	retention, err := env.c.TSGetRetention(ctx)
	if err != nil {
		t.Fatalf("TSGetRetention: %v", err)
	}
	if len(retention.Timelines) != 2 {
		t.Fatalf("TSGetRetention: want 2 timelines, got %d: %+v", len(retention.Timelines), retention.Timelines)
	}

	patched, err := env.c.TSPatchRetention(ctx, 30)
	if err != nil {
		t.Fatalf("TSPatchRetention: %v", err)
	}
	if patched.DefaultRetentionDays != 30 {
		t.Fatalf("TSPatchRetention: got %+v", patched)
	}
	afterPatch, err := env.c.TSGetRetention(ctx)
	if err != nil {
		t.Fatalf("TSGetRetention (after patch): %v", err)
	}
	if afterPatch.DefaultRetentionDays != 30 {
		t.Fatalf("TSGetRetention (after patch): want default=30, got %+v", afterPatch)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := env.c.TSAppend(ctx, client.TSEvent{Timeline: 1, Dims: []uint64{7}, Time: now, Nums: []float64{1}}); err != nil {
		t.Fatalf("TSAppend (timeline 1): %v", err)
	}
	purgeBatch := []client.TSEvent{
		{Timeline: 2, Dims: []uint64{7}, Time: now.Add(-3 * time.Hour), Nums: []float64{1}},
		{Timeline: 2, Dims: []uint64{7}, Time: now.Add(-2 * time.Hour), Nums: []float64{2}},
		{Timeline: 2, Dims: []uint64{7}, Time: now, Nums: []float64{3}},
	}
	if _, err := env.c.TSBatchAppend(ctx, purgeBatch); err != nil {
		t.Fatalf("TSBatchAppend (timeline 2): %v", err)
	}

	stats, err := env.c.TSStats(ctx)
	if err != nil {
		t.Fatalf("TSStats: %v", err)
	}
	if stats.Timelines != 2 {
		t.Fatalf("TSStats: want 2 timelines, got %+v", stats)
	}

	tlStats1, err := env.c.TSTimelineStats(ctx, 1)
	if err != nil {
		t.Fatalf("TSTimelineStats (1): %v", err)
	}
	if tlStats1.TotalEvents != 1 || tlStats1.OldestEvent == nil {
		t.Fatalf("TSTimelineStats (1, before delete): got %+v", tlStats1)
	}

	if err := env.c.TSDeleteTimelineData(ctx, 1); err != nil {
		t.Fatalf("TSDeleteTimelineData: %v", err)
	}
	tlStats1After, err := env.c.TSTimelineStats(ctx, 1)
	if err != nil {
		t.Fatalf("TSTimelineStats (1, after delete): %v", err)
	}
	if tlStats1After.TotalEvents != 0 {
		t.Fatalf("TSTimelineStats (1, after TSDeleteTimelineData): want 0 events, got %+v", tlStats1After)
	}
	if _, err := env.c.TSGetTimeline(ctx, 1); err != nil {
		t.Fatalf("TSGetTimeline (1, after TSDeleteTimelineData): want the definition to survive, got error: %v", err)
	}

	// Purge only the middle event's own range on timeline 2, keeping
	// the oldest and newest -- confirmed via the already-covered
	// TSQueryRange, not just trusting TSPurgeTimelineRange's own
	// success response.
	if err := env.c.TSPurgeTimelineRange(ctx, 2, now.Add(-150*time.Minute), now.Add(-90*time.Minute)); err != nil {
		t.Fatalf("TSPurgeTimelineRange: %v", err)
	}
	remaining, err := env.c.TSQueryRange(ctx, client.TSQueryRangeRequest{
		Timeline: 2, Dims: []uint64{7}, From: now.Add(-4 * time.Hour), To: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("TSQueryRange (after purge): %v", err)
	}
	if len(remaining.Events) != 2 {
		t.Fatalf("TSQueryRange (after purge): want 2 surviving events (oldest + newest), got %d: %+v",
			len(remaining.Events), remaining.Events)
	}
}

// TestIntegration_CalCreateCalendar_ClosesTheLoop is XM-8, end to
// end: xoluman's own report was specifically that CalListCalendars
// and CalListBookings both worked correctly but had nothing to list,
// since no route anywhere could create a calendar through the public
// API at all. This test uses no test-only facade whatsoever --
// CalManagerForTest() does not appear here -- every step, including
// calendar creation itself, goes through real HTTP, proving the loop
// XM-8 identified as broken is now actually closed: create a
// calendar, propose and confirm a real booking on it, then confirm
// both CalListCalendars and CalListBookings see it.
func TestIntegration_CalCreateCalendar_ClosesTheLoop(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	created, err := env.c.CalCreateCalendar(ctx, client.CalCreateCalendarRequest{CalendarID: "room-a"})
	if err != nil {
		t.Fatalf("CalCreateCalendar: %v", err)
	}
	if created.CalendarID != "room-a" || created.DefaultState != "binding" || created.MatchPolicy != "binding" {
		t.Errorf("unexpected created calendar: %+v", created)
	}

	cals, err := env.c.CalListCalendars(ctx)
	if err != nil {
		t.Fatalf("CalListCalendars: %v", err)
	}
	if len(cals.Calendars) != 1 || cals.Calendars[0].CalendarID != "room-a" {
		t.Fatalf("CalListCalendars: want exactly the created calendar, got %+v", cals.Calendars)
	}

	base := time.Now().UTC().Truncate(time.Hour).Add(24 * time.Hour)
	b, err := env.c.CalPropose(ctx, client.CalProposeRequest{
		BookingID: "b1", CalendarID: "room-a",
		Span:   client.CalSpan{Start: base.Add(time.Hour), End: base.Add(2 * time.Hour)},
		Bearer: 1,
	})
	if err != nil {
		t.Fatalf("CalPropose against a real, HTTP-created calendar: %v", err)
	}
	if _, err := env.c.CalConfirm(ctx, "room-a", b.BookingID); err != nil {
		t.Fatalf("CalConfirm: %v", err)
	}

	bookings, err := env.c.CalListBookings(ctx, "room-a", base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("CalListBookings: %v", err)
	}
	if len(bookings.Bookings) != 1 || bookings.Bookings[0].BookingID != "b1" {
		t.Fatalf("CalListBookings: want exactly the confirmed booking, got %+v", bookings.Bookings)
	}
}

func TestIntegration_CalCreateCalendar_AlreadyExists(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	if _, err := env.c.CalCreateCalendar(ctx, client.CalCreateCalendarRequest{CalendarID: "room-a"}); err != nil {
		t.Fatalf("first CalCreateCalendar: %v", err)
	}
	_, err := env.c.CalCreateCalendar(ctx, client.CalCreateCalendarRequest{CalendarID: "room-a"})
	if err == nil {
		t.Fatal("expected error creating a calendar_id that already exists, got nil")
	}
	ce, ok := err.(*client.Error)
	if !ok || ce.HTTPStatus != http.StatusConflict {
		t.Fatalf("expected a structured 409 *client.Error, got %T: %v", err, err)
	}
}

// TestIntegration_CalCreateCalendar_TenantIsolation: same
// calendar_id on two tenants, confirm each can independently create
// it (no false conflict across tenants) and neither tenant's own
// CalListCalendars result reflects the other's.
func TestIntegration_CalCreateCalendar_TenantIsolation(t *testing.T) {
	env := bootServerMultiTenant(t)
	ctx := context.Background()

	clientA := client.New(env.ts.URL, client.WithTenant("tenanta"))
	clientB := client.New(env.ts.URL, client.WithTenant("tenantb"))

	if _, err := clientA.CalCreateCalendar(ctx, client.CalCreateCalendarRequest{CalendarID: "room"}); err != nil {
		t.Fatalf("tenanta CalCreateCalendar: %v", err)
	}
	if _, err := clientB.CalCreateCalendar(ctx, client.CalCreateCalendarRequest{CalendarID: "room"}); err != nil {
		t.Fatalf("tenantb CalCreateCalendar (same calendar_id, different tenant -- must not conflict): %v", err)
	}

	resA, err := clientA.CalListCalendars(ctx)
	if err != nil {
		t.Fatalf("tenanta CalListCalendars: %v", err)
	}
	if len(resA.Calendars) != 1 {
		t.Fatalf("tenant isolation violated: tenanta wants exactly its own calendar, got %+v", resA.Calendars)
	}

	resB, err := clientB.CalListCalendars(ctx)
	if err != nil {
		t.Fatalf("tenantb CalListCalendars: %v", err)
	}
	if len(resB.Calendars) != 1 {
		t.Fatalf("tenant isolation violated: tenantb wants exactly its own calendar, got %+v", resB.Calendars)
	}
}

// TestIntegration_CalListCalendars proves CalListCalendars against a
// real server -- the storage capability (Calendars()) already
// existed and was already tenant-scoped, but was confirmed
// unreachable via any HTTP route or client method during the XOT180
// audit (2026-08-11), not just undocumented.
func TestIntegration_CalListCalendars(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	mgr := env.srv.CalManagerForTest()
	if _, err := mgr.CreateCalendar(0, cal.Calendar{
		CalendarID: "room-a", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("seed room-a: %v", err)
	}
	if _, err := mgr.CreateCalendar(0, cal.Calendar{
		CalendarID: "room-b", DefaultState: cal.StateProposed, MatchPolicy: cal.ConsiderBindingProposed,
	}); err != nil {
		t.Fatalf("seed room-b: %v", err)
	}

	res, err := env.c.CalListCalendars(ctx)
	if err != nil {
		t.Fatalf("CalListCalendars: %v", err)
	}
	if len(res.Calendars) != 2 {
		t.Fatalf("want 2 calendars, got %d: %+v", len(res.Calendars), res.Calendars)
	}
	byID := make(map[string]client.CalendarSummary, len(res.Calendars))
	for _, c := range res.Calendars {
		byID[c.CalendarID] = c
	}
	if roomA, ok := byID["room-a"]; !ok || roomA.DefaultState != "binding" {
		t.Errorf("room-a: want default_state=binding, got %+v", byID["room-a"])
	}
	if roomB, ok := byID["room-b"]; !ok || roomB.DefaultState != "proposed" {
		t.Errorf("room-b: want default_state=proposed, got %+v", byID["room-b"])
	}
}

// TestIntegration_CalListCalendars_TenantIsolation: two tenants, each
// with a calendar of a different name, confirm neither tenant's own
// CalListCalendars result reflects the other's. Included from the
// start rather than added after the fact, matching XOT180's own
// finding that this exact check was initially missing for
// CalListBookings.
func TestIntegration_CalListCalendars_TenantIsolation(t *testing.T) {
	env := bootServerMultiTenant(t)
	ctx := context.Background()

	clientA := client.New(env.ts.URL, client.WithTenant("tenanta"))
	clientB := client.New(env.ts.URL, client.WithTenant("tenantb"))

	// Trigger tenant auto-registration via a request that's allowed to
	// fail (no calendars seeded yet), matching the established pattern.
	if _, err := clientA.CalListCalendars(ctx); err != nil {
		t.Fatalf("tenanta CalListCalendars (pre-seed): %v", err)
	}
	if _, err := clientB.CalListCalendars(ctx); err != nil {
		t.Fatalf("tenantb CalListCalendars (pre-seed): %v", err)
	}
	tidA, ok := env.srv.TenantIDForTest("tenanta")
	if !ok {
		t.Fatal("tenanta not found in registry after a request against it")
	}
	tidB, ok := env.srv.TenantIDForTest("tenantb")
	if !ok {
		t.Fatal("tenantb not found in registry after a request against it")
	}

	mgr := env.srv.CalManagerForTest()
	if _, err := mgr.CreateCalendar(tidA, cal.Calendar{
		CalendarID: "tenanta-room", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("seed tenanta's own calendar: %v", err)
	}
	if _, err := mgr.CreateCalendar(tidB, cal.Calendar{
		CalendarID: "tenantb-room", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("seed tenantb's own calendar: %v", err)
	}

	resA, err := clientA.CalListCalendars(ctx)
	if err != nil {
		t.Fatalf("tenanta CalListCalendars: %v", err)
	}
	if len(resA.Calendars) != 1 || resA.Calendars[0].CalendarID != "tenanta-room" {
		t.Fatalf("tenant isolation violated: tenanta wants exactly its own calendar, got %+v", resA.Calendars)
	}

	resB, err := clientB.CalListCalendars(ctx)
	if err != nil {
		t.Fatalf("tenantb CalListCalendars: %v", err)
	}
	if len(resB.Calendars) != 1 || resB.Calendars[0].CalendarID != "tenantb-room" {
		t.Fatalf("tenant isolation violated: tenantb wants exactly its own calendar, got %+v", resB.Calendars)
	}
}

// TestIntegration_CalListBookings proves CalListBookings against a
// real server: seed two live bookings (one wholly inside the query
// window, one straddling its own start) and one on a different
// calendar, confirm the range query returns exactly the two on the
// right calendar and none of the other.
func TestIntegration_CalListBookings(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	mgr := env.srv.CalManagerForTest()
	for _, id := range []string{"room-a", "room-b"} {
		if _, err := mgr.CreateCalendar(0, cal.Calendar{
			CalendarID:   id,
			DefaultState: cal.StateBinding,
			MatchPolicy:  cal.ConsiderBinding,
		}); err != nil {
			t.Fatalf("seed calendar %s: %v", id, err)
		}
	}

	base := time.Now().UTC().Truncate(time.Hour).Add(24 * time.Hour)
	seedBooking := func(id, calendarID string, startOffset, endOffset time.Duration) {
		t.Helper()
		_, err := env.c.CalPropose(ctx, client.CalProposeRequest{
			BookingID:  id,
			CalendarID: calendarID,
			Span:       client.CalSpan{Start: base.Add(startOffset), End: base.Add(endOffset)},
			Bearer:     1,
		})
		if err != nil {
			t.Fatalf("seed booking %s: %v", id, err)
		}
		if _, err := env.c.CalConfirm(ctx, calendarID, id); err != nil {
			t.Fatalf("confirm booking %s: %v", id, err)
		}
	}

	// Query window: [base+2h, base+6h).
	seedBooking("inside", "room-a", 3*time.Hour, 4*time.Hour)
	seedBooking("straddles-start", "room-a", 1*time.Hour, 3*time.Hour)
	seedBooking("before-window", "room-a", -3*time.Hour, -2*time.Hour)
	seedBooking("other-calendar", "room-b", 3*time.Hour, 4*time.Hour)

	res, err := env.c.CalListBookings(ctx, "room-a", base.Add(2*time.Hour), base.Add(6*time.Hour))
	if err != nil {
		t.Fatalf("CalListBookings: %v", err)
	}
	got := make(map[string]bool, len(res.Bookings))
	for _, b := range res.Bookings {
		got[b.BookingID] = true
		if b.State != "binding" {
			t.Errorf("booking %s: want state=binding, got %s", b.BookingID, b.State)
		}
	}
	for _, id := range []string{"inside", "straddles-start"} {
		if !got[id] {
			t.Errorf("expected %q in result, got %v", id, got)
		}
	}
	for _, id := range []string{"before-window", "other-calendar"} {
		if got[id] {
			t.Errorf("did not expect %q in result, got %v", id, got)
		}
	}
	if len(res.Bookings) != 2 {
		t.Errorf("want exactly 2 bookings, got %d: %v", len(res.Bookings), got)
	}
}

// TestIntegration_CalListBookingsForBearer proves the cross-calendar
// bearer query (XOT180, 2026-08-11) against a real server: bearer 100
// holds bookings on two different calendars, confirm both come back
// in one call and a second bearer's own booking does not.
func TestIntegration_CalListBookingsForBearer(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	mgr := env.srv.CalManagerForTest()
	for _, id := range []string{"room-a", "room-b"} {
		if _, err := mgr.CreateCalendar(0, cal.Calendar{
			CalendarID: id, DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
		}); err != nil {
			t.Fatalf("seed calendar %s: %v", id, err)
		}
	}

	base := time.Now().UTC().Truncate(time.Hour).Add(24 * time.Hour)
	seedBooking := func(bookingID, calendarID string, bearer uint64) {
		t.Helper()
		b, err := env.c.CalPropose(ctx, client.CalProposeRequest{
			BookingID: bookingID, CalendarID: calendarID,
			Span:   client.CalSpan{Start: base.Add(time.Hour), End: base.Add(2 * time.Hour)},
			Bearer: bearer,
		})
		if err != nil {
			t.Fatalf("seed booking %s: %v", bookingID, err)
		}
		if _, err := env.c.CalConfirm(ctx, calendarID, b.BookingID); err != nil {
			t.Fatalf("confirm booking %s: %v", bookingID, err)
		}
	}
	seedBooking("bearer100-a", "room-a", 100)
	seedBooking("bearer100-b", "room-b", 100)
	seedBooking("bearer200-a", "room-a", 200)

	res, err := env.c.CalListBookingsForBearer(ctx, 100)
	if err != nil {
		t.Fatalf("CalListBookingsForBearer: %v", err)
	}
	got := make(map[string]bool, len(res.Bookings))
	for _, b := range res.Bookings {
		got[b.BookingID] = true
	}
	for _, id := range []string{"bearer100-a", "bearer100-b"} {
		if !got[id] {
			t.Errorf("expected %q in bearer 100's own result, got %v", id, got)
		}
	}
	if got["bearer200-a"] {
		t.Errorf("did not expect bearer 200's own booking in bearer 100's own result, got %v", got)
	}
	if len(res.Bookings) != 2 {
		t.Errorf("want exactly 2 bookings, got %d: %v", len(res.Bookings), got)
	}
}

// TestIntegration_CalListBookingsForBearer_TenantIsolation: same
// bearer id on two tenants, confirm neither tenant's own result
// reflects the other's booking. Included from the start, matching
// XOT180's own general discipline.
func TestIntegration_CalListBookingsForBearer_TenantIsolation(t *testing.T) {
	env := bootServerMultiTenant(t)
	ctx := context.Background()

	clientA := client.New(env.ts.URL, client.WithTenant("tenanta"))
	clientB := client.New(env.ts.URL, client.WithTenant("tenantb"))

	// Trigger tenant auto-registration (middleware-level, independent
	// of the handler's own response) -- unlike CalListBookings, this
	// endpoint has no single calendarID to validate, so an empty
	// result is a genuine 200, not an error to expect.
	if _, err := clientA.CalListBookingsForBearer(ctx, 100); err != nil {
		t.Fatalf("tenanta CalListBookingsForBearer (pre-seed): %v", err)
	}
	if _, err := clientB.CalListBookingsForBearer(ctx, 100); err != nil {
		t.Fatalf("tenantb CalListBookingsForBearer (pre-seed): %v", err)
	}
	tidA, ok := env.srv.TenantIDForTest("tenanta")
	if !ok {
		t.Fatal("tenanta not found in registry after a request against it")
	}
	tidB, ok := env.srv.TenantIDForTest("tenantb")
	if !ok {
		t.Fatal("tenantb not found in registry after a request against it")
	}

	mgr := env.srv.CalManagerForTest()
	if _, err := mgr.CreateCalendar(tidA, cal.Calendar{
		CalendarID: "room", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("seed tenanta's own room: %v", err)
	}
	if _, err := mgr.CreateCalendar(tidB, cal.Calendar{
		CalendarID: "room", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("seed tenantb's own room: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Hour).Add(24 * time.Hour)
	seed := func(c *client.Client, bookingID string) {
		t.Helper()
		b, err := c.CalPropose(ctx, client.CalProposeRequest{
			BookingID: bookingID, CalendarID: "room",
			Span:   client.CalSpan{Start: base.Add(time.Hour), End: base.Add(2 * time.Hour)},
			Bearer: 100,
		})
		if err != nil {
			t.Fatalf("seed booking %s: %v", bookingID, err)
		}
		if _, err := c.CalConfirm(ctx, "room", b.BookingID); err != nil {
			t.Fatalf("confirm booking %s: %v", bookingID, err)
		}
	}
	seed(clientA, "a-booking")
	seed(clientB, "b-booking")

	resA, err := clientA.CalListBookingsForBearer(ctx, 100)
	if err != nil {
		t.Fatalf("tenanta CalListBookingsForBearer: %v", err)
	}
	if len(resA.Bookings) != 1 || resA.Bookings[0].BookingID != "a-booking" {
		t.Fatalf("tenant isolation violated: tenanta wants exactly its own booking, got %+v", resA.Bookings)
	}

	resB, err := clientB.CalListBookingsForBearer(ctx, 100)
	if err != nil {
		t.Fatalf("tenantb CalListBookingsForBearer: %v", err)
	}
	if len(resB.Bookings) != 1 || resB.Bookings[0].BookingID != "b-booking" {
		t.Fatalf("tenant isolation violated: tenantb wants exactly its own booking, got %+v", resB.Bookings)
	}
}

// TestIntegration_CalListBookings_TenantIsolation is the check
// CalListBookings itself was missing until this test was added --
// found directly while explaining why XOT173/XM-4/XM-2 slipped past
// this project's own existing test suite in the first place: the
// JOIN fix earlier in this session (XOT173) got exactly this class
// of test (TestXM3a_JoinQuery_TenantIsolation), CalListBookings did
// not, despite being new storage-layer code with tenant_id in its own
// WHERE clause -- the same shape of risk, checked for one feature and
// not the other in the same sitting. Two tenants, same calendar name,
// distinct bookings, confirm neither tenant's own CalListBookings
// result contains the other's.
func TestIntegration_CalListBookings_TenantIsolation(t *testing.T) {
	env := bootServerMultiTenant(t)
	ctx := context.Background()

	clientA := client.New(env.ts.URL, client.WithTenant("tenanta"))
	clientB := client.New(env.ts.URL, client.WithTenant("tenantb"))

	// Calendar creation is deliberately not on the wire (see this
	// file's own header comment) -- seed via the same test facade
	// TestIntegration_CalFullFlow uses. Tenant auto-registration is
	// lazy (first HTTP request naming that tenant), so trigger it with
	// a request that's allowed to fail (calendar doesn't exist yet)
	// before looking the numeric ID up.
	if _, err := clientA.CalListBookings(ctx, "room-a", time.Now(), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected error (calendar not yet seeded) triggering tenanta's own registration")
	}
	if _, err := clientB.CalListBookings(ctx, "room-a", time.Now(), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected error (calendar not yet seeded) triggering tenantb's own registration")
	}
	tidA, ok := env.srv.TenantIDForTest("tenanta")
	if !ok {
		t.Fatal("tenanta not found in registry after a request against it")
	}
	tidB, ok := env.srv.TenantIDForTest("tenantb")
	if !ok {
		t.Fatal("tenantb not found in registry after a request against it")
	}

	mgr := env.srv.CalManagerForTest()
	if _, err := mgr.CreateCalendar(tidA, cal.Calendar{
		CalendarID: "room-a", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("seed tenanta's own room-a: %v", err)
	}
	if _, err := mgr.CreateCalendar(tidB, cal.Calendar{
		CalendarID: "room-a", DefaultState: cal.StateBinding, MatchPolicy: cal.ConsiderBinding,
	}); err != nil {
		t.Fatalf("seed tenantb's own room-a: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Hour).Add(24 * time.Hour)
	seedBooking := func(c *client.Client, bookingID string) {
		t.Helper()
		b, err := c.CalPropose(ctx, client.CalProposeRequest{
			BookingID: bookingID, CalendarID: "room-a",
			Span:   client.CalSpan{Start: base.Add(time.Hour), End: base.Add(2 * time.Hour)},
			Bearer: 1,
		})
		if err != nil {
			t.Fatalf("seed booking %s: %v", bookingID, err)
		}
		if _, err := c.CalConfirm(ctx, "room-a", b.BookingID); err != nil {
			t.Fatalf("confirm booking %s: %v", bookingID, err)
		}
	}
	seedBooking(clientA, "a-booking")
	seedBooking(clientB, "b-booking")

	resA, err := clientA.CalListBookings(ctx, "room-a", base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("tenanta CalListBookings: %v", err)
	}
	for _, b := range resA.Bookings {
		if b.BookingID == "b-booking" {
			t.Fatalf("tenant isolation violated: tenanta's CalListBookings result contains tenantb's booking: %+v", b)
		}
	}
	if len(resA.Bookings) != 1 || resA.Bookings[0].BookingID != "a-booking" {
		t.Fatalf("tenanta: want exactly its own booking, got %+v", resA.Bookings)
	}

	resB, err := clientB.CalListBookings(ctx, "room-a", base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("tenantb CalListBookings: %v", err)
	}
	for _, b := range resB.Bookings {
		if b.BookingID == "a-booking" {
			t.Fatalf("tenant isolation violated: tenantb's CalListBookings result contains tenanta's booking: %+v", b)
		}
	}
	if len(resB.Bookings) != 1 || resB.Bookings[0].BookingID != "b-booking" {
		t.Fatalf("tenantb: want exactly its own booking, got %+v", resB.Bookings)
	}
}

// ─── bal ─────────────────────────────────────────────────────────────────

// TestIntegration_BalFullFlow exercises the whole client surface
// against a real server: define two accounts, transfer between them,
// read the authoritative balance and the derived as-of balance and
// confirm they agree, read entries, seal a period, and confirm the
// seal actually refuses a backdated entry over real HTTP — not just
// that the endpoints return 2xx.
func TestIntegration_BalFullFlow(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "~in", Unit: "u", Scale: 0, Floor: strPtr("-1000000"),
	}); err != nil {
		t.Fatalf("BalDefine ~in: %v", err)
	}
	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "acct", Unit: "u", Scale: 0,
	}); err != nil {
		t.Fatalf("BalDefine acct: %v", err)
	}

	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	tr, err := env.c.BalTransfer(ctx, client.BalTransferRequest{
		From: "~in", To: "acct", Amount: "150", Scale: 0, At: at.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("BalTransfer: %v", err)
	}
	if tr.Amount != "150" {
		t.Errorf("transfer amount echo: got %s", tr.Amount)
	}

	bal, err := env.c.BalBalance(ctx, "acct")
	if err != nil {
		t.Fatalf("BalBalance: %v", err)
	}
	if bal.Value != "150" || bal.Minor != 150 {
		t.Errorf("balance: got value=%s minor=%d, want 150/150", bal.Value, bal.Minor)
	}

	asof, err := env.c.BalAsOf(ctx, "acct", at.Add(time.Hour))
	if err != nil {
		t.Fatalf("BalAsOf: %v", err)
	}
	if asof.Value != bal.Value {
		t.Errorf("as-of (derived) disagrees with authoritative balance: %s vs %s", asof.Value, bal.Value)
	}
	if asof.Source != "rollup" {
		t.Errorf("as-of source: got %s, want rollup", asof.Source)
	}

	entries, err := env.c.BalEntries(ctx, "acct")
	if err != nil {
		t.Fatalf("BalEntries: %v", err)
	}
	if len(entries.Entries) != 1 || entries.Entries[0].Amount != "150" {
		t.Fatalf("entries: got %+v, want one 150 entry", entries.Entries)
	}

	// Seal through the end of June — over the wire, tenant-wide.
	closed, err := env.c.BalClose(ctx, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BalClose: %v", err)
	}
	if closed.AccountsClosed != 2 {
		t.Errorf("accounts closed: got %d, want 2", closed.AccountsClosed)
	}

	// A backdated entry into the now-sealed period must be refused
	// with the structured XOLU-BAL003 error, over real HTTP — proving
	// the seal actually enforces, not just that /bal/close returns 2xx.
	_, err = env.c.BalTransfer(ctx, client.BalTransferRequest{
		From: "~in", To: "acct", Amount: "10", Scale: 0, At: "2026-06-20T12:00:00Z",
	})
	var xerr *client.Error
	if !errors.As(err, &xerr) {
		t.Fatalf("transfer into sealed period: expected *client.Error, got %v", err)
	}
	if xerr.Code != "XOLU-BAL003" {
		t.Errorf("transfer into sealed period: want XOLU-BAL003, got %s", xerr.Code)
	}
}

func strPtr(s string) *string { return &s }

// TestIntegration_BalListAccounts exercises BalListAccounts (XM-2,
// XOT172) against a real server: define three accounts with distinct
// definitions (postable/non-postable, with/without a ceiling), move a
// real transfer between two of them, confirm the listing reflects
// both the correct definitions and the post-transfer balances.
func TestIntegration_BalListAccounts(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	ceiling := "1000"
	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "~in", Unit: "u", Scale: 0, Floor: strPtr("-1000000"),
	}); err != nil {
		t.Fatalf("BalDefine ~in: %v", err)
	}
	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "acct", Unit: "u", Scale: 0, Ceiling: &ceiling,
	}); err != nil {
		t.Fatalf("BalDefine acct: %v", err)
	}
	notPostable := false
	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "summary", Unit: "u", Scale: 0, Postable: &notPostable,
	}); err != nil {
		t.Fatalf("BalDefine summary: %v", err)
	}

	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if _, err := env.c.BalTransfer(ctx, client.BalTransferRequest{
		From: "~in", To: "acct", Amount: "150", Scale: 0, At: at.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("BalTransfer: %v", err)
	}

	res, err := env.c.BalListAccounts(ctx)
	if err != nil {
		t.Fatalf("BalListAccounts: %v", err)
	}
	if len(res.Accounts) != 3 {
		t.Fatalf("want 3 accounts, got %d: %+v", len(res.Accounts), res.Accounts)
	}

	byID := make(map[string]client.BalAccountSummary, len(res.Accounts))
	for _, a := range res.Accounts {
		byID[a.AccountID] = a
	}

	acct, ok := byID["acct"]
	if !ok {
		t.Fatal("acct missing from listing")
	}
	if acct.Value != "150" || acct.Minor != 150 {
		t.Errorf("acct balance after receiving 150: want 150, got %+v", acct)
	}
	if acct.Ceiling != "1000" {
		t.Errorf("acct ceiling: want 1000, got %q", acct.Ceiling)
	}
	if !acct.Postable {
		t.Error("acct defined postable, listing reports it not postable")
	}

	in, ok := byID["~in"]
	if !ok {
		t.Fatal("~in missing from listing")
	}
	if in.Value != "-150" {
		t.Errorf("~in balance after sending 150: want -150, got %+v", in)
	}

	summary, ok := byID["summary"]
	if !ok {
		t.Fatal("summary missing from listing")
	}
	if summary.Postable {
		t.Error("summary defined non-postable, listing reports it postable")
	}
}

// TestIntegration_BalListAccounts_TenantIsolation is the same check
// XOT180 flagged as missing on the cal side, applied here from the
// start rather than added after the fact: two tenants, an account
// with the identical id in both, distinct balances, confirm neither
// tenant's own BalListAccounts result reflects the other's data. Bal
// isolates by table-name prefixing (tenant.TenantID.TablePrefix()),
// a structurally different mechanism from cal's shared-table-plus-
// tenant_id-column approach -- proven directly here rather than
// inferred from the storage-layer unit test alone
// (TestListAccounts_TenantIsolation in pkg/bal), since that test
// exercises the *Store type directly and this one proves the same
// property holds through the real HTTP + tenant-resolution path a
// caller actually uses.
func TestIntegration_BalListAccounts_TenantIsolation(t *testing.T) {
	env := bootServerMultiTenant(t)
	ctx := context.Background()

	clientA := client.New(env.ts.URL, client.WithTenant("tenanta"))
	clientB := client.New(env.ts.URL, client.WithTenant("tenantb"))

	if _, err := clientA.BalDefine(ctx, client.BalDefineRequest{AccountID: "shared-id", Unit: "EUR", Scale: 2}); err != nil {
		t.Fatalf("tenanta BalDefine: %v", err)
	}
	if _, err := clientA.BalDefine(ctx, client.BalDefineRequest{AccountID: "~in-a", Unit: "EUR", Scale: 2, Floor: strPtr("-100000")}); err != nil {
		t.Fatalf("tenanta BalDefine ~in-a: %v", err)
	}
	if _, err := clientB.BalDefine(ctx, client.BalDefineRequest{AccountID: "shared-id", Unit: "USD", Scale: 2}); err != nil {
		t.Fatalf("tenantb BalDefine: %v", err)
	}

	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if _, err := clientA.BalTransfer(ctx, client.BalTransferRequest{
		From: "~in-a", To: "shared-id", Amount: "500", Scale: 2, At: at.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("tenanta BalTransfer: %v", err)
	}

	resA, err := clientA.BalListAccounts(ctx)
	if err != nil {
		t.Fatalf("tenanta BalListAccounts: %v", err)
	}
	if len(resA.Accounts) != 2 {
		t.Fatalf("tenant isolation violated: tenanta wants exactly its own 2 accounts, got %d: %+v", len(resA.Accounts), resA.Accounts)
	}
	for _, a := range resA.Accounts {
		if a.AccountID == "shared-id" {
			if a.Unit != "EUR" {
				t.Fatalf("tenant isolation violated: tenanta's own shared-id shows tenantb's currency: %+v", a)
			}
			if a.Value != "500.00" {
				t.Errorf("tenanta's own shared-id balance: want 500.00, got %+v", a)
			}
		}
	}

	resB, err := clientB.BalListAccounts(ctx)
	if err != nil {
		t.Fatalf("tenantb BalListAccounts: %v", err)
	}
	if len(resB.Accounts) != 1 {
		t.Fatalf("tenant isolation violated: tenantb wants exactly its own 1 account, got %d: %+v", len(resB.Accounts), resB.Accounts)
	}
	if resB.Accounts[0].Unit != "USD" {
		t.Errorf("tenantb's own shared-id shows tenanta's currency: %+v", resB.Accounts[0])
	}
	if resB.Accounts[0].Value != "0.00" {
		t.Errorf("tenant isolation violated: tenantb's own shared-id balance should be untouched by tenanta's transfer, got %+v", resB.Accounts[0])
	}
}

func float64Ptr(f float64) *float64 { return &f }
func int64Ptr(i int64) *int64       { return &i }

// TestIntegration_ObjFullFlow exercises the core obj client surface
// (XOT210, wave 15) against a real server: attach two subjects, list
// (confirming the enumeration this primitive never had before XOT209
// actually returns them), move one to a real /loc leaf and the other
// into containment inside the first, report a raw position, resolve
// position through the containment chain, patch capacity, read
// contents both directly and transitively, retire, and detach --
// every one of these went through a real request/response round trip
// against a real server, not a mock that could share this file's own
// wrong assumptions (see this suite's own header comment on why mocks
// cannot catch wire-shape drift).
func TestIntegration_ObjFullFlow(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	notPostableLoc := false
	postableLoc := true
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: "root", Name: "root", Postable: &notPostableLoc,
		Placement: client.LocPlacement{Anchor: &client.LocAnchor{Lat: -34.9, Lon: -56.16, Alt: 10}},
	}); err != nil {
		t.Fatalf("LocDefine root: %v", err)
	}
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: "root/shelf", ParentID: strPtr("root"), Name: "shelf", Postable: &postableLoc,
		Capacity: int64Ptr(500),
	}); err != nil {
		t.Fatalf("LocDefine root/shelf: %v", err)
	}

	veh, err := env.c.ObjAttach(ctx, client.ObjAttachRequest{
		Subject:  "vehicles:1",
		Capacity: client.ObjCapacity{MaxWeightKg: float64Ptr(12000), MaxVolumeM3: float64Ptr(40)},
	})
	if err != nil {
		t.Fatalf("ObjAttach vehicles:1: %v", err)
	}
	if veh.Subject != "vehicles:1" || veh.PositionKind != "" {
		t.Errorf("freshly-attached vehicles:1: got %+v", veh)
	}
	if _, err := env.c.ObjAttach(ctx, client.ObjAttachRequest{Subject: "pallets:9"}); err != nil {
		t.Fatalf("ObjAttach pallets:9: %v", err)
	}

	listed, err := env.c.ObjList(ctx)
	if err != nil {
		t.Fatalf("ObjList: %v", err)
	}
	if len(listed.Subjects) != 2 {
		t.Fatalf("ObjList: want 2 subjects, got %d: %+v", len(listed.Subjects), listed.Subjects)
	}

	moved, err := env.c.ObjMove(ctx, "vehicles:1", client.ObjMoveTarget{Kind: "loc_leaf", LocationID: "root/shelf"})
	if err != nil {
		t.Fatalf("ObjMove vehicles:1 to loc_leaf: %v", err)
	}
	if moved.PositionKind != "loc_leaf" || moved.LocLeafID == nil || *moved.LocLeafID != "root/shelf" {
		t.Errorf("moved vehicles:1: got %+v", moved)
	}

	contained, err := env.c.ObjMove(ctx, "pallets:9", client.ObjMoveTarget{Kind: "obj", Subject: "vehicles:1"})
	if err != nil {
		t.Fatalf("ObjMove pallets:9 into vehicles:1: %v", err)
	}
	if contained.PositionKind != "obj" {
		t.Errorf("contained pallets:9: want position_kind=obj, got %+v", contained)
	}

	if err := env.c.ObjReport(ctx, "vehicles:1", -34.9, -56.16, 12); err != nil {
		t.Fatalf("ObjReport: %v", err)
	}

	pos, err := env.c.ObjPosition(ctx, "pallets:9")
	if err != nil {
		t.Fatalf("ObjPosition pallets:9: %v", err)
	}
	if pos.Resolved.Kind != "loc_leaf" {
		t.Errorf("pallets:9 resolved through containment: want loc_leaf, got %+v", pos.Resolved)
	}
	if len(pos.Chain) == 0 {
		t.Errorf("pallets:9 resolution chain should be non-empty (contained -> vehicles:1 -> loc_leaf), got %v", pos.Chain)
	}

	patched, err := env.c.ObjCapacityPatch(ctx, "vehicles:1", client.ObjCapacity{MaxWeightKg: float64Ptr(15000)})
	if err != nil {
		t.Fatalf("ObjCapacityPatch: %v", err)
	}
	if patched.Capacity.MaxWeightKg == nil || *patched.Capacity.MaxWeightKg != 15000 {
		t.Errorf("capacity patch not applied: got %+v", patched.Capacity)
	}

	direct, err := env.c.ObjContents(ctx, "vehicles:1", false)
	if err != nil {
		t.Fatalf("ObjContents (direct): %v", err)
	}
	if len(direct.Contents) != 1 || direct.Contents[0] != "pallets:9" {
		t.Errorf("direct contents: want [pallets:9], got %v", direct.Contents)
	}
	transitive, err := env.c.ObjContents(ctx, "vehicles:1", true)
	if err != nil {
		t.Fatalf("ObjContents (transitive): %v", err)
	}
	if len(transitive.Contents) != 1 {
		t.Errorf("transitive contents: want 1, got %v", transitive.Contents)
	}

	if err := env.c.ObjRetire(ctx, "pallets:9"); err != nil {
		t.Fatalf("ObjRetire pallets:9: %v", err)
	}
	retired, err := env.c.ObjGet(ctx, "pallets:9")
	if err != nil {
		t.Fatalf("ObjGet after retire: %v", err)
	}
	if retired.Subject != "pallets:9" {
		t.Errorf("retired subject should still be gettable, got %+v", retired)
	}
}

// TestIntegration_ObjPromoteDemote exercises the promote/demote pair
// (XOT210) -- the one part of the obj client surface that is not
// plain CRUD: an atomic dxp-orchestrated operation pairing obj
// capability with a real bal transfer, both directions.
func TestIntegration_ObjPromoteDemote(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "~in", Unit: "u", Scale: 0, Floor: strPtr("-1000000"),
	}); err != nil {
		t.Fatalf("BalDefine ~in: %v", err)
	}
	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "promo-acct", Unit: "u", Scale: 0,
	}); err != nil {
		t.Fatalf("BalDefine promo-acct: %v", err)
	}
	if _, err := env.c.ObjAttach(ctx, client.ObjAttachRequest{Subject: "containers:1"}); err != nil {
		t.Fatalf("ObjAttach containers:1 (the promotion's own container): %v", err)
	}

	promoted, err := env.c.ObjPromote(ctx, client.ObjPromoteRequest{
		BalAccount: "~in", ToAccount: "promo-acct", Amount: "500",
		Entity:   client.ObjPromoteEntity{Kind: "widget", Create: map[string]interface{}{"name": "promoted-widget"}},
		Position: client.ObjPromotePosition{Kind: "obj", Subject: "containers:1"},
	})
	if err != nil {
		t.Fatalf("ObjPromote: %v", err)
	}
	if promoted.TxnID == 0 {
		t.Errorf("ObjPromote: want a real txn id, got zero")
	}
	if promoted.Status != "committed" {
		t.Errorf("ObjPromote: want status=committed, got %+v", promoted)
	}

	afterPromote, err := env.c.ObjGet(ctx, promoted.Subject)
	if err != nil {
		t.Fatalf("ObjGet after promote (%s): %v", promoted.Subject, err)
	}
	if afterPromote.PositionKind != "obj" {
		t.Errorf("promoted subject should be contained in containers:1, got %+v", afterPromote)
	}

	demoted, err := env.c.ObjDemote(ctx, client.ObjDemoteRequest{
		Subject: promoted.Subject, BalAccount: "~in", FromAccount: "promo-acct", Amount: "500",
	})
	if err != nil {
		t.Fatalf("ObjDemote: %v", err)
	}
	if demoted.TxnID == 0 {
		t.Errorf("ObjDemote: want a real txn id, got zero")
	}
}

// TestIntegration_TenantSummary_EmptyTenant confirms a genuinely
// empty tenant summarizes to Empty=true and every count at zero
// (XOT213, wave 15) -- the exact case xoluman's own tenant-emptiness
// safety check needs to be able to trust in one round trip.
func TestIntegration_TenantSummary_EmptyTenant(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	summary, err := env.c.TenantSummary(ctx)
	if err != nil {
		t.Fatalf("TenantSummary: %v", err)
	}
	if !summary.Empty {
		t.Errorf("Empty: want true for a genuinely empty tenant, got false: %+v", summary)
	}
	if summary.TS != 0 || summary.CalIndex != 0 || summary.BalRollup != 0 {
		t.Errorf("pebble counts: want all zero, got ts=%d cal=%d bal=%d",
			summary.TS, summary.CalIndex, summary.BalRollup)
	}
}

// TestIntegration_TenantSummary_ReflectsRealData exercises the
// closing of XOT209's own emptiness-check gap directly -- an obj
// subject attached through the already-verified typed client (XOT210)
// should be visible in the summary's own Obj counts, and a loc
// location (XOT211) in its own Loc counts, without either of those
// items needing their own separate enumeration call to prove it.
func TestIntegration_TenantSummary_ReflectsRealData(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	before, err := env.c.TenantSummary(ctx)
	if err != nil {
		t.Fatalf("TenantSummary (before): %v", err)
	}
	if !before.Empty {
		t.Fatalf("TenantSummary (before): want a fresh tenant to be empty, got %+v", before)
	}

	if _, err := env.c.ObjAttach(ctx, client.ObjAttachRequest{Subject: "vehicles:1"}); err != nil {
		t.Fatalf("ObjAttach: %v", err)
	}
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: "root", Name: "root",
		Placement: client.LocPlacement{Anchor: &client.LocAnchor{Lat: -34.9, Lon: -56.16}},
	}); err != nil {
		t.Fatalf("LocDefine: %v", err)
	}

	after, err := env.c.TenantSummary(ctx)
	if err != nil {
		t.Fatalf("TenantSummary (after): %v", err)
	}
	if after.Empty {
		t.Fatalf("TenantSummary (after): want false, real data now exists, got true: %+v", after)
	}
	if after.Obj["obj_subjects"] == 0 {
		t.Errorf("TenantSummary (after): want obj_subjects > 0 after ObjAttach, got %+v", after.Obj)
	}
	if after.Loc["locations"] == 0 {
		t.Errorf("TenantSummary (after): want locations > 0 after LocDefine, got %+v", after.Loc)
	}
}

// TestIntegration_TenantSummary_BlobOnlyTenant is XOT216's own real
// proof through the full stack, not just the library level (see
// TestSummarize_BlobOnlyTenant_NotReportedEmpty in pkg/tenantexport
// for that layer): a fresh tenant that uploads a real file via the
// already-existing BlobPut and touches nothing else must now report
// Empty=false, not the wrong Empty=true this item exists to fix.
func TestIntegration_TenantSummary_BlobOnlyTenant(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	before, err := env.c.TenantSummary(ctx)
	if err != nil {
		t.Fatalf("TenantSummary (before): %v", err)
	}
	if !before.Empty || before.Blob != 0 {
		t.Fatalf("TenantSummary (before): want a fresh tenant empty with Blob=0, got %+v", before)
	}

	if _, err := env.c.BlobPut(ctx, "seed-file.txt", "text/plain", strings.NewReader("real tenant data")); err != nil {
		t.Fatalf("BlobPut: %v", err)
	}

	after, err := env.c.TenantSummary(ctx)
	if err != nil {
		t.Fatalf("TenantSummary (after): %v", err)
	}
	if after.Blob != 1 {
		t.Errorf("TenantSummary (after): want Blob=1, got %d", after.Blob)
	}
	if after.Empty {
		t.Fatalf("TenantSummary (after): want Empty=false -- this tenant has real blob data -- got true: %+v", after)
	}
}

// TestIntegration_LocCoreFlow exercises the loc client methods not
// already incidentally covered by TestIntegration_ObjFullFlow's own
// LocDefine setup calls (XOT211, wave 15) -- list, get, patch
// (including the tri-state capacity semantics LocPatchRequest's own
// doc comment describes: set a new ceiling, then clear it entirely),
// move, report, and delete. Fences are deliberately out of scope
// here, matching this item's own split-out follow-up.
func TestIntegration_LocCoreFlow(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	notPostable := false
	postable := true
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: "warehouse", Name: "Warehouse", Postable: &notPostable,
		Placement: client.LocPlacement{Anchor: &client.LocAnchor{Lat: -34.9, Lon: -56.16, Alt: 5}},
	}); err != nil {
		t.Fatalf("LocDefine warehouse: %v", err)
	}
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: "warehouse/bay1", ParentID: strPtr("warehouse"), Name: "Bay 1", Postable: &postable,
		Capacity: int64Ptr(10),
	}); err != nil {
		t.Fatalf("LocDefine warehouse/bay1: %v", err)
	}

	listAll, err := env.c.LocList(ctx, "")
	if err != nil {
		t.Fatalf("LocList (unfiltered): %v", err)
	}
	if len(listAll.Locations) != 2 {
		t.Fatalf("LocList (unfiltered): want 2, got %d: %+v", len(listAll.Locations), listAll.Locations)
	}
	listChildren, err := env.c.LocList(ctx, "warehouse")
	if err != nil {
		t.Fatalf("LocList (parent_id=warehouse): %v", err)
	}
	if len(listChildren.Locations) != 1 || listChildren.Locations[0].LocationID != "warehouse/bay1" {
		t.Fatalf("LocList (parent_id=warehouse): want [warehouse/bay1], got %+v", listChildren.Locations)
	}

	got, err := env.c.LocGet(ctx, "warehouse/bay1")
	if err != nil {
		t.Fatalf("LocGet: %v", err)
	}
	if got.Name != "Bay 1" || got.Capacity == nil || *got.Capacity != 10 {
		t.Fatalf("LocGet: got %+v", got)
	}

	newName := "Bay One"
	newCap := int64(25)
	newCapPtr := &newCap
	patched, err := env.c.LocPatch(ctx, "warehouse/bay1", client.LocPatchRequest{
		Name: &newName, Capacity: &newCapPtr,
	})
	if err != nil {
		t.Fatalf("LocPatch (set capacity): %v", err)
	}
	if patched.Name != "Bay One" || patched.Capacity == nil || *patched.Capacity != 25 {
		t.Fatalf("LocPatch (set capacity): got %+v", patched)
	}

	var clearedCap *int64
	clearedPatched, err := env.c.LocPatch(ctx, "warehouse/bay1", client.LocPatchRequest{
		Capacity: &clearedCap,
	})
	if err != nil {
		t.Fatalf("LocPatch (clear capacity): %v", err)
	}
	if clearedPatched.Capacity != nil {
		t.Fatalf("LocPatch (clear capacity): want nil ceiling, got %v", *clearedPatched.Capacity)
	}
	if clearedPatched.Name != "Bay One" {
		t.Errorf("LocPatch (clear capacity): name should be untouched by a capacity-only patch, got %q", clearedPatched.Name)
	}

	moved, err := env.c.LocMove(ctx, "pallets", 501, "warehouse/bay1")
	if err != nil {
		t.Fatalf("LocMove: %v", err)
	}
	if !moved.Moved || moved.Leaf != "warehouse/bay1" {
		t.Fatalf("LocMove: got %+v", moved)
	}

	reported, err := env.c.LocReport(ctx, "pallets", 501, client.LocPoint{Lat: -34.9, Lon: -56.16, Alt: 5})
	if err != nil {
		t.Fatalf("LocReport: %v", err)
	}
	if reported.Fences == nil {
		t.Errorf("LocReport: want a non-nil (even if empty) fences slice, got nil")
	}

	// Delete tested on a SEPARATE, never-occupied location -- bay1 now
	// holds pallets:501 (moved above), and a real, found-not-assumed
	// server behaviour is that force=true does NOT override an
	// occupied-location refusal (XOLU-LOC012): force's own actual
	// meaning is narrower than "delete regardless of occupancy", and
	// this test's first draft assumed otherwise until the real server
	// said no. That refusal is correct, intentional safety behaviour,
	// not a bug this client should route around.
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: "warehouse/bay2", ParentID: strPtr("warehouse"), Name: "Bay 2", Postable: &postable,
	}); err != nil {
		t.Fatalf("LocDefine warehouse/bay2: %v", err)
	}
	if err := env.c.LocDelete(ctx, "warehouse/bay2", false); err != nil {
		t.Fatalf("LocDelete (unoccupied, no force needed): %v", err)
	}
	if _, err := env.c.LocGet(ctx, "warehouse/bay2"); err == nil {
		t.Errorf("LocGet after delete: want an error, got a live record")
	}

	// The occupied case, confirmed as the real, intentional refusal
	// rather than routed around -- proves LocDelete correctly surfaces
	// XOLU-LOC012 as a structured *client.Error, not a generic failure.
	if err := env.c.LocDelete(ctx, "warehouse/bay1", true); err == nil {
		t.Errorf("LocDelete on an occupied location: want XOLU-LOC012 refusal even with force, got success")
	} else if xerr, ok := err.(*client.Error); !ok || xerr.Code != "XOLU-LOC012" {
		t.Errorf("LocDelete on an occupied location: want a structured XOLU-LOC012 *client.Error, got %v (%T)", err, err)
	}
}

// TestIntegration_LocFencesAndGeospatial exercises the fence,
// subject-position/history, contains/nearby, and pattern methods
// (XOT214, wave 16 Stage 2) against a real server -- everything
// TestIntegration_LocCoreFlow's own core set didn't already cover.
// Coordinate order is checked directly against loc.DecodeGeoJSONPolygon
// before this test was written (GeoJSON [lon, lat], not [lat, lon]) --
// a real square polygon around a known center, with a report point
// placed inside it, so fence membership, contains, and history all
// have something genuine to observe rather than an always-empty case.
func TestIntegration_LocFencesAndGeospatial(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	postable := true
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: "yard", Name: "Yard", Postable: &postable,
		Placement: client.LocPlacement{Anchor: &client.LocAnchor{Lat: -34.90, Lon: -56.16}},
	}); err != nil {
		t.Fatalf("LocDefine yard: %v", err)
	}

	pattern, err := env.c.LocPatternDefine(ctx, "standard-yard", 50)
	if err != nil {
		t.Fatalf("LocPatternDefine: %v", err)
	}
	if pattern.Name != "standard-yard" || pattern.Capacity != 50 {
		t.Fatalf("LocPatternDefine: got %+v", pattern)
	}
	patternList, err := env.c.LocPatternList(ctx)
	if err != nil {
		t.Fatalf("LocPatternList: %v", err)
	}
	if len(patternList.Patterns) != 1 {
		t.Fatalf("LocPatternList: want 1, got %d: %+v", len(patternList.Patterns), patternList.Patterns)
	}
	gotPattern, err := env.c.LocPatternGet(ctx, "standard-yard")
	if err != nil {
		t.Fatalf("LocPatternGet: %v", err)
	}
	if gotPattern.Capacity != 50 {
		t.Fatalf("LocPatternGet: got %+v", gotPattern)
	}

	// A real, valid closed square ring: 4 distinct vertices + closing
	// repeat of the first, [lon, lat] order, centered near (-56.16,
	// -34.90) with roughly a 0.02-degree half-width.
	square := [][][2]float64{{
		{-56.18, -34.88}, {-56.14, -34.88}, {-56.14, -34.92}, {-56.18, -34.92}, {-56.18, -34.88},
	}}
	fence, err := env.c.LocFenceAttach(ctx, client.LocFenceAttachRequest{
		AlignedTo: "yard",
		Geometry:  client.LocFenceGeometry{Type: "Polygon", Coordinates: square},
		Pattern:   strPtr("standard-yard"),
	})
	if err != nil {
		t.Fatalf("LocFenceAttach: %v", err)
	}
	if fence.FenceID != "yard" {
		t.Fatalf("LocFenceAttach: want fence_id=yard (tree-aligned, identity is the location itself), got %+v", fence)
	}
	if fence.PatternID == nil || *fence.PatternID != "standard-yard" {
		t.Errorf("LocFenceAttach: want pattern_id=standard-yard, got %+v", fence)
	}

	listed, err := env.c.LocFenceList(ctx)
	if err != nil {
		t.Fatalf("LocFenceList: %v", err)
	}
	if len(listed.Fences) != 1 || listed.Fences[0] != "yard" {
		t.Fatalf("LocFenceList: want [yard], got %v", listed.Fences)
	}

	gotFence, err := env.c.LocFenceGet(ctx, client.LocFenceRefByLocation("yard"))
	if err != nil {
		t.Fatalf("LocFenceGet: %v", err)
	}
	if gotFence.FenceID != "yard" {
		t.Fatalf("LocFenceGet: got %+v", gotFence)
	}

	// Widen the square slightly via patch, proving geometry replacement
	// works, not just that attach's own initial geometry was stored.
	widened := [][][2]float64{{
		{-56.20, -34.86}, {-56.12, -34.86}, {-56.12, -34.94}, {-56.20, -34.94}, {-56.20, -34.86},
	}}
	patchedFence, err := env.c.LocFencePatch(ctx, client.LocFenceRefByLocation("yard"), client.LocFenceGeometry{Type: "Polygon", Coordinates: widened})
	if err != nil {
		t.Fatalf("LocFencePatch: %v", err)
	}
	if patchedFence.FenceID != "yard" {
		t.Fatalf("LocFencePatch: got %+v", patchedFence)
	}

	// A real report INSIDE the fence's own area, so contains/nearby/
	// position/history all have something genuine to observe.
	if _, err := env.c.LocReport(ctx, "pallets", 701, client.LocPoint{Lat: -34.90, Lon: -56.16}); err != nil {
		t.Fatalf("LocReport (inside fence): %v", err)
	}

	reconciled, err := env.c.LocFenceReconcile(ctx, client.LocFenceRefByLocation("yard"))
	if err != nil {
		t.Fatalf("LocFenceReconcile: %v", err)
	}
	if reconciled.FenceID != "yard" {
		t.Fatalf("LocFenceReconcile: got %+v", reconciled)
	}

	contains, err := env.c.LocContains(ctx, -34.90, -56.16)
	if err != nil {
		t.Fatalf("LocContains: %v", err)
	}
	found := false
	for _, f := range contains.Fences {
		if f == "yard" {
			found = true
		}
	}
	if !found {
		t.Errorf("LocContains: want yard in the result for a point inside its own fence, got %v", contains.Fences)
	}

	nearby, err := env.c.LocNearby(ctx, -34.90, -56.16, 5000)
	if err != nil {
		t.Fatalf("LocNearby: %v", err)
	}
	if len(nearby.Locations) == 0 && len(nearby.Fences) == 0 {
		t.Errorf("LocNearby: want at least the yard location or fence within 5km, got neither: %+v", nearby)
	}

	pos, err := env.c.LocSubjectPosition(ctx, "pallets", 701)
	if err != nil {
		t.Fatalf("LocSubjectPosition: %v", err)
	}
	if pos.LastReportPoint == nil {
		t.Fatalf("LocSubjectPosition: want a last report point after LocReport, got nil: %+v", pos)
	}

	history, err := env.c.LocSubjectHistory(ctx, "pallets", 701)
	if err != nil {
		t.Fatalf("LocSubjectHistory: %v", err)
	}
	if len(history.Entries) == 0 {
		t.Fatalf("LocSubjectHistory: want at least one entry after LocReport, got none")
	}

	if err := env.c.LocFenceDelete(ctx, client.LocFenceRefByLocation("yard")); err != nil {
		t.Fatalf("LocFenceDelete: %v", err)
	}
	if _, err := env.c.LocFenceGet(ctx, client.LocFenceRefByLocation("yard")); err == nil {
		t.Errorf("LocFenceGet after delete: want an error, got a live record")
	}

	if err := env.c.LocPatternDelete(ctx, "standard-yard"); err != nil {
		t.Fatalf("LocPatternDelete: %v", err)
	}
	if _, err := env.c.LocPatternGet(ctx, "standard-yard"); err == nil {
		t.Errorf("LocPatternGet after delete: want an error, got a live record")
	}
}

// TestIntegration_DxpFullFlow exercises the whole dxp client surface
// (item 23) against a real server: register a def (a single bal
// transfer participant), instantiate it and confirm it commits and the
// transfer actually landed (via BalBalance, a completely separate
// primitive's own read path — not just that POST /dxp/txn returned
// 2xx), then round-trips DxpDefList/DxpDefGet/DxpTxnList/DxpTxnGet
// against what was just created, and finally confirms a genuinely
// refused instance (insufficient funds) comes back as a normal
// "released" response with a reason, not a client error — the same
// distinction dxp-composed-commitment.md's own doctrine draws between
// a refused instance and a failed request.
func TestIntegration_DxpFullFlow(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "~in", Unit: "u", Scale: 0, Floor: strPtr("-1000000"),
	}); err != nil {
		t.Fatalf("BalDefine ~in: %v", err)
	}
	if _, err := env.c.BalDefine(ctx, client.BalDefineRequest{
		AccountID: "acct", Unit: "u", Scale: 0,
	}); err != nil {
		t.Fatalf("BalDefine acct: %v", err)
	}

	def, err := env.c.DxpDefCreate(ctx, client.DxpDefCreateRequest{
		Name:    "integration_payment",
		Pattern: "3ps",
		Participants: []client.DxpParticipant{
			{ID: "payment", Primitive: "bal", Op: "transfer",
				Params: map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
		},
		PhaseTTL: client.DxpPhaseTTL{Reserve: "PT2M"},
		BindingsSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"amount": map[string]interface{}{"type": "string"}},
			"required":   []interface{}{"amount"},
		},
	})
	if err != nil {
		t.Fatalf("DxpDefCreate: %v", err)
	}
	if !def.Analysis.EngineHomogeneous {
		t.Errorf("expected engine_homogeneous:true for a bal-only def, got %+v", def.Analysis)
	}

	txn, err := env.c.DxpTxnCreate(ctx, client.DxpTxnCreateRequest{
		DefID:    def.ID,
		Bindings: map[string]interface{}{"amount": "250"},
	})
	if err != nil {
		t.Fatalf("DxpTxnCreate: %v", err)
	}
	if txn.Status != "committed" || txn.CommittedThrough != 1 {
		t.Fatalf("expected committed/1, got status=%s committed_through=%d reason=%s", txn.Status, txn.CommittedThrough, txn.Reason)
	}

	// The actual proof: bal's own read path, a completely separate
	// primitive from dxp, must show the transfer really happened.
	balRes, err := env.c.BalBalance(ctx, "acct")
	if err != nil {
		t.Fatalf("BalBalance: %v", err)
	}
	if balRes.Value != "250" {
		t.Fatalf("acct balance after dxp commit: got %s, want 250", balRes.Value)
	}

	// Round-trip DxpDefList/DxpDefGet against what was just registered.
	defList, err := env.c.DxpDefList(ctx)
	if err != nil {
		t.Fatalf("DxpDefList: %v", err)
	}
	if len(defList.Definitions) != 1 || defList.Definitions[0].ID != def.ID {
		t.Fatalf("DxpDefList: got %+v, want exactly def.ID=%d", defList.Definitions, def.ID)
	}
	gotDef, err := env.c.DxpDefGet(ctx, def.ID)
	if err != nil {
		t.Fatalf("DxpDefGet: %v", err)
	}
	if gotDef.Spec == nil || len(gotDef.Spec.Participants) != 1 || gotDef.Spec.Participants[0].Primitive != "bal" {
		t.Fatalf("DxpDefGet: expected the one bal participant back in Spec, got %+v", gotDef.Spec)
	}

	// Round-trip DxpTxnList/DxpTxnGet against what was just committed.
	txnList, err := env.c.DxpTxnList(ctx, "committed")
	if err != nil {
		t.Fatalf("DxpTxnList: %v", err)
	}
	if len(txnList.Instances) != 1 || txnList.Instances[0].ID != txn.ID {
		t.Fatalf("DxpTxnList: got %+v, want exactly txn.ID=%d", txnList.Instances, txn.ID)
	}
	gotTxn, err := env.c.DxpTxnGet(ctx, txn.ID)
	if err != nil {
		t.Fatalf("DxpTxnGet: %v", err)
	}
	if gotTxn.DefName != "integration_payment" || gotTxn.DeadlineNs == 0 {
		t.Fatalf("DxpTxnGet: expected DefName and a non-zero DeadlineNs, got %+v", gotTxn)
	}

	// A genuinely refused instance (insufficient funds against acct's
	// own zero floor) must come back as a normal 201 with
	// status:released and a reason -- not a client error. Proves the
	// released-is-not-an-error contract holds over real HTTP, not just
	// against the mock in dxp_test.go.
	refused, err := env.c.DxpTxnCreate(ctx, client.DxpTxnCreateRequest{
		DefID:    def.ID,
		Bindings: map[string]interface{}{"amount": "99999999"},
	})
	if err != nil {
		t.Fatalf("DxpTxnCreate (expected refused, not a request error): %v", err)
	}
	if refused.Status != "released" || refused.CommittedThrough != 0 || refused.Reason == "" {
		t.Fatalf("expected released/0/non-empty reason, got status=%s committed_through=%d reason=%q",
			refused.Status, refused.CommittedThrough, refused.Reason)
	}

	// Confirm the refused instance truly changed nothing: acct's
	// balance must still be exactly what the first, committed transfer
	// left it at.
	balAfterRefusal, err := env.c.BalBalance(ctx, "acct")
	if err != nil {
		t.Fatalf("BalBalance after refused instance: %v", err)
	}
	if balAfterRefusal.Value != "250" {
		t.Fatalf("acct balance after a refused dxp instance: got %s, want unchanged 250", balAfterRefusal.Value)
	}
}

// TestIntegration_BlobFullFlow exercises the six blob methods against a
// real in-process server: put (with and without an explicit key,
// proving content-addressed dedup), get (proving the streamed body and
// headers round-trip correctly), head, list (with a prefix filter),
// usage, and delete -- then confirms delete's own effect by expecting
// BlobGet to return a real 404 over HTTP, not by trusting the delete
// call's own 2xx alone.
func TestIntegration_BlobFullFlow(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	put1, err := env.c.BlobPut(ctx, "readme.txt", "text/plain", strings.NewReader("hello from the integration suite"))
	if err != nil {
		t.Fatalf("BlobPut with key: %v", err)
	}
	if put1.Key != "readme.txt" {
		t.Errorf("Key: got %q", put1.Key)
	}
	if !put1.Created {
		t.Error("Created: got false on first put, want true")
	}

	// No key: content-addressed. Putting the SAME bytes again must
	// report Created=false (dedup), proving the store is genuinely
	// content-addressed over real HTTP, not just by the client's own
	// assumption.
	put2, err := env.c.BlobPut(ctx, "", "text/plain", strings.NewReader("content addressed bytes"))
	if err != nil {
		t.Fatalf("BlobPut content-addressed (first): %v", err)
	}
	if put2.Key != put2.SHA256 {
		t.Errorf("content-addressed key should equal its own sha256: key=%q sha256=%q", put2.Key, put2.SHA256)
	}
	if !put2.Created {
		t.Error("Created: got false on genuinely new content, want true")
	}
	put3, err := env.c.BlobPut(ctx, "", "text/plain", strings.NewReader("content addressed bytes"))
	if err != nil {
		t.Fatalf("BlobPut content-addressed (dedup): %v", err)
	}
	if put3.Key != put2.Key {
		t.Errorf("identical content must produce the identical content-addressed key: got %q, want %q", put3.Key, put2.Key)
	}
	if put3.Created {
		t.Error("Created: got true on a dedup put, want false (already existed)")
	}

	get, err := env.c.BlobGet(ctx, "readme.txt")
	if err != nil {
		t.Fatalf("BlobGet: %v", err)
	}
	gotBody, err := io.ReadAll(get.Body)
	get.Body.Close()
	if err != nil {
		t.Fatalf("reading BlobGet body: %v", err)
	}
	if string(gotBody) != "hello from the integration suite" {
		t.Errorf("BlobGet body: got %q", gotBody)
	}
	if get.SHA256 != put1.SHA256 {
		t.Errorf("BlobGet SHA256 disagrees with BlobPut's own: %q vs %q", get.SHA256, put1.SHA256)
	}

	head, err := env.c.BlobHead(ctx, "readme.txt")
	if err != nil {
		t.Fatalf("BlobHead: %v", err)
	}
	if head.Size != int64(len("hello from the integration suite")) {
		t.Errorf("BlobHead Size: got %d", head.Size)
	}
	if head.SHA256 != put1.SHA256 {
		t.Errorf("BlobHead SHA256 disagrees with BlobPut's own: %q vs %q", head.SHA256, put1.SHA256)
	}

	list, err := env.c.BlobList(ctx, "read")
	if err != nil {
		t.Fatalf("BlobList: %v", err)
	}
	if list.Count != 1 || len(list.Blobs) != 1 || list.Blobs[0].Key != "readme.txt" {
		t.Fatalf("BlobList with prefix docs/: got %+v, want exactly docs/readme.txt", list.Blobs)
	}

	usage, err := env.c.BlobUsage(ctx)
	if err != nil {
		t.Fatalf("BlobUsage: %v", err)
	}
	if usage.KeyCount < 1 {
		t.Errorf("BlobUsage KeyCount: got %d, want at least 1 after puts above", usage.KeyCount)
	}

	del, err := env.c.BlobDelete(ctx, "readme.txt")
	if err != nil {
		t.Fatalf("BlobDelete: %v", err)
	}
	if !del.Deleted {
		t.Error("BlobDelete: Deleted got false")
	}

	// Prove deletion actually took effect over real HTTP -- not just
	// that /blob/{key} returned 2xx.
	_, err = env.c.BlobGet(ctx, "readme.txt")
	if err == nil {
		t.Fatal("BlobGet after delete: expected an error, got nil (key should no longer resolve)")
	}
	xoluErr, ok := err.(*client.Error)
	if !ok {
		t.Fatalf("BlobGet after delete: expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("BlobGet after delete: HTTPStatus got %d, want 404", xoluErr.HTTPStatus)
	}
}

// TestIntegration_Raw exercises Raw against a real in-process server,
// issuing a request to a real, already-declared endpoint (bal define)
// as a stand-in for "a path this client's own typed methods don't
// cover" -- proving the request actually reaches the server correctly
// and the response comes back unfiltered, not decoded.
func TestIntegration_Raw(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	result, err := env.c.Raw(ctx, http.MethodPost, "/api/v2/bal/def", "",
		strings.NewReader(`{"account_id":"raw_test_acct","unit":"u","scale":0}`))
	if err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if result.StatusCode != http.StatusCreated {
		t.Fatalf("StatusCode: got %d, body=%s", result.StatusCode, result.Body)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(result.Body, &decoded); err != nil {
		t.Fatalf("response body is not valid JSON: %v (body=%s)", err, result.Body)
	}
	if decoded["account_id"] != "raw_test_acct" {
		t.Errorf("account_id: got %v", decoded["account_id"])
	}

	// Confirm this actually created a real account, not just that the
	// endpoint returned 2xx -- read it back through the client's own
	// typed BalBalance method.
	bal, err := env.c.BalBalance(ctx, "raw_test_acct")
	if err != nil {
		t.Fatalf("BalBalance after Raw create: %v", err)
	}
	if bal.Value != "0" {
		t.Errorf("balance of a freshly Raw-created account: got %s, want 0", bal.Value)
	}

	// A deliberately malformed request (missing required fields) must
	// come back as a real non-2xx with the body intact, not a Go error
	// -- that's the whole point of Raw not decoding errors. The real
	// server happens to return 500 here (an internal validation path,
	// not a clean 400) -- Raw's own job is just to pass that through
	// unfiltered, whatever it is, not to characterise which status
	// bal/def's own handler chooses for a given bad input.
	badResult, err := env.c.Raw(ctx, http.MethodPost, "/api/v2/bal/def", "", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Raw with an invalid body should still return a result, not an error: %v", err)
	}
	if badResult.StatusCode < 400 {
		t.Errorf("expected a non-2xx for a malformed bal/def body, got %d", badResult.StatusCode)
	}
}

// TestIntegration_Export exercises the full async export client flow
// against a real in-process server: seeds real entity data, runs
// Export end to end (start, poll, download), and confirms the
// downloaded content is a genuine, openable zip containing that data
// -- not just that the call sequence completes without error.
func TestIntegration_Export(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	if _, err := env.c.Create(ctx, "export_probe", map[string]any{"marker": "TestIntegration_Export"}); err != nil {
		t.Fatalf("seed entity: %v", err)
	}

	var buf bytes.Buffer
	result, err := env.c.Export(ctx, &buf)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if result.Ticket == "" {
		t.Error("Ticket is empty")
	}
	if result.BlobKey == "" {
		t.Error("BlobKey is empty")
	}
	if buf.Len() == 0 {
		t.Fatal("Export wrote zero bytes")
	}
	if int64(buf.Len()) != result.Size {
		t.Errorf("streamed %d bytes but result.Size says %d", buf.Len(), result.Size)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("downloaded export is not a valid zip: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if f.Name == "nodes.json" {
			found = true
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("opening nodes.json: %v", err)
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatalf("reading nodes.json: %v", err)
			}
			if !bytes.Contains(content, []byte("TestIntegration_Export")) {
				t.Errorf("nodes.json does not contain the seeded entity's data: %s", content)
			}
		}
	}
	if !found {
		t.Error("downloaded export has no nodes.json")
	}

	// The blob is retrievable the normal way too -- confirms Export's
	// own download used the same blob the ticket actually pointed at,
	// not some other artifact.
	direct, err := env.c.BlobGet(ctx, result.BlobKey)
	if err != nil {
		t.Fatalf("BlobGet on the export's own key: %v", err)
	}
	direct.Body.Close()
	if direct.SHA256 != result.SHA256 {
		t.Errorf("SHA256 mismatch between Export's own result and a direct BlobGet: %q vs %q",
			result.SHA256, direct.SHA256)
	}
}

// TestIntegration_DefineEntitySchema exercises schema registration
// against a real server end to end: defines a schema, confirms an
// entity conforming to it is accepted, confirms one violating it
// (missing a required field) is rejected, confirms GetEntitySchema
// reads back what was just written, then updates the schema and
// confirms the new rule takes effect immediately.
func TestIntegration_DefineEntitySchema(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"sku":   map[string]interface{}{"type": "string"},
			"price": map[string]interface{}{"type": "number"},
		},
		"required":             []string{"sku"},
		"additionalProperties": false,
	}
	if err := env.c.DefineEntitySchema(ctx, "products", schema); err != nil {
		t.Fatalf("DefineEntitySchema: %v", err)
	}

	if _, err := env.c.Create(ctx, "products", map[string]any{"sku": "WIDGET-1", "price": 9.99}); err != nil {
		t.Fatalf("create conforming to the new schema: %v", err)
	}

	if _, err := env.c.Create(ctx, "products", map[string]any{"price": 9.99}); err == nil {
		t.Fatal("expected an error creating an entity missing the required 'sku' field")
	}

	got, err := env.c.GetEntitySchema(ctx, "products")
	if err != nil {
		t.Fatalf("GetEntitySchema: %v", err)
	}
	foundSKU := false
	for _, f := range got.Fields {
		if f.Name == "sku" && f.Required {
			foundSKU = true
		}
	}
	if !foundSKU {
		t.Errorf("GetEntitySchema did not read back the 'sku' field as required: %+v", got.Fields)
	}

	// Update: make price required too, confirm the new rule takes
	// effect on the very next write, proving this was an update, not
	// a silently-ignored second create.
	schema["required"] = []string{"sku", "price"}
	if err := env.c.DefineEntitySchema(ctx, "products", schema); err != nil {
		t.Fatalf("DefineEntitySchema (update): %v", err)
	}
	if _, err := env.c.Create(ctx, "products", map[string]any{"sku": "WIDGET-2"}); err == nil {
		t.Fatal("expected an error after price became required, but the create succeeded")
	}
}

// TestIntegration_ListEntities reuses the same mixed schemaless/
// adapted scenario proven directly against pkg/server
// (TestListEntities_MixedSchemalessAndAdapted), through the client
// this time -- confirms the client correctly surfaces both an adapted
// entity type's columns and a schemaless one's row count/timestamps.
func TestIntegration_ListEntities(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	if _, err := env.c.Create(ctx, "notes", map[string]any{"text": "a"}); err != nil {
		t.Fatalf("create notes: %v", err)
	}
	if _, err := env.c.Create(ctx, "notes", map[string]any{"text": "b"}); err != nil {
		t.Fatalf("create notes: %v", err)
	}

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"tag": map[string]interface{}{"type": "string"}},
		"required":   []string{"tag"},
	}
	if err := env.c.DefineEntitySchema(ctx, "labels", schema); err != nil {
		t.Fatalf("DefineEntitySchema: %v", err)
	}
	if _, err := env.c.Create(ctx, "labels", map[string]any{"tag": "urgent"}); err != nil {
		t.Fatalf("create labels: %v", err)
	}

	entities, err := env.c.ListEntities(ctx, false)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}

	var notes, labels *client.EntityListEntry
	for i := range entities {
		switch entities[i].EntityType {
		case "notes":
			notes = &entities[i]
		case "labels":
			labels = &entities[i]
		}
	}

	if notes == nil {
		t.Fatal("notes missing from ListEntities")
	}
	if notes.Count != 2 || notes.HasSchema || notes.Adapted {
		t.Errorf("notes: got count=%d has_schema=%v adapted=%v", notes.Count, notes.HasSchema, notes.Adapted)
	}
	if notes.FirstSeen == "" {
		t.Error("notes: expected FirstSeen populated for a schemaless entity")
	}

	if labels == nil {
		t.Fatal("labels missing from ListEntities -- this is the real bug this feature was built to guard against")
	}
	if labels.Count != 1 || !labels.HasSchema || !labels.Adapted {
		t.Errorf("labels: got count=%d has_schema=%v adapted=%v", labels.Count, labels.HasSchema, labels.Adapted)
	}
	found := false
	for _, c := range labels.Columns {
		if c == "tag" {
			found = true
		}
	}
	if !found {
		t.Errorf("labels: expected 'tag' among Columns, got %v", labels.Columns)
	}
}

// TestIntegration_PromoteStrict_Success exercises the full strict
// promotion flow against a real server: seed conforming data, promote
// strict, confirm migration actually happened (ListEntities shows the
// adapted entity type with the correct row count, not the split
// picture flex would leave behind).
func TestIntegration_PromoteStrict_Success(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := env.c.Create(ctx, "invoices", map[string]any{"amount": "10.00", "paid": false}); err != nil {
			t.Fatalf("seed invoices: %v", err)
		}
	}

	job, err := env.c.PromoteStrict(ctx, "invoices", nil)
	if err != nil {
		t.Fatalf("PromoteStrict: %v", err)
	}
	if job.Status != client.PromoteJobComplete {
		t.Fatalf("job status: got %v, want complete (job=%+v)", job.Status, job)
	}
	if job.Result == nil || job.Result.MigratedRows != 5 {
		t.Fatalf("Result: got %+v, want MigratedRows=5", job.Result)
	}

	entities, err := env.c.ListEntities(ctx, false)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	var invoices *client.EntityListEntry
	for i := range entities {
		if entities[i].EntityType == "invoices" {
			invoices = &entities[i]
		}
	}
	if invoices == nil {
		t.Fatal("invoices missing from ListEntities after strict promotion")
	}
	if invoices.Count != 5 || !invoices.Adapted || !invoices.HasSchema {
		t.Errorf("invoices after strict promotion: got %+v, want count=5 adapted=true has_schema=true", invoices)
	}
}

// TestIntegration_PromoteStrict_Rejected exercises the rejection path
// against a real server: seed data that violates an explicit schema,
// confirm the job reports rejected with the correct failing row, and
// confirm the entity type is genuinely untouched afterward.
func TestIntegration_PromoteStrict_Rejected(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	if _, err := env.c.Create(ctx, "messy", map[string]any{"val": "text"}); err != nil {
		t.Fatalf("seed messy 1: %v", err)
	}
	if _, err := env.c.Create(ctx, "messy", map[string]any{"val": 42}); err != nil {
		t.Fatalf("seed messy 2: %v", err)
	}

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"val": map[string]interface{}{"type": "string"}},
		"required":   []string{"val"},
	}
	job, err := env.c.PromoteStrict(ctx, "messy", schema)
	if err != nil {
		t.Fatalf("PromoteStrict (rejection is not a Go error): %v", err)
	}
	if job.Status != client.PromoteJobRejected {
		t.Fatalf("job status: got %v, want rejected (job=%+v)", job.Status, job)
	}
	if len(job.Failures) != 1 || job.Failures[0].ID != 2 {
		t.Errorf("Failures: got %+v, want exactly one failure for row id=2", job.Failures)
	}

	entities, err := env.c.ListEntities(ctx, false)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}
	var messy *client.EntityListEntry
	for i := range entities {
		if entities[i].EntityType == "messy" {
			messy = &entities[i]
		}
	}
	if messy == nil || messy.Adapted || messy.HasSchema || messy.Count != 2 {
		t.Errorf("messy after a rejected promotion: got %+v, want unchanged (count=2, adapted=false, has_schema=false)", messy)
	}
}

// TestIntegration_PromoteFlex_WarnsAboutUnmigratedData exercises flex
// against a real server with pre-existing data, confirming the
// Warning field is genuinely populated (not just present in a mock).
func TestIntegration_PromoteFlex_WarnsAboutUnmigratedData(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	if _, err := env.c.Create(ctx, "notes", map[string]any{"text": "pre-existing"}); err != nil {
		t.Fatalf("seed notes: %v", err)
	}

	result, err := env.c.PromoteFlex(ctx, "notes", nil)
	if err != nil {
		t.Fatalf("PromoteFlex: %v", err)
	}
	if result.Warning == "" {
		t.Error("expected a warning about the pre-existing, un-migrated row")
	}

	suggestion, err := env.c.GetSchemaSuggestion(ctx, "notes")
	// After promotion, "notes" is adapted, not schemaless -- the
	// server's own suggestion endpoint reads from blob storage
	// directly, so this should now find nothing to sample.
	if err == nil {
		t.Logf("schema-suggestion after promotion still returned data: %+v (not necessarily wrong, just noting the boundary)", suggestion)
	}
}

// TestIntegration_MachineDefWrites exercises the full FSM-definition
// write surface against a real server: create, read back (both the
// raw and the parsed-analysis form), replace, validate (an invalid
// spec first -- confirming this never produces a Go error -- then a
// valid one), and delete, confirming the definition is genuinely gone
// afterward.
func TestIntegration_MachineDefWrites(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	spec := client.MachineSpec{
		Name:        "door",
		Initial:     "closed",
		Determinism: "firstmatch",
		States: map[string]client.StateDef{
			"closed": {},
			"open":   {Terminal: true},
		},
		Transitions: []client.TransitionDef{
			{From: json.RawMessage(`"closed"`), Input: "open", To: "open"},
		},
	}

	created, err := env.c.CreateMachineDef(ctx, spec)
	if err != nil {
		t.Fatalf("CreateMachineDef: %v", err)
	}
	if created.ID == 0 || created.Name != "door" {
		t.Fatalf("got %+v", created)
	}
	if created.Analysis == nil || !created.Analysis.Reachable {
		t.Fatalf("expected a populated, reachable Analysis, got %+v", created.Analysis)
	}

	fetched, err := env.c.GetMachineDef(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMachineDef: %v", err)
	}
	if fetched.Spec.Name != "door" {
		t.Errorf("fetched spec name: got %q", fetched.Spec.Name)
	}
	parsed, err := fetched.ParsedAnalysis()
	if err != nil {
		t.Fatalf("ParsedAnalysis: %v", err)
	}
	if parsed == nil || !parsed.Reachable {
		t.Fatalf("ParsedAnalysis: got %+v", parsed)
	}

	replacedSpec := spec
	replacedSpec.Name = "door-v2"
	replaced, err := env.c.ReplaceMachineDef(ctx, created.ID, replacedSpec)
	if err != nil {
		t.Fatalf("ReplaceMachineDef: %v", err)
	}
	if replaced.Name != "door-v2" {
		t.Errorf("replaced name: got %q", replaced.Name)
	}
	refetched, err := env.c.GetMachineDef(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMachineDef after replace: %v", err)
	}
	if refetched.Spec.Name != "door-v2" {
		t.Errorf("the replacement did not take effect: got %q", refetched.Spec.Name)
	}

	invalidResult, err := env.c.ValidateMachineDef(ctx, client.MachineSpec{Name: "no-determinism"})
	if err != nil {
		t.Fatalf("ValidateMachineDef on an invalid spec must not be a Go error: %v", err)
	}
	if invalidResult.Valid {
		t.Error("expected Valid=false for a spec with no determinism declared")
	}
	if len(invalidResult.Errors) == 0 {
		t.Error("expected at least one validation error")
	}

	validResult, err := env.c.ValidateMachineDef(ctx, spec)
	if err != nil {
		t.Fatalf("ValidateMachineDef: %v", err)
	}
	if !validResult.Valid {
		t.Fatalf("expected the same spec that was successfully created to validate, got %+v", validResult)
	}

	if err := env.c.DeleteMachineDef(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMachineDef: %v", err)
	}
	_, err = env.c.GetMachineDef(ctx, created.ID)
	if err == nil {
		t.Fatal("expected GetMachineDef to fail after deletion")
	}
	xoluErr, ok := err.(*client.Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus after delete: got %d, want 404", xoluErr.HTTPStatus)
	}
}
