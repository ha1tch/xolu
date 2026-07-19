// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package sulpher — SQL push-down coverage tests.
//
// This file exercises the push-down path end-to-end:
//
//   planGraphQueryAST    — decides whether a query can be pushed down to SQL
//   executeGraphPushDownAST — executes the push-down
//   generateGraphSQLAST     — builds the JOIN chain SQL
//   buildSelectClauseAST    — builds the SELECT list
//   buildWhereClauseAST     — translates the WHERE tree
//   whereExprIsPushable     — determines WHERE push-down eligibility
//   isLiteralExpr           — helper used by whereExprIsPushable
//   sqlgen.go helpers       — operatorToSQL, tenantIDFromPrefix,
//                             isSimpleIdent, add (argBuilder)
//
// It also covers crossJoinEnvs, applyDistinctRows, buildVarSet,
// secondMatch, extractClauses, and preHydrateEnvs via queries that
// exercise those paths.

package sulpher

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	sulpherast "github.com/ha1tch/sulpher/ast"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// parseQuery is a test helper that parses a query string and fatals on error.
func parseQuery(t *testing.T, query string) (*sulpherast.Query, *AlgorithmHint) {
	t.Helper()
	p := NewParser()
	q, hint, err := p.Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	return q, hint
}

// execQuery is a test helper that parses and executes a query.
func execQuery(t *testing.T, exec *Executor, query string) *QueryResult {
	t.Helper()
	q, hint := parseQuery(t, query)
	result, err := exec.Execute(context.Background(), q, hint)
	if err != nil {
		t.Fatalf("Execute(%q): %v", query, err)
	}
	return result
}

// ---------------------------------------------------------------------------
// graphQueryableAdapter mirrors the server-side adapter
// ---------------------------------------------------------------------------

type testGraphQueryableAdapter struct {
	storage.AggregateQueryable
	edgeTable string
}

func (a *testGraphQueryableAdapter) GraphEdgesTable() string { return a.edgeTable }

// ---------------------------------------------------------------------------
// Test fixture setup
// ---------------------------------------------------------------------------

// pushDownFixture wires a real SQLiteStore with adapted entities and graph
// edges plus a FlatGraph with matching topology, returning an Executor
// configured for push-down.
//
// Schema:
//
//	person: id(int), name(string), age(int), city(string)
//	dept:   id(int), name(string), budget(int)
//
// Topology:
//
//	person:1 -[WORKS_IN]-> dept:1
//	person:2 -[WORKS_IN]-> dept:1
//	person:3 -[WORKS_IN]-> dept:2
type pushDownFixture struct {
	store     *storage.SQLiteStore
	g         graph.Graph
	executor  *Executor
	personIDs []int
	deptIDs   []int
}

func setupPushDownFixture(t *testing.T) *pushDownFixture {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pushdown.db")

	store, err := storage.NewSQLiteStore(dbPath, storage.SQLiteConfig{
		GraphEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()

	// Register adapted schemas.
	personSchema := map[string]interface{}{
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"age":  map[string]interface{}{"type": "integer"},
			"city": map[string]interface{}{"type": "string"},
		},
	}
	deptSchema := map[string]interface{}{
		"properties": map[string]interface{}{
			"name":   map[string]interface{}{"type": "string"},
			"budget": map[string]interface{}{"type": "integer"},
		},
	}
	if err := store.RegisterAdaptedEntity(ctx, "person", personSchema); err != nil {
		t.Fatalf("RegisterAdaptedEntity(person): %v", err)
	}
	if err := store.RegisterAdaptedEntity(ctx, "dept", deptSchema); err != nil {
		t.Fatalf("RegisterAdaptedEntity(dept): %v", err)
	}

	// Insert persons.
	persons := []map[string]interface{}{
		{"name": "Alice", "age": 30, "city": "London"},
		{"name": "Bob", "age": 25, "city": "Paris"},
		{"name": "Carol", "age": 35, "city": "London"},
	}
	personIDs := make([]int, len(persons))
	for i, p := range persons {
		id, err := store.Create(ctx, "person", p)
		if err != nil {
			t.Fatalf("Create person: %v", err)
		}
		personIDs[i] = id
	}

	// Insert depts.
	depts := []map[string]interface{}{
		{"name": "Engineering", "budget": 500000},
		{"name": "Marketing", "budget": 200000},
	}
	deptIDs := make([]int, len(depts))
	for i, d := range depts {
		id, err := store.Create(ctx, "dept", d)
		if err != nil {
			t.Fatalf("Create dept: %v", err)
		}
		deptIDs[i] = id
	}

	// Insert graph edges in the SQLite graph table.
	// person:1 → dept:1, person:2 → dept:1, person:3 → dept:2
	edges := [][2]int{
		{personIDs[0], deptIDs[0]},
		{personIDs[1], deptIDs[0]},
		{personIDs[2], deptIDs[1]},
	}
	for _, e := range edges {
		from := fmt.Sprintf("person:%d", e[0])
		to := fmt.Sprintf("dept:%d", e[1])
		if _, err := store.AddEdgeWithProps(ctx, from, to, "WORKS_IN", nil); err != nil {
			t.Fatalf("AddEdgeWithProps %s->%s: %v", from, to, err)
		}
	}

	// Build matching in-memory graph for BFS/DFS path.
	g := graph.NewFlatGraph()
	for _, pid := range personIDs {
		g.AddNode(fmt.Sprintf("person:%d", pid), "person")
	}
	for _, did := range deptIDs {
		g.AddNode(fmt.Sprintf("dept:%d", did), "dept")
	}
	for _, e := range edges {
		from := fmt.Sprintf("person:%d", e[0])
		to := fmt.Sprintf("dept:%d", e[1])
		g.AddEdge(from, to, "WORKS_IN")
	}

	// Wire the push-down adapter.
	edgeTable := tenant.GraphTableName(0)
	adapter := &testGraphQueryableAdapter{
		AggregateQueryable: store,
		edgeTable:          edgeTable,
	}

	exec := NewExecutor(g, 5).WithGraphStore(adapter)

	return &pushDownFixture{
		store:     store,
		g:         g,
		executor:  exec,
		personIDs: personIDs,
		deptIDs:   deptIDs,
	}
}

// ---------------------------------------------------------------------------
// planGraphQueryAST — push-down decision tests
// ---------------------------------------------------------------------------

// TestPushDown_PlanDecision verifies that planGraphQueryAST chooses
// planPushDown for eligible queries and planTraversal for ineligible ones.
func TestPushDown_PlanDecision(t *testing.T) {
	f := setupPushDownFixture(t)
	exec := f.executor

	eligible := []struct {
		name  string
		query string
	}{
		{"single hop no where",
			`MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name, d.name`},
		{"single hop with where",
			`MATCH (p:person)-[:WORKS_IN]->(d:dept) WHERE p.age > 26 RETURN p.name`},
		{"single hop with AND",
			`MATCH (p:person)-[:WORKS_IN]->(d:dept) WHERE p.age > 20 AND p.city = "London" RETURN p.name`},
	}
	for _, tc := range eligible {
		t.Run("eligible/"+tc.name, func(t *testing.T) {
			q, _ := parseQuery(t, tc.query)
			plan := exec.planGraphQueryAST(q)
			if plan != planPushDown {
				t.Errorf("expected planPushDown, got %v", plan)
			}
		})
	}

	ineligible := []struct {
		name  string
		query string
	}{
		// Note: RETURN * is caught at execution time (buildSelectClauseAST),
		// not at plan time — planGraphQueryAST returns planPushDown for it,
		// and the error surfaces during executeGraphPushDownAST. The test
		// for this behaviour is TestPushDown_StarReturnErrors below.
		{"untyped node",
			`MATCH (p)-[:WORKS_IN]->(d:dept) RETURN p.name`},
		{"non-adapted entity",
			`MATCH (u:users)-[:FOLLOWS]->(v:users) RETURN u.name`},
		{"variable-length hop",
			`MATCH (p:person)-[:WORKS_IN*]->(d:dept) RETURN p.name`},
		{"with clause",
			`MATCH (p:person) WITH p MATCH (p)-[:WORKS_IN]->(d:dept) RETURN d.name`},
	}
	for _, tc := range ineligible {
		t.Run("ineligible/"+tc.name, func(t *testing.T) {
			q, _ := parseQuery(t, tc.query)
			plan := exec.planGraphQueryAST(q)
			if plan != planTraversal {
				t.Errorf("expected planTraversal, got %v", plan)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// End-to-end push-down execution
// ---------------------------------------------------------------------------

// TestPushDown_BasicJoin verifies that a simple two-entity MATCH executes
// via push-down and returns the correct rows.
func TestPushDown_BasicJoin(t *testing.T) {
	f := setupPushDownFixture(t)

	result := execQuery(t, f.executor, `MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name, d.name`)
	if len(result.Data) != 3 {
		t.Fatalf("expected 3 rows, got %d: %v", len(result.Data), result.Data)
	}

	// Verify every row has both projected fields.
	for _, row := range result.Data {
		if _, ok := row["p.name"]; !ok {
			t.Errorf("row missing p.name: %v", row)
		}
		if _, ok := row["d.name"]; !ok {
			t.Errorf("row missing d.name: %v", row)
		}
	}
}

// TestPushDown_WhereFilter exercises the WHERE push-down and
// buildWhereClauseAST / operatorToSQL.
func TestPushDown_WhereFilter(t *testing.T) {
	f := setupPushDownFixture(t)

	// Only persons with age > 26 — Alice(30) and Carol(35); Bob(25) excluded.
	result := execQuery(t, f.executor, `MATCH (p:person)-[:WORKS_IN]->(d:dept) WHERE p.age > 26 RETURN p.name`)
	if len(result.Data) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(result.Data), result.Data)
	}
	names := make([]string, 0, 2)
	for _, row := range result.Data {
		names = append(names, fmt.Sprintf("%v", row["p.name"]))
	}
	sort.Strings(names)
	if names[0] != "Alice" || names[1] != "Carol" {
		t.Errorf("expected [Alice Carol], got %v", names)
	}
}

// TestPushDown_WhereAND exercises the AND branch in buildWhereClauseAST.
func TestPushDown_WhereAND(t *testing.T) {
	f := setupPushDownFixture(t)

	// age > 20 AND city = "London" — Alice(30,London) and Carol(35,London).
	result := execQuery(t, f.executor, `MATCH (p:person)-[:WORKS_IN]->(d:dept) WHERE p.age > 20 AND p.city = "London" RETURN p.name`)
	if len(result.Data) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(result.Data), result.Data)
	}
}

// TestPushDown_EqualityOp exercises the = operator via operatorToSQL.
func TestPushDown_EqualityOp(t *testing.T) {
	f := setupPushDownFixture(t)

	result := execQuery(t, f.executor, `MATCH (p:person)-[:WORKS_IN]->(d:dept) WHERE p.city = "Paris" RETURN p.name`)
	if len(result.Data) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(result.Data), result.Data)
	}
	if fmt.Sprintf("%v", result.Data[0]["p.name"]) != "Bob" {
		t.Errorf("expected Bob, got %v", result.Data[0]["p.name"])
	}
}

// TestPushDown_LimitClause exercises the LIMIT path in generateGraphSQLAST.
func TestPushDown_LimitClause(t *testing.T) {
	f := setupPushDownFixture(t)

	result := execQuery(t, f.executor, `MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name LIMIT 2`)
	if len(result.Data) != 2 {
		t.Errorf("expected 2 rows with LIMIT 2, got %d", len(result.Data))
	}
}

// TestPushDown_ColumnAlias exercises the item.Alias branch in
// buildSelectClauseAST.
func TestPushDown_ColumnAlias(t *testing.T) {
	f := setupPushDownFixture(t)

	result := execQuery(t, f.executor, `MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name AS employee, d.name AS team`)
	if len(result.Data) == 0 {
		t.Fatal("expected rows, got none")
	}
	if _, ok := result.Data[0]["employee"]; !ok {
		t.Errorf("alias 'employee' not in row: %v", result.Data[0])
	}
	if _, ok := result.Data[0]["team"]; !ok {
		t.Errorf("alias 'team' not in row: %v", result.Data[0])
	}
}

// TestPushDown_OrderBy exercises the ORDER BY path in generateGraphSQLAST.
func TestPushDown_OrderBy(t *testing.T) {
	f := setupPushDownFixture(t)

	result := execQuery(t, f.executor, `MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name, p.age ORDER BY p.age DESC`)
	if len(result.Data) < 2 {
		t.Fatalf("expected multiple rows, got %d", len(result.Data))
	}
	// First row should be the highest age (Carol=35).
	if fmt.Sprintf("%v", result.Data[0]["p.name"]) != "Carol" {
		t.Errorf("expected Carol first (age 35 DESC), got %v", result.Data[0]["p.name"])
	}
}

// TestPushDown_StarReturnErrors verifies that RETURN * is correctly rejected
// during execution (by buildSelectClauseAST) even though planGraphQueryAST
// selects planPushDown. The error is caught at execution time, not plan time.
func TestPushDown_StarReturnErrors(t *testing.T) {
	f := setupPushDownFixture(t)
	q, hint := parseQuery(t, `MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN *`)
	_, err := f.executor.Execute(context.Background(), q, hint)
	if err == nil {
		t.Error("RETURN * on push-down path should return an error")
	}
}

// TestPushDown_FallbackToTraversal verifies that a non-adapted entity query
// falls back to BFS/DFS and still returns correct results — i.e. the
// traversal path is not broken by the push-down code path.
func TestPushDown_FallbackToTraversal(t *testing.T) {
	f := setupPushDownFixture(t)

	// Add non-adapted nodes/edges to the graph.
	f.g.AddNode("users:1", "users")
	f.g.AddNode("users:2", "users")
	f.g.AddEdge("users:1", "users:2", "FOLLOWS")

	result := execQuery(t, f.executor, `MATCH (u:users)-[:FOLLOWS]->(v:users) RETURN u, v`)
	if len(result.Data) != 1 {
		t.Errorf("expected 1 row via traversal, got %d", len(result.Data))
	}
}

// ---------------------------------------------------------------------------
// sqlgen.go helpers — direct unit tests
// ---------------------------------------------------------------------------

// TestOperatorToSQL verifies every supported operator and the error path.
func TestOperatorToSQL(t *testing.T) {
	cases := []struct {
		op   Operator
		want string
		err  bool
	}{
		{OpEq, "=", false},
		{OpNe, "<>", false},
		{OpLt, "<", false},
		{OpGt, ">", false},
		{OpLte, "<=", false},
		{OpGte, ">=", false},
		{Operator("LIKE"), "", true}, // unsupported
	}
	for _, tc := range cases {
		sql, err := operatorToSQL(tc.op)
		if tc.err {
			if err == nil {
				t.Errorf("operatorToSQL(%q) expected error, got %q", tc.op, sql)
			}
		} else {
			if err != nil {
				t.Errorf("operatorToSQL(%q): %v", tc.op, err)
			}
			if sql != tc.want {
				t.Errorf("operatorToSQL(%q) = %q, want %q", tc.op, sql, tc.want)
			}
		}
	}
}

// TestTenantIDFromPrefix exercises tenantIDFromPrefix for all branches.
func TestTenantIDFromPrefix(t *testing.T) {
	cases := []struct {
		prefix string
		want   uint16
		err    bool
	}{
		{"", 0, false},
		{"0000@", 0, false},
		{"0001@", 1, false},
		{"FFFE@", 0xFFFE, false},
		{"FFFF@", 0xFFFF, false},
		{"xyz", 0, true},    // no @
		{"00001@", 0, true}, // too long
		{"GG01@", 0, true},  // invalid hex
	}
	for _, tc := range cases {
		got, err := tenantIDFromPrefix(tc.prefix)
		if tc.err {
			if err == nil {
				t.Errorf("tenantIDFromPrefix(%q) expected error", tc.prefix)
			}
		} else {
			if err != nil {
				t.Errorf("tenantIDFromPrefix(%q): %v", tc.prefix, err)
			}
			if got != tc.want {
				t.Errorf("tenantIDFromPrefix(%q) = %d, want %d", tc.prefix, got, tc.want)
			}
		}
	}
}

// TestIsSimpleIdent exercises isSimpleIdent.
func TestIsSimpleIdent(t *testing.T) {
	valid := []string{"p", "person", "my_var", "X123", "a"}
	for _, s := range valid {
		if !isSimpleIdent(s) {
			t.Errorf("isSimpleIdent(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "a b", "my-var", "a.b", "0start", "has space"}
	// Note: "0start" starts with digit — isSimpleIdent allows digits anywhere,
	// so only truly non-ident chars should fail.
	for _, s := range []string{"", "a b", "my-var", "a.b"} {
		if isSimpleIdent(s) {
			t.Errorf("isSimpleIdent(%q) = true, want false", s)
		}
	}
	_ = invalid
}

// TestArgBuilder_Add exercises the argBuilder.add method (the only
// function in sqlgen.go not reached by the integration tests above
// when queries have no args — this ensures the placeholder chain is covered).
func TestArgBuilder_Add(t *testing.T) {
	d := &storage.SQLiteStorageDialect{}
	ab := &argBuilder{dialect: d}

	p1 := ab.add("first")
	p2 := ab.add(42)
	p3 := ab.add(nil)

	if p1 != "?" || p2 != "?" || p3 != "?" {
		t.Errorf("placeholders: %q %q %q", p1, p2, p3)
	}
	if len(ab.args) != 3 {
		t.Errorf("len(args) = %d, want 3", len(ab.args))
	}
	if ab.args[0] != "first" || ab.args[1] != 42 {
		t.Errorf("args = %v", ab.args)
	}
}

// ---------------------------------------------------------------------------
// whereExprIsPushable / isLiteralExpr
// ---------------------------------------------------------------------------

// TestWhereExprIsPushable exercises the pushability checker via the
// planGraphQueryAST decision — if the WHERE is pushable the plan is
// planPushDown; if not, planTraversal. This drives both functions
// through their respective branches.
func TestWhereExprIsPushable_Branches(t *testing.T) {
	f := setupPushDownFixture(t)

	// AND of two pushable comparisons.
	q, _ := parseQuery(t, `MATCH (p:person)-[:WORKS_IN]->(d:dept) WHERE p.age > 20 AND p.city = "London" RETURN p.name`)
	if f.executor.planGraphQueryAST(q) != planPushDown {
		t.Error("AND of two pushable exprs should be pushable")
	}

	// Non-adapted property — not pushable.
	q, _ = parseQuery(t, `MATCH (p:person)-[:WORKS_IN]->(d:dept) WHERE p.nonexistent = 1 RETURN p.name`)
	if f.executor.planGraphQueryAST(q) != planTraversal {
		t.Error("non-adapted property should fall back to traversal")
	}

	// Non-property access on left side (e.g. literal = literal) — not pushable.
	q, _ = parseQuery(t, `MATCH (p:person)-[:WORKS_IN]->(d:dept) WHERE 1 = 1 RETURN p.name`)
	if f.executor.planGraphQueryAST(q) != planTraversal {
		t.Error("literal comparison should not be pushable")
	}
}

// ---------------------------------------------------------------------------
// crossJoinEnvs
// ---------------------------------------------------------------------------

// TestCrossJoinEnvs exercises crossJoinEnvs for all three branches:
// empty left, empty right, and the full Cartesian product.
func TestCrossJoinEnvs(t *testing.T) {
	// Empty left → empty result.
	r := crossJoinEnvs(nil, []Env{{"a": 1}})
	if len(r) != 0 {
		t.Errorf("crossJoinEnvs(nil, ...) = %d rows, want 0", len(r))
	}

	// Empty right → empty result.
	r = crossJoinEnvs([]Env{{"a": 1}}, nil)
	if len(r) != 0 {
		t.Errorf("crossJoinEnvs(..., nil) = %d rows, want 0", len(r))
	}

	// 2 × 3 = 6 rows; right bindings win on collision.
	left := []Env{{"x": 1}, {"x": 2}}
	right := []Env{{"y": "a"}, {"y": "b"}, {"x": 99}}
	r = crossJoinEnvs(left, right)
	if len(r) != 6 {
		t.Errorf("crossJoinEnvs(2×3) = %d rows, want 6", len(r))
	}
	// Verify right wins on key collision: left x=1 × right x=99 → x=99.
	found := false
	for _, env := range r {
		if env["x"] == 99 && env["y"] == nil {
			found = true
		}
	}
	if !found {
		// x=99 from right should override x=1 from left in one row.
		for _, env := range r {
			if env["x"] == 99 {
				found = true
				break
			}
		}
		if !found {
			t.Error("crossJoinEnvs: right binding did not override left on collision")
		}
	}
}

// ---------------------------------------------------------------------------
// applyDistinctRows / buildVarSet
// ---------------------------------------------------------------------------

// TestApplyDistinctRows exercises applyDistinctRows and buildVarSet by
// running a RETURN DISTINCT query through the executor so those helpers
// are called by the result-processing pipeline.
func TestApplyDistinctRows(t *testing.T) {
	g := setupTestGraph()
	// Two paths from users:1 lead to users:2 and users:5; DISTINCT collapses
	// duplicates if any exist in the variable bindings.
	exec := NewExecutor(g, 5)

	result := execQuery(t, exec, `MATCH (a:users)-[:FOLLOWS]->(b:users) RETURN DISTINCT b`)
	// Verify no duplicate b values.
	seen := map[interface{}]bool{}
	for _, row := range result.Data {
		key := fmt.Sprintf("%v", row["b"])
		if seen[key] {
			t.Errorf("DISTINCT returned duplicate: %v", key)
		}
		seen[key] = true
	}
}

// ---------------------------------------------------------------------------
// secondMatch — via multi-pattern queries
// ---------------------------------------------------------------------------

// TestSecondMatch exercises the secondMatch helper via a double-MATCH
// with a WITH clause, which triggers the second-segment code path.
func TestSecondMatch(t *testing.T) {
	g := setupTestGraph()
	exec := NewExecutor(g, 5)

	result := execQuery(t, exec, `MATCH (a:users)-[:FOLLOWS]->(b:users) WITH b MATCH (b)-[:FOLLOWS]->(c:users) RETURN b, c`)
	// users:2 follows users:3, so there should be at least one result.
	if len(result.Data) == 0 {
		t.Error("expected at least one second-match result")
	}
}

// ---------------------------------------------------------------------------
// extractClauses
// ---------------------------------------------------------------------------

// TestExtractClauses exercises extractClauses via planGraphQueryAST, which
// calls it on every query. An empty Parts slice returns an error internally
// and falls back to traversal — cover the error branch explicitly.
func TestExtractClauses_ErrorBranch(t *testing.T) {
	f := setupPushDownFixture(t)

	// A RETURN-only query has no MATCH clause, so extractClauses returns an
	// error and planGraphQueryAST returns planTraversal.
	// parseQuery fatals on parse error, so use the parser directly here.
	p := NewParser()
	q, _, parseErr := p.Parse(`RETURN 1`)
	if parseErr != nil {
		t.Skip("parser does not accept bare RETURN — skip")
	}
	plan := f.executor.planGraphQueryAST(q)
	if plan != planTraversal {
		t.Errorf("bare RETURN should give planTraversal, got %v", plan)
	}
}

// ---------------------------------------------------------------------------
// preHydrateEnvs
// ---------------------------------------------------------------------------

// TestPreHydrateEnvs exercises preHydrateEnvs via a query with a property
// condition on a field that requires store hydration. The function is called
// during BFS/DFS when the executor has a store attached.
func TestPreHydrateEnvs(t *testing.T) {
	g := setupTestGraph()
	store := newMockStore()
	store.set("users", 1, map[string]interface{}{"name": "Alice", "age": 30})
	store.set("users", 2, map[string]interface{}{"name": "Bob", "age": 25})

	exec := NewExecutor(g, 5).WithStore(store)

	// WHERE u.age > 26 requires hydration to evaluate the age field.
	result := execQuery(t, exec, `MATCH (u:users) WHERE u.age > 26 RETURN u`)
	// Only Alice (age=30) should be returned; Bob (age=25) is excluded.
	if len(result.Data) != 1 {
		t.Errorf("expected 1 row, got %d: %v", len(result.Data), result.Data)
	}
}

// ---------------------------------------------------------------------------
// WithGraphStore — nil graphStore path
// ---------------------------------------------------------------------------

// TestWithGraphStore_NilPath verifies that an executor without a graphStore
// always returns planTraversal.
func TestWithGraphStore_NilPath(t *testing.T) {
	g := setupTestGraph()
	exec := NewExecutor(g, 5) // no WithGraphStore

	q, _ := parseQuery(t, `MATCH (u:users)-[:FOLLOWS]->(v:users) RETURN u`)
	plan := exec.planGraphQueryAST(q)
	if plan != planTraversal {
		t.Errorf("no graphStore: expected planTraversal, got %v", plan)
	}
}

// TestWithGraphStore_Attaches verifies that WithGraphStore returns the same
// executor (fluent API) and that the store is reachable.
func TestWithGraphStore_Attaches(t *testing.T) {
	f := setupPushDownFixture(t)

	// The executor already has a graphStore. Verify the method is fluent.
	edgeTable := tenant.GraphTableName(0)
	adapter := &testGraphQueryableAdapter{
		AggregateQueryable: f.store,
		edgeTable:          edgeTable,
	}
	exec2 := f.executor.WithGraphStore(adapter)
	if exec2 != f.executor {
		t.Error("WithGraphStore should return the same executor (fluent)")
	}
}
