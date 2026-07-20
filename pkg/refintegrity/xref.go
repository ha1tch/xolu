// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package refintegrity implements referential integrity for xolu's
// entity references, per docs/proposals/referential-integrity.md (@R).
//
// Stage 2 (this file + the delete-time restrict check) delivers the
// safety half of RI: a ref field annotated with x-ref and an on_delete
// policy of "restrict" causes a DELETE of the referenced entity to be
// refused while live referrers exist — the SQL ON DELETE RESTRICT
// behaviour. Cascade and nullify (stage 3) are not yet implemented; a
// policy of cascade or nullify parses correctly but is treated as
// unenforced-today by the stage-2 delete path, which is documented at
// the call site rather than silently swallowed.
package refintegrity

import "fmt"

// OnDelete is the delete-time policy for a reference field (@R02.1).
type OnDelete string

const (
	// OnDeleteRestrict refuses to delete a referenced entity while any
	// live referrer names it. The default when x-ref is present, because
	// it is the only policy that destroys nothing: refusal is
	// recoverable, deletion is not.
	OnDeleteRestrict OnDelete = "restrict"
	// OnDeleteCascade deletes referrers along with the target. Stage 3.
	OnDeleteCascade OnDelete = "cascade"
	// OnDeleteNullify sets the referring field to null. Stage 3.
	OnDeleteNullify OnDelete = "nullify"
)

// Valid reports whether the policy is one of the three known values.
func (p OnDelete) Valid() bool {
	switch p {
	case OnDeleteRestrict, OnDeleteCascade, OnDeleteNullify:
		return true
	default:
		return false
	}
}

// XRef is a parsed x-ref annotation on a schema field (@R02.1).
type XRef struct {
	// Field is the ref field's name on the referring entity.
	Field string
	// Entity is the referenced entity type. Required.
	Entity string
	// OnDelete is the delete policy; defaults to restrict when x-ref is
	// present but on_delete is omitted.
	OnDelete OnDelete
	// Validate is the write-time target-existence check (stage 4). Parsed
	// here so the annotation round-trips, but not enforced until stage 4.
	Validate bool
}

// ParseXRef reads an x-ref annotation from a field's raw metadata map
// (as returned by the schema browser's GetMeta("x-ref")). It returns
// ok=false when the field has no x-ref annotation. A malformed
// annotation (missing entity, unknown policy) is an error, not a silent
// skip — a schema author who wrote x-ref meant to enforce something.
func ParseXRef(field string, raw interface{}) (XRef, bool, error) {
	if raw == nil {
		return XRef{}, false, nil
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return XRef{}, false, fmt.Errorf("x-ref on field %q: expected an object, got %T", field, raw)
	}

	xr := XRef{Field: field, OnDelete: OnDeleteRestrict} // restrict is the default

	entity, ok := m["entity"].(string)
	if !ok || entity == "" {
		return XRef{}, false, fmt.Errorf("x-ref on field %q: \"entity\" is required and must be a non-empty string", field)
	}
	xr.Entity = entity

	if od, present := m["on_delete"]; present {
		s, ok := od.(string)
		if !ok {
			return XRef{}, false, fmt.Errorf("x-ref on field %q: \"on_delete\" must be a string", field)
		}
		p := OnDelete(s)
		if !p.Valid() {
			return XRef{}, false, fmt.Errorf("x-ref on field %q: unknown on_delete policy %q (want restrict|cascade|nullify)", field, s)
		}
		xr.OnDelete = p
	}

	if v, present := m["validate"]; present {
		b, ok := v.(bool)
		if !ok {
			return XRef{}, false, fmt.Errorf("x-ref on field %q: \"validate\" must be a boolean", field)
		}
		xr.Validate = b
	}

	return xr, true, nil
}
