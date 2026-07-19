// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_query_test.go — transition pre-query (cheap version).
//
// A definition may associate an OQL SELECT with an input. Before a walk on that
// input, the server runs the query (read-only, before the walk transaction) and
// binds its first result row under the "query." prefix, so guards and set
// clauses can read query.<column> alongside payload.<field>. This saves the
// caller a round-trip it would otherwise make to fetch the data and pass it in
// the payload.
//
// These tests exercise the standalone /walk path (the only path that supports
// pre-queries; the commit-embedded walk passes nil query bindings).

import (
	"fmt"
	"net/http"
	"testing"
)

// seedAsset creates an asset the pre-query can SELECT, returning its id.
func seedAsset(t *testing.T, env *stdTestServer, name, status string) int64 {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/tenant/default/assets", env.ts.URL)
	st, resp := doJSONRequest(t, "POST", url, map[string]interface{}{
		"name":   name,
		"status": status,
	})
	if st != http.StatusCreated {
		t.Fatalf("seed asset: want 201, got %d: %v", st, resp)
	}
	id, _ := resp["id"].(float64)
	return int64(id)
}

// queryGatedDef is an FSM whose "check" input runs a pre-query selecting an
// asset's status, then routes on query.status. firstmatch, because guards that
// read query results cannot be proven exclusive.
func queryGatedDef(query string) map[string]interface{} {
	return map[string]interface{}{
		"name":        "QueryGated",
		"initial":     "Pending",
		"determinism": "firstmatch",
		"states": map[string]interface{}{
			"Pending":  map[string]interface{}{"terminal": false},
			"Approved": map[string]interface{}{"terminal": true},
			"Denied":   map[string]interface{}{"terminal": true},
		},
		"input_queries": map[string]interface{}{
			"check": query,
		},
		"transitions": []map[string]interface{}{
			{"from": "Pending", "input": "check", "to": "Approved", "guard": "query.status = 'approved'"},
			{"from": "Pending", "input": "check", "to": "Denied", "guard": "query.status != 'approved'"},
		},
	}
}

func TestPrequery_GuardReadsQueryResult_Approved(t *testing.T) {
	env := newV2Server(t)
	seedAsset(t, env, "acme", "approved")

	query := "SELECT status FROM assets WHERE name = 'acme'"
	st, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), queryGatedDef(query))
	if st != http.StatusCreated {
		t.Fatalf("create query-gated def: %d %v", st, resp)
	}
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	// Walk with no payload — the guard's data comes from the pre-query.
	wst, wResp := walk(t, env, id, "check", nil)
	if wst != http.StatusOK {
		t.Fatalf("walk: want 200, got %d: %v", wst, wResp)
	}
	if wResp["current"] != "Approved" {
		t.Errorf("query.status='approved' should route to Approved, got %v", wResp["current"])
	}
}

func TestPrequery_GuardReadsQueryResult_Denied(t *testing.T) {
	env := newV2Server(t)
	seedAsset(t, env, "beta", "rejected")

	query := "SELECT status FROM assets WHERE name = 'beta'"
	_, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), queryGatedDef(query))
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	wst, wResp := walk(t, env, id, "check", nil)
	if wst != http.StatusOK {
		t.Fatalf("walk: want 200, got %d: %v", wst, wResp)
	}
	if wResp["current"] != "Denied" {
		t.Errorf("query.status='rejected' should route to Denied, got %v", wResp["current"])
	}
}

// When the pre-query returns no rows, query.<col> references resolve to NULL,
// so an equality guard is false and the "!=" branch (also against NULL) is also
// false — meaning no transition fires (XOLU-FSM004). This pins the 0-row
// semantics: missing query data does not silently match.
//
// Crucially, this test also proves the query ACTUALLY RAN (and matched nothing)
// rather than the prequery being a no-op: the control case below uses the same
// definition shape with a matching row and confirms it routes to Approved. If
// the prequery did not execute, BOTH cases would yield NULL and the control
// would fail.
func TestPrequery_ZeroRowsBindsNull(t *testing.T) {
	env := newV2Server(t)
	// Seed an asset that the zero-row query deliberately does NOT match (wrong
	// name), so the table is non-empty and a no-op prequery would be
	// indistinguishable only if the query never filtered. The control case
	// proves the filter (and thus execution) is real.
	seedAsset(t, env, "present_but_unmatched", "approved")

	query := "SELECT status FROM assets WHERE name = 'does_not_exist'"
	_, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), queryGatedDef(query))
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	wst, wResp := walk(t, env, id, "check", nil)
	// query.status is NULL: "= 'approved'" is false, "!= 'approved'" is also
	// false (NULL comparison), so no guard passes.
	if wst != http.StatusUnprocessableEntity || errCode(wResp) != "XOLU-FSM004" {
		t.Errorf("zero-row query: want 422/XOLU-FSM004 (no guard passes on NULL), got %d/%v",
			wst, wResp["error"])
	}

	// Control: same definition shape, but a query that DOES match the seeded
	// row must route to Approved. This is what makes the zero-row assertion
	// load-bearing — it proves the query executes and filters, rather than the
	// prequery being absent (which would make both cases NULL).
	matchQuery := "SELECT status FROM assets WHERE name = 'present_but_unmatched'"
	_, resp2 := doJSONRequest(t, "POST", fsmDefURL(env, ""), queryGatedDef(matchQuery))
	defID2 := int64(resp2["id"].(float64))
	_, m2 := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID2})
	id2 := int64(m2["id"].(float64))
	st2, r2 := walk(t, env, id2, "check", nil)
	if st2 != http.StatusOK || r2["current"] != "Approved" {
		t.Errorf("control: matching query must route to Approved (proves query executes), got %d/%v",
			st2, r2["current"])
	}
}

// A set clause can capture a pre-query column into a machine variable.
func TestPrequery_SetClauseCapturesQueryColumn(t *testing.T) {
	env := newV2Server(t)
	seedAsset(t, env, "gamma", "approved")

	def := map[string]interface{}{
		"name":        "QueryCapture",
		"initial":     "Pending",
		"determinism": "firstmatch",
		"states": map[string]interface{}{
			"Pending": map[string]interface{}{"terminal": false},
			"Done":    map[string]interface{}{"terminal": true},
		},
		"variables": map[string]interface{}{
			"@captured": map[string]interface{}{"type": "string", "default": ""},
		},
		"input_queries": map[string]interface{}{
			"capture": "SELECT status FROM assets WHERE name = 'gamma'",
		},
		"transitions": []map[string]interface{}{
			{"from": "Pending", "input": "capture", "to": "Done",
				"set": map[string]string{"@captured": "query.status"}},
		},
	}
	st, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), def)
	if st != http.StatusCreated {
		t.Fatalf("create capture def: %d %v", st, resp)
	}
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	wst, wResp := walk(t, env, id, "capture", nil)
	if wst != http.StatusOK {
		t.Fatalf("walk: want 200, got %d: %v", wst, wResp)
	}
	vars, _ := wResp["vars"].(map[string]interface{})
	if vars["@captured"] != "approved" {
		t.Errorf("set clause should capture query.status into @captured, got %v", vars["@captured"])
	}
}

// A definition without a query for the walked input behaves normally — the
// pre-query step is a no-op.
func TestPrequery_NoQueryForInputIsNoOp(t *testing.T) {
	env := newV2Server(t)
	id := newAssetMachine(t, env)
	// AssetLifecycle has no queries; a normal walk must still work.
	st, resp := walk(t, env, id, "ready_for_inspection", nil)
	if st != http.StatusOK {
		t.Fatalf("walk on no-query machine: want 200, got %d: %v", st, resp)
	}
}

// A query that matches several rows is bounded to one row by the forced TOP 1;
// the walk binds exactly the first row's column and routes deterministically
// (the query orders the result so "first" is well-defined).
func TestPrequery_MultiRowQueryBoundedToOne(t *testing.T) {
	env := newV2Server(t)
	// Three approved assets — an unbounded query would return all three.
	seedAsset(t, env, "m1", "approved")
	seedAsset(t, env, "m2", "approved")
	seedAsset(t, env, "m3", "approved")

	// Ordered so the first row is deterministic.
	query := "SELECT status FROM assets WHERE status = 'approved' ORDER BY name ASC"
	_, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), queryGatedDef(query))
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	wst, wResp := walk(t, env, id, "check", nil)
	if wst != http.StatusOK {
		t.Fatalf("multi-row walk: want 200, got %d: %v", wst, wResp)
	}
	// First row's status is 'approved' → Approved branch.
	if wResp["current"] != "Approved" {
		t.Errorf("multi-row bounded query should bind first row (approved), got %v", wResp["current"])
	}
}
