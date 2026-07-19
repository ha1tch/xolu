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
	"testing"
)

// newPerFileStore creates a SQLiteStore in per-file tenant mode.
// Each call produces a store whose file lives at dbPath; the caller is
// responsible for using distinct paths when testing isolation.
func newPerFileStore(t *testing.T, dbPath string, tenantID uint16) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(dbPath, SQLiteConfig{
		DBPath:            dbPath,
		EnableWAL:         true,
		EnableForeignKeys: true,
		CacheSize:         2000,
		BusyTimeout:       5000,
		FullTextEnabled:   true,
		GraphEnabled:      false,
		TenantID:          tenantID,
		PerFileTenants:    true,
	})
	if err != nil {
		t.Fatalf("newPerFileStore(%q, tenant=%d): %v", dbPath, tenantID, err)
	}
	return store
}

// ---------------------------------------------------------------------------
// Schema verification
// ---------------------------------------------------------------------------

// TestPerFile_SchemaHasNoTenantIDColumn verifies that the entities,
// `+tenant.NodeSeqTableName(1)+`, and `+tenant.NodeFTSTableName(1)+` tables are created without a tenant_id
// column when PerFileTenants = true.
func TestPerFile_SchemaHasNoTenantIDColumn(t *testing.T) {
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "t0001.db"), 1)
	defer store.Close()

	db := store.DB()
	ctx := context.Background()

	tables := []string{tenant.NodesTableName(1), tenant.NodeSeqTableName(1)}
	for _, tbl := range tables {
		rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", tbl))
		if err != nil {
			t.Fatalf("PRAGMA table_info(%s): %v", tbl, err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull int
			var dfltValue sql.NullString
			var pk int
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if name == "tenant_id" {
				t.Errorf("table %s: unexpected tenant_id column in per-file schema", tbl)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows.Err: %v", err)
		}
	}

	// `+tenant.NodeFTSTableName(1)+`: check via fts5 content table (no PRAGMA table_info for virtual tables)
	// Insert a row and confirm it only has (entity_type, entity_id, content) columns.
	_, err := db.ExecContext(ctx,
		`INSERT INTO `+tenant.NodeFTSTableName(1)+` (entity_type, entity_id, content) VALUES (?, ?, ?)`,
		"probe", "1", "hello")
	if err != nil {
		t.Errorf(tenant.NodeFTSTableName(1)+" INSERT without tenant_id failed: %v", err)
	}

	// Also confirm entities PRIMARY KEY is (entity_type, id), not (tenant_id, entity_type, id).
	// A duplicate insert with the same (entity_type, id) should fail.
	_, err = db.ExecContext(ctx,
		`INSERT INTO `+tenant.NodesTableName(1)+` (entity_type, id, data, _version) VALUES (?, ?, ?, ?)`,
		"widget", 1, `{"id":1}`, 1)
	if err != nil {
		t.Fatalf("first entities INSERT failed: %v", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO `+tenant.NodesTableName(1)+` (entity_type, id, data, _version) VALUES (?, ?, ?, ?)`,
		"widget", 1, `{"id":1}`, 1)
	if err == nil {
		t.Error("duplicate (entity_type, id) INSERT should have failed under per-file PK, but did not")
	}
}

// ---------------------------------------------------------------------------
// CRUD round-trip
// ---------------------------------------------------------------------------

// TestPerFile_CRUDRoundTrip verifies Create/Get/Update/Delete in per-file mode.
func TestPerFile_CRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "crud.db"), 1)
	defer store.Close()

	// Create
	id, err := store.Create(ctx, "product", map[string]interface{}{
		"name": "Widget", "price": 9.99,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != 1 {
		t.Errorf("Create: want id=1, got %d", id)
	}

	// Get
	rec, err := store.Get(ctx, "product", id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec["name"] != "Widget" {
		t.Errorf("Get: name = %v, want Widget", rec["name"])
	}

	// Update
	err = store.Update(ctx, "product", id, map[string]interface{}{
		"id": id, "name": "Gadget", "price": 14.99,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	rec, err = store.Get(ctx, "product", id)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if rec["name"] != "Gadget" {
		t.Errorf("Get after Update: name = %v, want Gadget", rec["name"])
	}

	// Patch
	err = store.Patch(ctx, "product", id, map[string]interface{}{"name": "SuperGadget"})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	rec, err = store.Get(ctx, "product", id)
	if err != nil {
		t.Fatalf("Get after Patch: %v", err)
	}
	if rec["name"] != "SuperGadget" {
		t.Errorf("Get after Patch: name = %v, want SuperGadget", rec["name"])
	}

	// Delete
	err = store.Delete(ctx, "product", id)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = store.Get(ctx, "product", id)
	if err != ErrNotFound {
		t.Errorf("Get after Delete: want ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Two-store isolation
// ---------------------------------------------------------------------------

// TestPerFile_TwoStoreIsolation verifies that two stores at different paths
// do not share data — writes to one are invisible to the other.
func TestPerFile_TwoStoreIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	storeA := newPerFileStore(t, filepath.Join(dir, "tenant_a.db"), 1)
	storeB := newPerFileStore(t, filepath.Join(dir, "tenant_b.db"), 2)
	defer storeA.Close()
	defer storeB.Close()

	// Create in A
	idA, err := storeA.Create(ctx, "item", map[string]interface{}{"owner": "A"})
	if err != nil {
		t.Fatalf("storeA.Create: %v", err)
	}

	// A can see its own record
	recA, err := storeA.Get(ctx, "item", idA)
	if err != nil {
		t.Fatalf("storeA.Get: %v", err)
	}
	if recA["owner"] != "A" {
		t.Errorf("storeA.Get: owner = %v, want A", recA["owner"])
	}

	// B cannot see A's record — IDs start at 1 in each file
	_, err = storeB.Get(ctx, "item", idA)
	if err != ErrNotFound {
		t.Errorf("storeB.Get(A's id): want ErrNotFound, got %v", err)
	}

	// Create in B
	idB, err := storeB.Create(ctx, "item", map[string]interface{}{"owner": "B"})
	if err != nil {
		t.Fatalf("storeB.Create: %v", err)
	}

	// Both start at id=1 (independent sequences)
	if idA != 1 || idB != 1 {
		t.Errorf("expected both stores to start at id=1; got idA=%d idB=%d", idA, idB)
	}

	// List isolation: A sees 1 record, B sees 1 record
	listA, err := storeA.List(ctx, "item")
	if err != nil {
		t.Fatalf("storeA.List: %v", err)
	}
	if len(listA) != 1 {
		t.Errorf("storeA.List: want 1 item, got %d", len(listA))
	}

	listB, err := storeB.List(ctx, "item")
	if err != nil {
		t.Fatalf("storeB.List: %v", err)
	}
	if len(listB) != 1 {
		t.Errorf("storeB.List: want 1 item, got %d", len(listB))
	}

	// A's list contains A's record, not B's
	if listA[0]["owner"] != "A" {
		t.Errorf("storeA.List[0].owner = %v, want A", listA[0]["owner"])
	}
	if listB[0]["owner"] != "B" {
		t.Errorf("storeB.List[0].owner = %v, want B", listB[0]["owner"])
	}
}

// ---------------------------------------------------------------------------
// CountEntities and QueryWithPlan without tenant filtering
// ---------------------------------------------------------------------------

// TestPerFile_CountEntities verifies that CountEntities returns the correct
// count without any tenant_id filter in per-file mode.
func TestPerFile_CountEntities(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "count.db"), 1)
	defer store.Close()

	for i := 0; i < 5; i++ {
		if _, err := store.Create(ctx, "order", map[string]interface{}{
			"seq": i,
		}); err != nil {
			t.Fatalf("Create[%d]: %v", i, err)
		}
	}

	n, err := store.CountEntities(ctx, "order")
	if err != nil {
		t.Fatalf("CountEntities: %v", err)
	}
	if n != 5 {
		t.Errorf("CountEntities: want 5, got %d", n)
	}
}

// TestPerFile_QueryWithPlan verifies that a push-down query without
// tenant_id returns correct results in per-file mode.
func TestPerFile_QueryWithPlan(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "qwp.db"), 1)
	defer store.Close()

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := store.Create(ctx, "tag", map[string]interface{}{
			"label": name,
		}); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	// QueryWithPlan takes raw SQL + args; in per-file mode there's no tenant_id param.
	results, err := store.QueryWithPlan(ctx,
		`SELECT data, _version FROM `+tenant.NodesTableName(1)+` WHERE entity_type = ? ORDER BY id`,
		[]interface{}{"tag"})
	if err != nil {
		t.Fatalf("QueryWithPlan: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("QueryWithPlan: want 3 results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// IsPerFileTenant
// ---------------------------------------------------------------------------

func TestPerFile_IsPerFileTenant(t *testing.T) {
	dir := t.TempDir()

	pf := newPerFileStore(t, filepath.Join(dir, "pf.db"), 1)
	defer pf.Close()
	if !pf.IsPerFileTenant() {
		t.Error("IsPerFileTenant: want true for per-file store")
	}

	shared := newTenantSQLiteStore(t, filepath.Join(dir, "shared.db"), 1, false, false)
	defer shared.Close()
	if shared.IsPerFileTenant() {
		t.Error("IsPerFileTenant: want false for shared store")
	}
}

// ---------------------------------------------------------------------------
// Adapted table DDL omits tenant_id
// ---------------------------------------------------------------------------

// TestPerFile_AdaptedTableOmitsTenantID verifies that RegisterAdaptedEntity
// in per-file mode creates a table without a tenant_id column.
func TestPerFile_AdaptedTableOmitsTenantID(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "adapted.db"), 1)
	defer store.Close()

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"sku":   map[string]interface{}{"type": "string"},
			"stock": map[string]interface{}{"type": "integer"},
		},
		"required": []interface{}{"sku"},
	}
	if err := store.RegisterAdaptedEntity(ctx, "inventory", schema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}

	db := store.DB()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(t0001_ndata_inventory)")
	if err != nil {
		t.Fatalf("PRAGMA table_info(t0001_ndata_inventory): %v", err)
	}
	defer rows.Close()

	colNames := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		colNames[name] = true
	}

	if colNames["tenant_id"] {
		t.Error("adapted table 't0001_ndata_inventory': unexpected tenant_id column")
	}
	if !colNames["sku"] {
		t.Error("adapted table 't0001_ndata_inventory': missing 'sku' column")
	}
	if !colNames["id"] {
		t.Error("adapted table 't0001_ndata_inventory': missing 'id' column")
	}

	// CRUD on the adapted table should also work
	id, err := store.Create(ctx, "inventory", map[string]interface{}{
		"sku": "ABC-001", "stock": 42,
	})
	if err != nil {
		t.Fatalf("Create adapted: %v", err)
	}
	rec, err := store.Get(ctx, "inventory", id)
	if err != nil {
		t.Fatalf("Get adapted: %v", err)
	}
	if rec["sku"] != "ABC-001" {
		t.Errorf("Get adapted: sku = %v, want ABC-001", rec["sku"])
	}
}

// ---------------------------------------------------------------------------
// FTS in per-file mode
// ---------------------------------------------------------------------------

// TestPerFile_FTS verifies that full-text search works correctly in per-file
// mode (no tenant_id in fts5 table).
func TestPerFile_FTS(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "fts.db"), 1)
	defer store.Close()

	if _, err := store.Create(ctx, "article", map[string]interface{}{
		"title": "quantum entanglement explained",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Create(ctx, "article", map[string]interface{}{
		"title": "classical mechanics overview",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	results, err := store.FullTextSearch(ctx, "quantum", "article")
	if err != nil {
		t.Fatalf("FullTextSearch: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("FullTextSearch 'quantum': want 1 result, got %d", len(results))
	}

	// Search across all entities
	all, err := store.FullTextSearch(ctx, "overview", "")
	if err != nil {
		t.Fatalf("FullTextSearch all: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("FullTextSearch '' 'overview': want 1 result, got %d", len(all))
	}
}

// ---------------------------------------------------------------------------
// Save (upsert) in per-file mode
// ---------------------------------------------------------------------------

func TestPerFile_Save(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newPerFileStore(t, filepath.Join(dir, "save.db"), 1)
	defer store.Close()

	// Save creates
	created, err := store.Save(ctx, "config", 42, map[string]interface{}{
		"id": 42, "value": "original",
	})
	if err != nil {
		t.Fatalf("Save(create): %v", err)
	}
	if !created {
		t.Error("Save: want created=true on first save")
	}

	// Save overwrites
	created, err = store.Save(ctx, "config", 42, map[string]interface{}{
		"id": 42, "value": "updated",
	})
	if err != nil {
		t.Fatalf("Save(overwrite): %v", err)
	}
	if created {
		t.Error("Save: want created=false on overwrite")
	}

	rec, err := store.Get(ctx, "config", 42)
	if err != nil {
		t.Fatalf("Get after Save overwrite: %v", err)
	}
	if rec["value"] != "updated" {
		t.Errorf("Get after Save: value = %v, want updated", rec["value"])
	}
}
