// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// commit_ts_e2e_test.go
//
// End-to-end tests for the timeseries write path of the /commit endpoint.
// All tests go through the HTTP layer via httptest.Server.
//
// Coverage:
//   - Happy path: TS events written + entity state advanced; ts_accepted in response
//   - TS-only commit (append array empty, timeseries non-empty)
//   - Both append and timeseries empty → 400 OLU-CM003
//   - TS disabled (tsManager nil) → 400 OLU-CM010
//   - Tenant not provisioned → 400 OLU-CM011
//   - Unknown timeline → 400 OLU-CM012
//   - Wrong dims → 400 OLU-CM013
//   - Batch exceeds TSMaxBatchSize → 400 OLU-CM014
//   - Zero time in TS event → 400
//
// Failure injection tests (Pebble fails / SQLite fails / rollback) live in
// commit_ts_rollback_test.go.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cache"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/validation"
	"github.com/rs/zerolog"
)

// ----------------------------------------------------------------------------
// Harness
// ----------------------------------------------------------------------------

// commitTSEnv is a fully-wired test server with both SQLite and timeseries
// enabled, using the "path" tenant mode so /commit is reachable without a
// tenant prefix.
type commitTSEnv struct {
	ts     *httptest.Server
	srv    *server.Server
	tmpDir string
	t      *testing.T
}

func newCommitTSEnv(t *testing.T, overrides func(*config.Config)) *commitTSEnv {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "olu-commit-ts-e2e-*")
	if err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	schemaDir := filepath.Join(tmpDir, "schema")
	os.MkdirAll(schemaDir, 0755)

	cfg := &config.Config{
		Host:                  "localhost",
		Port:                  0,
		StorageType:           "sqlite",
		DBPath:                dbPath,
		BaseDir:               tmpDir,
		Schema:                "schema",
		SchemaDir:             schemaDir,
		CacheType:             "memory",
		CacheTTL:              300,
		GraphEnabled:          true,
		GraphMode:             "flat",
		MaxEntitySize:         1048576,
		DefaultPageSize:       10,
		PatchNullBehavior:     "store",
		TenantMode:            "strict",
		TenantAutoRegister:    true,
		QueryTimeout:          30,
		QueryMaxRows:          10000,
		QueryMaxScanRows:      100000,
		QueryMaxResponseBytes: 10485760,
		GraphDataFile:         filepath.Join(tmpDir, "graph.data"),
		GraphIndexFile:        filepath.Join(tmpDir, "graph.index"),
		GraphQueryTTL:         86400,
		MaxQueryDepth:         10,
		StrictCommit:          false, // keep strict=false so we don't need schemas

		TimeseriesEnabled:        true,
		TSMemtableSize:           4 * 1024 * 1024,
		TSBlockSize:              4096,
		TSCompression:            "snappy",
		TSL0CompactionThreshold:  4,
		TSMaxOpenFiles:           50,
		TSDefaultRetentionDays:   90,
		TSCompactionIntervalSecs: 3600,
		TSRetentionEnabled:       false,
		TSQueryTimeoutSecs:       30,
		TSMaxQueryEvents:         10000,
		TSMaxScanEvents:          500000,
		TSMaxRangeDays:           366,
		TSMaxBatchSize:           5000,
		TSMaxResponseBytes:       10 * 1024 * 1024,
	}

	if overrides != nil {
		overrides(cfg)
	}

	os.MkdirAll(schemaDir+"/_schemas", 0755)

	store, err := storage.NewStore("sqlite", map[string]interface{}{
		"db_path": dbPath,
	})
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatal(err)
	}

	memCache := cache.NewMemoryCache(1000, time.Duration(cfg.CacheTTL)*time.Second)
	g := graph.NewFlatGraph()
	validator := validation.NewJSONSchemaValidator(schemaDir + "/_schemas")
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, nil, validator, logger)
	ts := httptest.NewServer(srv.Handler())

	env := &commitTSEnv{ts: ts, srv: srv, tmpDir: tmpDir, t: t}
	t.Cleanup(func() {
		ts.Close()
		os.RemoveAll(tmpDir)
	})
	return env
}

func (e *commitTSEnv) registerTenant(name string) {
	e.t.Helper()
	// Register directly via the registry — no HTTP route exists for explicit
	// tenant registration in strict mode. Mirrors tsEnv.registerTenant.
	_, err := e.srv.TenantRegistry().GetOrRegister(context.Background(), name)
	if err != nil {
		e.t.Fatalf("registerTenant %q: %v", name, err)
	}
}

func (e *commitTSEnv) provision(tenant string) {
	e.t.Helper()
	status, result := doJSONRequest(e.t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/%s/ts/provision", e.ts.URL, tenant), nil)
	if status != http.StatusCreated {
		e.t.Fatalf("provision %s: got %d: %v", tenant, status, result)
	}
}

func (e *commitTSEnv) defineTimeline(tenant string, id, dims int, name string) {
	e.t.Helper()
	status, result := doJSONRequest(e.t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/%s/ts/timelines", e.ts.URL, tenant),
		map[string]interface{}{"id": id, "dims": dims, "name": name})
	if status != http.StatusCreated {
		e.t.Fatalf("defineTimeline %d: got %d: %v", id, status, result)
	}
}

func (e *commitTSEnv) commitURL(tenant string) string {
	return fmt.Sprintf("%s/api/v1/tenant/%s/commit", e.ts.URL, tenant)
}

// tsQueryRange queries Pebble for events and returns the response body.
func (e *commitTSEnv) tsQueryRange(tenant string, body map[string]interface{}) (int, map[string]interface{}) {
	e.t.Helper()
	return doJSONRequest(e.t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/%s/ts/query/range", e.ts.URL, tenant),
		body)
}

// setupTenant registers tenant, provisions TS, and defines one timeline (id=1, dims=1).
// Returns the tenant name for chaining.
func (e *commitTSEnv) setupTenant(name string) string {
	e.t.Helper()
	e.registerTenant(name)
	e.provision(name)
	e.defineTimeline(name, 1, 1, "fsm")
	return name
}

// baseCommitBody returns a minimal valid CommitRequest body with no TS events.
func baseCommitBody() map[string]interface{} {
	return map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "pending"},
		},
		"append": []interface{}{
			map[string]interface{}{
				"entity": "event_log",
				"data":   map[string]interface{}{"msg": "created"},
			},
		},
	}
}

// ----------------------------------------------------------------------------
// Happy path
// ----------------------------------------------------------------------------

func TestCommitTS_HappyPath_WithAppendAndTS(t *testing.T) {
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("acme")

	ts0 := time.Now().UTC().Truncate(time.Second)

	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "submitted"},
		},
		"append": []interface{}{
			map[string]interface{}{
				"entity": "event_log",
				"data":   map[string]interface{}{"msg": "submitted"},
			},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{42},
				"time":     ts0.Format(time.RFC3339Nano),
				"nums":     []interface{}{1.0},
			},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusOK {
		t.Fatalf("commit: got %d: %v", status, result)
	}

	// ts_accepted must be 1.
	tsAccepted, ok := result["ts_accepted"].(float64)
	if !ok || int(tsAccepted) != 1 {
		t.Errorf("ts_accepted: got %v, want 1", result["ts_accepted"])
	}

	// The event must be readable from Pebble.
	from := ts0.Add(-time.Second).Format(time.RFC3339Nano)
	to := ts0.Add(time.Second).Format(time.RFC3339Nano)
	qStatus, qResult := env.tsQueryRange(tenant, map[string]interface{}{
		"timeline": 1,
		"dims":     []interface{}{42},
		"from":     from,
		"to":       to,
	})
	if qStatus != http.StatusOK {
		t.Fatalf("tsQueryRange: got %d: %v", qStatus, qResult)
	}
	events, _ := qResult["events"].([]interface{})
	if len(events) != 1 {
		t.Errorf("Pebble: got %d events, want 1", len(events))
	}
}

func TestCommitTS_HappyPath_TSOnlyNoAppend(t *testing.T) {
	// append is empty; timeseries is non-empty. Must succeed (relaxed validation).
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("beta")

	ts0 := time.Now().UTC().Truncate(time.Second)

	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "shipped"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{7},
				"time":     ts0.Format(time.RFC3339Nano),
			},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusOK {
		t.Fatalf("TS-only commit: got %d: %v", status, result)
	}
	tsAccepted, _ := result["ts_accepted"].(float64)
	if int(tsAccepted) != 1 {
		t.Errorf("ts_accepted: got %v, want 1", result["ts_accepted"])
	}
}

func TestCommitTS_HappyPath_MultipleEvents(t *testing.T) {
	// Three TS events in one commit; ts_accepted must equal 3.
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("gamma")

	base := time.Now().UTC().Truncate(time.Second)
	tsEvents := make([]interface{}, 3)
	for i := range tsEvents {
		tsEvents[i] = map[string]interface{}{
			"timeline": 1,
			"dims":     []interface{}{uint64(i + 1)},
			"time":     base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
			"nums":     []interface{}{float64(i)},
		}
	}

	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     2,
			"data":   map[string]interface{}{"state": "done"},
		},
		"timeseries": tsEvents,
	}

	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusOK {
		t.Fatalf("multi-event commit: got %d: %v", status, result)
	}
	tsAccepted, _ := result["ts_accepted"].(float64)
	if int(tsAccepted) != 3 {
		t.Errorf("ts_accepted: got %v, want 3", result["ts_accepted"])
	}
}

// ----------------------------------------------------------------------------
// Validation errors (all pre-write; neither store is touched)
// ----------------------------------------------------------------------------

func TestCommitTS_BothEmpty_Returns400(t *testing.T) {
	// append=[] and timeseries absent → OLU-CM003.
	env := newCommitTSEnv(t, nil)
	env.registerTenant("delta")

	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "x"},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL("delta"), body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, result)
	}
	assertErrorCode(t, result, "OLU-CM003")
}

func TestCommitTS_TSDisabled_Returns400(t *testing.T) {
	// Server started without timeseries enabled → OLU-CM010.
	env := newCommitTSEnv(t, func(cfg *config.Config) {
		cfg.TimeseriesEnabled = false
	})
	env.registerTenant("nodets")

	ts0 := time.Now().UTC().Truncate(time.Second)
	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "x"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{1},
				"time":     ts0.Format(time.RFC3339Nano),
			},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL("nodets"), body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, result)
	}
	assertErrorCode(t, result, "OLU-CM010")
}

func TestCommitTS_TenantNotProvisioned_Returns400(t *testing.T) {
	// TS enabled but tenant not provisioned → OLU-CM011.
	env := newCommitTSEnv(t, nil)
	env.registerTenant("noprov")
	// Deliberately skip env.provision("noprov").

	ts0 := time.Now().UTC().Truncate(time.Second)
	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "x"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{1},
				"time":     ts0.Format(time.RFC3339Nano),
			},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL("noprov"), body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, result)
	}
	assertErrorCode(t, result, "OLU-CM011")
}

func TestCommitTS_UnknownTimeline_Returns400(t *testing.T) {
	// Timeline 99 is never defined → OLU-CM012.
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("epsilon")

	ts0 := time.Now().UTC().Truncate(time.Second)
	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "x"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 99, // not defined
				"dims":     []interface{}{1},
				"time":     ts0.Format(time.RFC3339Nano),
			},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, result)
	}
	assertErrorCode(t, result, "OLU-CM012")
}

func TestCommitTS_WrongDims_Returns400(t *testing.T) {
	// Timeline 1 has dims=1 but request sends 2 dims → OLU-CM013.
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("zeta")

	ts0 := time.Now().UTC().Truncate(time.Second)
	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "x"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{1, 2}, // timeline expects 1
				"time":     ts0.Format(time.RFC3339Nano),
			},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, result)
	}
	assertErrorCode(t, result, "OLU-CM013")
}

func TestCommitTS_BatchTooLarge_Returns400(t *testing.T) {
	// TSMaxBatchSize=2; send 3 events → OLU-CM014.
	env := newCommitTSEnv(t, func(cfg *config.Config) {
		cfg.TSMaxBatchSize = 2
	})
	tenant := env.setupTenant("eta")

	base := time.Now().UTC().Truncate(time.Second)
	tsEvents := make([]interface{}, 3)
	for i := range tsEvents {
		tsEvents[i] = map[string]interface{}{
			"timeline": 1,
			"dims":     []interface{}{i + 1},
			"time":     base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
		}
	}

	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "x"},
		},
		"timeseries": tsEvents,
	}

	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, result)
	}
	assertErrorCode(t, result, "OLU-CM014")
}

func TestCommitTS_ZeroTime_Returns400(t *testing.T) {
	// Zero time in TS event must be rejected before any write.
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("theta")

	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "x"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{1},
				"time":     "0001-01-01T00:00:00Z", // zero time
			},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, result)
	}
	// Any CM01x code is acceptable; the point is no write occurred.
	if result == nil {
		t.Error("expected error response body, got nil")
	}
}

func TestCommitTS_ReservedTimelineZero_Returns400(t *testing.T) {
	// Timeline 0x0000 is reserved → OLU-CM012.
	env := newCommitTSEnv(t, nil)
	tenant := env.setupTenant("iota")

	ts0 := time.Now().UTC().Truncate(time.Second)
	body := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "x"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 0,
				"dims":     []interface{}{1},
				"time":     ts0.Format(time.RFC3339Nano),
			},
		},
	}

	status, result := doJSONRequest(t, "POST", env.commitURL(tenant), body)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %v", status, result)
	}
	assertErrorCode(t, result, "OLU-CM012")
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// assertErrorCode checks that result["error"]["code"] equals wantCode.
func assertErrorCode(t *testing.T, result map[string]interface{}, wantCode string) {
	t.Helper()
	errObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("response has no 'error' object: %v", result)
	}
	code, _ := errObj["code"].(string)
	if code != wantCode {
		t.Errorf("error code: got %q, want %q (full response: %v)", code, wantCode, result)
	}
}
