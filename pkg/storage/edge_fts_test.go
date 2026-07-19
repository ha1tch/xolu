// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

// ---------------------------------------------------------------------------
// Stage 8 — Edge FTS tests
//
// Covers:
//   - t<X>_efts DDL created at store open
//   - IndexEdgeContent: writes row to t<X>_efts
//   - IndexEdgeContent: idempotent (second call updates, no duplicate)
//   - SearchEdges: finds indexed content
//   - SearchEdges: returns empty on no match
//   - SearchEdges: limit respected
//   - AddEdgeWithProps: automatically indexes content (integration)
//   - extractEdgeContent: string, numeric, boolean, nil values
// ---------------------------------------------------------------------------

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/storage"
)

func setupEdgeFTSStore(t *testing.T) (storage.Store, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "xolu-edge-fts-*.db")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	f.Close()
	dbPath := f.Name()

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

func asEdgeFTSStore(t *testing.T, store storage.Store) storage.EdgeFTSStore {
	t.Helper()
	fts, ok := store.(storage.EdgeFTSStore)
	if !ok {
		t.Skip("store does not implement EdgeFTSStore")
	}
	return fts
}

func rawEdgeFTSDB(t *testing.T, store storage.Store) *sql.DB {
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

func TestEdgeFTS_DDL(t *testing.T) {
	store, cleanup := setupEdgeFTSStore(t)
	defer cleanup()
	db := rawEdgeFTSDB(t, store)
	ctx := context.Background()

	var n int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='t0000_efts'",
	).Scan(&n)
	if err != nil || n != 1 {
		t.Errorf("t0000_efts not created: n=%d err=%v", n, err)
	}
}

func TestEdgeFTS_IndexAndSearch(t *testing.T) {
	store, cleanup := setupEdgeFTSStore(t)
	defer cleanup()
	fts := asEdgeFTSStore(t, store)
	ctx := context.Background()

	props := map[string]interface{}{
		"since":  2020,
		"note":   "they met at a conference",
		"weight": 0.9,
		"active": true,
	}

	if err := fts.IndexEdgeContent(ctx, "KNOWS", 42, props); err != nil {
		t.Fatalf("IndexEdgeContent: %v", err)
	}

	// Full-text search for a word that appears in the content.
	results, err := fts.SearchEdges(ctx, "conference", 0)
	if err != nil {
		t.Fatalf("SearchEdges: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchEdges: expected 1 result, got %d", len(results))
	}
	if results[0].Rel != "KNOWS" {
		t.Errorf("Rel = %q, want KNOWS", results[0].Rel)
	}
	if results[0].EdgeID != 42 {
		t.Errorf("EdgeID = %d, want 42", results[0].EdgeID)
	}
}

func TestEdgeFTS_SearchNoMatch(t *testing.T) {
	store, cleanup := setupEdgeFTSStore(t)
	defer cleanup()
	fts := asEdgeFTSStore(t, store)
	ctx := context.Background()

	if err := fts.IndexEdgeContent(ctx, "KNOWS", 1, map[string]interface{}{"note": "hello"}); err != nil {
		t.Fatalf("IndexEdgeContent: %v", err)
	}

	results, err := fts.SearchEdges(ctx, "nomatch", 0)
	if err != nil {
		t.Fatalf("SearchEdges: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for non-matching query, got %d", len(results))
	}
}

func TestEdgeFTS_IdempotentIndex(t *testing.T) {
	store, cleanup := setupEdgeFTSStore(t)
	defer cleanup()
	fts := asEdgeFTSStore(t, store)
	db := rawEdgeFTSDB(t, store)
	ctx := context.Background()

	props := map[string]interface{}{"note": "original"}
	if err := fts.IndexEdgeContent(ctx, "KNOWS", 7, props); err != nil {
		t.Fatalf("first index: %v", err)
	}

	// Re-index the same (rel, edge_id) with different content.
	props2 := map[string]interface{}{"note": "updated"}
	if err := fts.IndexEdgeContent(ctx, "KNOWS", 7, props2); err != nil {
		t.Fatalf("second index: %v", err)
	}

	// Only one row must exist for (KNOWS, 7).
	var n int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM t0000_efts WHERE rel='KNOWS' AND edge_id=7",
	).Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 FTS row for (KNOWS, 7) after re-index, got %d", n)
	}

	// Old content must not be findable; new content must be.
	r1, _ := fts.SearchEdges(ctx, "original", 0)
	if len(r1) != 0 {
		t.Error("old content still searchable after re-index")
	}
	r2, _ := fts.SearchEdges(ctx, "updated", 0)
	if len(r2) != 1 {
		t.Errorf("new content not found after re-index: got %d results", len(r2))
	}
}

func TestEdgeFTS_Limit(t *testing.T) {
	store, cleanup := setupEdgeFTSStore(t)
	defer cleanup()
	fts := asEdgeFTSStore(t, store)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		if err := fts.IndexEdgeContent(ctx, "KNOWS", i,
			map[string]interface{}{"note": "shared keyword here"}); err != nil {
			t.Fatalf("IndexEdgeContent %d: %v", i, err)
		}
	}

	results, err := fts.SearchEdges(ctx, "shared", 3)
	if err != nil {
		t.Fatalf("SearchEdges with limit: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("limit=3: expected 3 results, got %d", len(results))
	}
}

func TestEdgeFTS_NumericAndBoolContent(t *testing.T) {
	store, cleanup := setupEdgeFTSStore(t)
	defer cleanup()
	fts := asEdgeFTSStore(t, store)
	ctx := context.Background()

	props := map[string]interface{}{
		"year":   2022,
		"active": true,
	}
	if err := fts.IndexEdgeContent(ctx, "FOLLOWS", 99, props); err != nil {
		t.Fatalf("IndexEdgeContent: %v", err)
	}

	// Numeric value should be searchable as string.
	r, _ := fts.SearchEdges(ctx, "2022", 0)
	if len(r) != 1 {
		t.Errorf("numeric value not indexed: expected 1, got %d", len(r))
	}

	// Boolean should be searchable as "true".
	r2, _ := fts.SearchEdges(ctx, "true", 0)
	if len(r2) != 1 {
		t.Errorf("boolean value not indexed: expected 1, got %d", len(r2))
	}
}

func TestEdgeFTS_IntegrationWithAddEdgeWithProps(t *testing.T) {
	// When AddEdgeWithProps is called with non-nil props, the FTS index
	// must be updated automatically.
	store, cleanup := setupEdgeFTSStore(t)
	defer cleanup()
	fts := asEdgeFTSStore(t, store)
	eps := asEdgeStore(t, store)
	ctx := context.Background()

	edgeID, err := eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS",
		map[string]interface{}{"note": "automatic FTS indexing"})
	if err != nil {
		t.Fatalf("AddEdgeWithProps: %v", err)
	}
	if edgeID == 0 {
		t.Fatal("expected non-zero edgeID")
	}

	results, err := fts.SearchEdges(ctx, "automatic", 0)
	if err != nil {
		t.Fatalf("SearchEdges: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("FTS not updated by AddEdgeWithProps: expected 1 result, got %d", len(results))
	}
	if results[0].EdgeID != edgeID {
		t.Errorf("EdgeID = %d, want %d", results[0].EdgeID, edgeID)
	}
}

func TestEdgeFTS_MultipleLabels(t *testing.T) {
	store, cleanup := setupEdgeFTSStore(t)
	defer cleanup()
	fts := asEdgeFTSStore(t, store)
	ctx := context.Background()

	if err := fts.IndexEdgeContent(ctx, "KNOWS", 1, map[string]interface{}{"note": "alpha"}); err != nil {
		t.Fatalf("index KNOWS: %v", err)
	}
	if err := fts.IndexEdgeContent(ctx, "FOLLOWS", 2, map[string]interface{}{"note": "alpha beta"}); err != nil {
		t.Fatalf("index FOLLOWS: %v", err)
	}

	// "alpha" matches both labels.
	r, _ := fts.SearchEdges(ctx, "alpha", 0)
	if len(r) != 2 {
		t.Errorf("expected 2 results for 'alpha', got %d", len(r))
	}

	// "beta" matches only FOLLOWS.
	r2, _ := fts.SearchEdges(ctx, "beta", 0)
	if len(r2) != 1 || r2[0].Rel != "FOLLOWS" {
		t.Errorf("expected 1 FOLLOWS result for 'beta', got %v", r2)
	}
}
