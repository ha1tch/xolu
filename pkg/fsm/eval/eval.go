// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package eval provides T-SQL expression evaluation for xolu FSM guard
// conditions and set clauses.
//
// It is a surgical extraction of the ExpressionEvaluator from
// github.com/ha1tch/aulsql/pkg/tsqlruntime. The Interpreter,
// ExecutionContext, TempTableManager, CursorManager, and all database
// machinery have been removed. Only the expression evaluator, the Value
// type system, ToValue, and the built-in function registry are retained.
//
// # Entry points
//
// EvalGuard evaluates a boolean guard expression against machine variables
// and a walk payload:
//
//	ok, err := eval.EvalGuard(e, "payload.result = 'pass' AND @retries < 3",
//	    map[string]interface{}{"retries": 2},
//	    map[string]interface{}{"result": "pass", "technician": "alice"})
//
// EvalSet evaluates an arithmetic set-clause expression and returns the new
// variable value:
//
//	val, err := eval.EvalSet(e, "@retries + 1", map[string]interface{}{"retries": 2})
//
// # Payload binding convention
//
// Payload fields are bound into the variable map with the prefix "payload."
// so that the expression `payload.result` resolves to the payload field
// named "result". This prefix is flattened — nested payload objects are not
// supported in guard expressions; only top-level string/number/bool fields
// are accessible.
//
// Machine variables are bound without prefix. A variable declared as
// `@retries INTEGER` is bound as key "retries" (the @ is stripped by
// SetVariable). Guard expressions reference it as `@retries`.
//
// # Generator functions
//
// The four stateless generators are registered on every Evaluator so they
// are available in FSM set clauses:
//
//	SET @id = UUID_V4()
//	SET @ref = CUID()
package eval

import (
	"fmt"
	"strings"

	"github.com/ha1tch/tsqlparser/ast"
	"github.com/ha1tch/tsqlparser/lexer"
	"github.com/ha1tch/tsqlparser/parser"
	"github.com/ha1tch/tsqlparser/token"

	"github.com/google/uuid"
	cuidlib "github.com/lucsky/cuid"
	ulidlib "github.com/oklog/ulid/v2"
)

// Evaluator wraps ExpressionEvaluator with the xolu-specific API.
// Create one per FSM definition validation or per walk execution.
// Evaluators are not safe for concurrent use.
type Evaluator struct {
	inner          *ExpressionEvaluator
	seqIncrementor SeqIncrementor
}

// New creates a new Evaluator with the built-in function registry seeded
// with the four stateless generators.
func New() *Evaluator {
	e := &Evaluator{inner: NewExpressionEvaluator()}
	e.registerGenerators()
	return e
}

// registerGenerators adds the four stateless generator functions so they
// can be called from FSM set clauses (e.g. SET @id = UUID_V4()).
func (e *Evaluator) registerGenerators() {
	e.inner.functions.Register("UUID_V4", func(_ []Value) (Value, error) {
		id, err := uuid.NewRandom()
		if err != nil {
			return Null(TypeUnknown), err
		}
		return NewVarChar(id.String(), -1), nil
	})
	e.inner.functions.Register("UUID_V7", func(_ []Value) (Value, error) {
		id, err := uuid.NewV7()
		if err != nil {
			return Null(TypeUnknown), err
		}
		return NewVarChar(id.String(), -1), nil
	})
	e.inner.functions.Register("CUID", func(_ []Value) (Value, error) {
		return NewVarChar(cuidlib.New(), -1), nil
	})
	e.inner.functions.Register("ULID", func(_ []Value) (Value, error) {
		return NewVarChar(ulidlib.Make().String(), -1), nil
	})
}

// RegisterFunc registers a custom function on this Evaluator.
// name is normalised to uppercase. Use this to register @SEQ, @GEN,
// or domain-specific functions needed in a specific FSM definition.
func (e *Evaluator) RegisterFunc(name string, fn func(args []Value) (Value, error)) {
	e.inner.functions.Register(name, fn)
}

// BindVars binds machine variable values into the evaluator.
// The @ prefix is stripped from keys; `@retries` and `retries` both bind
// as "retries". Values are converted via ToValue.
func (e *Evaluator) BindVars(vars map[string]interface{}) {
	e.inner.SetVariables(vars)
}

// BindPayload binds walk payload fields into the evaluator under the
// "payload." prefix. A payload field "result" is accessible in expressions
// as `payload.result`. Only top-level string, number, and bool fields are
// bound; nested objects are skipped.
func (e *Evaluator) BindPayload(payload map[string]interface{}) {
	for k, v := range payload {
		// Only bind scalar values; nested objects are not accessible.
		switch v.(type) {
		case string, int, int64, float64, bool, nil:
			e.inner.SetVariable("payload."+k, ToValue(v))
		}
	}
}

// BindQuery binds transition pre-query result columns under the "query."
// prefix, so a guard or set clause can read query.<column>. Like BindPayload,
// only top-level scalar columns are bound.
func (e *Evaluator) BindQuery(cols map[string]interface{}) {
	for k, v := range cols {
		switch v.(type) {
		case string, int, int64, float64, bool, nil:
			e.inner.SetVariable("query."+k, ToValue(v))
		}
	}
}

// EvalGuard parses and evaluates a boolean guard expression.
// Returns true if the guard passes, false if it fails, and an error if
// the expression cannot be parsed or evaluated.
//
// vars and payload are bound before evaluation; they do not persist between
// calls. Create a new Evaluator per walk step.
func EvalGuard(e *Evaluator, expr string, vars map[string]interface{}, payload map[string]interface{}) (bool, error) {
	return EvalGuardWithQuery(e, expr, vars, payload, nil)
}

// EvalGuardWithQuery evaluates a guard with vars, payload, and transition
// pre-query result columns (bound under the "query." prefix) all in scope.
func EvalGuardWithQuery(e *Evaluator, expr string, vars, payload, query map[string]interface{}) (bool, error) {
	e.BindVars(vars)
	e.BindPayload(payload)
	if query != nil {
		e.BindQuery(query)
	}

	parsed, err := parseExpression(expr)
	if err != nil {
		return false, fmt.Errorf("fsm/eval: guard parse error in %q: %w", expr, err)
	}
	val, err := e.inner.Evaluate(parsed)
	if err != nil {
		return false, fmt.Errorf("fsm/eval: guard eval error in %q: %w", expr, err)
	}
	return valueToBool(val), nil
}

// EvalSet parses and evaluates a set-clause expression and returns the
// result as a Go value suitable for storing back into machine variables.
//
// vars is bound before evaluation. The expression should be an arithmetic
// or string expression (e.g. "@retries + 1", "UPPER(@status)").
func EvalSet(e *Evaluator, expr string, vars map[string]interface{}) (interface{}, error) {
	e.BindVars(vars)

	parsed, err := parseExpression(expr)
	if err != nil {
		return nil, fmt.Errorf("fsm/eval: set-clause parse error in %q: %w", expr, err)
	}

	val, err := e.inner.Evaluate(parsed)
	if err != nil {
		return nil, fmt.Errorf("fsm/eval: set-clause eval error in %q: %w", expr, err)
	}

	return valueToGo(val), nil
}

// ParseGuard parses a guard expression string and returns the AST node.
// Use this at FSM definition creation time to validate guard syntax without
// evaluating. Returns a non-nil error if the expression is syntactically
// invalid.
func ParseGuard(expr string) (ast.Expression, error) {
	return parseExpression(expr)
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// parseExpression parses a T-SQL expression string into an AST node using
// xolu's tsqlparser v0.6.0.
func parseExpression(expr string) (ast.Expression, error) {
	// Wrap in a minimal SELECT so the parser produces a full statement,
	// then extract the first column expression.
	src := "SELECT " + expr
	l := lexer.New(src)
	p := parser.New(l)
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	if len(prog.Statements) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	sel, ok := prog.Statements[0].(*ast.SelectStatement)
	if !ok || len(sel.Columns) == 0 {
		return nil, fmt.Errorf("expression did not produce a SELECT column")
	}
	col := sel.Columns[0]
	// T-SQL treats `@var = expr` in a SELECT column as variable assignment,
	// producing Variable=@var and Expression=expr (the comparison is lost). In
	// an FSM guard/set context `@var = expr` is unambiguously an equality
	// comparison — assignment has no meaning in a boolean guard — so
	// reconstruct it as `@var = expr`. (Inequalities like `@var > expr` are not
	// affected; only `=` is parsed as assignment.)
	//
	// The assignment greedily swallows everything to the right of `=`, so
	// `@n = 5 OR @a = 1` parses as Variable=@n, Expression=(5 OR @a = 1). The
	// `=` binds tighter than AND/OR, so the equality must apply only to the
	// left spine: `(@n = 5) OR (@a = 1)`. reattachVarEquality walks down the
	// AND/OR/NOT spine of the RHS, grafts `@var = <leftmost operand>` at the
	// bottom, and returns the rebalanced tree.
	if col.Variable != nil {
		return reattachVarEquality(col.Variable, col.Expression), nil
	}
	return col.Expression, nil
}

// reattachVarEquality rebuilds `@var = rhs` so the equality binds only to the
// high-precedence left spine of rhs, with AND/OR/NOT (which bind looser than
// `=`) lifted above the equality. For a non-boolean rhs it is simply
// `@var = rhs`.
func reattachVarEquality(v *ast.Variable, rhs ast.Expression) ast.Expression {
	switch ex := rhs.(type) {
	case *ast.InfixExpression:
		op := strings.ToUpper(ex.Operator)
		if op == "AND" || op == "OR" {
			// `@var = (L <op> R)` actually means `(@var = L) <op> R`.
			ex.Left = reattachVarEquality(v, ex.Left)
			return ex
		}
	case *ast.PrefixExpression:
		// `@var = (NOT X)` means `@var = ...`? NOT binds looser than `=`, so
		// `@var = NOT X` is not valid as a comparison RHS; treat NOT as lifted:
		// this shape does not arise from a well-formed guard, but handle it
		// defensively by leaving NOT on top.
		if strings.ToUpper(ex.Operator) == "NOT" {
			ex.Right = reattachVarEquality(v, ex.Right)
			return ex
		}
	}
	// Base case: rhs is the right operand of the equality.
	return &ast.InfixExpression{
		Token:    token.Token{Type: token.EQ, Literal: "="},
		Left:     v,
		Operator: "=",
		Right:    rhs,
	}
}

// valueToBool coerces a Value to a Go bool for guard evaluation.
func valueToBool(v Value) bool {
	if v.IsNull {
		return false
	}
	switch v.Type {
	case TypeBit:
		return v.AsBool()
	case TypeInt, TypeBigInt, TypeSmallInt, TypeTinyInt:
		return v.AsInt() != 0
	case TypeVarChar, TypeNVarChar, TypeChar, TypeNChar:
		upper := strings.ToUpper(strings.TrimSpace(v.AsString()))
		return upper == "TRUE" || upper == "1" || upper == "YES"
	default:
		return v.AsInt() != 0
	}
}

// valueToGo converts a Value back to a Go native value for storage.
func valueToGo(v Value) interface{} {
	if v.IsNull {
		return nil
	}
	switch v.Type {
	case TypeBit:
		return v.AsBool()
	case TypeTinyInt, TypeSmallInt, TypeInt, TypeBigInt:
		return v.AsInt()
	case TypeFloat, TypeReal:
		return v.AsFloat()
	case TypeVarChar, TypeChar, TypeNVarChar, TypeNChar, TypeText, TypeNText:
		return v.AsString()
	case TypeDecimal, TypeNumeric, TypeMoney, TypeSmallMoney:
		return v.AsFloat()
	default:
		return v.AsString()
	}
}
