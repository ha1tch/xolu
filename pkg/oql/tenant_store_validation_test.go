package oql

import (
	"context"
	"testing"
)

// XOT229: names in a query run with a tenant's store are validated against that
// store as well as the default store and the schemas on disk.

// A query run with a store scoped to a tenant must be validated against that
// store's entity types, not the engine's default store.
func TestExecuteWithStoreValidatesAgainstThatStore(t *testing.T) {
	ctx := context.Background()
	defaultStore := openRegressionStore(t)
	tenantStore := openRegressionStore(t)
	engine := NewEngine(defaultStore, t.TempDir())

	for i := 0; i < 3; i++ {
		if _, err := tenantStore.Create(ctx, "widget", map[string]interface{}{"n": float64(i)}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if _, err := defaultStore.Create(ctx, "gadget", map[string]interface{}{"n": float64(1)}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// An entity type that exists only in the tenant's store is queryable there.
	res, err := engine.ExecuteWithStore(ctx, "SELECT id FROM widget", tenantStore)
	if err != nil {
		t.Fatalf("tenant query for a tenant-only type: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(res.Rows))
	}
	// The same type is still unknown to the default store.
	if _, err := engine.Execute(ctx, "SELECT id FROM widget"); err == nil {
		t.Fatal("the default store has no widget type; Execute should fail")
	}
	// A type that exists in neither the tenant's store nor as a schema is unknown there.
	if _, err := engine.ExecuteWithStore(ctx, "SELECT id FROM nosuch", tenantStore); err == nil {
		t.Fatal("a type unknown to the tenant store should fail validation")
	}
	// What already worked is kept: a type the default store knows still validates
	// through a tenant store, and the query sees only the tenant's own rows (none).
	res, err = engine.ExecuteWithStore(ctx, "SELECT id FROM gadget", tenantStore)
	if err != nil {
		t.Fatalf("a type known to the default store stopped validating through a tenant store: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("the tenant store returned rows it does not hold: %v", res.Rows)
	}
}
