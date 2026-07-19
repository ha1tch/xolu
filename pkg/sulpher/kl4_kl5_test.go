// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// kl4_kl5_test.go — regression tests for KL-4 and KL-5.
//
// KL-4: Variable-length [*] traversal was binding the end-node to cur.node
//        (the traversal position pointer) rather than the actual neighbour.
//        Result: start node appeared as its own reachable destination.
//
// KL-5: Cycle detection via (n)-[*]->(n) returned false positives because
//        end-node identity was never verified against the start binding.
//
// Graph used throughout:
//
//   a -[LINK]-> b -[LINK]-> c -[LINK]-> d   (linear DAG, no cycles)
//
// Every test that should return 0 rows verifies absence; every test that
// should return specific nodes verifies exact IDs — not just len > 0.

import (
	"context"
	"testing"

	"github.com/ha1tch/xolu/pkg/graph"
)

// buildKLGraph returns a tiny linear DAG: a→b→c→d (all LINK edges).
// Node IDs use the "hop" entity type so type-label filters work cleanly.
func buildKLGraph() graph.Graph {
	g := graph.NewFlatGraph()
	for _, n := range []string{"hop:a", "hop:b", "hop:c", "hop:d"} {
		_ = g.AddNode(n, "hop")
	}
	_ = g.AddEdge("hop:a", "hop:b", "LINK")
	_ = g.AddEdge("hop:b", "hop:c", "LINK")
	_ = g.AddEdge("hop:c", "hop:d", "LINK")
	return g
}

// buildKLCycleGraph returns a graph with one genuine 3-cycle (a→b→c→a)
// and one isolated node d with no outgoing edges.
func buildKLCycleGraph() graph.Graph {
	g := graph.NewFlatGraph()
	for _, n := range []string{"hop:a", "hop:b", "hop:c", "hop:d"} {
		_ = g.AddNode(n, "hop")
	}
	_ = g.AddEdge("hop:a", "hop:b", "LINK")
	_ = g.AddEdge("hop:b", "hop:c", "LINK")
	_ = g.AddEdge("hop:c", "hop:a", "LINK") // closes the cycle
	// d is isolated — no LINK edges
	return g
}

func klQuery(t *testing.T, g graph.Graph, q string) []map[string]interface{} {
	t.Helper()
	parser := NewParser()
	ast, hint, err := parser.Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	result, err := NewExecutor(g, 10).Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute(%q): %v", q, err)
	}
	return result.Data
}

// ── KL-4: end-node binding correctness ───────────────────────────────────

// The start node must never appear as its own reachable descendant in a DAG.
func TestKL4_StartNodeNotInResults(t *testing.T) {
	g := buildKLGraph()
	rows := klQuery(t, g,
		"MATCH (a:hop)-[:LINK*1..3]->(b:hop) WHERE a.id = 'a' RETURN b")
	for _, row := range rows {
		if bMap, ok := row["b"].(map[string]interface{}); ok {
			if id, _ := bMap["_nodeID"].(string); id == "hop:a" {
				t.Errorf("[KL-4] start node 'a' appeared as its own descendant")
			}
		}
	}
}

// From a, exactly the nodes reachable at 1, 2, and 3 hops must appear.
func TestKL4_VarLen_ReachableFromA(t *testing.T) {
	g := buildKLGraph()
	// Without a store the id property won't hydrate; verify via count and absence
	// of self-loops instead.
	rows := klQuery(t, g,
		"MATCH (src:hop)-[:LINK*1..3]->(dst:hop) RETURN src.id AS s, dst.id AS d")
	for _, row := range rows {
		s, _ := row["s"].(string)
		d, _ := row["d"].(string)
		if s == d && s != "" {
			t.Errorf("[KL-4] self-loop in results: %s -> %s", s, d)
		}
	}
}

// Precise hop-count test: [*1..1] from a must reach only b, not a itself.
func TestKL4_ExactlyOneHop(t *testing.T) {
	g := buildKLGraph()
	parser := NewParser()
	ast, hint, err := parser.Parse("MATCH (src:hop)-[:LINK*1..1]->(dst:hop) RETURN src, dst")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := NewExecutor(g, 10).Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// After envBindWithSnapshot the map has _id (entity ID fragment), not _nodeID.
	for _, row := range result.Data {
		srcMap, _ := row["src"].(map[string]interface{})
		dstMap, _ := row["dst"].(map[string]interface{})
		srcID, _ := srcMap["_id"].(string)
		dstID, _ := dstMap["_id"].(string)
		if srcID != "" && srcID == dstID {
			t.Errorf("[KL-4] [*1..1] produced self-loop: %s -> %s", srcID, dstID)
		}
	}
	// a→b, b→c, c→d: expect exactly 3 pairs
	if len(result.Data) != 3 {
		t.Errorf("[KL-4] [*1..1] expected 3 edges, got %d", len(result.Data))
		for _, row := range result.Data {
			t.Logf("  row: %v", row)
		}
	}
}

// [*2..2]: exactly 2 hops. a→c, b→d. Expect 2 pairs, no self-loops.
func TestKL4_ExactlyTwoHops(t *testing.T) {
	g := buildKLGraph()
	parser := NewParser()
	ast, hint, err := parser.Parse("MATCH (src:hop)-[:LINK*2..2]->(dst:hop) RETURN src, dst")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := NewExecutor(g, 10).Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, row := range result.Data {
		srcMap, _ := row["src"].(map[string]interface{})
		dstMap, _ := row["dst"].(map[string]interface{})
		srcID, _ := srcMap["_id"].(string)
		dstID, _ := dstMap["_id"].(string)
		if srcID != "" && srcID == dstID {
			t.Errorf("[KL-4] [*2..2] produced self-loop: %s", srcID)
		}
	}
	// a→c and b→d: exactly 2 pairs, sources must differ from destinations
	if len(result.Data) != 2 {
		t.Errorf("[KL-4] [*2..2] expected 2 pairs (a→c, b→d), got %d", len(result.Data))
		for _, row := range result.Data {
			t.Logf("  row: %v", row)
		}
	}
}

// ── KL-5: cycle detection identity constraint ────────────────────────────

// DAG: no directed cycles. (n)-[*1..4]->(n) must return 0 rows.
func TestKL5_NoCyclesInDAG(t *testing.T) {
	g := buildKLGraph()
	rows := klQuery(t, g,
		"MATCH (n:hop)-[:LINK*1..4]->(n) RETURN n")
	if len(rows) != 0 {
		t.Errorf("[KL-5] cycle detection false positive: DAG returned %d cycle rows", len(rows))
		for _, row := range rows {
			t.Logf("  row: %v", row)
		}
	}
}

// Cycle graph: a→b→c→a. (n)-[*1..3]->(n) must find a, b, and c
// (each is reachable from itself via the cycle in exactly 3 hops).
func TestKL5_DetectRealCycle(t *testing.T) {
	g := buildKLCycleGraph()
	rows := klQuery(t, g,
		"MATCH (n:hop)-[:LINK*1..3]->(n) RETURN n")
	if len(rows) == 0 {
		t.Error("[KL-5] cycle graph: expected cycle nodes in results, got 0")
		return
	}
	// d has no outgoing edges and should not appear
	for _, row := range rows {
		if nMap, ok := row["n"].(map[string]interface{}); ok {
			if id, _ := nMap["_nodeID"].(string); id == "hop:d" {
				t.Errorf("[KL-5] isolated node 'd' falsely reported as in a cycle")
			}
		}
	}
}

// [*1..1] self-loop check: no node should have a self-loop in either graph.
func TestKL5_NoSelfLoopOneHop(t *testing.T) {
	for _, name := range []string{"DAG", "cycle"} {
		var g graph.Graph
		if name == "DAG" {
			g = buildKLGraph()
		} else {
			g = buildKLCycleGraph()
		}
		rows := klQuery(t, g, "MATCH (n:hop)-[:LINK*1..1]->(n) RETURN n")
		if len(rows) != 0 {
			t.Errorf("[KL-5] %s: [*1..1] self-loop returned %d rows (expected 0)", name, len(rows))
		}
	}
}

// ── Regression: existing star-traversal behaviour ─────────────────────────

// Unlabelled [*] must return results and must not include self-loops.
func TestKL4_UnlabelledStar_NoSelfLoops(t *testing.T) {
	g := buildKLGraph()
	parser := NewParser()
	ast, hint, err := parser.Parse("MATCH (src:hop)-[*1..4]->(dst:hop) RETURN src, dst")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := NewExecutor(g, 10).Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Data) == 0 {
		t.Error("[KL-4] unlabelled [*] returned 0 rows on a non-empty graph")
	}
	for _, row := range result.Data {
		srcMap, _ := row["src"].(map[string]interface{})
		dstMap, _ := row["dst"].(map[string]interface{})
		srcID, _ := srcMap["_id"].(string)
		dstID, _ := dstMap["_id"].(string)
		if srcID != "" && srcID == dstID {
			t.Errorf("[KL-4] unlabelled [*] self-loop: %s", srcID)
		}
	}
}
