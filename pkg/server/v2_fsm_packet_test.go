// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_fsm_packet_test.go — S7/S8: a non-trivial validator FSM.
//
// PacketValidator checks a framed packet field by field, rejecting malformed
// packets. It exercises behaviour AssetLifecycle does not:
//
//   - guard-disambiguated multi-edges: each validating state has three
//     transitions on the same input — accept (field present and valid),
//     reject-invalid (present but wrong), reject-missing (absent) — selected
//     at walk time by their guards;
//   - two terminal states (Accepted / Rejected) rather than one;
//   - presence checks via IS NULL / IS NOT NULL, so a missing field is
//     rejected cleanly rather than collapsing to a false comparison;
//   - multi-variable guards (@received = @expected);
//   - a payload-accumulating self-loop with a set clause.
//
// Frame: header(magic, version) -> length(len) -> payload chunks -> end ->
// checksum(crc). Valid only if version==1, 0<len<=1024, the accumulated chunk
// sizes equal len, and crc matches the running total.

import (
	"fmt"
	"net/http"
	"testing"
)

func packetValidatorSpec() map[string]interface{} {
	return map[string]interface{}{
		"name":        "PacketValidator",
		"initial":     "AwaitHeader",
		"determinism": "loose",
		"states": map[string]interface{}{
			"AwaitHeader":   map[string]interface{}{"terminal": false},
			"AwaitLength":   map[string]interface{}{"terminal": false},
			"AwaitPayload":  map[string]interface{}{"terminal": false},
			"AwaitChecksum": map[string]interface{}{"terminal": false},
			"Accepted":      map[string]interface{}{"terminal": true},
			"Rejected":      map[string]interface{}{"terminal": true},
		},
		"variables": map[string]interface{}{
			"@expected": map[string]interface{}{"type": "int", "default": 0},
			"@received": map[string]interface{}{"type": "int", "default": 0},
		},
		"transitions": []map[string]interface{}{
			// Header: version must be present and == 1.
			{"from": "AwaitHeader", "input": "header", "to": "AwaitLength",
				"guard": "payload.version IS NOT NULL AND payload.version = 1"},
			{"from": "AwaitHeader", "input": "header", "to": "Rejected",
				"guard": "payload.version IS NOT NULL AND payload.version != 1"},
			{"from": "AwaitHeader", "input": "header", "to": "Rejected",
				"guard": "payload.version IS NULL"},

			// Length: present and 0 < len <= 1024; capture into @expected.
			{"from": "AwaitLength", "input": "length", "to": "AwaitPayload",
				"guard": "payload.len IS NOT NULL AND payload.len > 0 AND payload.len <= 1024",
				"set":   map[string]string{"@expected": "payload.len"}},
			{"from": "AwaitLength", "input": "length", "to": "Rejected",
				"guard": "payload.len IS NOT NULL AND (payload.len <= 0 OR payload.len > 1024)"},
			{"from": "AwaitLength", "input": "length", "to": "Rejected",
				"guard": "payload.len IS NULL"},

			// Payload: accumulate chunk sizes (self-loop), then end.
			{"from": "AwaitPayload", "input": "chunk", "to": "AwaitPayload",
				"guard": "payload.size IS NOT NULL AND payload.size > 0",
				"set":   map[string]string{"@received": "@received + payload.size"}},
			{"from": "AwaitPayload", "input": "chunk", "to": "Rejected",
				"guard": "payload.size IS NULL OR payload.size <= 0"},
			{"from": "AwaitPayload", "input": "end", "to": "AwaitChecksum",
				"guard": "@received = @expected"},
			{"from": "AwaitPayload", "input": "end", "to": "Rejected",
				"guard": "@received != @expected"},

			// Checksum: crc present and equal to the accumulated total.
			{"from": "AwaitChecksum", "input": "checksum", "to": "Accepted",
				"guard": "payload.crc IS NOT NULL AND payload.crc = @received"},
			{"from": "AwaitChecksum", "input": "checksum", "to": "Rejected",
				"guard": "payload.crc IS NOT NULL AND payload.crc != @received"},
			{"from": "AwaitChecksum", "input": "checksum", "to": "Rejected",
				"guard": "payload.crc IS NULL"},
		},
	}
}

func newPacketMachine(t *testing.T, env *stdTestServer) int64 {
	t.Helper()
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, ""), packetValidatorSpec())
	if status != http.StatusCreated {
		t.Fatalf("create PacketValidator def: want 201, got %d: %v", status, resp)
	}
	defID := int64(resp["id"].(float64))
	_, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	return int64(mResp["id"].(float64))
}

// ─── Definition validity ──────────────────────────────────────────────────────

func TestPacket_DefinitionValidatesButReportsNonDeterministic(t *testing.T) {
	env := newV2Server(t)
	status, resp := doJSONRequest(t, "POST", fsmDefURL(env, "/validate"), packetValidatorSpec())
	if status != http.StatusOK {
		t.Fatalf("validate: want 200, got %d: %v", status, resp)
	}
	if resp["valid"] != true {
		t.Fatalf("PacketValidator should be valid: %v", resp)
	}
	analysis, _ := resp["analysis"].(map[string]interface{})
	// Structurally non-deterministic (multiple same-input edges) but valid:
	// the determinism flag is a structural signal, not a behavioural guarantee
	// when guards disambiguate.
	if analysis["deterministic"] != false {
		t.Errorf("expected deterministic=false (guard-disambiguated edges), got %v", analysis["deterministic"])
	}
}

// ─── Happy path: a fully valid packet is Accepted ─────────────────────────────

func TestPacket_ValidPacketAccepted(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)

	steps := []struct {
		input   string
		payload map[string]interface{}
		want    string
	}{
		{"header", map[string]interface{}{"version": 1}, "AwaitLength"},
		{"length", map[string]interface{}{"len": 30}, "AwaitPayload"},
		{"chunk", map[string]interface{}{"size": 10}, "AwaitPayload"},
		{"chunk", map[string]interface{}{"size": 20}, "AwaitPayload"},
		{"end", nil, "AwaitChecksum"},
		{"checksum", map[string]interface{}{"crc": 30}, "Accepted"},
	}
	for i, s := range steps {
		st, resp := walk(t, env, id, s.input, s.payload)
		if st != http.StatusOK {
			t.Fatalf("step %d (%s): want 200, got %d: %v", i, s.input, st, resp)
		}
		if resp["current"] != s.want {
			t.Fatalf("step %d (%s): want %s, got %v", i, s.input, s.want, resp["current"])
		}
	}
	// Final state is the Accepted terminal.
	_, stateResp := doJSONRequest(t, "GET", fsmMachineURL(env, fmt.Sprintf("/%d/state", id)), nil)
	if stateResp["state"] != "Accepted" || stateResp["terminal"] != true {
		t.Errorf("final: want Accepted/terminal, got %v/%v", stateResp["state"], stateResp["terminal"])
	}
}

// ─── Modelled rejection: present-but-invalid field routes to Rejected ─────────

func TestPacket_BadVersionRejected(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)
	st, resp := walk(t, env, id, "header", map[string]interface{}{"version": 2})
	if st != http.StatusOK {
		t.Fatalf("bad version walk: want 200 (modelled reject), got %d: %v", st, resp)
	}
	if resp["current"] != "Rejected" || resp["terminal"] != true {
		t.Errorf("bad version: want Rejected/terminal, got %v/%v", resp["current"], resp["terminal"])
	}
}

func TestPacket_LengthOutOfBoundsRejected(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)
	walk(t, env, id, "header", map[string]interface{}{"version": 1})
	st, resp := walk(t, env, id, "length", map[string]interface{}{"len": 2048})
	if st != http.StatusOK || resp["current"] != "Rejected" {
		t.Fatalf("oversize length: want 200 -> Rejected, got %d -> %v", st, resp["current"])
	}
}

func TestPacket_LengthMismatchRejected(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)
	walk(t, env, id, "header", map[string]interface{}{"version": 1})
	walk(t, env, id, "length", map[string]interface{}{"len": 30})
	walk(t, env, id, "chunk", map[string]interface{}{"size": 10}) // received=10, expected=30
	// end with received(10) != expected(30) -> Rejected.
	st, resp := walk(t, env, id, "end", nil)
	if st != http.StatusOK || resp["current"] != "Rejected" {
		t.Fatalf("length mismatch: want 200 -> Rejected, got %d -> %v", st, resp["current"])
	}
}

func TestPacket_BadChecksumRejected(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)
	walk(t, env, id, "header", map[string]interface{}{"version": 1})
	walk(t, env, id, "length", map[string]interface{}{"len": 30})
	walk(t, env, id, "chunk", map[string]interface{}{"size": 30})
	walk(t, env, id, "end", nil)
	st, resp := walk(t, env, id, "checksum", map[string]interface{}{"crc": 999})
	if st != http.StatusOK || resp["current"] != "Rejected" {
		t.Fatalf("bad checksum: want 200 -> Rejected, got %d -> %v", st, resp["current"])
	}
}

// ─── Missing-field rejection via IS NULL edge (not NULL-collapse / FSM003) ─────

func TestPacket_MissingVersionRejectedCleanly(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)
	// header with no version field at all -> IS NULL edge -> Rejected,
	// NOT an XOLU-FSM003 fall-through.
	st, resp := walk(t, env, id, "header", map[string]interface{}{})
	if st != http.StatusOK {
		t.Fatalf("missing version: want 200 (clean modelled reject), got %d: %v", st, resp)
	}
	if resp["current"] != "Rejected" {
		t.Errorf("missing version: want Rejected, got %v", resp["current"])
	}
}

func TestPacket_MissingChecksumRejectedCleanly(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)
	walk(t, env, id, "header", map[string]interface{}{"version": 1})
	walk(t, env, id, "length", map[string]interface{}{"len": 15})
	walk(t, env, id, "chunk", map[string]interface{}{"size": 15})
	walk(t, env, id, "end", nil)
	st, resp := walk(t, env, id, "checksum", map[string]interface{}{}) // no crc
	if st != http.StatusOK || resp["current"] != "Rejected" {
		t.Fatalf("missing checksum: want 200 -> Rejected, got %d -> %v", st, resp["current"])
	}
}

// ─── Structural rejection: wrong input for the current state -> XOLU-FSM003 ────

func TestPacket_OutOfSequenceInputIsStructuralError(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)
	// A checksum while still in AwaitHeader: no (state,input) edge exists.
	st, resp := walk(t, env, id, "checksum", map[string]interface{}{"crc": 1})
	if st != http.StatusConflict {
		t.Fatalf("out-of-sequence: want 409, got %d: %v", st, resp)
	}
	if errCode(resp) != "XOLU-FSM003" {
		t.Errorf("out-of-sequence: want XOLU-FSM003, got %v", resp["error"])
	}
}

// ─── Multi-chunk accumulation reaches Accepted ────────────────────────────────

func TestPacket_MultiChunkAccumulation(t *testing.T) {
	env := newV2Server(t)
	id := newPacketMachine(t, env)
	walk(t, env, id, "header", map[string]interface{}{"version": 1})
	walk(t, env, id, "length", map[string]interface{}{"len": 100})
	// five chunks of 20 = 100
	for i := 0; i < 5; i++ {
		st, resp := walk(t, env, id, "chunk", map[string]interface{}{"size": 20})
		if st != http.StatusOK || resp["current"] != "AwaitPayload" {
			t.Fatalf("chunk %d: want stay AwaitPayload, got %d -> %v", i, st, resp["current"])
		}
	}
	st, resp := walk(t, env, id, "end", nil)
	if st != http.StatusOK || resp["current"] != "AwaitChecksum" {
		t.Fatalf("end after 100 received: want AwaitChecksum, got %d -> %v", st, resp["current"])
	}
	st, resp = walk(t, env, id, "checksum", map[string]interface{}{"crc": 100})
	if st != http.StatusOK || resp["current"] != "Accepted" {
		t.Fatalf("final checksum: want Accepted, got %d -> %v", st, resp["current"])
	}
}
