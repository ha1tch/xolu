// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package refintegrity

import "sort"

// FieldXRefMeta is the field name paired with its raw x-ref annotation
// value (as returned by a schema introspector's Meta("x-ref") lookup, or
// nil if the field has none). The caller — pkg/storage or the server,
// which own the concrete schema types — extracts these and hands them to
// CollectXRefs, keeping refintegrity free of any schema-package import
// and the import cycle that would create.
type FieldXRefMeta struct {
	Field string
	// Raw is the value of the field's "x-ref" metadata key, or nil if the
	// field carries no x-ref annotation.
	Raw interface{}
}

// FieldsFromRawSchema extracts field/x-ref pairs from a raw JSON Schema
// map of the shape {"properties": {"<field>": {"x-ref": {...}}}} — the
// form pkg/validation's GetSchema returns. Fields without an x-ref key
// are omitted (only annotated fields matter to the collector). A schema
// with no "properties" object yields nil, not an error: an entity may
// legitimately have no reference fields.
func FieldsFromRawSchema(raw map[string]interface{}) []FieldXRefMeta {
	props, ok := raw["properties"].(map[string]interface{})
	if !ok {
		return nil
	}
	var out []FieldXRefMeta
	for field, fs := range props {
		fsMap, ok := fs.(map[string]interface{})
		if !ok {
			continue
		}
		if xr, present := fsMap["x-ref"]; present {
			out = append(out, FieldXRefMeta{Field: field, Raw: xr})
		}
	}
	return out
}

// CollectXRefs parses every x-ref annotation from a schema's fields,
// sorted by field name for determinism. A malformed annotation aborts
// the whole collection with an error — a schema with a broken x-ref must
// not load as if the annotation were absent, because that would silently
// disable an enforcement the author asked for.
func CollectXRefs(fields []FieldXRefMeta) ([]XRef, error) {
	var out []XRef
	for _, f := range fields {
		if f.Raw == nil {
			continue
		}
		xr, present, err := ParseXRef(f.Field, f.Raw)
		if err != nil {
			return nil, err
		}
		if present {
			out = append(out, xr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out, nil
}

// ReferrerPolicy names one field on one entity type that references a
// target entity, and the on_delete policy governing that reference. It
// is the unit the delete path consults: "who points at E, and what must
// happen to them when E is deleted."
type ReferrerPolicy struct {
	// ReferringEntity is the entity type that carries the ref field.
	ReferringEntity string
	// Field is the ref field's name on the referring entity.
	Field string
	// OnDelete is the policy governing a delete of the referenced entity.
	OnDelete OnDelete
}

// Registry maps a referenced entity type to the set of referrer
// policies that target it. It is built once from the loaded schemas and
// consulted at delete time. A nil or empty registry enforces nothing —
// the safe default for a deployment that has adopted no x-ref
// annotations.
type Registry struct {
	// byTarget[targetEntity] = policies that reference targetEntity.
	byTarget map[string][]ReferrerPolicy
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{byTarget: make(map[string][]ReferrerPolicy)}
}

// AddEntitySchema records every x-ref on referringEntity's schema,
// indexing each by the entity it targets. Call once per entity type as
// schemas load. `fields` is the entity's field-name/x-ref-meta pairs,
// which the caller extracts from its concrete schema type. Returns an
// error if the schema carries a malformed x-ref.
func (r *Registry) AddEntitySchema(referringEntity string, fields []FieldXRefMeta) error {
	xrefs, err := CollectXRefs(fields)
	if err != nil {
		return err
	}
	for _, xr := range xrefs {
		r.byTarget[xr.Entity] = append(r.byTarget[xr.Entity], ReferrerPolicy{
			ReferringEntity: referringEntity,
			Field:           xr.Field,
			OnDelete:        xr.OnDelete,
		})
	}
	return nil
}

// ReferrersOf returns the policies whose reference targets the given
// entity type. The result is read-only; callers must not mutate it.
// Returns nil when nothing references the entity.
func (r *Registry) ReferrersOf(targetEntity string) []ReferrerPolicy {
	if r == nil {
		return nil
	}
	return r.byTarget[targetEntity]
}

// HasRestrictReferrers reports whether any referrer of targetEntity
// carries the restrict policy — the cheap pre-check the delete path
// uses to decide whether an inbound-edge query is even necessary.
func (r *Registry) HasRestrictReferrers(targetEntity string) bool {
	if r == nil {
		return false
	}
	for _, p := range r.byTarget[targetEntity] {
		if p.OnDelete == OnDeleteRestrict {
			return true
		}
	}
	return false
}
