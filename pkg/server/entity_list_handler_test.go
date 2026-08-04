// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

type entityListResponse struct {
	Count    int `json:"count"`
	Entities []struct {
		EntityType string   `json:"entity_type"`
		Count      int64    `json:"count"`
		HasSchema  bool     `json:"has_schema"`
		Adapted    bool     `json:"adapted"`
		Columns    []string `json:"columns"`
		Indexes    []struct {
			Name    string   `json:"name"`
			Columns []string `json:"columns"`
			Unique  bool     `json:"unique"`
		} `json:"indexes"`
		Graph *struct {
			OutEdges          int64    `json:"out_edges"`
			InEdges           int64    `json:"in_edges"`
			RelationshipTypes []string `json:"relationship_types"`
		} `json:"graph"`
		FirstSeen  string `json:"first_seen"`
		LastUpdate string `json:"last_update"`
	} `json:"entities"`
}

func findEntity(resp *entityListResponse, entityType string) *struct {
	EntityType string   `json:"entity_type"`
	Count      int64    `json:"count"`
	HasSchema  bool     `json:"has_schema"`
	Adapted    bool     `json:"adapted"`
	Columns    []string `json:"columns"`
	Indexes    []struct {
		Name    string   `json:"name"`
		Columns []string `json:"columns"`
		Unique  bool     `json:"unique"`
	} `json:"indexes"`
	Graph *struct {
		OutEdges          int64    `json:"out_edges"`
		InEdges           int64    `json:"in_edges"`
		RelationshipTypes []string `json:"relationship_types"`
	} `json:"graph"`
	FirstSeen  string `json:"first_seen"`
	LastUpdate string `json:"last_update"`
} {
	for i := range resp.Entities {
		if resp.Entities[i].EntityType == entityType {
			return &resp.Entities[i]
		}
	}
	return nil
}

// TestListEntities_MixedSchemalessAndAdapted is a direct regression
// test for a real bug: adapted entity types never get a row in the
// generic nodes table (SQLiteStore.createInner's own comment: "Insert
// entity: adapted table or blob"), so an implementation that only
// GROUPs BY entity_type on nodes silently omits every adapted entity
// type entirely. Caught by seeding one of each and checking both
// appear -- an implementation with this bug would show only "widgets"
// here, not "gizmos".
func TestListEntities_MixedSchemalessAndAdapted(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	ts.doRequest("POST", "/api/v1/widgets", map[string]interface{}{"name": "gadget-1"})
	ts.doRequest("POST", "/api/v1/widgets", map[string]interface{}{"name": "gadget-2"})

	schemaResp, _ := ts.doRequest("POST", "/api/v1/schema/gizmos", map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"sku": map[string]interface{}{"type": "string"}},
		"required":   []string{"sku"},
	})
	if schemaResp.StatusCode != http.StatusCreated {
		t.Fatalf("schema registration: status %d", schemaResp.StatusCode)
	}
	ts.doRequest("POST", "/api/v1/gizmos", map[string]interface{}{"sku": "G-1"})

	resp, body := ts.doRequest("GET", "/api/v1/entities", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var out entityListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}

	widgets := findEntity(&out, "widgets")
	if widgets == nil {
		t.Fatal("widgets missing from the listing")
	}
	if widgets.Count != 2 || widgets.HasSchema || widgets.Adapted {
		t.Errorf("widgets: got count=%d has_schema=%v adapted=%v, want count=2 has_schema=false adapted=false",
			widgets.Count, widgets.HasSchema, widgets.Adapted)
	}
	if widgets.FirstSeen == "" {
		t.Error("widgets: expected FirstSeen to be populated for a blob-stored entity")
	}

	gizmos := findEntity(&out, "gizmos")
	if gizmos == nil {
		t.Fatal("gizmos missing from the listing -- this is exactly the bug this test guards against")
	}
	if gizmos.Count != 1 || !gizmos.HasSchema || !gizmos.Adapted {
		t.Errorf("gizmos: got count=%d has_schema=%v adapted=%v, want count=1 has_schema=true adapted=true",
			gizmos.Count, gizmos.HasSchema, gizmos.Adapted)
	}
	if len(gizmos.Columns) == 0 {
		t.Error("gizmos: expected Columns to be populated for an adapted entity")
	}
	found := false
	for _, c := range gizmos.Columns {
		if c == "sku" {
			found = true
		}
	}
	if !found {
		t.Errorf("gizmos: expected 'sku' among Columns, got %v", gizmos.Columns)
	}
}

// TestListEntities_LiteralEntityTypeNamedEntity is the adversarial
// routing test: /entities (this endpoint) and /{entity}/{id} (the
// existing get-by-id route) are the same path shape one segment
// shorter/longer respectively -- if a naive route registration let
// /entities collide with /{entity}, a real entity type named "entity"
// would be indistinguishable from a request for this listing. Proves
// both resolve independently and correctly.
func TestListEntities_LiteralEntityTypeNamedEntity(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	createResp, createBody := ts.doRequest("POST", "/api/v1/entity", map[string]interface{}{"probe": true})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d: %s", createResp.StatusCode, createBody)
	}

	listResp, listBody := ts.doRequest("GET", "/api/v1/entities", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d: %s", listResp.StatusCode, listBody)
	}
	var out entityListResponse
	if err := json.Unmarshal(listBody, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, listBody)
	}
	entityEntry := findEntity(&out, "entity")
	if entityEntry == nil {
		t.Fatal("the literal 'entity' type is missing from /entities' own listing")
	}
	if entityEntry.Count != 1 {
		t.Errorf("entity type 'entity': got count %d, want 1", entityEntry.Count)
	}

	getResp, getBody := ts.doRequest("GET", "/api/v1/entity/1", nil)
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("/entity/1 should still resolve to the get-by-id route, independent of /entities: status %d: %s",
			getResp.StatusCode, getBody)
	}
	var entity map[string]interface{}
	if err := json.Unmarshal(getBody, &entity); err != nil {
		t.Fatalf("unmarshal entity: %v", err)
	}
	if entity["probe"] != true {
		t.Errorf("expected the actually-created entity back, got %v", entity)
	}
}

// TestListEntities_GraphFootprintOptIn proves the graph field is
// absent by default and populated correctly (with real edge counts
// and relationship names) only when ?include_graph=true is passed.
func TestListEntities_GraphFootprintOptIn(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	ts.doRequest("POST", "/api/v1/authors", map[string]interface{}{"name": "author-1"})
	ts.doRequest("POST", "/api/v1/books", map[string]interface{}{
		"title":  "book-1",
		"author": map[string]interface{}{"type": "REF", "entity": "authors", "id": 1},
	})
	ts.doRequest("POST", "/api/v1/books", map[string]interface{}{
		"title":  "book-2",
		"author": map[string]interface{}{"type": "REF", "entity": "authors", "id": 1},
	})

	withoutResp, withoutBody := ts.doRequest("GET", "/api/v1/entities", nil)
	if withoutResp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", withoutResp.StatusCode, withoutBody)
	}
	var without entityListResponse
	json.Unmarshal(withoutBody, &without)
	if a := findEntity(&without, "authors"); a != nil && a.Graph != nil {
		t.Error("Graph should be nil/absent when include_graph is not set")
	}

	withResp, withBody := ts.doRequest("GET", "/api/v1/entities?include_graph=true", nil)
	if withResp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", withResp.StatusCode, withBody)
	}
	var with entityListResponse
	if err := json.Unmarshal(withBody, &with); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, withBody)
	}

	authors := findEntity(&with, "authors")
	if authors == nil || authors.Graph == nil {
		t.Fatal("authors: expected a populated Graph field with include_graph=true")
	}
	if authors.Graph.InEdges != 2 || authors.Graph.OutEdges != 0 {
		t.Errorf("authors graph: got in=%d out=%d, want in=2 out=0", authors.Graph.InEdges, authors.Graph.OutEdges)
	}

	books := findEntity(&with, "books")
	if books == nil || books.Graph == nil {
		t.Fatal("books: expected a populated Graph field with include_graph=true")
	}
	if books.Graph.OutEdges != 2 || books.Graph.InEdges != 0 {
		t.Errorf("books graph: got out=%d in=%d, want out=2 in=0", books.Graph.OutEdges, books.Graph.InEdges)
	}
	if len(books.Graph.RelationshipTypes) != 1 || books.Graph.RelationshipTypes[0] != "author" {
		t.Errorf("books relationship types: got %v, want [\"author\"]", books.Graph.RelationshipTypes)
	}
}

func TestListEntities_EmptyTenant(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	resp, body := ts.doRequest("GET", "/api/v1/entities", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var out entityListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}
	if out.Count != 0 || len(out.Entities) != 0 {
		t.Errorf("expected an empty listing, got count=%d entities=%v", out.Count, out.Entities)
	}
}
