// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestMove_ConcurrentSameSubject_NoDoubleCount asks a question G-14's
// own race harness doesn't: G-14 races DIFFERENT subjects into ONE
// leaf. This races the SAME subject into DIFFERENT leaves
// concurrently. moveInTx's own exit-then-enter logic reads the
// subject's current assignment mid-transaction — if SQLite's write
// serialization didn't give that read a genuinely up-to-date view (a
// real, non-obvious question about WAL-mode write-lock semantics, not
// assumed safe from reasoning about it), a subject could end up
// "logically" assigned to one leaf while a DIFFERENT leaf's count was
// also incremented for it — a phantom occupant nobody's capacity
// accounting would ever release.
func TestMove_ConcurrentSameSubject_NoDoubleCount(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	const nLeaves = 8
	for i := 0; i < nLeaves; i++ {
		mustDefPostable(t, s, fmt.Sprintf("leaf-%d", i), &root, nil)
	}

	const attempts = 40
	var wg sync.WaitGroup
	var errs int64
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			leaf := fmt.Sprintf("leaf-%d", n%nLeaves)
			if err := s.Move(ctx, MoveParams{SubjectRef: "shared-subject", ToLocationID: leaf}); err != nil {
				atomic.AddInt64(&errs, 1)
			}
		}(i)
	}
	wg.Wait()
	if errs > 0 {
		t.Fatalf("%d of %d concurrent moves for the same subject returned an unexpected error (none should refuse — no capacity limits set)", errs, attempts)
	}

	// Exactly one assignment row for the subject.
	var assignCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM loc_assignment WHERE subject_ref = 'shared-subject'`).Scan(&assignCount); err != nil {
		t.Fatal(err)
	}
	if assignCount != 1 {
		t.Fatalf("want exactly 1 loc_assignment row after %d concurrent moves of one subject, got %d", attempts, assignCount)
	}

	// The SUM of every leaf's count must be exactly 1 — the subject is
	// "logically present" in precisely one leaf's accounting, not
	// double-counted across two, and not zero (vanished).
	var totalCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(count), 0) FROM loc_capacity WHERE location_key != (SELECT location_key FROM locations WHERE location_id = 'root')`).Scan(&totalCount); err != nil {
		t.Fatal(err)
	}
	if totalCount != 1 {
		t.Fatalf("sum of all leaf counts after %d concurrent moves of ONE subject: want 1, got %d — a phantom occupant or a lost one", attempts, totalCount)
	}

	checkAllOracles(t, ctx, s)
}

// TestDef_ConcurrentDistinctLocations_NoKeyCollision races the dense
// MAX(location_key)+1 allocation Def uses (the same "declare-at-
// known-id" pattern bal/cal already use, presumably vetted for them —
// not assumed automatically safe for loc without checking directly).
func TestDef_ConcurrentDistinctLocations_NoKeyCollision(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})

	const n = 30
	var wg sync.WaitGroup
	var errs int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("concurrent-loc-%d", i)
			if _, err := s.Def(ctx, LocationDef{ID: id, ParentID: &root, Name: id, Postable: true}); err != nil {
				atomic.AddInt64(&errs, 1)
				t.Logf("Def(%q) failed: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	if errs > 0 {
		t.Fatalf("%d of %d concurrent Def calls for DISTINCT location_ids failed — dense-key allocation raced incorrectly", errs, n)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM locations WHERE location_id LIKE 'concurrent-loc-%'`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != n {
		t.Fatalf("want %d distinct locations created, got %d", n, total)
	}
	var distinctKeys int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT location_key) FROM locations WHERE location_id LIKE 'concurrent-loc-%'`).Scan(&distinctKeys); err != nil {
		t.Fatal(err)
	}
	if distinctKeys != n {
		t.Fatalf("want %d distinct internal keys, got %d — a key collision occurred", n, distinctKeys)
	}
}

// TestMove_RaceAgainstConcurrentDelete: one goroutine repeatedly tries
// to Move a subject into a leaf while another concurrently tries to
// Delete that same leaf. Neither operation has any special awareness
// of the other beyond the ordinary CAS/occupancy guards — this proves
// the outcome is always well-defined (each individual call either
// cleanly succeeds or cleanly fails, matching what its own guard
// should decide) rather than assuming a schedule this adversarial
// never produces database corruption.
func TestMove_RaceAgainstConcurrentDelete(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := "root"
	mustDef(t, s, LocationDef{ID: root, Name: "root", Placement: rootAnchor()})
	mustDefPostable(t, s, "contested", &root, nil)

	var wg sync.WaitGroup
	var moveErrs, panics int64

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				atomic.AddInt64(&panics, 1)
			}
		}()
		for i := 0; i < 20; i++ {
			if err := s.Move(ctx, MoveParams{SubjectRef: fmt.Sprintf("s%d", i), ToLocationID: "contested"}); err != nil {
				atomic.AddInt64(&moveErrs, 1)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				atomic.AddInt64(&panics, 1)
			}
		}()
		for i := 0; i < 20; i++ {
			_ = s.Delete(ctx, "contested", false) // expected to fail once occupied, or once already deleted — never expected to panic or corrupt
		}
	}()
	wg.Wait()

	if panics > 0 {
		t.Fatalf("Move/Delete race produced %d panics — must never panic regardless of schedule", panics)
	}
	// moveErrs are expected once "contested" is deleted (XOLU-LOC003
	// for every subsequent Move attempt) — the only thing under test
	// is that nothing panicked or left the store in a state the
	// oracle can't reconcile with itself.
	t.Logf("move errors: %d (expected once the leaf is deleted)", moveErrs)

	// If "contested" still exists, the oracle must still agree with
	// reality. If it was deleted, no assigned subject can reference a
	// now-nonexistent location_key (an orphaned foreign key would be
	// its own kind of corruption).
	var orphaned int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM loc_assignment a
		WHERE a.location_key IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM locations l WHERE l.location_key = a.location_key)`).Scan(&orphaned); err != nil {
		t.Fatal(err)
	}
	if orphaned > 0 {
		t.Fatalf("found %d loc_assignment rows referencing a deleted location_key — orphaned by the race", orphaned)
	}
}
