// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// containment.go — T-120 (wave 10), Stage 2: obj-to-obj Move targets,
// the universal cycle-safety guard, and the count dimension of
// capacity CAS. Highest-risk item in both new waves
// (obj-02-implementation.md's own naming) — design-then-race, not
// TDD, the same instruction cal's hardest stage got.
//
// Scope, deliberately narrowed from what T-120 was originally filed
// with: max_weight_kg/max_volume_m3 enforcement is NOT built here.
// Two real, unresolved questions block it (confirmed directly with
// Horacio before writing this file, not assumed): what a contained
// subject itself contributes to its container's load, and units
// (kg/m3 hardcoded into field names doesn't accommodate L/mL/cm3,
// imperial, or whatever a given deployment actually uses). What ships
// here is the part with no such ambiguity: containment, universal
// cycle safety, and the max_count dimension (a plain integer, no
// units question at all).
//
// No separate containment-edge table: obj_position.contains_ref
// (T-119's own schema) already IS the containment edge, contained ->
// container. The "graph" this guards is therefore always a reverse
// tree (each subject has at most one container), not a general DAG —
// pkg/graphalgo's shared bounded-BFS is still the correct tool, just
// operating on a graph with branching factor <= 1 at the point being
// walked.
//
// Write-first, the T-34 discipline this codebase has now confirmed
// three times over (loc.Move, bal.Transfer/DefineAccount, and this
// function's own first version): the count CAS is the transaction's
// opening statement, not a preceding SELECT. The first version of
// this function checked subject/container existence and walked the
// cycle check BEFORE any write — real, sandbox-reproduced SQLITE_BUSY
// failures under this stage's own 32-goroutine stress harness (not
// assumed, caught by actually running it) confirmed this was the
// identical WAL-snapshot-invalidation defect class, not a theoretical
// concern. Diagnosis of *why* a refusal happened (container unknown
// vs. genuinely at capacity) runs only on the failure path, after the
// write — diagnoseContainerRefusal, mirroring loc's own
// diagnoseLeafRefusal exactly.

package obj

import (
	"context"
	"database/sql"
	"time"

	"github.com/ha1tch/xolu/pkg/graphalgo"
)

// cycleCheckLimit mirrors pkg/graph's own DefaultCycleCheckLimit —
// conservative on budget exhaustion is graphalgo.WouldCreateCycle's
// own documented behaviour, inherited unchanged.
const cycleCheckLimit = 5000

// txContainerOf returns tx's own transaction-scoped view of what
// subjectRef is currently contained by, if anything. Used both as
// graphalgo's own neighbour-lookup closure and directly by
// MoveToContainer to find the prior container to relinquish. A
// missing obj_position row (subject never attached, or a stale ref)
// is reported as "no container" here, not an error — callers that
// need to distinguish "never attached" do so via their own explicit
// existence check against obj_subjects, not by inspecting this
// function's own return.
func txContainerOf(ctx context.Context, tx *sql.Tx, subjectRef string) (string, bool, error) {
	var containerRef sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT contains_ref FROM obj_position WHERE subject_ref = ?`, subjectRef).Scan(&containerRef)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !containerRef.Valid {
		return "", false, nil
	}
	return containerRef.String, true, nil
}

// diagnoseContainerRefusal runs only on MoveToContainer's failure
// path, after the guarded count-CAS has already matched zero rows —
// distinguishing "containerRef was never obj-attached" (XOLU-OBJ005)
// from "containerRef is genuinely at its count ceiling" (XOLU-OBJ003),
// the same shape loc.diagnoseLeafRefusal already established for the
// identical two-cause-one-symptom problem.
func diagnoseContainerRefusal(ctx context.Context, tx *sql.Tx, containerRef string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM obj_subjects WHERE subject_ref = ?`, containerRef).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return &ContainerNotAttachedError{ContainerRef: containerRef}
	}
	return &CapacityError{SubjectRef: containerRef, Dimension: "count"}
}

// MoveToContainer sets subjectRef's position to "contained by
// containerRef" (obj-00-design.md §5, the "obj" kind of §0's own
// three move targets). Universal cycle safety, guard-bearing, checked
// in the same transaction as the write — never opt-in (§5's own
// rejection of the earlier "portable flag" alternative). Also guards
// containerRef's own max_count dimension in the identical
// transaction, write-first (see this file's own package doc for why).
//
// XOLU-OBJ004 if the move would create a cycle (self-containment
// always is, checked before opening a transaction at all — a pure
// string comparison, no I/O). XOLU-OBJ005 if containerRef is not
// itself obj-attached. XOLU-OBJ003 if containerRef is at its own
// count ceiling.
func (s *Store) MoveToContainer(ctx context.Context, subjectRef, containerRef string) error {
	if subjectRef == containerRef {
		return &ContainmentCycleError{SubjectRef: subjectRef, ContainerRef: containerRef}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	prevContainer, hadPrev, err := moveToContainerInTx(ctx, tx, subjectRef, containerRef)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Mirror after commit, best-effort (this file's own doc comment):
	// the old edge (if any) is dissolved, the new one added.
	if hadPrev {
		s.mirrorContainmentRemove(subjectRef, prevContainer)
	}
	s.mirrorContainmentAdd(subjectRef, containerRef)
	return nil
}

// moveToContainerInTx is MoveToContainer's own transaction-scoped
// core — split out so dxp's own Execute (dxp_adapter.go, T-121) can
// run the identical guard logic against the coordinator-supplied
// transaction, matching loc.Move/moveInTx's own established split
// exactly. Self-containment is NOT re-checked here — MoveToContainer
// already refused it before ever opening a transaction (a pure string
// comparison needs no I/O); dxp's own Execute checks it itself before
// calling in, at the point it has both refs in hand. Returns the
// subject's own previous container (T-123: for the caller's own
// best-effort graph-mirror removal after commit), hadPrev false if it
// had none.
func moveToContainerInTx(ctx context.Context, tx *sql.Tx, subjectRef, containerRef string) (prevContainer string, hadPrev bool, err error) {
	// Write-first: the count CAS is the transaction's opening
	// statement. Decision lives inside the write's own predicate,
	// rows-affected is the verdict — never read-then-decide-then-write
	// (the house discipline, T-34).
	res, err := tx.ExecContext(ctx, `
		UPDATE obj_subjects SET cur_count = cur_count + 1
		WHERE subject_ref = ? AND (max_count IS NULL OR cur_count + 1 <= max_count)`,
		containerRef)
	if err != nil {
		return "", false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", false, diagnoseContainerRefusal(ctx, tx, containerRef)
	}

	// Cycle check, now that the write lock guarding containerRef's own
	// count is already secured. A detected cycle rolls back the count
	// increment above via the deferred Rollback — never a window where
	// a cycle-refused attempt leaves a phantom count charge behind.
	wouldCycle, err := graphalgo.WouldCreateCycle(subjectRef, containerRef, cycleCheckLimit,
		func(node string) ([]string, error) {
			c, ok, err := txContainerOf(ctx, tx, node)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, nil
			}
			return []string{c}, nil
		})
	if err != nil {
		return "", false, err
	}
	if wouldCycle {
		return "", false, &ContainmentCycleError{SubjectRef: subjectRef, ContainerRef: containerRef}
	}

	// subjectRef's own existence + prior container, needed to
	// relinquish correctly. A subject that was never attached at all
	// is refused here, not left to surface as a confusing 0-rows
	// result from the final UPDATE below.
	var subjectExists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM obj_subjects WHERE subject_ref = ?`, subjectRef).Scan(&subjectExists); err != nil {
		return "", false, err
	}
	if subjectExists == 0 {
		return "", false, &NotAttachedError{SubjectRef: subjectRef}
	}
	prevContainer, hadPrev, err = txContainerOf(ctx, tx, subjectRef)
	if err != nil {
		return "", false, err
	}

	// If subjectRef was already contained by something else, that
	// former container's own count is relinquished in this same
	// transaction — never a window where the count is double-booked
	// (both old and new container charged) or lost (neither charged).
	if hadPrev {
		if _, err := tx.ExecContext(ctx, `UPDATE obj_subjects SET cur_count = cur_count - 1 WHERE subject_ref = ?`, prevContainer); err != nil {
			return "", false, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE obj_position SET kind = 'obj', loc_leaf_id = NULL, contains_ref = ?, updated_at = ?
		WHERE subject_ref = ?`, containerRef, time.Now().UTC(), subjectRef); err != nil {
		return "", false, err
	}
	if err := journalEntry(ctx, tx, subjectRef, "move", string(PositionKindObj), "", containerRef); err != nil {
		return "", false, err
	}
	return prevContainer, hadPrev, nil
}
