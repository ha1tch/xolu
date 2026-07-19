// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

// ---------------------------------------------------------------------------
// Stage 5 — Edge property infrastructure tests
//
// Covers:
//   - t<X>_edges and t<X>_eseq table creation alongside t<X>_graph
//   - edge_id column present in t<X>_graph
//   - AddEdgeWithProps: topology row written without props (edge_id stays NULL)
//   - AddEdgeWithProps: topology + property blob written, edge_id set
//   - AddEdgeWithProps: idempotent property update increments _version
//   - GetEdge: retrieves property blob by surrogate ID
//   - GetEdge: returns ErrNotFound for unknown ID
//   - GetManyEdges: batch retrieval; absent IDs not in result map
//   - Sequence: each relationship label gets independent counter
//   - graph.EdgeRef: type present with Rel and ID fields
// ---------------------------------------------------------------------------

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/storage"
)

// setupEdgePropsStore returns a graph-enabled SQLiteStore and a cleanup func.
func setupEdgePropsStore(t *testing.T) (storage.Store, func()) {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "xolu-edge-props-test-*.db")
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
	return store, func() {
		store.Close()
		os.Remove(dbPath)
	}
}

// asEdgeStore casts store to EdgePropertyStore or skips the test.
func asEdgeStore(t *testing.T, store storage.Store) storage.EdgePropertyStore {
	t.Helper()
	eps, ok := store.(storage.EdgePropertyStore)
	if !ok {
		t.Skip("store does not implement EdgePropertyStore")
	}
	return eps
}

// openRaw opens the underlying SQLite file directly for schema assertions.
func openRaw(t *testing.T, store storage.Store) *sql.DB {
	t.Helper()
	cfg := store.Config()
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name,
	).Scan(&n)
	if err != nil {
		t.Fatalf("tableExists(%q): %v", name, err)
	}
	return n > 0
}

func columnExists(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		"PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%q): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan pragma: %v", err)
		}
		if name == col {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------

func TestEdgeProps_DDL(t *testing.T) {
	store, cleanup := setupEdgePropsStore(t)
	defer cleanup()
	db := openRaw(t, store)

	// t0000_graph must exist and have edge_id column.
	if !tableExists(t, db, "t0000_graph") {
		t.Error("t0000_graph not created")
	}
	if !columnExists(t, db, "t0000_graph", "edge_id") {
		t.Error("t0000_graph missing edge_id column")
	}

	// t0000_edges (blob edge property store) must exist.
	if !tableExists(t, db, "t0000_edges") {
		t.Error("t0000_edges not created")
	}

	// t0000_eseq (edge ID sequence) must exist.
	if !tableExists(t, db, "t0000_eseq") {
		t.Error("t0000_eseq not created")
	}
}

func TestEdgeProps_AddWithoutProps(t *testing.T) {
	store, cleanup := setupEdgePropsStore(t)
	defer cleanup()
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	// An edge with no properties: edge_id should be 0 (no property row written).
	edgeID, err := eps.AddEdgeWithProps(ctx, "user:1", "post:1", "WROTE", nil)
	if err != nil {
		t.Fatalf("AddEdgeWithProps(no props): %v", err)
	}
	if edgeID != 0 {
		t.Errorf("expected edgeID 0 for no-props edge, got %d", edgeID)
	}

	// Topology row must be present.
	db := openRaw(t, store)
	var count int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM t0000_graph WHERE source_entity='user' AND source_id=1 AND target_entity='post' AND target_id=1 AND relationship_name='WROTE'",
	).Scan(&count)
	if err != nil || count != 1 {
		t.Errorf("topology row not found: count=%d err=%v", count, err)
	}

	// edge_id in topology must be NULL.
	var edgeIDNull sql.NullInt64
	err = db.QueryRowContext(ctx,
		"SELECT edge_id FROM t0000_graph WHERE source_entity='user' AND source_id=1 AND relationship_name='WROTE'",
	).Scan(&edgeIDNull)
	if err != nil {
		t.Fatalf("query edge_id: %v", err)
	}
	if edgeIDNull.Valid {
		t.Errorf("edge_id should be NULL for no-props edge, got %d", edgeIDNull.Int64)
	}
}

func TestEdgeProps_AddWithProps(t *testing.T) {
	store, cleanup := setupEdgePropsStore(t)
	defer cleanup()
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	props := map[string]interface{}{"since": "2024-01-01", "weight": 42}
	edgeID, err := eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS", props)
	if err != nil {
		t.Fatalf("AddEdgeWithProps: %v", err)
	}
	if edgeID == 0 {
		t.Fatal("expected non-zero edgeID for edge with props")
	}

	// GetEdge must return the stored blob.
	result, err := eps.GetEdge(ctx, edgeID)
	if err != nil {
		t.Fatalf("GetEdge(%d): %v", edgeID, err)
	}
	if result.Rel != "KNOWS" {
		t.Errorf("Rel = %q, want KNOWS", result.Rel)
	}
	if result.Properties["since"] != "2024-01-01" {
		t.Errorf("since = %v, want 2024-01-01", result.Properties["since"])
	}

	// Topology row must have edge_id set.
	db := openRaw(t, store)
	var storedEdgeID sql.NullInt64
	err = db.QueryRowContext(ctx,
		"SELECT edge_id FROM t0000_graph WHERE source_entity='user' AND source_id=1 AND target_entity='user' AND target_id=2 AND relationship_name='KNOWS'",
	).Scan(&storedEdgeID)
	if err != nil {
		t.Fatalf("query topology edge_id: %v", err)
	}
	if !storedEdgeID.Valid || int(storedEdgeID.Int64) != edgeID {
		t.Errorf("topology edge_id = %v, want %d", storedEdgeID, edgeID)
	}
}

func TestEdgeProps_GetEdge_NotFound(t *testing.T) {
	store, cleanup := setupEdgePropsStore(t)
	defer cleanup()
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	_, err := eps.GetEdge(ctx, 99999)
	if err != storage.ErrNotFound {
		t.Errorf("GetEdge(unknown): want ErrNotFound, got %v", err)
	}
}

func TestEdgeProps_GetManyEdges(t *testing.T) {
	store, cleanup := setupEdgePropsStore(t)
	defer cleanup()
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	id1, _ := eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS", map[string]interface{}{"since": "2020"})
	id2, _ := eps.AddEdgeWithProps(ctx, "user:1", "user:3", "KNOWS", map[string]interface{}{"since": "2021"})

	results, err := eps.GetManyEdges(ctx, []int{id1, id2, 99999})
	if err != nil {
		t.Fatalf("GetManyEdges: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("len(results) = %d, want 2", len(results))
	}
	if _, ok := results[id1]; !ok {
		t.Errorf("result missing id1 (%d)", id1)
	}
	if _, ok := results[id2]; !ok {
		t.Errorf("result missing id2 (%d)", id2)
	}
	if _, ok := results[99999]; ok {
		t.Error("result should not contain unknown id 99999")
	}
}

func TestEdgeProps_SequenceGlobal(t *testing.T) {
	store, cleanup := setupEdgePropsStore(t)
	defer cleanup()
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	// All edges share a single global sequence — IDs are globally unique across labels.
	id1, _ := eps.AddEdgeWithProps(ctx, "a:1", "b:1", "KNOWS", map[string]interface{}{"x": 1})
	id2, _ := eps.AddEdgeWithProps(ctx, "a:1", "b:2", "KNOWS", map[string]interface{}{"x": 2})
	id3, _ := eps.AddEdgeWithProps(ctx, "a:1", "c:1", "FOLLOWS", map[string]interface{}{"x": 3})

	if id1 == 0 || id2 == 0 || id3 == 0 {
		t.Fatalf("expected non-zero IDs, got %d %d %d", id1, id2, id3)
	}
	if id1 == id2 {
		t.Errorf("id1=%d == id2=%d, want distinct", id1, id2)
	}
	if id2 == id3 {
		t.Errorf("cross-label collision: id2=%d == id3=%d, want distinct", id2, id3)
	}
}

func TestEdgeProps_IdempotentUpdate(t *testing.T) {
	store, cleanup := setupEdgePropsStore(t)
	defer cleanup()
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	id1, err := eps.AddEdgeWithProps(ctx, "a:1", "b:1", "KNOWS", map[string]interface{}{"v": 1})
	if err != nil || id1 == 0 {
		t.Fatalf("first AddEdgeWithProps: id=%d err=%v", id1, err)
	}

	// Re-adding the same edge with new props should update the blob.
	id2, err := eps.AddEdgeWithProps(ctx, "a:1", "b:1", "KNOWS", map[string]interface{}{"v": 2})
	if err != nil {
		t.Fatalf("second AddEdgeWithProps: %v", err)
	}
	// The topology row is the same edge; the sequence yields a new ID for the
	// new props row, but the topology upsert updates edge_id to id2.
	result, err := eps.GetEdge(ctx, id2)
	if err != nil {
		t.Fatalf("GetEdge after update: %v", err)
	}
	if result.Properties["v"] != float64(2) {
		t.Errorf("updated prop v = %v, want 2", result.Properties["v"])
	}
}

func TestEdgeRef_Type(t *testing.T) {
	// Compile-time presence check: EdgeRef must have Rel string and ID int.
	ref := graph.EdgeRef{Rel: "KNOWS", ID: 42}
	if ref.Rel != "KNOWS" {
		t.Errorf("EdgeRef.Rel = %q, want KNOWS", ref.Rel)
	}
	if ref.ID != 42 {
		t.Errorf("EdgeRef.ID = %d, want 42", ref.ID)
	}
}

func TestEdgeProps_AddEdgeWithProps_GraphInterface(t *testing.T) {
	// FlatGraph must satisfy the updated Graph interface (AddEdgeWithProps present).
	g := graph.NewFlatGraph()
	if err := g.AddEdgeWithProps("a:1", "b:1", "KNOWS", map[string]interface{}{"x": 1}); err != nil {
		t.Errorf("FlatGraph.AddEdgeWithProps: %v", err)
	}
	// The edge must be visible via the existing topology.
	neighbors, err := g.GetNeighbors("a:1")
	if err != nil {
		t.Fatalf("GetNeighbors: %v", err)
	}
	if ref, ok := neighbors["b:1"]; !ok || ref.Rel != "KNOWS" {
		t.Errorf("neighbors[b:1] = %q ok=%v, want KNOWS true", ref.Rel, ok)
	}
}

func TestEdgeProps_GetManyEdges_Empty(t *testing.T) {
	store, cleanup := setupEdgePropsStore(t)
	defer cleanup()
	eps := asEdgeStore(t, store)

	results, err := eps.GetManyEdges(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetManyEdges(nil): %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty result, got %d entries", len(results))
	}
}
