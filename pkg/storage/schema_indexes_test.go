// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

// schema_indexes_test.go
//
// XOT236: unique and composite indexes declared in a schema.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

func historySchema(extra map[string]interface{}) map[string]interface{} {
	s := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entity_id": map[string]interface{}{"type": "integer"},
			"version":   map[string]interface{}{"type": "integer"},
			"note":      map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"entity_id", "version"},
	}
	for k, v := range extra {
		s[k] = v
	}
	return s
}

func uniqueEntityVersion() map[string]interface{} {
	return map[string]interface{}{
		"x-xolu-indexes": []interface{}{
			map[string]interface{}{"columns": []interface{}{"entity_id", "version"}, "unique": true},
		},
	}
}

func sqliteOf(t *testing.T) (*storage.SQLiteStore, func()) {
	t.Helper()
	store, cleanup := sqliteFactory(t)
	s, ok := store.(*storage.SQLiteStore)
	if !ok {
		cleanup()
		t.Skip("not a SQLite store")
	}
	return s, cleanup
}

func TestUniqueCompositeIndex_RefusesASecondRowWithTheSameKey(t *testing.T) {
	store, cleanup := sqliteOf(t)
	defer cleanup()
	ctx := context.Background()
	if err := store.RegisterAdaptedEntity(ctx, "hist", historySchema(uniqueEntityVersion())); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}
	row := func(e, v int) map[string]interface{} {
		return map[string]interface{}{"entity_id": float64(e), "version": float64(v)}
	}
	if _, err := store.Save(ctx, "hist", 1, row(7, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, "hist", 2, row(7, 2)); err != nil {
		t.Fatalf("a different version of the same entity must be allowed: %v", err)
	}
	if _, err := store.Save(ctx, "hist", 3, row(8, 1)); err != nil {
		t.Fatalf("the same version of a different entity must be allowed: %v", err)
	}

	_, err := store.Save(ctx, "hist", 4, row(7, 2)) // a different id, the same (entity_id, version)
	var uv *storage.UniqueViolationError
	if !errors.As(err, &uv) || !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("err = %v, want a *UniqueViolationError", err)
	}
	if strings.Join(uv.Columns, ",") != "entity_id,version" || uv.Entity != "hist" {
		t.Fatalf("violation = %+v, want hist over entity_id,version", uv)
	}
	if _, err := store.Get(ctx, "hist", 4); err == nil {
		t.Fatal("the refused row exists")
	}

	// An update that would collide is refused too, and leaves the row as it was.
	if _, err := store.Save(ctx, "hist", 3, row(7, 2)); !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("update into a taken key: err = %v, want ErrUniqueViolation", err)
	}
	if got, err := store.Get(ctx, "hist", 3); err != nil || fmt.Sprint(got["entity_id"]) != "8" {
		t.Fatalf("the row changed after a refused update: %v %v", got, err)
	}
}

// A duplicate primary key on an adapted table answers like a blob entity does.
func TestAdaptedAppendWithAnExistingIDIsAlreadyExists(t *testing.T) {
	store, cleanup := sqliteOf(t)
	defer cleanup()
	ctx := context.Background()
	if err := store.RegisterAdaptedEntity(ctx, "hist", historySchema(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, "hist", 5, map[string]interface{}{"entity_id": float64(1), "version": float64(1)}); err != nil {
		t.Fatal(err)
	}
	two := 5
	_, err := store.Commit(ctx, storage.CommitRequest{
		Update: storage.CommitUpdate{Entity: "anchor", ID: 1, Data: map[string]interface{}{"a": 1}},
		Append: []storage.CommitAppend{{Entity: "hist", ID: &two, Data: map[string]interface{}{"entity_id": float64(2), "version": float64(1)}}},
	})
	if !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
}

// In a commit, a refused unique key rolls the whole commit back, update included.
func TestCommit_UniqueViolationRollsBackTheCommit(t *testing.T) {
	store, cleanup := sqliteOf(t)
	defer cleanup()
	ctx := context.Background()
	if err := store.RegisterAdaptedEntity(ctx, "hist", historySchema(uniqueEntityVersion())); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, "anchor", 1, map[string]interface{}{"state": "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, "hist", 1, map[string]interface{}{"entity_id": float64(7), "version": float64(2)}); err != nil {
		t.Fatal(err)
	}
	ver := 1
	_, err := store.Commit(ctx, storage.CommitRequest{
		Update: storage.CommitUpdate{Entity: "anchor", ID: 1, Version: &ver, Data: map[string]interface{}{"state": "B"}},
		Append: []storage.CommitAppend{{Entity: "hist", Data: map[string]interface{}{"entity_id": float64(7), "version": float64(2)}}},
	})
	if !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("err = %v, want ErrUniqueViolation", err)
	}
	if got, _ := store.Get(ctx, "anchor", 1); got["state"] != "A" {
		t.Fatalf("the update of a refused commit landed: %v", got)
	}
}

func TestUniqueOnAProperty(t *testing.T) {
	store, cleanup := sqliteOf(t)
	defer cleanup()
	ctx := context.Background()
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"sku": map[string]interface{}{"type": "string", "x-xolu-unique": true, "x-xolu-index": true},
		},
	}
	if err := store.RegisterAdaptedEntity(ctx, "part", schema); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, "part", 1, map[string]interface{}{"sku": "A-1"}); err != nil {
		t.Fatal(err)
	}
	// The same column is asked for both as a plain index and as a unique one: the
	// unique one must survive de-duplication, or the constraint would be lost.
	if _, err := store.Save(ctx, "part", 2, map[string]interface{}{"sku": "A-1"}); !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("a duplicate sku: err = %v, want ErrUniqueViolation", err)
	}
}

func TestIndexKeywordsAreValidated(t *testing.T) {
	for name, extra := range map[string]map[string]interface{}{
		"not an array":     {"x-xolu-indexes": "entity_id"},
		"not an object":    {"x-xolu-indexes": []interface{}{"entity_id"}},
		"no fields":        {"x-xolu-indexes": []interface{}{map[string]interface{}{"unique": true}}},
		"empty fields":     {"x-xolu-indexes": []interface{}{map[string]interface{}{"columns": []interface{}{}}}},
		"unknown field":    {"x-xolu-indexes": []interface{}{map[string]interface{}{"columns": []interface{}{"nope"}}}},
		"the primary key":  {"x-xolu-indexes": []interface{}{map[string]interface{}{"columns": []interface{}{"id"}}}},
		"a repeated field": {"x-xolu-indexes": []interface{}{map[string]interface{}{"columns": []interface{}{"version", "version"}}}},
		"unknown key":      {"x-xolu-indexes": []interface{}{map[string]interface{}{"columns": []interface{}{"version"}, "uniq": true}}},
		"unique not bool":  {"x-xolu-indexes": []interface{}{map[string]interface{}{"columns": []interface{}{"version"}, "unique": "yes"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := storage.ValidateSchemaIndexes(historySchema(extra)); err == nil {
				t.Fatal("a malformed x-xolu-indexes was accepted")
			}
		})
	}
	if err := storage.ValidateSchemaIndexes(historySchema(uniqueEntityVersion())); err != nil {
		t.Fatalf("a valid declaration was refused: %v", err)
	}
	if err := storage.ValidateSchemaIndexes(historySchema(nil)); err != nil {
		t.Fatalf("a schema with no index keywords was refused: %v", err)
	}
}

// Re-registering a schema that adds a unique index creates it, and refuses to
// when existing rows already violate it, leaving the data and the old schema as
// they were.
func TestAddingAUniqueIndexToExistingData(t *testing.T) {
	store, cleanup := sqliteOf(t)
	defer cleanup()
	ctx := context.Background()
	if err := store.RegisterAdaptedEntity(ctx, "hist", historySchema(nil)); err != nil {
		t.Fatal(err)
	}
	for id, v := range []int{1, 2, 2} { // two rows share (7, 2)
		if _, err := store.Save(ctx, "hist", id+1, map[string]interface{}{"entity_id": float64(7), "version": float64(v)}); err != nil {
			t.Fatal(err)
		}
	}
	err := store.RegisterAdaptedEntity(ctx, "hist", historySchema(uniqueEntityVersion()))
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("adding a unique index over duplicate rows: err = %v, want a unique-constraint failure", err)
	}
	if rows, _ := store.List(ctx, "hist"); len(rows) != 3 {
		t.Fatalf("rows = %d after the refused registration, want 3", len(rows))
	}

	// Without the duplicate the same registration succeeds and the index exists.
	if err := store.Delete(ctx, "hist", 3); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterAdaptedEntity(ctx, "hist", historySchema(uniqueEntityVersion())); err != nil {
		t.Fatalf("adding the unique index over clean data: %v", err)
	}
	if _, err := store.Save(ctx, "hist", 9, map[string]interface{}{"entity_id": float64(7), "version": float64(2)}); !errors.Is(err, storage.ErrUniqueViolation) {
		t.Fatalf("after the migration: err = %v, want ErrUniqueViolation", err)
	}
}

var _ = sql.ErrNoRows
