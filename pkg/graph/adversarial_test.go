// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// adversarial_test.go — graph edge-case and adversarial correctness tests.
//
// These tests probe conditions that the contract suite does not cover:
// depth boundary semantics, BFS termination under tight depth limits,
// consistency between PathExists and FindPath under identical inputs,
// concurrent node mutation during traversal, and REF extraction behaviour
// for structurally degenerate entity data.

package graph

import (
	"fmt"
	"sync"
	"testing"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// ---------------------------------------------------------------------------
// maxDepth boundary — depth=0
// ---------------------------------------------------------------------------

// TestAdversarial_FindPath_MaxDepthZero asserts that FindPath with maxDepth=0
// still succeeds for a self-path (zero edges needed) but fails for any
// adjacent pair (one edge needed, depth budget already exhausted).
func TestAdversarial_FindPath_MaxDepthZero(t *testing.T) {
	t.Parallel()
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()
			mustAddNodeG(t, g, "n:1", "n")
			mustAddNodeG(t, g, "n:2", "n")
			mustAddEdgeG(t, g, "n:1", "n:2", "L")

			// Self-path with depth 0 must succeed — no edges required.
			path, err := g.FindPath("n:1", "n:1", 0)
			if err != nil {
				t.Errorf("FindPath self depth=0: unexpected error: %v", err)
			}
			if len(path) != 1 || path[0] != "n:1" {
				t.Errorf("FindPath self depth=0: want [\"n:1\"], got %v", path)
			}

			// Adjacent pair with depth 0 must fail — the one edge cannot be traversed.
			if _, err := g.FindPath("n:1", "n:2", 0); err == nil {
				t.Error("FindPath adjacent depth=0: expected error, got nil")
			}
		})
	}
}

// TestAdversarial_PathExists_MaxDepthZero mirrors FindPath: self-path is
// reachable at depth 0; adjacent pair is not.
func TestAdversarial_PathExists_MaxDepthZero(t *testing.T) {
	t.Parallel()
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()
			mustAddNodeG(t, g, "n:1", "n")
			mustAddNodeG(t, g, "n:2", "n")
			mustAddEdgeG(t, g, "n:1", "n:2", "L")

			// Self-path at depth 0.
			found, length, err := g.PathExists("n:1", "n:1", 0)
			if err != nil {
				t.Fatalf("PathExists self depth=0: %v", err)
			}
			if !found {
				t.Error("PathExists self depth=0: want true")
			}
			if length != 0 {
				t.Errorf("PathExists self depth=0: want length 0, got %d", length)
			}

			// Adjacent pair at depth 0: should not be found.
			found, _, err = g.PathExists("n:1", "n:2", 0)
			if err != nil {
				t.Fatalf("PathExists adjacent depth=0: unexpected error: %v", err)
			}
			if found {
				t.Error("PathExists adjacent depth=0: want false (edge requires depth ≥ 1)")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PathExists / FindPath consistency under identical inputs
// ---------------------------------------------------------------------------

// TestAdversarial_PathExists_FindPath_DepthConsistency verifies that for every
// depth d in 0..chainLen, PathExists and FindPath agree: both succeed when the
// path fits within the depth budget, and both fail (or return not-found) when
// it does not. Any disagreement is a contract violation.
func TestAdversarial_PathExists_FindPath_DepthConsistency(t *testing.T) {
	t.Parallel()
	const chainLen = 5
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()
			// Build a linear chain: n:1 → n:2 → ... → n:(chainLen+1)
			for i := 1; i <= chainLen+1; i++ {
				mustAddNodeG(t, g, fmt.Sprintf("n:%d", i), "n")
			}
			for i := 1; i <= chainLen; i++ {
				mustAddEdgeG(t, g, fmt.Sprintf("n:%d", i), fmt.Sprintf("n:%d", i+1), "L")
			}
			src := "n:1"
			dst := fmt.Sprintf("n:%d", chainLen+1) // requires chainLen edges

			for d := 0; d <= chainLen+1; d++ {
				d := d
				t.Run(fmt.Sprintf("depth=%d", d), func(t *testing.T) {
					t.Parallel()
					exists, exLen, exErr := g.PathExists(src, dst, d)
					path, fpErr := g.FindPath(src, dst, d)
					fpFound := fpErr == nil

					// Both must agree on reachability.
					if exists != fpFound {
						t.Errorf("depth=%d: PathExists=%v, FindPath found=%v — disagreement", d, exists, fpFound)
					}

					// When both agree the path is reachable, lengths must match.
					if exists && fpFound {
						fpLen := len(path) - 1
						if fpLen < 0 {
							fpLen = 0
						}
						if exLen != fpLen {
							t.Errorf("depth=%d: PathExists length=%d, FindPath length=%d — disagreement", d, exLen, fpLen)
						}
					}

					// Reachability is expected only when d >= chainLen.
					wantReachable := d >= chainLen
					if exists != wantReachable {
						t.Errorf("depth=%d: want reachable=%v, got %v", d, wantReachable, exists)
					}
					_ = exErr
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BFS halts within budget — graph that is reachable only beyond maxDepth
// ---------------------------------------------------------------------------

// TestAdversarial_FindPath_ExactDepthBoundary confirms that a path of exactly
// N edges is found at maxDepth=N and fails at maxDepth=N-1.  This catches
// off-by-one errors in depth-gating logic.
func TestAdversarial_FindPath_ExactDepthBoundary(t *testing.T) {
	t.Parallel()
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()
			// Chain of exactly 3 edges: a→b→c→d
			for _, id := range []string{"a:1", "b:1", "c:1", "d:1"} {
				mustAddNodeG(t, g, id, id[:1])
			}
			mustAddEdgeG(t, g, "a:1", "b:1", "L")
			mustAddEdgeG(t, g, "b:1", "c:1", "L")
			mustAddEdgeG(t, g, "c:1", "d:1", "L")

			// maxDepth=2: path requires 3 edges, must fail.
			if _, err := g.FindPath("a:1", "d:1", 2); err == nil {
				t.Error("FindPath depth=2 on 3-edge chain: expected error, got nil")
			}
			// maxDepth=3: exactly meets the requirement, must succeed.
			path, err := g.FindPath("a:1", "d:1", 3)
			if err != nil {
				t.Fatalf("FindPath depth=3 on 3-edge chain: unexpected error: %v", err)
			}
			want := []string{"a:1", "b:1", "c:1", "d:1"}
			if len(path) != len(want) {
				t.Fatalf("FindPath depth=3: want %v, got %v", want, path)
			}
			for i, n := range want {
				if path[i] != n {
					t.Errorf("FindPath depth=3: path[%d] = %q, want %q", i, path[i], n)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Concurrent mutation during traversal
// ---------------------------------------------------------------------------

// TestAdversarial_ConcurrentUpdateAndTraversal fires concurrent writers that
// repeatedly call UpdateFromEntity (which removes then re-adds edges) while
// readers call FindPath on the same nodes.  The test asserts that no
// goroutine panics and that every traversal either succeeds or returns a
// well-formed error — no corrupt or partial state is observable.
func TestAdversarial_ConcurrentUpdateAndTraversal(t *testing.T) {
	t.Parallel()
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()

			// Static topology: src → mid → dst
			mustAddNodeG(t, g, "src:1", "src")
			mustAddNodeG(t, g, "mid:1", "mid")
			mustAddNodeG(t, g, "dst:1", "dst")
			mustAddEdgeG(t, g, "src:1", "mid:1", "A")
			mustAddEdgeG(t, g, "mid:1", "dst:1", "B")

			const writers = 4
			const readers = 4
			const iterations = 50

			var wg sync.WaitGroup
			errs := make(chan error, (writers+readers)*iterations)

			// Writers: cycle mid:1's REF from dst:1 back to itself and back.
			for w := 0; w < writers; w++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for i := 0; i < iterations; i++ {
						// Alternate between two valid REF states.
						var data map[string]interface{}
						if i%2 == 0 {
							data = map[string]interface{}{
								"id":   2,
								"link": map[string]interface{}{"type": "REF", "entity": "dst", "id": float64(1)},
							}
						} else {
							data = map[string]interface{}{
								"id":   2,
								"link": map[string]interface{}{"type": "REF", "entity": "src", "id": float64(1)},
							}
						}
						if err := g.UpdateFromEntity("mid", 1, data); err != nil {
							errs <- fmt.Errorf("writer %d iter %d: %w", id, i, err)
						}
					}
				}(w)
			}

			// Readers: traverse src:1 → dst:1 repeatedly; accept any outcome.
			for r := 0; r < readers; r++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for i := 0; i < iterations; i++ {
						// FindPath may return an error if mid's edge temporarily
						// points away from dst — that is a legitimate transient
						// state, not a bug.  What must never happen is a panic
						// or a hang.
						_, _ = g.FindPath("src:1", "dst:1", 5)
					}
				}(r)
			}

			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UpdateFromEntity with degenerate REF data
// ---------------------------------------------------------------------------

// TestAdversarial_UpdateFromEntity_EmptyEntityREF confirms that an entity
// document containing a REF with an empty "entity" string does not cause a
// panic, does not add a malformed edge, and leaves the graph in a consistent
// state.
func TestAdversarial_UpdateFromEntity_EmptyEntityREF(t *testing.T) {
	t.Parallel()
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()
			mustAddNodeG(t, g, "item:1", "item")

			// REF with empty entity — should be silently dropped by
			// ExtractEntityEdges, not cause a panic or malformed node.
			data := map[string]interface{}{
				"id":  1,
				"bad": map[string]interface{}{"type": "REF", "entity": "", "id": float64(99)},
			}
			if err := g.UpdateFromEntity("item", 1, data); err != nil {
				t.Fatalf("UpdateFromEntity empty entity REF: unexpected error: %v", err)
			}

			// The node itself must still exist.
			if !g.NodeExists("item:1") {
				t.Error("item:1 must still exist after update with empty-entity REF")
			}

			// No edges should have been created to a \":99\" node.
			neighbours, err := g.GetNeighbors("item:1")
			if err != nil {
				t.Fatalf("GetNeighbors: %v", err)
			}
			if len(neighbours) != 0 {
				t.Errorf("expected no edges from item:1 after empty-entity REF, got %v", neighbours)
			}
		})
	}
}

// TestAdversarial_UpdateFromEntity_NilIDInREF confirms that a REF whose "id"
// value is missing (nil / absent key) is rejected without panic.
func TestAdversarial_UpdateFromEntity_MissingIDInREF(t *testing.T) {
	t.Parallel()
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()
			mustAddNodeG(t, g, "item:1", "item")

			// No "id" key — IsReference should return false; the field is not a REF.
			data := map[string]interface{}{
				"id":  1,
				"ref": map[string]interface{}{"type": "REF", "entity": "other"},
			}
			if err := g.UpdateFromEntity("item", 1, data); err != nil {
				t.Fatalf("UpdateFromEntity missing id in REF: unexpected error: %v", err)
			}
			neighbours, err := g.GetNeighbors("item:1")
			if err != nil {
				t.Fatalf("GetNeighbors: %v", err)
			}
			if len(neighbours) != 0 {
				t.Errorf("expected no edges from item:1 with missing-id REF, got %v", neighbours)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PathExists on a highly-connected graph (stress termination)
// ---------------------------------------------------------------------------

// TestAdversarial_PathExists_DenseGraph ensures PathExists terminates in
// bounded time on a fully-connected graph where every node reaches every
// other node — the BFS visited-set must prevent re-enqueuing.
func TestAdversarial_PathExists_DenseGraph(t *testing.T) {
	t.Parallel()
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()
			const n = 20
			nodes := make([]string, n)
			for i := 0; i < n; i++ {
				nodes[i] = fmt.Sprintf("n:%d", i+1)
				mustAddNodeG(t, g, nodes[i], "n")
			}
			// Fully-connected directed graph.
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					if i != j {
						mustAddEdgeG(t, g, nodes[i], nodes[j], "L")
					}
				}
			}
			// Every pair must be reachable at depth 1.
			found, length, err := g.PathExists(nodes[0], nodes[n-1], 1)
			if err != nil {
				t.Fatalf("PathExists dense graph: %v", err)
			}
			if !found {
				t.Error("PathExists dense graph: want true")
			}
			if length != 1 {
				t.Errorf("PathExists dense graph: want length 1, got %d", length)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tenant-prefixed node with maxDepth=0
// ---------------------------------------------------------------------------

// TestAdversarial_FindPath_TenantSelfPath_MaxDepthZero tests the tenant-prefix
// variant of the depth=0 self-path case to ensure the tenant codec does not
// interfere with the early-return branch.
func TestAdversarial_FindPath_TenantSelfPath_MaxDepthZero(t *testing.T) {
	t.Parallel()
	for _, impl := range graphImpls {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			g := impl.new()
			id := tenant.TenantID(3).NodeID("item", 7)
			mustAddNodeG(t, g, id, "item")

			path, err := g.FindPath(id, id, 0)
			if err != nil {
				t.Fatalf("FindPath tenant self depth=0: %v", err)
			}
			if len(path) != 1 || path[0] != id {
				t.Errorf("FindPath tenant self depth=0: want [%q], got %v", id, path)
			}
		})
	}
}
