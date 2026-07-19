// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// kl1_test.go — regression tests for KL-1: WITH pipeline must not route to push-down.
//
// Before the fix, a query like:
//   MATCH (lead:person)<-[:lead]-(proj:project) WHERE proj.status = 'active'
//   WITH lead, proj
//   MATCH (lead)-[:knows]->(contact:person) WHERE contact.department = 'Data'
//   RETURN lead.name, contact.name
//
// was routed to the SQL push-down executor because the first MATCH had a
// property-only WHERE clause. The push-down generator produced a flat SQL
// SELECT that could not bind variables from a subsequent MATCH.
//
// Fix: planGraphQueryAST returns planTraversal whenever the query contains
// a WITH clause (withStages non-empty), regardless of the WHERE clause.

import (
	"context"
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/graph"
)

// buildKL1Graph builds:
//
//	lead:1 <-[lead]- proj:1 (status="active")
//	lead:1 -[knows]-> contact:1 (department="Data")
func buildKL1Graph() (graph.Graph, *mockStore) {
	// Graph: mgr:1 -[manages]-> worker:1 -[knows]-> colleague:1
	// This gives a clean two-hop chain for the WITH pipeline test.
	g := graph.NewFlatGraph()
	for _, n := range []string{"mgr:1", "worker:1", "colleague:1"} {
		parts := strings.SplitN(n, ":", 2)
		_ = g.AddNode(n, parts[0])
	}
	_ = g.AddEdge("mgr:1", "worker:1", "manages")     // mgr -[manages]-> worker
	_ = g.AddEdge("worker:1", "colleague:1", "knows") // worker -[knows]-> colleague

	store := newMockStore()
	store.set("mgr", 1, map[string]interface{}{"name": "Alice", "level": 6})
	store.set("worker", 1, map[string]interface{}{"name": "Bob", "dept": "Engineering"})
	store.set("colleague", 1, map[string]interface{}{"name": "Carol", "dept": "Data"})
	return g, store
}

func kl1Query(t *testing.T, q string) []map[string]interface{} {
	t.Helper()
	g, store := buildKL1Graph()
	exec := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()
	ast, hint, err := parser.Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	result, err := exec.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute(%q): %v", q, err)
	}
	return result.Data
}

func TestKL1_WithPipelineNotRoutedToPushDown(t *testing.T) {
	// This query used to fail with "RETURN variable 'contact' not bound in MATCH"
	// because the push-down executor processed the first MATCH as a SQL SELECT
	// and couldn't bind variables introduced in the second MATCH.
	rows := kl1Query(t,
		`MATCH (m:mgr)-[:manages]->(w:worker)
		 WITH w
		 MATCH (w)-[:knows]->(c:colleague)
		 RETURN w.name AS worker, c.name AS colleague`)
	if len(rows) == 0 {
		t.Error("[KL-1] WITH pipeline returned 0 rows; expected Bob → Carol")
	}
}

func TestKL1_WithPipelineWhereFilterOnBothMatches(t *testing.T) {
	// Verify the WITH pipeline still correctly applies WHERE on both sides.
	rows := kl1Query(t,
		`MATCH (m:mgr)-[:manages]->(w:worker)
		 WITH w
		 MATCH (w)-[:knows]->(c:colleague)
		 WHERE c.dept = 'Data'
		 RETURN w.name AS worker, c.dept AS dept`)
	if len(rows) == 0 {
		t.Error("[KL-1] WITH + WHERE: expected rows, got 0")
	}
	for _, row := range rows {
		if dept, _ := row["dept"].(string); dept != "Data" {
			t.Errorf("[KL-1] WHERE filter not applied: dept = %q", dept)
		}
	}
}

func TestKL1_SimpleQueryWithoutWithNotAffected(t *testing.T) {
	// Non-WITH queries must still work as before (BFS or push-down as applicable).
	rows := kl1Query(t,
		`MATCH (w:worker)-[:knows]->(c:colleague)
		 RETURN w.name AS worker, c.name AS colleague`)
	if len(rows) == 0 {
		t.Error("[KL-1] simple query without WITH returned 0 rows; regression suspected")
	}
}
