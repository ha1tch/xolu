// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// retire.go — T-122's own minimal, focused build of retire
// (obj-01-rest-api.md §6, obj-00-design.md §12): built here only
// because T-122's own filed exit criteria names "one retire" in its
// fixture, not as a front-run of T-124's own fuller "API surface
// completion" scope. What ships: Retire itself (irreversible, refuses
// if the subject still contains anything, journaled). What does NOT
// ship here, deliberately out of scope for this item: retrofitting
// Move/Detach/MoveToContainer to refuse further operations against an
// already-retired subject. A retired subject's row persists (unlike
// Detach's own deletion) so GET and the journal fold can still see
// it, matching §12's own "closer in shape to a bal account closure"
// framing -- but nothing yet stops a caller from calling Move against
// a retired subject_ref. Flagged here, not silently left as an
// undocumented gap, and left for T-124 to close properly alongside
// the other lifecycle-completion work that item already owns.
package obj

import (
	"context"
	"database/sql"
	"time"
)

// Retire marks subjectRef permanently retired (obj-00-design.md §12):
// the physical thing itself has ceased to exist. Irreversible.
// XOLU-OBJ012 if the subject currently contains anything, or is
// already retired.
func (s *Store) Retire(ctx context.Context, subjectRef string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Write-first, matching this package's own T-34 discipline: the
	// retired_at CAS is the transaction's opening statement, its own
	// predicate excluding an already-retired row so a double-retire
	// attempt fails this UPDATE's own row match rather than needing a
	// separate pre-check.
	res, err := tx.ExecContext(ctx, `
		UPDATE obj_subjects SET retired_at = ?
		WHERE subject_ref = ? AND retired_at IS NULL`,
		time.Now().UTC(), subjectRef)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return diagnoseRetireRefusal(ctx, tx, subjectRef)
	}

	var containsAnything int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM obj_position WHERE contains_ref = ?`, subjectRef).Scan(&containsAnything); err != nil {
		return err
	}
	if containsAnything > 0 {
		return &RetireRefusedError{SubjectRef: subjectRef}
	}

	if err := journalEntry(ctx, tx, subjectRef, "retire", "", "", ""); err != nil {
		return err
	}
	return tx.Commit()
}

// diagnoseRetireRefusal runs only on Retire's failure path, after the
// guarded retired_at UPDATE has already matched zero rows —
// distinguishing "never attached at all" from "already retired",
// mirroring diagnoseContainerRefusal's own established shape.
func diagnoseRetireRefusal(ctx context.Context, tx *sql.Tx, subjectRef string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM obj_subjects WHERE subject_ref = ?`, subjectRef).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return &NotAttachedError{SubjectRef: subjectRef}
	}
	return &AlreadyRetiredError{SubjectRef: subjectRef}
}
