// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_dxp_phased_test.go — genuine multi-substrate dxp dispatch
// through the real HTTP path (T-105). Everything before this file
// only ever dispatched SQL-only defs — this is the first test
// anywhere that mixes a SQL participant (bal) with a Pebble one (ts)
// and drives it through dispatchPhased for real, not a standalone
// reproduction of the pattern elsewhere.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

// newPhasedDxpServer enables bal + timeseries — the minimum needed to
// exercise a genuine SQL+Pebble dispatch.
func newPhasedDxpServer(t *testing.T) *stdTestServer {
	return newMetaServer(t, func(cfg *config.Config) {
		cfg.BalEnabled = true
		cfg.TimeseriesEnabled = true
		cfg.TSMemtableSize = 4 * 1024 * 1024
		cfg.TSBlockSize = 4096
		cfg.TSCompression = "snappy"
		cfg.TSL0CompactionThreshold = 4
		cfg.TSMaxOpenFiles = 50
		cfg.TSDefaultRetentionDays = 90
		cfg.TSCompactionIntervalSecs = 3600
	})
}

func tsURLFor(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v1/tenant/default/ts%s", sts.ts.URL, path)
}

func provisionTsAndDefineTimeline(t *testing.T, env *stdTestServer, timelineID int, dims int) {
	t.Helper()
	status, resp := doJSONRequest(t, "POST", tsURLFor(env, "/provision"), nil)
	if status != http.StatusCreated {
		t.Fatalf("ts provision: want 201, got %d %v", status, resp)
	}
	status, resp = doJSONRequest(t, "POST", tsURLFor(env, "/tl/def"), map[string]interface{}{
		"id": timelineID, "dims": dims, "name": "audit",
	})
	if status != http.StatusCreated {
		t.Fatalf("ts define timeline: want 201, got %d %v", status, resp)
	}
}

func phasedMultiSubstrateDef() map[string]interface{} {
	return map[string]interface{}{
		"name":    "phased_multi_substrate",
		"pattern": "3ps",
		"bindings_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"amount": map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"amount"},
		},
		"participants": []map[string]interface{}{
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
			{"id": "audit", "primitive": "ts", "op": "append",
				"params": map[string]interface{}{
					"timeline":     1,
					"dims":         []interface{}{1},
					"time_unix_ns": 1780000000000000000,
					"nums":         []interface{}{1.0},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
}

// TestDxpDefAPI_Create_MultiSubstrateDef_CollapseIneligible confirms
// the registration-time analysis correctly reflects a mixed SQL+ts
// def as non-collapsible — checked at the def level before ever
// dispatching, matching how the SQL-only hotel example test checks
// the opposite case.
func TestDxpDefAPI_Create_MultiSubstrateDef_CollapseIneligible(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), phasedMultiSubstrateDef())
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, resp)
	}
	analysis, ok := resp["analysis"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a nested analysis object, got %v", resp)
	}
	if analysis["engine_homogeneous"] != false {
		t.Errorf("expected engine_homogeneous: false (bal is sql, ts is pebble), got %v", analysis["engine_homogeneous"])
	}
}

// TestDxpTxnAPI_Create_MultiSubstrateDispatch_BothCommit is the
// actual proof: a real bal transfer and a real ts append, dispatched
// together through dispatchPhased, both landing durably. Checked
// against both participants' own real side effects, not just the
// coordinator's own reported status.
func TestDxpTxnAPI_Create_MultiSubstrateDispatch_BothCommit(t *testing.T) {
	env := newPhasedDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	provisionTsAndDefineTimeline(t, env, 1, 1)

	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), phasedMultiSubstrateDef())
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "40"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected status committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != 2 {
		t.Errorf("expected committed_through 2, got %v", resp["committed_through"])
	}

	// Real side effect #1: the bal leg.
	status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account=acct"), nil)
	if status != http.StatusOK || balResp["value"] != "40" {
		t.Fatalf("acct balance after dispatch: want 40, got %d %v", status, balResp)
	}

	// Real side effect #2: the ts leg, queried back through its own
	// real HTTP API, not the coordinator's own bookkeeping.
	status, evResp := doJSONRequest(t, "GET",
		tsURLFor(env, "/events?timeline=1&dims=1&from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z&limit=10"), nil)
	if status != http.StatusOK {
		t.Fatalf("ts query range: want 200, got %d %v", status, evResp)
	}
	events, ok := evResp["events"].([]interface{})
	if !ok || len(events) != 1 {
		t.Fatalf("expected exactly 1 ts event, got %v", evResp["events"])
	}
}

// TestDxpTxnAPI_Create_MultiSubstrateDispatch_PartialFailure_TornAccepted
// is the companion adversarial case: bal's own credit-side ceiling
// check (its own documented Execute-time-only guard, not Reserve-time
// — see bal.TransferParams' own doc) refuses at Execute, while ts's
// append has nothing stopping it. This is the genuine torn-commit
// case dxp-coordinator-design.md §6 accepts by design for the phased
// path: one participant's independent commit can durably succeed
// while a sibling's fails, and committed_through must reflect that
// honestly as a real partial count — never 0, never claiming full
// success either.
func TestDxpTxnAPI_Create_MultiSubstrateDispatch_PartialFailure_TornAccepted(t *testing.T) {
	env := newPhasedDxpServer(t)
	t.Helper()

	// acct has a ceiling of 10 -- Reserve only checks the DEBIT (~in)
	// side per bal's own adapter doc, so this passes attendance and
	// fails specifically at Execute.
	for _, def := range []map[string]interface{}{
		{"account_id": "~in", "unit": "unit", "scale": 0, "floor": "-1000000000"},
		{"account_id": "acct", "unit": "unit", "scale": 0, "ceiling": "10"},
	} {
		status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), def)
		if status != http.StatusCreated {
			t.Fatalf("defining bal account %v: want 201, got %d %v", def["account_id"], status, resp)
		}
	}
	provisionTsAndDefineTimeline(t, env, 1, 1)

	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), phasedMultiSubstrateDef())
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "999"}, // exceeds acct's ceiling of 10
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "expired" {
		t.Fatalf("expected status expired (torn, accepted by design), got %v", resp["status"])
	}
	ct, ok := resp["committed_through"].(float64)
	if !ok || ct != 1 {
		t.Fatalf("expected committed_through 1 (ts committed, bal refused) -- a genuine partial count, got %v", resp["committed_through"])
	}

	// The real, durable, accepted-as-torn side effect: ts's append DID
	// land, even though the overall instance reads "expired". This is
	// the actual thing §6 accepts, not a hidden bug -- confirmed
	// directly, not inferred from committed_through alone.
	status, evResp := doJSONRequest(t, "GET",
		tsURLFor(env, "/events?timeline=1&dims=1&from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z&limit=10"), nil)
	if status != http.StatusOK {
		t.Fatalf("ts query range: want 200, got %d %v", status, evResp)
	}
	events, ok := evResp["events"].([]interface{})
	if !ok || len(events) != 1 {
		t.Fatalf("expected the ts leg to have durably landed despite the overall torn instance, got %v", evResp["events"])
	}

	// And bal's own leg genuinely did NOT land -- acct's balance is
	// still 0, not 999 and not any partial amount.
	status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account=acct"), nil)
	if status != http.StatusOK || balResp["value"] != "0" {
		t.Fatalf("acct balance: want 0 (refused at Execute, never committed), got %d %v", status, balResp)
	}
}
