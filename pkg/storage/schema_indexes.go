// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ha1tch/xolu/pkg/models"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// Explicit indexes on an adapted table (XOT236).
//
// A schema asks for indexes in two places:
//
//   - on a property: "x-xolu-index": true (a plain index, already supported) and
//     "x-xolu-unique": true (a unique index on that one column);
//   - at the schema root: "x-xolu-indexes", a list of {"columns": [...], "unique": bool}
//     for composite indexes, unique or not (docs/ADAPTED_TABLES_DESIGN.md 10.3).
//
// A unique index makes xolu itself refuse a second row with the same key, so a
// client no longer needs to encode the key into the row id to get that guarantee.

// ErrUniqueViolation is returned when a write would make two rows share the
// key of a unique index. The concrete error is a *UniqueViolationError.
var ErrUniqueViolation = errors.New("unique constraint violated")

// UniqueViolationError reports which columns of which entity's unique index a
// write violated.
type UniqueViolationError struct {
	Entity  string
	Columns []string
}

func (e *UniqueViolationError) Error() string {
	return fmt.Sprintf("a row of %s with the same %s already exists", e.Entity, strings.Join(e.Columns, ", "))
}

// Is makes errors.Is(err, ErrUniqueViolation) true for every violation.
func (e *UniqueViolationError) Is(target error) bool { return target == ErrUniqueViolation }

// DeclaredIndex is one entry of a schema's root-level x-xolu-indexes.
type DeclaredIndex struct {
	Fields []string
	Unique bool
}

// IndexDeclarer is implemented by schema introspectors that can report the
// root-level x-xolu-indexes list. DeclaredIndexes returns an error when the
// keyword is malformed.
type IndexDeclarer interface {
	DeclaredIndexes() ([]DeclaredIndex, error)
}

// parseDeclaredIndexes validates and converts the raw x-xolu-indexes value.
func parseDeclaredIndexes(raw interface{}) ([]DeclaredIndex, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("x-xolu-indexes must be an array of {\"columns\": [...], \"unique\": bool}")
	}
	out := make([]DeclaredIndex, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("x-xolu-indexes[%d] must be an object", i)
		}
		for k := range m {
			if k != "columns" && k != "unique" {
				return nil, fmt.Errorf("x-xolu-indexes[%d]: unknown key %q (allowed: columns, unique)", i, k)
			}
		}
		rawFields, ok := m["columns"].([]interface{})
		if !ok || len(rawFields) == 0 {
			return nil, fmt.Errorf("x-xolu-indexes[%d].columns must be a non-empty array of property names", i)
		}
		d := DeclaredIndex{}
		seen := map[string]bool{}
		for _, f := range rawFields {
			name, ok := f.(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("x-xolu-indexes[%d].columns must contain only non-empty strings", i)
			}
			if seen[name] {
				return nil, fmt.Errorf("x-xolu-indexes[%d]: field %q appears twice", i, name)
			}
			seen[name] = true
			d.Fields = append(d.Fields, name)
		}
		if u, present := m["unique"]; present {
			b, ok := u.(bool)
			if !ok {
				return nil, fmt.Errorf("x-xolu-indexes[%d].unique must be true or false", i)
			}
			d.Unique = b
		}
		out = append(out, d)
	}
	return out, nil
}

// explicitIndexDefs returns the unique and composite indexes a schema declares
// beyond the per-property x-xolu-index. Field names must be declared properties,
// must not be "id" (the primary key) and must not be REF fields (a REF is two
// columns; ask for an index on them by a plain x-xolu-index).
func explicitIndexDefs(entity string, schema SchemaIntrospector, tenantID tenant.TenantID) ([]IndexDef, error) {
	var out []IndexDef

	for _, name := range schema.FieldNames() {
		if name == "id" {
			continue
		}
		field := schema.GetField(name)
		if field == nil {
			continue
		}
		v, ok := field.Meta("x-xolu-unique")
		if !ok {
			continue
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("field %q: x-xolu-unique must be true or false", name)
		}
		if !b {
			continue
		}
		if field.Format() == models.SchemaFormatREF {
			return nil, fmt.Errorf("field %q: x-xolu-unique is not supported on a REF field", name)
		}
		out = append(out, IndexDef{
			Name:    tenantID.AdaptedNodeIndexField(entity, "ux_"+name),
			Columns: []string{name},
			Unique:  true,
		})
	}

	decl, ok := schema.(IndexDeclarer)
	if !ok {
		return out, nil
	}
	declared, err := decl.DeclaredIndexes()
	if err != nil {
		return nil, err
	}
	for i, d := range declared {
		for _, f := range d.Fields {
			if f == "id" {
				return nil, fmt.Errorf("x-xolu-indexes[%d]: %q is the primary key and needs no index", i, f)
			}
			field := schema.GetField(f)
			if field == nil {
				return nil, fmt.Errorf("x-xolu-indexes[%d]: field %q is not a declared property", i, f)
			}
			if field.Format() == models.SchemaFormatREF {
				return nil, fmt.Errorf("x-xolu-indexes[%d]: field %q is a REF field; index it with x-xolu-index on the property", i, f)
			}
			if err := validateAdaptedFieldName(f); err != nil {
				return nil, fmt.Errorf("x-xolu-indexes[%d]: %w", i, err)
			}
		}
		prefix := "ix_"
		if d.Unique {
			prefix = "ux_"
		}
		out = append(out, IndexDef{
			Name:    tenantID.AdaptedNodeIndexField(entity, prefix+strings.Join(d.Fields, "_")),
			Columns: append([]string(nil), d.Fields...),
			Unique:  d.Unique,
		})
	}
	return out, nil
}

// ValidateSchemaIndexes checks a JSON Schema document's index keywords without
// touching a database, so a handler can answer a malformed one with a clean 400
// before anything is persisted.
func ValidateSchemaIndexes(schema map[string]interface{}) error {
	in := NewJSONSchemaIntrospector(schema)
	if in == nil {
		return nil
	}
	_, err := explicitIndexDefs("x", in, tenant.TenantID(0))
	return err
}

// uniqueViolationColumns reads the column names out of SQLite's
// "UNIQUE constraint failed: table.col1, table.col2" message. It returns nil if
// the error is not a unique-constraint failure.
func uniqueViolationColumns(err error) []string {
	const marker = "UNIQUE constraint failed: "
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return nil
	}
	tail := msg[i+len(marker):]
	// The driver appends the extended result code: "... table.col (2067)".
	if open := strings.LastIndex(tail, " ("); open >= 0 && strings.HasSuffix(tail, ")") {
		tail = tail[:open]
	}
	var cols []string
	for _, part := range strings.Split(tail, ",") {
		part = strings.TrimSpace(part)
		if dot := strings.LastIndex(part, "."); dot >= 0 {
			part = part[dot+1:]
		}
		if part != "" {
			cols = append(cols, part)
		}
	}
	return cols
}

// mapAdaptedWriteError turns a SQLite unique-constraint failure on an adapted
// table into a typed error: a duplicate primary key is ErrAlreadyExists, the same
// answer a blob entity gives; a duplicate of a declared unique index is a
// *UniqueViolationError. Any other error is wrapped as before.
func mapAdaptedWriteError(spec *AdaptedTableSpec, op string, err error) error {
	if cols := uniqueViolationColumns(err); cols != nil {
		if len(cols) == 1 && cols[0] == "id" {
			return ErrAlreadyExists
		}
		return &UniqueViolationError{Entity: spec.Entity, Columns: cols}
	}
	return fmt.Errorf("adapted %s %s failed: %w", op, spec.TableName(), err)
}
