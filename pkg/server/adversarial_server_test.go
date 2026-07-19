// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// adversarial_server_test.go — server-layer adversarial and edge-case tests.
//
// All tests use the SQLite-backed commitEnv.
// deprecated and scheduled for removal before v1.0.0; it is not exercised
// here.
//
// Tests use the standard REST API (POST /api/v1/{entity}, PUT, DELETE) rather
// than /commit, because the commit endpoint is a compound atomic operation
// that requires update+append together and is already covered by
// commit_e2e_test.go.  The adversarial concerns here are graph coherency,
// cache invalidation under various conditions, and REF field handling.
//
// Scenarios:
//  1. POST entity with a dangling REF — server must not crash; GET returns REF.
//  2. GET with embed_depth on a dangling REF — returns raw REF, not 500.
//  3. Sequential saves to the same entity — GET always returns latest value.
//  4. DELETE clears the cache — GET after DELETE must 404.
//  5. List reflects a new entity immediately — list cache invalidated on write.
//  6. Degenerate REF shapes in POST body — must not 500.
//  7. Concurrent saves to the same entity — no corruption on winner; no 500s.
//  8. Commit (proper use) with append — appended entity is immediately readable.

package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// ----------------------------------------------------------------------------
// Environment
// ----------------------------------------------------------------------------
//
// newAdversarialEnv reuses commitEnv: SQLite-backed, GraphEnabled,
// TenantAutoRegister.  The standard REST endpoints (POST/GET/PUT/DELETE) all
// work against this server.

func newAdversarialEnv(t *testing.T) *commitEnv {
	t.Helper()
	return newCommitEnv(t)
}

// post creates an entity and returns (status, parsed body).
func advPost(t *testing.T, env *commitEnv, entity string, data map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	return env.doJSON("POST", "/api/v1/"+entity, data)
}

// get fetches one entity.
func advGet(t *testing.T, env *commitEnv, entity string, id int) (int, map[string]interface{}) {
	t.Helper()
	return env.doJSON("GET", fmt.Sprintf("/api/v1/%s/%d", entity, id), nil)
}

// put replaces an entity.
func advPut(t *testing.T, env *commitEnv, entity string, id int, data map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	return env.doJSON("PUT", fmt.Sprintf("/api/v1/%s/%d", entity, id), data)
}

// del deletes an entity.
func advDel(t *testing.T, env *commitEnv, entity string, id int) int {
	t.Helper()
	status, _ := env.do("DELETE", fmt.Sprintf("/api/v1/%s/%d", entity, id), nil)
	return status
}

// list fetches the entity collection and returns the "data" slice.
func advList(t *testing.T, env *commitEnv, entity string) (int, []interface{}) {
	t.Helper()
	status, resp := env.doJSON("GET", "/api/v1/"+entity, nil)
	items, _ := resp["data"].([]interface{})
	return status, items
}

// mustCreate creates an entity, fatally failing the test if unsuccessful.
// Returns the assigned ID.
func mustCreate(t *testing.T, env *commitEnv, entity string, data map[string]interface{}) int {
	t.Helper()
	status, resp := advPost(t, env, entity, data)
	if status != http.StatusCreated {
		t.Fatalf("POST /api/v1/%s: want 201, got %d — %v", entity, status, resp)
	}
	id, ok := resp["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("POST /api/v1/%s: missing valid id in response: %v", entity, resp)
	}
	return int(id)
}

// ----------------------------------------------------------------------------
// 1. POST entity with a dangling REF — graph and server must not crash
// ----------------------------------------------------------------------------

func TestAdversarial_Post_DanglingREF(t *testing.T) {
	env := newAdversarialEnv(t)
	defer env.cleanup()

	id := mustCreate(t, env, "assets", map[string]interface{}{
		"name":       "orphaned-asset",
		"sensor_ref": map[string]interface{}{"type": "REF", "entity": "sensors", "id": float64(9999)},
	})

	// GET must succeed and return the stored REF.
	getStatus, getResp := advGet(t, env, "assets", id)
	if getStatus != http.StatusOK {
		t.Fatalf("GET after dangling-REF post: want 200, got %d", getStatus)
	}
	refField, ok := getResp["sensor_ref"].(map[string]interface{})
	if !ok {
		t.Fatalf("sensor_ref missing or wrong type in GET response: %v", getResp)
	}
	if refField["type"] != "REF" || refField["entity"] != "sensors" {
		t.Errorf("sensor_ref shape wrong: %v", refField)
	}

	// Graph path to the dangling target must not 500.
	pathStatus, _ := env.doJSON("POST", "/api/v1/graph/path", map[string]interface{}{
		"from":      fmt.Sprintf("assets:%d", id),
		"to":        "sensors:9999",
		"max_depth": 5,
	})
	if pathStatus == http.StatusInternalServerError {
		t.Errorf("graph/path with dangling REF target returned 500")
	}
}

// ----------------------------------------------------------------------------
// 2. GET with embed_depth on a dangling REF — raw REF returned, not 500
// ----------------------------------------------------------------------------

func TestAdversarial_Embed_DanglingREF_NoPanic(t *testing.T) {
	env := newAdversarialEnv(t)
	defer env.cleanup()

	id := mustCreate(t, env, "assets", map[string]interface{}{
		"name":       "embed-orphan",
		"sensor_ref": map[string]interface{}{"type": "REF", "entity": "sensors", "id": float64(8888)},
	})

	getStatus, getResp := env.doJSON("GET", fmt.Sprintf("/api/v1/assets/%d?embed_depth=2", id), nil)
	if getStatus != http.StatusOK {
		t.Fatalf("GET with embed_depth on dangling REF: want 200, got %d", getStatus)
	}
	refField, ok := getResp["sensor_ref"].(map[string]interface{})
	if !ok {
		t.Fatalf("sensor_ref missing or wrong type in response: %v", getResp)
	}
	if refField["type"] != "REF" {
		t.Errorf("expected raw REF map when target absent, got: %v", refField)
	}
}

// ----------------------------------------------------------------------------
// 3. Sequential saves — GET always returns latest value, no stale cache
// ----------------------------------------------------------------------------

func TestAdversarial_SequentialSaves_NoCacheStale(t *testing.T) {
	env := newAdversarialEnv(t)
	defer env.cleanup()

	id := mustCreate(t, env, "assets", map[string]interface{}{
		"name":    "v0",
		"counter": float64(0),
	})

	const iterations = 8
	for i := 1; i <= iterations; i++ {
		upStatus, upResp := advPut(t, env, "assets", id, map[string]interface{}{
			"name":    fmt.Sprintf("v%d", i),
			"counter": float64(i),
		})
		if upStatus != http.StatusOK {
			t.Fatalf("iter %d PUT: want 200, got %d — %v", i, upStatus, upResp)
		}

		readStatus, readResp := advGet(t, env, "assets", id)
		if readStatus != http.StatusOK {
			t.Fatalf("iter %d GET: want 200, got %d", i, readStatus)
		}
		wantName := fmt.Sprintf("v%d", i)
		if gotName, _ := readResp["name"].(string); gotName != wantName {
			t.Errorf("iter %d: stale cache? name=%q, want %q", i, gotName, wantName)
		}
		if gotCounter, _ := readResp["counter"].(float64); int(gotCounter) != i {
			t.Errorf("iter %d: stale cache? counter=%v, want %d", i, gotCounter, i)
		}
	}
}

// ----------------------------------------------------------------------------
// 4. DELETE clears the cache — GET after DELETE must 404
// ----------------------------------------------------------------------------

func TestAdversarial_DeleteClearsCache(t *testing.T) {
	env := newAdversarialEnv(t)
	defer env.cleanup()

	id := mustCreate(t, env, "assets", map[string]interface{}{"name": "to-be-deleted"})

	// Warm the read cache.
	if st, _ := advGet(t, env, "assets", id); st != http.StatusOK {
		t.Fatalf("pre-delete GET: want 200, got %d", st)
	}

	if st := advDel(t, env, "assets", id); st != http.StatusNoContent && st != http.StatusOK {
		t.Fatalf("delete: want 204/200, got %d", st)
	}

	// Must 404 — not return the cached pre-deletion body.
	if st, _ := advGet(t, env, "assets", id); st != http.StatusNotFound {
		t.Errorf("GET after delete: want 404, got %d (stale cache?)", st)
	}
}

// ----------------------------------------------------------------------------
// 5. List reflects a newly created entity (list cache invalidated on write)
// ----------------------------------------------------------------------------

func TestAdversarial_ListCacheInvalidatedAfterCreate(t *testing.T) {
	env := newAdversarialEnv(t)
	defer env.cleanup()

	// Prime the list cache.
	listStatus, initialItems := advList(t, env, "assets")
	if listStatus != http.StatusOK {
		t.Fatalf("initial list: want 200, got %d", listStatus)
	}
	initialCount := len(initialItems)

	mustCreate(t, env, "assets", map[string]interface{}{"name": "list-cache-probe"})

	afterStatus, afterItems := advList(t, env, "assets")
	if afterStatus != http.StatusOK {
		t.Fatalf("post-create list: want 200, got %d", afterStatus)
	}
	if len(afterItems) != initialCount+1 {
		t.Errorf("post-create list count: want %d, got %d (list cache not invalidated?)",
			initialCount+1, len(afterItems))
	}
}

// ----------------------------------------------------------------------------
// 6. Degenerate REF shapes in POST body — must not 500
// ----------------------------------------------------------------------------

func TestAdversarial_Post_StructurallyInvalidREF(t *testing.T) {
	env := newAdversarialEnv(t)
	defer env.cleanup()

	cases := []struct {
		name  string
		field map[string]interface{}
	}{
		{
			name:  "wrong type value",
			field: map[string]interface{}{"type": "NOTREF", "entity": "sensors", "id": float64(1)},
		},
		{
			name:  "missing id key",
			field: map[string]interface{}{"type": "REF", "entity": "sensors"},
		},
		{
			name:  "empty entity string",
			field: map[string]interface{}{"type": "REF", "entity": "", "id": float64(42)},
		},
		{
			name:  "id is string not number",
			field: map[string]interface{}{"type": "REF", "entity": "sensors", "id": "not-a-number"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			status, resp := advPost(t, env, "assets", map[string]interface{}{
				"name":       "degenerate-ref-test",
				"sensor_ref": tc.field,
			})
			if status == http.StatusInternalServerError {
				t.Errorf("case %q: POST returned 500 — server must not crash: %v", tc.name, resp)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// 7. Concurrent saves to the same entity — no corruption, no 500s
// ----------------------------------------------------------------------------

func TestAdversarial_ConcurrentSaves_NoCorruption(t *testing.T) {
	env := newAdversarialEnv(t)
	defer env.cleanup()

	id := mustCreate(t, env, "assets", map[string]interface{}{
		"name":    "concurrent-base",
		"counter": float64(0),
	})

	const goroutines = 8
	var wg sync.WaitGroup
	results := make(chan int, goroutines)

	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _ := advPut(t, env, "assets", id, map[string]interface{}{
				"name":    fmt.Sprintf("goroutine-%d", g),
				"counter": float64(g),
			})
			results <- st
		}()
	}

	wg.Wait()
	close(results)

	var ok200, other int
	for st := range results {
		switch st {
		case http.StatusOK:
			ok200++
		case http.StatusInternalServerError:
			other++
			t.Errorf("concurrent PUT returned 500")
		}
	}
	t.Logf("concurrent saves: %d OK, %d other (non-500)", ok200, goroutines-ok200-other)

	// Final state must be consistent and readable.
	finalStatus, finalResp := advGet(t, env, "assets", id)
	if finalStatus != http.StatusOK {
		t.Fatalf("GET after concurrent saves: want 200, got %d", finalStatus)
	}
	if _, ok := finalResp["name"].(string); !ok {
		t.Errorf("final entity missing 'name' field: %v", finalResp)
	}
}

// ----------------------------------------------------------------------------
// 8. Commit: appended entity is immediately readable
// ----------------------------------------------------------------------------

func TestAdversarial_CommitAppend_ImmediatelyReadable(t *testing.T) {
	env := newAdversarialEnv(t)
	defer env.cleanup()

	status, resp := env.doJSON("POST", commitURL(""), map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "assets",
			"id":     float64(8001),
			"data":   map[string]interface{}{"name": "parent-asset"},
		},
		"append": []interface{}{
			map[string]interface{}{
				"entity": "events",
				"data":   map[string]interface{}{"kind": "created", "asset_id": float64(8001)},
			},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("commit with append: want 200, got %d — %v", status, resp)
	}

	appended, _ := resp["appended"].([]interface{})
	if len(appended) != 1 {
		t.Fatalf("expected 1 appended result, got %d", len(appended))
	}
	appendedEntry, _ := appended[0].(map[string]interface{})
	eventID := int(appendedEntry["id"].(float64))
	if eventID <= 0 {
		t.Fatalf("appended event ID invalid: %v", appendedEntry)
	}

	getStatus, getResp := advGet(t, env, "events", eventID)
	if getStatus != http.StatusOK {
		t.Fatalf("GET appended event %d: want 200, got %d", eventID, getStatus)
	}
	if kind, _ := getResp["kind"].(string); kind != "created" {
		t.Errorf("appended event 'kind': want %q, got %q", "created", kind)
	}
}

// Silence the unused import warning; json is used indirectly via doJSON internals.
var _ = json.Marshal
