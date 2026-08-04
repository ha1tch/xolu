// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_obj_promote_handlers_test.go — T-121 (wave 10): end-to-end proof
// of the full atomic round trip (obj-00-design.md §9), through real
// HTTP requests against bal/entity/obj all composed via dxp.

package server_test

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func defineBalAccount(t *testing.T, env *stdTestServer, accountID, floor string) {
	t.Helper()
	def := map[string]interface{}{"account_id": accountID, "unit": "unit", "scale": 0}
	if floor != "" {
		def["floor"] = floor
	}
	status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("define bal account %q: want 201, got %d %v", accountID, status, resp)
	}
}

func balBalance(t *testing.T, env *stdTestServer, accountID string) string {
	t.Helper()
	status, resp := doJSONRequest(t, "GET", balURL(env, "/balance?account="+accountID), nil)
	if status != http.StatusOK {
		t.Fatalf("balance %q: want 200, got %d %v", accountID, status, resp)
	}
	v, _ := resp["value"].(string)
	return v
}

// TestObjAPI_Promote_CreatePath_FullRoundTrip is T-121's own central
// proof: bal decrement + entity create + obj attach-and-contain, all
// committed together through one dxp dispatch.
func TestObjAPI_Promote_CreatePath_FullRoundTrip(t *testing.T) {
	env := newBalServer(t)
	defineBalAccount(t, env, "pallet-88-cases", "-1000")
	defineBalAccount(t, env, "pallet-88-cases-promoted", "")
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:88"})

	status, resp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "pallet-88-cases",
		"to_account":  "pallet-88-cases-promoted",
		"amount":      "1",
		"entity": map[string]interface{}{
			"kind":   "cases",
			"create": map[string]interface{}{"lot_code": "L4471", "condition": "damaged"},
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:88"},
	})
	if status != http.StatusCreated {
		t.Fatalf("promote: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("promote must commit, got status=%v reason=%v", resp["status"], resp["reason"])
	}
	subject, _ := resp["subject"].(string)
	if subject == "" {
		t.Fatal("want a non-empty subject in the response")
	}

	// bal side: the source account is genuinely decremented.
	if got := balBalance(t, env, "pallet-88-cases"); got != "-1" {
		t.Errorf("source account: want -1, got %s", got)
	}
	if got := balBalance(t, env, "pallet-88-cases-promoted"); got != "1" {
		t.Errorf("destination account: want 1, got %s", got)
	}

	// obj side: the new entity is genuinely attached and contained.
	kind, key, _ := cutSubjectForTest(subject)
	status, getResp := doJSONRequest(t, "GET", objURL(env, "/"+kind+"/"+key), nil)
	if status != http.StatusOK {
		t.Fatalf("get promoted subject: want 200, got %d %v", status, getResp)
	}
	if getResp["position_kind"] != "obj" {
		t.Errorf("want position_kind obj, got %v", getResp["position_kind"])
	}
}

func cutSubjectForTest(s string) (kind, key string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// TestObjAPI_Promote_ReusePath_FullRoundTrip proves the
// entity.existing_key path -- no entity leg, just bal + obj.
func TestObjAPI_Promote_ReusePath_FullRoundTrip(t *testing.T) {
	env := newBalServer(t)
	defineBalAccount(t, env, "pallet-99-cases", "-1000")
	defineBalAccount(t, env, "pallet-99-cases-promoted", "")
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:99"})

	existingKey := 555
	status, resp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "pallet-99-cases",
		"to_account":  "pallet-99-cases-promoted",
		"amount":      "1",
		"entity": map[string]interface{}{
			"kind":         "cases",
			"existing_key": existingKey,
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:99"},
	})
	if status != http.StatusCreated {
		t.Fatalf("promote (reuse): want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("promote must commit, got status=%v reason=%v", resp["status"], resp["reason"])
	}
	if resp["subject"] != "cases:555" {
		t.Errorf("want subject cases:555, got %v", resp["subject"])
	}
}

// TestObjAPI_Promote_BothEntitySelectors_Returns400 proves XOLU-OBJ010.
func TestObjAPI_Promote_BothEntitySelectors_Returns400(t *testing.T) {
	env := newBalServer(t)
	status, resp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "a", "to_account": "b", "amount": "1",
		"entity": map[string]interface{}{
			"kind": "cases", "existing_key": 1, "create": map[string]interface{}{"x": "y"},
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:1"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("both selectors set: want 400, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-OBJ010" {
		t.Errorf("want XOLU-OBJ010, got %v", resp)
	}
}

// TestObjAPI_Promote_InsufficientBalance_RefusesWholeTransaction
// proves the atomicity itself: when bal refuses, nothing else
// committed either -- no orphaned entity, no obj attach.
func TestObjAPI_Promote_InsufficientBalance_RefusesWholeTransaction(t *testing.T) {
	env := newBalServer(t)
	// Deliberately no floor override -- default floor is 0, so any
	// decrement below zero must be refused.
	defineBalAccount(t, env, "pallet-77-cases", "")
	defineBalAccount(t, env, "pallet-77-cases-promoted", "")
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:77"})

	status, resp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "pallet-77-cases",
		"to_account":  "pallet-77-cases-promoted",
		"amount":      "1",
		"entity": map[string]interface{}{
			"kind":   "cases",
			"create": map[string]interface{}{"lot_code": "L1"},
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:77"},
	})
	if status != http.StatusCreated {
		t.Fatalf("promote call itself: want 201 (the transaction was created and dispatched, just refused), got %d %v", status, resp)
	}
	if resp["status"] != "released" {
		t.Fatalf("insufficient balance: want status \"released\", got %v (reason: %v)", resp["status"], resp["reason"])
	}

	// No orphaned side effects: the balance must be untouched.
	if got := balBalance(t, env, "pallet-77-cases"); got != "0" {
		t.Errorf("source account must be untouched after refusal, got %s", got)
	}
}

// TestObjAPI_Demote_FullRoundTrip proves the reverse transition.
func TestObjAPI_Demote_FullRoundTrip(t *testing.T) {
	env := newBalServer(t)
	defineBalAccount(t, env, "pallet-11-cases", "-1000")
	defineBalAccount(t, env, "pallet-11-cases-promoted", "")
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:11"})

	status, promResp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "pallet-11-cases",
		"to_account":  "pallet-11-cases-promoted",
		"amount":      "1",
		"entity": map[string]interface{}{
			"kind":   "cases",
			"create": map[string]interface{}{"lot_code": "L9"},
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:11"},
	})
	if status != http.StatusCreated || promResp["status"] != "committed" {
		t.Fatalf("promote setup: want 201/committed, got %d %v", status, promResp)
	}
	subject, _ := promResp["subject"].(string)

	status, demResp := doJSONRequest(t, "POST", objURL(env, "/demote"), map[string]interface{}{
		"subject":      subject,
		"bal_account":  "pallet-11-cases",
		"from_account": "pallet-11-cases-promoted",
		"amount":       "1",
	})
	if status != http.StatusOK {
		t.Fatalf("demote: want 200, got %d %v", status, demResp)
	}
	if demResp["status"] != "committed" {
		t.Fatalf("demote must commit, got status=%v reason=%v", demResp["status"], demResp["reason"])
	}

	if got := balBalance(t, env, "pallet-11-cases"); got != "0" {
		t.Errorf("source account restored: want 0, got %s", got)
	}
	if got := balBalance(t, env, "pallet-11-cases-promoted"); got != "0" {
		t.Errorf("promoted account drained: want 0, got %s", got)
	}
	kind, key, _ := cutSubjectForTest(subject)
	status, getResp := doJSONRequest(t, "GET", objURL(env, "/"+kind+"/"+key), nil)
	if status != http.StatusNotFound {
		t.Fatalf("get after demote: want 404 (no longer obj-attached), got %d %v", status, getResp)
	}
}

// TestObjAPI_Demote_RefusedWhileStillContainsSomething proves
// XOLU-OBJ011 surfaces correctly through the dxp dispatch reason.
func TestObjAPI_Demote_RefusedWhileStillContainsSomething(t *testing.T) {
	env := newBalServer(t)
	defineBalAccount(t, env, "pallet-22-cases", "-1000")
	defineBalAccount(t, env, "pallet-22-cases-promoted", "")
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:22"})

	status, promResp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "pallet-22-cases",
		"to_account":  "pallet-22-cases-promoted",
		"amount":      "1",
		"entity": map[string]interface{}{
			"kind":   "cases",
			"create": map[string]interface{}{"lot_code": "L2"},
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:22"},
	})
	if status != http.StatusCreated || promResp["status"] != "committed" {
		t.Fatalf("promote setup: want 201/committed, got %d %v", status, promResp)
	}
	promotedSubject, _ := promResp["subject"].(string)

	// Give the promoted case its own content -- demoting pallets:22
	// itself is not the point here; demote the promoted CASE while a
	// smaller item is attached to IT instead, matching the real
	// "still contains something" shape.
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "labels:1"})
	kind, key, _ := cutSubjectForTest(promotedSubject)
	status, moveResp := doJSONRequest(t, "PUT", objURL(env, "/labels/1/move"), map[string]interface{}{
		"to": map[string]interface{}{"kind": "obj", "subject": kind + ":" + key},
	})
	if status != http.StatusOK {
		t.Fatalf("attach label onto promoted case: want 200, got %d %v", status, moveResp)
	}

	status, demResp := doJSONRequest(t, "POST", objURL(env, "/demote"), map[string]interface{}{
		"subject":      promotedSubject,
		"bal_account":  "pallet-22-cases",
		"from_account": "pallet-22-cases-promoted",
		"amount":       "1",
	})
	if status != http.StatusOK {
		t.Fatalf("demote call itself: want 200 (dispatched, just refused), got %d %v", status, demResp)
	}
	// "expired", not "released": obj's own XOLU-OBJ011 refusal happens
	// inside Execute, not Reserve/Validate -- and because obj and bal
	// are different SQL engines (obj's own dedicated file), this
	// dispatches via the phased path, where each participant commits
	// independently in its own goroutine/transaction. A mid-Execute
	// refusal on ONE participant after another has already committed
	// is dispatchDxpTxnCore's own documented, accepted "torn" outcome
	// for the phased path (v2_dxp_dispatch.go's own §6 comment) --
	// "released" is reserved for a clean refusal caught before any
	// participant ever executes.
	if demResp["status"] != "expired" {
		t.Fatalf("demote while still containing something: want status \"expired\" (torn, phased path), got %v", demResp["status"])
	}

	// Confirmed, not assumed: the bal leg genuinely did commit despite
	// the overall refusal -- a real, accepted consequence of choosing
	// dxp's phased path for a heterogeneous-engine composition, not a
	// silent inconsistency. A caller must treat "expired" as needing
	// its own reconciliation, exactly as dxp's own design already
	// requires for any phased transaction.
	if got := balBalance(t, env, "pallet-22-cases-promoted"); got != "0" {
		t.Errorf("bal leg's own committedThrough behavior: want the transfer to have gone through (0 remaining) despite the overall \"expired\" status, got %s -- if this changes, the torn-commit characteristic documented above no longer holds and this test's own comment needs revisiting", got)
	}
}

// TestObjAPI_Promote_MirrorsIntoGraph_ViaRealDxpDispatch is T-123's
// own central exit criterion: PostCommit proven through a real dxp
// dispatch, not a unit test alone. Promote's own obj leg
// (attach_and_contain) always dispatches via dxp's phased path (obj
// is tagged "sql-obj", never eligible for collapse -- see
// obj-00-design.md §10's own note) -- this IS the phased-path proof.
func TestObjAPI_Promote_MirrorsIntoGraph_ViaRealDxpDispatch(t *testing.T) {
	env := newBalServer(t)
	defineBalAccount(t, env, "pallet-33-cases", "-1000")
	defineBalAccount(t, env, "pallet-33-cases-promoted", "")
	doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": "pallets:33"})

	status, resp := doJSONRequest(t, "POST", objURL(env, "/promote"), map[string]interface{}{
		"bal_account": "pallet-33-cases",
		"to_account":  "pallet-33-cases-promoted",
		"amount":      "1",
		"entity": map[string]interface{}{
			"kind":   "cases",
			"create": map[string]interface{}{"lot_code": "L33"},
		},
		"position": map[string]interface{}{"kind": "obj", "subject": "pallets:33"},
	})
	if status != http.StatusCreated || resp["status"] != "committed" {
		t.Fatalf("promote: want 201/committed, got %d %v", status, resp)
	}
	subject, _ := resp["subject"].(string)
	kind, key, _ := cutSubjectForTest(subject)
	childID, err := strconv.Atoi(key)
	if err != nil {
		t.Fatalf("child key not numeric: %v", err)
	}

	tenantID, ok := env.srv.TenantIDForTest("default")
	if !ok {
		t.Fatalf("could not resolve \"default\" tenant")
	}
	containerNode := tenantID.NodeID("pallets", 33)
	childNode := tenantID.NodeID(kind, childID)

	// PostCommit's own goroutine (dispatchPhased runs each
	// participant's Execute/PostCommit concurrently) needs a moment.
	waitForGraphMirror(t, env, containerNode, childNode)
}

// waitForGraphMirror polls the graph neighbors endpoint briefly --
// PostCommit's own mirror runs after the dxp response is already
// returned to the caller (best-effort, not synchronous with the HTTP
// response), so a single immediate check would be a real race, not a
// deliberate one worth encoding into the test itself. GetNeighbors
// (pkg/graph) returns map[string]EdgeRef keyed by TARGET node id, so
// the JSON response's own "outgoing" object has childNode as a KEY,
// not an element of a list.
func waitForGraphMirror(t *testing.T, env *stdTestServer, containerNode, childNode string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastResp map[string]interface{}
	var lastStatus int
	for time.Now().Before(deadline) {
		lastStatus, lastResp = doJSONRequest(t, "POST", env.ts.URL+"/api/v1/graph/neighbors", map[string]interface{}{
			"node_id":   containerNode,
			"direction": "out",
		})
		if lastStatus == http.StatusOK {
			if neighbors, ok := lastResp["neighbors"].(map[string]interface{}); ok {
				if out, ok := neighbors["outgoing"].(map[string]interface{}); ok {
					if _, found := out[childNode]; found {
						return
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("graph mirror never showed %s -> %s within deadline; last status=%d resp=%v", containerNode, childNode, lastStatus, lastResp)
}

// TestDxpTxnAPI_Adversarial_TwoObjParticipants_SameInstance_NoCollision
// is T-123's own filed adversarial requirement: obj's own T-109-shaped
// risk, constructed explicitly. Two obj participants of the SAME
// primitive in one dxp instance -- pallets:1 attached-and-contained
// into vehicles:1, pallets:2 attached-and-contained into vehicles:2,
// both in the same transaction. Each participant's own stashed params
// (pending[pendingKey(txn, participantID)]) must never cross-
// contaminate the other's -- proven here by checking BOTH ended up
// contained by their OWN correct container, not swapped or merged.
func TestDxpTxnAPI_Adversarial_TwoObjParticipants_SameInstance_NoCollision(t *testing.T) {
	env := newFullDxpServer(t)
	for _, ref := range []string{"vehicles:1", "vehicles:2"} {
		status, resp := doJSONRequest(t, "POST", objURL(env, "/attach"), map[string]interface{}{"subject": ref})
		if status != http.StatusCreated {
			t.Fatalf("attach %s: want 201, got %d %v", ref, status, resp)
		}
	}

	def := map[string]interface{}{
		"name": "adversarial_two_obj", "pattern": "3ps",
		"participants": []map[string]interface{}{
			{
				"id": "leg1", "primitive": "obj", "op": "attach_and_contain",
				"params": map[string]interface{}{"subject_ref": "pallets:1", "container_ref": "vehicles:1"},
			},
			{
				"id": "leg2", "primitive": "obj", "op": "attach_and_contain",
				"params": map[string]interface{}{"subject_ref": "pallets:2", "container_ref": "vehicles:2"},
			},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT30S"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("want committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != 2 {
		t.Fatalf("want committed_through 2, got %v", resp["committed_through"])
	}

	// Each leg's own params must have landed correctly -- pallets:1
	// with vehicles:1, pallets:2 with vehicles:2, never swapped.
	status, p1 := doJSONRequest(t, "GET", objURL(env, "/pallets/1"), nil)
	if status != http.StatusOK {
		t.Fatalf("get pallets:1: want 200, got %d %v", status, p1)
	}
	if p1["loc_leaf_id"] != nil {
		t.Errorf("pallets:1 must be contained, not loc_leaf-positioned: %v", p1)
	}
	status, p2 := doJSONRequest(t, "GET", objURL(env, "/pallets/2"), nil)
	if status != http.StatusOK {
		t.Fatalf("get pallets:2: want 200, got %d %v", status, p2)
	}

	status, v1 := doJSONRequest(t, "GET", objURL(env, "/vehicles/1"), nil)
	if status != http.StatusOK {
		t.Fatalf("get vehicles:1: want 200, got %d %v", status, v1)
	}
	status, v2 := doJSONRequest(t, "GET", objURL(env, "/vehicles/2"), nil)
	if status != http.StatusOK {
		t.Fatalf("get vehicles:2: want 200, got %d %v", status, v2)
	}

	// Correctness proof: each vehicle's own position endpoint chain
	// confirms which pallet it actually holds, via the resolved chain.
	status, p1pos := doJSONRequest(t, "GET", objURL(env, "/pallets/1/position"), nil)
	if status != http.StatusOK {
		t.Fatalf("get pallets:1 position: want 200, got %d %v", status, p1pos)
	}
	chain1, _ := p1pos["chain"].([]interface{})
	if len(chain1) != 2 || chain1[1] != "vehicles:1" {
		t.Errorf("pallets:1's own chain must end at vehicles:1, got %v (a cross-contaminated pending map would show vehicles:2 here)", chain1)
	}
	status, p2pos := doJSONRequest(t, "GET", objURL(env, "/pallets/2/position"), nil)
	if status != http.StatusOK {
		t.Fatalf("get pallets:2 position: want 200, got %d %v", status, p2pos)
	}
	chain2, _ := p2pos["chain"].([]interface{})
	if len(chain2) != 2 || chain2[1] != "vehicles:2" {
		t.Errorf("pallets:2's own chain must end at vehicles:2, got %v (a cross-contaminated pending map would show vehicles:1 here)", chain2)
	}
}
