// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

// ---------------------------------------------------------------------------
// Stage 7 — Adapted edge table tests
//
// Covers:
//   - RegisterAdaptedEdge: creates t<X>_edata_<label>, writes column_spec to
//     t<X>_e_sch, populates in-memory adapted registry with ElementEdge spec
//   - IsEdgeAdapted: false before, true after RegisterAdaptedEdge
//   - AdaptedTableSpec.TableName routes ElementEdge to edata_ prefix
//   - AddEdgeWithProps routes to adapted table when spec is registered
//   - GetEdge dispatches to adapted table when spec is registered
//   - GetManyEdges dispatches correctly across mixed adapted/blob edge IDs
//   - Startup persistence: adapted edge spec loaded from t<X>_e_sch on
//     second store open
//   - Blob path still works for unregistered labels alongside adapted labels
//   - Idempotent RegisterAdaptedEdge
// ---------------------------------------------------------------------------

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

func setupAdaptedEdgeStore(t *testing.T) (storage.Store, func()) {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "xolu-adapted-edge-test-*.db")
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
	return store, func() { store.Close(); os.Remove(dbPath) }
}

func asAdaptedEdgeStore(t *testing.T, store storage.Store) storage.AdaptedEdgeStore {
	t.Helper()
	aes, ok := store.(storage.AdaptedEdgeStore)
	if !ok {
		t.Skip("store does not implement AdaptedEdgeStore")
	}
	return aes
}

var knowsSchema = map[string]interface{}{
	"properties": map[string]interface{}{
		"since":  map[string]interface{}{"type": "integer"},
		"weight": map[string]interface{}{"type": "number"},
	},
}

// ---------------------------------------------------------------------------

func TestAdaptedEdge_TableName(t *testing.T) {
	// AdaptedTableSpec with Kind=ElementEdge must produce edata_ prefix.
	spec := &storage.AdaptedTableSpec{
		Entity:   "KNOWS",
		Kind:     tenant.ElementEdge,
		TenantID: 1,
	}
	want := "t0001_edata_KNOWS"
	if got := spec.TableName(); got != want {
		t.Errorf("TableName() = %q, want %q", got, want)
	}

	// Kind=ElementNode must still produce ndata_ prefix.
	nodeSpec := &storage.AdaptedTableSpec{
		Entity:   "user",
		Kind:     tenant.ElementNode,
		TenantID: 1,
	}
	wantNode := "t0001_ndata_user"
	if got := nodeSpec.TableName(); got != wantNode {
		t.Errorf("node TableName() = %q, want %q", got, wantNode)
	}
}

func TestAdaptedEdge_RegisterCreatesTable(t *testing.T) {
	store, cleanup := setupAdaptedEdgeStore(t)
	defer cleanup()
	aes := asAdaptedEdgeStore(t, store)
	ctx := context.Background()

	if err := aes.RegisterAdaptedEdge(ctx, "KNOWS", knowsSchema); err != nil {
		t.Fatalf("RegisterAdaptedEdge: %v", err)
	}

	// t0000_edata_KNOWS must exist.
	db := openRawEdge(t, store)
	var n int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='t0000_edata_KNOWS'",
	).Scan(&n)
	if n != 1 {
		t.Error("t0000_edata_KNOWS not created")
	}

	// t0000_e_sch must have column_spec populated.
	var colSpec sql.NullString
	db.QueryRowContext(ctx,
		"SELECT column_spec FROM t0000_e_sch WHERE rel='KNOWS'",
	).Scan(&colSpec)
	if !colSpec.Valid || colSpec.String == "" {
		t.Error("column_spec not written to t0000_e_sch")
	}
}

func TestAdaptedEdge_IsEdgeAdapted(t *testing.T) {
	store, cleanup := setupAdaptedEdgeStore(t)
	defer cleanup()
	aes := asAdaptedEdgeStore(t, store)
	ctx := context.Background()

	if aes.IsEdgeAdapted("KNOWS") {
		t.Error("IsEdgeAdapted should be false before registration")
	}

	if err := aes.RegisterAdaptedEdge(ctx, "KNOWS", knowsSchema); err != nil {
		t.Fatalf("RegisterAdaptedEdge: %v", err)
	}

	if !aes.IsEdgeAdapted("KNOWS") {
		t.Error("IsEdgeAdapted should be true after registration")
	}
}

func TestAdaptedEdge_AddEdgeWithPropsRoutes(t *testing.T) {
	store, cleanup := setupAdaptedEdgeStore(t)
	defer cleanup()
	aes := asAdaptedEdgeStore(t, store)
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	if err := aes.RegisterAdaptedEdge(ctx, "KNOWS", knowsSchema); err != nil {
		t.Fatalf("RegisterAdaptedEdge: %v", err)
	}

	edgeID, err := eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS",
		map[string]interface{}{"since": 2020, "weight": 0.9})
	if err != nil {
		t.Fatalf("AddEdgeWithProps: %v", err)
	}
	if edgeID == 0 {
		t.Fatal("expected non-zero edgeID")
	}

	db := openRawEdge(t, store)

	// Row must be in t0000_edata_KNOWS, not t0000_edges.
	var adaptedCount int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM t0000_edata_KNOWS WHERE id = ?", edgeID,
	).Scan(&adaptedCount)
	if adaptedCount != 1 {
		t.Errorf("expected 1 row in t0000_edata_KNOWS, got %d", adaptedCount)
	}

	var blobCount int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM t0000_edges WHERE edge_id = ?", edgeID,
	).Scan(&blobCount)
	if blobCount != 0 {
		t.Errorf("expected no row in t0000_edges for adapted edge, got %d", blobCount)
	}
}

func TestAdaptedEdge_GetEdgeDispatches(t *testing.T) {
	store, cleanup := setupAdaptedEdgeStore(t)
	defer cleanup()
	aes := asAdaptedEdgeStore(t, store)
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	if err := aes.RegisterAdaptedEdge(ctx, "KNOWS", knowsSchema); err != nil {
		t.Fatalf("RegisterAdaptedEdge: %v", err)
	}

	edgeID, _ := eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS",
		map[string]interface{}{"since": 2021, "weight": 1.5})

	result, err := eps.GetEdge(ctx, edgeID)
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if result.Rel != "KNOWS" {
		t.Errorf("Rel = %q, want KNOWS", result.Rel)
	}
	// since is stored as INTEGER — comes back as int64 from SQLite
	sinceVal := result.Properties["since"]
	switch v := sinceVal.(type) {
	case int64:
		if v != 2021 {
			t.Errorf("since = %d, want 2021", v)
		}
	case float64:
		if v != 2021 {
			t.Errorf("since = %f, want 2021", v)
		}
	default:
		t.Errorf("since unexpected type %T value %v", sinceVal, sinceVal)
	}
}

func TestAdaptedEdge_GetManyEdgesMixed(t *testing.T) {
	store, cleanup := setupAdaptedEdgeStore(t)
	defer cleanup()
	aes := asAdaptedEdgeStore(t, store)
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	// KNOWS is adapted; FOLLOWS is blob.
	if err := aes.RegisterAdaptedEdge(ctx, "KNOWS", knowsSchema); err != nil {
		t.Fatalf("RegisterAdaptedEdge: %v", err)
	}

	idAdapted, _ := eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS",
		map[string]interface{}{"since": 2022})
	idBlob, _ := eps.AddEdgeWithProps(ctx, "user:1", "user:3", "FOLLOWS",
		map[string]interface{}{"notify": true})

	results, err := eps.GetManyEdges(ctx, []int{idAdapted, idBlob, 99999})
	if err != nil {
		t.Fatalf("GetManyEdges: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	if _, ok := results[idAdapted]; !ok {
		t.Errorf("adapted edge %d not in results", idAdapted)
	}
	if _, ok := results[idBlob]; !ok {
		t.Errorf("blob edge %d not in results", idBlob)
	}
	if _, ok := results[99999]; ok {
		t.Error("unknown ID 99999 should not be in results")
	}
	if results[idAdapted].Rel != "KNOWS" {
		t.Errorf("adapted result Rel = %q, want KNOWS", results[idAdapted].Rel)
	}
	if results[idBlob].Rel != "FOLLOWS" {
		t.Errorf("blob result Rel = %q, want FOLLOWS", results[idBlob].Rel)
	}
}

func TestAdaptedEdge_StartupPersistence(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "xolu-adapted-edge-startup-*.db")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	tmpFile.Close()
	dbPath := tmpFile.Name()
	defer os.Remove(dbPath)

	ctx := context.Background()

	// Session 1: register adapted edge.
	{
		store, err := storage.NewStoreFromConfig(storage.StoreConfig{
			Type: "sqlite", DBPath: dbPath, GraphEnabled: true,
		})
		if err != nil {
			t.Fatalf("session 1 open: %v", err)
		}
		aes := asAdaptedEdgeStore(t, store)
		if err := aes.RegisterAdaptedEdge(ctx, "KNOWS", knowsSchema); err != nil {
			store.Close()
			t.Fatalf("session 1 register: %v", err)
		}
		store.Close()
	}

	// Session 2: open same DB — adapted spec must be loaded from t<X>_e_sch.
	store2, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type: "sqlite", DBPath: dbPath, GraphEnabled: true,
	})
	if err != nil {
		t.Fatalf("session 2 open: %v", err)
	}
	defer store2.Close()

	aes2 := asAdaptedEdgeStore(t, store2)
	if !aes2.IsEdgeAdapted("KNOWS") {
		t.Error("startup persistence failed: KNOWS not in adapted registry after reopen")
	}

	// AddEdgeWithProps must route to adapted table without re-registering.
	eps2 := asEdgeStore(t, store2)
	edgeID, err := eps2.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS",
		map[string]interface{}{"since": 2023})
	if err != nil {
		t.Fatalf("session 2 AddEdgeWithProps: %v", err)
	}

	db := openRawEdge(t, store2)
	var n int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM t0000_edata_KNOWS WHERE id = ?", edgeID,
	).Scan(&n)
	if n != 1 {
		t.Errorf("expected row in t0000_edata_KNOWS after session 2, got %d", n)
	}
}

func TestAdaptedEdge_IdempotentRegister(t *testing.T) {
	store, cleanup := setupAdaptedEdgeStore(t)
	defer cleanup()
	aes := asAdaptedEdgeStore(t, store)
	ctx := context.Background()

	if err := aes.RegisterAdaptedEdge(ctx, "KNOWS", knowsSchema); err != nil {
		t.Fatalf("first register: %v", err)
	}
	// Second call with same schema must not error.
	if err := aes.RegisterAdaptedEdge(ctx, "KNOWS", knowsSchema); err != nil {
		t.Fatalf("second register: %v", err)
	}
	if !aes.IsEdgeAdapted("KNOWS") {
		t.Error("not adapted after idempotent re-registration")
	}
}
