// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package obj

import (
	"context"
	"fmt"
	"testing"
)

// fakeGraph is a minimal objGraph implementation for testing the
// mirror in isolation, without pulling in pkg/graph's full weight —
// exactly the reason objGraph is an interface, not the concrete type.
type fakeGraph struct {
	edges map[string]string // from -> to (this test never adds parallel edges)
}

func newFakeGraph() *fakeGraph { return &fakeGraph{edges: make(map[string]string)} }

func (f *fakeGraph) AddEdge(from, to, relationship string) error {
	f.edges[from] = to
	return nil
}

func (f *fakeGraph) RemoveEdge(from, to string) error {
	if f.edges[from] != to {
		return fmt.Errorf("no edge %s->%s", from, to)
	}
	delete(f.edges, from)
	return nil
}

func TestMirror_ContainmentAdd_OnMoveToContainer(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	g := newFakeGraph()
	s.SetGraph(g)

	if err := s.Attach(ctx, "pallets:1", Capacity{}); err != nil {
		t.Fatalf("attach pallet: %v", err)
	}
	if err := s.Attach(ctx, "vehicles:1", Capacity{}); err != nil {
		t.Fatalf("attach vehicle: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:1"); err != nil {
		t.Fatalf("MoveToContainer: %v", err)
	}

	contNode, err := nodeIDForSubject(s.tenantID, "vehicles:1")
	if err != nil {
		t.Fatalf("nodeIDForSubject: %v", err)
	}
	subjNode, err := nodeIDForSubject(s.tenantID, "pallets:1")
	if err != nil {
		t.Fatalf("nodeIDForSubject: %v", err)
	}
	if g.edges[contNode] != subjNode {
		t.Errorf("want mirrored edge %s->%s, got edges=%v", contNode, subjNode, g.edges)
	}
}

func TestMirror_RemovesOldEdge_WhenMovedToNewContainer(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	g := newFakeGraph()
	s.SetGraph(g)

	for _, ref := range []string{"pallets:1", "vehicles:1", "vehicles:2"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:1"); err != nil {
		t.Fatalf("move into vehicle 1: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:2"); err != nil {
		t.Fatalf("move into vehicle 2: %v", err)
	}

	v1Node, _ := nodeIDForSubject(s.tenantID, "vehicles:1")
	v2Node, _ := nodeIDForSubject(s.tenantID, "vehicles:2")
	pNode, _ := nodeIDForSubject(s.tenantID, "pallets:1")

	if _, stillThere := g.edges[v1Node]; stillThere {
		t.Errorf("old edge from vehicle 1 must be removed, got %v", g.edges)
	}
	if g.edges[v2Node] != pNode {
		t.Errorf("want new edge %s->%s, got %v", v2Node, pNode, g.edges)
	}
}

func TestMirror_NilGraph_NeverPanics(t *testing.T) {
	ctx := context.Background()
	s := testStore(t) // no SetGraph call -- s.graph stays nil
	if err := s.Attach(ctx, "pallets:1", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.Attach(ctx, "vehicles:1", Capacity{}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:1"); err != nil {
		t.Fatalf("MoveToContainer with no graph attached must still succeed: %v", err)
	}
}

func TestMirror_DetachViaJournal_RemovesLastKnownEdge(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	g := newFakeGraph()
	s.SetGraph(g)

	for _, ref := range []string{"pallets:1", "vehicles:1"} {
		if err := s.Attach(ctx, ref, Capacity{}); err != nil {
			t.Fatalf("attach %s: %v", ref, err)
		}
	}
	if err := s.MoveToContainer(ctx, "pallets:1", "vehicles:1"); err != nil {
		t.Fatalf("move into vehicle: %v", err)
	}

	// Simulate demote's own PostCommit path directly: detach via the
	// tx-scoped core (as Adapter.Execute does), then resolve the
	// journal-based "what was its last container" the same way
	// Adapter.PostCommit does, and mirror-remove it.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := unassignAndDetachInTx(ctx, tx, "pallets:1"); err != nil {
		tx.Rollback()
		t.Fatalf("unassignAndDetachInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	prevContainer, hadPrev, err := s.lastKnownContainerFromJournal(ctx, "pallets:1")
	if err != nil {
		t.Fatalf("lastKnownContainerFromJournal: %v", err)
	}
	if !hadPrev || prevContainer != "vehicles:1" {
		t.Fatalf("want prevContainer vehicles:1, got %q hadPrev=%v", prevContainer, hadPrev)
	}
	s.mirrorContainmentRemove("pallets:1", prevContainer)

	v1Node, _ := nodeIDForSubject(s.tenantID, "vehicles:1")
	if _, stillThere := g.edges[v1Node]; stillThere {
		t.Errorf("edge must be removed after detach, got %v", g.edges)
	}
}
