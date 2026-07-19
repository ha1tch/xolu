// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"
	"testing"
)

// These tests cover D-005: SQL injection via OQL JOIN field names. T-SQL
// delimited identifiers ([..] or "..") let arbitrary characters through the
// lexer as the raw Identifier.Value. The single-table generator validates field
// names via g.fieldPath -> validateFieldName; the JOIN generator's blob branch
// (joinFieldRef -> JSONFieldAliasedAs) historically did not, allowing a crafted
// field name to break out of the json_extract path string literal.
//
// Expected end state after the fix: GenerateJoinSQL rejects these payloads at
// generation time (non-nil error), so the dangerous text never reaches SQL.

func TestJoinField_BracketedIdentifier_Injection(t *testing.T) {
	d := &SQLiteDialect{}
	store := newMockJoinStore("post", false, "author", false)

	payload := `x') UNION SELECT data,_version FROM t0000_nodes--`
	oqlStr := "SELECT a.[" + payload + "], b.name " +
		"FROM post AS a INNER JOIN author AS b ON a.author_id = b.id"

	s := parseSQLGen(t, oqlStr)
	js, jerr := extractJoinSpec(s)
	if jerr != nil || js == nil {
		t.Skipf("query did not yield a join spec (parser rejected payload at parse time — also acceptable): %v", jerr)
	}
	plan := buildJoinPlan(js, false, false)

	result, err := GenerateJoinSQL(s, plan, "", store, d)
	if err != nil {
		// Rejection at generation time is the SAFE (post-fix) outcome.
		return
	}
	if strings.Contains(result.SQL, "UNION SELECT") || strings.Contains(result.SQL, "')") {
		t.Errorf("SQL injection on JOIN blob SELECT path: raw payload reached SQL text:\n%s", result.SQL)
	}
}

func TestJoinWhere_BracketedIdentifier_Injection(t *testing.T) {
	d := &SQLiteDialect{}
	store := newMockJoinStore("post", false, "author", false)
	payload := `x' OR '1'='1`
	oqlStr := "SELECT a.title, b.name FROM post AS a INNER JOIN author AS b " +
		"ON a.author_id = b.id WHERE a.[" + payload + "] = 1"
	s := parseSQLGen(t, oqlStr)
	js, jerr := extractJoinSpec(s)
	if jerr != nil || js == nil {
		t.Skipf("no join spec: %v", jerr)
	}
	plan := buildJoinPlan(js, false, false)
	result, err := GenerateJoinSQL(s, plan, "", store, d)
	if err != nil {
		return
	}
	if strings.Contains(result.SQL, "OR '1'='1") {
		t.Errorf("SQL injection on JOIN WHERE path:\n%s", result.SQL)
	}
}

// Control: the single-table path already validates field names. This documents
// the asymmetry and guards against a regression that would weaken the allowlist.
func TestMainPath_BracketedIdentifier_Rejected(t *testing.T) {
	payload := `x') UNION SELECT 1--`
	if err := validateFieldName(payload); err == nil {
		t.Errorf("validateFieldName accepted injection payload %q", payload)
	}
}
