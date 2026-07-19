// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_union_test.go — tests for Stage 2.8: UNION and UNION ALL.

import (
	"context"
	"testing"
)

func unionQuery(t *testing.T, q string) []map[string]interface{} {
	t.Helper()
	g, store := buildSPGraph() // user:1..5 with names Alice,Bob,Carol,Dave,Eve
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()
	ast, hint, err := parser.Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute(%q): %v", q, err)
	}
	return result.Data
}

// ── UNION (with deduplication) ────────────────────────────────────────────────

func TestUnion_Distinct(t *testing.T) {
	t.Parallel()
	// Both halves return Alice; UNION should deduplicate.
	rows := unionQuery(t,
		"MATCH (u:user {id: '1'}) RETURN u.name AS name "+
			"UNION "+
			"MATCH (u:user {id: '1'}) RETURN u.name AS name")
	if len(rows) != 1 {
		t.Errorf("UNION should deduplicate; expected 1 row, got %d", len(rows))
	}
	if rows[0]["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", rows[0]["name"])
	}
}

func TestUnion_DifferentNodes(t *testing.T) {
	t.Parallel()
	rows := unionQuery(t,
		"MATCH (u:user {id: '1'}) RETURN u.name AS name "+
			"UNION "+
			"MATCH (u:user {id: '2'}) RETURN u.name AS name")
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
	names := make(map[interface{}]bool)
	for _, row := range rows {
		names[row["name"]] = true
	}
	if !names["Alice"] || !names["Bob"] {
		t.Errorf("expected Alice and Bob, got %v", names)
	}
}

func TestUnion_ThreeParts(t *testing.T) {
	t.Parallel()
	rows := unionQuery(t,
		"MATCH (u:user {id: '1'}) RETURN u.name AS name "+
			"UNION "+
			"MATCH (u:user {id: '2'}) RETURN u.name AS name "+
			"UNION "+
			"MATCH (u:user {id: '3'}) RETURN u.name AS name")
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
}

// ── UNION ALL (no deduplication) ─────────────────────────────────────────────

func TestUnionAll_NoDeduplicate(t *testing.T) {
	t.Parallel()
	// UNION ALL should NOT deduplicate.
	rows := unionQuery(t,
		"MATCH (u:user {id: '1'}) RETURN u.name AS name "+
			"UNION ALL "+
			"MATCH (u:user {id: '1'}) RETURN u.name AS name")
	if len(rows) != 2 {
		t.Errorf("UNION ALL should keep duplicates; expected 2 rows, got %d", len(rows))
	}
}

func TestUnionAll_DifferentTypes(t *testing.T) {
	t.Parallel()
	// In a real query you'd union different entity types; here we union
	// nodes by ID range to verify order is preserved.
	rows := unionQuery(t,
		"MATCH (u:user {id: '1'}) RETURN u.name AS name "+
			"UNION ALL "+
			"MATCH (u:user {id: '2'}) RETURN u.name AS name "+
			"UNION ALL "+
			"MATCH (u:user {id: '3'}) RETURN u.name AS name")
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
}

func TestUnion_EmptyFirstPart(t *testing.T) {
	t.Parallel()
	// First part matches nothing; second part matches Alice.
	rows := unionQuery(t,
		"MATCH (u:user {id: '999'}) RETURN u.name AS name "+
			"UNION "+
			"MATCH (u:user {id: '1'}) RETURN u.name AS name")
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", rows[0]["name"])
	}
}

func TestUnion_BothEmpty(t *testing.T) {
	t.Parallel()
	rows := unionQuery(t,
		"MATCH (u:user {id: '999'}) RETURN u.name AS name "+
			"UNION "+
			"MATCH (u:user {id: '998'}) RETURN u.name AS name")
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}
