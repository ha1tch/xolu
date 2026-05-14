// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test 1: Push-down SQL golden assertion — per-file vs shared mode
// ---------------------------------------------------------------------------

// TestGenerateSQL_PushWhere_PerFileNoTenantID is the high-value OQL test the
// spec called for: assert that with PushWhere and a real WHERE predicate,
// the generated SQL contains a json_extract predicate but NOT tenant_id, and
// that the arg list has exactly 2 entries (entity_type + predicate value) —
// not 3, which would indicate a spurious tenant_id arg was injected.
func TestGenerateSQL_PushWhere_PerFileNoTenantID(t *testing.T) {
	sel := parseSQLGen(t, "SELECT * FROM order WHERE status = 'shipped'")
	dialect := &SQLiteDialect{}
	plan := QueryPlan{Push: []PushDecision{PushWhere}}

	gen, err := GenerateSQL(sel, "order", "", plan, dialect)
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}

	// Must not contain tenant_id anywhere in the SQL string
	if strings.Contains(gen.SQL, "tenant_id") {
		t.Errorf("per-file GenerateSQL: SQL contains 'tenant_id':\n%s", gen.SQL)
	}

	// Must contain the WHERE predicate expression
	if !strings.Contains(gen.SQL, "json_extract") {
		t.Errorf("per-file GenerateSQL: SQL missing json_extract (WHERE not translated):\n%s", gen.SQL)
	}

	// Arg count: BaseQuery adds entity_type arg (1), WHERE adds predicate value (1) = 2 total.
	// If tenant_id were injected, count would be 3.
	if len(gen.Args) != 2 {
		t.Errorf("per-file GenerateSQL: want 2 args (entity_type + predicate), got %d: %v",
			len(gen.Args), gen.Args)
	}

	// The first arg should be the entity name
	if gen.Args[0] != "order" {
		t.Errorf("per-file GenerateSQL: Args[0] = %v, want 'order'", gen.Args[0])
	}

	// The second arg should be the predicate value
	if gen.Args[1] != "shipped" {
		t.Errorf("per-file GenerateSQL: Args[1] = %v, want 'shipped'", gen.Args[1])
	}
}

// TestGenerateSQL_PushWhere_SharedModeArgCount is the regression guard:
// in shared mode the arg list must have 3 entries
// (entity_type + tenant_id + predicate value), not 2.
func TestGenerateSQL_PushWhere_SharedModeArgCount(t *testing.T) {
	sel := parseSQLGen(t, "SELECT * FROM order WHERE status = 'shipped'")
	dialect := &SQLiteDialect{}
	plan := QueryPlan{Push: []PushDecision{PushWhere}}

	gen, err := GenerateSQL(sel, "order", "7", plan, dialect)
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}

	if !strings.Contains(gen.SQL, "tenant_id") {
		t.Errorf("shared GenerateSQL: SQL missing 'tenant_id':\n%s", gen.SQL)
	}

	// 3 args: entity_type, tenant_id value, predicate value
	if len(gen.Args) != 3 {
		t.Errorf("shared GenerateSQL: want 3 args (entity_type + tenant_id + predicate), got %d: %v",
			len(gen.Args), gen.Args)
	}
}

// TestGenerateSQL_PushWhere_MultiplePredicates verifies the arg count holds
// for a compound WHERE (AND), ensuring no off-by-one in per-file mode.
func TestGenerateSQL_PushWhere_MultiplePredicates(t *testing.T) {
	sel := parseSQLGen(t, "SELECT * FROM product WHERE price > 10 AND active = 1")
	dialect := &SQLiteDialect{}
	plan := QueryPlan{Push: []PushDecision{PushWhere}}

	gen, err := GenerateSQL(sel, "product", "", plan, dialect)
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}

	if strings.Contains(gen.SQL, "tenant_id") {
		t.Errorf("per-file compound WHERE: SQL contains 'tenant_id':\n%s", gen.SQL)
	}

	// 3 args: entity_type + price value + active value
	if len(gen.Args) != 3 {
		t.Errorf("per-file compound WHERE: want 3 args, got %d: %v", len(gen.Args), gen.Args)
	}
}

// TestGenerateSQL_PushWhere_SharedModeCompound is the shared-mode equivalent:
// compound WHERE should add tenant_id making 4 args total.
func TestGenerateSQL_PushWhere_SharedModeCompound(t *testing.T) {
	sel := parseSQLGen(t, "SELECT * FROM product WHERE price > 10 AND active = 1")
	dialect := &SQLiteDialect{}
	plan := QueryPlan{Push: []PushDecision{PushWhere}}

	gen, err := GenerateSQL(sel, "product", "3", plan, dialect)
	if err != nil {
		t.Fatalf("GenerateSQL: %v", err)
	}

	// 4 args: entity_type + tenant_id + price value + active value
	if len(gen.Args) != 4 {
		t.Errorf("shared compound WHERE: want 4 args, got %d: %v", len(gen.Args), gen.Args)
	}
}
