// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"testing"

	"github.com/ha1tch/tsqlparser"
	"github.com/ha1tch/tsqlparser/ast"
)

// TestAggregator_HavingExprAliases is the XOT186 follow-up fix, at
// the unit level: evalExpr's own FunctionCall case previously only
// ever resolved a HAVING expression like "SUM(val) > 50" against a
// row keyed by the raw expression string ("SUM(val)") or by a
// looser "row key contains the function name" heuristic. Neither
// matches a row shaped the way the SQL aggregate push-down path's
// own result rows actually are -- keyed only by the SELECT list's
// own declared alias ("total"), never also by the raw expression
// string. This test constructs rows in exactly that shape directly
// (alias-only keys, no raw expression key at all), matching the real
// push-down path rather than the Go-path Aggregate's own output
// (which happens to carry both keys and would mask this bug).
func TestAggregator_HavingExprAliases(t *testing.T) {
	program, errs := tsqlparser.Parse(`SELECT cat, SUM(val) AS total FROM entity GROUP BY cat HAVING SUM(val) > 50`)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	stmt := program.Statements[0].(*ast.SelectStatement)

	// Alias-only rows, matching the SQL push-down path's own real
	// shape -- no "SUM(val)" key present at all, only "total".
	rows := []map[string]interface{}{
		{"cat": "x", "total": float64(30)},
		{"cat": "y", "total": float64(100)},
	}

	a := NewAggregator()
	a.SetExprAliases(stmt.Columns)

	var kept []string
	for _, row := range rows {
		if a.EvalCondition(row, stmt.Having) {
			kept = append(kept, row["cat"].(string))
		}
	}
	if len(kept) != 1 || kept[0] != "y" {
		t.Fatalf("want exactly [y] (total 100 > 50, x's own total 30 excluded), got %v", kept)
	}
}

func TestAggregator_HavingExprAliases_WithoutConfiguration(t *testing.T) {
	// Confirms the bug's own exact original shape: without calling
	// SetExprAliases at all (the pre-fix behaviour, since the field
	// defaults to nil), the lookup fails and every row is silently
	// excluded -- not an error, a filter that looks like it ran
	// successfully but discarded everything.
	program, errs := tsqlparser.Parse(`SELECT cat, SUM(val) AS total FROM entity GROUP BY cat HAVING SUM(val) > 50`)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	stmt := program.Statements[0].(*ast.SelectStatement)

	rows := []map[string]interface{}{
		{"cat": "y", "total": float64(100)},
	}

	a := NewAggregator() // SetExprAliases deliberately not called
	if a.EvalCondition(rows[0], stmt.Having) {
		t.Error("without SetExprAliases configured, the row should not match (this documents the original bug's own exact failure mode, not a desired behaviour)")
	}
}

// TestAggregator_HavingExprAliases_MultipleAggregates confirms the
// mapping is built per-column, not just for the first aggregate in
// the SELECT list -- a query with two different aggregates, HAVING
// referencing the second one specifically.
func TestAggregator_HavingExprAliases_MultipleAggregates(t *testing.T) {
	program, errs := tsqlparser.Parse(
		`SELECT cat, SUM(val) AS total, COUNT(*) AS cnt FROM entity GROUP BY cat HAVING COUNT(*) >= 2`)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	stmt := program.Statements[0].(*ast.SelectStatement)

	rows := []map[string]interface{}{
		{"cat": "x", "total": float64(30), "cnt": float64(2)},
		{"cat": "y", "total": float64(100), "cnt": float64(1)},
	}

	a := NewAggregator()
	a.SetExprAliases(stmt.Columns)

	var kept []string
	for _, row := range rows {
		if a.EvalCondition(row, stmt.Having) {
			kept = append(kept, row["cat"].(string))
		}
	}
	if len(kept) != 1 || kept[0] != "x" {
		t.Fatalf("want exactly [x] (cnt 2 >= 2, y's own cnt 1 excluded), got %v", kept)
	}
}
