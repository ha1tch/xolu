// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

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
	"github.com/ha1tch/xolu/pkg/validation"
	"github.com/rs/zerolog"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type pfEnv struct {
	ts     *httptest.Server
	tmpDir string
	t      *testing.T
}

func newPFEnv(t *testing.T, perFile bool) *pfEnv {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "olu.db")
	schemaDir := filepath.Join(tmpDir, "schema")

	// Schema directory for "product" entity
	if err := os.MkdirAll(filepath.Join(schemaDir, "product"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Host:                 "localhost",
		Port:                 0,
		StorageType:          "sqlite",
		DBPath:               dbPath,
		SchemaDir:            schemaDir,
		CacheType:            "memory",
		CacheTTL:             300,
		GraphEnabled:         false,
		FullTextEnabled:      false,
		MaxEntitySize:        1048576,
		PatchNullBehavior:    "store",
		MaxEmbedDepth:        5,
		RefEmbedDepth:        3,
		MaxQueryDepth:        5,
		TenantMode:           "path",
		TenantAutoRegister:   true,
		SQLitePerFileTenants: perFile,
		SQLiteBusyTimeout:    5000,
		SQLiteCacheSize:      2000,
	}

	store, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type:                 "sqlite",
		DBPath:               dbPath,
		FullTextEnabled:      false,
		GraphEnabled:         false,
		SQLitePerFileTenants: perFile,
		SQLiteBusyTimeout:    5000,
		SQLiteCacheSize:      2000,
	})
	if err != nil {
		t.Fatalf("NewStoreFromConfig: %v", err)
	}

	memCache := cache.NewMemoryCache(1000, time.Duration(cfg.CacheTTL)*time.Second)
	g := graph.NewFlatGraph()
	validator := validation.NewJSONSchemaValidator(filepath.Join(schemaDir, "_schemas"))
	logger := zerolog.New(io.Discard)

	srv := server.New(cfg, store, memCache, g, nil, validator, logger)
	ts := httptest.NewServer(srv.Handler())

	t.Cleanup(func() {
		ts.Close()
		store.Close()
	})

	return &pfEnv{ts: ts, tmpDir: tmpDir, t: t}
}

// api returns the full URL for an API path.
func (e *pfEnv) api(path string) string {
	return e.ts.URL + "/api/v1" + path
}

func (e *pfEnv) do(method, path string, body interface{}) (int, map[string]interface{}) {
	e.t.Helper()
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
	}

	req, err := http.NewRequest(method, e.api(path), bytes.NewReader(bodyBytes))
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &result)
	}
	return resp.StatusCode, result
}

// ---------------------------------------------------------------------------
// Test: per-file mode — two tenants each get their own database file
// ---------------------------------------------------------------------------

func TestPerFileTenant_IsolationViaHTTP(t *testing.T) {
	env := newPFEnv(t, true /* perFile */)

	// Create product in tenant "alpha"
	status, body := env.do("POST", "/tenant/alpha/product",
		map[string]interface{}{"name": "Widget-A", "price": 9.99})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("alpha POST: want 201/200, got %d — %v", status, body)
	}
	idAlpha, _ := body["id"].(float64)
	if idAlpha == 0 {
		t.Fatalf("alpha POST: no id in response: %v", body)
	}

	// Create product in tenant "beta"
	status, body = env.do("POST", "/tenant/beta/product",
		map[string]interface{}{"name": "Widget-B", "price": 19.99})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("beta POST: want 201/200, got %d — %v", status, body)
	}
	idBeta, _ := body["id"].(float64)
	if idBeta == 0 {
		t.Fatalf("beta POST: no id in response: %v", body)
	}

	// Both tenants should have id=1 (independent sequences in per-file mode)
	if idAlpha != 1 || idBeta != 1 {
		t.Errorf("per-file: expected both tenants to start at id=1; alpha=%v beta=%v", idAlpha, idBeta)
	}

	// Alpha can GET its own product
	status, body = env.do("GET",
		fmt.Sprintf("/tenant/alpha/product/%d", int(idAlpha)), nil)
	if status != http.StatusOK {
		t.Errorf("alpha GET own: want 200, got %d — %v", status, body)
	}
	if name, _ := body["name"].(string); name != "Widget-A" {
		t.Errorf("alpha GET: name = %q, want Widget-A", name)
	}

	// Beta can GET its own product
	status, body = env.do("GET",
		fmt.Sprintf("/tenant/beta/product/%d", int(idBeta)), nil)
	if status != http.StatusOK {
		t.Errorf("beta GET own: want 200, got %d — %v", status, body)
	}
	if name, _ := body["name"].(string); name != "Widget-B" {
		t.Errorf("beta GET: name = %q, want Widget-B", name)
	}

	// Verify per-file layout: each tenant has its own file under sql/
	alphaFile := filepath.Join(env.tmpDir, "sql", "t0001", "olu.db")
	betaFile := filepath.Join(env.tmpDir, "sql", "t0002", "olu.db")
	if _, err := os.Stat(alphaFile); os.IsNotExist(err) {
		t.Errorf("per-file layout: alpha db not found at %s", alphaFile)
	}
	if _, err := os.Stat(betaFile); os.IsNotExist(err) {
		t.Errorf("per-file layout: beta db not found at %s", betaFile)
	}

	// List: alpha sees only its own product
	status, listBody := env.do("GET", "/tenant/alpha/product", nil)
	if status != http.StatusOK {
		t.Fatalf("alpha LIST: want 200, got %d — %v", status, listBody)
	}
	if items, ok := listBody["data"].([]interface{}); ok {
		if len(items) != 1 {
			t.Errorf("alpha LIST: want 1 item, got %d", len(items))
		}
		if len(items) > 0 {
			item := items[0].(map[string]interface{})
			if item["name"] != "Widget-A" {
				t.Errorf("alpha LIST[0]: name = %v, want Widget-A", item["name"])
			}
		}
	}

	// Beta's list should not include alpha's product
	status, listBody = env.do("GET", "/tenant/beta/product", nil)
	if status != http.StatusOK {
		t.Fatalf("beta LIST: want 200, got %d — %v", status, listBody)
	}
	if items, ok := listBody["data"].([]interface{}); ok {
		if len(items) != 1 {
			t.Errorf("beta LIST: want 1 item, got %d", len(items))
		}
	}
}

// ---------------------------------------------------------------------------
// Test: shared mode (default) — existing behaviour is unbroken
// ---------------------------------------------------------------------------

func TestSharedTenant_DefaultBehaviourUnbroken(t *testing.T) {
	env := newPFEnv(t, false /* shared, default */)

	// Create product in tenant "x"
	status, body := env.do("POST", "/tenant/x/product",
		map[string]interface{}{"name": "Shared-X", "sku": "SX1"})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("x POST: want 201/200, got %d — %v", status, body)
	}
	idX, _ := body["id"].(float64)
	if idX == 0 {
		t.Fatalf("x POST: no id in response: %v", body)
	}

	// Create product in tenant "y"
	status, body = env.do("POST", "/tenant/y/product",
		map[string]interface{}{"name": "Shared-Y", "sku": "SY1"})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("y POST: want 201/200, got %d — %v", status, body)
	}

	// x can get its product
	status, body = env.do("GET",
		fmt.Sprintf("/tenant/x/product/%d", int(idX)), nil)
	if status != http.StatusOK {
		t.Errorf("x GET own: want 200, got %d — %v", status, body)
	}
	if name, _ := body["name"].(string); name != "Shared-X" {
		t.Errorf("x GET: name = %q, want Shared-X", name)
	}

	// Create a second product under x so it gets id=2 (y's sequence is at 1).
	status, body = env.do("POST", "/tenant/x/product",
		map[string]interface{}{"name": "Shared-X2"})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("x POST second: want 201/200, got %d — %v", status, body)
	}
	idX2, _ := body["id"].(float64)

	// y cannot see x's second product (id=2 does not exist in y's tenant scope).
	// In shared mode, tenant_id column enforces isolation.
	status, _ = env.do("GET",
		fmt.Sprintf("/tenant/y/product/%d", int(idX2)), nil)
	if status != http.StatusNotFound {
		t.Errorf("y GET x's second product (id=%d): want 404 (tenant isolation), got %d", int(idX2), status)
	}

	// Verify y's list only contains its own product (1 item, not 2).
	status, yList := env.do("GET", "/tenant/y/product", nil)
	if status != http.StatusOK {
		t.Fatalf("y LIST: want 200, got %d", status)
	}
	if items, ok := yList["data"].([]interface{}); ok {
		if len(items) != 1 {
			t.Errorf("y LIST: want 1 item (its own), got %d", len(items))
		}
	}

	// Verify single shared db file exists, no sql/ subdirectory
	sharedDB := filepath.Join(env.tmpDir, "olu.db")
	if _, err := os.Stat(sharedDB); os.IsNotExist(err) {
		t.Errorf("shared mode: expected base db at %s", sharedDB)
	}
	sqlDir := filepath.Join(env.tmpDir, "sql")
	if _, err := os.Stat(sqlDir); !os.IsNotExist(err) {
		t.Errorf("shared mode: unexpected sql/ directory at %s", sqlDir)
	}
}

// ---------------------------------------------------------------------------
// Test 5: DELETE, UPDATE, PATCH at HTTP level — per-file mode
// ---------------------------------------------------------------------------

// TestPerFileTenant_MutationsAndCrossTenantIsolation exercises the full
// mutation surface (UPDATE, PATCH, DELETE) in per-file mode via HTTP, and
// verifies that deleting from one tenant does not affect another tenant's
// data at the same entity id.
func TestPerFileTenant_MutationsAndCrossTenantIsolation(t *testing.T) {
	env := newPFEnv(t, true /* perFile */)

	// --- Setup: create one product in each of two tenants ---
	status, body := env.do("POST", "/tenant/mu1/product",
		map[string]interface{}{"name": "Original", "price": 10.0})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("mu1 POST: want 201/200, got %d — %v", status, body)
	}
	id1, _ := body["id"].(float64)
	if id1 == 0 {
		t.Fatalf("mu1 POST: no id: %v", body)
	}

	status, body = env.do("POST", "/tenant/mu2/product",
		map[string]interface{}{"name": "Parallel", "price": 20.0})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("mu2 POST: want 201/200, got %d — %v", status, body)
	}
	id2, _ := body["id"].(float64)
	if id2 == 0 {
		t.Fatalf("mu2 POST: no id: %v", body)
	}

	// --- UPDATE mu1's product ---
	status, body = env.do("PUT",
		fmt.Sprintf("/tenant/mu1/product/%d", int(id1)),
		map[string]interface{}{"id": int(id1), "name": "Updated", "price": 15.0})
	if status != http.StatusOK {
		t.Fatalf("mu1 PUT: want 200, got %d — %v", status, body)
	}

	// Verify update applied in mu1
	status, body = env.do("GET", fmt.Sprintf("/tenant/mu1/product/%d", int(id1)), nil)
	if status != http.StatusOK {
		t.Fatalf("mu1 GET after PUT: want 200, got %d", status)
	}
	if name, _ := body["name"].(string); name != "Updated" {
		t.Errorf("mu1 GET after PUT: name = %q, want Updated", name)
	}

	// mu2's product is unaffected (same id, different db file)
	status, body = env.do("GET", fmt.Sprintf("/tenant/mu2/product/%d", int(id2)), nil)
	if status != http.StatusOK {
		t.Fatalf("mu2 GET after mu1 PUT: want 200, got %d", status)
	}
	if name, _ := body["name"].(string); name != "Parallel" {
		t.Errorf("mu2 GET after mu1 PUT: name = %q, want Parallel (should be unchanged)", name)
	}

	// --- PATCH mu1's product ---
	status, body = env.do("PATCH",
		fmt.Sprintf("/tenant/mu1/product/%d", int(id1)),
		map[string]interface{}{"name": "Patched"})
	if status != http.StatusOK {
		t.Fatalf("mu1 PATCH: want 200, got %d — %v", status, body)
	}

	status, body = env.do("GET", fmt.Sprintf("/tenant/mu1/product/%d", int(id1)), nil)
	if status != http.StatusOK {
		t.Fatalf("mu1 GET after PATCH: want 200, got %d", status)
	}
	if name, _ := body["name"].(string); name != "Patched" {
		t.Errorf("mu1 GET after PATCH: name = %q, want Patched", name)
	}

	// --- DELETE mu1's product ---
	status, _ = env.do("DELETE", fmt.Sprintf("/tenant/mu1/product/%d", int(id1)), nil)
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("mu1 DELETE: want 200/204, got %d", status)
	}

	// mu1: entity gone
	status, _ = env.do("GET", fmt.Sprintf("/tenant/mu1/product/%d", int(id1)), nil)
	if status != http.StatusNotFound {
		t.Errorf("mu1 GET after DELETE: want 404, got %d", status)
	}

	// mu2: entity still present — the key cross-tenant isolation assertion.
	// deleteInner's FTS delete in per-file mode must only touch its own file.
	status, body = env.do("GET", fmt.Sprintf("/tenant/mu2/product/%d", int(id2)), nil)
	if status != http.StatusOK {
		t.Errorf("mu2 GET after mu1 DELETE: want 200 (mu1 delete must not affect mu2), got %d", status)
	}
	if name, _ := body["name"].(string); name != "Parallel" {
		t.Errorf("mu2 GET after mu1 DELETE: name = %q, want Parallel", name)
	}
}
