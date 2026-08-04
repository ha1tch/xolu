// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_loc_fence_subject_test.go — T-127 (wave 9b): standalone fence
// identity resolved via pkg/storage's engine-inert ParseMetaSubject,
// the same validator /meta's own handlers use. Covers the invariant
// that "kind:key" shorthand and a REF-shaped object are equivalent,
// live XOLU-LOC005 on a malformed subject, and GET/DELETE resolving
// (kind,key) the same way attach composes it.

package server_test

import (
	"net/http"
	"testing"
)

// TestLocAPI_FenceSubject_ShorthandAndREFAreEquivalent proves the
// invariant named in parseFenceSubject's own doc comment: the
// "kind:key" shorthand and a {"type":"REF",...} object must resolve
// to the identical fence_id, checked by attaching with one form and
// retrieving with the other's own composed (kind,key) path.
func TestLocAPI_FenceSubject_ShorthandAndREFAreEquivalent(t *testing.T) {
	env := newMetaServer(t)

	// Attach using the REF-shaped object.
	status, attachResp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": map[string]interface{}{"type": "REF", "entity": "vehicles", "id": 4471},
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach via REF object: want 201, got %d %v", status, attachResp)
	}
	if attachResp["fence_id"] != "vehicles:4471" {
		t.Fatalf("REF object must canonicalise to \"vehicles:4471\", got %v", attachResp["fence_id"])
	}

	// Retrieve via the two-segment path the shorthand form's own
	// (kind,key) split would produce — proving both forms address the
	// same fence, not just that each individually decodes.
	status, getResp := doJSONRequest(t, "GET", locURL(env, "/fences/vehicles/4471"), nil)
	if status != http.StatusOK {
		t.Fatalf("get via composed (kind,key): want 200, got %d %v", status, getResp)
	}
	if getResp["fence_id"] != "vehicles:4471" {
		t.Errorf("want fence_id \"vehicles:4471\", got %v", getResp["fence_id"])
	}

	// Now attach a second, distinct fence using the shorthand string
	// form directly, and confirm it composes identically to what the
	// REF form above already produced for a different id.
	status, attachResp2 := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "vehicles:4472",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 10, "lon": 10}, "radius_m": 50.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach via shorthand string: want 201, got %d %v", status, attachResp2)
	}
	if attachResp2["fence_id"] != "vehicles:4472" {
		t.Fatalf("shorthand string must canonicalise to \"vehicles:4472\", got %v", attachResp2["fence_id"])
	}

	status, getResp2 := doJSONRequest(t, "GET", locURL(env, "/fences/vehicles/4472"), nil)
	if status != http.StatusOK {
		t.Fatalf("get second fence via composed (kind,key): want 200, got %d %v", status, getResp2)
	}
	if getResp2["fence_id"] != "vehicles:4472" {
		t.Errorf("want fence_id \"vehicles:4472\", got %v", getResp2["fence_id"])
	}
}

// TestLocAPI_FenceSubject_Malformed_ReturnsXOLULOC005 covers every
// shape parseFenceSubject/composedFenceSubjectID must refuse: no
// colon in the shorthand, a non-numeric key for an (undotted) entity
// kind, an unrecognised dotted namespaced kind, and a subject.type
// other than "REF" — none of these should ever reach DefFence at
// all, and none should surface as a 500.
func TestLocAPI_FenceSubject_Malformed_ReturnsXOLULOC005(t *testing.T) {
	cases := []struct {
		name    string
		subject interface{}
	}{
		{"no colon in shorthand", "not-a-valid-shorthand"},
		{"non-numeric key for entity kind", "vehicles:not-a-number"},
		{"unrecognised dotted kind", "bogus.kind:something"},
		{"wrong subject.type", map[string]interface{}{"type": "NOT_REF", "entity": "vehicles", "id": 1}},
		{"empty entity in REF object", map[string]interface{}{"type": "REF", "entity": "", "id": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newMetaServer(t)
			status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
				"subject": tc.subject,
				"geometry": map[string]interface{}{
					"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 100.0,
				},
			})
			if status != http.StatusNotFound {
				t.Fatalf("%s: want 404 (XOLU-LOC005), got %d %v", tc.name, status, resp)
			}
			errObj, ok := resp["error"].(map[string]interface{})
			if !ok || errObj["code"] != "XOLU-LOC005" {
				t.Errorf("%s: want XOLU-LOC005, got %v", tc.name, resp)
			}
		})
	}
}

// TestLocAPI_FenceGet_UnknownComposedSubject_ReturnsXOLULOC004 checks
// the distinct failure mode from the malformed-shape tests above: a
// well-formed (kind,key) that parses cleanly but was never attached —
// this is XOLU-LOC004 (not found), not XOLU-LOC005 (doesn't resolve
// in shape), since composedFenceSubjectID succeeds and the 404 comes
// from the store's own no-such-row path.
func TestLocAPI_FenceGet_UnknownComposedSubject_ReturnsXOLULOC004(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "GET", locURL(env, "/fences/vehicles/999999"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("well-formed but never-attached subject: want 404, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-LOC004" {
		t.Errorf("want XOLU-LOC004 (not XOLU-LOC005 — the shape was valid), got %v", resp)
	}
}

// TestLocAPI_FenceDelete_ComposedSubject_RoundTrip proves DELETE
// resolves the same (kind,key) composition GET and attach do, and
// that a second delete of the same address cleanly 404s rather than
// silently succeeding twice.
func TestLocAPI_FenceDelete_ComposedSubject_RoundTrip(t *testing.T) {
	env := newMetaServer(t)
	status, _ := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject": "vehicles:5001",
		"geometry": map[string]interface{}{
			"type": "circle", "center": map[string]interface{}{"lat": 0, "lon": 0}, "radius_m": 10.0,
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("attach: want 201, got %d", status)
	}

	status, _ = doJSONRequest(t, "DELETE", locURL(env, "/fences/vehicles/5001"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("first delete: want 204, got %d", status)
	}

	status, resp := doJSONRequest(t, "DELETE", locURL(env, "/fences/vehicles/5001"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("second delete of the same address: want 404, got %d %v", status, resp)
	}
}

// TestLocAPI_FenceSubject_EmptyStringTreatedAsAbsent proves an
// explicit `"subject": ""` behaves identically to omitting the field
// — both fall through to aligned_to for a tree-aligned fence, per
// parseFenceSubject's own doc comment, rather than surfacing as a
// spurious XOLU-LOC005.
func TestLocAPI_FenceSubject_EmptyStringTreatedAsAbsent(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "site2")
	defineLocLeaf(t, env, "site2/yard", "site2", nil)

	status, resp := doJSONRequest(t, "POST", locURL(env, "/fences/attach"), map[string]interface{}{
		"subject":    "",
		"aligned_to": "site2/yard",
		"geometry": map[string]interface{}{
			"type":        "Polygon",
			"coordinates": [][][2]float64{{{-56.17, -34.91}, {-56.15, -34.91}, {-56.15, -34.89}, {-56.17, -34.89}, {-56.17, -34.91}}},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("empty-string subject on a tree-aligned attach: want 201, got %d %v", status, resp)
	}
	if resp["fence_id"] != "site2/yard" {
		t.Errorf("want fence_id \"site2/yard\" (aligned_to fallback), got %v", resp["fence_id"])
	}
}
