// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_helpers.go — shared AST utilities used by executor_env.go
//
// This file contains the new execution path that walks the *sulpherast.Query
// directly, eliminating the bridge.go translation layer. Once this is wired
// in and all tests pass, bridge.go and the internal Query/PathElement/etc.
// structs are deleted.
//
// The traversal logic is unchanged from executor.go; only the input types
// change from internal structs to AST types.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	sulpherast "github.com/ha1tch/sulpher/ast"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/qs"
)

// algHint is the resolved traversal algorithm for a single query execution.
type algHint int

const (
	AlgBFS algHint = iota
	AlgDFS
)

// ── Entry points ─────────────────────────────────────────────────────────────

// ── Clause extraction ────────────────────────────────────────────────────────

// pathSegment is a (NodePattern, optional RelationshipPattern) pair.
type pathSegment struct {
	node *sulpherast.NodePattern
	rel  *sulpherast.RelationshipPattern // nil for last node
}

// extractClauses pulls the MATCH and RETURN clauses from a SingleQuery.
// For queries with OPTIONAL MATCH, the optional clause is collected but not
// returned by this function — use extractAllClauses instead.
// Returns an error for unsupported clause types (WITH, UNWIND, etc.).
func extractClauses(sq *sulpherast.SingleQuery) (*sulpherast.MatchClause, *sulpherast.ReturnClause, error) {
	cs, err := extractAllClauses(sq)
	if err != nil {
		return nil, nil, err
	}
	return cs.match, cs.ret, nil
}

// clauseSet holds the full set of clauses for a SingleQuery.
// withStage represents one WITH clause and the optional MATCH that follows it.
// A query MATCH→WITH→MATCH→WITH→MATCH→RETURN has two withStages.
type withStage struct {
	with  *sulpherast.WithClause
	match *sulpherast.MatchClause // nil when no MATCH follows this WITH
}

type clauseSet struct {
	match           *sulpherast.MatchClause
	optionalMatches []*sulpherast.MatchClause // all OPTIONAL MATCH clauses, in order
	withStages      []withStage               // ordered WITH (+ optional post-WITH MATCH) chain
	unwind          *sulpherast.UnwindClause  // UNWIND clause, or nil
	ret             *sulpherast.ReturnClause
}

// withClause returns the first WITH clause, or nil (backward compatibility accessor).
func (cs *clauseSet) withClause() *sulpherast.WithClause {
	if len(cs.withStages) == 0 {
		return nil
	}
	return cs.withStages[0].with
}

// extractAllClauses pulls all clauses including OPTIONAL MATCH, WITH, UNWIND.
// Multiple WITH clauses are supported; each becomes a withStage with an optional
// post-WITH MATCH. The pattern MATCH→WITH→MATCH→WITH→RETURN produces two stages:
//
//	stage[0]: {with: W1, match: M2}
//	stage[1]: {with: W2, match: nil}
func extractAllClauses(sq *sulpherast.SingleQuery) (*clauseSet, error) {
	cs := &clauseSet{}
	// pendingWith holds the most recently seen WITH clause waiting for a
	// potential following MATCH to be attached to it.
	var pendingWith *sulpherast.WithClause

	for _, clause := range sq.Clauses {
		switch c := clause.(type) {
		case *sulpherast.MatchClause:
			if c.Optional {
				cs.optionalMatches = append(cs.optionalMatches, c)
			} else if pendingWith != nil {
				// This MATCH follows a WITH — attach it and seal the stage.
				cs.withStages = append(cs.withStages, withStage{with: pendingWith, match: c})
				pendingWith = nil
			} else {
				if cs.match != nil {
					return nil, fmt.Errorf("multiple MATCH clauses before a WITH are not supported")
				}
				cs.match = c
			}
		case *sulpherast.WithClause:
			if pendingWith != nil {
				// Previous WITH had no following MATCH — seal it now.
				cs.withStages = append(cs.withStages, withStage{with: pendingWith, match: nil})
			}
			pendingWith = c
		case *sulpherast.UnwindClause:
			cs.unwind = c
		case *sulpherast.ReturnClause:
			cs.ret = c
		default:
			return nil, fmt.Errorf("unsupported clause %T", clause)
		}
	}
	// Seal any trailing WITH that had no following MATCH.
	if pendingWith != nil {
		cs.withStages = append(cs.withStages, withStage{with: pendingWith, match: nil})
	}

	if cs.match == nil && cs.unwind == nil {
		return nil, fmt.Errorf("query must contain a MATCH or UNWIND clause")
	}
	if cs.ret == nil {
		return nil, fmt.Errorf("query must contain a RETURN clause")
	}
	return cs, nil
}

// extractPathElements converts a *sulpherast.Pattern into a []pathSegment.
// Only single-part linear patterns are supported.
func extractPathElements(pat *sulpherast.Pattern) ([]pathSegment, error) {
	if pat == nil || len(pat.Parts) == 0 {
		return nil, fmt.Errorf("empty pattern")
	}
	// Single-part path: return the linear segment chain directly.
	// Multi-part (comma-separated) callers should use extractAllPathParts.
	if len(pat.Parts) > 1 {
		return nil, fmt.Errorf("comma-separated patterns: use extractAllPathParts")
	}
	return extractPartElements(pat.Parts[0])
}

// extractAllPathParts returns one []pathSegment per comma-separated pattern part.
// MATCH (a:User), (b:Item)-[:OWNS]->(c:Product) returns two segment slices.
// Each slice is evaluated independently; results are cross-joined by the caller.
func extractAllPathParts(pat *sulpherast.Pattern) ([][]pathSegment, error) {
	if pat == nil || len(pat.Parts) == 0 {
		return nil, fmt.Errorf("empty pattern")
	}
	allParts := make([][]pathSegment, 0, len(pat.Parts))
	for i, part := range pat.Parts {
		segs, err := extractPartElements(part)
		if err != nil {
			return nil, fmt.Errorf("pattern part %d: %w", i, err)
		}
		allParts = append(allParts, segs)
	}
	return allParts, nil
}

// extractPartElements converts a single PatternPart into a []pathSegment.
func extractPartElements(part *sulpherast.PatternPart) ([]pathSegment, error) {
	var segments []pathSegment
	elems := part.Elements

	for i := 0; i < len(elems); {
		np, ok := elems[i].(*sulpherast.NodePattern)
		if !ok {
			return nil, fmt.Errorf("expected node at element %d", i)
		}
		seg := pathSegment{node: np}
		if i+1 < len(elems) {
			rp, ok := elems[i+1].(*sulpherast.RelationshipPattern)
			if !ok {
				return nil, fmt.Errorf("expected relationship at element %d", i+1)
			}
			seg.rel = rp
			i += 2
		} else {
			i++
		}
		segments = append(segments, seg)
	}
	return segments, nil
}

// ── shortestPath pattern detection ─────────────────────────────────────────

// isShortestPathPattern detects the shortestPath and allShortestPaths pattern form:
//
//	MATCH p = shortestPath((a:Type)-[:REL*]-(b:Type))
//	MATCH p = allShortestPaths((a:Type)-[:REL*]-(b:Type))
//
// This appears in the AST as a PatternPart with a bound variable and a
// variable-length relationship between exactly two node patterns.
// Returns (pathVariable, isAll, true) when matched.
func isShortestPathPattern(pat *sulpherast.Pattern) (string, bool, bool) {
	if pat == nil {
		return "", false, false
	}
	for _, part := range pat.Parts {
		if part.Variable == nil {
			continue
		}
		// Must have exactly 3 elements: node, relationship, node
		if len(part.Elements) != 3 {
			continue
		}
		_, n1ok := part.Elements[0].(*sulpherast.NodePattern)
		rp, relok := part.Elements[1].(*sulpherast.RelationshipPattern)
		_, n2ok := part.Elements[2].(*sulpherast.NodePattern)
		if n1ok && relok && n2ok && rp.HasRange {
			return part.Variable.Value, part.All, true
		}
	}
	return "", false, false
}

// ── Path traversal helpers ──────────────────────────────────────────────────

// fullNodeID prepends the tenant prefix to a bare node ID when the executor
// is tenant-scoped. The FlatGraph stores prefixed IDs; the snapshot strips
// them, so we need to re-add when calling graph methods directly.
func (e *Executor) fullNodeID(bareID string) string {
	if e.tenantPrefix == "" {
		return bareID
	}
	return e.tenantPrefix + bareID
}

// pathMatchesRelType checks whether every edge along a path has the given
// relationship type. Uses the snapshot's adjacency map.
func (e *Executor) pathMatchesRelType(snapshot *graphSnapshot, pathIDs []string, relType string) bool {
	for i := 0; i < len(pathIDs)-1; i++ {
		src := pathIDs[i]
		dst := pathIDs[i+1]
		if e.tenantPrefix != "" {
			src = strings.TrimPrefix(src, e.tenantPrefix)
			dst = strings.TrimPrefix(dst, e.tenantPrefix)
		}
		// Check outgoing edge src→dst first.
		if neighbors, ok := snapshot.adjacency[src]; ok {
			if ref, ok := neighbors[dst]; ok {
				if ref.Rel != relType {
					return false
				}
				continue
			}
		}
		// Check reverse: the path followed an incoming edge (dst→src stored).
		if neighbors, ok := snapshot.adjacency[dst]; ok {
			if ref, ok := neighbors[src]; ok {
				if ref.Rel != relType {
					return false
				}
				continue
			}
		}
		return false
	}
	return true
}

// buildPathEdgeLabels extracts the relationship label for each hop in a path.
// Returns a slice of length len(pathIDs)-1. Uses the snapshot adjacency map.
func (e *Executor) buildPathEdgeLabels(snapshot *graphSnapshot, pathIDs []string) []string {
	if len(pathIDs) < 2 {
		return nil
	}
	labels := make([]string, len(pathIDs)-1)
	for i := 0; i < len(pathIDs)-1; i++ {
		src := pathIDs[i]
		dst := pathIDs[i+1]
		if e.tenantPrefix != "" {
			src = strings.TrimPrefix(src, e.tenantPrefix)
			dst = strings.TrimPrefix(dst, e.tenantPrefix)
		}
		// Check outgoing adjacency first, then reverse (for undirected paths).
		if neighbors, ok := snapshot.adjacency[src]; ok {
			if ref, ok := neighbors[dst]; ok {
				labels[i] = ref.Rel
				continue
			}
		}
		// Check reverse (incoming) direction — for PathDirIncoming or PathDirAny.
		if neighbors, ok := snapshot.revAdjacency[src]; ok {
			if ref, ok := neighbors[dst]; ok {
				labels[i] = ref.Rel
			}
		}
	}
	return labels
}

// ── Start node matching ──────────────────────────────────────────────────────

// findMatchingNodesAST finds all start nodes matching the first path segment.
func (e *Executor) findMatchingNodesAST(ctx context.Context, snapshot *graphSnapshot, seg pathSegment, env Env) []string {
	nodeType := astNodeType(seg.node)

	var candidates []string
	if nodeType != "" {
		if e.tenantPrefix != "" {
			typed, err := e.graph.GetNodesByTypeForTenant(e.tenantPrefix, nodeType)
			if err == nil {
				prefixLen := len(e.tenantPrefix)
				for _, nodeID := range typed {
					if strings.HasPrefix(nodeID, e.tenantPrefix) {
						candidates = append(candidates, nodeID[prefixLen:])
					} else {
						candidates = append(candidates, nodeID)
					}
				}
			}
		} else {
			candidates = e.graph.GetNodesByType(nodeType)
		}
	} else {
		for nodeID := range snapshot.adjacency {
			candidates = append(candidates, nodeID)
		}
	}

	var matches []string
	for _, nodeID := range candidates {
		if e.matchesNodePatternAST(ctx, nodeID, snapshot.nodeData[nodeID], seg.node, snapshot, env) {
			matches = append(matches, nodeID)
		}
	}
	sort.Strings(matches)
	return matches
}

// astNodeType extracts the first label from a NodePattern, or "" if unlabelled.
func astNodeType(np *sulpherast.NodePattern) string {
	if np == nil || len(np.Labels) == 0 {
		return ""
	}
	return np.Labels[0].Value
}

// astNodeVar extracts the variable name from a NodePattern, or "".
func astNodeVar(np *sulpherast.NodePattern) string {
	if np == nil || np.Variable == nil {
		return ""
	}
	return np.Variable.Value
}

// matchesNodePatternAST checks whether a node satisfies a NodePattern.
func (e *Executor) matchesNodePatternAST(ctx context.Context, nodeID string, nodeData map[string]interface{}, np *sulpherast.NodePattern, snapshot *graphSnapshot, env Env) bool {
	// Strip tenant prefix for bare entity:id parsing.
	bare := nodeID
	if e.tenantPrefix != "" && strings.HasPrefix(nodeID, e.tenantPrefix) {
		bare = nodeID[len(e.tenantPrefix):]
	}

	// Type check.
	if len(np.Labels) > 0 {
		parts := strings.SplitN(bare, ":", 2)
		if len(parts) < 2 || parts[0] != np.Labels[0].Value {
			return false
		}
	}

	// Inline properties.
	if np.Properties != nil {
		ml, ok := np.Properties.(*sulpherast.MapLiteral)
		if !ok {
			return false
		}
		for _, pair := range ml.Pairs {
			key := pair.Key.Value
			expected := evalLiteralAST(pair.Value, env)

			if key == "id" {
				// Entity ID is embedded in the node ID string — no store call needed.
				parts := strings.SplitN(bare, ":", 2)
				if len(parts) == 2 {
					idStr := parts[1]
					match := false
					switch v := expected.(type) {
					case int:
						match = idStr == strconv.Itoa(v)
					case int64:
						match = idStr == strconv.FormatInt(v, 10)
					case string:
						match = idStr == v
					}
					if !match {
						return false
					}
					continue
				}
			}

			if key == "type" {
				// Entity type is the prefix of the node ID string — no store call.
				parts := strings.SplitN(bare, ":", 2)
				if len(parts) == 2 {
					typeStr := parts[0]
					typExpected, _ := expected.(string)
					if typeStr != typExpected {
						return false
					}
					continue
				}
			}

			if len(nodeData) == 0 {
				e.hydrateNodeData(ctx, nodeID, nodeData, snapshot.hydrated)
			} else if _, present := nodeData[key]; !present {
				e.hydrateNodeData(ctx, nodeID, nodeData, snapshot.hydrated)
			}

			actual, exists := nodeData[key]
			if !exists || !valuesEqual(actual, expected) {
				return false
			}
		}
	}

	return true
}

// ── Utility functions ───────────────────────────────────────────────────────

// toInterfaceSlice converts an interface{} to []interface{} when it is one.
func toInterfaceSlice(v interface{}) ([]interface{}, bool) {
	if v == nil {
		return nil, true
	}
	if s, ok := v.([]interface{}); ok {
		return s, true
	}
	return nil, false
}

// evalLiteralAST extracts a Go value from a literal AST node.
// env is consulted when the expression is an Identifier, so that UNWIND
// variables (and other runtime bindings) are resolved correctly. Pass nil
// when no runtime environment is available (e.g. compile-time constant checks).
func evalLiteralAST(expr sulpherast.Expression, env Env) interface{} {
	switch v := expr.(type) {
	case *sulpherast.IntegerLiteral:
		return int(v.Value)
	case *sulpherast.FloatLiteral:
		return v.Value
	case *sulpherast.StringLiteral:
		return v.Value
	case *sulpherast.BooleanLiteral:
		return v.Value
	case *sulpherast.NullLiteral:
		return nil
	case *sulpherast.Identifier:
		if env != nil {
			return env[v.Value]
		}
		return nil
	}
	return nil
}

// exprToKey produces a string key for a RETURN expression with no alias.
func exprToKey(expr sulpherast.Expression) string {
	switch ex := expr.(type) {
	case *sulpherast.Identifier:
		return ex.Value
	case *sulpherast.PropertyAccess:
		return exprToKey(ex.Object) + "." + ex.Property.Value
	default:
		return expr.String()
	}
}

func evalIntExpr(expr sulpherast.Expression) int {
	if expr == nil {
		return 0
	}
	il, ok := expr.(*sulpherast.IntegerLiteral)
	if !ok {
		return 0
	}
	return int(il.Value)
}

// astToPathDir converts a RelDirection to a graph.PathDirection.
func astToPathDir(d RelDirection) graph.PathDirection {
	switch d {
	case RelIncoming:
		return graph.PathDirIncoming
	case RelBidirectional:
		return graph.PathDirAny
	default:
		return graph.PathDirOutgoing
	}
}

// ── Relationship helpers ──────────────────────────────────────────────────────

func astRelDirection(rp *sulpherast.RelationshipPattern) RelDirection {
	if rp == nil {
		return RelOutgoing
	}
	switch {
	case !rp.Left && rp.Right:
		return RelOutgoing
	case rp.Left && !rp.Right:
		return RelIncoming
	default:
		return RelBidirectional
	}
}

func astRelType(rp *sulpherast.RelationshipPattern) string {
	if rp == nil || len(rp.RelTypes) == 0 {
		return ""
	}
	return rp.RelTypes[0].Value
}

// astRelVar returns the variable name bound to a relationship pattern,
// or "" when the pattern has no variable (anonymous relationship).
func astRelVar(rp *sulpherast.RelationshipPattern) string {
	if rp == nil || rp.Variable == nil {
		return ""
	}
	return rp.Variable.Value
}

func astHops(rp *sulpherast.RelationshipPattern, maxDepth int) (min, max int) {
	min = 1
	if rp.RangeMin != nil {
		min = int(rp.RangeMin.Value)
	}
	max = maxDepth
	if rp.RangeMax != nil {
		max = int(rp.RangeMax.Value)
	}
	return
}

// ── Integer expression evaluation ────────────────────────────────────────────

// ── Push-down planner for AST ────────────────────────────────────────────────

// planGraphQueryAST is the AST-native version of planGraphQuery.
// It checks push-down eligibility directly from the Cypher AST.
func (e *Executor) planGraphQueryAST(q *sulpherast.Query) graphPlan {
	if e.graphStore == nil {
		return planTraversal
	}
	if len(q.Parts) == 0 {
		return planTraversal
	}
	sq := q.Parts[0]

	match, ret, err := extractClauses(sq)
	if err != nil || match == nil {
		return planTraversal
	}
	_ = ret

	// KL-1 fix: never push down queries that contain a WITH clause.
	// The push-down executor generates a flat SQL SELECT; it cannot compose
	// two separate graph traversals across a WITH boundary. A second MATCH
	// after WITH introduces unbound variables that the RETURN generator
	// cannot resolve. Check via extractAllClauses which parses withStages.
	if cs, csErr := extractAllClauses(sq); csErr == nil && len(cs.withStages) > 0 {
		return planTraversal
	}

	path, err := extractPathElements(match.Pattern)
	if err != nil || len(path) == 0 {
		return planTraversal
	}

	varToEntity := make(map[string]string, len(path))
	for _, seg := range path {
		if seg.rel != nil && seg.rel.HasRange {
			return planTraversal // variable-length: no push-down
		}
		typ := astNodeType(seg.node)
		if typ == "" {
			return planTraversal
		}
		if !e.graphStore.IsAdaptedEntity(typ) {
			return planTraversal
		}
		varName := astNodeVar(seg.node)
		if varName != "" {
			varToEntity[varName] = typ
		}
	}

	// WHERE must be fully translatable.
	if match.Where != nil {
		if !whereExprIsPushable(match.Where, varToEntity, e.graphStore) {
			return planTraversal
		}
	}

	return planPushDown
}

// whereExprIsPushable checks that every comparison in the WHERE tree
// resolves to an adapted column.
func whereExprIsPushable(expr sulpherast.Expression, varToEntity map[string]string, store interface {
	AdaptedColumnInfo(entity, field string) (string, int, bool, bool)
}) bool {
	switch ex := expr.(type) {
	case *sulpherast.InfixExpression:
		op := strings.ToUpper(ex.Operator)
		if op == "AND" || op == "OR" {
			return whereExprIsPushable(ex.Left, varToEntity, store) &&
				whereExprIsPushable(ex.Right, varToEntity, store)
		}
		// Comparison: left must be var.prop, right must be a literal.
		pa, ok := ex.Left.(*sulpherast.PropertyAccess)
		if !ok {
			return false
		}
		varIdent, ok := pa.Object.(*sulpherast.Identifier)
		if !ok {
			return false
		}
		entity, ok := varToEntity[varIdent.Value]
		if !ok {
			return false
		}
		_, _, _, colOk := store.AdaptedColumnInfo(entity, pa.Property.Value)
		return colOk && isLiteralExpr(ex.Right)
	case *sulpherast.PrefixExpression:
		return whereExprIsPushable(ex.Right, varToEntity, store)
	default:
		return false
	}
}

func isLiteralExpr(expr sulpherast.Expression) bool {
	switch expr.(type) {
	case *sulpherast.IntegerLiteral, *sulpherast.FloatLiteral,
		*sulpherast.StringLiteral, *sulpherast.BooleanLiteral,
		*sulpherast.NullLiteral:
		return true
	}
	return false
}

// executeGraphPushDownAST is the AST-native version of executeGraphPushDown.
func (e *Executor) executeGraphPushDownAST(ctx context.Context, q *sulpherast.Query) (*QueryResult, error) {
	sq := q.Parts[0]
	match, ret, err := extractClauses(sq)
	if err != nil {
		return nil, err
	}
	path, err := extractPathElements(match.Pattern)
	if err != nil {
		return nil, err
	}

	sqlr, err := e.generateGraphSQLAST(match, ret, path)
	if err != nil {
		return nil, fmt.Errorf("graph SQL: %w", err)
	}

	rows, err := e.graphStore.AggregateQuery(ctx, sqlr.sql, sqlr.args, sqlr.aliases)
	if err != nil {
		return nil, fmt.Errorf("graph push-down: %w", err)
	}

	if ret.Distinct {
		rows = qs.ApplyDistinct(rows)
	}
	if limit := evalIntExpr(ret.Limit); limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	return &QueryResult{
		Data:  rows,
		Stats: QueryStats{PathsFound: len(rows)},
	}, nil
}

// generateGraphSQLAST builds the JOIN chain SQL from AST clause types.
func (e *Executor) generateGraphSQLAST(match *sulpherast.MatchClause, ret *sulpherast.ReturnClause, path []pathSegment) (*graphSQLResult, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	firstEntity := astNodeType(path[0].node)
	dialect := e.graphStore.StorageDialectFor(firstEntity)
	if dialect == nil {
		return nil, fmt.Errorf("no storage dialect for entity %q", firstEntity)
	}

	ab := &argBuilder{dialect: dialect}
	edgeTable := e.graphStore.GraphEdgesTable()

	// Build node infos.
	nodes := make([]nodeInfo, len(path))
	for i, seg := range path {
		alias := astNodeVar(seg.node)
		if !isSimpleIdent(alias) {
			alias = fmt.Sprintf("n%d", i)
		}
		entity := astNodeType(seg.node)
		table, ok := e.graphStore.AdaptedTableName(entity)
		if !ok {
			return nil, fmt.Errorf("entity %q has no adapted table", entity)
		}
		nodes[i] = nodeInfo{variable: astNodeVar(seg.node), alias: alias, entity: entity, table: table}
	}

	varToIdx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if n.variable != "" {
			varToIdx[n.variable] = i
		}
	}

	// SELECT.
	aliases, selectCols, err := buildSelectClauseAST(ret.Items, nodes, varToIdx, e.graphStore)
	if err != nil {
		return nil, fmt.Errorf("SELECT: %w", err)
	}

	// FROM + JOINs.
	var sb strings.Builder
	fmt.Fprintf(&sb, "SELECT %s\nFROM %s %s",
		strings.Join(selectCols, ", "),
		nodes[0].table, nodes[0].alias,
	)

	for i := 0; i < len(path)-1; i++ {
		rel := path[i].rel
		if rel == nil {
			return nil, fmt.Errorf("path segment %d has no relationship", i)
		}
		edgeAlias := fmt.Sprintf("g%d", i)
		src := nodes[i]
		dst := nodes[i+1]
		dir := astRelDirection(rel)

		switch dir {
		case RelOutgoing:
			fmt.Fprintf(&sb, "\nJOIN %s %s ON %s.source_entity = %s AND %s.source_id = %s.id",
				edgeTable, edgeAlias, edgeAlias, ab.add(src.entity), edgeAlias, src.alias)
		case RelIncoming:
			fmt.Fprintf(&sb, "\nJOIN %s %s ON %s.target_entity = %s AND %s.target_id = %s.id",
				edgeTable, edgeAlias, edgeAlias, ab.add(src.entity), edgeAlias, src.alias)
		default:
			return nil, fmt.Errorf("bidirectional relationships not yet supported in push-down")
		}

		if relType := astRelType(rel); relType != "" {
			fmt.Fprintf(&sb, " AND %s.relationship_name = %s", edgeAlias, ab.add(relType))
		}

		switch dir {
		case RelOutgoing:
			fmt.Fprintf(&sb, "\nJOIN %s %s ON %s.id = %s.target_id AND %s.target_entity = %s",
				dst.table, dst.alias, dst.alias, edgeAlias, edgeAlias, ab.add(dst.entity))
		case RelIncoming:
			fmt.Fprintf(&sb, "\nJOIN %s %s ON %s.id = %s.source_id AND %s.source_entity = %s",
				dst.table, dst.alias, dst.alias, edgeAlias, edgeAlias, ab.add(dst.entity))
		}
	}

	// WHERE.
	var whereAnd []string
	tenantID, err := tenantIDFromPrefix(e.tenantPrefix)
	if err != nil {
		return nil, err
	}
	if tenantID != 0 {
		for _, n := range nodes {
			whereAnd = append(whereAnd, fmt.Sprintf("%s.tenant_id = %s", n.alias, ab.add(int(tenantID))))
		}
	}

	if match.Where != nil {
		varToEntity := make(map[string]string, len(nodes))
		for _, n := range nodes {
			if n.variable != "" {
				varToEntity[n.variable] = n.entity
			}
		}
		userWhere, err := buildWhereClauseAST(match.Where, varToEntity, varToIdx, nodes, ab, e.graphStore)
		if err != nil {
			return nil, fmt.Errorf("WHERE: %w", err)
		}
		if userWhere != "" {
			whereAnd = append(whereAnd, userWhere)
		}
	}

	if len(whereAnd) > 0 {
		sb.WriteString("\nWHERE ")
		sb.WriteString(strings.Join(whereAnd, " AND "))
	}

	// ORDER BY.
	if len(ret.OrderBy) > 0 {
		var obParts []string
		for _, si := range ret.OrderBy {
			key := exprToKey(si.Expr)
			parts := strings.SplitN(key, ".", 2)
			if len(parts) != 2 {
				continue
			}
			varName, field := parts[0], parts[1]
			idx, ok := varToIdx[varName]
			if !ok {
				continue
			}
			n := nodes[idx]
			colName, _, _, ok := e.graphStore.AdaptedColumnInfo(n.entity, field)
			if !ok {
				continue
			}
			dir := "ASC"
			if si.Descending {
				dir = "DESC"
			}
			obParts = append(obParts, fmt.Sprintf("%s.%s %s", n.alias, colName, dir))
		}
		if len(obParts) > 0 {
			sb.WriteString("\nORDER BY ")
			sb.WriteString(strings.Join(obParts, ", "))
		}
	}

	// LIMIT.
	if limit := evalIntExpr(ret.Limit); limit > 0 {
		fmt.Fprintf(&sb, "\nLIMIT %s", ab.add(limit))
	}

	return &graphSQLResult{sql: sb.String(), args: ab.args, aliases: aliases}, nil
}

// buildSelectClauseAST builds the SELECT column list from AST ProjectionItems.
func buildSelectClauseAST(items []*sulpherast.ProjectionItem, nodes []nodeInfo, varToIdx map[string]int, store interface {
	AdaptedColumnInfo(entity, field string) (string, int, bool, bool)
}) (aliases []string, selectCols []string, err error) {
	for _, item := range items {
		if item.Star {
			return nil, nil, fmt.Errorf("RETURN * not supported in push-down")
		}
		pa, ok := item.Expr.(*sulpherast.PropertyAccess)
		if !ok {
			return nil, nil, fmt.Errorf("push-down RETURN only supports var.property; got %T", item.Expr)
		}
		varIdent, ok := pa.Object.(*sulpherast.Identifier)
		if !ok {
			return nil, nil, fmt.Errorf("RETURN variable must be a simple identifier")
		}
		idx, ok := varToIdx[varIdent.Value]
		if !ok {
			return nil, nil, fmt.Errorf("RETURN variable %q not bound in MATCH", varIdent.Value)
		}
		n := nodes[idx]
		colName, _, _, ok := store.AdaptedColumnInfo(n.entity, pa.Property.Value)
		if !ok {
			return nil, nil, fmt.Errorf("property %q not in adapted schema for %q", pa.Property.Value, n.entity)
		}
		alias := varIdent.Value + "." + pa.Property.Value
		if item.Alias != nil {
			// Injection guard: an explicit alias is interpolated into
			// `... AS "<alias>"` and executed as push-down SQL. A backtick-quoted
			// Cypher identifier can carry a double-quote and break out of the SQL
			// alias quoting, rewriting the statement (D-005 class). Require a bare
			// identifier — the same constraint generateGraphSQLAST already enforces
			// for node/table aliases. The generated default alias below (var.prop)
			// is trusted and intentionally exempt.
			if !isSimpleIdent(item.Alias.Value) {
				return nil, nil, fmt.Errorf("invalid RETURN alias %q: must contain only letters, digits, and underscores", item.Alias.Value)
			}
			alias = item.Alias.Value
		}
		selectCols = append(selectCols, fmt.Sprintf(`%s.%s AS "%s"`, n.alias, colName, alias))
		aliases = append(aliases, alias)
	}
	if len(selectCols) == 0 {
		return nil, nil, fmt.Errorf("no projectable columns in RETURN")
	}
	return aliases, selectCols, nil
}

// buildWhereClauseAST translates the WHERE expression tree to SQL.
func buildWhereClauseAST(expr sulpherast.Expression, varToEntity map[string]string, varToIdx map[string]int, nodes []nodeInfo, ab *argBuilder, store interface {
	AdaptedColumnInfo(entity, field string) (string, int, bool, bool)
}) (string, error) {
	switch ex := expr.(type) {
	case *sulpherast.InfixExpression:
		op := strings.ToUpper(ex.Operator)
		if op == "AND" {
			l, err := buildWhereClauseAST(ex.Left, varToEntity, varToIdx, nodes, ab, store)
			if err != nil {
				return "", err
			}
			r, err := buildWhereClauseAST(ex.Right, varToEntity, varToIdx, nodes, ab, store)
			if err != nil {
				return "", err
			}
			return "(" + l + " AND " + r + ")", nil
		}
		if op == "OR" {
			l, err := buildWhereClauseAST(ex.Left, varToEntity, varToIdx, nodes, ab, store)
			if err != nil {
				return "", err
			}
			r, err := buildWhereClauseAST(ex.Right, varToEntity, varToIdx, nodes, ab, store)
			if err != nil {
				return "", err
			}
			return "(" + l + " OR " + r + ")", nil
		}
		// Comparison.
		pa, ok := ex.Left.(*sulpherast.PropertyAccess)
		if !ok {
			return "", fmt.Errorf("WHERE left side must be var.prop")
		}
		varIdent := pa.Object.(*sulpherast.Identifier)
		entity := varToEntity[varIdent.Value]
		idx := varToIdx[varIdent.Value]
		alias := nodes[idx].alias
		colName, _, _, _ := store.AdaptedColumnInfo(entity, pa.Property.Value)
		sqlOp, err := operatorToSQL(Operator(ex.Operator))
		if err != nil {
			sqlOp = ex.Operator // fallback
		}
		val := evalLiteralAST(ex.Right, nil)
		return fmt.Sprintf("%s.%s %s %s", alias, colName, sqlOp, ab.add(val)), nil
	default:
		return "", fmt.Errorf("unsupported WHERE expression %T", expr)
	}
}

// ListLiteral reference to confirm the import is used.
var _ *sulpherast.ListLiteral

// crossJoinEnvs returns the Cartesian product of two []Env slices.
// Each result Env contains all bindings from one left Env merged with one right Env.
// Right-side bindings win on key collision (consistent with second-MATCH semantics).
// If either slice is empty the result is empty — a cross join with an empty set
// always produces an empty set.
func crossJoinEnvs(left, right []Env) []Env {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	results := make([]Env, 0, len(left)*len(right))
	for _, l := range left {
		for _, r := range right {
			merged := make(Env, len(l)+len(r))
			for k, v := range l {
				merged[k] = v
			}
			for k, v := range r {
				merged[k] = v
			}
			results = append(results, merged)
		}
	}
	return results
}
