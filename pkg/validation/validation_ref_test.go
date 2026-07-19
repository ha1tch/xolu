// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package validation

// validation_ref_test.go — tests for REF field validation (custom queryfy
// validator wired via applyREFValidators) and LoadSchemaWithWarnings.

import (
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/models"
)

// ── constants ─────────────────────────────────────────────────────────────────

func TestRefConstants(t *testing.T) {
	t.Parallel()
	// The constants must be the exact strings used in the JSON data model.
	if models.RefTypeValue != "REF" {
		t.Errorf("RefTypeValue = %q, want \"REF\"", models.RefTypeValue)
	}
	if models.SchemaFormatREF != "ref" {
		t.Errorf("SchemaFormatREF = %q, want \"ref\"", models.SchemaFormatREF)
	}
}

// ── validateREFValue unit tests ───────────────────────────────────────────────

func TestValidateREFValue_Valid(t *testing.T) {
	t.Parallel()
	cases := []interface{}{
		map[string]interface{}{"type": "REF", "entity": "users", "id": float64(1)},
		map[string]interface{}{"type": "REF", "entity": "products", "id": float64(42)},
		map[string]interface{}{"type": "REF", "entity": "users", "id": 7},
		map[string]interface{}{"type": "REF", "entity": "users", "id": int64(99)},
	}
	for _, v := range cases {
		if err := validateREFValue(v); err != nil {
			t.Errorf("validateREFValue(%v): unexpected error: %v", v, err)
		}
	}
}

func TestValidateREFValue_Nil(t *testing.T) {
	t.Parallel()
	// nil is handled by CheckRequired, not the validator.
	if err := validateREFValue(nil); err != nil {
		t.Errorf("validateREFValue(nil): unexpected error: %v", err)
	}
}

func TestValidateREFValue_Invalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value interface{}
	}{
		{"string", "not-a-ref"},
		{"integer", float64(42)},
		{"missing entity", map[string]interface{}{"type": "REF", "id": float64(1)}},
		{"missing id", map[string]interface{}{"type": "REF", "entity": "users"}},
		{"wrong type value", map[string]interface{}{"type": "LINK", "entity": "users", "id": float64(1)}},
		{"empty entity", map[string]interface{}{"type": "REF", "entity": "", "id": float64(1)}},
		{"empty map", map[string]interface{}{}},
	}
	for _, tc := range cases {
		if err := validateREFValue(tc.value); err == nil {
			t.Errorf("validateREFValue(%s): expected error, got nil", tc.name)
		}
	}
}

// ── schema-level REF validation ───────────────────────────────────────────────

func refSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{"type": "string"},
			// required REF
			"author": map[string]interface{}{"type": "object", "format": "ref"},
			// optional REF
			"editor": map[string]interface{}{"type": "object", "format": "ref"},
		},
		"required": []interface{}{"title", "author"},
	}
}

func TestValidate_REFField_Valid(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	valid, errs := v.Validate("articles", map[string]interface{}{
		"title":  "Hello World",
		"author": map[string]interface{}{"type": "REF", "entity": "users", "id": float64(1)},
	})
	if !valid {
		t.Errorf("expected valid, got errors: %v", errs)
	}
}

func TestValidate_REFField_WithOptional(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	// Optional REF provided and valid.
	valid, errs := v.Validate("articles", map[string]interface{}{
		"title":  "Hello",
		"author": map[string]interface{}{"type": "REF", "entity": "users", "id": float64(1)},
		"editor": map[string]interface{}{"type": "REF", "entity": "users", "id": float64(2)},
	})
	if !valid {
		t.Errorf("expected valid with optional REF, got errors: %v", errs)
	}
}

func TestValidate_REFField_OptionalAbsent(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	// Optional REF absent entirely — must pass.
	valid, errs := v.Validate("articles", map[string]interface{}{
		"title":  "Hello",
		"author": map[string]interface{}{"type": "REF", "entity": "users", "id": float64(1)},
	})
	if !valid {
		t.Errorf("expected valid when optional REF absent, got errors: %v", errs)
	}
}

func TestValidate_REFField_OptionalNil(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	// Explicit nil for optional REF must pass.
	valid, errs := v.Validate("articles", map[string]interface{}{
		"title":  "Hello",
		"author": map[string]interface{}{"type": "REF", "entity": "users", "id": float64(1)},
		"editor": nil,
	})
	if !valid {
		t.Errorf("expected valid for nil optional REF, got errors: %v", errs)
	}
}

func TestValidate_REFField_RequiredMissing(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	valid, errs := v.Validate("articles", map[string]interface{}{
		"title": "Hello",
		// author missing
	})
	if valid {
		t.Error("expected invalid when required REF missing")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e, "author") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error mentioning author, got: %v", errs)
	}
}

func TestValidate_REFField_WrongType(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	valid, errs := v.Validate("articles", map[string]interface{}{
		"title":  "Hello",
		"author": "not-a-map",
	})
	if valid {
		t.Error("expected invalid when REF is string instead of map")
	}
	if len(errs) == 0 {
		t.Error("expected error messages")
	}
}

func TestValidate_REFField_InvalidMap_MissingEntity(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	// Map looks like a REF but missing "entity" field.
	valid, errs := v.Validate("articles", map[string]interface{}{
		"title":  "Hello",
		"author": map[string]interface{}{"type": "REF", "id": float64(1)},
	})
	if valid {
		t.Error("expected invalid for REF map missing entity")
	}
	if len(errs) == 0 {
		t.Error("expected error messages")
	}
}

func TestValidate_REFField_InvalidMap_WrongTypeValue(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	// Map has wrong "type" value.
	valid, errs := v.Validate("articles", map[string]interface{}{
		"title":  "Hello",
		"author": map[string]interface{}{"type": "LINK", "entity": "users", "id": float64(1)},
	})
	if valid {
		t.Error("expected invalid when type != REF")
	}
	if len(errs) == 0 {
		t.Error("expected error messages")
	}
}

// ── LoadSchemaWithWarnings ─────────────────────────────────────────────────────

func TestLoadSchemaWithWarnings_Clean(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	warnings, err := v.LoadSchemaWithWarnings("articles", refSchema())
	if err != nil {
		t.Fatalf("LoadSchemaWithWarnings: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for clean schema, got: %v", warnings)
	}
}

func TestLoadSchemaWithWarnings_RefSuffix(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")

	// Field named "author_ref" should trigger a warning.
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title":      map[string]interface{}{"type": "string"},
			"author_ref": map[string]interface{}{"type": "object", "format": "ref"},
		},
		"required": []interface{}{"title"},
	}
	warnings, err := v.LoadSchemaWithWarnings("articles", schema)
	if err != nil {
		t.Fatalf("LoadSchemaWithWarnings: %v", err)
	}
	if len(warnings) == 0 {
		t.Error("expected warning for REF field ending in _ref")
	}

	// Warning should mention the field name.
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "author_ref") {
			found = true
		}
	}
	if !found {
		t.Errorf("warning should mention 'author_ref', got: %v", warnings)
	}
}

func TestLoadSchemaWithWarnings_MultipleRefSuffix(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")

	// Two fields with _ref suffix — two warnings.
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"author_ref": map[string]interface{}{"type": "object", "format": "ref"},
			"editor_ref": map[string]interface{}{"type": "object", "format": "ref"},
		},
	}
	warnings, err := v.LoadSchemaWithWarnings("articles", schema)
	if err != nil {
		t.Fatalf("LoadSchemaWithWarnings: %v", err)
	}
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestLoadSchemaWithWarnings_NoRefSuffix_NoWarning(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")

	// "author" has format:ref but no _ref suffix — no warning.
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"author": map[string]interface{}{"type": "object", "format": "ref"},
		},
	}
	warnings, err := v.LoadSchemaWithWarnings("articles", schema)
	if err != nil {
		t.Fatalf("LoadSchemaWithWarnings: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings for clean field name: %v", warnings)
	}
}

func TestLoadSchema_StillWorks(t *testing.T) {
	t.Parallel()
	// LoadSchema must not return an error for a schema that only produces warnings.
	v := NewJSONSchemaValidator("")
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"author_ref": map[string]interface{}{"type": "object", "format": "ref"},
		},
	}
	if err := v.LoadSchema("articles", schema); err != nil {
		t.Errorf("LoadSchema should succeed even with naming warnings: %v", err)
	}
	if !v.HasSchema("articles") {
		t.Error("schema should be loaded despite naming warning")
	}
}

// ── REF validation still allows non-REF extra fields ─────────────────────────

func TestValidate_REFSchema_AllowsExtraFields(t *testing.T) {
	t.Parallel()
	v := NewJSONSchemaValidator("")
	if err := v.LoadSchema("articles", refSchema()); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	// Extra fields beyond the schema should be allowed (HasExtra=true default).
	valid, errs := v.Validate("articles", map[string]interface{}{
		"title":    "Hello",
		"author":   map[string]interface{}{"type": "REF", "entity": "users", "id": float64(1)},
		"subtitle": "World",
		"tags":     []interface{}{"go", "xolu"},
	})
	if !valid {
		t.Errorf("expected extra fields to be allowed, got errors: %v", errs)
	}
}
