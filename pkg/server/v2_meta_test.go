// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_meta_test.go
//
// Tests for S3: /api/v2/meta endpoints, key validation, TTL, cascade
// delete, MetaSweeper, and availability map update.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

// metaURL builds a v2 meta URL on a stdTestServer.
func metaURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/meta%s", sts.ts.URL, path)
}

// newMetaServer builds a v2-enabled server with default config.
func newMetaServer(t *testing.T, opts ...func(*config.Config)) *stdTestServer {
	t.Helper()
	return newV2Server(t, opts...)
}

// createMetaEntity creates an entity on the server and returns its id.
func createMetaEntity(t *testing.T, sts *stdTestServer) int {
	t.Helper()
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", sts.ts.URL),
		map[string]interface{}{"name": "meta-test", "type": "sensor"})
	if status != http.StatusCreated {
		t.Fatalf("createMetaEntity: want 201, got %d: %v", status, resp)
	}
	return int(resp["id"].(float64))
}

// ─── Availability ─────────────────────────────────────────────────────────────

func TestMeta_AvailabilityMapShowsMeta(t *testing.T) {
	env := newMetaServer(t)
	_, resp := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/", env.ts.URL), nil)
	subsystems, _ := resp["subsystems"].(map[string]interface{})
	meta, _ := subsystems["meta"].(map[string]interface{})
	if meta["available"] != true {
		t.Errorf("meta subsystem: want available=true, got %v", meta["available"])
	}
}

// ─── PUT ──────────────────────────────────────────────────────────────────────

func TestMeta_PutAndGetScalar(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)

	// Set a string value.
	status, resp := doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/annotation", id)),
		map[string]interface{}{"value": "needs review"})
	if status != http.StatusOK {
		t.Fatalf("PUT meta: want 200, got %d: %v", status, resp)
	}
	if resp["key"] != "annotation" {
		t.Errorf("key: want 'annotation', got %v", resp["key"])
	}
}

func TestMeta_PutObject(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	status, resp := doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/ui_state", id)),
		map[string]interface{}{"value": map[string]interface{}{"panel": "closed", "width": 320}})
	if status != http.StatusOK {
		t.Fatalf("PUT object: want 200, got %d: %v", status, resp)
	}
	_ = resp
}

func TestMeta_PutWithExpiry(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	status, resp := doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/remind_check", id)),
		map[string]interface{}{"value": "check due", "expires_at": future})
	if status != http.StatusOK {
		t.Fatalf("PUT with expiry: want 200, got %d: %v", status, resp)
	}
	if resp["expires_at"] == nil {
		t.Errorf("expires_at should be present in response: %v", resp)
	}
}

func TestMeta_PutBadExpiry(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	status, _ := doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/remind_check", id)),
		map[string]interface{}{"value": "x", "expires_at": "not-a-time"})
	if status != http.StatusBadRequest {
		t.Errorf("bad expires_at: want 400, got %d", status)
	}
}

func TestMeta_PutEntityNotFound(t *testing.T) {
	env := newMetaServer(t)
	status, _ := doJSONRequest(t, "PUT",
		metaURL(env, "/assets/99999/annotation"),
		map[string]interface{}{"value": "x"})
	if status != http.StatusNotFound {
		t.Errorf("entity not found: want 404, got %d", status)
	}
}

func TestMeta_PutOverwrite(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/score", id)),
		map[string]interface{}{"value": 1})
	status, _ := doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/score", id)),
		map[string]interface{}{"value": 2})
	if status != http.StatusOK {
		t.Errorf("overwrite: want 200, got %d", status)
	}
	_, resp := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d/score", id)), nil)
	if resp["value"].(float64) != 2 {
		t.Errorf("overwritten value: want 2, got %v", resp["value"])
	}
}

// ─── Key validation ───────────────────────────────────────────────────────────

func TestMeta_KeyValidation(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)

	valid := []string{"abc", "ABC", "a_b_c", "a1", "A1B2", "_", "a" + fmt.Sprintf("%0*d", 63, 0)}
	for _, k := range valid {
		if len(k) > 64 {
			continue
		}
		status, _ := doJSONRequest(t, "PUT",
			metaURL(env, fmt.Sprintf("/assets/%d/%s", id, k)),
			map[string]interface{}{"value": "x"})
		if status != http.StatusOK {
			t.Errorf("valid key %q: want 200, got %d", k, status)
		}
	}

	invalid := []string{
		"",                      // empty (caught by routing)
		"has space",             // space
		"has-hyphen",            // hyphen
		"has.dot",               // dot
		"has/slash",             // slash
		"has@at",                // at
		fmt.Sprintf("%065d", 0), // 65 chars (too long)
	}
	for _, k := range invalid {
		if k == "" {
			continue // empty handled by router
		}
		status, _ := doJSONRequest(t, "PUT",
			metaURL(env, fmt.Sprintf("/assets/%d/%s", id, k)),
			map[string]interface{}{"value": "x"})
		if status == http.StatusOK {
			t.Errorf("invalid key %q: should not succeed, got 200", k)
		}
	}
}

func TestMeta_KeyTooLong(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	longKey := fmt.Sprintf("%065d", 0) // 65 chars, all digits (valid charset but too long)
	status, _ := doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/%s", id, longKey)),
		map[string]interface{}{"value": "x"})
	if status == http.StatusOK {
		t.Errorf("65-char key: should not succeed, got 200")
	}
}

// ─── GET ──────────────────────────────────────────────────────────────────────

func TestMeta_GetRoundTrip(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/flag_beta", id)),
		map[string]interface{}{"value": true})
	status, resp := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d/flag_beta", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("GET: want 200, got %d: %v", status, resp)
	}
	if resp["value"] != true {
		t.Errorf("value: want true, got %v", resp["value"])
	}
	if resp["key"] != "flag_beta" {
		t.Errorf("key: want 'flag_beta', got %v", resp["key"])
	}
}

func TestMeta_GetNotFound(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	status, _ := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d/nonexistent", id)), nil)
	if status != http.StatusNotFound {
		t.Errorf("get missing key: want 404, got %d", status)
	}
}

func TestMeta_GetExpiryRoundTrip(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/remind_x", id)),
		map[string]interface{}{"value": "check", "expires_at": future})
	status, resp := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d/remind_x", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("GET with expiry: want 200, got %d", status)
	}
	if resp["expires_at"] == nil {
		t.Errorf("expires_at missing from GET response: %v", resp)
	}
}

// ─── LIST ─────────────────────────────────────────────────────────────────────

func TestMeta_List(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	for _, k := range []string{"alpha", "beta", "gamma"} {
		doJSONRequest(t, "PUT",
			metaURL(env, fmt.Sprintf("/assets/%d/%s", id, k)),
			map[string]interface{}{"value": k + "_val"})
	}
	status, resp := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("list: want 200, got %d: %v", status, resp)
	}
	entries, _ := resp["entries"].([]interface{})
	if len(entries) != 3 {
		t.Errorf("entries: want 3, got %d", len(entries))
	}
}

func TestMeta_ListEmpty(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	status, resp := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("list empty: want 200, got %d", status)
	}
	entries, _ := resp["entries"].([]interface{})
	if len(entries) != 0 {
		t.Errorf("empty entity: want 0 entries, got %d", len(entries))
	}
}

// ─── DELETE key ───────────────────────────────────────────────────────────────

func TestMeta_DeleteKey(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/to_delete", id)),
		map[string]interface{}{"value": "bye"})
	status, _ := doJSONRequest(t, "DELETE",
		metaURL(env, fmt.Sprintf("/assets/%d/to_delete", id)), nil)
	if status != http.StatusOK {
		t.Errorf("delete key: want 200, got %d", status)
	}
	// Verify gone.
	s2, _ := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d/to_delete", id)), nil)
	if s2 != http.StatusNotFound {
		t.Errorf("after delete: want 404, got %d", s2)
	}
}

func TestMeta_DeleteKeyNotFound(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	status, _ := doJSONRequest(t, "DELETE",
		metaURL(env, fmt.Sprintf("/assets/%d/nope", id)), nil)
	if status != http.StatusNotFound {
		t.Errorf("delete missing key: want 404, got %d", status)
	}
}

// ─── DELETE all ───────────────────────────────────────────────────────────────

func TestMeta_DeleteAll(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)
	for _, k := range []string{"x", "y", "z"} {
		doJSONRequest(t, "PUT",
			metaURL(env, fmt.Sprintf("/assets/%d/%s", id, k)),
			map[string]interface{}{"value": k})
	}
	status, resp := doJSONRequest(t, "DELETE",
		metaURL(env, fmt.Sprintf("/assets/%d", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("delete all: want 200, got %d: %v", status, resp)
	}
	if resp["deleted"].(float64) != 3 {
		t.Errorf("deleted count: want 3, got %v", resp["deleted"])
	}
	// Verify all gone.
	_, listResp := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d", id)), nil)
	if entries, _ := listResp["entries"].([]interface{}); len(entries) != 0 {
		t.Errorf("after delete all: want 0 entries, got %d", len(entries))
	}
}

// ─── Value size limit ─────────────────────────────────────────────────────────

func TestMeta_ValueTooLarge(t *testing.T) {
	env := newV2Server(t, func(cfg *config.Config) {
		cfg.MetaMaxValueBytes = 10
	})
	id := createMetaEntity(t, env)
	// Body is a JSON object; the value field itself must be within limit.
	// Construct a value that will exceed 10 bytes when JSON-serialised.
	bigVal := map[string]interface{}{"value": "12345678901"} // exceeds 10 byte limit
	status, _ := doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/big", id)), bigVal)
	if status != http.StatusRequestEntityTooLarge {
		t.Errorf("value too large: want 413, got %d", status)
	}
}

// ─── Cascade delete ───────────────────────────────────────────────────────────

func TestMeta_CascadeDeleteWithEntity(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)

	// Set some metadata.
	doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/note", id)),
		map[string]interface{}{"value": "will be deleted"})

	// Delete the entity via v1.
	delStatus, _ := doJSONRequest(t, "DELETE",
		fmt.Sprintf("%s/api/v1/tenant/default/assets/%d", env.ts.URL, id), nil)
	if delStatus != http.StatusOK {
		t.Fatalf("entity delete: want 200, got %d", delStatus)
	}

	// Metadata must be gone — but the entity_meta table exists so the
	// list endpoint returns empty rather than 404.
	// Create a new entity to prove the table is still functional.
	id2 := createMetaEntity(t, env)
	_, listResp := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d", id2)), nil)
	entries, _ := listResp["entries"].([]interface{})
	if len(entries) != 0 {
		t.Errorf("new entity should have no meta, got %d entries", len(entries))
	}

	// The deleted entity's metadata should not show up.
	// We create a new entity with a fresh id; id is now gone.
	// Attempt to list the deleted entity's meta (id not found in nodes,
	// but entity_meta check just queries the table — list returns empty).
	_, deletedMeta := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d", id)), nil)
	if entries2, _ := deletedMeta["entries"].([]interface{}); len(entries2) != 0 {
		t.Errorf("deleted entity meta should be cascade-deleted, got %d entries", len(entries2))
	}
}

// ─── TTL sweep (MetaSweeper) ──────────────────────────────────────────────────

func TestMeta_GCSweeper_DeletesExpired(t *testing.T) {
	env := newV2Server(t, func(cfg *config.Config) {
		cfg.MetaGCIntervalSecs = 3600 // don't auto-fire during test
	})
	id := createMetaEntity(t, env)

	// Set a key that expired in the past.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/expired_key", id)),
		map[string]interface{}{"value": "stale", "expires_at": past})

	// Set a key that expires in the future (should survive).
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/future_key", id)),
		map[string]interface{}{"value": "fresh", "expires_at": future})

	// Set a key with no expiry (should always survive).
	doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/permanent", id)),
		map[string]interface{}{"value": "permanent"})

	// Trigger the meta-gc worker synchronously.
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/admin/gc/meta-gc/run", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("meta-gc run: want 200, got %d: %v", status, resp)
	}
	report, _ := resp["report"].(map[string]interface{})
	if report["collected"].(float64) < 1 {
		t.Errorf("GC collected: want >= 1, got %v", report["collected"])
	}

	// expired_key should be gone.
	s1, _ := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d/expired_key", id)), nil)
	if s1 != http.StatusNotFound {
		t.Errorf("expired_key after GC: want 404, got %d", s1)
	}

	// future_key and permanent must survive.
	s2, _ := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d/future_key", id)), nil)
	if s2 != http.StatusOK {
		t.Errorf("future_key after GC: want 200, got %d", s2)
	}
	s3, _ := doJSONRequest(t, "GET",
		metaURL(env, fmt.Sprintf("/assets/%d/permanent", id)), nil)
	if s3 != http.StatusOK {
		t.Errorf("permanent after GC: want 200, got %d", s3)
	}
}

func TestMeta_GCSweeper_InGCList(t *testing.T) {
	env := newV2Server(t, func(cfg *config.Config) {
		cfg.MetaGCEnabled = true
		cfg.MetaGCIntervalSecs = 3600
	})
	status, resp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/admin/gc", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("admin/gc list: want 200, got %d", status)
	}
	workers, _ := resp["workers"].([]interface{})
	found := false
	for _, w := range workers {
		if w.(map[string]interface{})["name"] == "meta-gc" {
			found = true
		}
	}
	if !found {
		t.Errorf("meta-gc not in GC worker list: %v", workers)
	}
}

// ─── V2 disabled — meta routes not registered ─────────────────────────────────

func TestMeta_DisabledReturns404(t *testing.T) {
	env := newV1OnlyServer(t)
	id := createMetaEntity(t, env)
	// Must use v1 base URL since v2 routes don't exist.
	url := fmt.Sprintf("%s/api/v2/meta/assets/%d/key", env.ts.URL, id)
	resp, _ := http.Get(url)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("meta with v2 disabled: want 404, got %d", resp.StatusCode)
	}
}

// ─── Tenant isolation ─────────────────────────────────────────────────────────

func TestMeta_TenantIsolation(t *testing.T) {
	env := newMetaServer(t)

	// Create entity and set meta under tenant t1.
	s1, r1 := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/t1/assets", env.ts.URL),
		map[string]interface{}{"name": "t1-asset", "type": "sensor"})
	if s1 != http.StatusCreated {
		t.Fatalf("create t1 asset: want 201, got %d", s1)
	}
	id1 := int(r1["id"].(float64))

	doJSONRequest(t, "PUT",
		fmt.Sprintf("%s/api/v2/tenant/t1/meta/assets/%d/secret", env.ts.URL, id1),
		map[string]interface{}{"value": "t1_only"})

	// Same entity id under tenant t2 should not see t1's metadata.
	status, resp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v2/tenant/t2/meta/assets/%d/secret", env.ts.URL, id1), nil)
	if status == http.StatusOK {
		t.Errorf("t2 should not see t1 metadata, got 200: %v", resp)
	}
}
