// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// schema_guard_test.go
//
// XOT243 and XOT244: POST /schema/{entity} must neither hide rows that are
// already stored nor leave the server out of step with the table when the table
// cannot be changed.

import (
	"net/http"
	"testing"
)

// XOT244: a row written before any schema is a blob row. Registering a schema
// would create an empty adapted table and hide it, so the registration is
// refused, and nothing changes.
func TestSchemaRegistration_RefusesOverExistingBlobRows(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()

	row := map[string]interface{}{"entity_id": 1, "version": 1}
	if status, r := env.doJSON("POST", "/api/v1/xotblob", row); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("write before any schema: %d %v", status, r)
	}
	status, result := env.doJSON("POST", "/api/v1/schema/xotblob", uniqueHistorySchema())
	if status != http.StatusConflict {
		t.Fatalf("status %d, want 409: %v", status, result)
	}
	if status, _ := env.doJSON("GET", "/api/v1/schema/xotblob", nil); status != http.StatusNotFound {
		t.Errorf("GET schema after the refusal = %d, want 404 (nothing was registered)", status)
	}
	_, list := env.doJSON("GET", "/api/v1/xotblob", nil)
	if data, _ := list["data"].([]interface{}); len(data) != 1 {
		t.Errorf("rows = %d after the refusal, want 1", len(data))
	}
}

// XOT243: when the table cannot be migrated, the schema that was in force before
// the request stays in force, instead of the refused one being left loaded.
func TestSchemaRegistration_FailedMigrationLeavesThePreviousSchemaInForce(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()

	plain := uniqueHistorySchema()
	delete(plain, "x-xolu-indexes")
	if status, r := env.doJSON("POST", "/api/v1/schema/histq", plain); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("register plain schema: %d %v", status, r)
	}
	for i := 0; i < 2; i++ {
		env.doJSON("POST", "/api/v1/histq", map[string]interface{}{"entity_id": 5, "version": 1})
	}
	if status, r := env.doJSON("POST", "/api/v1/schema/histq", uniqueHistorySchema()); status != http.StatusConflict {
		t.Fatalf("status %d, want 409: %v", status, r)
	}
	_, got := env.doJSON("GET", "/api/v1/schema/histq", nil)
	if _, has := got["x-xolu-indexes"]; has {
		t.Errorf("the refused schema is in force after the failed migration: %v", got)
	}
}
