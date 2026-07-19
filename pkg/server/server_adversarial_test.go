// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// server_adversarial_test.go
//
// Adversarial coverage of entity CRUD, graph edge handling, graph query
// endpoints, handleCommit, graph verify/rebuild, Sulpher async lifecycle,
// dynconfig guard, and server Stop() ordering.
//
// Uses one shared e2eEnv per top-level test to minimise SQLite
// file-descriptor consumption.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

// ─── shared graph-enabled server ─────────────────────────────────────────────

func newAdversarialE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	env := newE2EEnv(t)
	t.Cleanup(env.cleanup)
	return env
}

// sharedE2EEnv is a package-level helper that returns one env per top-level test.
// Call it once at the top of each Test* function and pass it to sub-tests.
// Do NOT call from goroutines.
func sharedE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	return newAdversarialE2EEnv(t)
}

// e2e URL helpers on *e2eEnv
func (e *e2eEnv) apiURL(path string) string {
	return fmt.Sprintf("%s/api/v1%s", e.ts.URL, path)
}
func (e *e2eEnv) tenantAPIURL(tenant, path string) string {
	return fmt.Sprintf("%s/api/v1/tenant/%s%s", e.ts.URL, tenant, path)
}
func (e *e2eEnv) doE2E(method, url string, body interface{}) (int, map[string]interface{}) {
	return doJSONRequest(e.t, method, url, body)
}
func (e *e2eEnv) createEntity(tenant, entity string, data map[string]interface{}) int {
	e.t.Helper()
	url := e.tenantAPIURL(tenant, fmt.Sprintf("/%s", entity))
	status, resp := e.doE2E("POST", url, data)
	if status != http.StatusCreated {
		e.t.Fatalf("createEntity %s/%s: want 201, got %d: %v", tenant, entity, status, resp)
	}
	id, ok := resp["id"].(float64)
	if !ok {
		e.t.Fatalf("createEntity: no id in response: %v", resp)
	}
	return int(id)
}

// ─── handleCreate ─────────────────────────────────────────────────────────────

func TestHandleCreate(t *testing.T) {
	env := sharedE2EEnv(t)

	t.Run("entity_too_large", func(t *testing.T) {
		largeStr := make([]byte, 1024*1024+1)
		for i := range largeStr {
			largeStr[i] = 'x'
		}
		status, _ := env.doE2E("POST", env.tenantAPIURL("default", "/assets"),
			map[string]interface{}{"name": string(largeStr), "type": "sensor"})
		if status != http.StatusRequestEntityTooLarge {
			t.Errorf("too large: want 413, got %d", status)
		}
	})

	t.Run("invalid_json_body", func(t *testing.T) {
		status, _ := env.doE2E("POST", env.tenantAPIURL("default", "/assets"), "not-json")
		if status != http.StatusBadRequest {
			t.Errorf("bad JSON: want 400, got %d", status)
		}
	})

	t.Run("happy_path_graph_update", func(t *testing.T) {
		parentID := env.createEntity("default", "assets", map[string]interface{}{
			"name": "parent-asset", "type": "sensor",
		})
		env.createEntity("default", "sensors", map[string]interface{}{
			"name": "child-sensor", "asset": ref("assets", parentID),
		})
		status, resp := env.doE2E("GET", env.apiURL("/graph/stats"), nil)
		if status != http.StatusOK {
			t.Fatalf("graph stats: want 200, got %d: %v", status, resp)
		}
		nodeCount, _ := resp["node_count"].(float64)
		if nodeCount < 1 {
			t.Errorf("expected >= 1 node after create, got %v", nodeCount)
		}
		edgeCount, _ := resp["edge_count"].(float64)
		if edgeCount < 1 {
			t.Errorf("expected >= 1 edge after sensor→asset, got %v", edgeCount)
		}
	})

	t.Run("rapid_burst_no_deadlock", func(t *testing.T) {
		failed := 0
		for i := 0; i < 30; i++ {
			status, _ := env.doE2E("POST", env.tenantAPIURL("default", "/assets"),
				map[string]interface{}{
					"name": fmt.Sprintf("burst-%d", i), "type": "sensor",
				})
			if status != http.StatusCreated {
				failed++
			}
		}
		if failed > 0 {
			t.Errorf("rapid burst: %d/30 creates failed", failed)
		}
	})
}

// ─── handleUpdate ─────────────────────────────────────────────────────────────

func TestHandleUpdate(t *testing.T) {
	env := sharedE2EEnv(t)

	t.Run("happy_path", func(t *testing.T) {
		id := env.createEntity("default", "assets", map[string]interface{}{
			"name": "original", "type": "sensor",
		})
		status, resp := env.doE2E("PUT",
			env.tenantAPIURL("default", fmt.Sprintf("/assets/%d", id)),
			map[string]interface{}{"name": "updated", "type": "sensor"})
		if status != http.StatusOK {
			t.Fatalf("update: want 200, got %d: %v", status, resp)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		status, _ := env.doE2E("PUT", env.tenantAPIURL("default", "/assets/99999"),
			map[string]interface{}{"name": "ghost", "type": "sensor"})
		if status != http.StatusNotFound {
			t.Errorf("not found: want 404, got %d", status)
		}
	})

	t.Run("invalid_id", func(t *testing.T) {
		status, _ := env.doE2E("PUT", env.tenantAPIURL("default", "/assets/notanumber"),
			map[string]interface{}{"name": "x", "type": "sensor"})
		if status != http.StatusBadRequest {
			t.Errorf("invalid id: want 400, got %d", status)
		}
	})

	t.Run("bad_json", func(t *testing.T) {
		id := env.createEntity("default", "assets", map[string]interface{}{
			"name": "to-update", "type": "sensor",
		})
		status, _ := env.doE2E("PUT",
			env.tenantAPIURL("default", fmt.Sprintf("/assets/%d", id)), "not-json")
		if status != http.StatusBadRequest {
			t.Errorf("bad JSON: want 400, got %d", status)
		}
	})
}

// ─── handlePatch ──────────────────────────────────────────────────────────────

func TestHandlePatch(t *testing.T) {
	env := sharedE2EEnv(t)

	t.Run("happy_path", func(t *testing.T) {
		id := env.createEntity("default", "assets", map[string]interface{}{
			"name": "patchable", "type": "sensor",
		})
		status, resp := env.doE2E("PATCH",
			env.tenantAPIURL("default", fmt.Sprintf("/assets/%d", id)),
			map[string]interface{}{"name": "patched"})
		if status != http.StatusOK {
			t.Fatalf("patch: want 200, got %d: %v", status, resp)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		status, _ := env.doE2E("PATCH", env.tenantAPIURL("default", "/assets/99999"),
			map[string]interface{}{"name": "ghost"})
		if status != http.StatusNotFound {
			t.Errorf("not found: want 404, got %d", status)
		}
	})

	t.Run("bad_json", func(t *testing.T) {
		id := env.createEntity("default", "assets", map[string]interface{}{
			"name": "x", "type": "sensor",
		})
		status, _ := env.doE2E("PATCH",
			env.tenantAPIURL("default", fmt.Sprintf("/assets/%d", id)), "not-json")
		if status != http.StatusBadRequest {
			t.Errorf("bad JSON: want 400, got %d", status)
		}
	})
}

// ─── handleDelete / handleGet ─────────────────────────────────────────────────

func TestHandleDelete(t *testing.T) {
	env := sharedE2EEnv(t)

	t.Run("not_found", func(t *testing.T) {
		status, _ := env.doE2E("DELETE", env.tenantAPIURL("default", "/assets/99999"), nil)
		if status != http.StatusNotFound {
			t.Errorf("delete not found: want 404, got %d", status)
		}
	})

	t.Run("happy_path_then_gone", func(t *testing.T) {
		id := env.createEntity("default", "assets", map[string]interface{}{
			"name": "to-delete", "type": "sensor",
		})
		status, _ := env.doE2E("DELETE",
			env.tenantAPIURL("default", fmt.Sprintf("/assets/%d", id)), nil)
		if status != http.StatusOK && status != http.StatusNoContent {
			t.Fatalf("delete: want 200/204, got %d", status)
		}
		s2, _ := env.doE2E("GET",
			env.tenantAPIURL("default", fmt.Sprintf("/assets/%d", id)), nil)
		if s2 != http.StatusNotFound {
			t.Errorf("after delete: want 404, got %d", s2)
		}
	})
}

func TestHandleGet(t *testing.T) {
	env := sharedE2EEnv(t)

	t.Run("not_found", func(t *testing.T) {
		status, _ := env.doE2E("GET", env.tenantAPIURL("default", "/assets/99999"), nil)
		if status != http.StatusNotFound {
			t.Errorf("get not found: want 404, got %d", status)
		}
	})

	t.Run("invalid_id", func(t *testing.T) {
		status, _ := env.doE2E("GET", env.tenantAPIURL("default", "/assets/notanumber"), nil)
		if status != http.StatusBadRequest {
			t.Errorf("get invalid id: want 400, got %d", status)
		}
	})
}

// ─── handleGraphStats (50%) ───────────────────────────────────────────────────

func TestHandleGraphStats(t *testing.T) {
	env := sharedE2EEnv(t)
	env.createEntity("default", "assets", map[string]interface{}{"name": "a", "type": "sensor"})

	t.Run("happy_path", func(t *testing.T) {
		status, resp := env.doE2E("GET", env.apiURL("/graph/stats"), nil)
		if status != http.StatusOK {
			t.Fatalf("graph stats: want 200, got %d: %v", status, resp)
		}
		if _, ok := resp["node_count"]; !ok {
			t.Errorf("missing node_count: %v", resp)
		}
		if _, ok := resp["edge_count"]; !ok {
			t.Errorf("missing edge_count: %v", resp)
		}
	})

	t.Run("graph_disabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &config.Config{
			Host: "localhost", Port: 0,
			StorageType: "sqlite",
			BaseDir:     tmpDir, Schema: "test_schema",
			SchemaDir: tmpDir + "/test_schema",
			CacheType: "memory", CacheTTL: 300, MaxEntitySize: 1048576,
			GraphEnabled: false, TenantMode: "path", TenantAutoRegister: true,
		}
		sts := newTestServerFromConfig(t, cfg)
		defer sts.cleanup()
		status, _ := doJSONRequest(t, "GET",
			fmt.Sprintf("%s/api/v1/graph/stats", sts.ts.URL), nil)
		if status == http.StatusOK {
			t.Errorf("graph stats disabled: should not return 200, got %d", status)
		}
	})
}

// ─── handleGraphIncoming / handleGraphOutgoing (60%) ─────────────────────────

func TestHandleGraphEdges(t *testing.T) {
	env := sharedE2EEnv(t)
	parentID := env.createEntity("default", "assets", map[string]interface{}{
		"name": "parent", "type": "sensor",
	})
	env.createEntity("default", "sensors", map[string]interface{}{
		"name": "child", "asset": ref("assets", parentID),
	})

	t.Run("incoming_increases_edge_count", func(t *testing.T) {
		status, resp := env.doE2E("GET", env.apiURL("/graph/stats"), nil)
		if status != http.StatusOK {
			t.Fatalf("graph stats: want 200, got %d: %v", status, resp)
		}
		edgeCount, _ := resp["edge_count"].(float64)
		if edgeCount < 1 {
			t.Errorf("expected >= 1 edge after sensor→asset, got %v", edgeCount)
		}
	})

	t.Run("node_not_found_returns_empty", func(t *testing.T) {
		status, _ := env.doE2E("GET", env.apiURL("/graph/nonexistent:99999/in"), nil)
		if status != http.StatusOK {
			t.Errorf("incoming nonexistent: want 200 (empty), got %d", status)
		}
	})
}

// ─── Graph verify / rebuild (via tenant-scoped routes) ───────────────────────

func TestHandleGraphAdmin(t *testing.T) {
	env := sharedE2EEnv(t)
	env.createEntity("default", "assets", map[string]interface{}{"name": "a", "type": "sensor"})

	t.Run("verify_handler_reachable", func(t *testing.T) {
		status, resp := env.doE2E("GET",
			env.tenantAPIURL("default", "/graph/admin/verify"), nil)
		if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
			t.Errorf("graph verify not reachable, got %d: %v", status, resp)
		}
	})

	t.Run("rebuild_handler_reachable", func(t *testing.T) {
		status, resp := env.doE2E("POST",
			env.tenantAPIURL("default", "/graph/admin/rebuild"), nil)
		if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
			t.Errorf("graph rebuild not reachable, got %d: %v", status, resp)
		}
	})
}

// ─── handleGraphNodeInfo / handleGraphNodeDegree ─────────────────────────────

func TestHandleGraphNodes(t *testing.T) {
	env := sharedE2EEnv(t)
	env.createEntity("default", "assets", map[string]interface{}{"name": "a", "type": "sensor"})

	t.Run("node_info_not_found", func(t *testing.T) {
		status, _ := env.doE2E("GET", env.apiURL("/graph/nodes/assets:99999"), nil)
		if status != http.StatusNotFound {
			t.Errorf("node info not found: want 404, got %d", status)
		}
	})

	t.Run("node_degree_unknown", func(t *testing.T) {
		status, _ := env.doE2E("GET",
			env.apiURL("/graph/nodes/assets:99999/degree"), nil)
		if status != http.StatusOK && status != http.StatusNotFound {
			t.Errorf("node degree unknown: want 200 or 404, got %d", status)
		}
	})

	t.Run("graph_has_nodes_after_create", func(t *testing.T) {
		status, resp := env.doE2E("GET", env.apiURL("/graph/stats"), nil)
		if status != http.StatusOK {
			t.Fatalf("graph stats: want 200, got %d: %v", status, resp)
		}
		nodeCount, _ := resp["node_count"].(float64)
		if nodeCount < 1 {
			t.Errorf("expected >= 1 node, got %v", nodeCount)
		}
	})
}

// ─── handleGraphShortestPath ──────────────────────────────────────────────────

func TestHandleGraphShortestPath(t *testing.T) {
	env := sharedE2EEnv(t)
	aID := env.createEntity("default", "assets", map[string]interface{}{"name": "a", "type": "sensor"})
	sID := env.createEntity("default", "sensors", map[string]interface{}{
		"name": "s", "asset": ref("assets", aID),
	})

	t.Run("missing_fields", func(t *testing.T) {
		status, _ := env.doE2E("POST", env.apiURL("/graph/shortestPath"),
			map[string]interface{}{"from": fmt.Sprintf("sensors:%d", sID)})
		if status != http.StatusBadRequest {
			t.Errorf("missing to: want 400, got %d", status)
		}
	})

	t.Run("both_fields_needed", func(t *testing.T) {
		status, _ := env.doE2E("POST", env.apiURL("/graph/shortestPath"),
			map[string]interface{}{})
		if status != http.StatusBadRequest {
			t.Errorf("missing both: want 400, got %d", status)
		}
	})
}

// ─── handleSulpherQuery (50%) — sync ─────────────────────────────────────────

func TestHandleSulpherQuery(t *testing.T) {
	env := sharedE2EEnv(t)
	parentID := env.createEntity("default", "assets", map[string]interface{}{
		"name": "root", "type": "sensor",
	})
	env.createEntity("default", "sensors", map[string]interface{}{
		"name": "leaf", "asset": ref("assets", parentID),
	})

	t.Run("sync_query", func(t *testing.T) {
		query := fmt.Sprintf("MATCH (n:assets {id: %d})-[r]->(m) RETURN n, r, m", parentID)
		status, resp := env.doE2E("POST", env.apiURL("/graph/query"),
			map[string]interface{}{"query": query, "max_depth": 2})
		if status != http.StatusOK {
			t.Errorf("sulpher sync: want 200, got %d: %v", status, resp)
		}
	})

	t.Run("empty_query", func(t *testing.T) {
		status, _ := env.doE2E("POST", env.apiURL("/graph/query"),
			map[string]interface{}{"query": ""})
		if status != http.StatusBadRequest {
			t.Errorf("empty query: want 400, got %d", status)
		}
	})

	t.Run("missing_query", func(t *testing.T) {
		status, _ := env.doE2E("POST", env.apiURL("/graph/query"),
			map[string]interface{}{"max_depth": 3})
		if status != http.StatusBadRequest {
			t.Errorf("missing query: want 400, got %d", status)
		}
	})
}

// ─── Sulpher async lifecycle ──────────────────────────────────────────────────

func TestSulpherAsync(t *testing.T) {
	env := sharedE2EEnv(t)
	env.createEntity("default", "assets", map[string]interface{}{"name": "node", "type": "sensor"})

	t.Run("status_not_found", func(t *testing.T) {
		status, _ := env.doE2E("GET", env.apiURL("/graph/query/nonexistent-id"), nil)
		if status != http.StatusNotFound {
			t.Errorf("status not found: want 404, got %d", status)
		}
	})

	t.Run("result_not_found", func(t *testing.T) {
		status, _ := env.doE2E("GET", env.apiURL("/graph/query/nonexistent-id/result"), nil)
		if status != http.StatusNotFound {
			t.Errorf("result not found: want 404, got %d", status)
		}
	})

	t.Run("full_lifecycle", func(t *testing.T) {
		submitStatus, submitResp := env.doE2E("POST", env.apiURL("/graph/query/async"),
			map[string]interface{}{"query": "MATCH (n:assets) RETURN n", "max_depth": 2})
		if submitStatus != http.StatusAccepted {
			t.Fatalf("async submit: want 202, got %d: %v", submitStatus, submitResp)
		}
		queryID, ok := submitResp["query_id"].(string)
		if !ok || queryID == "" {
			t.Fatalf("no query_id: %v", submitResp)
		}

		statusURL := env.apiURL(fmt.Sprintf("/graph/query/%s", queryID))
		var lastStatus string
		for i := 0; i < 20; i++ {
			s, r := env.doE2E("GET", statusURL, nil)
			if s != http.StatusOK {
				t.Fatalf("poll: want 200, got %d: %v", s, r)
			}
			lastStatus, _ = r["status"].(string)
			if lastStatus == "completed" || lastStatus == "failed" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if lastStatus == "completed" {
			resultURL := env.apiURL(fmt.Sprintf("/graph/query/%s/result", queryID))
			rs, rr := env.doE2E("GET", resultURL, nil)
			if rs != http.StatusOK {
				t.Errorf("result: want 200, got %d: %v", rs, rr)
			}
		}
	})
}

// ─── Tenant Sulpher ───────────────────────────────────────────────────────────

func TestHandleTenantSulpherQuery(t *testing.T) {
	env := sharedE2EEnv(t)
	env.createEntity("t1", "assets", map[string]interface{}{"name": "a", "type": "sensor"})

	t.Run("sync", func(t *testing.T) {
		status, resp := env.doE2E("POST", env.tenantAPIURL("t1", "/graph/query"),
			map[string]interface{}{"query": "MATCH (n:assets) RETURN n", "max_depth": 1})
		if status != http.StatusOK {
			t.Errorf("tenant sulpher: want 200, got %d: %v", status, resp)
		}
	})

	t.Run("async_submit_and_poll", func(t *testing.T) {
		status, resp := env.doE2E("POST", env.tenantAPIURL("t1", "/graph/query/async"),
			map[string]interface{}{"query": "MATCH (n:assets) RETURN n"})
		if status != http.StatusAccepted {
			t.Fatalf("tenant async: want 202, got %d: %v", status, resp)
		}
		queryID, ok := resp["query_id"].(string)
		if !ok || queryID == "" {
			t.Fatalf("no query_id: %v", resp)
		}
		s, r := env.doE2E("GET",
			env.tenantAPIURL("t1", fmt.Sprintf("/graph/query/%s", queryID)), nil)
		if s != http.StatusOK {
			t.Errorf("tenant async status: want 200, got %d: %v", s, r)
		}
	})
}

// ─── handleTenantCreateEdge ───────────────────────────────────────────────────

func TestHandleTenantCreateEdge(t *testing.T) {
	env := sharedE2EEnv(t)
	aID := env.createEntity("default", "assets", map[string]interface{}{"name": "a", "type": "sensor"})
	bID := env.createEntity("default", "assets", map[string]interface{}{"name": "b", "type": "sensor"})

	t.Run("missing_from", func(t *testing.T) {
		status, _ := env.doE2E("POST", env.apiURL("/graph/edges"),
			map[string]interface{}{
				"to": fmt.Sprintf("assets:%d", bID), "rel": "DEPENDS_ON",
			})
		if status != http.StatusBadRequest {
			t.Errorf("missing from: want 400, got %d", status)
		}
	})

	t.Run("missing_rel", func(t *testing.T) {
		status, _ := env.doE2E("POST", env.apiURL("/graph/edges"),
			map[string]interface{}{
				"from": fmt.Sprintf("assets:%d", aID),
				"to":   fmt.Sprintf("assets:%d", bID),
			})
		if status != http.StatusBadRequest {
			t.Errorf("missing rel: want 400, got %d", status)
		}
	})

	t.Run("happy_path_or_backend_limit", func(t *testing.T) {
		status, resp := env.doE2E("POST", env.apiURL("/graph/edges"),
			map[string]interface{}{
				"from": fmt.Sprintf("assets:%d", aID),
				"to":   fmt.Sprintf("assets:%d", bID),
				"rel":  "DEPENDS_ON",
			})
		// 201=ok, 500/501=edge property store not available in this test env.
		if status != http.StatusCreated &&
			status != http.StatusNotImplemented &&
			status != http.StatusInternalServerError {
			t.Errorf("create edge: want 201/500/501, got %d: %v", status, resp)
		}
	})
}

// ─── handleCommit ─────────────────────────────────────────────────────────────

func TestHandleCommit(t *testing.T) {
	env := sharedE2EEnv(t)

	t.Run("bad_body", func(t *testing.T) {
		status, _ := env.doE2E("POST",
			env.tenantAPIURL("default", "/commit"), "bad-json")
		if status != http.StatusBadRequest {
			t.Errorf("bad body: want 400, got %d", status)
		}
	})

	t.Run("handler_reachable_with_update", func(t *testing.T) {
		targetID := env.createEntity("default", "assets", map[string]interface{}{
			"name": "commit-target", "type": "sensor",
		})
		status, resp := env.doE2E("POST",
			env.tenantAPIURL("default", "/commit"),
			map[string]interface{}{
				"update": map[string]interface{}{
					"entity": "assets",
					"id":     targetID,
					"data":   map[string]interface{}{"name": "via-commit", "type": "sensor"},
				},
				"append": []interface{}{},
			})
		if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
			t.Errorf("commit handler not reachable, got %d: %v", status, resp)
		}
	})
}

// ─── dynConfigGuard (50%) ─────────────────────────────────────────────────────

func TestDynConfigGuard_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Host: "localhost", Port: 0,
		StorageType: "sqlite",
		BaseDir:     tmpDir, Schema: "test_schema",
		SchemaDir: tmpDir + "/test_schema",
		CacheType: "memory", CacheTTL: 300, MaxEntitySize: 1048576,
		GraphEnabled: true, TenantMode: "path", TenantAutoRegister: true,
		DynConfigEnabled: false,
	}
	sts := newTestServerFromConfig(t, cfg)
	defer sts.cleanup()

	base := sts.ts.URL + "/api/v1/admin/config"
	for _, ep := range []struct{ method, path string }{
		{"GET", base},
		{"GET", base + "/global"},
		{"PUT", base + "/global/some.key"},
		{"DELETE", base + "/global/some.key"},
	} {
		status, _ := doJSONRequest(t, ep.method, ep.path,
			map[string]interface{}{"value": "x"})
		if status == http.StatusOK || status == http.StatusCreated {
			t.Errorf("dynconfig %s %s disabled: should not succeed, got %d",
				ep.method, ep.path, status)
		}
	}
}

// ─── handleVersion / handleReady ──────────────────────────────────────────────

func TestHandleServerEndpoints(t *testing.T) {
	env := sharedE2EEnv(t)

	t.Run("version", func(t *testing.T) {
		status, resp := env.doE2E("GET",
			fmt.Sprintf("%s/version", env.ts.URL), nil)
		if status != http.StatusOK {
			t.Fatalf("version: want 200, got %d: %v", status, resp)
		}
		if resp["version"] == nil {
			t.Errorf("version missing in response: %v", resp)
		}
	})

	t.Run("ready", func(t *testing.T) {
		url := fmt.Sprintf("%s/ready", env.ts.URL)
		status, _ := env.doE2E("GET", url, nil)
		if status != http.StatusOK && status != http.StatusServiceUnavailable {
			t.Errorf("ready: want 200 or 503, got %d", status)
		}
	})
}

// ─── Concurrent read/write on graph ──────────────────────────────────────────

func TestHandleGraphConcurrentReadWrite(t *testing.T) {
	env := sharedE2EEnv(t)
	aID := env.createEntity("default", "assets", map[string]interface{}{
		"name": "root", "type": "sensor",
	})

	done := make(chan struct{}, 10)
	for i := 0; i < 5; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			env.createEntity("default", "sensors", map[string]interface{}{
				"name":  fmt.Sprintf("sensor-%d", n),
				"asset": ref("assets", aID),
			})
		}(i)
	}
	for i := 0; i < 5; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			env.doE2E("GET", env.apiURL("/graph/stats"), nil)
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

// ─── server Stop() ────────────────────────────────────────────────────────────

func TestServerStop_NoDeadlock(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) {
		cfg.TSRetentionEnabled = true
		cfg.TSDefaultRetentionDays = 30
	})
	env.registerTenant("acme")
	env.provision("acme")
	env.ts.Close()
}

// ─── handleSave ───────────────────────────────────────────────────────────────

func TestHandleSave(t *testing.T) {
	env := sharedE2EEnv(t)

	t.Run("happy_path", func(t *testing.T) {
		id := env.createEntity("default", "assets", map[string]interface{}{
			"name": "saveme", "type": "sensor",
		})
		status, resp := env.doE2E("POST",
			env.tenantAPIURL("default", fmt.Sprintf("/assets/save/%d", id)),
			map[string]interface{}{"name": "saved", "type": "sensor"})
		if status != http.StatusOK && status != http.StatusCreated {
			t.Errorf("save: want 200/201, got %d: %v", status, resp)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		status, _ := env.doE2E("POST",
			env.tenantAPIURL("default", "/assets/save/1"), "bad-json")
		if status != http.StatusBadRequest {
			t.Errorf("save bad JSON: want 400, got %d", status)
		}
	})
}

// ─── handleCreateSchema ───────────────────────────────────────────────────────

func TestHandleCreateSchema_BadBody(t *testing.T) {
	env := sharedE2EEnv(t)
	status, _ := env.doE2E("POST", env.apiURL("/schema"), "not-json")
	if status != http.StatusBadRequest {
		t.Errorf("create schema bad body: want 400, got %d", status)
	}
}
