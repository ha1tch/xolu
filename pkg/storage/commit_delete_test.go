// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

// commit_delete_test.go
//
// XOT233: a Commit can delete an entity instead of upserting one, atomically
// with its appends. A deletion and the history row that records it commit or
// fail together.

import (
	"context"
	"errors"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

func entityVersion(t *testing.T, store storage.Store, entity string, id int) int {
	t.Helper()
	got, err := store.Get(context.Background(), entity, id)
	if err != nil {
		t.Fatalf("Get %s/%d: %v", entity, id, err)
	}
	switch v := got["_version"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	t.Fatalf("no _version in %v", got)
	return 0
}

func TestCommit_Delete_IsAtomicWithItsAppend(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.Save(ctx, "asset", 51, map[string]interface{}{"state": "A"}); err != nil {
		t.Fatal(err)
	}
	ver := entityVersion(t, store, "asset", 51)

	res, err := store.Commit(ctx, storage.CommitRequest{
		Delete: &storage.CommitDelete{Entity: "asset", ID: 51, Version: &ver},
		Append: []storage.CommitAppend{{Entity: "log", Data: map[string]interface{}{"ev": "deleted"}}},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Delete == nil || res.Delete.Version != ver || res.Delete.Entity != "asset" || res.Delete.ID != 51 {
		t.Fatalf("Delete result = %+v, want asset 51 at version %d", res.Delete, ver)
	}
	if len(res.Appended) != 1 {
		t.Fatalf("Appended = %v, want one row", res.Appended)
	}
	if _, err := store.Get(ctx, "asset", 51); err == nil {
		t.Fatal("the entity still exists after a committed delete")
	}
	if rows, _ := store.List(ctx, "log"); len(rows) != 1 {
		t.Fatalf("log rows = %d, want 1", len(rows))
	}
}

func TestCommit_Delete_StaleVersionConflictsAndChangesNothing(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	for _, state := range []string{"A", "A2"} {
		if _, err := store.Save(ctx, "asset", 52, map[string]interface{}{"state": state}); err != nil {
			t.Fatal(err)
		}
	}
	stale := entityVersion(t, store, "asset", 52) - 1

	_, err := store.Commit(ctx, storage.CommitRequest{
		Delete: &storage.CommitDelete{Entity: "asset", ID: 52, Version: &stale},
		Append: []storage.CommitAppend{{Entity: "log", Data: map[string]interface{}{"ev": "must-not-land"}}},
	})
	if !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if got, err := store.Get(ctx, "asset", 52); err != nil || got["state"] != "A2" {
		t.Fatalf("the entity changed after a refused delete: %v %v", got, err)
	}
	if rows, _ := store.List(ctx, "log"); len(rows) != 0 {
		t.Fatalf("the append of a refused commit landed: %v", rows)
	}
}

func TestCommit_Delete_UnconditionalDeletesWhateverVersion(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.Save(ctx, "asset", 53, map[string]interface{}{"state": "A"}); err != nil {
		t.Fatal(err)
	}
	res, err := store.Commit(ctx, storage.CommitRequest{
		Delete: &storage.CommitDelete{Entity: "asset", ID: 53},
		Append: []storage.CommitAppend{{Entity: "log", Data: map[string]interface{}{"ev": "d"}}},
	})
	if err != nil || res.Delete == nil {
		t.Fatalf("Commit: %+v %v", res, err)
	}
	if _, err := store.Get(ctx, "asset", 53); err == nil {
		t.Fatal("the entity still exists")
	}
}

// A delete of an entity that is not there: ErrNotFound without a version, and
// ErrConflict with one (the version describes a row that does not exist, as for
// an update, XOT230). Neither writes the append.
func TestCommit_Delete_MissingEntity(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	one := 1
	for _, tc := range []struct {
		name    string
		version *int
		want    error
	}{
		{"without a version", nil, storage.ErrNotFound},
		{"with a version", &one, storage.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Commit(ctx, storage.CommitRequest{
				Delete: &storage.CommitDelete{Entity: "asset", ID: 54, Version: tc.version},
				Append: []storage.CommitAppend{{Entity: "log", Data: map[string]interface{}{"ev": "must-not-land"}}},
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if rows, _ := store.List(ctx, "log"); len(rows) != 0 {
				t.Fatalf("the append of a refused commit landed: %v", rows)
			}
		})
	}
}

// If the append cannot be written, the delete is undone with it.
func TestCommit_Delete_RollsBackWhenTheAppendFails(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.Save(ctx, "asset", 55, map[string]interface{}{"state": "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, "log", 9, map[string]interface{}{"ev": "already here"}); err != nil {
		t.Fatal(err)
	}
	nine := 9
	_, err := store.Commit(ctx, storage.CommitRequest{
		Delete: &storage.CommitDelete{Entity: "asset", ID: 55},
		Append: []storage.CommitAppend{{Entity: "log", ID: &nine, Data: map[string]interface{}{"ev": "duplicate id"}}},
	})
	if !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if _, err := store.Get(ctx, "asset", 55); err != nil {
		t.Fatalf("the delete was not rolled back with its failed append: %v", err)
	}
}

// An entity with a registered schema is an adapted table; the delete reads its
// version from the table's own column.
func TestCommit_Delete_AdaptedEntity(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	sqlStore, ok := store.(*storage.SQLiteStore)
	if !ok {
		t.Skip("not a SQLite store")
	}
	ctx := context.Background()
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
	}
	if err := sqlStore.RegisterAdaptedEntity(ctx, "widget", schema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}
	for _, name := range []string{"a", "b"} {
		if _, err := store.Save(ctx, "widget", 61, map[string]interface{}{"name": name}); err != nil {
			t.Fatal(err)
		}
	}
	cur := entityVersion(t, store, "widget", 61)
	stale := cur - 1
	if _, err := store.Commit(ctx, storage.CommitRequest{
		Delete: &storage.CommitDelete{Entity: "widget", ID: 61, Version: &stale},
		Append: []storage.CommitAppend{{Entity: "log", Data: map[string]interface{}{"ev": "x"}}},
	}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale version on an adapted entity: err = %v, want ErrConflict", err)
	}
	res, err := store.Commit(ctx, storage.CommitRequest{
		Delete: &storage.CommitDelete{Entity: "widget", ID: 61, Version: &cur},
		Append: []storage.CommitAppend{{Entity: "log", Data: map[string]interface{}{"ev": "x"}}},
	})
	if err != nil || res.Delete == nil || res.Delete.Version != cur {
		t.Fatalf("Commit: %+v %v", res, err)
	}
	if _, err := store.Get(ctx, "widget", 61); err == nil {
		t.Fatal("the adapted entity still exists")
	}
}
