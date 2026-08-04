// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

// ---------------------------------------------------------------------------
// Adapted edge tables — Stage 7
// ---------------------------------------------------------------------------
//
// When a relationship label has both a registered schema (t<X>_e_sch row from
// Stage 6) AND a derived column layout (column_spec populated), edge properties
// for that label are stored in t<X>_edata_<label> rather than the blob
// t<X>_edges table.
//
// The adapted registry (SQLiteStore.adapted) holds both node and edge specs,
// distinguished by AdaptedTableSpec.Kind. At dispatch time:
//
//   AddEdgeWithProps: if adapted.Get(rel) returns an ElementEdge spec,
//     route to adaptedCreate against the edata table; otherwise blob path.
//   GetEdge / GetManyEdges: same dispatch.
//
// The t<X>_edata_<label> schema is identical to t<X>_ndata_<entity>:
//   id INTEGER PRIMARY KEY (the surrogate edge ID from t<X>_eseq)
//   <schema columns>
//   _extra TEXT (optional overflow)
//   _version INTEGER NOT NULL DEFAULT 1
//   created_at / updated_at TIMESTAMP
//
// This reuses all adapted CRUD helpers unchanged — they operate on
// AdaptedTableSpec and are kind-agnostic.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// AdaptedEdgeStore is an optional interface implemented by backends that
// support adapted (native-column) tables for edge properties.
type AdaptedEdgeStore interface {
	// RegisterAdaptedEdge derives a native-column table for the given
	// relationship label from its JSON Schema, creates the table and indexes
	// in the database, and records the adapted spec in t<X>_e_sch.
	// The label must already be registered in t<X>_e_sch via
	// RegisterEdgeSchema; if it is not, RegisterAdaptedEdge registers it
	// implicitly.
	RegisterAdaptedEdge(ctx context.Context, rel string, schema map[string]interface{}) error

	// IsEdgeAdapted reports whether rel has an adapted table spec in the
	// in-memory registry (i.e. whether edge properties for this label will
	// be stored in t<X>_edata_<label> rather than t<X>_edges).
	IsEdgeAdapted(rel string) bool
}

// ---------------------------------------------------------------------------
// RegisterAdaptedEdge implementation
// ---------------------------------------------------------------------------

// RegisterAdaptedEdge derives a native-column table for rel, creates it,
// persists the column spec in t<X>_e_sch, and populates the in-memory
// adapted registry with an ElementEdge spec.
func (s *SQLiteStore) RegisterAdaptedEdge(ctx context.Context, rel string, schema map[string]interface{}) error {
	if !s.config.GraphEnabled {
		return fmt.Errorf("RegisterAdaptedEdge: graph not enabled")
	}

	// Derive the spec with ElementEdge kind.
	spec, err := DeriveAdaptedTableSpec(rel, schema, s.dialect, s.config.TenantID)
	if err != nil {
		return fmt.Errorf("RegisterAdaptedEdge %q: derive spec: %w", rel, err)
	}
	spec.Kind = tenant.ElementEdge // override: this is an edge spec

	esch := s.config.TenantID.EdgeSchemaTableName()

	// Marshal schema JSON once for all uses below.
	schJSON, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("RegisterAdaptedEdge %q: marshal schema: %w", rel, err)
	}

	// Check for existing registration in t<X>_e_sch.
	var existingHash string
	var existingColSpec sql.NullString
	err = s.db.QueryRowContext(ctx,
		"SELECT schema_hash, column_spec FROM "+esch+" WHERE rel = ?", rel,
	).Scan(&existingHash, &existingColSpec)

	if err == nil {
		// Row exists.
		if existingHash == spec.SchemaHash && existingColSpec.Valid {
			// Same schema, adapted table already registered — update in-memory only.
			if s.adapted.Get(rel) == nil {
				s.adapted.Set(rel, spec)
			}
			s.suppressEdge(rel)
			return nil
		}
		if existingHash != spec.SchemaHash && existingColSpec.Valid {
			// Schema changed and adapted table exists — apply column diff.
			spec.Kind = tenant.ElementEdge // ensure kind is preserved through migration
			diff := DiffAdaptedSpecs(s.adapted.Get(rel), spec)
			if !diff.IsEmpty() {
				if diff.HasTypeConflicts() {
					return fmt.Errorf("RegisterAdaptedEdge %q: incompatible type changes", rel)
				}
				// Apply ADD COLUMN / DROP COLUMN DDL directly.
				tableName := spec.TableName()
				for _, col := range diff.Added {
					if _, err := s.db.ExecContext(ctx, addColumnSQL(tableName, col, s.dialect)); err != nil {
						return fmt.Errorf("RegisterAdaptedEdge %q: add column %s: %w", rel, col.Name, err)
					}
				}
			}
			return s.upsertEdgeAdaptedMeta(ctx, rel, spec, string(schJSON))
		}
		// Same hash or column_spec absent — adapted table not yet created. Fall through.
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("RegisterAdaptedEdge %q: query e_sch: %w", rel, err)
	}

	// Create the adapted table.
	ddl := GenerateCreateTableSQL(spec, s.dialect)
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("RegisterAdaptedEdge %q: create table %s: %w", rel, spec.TableName(), err)
	}
	for _, stmt := range GenerateIndexSQL(spec, s.dialect) {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("RegisterAdaptedEdge %q: create index: %w", rel, err)
		}
	}

	// Persist metadata to t<X>_e_sch.
	if err := s.upsertEdgeAdaptedMeta(ctx, rel, spec, string(schJSON)); err != nil {
		return err
	}

	s.adapted.Set(rel, spec)
	s.suppressEdge(rel)
	return nil
}

// upsertEdgeAdaptedMeta writes or updates the column_spec and has_extra
// fields in t<X>_e_sch for the given rel.
func (s *SQLiteStore) upsertEdgeAdaptedMeta(ctx context.Context, rel string, spec *AdaptedTableSpec, schemaJSON string) error {
	esch := s.config.TenantID.EdgeSchemaTableName()

	colSpecJSON, err := json.Marshal(spec.Columns)
	if err != nil {
		return fmt.Errorf("upsertEdgeAdaptedMeta %q: marshal columns: %w", rel, err)
	}
	hasExtraInt := 0
	if spec.HasExtra {
		hasExtraInt = 1
	}

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO "+esch+" (rel, schema_hash, schema_json, column_spec, has_extra)"+
			" VALUES (?, ?, ?, ?, ?)"+
			" ON CONFLICT(rel) DO UPDATE SET"+
			"   schema_hash = excluded.schema_hash,"+
			"   schema_json = excluded.schema_json,"+
			"   column_spec = excluded.column_spec,"+
			"   has_extra   = excluded.has_extra,"+
			"   updated_at  = CURRENT_TIMESTAMP",
		rel, spec.SchemaHash, schemaJSON, string(colSpecJSON), hasExtraInt)
	if err != nil {
		return fmt.Errorf("upsertEdgeAdaptedMeta %q: upsert: %w", rel, err)
	}
	return nil
}

// IsEdgeAdapted reports whether rel has an adapted (ElementEdge) spec in the
// in-memory registry.
func (s *SQLiteStore) IsEdgeAdapted(rel string) bool {
	spec := s.adapted.Get(rel)
	return spec != nil && spec.Kind == tenant.ElementEdge
}

// ---------------------------------------------------------------------------
// Adapted edge dispatch helpers
// ---------------------------------------------------------------------------

// edgeSpecFor returns the adapted spec for rel if one exists with
// ElementEdge kind, or nil if the label uses blob storage.
func (s *SQLiteStore) edgeSpecFor(rel string) *AdaptedTableSpec {
	spec := s.adapted.Get(rel)
	if spec == nil || spec.Kind != tenant.ElementEdge {
		return nil
	}
	return spec
}

// ---------------------------------------------------------------------------
// Startup: load adapted edge specs from t<X>_e_sch
// ---------------------------------------------------------------------------

// loadAdaptedEdgeSpecs reads any populated column_spec rows from t<X>_e_sch
// at store startup and populates the adapted registry with ElementEdge specs.
func (s *SQLiteStore) loadAdaptedEdgeSpecs(ctx context.Context, db *sql.DB) error {
	esch := s.config.TenantID.EdgeSchemaTableName()

	// Table may not exist for databases created before Stage 6.
	var tableExists int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", esch,
	).Scan(&tableExists); err != nil || tableExists == 0 {
		return nil
	}

	// Only read rows that have a column_spec (i.e. adapted table was registered).
	rows, err := db.QueryContext(ctx,
		"SELECT rel, schema_hash, column_spec, has_extra FROM "+esch+
			" WHERE column_spec IS NOT NULL")
	if err != nil {
		return fmt.Errorf("loadAdaptedEdgeSpecs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var rel, schemaHash, colSpecJSON string
		var hasExtraInt int
		if err := rows.Scan(&rel, &schemaHash, &colSpecJSON, &hasExtraInt); err != nil {
			return fmt.Errorf("loadAdaptedEdgeSpecs scan: %w", err)
		}
		var columns []ColumnDef
		if err := json.Unmarshal([]byte(colSpecJSON), &columns); err != nil {
			return fmt.Errorf("loadAdaptedEdgeSpecs unmarshal %q: %w", rel, err)
		}
		spec := &AdaptedTableSpec{
			Entity:     rel,
			Kind:       tenant.ElementEdge,
			TenantID:   s.config.TenantID,
			Columns:    columns,
			SchemaHash: schemaHash,
			HasExtra:   hasExtraInt == 1,
		}
		// D-009 residual: re-validate persisted identifiers at the trust boundary
		// (see validatePersistedSpec). Quarantine a poisoned edge spec and fail
		// startup rather than serve it.
		if err := validatePersistedSpec(spec); err != nil {
			return fmt.Errorf("refusing to load adapted edge %q from %s: "+
				"persisted schema contains an invalid SQL identifier (possible DDL-injection "+
				"payload in column_spec); entity quarantined: %w", rel, esch, err)
		}
		s.adapted.Set(rel, spec)
	}
	return rows.Err()
}
