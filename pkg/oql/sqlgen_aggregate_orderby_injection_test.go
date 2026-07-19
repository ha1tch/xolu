// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"
	"testing"
)

// TestAggregateOrderBy_AliasElseBranch_Injection covers SQL injection via the
// ORDER BY handling in GenerateAggregateSQL.
//
// When an ORDER BY field does not resolve to a known adapted column,
// generateAdaptedAggregateSQL falls into an "else" branch that interpolates the
// raw field string directly:
//
//	// Might be an alias -- use it directly
//	orderExprs = append(orderExprs, fieldName+" "+dir)
//
// fieldName is exprToString(ob.Expression) = expr.String(). Because OQL parses
// with tsqlparser, a T-SQL delimited identifier `ORDER BY [x) UNION SELECT ...--]`
// stringifies to its raw inner text with delimiters stripped, so the payload is
// interpolated unquoted into the ORDER BY clause and rewrites the statement.
//
// Expected post-fix outcome: GenerateAggregateSQL rejects an ORDER BY term that
// is neither a known column nor a valid output alias, so no metacharacters reach
// the SQL text.
func TestAggregateOrderBy_AliasElseBranch_Injection(t *testing.T) {
	d := &SQLiteDialect{}
	// "post" is adapted with known columns including "status" and "title".
	store := newMockJoinStore("post", true, "author", true)

	// GROUP BY a known column; ORDER BY a delimited identifier that is NOT a known
	// column, forcing the "use it directly" else branch.
	payload := `x) UNION SELECT data, _version FROM t0000_nodes--`
	oqlStr := "SELECT COUNT(*) AS c, status FROM post " +
		"GROUP BY status ORDER BY [" + payload + "] DESC"

	s := parseSQLGen(t, oqlStr)

	result, err := GenerateAggregateSQL(s, "post", "", store, d)
	if err != nil {
		// Rejection at generation time is the SAFE (post-fix) outcome.
		return
	}

	if strings.Contains(result.SQL, "UNION SELECT") {
		t.Errorf("SQL injection via aggregate ORDER BY else-branch: UNION payload reached SQL:\n%s", result.SQL)
	}
	if strings.Contains(result.SQL, "--") {
		t.Errorf("SQL injection via aggregate ORDER BY else-branch: comment sequence reached SQL:\n%s", result.SQL)
	}
}
