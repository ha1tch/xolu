// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

// TestOQLUnion_EndToEnd is the missing verification for the UNION/
// INTERSECT/EXCEPT executor support (T-04): validator-level rejection
// coverage already existed (TestValidatorUnionChain, pkg/oql), but
// nothing had ever actually run a UNION query against real data and
// checked the result -- this is that test. Two blob entities, deals
// and archived_deals, each seeded independently, exercised through
// every operator the validator accepts.
func TestOQLUnion_EndToEnd(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
	}
	for _, entity := range []string{"deals", "archived_deals", "third_deals"} {
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
		if status != http.StatusCreated {
			t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
		}
	}

	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		status, resp := doJSONRequest(t, "POST", base+"/deals", map[string]interface{}{"name": name})
		if status != http.StatusCreated {
			t.Fatalf("create deal %s: want 201, got %d %v", name, status, resp)
		}
	}
	// One deliberately-overlapping name (Beta) to exercise dedup for
	// plain UNION and non-trivial results for INTERSECT/EXCEPT.
	for _, name := range []string{"Beta", "Delta"} {
		status, resp := doJSONRequest(t, "POST", base+"/archived_deals", map[string]interface{}{"name": name})
		if status != http.StatusCreated {
			t.Fatalf("create archived_deal %s: want 201, got %d %v", name, status, resp)
		}
	}

	query := func(q string) []interface{} {
		t.Helper()
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": q})
		if status != http.StatusOK {
			t.Fatalf("query %q: want 200, got %d %v", q, status, resp)
		}
		data, _ := resp["data"].([]interface{})
		return data
	}
	names := func(rows []interface{}) map[string]int {
		out := make(map[string]int, len(rows))
		for _, r := range rows {
			out[r.(map[string]interface{})["name"].(string)]++
		}
		return out
	}

	t.Run("UNION deduplicates the overlapping row", func(t *testing.T) {
		rows := query(`SELECT name FROM deals UNION SELECT name FROM archived_deals`)
		got := names(rows)
		want := map[string]int{"Alpha": 1, "Beta": 1, "Gamma": 1, "Delta": 1}
		if len(rows) != 4 {
			t.Fatalf("want 4 deduplicated rows, got %d: %v", len(rows), got)
		}
		for name, count := range want {
			if got[name] != count {
				t.Errorf("%s: want count %d, got %d (full: %v)", name, count, got[name], got)
			}
		}
	})

	t.Run("UNION ALL keeps the duplicate", func(t *testing.T) {
		rows := query(`SELECT name FROM deals UNION ALL SELECT name FROM archived_deals`)
		got := names(rows)
		if len(rows) != 5 {
			t.Fatalf("want 5 rows (duplicate kept), got %d: %v", len(rows), got)
		}
		if got["Beta"] != 2 {
			t.Errorf("want Beta counted twice (once per side), got %d", got["Beta"])
		}
	})

	t.Run("INTERSECT returns only the row present on both sides", func(t *testing.T) {
		rows := query(`SELECT name FROM deals INTERSECT SELECT name FROM archived_deals`)
		got := names(rows)
		if len(rows) != 1 || got["Beta"] != 1 {
			t.Fatalf("want exactly {Beta: 1}, got %v", got)
		}
	})

	t.Run("EXCEPT returns the left side's own rows not present on the right", func(t *testing.T) {
		rows := query(`SELECT name FROM deals EXCEPT SELECT name FROM archived_deals`)
		got := names(rows)
		want := map[string]int{"Alpha": 1, "Gamma": 1}
		if len(rows) != 2 {
			t.Fatalf("want 2 rows, got %d: %v", len(rows), got)
		}
		for name := range want {
			if got[name] != 1 {
				t.Errorf("want %s present exactly once, got %v", name, got)
			}
		}
	})

	t.Run("chained UNION across three branches", func(t *testing.T) {
		status, resp := doJSONRequest(t, "POST", base+"/third_deals", map[string]interface{}{"name": "Epsilon"})
		if status != http.StatusCreated {
			t.Fatalf("create third_deals: want 201, got %d %v", status, resp)
		}
		rows := query(`SELECT name FROM deals UNION SELECT name FROM archived_deals UNION SELECT name FROM third_deals`)
		got := names(rows)
		if len(rows) != 5 || got["Epsilon"] != 1 {
			t.Fatalf("want 5 rows including Epsilon exactly once, got %v", got)
		}
	})

	t.Run("trailing ORDER BY and TOP apply to the combined result, not just the last branch", func(t *testing.T) {
		rows := query(`SELECT name FROM deals UNION SELECT name FROM archived_deals ORDER BY name ASC`)
		if len(rows) != 4 {
			t.Fatalf("want 4 rows, got %d", len(rows))
		}
		first := rows[0].(map[string]interface{})["name"]
		if first != "Alpha" {
			t.Errorf("want first row (ASC order) to be Alpha, got %v -- full: %v", first, rows)
		}

		topRows := query(`SELECT name FROM deals UNION SELECT TOP 2 name FROM archived_deals ORDER BY name ASC`)
		if len(topRows) > 2 {
			t.Errorf("TOP 2 on the combined result: want at most 2 rows, got %d: %v", len(topRows), topRows)
		}
	})

	t.Run("mismatched column counts rejected", func(t *testing.T) {
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT name, id FROM deals UNION SELECT name FROM archived_deals`})
		if status == http.StatusOK {
			t.Fatalf("want a rejection for mismatched column counts, got 200: %v", resp)
		}
	})
}

// TestOQLUnion_TenantIsolation proves each UNION branch inherits full
// tenant scoping from the same executeSelect path every other query
// uses, rather than assuming it from the implementation's own
// comments. Two tenants, identical entity/schema, distinct data,
// confirmed neither tenant's own UNION result reflects the other's.
func TestOQLUnion_TenantIsolation(t *testing.T) {
	env := newV2Server(t)

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
	}
	for _, entity := range []string{"deals", "archived_deals"} {
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
		if status != http.StatusCreated {
			t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
		}
	}

	seedTenant := func(tenantName string) {
		t.Helper()
		base := env.ts.URL + "/api/v1/tenant/" + tenantName
		status, resp := doJSONRequest(t, "POST", base+"/deals", map[string]interface{}{"name": tenantName + "-deal"})
		if status != http.StatusCreated {
			t.Fatalf("[%s] create deal: want 201, got %d %v", tenantName, status, resp)
		}
		status, resp = doJSONRequest(t, "POST", base+"/archived_deals", map[string]interface{}{"name": tenantName + "-archived"})
		if status != http.StatusCreated {
			t.Fatalf("[%s] create archived_deal: want 201, got %d %v", tenantName, status, resp)
		}
	}
	seedTenant("tenanta")
	seedTenant("tenantb")

	query := `SELECT name FROM deals UNION SELECT name FROM archived_deals`
	status, respA := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/tenanta/oql/query", map[string]interface{}{"query": query})
	if status != http.StatusOK {
		t.Fatalf("tenanta UNION: want 200, got %d %v", status, respA)
	}
	dataA, _ := respA["data"].([]interface{})
	if len(dataA) != 2 {
		t.Fatalf("tenant isolation violated: tenanta's own UNION result want exactly 2 rows, got %d: %v", len(dataA), dataA)
	}
	for _, row := range dataA {
		name := row.(map[string]interface{})["name"].(string)
		if name != "tenanta-deal" && name != "tenanta-archived" {
			t.Fatalf("tenant isolation violated: tenanta's own UNION result contains %q", name)
		}
	}

	status, respB := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/tenantb/oql/query", map[string]interface{}{"query": query})
	if status != http.StatusOK {
		t.Fatalf("tenantb UNION: want 200, got %d %v", status, respB)
	}
	dataB, _ := respB["data"].([]interface{})
	if len(dataB) != 2 {
		t.Fatalf("tenant isolation violated: tenantb's own UNION result want exactly 2 rows, got %d: %v", len(dataB), dataB)
	}
	for _, row := range dataB {
		name := row.(map[string]interface{})["name"].(string)
		if name != "tenantb-deal" && name != "tenantb-archived" {
			t.Fatalf("tenant isolation violated: tenantb's own UNION result contains %q", name)
		}
	}
}

// TestOQLUnion_Adversarial covers edge cases and adversarial inputs
// beyond the happy-path coverage in TestOQLUnion_EndToEnd -- found by
// deliberately probing where this session's own history says bugs
// hide (combinatorial interactions, resource limits, type coercion),
// not by exhaustively enumerating every possible input.
func TestOQLUnion_Adversarial(t *testing.T) {
	t.Run("MaxRows enforced on the combined result, not just each branch individually", func(t *testing.T) {
		// A genuine resource-limit bypass risk, found by reading the
		// code directly, not assumed: executeSelect enforces MaxRows
		// per branch (each branch runs through it independently), but
		// executeUnion's own combine step had no equivalent check on
		// the assembled result -- N branches, each individually under
		// the cap, could combine into something far over it. Set the
		// cap low enough to make this fast and deterministic: two
		// branches, 3 rows each (under a cap of 5 individually), 6
		// rows combined (over a cap of 5 combined).
		env := newV2Server(t, func(c *config.Config) { c.QueryMaxRows = 5 })
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		for _, entity := range []string{"a_side", "b_side"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		for _, entity := range []string{"a_side", "b_side"} {
			for i := 0; i < 3; i++ {
				status, resp := doJSONRequest(t, "POST", base+"/"+entity,
					map[string]interface{}{"name": fmt.Sprintf("%s-%d", entity, i)})
				if status != http.StatusCreated {
					t.Fatalf("create %s-%d: want 201, got %d %v", entity, i, status, resp)
				}
			}
		}

		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT name FROM a_side UNION ALL SELECT name FROM b_side`})
		if status == http.StatusOK {
			data, _ := resp["data"].([]interface{})
			t.Fatalf("MaxRows not enforced on the combined UNION result: 6 rows combined (3+3), cap is 5, want a rejection, got 200 with %d rows: %v", len(data), data)
		}
	})

	t.Run("SELECT * rejected in a UNION chain (column-count check is meaningless for it)", func(t *testing.T) {
		env := newFullDxpServer(t)
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		for _, entity := range []string{"star_a", "star_b"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT * FROM star_a UNION SELECT * FROM star_b`})
		if status == http.StatusOK {
			t.Fatalf("want SELECT * rejected in a UNION chain, got 200: %v", resp)
		}
	})

	t.Run("empty branch on either side does not error, combines cleanly", func(t *testing.T) {
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		for _, entity := range []string{"has_rows", "empty_one"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		// empty_one is registered but never populated -- a genuinely
		// empty result, not a nonexistent entity (that's already
		// covered separately by TestValidatorUnionChain).
		status, resp := doJSONRequest(t, "POST", base+"/has_rows", map[string]interface{}{"name": "only-row"})
		if status != http.StatusCreated {
			t.Fatalf("create has_rows: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT name FROM has_rows UNION SELECT name FROM empty_one`})
		if status != http.StatusOK {
			t.Fatalf("UNION with an empty right branch: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 || data[0].(map[string]interface{})["name"] != "only-row" {
			t.Fatalf("want exactly [only-row], got %v", data)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT name FROM empty_one UNION SELECT name FROM has_rows`})
		if status != http.StatusOK {
			t.Fatalf("UNION with an empty left branch: want 200, got %d %v", status, resp)
		}
		data, _ = resp["data"].([]interface{})
		if len(data) != 1 || data[0].(map[string]interface{})["name"] != "only-row" {
			t.Fatalf("want exactly [only-row], got %v", data)
		}
	})

	t.Run("both branches empty returns an empty result, not an error", func(t *testing.T) {
		env := newFullDxpServer(t)
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		for _, entity := range []string{"empty_a", "empty_b"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT name FROM empty_a UNION SELECT name FROM empty_b`})
		if status != http.StatusOK {
			t.Fatalf("want 200 for both-empty UNION, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 0 {
			t.Fatalf("want an empty result, got %v", data)
		}
	})

	t.Run("duplicate rows within a single branch collapse too, not just cross-branch duplicates", func(t *testing.T) {
		// Standard SQL UNION semantics apply DISTINCT to the whole
		// combined set, not just row pairs that happen to straddle
		// the branch boundary -- worth proving directly, since
		// dedupeRows operates on the fully-concatenated slice and
		// this is exactly the kind of assumption worth checking
		// rather than trusting.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		for _, entity := range []string{"dup_a", "dup_b"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		// Two records in dup_a with the identical name -- different
		// ids, same logical row once only "name" is selected.
		for i := 0; i < 2; i++ {
			status, resp := doJSONRequest(t, "POST", base+"/dup_a", map[string]interface{}{"name": "repeated"})
			if status != http.StatusCreated {
				t.Fatalf("create dup_a %d: want 201, got %d %v", i, status, resp)
			}
		}
		status, resp := doJSONRequest(t, "POST", base+"/dup_b", map[string]interface{}{"name": "unique"})
		if status != http.StatusCreated {
			t.Fatalf("create dup_b: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT name FROM dup_a UNION SELECT name FROM dup_b`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 2 {
			t.Fatalf("want exactly 2 rows (within-branch duplicate collapsed), got %d: %v", len(data), data)
		}
	})

	t.Run("bare aggregate as a UNION branch: real values, not just row-count consistency", func(t *testing.T) {
		// Pre-XOT186, this test could only honestly prove "UNION
		// doesn't corrupt it further" than the then-broken aggregate
		// path -- COUNT(*) itself returned every raw row with the
		// aggregate column null, independent of UNION entirely. Now
		// that XOT186 is fixed, this asserts the real, correct values
		// -- functionality that was never actually verified as
		// *working*, only as "not made worse by the new machinery".
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		for _, entity := range []string{"agg_a", "agg_b"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		for i := 0; i < 3; i++ {
			status, resp := doJSONRequest(t, "POST", base+"/agg_a", map[string]interface{}{"name": fmt.Sprintf("a-%d", i)})
			if status != http.StatusCreated {
				t.Fatalf("create agg_a %d: want 201, got %d %v", i, status, resp)
			}
		}
		status, resp := doJSONRequest(t, "POST", base+"/agg_b", map[string]interface{}{"name": "b-0"})
		if status != http.StatusCreated {
			t.Fatalf("create agg_b: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT COUNT(*) AS cnt FROM agg_a UNION ALL SELECT COUNT(*) AS cnt FROM agg_b`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 2 {
			t.Fatalf("want 2 rows (one count per branch, UNION ALL keeps both), got %d: %v", len(data), data)
		}
		counts := map[float64]bool{}
		for _, r := range data {
			counts[r.(map[string]interface{})["cnt"].(float64)] = true
		}
		if !counts[3] || !counts[1] {
			t.Errorf("want counts {3, 1} (agg_a has 3 rows, agg_b has 1), got %v", data)
		}
	})

	t.Run("GROUP BY as a UNION branch, real grouped values on both sides", func(t *testing.T) {
		// Never verified at all before XOT186 -- a UNION where each
		// branch is itself a GROUP BY query, not just a bare
		// aggregate.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cat": map[string]interface{}{"type": "string"},
				"val": map[string]interface{}{"type": "integer"},
			},
		}
		for _, entity := range []string{"grp_a", "grp_b"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		for _, r := range []map[string]interface{}{
			{"cat": "p", "val": float64(10)}, {"cat": "p", "val": float64(20)}, {"cat": "q", "val": float64(5)},
		} {
			status, resp := doJSONRequest(t, "POST", base+"/grp_a", r)
			if status != http.StatusCreated {
				t.Fatalf("create grp_a %v: want 201, got %d %v", r, status, resp)
			}
		}
		status, resp := doJSONRequest(t, "POST", base+"/grp_b", map[string]interface{}{"cat": "r", "val": float64(999)})
		if status != http.StatusCreated {
			t.Fatalf("create grp_b: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT cat, SUM(val) AS total FROM grp_a GROUP BY cat UNION SELECT cat, SUM(val) AS total FROM grp_b GROUP BY cat`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 3 {
			t.Fatalf("want 3 groups (p, q from grp_a; r from grp_b), got %d: %v", len(data), data)
		}
		got := map[string]float64{}
		for _, r := range data {
			row := r.(map[string]interface{})
			got[row["cat"].(string)] = row["total"].(float64)
		}
		if got["p"] != 30 || got["q"] != 5 || got["r"] != 999 {
			t.Errorf("want p=30, q=5, r=999, got %v", got)
		}
	})

	t.Run("HAVING within a UNION branch, real filtering", func(t *testing.T) {
		// Never verified at all before XOT186 -- and HAVING itself
		// had its own, separate bug (the exprAliases fix) found while
		// building the regression tests for the main aggregate fix,
		// never exercised through UNION specifically until now.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cat": map[string]interface{}{"type": "string"},
				"val": map[string]interface{}{"type": "integer"},
			},
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/having_union", schema)
		if status != http.StatusCreated {
			t.Fatalf("schema: want 201, got %d %v", status, resp)
		}
		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/having_union_other", schema)
		if status != http.StatusCreated {
			t.Fatalf("schema (other): want 201, got %d %v", status, resp)
		}
		for _, r := range []map[string]interface{}{
			{"cat": "p", "val": float64(10)}, {"cat": "p", "val": float64(20)}, {"cat": "q", "val": float64(5)},
		} {
			status, resp := doJSONRequest(t, "POST", base+"/having_union", r)
			if status != http.StatusCreated {
				t.Fatalf("create %v: want 201, got %d %v", r, status, resp)
			}
		}
		status, resp = doJSONRequest(t, "POST", base+"/having_union_other", map[string]interface{}{"cat": "r", "val": float64(1)})
		if status != http.StatusCreated {
			t.Fatalf("create other: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT cat, SUM(val) AS total FROM having_union GROUP BY cat HAVING SUM(val) > 15 UNION SELECT cat, SUM(val) AS total FROM having_union_other GROUP BY cat`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		got := map[string]bool{}
		for _, r := range data {
			got[r.(map[string]interface{})["cat"].(string)] = true
		}
		if !got["p"] || got["q"] || !got["r"] {
			t.Fatalf("want p (total 30 > 15) and r (other branch, no HAVING) present, q absent (total 5, filtered by HAVING), got %v", data)
		}
	})

	t.Run("chain depth stress: 10 branches all combine correctly", func(t *testing.T) {
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		const n = 10
		var entities []string
		for i := 0; i < n; i++ {
			entities = append(entities, fmt.Sprintf("chain_%d", i))
		}
		for _, entity := range entities {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
			status, resp = doJSONRequest(t, "POST", base+"/"+entity, map[string]interface{}{"name": entity})
			if status != http.StatusCreated {
				t.Fatalf("create %s: want 201, got %d %v", entity, status, resp)
			}
		}
		q := "SELECT name FROM " + entities[0]
		for _, e := range entities[1:] {
			q += " UNION SELECT name FROM " + e
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query", map[string]interface{}{"query": q})
		if status != http.StatusOK {
			t.Fatalf("10-branch chain: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != n {
			t.Fatalf("want %d distinct rows, got %d: %v", n, len(data), data)
		}
	})

	t.Run("decimal field denormalisation correct within each UNION branch independently", func(t *testing.T) {
		// XM-7b (this same session) was specifically about decimal
		// denormalisation being silently missing in one query path
		// (JOIN). UNION doesn't touch decimal handling at all -- each
		// branch runs through the ordinary executeSelect, which
		// already denormalises correctly -- but that's exactly the
		// kind of "should be fine by construction" assumption this
		// session's own history says is worth proving rather than
		// trusting.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string"},
				"amount": map[string]interface{}{
					"type": "string", "format": "decimal",
					"decimalPrecision": float64(10), "decimalScale": float64(2),
				},
			},
		}
		for _, entity := range []string{"dec_a", "dec_b"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		status, resp := doJSONRequest(t, "POST", base+"/dec_a", map[string]interface{}{"name": "x", "amount": "10.50"})
		if status != http.StatusCreated {
			t.Fatalf("create dec_a: want 201, got %d %v", status, resp)
		}
		status, resp = doJSONRequest(t, "POST", base+"/dec_b", map[string]interface{}{"name": "y", "amount": "20.25"})
		if status != http.StatusCreated {
			t.Fatalf("create dec_b: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT amount FROM dec_a UNION SELECT amount FROM dec_b`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 2 {
			t.Fatalf("want 2 rows, got %d: %v", len(data), data)
		}
		got := map[string]bool{}
		for _, row := range data {
			got[row.(map[string]interface{})["amount"].(string)] = true
		}
		if !got["10.50"] || !got["20.25"] {
			t.Errorf("want decimal strings \"10.50\" and \"20.25\" preserved through UNION, got %v", data)
		}
	})

	t.Run("NULL values deduplicate consistently across branches", func(t *testing.T) {
		// dedupeRows uses json.Marshal for row identity (rowSignature).
		// A Go nil field marshals to the JSON literal null consistently
		// regardless of which branch produced it -- worth proving
		// directly rather than trusting that encoding/json's own
		// behaviour composes correctly with this specific use, given
		// how much of this session's own history has been exactly
		// this kind of "should be fine by construction" assumption
		// turning out not to hold.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string"},
				"note": map[string]interface{}{"type": "string"},
			},
		}
		for _, entity := range []string{"null_a", "null_b"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		// Both records omit "note" entirely -- both will read back as
		// a Go nil for that field, from two independently-executed
		// branches.
		status, resp := doJSONRequest(t, "POST", base+"/null_a", map[string]interface{}{"name": "x"})
		if status != http.StatusCreated {
			t.Fatalf("create null_a: want 201, got %d %v", status, resp)
		}
		status, resp = doJSONRequest(t, "POST", base+"/null_b", map[string]interface{}{"name": "x"})
		if status != http.StatusCreated {
			t.Fatalf("create null_b: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT name, note FROM null_a UNION SELECT name, note FROM null_b`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 {
			t.Fatalf("want the two identical (name=x, note=NULL) rows deduplicated into 1, got %d: %v", len(data), data)
		}
		if data[0].(map[string]interface{})["note"] != nil {
			t.Errorf("want note=nil, got %v", data[0].(map[string]interface{})["note"])
		}
	})

	t.Run("numeric values compare consistently for dedup regardless of which branch's own storage kind produced them", func(t *testing.T) {
		// A genuinely subtle risk: if one branch's own scalar field
		// comes back as a Go int64 and another branch's identical
		// logical value comes back as float64 (plausible if the two
		// branches are backed by different storage paths internally),
		// rowSignature's own json.Marshal-based comparison needs both
		// to serialize identically for dedup to treat them as the
		// same row -- proven directly with two entities sharing an
		// identical numeric value, not assumed safe by construction.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":  map[string]interface{}{"type": "string"},
				"count": map[string]interface{}{"type": "integer"},
			},
		}
		for _, entity := range []string{"num_a", "num_b"} {
			status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/"+entity, schema)
			if status != http.StatusCreated {
				t.Fatalf("schema %s: want 201, got %d %v", entity, status, resp)
			}
		}
		status, resp := doJSONRequest(t, "POST", base+"/num_a", map[string]interface{}{"name": "x", "count": float64(5)})
		if status != http.StatusCreated {
			t.Fatalf("create num_a: want 201, got %d %v", status, resp)
		}
		status, resp = doJSONRequest(t, "POST", base+"/num_b", map[string]interface{}{"name": "x", "count": float64(5)})
		if status != http.StatusCreated {
			t.Fatalf("create num_b: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT name, count FROM num_a UNION SELECT name, count FROM num_b`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 {
			t.Fatalf("want the two identical (name=x, count=5) rows deduplicated into 1 regardless of each branch's own underlying numeric representation, got %d: %v", len(data), data)
		}
	})
}
