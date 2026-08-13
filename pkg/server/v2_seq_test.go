// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_seq_test.go — S5: named sequence endpoints and OQL integration.

import (
	"fmt"
	"net/http"
	"testing"
)

func seqURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/gen/seq%s", sts.ts.URL, path)
}

func seqAliasURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/seq%s", sts.ts.URL, path)
}

// ─── Availability ─────────────────────────────────────────────────────────────

func TestSeq_AvailabilityShowsSeq(t *testing.T) {
	env := newV2Server(t)
	_, resp := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/", env.ts.URL), nil)
	subsystems, _ := resp["subsystems"].(map[string]interface{})
	seq, _ := subsystems["seq"].(map[string]interface{})
	if seq["available"] != true {
		t.Errorf("seq subsystem: want available=true, got %v", seq["available"])
	}
}

// ─── Define ───────────────────────────────────────────────────────────────────

func TestSeq_Define(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "my_seq"})
	if status != http.StatusCreated {
		t.Fatalf("define seq: want 201, got %d: %v", status, resp)
	}
	if resp["name"] != "my_seq" {
		t.Errorf("name: want 'my_seq', got %v", resp["name"])
	}
	if resp["start"].(float64) != 1 {
		t.Errorf("start: want 1, got %v", resp["start"])
	}
}

func TestSeq_DefineWithOptions(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{
			"name":         "custom_seq",
			"start":        100,
			"increment_by": 5,
			"min_val":      100,
			"max_val":      200,
		})
	if status != http.StatusCreated {
		t.Fatalf("define with options: want 201, got %d: %v", status, resp)
	}
	if resp["increment_by"].(float64) != 5 {
		t.Errorf("increment_by: want 5, got %v", resp["increment_by"])
	}
}

func TestSeq_DefineDuplicateName(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""), map[string]interface{}{"name": "dup"})
	status, _ := doJSONRequest(t, "POST", seqURL(env, ""), map[string]interface{}{"name": "dup"})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("duplicate name: want 422, got %d", status)
	}
}

func TestSeq_DefineInvalidName(t *testing.T) {
	env := newV2Server(t)
	status, _ := doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "has-hyphen"})
	if status == http.StatusCreated {
		t.Errorf("invalid name: should not succeed, got 201")
	}
}

func TestSeq_DefineZeroIncrement(t *testing.T) {
	env := newV2Server(t)
	status, _ := doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "zero_inc", "increment_by": 0})
	if status == http.StatusCreated {
		t.Errorf("zero increment: should not succeed, got 201")
	}
}

// ─── Get ──────────────────────────────────────────────────────────────────────

func TestSeq_Get(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "info_seq", "start": 10, "increment_by": 2})
	status, resp := doJSONRequest(t, "GET", seqURL(env, "/info_seq"), nil)
	if status != http.StatusOK {
		t.Fatalf("get seq: want 200, got %d: %v", status, resp)
	}
	if resp["name"] != "info_seq" {
		t.Errorf("name: want 'info_seq', got %v", resp["name"])
	}
	if resp["increment_by"].(float64) != 2 {
		t.Errorf("increment_by: want 2, got %v", resp["increment_by"])
	}
}

func TestSeq_GetNotFound(t *testing.T) {
	env := newV2Server(t)
	status, _ := doJSONRequest(t, "GET", seqURL(env, "/nonexistent"), nil)
	if status != http.StatusNotFound {
		t.Errorf("get missing: want 404, got %d", status)
	}
}

// ─── Next ─────────────────────────────────────────────────────────────────────

func TestSeq_Next_Monotonic(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""), map[string]interface{}{"name": "mono"})
	var prev float64
	for i := 0; i < 5; i++ {
		status, resp := doJSONRequest(t, "GET", seqURL(env, "/mono/next"), nil)
		if status != http.StatusOK {
			t.Fatalf("next: want 200, got %d: %v", status, resp)
		}
		v := resp["value"].(float64)
		if v <= prev {
			t.Errorf("not monotonic at %d: %v <= %v", i, v, prev)
		}
		prev = v
	}
}

func TestSeq_Next_DefaultsFrom1(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""), map[string]interface{}{"name": "from1"})
	_, resp := doJSONRequest(t, "GET", seqURL(env, "/from1/next"), nil)
	if resp["value"].(float64) != 1 {
		t.Errorf("first value: want 1, got %v", resp["value"])
	}
}

func TestSeq_Next_CustomStart(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "from100", "start": 100})
	_, resp := doJSONRequest(t, "GET", seqURL(env, "/from100/next"), nil)
	if resp["value"].(float64) != 100 {
		t.Errorf("first value: want 100, got %v", resp["value"])
	}
}

func TestSeq_Next_CustomIncrement(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "by5", "increment_by": 5})
	_, r1 := doJSONRequest(t, "GET", seqURL(env, "/by5/next"), nil)
	_, r2 := doJSONRequest(t, "GET", seqURL(env, "/by5/next"), nil)
	if r2["value"].(float64)-r1["value"].(float64) != 5 {
		t.Errorf("increment: want 5, got %v", r2["value"].(float64)-r1["value"].(float64))
	}
}

func TestSeq_Next_NotFound(t *testing.T) {
	env := newV2Server(t)
	status, _ := doJSONRequest(t, "GET", seqURL(env, "/nope/next"), nil)
	if status != http.StatusNotFound {
		t.Errorf("next missing: want 404, got %d", status)
	}
}

func TestSeq_Next_Exhausted(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "small", "start": 1, "max_val": 2})
	doJSONRequest(t, "GET", seqURL(env, "/small/next"), nil)              // 1
	doJSONRequest(t, "GET", seqURL(env, "/small/next"), nil)              // 2
	status, _ := doJSONRequest(t, "GET", seqURL(env, "/small/next"), nil) // exhausted
	if status == http.StatusOK {
		t.Errorf("exhausted seq: should not return 200")
	}
}

func TestSeq_Next_Cyclic(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""),
		map[string]interface{}{"name": "cyclic", "start": 1, "max_val": 2, "cycle": true})
	doJSONRequest(t, "GET", seqURL(env, "/cyclic/next"), nil)                 // 1
	doJSONRequest(t, "GET", seqURL(env, "/cyclic/next"), nil)                 // 2
	status, resp := doJSONRequest(t, "GET", seqURL(env, "/cyclic/next"), nil) // wraps to 1
	if status != http.StatusOK {
		t.Fatalf("cyclic wrap: want 200, got %d: %v", status, resp)
	}
	if resp["value"].(float64) != 1 {
		t.Errorf("cyclic: want 1 after wrap, got %v", resp["value"])
	}
}

// ─── Reset ────────────────────────────────────────────────────────────────────

func TestSeq_Reset(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""), map[string]interface{}{"name": "rst"})
	doJSONRequest(t, "GET", seqURL(env, "/rst/next"), nil) // 1
	doJSONRequest(t, "GET", seqURL(env, "/rst/next"), nil) // 2
	status, _ := doJSONRequest(t, "POST", seqURL(env, "/rst/reset"), nil)
	if status != http.StatusOK {
		t.Fatalf("reset: want 200, got %d", status)
	}
	_, resp := doJSONRequest(t, "GET", seqURL(env, "/rst/next"), nil)
	if resp["value"].(float64) != 1 {
		t.Errorf("after reset: want 1, got %v", resp["value"])
	}
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func TestSeq_Delete(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST", seqURL(env, ""), map[string]interface{}{"name": "del_me"})
	status, _ := doJSONRequest(t, "DELETE", seqURL(env, "/del_me"), nil)
	if status != http.StatusOK {
		t.Fatalf("delete: want 200, got %d", status)
	}
	s2, _ := doJSONRequest(t, "GET", seqURL(env, "/del_me"), nil)
	if s2 != http.StatusNotFound {
		t.Errorf("after delete: want 404, got %d", s2)
	}
}

func TestSeq_DeleteNotFound(t *testing.T) {
	env := newV2Server(t)
	status, _ := doJSONRequest(t, "DELETE", seqURL(env, "/ghost"), nil)
	if status != http.StatusNotFound {
		t.Errorf("delete missing: want 404, got %d", status)
	}
}

// ─── /seq alias ───────────────────────────────────────────────────────────────

func TestSeq_Alias(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", seqAliasURL(env, ""),
		map[string]interface{}{"name": "alias_seq"})
	if status != http.StatusCreated {
		t.Fatalf("alias define: want 201, got %d: %v", status, resp)
	}
	s2, r2 := doJSONRequest(t, "GET", seqAliasURL(env, "/alias_seq/next"), nil)
	if s2 != http.StatusOK {
		t.Errorf("alias next: want 200, got %d: %v", s2, r2)
	}
}

// ─── Tenant isolation ─────────────────────────────────────────────────────────

func TestSeq_TenantIsolation(t *testing.T) {
	env := newV2Server(t)
	// Define same name under two tenants.
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v2/tenant/t1/gen/seq", env.ts.URL),
		map[string]interface{}{"name": "shared_name", "start": 1})
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v2/tenant/t2/gen/seq", env.ts.URL),
		map[string]interface{}{"name": "shared_name", "start": 1000})

	_, r1 := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v2/tenant/t1/gen/seq/shared_name/next", env.ts.URL), nil)
	_, r2 := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v2/tenant/t2/gen/seq/shared_name/next", env.ts.URL), nil)

	v1 := r1["value"].(float64)
	v2 := r2["value"].(float64)
	if v1 == v2 {
		t.Errorf("tenant isolation: t1 and t2 should have different values, both got %v", v1)
	}
	if v1 != 1 {
		t.Errorf("t1 start: want 1, got %v", v1)
	}
	if v2 != 1000 {
		t.Errorf("t2 start: want 1000, got %v", v2)
	}
}

// TestSeq_ListTenantIsolation is the XOT180 general-sweep check
// (2026-08-12): handleSeqList's own SQL looks correctly tenant-scoped
// by inspection ("WHERE tenant_id=?"), but "looks correct in the
// code" is exactly the standard that let XOT173 ship. Proven directly
// here instead: two tenants, each with their own sequence of the same
// name, confirm neither tenant's own GET /gen/seq list result
// reflects the other's.
func TestSeq_ListTenantIsolation(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v2/tenant/t1/gen/seq", env.ts.URL),
		map[string]interface{}{"name": "shared_name", "start": 1})
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v2/tenant/t2/gen/seq", env.ts.URL),
		map[string]interface{}{"name": "shared_name", "start": 1000})

	status, listT1 := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/tenant/t1/gen/seq", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("t1 seq list: want 200, got %d %v", status, listT1)
	}
	seqs, _ := listT1["sequences"].([]interface{})
	if len(seqs) != 1 {
		t.Fatalf("tenant isolation violated: t1's own sequence list want exactly 1, got %d: %v", len(seqs), seqs)
	}
}

// TestGenList_TenantIsolation covers the generic named-generator list
// endpoint (handleGenList, GET /gen/{type}) specifically -- a
// genuinely different handler than TestSeq_ListTenantIsolation above
// exercises (sequences are routed separately, /gen/seq is a static
// route hitting handleSeqList, not the {type} pattern at all --
// confirmed directly against the route table, not assumed). Uses
// "token" as a valid generic type, since "seq"/"sequence" are
// explicitly rejected by genTypeFromURL for this endpoint.
func TestGenList_TenantIsolation(t *testing.T) {
	env := newV2Server(t)
	body := map[string]interface{}{"name": "shared_name", "config": map[string]interface{}{"length": 16}}
	status, defT1 := doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v2/tenant/t1/gen/token", env.ts.URL), body)
	if status != http.StatusCreated {
		t.Fatalf("t1 gen/token define: want 201, got %d %v", status, defT1)
	}
	status, defT2 := doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v2/tenant/t2/gen/token", env.ts.URL), body)
	if status != http.StatusCreated {
		t.Fatalf("t2 gen/token define: want 201, got %d %v", status, defT2)
	}

	status, listT1 := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/tenant/t1/gen/token", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("t1 gen/token list: want 200, got %d %v", status, listT1)
	}
	gens, _ := listT1["generators"].([]interface{})
	if len(gens) != 1 {
		t.Fatalf("tenant isolation violated: t1's own generator list want exactly 1, got %d: %v", len(gens), gens)
	}
}

// ─── OQL: NEXT VALUE FOR ──────────────────────────────────────────────────────

func TestSeq_OQLNextValueFor(t *testing.T) {
	env := newV2Server(t)
	// Create a sequence and an entity to query against.
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v2/tenant/default/gen/seq", env.ts.URL),
		map[string]interface{}{"name": "oql_seq"})
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "a", "type": "sensor"})
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "b", "type": "sensor"})

	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL),
		map[string]interface{}{"query": "SELECT NEXT VALUE FOR oql_seq AS n FROM assets"})
	if status != http.StatusOK {
		t.Fatalf("OQL NEXT VALUE FOR: want 200, got %d: %v", status, resp)
	}
	rows, _ := resp["data"].([]interface{})
	if len(rows) < 2 {
		t.Fatalf("OQL NEXT VALUE FOR: want >= 2 rows, got %d", len(rows))
	}
	// Each row should have a distinct sequence value.
	v0 := rows[0].(map[string]interface{})["n"].(float64)
	v1 := rows[1].(map[string]interface{})["n"].(float64)
	if v0 == v1 {
		t.Errorf("OQL NEXT VALUE FOR: rows should have distinct seq values, both got %v", v0)
	}
	if v1 != v0+1 {
		t.Errorf("OQL NEXT VALUE FOR: want consecutive values, got %v and %v", v0, v1)
	}
}

func TestSeq_OQLNextValueFor_NotFound(t *testing.T) {
	env := newV2Server(t)
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL),
		map[string]interface{}{"name": "a", "type": "sensor"})
	// Sequence does not exist — should return null values, not crash.
	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL),
		map[string]interface{}{"query": "SELECT NEXT VALUE FOR ghost_seq AS n FROM assets"})
	if status >= http.StatusInternalServerError {
		t.Errorf("NEXT VALUE FOR missing seq: should not 5xx, got %d: %v", status, resp)
	}
}
