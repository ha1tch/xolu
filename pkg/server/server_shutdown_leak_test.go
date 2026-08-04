// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

// T-139: Server.Stop() previously never released the per-tenant
// *bal.RollupPebble handles cached in s.balRollup (T-62's own
// long-lived-resource pattern) or s.calMgr's per-tenant Pebble-backed
// IndexStore. Every test server touching /bal leaked those file
// descriptors for the lifetime of the test BINARY process, not the
// individual test -- exactly the same shape as the tenantStores leak
// stdTestServer.cleanup()'s own comment already documents, just for a
// second, separate cache the earlier fix didn't cover. Across
// pkg/server's several hundred /bal-touching tests running in one
// process, this exceeded the environment's ulimit -n partway through
// a full run, presenting as an order-dependent, unreproducible "too
// many open files" failure.
//
// First attempt at this test tried proving the fix via Pebble lock
// semantics (reopen the same directory after Stop(), expect success
// only if the original handle was released) -- empirically unreliable:
// a same-process reopen succeeded regardless of whether Close() had
// actually run, so it discriminated nothing. This version tests the
// real failure mode directly: open+Stop() a server touching /bal
// repeatedly, and confirm the process's own open-file-descriptor count
// does NOT grow with iteration count. Uses /dev/fd (present on both
// Linux and macOS, unlike /proc/self/fd) rather than counting file
// descriptors platform-specifically via syscalls.
func openFDCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Skipf("cannot read /dev/fd on this platform: %v", err)
	}
	return len(entries)
}

func TestServer_Stop_DoesNotLeakBalRollupFileDescriptors(t *testing.T) {
	const iterations = 30

	runOnce := func() {
		tmpDir, err := os.MkdirTemp("", "xolu-t139-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		cfg := &config.Config{
			Host: "localhost", Port: 0,
			BaseDir:             tmpDir,
			Schema:              "test_schema",
			CacheType:           "memory",
			CacheTTL:            300,
			TenantMode:          "path",
			TenantAutoRegister:  true,
			APIV2Enabled:        true,
			BalEnabled:          true,
			MaxEntitySize:       1048576,
			MaxCascadeDeletions: 100,
		}
		store, err := storage.NewStore("sqlite", map[string]interface{}{
			"db_path": tmpDir + "/test.db",
		})
		if err != nil {
			t.Fatal(err)
		}
		memCache := cache.NewMemoryCache(1000, time.Duration(cfg.CacheTTL)*time.Second)
		g := graph.NewFlatGraph()
		validator := validation.NewJSONSchemaValidator(tmpDir + "/test_schema/_schemas")
		logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

		srv := server.New(cfg, store, memCache, g, validator, logger)
		ts := httptest.NewServer(srv.Handler())

		body, _ := json.Marshal(map[string]interface{}{
			"account_id": "acct0", "unit": "usd", "scale": 2,
		})
		resp, err := http.Post(ts.URL+"/api/v2/tenant/default/bal/def", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("bal/def request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("bal/def setup: want 201, got %d", resp.StatusCode)
		}

		ts.Close()
		srv.Stop()
		_ = store.Close()
	}

	// Warm up one cycle first: the first iteration in any process opens
	// shared static resources (schema validators, sqlite driver
	// registration internals) that legitimately persist across
	// iterations and would otherwise register as false leak signal.
	runOnce()
	baseline := openFDCount(t)

	for i := 0; i < iterations; i++ {
		runOnce()
	}
	after := openFDCount(t)

	// Allow a small fixed slack (httptest/net teardown timing, GC not
	// having run yet) rather than requiring an exact match -- the
	// pre-fix leak grows by roughly one Pebble instance's worth of FDs
	// (typically single digits to low tens) PER iteration, so a fixed
	// slack of iterations/2 cleanly separates "no leak" from "leaking"
	// without the test being sensitive to minor per-run FD wobble.
	slack := iterations / 2
	if after > baseline+slack {
		t.Fatalf("open FD count grew from %d to %d over %d iterations "+
			"(allowed slack %d) -- Server.Stop() is leaking file descriptors, "+
			"most likely the bal rollup Pebble handle or cal manager index store",
			baseline, after, iterations, slack)
	}
}
