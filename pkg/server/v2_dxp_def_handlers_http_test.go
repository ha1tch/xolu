// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_dxp_def_handlers_http_test.go — POST /dxp/def against a real
// running server, external package server_test (matching
// v2_bal_handlers_test.go's own convention), separate from
// v2_dxp_def_handlers_test.go's internal-package unit tests for the
// unexported validateDxpDef/parsePhaseTTL/allocDXPID.

package server_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

func dxpURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/dxp%s", sts.ts.URL, path)
}

// newDxpServer enables both bal and cal — POST /dxp/txn now actually
// dispatches (the real coordinator, not just a snapshot), so any test
// where dispatch runs needs both real participants available, exactly
// as their own direct HTTP endpoints require outside dxp. Tests that
// only register a def, or that refuse before dispatch ever runs (an
// unknown def_id, a schema violation), can still use plain
// newMetaServer.
func newDxpServer(t *testing.T) *stdTestServer {
	return newMetaServer(t, func(cfg *config.Config) {
		cfg.BalEnabled = true
		cfg.CalEnabled = true
	})
}

func TestDxpDefAPI_Create_HotelExample_Succeeds(t *testing.T) {
	env := newMetaServer(t)

	def := map[string]interface{}{
		"name":    "hotel_reserve",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "room", "primitive": "cal", "op": "book"},
			{"id": "payment", "primitive": "bal", "op": "transfer"},
			{"id": "booking", "primitive": "fsm", "op": "transition"},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, resp)
	}
	if resp["id"] == nil {
		t.Fatalf("expected an allocated id in the response, got %v", resp)
	}
	if resp["name"] != "hotel_reserve" {
		t.Fatalf("expected name echoed back, got %v", resp["name"])
	}
	analysis, ok := resp["analysis"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a nested analysis object, got %v", resp)
	}
	if analysis["collapse_eligible"] != true {
		t.Errorf("expected collapse_eligible: true, got %v", analysis["collapse_eligible"])
	}
	if analysis["engine_homogeneous"] != true {
		t.Errorf("expected engine_homogeneous: true (cal/bal/fsm are all SQL), got %v", analysis["engine_homogeneous"])
	}
}

func TestDxpDefAPI_Create_TwoRegistrationsGetIndependentIDs(t *testing.T) {
	env := newMetaServer(t)
	def := map[string]interface{}{
		"name":    "hotel_reserve",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "room", "primitive": "cal", "op": "book"},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}

	_, resp1 := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	_, resp2 := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)

	// fsm has no uniqueness constraint on name at all (checked
	// directly against its real schema before this design was
	// settled) -- registering the "same" name twice must succeed
	// twice, with two independent, sequential ids, not a conflict.
	if resp1["id"] == resp2["id"] {
		t.Fatalf("expected two independent ids for two registrations under the same name, got %v twice", resp1["id"])
	}
}

func TestDxpDefAPI_Create_InvalidPattern_Refused(t *testing.T) {
	env := newMetaServer(t)
	def := map[string]interface{}{
		"name":    "bad_def",
		"pattern": "2ps",
		"participants": []map[string]interface{}{
			{"id": "room", "primitive": "cal", "op": "book"},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a nested error object, got %v", resp)
	}
	if errObj["code"] != "XOLU-DXP006" {
		t.Fatalf("want XOLU-DXP006, got %v", errObj["code"])
	}
}

func TestDxpDefAPI_Create_UnknownPrimitive_Refused(t *testing.T) {
	env := newMetaServer(t)
	def := map[string]interface{}{
		"name":    "bad_def",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "audit", "primitive": "nonexistent_primitive", "op": "append"}, // genuinely unregistered, not ts -- ts became real (T-86/T-105)
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d %v", status, resp)
	}
}

func TestDxpDefAPI_Create_EntityCreateOp_Accepted(t *testing.T) {
	env := newMetaServer(t)
	def := map[string]interface{}{
		"name":    "place_order",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "stock", "primitive": "bal", "op": "transfer"},
			{"id": "order", "primitive": "entity", "op": "create"},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, resp)
	}
}

// ─── POST /dxp/txn ──────────────────────────────────────────────────────────

// simplePaymentDef is the worked example proposed while designing
// bindings_schema/jsonplate — one participant, for clarity.
func simplePaymentDef() map[string]interface{} {
	return map[string]interface{}{
		"name":    "simple_payment",
		"pattern": "3ps",
		"bindings_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				// string, not integer — @B04: amounts cross any JSON
				// boundary as decimal strings only, and dxp's own
				// binding path is exactly such a boundary, checked
				// directly by decodeDxpParticipantParams (T-95).
				"amount": map[string]interface{}{"type": "string"},
				"note":   map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"amount"},
		},
		"participants": []map[string]interface{}{
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
}

// defineSimplePaymentAccounts defines the two bal accounts
// simplePaymentDef's own participant references ("~in", "acct") —
// dispatch now actually runs (POST /dxp/txn drives the real
// coordinator, not just a snapshot), so any test expecting the
// transfer to genuinely succeed needs these to exist first, exactly
// as bal's own /transfer endpoint already requires outside dxp.
func defineSimplePaymentAccounts(t *testing.T, env *stdTestServer) {
	t.Helper()
	for _, def := range []map[string]interface{}{
		{"account_id": "~in", "unit": "unit", "scale": 0, "floor": "-1000000000"},
		{"account_id": "acct", "unit": "unit", "scale": 0},
	} {
		status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), def)
		if status != http.StatusCreated {
			t.Fatalf("defining bal account %v: want 201, got %d %v", def["account_id"], status, resp)
		}
	}
}

func TestDxpTxnAPI_Create_ResolvesBindingsIntoSnapshot(t *testing.T) {
	env := newDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	defID := defResp["id"]

	txn := map[string]interface{}{
		"def_id":   defID,
		"bindings": map[string]interface{}{"amount": "150", "note": "test payment"},
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Errorf("expected status committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}

	snapshot, ok := resp["snapshot"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a nested snapshot object, got %v", resp)
	}
	participants, ok := snapshot["participants"].([]interface{})
	if !ok || len(participants) != 1 {
		t.Fatalf("expected exactly 1 resolved participant, got %v", snapshot["participants"])
	}
	p0 := participants[0].(map[string]interface{})
	params := p0["params"].(map[string]interface{})
	// The whole point: amount must be the concrete bound value "150",
	// not the {"$ref": "amount"} template it was registered with — a
	// string, not a number, matching @B04's own requirement (checked
	// directly, T-95): dxp's binding path is the same JSON boundary
	// bal's own /transfer endpoint enforces this at.
	if amt, ok := params["amount"].(string); !ok || amt != "150" {
		t.Errorf("expected params.amount resolved to \"150\", got %v (type %T)", params["amount"], params["amount"])
	}
}

// TestDxpTxnAPI_Create_TwoParticipants_DispatchesBothAtomically is the
// first test anywhere in the codebase to dispatch a real /dxp/txn
// instance with more than one participant through dispatchDxpTxn
// itself (T-99). Every prior test either only registered a
// multi-participant def without ever creating an instance against it
// (TestDxpDefAPI_Create_HotelExample_Succeeds), or hand-wired
// sequential Execute calls directly (pkg/dxp/integration), never
// exercising the coordinator's own concurrent Execute+Commit
// goroutines with more than one participant at all — the exact gap
// that let T-99's race go untested. Regression coverage for its fix:
// two participants (bal, entity) dispatched together, checked against
// both the coordinator's own reported outcome and one real side
// effect (the bal balance), not just a "committed" string.
func TestDxpTxnAPI_Create_TwoParticipants_DispatchesBothAtomically(t *testing.T) {
	env := newDxpServer(t)
	defineSimplePaymentAccounts(t, env)

	def := map[string]interface{}{
		"name":    "place_order_dispatched",
		"pattern": "3ps",
		"bindings_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"amount": map[string]interface{}{"type": "string"},
				"note":   map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"amount", "note"},
		},
		"participants": []map[string]interface{}{
			{"id": "stock", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
			{"id": "order", "primitive": "entity", "op": "create",
				"params": map[string]interface{}{
					"entity": "assets",
					"data": map[string]interface{}{
						"name": map[string]interface{}{"$ref": "note"},
						"type": "order",
					},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}

	txn := map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "225", "note": "widget order"},
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected status committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != 2 {
		t.Errorf("expected committed_through 2 (both participants), got %v", resp["committed_through"])
	}

	// Real side effect, not just the coordinator's own say-so: the bal
	// leg actually landed in bal's own table.
	status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account=acct"), nil)
	if status != http.StatusOK || balResp["value"] != "225" {
		t.Fatalf("acct balance after dispatch: want 225, got %d %v", status, balResp)
	}
}

func TestDxpTxnAPI_Create_UnknownDefID_Refused(t *testing.T) {
	env := newMetaServer(t)
	txn := map[string]interface{}{"def_id": 99999, "bindings": map[string]interface{}{"amount": 5}}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d %v", status, resp)
	}
}

func TestDxpTxnAPI_Create_BindingsFailSchema_Refused(t *testing.T) {
	env := newMetaServer(t)
	_, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	defID := defResp["id"]

	// amount is required and must be >= 1 — this violates both.
	txn := map[string]interface{}{"def_id": defID, "bindings": map[string]interface{}{"note": "no amount"}}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d %v", status, resp)
	}
}

func TestDxpTxnAPI_Create_NoBindingsSchema_SkipsValidation(t *testing.T) {
	env := newDxpServer(t)
	def := map[string]interface{}{
		"name":    "hotel_reserve",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "room", "primitive": "cal", "op": "book"},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
	_, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	defID := defResp["id"]

	// No bindings_schema was declared -- any bindings (including none
	// at all) must be accepted, matching JSONSchemaValidator's own
	// "no schema means validation passes" behavior.
	txn := map[string]interface{}{"def_id": defID}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if status != http.StatusCreated {
		t.Fatalf("want 201, got %d %v", status, resp)
	}
}

func TestDxpTxnAPI_Create_TwoInstancesGetIndependentIDs(t *testing.T) {
	env := newMetaServer(t)
	_, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	defID := defResp["id"]

	txn := map[string]interface{}{"def_id": defID, "bindings": map[string]interface{}{"amount": "10"}}
	_, resp1 := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	_, resp2 := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if resp1["id"] == resp2["id"] {
		t.Fatalf("expected two independent instance ids from two separate calls, got %v twice", resp1["id"])
	}
}

// ─── Adversarial ─────────────────────────────────────────────────────────────
//
// Concurrent load, cross-tenant isolation, jsonplate edge cases (nested and
// array paths, absent paths, whole-object substitution), schema-violation
// boundaries, malformed HTTP bodies, and SQL-injection-style content — none
// of this exercised by the earlier, happy-path-focused test set.

func doRawRequest(t *testing.T, method, url, contentType string, body []byte) (int, []byte) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, respBody
}

// TestDxpDefAPI_Adversarial_ConcurrentCreatesNeverCollide fires 50
// concurrent POST /dxp/def calls and confirms every allocated id is
// genuinely unique — proving allocDXPID's atomic sequence under real
// concurrent load rather than trusting the SQL pattern's theoretical
// atomicity. Run under -race elsewhere; this test itself checks outcome
// correctness, not just absence of a race detector flag.
func TestDxpDefAPI_Adversarial_ConcurrentCreatesNeverCollide(t *testing.T) {
	env := newMetaServer(t)
	def := map[string]interface{}{
		"name":    "concurrent_def",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "room", "primitive": "cal", "op": "book"},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}

	// Warm up the tenant first — a single request so
	// TenantAutoRegister's own first-touch path completes before any
	// concurrency starts. Without this, 50 simultaneous first-touch
	// requests race tenant auto-registration itself, a separate,
	// pre-existing piece of shared infrastructure this test isn't
	// about — conflating that with allocDXPID's own concurrency
	// safety, which is what this test actually exists to prove.
	if status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def); status != http.StatusCreated {
		t.Fatalf("tenant warm-up call failed: %d %v", status, resp)
	}

	const n = 20 // kept modest deliberately -- the sandbox's own FD ceiling (ulimit -n 1024, not raisable here) means 50 truly simultaneous connections across two such tests in one suite run genuinely exhausts it; 20 already proves the same property (zero id collisions under real concurrent load), matching markDxpTxnTerminal's own concurrent test (T-93)
	ids := make([]interface{}, n)
	statuses := make([]int, n)
	resps := make([]map[string]interface{}, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
			statuses[idx] = status
			resps[idx] = resp
			ids[idx] = resp["id"]
		}(i)
	}
	wg.Wait()

	seen := make(map[interface{}]bool, n)
	for i := 0; i < n; i++ {
		if statuses[i] != http.StatusCreated {
			t.Errorf("call %d: want 201, got %d: %v", i, statuses[i], resps[i])
			continue
		}
		if seen[ids[i]] {
			t.Fatalf("call %d: id %v was already allocated to another concurrent call — allocDXPID is not safe under concurrency", i, ids[i])
		}
		seen[ids[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d unique ids, got %d", n, len(seen))
	}
}

// TestDxpTxnAPI_Adversarial_ConcurrentCreatesNeverCollide is the same
// proof for dxp_txn's own sequence, under real concurrent instantiation
// against one shared def.
func TestDxpTxnAPI_Adversarial_ConcurrentCreatesNeverCollide(t *testing.T) {
	env := newMetaServer(t)
	_, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	defID := defResp["id"]

	const n = 20 // kept modest deliberately -- the sandbox's own FD ceiling (ulimit -n 1024, not raisable here) means 50 truly simultaneous connections across two such tests in one suite run genuinely exhausts it; 20 already proves the same property (zero id collisions under real concurrent load), matching markDxpTxnTerminal's own concurrent test (T-93)
	ids := make([]interface{}, n)
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			txn := map[string]interface{}{"def_id": defID, "bindings": map[string]interface{}{"amount": strconv.Itoa(idx + 1)}}
			status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
			statuses[idx] = status
			ids[idx] = resp["id"]
		}(i)
	}
	wg.Wait()

	seen := make(map[interface{}]bool, n)
	for i := 0; i < n; i++ {
		if statuses[i] != http.StatusCreated {
			t.Errorf("call %d: want 201, got %d", i, statuses[i])
			continue
		}
		if seen[ids[i]] {
			t.Fatalf("call %d: instance id %v was already allocated to another concurrent call", i, ids[i])
		}
		seen[ids[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d unique instance ids, got %d", n, len(seen))
	}
}

// TestDxpTxnAPI_Adversarial_CrossTenantIsolation proves a def registered
// under one tenant cannot be instantiated by referencing its numeric id
// from a different tenant — a real security property, not previously
// exercised by any test.
func TestDxpTxnAPI_Adversarial_CrossTenantIsolation(t *testing.T) {
	env := newMetaServer(t)
	defaultURL := dxpURL(env, "/def")
	_, defResp := doJSONRequest(t, "POST", defaultURL, simplePaymentDef())
	defID := defResp["id"]

	otherTxnURL := fmt.Sprintf("%s/api/v2/tenant/other-tenant/dxp/txn", env.ts.URL)
	txn := map[string]interface{}{"def_id": defID, "bindings": map[string]interface{}{"amount": 50}}
	status, resp := doJSONRequest(t, "POST", otherTxnURL, txn)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 (def id %v belongs to a different tenant), got %d %v", defID, status, resp)
	}
}

// TestDxpTxnAPI_Adversarial_NestedAndArrayJsonplatePaths exercises path
// shapes never touched by the earlier flat "$ref": "amount" tests —
// nested object access and array indexing, both explicitly documented
// as supported by jsonplate's own package doc.
func TestDxpTxnAPI_Adversarial_NestedAndArrayJsonplatePaths(t *testing.T) {
	env := newMetaServer(t)
	def := map[string]interface{}{
		"name":    "nested_ref_def",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": map[string]interface{}{"$ref": "order.customer_acct"},
					"to":   "~received",
					"amount": map[string]interface{}{"$ref": "order.items[0].price"},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
	_, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	defID := defResp["id"]

	txn := map[string]interface{}{
		"def_id": defID,
		"bindings": map[string]interface{}{
			"order": map[string]interface{}{
				"customer_acct": "acct:12345",
				"items": []interface{}{
					map[string]interface{}{"price": 75, "sku": "widget-a"},
				},
			},
		},
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if status != http.StatusCreated {
		t.Fatalf("want 201, got %d %v", status, resp)
	}
	snapshot := resp["snapshot"].(map[string]interface{})
	p0 := snapshot["participants"].([]interface{})[0].(map[string]interface{})
	params := p0["params"].(map[string]interface{})
	if params["from"] != "acct:12345" {
		t.Errorf("nested path order.customer_acct: got %v, want acct:12345", params["from"])
	}
	if amt, ok := params["amount"].(float64); !ok || amt != 75 {
		t.Errorf("array-indexed path order.items[0].price: got %v (type %T), want 75", params["amount"], params["amount"])
	}
}

// TestDxpTxnAPI_Adversarial_RefToNonexistentPath_ResolvesToNull proves
// jsonplate's own documented behavior directly in this integration,
// rather than trusting the package doc's claim on faith: a $ref whose
// path is absent from the bindings resolves to JSON null, not an error.
func TestDxpTxnAPI_Adversarial_RefToNonexistentPath_ResolvesToNull(t *testing.T) {
	env := newMetaServer(t)
	def := map[string]interface{}{
		"name":    "absent_path_def",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "does_not_exist"},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
	_, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	defID := defResp["id"]

	txn := map[string]interface{}{"def_id": defID, "bindings": map[string]interface{}{"unrelated": 1}}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if status != http.StatusCreated {
		t.Fatalf("want 201 (creation itself does not fail on a null-resolved ref — Reserve refuses it later), got %d %v", status, resp)
	}
	snapshot := resp["snapshot"].(map[string]interface{})
	p0 := snapshot["participants"].([]interface{})[0].(map[string]interface{})
	params := p0["params"].(map[string]interface{})
	if params["amount"] != nil {
		t.Errorf("expected amount to resolve to JSON null for an absent path, got %v", params["amount"])
	}
}

// TestDxpTxnAPI_Adversarial_RefResolvesToWholeObject proves a $ref can
// substitute an entire object, not just a scalar — the doctrine's own
// "cal.span" example (a span is a start/end pair, not a single value).
func TestDxpTxnAPI_Adversarial_RefResolvesToWholeObject(t *testing.T) {
	env := newDxpServer(t)
	def := map[string]interface{}{
		"name":    "span_ref_def",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "room", "primitive": "cal", "op": "book",
				"params": map[string]interface{}{
					"calendar": "main",
					"span":     map[string]interface{}{"$ref": "delivery_span"},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
	_, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	defID := defResp["id"]

	span := map[string]interface{}{"start": "2026-08-01T10:00:00Z", "end": "2026-08-01T11:00:00Z"}
	txn := map[string]interface{}{"def_id": defID, "bindings": map[string]interface{}{"delivery_span": span}}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
	if status != http.StatusCreated {
		t.Fatalf("want 201, got %d %v", status, resp)
	}
	snapshot := resp["snapshot"].(map[string]interface{})
	p0 := snapshot["participants"].([]interface{})[0].(map[string]interface{})
	params := p0["params"].(map[string]interface{})
	gotSpan, ok := params["span"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected span to resolve to a whole object, got %T: %v", params["span"], params["span"])
	}
	if gotSpan["start"] != span["start"] || gotSpan["end"] != span["end"] {
		t.Errorf("resolved span = %v, want %v", gotSpan, span)
	}
}

// TestDxpTxnAPI_Adversarial_SchemaViolations is table-driven, covering
// boundary and hostile inputs against bindings_schema_json's own
// constraints, none of which the earlier "amount missing entirely" test
// exercised.
func TestDxpTxnAPI_Adversarial_SchemaViolations(t *testing.T) {
	env := newMetaServer(t)
	_, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	defID := defResp["id"]

	cases := []struct {
		name     string
		bindings map[string]interface{}
		wantOK   bool
	}{
		// bindings_schema itself only declares {"type": "string"} for
		// amount (@B04 — no numeric constraint exists on a string
		// type at all, so "zero"/"negative" schema-violation cases
		// from an earlier, wrongly-typed pass of this schema no
		// longer apply; bal.ParseAmount's own semantic validation of
		// the string's content is a separate layer, already covered
		// directly by decodeDxpParticipantParams' own unit tests, T-95).
		{"wrong type (number for string)", map[string]interface{}{"amount": 150}, false},
		{"null amount", map[string]interface{}{"amount": nil}, false},
		{"missing amount entirely", map[string]interface{}{"note": "no amount"}, false},
		// Extra, undeclared fields are REJECTED, not allowed — proven
		// here rather than assumed. pkg/validation's own doc comment
		// claims "Loose mode by default: additional properties are
		// allowed" but its actual code uses queryfy.Strict (checked
		// directly, a real discrepancy in that file, filed separately
		// as T-91). handleDxpTxnCreate matches the real, tested
		// behavior (queryfy.Strict, copied from the actual code, not
		// the doc comment) — so this must refuse, matching what
		// entity's own adapted-table schemas have actually been
		// running against all along.
		{"extra undeclared field, Strict mode rejects it", map[string]interface{}{"amount": "10", "unexpected_field": "ignored"}, false},
		{"a valid decimal string", map[string]interface{}{"amount": "1"}, true},
		{"a large decimal string", map[string]interface{}{"amount": "999999999"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			txn := map[string]interface{}{"def_id": defID, "bindings": c.bindings}
			status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), txn)
			gotOK := status == http.StatusCreated
			if gotOK != c.wantOK {
				t.Errorf("bindings %v: want ok=%v, got status %d %v", c.bindings, c.wantOK, status, resp)
			}
		})
	}
}

// TestDxpDefAPI_Adversarial_MalformedHTTPBodies proves the handler
// degrades to a clean 4xx rather than panicking or hanging on input
// that never reaches valid JSON at all.
func TestDxpDefAPI_Adversarial_MalformedHTTPBodies(t *testing.T) {
	env := newMetaServer(t)
	cases := []struct {
		name        string
		contentType string
		body        []byte
	}{
		{"empty body", "application/json", []byte{}},
		{"truncated JSON", "application/json", []byte(`{"name": "x", "pattern"`)},
		{"not JSON at all", "application/json", []byte(`this is not json`)},
		{"JSON array instead of object", "application/json", []byte(`[1,2,3]`)},
		{"deeply nested garbage", "application/json", []byte(strings.Repeat(`{"a":`, 1000) + `1` + strings.Repeat(`}`, 1000))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body := doRawRequest(t, "POST", dxpURL(env, "/def"), c.contentType, c.body)
			if status < 400 || status >= 500 {
				t.Errorf("expected a clean 4xx, got %d: %s", status, body)
			}
		})
	}
}

// TestDxpDefAPI_Adversarial_SQLInjectionStyleContent proves free-form
// string fields (name, participant id, bindings values) that look like
// SQL injection attempts are treated as inert data, not executed —
// proving parameterized queries hold rather than trusting the design.
func TestDxpDefAPI_Adversarial_SQLInjectionStyleContent(t *testing.T) {
	env := newMetaServer(t)
	malicious := "'; DROP TABLE dxp_defs; --"
	def := map[string]interface{}{
		"name":    malicious,
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": malicious, "primitive": "cal", "op": "book"},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT90S"},
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("want 201 (malicious-looking content is just a string), got %d %v", status, resp)
	}

	// If the table had actually been dropped, this second, entirely
	// unrelated registration would fail with a SQL error instead of
	// succeeding normally.
	status2, resp2 := doJSONRequest(t, "POST", dxpURL(env, "/def"), simplePaymentDef())
	if status2 != http.StatusCreated {
		t.Fatalf("dxp_defs appears to have been damaged by the malicious name: %d %v", status2, resp2)
	}
}
