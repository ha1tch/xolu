// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// kl6_test.go — regression tests for KL-6: converging variable-length patterns.
//
// Pattern:  MATCH (a)-[*]->(m)<-[*]-(b)
//
// The engine must recognise that 'm' appears at the end of both arms,
// expand each arm independently, then intersect on the 'm' node ID.
//
// Graph used:
//
//   dev → bob → alice → zara ← selin ← fatima ← guo
//                             ↑
//                           marcus
//
// Common ancestors of dev and fatima: alice and zara.
// Common ancestors of dev and guo:    alice and zara.
// Common ancestors of dev and bob:    alice and zara (bob can reach alice→zara).

import (
	"context"
	"sort"
	"testing"

	"github.com/ha1tch/xolu/pkg/graph"
)

// buildLCAGraph returns a 7-node management hierarchy.
// All edges are "reports_to": juniors → seniors → zara.
func buildLCAGraph() graph.Graph {
	g := graph.NewFlatGraph()
	nodes := []struct{ id, typ string }{
		{"person:1", "person"}, // zara (apex)
		{"person:2", "person"}, // alice
		{"person:3", "person"}, // bob
		{"person:4", "person"}, // dev
		{"person:5", "person"}, // selin
		{"person:6", "person"}, // fatima
		{"person:7", "person"}, // guo
		{"person:8", "person"}, // marcus
	}
	for _, n := range nodes {
		_ = g.AddNode(n.id, n.typ)
	}
	// Engineering branch: dev(4)→bob(3)→alice(2)→zara(1)
	_ = g.AddEdge("person:4", "person:3", "reports_to") // dev→bob
	_ = g.AddEdge("person:3", "person:2", "reports_to") // bob→alice
	_ = g.AddEdge("person:2", "person:1", "reports_to") // alice→zara
	// Data branch: guo(7)→fatima(6)→selin(5)→zara(1)
	_ = g.AddEdge("person:7", "person:6", "reports_to") // guo→fatima
	_ = g.AddEdge("person:6", "person:5", "reports_to") // fatima→selin
	_ = g.AddEdge("person:5", "person:1", "reports_to") // selin→zara
	// Product branch: marcus(8)→zara(1)
	_ = g.AddEdge("person:8", "person:1", "reports_to") // marcus→zara
	return g
}

func lcaQuery(t *testing.T, g graph.Graph, store *mockStore, q string) []string {
	t.Helper()
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
	var names []string
	for _, row := range result.Data {
		if v, ok := row["ancestor"]; ok {
			names = append(names, v.(string))
		}
	}
	sort.Strings(names)
	return names
}

func buildLCAStore() *mockStore {
	store := newMockStore()
	store.set("person", 1, map[string]interface{}{"name": "Zara", "level": 9})
	store.set("person", 2, map[string]interface{}{"name": "Alice", "level": 6})
	store.set("person", 3, map[string]interface{}{"name": "Bob", "level": 5})
	store.set("person", 4, map[string]interface{}{"name": "Dev", "level": 4})
	store.set("person", 5, map[string]interface{}{"name": "Selin", "level": 8})
	store.set("person", 6, map[string]interface{}{"name": "Fatima", "level": 6})
	store.set("person", 7, map[string]interface{}{"name": "Guo", "level": 5})
	store.set("person", 8, map[string]interface{}{"name": "Marcus", "level": 8})
	return store
}

// ---------------------------------------------------------------------------

func TestKL6_CommonAncestors_DevAndFatima(t *testing.T) {
	g := buildLCAGraph()
	store := buildLCAStore()

	// Dev(4)→Bob→Alice→Zara, Fatima(6)→Selin→Zara.
	// Common ancestors reachable from both via reports_to*: Alice, Zara.
	// (Alice is reachable from Dev in 2 hops; Zara in 3.
	//  Alice is reachable from Fatima in 2 hops via Alice? No — different branches.
	//  Actually: Fatima reaches Selin→Zara. Zara is common; Alice is not reachable from Fatima.)
	// Correct answer: only Zara is reachable from both Dev and Fatima.
	ancestors := lcaQuery(t, g, store,
		`MATCH (a:person)-[:reports_to*1..6]->(m:person)<-[:reports_to*1..6]-(b:person)
		 WHERE a.name = 'Dev' AND b.name = 'Fatima'
		 RETURN DISTINCT m.name AS ancestor`)

	if len(ancestors) == 0 {
		t.Fatal("[KL-6] CommonAncestors DevAndFatima: expected at least 1, got 0")
	}
	// Zara is the only common ancestor (apex of both branches).
	found := false
	for _, a := range ancestors {
		if a == "Zara" {
			found = true
		}
	}
	if !found {
		t.Errorf("[KL-6] Zara not in ancestors: %v", ancestors)
	}
	// Alice must NOT be in the result (not reachable from Fatima via reports_to).
	for _, a := range ancestors {
		if a == "Alice" {
			t.Errorf("[KL-6] Alice incorrectly appears as common ancestor of Dev and Fatima: %v", ancestors)
		}
	}
}

func TestKL6_CommonAncestors_DevAndGuo(t *testing.T) {
	g := buildLCAGraph()
	store := buildLCAStore()

	// Dev reaches: Bob, Alice, Zara.
	// Guo reaches: Fatima, Selin, Zara.
	// Intersection: only Zara.
	ancestors := lcaQuery(t, g, store,
		`MATCH (a:person)-[:reports_to*1..6]->(m:person)<-[:reports_to*1..6]-(b:person)
		 WHERE a.name = 'Dev' AND b.name = 'Guo'
		 RETURN DISTINCT m.name AS ancestor`)

	if len(ancestors) == 0 {
		t.Fatal("[KL-6] CommonAncestors DevAndGuo: expected at least 1, got 0")
	}
	found := false
	for _, a := range ancestors {
		if a == "Zara" {
			found = true
		}
	}
	if !found {
		t.Errorf("[KL-6] Zara not in common ancestors of Dev and Guo: %v", ancestors)
	}
}

func TestKL6_CommonAncestors_DevAndBob(t *testing.T) {
	g := buildLCAGraph()
	store := buildLCAStore()

	// Dev reaches: Bob(1), Alice(2), Zara(3).
	// Bob reaches: Alice(1), Zara(2).
	// Intersection: Alice and Zara.
	ancestors := lcaQuery(t, g, store,
		`MATCH (a:person)-[:reports_to*1..5]->(m:person)<-[:reports_to*1..5]-(b:person)
		 WHERE a.name = 'Dev' AND b.name = 'Bob'
		 RETURN DISTINCT m.name AS ancestor`)

	if len(ancestors) < 2 {
		t.Fatalf("[KL-6] CommonAncestors DevAndBob: expected ≥2 (Alice, Zara), got %v", ancestors)
	}
	expected := map[string]bool{"Alice": true, "Zara": true}
	for _, a := range ancestors {
		if !expected[a] {
			t.Errorf("[KL-6] unexpected ancestor: %q", a)
		}
	}
}

func TestKL6_NoCommonAncestors(t *testing.T) {
	// Build a disconnected graph where a and b share no common ancestor.
	g := graph.NewFlatGraph()
	_ = g.AddNode("person:1", "person") // alice
	_ = g.AddNode("person:2", "person") // bob — no shared ancestor with alice
	// No edges connecting them upward to any shared node.

	store := newMockStore()
	store.set("person", 1, map[string]interface{}{"name": "Alice"})
	store.set("person", 2, map[string]interface{}{"name": "Bob"})

	ancestors := lcaQuery(t, g, store,
		`MATCH (a:person)-[:reports_to*1..3]->(m:person)<-[:reports_to*1..3]-(b:person)
		 WHERE a.name = 'Alice' AND b.name = 'Bob'
		 RETURN DISTINCT m.name AS ancestor`)

	if len(ancestors) != 0 {
		t.Errorf("[KL-6] NoCommonAncestors: expected 0, got %v", ancestors)
	}
}

func TestKL6_SameNode_BothEndpoints(t *testing.T) {
	// When a and b are the same node, the common ancestors are all nodes
	// reachable from that node (including itself if there are self-loops,
	// but in a DAG just the transitive ancestors).
	// Use the full LCA graph: Dev's ancestors are Bob, Alice, Zara.
	g := buildLCAGraph()
	store := buildLCAStore()

	ancestors := lcaQuery(t, g, store,
		`MATCH (a:person)-[:reports_to*1..4]->(m:person)<-[:reports_to*1..4]-(b:person)
		 WHERE a.name = 'Dev' AND b.name = 'Dev'
		 RETURN DISTINCT m.name AS ancestor`)

	// Dev's transitive ancestors: Bob, Alice, Zara — all should appear.
	expected := map[string]bool{"Bob": true, "Alice": true, "Zara": true}
	for _, a := range ancestors {
		if !expected[a] {
			t.Errorf("[KL-6] SameNode: unexpected ancestor %q", a)
		}
	}
	if len(ancestors) < 3 {
		t.Errorf("[KL-6] SameNode: expected ≥3 ancestors for Dev→Dev, got %v", ancestors)
	}
}

func TestKL6_ExistingLinearPatternsUnaffected(t *testing.T) {
	// Verify that the convergence detection doesn't misfire on a normal
	// linear two-pattern MATCH where the start variable of the second
	// pattern is already bound (KL-7 case, not KL-6).
	g := buildLCAGraph()
	store := buildLCAStore()

	// MATCH (a:person)-[:reports_to]->(m:person), (m)-[:reports_to]->(z:person)
	// This is not a convergence pattern: m is the *start* of the second arm.
	rows := func() []map[string]interface{} {
		exec := NewExecutor(g, 10).WithStore(store)
		parser := NewParser()
		ast, hint, _ := parser.Parse(
			`MATCH (a:person)-[:reports_to]->(m:person), (m)-[:reports_to]->(z:person)
			 RETURN a.name AS junior, m.name AS mid, z.name AS senior`)
		result, err := exec.Execute(context.Background(), ast, hint)
		if err != nil {
			return nil
		}
		return result.Data
	}()

	if len(rows) == 0 {
		t.Error("[KL-6] Linear pattern regression: MATCH (a)→(m), (m)→(z) returned 0 rows")
	}
}

func TestKL6_SequentialContinuationNotMistakenForConvergence(t *testing.T) {
	// Reviewer identified: after KL-7, many envs already contain variables.
	// Detection must not fire on (a)-[*]->(m), (m)-[r]->(c) where m is bound
	// as the terminal of the first arm but is the *start* of the second arm.
	// That is sequential continuation (KL-7), not convergence (KL-6).
	g := graph.NewFlatGraph()
	_ = g.AddNode("person:1", "person")            // alice
	_ = g.AddNode("person:2", "person")            // bob (m)
	_ = g.AddNode("person:3", "person")            // carol (c)
	_ = g.AddEdge("person:1", "person:2", "knows") // alice→bob
	_ = g.AddEdge("person:2", "person:3", "knows") // bob→carol

	store := newMockStore()
	store.set("person", 1, map[string]interface{}{"name": "Alice"})
	store.set("person", 2, map[string]interface{}{"name": "Bob"})
	store.set("person", 3, map[string]interface{}{"name": "Carol"})

	exec := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()
	ast, hint, err := parser.Parse(
		`MATCH (a:person)-[:knows*1..2]->(m:person), (m)-[:knows]->(c:person)
		 RETURN a.name AS a_name, m.name AS mid, c.name AS c_name`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result, err := exec.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// alice→bob→carol: a=Alice, m=Bob, c=Carol. Exactly 1 row.
	if len(result.Data) != 1 {
		t.Errorf("[KL-6] sequential continuation: expected 1 row, got %d: %v",
			len(result.Data), result.Data)
	}
}

func TestKL6_CommaPatternConvergence(t *testing.T) {
	// Comma-separated convergence (two separate MATCH patterns, same m terminal):
	//   MATCH (a)-[*]->(m), (b)-[*]->(m)
	// where m is the terminal of *both* arms in allParts[1:] loop.
	g := buildLCAGraph()
	store := buildLCAStore()

	exec := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()
	// Express as comma-separated (routes through multi-part loop, not tryConvergenceJoin).
	ast, hint, err := parser.Parse(
		`MATCH (a:person)-[:reports_to*1..4]->(m:person), (b:person)-[:reports_to*1..4]->(m)
		 WHERE a.name = 'Dev' AND b.name = 'Fatima'
		 RETURN DISTINCT m.name AS ancestor`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result, err := exec.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	found := false
	for _, row := range result.Data {
		if row["ancestor"] == "Zara" {
			found = true
		}
	}
	if !found {
		t.Errorf("[KL-6] comma-separated convergence: Zara not in %v", result.Data)
	}
}
