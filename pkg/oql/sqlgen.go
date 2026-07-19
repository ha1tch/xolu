// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"fmt"
	"strings"

	"github.com/ha1tch/tsqlparser/ast"
	"github.com/ha1tch/xolu/pkg/qs"
)

// ---------------------------------------------------------------------------
// Dialect abstraction
// ---------------------------------------------------------------------------

// SQLDialect defines how to emit backend-specific SQL fragments.
// The generator calls dialect methods to produce the correct syntax
// for the target database. T-SQL arrives via tsqlparser's AST; the
// dialect translates it to the backend's native SQL.
//
// # OQL field types
//
// Several methods accept an oqlType string that controls how a JSON field
// is extracted and cast. The following tokens are defined:
//
//	"text"    — extract as text; no numeric coercion.
//	           SQLite:     json_extract(data, '$.f')  (returns TEXT/NULL)
//	           PostgreSQL: (data->>'f')::text
//
//	"numeric" — extract as a number; enables numeric comparison and ordering.
//	           SQLite:     CAST(json_extract(data, '$.f') AS REAL)
//	           PostgreSQL: (data->>'f')::numeric
//
//	"boolean" — extract as a boolean.
//	           SQLite:     json_extract(data, '$.f')  (SQLite has no BOOL type;
//	                       JSON true/false are stored as 1/0)
//	           PostgreSQL: (data->>'f')::boolean
//
//	"auto"    — extract without an explicit cast; use the backend's native
//	           return type from the JSON accessor. This is safe for SQLite
//	           equality comparisons (json_extract returns typed values) but
//	           must NOT be used for ordering or inequality comparisons on
//	           PostgreSQL (where all JSON accessors return text). Prefer an
//	           explicit type whenever the stored type is known.
type SQLDialect interface {
	// JSONFieldAs extracts a field from the JSON data column and casts it
	// to the requested OQL type. This is the canonical extraction method;
	// all comparison and ordering sites should use it rather than the
	// deprecated JSONField / JSONFieldNumeric shortcuts.
	//
	// oqlType must be one of: "text", "numeric", "boolean", "auto".
	JSONFieldAs(fieldPath, oqlType string) string

	// JSONFieldAliasedAs is JSONFieldAs for JOIN queries where the data
	// column is qualified by a table alias.
	JSONFieldAliasedAs(alias, fieldPath, oqlType string) string

	// JSONField extracts a field from the JSON data column without an
	// explicit type cast. Equivalent to JSONFieldAs(fieldPath, "auto").
	//
	// Deprecated: use JSONFieldAs with an explicit oqlType. JSONField is
	// retained for backward compatibility with existing dialect implementations
	// and will be removed when a PostgreSQL dialect is added.
	JSONField(fieldPath string) string

	// JSONFieldNumeric extracts a field and casts it to a numeric type.
	// Equivalent to JSONFieldAs(fieldPath, "numeric").
	//
	// Deprecated: use JSONFieldAs(fieldPath, "numeric").
	JSONFieldNumeric(fieldPath string) string

	// JSONFieldAliased extracts a field from a JOIN-aliased data column
	// without a type cast. Equivalent to JSONFieldAliasedAs(alias, fieldPath, "auto").
	//
	// Deprecated: use JSONFieldAliasedAs with an explicit oqlType.
	JSONFieldAliased(alias, fieldPath string) string

	// Placeholder emits a parameter placeholder for the n-th argument (1-based).
	// For SQLite: ?
	// For Postgres: $1, $2, ...
	Placeholder(n int) string

	// LimitClause emits the LIMIT/TOP equivalent.
	// T-SQL uses TOP N (before columns); SQLite and Postgres use LIMIT N (at end).
	LimitClause(placeholder string) string

	// BaseQuery emits the initial SELECT ... FROM ... WHERE entity_type = <param>.
	// Returns the SQL fragment and the initial argument (entity name).
	BaseQuery(entity string) (sql string, arg interface{})

	// Name returns the dialect identifier (for debug logging).
	Name() string

	// DefaultThreshold returns the minimum entity count at which push-down
	// becomes worthwhile for this backend. Below this count, the fixed
	// overhead of generating and executing backend SQL exceeds the cost
	// of Go-side processing. This varies by backend: an in-process SQLite
	// has near-zero call overhead, while a networked Postgres has
	// connection and round-trip costs that raise the crossover point.
	DefaultThreshold() int

	// ScalarFunction translates an OQL scalar function name to the
	// backend's equivalent SQL expression. Returns the SQL fragment
	// and true, or ("", false) if the function is not supported.
	// Example: ("LEN", "col") -> ("LENGTH(col)", true) on SQLite,
	//          ("LEN", "col") -> ("CHAR_LENGTH(col)", true) on PostgreSQL.
	ScalarFunction(name string, argSQL string) (string, bool)

	// CastExpression emits a CAST for the target backend.
	// Example: CastExpression("price", "REAL") -> "CAST(price AS REAL)"
	// on SQLite, "CAST(price AS DOUBLE PRECISION)" on PostgreSQL.
	CastExpression(expr, targetType string) string
}

// ---------------------------------------------------------------------------
// SQLite dialect
// ---------------------------------------------------------------------------

// SQLiteDialect generates SQLite-compatible SQL using json_extract().
type SQLiteDialect struct {
	// NodesTable is the tenant-scoped blob node store table name (e.g. t0000_nodes).
	// Set by the OQL executor from store.NodesTable() when the store implements
	// storage.TableNamer. Defaults to "t0000_nodes" if unset.
	NodesTable string
}

func (d *SQLiteDialect) nodesTable() string {
	if d.NodesTable != "" {
		return d.NodesTable
	}
	return "t0000_nodes" // safe default for zero-value dialect in tests
}

// JSONFieldAs extracts a field from the JSON data column with an explicit
// type cast appropriate for the requested OQL type.
//
// SQLite mapping:
//   - "numeric"  → CAST(json_extract(data, '$.f') AS REAL)
//   - "boolean"  → json_extract(data, '$.f')  (SQLite stores JSON booleans
//     as integer 1/0; no separate BOOL type needed)
//   - "text"     → json_extract(data, '$.f')  (returns TEXT when stored as string)
//   - "auto"     → json_extract(data, '$.f')  (return type mirrors stored JSON type)
func (d *SQLiteDialect) JSONFieldAs(fieldPath, oqlType string) string {
	if oqlType == "numeric" {
		return fmt.Sprintf("CAST(json_extract(data, '$.%s') AS REAL)", fieldPath)
	}
	return fmt.Sprintf("json_extract(data, '$.%s')", fieldPath)
}

// JSONFieldAliasedAs is JSONFieldAs for JOIN queries where the data column
// is qualified by a table alias.
func (d *SQLiteDialect) JSONFieldAliasedAs(alias, fieldPath, oqlType string) string {
	if oqlType == "numeric" {
		return fmt.Sprintf("CAST(json_extract(%s.data, '$.%s') AS REAL)", alias, fieldPath)
	}
	return fmt.Sprintf("json_extract(%s.data, '$.%s')", alias, fieldPath)
}

// JSONField is a deprecated shortcut. Use JSONFieldAs(fieldPath, "auto").
func (d *SQLiteDialect) JSONField(fieldPath string) string {
	return d.JSONFieldAs(fieldPath, "auto")
}

// JSONFieldNumeric is a deprecated shortcut. Use JSONFieldAs(fieldPath, "numeric").
func (d *SQLiteDialect) JSONFieldNumeric(fieldPath string) string {
	return d.JSONFieldAs(fieldPath, "numeric")
}

// JSONFieldAliased is a deprecated shortcut. Use JSONFieldAliasedAs(alias, fieldPath, "auto").
func (d *SQLiteDialect) JSONFieldAliased(alias, fieldPath string) string {
	return d.JSONFieldAliasedAs(alias, fieldPath, "auto")
}

func (d *SQLiteDialect) Placeholder(_ int) string {
	return "?"
}

func (d *SQLiteDialect) LimitClause(placeholder string) string {
	return "LIMIT " + placeholder
}

func (d *SQLiteDialect) BaseQuery(entity string) (string, interface{}) {
	return "SELECT data, _version FROM " + d.nodesTable() + " WHERE entity_type = ?", entity
}

func (d *SQLiteDialect) Name() string { return "sqlite" }

// DefaultThreshold returns 50 for SQLite. Benchmarked crossover point:
// push-down is faster than Go-side even at 100 records (1.5x) because
// SQLite is in-process with zero network overhead. The only case where
// push-down is marginal is broad LIKE patterns, where the crossover
// is higher (~500), but a 50-record threshold captures the common case.
func (d *SQLiteDialect) DefaultThreshold() int { return 50 }

// ScalarFunction translates OQL scalar function names to SQLite equivalents.
// Returns ("", false) for functions that have no clean SQLite translation.
func (d *SQLiteDialect) ScalarFunction(name string, argSQL string) (string, bool) {
	switch strings.ToUpper(name) {
	case "UPPER":
		return "UPPER(" + argSQL + ")", true
	case "LOWER":
		return "LOWER(" + argSQL + ")", true
	case "LEN":
		return "LENGTH(" + argSQL + ")", true
	case "TRIM":
		return "TRIM(" + argSQL + ")", true
	case "LTRIM":
		return "LTRIM(" + argSQL + ")", true
	case "RTRIM":
		return "RTRIM(" + argSQL + ")", true
	case "ABS":
		return "ABS(" + argSQL + ")", true
	case "ROUND":
		return "ROUND(" + argSQL + ")", true
	case "COALESCE":
		return "COALESCE(" + argSQL + ")", true
	case "REPLACE":
		return "REPLACE(" + argSQL + ")", true
	case "LENGTH":
		return "LENGTH(" + argSQL + ")", true
	case "TYPEOF":
		return "TYPEOF(" + argSQL + ")", true
	default:
		return "", false
	}
}

// CastExpression emits a CAST expression for SQLite.
func (d *SQLiteDialect) CastExpression(expr, targetType string) string {
	return "CAST(" + expr + " AS " + targetType + ")"
}

// ---------------------------------------------------------------------------
// Field name validation
// ---------------------------------------------------------------------------

// dangerousFieldChars are characters that must never appear in field names
// embedded in SQL. Retained as an explicit backstop even though
// qs.ValidateFieldPath already excludes them via its ASCII allowlist.
var dangerousFieldChars = []string{"'", "\"", ")", "--", ";", "/*"}

func validateFieldName(name string) error {
	// Canonical identifier policy lives in pkg/qs (ASCII bare identifiers, dotted
	// paths permitted). This is the single source of truth shared across OQL,
	// Sulpher, storage, and the server handlers.
	if err := qs.ValidateFieldPath(name); err != nil {
		return fmt.Errorf("invalid field name %q: must be alphanumeric with underscores/dots", name)
	}
	for _, ch := range dangerousFieldChars {
		if strings.Contains(name, ch) {
			return fmt.Errorf("invalid field name %q: contains dangerous character %q", name, ch)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// SQL generation
// ---------------------------------------------------------------------------

// GeneratedSQL holds the output of the SQL generator: a parameterised SQL
// string and the ordered argument list. The SQL is ready for execution
// via storage.Queryable.QueryWithPlan().
type GeneratedSQL struct {
	SQL  string
	Args []interface{}
}

// SQLGenerator translates pushable portions of an OQL AST into
// backend-specific SQL. It uses a Dialect to emit the correct syntax.
type SQLGenerator struct {
	dialect SQLDialect
	args    []interface{}
}

// NewSQLGenerator creates a generator for the given dialect.
func NewSQLGenerator(dialect SQLDialect) *SQLGenerator {
	return &SQLGenerator{
		dialect: dialect,
		args:    nil,
	}
}

// addArg appends an argument and returns the placeholder string.
func (g *SQLGenerator) addArg(val interface{}) string {
	g.args = append(g.args, val)
	return g.dialect.Placeholder(len(g.args))
}

// GenerateSQL translates the pushable portions of an OQL SELECT statement
// into backend-specific SQL. Only operations listed in plan.Push are
// translated; the executor handles the rest in Go.
//
// The function is the main entry point for SQL generation and is
// dialect-agnostic — it delegates all syntax decisions to the dialect.
func GenerateSQL(
	stmt *ast.SelectStatement,
	entity string,
	tenantID string,
	plan QueryPlan,
	dialect SQLDialect,
) (*GeneratedSQL, error) {
	gen := NewSQLGenerator(dialect)

	// Base query: SELECT data FROM entities WHERE entity_type = ?
	baseSql, baseArg := dialect.BaseQuery(entity)
	gen.args = append(gen.args, baseArg)

	var clauses []string
	clauses = append(clauses, baseSql)

	// Tenant filter (always applied if tenantID is non-empty, regardless of plan).
	// If tenantID is purely numeric, it targets the tenant_id INTEGER column
	// (used by ExecuteWithStore which extracts the store's uint16 TenantID).
	// Otherwise it targets json_extract(data, '$.tenant_id') for backward
	// compatibility with ExecuteWithTenant's string-based tenant IDs.
	if tenantID != "" {
		if isNumeric(tenantID) {
			clauses = append(clauses, fmt.Sprintf("AND tenant_id = %s",
				gen.addArg(tenantID)))
		} else {
			clauses = append(clauses, fmt.Sprintf("AND %s = %s",
				dialect.JSONFieldAs("tenant_id", "text"), gen.addArg(tenantID)))
		}
	}

	// WHERE push-down
	if plan.pushed(PushWhere) && stmt.Where != nil {
		whereSQL, err := gen.translateExpr(stmt.Where)
		if err != nil {
			return nil, fmt.Errorf("WHERE translation: %w", err)
		}
		clauses = append(clauses, "AND ("+whereSQL+")")
	}

	sql := strings.Join(clauses, " ")

	// ORDER BY push-down
	if plan.pushed(PushOrderBy) && len(stmt.OrderBy) > 0 {
		orderSQL, err := gen.translateOrderBy(stmt.OrderBy)
		if err != nil {
			return nil, fmt.Errorf("ORDER BY translation: %w", err)
		}
		sql += " ORDER BY " + orderSQL
	}

	// LIMIT push-down (T-SQL TOP → backend LIMIT)
	if plan.pushed(PushLimit) && stmt.Top != nil {
		limitVal, err := evalTopCount(stmt.Top)
		if err != nil {
			return nil, fmt.Errorf("LIMIT translation: %w", err)
		}
		placeholder := gen.addArg(limitVal)
		sql += " " + dialect.LimitClause(placeholder)
	}

	return &GeneratedSQL{SQL: sql, Args: gen.args}, nil
}

// ---------------------------------------------------------------------------
// Expression translation
// ---------------------------------------------------------------------------

// translateExpr recursively translates an AST expression to SQL.
func (g *SQLGenerator) translateExpr(expr ast.Expression) (string, error) {
	switch ex := expr.(type) {
	case *ast.InfixExpression:
		return g.translateInfix(ex)

	case *ast.PrefixExpression:
		if ex.Operator == "NOT" {
			inner, err := g.translateExpr(ex.Right)
			if err != nil {
				return "", err
			}
			return "NOT (" + inner + ")", nil
		}
		return "", fmt.Errorf("unsupported prefix operator: %s", ex.Operator)

	case *ast.IsNullExpression:
		field, err := g.fieldPath(ex.Expr)
		if err != nil {
			return "", err
		}
		// IS NULL checks existence, not value ordering — "auto" is safe on all backends.
		jsonField := g.dialect.JSONFieldAs(field, "auto")
		if ex.Not {
			return jsonField + " IS NOT NULL", nil
		}
		return jsonField + " IS NULL", nil

	case *ast.BetweenExpression:
		field, err := g.fieldPath(ex.Expr)
		if err != nil {
			return "", err
		}
		lowVal, err := g.literalValue(ex.Low)
		if err != nil {
			return "", err
		}
		highVal, err := g.literalValue(ex.High)
		if err != nil {
			return "", err
		}
		// Choose the OQL type based on bound types.
		// Both bounds numeric → "numeric" so that stored numeric strings sort
		// numerically. Either bound a string → "text" so that lexicographic
		// ordering is preserved — CAST(date_string AS REAL/NUMERIC) silently
		// coerces to the year portion (e.g. "2025-06-01" → 2025.0), making
		// all dates within the same year compare equal.
		oqlType := "text"
		if isNumericValue(lowVal) && isNumericValue(highVal) {
			oqlType = "numeric"
		}
		jsonField := g.dialect.JSONFieldAs(field, oqlType)
		lowPh := g.addArg(lowVal)
		highPh := g.addArg(highVal)
		sql := fmt.Sprintf("%s BETWEEN %s AND %s", jsonField, lowPh, highPh)
		if ex.Not {
			sql = "NOT (" + sql + ")"
		}
		return sql, nil

	case *ast.InExpression:
		field, err := g.fieldPath(ex.Expr)
		if err != nil {
			return "", err
		}
		var placeholders []string
		var firstVal interface{}
		for _, v := range ex.Values {
			val, err := g.literalValue(v)
			if err != nil {
				return "", err
			}
			if firstVal == nil {
				firstVal = val
			}
			placeholders = append(placeholders, g.addArg(val))
		}
		// Infer extraction type from the first IN value. All values in a well-
		// formed IN list share the same type; heterogeneous lists are rare and
		// the first value is a reasonable approximation.
		inType := chooseType("=", firstVal)
		jsonField := g.dialect.JSONFieldAs(field, inType)
		sql := fmt.Sprintf("%s IN (%s)", jsonField, strings.Join(placeholders, ", "))
		if ex.Not {
			sql = "NOT (" + sql + ")"
		}
		return sql, nil

	case *ast.LikeExpression:
		field, err := g.fieldPath(ex.Expr)
		if err != nil {
			return "", err
		}
		patternVal, err := g.literalValue(ex.Pattern)
		if err != nil {
			return "", err
		}
		// LIKE is always a text pattern match.
		jsonField := g.dialect.JSONFieldAs(field, "text")
		ph := g.addArg(patternVal)
		sql := fmt.Sprintf("%s LIKE %s", jsonField, ph)
		if ex.Not {
			sql = "NOT (" + sql + ")"
		}
		return sql, nil

	default:
		return "", fmt.Errorf("unsupported expression type for push-down: %T", expr)
	}
}

// translateInfix handles AND, OR, and comparison operators.
func (g *SQLGenerator) translateInfix(ex *ast.InfixExpression) (string, error) {
	switch ex.Operator {
	case "AND", "OR":
		left, err := g.translateExpr(ex.Left)
		if err != nil {
			return "", err
		}
		right, err := g.translateExpr(ex.Right)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s) %s (%s)", left, ex.Operator, right), nil

	default:
		// Comparison: field <op> literal
		field, err := g.fieldPath(ex.Left)
		if err != nil {
			return "", err
		}
		val, err := g.literalValue(ex.Right)
		if err != nil {
			return "", err
		}

		// Determine whether to use numeric or text field extraction.
		// Numeric operators always need CAST; equality depends on the
		// literal's type — this matches the Go-side compareValues logic.
		jsonField := g.chooseFieldExtraction(field, ex.Operator, val)
		ph := g.addArg(val)

		// Map T-SQL <> to standard SQL != (both are valid in SQLite,
		// but normalising keeps output predictable for tests)
		op := ex.Operator
		if op == "<>" {
			op = "!="
		}

		return fmt.Sprintf("%s %s %s", jsonField, op, ph), nil
	}
}

// chooseType returns the OQL type token for a field extraction based on the
// comparison operator and the literal value's type. This is the single
// authoritative place where the operator+literal pair maps to a storage type.
//
// Rules:
//   - Ordering operators (>, <, >=, <=): "numeric" when RHS is numeric;
//     "text" otherwise (ISO 8601 timestamps sort correctly as text, and
//     CAST(date_string AS REAL/NUMERIC) silently truncates to the year).
//   - Equality (=, !=, <>): "numeric" when RHS is numeric; "text" otherwise.
//   - All other operators: "auto" (fall back to backend's native JSON type).
func chooseType(op string, val interface{}) string {
	switch op {
	case ">", "<", ">=", "<=", "=", "!=", "<>":
		if isNumericValue(val) {
			return "numeric"
		}
		return "text"
	default:
		return "auto"
	}
}

// chooseFieldExtraction is a convenience wrapper used at comparison sites.
// It calls chooseType and returns the appropriate dialect extraction expression.
func (g *SQLGenerator) chooseFieldExtraction(field, op string, val interface{}) string {
	return g.dialect.JSONFieldAs(field, chooseType(op, val))
}

// ---------------------------------------------------------------------------
// ORDER BY translation
// ---------------------------------------------------------------------------

func (g *SQLGenerator) translateOrderBy(items []*ast.OrderByItem) (string, error) {
	var parts []string
	for _, item := range items {
		field, err := g.fieldPath(item.Expression)
		if err != nil {
			return "", err
		}
		// ORDER BY without a literal context: use "auto" for now. A future
		// improvement is to thread schema type information through the generator
		// so that numeric fields can use "numeric" here. On PostgreSQL this
		// ordering will produce text-order results for numeric fields — schema
		// typing is required before a PostgreSQL dialect can be added.
		jsonField := g.dialect.JSONFieldAs(field, "auto")
		dir := "ASC"
		if item.Descending {
			dir = "DESC"
		}
		parts = append(parts, jsonField+" "+dir)
	}
	return strings.Join(parts, ", "), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fieldPath extracts the dotted field path from an Identifier or
// QualifiedIdentifier and validates it for safe embedding in SQL.
func (g *SQLGenerator) fieldPath(expr ast.Expression) (string, error) {
	var name string
	switch e := expr.(type) {
	case *ast.Identifier:
		name = e.Value
	case *ast.QualifiedIdentifier:
		name = e.String()
	default:
		return "", fmt.Errorf("expected field identifier, got %T", expr)
	}

	if err := validateFieldName(name); err != nil {
		return "", err
	}
	return name, nil
}

// literalValue extracts a Go value from an AST literal expression.
func (g *SQLGenerator) literalValue(expr ast.Expression) (interface{}, error) {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return e.Value, nil
	case *ast.FloatLiteral:
		return e.Value, nil
	case *ast.StringLiteral:
		return e.Value, nil
	case *ast.NullLiteral:
		return nil, nil
	case *ast.Identifier:
		// TRUE/FALSE are identifiers in T-SQL
		upper := strings.ToUpper(e.Value)
		switch upper {
		case "TRUE":
			return true, nil
		case "FALSE":
			return false, nil
		default:
			// Could be a field reference on the RHS — not supported in push-down
			return nil, fmt.Errorf("field reference %q on RHS not supported in push-down", e.Value)
		}
	default:
		return nil, fmt.Errorf("unsupported literal type: %T", expr)
	}
}

// evalTopCount extracts the integer count from a TOP clause.
// T-SQL: TOP 10 — the count is an expression in the AST.
func evalTopCount(top *ast.TopClause) (int64, error) {
	if top.Percent {
		return 0, fmt.Errorf("TOP PERCENT not supported in push-down")
	}
	if top.WithTies {
		return 0, fmt.Errorf("TOP WITH TIES not supported in push-down")
	}
	switch c := top.Count.(type) {
	case *ast.IntegerLiteral:
		return c.Value, nil
	default:
		return 0, fmt.Errorf("TOP count must be an integer literal for push-down, got %T", top.Count)
	}
}

// isNumericValue returns true if the value is a numeric type.
func isNumericValue(v interface{}) bool {
	switch v.(type) {
	case int, int64, float64, float32:
		return true
	default:
		return false
	}
}

// isNumeric returns true if s consists entirely of digits (e.g. "1", "42").
// Used to distinguish store-derived numeric tenant IDs (which target the
// tenant_id INTEGER column) from string-based tenant IDs (which target
// json_extract(data, '$.tenant_id')).
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// generateMutationSQL builds a push-down query for UPDATE/DELETE mutations.
// It produces a SELECT that returns the matching records (with their data)
// so the executor can then apply SET clauses or delete them.
func generateMutationSQL(where ast.Expression, entity, tenantID string, dialect SQLDialect) (*GeneratedSQL, error) {
	gen := NewSQLGenerator(dialect)

	baseSql, baseArg := dialect.BaseQuery(entity)
	gen.args = append(gen.args, baseArg)

	var clauses []string
	clauses = append(clauses, baseSql)

	if tenantID != "" {
		if isNumeric(tenantID) {
			clauses = append(clauses, fmt.Sprintf("AND tenant_id = %s",
				gen.addArg(tenantID)))
		} else {
			clauses = append(clauses, fmt.Sprintf("AND %s = %s",
				dialect.JSONFieldAs("tenant_id", "text"), gen.addArg(tenantID)))
		}
	}

	if where != nil {
		whereSQL, err := gen.translateExpr(where)
		if err != nil {
			return nil, fmt.Errorf("mutation WHERE translation: %w", err)
		}
		clauses = append(clauses, "AND ("+whereSQL+")")
	}

	return &GeneratedSQL{SQL: strings.Join(clauses, " "), Args: gen.args}, nil
}
