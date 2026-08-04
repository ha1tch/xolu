// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ha1tch/xolu/pkg/models"
	"github.com/ha1tch/xolu/pkg/qs"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// validPersistedSQLType reports whether t is a SQL column type that the
// dialect's ColumnType mapping can legitimately produce. SQLType is interpolated
// into DDL unparameterised, so a persisted value outside this closed set is
// treated as an injection payload. Keep this in sync with the values returned by
// every StorageDialect.ColumnType implementation (currently SQLite: TEXT,
// INTEGER, REAL).
func validPersistedSQLType(t string) bool {
	switch t {
	case "TEXT", "INTEGER", "REAL":
		return true
	default:
		return false
	}
}

// validateAdaptedFieldName checks that a schema field name is safe to use as a
// SQL column/index identifier in derived DDL. It rejects any name that is not
// a single bare identifier.
func validateAdaptedFieldName(field string) error {
	// Canonical strict-identifier policy lives in pkg/qs (ASCII, leading letter
	// required so the leading-underscore namespace stays reserved for system
	// columns). Shared with the server entity-name validator.
	if err := qs.ValidateStrictIdentifier(field); err != nil {
		return fmt.Errorf("invalid schema field name %q: must start with a letter and contain only letters, digits, and underscores", field)
	}
	return nil
}

// validateAdaptedIndexName checks that an index name persisted in column-spec
// metadata is a safe SQL identifier. Index names are derived (via
// tenant.AdaptedNodeIndexField) from already-validated field names, but the
// persisted form is read back verbatim, so it is re-checked at the trust
// boundary alongside column names.
func validateAdaptedIndexName(name string) error {
	if err := qs.ValidateStrictIdentifier(name); err != nil {
		return fmt.Errorf("invalid index name %q: must start with a letter and contain only letters, digits, and underscores", name)
	}
	return nil
}

// validatePersistedSpec re-validates every column and index identifier in a spec
// that was loaded from persisted column_spec metadata (or otherwise did not pass
// through DeriveAdaptedTableSpec). This guards the D-009 residual class: the
// derivation path validates field names before they become identifiers, but the
// schema-evolution migration builds DROP COLUMN / DROP INDEX / data-migration
// SELECT statements from the *old* spec read back from storage. A column_spec
// written by a pre-D-009 binary, restored from backup, or written by any
// non-derivation path could otherwise carry a chained-DDL payload as a column
// name straight into an unparameterisable identifier position.
//
// Every offending identifier is reported (not just the first) so an operator can
// see the full extent of a poisoned spec in one pass.
func validatePersistedSpec(spec *AdaptedTableSpec) error {
	if spec == nil {
		return nil
	}
	var bad []string
	// The entity name is interpolated into the table name (t<X>_ndata_<entity>),
	// which reaches SQL unparameterised in every adapted-table statement. The HTTP
	// layer validates it at registration, but a persisted entity_type from a
	// pre-fix DB / restored backup / non-derivation writer is read back raw, so it
	// is re-validated here at the trust boundary (D-015).
	if err := validateAdaptedFieldName(spec.Entity); err != nil {
		bad = append(bad, fmt.Sprintf("entity name: %v", err))
	}
	for _, col := range spec.Columns {
		if err := validateAdaptedFieldName(col.Name); err != nil {
			bad = append(bad, fmt.Sprintf("column: %v", err))
		}
		// SQLType is interpolated into ADD COLUMN / CREATE TABLE DDL after the
		// column name (addColumnSQL: "ADD COLUMN %s %s"). At derivation it comes
		// from dialect.ColumnType, a closed set; but the persisted value is read
		// back raw, so a poisoned sql_type must be rejected at the trust boundary
		// (D-016, sibling of D-009/D-010/D-015).
		if !validPersistedSQLType(col.SQLType) {
			bad = append(bad, fmt.Sprintf("column %q: invalid SQL type %q", col.Name, col.SQLType))
		}
	}
	for _, idx := range spec.Indexes {
		if err := validateAdaptedIndexName(idx.Name); err != nil {
			bad = append(bad, fmt.Sprintf("index: %v", err))
		}
		for _, c := range idx.Columns {
			if err := validateAdaptedFieldName(c); err != nil {
				bad = append(bad, fmt.Sprintf("index column: %v", err))
			}
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("entity %q has %d invalid persisted identifier(s): %s",
			spec.Entity, len(bad), strings.Join(bad, "; "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Adapted table metadata
// ---------------------------------------------------------------------------
// This file implements the schema-to-DDL layer for adapted tables.
// It derives native SQLite column definitions from JSON Schema documents,
// manages the per-tenant t<X>_n_sch metadata table that tracks which entity
// types have adapted tables and what their column layout is, and provides
// the schema change detection logic used at startup and schema registration.
//
// The design is backend-portable: all SQL generation is delegated to the
// SQLDialect interface (defined in pkg/oql/sqlgen.go and extended here).
// ---------------------------------------------------------------------------

// ColumnDef describes a single column in an adapted table.
type ColumnDef struct {
	Name        string `json:"name"`          // Column name (e.g., "age", "REF_author_entity")
	JSONField   string `json:"json_field"`    // Original JSON field name (e.g., "age", "author")
	Type        string `json:"type"`          // JSON Schema type: string, integer, number, boolean, array, object
	Format      string `json:"format"`        // JSON Schema format: "", "decimal", "ref", "email", etc.
	SQLType     string `json:"sql_type"`      // Backend-specific SQL type (e.g., "TEXT", "INTEGER", "REAL")
	Required    bool   `json:"required"`      // Whether the field is in the schema's required array
	Precision   int    `json:"precision"`     // For decimal: total significant digits
	Scale       int    `json:"scale"`         // For decimal: digits after decimal point
	IsREF       bool   `json:"is_ref"`        // True if this column is part of a REF decomposition
	IsREFEntity bool   `json:"is_ref_entity"` // True for the _entity column of a REF pair
	IsREFID     bool   `json:"is_ref_id"`     // True for the _id column of a REF pair
}

// AdaptedTableSpec describes the full column layout of an adapted table.
type AdaptedTableSpec struct {
	Entity     string             `json:"entity"`      // Entity/relationship label name
	Kind       tenant.ElementKind `json:"kind"`        // ElementNode or ElementEdge
	TenantID   tenant.TenantID    `json:"tenant_id"`   // Owning tenant; used to derive the table name
	Columns    []ColumnDef        `json:"columns"`     // Ordered column definitions
	SchemaHash string             `json:"schema_hash"` // SHA-256 of canonical schema JSON
	HasExtra   bool               `json:"has_extra"`   // Whether _extra overflow column is present
	Indexes    []IndexDef         `json:"indexes"`     // Indexes to create
}

// IndexDef describes an index on an adapted table.
type IndexDef struct {
	Name    string   `json:"name"`    // Index name
	Columns []string `json:"columns"` // Column names
	Unique  bool     `json:"unique"`  // Whether the index is unique
}

// TableName returns the SQL table name for this adapted table.
// Routes to the node or edge naming convention based on Kind:
//   - ElementNode → t<XXXX>_ndata_<entity>   (e.g. t0001_ndata_user)
//   - ElementEdge → t<XXXX>_edata_<label>    (e.g. t0001_edata_KNOWS)
func (s *AdaptedTableSpec) TableName() string {
	if s.Kind == tenant.ElementEdge {
		return s.TenantID.AdaptedEdgeTableName(s.Entity)
	}
	return s.TenantID.AdaptedNodeTableName(s.Entity)
}

// ColumnNames returns all column names in order (excluding system columns).
func (s *AdaptedTableSpec) ColumnNames() []string {
	names := make([]string, len(s.Columns))
	for i, col := range s.Columns {
		names[i] = col.Name
	}
	return names
}

// FieldToColumn maps a JSON field name to its column name(s).
// For REF fields, this returns two names: REF_{field}_entity, REF_{field}_id.
// For all other fields, it returns a single name equal to the field name.
func (s *AdaptedTableSpec) FieldToColumn(jsonField string) []string {
	var cols []string
	for _, col := range s.Columns {
		if col.JSONField == jsonField {
			cols = append(cols, col.Name)
		}
	}
	return cols
}

// IsSchemaField reports whether a JSON field name is a declared schema field.
func (s *AdaptedTableSpec) IsSchemaField(jsonField string) bool {
	for _, col := range s.Columns {
		if col.JSONField == jsonField {
			return true
		}
	}
	return false
}

// ColumnByName returns the ColumnDef for a given SQL column name.
func (s *AdaptedTableSpec) ColumnByName(name string) (ColumnDef, bool) {
	for _, col := range s.Columns {
		if col.Name == name {
			return col, true
		}
	}
	return ColumnDef{}, false
}

// ---------------------------------------------------------------------------
// Schema-to-column derivation
// ---------------------------------------------------------------------------

// DeriveAdaptedTableSpec examines a JSON Schema document and produces a
// complete AdaptedTableSpec describing the adapted table layout.
//
// The dialect parameter determines backend-specific column types.
//
// This is a convenience wrapper that creates a SchemaIntrospector from
// the raw JSON Schema map. For direct use with queryfy (future), call
// DeriveAdaptedTableSpecFrom with a queryfy-backed introspector.
func DeriveAdaptedTableSpec(entity string, schema map[string]interface{}, dialect StorageDialect, tenantID tenant.TenantID) (*AdaptedTableSpec, error) {
	introspector := NewJSONSchemaIntrospector(schema)
	if introspector == nil {
		return nil, fmt.Errorf("schema for %q has no properties", entity)
	}

	// Schema hash still computed from the raw map for determinism
	schemaHash, err := canonicalSchemaHash(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to hash schema for %q: %w", entity, err)
	}

	return DeriveAdaptedTableSpecFrom(entity, introspector, dialect, schemaHash, tenantID)
}

// DeriveAdaptedTableSpecFrom derives an AdaptedTableSpec from a
// SchemaIntrospector. This is the backend-agnostic core that works
// with any schema representation (JSON Schema maps, queryfy objects,
// or anything else that implements SchemaIntrospector).
func DeriveAdaptedTableSpecFrom(entity string, schema SchemaIntrospector, dialect StorageDialect, schemaHash string, tenantID tenant.TenantID) (*AdaptedTableSpec, error) {
	// The entity name becomes part of the table name (t<X>_ndata_<entity>), which
	// reaches SQL unparameterised. HTTP callers validate it via validateEntityName,
	// but validate here too so a spec can never be derived with an injectable
	// entity name regardless of the caller (defence in depth; see D-015).
	if err := validateAdaptedFieldName(entity); err != nil {
		return nil, fmt.Errorf("invalid entity name %q: %w", entity, err)
	}

	hasExtra := schema.AllowsAdditional()

	// Get field names (already sorted), excluding system columns
	allFields := schema.FieldNames()
	fieldNames := make([]string, 0, len(allFields))
	for _, name := range allFields {
		if name == "id" {
			continue // id is a system column, not a schema column
		}
		fieldNames = append(fieldNames, name)
	}

	var columns []ColumnDef
	var indexes []IndexDef

	for _, fieldName := range fieldNames {
		// D-009: reject any field name that is not a bare SQL identifier
		// before it can become a column or index name in derived DDL.
		if err := validateAdaptedFieldName(fieldName); err != nil {
			return nil, fmt.Errorf("entity %q: %w", entity, err)
		}

		field := schema.GetField(fieldName)
		if field == nil {
			continue
		}

		jsonType := field.JSONType()
		format := field.Format()
		required := schema.IsRequired(fieldName)

		precision := 18 // default decimal precision
		scale := 4      // default decimal scale
		if p, ok := field.Meta("decimalPrecision"); ok {
			if pf, ok := p.(float64); ok {
				precision = int(pf)
			}
		}
		if s, ok := field.Meta("decimalScale"); ok {
			if sf, ok := s.(float64); ok {
				scale = int(sf)
			}
		}

		if format == models.SchemaFormatREF {
			// REF fields decompose into two columns: one for the target entity
			// type and one for the target entity ID. IsREFEntity and IsREFID
			// are set explicitly so consumers never need to inspect column names.
			columns = append(columns, ColumnDef{
				Name:        "REF_" + fieldName + "_entity",
				JSONField:   fieldName,
				Type:        "string",
				Format:      models.SchemaFormatREF,
				SQLType:     dialect.ColumnType("string", "", 0, 0),
				Required:    required,
				IsREF:       true,
				IsREFEntity: true,
			})
			columns = append(columns, ColumnDef{
				Name:      "REF_" + fieldName + "_id",
				JSONField: fieldName,
				Type:      "integer",
				Format:    models.SchemaFormatREF,
				SQLType:   dialect.ColumnType("integer", "", 0, 0),
				Required:  required,
				IsREF:     true,
				IsREFID:   true,
			})
			// Index on _id column for join lookups
			indexes = append(indexes, IndexDef{
				Name:    tenantID.AdaptedNodeIndexField(entity, "ref_"+fieldName),
				Columns: []string{"REF_" + fieldName + "_id"},
			})
		} else {
			sqlType := dialect.ColumnType(jsonType, format, precision, scale)
			columns = append(columns, ColumnDef{
				Name:      fieldName,
				JSONField: fieldName,
				Type:      jsonType,
				Format:    format,
				SQLType:   sqlType,
				Required:  required,
				Precision: precision,
				Scale:     scale,
			})

			// Auto-index heuristics
			enumVals := field.EnumValues()
			if shouldAutoIndexField(fieldName, jsonType, format, required, enumVals) {
				indexes = append(indexes, IndexDef{
					Name:    tenantID.AdaptedNodeIndexField(entity, fieldName),
					Columns: []string{fieldName},
				})
			}
		}

		// Check for explicit index override
		if idx, ok := field.Meta("x-xolu-index"); ok {
			if b, ok := idx.(bool); ok && b {
				indexes = append(indexes, IndexDef{
					Name:    tenantID.AdaptedNodeIndexField(entity, fieldName),
					Columns: []string{fieldName},
				})
			}
		}
	}

	return &AdaptedTableSpec{
		Entity:     entity,
		TenantID:   tenantID,
		Columns:    columns,
		SchemaHash: schemaHash,
		HasExtra:   hasExtra,
		Indexes:    deduplicateIndexes(indexes),
	}, nil
}

// shouldAutoIndex determines whether a field should receive an automatic
// index based on schema heuristics.
// shouldAutoIndexField determines whether a field should be automatically
// indexed based on its type, format, and constraints. This is the
// interface-friendly version used by DeriveAdaptedTableSpecFrom.
func shouldAutoIndexField(name, jsonType, format string, required bool, enumVals []string) bool {
	// Decimal fields: likely used in range queries
	if format == "decimal" {
		return true
	}
	// Enum fields: low cardinality, often filtered on
	if len(enumVals) > 0 {
		return true
	}
	// Required string fields: likely identifiers
	// (Pattern check removed — not available via EnumValues alone.
	//  When queryfy introspection lands, we can add PatternString() check.)
	return false
}

// canonicalSchemaHash produces a deterministic SHA-256 hash of a JSON Schema.
// The schema is re-serialised with sorted keys to ensure stability.
func canonicalSchemaHash(schema map[string]interface{}) (string, error) {
	// json.Marshal sorts map keys deterministically in Go
	canonical, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(canonical)
	return fmt.Sprintf("%x", hash), nil
}

// deduplicateIndexes removes duplicate index definitions (same columns).
func deduplicateIndexes(indexes []IndexDef) []IndexDef {
	seen := make(map[string]bool)
	var result []IndexDef
	for _, idx := range indexes {
		key := strings.Join(idx.Columns, ",")
		if !seen[key] {
			seen[key] = true
			result = append(result, idx)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// DDL generation
// ---------------------------------------------------------------------------

// GenerateCreateTableSQL produces the CREATE TABLE statement for an adapted
// table using the given dialect.
func GenerateCreateTableSQL(spec *AdaptedTableSpec, dialect StorageDialect) string {
	return dialect.CreateTableSQL(spec)
}

// GenerateIndexSQL produces CREATE INDEX statements for the adapted table
// using the given dialect.
func GenerateIndexSQL(spec *AdaptedTableSpec, dialect StorageDialect) []string {
	return dialect.CreateIndexSQL(spec)
}

// ---------------------------------------------------------------------------
// Data partitioning (decompose map into columns + overflow)
// ---------------------------------------------------------------------------

// PartitionData separates a data map into schema-column values and overflow.
// Returns:
//   - columnValues: ordered values matching spec.Columns, ready for INSERT
//   - extra: map of fields not in the schema (nil if none or !hasExtra)
//
// REF fields are decomposed: {"type":"REF","entity":"users","id":42} becomes
// two column values: "users" (for REF_{field}_entity) and 42 (for REF_{field}_id).
func PartitionData(spec *AdaptedTableSpec, data map[string]interface{}) (columnValues []interface{}, extra map[string]interface{}) {
	// Build a set of known JSON field names for fast lookup
	knownFields := make(map[string]bool, len(spec.Columns))
	for _, col := range spec.Columns {
		knownFields[col.JSONField] = true
	}

	// Collect column values in order
	columnValues = make([]interface{}, len(spec.Columns))
	for i, col := range spec.Columns {
		if col.IsREF {
			// REF decomposition through the canonical recogniser
			// (models.IsReference) — the same predicate ExtractEntityEdges
			// uses via ExtractRefs, so the column pipeline and the edge
			// pipeline are structurally guaranteed to agree on what counts
			// as a REF. A value that IsReference rejects (wrong shape,
			// missing "type":"REF", bad id type) decomposes to nil/nil
			// rather than half a reference. IsREFEntity and IsREFID
			// identify which half of the pair this column is, with no
			// dependency on the column name string.
			ref, ok := models.IsReference(data[col.JSONField])
			if !ok {
				columnValues[i] = nil
				continue
			}
			if col.IsREFEntity {
				columnValues[i] = ref.Entity
			} else if col.IsREFID {
				columnValues[i] = int(ref.ID)
			}
		} else {
			columnValues[i] = data[col.JSONField]
		}
	}

	// Collect overflow fields
	if spec.HasExtra {
		for key, val := range data {
			if key == "id" || key == "_version" {
				continue
			}
			if !knownFields[key] {
				if extra == nil {
					extra = make(map[string]interface{})
				}
				extra[key] = val
			}
		}
	}

	return columnValues, extra
}

// ReassembleData reconstructs a map[string]interface{} from column values
// and an optional overflow map. This is the inverse of PartitionData.
func ReassembleData(spec *AdaptedTableSpec, columnValues []interface{}, extra map[string]interface{}, id int, version int) map[string]interface{} {
	result := make(map[string]interface{}, len(spec.Columns)+4)
	result["id"] = id
	result["_version"] = version

	// Track which JSON fields have been set (for REF reconstruction)
	refFields := make(map[string]map[string]interface{})

	for i, col := range spec.Columns {
		val := columnValues[i]
		if val == nil {
			continue
		}

		if col.IsREF {
			// Accumulate REF parts using the explicit part flags,
			// not by inspecting the column name suffix.
			if refFields[col.JSONField] == nil {
				refFields[col.JSONField] = map[string]interface{}{
					"type": models.RefTypeValue,
				}
			}
			if col.IsREFEntity {
				refFields[col.JSONField]["entity"] = val
			} else if col.IsREFID {
				refFields[col.JSONField]["id"] = val
			}
		} else if col.Type == "array" || (col.Type == "object" && col.Format != models.SchemaFormatREF) {
			// Deserialise JSON-stored columns
			if s, ok := val.(string); ok && s != "" {
				var parsed interface{}
				if err := json.Unmarshal([]byte(s), &parsed); err == nil {
					result[col.JSONField] = parsed
				} else {
					result[col.JSONField] = val
				}
			}
		} else if col.Type == "boolean" {
			// SQLite stores booleans as integers
			switch v := val.(type) {
			case int64:
				result[col.JSONField] = v != 0
			case int:
				result[col.JSONField] = v != 0
			case float64:
				result[col.JSONField] = v != 0
			default:
				result[col.JSONField] = val
			}
		} else {
			result[col.JSONField] = val
		}
	}

	// Merge assembled REF objects, composed through the canonical
	// constructor (models.NewReference / ToMap) so the reassembled shape is
	// exactly what models.IsReference recognises — the round trip
	// PartitionData ∘ ReassembleData is identity on REF fields by
	// construction, not by parallel hand-built maps. A pair with a missing
	// or non-integer half is dropped rather than emitted as half a
	// reference.
	for field, refObj := range refFields {
		entity, eok := refObj["entity"].(string)
		var id int64
		iok := false
		switch v := refObj["id"].(type) {
		case int:
			id, iok = int64(v), true
		case int64:
			id, iok = v, true
		case float64:
			id, iok = int64(v), true
		}
		if !eok || !iok {
			continue
		}
		result[field] = models.NewReference(entity, id).ToMap()
	}

	// Merge overflow
	for k, v := range extra {
		result[k] = v
	}

	return result
}
