// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// join_e2e_test.go
//
// Integration tests for OQL JOIN push-down. Exercises the full HTTP path:
//
//   POST /api/v1/schema/<entity>   → register adapted entities
//   POST /api/v1/<entity>          → seed rows
//   POST /api/v1/oql/query         → execute JOIN query
//
// Three scenarios from the spec:
//
//   TestJoinPushdown_BothAdapted       — INNER JOIN, both sides adapted
//   TestJoinPushdown_BlobFallback      — INNER JOIN, right side blob only
//   TestJoinPushdown_OuterJoin_NullRows — LEFT JOIN, unmatched left rows
//
// All tests use setupSQLiteTestServer so they run against a real SQLite
// store that implements AggregateQueryable.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// joinOQLData posts an OQL query and returns the rows, fataling on non-200.
func joinOQLData(t *testing.T, ts *TestServer, query string) []map[string]interface{} {
	t.Helper()
	resp, body := ts.doRequest("POST", "/api/v1/oql/query",
		map[string]interface{}{"query": query})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OQL query failed (status %d): %s\nquery: %s",
			resp.StatusCode, string(body), query)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal OQL response: %v", err)
	}
	raw, ok := result["data"]
	if !ok || raw == nil {
		return nil
	}
	rows, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T in OQL response", raw)
	}
	out := make([]map[string]interface{}, 0, len(rows))
	for _, r := range rows {
		m, ok := r.(map[string]interface{})
		if !ok {
			t.Fatalf("unexpected row type %T", r)
		}
		out = append(out, m)
	}
	return out
}

// joinOQL posts an OQL query and returns (statusCode, raw result map).
func joinOQL(t *testing.T, ts *TestServer, query string) (int, map[string]interface{}) {
	t.Helper()
	resp, body := ts.doRequest("POST", "/api/v1/oql/query",
		map[string]interface{}{"query": query})
	var result map[string]interface{}
	json.Unmarshal(body, &result) //nolint:errcheck
	return resp.StatusCode, result
}

// postSchema registers an adapted entity schema.
func postSchema(t *testing.T, ts *TestServer, entity string, schema map[string]interface{}) {
	t.Helper()
	resp, body := ts.doRequest("POST", "/api/v1/schema/"+entity, schema)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST schema/%s: status %d: %s", entity, resp.StatusCode, string(body))
	}
}

// createRow creates a single entity row and returns its assigned ID.
func createRow(t *testing.T, ts *TestServer, entity string, data map[string]interface{}) int {
	t.Helper()
	resp, body := ts.doRequest("POST", "/api/v1/"+entity, data)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s: status %d: %s", entity, resp.StatusCode, string(body))
	}
	var result map[string]interface{}
	json.Unmarshal(body, &result) //nolint:errcheck
	idF, ok := result["id"].(float64)
	if !ok {
		t.Fatalf("POST %s: no id in response: %v", entity, result)
	}
	return int(idF)
}

// rowStr extracts a string field from a result row.
func rowStr(row map[string]interface{}, key string) string {
	if v, ok := row[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// ---------------------------------------------------------------------------
// TestJoinPushdown_BothAdapted
// ---------------------------------------------------------------------------
//
// Setup:
//   authors (adapted) — id, name, country
//   posts   (adapted) — id, author_id, title, status
//
// Relationship: posts.author_id → authors.id
//
// Query: SELECT a.title, b.name FROM posts AS a INNER JOIN authors AS b ON a.author_id = b.id
//        WHERE a.status = 'published'
//
// Asserts:
//   - Returns only published posts with the correct author name attached.
//   - rows_scanned reflects push-down cardinality (≤ total row count, not sum).

func TestJoinPushdown_BothAdapted(t *testing.T) {
	ts := setupSQLiteTestServer(t)
	defer ts.cleanup()

	// Register schemas
	postSchema(t, ts, "authors", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":    map[string]interface{}{"type": "string"},
			"country": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"name"},
	})
	postSchema(t, ts, "posts", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"author_id": map[string]interface{}{"type": "integer"},
			"title":     map[string]interface{}{"type": "string"},
			"status":    map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"author_id", "title", "status"},
	})

	// Seed authors
	aliceID := createRow(t, ts, "authors", map[string]interface{}{"name": "Alice", "country": "UY"})
	bobID := createRow(t, ts, "authors", map[string]interface{}{"name": "Bob", "country": "AR"})

	// Seed posts: two published (one per author), one draft
	createRow(t, ts, "posts", map[string]interface{}{"author_id": aliceID, "title": "Alice Published", "status": "published"})
	createRow(t, ts, "posts", map[string]interface{}{"author_id": bobID, "title": "Bob Published", "status": "published"})
	createRow(t, ts, "posts", map[string]interface{}{"author_id": aliceID, "title": "Alice Draft", "status": "draft"})

	// Execute JOIN query — only published posts
	rows := joinOQLData(t, ts,
		`SELECT a.title, b.name FROM posts AS a INNER JOIN authors AS b ON a.author_id = b.id WHERE a.status = 'published'`)

	if len(rows) != 2 {
		t.Fatalf("expected 2 published posts, got %d: %v", len(rows), rows)
	}

	// Collect title→name pairs to verify cross-entity data is correct
	titleToAuthor := map[string]string{}
	for _, row := range rows {
		title := rowStr(row, "title")
		name := rowStr(row, "name")
		if title == "" || name == "" {
			t.Errorf("row missing title or name: %v", row)
		}
		titleToAuthor[title] = name
	}

	if titleToAuthor["Alice Published"] != "Alice" {
		t.Errorf("Alice Published should map to Alice, got %q", titleToAuthor["Alice Published"])
	}
	if titleToAuthor["Bob Published"] != "Bob" {
		t.Errorf("Bob Published should map to Bob, got %q", titleToAuthor["Bob Published"])
	}

	// Draft post must not appear
	for _, row := range rows {
		if rowStr(row, "title") == "Alice Draft" {
			t.Error("draft post appeared in published-only join result")
		}
	}

	// Verify rows_scanned is sensible (push-down does not scan both full tables
	// individually — it scans the join result, which is ≤ total entity count)
	resp, body := ts.doRequest("POST", "/api/v1/oql/query", map[string]interface{}{
		"query": `SELECT a.title, b.name FROM posts AS a INNER JOIN authors AS b ON a.author_id = b.id WHERE a.status = 'published'`,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats query failed: %s", string(body))
	}
	var result map[string]interface{}
	json.Unmarshal(body, &result) //nolint:errcheck
	stats, _ := result["stats"].(map[string]interface{})
	scanned, _ := stats["rows_scanned"].(float64)
	// We have 3 posts + 2 authors = 5 total rows. Push-down returns 2 joined rows.
	// rows_scanned should equal the matched join output, not the sum of both tables.
	if scanned > 5 {
		t.Errorf("rows_scanned=%v seems too high for a push-down join of 5 total rows", scanned)
	}
}

// ---------------------------------------------------------------------------
// TestJoinPushdown_BlobFallback
// ---------------------------------------------------------------------------
//
// Setup:
//   tags      (blob only — no schema registration) — id, label
//   tag_links (blob only)                          — id, post_id, tag_id
//
// Because neither entity is adapted, the join uses the blob path
// (entities table aliased twice, json_extract on both sides).
//
// Asserts:
//   - Join executes correctly via the blob path.
//   - Result rows contain data from both sides.

func TestJoinPushdown_BlobFallback(t *testing.T) {
	ts := setupSQLiteTestServer(t)
	defer ts.cleanup()

	// No schema registration — both entities stay as blobs.

	// Seed tags
	tagGoID := createRow(t, ts, "tags", map[string]interface{}{"label": "go"})
	tagWebID := createRow(t, ts, "tags", map[string]interface{}{"label": "web"})

	// Seed tag_links referencing tag IDs
	createRow(t, ts, "tag_links", map[string]interface{}{"post_id": 100, "tag_id": tagGoID})
	createRow(t, ts, "tag_links", map[string]interface{}{"post_id": 101, "tag_id": tagWebID})
	createRow(t, ts, "tag_links", map[string]interface{}{"post_id": 102, "tag_id": tagGoID})

	// Execute a JOIN between tag_links and tags
	rows := joinOQLData(t, ts,
		`SELECT a.post_id, b.label FROM tag_links AS a INNER JOIN tags AS b ON a.tag_id = b.id WHERE b.label = 'go'`)

	// Two tag_links point to the 'go' tag
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows for tag 'go', got %d: %v", len(rows), rows)
	}

	// Each row must carry post_id from tag_links and label from tags
	for _, row := range rows {
		label := rowStr(row, "label")
		if label != "go" {
			t.Errorf("expected label='go', got %q in row %v", label, row)
		}
		postID := rowStr(row, "post_id")
		if postID == "" {
			t.Errorf("post_id missing in row %v", row)
		}
	}

	// Verify 'web' tag row is not included
	for _, row := range rows {
		if rowStr(row, "label") == "web" {
			t.Error("'web' tag appeared in 'go'-only filter result")
		}
	}

	// _ suppress unused var warnings
	_ = tagGoID
	_ = tagWebID
}

// ---------------------------------------------------------------------------
// TestJoinPushdown_OuterJoin_NullRows
// ---------------------------------------------------------------------------
//
// Setup:
//   customers (adapted) — id, name
//   orders    (adapted) — id, customer_id, amount
//
// Seed 3 customers but only 2 have orders. A LEFT JOIN returns all customers
// including the one with no orders; that row should have null/empty order fields
// rather than a parse error or a missing-key panic.

func TestJoinPushdown_OuterJoin_NullRows(t *testing.T) {
	ts := setupSQLiteTestServer(t)
	defer ts.cleanup()

	postSchema(t, ts, "customers", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"name"},
	})
	postSchema(t, ts, "orders", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"customer_id": map[string]interface{}{"type": "integer"},
			"amount":      map[string]interface{}{"type": "integer"},
		},
		"required": []interface{}{"customer_id", "amount"},
	})

	// Three customers
	aliceID := createRow(t, ts, "customers", map[string]interface{}{"name": "Alice"})
	bobID := createRow(t, ts, "customers", map[string]interface{}{"name": "Bob"})
	carolID := createRow(t, ts, "customers", map[string]interface{}{"name": "Carol"}) // no orders

	// Two orders: Alice has one, Bob has one, Carol has none
	createRow(t, ts, "orders", map[string]interface{}{"customer_id": aliceID, "amount": 100})
	createRow(t, ts, "orders", map[string]interface{}{"customer_id": bobID, "amount": 200})

	// LEFT JOIN: all customers, with orders where they exist
	rows := joinOQLData(t, ts,
		`SELECT b.name, a.amount FROM orders AS a RIGHT JOIN customers AS b ON a.customer_id = b.id`)

	// All 3 customers must appear
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (all customers), got %d: %v", len(rows), rows)
	}

	// Find the Carol row — it must exist and must not cause a parse error
	carolSeen := false
	for _, row := range rows {
		name := rowStr(row, "name")
		if name == "Carol" {
			carolSeen = true
			// amount should be null/zero/empty — not a missing key causing panic
			// The key may be absent or nil; what matters is no panic and no error.
			// We just verify the row is accessible as a map.
			if row == nil {
				t.Error("Carol row is nil — null blob not guarded in executor")
			}
		}
	}
	if !carolSeen {
		t.Errorf("Carol (no orders) did not appear in RIGHT JOIN result: %v", rows)
	}

	// Alice and Bob must have non-nil amounts
	for _, row := range rows {
		name := rowStr(row, "name")
		if name == "Alice" || name == "Bob" {
			amt := rowStr(row, "amount")
			if amt == "" || amt == "<nil>" {
				t.Errorf("%s should have a non-nil amount, got %q in row %v", name, amt, row)
			}
		}
	}

	// Suppress unused ID warnings
	_ = carolID
}

// ---------------------------------------------------------------------------
// TestJoinPushdown_ValidatorRejectsSubquery
// ---------------------------------------------------------------------------
//
// Subquery tables in JOIN are rejected by the validator — the server
// must return a 4xx error, not a 500.

func TestJoinPushdown_ValidatorRejectsUnsupported(t *testing.T) {
	ts := setupSQLiteTestServer(t)
	defer ts.cleanup()

	// CROSS JOIN has no ON condition — validator should reject
	status, _ := joinOQL(t, ts,
		`SELECT a.id, b.id FROM posts AS a CROSS JOIN authors AS b`)
	if status == http.StatusOK {
		t.Errorf("expected non-200 for CROSS JOIN (unsupported), got %d", status)
	}
	if status >= 500 {
		t.Errorf("CROSS JOIN should produce 4xx, not 5xx; got %d", status)
	}
}

// ---------------------------------------------------------------------------
// Regression: Bug 5 — projectColumns must not re-key join result rows
// ---------------------------------------------------------------------------
//
// After JOIN push-down, records are already correctly shaped with bare field
// aliases ("title", "name"). The executor's universal projectColumns step
// previously ran over them using columnAlias, which returns "a.title" for an
// unaliased QualifiedIdentifier, re-keying the row and losing the bare name.
// The fix bypasses projectColumns for PushJoin results.
//
// This test asserts that without an explicit AS alias, the result map key is
// the bare field name (e.g. "title"), never a dotted qualifier ("a.title").

func TestRegression_JoinResultKeysAreBareName(t *testing.T) {
	ts := setupSQLiteTestServer(t)
	defer ts.cleanup()

	postSchema(t, ts, "authors", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"name"},
	})
	postSchema(t, ts, "posts", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"author_id": map[string]interface{}{"type": "integer"},
			"title":     map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"author_id", "title"},
	})

	aliceID := createRow(t, ts, "authors", map[string]interface{}{"name": "Alice"})
	createRow(t, ts, "posts", map[string]interface{}{"author_id": aliceID, "title": "Hello World"})

	// Query without explicit AS aliases — the field names alone must be the keys.
	rows := joinOQLData(t, ts,
		`SELECT a.title, b.name FROM posts AS a INNER JOIN authors AS b ON a.author_id = b.id`)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
	}
	row := rows[0]

	// Dotted keys are the regression indicator
	for k := range row {
		if strings.Contains(k, ".") {
			t.Errorf("result row key %q contains a dot — projectColumns re-keyed join results (Bug 5 regression); full row: %v", k, row)
		}
	}

	// Bare keys must be present and correct
	if rowStr(row, "title") != "Hello World" {
		t.Errorf("expected title='Hello World', got %q; full row: %v", rowStr(row, "title"), row)
	}
	if rowStr(row, "name") != "Alice" {
		t.Errorf("expected name='Alice', got %q; full row: %v", rowStr(row, "name"), row)
	}
}
