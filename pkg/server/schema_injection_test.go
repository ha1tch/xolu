// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http"
	"testing"
)

// D-009 (HTTP layer): a schema whose property key is not a bare SQL identifier
// must be rejected with a 400 before any persistence or adapted-table
// derivation. The property key would otherwise become a SQL column name in
// derived DDL (CREATE TABLE / ALTER TABLE / CREATE INDEX), which cannot be
// parameterised — a DDL-injection vector.
//
// This complements the storage-layer guard pinned by
// pkg/storage/adapted_injection_test.go (TestDeriveAdaptedSpec_MaliciousFieldName_DDL):
// that test proves no injected DDL is generated; this one proves the request is
// rejected cleanly at the boundary rather than failing late with a 500.
func TestHandleCreateSchema_MaliciousFieldName_Rejected(t *testing.T) {
	env := newE2EEnv(t)
	defer env.cleanup()

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type": "string",
			},
			"evil TEXT); DROP TABLE t0000_nodes;--": map[string]interface{}{
				"type": "string",
			},
		},
	}

	status, r := env.doJSON("POST", "/api/v1/schema/widget", schema)
	if status != http.StatusBadRequest {
		t.Fatalf("malicious schema field name: want 400, got %d: %v", status, r)
	}
}

// Control: a well-formed schema with valid identifier field names must still be
// accepted, so the D-009 guard does not over-reject legitimate input.
func TestHandleCreateSchema_ValidFieldNames_Accepted(t *testing.T) {
	env := newE2EEnv(t)
	defer env.cleanup()

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":      map[string]interface{}{"type": "string"},
			"unit_cost": map[string]interface{}{"type": "number"},
			"sku_code":  map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"name"},
	}

	status, r := env.doJSON("POST", "/api/v1/schema/gadget", schema)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("valid schema: want 200/201, got %d: %v", status, r)
	}
}
