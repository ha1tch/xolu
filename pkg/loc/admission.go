// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ─── Typed errors (XOLU-LOC family) ─────────────────────────────────

// CapacityError is returned when a leaf or fence's capacity CAS
// refuses — the predicate matched zero rows because the entry would
// exceed the ceiling (bal's §6 pattern, applied to admission instead
// of bounds). XOLU-LOC002 (leaf) or XOLU-LOC001 (fence).
type CapacityError struct {
	Kind string // "leaf" or "fence"
	Key  uint32
}

func (e *CapacityError) Error() string {
	code := "XOLU-LOC002"
	if e.Kind == "fence" {
		code = "XOLU-LOC001"
	}
	return fmt.Sprintf("%s: %s at capacity (key %d)", code, e.Kind, e.Key)
}

// InvariantError marks an impossible state: an exit CAS found no
// matching row with count > 0, meaning bookkeeping was already wrong
// before this call — asserted, not silently ignored, the same
// fsck-style treatment dxp gives its own impossible "abandoned-dirty"
// case (docs/RESOLVED.md, XOLU-DXP010's own doc comment).
type InvariantError struct {
	Detail string
}

func (e *InvariantError) Error() string {
	return "loc invariant violation: " + e.Detail
}

const (
	leafExitCAS = `UPDATE loc_capacity
	   SET count = count - 1
	 WHERE location_key = ?
	   AND count > 0`

	fenceEntryCAS = `UPDATE loc_fence_capacity
	   SET count = count + 1
	 WHERE fence_key = ?
	   AND (ceiling IS NULL OR count + 1 <= ceiling)`

	fenceExitCAS = `UPDATE loc_fence_capacity
	   SET count = count - 1
	 WHERE fence_key = ?
	   AND count > 0`
)

// MoveParams is Move's input. EnteredFenceKeys/ExitedFenceKeys are a
// membership *delta*, supplied directly by the caller — Stage 2's own
// "test hook" (loc-02-implementation.md): Move's job is applying the
// CAS guards correctly given a membership delta, not computing that
// delta from geometry. Stage 3 (T-116) replaces the caller that
// computes the delta from real Contains tests; Move's own logic here
// does not change when that happens.
type MoveParams struct {
	SubjectRef       string
	ToLocationID     string
	EnteredFenceKeys []FenceKey
	ExitedFenceKeys  []FenceKey
}

// diagnoseLeafRefusal runs only on Move's failure path, after the
// guarded entry UPDATE has already matched zero rows — the same
// shape as bal.diagnoseRefusal (store.go): the transaction is
// read-only and about to roll back, so extra queries here cost
// nothing a normal admit path pays. Distinguishes "location doesn't
// exist" (XOLU-LOC003) from "location exists, at capacity"
// (CapacityError).
func (s *Store) diagnoseLeafRefusal(ctx context.Context, tx *sql.Tx, locationID string) error {
	var key int64
	err := tx.QueryRowContext(ctx,
		`SELECT location_key FROM `+s.locationsTable()+` WHERE location_id = ?`, locationID).Scan(&key)
	if err == sql.ErrNoRows {
		return &UnknownLocationError{LocationID: locationID}
	}
	if err != nil {
		return err
	}
	return &CapacityError{Kind: "leaf", Key: uint32(key)}
}

// Move reassigns a subject's canonical tree-leaf position
// (loc-00-design.md §3c/§7a). Multi-target atomicity: one destination
// leaf plus every entered/exited fence, all guarded in one
// transaction — the first zero-rows-affected CAS refuses the whole
// move (rollback), never a partial application. Decision lives inside
// each UPDATE's predicate, rows-affected is the verdict — never
// read-then-decide-then-write (the house discipline, T-34).
//
// Write-first, mirroring bal.transferInTx exactly: the leaf entry CAS
// is the transaction's opening statement, resolving location_id via a
// subquery rather than a preceding SELECT. A read-first shape (SELECT
// the key, then UPDATE) hits WAL's snapshot invalidation under real
// concurrency — confirmed directly, not assumed: an early version of
// this function did exactly that, and a sandbox race pass surfaced
// SQLITE_BUSY errors under 32-way contention before this fix.
// treeAlignedFenceKeys walks locationKey's own ancestor chain (itself
// included) up to the root, collecting every fence aligned to any
// node in that chain — the "free tree walk" loc-00-design.md §5
// describes: a fence's membership for a tree-assigned subject follows
// automatically from where in the tree it sits, no exact Contains
// test needed (that's reserved for a genuinely reported coordinate,
// §7b).
func (s *Store) treeAlignedFenceKeys(ctx context.Context, locationKey int64) ([]FenceKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH RECURSIVE ancestors(location_key, parent_key) AS (
			SELECT location_key, parent_key FROM `+s.locationsTable()+` WHERE location_key = ?
			UNION ALL
			SELECT l.location_key, l.parent_key FROM `+s.locationsTable()+` l
				JOIN ancestors a ON l.location_key = a.parent_key
		)
		SELECT fence_key FROM fences WHERE aligned_location_key IN (SELECT location_key FROM ancestors)`,
		locationKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FenceKey
	for rows.Next() {
		var fk int64
		if err := rows.Scan(&fk); err != nil {
			return nil, err
		}
		out = append(out, FenceKey(uint32(fk)))
	}
	return out, rows.Err()
}

// TreeAlignedFenceDelta computes the entered/exited tree-aligned fence
// sets a move from the subject's current position to toLocationID
// would produce — the symmetric difference between the destination's
// own ancestor-chain fences and the origin's. Plain autocommit reads,
// run BEFORE any transaction opens: safe from the WAL read-then-write
// upgrade problem moveInTx's own write-first shape exists to avoid
// (this isn't a read inside a transaction that later writes — it's a
// read with no transaction at all, resolved once, before one starts).
func (s *Store) TreeAlignedFenceDelta(ctx context.Context, subjectRef, toLocationID string) (entered, exited []FenceKey, err error) {
	var toKey int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT location_key FROM `+s.locationsTable()+` WHERE location_id = ?`, toLocationID).Scan(&toKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, &UnknownLocationError{LocationID: toLocationID}
		}
		return nil, nil, err
	}
	destFences, err := s.treeAlignedFenceKeys(ctx, toKey)
	if err != nil {
		return nil, nil, err
	}

	var fromKey sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT location_key FROM loc_assignment WHERE subject_ref = ?`, subjectRef).Scan(&fromKey); err != nil && err != sql.ErrNoRows {
		return nil, nil, err
	}
	var originFences []FenceKey
	if fromKey.Valid {
		originFences, err = s.treeAlignedFenceKeys(ctx, fromKey.Int64)
		if err != nil {
			return nil, nil, err
		}
	}

	destSet := map[FenceKey]bool{}
	for _, fk := range destFences {
		destSet[fk] = true
	}
	originSet := map[FenceKey]bool{}
	for _, fk := range originFences {
		originSet[fk] = true
	}
	for fk := range destSet {
		if !originSet[fk] {
			entered = append(entered, fk)
		}
	}
	for fk := range originSet {
		if !destSet[fk] {
			exited = append(exited, fk)
		}
	}
	return entered, exited, nil
}

func (s *Store) Move(ctx context.Context, p MoveParams) error {
	if p.SubjectRef == "" {
		return &ValidationError{Detail: "subject_ref is required"}
	}

	// Auto-derive tree-aligned fence membership when the caller hasn't
	// explicitly supplied it. Explicit non-empty EnteredFenceKeys/
	// ExitedFenceKeys remains Stage 2's own test hook for exercising
	// the multi-target CAS guard directly against arbitrary fences —
	// every existing test that relies on it is unaffected, since this
	// only fires when both are empty. Real callers (the dxp adapter,
	// the HTTP handler) never supply these, so they always get real
	// tree-alignment-derived membership, closing the gap Stage 5's own
	// DxpMoveParams doc comment named explicitly.
	if len(p.EnteredFenceKeys) == 0 && len(p.ExitedFenceKeys) == 0 {
		entered, exited, err := s.TreeAlignedFenceDelta(ctx, p.SubjectRef, p.ToLocationID)
		if err != nil {
			return err
		}
		p.EnteredFenceKeys = entered
		p.ExitedFenceKeys = exited
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := s.moveInTx(ctx, tx, p); err != nil {
		return err
	}
	return tx.Commit()
}

// moveInTx is Move's guarded core, extracted so a dxp coordinator
// (Stage 5, T-118) can drive it against an externally-supplied, shared
// transaction — mirroring bal.transferInTx's own extraction exactly
// (pkg/bal/store.go's doc explains the general shape: does NOT commit,
// caller owns that; returns the resolved destination key so the
// caller — Execute — can use it without a second lookup). Multi-target
// atomicity: one destination leaf plus every entered/exited fence, all
// guarded in one transaction — the first zero-rows-affected CAS
// refuses the whole move (rollback), never a partial application.
// Decision lives inside each UPDATE's predicate, rows-affected is the
// verdict — never read-then-decide-then-write (the house discipline,
// T-34).
//
// Write-first, mirroring bal.transferInTx exactly: the leaf entry CAS
// is the transaction's opening statement, resolving location_id via a
// subquery rather than a preceding SELECT. A read-first shape (SELECT
// the key, then UPDATE) hits WAL's snapshot invalidation under real
// concurrency — confirmed directly, not assumed: an early version of
// this function did exactly that, and a sandbox race pass surfaced
// SQLITE_BUSY errors under 32-way contention before this fix.
func (s *Store) moveInTx(ctx context.Context, tx *sql.Tx, p MoveParams) (toKey int64, err error) {
	// Leaf entry — the transaction's first statement, a write.
	err = tx.QueryRowContext(ctx,
		`UPDATE loc_capacity
		   SET count = count + 1
		 WHERE location_key = (SELECT location_key FROM `+s.locationsTable()+` WHERE location_id = ?)
		   AND (ceiling IS NULL OR count + 1 <= ceiling)
		 RETURNING location_key`,
		p.ToLocationID).Scan(&toKey)
	if err == sql.ErrNoRows {
		return 0, s.diagnoseLeafRefusal(ctx, tx, p.ToLocationID)
	}
	if err != nil {
		return 0, err
	}

	// Now a writer: resolving the previous assignment via a plain
	// SELECT is safe here — no WAL upgrade risk, we're already past it.
	var fromKey sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT location_key FROM loc_assignment WHERE subject_ref = ?`, p.SubjectRef).Scan(&fromKey); err != nil && err != sql.ErrNoRows {
		return 0, err
	}

	// Exit the previous leaf, if any. A zero-rows result here is an
	// invariant violation, not a normal refusal: the assignment row
	// says the subject was at fromKey, so loc_capacity must have a
	// matching count > 0 row for it, or bookkeeping already drifted.
	if fromKey.Valid {
		res, err := tx.ExecContext(ctx, leafExitCAS, fromKey.Int64)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, &InvariantError{Detail: fmt.Sprintf("leaf exit found no matching capacity row for location_key %d", fromKey.Int64)}
		}
	}

	// Exit every named fence (invariant violation on zero rows, same
	// reasoning as the leaf exit above).
	for _, fk := range p.ExitedFenceKeys {
		res, err := tx.ExecContext(ctx, fenceExitCAS, uint32(fk))
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, &InvariantError{Detail: fmt.Sprintf("fence exit found no matching capacity row for fence_key %d", fk)}
		}
	}

	// Enter every named fence — a normal refusal on zero rows, same as
	// the leaf entry: rolls back everything above, not a partial
	// application. This is the multi-target atomicity rule under test:
	// the leaf CAS that already succeeded as this transaction's first
	// statement is undone by the same rollback a fence CAS failure
	// triggers.
	for _, fk := range p.EnteredFenceKeys {
		res, err := tx.ExecContext(ctx, fenceEntryCAS, uint32(fk))
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, &CapacityError{Kind: "fence", Key: uint32(fk)}
		}
	}

	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO loc_assignment (subject_ref, location_key, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(subject_ref) DO UPDATE SET location_key = excluded.location_key, updated_at = excluded.updated_at`,
		p.SubjectRef, toKey, now); err != nil {
		return 0, err
	}

	// Journal stub (Stage 4, T-117, makes the fold-oracle real; this
	// stage needs only that exactly one row lands per move).
	enteredJSON, _ := json.Marshal(p.EnteredFenceKeys)
	exitedJSON, _ := json.Marshal(p.ExitedFenceKeys)
	var fromKeyArg interface{}
	if fromKey.Valid {
		fromKeyArg = fromKey.Int64
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO loc_journal (subject_ref, kind, from_location_key, to_location_key, entered_fence_keys, exited_fence_keys, at)
		 VALUES (?, 'move', ?, ?, ?, ?, ?)`,
		p.SubjectRef, fromKeyArg, toKey, string(enteredJSON), string(exitedJSON), now); err != nil {
		return 0, err
	}

	return toKey, nil
}
