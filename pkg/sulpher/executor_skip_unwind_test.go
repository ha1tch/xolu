// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_skip_unwind_test.go — tests for SKIP (Stage 2.11) and
// UNWIND (Stage 2.9).

import (
	"context"
	"testing"
)

// ── SKIP tests ────────────────────────────────────────────────────────────────

func TestSkip_Basic(t *testing.T) {
	t.Parallel()
	g, store := buildPredicateGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()

	// All users ordered by age; skip first 2 (ages 25 and 30).
	ast, hint, err := parser.Parse("MATCH (u:users) WHERE u.age IS NOT NULL RETURN u.age ORDER BY u.age SKIP 2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Ages in order: 25, 30, 35, 40. After SKIP 2: 35, 40.
	if len(result.Data) != 2 {
		t.Fatalf("expected 2 rows after SKIP 2, got %d", len(result.Data))
	}
	if result.Data[0]["u.age"] != 35 {
		t.Errorf("expected first row age=35, got %v", result.Data[0]["u.age"])
	}
}

func TestSkip_PastEnd(t *testing.T) {
	t.Parallel()
	g, store := buildPredicateGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()

	ast, hint, err := parser.Parse("MATCH (u:users) RETURN u SKIP 100")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) != 0 {
		t.Errorf("expected 0 rows when SKIP exceeds result count, got %d", len(result.Data))
	}
}

func TestSkip_WithLimit(t *testing.T) {
	t.Parallel()
	g, store := buildPredicateGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()

	// SKIP 1 LIMIT 2: skip first, take next 2.
	ast, hint, err := parser.Parse("MATCH (u:users) WHERE u.age IS NOT NULL RETURN u.age ORDER BY u.age SKIP 1 LIMIT 2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Ages: 25, 30, 35, 40. Skip 1 → 30, 35, 40. Limit 2 → 30, 35.
	if len(result.Data) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Data))
	}
	if result.Data[0]["u.age"] != 30 {
		t.Errorf("expected first=30, got %v", result.Data[0]["u.age"])
	}
	if result.Data[1]["u.age"] != 35 {
		t.Errorf("expected second=35, got %v", result.Data[1]["u.age"])
	}
}

func TestSkip_Zero(t *testing.T) {
	t.Parallel()
	g, store := buildPredicateGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()

	all, _, _ := parser.Parse("MATCH (u:users) RETURN u")
	withSkip, _, _ := parser.Parse("MATCH (u:users) RETURN u SKIP 0")

	rAll, _ := executor.Execute(context.Background(), all, nil)
	rSkip, _ := executor.Execute(context.Background(), withSkip, nil)
	_ = store

	if len(rAll.Data) != len(rSkip.Data) {
		t.Errorf("SKIP 0 should return same count as no SKIP: %d vs %d",
			len(rAll.Data), len(rSkip.Data))
	}
}

func TestSkip_WithPipeline(t *testing.T) {
	t.Parallel()
	g, store := buildDirectedGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()

	// Skip first result in WITH pipeline.
	ast, hint, err := parser.Parse(
		"MATCH (a:user {id: '1'})-[:knows]->(f:user) WITH f SKIP 1 MATCH (f)-[:knows]->(b:user) RETURN b.name")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Without SKIP: Bob→Carol and Carol→Dave. With SKIP 1 in WITH: only one f
	// passes; its downstream should be a subset. Just verify it runs.
	_ = result
}

// ── UNWIND tests ──────────────────────────────────────────────────────────────

func TestUnwind_BasicList(t *testing.T) {
	t.Parallel()
	g, _ := buildPredicateGraph()
	executor := NewExecutor(g, 10)
	parser := NewParser()

	ast, hint, err := parser.Parse("UNWIND [1, 2, 3] AS x RETURN x")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Data))
	}
	for i, row := range result.Data {
		expected := i + 1
		v, ok := toFloat64(row["x"])
		if !ok || int(v) != expected {
			t.Errorf("row %d: expected x=%d, got %v", i, expected, row["x"])
		}
	}
}

func TestUnwind_StringList(t *testing.T) {
	t.Parallel()
	g, _ := buildPredicateGraph()
	executor := NewExecutor(g, 10)
	parser := NewParser()

	ast, hint, err := parser.Parse("UNWIND ['alice', 'bob', 'carol'] AS name RETURN name")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Data))
	}
	if result.Data[0]["name"] != "alice" {
		t.Errorf("expected alice, got %v", result.Data[0]["name"])
	}
}

func TestUnwind_WithLimit(t *testing.T) {
	t.Parallel()
	g, _ := buildPredicateGraph()
	executor := NewExecutor(g, 10)
	parser := NewParser()

	ast, hint, err := parser.Parse("UNWIND [10, 20, 30, 40, 50] AS x RETURN x LIMIT 3")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) != 3 {
		t.Errorf("expected 3 rows (LIMIT 3), got %d", len(result.Data))
	}
}

func TestUnwind_WithSkip(t *testing.T) {
	t.Parallel()
	g, _ := buildPredicateGraph()
	executor := NewExecutor(g, 10)
	parser := NewParser()

	ast, hint, err := parser.Parse("UNWIND [1, 2, 3, 4, 5] AS x RETURN x SKIP 2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// [1,2,3,4,5] SKIP 2 → [3,4,5]
	if len(result.Data) != 3 {
		t.Fatalf("expected 3 rows after SKIP 2, got %d", len(result.Data))
	}
}

func TestUnwind_EmptyList(t *testing.T) {
	t.Parallel()
	g, _ := buildPredicateGraph()
	executor := NewExecutor(g, 10)
	parser := NewParser()

	ast, hint, err := parser.Parse("UNWIND [] AS x RETURN x")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) != 0 {
		t.Errorf("expected 0 rows for empty UNWIND, got %d", len(result.Data))
	}
}

func TestUnwind_WithExpressionAlias(t *testing.T) {
	t.Parallel()
	g, _ := buildPredicateGraph()
	executor := NewExecutor(g, 10)
	parser := NewParser()

	ast, hint, err := parser.Parse("UNWIND [1, 2, 3] AS x RETURN x * 2 AS doubled")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Data))
	}
	if v, ok := toFloat64(result.Data[0]["doubled"]); !ok || v != 2 {
		t.Errorf("expected doubled=2, got %v", result.Data[0]["doubled"])
	}
	if v, ok := toFloat64(result.Data[2]["doubled"]); !ok || v != 6 {
		t.Errorf("expected doubled=6, got %v", result.Data[2]["doubled"])
	}
}

func TestUnwind_Distinct(t *testing.T) {
	t.Parallel()
	g, _ := buildPredicateGraph()
	executor := NewExecutor(g, 10)
	parser := NewParser()

	ast, hint, err := parser.Parse("UNWIND [1, 2, 2, 3, 1] AS x RETURN DISTINCT x")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) != 3 {
		t.Errorf("expected 3 distinct rows, got %d: %v", len(result.Data), result.Data)
	}
}
