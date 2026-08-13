// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ha1tch/tsqlparser"
)

func TestParseSelect(t *testing.T) {
	queries := []string{
		"SELECT * FROM items",
		"SELECT id, name FROM items",
		"SELECT id, name FROM items WHERE status = 'active'",
		"SELECT category_id, COUNT(*) FROM items GROUP BY category_id",
		"SELECT category_id, COUNT(*) AS cnt FROM items GROUP BY category_id HAVING COUNT(*) > 5",
		"SELECT id, name FROM items ORDER BY name",
		"SELECT id, name FROM items ORDER BY name DESC",
		"SELECT TOP 10 * FROM items",
		"SELECT DISTINCT status FROM items",
	}

	for _, q := range queries {
		program, errs := tsqlparser.Parse(q)
		if len(errs) > 0 {
			t.Errorf("Failed to parse '%s': %v", q, errs[0])
			continue
		}
		if len(program.Statements) != 1 {
			t.Errorf("Expected 1 statement for '%s', got %d", q, len(program.Statements))
		}
	}
}

func TestParseInsert(t *testing.T) {
	queries := []string{
		"INSERT INTO items (category_id, status) VALUES (1, 'active')",
		"INSERT INTO items (category_id, status) VALUES (1, 'active'), (2, 'inactive')",
	}

	for _, q := range queries {
		program, errs := tsqlparser.Parse(q)
		if len(errs) > 0 {
			t.Errorf("Failed to parse '%s': %v", q, errs[0])
			continue
		}
		if len(program.Statements) != 1 {
			t.Errorf("Expected 1 statement for '%s', got %d", q, len(program.Statements))
		}
	}
}

func TestParseUpdate(t *testing.T) {
	queries := []string{
		"UPDATE items SET status = 'inactive' WHERE category_id = 5",
		"UPDATE items SET status = 'inactive', value = 0 WHERE id = 1",
	}

	for _, q := range queries {
		program, errs := tsqlparser.Parse(q)
		if len(errs) > 0 {
			t.Errorf("Failed to parse '%s': %v", q, errs[0])
			continue
		}
		if len(program.Statements) != 1 {
			t.Errorf("Expected 1 statement for '%s', got %d", q, len(program.Statements))
		}
	}
}

func TestParseDelete(t *testing.T) {
	queries := []string{
		"DELETE FROM items WHERE status = 'decommissioned'",
		"DELETE FROM items WHERE category_id = 5 AND status = 'inactive'",
	}

	for _, q := range queries {
		program, errs := tsqlparser.Parse(q)
		if len(errs) > 0 {
			t.Errorf("Failed to parse '%s': %v", q, errs[0])
			continue
		}
		if len(program.Statements) != 1 {
			t.Errorf("Expected 1 statement for '%s', got %d", q, len(program.Statements))
		}
	}
}

func TestAggregates(t *testing.T) {
	tests := []struct {
		name     string
		values   []interface{}
		expected interface{}
		fn       AggregateFunc
	}{
		{"count", []interface{}{1, 2, 3, nil, 5}, 4, Aggregates["COUNT"]},
		{"sum", []interface{}{1.0, 2.0, 3.0}, 6.0, Aggregates["SUM"]},
		{"avg", []interface{}{2.0, 4.0, 6.0}, 4.0, Aggregates["AVG"]},
		{"min", []interface{}{3, 1, 2}, 1, Aggregates["MIN"]},
		{"max", []interface{}{3, 1, 2}, 3, Aggregates["MAX"]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.fn(tt.values)
			if result != tt.expected {
				// Handle float comparison
				if f1, ok := result.(float64); ok {
					if f2, ok := tt.expected.(float64); ok {
						if f1 != f2 {
							t.Errorf("%s: expected %v, got %v", tt.name, tt.expected, result)
						}
						return
					}
				}
				if result != tt.expected {
					t.Errorf("%s: expected %v, got %v", tt.name, tt.expected, result)
				}
			}
		})
	}
}

func TestCompareValues(t *testing.T) {
	tests := []struct {
		a, b     interface{}
		expected int
	}{
		{1, 2, -1},
		{2, 1, 1},
		{1, 1, 0},
		{"a", "b", -1},
		{"b", "a", 1},
		{"a", "a", 0},
		{nil, 1, -1},
		{1, nil, 1},
		{nil, nil, 0},
	}

	for _, tt := range tests {
		result := compareValues(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("compareValues(%v, %v): expected %d, got %d", tt.a, tt.b, tt.expected, result)
		}
	}
}

func TestMatchLike(t *testing.T) {
	tests := []struct {
		value, pattern string
		expected       bool
	}{
		{"hello", "hello", true},
		{"hello", "Hello", true}, // Case insensitive
		{"hello", "%ello", true},
		{"hello", "hell%", true},
		{"hello", "%ell%", true},
		{"hello", "%xyz%", false},
		{"hello", "xyz%", false},
		{"hello", "%xyz", false},
	}

	for _, tt := range tests {
		result := matchLike(tt.value, tt.pattern)
		if result != tt.expected {
			t.Errorf("matchLike(%q, %q): expected %v, got %v", tt.value, tt.pattern, tt.expected, result)
		}
	}
}

func TestNormalizeEntityName(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"items", "items"},
		{"Items", "items"},
		{"[items]", "items"},
		{"dbo.items", "items"},
		{"dbo.[Items]", "items"},
		{`"items"`, "items"},
	}

	for _, tt := range tests {
		result := normalizeEntityName(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeEntityName(%q): expected %q, got %q", tt.input, tt.expected, result)
		}
	}
}

func TestValidatorRejectsUpdateWithoutWhere(t *testing.T) {
	v := NewValidator("/tmp/nonexistent")

	// This should fail validation
	program, _ := tsqlparser.Parse("UPDATE items SET status = 'inactive'")
	if len(program.Statements) == 1 {
		err := v.Validate(program.Statements[0])
		if err == nil {
			t.Error("Expected error for UPDATE without WHERE")
		}
		if err != nil && err.Error() != "UPDATE without WHERE clause is not permitted" {
			t.Errorf("Unexpected error message: %v", err)
		}
	}
}

func TestValidatorRejectsDeleteWithoutWhere(t *testing.T) {
	v := NewValidator("/tmp/nonexistent")

	// This should fail validation
	program, _ := tsqlparser.Parse("DELETE FROM items")
	if len(program.Statements) == 1 {
		err := v.Validate(program.Statements[0])
		if err == nil {
			t.Error("Expected error for DELETE without WHERE")
		}
		if err != nil && err.Error() != "DELETE without WHERE clause is not permitted" {
			t.Errorf("Unexpected error message: %v", err)
		}
	}
}

// TestValidatorNamesSetOperator confirms the set-operation rejection names the
// actual operator (UNION/INTERSECT/EXCEPT) rather than always saying "UNION".
// Regression test for the TD-002 error-message fix.
// mockEntityChecker is a minimal EntityChecker for validator tests that
// need real entities to exist, rather than the "/tmp/nonexistent" schema
// dir every other validator test in this file uses (which is fine for
// tests that only care about rejection before entity resolution is ever
// reached, but not for UNION/INTERSECT/EXCEPT tests, which need both
// branches' own entities to resolve successfully to test the set-operator
// logic itself, not an unrelated "entity doesn't exist" failure).
type mockEntityChecker struct{ names []string }

func (m *mockEntityChecker) ListEntities(_ context.Context) ([]string, error) {
	return m.names, nil
}

// TestValidatorUnionChain covers UNION/INTERSECT/EXCEPT validation
// (2026-08-12): previously rejected outright ("UNION is not supported");
// now validated properly. Positive cases confirm a well-formed chain is
// accepted; negative cases confirm the two deliberate restrictions this
// implementation chose (homogeneous operator type across a chain, matching
// column counts on both sides) are enforced, and that an invalid entity on
// either branch still propagates as a real error, not silently ignored.
func TestValidatorUnionChain(t *testing.T) {
	v := NewValidatorWithStore("/tmp/nonexistent", &mockEntityChecker{names: []string{"items", "other", "third"}})

	tests := []struct {
		name    string
		query   string
		wantErr bool
		errSub  string
	}{
		{"plain UNION accepted", "SELECT id FROM items UNION SELECT id FROM other", false, ""},
		{"UNION ALL accepted", "SELECT id FROM items UNION ALL SELECT id FROM other", false, ""},
		{"INTERSECT accepted", "SELECT id FROM items INTERSECT SELECT id FROM other", false, ""},
		{"EXCEPT accepted", "SELECT id FROM items EXCEPT SELECT id FROM other", false, ""},
		{"chained UNION (3 branches) accepted", "SELECT id FROM items UNION SELECT id FROM other UNION SELECT id FROM third", false, ""},
		{"mixed operators rejected", "SELECT id FROM items UNION SELECT id FROM other INTERSECT SELECT id FROM third",
			true, "mixed set operators"},
		{"mismatched column counts rejected", "SELECT id, name FROM items UNION SELECT id FROM other",
			true, "same number of columns"},
		{"unknown entity on the right branch still rejected", "SELECT id FROM items UNION SELECT id FROM nonexistent_entity",
			true, "does not exist"},
		{"INTERSECT ALL rejected (not valid T-SQL, would need real multiset semantics)",
			"SELECT id FROM items INTERSECT ALL SELECT id FROM other", true, "INTERSECT ALL is not supported"},
		{"EXCEPT ALL rejected", "SELECT id FROM items EXCEPT ALL SELECT id FROM other", true, "EXCEPT ALL is not supported"},
		{"SELECT * rejected on the left branch", "SELECT * FROM items UNION SELECT id FROM other",
			true, "SELECT * is not supported"},
		{"SELECT * rejected on the right branch", "SELECT id FROM items UNION SELECT * FROM other",
			true, "SELECT * is not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			program, errs := tsqlparser.Parse(tt.query)
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			if len(program.Statements) != 1 {
				t.Fatalf("expected 1 statement, got %d", len(program.Statements))
			}
			err := v.Validate(program.Statements[0])
			if tt.wantErr {
				if err == nil {
					t.Fatalf("%q: expected rejection, got nil", tt.query)
				}
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Errorf("%q: want error containing %q, got %q", tt.query, tt.errSub, err.Error())
				}
			} else if err != nil {
				t.Errorf("%q: expected acceptance, got error: %v", tt.query, err)
			}
		})
	}
}

func TestValidatorDerivedTable(t *testing.T) {
	v := NewValidatorWithStore("/tmp/nonexistent", &mockEntityChecker{names: []string{"items", "other"}})

	tests := []struct {
		name    string
		query   string
		wantErr bool
		errSub  string
	}{
		{"basic derived table accepted", "SELECT * FROM (SELECT id FROM items) AS x", false, ""},
		{"derived table with WHERE inside accepted", "SELECT * FROM (SELECT id FROM items WHERE id = 1) AS x", false, ""},
		{"derived table with WHERE outside accepted", "SELECT * FROM (SELECT id FROM items) AS x WHERE id = 1", false, ""},
		{"nested derived table accepted", "SELECT * FROM (SELECT id FROM (SELECT id FROM items) AS inner1) AS outer1", false, ""},
		{"derived table containing UNION accepted", "SELECT * FROM (SELECT id FROM items UNION SELECT id FROM other) AS x", false, ""},
		{"missing alias rejected", "SELECT * FROM (SELECT id FROM items)", true, "requires an explicit alias"},
		{"column alias list rejected", "SELECT * FROM (SELECT id FROM items) AS x(n)", true, "column alias list"},
		{"unknown entity inside the subquery still rejected", "SELECT * FROM (SELECT id FROM nonexistent_entity) AS x", true, "does not exist"},
		{"derived table on JOIN left side rejected", "SELECT a.id FROM (SELECT id FROM items) AS a INNER JOIN other AS b ON a.id = b.id",
			true, "not a subquery or derived table"},
		{"derived table on JOIN right side rejected", "SELECT a.id FROM items AS a INNER JOIN (SELECT id FROM other) AS b ON a.id = b.id",
			true, "not a subquery or derived table"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			program, errs := tsqlparser.Parse(tt.query)
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			if len(program.Statements) != 1 {
				t.Fatalf("expected 1 statement, got %d", len(program.Statements))
			}
			err := v.Validate(program.Statements[0])
			if tt.wantErr {
				if err == nil {
					t.Fatalf("%q: expected rejection, got nil", tt.query)
				}
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Errorf("%q: want error containing %q, got %q", tt.query, tt.errSub, err.Error())
				}
			} else if err != nil {
				t.Errorf("%q: expected acceptance, got error: %v", tt.query, err)
			}
		})
	}
}

func TestValidatorDerivedTableDepthCap(t *testing.T) {
	v := NewValidatorWithStore("/tmp/nonexistent", &mockEntityChecker{names: []string{"items"}})

	build := func(levels int) string {
		q := "SELECT id FROM items"
		for i := 0; i < levels; i++ {
			q = fmt.Sprintf("SELECT id FROM (%s) AS lvl%d", q, i)
		}
		return q
	}

	// maxDerivedTableDepth is 10 -- exactly at the cap must pass,
	// one more must fail. Testing the boundary precisely, not just
	// "some large number fails".
	atCap := build(10)
	program, errs := tsqlparser.Parse(atCap)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	if err := v.Validate(program.Statements[0]); err != nil {
		t.Errorf("exactly %d levels (the cap): want acceptance, got %v", maxDerivedTableDepth, err)
	}

	overCap := build(11)
	program, errs = tsqlparser.Parse(overCap)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	err := v.Validate(program.Statements[0])
	if err == nil {
		t.Fatal("11 levels (one over the cap): want rejection, got nil")
	}
	if !strings.Contains(err.Error(), "nesting too deep") {
		t.Errorf("want a nesting-depth error, got %q", err.Error())
	}
}
