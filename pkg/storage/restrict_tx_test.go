// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ha1tch/xolu/pkg/models"
)

// These tests exercise the in-transaction restrict check (G-12 closure)
// prong by prong, at the store level — below the server's in-memory
// fast-path, so they verify the authoritative check itself:
//   - blob referrers, discovered via the transactionally-synced edge table;
//   - adapted referrers, discovered via their REF_{field}_entity/_id
//     columns (adapted writes never populate the edge table).

func newRestrictTestStore(t *testing.T) (*SQLiteStore, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "restrict-tx")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(tmpDir+"/t.db", SQLiteConfig{
		DBPath:       tmpDir + "/t.db",
		EnableWAL:    true,
		GraphEnabled: true,
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store, func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}
}

// TestDeleteWithRestrict_BlobReferrerBlocks: a blob entity referencing the
// target through a REF field blocks the delete inside the transaction.
func TestDeleteWithRestrict_BlobReferrerBlocks(t *testing.T) {
	store, cleanup := newRestrictTestStore(t)
	defer cleanup()
	ctx := context.Background()

	userID, err := store.Create(ctx, "users", map[string]interface{}{"name": "alice"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = store.Create(ctx, "posts", map[string]interface{}{
		"title":     "hello",
		"author_id": models.NewReference("users", int64(userID)).ToMap(),
	})
	if err != nil {
		t.Fatalf("create post: %v", err)
	}

	// Restricted delete must refuse with RestrictViolationError.
	err = store.DeleteWithRestrict(ctx, "users", userID, []string{"posts"})
	var rve *RestrictViolationError
	if !errors.As(err, &rve) {
		t.Fatalf("expected RestrictViolationError, got %v", err)
	}
	if len(rve.Referrers) == 0 {
		t.Error("violation should name at least one referrer")
	}

	// Target must still exist.
	if _, err := store.Get(ctx, "users", userID); err != nil {
		t.Errorf("user should still exist after refused delete: %v", err)
	}

	// With no restricting entities, the same delete proceeds.
	if err := store.DeleteWithRestrict(ctx, "users", userID, nil); err != nil {
		t.Errorf("unrestricted delete should succeed: %v", err)
	}
}

// TestDeleteWithRestrict_AdaptedReferrerBlocks: an ADAPTED entity
// referencing the target blocks the delete via the REF-column probe — the
// prong the edge table cannot serve, previously a silent enforcement hole.
func TestDeleteWithRestrict_AdaptedReferrerBlocks(t *testing.T) {
	store, cleanup := newRestrictTestStore(t)
	defer cleanup()
	ctx := context.Background()

	userID, err := store.Create(ctx, "users", map[string]interface{}{"name": "bob"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// reviews is ADAPTED, with a REF field to users.
	reviewsSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"stars": map[string]interface{}{"type": "integer"},
			"reviewer": map[string]interface{}{
				"type": "object", "format": "ref",
			},
		},
	}
	if err := store.RegisterAdaptedEntity(ctx, "reviews", reviewsSchema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}
	_, err = store.Create(ctx, "reviews", map[string]interface{}{
		"stars":    5,
		"reviewer": models.NewReference("users", int64(userID)).ToMap(),
	})
	if err != nil {
		t.Fatalf("create review: %v", err)
	}

	// Restricted delete must refuse — discovered via REF columns, not edges.
	err = store.DeleteWithRestrict(ctx, "users", userID, []string{"reviews"})
	var rve *RestrictViolationError
	if !errors.As(err, &rve) {
		t.Fatalf("expected RestrictViolationError from adapted referrer, got %v", err)
	}

	// Target survives; removing the referrer unblocks.
	if _, err := store.Get(ctx, "users", userID); err != nil {
		t.Errorf("user should still exist: %v", err)
	}
}

// TestDeleteWithRestrict_MixedReferrers: both prongs active in one check.
func TestDeleteWithRestrict_MixedReferrers(t *testing.T) {
	store, cleanup := newRestrictTestStore(t)
	defer cleanup()
	ctx := context.Background()

	userID, err := store.Create(ctx, "users", map[string]interface{}{"name": "eve"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := store.Create(ctx, "posts", map[string]interface{}{
		"title":     "p",
		"author_id": models.NewReference("users", int64(userID)).ToMap(),
	}); err != nil {
		t.Fatalf("create post: %v", err)
	}
	reviewsSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"reviewer": map[string]interface{}{"type": "object", "format": "ref"},
		},
	}
	if err := store.RegisterAdaptedEntity(ctx, "reviews", reviewsSchema); err != nil {
		t.Fatalf("RegisterAdaptedEntity: %v", err)
	}
	if _, err := store.Create(ctx, "reviews", map[string]interface{}{
		"reviewer": models.NewReference("users", int64(userID)).ToMap(),
	}); err != nil {
		t.Fatalf("create review: %v", err)
	}

	err = store.DeleteWithRestrict(ctx, "users", userID, []string{"posts", "reviews"})
	var rve *RestrictViolationError
	if !errors.As(err, &rve) {
		t.Fatalf("expected RestrictViolationError, got %v", err)
	}
	if len(rve.Referrers) < 2 {
		t.Errorf("expected referrers from both prongs, got: %v", rve.Referrers)
	}
}
