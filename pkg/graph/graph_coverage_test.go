// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package graph — coverage tests for zero-coverage functions.
//
// Covers:
//   NewFlatGraphWithLogger    (0% → covered)
//   CheckEdge                 (0% → covered: happy, dup, cycle, cross-tenant)
//   AddEdgeWithProps          (0% → covered)
//   AddEdgeWithID             (0% → covered)
//   FindPathDirected          (0% → all three PathDir variants + error paths)
//   AllShortestPaths          (0% → covered)

package graph

import (
	"testing"

	"github.com/rs/zerolog"
)

// setupDirectedGraph builds a small directed graph suitable for path tests:
//
//	A → B → C → D
//	        ↑
//	        E
func setupDirectedGraph(t *testing.T) (*FlatGraph, map[string]string) {
	t.Helper()
	g := NewFlatGraph()
	nodes := map[string]string{
		"A": "thing:1",
		"B": "thing:2",
		"C": "thing:3",
		"D": "thing:4",
		"E": "thing:5",
	}
	for _, id := range nodes {
		g.AddNode(id, "thing")
	}
	for _, pair := range [][2]string{
		{"thing:1", "thing:2"}, // A→B
		{"thing:2", "thing:3"}, // B→C
		{"thing:3", "thing:4"}, // C→D
		{"thing:5", "thing:3"}, // E→C
	} {
		if err := g.AddEdge(pair[0], pair[1], "LINK"); err != nil {
			t.Fatalf("AddEdge %v: %v", pair, err)
		}
	}
	return g, nodes
}

// ---------------------------------------------------------------------------
// NewFlatGraphWithLogger
// ---------------------------------------------------------------------------

func TestNewFlatGraphWithLogger(t *testing.T) {
	logger := zerolog.Nop()
	g := NewFlatGraphWithLogger(logger)
	if g == nil {
		t.Fatal("NewFlatGraphWithLogger returned nil")
	}
	// Verify it is functional.
	g.AddNode("x:1", "x")
	if g.NodeCount() != 1 {
		t.Errorf("NodeCount() = %d, want 1", g.NodeCount())
	}
}

// ---------------------------------------------------------------------------
// CheckEdge
// ---------------------------------------------------------------------------

func TestCheckEdge(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("person:1", "person")
	g.AddNode("person:2", "person")

	// Non-existent edge — no conflict.
	if err := g.CheckEdge("person:1", "person:2", "KNOWS"); err != nil {
		t.Errorf("CheckEdge (new edge): %v", err)
	}

	// Add the edge then check again — idempotent, same label returns nil.
	g.AddEdge("person:1", "person:2", "KNOWS")
	if err := g.CheckEdge("person:1", "person:2", "KNOWS"); err != nil {
		t.Errorf("CheckEdge (existing, same label): %v", err)
	}

	// Existing edge with different label — ErrEdgeAlreadyExists.
	if err := g.CheckEdge("person:1", "person:2", "FOLLOWS"); err != ErrEdgeAlreadyExists {
		t.Errorf("CheckEdge (different label): got %v, want ErrEdgeAlreadyExists", err)
	}

	// Cross-tenant edge — ErrCrossTenantEdge.
	g.AddNode("0001@person:1", "person")
	g.AddNode("0002@person:1", "person")
	err := g.CheckEdge("0001@person:1", "0002@person:1", "X")
	if err == nil {
		t.Error("CheckEdge (cross-tenant): expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// AddEdgeWithProps
// ---------------------------------------------------------------------------

func TestAddEdgeWithProps(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("a:1", "a")
	g.AddNode("b:1", "b")

	// Props are accepted but not stored in-memory.
	if err := g.AddEdgeWithProps("a:1", "b:1", "LINKS", map[string]interface{}{
		"weight": 1.5,
		"label":  "test",
	}); err != nil {
		t.Errorf("AddEdgeWithProps: %v", err)
	}

	// Nil props — equivalent to AddEdge.
	g.AddNode("a:2", "a")
	g.AddNode("b:2", "b")
	if err := g.AddEdgeWithProps("a:2", "b:2", "LINKS", nil); err != nil {
		t.Errorf("AddEdgeWithProps (nil props): %v", err)
	}

	if g.EdgeCount() != 2 {
		t.Errorf("EdgeCount() = %d, want 2", g.EdgeCount())
	}
}

// ---------------------------------------------------------------------------
// AddEdgeWithID
// ---------------------------------------------------------------------------

func TestAddEdgeWithID(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("n:1", "n")
	g.AddNode("n:2", "n")

	if err := g.AddEdgeWithID("n:1", "n:2", "REL", 42); err != nil {
		t.Errorf("AddEdgeWithID: %v", err)
	}

	// Duplicate call with same ID should be idempotent (same label).
	if err := g.AddEdgeWithID("n:1", "n:2", "REL", 42); err != nil {
		t.Errorf("AddEdgeWithID (duplicate): %v", err)
	}

	// edgeID = 0 — still adds the edge, skips patching.
	g.AddNode("n:3", "n")
	if err := g.AddEdgeWithID("n:1", "n:3", "REL", 0); err != nil {
		t.Errorf("AddEdgeWithID (id=0): %v", err)
	}
}

// ---------------------------------------------------------------------------
// FindPathDirected
// ---------------------------------------------------------------------------

func TestFindPathDirected_Outgoing(t *testing.T) {
	g, _ := setupDirectedGraph(t)

	// A→B→C→D: should find path outgoing.
	path, err := g.FindPathDirected("thing:1", "thing:4", 10, PathDirOutgoing)
	if err != nil {
		t.Fatalf("FindPathDirected (outgoing): %v", err)
	}
	if len(path) == 0 {
		t.Error("FindPathDirected (outgoing): empty path")
	}
	if path[0] != "thing:1" || path[len(path)-1] != "thing:4" {
		t.Errorf("FindPathDirected (outgoing): path = %v", path)
	}
}

func TestFindPathDirected_Incoming(t *testing.T) {
	g, _ := setupDirectedGraph(t)

	// Reverse: D←C←B←A — incoming from D should reach A.
	path, err := g.FindPathDirected("thing:4", "thing:1", 10, PathDirIncoming)
	if err != nil {
		t.Fatalf("FindPathDirected (incoming): %v", err)
	}
	if path[0] != "thing:4" || path[len(path)-1] != "thing:1" {
		t.Errorf("FindPathDirected (incoming): path = %v", path)
	}
}

func TestFindPathDirected_Any(t *testing.T) {
	g, _ := setupDirectedGraph(t)

	// E→C→D: undirected — should find a path from E to D.
	path, err := g.FindPathDirected("thing:5", "thing:4", 10, PathDirAny)
	if err != nil {
		t.Fatalf("FindPathDirected (any): %v", err)
	}
	if path[0] != "thing:5" || path[len(path)-1] != "thing:4" {
		t.Errorf("FindPathDirected (any): path = %v", path)
	}
}

func TestFindPathDirected_SameNode(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("x:1", "x")

	path, err := g.FindPathDirected("x:1", "x:1", 5, PathDirOutgoing)
	if err != nil {
		t.Fatalf("FindPathDirected (same node): %v", err)
	}
	if len(path) != 1 || path[0] != "x:1" {
		t.Errorf("FindPathDirected (same node): path = %v", path)
	}
}

func TestFindPathDirected_NoPath(t *testing.T) {
	g, _ := setupDirectedGraph(t)

	// No outgoing path from D to A (edges go A→B→C→D, not the other way).
	_, err := g.FindPathDirected("thing:4", "thing:1", 10, PathDirOutgoing)
	if err == nil {
		t.Error("FindPathDirected (no path): expected error, got nil")
	}
}

func TestFindPathDirected_MissingNode(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("x:1", "x")

	_, err := g.FindPathDirected("x:1", "x:99", 5, PathDirOutgoing)
	if err == nil {
		t.Error("FindPathDirected (missing to-node): expected error, got nil")
	}

	_, err = g.FindPathDirected("x:99", "x:1", 5, PathDirOutgoing)
	if err == nil {
		t.Error("FindPathDirected (missing from-node): expected error, got nil")
	}
}

func TestFindPathDirected_CrossTenant(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("0001@x:1", "x")
	g.AddNode("0002@x:1", "x")

	_, err := g.FindPathDirected("0001@x:1", "0002@x:1", 5, PathDirOutgoing)
	if err == nil {
		t.Error("FindPathDirected (cross-tenant): expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// AllShortestPaths
// ---------------------------------------------------------------------------

func TestAllShortestPaths_Single(t *testing.T) {
	g, _ := setupDirectedGraph(t)

	// A→B→C→D: one shortest path of length 3.
	paths, err := g.AllShortestPaths("thing:1", "thing:4", 10, PathDirOutgoing)
	if err != nil {
		t.Fatalf("AllShortestPaths: %v", err)
	}
	if len(paths) == 0 {
		t.Error("AllShortestPaths: expected at least one path")
	}
	for _, p := range paths {
		if p[0] != "thing:1" || p[len(p)-1] != "thing:4" {
			t.Errorf("AllShortestPaths: bad path %v", p)
		}
	}
}

func TestAllShortestPaths_Multiple(t *testing.T) {
	// Build a diamond: A→B→D and A→C→D.
	g := NewFlatGraph()
	for _, id := range []string{"x:1", "x:2", "x:3", "x:4"} {
		g.AddNode(id, "x")
	}
	g.AddEdge("x:1", "x:2", "L")
	g.AddEdge("x:1", "x:3", "L")
	g.AddEdge("x:2", "x:4", "L")
	g.AddEdge("x:3", "x:4", "L")

	paths, err := g.AllShortestPaths("x:1", "x:4", 10, PathDirOutgoing)
	if err != nil {
		t.Fatalf("AllShortestPaths (diamond): %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("AllShortestPaths (diamond): got %d paths, want 2", len(paths))
	}
}

func TestAllShortestPaths_SameNode(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("x:1", "x")

	paths, err := g.AllShortestPaths("x:1", "x:1", 5, PathDirOutgoing)
	if err != nil {
		t.Fatalf("AllShortestPaths (same node): %v", err)
	}
	if len(paths) != 1 || len(paths[0]) != 1 {
		t.Errorf("AllShortestPaths (same node): %v", paths)
	}
}

func TestAllShortestPaths_NoPath(t *testing.T) {
	g, _ := setupDirectedGraph(t)

	paths, err := g.AllShortestPaths("thing:4", "thing:1", 10, PathDirOutgoing)
	if err != nil {
		t.Fatalf("AllShortestPaths (no path): unexpected error: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("AllShortestPaths (no path): got %d paths, want 0", len(paths))
	}
}

func TestAllShortestPaths_Undirected(t *testing.T) {
	g, _ := setupDirectedGraph(t)

	// E→C has an outgoing edge to C; using PathDirAny E can also reach B via C←B.
	paths, err := g.AllShortestPaths("thing:5", "thing:4", 10, PathDirAny)
	if err != nil {
		t.Fatalf("AllShortestPaths (undirected): %v", err)
	}
	if len(paths) == 0 {
		t.Error("AllShortestPaths (undirected): expected at least one path")
	}
}

func TestAllShortestPaths_MissingNode(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("x:1", "x")

	_, err := g.AllShortestPaths("x:1", "x:99", 5, PathDirOutgoing)
	if err == nil {
		t.Error("AllShortestPaths (missing to): expected error")
	}
}

func TestAllShortestPaths_CrossTenant(t *testing.T) {
	g := NewFlatGraph()
	g.AddNode("0001@x:1", "x")
	g.AddNode("0002@x:1", "x")

	_, err := g.AllShortestPaths("0001@x:1", "0002@x:1", 5, PathDirOutgoing)
	if err == nil {
		t.Error("AllShortestPaths (cross-tenant): expected error")
	}
}
