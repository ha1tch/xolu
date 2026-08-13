// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http"
	"testing"
)

// TestRegression_AdaptedWhereFilter is a genuine, severe, pre-existing
// bug found 2026-08-13 while testing an unrelated feature (derived
// tables): WHERE was silently dropped entirely for any adapted entity
// small enough that the query planner chose not to push down (the
// "B4 path", storage.FilterableStore.ListWithFieldsAndFilter) --
// every row came back regardless of the WHERE clause, on any field,
// including the primary key. Root cause:
// SQLiteStore.ListWithFieldsAndFilter's own adapted-table branch
// silently ignored its own preds argument entirely, returning
// adaptedList's plain, unfiltered rows -- its own comment ("adapted
// tables don't benefit -- their List already reads native columns")
// was simply wrong; adaptedList has no predicate-filtering capability
// at all. Fixed by skipping this optimisation path for adapted
// entities specifically, falling through to the pre-existing, already
// -correct fallback (full list, full WHERE re-applied in Go).
//
// Confirmed to genuinely reproduce and fix: the standalone, most
// minimal possible trigger (a freshly schema-registered entity small
// enough to stay under the push-down threshold, WHERE on the primary
// key) failed before this fix and passes after it.
func TestRegression_AdaptedWhereFilter(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"age":  map[string]interface{}{"type": "integer"},
		},
	}
	status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/where_regress", schema)
	if status != http.StatusCreated {
		t.Fatalf("schema: want 201, got %d %v", status, resp)
	}
	for _, r := range []map[string]interface{}{
		{"name": "Alpha", "age": float64(10)},
		{"name": "Beta", "age": float64(20)},
		{"name": "Gamma", "age": float64(30)},
	} {
		status, resp := doJSONRequest(t, "POST", base+"/where_regress", r)
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

	if rows := query(`SELECT name FROM where_regress WHERE id = 1`); len(rows) != 1 {
		t.Errorf("WHERE id = 1: want exactly 1 row, got %d: %v", len(rows), rows)
	}
	if rows := query(`SELECT name FROM where_regress WHERE id = 999`); len(rows) != 0 {
		t.Errorf("WHERE id = 999 (no match): want 0 rows, got %d: %v", len(rows), rows)
	}
	if rows := query(`SELECT name FROM where_regress WHERE name = 'Alpha'`); len(rows) != 1 {
		t.Errorf("WHERE name = 'Alpha': want exactly 1 row, got %d: %v", len(rows), rows)
	}
	if rows := query(`SELECT name FROM where_regress WHERE name = 'nonexistent'`); len(rows) != 0 {
		t.Errorf("WHERE name = 'nonexistent': want 0 rows, got %d: %v", len(rows), rows)
	}
	if rows := query(`SELECT name FROM where_regress WHERE age = 20`); len(rows) != 1 {
		t.Errorf("WHERE age = 20: want exactly 1 row, got %d: %v", len(rows), rows)
	}
	if rows := query(`SELECT name FROM where_regress WHERE age > 15`); len(rows) != 2 {
		t.Errorf("WHERE age > 15: want exactly 2 rows (Beta, Gamma), got %d: %v", len(rows), rows)
	}
}
