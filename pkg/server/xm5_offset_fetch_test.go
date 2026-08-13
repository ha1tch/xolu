// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http"
	"testing"
)

// TestXM5_OffsetFetchNext_Applied is xoluman's own XM-5 report, end
// to end: OFFSET N ROWS FETCH NEXT M ROWS ONLY parsed correctly but
// was never applied anywhere -- stmt.Offset/stmt.Fetch were simply
// never read by the executor, so the clause silently vanished after
// parsing, returning every row regardless. Root cause confirmed and
// fixed in pkg/oql/aggregator.go's own new ApplyOffsetFetch, wired
// into executor.go's own post-processing pipeline; unit-tested there
// directly (TestApplyOffsetFetch). This test proves the fix at the
// level xoluman actually hit it -- a real OQL query against a real
// server, matching their own reported query shape exactly (5 rows,
// scaled down from their own 35 for a faster test, OFFSET 2 FETCH
// NEXT 2 to keep the boundary meaningfully non-trivial).
func TestXM5_OffsetFetchNext_Applied(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"first_name": map[string]interface{}{"type": "string"}},
	}
	status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/contacts", schema)
	if status != http.StatusCreated {
		t.Fatalf("schema registration: want 201, got %d %v", status, resp)
	}

	names := []string{"Lily", "James", "Sara", "Omar", "Nina"}
	for _, name := range names {
		status, resp := doJSONRequest(t, "POST", base+"/contacts", map[string]interface{}{"first_name": name})
		if status != http.StatusCreated {
			t.Fatalf("create contact %s: want 201, got %d %v", name, status, resp)
		}
	}

	query := `SELECT id, first_name FROM contacts ORDER BY id OFFSET 2 ROWS FETCH NEXT 2 ROWS ONLY`
	status, queryResp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
		map[string]interface{}{"query": query})
	if status != http.StatusOK {
		t.Fatalf("query: want 200, got %d %v (this is XM-5's own reported failure mode if non-200)", status, queryResp)
	}

	data, _ := queryResp["data"].([]interface{})
	if len(data) != 2 {
		t.Fatalf("want exactly 2 rows (OFFSET 2 FETCH NEXT 2, xoluman's own reported symptom was all 5), got %d: %v", len(data), data)
	}
	row0 := data[0].(map[string]interface{})
	row1 := data[1].(map[string]interface{})
	if row0["first_name"] != "Sara" || row1["first_name"] != "Omar" {
		t.Errorf("want rows 3-4 (Sara, Omar) per OFFSET 2, got %v, %v", row0, row1)
	}
}
