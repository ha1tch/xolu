// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/dxp"
)

func futureDeadline() int64 { return time.Now().Add(time.Minute).UnixNano() }

func testAdapter(t *testing.T) (*Store, *dxp.MemCache, *Adapter) {
	t.Helper()
	s := testStore(t)
	cache := dxp.NewMemCache()
	return s, cache, NewAdapter(s, cache)
}

func defRootAndLeaf(t *testing.T, s *Store, leafID string, ceiling *int64) {
	t.Helper()
	root := "root"
	if _, err := s.Get(context.Background(), root); err != nil {
		mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	}
	mustDefPostable(t, s, leafID, &root, ceiling)
}

func TestLocAdapter_Reserve_Succeeds(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin", nil)

	cl, err := a.Reserve(context.Background(), s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if cl.Primitive != "loc" || cl.Resource == "" {
		t.Fatalf("unexpected claim: %+v", cl)
	}
}

func TestLocAdapter_Reserve_UnknownLocation_Refused(t *testing.T) {
	_, _, a := testAdapter(t)
	_, err := a.Reserve(context.Background(), "0",
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "nowhere"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err == nil {
		t.Fatal("expected refusal for an unknown location_id")
	}
}

func TestLocAdapter_Reserve_AtCapacity_Refused(t *testing.T) {
	s, _, a := testAdapter(t)
	ceiling := int64(1)
	defRootAndLeaf(t, s, "bin", &ceiling)
	ctx := context.Background()

	if _, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	_, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s2", ToLocationID: "bin"},
		"txn-2", "p1", futureDeadline(), dxp.Pessimistic)
	var capErr *CapacityError
	if !errors.As(err, &capErr) || capErr.Kind != "leaf" {
		t.Fatalf("second reserve against a full-by-claims leaf: want *CapacityError{Kind:leaf}, got %v", err)
	}
}

// TestLocAdapter_Reserve_OptimisticSiblingsCoexist proves the mixed-
// weight admission rule for loc's own counted-capacity shape: two
// optimistic claims against a ceiling-1 leaf both succeed (optimistic
// claims are invisible to guard arithmetic everywhere), but a
// pessimistic claim against the same leaf, once its own count is
// accounted, is refused when it would exceed the ceiling.
func TestLocAdapter_Reserve_OptimisticInvisibleToArithmetic(t *testing.T) {
	s, _, a := testAdapter(t)
	ceiling := int64(1)
	defRootAndLeaf(t, s, "bin", &ceiling)
	ctx := context.Background()

	if _, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin"},
		"txn-1", "p1", futureDeadline(), dxp.Optimistic); err != nil {
		t.Fatalf("optimistic reserve: %v", err)
	}
	// A second OPTIMISTIC claim: optimistic claims don't count toward
	// guard arithmetic for anyone, so this must succeed even against a
	// ceiling-1 leaf that already has a live optimistic claim.
	if _, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s2", ToLocationID: "bin"},
		"txn-2", "p1", futureDeadline(), dxp.Optimistic); err != nil {
		t.Fatalf("second optimistic reserve: want success (optimistic invisible to arithmetic), got %v", err)
	}
}

func TestLocAdapter_Validate_Succeeds(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin", nil)
	ctx := context.Background()
	cl, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(ctx, cl); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestLocAdapter_Execute_ReadableAfterCommit is the T-86 precedent's
// own read-back-after-commit proof.
func TestLocAdapter_Execute_ReadableAfterCommit(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin", nil)
	ctx := context.Background()

	cl, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var locationKey int64
	if err := s.db.QueryRowContext(ctx, `SELECT location_key FROM loc_assignment WHERE subject_ref='s1'`).Scan(&locationKey); err != nil {
		t.Fatalf("loc_assignment not readable after commit: %v", err)
	}
}

// TestLocAdapter_Execute_AbortedTx_NothingPersists is this stage's own
// version of the ts adapter's aborted-batch-leaves-no-trace proof
// (T-86) — the SQL-transaction shape of the same property.
func TestLocAdapter_Execute_AbortedTx_NothingPersists(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin", nil)
	ctx := context.Background()

	cl, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	sqlStore := dxp.NewSQLStore(tx)
	if _, err := a.Execute(ctx, sqlStore, cl); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := sqlStore.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_assignment WHERE subject_ref='s1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected zero loc_assignment rows after Abort, got %d", count)
	}
	var journalCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_journal WHERE subject_ref='s1'`).Scan(&journalCount); err != nil {
		t.Fatal(err)
	}
	if journalCount != 0 {
		t.Fatalf("expected zero loc_journal rows after Abort, got %d", journalCount)
	}
}

func TestLocAdapter_Execute_WrongStoreType_Refused(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin", nil)
	ctx := context.Background()
	cl, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(ctx, &dxp.PebbleStore{}, cl); err == nil {
		t.Fatal("expected refusal when handed a non-SQL ParticipantStore")
	}
}

func TestLocAdapter_Release_ClearsPending_Idempotent(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin", nil)
	ctx := context.Background()
	cl, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Release(ctx, cl); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// A second Release for the same claim must be a harmless no-op.
	if err := a.Release(ctx, cl); err != nil {
		t.Fatalf("second Release (idempotency): %v", err)
	}
	// Execute after Release must fail cleanly — pending was cleared.
	tx, _ := s.db.BeginTx(ctx, nil)
	defer func() { _ = tx.Rollback() }()
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl); err == nil {
		t.Fatal("expected Execute to fail after Release cleared pending")
	}
}
