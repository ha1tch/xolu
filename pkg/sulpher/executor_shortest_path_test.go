// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_shortest_path_test.go — tests for Stage 2.10 shortestPath queries.
//
// Graph layout for these tests:
//
//   alice -> bob -> carol -> dave
//   alice -> carol  (shortcut)
//   eve (isolated — no edges)
//
// Edges all have relationship "knows".

import (
	"context"
	"testing"

	"github.com/ha1tch/xolu/pkg/graph"
)

// buildSPGraph builds a graph with numeric IDs usable with the mock store.
//
//	user:1 (Alice) -> user:2 (Bob)   -> user:3 (Carol) -> user:4 (Dave)
//	user:1 (Alice) -> user:3 (Carol)   (shortcut)
//	user:5 (Eve)   -- isolated
func buildSPGraph() (graph.Graph, *mockStore) {
	g := graph.NewFlatGraph()
	for i := 1; i <= 5; i++ {
		g.AddNode("user:"+intStr(i), "user")
	}
	g.AddEdge("user:1", "user:2", "knows")
	g.AddEdge("user:2", "user:3", "knows")
	g.AddEdge("user:3", "user:4", "knows")
	g.AddEdge("user:1", "user:3", "knows") // shortcut

	store := newMockStore()
	store.set("user", 1, map[string]interface{}{"name": "Alice"})
	store.set("user", 2, map[string]interface{}{"name": "Bob"})
	store.set("user", 3, map[string]interface{}{"name": "Carol"})
	store.set("user", 4, map[string]interface{}{"name": "Dave"})
	store.set("user", 5, map[string]interface{}{"name": "Eve"})

	return g, store
}

func spQuery(t *testing.T, q string) []map[string]interface{} {
	t.Helper()
	g, store := buildSPGraph()
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

// ── Basic shortestPath tests ──────────────────────────────────────────────────

// getPathNodes extracts the nodes list from a path object returned by RETURN p.
// In the Env-based model, p is {"nodes": [...], "relationships": [...], "length": n}.
func getPathNodes(t *testing.T, pathVal interface{}) []interface{} {
	t.Helper()
	if pathVal == nil {
		t.Fatal("path is nil")
	}
	// New Env model: path is a map with "nodes" key
	if m, ok := pathVal.(map[string]interface{}); ok {
		if nodes, ok := m["nodes"].([]interface{}); ok {
			return nodes
		}
		t.Fatalf("path map has no 'nodes' key: %v", m)
	}
	// Legacy: flat []interface{} (backward compat)
	if s, ok := pathVal.([]interface{}); ok {
		return s
	}
	t.Fatalf("expected path object, got %T: %v", pathVal, pathVal)
	return nil
}

func TestShortestPath_Basic(t *testing.T) {
	t.Parallel()
	// Alice(1) to Dave(4): shortest is 1→3→4 (2 hops via shortcut) OR 1→2→3→4
	// FindPath uses BFS so should return shortest: 1→3→4
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '4'})) RETURN p")
	if len(rows) == 0 {
		t.Fatal("expected a path from user:1 to user:4")
	}
	path := getPathNodes(t, rows[0]["p"])
	// Shortest should be 3 nodes (2 hops): 1→3→4
	if len(path) != 3 {
		t.Errorf("expected 3 nodes in shortest path, got %d: %v", len(path), path)
	}
}

func TestShortestPath_DirectNeighbour(t *testing.T) {
	t.Parallel()
	// Alice(1) to Bob(2): direct edge, 1 hop
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '2'})) RETURN p")
	if len(rows) == 0 {
		t.Fatal("expected a path")
	}
	path := getPathNodes(t, rows[0]["p"])
	if len(path) != 2 {
		t.Errorf("expected 2 nodes (direct), got %d", len(path))
	}
}

func TestShortestPath_SameNode(t *testing.T) {
	t.Parallel()
	// Same start and end — should return no results (we skip fromID==toID)
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '1'})) RETURN p")
	// Our implementation skips fromID == toID pairs
	if len(rows) != 0 {
		t.Errorf("expected no results for same-node shortestPath, got %d", len(rows))
	}
}

func TestShortestPath_NoPath(t *testing.T) {
	t.Parallel()
	// Eve(5) is isolated — no path to Dave
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '5'})-[:knows*]-(b:user {id: '4'})) RETURN p")
	if len(rows) != 0 {
		t.Errorf("expected no results for disconnected nodes, got %d", len(rows))
	}
}

func TestShortestPath_ReturnLength(t *testing.T) {
	t.Parallel()
	// RETURN p.length gives the number of hops
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '4'})) RETURN p.length")
	if len(rows) == 0 {
		t.Fatal("expected a result")
	}
	length := rows[0]["p.length"]
	// Shortest is 2 hops (1→3→4)
	if length != 2 {
		t.Errorf("expected p.length=2, got %v", length)
	}
}

func TestShortestPath_ReturnNodes(t *testing.T) {
	t.Parallel()
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '3'})) RETURN p.nodes")
	if len(rows) == 0 {
		t.Fatal("expected a result")
	}
	nodes, ok := rows[0]["p.nodes"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", rows[0]["p.nodes"])
	}
	// Alice→Carol: 2 nodes
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestShortestPath_WithoutRelType(t *testing.T) {
	t.Parallel()
	// No relationship type constraint — should still find path
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '1'})-[*]-(b:user {id: '4'})) RETURN p")
	if len(rows) == 0 {
		t.Fatal("expected a path without relationship type constraint")
	}
}

func TestShortestPath_WrongRelType(t *testing.T) {
	t.Parallel()
	// Relationship type "likes" doesn't exist — no path
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '1'})-[:likes*]-(b:user {id: '4'})) RETURN p")
	if len(rows) != 0 {
		t.Errorf("expected no results for non-existent relationship type, got %d", len(rows))
	}
}

func TestShortestPath_MaxDepthRespected(t *testing.T) {
	t.Parallel()
	// Alice(1) to Dave(4): shortest path is 2 hops, but with maxDepth=1 should fail
	g, store := buildSPGraph()
	executor := NewExecutor(g, 1).WithStore(store) // maxDepth=1
	parser := NewParser()
	ast, hint, err := parser.Parse("MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '4'})) RETURN p")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// With maxDepth=1, only direct neighbours are reachable; user:4 is 2+ hops away
	if len(result.Data) != 0 {
		t.Errorf("expected no results with maxDepth=1, got %d", len(result.Data))
	}
}

func TestShortestPath_AllPairsInType(t *testing.T) {
	t.Parallel()
	// Without specific start/end, matches all user→user shortestPath pairs
	// where a path exists. We just check it returns multiple rows.
	rows := spQuery(t, "MATCH p = shortestPath((a:user)-[:knows*]-(b:user)) RETURN p.length")
	// With 5 users and 4 edges, many pairs have paths; just verify multiple results
	if len(rows) == 0 {
		t.Error("expected multiple shortestPath results")
	}
}

// ── RETURN shortestPath(...) form ─────────────────────────────────────────────

func TestShortestPath_InReturn(t *testing.T) {
	t.Parallel()
	// The alternative RETURN shortestPath(...) form requires bound variables
	// from a prior MATCH with comma-separated patterns (Stage 2.4).
	// Until Stage 2.4 is implemented, this query returns a parse/execute error
	// for the comma-separated MATCH — verify it doesn't panic.
	g, store := buildSPGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()
	ast, hint, err := parser.Parse(
		"MATCH (a:user {id: '1'}), (b:user {id: '4'}) " +
			"RETURN shortestPath((a)-[:knows*]-(b))")
	if err != nil {
		// Parse error is acceptable — not yet supported.
		return
	}
	// If parse succeeds, execute should either return results or a clean error.
	_, execErr := executor.Execute(context.Background(), ast, hint)
	if execErr != nil {
		// Clean error is acceptable for unimplemented Stage 2.4 feature.
		return
	}
	// If it works, great — don't assert anything specific yet.
}

// ── Node content verification ─────────────────────────────────────────────────

func TestShortestPath_NodeContent(t *testing.T) {
	t.Parallel()
	// Verify that path nodes contain hydrated data and _id fields
	rows := spQuery(t, "MATCH p = shortestPath((a:user {id: '1'})-[:knows*]-(b:user {id: '3'})) RETURN p")
	if len(rows) == 0 {
		t.Fatal("expected a result")
	}
	path := getPathNodes(t, rows[0]["p"])
	for i, nodeI := range path {
		node, ok := nodeI.(map[string]interface{})
		if !ok {
			t.Errorf("node %d: expected map, got %T", i, nodeI)
			continue
		}
		if _, ok := node["_id"]; !ok {
			t.Errorf("node %d: missing _id field", i)
		}
		if _, ok := node["type"]; !ok {
			t.Errorf("node %d: missing type field", i)
		}
	}
}
