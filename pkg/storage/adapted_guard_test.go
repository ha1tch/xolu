// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

// XOT244: registering an adapted table over a type that already holds blob rows
// would hide them, so CheckAdaptedRegistrable must refuse and say how many.
func TestCheckAdaptedRegistrable_RefusesOverBlobRows(t *testing.T) {
	store, cleanup := sqliteOf(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CheckAdaptedRegistrable(ctx, "xot244"); err != nil {
		t.Fatalf("a type with no rows must be registrable: %v", err)
	}
	row := map[string]interface{}{"entity_id": float64(1), "version": float64(1)}
	if _, err := store.Save(ctx, "xot244", 1, row); err != nil {
		t.Fatalf("Save into an unregistered type: %v", err)
	}
	err := store.CheckAdaptedRegistrable(ctx, "xot244")
	var held *storage.BlobRowsExistError
	if !errors.Is(err, storage.ErrBlobRowsExist) || !errors.As(err, &held) || held.Rows != 1 {
		t.Fatalf("err = %v, want a BlobRowsExistError for 1 row", err)
	}
}

// A type that already has its adapted table stays registrable: registering it
// again is a no-op or a migration, never a way to hide rows.
func TestCheckAdaptedRegistrable_AllowsAnAlreadyAdaptedType(t *testing.T) {
	store, cleanup := sqliteOf(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.RegisterAdaptedEntity(ctx, "hist", historySchema(nil)); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}
	row := map[string]interface{}{"entity_id": float64(1), "version": float64(1)}
	if _, err := store.Save(ctx, "hist", 1, row); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.CheckAdaptedRegistrable(ctx, "hist"); err != nil {
		t.Fatalf("an already adapted type must stay registrable: %v", err)
	}
}
