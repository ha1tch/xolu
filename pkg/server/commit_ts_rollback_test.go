// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// commit_ts_rollback_test.go
//
// Tests for the failure paths in handleCommit's timeseries write ordering:
//
//   SQLite fails after Pebble succeeds → DeleteKeys tombstones the written
//   events before the error is returned to the caller.
//
// Strategy: drive SQLite failure via a CAS version conflict, which is a
// deterministic, always-available failure path that does not require mocking
// the storage layer.  The test:
//
//  1. Advances the entity state once without TS to establish version=1.
//  2. Issues a /commit carrying TS events and version=0 (stale) so SQLite
//     returns ErrConflict.
//  3. Pebble write (step 1 of handleCommit) succeeds.
//  4. SQLite commit (step 2) fails → DeleteKeys is called.
//  5. Asserts the TS event is NOT readable from Pebble.
//  6. Asserts the entity state is unchanged (still at the pre-stale version).
//
// The XOLU-CM016 double-failure path (DeleteKeys also fails) requires injecting
// a failing Store into the unexported tsManager field, which is typed as
// *timeseries.DefaultManager (not the Manager interface).  That injection point
// does not exist yet.  The test is noted below as a TODO; the fix is to widen
// the field type to timeseries.Manager in server.go and add an exported setter
// or constructor option.

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestCommitTS_SQLiteFailure_TombstonesEvents verifies that when the SQLite
// transaction fails after the Pebble write has already succeeded, the handler
// calls DeleteKeys to tombstone the written events before returning an error,
// so the event is not observable in subsequent reads.
func TestCommitTS_SQLiteFailure_TombstonesEvents(t *testing.T) {
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("rollback-tenant")

	// Step 1: Establish entity at version 1 via a plain (no-TS) commit.
	initBody := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     10,
			"data":   map[string]interface{}{"state": "created"},
		},
		"append": []interface{}{
			map[string]interface{}{
				"entity": "event_log",
				"data":   map[string]interface{}{"msg": "init"},
			},
		},
	}
	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), initBody)
	if status != http.StatusOK {
		t.Fatalf("init commit: got %d: %v", status, result)
	}
	// Confirm we can read the entity and capture the current version.
	getURL := fmt.Sprintf("%s/api/v1/tenant/%s/order/10", env.ts.URL, tenant)
	getStatus, entity := doJSONRequest(t, "GET", getURL, nil)
	if getStatus != http.StatusOK {
		t.Fatalf("GET order/10: got %d: %v", getStatus, entity)
	}
	currentVersion, ok := entity["_version"].(float64)
	if !ok {
		t.Fatalf("_version not in response: %v", entity)
	}
	staleVersion := int(currentVersion) - 1 // intentionally one behind

	// Step 2: Issue a commit carrying TS events with a stale version so SQLite
	// will refuse the write.  Pebble writes first (our ordering guarantee), then
	// SQLite detects the conflict.
	ts0 := time.Now().UTC().Truncate(time.Second)
	conflictBody := map[string]interface{}{
		"update": map[string]interface{}{
			"entity":  "order",
			"id":      10,
			"version": staleVersion,
			"data":    map[string]interface{}{"state": "this-should-not-stick"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{10},
				"time":     ts0.Format(time.RFC3339Nano),
				"nums":     []interface{}{99.0},
			},
		},
	}
	status, result = doJSONRequest(t, "POST", env.commitURL(tenant), conflictBody)
	if status != http.StatusConflict {
		t.Fatalf("expected 409 conflict from stale version, got %d: %v", status, result)
	}

	// Step 3: The TS event must NOT be readable — DeleteKeys must have tombstoned it.
	from := ts0.Add(-time.Second).Format(time.RFC3339Nano)
	to := ts0.Add(time.Second).Format(time.RFC3339Nano)
	qStatus, qResult := env.tsQueryRange(tenant, map[string]interface{}{
		"timeline": 1,
		"dims":     []interface{}{10},
		"from":     from,
		"to":       to,
	})
	if qStatus != http.StatusOK {
		t.Fatalf("tsQueryRange: got %d: %v", qStatus, qResult)
	}
	events, _ := qResult["events"].([]interface{})
	if len(events) != 0 {
		t.Errorf("rollback failed: %d TS event(s) are readable after SQLite conflict; want 0", len(events))
	}

	// Step 4: Entity state must be unchanged.
	getStatus, entityAfter := doJSONRequest(t, "GET", getURL, nil)
	if getStatus != http.StatusOK {
		t.Fatalf("GET order/10 after conflict: got %d", getStatus)
	}
	state, _ := entityAfter["state"].(string)
	if state == "this-should-not-stick" {
		t.Error("entity state was advanced despite SQLite conflict; CAS is broken")
	}
	versionAfter, _ := entityAfter["_version"].(float64)
	if int(versionAfter) != int(currentVersion) {
		t.Errorf("entity _version changed: got %v, want %v", versionAfter, currentVersion)
	}
}

// TestCommitTS_SQLiteFailure_MultipleEvents_AllTombstoned verifies the
// DeleteKeys batch path: when multiple TS events are written and SQLite fails,
// all of them are tombstoned, not just the first.
func TestCommitTS_SQLiteFailure_MultipleEvents_AllTombstoned(t *testing.T) {
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("rollback-batch")

	// Establish entity at version >= 1.
	initBody := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     20,
			"data":   map[string]interface{}{"state": "init"},
		},
		"append": []interface{}{
			map[string]interface{}{
				"entity": "event_log",
				"data":   map[string]interface{}{"msg": "init"},
			},
		},
	}
	status, _ := doJSONRequest(t, "POST", env.commitURL(tenant), initBody)
	if status != http.StatusOK {
		t.Fatalf("init commit: got %d", status)
	}

	// Use version 0 (stale) to force SQLite conflict.
	base := time.Now().UTC().Truncate(time.Second)
	tsEvents := make([]interface{}, 4)
	for i := range tsEvents {
		tsEvents[i] = map[string]interface{}{
			"timeline": 1,
			"dims":     []interface{}{uint64(20 + i)},
			"time":     base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
			"nums":     []interface{}{float64(i)},
		}
	}

	conflictBody := map[string]interface{}{
		"update": map[string]interface{}{
			"entity":  "order",
			"id":      20,
			"version": 0, // stale
			"data":    map[string]interface{}{"state": "should-not-stick"},
		},
		"timeseries": tsEvents,
	}
	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), conflictBody)
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %v", status, result)
	}

	// All 4 events must be absent.
	for i := 0; i < 4; i++ {
		dim := uint64(20 + i)
		ts := base.Add(time.Duration(i) * time.Second)
		from := ts.Add(-time.Millisecond).Format(time.RFC3339Nano)
		to := ts.Add(time.Millisecond).Format(time.RFC3339Nano)
		qStatus, qResult := env.tsQueryRange(tenant, map[string]interface{}{
			"timeline": 1,
			"dims":     []interface{}{dim},
			"from":     from,
			"to":       to,
		})
		if qStatus != http.StatusOK {
			t.Errorf("tsQueryRange[%d]: got %d", i, qStatus)
			continue
		}
		events, _ := qResult["events"].([]interface{})
		if len(events) != 0 {
			t.Errorf("event[%d] (dim=%d) still readable after rollback; want 0", i, dim)
		}
	}
}

// TestCommitTS_SuccessfulCommit_EventPersists verifies the positive case:
// when both Pebble and SQLite succeed, the TS event is readable and the entity
// state has advanced. This is a regression guard for the rollback logic — it
// must not tombstone events on the success path.
func TestCommitTS_SuccessfulCommit_EventPersists(t *testing.T) {
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("persist-check")

	ts0 := time.Now().UTC().Truncate(time.Second)
	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     30,
			"data":   map[string]interface{}{"state": "active"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{30},
				"time":     ts0.Format(time.RFC3339Nano),
				"nums":     []interface{}{7.7},
			},
		},
	}
	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusOK {
		t.Fatalf("commit: got %d: %v", status, result)
	}

	// TS event must be readable.
	from := ts0.Add(-time.Second).Format(time.RFC3339Nano)
	to := ts0.Add(time.Second).Format(time.RFC3339Nano)
	qStatus, qResult := env.tsQueryRange(tenant, map[string]interface{}{
		"timeline": 1,
		"dims":     []interface{}{30},
		"from":     from,
		"to":       to,
	})
	if qStatus != http.StatusOK {
		t.Fatalf("tsQueryRange: got %d: %v", qStatus, qResult)
	}
	events, _ := qResult["events"].([]interface{})
	if len(events) != 1 {
		t.Errorf("expected 1 TS event after successful commit, got %d", len(events))
	}

	// Entity state must have advanced.
	getStatus, entity := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/tenant/%s/order/30", env.ts.URL, tenant), nil)
	if getStatus != http.StatusOK {
		t.Fatalf("GET: got %d", getStatus)
	}
	state, _ := entity["state"].(string)
	if state != "active" {
		t.Errorf("entity state: got %q, want %q", state, "active")
	}
}

// TODO: TestCommitTS_RollbackFails_OLU_CM016
//
// This test requires injecting a Store implementation whose DeleteKeys returns
// an error, into the server's tsManager field.  Currently tsManager is typed
// as *timeseries.DefaultManager (concrete), so there is no injection point.
//
// To enable this test:
//   1. Change server.Server.tsManager from *timeseries.DefaultManager to
//      timeseries.Manager (the interface).
//   2. Add a server.WithTSManager(m timeseries.Manager) option or an exported
//      setter so tests can inject a failing manager.
//   3. Implement a failingStore that delegates Append/AppendBatch normally but
//      returns a sentinel error from DeleteKeys.
//
// Until then, XOLU-CM016 is covered by code review and the structured log
// message emitted by handleCommit in that branch.
