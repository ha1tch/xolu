// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

// newPerFileSQLiteStore creates a SQLiteStore in per-file tenant mode for OQL tests.
func newPerFileSQLiteStore(t *testing.T, dbPath string, tenantID uint16) storage.Store {
	t.Helper()
	store, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type:                 "sqlite",
		DBPath:               dbPath,
		FullTextEnabled:      false,
		GraphEnabled:         false,
		TenantID:             tenantID,
		SQLitePerFileTenants: true,
	})
	if err != nil {
		t.Fatalf("newPerFileSQLiteStore(tenant=%d): %v", tenantID, err)
	}
	return store
}

// TestGenerateSQL_EmptyTenantIDOmitsTenantClause directly tests the SQL
// generator with an empty tenantID and asserts the output contains no
// reference to tenant_id. This is what ExecuteWithStore sets for per-file stores.
func TestGenerateSQL_EmptyTenantIDOmitsTenantClause(t *testing.T) {
	sel := parseSQLGen(t, "SELECT * FROM product")
	dialect := &SQLiteDialect{}
	plan := QueryPlan{Push: []PushDecision{PushNone}}

	gen, err := GenerateSQL(sel, "product", "", plan, dialect)
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}
	if strings.Contains(gen.SQL, "tenant_id") {
		t.Errorf("GenerateSQL with empty tenantID: SQL contains 'tenant_id':\n%s", gen.SQL)
	}
}

// TestGenerateSQL_NonEmptyTenantIDIncludesTenantClause verifies the
// shared-mode path still emits the tenant_id clause when tenantID is set.
func TestGenerateSQL_NonEmptyTenantIDIncludesTenantClause(t *testing.T) {
	sel := parseSQLGen(t, "SELECT * FROM product")
	dialect := &SQLiteDialect{}
	plan := QueryPlan{Push: []PushDecision{PushNone}}

	gen, err := GenerateSQL(sel, "product", "7", plan, dialect)
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}
	if !strings.Contains(gen.SQL, "tenant_id") {
		t.Errorf("GenerateSQL with tenantID='7': SQL missing 'tenant_id':\n%s", gen.SQL)
	}
}

// TestExecuteWithStore_PerFileStore verifies end-to-end that ExecuteWithStore
// suppresses sqlTenantID for a per-file store. If tenant_id were emitted,
// SQLite would return "no such column: tenant_id" because per-file schemas
// have no such column.
func TestExecuteWithStore_PerFileStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pf_e2e.db")

	store := newPerFileSQLiteStore(t, dbPath, 1)
	defer store.Close()

	ctx := context.Background()
	schemaDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(schemaDir, "item"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	engine := NewEngine(store, schemaDir)

	if _, err := engine.ExecuteWithStore(ctx,
		"INSERT INTO item (name, qty) VALUES ('bolt', 100)", store); err != nil {
		t.Fatalf("INSERT bolt: %v", err)
	}
	if _, err := engine.ExecuteWithStore(ctx,
		"INSERT INTO item (name, qty) VALUES ('nut', 200)", store); err != nil {
		t.Fatalf("INSERT nut: %v", err)
	}

	result, err := engine.ExecuteWithStore(ctx, "SELECT * FROM item", store)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Errorf("SELECT: want 2 rows, got %d", len(result.Rows))
	}

	result, err = engine.ExecuteWithStore(ctx,
		"SELECT * FROM item WHERE name = 'bolt'", store)
	if err != nil {
		t.Fatalf("SELECT WHERE: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Errorf("SELECT WHERE name='bolt': want 1 row, got %d", len(result.Rows))
	}
}

// TestExecuteWithStore_PerFileTwoTenantIsolation verifies that two per-file
// stores at different paths don't bleed data through OQL queries.
func TestExecuteWithStore_PerFileTwoTenantIsolation(t *testing.T) {
	dir := t.TempDir()

	storeA := newPerFileSQLiteStore(t, filepath.Join(dir, "ta.db"), 1)
	storeB := newPerFileSQLiteStore(t, filepath.Join(dir, "tb.db"), 2)
	defer storeA.Close()
	defer storeB.Close()

	ctx := context.Background()
	schemaDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(schemaDir, "record"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	engineA := NewEngine(storeA, schemaDir)
	engineB := NewEngine(storeB, schemaDir)

	for i := 0; i < 3; i++ {
		if _, err := engineA.ExecuteWithStore(ctx,
			"INSERT INTO record (owner) VALUES ('A')", storeA); err != nil {
			t.Fatalf("A INSERT[%d]: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := engineB.ExecuteWithStore(ctx,
			"INSERT INTO record (owner) VALUES ('B')", storeB); err != nil {
			t.Fatalf("B INSERT[%d]: %v", i, err)
		}
	}

	resA, err := engineA.ExecuteWithStore(ctx, "SELECT * FROM record", storeA)
	if err != nil {
		t.Fatalf("A SELECT: %v", err)
	}
	if len(resA.Rows) != 3 {
		t.Errorf("A SELECT: want 3 rows, got %d", len(resA.Rows))
	}

	resB, err := engineB.ExecuteWithStore(ctx, "SELECT * FROM record", storeB)
	if err != nil {
		t.Fatalf("B SELECT: %v", err)
	}
	if len(resB.Rows) != 2 {
		t.Errorf("B SELECT: want 2 rows, got %d", len(resB.Rows))
	}

	for _, row := range resA.Rows {
		if row["owner"] == "B" {
			t.Errorf("A SELECT: unexpected row owned by B: %v", row)
		}
	}
	for _, row := range resB.Rows {
		if row["owner"] == "A" {
			t.Errorf("B SELECT: unexpected row owned by A: %v", row)
		}
	}
}
