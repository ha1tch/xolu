// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

// prequery.go — force a row-count bound onto transition pre-queries.
//
// A transition pre-query is run before a walk and only its first row is used.
// To avoid materializing a large, wasteful result set, the engine forces TOP 1
// onto the query before execution, overriding any TOP the author wrote. The
// author still controls *which* row via ORDER BY; TOP 1 only bounds the cost.

import (
	"fmt"

	"github.com/ha1tch/tsqlparser/ast"
	"github.com/ha1tch/tsqlparser/lexer"
	"github.com/ha1tch/tsqlparser/parser"
	"github.com/ha1tch/tsqlparser/token"
)

// ForceTop1 parses a SELECT query, forces TOP 1 onto it (replacing any existing
// TOP), and returns the rewritten query text. It returns an error if the query
// does not parse or is not a single SELECT statement. ORDER BY, WHERE, and all
// other clauses are preserved; only the row-count bound is imposed.
func ForceTop1(query string) (string, error) {
	l := lexer.New(query)
	p := parser.New(l)
	prog := p.ParseProgram()
	if errs := p.Errors(); len(errs) > 0 {
		return "", fmt.Errorf("pre-query parse error: %s", errs[0])
	}
	if len(prog.Statements) != 1 {
		return "", fmt.Errorf("pre-query must be a single statement, got %d", len(prog.Statements))
	}
	sel, ok := prog.Statements[0].(*ast.SelectStatement)
	if !ok {
		return "", fmt.Errorf("pre-query must be a SELECT statement")
	}
	sel.Top = &ast.TopClause{
		Count: &ast.IntegerLiteral{
			Token: token.Token{Type: token.INT, Literal: "1"},
			Value: 1,
		},
	}
	return sel.String(), nil
}
