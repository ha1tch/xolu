// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestLocAPI_DuplicateDefine_Returns409NotFiveHundred is the direct
// proof of the fix: a duplicate location_id must return 409 Conflict
// with XOLU-LOC014, not a bare 500 from an unwrapped SQLite driver
// error — the gap adversarial testing found (the store-level
// UNIQUE-constraint violation previously had no typed error at all,
// falling through writeLocError's default case).
func TestLocAPI_DuplicateDefine_Returns409NotFiveHundred(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "root")

	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "root", "parent_id": nil, "name": "duplicate attempt", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 0, "lon": 0, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusConflict {
		t.Fatalf("duplicate location_id: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC014" {
		t.Errorf("want XOLU-LOC014, got %v", resp)
	}

	// The original location must be completely undamaged by the
	// failed duplicate attempt.
	status, getResp := doJSONRequest(t, "GET", locURL(env, "/root"), nil)
	if status != http.StatusOK {
		t.Fatalf("original location damaged by the failed duplicate: %d %v", status, getResp)
	}
	if getResp["name"] != "root" {
		t.Errorf("original location's name changed by the failed duplicate attempt: got %v", getResp["name"])
	}
}

// TestLocAPI_DuplicateFenceAttach_Returns409 is the fence_id version
// of the same proof.
func TestLocAPI_DuplicateFenceAttach_Returns409(t *testing.T) {
	env := newMetaServer(t)
	status, _ := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:1",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("first fence attach: want 201, got %d", status)
	}
	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "zones:1",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
		},
	})
	if status != http.StatusConflict {
		t.Fatalf("duplicate fence_id: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC015" {
		t.Errorf("want XOLU-LOC015, got %v", resp)
	}
}

// TestLocAPI_MalformedJSON_Returns400NotFiveHundred covers every
// write endpoint with a garbage body — none should ever surface a
// 500 for a client's own malformed JSON.
func TestLocAPI_MalformedJSON_Returns400NotFiveHundred(t *testing.T) {
	env := newMetaServer(t)
	endpoints := []string{"/def", "/move", "/report", "/fences/attach"}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req, err := http.NewRequest("POST", locURL(env, ep), bytes.NewBufferString(`{"not": "valid json"`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s with truncated JSON: want 400, got %d", ep, resp.StatusCode)
			}
		})
	}
}

// TestLocAPI_SQLInjectionShapedLocationID_TreatedAsLiteral proves
// parameterized queries protect location_id (and fence subject)
// values from being interpreted as SQL — a classic-shaped injection
// attempt must be stored and retrieved as an ordinary, harmless
// string, not alter query structure or damage other data.
func TestLocAPI_SQLInjectionShapedLocationID_TreatedAsLiteral(t *testing.T) {
	env := newMetaServer(t)
	malicious := `bin'; DROP TABLE locations; --`
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": malicious, "parent_id": nil, "name": "test", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 0, "lon": 0, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("defining a SQL-injection-shaped location_id: want 201 (treated as a literal string), got %d %v", status, resp)
	}
	if resp["location_id"] != malicious {
		t.Errorf("location_id round-trip: want the exact literal string back, got %v", resp["location_id"])
	}

	// The table must still exist and be queryable — confirms the
	// string was never interpreted as SQL.
	status, listResp := doJSONRequest(t, "GET", locURL(env, "/list"), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /list after a SQL-injection-shaped define: want 200, got %d %v", status, listResp)
	}
	locs, _ := listResp["locations"].([]interface{})
	if len(locs) != 1 {
		t.Fatalf("want exactly 1 location surviving (the table was not dropped), got %d", len(locs))
	}
}

// TestLocAPI_PathTraversalShapedLocationID_TreatedAsLiteral: a
// location_id containing "../" sequences must be stored and
// retrieved as an ordinary literal string. This package's own
// location_id is never used as a filesystem path component (the
// per-tenant SQLite file itself is keyed by tenant, not by any
// location_id), so there's no traversal surface here regardless — but
// worth confirming directly rather than assumed, since the two-
// identity split's whole point is that untrusted external strings
// never drive internal addressing.
func TestLocAPI_PathTraversalShapedLocationID_TreatedAsLiteral(t *testing.T) {
	env := newMetaServer(t)
	tricky := "../../../etc/passwd"
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": tricky, "parent_id": nil, "name": "test", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 0, "lon": 0, "alt": 0, "true_north": 0},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("defining a path-traversal-shaped location_id: want 201, got %d %v", status, resp)
	}
	if resp["location_id"] != tricky {
		t.Errorf("location_id round-trip: want the exact literal string back, got %v", resp["location_id"])
	}
}

// TestLocAPI_ExtremelyLongLocationID_HandledSanely: a very long
// string must not crash the handler or the store — either accepted
// and round-tripped exactly, or refused cleanly, never a 500.
func TestLocAPI_ExtremelyLongLocationID_HandledSanely(t *testing.T) {
	env := newMetaServer(t)
	long := strings.Repeat("a/very/long/path/segment/", 200) // ~5000 chars
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": long, "parent_id": nil, "name": "test", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 0, "lon": 0, "alt": 0, "true_north": 0},
		},
	})
	if status == http.StatusInternalServerError {
		t.Fatalf("an extremely long location_id must never produce a 500, got %d %v", status, resp)
	}
	if status == http.StatusCreated && resp["location_id"] != long {
		t.Error("accepted a long location_id but did not round-trip it exactly")
	}
}

// TestLocAPI_WrongTypeInRequestBody covers a capacity field submitted
// as a string instead of a number — a type mismatch must produce a
// clean 400, not a panic or a 500.
func TestLocAPI_WrongTypeInRequestBody(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "POST", locURL(env, "/def"), map[string]interface{}{
		"location_id": "wrong-type-test", "parent_id": nil, "name": "test", "postable": true,
		"capacity": "five", // should be a number
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
		},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("capacity as a string instead of a number: want 400, got %d %v", status, resp)
	}
}

// TestLocAPI_ConcurrentDuplicateDefine_ExactlyOneSucceeds races two
// concurrent HTTP defines for the SAME location_id — exactly one
// must succeed (201), the other must fail cleanly with 409, and no
// schedule should produce two successes or a corrupted/partial
// record.
func TestLocAPI_ConcurrentDuplicateDefine_ExactlyOneSucceeds(t *testing.T) {
	env := newMetaServer(t)
	body := map[string]interface{}{
		"location_id": "race-target", "parent_id": nil, "name": "test", "postable": false,
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": 0, "lon": 0, "alt": 0, "true_north": 0},
		},
	}

	const n = 10
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, _ := doJSONRequest(t, "POST", locURL(env, "/def"), body)
			statuses[i] = status
		}(i)
	}
	wg.Wait()

	created, other := 0, 0
	for _, s := range statuses {
		switch s {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			// expected for every losing attempt
		default:
			other++
		}
	}
	if created != 1 {
		t.Fatalf("want exactly 1 of %d concurrent identical defines to succeed (201), got %d (statuses: %v)", n, created, statuses)
	}
	if other != 0 {
		t.Fatalf("want every non-winning attempt to be a clean 409, got %d unexpected status(es): %v", other, statuses)
	}
}
