// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package graphalgo

import (
	"errors"
	"testing"
)

// mapNeighbors builds a neighbors func from a plain adjacency map —
// enough to test the algorithm in isolation, no storage involved.
func mapNeighbors(adj map[string][]string) func(string) ([]string, error) {
	return func(node string) ([]string, error) { return adj[node], nil }
}

func TestWouldCreateCycle_SelfLoop(t *testing.T) {
	got, err := WouldCreateCycle("a", "a", 100, mapNeighbors(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("a->a must always be a cycle")
	}
}

func TestWouldCreateCycle_DirectCycle(t *testing.T) {
	// b already points to a; adding a->b would close the loop.
	adj := map[string][]string{"b": {"a"}}
	got, err := WouldCreateCycle("a", "b", 100, mapNeighbors(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("want cycle detected")
	}
}

func TestWouldCreateCycle_TransitiveCycle(t *testing.T) {
	// c -> b -> a already exists; adding a->c would close a 3-node loop.
	adj := map[string][]string{"c": {"b"}, "b": {"a"}}
	got, err := WouldCreateCycle("a", "c", 100, mapNeighbors(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("want transitive cycle detected")
	}
}

func TestWouldCreateCycle_NoCycle_DAG(t *testing.T) {
	// A simple DAG: x -> y -> z, no path back to any ancestor.
	adj := map[string][]string{"x": {"y"}, "y": {"z"}}
	got, err := WouldCreateCycle("w", "x", 100, mapNeighbors(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("want no cycle for a genuine DAG extension")
	}
}

func TestWouldCreateCycle_ParallelPaths_NotOverCounted(t *testing.T) {
	// Bushy graph: many parallel paths from "to" that never reach
	// "from" -- must not spuriously trip a low budget just from
	// breadth, matching pkg/graph's own documented reasoning for using
	// unique-visited-count rather than a raw dequeue count.
	adj := map[string][]string{
		"to": {"n1", "n2", "n3", "n4", "n5"},
	}
	got, err := WouldCreateCycle("from", "to", 10, mapNeighbors(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("want no cycle -- five parallel dead-end branches from one node is well within budget 10")
	}
}

func TestWouldCreateCycle_BudgetExhausted_ConservativelyAssumesCycle(t *testing.T) {
	// A long chain that never reaches "from" within the given budget
	// must still report a cycle -- conservative-on-exhaustion is the
	// whole point of the budget, not an incidental side effect.
	adj := map[string][]string{}
	for i := 0; i < 20; i++ {
		adj[nodeName(i)] = []string{nodeName(i + 1)}
	}
	got, err := WouldCreateCycle("from", nodeName(0), 5, mapNeighbors(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("want conservative true on budget exhaustion, got false")
	}
}

func nodeName(i int) string {
	return "n" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}

func TestWouldCreateCycle_NeighborsError_Propagates(t *testing.T) {
	boom := errors.New("boom")
	_, err := WouldCreateCycle("a", "b", 100, func(string) ([]string, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want the neighbors error to propagate, got %v", err)
	}
}

func TestWouldCreateCycle_UnboundedWhenLimitZero(t *testing.T) {
	adj := map[string][]string{}
	for i := 0; i < 200; i++ {
		adj[nodeName(i)] = []string{nodeName(i + 1)}
	}
	got, err := WouldCreateCycle("from", nodeName(0), 0, mapNeighbors(adj))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("limit<=0 must mean unbounded, not budget-zero -- a long DAG chain must not be treated as a cycle")
	}
}
