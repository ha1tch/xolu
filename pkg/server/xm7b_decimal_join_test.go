// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http"
	"testing"
)

// TestXM7b_DecimalThroughJoin is xoluman's own XM-7b report, end to
// end: a decimal field (deals.amount, scale 2) returned through a
// JOIN came back as its raw, scaled-integer stored form -- an exact
// x100 scale factor, decimal point gone -- while the identical field
// via a non-JOIN query was correctly formatted as a decimal string.
// Root cause confirmed and fixed in pkg/oql/sqlgen_join.go's own
// generateJoinSelectColumns (now tracks decimal columns, previously
// discarded the scale/isDecimal AdaptedColumnInfo already returned)
// and executor.go's own JOIN case (now calls
// denormaliseAggregateDecimals, the same function the adapted and
// aggregate paths already used, entirely unmodified). Unit-tested at
// the SQL-generation level directly
// (TestGenerateJoinSQL_DecimalColumns, pkg/oql). This test proves the
// fix at the level xoluman actually hit it -- a real OQL query,
// non-JOIN and JOIN side by side, against a real, schema-adapted
// tenant, matching their own exact reported value.
func TestXM7b_DecimalThroughJoin(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"amount": map[string]interface{}{
				"type":             "string",
				"format":           "decimal",
				"decimalPrecision": float64(10),
				"decimalScale":     float64(2),
			},
			"company_id": map[string]interface{}{"type": "number"},
		},
	}
	for _, entity := range []string{"deals", "companies"} {
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
		if status != http.StatusCreated {
			t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
		}
	}

	status, co := doJSONRequest(t, "POST", base+"/companies", map[string]interface{}{"name": "Summit Labs"})
	if status != http.StatusCreated {
		t.Fatalf("create company: want 201, got %d %v", status, co)
	}
	// xoluman's own exact reported value.
	status, deal := doJSONRequest(t, "POST", base+"/deals", map[string]interface{}{
		"name": "deal1", "amount": "333000.65", "company_id": co["id"],
	})
	if status != http.StatusCreated {
		t.Fatalf("create deal: want 201, got %d %v", status, deal)
	}

	// Non-JOIN: the already-correct baseline, confirmed still correct.
	status, plainResp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
		map[string]interface{}{"query": `SELECT id, amount FROM deals WHERE id = 1`})
	if status != http.StatusOK {
		t.Fatalf("non-JOIN query: want 200, got %d %v", status, plainResp)
	}
	plainData, _ := plainResp["data"].([]interface{})
	if len(plainData) != 1 || plainData[0].(map[string]interface{})["amount"] != "333000.65" {
		t.Fatalf("non-JOIN: want amount=\"333000.65\", got %v", plainData)
	}

	// JOIN: xoluman's own reported failure mode -- was 33300065 (int,
	// x100, decimal point gone), want the identical "333000.65".
	query := `SELECT a.id, a.amount, b.name FROM deals AS a INNER JOIN companies AS b ON a.company_id = b.id WHERE a.id = 1`
	status, joinResp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
		map[string]interface{}{"query": query})
	if status != http.StatusOK {
		t.Fatalf("JOIN query: want 200, got %d %v", status, joinResp)
	}
	joinData, _ := joinResp["data"].([]interface{})
	if len(joinData) != 1 {
		t.Fatalf("JOIN: want 1 row, got %d: %v", len(joinData), joinData)
	}
	row := joinData[0].(map[string]interface{})
	if row["amount"] != "333000.65" {
		t.Errorf("JOIN: want amount=\"333000.65\" (matching the non-JOIN query exactly), got %v (%T) -- full row: %v",
			row["amount"], row["amount"], row)
	}
}
