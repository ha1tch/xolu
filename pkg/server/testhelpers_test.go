// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cache"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/validation"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"os"
)

// stdTestServer is the standard test server struct used by most test files.
// It bundles the server, httptest.Server, tmpDir, and store for cleanup.
type stdTestServer struct {
	srv    *server.Server
	ts     *httptest.Server
	store  storage.Store
	tmpDir string
	t      *testing.T
}

func (s *stdTestServer) cleanup() {
	s.ts.Close()
	// Stop() drains and closes cached per-tenant stores (the tenantStores
	// sync.Map). Without this, every test that touches a tenant leaks its
	// per-tenant SQLite connections, eventually exhausting handles and causing
	// unrelated tests to fail with "out of memory (14)" when they open a DB.
	if s.srv != nil {
		s.srv.Stop()
	}
	if s.store != nil {
		s.store.Close()
	}
	os.RemoveAll(s.tmpDir)
}

// baseStorePath returns the base store path for a config, mirroring exactly how
// the server resolves it in storeForTenant / startup: shared mode uses the
// shared store, per-file mode uses tenant 0's store. Tests must use this rather
// than hardcoding a path, so they cannot drift from the server's derivation.
func baseStorePath(cfg *config.Config) string {
	if cfg.SQLitePerFileTenants {
		return sl.TenantStorePath(cfg.BaseDir, 0)
	}
	return sl.SharedStorePath(cfg.BaseDir)
}

// newTestServerFromConfig builds a standard server from the given config.
// The config's BaseDir is expected to point into tmpDir; the store path is
// The cache TTL is derived from cfg.CacheTTL; the graph is a flat in-memory graph.
// Validator is JSON-schema-aware (reads from cfg.SchemaDir).
func newTestServerFromConfig(t *testing.T, cfg *config.Config) *stdTestServer {
	t.Helper()

	// Resolve the base store path from the data root by the invariant layout,
	// and create its directory (the SQLite store does not create parents).
	var dbPath string
	if cfg.SQLitePerFileTenants {
		dbPath = sl.TenantStorePath(cfg.BaseDir, 0)
	} else {
		dbPath = sl.SharedStorePath(cfg.BaseDir)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("newTestServerFromConfig: mkdir: %v", err)
	}

	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": dbPath})
	if err != nil {
		t.Fatalf("newTestServerFromConfig: storage: %v", err)
	}

	ttl := time.Duration(cfg.CacheTTL) * time.Second
	if ttl == 0 {
		ttl = 300 * time.Second
	}
	memCache := cache.NewMemoryCache(1000, ttl)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	_ = log.Logger // suppress unused import
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())

	return &stdTestServer{srv: srv, ts: ts, store: store, tmpDir: cfg.BaseDir, t: t}
}
