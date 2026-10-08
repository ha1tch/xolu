// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// ts_default_store_test.go
//
// XOT239: /ts is served for the default store (tenant 0) at /api/v2/ts, so a
// client that keeps its data on the ordinary routes can index it in /ts and
// write the events in the same /commit, without moving to tenant routes.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

func pathModeV2(cfg *config.Config) {
	cfg.TenantMode = "path"
	cfg.APIV2Enabled = true
}

func TestTSDefaultStore_ProvisionDefineCommitQuery(t *testing.T) {
	env := setupTSServer(t, pathModeV2)
	base := env.ts.URL
	v2 := func(p string) string { return base + "/api/v2/ts" + p }

	// Not provisioned yet: the same answer a tenant gets.
	if st, body := env.do("GET", v2("/tl/list"), nil); st != http.StatusNotFound || tsCode(body) != "XOLU-TS003" {
		t.Fatalf("before provisioning: %d %v, want 404 XOLU-TS003", st, body)
	}
	if st, body := env.do("POST", v2("/provision"), nil); st != http.StatusCreated || body["tenant_id"] != "_default" {
		t.Fatalf("provision: %d %v", st, body)
	}
	if st, body := env.do("POST", v2("/tl/def"), map[string]interface{}{"id": 7, "name": "idx", "dims": 2, "retention_days": -1}); st != http.StatusCreated {
		t.Fatalf("define timeline: %d %v", st, body)
	}

	// An entity on the ordinary routes and a /ts event, in one commit.
	st, body := env.do("POST", base+"/api/v1/commit", map[string]interface{}{
		"update": map[string]interface{}{"entity": "asset", "id": 51, "data": map[string]interface{}{"state": "A"}},
		"timeseries": []interface{}{
			map[string]interface{}{"timeline": 7, "dims": []uint64{51, 1}, "time": "2026-01-01T00:00:00Z", "nums": []float64{1}},
		},
	})
	if st != http.StatusOK || body["ts_accepted"] != float64(1) {
		t.Fatalf("commit with a timeseries event on the default store: %d %v", st, body)
	}
	if st, _ := env.do("GET", base+"/api/v1/asset/51", nil); st != http.StatusOK {
		t.Fatalf("the entity is not on the ordinary routes: %d", st)
	}
	st, body = env.do("GET", v2("/events/latest")+"?timeline=7&dims=51&n=5", nil)
	if evs, _ := body["events"].([]interface{}); st != http.StatusOK || len(evs) != 1 {
		t.Fatalf("latest on the default store: %d %v, want 1 event", st, body)
	}

	// A refused commit leaves no event behind: the Pebble write is undone.
	st, body = env.do("POST", base+"/api/v1/commit", map[string]interface{}{
		"update": map[string]interface{}{"entity": "asset", "id": 51, "version": 99, "data": map[string]interface{}{"state": "B"}},
		"timeseries": []interface{}{
			map[string]interface{}{"timeline": 7, "dims": []uint64{51, 2}, "time": "2026-01-02T00:00:00Z", "nums": []float64{2}},
		},
	})
	if st != http.StatusConflict {
		t.Fatalf("stale commit: %d %v, want 409", st, body)
	}
	_, body = env.do("GET", v2("/events/latest")+"?timeline=7&dims=51&n=5", nil)
	if evs, _ := body["events"].([]interface{}); len(evs) != 1 {
		t.Fatalf("a refused commit left its event: %v", body)
	}

	// The cursor, the exact-key delete and the definition rule work here too.
	if st, body := env.do("DELETE", v2("/events"), map[string]interface{}{"timeline": 7, "dims": []uint64{51, 1}, "time": "2026-01-01T00:00:00Z"}); st != http.StatusOK {
		t.Fatalf("exact-key delete on the default store: %d %v", st, body)
	}
	if st, body := env.do("POST", v2("/tl/def"), map[string]interface{}{"id": 7, "name": "idx", "dims": 2, "retention_days": 30}); st != http.StatusConflict {
		t.Fatalf("re-definition with another retention: %d %v, want 409", st, body)
	}
}

// The default store's /ts and a tenant's are separate stores.
func TestTSDefaultStore_IsSeparateFromTenants(t *testing.T) {
	env := setupTSServer(t, pathModeV2)
	base := env.ts.URL
	env.registerTenant("acme")
	env.provision("acme")
	env.defineTimeline("acme", map[string]interface{}{"id": 7, "name": "idx", "dims": 1, "retention_days": -1})
	env.appendEvent("acme", map[string]interface{}{"timeline": 7, "dims": []uint64{1}, "time": "2026-01-01T00:00:00Z", "nums": []float64{1}})

	if st, _ := env.do("POST", base+"/api/v2/ts/provision", nil); st != http.StatusCreated {
		t.Fatalf("provision the default store: %d", st)
	}
	// Timeline 7 does not exist in the default store, whatever acme defined.
	st, body := env.do("GET", base+"/api/v2/ts/events/latest?timeline=7&dims=1&n=5", nil)
	if st == http.StatusOK {
		t.Fatalf("the default store answered for acme's timeline: %v", body)
	}
	if st, body := env.do("GET", env.tsURL("acme", "/events/latest")+"?timeline=7&dims=1&n=5", nil); st != http.StatusOK {
		t.Fatalf("acme's own events: %d %v", st, body)
	}
}

// Strict tenancy disables every unscoped route, /ts included.
func TestTSDefaultStore_AbsentInStrictTenancy(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) {
		cfg.TenantMode = "strict"
		cfg.APIV2Enabled = true
	})
	for _, p := range []string{"/provision", "/tl/list"} {
		method := "GET"
		if p == "/provision" {
			method = "POST"
		}
		if st, _ := env.do(method, fmt.Sprintf("%s/api/v2/ts%s", env.ts.URL, p), nil); st != http.StatusNotFound && st != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/v2/ts%s in strict mode: %d, want 404", method, p, st)
		}
	}
	// The tenant route still resolves.
	env.registerTenant("acme")
	env.provision("acme")
}

// Without APIV2Enabled there is no /api/v2, and the tenant routes are unchanged.
func TestTSDefaultStore_NeedsV2(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) { cfg.TenantMode = "path" })
	if st, _ := env.do("POST", env.ts.URL+"/api/v2/ts/provision", nil); st != http.StatusNotFound {
		t.Fatalf("/api/v2/ts/provision without v2: %d, want 404", st)
	}
	env.registerTenant("acme")
	env.provision("acme")
}
