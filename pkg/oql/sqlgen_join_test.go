// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildJoinPlan constructs a QueryPlan with PushJoin set, using the provided
// join spec and adapted flags.
func buildJoinPlan(js *joinSpec, leftAdapted, rightAdapted bool) QueryPlan {
	return QueryPlan{
		Push:         []PushDecision{PushJoin},
		Join:         js,
		LeftAdapted:  leftAdapted,
		RightAdapted: rightAdapted,
	}
}

// ---------------------------------------------------------------------------
// TestGenerateJoinSQL
// ---------------------------------------------------------------------------

func TestGenerateJoinSQL(t *testing.T) {
	d := &SQLiteDialect{}

	t.Run("both adapted: native columns on both sides", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, true, true)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}

		assertContains(t, result.SQL, "olu_post")
		assertContains(t, result.SQL, "olu_author")
		assertContains(t, result.SQL, "INNER JOIN")
		// No json_extract for fully adapted
		assertNotContains(t, result.SQL, "json_extract")
		// Should have native column references
		assertContains(t, result.SQL, "a.title")
		assertContains(t, result.SQL, "b.name")
		// Aliases must be populated
		if len(result.Aliases) != 2 {
			t.Errorf("expected 2 aliases, got %d: %v", len(result.Aliases), result.Aliases)
		}
	})

	t.Run("both blob: json_extract throughout, entities table aliased twice", func(t *testing.T) {
		store := newMockJoinStore("post", false, "author", false)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, false, false)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}

		assertContains(t, result.SQL, "entities")
		assertContains(t, result.SQL, "json_extract")
		// entity_type filter for both sides
		assertContains(t, result.SQL, "entity_type")
		// Both entity type args
		assertArgContains(t, result.Args, "post")
		assertArgContains(t, result.Args, "author")
		// No adapted table names
		assertNotContains(t, result.SQL, "olu_post")
		assertNotContains(t, result.SQL, "olu_author")
	})

	t.Run("left adapted right blob: mixed extraction", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", false)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, true, false)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}

		// Left side uses adapted table, no json_extract for left fields
		assertContains(t, result.SQL, "olu_post")
		// Right side uses entities table + json_extract
		assertContains(t, result.SQL, "entities")
		assertContains(t, result.SQL, "json_extract")
		// entity_type arg for right side only
		assertArgContains(t, result.Args, "author")
		// No entity_type arg for left (adapted)
	})

	t.Run("right adapted left blob: mirror case", func(t *testing.T) {
		store := newMockJoinStore("post", false, "author", true)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, false, true)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}

		assertContains(t, result.SQL, "olu_author")
		assertContains(t, result.SQL, "entities")
		assertContains(t, result.SQL, "json_extract")
		assertArgContains(t, result.Args, "post")
	})

	t.Run("WHERE clause emitted correctly", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id WHERE a.status = 'published'`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, true, true)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}

		assertContains(t, result.SQL, "WHERE")
		assertContains(t, result.SQL, "a.status")
		assertArgContains(t, result.Args, "published")
	})

	t.Run("blob WHERE uses json_extract", func(t *testing.T) {
		store := newMockJoinStore("post", false, "author", false)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id WHERE a.status = 'published'`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, false, false)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}

		assertContains(t, result.SQL, "WHERE")
		assertContains(t, result.SQL, "json_extract")
		assertArgContains(t, result.Args, "published")
	})

	t.Run("tenant scoping appends tenant_id for both sides", func(t *testing.T) {
		store := newMockJoinStore("post", false, "author", false)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, false, false)

		result, err := GenerateJoinSQL(s, plan, "42", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}

		assertContains(t, result.SQL, "tenant_id")
		// Two tenant_id clauses (one per side)
		count := strings.Count(result.SQL, "tenant_id")
		if count < 2 {
			t.Errorf("expected at least 2 tenant_id clauses, got %d in:\n%s", count, result.SQL)
		}
		assertArgContains(t, result.Args, "42")
	})

	t.Run("no WHERE clause produces clean minimal SQL", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, true, true)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}

		// No WHERE when there are no filters at all
		if strings.Contains(result.SQL, "WHERE") {
			t.Errorf("expected no WHERE clause when no filters, got:\n%s", result.SQL)
		}
		if len(result.Args) != 0 {
			t.Errorf("expected 0 args with no filters, got %d: %v", len(result.Args), result.Args)
		}
	})

	t.Run("FULL JOIN emitted as FULL OUTER JOIN", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		oqlStr := `SELECT a.title, b.name FROM post AS a FULL JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		if js == nil {
			t.Skip("parser does not support FULL JOIN")
		}
		plan := buildJoinPlan(js, true, true)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}
		assertContains(t, result.SQL, "FULL OUTER JOIN")
	})

	t.Run("LEFT JOIN keyword preserved", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		oqlStr := `SELECT a.title, b.name FROM post AS a LEFT JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, true, true)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}
		assertContains(t, result.SQL, "LEFT JOIN")
	})

	t.Run("alias list matches SELECT column count", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		oqlStr := `SELECT a.title, a.status, b.name, b.email FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		js, _ := extractJoinSpec(s)
		plan := buildJoinPlan(js, true, true)

		result, err := GenerateJoinSQL(s, plan, "", store, d)
		if err != nil {
			t.Fatalf("GenerateJoinSQL: %v", err)
		}
		if len(result.Aliases) != 4 {
			t.Errorf("expected 4 aliases for 4 SELECT columns, got %d: %v", len(result.Aliases), result.Aliases)
		}
	})

	t.Run("nil plan.Join returns error", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		oqlStr := `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`
		s := parseSQLGen(t, oqlStr)
		plan := QueryPlan{Push: []PushDecision{PushJoin}} // Join is nil

		_, err := GenerateJoinSQL(s, plan, "", store, d)
		if err == nil {
			t.Error("expected error for nil plan.Join")
		}
	})
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

func assertContains(t *testing.T, sql, substring string) {
	t.Helper()
	if !strings.Contains(sql, substring) {
		t.Errorf("expected SQL to contain %q, got:\n%s", substring, sql)
	}
}

func assertNotContains(t *testing.T, sql, substring string) {
	t.Helper()
	if strings.Contains(sql, substring) {
		t.Errorf("expected SQL to NOT contain %q, got:\n%s", substring, sql)
	}
}

func assertArgContains(t *testing.T, args []interface{}, val interface{}) {
	t.Helper()
	for _, a := range args {
		if a == val {
			return
		}
	}
	t.Errorf("expected args to contain %v, got %v", val, args)
}

// ---------------------------------------------------------------------------
// Regression tests — one per bug found during integration testing
// ---------------------------------------------------------------------------

// Regression: Bug 1 — blob-path field references must use JSONFieldAliased
// (alias qualifies data column inside the expression), not plain JSONField
// with alias prepended. Prior code emitted "a.json_extract(data, '$.x')"
// which is a syntax error; correct form is "json_extract(a.data, '$.x')".
func TestRegression_BlobJoinUsesJSONFieldAliased(t *testing.T) {
	store := newMockJoinStore("tag_links", false, "tags", false)
	s := parseSQLGen(t, `SELECT a.post_id, b.label FROM tag_links AS a INNER JOIN tags AS b ON a.tag_id = b.id`)
	js, _ := extractJoinSpec(s)
	plan := buildJoinPlan(js, false, false)

	result, err := GenerateJoinSQL(s, plan, "", store, &SQLiteDialect{})
	if err != nil {
		t.Fatalf("GenerateJoinSQL: %v", err)
	}

	// Must NOT contain the broken form "alias.json_extract(...)"
	if strings.Contains(result.SQL, "a.json_extract") || strings.Contains(result.SQL, "b.json_extract") {
		t.Errorf("SQL contains alias-prefixed json_extract (Bug 1 regression):\n%s", result.SQL)
	}

	// Must contain the correct form "json_extract(a.data, ...)"
	if !strings.Contains(result.SQL, "json_extract(a.data,") && !strings.Contains(result.SQL, "json_extract(a.data ,") {
		t.Errorf("SQL missing json_extract(a.data, ...) (Bug 1 regression):\n%s", result.SQL)
	}
	if !strings.Contains(result.SQL, "json_extract(b.data,") && !strings.Contains(result.SQL, "json_extract(b.data ,") {
		t.Errorf("SQL missing json_extract(b.data, ...) (Bug 1 regression):\n%s", result.SQL)
	}
}

// Regression: Bug 2 — column aliases must not contain dots. SQLite rejects
// "AS a.title" as a column alias. Without an explicit AS, qualified
// identifiers like "a.title" must produce alias "title", not "a.title".
func TestRegression_JoinColumnAliasNoDots(t *testing.T) {
	store := newMockJoinStore("posts", true, "authors", true)
	s := parseSQLGen(t, `SELECT a.title, b.name FROM posts AS a INNER JOIN authors AS b ON a.author_id = b.id`)
	js, _ := extractJoinSpec(s)
	plan := buildJoinPlan(js, true, true)

	result, err := GenerateJoinSQL(s, plan, "", store, &SQLiteDialect{})
	if err != nil {
		t.Fatalf("GenerateJoinSQL: %v", err)
	}

	// Aliases must be bare field names, no table qualifier
	for _, alias := range result.Aliases {
		if strings.Contains(alias, ".") {
			t.Errorf("alias %q contains a dot — SQLite will reject it as a column alias (Bug 2 regression)", alias)
		}
	}

	// SQL must not contain "AS a.title" or "AS b.name" patterns
	if strings.Contains(result.SQL, "AS a.") || strings.Contains(result.SQL, "AS b.") {
		t.Errorf("SQL contains dotted AS alias (Bug 2 regression):\n%s", result.SQL)
	}
}

// Regression: Bug 3 — system column "id" must be resolvable on adapted
// entities even though it is not declared in the user schema. ON conditions
// like "a.author_id = b.id" must not fail with "field id not found".
func TestRegression_SystemColumnIDResolvedForAdapted(t *testing.T) {
	store := newMockJoinStore("posts", true, "authors", true)
	// ON condition references b.id — a system column, not in the mock schema
	s := parseSQLGen(t, `SELECT a.title, b.name FROM posts AS a INNER JOIN authors AS b ON a.author_id = b.id`)
	js, _ := extractJoinSpec(s)
	plan := buildJoinPlan(js, true, true)

	_, err := GenerateJoinSQL(s, plan, "", store, &SQLiteDialect{})
	if err != nil {
		t.Errorf("GenerateJoinSQL failed for ON a.author_id = b.id (Bug 3 regression): %v", err)
	}
}
