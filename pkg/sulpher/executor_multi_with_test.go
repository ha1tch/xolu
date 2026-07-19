// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_multi_with_test.go — tests for chained WITH clauses.
//
// A single WITH clause was already tested in executor_oc9_test.go.
// These tests exercise two or more sequential WITH clauses (pipeline mode).

import (
	"testing"

	"github.com/ha1tch/xolu/pkg/graph"
)

// buildWithChainGraph creates:
//
//	user:1 (Alice, dept=eng,  score=90)
//	user:2 (Bob,   dept=eng,  score=70)
//	user:3 (Carol, dept=mkt,  score=85)
//	user:4 (Dave,  dept=mkt,  score=60)
//
//	All users connected as a chain: 1→2→3→4 via KNOWS.
func buildWithChainGraph() (graph.Graph, *mockStore) {
	g := graph.NewFlatGraph()
	for i := 1; i <= 4; i++ {
		g.AddNode("user:"+intStr(i), "user")
	}
	g.AddEdge("user:1", "user:2", "KNOWS")
	g.AddEdge("user:2", "user:3", "KNOWS")
	g.AddEdge("user:3", "user:4", "KNOWS")

	store := newMockStore()
	store.set("user", 1, map[string]interface{}{"name": "Alice", "dept": "eng", "score": 90})
	store.set("user", 2, map[string]interface{}{"name": "Bob", "dept": "eng", "score": 70})
	store.set("user", 3, map[string]interface{}{"name": "Carol", "dept": "mkt", "score": 85})
	store.set("user", 4, map[string]interface{}{"name": "Dave", "dept": "mkt", "score": 60})
	return g, store
}

func TestMultiWith_TwoWithNoMatch(t *testing.T) {
	t.Parallel()
	// MATCH → WITH (project name) → WITH (project again, filter) → RETURN
	// Tests that a chain of two WITHs with no intervening MATCH works.
	g, store := buildWithChainGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user)
		 WITH u.name AS name, u.score AS score
		 WITH name, score
		 WHERE score > 75
		 RETURN name, score`)
	// Alice(90) and Carol(85) qualify.
	if len(rows) != 2 {
		t.Errorf("want 2 rows (score > 75), got %d: %v", len(rows), rows)
	}
}

func TestMultiWith_TwoWithFiltersChained(t *testing.T) {
	t.Parallel()
	// First WITH filters dept=eng, second WITH filters score > 75.
	// Only Alice qualifies (dept=eng AND score=90).
	g, store := buildWithChainGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user)
		 WITH u.name AS name, u.dept AS dept, u.score AS score
		 WHERE dept = "eng"
		 WITH name, score
		 WHERE score > 75
		 RETURN name, score`)
	if len(rows) != 1 {
		t.Errorf("want 1 row (Alice), got %d: %v", len(rows), rows)
	}
	if len(rows) == 1 && str(rows[0]["name"]) != "Alice" {
		t.Errorf("want Alice, got %v", rows[0]["name"])
	}
}

func TestMultiWith_WithMatchWith(t *testing.T) {
	t.Parallel()
	// MATCH (u:user) WITH u WHERE u.dept = "eng"
	// MATCH (u)-[:KNOWS]->(v:user)
	// WITH v.name AS colleague
	// RETURN colleague
	//
	// Eng users: Alice(1), Bob(2).
	// Alice knows Bob; Bob knows Carol.
	// So colleague values: Bob, Carol.
	g, store := buildWithChainGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user)
		 WITH u
		 WHERE u.dept = "eng"
		 MATCH (u)-[:KNOWS]->(v:user)
		 WITH v.name AS colleague
		 RETURN colleague`)
	if len(rows) != 2 {
		t.Errorf("want 2 colleagues (Bob, Carol), got %d: %v", len(rows), rows)
	}
	names := map[string]bool{}
	for _, r := range rows {
		names[str(r["colleague"])] = true
	}
	for _, want := range []string{"Bob", "Carol"} {
		if !names[want] {
			t.Errorf("expected %q in results, got %v", want, rows)
		}
	}
}

func TestMultiWith_ThreeWithClauses(t *testing.T) {
	t.Parallel()
	// Three sequential WITH clauses, each narrowing the result.
	// First: all users. Second: filter dept=mkt. Third: filter score > 70.
	// Only Carol (dept=mkt, score=85) survives.
	g, store := buildWithChainGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user)
		 WITH u.name AS name, u.dept AS dept, u.score AS score
		 WITH name, dept, score
		 WHERE dept = "mkt"
		 WITH name, score
		 WHERE score > 70
		 RETURN name`)
	if len(rows) != 1 {
		t.Errorf("want 1 row (Carol), got %d: %v", len(rows), rows)
	}
	if len(rows) == 1 && str(rows[0]["name"]) != "Carol" {
		t.Errorf("want Carol, got %v", rows[0]["name"])
	}
}

func TestMultiWith_WithAggregationThenFilter(t *testing.T) {
	t.Parallel()
	// MATCH → WITH COUNT(*) AS n, dept → WITH ... WHERE n > 1 → RETURN dept
	// Both depts (eng, mkt) have 2 users each, so both qualify.
	g, store := buildWithChainGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user)
		 WITH u.dept AS dept, COUNT(*) AS n
		 WITH dept, n
		 WHERE n > 1
		 RETURN dept, n`)
	if len(rows) != 2 {
		t.Errorf("want 2 dept rows, got %d: %v", len(rows), rows)
	}
}
