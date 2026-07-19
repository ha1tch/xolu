// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_scaffold_test.go
//
// Tests for S1: feature flag, X-API-Stability middleware, GET /api/v2
// availability endpoint, and 404 behaviour when v2 is disabled.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// newV2Server builds a test server with APIV2Enabled=true.
func newV2Server(t *testing.T, opts ...func(*config.Config)) *stdTestServer {
	t.Helper()
	tmpDir := t.TempDir()

	// Create entity schema directories so OQL queries work.
	for _, entity := range []string{
		"assets", "events", "asset_types", "sensors", "sensor_bindings",
		"users", "locations", "audit_log",
	} {
		_ = os.MkdirAll(tmpDir+"/test_schema/"+entity, 0755)
	}

	cfg := &config.Config{
		Host: "localhost", Port: 0,
		StorageType:          "sqlite",
		BaseDir:              tmpDir,
		Schema:               "test_schema",
		SchemaDir:            tmpDir + "/test_schema",
		CacheType:            "memory",
		CacheTTL:             300,
		MaxEntitySize:        1048576,
		GraphEnabled:         true,
		TenantMode:           "path",
		TenantAutoRegister:   true,
		APIV2Enabled:         true,
		MetaMaxValueBytes:    65536,
		MetaGCEnabled:        true,
		MetaGCIntervalSecs:   3600,
		MaxQueryDepth:        10,
		AsyncJobRetentionTTL: 86400,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	sts := newTestServerFromConfig(t, cfg)
	t.Cleanup(sts.cleanup)
	return sts
}

// newV1OnlyServer builds a test server with APIV2Enabled=false (the default).
func newV1OnlyServer(t *testing.T) *stdTestServer {
	t.Helper()
	tmpDir := t.TempDir()
	for _, entity := range []string{
		"assets", "events", "asset_types", "sensors", "sensor_bindings",
		"users", "locations", "audit_log",
	} {
		_ = os.MkdirAll(tmpDir+"/test_schema/"+entity, 0755)
	}
	cfg := &config.Config{
		Host: "localhost", Port: 0,
		StorageType:          "sqlite",
		BaseDir:              tmpDir,
		Schema:               "test_schema",
		SchemaDir:            tmpDir + "/test_schema",
		CacheType:            "memory",
		CacheTTL:             300,
		MaxEntitySize:        1048576,
		GraphEnabled:         true,
		TenantMode:           "path",
		TenantAutoRegister:   true,
		APIV2Enabled:         false,
		MaxQueryDepth:        10,
		AsyncJobRetentionTTL: 86400,
	}
	sts := newTestServerFromConfig(t, cfg)
	t.Cleanup(sts.cleanup)
	return sts
}

// v2Get makes a GET request to a v2 path.
func v2Get(t *testing.T, sts *stdTestServer, path string) *http.Response {
	t.Helper()
	url := fmt.Sprintf("%s/api/v2%s", sts.ts.URL, path)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /api/v2%s: %v", path, err)
	}
	return resp
}

// ─── Flag behaviour ───────────────────────────────────────────────────────────

func TestV2Scaffold_DisabledReturns404(t *testing.T) {
	env := newV1OnlyServer(t)
	for _, path := range []string{"/", "/meta/assets/1", "/fsm/def", "/gen/uuid_v4"} {
		resp := v2Get(t, env, path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("v2 disabled: GET /api/v2%s want 404, got %d", path, resp.StatusCode)
		}
	}
}

func TestV2Scaffold_EnabledRootExists(t *testing.T) {
	env := newV2Server(t)
	resp := v2Get(t, env, "/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("v2 enabled: GET /api/v2/ want 200, got %d", resp.StatusCode)
	}
}

// ─── X-API-Stability middleware ───────────────────────────────────────────────

func TestV2Scaffold_StabilityHeader(t *testing.T) {
	env := newV2Server(t)
	resp := v2Get(t, env, "/")
	resp.Body.Close()
	if got := resp.Header.Get("X-API-Stability"); got != "experimental" {
		t.Errorf("X-API-Stability: want 'experimental', got %q", got)
	}
}

func TestV2Scaffold_StabilityHeaderOnUnimplementedRoute(t *testing.T) {
	env := newV2Server(t)
	// A route that no handler will ever claim: the v2 middleware runs before
	// the router's 404, so the stability header must be present even on a
	// not-found response. This deliberately does NOT target a real endpoint —
	// earlier versions anchored to /fsm/def and then /walk, both of which
	// later became implemented; a permanently-absent path is the stable
	// vehicle for asserting the header survives on error responses.
	resp := v2Get(t, env, "/this-route-will-never-exist")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("nonexistent v2 route: want 404, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-API-Stability"); got != "experimental" {
		t.Errorf("X-API-Stability on error response: want 'experimental', got %q", got)
	}
}

func TestV2Scaffold_DocsHeader(t *testing.T) {
	env := newV2Server(t)
	resp := v2Get(t, env, "/")
	resp.Body.Close()
	if got := resp.Header.Get("X-API-Docs"); got == "" {
		t.Error("X-API-Docs header missing from v2 response")
	}
}

func TestV2Scaffold_V1HasNoStabilityHeader(t *testing.T) {
	env := newV2Server(t)
	url := fmt.Sprintf("%s/version", env.ts.URL)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /version: %v", err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("X-API-Stability"); got != "" {
		t.Errorf("v1/non-v2 route must not carry X-API-Stability, got %q", got)
	}
}

// ─── GET /api/v2 availability endpoint ───────────────────────────────────────

func TestV2Scaffold_AvailabilityShape(t *testing.T) {
	env := newV2Server(t)
	resp := v2Get(t, env, "/")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("availability: want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("availability: invalid JSON: %v\nbody: %s", err, body)
	}
	for _, field := range []string{"version", "enabled", "as_of", "warning", "subsystems"} {
		if payload[field] == nil {
			t.Errorf("availability response missing field %q", field)
		}
	}
	if payload["version"] != "experimental" {
		t.Errorf("version: want 'experimental', got %v", payload["version"])
	}
	if payload["enabled"] != true {
		t.Errorf("enabled: want true, got %v", payload["enabled"])
	}
}

func TestV2Scaffold_AvailabilitySubsystems(t *testing.T) {
	env := newV2Server(t)
	resp := v2Get(t, env, "/")
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var payload map[string]interface{}
	_ = json.Unmarshal(body, &payload)

	subsystems, ok := payload["subsystems"].(map[string]interface{})
	if !ok {
		t.Fatalf("subsystems: want object, got %T", payload["subsystems"])
	}
	for _, name := range []string{"event"} {
		sub, ok := subsystems[name].(map[string]interface{})
		if !ok {
			t.Errorf("subsystem %q missing or not an object", name)
			continue
		}
		if sub["available"] == nil {
			t.Errorf("subsystem %q missing 'available' field", name)
		}
		if sub["available"] == true {
			t.Errorf("subsystem %q: available should be false at current stage, got true", name)
		}
	}
	// meta (S3), gen (S4), seq (S5), fsm (S7/S8) are available.
	for _, name := range []string{"meta", "gen", "seq", "fsm"} {
		sub, ok := subsystems[name].(map[string]interface{})
		if !ok {
			t.Errorf("subsystem %q missing from availability map", name)
			continue
		}
		if sub["available"] != true {
			t.Errorf("subsystem %q: want available=true (shipped), got %v", name, sub["available"])
		}
	}
}

func TestV2Scaffold_AvailabilityWarning(t *testing.T) {
	env := newV2Server(t)
	resp := v2Get(t, env, "/")
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var payload map[string]interface{}
	_ = json.Unmarshal(body, &payload)

	if warning, _ := payload["warning"].(string); warning == "" {
		t.Error("availability response must include a non-empty warning field")
	}
}

// ─── CommitRequest.FsmWalk disabled guard ─────────────────────────────────────

func TestV2Scaffold_CommitFsmWalkRejectedWhenDisabled(t *testing.T) {
	env := newV1OnlyServer(t)

	// Create an entity first.
	_, idResp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "test", "type": "sensor"})
	id := int(idResp["id"].(float64))

	// Commit with fsm_walk set while v2 is disabled.
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/commit", env.ts.URL),
		map[string]interface{}{
			"update": map[string]interface{}{
				"entity": "assets",
				"id":     id,
				"data":   map[string]interface{}{"name": "updated"},
			},
			"append": []interface{}{},
			"fsm_walk": map[string]interface{}{
				"machine": 1,
				"input":   "ready",
			},
		})
	if status == http.StatusOK {
		t.Errorf("commit with fsm_walk when v2 disabled: should not succeed, got 200: %v", resp)
	}
	if status >= http.StatusInternalServerError {
		t.Errorf("commit with fsm_walk when v2 disabled: should not 5xx, got %d: %v", status, resp)
	}
}

func TestV2Scaffold_CommitWithoutFsmWalkUnchanged(t *testing.T) {
	env := newV1OnlyServer(t)

	_, idResp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "test", "type": "sensor"})
	id := int(idResp["id"].(float64))

	// Normal commit with no fsm_walk — must behave exactly as before.
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/commit", env.ts.URL),
		map[string]interface{}{
			"update": map[string]interface{}{
				"entity": "assets",
				"id":     id,
				"data":   map[string]interface{}{"name": "updated"},
			},
			"append": []interface{}{
				map[string]interface{}{
					"entity": "audit_log",
					"data":   map[string]interface{}{"action": "update"},
				},
			},
		})
	if status != http.StatusOK {
		t.Errorf("normal commit without fsm_walk: want 200, got %d: %v", status, resp)
	}
}
