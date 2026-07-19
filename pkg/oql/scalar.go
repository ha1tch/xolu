// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"

	"github.com/ha1tch/tsqlparser/ast"
	"github.com/ha1tch/xolu/pkg/qs"
)

// ScalarFunc is a function that operates on a single value and returns a result.
type ScalarFunc func(args []interface{}) interface{}

// ScalarFunctions maps function names to their implementations.
// ScalarFunctions maps T-SQL function names to implementations.
// Implementations live in pkg/qs and are shared with Sulpher.
// OQL-specific aliases (e.g. ISNULL) are added here.
var ScalarFunctions = map[string]ScalarFunc{
	"DATE_TRUNC": qs.ScalarDateTrunc,
	"UPPER":      qs.ScalarUpper,
	"LOWER":      qs.ScalarLower,
	"LEN":        qs.ScalarLen,
	"TRIM":       qs.ScalarTrim,
	"COALESCE":   qs.ScalarCoalesce,
	"ISNULL":     qs.ScalarCoalesce, // T-SQL alias for COALESCE with 2 args
	"CONCAT":     qs.ScalarConcat,
	"CAST":       qs.ScalarCast,
	"ABS":        qs.ScalarAbs,
	"ROUND":      qs.ScalarRound,
	"FLOOR":      qs.ScalarFloor,
	"CEILING":    qs.ScalarCeiling,
	"GETDATE":    qs.ScalarGetDate,
	"GETUTCDATE": qs.ScalarGetUTCDate,
	"YEAR":       qs.ScalarYear,
	"MONTH":      qs.ScalarMonth,
	"DAY":        qs.ScalarDay,
	"DATEPART":   qs.ScalarDatePart,
	"DATEDIFF":   qs.ScalarDateDiff,
	"SUBSTRING":  qs.ScalarSubstring,
	"LEFT":       qs.ScalarLeft,
	"RIGHT":      qs.ScalarRight,
	"REPLACE":    qs.ScalarReplace,
	"CHARINDEX":  qs.ScalarCharIndex,
	"NEWID":      qs.ScalarNewID,
}

// RegisterScalarFunc registers a new scalar function in the OQL function map.
// The name is normalised to uppercase. Calling this with an existing name
// overwrites the previous registration. Safe to call from init().
func RegisterScalarFunc(name string, fn ScalarFunc) {
	ScalarFunctions[strings.ToUpper(name)] = fn
}

// IsScalarFunction checks whether a function call is a scalar (non-aggregate)
// function. Returns true if the function name matches a known scalar.
func IsScalarFunction(expr ast.Expression) bool {
	fc, ok := expr.(*ast.FunctionCall)
	if !ok {
		return false
	}
	funcName := strings.ToUpper(exprToString(fc.Function))
	_, isScalar := ScalarFunctions[funcName]
	return isScalar
}

// EvalScalarFunction evaluates a scalar function call against a record.
// The evalFn callback is used to resolve argument expressions to values.
func EvalScalarFunction(fc *ast.FunctionCall, evalFn func(ast.Expression) interface{}) interface{} {
	funcName := strings.ToUpper(exprToString(fc.Function))
	fn, exists := ScalarFunctions[funcName]
	if !exists {
		return nil
	}

	// Evaluate arguments
	args := make([]interface{}, len(fc.Arguments))
	for i, arg := range fc.Arguments {
		args[i] = evalFn(arg)
	}

	return fn(args)
}
