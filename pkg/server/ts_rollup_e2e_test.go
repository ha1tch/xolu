// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// ts_rollup_e2e_test.go
//
// End-to-end HTTP tests for the rollup management and timeline data deletion
// endpoints. All requests go through httptest.Server — no direct store access.
//
// Endpoints covered:
//   POST   /ts/tl/{tid}/rollup/def
//   GET    /ts/tl/{tid}/rollup/list
//   GET    /ts/tl/{tid}/rollup/parent
//   GET    /ts/tl/{tid}/rollup/{rid}
//   DELETE /ts/tl/{tid}/rollup/{rid}
//   POST   /ts/tl/{tid}/rollup/{rid}/run
//   GET    /ts/tl/{tid}/rollup/{rid}/status
//   GET    /ts/rollup/tree
//   DELETE /ts/tl/{tid}/data
//   POST   /ts/tl/{tid}/data/purge

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// ─── harness helpers ──────────────────────────────────────────────────────────

// rollupEnv wraps tsEnv with rollup-specific helpers.
type rollupEnv struct {
	*tsEnv
}

func setupRollupEnv(t *testing.T) *rollupEnv {
	t.Helper()
	env := setupTSServer(t, nil)
	return &rollupEnv{env}
}

func (e *rollupEnv) rollupURL(tenant string, tid int, rest string) string {
	return fmt.Sprintf("%s/api/v1/tenant/%s/ts/tl/%d%s", e.ts.URL, tenant, tid, rest)
}

func (e *rollupEnv) tsTreeURL(tenant string) string {
	return fmt.Sprintf("%s/api/v1/tenant/%s/ts/rollup/tree", e.ts.URL, tenant)
}

// defRollup defines a rollup definition via HTTP and returns the rollup ID.
// Fails the test if the response is not 201.
func (e *rollupEnv) defRollup(tenant string, srcTID int, body map[string]interface{}) string {
	e.t.Helper()
	status, resp := e.do("POST", e.rollupURL(tenant, srcTID, "/rollup/def"), body)
	if status != http.StatusCreated {
		e.t.Fatalf("defRollup %d: got %d: %v", srcTID, status, resp)
	}
	id, ok := resp["id"].(string)
	if !ok || id == "" {
		e.t.Fatalf("defRollup: no id in response: %v", resp)
	}
	return id
}

// provisionAndDefine registers tenant, provisions TS, and defines a set of
// timelines with dims=1 each.
func (e *rollupEnv) provisionAndDefine(tenant string, tids ...int) {
	e.t.Helper()
	e.registerTenant(tenant)
	e.provision(tenant)
	for _, tid := range tids {
		status, resp := e.do("POST", e.tsURL(tenant, "/tl/def"), map[string]interface{}{
			"id": tid, "dims": 1, "name": fmt.Sprintf("tl%d", tid),
		})
		if status != http.StatusCreated {
			e.t.Fatalf("define timeline %d: got %d: %v", tid, status, resp)
		}
	}
}

// appendTS appends a single event to the given timeline at the given time.
func (e *rollupEnv) appendTS(tenant string, tid int, ts time.Time, val float64) {
	e.t.Helper()
	status, resp := e.do("POST", e.tsURL(tenant, "/events"), map[string]interface{}{
		"timeline": tid,
		"dims":     []interface{}{0},
		"time":     ts.UTC().Format(time.RFC3339),
		"nums":     []interface{}{val},
	})
	if status != http.StatusCreated {
		e.t.Fatalf("appendTS tl%d: got %d: %v", tid, status, resp)
	}
}

// ─── POST /rollup/def ────────────────────────────────────────────────────────

func TestTSRollup_DefineHappyPath(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	status, resp := e.do("POST", e.rollupURL("acme", 1, "/rollup/def"), map[string]interface{}{
		"dest_tid":        2,
		"bucket_duration": "1m",
	})
	if status != http.StatusCreated {
		t.Fatalf("define rollup: got %d: %v", status, resp)
	}
	if _, ok := resp["id"]; !ok {
		t.Errorf("response missing 'id': %v", resp)
	}
	if resp["source_tid"] != float64(1) {
		t.Errorf("source_tid: want 1, got %v", resp["source_tid"])
	}
	if resp["dest_tid"] != float64(2) {
		t.Errorf("dest_tid: want 2, got %v", resp["dest_tid"])
	}
	if resp["running"] != false {
		t.Errorf("running: want false immediately after define, got %v", resp["running"])
	}
}

func TestTSRollup_DefineWithLateWindow(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	status, resp := e.do("POST", e.rollupURL("acme", 1, "/rollup/def"), map[string]interface{}{
		"dest_tid":        2,
		"bucket_duration": "5m",
		"late_window":     "30s",
	})
	if status != http.StatusCreated {
		t.Fatalf("define rollup with late_window: got %d: %v", status, resp)
	}
	if resp["late_window"] == "" || resp["late_window"] == nil {
		t.Errorf("late_window missing in response: %v", resp)
	}
}

func TestTSRollup_DefineRejectsTimeline0(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	// Timeline 0 as source.
	status, _ := e.do("POST", e.rollupURL("acme", 0, "/rollup/def"), map[string]interface{}{
		"dest_tid": 1, "bucket_duration": "1m",
	})
	if status != http.StatusBadRequest {
		t.Errorf("timeline 0 as source: want 400, got %d", status)
	}
}

func TestTSRollup_DefineRejectsTimeline0AsDest(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("POST", e.rollupURL("acme", 1, "/rollup/def"), map[string]interface{}{
		"dest_tid": 0, "bucket_duration": "1m",
	})
	if status != http.StatusBadRequest {
		t.Errorf("timeline 0 as dest: want 400, got %d", status)
	}
}

func TestTSRollup_DefineRejectsCycle(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})

	// 2→1 closes the cycle.
	status, _ := e.do("POST", e.rollupURL("acme", 2, "/rollup/def"), map[string]interface{}{
		"dest_tid": 1, "bucket_duration": "1m",
	})
	if status != http.StatusBadRequest {
		t.Errorf("cycle definition: want 400, got %d", status)
	}
}

func TestTSRollup_DefineRejectsSingleParent(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2, 3)

	e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})
	// Third timeline also trying to write to timeline 2.
	status, _ := e.do("POST", e.rollupURL("acme", 3, "/rollup/def"), map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})
	if status != http.StatusBadRequest {
		t.Errorf("single-parent violation: want 400, got %d", status)
	}
}

func TestTSRollup_DefineRejectsMissingBucketDuration(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	status, _ := e.do("POST", e.rollupURL("acme", 1, "/rollup/def"), map[string]interface{}{
		"dest_tid": 2,
	})
	if status != http.StatusBadRequest {
		t.Errorf("missing bucket_duration: want 400, got %d", status)
	}
}

func TestTSRollup_DefineRejectsInvalidBucketDuration(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	status, _ := e.do("POST", e.rollupURL("acme", 1, "/rollup/def"), map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "not-a-duration",
	})
	if status != http.StatusBadRequest {
		t.Errorf("invalid bucket_duration: want 400, got %d", status)
	}
}

func TestTSRollup_DefineRejectsUndefinedDest(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("POST", e.rollupURL("acme", 1, "/rollup/def"), map[string]interface{}{
		"dest_tid": 999, "bucket_duration": "1m",
	})
	if status != http.StatusBadRequest {
		t.Errorf("undefined dest: want 400, got %d", status)
	}
}

// ─── GET /rollup/list ────────────────────────────────────────────────────────

func TestTSRollup_ListEmpty(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := doJSONArray(t, "GET", e.rollupURL("acme", 1, "/rollup/list"), nil)
	if status != http.StatusOK {
		t.Fatalf("list empty: want 200, got %d", status)
	}
}

func TestTSRollup_ListReturnsDefinitions(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2, 3)

	e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})
	e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 3, "bucket_duration": "5m"})

	// Wait — timeline 3 can't have two parents. 1→2 and 1→3 is fine (two children of 1).
	// But both have source_tid=1, so list should show 2 entries.
	status, defs := doJSONArray(t, "GET", e.rollupURL("acme", 1, "/rollup/list"), nil)
	if status != http.StatusOK {
		t.Fatalf("list: want 200, got %d", status)
	}
	if len(defs) != 2 {
		t.Errorf("list: want 2 defs, got %d: %v", len(defs), defs)
	}
}

func TestTSRollup_ListRejectsTimeline0(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := doJSONArray(t, "GET", e.rollupURL("acme", 0, "/rollup/list"), nil)
	if status != http.StatusBadRequest {
		t.Errorf("list tl0: want 400, got %d", status)
	}
}

// ─── GET /rollup/{rid} ────────────────────────────────────────────────────────

func TestTSRollup_GetReturnsDefinition(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	rid := e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "5m",
	})

	status, resp := e.do("GET", e.rollupURL("acme", 1, "/rollup/"+rid), nil)
	if status != http.StatusOK {
		t.Fatalf("get rollup: want 200, got %d: %v", status, resp)
	}
	if resp["id"] != rid {
		t.Errorf("id: want %q, got %v", rid, resp["id"])
	}
	if resp["bucket_duration"] == "" {
		t.Errorf("bucket_duration missing in response")
	}
}

func TestTSRollup_GetNotFound(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("GET", e.rollupURL("acme", 1, "/rollup/nonexistent"), nil)
	if status != http.StatusNotFound {
		t.Errorf("get nonexistent: want 404, got %d", status)
	}
}

func TestTSRollup_GetRejectsWrongSource(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2, 3)

	rid := e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})

	// rid belongs to source=1, looking it up under source=3 should 404.
	status, _ := e.do("GET", e.rollupURL("acme", 3, "/rollup/"+rid), nil)
	if status != http.StatusNotFound {
		t.Errorf("get wrong source: want 404, got %d", status)
	}
}

// ─── GET /rollup/parent ───────────────────────────────────────────────────────

func TestTSRollup_Parent(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})

	// Parent of timeline 2 is the 1→2 definition.
	status, resp := e.do("GET", e.rollupURL("acme", 2, "/rollup/parent"), nil)
	if status != http.StatusOK {
		t.Fatalf("rollup parent: want 200, got %d: %v", status, resp)
	}
	if resp["source_tid"] != float64(1) {
		t.Errorf("parent source_tid: want 1, got %v", resp["source_tid"])
	}
	if resp["dest_tid"] != float64(2) {
		t.Errorf("parent dest_tid: want 2, got %v", resp["dest_tid"])
	}
}

func TestTSRollup_ParentNotFound(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	// Raw timeline with no parent.
	status, _ := e.do("GET", e.rollupURL("acme", 1, "/rollup/parent"), nil)
	if status != http.StatusNotFound {
		t.Errorf("parent of raw timeline: want 404, got %d", status)
	}
}

// ─── GET /rollup/tree ─────────────────────────────────────────────────────────

func TestTSRollup_TreeEmpty(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, resp := e.do("GET", e.tsTreeURL("acme"), nil)
	if status != http.StatusOK {
		t.Fatalf("tree (empty): want 200, got %d: %v", status, resp)
	}
	if resp["tid"] != float64(0) {
		t.Errorf("tree root tid: want 0, got %v", resp["tid"])
	}
}

func TestTSRollup_TreeStructure(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2, 3)

	e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})
	e.defRollup("acme", 2, map[string]interface{}{"dest_tid": 3, "bucket_duration": "5m"})

	status, tree := e.do("GET", e.tsTreeURL("acme"), nil)
	if status != http.StatusOK {
		t.Fatalf("tree: want 200, got %d: %v", status, tree)
	}
	// Root must be tid=0.
	if tree["tid"] != float64(0) {
		t.Errorf("root tid: want 0, got %v", tree["tid"])
	}
	// Timeline 1 should be a child of root.
	children, _ := tree["children"].([]interface{})
	if len(children) == 0 {
		t.Fatal("tree root has no children")
	}
	found1 := false
	for _, c := range children {
		node, _ := c.(map[string]interface{})
		if node["tid"] == float64(1) {
			found1 = true
			// Timeline 1 should have timeline 2 as a child.
			grandchildren, _ := node["children"].([]interface{})
			if len(grandchildren) != 1 {
				t.Errorf("tl1 children: want [2], got %v", grandchildren)
			}
		}
	}
	if !found1 {
		t.Errorf("timeline 1 not found as child of root: %v", children)
	}
}

// ─── POST /rollup/{rid}/run ───────────────────────────────────────────────────

func TestTSRollup_RunHappyPath(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	rid := e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})

	// Append events into the bucket.
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		e.appendTS("acme", 1, base.Add(time.Duration(i)*time.Second), float64(i+1))
	}

	// Run the 1-minute bucket.
	status, resp := e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid+"/run"),
		map[string]interface{}{
			"from": base.Format(time.RFC3339),
			"to":   base.Add(time.Minute).Format(time.RFC3339),
		})
	if status != http.StatusOK {
		t.Fatalf("run rollup: want 200, got %d: %v", status, resp)
	}
	if resp["status"] != "ok" {
		t.Errorf("run status: want \"ok\", got %v", resp["status"])
	}

	// Verify the rollup event was written into timeline 2.
	status2, result := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 2,
		"dims":     []interface{}{0},
		"from":     base.Add(-time.Hour).Format(time.RFC3339),
		"to":       base.Add(time.Hour).Format(time.RFC3339),
	})
	if status2 != http.StatusOK {
		t.Fatalf("range_aggregate on rollup timeline: got %d: %v", status2, result)
	}
	if result["count"] != float64(1) {
		t.Errorf("rollup event count: want 1, got %v", result["count"])
	}
}

func TestTSRollup_RunDefaultsToLastBucket(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	rid := e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})

	// Append an event in the last closed bucket.
	lastBucket := time.Now().UTC().Truncate(time.Minute)
	e.appendTS("acme", 1, lastBucket.Add(-30*time.Second), 42.0)

	// Run with no body — should default to last closed bucket.
	status, resp := e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid+"/run"), nil)
	if status != http.StatusOK {
		t.Fatalf("run (default bucket): want 200, got %d: %v", status, resp)
	}
}

func TestTSRollup_RunStartsWorker(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	rid := e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})

	// Worker should NOT be running immediately after define.
	status1, st1 := e.do("GET", e.rollupURL("acme", 1, "/rollup/"+rid+"/status"), nil)
	if status1 != http.StatusOK {
		t.Fatalf("status before run: want 200, got %d", status1)
	}
	if st1["running"] != false {
		t.Errorf("running before run: want false, got %v", st1["running"])
	}

	// Run it.
	e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid+"/run"), nil)

	// Worker should now be running.
	status2, st2 := e.do("GET", e.rollupURL("acme", 1, "/rollup/"+rid+"/status"), nil)
	if status2 != http.StatusOK {
		t.Fatalf("status after run: want 200, got %d", status2)
	}
	if st2["running"] != true {
		t.Errorf("running after run: want true, got %v", st2["running"])
	}
}

func TestTSRollup_RunCascadeStartsDescendants(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2, 3)

	rid1 := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})
	rid2 := e.defRollup("acme", 2, map[string]interface{}{"dest_tid": 3, "bucket_duration": "5m"})

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	e.appendTS("acme", 1, base.Add(30*time.Second), 1.0)

	// Run with cascade=true.
	status, resp := e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid1+"/run"),
		map[string]interface{}{
			"from":    base.Format(time.RFC3339),
			"to":      base.Add(time.Minute).Format(time.RFC3339),
			"cascade": true,
		})
	if status != http.StatusOK {
		t.Fatalf("run cascade: want 200, got %d: %v", status, resp)
	}

	// Both workers should be running.
	for _, tc := range []struct {
		tid int
		rid string
	}{{1, rid1}, {2, rid2}} {
		_, workerSt := e.do("GET", e.rollupURL("acme", tc.tid, "/rollup/"+tc.rid+"/status"), nil)
		if workerSt["running"] != true {
			t.Errorf("rollup tl%d: running should be true after cascade, got %v", tc.tid, workerSt["running"])
		}
	}
}

func TestTSRollup_RunNotFound(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("POST", e.rollupURL("acme", 1, "/rollup/nonexistent/run"), nil)
	if status != http.StatusNotFound {
		t.Errorf("run nonexistent: want 404, got %d", status)
	}
}

func TestTSRollup_RunFieldLayout(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	rid := e.defRollup("acme", 1, map[string]interface{}{
		"dest_tid": 2, "bucket_duration": "1m",
	})

	// Write 6 events with known values.
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	vals := []float64{10, 20, 30, 40, 50, 60}
	for i, v := range vals {
		e.appendTS("acme", 1, base.Add(time.Duration(i)*10*time.Second), v)
	}

	e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid+"/run"), map[string]interface{}{
		"from": base.Format(time.RFC3339),
		"to":   base.Add(time.Minute).Format(time.RFC3339),
	})

	// Read back via range_aggregate which returns per-field stats.
	status, result := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 2,
		"dims":     []interface{}{0},
		"from":     base.Add(-time.Hour).Format(time.RFC3339),
		"to":       base.Add(time.Hour).Format(time.RFC3339),
	})
	if status != http.StatusOK {
		t.Fatalf("range_aggregate: got %d: %v", status, result)
	}
	// val4 = count = 1 rollup event
	if result["count"] != float64(1) {
		t.Errorf("rollup event count in dest timeline: want 1, got %v", result["count"])
	}

	// Verify rollup field values via range_aggregate on dest timeline.
	// val0=mean, val1=min, val2=max, val3=sum, val4=count.
	// The rollup event itself is one event containing the aggregated stats.
	_, aggResult := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 2,
		"dims":     []interface{}{0},
		"from":     base.Add(-time.Hour).Format(time.RFC3339),
		"to":       base.Add(time.Hour).Format(time.RFC3339),
	})
	// 1 rollup event in the dest timeline.
	if aggResult["count"] != float64(1) {
		t.Fatalf("rollup event count: want 1, got %v", aggResult["count"])
	}
	// The rollup event's val0 is the mean of the src events (35.0).
	// We verify via the avg of the dest timeline's val0 field.
	wantMean := (10.0 + 20 + 30 + 40 + 50 + 60) / 6
	avgs, _ := aggResult["avgs"].([]interface{})
	if len(avgs) == 0 {
		t.Fatalf("range_aggregate missing avgs: %v", aggResult)
	}
	if avgs[0].(float64) != wantMean {
		t.Errorf("dest val0 (mean of src mean): want %.4f, got %.4f", wantMean, avgs[0].(float64))
	}
	// val4 = count = 6 raw events encoded as float in the rollup event.
	// When we aggregate the 1 rollup event, val4 average = 6.0.
	if len(avgs) >= 5 && avgs[4].(float64) != 6.0 {
		t.Errorf("dest val4 (count): want 6.0, got %v", avgs[4])
	}
}

func TestTSRollup_RunInvalidFromTimestamp(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)
	rid := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})

	// Malformed timestamp — should return 400.
	status, _ := e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid+"/run"),
		map[string]interface{}{
			"from": "not-a-timestamp",
			"to":   "2026-06-15T10:00:00Z",
		})
	if status != http.StatusBadRequest {
		t.Errorf("invalid from timestamp: want 400, got %d", status)
	}
}

// ─── GET /rollup/{rid}/status ─────────────────────────────────────────────────

func TestTSRollup_StatusFields(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)
	rid := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	e.appendTS("acme", 1, base.Add(30*time.Second), 5.0)
	e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid+"/run"), map[string]interface{}{
		"from": base.Format(time.RFC3339),
		"to":   base.Add(time.Minute).Format(time.RFC3339),
	})

	status, st := e.do("GET", e.rollupURL("acme", 1, "/rollup/"+rid+"/status"), nil)
	if status != http.StatusOK {
		t.Fatalf("status: want 200, got %d: %v", status, st)
	}
	if st["id"] != rid {
		t.Errorf("status id: want %q, got %v", rid, st["id"])
	}
	if st["source_tid"] != float64(1) {
		t.Errorf("source_tid: want 1, got %v", st["source_tid"])
	}
	if st["dest_tid"] != float64(2) {
		t.Errorf("dest_tid: want 2, got %v", st["dest_tid"])
	}
	if st["running"] != true {
		t.Errorf("running: want true, got %v", st["running"])
	}
	if st["events_written"] != float64(1) {
		t.Errorf("events_written: want 1, got %v", st["events_written"])
	}
	if st["last_bucket_end"] == nil || st["last_bucket_end"] == "" {
		t.Errorf("last_bucket_end should be set: %v", st)
	}
}

func TestTSRollup_StatusNotFound(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("GET", e.rollupURL("acme", 1, "/rollup/nonexistent/status"), nil)
	if status != http.StatusNotFound {
		t.Errorf("status nonexistent: want 404, got %d", status)
	}
}

// ─── DELETE /rollup/{rid} ────────────────────────────────────────────────────

func TestTSRollup_DeleteLeaf(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)
	rid := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})

	status, _ := e.do("DELETE", e.rollupURL("acme", 1, "/rollup/"+rid), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete leaf: want 204, got %d", status)
	}

	// Should no longer appear in the list.
	_, defs := doJSONArray(t, "GET", e.rollupURL("acme", 1, "/rollup/list"), nil)
	if len(defs) != 0 {
		t.Errorf("after delete: want 0 defs, got %d", len(defs))
	}
}

func TestTSRollup_DeleteCascades(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2, 3)
	rid1 := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})
	// Define the child rollup 2→3.
	e.defRollup("acme", 2, map[string]interface{}{"dest_tid": 3, "bucket_duration": "5m"})

	// Confirm child def exists before cascade delete.
	_, defsBeforeDelete := doJSONArray(t, "GET", e.rollupURL("acme", 2, "/rollup/list"), nil)
	if len(defsBeforeDelete) != 1 {
		t.Fatalf("before delete: tl2 should have 1 def, got %d", len(defsBeforeDelete))
	}

	// Delete the parent — with default cascade=true, child 2→3 also deleted.
	status, resp := e.do("DELETE", e.rollupURL("acme", 1, "/rollup/"+rid1), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete cascade: want 204, got %d: %v", status, resp)
	}

	// Timeline 2's rollup (2→3) should be gone.
	_, defs := doJSONArray(t, "GET", e.rollupURL("acme", 2, "/rollup/list"), nil)
	if len(defs) != 0 {
		t.Errorf("after cascade delete: tl2 defs should be 0, got %d", len(defs))
	}
}

func TestTSRollup_DeleteNotFound(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("DELETE", e.rollupURL("acme", 1, "/rollup/nonexistent"), nil)
	if status != http.StatusNotFound {
		t.Errorf("delete nonexistent: want 404, got %d", status)
	}
}

func TestTSRollup_DeleteStopsWorker(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)
	rid := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})

	// Start the worker.
	e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid+"/run"), nil)

	// Delete it.
	e.do("DELETE", e.rollupURL("acme", 1, "/rollup/"+rid), nil)

	// Status should now 404 (definition removed).
	status, _ := e.do("GET", e.rollupURL("acme", 1, "/rollup/"+rid+"/status"), nil)
	if status != http.StatusNotFound {
		t.Errorf("status after delete: want 404, got %d", status)
	}
}

// ─── Multi-bucket and backfill scenarios ──────────────────────────────────────

func TestTSRollup_BackfillMultipleBuckets(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)
	rid := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	// 3 minutes of data: 1 event per minute.
	for m := 0; m < 3; m++ {
		e.appendTS("acme", 1, base.Add(time.Duration(m)*time.Minute+30*time.Second), float64(m+1))
	}

	// Run covering all 3 minutes — should produce 3 rollup events.
	status, _ := e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid+"/run"),
		map[string]interface{}{
			"from": base.Format(time.RFC3339),
			"to":   base.Add(3 * time.Minute).Format(time.RFC3339),
		})
	if status != http.StatusOK {
		t.Fatalf("backfill run: want 200, got %d", status)
	}

	// Timeline 2 should have 3 rollup events.
	_, result := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 2,
		"dims":     []interface{}{0},
		"from":     base.Add(-time.Hour).Format(time.RFC3339),
		"to":       base.Add(time.Hour).Format(time.RFC3339),
	})
	if result["count"] != float64(3) {
		t.Errorf("backfill: want 3 rollup events, got %v", result["count"])
	}

	// events_written should reflect the 3 buckets.
	_, st := e.do("GET", e.rollupURL("acme", 1, "/rollup/"+rid+"/status"), nil)
	if st["events_written"] != float64(3) {
		t.Errorf("events_written: want 3, got %v", st["events_written"])
	}
}

func TestTSRollup_CascadeBackfillThreeLevels(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2, 3)
	rid1 := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})
	e.defRollup("acme", 2, map[string]interface{}{"dest_tid": 3, "bucket_duration": "5m"})

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	e.appendTS("acme", 1, base.Add(30*time.Second), 10.0)

	// Run with cascade — both levels should be populated.
	e.do("POST", e.rollupURL("acme", 1, "/rollup/"+rid1+"/run"),
		map[string]interface{}{
			"from":    base.Format(time.RFC3339),
			"to":      base.Add(time.Minute).Format(time.RFC3339),
			"cascade": true,
		})

	// L1 (tl2): 1 event.
	_, r2 := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 2, "dims": []interface{}{0},
		"from": base.Add(-time.Hour).Format(time.RFC3339),
		"to":   base.Add(time.Hour).Format(time.RFC3339),
	})
	if r2["count"] != float64(1) {
		t.Errorf("L1 rollup (tl2): want 1 event, got %v", r2["count"])
	}

	// L2 (tl3): 1 event.
	_, r3 := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 3, "dims": []interface{}{0},
		"from": base.Add(-time.Hour).Format(time.RFC3339),
		"to":   base.Add(time.Hour).Format(time.RFC3339),
	})
	if r3["count"] != float64(1) {
		t.Errorf("L2 rollup (tl3): want 1 event, got %v", r3["count"])
	}
}

// ─── DELETE /timelines/{tid}/data ────────────────────────────────────────────

func TestTSRollup_DeleteTimelineDataClearsAll(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		e.appendTS("acme", 1, base.Add(time.Duration(i)*time.Second), 1.0)
	}

	// Confirm 20 events.
	_, before := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 1, "dims": []interface{}{0},
		"from": base.Add(-time.Hour).Format(time.RFC3339),
		"to":   base.Add(time.Hour).Format(time.RFC3339),
	})
	if before["count"] != float64(20) {
		t.Fatalf("before delete: want 20, got %v", before["count"])
	}

	status, _ := e.do("DELETE", e.rollupURL("acme", 1, "/data"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete data: want 204, got %d", status)
	}

	_, after := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 1, "dims": []interface{}{0},
		"from": base.Add(-time.Hour).Format(time.RFC3339),
		"to":   base.Add(time.Hour).Format(time.RFC3339),
	})
	if after["count"] != float64(0) {
		t.Errorf("after delete: want 0, got %v", after["count"])
	}
}

func TestTSRollup_DeleteTimelineDataPreservesDefinition(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	e.appendTS("acme", 1, time.Now(), 1.0)
	e.do("DELETE", e.rollupURL("acme", 1, "/data"), nil)

	// Timeline should still be gettable.
	status, _ := e.do("GET", e.tsURL("acme", "/tl/1"), nil)
	if status != http.StatusOK {
		t.Errorf("get timeline after data delete: want 200, got %d", status)
	}

	// Should be able to append new events.
	e.appendTS("acme", 1, time.Now().Add(time.Minute), 2.0)
	_, result := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 1, "dims": []interface{}{0},
		"from": time.Now().Add(-time.Hour).Format(time.RFC3339),
		"to":   time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	if result["count"] != float64(1) {
		t.Errorf("after re-append: want 1 event, got %v", result["count"])
	}
}

func TestTSRollup_DeleteTimelineDataRejectsTimeline0(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("DELETE", e.rollupURL("acme", 0, "/data"), nil)
	if status != http.StatusBadRequest {
		t.Errorf("delete data tl0: want 400, got %d", status)
	}
}

// ─── POST /timelines/{tid}/data/purge ─────────────────────────────────────────

func TestTSRollup_PurgeTimelineRangeHappyPath(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		e.appendTS("acme", 1, base.Add(time.Duration(i)*time.Second), float64(i))
	}

	// Purge seconds 3–6 (exclusive): removes events at t+3s, t+4s, t+5s.
	status, _ := e.do("POST", e.rollupURL("acme", 1, "/data/purge"), map[string]interface{}{
		"from": base.Add(3 * time.Second).Format(time.RFC3339),
		"to":   base.Add(6 * time.Second).Format(time.RFC3339),
	})
	if status != http.StatusNoContent {
		t.Fatalf("purge: want 204, got %d", status)
	}

	// 7 events should remain (0,1,2 before + 6,7,8,9 after).
	_, result := e.do("POST", e.tsURL("acme", "/range_aggregate"), map[string]interface{}{
		"timeline": 1, "dims": []interface{}{0},
		"from": base.Add(-time.Hour).Format(time.RFC3339),
		"to":   base.Add(time.Hour).Format(time.RFC3339),
	})
	if result["count"] != float64(7) {
		t.Errorf("after purge: want 7 events, got %v", result["count"])
	}
}

func TestTSRollup_PurgeTimelineRangeRejectsTimeline0(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("POST", e.rollupURL("acme", 0, "/data/purge"), map[string]interface{}{
		"from": "2026-01-01T00:00:00Z",
		"to":   "2026-01-02T00:00:00Z",
	})
	if status != http.StatusBadRequest {
		t.Errorf("purge tl0: want 400, got %d", status)
	}
}

func TestTSRollup_PurgeTimelineRangeRejectsInvertedRange(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	status, _ := e.do("POST", e.rollupURL("acme", 1, "/data/purge"), map[string]interface{}{
		"from": "2026-01-02T00:00:00Z",
		"to":   "2026-01-01T00:00:00Z",
	})
	if status != http.StatusBadRequest {
		t.Errorf("purge inverted range: want 400, got %d", status)
	}
}

func TestTSRollup_PurgeTimelineRangeMissingFields(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1)

	// Missing "to".
	status, _ := e.do("POST", e.rollupURL("acme", 1, "/data/purge"), map[string]interface{}{
		"from": "2026-01-01T00:00:00Z",
	})
	if status == http.StatusNoContent {
		t.Errorf("missing 'to': want non-204, got 204")
	}
}

// ─── Error code surface ───────────────────────────────────────────────────────

func TestTSRollup_ErrorCodesPresent(t *testing.T) {
	e := setupRollupEnv(t)
	e.provisionAndDefine("acme", 1, 2)

	rid := e.defRollup("acme", 1, map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})

	checkCode := func(method, path string, body interface{}, wantCode string) {
		t.Helper()
		status, resp := e.do(method, fmt.Sprintf("%s/api/v1/tenant/acme/ts%s", e.ts.URL, path), body)
		if status == http.StatusOK || status == http.StatusCreated || status == http.StatusNoContent {
			t.Errorf("%s %s: expected error, got %d", method, path, status)
			return
		}
		// Error envelope: {"error": {"code": "...", "message": "...", "status": N}}
		var code string
		if errObj, ok := resp["error"].(map[string]interface{}); ok {
			code, _ = errObj["code"].(string)
		}
		if code != wantCode {
			t.Errorf("%s %s: want code %q, got %q (status=%d resp=%v)",
				method, path, wantCode, code, status, resp)
		}
	}

	// XOLU-TS022: timeline 0
	checkCode("DELETE", "/tl/0/rollup/"+rid, nil, "XOLU-TS022")
	checkCode("DELETE", "/tl/0/data", nil, "XOLU-TS022")
	checkCode("POST", "/tl/0/data/purge", map[string]interface{}{
		"from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00Z",
	}, "XOLU-TS022")

	// XOLU-TS023: cycle
	e.provisionAndDefine("acme") // no-op — already provisioned
	// 1→2 already defined; try 2→1
	_, r23 := e.do("POST", fmt.Sprintf("%s/api/v1/tenant/acme/ts/tl/2/rollup/def", e.ts.URL),
		map[string]interface{}{"dest_tid": 1, "bucket_duration": "1m"})
	{
		var code23 string
		if errObj, ok := r23["error"].(map[string]interface{}); ok {
			code23, _ = errObj["code"].(string)
		}
		if code23 != "XOLU-TS023" {
			t.Errorf("XOLU-TS023: want cycle error, got code=%q resp=%v", code23, r23)
		}
	}

	// XOLU-TS025: rollup not found
	checkCode("GET", "/tl/1/rollup/missing", nil, "XOLU-TS025")
	checkCode("DELETE", "/tl/1/rollup/missing", nil, "XOLU-TS025")
	checkCode("POST", "/tl/1/rollup/missing/run", nil, "XOLU-TS025")
	checkCode("GET", "/tl/1/rollup/missing/status", nil, "XOLU-TS025")

	// XOLU-TS026: dest already in use — need a third timeline
	e.do("POST", e.tsURL("acme", "/tl/def"), map[string]interface{}{
		"id": 4, "dims": 1, "name": "tl4",
	})
	_, r26 := e.do("POST", fmt.Sprintf("%s/api/v1/tenant/acme/ts/tl/4/rollup/def", e.ts.URL),
		map[string]interface{}{"dest_tid": 2, "bucket_duration": "1m"})
	{
		var code26 string
		if errObj, ok := r26["error"].(map[string]interface{}); ok {
			code26, _ = errObj["code"].(string)
		}
		if code26 != "XOLU-TS026" {
			t.Errorf("XOLU-TS026: want dest-in-use error, got code=%q resp=%v", code26, r26)
		}
	}
}

// ─── Multi-tenant isolation ───────────────────────────────────────────────────

func TestTSRollup_MultiTenantIsolation(t *testing.T) {
	e := setupRollupEnv(t)

	// Two tenants, both with rollup definitions on the same timeline IDs.
	for _, tenant := range []string{"alpha", "beta"} {
		e.provisionAndDefine(tenant, 1, 2)
		e.defRollup(tenant, 1, map[string]interface{}{
			"dest_tid": 2, "bucket_duration": "1m",
		})
	}

	// Alpha's data must not appear in beta's rollup and vice versa.
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	e.appendTS("alpha", 1, base.Add(30*time.Second), 100.0)

	alphaRID := func() string {
		_, defs := doJSONArray(t, "GET", e.rollupURL("alpha", 1, "/rollup/list"), nil)
		if len(defs) == 0 {
			t.Fatal("alpha: no rollup defs")
		}
		return defs[0].(map[string]interface{})["id"].(string)
	}()

	e.do("POST", e.rollupURL("alpha", 1, "/rollup/"+alphaRID+"/run"), map[string]interface{}{
		"from": base.Format(time.RFC3339),
		"to":   base.Add(time.Minute).Format(time.RFC3339),
	})

	// Beta's timeline 2 should have 0 rollup events.
	_, betaResult := e.do("POST", e.tsURL("beta", "/range_aggregate"), map[string]interface{}{
		"timeline": 2, "dims": []interface{}{0},
		"from": base.Add(-time.Hour).Format(time.RFC3339),
		"to":   base.Add(time.Hour).Format(time.RFC3339),
	})
	if betaResult["count"] != float64(0) {
		t.Errorf("tenant isolation: beta tl2 should have 0 events, got %v", betaResult["count"])
	}
}

// TestTSDeleteTimeline covers DELETE /ts/tl/{timeline_id} — removing a timeline
// definition together with its data and rollups (distinct from deleting only
// the data). It exercises: a plain delete, a delete of a timeline that carries
// both events and a rollup (cascade default-on), the root-timeline guard, and
// the unknown-timeline 404.
func TestTSDeleteTimeline(t *testing.T) {
	e := setupRollupEnv(t)

	// Case 1: plain delete of an empty, rollup-less timeline.
	e.provisionAndDefine("acme", 1)
	status, _ := e.do("DELETE", e.tsURL("acme", "/tl/1"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("plain delete: want 204, got %d", status)
	}
	// It must now be gone: a fetch is 404.
	status, _ = e.do("GET", e.tsURL("acme", "/tl/1"), nil)
	if status != http.StatusNotFound {
		t.Errorf("after delete, GET tl/1: want 404, got %d", status)
	}

	// Case 2: delete a timeline that has data AND a rollup hanging off it.
	// With cascade on (the test server default), this removes the rollup, the
	// data, and the definition in one call.
	e.provisionAndDefine("beta", 10, 11)
	e.appendTS("beta", 10, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1.5)
	rid := e.defRollup("beta", 10, map[string]interface{}{
		"dest_tid":        11,
		"bucket_duration": "1m",
	})
	status, _ = e.do("DELETE", e.tsURL("beta", "/tl/10"), nil)
	if status != http.StatusNoContent {
		t.Fatalf("cascade delete: want 204, got %d", status)
	}
	// The source timeline is gone…
	status, _ = e.do("GET", e.tsURL("beta", "/tl/10"), nil)
	if status != http.StatusNotFound {
		t.Errorf("after cascade delete, GET tl/10: want 404, got %d", status)
	}
	// …and so is its rollup (fetching it on the now-deleted source is 404).
	status, _ = e.do("GET", e.rollupURL("beta", 10, "/rollup/"+rid), nil)
	if status != http.StatusNotFound {
		t.Errorf("after cascade delete, GET rollup %s: want 404, got %d", rid, status)
	}

	// Case 3: the structural root timeline (id 0) cannot be deleted.
	status, _ = e.do("DELETE", e.tsURL("acme", "/tl/0"), nil)
	if status != http.StatusBadRequest {
		t.Errorf("delete root tl/0: want 400, got %d", status)
	}

	// Case 4: deleting an undefined timeline is a 404.
	status, _ = e.do("DELETE", e.tsURL("acme", "/tl/999"), nil)
	if status != http.StatusNotFound {
		t.Errorf("delete undefined tl/999: want 404, got %d", status)
	}
}
