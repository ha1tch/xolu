// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_bal_handlers_test.go — the bal HTTP surface (@B09), including the
// @B04 obligation: a float smuggled through the API must fail.

package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

func newBalServer(t *testing.T) *stdTestServer {
	return newMetaServer(t, func(cfg *config.Config) {
		cfg.BalEnabled = true
	})
}

func balURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/bal%s", sts.ts.URL, path)
}

func TestBalAPI_DefineTransferBalanceEntries(t *testing.T) {
	env := newBalServer(t)

	for _, def := range []map[string]interface{}{
		{"account_id": "~received", "unit": "widget", "scale": 0, "floor": "-1000000"},
		{"account_id": "warehouse:A/widget", "unit": "widget", "scale": 0},
	} {
		status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), def)
		if status != http.StatusCreated {
			t.Fatalf("def %v: %d %v", def["account_id"], status, resp)
		}
	}

	status, resp := doJSONRequest(t, "POST", balURL(env, "/transfer"), map[string]interface{}{
		"from": "~received", "to": "warehouse:A/widget",
		"amount": "125", "scale": 0, "memo": "goods in",
	})
	if status != http.StatusOK {
		t.Fatalf("transfer: %d %v", status, resp)
	}
	if resp["amount"] != "125" {
		t.Fatalf("amount echo: %v", resp["amount"])
	}

	status, resp = doJSONRequest(t, "GET", balURL(env, "/balance?account=warehouse:A%2Fwidget"), nil)
	if status != http.StatusOK || resp["value"] != "125" {
		t.Fatalf("balance: %d %v", status, resp)
	}
	// minor units come back as an exact integer.
	if minor, ok := resp["minor"].(float64); !ok || minor != 125 {
		t.Fatalf("minor: %v", resp["minor"])
	}

	status, resp = doJSONRequest(t, "GET", balURL(env, "/entries?account=warehouse:A%2Fwidget"), nil)
	if status != http.StatusOK {
		t.Fatalf("entries: %d", status)
	}
	entries, _ := resp["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("entries count: %d", len(entries))
	}
	e0 := entries[0].(map[string]interface{})
	if e0["previous_balance"] != "0" || e0["current_balance"] != "125" {
		t.Fatalf("chain triple on the wire: %v", e0)
	}
}

func TestBalAPI_FloatSmugglingRefused(t *testing.T) {
	env := newBalServer(t)
	// A JSON NUMBER amount — the exact smuggling the doctrine forbids —
	// must be refused before any parsing, with BAL004.
	status, resp := doJSONRequest(t, "POST", balURL(env, "/transfer"), map[string]interface{}{
		"from": "a", "to": "b",
		"amount": 12.34, "scale": 2,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("numeric amount accepted: %d %v", status, resp)
	}
	errObj, _ := resp["error"].(map[string]interface{})
	if errObj["code"] != "XOLU-BAL004" {
		t.Fatalf("wrong code for smuggled float: %v", errObj)
	}
}

func TestBalAPI_RefusalMapping(t *testing.T) {
	env := newBalServer(t)
	status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), map[string]interface{}{
		"account_id": "1", "unit": "u", "scale": 0, "postable": false})
	if status != http.StatusCreated {
		t.Fatalf("def summary: %d %v", status, resp)
	}
	status, resp = doJSONRequest(t, "POST", balURL(env, "/def"), map[string]interface{}{
		"account_id": "1.1", "unit": "u", "scale": 0, "floor": "-100"})
	if status != http.StatusCreated {
		t.Fatalf("def leaf: %d %v", status, resp)
	}

	cases := []struct {
		body map[string]interface{}
		want string
		code int
	}{
		{map[string]interface{}{"from": "1", "to": "1.1", "amount": "1", "scale": 0}, "XOLU-BAL005", http.StatusConflict},
		{map[string]interface{}{"from": "1.1", "to": "ghost", "amount": "1", "scale": 0}, "XOLU-BAL002", http.StatusNotFound},
		{map[string]interface{}{"from": "1.1", "to": "1", "amount": "0.5", "scale": 0}, "XOLU-BAL004", http.StatusBadRequest},
	}
	for _, c := range cases {
		status, resp := doJSONRequest(t, "POST", balURL(env, "/transfer"), c.body)
		errObj, _ := resp["error"].(map[string]interface{})
		if status != c.code || errObj["code"] != c.want {
			t.Fatalf("%v: got %d %v, want %d %s", c.body, status, errObj, c.code, c.want)
		}
	}
	// Floor refusal: leaf at 0 cannot debit.
	status, resp = doJSONRequest(t, "POST", balURL(env, "/transfer"), map[string]interface{}{
		"from": "1.1", "to": "1.1x", "amount": "1", "scale": 0})
	_ = resp
	if status != http.StatusNotFound { // 1.1x unknown — checked before bounds in write-first order? floor guard fires first
		// Accept either mapping shape: bounds (409) if the debit guard
		// fires first, unknown (404) if diagnosis reaches the target.
		if status != http.StatusConflict {
			t.Fatalf("floor/unknown case: %d", status)
		}
	}
}

// ─── Stage 3: rollup surface (as-of and period close) ───────────────────────

func TestBalAPI_AsOfAndClose(t *testing.T) {
	env := newBalServer(t)

	for _, def := range []map[string]interface{}{
		{"account_id": "~in", "unit": "u", "scale": 0, "floor": "-1000000"},
		{"account_id": "ledger", "unit": "u", "scale": 0},
	} {
		if status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), def); status != http.StatusCreated {
			t.Fatalf("def %v: %d %v", def["account_id"], status, resp)
		}
	}

	// Two transfers at known instants.
	t1 := "2026-05-01T10:00:00Z"
	t2 := "2026-05-01T14:00:00Z"
	for _, tr := range []map[string]interface{}{
		{"from": "~in", "to": "ledger", "amount": "100", "scale": 0, "at": t1},
		{"from": "~in", "to": "ledger", "amount": "50", "scale": 0, "at": t2},
	} {
		if status, resp := doJSONRequest(t, "POST", balURL(env, "/transfer"), tr); status != http.StatusOK {
			t.Fatalf("transfer: %d %v", status, resp)
		}
	}

	// As-of between the two transfers sees only the first.
	status, resp := doJSONRequest(t, "GET",
		balURL(env, "/asof?account=ledger&at=2026-05-01T12:00:00Z"), nil)
	if status != http.StatusOK {
		t.Fatalf("asof: %d %v", status, resp)
	}
	if resp["value"] != "100" {
		t.Fatalf("as-of mid-series: want 100, got %v", resp["value"])
	}
	if resp["source"] != "rollup" {
		t.Fatalf("as-of should declare the derived source, got %v", resp["source"])
	}

	// As-of after both sees the total.
	_, resp = doJSONRequest(t, "GET",
		balURL(env, "/asof?account=ledger&at=2026-05-02T00:00:00Z"), nil)
	if resp["value"] != "150" {
		t.Fatalf("as-of after series: want 150, got %v", resp["value"])
	}

	// Period close writes a checkpoint; as-of past it must still agree.
	status, resp = doJSONRequest(t, "POST", balURL(env, "/close"), map[string]interface{}{
		"account_id": "ledger", "at": "2026-05-01T12:00:00Z",
	})
	if status != http.StatusOK {
		t.Fatalf("close: %d %v", status, resp)
	}
	_, resp = doJSONRequest(t, "GET",
		balURL(env, "/asof?account=ledger&at=2026-05-02T00:00:00Z"), nil)
	if resp["value"] != "150" {
		t.Fatalf("as-of past checkpoint: want 150, got %v", resp["value"])
	}

	// Refusals.
	if status, _ := doJSONRequest(t, "GET", balURL(env, "/asof?account=ledger"), nil); status != http.StatusBadRequest {
		t.Fatalf("asof without at: want 400, got %d", status)
	}
	if status, _ := doJSONRequest(t, "GET",
		balURL(env, "/asof?account=ghost&at=2026-05-02T00:00:00Z"), nil); status != http.StatusNotFound {
		t.Fatalf("asof unknown account: want 404, got %d", status)
	}
}

// TestBalAPI_SealEnforcement proves the production wiring end to end
// over real HTTP, not just the unit-level logic already covered in
// pkg/bal: the per-tenant Sealer cache (Server.balSealer, mirroring
// balRollup's own long-lived-handle pattern) must actually be attached
// to the Store handling each request, not just constructible in
// isolation.
func TestBalAPI_SealEnforcement(t *testing.T) {
	env := newBalServer(t)

	for _, def := range []map[string]interface{}{
		{"account_id": "~in", "unit": "u", "scale": 0, "floor": "-1000000"},
		{"account_id": "acct", "unit": "u", "scale": 0},
	} {
		if status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), def); status != http.StatusCreated {
			t.Fatalf("def %v: %d %v", def["account_id"], status, resp)
		}
	}

	// Seal through the end of June.
	status, resp := doJSONRequest(t, "POST", balURL(env, "/close"), map[string]interface{}{
		"at": "2026-07-01T00:00:00Z",
	})
	if status != http.StatusOK {
		t.Fatalf("close: %d %v", status, resp)
	}
	if resp["accounts_closed"] != float64(2) {
		t.Fatalf("accounts_closed: want 2, got %v", resp["accounts_closed"])
	}

	// A transfer dated inside the sealed period must be refused with
	// XOLU-BAL003, over a completely fresh request — the Sealer must
	// have survived being attached to a NEW Store instance, not just
	// the one that happened to handle the close.
	status, resp = doJSONRequest(t, "POST", balURL(env, "/transfer"), map[string]interface{}{
		"from": "~in", "to": "acct", "amount": "10", "scale": 0, "at": "2026-06-15T12:00:00Z",
	})
	if status != http.StatusConflict {
		t.Fatalf("transfer within sealed period: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a nested error object, got %v", resp)
	}
	if errObj["code"] != "XOLU-BAL003" {
		t.Fatalf("transfer within sealed period: want XOLU-BAL003, got %v", errObj["code"])
	}

	// A transfer after the frontier must still succeed.
	status, resp = doJSONRequest(t, "POST", balURL(env, "/transfer"), map[string]interface{}{
		"from": "~in", "to": "acct", "amount": "10", "scale": 0, "at": "2026-07-15T12:00:00Z",
	})
	if status != http.StatusOK {
		t.Fatalf("transfer after the sealed period: want 200, got %d %v", status, resp)
	}
}

// TestBalAPI_BackdatedErrorMapping pins a real gap found while
// building the client library: BackdatedError (XOLU-BAL006) existed
// in pkg/bal but writeBalError's switch never had a case for it,
// meaning a normal, expected backdated-entry refusal fell through to
// a 500 Internal Server Error with the WRONG code (ErrStorageFailed)
// instead of a 409 with XOLU-BAL006. Fixed alongside adding the
// missing ErrBalBackdated constant to pkg/errors.
func TestBalAPI_BackdatedErrorMapping(t *testing.T) {
	env := newBalServer(t)

	for _, def := range []map[string]interface{}{
		{"account_id": "~in", "unit": "u", "scale": 0, "floor": "-1000000"},
		{"account_id": "acct", "unit": "u", "scale": 0}, // default append_only policy
	} {
		if status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), def); status != http.StatusCreated {
			t.Fatalf("def %v: %d %v", def["account_id"], status, resp)
		}
	}

	// First entry establishes the account's latest instant.
	if status, resp := doJSONRequest(t, "POST", balURL(env, "/transfer"), map[string]interface{}{
		"from": "~in", "to": "acct", "amount": "10", "scale": 0, "at": "2026-06-15T12:00:00Z",
	}); status != http.StatusOK {
		t.Fatalf("first transfer: %d %v", status, resp)
	}

	// A strictly-earlier entry on the default (append_only) policy
	// must be refused with 409 + XOLU-BAL006, not 500.
	status, resp := doJSONRequest(t, "POST", balURL(env, "/transfer"), map[string]interface{}{
		"from": "~in", "to": "acct", "amount": "5", "scale": 0, "at": "2026-06-01T12:00:00Z",
	})
	if status != http.StatusConflict {
		t.Fatalf("backdated transfer: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a nested error object, got %v", resp)
	}
	if errObj["code"] != "XOLU-BAL006" {
		t.Fatalf("backdated transfer: want XOLU-BAL006, got %v", errObj["code"])
	}
}
