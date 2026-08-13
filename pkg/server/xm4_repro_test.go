// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

// xm4_repro_test.go — regression coverage for xoluman's own XM-4 report
// (xolu's own XOT175/XOT178), a real data-correctness bug found by
// direct reproduction, not assumed from reading code alone.
//
// Root cause, confirmed precisely: EntityAdapter.Execute's own
// EntityUpdateParams case called saveInTx directly with the raw PARTIAL
// patch data -- entirely bypassing the read-merge-write that the
// regular PATCH endpoint (patchInner) correctly performs. For a blob
// entity, saveInTx's own JSON write overwrote the whole stored document
// with just the patch; for an adapted entity, adaptedUpdate's own
// generated UPDATE touches every column in the spec regardless of what
// the patch actually contains, so every column the patch didn't mention
// got overwritten with a zero value. Fixed by mirroring patchInner's own
// read-merge logic inside EntityAdapter.Execute, in the same
// transaction the write itself uses.
//
// A second, separate, broader bug (XOT178) was found investigating this
// one: schema registration (POST /schema/{entity}) only ever registered
// the resulting adapted table on the server's own default (tenantID 0)
// store instance -- never on any named tenant's own, separate
// *storage.SQLiteStore (storeForTenant creates one per non-zero tenant,
// even in shared-database mode). Confirmed directly: a "default"-named
// tenant auto-registers to numeric id 1, not 0 -- meaning every entity
// with a schema was silently blob, never adapted, for any named-tenant
// deployment's own real data operations. Fixed with
// registerAdaptedEverywhere (propagates a new/changed schema to every
// tenant store that already exists) and replayAdaptedSchemas (applies
// every already-loaded schema to a tenant store the moment it's
// created, before any caller ever sees it).

func TestXM4_DXPPartialUpdate_BlobEntity_PreservesUntouchedFields(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"

	status, coResp := doJSONRequest(t, "POST", base+"/companies",
		map[string]interface{}{"name": "Acme", "industry": "logistics"})
	if status != http.StatusCreated {
		t.Fatalf("create company: want 201, got %d %v", status, coResp)
	}
	companyID := coResp["id"]

	status, createResp := doJSONRequest(t, "POST", base+"/deals",
		map[string]interface{}{
			"amount": float64(500), "stage": "open",
			"company": map[string]interface{}{"type": "REF", "entity": "companies", "id": companyID},
		})
	if status != http.StatusCreated {
		t.Fatalf("create deal: want 201, got %d %v", status, createResp)
	}
	dealID := createResp["id"]

	def := map[string]interface{}{
		"name": "xm4_blob", "pattern": "3ps",
		"participants": []map[string]interface{}{
			{
				"id": "p0", "primitive": "entity", "op": "update",
				"params": map[string]interface{}{
					"entity": "deals", "id": dealID, "data": map[string]interface{}{"stage": "closed_won"},
				},
			},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", env.ts.URL+"/api/v2/tenant/default/dxp/def", def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, txnResp := doJSONRequest(t, "POST", env.ts.URL+"/api/v2/tenant/default/dxp/txn",
		map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, txnResp)
	}
	if txnResp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", txnResp["status"], txnResp["reason"])
	}

	// REST: the field the transaction never touched must survive.
	status, restResp := doJSONRequest(t, "GET", fmt.Sprintf("%s/deals/%v", base, dealID), nil)
	if status != http.StatusOK {
		t.Fatalf("GET deal: want 200, got %d %v", status, restResp)
	}
	if restResp["amount"] != float64(500) {
		t.Errorf("REST: want amount=500 (untouched by the DXP update), got %v -- full body: %v",
			restResp["amount"], restResp)
	}
	if restResp["stage"] != "closed_won" {
		t.Errorf("REST: want stage=closed_won (the field the update DID touch), got %v", restResp["stage"])
	}

	// Sulpher: same two assertions via the graph read path.
	status, sulpherResp := doJSONRequest(t, "POST", base+"/graph/query",
		map[string]interface{}{"query": fmt.Sprintf("MATCH (d:deals) WHERE d.id = %v RETURN d.id, d.stage, d.amount", dealID)})
	if status != http.StatusOK {
		t.Fatalf("Sulpher query: want 200, got %d %v", status, sulpherResp)
	}
	rows, _ := sulpherResp["result"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("Sulpher query: want 1 row, got %d: %v", len(rows), sulpherResp)
	}
	row, _ := rows[0].(map[string]interface{})
	if row["d.amount"] != float64(500) {
		t.Errorf("Sulpher: want d.amount=500 (untouched by the DXP update), got %v -- full row: %v",
			row["d.amount"], row)
	}
	if row["d.stage"] != "closed_won" {
		t.Errorf("Sulpher: want d.stage=closed_won (the field the update DID touch), got %v", row["d.stage"])
	}
}

func TestXM4_DXPPartialUpdate_AdaptedEntity_PreservesUntouchedFields(t *testing.T) {
	env := newFullDxpServer(t)
	base := env.ts.URL + "/api/v1/tenant/default"

	status, schemaResp := doJSONRequest(t, "POST", env.ts.URL+"/api/v1/schema/deals", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"amount":  map[string]interface{}{"type": "number"},
			"stage":   map[string]interface{}{"type": "string"},
			"company": map[string]interface{}{"type": "object", "format": "ref", "target": "companies"},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("schema registration: want 201, got %d %v", status, schemaResp)
	}

	status, coResp := doJSONRequest(t, "POST", base+"/companies",
		map[string]interface{}{"name": "Acme", "industry": "logistics"})
	if status != http.StatusCreated {
		t.Fatalf("create company: want 201, got %d %v", status, coResp)
	}
	companyID := coResp["id"]

	// Confirms XOT178's own fix, not just XOT175's: this entity must
	// genuinely be adapted on the TENANT-scoped store the requests
	// above actually use -- not merely on the server's own default
	// (tenantID 0) instance, which schema registration alone used to
	// reach before this fix.
	tid, ok := env.srv.TenantIDForTest("default")
	if !ok {
		t.Fatal("tenant \"default\" not found in registry after a request against it")
	}
	if isAdapted, err := env.srv.IsAdaptedForTest(tid, "deals"); err != nil {
		t.Fatalf("IsAdaptedForTest: %v", err)
	} else if !isAdapted {
		t.Fatalf("deals is NOT adapted on tenant %d's own store -- XOT178 regression", tid)
	}

	status, createResp := doJSONRequest(t, "POST", base+"/deals",
		map[string]interface{}{
			"amount": float64(500), "stage": "open",
			"company": map[string]interface{}{"type": "REF", "entity": "companies", "id": companyID},
		})
	if status != http.StatusCreated {
		t.Fatalf("create deal: want 201, got %d %v", status, createResp)
	}
	dealID := createResp["id"]

	def := map[string]interface{}{
		"name": "xm4_adapted", "pattern": "3ps",
		"participants": []map[string]interface{}{
			{
				"id": "p0", "primitive": "entity", "op": "update",
				"params": map[string]interface{}{
					"entity": "deals", "id": dealID, "data": map[string]interface{}{"stage": "closed_won"},
				},
			},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", env.ts.URL+"/api/v2/tenant/default/dxp/def", def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, txnResp := doJSONRequest(t, "POST", env.ts.URL+"/api/v2/tenant/default/dxp/txn",
		map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, txnResp)
	}
	if txnResp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", txnResp["status"], txnResp["reason"])
	}

	status, restResp := doJSONRequest(t, "GET", fmt.Sprintf("%s/deals/%v", base, dealID), nil)
	if status != http.StatusOK {
		t.Fatalf("GET deal: want 200, got %d %v", status, restResp)
	}
	if restResp["amount"] != float64(500) {
		t.Errorf("REST: want amount=500 (untouched by the DXP update), got %v -- full body: %v",
			restResp["amount"], restResp)
	}
	if restResp["stage"] != "closed_won" {
		t.Errorf("REST: want stage=closed_won (the field the update DID touch), got %v", restResp["stage"])
	}

	status, sulpherResp := doJSONRequest(t, "POST", base+"/graph/query",
		map[string]interface{}{"query": fmt.Sprintf("MATCH (d:deals) WHERE d.id = %v RETURN d.id, d.stage, d.amount", dealID)})
	if status != http.StatusOK {
		t.Fatalf("Sulpher query: want 200, got %d %v", status, sulpherResp)
	}
	rows, _ := sulpherResp["result"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("Sulpher query: want 1 row, got %d: %v", len(rows), sulpherResp)
	}
	row, _ := rows[0].(map[string]interface{})
	if row["d.amount"] != float64(500) {
		t.Errorf("Sulpher: want d.amount=500 (untouched by the DXP update), got %v -- full row: %v",
			row["d.amount"], row)
	}
	if row["d.stage"] != "closed_won" {
		t.Errorf("Sulpher: want d.stage=closed_won (the field the update DID touch), got %v", row["d.stage"])
	}
}
