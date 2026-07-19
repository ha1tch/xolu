// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// TestRegression_ListEntitiesIncludesAdapted guards against the bug where
// ListEntities only queried the entities blob table, missing adapted entities
// entirely. Adapted entities live in their own tables (olu_<name>) and have
// no rows in the entities table, so a plain
//
//	SELECT DISTINCT entity_type FROM " + tenant.NodesTableName(0) + "
//
// returns nothing for them. The OQL validator calls ListEntities to check
// entity existence; the old behaviour caused JOIN queries targeting adapted
// entities to be rejected with "entity does not exist".
func TestRegression_ListEntitiesIncludesAdapted(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "regression.db")

	store, err := NewStore("sqlite", map[string]interface{}{"db_path": dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	sqlStore := store.(*SQLiteStore)
	ctx := context.Background()

	// Register two adapted entities
	for _, entity := range []string{"authors", "posts"} {
		schema := map[string]interface{}{
			"properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"name"},
		}
		if err := sqlStore.RegisterAdaptedEntity(ctx, entity, schema); err != nil {
			t.Fatalf("RegisterAdaptedEntity(%s): %v", entity, err)
		}
	}

	// Also create a blob entity by inserting a record
	if _, err := sqlStore.Create(ctx, "events", map[string]interface{}{"type": "click"}); err != nil {
		t.Fatalf("Create events: %v", err)
	}

	entities, err := sqlStore.ListEntities(ctx)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entities {
		found[e] = true
	}

	// Adapted entities must appear even though they have no blob rows
	for _, expected := range []string{"authors", "posts"} {
		if !found[expected] {
			t.Errorf("ListEntities missing adapted entity %q (Bug 4 regression); got: %v", expected, entities)
		}
	}

	// Blob entity must still appear
	if !found["events"] {
		t.Errorf("ListEntities missing blob entity %q; got: %v", "events", entities)
	}
}

// TestRegression_ListEntitiesNoDuplicates guards against the complementary
// case: if an adapted entity also happens to have a blob row in the entities
// table, it should appear exactly once.
func TestRegression_ListEntitiesNoDuplicates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "regression_dup.db")

	store, err := NewStore("sqlite", map[string]interface{}{"db_path": dbPath})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	sqlStore := store.(*SQLiteStore)
	ctx := context.Background()

	// Register one adapted entity
	schema := map[string]interface{}{
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
	}
	if err := sqlStore.RegisterAdaptedEntity(ctx, "products", schema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}

	// Create a row via the adapted entity
	if _, err := sqlStore.Create(ctx, "products", map[string]interface{}{"name": "widget"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entities, err := sqlStore.ListEntities(ctx)
	if err != nil {
		t.Fatalf("ListEntities: %v", err)
	}

	count := 0
	for _, e := range entities {
		if e == "products" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ListEntities returned %q %d times, want exactly 1; full list: %v", "products", count, entities)
	}
}
