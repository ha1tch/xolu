// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// ---------------------------------------------------------------------------
// Adapted table registry
// ---------------------------------------------------------------------------
// The AdaptedRegistry manages adapted table specs and provides the dispatch
// decision for CRUD operations. It is embedded in SQLiteStore and consulted
// on every CRUD call to determine whether to use the adapted path or the
// blob path.
//
// Thread safety: the registry is protected by an RWMutex. Reads (spec
// lookups during CRUD) take a read lock; writes (table registration) take
// a write lock. Registration is rare (startup + schema changes); reads
// are on every request.
// ---------------------------------------------------------------------------

// AdaptedRegistry tracks which entity types have adapted tables.
type AdaptedRegistry struct {
	mu    sync.RWMutex
	specs map[string]*AdaptedTableSpec
}

// NewAdaptedRegistry creates an empty registry.
func NewAdaptedRegistry() *AdaptedRegistry {
	return &AdaptedRegistry{
		specs: make(map[string]*AdaptedTableSpec),
	}
}

// Get returns the adapted table spec for an entity, or nil if the entity
// uses blob storage.
func (r *AdaptedRegistry) Get(entity string) *AdaptedTableSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.specs[entity]
}

// Set registers an adapted table spec for an entity.
func (r *AdaptedRegistry) Set(entity string, spec *AdaptedTableSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs[entity] = spec
}

// IsAdapted reports whether an entity type has an adapted table.
func (r *AdaptedRegistry) IsAdapted(entity string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.specs[entity]
	return ok
}

// Entities returns a sorted list of all adapted entity types.
func (r *AdaptedRegistry) Entities() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entities := make([]string, 0, len(r.specs))
	for e := range r.specs {
		entities = append(entities, e)
	}
	return entities
}

// ---------------------------------------------------------------------------
// Table creation and metadata persistence
// ---------------------------------------------------------------------------

// RegisterAdaptedTable derives a table spec from a JSON Schema, creates
// the table and indexes in the database, and records the spec in the
// per-tenant t<X>_n_sch metadata table.
//
// If the table already exists with the same schema hash, this is a no-op.
// If the schema has changed, the caller must handle migration separately
// (Phase 4 of the design).
func RegisterAdaptedTable(ctx context.Context, db *sql.DB, registry *AdaptedRegistry, entity string, schema map[string]interface{}, dialect StorageDialect, tenantID tenant.TenantID) error {
	spec, err := DeriveAdaptedTableSpec(entity, schema, dialect, tenantID)
	if err != nil {
		return fmt.Errorf("failed to derive adapted table spec for %q: %w", entity, err)
	}

	nsch := tenantID.NodeSchemaTableName()

	// Ensure the per-tenant schema registry table exists (lazy creation).
	if _, err := db.ExecContext(ctx, dialect.NodeSchemaTableSQL(tenantID)); err != nil {
		return fmt.Errorf("failed to ensure %s table: %w", nsch, err)
	}

	// Check for existing registration in this tenant's schema table.
	var existingHash string
	err = db.QueryRowContext(ctx,
		"SELECT schema_hash FROM "+nsch+" WHERE entity_type = ?",
		entity).Scan(&existingHash)

	if err == nil && existingHash == spec.SchemaHash {
		// Same schema, just ensure registry is populated.
		registry.Set(entity, spec)
		return nil
	}

	if err == nil && existingHash != spec.SchemaHash {
		// Schema changed — attempt automatic migration.
		// The registry already has the old spec (loaded at startup).
		if migrateErr := MigrateAdaptedTable(ctx, db, registry, entity, schema, dialect); migrateErr != nil {
			return fmt.Errorf("schema migration for %q failed: %w", entity, migrateErr)
		}
		return nil
	}

	// err is sql.ErrNoRows — new entity for this tenant, create everything.
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed to check %s: %w", nsch, err)
	}

	// Create the adapted table.
	ddl := GenerateCreateTableSQL(spec, dialect)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("failed to create adapted table %s: %w", spec.TableName(), err)
	}

	// Create indexes.
	for _, stmt := range GenerateIndexSQL(spec, dialect) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// Persist metadata into the per-tenant schema registry.
	columnSpecJSON, err := json.Marshal(spec.Columns)
	if err != nil {
		return fmt.Errorf("failed to marshal column spec: %w", err)
	}

	hasExtraInt := 0
	if spec.HasExtra {
		hasExtraInt = 1
	}

	_, err = db.ExecContext(ctx,
		"INSERT INTO "+nsch+" (entity_type, schema_hash, column_spec, has_extra) VALUES (?, ?, ?, ?)",
		entity, spec.SchemaHash, string(columnSpecJSON), hasExtraInt)
	if err != nil {
		return fmt.Errorf("failed to record adapted table metadata in %s: %w", nsch, err)
	}

	registry.Set(entity, spec)
	return nil
}

// LoadAdaptedRegistry reads the per-tenant t<X>_n_sch metadata table and
// populates the registry. Called at store startup.
func LoadAdaptedRegistry(ctx context.Context, db *sql.DB, tenantID tenant.TenantID) (*AdaptedRegistry, error) {
	registry := NewAdaptedRegistry()

	nsch := tenantID.NodeSchemaTableName()

	// Check if the per-tenant schema registry table exists.
	var tableExists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
		nsch,
	).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		return registry, nil // No adapted tables yet for this tenant.
	}

	rows, err := db.QueryContext(ctx,
		"SELECT entity_type, schema_hash, column_spec, has_extra FROM "+nsch)
	if err != nil {
		return nil, fmt.Errorf("failed to load adapted table schemas from %s: %w", nsch, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var entity, schemaHash, columnSpecJSON string
		var hasExtraInt int
		if err := rows.Scan(&entity, &schemaHash, &columnSpecJSON, &hasExtraInt); err != nil {
			return nil, fmt.Errorf("failed to scan adapted table schema: %w", err)
		}

		var columns []ColumnDef
		if err := json.Unmarshal([]byte(columnSpecJSON), &columns); err != nil {
			return nil, fmt.Errorf("failed to unmarshal column spec for %q: %w", entity, err)
		}

		spec := &AdaptedTableSpec{
			Entity:     entity,
			TenantID:   tenantID,
			Columns:    columns,
			SchemaHash: schemaHash,
			HasExtra:   hasExtraInt == 1,
		}

		// D-009 residual: persisted column/index identifiers never passed back
		// through DeriveAdaptedTableSpec's allowlist. Re-validate at the trust
		// boundary so a poisoned column_spec (pre-fix DB, restored backup, or any
		// non-derivation writer) cannot reach DDL construction. A poisoned entity
		// is quarantined (never registered) and startup fails loudly.
		if err := validatePersistedSpec(spec); err != nil {
			return nil, fmt.Errorf("refusing to load adapted table %q from %s: "+
				"persisted schema contains an invalid SQL identifier (possible DDL-injection "+
				"payload in column_spec); entity quarantined: %w", entity, nsch, err)
		}

		registry.Set(entity, spec)
	}

	return registry, rows.Err()
}

// ---------------------------------------------------------------------------
// Adapted CRUD operations
// ---------------------------------------------------------------------------
// These functions implement CRUD against an adapted table. They are called
// by the dispatch layer in the SQLiteStore methods when the entity has an
// adapted table spec.
// ---------------------------------------------------------------------------

// adaptedCreate inserts an entity into its adapted table.
// Table name encodes the tenant (t<XXXX>_ndata_<entity>) — no tenant_id argument needed.
func adaptedCreate(ctx context.Context, tx *sql.Tx, spec *AdaptedTableSpec, dialect StorageDialect, id int, data map[string]interface{}) error {
	colVals, extra := PartitionData(spec, data)

	// Normalise decimal columns for storage
	if err := NormaliseDecimalColumns(spec, dialect, colVals); err != nil {
		return err
	}

	// Table name encodes the tenant — no tenant_id argument.
	args := []interface{}{id}
	args = append(args, colVals...)

	hasExtraArg := spec.HasExtra
	if spec.HasExtra && extra != nil {
		extraJSON, err := json.Marshal(extra)
		if err != nil {
			return fmt.Errorf("failed to marshal overflow: %w", err)
		}
		args = append(args, string(extraJSON))
	} else if spec.HasExtra {
		args = append(args, nil)
	}

	query, _ := dialect.InsertSQL(spec, hasExtraArg)

	_, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("adapted insert into %s failed: %w", spec.TableName(), err)
	}
	return nil
}

// adaptedGet retrieves an entity from its adapted table.
func adaptedGet(ctx context.Context, db *sql.DB, spec *AdaptedTableSpec, dialect StorageDialect, id int) (map[string]interface{}, error) {
	query := dialect.SelectSQL(spec)
	row := db.QueryRowContext(ctx, query, id)

	// Prepare scan targets: columns + optional _extra + _version
	scanCount := len(spec.Columns) + 1 // +1 for _version
	if spec.HasExtra {
		scanCount++
	}
	scanTargets := make([]interface{}, scanCount)
	colValues := make([]interface{}, len(spec.Columns))

	// Use sql.Null* types for nullable columns
	nullStrings := make([]sql.NullString, len(spec.Columns))
	nullInts := make([]sql.NullInt64, len(spec.Columns))
	nullFloats := make([]sql.NullFloat64, len(spec.Columns))

	for i, col := range spec.Columns {
		switch col.SQLType {
		case "TEXT":
			scanTargets[i] = &nullStrings[i]
		case "INTEGER":
			scanTargets[i] = &nullInts[i]
		case "REAL":
			scanTargets[i] = &nullFloats[i]
		default:
			scanTargets[i] = &nullStrings[i]
		}
	}

	idx := len(spec.Columns)
	var extraStr sql.NullString
	if spec.HasExtra {
		scanTargets[idx] = &extraStr
		idx++
	}
	var version int
	scanTargets[idx] = &version

	if err := row.Scan(scanTargets...); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("adapted get from %s failed: %w", spec.TableName(), err)
	}

	// Extract scanned values
	for i, col := range spec.Columns {
		switch col.SQLType {
		case "TEXT":
			if nullStrings[i].Valid {
				colValues[i] = nullStrings[i].String
			}
		case "INTEGER":
			if nullInts[i].Valid {
				colValues[i] = nullInts[i].Int64
			}
		case "REAL":
			if nullFloats[i].Valid {
				colValues[i] = nullFloats[i].Float64
			}
		}
	}

	// Parse overflow
	var extra map[string]interface{}
	if spec.HasExtra && extraStr.Valid && extraStr.String != "" {
		if err := json.Unmarshal([]byte(extraStr.String), &extra); err != nil {
			return nil, fmt.Errorf("failed to unmarshal overflow: %w", err)
		}
	}

	// Denormalise decimal columns for client presentation
	DenormaliseDecimalColumns(spec, dialect, colValues)

	return ReassembleData(spec, colValues, extra, id, version), nil
}

// adaptedUpdate replaces all schema fields in an adapted table row.
func adaptedUpdate(ctx context.Context, tx *sql.Tx, spec *AdaptedTableSpec, dialect StorageDialect, id int, data map[string]interface{}, expectVersion int, hasVersion bool) error {
	colVals, extra := PartitionData(spec, data)

	// Normalise decimal columns for storage
	if err := NormaliseDecimalColumns(spec, dialect, colVals); err != nil {
		return err
	}

	// Build argument list matching the dialect's UpdateSQL column order
	args := make([]interface{}, 0, len(spec.Columns)+6)
	args = append(args, colVals...)

	if spec.HasExtra {
		if extra != nil {
			extraJSON, err := json.Marshal(extra)
			if err != nil {
				return fmt.Errorf("failed to marshal overflow: %w", err)
			}
			args = append(args, string(extraJSON))
		} else {
			args = append(args, nil)
		}
	}

	// WHERE args — table name encodes the tenant, just use id
	args = append(args, id)
	if hasVersion {
		args = append(args, expectVersion)
	}

	query := dialect.UpdateSQL(spec, hasVersion)

	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("adapted update on %s failed: %w", spec.TableName(), err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		if hasVersion {
			return ErrConflict
		}
		return ErrNotFound
	}
	return nil
}

// adaptedDelete removes an entity from its adapted table.
func adaptedDelete(ctx context.Context, tx *sql.Tx, spec *AdaptedTableSpec, dialect StorageDialect, id int) error {
	query := dialect.DeleteSQL(spec)

	result, err := tx.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("adapted delete from %s failed: %w", spec.TableName(), err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// adaptedList retrieves all entities from an adapted table.
func adaptedList(ctx context.Context, db *sql.DB, spec *AdaptedTableSpec, dialect StorageDialect) ([]map[string]interface{}, error) {
	query := dialect.SelectAllSQL(spec)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("adapted list from %s failed: %w", spec.TableName(), err)
	}
	defer func() { _ = rows.Close() }()

	var results []map[string]interface{}
	for rows.Next() {
		// Prepare scan targets: id + columns + optional _extra + _version
		scanCount := 1 + len(spec.Columns) + 1 // id + columns + _version
		if spec.HasExtra {
			scanCount++
		}
		scanTargets := make([]interface{}, scanCount)

		var rowID int
		scanTargets[0] = &rowID

		nullStrings := make([]sql.NullString, len(spec.Columns))
		nullInts := make([]sql.NullInt64, len(spec.Columns))
		nullFloats := make([]sql.NullFloat64, len(spec.Columns))

		for i, col := range spec.Columns {
			switch col.SQLType {
			case "TEXT":
				scanTargets[i+1] = &nullStrings[i]
			case "INTEGER":
				scanTargets[i+1] = &nullInts[i]
			case "REAL":
				scanTargets[i+1] = &nullFloats[i]
			default:
				scanTargets[i+1] = &nullStrings[i]
			}
		}

		idx := len(spec.Columns) + 1
		var extraStr sql.NullString
		if spec.HasExtra {
			scanTargets[idx] = &extraStr
			idx++
		}
		var version int
		scanTargets[idx] = &version

		if err := rows.Scan(scanTargets...); err != nil {
			return nil, fmt.Errorf("adapted list scan failed: %w", err)
		}

		// Extract scanned values
		colValues := make([]interface{}, len(spec.Columns))
		for i, col := range spec.Columns {
			switch col.SQLType {
			case "TEXT":
				if nullStrings[i].Valid {
					colValues[i] = nullStrings[i].String
				}
			case "INTEGER":
				if nullInts[i].Valid {
					colValues[i] = nullInts[i].Int64
				}
			case "REAL":
				if nullFloats[i].Valid {
					colValues[i] = nullFloats[i].Float64
				}
			}
		}

		var extra map[string]interface{}
		if spec.HasExtra && extraStr.Valid && extraStr.String != "" {
			if err := json.Unmarshal([]byte(extraStr.String), &extra); err != nil {
				return nil, fmt.Errorf("failed to unmarshal overflow: %w", err)
			}
		}

		DenormaliseDecimalColumns(spec, dialect, colValues)
		results = append(results, ReassembleData(spec, colValues, extra, rowID, version))
	}

	return results, rows.Err()
}

// adaptedExists checks if an entity exists in its adapted table.
func adaptedExists(ctx context.Context, db *sql.DB, spec *AdaptedTableSpec, dialect StorageDialect, id int) bool {
	query := dialect.ExistsSQL(spec)
	var exists bool
	err := db.QueryRowContext(ctx, query, id).Scan(&exists)
	return err == nil && exists
}

// adaptedGetInTx retrieves an entity within a transaction (for patch/update).
func adaptedGetInTx(ctx context.Context, tx *sql.Tx, spec *AdaptedTableSpec, dialect StorageDialect, id int) (map[string]interface{}, int, error) {
	// Reuse the dialect's SelectSQL but execute against tx instead of db
	query := dialect.SelectSQL(spec)
	row := tx.QueryRowContext(ctx, query, id)

	// Prepare scan targets: columns + optional _extra + _version
	scanCount := len(spec.Columns) + 1 // +1 for _version
	if spec.HasExtra {
		scanCount++
	}
	scanTargets := make([]interface{}, scanCount)
	nullStrings := make([]sql.NullString, len(spec.Columns))
	nullInts := make([]sql.NullInt64, len(spec.Columns))
	nullFloats := make([]sql.NullFloat64, len(spec.Columns))

	for i, col := range spec.Columns {
		switch col.SQLType {
		case "TEXT":
			scanTargets[i] = &nullStrings[i]
		case "INTEGER":
			scanTargets[i] = &nullInts[i]
		case "REAL":
			scanTargets[i] = &nullFloats[i]
		default:
			scanTargets[i] = &nullStrings[i]
		}
	}

	idx := len(spec.Columns)
	var extraStr sql.NullString
	if spec.HasExtra {
		scanTargets[idx] = &extraStr
		idx++
	}
	var version int
	scanTargets[idx] = &version

	if err := row.Scan(scanTargets...); err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("adapted get in tx from %s failed: %w", spec.TableName(), err)
	}

	// Extract scanned values
	colValues := make([]interface{}, len(spec.Columns))
	for i, col := range spec.Columns {
		switch col.SQLType {
		case "TEXT":
			if nullStrings[i].Valid {
				colValues[i] = nullStrings[i].String
			}
		case "INTEGER":
			if nullInts[i].Valid {
				colValues[i] = nullInts[i].Int64
			}
		case "REAL":
			if nullFloats[i].Valid {
				colValues[i] = nullFloats[i].Float64
			}
		}
	}

	var extra map[string]interface{}
	if spec.HasExtra && extraStr.Valid && extraStr.String != "" {
		if err := json.Unmarshal([]byte(extraStr.String), &extra); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal overflow: %w", err)
		}
	}

	// Denormalise decimal columns for client presentation
	DenormaliseDecimalColumns(spec, dialect, colValues)

	return ReassembleData(spec, colValues, extra, id, version), version, nil
}
