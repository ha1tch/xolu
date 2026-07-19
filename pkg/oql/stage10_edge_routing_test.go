// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql_test

// ---------------------------------------------------------------------------
// Stage 10 — OQL FROM <edge_label> tests
//
// Covers:
//   - SELECT * FROM KNOWS routes to adapted t<X>_edata_KNOWS
//   - SELECT * FROM FOLLOWS routes to blob t<X>_edges
//   - WHERE filter works on adapted edge query
//   - WHERE filter works on blob edge query
//   - IsEdgeLabel returns true for registered labels, false for node types
//   - ListEdges returns correct rows from blob path
//   - FROM unknown label routes to node store (no regression on node queries)
// ---------------------------------------------------------------------------

import (
	"context"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ha1tch/tsqlparser"
	"github.com/ha1tch/xolu/pkg/oql"
	"github.com/ha1tch/xolu/pkg/storage"
)

func setupOQLEdgeStore(t *testing.T) (storage.Store, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "xolu-oql-edge-*.db")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	f.Close()
	dbPath := f.Name()

	store, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type:         "sqlite",
		DBPath:       dbPath,
		GraphEnabled: true,
	})
	if err != nil {
		os.Remove(dbPath)
		t.Fatalf("NewStoreFromConfig: %v", err)
	}
	return store, func() { store.Close(); os.Remove(dbPath) }
}

func oqlExec(t *testing.T, store storage.Store, query string) []map[string]interface{} {
	t.Helper()
	exec := oql.NewExecutor(store, nil)
	program, errs := tsqlparser.Parse(query)
	if len(errs) > 0 {
		t.Fatalf("Parse(%q): %v", query, errs)
	}
	if len(program.Statements) == 0 {
		t.Fatalf("Parse(%q): no statements", query)
	}
	result, err := exec.Execute(context.Background(), program.Statements[0])
	if err != nil {
		t.Fatalf("Execute(%q): %v", query, err)
	}
	return result.Rows
}

// ---------------------------------------------------------------------------

func TestOQL_Stage10_IsEdgeLabel(t *testing.T) {
	store, cleanup := setupOQLEdgeStore(t)
	defer cleanup()
	ctx := context.Background()

	el, ok := store.(storage.EdgeLister)
	if !ok {
		t.Skip("store does not implement EdgeLister")
	}

	// Unknown label — not an edge.
	isEdge, err := el.IsEdgeLabel(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("IsEdgeLabel nonexistent: %v", err)
	}
	if isEdge {
		t.Error("nonexistent label should not be an edge label")
	}

	// Register an adapted edge — must be detected.
	must(t, store.(storage.AdaptedEdgeStore).RegisterAdaptedEdge(ctx, "KNOWS",
		map[string]interface{}{
			"properties": map[string]interface{}{
				"since": map[string]interface{}{"type": "integer"},
			},
		}))
	isEdge, err = el.IsEdgeLabel(ctx, "KNOWS")
	if err != nil || !isEdge {
		t.Errorf("KNOWS should be an edge label after RegisterAdaptedEdge: isEdge=%v err=%v", isEdge, err)
	}

	// Register schema-only (no adapted table) — must also be detected.
	must(t, store.(storage.EdgeSchemaStore).RegisterEdgeSchema(ctx, "FOLLOWS",
		map[string]interface{}{
			"properties": map[string]interface{}{
				"since": map[string]interface{}{"type": "integer"},
			},
		}))
	isEdge, err = el.IsEdgeLabel(ctx, "FOLLOWS")
	if err != nil || !isEdge {
		t.Errorf("FOLLOWS should be an edge label after RegisterEdgeSchema: isEdge=%v err=%v", isEdge, err)
	}
}

func TestOQL_Stage10_AdaptedEdge_SelectStar(t *testing.T) {
	store, cleanup := setupOQLEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	eps := store.(storage.EdgePropertyStore)

	// Register adapted edge table.
	must(t, store.(storage.AdaptedEdgeStore).RegisterAdaptedEdge(ctx, "KNOWS",
		map[string]interface{}{
			"properties": map[string]interface{}{
				"since":  map[string]interface{}{"type": "integer"},
				"weight": map[string]interface{}{"type": "number"},
			},
		}))

	// Write some edges.
	eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS", map[string]interface{}{"since": 2020, "weight": 0.9})
	eps.AddEdgeWithProps(ctx, "user:1", "user:3", "KNOWS", map[string]interface{}{"since": 2022, "weight": 0.5})
	eps.AddEdgeWithProps(ctx, "user:2", "user:3", "KNOWS", map[string]interface{}{"since": 2021, "weight": 0.7})

	rows := oqlExec(t, store, "SELECT * FROM KNOWS")
	if len(rows) != 3 {
		t.Errorf("SELECT * FROM KNOWS (adapted): expected 3 rows, got %d", len(rows))
		for _, r := range rows {
			t.Logf("  row: %v", r)
		}
	}
}

func TestOQL_Stage10_AdaptedEdge_WhereFilter(t *testing.T) {
	store, cleanup := setupOQLEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	eps := store.(storage.EdgePropertyStore)

	must(t, store.(storage.AdaptedEdgeStore).RegisterAdaptedEdge(ctx, "KNOWS",
		map[string]interface{}{
			"properties": map[string]interface{}{
				"since": map[string]interface{}{"type": "integer"},
			},
		}))

	eps.AddEdgeWithProps(ctx, "user:1", "user:2", "KNOWS", map[string]interface{}{"since": 2018})
	eps.AddEdgeWithProps(ctx, "user:1", "user:3", "KNOWS", map[string]interface{}{"since": 2022})
	eps.AddEdgeWithProps(ctx, "user:2", "user:3", "KNOWS", map[string]interface{}{"since": 2021})

	rows := oqlExec(t, store, "SELECT * FROM KNOWS WHERE since > 2020")
	if len(rows) != 2 {
		t.Errorf("WHERE since > 2020: expected 2 rows, got %d", len(rows))
	}
}

func TestOQL_Stage10_BlobEdge_SelectStar(t *testing.T) {
	store, cleanup := setupOQLEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	eps := store.(storage.EdgePropertyStore)

	// Register schema only (no adapted table) — blob path.
	must(t, store.(storage.EdgeSchemaStore).RegisterEdgeSchema(ctx, "FOLLOWS",
		map[string]interface{}{
			"properties": map[string]interface{}{
				"notify": map[string]interface{}{"type": "boolean"},
			},
		}))

	eps.AddEdgeWithProps(ctx, "user:1", "user:2", "FOLLOWS", map[string]interface{}{"notify": true})
	eps.AddEdgeWithProps(ctx, "user:2", "user:3", "FOLLOWS", map[string]interface{}{"notify": false})

	rows := oqlExec(t, store, "SELECT * FROM FOLLOWS")
	if len(rows) != 2 {
		t.Errorf("SELECT * FROM FOLLOWS (blob): expected 2 rows, got %d", len(rows))
		for _, r := range rows {
			t.Logf("  row: %v", r)
		}
	}
}

func TestOQL_Stage10_BlobEdge_WhereFilter(t *testing.T) {
	store, cleanup := setupOQLEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	eps := store.(storage.EdgePropertyStore)

	must(t, store.(storage.EdgeSchemaStore).RegisterEdgeSchema(ctx, "FOLLOWS",
		map[string]interface{}{
			"properties": map[string]interface{}{
				"since": map[string]interface{}{"type": "integer"},
			},
		}))

	eps.AddEdgeWithProps(ctx, "user:1", "user:2", "FOLLOWS", map[string]interface{}{"since": 2019})
	eps.AddEdgeWithProps(ctx, "user:1", "user:3", "FOLLOWS", map[string]interface{}{"since": 2023})

	rows := oqlExec(t, store, "SELECT * FROM FOLLOWS WHERE since > 2020")
	if len(rows) != 1 {
		t.Errorf("blob WHERE since > 2020: expected 1 row, got %d", len(rows))
	}
}

func TestOQL_Stage10_NodeQueryNotAffected(t *testing.T) {
	store, cleanup := setupOQLEdgeStore(t)
	defer cleanup()
	ctx := context.Background()

	// Register adapted node table and create some nodes.
	type rae interface {
		RegisterAdaptedEntity(context.Context, string, map[string]interface{}) error
	}
	must(t, store.(rae).RegisterAdaptedEntity(ctx, "user", map[string]interface{}{
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"name"},
	}))
	store.Create(ctx, "user", map[string]interface{}{"name": "Alice"})
	store.Create(ctx, "user", map[string]interface{}{"name": "Bob"})

	rows := oqlExec(t, store, "SELECT * FROM user")
	if len(rows) != 2 {
		t.Errorf("node query regression: SELECT * FROM user expected 2, got %d", len(rows))
	}
}

func TestOQL_Stage10_ListEdges_BlobPath(t *testing.T) {
	store, cleanup := setupOQLEdgeStore(t)
	defer cleanup()
	ctx := context.Background()
	eps := store.(storage.EdgePropertyStore)

	el, ok := store.(storage.EdgeLister)
	if !ok {
		t.Skip("store does not implement EdgeLister")
	}

	must(t, store.(storage.EdgeSchemaStore).RegisterEdgeSchema(ctx, "KNOWS",
		map[string]interface{}{
			"properties": map[string]interface{}{
				"since": map[string]interface{}{"type": "integer"},
			},
		}))

	eps.AddEdgeWithProps(ctx, "a:1", "b:1", "KNOWS", map[string]interface{}{"since": 2020})
	eps.AddEdgeWithProps(ctx, "a:1", "b:2", "KNOWS", map[string]interface{}{"since": 2021})

	rows, err := el.ListEdges(ctx, "KNOWS")
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("ListEdges: expected 2, got %d", len(rows))
	}
	for _, row := range rows {
		if _, ok := row["edge_id"]; !ok {
			t.Error("ListEdges row missing edge_id")
		}
		if _, ok := row["rel"]; !ok {
			t.Error("ListEdges row missing rel")
		}
	}
}

// ---------------------------------------------------------------------------

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
