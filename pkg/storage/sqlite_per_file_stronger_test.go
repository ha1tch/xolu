// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/ha1tch/xolu/pkg/tenant"
	"path/filepath"
	"strings"
	"testing"
)

// newPerFileStoreWithGraph creates a per-file store with graph enabled.
func newPerFileStoreWithGraph(t *testing.T, dbPath string, tenantID tenant.TenantID) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(dbPath, SQLiteConfig{
		DBPath:            dbPath,
		EnableWAL:         true,
		EnableForeignKeys: true,
		CacheSize:         2000,
		BusyTimeout:       5000,
		FullTextEnabled:   false,
		GraphEnabled:      true,
		TenantID:          tenantID,
		PerFileTenants:    true,
	})
	if err != nil {
		t.Fatalf("newPerFileStoreWithGraph(%q, tenant=%d): %v", dbPath, tenantID, err)
	}
	return store
}

// ---------------------------------------------------------------------------
// Test 2: FTS delete isolation — per-file mode
// ---------------------------------------------------------------------------

// TestPerFile_FTSDeleteIsolation verifies that deleting an entity in store A
// removes only that store's FTS entry — store B's FTS index is untouched.
// This is the per-file equivalent of TestSQLiteTenantIsolation_FTSUpdateDelete,
// and directly covers the deleteInner FTS path that caused the patched96 regression.
func TestPerFile_FTSDeleteIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	storeA := newPerFileStore(t, filepath.Join(dir, "a.db"), 1)
	storeB := newPerFileStore(t, filepath.Join(dir, "b.db"), 2)
	defer storeA.Close()
	defer storeB.Close()

	// Both stores create two "original" documents
	storeA.Create(ctx, "doc", map[string]interface{}{"body": "original alpha one"})
	storeA.Create(ctx, "doc", map[string]interface{}{"body": "original alpha two"})
	storeB.Create(ctx, "doc", map[string]interface{}{"body": "original beta one"})
	storeB.Create(ctx, "doc", map[string]interface{}{"body": "original beta two"})

	// Baseline
	ftsA, err := storeA.FullTextSearch(ctx, "original", "doc")
	if err != nil {
		t.Fatalf("baseline A FTS: %v", err)
	}
	ftsB, err := storeB.FullTextSearch(ctx, "original", "doc")
	if err != nil {
		t.Fatalf("baseline B FTS: %v", err)
	}
	if len(ftsA) != 2 {
		t.Fatalf("baseline: A FTS 'original' = %d, want 2", len(ftsA))
	}
	if len(ftsB) != 2 {
		t.Fatalf("baseline: B FTS 'original' = %d, want 2", len(ftsB))
	}

	// Update A doc 1: replace "original" with "revised"
	if err := storeA.Update(ctx, "doc", 1, map[string]interface{}{
		"id": 1, "body": "revised alpha one",
	}); err != nil {
		t.Fatalf("A Update: %v", err)
	}

	// A: "original" → 1, "revised" → 1
	ftsA, _ = storeA.FullTextSearch(ctx, "original", "doc")
	if len(ftsA) != 1 {
		t.Errorf("after A update: A FTS 'original' = %d, want 1", len(ftsA))
	}
	ftsARevised, _ := storeA.FullTextSearch(ctx, "revised", "doc")
	if len(ftsARevised) != 1 {
		t.Errorf("after A update: A FTS 'revised' = %d, want 1", len(ftsARevised))
	}

	// B: still 2 "original", no "revised"
	ftsB, _ = storeB.FullTextSearch(ctx, "original", "doc")
	if len(ftsB) != 2 {
		t.Errorf("after A update: B FTS 'original' = %d, want 2 (isolation broken)", len(ftsB))
	}
	ftsBRevised, _ := storeB.FullTextSearch(ctx, "revised", "doc")
	if len(ftsBRevised) != 0 {
		t.Errorf("after A update: B FTS 'revised' = %d, want 0", len(ftsBRevised))
	}

	// Delete A doc 2
	if err := storeA.Delete(ctx, "doc", 2); err != nil {
		t.Fatalf("A Delete: %v", err)
	}

	// A: "original" → 0
	ftsA, _ = storeA.FullTextSearch(ctx, "original", "doc")
	if len(ftsA) != 0 {
		t.Errorf("after A delete: A FTS 'original' = %d, want 0", len(ftsA))
	}

	// B: still 2 — the delete in A must not touch B's separate db file
	ftsB, _ = storeB.FullTextSearch(ctx, "original", "doc")
	if len(ftsB) != 2 {
		t.Errorf("after A delete: B FTS 'original' = %d, want 2 (isolation broken)", len(ftsB))
	}
}

// ---------------------------------------------------------------------------
// Test 3: Adapted CRUD round-trip — all four mutations
// ---------------------------------------------------------------------------

// TestPerFile_AdaptedCRUDRoundTrip verifies Create, Get, Update, Delete, and List
// on an adapted table in per-file mode, exercising all dialectIsPerFile() branches
// in adapted_crud.go (Update passes id only; Delete passes id only; List needs no args).
func TestPerFile_AdaptedCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "adapted_crud.db"), 1)
	defer store.Close()

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"sku":   map[string]interface{}{"type": "string"},
			"stock": map[string]interface{}{"type": "integer"},
			"note":  map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"sku"},
	}
	if err := store.RegisterAdaptedEntity(ctx, "part", schema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}

	// Create
	id, err := store.Create(ctx, "part", map[string]interface{}{
		"sku": "P-001", "stock": 100, "note": "initial",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get
	rec, err := store.Get(ctx, "part", id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec["sku"] != "P-001" {
		t.Errorf("Get: sku = %v, want P-001", rec["sku"])
	}

	// Update — exercises adaptedUpdate with dialectIsPerFile args (id only, not tenantID+id)
	if err := store.Update(ctx, "part", id, map[string]interface{}{
		"id": id, "sku": "P-001", "stock": 200, "note": "updated",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rec, err = store.Get(ctx, "part", id)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if fmt.Sprintf("%v", rec["stock"]) != "200" {
		t.Errorf("Get after Update: stock = %v, want 200", rec["stock"])
	}

	// Create a second record
	id2, err := store.Create(ctx, "part", map[string]interface{}{
		"sku": "P-002", "stock": 50,
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	// List — exercises adaptedList with no args in per-file mode (no tenant_id param)
	list, err := store.List(ctx, "part")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("List: want 2 records, got %d", len(list))
	}

	// Delete — exercises adaptedDelete with dialectIsPerFile args (id only)
	if err := store.Delete(ctx, "part", id2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, _ = store.List(ctx, "part")
	if len(list) != 1 {
		t.Errorf("List after Delete: want 1 record, got %d", len(list))
	}
	if _, err := store.Get(ctx, "part", id2); err != ErrNotFound {
		t.Errorf("Get deleted: want ErrNotFound, got %v", err)
	}

	// Patch — exercises adaptedGetInTx + adaptedUpdate via patchInner
	if err := store.Patch(ctx, "part", id, map[string]interface{}{"note": "patched"}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	rec, _ = store.Get(ctx, "part", id)
	if rec["note"] != "patched" {
		t.Errorf("Get after Patch: note = %v, want patched", rec["note"])
	}
}

// ---------------------------------------------------------------------------
// Test 4: Commit round-trip — saveInTx and createInTx branches
// ---------------------------------------------------------------------------

// TestPerFile_CommitRoundTrip exercises the Commit transactional batch in
// per-file mode, covering saveInTx (upsert) and createInTx (append) paths.
func TestPerFile_CommitRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "commit.db"), 1)
	defer store.Close()

	// Append two records via Commit (exercises createInTx auto-ID path)
	result, err := store.Commit(ctx, CommitRequest{
		Append: []CommitAppend{
			{Entity: "line", Data: map[string]interface{}{"val": "A"}},
			{Entity: "line", Data: map[string]interface{}{"val": "B"}},
		},
	})
	if err != nil {
		t.Fatalf("Commit(append): %v", err)
	}
	if len(result.Appended) != 2 {
		t.Fatalf("Commit(append): want 2 appended, got %d", len(result.Appended))
	}
	idA := result.Appended[0].ID
	idB := result.Appended[1].ID
	if idA == 0 || idB == 0 || idA == idB {
		t.Errorf("Commit(append): unexpected IDs: %d, %d", idA, idB)
	}

	// Verify records exist
	recA, err := store.Get(ctx, "line", idA)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	if recA["val"] != "A" {
		t.Errorf("Get A: val = %v, want A", recA["val"])
	}

	// Upsert via Commit (exercises saveInTx: exists → update path)
	result2, err := store.Commit(ctx, CommitRequest{
		Update: CommitUpdate{
			Entity: "line",
			ID:     idA,
			Data:   map[string]interface{}{"id": idA, "val": "A-updated"},
		},
	})
	if err != nil {
		t.Fatalf("Commit(update): %v", err)
	}
	if result2.Update.Created {
		t.Error("Commit(update): want Created=false for existing record")
	}
	if result2.Update.Version < 2 {
		t.Errorf("Commit(update): want Version >= 2, got %d", result2.Update.Version)
	}

	// Verify update applied
	recA, _ = store.Get(ctx, "line", idA)
	if recA["val"] != "A-updated" {
		t.Errorf("Get A after Commit(update): val = %v, want A-updated", recA["val"])
	}

	// Upsert a new record via Commit (exercises saveInTx: !exists → insert path)
	newID := idB + 100
	result3, err := store.Commit(ctx, CommitRequest{
		Update: CommitUpdate{
			Entity: "line",
			ID:     newID,
			Data:   map[string]interface{}{"id": newID, "val": "C-new"},
		},
	})
	if err != nil {
		t.Fatalf("Commit(insert-via-update): %v", err)
	}
	if !result3.Update.Created {
		t.Error("Commit(insert-via-update): want Created=true for new record")
	}

	// Append with explicit ID (exercises createInTx explicit-ID path)
	explicitID := newID + 50
	result4, err := store.Commit(ctx, CommitRequest{
		Append: []CommitAppend{
			{Entity: "line", ID: &explicitID, Data: map[string]interface{}{"val": "D-explicit"}},
		},
	})
	if err != nil {
		t.Fatalf("Commit(append explicit ID): %v", err)
	}
	if result4.Appended[0].ID != explicitID {
		t.Errorf("Commit(append explicit ID): want %d, got %d", explicitID, result4.Appended[0].ID)
	}
}

// ---------------------------------------------------------------------------
// Test 6: Sequence PRIMARY KEY constraint
// ---------------------------------------------------------------------------

// TestPerFile_SequencePrimaryKeyConstraint verifies the per-file schema's
// composite PRIMARY KEY (entity_type, id) semantics:
//   - Same (entity_type, id) → constraint violation
//   - Different entity_type, same id → no conflict
//   - Different id, same entity_type → no conflict
func TestPerFile_SequencePrimaryKeyConstraint(t *testing.T) {
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "pk.db"), 1)
	defer store.Close()

	db := store.DB()
	ctx := context.Background()

	insert := func(entityType string, id int) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO `+tenant.TenantID(1).NodesTableName()+` (entity_type, id, data, _version) VALUES (?, ?, ?, 1)`,
			entityType, id, fmt.Sprintf(`{"id":%d}`, id))
		return err
	}

	// First insert — must succeed
	if err := insert("widget", 1); err != nil {
		t.Fatalf("first insert (widget,1): %v", err)
	}

	// Duplicate (entity_type, id) — must fail
	err := insert("widget", 1)
	if err == nil {
		t.Error("duplicate (widget,1): expected UNIQUE constraint failure, got nil")
	} else if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Errorf("duplicate (widget,1): unexpected error: %v", err)
	}

	// Same entity_type, different id — must succeed
	if err := insert("widget", 2); err != nil {
		t.Errorf("(widget,2): unexpected error: %v", err)
	}

	// Different entity_type, same id — must succeed (not a PK conflict)
	if err := insert("gadget", 1); err != nil {
		t.Errorf("(gadget,1): unexpected error: %v", err)
	}

	// Verify three distinct rows exist
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tenant.TenantID(1).NodesTableName()+``).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("row count: want 3, got %d", count)
	}

	// Also verify `+tenant.TenantID(1).NodeSeqTableName()+` has no tenant_id column via a raw insert
	_, err = db.ExecContext(ctx,
		`INSERT INTO `+tenant.TenantID(1).NodeSeqTableName()+` (entity_type, next_id) VALUES (?, ?)`,
		"probe", 1)
	if err != nil {
		t.Errorf(tenant.TenantID(1).NodeSeqTableName()+" insert without tenant_id: %v", err)
	}
	// Duplicate primary key in sequences table should also fail
	_, err = db.ExecContext(ctx,
		`INSERT INTO `+tenant.TenantID(1).NodeSeqTableName()+` (entity_type, next_id) VALUES (?, ?)`,
		"probe", 2)
	if err == nil {
		t.Error("duplicate " + tenant.TenantID(1).NodeSeqTableName() + " PK: expected failure, got nil")
	}
}

// ---------------------------------------------------------------------------
// Test 7: VerifyGraphIntegrity + RebuildGraph in per-file mode
// ---------------------------------------------------------------------------

// TestPerFile_GraphIntegrityAndRebuild verifies that VerifyGraphIntegrity and
// RebuildGraph operate correctly in per-file mode, where the entity scan uses
// WHERE 1=1 (no tenant_id filter). Both methods would silently return zero
// edges if the entity scan SQL were broken, so we assert on edge count too.
func TestPerFile_GraphIntegrityAndRebuild(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Use tenantID=1 so the graph table is t0001_graph
	store := newPerFileStoreWithGraph(t, filepath.Join(dir, "graph.db"), 1)
	defer store.Close()

	// Create a target entity
	projID, err := store.Create(ctx, "project", map[string]interface{}{"name": "Alpha"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}

	// Create entities that REF the project — this writes graph edges
	for i := 0; i < 3; i++ {
		if _, err := store.Create(ctx, "task", map[string]interface{}{
			"title": fmt.Sprintf("Task %d", i),
			"project": map[string]interface{}{
				"type": "REF", "entity": "project", "id": projID,
			},
		}); err != nil {
			t.Fatalf("Create task[%d]: %v", i, err)
		}
	}

	// VerifyGraphIntegrity: should find no discrepancies
	if err := store.VerifyGraphIntegrity(ctx); err != nil {
		t.Errorf("VerifyGraphIntegrity: unexpected error: %v", err)
	}

	// Count edges via ScanGraphEdges
	edgeCount := 0
	if err := store.ScanGraphEdges(ctx, 1, func(_ GraphEdge) error {
		edgeCount++
		return nil
	}); err != nil {
		t.Fatalf("ScanGraphEdges: %v", err)
	}
	if edgeCount != 3 {
		t.Errorf("ScanGraphEdges: want 3 edges (task→project), got %d", edgeCount)
	}

	// Delete all edges manually then RebuildGraph — should restore all 3
	db := store.DB()
	tbl := "t0001_graph"
	if _, err := db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", tbl)); err != nil {
		t.Fatalf("DELETE graph edges: %v", err)
	}
	zeroCheck := 0
	store.ScanGraphEdges(ctx, 1, func(_ GraphEdge) error { zeroCheck++; return nil })
	if zeroCheck != 0 {
		t.Fatalf("expected 0 edges after manual delete, got %d", zeroCheck)
	}

	// RebuildGraph: entity scan uses WHERE 1=1 in per-file mode
	if err := store.RebuildGraph(ctx); err != nil {
		t.Fatalf("RebuildGraph: %v", err)
	}

	// Edges should be restored
	rebuiltCount := 0
	if err := store.ScanGraphEdges(ctx, 1, func(_ GraphEdge) error {
		rebuiltCount++
		return nil
	}); err != nil {
		t.Fatalf("ScanGraphEdges after rebuild: %v", err)
	}
	if rebuiltCount != 3 {
		t.Errorf("RebuildGraph: want 3 edges restored, got %d", rebuiltCount)
	}

	// VerifyGraphIntegrity after rebuild: should still be clean
	if err := store.VerifyGraphIntegrity(ctx); err != nil {
		t.Errorf("VerifyGraphIntegrity after rebuild: %v", err)
	}

	// Cross-contamination check: a second store at a different path should have
	// zero edges (its graph table is independent)
	store2 := newPerFileStoreWithGraph(t, filepath.Join(dir, "graph2.db"), 1)
	defer store2.Close()
	crossCount := 0
	store2.ScanGraphEdges(ctx, 1, func(_ GraphEdge) error { crossCount++; return nil })
	if crossCount != 0 {
		t.Errorf("cross-contamination: store2 has %d edges, want 0", crossCount)
	}
}

// ---------------------------------------------------------------------------
// Bonus: NULL column in adapted table (HasExtra path) in per-file mode
// ---------------------------------------------------------------------------

// TestPerFile_AdaptedHasExtra verifies that adapted tables with overflow
// columns (_extra) work in per-file mode. InsertSQL omits tenant_id and
// the extra column is correctly round-tripped.
func TestPerFile_AdaptedHasExtra(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "extra.db"), 1)
	defer store.Close()

	// Schema with few mapped columns — extra fields go into _extra JSON blob
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"code": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"code"},
	}
	if err := store.RegisterAdaptedEntity(ctx, "tag", schema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}

	// Create with extra fields beyond the schema
	id, err := store.Create(ctx, "tag", map[string]interface{}{
		"code": "T1", "description": "overflow field", "rank": 42,
	})
	if err != nil {
		t.Fatalf("Create with extras: %v", err)
	}

	rec, err := store.Get(ctx, "tag", id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec["code"] != "T1" {
		t.Errorf("Get: code = %v, want T1", rec["code"])
	}

	// Verify adapted table has no tenant_id column
	var tenantColCount int
	rows, err := store.DB().QueryContext(ctx, "PRAGMA table_info(t0001_ndata_tag)")
	if err != nil {
		t.Fatalf("PRAGMA: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt sql.NullString
		var pk int
		rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk)
		if name == "tenant_id" {
			tenantColCount++
		}
	}
	if tenantColCount > 0 {
		t.Error("t0001_ndata_tag: unexpected tenant_id column in per-file adapted table")
	}
}
