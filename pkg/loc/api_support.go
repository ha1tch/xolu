// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// FenceIDsFor resolves internal fence keys to their external fence_id
// strings — the two-identity split (§11a) applied to Stage 6's own
// response building: entered/exited/fences in every HTTP response are
// always fence_id, never a FenceKey. Order-preserving (matches the
// order keys were supplied in, not a set), and empty input returns an
// empty, non-nil slice — every wire response wants `[]`, never `null`,
// for an empty fence list.
func (s *Store) FenceIDsFor(ctx context.Context, keys []FenceKey) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, fk := range keys {
		var id string
		if err := s.db.QueryRowContext(ctx, `SELECT fence_id FROM fences WHERE fence_key = ?`, uint32(fk)).Scan(&id); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("loc invariant violation: fence_key %d referenced but not found", fk)
			}
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// CurrentFenceKeys returns a subject's current fence membership —
// loc_fence_membership read directly, the same live-tracked-not-
// derived state Report itself maintains (Stage 3).
func (s *Store) CurrentFenceKeys(ctx context.Context, subjectRef string) ([]FenceKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fence_key FROM loc_fence_membership WHERE subject_ref = ?`, subjectRef)
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

// SubjectPosition is the canonical-state response loc-01-rest-api.md
// §3's GET .../position describes: leaf is nil for a subject only
// ever report-tracked, LastReportPoint is nil for one only ever
// moved — both nil is a subject never referenced by either verb.
type SubjectPosition struct {
	Leaf            *string
	Fences          []string
	LastReportPoint *ReportPoint
	AsOf            *string // RFC3339; nil only when Leaf and LastReportPoint are both nil
}

type ReportPoint struct {
	Lat, Lon, Alt float64
}

// SubjectPosition resolves current canonical state directly from
// loc_assignment (leaf), loc_fence_membership (fences), and
// loc_journal's own most recent report row (last_report_point) —
// never re-derived from a fold, matching this package's own §8a
// distinction between live-tracked current state and the rebuild
// oracle's separate (Stage 4) verification role.
func (s *Store) SubjectPosition(ctx context.Context, subjectRef string) (SubjectPosition, error) {
	var pos SubjectPosition

	var leafKey sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT location_key FROM loc_assignment WHERE subject_ref = ?`, subjectRef).Scan(&leafKey); err != nil && err != sql.ErrNoRows {
		return pos, err
	}
	if leafKey.Valid {
		var leafID string
		if err := s.db.QueryRowContext(ctx, `SELECT location_id FROM `+s.locationsTable()+` WHERE location_key = ?`, leafKey.Int64).Scan(&leafID); err != nil {
			return pos, err
		}
		pos.Leaf = &leafID
	}

	fenceKeys, err := s.CurrentFenceKeys(ctx, subjectRef)
	if err != nil {
		return pos, err
	}
	pos.Fences, err = s.FenceIDsFor(ctx, fenceKeys)
	if err != nil {
		return pos, err
	}

	var reportLat, reportLon, reportAlt sql.NullFloat64
	var reportAt sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT report_lat, report_lon, report_alt, at FROM loc_journal
		WHERE subject_ref = ? AND kind = 'report' AND report_lat IS NOT NULL
		ORDER BY entry_id DESC LIMIT 1`, subjectRef).Scan(&reportLat, &reportLon, &reportAlt, &reportAt)
	if err != nil && err != sql.ErrNoRows {
		return pos, err
	}
	if err == nil {
		pos.LastReportPoint = &ReportPoint{Lat: reportLat.Float64, Lon: reportLon.Float64, Alt: reportAlt.Float64}
	}

	// as_of: the more recent of the last move and the last report,
	// whichever exists.
	var asOf sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(at) FROM loc_journal WHERE subject_ref = ?`, subjectRef).Scan(&asOf); err != nil {
		return pos, err
	}
	if asOf.Valid {
		pos.AsOf = &asOf.String
	}
	return pos, nil
}

// HistoryEntry is one loc_journal row, resolved back to external ids
// — the two-identity split applied to history reads exactly as to
// every other response.
type HistoryEntry struct {
	At      string
	Kind    string
	From    *string // move only
	To      *string // move only
	Entered []string // report only (or a tree-aligned move, which also carries fence deltas)
	Exited  []string
}

// SubjectHistory returns a subject's movement journal, newest first,
// limited to at most limit rows — loc-01-rest-api.md §3's own
// pagination contract; this package's own v1 scope stops at a single
// page (no cursor support yet), the same v1 non-goal boundary this
// project draws elsewhere rather than half-building pagination.
func (s *Store) SubjectHistory(ctx context.Context, subjectRef string, limit int) ([]HistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT at, kind, from_location_key, to_location_key, entered_fence_keys, exited_fence_keys
		FROM loc_journal WHERE subject_ref = ? ORDER BY entry_id DESC LIMIT ?`, subjectRef, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HistoryEntry
	for rows.Next() {
		var at, kind, enteredJSON, exitedJSON string
		var fromKey, toKey sql.NullInt64
		if err := rows.Scan(&at, &kind, &fromKey, &toKey, &enteredJSON, &exitedJSON); err != nil {
			return nil, err
		}
		entry := HistoryEntry{At: at, Kind: kind}
		if fromKey.Valid {
			id, err := s.locationIDForKey(ctx, fromKey.Int64)
			if err != nil {
				return nil, err
			}
			entry.From = &id
		}
		if toKey.Valid {
			id, err := s.locationIDForKey(ctx, toKey.Int64)
			if err != nil {
				return nil, err
			}
			entry.To = &id
		}
		enteredKeys, err := decodeFenceKeyJSON(enteredJSON)
		if err != nil {
			return nil, err
		}
		exitedKeys, err := decodeFenceKeyJSON(exitedJSON)
		if err != nil {
			return nil, err
		}
		entry.Entered, err = s.FenceIDsFor(ctx, enteredKeys)
		if err != nil {
			return nil, err
		}
		entry.Exited, err = s.FenceIDsFor(ctx, exitedKeys)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *Store) locationIDForKey(ctx context.Context, key int64) (string, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT location_id FROM `+s.locationsTable()+` WHERE location_key = ?`, key).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("loc invariant violation: location_key %d referenced but not found", key)
		}
		return "", err
	}
	return id, nil
}

func decodeFenceKeyJSON(raw string) ([]FenceKey, error) {
	var vals []uint32
	if err := json.Unmarshal([]byte(raw), &vals); err != nil {
		return nil, err
	}
	out := make([]FenceKey, len(vals))
	for i, v := range vals {
		out[i] = FenceKey(v)
	}
	return out, nil
}

// ─── Nearby ───────────────────────────────────────────────────────────

type NearbyLocation struct {
	LocationID string
	DistanceM  float64
}

type NearbyFence struct {
	FenceID   string
	DistanceM float64
}

// Nearby answers "what's near this point" (loc-01-rest-api.md §4) —
// a read convenience, never a guard input (§7d): advisory distance
// ordering, not a correctness-bearing containment test the way
// ResolveFenceMembership is. Locations: every postable leaf's
// resolved absolute position (ComposeAbsolutePosition, placement.go)
// within radius, sorted nearest first — a full scan, not R-tree
// pre-filtered, since locations carry no bounding-box index the way
// fences do (a real v1 simplicity, acceptable for the "hundreds to
// low thousands" scale this package targets, per loc-00-design.md
// §6's own scale statement). Fences: the same R-tree pre-filter
// ResolveFenceMembership uses, widened by radius, then the exact
// Distance() test for true ordering.
func (s *Store) Nearby(ctx context.Context, lat, lon, radiusMeters float64) ([]NearbyLocation, []NearbyFence, error) {
	locs, err := s.nearbyLocations(ctx, lat, lon, radiusMeters)
	if err != nil {
		return nil, nil, err
	}
	fences, err := s.nearbyFences(ctx, lat, lon, radiusMeters)
	if err != nil {
		return nil, nil, err
	}
	return locs, fences, nil
}

func (s *Store) nearbyLocations(ctx context.Context, lat, lon, radiusMeters float64) ([]NearbyLocation, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []NearbyLocation
	for _, l := range all {
		if !l.Postable {
			continue
		}
		pos, err := s.ComposeAbsolutePosition(ctx, l.ID)
		if err != nil {
			continue // an unreachable/malformed placement chain is skipped, not fatal to the whole query
		}
		d := haversineMeters(lat, lon, pos.Lat, pos.Lon)
		if d <= radiusMeters {
			out = append(out, NearbyLocation{LocationID: l.ID, DistanceM: d})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DistanceM < out[j].DistanceM })
	return out, nil
}

func (s *Store) nearbyFences(ctx context.Context, lat, lon, radiusMeters float64) ([]NearbyFence, error) {
	dLat := radiusMeters / metresPerDegreeLat
	metresPerDegreeLon := metresPerDegreeLat * math.Cos(lat*math.Pi/180.0)
	// Magnitude, not a signed direction — this is a symmetric search
	// box around (lat,lon), the same reasoning as Circle.BoundingBox's
	// own use of this helper.
	dLon := math.Abs(safeLongitudeDelta(radiusMeters, metresPerDegreeLon))
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM fences_rtree WHERE minLat <= ? AND maxLat >= ? AND minLon <= ? AND maxLon >= ?`,
		lat+dLat, lat-dLat, lon+dLon, lon-dLon)
	if err != nil {
		return nil, err
	}
	var candidates []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, id)
	}
	rows.Close()

	var out []NearbyFence
	for _, fk := range candidates {
		geom, err := loadFenceGeometry(ctx, s.db, fk)
		if err != nil {
			return nil, err
		}
		if geom == nil {
			continue
		}
		d := geom.Distance(lat, lon)
		if geom.Contains(lat, lon) {
			d = 0 // "0.0 when the point is inside the shape" (§4's own contract)
		}
		if d <= radiusMeters {
			var id string
			if err := s.db.QueryRowContext(ctx, `SELECT fence_id FROM fences WHERE fence_key = ?`, fk).Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, NearbyFence{FenceID: id, DistanceM: d})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DistanceM < out[j].DistanceM })
	return out, nil
}
