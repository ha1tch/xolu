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

// TestOQLSubquery_EndToEnd proves derived-table (subquery in FROM)
// execution against real data. The validator side already had
// TestValidatorDerivedTable (pkg/oql); this is the execution-side
// equivalent to TestOQLUnion_EndToEnd, following the exact same
// discipline this session applied to UNION: don't declare a feature
// working from validator acceptance alone, run it for real.
func TestOQLSubquery_EndToEnd(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"cat":  map[string]interface{}{"type": "string"},
		},
	}
	status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/sq_items", schema)
	if status != http.StatusCreated {
		t.Fatalf("schema: want 201, got %d %v", status, resp)
	}
	for _, r := range []map[string]interface{}{
		{"name": "Alpha", "cat": "x"}, {"name": "Beta", "cat": "x"}, {"name": "Gamma", "cat": "y"},
	} {
		status, resp := doJSONRequest(t, "POST", base+"/sq_items", r)
		if status != http.StatusCreated {
			t.Fatalf("create %v: want 201, got %d %v", r, status, resp)
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

	t.Run("basic passthrough, SELECT *", func(t *testing.T) {
		rows := query(`SELECT * FROM (SELECT name FROM sq_items) AS x`)
		if len(rows) != 3 {
			t.Fatalf("want 3 rows, got %d: %v", len(rows), rows)
		}
	})

	t.Run("column projection: outer SELECT can name a subset of the inner columns", func(t *testing.T) {
		rows := query(`SELECT name FROM (SELECT name, cat FROM sq_items) AS x`)
		if len(rows) != 3 {
			t.Fatalf("want 3 rows, got %d: %v", len(rows), rows)
		}
		row := rows[0].(map[string]interface{})
		if _, hasCat := row["cat"]; hasCat {
			t.Errorf("outer projection selected only name, want cat excluded, got %v", row)
		}
	})

	t.Run("inner and outer WHERE both apply", func(t *testing.T) {
		rows := query(`SELECT name FROM (SELECT name FROM sq_items WHERE name != 'Beta') AS x WHERE name = 'Alpha'`)
		if len(rows) != 1 || rows[0].(map[string]interface{})["name"] != "Alpha" {
			t.Fatalf("want exactly [Alpha], got %v", rows)
		}
	})

	t.Run("GROUP BY and aggregate over a derived table", func(t *testing.T) {
		rows := query(`SELECT cat, COUNT(*) AS n FROM (SELECT name, cat FROM sq_items) AS x GROUP BY cat`)
		if len(rows) != 2 {
			t.Fatalf("want 2 groups, got %d: %v", len(rows), rows)
		}
		got := map[string]interface{}{}
		for _, r := range rows {
			row := r.(map[string]interface{})
			got[row["cat"].(string)] = row["n"]
		}
		if got["x"] != float64(2) || got["y"] != float64(1) {
			t.Errorf("want cat=x:2, cat=y:1, got %v", got)
		}
	})

	t.Run("ORDER BY over a derived table", func(t *testing.T) {
		rows := query(`SELECT name FROM (SELECT name FROM sq_items) AS x ORDER BY name DESC`)
		if len(rows) != 3 {
			t.Fatalf("want 3 rows, got %d: %v", len(rows), rows)
		}
		if rows[0].(map[string]interface{})["name"] != "Gamma" {
			t.Errorf("want Gamma first (DESC), got %v", rows[0])
		}
	})

	t.Run("DISTINCT over a derived table", func(t *testing.T) {
		rows := query(`SELECT DISTINCT cat FROM (SELECT cat FROM sq_items) AS x`)
		if len(rows) != 2 {
			t.Fatalf("want 2 distinct cats, got %d: %v", len(rows), rows)
		}
	})

	t.Run("TOP over a derived table", func(t *testing.T) {
		rows := query(`SELECT TOP 1 name FROM (SELECT name FROM sq_items ORDER BY name ASC) AS x`)
		if len(rows) != 1 {
			t.Fatalf("want exactly 1 row, got %d: %v", len(rows), rows)
		}
	})

	t.Run("nested derived tables, two levels", func(t *testing.T) {
		rows := query(`SELECT name FROM (SELECT name FROM (SELECT name FROM sq_items) AS inner1) AS outer1`)
		got := names(rows)
		if len(rows) != 3 || got["Alpha"] != 1 || got["Beta"] != 1 || got["Gamma"] != 1 {
			t.Fatalf("want all 3 rows through two levels of nesting, got %v", got)
		}
	})

	t.Run("UNION nested inside a derived table", func(t *testing.T) {
		rows := query(`SELECT name FROM (SELECT name FROM sq_items WHERE cat='x' UNION SELECT name FROM sq_items WHERE cat='y') AS x`)
		got := names(rows)
		if len(rows) != 3 || got["Alpha"] != 1 || got["Beta"] != 1 || got["Gamma"] != 1 {
			t.Fatalf("want all 3 rows via UNION inside the derived table, got %v", got)
		}
	})

	t.Run("empty inner result, no error", func(t *testing.T) {
		rows := query(`SELECT * FROM (SELECT name FROM sq_items WHERE name = 'nonexistent') AS x`)
		if len(rows) != 0 {
			t.Fatalf("want 0 rows, got %d: %v", len(rows), rows)
		}
	})

	t.Run("empty after outer filter, no error", func(t *testing.T) {
		rows := query(`SELECT * FROM (SELECT name FROM sq_items) AS x WHERE name = 'nonexistent'`)
		if len(rows) != 0 {
			t.Fatalf("want 0 rows, got %d: %v", len(rows), rows)
		}
	})
}

// TestOQLSubquery_TenantIsolation proves tenant scoping is genuinely
// inherited from the recursive executeSelect call, not merely
// asserted in a code comment -- same discipline as
// TestOQLUnion_TenantIsolation.
func TestOQLSubquery_TenantIsolation(t *testing.T) {
	env := newV2Server(t)
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
	}
	status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/sq_tenant", schema)
	if status != http.StatusCreated {
		t.Fatalf("schema: want 201, got %d %v", status, resp)
	}

	seed := func(tenantName string) {
		t.Helper()
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/"+tenantName+"/sq_tenant",
			map[string]interface{}{"name": tenantName + "-item"})
		if status != http.StatusCreated {
			t.Fatalf("[%s] create: want 201, got %d %v", tenantName, status, resp)
		}
	}
	seed("tenanta")
	seed("tenantb")

	for _, tenantName := range []string{"tenanta", "tenantb"} {
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/"+tenantName+"/oql/query",
			map[string]interface{}{"query": `SELECT name FROM (SELECT name FROM sq_tenant) AS x`})
		if status != http.StatusOK {
			t.Fatalf("[%s] query: want 200, got %d %v", tenantName, status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 {
			t.Fatalf("tenant isolation violated: [%s] want exactly its own 1 row, got %d: %v", tenantName, len(data), data)
		}
		if data[0].(map[string]interface{})["name"] != tenantName+"-item" {
			t.Fatalf("tenant isolation violated: [%s] got %v", tenantName, data[0])
		}
	}
}

// TestOQLSubquery_Adversarial covers edge cases and adversarial
// inputs beyond the happy-path coverage in TestOQLSubquery_EndToEnd,
// matching TestOQLUnion_Adversarial's own discipline: probe where
// this session's own history says bugs hide (resource limits, deep
// nesting, decimal handling, aggregate interactions), not exhaustive
// enumeration.
func TestOQLSubquery_Adversarial(t *testing.T) {
	t.Run("MaxRows enforced on the outer, final result", func(t *testing.T) {
		env := newV2Server(t, func(c *config.Config) { c.QueryMaxRows = 2 })
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/sq_maxrows", schema)
		if status != http.StatusCreated {
			t.Fatalf("schema: want 201, got %d %v", status, resp)
		}
		for i := 0; i < 3; i++ {
			status, resp := doJSONRequest(t, "POST", base+"/sq_maxrows", map[string]interface{}{"name": fmt.Sprintf("n%d", i)})
			if status != http.StatusCreated {
				t.Fatalf("create %d: want 201, got %d %v", i, status, resp)
			}
		}
		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT * FROM (SELECT name FROM sq_maxrows) AS x`})
		if status == http.StatusOK {
			data, _ := resp["data"].([]interface{})
			t.Fatalf("MaxRows not enforced: 3 rows against a cap of 2, want a rejection, got 200 with %d rows: %v", len(data), data)
		}
	})

	t.Run("deep nesting at exactly the cap succeeds against a real server, one over fails", func(t *testing.T) {
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/sq_deep", schema)
		if status != http.StatusCreated {
			t.Fatalf("schema: want 201, got %d %v", status, resp)
		}
		status, resp = doJSONRequest(t, "POST", base+"/sq_deep", map[string]interface{}{"name": "leaf"})
		if status != http.StatusCreated {
			t.Fatalf("create: want 201, got %d %v", status, resp)
		}

		build := func(levels int) string {
			q := "SELECT name FROM sq_deep"
			for i := 0; i < levels; i++ {
				q = fmt.Sprintf("SELECT name FROM (%s) AS lvl%d", q, i)
			}
			return q
		}
		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query", map[string]interface{}{"query": build(10)})
		if status != http.StatusOK {
			t.Fatalf("10 levels (at the cap): want 200, got %d %v", status, resp)
		}
		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query", map[string]interface{}{"query": build(11)})
		if status == http.StatusOK {
			t.Fatalf("11 levels (one over the cap): want a rejection, got 200: %v", resp)
		}
	})

	t.Run("decimal denormalisation correct through a derived table", func(t *testing.T) {
		// XM-7b (this same session) was about decimal denormalisation
		// silently missing in the JOIN path specifically. A derived
		// table's own inner query already denormalises correctly
		// (inherited from the ordinary executeSelect path) -- worth
		// proving directly rather than trusting that inheritance,
		// matching this session's own established discipline.
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
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/sq_decimal", schema)
		if status != http.StatusCreated {
			t.Fatalf("schema: want 201, got %d %v", status, resp)
		}
		status, resp = doJSONRequest(t, "POST", base+"/sq_decimal", map[string]interface{}{"name": "x", "amount": "10.50"})
		if status != http.StatusCreated {
			t.Fatalf("create: want 201, got %d %v", status, resp)
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT amount FROM (SELECT amount FROM sq_decimal) AS x`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 || data[0].(map[string]interface{})["amount"] != "10.50" {
			t.Fatalf("want decimal string \"10.50\" preserved through the derived table, got %v", data)
		}
	})

	t.Run("nonexistent entity inside the subquery rejected at validation, not a 500 at execution", func(t *testing.T) {
		env := newFullDxpServer(t)
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT * FROM (SELECT name FROM totally_nonexistent_entity) AS x`})
		if status >= 500 {
			t.Fatalf("want a clean 4xx rejection, got %d: %v", status, resp)
		}
		if status == http.StatusOK {
			t.Fatalf("want a rejection for a nonexistent entity inside the subquery, got 200: %v", resp)
		}
	})

	t.Run("aggregate inside the inner subquery: real grouped values, outer query filters on the aggregate's own alias", func(t *testing.T) {
		// Pre-XOT186, this test could only honestly prove "the
		// derived-table machinery doesn't corrupt it further" than
		// the then-broken aggregate path -- GROUP BY inside the inner
		// subquery returned raw, ungrouped rows, avgval null,
		// independent of derived tables entirely. This is also the
		// restoration of this test's own original intent, abandoned
		// at the time because it couldn't have passed: the outer
		// query doesn't just pass the inner aggregate through, it
		// filters on the aggregate's own result (WHERE cat = 'x'),
		// which was never actually verified as *working* before now.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cat": map[string]interface{}{"type": "string"},
				"val": map[string]interface{}{"type": "integer"},
			},
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/sq_inner_agg", schema)
		if status != http.StatusCreated {
			t.Fatalf("schema: want 201, got %d %v", status, resp)
		}
		for _, r := range []map[string]interface{}{
			{"cat": "x", "val": float64(10)}, {"cat": "x", "val": float64(20)}, {"cat": "y", "val": float64(100)},
		} {
			status, resp := doJSONRequest(t, "POST", base+"/sq_inner_agg", r)
			if status != http.StatusCreated {
				t.Fatalf("create %v: want 201, got %d %v", r, status, resp)
			}
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT cat, avgval FROM (SELECT cat, AVG(val) AS avgval FROM sq_inner_agg GROUP BY cat) AS x WHERE cat = 'x'`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 {
			t.Fatalf("want exactly 1 row (cat=x, filtered on the inner aggregate's own result), got %d: %v", len(data), data)
		}
		if data[0].(map[string]interface{})["avgval"] != float64(15) {
			t.Errorf("want avgval=15 (avg of 10,20), got %v", data[0])
		}
	})

	t.Run("HAVING inside the inner subquery, outer query reads the filtered result", func(t *testing.T) {
		// Never verified at all before XOT186 -- and HAVING itself
		// had its own, separate bug (the exprAliases fix), never
		// exercised through a derived table until now.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cat": map[string]interface{}{"type": "string"},
				"val": map[string]interface{}{"type": "integer"},
			},
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/sq_having", schema)
		if status != http.StatusCreated {
			t.Fatalf("schema: want 201, got %d %v", status, resp)
		}
		for _, r := range []map[string]interface{}{
			{"cat": "p", "val": float64(10)}, {"cat": "p", "val": float64(20)}, {"cat": "q", "val": float64(5)},
		} {
			status, resp := doJSONRequest(t, "POST", base+"/sq_having", r)
			if status != http.StatusCreated {
				t.Fatalf("create %v: want 201, got %d %v", r, status, resp)
			}
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT cat, total FROM (SELECT cat, SUM(val) AS total FROM sq_having GROUP BY cat HAVING SUM(val) > 15) AS x`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 || data[0].(map[string]interface{})["cat"] != "p" {
			t.Fatalf("want exactly [cat=p] (total 30 > 15, q's own total 5 filtered by the inner HAVING), got %v", data)
		}
	})

	t.Run("decimal field, SUM aggregate, inside the inner subquery", func(t *testing.T) {
		// Never verified at all before XOT186 -- decimal
		// denormalisation through a derived table was already tested
		// (plain passthrough), but never combined with an aggregate
		// inside the inner subquery specifically.
		env := newFullDxpServer(t)
		base := env.ts.URL + "/api/v1/tenant/default"
		schema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cat": map[string]interface{}{"type": "string"},
				"amount": map[string]interface{}{
					"type": "string", "format": "decimal",
					"decimalPrecision": float64(10), "decimalScale": float64(2),
				},
			},
		}
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/sq_decimal_agg", schema)
		if status != http.StatusCreated {
			t.Fatalf("schema: want 201, got %d %v", status, resp)
		}
		for _, r := range []map[string]interface{}{
			{"cat": "x", "amount": "10.50"}, {"cat": "x", "amount": "20.25"},
		} {
			status, resp := doJSONRequest(t, "POST", base+"/sq_decimal_agg", r)
			if status != http.StatusCreated {
				t.Fatalf("create %v: want 201, got %d %v", r, status, resp)
			}
		}

		status, resp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/default/oql/query",
			map[string]interface{}{"query": `SELECT cat, total FROM (SELECT cat, SUM(amount) AS total FROM sq_decimal_agg GROUP BY cat) AS x`})
		if status != http.StatusOK {
			t.Fatalf("query: want 200, got %d %v", status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 {
			t.Fatalf("want exactly 1 group, got %d: %v", len(data), data)
		}
		got := data[0].(map[string]interface{})["total"]
		if got != "30.75" {
			t.Errorf("want decimal string \"30.75\" (10.50 + 20.25), got %v (%T)", got, got)
		}
	})
}
