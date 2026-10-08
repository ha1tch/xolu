package oql

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

// Regression tests for two defects found by a client that keeps entity history
// in an adapted (schema-registered) type and queries it by entity.

func openRegressionStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "reg.db"), storage.SQLiteConfig{})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// A SELECT that returns rows and filters an adapted table must return the
// matching rows at any table size. Below the planner's push-down threshold the
// Go path read the adapted table and was correct; above it the blob push-down
// ran against a table that holds no rows for an adapted type and returned
// nothing, while still reporting the whole table as scanned.
func TestAdaptedFilteredSelectReturnsRowsAboveThreshold(t *testing.T) {
	ctx := context.Background()
	store := openRegressionStore(t)
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entity_id":   map[string]interface{}{"type": "integer", "x-xolu-index": true},
			"version":     map[string]interface{}{"type": "integer", "x-xolu-index": true},
			"change_kind": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"entity_id", "version"},
	}
	if err := store.RegisterAdaptedEntity(ctx, "hist", schema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}
	engine := NewEngine(store, t.TempDir())

	// Entity 7 has 20 versions; every other row belongs to some other entity.
	total := 0
	grow := func(target int) {
		for ; total < target; total++ {
			entity := 1000 + total
			version := 1 + total%900
			if total < 20 {
				entity, version = 7, total+1
			}
			row := map[string]interface{}{"entity_id": float64(entity), "version": float64(version), "change_kind": "save"}
			if _, err := store.Save(ctx, "hist", 100+total, row); err != nil {
				t.Fatalf("Save row %d: %v", total, err)
			}
		}
	}

	queries := []struct {
		name, oql string
		want      func(total int) int
	}{
		{"filter on an indexed integer", "SELECT id, version FROM hist WHERE entity_id = 7", func(int) int { return 20 }},
		{"filter on the other indexed integer", "SELECT id FROM hist WHERE version = 3", func(total int) int {
			n := 1 // entity 7 version 3
			for i := 20; i < total; i++ {
				if 1+i%900 == 3 {
					n++
				}
			}
			return n
		}},
		{"filter on a string column", "SELECT id FROM hist WHERE change_kind = 'save'", func(total int) int { return total }},
		{"filter with ORDER BY", "SELECT id, version FROM hist WHERE entity_id = 7 ORDER BY version DESC", func(int) int { return 20 }},
		{"TOP with a filter", "SELECT TOP 5 id, version FROM hist WHERE entity_id = 7 ORDER BY version DESC", func(int) int { return 5 }},
	}
	for _, size := range []int{20, 100, 1000} {
		grow(size)
		for _, q := range queries {
			t.Run(fmt.Sprintf("%d rows/%s", size, q.name), func(t *testing.T) {
				res, err := engine.Execute(ctx, q.oql)
				if err != nil {
					t.Fatalf("%s: %v", q.oql, err)
				}
				if got, want := len(res.Rows), q.want(size); got != want {
					t.Fatalf("%s returned %d rows, want %d (table has %d rows)", q.oql, got, want, size)
				}
			})
		}
	}
}
