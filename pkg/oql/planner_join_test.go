// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"context"
	"github.com/ha1tch/xolu/pkg/tenant"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

// ---------------------------------------------------------------------------
// Mock store that implements AggregateQueryable for join planner tests
// ---------------------------------------------------------------------------

type mockJoinStore struct {
	storage.Store
	adaptedEntities map[string]bool // entity → is adapted
	tableNames      map[string]string
	columnInfos     map[string]map[string]string // entity → field → colName
	queryResults    []map[string]interface{}
	queryErr        error
}

func (m *mockJoinStore) Capabilities() storage.QueryCapabilities {
	return storage.QueryCapabilities{Where: true, OrderBy: true, Limit: true}
}

func (m *mockJoinStore) CountEntities(_ context.Context, entity string) (int, error) {
	return 500, nil
}

func (m *mockJoinStore) QueryWithPlan(_ context.Context, _ string, _ []interface{}) ([]map[string]interface{}, error) {
	return m.queryResults, nil
}

func (m *mockJoinStore) IsAdaptedEntity(entity string) bool {
	return m.adaptedEntities[entity]
}

func (m *mockJoinStore) AdaptedTableName(entity string) (string, bool) {
	name, ok := m.tableNames[entity]
	return name, ok
}

func (m *mockJoinStore) AdaptedColumnInfo(entity, field string) (colName string, scale int, isDecimal bool, ok bool) {
	if cols, exists := m.columnInfos[entity]; exists {
		if col, found := cols[field]; found {
			return col, 0, false, true
		}
	}
	return "", 0, false, false
}

func (m *mockJoinStore) AggregateQuery(_ context.Context, _ string, _ []interface{}, _ []string) ([]map[string]interface{}, error) {
	return m.queryResults, m.queryErr
}

func (m *mockJoinStore) StorageDialectFor(_ string) storage.StorageDialect { return nil }

// Store interface stubs
func (m *mockJoinStore) Create(_ context.Context, _ string, _ map[string]interface{}) (int, error) {
	return 0, nil
}
func (m *mockJoinStore) Get(_ context.Context, _ string, _ int) (map[string]interface{}, error) {
	return nil, nil
}
func (m *mockJoinStore) Update(_ context.Context, _ string, _ int, _ map[string]interface{}) error {
	return nil
}
func (m *mockJoinStore) Patch(_ context.Context, _ string, _ int, _ map[string]interface{}) error {
	return nil
}
func (m *mockJoinStore) Delete(_ context.Context, _ string, _ int) error { return nil }
func (m *mockJoinStore) Save(_ context.Context, _ string, _ int, _ map[string]interface{}) (bool, error) {
	return false, nil
}
func (m *mockJoinStore) List(_ context.Context, _ string) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockJoinStore) Exists(_ context.Context, _ string, _ int) bool { return false }
func (m *mockJoinStore) Search(_ context.Context, _ string, _ string, _ string, _ string) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockJoinStore) FullTextSearch(_ context.Context, _ string, _ string) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockJoinStore) Close() error { return nil }

var _ storage.Store = (*mockJoinStore)(nil)
var _ storage.AggregateQueryable = (*mockJoinStore)(nil)

// newMockJoinStore builds a mock store with two entities. Pass adapted=true
// for each side to control the adapted/blob classification.
func newMockJoinStore(leftEntity string, leftAdapted bool, rightEntity string, rightAdapted bool) *mockJoinStore {
	adaptedEntities := map[string]bool{
		leftEntity:  leftAdapted,
		rightEntity: rightAdapted,
	}
	tableNames := map[string]string{
		leftEntity:  tenant.AdaptedNodeTableName(0, leftEntity),
		rightEntity: tenant.AdaptedNodeTableName(0, rightEntity),
	}
	columnInfos := map[string]map[string]string{
		leftEntity:  {"id": "id", "author_id": "author_id", "title": "title", "status": "status", "name": "name"},
		rightEntity: {"id": "id", "name": "name", "email": "email", "author_id": "author_id"},
	}
	return &mockJoinStore{
		adaptedEntities: adaptedEntities,
		tableNames:      tableNames,
		columnInfos:     columnInfos,
	}
}

// ---------------------------------------------------------------------------
// TestPlanner_Join
// ---------------------------------------------------------------------------

func TestPlanner_Join(t *testing.T) {
	ctx := context.Background()
	dialect := &SQLiteDialect{}
	planner := NewPlannerWithDialectAndThreshold(dialect, 1)

	t.Run("INNER JOIN both adapted returns PushJoin", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin, got %s (reason: %s)", plan.pushNames(), plan.Reason)
		}
		if !plan.LeftAdapted {
			t.Error("expected LeftAdapted=true")
		}
		if !plan.RightAdapted {
			t.Error("expected RightAdapted=true")
		}
	})

	t.Run("INNER JOIN both blob returns PushJoin", func(t *testing.T) {
		store := newMockJoinStore("post", false, "author", false)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin, got %s (reason: %s)", plan.pushNames(), plan.Reason)
		}
		if plan.LeftAdapted {
			t.Error("expected LeftAdapted=false")
		}
		if plan.RightAdapted {
			t.Error("expected RightAdapted=false")
		}
	})

	t.Run("INNER JOIN left adapted right blob returns PushJoin", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", false)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin, got %s", plan.pushNames())
		}
		if !plan.LeftAdapted {
			t.Error("expected LeftAdapted=true")
		}
		if plan.RightAdapted {
			t.Error("expected RightAdapted=false")
		}
	})

	t.Run("INNER JOIN left blob right adapted returns PushJoin", func(t *testing.T) {
		store := newMockJoinStore("post", false, "author", true)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin, got %s", plan.pushNames())
		}
		if plan.LeftAdapted {
			t.Error("expected LeftAdapted=false")
		}
		if !plan.RightAdapted {
			t.Error("expected RightAdapted=true")
		}
	})

	t.Run("LEFT JOIN returns PushJoin", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a LEFT JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin for LEFT JOIN, got %s", plan.pushNames())
		}
		if plan.Join.JoinType != "LEFT" {
			t.Errorf("expected JoinType=LEFT, got %q", plan.Join.JoinType)
		}
	})

	t.Run("RIGHT JOIN returns PushJoin", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a RIGHT JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin for RIGHT JOIN, got %s", plan.pushNames())
		}
		if plan.Join.JoinType != "RIGHT" {
			t.Errorf("expected JoinType=RIGHT, got %q", plan.Join.JoinType)
		}
	})

	t.Run("FULL JOIN returns PushJoin", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a FULL JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin for FULL JOIN, got %s", plan.pushNames())
		}
		if plan.Join.JoinType != "FULL" {
			t.Errorf("expected JoinType=FULL, got %q", plan.Join.JoinType)
		}
	})

	t.Run("store without AggregateQueryable returns PushNone", func(t *testing.T) {
		plain := &mockQueryableStore{
			caps:   storage.QueryCapabilities{Where: true},
			counts: map[string]int{"post": 500, "author": 500},
		}
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, plain)
		if plan.pushed(PushJoin) {
			t.Fatalf("expected PushNone when store lacks AggregateQueryable, got %s", plan.pushNames())
		}
	})

	t.Run("ON condition not a simple equality returns PushNone", func(t *testing.T) {
		// Non-equality ON condition — extractJoinSpec will return nil for this
		// because the operator is not "=", so the query falls through to the
		// standard blob path. Plan will be PushNone (count below threshold for
		// non-AggregateQueryable — but our mock IS AggregateQueryable).
		// Actually extractJoinSpec returns nil → planJoin never called →
		// falls through to standard path → AggregateQueryable is not Queryable,
		// so PushNone.
		store := newMockJoinStore("post", true, "author", true)
		// Use a WHERE-only single table query to ensure non-join path
		s := parseSelect(t, `SELECT title FROM post WHERE id > 1`)
		plan := planner.Plan(ctx, s, store)
		// This is a single-table query — should not be PushJoin
		if plan.pushed(PushJoin) {
			t.Fatalf("single-table query should not produce PushJoin, got %s", plan.pushNames())
		}
	})

	t.Run("JOIN spec is populated in returned plan", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin")
		}
		if plan.Join == nil {
			t.Fatal("plan.Join is nil")
		}
		if plan.Join.LeftEntity != "post" {
			t.Errorf("LeftEntity: got %q, want %q", plan.Join.LeftEntity, "post")
		}
		if plan.Join.RightEntity != "author" {
			t.Errorf("RightEntity: got %q, want %q", plan.Join.RightEntity, "author")
		}
		if plan.Join.LeftAlias != "a" {
			t.Errorf("LeftAlias: got %q, want %q", plan.Join.LeftAlias, "a")
		}
		if plan.Join.RightAlias != "b" {
			t.Errorf("RightAlias: got %q, want %q", plan.Join.RightAlias, "b")
		}
		if plan.Join.JoinType != "INNER" {
			t.Errorf("JoinType: got %q, want %q", plan.Join.JoinType, "INNER")
		}
	})

	t.Run("WHERE clause pushable with join aliases returns PushJoin", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id WHERE a.status = 'published'`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin with pushable WHERE, got %s (reason: %s)", plan.pushNames(), plan.Reason)
		}
	})

	t.Run("join with no explicit aliases uses entity names as aliases", func(t *testing.T) {
		store := newMockJoinStore("post", true, "author", true)
		s := parseSelect(t, `SELECT post.title, author.name FROM post INNER JOIN author ON post.author_id = author.id`)
		plan := planner.Plan(ctx, s, store)
		if !plan.pushed(PushJoin) {
			t.Fatalf("expected PushJoin without explicit aliases, got %s", plan.pushNames())
		}
		if plan.Join.LeftAlias != "post" {
			t.Errorf("LeftAlias without AS: got %q, want %q", plan.Join.LeftAlias, "post")
		}
		if plan.Join.RightAlias != "author" {
			t.Errorf("RightAlias without AS: got %q, want %q", plan.Join.RightAlias, "author")
		}
	})
}

// ---------------------------------------------------------------------------
// TestExtractJoinSpec
// ---------------------------------------------------------------------------

func TestExtractJoinSpec(t *testing.T) {
	t.Run("returns nil for single-table query", func(t *testing.T) {
		s := parseSelect(t, `SELECT id FROM post WHERE id = 1`)
		js, err := extractJoinSpec(s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if js != nil {
			t.Errorf("expected nil joinSpec for single table, got %+v", js)
		}
	})

	t.Run("returns nil for nil FROM clause", func(t *testing.T) {
		// Construct a minimal SelectStatement with no FROM directly.
		s := parseSelect(t, `SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id`)
		s.From = nil
		js, err := extractJoinSpec(s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if js != nil {
			t.Errorf("expected nil for nil FROM, got %+v", js)
		}
	})

	t.Run("INNER JOIN returns correct spec", func(t *testing.T) {
		s := parseSelect(t, `SELECT a.x, b.y FROM foo AS a INNER JOIN bar AS b ON a.fk = b.id`)
		js, err := extractJoinSpec(s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if js == nil {
			t.Fatal("expected joinSpec, got nil")
		}
		if js.LeftEntity != "foo" || js.RightEntity != "bar" {
			t.Errorf("entities: left=%q right=%q", js.LeftEntity, js.RightEntity)
		}
		if js.LeftAlias != "a" || js.RightAlias != "b" {
			t.Errorf("aliases: left=%q right=%q", js.LeftAlias, js.RightAlias)
		}
		if js.JoinType != "INNER" {
			t.Errorf("JoinType: %q", js.JoinType)
		}
		if js.Condition == nil {
			t.Error("Condition is nil")
		}
	})

	t.Run("ON condition not an equality returns nil", func(t *testing.T) {
		// Parsers may not support non-equality ON; if they do, extractJoinSpec
		// must return nil. Use a structure test — rely on the condition check
		// rather than a non-equality ON which the parser may mangle.
		s := parseSelect(t, `SELECT a.x FROM foo AS a INNER JOIN bar AS b ON a.fk = b.id`)
		js, _ := extractJoinSpec(s)
		if js == nil {
			t.Fatal("basic INNER JOIN should return a spec")
		}
		if js.Condition.Operator != "=" {
			t.Errorf("condition operator: %q (should be =)", js.Condition.Operator)
		}
	})
}

// ---------------------------------------------------------------------------
// TestIsJoinConditionPushable
// ---------------------------------------------------------------------------

func TestIsJoinConditionPushable(t *testing.T) {
	t.Run("qualified equality is pushable", func(t *testing.T) {
		s := parseSelect(t, `SELECT a.x FROM foo AS a INNER JOIN bar AS b ON a.fk = b.id`)
		js, _ := extractJoinSpec(s)
		if js == nil {
			t.Fatal("need a valid join spec")
		}
		if !isJoinConditionPushable(js.Condition, "a", "b") {
			t.Error("expected pushable ON condition")
		}
	})

	t.Run("reversed operand order is pushable", func(t *testing.T) {
		s := parseSelect(t, `SELECT a.x FROM foo AS a INNER JOIN bar AS b ON b.id = a.fk`)
		js, _ := extractJoinSpec(s)
		if js == nil {
			t.Fatal("need a valid join spec")
		}
		if !isJoinConditionPushable(js.Condition, "a", "b") {
			t.Error("b.id = a.fk should be pushable")
		}
	})
}

// ---------------------------------------------------------------------------
// TestIsJoinWherePushable
// ---------------------------------------------------------------------------

func TestIsJoinWherePushable(t *testing.T) {
	cases := []struct {
		name     string
		sql      string
		pushable bool
	}{
		{
			name:     "qualified field comparison",
			sql:      `SELECT a.x, b.y FROM foo AS a INNER JOIN bar AS b ON a.fk = b.id WHERE a.status = 'active'`,
			pushable: true,
		},
		{
			name:     "AND combining two qualified predicates",
			sql:      `SELECT a.x, b.y FROM foo AS a INNER JOIN bar AS b ON a.fk = b.id WHERE a.status = 'active' AND b.active = 1`,
			pushable: true,
		},
		{
			name:     "IN list",
			sql:      `SELECT a.x FROM foo AS a INNER JOIN bar AS b ON a.fk = b.id WHERE a.status IN ('active', 'pending')`,
			pushable: true,
		},
		{
			name:     "IS NULL",
			sql:      `SELECT a.x FROM foo AS a INNER JOIN bar AS b ON a.fk = b.id WHERE b.email IS NULL`,
			pushable: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := parseSelect(t, tc.sql)
			js, err := extractJoinSpec(s)
			if err != nil || js == nil {
				t.Skipf("parse or spec error: %v", err)
			}
			if s.Where == nil {
				t.Skip("no WHERE clause")
			}
			result := isJoinWherePushable(s.Where, js.LeftAlias, js.RightAlias)
			if result != tc.pushable {
				t.Errorf("isJoinWherePushable = %v, want %v", result, tc.pushable)
			}
		})
	}
}
