// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

import (
	"context"
	"strings"
	"testing"
)

// TestPushDown_AliasInjection is a red-first regression test for SQL injection
// via the RETURN projection alias in the Sulpher push-down path.
//
// buildSelectClauseAST interpolates the user-supplied RETURN alias verbatim into
//
//	<col> AS "<alias>"
//
// with no validation. openCypher backtick-quoted identifiers (`...`) accept any
// character except a backtick — including the double-quote that delimits the SQL
// alias — so a query such as
//
//	RETURN p.name AS `x" , (SELECT ...) AS leaked --`
//
// breaks out of the quoted alias and rewrites the single SELECT statement. Node
// (table) aliases are guarded by isSimpleIdent in generateGraphSQLAST; the
// projection alias was not. The generated SQL reaches AggregateQuery and is
// executed.
//
// This is the D-005 class (identifier interpolated into push-down SQL) on the
// Cypher surface, which the original audit did not cover.
func TestPushDown_AliasInjection(t *testing.T) {
	f := setupPushDownFixture(t)

	// Backtick-quoted alias that breaks out of AS "<alias>" and injects an extra
	// projected column reading the dept budget — a value the person-only RETURN
	// has no business exposing. If the alias is interpolated raw, the SELECT
	// becomes:
	//   ... p_name AS "x", (SELECT budget FROM t0000_ndata_dept LIMIT 1) AS "leaked" --"
	// and the "leaked" key appears in the result rows.
	const payload = "x\", (SELECT budget FROM t0000_ndata_dept LIMIT 1) AS \"leaked\" --"
	query := "MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name AS `" + payload + "`"

	// Primary defence (post-gate): the smuggled backtick alias is rejected at
	// parse time by the AST identifier gate, before the executor runs.
	p := NewParser()
	q, hint, parseErr := p.Parse(query)
	if parseErr != nil {
		// Rejected at the parser boundary — the safe, preferred outcome.
		return
	}

	// Fallback: if a future parser change lets it through, the executor / per-sink
	// guard must still prevent the breakout.
	result, err := f.executor.Execute(context.Background(), q, hint)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "destination arguments") || strings.Contains(msg, "scan") {
			t.Fatalf("SQL INJECTION: alias broke out of quoting and altered the SELECT "+
				"column set (scan mismatch): %v", err)
		}
		return
	}

	for _, row := range result.Data {
		if _, leaked := row["leaked"]; leaked {
			t.Fatalf("SQL INJECTION: RETURN alias broke out of quoting and exfiltrated "+
				"a column; row=%v", row)
		}
	}
}
