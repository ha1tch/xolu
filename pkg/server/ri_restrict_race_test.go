// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// TestRIRestrict_Race probes the check-then-act window that @R §5
// identifies: restrict enforcement reads "no inbound refs", and a
// concurrent write creating a referrer races that read. The proposal's
// safety-by-construction argument requires the enforcement read to run
// INSIDE the delete's transaction; stage 2's enforcement does NOT yet do
// that (the graph inbound-edge query and the SQL delete are separate
// operations). The handler-level pre-check still has that shape, but the
// authoritative store-level check (DeleteWithRestrict) runs inside the
// delete's transaction, closing the window; this harness asserts that.
//
// Registered as dormant guard G-12. Like the cal race guards, it only
// manifests under true multi-core parallelism; a single-core pass is not
// evidence. Since 2026-07-21 this test ASSERTS the invariant: the window
// was closed by DeleteWithRestrict's in-transaction referrer check
// (@C04a), so after concurrent delete-target + create-referrer the store
// must never hold a committed referrer pointing at a deleted target. A
// single-core pass remains vacuous; the dormant-guard entry owes a
// real-silicon multi-core run before the closure counts as verified.
//
// Invocation (real silicon):
//
//	GOMAXPROCS=<cores> go test ./pkg/server/ -run TestRIRestrict_Race -count=20 -race
func TestRIRestrict_Race(t *testing.T) {
	const trials = 8

	dangling := 0
	for trial := 0; trial < trials; trial++ {
		if raceProducedDangling(t, trial) {
			dangling++
		}
	}

	// ASSERTION (window closed 2026-07-21 by DeleteWithRestrict's
	// in-transaction check, ahead of the stage-3 schedule): after
	// concurrent delete-target + create-referrer, the store must never
	// hold a committed referrer pointing at a deleted target. On single-
	// core hardware this cannot open the window and passes vacuously —
	// the dormant-guard entry still owes a real-silicon multi-core run
	// (G-12) before the closure counts as verified.
	if dangling > 0 {
		t.Fatalf("RI restrict race: %d/%d trials left a dangling reference — "+
			"the in-transaction restrict check (DeleteWithRestrict, @C04a) failed to close the window",
			dangling, trials)
	}
	t.Logf("RI restrict race: 0/%d trials left a dangling reference (window closed; "+
		"single-core runs pass vacuously — multi-core run still owed per G-12)", trials)
}

// raceProducedDangling runs one trial: concurrently delete a user and
// create a post referencing it, then check whether the store ended with
// a post pointing at a now-deleted user. Returns true if the dangling
// reference occurred.
func raceProducedDangling(t *testing.T, trial int) bool {
	t.Helper()
	ts := setupTestServer(t)
	defer ts.cleanup()

	// Schemas: users, and posts.author_id →restrict→ users.
	ts.doRequest(http.MethodPost, "/api/v1/schema/users", map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
	})
	ts.doRequest(http.MethodPost, "/api/v1/schema/posts", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"author_id": map[string]interface{}{
				"type":   "object",
				"format": "ref",
				"x-ref":  map[string]interface{}{"entity": "users", "on_delete": "restrict"},
			},
		},
	})

	// A user to contend over.
	_, body := ts.doRequest(http.MethodPost, "/api/v1/users", map[string]interface{}{"name": fmt.Sprintf("u%d", trial)})
	var u struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decode user: %v", err)
	}

	// Race: delete the user and create a referring post simultaneously.
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(2)

	var postCreated bool
	var postID int

	go func() {
		defer done.Done()
		start.Wait()
		ts.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", u.ID), nil)
	}()
	go func() {
		defer done.Done()
		start.Wait()
		resp, b := ts.doRequest(http.MethodPost, "/api/v1/posts", map[string]interface{}{
			"author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": u.ID},
		})
		if resp.StatusCode == http.StatusCreated {
			postCreated = true
			var p struct {
				ID int `json:"id"`
			}
			_ = json.Unmarshal(b, &p)
			postID = p.ID
		}
	}()
	start.Done()
	done.Wait()

	// Dangling iff the post was created AND the user no longer exists.
	if !postCreated {
		return false
	}
	_ = postID
	resp, _ := ts.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/users/%d", u.ID), nil)
	userGone := resp.StatusCode == http.StatusNotFound
	return userGone
}
