// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"errors"
	"fmt"
)

// ErrBlobRowsExist is returned when a table would be registered for an entity
// type that already holds rows in the blob node store. Registering the table
// first would hide those rows: queries read the new, empty table while the
// rows stay in the blob store (XOT244). The strict promotion route migrates
// them instead.
var ErrBlobRowsExist = errors.New("entity type already has blob rows")

// BlobRowsExistError carries the entity type and how many rows would be hidden.
type BlobRowsExistError struct {
	Entity string
	Rows   int
}

func (e *BlobRowsExistError) Error() string {
	return fmt.Sprintf("%s already has %d row(s) in blob storage", e.Entity, e.Rows)
}

// Is makes errors.Is(err, ErrBlobRowsExist) true for every such error.
func (e *BlobRowsExistError) Is(target error) bool { return target == ErrBlobRowsExist }

// CheckAdaptedRegistrable reports whether an adapted table can be registered for
// entity in this store without hiding existing rows. A type that already has an
// adapted table is always registrable (the registration is then a no-op or a
// migration). It changes nothing.
func (s *SQLiteStore) CheckAdaptedRegistrable(ctx context.Context, entity string) error {
	if s.adapted.Get(entity) != nil {
		return nil
	}
	n, err := s.CountEntities(ctx, entity)
	if err != nil {
		return fmt.Errorf("check existing rows of %q: %w", entity, err)
	}
	if n > 0 {
		return &BlobRowsExistError{Entity: entity, Rows: n}
	}
	return nil
}
