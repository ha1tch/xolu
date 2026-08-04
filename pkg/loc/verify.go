// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

// loc's own rebuild oracles (loc-02-implementation.md Stage 4, T-117):
// bal's §8 fold pattern, applied to loc's own targets. loc has no
// equivalent of bal's *local chain* verification (previous+amount=
// current per entry, bal-conservation-primitive.md §8) — a move's
// journal entry carries no running total the way a ledger entry does.
// loc's oracle is global-fold-only; that asymmetry is stated here
// rather than left as a silently thinner verification story than
// bal's.

// AssignmentFoldOracle: derive(journal) == current for leaf
// assignment. Derive is each subject's most recent 'move' entry
// (ROW_NUMBER partitioned by subject_ref, ordered by entry_id desc);
// Current reads loc_assignment directly. Both sides fingerprint as
// sorted "subject_ref location_key" lines.
func (s *Store) AssignmentFoldOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name: "loc.assignment.fold",
		Derive: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx, `
				SELECT subject_ref, to_location_key FROM (
					SELECT subject_ref, to_location_key,
					       ROW_NUMBER() OVER (PARTITION BY subject_ref ORDER BY entry_id DESC) AS rn
					FROM loc_journal WHERE kind = 'move'
				) WHERE rn = 1`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var subj string
				var key int64
				if err := rows.Scan(&subj, &key); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s %d", subj, key))
			}
			if err := rows.Err(); err != nil {
				return "", err
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), nil
		},
		Current: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx,
				`SELECT subject_ref, location_key FROM loc_assignment WHERE location_key IS NOT NULL`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var subj string
				var key int64
				if err := rows.Scan(&subj, &key); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s %d", subj, key))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), rows.Err()
		},
	}
}

// OccupancyFoldOracle: derive(journal) == current for leaf occupancy
// counts. Derive folds the same "last move per subject" view as
// AssignmentFoldOracle, grouped by destination leaf; Current reads
// loc_capacity.count. Locations with zero derived occupants are
// omitted from Derive symmetrically with Current (bal's own
// GlobalFoldOracle documents the identical convention) — a
// never-occupied or fully-vacated location has no row on either side,
// not a "0" row on one side and an absent row on the other.
func (s *Store) OccupancyFoldOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name: "loc.occupancy.fold",
		Derive: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx, `
				SELECT to_location_key, COUNT(*) FROM (
					SELECT subject_ref, to_location_key,
					       ROW_NUMBER() OVER (PARTITION BY subject_ref ORDER BY entry_id DESC) AS rn
					FROM loc_journal WHERE kind = 'move'
				) WHERE rn = 1
				GROUP BY to_location_key`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var key, count int64
				if err := rows.Scan(&key, &count); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%d %d", key, count))
			}
			if err := rows.Err(); err != nil {
				return "", err
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), nil
		},
		Current: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx, `SELECT location_key, count FROM loc_capacity WHERE count > 0`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var key, count int64
				if err := rows.Scan(&key, &count); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%d %d", key, count))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), rows.Err()
		},
	}
}

// fenceMembershipFoldSQL is shared by FenceMembershipFoldOracle and
// FenceOccupancyFoldOracle: fence membership has no "last value wins"
// shape the way leaf assignment does (a subject enters and exits the
// SAME fence repeatedly across both move and report activity) — it is
// a genuine net fold of +1-on-entry/-1-on-exit deltas per
// (subject_ref, fence_key) pair, unnested from each journal row's
// JSON arrays via json_each. A pair with net <= 0 is not currently a
// member and is correctly absent from both derive and current sides.
const fenceMembershipFoldSQL = `
	WITH deltas AS (
		SELECT subject_ref, je.value AS fence_key, 1 AS delta
		FROM loc_journal, json_each(entered_fence_keys) je
		UNION ALL
		SELECT subject_ref, je.value AS fence_key, -1 AS delta
		FROM loc_journal, json_each(exited_fence_keys) je
	)
	SELECT subject_ref, fence_key, SUM(delta) AS net
	FROM deltas GROUP BY subject_ref, fence_key
	HAVING SUM(delta) > 0`

// FenceMembershipFoldOracle: derive(journal) == current for fence
// membership, extending Stage 4's own leaf-shaped pattern to the
// fence-shaped state Stage 3 (T-116) introduced — the plan's own SQL
// targets cover leaf assignment/occupancy explicitly; this is the
// same discipline applied to loc_fence_membership, not a narrower
// verification story for fences than for leaves.
func (s *Store) FenceMembershipFoldOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name: "loc.fence_membership.fold",
		Derive: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx, fenceMembershipFoldSQL)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var subj string
				var fk, net int64
				if err := rows.Scan(&subj, &fk, &net); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s %d", subj, fk))
			}
			if err := rows.Err(); err != nil {
				return "", err
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), nil
		},
		Current: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx, `SELECT subject_ref, fence_key FROM loc_fence_membership`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var subj string
				var fk int64
				if err := rows.Scan(&subj, &fk); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%s %d", subj, fk))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), rows.Err()
		},
	}
}

// FenceOccupancyFoldOracle: derive(journal) == current for fence
// capacity counts, the fence-shaped counterpart to OccupancyFoldOracle.
func (s *Store) FenceOccupancyFoldOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name: "loc.fence_occupancy.fold",
		Derive: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx,
				`SELECT fence_key, COUNT(*) FROM (`+fenceMembershipFoldSQL+`) GROUP BY fence_key`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var fk, count int64
				if err := rows.Scan(&fk, &count); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%d %d", fk, count))
			}
			if err := rows.Err(); err != nil {
				return "", err
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), nil
		},
		Current: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx, `SELECT fence_key, count FROM loc_fence_capacity WHERE count > 0`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var fk, count int64
				if err := rows.Scan(&fk, &count); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%d %d", fk, count))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), rows.Err()
		},
	}
}

// Oracles returns every rebuild oracle this package defines — the
// hook point for iolu db check (loc-02-implementation.md Stage 4),
// matching ts/cal/bal's own oracle-registration shape. iolu itself
// (wave 6) is still 0% built as of this writing, so this is the hook
// only, not a shipped CLI surface, per Stage 0's own decision to keep
// iolu wiring out of scope.
func (s *Store) Oracles() []chronicle.RebuildOracle {
	return []chronicle.RebuildOracle{
		s.AssignmentFoldOracle(),
		s.OccupancyFoldOracle(),
		s.FenceMembershipFoldOracle(),
		s.FenceOccupancyFoldOracle(),
	}
}
