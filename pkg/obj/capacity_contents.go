// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// capacity_contents.go — T-124 (wave 10), Stage 7: the two remaining
// obj-01-rest-api.md endpoints with real store-level logic of their
// own — capacity update (§4) and contents (§3). Retire (§6) already
// shipped at the store level under T-122 (retire.go); this file adds
// no new retire logic, only capacity_contents_test.go's own coverage
// alongside it.

package obj

import "context"

// SetCapacity updates subjectRef's own capacity ceilings (§4).
// XOLU-OBJ008 if every dimension would end up unset — a subject with
// no capacity dimension at all cannot hold anything, matching /loc's
// own "capacity set on a non-postable node" refusal shape.
func (s *Store) SetCapacity(ctx context.Context, subjectRef string, capacity Capacity) error {
	if capacity.MaxWeightKg == nil && capacity.MaxVolumeM3 == nil && capacity.MaxCount == nil {
		return &CapacityInvalidError{SubjectRef: subjectRef}
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE obj_subjects SET max_weight_kg = ?, max_volume_m3 = ?, max_count = ?
		WHERE subject_ref = ?`,
		capacity.MaxWeightKg, capacity.MaxVolumeM3, capacity.MaxCount, subjectRef)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &NotAttachedError{SubjectRef: subjectRef}
	}
	return nil
}

// DirectContents returns every subject directly contained by
// containerRef (obj-01-rest-api.md §3's own default, no ?depth=all) —
// a plain SQL read against obj_position.contains_ref, guard-free,
// never the graph (obj-00-design.md §10's own guard-locality rule
// extends to reads too: the graph is a mirror, /obj's own SQL is
// canonical, and canonical is what an ordinary GET should answer
// from). Confirms containerRef itself is attached first — an unknown
// or never-attached container should read as XOLU-OBJ001, not a
// confusing empty list.
func (s *Store) DirectContents(ctx context.Context, containerRef string) ([]string, error) {
	if _, err := s.Get(ctx, containerRef); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT subject_ref FROM obj_position WHERE contains_ref = ? ORDER BY subject_ref`, containerRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// TransitiveContents walks the full containment closure below
// containerRef (?depth=all) via the identical SQL read DirectContents
// uses, applied recursively — deliberately NOT the mirrored graph
// (obj-00-design.md §10 names FindPath/GetNeighbors against the live
// graph as the closure-query mechanism once mirrored, but the mirror
// is best-effort and can lag; a subject's own transitive contents,
// asked of /obj directly rather than of the graph, should answer from
// the SAME canonical source DirectContents already does, not risk a
// stale answer from a derived plane for a query this primitive can
// already serve exactly from its own tables). visited guards against
// a corrupted/cyclic state defensively — the write-time cycle guard
// (containment.go) makes a real cycle impossible for legitimately-
// written data, so hitting the guard here means an invariant was
// already violated elsewhere, not a normal operating condition.
func (s *Store) TransitiveContents(ctx context.Context, containerRef string) ([]string, error) {
	visited := make(map[string]bool)
	var out []string
	var walk func(ref string) error
	walk = func(ref string) error {
		direct, err := s.DirectContents(ctx, ref)
		if err != nil {
			return err
		}
		for _, child := range direct {
			if visited[child] {
				continue // defensive: see doc comment above
			}
			visited[child] = true
			out = append(out, child)
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(containerRef); err != nil {
		return nil, err
	}
	return out, nil
}
