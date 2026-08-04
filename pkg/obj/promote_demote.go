// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// promote_demote.go — T-121 (wave 10), Stage 3: store-level helpers
// specific to the promote/demote lifecycle transition
// (obj-00-design.md §9, obj-01-rest-api.md §5). The dxp.Participant
// itself lives in dxp_adapter.go; this file holds the transaction-
// scoped store logic that adapter's own Execute calls into, matching
// this package's own established split (containment.go's
// moveToContainerInTx, store.go's attachInTx).

package obj

import (
	"context"
	"database/sql"
)

// unassignAndDetachInTx is demote's own obj-side operation: clear the
// subject's current position (relinquishing its container's own count
// if it was PositionKindObj) and remove obj capability entirely, in
// one step. Deliberately NOT ordinary Detach's own logic — Detach
// (store.go) refuses outright unless already unassigned (XOLU-OBJ007,
// obj-01-rest-api.md §1's own bookkeeping-cleanup rule); demote is the
// opposite case by design — the subject IS expected to be positioned,
// that is the entire point of demoting it back into bulk `bal`
// tracking. XOLU-OBJ011 (obj-01-rest-api.md §5) if the subject
// currently CONTAINS anything of its own — dissolve contents first,
// checked here rather than left for the final DELETE's own FK
// behaviour to surface confusingly.
func unassignAndDetachInTx(ctx context.Context, tx *sql.Tx, subjectRef string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM obj_subjects WHERE subject_ref = ?`, subjectRef).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return &NotAttachedError{SubjectRef: subjectRef}
	}

	var containsAnything int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM obj_position WHERE contains_ref = ?`, subjectRef).Scan(&containsAnything); err != nil {
		return err
	}
	if containsAnything > 0 {
		return &DemoteRefusedError{SubjectRef: subjectRef}
	}

	prevContainer, hadPrev, err := txContainerOf(ctx, tx, subjectRef)
	if err != nil {
		return err
	}
	if hadPrev {
		if _, err := tx.ExecContext(ctx, `UPDATE obj_subjects SET cur_count = cur_count - 1 WHERE subject_ref = ?`, prevContainer); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM obj_position WHERE subject_ref = ?`, subjectRef); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM obj_subjects WHERE subject_ref = ?`, subjectRef); err != nil {
		return err
	}
	return journalEntry(ctx, tx, subjectRef, "detach", "", "", "")
}

// attachAndContainInTx composes attachInTx immediately followed by
// moveToContainerInTx — promote's own obj leg, atomically: an entity
// that has never had obj capability before gets it, and is positioned
// as contained by containerRef, in one step. Matches the ordinary
// two-call Attach-then-MoveToContainer sequence exactly, just within
// one coordinator-supplied transaction instead of two independent
// commits.
func attachAndContainInTx(ctx context.Context, tx *sql.Tx, subjectRef, containerRef string, capacity Capacity) error {
	if subjectRef == containerRef {
		return &ContainmentCycleError{SubjectRef: subjectRef, ContainerRef: containerRef}
	}
	if err := attachInTx(ctx, tx, subjectRef, capacity); err != nil {
		return err
	}
	_, _, err := moveToContainerInTx(ctx, tx, subjectRef, containerRef)
	return err
}
