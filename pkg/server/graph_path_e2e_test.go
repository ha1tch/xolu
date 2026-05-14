// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// graph_path_e2e_test.go — HTTP-layer tests for path, shortestPath, and
// pathExists endpoints, plus concurrent counter-accuracy and cyclic-graph
// path termination.
//
// Gap addressed: the existing graph test suite exercised these three endpoints
// only through the unit-level contract (pkg/graph). No server-layer test
// verified that the HTTP handlers parse parameters correctly, produce the
// right status codes, strip tenant prefixes from responses, or terminate
// correctly on cyclic inputs.
//
// Tests in this file:
//
//  1. TestGraphPath_HappyPath          — /graph/path returns correct path and length.
//  2. TestGraphPath_SelfPath            — from == to returns single-node path, length 0.
//  3. TestGraphPath_NoPath_Returns404   — disconnected nodes → 404.
//  4. TestGraphPath_MaxDepthExceeded    — path longer than max_depth → 404.
//  5. TestGraphPath_MissingParams       — missing from or to → 400.
//  6. TestGraphPath_NoPrefixInResponse  — XXXX@ prefix never leaks to client.
//  7. TestGraphPath_TenantIsolation     — alpha path is invisible to beta.
//
//  8. TestGraphShortestPath_Found       — /graph/shortestPath returns 200 + exists:true.
//  9. TestGraphShortestPath_NotFound    — 200 + exists:false (no error unlike /path).
// 10. TestGraphShortestPath_MissingParams — 400.
//
// 11. TestGraphPathExists_Found         — /graph/pathExists exists:true + correct length.
// 12. TestGraphPathExists_NotFound      — exists:false, length 0.
// 13. TestGraphPathExists_MissingParams — 400.
// 14. TestGraphPathExists_AbsentNode    — 404 for unknown node.
//
// 15. TestGraphPath_CyclicGraph_Terminates — path and pathExists complete without
//     hanging on a graph that contains a cycle (regression for BFS infinite-loop).
//     This is the HTTP-layer analogue of TestContract_PathExists_CyclicGraph_Terminates.
//
// 16. TestGraphCounters_ConcurrentAccuracy — per-tenant node/edge counters remain
//     correct after a concurrent write storm (unit contract tests only verify
//     counter accuracy sequentially).

package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// newPathTestServer returns a server identical to newGraphTenantServer but
// with "alpha" registered as tenant 1 only. Tests that need beta register it
// themselves.
func newPathTestServer(t *testing.T) *Server {
	t.Helper()
	s := newGraphTenantServer(t) // registers alpha=1, beta=2
	return s
}

// seedChain creates entity:1 → entity:2 → … → entity:n under tenantName,
// where each entity i has a REF to entity i+1 named "next_ref".
// Returns the node IDs as the client sees them (no prefix): "entity:1" …
func seedChain(t *testing.T, s *Server, tenantName, entityType string, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := 1; i <= n; i++ {
		data := map[string]interface{}{}
		if i < n {
			data["next_ref"] = map[string]interface{}{
				"type":   "REF",
				"entity": entityType,
				"id":     i + 1,
			}
		}
		seedGraphEntity(t, s, tenantName, entityType, i, data)
		ids[i-1] = fmt.Sprintf("%s:%d", entityType, i)
	}
	return ids
}

// graphPath is a convenience wrapper for POST /graph/path.
func graphPath(t *testing.T, s *Server, tenantName, from, to string, maxDepth int) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]interface{}{"from": from, "to": to}
	if maxDepth > 0 {
		body["max_depth"] = maxDepth
	}
	return tgDo(t, s, http.MethodPost, tenantName, "/path", body)
}

// graphPathExists is a convenience wrapper for POST /graph/pathExists.
func graphPathExists(t *testing.T, s *Server, tenantName, from, to string, maxDepth int) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]interface{}{"from": from, "to": to}
	if maxDepth > 0 {
		body["max_depth"] = maxDepth
	}
	return tgDo(t, s, http.MethodPost, tenantName, "/pathExists", body)
}

// graphShortestPath is a convenience wrapper for POST /graph/shortestPath.
func graphShortestPath(t *testing.T, s *Server, tenantName, from, to string, maxDepth int) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]interface{}{"from": from, "to": to}
	if maxDepth > 0 {
		body["max_depth"] = maxDepth
	}
	return tgDo(t, s, http.MethodPost, tenantName, "/shortestPath", body)
}

// assertPathResponse decodes and returns the response map; fatals on non-200.
func assertPathOK(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d — body: %s", w.Code, w.Body.String())
	}
	return decodeGraphJSON(t, w)
}

// pathNodes extracts the "path" field as []string.
func pathNodes(t *testing.T, m map[string]interface{}) []string {
	t.Helper()
	raw, ok := m["path"]
	if !ok {
		t.Fatal("response has no 'path' key")
	}
	if raw == nil {
		return nil
	}
	slice, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("'path' is not an array: %T", raw)
	}
	out := make([]string, len(slice))
	for i, v := range slice {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("path[%d] is not a string: %T", i, v)
		}
		out[i] = s
	}
	return out
}

// pathLength extracts the "length" field as int.
func pathLength(t *testing.T, m map[string]interface{}) int {
	t.Helper()
	v, ok := m["length"]
	if !ok {
		t.Fatal("response has no 'length' key")
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("'length' is not a number: %T", v)
	}
	return int(f)
}

// ---------------------------------------------------------------------------
// /graph/path
// ---------------------------------------------------------------------------

// 1. Happy path: chain of 4 nodes, path from first to last.
func TestGraphPath_HappyPath(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedChain(t, s, "alpha", "node", 4)

	w := graphPath(t, s, "alpha", "node:1", "node:4", 10)
	m := assertPathOK(t, w)

	nodes := pathNodes(t, m)
	if len(nodes) != 4 {
		t.Fatalf("path length: want 4 nodes, got %d: %v", len(nodes), nodes)
	}
	if nodes[0] != "node:1" || nodes[len(nodes)-1] != "node:4" {
		t.Errorf("path bounds: want node:1…node:4, got %v", nodes)
	}
	if pathLength(t, m) != 3 {
		t.Errorf("length: want 3 (edges), got %d", pathLength(t, m))
	}
}

// 2. Self path: from == to.
func TestGraphPath_SelfPath(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedGraphEntity(t, s, "alpha", "node", 1, map[string]interface{}{})

	w := graphPath(t, s, "alpha", "node:1", "node:1", 5)
	m := assertPathOK(t, w)

	nodes := pathNodes(t, m)
	if len(nodes) != 1 || nodes[0] != "node:1" {
		t.Errorf("self path: want [node:1], got %v", nodes)
	}
	if pathLength(t, m) != 0 {
		t.Errorf("self path length: want 0, got %d", pathLength(t, m))
	}
}

// 3. No path between disconnected nodes → 404.
func TestGraphPath_NoPath_Returns404(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedGraphEntity(t, s, "alpha", "node", 1, map[string]interface{}{})
	seedGraphEntity(t, s, "alpha", "node", 2, map[string]interface{}{})
	// No edge between 1 and 2.

	w := graphPath(t, s, "alpha", "node:1", "node:2", 10)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d — body: %s", w.Code, w.Body.String())
	}
}

// 4. Path exists but exceeds max_depth → 404.
func TestGraphPath_MaxDepthExceeded(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedChain(t, s, "alpha", "node", 5) // chain of 5 requires depth 4

	// max_depth=2 is not enough.
	w := graphPath(t, s, "alpha", "node:1", "node:5", 2)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 for exceeded max_depth, got %d — body: %s", w.Code, w.Body.String())
	}

	// max_depth=4 is exactly enough.
	w2 := graphPath(t, s, "alpha", "node:1", "node:5", 4)
	if w2.Code != http.StatusOK {
		t.Errorf("want 200 for sufficient max_depth, got %d — body: %s", w2.Code, w2.Body.String())
	}
}

// 5. Missing from or to → 400.
func TestGraphPath_MissingParams(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedGraphEntity(t, s, "alpha", "node", 1, map[string]interface{}{})

	cases := []map[string]interface{}{
		{"to": "node:1"},
		{"from": "node:1"},
		{},
	}
	for _, body := range cases {
		w := tgDo(t, s, http.MethodPost, "alpha", "/path", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %v: want 400, got %d", body, w.Code)
		}
	}
}

// 6. No XXXX@ prefix appears in any response field.
func TestGraphPath_NoPrefixInResponse(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedChain(t, s, "alpha", "node", 3)

	w := graphPath(t, s, "alpha", "node:1", "node:3", 10)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if containsPrefix(w.Body.String()) {
		t.Errorf("response contains XXXX@ tenant prefix: %s", w.Body.String())
	}
}

// 7. Alpha's chain is not reachable from beta's routes.
func TestGraphPath_TenantIsolation(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedChain(t, s, "alpha", "node", 3)
	// beta has no nodes at all — but even if alpha's nodes existed under beta
	// prefixes, the handler must not serve them.

	// beta querying for alpha's node IDs must get 404.
	w := graphPath(t, s, "beta", "node:1", "node:3", 10)
	if w.Code != http.StatusNotFound {
		t.Errorf("beta querying alpha chain: want 404, got %d — body: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// /graph/shortestPath
// ---------------------------------------------------------------------------

// 8. Found: returns 200 + exists:true + non-nil path.
func TestGraphShortestPath_Found(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedChain(t, s, "alpha", "hop", 3)

	w := graphShortestPath(t, s, "alpha", "hop:1", "hop:3", 10)
	m := assertPathOK(t, w)

	if exists, _ := m["exists"].(bool); !exists {
		t.Errorf("shortestPath found: want exists:true, got %v", m["exists"])
	}
	nodes := pathNodes(t, m)
	if len(nodes) != 3 {
		t.Errorf("shortestPath nodes: want 3, got %d: %v", len(nodes), nodes)
	}
	if pathLength(t, m) != 2 {
		t.Errorf("shortestPath length: want 2, got %d", pathLength(t, m))
	}
}

// 9. Not found: 200 + exists:false + nil path (not an error unlike /path).
func TestGraphShortestPath_NotFound(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedGraphEntity(t, s, "alpha", "hop", 1, map[string]interface{}{})
	seedGraphEntity(t, s, "alpha", "hop", 2, map[string]interface{}{})

	w := graphShortestPath(t, s, "alpha", "hop:1", "hop:2", 10)
	// Must be 200, not 404 — this is the semantic difference from /path.
	m := assertPathOK(t, w)

	if exists, _ := m["exists"].(bool); exists {
		t.Errorf("shortestPath not-found: want exists:false, got true")
	}
	if m["path"] != nil {
		t.Errorf("shortestPath not-found: want nil path, got %v", m["path"])
	}
	if pathLength(t, m) != 0 {
		t.Errorf("shortestPath not-found: want length 0, got %d", pathLength(t, m))
	}
}

// 10. Missing params → 400.
func TestGraphShortestPath_MissingParams(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)

	cases := []map[string]interface{}{
		{"to": "node:1"},
		{"from": "node:1"},
		{},
	}
	for _, body := range cases {
		w := tgDo(t, s, http.MethodPost, "alpha", "/shortestPath", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %v: want 400, got %d", body, w.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// /graph/pathExists
// ---------------------------------------------------------------------------

// 11. Exists: correct exists:true and length.
func TestGraphPathExists_Found(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedChain(t, s, "alpha", "step", 4)

	w := graphPathExists(t, s, "alpha", "step:1", "step:4", 10)
	m := assertPathOK(t, w)

	if exists, _ := m["exists"].(bool); !exists {
		t.Errorf("pathExists found: want true, got %v", m["exists"])
	}
	if pathLength(t, m) != 3 {
		t.Errorf("pathExists length: want 3, got %d", pathLength(t, m))
	}
}

// 12. Not found: exists:false, length 0.
func TestGraphPathExists_NotFound(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedGraphEntity(t, s, "alpha", "step", 1, map[string]interface{}{})
	seedGraphEntity(t, s, "alpha", "step", 2, map[string]interface{}{})

	w := graphPathExists(t, s, "alpha", "step:1", "step:2", 10)
	m := assertPathOK(t, w)

	if exists, _ := m["exists"].(bool); exists {
		t.Errorf("pathExists not-found: want false, got true")
	}
	if pathLength(t, m) != 0 {
		t.Errorf("pathExists not-found: want length 0, got %d", pathLength(t, m))
	}
}

// 13. Missing params → 400.
func TestGraphPathExists_MissingParams(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)

	cases := []map[string]interface{}{
		{"to": "node:1"},
		{"from": "node:1"},
		{},
	}
	for _, body := range cases {
		w := tgDo(t, s, http.MethodPost, "alpha", "/pathExists", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %v: want 400, got %d", body, w.Code)
		}
	}
}

// 14. Absent node → 404.
func TestGraphPathExists_AbsentNode(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)
	seedGraphEntity(t, s, "alpha", "step", 1, map[string]interface{}{})

	w := graphPathExists(t, s, "alpha", "step:1", "ghost:999", 10)
	if w.Code != http.StatusNotFound {
		t.Errorf("absent node: want 404, got %d — body: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 15. Cyclic graph — HTTP-layer termination test.
//
// This is the HTTP analogue of TestContract_PathExists_CyclicGraph_Terminates.
// The entity write path cannot create back-edges, so we inject the cycle
// directly via s.graph after seeding the forward chain through HTTP. Both
// /graph/path and /graph/pathExists must return within the test timeout.
//
// Topology: alpha has node:1 → node:2 → node:3 → node:1 (cycle).
// ---------------------------------------------------------------------------

func TestGraphPath_CyclicGraph_Terminates(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)

	// Seed the forward chain node:1 → node:2 → node:3 via HTTP.
	seedChain(t, s, "alpha", "node", 3)

	// Inject the back-edge node:3 → node:1 directly, closing the cycle.
	// Alpha is tenant 1 → prefix "0001@".
	prefix := tenant.GraphNodePrefix(1)
	n1 := prefix + "node:1"
	n3 := prefix + "node:3"
	if err := s.graph.AddEdge(n3, n1, "back"); err != nil {
		t.Fatalf("inject back-edge: %v", err)
	}

	// /graph/pathExists — forward path within the cycle must resolve.
	w := graphPathExists(t, s, "alpha", "node:1", "node:3", 10)
	m := assertPathOK(t, w)
	if exists, _ := m["exists"].(bool); !exists {
		t.Errorf("cyclic pathExists node:1→node:3: want true, got false")
	}
	if pathLength(t, m) != 2 {
		t.Errorf("cyclic pathExists length: want 2, got %d", pathLength(t, m))
	}

	// /graph/pathExists — back-edge path (node:2 → via cycle → node:1) must
	// also resolve without hanging.
	w2 := graphPathExists(t, s, "alpha", "node:2", "node:1", 10)
	m2 := assertPathOK(t, w2)
	if exists, _ := m2["exists"].(bool); !exists {
		t.Errorf("cyclic pathExists node:2→node:1 via back-edge: want true, got false")
	}

	// /graph/path — must return a valid path and not hang.
	w3 := graphPath(t, s, "alpha", "node:1", "node:3", 10)
	m3 := assertPathOK(t, w3)
	nodes := pathNodes(t, m3)
	if len(nodes) == 0 {
		t.Error("cyclic path node:1→node:3: want non-empty path")
	}
	if nodes[0] != "node:1" || nodes[len(nodes)-1] != "node:3" {
		t.Errorf("cyclic path bounds: want node:1…node:3, got %v", nodes)
	}
	if containsPrefix(w3.Body.String()) {
		t.Errorf("cyclic path response contains XXXX@ prefix: %s", w3.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 16. Concurrent counter accuracy.
//
// The unit contract tests verify counter correctness sequentially. This test
// fires 100 concurrent goroutines writing entities under two tenants and then
// asserts that NodeCountForTenant and EdgeCountForTenant on the underlying
// graph match the expected totals. Counter drift between the global and
// per-tenant maps under write concurrency is the most likely production
// failure mode and was previously untested at this layer.
// ---------------------------------------------------------------------------

func TestGraphCounters_ConcurrentAccuracy(t *testing.T) {
	t.Parallel()
	s := newPathTestServer(t)

	const (
		alphaWrites = 50 // concurrent entity writes for alpha
		betaWrites  = 30 // concurrent entity writes for beta
	)

	// Pre-warm both tenant stores with a single synchronous write each.
	// storeForTenant opens the SQLite connection on first access; if many
	// goroutines race to initialise the same cold tenant simultaneously
	// (especially under -race timing) some opens fail with OLU-ST006.
	// One synchronous write per tenant guarantees the store is cached before
	// the concurrent goroutines start.
	seedGraphEntity(t, s, "alpha", "anode", 0, map[string]interface{}{})
	seedGraphEntity(t, s, "beta", "bnode", 0, map[string]interface{}{})

	var wg sync.WaitGroup

	// Concurrent alpha writes: node i has a REF to node i+1 (except last).
	for i := 1; i <= alphaWrites; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := map[string]interface{}{}
			if id < alphaWrites {
				data["next_ref"] = map[string]interface{}{
					"type":   "REF",
					"entity": "anode",
					"id":     id + 1,
				}
			}
			seedGraphEntity(t, s, "alpha", "anode", id, data)
		}(i)
	}

	// Concurrent beta writes: isolated nodes, no edges.
	for i := 1; i <= betaWrites; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			seedGraphEntity(t, s, "beta", "bnode", id, map[string]interface{}{})
		}(i)
	}

	wg.Wait()

	// Verify via HTTP stats endpoint (which reads NodeCountForTenant /
	// EdgeCountForTenant under the hood).
	wAlpha := tgDo(t, s, http.MethodGet, "alpha", "/stats", nil)
	if wAlpha.Code != http.StatusOK {
		t.Fatalf("alpha stats: want 200, got %d — %s", wAlpha.Code, wAlpha.Body.String())
	}
	alphaStats := decodeGraphJSON(t, wAlpha)

	wBeta := tgDo(t, s, http.MethodGet, "beta", "/stats", nil)
	if wBeta.Code != http.StatusOK {
		t.Fatalf("beta stats: want 200, got %d — %s", wBeta.Code, wBeta.Body.String())
	}
	betaStats := decodeGraphJSON(t, wBeta)

	// Alpha: alphaWrites+1 nodes (warm-up node 0 plus chain nodes 1..alphaWrites),
	// alphaWrites-1 edges (chain: node 1→2→…→alphaWrites; node 0 is isolated).
	alphaN := int(alphaStats["node_count"].(float64))
	alphaE := int(alphaStats["edge_count"].(float64))
	wantAlphaN := alphaWrites + 1
	if alphaN != wantAlphaN {
		t.Errorf("alpha node count: want %d, got %d", wantAlphaN, alphaN)
	}
	wantEdges := alphaWrites - 1
	if alphaE != wantEdges {
		t.Errorf("alpha edge count: want %d, got %d", wantEdges, alphaE)
	}

	// Beta: betaWrites+1 isolated nodes (warm-up node 0 plus nodes 1..betaWrites), 0 edges.
	betaN := int(betaStats["node_count"].(float64))
	betaE := int(betaStats["edge_count"].(float64))
	wantBetaN := betaWrites + 1
	if betaN != wantBetaN {
		t.Errorf("beta node count: want %d, got %d", wantBetaN, betaN)
	}
	if betaE != 0 {
		t.Errorf("beta edge count: want 0, got %d", betaE)
	}

	// Cross-check: verify directly against the graph layer that the counters
	// match. This catches drift between the HTTP layer and the underlying maps.
	alphaPrefix := tenant.GraphNodePrefix(1)
	betaPrefix := tenant.GraphNodePrefix(2)

	graphAlphaN, err := s.graph.NodeCountForTenant(alphaPrefix)
	if err != nil {
		t.Fatalf("NodeCountForTenant(alpha): %v", err)
	}
	if graphAlphaN != wantAlphaN {
		t.Errorf("graph-layer alpha node count: want %d, got %d", wantAlphaN, graphAlphaN)
	}

	graphBetaN, err := s.graph.NodeCountForTenant(betaPrefix)
	if err != nil {
		t.Fatalf("NodeCountForTenant(beta): %v", err)
	}
	if graphBetaN != wantBetaN {
		t.Errorf("graph-layer beta node count: want %d, got %d", wantBetaN, graphBetaN)
	}

	graphAlphaE, err := s.graph.EdgeCountForTenant(alphaPrefix)
	if err != nil {
		t.Fatalf("EdgeCountForTenant(alpha): %v", err)
	}
	if graphAlphaE != wantEdges {
		t.Errorf("graph-layer alpha edge count: want %d, got %d", wantEdges, graphAlphaE)
	}

	graphBetaE, err := s.graph.EdgeCountForTenant(betaPrefix)
	if err != nil {
		t.Fatalf("EdgeCountForTenant(beta): %v", err)
	}
	if graphBetaE != 0 {
		t.Errorf("graph-layer beta edge count: want 0, got %d", graphBetaE)
	}
}

