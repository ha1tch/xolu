// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

// commit_missing_entity_test.go
//
// XOT230: a compare-and-set commit against an entity that does not exist must
// conflict. It used to take the create path and silently recreate the entity,
// so a stale write that lost a race with a delete resurrected the row.

import (
	"context"
	"errors"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

func TestCommit_CAS_MissingEntityConflicts(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := store.Save(ctx, "asset", 31, map[string]interface{}{"state": "A"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(ctx, "asset", 31); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	stale := 1
	req := storage.CommitRequest{
		Update: storage.CommitUpdate{Entity: "asset", ID: 31, Version: &stale, Data: map[string]interface{}{"state": "B"}},
		Append: []storage.CommitAppend{{Entity: "log", Data: map[string]interface{}{"ev": "must-not-land"}}},
	}
	if _, err := store.Commit(ctx, req); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("commit with a version on a deleted entity: err = %v, want ErrConflict", err)
	}
	if _, err := store.Get(ctx, "asset", 31); err == nil {
		t.Fatal("the deleted entity was recreated by a compare-and-set commit")
	}
	if rows, _ := store.List(ctx, "log"); len(rows) != 0 {
		t.Fatalf("the append of a refused commit landed: %v", rows)
	}

	// An id that never existed behaves the same way.
	req.Update.ID = 32
	if _, err := store.Commit(ctx, req); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("commit with a version on an id that never existed: err = %v, want ErrConflict", err)
	}
	if _, err := store.Get(ctx, "asset", 32); err == nil {
		t.Fatal("an entity that never existed was created by a compare-and-set commit")
	}
}

// Without a version the commit is an upsert and still creates the entity.
func TestCommit_NoVersionOnMissingEntityStillCreates(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()

	res, err := store.Commit(ctx, storage.CommitRequest{
		Update: storage.CommitUpdate{Entity: "asset", ID: 41, Data: map[string]interface{}{"state": "A"}},
	})
	if err != nil {
		t.Fatalf("Commit without a version: %v", err)
	}
	if !res.Update.Created {
		t.Fatalf("Created = false, want true")
	}
	got, err := store.Get(ctx, "asset", 41)
	if err != nil || got["state"] != "A" {
		t.Fatalf("Get after create: %v %v", got, err)
	}
}
