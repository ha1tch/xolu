// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ha1tch/tsqlparser/ast"
	"github.com/ha1tch/xolu/pkg/qs"
)

// AggregateFunc is a function that computes an aggregate over values
type AggregateFunc func(values []interface{}) interface{}

// Aggregates maps function names to their implementations
var Aggregates = map[string]AggregateFunc{
	"COUNT": aggCount,
	"SUM":   aggSum,
	"AVG":   aggAvg,
	"MIN":   aggMin,
	"MAX":   aggMax,
}

// Aggregate implementations delegate to pkg/qs so that OQL and Sulpher
// share a single canonical implementation.
func aggCount(values []interface{}) interface{} { return qs.AggCount(values) }
func aggSum(values []interface{}) interface{}   { return qs.AggSum(values) }
func aggAvg(values []interface{}) interface{}   { return qs.AggAvg(values) }
func aggMin(values []interface{}) interface{}   { return qs.AggMin(values) }
func aggMax(values []interface{}) interface{}   { return qs.AggMax(values) }

// toFloat delegates to pkg/qs but also handles string-encoded numerics,
// matching the original OQL behaviour. Returns 0 for unrecognised types
// (does not handle bool — use qs.ToFloat directly if bool support needed).
func toFloat(v interface{}) float64 {
	if f, ok := qs.ToFloatSafe(v); ok {
		// Original OQL toFloat did not handle bool
		if _, isBool := v.(bool); isBool {
			return 0
		}
		return f
	}
	if s, ok := v.(string); ok {
		// See toFloatSafe's own comment: strconv.ParseFloat, not
		// fmt.Sscanf, for the same reason (a leading-numeric-prefix
		// string like a timestamp must not silently parse as a bare
		// number).
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return 0
}

// compareValues delegates to pkg/qs.
func compareValues(a, b interface{}) int { return qs.CompareValues(a, b) }

// toFloatSafe delegates to pkg/qs but also handles string-encoded numerics.
func toFloatSafe(v interface{}) (float64, bool) {
	if f, ok := qs.ToFloatSafe(v); ok {
		return f, true
	}
	if s, ok := v.(string); ok {
		// strconv.ParseFloat, not fmt.Sscanf: Sscanf's "%f" verb matches
		// only a leading numeric prefix and reports success even when
		// trailing characters remain unconsumed -- "2026-08-03T02:04:33Z"
		// parsed as the bare float 2026, discarding everything after the
		// year. Every timestamp from the same year then compared as
		// numerically equal under havingCompare, which is silently wrong
		// for ORDER BY: a stable sort preserves original (insertion)
		// order among values it believes are tied, so DESC on a real
		// timestamp field correctly placed a single genuine outlier
		// first, then silently fell back to ascending insertion order
		// for everything else, which happened to look like a coherent
		// but wrong result rather than an obvious failure. ParseFloat
		// requires the entire string to be a valid float, so a genuine
		// numeric string ("9369.41", the case this function exists for
		// -- denormalised decimal aggregate results) still succeeds,
		// while any string merely starting with digits now correctly
		// fails and falls through to qs.CompareValues instead.
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// Aggregator handles GROUP BY and aggregate function execution
type Aggregator struct {
	// decimalFields is the set of field names that should use
	// decimal-precise aggregation instead of float64. Set via
	// SetDecimalFields before calling Aggregate.
	decimalFields map[string]bool
}

// NewAggregator creates a new aggregator
func NewAggregator() *Aggregator {
	return &Aggregator{}
}

// SetDecimalFields configures which fields should use decimal-precise
// aggregation. Call this before Aggregate when adapted table metadata
// indicates decimal columns.
func (a *Aggregator) SetDecimalFields(fields map[string]bool) {
	a.decimalFields = fields
}

// isDecimalField checks if a field name should use decimal aggregation.
func (a *Aggregator) isDecimalField(fieldName string) bool {
	if a.decimalFields == nil {
		return false
	}
	return a.decimalFields[fieldName]
}

// Aggregate groups records and applies aggregate functions
func (a *Aggregator) Aggregate(
	records []map[string]interface{},
	columns []ast.SelectColumn,
	groupBy []ast.Expression,
	having ast.Expression,
) []map[string]interface{} {
	// If no GROUP BY and no aggregates, return as-is
	if len(groupBy) == 0 && !hasAggregates(columns) {
		return records
	}

	// Group records by GROUP BY keys
	groups := make(map[string][]map[string]interface{})
	groupOrder := []string{} // Preserve order

	for _, rec := range records {
		key := a.buildGroupKey(rec, groupBy)
		if _, exists := groups[key]; !exists {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], rec)
	}

	// SQL standard: aggregate queries without GROUP BY always produce exactly
	// one row, even when the input set is empty. The implicit single group
	// must exist so that COUNT(*) returns 0, SUM/AVG/MIN/MAX return NULL.
	// With GROUP BY, zero input rows correctly produces zero output rows
	// (no groups formed).
	if len(groupBy) == 0 && len(groupOrder) == 0 && hasAggregates(columns) {
		groupOrder = append(groupOrder, "")
		groups[""] = nil // empty group — aggregate functions receive empty slices
	}

	// Apply aggregates to each group
	var results []map[string]interface{}
	for _, key := range groupOrder {
		groupRecords := groups[key]
		row := make(map[string]interface{})

		for _, col := range columns {
			alias := columnAlias(col)

			if isAggregate(col.Expression) {
				// Aggregate function
				fc := col.Expression.(*ast.FunctionCall)
				funcName := strings.ToUpper(exprToString(fc.Function))

				if aggFn, exists := Aggregates[funcName]; exists {
					var values []interface{}
					if funcName == "COUNT" && isCountStar(fc) {
						// COUNT(*) counts all rows
						values = make([]interface{}, len(groupRecords))
						for i := range groupRecords {
							values[i] = 1 // Non-nil placeholder
						}
					} else if len(fc.Arguments) > 0 {
						values = a.extractColumnValues(groupRecords, fc.Arguments[0])

						// COUNT(DISTINCT x) / SUM(DISTINCT x) etc. — deduplicate
						// extracted values before aggregating. nil (absent field)
						// is excluded from the distinct set, consistent with SQL semantics.
						if fc.Distinct {
							seen := make(map[string]struct{}, len(values))
							deduped := make([]interface{}, 0, len(values))
							for _, v := range values {
								if v == nil {
									continue
								}
								key := fmt.Sprintf("%T:%v", v, v)
								if _, exists := seen[key]; !exists {
									seen[key] = struct{}{}
									deduped = append(deduped, v)
								}
							}
							values = deduped
						}

						// Use decimal-precise aggregation if the field is decimal
						if funcName != "COUNT" {
							argName := exprToString(fc.Arguments[0])
							if a.isDecimalField(argName) {
								if decFn, ok := DecimalAggregates[funcName]; ok {
									aggFn = decFn
								}
							}
						}
					}
					result := aggFn(values)
					row[alias] = result
					// Also store under the expression string for HAVING lookup
					exprStr := exprToString(col.Expression)
					if exprStr != alias {
						row[exprStr] = result
					}
				}
			} else {
				// Non-aggregate column (must be in GROUP BY)
				colName := exprToString(col.Expression)
				if len(groupRecords) > 0 {
					row[alias] = getFieldValue(groupRecords[0], colName)
				}
			}
		}

		// Apply HAVING filter
		if having == nil || a.evalCondition(row, having) {
			results = append(results, row)
		}
	}

	return results
}

// buildGroupKey creates a string key for grouping
func (a *Aggregator) buildGroupKey(rec map[string]interface{}, groupBy []ast.Expression) string {
	if len(groupBy) == 0 {
		return "" // Single group for all records
	}

	var parts []string
	for _, expr := range groupBy {
		colName := exprToString(expr)
		val := getFieldValue(rec, colName)
		parts = append(parts, fmt.Sprintf("%v", val))
	}
	return strings.Join(parts, "|")
}

// extractColumnValues gets values for a column across records
func (a *Aggregator) extractColumnValues(records []map[string]interface{}, expr ast.Expression) []interface{} {
	colName := exprToString(expr)
	values := make([]interface{}, len(records))
	for i, rec := range records {
		values[i] = getFieldValue(rec, colName)
	}
	return values
}

// EvalCondition evaluates a HAVING condition against a row.
// Exported for use by aggregate push-down HAVING filter.
func (a *Aggregator) EvalCondition(row map[string]interface{}, expr ast.Expression) bool {
	return a.evalCondition(row, expr)
}

// evalCondition evaluates a HAVING condition
func (a *Aggregator) evalCondition(row map[string]interface{}, expr ast.Expression) bool {
	// Simple evaluation for common cases
	switch e := expr.(type) {
	case *ast.InfixExpression:
		left := a.evalExpr(row, e.Left)
		right := a.evalExpr(row, e.Right)

		switch e.Operator {
		case "=":
			return havingCompare(left, right) == 0
		case "!=", "<>":
			return havingCompare(left, right) != 0
		case "<":
			return havingCompare(left, right) < 0
		case ">":
			return havingCompare(left, right) > 0
		case "<=":
			return havingCompare(left, right) <= 0
		case ">=":
			return havingCompare(left, right) >= 0
		case "AND":
			return a.evalCondition(row, e.Left) && a.evalCondition(row, e.Right)
		case "OR":
			return a.evalCondition(row, e.Left) || a.evalCondition(row, e.Right)
		}
	}
	return true
}

// havingCompare compares two values for HAVING evaluation.
// Always attempts numeric comparison first (including string-encoded
// numerics such as denormalised decimal aggregate results like "42372.89"),
// falling back to qs.CompareValues for non-numeric types.
func havingCompare(a, b interface{}) int {
	af, aOk := toFloatSafe(a)
	bf, bOk := toFloatSafe(b)
	if aOk && bOk {
		if af < bf {
			return -1
		}
		if af > bf {
			return 1
		}
		return 0
	}
	return qs.CompareValues(a, b)
}

// evalExpr evaluates an expression against a row
func (a *Aggregator) evalExpr(row map[string]interface{}, expr ast.Expression) interface{} {
	switch e := expr.(type) {
	case *ast.Identifier:
		return row[e.Value]
	case *ast.QualifiedIdentifier:
		return row[e.String()]
	case *ast.IntegerLiteral:
		return e.Value
	case *ast.FloatLiteral:
		return e.Value
	case *ast.StringLiteral:
		return e.Value
	case *ast.FunctionCall:
		// Check if result is already computed under function name
		alias := exprToString(e)
		if val, ok := row[alias]; ok {
			return val
		}
		// Also check common variations (COUNT(*) might be stored differently)
		funcName := strings.ToUpper(exprToString(e.Function))
		for key, val := range row {
			// If the key contains the function name, it might be our aggregate
			if strings.Contains(strings.ToUpper(key), funcName) {
				return val
			}
		}
		// Try to compute the aggregate on the fly if we have the data
		// (this handles HAVING referencing aggregates not in SELECT)
	}
	return nil
}

// hasAggregates checks if any column uses an aggregate function
func hasAggregates(columns []ast.SelectColumn) bool {
	for _, col := range columns {
		if isAggregate(col.Expression) {
			return true
		}
	}
	return false
}

// isAggregate checks if an expression is an aggregate function call
func isAggregate(expr ast.Expression) bool {
	fc, ok := expr.(*ast.FunctionCall)
	if !ok {
		return false
	}
	funcName := strings.ToUpper(exprToString(fc.Function))
	_, isAgg := Aggregates[funcName]
	return isAgg
}

// isCountStar checks if a function call is COUNT(*)
func isCountStar(fc *ast.FunctionCall) bool {
	if len(fc.Arguments) == 0 {
		return true // COUNT() with no args treated as COUNT(*)
	}
	if len(fc.Arguments) == 1 {
		// Check if argument is * (as Identifier)
		if ident, ok := fc.Arguments[0].(*ast.Identifier); ok && ident.Value == "*" {
			return true
		}
		// Check if argument is * (as string)
		if fc.Arguments[0].String() == "*" {
			return true
		}
	}
	return false
}

// columnAlias returns the alias or expression string for a column
func columnAlias(col ast.SelectColumn) string {
	if col.Alias != nil {
		return col.Alias.Value
	}
	return exprToString(col.Expression)
}

// exprToString converts an expression to its string representation
func exprToString(expr ast.Expression) string {
	if expr == nil {
		return ""
	}
	return expr.String()
}

// getFieldValue gets a field value from a record, handling nested paths
func getFieldValue(rec map[string]interface{}, field string) interface{} {
	// Direct lookup first
	if val, ok := rec[field]; ok {
		return val
	}

	// Try without table prefix (e.g., "items.value" -> "reading")
	if idx := strings.LastIndex(field, "."); idx != -1 {
		shortName := field[idx+1:]
		if val, ok := rec[shortName]; ok {
			return val
		}
	}

	return nil
}

// OrderBy sorts records by the specified order items
func OrderBy(records []map[string]interface{}, orderBy []*ast.OrderByItem) []map[string]interface{} {
	if len(orderBy) == 0 {
		return records
	}

	sort.SliceStable(records, func(i, j int) bool {
		for _, ob := range orderBy {
			colName := exprToString(ob.Expression)
			vi := getFieldValue(records[i], colName)
			vj := getFieldValue(records[j], colName)

			// Use havingCompare (numeric-aware, handles string-encoded decimals)
			// rather than compareValues so that denormalised aggregate results
			// like "9369.41" sort numerically rather than lexicographically.
			cmp := havingCompare(vi, vj)
			if cmp == 0 {
				continue
			}

			if ob.Descending {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})

	return records
}

// ApplyTop limits results to TOP n
func ApplyTop(records []map[string]interface{}, top *ast.TopClause) []map[string]interface{} {
	if top == nil {
		return records
	}

	limit := 0
	if intLit, ok := top.Count.(*ast.IntegerLiteral); ok {
		limit = int(intLit.Value)
	}

	if limit > 0 && len(records) > limit {
		return records[:limit]
	}
	return records
}
