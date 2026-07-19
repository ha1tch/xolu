// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"context"
	"fmt"

	"github.com/ha1tch/tsqlparser/ast"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/rs/zerolog/log"
)

// PushDecision represents an operation that can be pushed to the storage engine.
type PushDecision int

const (
	PushNone      PushDecision = iota // Stay in Go
	PushWhere                         // Push WHERE to storage
	PushOrderBy                       // Push ORDER BY to storage
	PushLimit                         // Push TOP/LIMIT to storage
	PushAggregate                     // Push GROUP BY + aggregates to storage (adapted tables only)
	PushFull                          // Push entire SELECT to storage (adapted tables, fully translatable)
	PushJoin                          // Push a two-table JOIN to SQLite
)

func (pd PushDecision) String() string {
	switch pd {
	case PushNone:
		return "none"
	case PushWhere:
		return "WHERE"
	case PushOrderBy:
		return "ORDER_BY"
	case PushLimit:
		return "LIMIT"
	case PushAggregate:
		return "AGGREGATE"
	case PushFull:
		return "FULL"
	case PushJoin:
		return "JOIN"
	default:
		return fmt.Sprintf("PushDecision(%d)", int(pd))
	}
}

// QueryPlan describes which operations the planner decided to push down
// to the storage engine and which remain in the Go execution path.
type QueryPlan struct {
	Push        []PushDecision            // Which operations to push
	EstimatedN  int                       // Estimated input cardinality
	BackendCaps storage.QueryCapabilities // What the backend can do
	Reason      string                    // Human-readable explanation for debug log

	// Join is non-nil when Push contains PushJoin.
	Join         *joinSpec
	LeftAdapted  bool // true when the left-side entity uses an adapted table
	RightAdapted bool // true when the right-side entity uses an adapted table
}

// hasPush returns true if any operation is being pushed down.
func (qp *QueryPlan) hasPush() bool {
	for _, p := range qp.Push {
		if p != PushNone {
			return true
		}
	}
	return false
}

// pushed returns true if a specific operation is in the push list.
func (qp *QueryPlan) pushed(d PushDecision) bool {
	for _, p := range qp.Push {
		if p == d {
			return true
		}
	}
	return false
}

// pushNames returns a comma-separated list of pushed operations for logging.
func (qp *QueryPlan) pushNames() string {
	if !qp.hasPush() {
		return "none"
	}
	var names []string
	for _, p := range qp.Push {
		if p != PushNone {
			names = append(names, p.String())
		}
	}
	result := ""
	for i, n := range names {
		if i > 0 {
			result += ","
		}
		result += n
	}
	return result
}

// DefaultPushDownThreshold is the fallback minimum entity count above
// which push-down becomes worthwhile, used only when no dialect is
// available to provide a backend-specific threshold. Each SQLDialect
// implementation provides its own threshold via DefaultThreshold()
// based on actual benchmark data for that backend.
const DefaultPushDownThreshold = 200

// Planner examines parsed OQL ASTs and decides which operations to
// delegate to the storage engine (push-down) versus executing in Go.
//
// The planner is conservative: it only pushes down operations when
// (a) the backend supports the operation, (b) the entity cardinality
// exceeds the threshold, and (c) the expression tree is fully
// translatable to SQL.
//
// For adapted entities the planner can bypass the cardinality check
// entirely: if the full query is translatable to native-column SQL,
// push-down is always beneficial regardless of row count — unless the
// query's estimated complexity exceeds a hardware-dependent threshold
// (see EstimateComplexity and HardwareProfile).
type Planner struct {
	threshold int              // Minimum entity count for blob push-down
	dialect   SQLDialect       // nil when no dialect is available
	profile   *HardwareProfile // nil uses VPS defaults
}

// NewPlanner creates a planner with the default push-down threshold
// and no hardware profile. Prefer NewPlannerFromDialect or
// NewPlannerWithProfile when possible.
func NewPlanner() *Planner {
	return &Planner{
		threshold: DefaultPushDownThreshold,
	}
}

// NewPlannerFromDialect creates a planner whose threshold is derived
// from the backend's characteristics. An in-process SQLite has near-zero
// call overhead (threshold=50), while a networked backend like Postgres
// has connection and round-trip costs that justify a higher threshold.
func NewPlannerFromDialect(dialect SQLDialect) *Planner {
	return &Planner{
		threshold: dialect.DefaultThreshold(),
		dialect:   dialect,
	}
}

// NewPlannerWithProfile creates a planner configured from a hardware
// profile. The profile's BlobPushThreshold overrides the dialect
// default, and its complexity thresholds gate PushFull for expensive
// adapted-entity queries.
func NewPlannerWithProfile(dialect SQLDialect, profile *HardwareProfile) *Planner {
	threshold := DefaultPushDownThreshold
	if profile != nil {
		threshold = profile.BlobPushThreshold
	}
	if dialect != nil && profile == nil {
		threshold = dialect.DefaultThreshold()
	}
	return &Planner{
		threshold: threshold,
		dialect:   dialect,
		profile:   profile,
	}
}

// NewPlannerWithThreshold creates a planner with a custom threshold.
// Useful for testing with smaller datasets.
func NewPlannerWithThreshold(threshold int) *Planner {
	return &Planner{
		threshold: threshold,
	}
}

// NewPlannerWithDialectAndThreshold creates a planner with both a custom
// threshold and a dialect. Used by tests that need adapted-entity awareness
// at low row counts.
func NewPlannerWithDialectAndThreshold(dialect SQLDialect, threshold int) *Planner {
	return &Planner{
		threshold: threshold,
		dialect:   dialect,
	}
}

// effectiveProfile returns the planner's hardware profile, falling back
// to the VPS default if none was set.
func (p *Planner) effectiveProfile() *HardwareProfile {
	if p.profile != nil {
		return p.profile
	}
	def := DefaultProfile()
	return &def
}

// Plan examines a SELECT statement and the storage backend to produce
// a QueryPlan. The plan tells the executor which operations to push
// down and which to execute in Go.
//
// For adapted entities the planner checks full translatability first,
// skipping the CountEntities round-trip entirely when push-down is
// possible. For blob entities the original threshold-based logic applies.
//
// For non-SELECT statements (UPDATE, DELETE), use PlanMutation.
func (p *Planner) Plan(ctx context.Context, s *ast.SelectStatement, store storage.Store) QueryPlan {
	entity := extractEntityFromSelect(s)

	// --- JOIN path: two-table JOIN pushed entirely to SQLite ---
	if js, err := extractJoinSpec(s); js != nil && err == nil {
		return p.planJoin(ctx, s, js, store)
	}

	// --- Fast path: adapted entity push-down (no CountEntities needed) ---
	if p.dialect != nil {
		if aggStore, ok := store.(storage.AggregateQueryable); ok && aggStore.IsAdaptedEntity(entity) {
			// Try full push-down first (entire SELECT in one SQL statement).
			if isFullyTranslatable(s, entity, aggStore, p.dialect) {
				// Estimate query complexity to detect cases where push-down
				// generates expensive SQLite plans (multi-key GROUP BY,
				// non-covering aggregate scans, misaligned ORDER BY).
				qc := EstimateComplexity(s)
				complexityThreshold := qc.Threshold(p.effectiveProfile())

				if complexityThreshold == 0 {
					// Simple query: push-down always wins.
					plan := QueryPlan{
						Push:   []PushDecision{PushFull},
						Reason: fmt.Sprintf("adapted entity %q: full push-down", entity),
					}
					log.Debug().
						Str("entity", entity).
						Str("push", plan.pushNames()).
						Str("reason", plan.Reason).
						Msg("Query planner")
					return plan
				}

				// Complex query: push-down only wins above a row count threshold.
				// This costs a CountEntities round-trip (~200µs) to avoid a
				// potentially much larger regression (e.g. 8ms vs 5ms).
				queryable, qOk := store.(storage.Queryable)
				if qOk {
					count, err := queryable.CountEntities(ctx, entity)
					if err == nil && count >= complexityThreshold {
						plan := QueryPlan{
							Push:       []PushDecision{PushFull},
							EstimatedN: count,
							Reason: fmt.Sprintf(
								"adapted entity %q: full push-down (complexity=%d temp B-trees, nonCovering=%v, count=%d >= threshold=%d)",
								entity, qc.TempBTrees, qc.NonCovering, count, complexityThreshold),
						}
						log.Debug().
							Str("entity", entity).
							Int("tempBTrees", qc.TempBTrees).
							Bool("nonCovering", qc.NonCovering).
							Int("count", count).
							Int("threshold", complexityThreshold).
							Str("push", plan.pushNames()).
							Msg("Query planner: complex adapted push-down")
						return plan
					}
					if err == nil {
						// Below threshold: fall through to Go path.
						log.Debug().
							Str("entity", entity).
							Int("tempBTrees", qc.TempBTrees).
							Bool("nonCovering", qc.NonCovering).
							Int("count", count).
							Int("threshold", complexityThreshold).
							Msg("Query planner: complex adapted query below threshold, using Go path")
					}
				}
				// If CountEntities fails or count < threshold, fall through.
			}
			// Try aggregate push-down (GROUP BY + aggregates without scalar functions).
			if (len(s.GroupBy) > 0 || hasAggregates(s.Columns)) && !hasScalarFunctions(s.Columns, s.GroupBy) {
				plan := QueryPlan{
					Push:   []PushDecision{PushAggregate},
					Reason: fmt.Sprintf("adapted entity %q: aggregate push-down", entity),
				}
				log.Debug().
					Str("entity", entity).
					Str("push", plan.pushNames()).
					Str("reason", plan.Reason).
					Msg("Query planner")
				return plan
			}
			// Adapted entity but not translatable — fall through to blob logic.
			// (The adapted entity still has a blob row in the entities table.)
		}
	}

	// --- Standard path: blob entity push-down (threshold-gated) ---

	// 1. Backend capable?
	queryable, ok := store.(storage.Queryable)
	if !ok {
		return QueryPlan{
			Push:   []PushDecision{PushNone},
			Reason: "backend does not implement Queryable",
		}
	}

	caps := queryable.Capabilities()

	// 2. Entity count above threshold?
	count, err := queryable.CountEntities(ctx, entity)
	if err != nil {
		return QueryPlan{
			Push:        []PushDecision{PushNone},
			BackendCaps: caps,
			Reason:      fmt.Sprintf("CountEntities failed: %v", err),
		}
	}

	if count < p.threshold {
		plan := QueryPlan{
			Push:        []PushDecision{PushNone},
			EstimatedN:  count,
			BackendCaps: caps,
			Reason:      fmt.Sprintf("count %d < threshold %d", count, p.threshold),
		}
		log.Debug().
			Str("entity", entity).
			Int("count", count).
			Str("push", "none").
			Str("reason", plan.Reason).
			Msg("Query planner")
		return plan
	}

	// 3. Determine what to push
	var pushOps []PushDecision

	// 3a. WHERE pushable?
	wherePushable := false
	if s.Where != nil && caps.Where {
		wherePushable = isWherePushable(s.Where)
		if wherePushable {
			pushOps = append(pushOps, PushWhere)
		}
	}

	// 3b. ORDER BY pushable? Only if WHERE is also pushed (no benefit alone)
	if len(s.OrderBy) > 0 && caps.OrderBy && wherePushable {
		if isOrderByPushable(s.OrderBy) {
			pushOps = append(pushOps, PushOrderBy)
		}
	}

	// 3c. TOP/LIMIT pushable? Only if at least WHERE or ORDER BY is pushed
	if s.Top != nil && caps.Limit && len(pushOps) > 0 {
		pushOps = append(pushOps, PushLimit)
	}

	// If nothing was pushable, return PushNone
	if len(pushOps) == 0 {
		var reason string
		if s.Where == nil {
			reason = "no WHERE clause"
		} else if !caps.Where {
			reason = "backend does not support WHERE push-down"
		} else {
			reason = fmt.Sprintf("count %d > threshold %d but WHERE is not pushable (contains non-translatable expressions)", count, p.threshold)
		}

		plan := QueryPlan{
			Push:        []PushDecision{PushNone},
			EstimatedN:  count,
			BackendCaps: caps,
			Reason:      reason,
		}
		log.Debug().
			Str("entity", entity).
			Int("count", count).
			Str("push", "none").
			Str("reason", plan.Reason).
			Msg("Query planner")
		return plan
	}

	plan := QueryPlan{
		Push:        pushOps,
		EstimatedN:  count,
		BackendCaps: caps,
		Reason:      fmt.Sprintf("count %d > threshold %d, pushing %s", count, p.threshold, (&QueryPlan{Push: pushOps}).pushNames()),
	}

	log.Debug().
		Str("entity", entity).
		Int("count", count).
		Str("push", plan.pushNames()).
		Str("reason", plan.Reason).
		Msg("Query planner")

	return plan
}

// PlanMutation examines an UPDATE or DELETE WHERE clause against the
// storage backend. Returns a plan that may include PushWhere to narrow
// the initial record fetch.
func (p *Planner) PlanMutation(ctx context.Context, where ast.Expression, entity string, store storage.Store) QueryPlan {
	queryable, ok := store.(storage.Queryable)
	if !ok {
		return QueryPlan{
			Push:   []PushDecision{PushNone},
			Reason: "backend does not implement Queryable",
		}
	}

	caps := queryable.Capabilities()

	count, err := queryable.CountEntities(ctx, entity)
	if err != nil {
		return QueryPlan{
			Push:        []PushDecision{PushNone},
			BackendCaps: caps,
			Reason:      fmt.Sprintf("CountEntities failed: %v", err),
		}
	}

	if count < p.threshold {
		return QueryPlan{
			Push:        []PushDecision{PushNone},
			EstimatedN:  count,
			BackendCaps: caps,
			Reason:      fmt.Sprintf("count %d < threshold %d", count, p.threshold),
		}
	}

	if where != nil && caps.Where && isWherePushable(where) {
		return QueryPlan{
			Push:        []PushDecision{PushWhere},
			EstimatedN:  count,
			BackendCaps: caps,
			Reason:      fmt.Sprintf("mutation: count %d > threshold %d, pushing WHERE", count, p.threshold),
		}
	}

	return QueryPlan{
		Push:        []PushDecision{PushNone},
		EstimatedN:  count,
		BackendCaps: caps,
		Reason:      "mutation: WHERE not pushable or absent",
	}
}

// isWherePushable walks the WHERE expression tree and returns true if
// every leaf predicate is translatable to SQLite json_extract() SQL.
//
// Pushable expressions:
//   - Simple field comparison: field = value, field > value, etc.
//   - LIKE: field LIKE pattern
//   - IS NULL / IS NOT NULL
//   - BETWEEN
//   - IN with literal values
//   - AND/OR/NOT connectives
//
// NOT pushable:
//   - Scalar function calls in predicates (UPPER(name) = 'FOO')
//   - Subqueries
//   - Arithmetic expressions on the left-hand side
//   - CASE expressions
func isWherePushable(expr ast.Expression) bool {
	switch ex := expr.(type) {
	case *ast.InfixExpression:
		switch ex.Operator {
		case "AND", "OR":
			return isWherePushable(ex.Left) && isWherePushable(ex.Right)
		default:
			// Comparison operator: left must be a simple field, right must be a literal
			return isSimpleField(ex.Left) && isLiteralOrParam(ex.Right)
		}

	case *ast.PrefixExpression:
		if ex.Operator == "NOT" {
			return isWherePushable(ex.Right)
		}
		return false

	case *ast.IsNullExpression:
		return isSimpleField(ex.Expr)

	case *ast.BetweenExpression:
		return isSimpleField(ex.Expr) && isLiteralOrParam(ex.Low) && isLiteralOrParam(ex.High)

	case *ast.InExpression:
		if !isSimpleField(ex.Expr) {
			return false
		}
		for _, v := range ex.Values {
			if !isLiteralOrParam(v) {
				return false
			}
		}
		return true

	case *ast.LikeExpression:
		return isSimpleField(ex.Expr) && isLiteralOrParam(ex.Pattern)

	default:
		// Anything else (subqueries, CASE, EXISTS, etc.) is not pushable
		return false
	}
}

// isSimpleField returns true if the expression is a plain field reference
// (Identifier or QualifiedIdentifier like "address.city").
func isSimpleField(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.Identifier:
		return true
	case *ast.QualifiedIdentifier:
		return true
	default:
		return false
	}
}

// isLiteralOrParam returns true if the expression is a literal value
// that can be parameterised in SQL.
func isLiteralOrParam(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.IntegerLiteral:
		return true
	case *ast.FloatLiteral:
		return true
	case *ast.StringLiteral:
		return true
	case *ast.NullLiteral:
		return true
	case *ast.Identifier:
		// TRUE/FALSE are identifiers in the AST but are literal values
		return true
	default:
		return false
	}
}

// isOrderByPushable returns true if all ORDER BY items are simple field
// references (not function calls or computed expressions).
func isOrderByPushable(orderBy []*ast.OrderByItem) bool {
	for _, item := range orderBy {
		if !isSimpleField(item.Expression) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// JOIN push-down helpers
// ---------------------------------------------------------------------------

// joinSpec holds the parsed components of a two-table JOIN FROM clause.
type joinSpec struct {
	LeftEntity  string               // e.g. "post"
	LeftAlias   string               // e.g. "a" (falls back to entity name)
	RightEntity string               // e.g. "author"
	RightAlias  string               // e.g. "b"
	JoinType    string               // "INNER", "LEFT", "RIGHT", "FULL"
	Condition   *ast.InfixExpression // the ON a.x = b.y expression
}

// extractJoinSpec inspects a SelectStatement and returns a populated
// joinSpec if the FROM clause is exactly one JoinClause with a simple
// equality ON condition between two plain TableName references.
// Returns (nil, nil) when the query is not a supported join shape — the
// caller treats this as "not a join query" rather than an error.
func extractJoinSpec(s *ast.SelectStatement) (*joinSpec, error) {
	if s.From == nil || len(s.From.Tables) != 1 {
		return nil, nil
	}
	jc, ok := s.From.Tables[0].(*ast.JoinClause)
	if !ok {
		return nil, nil
	}

	// Supported join types only; CROSS has no ON condition.
	switch jc.Type {
	case "INNER", "LEFT", "RIGHT", "FULL":
		// supported
	default:
		return nil, nil
	}

	leftTable, ok := jc.Left.(*ast.TableName)
	if !ok {
		return nil, nil
	}
	rightTable, ok := jc.Right.(*ast.TableName)
	if !ok {
		return nil, nil
	}

	// ON condition must be a simple equality between two qualified identifiers.
	cond, ok := jc.Condition.(*ast.InfixExpression)
	if !ok || cond.Operator != "=" {
		return nil, nil
	}
	_, lOk := cond.Left.(*ast.QualifiedIdentifier)
	_, rOk := cond.Right.(*ast.QualifiedIdentifier)
	if !lOk || !rOk {
		return nil, nil
	}

	leftEntity := normalizeEntityName(leftTable.Name.String())
	rightEntity := normalizeEntityName(rightTable.Name.String())

	leftAlias := leftEntity
	if leftTable.Alias != nil && leftTable.Alias.Value != "" {
		leftAlias = leftTable.Alias.Value
	}
	rightAlias := rightEntity
	if rightTable.Alias != nil && rightTable.Alias.Value != "" {
		rightAlias = rightTable.Alias.Value
	}

	return &joinSpec{
		LeftEntity:  leftEntity,
		LeftAlias:   leftAlias,
		RightEntity: rightEntity,
		RightAlias:  rightAlias,
		JoinType:    jc.Type,
		Condition:   cond,
	}, nil
}

// isJoinConditionPushable returns true if the ON condition is a simple
// equality where both sides are qualified identifiers (alias.field) whose
// table qualifiers belong to the known left or right alias.
func isJoinConditionPushable(cond ast.Expression, leftAlias, rightAlias string) bool {
	infix, ok := cond.(*ast.InfixExpression)
	if !ok || infix.Operator != "=" {
		return false
	}
	lqi, lOk := infix.Left.(*ast.QualifiedIdentifier)
	rqi, rOk := infix.Right.(*ast.QualifiedIdentifier)
	if !lOk || !rOk {
		return false
	}
	lTable := qualifiedTable(lqi)
	rTable := qualifiedTable(rqi)
	known := map[string]bool{leftAlias: true, rightAlias: true}
	return known[lTable] && known[rTable]
}

// isJoinWherePushable is the same as isWherePushable but additionally
// accepts QualifiedIdentifiers of the form alias.field as valid left-hand
// field references (the join aliases qualify both sides).
func isJoinWherePushable(expr ast.Expression, leftAlias, rightAlias string) bool {
	aliases := map[string]bool{leftAlias: true, rightAlias: true}
	var walk func(ast.Expression) bool
	walk = func(e ast.Expression) bool {
		switch ex := e.(type) {
		case *ast.InfixExpression:
			switch ex.Operator {
			case "AND", "OR":
				return walk(ex.Left) && walk(ex.Right)
			default:
				return isJoinField(ex.Left, aliases) && isLiteralOrParam(ex.Right)
			}
		case *ast.PrefixExpression:
			if ex.Operator == "NOT" {
				return walk(ex.Right)
			}
			return false
		case *ast.IsNullExpression:
			return isJoinField(ex.Expr, aliases)
		case *ast.BetweenExpression:
			return isJoinField(ex.Expr, aliases) && isLiteralOrParam(ex.Low) && isLiteralOrParam(ex.High)
		case *ast.InExpression:
			if !isJoinField(ex.Expr, aliases) {
				return false
			}
			for _, v := range ex.Values {
				if !isLiteralOrParam(v) {
					return false
				}
			}
			return true
		case *ast.LikeExpression:
			return isJoinField(ex.Expr, aliases) && isLiteralOrParam(ex.Pattern)
		default:
			return false
		}
	}
	return walk(expr)
}

// isJoinField returns true if expr is a simple Identifier or a
// QualifiedIdentifier whose table qualifier is one of the join aliases.
func isJoinField(expr ast.Expression, aliases map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.Identifier:
		return true
	case *ast.QualifiedIdentifier:
		return aliases[qualifiedTable(e)]
	default:
		return false
	}
}

// qualifiedTable extracts the table/alias part from a QualifiedIdentifier
// (the first part of a dotted name, e.g. "a" from "a.field").
func qualifiedTable(qi *ast.QualifiedIdentifier) string {
	if len(qi.Parts) >= 2 {
		return qi.Parts[0].Value
	}
	return ""
}

// qualifiedField extracts the field part from a QualifiedIdentifier
// (the last part of a dotted name, e.g. "field" from "a.field").
func qualifiedField(qi *ast.QualifiedIdentifier) string {
	if len(qi.Parts) >= 2 {
		return qi.Parts[len(qi.Parts)-1].Value
	}
	return qi.String()
}

// planJoin attempts to build a PushJoin plan for a two-table join query.
// Returns PushNone with a reason if push-down is not possible.
func (p *Planner) planJoin(ctx context.Context, s *ast.SelectStatement, js *joinSpec, store storage.Store) QueryPlan {
	noPush := func(reason string) QueryPlan {
		log.Debug().
			Str("leftEntity", js.LeftEntity).
			Str("rightEntity", js.RightEntity).
			Str("push", "none").
			Str("reason", reason).
			Msg("Query planner: join")
		return QueryPlan{
			Push:   []PushDecision{PushNone},
			Reason: reason,
		}
	}

	aggStore, ok := store.(storage.AggregateQueryable)
	if !ok {
		return noPush("store does not support AggregateQueryable")
	}

	leftAdapted := aggStore.IsAdaptedEntity(js.LeftEntity)
	rightAdapted := aggStore.IsAdaptedEntity(js.RightEntity)

	if !isJoinConditionPushable(js.Condition, js.LeftAlias, js.RightAlias) {
		return noPush("ON condition is not a simple qualified-field equality")
	}

	if s.Where != nil && !isJoinWherePushable(s.Where, js.LeftAlias, js.RightAlias) {
		return noPush("WHERE clause contains non-pushable expressions for join")
	}

	plan := QueryPlan{
		Push: []PushDecision{PushJoin},
		Reason: fmt.Sprintf(
			"join %s(%s) %s %s(%s): left_adapted=%v right_adapted=%v",
			js.LeftEntity, js.LeftAlias, js.JoinType,
			js.RightEntity, js.RightAlias,
			leftAdapted, rightAdapted,
		),
		Join:         js,
		LeftAdapted:  leftAdapted,
		RightAdapted: rightAdapted,
	}
	log.Debug().
		Str("leftEntity", js.LeftEntity).
		Str("rightEntity", js.RightEntity).
		Str("joinType", js.JoinType).
		Bool("leftAdapted", leftAdapted).
		Bool("rightAdapted", rightAdapted).
		Str("push", plan.pushNames()).
		Str("reason", plan.Reason).
		Msg("Query planner: join")
	return plan
}
