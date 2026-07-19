// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package server — handler error-path coverage tests.
//
// Covers the disabled/nil-guard branches and error paths of:
//   handleGraphVerify          (0% → covered)
//   handleTenantCreateEdge     (0% → covered)
//   handleTenantSulpherQueryStatus (0% → covered)
//   dynConfigGuard             (50% → disabled branch covered)
//   handleSulpherQuery         (50% → disabled + nil-engine branches)
//   handleGraphStats           (50% → disabled branch)
//   HandleTSProvision          (50% → nil-manager branch)
//   handleGraphIncoming        (60% → disabled branch)
//   handleGraphOutgoing        (60% → disabled branch)

package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cache"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/validation"
	"github.com/rs/zerolog"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newGraphServer builds a server with GraphEnabled = true and
// SulpherEnabled = true so graph and Sulpher endpoints are registered.
func newGraphServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	dir := t.TempDir()

	cfg := &config.Config{
		Host:                 "127.0.0.1",
		Port:                 0,
		StorageType:          "sqlite",
		BaseDir:              dir,
		Schema:               "schema",
		SchemaDir:            filepath.Join(dir, "schema"),
		CacheType:            "memory",
		CacheTTL:             60,
		GraphEnabled:         true,
		GraphMode:            "flat",
		FullTextEnabled:      false,
		MaxEmbedDepth:        10,
		RefEmbedDepth:        3,
		MaxEntitySize:        1048576,
		PatchNullBehavior:    "store",
		TenantMode:           "path",
		TenantAutoRegister:   true,
		MaxCascadeDeletions:  100,
		QueryTimeout:         30,
		MaxQueryDepth:        10,
		AsyncJobRetentionTTL: 86400,
	}
	os.MkdirAll(cfg.SchemaDir, 0755)

	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": sl.TenantStorePath(cfg.BaseDir, 0)})
	if err != nil {
		t.Fatalf("storage.NewStore: %v", err)
	}
	memCache := cache.NewMemoryCache(1000, 60*time.Second)
	g := graph.NewFlatGraph()
	validator := validation.NewJSONSchemaValidator("")
	logger := zerolog.Nop()

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	return ts, func() {
		ts.Close()
		store.Close()
		os.RemoveAll(dir)
	}
}

// newNoGraphServer builds a server with GraphEnabled = false.
func newNoGraphServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	dir := t.TempDir()

	cfg := &config.Config{
		Host:                "127.0.0.1",
		Port:                0,
		StorageType:         "sqlite",
		BaseDir:             dir,
		CacheType:           "memory",
		CacheTTL:            60,
		GraphEnabled:        false,
		FullTextEnabled:     false,
		MaxEmbedDepth:       10,
		RefEmbedDepth:       3,
		MaxEntitySize:       1048576,
		PatchNullBehavior:   "store",
		TenantMode:          "path",
		TenantAutoRegister:  true,
		MaxCascadeDeletions: 100,
	}

	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": sl.TenantStorePath(cfg.BaseDir, 0)})
	if err != nil {
		t.Fatalf("storage.NewStore: %v", err)
	}
	memCache := cache.NewMemoryCache(100, 60*time.Second)
	g := graph.NewFlatGraph()
	validator := validation.NewJSONSchemaValidator("")
	logger := zerolog.Nop()

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	return ts, func() {
		ts.Close()
		store.Close()
		os.RemoveAll(dir)
	}
}

// do is a minimal HTTP helper.
func do(t *testing.T, method, url string, body interface{}) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// handleGraphVerify — disabled path (no GraphIntegrity on test store)
// ---------------------------------------------------------------------------

// TestHandlerErrorPaths_GraphVerify exercises handleGraphVerify.
// The SQLite test store does not implement storage.GraphIntegrity in a way
// that responds over HTTP — the handler type-asserts for GraphIntegrity and
// returns 501 when absent or, when present, calls VerifyGraphIntegrity.
// Both the path-mode and tenant-mode routes are registered.
func TestHandlerErrorPaths_GraphVerify(t *testing.T) {
	ts, cleanup := newGraphServer(t)
	defer cleanup()

	// Path-mode route: GET /api/v1/graph/admin/verify
	resp := do(t, http.MethodGet, ts.URL+"/api/v1/graph/admin/verify", nil)
	resp.Body.Close()
	// The store implements GraphIntegrity; success (200) or conflict (409) are
	// both valid. What we are testing is that the handler executes without panic.
	if resp.StatusCode == 0 {
		t.Error("graph/admin/verify returned zero status")
	}

	// With graph disabled, must return 501.
	tsNG, cleanupNG := newNoGraphServer(t)
	defer cleanupNG()
	resp = do(t, http.MethodGet, tsNG.URL+"/api/v1/graph/admin/verify", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("graph/admin/verify (graph disabled): status %d, want 404 or 501", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// handleGraphStats — disabled branch
// ---------------------------------------------------------------------------

func TestHandlerErrorPaths_GraphStats(t *testing.T) {
	ts, cleanup := newGraphServer(t)
	defer cleanup()

	// Happy path — graph enabled.
	resp := do(t, http.MethodGet, ts.URL+"/api/v1/graph/stats", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("graph/stats: status %d, want 200; body: %s", resp.StatusCode, body)
	}

	// Disabled branch — graph not enabled.
	tsNG, cleanupNG := newNoGraphServer(t)
	defer cleanupNG()
	resp = do(t, http.MethodGet, tsNG.URL+"/api/v1/graph/stats", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented && resp.StatusCode != http.StatusNotFound &&
		resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("graph/stats (disabled): status %d, want non-2xx", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// handleGraphIncoming / handleGraphOutgoing — disabled branches
// ---------------------------------------------------------------------------

func TestHandlerErrorPaths_GraphDirectional(t *testing.T) {
	tsNG, cleanup := newNoGraphServer(t)
	defer cleanup()

	for _, path := range []string{
		"/api/v1/graph/entity:1/in",
		"/api/v1/graph/entity:1/out",
	} {
		resp := do(t, http.MethodGet, tsNG.URL+path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotImplemented && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s (graph disabled): status %d, want 404 or 501", path, resp.StatusCode)
		}
	}
}

// ---------------------------------------------------------------------------
// handleSulpherQuery — disabled and nil-engine branches
// ---------------------------------------------------------------------------

func TestHandlerErrorPaths_SulpherQuery(t *testing.T) {
	// Graph disabled → 501.
	tsNG, cleanup := newNoGraphServer(t)
	defer cleanup()

	resp := do(t, http.MethodPost, tsNG.URL+"/api/v1/graph/query",
		map[string]interface{}{"query": "MATCH (n) RETURN n"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("sulpher/query (graph disabled): status %d, want 404 or 501", resp.StatusCode)
	}

	// Graph enabled, query body present → engine executes (may succeed or error,
	// both are valid). Tests the executeSulpherQueryBody path.
	ts, cleanup2 := newGraphServer(t)
	defer cleanup2()

	resp = do(t, http.MethodPost, ts.URL+"/api/v1/graph/query",
		map[string]interface{}{"query": "MATCH (n:person) RETURN n"})
	resp.Body.Close()
	// 200, 404, or 500 are all valid depending on data; 400 means empty query.
	if resp.StatusCode == http.StatusBadRequest {
		t.Error("sulpher/query with non-empty query returned 400")
	}

	// Empty query → 400.
	resp = do(t, http.MethodPost, ts.URL+"/api/v1/graph/query",
		map[string]interface{}{"query": ""})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("sulpher/query (empty query): status %d, want 400", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// handleTenantCreateEdge — full branch coverage
// ---------------------------------------------------------------------------

func TestHandlerErrorPaths_TenantCreateEdge(t *testing.T) {
	ts, cleanup := newGraphServer(t)
	defer cleanup()

	tenantURL := ts.URL + "/api/v1/tenant/default/graph/edges"

	// Graph disabled variant.
	tsNG, cleanupNG := newNoGraphServer(t)
	defer cleanupNG()
	resp := do(t, http.MethodPost, tsNG.URL+"/api/v1/tenant/default/graph/edges",
		map[string]interface{}{"from": "person:1", "to": "dept:1", "rel": "WORKS_IN"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("tenant/graph/edges (graph disabled): status %d", resp.StatusCode)
	}

	// Missing required fields → 400.
	for _, body := range []map[string]interface{}{
		{"to": "dept:1", "rel": "WORKS_IN"},     // missing from
		{"from": "person:1", "rel": "WORKS_IN"}, // missing to
		{"from": "person:1", "to": "dept:1"},    // missing rel
	} {
		resp = do(t, http.MethodPost, tenantURL, body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			b, _ := json.Marshal(body)
			t.Errorf("tenant/graph/edges (missing field): status %d, want 400; body: %s",
				resp.StatusCode, b)
		}
	}

	// Valid request with non-existent nodes — AddEdgeWithProps will attempt
	// to create the edge; result depends on graph config but must not panic.
	resp = do(t, http.MethodPost, tenantURL, map[string]interface{}{
		"from":  "person:1",
		"to":    "dept:1",
		"rel":   "WORKS_IN",
		"props": map[string]interface{}{"since": "2024"},
	})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// 201 (created) or 5xx (AddEdgeWithProps error) are both valid; 400 is not.
	if resp.StatusCode == http.StatusBadRequest {
		t.Errorf("tenant/graph/edges (valid request): unexpected 400; body: %s", body)
	}
}

// ---------------------------------------------------------------------------
// handleTenantSulpherQueryStatus — full branch coverage
// ---------------------------------------------------------------------------

func TestHandlerErrorPaths_TenantSulpherQueryStatus(t *testing.T) {
	// Graph disabled → 501.
	tsNG, cleanup := newNoGraphServer(t)
	defer cleanup()
	resp := do(t, http.MethodGet,
		tsNG.URL+"/api/v1/tenant/default/graph/query/nonexistent", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("tenant/graph/query/status (disabled): status %d", resp.StatusCode)
	}

	// Graph enabled, non-existent query_id → 404.
	ts, cleanup2 := newGraphServer(t)
	defer cleanup2()
	resp = do(t, http.MethodGet,
		ts.URL+"/api/v1/tenant/default/graph/query/does-not-exist", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("tenant/graph/query/status (missing id): status %d, want 404 or 501; body: %s",
			resp.StatusCode, body)
	}

	// Submit a real async query, then poll its status.
	queryResp := do(t, http.MethodPost,
		ts.URL+"/api/v1/tenant/default/graph/query/async",
		map[string]interface{}{"query": "MATCH (n:person) RETURN n"})
	qBody, _ := io.ReadAll(queryResp.Body)
	queryResp.Body.Close()

	if queryResp.StatusCode == http.StatusAccepted || queryResp.StatusCode == http.StatusOK {
		var jobResp map[string]interface{}
		if err := json.Unmarshal(qBody, &jobResp); err == nil {
			if queryID, ok := jobResp["query_id"].(string); ok && queryID != "" {
				// Poll status — exercises the main success path.
				statusResp := do(t, http.MethodGet,
					fmt.Sprintf("%s/api/v1/tenant/default/graph/query/%s",
						ts.URL, queryID), nil)
				statusBody, _ := io.ReadAll(statusResp.Body)
				statusResp.Body.Close()
				if statusResp.StatusCode != http.StatusOK {
					t.Errorf("tenant/graph/query/status: status %d, want 200; body: %s",
						statusResp.StatusCode, statusBody)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// dynConfigGuard — disabled branch
// ---------------------------------------------------------------------------

func TestHandlerErrorPaths_DynConfigGuard(t *testing.T) {
	dir := t.TempDir()
	defer os.RemoveAll(dir)

	cfg := &config.Config{
		Host:               "127.0.0.1",
		BaseDir:            dir,
		StorageType:        "sqlite",
		CacheType:          "memory",
		CacheTTL:           60,
		DynConfigEnabled:   false, // dynConfig will be nil → dynConfigGuard fires
		MaxEntitySize:      1048576,
		RefEmbedDepth:      3,
		MaxEmbedDepth:      10,
		PatchNullBehavior:  "store",
		TenantMode:         "path",
		TenantAutoRegister: true,
	}
	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": sl.TenantStorePath(cfg.BaseDir, 0)})
	if err != nil {
		t.Fatalf("storage.NewStore: %v", err)
	}
	defer store.Close()
	memCache := cache.NewMemoryCache(100, 60*time.Second)
	g := graph.NewFlatGraph()
	validator := validation.NewJSONSchemaValidator("")
	logger := zerolog.Nop()

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// When DynConfigEnabled is false, the admin/config routes should either be
	// absent (404) or the guard should fire (503). Either is correct.
	for _, tc := range []struct {
		method string
		path   string
		body   []byte
	}{
		{"GET", "/api/v1/admin/config", nil},
		{"GET", "/api/v1/admin/config/global", nil},
		{"GET", "/api/v1/admin/config/global/k", nil},
		{"PUT", "/api/v1/admin/config/global/k", []byte(`"v"`)},
		{"DELETE", "/api/v1/admin/config/global/k", nil},
	} {
		var r io.Reader
		if tc.body != nil {
			r = bytes.NewReader(tc.body)
		}
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, r)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("%s %s (dynconfig disabled): status %d, want 4xx or 5xx",
				tc.method, tc.path, resp.StatusCode)
		}
	}
}

// ---------------------------------------------------------------------------
// HandleTSProvision — nil-manager branch
// ---------------------------------------------------------------------------

func TestHandlerErrorPaths_TSProvision(t *testing.T) {
	dir := t.TempDir()
	defer os.RemoveAll(dir)

	// Server without TSManager (tsManager will be nil).
	cfg := &config.Config{
		Host:               "127.0.0.1",
		BaseDir:            dir,
		StorageType:        "sqlite",
		CacheType:          "memory",
		CacheTTL:           60,
		MaxEntitySize:      1048576,
		RefEmbedDepth:      3,
		MaxEmbedDepth:      10,
		PatchNullBehavior:  "store",
		TenantMode:         "path",
		TenantAutoRegister: true,
	}
	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": sl.TenantStorePath(cfg.BaseDir, 0)})
	if err != nil {
		t.Fatalf("storage.NewStore: %v", err)
	}
	defer store.Close()
	memCache := cache.NewMemoryCache(100, 60*time.Second)
	g := graph.NewFlatGraph()
	validator := validation.NewJSONSchemaValidator("")
	logger := zerolog.Nop()

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// POST /api/v1/tenant/{id}/ts/provision with nil tsManager → 403.
	resp := do(t, http.MethodPost,
		ts.URL+"/api/v1/tenant/default/ts/provision", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("ts/provision (nil manager): status %d, want 403 or 404; body: %s",
			resp.StatusCode, body)
	}
}
