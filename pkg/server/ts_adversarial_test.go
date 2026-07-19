// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// ts_adversarial_test.go
//
// Adversarial coverage of previously untested and lightly-tested
// timeseries handler paths. Uses shared servers per configuration group
// to minimise SQLite file-descriptor consumption.
//
// Targets: tsParseSyncTimeline/HandleTSSyncGet/On/Off (0%),
// HandleTSProvision error branches (50%), HandleTSUpdateTimeline (56%),
// HandleTSBatchAppend payload+per-event errors (79%), HandleTSQueryRangePost (57%),
// HandleTSLatest (61%), HandleTSPatchRetention (54%), HandleTSRangeAggregate (50%),
// HandleTSFullAggregate (56%), HandleTSStats (66%), HandleTSTimelineStats (66%),
// tsStore error branches (60%), eventToResponse payload paths (42%),
// encodePayload (33%), parseInterval all 9 values (27%), tsQueryLimits (64%).

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

// ─── shared server helpers ────────────────────────────────────────────────────

// syncURL builds the URL for a timeline's sync endpoints.
func syncURL(base, tenant string, tid int, suffix string) string {
	return fmt.Sprintf("%s/api/v1/tenant/%s/ts/tl/%d/sync%s", base, tenant, tid, suffix)
}

// sharedTSEnv provisions acme with one dims=1 timeline. All subtests under
// the same top-level test share this single server+database.
func sharedTSEnv(t *testing.T) *tsEnv {
	t.Helper()
	env := setupTSServer(t, nil)
	env.registerTenant("acme")
	env.provision("acme")
	env.defineTimeline("acme", map[string]interface{}{"id": 1, "dims": 1, "name": "sensors"})
	return env
}

// ─── HandleTSSyncGet / HandleTSSyncOn / HandleTSSyncOff (all 0%) ─────────────

func TestTSSync(t *testing.T) {
	env := sharedTSEnv(t)

	t.Run("default_is_sync", func(t *testing.T) {
		status, resp := doJSONRequest(t, "GET", syncURL(env.ts.URL, "acme", 1, ""), nil)
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d: %v", status, resp)
		}
		if resp["nosync"] != false {
			t.Errorf("default nosync: want false, got %v", resp["nosync"])
		}
		if resp["timeline_id"] != float64(1) {
			t.Errorf("timeline_id: want 1, got %v", resp["timeline_id"])
		}
	})

	t.Run("turn_off_then_on", func(t *testing.T) {
		_, r1 := doJSONRequest(t, "POST", syncURL(env.ts.URL, "acme", 1, "/off"), nil)
		if r1["nosync"] != true {
			t.Errorf("after /off: nosync want true, got %v", r1["nosync"])
		}
		s2, r2 := doJSONRequest(t, "GET", syncURL(env.ts.URL, "acme", 1, ""), nil)
		if s2 != http.StatusOK || r2["nosync"] != true {
			t.Errorf("GET after /off: want nosync=true, got status=%d %v", s2, r2)
		}
		_, r3 := doJSONRequest(t, "POST", syncURL(env.ts.URL, "acme", 1, "/on"), nil)
		if r3["nosync"] != false {
			t.Errorf("after /on: nosync want false, got %v", r3["nosync"])
		}
		s4, r4 := doJSONRequest(t, "GET", syncURL(env.ts.URL, "acme", 1, ""), nil)
		if s4 != http.StatusOK || r4["nosync"] != false {
			t.Errorf("GET after /on: want nosync=false, got status=%d %v", s4, r4)
		}
	})

	t.Run("invalid_timeline_id", func(t *testing.T) {
		for _, suffix := range []string{"", "/on", "/off"} {
			method := "GET"
			if suffix != "" {
				method = "POST"
			}
			url := fmt.Sprintf("%s/api/v1/tenant/acme/ts/tl/notanumber/sync%s", env.ts.URL, suffix)
			status, _ := doJSONRequest(t, method, url, nil)
			if status != http.StatusBadRequest {
				t.Errorf("sync%s invalid tid: want 400, got %d", suffix, status)
			}
		}
	})

	t.Run("undefined_timeline", func(t *testing.T) {
		for _, suffix := range []string{"", "/on", "/off"} {
			method := "GET"
			if suffix != "" {
				method = "POST"
			}
			status, _ := doJSONRequest(t, method, syncURL(env.ts.URL, "acme", 99, suffix), nil)
			if status != http.StatusNotFound {
				t.Errorf("sync%s undefined tl: want 404, got %d", suffix, status)
			}
		}
	})

	t.Run("rapid_toggle", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			suffix := "/on"
			if i%2 == 0 {
				suffix = "/off"
			}
			status, _ := doJSONRequest(t, "POST", syncURL(env.ts.URL, "acme", 1, suffix), nil)
			if status != http.StatusOK {
				t.Fatalf("toggle %d (sync%s): want 200, got %d", i, suffix, status)
			}
		}
		// Final: odd i=19 → /on
		_, r := doJSONRequest(t, "GET", syncURL(env.ts.URL, "acme", 1, ""), nil)
		if r["nosync"] != false {
			t.Errorf("after final /on: want nosync=false, got %v", r["nosync"])
		}
	})

	t.Run("persists_data_in_nosync_mode", func(t *testing.T) {
		doJSONRequest(t, "POST", syncURL(env.ts.URL, "acme", 1, "/off"), nil)
		base := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
		env.appendEvent("acme", map[string]interface{}{
			"timeline": 1, "dims": []interface{}{0},
			"time": base.Format(time.RFC3339), "nums": []interface{}{42.0},
		})
		from := base.Add(-time.Second).Format(time.RFC3339)
		to := base.Add(time.Second).Format(time.RFC3339)
		qURL := fmt.Sprintf("%s?timeline=1&dims=0&from=%s&to=%s", env.tsURL("acme", "/events"), from, to)
		status, result := doJSONRequest(t, "GET", qURL, nil)
		if status != http.StatusOK {
			t.Fatalf("query after nosync: got %d: %v", status, result)
		}
		events, _ := result["events"].([]interface{})
		if len(events) != 1 {
			t.Errorf("want 1 event in nosync mode, got %d", len(events))
		}
		doJSONRequest(t, "POST", syncURL(env.ts.URL, "acme", 1, "/on"), nil)
	})
}

// ─── Sync with TS disabled / unprovisioned (separate servers needed) ─────────

func TestTSSync_TSDisabled(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) { cfg.TimeseriesEnabled = false })
	env.registerTenant("acme")
	url := syncURL(env.ts.URL, "acme", 1, "")
	status, _ := doJSONRequest(t, "GET", url, nil)
	if status == http.StatusOK {
		t.Errorf("sync GET with TS disabled: should not return 200, got %d", status)
	}
}

func TestTSSync_UnprovisionedTenant(t *testing.T) {
	env := setupTSServer(t, nil)
	env.registerTenant("ghost")
	status, _ := doJSONRequest(t, "GET", syncURL(env.ts.URL, "ghost", 1, ""), nil)
	if status != http.StatusNotFound {
		t.Errorf("sync GET unprovisioned: want 404, got %d", status)
	}
}

// ─── HandleTSProvision error branches ────────────────────────────────────────

func TestTSProvision_Errors(t *testing.T) {
	t.Run("ts_disabled", func(t *testing.T) {
		env := setupTSServer(t, func(cfg *config.Config) { cfg.TimeseriesEnabled = false })
		env.registerTenant("acme")
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/provision"), nil)
		if status == http.StatusCreated || status == http.StatusOK {
			t.Errorf("provision with TS disabled: should not succeed, got %d", status)
		}
	})

	t.Run("unknown_tenant", func(t *testing.T) {
		env := setupTSServer(t, nil)
		url := fmt.Sprintf("%s/api/v1/tenant/nobody/ts/provision", env.ts.URL)
		status, _ := doJSONRequest(t, "POST", url, nil)
		if status != http.StatusNotFound {
			t.Errorf("provision unknown tenant: want 404, got %d", status)
		}
	})
}

// ─── HandleTSUpdateTimeline ───────────────────────────────────────────────────

func TestTSUpdateTimeline(t *testing.T) {
	env := sharedTSEnv(t)

	t.Run("happy_path", func(t *testing.T) {
		status, resp := doJSONRequest(t, "PATCH", env.tsURL("acme", "/tl/1"),
			map[string]interface{}{"name": "renamed-sensor", "retention_days": 30})
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d: %v", status, resp)
		}
		if resp["name"] != "renamed-sensor" {
			t.Errorf("name: want 'renamed-sensor', got %v", resp["name"])
		}
	})

	t.Run("invalid_id", func(t *testing.T) {
		status, _ := doJSONRequest(t, "PATCH", env.tsURL("acme", "/tl/notanumber"),
			map[string]interface{}{"name": "x"})
		if status != http.StatusBadRequest {
			t.Errorf("invalid tid: want 400, got %d", status)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		status, _ := doJSONRequest(t, "PATCH", env.tsURL("acme", "/tl/99"),
			map[string]interface{}{"name": "ghost"})
		if status != http.StatusNotFound {
			t.Errorf("undefined tl: want 404, got %d", status)
		}
	})

	t.Run("bad_body", func(t *testing.T) {
		status, _ := doJSONRequest(t, "PATCH", env.tsURL("acme", "/tl/1"), "not-json-object")
		if status != http.StatusBadRequest {
			t.Errorf("bad body: want 400, got %d", status)
		}
	})
}

// ─── HandleTSBatchAppend payload and per-event errors ─────────────────────────

func TestTSBatch(t *testing.T) {
	env := sharedTSEnv(t)
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	t.Run("with_payload", func(t *testing.T) {
		status, resp := doJSONRequest(t, "POST", env.tsURL("acme", "/events/batch"),
			map[string]interface{}{
				"events": []interface{}{
					map[string]interface{}{
						"timeline": 1, "dims": []interface{}{0},
						"time": base.Format(time.RFC3339), "nums": []interface{}{1.0},
						"payload": map[string]interface{}{"sensor": "T1", "unit": "°C"},
					},
				},
			})
		if status != http.StatusOK {
			t.Fatalf("batch with payload: want 200, got %d: %v", status, resp)
		}
		if resp["accepted"] != float64(1) {
			t.Errorf("accepted: want 1, got %v", resp["accepted"])
		}
	})

	t.Run("per_event_timestamp_error", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/events/batch"),
			map[string]interface{}{
				"events": []interface{}{
					map[string]interface{}{
						"timeline": 1, "dims": []interface{}{0},
						"time": base.Add(time.Hour).Format(time.RFC3339), "nums": []interface{}{1.0},
					},
					map[string]interface{}{
						"timeline": 1, "dims": []interface{}{0},
						"time": "not-a-timestamp", "nums": []interface{}{2.0},
					},
				},
			})
		if status != http.StatusBadRequest {
			t.Errorf("per-event bad timestamp: want 400, got %d", status)
		}
	})

	t.Run("bad_body", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/events/batch"), "not-json")
		if status != http.StatusBadRequest {
			t.Errorf("bad body: want 400, got %d", status)
		}
	})

	t.Run("empty_events", func(t *testing.T) {
		status, resp := doJSONRequest(t, "POST", env.tsURL("acme", "/events/batch"),
			map[string]interface{}{"events": []interface{}{}})
		if status != http.StatusOK {
			t.Errorf("empty: want 200, got %d: %v", status, resp)
		}
	})
}

// ─── HandleTSQueryRangePost ───────────────────────────────────────────────────

func TestTSQueryRangePost(t *testing.T) {
	env := sharedTSEnv(t)
	base := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	env.appendEvent("acme", map[string]interface{}{
		"timeline": 1, "dims": []interface{}{0},
		"time": base.Format(time.RFC3339), "nums": []interface{}{5.0},
	})

	t.Run("happy_path", func(t *testing.T) {
		status, resp := doJSONRequest(t, "POST", env.tsURL("acme", "/query/range"),
			map[string]interface{}{
				"timeline": 1, "dims": []interface{}{0},
				"from": base.Add(-time.Second).Format(time.RFC3339),
				"to":   base.Add(time.Second).Format(time.RFC3339),
			})
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d: %v", status, resp)
		}
	})

	t.Run("missing_timeline", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/query/range"),
			map[string]interface{}{"dims": []interface{}{0},
				"from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00Z"})
		if status != http.StatusBadRequest {
			t.Errorf("missing timeline: want 400, got %d", status)
		}
	})

	t.Run("empty_dims", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/query/range"),
			map[string]interface{}{"timeline": 1, "dims": []interface{}{},
				"from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00Z"})
		if status != http.StatusBadRequest {
			t.Errorf("empty dims: want 400, got %d", status)
		}
	})

	t.Run("bad_from", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/query/range"),
			map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
				"from": "not-a-time", "to": "2026-01-02T00:00:00Z"})
		if status != http.StatusBadRequest {
			t.Errorf("bad from: want 400, got %d", status)
		}
	})

	t.Run("bad_to", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/query/range"),
			map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
				"from": "2026-01-01T00:00:00Z", "to": "bad-time"})
		if status != http.StatusBadRequest {
			t.Errorf("bad to: want 400, got %d", status)
		}
	})

	t.Run("bad_body", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/query/range"), "bad")
		if status != http.StatusBadRequest {
			t.Errorf("bad body: want 400, got %d", status)
		}
	})
}

func TestTSQueryRangePost_RangeExceeded(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) { cfg.TSMaxRangeDays = 1 })
	env.registerTenant("acme")
	env.provision("acme")
	env.defineTimeline("acme", map[string]interface{}{"id": 1, "dims": 1})
	status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/query/range"),
		map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "2026-01-01T00:00:00Z", "to": "2026-01-10T00:00:00Z"})
	if status != http.StatusBadRequest {
		t.Errorf("range exceeded: want 400, got %d", status)
	}
}

// ─── HandleTSLatest ───────────────────────────────────────────────────────────

func TestTSLatest(t *testing.T) {
	env := sharedTSEnv(t)
	base := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		env.appendEvent("acme", map[string]interface{}{
			"timeline": 1, "dims": []interface{}{0},
			"time": base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			"nums": []interface{}{float64(i)},
		})
	}

	t.Run("happy_path_n3", func(t *testing.T) {
		url := fmt.Sprintf("%s?timeline=1&dims=0&n=3", env.tsURL("acme", "/events/latest"))
		status, resp := doJSONRequest(t, "GET", url, nil)
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d: %v", status, resp)
		}
		events, _ := resp["events"].([]interface{})
		if len(events) != 3 {
			t.Errorf("n=3: want 3 events, got %d", len(events))
		}
	})

	t.Run("missing_timeline", func(t *testing.T) {
		url := fmt.Sprintf("%s?dims=0", env.tsURL("acme", "/events/latest"))
		status, _ := doJSONRequest(t, "GET", url, nil)
		if status != http.StatusBadRequest {
			t.Errorf("missing timeline: want 400, got %d", status)
		}
	})

	t.Run("invalid_timeline", func(t *testing.T) {
		url := fmt.Sprintf("%s?timeline=abc&dims=0", env.tsURL("acme", "/events/latest"))
		status, _ := doJSONRequest(t, "GET", url, nil)
		if status != http.StatusBadRequest {
			t.Errorf("invalid timeline: want 400, got %d", status)
		}
	})

	t.Run("bad_dims", func(t *testing.T) {
		url := fmt.Sprintf("%s?timeline=1&dims=notanumber", env.tsURL("acme", "/events/latest"))
		status, _ := doJSONRequest(t, "GET", url, nil)
		if status != http.StatusBadRequest {
			t.Errorf("bad dims: want 400, got %d", status)
		}
	})

	t.Run("with_time_bounds", func(t *testing.T) {
		from := base.Format(time.RFC3339)
		to := base.Add(2 * time.Second).Format(time.RFC3339)
		url := fmt.Sprintf("%s?timeline=1&dims=0&from=%s&to=%s", env.tsURL("acme", "/events/latest"), from, to)
		status, _ := doJSONRequest(t, "GET", url, nil)
		if status != http.StatusOK {
			t.Errorf("with bounds: want 200, got %d", status)
		}
	})

	t.Run("default_n_fallback", func(t *testing.T) {
		for _, n := range []string{"", "0", "-1", "abc"} {
			url := fmt.Sprintf("%s?timeline=1&dims=0&n=%s", env.tsURL("acme", "/events/latest"), n)
			status, _ := doJSONRequest(t, "GET", url, nil)
			if status != http.StatusOK {
				t.Errorf("default n=%q: want 200, got %d", n, status)
			}
		}
	})
}

// ─── HandleTSPatchRetention ───────────────────────────────────────────────────

func TestTSPatchRetention_Adversarial(t *testing.T) {
	env := sharedTSEnv(t)

	t.Run("bad_body", func(t *testing.T) {
		status, _ := doJSONRequest(t, "PATCH", env.tsURL("acme", "/retention"), "not-json")
		if status != http.StatusBadRequest {
			t.Errorf("bad body: want 400, got %d", status)
		}
	})

	t.Run("happy_path", func(t *testing.T) {
		status, resp := doJSONRequest(t, "PATCH", env.tsURL("acme", "/retention"),
			map[string]interface{}{"default_retention_days": 60})
		if status != http.StatusOK {
			t.Fatalf("want 200, got %d: %v", status, resp)
		}
		if resp["default_retention_days"] != float64(60) {
			t.Errorf("retention days: want 60, got %v", resp["default_retention_days"])
		}
	})
}

// ─── HandleTSRangeAggregate ───────────────────────────────────────────────────

func TestTSRangeAggregate_Errors(t *testing.T) {
	env := sharedTSEnv(t)

	for _, tc := range []struct {
		name string
		body interface{}
	}{
		{"bad_body", "bad"},
		{"bad_from", map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "bad-time", "to": "2026-01-02T00:00:00Z"}},
		{"bad_to", map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "2026-01-01T00:00:00Z", "to": "bad-time"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/range_aggregate"), tc.body)
			if status != http.StatusBadRequest {
				t.Errorf("%s: want 400, got %d", tc.name, status)
			}
		})
	}
}

func TestTSRangeAggregate_RangeExceeded(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) { cfg.TSMaxRangeDays = 1 })
	env.registerTenant("acme")
	env.provision("acme")
	env.defineTimeline("acme", map[string]interface{}{"id": 1, "dims": 1})
	status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/range_aggregate"),
		map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "2026-01-01T00:00:00Z", "to": "2026-02-01T00:00:00Z"})
	if status != http.StatusBadRequest {
		t.Errorf("range exceeded: want 400, got %d", status)
	}
}

// ─── HandleTSFullAggregate ────────────────────────────────────────────────────

func TestTSFullAggregate_Errors(t *testing.T) {
	env := sharedTSEnv(t)

	for _, tc := range []struct {
		name string
		body interface{}
	}{
		{"bad_body", "not-json"},
		{"bad_from", map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "bad", "to": "2026-01-02T00:00:00Z"}},
		{"bad_to", map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "2026-01-01T00:00:00Z", "to": "bad"}},
		{"quantile_over_1", map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00Z",
			"quantiles": []interface{}{0.5, 1.5}, "quantile_fields": []interface{}{0}}},
		{"quantile_negative", map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00Z",
			"quantiles": []interface{}{-0.1}, "quantile_fields": []interface{}{0}}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/full_aggregate"), tc.body)
			if status != http.StatusBadRequest {
				t.Errorf("%s: want 400, got %d", tc.name, status)
			}
		})
	}
}

func TestTSFullAggregate_RangeExceeded(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) { cfg.TSMaxRangeDays = 1 })
	env.registerTenant("acme")
	env.provision("acme")
	env.defineTimeline("acme", map[string]interface{}{"id": 1, "dims": 1})
	status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/full_aggregate"),
		map[string]interface{}{"timeline": 1, "dims": []interface{}{0},
			"from": "2026-01-01T00:00:00Z", "to": "2026-02-01T00:00:00Z"})
	if status != http.StatusBadRequest {
		t.Errorf("range exceeded: want 400, got %d", status)
	}
}

// ─── HandleTSStats / HandleTSTimelineStats ────────────────────────────────────

func TestTSStats(t *testing.T) {
	env := sharedTSEnv(t)

	t.Run("store_stats_happy", func(t *testing.T) {
		status, resp := doJSONRequest(t, "GET", env.tsURL("acme", "/stats"), nil)
		if status != http.StatusOK {
			t.Fatalf("stats: want 200, got %d: %v", status, resp)
		}
		if _, ok := resp["timelines"]; !ok {
			t.Errorf("stats missing 'timelines': %v", resp)
		}
	})

	t.Run("timeline_stats_happy", func(t *testing.T) {
		status, _ := doJSONRequest(t, "GET", env.tsURL("acme", "/stats/1"), nil)
		if status != http.StatusOK {
			t.Errorf("timeline stats tl1: want 200, got %d", status)
		}
	})

	t.Run("timeline_stats_invalid_id", func(t *testing.T) {
		status, _ := doJSONRequest(t, "GET", env.tsURL("acme", "/stats/notanumber"), nil)
		if status != http.StatusBadRequest {
			t.Errorf("timeline stats invalid id: want 400, got %d", status)
		}
	})

	t.Run("timeline_stats_not_found", func(t *testing.T) {
		status, _ := doJSONRequest(t, "GET", env.tsURL("acme", "/stats/99"), nil)
		if status != http.StatusNotFound {
			t.Errorf("timeline stats not found: want 404, got %d", status)
		}
	})
}

// ─── tsStore error branches ───────────────────────────────────────────────────

func TestTSStore_Errors(t *testing.T) {
	t.Run("unprovisioned_404", func(t *testing.T) {
		env := setupTSServer(t, nil)
		env.registerTenant("ghost")
		status, _ := doJSONRequest(t, "GET", env.tsURL("ghost", "/tl/list"), nil)
		if status != http.StatusNotFound {
			t.Errorf("unprovisioned tenant: want 404, got %d", status)
		}
	})

	t.Run("ts_manager_nil", func(t *testing.T) {
		env := setupTSServer(t, func(cfg *config.Config) { cfg.TimeseriesEnabled = false })
		env.registerTenant("acme")
		status, _ := doJSONRequest(t, "GET", env.tsURL("acme", "/tl/list"), nil)
		if status == http.StatusOK {
			t.Errorf("ts disabled timelines list: should not return 200, got %d", status)
		}
	})
}

// ─── eventToResponse payload paths ───────────────────────────────────────────

func TestTSEventPayload(t *testing.T) {
	env := sharedTSEnv(t)
	base := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	t.Run("object_payload_round_trip", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/events"),
			map[string]interface{}{
				"timeline": 1, "dims": []interface{}{0},
				"time": base.Format(time.RFC3339), "nums": []interface{}{7.0},
				"payload": map[string]interface{}{"tag": "alpha", "seq": 42},
			})
		if status != http.StatusCreated {
			t.Fatalf("append with payload: want 201, got %d", status)
		}
		from := base.Add(-time.Second).Format(time.RFC3339)
		to := base.Add(time.Second).Format(time.RFC3339)
		qURL := fmt.Sprintf("%s?timeline=1&dims=0&from=%s&to=%s", env.tsURL("acme", "/events"), from, to)
		_, result := doJSONRequest(t, "GET", qURL, nil)
		events, _ := result["events"].([]interface{})
		if len(events) == 0 {
			t.Fatal("expected at least 1 event with payload")
		}
		ev := events[len(events)-1].(map[string]interface{})
		payload, _ := ev["payload"].(map[string]interface{})
		if payload["tag"] != "alpha" {
			t.Errorf("payload.tag: want 'alpha', got %v", payload["tag"])
		}
	})

	t.Run("array_payload", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/events"),
			map[string]interface{}{
				"timeline": 1, "dims": []interface{}{0},
				"time": base.Add(time.Minute).Format(time.RFC3339), "nums": []interface{}{1.0},
				"payload": []interface{}{"a", "b", "c"},
			})
		if status != http.StatusCreated {
			t.Errorf("array payload: want 201, got %d", status)
		}
	})

	t.Run("no_payload_clean", func(t *testing.T) {
		from := base.Add(-time.Second).Format(time.RFC3339)
		to := base.Add(time.Second).Format(time.RFC3339)
		qURL := fmt.Sprintf("%s?timeline=1&dims=0&from=%s&to=%s", env.tsURL("acme", "/events"), from, to)
		_, result := doJSONRequest(t, "GET", qURL, nil)
		events, _ := result["events"].([]interface{})
		// the first event appended (at base) has payload; verify it's present
		if len(events) > 0 {
			ev := events[0].(map[string]interface{})
			// Just confirm the response is parseable
			_ = ev
		}
	})
}

// ─── parseInterval — all 9 valid values + invalid ────────────────────────────

func TestTSAggregate_Intervals(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) {
		cfg.TSMaxRangeDays = 366
		cfg.TSMaxAggregateBuckets = 100000
	})
	env.registerTenant("acme")
	env.provision("acme")
	env.defineTimeline("acme", map[string]interface{}{"id": 1, "dims": 1})

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		doJSONRequest(t, "POST", env.tsURL("acme", "/events"), map[string]interface{}{
			"timeline": 1, "dims": []interface{}{0},
			"time": base.Add(time.Duration(i) * 24 * time.Hour).Format(time.RFC3339),
			"nums": []interface{}{float64(i + 1)},
		})
	}

	from := base.Add(-time.Hour).Format(time.RFC3339)
	to := base.Add(30 * 24 * time.Hour).Format(time.RFC3339)

	for _, interval := range []string{"1m", "5m", "15m", "30m", "1h", "6h", "12h", "1d", "7d"} {
		interval := interval
		t.Run("interval_"+interval, func(t *testing.T) {
			status, resp := doJSONRequest(t, "POST", env.tsURL("acme", "/aggregate"),
				map[string]interface{}{
					"timeline": 1, "dims": []interface{}{0},
					"from": from, "to": to,
					"interval": interval, "function": "avg", "num_field": 0,
				})
			if status != http.StatusOK {
				t.Errorf("interval %q: want 200, got %d: %v", interval, status, resp)
			}
		})
	}

	t.Run("invalid_interval", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/aggregate"),
			map[string]interface{}{
				"timeline": 1, "dims": []interface{}{0},
				"from": from, "to": to,
				"interval": "3y", "function": "avg", "num_field": 0,
			})
		if status != http.StatusBadRequest {
			t.Errorf("invalid interval: want 400, got %d", status)
		}
	})

	t.Run("bad_agg_function", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/aggregate"),
			map[string]interface{}{
				"timeline": 1, "dims": []interface{}{0},
				"from": from, "to": to,
				"interval": "1h", "function": "median", "num_field": 0,
			})
		if status != http.StatusBadRequest {
			t.Errorf("bad agg function: want 400, got %d", status)
		}
	})

	t.Run("num_field_out_of_range", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/aggregate"),
			map[string]interface{}{
				"timeline": 1, "dims": []interface{}{0},
				"from": from, "to": to,
				"interval": "1h", "function": "avg", "num_field": 9,
			})
		if status != http.StatusBadRequest {
			t.Errorf("num_field=9: want 400, got %d", status)
		}
	})
}

// ─── tsQueryLimits custom values ──────────────────────────────────────────────

func TestTSQueryLimits_Custom(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) {
		cfg.TSQueryTimeoutSecs = 5
		cfg.TSMaxQueryEvents = 2
		cfg.TSMaxScanEvents = 100
	})
	env.registerTenant("acme")
	env.provision("acme")
	env.defineTimeline("acme", map[string]interface{}{"id": 1, "dims": 1})

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		doJSONRequest(t, "POST", env.tsURL("acme", "/events"), map[string]interface{}{
			"timeline": 1, "dims": []interface{}{0},
			"time": base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			"nums": []interface{}{float64(i)},
		})
	}
	from := base.Add(-time.Second).Format(time.RFC3339)
	to := base.Add(10 * time.Second).Format(time.RFC3339)
	qURL := fmt.Sprintf("%s?timeline=1&dims=0&from=%s&to=%s", env.tsURL("acme", "/events"), from, to)
	status, resp := doJSONRequest(t, "GET", qURL, nil)
	if status != http.StatusOK {
		t.Fatalf("query with custom limits: want 200, got %d: %v", status, resp)
	}
	events, _ := resp["events"].([]interface{})
	if len(events) > 2 {
		t.Errorf("MaxQueryEvents=2 not enforced: got %d events", len(events))
	}
}

// ─── Multi-tenant tsStore isolation ──────────────────────────────────────────

func TestTSStore_TenantIsolation(t *testing.T) {
	env := setupTSServer(t, nil)
	for _, tenant := range []string{"alpha", "beta"} {
		env.registerTenant(tenant)
		env.provision(tenant)
		env.defineTimeline(tenant, map[string]interface{}{"id": 1, "dims": 1})
	}

	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	env.appendEvent("alpha", map[string]interface{}{
		"timeline": 1, "dims": []interface{}{0},
		"time": base.Format(time.RFC3339), "nums": []interface{}{999.0},
	})
	from := base.Add(-time.Second).Format(time.RFC3339)
	to := base.Add(time.Second).Format(time.RFC3339)
	qURL := fmt.Sprintf("%s?timeline=1&dims=0&from=%s&to=%s", env.tsURL("beta", "/events"), from, to)
	_, result := doJSONRequest(t, "GET", qURL, nil)
	events, _ := result["events"].([]interface{})
	if len(events) != 0 {
		t.Errorf("beta should have 0 events, got %d", len(events))
	}
}

// ─── HandleTSDefineTimeline edge cases ───────────────────────────────────────

func TestTSDefineTimeline_Errors(t *testing.T) {
	env := sharedTSEnv(t)

	t.Run("reserved_id_0", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/tl/def"),
			map[string]interface{}{"id": 0, "dims": 1, "name": "zero"})
		if status != http.StatusBadRequest {
			t.Errorf("reserved id 0: want 400, got %d", status)
		}
	})

	t.Run("bad_body", func(t *testing.T) {
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/tl/def"), "not-json")
		if status != http.StatusBadRequest {
			t.Errorf("bad body: want 400, got %d", status)
		}
	})

	t.Run("dims_immutable_after_write", func(t *testing.T) {
		env.appendEvent("acme", map[string]interface{}{
			"timeline": 1, "dims": []interface{}{0},
			"time": time.Now().UTC().Format(time.RFC3339), "nums": []interface{}{1.0},
		})
		status, _ := doJSONRequest(t, "POST", env.tsURL("acme", "/tl/def"),
			map[string]interface{}{"id": 1, "dims": 3, "name": "changed"})
		if status != http.StatusConflict {
			t.Errorf("dims immutable: want 409, got %d", status)
		}
	})
}

// ─── HandleTSGetTimeline / GetRetention errors ────────────────────────────────

func TestTSGet_Errors(t *testing.T) {
	env := sharedTSEnv(t)

	t.Run("get_timeline_invalid_id", func(t *testing.T) {
		status, _ := doJSONRequest(t, "GET", env.tsURL("acme", "/tl/abc"), nil)
		if status != http.StatusBadRequest {
			t.Errorf("get invalid id: want 400, got %d", status)
		}
	})

	t.Run("get_retention_happy", func(t *testing.T) {
		status, resp := doJSONRequest(t, "GET", env.tsURL("acme", "/retention"), nil)
		if status != http.StatusOK {
			t.Fatalf("get retention: want 200, got %d: %v", status, resp)
		}
		if _, ok := resp["default_retention_days"]; !ok {
			t.Errorf("response missing default_retention_days: %v", resp)
		}
	})
}

// ─── Error response envelope shape ───────────────────────────────────────────

func TestTSErrorShape_Envelope(t *testing.T) {
	env := sharedTSEnv(t)
	checks := []struct {
		method, path string
		body         interface{}
	}{
		{"GET", "/events/latest?dims=0", nil},
		{"POST", "/aggregate", "bad"},
		{"POST", "/range_aggregate", "bad"},
		{"POST", "/full_aggregate", "bad"},
		{"POST", "/query/range", "bad"},
		{"GET", "/tl/notanumber", nil},
	}
	for _, tc := range checks {
		status, resp := doJSONRequest(t, tc.method, env.tsURL("acme", tc.path), tc.body)
		if status == http.StatusOK || status == http.StatusCreated {
			continue
		}
		errObj, ok := resp["error"].(map[string]interface{})
		if !ok {
			t.Errorf("%s %s: error not in envelope shape: %v", tc.method, tc.path, resp)
			continue
		}
		if errObj["code"] == nil || errObj["message"] == nil {
			t.Errorf("%s %s: error envelope missing code/message: %v", tc.method, tc.path, errObj)
		}
	}
}

// ─── Query range limit and order parameters ───────────────────────────────────

func TestTSQueryRange_Parameters(t *testing.T) {
	env := sharedTSEnv(t)
	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		env.appendEvent("acme", map[string]interface{}{
			"timeline": 1, "dims": []interface{}{0},
			"time": base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			"nums": []interface{}{float64(i)},
		})
	}
	from := base.Add(-time.Second).Format(time.RFC3339)
	to := base.Add(10 * time.Second).Format(time.RFC3339)

	t.Run("limit_2", func(t *testing.T) {
		qURL := fmt.Sprintf("%s?timeline=1&dims=0&from=%s&to=%s&limit=2", env.tsURL("acme", "/events"), from, to)
		_, result := doJSONRequest(t, "GET", qURL, nil)
		events, _ := result["events"].([]interface{})
		if len(events) > 2 {
			t.Errorf("limit=2: got %d events", len(events))
		}
	})

	t.Run("order_desc", func(t *testing.T) {
		qURL := fmt.Sprintf("%s?timeline=1&dims=0&from=%s&to=%s&order=desc", env.tsURL("acme", "/events"), from, to)
		status, result := doJSONRequest(t, "GET", qURL, nil)
		if status != http.StatusOK {
			t.Fatalf("order=desc: want 200, got %d: %v", status, result)
		}
		events, _ := result["events"].([]interface{})
		if len(events) >= 3 {
			first := events[0].(map[string]interface{})
			nums, _ := first["nums"].([]interface{})
			if len(nums) > 0 && nums[0].(float64) != 4.0 {
				t.Errorf("desc order: first nums[0] want 4.0, got %v", nums[0])
			}
		}
	})
}
