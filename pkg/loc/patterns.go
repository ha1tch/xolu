// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// patterns.go — T-131 (wave 9b), loc-00-design.md §5d: fence-type
// patterns, the flat definitional-record mechanism many fences (or
// locations) of a shared kind can clone a capacity default from at
// creation time. Cloned child, lineage pointer, computed
// pattern_deleted — the same shape fsm/dxp's own definition-snapshot
// pattern already proves, applied a fourth time (obj-01-rest-api.md
// §4a is the third). A pattern changing later never retroactively
// touches an already-cloned fence or location: the clone snapshots
// the pattern's capacity once, at Def/attach time, and keeps it
// regardless of what the source pattern does afterward.
//
// Deliberately not offered here, per loc-01-rest-api.md §2a's own
// text: obj's `extract`/`pattern_after` forms. Every loc pattern is
// drafted from scratch via `def`; no real need for the other two
// forms has been named yet.

package loc

import (
	"context"
	"database/sql"
	"errors"

	"modernc.org/sqlite"
)

// Pattern is a fence-or-location capacity default, addressed by its
// own caller-chosen id — the same declare-at-known-id convention
// location_id/fence_id already use, not a separate auto-assigned key.
type Pattern struct {
	ID       string
	Capacity int64
}

// DefPattern creates a pattern. XOLU-LOC023 if the id is already
// defined — write-first (INSERT...RETURNING, the sqliteConstraintUnique
// check on the actual write), not a read-first existence check, for
// the identical WAL-race reason Def/DefFence/DefineAccount already
// learned this the hard way (T-115, T-125).
func (s *Store) DefPattern(ctx context.Context, id string, capacity int64) error {
	if id == "" {
		return &ValidationError{Detail: "pattern id is required"}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO loc_patterns (pattern_id, capacity) VALUES (?, ?)`, id, capacity)
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) &&
			(sqliteErr.Code() == sqliteConstraintUnique || sqliteErr.Code() == sqliteConstraintPrimaryKey) {
			return &DuplicatePatternError{PatternID: id}
		}
		return err
	}
	return nil
}

// GetPattern fetches one pattern. XOLU-LOC024 if unknown.
func (s *Store) GetPattern(ctx context.Context, id string) (*Pattern, error) {
	var p Pattern
	p.ID = id
	if err := s.db.QueryRowContext(ctx,
		`SELECT capacity FROM loc_patterns WHERE pattern_id = ?`, id).Scan(&p.Capacity); err != nil {
		if err == sql.ErrNoRows {
			return nil, &UnknownPatternError{PatternID: id}
		}
		return nil, err
	}
	return &p, nil
}

// ListPatterns returns every pattern, ordered by id for a stable
// listing (patterns have no dense internal key the way locations/
// fences do — pattern_id is the only identity there is).
func (s *Store) ListPatterns(ctx context.Context) ([]Pattern, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pattern_id, capacity FROM loc_patterns ORDER BY pattern_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Pattern
	for rows.Next() {
		var p Pattern
		if err := rows.Scan(&p.ID, &p.Capacity); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePattern removes a pattern definition outright — no cascade
// refusal, mirroring obj-01-rest-api.md §4a's own DELETE exactly:
// already-cloned fences/locations keep their own snapshotted capacity
// regardless; only their next GET's computed pattern_deleted reflects
// the change. XOLU-LOC024 if unknown (matches DELETE's usual not-found
// shape elsewhere in this package, e.g. UnknownFenceError).
func (s *Store) DeletePattern(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM loc_patterns WHERE pattern_id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &UnknownPatternError{PatternID: id}
	}
	return nil
}

// ApplyLocationPattern clones patternID's current capacity onto an
// already-`Def`d location, recording the lineage pointer — one-time,
// at creation, never re-applied later (loc-00-design.md §5d's own
// "changing a pattern later never retroactively touches already-
// cloned" rule). XOLU-LOC003 if locationID is unknown, XOLU-LOC024 if
// patternID is unknown, XOLU-LOC011 if the location is non-postable
// (the same rule an inline capacity already enforces — a pattern is
// not a way around it).
func (s *Store) ApplyLocationPattern(ctx context.Context, locationID, patternID string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var locKey int64
	var postable int
	if err := tx.QueryRowContext(ctx,
		`SELECT location_key, postable FROM `+s.locationsTable()+` WHERE location_id = ?`, locationID).
		Scan(&locKey, &postable); err != nil {
		if err == sql.ErrNoRows {
			return 0, &UnknownLocationError{LocationID: locationID}
		}
		return 0, err
	}
	if postable == 0 {
		return 0, &CapacityOnNonPostableError{LocationID: locationID}
	}

	var capacity int64
	if err := tx.QueryRowContext(ctx,
		`SELECT capacity FROM loc_patterns WHERE pattern_id = ?`, patternID).Scan(&capacity); err != nil {
		if err == sql.ErrNoRows {
			return 0, &UnknownPatternError{PatternID: patternID}
		}
		return 0, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE loc_capacity SET ceiling = ?, pattern_id = ? WHERE location_key = ?`,
		capacity, patternID, locKey); err != nil {
		return 0, err
	}
	return capacity, tx.Commit()
}

// ApplyFencePattern is ApplyLocationPattern's fence-shaped sibling.
// XOLU-LOC004 if fenceID is unknown, XOLU-LOC024 if patternID is
// unknown.
func (s *Store) ApplyFencePattern(ctx context.Context, fenceID, patternID string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var fenceKey int64
	if err := tx.QueryRowContext(ctx,
		`SELECT fence_key FROM fences WHERE fence_id = ?`, fenceID).Scan(&fenceKey); err != nil {
		if err == sql.ErrNoRows {
			return 0, &UnknownFenceError{FenceID: fenceID}
		}
		return 0, err
	}

	var capacity int64
	if err := tx.QueryRowContext(ctx,
		`SELECT capacity FROM loc_patterns WHERE pattern_id = ?`, patternID).Scan(&capacity); err != nil {
		if err == sql.ErrNoRows {
			return 0, &UnknownPatternError{PatternID: patternID}
		}
		return 0, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE loc_fence_capacity SET ceiling = ?, pattern_id = ? WHERE fence_key = ?`,
		capacity, patternID, fenceKey); err != nil {
		return 0, err
	}
	return capacity, tx.Commit()
}

// FenceOrLocationPatternInfo is the read-side shape §2a's own GET
// response needs: pattern (the id, echoing what was supplied),
// pattern_id (the same value again, under the "_id" naming this
// package's other identity fields use), and a computed
// pattern_deleted — recomputed on every read, never cached or
// stored, per §5c's own recompute-and-compare precedent this
// mechanism reuses directly.
type PatternLineage struct {
	PatternID      string
	PatternDeleted bool
}

// LocationPatternLineage returns nil when the location was never
// cloned from a pattern — the common case, and the response builder's
// own signal to omit all three pattern fields entirely rather than
// emit them null.
func (s *Store) LocationPatternLineage(ctx context.Context, locationKey LocationKey) (*PatternLineage, error) {
	var patternID sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT pattern_id FROM loc_capacity WHERE location_key = ?`, uint32(locationKey)).Scan(&patternID); err != nil {
		return nil, err
	}
	if !patternID.Valid {
		return nil, nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM loc_patterns WHERE pattern_id = ?`, patternID.String).Scan(&exists); err != nil {
		return nil, err
	}
	return &PatternLineage{PatternID: patternID.String, PatternDeleted: exists == 0}, nil
}

// FencePatternLineage is LocationPatternLineage's fence-shaped sibling.
func (s *Store) FencePatternLineage(ctx context.Context, fenceKey FenceKey) (*PatternLineage, error) {
	var patternID sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT pattern_id FROM loc_fence_capacity WHERE fence_key = ?`, uint32(fenceKey)).Scan(&patternID); err != nil {
		return nil, err
	}
	if !patternID.Valid {
		return nil, nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM loc_patterns WHERE pattern_id = ?`, patternID.String).Scan(&exists); err != nil {
		return nil, err
	}
	return &PatternLineage{PatternID: patternID.String, PatternDeleted: exists == 0}, nil
}
