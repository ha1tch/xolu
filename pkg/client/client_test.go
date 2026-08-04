package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockServer creates a test server that simulates xolu API responses.
func mockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func TestNew(t *testing.T) {
	client := New("http://localhost:9090")
	
	if client.baseURL != "http://localhost:9090" {
		t.Errorf("expected baseURL to be http://localhost:9090, got %s", client.baseURL)
	}
	
	if client.httpClient == nil {
		t.Error("expected httpClient to be set")
	}
	
	if client.tenantID != "" {
		t.Error("expected tenantID to be empty by default")
	}
}

func TestNewWithOptions(t *testing.T) {
	customClient := &http.Client{Timeout: 60 * time.Second}
	
	client := New("http://localhost:9090",
		WithHTTPClient(customClient),
		WithTenant("tenant-123"),
	)
	
	if client.httpClient != customClient {
		t.Error("expected custom HTTP client to be set")
	}
	
	if client.tenantID != "tenant-123" {
		t.Errorf("expected tenantID to be tenant-123, got %s", client.tenantID)
	}
}

func TestWithTenantContext(t *testing.T) {
	client := New("http://localhost:9090")
	tenantClient := client.WithTenantContext("tenant-456")
	
	if tenantClient.tenantID != "tenant-456" {
		t.Errorf("expected tenantID to be tenant-456, got %s", tenantClient.tenantID)
	}
	
	// Original client should be unchanged
	if client.tenantID != "" {
		t.Error("original client should not be modified")
	}
}

func TestBuildURL(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
		path     string
		expected string
	}{
		{
			name:     "without tenant",
			tenantID: "",
			path:     "/assets",
			expected: "http://localhost:9090/api/v1/assets",
		},
		{
			name:     "with tenant",
			tenantID: "tenant-123",
			path:     "/assets",
			expected: "http://localhost:9090/api/v1/tenant/tenant-123/assets",
		},
		{
			name:     "with tenant and id",
			tenantID: "tenant-123",
			path:     "/assets/42",
			expected: "http://localhost:9090/api/v1/tenant/tenant-123/assets/42",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := New("http://localhost:9090", WithTenant(tt.tenantID))
			got := client.buildURL(tt.path)
			if got != tt.expected {
				t.Errorf("buildURL(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/assets" {
			t.Errorf("expected /api/v1/assets, got %s", r.URL.Path)
		}
		
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"message": "Resource of entity assets created successfully",
			"id":      1,
		})
	})
	defer server.Close()
	
	client := New(server.URL)
	entity, err := client.Create(context.Background(), "assets", map[string]any{
		"name":   "Test Asset",
		"status": "active",
	})
	
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	
	if entity.ID != 1 {
		t.Errorf("expected ID 1, got %d", entity.ID)
	}
	// Data is nil after Create — xolu does not echo the document on creation.
	if entity.Data != nil {
		t.Errorf("expected Data to be nil after Create, got %v", entity.Data)
	}
}

func TestGet(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/assets/42" {
			t.Errorf("expected /api/v1/assets/42, got %s", r.URL.Path)
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":         42,
			"name":       "Test Asset",
			"status":     "active",
			"created_at": "2025-01-01T00:00:00Z",
			"updated_at": "2025-01-02T00:00:00Z",
		})
	})
	defer server.Close()
	
	client := New(server.URL)
	entity, err := client.Get(context.Background(), "assets", 42)
	
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	
	if entity.ID != 42 {
		t.Errorf("expected ID 42, got %d", entity.ID)
	}
	
	if entity.Data["name"] != "Test Asset" {
		t.Errorf("expected name 'Test Asset', got %v", entity.Data["name"])
	}
	if entity.CreatedAt.IsZero() {
		t.Errorf("expected CreatedAt to be parsed from flat doc")
	}
}

func TestUpdate(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message": "Resource of entity assets with id 42 updated successfully",
		})
	})
	defer server.Close()
	
	client := New(server.URL)
	entity, err := client.Update(context.Background(), "assets", 42, map[string]any{
		"name":   "Updated Asset",
		"status": "inactive",
	})
	
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	
	if entity.ID != 42 {
		t.Errorf("expected ID 42, got %d", entity.ID)
	}
	// Data is nil after Update — xolu does not echo the document on writes.
	if entity.Data != nil {
		t.Errorf("expected Data to be nil after Update, got %v", entity.Data)
	}
}

func TestDelete(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/assets/42" {
			t.Errorf("expected /api/v1/assets/42, got %s", r.URL.Path)
		}
		
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()
	
	client := New(server.URL)
	err := client.Delete(context.Background(), "assets", 42)
	
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestPatch(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/assets/42" {
			t.Errorf("expected /api/v1/assets/42, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message": "Resource of entity assets with id 42 updated successfully",
		})
	})
	defer server.Close()

	client := New(server.URL)
	entity, err := client.Patch(context.Background(), "assets", 42, map[string]any{
		"status": "inactive",
	})
	if err != nil {
		t.Fatalf("Patch failed: %v", err)
	}
	if entity.ID != 42 {
		t.Errorf("expected ID 42, got %d", entity.ID)
	}
	// Data is nil after Patch — xolu does not echo the document on writes.
	if entity.Data != nil {
		t.Errorf("expected Data to be nil after Patch, got %v", entity.Data)
	}
}

func TestError(t *testing.T) {
	err := &Error{StatusCode: 404, Message: "entity not found"}
	got := err.Error()
	if got != "xolu: entity not found (status 404)" {
		t.Errorf("unexpected error string: %s", got)
	}

	errNoMsg := &Error{StatusCode: 500}
	got2 := errNoMsg.Error()
	if got2 != "xolu: request failed with status 500" {
		t.Errorf("unexpected error string: %s", got2)
	}
}

func TestList(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		
		// Check query params — client maps Limit->per_page, Offset->page
		if r.URL.Query().Get("per_page") != "10" {
			t.Errorf("expected per_page=10, got %s", r.URL.Query().Get("per_page"))
		}
		if r.URL.Query().Get("page") != "3" {
			t.Errorf("expected page=3 (offset 20 / limit 10 + 1), got %s", r.URL.Query().Get("page"))
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": float64(1), "name": "Asset 1"},
				{"id": float64(2), "name": "Asset 2"},
			},
			"pagination": map[string]any{
				"page": 3, "per_page": 10,
				"total_items": 25, "total_pages": 3,
			},
		})
	})
	defer server.Close()
	
	client := New(server.URL)
	entities, err := client.List(context.Background(), "assets", &ListParams{
		Limit:  10,
		Offset: 20,
	})
	
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	
	if len(entities.Entities) != 2 {
		t.Errorf("expected 2 entities, got %d", len(entities.Entities))
	}
	if entities.TotalItems != 25 {
		t.Errorf("expected TotalItems=25, got %d", entities.TotalItems)
	}
}

func TestSearch(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		// xolu endpoint is /search, not /{entity}/search
		if r.URL.Path != "/api/v1/search" {
			t.Errorf("expected /api/v1/search, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "test query" {
			t.Errorf("expected q='test query', got %s", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("entity") != "assets" {
			t.Errorf("expected entity=assets, got %s", r.URL.Query().Get("entity"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"query":   "test query",
			"entity":  "assets",
			"count":   1,
			"results": []map[string]any{
				{"id": float64(1), "name": "Test Asset"},
			},
		})
	})
	defer server.Close()

	client := New(server.URL)
	entities, err := client.Search(context.Background(), "assets", SearchParams{
		Query: "test query",
		Limit: 10,
	})

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(entities))
	}
	if entities[0].Data["name"] != "Test Asset" {
		t.Errorf("expected name Test Asset, got %v", entities[0].Data["name"])
	}
}

func TestAuthHeader(t *testing.T) {
	var capturedAuth string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"message": "created", "id": 1})
	})
	defer server.Close()

	// Without API key: no Authorization header.
	clientNoAuth := New(server.URL)
	clientNoAuth.Create(context.Background(), "assets", map[string]any{"name": "x"})
	if capturedAuth != "" {
		t.Errorf("expected no Authorization header without API key, got %q", capturedAuth)
	}

	// With API key: Authorization: ApiKey <key> -- NOT "Bearer". T-160
	// (2026-08-04, reported by the xoluman team): this assertion used
	// to check for "Bearer test-key-abc", actively enshrining a real
	// bug as expected behaviour -- the mock server here just captures
	// whatever the client sends, so a wrong assertion stayed green
	// indefinitely. Confirmed directly against the real server's own
	// pkg/authmw.validateAPIKey before fixing this: it never accepts
	// a Bearer-prefixed key for apikey auth type.
	clientWithAuth := New(server.URL, WithAPIKey("test-key-abc"))
	clientWithAuth.Create(context.Background(), "assets", map[string]any{"name": "x"})
	if capturedAuth != "ApiKey test-key-abc" {
		t.Errorf("expected Authorization: ApiKey test-key-abc, got %q", capturedAuth)
	}
}

func TestOQL(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/oql/query" {
			t.Errorf("expected /api/v1/oql/query, got %s", r.URL.Path)
		}
		
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		
		if body["query"] != "SELECT * FROM assets WHERE status = 'active'" {
			t.Errorf("unexpected query: %s", body["query"])
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"data": []map[string]any{
				{"id": 1, "name": "Asset 1", "status": "active"},
				{"id": 2, "name": "Asset 2", "status": "active"},
			},
			"stats": map[string]any{
				"rows_scanned": 10,
				"rows_returned": 2,
				"execution_time_ms": 5,
			},
		})
	})
	defer server.Close()
	
	client := New(server.URL)
	result, err := client.OQL(context.Background(), "SELECT * FROM assets WHERE status = 'active'")
	
	if err != nil {
		t.Fatalf("OQL failed: %v", err)
	}
	
	if result.Stats.RowsReturned != 2 {
		t.Errorf("expected rows_returned 2, got %d", result.Stats.RowsReturned)
	}
	
	if len(result.Data) != 2 {
		t.Errorf("expected 2 data rows, got %d", len(result.Data))
	}
}

func TestGraphQuery(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/graph/query" {
			t.Errorf("expected /api/v1/graph/query, got %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["query"] == nil {
			t.Errorf("expected query field in request body")
		}
		w.Header().Set("Content-Type", "application/json")
		// xolu graph/query response uses "result", not "data"
		json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"result": []map[string]any{
				{"from": "assets:1", "to": "assets:2", "via": "sensors:5"},
			},
			"stats": map[string]any{
				"nodes_traversed":   3,
				"paths_found":       1,
				"execution_time_ms": 2,
			},
		})
	})
	defer server.Close()

	client := New(server.URL)
	result, err := client.GraphQuery(context.Background(), "assets:1 -[*1..3]-> assets:*", 3)

	if err != nil {
		t.Fatalf("GraphQuery failed: %v", err)
	}
	if result.Stats.PathsFound != 1 {
		t.Errorf("expected paths_found 1, got %d", result.Stats.PathsFound)
	}
	if len(result.Result) != 1 {
		t.Errorf("expected 1 result row, got %d", len(result.Result))
	}
}

func TestGraphNeighbors(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/graph/neighbors" {
			t.Errorf("expected /api/v1/graph/neighbors, got %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["node_id"] != "assets:42" {
			t.Errorf("expected node_id=assets:42, got %v", body["node_id"])
		}
		if body["direction"] != "both" {
			t.Errorf("expected direction=both, got %v", body["direction"])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"neighbors": map[string]any{
				"outgoing": map[string]any{"locations:5": "location_ref"},
				"incoming": map[string]any{"events:12": "asset_ref"},
			},
		})
	})
	defer server.Close()

	client := New(server.URL)
	result, err := client.GraphNeighbors(context.Background(), "assets:42", "both")

	if err != nil {
		t.Fatalf("GraphNeighbors failed: %v", err)
	}
	if result.Outgoing["locations:5"] != "location_ref" {
		t.Errorf("expected outgoing locations:5=location_ref, got %v", result.Outgoing)
	}
	if result.Incoming["events:12"] != "asset_ref" {
		t.Errorf("expected incoming events:12=asset_ref, got %v", result.Incoming)
	}
}

func TestGraphShortestPath(t *testing.T) {
	t.Run("path found", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/api/v1/graph/shortestPath" {
				t.Errorf("expected /api/v1/graph/shortestPath, got %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"from":   "assets:1",
				"to":     "locations:5",
				"exists": true,
				"path":   []string{"assets:1", "locations:5"},
				"length": 1,
			})
		})
		defer server.Close()

		client := New(server.URL)
		result, err := client.GraphShortestPath(context.Background(), "assets:1", "locations:5", 10)

		if err != nil {
			t.Fatalf("GraphShortestPath failed: %v", err)
		}
		if !result.Exists {
			t.Errorf("expected path to exist")
		}
		if result.Length != 1 {
			t.Errorf("expected length 1, got %d", result.Length)
		}
		if len(result.Path) != 2 {
			t.Errorf("expected 2 nodes in path, got %d", len(result.Path))
		}
	})

	t.Run("no path", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"from": "assets:1", "to": "assets:99",
				"exists": false, "path": nil, "length": 0,
			})
		})
		defer server.Close()

		client := New(server.URL)
		result, err := client.GraphShortestPath(context.Background(), "assets:1", "assets:99", 0)

		if err != nil {
			t.Fatalf("GraphShortestPath failed: %v", err)
		}
		if result.Exists {
			t.Errorf("expected no path to exist")
		}
	})
}

func TestWithTenantID(t *testing.T) {
	var capturedPath string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": float64(1), "name": "x"})
	})
	defer server.Close()

	// uint16(1) should format as "0001" in the URL
	client := New(server.URL, WithTenantID(1))
	client.Get(context.Background(), "assets", 42)
	if capturedPath != "/api/v1/tenant/0001/assets/42" {
		t.Errorf("expected /api/v1/tenant/0001/assets/42, got %s", capturedPath)
	}

	// uint16(255) should format as "00FF"
	client2 := New(server.URL, WithTenantID(255))
	client2.Get(context.Background(), "assets", 1)
	if capturedPath != "/api/v1/tenant/00FF/assets/1" {
		t.Errorf("expected /api/v1/tenant/00FF/assets/1, got %s", capturedPath)
	}
}

func TestErrorHandling(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"error": "entity not found",
		})
	})
	defer server.Close()
	
	client := New(server.URL)
	_, err := client.Get(context.Background(), "assets", 999)
	
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	
	oluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	
	if oluErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", oluErr.StatusCode)
	}
	
	if oluErr.Message != "entity not found" {
		t.Errorf("expected message 'entity not found', got %s", oluErr.Message)
	}
}

func TestSave(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/api/v1/assets/save/42" {
				t.Errorf("expected /api/v1/assets/save/42, got %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"created": true, "id": float64(42)})
		})
		defer server.Close()

		client := New(server.URL)
		created, err := client.Save(context.Background(), "assets", 42, map[string]any{"name": "Device A"})
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
		if !created {
			t.Errorf("expected created=true")
		}
	})

	t.Run("replaced", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"created": false, "id": float64(42)})
		})
		defer server.Close()

		client := New(server.URL)
		created, err := client.Save(context.Background(), "assets", 42, map[string]any{"name": "Device A"})
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
		if created {
			t.Errorf("expected created=false on replace")
		}
	})
}

func TestCommit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/api/v1/commit" {
				t.Errorf("expected /api/v1/commit, got %s", r.URL.Path)
			}
			var body CommitRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode body: %v", err)
			}
			if body.Update.Entity != "assets" {
				t.Errorf("expected update entity=assets, got %s", body.Update.Entity)
			}
			if len(body.Append) != 1 {
				t.Errorf("expected 1 append, got %d", len(body.Append))
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"update": map[string]any{
					"entity":  "assets",
					"id":      float64(7),
					"created": false,
					"version": float64(3),
				},
				"appended": []map[string]any{
					{"entity": "events", "id": float64(99)},
				},
			})
		})
		defer server.Close()

		ver := 2
		client := New(server.URL)
		result, err := client.Commit(context.Background(), CommitRequest{
			Update: CommitUpdate{
				Entity:  "assets",
				ID:      7,
				Version: &ver,
				Data:    map[string]any{"status": "active"},
			},
			Append: []CommitAppend{
				{Entity: "events", Data: map[string]any{"type": "status_change"}},
			},
		})
		if err != nil {
			t.Fatalf("Commit failed: %v", err)
		}
		if result.Update.ID != 7 {
			t.Errorf("expected update ID 7, got %d", result.Update.ID)
		}
		if result.Update.Version != 3 {
			t.Errorf("expected version 3, got %d", result.Update.Version)
		}
		if len(result.Appended) != 1 {
			t.Errorf("expected 1 appended, got %d", len(result.Appended))
		}
		if result.Appended[0].ID != 99 {
			t.Errorf("expected appended ID 99, got %d", result.Appended[0].ID)
		}
	})

	t.Run("version conflict", func(t *testing.T) {
		server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "version_conflict",
					"message": "version conflict: asset 7 modified by another request",
					"status":  float64(409),
				},
			})
		})
		defer server.Close()

		ver := 1
		client := New(server.URL)
		_, err := client.Commit(context.Background(), CommitRequest{
			Update: CommitUpdate{Entity: "assets", ID: 7, Version: &ver, Data: map[string]any{}},
			Append: []CommitAppend{{Entity: "events", Data: map[string]any{}}},
		})
		if err == nil {
			t.Errorf("expected error on version conflict, got nil")
		}
		oluErr, ok := err.(*Error)
		if !ok {
			t.Errorf("expected *xolu.Error, got %T", err)
		} else if oluErr.StatusCode != http.StatusConflict {
			t.Errorf("expected status 409, got %d", oluErr.StatusCode)
		}
	})
}

func TestHealth(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("expected /health, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()
	
	client := New(server.URL)
	err := client.Health(context.Background())
	
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
}

func TestHealthFailure(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	defer server.Close()
	
	client := New(server.URL)
	err := client.Health(context.Background())
	
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ----------------------------------------------------------------------------
// Stage 1 — auth modes, structured errors, Ready()
// ----------------------------------------------------------------------------

func TestAuthNoneSendsNoAuthorizationHeader(t *testing.T) {
	var got string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
	defer server.Close()

	c := New(server.URL)
	_, _ = c.Get(context.Background(), "users", 1)

	if got != "" {
		t.Errorf("expected no Authorization header, got %q", got)
	}
}

func TestWithAPIKeySendsApiKeyScheme(t *testing.T) {
	// Renamed from TestWithAPIKeySendsBearer -- T-160 (2026-08-04):
	// that name and its own assertion both enshrined the actual bug.
	var got string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
	defer server.Close()

	c := New(server.URL, WithAPIKey("key-abc"))
	_, _ = c.Get(context.Background(), "users", 1)

	if got != "ApiKey key-abc" {
		t.Errorf("expected Authorization %q, got %q", "ApiKey key-abc", got)
	}
	if c.authMode != AuthAPIKey {
		t.Errorf("expected authMode AuthAPIKey, got %v", c.authMode)
	}
}

func TestWithBearerTokenSendsBearer(t *testing.T) {
	var got string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
	defer server.Close()

	c := New(server.URL, WithBearerToken("tok-xyz"))
	_, _ = c.Get(context.Background(), "users", 1)

	if got != "Bearer tok-xyz" {
		t.Errorf("expected Authorization %q, got %q", "Bearer tok-xyz", got)
	}
	if c.authMode != AuthBearer {
		t.Errorf("expected authMode AuthBearer, got %v", c.authMode)
	}
}

func TestWithJWTSendsBearer(t *testing.T) {
	// A representative unsigned JWT header.payload.signature shape. The
	// client does not parse or validate the token; it emits it verbatim.
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.sig"
	var got string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
	defer server.Close()

	c := New(server.URL, WithJWT(jwt))
	_, _ = c.Get(context.Background(), "users", 1)

	want := "Bearer " + jwt
	if got != want {
		t.Errorf("expected Authorization %q, got %q", want, got)
	}
	if c.authMode != AuthJWT {
		t.Errorf("expected authMode AuthJWT, got %v", c.authMode)
	}
}

func TestLastAuthModeWins(t *testing.T) {
	c := New("http://example",
		WithAPIKey("first"),
		WithJWT("second"),
	)
	if c.authMode != AuthJWT {
		t.Errorf("expected AuthJWT after last-write-wins, got %v", c.authMode)
	}
	if got := c.authHeader(); got != "Bearer second" {
		t.Errorf("expected header %q, got %q", "Bearer second", got)
	}
}

func TestWithTenantContextPreservesAuth(t *testing.T) {
	c := New("http://example", WithAPIKey("key-1"))
	c2 := c.WithTenantContext("0002")

	if c2.authMode != AuthAPIKey {
		t.Errorf("expected authMode preserved, got %v", c2.authMode)
	}
	if c2.apiKey != "key-1" {
		t.Errorf("expected apiKey preserved, got %q", c2.apiKey)
	}
	if c2.tenantID != "0002" {
		t.Errorf("expected tenantID 0002, got %q", c2.tenantID)
	}
}

// Structured error parsing: xolu writes
//	{"error":{"code":"XOLU-ST001","message":"...","status":404}}
func TestStructuredErrorParsed(t *testing.T) {
	body := `{"error":{"code":"XOLU-ST001","message":"entity not found","status":404}}`
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(body))
	})
	defer server.Close()

	c := New(server.URL)
	_, err := c.Get(context.Background(), "users", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var xerr *Error
	if !errorsAs(err, &xerr) {
		t.Fatalf("expected *client.Error, got %T", err)
	}
	if xerr.Code != "XOLU-ST001" {
		t.Errorf("expected Code XOLU-ST001, got %q", xerr.Code)
	}
	if xerr.HTTPStatus != 404 {
		t.Errorf("expected HTTPStatus 404, got %d", xerr.HTTPStatus)
	}
	if xerr.StatusCode != 404 {
		t.Errorf("expected StatusCode 404 (deprecated alias), got %d", xerr.StatusCode)
	}
	if xerr.Message != "entity not found" {
		t.Errorf("expected message %q, got %q", "entity not found", xerr.Message)
	}
	if string(xerr.Detail) != body {
		t.Errorf("expected Detail to hold raw body, got %q", xerr.Detail)
	}
	// Error string carries the code:
	want := "xolu: XOLU-ST001: entity not found (status 404)"
	if got := xerr.Error(); got != want {
		t.Errorf("expected error string %q, got %q", want, got)
	}
}

// Legacy flat error parsing: earlier xolu / test servers write
//	{"error":"message","details":{...}}
func TestFlatErrorParsedForBackwardsCompat(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid payload","details":{"field":"email"}}`))
	})
	defer server.Close()

	c := New(server.URL)
	_, err := c.Get(context.Background(), "users", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var xerr *Error
	if !errorsAs(err, &xerr) {
		t.Fatalf("expected *client.Error, got %T", err)
	}
	if xerr.Code != "" {
		t.Errorf("expected empty Code for flat error, got %q", xerr.Code)
	}
	if xerr.Message != "invalid payload" {
		t.Errorf("expected message %q, got %q", "invalid payload", xerr.Message)
	}
	if xerr.Details["field"] != "email" {
		t.Errorf("expected Details[field]=email, got %v", xerr.Details["field"])
	}
}

// Non-JSON error body — server returned plain text or HTML.
func TestNonJSONErrorFallsBackToRawBody(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("gateway timeout"))
	})
	defer server.Close()

	c := New(server.URL)
	_, err := c.Get(context.Background(), "users", 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var xerr *Error
	if !errorsAs(err, &xerr) {
		t.Fatalf("expected *client.Error, got %T", err)
	}
	if xerr.Message != "gateway timeout" {
		t.Errorf("expected raw body as message, got %q", xerr.Message)
	}
}

// Ready() — 200 = ready.
func TestReadyOK(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			t.Errorf("expected /ready, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ready"}`))
	})
	defer server.Close()

	c := New(server.URL)
	if err := c.Ready(context.Background()); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// Ready() — 503 during initialisation or when storage is unreachable.
func TestReadyServiceUnavailable(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"initialising"}`))
	})
	defer server.Close()

	c := New(server.URL)
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected error to mention status 503, got %v", err)
	}
}

// Ready() should NOT send an Authorization header — /ready is deliberately
// unauthenticated so probes work without credentials.
func TestReadyDoesNotSendAuthHeader(t *testing.T) {
	var got string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	c := New(server.URL, WithAPIKey("should-not-appear"))
	_ = c.Ready(context.Background())

	if got != "" {
		t.Errorf("expected no Authorization header on /ready, got %q", got)
	}
}

// Small local errors.As shim so this test file does not need to import the
// stdlib errors package (which the rest of the file does not use). Behaves
// like errors.As for the shapes we return.
func errorsAs(err error, target **Error) bool {
	return errors.As(err, target)
}

// ─── TestConnection (T-161: Client.Health cannot verify a credential) ──────

func TestTestConnection_Success(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/schemas" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"count":0,"schemas":[]}`))
	})
	defer server.Close()

	c := New(server.URL)
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTestConnection_RejectedCredential(t *testing.T) {
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":"XOLU-AU001","message":"Authentication required","status":401}}`))
	})
	defer server.Close()

	c := New(server.URL, WithAPIKey("wrong-key"))
	err := c.TestConnection(context.Background())
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("HTTPStatus: got %d, want 401", xoluErr.HTTPStatus)
	}
}

func TestTestConnection_TenantConfigured_NotTenantPrefixed(t *testing.T) {
	// Same class of bug as T-158: must hit the global /schemas path,
	// not a tenant-prefixed one, so a connection can be tested before
	// a tenant is even chosen.
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/schemas" {
			t.Errorf("path should not be tenant-prefixed: got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"count":0,"schemas":[]}`))
	})
	defer server.Close()

	c := New(server.URL, WithTenant("acme"))
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTestConnection_UsesApiKeyScheme(t *testing.T) {
	var got string
	server := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"count":0,"schemas":[]}`))
	})
	defer server.Close()

	c := New(server.URL, WithAPIKey("test-key"))
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ApiKey test-key" {
		t.Errorf("Authorization: got %q, want %q", got, "ApiKey test-key")
	}
}
