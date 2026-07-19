// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

// eval_seq.go — NEXT VALUE FOR support in FSM set clauses (S8).
//
// The extracted ExpressionEvaluator knows nothing about sequences. Rather
// than fork it to add a *ast.NextValueForExpression case, this file resolves
// NEXT VALUE FOR nodes by AST substitution before evaluation: each
// NextValueForExpression in a parsed set-clause expression is replaced with
// an integer literal carrying the value returned by an injected incrementor,
// then the rewritten expression is handed to the inner evaluator.
//
// The incrementor is supplied by the walk runtime and runs the atomic
// sequence increment on the walk's own transaction, so the increment
// participates in the walk's atomicity guarantee. This handles a bare
// `NEXT VALUE FOR seq` and forms nested in arithmetic (`NEXT VALUE FOR seq
// + 1`). The @SEQ read function and @GEN dispatch are separate concerns
// (OQL session state and S10/S21 respectively) and are not handled here.

import (
	"fmt"
	"strconv"

	"github.com/ha1tch/tsqlparser/ast"
	"github.com/ha1tch/tsqlparser/token"
)

// SeqIncrementor increments the named sequence and returns the new value.
// It is expected to run on the caller's transaction so the increment is
// atomic with the surrounding walk. A non-nil error aborts the set clause.
type SeqIncrementor func(name string) (int64, error)

// SetSeqIncrementor installs the sequence incrementor used by
// EvalSetWithSeq. When nil (the default), a NEXT VALUE FOR in a set clause
// produces an error rather than silently evaluating to nil.
func (e *Evaluator) SetSeqIncrementor(fn SeqIncrementor) {
	e.seqIncrementor = fn
}

// ContainsNextValueFor reports whether a set-clause expression string
// references NEXT VALUE FOR. The walk runtime uses this to decide whether a
// set clause must run inside the sequence-aware path.
func ContainsNextValueFor(expr string) (bool, error) {
	parsed, err := parseExpression(expr)
	if err != nil {
		return false, err
	}
	found := false
	walkExpr(parsed, func(e ast.Expression) {
		if _, ok := e.(*ast.NextValueForExpression); ok {
			found = true
		}
	})
	return found, nil
}

// EvalSetWithSeq evaluates a set-clause expression, resolving any
// NEXT VALUE FOR references via the installed incrementor before evaluation.
// If the expression contains no NEXT VALUE FOR it behaves exactly like
// EvalSet. If it contains NEXT VALUE FOR and no incrementor is installed,
// it returns an error.
func EvalSetWithSeq(e *Evaluator, expr string, vars map[string]interface{}) (interface{}, error) {
	parsed, err := parseExpression(expr)
	if err != nil {
		return nil, fmt.Errorf("fsm/eval: set-clause parse error in %q: %w", expr, err)
	}

	substituted, err := e.substituteNextValueFor(parsed)
	if err != nil {
		return nil, err
	}

	e.BindVars(vars)
	val, err := e.inner.Evaluate(substituted)
	if err != nil {
		return nil, fmt.Errorf("fsm/eval: set-clause eval error in %q: %w", expr, err)
	}
	return valueToGo(val), nil
}

// substituteNextValueFor returns a copy of the expression with every
// NextValueForExpression replaced by an integer literal holding the
// incremented sequence value. Only the node forms that can appear in a set
// clause are traversed: the bare node, infix arithmetic, prefix expressions,
// and parenthesised/grouped expressions.
func (e *Evaluator) substituteNextValueFor(expr ast.Expression) (ast.Expression, error) {
	switch ex := expr.(type) {
	case *ast.NextValueForExpression:
		if e.seqIncrementor == nil {
			return nil, fmt.Errorf("fsm/eval: NEXT VALUE FOR requires a sequence incrementor; none installed")
		}
		name := ex.SequenceName.String()
		val, err := e.seqIncrementor(name)
		if err != nil {
			return nil, fmt.Errorf("fsm/eval: NEXT VALUE FOR %s: %w", name, err)
		}
		return intLiteral(val), nil
	case *ast.InfixExpression:
		left, err := e.substituteNextValueFor(ex.Left)
		if err != nil {
			return nil, err
		}
		right, err := e.substituteNextValueFor(ex.Right)
		if err != nil {
			return nil, err
		}
		ex.Left = left
		ex.Right = right
		return ex, nil
	case *ast.PrefixExpression:
		right, err := e.substituteNextValueFor(ex.Right)
		if err != nil {
			return nil, err
		}
		ex.Right = right
		return ex, nil
	default:
		return expr, nil
	}
}

// walkExpr visits an expression tree, calling fn on each node. Only the node
// forms relevant to set-clause expressions are traversed.
func walkExpr(expr ast.Expression, fn func(ast.Expression)) {
	if expr == nil {
		return
	}
	fn(expr)
	switch ex := expr.(type) {
	case *ast.InfixExpression:
		walkExpr(ex.Left, fn)
		walkExpr(ex.Right, fn)
	case *ast.PrefixExpression:
		walkExpr(ex.Right, fn)
	}
}

// intLiteral builds a synthetic integer-literal AST node.
func intLiteral(v int64) *ast.IntegerLiteral {
	return &ast.IntegerLiteral{
		Token: token.Token{Type: token.INT, Literal: strconv.FormatInt(v, 10)},
		Value: v,
	}
}
