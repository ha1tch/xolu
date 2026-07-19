// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"
	"testing"
)

// TestJoinAlias_BracketedIdentifier_Injection covers the D-005 class on the
// JOIN *alias* surface, which the original D-005 fix did not reach.
//
// The D-005 fix routed JOIN field *names* (SELECT and WHERE/ON) through
// validateFieldName. But generateJoinSelectColumns emits each column as
//
//	<expr> AS <alias>
//
// where <alias> = joinColumnAlias(col) = col.Alias.Value — the user's explicit
// AS alias — interpolated *unquoted* with no validation. Because OQL parses with
// tsqlparser, a T-SQL delimited alias `AS [a) UNION SELECT ...--]` is stored with
// its delimiters stripped, so the raw inner text `a) UNION SELECT ...--` lands
// directly in the bare AS position and rewrites the statement.
//
// Expected post-fix outcome: GenerateJoinSQL rejects the payload at generation
// time, or the alias is safely quoted/escaped so the metacharacters cannot alter
// the statement.
func TestJoinAlias_BracketedIdentifier_Injection(t *testing.T) {
	d := &SQLiteDialect{}
	store := newMockJoinStore("post", false, "author", false)

	// Delimited alias whose inner text closes the column list and unions in a
	// second query. tsqlparser strips the [ ] and stores the raw inner string.
	payload := `c) UNION SELECT data, _version FROM t0000_nodes--`
	oqlStr := "SELECT a.title AS [" + payload + "], b.name " +
		"FROM post AS a INNER JOIN author AS b ON a.author_id = b.id"

	s := parseSQLGen(t, oqlStr)
	js, jerr := extractJoinSpec(s)
	if jerr != nil || js == nil {
		t.Skipf("query did not yield a join spec (parser rejected payload — also acceptable): %v", jerr)
	}
	plan := buildJoinPlan(js, false, false)

	result, err := GenerateJoinSQL(s, plan, "", store, d)
	if err != nil {
		// Rejection at generation time is the SAFE (post-fix) outcome.
		return
	}

	// If generation succeeded, the metacharacters must not survive into SQL in a
	// statement-altering form. A bare `UNION SELECT` or an unescaped `)` from the
	// alias means the injection reached the query text.
	if strings.Contains(result.SQL, "UNION SELECT") {
		t.Errorf("SQL injection via JOIN alias: UNION payload reached SQL text:\n%s", result.SQL)
	}
	// The closing paren from the payload, if present unescaped right after the
	// expression alias, indicates breakout. A safely-quoted alias would contain
	// it inside quotes only.
	if strings.Contains(result.SQL, "--") {
		t.Errorf("SQL injection via JOIN alias: comment sequence reached SQL text:\n%s", result.SQL)
	}
}
