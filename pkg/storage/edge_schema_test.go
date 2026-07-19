// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

// ---------------------------------------------------------------------------
// Stage 6 — Edge schema registry tests
//
// Covers:
//   - t<X>_e_sch DDL created alongside t<X>_edges and t<X>_eseq
//   - RegisterEdgeSchema: persists row, suppresses warning
//   - RegisterEdgeSchema: idempotent on same schema
//   - RegisterEdgeSchema: updates row on schema change
//   - IsEdgeSchemaRegistered: returns false before, true after registration
//   - SuppressEdgeSchemaWarning: in-memory suppression, no DB write
//   - warnOnceEdge: fires exactly once per label across multiple calls
//   - Startup suppression: loadEdgeSchemaSuppressions pre-suppresses
//     labels registered in a previous session (simulated via a second store
//     opening the same DB file)
//   - Warning does NOT fire for plain AddEdge (topology only, no props)
// ---------------------------------------------------------------------------

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/rs/zerolog"
)

// setupEdgeSchemaStore returns a graph-enabled store wired with a zerolog
// buffer so warn output can be inspected in tests.
func setupEdgeSchemaStore(t *testing.T) (storage.Store, *bytes.Buffer, func()) {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "xolu-edge-schema-test-*.db")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	tmpFile.Close()
	dbPath := tmpFile.Name()

	store, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type:         "sqlite",
		DBPath:       dbPath,
		GraphEnabled: true,
	})
	if err != nil {
		os.Remove(dbPath)
		t.Fatalf("NewStoreFromConfig: %v", err)
	}

	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	store.(*storage.SQLiteStore).WithLogger(logger)

	return store, &buf, func() {
		store.Close()
		os.Remove(dbPath)
	}
}

func asEdgeSchemaStore(t *testing.T, store storage.Store) storage.EdgeSchemaStore {
	t.Helper()
	ess, ok := store.(storage.EdgeSchemaStore)
	if !ok {
		t.Skip("store does not implement EdgeSchemaStore")
	}
	return ess
}

func openRawEdge(t *testing.T, store storage.Store) *sql.DB {
	t.Helper()
	cfg := store.Config()
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ---------------------------------------------------------------------------

func TestEdgeSchema_DDL(t *testing.T) {
	store, _, cleanup := setupEdgeSchemaStore(t)
	defer cleanup()
	db := openRawEdge(t, store)
	ctx := context.Background()

	// t0000_e_sch must exist.
	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='t0000_e_sch'",
	).Scan(&n)
	if err != nil || n != 1 {
		t.Errorf("t0000_e_sch not created: n=%d err=%v", n, err)
	}
}

func TestEdgeSchema_RegisterAndQuery(t *testing.T) {
	store, _, cleanup := setupEdgeSchemaStore(t)
	defer cleanup()
	ess := asEdgeSchemaStore(t, store)
	ctx := context.Background()

	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"since": map[string]interface{}{"type": "integer"},
		},
	}

	// Before registration: not registered.
	registered, err := ess.IsEdgeSchemaRegistered(ctx, "KNOWS")
	if err != nil || registered {
		t.Fatalf("IsEdgeSchemaRegistered before: registered=%v err=%v", registered, err)
	}

	// Register.
	if err := ess.RegisterEdgeSchema(ctx, "KNOWS", schema); err != nil {
		t.Fatalf("RegisterEdgeSchema: %v", err)
	}

	// After registration: registered.
	registered, err = ess.IsEdgeSchemaRegistered(ctx, "KNOWS")
	if err != nil || !registered {
		t.Fatalf("IsEdgeSchemaRegistered after: registered=%v err=%v", registered, err)
	}

	// DB row must be present with non-empty hash and JSON.
	db := openRawEdge(t, store)
	var hash, schemaJSON string
	err = db.QueryRowContext(ctx,
		"SELECT schema_hash, schema_json FROM t0000_e_sch WHERE rel = 'KNOWS'",
	).Scan(&hash, &schemaJSON)
	if err != nil {
		t.Fatalf("query t0000_e_sch: %v", err)
	}
	if hash == "" {
		t.Error("schema_hash is empty")
	}
	if !strings.Contains(schemaJSON, "since") {
		t.Errorf("schema_json missing 'since': %s", schemaJSON)
	}
}

func TestEdgeSchema_RegisterIdempotent(t *testing.T) {
	store, _, cleanup := setupEdgeSchemaStore(t)
	defer cleanup()
	ess := asEdgeSchemaStore(t, store)
	ctx := context.Background()

	schema := map[string]interface{}{"properties": map[string]interface{}{
		"weight": map[string]interface{}{"type": "number"},
	}}

	if err := ess.RegisterEdgeSchema(ctx, "FOLLOWS", schema); err != nil {
		t.Fatalf("first register: %v", err)
	}
	// Second call with same schema must not error.
	if err := ess.RegisterEdgeSchema(ctx, "FOLLOWS", schema); err != nil {
		t.Fatalf("second register (same schema): %v", err)
	}
	// Must still be registered.
	ok, _ := ess.IsEdgeSchemaRegistered(ctx, "FOLLOWS")
	if !ok {
		t.Error("not registered after idempotent re-registration")
	}
}

func TestEdgeSchema_RegisterUpdatesOnSchemaChange(t *testing.T) {
	store, _, cleanup := setupEdgeSchemaStore(t)
	defer cleanup()
	ess := asEdgeSchemaStore(t, store)
	ctx := context.Background()
	db := openRawEdge(t, store)

	schema1 := map[string]interface{}{"properties": map[string]interface{}{
		"since": map[string]interface{}{"type": "integer"},
	}}
	schema2 := map[string]interface{}{"properties": map[string]interface{}{
		"since":  map[string]interface{}{"type": "integer"},
		"weight": map[string]interface{}{"type": "number"},
	}}

	if err := ess.RegisterEdgeSchema(ctx, "KNOWS", schema1); err != nil {
		t.Fatalf("register v1: %v", err)
	}
	var hash1 string
	db.QueryRowContext(ctx, "SELECT schema_hash FROM t0000_e_sch WHERE rel='KNOWS'").Scan(&hash1)

	if err := ess.RegisterEdgeSchema(ctx, "KNOWS", schema2); err != nil {
		t.Fatalf("register v2: %v", err)
	}
	var hash2 string
	db.QueryRowContext(ctx, "SELECT schema_hash FROM t0000_e_sch WHERE rel='KNOWS'").Scan(&hash2)

	if hash1 == hash2 {
		t.Error("schema hash did not change after schema update")
	}
}

func TestEdgeSchema_WarnOnceOnAddEdgeWithProps(t *testing.T) {
	store, buf, cleanup := setupEdgeSchemaStore(t)
	defer cleanup()
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	props := map[string]interface{}{"since": 2024}

	// First call with an unregistered label: the label is now auto-registered
	// silently before the write. The warn-once should NOT fire because the label
	// is registered (by auto-registration) before the isEdgeSuppressed check.
	if _, err := eps.AddEdgeWithProps(ctx, "user:1", "user:2", "UNREGISTERED", props); err != nil {
		t.Fatalf("AddEdgeWithProps 1: %v", err)
	}
	// Auto-registration suppresses the warning — no warn expected.
	log1 := buf.String()
	if strings.Contains(log1, "UNREGISTERED") {
		t.Errorf("expected no warning after auto-registration, got: %s", log1)
	}

	// The label should now be discoverable via IsEdgeSchemaRegistered.
	if ess, ok := store.(storage.EdgeSchemaStore); ok {
		registered, err := ess.IsEdgeSchemaRegistered(ctx, "UNREGISTERED")
		if err != nil {
			t.Fatalf("IsEdgeSchemaRegistered: %v", err)
		}
		if !registered {
			t.Error("label UNREGISTERED should be registered after auto-registration")
		}
	}

	buf.Reset()

	// Second call: still no warning (already registered).
	if _, err := eps.AddEdgeWithProps(ctx, "user:1", "user:3", "UNREGISTERED", props); err != nil {
		t.Fatalf("AddEdgeWithProps 2: %v", err)
	}
	if buf.Len() > 0 {
		t.Errorf("expected no warning on second call, got: %s", buf.String())
	}
}

func TestEdgeSchema_NoWarnAfterRegister(t *testing.T) {
	store, buf, cleanup := setupEdgeSchemaStore(t)
	defer cleanup()
	ess := asEdgeSchemaStore(t, store)
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	schema := map[string]interface{}{"properties": map[string]interface{}{
		"since": map[string]interface{}{"type": "integer"},
	}}
	if err := ess.RegisterEdgeSchema(ctx, "KNOWS", schema); err != nil {
		t.Fatalf("RegisterEdgeSchema: %v", err)
	}

	buf.Reset()

	// AddEdgeWithProps on a registered label must not warn.
	if _, err := eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS",
		map[string]interface{}{"since": 2020}); err != nil {
		t.Fatalf("AddEdgeWithProps: %v", err)
	}
	if buf.Len() > 0 {
		t.Errorf("unexpected warning after registration: %s", buf.String())
	}
}

func TestEdgeSchema_SuppressWithoutRegister(t *testing.T) {
	store, buf, cleanup := setupEdgeSchemaStore(t)
	defer cleanup()
	ess := asEdgeSchemaStore(t, store)
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	ess.SuppressEdgeSchemaWarning("SILENT")

	// Must not warn and must not write to DB.
	if _, err := eps.AddEdgeWithProps(ctx, "a:1", "b:1", "SILENT",
		map[string]interface{}{"x": 1}); err != nil {
		t.Fatalf("AddEdgeWithProps: %v", err)
	}
	if buf.Len() > 0 {
		t.Errorf("unexpected warning after SuppressEdgeSchemaWarning: %s", buf.String())
	}

	// Must not be in the DB.
	registered, _ := ess.IsEdgeSchemaRegistered(ctx, "SILENT")
	if registered {
		t.Error("SuppressEdgeSchemaWarning must not write to t0000_e_sch")
	}
}

func TestEdgeSchema_NoWarnForTopologyOnlyEdge(t *testing.T) {
	// Plain AddEdge (no properties) must never trigger the schema warning,
	// even for an unregistered label.
	store, buf, cleanup := setupEdgeSchemaStore(t)
	defer cleanup()
	ctx := context.Background()

	g, ok := store.(interface {
		Graph() interface {
			AddEdge(string, string, string) error
		}
	})
	_ = g
	_ = ok
	// AddEdge goes through the graph layer, not the store directly.
	// We verify via the absence of any log output after creating a node
	// with a REF field (which internally calls AddEdge).
	buf.Reset()
	if _, err := store.Create(ctx, "user", map[string]interface{}{
		"name": "Alice",
		"friend": map[string]interface{}{
			"type": "REF", "entity": "user", "id": 99,
		},
	}); err != nil {
		// Will fail with ErrNotFound for the target — that's fine, the edge was still attempted.
		_ = err
	}
	log := buf.String()
	if strings.Contains(log, "no registered schema") || strings.Contains(log, "AddEdgeWithProps") {
		t.Errorf("plain topology AddEdge should not trigger schema warning, got: %s", log)
	}
}

func TestEdgeSchema_StartupSuppression(t *testing.T) {
	// Register a label in session 1, then open the same DB as session 2 and
	// verify AddEdgeWithProps does not warn (loaded from t<X>_e_sch at startup).
	tmpFile, err := os.CreateTemp("", "xolu-edge-schema-startup-*.db")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	tmpFile.Close()
	dbPath := tmpFile.Name()
	defer os.Remove(dbPath)

	ctx := context.Background()
	schema := map[string]interface{}{"properties": map[string]interface{}{
		"since": map[string]interface{}{"type": "integer"},
	}}

	// Session 1: register the schema.
	{
		store, err := storage.NewStoreFromConfig(storage.StoreConfig{
			Type: "sqlite", DBPath: dbPath, GraphEnabled: true,
		})
		if err != nil {
			t.Fatalf("session 1 open: %v", err)
		}
		ess := asEdgeSchemaStore(t, store)
		if err := ess.RegisterEdgeSchema(ctx, "KNOWS", schema); err != nil {
			store.Close()
			t.Fatalf("session 1 register: %v", err)
		}
		store.Close()
	}

	// Session 2: open the same DB — loadEdgeSchemaSuppressions must pre-suppress.
	var buf bytes.Buffer
	store2, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type: "sqlite", DBPath: dbPath, GraphEnabled: true,
	})
	if err != nil {
		t.Fatalf("session 2 open: %v", err)
	}
	defer store2.Close()

	logger := zerolog.New(&buf)
	store2.(*storage.SQLiteStore).WithLogger(logger)

	eps2 := asEdgeStore(t, store2)
	if _, err := eps2.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS",
		map[string]interface{}{"since": 2020}); err != nil {
		t.Fatalf("session 2 AddEdgeWithProps: %v", err)
	}
	if buf.Len() > 0 {
		t.Errorf("startup suppression failed — warning fired in session 2: %s", buf.String())
	}
}
