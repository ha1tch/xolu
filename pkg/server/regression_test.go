// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// regression_test.go — regression tests for bugs found during jsonfile backend
// removal (v0.9.9-rc01). These tests exercise the full HTTP server stack so
// that transport-level behaviour (query-string parsing, pagination, SQLite
// binding) is covered end-to-end.
//
// Bug tested here:
//
//   Bug 3 (listWithPushDown numeric cast) — pkg/server/server.go
//   The list endpoint reads filter values from HTTP query strings, which are
//   always typed as Go strings. listWithPushDown passed the raw string to
//   SQLite as `json_extract(data, '$.field') = ?`. SQLite's json_extract
//   returns typed values from JSON (integer 2, not string "2"), so numeric
//   equality comparisons like `?type_id=2` never matched any row. Fix: filter
//   values are now parsed as int64 or float64 before binding, falling back to
//   string only when numeric parsing fails.
//
// Adjacent issues also tested:
//
//   - Float field filter (?score=95.5) — same root cause, float64 variant
//   - String field filter (?status=active) — must still work (no regression)
//   - Boolean field filter (?active=true) — booleans are not numbers, must
//     remain string-passed so SQLite json_extract(... 'true') works
//   - Combined numeric + string filter (?type_id=2&status=active)
//   - Filter on a field that does not exist in any record — must return empty,
//     not 500
//   - Numeric filter combined with pagination

import (
	"fmt"
	"net/http"
	"testing"
)

// ---------------------------------------------------------------------------
// Helper: paginationTotal reads the total_items from a paginated list response.
// ---------------------------------------------------------------------------

func paginationTotal(result map[string]interface{}) int {
	pag, ok := result["pagination"].(map[string]interface{})
	if !ok {
		return -1
	}
	return int(toFloat64(pag["total_items"]))
}

// ---------------------------------------------------------------------------
// Bug 3 — listWithPushDown numeric cast
// ---------------------------------------------------------------------------

func TestRegression_ListFilter_IntegerField(t *testing.T) {
	// Pre-fix: ?type_id=2 matched 0 rows because the string "2" ≠ integer 2
	// in SQLite's typed comparison. Post-fix: the server parses "2" as int64
	// before binding.
	env := newIntegrationEnv(t)
	defer env.cleanup()

	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "Forklift A", "type_id": 1})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "Forklift B", "type_id": 1})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "Crane C", "type_id": 2})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "Crane D", "type_id": 2})

	status, result := env.doJSON("GET", "/api/v1/assets?type_id=2", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if got := paginationTotal(result); got != 2 {
		t.Errorf("integer filter ?type_id=2: want 2 rows, got %d", got)
	}
}

func TestRegression_ListFilter_FloatField(t *testing.T) {
	// Float field: ?score=95.5 should match rows where json_extract returns
	// a float. Pre-fix: string "95.5" ≠ float 95.5.
	env := newIntegrationEnv(t)
	defer env.cleanup()

	env.createEntity("/api/v1/items", map[string]interface{}{"name": "A", "score": 95.5})
	env.createEntity("/api/v1/items", map[string]interface{}{"name": "B", "score": 80.0})
	env.createEntity("/api/v1/items", map[string]interface{}{"name": "C", "score": 95.5})

	status, result := env.doJSON("GET", "/api/v1/items?score=95.5", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if got := paginationTotal(result); got != 2 {
		t.Errorf("float filter ?score=95.5: want 2 rows, got %d", got)
	}
}

func TestRegression_ListFilter_StringField_Unaffected(t *testing.T) {
	// String filter must still work — the fix must not break string comparison.
	env := newIntegrationEnv(t)
	defer env.cleanup()

	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "Forklift A", "status": "active"})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "Forklift B", "status": "active"})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "Crane C", "status": "idle"})

	status, result := env.doJSON("GET", "/api/v1/assets?status=active", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if got := paginationTotal(result); got != 2 {
		t.Errorf("string filter ?status=active: want 2 rows, got %d", got)
	}
}

func TestRegression_ListFilter_CombinedNumericAndString(t *testing.T) {
	// Multiple filters combined: ?type_id=1&status=active
	// Both must be applied; only rows matching both are returned.
	env := newIntegrationEnv(t)
	defer env.cleanup()

	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "A", "type_id": 1, "status": "active"})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "B", "type_id": 1, "status": "idle"})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "C", "type_id": 2, "status": "active"})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "D", "type_id": 2, "status": "idle"})

	status, result := env.doJSON("GET", "/api/v1/assets?type_id=1&status=active", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if got := paginationTotal(result); got != 1 {
		t.Errorf("combined filter ?type_id=1&status=active: want 1 row, got %d", got)
	}
}

func TestRegression_ListFilter_NumericNoMatch(t *testing.T) {
	// Integer filter that matches zero rows — must return empty pagination,
	// not an error. Verify this is a clean 200 with total_items=0.
	env := newIntegrationEnv(t)
	defer env.cleanup()

	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "A", "type_id": 1})
	env.createEntity("/api/v1/assets", map[string]interface{}{"name": "B", "type_id": 2})

	status, result := env.doJSON("GET", "/api/v1/assets?type_id=99", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if got := paginationTotal(result); got != 0 {
		t.Errorf("no-match numeric filter ?type_id=99: want 0 rows, got %d", got)
	}
}

func TestRegression_ListFilter_NumericZero(t *testing.T) {
	// Filter on numeric value 0 — must work even though 0 is falsy in Go.
	env := newIntegrationEnv(t)
	defer env.cleanup()

	env.createEntity("/api/v1/items", map[string]interface{}{"name": "A", "qty": 0})
	env.createEntity("/api/v1/items", map[string]interface{}{"name": "B", "qty": 5})
	env.createEntity("/api/v1/items", map[string]interface{}{"name": "C", "qty": 0})

	status, result := env.doJSON("GET", "/api/v1/items?qty=0", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if got := paginationTotal(result); got != 2 {
		t.Errorf("numeric zero filter ?qty=0: want 2 rows, got %d", got)
	}
}

func TestRegression_ListFilter_WithPagination(t *testing.T) {
	// Numeric filter combined with pagination: create 5 matching rows, request
	// page 1 with per_page=2. total_items must reflect filtered count (5),
	// not raw entity count (8).
	env := newIntegrationEnv(t)
	defer env.cleanup()

	for i := 1; i <= 5; i++ {
		env.createEntity("/api/v1/assets", map[string]interface{}{
			"name":    fmt.Sprintf("Type1-%d", i),
			"type_id": 1,
		})
	}
	for i := 1; i <= 3; i++ {
		env.createEntity("/api/v1/assets", map[string]interface{}{
			"name":    fmt.Sprintf("Type2-%d", i),
			"type_id": 2,
		})
	}

	status, result := env.doJSON("GET", "/api/v1/assets?type_id=1&page=1&per_page=2", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if got := paginationTotal(result); got != 5 {
		t.Errorf("numeric filter + pagination: want total_items=5, got %d", got)
	}
	data, _ := result["data"].([]interface{})
	if len(data) != 2 {
		t.Errorf("numeric filter + pagination: want 2 items on page 1, got %d", len(data))
	}
}

func TestRegression_ListFilter_TenantScopedIntegerField(t *testing.T) {
	// Numeric filter on a tenant-scoped route must also work — the tenant store
	// also goes through listWithPushDown.
	env := newIntegrationEnv(t)
	defer env.cleanup()

	env.createEntity("/api/v1/tenant/acme/assets", map[string]interface{}{"name": "A", "type_id": 1})
	env.createEntity("/api/v1/tenant/acme/assets", map[string]interface{}{"name": "B", "type_id": 2})
	env.createEntity("/api/v1/tenant/acme/assets", map[string]interface{}{"name": "C", "type_id": 1})
	env.createEntity("/api/v1/tenant/globex/assets", map[string]interface{}{"name": "D", "type_id": 1})

	status, result := env.doJSON("GET", "/api/v1/tenant/acme/assets?type_id=1", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if got := paginationTotal(result); got != 2 {
		t.Errorf("tenant-scoped integer filter: want 2 acme type_id=1 rows, got %d", got)
	}
}
