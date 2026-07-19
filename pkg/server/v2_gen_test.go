// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_gen_test.go
//
// Tests for S4: stateless generator endpoints and OQL scalar functions.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// genURL builds a v2 gen URL on a stdTestServer.
func genURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/gen%s", sts.ts.URL, path)
}

// ─── Availability ─────────────────────────────────────────────────────────────

func TestGen_AvailabilityMapShowsGen(t *testing.T) {
	env := newV2Server(t)
	_, resp := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/", env.ts.URL), nil)
	subsystems, _ := resp["subsystems"].(map[string]interface{})
	gen, _ := subsystems["gen"].(map[string]interface{})
	if gen["available"] != true {
		t.Errorf("gen subsystem: want available=true, got %v", gen["available"])
	}
}

// ─── HTTP endpoints ───────────────────────────────────────────────────────────

func TestGen_UUIDv4_Shape(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET", genURL(env, "/uuid_v4"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /gen/uuid_v4: want 200, got %d: %v", status, resp)
	}
	if resp["type"] != "uuid_v4" {
		t.Errorf("type: want 'uuid_v4', got %v", resp["type"])
	}
	val, _ := resp["value"].(string)
	if len(val) != 36 {
		t.Errorf("uuid_v4 value: want 36 chars, got %d: %q", len(val), val)
	}
	if resp["generated_at"] == nil {
		t.Errorf("generated_at missing from response")
	}
}

func TestGen_UUIDv4_Unique(t *testing.T) {
	env := newV2Server(t)
	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		_, resp := doJSONRequest(t, "GET", genURL(env, "/uuid_v4"), nil)
		v, _ := resp["value"].(string)
		if seen[v] {
			t.Errorf("uuid_v4 collision at iteration %d: %q", i, v)
		}
		seen[v] = true
	}
}

func TestGen_UUIDv4_Version(t *testing.T) {
	env := newV2Server(t)
	_, resp := doJSONRequest(t, "GET", genURL(env, "/uuid_v4"), nil)
	val, _ := resp["value"].(string)
	// UUID v4 has version nibble = '4' at position 14.
	if len(val) >= 15 && string(val[14]) != "4" {
		t.Errorf("uuid_v4 version nibble: want '4', got %q at pos 14 of %q", string(val[14]), val)
	}
}

func TestGen_UUIDv7_Shape(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET", genURL(env, "/uuid_v7"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /gen/uuid_v7: want 200, got %d: %v", status, resp)
	}
	val, _ := resp["value"].(string)
	if len(val) != 36 {
		t.Errorf("uuid_v7 value: want 36 chars, got %d", len(val))
	}
}

func TestGen_UUIDv7_Monotonic(t *testing.T) {
	// UUID v7 values should be monotonically increasing (lexicographic).
	env := newV2Server(t)
	var prev string
	for i := 0; i < 5; i++ {
		_, resp := doJSONRequest(t, "GET", genURL(env, "/uuid_v7"), nil)
		v, _ := resp["value"].(string)
		if prev != "" && v <= prev {
			t.Errorf("uuid_v7 not monotonic at %d: %q <= %q", i, v, prev)
		}
		prev = v
	}
}

func TestGen_UUIDv7_Version(t *testing.T) {
	env := newV2Server(t)
	_, resp := doJSONRequest(t, "GET", genURL(env, "/uuid_v7"), nil)
	val, _ := resp["value"].(string)
	if len(val) >= 15 && string(val[14]) != "7" {
		t.Errorf("uuid_v7 version nibble: want '7', got %q", string(val[14]))
	}
}

func TestGen_CUID_Shape(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET", genURL(env, "/cuid"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /gen/cuid: want 200, got %d: %v", status, resp)
	}
	val, _ := resp["value"].(string)
	if !strings.HasPrefix(val, "c") {
		t.Errorf("cuid: want prefix 'c', got %q", val)
	}
	if len(val) < 20 {
		t.Errorf("cuid: want length >= 20, got %d: %q", len(val), val)
	}
}

func TestGen_CUID_Unique(t *testing.T) {
	env := newV2Server(t)
	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		_, resp := doJSONRequest(t, "GET", genURL(env, "/cuid"), nil)
		v, _ := resp["value"].(string)
		if seen[v] {
			t.Errorf("cuid collision at iteration %d: %q", i, v)
		}
		seen[v] = true
	}
}

func TestGen_ULID_Shape(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "GET", genURL(env, "/ulid"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /gen/ulid: want 200, got %d: %v", status, resp)
	}
	val, _ := resp["value"].(string)
	// ULID is 26 characters of Crockford base32.
	if len(val) != 26 {
		t.Errorf("ulid: want 26 chars, got %d: %q", len(val), val)
	}
}

func TestGen_ULID_Monotonic(t *testing.T) {
	env := newV2Server(t)
	var prev string
	for i := 0; i < 5; i++ {
		_, resp := doJSONRequest(t, "GET", genURL(env, "/ulid"), nil)
		v, _ := resp["value"].(string)
		if prev != "" && v < prev {
			t.Errorf("ulid not monotonic at %d: %q < %q", i, v, prev)
		}
		prev = v
	}
}

// ─── Stability header ─────────────────────────────────────────────────────────

func TestGen_StabilityHeader(t *testing.T) {
	env := newV2Server(t)
	resp, err := http.Get(genURL(env, "/uuid_v4"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("X-API-Stability"); got != "experimental" {
		t.Errorf("X-API-Stability: want 'experimental', got %q", got)
	}
}

// ─── Disabled ─────────────────────────────────────────────────────────────────

func TestGen_DisabledReturns404(t *testing.T) {
	env := newV1OnlyServer(t)
	for _, path := range []string{"/uuid_v4", "/uuid_v7", "/cuid", "/ulid"} {
		resp, _ := http.Get(genURL(env, path))
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("gen%s with v2 disabled: want 404, got %d", path, resp.StatusCode)
		}
	}
}

// ─── OQL scalar functions ─────────────────────────────────────────────────────

func TestGen_OQLUUIDv4(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "oql-gen-test", "type": "sensor"})
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL),
		map[string]interface{}{"query": "SELECT UUID_V4() as id FROM assets"})
	if status != http.StatusOK {
		t.Fatalf("OQL UUID_V4(): want 200, got %d: %v", status, resp)
	}
	results, _ := resp["data"].([]interface{})
	if len(results) == 0 {
		t.Fatal("OQL UUID_V4(): expected at least one result row")
	}
	row := results[0].(map[string]interface{})
	val, _ := row["id"].(string)
	if len(val) != 36 {
		t.Errorf("OQL UUID_V4(): want 36-char UUID, got %q", val)
	}
}

func TestGen_OQLUUIDv7(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "oql-gen-test", "type": "sensor"})
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL),
		map[string]interface{}{"query": "SELECT UUID_V7() as id FROM assets"})
	if status != http.StatusOK {
		t.Fatalf("OQL UUID_V7(): want 200, got %d: %v", status, resp)
	}
	results, _ := resp["data"].([]interface{})
	if len(results) == 0 {
		t.Fatal("OQL UUID_V7(): expected at least one result row")
	}
	row := results[0].(map[string]interface{})
	val, _ := row["id"].(string)
	if len(val) != 36 {
		t.Errorf("OQL UUID_V7(): want 36-char UUID, got %q", val)
	}
}

func TestGen_OQLCUID(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "oql-cuid-test", "type": "sensor"})
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL),
		map[string]interface{}{"query": "SELECT CUID() as cid FROM assets"})
	if status != http.StatusOK {
		t.Fatalf("OQL CUID(): want 200, got %d: %v", status, resp)
	}
	results, _ := resp["data"].([]interface{})
	if len(results) == 0 {
		t.Fatal("OQL CUID(): expected at least one result row")
	}
	row := results[0].(map[string]interface{})
	val, _ := row["cid"].(string)
	if !strings.HasPrefix(val, "c") {
		t.Errorf("OQL CUID(): want prefix 'c', got %q", val)
	}
}

func TestGen_OQLULID(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "oql-ulid-test", "type": "sensor"})
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL),
		map[string]interface{}{"query": "SELECT ULID() as uid FROM assets"})
	if status != http.StatusOK {
		t.Fatalf("OQL ULID(): want 200, got %d: %v", status, resp)
	}
	results, _ := resp["data"].([]interface{})
	if len(results) == 0 {
		t.Fatal("OQL ULID(): expected at least one result row")
	}
	row := results[0].(map[string]interface{})
	val, _ := row["uid"].(string)
	if len(val) != 26 {
		t.Errorf("OQL ULID(): want 26-char ULID, got %q", val)
	}
}

// ─── OQL available regardless of v2 flag ─────────────────────────────────────

func TestGen_OQLAvailableWhenV2Disabled(t *testing.T) {
	// The scalar functions are registered in init() unconditionally.
	// They must work even on a v1-only server.
	env := newV1OnlyServer(t)
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "oql-gen-v1-test", "type": "sensor"})
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL),
		map[string]interface{}{"query": "SELECT UUID_V4() as id FROM assets"})
	if status != http.StatusOK {
		t.Fatalf("OQL UUID_V4() on v1-only server: want 200, got %d: %v", status, resp)
	}
}
