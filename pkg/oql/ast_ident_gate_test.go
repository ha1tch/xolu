// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"testing"

	"github.com/ha1tch/tsqlparser"
	"github.com/ha1tch/tsqlparser/ast"
)

// parseFirstStmt parses a single-statement program and returns the statement,
// reporting parsed=false if the parser itself rejected the input (also a safe
// outcome — the gate only needs to catch what the parser accepts).
func parseFirstStmt(t *testing.T, sql string) (ast.Node, bool) {
	t.Helper()
	prog, errs := tsqlparser.Parse(sql)
	if len(errs) > 0 || prog == nil || len(prog.Statements) == 0 {
		return nil, false
	}
	return prog.Statements[0], true
}

func TestASTGate_RejectsSmuggledIdentifiers(t *testing.T) {
	// Each query smuggles a delimited identifier carrying SQL metacharacters into
	// a different position.
	smuggled := []string{
		// SELECT/JOIN alias (D-012 shape)
		"SELECT a.title AS [c) UNION SELECT data FROM t0000_nodes--] FROM post AS a INNER JOIN author AS b ON a.author_id = b.id",
		// aggregate ORDER BY term (D-013 shape)
		"SELECT COUNT(*) AS c, status FROM post GROUP BY status ORDER BY [x) UNION SELECT data FROM t0000_nodes--] DESC",
		// WHERE field name (D-005 shape, bracketed)
		"SELECT a.title FROM post AS a WHERE a.[x' OR '1'='1] = 1",
		// double-quoted identifier carrying a quote breakout
		`SELECT a.title AS "x"" , evil" FROM post AS a`,
		// bracketed table name
		"SELECT x FROM [post; DROP TABLE t0000_nodes--]",
	}

	for _, q := range smuggled {
		stmt, ok := parseFirstStmt(t, q)
		if !ok {
			continue // parser rejected — safe
		}
		if err := checkASTForSmuggledIdentifiers(stmt); err == nil {
			t.Errorf("gate FAILED to reject smuggled identifier in: %s", q)
		}
	}
}

func TestASTGate_AllowsLegitimateQueries(t *testing.T) {
	// All of these use only bare identifiers and must pass the gate cleanly.
	legit := []string{
		"SELECT a.title AS employee, b.name AS team FROM post AS a INNER JOIN author AS b ON a.author_id = b.id",
		"SELECT COUNT(*) AS c, status FROM post GROUP BY status ORDER BY SUM(amount) DESC",
		"SELECT region, SUM(amount) FROM items GROUP BY region ORDER BY region ASC",
		"SELECT title, author_id FROM post WHERE author_id = 5",
		"SELECT p.name, p.age FROM person AS p ORDER BY p.age DESC",
		"SELECT name_2, field_3, col$x, tag#1 FROM items",
	}

	for _, q := range legit {
		stmt, ok := parseFirstStmt(t, q)
		if !ok {
			t.Errorf("legitimate query failed to parse: %s", q)
			continue
		}
		if err := checkASTForSmuggledIdentifiers(stmt); err != nil {
			t.Errorf("gate wrongly rejected legitimate query %q: %v", q, err)
		}
	}
}
