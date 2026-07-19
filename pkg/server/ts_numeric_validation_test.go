// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// D-006: request integer fields (num_field, dims) were narrowed int -> uint8
// BEFORE their range was validated. An out-of-range request whose low byte falls
// in the valid window (e.g. num_field 256->0, 262->6; dims 257->1 .. 261->5) was
// silently accepted as a different, in-range value rather than rejected. These
// values slip PAST the downstream uint8 guards (>6 / [1,5]); a plain
// out-of-range value like 9 is already caught because 9 survives narrowing.
//
// After the fix, the handler validates the raw int against its range before the
// uint8 conversion, so the aliasing values are rejected with a 400. These tests
// drive the real handlers (not an inline uint8 recomputation) and are the
// tripwire for that guard.

// num_field aliasing values must be rejected by HandleTSAggregate with a 400.
func TestNumField_AliasingValues_Rejected(t *testing.T) {
	env := setupTSServer(t, nil)
	env.registerTenant("acme")
	env.provision("acme")
	env.defineTimeline("acme", map[string]interface{}{"id": 1, "dims": 1, "name": "sensors"})

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	from := base.Add(-time.Hour).Format(time.RFC3339)
	to := base.Add(time.Hour).Format(time.RFC3339)

	// 256->0, 262->6, 513->1 : all land in [0,6] after narrowing.
	for _, nf := range []int{256, 262, 513} {
		status, resp := doJSONRequest(t, "POST", env.tsURL("acme", "/aggregate"),
			map[string]interface{}{
				"timeline": 1, "dims": []interface{}{0},
				"from": from, "to": to,
				"interval": "1h", "function": "avg", "num_field": nf,
			})
		if status != http.StatusBadRequest {
			t.Errorf("num_field=%d (aliases to uint8=%d): want 400, got %d: %v",
				nf, uint8(nf), status, resp)
		}
	}
}

// dims aliasing values must be rejected by HandleTSDefineTimeline with a 400.
func TestDims_AliasingValues_Rejected(t *testing.T) {
	env := setupTSServer(t, nil)
	env.registerTenant("acme")
	env.provision("acme")

	// 257->1 .. 261->5 : all land in [1,5] after narrowing.
	id := 100
	for _, d := range []int{257, 258, 259, 260, 261} {
		status, resp := env.do("POST", env.tsURL("acme", "/tl/def"),
			map[string]interface{}{"id": id, "dims": d, "name": fmt.Sprintf("tl_%d", d)})
		if status != http.StatusBadRequest {
			t.Errorf("dims=%d (aliases to uint8=%d): want 400, got %d: %v",
				d, uint8(d), status, resp)
		}
		id++
	}
}

// Control: legitimate in-range values are still accepted, so the guard does not
// over-reject. dims=1..5 define cleanly; num_field 0..6 aggregates with 200.
func TestNumField_Dims_ValidRange_Accepted(t *testing.T) {
	env := setupTSServer(t, nil)
	env.registerTenant("acme")
	env.provision("acme")

	id := 200
	for _, d := range []int{1, 2, 3, 4, 5} {
		status, resp := env.do("POST", env.tsURL("acme", "/tl/def"),
			map[string]interface{}{"id": id, "dims": d, "name": fmt.Sprintf("ok_%d", d)})
		if status != http.StatusCreated {
			t.Errorf("dims=%d: want 201, got %d: %v", d, status, resp)
		}
		id++
	}

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	from := base.Add(-time.Hour).Format(time.RFC3339)
	to := base.Add(time.Hour).Format(time.RFC3339)
	for _, nf := range []int{0, 3, 6} {
		status, resp := doJSONRequest(t, "POST", env.tsURL("acme", "/aggregate"),
			map[string]interface{}{
				"timeline": 200, "dims": []interface{}{0},
				"from": from, "to": to,
				"interval": "1h", "function": "avg", "num_field": nf,
			})
		if status != http.StatusOK {
			t.Errorf("num_field=%d: want 200, got %d: %v", nf, status, resp)
		}
	}
}
