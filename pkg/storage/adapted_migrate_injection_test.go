package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// TestMigrate_DropColumn_RejectsPoisonedPersistedName is a red-first regression
// test for the residual D-009 class on the *read-back* path.
//
// D-009 closed identifier injection at schema *derivation* (DeriveAdaptedTableSpec
// validates every field name before it becomes a column/index identifier). But the
// schema-evolution migration computes its DROP COLUMN / DROP INDEX / data-migration
// SELECT statements from the *old* spec, which LoadAdaptedRegistry unmarshals
// verbatim from the persisted column_spec JSON with no re-validation.
//
// A column_spec row written by a pre-D-009 binary — or by any writer that does not
// go through derivation — can therefore carry a malicious column name straight into
// `ALTER TABLE <t> DROP COLUMN <name>`, which the modernc driver will execute as
// chained statements.
//
// This test poisons the persisted column_spec, reloads via LoadAdaptedRegistry
// (the upgrade path), and runs a migration that drops the poisoned column. If the
// drop name is interpolated unvalidated, the chained `DROP TABLE canary` fires.
func TestMigrate_DropColumn_RejectsPoisonedPersistedName(t *testing.T) {
	db, registry := setupMigrationDB(t)
	ctx := context.Background()
	dialect := &SQLiteStorageDialect{}

	// 1. Register a clean adapted table with two real columns.
	schemaV1 := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"score": map[string]interface{}{"type": "integer"},
			"label": map[string]interface{}{"type": "string"},
		},
	}
	registerWithSchema(t, db, registry, "scores", schemaV1)

	// 2. A canary table the injection payload will try to destroy.
	if _, err := db.ExecContext(ctx, `CREATE TABLE canary (x INTEGER)`); err != nil {
		t.Fatal(err)
	}

	// 3. Poison the persisted column_spec, simulating a pre-D-009 / non-derivation
	//    writer. The "label" column name is replaced with a chained-DDL payload.
	poison := []ColumnDef{
		{Name: "score", JSONField: "score", Type: "integer", SQLType: "INTEGER"},
		{Name: `label; DROP TABLE canary;--`, JSONField: "label", Type: "string", SQLType: "TEXT"},
	}
	poisonJSON, err := json.Marshal(poison)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE t0000_n_sch SET column_spec = ? WHERE entity_type = ?`,
		string(poisonJSON), "scores"); err != nil {
		t.Fatal(err)
	}

	// 4. LAYER 1 — load must reject the poisoned spec at the trust boundary,
	//    quarantine the entity, and surface a loud error (fail startup).
	_, loadErr := LoadAdaptedRegistry(ctx, db, 0)
	if loadErr == nil {
		t.Fatalf("LoadAdaptedRegistry accepted a poisoned column_spec; expected rejection")
	}
	if !strings.Contains(loadErr.Error(), "invalid") {
		t.Errorf("load rejection message should identify the invalid identifier, got: %v", loadErr)
	}

	// 5. LAYER 2 — defence in depth: even if a poisoned spec reaches a registry by
	//    some path that bypasses the loader, migration must refuse before any DDL.
	poisonReg := NewAdaptedRegistry()
	poisonReg.Set("scores", &AdaptedTableSpec{
		Entity:   "scores",
		TenantID: 0,
		Columns:  poison,
	})
	schemaV2 := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"score": map[string]interface{}{"type": "integer"},
		},
	}
	migErr := MigrateAdaptedTable(ctx, db, poisonReg, "scores", schemaV2, dialect)
	if migErr == nil {
		t.Errorf("MigrateAdaptedTable accepted a poisoned oldSpec; expected rejection")
	}

	// 6. The canary must still exist — no DDL payload executed at any layer.
	var canaryCount int
	row := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='canary'`)
	if scanErr := row.Scan(&canaryCount); scanErr != nil {
		t.Fatalf("canary existence check failed: %v", scanErr)
	}
	if canaryCount == 0 {
		t.Fatalf("DDL INJECTION: poisoned persisted column name executed chained DROP TABLE; canary destroyed")
	}
}

var _ = sql.ErrNoRows
