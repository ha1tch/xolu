// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_multi_match_test.go — tests for comma-separated MATCH patterns.
//
// MATCH (a:User), (b:Item) — Cartesian product of two independent node sets.
// MATCH (a:User), (b:Item)-[:OWNS]->(c:Tag) — cross-join of node set × traversal.

import (
	"context"
	"testing"

	"github.com/ha1tch/xolu/pkg/graph"
)

// buildMultiMatchGraph builds:
//
//	user:1 (Alice), user:2 (Bob)
//	product:1 (Widget), product:2 (Gadget)
//	tag:1 (cheap), tag:2 (premium)
//
//	user:1 -[LIKES]-> product:1
//	user:2 -[LIKES]-> product:2
//	product:1 -[TAGGED]-> tag:1
//	product:2 -[TAGGED]-> tag:2
func buildMultiMatchGraph() (graph.Graph, *mockStore) {
	g := graph.NewFlatGraph()
	g.AddNode("user:1", "user")
	g.AddNode("user:2", "user")
	g.AddNode("product:1", "product")
	g.AddNode("product:2", "product")
	g.AddNode("tag:1", "tag")
	g.AddNode("tag:2", "tag")

	g.AddEdge("user:1", "product:1", "LIKES")
	g.AddEdge("user:2", "product:2", "LIKES")
	g.AddEdge("product:1", "tag:1", "TAGGED")
	g.AddEdge("product:2", "tag:2", "TAGGED")

	store := newMockStore()
	store.set("user", 1, map[string]interface{}{"name": "Alice"})
	store.set("user", 2, map[string]interface{}{"name": "Bob"})
	store.set("product", 1, map[string]interface{}{"name": "Widget", "price": 10})
	store.set("product", 2, map[string]interface{}{"name": "Gadget", "price": 99})
	store.set("tag", 1, map[string]interface{}{"label": "cheap"})
	store.set("tag", 2, map[string]interface{}{"label": "premium"})

	return g, store
}

func TestMultiMatch_CartesianProduct(t *testing.T) {
	t.Parallel()
	// MATCH (u:user), (p:product) — 2 users × 2 products = 4 rows.
	g, store := buildMultiMatchGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user), (p:product) RETURN u.name, p.name`)
	if len(rows) != 4 {
		t.Errorf("want 4 rows (2×2 cross), got %d: %v", len(rows), rows)
	}
	// Every (user, product) pair should appear exactly once.
	pairs := map[string]int{}
	for _, r := range rows {
		key := str(r["u.name"]) + ":" + str(r["p.name"])
		pairs[key]++
	}
	for _, pair := range []string{"Alice:Widget", "Alice:Gadget", "Bob:Widget", "Bob:Gadget"} {
		if pairs[pair] != 1 {
			t.Errorf("pair %q: want 1, got %d", pair, pairs[pair])
		}
	}
}

func TestMultiMatch_CartesianProductWithWhere(t *testing.T) {
	t.Parallel()
	// Cross then filter: only the expensive product.
	g, store := buildMultiMatchGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user), (p:product) WHERE p.price > 50 RETURN u.name, p.name`)
	if len(rows) != 2 {
		t.Errorf("want 2 rows (2 users × 1 expensive product), got %d: %v", len(rows), rows)
	}
	for _, r := range rows {
		if str(r["p.name"]) != "Gadget" {
			t.Errorf("expected only Gadget, got %v", r["p.name"])
		}
	}
}

func TestMultiMatch_NodeAndTraversal(t *testing.T) {
	t.Parallel()
	// MATCH (u:user), (p:product)-[:TAGGED]->(t:tag)
	// 2 users × 2 (product→tag) traversal results = 4 rows.
	g, store := buildMultiMatchGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user), (p:product)-[:TAGGED]->(t:tag) RETURN u.name, p.name, t.label`)
	if len(rows) != 4 {
		t.Errorf("want 4 rows (2 users × 2 product→tag paths), got %d: %v", len(rows), rows)
	}
}

func TestMultiMatch_SingleNodeEachPart(t *testing.T) {
	t.Parallel()
	// MATCH (u:user), (t:tag) — 2 × 2 = 4.
	g, store := buildMultiMatchGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user), (t:tag) RETURN u.name, t.label`)
	if len(rows) != 4 {
		t.Errorf("want 4 rows, got %d: %v", len(rows), rows)
	}
}

func TestMultiMatch_ThreeParts(t *testing.T) {
	t.Parallel()
	// MATCH (u:user), (p:product), (t:tag) — 2 × 2 × 2 = 8 rows.
	g, store := buildMultiMatchGraph()
	rows := runQuery(t, g, store,
		`MATCH (u:user), (p:product), (t:tag) RETURN u.name, p.name, t.label`)
	if len(rows) != 8 {
		t.Errorf("want 8 rows (2×2×2), got %d: %v", len(rows), rows)
	}
}

func TestMultiMatch_EmptyPartYieldsEmpty(t *testing.T) {
	t.Parallel()
	// MATCH (u:user), (x:nonexistent) — second part empty → result empty.
	g, store := buildMultiMatchGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()
	ast, hint, err := parser.Parse(`MATCH (u:user), (x:nonexistent) RETURN u.name`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) != 0 {
		t.Errorf("want 0 rows when second part matches nothing, got %d", len(result.Data))
	}
}

// str safely converts an interface{} to string for test assertions.
func str(v interface{}) string {
	if v == nil {
		return "<nil>"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return "<non-string>"
}

// ── KL-7 regression tests ─────────────────────────────────────────────────────
//
// KL-7: comma-separated MATCH patterns where the second pattern's start
// variable is already bound in the accumulated envs.
//
// Before the fix, the second BFS was seeded from all nodes of the start
// variable's type rather than from the specific bound node, producing an
// inflated result set. The fix: when the start variable is bound, seed the
// BFS only from the bound node.

// buildKL7Graph returns:
//
//	a --[r]--> m --[hop]--> c
//	                |
//	                \--[hop]--> d   (m has two outgoing hop edges)
//
// x --[r]--> m as well: m is reachable from both a and x.
// After the BFS fix, both sources must produce results.
func buildKL7Graph() (graph.Graph, *mockStore) {
	g := graph.NewFlatGraph()
	for _, n := range []struct{ id, typ string }{
		{"node:1", "node"}, // a
		{"node:2", "node"}, // m
		{"node:3", "node"}, // c
		{"node:4", "node"}, // d
		{"node:5", "node"}, // x (also reaches m via :r)
	} {
		g.AddNode(n.id, n.typ)
	}
	g.AddEdge("node:1", "node:2", "r")   // a→m
	g.AddEdge("node:2", "node:3", "hop") // m→c
	g.AddEdge("node:2", "node:4", "hop") // m→d
	g.AddEdge("node:5", "node:2", "r")   // x→m

	s := newMockStore()
	s.set("node", 1, map[string]interface{}{"name": "a"})
	s.set("node", 2, map[string]interface{}{"name": "m"})
	s.set("node", 3, map[string]interface{}{"name": "c"})
	s.set("node", 4, map[string]interface{}{"name": "d"})
	s.set("node", 5, map[string]interface{}{"name": "x"})
	return g, s
}

// TestKL7_BoundStartSeededFromExactNode verifies that when the second
// pattern's start variable is already bound, the traversal is seeded
// only from that bound node — not from all nodes of its type.
//
// After the BFS completeness fix (origin-scoped visited set), the first arm
// MATCH (a:node)-[:r]->(m:node) must return BOTH {a=node:1, m=node:2} AND
// {a=node:5, m=node:2} — both sources reach m.  Filtered to a.name='a',
// exactly 2 rows result: a→m→c and a→m→d.
func TestKL7_BoundStartSeededFromExactNode(t *testing.T) {
	g, s := buildKL7Graph()
	rows := runQuery(t, g, s,
		`MATCH (a:node)-[:r]->(m:node), (m)-[:hop]->(c:node)
		 WHERE a.name = 'a'
		 RETURN a, m, c`)

	// Expected exactly 2 rows: (a→m→c) and (a→m→d).
	// Before the origin-scoped BFS fix: whichever of {a, x} won the map
	// iteration race owned the m visited entry; if x won, a was silently
	// dropped and the WHERE returned 0 rows. After the fix: both sources
	// produce their own results; WHERE a.name='a' selects only the a rows.
	if len(rows) != 2 {
		t.Errorf("[KL-7] expected 2 rows (a→m→c, a→m→d), got %d", len(rows))
		for _, r := range rows {
			t.Logf("  row: %v", r)
		}
	}

	// Every row must have a.name='a' and m.name='m'.
	for _, row := range rows {
		if aMap, _ := row["a"].(map[string]interface{}); aMap["name"] != "a" {
			t.Errorf("[KL-7] a.name should be 'a', got %v", aMap["name"])
		}
		if mMap, _ := row["m"].(map[string]interface{}); mMap["name"] != "m" {
			t.Errorf("[KL-7] m.name should be 'm', got %v", mMap["name"])
		}
	}

	// c must be only 'c' or 'd'.
	valid := map[string]bool{"c": true, "d": true}
	for _, row := range rows {
		cMap, _ := row["c"].(map[string]interface{})
		if !valid[str(cMap["name"])] {
			t.Errorf("[KL-7] c.name = %q is not a valid hop destination from m", cMap["name"])
		}
	}
}

// TestKL7_BoundStartCartesianWhenUnbound verifies that when the second
// pattern introduces an entirely unrelated variable, the original Cartesian
// behaviour is preserved. After the origin-fix, both a→m and x→m are
// returned from the first arm (2 results), crossed with 5 node-type nodes
// for z, giving 10 rows.
func TestKL7_BoundStartCartesianWhenUnbound(t *testing.T) {
	g, s := buildKL7Graph()
	// MATCH (a:node)-[:r]->(m:node), (z:node) — z is unbound; Cartesian.
	// First arm produces 2 envs: {a=node:1,m=node:2} and {a=node:5,m=node:2}.
	// Cross with 5 node-type nodes for z: 2 × 5 = 10 rows.
	rows := runQuery(t, g, s,
		`MATCH (a:node)-[:r]->(m:node), (z:node)
		 RETURN a, m, z`)
	if len(rows) != 10 {
		t.Errorf("[KL-7] Cartesian with unbound z: expected 10 rows (2 a→m × 5 nodes), got %d", len(rows))
	}
}

// TestKL7_ThreePatternChain verifies a three-part MATCH where each part
// constrains the next via a shared variable.
func TestKL7_ThreePatternChainEmptyTerminal(t *testing.T) {
	g, s := buildKL7Graph()
	// c (node:3 or node:4) has no outgoing :r edges, so z is unreachable.
	rows := runQuery(t, g, s,
		`MATCH (a:node)-[:r]->(m:node), (m)-[:hop]->(c:node), (c)-[:r]->(z:node)
		 WHERE a.name = 'a'
		 RETURN a, m, c, z`)
	if len(rows) != 0 {
		t.Errorf("[KL-7] three-pattern chain with dead terminal: expected 0 rows, got %d", len(rows))
	}
}
