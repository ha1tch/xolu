// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

// ---------------------------------------------------------------------------
// Edge FTS — Stage 8
// ---------------------------------------------------------------------------
//
// t<X>_efts is an FTS5 virtual table with three columns:
//   rel      UNINDEXED  — relationship label (not tokenised, used for filtering)
//   edge_id  UNINDEXED  — surrogate ID from t<X>_eseq (not tokenised)
//   content               — searchable text extracted from the property blob
//
// IndexEdgeContent is called:
//   - by AddEdgeWithProps after the property blob/adapted row is written
//   - by RegisterAdaptedEdge does NOT index (no content yet at registration time)
//
// Content extraction: string property values are concatenated into a
// space-separated string. Non-string values (numbers, booleans) are converted
// to their string representation and included so they are searchable.
//
// The (rel, edge_id) pair is used as a logical key via DELETE + INSERT to
// provide idempotent re-indexing without a UNIQUE constraint (FTS5 virtual
// tables do not support UNIQUE indexes).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// ---------------------------------------------------------------------------
// Content extraction
// ---------------------------------------------------------------------------

// extractEdgeContent converts a property map into a flat, space-separated
// string suitable for FTS5 indexing. String values are included directly;
// numeric and boolean values are rendered as strings. Nested objects and
// arrays are JSON-marshalled. Keys are also indexed so property names are
// searchable.
func extractEdgeContent(props map[string]interface{}) string {
	if len(props) == 0 {
		return ""
	}
	var parts []string
	for k, v := range props {
		parts = append(parts, k)
		switch val := v.(type) {
		case string:
			parts = append(parts, val)
		case float64:
			parts = append(parts, fmt.Sprintf("%g", val))
		case int:
			parts = append(parts, fmt.Sprintf("%d", val))
		case int64:
			parts = append(parts, fmt.Sprintf("%d", val))
		case bool:
			if val {
				parts = append(parts, "true")
			} else {
				parts = append(parts, "false")
			}
		case nil:
			// skip
		default:
			if b, err := json.Marshal(val); err == nil {
				parts = append(parts, string(b))
			}
		}
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// EdgeFTSStore implementation
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// EdgeLister implementation
// ---------------------------------------------------------------------------

// IsEdgeLabel reports whether rel is a registered edge label (either adapted
// with a t<X>_edata_<label> table, or blob-only registered in t<X>_e_sch).
// The lookup is case-insensitive so that OQL's entity-name normalisation
// (which lowercases FROM <Label> to "label") matches labels registered with
// any casing (e.g. "KNOWS", "Knows", "knows" all resolve correctly).
func (s *SQLiteStore) IsEdgeLabel(ctx context.Context, rel string) (bool, error) {
	// Check adapted registry (case-sensitive as stored).
	// Try both the provided name and the uppercase variant since OQL normalises
	// to lowercase but adapted labels may have been registered as uppercase.
	for _, candidate := range []string{rel, strings.ToUpper(rel)} {
		if spec := s.adapted.Get(candidate); spec != nil && spec.Kind == tenant.ElementEdge {
			return true, nil
		}
	}
	// Schema-only: check t<X>_e_sch with case-insensitive comparison.
	if !s.config.GraphEnabled {
		return false, nil
	}
	esch := s.config.TenantID.EdgeSchemaTableName()
	var tableExists int
	if err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", esch,
	).Scan(&tableExists); err != nil || tableExists == 0 {
		return false, nil
	}
	var n int
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+esch+" WHERE LOWER(rel) = LOWER(?)", rel,
	).Scan(&n)
	return n > 0, err
}

// ResolveEdgeRelName returns the canonical (registry-cased) relationship label.
// OQL normalises entity names to lowercase; this restores the original casing
// so adapted.Get() and edge table queries work correctly.
func (s *SQLiteStore) ResolveEdgeRelName(ctx context.Context, rel string) string {
	// Check adapted registry with provided name then uppercase variant.
	for _, candidate := range []string{rel, strings.ToUpper(rel)} {
		if spec := s.adapted.Get(candidate); spec != nil && spec.Kind == tenant.ElementEdge {
			return candidate
		}
	}
	// Fall through to t<X>_e_sch for blob-only or schema-only labels.
	if !s.config.GraphEnabled {
		return rel
	}
	esch := s.config.TenantID.EdgeSchemaTableName()
	var stored string
	if err := s.readDB.QueryRowContext(ctx,
		"SELECT rel FROM "+esch+" WHERE LOWER(rel) = LOWER(?)", rel,
	).Scan(&stored); err == nil {
		return stored
	}
	return rel
}

// ListEdges returns all property rows for rel from the blob t<X>_edges table.
// Uses case-insensitive matching on rel so OQL-normalised names resolve correctly.
func (s *SQLiteStore) ListEdges(ctx context.Context, rel string) ([]map[string]interface{}, error) {
	if !s.config.GraphEnabled {
		return nil, nil
	}
	// Resolve canonical casing before querying the blob table.
	canonicalRel := s.ResolveEdgeRelName(ctx, rel)

	edgesTable := s.config.TenantID.EdgePropsTableName()
	rows, err := s.readDB.QueryContext(ctx,
		"SELECT edge_id, data FROM "+edgesTable+" WHERE rel = ? ORDER BY edge_id", canonicalRel)
	if err != nil {
		return nil, fmt.Errorf("ListEdges(%q): %w", rel, err)
	}
	defer func() { _ = rows.Close() }()

	var results []map[string]interface{}
	for rows.Next() {
		var edgeID int
		var dataJSON string
		if err := rows.Scan(&edgeID, &dataJSON); err != nil {
			return nil, fmt.Errorf("ListEdges(%q): scan: %w", rel, err)
		}
		var props map[string]interface{}
		if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
			return nil, fmt.Errorf("ListEdges(%q): unmarshal: %w", rel, err)
		}
		props["edge_id"] = edgeID
		props["rel"] = canonicalRel
		results = append(results, props)
	}
	return results, rows.Err()
}

// Uses DELETE + INSERT to simulate an UPSERT since FTS5 virtual tables do
// not support ON CONFLICT clauses.
func (s *SQLiteStore) IndexEdgeContent(ctx context.Context, rel string, edgeID int, props map[string]interface{}) error {
	efts := s.config.TenantID.EdgeFTSTableName()
	content := extractEdgeContent(props)

	// Delete any existing row for this (rel, edge_id) pair first.
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM "+efts+" WHERE rel = ? AND edge_id = ?", rel, edgeID)
	if err != nil {
		return fmt.Errorf("IndexEdgeContent: delete stale row: %w", err)
	}

	// Insert the new row.
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO "+efts+"(rel, edge_id, content) VALUES (?, ?, ?)",
		rel, edgeID, content)
	if err != nil {
		return fmt.Errorf("IndexEdgeContent: insert: %w", err)
	}
	return nil
}

// SearchEdges queries t<X>_efts using FTS5's MATCH operator and returns
// matching (rel, edge_id) pairs ordered by BM25 rank (best match first).
// limit ≤ 0 returns all matches.
func (s *SQLiteStore) SearchEdges(ctx context.Context, query string, limit int) ([]EdgeFTSResult, error) {
	efts := s.config.TenantID.EdgeFTSTableName()

	sql := "SELECT rel, edge_id FROM " + efts +
		" WHERE " + efts + " MATCH ? ORDER BY rank"
	args := []interface{}{query}
	if limit > 0 {
		sql += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.readDB.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("SearchEdges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []EdgeFTSResult
	for rows.Next() {
		var r EdgeFTSResult
		if err := rows.Scan(&r.Rel, &r.EdgeID); err != nil {
			return nil, fmt.Errorf("SearchEdges: scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
