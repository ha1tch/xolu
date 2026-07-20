// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// T-41 (@R08 stage 1) regression:
//
// The pre-fix `cascadeDelete` never discovered referents — it seeded
// the queue with only the target and reported cascaded_deletes as if
// a cascade had run. The test that would have caught it is the one
// this file now provides: create A, then B and C referring to A, then
// DELETE ?cascade=true on A, then verify B and C are gone.
//
// Any implementation that returns "1 deletion" for the whole call is
// the stub reborn.

// TestCascadeDelete_ActuallyCascadesReferents creates a small ref chain
// and verifies cascade deletes referents too, not only the target.
//
// Graph:  parent (id 1)
//           ↑           ↑
//         childA       childB
//
// Expected on `DELETE parent/1?cascade=true`:
//   - parent, childA, childB all gone from the store
//   - response reports 3 cascaded deletions
func TestCascadeDelete_ActuallyCascadesReferents(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Flip CascadingDelete on for this test — setupTestServer defaults it off.
	ts.cfg.CascadingDelete = true

	// Create the parent entity with a schema-free JSON payload.
	parentBody := map[string]interface{}{"name": "root"}
	resp, body := ts.doRequest(http.MethodPost, "/api/v1/parents", parentBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create parent: got %d, body=%s", resp.StatusCode, string(body))
	}
	var parentResp struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &parentResp); err != nil {
		t.Fatalf("decode parent response: %v", err)
	}
	parentID := parentResp.ID
	if parentID == 0 {
		t.Fatalf("parent id not returned: body=%s", string(body))
	}

	// Create two children referring to the parent.
	// Using the $ref shape the graph maintenance already understands.
	makeChild := func(name string) int {
		body := map[string]interface{}{
			"name": name,
			"parent": map[string]interface{}{
				"type":   "REF",
				"entity": "parents",
				"id":     parentID,
			},
		}
		resp, respBody := ts.doRequest(http.MethodPost, "/api/v1/children", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create child %s: got %d, body=%s", name, resp.StatusCode, string(respBody))
		}
		var r struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(respBody, &r); err != nil {
			t.Fatalf("decode child %s response: %v", name, err)
		}
		return r.ID
	}
	childA := makeChild("A")
	childB := makeChild("B")

	// Cascade-delete the parent.
	url := fmt.Sprintf("/api/v1/parents/%d", parentID)
	resp, body = ts.doRequest(http.MethodDelete, url, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cascade delete: got %d, body=%s", resp.StatusCode, string(body))
	}

	// The response should report a cascade count of at least 3
	// (parent + two children). We assert >=3 rather than ==3 to allow
	// legitimate future ordering/duplicate-guard behaviour, but the
	// pre-fix stub reports 1 here — that number is what this test refuses.
	var delResp struct {
		CascadedDeletes []string `json:"cascaded_deletes"`
	}
	if err := json.Unmarshal(body, &delResp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if len(delResp.CascadedDeletes) < 3 {
		t.Errorf("T-41 stub: expected >=3 cascaded deletes (parent + 2 children), got %d: %v",
			len(delResp.CascadedDeletes), delResp.CascadedDeletes)
	}

	// Verify children are actually gone — the response count is one
	// thing, the store's contents are the ground truth.
	for _, ch := range []struct {
		name string
		id   int
	}{
		{"parent", parentID},
		{"childA", childA},
		{"childB", childB},
	} {
		entityName := map[string]string{"parent": "parents", "childA": "children", "childB": "children"}[ch.name]
		resp, _ := ts.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/%s/%d", entityName, ch.id), nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("T-41: %s (id=%d) should be gone after cascade, GET returned %d",
				ch.name, ch.id, resp.StatusCode)
		}
	}
}
