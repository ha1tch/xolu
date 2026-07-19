// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"strings"
	"testing"
)

// TestValidatePersistedSpec_RejectsPoisonedSQLType is the regression test for
// D-016, the SQLType half of the persisted-identifier injection class.
//
// addColumnSQL builds "ALTER TABLE %s ADD COLUMN %s %s" with col.Name AND
// col.SQLType interpolated. The column name is validated everywhere, but
// col.SQLType — a persisted JSON value read back raw by LoadAdaptedRegistry —
// reached DDL unvalidated. At derivation SQLType comes from dialect.ColumnType
// (a closed set: TEXT/INTEGER/REAL), so a poisoned value can only arrive via a
// pre-fix DB, a restored backup, or a non-derivation writer — the same threat
// model as D-009/D-010/D-015. validatePersistedSpec now rejects any SQLType
// outside the allowlist.
func TestValidatePersistedSpec_RejectsPoisonedSQLType(t *testing.T) {
	poisoned := &AdaptedTableSpec{
		Entity: "scores",
		Columns: []ColumnDef{
			{Name: "score", JSONField: "score", Type: "integer", SQLType: "INTEGER"},
			{Name: "label", JSONField: "label", Type: "string", SQLType: "TEXT); DROP TABLE t0000_nodes;--"},
		},
	}
	err := validatePersistedSpec(poisoned)
	if err == nil {
		t.Fatalf("validatePersistedSpec accepted a poisoned SQLType; expected rejection")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sql type") {
		t.Errorf("rejection should identify the invalid SQL type, got: %v", err)
	}

	// Every legitimate dialect-produced type must pass.
	for _, ty := range []string{"TEXT", "INTEGER", "REAL"} {
		ok := &AdaptedTableSpec{
			Entity:  "scores",
			Columns: []ColumnDef{{Name: "v", JSONField: "v", Type: "string", SQLType: ty}},
		}
		if err := validatePersistedSpec(ok); err != nil {
			t.Errorf("validatePersistedSpec rejected legitimate SQLType %q: %v", ty, err)
		}
	}
}
