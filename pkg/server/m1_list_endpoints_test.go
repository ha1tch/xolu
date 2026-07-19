// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// m1_list_endpoints_test.go — M1 (molu readiness): the two enumeration
// endpoints filed as T-24 and T-25.
//
//   GET /api/v1/schemas          — entity types with a registered schema
//   GET /api/v2/.../gen/seq      — the tenant's named sequences
//   GET /api/v2/.../seq          — permanent alias, same handler
//
// Includes a regression pinning chi's static-over-wildcard precedence:
// before T-25, GET /gen/seq fell through to GET /gen/{type} as
// type="seq" and could never reach a sequence listing.

import (
	"fmt"
	"net/http"
	"testing"
)

// ─── T-24: GET /api/v1/schemas ───────────────────────────────────────────────

func TestSchemasList_Empty(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/schemas", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("list schemas: want 200, got %d: %v", status, resp)
	}
	schemas, ok := resp["schemas"].([]interface{})
	if !ok {
		t.Fatalf("schemas: want array, got %T", resp["schemas"])
	}
	if len(schemas) != 0 {
		t.Errorf("schemas: want empty on fresh server, got %v", schemas)
	}
	if got := resp["count"]; got != float64(0) {
		t.Errorf("count: want 0, got %v", got)
	}
}

func TestSchemasList_AfterCreate(t *testing.T) {
	env := newV2Server(t)
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
	}
	for _, entity := range []string{"widget", "asset"} {
		status, resp := doJSONRequest(t, "POST",
			fmt.Sprintf("%s/api/v1/schema/%s", env.ts.URL, entity), schema)
		if status != http.StatusCreated {
			t.Fatalf("create schema %s: want 201, got %d: %v", entity, status, resp)
		}
	}

	status, resp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/schemas", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("list schemas: want 200, got %d: %v", status, resp)
	}
	schemas, _ := resp["schemas"].([]interface{})
	if len(schemas) != 2 {
		t.Fatalf("schemas: want 2 entries, got %d: %v", len(schemas), schemas)
	}
	// Sorted output: asset before widget.
	first, _ := schemas[0].(map[string]interface{})
	second, _ := schemas[1].(map[string]interface{})
	if first["name"] != "asset" || second["name"] != "widget" {
		t.Errorf("schemas: want sorted [asset widget], got [%v %v]",
			first["name"], second["name"])
	}
	if got := resp["count"]; got != float64(2) {
		t.Errorf("count: want 2, got %v", got)
	}
}

// ─── T-25: GET /gen/seq and the /seq alias ───────────────────────────────────

func TestSeqList_Empty(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET", seqURL(env, ""), nil)
	if status != http.StatusOK {
		t.Fatalf("list sequences: want 200, got %d: %v", status, resp)
	}
	seqs, ok := resp["sequences"].([]interface{})
	if !ok {
		t.Fatalf("sequences: want array, got %T", resp["sequences"])
	}
	if len(seqs) != 0 {
		t.Errorf("sequences: want empty on fresh server, got %v", seqs)
	}
}

func TestSeqList_AfterDefine(t *testing.T) {
	env := newV2Server(t)
	for _, name := range []string{"orders", "invoices"} {
		status, resp := doJSONRequest(t, "POST", seqURL(env, ""),
			map[string]interface{}{"name": name, "increment_by": 2})
		if status != http.StatusCreated {
			t.Fatalf("define %s: want 201, got %d: %v", name, status, resp)
		}
	}
	// Advance one so current differs between the two.
	if status, resp := doJSONRequest(t, "GET", seqURL(env, "/orders/next"), nil); status != http.StatusOK {
		t.Fatalf("next: want 200, got %d: %v", status, resp)
	}

	status, resp := doJSONRequest(t, "GET", seqURL(env, ""), nil)
	if status != http.StatusOK {
		t.Fatalf("list sequences: want 200, got %d: %v", status, resp)
	}
	seqs, _ := resp["sequences"].([]interface{})
	if len(seqs) != 2 {
		t.Fatalf("sequences: want 2, got %d: %v", len(seqs), seqs)
	}
	// Sorted by name: invoices, orders.
	first, _ := seqs[0].(map[string]interface{})
	second, _ := seqs[1].(map[string]interface{})
	if first["name"] != "invoices" || second["name"] != "orders" {
		t.Fatalf("sequences: want sorted [invoices orders], got [%v %v]",
			first["name"], second["name"])
	}
	if first["increment_by"] != float64(2) {
		t.Errorf("invoices increment_by: want 2, got %v", first["increment_by"])
	}
	if _, ok := first["cycle"].(bool); !ok {
		t.Errorf("cycle: want bool, got %T", first["cycle"])
	}
	if second["current"] == first["current"] {
		t.Errorf("current: orders advanced once, want values to differ; both %v",
			first["current"])
	}
}

func TestSeqList_AliasMatchesPrimary(t *testing.T) {
	env := newV2Server(t)
	if status, resp := doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "only"}); status != http.StatusCreated {
		t.Fatalf("define: want 201, got %d: %v", status, resp)
	}
	sPrim, primary := doJSONRequest(t, "GET", seqURL(env, ""), nil)
	sAlias, alias := doJSONRequest(t, "GET", seqAliasURL(env, ""), nil)
	if sPrim != http.StatusOK || sAlias != http.StatusOK {
		t.Fatalf("want 200/200, got %d/%d", sPrim, sAlias)
	}
	if fmt.Sprint(primary["sequences"]) != fmt.Sprint(alias["sequences"]) {
		t.Errorf("alias divergence:\n primary: %v\n alias:   %v",
			primary["sequences"], alias["sequences"])
	}
}

// Regression: the static GET /gen/seq route must shadow GET /gen/{type}.
// If the static route is lost, this request reaches handleGenList with
// type="seq", which rejects it — so a non-200 here means the shadowing
// broke, not that listing was never wired.
func TestSeqList_StaticRouteShadowsGenWildcard(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET", seqURL(env, ""), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /gen/seq fell through to /gen/{type}: got %d: %v", status, resp)
	}
	if _, ok := resp["sequences"]; !ok {
		t.Fatalf("response is not the sequence-list envelope: %v", resp)
	}
}
