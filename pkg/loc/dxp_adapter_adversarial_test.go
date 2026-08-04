// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"testing"

	"github.com/ha1tch/xolu/pkg/dxp"
)

// TestLocAdapter_Execute_WithoutReserve_RefusedNotPanic: a claim
// whose (txn, participantID) was never Reserved — Execute's own
// pending-map lookup must refuse cleanly, never index into a
// zero-value or nil and panic. A coordinator bug or a malicious/
// malformed claim object handed to Execute directly should never
// crash the adapter.
func TestLocAdapter_Execute_WithoutReserve_RefusedNotPanic(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin", nil)
	ctx := context.Background()

	orphanClaim := dxp.Claim{
		Txn: "never-reserved-txn", Primitive: "loc", Tenant: s.TenantID().String(),
		ParticipantID: "p1", Resource: "loc:leaf:1", Weight: dxp.Pessimistic, Amount: 1,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute on a never-Reserved claim panicked: %v", r)
		}
	}()
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), orphanClaim); err == nil {
		t.Fatal("Execute on a never-Reserved claim: expected an error, got nil")
	}
}

// TestLocAdapter_Validate_WithoutReserve_RefusedNotPanic is
// Validate's own version of the same proof.
func TestLocAdapter_Validate_WithoutReserve_RefusedNotPanic(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin", nil)
	ctx := context.Background()

	orphanClaim := dxp.Claim{
		Txn: "never-reserved-txn", Primitive: "loc", Tenant: s.TenantID().String(),
		ParticipantID: "p1", Resource: "loc:leaf:1", Weight: dxp.Pessimistic, Amount: 1,
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Validate on a never-Reserved claim panicked: %v", r)
		}
	}()
	if err := a.Validate(ctx, orphanClaim); err == nil {
		t.Fatal("Validate on a never-Reserved claim: expected an error, got nil")
	}
}

// TestLocAdapter_DoubleReserve_SecondOverwritesPendingCleanly: two
// Reserve calls for the SAME (txn, participantID) — a coordinator bug
// this adapter has no way to prevent on its own, so it must not
// panic or leave inconsistent internal state, even though the
// coordinator's own attendance protocol should never actually produce
// this in practice.
func TestLocAdapter_DoubleReserve_SecondOverwritesPendingCleanly(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "bin-a", nil)
	defRootAndLeaf(t, s, "bin-b", nil)
	ctx := context.Background()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("double Reserve panicked: %v", r)
		}
	}()
	if _, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin-a"},
		"same-txn", "p1", futureDeadline(), dxp.Pessimistic); err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	cl2, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "bin-b"},
		"same-txn", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("second Reserve for the same (txn,participantID): %v", err)
	}

	// Execute must use whichever params are ACTUALLY pending now (the
	// second Reserve's own params) — proving the internal map was
	// overwritten cleanly, not corrupted into some mixed state.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Execute(ctx, dxp.NewSQLStore(tx), cl2); err != nil {
		t.Fatalf("Execute after double-Reserve: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var locationID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT l.location_id FROM loc_assignment a JOIN locations l ON l.location_key=a.location_key WHERE a.subject_ref='s1'`).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if locationID != "bin-b" {
		t.Fatalf("double-Reserve: want the SECOND Reserve's destination (bin-b) to win, got %q", locationID)
	}
}

// TestLocAdapter_Execute_LeafDeletedBetweenReserveAndExecute: a
// genuine mid-flight race no in-process lock can prevent — Reserve
// resolves and holds a claim against a leaf, then (in a real
// deployment) a concurrent DELETE on that same leaf could complete
// before Execute's own moveInTx runs. Execute's own leaf-entry CAS
// resolves location_id via a subquery at COMMIT time, not from
// Reserve's earlier snapshot, so a deleted leaf must be refused
// cleanly at Execute, not silently succeed against a location that no
// longer exists, and not panic.
func TestLocAdapter_Execute_LeafDeletedBetweenReserveAndExecute(t *testing.T) {
	s, _, a := testAdapter(t)
	defRootAndLeaf(t, s, "doomed", nil)
	ctx := context.Background()

	cl, err := a.Reserve(ctx, s.TenantID().String(),
		DxpMoveParams{SubjectRef: "s1", ToLocationID: "doomed"},
		"txn-1", "p1", futureDeadline(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	// Simulate the concurrent delete completing before Execute runs.
	if err := s.Delete(ctx, "doomed", false); err != nil {
		t.Fatalf("deleting the reserved-against leaf: %v", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Execute against a since-deleted leaf panicked: %v", r)
		}
	}()
	_, execErr := a.Execute(ctx, dxp.NewSQLStore(tx), cl)
	_ = tx.Rollback()
	if execErr == nil {
		t.Fatal("Execute against a since-deleted leaf: expected a refusal, got nil")
	}

	// No loc_assignment row must have been created for a location that
	// no longer exists.
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_assignment WHERE subject_ref='s1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("want 0 loc_assignment rows after Execute refused a deleted leaf, got %d", count)
	}
}

// TestLocAdapter_Release_WithoutReserve_Idempotent: Release on a
// claim that was never Reserved must be a harmless no-op, matching
// the documented idempotency contract, not a panic from deleting a
// nonexistent map key (Go's own delete() is already nil-safe for a
// missing key, but worth confirming directly at this package's own
// call site rather than assumed from the language spec alone).
func TestLocAdapter_Release_WithoutReserve_Idempotent(t *testing.T) {
	s, _, a := testAdapter(t)
	ctx := context.Background()
	orphanClaim := dxp.Claim{
		Txn: "never-reserved", Primitive: "loc", Tenant: s.TenantID().String(),
		ParticipantID: "p1", Resource: "loc:leaf:999", Weight: dxp.Pessimistic, Amount: 1,
	}
	if err := a.Release(ctx, orphanClaim); err != nil {
		t.Fatalf("Release on a never-Reserved claim: want nil (idempotent no-op), got %v", err)
	}
}
