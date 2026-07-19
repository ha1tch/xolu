// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package validation

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// LoadedEntities
// ---------------------------------------------------------------------------

func TestLoadedEntities_Empty(t *testing.T) {
	v := NewJSONSchemaValidator(t.TempDir())
	entities := v.LoadedEntities()
	if entities == nil {
		t.Error("LoadedEntities() returned nil, want empty slice")
	}
	if len(entities) != 0 {
		t.Errorf("LoadedEntities() = %v, want empty", entities)
	}
}

func TestLoadedEntities_AfterLoad(t *testing.T) {
	dir := t.TempDir()
	v := NewJSONSchemaValidator(dir)

	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"age":  map[string]interface{}{"type": "integer"},
		},
	}
	if err := v.LoadSchema("person", schema); err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	if err := v.LoadSchema("dept", map[string]interface{}{
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
	}); err != nil {
		t.Fatalf("LoadSchema dept: %v", err)
	}

	entities := v.LoadedEntities()
	if len(entities) != 2 {
		t.Errorf("LoadedEntities() len = %d, want 2; got %v", len(entities), entities)
	}
	found := map[string]bool{}
	for _, e := range entities {
		found[e] = true
	}
	if !found["person"] || !found["dept"] {
		t.Errorf("LoadedEntities() = %v, want [person dept] in any order", entities)
	}
}

// ---------------------------------------------------------------------------
// SaveSchema (JSONSchemaValidator)
// ---------------------------------------------------------------------------

func TestSaveSchema_CreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	v := NewJSONSchemaValidator(dir)

	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"title":  map[string]interface{}{"type": "string"},
			"rating": map[string]interface{}{"type": "integer"},
		},
	}

	if err := v.SaveSchema("book", schema); err != nil {
		t.Fatalf("SaveSchema: %v", err)
	}

	// File must exist on disk.
	schemaFile := filepath.Join(dir, "book.json")
	if _, err := os.Stat(schemaFile); os.IsNotExist(err) {
		t.Errorf("SaveSchema did not create %s", schemaFile)
	}

	// Schema must be loaded and validate correctly.
	validData := map[string]interface{}{"title": "Dune", "rating": 5}
	if ok, errs := v.Validate("book", validData); !ok {
		t.Errorf("Validate after SaveSchema: %v", errs)
	}

	invalidData := map[string]interface{}{"title": 123}
	if ok, _ := v.Validate("book", invalidData); ok {
		t.Error("Validate with wrong type: expected failure, got ok")
	}

	// SaveSchema is idempotent — saving again overwrites without error.
	if err := v.SaveSchema("book", schema); err != nil {
		t.Errorf("SaveSchema (idempotent): %v", err)
	}
}

func TestSaveSchema_NestedDir(t *testing.T) {
	// SchemaDir does not exist yet — SaveSchema must create it.
	base := t.TempDir()
	dir := filepath.Join(base, "schemas", "nested")
	v := NewJSONSchemaValidator(dir)

	schema := map[string]interface{}{
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
	}
	if err := v.SaveSchema("widget", schema); err != nil {
		t.Fatalf("SaveSchema (no dir): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "widget.json")); os.IsNotExist(err) {
		t.Error("SaveSchema did not create nested schema dir")
	}
}
