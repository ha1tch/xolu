// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// D-009: a JSON-schema property key becomes a SQL column name verbatim
// (DeriveAdaptedTableSpecFrom, adapted.go:218) and is interpolated into DDL by
// CreateTableSQL / schema_evolution with no identifier validation. A crafted
// key is DDL injection, executed end-to-end via POST /api/v1/schema/{entity}.
//
// Expected end state after the fix: a non-identifier field name is rejected
// during derivation (error), so no DDL is generated from it.
func TestDeriveAdaptedSpec_MaliciousFieldName_DDL(t *testing.T) {
	dialect := &SQLiteStorageDialect{}
	payload := `evil TEXT); DROP TABLE t0000_nodes;--`
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":  map[string]interface{}{"type": "string"},
			payload: map[string]interface{}{"type": "string"},
		},
	}

	spec, err := DeriveAdaptedTableSpec("widget", schema, dialect, 0)
	if err != nil {
		// Rejection at derivation is the SAFE (post-fix) outcome.
		return
	}
	for _, c := range spec.Columns {
		if strings.Contains(c.Name, "DROP TABLE") || strings.Contains(c.Name, ");") {
			t.Errorf("DDL injection: schema field name became column %q; DDL:\n%s",
				c.Name, dialect.CreateTableSQL(spec))
			return
		}
	}
}

// Confirms modernc.org/sqlite executes chained ;-separated statements in one
// ExecContext, which makes the D-009 injection able to chain DROP/ALTER after
// the CREATE rather than merely corrupting one statement.
func TestModerncSQLite_MultiStatementExec(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mustExec(t, db, "CREATE TABLE victim (id INTEGER)")
	mustExec(t, db, "INSERT INTO victim VALUES (1)")

	if _, err := db.ExecContext(ctx, "CREATE TABLE x (a TEXT); DROP TABLE victim;--"); err != nil {
		t.Fatalf("chained exec returned error (driver may have changed behaviour): %v", err)
	}
	var n int
	if scanErr := db.QueryRowContext(ctx, "SELECT count(*) FROM victim").Scan(&n); scanErr != nil {
		t.Logf("confirmed: modernc.org/sqlite chains statements in one Exec (victim dropped); " +
			"D-009 injection can chain DDL/DML")
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatal(err)
	}
}
