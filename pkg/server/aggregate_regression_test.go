// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http"
	"testing"
)

// TestRegression_AggregateCorrectness is XOT186, the "grave COUNT
// bug": aggregate functions (found first for COUNT(*), then confirmed
// broader -- GROUP BY generally, AVG specifically, and eventually
// isolated to any adapted entity) returned raw, ungrouped rows with
// the aggregate column always null, instead of correctly aggregated
// results. Root cause, confirmed precisely by direct unit testing of
// Aggregator.Aggregate (which was already correct in isolation) then
// tracing the planner's own dispatch: GenerateAdaptedSQL and
// GenerateAggregateSQL (pkg/oql/sqlgen_adapted.go,
// sqlgen_aggregate.go) both unconditionally emitted a
// "WHERE tenant_id = ?" predicate for adapted tables, which never
// have a tenant_id column at all (they're isolated by their own
// per-tenant table name, t<XXXX>_ndata_<entity>) -- the generated SQL
// failed outright with "no such column: tenant_id", and a second,
// independent bug (fixed alongside this one: a failed push-down left
// plan.Push unchanged, incorrectly gating off the Go-path aggregate
// fallback that would otherwise have caught this) meant the failure
// was silent rather than either succeeding correctly or erroring
// loudly.
func TestRegression_AggregateCorrectness(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"cat": map[string]interface{}{"type": "string"},
			"val": map[string]interface{}{"type": "integer"},
		},
	}
	status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/agg_regress", schema)
	if status != http.StatusCreated {
		t.Fatalf("schema: want 201, got %d %v", status, resp)
	}
	for _, r := range []map[string]interface{}{
		{"cat": "x", "val": float64(10)},
		{"cat": "x", "val": float64(20)},
		{"cat": "y", "val": float64(100)},
	} {
		status, resp := doJSONRequest(t, "POST", base+"/agg_regress", r)
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

	t.Run("COUNT(*), no GROUP BY: exactly one row with the real count", func(t *testing.T) {
		rows := query(`SELECT COUNT(*) AS cnt FROM agg_regress`)
		if len(rows) != 1 {
			t.Fatalf("want exactly 1 row, got %d: %v", len(rows), rows)
		}
		if rows[0].(map[string]interface{})["cnt"] != float64(3) {
			t.Errorf("want cnt=3, got %v", rows[0])
		}
	})

	t.Run("GROUP BY with AVG: correctly grouped, not raw rows", func(t *testing.T) {
		rows := query(`SELECT cat, AVG(val) AS avgval FROM agg_regress GROUP BY cat`)
		if len(rows) != 2 {
			t.Fatalf("want 2 groups, got %d: %v", len(rows), rows)
		}
		got := map[string]interface{}{}
		for _, r := range rows {
			row := r.(map[string]interface{})
			got[row["cat"].(string)] = row["avgval"]
		}
		if got["x"] != float64(15) || got["y"] != float64(100) {
			t.Errorf("want cat=x:15, cat=y:100, got %v", got)
		}
	})

	t.Run("GROUP BY with SUM", func(t *testing.T) {
		rows := query(`SELECT cat, SUM(val) AS total FROM agg_regress GROUP BY cat`)
		got := map[string]interface{}{}
		for _, r := range rows {
			row := r.(map[string]interface{})
			got[row["cat"].(string)] = row["total"]
		}
		if got["x"] != float64(30) || got["y"] != float64(100) {
			t.Fatalf("want cat=x:30, cat=y:100, got %v", got)
		}
	})

	t.Run("GROUP BY with MIN and MAX together", func(t *testing.T) {
		rows := query(`SELECT cat, MIN(val) AS lo, MAX(val) AS hi FROM agg_regress GROUP BY cat`)
		for _, r := range rows {
			row := r.(map[string]interface{})
			if row["cat"] == "x" {
				if row["lo"] != float64(10) || row["hi"] != float64(20) {
					t.Errorf("cat=x: want lo=10 hi=20, got %v", row)
				}
			}
		}
	})

	t.Run("GROUP BY with HAVING (still runs in Go per the push-down's own design)", func(t *testing.T) {
		rows := query(`SELECT cat, SUM(val) AS total FROM agg_regress GROUP BY cat HAVING SUM(val) > 50`)
		if len(rows) != 1 || rows[0].(map[string]interface{})["cat"] != "y" {
			t.Fatalf("want exactly [cat=y] (total 100 > 50, x's own total 30 excluded), got %v", rows)
		}
	})

	t.Run("bare aggregate, no rows in the entity, still returns exactly one row (SQL standard)", func(t *testing.T) {
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/agg_empty",
			map[string]interface{}{"type": "object", "properties": map[string]interface{}{"val": map[string]interface{}{"type": "integer"}}})
		if status != http.StatusCreated {
			t.Fatalf("schema: want 201, got %d %v", status, resp)
		}
		rows := query(`SELECT COUNT(*) AS cnt FROM agg_empty`)
		if len(rows) != 1 || rows[0].(map[string]interface{})["cnt"] != float64(0) {
			t.Fatalf("want exactly [cnt=0], got %v", rows)
		}
	})
}

// TestRegression_AggregateTenantIsolation proves tenant isolation
// still holds for adapted-table aggregate/GROUP BY push-down after
// removing the incorrect "tenant_id = ?" predicate -- adapted tables
// were never isolated by that column at all (they don't have it),
// their own per-tenant table name is the real isolation boundary, but
// this is exactly the kind of change ("we removed a WHERE clause")
// that needs to be proven safe directly, not assumed.
func TestRegression_AggregateTenantIsolation(t *testing.T) {
	env := newV2Server(t)
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"cat": map[string]interface{}{"type": "string"},
			"val": map[string]interface{}{"type": "integer"},
		},
	}
	status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/agg_tenant", schema)
	if status != http.StatusCreated {
		t.Fatalf("schema: want 201, got %d %v", status, resp)
	}

	seed := func(tenantName string, val float64) {
		t.Helper()
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/"+tenantName+"/agg_tenant",
			map[string]interface{}{"cat": "x", "val": val})
		if status != http.StatusCreated {
			t.Fatalf("[%s] create: want 201, got %d %v", tenantName, status, resp)
		}
	}
	seed("tenanta", 10)
	seed("tenanta", 20)
	seed("tenantb", 1000)

	for tenantName, wantSum := range map[string]float64{"tenanta": 30, "tenantb": 1000} {
		status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/tenant/"+tenantName+"/oql/query",
			map[string]interface{}{"query": `SELECT cat, SUM(val) AS total FROM agg_tenant GROUP BY cat`})
		if status != http.StatusOK {
			t.Fatalf("[%s] query: want 200, got %d %v", tenantName, status, resp)
		}
		data, _ := resp["data"].([]interface{})
		if len(data) != 1 {
			t.Fatalf("tenant isolation violated: [%s] want exactly 1 group, got %d: %v", tenantName, len(data), data)
		}
		if data[0].(map[string]interface{})["total"] != wantSum {
			t.Fatalf("tenant isolation violated: [%s] want total=%v, got %v", tenantName, wantSum, data[0])
		}
	}
}
