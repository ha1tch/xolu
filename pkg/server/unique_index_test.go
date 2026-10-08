// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// unique_index_test.go
//
// XOT236: unique and composite indexes declared in a schema, as a client sees
// them over HTTP.

import (
	"net/http"
	"testing"
)

func uniqueHistorySchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entity_id": map[string]interface{}{"type": "integer", "x-xolu-index": true},
			"version":   map[string]interface{}{"type": "integer"},
		},
		"required": []interface{}{"entity_id", "version"},
		"x-xolu-indexes": []interface{}{
			map[string]interface{}{"columns": []interface{}{"entity_id", "version"}, "unique": true},
		},
	}
}

func uniqueErrCode(result map[string]interface{}) interface{} {
	if e, ok := result["error"].(map[string]interface{}); ok {
		return e["code"]
	}
	return nil
}

func TestUniqueIndex_CommitRefusesADuplicateKeyAndRollsBack(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	if status, body := env.doJSON("POST", "/api/v1/schema/histx", uniqueHistorySchema()); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("register schema: %d %v", status, body)
	}
	env.doJSON("POST", "/api/v1/assets/save/7001", map[string]interface{}{"state": "A"})

	row := func(entity, version int) map[string]interface{} {
		return map[string]interface{}{"entity": "histx", "data": map[string]interface{}{"entity_id": entity, "version": version}}
	}
	commit := func(version int, rows ...map[string]interface{}) (int, map[string]interface{}) {
		appends := make([]interface{}, 0, len(rows))
		for _, r := range rows {
			appends = append(appends, r)
		}
		return env.doJSON("POST", commitURL(""), map[string]interface{}{
			"update": map[string]interface{}{"entity": "assets", "id": 7001, "version": version, "data": map[string]interface{}{"state": "B"}},
			"append": appends,
		})
	}
	if status, result := commit(1, row(7, 1)); status != http.StatusOK {
		t.Fatalf("first commit: %d %v", status, result)
	}
	// The same (entity_id, version) again, under a new id: refused as a unique violation.
	status, result := commit(2, row(7, 1))
	if status != http.StatusConflict || uniqueErrCode(result) != "XOLU-ST012" {
		t.Fatalf("duplicate key: status %d code %v, want 409 XOLU-ST012: %v", status, uniqueErrCode(result), result)
	}
	_, asset := env.doJSON("GET", "/api/v1/assets/7001", nil)
	if int(asset["_version"].(float64)) != 2 {
		t.Errorf("the update of a refused commit landed: version %v, want 2", asset["_version"])
	}
	// A different version of the same entity, and the same version of another entity, are fine.
	if status, result := commit(2, row(7, 2), row(8, 1)); status != http.StatusOK {
		t.Fatalf("distinct keys: %d %v", status, result)
	}
}

func TestUniqueIndex_CreateAndSaveAnswer409(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	env.doJSON("POST", "/api/v1/schema/histy", uniqueHistorySchema())
	body := map[string]interface{}{"entity_id": 3, "version": 1}
	if status, r := env.doJSON("POST", "/api/v1/histy", body); status != http.StatusCreated {
		t.Fatalf("first create: %d %v", status, r)
	}
	status, result := env.doJSON("POST", "/api/v1/histy", body)
	if status != http.StatusConflict || uniqueErrCode(result) != "XOLU-ST012" {
		t.Fatalf("duplicate create: status %d code %v, want 409 XOLU-ST012", status, uniqueErrCode(result))
	}
	status, result = env.doJSON("POST", "/api/v1/histy/save/99", body)
	if status != http.StatusConflict || uniqueErrCode(result) != "XOLU-ST012" {
		t.Fatalf("duplicate save: status %d code %v, want 409 XOLU-ST012", status, uniqueErrCode(result))
	}
}

func TestUniqueIndex_MalformedKeywordsAre400(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	for name, extra := range map[string]interface{}{
		"not an array":  "entity_id",
		"unknown field": []interface{}{map[string]interface{}{"columns": []interface{}{"nope"}}},
		"no fields":     []interface{}{map[string]interface{}{"unique": true}},
	} {
		schema := uniqueHistorySchema()
		schema["x-xolu-indexes"] = extra
		status, result := env.doJSON("POST", "/api/v1/schema/histz", schema)
		if status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %v", name, status, result)
		}
	}
}

// Adding a unique index to a type that already has rows that violate it is a 409,
// and the rows are untouched.
func TestUniqueIndex_AddingItOverDuplicateRowsIs409(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	plain := uniqueHistorySchema()
	delete(plain, "x-xolu-indexes")
	if status, r := env.doJSON("POST", "/api/v1/schema/histw", plain); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("register plain schema: %d %v", status, r)
	}
	for i := 0; i < 2; i++ {
		env.doJSON("POST", "/api/v1/histw", map[string]interface{}{"entity_id": 5, "version": 1})
	}
	status, result := env.doJSON("POST", "/api/v1/schema/histw", uniqueHistorySchema())
	if status != http.StatusConflict || uniqueErrCode(result) != "XOLU-ST012" {
		t.Fatalf("status %d code %v, want 409 XOLU-ST012: %v", status, uniqueErrCode(result), result)
	}
	_, list := env.doJSON("GET", "/api/v1/histw", nil)
	if data, _ := list["data"].([]interface{}); len(data) != 2 {
		t.Errorf("rows = %d after the refused migration, want 2", len(data))
	}
}
