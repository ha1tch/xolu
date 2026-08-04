// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// commit_ts_cm016_test.go
//
// Tests for the XOLU-CM016 double-failure path: Pebble write succeeds, SQLite
// transaction fails, AND the subsequent DeleteKeys tombstone call also fails.
//
// This requires a timeseries.Manager whose StoreFor returns a Store that
// delegates AppendBatch to real Pebble (so the write actually lands) but
// returns a sentinel error from DeleteKeys (so the rollback appears to fail).
//
// The server.Server.tsManager field is now typed as timeseries.Manager
// (interface), which allows us to substitute a failingManager via
// server.WithTSManager. The production code path is unchanged.

import (
	"context"
	"errors"
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
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/ha1tch/xolu/pkg/timeseries"
	"github.com/ha1tch/xolu/pkg/validation"
	"github.com/rs/zerolog"
)

// ----------------------------------------------------------------------------
// Fakes
// ----------------------------------------------------------------------------

// errDeleteKeys is the sentinel returned by failingStore.DeleteKeys.
var errDeleteKeys = errors.New("injected DeleteKeys failure")

// failingStore wraps a real timeseries.Store and overrides DeleteKeys to
// return errDeleteKeys. All other methods delegate to the underlying store,
// so AppendBatch genuinely writes to Pebble.
type failingStore struct {
	timeseries.Store
}

func (f *failingStore) DeleteKeys(_ context.Context, _ [][]byte) error {
	return errDeleteKeys
}

// failingManager wraps a real timeseries.Manager and returns a failingStore
// from StoreFor. Provision, IsProvisioned, and Close delegate to the real
// manager so tenant setup works normally.
type failingManager struct {
	real timeseries.Manager
}

func (m *failingManager) Provision(ctx context.Context, tenantID tenant.TenantID, tenantName string) error {
	return m.real.Provision(ctx, tenantID, tenantName)
}

func (m *failingManager) IsProvisioned(tenantID tenant.TenantID) bool {
	return m.real.IsProvisioned(tenantID)
}

func (m *failingManager) StoreFor(tenantID tenant.TenantID) (timeseries.Store, error) {
	real, err := m.real.StoreFor(tenantID)
	if err != nil {
		return nil, err
	}
	return &failingStore{real}, nil
}

func (m *failingManager) Close() error {
	return m.real.Close()
}

// ----------------------------------------------------------------------------
// Harness
// ----------------------------------------------------------------------------

// newCM016Env builds a server where the real tsManager is replaced with a
// failingManager. The real manager is constructed first (so Pebble stores
// actually exist on disk), then wrapped.
func newCM016Env(t *testing.T) *commitTSEnv {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "xolu-cm016-*")
	if err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	schemaDir := filepath.Join(tmpDir, "schema")
	os.MkdirAll(schemaDir+"/_schemas", 0755)

	cfg := &config.Config{
		Host:                  "localhost",
		Port:                  0,
		StorageType:           "sqlite",
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
		AsyncJobRetentionTTL:  86400,
		MaxQueryDepth:         10,
		StrictCommit:          false,

		// Timeseries enabled so the real manager is wired up during New().
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

	entityStore, err := storage.NewStore("sqlite", map[string]interface{}{
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

	// Build the server normally — this wires the real tsManager.
	srv := server.New(cfg, entityStore, memCache, g, validator, logger)

	// Wrap the real manager with our failing wrapper.
	srv.SetTSManager(&failingManager{real: srv.TSManager()})

	ts := httptest.NewServer(srv.Handler())
	env := &commitTSEnv{ts: ts, srv: srv, tmpDir: tmpDir, t: t}
	t.Cleanup(func() {
		ts.Close()
		os.RemoveAll(tmpDir)
	})
	return env
}

// ----------------------------------------------------------------------------
// Test
// ----------------------------------------------------------------------------

// TestCommitTS_CM016_DeleteKeysFails verifies that when the Pebble write
// succeeds, the SQLite transaction fails (via stale CAS version), and
// DeleteKeys also fails, the handler returns 500 XOLU-CM016 rather than the
// normal conflict or rollback-success response.
func TestCommitTS_CM016_DeleteKeysFails(t *testing.T) {
	env := newCM016Env(t)

	// Register and provision tenant using the real Pebble path.
	tenant := "cm016-tenant"
	env.registerTenant(tenant)
	env.provision(tenant)
	env.defineTimeline(tenant, 1, 1, "fsm")

	// Establish entity at version >= 1 so CAS can be made stale.
	initBody := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "order",
			"id":     1,
			"data":   map[string]interface{}{"state": "init"},
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

	// Now issue a commit with TS events and a stale version.
	// AppendBatch → real Pebble (succeeds via delegating failingStore).
	// SQLite commit → ErrConflict (stale version=0).
	// DeleteKeys → errDeleteKeys (injected failure).
	// Expected: 500 XOLU-CM016.
	ts0 := time.Now().UTC().Truncate(time.Second)
	conflictBody := map[string]interface{}{
		"update": map[string]interface{}{
			"entity":  "order",
			"id":      1,
			"version": 0, // stale
			"data":    map[string]interface{}{"state": "should-not-stick"},
		},
		"timeseries": []interface{}{
			map[string]interface{}{
				"timeline": 1,
				"dims":     []interface{}{1},
				"time":     ts0.Format(time.RFC3339Nano),
				"nums":     []interface{}{42.0},
			},
		},
	}
	status, result = doJSONRequest(t, "POST", env.commitURL(tenant), conflictBody)
	if status != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %v", status, result)
	}
	assertErrorCode(t, result, "XOLU-CM016")

	// Entity state must be unchanged — SQLite never committed.
	getStatus, entity := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/tenant/%s/order/1", env.ts.URL, tenant), nil)
	if getStatus != http.StatusOK {
		t.Fatalf("GET order/1: got %d", getStatus)
	}
	state, _ := entity["state"].(string)
	if state == "should-not-stick" {
		t.Error("entity state advanced despite SQLite conflict")
	}
}
