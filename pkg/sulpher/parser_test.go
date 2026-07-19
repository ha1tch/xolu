// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// parser_test.go — tests for the Sulpher parser.
//
// Since parser.Parse now returns *sulpherast.Query directly, these tests
// inspect the Cypher AST rather than the old internal Query struct.
// The same semantic assertions are preserved; only the access path changes.

import (
	"testing"

	sulpherast "github.com/ha1tch/sulpher/ast"
)

// ── AST access helpers ────────────────────────────────────────────────────────

// mustParseQuery parses and returns the first SingleQuery or fails the test.
func mustParseQuery(t *testing.T, q string) *sulpherast.SingleQuery {
	t.Helper()
	parser := NewParser()
	ast, _, err := parser.Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	if len(ast.Parts) == 0 {
		t.Fatalf("Parse(%q): no query parts", q)
	}
	return ast.Parts[0]
}

// getMatch returns the first MatchClause from a SingleQuery or fails.
func getMatch(t *testing.T, sq *sulpherast.SingleQuery) *sulpherast.MatchClause {
	t.Helper()
	for _, c := range sq.Clauses {
		if mc, ok := c.(*sulpherast.MatchClause); ok {
			return mc
		}
	}
	t.Fatal("no MATCH clause found")
	return nil
}

// getReturn returns the ReturnClause or fails.
func getReturn(t *testing.T, sq *sulpherast.SingleQuery) *sulpherast.ReturnClause {
	t.Helper()
	for _, c := range sq.Clauses {
		if rc, ok := c.(*sulpherast.ReturnClause); ok {
			return rc
		}
	}
	t.Fatal("no RETURN clause found")
	return nil
}

// pathSegments extracts []pathSegment from a MATCH clause.
func pathSegs(t *testing.T, mc *sulpherast.MatchClause) []pathSegment {
	t.Helper()
	segs, err := extractPathElements(mc.Pattern)
	if err != nil {
		t.Fatalf("extractPathElements: %v", err)
	}
	return segs
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestParserSimpleQuery(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User) RETURN u")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)

	if len(segs) != 1 {
		t.Fatalf("expected 1 path segment, got %d", len(segs))
	}
	if astNodeVar(segs[0].node) != "u" {
		t.Errorf("expected variable 'u', got %q", astNodeVar(segs[0].node))
	}
	if astNodeType(segs[0].node) != "User" {
		t.Errorf("expected type 'User', got %q", astNodeType(segs[0].node))
	}

	ret := getReturn(t, sq)
	if len(ret.Items) != 1 {
		t.Fatalf("expected 1 RETURN item, got %d", len(ret.Items))
	}
	if ident, ok := ret.Items[0].Expr.(*sulpherast.Identifier); !ok || ident.Value != "u" {
		t.Errorf("expected RETURN u")
	}
}

func TestParserAlgorithmHintBFS(t *testing.T) {
	t.Parallel()
	parser := NewParser()
	_, hint, err := parser.Parse("MATCH (u:User) RETURN u")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if hint.Algorithm != BFS {
		t.Errorf("expected BFS, got %s", hint.Algorithm)
	}
}

func TestParserAlgorithmHintDFS_CommentForm(t *testing.T) {
	t.Parallel()
	parser := NewParser()
	_, hint, err := parser.Parse("// sulpher.algorithm: dfs\nMATCH (u:User)-[:FOLLOWS]->(f) RETURN f")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if hint.Algorithm != DFS {
		t.Errorf("expected DFS, got %s", hint.Algorithm)
	}
}

func TestParserAlgorithmHintBFS_CommentForm(t *testing.T) {
	t.Parallel()
	parser := NewParser()
	_, hint, err := parser.Parse("// sulpher.algorithm: bfs\nMATCH (u:User) RETURN u")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if hint.Algorithm != BFS {
		t.Errorf("expected BFS, got %s", hint.Algorithm)
	}
}

func TestParserAlgorithmHintCaseInsensitive(t *testing.T) {
	t.Parallel()
	parser := NewParser()
	for _, q := range []string{
		"// sulpher.algorithm: DFS\nMATCH (u:User) RETURN u",
		"// sulpher.algorithm: Dfs\nMATCH (u:User) RETURN u",
		"// SULPHER.ALGORITHM: DFS\nMATCH (u:User) RETURN u",
	} {
		_, hint, err := parser.Parse(q)
		if err != nil {
			t.Errorf("Parse(%q): %v", q, err)
			continue
		}
		if hint.Algorithm != DFS {
			t.Errorf("expected DFS for %q, got %s", q, hint.Algorithm)
		}
	}
}

func TestParserAlgorithmHintInvalid(t *testing.T) {
	t.Parallel()
	parser := NewParser()
	_, _, err := parser.Parse("// sulpher.algorithm: random\nMATCH (u:User) RETURN u")
	if err == nil {
		t.Error("expected error for unknown algorithm hint")
	}
}

func TestParserAlgorithmHintDFS_LegacyForm(t *testing.T) {
	t.Parallel()
	// Legacy BFS /DFS prefix still works for backward compatibility.
	parser := NewParser()
	_, hint, err := parser.Parse("DFS MATCH (u:User)-[:FOLLOWS]->(f) RETURN f")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if hint.Algorithm != DFS {
		t.Errorf("expected DFS, got %s", hint.Algorithm)
	}
}

func TestParserWithInlineProperties(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User {id: 123, active: true}) RETURN u")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)

	if len(segs) == 0 {
		t.Fatal("no path segments")
	}
	ml, ok := segs[0].node.Properties.(*sulpherast.MapLiteral)
	if !ok {
		t.Fatalf("expected MapLiteral properties, got %T", segs[0].node.Properties)
	}
	props := make(map[string]interface{})
	for _, pair := range ml.Pairs {
		props[pair.Key.Value] = evalLiteralAST(pair.Value, nil)
	}
	if props["id"] != 123 {
		t.Errorf("expected id=123, got %v", props["id"])
	}
	if props["active"] != true {
		t.Errorf("expected active=true, got %v", props["active"])
	}
}

func TestParserSingleHop(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[r:FOLLOWS]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)

	if len(segs) != 2 {
		t.Fatalf("expected 2 path segments, got %d", len(segs))
	}
	if astNodeVar(segs[0].node) != "u" || astNodeType(segs[0].node) != "User" {
		t.Errorf("first node: var=%q type=%q", astNodeVar(segs[0].node), astNodeType(segs[0].node))
	}
	if segs[0].rel == nil {
		t.Fatal("expected relationship on segment 0")
	}
	if segs[0].rel.Variable == nil || segs[0].rel.Variable.Value != "r" {
		t.Errorf("expected rel variable 'r'")
	}
	if astRelType(segs[0].rel) != "FOLLOWS" {
		t.Errorf("expected rel type 'FOLLOWS', got %q", astRelType(segs[0].rel))
	}
	if astNodeVar(segs[1].node) != "f" || astNodeType(segs[1].node) != "User" {
		t.Errorf("second node: var=%q type=%q", astNodeVar(segs[1].node), astNodeType(segs[1].node))
	}
}

func TestParserMultiHop(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[:FOLLOWS]->(f:User)-[:LIKES]->(p:Post) RETURN p")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)

	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segs))
	}
	if astRelType(segs[0].rel) != "FOLLOWS" {
		t.Errorf("expected FOLLOWS, got %q", astRelType(segs[0].rel))
	}
	if astRelType(segs[1].rel) != "LIKES" {
		t.Errorf("expected LIKES, got %q", astRelType(segs[1].rel))
	}
	if astNodeType(segs[2].node) != "Post" {
		t.Errorf("expected Post, got %q", astNodeType(segs[2].node))
	}
}

func TestParserWithWhere(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[:FOLLOWS]->(f:User) WHERE u.id = 123 RETURN f")
	mc := getMatch(t, sq)

	if mc.Where == nil {
		t.Fatal("expected WHERE clause")
	}
	// WHERE should be: u.id = 123 (an InfixExpression)
	infix, ok := mc.Where.(*sulpherast.InfixExpression)
	if !ok {
		t.Fatalf("expected InfixExpression, got %T", mc.Where)
	}
	if infix.Operator != "=" {
		t.Errorf("expected operator '=', got %q", infix.Operator)
	}
	// Left: u.id (PropertyAccess)
	pa, ok := infix.Left.(*sulpherast.PropertyAccess)
	if !ok {
		t.Fatalf("expected PropertyAccess on left, got %T", infix.Left)
	}
	if ident, ok := pa.Object.(*sulpherast.Identifier); !ok || ident.Value != "u" {
		t.Errorf("expected variable 'u' on left")
	}
	if pa.Property.Value != "id" {
		t.Errorf("expected property 'id', got %q", pa.Property.Value)
	}
	// Right: 123 (IntegerLiteral)
	il, ok := infix.Right.(*sulpherast.IntegerLiteral)
	if !ok {
		t.Fatalf("expected IntegerLiteral on right, got %T", infix.Right)
	}
	if il.Value != 123 {
		t.Errorf("expected 123, got %d", il.Value)
	}
}

func TestParserWithMultipleConditions(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User) WHERE u.age >= 18 AND u.active = true RETURN u")
	mc := getMatch(t, sq)

	if mc.Where == nil {
		t.Fatal("expected WHERE clause")
	}
	// WHERE should be an AND
	infix, ok := mc.Where.(*sulpherast.InfixExpression)
	if !ok || infix.Operator != "AND" {
		t.Fatalf("expected AND expression at top level")
	}
	// Left: u.age >= 18
	left, ok := infix.Left.(*sulpherast.InfixExpression)
	if !ok || left.Operator != ">=" {
		t.Errorf("expected >= on left side of AND")
	}
	// Right: u.active = true
	right, ok := infix.Right.(*sulpherast.InfixExpression)
	if !ok || right.Operator != "=" {
		t.Errorf("expected = on right side of AND")
	}
}

func TestParserReturnProperties(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[r:MANAGES]->(e:Employee) RETURN u.name, e.email, r")
	ret := getReturn(t, sq)

	if len(ret.Items) != 3 {
		t.Fatalf("expected 3 RETURN items, got %d", len(ret.Items))
	}
	// u.name
	if pa, ok := ret.Items[0].Expr.(*sulpherast.PropertyAccess); !ok ||
		pa.Object.(*sulpherast.Identifier).Value != "u" || pa.Property.Value != "name" {
		t.Errorf("first RETURN item: expected u.name")
	}
	// e.email
	if pa, ok := ret.Items[1].Expr.(*sulpherast.PropertyAccess); !ok ||
		pa.Object.(*sulpherast.Identifier).Value != "e" || pa.Property.Value != "email" {
		t.Errorf("second RETURN item: expected e.email")
	}
	// r (bare identifier)
	if ident, ok := ret.Items[2].Expr.(*sulpherast.Identifier); !ok || ident.Value != "r" {
		t.Errorf("third RETURN item: expected r")
	}
}

func TestParserInvalidQueries(t *testing.T) {
	t.Parallel()
	parser := NewParser()

	invalidQueries := []string{
		"",
		"SELECT * FROM users",
		// "MATCH (u) RETURN" — the new Cypher parser parses this as RETURN with an
		// implicit item; it's permissive here. Execution would still fail.
		"MATCH RETURN u",
		"(u:User) RETURN u",
	}
	for _, q := range invalidQueries {
		_, _, err := parser.Parse(q)
		if err == nil {
			t.Errorf("expected error for invalid query: %s", q)
		}
	}
}

func TestParserCaseInsensitive(t *testing.T) {
	t.Parallel()
	parser := NewParser()
	queries := []string{
		"match (u:User) return u",
		"MATCH (u:User) RETURN u",
		"Match (u:User) Return u",
		// Legacy algorithm prefixes also case-insensitive
		"bfs MATCH (u:User) RETURN u",
		"BFS match (u:User) return u",
	}
	for _, q := range queries {
		_, _, err := parser.Parse(q)
		if err != nil {
			t.Errorf("failed to parse case variant: %s - %v", q, err)
		}
	}
}

// ── Variable-length path tests ────────────────────────────────────────────────

func TestParserVariableLengthMinMax(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[:FOLLOWS*1..5]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)

	rel := segs[0].rel
	if !rel.HasRange {
		t.Fatal("expected HasRange")
	}
	min, max := astHops(rel, 100)
	if min != 1 {
		t.Errorf("expected MinHops=1, got %d", min)
	}
	if max != 5 {
		t.Errorf("expected MaxHops=5, got %d", max)
	}
	if astRelType(rel) != "FOLLOWS" {
		t.Errorf("expected FOLLOWS, got %q", astRelType(rel))
	}
}

func TestParserVariableLengthMaxOnly(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[:FOLLOWS*..3]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	rel := segs[0].rel
	min, max := astHops(rel, 100)
	if min != 1 {
		t.Errorf("expected MinHops=1 (default), got %d", min)
	}
	if max != 3 {
		t.Errorf("expected MaxHops=3, got %d", max)
	}
}

func TestParserVariableLengthMinOnly(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[:FOLLOWS*2..]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	rel := segs[0].rel
	min, max := astHops(rel, 0)
	if min != 2 {
		t.Errorf("expected MinHops=2, got %d", min)
	}
	if max != 0 {
		t.Errorf("expected MaxHops=0 (unlimited), got %d", max)
	}
}

func TestParserVariableLengthUnlimited(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[:FOLLOWS*]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	rel := segs[0].rel
	min, max := astHops(rel, 0)
	if min != 1 {
		t.Errorf("expected MinHops=1, got %d", min)
	}
	if max != 0 {
		t.Errorf("expected MaxHops=0 (unlimited), got %d", max)
	}
}

func TestParserVariableLengthExact(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[:FOLLOWS*3]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	rel := segs[0].rel
	if rel.RangeMin == nil || rel.RangeMin.Value != 3 {
		t.Errorf("expected RangeMin=3")
	}
	// Since sulpher v0.2.4, [*3] correctly sets RangeMax=RangeMin so
	// executors can distinguish exact hop count from open-ended [*3..].
	if rel.RangeMax == nil || rel.RangeMax.Value != 3 {
		t.Errorf("expected RangeMax=3 for [*3] (exact hop count), got %v", rel.RangeMax)
	}
}

func TestParserVariableLengthWithVariable(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[r:FOLLOWS*1..5]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	rel := segs[0].rel
	if rel.Variable == nil || rel.Variable.Value != "r" {
		t.Errorf("expected rel variable 'r'")
	}
	if astRelType(rel) != "FOLLOWS" {
		t.Errorf("expected FOLLOWS")
	}
}

func TestParserVariableLengthNoType(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[*1..3]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	rel := segs[0].rel
	if astRelType(rel) != "" {
		t.Errorf("expected empty type, got %q", astRelType(rel))
	}
	min, max := astHops(rel, 100)
	if min != 1 || max != 3 {
		t.Errorf("expected 1..3, got %d..%d", min, max)
	}
}

func TestParserVariableLengthInvalid(t *testing.T) {
	t.Parallel()
	parser := NewParser()
	invalidQueries := []string{
		"MATCH (u:User)-[:FOLLOWS*5..2]->(f:User) RETURN f",
		"MATCH (u:User)-[:FOLLOWS*-1..5]->(f:User) RETURN f",
		"MATCH (u:User)-[:FOLLOWS*abc]->(f:User) RETURN f",
	}
	for _, q := range invalidQueries {
		_, _, err := parser.Parse(q)
		if err == nil {
			t.Errorf("expected error for: %s", q)
		}
	}
}

// ── DISTINCT, LIMIT, ORDER BY ─────────────────────────────────────────────────

func TestParserDistinct(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[:FOLLOWS]->(f:User) RETURN DISTINCT f")
	ret := getReturn(t, sq)
	if !ret.Distinct {
		t.Error("expected Distinct=true")
	}
}

func TestParserLimit(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User) RETURN u LIMIT 10")
	ret := getReturn(t, sq)
	if limit := evalIntExpr(ret.Limit); limit != 10 {
		t.Errorf("expected Limit=10, got %d", limit)
	}
}

func TestParserOrderBy(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User) RETURN u ORDER BY u.name")
	ret := getReturn(t, sq)
	if len(ret.OrderBy) != 1 {
		t.Fatalf("expected 1 ORDER BY, got %d", len(ret.OrderBy))
	}
	if exprToKey(ret.OrderBy[0].Expr) != "u.name" {
		t.Errorf("expected u.name, got %q", exprToKey(ret.OrderBy[0].Expr))
	}
	if ret.OrderBy[0].Descending {
		t.Error("expected ASC by default")
	}
}

func TestParserOrderByDesc(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User) RETURN u ORDER BY u.age DESC")
	ret := getReturn(t, sq)
	if len(ret.OrderBy) == 0 {
		t.Fatal("no ORDER BY")
	}
	if !ret.OrderBy[0].Descending {
		t.Error("expected DESC")
	}
}

func TestParserOrderByMultiple(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User) RETURN u ORDER BY u.name ASC, u.age DESC")
	ret := getReturn(t, sq)
	if len(ret.OrderBy) != 2 {
		t.Fatalf("expected 2 ORDER BY, got %d", len(ret.OrderBy))
	}
	if ret.OrderBy[0].Descending {
		t.Error("expected first item ASC")
	}
	if !ret.OrderBy[1].Descending {
		t.Error("expected second item DESC")
	}
}

func TestParserCombinedClauses(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User) RETURN DISTINCT u ORDER BY u.name LIMIT 5")
	ret := getReturn(t, sq)
	if !ret.Distinct {
		t.Error("expected Distinct")
	}
	if len(ret.OrderBy) != 1 {
		t.Error("expected ORDER BY")
	}
	if evalIntExpr(ret.Limit) != 5 {
		t.Error("expected LIMIT 5")
	}
}

func TestParserWhereOr(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User) WHERE u.name = 'Alice' OR u.name = 'Bob' RETURN u")
	mc := getMatch(t, sq)
	if mc.Where == nil {
		t.Fatal("expected WHERE")
	}
	infix, ok := mc.Where.(*sulpherast.InfixExpression)
	if !ok || infix.Operator != "OR" {
		t.Errorf("expected OR at top level")
	}
}

func TestParserWhereAndOr(t *testing.T) {
	t.Parallel()
	// Cypher precedence: AND binds tighter than OR
	// So: (a AND b) OR (c AND d)
	sq := mustParseQuery(t, "MATCH (u:User) WHERE u.age > 18 AND u.active = true OR u.role = 'admin' AND u.verified = true RETURN u")
	mc := getMatch(t, sq)
	if mc.Where == nil {
		t.Fatal("expected WHERE")
	}
	// Top level should be OR
	top, ok := mc.Where.(*sulpherast.InfixExpression)
	if !ok || top.Operator != "OR" {
		t.Errorf("expected OR at top level, got %T %q", mc.Where, mc.Where.String())
	}
}

// ── Direction tests ───────────────────────────────────────────────────────────

func TestParserBidirectionalUndirected(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)-[r:KNOWS]-(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	if astRelDirection(segs[0].rel) != RelBidirectional {
		t.Errorf("expected bidirectional, got %v", astRelDirection(segs[0].rel))
	}
}

func TestParserIncoming(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)<-[r:FOLLOWS]-(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	if astRelDirection(segs[0].rel) != RelIncoming {
		t.Errorf("expected incoming, got %v", astRelDirection(segs[0].rel))
	}
}

func TestParserBidirectionalBothArrows(t *testing.T) {
	t.Parallel()
	sq := mustParseQuery(t, "MATCH (u:User)<-[r:KNOWS]->(f:User) RETURN f")
	mc := getMatch(t, sq)
	segs := pathSegs(t, mc)
	if astRelDirection(segs[0].rel) != RelBidirectional {
		t.Errorf("expected bidirectional, got %v", astRelDirection(segs[0].rel))
	}
}
