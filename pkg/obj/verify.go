// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// verify.go — T-122 (wave 10): obj's own journal + rebuild oracle,
// mirroring pkg/loc's own AssignmentFoldOracle shape
// (pkg/loc/verify.go) exactly where the two primitives' semantics
// line up, and diverging deliberately where they don't.
//
// The one genuine divergence from loc's own pattern: obj has TWO
// distinct terminal outcomes for a subject, not one. Detach
// (store.go) DELETES the subject's row entirely (obj-01-rest-api.md
// §1's own "bookkeeping cleanup" framing) — a detached subject has no
// current state to fold at all. Retire (retire.go) does NOT delete
// the row (§12's own "closer in shape to a bal account closure" than
// to deletion) — a retired subject's position row persists exactly as
// it was at the moment of retirement, still fully present in
// obj_position, still fold-checkable. The oracle below has to find
// each subject's latest POSITION-bearing entry (attach/move) for the
// derive side, but only include a subject at all if its OVERALL
// latest entry isn't "detach" — retire does not exclude a subject
// from the fold, detach does. Getting this backwards (excluding both
// detach AND retire from the derive side, the naive mirror of loc's
// own single-outcome shape) would silently mismatch every retired
// subject against Current, which still has a real row for it.

package obj

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

// PositionFoldOracle: derive(journal) == current for every subject's
// position — including retired subjects (whose position persists
// unchanged), excluding detached ones (whose row is gone). Derive is
// each subject's latest 'attach'/'move' entry, gated on that
// subject's overall latest journal entry not being 'detach'. Current
// reads obj_position directly. Both sides fingerprint as sorted
// "subject_ref position_kind loc_leaf_id_or_dash container_ref_or_dash"
// lines.
func (s *Store) PositionFoldOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name: "obj.position.fold",
		Derive: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx, `
				WITH latest_position AS (
					SELECT subject_ref, position_kind, loc_leaf_id, container_ref,
					       ROW_NUMBER() OVER (PARTITION BY subject_ref ORDER BY entry_id DESC) AS rn
					FROM obj_journal WHERE kind IN ('attach', 'move')
				),
				latest_overall AS (
					SELECT subject_ref, kind,
					       ROW_NUMBER() OVER (PARTITION BY subject_ref ORDER BY entry_id DESC) AS rn
					FROM obj_journal
				)
				SELECT lp.subject_ref, lp.position_kind, lp.loc_leaf_id, lp.container_ref
				FROM latest_position lp
				JOIN latest_overall lo ON lo.subject_ref = lp.subject_ref AND lo.rn = 1
				WHERE lp.rn = 1 AND lo.kind != 'detach'`)
			if err != nil {
				return "", err
			}
			defer rows.Close()
			return foldFingerprint(rows)
		},
		Current: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx,
				`SELECT subject_ref, kind, loc_leaf_id, contains_ref FROM obj_position`)
			if err != nil {
				return "", err
			}
			defer rows.Close()
			return foldFingerprint(rows)
		},
	}
}

// foldFingerprint scans (subject_ref, position_kind, loc_leaf_id,
// container_ref) rows — the identical four-column shape both
// Derive's own query and Current's own obj_position read produce —
// into sorted, comparable lines. Shared so the two sides can never
// silently drift in how they format the same fact.
func foldFingerprint(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}) (string, error) {
	var lines []string
	for rows.Next() {
		var subj, kind string
		var locLeafID, containerRef *string
		if err := rows.Scan(&subj, &kind, &locLeafID, &containerRef); err != nil {
			return "", err
		}
		leaf := "-"
		if locLeafID != nil {
			leaf = *locLeafID
		}
		container := "-"
		if containerRef != nil {
			container = *containerRef
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s", subj, kind, leaf, container))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

// Oracles returns every fold oracle this store owns — the entry point
// a rebuild-check caller (package-internal today; iolu's own obj
// subcommand is wave 6's own job, still 0% built as of this writing,
// matching loc's identical T-117 note) uses to verify derive(journal)
// == current.
func (s *Store) Oracles() []chronicle.RebuildOracle {
	return []chronicle.RebuildOracle{s.PositionFoldOracle()}
}
