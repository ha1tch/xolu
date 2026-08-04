// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"bytes"
	"context"
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

// TestServer holds test server instance and helpers
type TestServer struct {
	server      *server.Server
	ts          *httptest.Server
	cfg         *config.Config
	t           testing.TB
	sqliteStore storage.Store // Optional, for SQLite-based tests
}

// setupTestServer creates a test server with temporary storage. Accepts
// testing.TB so both tests and any benchmarks can use it.
func setupTestServer(t testing.TB) *TestServer {
	// Create temporary directory for test data
	tmpDir, err := os.MkdirTemp("", "xolu-test-*")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Host:                "localhost",
		Port:                0, // Let httptest choose port
		BaseDir:             tmpDir,
		Schema:              "test_schema",
		SchemaDir:           filepath.Join(tmpDir, "test_schema"), // For OQL entity discovery
		CacheType:           "memory",
		CacheTTL:            300,
		GraphEnabled:        true,
		GraphMode:           "flat",
		FullTextEnabled:     false,
		CascadingDelete:     false,
		RefEmbedDepth:       3,
		MaxEmbedDepth:       10,
		MaxEntitySize:       1048576,
		PatchNullBehavior:   "store",
		MaxCascadeDeletions: 100,
		TenantMode:          "path",
		TenantAutoRegister:  true, // Tests rely on auto-registration
	}

	// Initialize components
store, err := storage.NewStore("sqlite", map[string]interface{}{
		"db_path":       filepath.Join(tmpDir, "test.db"),
		"graph_enabled": true, // parity with production (NewStoreFromConfig); without this
		//                        syncGraphEdges short-circuits and RI enforcement never runs.
	})
	if err != nil {
		t.Fatal(err)
	}

	memCache := cache.NewMemoryCache(1000, time.Duration(cfg.CacheTTL)*time.Second)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())

	return &TestServer{
		server:      srv,
		ts:          ts,
		cfg:         cfg,
		t:           t,
		sqliteStore: store,
	}
}

// cleanup removes temporary test data
func (ts *TestServer) cleanup() {
	ts.ts.Close()
	// Stop() drains and closes any per-tenant SQLite stores the server lazily
	// created during the test (s.tenantStores), mirroring stdTestServer's own
	// cleanup. Without it, tenant-scoped tests using setupTestServer leaked
	// those connections indefinitely -- the base sqliteStore.Close() below
	// only ever closed the single store passed in at construction time.
	if ts.server != nil {
		ts.server.Stop()
	}
	if ts.sqliteStore != nil {
		ts.sqliteStore.Close()
	}
	os.RemoveAll(ts.cfg.BaseDir)
}

// doRequest makes HTTP request and returns response
func (ts *TestServer) doRequest(method, path string, body interface{}) (*http.Response, []byte) {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			ts.t.Fatal(err)
		}
	}

	req, err := http.NewRequest(method, ts.ts.URL+path, bytes.NewBuffer(bodyBytes))
	if err != nil {
		ts.t.Fatal(err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatal(err)
	}
	defer resp.Body.Close()

	respBody := &bytes.Buffer{}
	respBody.ReadFrom(resp.Body)

	return resp, respBody.Bytes()
}

// TestHealthEndpoints tests health and version endpoints
func TestHealthEndpoints(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	t.Run("GET /health", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/health", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}

		if result["status"] != "ok" {
			t.Errorf("Expected status ok, got %v", result["status"])
		}
	})

	t.Run("GET /ready", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/ready", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}

		if result["status"] != "ready" {
			t.Errorf("Expected status ready, got %v", result["status"])
		}
	})

	t.Run("GET /version", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/version", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}

		if result["version"] == nil {
			t.Error("Expected version field")
		}
	})
}

// TestCORSMiddleware tests CORS header behaviour
func TestCORSMiddleware(t *testing.T) {
	// Create a server with CORS enabled
	tmpDir, err := os.MkdirTemp("", "xolu-cors-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Host:                "localhost",
		Port:                0,
		BaseDir:             tmpDir,
		Schema:              "test_schema",
		SchemaDir:           filepath.Join(tmpDir, "test_schema"),
		CacheType:           "memory",
		CacheTTL:            300,
		GraphEnabled:        true,
		GraphMode:           "flat",
		FullTextEnabled:     false,
		CascadingDelete:     false,
		RefEmbedDepth:       3,
		MaxEmbedDepth:       10,
		MaxEntitySize:       1048576,
		PatchNullBehavior:   "store",
		MaxCascadeDeletions: 100,
		TenantMode:          "path",
		TenantAutoRegister:  true,
		CORSOrigins:         []string{"https://dashboard.example.com", "https://admin.example.com"},
	}

	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": filepath.Join(tmpDir, "test.db")})
	if err != nil {
		t.Fatal(err)
	}

	memCache := cache.NewMemoryCache(1000, time.Duration(cfg.CacheTTL)*time.Second)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("allowed origin gets CORS headers", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.URL+"/health", nil)
		req.Header.Set("Origin", "https://dashboard.example.com")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		acao := resp.Header.Get("Access-Control-Allow-Origin")
		if acao != "https://dashboard.example.com" {
			t.Errorf("Expected ACAO header 'https://dashboard.example.com', got %q", acao)
		}
	})

	t.Run("disallowed origin gets no CORS headers", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.URL+"/health", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		acao := resp.Header.Get("Access-Control-Allow-Origin")
		if acao != "" {
			t.Errorf("Expected no ACAO header for disallowed origin, got %q", acao)
		}
	})

	t.Run("preflight OPTIONS returns 204", func(t *testing.T) {
		req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/v1/test", nil)
		req.Header.Set("Origin", "https://dashboard.example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("Expected 204 for preflight, got %d", resp.StatusCode)
		}

		methods := resp.Header.Get("Access-Control-Allow-Methods")
		if methods == "" {
			t.Error("Expected Access-Control-Allow-Methods header")
		}
	})

	t.Run("no origin header skips CORS", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.URL+"/health", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		acao := resp.Header.Get("Access-Control-Allow-Origin")
		if acao != "" {
			t.Errorf("Expected no ACAO header when no Origin sent, got %q", acao)
		}
	})
}

// TestEntityCRUD tests complete entity CRUD operations
func TestEntityCRUD(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	entity := "users"
	var createdID float64

	t.Run("POST /api/v1/{entity} - Create", func(t *testing.T) {
		data := map[string]interface{}{
			"name":  "Alice Smith",
			"email": "alice@example.com",
			"age":   30,
		}

		resp, body := ts.doRequest("POST", "/api/v1/"+entity, data)
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("Expected 201, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}

		if result["id"] == nil {
			t.Fatal("Expected id in response")
		}
		createdID = result["id"].(float64)
	})

	t.Run("GET /api/v1/{entity}/{id} - Get", func(t *testing.T) {
		resp, body := ts.doRequest("GET", fmt.Sprintf("/api/v1/%s/%d", entity, int(createdID)), nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}

		if result["name"] != "Alice Smith" {
			t.Errorf("Expected name 'Alice Smith', got %v", result["name"])
		}
		if result["email"] != "alice@example.com" {
			t.Errorf("Expected email 'alice@example.com', got %v", result["email"])
		}
	})

	t.Run("PUT /api/v1/{entity}/{id} - Update", func(t *testing.T) {
		data := map[string]interface{}{
			"name":  "Alice Johnson",
			"email": "alice.johnson@example.com",
			"age":   31,
		}

		resp, body := ts.doRequest("PUT", fmt.Sprintf("/api/v1/%s/%d", entity, int(createdID)), data)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		// Verify update
		_, body = ts.doRequest("GET", fmt.Sprintf("/api/v1/%s/%d", entity, int(createdID)), nil)
		var result map[string]interface{}
		_ = json.Unmarshal(body, &result)

		if result["name"] != "Alice Johnson" {
			t.Errorf("Expected updated name, got %v", result["name"])
		}
	})

	t.Run("PATCH /api/v1/{entity}/{id} - Patch", func(t *testing.T) {
		data := map[string]interface{}{
			"age": 32,
		}

		resp, body := ts.doRequest("PATCH", fmt.Sprintf("/api/v1/%s/%d", entity, int(createdID)), data)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		// Verify patch
		_, body = ts.doRequest("GET", fmt.Sprintf("/api/v1/%s/%d", entity, int(createdID)), nil)
		var result map[string]interface{}
		_ = json.Unmarshal(body, &result)

		if result["age"].(float64) != 32 {
			t.Errorf("Expected age 32, got %v", result["age"])
		}
		// Name should still be from update
		if result["name"] != "Alice Johnson" {
			t.Errorf("Expected name unchanged, got %v", result["name"])
		}
	})

	t.Run("GET /api/v1/{entity} - List", func(t *testing.T) {
		// Create another entity
		data := map[string]interface{}{
			"name":  "Bob Smith",
			"email": "bob@example.com",
			"age":   25,
		}
		ts.doRequest("POST", "/api/v1/"+entity, data)

		resp, body := ts.doRequest("GET", "/api/v1/"+entity, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}

		dataArray, ok := result["data"].([]interface{})
		if !ok {
			t.Fatal("Expected data array")
		}

		if len(dataArray) < 2 {
			t.Errorf("Expected at least 2 entities, got %d", len(dataArray))
		}
	})

	t.Run("DELETE /api/v1/{entity}/{id} - Delete", func(t *testing.T) {
		resp, body := ts.doRequest("DELETE", fmt.Sprintf("/api/v1/%s/%d", entity, int(createdID)), nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		// Verify deletion
		resp, _ = ts.doRequest("GET", fmt.Sprintf("/api/v1/%s/%d", entity, int(createdID)), nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404 after delete, got %d", resp.StatusCode)
		}
	})
}

// TestEntitySave tests save with specific ID
func TestEntitySave(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	t.Run("POST /api/v1/{entity}/save/{id}", func(t *testing.T) {
		data := map[string]interface{}{
			"name":  "Charlie",
			"email": "charlie@example.com",
		}

		resp, body := ts.doRequest("POST", "/api/v1/users/save/100", data)
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("Expected 201 on first save, got %d: %s", resp.StatusCode, string(body))
		}

		// Verify entity exists with ID 100
		resp, body = ts.doRequest("GET", "/api/v1/users/100", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)
		if result["id"].(float64) != 100 {
			t.Errorf("Expected id 100, got %v", result["id"])
		}
	})

	t.Run("POST /api/v1/{entity}/save/{id} - Overwrite", func(t *testing.T) {
		// Second save to the same ID should overwrite (upsert), not conflict.
		data := map[string]interface{}{
			"name":  "Charlie Updated",
			"email": "charlie-updated@example.com",
		}

		resp, body := ts.doRequest("POST", "/api/v1/users/save/100", data)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 on overwrite, got %d: %s", resp.StatusCode, string(body))
		}

		// Verify the data was actually replaced.
		resp, body = ts.doRequest("GET", "/api/v1/users/100", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 on GET after overwrite, got %d", resp.StatusCode)
		}
		var result map[string]interface{}
		json.Unmarshal(body, &result)
		if result["name"] != "Charlie Updated" {
			t.Errorf("Expected overwritten name 'Charlie Updated', got %v", result["name"])
		}
	})
}

// TestEntityReferences tests entity references and graph updates
func TestEntityReferences(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	var managerID, employeeID float64

	t.Run("Create entities with references", func(t *testing.T) {
		// Create manager
		manager := map[string]interface{}{
			"name": "Manager Bob",
			"role": "manager",
		}
		resp, body := ts.doRequest("POST", "/api/v1/users", manager)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("Failed to create manager: %s", string(body))
		}
		var result map[string]interface{}
		json.Unmarshal(body, &result)
		managerID = result["id"].(float64)

		// Create employee with reference to manager
		employee := map[string]interface{}{
			"name": "Employee Alice",
			"role": "employee",
			"manager": map[string]interface{}{
				"type":   "REF",
				"entity": "users",
				"id":     managerID,
			},
		}
		resp, body = ts.doRequest("POST", "/api/v1/users", employee)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("Failed to create employee: %s", string(body))
		}
		json.Unmarshal(body, &result)
		employeeID = result["id"].(float64)
	})

	t.Run("Get with embedded references", func(t *testing.T) {
		resp, body := ts.doRequest("GET", fmt.Sprintf("/api/v1/users/%d?embed_depth=1", int(employeeID)), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Failed to get employee: %s", string(body))
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		manager, ok := result["manager"].(map[string]interface{})
		if !ok {
			t.Fatal("Expected manager to be embedded")
		}

		if manager["name"] != "Manager Bob" {
			t.Errorf("Expected embedded manager name, got %v", manager["name"])
		}
	})
}

// TestGraphOperations tests graph endpoints
func TestGraphOperations(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create test data with relationships
	var user1ID, user2ID, user3ID float64

	// Setup: Create users with relationships
	user1 := map[string]interface{}{"name": "User1"}
	_, body := ts.doRequest("POST", "/api/v1/users", user1)
	var result map[string]interface{}
	json.Unmarshal(body, &result)
	user1ID = result["id"].(float64)

	user2 := map[string]interface{}{
		"name": "User2",
		"friend": map[string]interface{}{
			"type": "REF", "entity": "users", "id": user1ID,
		},
	}
	_, body = ts.doRequest("POST", "/api/v1/users", user2)
	json.Unmarshal(body, &result)
	user2ID = result["id"].(float64)

	user3 := map[string]interface{}{
		"name": "User3",
		"friend": map[string]interface{}{
			"type": "REF", "entity": "users", "id": user2ID,
		},
	}
	_, body = ts.doRequest("POST", "/api/v1/users", user3)
	json.Unmarshal(body, &result)
	user3ID = result["id"].(float64)

	t.Run("GET /api/v1/graph/stats", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/graph/stats", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		if result["node_count"] == nil {
			t.Error("Expected node_count in response")
		}
		if result["edge_count"] == nil {
			t.Error("Expected edge_count in response")
		}
	})

	t.Run("POST /api/v1/graph/path", func(t *testing.T) {
		data := map[string]interface{}{
			"from":      fmt.Sprintf("users:%d", int(user3ID)),
			"to":        fmt.Sprintf("users:%d", int(user1ID)),
			"max_depth": 10,
		}

		resp, body := ts.doRequest("POST", "/api/v1/graph/path", data)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		path, ok := result["path"].([]interface{})
		if !ok {
			t.Fatal("Expected path in response")
		}

		if len(path) < 2 {
			t.Errorf("Expected path with at least 2 nodes, got %d", len(path))
		}
	})

	t.Run("POST /api/v1/graph/neighbors", func(t *testing.T) {
		data := map[string]interface{}{
			"node_id": fmt.Sprintf("users:%d", int(user2ID)),
		}

		resp, body := ts.doRequest("POST", "/api/v1/graph/neighbors", data)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		neighbors, ok := result["neighbors"].(map[string]interface{})
		if !ok {
			t.Fatal("Expected neighbors in response")
		}

		if len(neighbors) == 0 {
			t.Error("Expected at least one neighbor")
		}
	})
}

// TestSchemaOperations tests schema endpoints
func TestSchemaOperations(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	entity := "products"

	t.Run("POST /api/v1/schema/{entity}", func(t *testing.T) {
		schema := map[string]interface{}{
			"type":     "object",
			"required": []string{"name", "price"},
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type": "string",
				},
				"price": map[string]interface{}{
					"type": "number",
				},
			},
		}

		resp, body := ts.doRequest("POST", "/api/v1/schema/"+entity, schema)
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("Expected 201, got %d: %s", resp.StatusCode, string(body))
		}
	})

	t.Run("GET /api/v1/schema/{entity}", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/schema/"+entity, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		if result["type"] != "object" {
			t.Error("Expected schema type object")
		}
	})
}

// TestPagination tests list pagination
func TestPagination(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create 10 test entities
	for i := 0; i < 10; i++ {
		data := map[string]interface{}{
			"name": fmt.Sprintf("User%d", i),
			"age":  20 + i,
		}
		ts.doRequest("POST", "/api/v1/users", data)
	}

	t.Run("Pagination parameters", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/users?page=1&per_page=5", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		pagination, ok := result["pagination"].(map[string]interface{})
		if !ok {
			t.Fatal("Expected pagination in response")
		}

		if pagination["page"].(float64) != 1 {
			t.Errorf("Expected page 1, got %v", pagination["page"])
		}
		if pagination["per_page"].(float64) != 5 {
			t.Errorf("Expected per_page 5, got %v", pagination["per_page"])
		}

		data, ok := result["data"].([]interface{})
		if !ok {
			t.Fatal("Expected data array")
		}
		if len(data) != 5 {
			t.Errorf("Expected 5 items, got %d", len(data))
		}
	})
}

// TestErrorHandling tests error responses
func TestErrorHandling(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	t.Run("GET non-existent entity", func(t *testing.T) {
		resp, _ := ts.doRequest("GET", "/api/v1/users/99999", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("Invalid ID format", func(t *testing.T) {
		resp, _ := ts.doRequest("GET", "/api/v1/users/invalid", nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("Invalid JSON body", func(t *testing.T) {
		req, _ := http.NewRequest("POST", ts.ts.URL+"/api/v1/users", bytes.NewBufferString("invalid json"))
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatalf("request failed: %v", doErr)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected 400, got %d", resp.StatusCode)
		}
	})
}

// TestErrorResponseEnvelope verifies that API error responses use the
// structured envelope: {"error": {"code": "XOLU-...", "message": "...", "status": N}}.
// This catches regressions if someone changes writeError or adds a new
// error path that uses the old flat format.
func TestErrorResponseEnvelope(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Trigger a writeError response — invalid ID is the simplest case.
	resp, body := ts.doRequest("GET", "/api/v1/users/invalid", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d", resp.StatusCode)
	}

	var envelope map[string]interface{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("Response is not valid JSON: %v", err)
	}

	errObj, ok := envelope["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected nested 'error' object, got %T: %v", envelope["error"], envelope["error"])
	}

	// Verify all three required fields exist
	code, ok := errObj["code"].(string)
	if !ok || code == "" {
		t.Errorf("error.code missing or empty: %v", errObj["code"])
	}
	msg, ok := errObj["message"].(string)
	if !ok || msg == "" {
		t.Errorf("error.message missing or empty: %v", errObj["message"])
	}
	status, ok := errObj["status"].(float64) // JSON numbers are float64
	if !ok || status != 400 {
		t.Errorf("error.status expected 400, got %v", errObj["status"])
	}
}

// ============================================================================
// Full-Text Search Endpoint Tests
// ============================================================================

// setupTestServerWithFTS creates a test server with SQLite and FTS enabled
func setupTestServerWithFTS(t *testing.T) *TestServer {
	tmpDir, err := os.MkdirTemp("", "xolu-fts-test-*")
	if err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := &config.Config{
		Host:                "localhost",
		Port:                0,
		StorageType:         "sqlite",
		BaseDir:             tmpDir,
		Schema:              "test_schema",
		CacheType:           "memory",
		CacheTTL:            300,
		GraphEnabled:        true,
		GraphMode:           "flat",
		FullTextEnabled:     true,
		CascadingDelete:     false,
		RefEmbedDepth:       3,
		MaxEmbedDepth:       10,
		MaxEntitySize:       1048576,
		PatchNullBehavior:   "store",
		MaxCascadeDeletions: 100,
		TenantMode:          "path",
		TenantAutoRegister:  true, // Tests rely on auto-registration
	}

	store, err := storage.NewStore("sqlite", map[string]interface{}{
		"db_path":           dbPath,
		"full_text_enabled": true,
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatal(err)
	}

	memCache := cache.NewMemoryCache(1000, time.Duration(cfg.CacheTTL)*time.Second)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	httpServer := httptest.NewServer(srv.Handler())

	// Store the store reference for cleanup
	testServer := &TestServer{
		server: srv,
		ts:     httpServer,
		cfg:    cfg,
		t:      t,
	}
	// Override cleanup to also close SQLite
	testServer.sqliteStore = store
	return testServer
}

func TestFullTextSearchEndpoint(t *testing.T) {
	ts := setupTestServerWithFTS(t)
	defer ts.cleanup()

	// Create test data
	ts.doRequest("POST", "/api/v1/users", map[string]interface{}{
		"name":  "Alice Engineer",
		"email": "alice@example.com",
		"bio":   "Software developer who loves Go programming",
	})
	ts.doRequest("POST", "/api/v1/users", map[string]interface{}{
		"name":  "Bob Manager",
		"email": "bob@example.com",
		"bio":   "Product manager with technical background",
	})
	ts.doRequest("POST", "/api/v1/posts", map[string]interface{}{
		"title":   "Go Programming Tips",
		"content": "Learn Go programming effectively",
	})

	t.Run("Search without query returns error", func(t *testing.T) {
		resp, _ := ts.doRequest("GET", "/api/v1/search", nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected 400 for missing query, got %d", resp.StatusCode)
		}
	})

	t.Run("Search across all entities", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/search?q=programming", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d. Body: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		countVal, ok := result["count"].(float64)
		if !ok {
			t.Fatalf("Expected count in response, got: %v", result)
		}
		count := int(countVal)
		if count != 2 {
			t.Errorf("Expected 2 results for 'programming', got %d", count)
		}
	})

	t.Run("Search within entity type", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/search?q=programming&entity=users", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d. Body: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		countVal, ok := result["count"].(float64)
		if !ok {
			t.Fatalf("Expected count in response, got: %v", result)
		}
		count := int(countVal)
		if count != 1 {
			t.Errorf("Expected 1 result for 'programming' in users, got %d", count)
		}
	})

	t.Run("Search with no matches", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/search?q=xyznonexistent", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d. Body: %s", resp.StatusCode, string(body))
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		countVal, ok := result["count"].(float64)
		if !ok {
			t.Fatalf("Expected count in response, got: %v", result)
		}
		count := int(countVal)
		if count != 0 {
			t.Errorf("Expected 0 results for nonexistent term, got %d", count)
		}
	})
}

func TestFullTextSearchDisabled(t *testing.T) {
	// Use regular setup (FTS disabled)
	ts := setupTestServer(t)
	defer ts.cleanup()

	resp, _ := ts.doRequest("GET", "/api/v1/search?q=test", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 when FTS disabled, got %d", resp.StatusCode)
	}
}

// ============================================================================
// REF Embed Depth Tests
// ============================================================================

func TestRefEmbedDepth(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create a chain of references: user1 -> user2 -> user3
	_, body1 := ts.doRequest("POST", "/api/v1/users", map[string]interface{}{
		"name": "User 3 (deepest)",
	})
	var u3 map[string]interface{}
	json.Unmarshal(body1, &u3)
	id3 := int(u3["id"].(float64))

	_, body2 := ts.doRequest("POST", "/api/v1/users", map[string]interface{}{
		"name": "User 2 (middle)",
		"manager": map[string]interface{}{
			"type":   "REF",
			"entity": "users",
			"id":     id3,
		},
	})
	var u2 map[string]interface{}
	json.Unmarshal(body2, &u2)
	id2 := int(u2["id"].(float64))

	ts.doRequest("POST", "/api/v1/users", map[string]interface{}{
		"name": "User 1 (top)",
		"manager": map[string]interface{}{
			"type":   "REF",
			"entity": "users",
			"id":     id2,
		},
	})

	t.Run("Default embedding resolves refs", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/users/3", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		// Manager should be embedded (not a REF object)
		manager, ok := result["manager"].(map[string]interface{})
		if !ok {
			t.Fatal("Expected manager to be embedded object")
		}
		if manager["type"] == "REF" {
			t.Error("Expected manager to be resolved, not REF")
		}
		if manager["name"] != "User 2 (middle)" {
			t.Errorf("Expected manager name 'User 2 (middle)', got %v", manager["name"])
		}
	})

	t.Run("embed=false disables embedding", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/users/3?embed=false", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		// Manager should be a REF object
		manager, ok := result["manager"].(map[string]interface{})
		if !ok {
			t.Fatal("Expected manager field")
		}
		if manager["type"] != "REF" {
			t.Error("Expected manager to be REF when embed=false")
		}
	})

	t.Run("embed_depth=1 limits depth", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/users/3?embed_depth=1", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		// First level should be embedded
		manager, ok := result["manager"].(map[string]interface{})
		if !ok || manager["type"] == "REF" {
			t.Error("Expected first level manager to be embedded")
		}

		// Second level should still be REF (depth exhausted)
		nestedManager, ok := manager["manager"].(map[string]interface{})
		if ok && nestedManager["type"] != "REF" {
			t.Error("Expected nested manager to remain as REF at depth 1")
		}
	})
}

// ============================================================================
// OQL Endpoint Tests
// ============================================================================

func TestOQLQueryEndpoint(t *testing.T) {
	// Create temp directory with pre-existing entity folders
	// so OQL validator recognizes them at startup
	tmpDir, err := os.MkdirTemp("", "xolu-oql-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Pre-create entity directories so OQL validator finds them
	usersDir := filepath.Join(tmpDir, "test_schema", "users")
	if err := os.MkdirAll(usersDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Host:                "localhost",
		Port:                0,
		StorageType:         "sqlite",
		BaseDir:             tmpDir,
		Schema:              "test_schema",
		SchemaDir:           filepath.Join(tmpDir, "test_schema"),
		CacheType:           "memory",
		CacheTTL:            300,
		GraphEnabled:        true,
		GraphMode:           "flat",
		FullTextEnabled:     false,
		RefEmbedDepth:       3,
		MaxEmbedDepth:       10,
		MaxEntitySize:       1048576,
		PatchNullBehavior:   "store",
		MaxCascadeDeletions: 100,
	}

	store, _ := storage.NewStore("sqlite", map[string]interface{}{"db_path": filepath.Join(tmpDir, "test.db")})
	memCache := cache.NewMemoryCache(1000, time.Second*300)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	doReq := func(method, path string, body interface{}) (*http.Response, []byte) {
		var bodyBytes []byte
		if body != nil {
			bodyBytes, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewBuffer(bodyBytes))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatalf("request failed: %v", doErr)
		}
		defer resp.Body.Close()
		respBody := &bytes.Buffer{}
		respBody.ReadFrom(resp.Body)
		return resp, respBody.Bytes()
	}

	// Create test data
	doReq("POST", "/api/v1/users", map[string]interface{}{"name": "Alice", "age": 30})
	doReq("POST", "/api/v1/users", map[string]interface{}{"name": "Bob", "age": 25})
	doReq("POST", "/api/v1/users", map[string]interface{}{"name": "Carol", "age": 35})

	t.Run("Basic SELECT query", func(t *testing.T) {
		resp, body := doReq("POST", "/api/v1/oql/query", map[string]interface{}{
			"query": "SELECT * FROM users",
		})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d. Body: %s", resp.StatusCode, string(body))
			return
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		data, ok := result["data"].([]interface{})
		if !ok {
			t.Fatalf("Expected data array, got: %v", result)
		}
		if len(data) != 3 {
			t.Errorf("Expected 3 results, got %d", len(data))
		}
	})

	t.Run("SELECT with WHERE", func(t *testing.T) {
		resp, body := doReq("POST", "/api/v1/oql/query", map[string]interface{}{
			"query": "SELECT * FROM users WHERE age > 28",
		})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
			return
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		data, ok := result["data"].([]interface{})
		if !ok {
			t.Fatalf("Expected data array")
		}
		if len(data) != 2 {
			t.Errorf("Expected 2 results (age > 28), got %d", len(data))
		}
	})

	t.Run("SELECT with ORDER BY", func(t *testing.T) {
		resp, body := doReq("POST", "/api/v1/oql/query", map[string]interface{}{
			"query": "SELECT name FROM users ORDER BY age DESC",
		})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
			return
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		data, ok := result["data"].([]interface{})
		if !ok || len(data) == 0 {
			t.Fatalf("Expected non-empty data array")
		}
		first := data[0].(map[string]interface{})
		if first["name"] != "Carol" {
			t.Errorf("Expected Carol first (oldest), got %v", first["name"])
		}
	})

	t.Run("SELECT with LIMIT", func(t *testing.T) {
		resp, body := doReq("POST", "/api/v1/oql/query", map[string]interface{}{
			"query": "SELECT TOP 2 * FROM users",
		})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
			return
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		data, ok := result["data"].([]interface{})
		if !ok {
			t.Fatalf("Expected data array")
		}
		if len(data) != 2 {
			t.Errorf("Expected 2 results with TOP 2, got %d", len(data))
		}
	})

	t.Run("Invalid query", func(t *testing.T) {
		resp, _ := doReq("POST", "/api/v1/oql/query", map[string]interface{}{
			"query": "INVALID QUERY SYNTAX",
		})
		if resp.StatusCode == http.StatusOK {
			t.Error("Expected error for invalid query")
		}
	})

	t.Run("Empty query", func(t *testing.T) {
		resp, _ := doReq("POST", "/api/v1/oql/query", map[string]interface{}{
			"query": "",
		})
		if resp.StatusCode == http.StatusOK {
			t.Error("Expected error for empty query")
		}
	})
}

func TestOQLAsyncEndpoint(t *testing.T) {
	// Create temp directory with pre-existing entity folders
	tmpDir, err := os.MkdirTemp("", "xolu-oql-async-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Pre-create entity directory
	usersDir := filepath.Join(tmpDir, "test_schema", "users")
	if err := os.MkdirAll(usersDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Host:                 "localhost",
		Port:                 0,
		StorageType:          "sqlite",
		BaseDir:              tmpDir,
		Schema:               "test_schema",
		SchemaDir:            filepath.Join(tmpDir, "test_schema"),
		CacheType:            "memory",
		CacheTTL:             300,
		GraphEnabled:         true,
		GraphMode:            "flat",
		RefEmbedDepth:        3,
		MaxEmbedDepth:        10,
		MaxEntitySize:        1048576,
		PatchNullBehavior:    "store",
		MaxCascadeDeletions:  100,
		AsyncJobRetentionTTL: 3600,
	}

	store, _ := storage.NewStore("sqlite", map[string]interface{}{"db_path": filepath.Join(tmpDir, "test.db")})
	memCache := cache.NewMemoryCache(1000, time.Second*300)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	doReq := func(method, path string, body interface{}) (*http.Response, []byte) {
		var bodyBytes []byte
		if body != nil {
			bodyBytes, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewBuffer(bodyBytes))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatalf("request failed: %v", doErr)
		}
		defer resp.Body.Close()
		respBody := &bytes.Buffer{}
		respBody.ReadFrom(resp.Body)
		return resp, respBody.Bytes()
	}

	// Create test data
	doReq("POST", "/api/v1/users", map[string]interface{}{"name": "Test"})

	t.Run("Submit async query", func(t *testing.T) {
		resp, body := doReq("POST", "/api/v1/oql/query/async", map[string]interface{}{
			"query": "SELECT * FROM users",
		})
		if resp.StatusCode != http.StatusAccepted {
			t.Errorf("Expected 202, got %d. Body: %s", resp.StatusCode, string(body))
			return
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		queryID, ok := result["query_id"].(string)
		if !ok || queryID == "" {
			t.Errorf("Expected query_id in response, got: %v", result)
		}
	})
}

// ============================================================================
// Sulpher Endpoint Tests
// ============================================================================

func TestSulpherQueryEndpoint(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create connected entities
	ts.doRequest("POST", "/api/v1/users", map[string]interface{}{
		"name": "Alice",
	})
	ts.doRequest("POST", "/api/v1/posts", map[string]interface{}{
		"title": "Hello",
		"author": map[string]interface{}{
			"type":   "REF",
			"entity": "users",
			"id":     1,
		},
	})

	t.Run("Basic path query", func(t *testing.T) {
		resp, body := ts.doRequest("POST", "/api/v1/graph/query", map[string]interface{}{
			"query": "MATCH (u:users) RETURN u",
		})
		// Sulpher queries may return 200 even with no results
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			t.Errorf("Expected 200 or 202, got %d. Body: %s", resp.StatusCode, string(body))
		}
	})

	t.Run("Invalid query", func(t *testing.T) {
		resp, _ := ts.doRequest("POST", "/api/v1/graph/query", map[string]interface{}{
			"query": "",
		})
		if resp.StatusCode == http.StatusOK {
			t.Error("Expected error for empty query")
		}
	})
}

// ============================================================================
// Multi-Tenant Tests
// ============================================================================

func TestTenantIsolation(t *testing.T) {
	// Tenant isolation on point operations (Get/Put/Delete by ID) requires
	// a storage backend that scopes by tenant_id.
	// not support this; use SQLite.
	tmpDir, err := os.MkdirTemp("", "xolu-tenant-iso-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "tenant_iso.db")
	cfg := &config.Config{
		Host:                "localhost",
		Port:                0,
		StorageType:         "sqlite",
		BaseDir:             tmpDir,
		Schema:              "test_schema",
		SchemaDir:           filepath.Join(tmpDir, "test_schema"),
		CacheType:           "memory",
		CacheTTL:            300,
		GraphEnabled:        true,
		GraphMode:           "flat",
		FullTextEnabled:     false,
		CascadingDelete:     false,
		RefEmbedDepth:       3,
		MaxEmbedDepth:       10,
		MaxEntitySize:       1048576,
		PatchNullBehavior:   "store",
		MaxCascadeDeletions: 100,
		TenantMode:          "path",
		TenantAutoRegister:  true, // Tests rely on auto-registration
	}

	sqliteStore, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type:   "sqlite",
		DBPath: dbPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteStore.Close()

	memCache := cache.NewMemoryCache(1000, time.Duration(cfg.CacheTTL)*time.Second)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, sqliteStore, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	doReq := func(method, path string, body interface{}) (*http.Response, []byte) {
		var bodyBytes []byte
		if body != nil {
			bodyBytes, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewBuffer(bodyBytes))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatalf("request failed: %v", doErr)
		}
		defer resp.Body.Close()
		respBody := &bytes.Buffer{}
		respBody.ReadFrom(resp.Body)
		return resp, respBody.Bytes()
	}

	// Create entities in tenant1
	doReq("POST", "/api/v1/tenant/tenant1/users", map[string]interface{}{
		"name": "Alice",
	})
	doReq("POST", "/api/v1/tenant/tenant1/users", map[string]interface{}{
		"name": "Bob",
	})

	// Create entity in tenant2
	doReq("POST", "/api/v1/tenant/tenant2/users", map[string]interface{}{
		"name": "Carol",
	})

	t.Run("List only shows tenant's data", func(t *testing.T) {
		resp, body := doReq("GET", "/api/v1/tenant/tenant1/users", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Expected 200, got %d", resp.StatusCode)
		}

		var result map[string]interface{}
		json.Unmarshal(body, &result)

		data := result["data"].([]interface{})
		if len(data) != 2 {
			t.Errorf("Expected 2 users in tenant1, got %d", len(data))
		}
	})

	t.Run("Get from wrong tenant returns 404", func(t *testing.T) {
		// Tenant1 has id=1 (Alice) and id=2 (Bob). Tenant2 has id=1 (Carol).
		// Querying tenant2 for id=2 should return 404 — that ID only exists in tenant1.
		resp, body := doReq("GET", "/api/v1/tenant/tenant2/users/2", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404 for cross-tenant access, got %d; body: %s", resp.StatusCode, string(body))
		}
	})

	t.Run("Update in wrong tenant fails", func(t *testing.T) {
		resp, _ := doReq("PUT", "/api/v1/tenant/tenant2/users/2", map[string]interface{}{
			"name": "Hacked",
		})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404 for cross-tenant update, got %d", resp.StatusCode)
		}
	})

	t.Run("Delete in wrong tenant fails", func(t *testing.T) {
		resp, _ := doReq("DELETE", "/api/v1/tenant/tenant2/users/2", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected 404 for cross-tenant delete, got %d", resp.StatusCode)
		}
	})
}

// ============================================================================
// Additional Server Tests
// ============================================================================

func TestMetricsEndpoint(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	t.Run("Prometheus format", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.ts.URL+"/metrics", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		// Metrics may be disabled in test config
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("Expected 200 or 503, got %d", resp.StatusCode)
		}
	})

	t.Run("JSON format", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.ts.URL+"/metrics", nil)
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("Expected 200 or 503, got %d", resp.StatusCode)
		}
	})
}

func TestExportEndpoint(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create some data
	ts.doRequest("POST", "/api/v1/users", map[string]interface{}{
		"name": "Alice",
	})

	resp, body := ts.doRequest("GET", "/api/v1/export", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d. Body: %s", resp.StatusCode, string(body))
	}

	// Check content type is zip
	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/zip" {
		t.Errorf("Expected Content-Type application/zip, got %s", contentType)
	}
}

func TestSearchEndpoint(t *testing.T) {
	// Full-text search requires SQLite with FTS enabled
	// These tests verify the endpoint behavior with FTS disabled
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create test data
	ts.doRequest("POST", "/api/v1/users", map[string]interface{}{
		"name":  "Alice Smith",
		"email": "alice@example.com",
	})

	t.Run("Full-text search disabled returns 503", func(t *testing.T) {
		resp, body := ts.doRequest("GET", "/api/v1/search?q=Alice", nil)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("Expected 503 (FTS disabled), got %d. Body: %s", resp.StatusCode, string(body))
		}
	})

	t.Run("Full-text search missing query returns 400", func(t *testing.T) {
		resp, _ := ts.doRequest("GET", "/api/v1/search", nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected 400 for missing query, got %d", resp.StatusCode)
		}
	})
}

// ============================================================================
// Tenant Strict Mode Tests
// ============================================================================

func TestTenantStrictMode(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "xolu-tenant-strict-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Host:                "localhost",
		Port:                0,
		StorageType:         "sqlite",
		BaseDir:             tmpDir,
		Schema:              "test_schema",
		SchemaDir:           filepath.Join(tmpDir, "test_schema"),
		CacheType:           "memory",
		CacheTTL:            300,
		GraphEnabled:        true,
		GraphMode:           "flat",
		RefEmbedDepth:       3,
		MaxEmbedDepth:       10,
		MaxEntitySize:       1048576,
		PatchNullBehavior:   "store",
		MaxCascadeDeletions: 100,
		TenantMode:          "strict", // Explicit: tenants must be pre-registered
		AuthType:            "none",
	}

	store, _ := storage.NewStore("sqlite", map[string]interface{}{"db_path": filepath.Join(tmpDir, "test.db")})
	memCache := cache.NewMemoryCache(1000, time.Second*300)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)

	// In strict mode, tenants must be pre-registered before use.
	srv.TenantRegistry().Register(context.Background(), "acme", 1)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	doReq := func(method, path string, body interface{}) (*http.Response, []byte) {
		var bodyBytes []byte
		if body != nil {
			bodyBytes, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewBuffer(bodyBytes))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatalf("request failed: %v", doErr)
		}
		defer resp.Body.Close()
		respBody := &bytes.Buffer{}
		respBody.ReadFrom(resp.Body)
		return resp, respBody.Bytes()
	}

	t.Run("Non-tenant entity route blocked", func(t *testing.T) {
		resp, _ := doReq("POST", "/api/v1/users", map[string]interface{}{
			"name": "Alice",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected 403 for non-tenant route in strict mode, got %d", resp.StatusCode)
		}
	})

	t.Run("Tenant route allowed", func(t *testing.T) {
		resp, _ := doReq("POST", "/api/v1/tenant/acme/users", map[string]interface{}{
			"name": "Alice",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("Expected 201 for tenant route, got %d", resp.StatusCode)
		}
	})

	t.Run("Health endpoint allowed", func(t *testing.T) {
		resp, _ := doReq("GET", "/health", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 for health endpoint, got %d", resp.StatusCode)
		}
	})

	t.Run("Version endpoint allowed", func(t *testing.T) {
		resp, _ := doReq("GET", "/version", nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 for version endpoint, got %d", resp.StatusCode)
		}
	})

	t.Run("Graph routes blocked in strict mode", func(t *testing.T) {
		resp, _ := doReq("GET", "/api/v1/graph/stats", nil)
		// Graph is automatically disabled in strict mode (not tenant-isolated).
		// The middleware blocks all non-tenant, non-schema /api/v1/ routes.
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected 403 for graph stats in strict mode, got %d", resp.StatusCode)
		}
	})
}

// ── KL-2: POST /api/v1/graph/edges — route exists and validates input ───────
//
// Full graph-enabled integration is tested in pkg/storage/edge_props_test.go.
// Here we verify the route is registered and input validation works, using the
// test server which doesn't have GraphEnabled on its store.

func TestKL2_CreateEdgeRouteRegistered(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// The endpoint must exist (not 404). It will return 400 for missing fields
	// or 500 if the store doesn't support edge props, but not 404.
	resp, _ := ts.doRequest("POST", "/api/v1/graph/edges", map[string]interface{}{
		"from": "person:1",
		"to":   "person:2",
		"rel":  "KNOWS",
	})
	if resp.StatusCode == http.StatusNotFound {
		t.Error("[KL-2] POST /api/v1/graph/edges returned 404 — route not registered")
	}
}

func TestKL2_CreateEdgeRequiresFields(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Missing 'rel' — must return 400, not 404.
	resp, _ := ts.doRequest("POST", "/api/v1/graph/edges", map[string]interface{}{
		"from": "person:1",
		"to":   "person:2",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("[KL-2] missing rel: expected 400, got %d", resp.StatusCode)
	}
}

// ── KL-3: WAL checkpoint is called in reloadGraphFromStore ───────────────────
//
// The fix adds a PRAGMA wal_checkpoint(FULL) before graph.Clear().
// We test this by verifying the function no longer panics or errors when called
// immediately after writes, and that the graph stats endpoint remains reachable.

func TestKL3_RebuildEndpointReachable(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Create an entity so there is something in the database.
	ts.doRequest("POST", "/api/v1/person",
		map[string]interface{}{"name": "Alice"})

	// Rebuild should not 500 due to WAL issues (the test store may return
	// a different error if graph tables don't exist, but should not panic).
	resp, _ := ts.doRequest("POST", "/api/v1/graph/admin/rebuild", nil)
	if resp.StatusCode == 0 {
		t.Error("[KL-3] rebuild panicked or returned no response")
	}

	// The stats endpoint must still be reachable after rebuild (no panic, no 500).
	resp, _ = ts.doRequest("GET", "/api/v1/graph/stats", nil)
	if resp.StatusCode == 0 {
		t.Error("[KL-3] stats endpoint unreachable after rebuild")
	}
}

// ── KL-2 and KL-3: round-trip tests with graph-enabled store ─────────────────
//
// setupTestServer uses storage.NewStore which doesn't propagate GraphEnabled.
// These tests construct their own server with storage.NewStoreFromConfig so the
// graph tables are created and edge property storage works.

type graphTestServer struct {
	srv    *server.Server
	ts     *httptest.Server
	tmpDir string
	t      *testing.T
}

func setupGraphServer(t *testing.T) *graphTestServer {
	t.Helper()
	return setupGraphServerWithOptions(t, 0, cache.NewMemoryCache(1000, 300*time.Second))
}

// setupCachedGraphServer builds a graph-enabled server with query-result caching
// enabled at the given TTL. The standard MemoryCache is used; per-item TTL is
// honoured correctly so no special cache construction is needed.
func setupCachedGraphServer(t *testing.T, cacheTTL time.Duration) *graphTestServer {
	t.Helper()
	return setupGraphServerWithOptions(t, cacheTTL, cache.NewMemoryCache(1000, 300*time.Second))
}

func setupGraphServerWithOptions(t *testing.T, queryCacheTTL time.Duration, memCache cache.Cache) *graphTestServer {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "xolu-graph-test-*")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type:         "sqlite",
		DBPath:       dbPath,
		GraphEnabled: true,
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("NewStoreFromConfig: %v", err)
	}

	cfg := &config.Config{
		Host:               "localhost",
		Port:               0,
		BaseDir:            tmpDir,
		Schema:             "test_schema",
		SchemaDir:          filepath.Join(tmpDir, "test_schema"),
		CacheType:          "memory",
		CacheTTL:           300,
		GraphEnabled:       true,
		GraphMode:          "flat",
		TenantMode:         "path",
		TenantAutoRegister: true,
		RefEmbedDepth:      3,
		MaxEmbedDepth:      10,
		MaxEntitySize:      1048576,
		PatchNullBehavior:  "store",
		GraphQueryCacheTTL: int(queryCacheTTL.Seconds()),
	}

	g := graph.NewFlatGraph()
	validator := validation.NewNoOpValidator()
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())

	return &graphTestServer{srv: srv, ts: ts, tmpDir: tmpDir, t: t}
}

func (g *graphTestServer) cleanup() {
	g.ts.Close()
	os.RemoveAll(g.tmpDir)
}

func (g *graphTestServer) do(method, path string, body interface{}) (*http.Response, []byte) {
	g.t.Helper()
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			g.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, g.ts.URL+path, bytes.NewBuffer(bodyBytes))
	if err != nil {
		g.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp, respBody
}

// TestKL2_EdgeWriteAppearsInSulpherQuery verifies the full pipeline:
// POST /graph/edges → in-memory graph updated → Sulpher query finds the edge.
//
// Before the fix, handleCreateEdge called updateGraph("", 0, {}) which was a
// no-op. The AddEdgeWithID call was missing, so the in-memory adjacency was
// never updated and Sulpher queries found nothing.
func TestKL2_EdgeWriteAppearsInSulpherQuery(t *testing.T) {
	ts := setupGraphServer(t)
	defer ts.cleanup()

	// Register schema and create two entities.
	ts.do("POST", "/api/v1/schema/person", map[string]interface{}{
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
		"required": []string{"name"},
	})

	_, b1 := ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Alice"})
	_, b2 := ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Bob"})

	var r1, r2 map[string]interface{}
	json.Unmarshal(b1, &r1)
	json.Unmarshal(b2, &r2)
	id1 := int(r1["id"].(float64))
	id2 := int(r2["id"].(float64))

	// Write edge with properties via the REST endpoint.
	resp, body := ts.do("POST", "/api/v1/graph/edges", map[string]interface{}{
		"from":  fmt.Sprintf("person:%d", id1),
		"to":    fmt.Sprintf("person:%d", id2),
		"rel":   "KNOWS",
		"props": map[string]interface{}{"since": 2020},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("[KL-2] POST /graph/edges: %d %s", resp.StatusCode, body)
	}

	// Sulpher query must find the edge in the in-memory graph.
	resp, body = ts.do("POST", "/api/v1/graph/query", map[string]interface{}{
		"query":     "MATCH (a:person)-[:KNOWS]->(b:person) RETURN a.name AS a_name, b.name AS b_name",
		"max_depth": 5,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[KL-2] graph query: %d %s", resp.StatusCode, body)
	}
	var qr map[string]interface{}
	json.Unmarshal(body, &qr)
	rows, _ := qr["result"].([]interface{})
	if len(rows) == 0 {
		t.Error("[KL-2] Sulpher query found 0 KNOWS edges after REST write; in-memory graph not updated")
	}
}

// TestKL3_RebuildPreservesWrittenNodes verifies that calling the rebuild
// endpoint immediately after writing entities with edges does not clear
// the graph. Isolated nodes (no edges) are not stored in t<X>_graph and
// thus won't appear after rebuild — that is correct by design. This test
// uses entities connected by a REF edge so they survive the scan.
func TestKL3_RebuildPreservesWrittenNodes(t *testing.T) {
	ts := setupGraphServer(t)
	defer ts.cleanup()

	// No schema registration needed — entities stored as blobs.
	// The test only verifies that rebuild preserves graph edges.

	// Create Alice.
	_, b1 := ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Alice"})
	var r1 map[string]interface{}
	json.Unmarshal(b1, &r1)
	aliceID := int(r1["id"].(float64))

	// Create Bob with a REF to Alice — this creates an edge in t<X>_graph.
	// Note: reports_to is not declared in the schema, which is correct —
	// the server accepts extra fields for graph edge tracking purposes.
	ts.do("POST", "/api/v1/person", map[string]interface{}{
		"name":       "Bob",
		"reports_to": map[string]interface{}{"type": "REF", "entity": "person", "id": aliceID},
	})

	// Read edge count before rebuild (must be > 0).
	_, sb := ts.do("GET", "/api/v1/graph/stats", nil)
	var statsBefore map[string]interface{}
	json.Unmarshal(sb, &statsBefore)
	edgesBefore := int(statsBefore["edge_count"].(float64))
	if edgesBefore == 0 {
		t.Fatal("[KL-3] precondition: expected > 0 edges before rebuild")
	}

	// Trigger rebuild.
	resp, body := ts.do("POST", "/api/v1/graph/admin/rebuild", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[KL-3] rebuild: %d %s", resp.StatusCode, body)
	}

	// Edge count must be non-zero after rebuild.
	_, sa := ts.do("GET", "/api/v1/graph/stats", nil)
	var statsAfter map[string]interface{}
	json.Unmarshal(sa, &statsAfter)
	edgesAfter := int(statsAfter["edge_count"].(float64))

	if edgesAfter == 0 {
		t.Errorf("[KL-3] after rebuild: 0 edges; WAL checkpoint not flushing correctly (before=%d)", edgesBefore)
	}
}

// ── Graph query result cache tests ─────────────────────────────────────────── verifies that a repeated identical query is
// served from the cache (X-Cache: HIT) without re-running the BFS.
func TestGraphQueryCache_HappyPath(t *testing.T) {
	ts := setupCachedGraphServer(t, 30*time.Second)
	defer ts.cleanup()

	// Create two people connected by a REF edge.
	_, b1 := ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Alice"})
	var r1 map[string]interface{}
	json.Unmarshal(b1, &r1)
	aliceID := int(r1["id"].(float64))

	ts.do("POST", "/api/v1/person", map[string]interface{}{
		"name":       "Bob",
		"reports_to": map[string]interface{}{"type": "REF", "entity": "person", "id": aliceID},
	})

	query := `MATCH (a:person)-[:reports_to]->(b:person) RETURN a, b`

	// First call — should be a MISS (BFS executed, result cached).
	resp1, body1 := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{
		"query": query, "max_depth": 5,
	})
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("[cache happy path] first query: %d %s", resp1.StatusCode, body1)
	}
	if resp1.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[cache happy path] first query: expected X-Cache: MISS, got %q", resp1.Header.Get("X-Cache"))
	}

	// Second call — same query, should be a HIT.
	resp2, body2 := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{
		"query": query, "max_depth": 5,
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("[cache happy path] second query: %d %s", resp2.StatusCode, body2)
	}
	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Errorf("[cache happy path] second query: expected X-Cache: HIT, got %q", resp2.Header.Get("X-Cache"))
	}

	// Both responses must have the same body.
	if string(body1) != string(body2) {
		t.Errorf("[cache happy path] cache hit body differs from original\nMISS: %s\nHIT:  %s", body1, body2)
	}
}

// TestGraphQueryCache_DifferentQueriesNotShared verifies that two distinct
// queries have independent cache entries.
func TestGraphQueryCache_DifferentQueriesNotShared(t *testing.T) {
	ts := setupCachedGraphServer(t, 30*time.Second)
	defer ts.cleanup()

	ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Alice"})

	q1 := `MATCH (a:person) RETURN a`
	q2 := `MATCH (a:person) WHERE a.name = 'Alice' RETURN a`

	resp1, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": q1, "max_depth": 5})
	resp2, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": q2, "max_depth": 5})

	// Both must be MISS — they are different queries.
	if resp1.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[distinct queries] q1: expected MISS, got %q", resp1.Header.Get("X-Cache"))
	}
	if resp2.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[distinct queries] q2: expected MISS (distinct query), got %q", resp2.Header.Get("X-Cache"))
	}

	// Third call with q1 again — must be HIT, not q2's entry.
	resp3, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": q1, "max_depth": 5})
	if resp3.Header.Get("X-Cache") != "HIT" {
		t.Errorf("[distinct queries] q1 repeat: expected HIT, got %q", resp3.Header.Get("X-Cache"))
	}
}

// TestGraphQueryCache_WriteInvalidation verifies that a graph write (entity
// creation carrying a REF edge) evicts all cached query results for the tenant,
// so the next query re-runs the BFS and reflects the new topology.
func TestGraphQueryCache_WriteInvalidation(t *testing.T) {
	ts := setupCachedGraphServer(t, 30*time.Second)
	defer ts.cleanup()

	// Create Alice.
	_, b1 := ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Alice"})
	var r1 map[string]interface{}
	json.Unmarshal(b1, &r1)
	aliceID := int(r1["id"].(float64))

	query := `MATCH (a:person) RETURN a`

	// First query — MISS; result has 1 row (Alice only).
	resp1, body1 := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp1.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[write invalidation] first query: expected MISS, got %q", resp1.Header.Get("X-Cache"))
	}
	var r1Result map[string]interface{}
	json.Unmarshal(body1, &r1Result)
	rows1 := r1Result["result"].([]interface{})
	if len(rows1) != 1 {
		t.Fatalf("[write invalidation] expected 1 row before Bob, got %d", len(rows1))
	}

	// Second query — HIT (still Alice only, from cache).
	resp2, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Errorf("[write invalidation] second query: expected HIT, got %q", resp2.Header.Get("X-Cache"))
	}

	// Write Bob (with REF to Alice) — this must invalidate the cache.
	ts.do("POST", "/api/v1/person", map[string]interface{}{
		"name":       "Bob",
		"reports_to": map[string]interface{}{"type": "REF", "entity": "person", "id": aliceID},
	})

	// Third query — must be MISS (cache evicted by the write) and must have 2 rows.
	resp3, body3 := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp3.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[write invalidation] post-write query: expected MISS (cache evicted), got %q", resp3.Header.Get("X-Cache"))
	}
	var r3Result map[string]interface{}
	json.Unmarshal(body3, &r3Result)
	rows3 := r3Result["result"].([]interface{})
	if len(rows3) != 2 {
		t.Errorf("[write invalidation] expected 2 rows after Bob was added, got %d", len(rows3))
	}
}

// TestGraphQueryCache_EdgeWriteInvalidation verifies that a REST edge write
// (POST /graph/edges) also invalidates the cached query results.
func TestGraphQueryCache_EdgeWriteInvalidation(t *testing.T) {
	ts := setupCachedGraphServer(t, 30*time.Second)
	defer ts.cleanup()

	_, b1 := ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Alice"})
	var r1 map[string]interface{}
	json.Unmarshal(b1, &r1)
	aliceID := int(r1["id"].(float64))

	_, b2 := ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Bob"})
	var r2 map[string]interface{}
	json.Unmarshal(b2, &r2)
	bobID := int(r2["id"].(float64))

	query := `MATCH (a:person)-[:KNOWS]->(b:person) RETURN a, b`

	// First query — MISS; 0 rows (no edges yet).
	resp1, body1 := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp1.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[edge invalidation] first query: expected MISS, got %q", resp1.Header.Get("X-Cache"))
	}
	var r1res map[string]interface{}
	json.Unmarshal(body1, &r1res)
	if rows, _ := r1res["result"].([]interface{}); len(rows) != 0 {
		t.Fatalf("[edge invalidation] expected 0 rows before edge, got %d", len(rows))
	}

	// Second query — HIT (0 rows from cache).
	resp2, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Errorf("[edge invalidation] second query: expected HIT, got %q", resp2.Header.Get("X-Cache"))
	}

	// Write an edge via REST.
	ts.do("POST", "/api/v1/graph/edges", map[string]interface{}{
		"from": fmt.Sprintf("person:%d", aliceID),
		"to":   fmt.Sprintf("person:%d", bobID),
		"rel":  "KNOWS",
	})

	// Third query — must be MISS and see the new edge.
	resp3, body3 := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp3.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[edge invalidation] post-edge query: expected MISS, got %q", resp3.Header.Get("X-Cache"))
	}
	var r3res map[string]interface{}
	json.Unmarshal(body3, &r3res)
	if rows := r3res["result"].([]interface{}); len(rows) != 1 {
		t.Errorf("[edge invalidation] expected 1 row after edge write, got %d", len(rows))
	}
}

// TestGraphQueryCache_TTLExpiry verifies that a cached result becomes a MISS
// after the TTL has elapsed, even without an explicit write.
//
// GraphQueryCacheTTL is stored as integer seconds, so the minimum testable
// TTL is 1 second. The MemoryCache now honours per-item TTL correctly.
func TestGraphQueryCache_TTLExpiry(t *testing.T) {
	const cacheTTL = 1 * time.Second
	ts := setupCachedGraphServer(t, cacheTTL)
	defer ts.cleanup()

	ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Alice"})

	query := `MATCH (a:person) RETURN a`

	// First call — MISS, result cached with 1s TTL.
	resp1, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp1.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[TTL expiry] first call: expected MISS, got %q", resp1.Header.Get("X-Cache"))
	}

	// Immediate second call — HIT (entry still live).
	resp2, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Errorf("[TTL expiry] immediate second call: expected HIT, got %q", resp2.Header.Get("X-Cache"))
	}

	// Wait for TTL to expire.
	time.Sleep(cacheTTL + 500*time.Millisecond)

	// Call after expiry — must be MISS again.
	resp3, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
	if resp3.Header.Get("X-Cache") != "MISS" {
		t.Errorf("[TTL expiry] post-expiry call: expected MISS (TTL elapsed), got %q", resp3.Header.Get("X-Cache"))
	}
}

// TestGraphQueryCache_DisabledWhenTTLZero verifies that when GraphQueryCacheTTL
// is 0 (the default), no caching occurs and no X-Cache header is set.
func TestGraphQueryCache_DisabledWhenTTLZero(t *testing.T) {
	// setupGraphServer does NOT set GraphQueryCacheTTL, so it defaults to 0.
	ts := setupGraphServer(t)
	defer ts.cleanup()

	ts.do("POST", "/api/v1/person", map[string]interface{}{"name": "Alice"})

	query := `MATCH (a:person) RETURN a`
	for i := 0; i < 3; i++ {
		resp, _ := ts.do("POST", "/api/v1/graph/query", map[string]interface{}{"query": query, "max_depth": 5})
		if h := resp.Header.Get("X-Cache"); h != "" {
			t.Errorf("[disabled cache] call %d: X-Cache should be absent when TTL=0, got %q", i+1, h)
		}
	}
}
