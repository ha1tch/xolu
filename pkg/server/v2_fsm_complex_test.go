// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_complex_test.go — adversarial machines more complex than the packet
// validator, each targeting a dimension the validator did not exercise:
//
//   1. Accumulator loop      — stateful iteration; a self-loop accumulates into
//                              a variable across many walks, with an interval
//                              guard deciding the exit (loose).
//   2. Diamond reconvergence — multiple paths to a common state then onward;
//                              reachability and path-independence (strict).
//   3. Sequence allocation   — NEXT VALUE FOR across a multi-step walk; IDs
//                              monotonic and gap-free under the transaction.
//   4. Full recognizer       — one loose machine whose states between them use
//                              every recognized exclusivity pattern, proving the
//                              recognizer holds up when patterns coexist.

import (
	"fmt"
	"net/http"
	"testing"
)

// ─── 1. Accumulator loop ──────────────────────────────────────────────────────

// quotaAccumulatorSpec: a Collecting state loops on "add", summing payload.amount
// into @total, until @total reaches a threshold, then "finalise" routes on the
// accumulated total. The Collecting "finalise" edges are loose: disjoint
// intervals on @total.
func quotaAccumulatorSpec() map[string]interface{} {
	return map[string]interface{}{
		"name":        "QuotaAccumulator",
		"initial":     "Collecting",
		"determinism": "loose",
		"states": map[string]interface{}{
			"Collecting": map[string]interface{}{"terminal": false},
			"Met":        map[string]interface{}{"terminal": true},
			"Short":      map[string]interface{}{"terminal": true},
		},
		"variables": map[string]interface{}{
			"@total": map[string]interface{}{"type": "int", "default": 0},
		},
		"transitions": []map[string]interface{}{
			// Self-loop: accumulate. Single edge on "add" (strict-shaped, but the
			// machine is loose overall because of the finalise edges).
			{"from": "Collecting", "input": "add", "to": "Collecting",
				"set": map[string]string{"@total": "@total + payload.amount"}},
			// Finalise: disjoint interval guards on the accumulated total.
			{"from": "Collecting", "input": "finalise", "to": "Met",
				"guard": "@total >= 100"},
			{"from": "Collecting", "input": "finalise", "to": "Short",
				"guard": "@total < 100"},
		},
	}
}

func TestComplex_AccumulatorLoopMet(t *testing.T) {
	env := newV2Server(t)
	st, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), quotaAccumulatorSpec())
	if st != http.StatusCreated {
		t.Fatalf("create accumulator (loose, interval guards): %d %v", st, resp)
	}
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	// Accumulate 40 + 35 + 30 = 105 over three walks.
	for _, amt := range []int{40, 35, 30} {
		wst, wResp := walk(t, env, id, "add", map[string]interface{}{"amount": amt})
		if wst != http.StatusOK {
			t.Fatalf("add %d: want 200, got %d: %v", amt, wst, wResp)
		}
	}
	wst, wResp := walk(t, env, id, "finalise", nil)
	if wst != http.StatusOK {
		t.Fatalf("finalise: want 200, got %d: %v", wst, wResp)
	}
	if wResp["current"] != "Met" {
		t.Errorf("total 105 >= 100 should reach Met, got %v", wResp["current"])
	}
}

func TestComplex_AccumulatorLoopShort(t *testing.T) {
	env := newV2Server(t)
	_, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), quotaAccumulatorSpec())
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	// Accumulate 20 + 30 = 50, below threshold.
	for _, amt := range []int{20, 30} {
		walk(t, env, id, "add", map[string]interface{}{"amount": amt})
	}
	_, wResp := walk(t, env, id, "finalise", nil)
	if wResp["current"] != "Short" {
		t.Errorf("total 50 < 100 should reach Short, got %v", wResp["current"])
	}
}

func TestComplex_AccumulatorExactBoundary(t *testing.T) {
	// Exactly 100: the >= edge must win, not the < edge. Pins the interval
	// boundary the recognizer proved disjoint.
	env := newV2Server(t)
	_, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), quotaAccumulatorSpec())
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(mResp["id"].(float64))

	walk(t, env, id, "add", map[string]interface{}{"amount": 100})
	_, wResp := walk(t, env, id, "finalise", nil)
	if wResp["current"] != "Met" {
		t.Errorf("total exactly 100 should reach Met (>= boundary), got %v", wResp["current"])
	}
}

// ─── 2. Diamond reconvergence ─────────────────────────────────────────────────

// approvalDiamondSpec: Draft can go via either FastTrack or Review to Approved,
// then Approved -> Published. Two paths reconverge at Approved. strict: one edge
// per (state, input).
func approvalDiamondSpec() map[string]interface{} {
	return map[string]interface{}{
		"name":        "ApprovalDiamond",
		"initial":     "Draft",
		"determinism": "strict",
		"states": map[string]interface{}{
			"Draft":     map[string]interface{}{"terminal": false},
			"FastTrack": map[string]interface{}{"terminal": false},
			"Review":    map[string]interface{}{"terminal": false},
			"Approved":  map[string]interface{}{"terminal": false},
			"Published": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "Draft", "input": "expedite", "to": "FastTrack"},
			{"from": "Draft", "input": "submit", "to": "Review"},
			{"from": "FastTrack", "input": "approve", "to": "Approved"},
			{"from": "Review", "input": "approve", "to": "Approved"},
			{"from": "Approved", "input": "publish", "to": "Published"},
		},
	}
}

func TestComplex_DiamondBothPathsReachPublished(t *testing.T) {
	env := newV2Server(t)
	st, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), approvalDiamondSpec())
	if st != http.StatusCreated {
		t.Fatalf("create diamond (strict): %d %v", st, resp)
	}
	defID := int64(resp["id"].(float64))

	// Path A: expedite -> approve -> publish.
	_, mA := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	idA := int64(mA["id"].(float64))
	for _, in := range []string{"expedite", "approve", "publish"} {
		st, r := walk(t, env, idA, in, nil)
		if st != http.StatusOK {
			t.Fatalf("path A %q: %d %v", in, st, r)
		}
	}
	_, sA := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", idA)), nil)
	if sA["state"] != "Published" {
		t.Errorf("path A should end Published, got %v", sA["state"])
	}

	// Path B: submit -> approve -> publish, reconverging at Approved.
	_, mB := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	idB := int64(mB["id"].(float64))
	for _, in := range []string{"submit", "approve", "publish"} {
		st, r := walk(t, env, idB, in, nil)
		if st != http.StatusOK {
			t.Fatalf("path B %q: %d %v", in, st, r)
		}
	}
	_, sB := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", idB)), nil)
	if sB["state"] != "Published" {
		t.Errorf("path B should end Published, got %v", sB["state"])
	}
}

func TestComplex_DiamondWrongInputForPathRejected(t *testing.T) {
	// After expedite (in FastTrack), the Review-only input "approve" works, but
	// the Draft-only inputs must not.
	env := newV2Server(t)
	_, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), approvalDiamondSpec())
	defID := int64(resp["id"].(float64))
	_, m := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(m["id"].(float64))

	walk(t, env, id, "expedite", nil)        // now in FastTrack
	st, r := walk(t, env, id, "submit", nil) // submit is a Draft input
	if st != http.StatusConflict || errCode(r) != "XOLU-FSM003" {
		t.Errorf("submit from FastTrack should be XOLU-FSM003, got %d/%v", st, r["error"])
	}
}

// ─── 3. Sequence allocation across steps ──────────────────────────────────────

// orderSeqSpec allocates an order number and a shipment number from two
// different sequences at two different steps. firstmatch (single edges, no need
// for exclusivity), exercising NEXT VALUE FOR atomicity.
func orderSeqSpec() map[string]interface{} {
	return map[string]interface{}{
		"name":        "OrderSequencing",
		"initial":     "Cart",
		"determinism": "firstmatch",
		"states": map[string]interface{}{
			"Cart":    map[string]interface{}{"terminal": false},
			"Ordered": map[string]interface{}{"terminal": false},
			"Shipped": map[string]interface{}{"terminal": true},
		},
		"variables": map[string]interface{}{
			"@order_no": map[string]interface{}{"type": "int", "default": 0},
			"@ship_no":  map[string]interface{}{"type": "int", "default": 0},
		},
		"transitions": []map[string]interface{}{
			{"from": "Cart", "input": "place", "to": "Ordered",
				"set": map[string]string{"@order_no": "NEXT VALUE FOR order_seq"}},
			{"from": "Ordered", "input": "ship", "to": "Shipped",
				"set": map[string]string{"@ship_no": "NEXT VALUE FOR ship_seq"}},
		},
	}
}

func TestComplex_SequenceAllocationMonotonic(t *testing.T) {
	env := newV2Server(t)
	// Create the two sequences.
	for _, s := range []struct {
		name  string
		start int
	}{{"order_seq", 5000}, {"ship_seq", 9000}} {
		st, r := doJSONRequest(t, "POST", seqURL(env, ""),
			map[string]interface{}{"name": s.name, "start": s.start})
		if st != http.StatusCreated && st != http.StatusOK {
			t.Fatalf("create seq %s: %d %v", s.name, st, r)
		}
	}
	_, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), orderSeqSpec())
	defID := int64(resp["id"].(float64))

	// Two orders should get distinct, monotonic order numbers.
	var orderNos []float64
	for i := 0; i < 2; i++ {
		_, m := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
		id := int64(m["id"].(float64))
		_, wr := walk(t, env, id, "place", nil)
		vars, _ := wr["vars"].(map[string]interface{})
		on, _ := vars["@order_no"].(float64)
		orderNos = append(orderNos, on)
		// ship in the same machine, from a different sequence
		_, wr2 := walk(t, env, id, "ship", nil)
		v2, _ := wr2["vars"].(map[string]interface{})
		sn, _ := v2["@ship_no"].(float64)
		if sn < 9000 {
			t.Errorf("ship_no should come from ship_seq (>=9000), got %v", sn)
		}
	}
	if !(orderNos[0] >= 5000 && orderNos[1] > orderNos[0]) {
		t.Errorf("order numbers should be monotonic from 5000, got %v", orderNos)
	}
}

// ─── 4. Full recognizer vocabulary in one loose machine ───────────────────────

// fullVocabSpec is a single loose machine whose four branching states between
// them use every recognized exclusivity pattern: null partition, distinct
// equality, disjoint intervals, interval complement, and var-vs-var. If the
// recognizer holds up with all patterns coexisting in one definition, this
// creates successfully; if any pattern regresses, creation fails.
func fullVocabSpec() map[string]interface{} {
	return map[string]interface{}{
		"name":        "FullVocab",
		"initial":     "S_null",
		"determinism": "loose",
		"states": map[string]interface{}{
			"S_null":     map[string]interface{}{"terminal": false},
			"S_eq":       map[string]interface{}{"terminal": false},
			"S_interval": map[string]interface{}{"terminal": false},
			"S_relvar":   map[string]interface{}{"terminal": false},
			"Done":       map[string]interface{}{"terminal": true},
		},
		"variables": map[string]interface{}{
			"@expected": map[string]interface{}{"type": "int", "default": 0},
		},
		"transitions": []map[string]interface{}{
			// null partition
			{"from": "S_null", "input": "n", "to": "S_eq", "guard": "payload.x IS NULL"},
			{"from": "S_null", "input": "n", "to": "S_eq", "guard": "payload.x IS NOT NULL"},
			// distinct equality
			{"from": "S_eq", "input": "e", "to": "S_interval", "guard": "payload.code = 1"},
			{"from": "S_eq", "input": "e", "to": "S_interval", "guard": "payload.code = 2"},
			{"from": "S_eq", "input": "e", "to": "S_interval", "guard": "payload.code = 3"},
			// disjoint interval vs its complement
			{"from": "S_interval", "input": "i", "to": "S_relvar",
				"guard": "payload.len > 0 AND payload.len <= 1024"},
			{"from": "S_interval", "input": "i", "to": "S_relvar",
				"guard": "payload.len <= 0 OR payload.len > 1024"},
			// var-vs-var complementarity
			{"from": "S_relvar", "input": "r", "to": "Done",
				"guard": "payload.got = @expected"},
			{"from": "S_relvar", "input": "r", "to": "Done",
				"guard": "payload.got != @expected"},
		},
	}
}

func TestComplex_FullVocabularyLooseValidates(t *testing.T) {
	env := newV2Server(t)
	st, resp := doJSONRequest(t, "POST", fsmDefURL(env, "/validate"), fullVocabSpec())
	if st != http.StatusOK {
		t.Fatalf("full-vocab loose validate: want 200, got %d: %v", st, resp)
	}
	analysis, _ := resp["analysis"].(map[string]interface{})
	if analysis["exclusivity_verified"] != true {
		t.Errorf("full-vocab machine should be exclusivity_verified, got %v", analysis["exclusivity_verified"])
	}
}

func TestComplex_FullVocabularyCreatesAndWalks(t *testing.T) {
	env := newV2Server(t)
	st, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), fullVocabSpec())
	if st != http.StatusCreated {
		t.Fatalf("full-vocab create: want 201, got %d: %v", st, resp)
	}
	defID := int64(resp["id"].(float64))
	_, m := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	id := int64(m["id"].(float64))

	// Walk each branching state via one of its disjoint guards.
	steps := []struct {
		input   string
		payload map[string]interface{}
	}{
		{"n", map[string]interface{}{"x": 1}},     // x IS NOT NULL
		{"e", map[string]interface{}{"code": 2}},  // code = 2
		{"i", map[string]interface{}{"len": 512}}, // 0 < len <= 1024
		{"r", map[string]interface{}{"got": 0}},   // got = @expected (both 0)
	}
	for _, s := range steps {
		st, r := walk(t, env, id, s.input, s.payload)
		if st != http.StatusOK {
			t.Fatalf("full-vocab walk %q: want 200, got %d: %v", s.input, st, r)
		}
	}
	_, sf := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", id)), nil)
	if sf["state"] != "Done" {
		t.Errorf("full-vocab walk should reach Done, got %v", sf["state"])
	}
}
