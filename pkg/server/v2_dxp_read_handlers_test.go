// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_dxp_read_handlers_test.go — GET /dxp/def, GET /dxp/def/{id},
// GET /dxp/txn, GET /dxp/txn/{id} (item 20's remaining scope, T-101).

import (
	"fmt"
	"net/http"
	"testing"
)

func TestDxpDefAPI_List_ReturnsRegisteredDefs(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, resp)
	}
	defID := resp["id"]

	status, listResp := doJSONRequest(t, "GET", dxpURL(env, "/def"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /dxp/def: want 200, got %d %v", status, listResp)
	}
	defs, ok := listResp["definitions"].([]interface{})
	if !ok || len(defs) != 1 {
		t.Fatalf("expected exactly 1 definition, got %v", listResp["definitions"])
	}
	d0 := defs[0].(map[string]interface{})
	if d0["id"] != defID || d0["name"] != "simple_payment" {
		t.Errorf("list entry mismatch: got %v", d0)
	}
}

func TestDxpDefAPI_Get_ReturnsFullSpecAndAnalysis(t *testing.T) {
	env := newMetaServer(t)
	status, createResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, createResp)
	}
	id := int64(createResp["id"].(float64))

	status, resp := doJSONRequest(t, "GET", dxpURL(env, fmt.Sprintf("/def/%d", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /dxp/def/%d: want 200, got %d %v", id, status, resp)
	}
	if resp["name"] != "simple_payment" {
		t.Errorf("expected name simple_payment, got %v", resp["name"])
	}
	if _, ok := resp["spec"].(map[string]interface{}); !ok {
		t.Errorf("expected a nested spec object, got %v", resp["spec"])
	}
	if _, ok := resp["analysis"].(map[string]interface{}); !ok {
		t.Errorf("expected a nested analysis object, got %v", resp["analysis"])
	}
}

func TestDxpDefAPI_Get_UnknownID_404(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "GET", dxpURL(env, "/def/99999"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d %v", status, resp)
	}
}

// TestDxpTxnAPI_List_StatusFilter_SeparatesTerminalOutcomes dispatches
// one instance that commits and one that's refused outright (unknown
// bindings-schema violation, never reaches dispatch), then checks
// ?status= actually narrows the list — the whole reason this was
// built now rather than later: without it, a swept/expired instance
// (T-100) or a torn one is invisible.
func TestDxpTxnAPI_List_StatusFilter_SeparatesTerminalOutcomes(t *testing.T) {
	env := newDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}

	// One committed instance.
	status, txnResp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "50", "note": "a"},
	})
	if status != http.StatusCreated || txnResp["status"] != "committed" {
		t.Fatalf("expected committed, got %d %v", status, txnResp)
	}

	// One refused at bindings validation — never dispatched, never
	// committed; per handleDxpTxnCreate's own contract this refuses
	// before any dxp_txn row is even created, so it must NOT show up
	// in the list at all, committed or otherwise.
	status, badResp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"note": "missing amount"},
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected the missing-amount binding refused pre-dispatch, got %d %v", status, badResp)
	}

	status, listResp := doJSONRequest(t, "GET", dxpURL(env, "/txn?status=committed"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /dxp/txn?status=committed: want 200, got %d %v", status, listResp)
	}
	committed, ok := listResp["instances"].([]interface{})
	if !ok || len(committed) != 1 {
		t.Fatalf("expected exactly 1 committed instance, got %v", listResp["instances"])
	}

	status, activeResp := doJSONRequest(t, "GET", dxpURL(env, "/txn?status=active"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /dxp/txn?status=active: want 200, got %d %v", status, activeResp)
	}
	active, ok := activeResp["instances"].([]interface{})
	if !ok || len(active) != 0 {
		t.Fatalf("expected zero active instances (dispatch is synchronous), got %v", activeResp["instances"])
	}
}

func TestDxpTxnAPI_Get_ReturnsSnapshotAndStatus(t *testing.T) {
	env := newDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, txnResp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "75", "note": "b"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, txnResp)
	}
	id := int64(txnResp["id"].(float64))

	status, resp := doJSONRequest(t, "GET", dxpURL(env, fmt.Sprintf("/txn/%d", id)), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /dxp/txn/%d: want 200, got %d %v", id, status, resp)
	}
	if resp["status"] != "committed" {
		t.Errorf("expected status committed, got %v", resp["status"])
	}
	if _, ok := resp["snapshot"].(map[string]interface{}); !ok {
		t.Errorf("expected a nested snapshot object, got %v", resp["snapshot"])
	}
}

func TestDxpTxnAPI_Get_UnknownID_404(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "GET", dxpURL(env, "/txn/99999"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d %v", status, resp)
	}
}
