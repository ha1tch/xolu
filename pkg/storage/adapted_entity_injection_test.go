// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"strings"
	"testing"
)

// TestLoadRegistry_RejectsPoisonedEntityName is the regression test for D-015,
// the entity-name half of the D-010 persisted-identifier class.
//
// AdaptedTableSpec.TableName() builds the SQL table name as
// "t<X>_ndata_" + spec.Entity, and that table name is interpolated (unquoted,
// unparameterisable) into every adapted-table statement — adaptedEntityIDs'
// "SELECT id FROM <table>", the schema-evolution DDL, CRUD, etc. The HTTP layer
// validates entity names at registration (validateEntityName), but
// LoadAdaptedRegistry read spec.Entity raw from the persisted n_sch table, and
// the D-010 validatePersistedSpec guard checked column/index names but NOT the
// entity name itself. A poisoned entity_type from a pre-fix DB, a restored
// backup, or any non-derivation writer therefore reached SQL.
func TestLoadRegistry_RejectsPoisonedEntityName(t *testing.T) {
	db, _ := setupMigrationDB(t)
	ctx := context.Background()

	// Create the per-tenant schema registry table for tenant 0.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS t0000_n_sch (
		entity_type TEXT PRIMARY KEY,
		schema_hash TEXT NOT NULL,
		column_spec TEXT NOT NULL,
		has_extra   INTEGER NOT NULL DEFAULT 1,
		created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`); err != nil {
		t.Fatal(err)
	}

	// Insert a row whose entity_type carries a SQL payload. column_spec is a
	// valid (empty) column array so the only invalid identifier is the entity.
	poisonEntity := `x; DROP TABLE t0000_nodes;--`
	if _, err := db.ExecContext(ctx,
		`INSERT INTO t0000_n_sch (entity_type, schema_hash, column_spec, has_extra) VALUES (?, ?, ?, ?)`,
		poisonEntity, "hash", "[]", 0); err != nil {
		t.Fatal(err)
	}

	// Load must reject the poisoned entity name at the trust boundary.
	_, err := LoadAdaptedRegistry(ctx, db, 0)
	if err == nil {
		t.Fatalf("LoadAdaptedRegistry accepted a poisoned entity_type; expected rejection")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "entity") &&
		!strings.Contains(strings.ToLower(err.Error()), "invalid") {
		t.Errorf("rejection should identify the invalid entity name, got: %v", err)
	}
}
