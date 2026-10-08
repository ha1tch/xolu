// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// commit_templates_test.go
//
// XOT234 and XOT235 over HTTP, on the default store with /api/v2/ts (XOT239).

import (
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

func templatesEnv(t *testing.T, strict bool) *tsEnv {
	env := setupTSServer(t, func(cfg *config.Config) {
		cfg.TenantMode = "path"
		cfg.APIV2Enabled = true
		cfg.StrictCommit = strict
	})
	if st, body := env.do("POST", env.ts.URL+"/api/v2/ts/provision", nil); st != http.StatusCreated {
		t.Fatalf("provision: %d %v", st, body)
	}
	if st, body := env.do("POST", env.ts.URL+"/api/v2/ts/tl/def", map[string]interface{}{"id": 7, "name": "idx", "dims": 2, "retention_days": -1}); st != http.StatusCreated {
		t.Fatalf("define: %d %v", st, body)
	}
	return env
}

func TestCommitTemplatesE2E_OneRequestOneInstant(t *testing.T) {
	env := templatesEnv(t, false)
	base := env.ts.URL
	env.do("POST", base+"/api/v1/assets/save/8201", map[string]interface{}{"state": "A"})
	_, asset := env.do("GET", base+"/api/v1/assets/8201", nil)
	ver := int(asset["_version"].(float64))

	st, body := env.do("POST", base+"/api/v1/commit", map[string]interface{}{
		"templates": true,
		"update":    map[string]interface{}{"entity": "assets", "id": 8201, "version": ver, "data": map[string]interface{}{"state": "B"}},
		"append": []interface{}{
			map[string]interface{}{"entity": "hist", "id": 8201002, "on_conflict": "ignore", "data": map[string]interface{}{
				"entity_id": 8201, "version": "$update.version", "snapshot": "$update.data", "saved_at": "$now"}},
		},
		"timeseries": []interface{}{
			map[string]interface{}{"timeline": 7, "dims": []uint64{8201, uint64(ver) + 1}, "time": "$now", "nums": []float64{0}},
		},
	})
	if st != http.StatusOK {
		t.Fatalf("commit: %d %v", st, body)
	}
	clock, _ := body["clock"].(map[string]interface{})
	now, _ := clock["now"].(string)
	if len(now) != 30 || body["ts_accepted"] != float64(1) {
		t.Fatalf("response = %v, want a clock with a fixed-width instant and one ts event", body)
	}
	_, row := env.do("GET", base+"/api/v1/hist/8201002", nil)
	if row["saved_at"] != now || int(row["version"].(float64)) != ver+1 {
		t.Fatalf("history row = %v, want saved_at %s and version %d", row, now, ver+1)
	}
	if snap, _ := row["snapshot"].(map[string]interface{}); snap["state"] != "B" {
		t.Fatalf("snapshot = %v", row["snapshot"])
	}
	// The event carries the same instant as the row: one instant for the whole request.
	st, evs := env.do("GET", base+"/api/v2/ts/events/latest?timeline=7&dims=8201&n=5", nil)
	list, _ := evs["events"].([]interface{})
	if st != http.StatusOK || len(list) != 1 {
		t.Fatalf("latest: %d %v", st, evs)
	}
	got, _ := time.Parse(time.RFC3339Nano, list[0].(map[string]interface{})["time"].(string))
	want, _ := time.Parse(time.RFC3339Nano, now)
	if !got.Equal(want) {
		t.Fatalf("event time %s differs from the row's saved_at %s", got, want)
	}

	// Sending the same commit again: the history row exists, on_conflict skips it, and the
	// stale version is what refuses the commit.
	st, body = env.do("POST", base+"/api/v1/commit", map[string]interface{}{
		"templates": true,
		"update":    map[string]interface{}{"entity": "assets", "id": 8201, "version": ver, "data": map[string]interface{}{"state": "B"}},
		"append":    []interface{}{map[string]interface{}{"entity": "hist", "id": 8201002, "on_conflict": "ignore", "data": map[string]interface{}{"saved_at": "$now"}}},
	})
	if st != http.StatusConflict {
		t.Fatalf("repeat with a stale version: %d %v, want 409", st, body)
	}
}

func TestCommitTemplatesE2E_InstantsIncrease(t *testing.T) {
	env := templatesEnv(t, false)
	prev := ""
	for i := 0; i < 20; i++ {
		st, body := env.do("POST", env.ts.URL+"/api/v1/commit", map[string]interface{}{
			"templates": true,
			"update":    map[string]interface{}{"entity": "assets", "id": 8300, "data": map[string]interface{}{"i": i}},
			"append":    []interface{}{map[string]interface{}{"entity": "hist", "data": map[string]interface{}{"at": "$now"}}},
		})
		if st != http.StatusOK {
			t.Fatalf("commit %d: %d %v", i, st, body)
		}
		now := body["clock"].(map[string]interface{})["now"].(string)
		if now <= prev {
			t.Fatalf("commit %d got %s, not after %s", i, now, prev)
		}
		prev = now
	}
}

func TestCommitTemplatesE2E_OffAndMistyped(t *testing.T) {
	env := templatesEnv(t, false)
	base := env.ts.URL
	row := func(v interface{}) []interface{} {
		return []interface{}{map[string]interface{}{"entity": "hist", "id": 8401, "data": map[string]interface{}{"v": v}}}
	}
	upd := map[string]interface{}{"entity": "assets", "id": 8400, "data": map[string]interface{}{"a": 1}}

	// Without "templates" the strings are data.
	if st, body := env.do("POST", base+"/api/v1/commit", map[string]interface{}{"update": upd, "append": row("$now")}); st != http.StatusOK {
		t.Fatalf("literal $now: %d %v", st, body)
	}
	if _, r := env.do("GET", base+"/api/v1/hist/8401", nil); r["v"] != "$now" {
		t.Fatalf("stored %v, want the literal string", r["v"])
	}
	// "$now" as a timeseries time needs the flag.
	st, body := env.do("POST", base+"/api/v1/commit", map[string]interface{}{"update": upd,
		"timeseries": []interface{}{map[string]interface{}{"timeline": 7, "dims": []uint64{1, 1}, "time": "$now", "nums": []float64{0}}}})
	if st != http.StatusBadRequest || tsCode(body) != "XOLU-CM019" {
		t.Fatalf("time $now without templates: %d %v, want 400 XOLU-CM019", st, body)
	}
	// A typo is a 400 before anything is written.
	st, body = env.do("POST", base+"/api/v1/commit", map[string]interface{}{"templates": true, "update": upd, "append": row("$updat.version")})
	if st != http.StatusBadRequest || tsCode(body) != "XOLU-CM019" {
		t.Fatalf("typo: %d %v, want 400 XOLU-CM019", st, body)
	}
	// A template that is only unavailable inside the transaction is a 400 and rolls back.
	st, body = env.do("POST", base+"/api/v1/commit", map[string]interface{}{"templates": true,
		"update": map[string]interface{}{"entity": "assets", "id": 8402, "data": map[string]interface{}{"a": "$update.version"}},
		"append": row("x")})
	if st != http.StatusBadRequest || tsCode(body) != "XOLU-CM019" {
		t.Fatalf("$update.version inside update.data: %d %v, want 400 XOLU-CM019", st, body)
	}
	if st, _ := env.do("GET", base+"/api/v1/assets/8402", nil); st != http.StatusNotFound {
		t.Errorf("the entity of a refused commit exists: %d", st)
	}
}

// Strict commit validates against the schema; a template must not make a valid
// request look invalid, and a real violation must still be caught.
func TestCommitTemplatesE2E_StrictValidationSeesTypedPlaceholders(t *testing.T) {
	env := templatesEnv(t, true)
	base := env.ts.URL
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entity_id": map[string]interface{}{"type": "integer"},
			"version":   map[string]interface{}{"type": "integer"},
			"snapshot":  map[string]interface{}{"type": "object"},
			"saved_at":  map[string]interface{}{"type": "string"},
			"seq":       map[string]interface{}{"type": "integer"},
		},
		"required": []interface{}{"entity_id", "version", "snapshot"},
	}
	if st, body := env.do("POST", base+"/api/v1/schema/hist", schema); st != http.StatusOK && st != http.StatusCreated {
		t.Fatalf("schema: %d %v", st, body)
	}
	commit := func(id int, data map[string]interface{}) (int, map[string]interface{}) {
		return env.do("POST", base+"/api/v1/commit", map[string]interface{}{
			"templates": true,
			"update":    map[string]interface{}{"entity": "assets", "id": 8500, "data": map[string]interface{}{"state": "B"}},
			"append":    []interface{}{map[string]interface{}{"entity": "hist", "id": id, "data": data}},
		})
	}
	if st, body := commit(1, map[string]interface{}{"entity_id": 8500, "version": "$update.version", "snapshot": "$update.data", "saved_at": "$now", "seq": "$commit_seq"}); st != http.StatusOK {
		t.Fatalf("a valid templated row under strict commit: %d %v", st, body)
	}
	// A required field still missing, and a template of the wrong type, are still refused.
	if st, _ := commit(2, map[string]interface{}{"version": "$update.version", "snapshot": "$update.data"}); st != http.StatusBadRequest {
		t.Errorf("a row without its required entity_id: %d, want 400", st)
	}
	if st, _ := commit(3, map[string]interface{}{"entity_id": 8500, "version": "$update.version", "snapshot": "$now"}); st != http.StatusBadRequest {
		t.Errorf("$now where an object is required: %d, want 400", st)
	}
}
