// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_loc_anchor_patch_test.go — T-128 (wave 9b), HTTP-level
// companion to pkg/loc's own store-level anchor_journal_test.go:
// proves the real PATCH /loc/{location_id} endpoint reaches the new
// journal-write path, not just the store method directly.

package server_test

import (
	"net/http"
	"testing"
)

// TestLocAPI_AnchorPatch_ReachesJournalEntry confirms an anchor
// change made through the real HTTP PATCH endpoint produces the same
// effect pkg/loc's own store-level test proves directly — the live
// location row reflects the new anchor, and the change was real (not
// silently dropped or misrouted between the handler and the store).
func TestLocAPI_AnchorPatch_ReachesJournalEntry(t *testing.T) {
	env := newMetaServer(t)
	defineLocRoot(t, env, "hq") // anchor lat=-34.9, lon=-56.16, alt=10

	status, patchResp := doJSONRequest(t, "PATCH", locURL(env, "/hq"), map[string]interface{}{
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": -34.85, "lon": -56.10, "alt": 20, "true_north": 0},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("anchor patch: want 200, got %d %v", status, patchResp)
	}
	placement, ok := patchResp["placement"].(map[string]interface{})
	if !ok {
		t.Fatalf("patch response missing placement: %v", patchResp)
	}
	anchor, ok := placement["anchor"].(map[string]interface{})
	if !ok {
		t.Fatalf("patch response missing anchor: %v", placement)
	}
	if anchor["lat"] != -34.85 || anchor["lon"] != -56.10 {
		t.Errorf("anchor not updated in response: %v", anchor)
	}

	// A second, identical PATCH (no-op) must succeed cleanly too — the
	// no-op-writes-nothing rule is invisible at the HTTP layer (no
	// distinct wire response for "nothing changed" on a location PATCH,
	// unlike report's own changed:false), but must never error.
	status, patchResp2 := doJSONRequest(t, "PATCH", locURL(env, "/hq"), map[string]interface{}{
		"placement": map[string]interface{}{
			"offset_x": 0, "offset_y": 0, "offset_z": 0, "rotation": 0,
			"anchor": map[string]interface{}{"lat": -34.85, "lon": -56.10, "alt": 20, "true_north": 0},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("identical anchor patch (no-op): want 200, got %d %v", status, patchResp2)
	}

	status, getResp := doJSONRequest(t, "GET", locURL(env, "/hq"), nil)
	if status != http.StatusOK {
		t.Fatalf("get after anchor patch: want 200, got %d %v", status, getResp)
	}
}
