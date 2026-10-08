package oql

import (
	"context"
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/ha1tch/tsqlparser/ast"
)

// XOT228 follow-up: id and _version are real columns of every adapted table. A
// SELECT that named them used to be untranslatable, so it never took the
// push-down path and never used the table's indexes. Every query here must
// plan as a full push-down and return what the Go path returns.

func numString(v interface{}) string {
	switch n := v.(type) {
	case int:
		return fmt.Sprint(n)
	case int64:
		return fmt.Sprint(n)
	case float64:
		return fmt.Sprintf("%.0f", n)
	}
	return fmt.Sprintf("?%T", v)
}

func TestAdaptedSelectOfSystemColumnsIsPushedDown(t *testing.T) {
	ctx := context.Background()
	store := openRegressionStore(t)
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entity_id": map[string]interface{}{"type": "integer", "x-xolu-index": true},
			"version":   map[string]interface{}{"type": "integer", "x-xolu-index": true},
		},
		"required": []interface{}{"entity_id", "version"},
	}
	if err := store.RegisterAdaptedEntity(ctx, "hist", schema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}
	const rows = 400
	for i := 0; i < rows; i++ {
		entity, version := 1000+i, 1+i%9
		if i < 20 {
			entity, version = 7, i+1
		}
		row := map[string]interface{}{"entity_id": float64(entity), "version": float64(version)}
		if _, err := store.Save(ctx, "hist", 100+i, row); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	push := NewPlannerWithDialectAndThreshold(&SQLiteDialect{}, 30)
	pdExec := &Executor{store: store, aggregator: NewAggregator(), planner: push, dialect: &SQLiteDialect{}}
	goStore := &nonAggStore{Store: store, q: store}
	goExec := &Executor{
		store:      goStore,
		aggregator: NewAggregator(),
		planner:    NewPlannerWithThreshold(math.MaxInt32),
		dialect:    &SQLiteDialect{},
	}

	ids := func(res *Result) []string {
		var out []string
		for _, r := range res.Rows {
			out = append(out, numString(r["id"]))
		}
		sort.Strings(out)
		return out
	}
	for _, q := range []string{
		"SELECT id, version FROM hist WHERE entity_id = 7",
		"SELECT id FROM hist WHERE entity_id = 7 ORDER BY version DESC",
		"SELECT id, entity_id FROM hist WHERE version = 3",
		"SELECT id FROM hist WHERE id = 105",
		"SELECT id, _version FROM hist WHERE entity_id = 7",
		"SELECT id FROM hist WHERE _version = 1 AND entity_id = 7",
	} {
		t.Run(q, func(t *testing.T) {
			stmt := parseOQL(t, q)
			plan := push.Plan(ctx, stmt.(*ast.SelectStatement), store)
			if len(plan.Push) == 0 || plan.Push[0] != PushFull {
				t.Fatalf("plan = %s (%s), want a full push-down", plan.pushNames(), plan.Reason)
			}
			want, err := goExec.ExecuteWithStore(ctx, stmt, goStore)
			if err != nil {
				t.Fatalf("Go path: %v", err)
			}
			got, err := pdExec.ExecuteWithStore(ctx, stmt, store)
			if err != nil {
				t.Fatalf("push-down: %v", err)
			}
			if len(want.Rows) == 0 {
				t.Fatal("the query is meant to match rows; the Go path returned none")
			}
			if fmt.Sprint(ids(got)) != fmt.Sprint(ids(want)) {
				t.Fatalf("push-down ids %v differ from the Go path's %v", ids(got), ids(want))
			}
		})
	}
}
