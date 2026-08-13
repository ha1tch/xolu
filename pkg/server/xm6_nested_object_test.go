// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestXM6_NestedObjectField_AdaptedEntity is xoluman's own XM-6
// report, end to end: a plain nested (non-REF) object field
// (`address`, matching their own real `companies` schema exactly --
// no `format: ref`) used to fail outright on create against an
// adapted entity, with the identical error text xoluman reported
// ("unsupported type map[string]interface {}, a map"). Root cause
// confirmed and fixed in pkg/storage/adapted.go's own PartitionData;
// unit-tested there directly (TestAdaptedCRUD_NestedObjectField).
// This test proves the fix at the level xoluman actually hit it --
// a real HTTP request against a real, schema-adapted tenant.
func TestXM6_NestedObjectField_AdaptedEntity(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":     map[string]interface{}{"type": "string"},
			"industry": map[string]interface{}{"type": "string"},
			"address": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"street":  map[string]interface{}{"type": "string"},
					"city":    map[string]interface{}{"type": "string"},
					"state":   map[string]interface{}{"type": "string"},
					"country": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
	status, resp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/companies", schema)
	if status != http.StatusCreated {
		t.Fatalf("schema registration: want 201, got %d %v", status, resp)
	}

	status, created := doJSONRequest(t, "POST", base+"/companies", map[string]interface{}{
		"name": "With Address Co", "industry": "tech",
		"address": map[string]interface{}{
			"street": "1 Main St", "city": "X", "state": "Y", "country": "Z",
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("create with nested address: want 201, got %d %v (this is XM-6's own reported failure mode if non-201)", status, created)
	}

	status, fetched := doJSONRequest(t, "GET", fmt.Sprintf("%s/companies/%v", base, created["id"]), nil)
	if status != http.StatusOK {
		t.Fatalf("fetch: want 200, got %d %v", status, fetched)
	}
	address, ok := fetched["address"].(map[string]interface{})
	if !ok {
		t.Fatalf("address: want map[string]interface{}, got %T: %v", fetched["address"], fetched["address"])
	}
	if address["street"] != "1 Main St" || address["city"] != "X" {
		t.Errorf("address round-trip mismatch: got %+v", address)
	}

	// A company without an address must still work (the field is
	// optional) -- the original bug's own "omit address, succeeds"
	// isolation, confirmed still holds after the fix, not just the
	// with-address case.
	status, noAddr := doJSONRequest(t, "POST", base+"/companies", map[string]interface{}{
		"name": "No Address Co", "industry": "retail",
	})
	if status != http.StatusCreated {
		t.Fatalf("create without address: want 201, got %d %v", status, noAddr)
	}
}
