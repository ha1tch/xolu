// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package validation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ha1tch/queryfy"
	"github.com/ha1tch/queryfy/builders"
	"github.com/ha1tch/queryfy/builders/jsonschema"
	"github.com/ha1tch/xolu/pkg/models"
)

// Validator interface defines validation operations
type Validator interface {
	Validate(entity string, data map[string]interface{}) (bool, []string)
	LoadSchema(entity string, schemaData map[string]interface{}) error
	SaveSchema(entity string, schemaData map[string]interface{}) error
	HasSchema(entity string) bool
	GetSchema(entity string) (map[string]interface{}, error)
	LoadedEntities() []string
}

// entitySchema holds both the raw JSON Schema (for GetSchema and adapted
// tables) and the compiled queryfy schema (for validation).
type entitySchema struct {
	raw      map[string]interface{}
	compiled *builders.ObjectSchema
}

// JSONSchemaValidator implements JSON schema validation using queryfy.
//
// Schemas are stored in two forms: the raw JSON Schema map (returned by
// GetSchema, consumed by the adapted tables layer) and a compiled queryfy
// ObjectSchema (used for validation). Compilation happens once at load
// time via jsonschema.FromJSON.
//
// Validation runs in Loose mode by default: type coercion is applied
// (e.g., integer values pass for number fields) and additional properties
// are allowed unless the schema explicitly sets additionalProperties:false.
type JSONSchemaValidator struct {
	schemas   map[string]*entitySchema
	schemaDir string
	mu        sync.RWMutex
}

// NewJSONSchemaValidator creates a new JSON schema validator
func NewJSONSchemaValidator(schemaDir string) *JSONSchemaValidator {
	return &JSONSchemaValidator{
		schemas:   make(map[string]*entitySchema),
		schemaDir: schemaDir,
	}
}

// LoadSchema loads a schema for an entity. The raw map is stored for
// GetSchema; a compiled queryfy schema is built for validation.
// LoadSchema loads a schema for an entity.
// It is equivalent to LoadSchemaWithWarnings and discards any warnings.
// Use LoadSchemaWithWarnings to retrieve naming convention warnings.
func (v *JSONSchemaValidator) LoadSchema(entity string, schemaData map[string]interface{}) error {
	_, err := v.LoadSchemaWithWarnings(entity, schemaData)
	return err
}

// LoadSchemaWithWarnings loads a schema for an entity and returns any naming
// convention warnings alongside the error. A non-nil error means the schema
// was rejected; warnings are informational and the schema is still loaded.
//
// Currently the only warning is when a REF field (format:"ref") is named with
// a "_ref" suffix: the resulting SQL columns and graph relationship label will
// carry the redundant suffix, which is valid but potentially confusing.
func (v *JSONSchemaValidator) LoadSchemaWithWarnings(entity string, schemaData map[string]interface{}) ([]string, error) {
	// Compile to queryfy schema.
	raw, err := json.Marshal(schemaData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal schema for %q: %w", entity, err)
	}

	compiled, convErrs := jsonschema.FromJSON(raw, &jsonschema.Options{
		StoreUnknown: true,
	})
	for _, e := range convErrs {
		if !e.IsWarning {
			return nil, fmt.Errorf("schema compilation error for %q at %s: %s",
				entity, e.Path, e.Message)
		}
	}

	obj, ok := compiled.(*builders.ObjectSchema)
	if !ok {
		return nil, fmt.Errorf("schema for %q did not compile to an ObjectSchema (got %T)",
			entity, compiled)
	}

	// xolu default: allow extra properties unless the schema explicitly
	// sets additionalProperties:false.
	setDefaultAllowAdditional(obj)

	// Post-compilation fixup: replace fields carrying "format":"ref" in the raw
	// schema with a CustomSchema that validates the xolu REF structure.
	// The raw schema is authoritative because queryfy's FromJSON discards
	// "format" on object-typed fields.
	rawProps, _ := schemaData["properties"].(map[string]interface{})
	applyREFValidators(obj, rawProps)

	// Convention check: warn when a REF field name ends in "_ref".
	// Such a field produces SQL columns REF_fieldname_ref_entity / REF_fieldname_ref_id
	// and a graph relationship label "fieldname_ref" — valid but redundant-looking.
	var warnings []string
	for fieldName, rawFieldVal := range rawProps {
		rawField, _ := rawFieldVal.(map[string]interface{})
		if rawField == nil {
			continue
		}
		if fmtVal, _ := rawField["format"].(string); fmtVal == models.SchemaFormatREF {
			if strings.HasSuffix(fieldName, "_ref") {
				warnings = append(warnings, fmt.Sprintf(
					"schema %q: REF field %q ends in \"_ref\" — "+
						"SQL columns will be REF_%s_entity / REF_%s_id and the "+
						"graph relationship label will be %q; "+
						"consider renaming the field to remove the redundant suffix",
					entity, fieldName, fieldName, fieldName, fieldName,
				))
			}
		}
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	v.schemas[entity] = &entitySchema{
		raw:      schemaData,
		compiled: obj,
	}
	return warnings, nil
}

// LoadSchemaFromFile loads a schema from a file
func (v *JSONSchemaValidator) LoadSchemaFromFile(entity string) error {
	schemaFile := filepath.Join(v.schemaDir, entity+".json")

	data, err := os.ReadFile(schemaFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No schema file, validation passes
		}
		return err
	}

	var schemaData map[string]interface{}
	if err := json.Unmarshal(data, &schemaData); err != nil {
		return err
	}

	return v.LoadSchema(entity, schemaData)
}

// HasSchema checks if a schema exists for an entity
func (v *JSONSchemaValidator) HasSchema(entity string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()

	_, exists := v.schemas[entity]
	return exists
}

// GetSchema retrieves the raw JSON Schema for an entity.
// This is used by the adapted tables layer and the schema API endpoint.
func (v *JSONSchemaValidator) GetSchema(entity string) (map[string]interface{}, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	es, exists := v.schemas[entity]
	if !exists {
		return nil, fmt.Errorf("schema not found for entity: %s", entity)
	}
	return es.raw, nil
}

// LoadedEntities returns the names of all entities that have a loaded schema.
func (v *JSONSchemaValidator) LoadedEntities() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	entities := make([]string, 0, len(v.schemas))
	for name := range v.schemas {
		entities = append(entities, name)
	}
	return entities
}

// GetCompiledSchema retrieves the compiled queryfy schema for an entity.
// Returns nil if no schema is loaded.
func (v *JSONSchemaValidator) GetCompiledSchema(entity string) *builders.ObjectSchema {
	v.mu.RLock()
	defer v.mu.RUnlock()

	es, exists := v.schemas[entity]
	if !exists {
		return nil
	}
	return es.compiled
}

// Validate validates data against a schema using queryfy in Loose mode.
//
// Returns (true, nil) if validation passes or no schema exists.
// Returns (false, errors) with human-readable error strings on failure.
func (v *JSONSchemaValidator) Validate(entity string, data map[string]interface{}) (bool, []string) {
	v.mu.RLock()
	es, exists := v.schemas[entity]
	v.mu.RUnlock()

	if !exists {
		// No schema means validation passes
		return true, nil
	}

	ctx := queryfy.NewValidationContext(queryfy.Strict)
	_ = es.compiled.Validate(data, ctx)

	if !ctx.HasErrors() {
		return true, nil
	}

	// Convert queryfy field errors to string slice
	fieldErrors := ctx.Errors()
	errors := make([]string, 0, len(fieldErrors))
	for _, fe := range fieldErrors {
		if fe.Path == "" {
			errors = append(errors, fe.Message)
		} else {
			errors = append(errors, fmt.Sprintf("%s: %s", fe.Path, fe.Message))
		}
	}

	return false, errors
}

// LoadAllSchemas loads all schemas from the schema directory
func (v *JSONSchemaValidator) LoadAllSchemas() error {
	if _, err := os.Stat(v.schemaDir); os.IsNotExist(err) {
		return nil // Schema directory doesn't exist yet
	}

	files, err := os.ReadDir(v.schemaDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}

		entity := file.Name()[:len(file.Name())-5] // Remove .json extension
		if err := v.LoadSchemaFromFile(entity); err != nil {
			return fmt.Errorf("failed to load schema for %s: %w", entity, err)
		}
	}

	return nil
}

// SaveSchema saves a schema to a file
func (v *JSONSchemaValidator) SaveSchema(entity string, schemaData map[string]interface{}) error {
	if err := os.MkdirAll(v.schemaDir, 0755); err != nil {
		return err
	}

	schemaFile := filepath.Join(v.schemaDir, entity+".json")
	data, err := json.MarshalIndent(schemaData, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(schemaFile, data, 0644); err != nil {
		return err
	}

	return v.LoadSchema(entity, schemaData)
}

// applyREFValidators walks a compiled ObjectSchema and replaces any field
// carrying Meta("format", models.SchemaFormatREF) with a CustomSchema that
// validates the xolu REF structure.
//
// This is a post-compilation fixup. queryfy's FromJSON stores "format":"ref"
// as metadata on object fields without applying any validation, because format
// validators only apply to string schemas. This function closes that gap.
// applyREFValidators walks a compiled ObjectSchema alongside the raw JSON Schema
// properties map and replaces any field declared with "format":"ref" with a
// CustomSchema that validates the xolu REF structure.
//
// queryfy's FromJSON discards "format":"ref" on object fields (format is only
// processed for string fields), so the raw schema is the authoritative source.
func applyREFValidators(obj *builders.ObjectSchema, rawProps map[string]interface{}) {
	if rawProps == nil {
		return
	}
	for fieldName, rawFieldVal := range rawProps {
		rawField, ok := rawFieldVal.(map[string]interface{})
		if !ok {
			continue
		}

		// Check for format:ref on this field in the raw schema.
		if fmtVal, hasFmt := rawField["format"]; hasFmt {
			if fmtStr, ok := fmtVal.(string); ok && fmtStr == models.SchemaFormatREF {
				existingField, exists := obj.GetField(fieldName)
				isRequired := false
				if exists {
					type requiredChecker interface{ IsRequired() bool }
					if rc, ok := existingField.(requiredChecker); ok {
						isRequired = rc.IsRequired()
					}
				}
				cs := builders.Custom(validateREFValue)
				if isRequired {
					cs.Required()
				} else {
					cs.Optional().Nullable()
				}
				cs.Meta("format", models.SchemaFormatREF)
				obj.Field(fieldName, cs)
				continue
			}
		}

		// Recurse into nested object fields.
		if fieldType, _ := rawField["type"].(string); fieldType == "object" {
			if nestedProps, ok := rawField["properties"].(map[string]interface{}); ok {
				if nestedSchema, ok := obj.GetField(fieldName); ok {
					if nestedObj, ok := nestedSchema.(*builders.ObjectSchema); ok {
						applyREFValidators(nestedObj, nestedProps)
					}
				}
			}
		}
	}
}

// validateREFValue is the queryfy ValidatorFunc for REF fields.
// A valid REF is a map with:
//   - "type" == models.RefTypeValue ("REF")
//   - "entity": non-empty string
//   - "id": integer or float64 representable as integer
//
// A nil value passes (handled by the CustomSchema's CheckRequired mechanism).
func validateREFValue(value interface{}) error {
	if value == nil {
		return nil
	}
	ref, ok := models.IsReference(value)
	if !ok {
		return fmt.Errorf(
			"invalid REF value: expected {\"type\":%q,\"entity\":\"<name>\",\"id\":<int>}, got %T",
			models.RefTypeValue, value,
		)
	}
	// IsReference accepts a present-but-empty entity key; we require non-empty.
	if ref.Entity == "" {
		return fmt.Errorf("invalid REF value: \"entity\" must be a non-empty string")
	}
	return nil
}

// setDefaultAllowAdditional recursively sets AllowAdditional(true) on
// every ObjectSchema in the tree that hasn't explicitly declared a policy.
// This ensures Strict mode (used for type checking) doesn't accidentally
// reject extra fields on schemas that didn't opt into that behaviour.
func setDefaultAllowAdditional(obj *builders.ObjectSchema) {
	if _, explicit := obj.AllowsAdditional(); !explicit {
		obj.AllowAdditional(true)
	}

	// Recurse into nested ObjectSchemas
	for _, name := range obj.FieldNames() {
		fieldSchema, ok := obj.GetField(name)
		if !ok {
			continue
		}
		if nested, ok := fieldSchema.(*builders.ObjectSchema); ok {
			setDefaultAllowAdditional(nested)
		}
		// Array items that are objects
		if arr, ok := fieldSchema.(*builders.ArraySchema); ok {
			if elem := arr.ElementSchema(); elem != nil {
				if nestedObj, ok := elem.(*builders.ObjectSchema); ok {
					setDefaultAllowAdditional(nestedObj)
				}
			}
		}
	}
}

// NoOpValidator is a validator that always passes
type NoOpValidator struct{}

// NewNoOpValidator creates a no-op validator
func NewNoOpValidator() *NoOpValidator {
	return &NoOpValidator{}
}

// Validate always returns true
func (n *NoOpValidator) Validate(entity string, data map[string]interface{}) (bool, []string) {
	return true, nil
}

// LoadSchema is a no-op
func (n *NoOpValidator) LoadSchema(entity string, schemaData map[string]interface{}) error {
	return nil
}

// SaveSchema is a no-op
func (n *NoOpValidator) SaveSchema(entity string, schemaData map[string]interface{}) error {
	return nil
}

// HasSchema always returns false
func (n *NoOpValidator) HasSchema(entity string) bool {
	return false
}

// GetSchema always returns error
// LoadedEntities returns an empty slice: the no-op validator tracks nothing.
func (n *NoOpValidator) LoadedEntities() []string {
	return []string{}
}

func (n *NoOpValidator) GetSchema(entity string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("no-op validator has no schemas")
}
