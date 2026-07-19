// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_predicates_test.go — tests for Stage 2.5 (richer WHERE) and
// Stage 2.6 (RETURN enhancements).
//
// All predicates are tested against a graph with a store that supplies
// entity properties, since the executor only evaluates WHERE conditions
// against hydrated node data.

import (
	"context"
	"testing"

	"github.com/ha1tch/xolu/pkg/graph"
)

// ── Test graph setup ──────────────────────────────────────────────────────────

// buildPredicateGraph creates a graph and store for predicate tests.
//
// Nodes:
//
//	users:1  name="Alice"  age=30  email="alice@example.com"  active=true
//	users:2  name="Bob"    age=25  email="bob@corp.org"        active=false
//	users:3  name="Carol"  age=35  email="carol@example.com"  active=true
//	users:4  name=""       age=40  email=nil                   active=true
//	users:5  (no properties in store — bare node)
func buildPredicateGraph() (graph.Graph, *mockStore) {
	g := graph.NewFlatGraph()
	for i := 1; i <= 5; i++ {
		g.AddNode(nodeID("users", i), "users")
	}
	// Add a relationship for join tests
	g.AddEdge(nodeID("users", 1), nodeID("users", 2), "FOLLOWS")

	store := newMockStore()
	store.set("users", 1, map[string]interface{}{
		"name": "Alice", "age": 30, "email": "alice@example.com", "active": true,
	})
	store.set("users", 2, map[string]interface{}{
		"name": "Bob", "age": 25, "email": "bob@corp.org", "active": false,
	})
	store.set("users", 3, map[string]interface{}{
		"name": "Carol", "age": 35, "email": "carol@example.com", "active": true,
	})
	store.set("users", 4, map[string]interface{}{
		"name": "", "age": 40, "active": true,
		// email is intentionally absent — IS NULL test
	})
	// users:5 has no entry in store — all properties are nil

	return g, store
}

func nodeID(entity string, id int) string {
	return entity + ":" + intStr(id)
}

func intStr(i int) string {
	switch i {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	case 5:
		return "5"
	}
	return ""
}

func executeQuery(t *testing.T, query string) []map[string]interface{} {
	t.Helper()
	g, store := buildPredicateGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()
	ast, hint, err := parser.Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute(%q): %v", query, err)
	}
	return result.Data
}

// ── Stage 2.5: IS NULL / IS NOT NULL ─────────────────────────────────────────

func TestWhere_IsNull(t *testing.T) {
	t.Parallel()
	// users:4 has no email field; users:5 has no store entry at all
	rows := executeQuery(t, "MATCH (u:users) WHERE u.email IS NULL RETURN u")
	if len(rows) < 1 {
		t.Errorf("expected at least 1 user with null email, got %d", len(rows))
	}
	// users:1, 2, 3 have email — should not appear
	for _, row := range rows {
		if node, ok := row["u"].(map[string]interface{}); ok {
			if email, ok := node["email"]; ok && email != nil {
				t.Errorf("found non-null email %v in IS NULL result", email)
			}
		}
	}
}

func TestWhere_IsNotNull(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.email IS NOT NULL RETURN u")
	// users:1 (alice@example.com), 2 (bob@corp.org), 3 (carol@example.com)
	if len(rows) != 3 {
		t.Errorf("expected 3 users with non-null email, got %d", len(rows))
	}
	for _, row := range rows {
		if node, ok := row["u"].(map[string]interface{}); ok {
			if email, ok := node["email"]; !ok || email == nil {
				t.Errorf("found null email in IS NOT NULL result")
			}
		}
	}
}

func TestWhere_IsNull_Combined(t *testing.T) {
	t.Parallel()
	// Combine IS NULL with another condition
	rows := executeQuery(t, "MATCH (u:users) WHERE u.email IS NULL AND u.active = true RETURN u")
	// Only users:4 has active=true AND no email
	if len(rows) != 1 {
		t.Errorf("expected 1 result, got %d", len(rows))
	}
}

// ── Stage 2.5: STARTS WITH ───────────────────────────────────────────────────

func TestWhere_StartsWith(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.email STARTS WITH 'alice' RETURN u")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	node := rows[0]["u"].(map[string]interface{})
	if node["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", node["name"])
	}
}

func TestWhere_StartsWith_Multiple(t *testing.T) {
	t.Parallel()
	// alice@example.com and carol@example.com both start with a letter,
	// but both contain @example.com — test domain match via ENDS WITH instead.
	// Here test that multiple nodes match STARTS WITH prefix.
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name STARTS WITH 'A' RETURN u")
	if len(rows) != 1 {
		t.Errorf("expected 1 result (Alice), got %d", len(rows))
	}
}

func TestWhere_NotStartsWith(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE NOT u.email STARTS WITH 'alice' RETURN u")
	// users:2 (bob) and users:3 (carol) have emails not starting with alice
	// users:4 and 5 have null email — STARTS WITH on null gives false, NOT false = true
	// In practice: fmt.Sprintf("%v", nil) = "<nil>" which doesn't start with "alice"
	// So we expect 2, 3, 4, 5 — but let's just check alice is absent
	for _, row := range rows {
		if node, ok := row["u"].(map[string]interface{}); ok {
			if node["name"] == "Alice" {
				t.Error("Alice should not appear in NOT STARTS WITH 'alice' results")
			}
		}
	}
}

// ── Stage 2.5: ENDS WITH ─────────────────────────────────────────────────────

func TestWhere_EndsWith(t *testing.T) {
	t.Parallel()
	// alice and carol both end with @example.com
	rows := executeQuery(t, "MATCH (u:users) WHERE u.email ENDS WITH '@example.com' RETURN u")
	if len(rows) != 2 {
		t.Errorf("expected 2 results (Alice, Carol), got %d", len(rows))
	}
}

func TestWhere_EndsWith_Name(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name ENDS WITH 'ol' RETURN u")
	// Carol ends with "ol"
	if len(rows) != 1 {
		t.Errorf("expected 1 result (Carol), got %d", len(rows))
	}
}

// ── Stage 2.5: CONTAINS ──────────────────────────────────────────────────────

func TestWhere_Contains(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.email CONTAINS 'corp' RETURN u")
	// Only bob@corp.org
	if len(rows) != 1 {
		t.Fatalf("expected 1 result (Bob), got %d", len(rows))
	}
	node := rows[0]["u"].(map[string]interface{})
	if node["name"] != "Bob" {
		t.Errorf("expected Bob, got %v", node["name"])
	}
}

func TestWhere_Contains_Multiple(t *testing.T) {
	t.Parallel()
	// "example" appears in alice@example.com and carol@example.com
	rows := executeQuery(t, "MATCH (u:users) WHERE u.email CONTAINS 'example' RETURN u")
	if len(rows) != 2 {
		t.Errorf("expected 2 results, got %d", len(rows))
	}
}

func TestWhere_NotContains(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE NOT u.email CONTAINS 'example' RETURN u.name")
	// Bob (corp.org) — users:4,5 have nil email
	names := make(map[interface{}]bool)
	for _, row := range rows {
		names[row["u.name"]] = true
	}
	if !names["Bob"] {
		t.Error("expected Bob in NOT CONTAINS 'example' results")
	}
	if names["Alice"] || names["Carol"] {
		t.Error("Alice and Carol should not appear in NOT CONTAINS 'example'")
	}
}

// ── Stage 2.5: IN ─────────────────────────────────────────────────────────────

func TestWhere_In_Integer(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.age IN [25, 35] RETURN u")
	if len(rows) != 2 {
		t.Fatalf("expected 2 results (Bob age=25, Carol age=35), got %d", len(rows))
	}
	names := make(map[interface{}]bool)
	for _, row := range rows {
		if node, ok := row["u"].(map[string]interface{}); ok {
			names[node["name"]] = true
		}
	}
	if !names["Bob"] || !names["Carol"] {
		t.Errorf("expected Bob and Carol, got %v", names)
	}
}

func TestWhere_In_String(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name IN ['Alice', 'Carol'] RETURN u.name")
	if len(rows) != 2 {
		t.Fatalf("expected 2 results, got %d", len(rows))
	}
	names := make(map[interface{}]bool)
	for _, row := range rows {
		names[row["u.name"]] = true
	}
	if !names["Alice"] || !names["Carol"] {
		t.Errorf("expected Alice and Carol, got %v", names)
	}
}

func TestWhere_NotIn(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.age NOT IN [25, 30] RETURN u.age")
	// Carol(35), (40), and nodes with no age
	for _, row := range rows {
		age := row["u.age"]
		if age == 25 || age == 30 {
			t.Errorf("age %v should not appear in NOT IN [25, 30]", age)
		}
	}
}

func TestWhere_In_Single(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name IN ['Bob'] RETURN u")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
}

func TestWhere_In_Empty(t *testing.T) {
	t.Parallel()
	// Nothing is IN an empty list
	rows := executeQuery(t, "MATCH (u:users) WHERE u.age IN [] RETURN u")
	if len(rows) != 0 {
		t.Errorf("expected 0 results for IN [], got %d", len(rows))
	}
}

// ── Stage 2.5: String predicates combined with other conditions ────────────

func TestWhere_StringAndComparison(t *testing.T) {
	t.Parallel()
	// Email contains "example" AND age > 28
	rows := executeQuery(t, "MATCH (u:users) WHERE u.email CONTAINS 'example' AND u.age > 28 RETURN u.name")
	// Alice (30, alice@example.com) and Carol (35, carol@example.com)
	if len(rows) != 2 {
		t.Errorf("expected 2 results, got %d", len(rows))
	}
}

func TestWhere_StartsWith_Or_EndsWith(t *testing.T) {
	t.Parallel()
	// name starts with 'A' OR email ends with '.org'
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name STARTS WITH 'A' OR u.email ENDS WITH '.org' RETURN u.name")
	// Alice (starts with A) + Bob (ends with .org)
	names := make(map[interface{}]bool)
	for _, row := range rows {
		names[row["u.name"]] = true
	}
	if !names["Alice"] {
		t.Error("expected Alice")
	}
	if !names["Bob"] {
		t.Error("expected Bob")
	}
	if names["Carol"] {
		t.Error("Carol should not appear")
	}
}

// ── Stage 2.6: RETURN enhancements ───────────────────────────────────────────

func TestReturn_Alias(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name = 'Alice' RETURN u.name AS username")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	if _, ok := rows[0]["username"]; !ok {
		t.Errorf("expected alias key 'username', got %v", rows[0])
	}
	if rows[0]["username"] != "Alice" {
		t.Errorf("expected Alice, got %v", rows[0]["username"])
	}
}

func TestReturn_MultipleAliases(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name = 'Bob' RETURN u.name AS n, u.age AS a")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	if rows[0]["n"] != "Bob" {
		t.Errorf("expected n=Bob, got %v", rows[0]["n"])
	}
	if rows[0]["a"] != 25 {
		t.Errorf("expected a=25, got %v", rows[0]["a"])
	}
}

func TestReturn_Star(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name = 'Alice' RETURN *")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	// RETURN * should project the 'u' variable as a node map
	row := rows[0]
	if _, ok := row["u"]; !ok {
		t.Errorf("expected 'u' key in RETURN * result, got keys: %v", mapKeys(row))
	}
	node, ok := row["u"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'u' to be a map, got %T", row["u"])
	}
	if node["name"] != "Alice" {
		t.Errorf("expected name=Alice in node, got %v", node["name"])
	}
}

func TestReturn_Star_MultipleVars(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users)-[:FOLLOWS]->(f:users) RETURN *")
	if len(rows) == 0 {
		t.Fatal("expected at least 1 result")
	}
	// Both u and f should be present
	row := rows[0]
	if _, ok := row["u"]; !ok {
		t.Error("expected 'u' in RETURN * result")
	}
	if _, ok := row["f"]; !ok {
		t.Error("expected 'f' in RETURN * result")
	}
}

func TestReturn_ArithmeticInReturn(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name = 'Alice' RETURN u.age + 1 AS nextAge")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	// Alice age=30, 30+1=31
	nextAge := rows[0]["nextAge"]
	if nf, ok := toFloat64(nextAge); !ok || nf != 31 {
		t.Errorf("expected nextAge=31, got %v", nextAge)
	}
}

func TestReturn_ArithmeticMultiply(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name = 'Bob' RETURN u.age * 2 AS doubled")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	// Bob age=25, 25*2=50
	if dbl, ok := toFloat64(rows[0]["doubled"]); !ok || dbl != 50 {
		t.Errorf("expected doubled=50, got %v", rows[0]["doubled"])
	}
}

func TestReturn_WholeNode(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name = 'Carol' RETURN u")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	node, ok := rows[0]["u"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected node map for u")
	}
	if node["name"] != "Carol" {
		t.Errorf("expected Carol, got %v", node["name"])
	}
	if _, ok := node["_id"]; !ok {
		t.Error("expected _id in whole-node RETURN")
	}
}

func TestReturn_IdProperty(t *testing.T) {
	t.Parallel()
	// u.id should return the entity ID string
	rows := executeQuery(t, "MATCH (u:users) WHERE u.name = 'Alice' RETURN u.id")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	if rows[0]["u.id"] != "1" {
		t.Errorf("expected u.id='1', got %v", rows[0]["u.id"])
	}
}

func TestReturn_PropertyAndAlias(t *testing.T) {
	t.Parallel()
	// Mix: one raw property and one alias
	rows := executeQuery(t, "MATCH (u:users) WHERE u.age = 25 RETURN u.name, u.age AS years")
	if len(rows) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rows))
	}
	if rows[0]["u.name"] != "Bob" {
		t.Errorf("expected u.name=Bob, got %v", rows[0]["u.name"])
	}
	if rows[0]["years"] != 25 {
		t.Errorf("expected years=25, got %v", rows[0]["years"])
	}
}

// ── Stage 2.5: NOT operator ───────────────────────────────────────────────────

func TestWhere_NotComparison(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, "MATCH (u:users) WHERE NOT u.active = true RETURN u.name")
	// Bob has active=false; users:5 has no active property (nil != true also passes NOT).
	// At least Bob should be present; Carol/Alice/users:4 should not.
	names := make(map[interface{}]bool)
	for _, row := range rows {
		names[row["u.name"]] = true
	}
	if !names["Bob"] {
		t.Error("expected Bob (active=false) in NOT active=true results")
	}
	if names["Alice"] || names["Carol"] {
		t.Error("Alice and Carol (active=true) should not appear")
	}
}

func TestWhere_NotAnd(t *testing.T) {
	t.Parallel()
	// NOT (age > 30) means age <= 30
	// Alice=30, Bob=25 qualify; Carol=35 does not
	rows := executeQuery(t, "MATCH (u:users) WHERE NOT u.age > 30 RETURN u.name")
	names := make(map[interface{}]bool)
	for _, row := range rows {
		names[row["u.name"]] = true
	}
	if names["Carol"] {
		t.Error("Carol (age=35) should not appear in NOT age > 30")
	}
	if !names["Alice"] {
		t.Error("Alice (age=30) should appear in NOT age > 30")
	}
	if !names["Bob"] {
		t.Error("Bob (age=25) should appear in NOT age > 30")
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ── Stage 2.5 addendum: =~ regex predicate ───────────────────────────────────

func TestWhere_Regex_Match(t *testing.T) {
	t.Parallel()
	// Alice and Carol both have @example.com emails; Bob has @corp.org.
	rows := executeQuery(t, `MATCH (u:users) WHERE u.email =~ '.*@example\\.com' RETURN u.name`)
	if len(rows) != 2 {
		t.Fatalf("=~ example.com: want 2 results, got %d", len(rows))
	}
	names := map[string]bool{}
	for _, row := range rows {
		if n, ok := row["u.name"].(string); ok {
			names[n] = true
		}
	}
	if !names["Alice"] {
		t.Error("=~ example.com: Alice should match")
	}
	if !names["Carol"] {
		t.Error("=~ example.com: Carol should match")
	}
	if names["Bob"] {
		t.Error("=~ example.com: Bob should not match")
	}
}

func TestWhere_Regex_CaseInsensitiveFlag(t *testing.T) {
	t.Parallel()
	// Go regexp supports (?i) inline flag.
	rows := executeQuery(t, `MATCH (u:users) WHERE u.name =~ '(?i)alice' RETURN u.name`)
	if len(rows) != 1 {
		t.Fatalf("=~ (?i)alice: want 1, got %d", len(rows))
	}
	if name, _ := rows[0]["u.name"].(string); name != "Alice" {
		t.Errorf("=~ (?i)alice: want Alice, got %q", name)
	}
}

func TestWhere_Regex_NoMatch(t *testing.T) {
	t.Parallel()
	rows := executeQuery(t, `MATCH (u:users) WHERE u.name =~ 'Zzz.*' RETURN u.name`)
	if len(rows) != 0 {
		t.Errorf("=~ no-match: want 0 results, got %d", len(rows))
	}
}

func TestWhere_Regex_InvalidPattern_NoResults(t *testing.T) {
	t.Parallel()
	// An invalid regex pattern should not panic — it should simply not match.
	rows := executeQuery(t, `MATCH (u:users) WHERE u.name =~ '[invalid' RETURN u.name`)
	if len(rows) != 0 {
		t.Errorf("=~ invalid pattern: want 0 results (no match), got %d", len(rows))
	}
}

func TestWhere_Regex_AnchoredMatch(t *testing.T) {
	t.Parallel()
	// Without anchors, Go regexp.MatchString does full-match; with =~ Cypher
	// typically does full-string match. Go's MatchString matches a substring
	// unless anchored — verify the behaviour is consistent.
	rows := executeQuery(t, `MATCH (u:users) WHERE u.name =~ 'Ali' RETURN u.name`)
	// "Ali" is a substring of "Alice" — Go MatchString returns true for substrings.
	// This is documented behaviour: use anchors (^Alice$) for full-match.
	if len(rows) != 1 {
		t.Errorf("=~ substring 'Ali': want 1 (Alice), got %d", len(rows))
	}
}
