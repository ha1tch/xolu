// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_dxp_loc_test.go — loc's own dxp participation, proven end to end
// (Stage 5, T-118, wave 9). Mirrors v2_dxp_hotel_test.go's own shape
// (real HTTP def+txn dispatch, side effects checked independently
// through each primitive's own real store) at a smaller scale: loc
// has no HTTP surface of its own yet (Stage 6), so locations are
// seeded directly through LocStoreForTest, the same ForTest pattern
// BlobStoreForTest/CalManagerForTest already establish.

package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/ha1tch/xolu/pkg/loc"
)

// seedLocTreeForDefaultTenant creates a georeferenced root plus one
// postable leaf ("bin"), optionally capacity-limited, against
// "default"'s real registered tenant id — mirroring
// seedCalendarForDefaultTenant's own reasoning (defaultTenantID's
// doc): dxp URLs in this file all carry a /tenant/{name}/ segment and
// auto-register "default" to a real (non-zero) numeric id.
func seedLocTreeForDefaultTenant(t *testing.T, env *stdTestServer, leafID string, ceiling *int64) {
	t.Helper()
	tid := defaultTenantID(t, env)
	st, err := env.srv.LocStoreForTest(context.Background(), tid)
	if err != nil {
		t.Fatalf("LocStoreForTest: %v", err)
	}
	root := "root"
	if _, err := st.Def(context.Background(), loc.LocationDef{
		ID: root, Name: "root",
		Placement: loc.Placement{Anchor: &loc.GeoAnchor{Lat: -34.9, Lon: -56.16, Alt: 10, TrueNorth: 0}},
	}); err != nil {
		t.Fatalf("seed loc root: %v", err)
	}
	if _, err := st.Def(context.Background(), loc.LocationDef{
		ID: leafID, ParentID: &root, Name: leafID, Postable: true,
	}); err != nil {
		t.Fatalf("seed loc leaf %q: %v", leafID, err)
	}
	if ceiling != nil {
		c := ceiling
		if err := st.Patch(context.Background(), leafID, loc.PatchParams{Ceiling: &c}); err != nil {
			t.Fatalf("set loc leaf ceiling: %v", err)
		}
	}
}

func minimalLocDxpDef(amountBindingRequired bool) map[string]interface{} {
	bindingsSchema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"amount": map[string]interface{}{"type": "string"}},
	}
	if amountBindingRequired {
		bindingsSchema["required"] = []interface{}{"amount"}
	}
	return map[string]interface{}{
		"name":            "loc_and_bal_minimal",
		"pattern":         "3ps",
		"bindings_schema": bindingsSchema,
		"participants": []map[string]interface{}{
			{"id": "place", "primitive": "loc", "op": "move",
				"params": map[string]interface{}{
					"subject_ref":    "pkg-1",
					"to_location_id": "bin",
				}},
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
}

// TestDxpTxnAPI_LocAndBal_BothCommit is Stage 5's own stated exit
// criterion: a real multi-participant dxp transaction touching loc
// alongside another primitive, dispatched through the real HTTP API
// and the real coordinator (dispatchDxpTxn) — not pkg/dxp/integration's
// own hand-wired test doubles, and not a unit test calling the
// adapter's methods directly.
func TestDxpTxnAPI_LocAndBal_BothCommit(t *testing.T) {
	env := newDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	seedLocTreeForDefaultTenant(t, env, "bin", nil)

	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), minimalLocDxpDef(true))
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "150"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != 2 {
		t.Fatalf("expected committed_through 2, got %v", resp["committed_through"])
	}

	// bal leg, checked independently through its own real API —
	// matching the hotel test's own discipline exactly.
	status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account=acct"), nil)
	if status != http.StatusOK || balResp["value"] != "150" {
		t.Errorf("bal leg: want balance 150, got %d %v", status, balResp)
	}

	// loc leg, checked directly against loc_assignment through the
	// same store instance the dxp adapter used — not a derived read
	// path, matching the cal leg's own raw-SQL discipline in the
	// hotel test.
	tid := defaultTenantID(t, env)
	st, err := env.srv.LocStoreForTest(context.Background(), tid)
	if err != nil {
		t.Fatalf("LocStoreForTest: %v", err)
	}
	var locationID string
	err = st.DB().QueryRowContext(context.Background(),
		`SELECT l.location_id FROM loc_assignment a JOIN locations l ON l.location_key = a.location_key
		 WHERE a.subject_ref = ?`, "pkg-1").Scan(&locationID)
	if err != nil {
		t.Fatalf("loc leg: querying loc_assignment for pkg-1: %v", err)
	}
	if locationID != "bin" {
		t.Errorf("loc leg: want location_id %q, got %q", "bin", locationID)
	}
}

// TestDxpTxnAPI_LocAndBal_LocRefusalReleasesBalClaim proves dxp's own
// attendance mechanism correctly releases the OTHER participant's
// claim when loc refuses — the same property the hotel test's own
// overlap tests prove for cal's refusals, exercised here for loc's
// own capacity guard instead. The bal payment must NOT have committed
// either, even though bal's own admission would have succeeded in
// isolation.
func TestDxpTxnAPI_LocAndBal_LocRefusalReleasesBalClaim(t *testing.T) {
	env := newDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	ceiling := int64(0) // the leaf is full before this transaction even starts
	seedLocTreeForDefaultTenant(t, env, "bin", &ceiling)

	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), minimalLocDxpDef(true))
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "150"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201 (a refused instance is still a created resource), got %d %v", status, resp)
	}
	if resp["status"] == "committed" {
		t.Fatalf("expected a refused/failed status given the leaf is at capacity 0, got committed")
	}

	// bal must show NO balance change — its own claim was released,
	// not executed, when loc's Reserve refused.
	status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account=acct"), nil)
	if status != http.StatusOK || balResp["value"] != "0" {
		t.Errorf("bal leg: want balance 0 (unclaimed/released), got %d %v", status, balResp)
	}
}
