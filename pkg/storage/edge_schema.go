// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

// ---------------------------------------------------------------------------
// Edge schema registry — implements EdgeSchemaStore
// ---------------------------------------------------------------------------
//
// t<X>_e_sch holds one row per registered relationship label.
// Registration suppresses the warn-once log that fires from AddEdgeWithProps
// when a label has never been registered.
//
// The warn-once mechanism is in-memory (resets on restart). Suppression via
// SuppressEdgeSchemaWarning has the same effect as registration for the
// lifetime of the process but writes nothing to the database.
//
// The warning fires only when AddEdgeWithProps is called with non-nil props
// for an unregistered, non-suppressed label — plain AddEdge (topology only)
// never triggers it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// warnOnceEdge fires a WARN log for an unregistered edge label the first
// time AddEdgeWithProps is called for that label. Subsequent calls for the
// same label are silent. The suppression set is populated both by this
// function (after firing) and by RegisterEdgeSchema / SuppressEdgeSchemaWarning.
func (s *SQLiteStore) warnOnceEdge(rel string) {
	s.edgeWarnMu.Lock()
	defer s.edgeWarnMu.Unlock()
	if s.edgeWarnSuppressed[rel] {
		return
	}
	s.edgeWarnSuppressed[rel] = true
	s.logger.Warn().
		Str("rel", rel).
		Str("action", "call RegisterEdgeSchema to suppress this warning").
		Msg("AddEdgeWithProps: relationship label has no registered schema; " +
			"edge properties will be stored as blob only — no adapted table, " +
			"no OQL FROM <label> routing, no schema-level index")
}

// suppressEdge marks rel as suppressed without logging. Called by both
// RegisterEdgeSchema (after a successful DB write) and SuppressEdgeSchemaWarning.
func (s *SQLiteStore) suppressEdge(rel string) {
	s.edgeWarnMu.Lock()
	s.edgeWarnSuppressed[rel] = true
	s.edgeWarnMu.Unlock()
}

// isEdgeSuppressed reports whether rel is already suppressed.
func (s *SQLiteStore) isEdgeSuppressed(rel string) bool {
	s.edgeWarnMu.Lock()
	defer s.edgeWarnMu.Unlock()
	return s.edgeWarnSuppressed[rel]
}

// ---------------------------------------------------------------------------
// EdgeSchemaStore implementation
// ---------------------------------------------------------------------------

// RegisterEdgeSchema persists the JSON Schema for rel in t<X>_e_sch and
// suppresses the unregistered-label warning for that label.
// Idempotent when called with the same schema hash; updates when the hash
// changes.
func (s *SQLiteStore) RegisterEdgeSchema(ctx context.Context, rel string, schema map[string]interface{}) error {
	if !s.config.GraphEnabled {
		return fmt.Errorf("RegisterEdgeSchema: graph not enabled")
	}

	hash, err := canonicalSchemaHash(schema)
	if err != nil {
		return fmt.Errorf("RegisterEdgeSchema %q: hash schema: %w", rel, err)
	}
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("RegisterEdgeSchema %q: marshal schema: %w", rel, err)
	}

	esch := s.config.TenantID.EdgeSchemaTableName()

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO "+esch+" (rel, schema_hash, schema_json) VALUES (?, ?, ?)"+
			" ON CONFLICT(rel) DO UPDATE SET"+
			"   schema_hash = excluded.schema_hash,"+
			"   schema_json = excluded.schema_json,"+
			"   updated_at  = CURRENT_TIMESTAMP"+
			" WHERE schema_hash != excluded.schema_hash",
		rel, hash, string(schemaJSON))
	if err != nil {
		return fmt.Errorf("RegisterEdgeSchema %q: upsert: %w", rel, err)
	}

	s.suppressEdge(rel)
	return nil
}

// SuppressEdgeSchemaWarning silences the unregistered-label warning for rel
// without writing anything to the database. The suppression is in-memory
// only and resets on restart.
func (s *SQLiteStore) SuppressEdgeSchemaWarning(rel string) {
	s.suppressEdge(rel)
}

// IsEdgeSchemaRegistered reports whether rel has a row in t<X>_e_sch.
func (s *SQLiteStore) IsEdgeSchemaRegistered(ctx context.Context, rel string) (bool, error) {
	if !s.config.GraphEnabled {
		return false, nil
	}
	esch := s.config.TenantID.EdgeSchemaTableName()
	var n int
	err := s.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+esch+" WHERE rel = ?", rel,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("IsEdgeSchemaRegistered %q: %w", rel, err)
	}
	return n > 0, nil
}

// loadEdgeSchemaSuppressions reads all registered labels from t<X>_e_sch at
// store startup and pre-suppresses their warnings. This ensures that a label
// registered before a restart does not warn on first use after restart.
func (s *SQLiteStore) loadEdgeSchemaSuppressions(ctx context.Context, db *sql.DB) error {
	esch := s.config.TenantID.EdgeSchemaTableName()

	// Table may not exist yet for databases created before Stage 6.
	var tableExists int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", esch,
	).Scan(&tableExists); err != nil || tableExists == 0 {
		return nil // no table yet — nothing to load
	}

	rows, err := db.QueryContext(ctx, "SELECT rel FROM "+esch)
	if err != nil {
		return fmt.Errorf("loadEdgeSchemaSuppressions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	s.edgeWarnMu.Lock()
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			s.edgeWarnMu.Unlock()
			return fmt.Errorf("loadEdgeSchemaSuppressions scan: %w", err)
		}
		s.edgeWarnSuppressed[rel] = true
	}
	s.edgeWarnMu.Unlock()
	return rows.Err()
}
