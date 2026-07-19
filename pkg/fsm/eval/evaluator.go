package eval

import (
	"fmt"
	"strings"
	"time"

	"github.com/ha1tch/tsqlparser/ast"
)

// ExpressionEvaluator evaluates T-SQL expressions at runtime
type ExpressionEvaluator struct {
	variables map[string]Value
	functions *FunctionRegistry
}

// NewExpressionEvaluator creates a new expression evaluator
func NewExpressionEvaluator() *ExpressionEvaluator {
	return &ExpressionEvaluator{
		variables: make(map[string]Value),
		functions: NewFunctionRegistry(),
	}
}

// SetVariable sets a variable value
func (e *ExpressionEvaluator) SetVariable(name string, value Value) {
	// Normalize name (remove @ prefix if present)
	name = strings.TrimPrefix(strings.ToLower(name), "@")
	e.variables[name] = value
}

// GetVariable gets a variable value
func (e *ExpressionEvaluator) GetVariable(name string) (Value, bool) {
	name = strings.TrimPrefix(strings.ToLower(name), "@")
	v, ok := e.variables[name]
	return v, ok
}

// SetVariables sets multiple variables from a map
func (e *ExpressionEvaluator) SetVariables(vars map[string]interface{}) {
	for name, val := range vars {
		e.SetVariable(name, ToValue(val))
	}
}

// Evaluate evaluates an AST expression and returns its value
func (e *ExpressionEvaluator) Evaluate(expr ast.Expression) (Value, error) {
	if expr == nil {
		return Null(TypeUnknown), nil
	}

	switch ex := expr.(type) {
	case *ast.IntegerLiteral:
		return NewBigInt(ex.Value), nil

	case *ast.FloatLiteral:
		return NewFloat(ex.Value), nil

	case *ast.StringLiteral:
		return NewVarChar(ex.Value, -1), nil

	case *ast.NullLiteral:
		return Null(TypeUnknown), nil

	case *ast.Variable:
		return e.evaluateVariable(ex)

	case *ast.Identifier:
		// Could be a column reference or a function name without parens
		// For now, treat as variable
		name := ex.Value
		if v, ok := e.GetVariable(name); ok {
			return v, nil
		}
		return Null(TypeUnknown), nil

	case *ast.QualifiedIdentifier:
		// e.g., table.column — try the full dotted name first (supports
		// payload.field convention used in FSM guards), then fall back to
		// just the last part for legacy SQL column references.
		if len(ex.Parts) > 0 {
			// Build full dotted name: "payload.result"
			parts := make([]string, len(ex.Parts))
			for i, p := range ex.Parts {
				parts[i] = p.Value
			}
			fullName := strings.Join(parts, ".")
			if v, ok := e.GetVariable(fullName); ok {
				return v, nil
			}
			// Fall back to last part only.
			lastName := ex.Parts[len(ex.Parts)-1].Value
			if v, ok := e.GetVariable(lastName); ok {
				return v, nil
			}
		}
		return Null(TypeUnknown), nil

	case *ast.PrefixExpression:
		return e.evaluatePrefixExpression(ex)

	case *ast.InfixExpression:
		return e.evaluateInfixExpression(ex)

	case *ast.FunctionCall:
		return e.evaluateFunctionCall(ex)

	case *ast.CaseExpression:
		return e.evaluateCaseExpression(ex)

	case *ast.CastExpression:
		return e.evaluateCastExpression(ex)

	case *ast.ConvertExpression:
		return e.evaluateConvertExpression(ex)

	case *ast.BetweenExpression:
		return e.evaluateBetweenExpression(ex)

	case *ast.InExpression:
		return e.evaluateInExpression(ex)

	case *ast.LikeExpression:
		return e.evaluateLikeExpression(ex)

	case *ast.IsNullExpression:
		return e.evaluateIsNullExpression(ex)

	case *ast.ExistsExpression:
		// EXISTS would require query execution - not supported in expression evaluator
		return Value{}, fmt.Errorf("EXISTS not supported in expression evaluation")

	case *ast.SubqueryExpression:
		return Value{}, fmt.Errorf("subqueries not supported in expression evaluation")

	case *ast.TupleExpression:
		// Handle tuple/parenthesized expressions - evaluate first element
		if len(ex.Elements) == 1 {
			return e.Evaluate(ex.Elements[0])
		}
		return Value{}, fmt.Errorf("tuple expressions not supported in scalar context")

	default:
		return Value{}, fmt.Errorf("unsupported expression type: %T", expr)
	}
}

func (e *ExpressionEvaluator) evaluateVariable(v *ast.Variable) (Value, error) {
	name := v.Name

	// Check if it's a global variable (starts with @@)
	if strings.HasPrefix(name, "@@") {
		return e.evaluateGlobalVariable(name)
	}

	// Local variable - remove @ prefix if present
	name = strings.TrimPrefix(name, "@")
	if val, ok := e.GetVariable(name); ok {
		return val, nil
	}
	return Null(TypeUnknown), nil
}

func (e *ExpressionEvaluator) evaluateGlobalVariable(name string) (Value, error) {
	upperName := strings.ToUpper(name)

	switch upperName {
	case "@@ROWCOUNT":
		if val, ok := e.GetVariable("@@ROWCOUNT"); ok {
			return val, nil
		}
		return NewInt(0), nil

	case "@@ERROR":
		if val, ok := e.GetVariable("@@ERROR"); ok {
			return val, nil
		}
		return NewInt(0), nil

	case "@@IDENTITY":
		if val, ok := e.GetVariable("@@IDENTITY"); ok {
			return val, nil
		}
		return Null(TypeBigInt), nil

	case "@@FETCH_STATUS":
		if val, ok := e.GetVariable("@@FETCH_STATUS"); ok {
			return val, nil
		}
		return NewInt(-1), nil // -1 = no more rows

	case "@@TRANCOUNT":
		if val, ok := e.GetVariable("@@TRANCOUNT"); ok {
			return val, nil
		}
		return NewInt(0), nil

	case "@@SPID":
		// Session ID - return a dummy value
		return NewInt(1), nil

	case "@@VERSION":
		return NewVarChar("Microsoft SQL Server 2019 (RTM-CU28) - 15.0.4415.2 (X64) \n\tDec 13 2024 18:00:00 \n\tCopyright (C) 2019 Microsoft Corporation\n\tDeveloper Edition (64-bit) on Linux (aul-server)", -1), nil

	case "@@SERVERNAME":
		return NewVarChar("aul", -1), nil

	case "@@LANGUAGE":
		return NewVarChar("us_english", -1), nil

	default:
		return Null(TypeUnknown), nil
	}
}

func (e *ExpressionEvaluator) evaluatePrefixExpression(ex *ast.PrefixExpression) (Value, error) {
	right, err := e.Evaluate(ex.Right)
	if err != nil {
		return Value{}, err
	}

	switch ex.Operator {
	case "-":
		return right.Neg(), nil
	case "+":
		return right, nil
	case "NOT":
		return right.Not(), nil
	case "~":
		return right.BitwiseNot(), nil
	default:
		return Value{}, fmt.Errorf("unknown prefix operator: %s", ex.Operator)
	}
}

func (e *ExpressionEvaluator) evaluateInfixExpression(ex *ast.InfixExpression) (Value, error) {
	left, err := e.Evaluate(ex.Left)
	if err != nil {
		return Value{}, err
	}

	// Short-circuit evaluation for AND/OR
	op := strings.ToUpper(ex.Operator)
	if op == "AND" {
		if left.IsNull {
			right, err := e.Evaluate(ex.Right)
			if err != nil {
				return Value{}, err
			}
			return left.And(right), nil
		}
		if !left.AsBool() {
			return NewBit(false), nil
		}
		right, err := e.Evaluate(ex.Right)
		if err != nil {
			return Value{}, err
		}
		return left.And(right), nil
	}

	if op == "OR" {
		if !left.IsNull && left.AsBool() {
			return NewBit(true), nil
		}
		right, err := e.Evaluate(ex.Right)
		if err != nil {
			return Value{}, err
		}
		return left.Or(right), nil
	}

	right, err := e.Evaluate(ex.Right)
	if err != nil {
		return Value{}, err
	}

	switch op {
	// Arithmetic
	case "+":
		return left.Add(right), nil
	case "-":
		return left.Sub(right), nil
	case "*":
		return left.Mul(right), nil
	case "/":
		return left.Div(right), nil
	case "%":
		return left.Mod(right), nil

	// Comparison
	case "=":
		return left.Equals(right), nil
	case "<>", "!=":
		return left.NotEquals(right), nil
	case "<":
		return left.LessThan(right), nil
	case "<=":
		return left.LessThanOrEqual(right), nil
	case ">":
		return left.GreaterThan(right), nil
	case ">=":
		return left.GreaterThanOrEqual(right), nil

	// Bitwise
	case "&":
		return left.BitwiseAnd(right), nil
	case "|":
		return left.BitwiseOr(right), nil
	case "^":
		return left.BitwiseXor(right), nil

	// String concatenation (also handled by +)
	case "||":
		return NewVarChar(left.AsString()+right.AsString(), -1), nil

	default:
		return Value{}, fmt.Errorf("unknown operator: %s", ex.Operator)
	}
}

func (e *ExpressionEvaluator) evaluateFunctionCall(fc *ast.FunctionCall) (Value, error) {
	funcName := ""
	if fc.Function != nil {
		funcName = fc.Function.String()
	}

	// Evaluate arguments
	args := make([]Value, len(fc.Arguments))
	for i, arg := range fc.Arguments {
		// Special handling for datepart arguments (first arg is interval name)
		if i == 0 && isDatePartFunction(funcName) {
			if ident, ok := arg.(*ast.Identifier); ok {
				args[i] = NewVarChar(ident.Value, -1)
				continue
			}
		}

		val, err := e.Evaluate(arg)
		if err != nil {
			return Value{}, err
		}
		args[i] = val
	}

	return e.functions.Call(funcName, args)
}

func isDatePartFunction(name string) bool {
	upper := strings.ToUpper(name)
	return upper == "DATEADD" || upper == "DATEDIFF" || upper == "DATEDIFF_BIG" ||
		upper == "DATEPART" || upper == "DATENAME"
}

func (e *ExpressionEvaluator) evaluateCaseExpression(ex *ast.CaseExpression) (Value, error) {
	// Simple CASE: CASE expr WHEN value THEN result
	if ex.Operand != nil {
		operand, err := e.Evaluate(ex.Operand)
		if err != nil {
			return Value{}, err
		}

		for _, when := range ex.WhenClauses {
			condition, err := e.Evaluate(when.Condition)
			if err != nil {
				return Value{}, err
			}

			if !operand.IsNull && !condition.IsNull && operand.Compare(condition) == 0 {
				return e.Evaluate(when.Result)
			}
		}
	} else {
		// Searched CASE: CASE WHEN condition THEN result
		for _, when := range ex.WhenClauses {
			condition, err := e.Evaluate(when.Condition)
			if err != nil {
				return Value{}, err
			}

			if condition.IsTruthy() {
				return e.Evaluate(when.Result)
			}
		}
	}

	// ELSE clause or NULL
	if ex.ElseClause != nil {
		return e.Evaluate(ex.ElseClause)
	}
	return Null(TypeUnknown), nil
}

func (e *ExpressionEvaluator) evaluateCastExpression(ex *ast.CastExpression) (Value, error) {
	val, err := e.Evaluate(ex.Expression)
	if err != nil {
		return Value{}, err
	}

	if ex.TargetType == nil {
		return val, nil
	}

	targetType, precision, scale, maxLen := ParseDataType(ex.TargetType.String())
	return Cast(val, targetType, precision, scale, maxLen)
}

func (e *ExpressionEvaluator) evaluateConvertExpression(ex *ast.ConvertExpression) (Value, error) {
	val, err := e.Evaluate(ex.Expression)
	if err != nil {
		return Value{}, err
	}

	if ex.TargetType == nil {
		return val, nil
	}

	targetType, precision, scale, maxLen := ParseDataType(ex.TargetType.String())

	style := 0
	if ex.Style != nil {
		styleVal, err := e.Evaluate(ex.Style)
		if err != nil {
			return Value{}, err
		}
		style = int(styleVal.AsInt())
	}

	return Convert(val, targetType, precision, scale, maxLen, style)
}

func (e *ExpressionEvaluator) evaluateBetweenExpression(ex *ast.BetweenExpression) (Value, error) {
	val, err := e.Evaluate(ex.Expr)
	if err != nil {
		return Value{}, err
	}

	low, err := e.Evaluate(ex.Low)
	if err != nil {
		return Value{}, err
	}

	high, err := e.Evaluate(ex.High)
	if err != nil {
		return Value{}, err
	}

	if val.IsNull || low.IsNull || high.IsNull {
		return Null(TypeBit), nil
	}

	inRange := val.Compare(low) >= 0 && val.Compare(high) <= 0
	if ex.Not {
		return NewBit(!inRange), nil
	}
	return NewBit(inRange), nil
}

func (e *ExpressionEvaluator) evaluateInExpression(ex *ast.InExpression) (Value, error) {
	val, err := e.Evaluate(ex.Expr)
	if err != nil {
		return Value{}, err
	}

	if val.IsNull {
		return Null(TypeBit), nil
	}

	for _, item := range ex.Values {
		itemVal, err := e.Evaluate(item)
		if err != nil {
			return Value{}, err
		}

		if !itemVal.IsNull && val.Compare(itemVal) == 0 {
			if ex.Not {
				return NewBit(false), nil
			}
			return NewBit(true), nil
		}
	}

	if ex.Not {
		return NewBit(true), nil
	}
	return NewBit(false), nil
}

func (e *ExpressionEvaluator) evaluateLikeExpression(ex *ast.LikeExpression) (Value, error) {
	val, err := e.Evaluate(ex.Expr)
	if err != nil {
		return Value{}, err
	}

	pattern, err := e.Evaluate(ex.Pattern)
	if err != nil {
		return Value{}, err
	}

	if val.IsNull || pattern.IsNull {
		return Null(TypeBit), nil
	}

	matches := matchLikePattern(val.AsString(), pattern.AsString())
	if ex.Not {
		return NewBit(!matches), nil
	}
	return NewBit(matches), nil
}

// matchLikePattern implements SQL LIKE pattern matching
func matchLikePattern(s, pattern string) bool {
	// Convert SQL LIKE pattern to a simple matcher
	// % = any characters, _ = single character
	// This is a simplified implementation

	sIdx := 0
	pIdx := 0
	sLen := len(s)
	pLen := len(pattern)
	starIdx := -1
	matchIdx := 0

	for sIdx < sLen {
		if pIdx < pLen && (pattern[pIdx] == '_' || pattern[pIdx] == s[sIdx]) {
			sIdx++
			pIdx++
		} else if pIdx < pLen && pattern[pIdx] == '%' {
			starIdx = pIdx
			matchIdx = sIdx
			pIdx++
		} else if starIdx != -1 {
			pIdx = starIdx + 1
			matchIdx++
			sIdx = matchIdx
		} else {
			return false
		}
	}

	for pIdx < pLen && pattern[pIdx] == '%' {
		pIdx++
	}

	return pIdx == pLen
}

func (e *ExpressionEvaluator) evaluateIsNullExpression(ex *ast.IsNullExpression) (Value, error) {
	val, err := e.Evaluate(ex.Expr)
	if err != nil {
		return Value{}, err
	}

	isNull := val.IsNull
	if ex.Not {
		return NewBit(!isNull), nil
	}
	return NewBit(isNull), nil
}

// ToValue converts a Go value to a runtime Value
func ToValue(v interface{}) Value {
	if v == nil {
		return Null(TypeUnknown)
	}

	switch val := v.(type) {
	case Value:
		return val
	case bool:
		return NewBit(val)
	case int:
		return NewInt(int64(val))
	case int8:
		return NewTinyInt(uint8(val))
	case int16:
		return NewSmallInt(val)
	case int32:
		return NewInt(int64(val))
	case int64:
		return NewBigInt(val)
	case uint8:
		return NewTinyInt(val)
	case uint16:
		return NewInt(int64(val))
	case uint32:
		return NewBigInt(int64(val))
	case uint64:
		return NewBigInt(int64(val))
	case float32:
		return NewReal(val)
	case float64:
		return NewFloat(val)
	case string:
		return NewVarChar(val, -1)
	case []byte:
		return NewBinary(val)
	case time.Time:
		return NewDateTime(val)
	default:
		// Try to convert via string representation
		return NewVarChar(fmt.Sprintf("%v", v), -1)
	}
}

// FromValue converts a runtime Value to a Go value
func FromValue(v Value) interface{} {
	if v.IsNull {
		return nil
	}

	switch v.Type {
	case TypeBit:
		return v.AsBool()
	case TypeTinyInt:
		return uint8(v.intVal)
	case TypeSmallInt:
		return int16(v.intVal)
	case TypeInt:
		return int32(v.intVal)
	case TypeBigInt:
		return v.intVal
	case TypeFloat, TypeReal:
		return v.floatVal
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		return v.decimalVal
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar, TypeText, TypeNText:
		return v.stringVal
	case TypeDate, TypeTime, TypeDateTime, TypeDateTime2, TypeSmallDateTime:
		return v.timeVal
	case TypeBinary, TypeVarBinary:
		return v.bytesVal
	default:
		return v.AsString()
	}
}
