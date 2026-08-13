// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http"
	"testing"
)

// TestXM3a_JoinQuery_TenantIsolation is xoluman's own XM-3a report
// (XOT173), end to end: a plain, syntactically-correct JOIN query used
// to fail against any real tenant-scoped instance with "no such
// column: a.tenant_id" -- root cause was a copy-paste bug in
// GenerateJoinSQL emitting an identical tenant_id predicate for both
// adapted and blob sides, when adapted tables carry no such column at
// all (the per-tenant table name IS the scoping). Fixed in
// pkg/oql/sqlgen_join.go, unit-tested there directly
// (TestRegression_JoinTenantScoping_AdaptedSideOmitsColumn).
//
// This test proves the fix at the level xoluman actually hit it --
// two real tenants, same entity name, a real HTTP JOIN query -- and
// goes one step further than xoluman's own report asked for: genuine
// tenant isolation, not just "the query no longer errors." A fix that
// merely stopped referencing tenant_id without the per-tenant table
// name actually scoping correctly would make this query SUCCEED while
// silently leaking data across tenants -- worth proving directly
// rather than inferring from the absence of an error.
func TestXM3a_JoinQuery_TenantIsolation(t *testing.T) {
	env := newFullDxpServer(t)

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":       map[string]interface{}{"type": "string"},
			"company_id": map[string]interface{}{"type": "number"},
		},
	}
	for _, entity := range []string{"deals", "companies"} {
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
		if status != http.StatusCreated {
			t.Fatalf("schema registration for %s: want 201, got %d %v", entity, status, resp)
		}
	}

	seedTenant := func(tenantName, companyName, dealName string) {
		base := env.ts.URL + "/api/v1/tenant/" + tenantName
		status, coResp := doJSONRequest(t, "POST", base+"/companies", map[string]interface{}{"name": companyName})
		if status != http.StatusCreated {
			t.Fatalf("[%s] create company: want 201, got %d %v", tenantName, status, coResp)
		}
		status, dealResp := doJSONRequest(t, "POST", base+"/deals", map[string]interface{}{
			"name": dealName, "company_id": coResp["id"],
		})
		if status != http.StatusCreated {
			t.Fatalf("[%s] create deal: want 201, got %d %v", tenantName, status, dealResp)
		}
	}

	// Two tenants, same entity names, deliberately distinct data so any
	// cross-tenant leak would be immediately visible in the results.
	seedTenant("tenanta", "Acme", "Acme Renewal")
	seedTenant("tenantb", "Globex", "Globex Onboarding")

	query := `SELECT a.name, b.name FROM deals AS a INNER JOIN companies AS b ON a.company_id = b.id`

	status, respA := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/tenanta/oql/query",
		map[string]interface{}{"query": query})
	if status != http.StatusOK {
		t.Fatalf("tenanta JOIN query: want 200, got %d %v (this is XM-3a's own reported failure mode if non-200)", status, respA)
	}
	rowsA, _ := respA["data"].([]interface{})
	if len(rowsA) != 1 {
		t.Fatalf("tenanta: want exactly 1 row (its own single deal), got %d: %v", len(rowsA), respA)
	}
	rowA, _ := rowsA[0].(map[string]interface{})
	for _, v := range rowA {
		if v == "Globex" || v == "Globex Onboarding" {
			t.Fatalf("tenant isolation violated: tenanta's JOIN result contains tenantb's data: %v", rowA)
		}
	}

	status, respB := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/tenantb/oql/query",
		map[string]interface{}{"query": query})
	if status != http.StatusOK {
		t.Fatalf("tenantb JOIN query: want 200, got %d %v", status, respB)
	}
	rowsB, _ := respB["data"].([]interface{})
	if len(rowsB) != 1 {
		t.Fatalf("tenantb: want exactly 1 row (its own single deal), got %d: %v", len(rowsB), respB)
	}
	rowB, _ := rowsB[0].(map[string]interface{})
	for _, v := range rowB {
		if v == "Acme" || v == "Acme Renewal" {
			t.Fatalf("tenant isolation violated: tenantb's JOIN result contains tenanta's data: %v", rowB)
		}
	}
}
